package worldgen

import (
	"archive/zip"
	"hash/fnv"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
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

	inlineMu sync.Mutex
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
	Type string
	Raw  json.RawMessage
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
// the feature can choose a state with the feature RNG at placement time.
type SimpleBlockFeatureConfig struct {
	States []WeightedBlockState
}

type WeightedBlockState struct {
	State  BlockState
	Weight int
}

// StateProviderSpec is the parsed form of a block-state provider: one of
// simple, weighted, or randomized-int-over-another-provider. Sample draws
// from the feature RNG exactly like vanilla's BlockStateProvider.getState.
type StateProviderSpec struct {
	Type     string
	State    BlockState            // simple
	Entries  []WeightedBlockState  // weighted
	Property string                // randomized_int
	Source   *StateProviderSpec    // randomized_int
	Values   CountProvider         // randomized_int
}

// SampleState mirrors BlockStateProvider.getState(random): simple returns
// without a draw, weighted draws nextInt(totalWeight) and walks the entries,
// randomized_int draws its source then the int provider and overrides the
// named property.
func (p StateProviderSpec) SampleState(r RandomSource) (BlockState, bool) {
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
		base, ok := p.Source.SampleState(r)
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
	}
	return BlockState{}, false
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

type TreeFeatureConfig struct {
	TrunkProvider struct {
		Type  string     `json:"type"`
		State BlockState `json:"state"`
	} `json:"trunk_provider"`
	FoliageProvider struct {
		Type  string     `json:"type"`
		State BlockState `json:"state"`
	} `json:"foliage_provider"`
	TrunkPlacer struct {
		Type        string `json:"type"`
		BaseHeight  int    `json:"base_height"`
		HeightRandA int    `json:"height_rand_a"`
		HeightRandB int    `json:"height_rand_b"`
	} `json:"trunk_placer"`
	FoliagePlacer struct {
		Type   string `json:"type"`
		Height int    `json:"height"`
		Offset int    `json:"offset"`
		Radius int    `json:"radius"`
	} `json:"foliage_placer"`
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

// FeatureSteps mirrors FeatureSorter.buildFeaturesPerStep. biomeOrder must be
// BiomeSource.possibleBiomes encounter order because it assigns the stable
// identity used to break otherwise unconstrained graph ties.
func (s *FeatureSet) FeatureSteps(biomeOrder []string) ([][]string, error) {
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
	configured, ok := s.Configured[name]
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
	return config, nil
}

func (s *FeatureSet) Disk(name string) (DiskFeatureConfig, error) {
	configured, ok := s.Configured[name]
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
	return config, nil
}

func (s *FeatureSet) Geode(name string) (GeodeFeatureConfig, error) {
	configured, ok := s.Configured[name]
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
	return config, nil
}

func (s *FeatureSet) VegetationPatch(name string) (VegetationPatchFeatureConfig, error) {
	configured, ok := s.Configured[name]
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
	return config, nil
}

func (s *FeatureSet) SimpleBlock(name string) (SimpleBlockFeatureConfig, error) {
	configured, ok := s.Configured[name]
	if !ok || configured.Type != "minecraft:simple_block" {
		return SimpleBlockFeatureConfig{}, fmt.Errorf("worldgen: %s is not a simple block feature", name)
	}
	var raw struct {
		ToPlace json.RawMessage `json:"to_place"`
	}
	if err := json.Unmarshal(configured.Config, &raw); err != nil {
		return SimpleBlockFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	var provider struct {
		Type    string     `json:"type"`
		State   BlockState `json:"state"`
		Entries []struct {
			Data   BlockState `json:"data"`
			Weight int        `json:"weight"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw.ToPlace, &provider); err != nil {
		return SimpleBlockFeatureConfig{}, fmt.Errorf("worldgen: decode %s provider: %w", name, err)
	}
	config := SimpleBlockFeatureConfig{}
	switch provider.Type {
	case "minecraft:simple_state_provider":
		config.States = []WeightedBlockState{{State: provider.State, Weight: 1}}
	case "minecraft:weighted_state_provider":
		for _, entry := range provider.Entries {
			if entry.Weight > 0 && entry.Data.Name != "" {
				config.States = append(config.States, WeightedBlockState{State: entry.Data, Weight: entry.Weight})
			}
		}
	default:
		return SimpleBlockFeatureConfig{}, fmt.Errorf("worldgen: unsupported simple block provider %q", provider.Type)
	}
	if len(config.States) == 0 {
		return SimpleBlockFeatureConfig{}, fmt.Errorf("worldgen: invalid simple block feature %s", name)
	}
	return config, nil
}

func (s *FeatureSet) UnderwaterMagma(name string) (UnderwaterMagmaFeatureConfig, error) {
	configured, ok := s.Configured[name]
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
	return config, nil
}

func (s *FeatureSet) Probability(name string) (ProbabilityFeatureConfig, error) {
	configured, ok := s.Configured[name]
	if !ok || configured.Type != "minecraft:seagrass" {
		return ProbabilityFeatureConfig{}, fmt.Errorf("worldgen: %s is not seagrass", name)
	}
	var config ProbabilityFeatureConfig
	if err := json.Unmarshal(configured.Config, &config); err != nil {
		return ProbabilityFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	if config.Probability < 0 || config.Probability > 1 {
		return ProbabilityFeatureConfig{}, fmt.Errorf("worldgen: invalid probability %s", name)
	}
	return config, nil
}

func (s *FeatureSet) Spring(name string) (SpringFeatureConfig, error) {
	configured, ok := s.Configured[name]
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
	return config, nil
}

func (s *FeatureSet) Tree(name string) (TreeFeatureConfig, error) {
	configured, ok := s.Configured[name]
	if !ok || configured.Type != "minecraft:tree" {
		return TreeFeatureConfig{}, fmt.Errorf("worldgen: %s is not a tree feature", name)
	}
	var config TreeFeatureConfig
	if err := json.Unmarshal(configured.Config, &config); err != nil {
		return TreeFeatureConfig{}, fmt.Errorf("worldgen: decode %s: %w", name, err)
	}
	if config.TrunkPlacer.Type == "" || config.FoliagePlacer.Type == "" || config.TrunkProvider.State.Name == "" || config.FoliageProvider.State.Name == "" {
		return TreeFeatureConfig{}, fmt.Errorf("worldgen: invalid tree config %s", name)
	}
	return config, nil
}

func (s *FeatureSet) RandomSelector(name string) (RandomSelectorConfig, error) {
	configured, ok := s.Configured[name]
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
	return selector, nil
}

// BlockColumn parses the block_column configured feature: direction, layers
// (nested height provider + state provider), the allowed-placement predicate,
// and the tip priority.
func (s *FeatureSet) BlockColumn(name string) (BlockColumnFeatureConfig, error) {
	configured, ok := s.Configured[name]
	if !ok || configured.Type != "minecraft:block_column" {
		return BlockColumnFeatureConfig{}, fmt.Errorf("worldgen: %s is not a block column", name)
	}
	var raw struct {
		Allowed       json.RawMessage `json:"allowed_placement"`
		Direction     string          `json:"direction"`
		Layers        []struct {
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
	return config, nil
}

// SimpleRandomSelector parses the simple_random_selector configured feature.
func (s *FeatureSet) SimpleRandomSelector(name string) (SimpleRandomSelectorConfig, error) {
	configured, ok := s.Configured[name]
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
func parseStateProviderSpec(raw json.RawMessage) (StateProviderSpec, error) {
	var probe struct {
		Type     string          `json:"type"`
		State    BlockState      `json:"state"`
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
	}
	return StateProviderSpec{}, fmt.Errorf("unsupported state provider %s", raw)
}

type WeightedBlockStateRaw struct {
	Data   BlockState `json:"data"`
	Weight int        `json:"weight"`
}

func (s *FeatureSet) RandomBooleanSelector(name string) (RandomBooleanSelectorConfig, error) {	configured, ok := s.Configured[name]
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
	if _, exists := s.Configured[key]; !exists {
		s.Configured[key] = ConfiguredFeature{Type: doc.Type, Config: doc.Config}
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
	var apply func(int, FeaturePosition) error
	apply = func(index int, position FeaturePosition) error {
		if index == len(placed.Placement) {
			return visit(position)
		}
		modifier := placed.Placement[index]
		next := func(value FeaturePosition) error { return apply(index+1, value) }
		switch modifier.Type {
		case "minecraft:noise_based_count":
			var value struct {
				Ratio  int     `json:"noise_to_count_ratio"`
				Factor float64 `json:"noise_factor"`
				Offset float64 `json:"noise_offset"`
			}
			if err := json.Unmarshal(modifier.Raw, &value); err != nil || value.Ratio < 0 || value.Factor == 0 {
				return fmt.Errorf("worldgen: %s invalid noise based count", name)
			}
			noise := BiomeInfoNoise(float64(position.X)/value.Factor, float64(position.Z)/value.Factor)
			for count := int(math.Ceil((noise + value.Offset) * float64(value.Ratio))); count > 0; count-- {
				if err := next(position); err != nil {
					return err
				}
			}
			return nil
		case "minecraft:count":
			var value struct {
				Count json.RawMessage `json:"count"`
			}
			if err := json.Unmarshal(modifier.Raw, &value); err != nil {
				return err
			}
			count, err := parseIntProvider(value.Count)
			if err != nil {
				return err
			}
			for n := count.Sample(r); n > 0; n-- {
				if err := next(position); err != nil {
					return err
				}
			}
			return nil
		case "minecraft:rarity_filter":
			var value struct {
				Chance int `json:"chance"`
			}
			if err := json.Unmarshal(modifier.Raw, &value); err != nil || value.Chance < 1 {
				return fmt.Errorf("worldgen: %s invalid rarity filter", name)
			}
			if r.NextFloat() < 1/float32(value.Chance) {
				return next(position)
			}
			return nil
		case "minecraft:in_square":
			position.X += int(r.NextIntN(16))
			position.Z += int(r.NextIntN(16))
			return next(position)
		case "minecraft:height_range":
			plan, err := placementHeightPlan(modifier.Raw)
			if err != nil {
				return err
			}
			position.Y = plan.SampleY(r, context.MinY, context.Height)
			return next(position)
		case "minecraft:heightmap":
			var value struct {
				Heightmap string `json:"heightmap"`
			}
			if err := json.Unmarshal(modifier.Raw, &value); err != nil || value.Heightmap == "" {
				return fmt.Errorf("worldgen: %s invalid heightmap placement", name)
			}
			if context.HeightAt == nil {
				return fmt.Errorf("worldgen: %s heightmap placement requires HeightAt", name)
			}
			position.Y = context.HeightAt(value.Heightmap, position.X, position.Z)
			if position.Y > context.MinY {
				return next(position)
			}
			return nil
		case "minecraft:surface_water_depth_filter":
			var value struct {
				MaxWaterDepth int `json:"max_water_depth"`
			}
			if err := json.Unmarshal(modifier.Raw, &value); err != nil || value.MaxWaterDepth < 0 {
				return fmt.Errorf("worldgen: %s invalid surface water depth filter", name)
			}
			if context.HeightAt == nil {
				return fmt.Errorf("worldgen: %s surface water depth filter requires HeightAt", name)
			}
			oceanFloor := context.HeightAt("OCEAN_FLOOR", position.X, position.Z)
			worldSurface := context.HeightAt("WORLD_SURFACE", position.X, position.Z)
			if worldSurface-oceanFloor <= value.MaxWaterDepth {
				return next(position)
			}
			return nil
		case "minecraft:surface_relative_threshold_filter":
			var value struct {
				Heightmap string `json:"heightmap"`
				Min       *int   `json:"min_inclusive"`
				Max       *int   `json:"max_inclusive"`
			}
			if err := json.Unmarshal(modifier.Raw, &value); err != nil || value.Heightmap == "" {
				return fmt.Errorf("worldgen: %s invalid surface relative threshold", name)
			}
			if context.HeightAt == nil {
				return fmt.Errorf("worldgen: %s surface relative threshold requires HeightAt", name)
			}
			minOffset, maxOffset := -int(^uint(0)>>1)-1, int(^uint(0)>>1)
			if value.Min != nil {
				minOffset = *value.Min
			}
			if value.Max != nil {
				maxOffset = *value.Max
			}
			surface := context.HeightAt(value.Heightmap, position.X, position.Z)
			positionY, surfaceY := int64(position.Y), int64(surface)
			if positionY >= surfaceY+int64(minOffset) && positionY <= surfaceY+int64(maxOffset) {
				return next(position)
			}
			return nil
		case "minecraft:random_offset":
			var value struct {
				XZSpread json.RawMessage `json:"xz_spread"`
				YSpread  json.RawMessage `json:"y_spread"`
			}
			if err := json.Unmarshal(modifier.Raw, &value); err != nil {
				return err
			}
			xz, err := parsePlacementIntProvider(value.XZSpread)
			if err != nil {
				return fmt.Errorf("worldgen: %s xz spread: %w", name, err)
			}
			y, err := parsePlacementIntProvider(value.YSpread)
			if err != nil {
				return fmt.Errorf("worldgen: %s y spread: %w", name, err)
			}
			position.X += xz.Sample(r)
			position.Y += y.Sample(r)
			position.Z += xz.Sample(r)
			return next(position)
		case "minecraft:block_predicate_filter":
			var value struct {
				Predicate json.RawMessage `json:"predicate"`
			}
			if err := json.Unmarshal(modifier.Raw, &value); err != nil || len(value.Predicate) == 0 {
				return fmt.Errorf("worldgen: %s invalid block predicate filter", name)
			}
			if context.BlockPredicate == nil {
				return fmt.Errorf("worldgen: %s block predicate filter requires BlockPredicate", name)
			}
			ok, err := context.BlockPredicate(value.Predicate, position)
			if err != nil {
				return err
			}
			if ok {
				return next(position)
			}
			return nil
		case "minecraft:environment_scan":
			var value struct {
				Direction string          `json:"direction_of_search"`
				Target    json.RawMessage `json:"target_condition"`
				Allowed   json.RawMessage `json:"allowed_search_condition"`
				MaxSteps  int             `json:"max_steps"`
			}
			if err := json.Unmarshal(modifier.Raw, &value); err != nil || value.MaxSteps < 1 ||
				value.Direction != "up" && value.Direction != "down" {
				return fmt.Errorf("worldgen: %s invalid environment scan", name)
			}
			if context.BlockPredicate == nil {
				return fmt.Errorf("worldgen: %s environment scan requires BlockPredicate", name)
			}
			// An absent allowed_search_condition means every block is a
			// valid search step (vanilla's Optional<BlockPredicate>).
			if len(value.Allowed) > 0 {
				allowed, err := context.BlockPredicate(value.Allowed, position)
				if err != nil || !allowed {
					return err
				}
			}
			dy := 1
			if value.Direction == "down" {
				dy = -1
			}
			for step := 0; step < value.MaxSteps; step++ {
				matched, err := context.BlockPredicate(value.Target, position)
				if err != nil {
					return err
				}
				if matched {
					return next(position)
				}
				position.Y += dy
				if position.Y < context.MinY || position.Y >= context.MinY+context.Height {
					return nil
				}
				if len(value.Allowed) > 0 {
					allowed, err := context.BlockPredicate(value.Allowed, position)
					if err != nil {
						return err
					}
					if !allowed {
						break
					}
				}
			}
			matched, err := context.BlockPredicate(value.Target, position)
			if err != nil {
				return err
			}
			if matched {
				return next(position)
			}
			return nil
		case "minecraft:biome":
			if context.BiomeAllows == nil || context.BiomeAllows(position) {
				return next(position)
			}
			return nil
		default:
			return fmt.Errorf("worldgen: %s unsupported executable placement modifier %q", name, modifier.Type)
		}
	}
	return apply(0, origin)
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
		Type            string          `json:"type"`
		Min             int             `json:"min"`
		Max             int             `json:"max"`
		MinInclusive    int             `json:"min_inclusive"`
		MaxInclusive    int             `json:"max_inclusive"`
		Plateau         int             `json:"plateau"`
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
		Type          string          `json:"type"`
		MinInclusive  int             `json:"min_inclusive"`
		MaxInclusive  int             `json:"max_inclusive"`
		Source        json.RawMessage `json:"source"`
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
	return set, nil
}

func resourceName(file string) string {
	base := strings.TrimSuffix(path.Base(file), ".json")
	return "minecraft:" + base
}
