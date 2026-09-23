package world

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestEveryDiagnosticGateIsReachable keeps "the diagnostics run unattended" from
// becoming a claim nobody can check.
//
// Step 4 of the clay investigation promoted a pile of ad-hoc printfs to gated
// tests, and the rule it established was that a mode nothing runs rots - the
// probe's private copy of the dispatch loop had already drifted exactly that way.
// But promoting a diagnostic to a gate is only half of it: the gate has to be set
// by something, or the code behind it is unreachable and the rot is silent again.
//
// Two ways that happened while the rule was supposedly in force. The consolidated
// trace flag reads REGIONIO_LUSH_CLAY_TRACE, while the Makefile and CI have been
// setting REGIONIO_CLAY_TRACE, which is a different flag gating a different layer -
// so every `lushClayTrace()` print inside the generator has been dead in every CI
// run. And six ore diagnostics in region_ore_parity_test.go are gated on names no
// runner sets at all.
//
// So this is derived rather than listed: the gate names come out of the source, the
// names the automation sets come out of the Makefile and the workflow, and anything
// in the first set that is not in the second must be excused in writing below. An
// allowlist with a reason per entry is the point - an unexplained skip is the same
// bug with a comment on it.
func TestEveryDiagnosticGateIsReachable(t *testing.T) {
	root := repoRoot(t)

	gates := gateNames(t, root)
	set := automationEnvNames(t, root)

	var unreachable []string
	for name := range gates {
		if set[name] {
			continue
		}
		if reason, ok := excusedGates[name]; ok {
			t.Logf("%s is not run by automation: %s", name, reason)
			continue
		}
		unreachable = append(unreachable, name)
	}
	sort.Strings(unreachable)
	for _, name := range unreachable {
		t.Errorf("%s gates a diagnostic that no runner ever sets: it is unreachable code that reads like a test. Set it in the Makefile diagnostics target and the CI diagnostics job, or add it to excusedGates with a reason.", name)
	}

	// The mirror image: a runner that sets a name nothing reads is how
	// REGIONIO_LUSH_CLAY_DIFF survived as a dead gate after its test stopped
	// checking it. Both halves of the pair are asserted because both drifted.
	var stale []string
	for name := range set {
		if _, ok := gates[name]; ok {
			continue
		}
		if _, ok := excusedGates[name]; ok {
			continue
		}
		stale = append(stale, name)
	}
	sort.Strings(stale)
	for _, name := range stale {
		t.Errorf("the Makefile or CI sets %s, which no Go source reads any more; either the gate was renamed or the diagnostic behind it was deleted", name)
	}

	t.Logf("%d gate names in source, %d set by automation, %d unreachable, %d stale",
		len(gates), len(set), len(unreachable), len(stale))
	if len(gates) < 10 {
		t.Errorf("only %d gate names were read from source, which means the scan is inert rather than the repo clean", len(gates))
	}
}

// excusedGates is every name that is deliberately not driven by the diagnostics
// job, with why. Adding to this list is the cheapest way to make this test pass,
// so each entry has to be true of the named variable, not of the test that reads
// it.
var excusedGates = map[string]string{
	// Strictness and mode switches, exercised by the parity job or by a
	// developer comparing two generator versions, not by the diagnostics job.
	"REGIONIO_REQUIRE_PARITY":                "a failure-mode switch, not a diagnostic: the parity job deliberately omits it because exact equality is unattainable while the cascade cells remain",
	"REGIONIO_REQUIRE_BASE_PARITY":           "a failure-mode switch on the base-terrain fixture, same reason",
	"REGIONIO_PARITY_GENERATOR":              "selects the legacy generator to compare against; running both versions unattended doubles the suite for no new information",
	"REGIONIO_LUSH_CLAY_PROBE_EXTRA_SOURCES": "a knob of the task-8 experiment, replaying sources outside the 3x3; the always-on position table asserts what it showed",
	"REGIONIO_LUSH_CLAY_PROBE_CLAY_IN":       "an ad-hoc census knob of the same probe, whose permanent form is the clay ratchet in TestVanillaLushClayDiff",
	"REGIONIO_LUSH_CLAY_PROBE_SKIP":          "a causal mode of the gated probe, set by the Makefile and CI under the names listed there",
	// Not a diagnostic that nothing runs: the sweep it filters is run by the
	// diagnostics job in full, and this name only narrows which arms a person
	// re-measures by hand, so setting it in automation would ask CI to do the
	// same work less completely than it already does.
	"REGIONIO_DECORATION_ORDER_ARM": "an arm filter for TestDecorationSourceOrderParity, whose enabling gate REGIONIO_DECORATION_ORDER_DIAGNOSTIC is set by the Makefile and CI; the job runs every arm, so naming one in automation would only run a subset",
	// Fixture paths and capture parameters: a tool flag or an optional fixture
	// location rather than a diagnostic gate.
	"REGIONIO_BASE_CAPTURE":     "path to an optional base-terrain capture; the test that reads it skips when the file is absent",
	"REGIONIO_RUIN_CAPTURE":     "path to an optional ruined-portal capture, same",
	"REGIONIO_SEED":             "the world seed for a capture, not a diagnostic gate",
	"REGIONIO_LUSH_CLAY_COLUMN": "column list handed to the trace by the Makefile's probe step",
}

// gateNameRe matches any REGIONIO_* name written as a Go string literal.
//
// The first version matched only `os.Getenv("X")` and `requireDiagnostic(t, "X")`,
// which made the guard inert for every knob read through a helper - the probe's
// TARGET, SOURCE, EXTRA_SOURCES and CLAY_IN are all read as
// `probeChunk(t, "REGIONIO_...")`, and a scan that cannot see them reports a clean
// repo. A literal is the widest honest signal: any name the source can mention is a
// name a runner has to set or a reader has to be told why not.
var gateNameRe = regexp.MustCompile(`"(REGIONIO_[A-Z0-9_]+)"`)

// gateNames reads the source rather than a list, because the failure being guarded
// is a list that stopped matching the source.
func gateNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range gateNameRe.FindAllStringSubmatch(string(data), -1) {
				out[m[1]] = true
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

var envSetRe = regexp.MustCompile(`(REGIONIO_[A-Z0-9_]+)\s*[:=]`)

// automationEnvNames collects every name the Makefile or the CI workflow assigns a
// value to, including the names set on a line continuation.
func automationEnvNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, rel := range []string{"Makefile", filepath.Join(".github", "workflows", "verify.yml")} {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for _, m := range envSetRe.FindAllStringSubmatch(string(data), -1) {
			out[m[1]] = true
		}
	}
	return out
}
