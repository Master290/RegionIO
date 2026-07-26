package worldgen

import "math"

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
