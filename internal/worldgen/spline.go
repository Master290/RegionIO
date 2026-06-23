package worldgen

// CubicSpline is Minecraft's cubic spline over a coordinate density function.
// Each point's value is itself a density function (often a constant or a nested
// spline), enabling multi-dimensional terrain shaping.
type CubicSpline struct {
	coordinate  DensityFunction
	locations   []float32
	values      []DensityFunction
	derivatives []float32
}

func (s *CubicSpline) Compute(c FunctionContext) float64 {
	t := float32(s.coordinate.Compute(c))
	n := len(s.locations)

	// Below the first point or above the last: linear extrapolation.
	if t < s.locations[0] {
		return float64(extend(s.values[0].Compute(c), s.derivatives[0], s.locations[0], t))
	}
	if t >= s.locations[n-1] {
		return float64(extend(s.values[n-1].Compute(c), s.derivatives[n-1], s.locations[n-1], t))
	}

	// Find the segment [i, i+1] containing t.
	i := 0
	for i < n-1 && s.locations[i+1] <= t {
		i++
	}
	loc0, loc1 := s.locations[i], s.locations[i+1]
	d0, d1 := s.derivatives[i], s.derivatives[i+1]
	p0 := float32(s.values[i].Compute(c))
	p1 := float32(s.values[i+1].Compute(c))

	k := (t - loc0) / (loc1 - loc0)
	l := d0*(loc1-loc0) - (p1 - p0)
	m := -d1*(loc1-loc0) + (p1 - p0)
	return float64(lerp32(k, p0, p1) + k*(1-k)*lerp32(k, l, m))
}

// extend linearly extrapolates a point's value beyond the spline's range.
func extend(value float64, derivative, loc, t float32) float32 {
	return float32(value) + derivative*(t-loc)
}

func lerp32(t, a, b float32) float32 { return a + t*(b-a) }
