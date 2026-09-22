package world

import (
	"encoding/json"
	"strings"
	"testing"

	"regionio/internal/worldgen"
)

// TestRuleBasedStateProviderFollowsTheTag is the behavioural half of the
// below_trunk_provider port. The old hand-written tree path wrote minecraft:dirt
// unconditionally under a trunk, which differs from vanilla exactly where the block
// already there is in cannot_replace_below_tree_trunk - a set of soft blocks (the
// dirt family, the mud family, the moss blocks, podzol) that vanilla leaves alone.
//
// Both branches are asserted because a provider that silently fell through to the
// "keep what is there" case would satisfy the moss expectation while quietly
// failing the stone one, and vice versa.
func TestRuleBasedStateProviderFollowsTheTag(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	config, err := set.Tree("minecraft:oak_bees_005")
	if err != nil {
		t.Fatalf("oak_bees_005: %v", err)
	}
	spec, err := set.StateProvider(config.BelowTrunkProvider)
	if err != nil {
		t.Fatalf("below_trunk_provider: %v", err)
	}
	if spec.Type != "rule_based" {
		t.Fatalf("parsed as %q, want rule_based", spec.Type)
	}
	if spec.Fallback != nil {
		t.Error("this config declares no fallback, so the parser must not invent one")
	}

	random, _ := worldgen.DecorationRandom(12345, 0, 0)
	tests := []struct {
		name  string
		below uint16
		want  uint16
	}{
		{"stone is replaceable, becomes dirt", StateStone, mustState("minecraft:dirt", nil)},
		{"moss_block is in the tag and stays moss_block", mustState("minecraft:moss_block", nil), mustState("minecraft:moss_block", nil)},
		{"podzol is in the tag and stays podzol", mustState("minecraft:podzol", nil), mustState("minecraft:podzol", nil)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chunk := NewChunk(0, 0, BiomePlains)
			chunk.SetBlock(6, 30, 6, tc.below)
			region, err := newDecorationRegion([]*Chunk{chunk})
			if err != nil {
				t.Fatal(err)
			}
			got, err := region.sampleStateProvider(set, spec, random, worldgen.FeaturePosition{X: 6, Y: 30, Z: 6})
			if err != nil {
				t.Fatalf("sampleStateProvider: %v", err)
			}
			if got != tc.want {
				t.Errorf("below %s got %s, want %s", stateLabel(tc.below), stateLabel(got), stateLabel(tc.want))
			}
		})
	}
}

// TestEveryTreeBelowTrunkProviderParses covers all 39 configs rather than the one
// the behaviour test uses, because the pack mixes rule_based (38) with a single
// simple_state_provider, and a parser that handled only the common shape would
// pass the test above and fail on that one config at generation time.
func TestEveryTreeBelowTrunkProviderParses(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	parsed, byType := 0, map[string]int{}
	for name, configured := range set.Configured {
		if configured.Type != "minecraft:tree" || strings.HasPrefix(name, "inline:") {
			continue
		}
		config, err := set.Tree(name)
		if err != nil {
			t.Errorf("Tree(%q): %v", name, err)
			continue
		}
		if len(config.BelowTrunkProvider) == 0 {
			t.Errorf("%q declares no below_trunk_provider", name)
			continue
		}
		spec, err := set.StateProvider(config.BelowTrunkProvider)
		if err != nil {
			t.Errorf("%q below_trunk_provider: %v", name, err)
			continue
		}
		byType[spec.Type]++
		parsed++
		// Nested rule bodies must parse too: a provider that only decodes at the
		// top level is a parser that has not read the shape.
		for _, rule := range spec.Rules {
			if _, err := set.StateProvider(rule.Then); err != nil {
				t.Errorf("%q rule body: %v", name, err)
			}
			var probe struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(rule.Predicate, &probe); err != nil || probe.Type == "" {
				t.Errorf("%q rule predicate is unreadable: %v", name, err)
			}
		}
	}
	if parsed == 0 {
		t.Fatal("no tree configs were checked, so this test is inert")
	}
	if parsed < 39 {
		t.Errorf("checked %d tree configs, expected the whole pack's 39", parsed)
	}
	t.Logf("%d below_trunk providers parsed, by type: %v", parsed, byType)
}

// TestRuleBasedProviderRejectsUnusableShapes keeps the parser from accepting a
// provider it cannot evaluate.
func TestRuleBasedProviderRejectsUnusableShapes(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"type":"minecraft:rule_based_state_provider","rules":[]}`,
		`{"type":"minecraft:rule_based_state_provider","rules":[{"then":{"type":"minecraft:simple_state_provider","state":{"Name":"minecraft:dirt"}}}]}`,
		`{"type":"minecraft:rule_based_state_provider"}`,
	} {
		if _, err := set.StateProvider(json.RawMessage(raw)); err == nil {
			t.Errorf("accepted an unusable rule_based provider: %s", raw)
		}
	}
}
