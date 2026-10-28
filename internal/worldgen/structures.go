package worldgen

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
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

// ConcentricRings mirrors minecraft:concentric_rings placement (strongholds).
type ConcentricRings struct {
	Distance        int    `json:"distance"`
	Spread          int    `json:"spread"`
	Count           int    `json:"count"`
	PreferredBiomes string `json:"preferred_biomes"`
}

// ExclusionZone mirrors StructurePlacement.ExclusionZone (e.g. pillager_outposts
// forbidding placement within ChunkCount chunks of a start chunk in OtherSet).
type ExclusionZone struct {
	OtherSet   string        `json:"other_set"`
	ChunkCount int           `json:"chunk_count"`
	other      *StructureSet `json:"-"`
}

// Placement is the tagged union of structure placement types.
type Placement struct {
	Type            string           `json:"type"`
	RandomSpread    *RandomSpread    `json:"-"`
	Concentric      bool             `json:"-"`
	ConcentricRings *ConcentricRings `json:"-"`
	ExclusionZone   *ExclusionZone   `json:"-"`
}

type rawPlacement struct {
	Type            string         `json:"type"`
	Salt            int            `json:"salt"`
	Separation      int            `json:"separation"`
	Spacing         int            `json:"spacing"`
	SpreadType      string         `json:"spread_type"`
	Frequency       float32        `json:"frequency"`
	FreqMethod      string         `json:"frequency_reduction_method"`
	LocateOffset    []int          `json:"locate_offset"`
	Distance        int            `json:"distance"`
	Spread          int            `json:"spread"`
	Count           int            `json:"count"`
	PreferredBiomes string         `json:"preferred_biomes"`
	ExclusionZone   *ExclusionZone `json:"exclusion_zone"`
}

func (p *rawPlacement) decode() (*Placement, error) {
	switch p.Type {
	case "minecraft:random_spread":
		out := &Placement{
			Type: p.Type,
			RandomSpread: &RandomSpread{
				Salt: p.Salt, Separation: p.Separation, Spacing: p.Spacing,
				SpreadType: p.SpreadType, Frequency: p.Frequency, FreqMethod: p.FreqMethod,
			},
			ExclusionZone: p.ExclusionZone,
		}
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
		return &Placement{
			Type:       p.Type,
			Concentric: true,
			ConcentricRings: &ConcentricRings{
				Distance:        p.Distance,
				Spread:          p.Spread,
				Count:           p.Count,
				PreferredBiomes: p.PreferredBiomes,
			},
			ExclusionZone: p.ExclusionZone,
		}, nil
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
	Name              string              `json:"-"`
	Type              string              `json:"type"`
	Biomes            string              `json:"biomes"`
	Step              string              `json:"step"`
	TerrainAdaptation string              `json:"terrain_adaptation"`
	// BiomeTemp is the ocean-ruin variant discriminator: "cold" | "warm".
	BiomeTemp          string              `json:"biome_temp"`
	LargeProbability   float32             `json:"large_probability"`
	ClusterProbability float32             `json:"cluster_probability"`
	// MineshaftType is the mineshaft variant discriminator: "normal" | "mesa".
	MineshaftType string              `json:"mineshaft_type"`
	IsBeached     bool                `json:"is_beached"`
	Setups        []RuinedPortalSetup `json:"-"`

	// Jigsaw structure fields.
	StartPool               string          `json:"start_pool"`
	StartJigsawName         string          `json:"start_jigsaw_name"`
	Size                    int             `json:"size"`
	MaxDistanceFromCenter   int             `json:"max_distance_from_center"`
	ProjectStartToHeightmap string          `json:"project_start_to_heightmap"`
	UseExpansionHack        bool            `json:"use_expansion_hack"`
	StartHeight             json.RawMessage `json:"start_height"`
	DimensionPadding        int             `json:"dimension_padding"`
	LiquidSettings          string          `json:"liquid_settings"`
	PoolAliases             json.RawMessage `json:"pool_aliases"`

	startHeightParsed  bool     `json:"-"`
	startHeightUniform bool     `json:"-"`
	startHeightMinY    int      `json:"-"`
	startHeightMaxY    int      `json:"-"`
	resolvedBiomes     []string `json:"-"`
}

func parseStartHeightRaw(raw json.RawMessage) (parsed, uniform bool, minY, maxY int) {
	if len(raw) == 0 {
		return true, false, 0, 0
	}
	var doc struct {
		Type         string `json:"type"`
		Absolute     *int   `json:"absolute"`
		MinInclusive struct {
			Absolute int `json:"absolute"`
		} `json:"min_inclusive"`
		MaxInclusive struct {
			Absolute int `json:"absolute"`
		} `json:"max_inclusive"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return true, false, 0, 0
	}
	if doc.Absolute != nil {
		return true, false, *doc.Absolute, *doc.Absolute
	}
	if doc.Type == "minecraft:uniform" || doc.Type == "uniform" {
		return true, true, doc.MinInclusive.Absolute, doc.MaxInclusive.Absolute
	}
	return true, false, 0, 0
}

// SampleStartHeight evaluates the structure's start_height HeightProvider
// (either {"absolute": N} or {"type": "minecraft:uniform", "min_inclusive": {"absolute": A}, "max_inclusive": {"absolute": B}}).
func (d *StructureDef) SampleStartHeight(random RandomSource) int {
	parsed, uniform, minY, maxY := d.startHeightParsed, d.startHeightUniform, d.startHeightMinY, d.startHeightMaxY
	if !parsed {
		_, uniform, minY, maxY = parseStartHeightRaw(d.StartHeight)
	}
	if !uniform {
		return minY
	}
	if minY > maxY {
		return minY
	}
	return minY + int(random.NextIntN(int32(maxY-minY+1)))
}

func decodeStructureDef(name string, raw []byte) (*StructureDef, error) {
	var doc struct {
		Type                    string              `json:"type"`
		Biomes                  string              `json:"biomes"`
		Step                    string              `json:"step"`
		TerrainAdaptation       string              `json:"terrain_adaptation"`
		BiomeTemp               string              `json:"biome_temp"`
		LargeProbability        float32             `json:"large_probability"`
		ClusterProbability      float32             `json:"cluster_probability"`
		MineshaftType           string              `json:"mineshaft_type"`
		IsBeached               bool                `json:"is_beached"`
		Setups                  []RuinedPortalSetup `json:"setups"`
		StartPool               string              `json:"start_pool"`
		StartJigsawName         string              `json:"start_jigsaw_name"`
		Size                    int                 `json:"size"`
		MaxDistanceFromCenter   int                 `json:"max_distance_from_center"`
		ProjectStartToHeightmap string              `json:"project_start_to_heightmap"`
		UseExpansionHack        bool                `json:"use_expansion_hack"`
		StartHeight             json.RawMessage     `json:"start_height"`
		DimensionPadding        int                 `json:"dimension_padding"`
		LiquidSettings          string              `json:"liquid_settings"`
		PoolAliases             json.RawMessage     `json:"pool_aliases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	parsed, uniform, minY, maxY := parseStartHeightRaw(doc.StartHeight)
	return &StructureDef{
		Name:                    name,
		Type:                    doc.Type,
		Biomes:                  strings.TrimPrefix(doc.Biomes, "minecraft:"),
		Step:                    doc.Step,
		TerrainAdaptation:       doc.TerrainAdaptation,
		Setups:                  doc.Setups,
		BiomeTemp:               doc.BiomeTemp,
		LargeProbability:        doc.LargeProbability,
		ClusterProbability:      doc.ClusterProbability,
		MineshaftType:           doc.MineshaftType,
		IsBeached:               doc.IsBeached,
		StartPool:               doc.StartPool,
		StartJigsawName:         doc.StartJigsawName,
		Size:                    doc.Size,
		MaxDistanceFromCenter:   doc.MaxDistanceFromCenter,
		ProjectStartToHeightmap: doc.ProjectStartToHeightmap,
		UseExpansionHack:        doc.UseExpansionHack,
		StartHeight:             doc.StartHeight,
		DimensionPadding:        doc.DimensionPadding,
		LiquidSettings:          doc.LiquidSettings,
		PoolAliases:             doc.PoolAliases,
		startHeightParsed:       parsed,
		startHeightUniform:      uniform,
		startHeightMinY:         minY,
		startHeightMaxY:         maxY,
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
	Sets            map[string]*StructureSet        // set name -> set
	Structures      map[string]*StructureDef        // structure name -> def
	BiomeTags       map[string][]string             // biome tag name -> member names, flattened
	BiomeTagSets    map[string]map[string]bool      // biome tag name -> member lookup set
	setOrder        []string                        // deterministic iteration order
	structuresBySet map[string][]*StructureDef      // expanded weighted entries per set
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
		BiomeTagSets:    map[string]map[string]bool{},
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
		set := make(map[string]bool, len(flat))
		for _, m := range flat {
			set[m] = true
		}
		out.BiomeTagSets[name] = set
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
		ref := def.Biomes
		if strings.HasPrefix(ref, "#") {
			tagName := strings.TrimPrefix(ref[1:], "minecraft:")
			tagName = strings.TrimPrefix(tagName, "has_structure/")
			def.resolvedBiomes = out.BiomeTags["minecraft:"+tagName]
		} else if ref != "" {
			def.resolvedBiomes = []string{"minecraft:" + ref}
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
	for _, set := range out.Sets {
		if set.Placement != nil && set.Placement.ExclusionZone != nil {
			ez := set.Placement.ExclusionZone
			otherName := "minecraft:" + strings.TrimPrefix(ez.OtherSet, "minecraft:")
			ez.other = out.Sets[otherName]
		}
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
	if def != nil && def.resolvedBiomes != nil {
		return def.resolvedBiomes
	}
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
	var random Legacy
	random.SetLargeFeatureWithSalt(seed, int(regionX), int(regionZ), r.Salt)
	span := r.Spacing - r.Separation
	offsetX := spreadEvaluate(&random, r.SpreadType, span)
	offsetZ := spreadEvaluate(&random, r.SpreadType, span)
	return [2]int32{regionX*int32(r.Spacing) + int32(offsetX), regionZ*int32(r.Spacing) + int32(offsetZ)}
}

// PotentialChunk exposes the region pick for tests and structure placement.
func (r *RandomSpread) PotentialChunk(seed int64, chunkX, chunkZ int32) [2]int32 {
	return r.potentialStructureChunk(seed, chunkX, chunkZ)
}

// spreadEvaluate is RandomSpreadType.evaluate.
func spreadEvaluate(random *Legacy, spreadType string, span int) int {
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
	var random Legacy
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

// RingPositions ports ChunkGeneratorStructureState.generateRingPositions and
// BiomeSource.findBiomeHorizontal. When biomeAtQuart is non-nil and
// allowedBiomes is non-empty, each ring anchor is snapped within a 112-block
// (28-quart) horizontal square at y=0 to a uniformly sampled preferred biome
// using random.Fork() reservoir sampling.
func (c *ConcentricRings) RingPositions(seed int64, biomeAtQuart func(qx, qy, qz int) string, allowedBiomes map[string]bool) [][2]int32 {
	if c == nil || c.Count <= 0 || c.Distance <= 0 || c.Spread <= 0 {
		return nil
	}
	distance := c.Distance
	count := c.Count
	spread := c.Spread
	random := NewLegacy(seed)
	d := random.NextDouble() * math.Pi * 2.0
	i := 0
	j := 0
	positions := make([][2]int32, count)
	forkedRNGs := make([]RandomSource, count)
	for k := 0; k < count; k++ {
		e := float64(4*distance+distance*j*6) + (random.NextDouble()-0.5)*(float64(distance)*2.5)
		l := int32(math.Floor(math.Cos(d)*e + 0.5))
		m := int32(math.Floor(math.Sin(d)*e + 0.5))
		forkedRNGs[k] = random.Fork()
		positions[k] = [2]int32{l, m}
		d += (math.Pi * 2.0) / float64(spread)
		i++
		if i == spread {
			j++
			i = 0
			spread = spread + 2*spread/(j+1)
			if spread > count-k {
				spread = count - k
			}
			d += random.NextDouble() * math.Pi * 2.0
		}
	}
	if biomeAtQuart != nil && len(allowedBiomes) > 0 {
		var wg sync.WaitGroup
		for k := 0; k < count; k++ {
			wg.Add(1)
			go func(k int) {
				defer wg.Done()
				l, m := positions[k][0], positions[k][1]
				forked := forkedRNGs[k]
				pos := [2]int32{l, m}
				centerQX := (int(l)*16 + 8) >> 2
				centerQZ := (int(m)*16 + 8) >> 2
				const radiusQ = 112 >> 2 // 28
				n := 0
				hasBest := false
				for dz := -radiusQ; dz <= radiusQ; dz++ {
					for dx := -radiusQ; dx <= radiusQ; dx++ {
						qx := centerQX + dx
						qz := centerQZ + dz
						if allowedBiomes[biomeAtQuart(qx, 0, qz)] {
							if !hasBest || forked.NextIntN(int32(n+1)) == 0 {
								pos = [2]int32{int32(qx >> 2), int32(qz >> 2)}
								hasBest = true
							}
							n++
						}
					}
				}
				positions[k] = pos
			}(k)
		}
		wg.Wait()
	}
	return positions
}

// isPlacementForbidden ports StructurePlacement.ExclusionZone.isPlacementForbidden
// and ChunkGeneratorStructureState.hasStructureChunkInRange: returns true if
// any chunk within ChunkCount of (chunkX, chunkZ) is a structure start chunk of
// the referenced OtherSet.
func (ez *ExclusionZone) isPlacementForbidden(seed int64, chunkX, chunkZ int32) bool {
	if ez == nil || ez.other == nil || ez.ChunkCount <= 0 {
		return false
	}
	r := int32(ez.ChunkCount)
	minCX, maxCX := chunkX-r, chunkX+r
	minCZ, maxCZ := chunkZ-r, chunkZ+r
	if spread := ez.other.Placement.RandomSpread; spread != nil && spread.Spacing > 0 {
		sp := int32(spread.Spacing)
		minRX := floorDiv32(minCX, sp)
		maxRX := floorDiv32(maxCX, sp)
		minRZ := floorDiv32(minCZ, sp)
		maxRZ := floorDiv32(maxCZ, sp)
		for rx := minRX; rx <= maxRX; rx++ {
			for rz := minRZ; rz <= maxRZ; rz++ {
				picked := spread.potentialStructureChunk(seed, rx*sp, rz*sp)
				if picked[0] >= minCX && picked[0] <= maxCX && picked[1] >= minCZ && picked[1] <= maxCZ {
					if ez.other.IsStartChunk(seed, picked[0], picked[1]) {
						return true
					}
				}
			}
		}
		return false
	}
	for x := minCX; x <= maxCX; x++ {
		for z := minCZ; z <= maxCZ; z++ {
			if ez.other.IsStartChunk(seed, x, z) {
				return true
			}
		}
	}
	return false
}

// IsStartChunk reports whether any structure of this set starts in the given
// chunk: the random-spread grid picks the chunk, the legacy frequency roll
// keeps or discards it, concentric_rings checks the generated ring anchors,
// and any exclusion_zone restriction is enforced.
func (s *StructureSet) IsStartChunk(seed int64, chunkX, chunkZ int32) bool {
	if s.Placement.Concentric {
		if s.Placement.ConcentricRings == nil {
			return false
		}
		found := false
		for _, pos := range s.Placement.ConcentricRings.RingPositions(seed, nil, nil) {
			if pos[0] == chunkX && pos[1] == chunkZ {
				found = true
				break
			}
		}
		if !found {
			return false
		}
		if s.Placement.ExclusionZone != nil && s.Placement.ExclusionZone.isPlacementForbidden(seed, chunkX, chunkZ) {
			return false
		}
		return true
	}
	spread := s.Placement.RandomSpread
	picked := spread.potentialStructureChunk(seed, chunkX, chunkZ)
	if picked[0] != chunkX || picked[1] != chunkZ {
		return false
	}
	if !spread.frequencyAllows(seed, chunkX, chunkZ) {
		return false
	}
	if s.Placement.ExclusionZone != nil && s.Placement.ExclusionZone.isPlacementForbidden(seed, chunkX, chunkZ) {
		return false
	}
	return true
}

// StrongholdRingPositions computes the 128 stronghold chunk positions for seed,
// snapping each ring anchor to #minecraft:stronghold_biased_to when
// biomeAtQuart is non-nil.
func (s *StructureSets) StrongholdRingPositions(seed int64, biomeAtQuart func(qx, qy, qz int) string) [][2]int32 {
	set := s.Sets["minecraft:strongholds"]
	if set == nil || set.Placement == nil || set.Placement.ConcentricRings == nil {
		return nil
	}
	var allowed map[string]bool
	if biomeAtQuart != nil {
		tag := strings.TrimPrefix(set.Placement.ConcentricRings.PreferredBiomes, "#")
		tag = "minecraft:" + strings.TrimPrefix(tag, "minecraft:")
		allowed = s.BiomeTagSets[tag]
		if allowed == nil {
			members := s.BiomeTags[tag]
			allowed = make(map[string]bool, len(members))
			for _, m := range members {
				allowed[m] = true
			}
		}
	}
	return set.Placement.ConcentricRings.RingPositions(seed, biomeAtQuart, allowed)
}

// IsStartChunkWithBiomes reports whether setName's placement selects (chunkX, chunkZ),
// using biome-snapped ring positions for concentric_rings sets when biomeAtQuart is provided.
func (s *StructureSets) IsStartChunkWithBiomes(setName string, seed int64, chunkX, chunkZ int32, biomeAtQuart func(qx, qy, qz int) string) bool {
	name := "minecraft:" + strings.TrimPrefix(setName, "minecraft:")
	set := s.Sets[name]
	if set == nil || set.Placement == nil {
		return false
	}
	if set.Placement.Concentric {
		for _, pos := range s.StrongholdRingPositions(seed, biomeAtQuart) {
			if pos[0] == chunkX && pos[1] == chunkZ {
				if set.Placement.ExclusionZone != nil && set.Placement.ExclusionZone.isPlacementForbidden(seed, chunkX, chunkZ) {
					return false
				}
				return true
			}
		}
		return false
	}
	return set.IsStartChunk(seed, chunkX, chunkZ)
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
	if i, ok := structureIndexCache["underground_decoration/"+name]; ok {
		return i
	}
	if i, ok := structureIndexCache["strongholds/"+name]; ok {
		return i
	}
	return 0
}
