package worldgen

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
)

//go:embed feature_data.zip
var featureData []byte

// FeatureSet is the validated vanilla datapack graph used by decoration.
// Configured also accumulates inline configured features (datapack feature
// references written as objects instead of names), registered lazily under
// deterministic "inline:" keys by parseFeatureRef.
type FeatureSet struct {
	Configured map[string]ConfiguredFeature
	Placed     map[string]PlacedFeature
	Biomes     map[string]BiomeGeneration
	BlockTags  map[string][]string

	inlineMu         sync.RWMutex
	inlineConfigured map[string]ConfiguredFeature
	cacheMu          sync.RWMutex
	lastStepsOrder   []string
	lastSteps        [][]string
	stepsCache       map[string][][]string
	cfgCache         map[[2]string]any
}

// LookupConfigured looks up a configured feature by name, checking both the
// immutable datapack Configured table and any inline configured features
// registered by parseFeatureRef.
func (s *FeatureSet) LookupConfigured(name string) (ConfiguredFeature, bool) {
	if cf, ok := s.Configured[name]; ok {
		return cf, true
	}
	s.inlineMu.RLock()
	cf, ok := s.inlineConfigured[name]
	s.inlineMu.RUnlock()
	return cf, ok
}

const (
	maxFeatureSetCfgCacheEntries   = 4096
	maxFeatureSetStepsCacheEntries = 64
)

func (s *FeatureSet) getCachedConfig(kind, name string) (any, bool) {
	s.cacheMu.RLock()
	v, ok := s.cfgCache[[2]string{kind, name}]
	s.cacheMu.RUnlock()
	return v, ok
}

func (s *FeatureSet) setCachedConfig(kind, name string, val any) {
	s.cacheMu.Lock()
	if s.cfgCache == nil {
		s.cfgCache = make(map[[2]string]any)
	} else if len(s.cfgCache) >= maxFeatureSetCfgCacheEntries {
		clear(s.cfgCache)
	}
	s.cfgCache[[2]string{kind, name}] = val
	s.cacheMu.Unlock()
}

type IndexedFeature struct {
	Name  string
	Index int
}

// ScheduledFeature is one feature selected for a decoration stage. Index is
// the stable FeatureSorter index within that stage and must be used when
// deriving the feature random seed; it is not the index in any one biome.
type ScheduledFeature struct {
	Name  string
	Index int
}

type ConfiguredFeature struct {
	Type   string
	Config json.RawMessage
}

type PlacedFeature struct {
	Feature   string
	Placement []PlacementModifier
}

type PlacementModifier struct {
	Type   string
	Raw    json.RawMessage
	parsed *parsedPlacementModifier
}

func (m *PlacementModifier) UnmarshalJSON(raw []byte) error {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return err
	}
	m.Type = head.Type
	m.Raw = append(m.Raw[:0], raw...)
	m.parsed = compilePlacementModifier(m.Type, m.Raw)
	return nil
}

type OreFeatureConfig struct {
	Size               int
	DiscardAirExposure float64 `json:"discard_chance_on_air_exposure"`
	Targets            []OreTarget
}

type OreTarget struct {
	State struct {
		Name string `json:"Name"`
	} `json:"state"`
	Target struct {
		PredicateType string `json:"predicate_type"`
		Tag           string `json:"tag"`
	} `json:"target"`
}

// DiskFeatureConfig is the subset of DiskFeature used by overworld disks.
// Radius is an inclusive uniform integer provider in the vanilla datapack.
type DiskFeatureConfig struct {
	HalfHeight           int
	RadiusMin, RadiusMax int
	State                BlockState
	Fallback             BlockState
	Rules                []DiskStateRule
	Targets              []string
}

type DiskStateRule struct {
	IfTrue json.RawMessage
	Then   BlockState
}

type GeodeFeatureConfig struct {
	Outer, Middle, Inner, Filling      BlockState
	AlternateInner                     BlockState
	InnerPlacements                    []BlockState
	DistributionMin, DistributionMax   int
	OuterWallMin, OuterWallMax         int
	PointOffsetMin, PointOffsetMax     int
	MaxGenOffset, MinGenOffset         int
	FillingLayer, InnerLayer           float64
	MiddleLayer, OuterLayer            float64
	NoiseMultiplier                    float64
	InvalidBlocksThreshold             int
	CannotReplaceTag, InvalidBlocksTag string
	CrackChance, BaseCrackSize         float64
	CrackPointOffset                   int
	UseAlternateLayerChance            float64
	PlacementsRequireAlternate         bool
	UsePotentialPlacementsChance       float64
}

type VegetationPatchFeatureConfig struct {
	Surface                  string
	DepthMin, DepthMax       int
	XZRadiusMin, XZRadiusMax int
	VerticalRange            int
	ExtraBottomBlockChance   float32
	ExtraEdgeColumnChance    float32
	VegetationChance         float32
	Ground                   BlockState
	ReplaceableTag           string
	Vegetation               FeatureRef
}

// SimpleBlockFeatureConfig is the datapack form used by simple_block. The
// provider entries retain their vanilla weights and named block properties so
// the feature can choose a state with the feature RNG at placement time, while
// Provider also supports noise_provider, noise_threshold_provider, and
// dual_noise_provider.
type SimpleBlockFeatureConfig struct {
	States   []WeightedBlockState
	Provider StateProviderSpec
}

type WeightedBlockState struct {
	State  BlockState
	Weight int
}

// StateProviderSpec is the parsed form of a block-state provider: simple,
// weighted, randomized-int-over-another-provider, rule-based, or one of the
// three noise-based providers (noise_provider, noise_threshold_provider,
// dual_noise_provider).
type StateProviderSpec struct {
	Type     string
	State    BlockState           // simple
	Entries  []WeightedBlockState // weighted
	Property string               // randomized_int
	Source   *StateProviderSpec   // randomized_int
	Values   CountProvider        // randomized_int
	// Rules and Fallback are rule_based_state_provider: the first rule whose
	// predicate passes supplies the state, and its `then` provider is kept raw
	// because evaluating a predicate needs the world, which this package does
	// not have. A missing fallback is significant, not an omission - see
	// RuleBasedStateProvider.getState, which returns the block already at the
	// position when no rule matched and there is no fallback.
	Rules    []StateProviderRule
	Fallback *StateProviderSpec

	// NoiseBasedStateProvider / NoiseProvider / NoiseThresholdProvider / DualNoiseProvider fields.
	Noise      *NormalNoise
	Scale      float32
	States     []BlockState
	Threshold  float32
	HighChance float32
	Default    BlockState
	LowStates  []BlockState
	HighStates []BlockState
	SlowNoise  *NormalNoise
	SlowScale  float32
	VarietyMin int
	VarietyMax int
}

// StateProviderRule is one {if_true, then} pair of a rule_based provider.
type StateProviderRule struct {
	Predicate json.RawMessage
	Then      json.RawMessage
}

// SampleState mirrors BlockStateProvider.getState(random) for position-independent
// providers.
func (p StateProviderSpec) SampleState(r RandomSource) (BlockState, bool) {
	return p.SampleStateAt(r, FeaturePosition{})
}

// SampleStateAt mirrors BlockStateProvider.getState(level, random, pos),
// evaluating noise-based providers at pos while consuming the exact RNG draws
// vanilla does.
func (p StateProviderSpec) SampleStateAt(r RandomSource, pos FeaturePosition) (BlockState, bool) {
	switch p.Type {
	case "simple":
		return p.State, true
	case "weighted":
		total := 0
		for _, e := range p.Entries {
			total += e.Weight
		}
		if total <= 0 {
			return BlockState{}, false
		}
		roll := int(r.NextIntN(int32(total)))
		for _, e := range p.Entries {
			if roll < e.Weight {
				return e.State, true
			}
			roll -= e.Weight
		}
		return BlockState{}, false
	case "randomized_int":
		if p.Source == nil {
			return BlockState{}, false
		}
		base, ok := p.Source.SampleStateAt(r, pos)
		if !ok {
			return BlockState{}, false
		}
		value := p.Values.Sample(r)
		out := base
		if out.Properties == nil {
			out.Properties = map[string]string{}
		} else {
			props := make(map[string]string, len(base.Properties)+1)
			for k, v := range base.Properties {
				props[k] = v
			}
			out.Properties = props
		}
		out.Properties[p.Property] = strconv.Itoa(value)
		return out, true
	case "noise":
		if p.Noise == nil || len(p.States) == 0 {
			return BlockState{}, false
		}
		return noiseProviderPickState(p.States, p.noiseValue(pos, float64(p.Scale))), true
	case "noise_threshold":
		if p.Noise == nil {
			return BlockState{}, false
		}
		v := p.noiseValue(pos, float64(p.Scale))
		if v < float64(p.Threshold) {
			if len(p.LowStates) == 0 {
				return BlockState{}, false
			}
			return p.LowStates[int(r.NextIntN(int32(len(p.LowStates))))], true
		}
		if r.NextFloat() < p.HighChance {
			if len(p.HighStates) == 0 {
				return BlockState{}, false
			}
			return p.HighStates[int(r.NextIntN(int32(len(p.HighStates))))], true
		}
		return p.Default, true
	case "dual_noise":
		if p.Noise == nil || p.SlowNoise == nil || len(p.States) == 0 {
			return BlockState{}, false
		}
		slowVal := p.slowNoiseValue(pos)
		localVariety := int(clampedMapDouble(slowVal, -1.0, 1.0, float64(p.VarietyMin), float64(p.VarietyMax+1)))
		if localVariety <= 0 {
			return BlockState{}, false
		}
		list := make([]BlockState, 0, localVariety)
		for i := 0; i < localVariety; i++ {
			offPos := FeaturePosition{
				X: pos.X + int(int32(i)*54545),
				Y: pos.Y,
				Z: pos.Z + int(int32(i)*34234),
			}
			list = append(list, noiseProviderPickState(p.States, p.slowNoiseValue(offPos)))
		}
		return noiseProviderPickState(list, p.noiseValue(pos, float64(p.Scale))), true
	}
	return BlockState{}, false
}

func (p StateProviderSpec) noiseValue(pos FeaturePosition, scale float64) float64 {
	return p.Noise.GetValue(float64(pos.X)*scale, float64(pos.Y)*scale, float64(pos.Z)*scale)
}

func (p StateProviderSpec) slowNoiseValue(pos FeaturePosition) float64 {
	return p.SlowNoise.GetValue(
		float64(float32(pos.X)*p.SlowScale),
		float64(float32(pos.Y)*p.SlowScale),
		float64(float32(pos.Z)*p.SlowScale),
	)
}

func noiseProviderPickState(states []BlockState, noiseValue float64) BlockState {
	clamped := (1.0 + noiseValue) / 2.0
	if clamped < 0.0 {
		clamped = 0.0
	} else if clamped > 0.9999 {
		clamped = 0.9999
	}
	return states[int(clamped*float64(len(states)))]
}

func clampedMapDouble(val, inMin, inMax, outMin, outMax float64) float64 {
	t := (val - inMin) / (inMax - inMin)
	if t <= 0.0 {
		return outMin
	}
	if t >= 1.0 {
		return outMax
	}
	return outMin + t*(outMax-outMin)
}

// BlockColumnLayer is one layer of a block_column: a height provider and the
// state provider for its blocks.
type BlockColumnLayer struct {
	Height   NestedIntProvider
	Provider StateProviderSpec
}

// BlockColumnFeatureConfig mirrors BlockColumnConfiguration.
type BlockColumnFeatureConfig struct {
	Direction     string // "up" | "down"
	Layers        []BlockColumnLayer
	Allowed       json.RawMessage
	PrioritizeTip bool
}

// NestedIntProvider is an int provider whose weighted-list entries may
// themselves be providers (weighted data = uniform{...}); constant data
// (raw int) draws nothing.
type NestedIntProvider struct {
	Type     string
	Min, Max int
	Weighted []WeightedNestedInt
}

type WeightedNestedInt struct {
	Weight int
	Value  NestedIntProvider
}

// ConstantValue reports the value of a provider that draws nothing - a bare int or
// a uniform whose span is one - and false for anything that samples. It exists so
// call sites that have not implemented provider semantics can say so explicitly
// instead of reading p.Min and quietly treating a draw as a constant.
func (p NestedIntProvider) ConstantValue() (int, bool) {
	switch p.Type {
	case "constant":
		return p.Min, true
	case "uniform":
		if p.Max-p.Min+1 <= 1 {
			return p.Min, true
		}
	}
	return 0, false
}

// Sample mirrors IntProvider.sample: uniform draws once, weighted draws the
// entry pick then recurses into the entry, biased_to_bottom draws twice
// (nextInt(span) then nextInt(that+1)).
func (p NestedIntProvider) Sample(r RandomSource) int {
	switch p.Type {
	case "constant":
		return p.Min
	case "uniform":
		span := p.Max - p.Min + 1
		if span <= 1 {
			return p.Min
		}
		return p.Min + int(r.NextIntN(int32(span)))
	case "biased_to_bottom":
		span := p.Max - p.Min + 1
		if span <= 1 {
			return p.Min
		}
		first := int(r.NextIntN(int32(span)))
		return p.Min + int(r.NextIntN(int32(first+1)))
	case "weighted":
		total := 0
		for _, e := range p.Weighted {
			total += e.Weight
		}
		if total <= 0 {
			return 0
		}
		roll := int(r.NextIntN(int32(total)))
		for _, e := range p.Weighted {
			if roll < e.Weight {
				return e.Value.Sample(r)
			}
			roll -= e.Weight
		}
		return 0
	}
	return 0
}

// SimpleRandomSelectorConfig mirrors SimpleRandomFeatureConfiguration: one
// nextInt(size) draw picks a feature to place at the same position.
type SimpleRandomSelectorConfig struct {
	Features []FeatureRef
}

type UnderwaterMagmaFeatureConfig struct {
	FloorSearchRange           int     `json:"floor_search_range"`
	PlacementProbability       float32 `json:"placement_probability_per_valid_position"`
	PlacementRadiusAroundFloor int     `json:"placement_radius_around_floor"`
}

type ProbabilityFeatureConfig struct {
	Probability float32
}

type SpringFeatureConfig struct {
	HoleCount          int        `json:"hole_count"`
	RequiresBlockBelow bool       `json:"requires_block_below"`
	RockCount          int        `json:"rock_count"`
	State              BlockState `json:"state"`
	ValidBlocks        []string   `json:"valid_blocks"`
}

// TreeFeatureConfig mirrors TreeConfiguration, whose fields `javap -p` lists as
// exactly: trunk_provider, trunk_placer, foliage_provider, foliage_placer,
// decorators, minimum_size, ignore_vines, below_trunk_provider, root_placer.
//
// The shape of this struct was a bug before it was an omission. trunk_placer and
// foliage_placer used to hold plain ints, but the taiga placers carry int
// providers - pine's `height`, spruce's `offset`, mega_pine's `crown_height` are
// all `uniform` objects - so `minecraft:pine` and `minecraft:spruce` failed to
// decode outright, while mega_pine and mega_spruce decoded "successfully" with
// radius 0 and height 0 silently filled in from absent fields. One failure was
// loud and one was invisible, and only the loud one looked like a bug.
type TreeFeatureConfig struct {
	TrunkProvider      TreeBlockProvider `json:"trunk_provider"`
	FoliageProvider    TreeBlockProvider `json:"foliage_provider"`
	BelowTrunkProvider json.RawMessage   `json:"below_trunk_provider"`
	TrunkPlacer        TreeTrunkPlacer   `json:"trunk_placer"`
	FoliagePlacer      TreeFoliagePlacer `json:"foliage_placer"`
	MinimumSize        TreeMinimumSize   `json:"minimum_size"`
	IgnoreVines        bool              `json:"ignore_vines"`
	Decorators         []TreeDecorator   `json:"decorators"`
	RootPlacer         json.RawMessage   `json:"root_placer"`
}

// TreeBlockProvider is one of the three state-supplier shapes the tree configs
// actually use, measured across the 39 configured trees: trunk_provider is always
// simple_state_provider, foliage_provider is simple except azalea_tree which is a
// weighted_state_provider, and below_trunk_provider is rule_based_state_provider
// except once. Only the simple form carries `state`, so the field may be empty for
// the others and is not treated as a defect - but the type itself must be known.
type TreeBlockProvider struct {
	Type  string          `json:"type"`
	State BlockState      `json:"state"`
	Raw   json.RawMessage `json:"-"`
}

// knownStateProviders is again measured rather than remembered.
var knownStateProviders = map[string]bool{
	"minecraft:simple_state_provider":         true,
	"minecraft:weighted_state_provider":       true,
	"minecraft:rule_based_state_provider":     true,
	"minecraft:randomized_int_state_provider": true,
	"minecraft:rotated_block_provider":        true,
	"minecraft:noise_provider":                true,
	"minecraft:dual_noise_provider":           true,
	"minecraft:noise_threshold_provider":      true,
}

type parsedPlacerFields struct {
	scalars   map[string]int
	floats    map[string]float64
	providers map[string]NestedIntProvider
}

type TreeTrunkPlacer struct {
	Type   string
	Fields map[string]json.RawMessage
	parsed *parsedPlacerFields
}

type TreeFoliagePlacer struct {
	Type   string
	Fields map[string]json.RawMessage
	parsed *parsedPlacerFields
}

// UnmarshalJSON peels "type" off and keeps every other member verbatim, so no
// field can be lost by not being declared.
func (p *TreeTrunkPlacer) UnmarshalJSON(raw []byte) error {
	return unmarshalPlacer(raw, &p.Type, &p.Fields, &p.parsed)
}

func (p *TreeFoliagePlacer) UnmarshalJSON(raw []byte) error {
	return unmarshalPlacer(raw, &p.Type, &p.Fields, &p.parsed)
}

func unmarshalPlacer(raw []byte, typ *string, fields *map[string]json.RawMessage, parsed **parsedPlacerFields) error {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return err
	}
	name, ok := all["type"]
	if !ok {
		return fmt.Errorf("placer has no type field")
	}
	if err := json.Unmarshal(name, typ); err != nil {
		return err
	}
	delete(all, "type")
	*fields = all
	pf := &parsedPlacerFields{
		scalars:   make(map[string]int, len(all)),
		floats:    make(map[string]float64, len(all)),
		providers: make(map[string]NestedIntProvider, len(all)),
	}
	for k, v := range all {
		var n int
		if err := json.Unmarshal(v, &n); err == nil {
			pf.scalars[k] = n
		}
		var f float64
		if err := json.Unmarshal(v, &f); err == nil {
			pf.floats[k] = f
		}
		if prov, err := parseNestedIntProvider(v); err == nil {
			pf.providers[k] = prov
		}
	}
	*parsed = pf
	return nil
}

// Scalar reads a field the data holds as a plain number; Provider reads one as an
// int provider, which also accepts a bare int as a draw-free constant.
func (p TreeTrunkPlacer) Scalar(name string) (int, bool) {
	if p.parsed != nil {
		v, ok := p.parsed.scalars[name]
		return v, ok
	}
	return scalarField(p.Fields, name)
}

func (p TreeFoliagePlacer) Scalar(name string) (int, bool) {
	if p.parsed != nil {
		v, ok := p.parsed.scalars[name]
		return v, ok
	}
	return scalarField(p.Fields, name)
}

func (p TreeTrunkPlacer) Float(name string) (float64, bool) {
	if p.parsed != nil {
		v, ok := p.parsed.floats[name]
		return v, ok
	}
	raw, ok := p.Fields[name]
	if !ok {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, false
	}
	return f, true
}

func (p TreeFoliagePlacer) Float(name string) (float64, bool) {
	if p.parsed != nil {
		v, ok := p.parsed.floats[name]
		return v, ok
	}
	raw, ok := p.Fields[name]
	if !ok {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, false
	}
	return f, true
}

func (p TreeTrunkPlacer) Provider(name string) (NestedIntProvider, bool, error) {
	if p.parsed != nil {
		if prov, ok := p.parsed.providers[name]; ok {
			return prov, true, nil
		}
		if _, exists := p.Fields[name]; !exists {
			return NestedIntProvider{}, false, nil
		}
	}
	return providerField(p.Fields, name)
}

func (p TreeFoliagePlacer) Provider(name string) (NestedIntProvider, bool, error) {
	if p.parsed != nil {
		if prov, ok := p.parsed.providers[name]; ok {
			return prov, true, nil
		}
		if _, exists := p.Fields[name]; !exists {
			return NestedIntProvider{}, false, nil
		}
	}
	return providerField(p.Fields, name)
}

func scalarField(fields map[string]json.RawMessage, name string) (int, bool) {
	raw, ok := fields[name]
	if !ok {
		return 0, false
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false
	}
	return n, true
}

func providerField(fields map[string]json.RawMessage, name string) (NestedIntProvider, bool, error) {
	raw, ok := fields[name]
	if !ok {
		return NestedIntProvider{}, false, nil
	}
	parsed, err := parseNestedIntProvider(raw)
	if err != nil {
		return NestedIntProvider{}, true, err
	}
	return parsed, true, nil
}

// TreeMinimumSize is two_layers or three_layers - 34 and 5 uses respectively in
// this pack - and keeps its fields raw for the same reason the placers do.
type TreeMinimumSize struct {
	Type   string
	Fields map[string]json.RawMessage

	parsed              bool
	limit               int
	hasLimit            bool
	lowerSize           int
	upperSize           int
	upperLimit          int
	middleSize          int
	minClippedHeight    int
	hasMinClippedHeight bool
}

func (m *TreeMinimumSize) UnmarshalJSON(raw []byte) error {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return err
	}
	name, ok := all["type"]
	if !ok {
		return fmt.Errorf("minimum_size has no type field")
	}
	if err := json.Unmarshal(name, &m.Type); err != nil {
		return err
	}
	delete(all, "type")
	m.Fields = all
	m.parsed = true
	if v, ok := scalarField(all, "limit"); ok {
		m.limit, m.hasLimit = v, true
	} else if v, ok := scalarField(all, "min_limit"); ok {
		m.limit, m.hasLimit = v, true
	} else if m.Type == "minecraft:two_layers_feature_size" || m.Type == "minecraft:three_layers_feature_size" {
		m.limit, m.hasLimit = 1, true
	}
	m.lowerSize, _ = scalarField(all, "lower_size")
	if v, ok := scalarField(all, "upper_size"); ok {
		m.upperSize = v
	} else {
		m.upperSize = 1
	}
	if v, ok := scalarField(all, "upper_limit"); ok {
		m.upperLimit = v
	} else {
		m.upperLimit = 1
	}
	if v, ok := scalarField(all, "middle_size"); ok {
		m.middleSize = v
	} else {
		m.middleSize = 1
	}
	if _, exists := all["min_clipped_height"]; exists {
		m.minClippedHeight, m.hasMinClippedHeight = scalarField(all, "min_clipped_height")
	}
	return nil
}

// SizeAtHeight is FeatureSize.getSizeAtHeight(height, selfHeight). Two facts are
// worth stating because both contradict the parameter names. The callers in
// TreeFeature.getMaxFreeTreeHeight pass (selfHeight, height) - the arguments are in
// the other order from the declaration - and each implementation reads only its
// SECOND parameter, so the value that matters is the y offset from the origin and
// the configured trunk height is ignored by two_layers entirely.
func (m TreeMinimumSize) SizeAtHeight(height, selfHeight int) int {
	if m.parsed {
		limit := m.limit
		if !m.hasLimit {
			limit = 1
		}
		if selfHeight < limit {
			return m.lowerSize
		}
		switch m.Type {
		case "minecraft:two_layers_feature_size":
			return m.upperSize
		case "minecraft:three_layers_feature_size":
			if selfHeight >= height-m.upperLimit {
				return m.upperSize
			}
			return m.middleSize
		}
		return 0
	}
	limit, ok := m.Limit()
	if !ok {
		limit = 1
	}
	lower, _ := scalarField(m.Fields, "lower_size")
	upper, ok := scalarField(m.Fields, "upper_size")
	if !ok {
		upper = 1
	}
	if selfHeight < limit {
		return lower
	}
	switch m.Type {
	case "minecraft:two_layers_feature_size":
		return upper
	case "minecraft:three_layers_feature_size":
		upperLimit, ok := scalarField(m.Fields, "upper_limit")
		if !ok {
			upperLimit = 1
		}
		if selfHeight >= height-upperLimit {
			return upper
		}
		middle, ok := scalarField(m.Fields, "middle_size")
		if !ok {
			middle = 1
		}
		return middle
	}
	return 0
}

// MinClippedHeight is FeatureSize.minClippedHeight: the optional min_clipped_height
// member, present on exactly one kind of tree in the pack. It is what lets a trunk be
// cut short and still place, instead of being refused.
func (m TreeMinimumSize) MinClippedHeight() (int, bool) {
	if m.parsed {
		return m.minClippedHeight, m.hasMinClippedHeight
	}
	if _, ok := m.Fields["min_clipped_height"]; !ok {
		return 0, false
	}
	value, ok := scalarField(m.Fields, "min_clipped_height")
	return value, ok
}

// Limit answers the one thing the size check needs from either variant; the two
// types spell the field differently, which is why it is looked up by name.
func (m TreeMinimumSize) Limit() (int, bool) {
	if m.parsed {
		return m.limit, m.hasLimit
	}
	if v, ok := scalarField(m.Fields, "limit"); ok {
		return v, true
	}
	if v, ok := scalarField(m.Fields, "min_limit"); ok {
		return v, true
	}
	if m.Type == "minecraft:two_layers_feature_size" || m.Type == "minecraft:three_layers_feature_size" {
		return 1, true
	}
	return 0, false
}

// TreeDecorator is one entry of a tree's decorators list. The type is what the
// replay dispatches on; every other member is kept raw because each decorator reads
// a different one - beehive a probability, alter_ground a block state provider - and
// a shared typed struct would have to know all of them.
type TreeDecorator struct {
	Type       string
	Fields     map[string]json.RawMessage
	scalars    map[string]int
	floats     map[string]float64
	directions map[string][]string
}

func (d *TreeDecorator) UnmarshalJSON(data []byte) error {
	var slots map[string]json.RawMessage
	if err := json.Unmarshal(data, &slots); err != nil {
		return err
	}
	out := TreeDecorator{
		Fields:     make(map[string]json.RawMessage, len(slots)),
		scalars:    make(map[string]int, len(slots)),
		floats:     make(map[string]float64, len(slots)),
		directions: make(map[string][]string, len(slots)),
	}
	for name, raw := range slots {
		if name == "type" {
			if err := json.Unmarshal(raw, &out.Type); err != nil {
				return err
			}
			continue
		}
		out.Fields[name] = raw
		var n int
		if err := json.Unmarshal(raw, &n); err == nil {
			out.scalars[name] = n
		}
		var f float64
		if err := json.Unmarshal(raw, &f); err == nil {
			out.floats[name] = f
		}
		var dirs []string
		if err := json.Unmarshal(raw, &dirs); err == nil && len(dirs) > 0 {
			out.directions[name] = dirs
		}
	}
	*d = out
	return nil
}

// Float reads a scalar member of a decorator, which is how beehive's probability
// arrives.
func (d TreeDecorator) Float(name string) (float64, bool) {
	if d.floats != nil {
		v, ok := d.floats[name]
		return v, ok
	}
	raw, ok := d.Fields[name]
	if !ok {
		return 0, false
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false
	}
	return value, true
}

// Scalar reads an integer member of a decorator, such as place_on_ground's tries,
// radius, and height.
func (d TreeDecorator) Scalar(name string) (int, bool) {
	if d.scalars != nil {
		v, ok := d.scalars[name]
		return v, ok
	}
	return scalarField(d.Fields, name)
}

// Provider returns a decorator's raw member for the caller to resolve; evaluating a
// block state provider needs a world, which this package does not have.
func (d TreeDecorator) Provider(name string) (json.RawMessage, bool) {
	raw, ok := d.Fields[name]
	return raw, ok && len(raw) > 0
}

// Directions reads a non-empty string list member of a decorator, which is how
// attached_to_logs's directions arrives.
func (d TreeDecorator) Directions(name string) ([]string, bool) {
	if d.directions != nil {
		v, ok := d.directions[name]
		return v, ok
	}
	raw, ok := d.Fields[name]
	if !ok {
		return nil, false
	}
	var dirs []string
	if err := json.Unmarshal(raw, &dirs); err != nil || len(dirs) == 0 {
		return nil, false
	}
	return dirs, true
}

// HugeMushroomFeatureConfig mirrors HugeMushroomFeatureConfiguration, shared by
// huge_red_mushroom and huge_brown_mushroom.
type HugeMushroomFeatureConfig struct {
	CapProvider   json.RawMessage `json:"cap_provider"`
	StemProvider  json.RawMessage `json:"stem_provider"`
	FoliageRadius int             `json:"foliage_radius"`
	CanPlaceOn    json.RawMessage `json:"can_place_on"`
}

// FallenTreeFeatureConfig mirrors FallenTreeConfiguration.
type FallenTreeFeatureConfig struct {
	TrunkProvider   json.RawMessage
	LogLength       NestedIntProvider
	StumpDecorators []TreeDecorator
	LogDecorators   []TreeDecorator
}

// UniformFloatProvider mirrors net.minecraft.util.valueproviders.UniformFloat
// and ClampedNormalFloat.
type UniformFloatProvider struct {
	Type            string
	Min             float32
	Max             float32
	Mean, Deviation float32
}

// Sample draws one float via Mth.randomBetween or ClampedNormalFloat.sample.
func (p UniformFloatProvider) Sample(random RandomSource) float32 {
	if p.Type == "minecraft:clamped_normal" {
		return SampleClampedNormalFloat(random, p.Mean, p.Deviation, p.Min, p.Max)
	}
	return randomBetween(random, p.Min, p.Max)
}

// SampleClampedNormalFloat mirrors ClampedNormalFloat.sample:
// Mth.clamp(Mth.normal(random, mean, deviation), min, max).
func SampleClampedNormalFloat(random RandomSource, mean, deviation, minVal, maxVal float32) float32 {
	v := mean + float32(random.NextGaussian())*deviation
	if v < minVal {
		return minVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

// LargeDripstoneFeatureConfig mirrors LargeDripstoneConfiguration.
type LargeDripstoneFeatureConfig struct {
	FloorToCeilingSearchRange        int
	ColumnRadiusMin, ColumnRadiusMax int
	HeightScale                      UniformFloatProvider
	MaxColumnRadiusToCaveHeightRatio float32
	StalactiteBluntness              UniformFloatProvider
	StalagmiteBluntness              UniformFloatProvider
	WindSpeed                        UniformFloatProvider
	MinRadiusForWind                 int
	MinBluntnessForWind              float32
}

// MultifaceGrowthFeatureConfig mirrors MultifaceGrowthConfiguration.
type MultifaceGrowthFeatureConfig struct {
	Block             string
	SearchRange       int
	CanPlaceOnFloor   bool
	CanPlaceOnCeiling bool
	CanPlaceOnWall    bool
	ChanceOfSpreading float32
	CanBePlacedOn     []string
}

// SculkPatchFeatureConfig mirrors SculkPatchConfiguration.
type SculkPatchFeatureConfig struct {
	ChargeCount      int
	AmountPerCharge  int
	SpreadAttempts   int
	GrowthRounds     int
	SpreadRounds     int
	ExtraRareGrowths NestedIntProvider
	CatalystChance   float32
}

// DripstoneClusterFeatureConfig mirrors DripstoneClusterConfiguration.
type DripstoneClusterFeatureConfig struct {
	FloorToCeilingSearchRange                           int
	Height                                              NestedIntProvider
	Radius                                              NestedIntProvider
	MaxStalagmiteStalactiteHeightDiff                   int
	HeightDeviation                                     int
	DripstoneBlockLayerThickness                        NestedIntProvider
	Density                                             UniformFloatProvider
	Wetness                                             UniformFloatProvider
	ChanceOfDripstoneColumnAtMaxDistanceFromCenter      float32
	MaxDistanceFromEdgeAffectingChanceOfDripstoneColumn int
	MaxDistanceFromCenterAffectingHeightBias            int
}

// PointedDripstoneFeatureConfig mirrors PointedDripstoneConfiguration.
type PointedDripstoneFeatureConfig struct {
	ChanceOfTallerDripstone   float32
	ChanceOfDirectionalSpread float32
	ChanceOfSpreadRadius2     float32
	ChanceOfSpreadRadius3     float32
}

type FeatureRef struct {
	Name      string
	Placement []PlacementModifier
}

type RandomSelectorConfig struct {
	Default  FeatureRef
	Features []RandomSelectorEntry
}

// RandomBooleanSelectorConfig is the random_boolean_selector feature: one
// nextBoolean draw picks feature_true or feature_false, placed at the same
// position with the same random.
type RandomBooleanSelectorConfig struct {
	FeatureTrue  FeatureRef
	FeatureFalse FeatureRef
}

type RandomSelectorEntry struct {
	Chance  float32
	Feature FeatureRef
}

type BlockState struct {
	Name       string            `json:"Name"`
	Properties map[string]string `json:"Properties"`
}

type PlacementPlan struct {
	Count              CountProvider
	RarityChance       int
	HeightDistribution string
	HeightPlateau      int
	MinY               HeightProvider
	MaxY               HeightProvider
}

type FeaturePosition struct {
	X, Y, Z int
}

type PlacementContext struct {
	MinY, Height   int
	BiomeAllows    func(FeaturePosition) bool
	HeightAt       func(string, int, int) int
	BlockPredicate func(json.RawMessage, FeaturePosition) (bool, error)
}

type CountProvider struct {
	Min, Max int
	Weighted []WeightedInt
	// ClampedTo names the clamped provider's inner source (a plain
	// provider), clamped to [Min, Max] after its draw.
	ClampedTo *CountProvider
}

type WeightedInt struct {
	Value, Weight int
}

type HeightProvider struct {
	Absolute    *int
	AboveBottom *int
	BelowTop    *int
}

func (p CountProvider) Sample(r RandomSource) int {
	if p.ClampedTo != nil {
		// ClampedInt: clamp(source.sample(r), min, max).
		value := p.ClampedTo.Sample(r)
		if value < p.Min {
			return p.Min
		}
		if value > p.Max {
			return p.Max
		}
		return value
	}
	if len(p.Weighted) > 0 {
		total := 0
		for _, entry := range p.Weighted {
			total += entry.Weight
		}
		if total <= 0 {
			return 0
		}
		roll := int(r.NextIntN(int32(total)))
		for _, entry := range p.Weighted {
			if roll < entry.Weight {
				return entry.Value
			}
			roll -= entry.Weight
		}
	}
	if p.Max <= p.Min {
		return p.Min
	}
	return p.Min + int(r.NextIntN(int32(p.Max-p.Min+1)))
}

func (p HeightProvider) Resolve(minY, height int) int {
	if p.Absolute != nil {
		return *p.Absolute
	}
	if p.AboveBottom != nil {
		return minY + *p.AboveBottom
	}
	if p.BelowTop != nil {
		return minY + height - 1 - *p.BelowTop
	}
	return minY
}

func (p PlacementPlan) SampleY(r RandomSource, minY, height int) int {
	lo, hi := p.MinY.Resolve(minY, height), p.MaxY.Resolve(minY, height)
	if hi < lo {
		return lo
	}
	span := hi - lo + 1
	if p.HeightDistribution == "minecraft:trapezoid" {
		rangeSize := span - 1
		if rangeSize <= 0 {
			return lo
		}
		plateau := p.HeightPlateau
		if plateau < 0 || plateau > rangeSize {
			return lo
		}
		left := (rangeSize - plateau) / 2
		right := rangeSize - left
		return lo + int(r.NextIntN(int32(right+1))) + int(r.NextIntN(int32(left+1)))
	}
	if p.HeightDistribution == "minecraft:very_biased_to_bottom" {
		const inner = 8
		if span-inner <= 0 {
			return lo
		}
		first := int(r.NextIntN(int32(span - inner)))
		return lo + int(r.NextIntN(int32(first+inner)))
	}
	return lo + int(r.NextIntN(int32(span)))
}

func DecorationRandom(seed int64, chunkX, chunkZ int) (*WorldgenRandom, int64) {
	random := NewWorldgenRandom(0)
	decorationSeed := random.SetDecorationSeed(seed, chunkX<<4, chunkZ<<4)
	return random, decorationSeed
}

type BiomeGeneration struct {
	Carvers  json.RawMessage `json:"carvers"`
	Features [][]string      `json:"features"`
}

var (
	featureSetOnce sync.Once
	featureSet     *FeatureSet
	featureSetErr  error
)

// LoadFeatureSet parses and validates the committed vanilla 26.1.2 feature
// datapack archive. The result is immutable and shared by all worlds.
func LoadFeatureSet() (*FeatureSet, error) {
	featureSetOnce.Do(func() { featureSet, featureSetErr = loadFeatureSet(featureData) })
	return featureSet, featureSetErr
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// FeatureSteps mirrors FeatureSorter.buildFeaturesPerStep. biomeOrder must be
// BiomeSource.possibleBiomes encounter order because it assigns the stable
// identity used to break otherwise unconstrained graph ties.
func (s *FeatureSet) FeatureSteps(biomeOrder []string) ([][]string, error) {
	s.cacheMu.RLock()
	if s.lastSteps != nil && stringSliceEqual(biomeOrder, s.lastStepsOrder) {
		steps := s.lastSteps
		s.cacheMu.RUnlock()
		return steps, nil
	}
	s.cacheMu.RUnlock()

	cacheKey := strings.Join(biomeOrder, "\x00")
	s.cacheMu.RLock()
	if steps, ok := s.stepsCache[cacheKey]; ok {
		s.cacheMu.RUnlock()
		return steps, nil
	}
	s.cacheMu.RUnlock()

	type node struct {
		stage, identity int
		name            string
	}
	identities := make(map[string]int)
	nodes := make(map[[2]int]node)
	edges := make(map[[2]int]map[[2]int]bool)
	maxStages := 0
	for _, biomeName := range biomeOrder {
		biome, ok := s.Biomes[biomeName]
		if !ok {
			continue
		}
		if len(biome.Features) > maxStages {
			maxStages = len(biome.Features)
		}
		var previous [2]int
		hasPrevious := false
		for stage, names := range biome.Features {
			for _, name := range names {
				identity, ok := identities[name]
				if !ok {
					identity = len(identities)
					identities[name] = identity
				}
				key := [2]int{stage, identity}
				nodes[key] = node{stage: stage, identity: identity, name: name}
				if hasPrevious {
					if edges[previous] == nil {
						edges[previous] = make(map[[2]int]bool)
					}
					edges[previous][key] = true
				}
				previous, hasPrevious = key, true
			}
		}
	}
	less := func(a, b [2]int) bool {
		return a[0] < b[0] || a[0] == b[0] && a[1] < b[1]
	}
	keys := make([][2]int, 0, len(nodes))
	for key := range nodes {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return less(keys[i], keys[j]) })
	visited := make(map[[2]int]bool)
	visiting := make(map[[2]int]bool)
	ordered := make([][2]int, 0, len(keys))
	var visit func([2]int) error
	visit = func(key [2]int) error {
		if visited[key] {
			return nil
		}
		if visiting[key] {
			return fmt.Errorf("worldgen: feature order cycle")
		}
		visiting[key] = true
		children := make([][2]int, 0, len(edges[key]))
		for child := range edges[key] {
			children = append(children, child)
		}
		sort.Slice(children, func(i, j int) bool { return less(children[i], children[j]) })
		for _, child := range children {
			if err := visit(child); err != nil {
				return err
			}
		}
		delete(visiting, key)
		visited[key] = true
		ordered = append(ordered, key)
		return nil
	}
	for _, key := range keys {
		if err := visit(key); err != nil {
			return nil, err
		}
	}
	steps := make([][]string, maxStages)
	for i := len(ordered) - 1; i >= 0; i-- {
		n := nodes[ordered[i]]
		steps[n.stage] = append(steps[n.stage], n.name)
	}
	s.cacheMu.Lock()
	if s.stepsCache == nil {
		s.stepsCache = make(map[string][][]string)
	} else if len(s.stepsCache) >= maxFeatureSetStepsCacheEntries {
		clear(s.stepsCache)
	}
	s.stepsCache[cacheKey] = steps
	s.lastStepsOrder = append([]string(nil), biomeOrder...)
	s.lastSteps = steps
	s.cacheMu.Unlock()
	return steps, nil
}

func IndexedFeatures(step []string, wanted map[string]bool) []IndexedFeature {
	result := make([]IndexedFeature, 0, len(wanted))
	for index, name := range step {
		if wanted[name] {
			result = append(result, IndexedFeature{Name: name, Index: index})
		}
	}
	return result
}

// FeatureSchedule returns the features present in the union of sourceBiomes
// for one decoration stage. allBiomeOrder is the complete BiomeSource encounter
// order used to build FeatureSorter, not the order of the local source region.
// The result is stable and contains each placed feature at most once.
func (s *FeatureSet) FeatureSchedule(allBiomeOrder, sourceBiomes []string, stage int) ([]ScheduledFeature, error) {
	steps, err := s.FeatureSteps(allBiomeOrder)
	if err != nil {
		return nil, err
	}
	if stage < 0 || stage >= len(steps) {
		return nil, fmt.Errorf("worldgen: feature stage %d out of range", stage)
	}
	wanted := make(map[string]bool)
	for _, biomeName := range sourceBiomes {
		biome, ok := s.Biomes[biomeName]
		if !ok || stage >= len(biome.Features) {
			continue
		}
		for _, name := range biome.Features[stage] {
			wanted[name] = true
		}
	}
	result := make([]ScheduledFeature, 0, len(wanted))
	for index, name := range steps[stage] {
		if wanted[name] {
			result = append(result, ScheduledFeature{Name: name, Index: index})
		}
	}
	return result, nil
}

func (s *FeatureSet) Ore(name string) (OreFeatureConfig, error) {
	if v, ok := s.getCachedConfig("ore", name); ok {
		return v.(OreFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:ore" {
		return OreFeatureConfig{}, fmt.Errorf("worldgen: %s is not an ore feature", name)
	}
	var config OreFeatureConfig
	if err := json.Unmarshal(configured.Config, &config); err != nil {
		return OreFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	if config.Size < 1 || len(config.Targets) == 0 {
		return OreFeatureConfig{}, fmt.Errorf("worldgen: invalid ore config %s", name)
	}
	s.setCachedConfig("ore", name, config)
	return config, nil
}

func (s *FeatureSet) Disk(name string) (DiskFeatureConfig, error) {
	if v, ok := s.getCachedConfig("disk", name); ok {
		return v.(DiskFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:disk" {
		return DiskFeatureConfig{}, fmt.Errorf("worldgen: %s is not a disk feature", name)
	}
	var raw struct {
		HalfHeight int `json:"half_height"`
		Radius     struct {
			Min int `json:"min_inclusive"`
			Max int `json:"max_inclusive"`
		} `json:"radius"`
		StateProvider json.RawMessage `json:"state_provider"`
		Target        struct {
			Blocks json.RawMessage `json:"blocks"`
		} `json:"target"`
	}
	if err := json.Unmarshal(configured.Config, &raw); err != nil {
		return DiskFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	var provider struct {
		State    BlockState `json:"state"`
		Fallback struct {
			State BlockState `json:"state"`
		} `json:"fallback"`
		Rules []struct {
			Then struct {
				State BlockState `json:"state"`
			} `json:"then"`
			IfTrue json.RawMessage `json:"if_true"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(raw.StateProvider, &provider); err != nil {
		return DiskFeatureConfig{}, fmt.Errorf("worldgen: decode %s state provider: %w", name, err)
	}
	config := DiskFeatureConfig{
		HalfHeight: raw.HalfHeight, RadiusMin: raw.Radius.Min, RadiusMax: raw.Radius.Max,
		State: provider.State, Fallback: provider.Fallback.State,
	}
	for _, rule := range provider.Rules {
		if len(rule.IfTrue) != 0 && rule.Then.State.Name != "" {
			config.Rules = append(config.Rules, DiskStateRule{IfTrue: rule.IfTrue, Then: rule.Then.State})
		}
	}
	if config.State.Name == "" {
		config.State = config.Fallback
	}
	if config.Fallback.Name == "" {
		config.Fallback = config.State
	}
	if len(raw.Target.Blocks) == 0 {
		return DiskFeatureConfig{}, fmt.Errorf("worldgen: invalid disk target %s", name)
	}
	if raw.Target.Blocks[0] == '"' {
		var block string
		if err := json.Unmarshal(raw.Target.Blocks, &block); err != nil {
			return DiskFeatureConfig{}, err
		}
		config.Targets = []string{block}
	} else if err := json.Unmarshal(raw.Target.Blocks, &config.Targets); err != nil {
		return DiskFeatureConfig{}, err
	}
	if config.RadiusMin < 0 || config.RadiusMax < config.RadiusMin || config.State.Name == "" || len(config.Targets) == 0 {
		return DiskFeatureConfig{}, fmt.Errorf("worldgen: invalid disk config %s", name)
	}
	s.setCachedConfig("disk", name, config)
	return config, nil
}

func (s *FeatureSet) Geode(name string) (GeodeFeatureConfig, error) {
	if v, ok := s.getCachedConfig("geode", name); ok {
		return v.(GeodeFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:geode" {
		return GeodeFeatureConfig{}, fmt.Errorf("worldgen: %s is not a geode feature", name)
	}
	var raw struct {
		Distribution struct {
			Min int `json:"min_inclusive"`
			Max int `json:"max_inclusive"`
		} `json:"distribution_points"`
		OuterWall struct {
			Min int `json:"min_inclusive"`
			Max int `json:"max_inclusive"`
		} `json:"outer_wall_distance"`
		PointOffset struct {
			Min int `json:"min_inclusive"`
			Max int `json:"max_inclusive"`
		} `json:"point_offset"`
		MinGenOffset int `json:"min_gen_offset"`
		MaxGenOffset int `json:"max_gen_offset"`
		Crack        struct {
			BaseSize    float64 `json:"base_crack_size"`
			PointOffset int     `json:"crack_point_offset"`
			Chance      float64 `json:"generate_crack_chance"`
		} `json:"crack"`
		Layers struct {
			Filling float64 `json:"filling"`
			Inner   float64 `json:"inner_layer"`
			Middle  float64 `json:"middle_layer"`
			Outer   float64 `json:"outer_layer"`
		} `json:"layers"`
		NoiseMultiplier              float64 `json:"noise_multiplier"`
		InvalidBlocksThreshold       int     `json:"invalid_blocks_threshold"`
		CannotReplace                string  `json:"cannot_replace"`
		InvalidBlocks                string  `json:"invalid_blocks"`
		UseAlternateLayerChance      float64 `json:"use_alternate_layer0_chance"`
		PlacementsRequireAlternate   bool    `json:"placements_require_layer0_alternate"`
		UsePotentialPlacementsChance float64 `json:"use_potential_placements_chance"`
	}
	if err := json.Unmarshal(configured.Config, &raw); err != nil {
		return GeodeFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	var wrapper struct {
		Blocks map[string]json.RawMessage `json:"blocks"`
	}
	if err := json.Unmarshal(configured.Config, &wrapper); err != nil {
		return GeodeFeatureConfig{}, err
	}
	blocks := wrapper.Blocks
	stateProvider := func(key string) BlockState {
		var provider struct {
			State BlockState `json:"state"`
		}
		_ = json.Unmarshal(blocks[key], &provider)
		return provider.State
	}
	stringValue := func(key string) string {
		var value string
		_ = json.Unmarshal(blocks[key], &value)
		return value
	}
	var innerPlacements []BlockState
	_ = json.Unmarshal(blocks["inner_placements"], &innerPlacements)
	config := GeodeFeatureConfig{
		Outer:           stateProvider("outer_layer_provider"),
		Middle:          stateProvider("middle_layer_provider"),
		Inner:           stateProvider("inner_layer_provider"),
		Filling:         stateProvider("filling_provider"),
		AlternateInner:  stateProvider("alternate_inner_layer_provider"),
		InnerPlacements: innerPlacements,
		DistributionMin: raw.Distribution.Min, DistributionMax: raw.Distribution.Max,
		OuterWallMin: raw.OuterWall.Min, OuterWallMax: raw.OuterWall.Max,
		PointOffsetMin: raw.PointOffset.Min, PointOffsetMax: raw.PointOffset.Max,
		MinGenOffset: raw.MinGenOffset, MaxGenOffset: raw.MaxGenOffset,
		FillingLayer: raw.Layers.Filling, InnerLayer: raw.Layers.Inner,
		MiddleLayer: raw.Layers.Middle, OuterLayer: raw.Layers.Outer,
		NoiseMultiplier: raw.NoiseMultiplier, InvalidBlocksThreshold: raw.InvalidBlocksThreshold,
		CannotReplaceTag: stringValue("cannot_replace"), InvalidBlocksTag: stringValue("invalid_blocks"),
		CrackChance: raw.Crack.Chance, BaseCrackSize: raw.Crack.BaseSize,
		CrackPointOffset:             raw.Crack.PointOffset,
		UseAlternateLayerChance:      raw.UseAlternateLayerChance,
		PlacementsRequireAlternate:   raw.PlacementsRequireAlternate,
		UsePotentialPlacementsChance: raw.UsePotentialPlacementsChance,
	}
	if config.Outer.Name == "" || config.Middle.Name == "" || config.Inner.Name == "" || config.Filling.Name == "" ||
		config.DistributionMin < 1 || config.DistributionMax < config.DistributionMin ||
		config.OuterWallMin < 1 || config.OuterWallMax < config.OuterWallMin ||
		config.PointOffsetMin < 0 || config.PointOffsetMax < config.PointOffsetMin ||
		config.MaxGenOffset < config.MinGenOffset || config.FillingLayer <= 0 || config.InnerLayer <= 0 ||
		config.MiddleLayer <= 0 || config.OuterLayer <= 0 || config.NoiseMultiplier < 0 ||
		config.InvalidBlocksThreshold < 0 || config.CannotReplaceTag == "" || config.InvalidBlocksTag == "" {
		return GeodeFeatureConfig{}, fmt.Errorf("worldgen: invalid geode config %s", name)
	}
	s.setCachedConfig("geode", name, config)
	return config, nil
}

func (s *FeatureSet) VegetationPatch(name string) (VegetationPatchFeatureConfig, error) {
	if v, ok := s.getCachedConfig("vegetation_patch", name); ok {
		return v.(VegetationPatchFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:vegetation_patch" && configured.Type != "minecraft:waterlogged_vegetation_patch" {
		return VegetationPatchFeatureConfig{}, fmt.Errorf("worldgen: %s is not a vegetation patch", name)
	}
	var raw struct {
		Surface                string          `json:"surface"`
		Depth                  json.RawMessage `json:"depth"`
		XZRadius               json.RawMessage `json:"xz_radius"`
		VerticalRange          int             `json:"vertical_range"`
		ExtraBottomBlockChance float32         `json:"extra_bottom_block_chance"`
		ExtraEdgeColumnChance  float32         `json:"extra_edge_column_chance"`
		VegetationChance       float32         `json:"vegetation_chance"`
		Replaceable            string          `json:"replaceable"`
		GroundState            struct {
			State BlockState `json:"state"`
		} `json:"ground_state"`
		VegetationFeature json.RawMessage `json:"vegetation_feature"`
	}
	if err := json.Unmarshal(configured.Config, &raw); err != nil {
		return VegetationPatchFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	depth, err := parseIntProvider(raw.Depth)
	if err != nil || len(depth.Weighted) != 0 {
		return VegetationPatchFeatureConfig{}, fmt.Errorf("worldgen: %s depth: %w", name, err)
	}
	radius, err := parseIntProvider(raw.XZRadius)
	if err != nil || len(radius.Weighted) != 0 {
		return VegetationPatchFeatureConfig{}, fmt.Errorf("worldgen: %s radius: %w", name, err)
	}
	vegetation, err := s.parseFeatureRef(raw.VegetationFeature)
	if err != nil {
		return VegetationPatchFeatureConfig{}, fmt.Errorf("worldgen: %s vegetation: %w", name, err)
	}
	config := VegetationPatchFeatureConfig{
		Surface: raw.Surface, DepthMin: depth.Min, DepthMax: depth.Max,
		XZRadiusMin: radius.Min, XZRadiusMax: radius.Max, VerticalRange: raw.VerticalRange,
		ExtraBottomBlockChance: raw.ExtraBottomBlockChance, ExtraEdgeColumnChance: raw.ExtraEdgeColumnChance,
		VegetationChance: raw.VegetationChance, Ground: raw.GroundState.State,
		ReplaceableTag: raw.Replaceable, Vegetation: vegetation,
	}
	if config.Surface != "floor" && config.Surface != "ceiling" || config.DepthMin < 1 ||
		config.DepthMax < config.DepthMin || config.XZRadiusMin < 1 || config.XZRadiusMax < config.XZRadiusMin ||
		config.VerticalRange < 1 || config.Ground.Name == "" || config.ReplaceableTag == "" {
		return VegetationPatchFeatureConfig{}, fmt.Errorf("worldgen: invalid vegetation patch %s", name)
	}
	s.setCachedConfig("vegetation_patch", name, config)
	return config, nil
}

// BlockBlobFeatureConfig is the block_blob configuration: the one state to paint and
// the predicate that says the blob has found ground it may sit on. There is no radius,
// size or provider list, which is why the feature is small enough to port exactly.
type BlockBlobFeatureConfig struct {
	State      BlockState      `json:"state"`
	CanPlaceOn json.RawMessage `json:"can_place_on"`
}

// BlockBlob decodes a block_blob configured feature - forest_rock, the mossy
// cobblestone boulder of old-growth taiga and a few other biomes.
func (s *FeatureSet) BlockBlob(name string) (BlockBlobFeatureConfig, error) {
	if v, ok := s.getCachedConfig("block_blob", name); ok {
		return v.(BlockBlobFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:block_blob" {
		return BlockBlobFeatureConfig{}, fmt.Errorf("worldgen: %s is not a block blob feature", name)
	}
	var config BlockBlobFeatureConfig
	if err := json.Unmarshal(configured.Config, &config); err != nil {
		return BlockBlobFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	if config.State.Name == "" {
		return BlockBlobFeatureConfig{}, fmt.Errorf("worldgen: %s has a block blob without a state", name)
	}
	if len(config.CanPlaceOn) == 0 {
		return BlockBlobFeatureConfig{}, fmt.Errorf("worldgen: %s has a block blob without can_place_on", name)
	}
	s.setCachedConfig("block_blob", name, config)
	return config, nil
}

func (s *FeatureSet) SimpleBlock(name string) (SimpleBlockFeatureConfig, error) {
	if v, ok := s.getCachedConfig("simple_block", name); ok {
		return v.(SimpleBlockFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:simple_block" {
		return SimpleBlockFeatureConfig{}, fmt.Errorf("worldgen: %s is not a simple block feature", name)
	}
	var raw struct {
		ToPlace json.RawMessage `json:"to_place"`
	}
	if err := json.Unmarshal(configured.Config, &raw); err != nil {
		return SimpleBlockFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	provider, err := parseStateProviderSpec(raw.ToPlace)
	if err != nil {
		return SimpleBlockFeatureConfig{}, fmt.Errorf("worldgen: decode %s provider: %w", name, err)
	}
	config := SimpleBlockFeatureConfig{Provider: provider}
	switch provider.Type {
	case "simple":
		config.States = []WeightedBlockState{{State: provider.State, Weight: 1}}
	case "weighted":
		for _, entry := range provider.Entries {
			if entry.Weight > 0 && entry.State.Name != "" {
				config.States = append(config.States, entry)
			}
		}
		if len(config.States) == 0 {
			return SimpleBlockFeatureConfig{}, fmt.Errorf("worldgen: invalid simple block feature %s", name)
		}
	case "noise", "noise_threshold", "dual_noise", "randomized_int":
		// Sampled via Provider.SampleStateAt at placement time.
	default:
		return SimpleBlockFeatureConfig{}, fmt.Errorf("worldgen: unsupported simple block provider %q", provider.Type)
	}
	s.setCachedConfig("simple_block", name, config)
	return config, nil
}

func (s *FeatureSet) UnderwaterMagma(name string) (UnderwaterMagmaFeatureConfig, error) {
	if v, ok := s.getCachedConfig("underwater_magma", name); ok {
		return v.(UnderwaterMagmaFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:underwater_magma" {
		return UnderwaterMagmaFeatureConfig{}, fmt.Errorf("worldgen: %s is not underwater magma", name)
	}
	var config UnderwaterMagmaFeatureConfig
	if err := json.Unmarshal(configured.Config, &config); err != nil {
		return UnderwaterMagmaFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	if config.FloorSearchRange < 1 || config.PlacementRadiusAroundFloor < 0 ||
		config.PlacementProbability <= 0 || config.PlacementProbability > 1 {
		return UnderwaterMagmaFeatureConfig{}, fmt.Errorf("worldgen: invalid underwater magma config %s", name)
	}
	s.setCachedConfig("underwater_magma", name, config)
	return config, nil
}

func (s *FeatureSet) Probability(name string) (ProbabilityFeatureConfig, error) {
	if v, ok := s.getCachedConfig("probability", name); ok {
		return v.(ProbabilityFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || (configured.Type != "minecraft:seagrass" && configured.Type != "minecraft:bamboo") {
		return ProbabilityFeatureConfig{}, fmt.Errorf("worldgen: %s is not a probability feature", name)
	}
	var config ProbabilityFeatureConfig
	if err := json.Unmarshal(configured.Config, &config); err != nil {
		return ProbabilityFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	if config.Probability < 0 || config.Probability > 1 {
		return ProbabilityFeatureConfig{}, fmt.Errorf("worldgen: invalid probability %s", name)
	}
	s.setCachedConfig("probability", name, config)
	return config, nil
}

type BlockStateFeatureConfig struct {
	State BlockState `json:"state"`
}

func (s *FeatureSet) BlockStateConfig(name string) (BlockStateFeatureConfig, error) {
	if v, ok := s.getCachedConfig("block_state", name); ok {
		return v.(BlockStateFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:iceberg" {
		return BlockStateFeatureConfig{}, fmt.Errorf("worldgen: %s is not an iceberg feature", name)
	}
	var config BlockStateFeatureConfig
	if err := json.Unmarshal(configured.Config, &config); err != nil {
		return BlockStateFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	if config.State.Name == "" {
		return BlockStateFeatureConfig{}, fmt.Errorf("worldgen: invalid block_state config %s", name)
	}
	s.setCachedConfig("block_state", name, config)
	return config, nil
}

type FossilFeatureConfig struct {
	FossilStructures       []string `json:"fossil_structures"`
	OverlayStructures      []string `json:"overlay_structures"`
	FossilProcessors       string   `json:"fossil_processors"`
	OverlayProcessors      string   `json:"overlay_processors"`
	MaxEmptyCornersAllowed int      `json:"max_empty_corners_allowed"`
}

func (s *FeatureSet) Fossil(name string) (FossilFeatureConfig, error) {
	if v, ok := s.getCachedConfig("fossil", name); ok {
		return v.(FossilFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:fossil" {
		return FossilFeatureConfig{}, fmt.Errorf("worldgen: %s is not a fossil feature", name)
	}
	var config FossilFeatureConfig
	if err := json.Unmarshal(configured.Config, &config); err != nil {
		return FossilFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	if len(config.FossilStructures) == 0 || len(config.FossilStructures) != len(config.OverlayStructures) {
		return FossilFeatureConfig{}, fmt.Errorf("worldgen: invalid fossil config %s", name)
	}
	s.setCachedConfig("fossil", name, config)
	return config, nil
}

type SpikeFeatureConfig struct {
	State      BlockState      `json:"state"`
	CanPlaceOn json.RawMessage `json:"can_place_on"`
	CanReplace json.RawMessage `json:"can_replace"`
}

func (s *FeatureSet) Spike(name string) (SpikeFeatureConfig, error) {
	if v, ok := s.getCachedConfig("spike", name); ok {
		return v.(SpikeFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:spike" {
		return SpikeFeatureConfig{}, fmt.Errorf("worldgen: %s is not a spike feature", name)
	}
	var config SpikeFeatureConfig
	if err := json.Unmarshal(configured.Config, &config); err != nil {
		return SpikeFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	if config.State.Name == "" || len(config.CanPlaceOn) == 0 || len(config.CanReplace) == 0 {
		return SpikeFeatureConfig{}, fmt.Errorf("worldgen: invalid spike config %s", name)
	}
	s.setCachedConfig("spike", name, config)
	return config, nil
}

type RootSystemFeatureConfig struct {
	AllowedTreePosition          json.RawMessage
	AllowedVerticalWaterForTree  int
	Feature                      FeatureRef
	HangingRootPlacementAttempts int
	HangingRootRadius            int
	HangingRootStateProvider     StateProviderSpec
	HangingRootsVerticalSpan     int
	RequiredVerticalSpaceForTree int
	RootColumnMaxHeight          int
	RootPlacementAttempts        int
	RootRadius                   int
	RootReplaceable              string
	RootStateProvider            StateProviderSpec
}

func (s *FeatureSet) RootSystem(name string) (RootSystemFeatureConfig, error) {
	if v, ok := s.getCachedConfig("root_system", name); ok {
		return v.(RootSystemFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:root_system" {
		return RootSystemFeatureConfig{}, fmt.Errorf("worldgen: %s is not a root_system feature", name)
	}
	var raw struct {
		AllowedTreePosition          json.RawMessage `json:"allowed_tree_position"`
		AllowedVerticalWaterForTree  int             `json:"allowed_vertical_water_for_tree"`
		Feature                      json.RawMessage `json:"feature"`
		HangingRootPlacementAttempts int             `json:"hanging_root_placement_attempts"`
		HangingRootRadius            int             `json:"hanging_root_radius"`
		HangingRootStateProvider     json.RawMessage `json:"hanging_root_state_provider"`
		HangingRootsVerticalSpan     int             `json:"hanging_roots_vertical_span"`
		RequiredVerticalSpaceForTree int             `json:"required_vertical_space_for_tree"`
		RootColumnMaxHeight          int             `json:"root_column_max_height"`
		RootPlacementAttempts        int             `json:"root_placement_attempts"`
		RootRadius                   int             `json:"root_radius"`
		RootReplaceable              string          `json:"root_replaceable"`
		RootStateProvider            json.RawMessage `json:"root_state_provider"`
	}
	if err := json.Unmarshal(configured.Config, &raw); err != nil {
		return RootSystemFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	featRef, err := s.parseFeatureRef(raw.Feature)
	if err != nil {
		return RootSystemFeatureConfig{}, fmt.Errorf("worldgen: %s feature: %w", name, err)
	}
	hangingProv, err := parseStateProviderSpec(raw.HangingRootStateProvider)
	if err != nil {
		return RootSystemFeatureConfig{}, fmt.Errorf("worldgen: %s hanging_root_state_provider: %w", name, err)
	}
	rootProv, err := parseStateProviderSpec(raw.RootStateProvider)
	if err != nil {
		return RootSystemFeatureConfig{}, fmt.Errorf("worldgen: %s root_state_provider: %w", name, err)
	}
	config := RootSystemFeatureConfig{
		AllowedTreePosition:          raw.AllowedTreePosition,
		AllowedVerticalWaterForTree:  raw.AllowedVerticalWaterForTree,
		Feature:                      featRef,
		HangingRootPlacementAttempts: raw.HangingRootPlacementAttempts,
		HangingRootRadius:            raw.HangingRootRadius,
		HangingRootStateProvider:     hangingProv,
		HangingRootsVerticalSpan:     raw.HangingRootsVerticalSpan,
		RequiredVerticalSpaceForTree: raw.RequiredVerticalSpaceForTree,
		RootColumnMaxHeight:          raw.RootColumnMaxHeight,
		RootPlacementAttempts:        raw.RootPlacementAttempts,
		RootRadius:                   raw.RootRadius,
		RootReplaceable:              raw.RootReplaceable,
		RootStateProvider:            rootProv,
	}
	s.setCachedConfig("root_system", name, config)
	return config, nil
}

type CountFeatureConfig struct {
	Count NestedIntProvider
}

func (s *FeatureSet) Count(name string) (CountFeatureConfig, error) {
	if v, ok := s.getCachedConfig("count", name); ok {
		return v.(CountFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:sea_pickle" {
		return CountFeatureConfig{}, fmt.Errorf("worldgen: %s is not a sea_pickle feature", name)
	}
	var raw struct {
		Count json.RawMessage `json:"count"`
	}
	if err := json.Unmarshal(configured.Config, &raw); err != nil {
		return CountFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	cnt, err := parseNestedIntProvider(raw.Count)
	if err != nil {
		return CountFeatureConfig{}, fmt.Errorf("worldgen: %s count: %w", name, err)
	}
	config := CountFeatureConfig{Count: cnt}
	s.setCachedConfig("count", name, config)
	return config, nil
}

func (s *FeatureSet) Spring(name string) (SpringFeatureConfig, error) {
	if v, ok := s.getCachedConfig("spring", name); ok {
		return v.(SpringFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:spring_feature" {
		return SpringFeatureConfig{}, fmt.Errorf("worldgen: %s is not a spring feature", name)
	}
	var config SpringFeatureConfig
	if err := json.Unmarshal(configured.Config, &config); err != nil {
		return SpringFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	if config.State.Name == "" || len(config.ValidBlocks) == 0 || config.HoleCount < 0 || config.RockCount < 0 {
		return SpringFeatureConfig{}, fmt.Errorf("worldgen: invalid spring config %s", name)
	}
	s.setCachedConfig("spring", name, config)
	return config, nil
}

// trunkPlacerSpec and foliagePlacerSpec are the field sets of every placer type,
// measured by enumerating the 39 minecraft:tree configured features in the embedded
// pack - 9 trunk types, 11 foliage types, and for each the exact set of members it
// carries (no type in this pack omits a field its siblings have, so every entry is
// required rather than split into required/optional).
//
// These lists were first written from recollection and were wrong twice: they
// invented large_oak_foliage_placer, which does not exist; omitted acacia, bush,
// jungle and mega_jungle; and gave spruce a `height` it has never had while hiding
// its real `trunk_height`. TestTreePlacerSpecMatchesThePack therefore re-derives
// them from the data on every run instead of trusting the comment above them.
var trunkPlacerSpec = map[string][]string{
	"minecraft:straight_trunk_placer":          {"base_height", "height_rand_a", "height_rand_b"},
	"minecraft:dark_oak_trunk_placer":          {"base_height", "height_rand_a", "height_rand_b"},
	"minecraft:fancy_trunk_placer":             {"base_height", "height_rand_a", "height_rand_b"},
	"minecraft:forking_trunk_placer":           {"base_height", "height_rand_a", "height_rand_b"},
	"minecraft:giant_trunk_placer":             {"base_height", "height_rand_a", "height_rand_b"},
	"minecraft:mega_jungle_trunk_placer":       {"base_height", "height_rand_a", "height_rand_b"},
	"minecraft:bending_trunk_placer":           {"base_height", "bend_length", "height_rand_a", "height_rand_b", "min_height_for_leaves"},
	"minecraft:cherry_trunk_placer":            {"base_height", "branch_count", "branch_end_offset_from_top", "branch_horizontal_length", "branch_start_offset_from_top", "height_rand_a", "height_rand_b"},
	"minecraft:upwards_branching_trunk_placer": {"base_height", "can_grow_through", "extra_branch_length", "extra_branch_steps", "height_rand_a", "height_rand_b", "place_branch_per_log_probability"},
}

var foliagePlacerSpec = map[string][]string{
	"minecraft:acacia_foliage_placer":        {"offset", "radius"},
	"minecraft:blob_foliage_placer":          {"height", "offset", "radius"},
	"minecraft:bush_foliage_placer":          {"height", "offset", "radius"},
	"minecraft:cherry_foliage_placer":        {"corner_hole_chance", "hanging_leaves_chance", "hanging_leaves_extension_chance", "height", "offset", "radius", "wide_bottom_layer_hole_chance"},
	"minecraft:dark_oak_foliage_placer":      {"offset", "radius"},
	"minecraft:fancy_foliage_placer":         {"height", "offset", "radius"},
	"minecraft:jungle_foliage_placer":        {"height", "offset", "radius"},
	"minecraft:mega_pine_foliage_placer":     {"crown_height", "offset", "radius"},
	"minecraft:pine_foliage_placer":          {"height", "offset", "radius"},
	"minecraft:random_spread_foliage_placer": {"foliage_height", "leaf_placement_attempts", "offset", "radius"},
	"minecraft:spruce_foliage_placer":        {"offset", "radius", "trunk_height"},
}

// validateBlockProvider requires a recognised supplier type, and requires an
// actual state only for the simple form - the weighted and rule-based shapes carry
// their states inside entries/rules, which is why demanding State.Name here would
// have rejected azalea_tree.
func validateBlockProvider(feature, slot string, p TreeBlockProvider) error {
	if !knownStateProviders[p.Type] {
		return fmt.Errorf("worldgen: %s: %s has unmodelled provider type %q", feature, slot, p.Type)
	}
	if p.Type == "minecraft:simple_state_provider" && p.State.Name == "" {
		return fmt.Errorf("worldgen: %s: %s is simple_state_provider without a state", feature, slot)
	}
	return nil
}

// validatePlacer checks a placer against its spec: a type the pack does not have is
// an error, a field the type does not carry is an error, a missing one is an error,
// and any object-shaped field must parse as an int provider. Without the last
// check, an unreadable provider would arrive as the zero value - which is exactly
// how mega_pine used to report a radius of 0.
func validatePlacer(feature, kind string, typ string, fields map[string]json.RawMessage, spec map[string][]string) error {
	want, ok := spec[typ]
	if !ok {
		return fmt.Errorf("worldgen: %s has an unmodelled %s type %q", feature, kind, typ)
	}
	for _, name := range want {
		if _, present := fields[name]; !present {
			return fmt.Errorf("worldgen: %s: %s is missing %s", feature, typ, name)
		}
	}
	for name, raw := range fields {
		found := false
		for _, w := range want {
			if w == name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("worldgen: %s: %s carries %q, which is not a field this build models for it", feature, typ, name)
		}
		if len(raw) > 0 && raw[0] == '{' {
			if _, err := parseNestedIntProvider(raw); err != nil {
				return fmt.Errorf("worldgen: %s: %s.%s is not a readable int provider: %w", feature, typ, name, err)
			}
		}
	}
	return nil
}

var defaultBelowTrunkProvider = json.RawMessage(`{"type":"minecraft:rule_based_state_provider","rules":[{"if_true":{"type":"minecraft:not","predicate":{"type":"minecraft:matching_block_tag","tag":"minecraft:cannot_replace_below_tree_trunk"}},"then":{"type":"minecraft:simple_state_provider","state":{"Name":"minecraft:dirt"}}}]}`)

func (s *FeatureSet) Tree(name string) (TreeFeatureConfig, error) {
	if v, ok := s.getCachedConfig("tree", name); ok {
		return v.(TreeFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:tree" {
		return TreeFeatureConfig{}, fmt.Errorf("worldgen: %s is not a tree feature", name)
	}
	var config TreeFeatureConfig
	if err := json.Unmarshal(configured.Config, &config); err != nil {
		return TreeFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	if len(config.BelowTrunkProvider) == 0 {
		config.BelowTrunkProvider = defaultBelowTrunkProvider
	}
	var rawSlots struct {
		TrunkProvider   json.RawMessage `json:"trunk_provider"`
		FoliageProvider json.RawMessage `json:"foliage_provider"`
	}
	if err := json.Unmarshal(configured.Config, &rawSlots); err == nil {
		config.TrunkProvider.Raw = rawSlots.TrunkProvider
		config.FoliageProvider.Raw = rawSlots.FoliageProvider
	}
	if err := validateBlockProvider(name, "trunk_provider", config.TrunkProvider); err != nil {
		return TreeFeatureConfig{}, err
	}
	if err := validateBlockProvider(name, "foliage_provider", config.FoliageProvider); err != nil {
		return TreeFeatureConfig{}, err
	}
	if err := validatePlacer(name, "trunk_placer", config.TrunkPlacer.Type, config.TrunkPlacer.Fields, trunkPlacerSpec); err != nil {
		return TreeFeatureConfig{}, err
	}
	if err := validatePlacer(name, "foliage_placer", config.FoliagePlacer.Type, config.FoliagePlacer.Fields, foliagePlacerSpec); err != nil {
		return TreeFeatureConfig{}, err
	}
	switch config.MinimumSize.Type {
	case "":
	case "minecraft:two_layers_feature_size", "minecraft:three_layers_feature_size":
	default:
		return TreeFeatureConfig{}, fmt.Errorf("worldgen: %s has unsupported minimum_size %q", name, config.MinimumSize.Type)
	}
	for _, d := range config.Decorators {
		if d.Type == "" {
			return TreeFeatureConfig{}, fmt.Errorf("worldgen: %s has a decorator without a type", name)
		}
	}
	s.setCachedConfig("tree", name, config)
	return config, nil
}

func (s *FeatureSet) HugeMushroom(name string) (HugeMushroomFeatureConfig, error) {
	if v, ok := s.getCachedConfig("huge_mushroom", name); ok {
		return v.(HugeMushroomFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || (configured.Type != "minecraft:huge_red_mushroom" && configured.Type != "minecraft:huge_brown_mushroom") {
		return HugeMushroomFeatureConfig{}, fmt.Errorf("worldgen: %s is not a huge mushroom feature", name)
	}
	config := HugeMushroomFeatureConfig{FoliageRadius: 2}
	if err := json.Unmarshal(configured.Config, &config); err != nil {
		return HugeMushroomFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	if len(config.CapProvider) == 0 || len(config.StemProvider) == 0 || len(config.CanPlaceOn) == 0 {
		return HugeMushroomFeatureConfig{}, fmt.Errorf("worldgen: invalid huge mushroom %s", name)
	}
	s.setCachedConfig("huge_mushroom", name, config)
	return config, nil
}

func (s *FeatureSet) FallenTree(name string) (FallenTreeFeatureConfig, error) {
	if v, ok := s.getCachedConfig("fallen_tree", name); ok {
		return v.(FallenTreeFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:fallen_tree" {
		return FallenTreeFeatureConfig{}, fmt.Errorf("worldgen: %s is not a fallen tree feature", name)
	}
	var raw struct {
		TrunkProvider   json.RawMessage `json:"trunk_provider"`
		LogLength       json.RawMessage `json:"log_length"`
		StumpDecorators []TreeDecorator `json:"stump_decorators"`
		LogDecorators   []TreeDecorator `json:"log_decorators"`
	}
	if err := json.Unmarshal(configured.Config, &raw); err != nil {
		return FallenTreeFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	if len(raw.TrunkProvider) == 0 || len(raw.LogLength) == 0 {
		return FallenTreeFeatureConfig{}, fmt.Errorf("worldgen: invalid fallen tree %s", name)
	}
	length, err := parseNestedIntProvider(raw.LogLength)
	if err != nil {
		return FallenTreeFeatureConfig{}, fmt.Errorf("worldgen: %s log_length: %w", name, err)
	}
	for _, d := range raw.StumpDecorators {
		if d.Type == "" {
			return FallenTreeFeatureConfig{}, fmt.Errorf("worldgen: %s has a stump decorator without a type", name)
		}
	}
	for _, d := range raw.LogDecorators {
		if d.Type == "" {
			return FallenTreeFeatureConfig{}, fmt.Errorf("worldgen: %s has a log decorator without a type", name)
		}
	}
	config := FallenTreeFeatureConfig{
		TrunkProvider:   raw.TrunkProvider,
		LogLength:       length,
		StumpDecorators: raw.StumpDecorators,
		LogDecorators:   raw.LogDecorators,
	}
	s.setCachedConfig("fallen_tree", name, config)
	return config, nil
}

// LargeDripstone parses a minecraft:large_dripstone configured feature.
func (s *FeatureSet) LargeDripstone(name string) (LargeDripstoneFeatureConfig, error) {
	if v, ok := s.getCachedConfig("large_dripstone", name); ok {
		return v.(LargeDripstoneFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:large_dripstone" {
		return LargeDripstoneFeatureConfig{}, fmt.Errorf("worldgen: %s is not a large dripstone feature", name)
	}
	var raw struct {
		FloorToCeilingSearchRange        *int            `json:"floor_to_ceiling_search_range"`
		ColumnRadius                     json.RawMessage `json:"column_radius"`
		HeightScale                      json.RawMessage `json:"height_scale"`
		MaxColumnRadiusToCaveHeightRatio float32         `json:"max_column_radius_to_cave_height_ratio"`
		StalactiteBluntness              json.RawMessage `json:"stalactite_bluntness"`
		StalagmiteBluntness              json.RawMessage `json:"stalagmite_bluntness"`
		WindSpeed                        json.RawMessage `json:"wind_speed"`
		MinRadiusForWind                 int             `json:"min_radius_for_wind"`
		MinBluntnessForWind              float32         `json:"min_bluntness_for_wind"`
	}
	if err := json.Unmarshal(configured.Config, &raw); err != nil {
		return LargeDripstoneFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	searchRange := 30
	if raw.FloorToCeilingSearchRange != nil {
		searchRange = *raw.FloorToCeilingSearchRange
	}
	colRadius, err := parseNestedIntProvider(raw.ColumnRadius)
	if err != nil {
		return LargeDripstoneFeatureConfig{}, fmt.Errorf("worldgen: %s column_radius: %w", name, err)
	}
	heightScale, err := parseUniformFloatProvider(raw.HeightScale)
	if err != nil {
		return LargeDripstoneFeatureConfig{}, fmt.Errorf("worldgen: %s height_scale: %w", name, err)
	}
	stalactiteBluntness, err := parseUniformFloatProvider(raw.StalactiteBluntness)
	if err != nil {
		return LargeDripstoneFeatureConfig{}, fmt.Errorf("worldgen: %s stalactite_bluntness: %w", name, err)
	}
	stalagmiteBluntness, err := parseUniformFloatProvider(raw.StalagmiteBluntness)
	if err != nil {
		return LargeDripstoneFeatureConfig{}, fmt.Errorf("worldgen: %s stalagmite_bluntness: %w", name, err)
	}
	windSpeed, err := parseUniformFloatProvider(raw.WindSpeed)
	if err != nil {
		return LargeDripstoneFeatureConfig{}, fmt.Errorf("worldgen: %s wind_speed: %w", name, err)
	}
	config := LargeDripstoneFeatureConfig{
		FloorToCeilingSearchRange:        searchRange,
		ColumnRadiusMin:                  colRadius.Min,
		ColumnRadiusMax:                  colRadius.Max,
		HeightScale:                      heightScale,
		MaxColumnRadiusToCaveHeightRatio: raw.MaxColumnRadiusToCaveHeightRatio,
		StalactiteBluntness:              stalactiteBluntness,
		StalagmiteBluntness:              stalagmiteBluntness,
		WindSpeed:                        windSpeed,
		MinRadiusForWind:                 raw.MinRadiusForWind,
		MinBluntnessForWind:              raw.MinBluntnessForWind,
	}
	s.setCachedConfig("large_dripstone", name, config)
	return config, nil
}

func parseUniformFloatProvider(raw json.RawMessage) (UniformFloatProvider, error) {
	var fixed float32
	if err := json.Unmarshal(raw, &fixed); err == nil {
		return UniformFloatProvider{Min: fixed, Max: fixed}, nil
	}
	var value struct {
		Type         string  `json:"type"`
		MinInclusive float32 `json:"min_inclusive"`
		MaxExclusive float32 `json:"max_exclusive"`
		Min          float32 `json:"min"`
		Max          float32 `json:"max"`
		Mean         float32 `json:"mean"`
		Deviation    float32 `json:"deviation"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return UniformFloatProvider{}, err
	}
	switch value.Type {
	case "minecraft:uniform":
		if value.MaxExclusive <= value.MinInclusive {
			return UniformFloatProvider{}, fmt.Errorf("invalid uniform float provider %s", raw)
		}
		return UniformFloatProvider{Type: value.Type, Min: value.MinInclusive, Max: value.MaxExclusive}, nil
	case "minecraft:clamped_normal":
		if value.Max < value.Min || value.Deviation <= 0 {
			return UniformFloatProvider{}, fmt.Errorf("invalid clamped_normal float provider %s", raw)
		}
		return UniformFloatProvider{
			Type:      value.Type,
			Min:       value.Min,
			Max:       value.Max,
			Mean:      value.Mean,
			Deviation: value.Deviation,
		}, nil
	default:
		return UniformFloatProvider{}, fmt.Errorf("invalid float provider %s", raw)
	}
}

// MultifaceGrowth parses a minecraft:multiface_growth configured feature.
func (s *FeatureSet) MultifaceGrowth(name string) (MultifaceGrowthFeatureConfig, error) {
	if v, ok := s.getCachedConfig("multiface_growth", name); ok {
		return v.(MultifaceGrowthFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:multiface_growth" {
		return MultifaceGrowthFeatureConfig{}, fmt.Errorf("worldgen: %s is not a multiface growth feature", name)
	}
	var raw struct {
		Block             string          `json:"block"`
		SearchRange       *int            `json:"search_range"`
		CanPlaceOnFloor   bool            `json:"can_place_on_floor"`
		CanPlaceOnCeiling bool            `json:"can_place_on_ceiling"`
		CanPlaceOnWall    bool            `json:"can_place_on_wall"`
		ChanceOfSpreading *float32        `json:"chance_of_spreading"`
		CanBePlacedOn     json.RawMessage `json:"can_be_placed_on"`
	}
	if err := json.Unmarshal(configured.Config, &raw); err != nil {
		return MultifaceGrowthFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	block := raw.Block
	if block == "" {
		block = "minecraft:glow_lichen"
	}
	searchRange := 10
	if raw.SearchRange != nil {
		searchRange = *raw.SearchRange
	}
	chance := float32(0.5)
	if raw.ChanceOfSpreading != nil {
		chance = *raw.ChanceOfSpreading
	}
	var canBePlacedOn []string
	if len(raw.CanBePlacedOn) > 0 {
		if raw.CanBePlacedOn[0] == '"' {
			var single string
			if err := json.Unmarshal(raw.CanBePlacedOn, &single); err != nil {
				return MultifaceGrowthFeatureConfig{}, err
			}
			canBePlacedOn = []string{single}
		} else if err := json.Unmarshal(raw.CanBePlacedOn, &canBePlacedOn); err != nil {
			return MultifaceGrowthFeatureConfig{}, err
		}
	}
	if searchRange < 1 || len(canBePlacedOn) == 0 {
		return MultifaceGrowthFeatureConfig{}, fmt.Errorf("worldgen: invalid multiface growth %s", name)
	}
	config := MultifaceGrowthFeatureConfig{
		Block:             block,
		SearchRange:       searchRange,
		CanPlaceOnFloor:   raw.CanPlaceOnFloor,
		CanPlaceOnCeiling: raw.CanPlaceOnCeiling,
		CanPlaceOnWall:    raw.CanPlaceOnWall,
		ChanceOfSpreading: chance,
		CanBePlacedOn:     canBePlacedOn,
	}
	s.setCachedConfig("multiface_growth", name, config)
	return config, nil
}

// SculkPatch parses a minecraft:sculk_patch configured feature.
func (s *FeatureSet) SculkPatch(name string) (SculkPatchFeatureConfig, error) {
	if v, ok := s.getCachedConfig("sculk_patch", name); ok {
		return v.(SculkPatchFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:sculk_patch" {
		return SculkPatchFeatureConfig{}, fmt.Errorf("worldgen: %s is not a sculk patch feature", name)
	}
	var raw struct {
		ChargeCount      int             `json:"charge_count"`
		AmountPerCharge  int             `json:"amount_per_charge"`
		SpreadAttempts   int             `json:"spread_attempts"`
		GrowthRounds     int             `json:"growth_rounds"`
		SpreadRounds     int             `json:"spread_rounds"`
		ExtraRareGrowths json.RawMessage `json:"extra_rare_growths"`
		CatalystChance   float32         `json:"catalyst_chance"`
	}
	if err := json.Unmarshal(configured.Config, &raw); err != nil {
		return SculkPatchFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	extra, err := parseNestedIntProvider(raw.ExtraRareGrowths)
	if err != nil {
		return SculkPatchFeatureConfig{}, fmt.Errorf("worldgen: %s extra_rare_growths: %w", name, err)
	}
	if raw.ChargeCount < 1 || raw.AmountPerCharge < 1 || raw.SpreadAttempts < 1 {
		return SculkPatchFeatureConfig{}, fmt.Errorf("worldgen: invalid sculk patch %s", name)
	}
	config := SculkPatchFeatureConfig{
		ChargeCount:      raw.ChargeCount,
		AmountPerCharge:  raw.AmountPerCharge,
		SpreadAttempts:   raw.SpreadAttempts,
		GrowthRounds:     raw.GrowthRounds,
		SpreadRounds:     raw.SpreadRounds,
		ExtraRareGrowths: extra,
		CatalystChance:   raw.CatalystChance,
	}
	s.setCachedConfig("sculk_patch", name, config)
	return config, nil
}

// DripstoneCluster parses a minecraft:dripstone_cluster configured feature.
func (s *FeatureSet) DripstoneCluster(name string) (DripstoneClusterFeatureConfig, error) {
	if v, ok := s.getCachedConfig("dripstone_cluster", name); ok {
		return v.(DripstoneClusterFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:dripstone_cluster" {
		return DripstoneClusterFeatureConfig{}, fmt.Errorf("worldgen: %s is not a dripstone cluster feature", name)
	}
	var raw struct {
		FloorToCeilingSearchRange                           int             `json:"floor_to_ceiling_search_range"`
		Height                                              json.RawMessage `json:"height"`
		Radius                                              json.RawMessage `json:"radius"`
		MaxStalagmiteStalactiteHeightDiff                   int             `json:"max_stalagmite_stalactite_height_diff"`
		HeightDeviation                                     int             `json:"height_deviation"`
		DripstoneBlockLayerThickness                        json.RawMessage `json:"dripstone_block_layer_thickness"`
		Density                                             json.RawMessage `json:"density"`
		Wetness                                             json.RawMessage `json:"wetness"`
		ChanceOfDripstoneColumnAtMaxDistanceFromCenter      float32         `json:"chance_of_dripstone_column_at_max_distance_from_center"`
		MaxDistanceFromEdgeAffectingChanceOfDripstoneColumn int             `json:"max_distance_from_edge_affecting_chance_of_dripstone_column"`
		MaxDistanceFromCenterAffectingHeightBias            int             `json:"max_distance_from_center_affecting_height_bias"`
	}
	if err := json.Unmarshal(configured.Config, &raw); err != nil {
		return DripstoneClusterFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	height, err := parseNestedIntProvider(raw.Height)
	if err != nil {
		return DripstoneClusterFeatureConfig{}, fmt.Errorf("worldgen: %s height: %w", name, err)
	}
	radius, err := parseNestedIntProvider(raw.Radius)
	if err != nil {
		return DripstoneClusterFeatureConfig{}, fmt.Errorf("worldgen: %s radius: %w", name, err)
	}
	thickness, err := parseNestedIntProvider(raw.DripstoneBlockLayerThickness)
	if err != nil {
		return DripstoneClusterFeatureConfig{}, fmt.Errorf("worldgen: %s dripstone_block_layer_thickness: %w", name, err)
	}
	density, err := parseUniformFloatProvider(raw.Density)
	if err != nil {
		return DripstoneClusterFeatureConfig{}, fmt.Errorf("worldgen: %s density: %w", name, err)
	}
	wetness, err := parseUniformFloatProvider(raw.Wetness)
	if err != nil {
		return DripstoneClusterFeatureConfig{}, fmt.Errorf("worldgen: %s wetness: %w", name, err)
	}
	config := DripstoneClusterFeatureConfig{
		FloorToCeilingSearchRange:         raw.FloorToCeilingSearchRange,
		Height:                            height,
		Radius:                            radius,
		MaxStalagmiteStalactiteHeightDiff: raw.MaxStalagmiteStalactiteHeightDiff,
		HeightDeviation:                   raw.HeightDeviation,
		DripstoneBlockLayerThickness:      thickness,
		Density:                           density,
		Wetness:                           wetness,
		ChanceOfDripstoneColumnAtMaxDistanceFromCenter:      raw.ChanceOfDripstoneColumnAtMaxDistanceFromCenter,
		MaxDistanceFromEdgeAffectingChanceOfDripstoneColumn: raw.MaxDistanceFromEdgeAffectingChanceOfDripstoneColumn,
		MaxDistanceFromCenterAffectingHeightBias:            raw.MaxDistanceFromCenterAffectingHeightBias,
	}
	s.setCachedConfig("dripstone_cluster", name, config)
	return config, nil
}

// PointedDripstone parses a minecraft:pointed_dripstone configured feature.
func (s *FeatureSet) PointedDripstone(name string) (PointedDripstoneFeatureConfig, error) {
	if v, ok := s.getCachedConfig("pointed_dripstone", name); ok {
		return v.(PointedDripstoneFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:pointed_dripstone" {
		return PointedDripstoneFeatureConfig{}, fmt.Errorf("worldgen: %s is not a pointed dripstone feature", name)
	}
	var raw struct {
		ChanceOfTallerDripstone   *float32 `json:"chance_of_taller_dripstone"`
		ChanceOfDirectionalSpread *float32 `json:"chance_of_directional_spread"`
		ChanceOfSpreadRadius2     *float32 `json:"chance_of_spread_radius_2"`
		ChanceOfSpreadRadius3     *float32 `json:"chance_of_spread_radius_3"`
	}
	if err := json.Unmarshal(configured.Config, &raw); err != nil {
		return PointedDripstoneFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	cfg := PointedDripstoneFeatureConfig{
		ChanceOfTallerDripstone:   0.2,
		ChanceOfDirectionalSpread: 0.7,
		ChanceOfSpreadRadius2:     0.5,
		ChanceOfSpreadRadius3:     0.5,
	}
	if raw.ChanceOfTallerDripstone != nil {
		cfg.ChanceOfTallerDripstone = *raw.ChanceOfTallerDripstone
	}
	if raw.ChanceOfDirectionalSpread != nil {
		cfg.ChanceOfDirectionalSpread = *raw.ChanceOfDirectionalSpread
	}
	if raw.ChanceOfSpreadRadius2 != nil {
		cfg.ChanceOfSpreadRadius2 = *raw.ChanceOfSpreadRadius2
	}
	if raw.ChanceOfSpreadRadius3 != nil {
		cfg.ChanceOfSpreadRadius3 = *raw.ChanceOfSpreadRadius3
	}
	s.setCachedConfig("pointed_dripstone", name, cfg)
	return cfg, nil
}

func (s *FeatureSet) RandomSelector(name string) (RandomSelectorConfig, error) {
	if v, ok := s.getCachedConfig("random_selector", name); ok {
		return v.(RandomSelectorConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:random_selector" {
		return RandomSelectorConfig{}, fmt.Errorf("worldgen: %s is not a random selector", name)
	}
	var raw struct {
		Default  json.RawMessage `json:"default"`
		Features []struct {
			Chance  float32         `json:"chance"`
			Feature json.RawMessage `json:"feature"`
		} `json:"features"`
	}
	if err := json.Unmarshal(configured.Config, &raw); err != nil {
		return RandomSelectorConfig{}, err
	}
	selector := RandomSelectorConfig{Features: make([]RandomSelectorEntry, len(raw.Features))}
	var err error
	selector.Default, err = s.parseFeatureRef(raw.Default)
	if err != nil {
		return RandomSelectorConfig{}, err
	}
	for i, entry := range raw.Features {
		selector.Features[i].Chance = entry.Chance
		selector.Features[i].Feature, err = s.parseFeatureRef(entry.Feature)
		if err != nil {
			return RandomSelectorConfig{}, err
		}
	}
	s.setCachedConfig("random_selector", name, selector)
	return selector, nil
}

// BlockColumn parses the block_column configured feature: direction, layers
// (nested height provider + state provider), the allowed-placement predicate,
// and the tip priority.
func (s *FeatureSet) BlockColumn(name string) (BlockColumnFeatureConfig, error) {
	if v, ok := s.getCachedConfig("block_column", name); ok {
		return v.(BlockColumnFeatureConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:block_column" {
		return BlockColumnFeatureConfig{}, fmt.Errorf("worldgen: %s is not a block column", name)
	}
	var raw struct {
		Allowed   json.RawMessage `json:"allowed_placement"`
		Direction string          `json:"direction"`
		Layers    []struct {
			Height   json.RawMessage `json:"height"`
			Provider json.RawMessage `json:"provider"`
		} `json:"layers"`
		PrioritizeTip bool `json:"prioritize_tip"`
	}
	if err := json.Unmarshal(configured.Config, &raw); err != nil {
		return BlockColumnFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	if raw.Direction != "up" && raw.Direction != "down" || len(raw.Layers) == 0 {
		return BlockColumnFeatureConfig{}, fmt.Errorf("worldgen: invalid block column %s", name)
	}
	config := BlockColumnFeatureConfig{
		Direction:     raw.Direction,
		Allowed:       raw.Allowed,
		PrioritizeTip: raw.PrioritizeTip,
		Layers:        make([]BlockColumnLayer, len(raw.Layers)),
	}
	for i, layer := range raw.Layers {
		height, err := parseNestedIntProvider(layer.Height)
		if err != nil {
			return BlockColumnFeatureConfig{}, fmt.Errorf("worldgen: %s layer %d height: %w", name, i, err)
		}
		provider, err := parseStateProviderSpec(layer.Provider)
		if err != nil {
			return BlockColumnFeatureConfig{}, fmt.Errorf("worldgen: %s layer %d provider: %w", name, i, err)
		}
		config.Layers[i] = BlockColumnLayer{Height: height, Provider: provider}
	}
	s.setCachedConfig("block_column", name, config)
	return config, nil
}

// SimpleRandomSelector parses the simple_random_selector configured feature.
func (s *FeatureSet) SimpleRandomSelector(name string) (SimpleRandomSelectorConfig, error) {
	if v, ok := s.getCachedConfig("simple_random_selector", name); ok {
		return v.(SimpleRandomSelectorConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:simple_random_selector" {
		return SimpleRandomSelectorConfig{}, fmt.Errorf("worldgen: %s is not a simple random selector", name)
	}
	var raw struct {
		Features []json.RawMessage `json:"features"`
	}
	if err := json.Unmarshal(configured.Config, &raw); err != nil || len(raw.Features) == 0 {
		return SimpleRandomSelectorConfig{}, fmt.Errorf("worldgen: invalid simple random selector %s", name)
	}
	config := SimpleRandomSelectorConfig{Features: make([]FeatureRef, len(raw.Features))}
	for i, f := range raw.Features {
		ref, err := s.parseFeatureRef(f)
		if err != nil {
			return SimpleRandomSelectorConfig{}, err
		}
		config.Features[i] = ref
	}
	s.setCachedConfig("simple_random_selector", name, config)
	return config, nil
}

// parseNestedIntProvider accepts a raw int (constant, draw-free), a uniform
// object, or a weighted_list whose data values are themselves providers.
func parseNestedIntProvider(raw json.RawMessage) (NestedIntProvider, error) {
	var fixed int
	if err := json.Unmarshal(raw, &fixed); err == nil {
		return NestedIntProvider{Type: "constant", Min: fixed, Max: fixed}, nil
	}
	var value struct {
		Type         string `json:"type"`
		Min          int    `json:"min_inclusive"`
		Max          int    `json:"max_inclusive"`
		Distribution []struct {
			Data   json.RawMessage `json:"data"`
			Weight int             `json:"weight"`
		} `json:"distribution"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return NestedIntProvider{}, err
	}
	switch value.Type {
	case "":
		// An int provider may be written without a discriminator at all:
		// cherry_trunk_placer's branch_start_offset_from_top is exactly
		// {"min_inclusive": -4, "max_inclusive": -3}. It dispatches as a uniform.
		// The key check matters - without it, any object that is not a provider at
		// all would parse as the constant 0 and be believed.
		if !bytes.Contains(raw, []byte("min_inclusive")) && !bytes.Contains(raw, []byte("max_inclusive")) {
			return NestedIntProvider{}, fmt.Errorf("int provider without a type: %s", raw)
		}
		if value.Max < value.Min {
			return NestedIntProvider{}, fmt.Errorf("invalid implicit uniform %s", raw)
		}
		return NestedIntProvider{Type: "uniform", Min: value.Min, Max: value.Max}, nil
	case "minecraft:uniform":
		if value.Max < value.Min {
			return NestedIntProvider{}, fmt.Errorf("invalid uniform %s", raw)
		}
		return NestedIntProvider{Type: "uniform", Min: value.Min, Max: value.Max}, nil
	case "minecraft:biased_to_bottom":
		if value.Max < value.Min {
			return NestedIntProvider{}, fmt.Errorf("invalid biased_to_bottom %s", raw)
		}
		return NestedIntProvider{Type: "biased_to_bottom", Min: value.Min, Max: value.Max}, nil
	case "minecraft:weighted_list":
		provider := NestedIntProvider{Type: "weighted"}
		for _, entry := range value.Distribution {
			if entry.Weight <= 0 {
				continue
			}
			inner, err := parseNestedIntProvider(entry.Data)
			if err != nil {
				return NestedIntProvider{}, err
			}
			provider.Weighted = append(provider.Weighted, WeightedNestedInt{Weight: entry.Weight, Value: inner})
		}
		if len(provider.Weighted) == 0 {
			return NestedIntProvider{}, fmt.Errorf("empty weighted list %s", raw)
		}
		return provider, nil
	}
	return NestedIntProvider{}, fmt.Errorf("unsupported int provider %s", raw)
}

// parseStateProviderSpec accepts simple, weighted, and randomized-int state
// providers.
// StateProvider parses a block-state provider held in a raw JSON member, so a
// caller that kept a provider as json.RawMessage (tree configs do) can resolve it
// without this package having to know where it came from.
func (s *FeatureSet) StateProvider(raw json.RawMessage) (StateProviderSpec, error) {
	key := string(raw)
	if v, ok := s.getCachedConfig("state_provider", key); ok {
		return v.(StateProviderSpec), nil
	}
	spec, err := parseStateProviderSpec(raw)
	if err != nil {
		return StateProviderSpec{}, err
	}
	s.setCachedConfig("state_provider", key, spec)
	return spec, nil
}

func parseStateProviderSpec(raw json.RawMessage) (StateProviderSpec, error) {
	var probe struct {
		Type     string     `json:"type"`
		State    BlockState `json:"state"`
		Entries  []WeightedBlockStateRaw
		Property string          `json:"property"`
		Source   json.RawMessage `json:"source"`
		Values   json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return StateProviderSpec{}, err
	}
	switch probe.Type {
	case "minecraft:simple_state_provider":
		if probe.State.Name == "" {
			return StateProviderSpec{}, fmt.Errorf("simple provider missing state")
		}
		return StateProviderSpec{Type: "simple", State: probe.State}, nil
	case "minecraft:weighted_state_provider":
		var doc struct {
			Entries []WeightedBlockStateRaw `json:"entries"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Entries) == 0 {
			return StateProviderSpec{}, fmt.Errorf("invalid weighted provider %s", raw)
		}
		spec := StateProviderSpec{Type: "weighted", Entries: make([]WeightedBlockState, len(doc.Entries))}
		for i, e := range doc.Entries {
			spec.Entries[i] = WeightedBlockState{State: e.Data, Weight: e.Weight}
		}
		return spec, nil
	case "minecraft:rule_based_state_provider":
		var doc struct {
			Rules []struct {
				IfTrue json.RawMessage `json:"if_true"`
				Then   json.RawMessage `json:"then"`
			} `json:"rules"`
			Default json.RawMessage `json:"default_provider"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return StateProviderSpec{}, fmt.Errorf("invalid rule based provider %s: %w", raw, err)
		}
		if len(doc.Rules) == 0 {
			return StateProviderSpec{}, fmt.Errorf("rule based provider with no rules %s", raw)
		}
		spec := StateProviderSpec{Type: "rule_based", Rules: make([]StateProviderRule, len(doc.Rules))}
		for i, rule := range doc.Rules {
			if len(rule.IfTrue) == 0 || len(rule.Then) == 0 {
				return StateProviderSpec{}, fmt.Errorf("rule based provider rule %d lacks if_true or then", i)
			}
			spec.Rules[i] = StateProviderRule{Predicate: rule.IfTrue, Then: rule.Then}
		}
		if len(doc.Default) > 0 {
			fallback, err := parseStateProviderSpec(doc.Default)
			if err != nil {
				return StateProviderSpec{}, err
			}
			spec.Fallback = &fallback
		}
		return spec, nil
	case "minecraft:randomized_int_state_provider":
		if probe.Property == "" || len(probe.Source) == 0 || len(probe.Values) == 0 {
			return StateProviderSpec{}, fmt.Errorf("invalid randomized int provider %s", raw)
		}
		source, err := parseStateProviderSpec(probe.Source)
		if err != nil {
			return StateProviderSpec{}, err
		}
		var fixed int
		values := CountProvider{}
		if err := json.Unmarshal(probe.Values, &fixed); err == nil {
			values = CountProvider{Min: fixed, Max: fixed}
		} else {
			var uniform struct {
				Min int `json:"min_inclusive"`
				Max int `json:"max_inclusive"`
			}
			if err := json.Unmarshal(probe.Values, &uniform); err != nil || uniform.Max < uniform.Min {
				return StateProviderSpec{}, fmt.Errorf("invalid randomized int values %s", probe.Values)
			}
			values = CountProvider{Min: uniform.Min, Max: uniform.Max}
		}
		return StateProviderSpec{Type: "randomized_int", Property: probe.Property, Source: &source, Values: values}, nil
	case "minecraft:noise_provider":
		var doc struct {
			Seed  int64 `json:"seed"`
			Noise struct {
				FirstOctave int       `json:"firstOctave"`
				Amplitudes  []float64 `json:"amplitudes"`
			} `json:"noise"`
			Scale  float32      `json:"scale"`
			States []BlockState `json:"states"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil || len(doc.States) == 0 || len(doc.Noise.Amplitudes) == 0 {
			return StateProviderSpec{}, fmt.Errorf("invalid noise provider %s", raw)
		}
		noise := NewNormalNoise(NewLegacy(doc.Seed), doc.Noise.FirstOctave, doc.Noise.Amplitudes)
		return StateProviderSpec{
			Type:   "noise",
			States: doc.States,
			Scale:  doc.Scale,
			Noise:  noise,
		}, nil
	case "minecraft:noise_threshold_provider":
		var doc struct {
			Seed  int64 `json:"seed"`
			Noise struct {
				FirstOctave int       `json:"firstOctave"`
				Amplitudes  []float64 `json:"amplitudes"`
			} `json:"noise"`
			Scale        float32      `json:"scale"`
			Threshold    float32      `json:"threshold"`
			HighChance   float32      `json:"high_chance"`
			DefaultState BlockState   `json:"default_state"`
			LowStates    []BlockState `json:"low_states"`
			HighStates   []BlockState `json:"high_states"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil || doc.DefaultState.Name == "" || len(doc.Noise.Amplitudes) == 0 {
			return StateProviderSpec{}, fmt.Errorf("invalid noise threshold provider %s", raw)
		}
		noise := NewNormalNoise(NewLegacy(doc.Seed), doc.Noise.FirstOctave, doc.Noise.Amplitudes)
		return StateProviderSpec{
			Type:       "noise_threshold",
			Scale:      doc.Scale,
			Noise:      noise,
			Threshold:  doc.Threshold,
			HighChance: doc.HighChance,
			Default:    doc.DefaultState,
			LowStates:  doc.LowStates,
			HighStates: doc.HighStates,
		}, nil
	case "minecraft:dual_noise_provider":
		var doc struct {
			Seed  int64 `json:"seed"`
			Noise struct {
				FirstOctave int       `json:"firstOctave"`
				Amplitudes  []float64 `json:"amplitudes"`
			} `json:"noise"`
			Scale     float32 `json:"scale"`
			SlowNoise struct {
				FirstOctave int       `json:"firstOctave"`
				Amplitudes  []float64 `json:"amplitudes"`
			} `json:"slow_noise"`
			SlowScale float32         `json:"slow_scale"`
			Variety   json.RawMessage `json:"variety"`
			States    []BlockState    `json:"states"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil || len(doc.States) == 0 || len(doc.Noise.Amplitudes) == 0 || len(doc.SlowNoise.Amplitudes) == 0 {
			return StateProviderSpec{}, fmt.Errorf("invalid dual noise provider %s", raw)
		}
		var varietyMin, varietyMax int
		var arr [2]int
		if err := json.Unmarshal(doc.Variety, &arr); err == nil {
			varietyMin, varietyMax = arr[0], arr[1]
		} else {
			var obj struct {
				Min int `json:"min_inclusive"`
				Max int `json:"max_inclusive"`
			}
			if err := json.Unmarshal(doc.Variety, &obj); err != nil {
				return StateProviderSpec{}, fmt.Errorf("invalid dual noise variety %s", doc.Variety)
			}
			varietyMin, varietyMax = obj.Min, obj.Max
		}
		if varietyMin < 1 || varietyMax < varietyMin {
			return StateProviderSpec{}, fmt.Errorf("invalid dual noise variety range [%d,%d]", varietyMin, varietyMax)
		}
		noise := NewNormalNoise(NewLegacy(doc.Seed), doc.Noise.FirstOctave, doc.Noise.Amplitudes)
		slowNoise := NewNormalNoise(NewLegacy(doc.Seed), doc.SlowNoise.FirstOctave, doc.SlowNoise.Amplitudes)
		return StateProviderSpec{
			Type:       "dual_noise",
			States:     doc.States,
			Scale:      doc.Scale,
			Noise:      noise,
			SlowScale:  doc.SlowScale,
			SlowNoise:  slowNoise,
			VarietyMin: varietyMin,
			VarietyMax: varietyMax,
		}, nil
	}
	return StateProviderSpec{}, fmt.Errorf("unsupported state provider %s", raw)
}

type WeightedBlockStateRaw struct {
	Data   BlockState `json:"data"`
	Weight int        `json:"weight"`
}

func (s *FeatureSet) RandomBooleanSelector(name string) (RandomBooleanSelectorConfig, error) {
	if v, ok := s.getCachedConfig("random_boolean_selector", name); ok {
		return v.(RandomBooleanSelectorConfig), nil
	}
	configured, ok := s.LookupConfigured(name)
	if !ok || configured.Type != "minecraft:random_boolean_selector" {
		return RandomBooleanSelectorConfig{}, fmt.Errorf("worldgen: %s is not a random boolean selector", name)
	}
	var raw struct {
		FeatureTrue  json.RawMessage `json:"feature_true"`
		FeatureFalse json.RawMessage `json:"feature_false"`
	}
	if err := json.Unmarshal(configured.Config, &raw); err != nil {
		return RandomBooleanSelectorConfig{}, err
	}
	selector := RandomBooleanSelectorConfig{}
	var err error
	if selector.FeatureTrue, err = s.parseFeatureRef(raw.FeatureTrue); err != nil {
		return RandomBooleanSelectorConfig{}, err
	}
	if selector.FeatureFalse, err = s.parseFeatureRef(raw.FeatureFalse); err != nil {
		return RandomBooleanSelectorConfig{}, err
	}
	s.setCachedConfig("random_boolean_selector", name, selector)
	return selector, nil
}

// parseFeatureRef accepts the three datapack reference forms: a bare name
// string, {"feature": "name", "placement": [...]}, and
// {"feature": {inline configured feature}, "placement": [...]}. Inline
// configured features are registered under a deterministic key derived from
// their raw bytes so repeated parses of the same reference resolve to the
// same entry (idempotent under the set mutex; the load itself happens once
// but placement-time parses run on generation goroutines).
func (s *FeatureSet) parseFeatureRef(raw json.RawMessage) (FeatureRef, error) {
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		return FeatureRef{Name: name}, nil
	}
	var inline struct {
		Feature   json.RawMessage   `json:"feature"`
		Placement []json.RawMessage `json:"placement"`
	}
	if err := json.Unmarshal(raw, &inline); err != nil || len(inline.Feature) == 0 {
		return FeatureRef{}, fmt.Errorf("worldgen: invalid feature reference %s", raw)
	}
	ref := FeatureRef{Placement: make([]PlacementModifier, len(inline.Placement))}
	for i, modifier := range inline.Placement {
		if err := json.Unmarshal(modifier, &ref.Placement[i]); err != nil {
			return FeatureRef{}, err
		}
		ref.Placement[i].Raw = modifier
	}
	// The nested feature is either a plain name or an inline configured
	// feature object.
	if err := json.Unmarshal(inline.Feature, &name); err == nil {
		ref.Name = name
		return ref, nil
	}
	var doc struct {
		Type   string          `json:"type"`
		Config json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(inline.Feature, &doc); err != nil || doc.Type == "" {
		return FeatureRef{}, fmt.Errorf("worldgen: invalid inline feature %s", inline.Feature)
	}
	key := "inline:" + hashRawFeature(inline.Feature)
	s.inlineMu.Lock()
	if s.inlineConfigured == nil {
		s.inlineConfigured = make(map[string]ConfiguredFeature)
	}
	if _, exists := s.inlineConfigured[key]; !exists {
		s.inlineConfigured[key] = ConfiguredFeature{Type: doc.Type, Config: doc.Config}
	}
	s.inlineMu.Unlock()
	ref.Name = key
	return ref, nil
}

// hashRawFeature derives the deterministic synthetic name for an inline
// configured feature from its exact bytes.
func hashRawFeature(raw []byte) string {
	h := fnv.New64a()
	h.Write(raw)
	return strconv.FormatUint(h.Sum64(), 16)
}

func (s *FeatureSet) Placement(name string) (PlacementPlan, error) {
	if v, ok := s.getCachedConfig("placement", name); ok {
		return v.(PlacementPlan), nil
	}
	placed, ok := s.Placed[name]
	if !ok {
		return PlacementPlan{}, fmt.Errorf("worldgen: placed feature %s missing", name)
	}
	plan := PlacementPlan{Count: CountProvider{Min: 1, Max: 1}, MinY: HeightProvider{AboveBottom: intPtr(0)}, MaxY: HeightProvider{Absolute: intPtr(320)}}
	for _, modifier := range placed.Placement {
		switch modifier.Type {
		case "minecraft:rarity_filter":
			var value struct {
				Chance int `json:"chance"`
			}
			if err := json.Unmarshal(modifier.Raw, &value); err != nil || value.Chance < 1 {
				return PlacementPlan{}, fmt.Errorf("worldgen: %s invalid rarity filter", name)
			}
			plan.RarityChance = value.Chance
		case "minecraft:count":
			var value struct {
				Count json.RawMessage `json:"count"`
			}
			if err := json.Unmarshal(modifier.Raw, &value); err != nil {
				return PlacementPlan{}, err
			}
			count, err := parseIntProvider(value.Count)
			if err != nil {
				return PlacementPlan{}, fmt.Errorf("worldgen: %s count: %w", name, err)
			}
			plan.Count = count
		case "minecraft:height_range":
			var value struct {
				Height struct {
					Type    string          `json:"type"`
					Plateau int             `json:"plateau"`
					Min     json.RawMessage `json:"min_inclusive"`
					Max     json.RawMessage `json:"max_inclusive"`
				} `json:"height"`
			}
			if err := json.Unmarshal(modifier.Raw, &value); err != nil {
				return PlacementPlan{}, err
			}
			min, err := parseFeatureHeight(value.Height.Min)
			if err != nil {
				return PlacementPlan{}, err
			}
			max, err := parseFeatureHeight(value.Height.Max)
			if err != nil {
				return PlacementPlan{}, err
			}
			if value.Height.Type != "minecraft:uniform" && value.Height.Type != "minecraft:trapezoid" &&
				value.Height.Type != "minecraft:very_biased_to_bottom" {
				return PlacementPlan{}, fmt.Errorf("worldgen: %s unsupported height distribution %q", name, value.Height.Type)
			}
			plan.HeightDistribution, plan.HeightPlateau, plan.MinY, plan.MaxY = value.Height.Type, value.Height.Plateau, min, max
		case "minecraft:in_square", "minecraft:biome", "minecraft:surface_water_depth_filter",
			"minecraft:heightmap", "minecraft:block_predicate_filter", "minecraft:noise_threshold_count",
			"minecraft:noise_based_count", "minecraft:random_offset", "minecraft:environment_scan", "minecraft:surface_relative_threshold_filter":
			// Coordinate spreading and biome validation are applied by the world
			// executor. Keeping them in the parsed plan preserves their order.
		default:
			return PlacementPlan{}, fmt.Errorf("worldgen: %s unsupported placement modifier %q", name, modifier.Type)
		}
	}
	s.setCachedConfig("placement", name, plan)
	return plan, nil
}

// PlacementPositions executes a placed feature's modifiers in declared order.
// The recursive walk mirrors Stream.flatMap: every repeated position completes
// the remaining chain before the next repeated position consumes random draws.
func (s *FeatureSet) PlacementPositions(name string, r RandomSource, origin FeaturePosition, context PlacementContext) ([]FeaturePosition, error) {
	var result []FeaturePosition
	err := s.ForEachPlacementPosition(name, r, origin, context, func(position FeaturePosition) error {
		result = append(result, position)
		return nil
	})
	return result, err
}

// ForEachPlacementPosition preserves vanilla's lazy placement stream: the
// configured feature consumes random draws for one position before modifiers
// produce the next repeated position.
func (s *FeatureSet) ForEachPlacementPosition(name string, r RandomSource, origin FeaturePosition, context PlacementContext, visit func(FeaturePosition) error) error {
	placed, ok := s.Placed[name]
	if !ok {
		return fmt.Errorf("worldgen: placed feature %s missing", name)
	}
	return s.ForEachPlacementModifiers(name, placed.Placement, r, origin, context, visit)
}

type parsedPlacementModifier struct {
	valid      bool
	err        error
	errLabel   string
	noiseLevel float64
	belowNoise int
	aboveNoise int

	noiseRatio  int
	noiseFactor float64
	noiseOffset float64

	count        CountProvider
	rarityChance int
	heightPlan   PlacementPlan

	heightmap     string
	minOffset     int
	maxOffset     int
	maxWaterDepth int

	xzSpread placementIntProvider
	ySpread  placementIntProvider

	predicate     json.RawMessage
	scanDirection string
	scanTarget    json.RawMessage
	scanAllowed   json.RawMessage
	scanMaxSteps  int
}

func compilePlacementModifier(typ string, raw json.RawMessage) *parsedPlacementModifier {
	p := &parsedPlacementModifier{}
	switch typ {
	case "minecraft:noise_threshold_count":
		var value struct {
			NoiseLevel float64 `json:"noise_level"`
			BelowNoise int     `json:"below_noise"`
			AboveNoise int     `json:"above_noise"`
		}
		if err := json.Unmarshal(raw, &value); err == nil {
			p.valid = true
			p.noiseLevel = value.NoiseLevel
			p.belowNoise = value.BelowNoise
			p.aboveNoise = value.AboveNoise
		}
	case "minecraft:noise_based_count":
		var value struct {
			Ratio  int     `json:"noise_to_count_ratio"`
			Factor float64 `json:"noise_factor"`
			Offset float64 `json:"noise_offset"`
		}
		if err := json.Unmarshal(raw, &value); err == nil && value.Ratio >= 0 && value.Factor != 0 {
			p.valid = true
			p.noiseRatio = value.Ratio
			p.noiseFactor = value.Factor
			p.noiseOffset = value.Offset
		}
	case "minecraft:count":
		var value struct {
			Count json.RawMessage `json:"count"`
		}
		if err := json.Unmarshal(raw, &value); err != nil {
			p.err = err
			return p
		}
		count, err := parseIntProvider(value.Count)
		if err != nil {
			p.err = err
			return p
		}
		p.valid = true
		p.count = count
	case "minecraft:rarity_filter":
		var value struct {
			Chance int `json:"chance"`
		}
		if err := json.Unmarshal(raw, &value); err == nil && value.Chance >= 1 {
			p.valid = true
			p.rarityChance = value.Chance
		}
	case "minecraft:in_square", "minecraft:biome":
		p.valid = true
	case "minecraft:height_range":
		plan, err := placementHeightPlan(raw)
		if err != nil {
			p.err = err
			return p
		}
		p.valid = true
		p.heightPlan = plan
	case "minecraft:heightmap":
		var value struct {
			Heightmap string `json:"heightmap"`
		}
		if err := json.Unmarshal(raw, &value); err == nil && value.Heightmap != "" {
			p.valid = true
			p.heightmap = value.Heightmap
		}
	case "minecraft:surface_water_depth_filter":
		var value struct {
			MaxWaterDepth int `json:"max_water_depth"`
		}
		if err := json.Unmarshal(raw, &value); err == nil && value.MaxWaterDepth >= 0 {
			p.valid = true
			p.maxWaterDepth = value.MaxWaterDepth
		}
	case "minecraft:surface_relative_threshold_filter":
		var value struct {
			Heightmap string `json:"heightmap"`
			Min       *int   `json:"min_inclusive"`
			Max       *int   `json:"max_inclusive"`
		}
		if err := json.Unmarshal(raw, &value); err == nil && value.Heightmap != "" {
			p.valid = true
			p.heightmap = value.Heightmap
			p.minOffset, p.maxOffset = -int(^uint(0)>>1)-1, int(^uint(0)>>1)
			if value.Min != nil {
				p.minOffset = *value.Min
			}
			if value.Max != nil {
				p.maxOffset = *value.Max
			}
		}
	case "minecraft:random_offset":
		var value struct {
			XZSpread json.RawMessage `json:"xz_spread"`
			YSpread  json.RawMessage `json:"y_spread"`
		}
		if err := json.Unmarshal(raw, &value); err != nil {
			p.err = err
			return p
		}
		xz, err := parsePlacementIntProvider(value.XZSpread)
		if err != nil {
			p.err = err
			p.errLabel = "xz spread"
			return p
		}
		y, err := parsePlacementIntProvider(value.YSpread)
		if err != nil {
			p.err = err
			p.errLabel = "y spread"
			return p
		}
		p.valid = true
		p.xzSpread = xz
		p.ySpread = y
	case "minecraft:block_predicate_filter":
		var value struct {
			Predicate json.RawMessage `json:"predicate"`
		}
		if err := json.Unmarshal(raw, &value); err == nil && len(value.Predicate) > 0 {
			p.valid = true
			p.predicate = value.Predicate
		}
	case "minecraft:environment_scan":
		var value struct {
			Direction string          `json:"direction_of_search"`
			Target    json.RawMessage `json:"target_condition"`
			Allowed   json.RawMessage `json:"allowed_search_condition"`
			MaxSteps  int             `json:"max_steps"`
		}
		if err := json.Unmarshal(raw, &value); err == nil && value.MaxSteps >= 1 &&
			(value.Direction == "up" || value.Direction == "down") {
			p.valid = true
			p.scanDirection = value.Direction
			p.scanTarget = value.Target
			p.scanAllowed = value.Allowed
			p.scanMaxSteps = value.MaxSteps
		}
	}
	return p
}

// ForEachPlacementModifiers executes an arbitrary slice of placement modifiers
// (either a placed feature's modifier chain or an inline FeatureRef's placement
// array) in declared order.
func (s *FeatureSet) ForEachPlacementModifiers(name string, modifiers []PlacementModifier, r RandomSource, origin FeaturePosition, context PlacementContext, visit func(FeaturePosition) error) error {
	return applyPlacementModifiers(name, modifiers, 0, origin, r, context, visit)
}

func applyPlacementModifiers(name string, modifiers []PlacementModifier, index int, position FeaturePosition, r RandomSource, context PlacementContext, visit func(FeaturePosition) error) error {
	if index == len(modifiers) {
		return visit(position)
	}
	modifier := modifiers[index]
	p := modifier.parsed
	if p == nil {
		p = compilePlacementModifier(modifier.Type, modifier.Raw)
	}
	switch modifier.Type {
	case "minecraft:noise_threshold_count":
		if !p.valid {
			return fmt.Errorf("worldgen: %s invalid noise threshold count", name)
		}
		noise := BiomeInfoNoise(float64(position.X)/200.0, float64(position.Z)/200.0)
		count := p.aboveNoise
		if noise < p.noiseLevel {
			count = p.belowNoise
		}
		for ; count > 0; count-- {
			if err := applyPlacementModifiers(name, modifiers, index+1, position, r, context, visit); err != nil {
				return err
			}
		}
		return nil
	case "minecraft:noise_based_count":
		if !p.valid {
			return fmt.Errorf("worldgen: %s invalid noise based count", name)
		}
		noise := BiomeInfoNoise(float64(position.X)/p.noiseFactor, float64(position.Z)/p.noiseFactor)
		for count := int(math.Ceil((noise + p.noiseOffset) * float64(p.noiseRatio))); count > 0; count-- {
			if err := applyPlacementModifiers(name, modifiers, index+1, position, r, context, visit); err != nil {
				return err
			}
		}
		return nil
	case "minecraft:count":
		if !p.valid {
			return p.err
		}
		for n := p.count.Sample(r); n > 0; n-- {
			if err := applyPlacementModifiers(name, modifiers, index+1, position, r, context, visit); err != nil {
				return err
			}
		}
		return nil
	case "minecraft:rarity_filter":
		if !p.valid {
			return fmt.Errorf("worldgen: %s invalid rarity filter", name)
		}
		if r.NextFloat() < 1/float32(p.rarityChance) {
			return applyPlacementModifiers(name, modifiers, index+1, position, r, context, visit)
		}
		return nil
	case "minecraft:in_square":
		position.X += int(r.NextIntN(16))
		position.Z += int(r.NextIntN(16))
		return applyPlacementModifiers(name, modifiers, index+1, position, r, context, visit)
	case "minecraft:height_range":
		if !p.valid {
			return p.err
		}
		position.Y = p.heightPlan.SampleY(r, context.MinY, context.Height)
		return applyPlacementModifiers(name, modifiers, index+1, position, r, context, visit)
	case "minecraft:heightmap":
		if !p.valid {
			return fmt.Errorf("worldgen: %s invalid heightmap placement", name)
		}
		if context.HeightAt == nil {
			return fmt.Errorf("worldgen: %s heightmap placement requires HeightAt", name)
		}
		position.Y = context.HeightAt(p.heightmap, position.X, position.Z)
		if position.Y > context.MinY {
			return applyPlacementModifiers(name, modifiers, index+1, position, r, context, visit)
		}
		return nil
	case "minecraft:surface_water_depth_filter":
		if !p.valid {
			return fmt.Errorf("worldgen: %s invalid surface water depth filter", name)
		}
		if context.HeightAt == nil {
			return fmt.Errorf("worldgen: %s surface water depth filter requires HeightAt", name)
		}
		oceanFloor := context.HeightAt("OCEAN_FLOOR", position.X, position.Z)
		worldSurface := context.HeightAt("WORLD_SURFACE", position.X, position.Z)
		if worldSurface-oceanFloor <= p.maxWaterDepth {
			return applyPlacementModifiers(name, modifiers, index+1, position, r, context, visit)
		}
		return nil
	case "minecraft:surface_relative_threshold_filter":
		if !p.valid {
			return fmt.Errorf("worldgen: %s invalid surface relative threshold", name)
		}
		if context.HeightAt == nil {
			return fmt.Errorf("worldgen: %s surface relative threshold requires HeightAt", name)
		}
		surface := context.HeightAt(p.heightmap, position.X, position.Z)
		positionY, surfaceY := int64(position.Y), int64(surface)
		if positionY >= surfaceY+int64(p.minOffset) && positionY <= surfaceY+int64(p.maxOffset) {
			return applyPlacementModifiers(name, modifiers, index+1, position, r, context, visit)
		}
		return nil
	case "minecraft:random_offset":
		if !p.valid {
			if p.errLabel != "" {
				return fmt.Errorf("worldgen: %s %s: %w", name, p.errLabel, p.err)
			}
			return p.err
		}
		position.X += p.xzSpread.Sample(r)
		position.Y += p.ySpread.Sample(r)
		position.Z += p.xzSpread.Sample(r)
		return applyPlacementModifiers(name, modifiers, index+1, position, r, context, visit)
	case "minecraft:block_predicate_filter":
		if !p.valid {
			return fmt.Errorf("worldgen: %s invalid block predicate filter", name)
		}
		if context.BlockPredicate == nil {
			return fmt.Errorf("worldgen: %s block predicate filter requires BlockPredicate", name)
		}
		ok, err := context.BlockPredicate(p.predicate, position)
		if err != nil {
			return err
		}
		if ok {
			return applyPlacementModifiers(name, modifiers, index+1, position, r, context, visit)
		}
		return nil
	case "minecraft:environment_scan":
		if !p.valid {
			return fmt.Errorf("worldgen: %s invalid environment scan", name)
		}
		if context.BlockPredicate == nil {
			return fmt.Errorf("worldgen: %s environment scan requires BlockPredicate", name)
		}
		// An absent allowed_search_condition means every block is a
		// valid search step (vanilla's Optional<BlockPredicate>).
		if len(p.scanAllowed) > 0 {
			allowed, err := context.BlockPredicate(p.scanAllowed, position)
			if err != nil || !allowed {
				return err
			}
		}
		dy := 1
		if p.scanDirection == "down" {
			dy = -1
		}
		for step := 0; step < p.scanMaxSteps; step++ {
			matched, err := context.BlockPredicate(p.scanTarget, position)
			if err != nil {
				return err
			}
			if matched {
				return applyPlacementModifiers(name, modifiers, index+1, position, r, context, visit)
			}
			position.Y += dy
			if position.Y < context.MinY || position.Y >= context.MinY+context.Height {
				return nil
			}
			if len(p.scanAllowed) > 0 {
				allowed, err := context.BlockPredicate(p.scanAllowed, position)
				if err != nil {
					return err
				}
				if !allowed {
					break
				}
			}
		}
		matched, err := context.BlockPredicate(p.scanTarget, position)
		if err != nil {
			return err
		}
		if matched {
			return applyPlacementModifiers(name, modifiers, index+1, position, r, context, visit)
		}
		return nil
	case "minecraft:biome":
		if context.BiomeAllows == nil || context.BiomeAllows(position) {
			return applyPlacementModifiers(name, modifiers, index+1, position, r, context, visit)
		}
		return nil
	default:
		return fmt.Errorf("worldgen: %s unsupported executable placement modifier %q", name, modifier.Type)
	}
}

type placementIntProvider struct {
	typeName          string
	min, max, plateau int
	mean, deviation   float32
	source            *placementIntProvider
}

func (p placementIntProvider) Sample(r RandomSource) int {
	switch p.typeName {
	case "minecraft:uniform":
		return p.min + int(r.NextIntN(int32(p.max-p.min+1)))
	case "minecraft:trapezoid":
		if p.plateau == 0 && p.max == -p.min {
			return int(r.NextIntN(int32(p.max+1))) - int(r.NextIntN(int32(p.max+1)))
		}
		rangeSize := p.max - p.min
		if p.plateau == rangeSize {
			return p.min + int(r.NextIntN(int32(rangeSize+1)))
		}
		left := (rangeSize - p.plateau) / 2
		right := rangeSize - left
		return p.min + int(r.NextIntN(int32(right+1))) + int(r.NextIntN(int32(left+1)))
	case "minecraft:clamped_normal":
		value := float32(r.NextGaussian())*p.deviation + p.mean
		if value < float32(p.min) {
			value = float32(p.min)
		} else if value > float32(p.max) {
			value = float32(p.max)
		}
		return int(value)
	case "minecraft:clamped":
		// ClampedInt: clamp(source.sample(r), min, max); the source draws.
		if p.source == nil {
			return p.min
		}
		value := p.source.Sample(r)
		if value < p.min {
			return p.min
		}
		if value > p.max {
			return p.max
		}
		return value
	default:
		return p.min
	}
}

func parsePlacementIntProvider(raw json.RawMessage) (placementIntProvider, error) {
	var fixed int
	if err := json.Unmarshal(raw, &fixed); err == nil {
		return placementIntProvider{min: fixed, max: fixed}, nil
	}
	var value struct {
		Type            string `json:"type"`
		Min             int    `json:"min"`
		Max             int    `json:"max"`
		MinInclusive    int    `json:"min_inclusive"`
		MaxInclusive    int    `json:"max_inclusive"`
		Plateau         int    `json:"plateau"`
		Mean, Deviation float32
		Source          json.RawMessage `json:"source"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return placementIntProvider{}, err
	}
	provider := placementIntProvider{typeName: value.Type, plateau: value.Plateau, mean: value.Mean, deviation: value.Deviation}
	switch value.Type {
	case "minecraft:trapezoid":
		provider.min, provider.max = value.Min, value.Max
		if provider.max < provider.min || provider.plateau < 0 || provider.plateau > provider.max-provider.min {
			return placementIntProvider{}, fmt.Errorf("invalid trapezoid provider %s", raw)
		}
	case "minecraft:uniform", "minecraft:clamped_normal":
		provider.min, provider.max = value.MinInclusive, value.MaxInclusive
		if provider.max < provider.min || value.Type == "minecraft:clamped_normal" && provider.deviation <= 0 {
			return placementIntProvider{}, fmt.Errorf("invalid %s provider %s", value.Type, raw)
		}
	case "minecraft:clamped":
		provider.min, provider.max = value.MinInclusive, value.MaxInclusive
		if provider.max < provider.min || len(value.Source) == 0 {
			return placementIntProvider{}, fmt.Errorf("invalid clamped provider %s", raw)
		}
		source, err := parsePlacementIntProvider(value.Source)
		if err != nil {
			return placementIntProvider{}, err
		}
		provider.source = &source
	default:
		return placementIntProvider{}, fmt.Errorf("unsupported int provider %s", raw)
	}
	return provider, nil
}

func placementHeightPlan(raw json.RawMessage) (PlacementPlan, error) {
	var value struct {
		Height struct {
			Type    string          `json:"type"`
			Plateau int             `json:"plateau"`
			Min     json.RawMessage `json:"min_inclusive"`
			Max     json.RawMessage `json:"max_inclusive"`
		} `json:"height"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return PlacementPlan{}, err
	}
	min, err := parseFeatureHeight(value.Height.Min)
	if err != nil {
		return PlacementPlan{}, err
	}
	max, err := parseFeatureHeight(value.Height.Max)
	if err != nil {
		return PlacementPlan{}, err
	}
	if value.Height.Type != "minecraft:uniform" && value.Height.Type != "minecraft:trapezoid" &&
		value.Height.Type != "minecraft:very_biased_to_bottom" {
		return PlacementPlan{}, fmt.Errorf("worldgen: unsupported height distribution %q", value.Height.Type)
	}
	return PlacementPlan{HeightDistribution: value.Height.Type, HeightPlateau: value.Height.Plateau, MinY: min, MaxY: max}, nil
}

func parseIntProvider(raw json.RawMessage) (CountProvider, error) {
	var fixed int
	if err := json.Unmarshal(raw, &fixed); err == nil {
		return CountProvider{Min: fixed, Max: fixed}, nil
	}
	var clamped struct {
		Type         string          `json:"type"`
		MinInclusive int             `json:"min_inclusive"`
		MaxInclusive int             `json:"max_inclusive"`
		Source       json.RawMessage `json:"source"`
	}
	if err := json.Unmarshal(raw, &clamped); err == nil && clamped.Type == "minecraft:clamped" {
		if clamped.MaxInclusive < clamped.MinInclusive || len(clamped.Source) == 0 {
			return CountProvider{}, fmt.Errorf("invalid clamped provider %s", raw)
		}
		source, err := parseIntProvider(clamped.Source)
		if err != nil {
			return CountProvider{}, err
		}
		return CountProvider{Min: clamped.MinInclusive, Max: clamped.MaxInclusive, ClampedTo: &source}, nil
	}
	var uniform struct {
		Type string `json:"type"`
		Min  int    `json:"min_inclusive"`
		Max  int    `json:"max_inclusive"`
	}
	if err := json.Unmarshal(raw, &uniform); err == nil && uniform.Type == "minecraft:uniform" {
		return CountProvider{Min: uniform.Min, Max: uniform.Max}, nil
	}
	var weighted struct {
		Type         string `json:"type"`
		Distribution []struct {
			Data   int `json:"data"`
			Weight int `json:"weight"`
		} `json:"distribution"`
	}
	if err := json.Unmarshal(raw, &weighted); err == nil && weighted.Type == "minecraft:weighted_list" {
		provider := CountProvider{Weighted: make([]WeightedInt, 0, len(weighted.Distribution))}
		for _, entry := range weighted.Distribution {
			if entry.Weight > 0 {
				provider.Weighted = append(provider.Weighted, WeightedInt{Value: entry.Data, Weight: entry.Weight})
			}
		}
		return provider, nil
	}
	return CountProvider{}, fmt.Errorf("unsupported count provider %s", raw)
}

func parseFeatureHeight(raw json.RawMessage) (HeightProvider, error) {
	var value struct {
		Absolute    *int `json:"absolute"`
		AboveBottom *int `json:"above_bottom"`
		BelowTop    *int `json:"below_top"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return HeightProvider{}, err
	}
	if value.Absolute == nil && value.AboveBottom == nil && value.BelowTop == nil {
		return HeightProvider{}, fmt.Errorf("unsupported height provider %s", raw)
	}
	return HeightProvider{Absolute: value.Absolute, AboveBottom: value.AboveBottom, BelowTop: value.BelowTop}, nil
}

func intPtr(value int) *int { return &value }

func loadFeatureSet(data []byte) (*FeatureSet, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("worldgen: feature archive: %w", err)
	}
	set := &FeatureSet{
		Configured: make(map[string]ConfiguredFeature),
		Placed:     make(map[string]PlacedFeature),
		Biomes:     make(map[string]BiomeGeneration),
		BlockTags:  make(map[string][]string),
	}
	for _, file := range zr.File {
		r, err := file.Open()
		if err != nil {
			return nil, err
		}
		var raw json.RawMessage
		if err := json.NewDecoder(r).Decode(&raw); err != nil {
			r.Close()
			return nil, fmt.Errorf("worldgen: decode %s: %w", file.Name, err)
		}
		r.Close()
		name := resourceName(file.Name)
		switch {
		case strings.Contains(file.Name, "/configured_feature/"):
			var value ConfiguredFeature
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, fmt.Errorf("worldgen: configured feature %s: %w", name, err)
			}
			set.Configured[name] = value
		case strings.Contains(file.Name, "/placed_feature/"):
			var value struct {
				Feature   string            `json:"feature"`
				Placement []json.RawMessage `json:"placement"`
			}
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, fmt.Errorf("worldgen: placed feature %s: %w", name, err)
			}
			placed := PlacedFeature{Feature: value.Feature, Placement: make([]PlacementModifier, len(value.Placement))}
			for i, modifierRaw := range value.Placement {
				if err := json.Unmarshal(modifierRaw, &placed.Placement[i]); err != nil {
					return nil, fmt.Errorf("worldgen: placed feature %s modifier %d: %w", name, i, err)
				}
				placed.Placement[i].Raw = modifierRaw
			}
			set.Placed[name] = placed
		case strings.Contains(file.Name, "/biome/"):
			var biome BiomeGeneration
			if err := json.Unmarshal(raw, &biome); err != nil {
				return nil, fmt.Errorf("worldgen: biome %s: %w", name, err)
			}
			set.Biomes[name] = biome
		case strings.Contains(file.Name, "/tags/block/"):
			var tag struct {
				Values []string `json:"values"`
			}
			if err := json.Unmarshal(raw, &tag); err != nil {
				return nil, fmt.Errorf("worldgen: block tag %s: %w", name, err)
			}
			set.BlockTags[name] = tag.Values
		}
	}
	if len(set.Configured) < 200 || len(set.Placed) < 250 || len(set.Biomes) < 60 {
		return nil, fmt.Errorf("worldgen: incomplete feature archive: %d configured, %d placed, %d biomes",
			len(set.Configured), len(set.Placed), len(set.Biomes))
	}
	for name, placed := range set.Placed {
		if strings.HasPrefix(placed.Feature, "minecraft:") {
			if _, ok := set.Configured[placed.Feature]; !ok {
				return nil, fmt.Errorf("worldgen: placed feature %s references missing %s", name, placed.Feature)
			}
		}
	}
	for biomeName, biome := range set.Biomes {
		for stage, names := range biome.Features {
			for _, name := range names {
				if _, ok := set.Placed[name]; !ok {
					return nil, fmt.Errorf("worldgen: biome %s stage %d references missing %s", biomeName, stage, name)
				}
			}
		}
	}
	set.prewarmInlineFeatures()
	return set, nil
}

// prewarmInlineFeatures resolves all inline feature references at load time so
// inlineConfigured is pre-populated before concurrent chunk generation starts.
func (s *FeatureSet) prewarmInlineFeatures() {
	visited := make(map[string]bool, len(s.Configured))
	for {
		var pending []string
		for name := range s.Configured {
			if !visited[name] {
				pending = append(pending, name)
			}
		}
		for name := range s.inlineConfigured {
			if !visited[name] {
				pending = append(pending, name)
			}
		}
		if len(pending) == 0 {
			return
		}
		for _, name := range pending {
			visited[name] = true
			cf, ok := s.LookupConfigured(name)
			if !ok {
				continue
			}
			switch cf.Type {
			case "minecraft:vegetation_patch", "minecraft:waterlogged_vegetation_patch":
				_, _ = s.VegetationPatch(name)
			case "minecraft:root_system":
				_, _ = s.RootSystem(name)
			case "minecraft:random_selector":
				_, _ = s.RandomSelector(name)
			case "minecraft:simple_random_selector":
				_, _ = s.SimpleRandomSelector(name)
			case "minecraft:random_boolean_selector":
				_, _ = s.RandomBooleanSelector(name)
			}
		}
	}
}

func resourceName(file string) string {
	base := strings.TrimSuffix(path.Base(file), ".json")
	return "minecraft:" + base
}

// fieldIn reports whether name appears in a measured field set.
func fieldIn(fields []string, name string) bool {
	for _, f := range fields {
		if f == name {
			return true
		}
	}
	return false
}
