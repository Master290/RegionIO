package worldgen

import (
	"math"
	"sync"
)

// FunctionContext is the sample point for a density function (block coords).
// During chunk generation, interp holds the precomputed cell-interpolated value
// for each Interpolated node (indexed by node); it is nil for plain evaluation.
type FunctionContext struct {
	X, Y, Z float64
	interp  []float64
}

// WithInterp returns a copy of c carrying the given per-node interpolated values.
func (c FunctionContext) WithInterp(v []float64) FunctionContext {
	c.interp = v
	return c
}

// Interpolated marks a sub-function that vanilla samples on the cell-corner grid
// and trilinearly interpolates (the heavy 3D terrain noise). During generation
// the value is looked up by Index; otherwise the inner function is evaluated.
type Interpolated struct {
	Inner DensityFunction
	Index int
}

func (n *Interpolated) Compute(c FunctionContext) float64 {
	if c.interp != nil {
		return c.interp[n.Index]
	}
	return n.Inner.Compute(c)
}

// DensityFunction is a node in the density-function tree. Compute returns the
// density at the given point; positive conventionally means "solid".
//
// This is the interpreter engine; only the node types we currently need are
// implemented. The full vanilla set (splines, blend_density, caches, etc.) can
// be added incrementally without changing this interface.
type DensityFunction interface {
	Compute(c FunctionContext) float64
}

// Constant is a fixed value.
type Constant float64

func (c Constant) Compute(FunctionContext) float64 { return float64(c) }

type binaryOp struct {
	a, b DensityFunction
	op   func(x, y float64) float64
}

func (n binaryOp) Compute(c FunctionContext) float64 { return n.op(n.a.Compute(c), n.b.Compute(c)) }

// Add, Mul, Min, Max combine two density functions pointwise.
func Add(a, b DensityFunction) DensityFunction {
	return binaryOp{a, b, func(x, y float64) float64 { return x + y }}
}
func Mul(a, b DensityFunction) DensityFunction {
	return binaryOp{a, b, func(x, y float64) float64 { return x * y }}
}
func Min(a, b DensityFunction) DensityFunction {
	return binaryOp{a, b, func(x, y float64) float64 {
		if x < y {
			return x
		}
		return y
	}}
}
func Max(a, b DensityFunction) DensityFunction {
	return binaryOp{a, b, func(x, y float64) float64 {
		if x > y {
			return x
		}
		return y
	}}
}

// YClampedGradient is the y_clamped_gradient node: a linear map of Y from
// [fromY, toY] onto [fromV, toV], clamped outside that range.
type YClampedGradient struct {
	FromY, ToY, FromV, ToV float64
}

func (g YClampedGradient) Compute(c FunctionContext) float64 {
	return clampedMap(c.Y, g.FromY, g.ToY, g.FromV, g.ToV)
}

// NoiseDF samples a NormalNoise, scaling the input coordinates (the "noise" /
// "shifted_noise" family, without the shift inputs).
type NoiseDF struct {
	Noise            *NormalNoise
	XZScale, YScale  float64
}

func (n NoiseDF) Compute(c FunctionContext) float64 {
	return n.Noise.GetValue(c.X*n.XZScale, c.Y*n.YScale, c.Z*n.XZScale)
}

type unaryOp struct {
	a  DensityFunction
	op func(float64) float64
}

func (n unaryOp) Compute(c FunctionContext) float64 { return n.op(n.a.Compute(c)) }

// Abs, Square, Cube, HalfNegative, QuarterNegative, Squeeze are the unary
// transforms used by the vanilla density tree.
func Abs(a DensityFunction) DensityFunction    { return unaryOp{a, math.Abs} }
func Square(a DensityFunction) DensityFunction { return unaryOp{a, func(x float64) float64 { return x * x }} }
func Cube(a DensityFunction) DensityFunction   { return unaryOp{a, func(x float64) float64 { return x * x * x }} }
func HalfNegative(a DensityFunction) DensityFunction {
	return unaryOp{a, func(x float64) float64 {
		if x > 0 {
			return x
		}
		return x * 0.5
	}}
}
func QuarterNegative(a DensityFunction) DensityFunction {
	return unaryOp{a, func(x float64) float64 {
		if x > 0 {
			return x
		}
		return x * 0.25
	}}
}
// Invert is the reciprocal transform (DensityFunctions.Mapped.Type.INVERT):
// 1/x, not negation. The overworld's preliminary_surface_level upper bound is
// the only place it appears.
func Invert(a DensityFunction) DensityFunction {
	return unaryOp{a, func(x float64) float64 { return 1.0 / x }}
}
func Squeeze(a DensityFunction) DensityFunction {
	return unaryOp{a, func(x float64) float64 {
		d := clamp(x, -1, 1)
		return d/2.0 - d*d*d/24.0
	}}
}

// Clamp constrains a density function to [min, max].
func Clamp(a DensityFunction, min, max float64) DensityFunction {
	return unaryOp{a, func(x float64) float64 { return clamp(x, min, max) }}
}

// RangeChoice picks whenInRange if input is within [min, max), else whenOut.
type RangeChoice struct {
	Input            DensityFunction
	Min, Max         float64
	WhenInRange      DensityFunction
	WhenOutOfRange   DensityFunction
}

func (r RangeChoice) Compute(c FunctionContext) float64 {
	d := r.Input.Compute(c)
	if d >= r.Min && d < r.Max {
		return r.WhenInRange.Compute(c)
	}
	return r.WhenOutOfRange.Compute(c)
}

// ShiftedNoise samples a NormalNoise at coordinates scaled and offset by shift
// density functions (the workhorse of climate/terrain inputs).
type ShiftedNoise struct {
	ShiftX, ShiftY, ShiftZ DensityFunction
	XZScale, YScale        float64
	Noise                  *NormalNoise
}

func (s ShiftedNoise) Compute(c FunctionContext) float64 {
	x := c.X*s.XZScale + s.ShiftX.Compute(c)
	y := c.Y*s.YScale + s.ShiftY.Compute(c)
	z := c.Z*s.XZScale + s.ShiftZ.Compute(c)
	return s.Noise.GetValue(x, y, z)
}

// shiftNoise samples the offset noise at quarter scale, times four.
func shiftNoise(noise *NormalNoise, x, y, z float64) float64 {
	return noise.GetValue(x*0.25, y*0.25, z*0.25) * 4.0
}

// ShiftA shifts along X/Z (used by shift_x): noise(x, 0, z).
type ShiftA struct{ Noise *NormalNoise }

func (s ShiftA) Compute(c FunctionContext) float64 { return shiftNoise(s.Noise, c.X, 0, c.Z) }

// ShiftB shifts with swapped axes (used by shift_z): noise(z, x, 0).
type ShiftB struct{ Noise *NormalNoise }

func (s ShiftB) Compute(c FunctionContext) float64 { return shiftNoise(s.Noise, c.Z, c.X, 0) }

// WeirdScaledSampler scales a noise sample by a rarity derived from an input
// density function (used by the spaghetti caves).
type WeirdScaledSampler struct {
	Input  DensityFunction
	Noise  *NormalNoise
	Rarity func(float64) float64
}

func (w WeirdScaledSampler) Compute(c FunctionContext) float64 {
	rarity := w.Rarity(w.Input.Compute(c))
	return rarity * math.Abs(w.Noise.GetValue(c.X/rarity, c.Y/rarity, c.Z/rarity))
}

// SpaghettiRarity2D is the type_2 rarity mapping.
func SpaghettiRarity2D(v float64) float64 {
	switch {
	case v < -0.75:
		return 0.5
	case v < -0.5:
		return 0.75
	case v < 0.5:
		return 1.0
	case v < 0.75:
		return 2.0
	default:
		return 3.0
	}
}

// SpaghettiRarity3D is the type_1 rarity mapping.
func SpaghettiRarity3D(v float64) float64 {
	switch {
	case v < -0.5:
		return 0.75
	case v < 0.0:
		return 1.0
	case v < 0.5:
		return 1.5
	default:
		return 2.0
	}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// clampedMap linearly maps v from [inMin,inMax] to [outMin,outMax], clamped.
func clampedMap(v, inMin, inMax, outMin, outMax float64) float64 {
	if v <= inMin {
		return outMin
	}
	if v >= inMax {
		return outMax
	}
	t := (v - inMin) / (inMax - inMin)
	return outMin + t*(outMax-outMin)
}

// SimplexNoise ports net.minecraft.world.level.levelgen.synth.SimplexNoise.
type SimplexNoise struct {
	p          [512]int
	Xo, Yo, Zo float64
}

var (
	simplexSqrt3     = math.Sqrt(3.0)
	simplexF2        = 0.5 * (simplexSqrt3 - 1.0)
	simplexG2        = (3.0 - simplexSqrt3) / 6.0
	simplexGradients = [16][3]float64{
		{1, 1, 0},
		{-1, 1, 0},
		{1, -1, 0},
		{-1, -1, 0},
		{1, 0, 1},
		{-1, 0, 1},
		{1, 0, -1},
		{-1, 0, -1},
		{0, 1, 1},
		{0, -1, 1},
		{0, 1, -1},
		{0, -1, -1},
		{1, 1, 0},
		{0, -1, 1},
		{-1, 1, 0},
		{0, -1, -1},
	}
)

// NewSimplexNoise constructs a SimplexNoise from a RandomSource, matching
// SimplexNoise(RandomSource).
func NewSimplexNoise(r RandomSource) *SimplexNoise {
	sn := &SimplexNoise{
		Xo: r.NextDouble() * 256.0,
		Yo: r.NextDouble() * 256.0,
		Zo: r.NextDouble() * 256.0,
	}
	for i := 0; i < 256; i++ {
		sn.p[i] = i
	}
	for i := 0; i < 256; i++ {
		j := int(r.NextIntN(int32(256 - i)))
		k := sn.p[i]
		sn.p[i] = sn.p[i+j]
		sn.p[i+j] = k
	}
	return sn
}

func (sn *SimplexNoise) pVal(i int) int {
	return sn.p[i&255]
}

func (sn *SimplexNoise) cornerNoise3D(gi int, x, y, z, falloff float64) float64 {
	t := falloff - x*x - y*y - z*z
	if t < 0.0 {
		return 0.0
	}
	t *= t
	g := simplexGradients[gi]
	dot := g[0]*x + g[1]*y + g[2]*z
	return t * t * dot
}

// GetValue2D mirrors SimplexNoise.getValue(double, double).
func (sn *SimplexNoise) GetValue2D(x, y float64) float64 {
	s := (x + y) * simplexF2
	i := int(math.Floor(x + s))
	j := int(math.Floor(y + s))
	t := float64(i+j) * simplexG2
	X0 := float64(i) - t
	Y0 := float64(j) - t
	x0 := x - X0
	y0 := y - Y0
	var i1, j1 int
	if x0 > y0 {
		i1, j1 = 1, 0
	} else {
		i1, j1 = 0, 1
	}
	x1 := x0 - float64(i1) + simplexG2
	y1 := y0 - float64(j1) + simplexG2
	x2 := x0 - 1.0 + 2.0*simplexG2
	y2 := y0 - 1.0 + 2.0*simplexG2
	ii := i & 255
	jj := j & 255
	gi0 := sn.pVal(ii+sn.pVal(jj)) % 12
	gi1 := sn.pVal(ii+i1+sn.pVal(jj+j1)) % 12
	gi2 := sn.pVal(ii+1+sn.pVal(jj+1)) % 12
	n0 := sn.cornerNoise3D(gi0, x0, y0, 0.0, 0.5)
	n1 := sn.cornerNoise3D(gi1, x1, y1, 0.0, 0.5)
	n2 := sn.cornerNoise3D(gi2, x2, y2, 0.0, 0.5)
	return 70.0 * (n0 + n1 + n2)
}

type endIslandCacheEntry struct {
	valid  bool
	ix, iz int32
	val    float32
}

// EndIslandDF ports DensityFunctions$EndIslandDensityFunction.
type EndIslandDF struct {
	islandNoise *SimplexNoise
	mu          sync.Mutex
	cache       [1024]endIslandCacheEntry
}

// NewEndIslandDF constructs the EndIslandDensityFunction for the given world seed.
func NewEndIslandDF(seed int64) *EndIslandDF {
	r := NewLegacy(seed)
	r.ConsumeCount(17292)
	return &EndIslandDF{islandNoise: NewSimplexNoise(r)}
}

func endIslandHeightValue(noise *SimplexNoise, x, z int) float32 {
	sectionX := x / 2
	sectionZ := z / 2
	subSectionX := x % 2
	subSectionZ := z % 2
	f := float32(100.0) - float32(math.Sqrt(float64(float32(int32(x)*int32(x)+int32(z)*int32(z)))))*float32(8.0)
	f = clamp32(f, -100.0, 80.0)
	for ox := -12; ox <= 12; ox++ {
		for oz := -12; oz <= 12; oz++ {
			cx := int64(sectionX + ox)
			cz := int64(sectionZ + oz)
			if cx*cx+cz*cz > 4096 && noise.GetValue2D(float64(cx), float64(cz)) < float64(float32(-0.9)) {
				absX := float32(cx)
				if absX < 0 {
					absX = -absX
				}
				absZ := float32(cz)
				if absZ < 0 {
					absZ = -absZ
				}
				g := float32(math.Mod(float64(absX*float32(3439.0)+absZ*float32(147.0)), 13.0)) + float32(9.0)
				dx := float32(subSectionX - ox*2)
				dz := float32(subSectionZ - oz*2)
				nf := float32(100.0) - float32(math.Sqrt(float64(dx*dx+dz*dz)))*g
				nf = clamp32(nf, -100.0, 80.0)
				if nf > f {
					f = nf
				}
			}
		}
	}
	return f
}

func clamp32(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (e *EndIslandDF) Compute(c FunctionContext) float64 {
	ix := int(math.Floor(c.X)) / 8
	iz := int(math.Floor(c.Z)) / 8
	slot := uint32(int32(ix)*73428767^int32(iz)*912931) & 1023
	e.mu.Lock()
	ent := e.cache[slot]
	if ent.valid && ent.ix == int32(ix) && ent.iz == int32(iz) {
		e.mu.Unlock()
		return (float64(ent.val) - 8.0) / 128.0
	}
	e.mu.Unlock()
	h := endIslandHeightValue(e.islandNoise, ix, iz)
	e.mu.Lock()
	e.cache[slot] = endIslandCacheEntry{valid: true, ix: int32(ix), iz: int32(iz), val: h}
	e.mu.Unlock()
	return (float64(h) - 8.0) / 128.0
}


