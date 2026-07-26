package worldgen

import (
	"math/rand"
	"testing"
)

// TestLoadSurfaceRule confirms the embedded overworld surface_rule parses into
// a rule tree without error. This guards the parser against any rule/condition
// type the overworld uses.
func TestLoadSurfaceRule(t *testing.T) {
	rule, err := LoadOverworldSurfaceRule()
	if err != nil {
		t.Fatalf("LoadOverworldSurfaceRule: %v", err)
	}
	if rule == nil {
		t.Fatal("nil surface rule")
	}
}

// TestSurfaceRuleNoPanic runs the full rule tree across a range of Y values and
// several biomes to confirm Apply never panics on real-world inputs. A panic
// during generation would crash the server.
func TestSurfaceRuleNoPanic(t *testing.T) {
	rule, err := LoadOverworldSurfaceRule()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	biomes := []string{
		"minecraft:plains", "minecraft:desert", "minecraft:forest",
		"minecraft:badlands", "minecraft:snowy_plains", "minecraft:ocean",
		"minecraft:mushroom_fields", "minecraft:wooded_badlands",
	}
	for _, b := range biomes {
		for y := 0; y < 100; y++ {
			ctx := &SurfaceContext{
				X: 100, Y: y, Z: 100,
				StoneDepthAbove: 100 - y, StoneDepthBelow: y + 1,
				SeaLevel: 63, BiomeName: b, MinY: -64,
				PreliminarySurface: 100, WaterHeight: NoWaterAbove,
				Rng: rand.New(rand.NewSource(1)),
			}
			rule.Apply(ctx) // must not panic
		}
	}
}

// TestSurfaceBedrockFloor confirms the bottom of the world resolves to bedrock
// (the vertical_gradient bedrock_floor rule is the first rule in the tree).
func TestSurfaceBedrockFloor(t *testing.T) {
	rule, err := LoadOverworldSurfaceRule()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ctx := &SurfaceContext{
		X: 0, Y: -64, Z: 0, StoneDepthAbove: 1, StoneDepthBelow: 1,
		SeaLevel: 63, BiomeName: "minecraft:plains", MinY: -64,
		PreliminarySurface: 70, WaterHeight: NoWaterAbove,
		Rng: rand.New(rand.NewSource(1)),
	}
	state, ok := rule.Apply(ctx)
	if !ok {
		t.Fatal("no rule matched at bedrock floor")
	}
	if state != 85 { // bedrock
		t.Errorf("bedrock floor state = %d, want 85", state)
	}
}

// TestSurfaceBlockIDResolution checks the block-ID table covers the blocks the
// overworld surface_rule references, including snowy property variants.
func TestSurfaceBlockIDResolution(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]string
		want  uint16
	}{
		{"minecraft:bedrock", nil, 85},
		{"minecraft:grass_block", nil, 9},
		{"minecraft:grass_block", map[string]string{"snowy": "true"}, 8},
		{"minecraft:mycelium", nil, 8919},
		{"minecraft:podzol", map[string]string{"snowy": "true"}, 12},
		{"minecraft:terracotta", nil, 12912},
		{"minecraft:red_sand", nil, 123},
		{"minecraft:coarse_dirt", nil, 11},
		{"minecraft:calcite", nil, 24687},
	}
	for _, c := range cases {
		if got := surfaceBlockID(c.name, c.props); got != c.want {
			t.Errorf("surfaceBlockID(%q,%v) = %d, want %d", c.name, c.props, got, c.want)
		}
	}
}

// TestIsColdBiome confirms the snow-cover predicate recognises cold biomes so
// the temperature condition routes snowy biomes to snow.
func TestIsColdBiome(t *testing.T) {
	cold := []string{"minecraft:snowy_plains", "minecraft:frozen_peaks", "minecraft:grove"}
	for _, b := range cold {
		if !isColdBiome(b) {
			t.Errorf("isColdBiome(%q) = false, want true", b)
		}
	}
	warm := []string{"minecraft:desert", "minecraft:plains", "minecraft:badlands"}
	for _, b := range warm {
		if isColdBiome(b) {
			t.Errorf("isColdBiome(%q) = true, want false", b)
		}
	}
}

// TestWaterCondition pins SurfaceRules.WaterConditionSource against the
// column's own water surface. The offsets are the three forms the overworld
// tree actually uses: (0,0,false) for "is this block dry", (-1,0,false) for the
// block just under the waterline, and (-6,-1,true) for the beach/shore band.
func TestWaterCondition(t *testing.T) {
	cases := []struct {
		name string
		test waterTest
		ctx  SurfaceContext
		want bool
	}{
		{
			name: "no water above passes",
			test: waterTest{},
			ctx:  SurfaceContext{Y: 20, WaterHeight: NoWaterAbove},
			want: true,
		},
		{
			name: "at the waterline passes",
			test: waterTest{},
			ctx:  SurfaceContext{Y: 63, WaterHeight: 63},
			want: true,
		},
		{
			name: "one block under water fails",
			test: waterTest{},
			ctx:  SurfaceContext{Y: 62, WaterHeight: 63},
			want: false,
		},
		{
			name: "offset -1 reaches one block deeper",
			test: waterTest{offset: -1},
			ctx:  SurfaceContext{Y: 62, WaterHeight: 63},
			want: true,
		},
		{
			name: "an aquifer pool at y=-20 is measured against itself, not sea level",
			test: waterTest{},
			ctx:  SurfaceContext{Y: -25, WaterHeight: -20, SeaLevel: 63},
			want: false,
		},
		{
			name: "stone above the same pool is dry",
			test: waterTest{},
			ctx:  SurfaceContext{Y: -19, WaterHeight: -20, SeaLevel: 63},
			want: true,
		},
		{
			name: "add_stone_depth counts buried stone towards the threshold",
			test: waterTest{offset: -6, surfaceDepthMul: -1, addStoneDepth: true},
			ctx:  SurfaceContext{Y: 55, WaterHeight: 63, StoneDepthAbove: 2, SurfaceDepth: 0},
			want: true,
		},
	}
	for _, c := range cases {
		if got := c.test.Test(&c.ctx); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
