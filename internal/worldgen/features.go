package worldgen

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"path"
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

type PlacementPlan struct {
	Count              CountProvider
	RarityChance       int
	HeightDistribution string
	MinY               HeightProvider
	MaxY               HeightProvider
}

type CountProvider struct {
	Min, Max int
}

type HeightProvider struct {
	Absolute    *int
	AboveBottom *int
	BelowTop    *int
}

func (p CountProvider) Sample(r RandomSource) int {
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
		plateau := 0
		triangle := span - plateau
		return lo + int(r.NextIntN(int32((triangle+1)/2))) + int(r.NextIntN(int32(triangle/2+1)))
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
			if value.Height.Type != "minecraft:uniform" && value.Height.Type != "minecraft:trapezoid" {
				return PlacementPlan{}, fmt.Errorf("worldgen: %s unsupported height distribution %q", name, value.Height.Type)
			}
			plan.HeightDistribution, plan.MinY, plan.MaxY = value.Height.Type, min, max
		case "minecraft:in_square", "minecraft:biome":
			// Coordinate spreading and biome validation are applied by the world
			// executor. Keeping them in the parsed plan preserves their order.
		default:
			return PlacementPlan{}, fmt.Errorf("worldgen: %s unsupported placement modifier %q", name, modifier.Type)
		}
	}
	return plan, nil
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
	if err := json.Unmarshal(raw, &uniform); err != nil || uniform.Type != "minecraft:uniform" {
		return CountProvider{}, fmt.Errorf("unsupported count provider %s", raw)
	}
	return CountProvider{Min: uniform.Min, Max: uniform.Max}, nil
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
