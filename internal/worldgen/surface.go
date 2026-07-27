package worldgen

import (
	"encoding/json"
	"fmt"
	"math"
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

// NoWaterAbove is the "dry column" sentinel for SurfaceContext.WaterHeight,
// matching the Integer.MIN_VALUE vanilla uses. A caller building a context by
// hand must set it explicitly; the zero value would read as water at y=0.
const NoWaterAbove = math.MinInt

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
	// directly above Y, or NoWaterAbove when no fluid sits above Y with no air
	// in between. It is what the water condition measures against.
	WaterHeight int
	// SeaLevel is the world sea level (63 for the overworld).
	SeaLevel int
	// BiomeName is the resolved surface biome (e.g. "minecraft:desert").
	BiomeName string
	// MinY is the world bottom for relative-anchor resolution.
	MinY int
	// Steep is true when the column's neighbours in the chunk differ in height
	// by four or more blocks (SurfaceRules.SteepMaterialCondition).
	Steep bool
	// SurfaceDepth is how thick the biome's surface layers are at this column
	// (SurfaceSystem.getSurfaceDepth): usually 3, sometimes 0 or less, which is
	// what "hole" tests for. It widens the stone_depth bands.
	SurfaceDepth int
	// SurfaceSecondary is the "minecraft:surface_secondary" noise at this
	// column, which widens a stone_depth band further when the rule sets
	// secondary_depth_range.
	SurfaceSecondary float64
	// MinSurfaceLevel is the lowest Y the biome surface subtree may reach:
	// the interpolated preliminary surface level plus SurfaceDepth less 8.
	// above_preliminary_surface tests Y against it.
	MinSurfaceLevel int
	// noiseValues holds one sample per noise the rule tree's noise_threshold
	// conditions reference, refreshed once per column by BeginColumn. Vanilla
	// caches these the same way, through LazyXZCondition.
	noiseValues []float64
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

// bandlandsRule reads the world's clay band table at the block's height,
// shifted horizontally by the clay_bands_offset noise. It is what stripes the
// badlands.
type bandlandsRule struct{ bands *clayBands }

func (r bandlandsRule) Apply(ctx *SurfaceContext) (uint16, bool) {
	return r.bands.bandAt(ctx.X, ctx.Y, ctx.Z), true
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

// holeTest passes where the surface depth noise came out at or below zero — a
// bare patch with no surface layer at all, which is how coarse dirt and gravel
// scars appear in the middle of grass.
type holeTest struct{}

func (holeTest) Test(ctx *SurfaceContext) bool { return ctx.SurfaceDepth <= 0 }

// waterTest passes when the block is clear of the water above it — either there
// is none, or it sits far enough below the water's underside
// (SurfaceRules.WaterConditionSource).
//
// The height compared against is the column's own water surface, not sea level.
// Those differ wherever the aquifer put a pool at its own level: an underground
// lake, a mountain tarn or a flooded cave sit nowhere near y=63, and measuring
// them against sea level dressed dry stone as lakebed and lakebed as dry stone.
type waterTest struct {
	offset          int
	surfaceDepthMul int
	addStoneDepth   bool
}

func (t waterTest) Test(ctx *SurfaceContext) bool {
	if ctx.WaterHeight == NoWaterAbove {
		return true
	}
	y := ctx.Y
	if t.addStoneDepth {
		y += ctx.StoneDepthAbove
	}
	return y >= ctx.WaterHeight+t.offset+ctx.SurfaceDepth*t.surfaceDepthMul
}

// temperatureTest passes where the biome is cold enough for snow and ice
// rather than rain. The overworld tree uses it once, to freeze holes in a
// frozen ocean floor.
type temperatureTest struct{}

func (temperatureTest) Test(ctx *SurfaceContext) bool {
	return coldEnoughToSnow(ctx.BiomeName)
}

// yAboveTest passes when Y clears an anchor, with optional surface-depth and
// stone-depth offsets. The anchor is resolved against the world's height bounds
// at parse time.
type yAboveTest struct {
	anchorY         int
	addStoneDepth   bool
	surfaceDepthMul int
}

func (t yAboveTest) Test(ctx *SurfaceContext) bool {
	y := ctx.Y
	if t.addStoneDepth {
		y += ctx.StoneDepthAbove
	}
	return y >= t.anchorY+ctx.SurfaceDepth*t.surfaceDepthMul
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
	secondary := 0
	if t.secondaryRange != 0 {
		secondary = int(mapRange(ctx.SurfaceSecondary, -1.0, 1.0, 0.0, float64(t.secondaryRange)))
	}
	return depth <= 1+t.offset+surfaceDepth+secondary
}

// noiseThresholdTest passes when its noise, sampled once per column at y=0, is
// within [min,max]. slot indexes SurfaceContext.noiseValues, which the rule set
// refreshes per column.
//
// Six of the seven noises the overworld tree uses were unsupported and fell
// through as false, so calcite on stony peaks, ice and packed ice on frozen
// peaks, powder snow, swamp water windows and gravel patches on stony shores
// never appeared at all.
type noiseThresholdTest struct {
	min, max float64
	slot     int
}

func (t noiseThresholdTest) Test(ctx *SurfaceContext) bool {
	v := ctx.noiseValues[t.slot]
	return v >= t.min && v <= t.max
}

// notTest inverts its inner test.
type notTest struct{ inner ConditionTest }

func (t notTest) Test(ctx *SurfaceContext) bool { return !t.inner.Test(ctx) }

// verticalGradientTest is the scattered transition between two layers: true
// below one anchor, false above another, and in between a per-position coin
// flip whose bias falls linearly with height. It draws the bedrock floor and
// the stone-to-deepslate boundary.
//
// The anchors are resolved once at parse time, so this needs the world's height
// bounds; the random factory is named by the rule (bedrock_floor, deepslate)
// and forked from the world seed, so the same y gets the same answer every
// time the chunk regenerates.
type verticalGradientTest struct {
	trueAtAndBelow  int
	falseAtAndAbove int
	random          PositionalRandomFactory
}

func (t verticalGradientTest) Test(ctx *SurfaceContext) bool {
	if ctx.Y <= t.trueAtAndBelow {
		return true
	}
	if ctx.Y >= t.falseAtAndAbove {
		return false
	}
	probability := mapRange(float64(ctx.Y), float64(t.trueAtAndBelow), float64(t.falseAtAndAbove), 1.0, 0.0)
	return float64(t.random.At(ctx.X, ctx.Y, ctx.Z).NextFloat()) < probability
}

// abovePreliminarySurfaceTest gates the whole biome surface subtree: below the
// column's minimum surface level nothing is dressed and the stone stays stone.
type abovePreliminarySurfaceTest struct{}

func (abovePreliminarySurfaceTest) Test(ctx *SurfaceContext) bool {
	return ctx.Y >= ctx.MinSurfaceLevel
}

// ---- Parser ------------------------------------------------------------

// SurfaceRuleSet is a compiled surface rule tree together with the seeded
// noises and random factories its conditions reference.
//
// The tree used to be parsed once, globally, and shared by every world: the
// conditions that need the seed simply did not work. Binding it to a
// RandomState is what lets noise_threshold sample a real noise and
// vertical_gradient roll a real per-position coin.
type SurfaceRuleSet struct {
	root   SurfaceRule
	noises []*NormalNoise
}

// NewContext returns a SurfaceContext sized for this rule set's per-column
// noise cache. Reuse one per goroutine; BeginColumn refreshes it.
func (s *SurfaceRuleSet) NewContext() *SurfaceContext {
	return &SurfaceContext{noiseValues: make([]float64, len(s.noises))}
}

// BeginColumn samples every noise the tree references at (x, z) and stores the
// column coordinates. Vanilla samples these lazily and caches them per column;
// sampling all of them up front costs a handful of evaluations per column and
// keeps the tree free of hidden state.
func (s *SurfaceRuleSet) BeginColumn(ctx *SurfaceContext, x, z int) {
	ctx.X, ctx.Z = x, z
	for i, n := range s.noises {
		ctx.noiseValues[i] = n.GetValue(float64(x), 0, float64(z))
	}
}

// Apply runs the tree at the context's current position.
func (s *SurfaceRuleSet) Apply(ctx *SurfaceContext) (uint16, bool) { return s.root.Apply(ctx) }

// surfaceParser carries the seed-dependent state a rule tree needs while it is
// being built: where to get noises and random factories, and the world's height
// bounds for resolving vertical anchors.
type surfaceParser struct {
	loader       *Loader
	minY, height int
	noises       []*NormalNoise
	noiseSlots   map[string]int
}

// noiseSlot returns the per-column cache index for a named noise, loading and
// seeding it on first use.
func (p *surfaceParser) noiseSlot(name string) (int, error) {
	if slot, ok := p.noiseSlots[name]; ok {
		return slot, nil
	}
	n, err := p.loader.noiseField(name)
	if err != nil {
		return 0, err
	}
	slot := len(p.noises)
	p.noises = append(p.noises, n)
	p.noiseSlots[name] = slot
	return slot, nil
}

// resolveAnchor is VerticalAnchor.resolveY.
func (p *surfaceParser) resolveAnchor(a anchorJSON) int {
	return resolveAnchorY(a, p.minY, p.height)
}

// resolveAnchorY is VerticalAnchor.resolveY against explicit world bounds. The
// carver configs use the same anchor shape as the surface rules.
func resolveAnchorY(a anchorJSON, minY, height int) int {
	switch a.kind {
	case anchorAboveBottom:
		return minY + a.value
	case anchorBelowTop:
		return minY + height - 1 - a.value
	default:
		return a.value
	}
}

func (p *surfaceParser) parseRule(raw json.RawMessage) (SurfaceRule, error) {
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
		state, ok := surfaceBlockID(b.Result.Name, b.Result.Properties)
		if !ok {
			return nil, fmt.Errorf("surface: no block-state ID for %q %v", b.Result.Name, b.Result.Properties)
		}
		return blockRule{state: state}, nil

	case "minecraft:sequence":
		var s struct {
			Sequence []json.RawMessage `json:"sequence"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		rules := make([]SurfaceRule, 0, len(s.Sequence))
		for _, child := range s.Sequence {
			r, err := p.parseRule(child)
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
		test, err := p.parseCondition(c.IfTrue)
		if err != nil {
			return nil, err
		}
		then, err := p.parseRule(c.Then)
		if err != nil {
			return nil, err
		}
		return conditionRule{test: test, then: then}, nil

	case "minecraft:bandlands":
		bands, err := p.loader.clayBands()
		if err != nil {
			return nil, err
		}
		return bandlandsRule{bands: bands}, nil
	}
	return nil, fmt.Errorf("surface: unknown rule type %q", obj.Type)
}

// parseCondition parses an if_true condition node into a ConditionTest.
func (p *surfaceParser) parseCondition(raw json.RawMessage) (ConditionTest, error) {
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
			Offset          int  `json:"offset"`
			SurfaceDepthMul int  `json:"surface_depth_multiplier"`
			AddStoneDepth   bool `json:"add_stone_depth"`
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
		slot, err := p.noiseSlot(n.Noise)
		if err != nil {
			return nil, fmt.Errorf("noise_threshold %q: %w", n.Noise, err)
		}
		return noiseThresholdTest{min: n.Min, max: n.Max, slot: slot}, nil

	case "minecraft:y_above":
		var y struct {
			AddStoneDepth   bool       `json:"add_stone_depth"`
			SurfaceDepthMul int        `json:"surface_depth_multiplier"`
			Anchor          anchorJSON `json:"anchor"`
		}
		if err := json.Unmarshal(raw, &y); err != nil {
			return nil, err
		}
		return yAboveTest{
			anchorY:         p.resolveAnchor(y.Anchor),
			addStoneDepth:   y.AddStoneDepth,
			surfaceDepthMul: y.SurfaceDepthMul,
		}, nil

	case "minecraft:not":
		var n struct {
			Invert json.RawMessage `json:"invert"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		inner, err := p.parseCondition(n.Invert)
		if err != nil {
			return nil, err
		}
		return notTest{inner: inner}, nil

	case "minecraft:vertical_gradient":
		var v struct {
			RandomName      string     `json:"random_name"`
			TrueAtAndBelow  anchorJSON `json:"true_at_and_below"`
			FalseAtAndAbove anchorJSON `json:"false_at_and_above"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		if v.RandomName == "" {
			return nil, fmt.Errorf("vertical_gradient: missing random_name")
		}
		return verticalGradientTest{
			trueAtAndBelow:  p.resolveAnchor(v.TrueAtAndBelow),
			falseAtAndAbove: p.resolveAnchor(v.FalseAtAndAbove),
			random:          p.loader.rs.Positional().FromHashOf(v.RandomName).ForkPositional(),
		}, nil

	case "minecraft:above_preliminary_surface":
		return abovePreliminarySurfaceTest{}, nil
	}
	return nil, fmt.Errorf("surface: unknown condition type %q", obj.Type)
}

// anchorJSON decodes a VerticalAnchor: exactly one of absolute, above_bottom or
// below_top. Which one it was matters — reading the value without the kind made
// every absolute anchor resolve as an offset from the world floor, which is why
// the deepslate rule (absolute 0 to 8) collapsed onto y=-64 and never fired.
type anchorJSON struct {
	kind  anchorKind
	value int
}

type anchorKind int

const (
	anchorAbsolute anchorKind = iota
	anchorAboveBottom
	anchorBelowTop
)

func (a *anchorJSON) UnmarshalJSON(data []byte) error {
	var m map[string]int
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	for key, kind := range map[string]anchorKind{
		"absolute":     anchorAbsolute,
		"above_bottom": anchorAboveBottom,
		"below_top":    anchorBelowTop,
	} {
		if v, ok := m[key]; ok {
			a.kind, a.value = kind, v
			return nil
		}
	}
	return fmt.Errorf("surface: anchor has none of absolute/above_bottom/below_top")
}

// ---- Loader ------------------------------------------------------------

// loadSurfaceRuleSet parses the overworld surface_rule tree, binding its
// conditions to this loader's seeded RandomState.
func (l *Loader) loadSurfaceRuleSet(minY, height int) (*SurfaceRuleSet, error) {
	var doc struct {
		SurfaceRule json.RawMessage `json:"surface_rule"`
	}
	if err := l.readJSON("data/overworld.json", &doc); err != nil {
		return nil, err
	}
	p := &surfaceParser{loader: l, minY: minY, height: height, noiseSlots: map[string]int{}}
	root, err := p.parseRule(doc.SurfaceRule)
	if err != nil {
		return nil, err
	}
	return &SurfaceRuleSet{root: root, noises: p.noises}, nil
}
