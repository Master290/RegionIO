package world

import (
	"reflect"
	"testing"
)

func TestDecorationRegionUsesAbsoluteNegativeCoordinates(t *testing.T) {
	chunk := NewChunk(-1, -1, BiomePlains)
	chunk.SetBlock(15, 12, 15, StateStone)
	region, err := newDecorationRegion([]*Chunk{chunk})
	if err != nil {
		t.Fatal(err)
	}
	if got := region.getBlock(-1, 12, -1); got != StateStone {
		t.Fatalf("block (-1,12,-1) = %d, want stone", got)
	}
	if biome, ok := region.getBiome(-1, 12, -1); !ok || biome != BiomePlains {
		t.Fatalf("biome = %d, %v", biome, ok)
	}
}

func TestDecorationRegionEnforcesSourceWriteRadius(t *testing.T) {
	var chunks []*Chunk
	for cx := int32(-2); cx <= 2; cx++ {
		chunks = append(chunks, NewChunk(cx, 0, BiomePlains))
	}
	region, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if err := region.setSource(0, 0); err != nil {
		t.Fatal(err)
	}
	if !region.setBlock(-1, 20, 0, StateStone) || !region.setBlock(31, 20, 0, StateDirt) {
		t.Fatal("write inside source radius rejected")
	}
	if region.setBlock(-17, 20, 0, StateStone) || region.setBlock(32, 20, 0, StateStone) {
		t.Fatal("write outside source radius accepted")
	}
	if got := region.getBlock(31, 20, 0); got != StateDirt {
		t.Fatalf("shared region mutation = %d, want dirt", got)
	}
}

func TestDecorationRegionHeightmapsMatchPlacementSemantics(t *testing.T) {
	chunk := NewChunk(0, 0, BiomePlains)
	chunk.SetBlock(2, 30, 3, StateStone)
	chunk.SetBlock(2, 31, 3, StateWater)
	region, err := newDecorationRegion([]*Chunk{chunk})
	if err != nil {
		t.Fatal(err)
	}
	if got := region.heightAt("WORLD_SURFACE", 2, 3); got != 32 {
		t.Fatalf("WORLD_SURFACE = %d, want 32", got)
	}
	if got := region.heightAt("OCEAN_FLOOR", 2, 3); got != 31 {
		t.Fatalf("OCEAN_FLOOR = %d, want 31", got)
	}
	if got := region.heightAt("MOTION_BLOCKING", 2, 3); got != 32 {
		t.Fatalf("MOTION_BLOCKING = %d, want 32", got)
	}
	if got := region.heightAt("WORLD_SURFACE", 4, 5); got != MinY {
		t.Fatalf("empty height = %d, want %d", got, MinY)
	}
}

func TestDecorationRegionSourceBiomesUseThreeByThreeChunks(t *testing.T) {
	var chunks []*Chunk
	for cx := int32(-1); cx <= 1; cx++ {
		for cz := int32(-1); cz <= 1; cz++ {
			chunks = append(chunks, NewChunk(cx, cz, BiomePlains))
		}
	}
	chunks = append(chunks, NewChunk(2, 0, biomeIDByName("minecraft:forest")))
	region, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if err := region.setSource(0, 0); err != nil {
		t.Fatal(err)
	}
	got := region.sourceBiomes()
	want := []string{"minecraft:plains"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("source biomes = %v, want %v", got, want)
	}
}

func TestDecorationRegionSchedulesRealFeatureStage(t *testing.T) {
	var chunks []*Chunk
	for cx := int32(-1); cx <= 1; cx++ {
		for cz := int32(-1); cz <= 1; cz++ {
			chunks = append(chunks, NewChunk(cx, cz, BiomePlains))
		}
	}
	region, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if err := region.setSource(0, 0); err != nil {
		t.Fatal(err)
	}
	features, err := region.scheduledFeatures(undergroundOresStage)
	if err != nil {
		t.Fatal(err)
	}
	if len(features) == 0 {
		t.Fatal("plains underground stage produced no scheduled features")
	}
	for i, feature := range features {
		if i > 0 && feature.Index <= features[i-1].Index {
			t.Fatalf("feature %s index %d is not after index %d", feature.Name, feature.Index, features[i-1].Index)
		}
	}
}

func TestDecorationRegionRequiresSourceBiomeNeighborhood(t *testing.T) {
	region, err := newDecorationRegion([]*Chunk{NewChunk(0, 0, BiomePlains)})
	if err != nil {
		t.Fatal(err)
	}
	if err := region.setSource(0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := region.scheduledFeatures(undergroundOresStage); err == nil {
		t.Fatal("missing source neighborhood succeeded")
	}
}
