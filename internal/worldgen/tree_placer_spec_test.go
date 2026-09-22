package worldgen

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// TestTreePlacerSpecMatchesThePack re-derives trunkPlacerSpec and
// foliagePlacerSpec from the embedded datapack and fails if the tables in
// features.go disagree with it.
//
// The tables are the third attempt at this. The first listed placer types from
// memory and invented minecraft:large_oak_foliage_placer while omitting acacia,
// bush, jungle and mega_jungle; the second listed their fields from memory and
// gave spruce a `height` it does not have while missing its `trunk_height`
// entirely. Both errors survived compilation and both were invisible until
// something measured the data. So the measurement is the test now, not a comment
// claiming the lists were checked.
//
// It asserts three things, because they fail differently: an unknown type name, a
// field set that differs from the data, and a config that Tree() refuses to
// decode at all. The last one is the one that mattered most - before this, pine
// and spruce failed to decode and mega_pine decoded with a silently zeroed
// radius, and neither failure stopped the generator from running.
func TestTreePlacerSpecMatchesThePack(t *testing.T) {
	set, err := LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for name, configured := range set.Configured {
		if configured.Type == "minecraft:tree" && !strings.HasPrefix(name, "inline:") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatal("no configured tree features found; the scan is inert")
	}

	observedTrunk := map[string]map[string]bool{}
	observedFoliage := map[string]map[string]bool{}
	decoded := 0
	for _, name := range names {
		config, err := set.Tree(name)
		if err != nil {
			t.Errorf("Tree(%q): %v", name, err)
			continue
		}
		decoded++
		// The rule is "a recognised supplier type, and an actual state only for the
		// simple form" - not "State.Name is always set", which is the assumption
		// that made azalea_tree's weighted foliage provider look broken.
		for slot, p := range map[string]TreeBlockProvider{
			"trunk_provider":   config.TrunkProvider,
			"foliage_provider": config.FoliageProvider,
		} {
			if !knownStateProviders[p.Type] {
				t.Errorf("Tree(%q): %s has unmodelled type %q", name, slot, p.Type)
			}
			if p.Type == "minecraft:simple_state_provider" && p.State.Name == "" {
				t.Errorf("Tree(%q): %s is simple_state_provider without a state", name, slot)
			}
			if len(p.Raw) == 0 {
				t.Errorf("Tree(%q): %s kept no raw form, so a consumer cannot read its entries or rules", name, slot)
			}
		}
		if config.TrunkPlacer.Type == "" || len(config.TrunkPlacer.Fields) == 0 {
			t.Errorf("Tree(%q): trunk placer carries no type or no fields", name)
		}
		observedTrunk[config.TrunkPlacer.Type] = fieldSet(config.TrunkPlacer.Fields)
		observedFoliage[config.FoliagePlacer.Type] = fieldSet(config.FoliagePlacer.Fields)
	}
	if decoded != len(names) {
		t.Errorf("only %d of %d configured tree features decode", decoded, len(names))
	}

	checkTable(t, "trunk_placer", trunkPlacerSpec, observedTrunk)
	checkTable(t, "foliage_placer", foliagePlacerSpec, observedFoliage)
	t.Logf("%d tree configs, %d trunk types, %d foliage types, all tables agree",
		len(names), len(trunkPlacerSpec), len(foliagePlacerSpec))
}

// checkTable compares a spec table against what the pack holds, in both directions:
// a type the data has and the table lacks is a missing model, and a type the table
// has and the data lacks is an invented one. Both were made here once.
func checkTable(t *testing.T, kind string, spec map[string][]string, observed map[string]map[string]bool) {
	t.Helper()
	for typ, fields := range observed {
		want, ok := spec[typ]
		if !ok {
			t.Errorf("%s type %q is in the pack but not modelled in %sSpec", kind, typ, kind)
			continue
		}
		got := make([]string, 0, len(fields))
		for f := range fields {
			got = append(got, f)
		}
		sort.Strings(got)
		sorted := append([]string(nil), want...)
		sort.Strings(sorted)
		if strings.Join(got, ",") != strings.Join(sorted, ",") {
			t.Errorf("%s %q: the pack holds fields %v, the table claims %v", kind, typ, got, sorted)
		}
	}
	for typ := range spec {
		if _, ok := observed[typ]; !ok {
			t.Errorf("%s type %q is in the table but appears in no configured feature of this pack", kind, typ)
		}
	}
}

func fieldSet(fields map[string]json.RawMessage) map[string]bool {
	out := make(map[string]bool, len(fields))
	for f := range fields {
		out[f] = true
	}
	return out
}

// TestIntProvidersCoverEveryFormInThePack pins the shapes parseNestedIntProvider
// accepts. The form that motivated it is written without a discriminator at all -
// cherry_trunk_placer's branch_start_offset_from_top is a bare
// {"min_inclusive": -4, "max_inclusive": -3} - and a parser that silently rejects
// or zero-fills an unknown object is indistinguishable from one that read it.
func TestIntProvidersCoverEveryFormInThePack(t *testing.T) {
	set, err := LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	var objects, rejected int
	for name, configured := range set.Configured {
		if configured.Type != "minecraft:tree" {
			continue
		}
		config, err := set.Tree(name)
		if err != nil {
			continue
		}
		for _, fields := range []map[string]json.RawMessage{config.TrunkPlacer.Fields, config.FoliagePlacer.Fields} {
			for _, raw := range fields {
				trimmed := strings.TrimSpace(string(raw))
				if !strings.HasPrefix(trimmed, "{") {
					continue
				}
				objects++
				if _, err := parseNestedIntProvider(raw); err != nil {
					rejected++
					t.Errorf("field of %s is an object no provider parser accepts: %v", name, err)
				}
			}
		}
	}
	if objects == 0 {
		t.Fatal("no object-shaped int providers were found, so this check proves nothing")
	}
	t.Logf("%d object-shaped provider fields, %d unreadable", objects, rejected)
}
