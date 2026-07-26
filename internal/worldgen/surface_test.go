package worldgen

import "testing"

// loadTestRules compiles the overworld surface rule set at a fixed seed.
func loadTestRules(t *testing.T) *SurfaceRuleSet {
	t.Helper()
	od, err := LoadOverworldFinalDensity(12345)
	if err != nil {
		t.Fatalf("load overworld density: %v", err)
	}
	rules, err := od.SurfaceRule()
	if err != nil {
		t.Fatalf("compile surface rule: %v", err)
	}
	if rules == nil {
		t.Fatal("nil surface rule set")
	}
	return rules
}

// TestLoadSurfaceRule confirms the embedded overworld surface_rule parses into
// a rule tree without error, and that every noise its noise_threshold
// conditions name resolved. Six of the seven used to fall through as false.
func TestLoadSurfaceRule(t *testing.T) {
	rules := loadTestRules(t)
	if len(rules.noises) != 7 {
		t.Errorf("rule set references %d noises, want 7", len(rules.noises))
	}
}

// TestSurfaceRuleNoPanic runs the full rule tree across a range of Y values and
// several biomes to confirm Apply never panics on real-world inputs. A panic
// during generation would crash the server.
func TestSurfaceRuleNoPanic(t *testing.T) {
	rules := loadTestRules(t)
	biomes := []string{
		"minecraft:plains", "minecraft:desert", "minecraft:forest",
		"minecraft:badlands", "minecraft:snowy_plains", "minecraft:ocean",
		"minecraft:mushroom_fields", "minecraft:wooded_badlands",
	}
	ctx := rules.NewContext()
	rules.BeginColumn(ctx, 100, 100)
	ctx.SeaLevel, ctx.MinY = 63, -64
	ctx.MinSurfaceLevel, ctx.WaterHeight = 80, NoWaterAbove
	ctx.SurfaceDepth = 3
	for _, b := range biomes {
		ctx.BiomeName = b
		for y := 0; y < 100; y++ {
			ctx.Y = y
			ctx.StoneDepthAbove, ctx.StoneDepthBelow = 100-y, y+1
			rules.Apply(ctx) // must not panic
		}
	}
}

// TestSurfaceBedrockFloor confirms the bottom of the world resolves to bedrock
// (the vertical_gradient bedrock_floor rule is the first rule in the tree).
func TestSurfaceBedrockFloor(t *testing.T) {
	rules := loadTestRules(t)
	ctx := rules.NewContext()
	rules.BeginColumn(ctx, 0, 0)
	ctx.Y = -64
	ctx.StoneDepthAbove, ctx.StoneDepthBelow = 1, 1
	ctx.SeaLevel, ctx.MinY = 63, -64
	ctx.BiomeName = "minecraft:plains"
	ctx.MinSurfaceLevel, ctx.WaterHeight = 62, NoWaterAbove
	ctx.SurfaceDepth = 3
	state, ok := rules.Apply(ctx)
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
		{"minecraft:deepslate", map[string]string{"axis": "y"}, 27924},
		{"minecraft:mud", nil, 27922},
		{"minecraft:air", nil, 0},
	}
	for _, c := range cases {
		got, ok := surfaceBlockID(c.name, c.props)
		if !ok {
			t.Errorf("surfaceBlockID(%q,%v) not in the table", c.name, c.props)
			continue
		}
		if got != c.want {
			t.Errorf("surfaceBlockID(%q,%v) = %d, want %d", c.name, c.props, got, c.want)
		}
	}
	if _, ok := surfaceBlockID("minecraft:not_a_block", nil); ok {
		t.Error("surfaceBlockID accepted an unknown name")
	}
}

// TestColdEnoughToSnow pins the temperature predicate against the biome table
// extracted from the jar. deep_frozen_ocean is the interesting case: the name
// reads cold but its base temperature is 0.5, so vanilla does not freeze it —
// the hand-written list this replaced got it wrong.
func TestColdEnoughToSnow(t *testing.T) {
	cold := []string{
		"minecraft:frozen_ocean", "minecraft:frozen_peaks", "minecraft:frozen_river",
		"minecraft:grove", "minecraft:ice_spikes", "minecraft:jagged_peaks",
		"minecraft:snowy_beach", "minecraft:snowy_plains", "minecraft:snowy_slopes",
		"minecraft:snowy_taiga",
	}
	for _, b := range cold {
		if !coldEnoughToSnow(b) {
			t.Errorf("coldEnoughToSnow(%q) = false, want true", b)
		}
	}
	warm := []string{
		"minecraft:desert", "minecraft:plains", "minecraft:badlands",
		"minecraft:deep_frozen_ocean", "minecraft:taiga", "minecraft:windswept_hills",
	}
	for _, b := range warm {
		if coldEnoughToSnow(b) {
			t.Errorf("coldEnoughToSnow(%q) = true, want false", b)
		}
	}
	if coldEnoughToSnow("minecraft:not_a_biome") {
		t.Error("an unknown biome read as cold")
	}
	if len(biomeTemperature) != 65 {
		t.Errorf("biome temperature table has %d entries, want 65", len(biomeTemperature))
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
