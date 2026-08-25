package world

import (
	"testing"

	"regionio/internal/worldgen"
)

func TestDiskStateProviderRules(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}

	makeRegion := func(block uint16) *decorationRegion {
		chunk := NewChunk(0, 0, BiomePlains)
		chunk.SetBlock(8, 0, 8, block)
		region, err := newDecorationRegion([]*Chunk{chunk})
		if err != nil {
			t.Fatal(err)
		}
		if err := region.setSource(0, 0); err != nil {
			t.Fatal(err)
		}
		return region
	}

	dirt, ok := nameToStateID("minecraft:dirt", nil)
	if !ok {
		t.Fatal("dirt state missing")
	}

	sandConfig, err := set.Disk("minecraft:disk_sand")
	if err != nil {
		t.Fatal(err)
	}
	sandConfig.RadiusMin, sandConfig.RadiusMax, sandConfig.HalfHeight = 0, 0, 0
	region := makeRegion(dirt)
	if err := region.placeDisk(set, worldgen.NewWorldgenRandom(1), worldgen.FeaturePosition{X: 8, Y: 0, Z: 8}, sandConfig); err != nil {
		t.Fatal(err)
	}
	sandstone, ok := nameToStateID("minecraft:sandstone", nil)
	if !ok || region.getBlock(8, 0, 8) != sandstone {
		t.Fatalf("sand rule state = %d, want sandstone %d", region.getBlock(8, 0, 8), sandstone)
	}

	grassConfig, err := set.Disk("minecraft:disk_grass")
	if err != nil {
		t.Fatal(err)
	}
	grassConfig.RadiusMin, grassConfig.RadiusMax, grassConfig.HalfHeight = 0, 0, 0
	region = makeRegion(dirt)
	if err := region.placeDisk(set, worldgen.NewWorldgenRandom(1), worldgen.FeaturePosition{X: 8, Y: 0, Z: 8}, grassConfig); err != nil {
		t.Fatal(err)
	}
	grass, ok := nameToStateID("minecraft:grass_block", map[string]string{"snowy": "false"})
	if !ok || region.getBlock(8, 0, 8) != grass {
		t.Fatalf("grass rule state = %d, want grass %d", region.getBlock(8, 0, 8), grass)
	}
}

func TestDiskStateProviderFallback(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	config, err := set.Disk("minecraft:disk_grass")
	if err != nil {
		t.Fatal(err)
	}
	config.RadiusMin, config.RadiusMax, config.HalfHeight = 0, 0, 0

	dirt, _ := nameToStateID("minecraft:dirt", nil)
	stone, _ := nameToStateID("minecraft:stone", nil)
	chunk := NewChunk(0, 0, BiomePlains)
	chunk.SetBlock(8, 0, 8, dirt)
	chunk.SetBlock(8, 1, 8, stone)
	region, err := newDecorationRegion([]*Chunk{chunk})
	if err != nil {
		t.Fatal(err)
	}
	if err := region.setSource(0, 0); err != nil {
		t.Fatal(err)
	}
	if err := region.placeDisk(set, worldgen.NewWorldgenRandom(1), worldgen.FeaturePosition{X: 8, Y: 0, Z: 8}, config); err != nil {
		t.Fatal(err)
	}
	if got := region.getBlock(8, 0, 8); got != dirt {
		t.Fatalf("fallback state = %d, want dirt %d", got, dirt)
	}
}
