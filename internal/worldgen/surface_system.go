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
	surfaceNoise   *NormalNoise
	secondaryNoise *NormalNoise
	positionalRand PositionalRandomFactory
}

// SurfaceDepth is SurfaceSystem.getSurfaceDepth: roughly three blocks, varied
// by the surface noise and jittered by a per-column draw. It can come out zero
// or negative, which is exactly what the "hole" condition looks for.
func (s *SurfaceSampler) SurfaceDepth(blockX, blockZ int) int {
	noiseValue := s.surfaceNoise.GetValue(float64(blockX), 0, float64(blockZ))
	jitter := s.positionalRand.At(blockX, 0, blockZ).NextDouble() * 0.25
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
