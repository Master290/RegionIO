package worldgen

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed data
var dataFS embed.FS

// Loader parses the embedded datapack density-function tree into evaluatable
// nodes, seeding noises through a RandomState. Shared sub-functions are cached
// by name so the DAG is built once.
type Loader struct {
	rs           *RandomState
	dfCache      map[string]DensityFunction
	interpolated []*Interpolated
}

// OverworldDensity is the parsed final_density plus the set of Interpolated
// nodes that the generator samples on the cell grid.
type OverworldDensity struct {
	Final        DensityFunction
	Interpolated []*Interpolated
	// Climate parameters sampled by the biome finder. Read from the same
	// noise_router as final_density. The router keys map to climate axes:
	// temperature→Temperature, vegetation→Humidity, continents→Continentalness,
	// erosion→Erosion, ridges→Weirdness, depth→Depth.
	Temperature, Humidity, Continentalness, Erosion, Weirdness, Depth DensityFunction
	// Aquifer inputs (NoiseRouter.barrierNoise and friends). Barrier is the
	// pressure noise that seals an aquifer off from the surrounding stone;
	// FluidLevelFloodedness and FluidLevelSpread decide whether a cell holds
	// fluid and at what level; Lava turns deep aquifers into lava.
	Barrier, FluidLevelFloodedness, FluidLevelSpread, Lava DensityFunction
	// Ore-vein inputs (unused until the OreVeinifier lands, but parsed here so
	// the whole router is wired in one place).
	VeinToggle, VeinRidged, VeinGap DensityFunction
	// PreliminarySurfaceLevel is the cheap surface estimate used by the aquifer
	// and by the above_preliminary_surface surface-rule condition. Read it
	// through PreliminarySurfaceLevelAt, which quart-aligns and memoises.
	PreliminarySurfaceLevel DensityFunction

	// Settings read from the same noise settings file.
	SeaLevel        int
	MinY            int
	Height          int
	AquifersEnabled bool
	OreVeinsEnabled bool

	// AquiferRandom places the aquifer cell centres.
	AquiferRandom PositionalRandomFactory

	prelim *levelCache
}

// SurfaceRule returns the overworld surface rule tree, loading it on first use.
// It does not depend on the seed. A nil rule (on error) is non-fatal: the
// generator falls back to its default surface heuristics.
func (od *OverworldDensity) SurfaceRule() (SurfaceRule, error) {
	return LoadOverworldSurfaceRule()
}

// LoadOverworldFinalDensity builds the overworld final_density function for the
// given world seed.
func LoadOverworldFinalDensity(seed int64) (*OverworldDensity, error) {
	l := &Loader{rs: NewRandomState(seed), dfCache: make(map[string]DensityFunction)}
	var settings struct {
		NoiseRouter map[string]json.RawMessage `json:"noise_router"`
		SeaLevel    int                        `json:"sea_level"`
		Noise       struct {
			MinY   int `json:"min_y"`
			Height int `json:"height"`
		} `json:"noise"`
		AquifersEnabled bool `json:"aquifers_enabled"`
		OreVeinsEnabled bool `json:"ore_veins_enabled"`
	}
	if err := l.readJSON("data/overworld.json", &settings); err != nil {
		return nil, err
	}
	var node any
	if err := json.Unmarshal(settings.NoiseRouter["final_density"], &node); err != nil {
		return nil, err
	}
	final, err := l.parseNode(node)
	if err != nil {
		return nil, err
	}
	od := &OverworldDensity{
		Final:           final,
		SeaLevel:        settings.SeaLevel,
		MinY:            settings.Noise.MinY,
		Height:          settings.Noise.Height,
		AquifersEnabled: settings.AquifersEnabled,
		OreVeinsEnabled: settings.OreVeinsEnabled,
		AquiferRandom:   l.rs.AquiferRandom(),
		prelim:          newLevelCache(),
	}

	// Parse the remaining router keys. Each resolves to a density function via
	// the same parseNode/loadRef machinery as final_density. A missing key is
	// not fatal — the field stays nil and its consumer treats it as absent —
	// but a parse error is.
	//
	// The climate keys feed the biome finder (temperature→Temperature,
	// vegetation→Humidity, continents→Continentalness, erosion→Erosion,
	// ridges→Weirdness, depth→Depth); the rest feed the aquifer, the ore veins
	// and the preliminary surface estimate.
	//
	// The order is fixed rather than a map range: parsing assigns Interpolated
	// node indices in encounter order, and those indices address the cell-corner
	// grids the generator fills.
	routerKeys := []struct {
		key string
		dst *DensityFunction
	}{
		{"temperature", &od.Temperature},
		{"vegetation", &od.Humidity},
		{"continents", &od.Continentalness},
		{"erosion", &od.Erosion},
		{"ridges", &od.Weirdness},
		{"depth", &od.Depth},
		{"barrier", &od.Barrier},
		{"fluid_level_floodedness", &od.FluidLevelFloodedness},
		{"fluid_level_spread", &od.FluidLevelSpread},
		{"lava", &od.Lava},
		{"vein_toggle", &od.VeinToggle},
		{"vein_ridged", &od.VeinRidged},
		{"vein_gap", &od.VeinGap},
		{"preliminary_surface_level", &od.PreliminarySurfaceLevel},
	}
	for _, rk := range routerKeys {
		raw, ok := settings.NoiseRouter[rk.key]
		if !ok {
			continue
		}
		var cn any
		if err := json.Unmarshal(raw, &cn); err != nil {
			return nil, fmt.Errorf("parse router key %q: %w", rk.key, err)
		}
		df, err := l.parseNode(cn)
		if err != nil {
			return nil, fmt.Errorf("router key %q: %w", rk.key, err)
		}
		*rk.dst = df
	}
	// Interpolated nodes are collected as the whole router is parsed, so the
	// list has to be taken after the loop, not just after final_density.
	od.Interpolated = l.interpolated
	return od, nil
}

func (l *Loader) readJSON(path string, v any) error {
	b, err := dataFS.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return json.Unmarshal(b, v)
}

// parseNode builds a density function from a decoded JSON value: a number is a
// constant, a string is a reference to another density-function file, and an
// object is a typed node.
func (l *Loader) parseNode(v any) (DensityFunction, error) {
	switch t := v.(type) {
	case float64:
		return Constant(t), nil
	case string:
		return l.loadRef(t)
	case map[string]any:
		return l.parseObject(t)
	default:
		return nil, fmt.Errorf("unexpected density-function node %T", v)
	}
}

// loadRef loads and caches a density function referenced by resource location.
func (l *Loader) loadRef(name string) (DensityFunction, error) {
	if df, ok := l.dfCache[name]; ok {
		return df, nil
	}
	path := "data/density_function/" + strings.TrimPrefix(name, "minecraft:") + ".json"
	var node any
	if err := l.readJSON(path, &node); err != nil {
		return nil, err
	}
	df, err := l.parseNode(node)
	if err != nil {
		return nil, fmt.Errorf("in %s: %w", name, err)
	}
	l.dfCache[name] = df
	return df, nil
}

func (l *Loader) parseObject(m map[string]any) (DensityFunction, error) {
	typ, _ := m["type"].(string)
	arg := func(k string) (DensityFunction, error) { return l.parseNode(m[k]) }
	num := func(k string) float64 { f, _ := m[k].(float64); return f }

	switch strings.TrimPrefix(typ, "minecraft:") {
	case "add", "mul", "min", "max":
		a, err := arg("argument1")
		if err != nil {
			return nil, err
		}
		b, err := arg("argument2")
		if err != nil {
			return nil, err
		}
		switch typ[10:] {
		case "add":
			return Add(a, b), nil
		case "mul":
			return Mul(a, b), nil
		case "min":
			return Min(a, b), nil
		default:
			return Max(a, b), nil
		}
	case "abs", "square", "cube", "half_negative", "quarter_negative", "invert", "squeeze":
		a, err := arg("argument")
		if err != nil {
			return nil, err
		}
		return unaryByName(strings.TrimPrefix(typ, "minecraft:"), a), nil
	case "clamp":
		a, err := arg("input")
		if err != nil {
			return nil, err
		}
		return Clamp(a, num("min"), num("max")), nil
	case "range_choice":
		in, err := arg("input")
		if err != nil {
			return nil, err
		}
		whenIn, err := arg("when_in_range")
		if err != nil {
			return nil, err
		}
		whenOut, err := arg("when_out_of_range")
		if err != nil {
			return nil, err
		}
		return RangeChoice{in, num("min_inclusive"), num("max_exclusive"), whenIn, whenOut}, nil
	case "y_clamped_gradient":
		return YClampedGradient{num("from_y"), num("to_y"), num("from_value"), num("to_value")}, nil
	case "noise":
		n, err := l.noiseField(m["noise"])
		if err != nil {
			return nil, err
		}
		return NoiseDF{Noise: n, XZScale: num("xz_scale"), YScale: num("y_scale")}, nil
	case "shifted_noise":
		sx, err := arg("shift_x")
		if err != nil {
			return nil, err
		}
		sy, err := arg("shift_y")
		if err != nil {
			return nil, err
		}
		sz, err := arg("shift_z")
		if err != nil {
			return nil, err
		}
		n, err := l.noiseField(m["noise"])
		if err != nil {
			return nil, err
		}
		return ShiftedNoise{sx, sy, sz, num("xz_scale"), num("y_scale"), n}, nil
	case "shift_a", "shift_b":
		n, err := l.noiseField(m["argument"])
		if err != nil {
			return nil, err
		}
		if typ[10:] == "shift_a" {
			return ShiftA{n}, nil
		}
		return ShiftB{n}, nil
	case "old_blended_noise":
		return l.rs.BlendedNoise(num("xz_scale"), num("y_scale"), num("xz_factor"), num("y_factor"), num("smear_scale_multiplier")), nil
	case "weird_scaled_sampler":
		in, err := arg("input")
		if err != nil {
			return nil, err
		}
		n, err := l.noiseField(m["noise"])
		if err != nil {
			return nil, err
		}
		rarity := SpaghettiRarity3D
		if s, _ := m["rarity_value_mapper"].(string); s == "type_2" {
			rarity = SpaghettiRarity2D
		}
		return WeirdScaledSampler{in, n, rarity}, nil
	case "find_top_surface":
		density, err := arg("density")
		if err != nil {
			return nil, err
		}
		upper, err := arg("upper_bound")
		if err != nil {
			return nil, err
		}
		cellHeight := int(num("cell_height"))
		if cellHeight <= 0 {
			return nil, fmt.Errorf("find_top_surface: cell_height must be positive, got %d", cellHeight)
		}
		return FindTopSurface{
			Density:    density,
			UpperBound: upper,
			LowerBound: int(num("lower_bound")),
			CellHeight: cellHeight,
		}, nil
	case "spline":
		return l.parseSpline(m["spline"])
	case "blend_alpha":
		return Constant(1.0), nil // no blending: alpha = 1
	case "blend_offset":
		return Constant(0.0), nil // no blending: offset = 0
	case "interpolated":
		inner, err := arg("argument")
		if err != nil {
			return nil, err
		}
		n := &Interpolated{Inner: inner, Index: len(l.interpolated)}
		l.interpolated = append(l.interpolated, n)
		return n, nil
	case "blend_density", "flat_cache", "cache_2d", "cache_once", "cache_all_in_cell":
		// 2D caches and blend wrappers are value-preserving for per-point
		// evaluation (recomputed rather than cached); only the 3D interpolated
		// marker changes the result and is handled above.
		return arg("argument")
	default:
		return nil, fmt.Errorf("unsupported density-function type %q", typ)
	}
}

func unaryByName(name string, a DensityFunction) DensityFunction {
	switch name {
	case "abs":
		return Abs(a)
	case "square":
		return Square(a)
	case "cube":
		return Cube(a)
	case "half_negative":
		return HalfNegative(a)
	case "quarter_negative":
		return QuarterNegative(a)
	case "invert":
		return Invert(a)
	default: // squeeze
		return Squeeze(a)
	}
}

// noiseField resolves a noise reference (a "minecraft:<name>" key, or an object
// with a "noise" key) to a seeded NormalNoise.
func (l *Loader) noiseField(v any) (*NormalNoise, error) {
	var key string
	switch t := v.(type) {
	case string:
		key = t
	case map[string]any:
		key, _ = t["noise"].(string)
	}
	if key == "" {
		return nil, fmt.Errorf("missing noise reference")
	}
	var params struct {
		FirstOctave int       `json:"firstOctave"`
		Amplitudes  []float64 `json:"amplitudes"`
	}
	path := "data/noise/" + strings.TrimPrefix(key, "minecraft:") + ".json"
	if err := l.readJSON(path, &params); err != nil {
		return nil, err
	}
	return l.rs.Noise(key, params.FirstOctave, params.Amplitudes), nil
}

func (l *Loader) parseSpline(v any) (DensityFunction, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("spline is not an object")
	}
	coord, err := l.parseNode(m["coordinate"])
	if err != nil {
		return nil, err
	}
	pts, _ := m["points"].([]any)
	s := &CubicSpline{coordinate: coord}
	for _, p := range pts {
		pm := p.(map[string]any)
		loc, _ := pm["location"].(float64)
		der, _ := pm["derivative"].(float64)
		val, err := l.parseSplineValue(pm["value"])
		if err != nil {
			return nil, err
		}
		s.locations = append(s.locations, float32(loc))
		s.derivatives = append(s.derivatives, float32(der))
		s.values = append(s.values, val)
	}
	return s, nil
}

// parseSplineValue handles a spline point's value: a number (constant), a raw
// nested spline (object with "coordinate"), or a density-function node.
func (l *Loader) parseSplineValue(v any) (DensityFunction, error) {
	if m, ok := v.(map[string]any); ok {
		if _, hasCoord := m["coordinate"]; hasCoord {
			return l.parseSpline(m)
		}
	}
	return l.parseNode(v)
}
