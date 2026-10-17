package worldgen

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
)

//go:embed structure_data.zip
var structureDataZip []byte

var (
	structureZipOnce sync.Once
	structureZipMap  map[string][]byte
	jigsawRegistryMu sync.Once
	jigsawRegistry   *JigsawDataRegistry
	jigsawRegErr     error
)

func ensureStructureZipMap() map[string][]byte {
	structureZipOnce.Do(func() {
		structureZipMap = make(map[string][]byte, 1200)
		zr, err := zip.NewReader(bytes.NewReader(structureDataZip), int64(len(structureDataZip)))
		if err != nil {
			return
		}
		for _, f := range zr.File {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			b, err := io.ReadAll(rc)
			rc.Close()
			if err == nil {
				structureZipMap[f.Name] = b
			}
		}
	})
	return structureZipMap
}

// EmbeddedStructureTemplateZip reads raw NBT bytes for a structure template
// (e.g. "village/plains/houses/plains_small_house_1") from structure_data.zip.
func EmbeddedStructureTemplateZip(name string) ([]byte, bool) {
	clean := strings.TrimPrefix(name, "minecraft:")
	m := ensureStructureZipMap()
	b, ok := m["data/minecraft/structure/"+clean+".nbt"]
	return b, ok
}

// RulePredicateDef is an input_predicate or location_predicate in a RuleProcessor.
type RulePredicateDef struct {
	PredicateType string     `json:"predicate_type"`
	Block         string     `json:"block"`
	BlockState    BlockState `json:"block_state"`
	Tag           string     `json:"tag"`
	Probability   float32    `json:"probability"`
}

// PosPredicateDef is a position_predicate in a RuleProcessor.
type PosPredicateDef struct {
	PredicateType string  `json:"predicate_type"`
	MinChance     float32 `json:"min_chance"`
	MaxChance     float32 `json:"max_chance"`
	MinDist       int     `json:"min_dist"`
	MaxDist       int     `json:"max_dist"`
	Axis          string  `json:"axis"`
}

// ProcessorRuleDef is one rule inside a RuleProcessor.
type ProcessorRuleDef struct {
	InputPredicate    RulePredicateDef `json:"input_predicate"`
	LocationPredicate RulePredicateDef `json:"location_predicate"`
	PositionPredicate PosPredicateDef  `json:"position_predicate"`
	OutputState       BlockState       `json:"output_state"`
}

// ProcessorDef is one entry of a StructureProcessorList.
type ProcessorDef struct {
	ProcessorType  string             `json:"processor_type"`
	Rules          []ProcessorRuleDef `json:"rules"`
	Delegate       *ProcessorDef      `json:"delegate"`
	Limit          int                `json:"-"`
	Integrity      float32            `json:"integrity"`
	RottableBlocks []string           `json:"-"`
	ProtectedTag   string             `json:"value"`
}

func (p *ProcessorDef) UnmarshalJSON(data []byte) error {
	type rawProc struct {
		ProcessorType  string             `json:"processor_type"`
		Rules          []ProcessorRuleDef `json:"rules"`
		Delegate       *ProcessorDef      `json:"delegate"`
		Limit          json.RawMessage    `json:"limit"`
		Integrity      float32            `json:"integrity"`
		RottableBlocks json.RawMessage    `json:"rottable_blocks"`
		Value          string             `json:"value"`
	}
	var r rawProc
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	p.ProcessorType = r.ProcessorType
	p.Rules = r.Rules
	p.Delegate = r.Delegate
	p.Integrity = r.Integrity
	p.ProtectedTag = r.Value
	if len(r.Limit) > 0 {
		var n int
		if err := json.Unmarshal(r.Limit, &n); err == nil {
			p.Limit = n
		} else {
			var obj struct {
				Value int `json:"value"`
			}
			if err := json.Unmarshal(r.Limit, &obj); err == nil {
				p.Limit = obj.Value
			}
		}
	}
	if len(r.RottableBlocks) > 0 {
		var s string
		if err := json.Unmarshal(r.RottableBlocks, &s); err == nil {
			p.RottableBlocks = []string{s}
		} else {
			var arr []string
			if err := json.Unmarshal(r.RottableBlocks, &arr); err == nil {
				p.RottableBlocks = arr
			}
		}
	}
	return nil
}

// PoolElementDef describes one StructurePoolElement inside a StructureTemplatePool.
type PoolElementDef struct {
	ElementType            string           // "minecraft:single_pool_element", "minecraft:legacy_single_pool_element", "minecraft:list_pool_element", "minecraft:feature_pool_element", "minecraft:empty_pool_element"
	Location               string           // template location for single/legacy_single
	ProcessorsRef          string           // named processor list reference (if any)
	InlineProcessors       []ProcessorDef   // inline processor list (if any)
	Projection             string           // "rigid" or "terrain_matching"
	OverrideLiquidSettings string           // "" | "apply_waterlogging" | "ignore_waterlogging"
	Elements               []PoolElementDef // sub-elements for list_pool_element
	Feature                string           // placed feature name for feature_pool_element
}

func (e *PoolElementDef) SetProjectionRecursive(proj string) {
	e.Projection = proj
	for i := range e.Elements {
		e.Elements[i].SetProjectionRecursive(proj)
	}
}

func (e *PoolElementDef) UnmarshalJSON(data []byte) error {
	var raw struct {
		ElementType            string          `json:"element_type"`
		Location               string          `json:"location"`
		Processors             json.RawMessage `json:"processors"`
		Projection             string          `json:"projection"`
		OverrideLiquidSettings string          `json:"override_liquid_settings"`
		Elements               []PoolElementDef `json:"elements"`
		Feature                string          `json:"feature"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	e.ElementType = raw.ElementType
	e.Location = raw.Location
	e.Projection = raw.Projection
	e.OverrideLiquidSettings = raw.OverrideLiquidSettings
	e.Elements = raw.Elements
	e.Feature = raw.Feature
	if e.Projection != "" && len(e.Elements) > 0 {
		for i := range e.Elements {
			e.Elements[i].SetProjectionRecursive(e.Projection)
		}
	}
	if len(raw.Processors) > 0 {
		var ref string
		if err := json.Unmarshal(raw.Processors, &ref); err == nil {
			e.ProcessorsRef = ref
		} else {
			var obj struct {
				Processors []ProcessorDef `json:"processors"`
			}
			if err := json.Unmarshal(raw.Processors, &obj); err == nil && obj.Processors != nil {
				e.InlineProcessors = obj.Processors
			} else {
				var arr []ProcessorDef
				if err := json.Unmarshal(raw.Processors, &arr); err == nil {
					e.InlineProcessors = arr
				}
			}
		}
	}
	return nil
}

// WeightedPoolElement is one entry in a template_pool JSON's "elements" array.
type WeightedPoolElement struct {
	Element PoolElementDef `json:"element"`
	Weight  int            `json:"weight"`
}

// TemplatePoolDef is one StructureTemplatePool loaded from data/minecraft/worldgen/template_pool.
type TemplatePoolDef struct {
	Name     string                `json:"-"`
	Fallback string                `json:"fallback"`
	Elements []WeightedPoolElement `json:"elements"`
	Expanded []PoolElementDef      `json:"-"`
}

// JigsawDataRegistry holds all vanilla template_pool and processor_list definitions.
type JigsawDataRegistry struct {
	Pools          map[string]*TemplatePoolDef
	ProcessorLists map[string][]ProcessorDef
}

// LoadJigsawDataRegistry parses and caches all template_pool and processor_list
// JSON files from structure_data.zip.
func LoadJigsawDataRegistry() (*JigsawDataRegistry, error) {
	jigsawRegistryMu.Do(func() {
		m := ensureStructureZipMap()
		reg := &JigsawDataRegistry{
			Pools:          make(map[string]*TemplatePoolDef, 200),
			ProcessorLists: make(map[string][]ProcessorDef, 48),
		}
		reg.Pools["minecraft:empty"] = &TemplatePoolDef{
			Name:     "minecraft:empty",
			Fallback: "minecraft:empty",
		}
		reg.ProcessorLists["minecraft:empty"] = nil

		const poolPrefix = "data/minecraft/worldgen/template_pool/"
		const procPrefix = "data/minecraft/worldgen/processor_list/"

		for name, raw := range m {
			switch {
			case strings.HasPrefix(name, poolPrefix) && strings.HasSuffix(name, ".json"):
				rel := strings.TrimSuffix(strings.TrimPrefix(name, poolPrefix), ".json")
				key := "minecraft:" + rel
				var pool TemplatePoolDef
				if err := json.Unmarshal(raw, &pool); err != nil {
					jigsawRegErr = fmt.Errorf("template_pool %s: %w", key, err)
					return
				}
				pool.Name = key
				if pool.Fallback != "" && !strings.HasPrefix(pool.Fallback, "minecraft:") {
					pool.Fallback = "minecraft:" + pool.Fallback
				}
				for _, we := range pool.Elements {
					for i := 0; i < we.Weight; i++ {
						pool.Expanded = append(pool.Expanded, we.Element)
					}
				}
				reg.Pools[key] = &pool

			case strings.HasPrefix(name, procPrefix) && strings.HasSuffix(name, ".json"):
				rel := strings.TrimSuffix(strings.TrimPrefix(name, procPrefix), ".json")
				key := "minecraft:" + rel
				var wrapper struct {
					Processors []ProcessorDef `json:"processors"`
				}
				if err := json.Unmarshal(raw, &wrapper); err != nil {
					jigsawRegErr = fmt.Errorf("processor_list %s: %w", key, err)
					return
				}
				reg.ProcessorLists[key] = wrapper.Processors
			}
		}
		jigsawRegistry = reg
	})
	return jigsawRegistry, jigsawRegErr
}

// ResolveProcessors returns the processor slice for a PoolElementDef.
func (r *JigsawDataRegistry) ResolveProcessors(elem PoolElementDef) []ProcessorDef {
	if len(elem.InlineProcessors) > 0 {
		return elem.InlineProcessors
	}
	if elem.ProcessorsRef == "" || elem.ProcessorsRef == "minecraft:empty" {
		return nil
	}
	key := "minecraft:" + strings.TrimPrefix(elem.ProcessorsRef, "minecraft:")
	return r.ProcessorLists[key]
}

// GetPool looks up a template pool by resource location.
func (r *JigsawDataRegistry) GetPool(name string) *TemplatePoolDef {
	if name == "" {
		return nil
	}
	key := "minecraft:" + strings.TrimPrefix(name, "minecraft:")
	return r.Pools[key]
}

// Beardifier kernel and fast inverse square root (net.minecraft.world.level.levelgen.Beardifier).
var (
	beardKernelOnce sync.Once
	beardKernel     [24 * 24 * 24]float32
)

func ensureBeardKernel() {
	beardKernelOnce.Do(func() {
		for zi := 0; zi < 24; zi++ {
			for xi := 0; xi < 24; xi++ {
				for yi := 0; yi < 24; yi++ {
					dx := float64(xi - 12)
					dy := float64(yi-12) + 0.5
					dz := float64(zi - 12)
					dSq := dx*dx + dy*dy + dz*dz
					beardKernel[zi*24*24+xi*24+yi] = float32(math.Pow(math.E, -dSq/16.0))
				}
			}
		}
	})
}

// MthFastInvSqrt ports net.minecraft.util.Mth.fastInvSqrt(double).
func MthFastInvSqrt(x float64) float64 {
	xhalf := 0.5 * x
	bits := math.Float64bits(x)
	bits = 0x5fe6eb50c7b537aa - (bits >> 1)
	y := math.Float64frombits(bits)
	y = y * (1.5 - xhalf*y*y)
	return y
}

// BeardContribution ports Beardifier.getBeardContribution(dx, dy, dz, dyFromGround).
func BeardContribution(dx, dy, dz, dyFromGround int) float64 {
	kx := dx + 12
	ky := dy + 12
	kz := dz + 12
	if kx < 0 || kx >= 24 || ky < 0 || ky >= 24 || kz < 0 || kz >= 24 {
		return 0.0
	}
	ensureBeardKernel()
	yOffset := float64(dyFromGround) + 0.5
	distSq := float64(dx*dx) + yOffset*yOffset + float64(dz*dz)
	factor := -yOffset * MthFastInvSqrt(distSq/2.0) / 2.0
	return factor * float64(beardKernel[kz*24*24+kx*24+ky])
}

// BuryContribution ports Beardifier.getBuryContribution(dx, dy, dz).
func BuryContribution(dx, dy, dz float64) float64 {
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist <= 0.0 {
		return 1.0
	}
	if dist >= 6.0 {
		return 0.0
	}
	return 1.0 - dist/6.0
}
