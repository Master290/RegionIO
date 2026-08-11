package world

import (
	"encoding/json"
	"testing"

	"regionio/internal/worldgen"
)

func TestDecorationRegionBlockPredicates(t *testing.T) {
	chunk := NewChunk(0, 0, BiomePlains)
	chunk.SetBlock(4, 20, 4, StateStone)
	chunk.SetBlock(5, 20, 4, StateWater)
	region, err := newDecorationRegion([]*Chunk{chunk})
	if err != nil {
		t.Fatal(err)
	}
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	position := worldgen.FeaturePosition{X: 4, Y: 21, Z: 4}
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{"block offset", `{"type":"minecraft:matching_blocks","blocks":"minecraft:stone","offset":[0,-1,0]}`, true},
		{"block list", `{"type":"minecraft:matching_blocks","blocks":["minecraft:dirt","minecraft:stone"],"offset":[0,-1,0]}`, true},
		{"air tag", `{"type":"minecraft:matching_block_tag","tag":"minecraft:air"}`, true},
		{"water", `{"type":"minecraft:matching_fluids","fluids":["minecraft:water","minecraft:flowing_water"],"offset":[1,-1,0]}`, true},
		{"all", `{"type":"minecraft:all_of","predicates":[{"type":"minecraft:inside_world_bounds"},{"type":"minecraft:matching_block_tag","tag":"minecraft:air"}]}`, true},
		{"any", `{"type":"minecraft:any_of","predicates":[{"type":"minecraft:matching_blocks","blocks":"minecraft:dirt"},{"type":"minecraft:matching_block_tag","tag":"minecraft:air"}]}`, true},
		{"not", `{"type":"minecraft:not","predicate":{"type":"minecraft:matching_blocks","blocks":"minecraft:stone"}}`, true},
		{"outside", `{"type":"minecraft:inside_world_bounds","offset":[0,-16,0]}`, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := region.testBlockPredicate(set, json.RawMessage(test.raw), position)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("predicate = %v, want %v", got, test.want)
			}
		})
	}
}

func TestDecorationRegionRejectsUnsupportedPredicate(t *testing.T) {
	region, err := newDecorationRegion([]*Chunk{NewChunk(0, 0, BiomePlains)})
	if err != nil {
		t.Fatal(err)
	}
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := region.testBlockPredicate(set, json.RawMessage(`{"type":"minecraft:would_survive","state":{"Name":"minecraft:oak_sapling"}}`), worldgen.FeaturePosition{}); err == nil {
		t.Fatal("unsupported would_survive succeeded")
	}
}
