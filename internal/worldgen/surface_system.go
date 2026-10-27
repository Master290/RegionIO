package worldgen

import "math"

// surface_system.go holds the per-column quantities SurfaceSystem computes
// before the rule tree runs: the surface depth (how thick the biome's surface
// layers are here) and the minimum surface level (how far down the biome
// subtree is allowed to reach at all).
//
// Both were stubbed out — surface depth at a constant 0, minimum surface level
// at the actual top block — and between them they collapsed every land column
// to a single block of grass sitting straight on stone.

// SurfaceSampler is the noise half of SurfaceSystem.
type SurfaceSampler struct {
	surfaceNoise       *NormalNoise
	secondaryNoise     *NormalNoise
	badlandsPillar     *NormalNoise
	badlandsPillarRoof *NormalNoise
	badlandsSurface    *NormalNoise
	icebergPillar      *NormalNoise
	icebergPillarRoof  *NormalNoise
	icebergSurface     *NormalNoise
	positionalRand     PositionalRandomFactory
}

// SurfaceDepth is SurfaceSystem.getSurfaceDepth: roughly three blocks, varied
// by the surface noise and jittered by a per-column draw. It can come out zero
// or negative, which is exactly what the "hole" condition looks for.
func (s *SurfaceSampler) SurfaceDepth(blockX, blockZ int) int {
	noiseValue := s.surfaceNoise.GetValue(float64(blockX), 0, float64(blockZ))
	jitter := nextDoubleAt(s.positionalRand, blockX, 0, blockZ) * 0.25
	return int(noiseValue*2.75 + 3.0 + jitter)
}

// SurfaceSecondary is SurfaceSystem.getSurfaceSecondary, the noise that widens
// a stone_depth band when the rule sets secondary_depth_range.
func (s *SurfaceSampler) SurfaceSecondary(blockX, blockZ int) float64 {
	return s.secondaryNoise.GetValue(float64(blockX), 0, float64(blockZ))
}

// Noise returns the primary surface noise value at a column, which the
// noise_threshold conditions on "minecraft:surface" range over.
func (s *SurfaceSampler) Noise(blockX, blockZ int) float64 {
	return s.surfaceNoise.GetValue(float64(blockX), 0, float64(blockZ))
}

// ErodedBadlandsExtension ports SurfaceSystem.erodedBadlandsExtension.
// In minecraft:eroded_badlands columns, before the surface rule tree runs, it
// raises stone pillars (hoodoos) from the existing stone surface up to j when
// the column is dry (no water between j and the first stone block below).
// It returns the updated top non-air index in col (or topY-1-minY when
// unchanged).
func (s *SurfaceSampler) ErodedBadlandsExtension(col []uint16, blockX, blockZ, topY, minY int) (newTop int, changed bool) {
	topIdx := topY - 1 - minY
	if s == nil || s.badlandsSurface == nil || s.badlandsPillar == nil || s.badlandsPillarRoof == nil {
		return topIdx, false
	}
	x, z := float64(blockX), float64(blockZ)
	e := math.Min(
		math.Abs(s.badlandsSurface.GetValue(x, 0, z)*8.25),
		s.badlandsPillar.GetValue(x*0.2, 0, z*0.2)*15.0,
	)
	if e <= 0.0 {
		return topIdx, false
	}
	h := math.Abs(s.badlandsPillarRoof.GetValue(x*0.75, 0, z*0.75) * 1.5)
	i := 64.0 + math.Min(e*e*2.5, math.Ceil(h*50.0)+24.0)
	j := int(math.Floor(i))
	if topY > j {
		return topIdx, false
	}
	maxY := minY + len(col) - 1
	if j > maxY {
		j = maxY
	}
	const (
		stateAir   uint16 = 0
		stateStone uint16 = 1
		stateWater uint16 = 86
	)
	for k := j; k >= minY; k-- {
		state := col[k-minY]
		if state == stateStone {
			break
		}
		if state == stateWater {
			return topIdx, false
		}
	}
	for k := j; k >= minY && col[k-minY] == stateAir; k-- {
		col[k-minY] = stateStone
		changed = true
	}
	if changed && j-minY > topIdx {
		return j - minY, true
	}
	return topIdx, changed
}

// FrozenOceanExtension ports SurfaceSystem.frozenOceanExtension.
// In minecraft:frozen_ocean and minecraft:deep_frozen_ocean columns, after the
// surface rule tree runs, it builds packed-ice and snow-block icebergs both
// above and below sea level.
func (s *SurfaceSampler) FrozenOceanExtension(col []uint16, minSurfaceLevel int, biome string, blockX, blockZ, topY, minY, seaLevel int) {
	if s == nil || s.icebergSurface == nil || s.icebergPillar == nil || s.icebergPillarRoof == nil {
		return
	}
	x, z := float64(blockX), float64(blockZ)
	e := math.Min(
		math.Abs(s.icebergSurface.GetValue(x, 0, z)*8.25),
		s.icebergPillar.GetValue(x*1.28, 0, z*1.28)*15.0,
	)
	if e <= 1.8 {
		return
	}
	h := math.Abs(s.icebergPillarRoof.GetValue(x*1.17, 0, z*1.17) * 1.5)
	i := math.Min(e*e*1.2, math.Ceil(h*40.0)+14.0)
	if ShouldMeltFrozenOceanIcebergSlightly(biome, blockX, blockZ, seaLevel) {
		i -= 2.0
	}
	var g float64
	if i > 2.0 {
		g = float64(seaLevel) - i - 7.0
		i = i + float64(seaLevel)
	} else {
		i = 0.0
		g = 0.0
	}
	j := i
	random := s.positionalRand.At(blockX, 0, blockZ)
	k := 2 + int(random.NextIntN(4))
	l := seaLevel + 18 + int(random.NextIntN(10))
	m := 0
	startY := max(topY, int(j)+1)
	maxY := minY + len(col) - 1
	if startY > maxY {
		startY = maxY
	}
	floorY := max(minSurfaceLevel, minY)
	const (
		stateAir       uint16 = 0
		stateWater     uint16 = 86
		stateSnowBlock uint16 = 6928
		statePackedIce uint16 = 12914
	)
	for n := startY; n >= floorY; n-- {
		state := col[n-minY]
		place := false
		if state == stateAir && n < int(j) && random.NextDouble() > 0.01 {
			place = true
		} else if state == stateWater && n > int(g) && n < seaLevel && g != 0.0 && random.NextDouble() > 0.15 {
			place = true
		}
		if place {
			if m <= k && n > l {
				col[n-minY] = stateSnowBlock
				m++
			} else {
				col[n-minY] = statePackedIce
			}
		}
	}
}

// MinSurfaceLevelAt is SurfaceRules.Context.getMinSurfaceLevel: the preliminary
// surface level sampled at the four corners of the 16-block cell containing the
// column, bilinearly interpolated, then offset by the surface depth less 8.
//
// Every biome-specific surface rule hangs under above_preliminary_surface,
// which tests blockY against this. Comparing against the column's actual top
// block instead — what we did before — let exactly one block per column through.
func (od *OverworldDensity) MinSurfaceLevelAt(blockX, blockZ, surfaceDepth int) int {
	cellX := blockX >> 4
	cellZ := blockZ >> 4
	c00 := float64(od.PreliminarySurfaceLevelAt(cellX<<4, cellZ<<4))
	c10 := float64(od.PreliminarySurfaceLevelAt((cellX+1)<<4, cellZ<<4))
	c01 := float64(od.PreliminarySurfaceLevelAt(cellX<<4, (cellZ+1)<<4))
	c11 := float64(od.PreliminarySurfaceLevelAt((cellX+1)<<4, (cellZ+1)<<4))
	// Vanilla forms the fractions in float before widening; both are exact here
	// because the divisor is a power of two.
	fx := float64(float32(blockX&15) / 16)
	fz := float64(float32(blockZ&15) / 16)
	return int(math.Floor(lerp2(fx, fz, c00, c10, c01, c11))) + surfaceDepth - 8
}

