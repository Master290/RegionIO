package world

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestDocumentationNamesExist guards the two ways the prose drifts away from the
// code, both of which happened during this session and neither of which any other
// test can see.
//
// A backticked `TestFoo` in a markdown file is a promise that a reader can go and
// run. When the test is renamed or was never written, the sentence keeps reading as
// evidence - which is worse than no citation, because the notes' own rule is that a
// claim backed by a test name is a claim that has been measured. STRUCTURE_NOTES.md
// carried a reference to `TestMossReplaceableIsBlockScoped`, a test that does not
// exist, three pages below the test that does.
//
// The second class is encoding: the docs went through a cp1251 round trip at some
// point and gained "â€”" where an em dash was intended. Those bytes are invisible
// in a terminal that renders them back as the character, so the corruption read as
// clean. 34 occurrences were repaired by hand; this stops new ones arriving.
//
// Only backticked names are checked, because "Testing" in a heading is not a
// citation, and only the docs a human reads, because a note in a generated file has
// no reader to mislead.
func TestDocumentationNamesExist(t *testing.T) {
	root := repoRoot(t)
	defined := definedTestNames(t, root)

	var refs, mojibake []string
	var files, checked int
	walkDocs(t, root, func(path, text string) {
		files++
		names := backtickedTestNames(text)
		checked += len(names)
		for _, name := range names {
			if _, ok := defined[name]; !ok {
				refs = append(refs, filepath.ToSlash(path)+": `"+name+"`")
			}
		}
		for _, line := range strings.Split(text, "\n") {
			if bad := mojibakeIn(line); bad != "" {
				mojibake = append(mojibake, filepath.ToSlash(path)+": "+bad+" in "+strings.TrimSpace(line))
			}
		}
	})

	// A guard that reads nothing reports the same clean result as a repo with
	// nothing to fix, so the coverage numbers are asserted rather than logged. If a
	// document moves out of docGlobs this fails instead of quietly stopping.
	if files < 3 {
		t.Errorf("walkDocs found %d markdown files under %s; docGlobs = %v no longer matches where the documentation lives", files, root, docGlobs)
	}
	if checked < 10 {
		t.Errorf("only %d backticked Test names were read across %d files, so the reference check is nearly inert; the notes cite test names by the dozen", checked, files)
	}

	sort.Strings(refs)
	sort.Strings(mojibake)
	for _, line := range refs {
		t.Errorf("documentation cites a test that is not defined anywhere in the repo: %s", line)
	}
	for _, line := range mojibake {
		t.Errorf("documentation carries a mojibake signature (a character that was decoded through cp1251 or cp1252 on the way in): %s", line)
	}
	t.Logf("%d files, %d test names defined, %d doc references read, %d unresolved, %d mojibake lines",
		files, len(defined), checked, len(refs), len(mojibake))
}

// repoRoot walks up from the package directory looking for go.mod rather than
// assuming a fixed number of "..", so the test survives a package being moved.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}

var docGlobs = []string{"*.md", "internal/*/*.md", "docs/*.md"}

func walkDocs(t *testing.T, root string, visit func(path, text string)) {
	t.Helper()
	for _, glob := range docGlobs {
		paths, err := filepath.Glob(filepath.Join(root, glob))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatal(err)
			}
			visit(rel, string(data))
		}
	}
}

var backtickRe = regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_.-]*)`")

// mojibakeIn returns the offending pair or rune if line carries the signature of
// text that was decoded with the wrong single-byte codec on the way in, or "" if it
// is clean.
//
// The three rules cover what this repo actually accumulated and the codecs that
// produced it. A UTF-8 em dash (E2 80 94) read as cp1251 becomes "вЂ”" and read as
// cp1252 becomes "â€”", so a Cyrillic letter or a U+00E2 sitting directly against a
// punctuation or currency mark is not prose. Separately, several of the 0x80-0x8F
// bytes map to historic Cyrillic letters (Ђ ѓ Љ Њ Ќ Ћ Џ) that occur in no text a
// server core writes about, so those are flagged on sight.
//
// An ordinary em dash is safe because it arrives with spaces on both sides.
func mojibakeIn(line string) string {
	runes := []rune(line)
	for i, r := range runes {
		if r >= 0x0402 && r <= 0x040F {
			return string(r)
		}
		if i+1 >= len(runes) {
			continue
		}
		next := runes[i+1]
		switch {
		case r >= 0x0400 && r <= 0x04FF && inRunes(next, 0x2000, 0x206F):
			return string(r) + string(next)
		// The upper bound matters: the Euro sign that cp1252 gives 0x80 is U+20AC,
		// in the Currency block *above* General Punctuation, so a range ending at
		// 0x206F lets "â€”" through. It did, on the first version of this test.
		case r == 0x00E2 && (inRunes(next, 0x0080, 0x00BF) || inRunes(next, 0x2000, 0x20BF)):
			return string(r) + string(next)
		}
	}
	return ""
}

func inRunes(r, lo, hi rune) bool { return r >= lo && r <= hi }

// TestMojibakeDetectorIsNotInert pins the detector against strings it must catch and
// must not. A guard whose pattern matches nothing is indistinguishable from a clean
// repo, which is how the first disk-state guard passed on broken code.
func TestMojibakeDetectorIsNotInert(t *testing.T) {
	for _, bad := range []string{
		"parity вЂ” 99.916%",   // an em dash read as cp1251: E2->в 80->Ђ 94->”
		"order в†‘ the target", // an arrow read as cp1251: E2->в 86->† 92->‘
		"parity â€” 99.916%",   // an em dash read as cp1252: E2->â 80->€ 94->"
	} {
		if got := mojibakeIn(bad); got == "" {
			t.Errorf("mojibakeIn(%q) = %q, want a signature", bad, got)
		}
	}
	for _, good := range []string{
		"parity — 99.916%",       // the dash we actually write
		"the target-first order", // ASCII hyphen inside a word
		"текст — spawn",          // Cyrillic with a spaced dash
	} {
		if got := mojibakeIn(good); got != "" {
			t.Errorf("mojibakeIn(%q) = %q, want clean", good, got)
		}
	}
}

// backtickedTestNames returns the bare identifiers, dropping anything with a dot or
// a dash, which is a file name or a flag rather than a Go function.
func backtickedTestNames(text string) []string {
	var out []string
	for _, m := range backtickRe.FindAllStringSubmatch(text, -1) {
		name := m[1]
		if !strings.HasPrefix(name, "Test") || strings.ContainsAny(name, ".-") {
			continue
		}
		if len(name) < 5 || name[4] < 'A' || name[4] > 'Z' {
			continue // "Test", "Testing", "tests"
		}
		out = append(out, name)
	}
	return out
}

// definedTestNames parses the repo's test files instead of asking `go test -list`,
// so the check does not compile or run the suite to know what the suite contains.
func definedTestNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	defined := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			switch d.Name() {
			case ".git", "versions", "node_modules", ".refjava", "testdata":
				return fs.SkipDir
			}
			return nil
		case !strings.HasSuffix(path, "_test.go"):
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range regexp.MustCompile(`func (Test[A-Za-z0-9_]*)\(`).FindAllStringSubmatch(string(data), -1) {
			defined[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return defined
}
