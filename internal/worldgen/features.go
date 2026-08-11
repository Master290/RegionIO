package worldgen

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
)

//go:embed feature_data.zip
var featureData []byte

// FeatureSet is the validated vanilla datapack graph used by decoration.
type FeatureSet struct {
	Configured map[string]ConfiguredFeature
	Placed     map[string]PlacedFeature
	Biomes     map[string]BiomeGeneration
	BlockTags  map[string][]string
}

type IndexedFeature struct {
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
		// Vanilla's TrapezoidHeight works with the inclusive range distance,
		// then performs two inclusive random draws around the midpoint.
		rangeSize := span - 1
		if rangeSize <= 0 {
			return lo
		}
		left := rangeSize / 2
		right := rangeSize - left
		return lo + int(r.NextIntN(int32(right+1))) + int(r.NextIntN(int32(left+1)))
	}
	if p.HeightDistribution == "minecraft:very_biased_to_bottom" {
		const inner = 8
		if span-inner <= 0 {
			return lo
		}
		first := lo + inner + int(r.NextIntN(int32(span-inner)))
		second := lo + int(r.NextIntN(int32(first-lo)))
		return lo + int(r.NextIntN(int32(second-lo+inner)))
	}
	return lo + int(r.NextIntN(int32(span)))
}

func DecorationRandom(seed int64, chunkX, chunkZ int) (*Legacy, int64) {
	random := NewLegacy(0)
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
	selector.Default, err = parseFeatureRef(raw.Default)
	if err != nil {
		return RandomSelectorConfig{}, err
	}
	for i, entry := range raw.Features {
		selector.Features[i].Chance = entry.Chance
		selector.Features[i].Feature, err = parseFeatureRef(entry.Feature)
		if err != nil {
			return RandomSelectorConfig{}, err
		}
	}
	return selector, nil
}

func parseFeatureRef(raw json.RawMessage) (FeatureRef, error) {
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		return FeatureRef{Name: name}, nil
	}
	var inline struct {
		Feature   string            `json:"feature"`
		Placement []json.RawMessage `json:"placement"`
	}
	if err := json.Unmarshal(raw, &inline); err != nil || inline.Feature == "" {
		return FeatureRef{}, fmt.Errorf("worldgen: invalid feature reference %s", raw)
	}
	ref := FeatureRef{Name: inline.Feature, Placement: make([]PlacementModifier, len(inline.Placement))}
	for i, modifier := range inline.Placement {
		if err := json.Unmarshal(modifier, &ref.Placement[i]); err != nil {
			return FeatureRef{}, err
		}
		ref.Placement[i].Raw = modifier
	}
	return ref, nil
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
					Type string          `json:"type"`
					Min  json.RawMessage `json:"min_inclusive"`
					Max  json.RawMessage `json:"max_inclusive"`
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
			plan.HeightDistribution, plan.MinY, plan.MaxY = value.Height.Type, min, max
		case "minecraft:in_square", "minecraft:biome", "minecraft:surface_water_depth_filter",
			"minecraft:heightmap", "minecraft:block_predicate_filter", "minecraft:noise_threshold_count",
			"minecraft:random_offset":
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
	placed, ok := s.Placed[name]
	if !ok {
		return nil, fmt.Errorf("worldgen: placed feature %s missing", name)
	}
	var result []FeaturePosition
	var apply func(int, FeaturePosition) error
	apply = func(index int, position FeaturePosition) error {
		if index == len(placed.Placement) {
			result = append(result, position)
			return nil
		}
		modifier := placed.Placement[index]
		next := func(value FeaturePosition) error { return apply(index+1, value) }
		switch modifier.Type {
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
		case "minecraft:biome":
			if context.BiomeAllows == nil || context.BiomeAllows(position) {
				return next(position)
			}
			return nil
		default:
			return fmt.Errorf("worldgen: %s unsupported executable placement modifier %q", name, modifier.Type)
		}
	}
	if err := apply(0, origin); err != nil {
		return nil, err
	}
	return result, nil
}

type placementIntProvider struct {
	typeName          string
	min, max, plateau int
	mean, deviation   float32
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
	default:
		return placementIntProvider{}, fmt.Errorf("unsupported int provider %s", raw)
	}
	return provider, nil
}

func placementHeightPlan(raw json.RawMessage) (PlacementPlan, error) {
	var value struct {
		Height struct {
			Type string          `json:"type"`
			Min  json.RawMessage `json:"min_inclusive"`
			Max  json.RawMessage `json:"max_inclusive"`
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
	return PlacementPlan{HeightDistribution: value.Height.Type, MinY: min, MaxY: max}, nil
}

func parseIntProvider(raw json.RawMessage) (CountProvider, error) {
	var fixed int
	if err := json.Unmarshal(raw, &fixed); err == nil {
		return CountProvider{Min: fixed, Max: fixed}, nil
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
