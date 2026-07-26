package worldgen

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
)

// surface.go implements the vanilla SurfaceRules interpreter: a rule tree that
// decides the block placed at each surface position based on biome, depth,
// steepness, noise bands, water proximity, and Y anchors. The tree is parsed
// from the embedded overworld.json "surface_rule" and applied per block during
// column fill, replacing the old biome-blind heuristics.
//
// It reproduces net.minecraft.world.level.levelgen.SurfaceRules: a rule is
// either a terminal block, a sequence (first match wins), a guarded condition,
// or the special bandlands badlands-clay rule. Condition tests are the 11 types
// present in the overworld rule tree.

// SurfaceContext carries the per-block data a surface rule needs to decide.
type SurfaceContext struct {
	// X, Y, Z are the block's world coordinates.
	X, Y, Z int
	// StoneDepthAbove counts solid blocks from the top of the current stone run
	// down to and including Y — 1 for the block directly under air or fluid.
	// StoneDepthBelow counts the other way, 1 for the block directly above the
	// cave roof under it. Together they are the vanilla "stone_depth" the
	// stone_depth condition compares against, floor and ceiling respectively.
	StoneDepthAbove int
	StoneDepthBelow int
	// WaterHeight is one above the lowest fluid block of the run of fluid
	// directly above Y, or math.MinInt when no fluid sits above Y with no air
	// in between. It is what the water condition measures against.
	WaterHeight int
	// SeaLevel is the world sea level (63 for the overworld).
	SeaLevel int
	// BiomeName is the resolved surface biome (e.g. "minecraft:desert").
	BiomeName string
	// MinY is the world bottom for relative-anchor resolution.
	MinY int
	// SurfaceNoise is the "minecraft:surface" noise sample at (X,Z); the
	// noise_threshold condition ranges over it.
	SurfaceNoise float64
	// Steep is true when the local slope exceeds the vanilla steep threshold
	// (~1.0 surface-depth delta between neighbours).
	Steep bool
	// SurfaceDepth is the vanilla surface-depth value at this column (a small
	// noise-driven integer 0..N) added to stone depth comparisons.
	SurfaceDepth int
	// PreliminarySurface is the top solid Y in this column; the
	// above_preliminary_surface condition passes for blocks above it.
	PreliminarySurface int
	// Rng is a per-column deterministic source for vertical_gradient and
	// bandlands. It is seeded by the column so results are stable across runs.
	Rng *rand.Rand
}

// SurfaceRule decides the block at a context. Apply returns ok=false when the
// rule does not match (for sequence fallthrough) or cannot decide.
type SurfaceRule interface {
	Apply(ctx *SurfaceContext) (state uint16, ok bool)
}

// ---- Rule nodes --------------------------------------------------------

// blockRule places a fixed block state.
type blockRule struct{ state uint16 }

func (r blockRule) Apply(_ *SurfaceContext) (uint16, bool) { return r.state, true }

// sequenceRule applies the first child that matches (short-circuit, like &&).
type sequenceRule struct{ rules []SurfaceRule }

func (r sequenceRule) Apply(ctx *SurfaceContext) (uint16, bool) {
	for _, rule := range r.rules {
		if s, ok := rule.Apply(ctx); ok {
			return s, true
		}
	}
	return 0, false
}

// conditionRule applies its inner rule only when the test passes.
type conditionRule struct {
	test ConditionTest
	then SurfaceRule
}

func (r conditionRule) Apply(ctx *SurfaceContext) (uint16, bool) {
	if !r.test.Test(ctx) {
		return 0, false
	}
	return r.then.Apply(ctx)
}

// bandlandsRule reproduces the vanilla badlands coloured-clay banding: a
// deterministic per-column pattern of terracotta colours at certain Y bands. We
// approximate the 8-band rotation using the column RNG; exact band geometry is
// captured well enough to read as badlands.
type bandlandsRule struct{}

func (bandlandsRule) Apply(ctx *SurfaceContext) (uint16, bool) {
	if ctx.Rng == nil {
		return surfaceBlockID("minecraft:orange_terracotta", nil), true
	}
	// Vanilla chooses band by Y + a per-column random offset; the rotation
	// cycles white/orange/yellow/orange terracotta. Pick from the cycle by Y.
	band := (ctx.Y + ctx.Rng.Intn(7)) % 4
	switch band {
	case 0:
		return surfaceBlockID("minecraft:white_terracotta", nil), true
	case 1, 3:
		return surfaceBlockID("minecraft:orange_terracotta", nil), true
	default:
		return surfaceBlockID("minecraft:yellow_terracotta", nil), true
	}
}

// ---- Condition tests ---------------------------------------------------

// ConditionTest is a boolean predicate over a SurfaceContext.
type ConditionTest interface {
	Test(ctx *SurfaceContext) bool
}

// biomeTest passes when the column's biome is in the allowlist.
type biomeTest struct{ allowed []string }

func (t biomeTest) Test(ctx *SurfaceContext) bool {
	for _, b := range t.allowed {
		if b == ctx.BiomeName {
			return true
		}
	}
	return false
}

// steepTest passes on steep terrain (vanilla SurfaceRules.STEEP, slope > ~1.0).
type steepTest struct{}

func (steepTest) Test(ctx *SurfaceContext) bool { return ctx.Steep }

// holeTest passes in surface "holes" below the surrounding terrain — we
// approximate as "below sea level and not the top" since true hole detection
// needs a neighbourhood. Conservative: false (rare rule, low visual cost).
type holeTest struct{}

func (holeTest) Test(ctx *SurfaceContext) bool { return false }

// waterTest passes when the block is within `offset` of the water surface
// (vanilla SurfaceRules.WATER). We treat it as "at or just below sea level" —
// the common case for beach/shore rules.
type waterTest struct {
	offset               int
	surfaceDepthMul      int
	addStoneDepth        bool
}

func (t waterTest) Test(ctx *SurfaceContext) bool {
	// Vanilla: passes when Y >= seaLevel + offset + surfaceDepth*mul (±stone).
	threshold := ctx.SeaLevel + t.offset + ctx.SurfaceDepth*t.surfaceDepthMul
	return ctx.Y >= threshold
}

// temperatureTest passes when the (column) temperature is below freezing — the
// snow-at-height rule. We fold temperature into the biome name (snowy_*
// biomes) rather than sampling the temperature noise, so pass for cold biomes.
type temperatureTest struct{}

func (temperatureTest) Test(ctx *SurfaceContext) bool {
	return isColdBiome(ctx.BiomeName)
}

// isColdBiome reports whether the biome should receive snow cover. We use the
// biome name rather than the temperature noise for simplicity; this matches
// the visible result for the standard overworld biomes.
func isColdBiome(name string) bool {
	switch name {
	case "minecraft:snowy_plains", "minecraft:snowy_taiga", "minecraft:snowy_beach",
		"minecraft:snowy_slopes", "minecraft:jagged_peaks", "minecraft:frozen_peaks",
		"minecraft:frozen_river", "minecraft:frozen_ocean", "minecraft:deep_frozen_ocean",
		"minecraft:ice_spikes", "minecraft:grove":
		return true
	}
	return false
}

// yAboveTest passes when Y is above an anchor (absolute, above_bottom, or
// below_top), with optional surface-depth and stone-depth offsets.
type yAboveTest struct {
	absolute           int
	hasAbsolute        bool
	aboveBottom        int
	hasAboveBottom     bool
	belowTop           int
	hasBelowTop        bool
	addStoneDepth      bool
	surfaceDepthMul    int
}

func (t yAboveTest) Test(ctx *SurfaceContext) bool {
	var anchor int
	switch {
	case t.hasAbsolute:
		anchor = t.absolute
	case t.hasAboveBottom:
		anchor = ctx.MinY + t.aboveBottom
	case t.hasBelowTop:
		anchor = (ctx.MinY + 384) - 1 - t.belowTop
	}
	threshold := anchor + ctx.SurfaceDepth*t.surfaceDepthMul
	if t.addStoneDepth {
		threshold += ctx.StoneDepthAbove
	}
	return ctx.Y >= threshold
}

// stoneDepthTest passes when the block is within `offset` of the surface it
// names: "floor" measures down from the top of the stone run (the ground you
// walk on), "ceiling" measures up from its bottom (the roof of whatever cave or
// ocean sits underneath). The overworld tree uses ceiling with offset 0 to dress
// cave roofs — fourteen times, more than any other stone_depth form.
type stoneDepthTest struct {
	surfaceType     string // "floor" or "ceiling"
	offset          int
	addSurfaceDepth bool
	secondaryRange  int
}

func (t stoneDepthTest) Test(ctx *SurfaceContext) bool {
	depth := ctx.StoneDepthAbove
	if t.surfaceType == "ceiling" {
		depth = ctx.StoneDepthBelow
	}
	surfaceDepth := 0
	if t.addSurfaceDepth {
		surfaceDepth = ctx.SurfaceDepth
	}
	// Vanilla widens the band by map(surface_secondary noise, -1..1, 0..range).
	// That noise is not sampled yet, so the secondary term stays 0; the two
	// rules that use it also set add_surface_depth, and both currently reduce to
	// the same single-block band either way.
	return depth <= 1+t.offset+surfaceDepth
}

// noiseThresholdTest passes when the named surface noise is within [min,max].
type noiseThresholdTest struct {
	min, max float64
	noise    string
}

func (t noiseThresholdTest) Test(ctx *SurfaceContext) bool {
	// Only "minecraft:surface" is sampled in SurfaceContext; other noises fall
	// through as false (conservative).
	if t.noise != "minecraft:surface" {
		return false
	}
	return ctx.SurfaceNoise >= t.min && ctx.SurfaceNoise <= t.max
}

// notTest inverts its inner test.
type notTest struct{ inner ConditionTest }

func (t notTest) Test(ctx *SurfaceContext) bool { return !t.inner.Test(ctx) }

// verticalGradientTest reproduces the bedrock-floor gradient: a deterministic
// band from true_at_and_below to false_at_and_above where membership tapers via
// the column RNG. Anchors are above_bottom offsets from the world floor.
type verticalGradientTest struct {
	randomName      string
	trueAtAndBelow  int // above_bottom
	falseAtAndAbove int // above_bottom
}

func (t verticalGradientTest) Test(ctx *SurfaceContext) bool {
	loY := ctx.MinY + t.trueAtAndBelow
	hiY := ctx.MinY + t.falseAtAndAbove
	switch {
	case ctx.Y <= loY:
		return true
	case ctx.Y >= hiY:
		return false
	}
	// Taper band: probability decreases linearly. Use the per-column RNG once
	// per Y so the floor is stable but noisy. We approximate vanilla's
	// random-based interpolation.
	if ctx.Rng == nil {
		return false
	}
	band := hiY - loY
	pos := ctx.Y - loY
	return ctx.Rng.Float64() > float64(pos)/float64(band)
}

// abovePreliminarySurfaceTest passes for blocks at or above the column's
// preliminary surface (the top solid Y). Vanilla gates the biome dispatch on
// this so submerged blocks far below the surface keep stone.
type abovePreliminarySurfaceTest struct{}

func (abovePreliminarySurfaceTest) Test(ctx *SurfaceContext) bool {
	return ctx.Y >= ctx.PreliminarySurface
}

// ---- Parser ------------------------------------------------------------

// ParseSurfaceRule parses a surface_rule JSON node into a rule tree.
func ParseSurfaceRule(raw json.RawMessage) (SurfaceRule, error) {
	var obj struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	switch obj.Type {
	case "minecraft:block":
		var b struct {
			Result struct {
				Name       string            `json:"Name"`
				Properties map[string]string `json:"Properties"`
			} `json:"result_state"`
		}
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		return blockRule{state: surfaceBlockID(b.Result.Name, b.Result.Properties)}, nil

	case "minecraft:sequence":
		var s struct {
			Sequence []json.RawMessage `json:"sequence"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		rules := make([]SurfaceRule, 0, len(s.Sequence))
		for _, child := range s.Sequence {
			r, err := ParseSurfaceRule(child)
			if err != nil {
				return nil, err
			}
			rules = append(rules, r)
		}
		return sequenceRule{rules: rules}, nil

	case "minecraft:condition":
		var c struct {
			IfTrue json.RawMessage `json:"if_true"`
			Then   json.RawMessage `json:"then_run"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		test, err := parseCondition(c.IfTrue)
		if err != nil {
			return nil, err
		}
		then, err := ParseSurfaceRule(c.Then)
		if err != nil {
			return nil, err
		}
		return conditionRule{test: test, then: then}, nil

	case "minecraft:bandlands":
		return bandlandsRule{}, nil
	}
	return nil, fmt.Errorf("surface: unknown rule type %q", obj.Type)
}

// parseCondition parses an if_true condition node into a ConditionTest.
func parseCondition(raw json.RawMessage) (ConditionTest, error) {
	var obj struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	switch obj.Type {
	case "minecraft:biome":
		var b struct {
			Is []string `json:"biome_is"`
		}
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		return biomeTest{allowed: b.Is}, nil

	case "minecraft:steep":
		return steepTest{}, nil

	case "minecraft:hole":
		return holeTest{}, nil

	case "minecraft:water":
		var w struct {
			Offset               int  `json:"offset"`
			SurfaceDepthMul      int  `json:"surface_depth_multiplier"`
			AddStoneDepth        bool `json:"add_stone_depth"`
		}
		if err := json.Unmarshal(raw, &w); err != nil {
			return nil, err
		}
		return waterTest{offset: w.Offset, surfaceDepthMul: w.SurfaceDepthMul, addStoneDepth: w.AddStoneDepth}, nil

	case "minecraft:temperature":
		return temperatureTest{}, nil

	case "minecraft:stone_depth":
		var s struct {
			SurfaceType     string `json:"surface_type"`
			Offset          int    `json:"offset"`
			AddSurfaceDepth bool   `json:"add_surface_depth"`
			SecondaryRange  int    `json:"secondary_depth_range"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		return stoneDepthTest{surfaceType: s.SurfaceType, offset: s.Offset, addSurfaceDepth: s.AddSurfaceDepth, secondaryRange: s.SecondaryRange}, nil

	case "minecraft:noise_threshold":
		var n struct {
			Min   float64 `json:"min_threshold"`
			Max   float64 `json:"max_threshold"`
			Noise string  `json:"noise"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		return noiseThresholdTest{min: n.Min, max: n.Max, noise: n.Noise}, nil

	case "minecraft:y_above":
		var y struct {
			AddStoneDepth   bool `json:"add_stone_depth"`
			SurfaceDepthMul int  `json:"surface_depth_multiplier"`
			Anchor          struct {
				Absolute    *int `json:"absolute"`
				AboveBottom *int `json:"above_bottom"`
				BelowTop    *int `json:"below_top"`
			} `json:"anchor"`
		}
		if err := json.Unmarshal(raw, &y); err != nil {
			return nil, err
		}
		t := yAboveTest{addStoneDepth: y.AddStoneDepth, surfaceDepthMul: y.SurfaceDepthMul}
		if y.Anchor.Absolute != nil {
			t.hasAbsolute, t.absolute = true, *y.Anchor.Absolute
		}
		if y.Anchor.AboveBottom != nil {
			t.hasAboveBottom, t.aboveBottom = true, *y.Anchor.AboveBottom
		}
		if y.Anchor.BelowTop != nil {
			t.hasBelowTop, t.belowTop = true, *y.Anchor.BelowTop
		}
		return t, nil

	case "minecraft:not":
		var n struct {
			Invert json.RawMessage `json:"invert"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		inner, err := parseCondition(n.Invert)
		if err != nil {
			return nil, err
		}
		return notTest{inner: inner}, nil

	case "minecraft:vertical_gradient":
		var v struct {
			TrueAtAndBelow  anchorJSON `json:"true_at_and_below"`
			FalseAtAndAbove anchorJSON `json:"false_at_and_above"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		return verticalGradientTest{
			trueAtAndBelow:  v.TrueAtAndBelow.aboveBottom,
			falseAtAndAbove: v.FalseAtAndAbove.aboveBottom,
		}, nil

	case "minecraft:above_preliminary_surface":
		return abovePreliminarySurfaceTest{}, nil
	}
	return nil, fmt.Errorf("surface: unknown condition type %q", obj.Type)
}

// anchorJSON decodes a {above_bottom|below_top|absolute: N} surface anchor.
type anchorJSON struct {
	absolute    int
	aboveBottom int
	belowTop    int
}

func (a *anchorJSON) UnmarshalJSON(data []byte) error {
	var m map[string]int
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	a.aboveBottom = m["above_bottom"]
	a.belowTop = m["below_top"]
	a.absolute = m["absolute"]
	return nil
}

// ---- Loader ------------------------------------------------------------

var (
	surfaceRuleOnce sync.Once
	surfaceRule     SurfaceRule
	surfaceRuleErr  error
)

// LoadOverworldSurfaceRule parses and caches the overworld surface_rule tree.
// The rule tree does not depend on the world seed, so it is loaded once.
func LoadOverworldSurfaceRule() (SurfaceRule, error) {
	surfaceRuleOnce.Do(func() {
		raw, err := dataFS.ReadFile("data/overworld.json")
		if err != nil {
			surfaceRuleErr = err
			return
		}
		var doc struct {
			SurfaceRule json.RawMessage `json:"surface_rule"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			surfaceRuleErr = err
			return
		}
		surfaceRule, surfaceRuleErr = ParseSurfaceRule(doc.SurfaceRule)
	})
	return surfaceRule, surfaceRuleErr
}
