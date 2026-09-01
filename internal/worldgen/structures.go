package worldgen

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"
)

// structures.go loads the vanilla 26.1.2 structure datapack: the structure-set
// JSONs that decide which chunk each structure tries to start in, the structure
// JSONs themselves (biome filter and generation step for now), and the worldgen
// biome tags the structure JSONs reference. Placement follows
// StructurePlacement and its subclasses byte for byte, including the four
// legacy frequency reducers and their exact RNG draws.
//
// The block-level work of each structure type (jigsaw assembly, template
// pieces, procedural pieces such as mineshaft corridors) lives above this
// layer; everything here answers only "does a structure of this set start in
// this chunk".

// WeightedEntry is one {structure, weight} pair of a structure set.
type WeightedEntry struct {
	Structure string `json:"structure"`
	Weight    int    `json:"weight"`
}

// RandomSpread mirrors minecraft:random_spread placement.
type RandomSpread struct {
	Salt       int     `json:"salt"`
	Separation int     `json:"separation"`
	Spacing    int     `json:"spacing"`
	SpreadType string  `json:"spread_type"`
	Frequency  float32 `json:"frequency"`
	FreqMethod string  `json:"frequency_reduction_method"`
	LocateOffX int     `json:"-"`
	LocateOffZ int     `json:"-"`
}

// Placement is the tagged union of structure placement types.
type Placement struct {
	Type         string       `json:"type"`
	RandomSpread *RandomSpread `json:"-"`
	Concentric   bool         `json:"-"`
}

type rawPlacement struct {
	Type          string  `json:"type"`
	Salt          int     `json:"salt"`
	Separation    int     `json:"separation"`
	Spacing       int     `json:"spacing"`
	SpreadType    string  `json:"spread_type"`
	Frequency     float32 `json:"frequency"`
	FreqMethod    string  `json:"frequency_reduction_method"`
	LocateOffset  []int   `json:"locate_offset"`
	Distance      int     `json:"distance"`
	Count         int     `json:"count"`
	PreferredBiomes string  `json:"preferred_biomes"`
}

func (p *rawPlacement) decode() (*Placement, error) {
	switch p.Type {
	case "minecraft:random_spread":
		out := &Placement{Type: p.Type, RandomSpread: &RandomSpread{
			Salt: p.Salt, Separation: p.Separation, Spacing: p.Spacing,
			SpreadType: p.SpreadType, Frequency: p.Frequency, FreqMethod: p.FreqMethod,
		}}
		if len(p.LocateOffset) == 3 {
			out.RandomSpread.LocateOffX, out.RandomSpread.LocateOffZ = p.LocateOffset[0], p.LocateOffset[2]
		}
		if out.RandomSpread.SpreadType == "" {
			out.RandomSpread.SpreadType = "linear"
		}
		if out.RandomSpread.FreqMethod == "" {
			out.RandomSpread.FreqMethod = "default"
		}
		if out.RandomSpread.Frequency == 0 {
			out.RandomSpread.Frequency = 1.0
		}
		return out, nil
	case "minecraft:concentric_rings":
		return &Placement{Type: p.Type, Concentric: true}, nil
	default:
		return nil, fmt.Errorf("unsupported structure placement %q", p.Type)
	}
}

// RuinedPortalSetup is one weighted entry of a ruined portal structure's
// "setups" list.
type RuinedPortalSetup struct {
	Placement             string  `json:"placement"`
	AirPocketProbability  float32 `json:"air_pocket_probability"`
	Mossiness             float32 `json:"mossiness"`
	Overgrown             bool    `json:"overgrown"`
	Vines                 bool    `json:"vines"`
	ReplaceWithBlackstone bool    `json:"replace_with_blackstone"`
	CanBeCold             bool    `json:"can_be_cold"`
	Weight                float32 `json:"weight"`
}

// StructureDef is one entry of data/minecraft/worldgen/structure.
type StructureDef struct {
	Name   string              `json:"-"`
	Type   string              `json:"type"`
	Biomes string              `json:"biomes"`
	Step   string              `json:"step"`
	// BiomeTemp is the ocean-ruin variant discriminator: "cold" | "warm".
	BiomeTemp string              `json:"biome_temp"`
	// MineshaftType is the mineshaft variant discriminator: "normal" | "mesa".
	MineshaftType string          `json:"mineshaft_type"`
	Setups []RuinedPortalSetup `json:"-"`
}

func decodeStructureDef(name string, raw []byte) (*StructureDef, error) {
	var doc struct {
		Type          string             `json:"type"`
		Biomes        string             `json:"biomes"`
		Step          string             `json:"step"`
		BiomeTemp     string             `json:"biome_temp"`
		MineshaftType string             `json:"mineshaft_type"`
		Setups        []RuinedPortalSetup `json:"setups"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return &StructureDef{
		Name: name, Type: doc.Type, Biomes: strings.TrimPrefix(doc.Biomes, "minecraft:"),
		Step: doc.Step, Setups: doc.Setups, BiomeTemp: doc.BiomeTemp,
		MineshaftType: doc.MineshaftType,
	}, nil
}

// StructureSet is one entry of data/minecraft/worldgen/structure_set.
type StructureSet struct {
	Name       string          `json:"-"`
	Structures []WeightedEntry `json:"structures"`
	Placement  *Placement      `json:"-"`
}

// StructureSets is the parsed, immutable structure datapack.
type StructureSets struct {
	Sets       map[string]*StructureSet            // set name -> set
	Structures map[string]*StructureDef            // structure name -> def
	BiomeTags  map[string][]string                 // biome tag name -> member names, flattened
	setOrder   []string                            // deterministic iteration order
	structuresBySet map[string][]*StructureDef    // expanded weighted entries per set
}

var (
	structureSetsOnce sync.Once
	structureSetsRef  *StructureSets
	structureSetsErr  error
)

// LoadStructureSets parses and validates the embedded structure datapack.
func LoadStructureSets() (*StructureSets, error) {
	structureSetsOnce.Do(func() { structureSetsRef, structureSetsErr = loadStructureSets() })
	return structureSetsRef, structureSetsErr
}

func loadStructureSets() (*StructureSets, error) {
	out := &StructureSets{
		Sets:            map[string]*StructureSet{},
		Structures:      map[string]*StructureDef{},
		BiomeTags:       map[string][]string{},
		structuresBySet: map[string][]*StructureDef{},
	}

	tagFiles, err := fs.Glob(dataFS, "data/biome_tag/*.json")
	if err != nil {
		return nil, err
	}
	rawTags := map[string][]string{}
	for _, path := range tagFiles {
		raw, err := dataFS.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var doc struct {
			Values []string `json:"values"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		name := "minecraft:" + strings.TrimSuffix(baseName(path), ".json")
		rawTags[name] = doc.Values
	}
	var flatten func(name string, seen map[string]bool) []string
	flatten = func(name string, seen map[string]bool) []string {
		if members, ok := out.BiomeTags[name]; ok {
			return members
		}
		if seen[name] {
			return nil
		}
		seen[name] = true
		var flat []string
		for _, value := range rawTags[name] {
			if strings.HasPrefix(value, "#") {
				ref := strings.TrimPrefix(value[1:], "minecraft:")
				flat = append(flat, flatten("minecraft:"+ref, seen)...)
				continue
			}
			flat = append(flat, "minecraft:"+strings.TrimPrefix(value, "minecraft:"))
		}
		out.BiomeTags[name] = flat
		return flat
	}
	for name := range rawTags {
		flatten(name, map[string]bool{})
	}

	defFiles, err := fs.Glob(dataFS, "data/structure_json/*.json")
	if err != nil {
		return nil, err
	}
	for _, path := range defFiles {
		raw, err := dataFS.ReadFile(path)
		if err != nil {
			return nil, err
		}
		name := "minecraft:" + strings.TrimSuffix(baseName(path), ".json")
		def, err := decodeStructureDef(name, raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out.Structures[name] = def
	}

	setFiles, err := fs.Glob(dataFS, "data/structure_set/*.json")
	if err != nil {
		return nil, err
	}
	for _, path := range setFiles {
		raw, err := dataFS.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var doc struct {
			Structures []WeightedEntry `json:"structures"`
			Placement  rawPlacement    `json:"placement"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		placement, err := doc.Placement.decode()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		name := "minecraft:" + strings.TrimSuffix(baseName(path), ".json")
		set := &StructureSet{Name: name, Structures: doc.Structures, Placement: placement}
		out.Sets[name] = set
		out.setOrder = append(out.setOrder, name)
		expanded := make([]*StructureDef, 0, len(set.Structures))
		for _, entry := range set.Structures {
			def, ok := out.Structures[strings.TrimPrefix(entry.Structure, "minecraft:")]
			if !ok {
				def, ok = out.Structures[entry.Structure]
			}
			if !ok {
				continue
			}
			expanded = append(expanded, def)
		}
		out.structuresBySet[name] = expanded
	}
	return out, nil
}

// SetOrder returns set names in filesystem order, which is stable per build.
func (s *StructureSets) SetOrder() []string { return s.setOrder }

// StructuresInSet expands the weighted entries of a set into its definitions.
func (s *StructureSets) StructuresInSet(set string) []*StructureDef {
	return s.structuresBySet[set]
}

// BiomesFor resolves a structure def's biome reference ("#tag" or a single
// name) to concrete biome names with the minecraft: prefix. Structure JSONs
// reference tags through the has_structure/ namespace, but the extracted tag
// files drop that prefix.
func (s *StructureSets) BiomesFor(def *StructureDef) []string {
	ref := def.Biomes
	if strings.HasPrefix(ref, "#") {
		name := strings.TrimPrefix(ref[1:], "minecraft:")
		name = strings.TrimPrefix(name, "has_structure/")
		return s.BiomeTags["minecraft:"+name]
	}
	return []string{"minecraft:" + ref}
}

// potentialStructureChunk is RandomSpreadStructurePlacement.
// getPotentialStructureChunk: pick the cell's offset chunk with two draws from
// a Legacy stream salted by the region coordinates.
func (r *RandomSpread) potentialStructureChunk(seed int64, chunkX, chunkZ int32) [2]int32 {
	regionX := floorDiv32(chunkX, int32(r.Spacing))
	regionZ := floorDiv32(chunkZ, int32(r.Spacing))
	random := NewLegacy(0)
	random.SetLargeFeatureWithSalt(seed, int(regionX), int(regionZ), r.Salt)
	span := r.Spacing - r.Separation
	offsetX := spreadEvaluate(random, r.SpreadType, span)
	offsetZ := spreadEvaluate(random, r.SpreadType, span)
	return [2]int32{regionX*int32(r.Spacing) + int32(offsetX), regionZ*int32(r.Spacing) + int32(offsetZ)}
}

// PotentialChunk exposes the region pick for tests and structure placement.
func (r *RandomSpread) PotentialChunk(seed int64, chunkX, chunkZ int32) [2]int32 {
	return r.potentialStructureChunk(seed, chunkX, chunkZ)
}

// spreadEvaluate is RandomSpreadType.evaluate.
func spreadEvaluate(random RandomSource, spreadType string, span int) int {
	if spreadType == "triangular" {
		return (int(random.NextIntN(int32(span))) + int(random.NextIntN(int32(span)))) / 2
	}
	return int(random.NextIntN(int32(span)))
}

// frequencyAllows is applyAdditionalChunkRestrictions plus the four reducer
// bodies from StructurePlacement. Runs only when frequency < 1.
func (r *RandomSpread) frequencyAllows(seed int64, chunkX, chunkZ int32) bool {
	if r.Frequency >= 1.0 {
		return true
	}
	random := NewLegacy(0)
	switch r.FreqMethod {
	case "default":
		random.SetLargeFeatureWithSalt(seed, int(chunkX), int(chunkZ), r.Salt)
		return random.NextFloat() < r.Frequency
	case "legacy_type_1": // legacyPillagerOutpostReducer
		regionX := int(chunkX) >> 4
		regionZ := int(chunkZ) >> 4
		random.SetSeed(int64(int32(regionX^(regionZ<<4))) ^ seed)
		random.NextInt() // vanilla discards one full-range draw
		bound := int(float32(1.0) / r.Frequency)
		if bound <= 0 {
			return false
		}
		return random.NextIntN(int32(bound)) == 0
	case "legacy_type_2": // legacyArbitrarySaltProbabilityReducer
		random.SetLargeFeatureWithSalt(seed, int(chunkX), int(chunkZ), 10387320)
		return random.NextFloat() < r.Frequency
	case "legacy_type_3": // legacyProbabilityReducerWithDouble
		random.SetLargeFeatureSeed(seed, int(chunkX), int(chunkZ))
		return random.NextDouble() < float64(r.Frequency)
	default:
		return false
	}
}

// IsStartChunk reports whether any structure of this set starts in the given
// chunk: the random-spread grid picks the chunk, the legacy frequency roll
// keeps or discards it.
func (s *StructureSet) IsStartChunk(seed int64, chunkX, chunkZ int32) bool {
	if s.Placement.Concentric {
		// Stronghold rings are not wired yet; nothing claims the chunk.
		return false
	}
	spread := s.Placement.RandomSpread
	picked := spread.potentialStructureChunk(seed, chunkX, chunkZ)
	if picked[0] != chunkX || picked[1] != chunkZ {
		return false
	}
	return spread.frequencyAllows(seed, chunkX, chunkZ)
}

func floorDiv32(a, b int32) int32 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func baseName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}


// StructureIndexInStep returns a structure's position in the alphabetically
// ordered list of structures sharing its generation step - the counter
// applyBiomeDecoration passes to setFeatureSeed before placing the step's
// structure pieces. Verified empirically for mineshafts (index 1 in
// underground_structures) and used by every per-chunk structure placement.
var (
	structureIndexOnce  sync.Once
	structureIndexCache map[string]int
)

// structureIndexOverride lets tests pin a structure's step index when the
// alphabetical hypothesis needs empirical verification (the mineshaft index
// was confirmed this way). -1 disables the override.
var structureIndexOverride = map[string]int{}
var structureStepOverride = map[string]int{}

func SetStructureIndexOverride(name string, index int) {
	if index < 0 {
		delete(structureIndexOverride, name)
		return
	}
	structureIndexOverride[name] = index
}

func SetStructureStepOverride(name string, step int) {
	if step < 0 {
		delete(structureStepOverride, name)
		return
	}
	structureStepOverride[name] = step
}

// StructureStepOverride reports the pinned step for a structure when tests
// scan the reseed parameters.
func StructureStepOverride(name string) (int, bool) {
	step, ok := structureStepOverride[name]
	return step, ok
}

func StructureIndexInStep(structureName string) int {
	if idx, ok := structureIndexOverride[structureName]; ok {
		return idx
	}
	structureIndexOnce.Do(func() {
		sets, err := LoadStructureSets()
		if err != nil {
			return
		}
		byStep := map[string][]string{}
		for defName, def := range sets.Structures {
			byStep[def.Step] = append(byStep[def.Step], defName)
		}
		structureIndexCache = make(map[string]int, len(sets.Structures))
		for step, names := range byStep {
			sort.Strings(names)
			for i, n := range names {
				key := strings.TrimPrefix(strings.TrimPrefix(n, "minecraft:"), "structure/")
				structureIndexCache[step+"/"+key] = i
			}
		}
	})
	name := strings.TrimPrefix(structureName, "minecraft:")
	if i, ok := structureIndexCache["surface_structures/"+name]; ok {
		return i
	}
	if i, ok := structureIndexCache["underground_structures/"+name]; ok {
		return i
	}
	return 0
}
