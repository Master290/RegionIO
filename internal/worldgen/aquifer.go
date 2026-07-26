package worldgen

import (
	"math"
	"sync"
)

// aquifer.go ports net.minecraft.world.level.levelgen.Aquifer.NoiseBasedAquifer.
//
// The aquifer is what decides, for every position the density function leaves
// empty, whether it becomes air, water or lava. Without it a generator has to
// guess — the usual guess being "water everywhere below sea level", which
// drowns every cave under y=63 and leaves no lava lakes anywhere.
//
// Vanilla scatters aquifer centres on a 16×12×16 grid, jittered by a positional
// RNG. Each centre gets a FluidStatus: a fluid level and a fluid type. A
// position takes the fluid of its nearest centre, unless the barrier noise
// raises enough "pressure" between the two or three nearest centres to seal the
// position off as stone instead. Centres near the open sky inherit the global
// sea level, so oceans and lakes still fill normally; centres buried deep get a
// randomised, usually much lower level, which is why caves are dry.

// Block-state network IDs the aquifer places. The worldgen package deliberately
// does not import the world package; these match blockids.go.
const (
	blockAir   uint16 = 0
	blockWater uint16 = 86
	blockLava  uint16 = 102
)

// Aquifer grid geometry (Aquifer.NoiseBasedAquifer constants).
const (
	aquiferXSpacing = 16
	aquiferYSpacing = 12
	aquiferZSpacing = 16
	aquiferXRange   = 10
	aquiferYRange   = 9
	aquiferZRange   = 10

	// WayBelowMinY is DimensionType.WAY_BELOW_MIN_Y (MIN_Y << 4, MIN_Y=-2032):
	// the "this aquifer holds nothing" sentinel fluid level.
	WayBelowMinY = -32512
)

// deepDark is OverworldBiomeBuilder.isDeepDarkRegion's thresholds, kept at the
// exact double values the float constants widen to.
const (
	deepDarkErosionMax = -0.22499999403953552
	deepDarkDepthMin   = 0.8999999761581421
)

// FluidStatus is a fluid level plus the fluid filling up to it (Aquifer.FluidStatus).
type FluidStatus struct {
	Level int
	Type  uint16
}

// At returns the fluid at blockY, or air above the level.
func (f FluidStatus) At(blockY int) uint16 {
	if blockY < f.Level {
		return f.Type
	}
	return blockAir
}

// FluidPicker is the dimension-wide fluid rule (Aquifer.FluidPicker): what a
// position would hold if there were no aquifer at all.
type FluidPicker func(x, y, z int) FluidStatus

// OverworldFluidPicker is NoiseBasedChunkGenerator.createFluidPicker: lava
// below y=-54, sea water above it.
func OverworldFluidPicker(seaLevel int) FluidPicker {
	lava := FluidStatus{Level: -54, Type: blockLava}
	sea := FluidStatus{Level: seaLevel, Type: blockWater}
	lavaBelow := min(-54, seaLevel)
	return func(_, y, _ int) FluidStatus {
		if y < lavaBelow {
			return lava
		}
		return sea
	}
}

// surfaceSamplingOffsets is SURFACE_SAMPLING_OFFSETS_IN_CHUNKS: the thirteen
// chunk offsets an aquifer centre probes to work out whether it is under open
// sky or buried. The set is lopsided towards -X on purpose — it is vanilla's.
var surfaceSamplingOffsets = [13][2]int{
	{0, 0}, {-2, -1}, {-1, -1}, {0, -1}, {1, -1}, {-3, 0}, {-2, 0},
	{-1, 0}, {1, 0}, {-2, 1}, {-1, 1}, {0, 1}, {1, 1},
}

// Aquifer resolves fluid for one chunk. Its cell grid is computed up front so
// the chunk's columns can be filled in parallel without locking.
type Aquifer struct {
	od     *OverworldDensity
	global FluidPicker

	minGridX, minGridY, minGridZ    int
	gridSizeX, gridSizeY, gridSizeZ int

	locations []aquiferPos
	status    []FluidStatus

	// skipSamplingAboveY is the height above which the grid is irrelevant and
	// the global fluid rule answers directly.
	skipSamplingAboveY int
}

type aquiferPos struct{ x, y, z int }

// NewAquifer builds the aquifer covering the given chunk.
//
// Vanilla fills the cell grid lazily as columns are generated; we fill it
// eagerly because our columns are generated concurrently. That is not a
// fidelity change: every cell's centre and status is a pure function of its
// grid coordinate, and every cell in the range computed here is consulted by
// some position in the chunk anyway.
func NewAquifer(od *OverworldDensity, chunkX, chunkZ int, picker FluidPicker) *Aquifer {
	minBlockX, minBlockZ := chunkX*16, chunkZ*16
	maxBlockX, maxBlockZ := minBlockX+15, minBlockZ+15

	a := &Aquifer{od: od, global: picker}
	a.minGridX = aquiferGridX(minBlockX - 5)
	maxGridX := aquiferGridX(maxBlockX-5) + 1
	a.gridSizeX = maxGridX - a.minGridX + 1
	a.minGridY = aquiferGridY(od.MinY+1) - 1
	maxGridY := aquiferGridY(od.MinY+od.Height+1) + 1
	a.gridSizeY = maxGridY - a.minGridY + 1
	a.minGridZ = aquiferGridZ(minBlockZ - 5)
	maxGridZ := aquiferGridZ(maxBlockZ-5) + 1
	a.gridSizeZ = maxGridZ - a.minGridZ + 1

	n := a.gridSizeX * a.gridSizeY * a.gridSizeZ
	a.locations = make([]aquiferPos, n)
	a.status = make([]FluidStatus, n)

	maxAdjusted := adjustSurfaceLevel(od.MaxPreliminarySurfaceLevel(
		fromAquiferGridX(a.minGridX, 0), fromAquiferGridZ(a.minGridZ, 0),
		fromAquiferGridX(maxGridX, 9), fromAquiferGridZ(maxGridZ, 9)))
	a.skipSamplingAboveY = fromAquiferGridY(aquiferGridY(maxAdjusted+12)+1, 11) - 1

	// Cells above the highest consulted anchor are never read: computeSubstance
	// returns the global fluid before touching the grid once y climbs past
	// skipSamplingAboveY, and the anchor search reaches at most one cell higher.
	topUsedGridY := min(aquiferGridY(a.skipSamplingAboveY+1)+1, maxGridY)

	var wg sync.WaitGroup
	for gy := a.minGridY; gy <= topUsedGridY; gy++ {
		wg.Add(1)
		go func(gy int) {
			defer wg.Done()
			for gz := a.minGridZ; gz < a.minGridZ+a.gridSizeZ; gz++ {
				for gx := a.minGridX; gx < a.minGridX+a.gridSizeX; gx++ {
					i := a.index(gx, gy, gz)
					r := od.AquiferRandom.At(gx, gy, gz)
					pos := aquiferPos{
						x: fromAquiferGridX(gx, int(r.NextIntN(aquiferXRange))),
						y: fromAquiferGridY(gy, int(r.NextIntN(aquiferYRange))),
						z: fromAquiferGridZ(gz, int(r.NextIntN(aquiferZRange))),
					}
					a.locations[i] = pos
					a.status[i] = a.computeFluid(pos.x, pos.y, pos.z)
				}
			}
		}(gy)
	}
	wg.Wait()
	return a
}

func (a *Aquifer) index(gridX, gridY, gridZ int) int {
	x := gridX - a.minGridX
	y := gridY - a.minGridY
	z := gridZ - a.minGridZ
	return (y*a.gridSizeZ+z)*a.gridSizeX + x
}

// ComputeSubstance decides what fills (x,y,z) given the final density there.
// ok=false means the position stays the settings' default block (stone);
// otherwise the returned state is the fluid — which may be air.
//
// Vanilla additionally tracks shouldScheduleFluidUpdate here, to mark positions
// where two neighbouring aquifers disagree so the fluid flows on first tick. We
// have no fluid ticking yet and the flag never affects the block placed, so it
// is left out; the fourth-nearest centre, which only feeds that flag, is not
// tracked either.
func (a *Aquifer) ComputeSubstance(x, y, z int, density float64) (uint16, bool) {
	if density > 0 {
		return 0, false
	}
	global := a.global(x, y, z)
	if y > a.skipSamplingAboveY {
		return global.At(y), true
	}
	if global.At(y) == blockLava {
		return blockLava, true
	}

	xAnchor := aquiferGridX(x - 5)
	yAnchor := aquiferGridY(y + 1)
	zAnchor := aquiferGridZ(z - 5)
	dist1, dist2, dist3 := math.MaxInt32, math.MaxInt32, math.MaxInt32
	idx1, idx2, idx3 := 0, 0, 0
	for dx := 0; dx <= 1; dx++ {
		for dy := -1; dy <= 1; dy++ {
			for dz := 0; dz <= 1; dz++ {
				i := a.index(xAnchor+dx, yAnchor+dy, zAnchor+dz)
				p := a.locations[i]
				ox, oy, oz := p.x-x, p.y-y, p.z-z
				d := ox*ox + oy*oy + oz*oz
				switch {
				case dist1 >= d:
					idx3, idx2, idx1 = idx2, idx1, i
					dist3, dist2, dist1 = dist2, dist1, d
				case dist2 >= d:
					idx3, idx2 = idx2, i
					dist3, dist2 = dist2, d
				case dist3 >= d:
					idx3, dist3 = i, d
				}
			}
		}
	}

	closest1 := a.status[idx1]
	sim12 := aquiferSimilarity(dist1, dist2)
	fluid := closest1.At(y)
	if sim12 <= 0 {
		return fluid, true
	}
	// Water sitting directly on the global lava level always wins: it is what
	// makes the lava-lake shorelines steam rather than vanish.
	if fluid == blockWater && a.global(x, y-1, z).At(y-1) == blockLava {
		return fluid, true
	}

	barrierNoise := math.NaN()
	closest2 := a.status[idx2]
	if density+sim12*a.calculatePressure(x, y, z, &barrierNoise, closest1, closest2) > 0 {
		return 0, false
	}
	closest3 := a.status[idx3]
	if sim13 := aquiferSimilarity(dist1, dist3); sim13 > 0 {
		if density+sim12*sim13*a.calculatePressure(x, y, z, &barrierNoise, closest1, closest3) > 0 {
			return 0, false
		}
	}
	if sim23 := aquiferSimilarity(dist2, dist3); sim23 > 0 {
		if density+sim12*sim23*a.calculatePressure(x, y, z, &barrierNoise, closest2, closest3) > 0 {
			return 0, false
		}
	}
	return fluid, true
}

// aquiferSimilarity falls from 1 to 0 as the second distance pulls away from
// the first; at or below 0 the nearest centre wins outright and no barrier is
// evaluated.
func aquiferSimilarity(distSqr1, distSqr2 int) float64 {
	return 1.0 - float64(distSqr2-distSqr1)/25.0
}

// calculatePressure is the barrier between two aquifers: how hard the rock
// between them resists being carved open. barrierNoise memoises the noise
// sample across the (up to three) pressure evaluations at one position, exactly
// as vanilla's MutableDouble does.
func (a *Aquifer) calculatePressure(x, y, z int, barrierNoise *float64, s1, s2 FluidStatus) float64 {
	type1 := s1.At(y)
	type2 := s2.At(y)
	if (type1 == blockLava && type2 == blockWater) || (type1 == blockWater && type2 == blockLava) {
		return 2.0
	}
	fluidYDiff := s1.Level - s2.Level
	if fluidYDiff < 0 {
		fluidYDiff = -fluidYDiff
	}
	if fluidYDiff == 0 {
		return 0.0
	}
	averageFluidY := 0.5 * float64(s1.Level+s2.Level)
	howFarAboveAverage := float64(y) + 0.5 - averageFluidY
	baseValue := float64(fluidYDiff) / 2.0
	// Distance from the barrier's edge towards its middle; the biases below are
	// vanilla's, and they are asymmetric: rock reaches much further down from a
	// fluid surface than up from it.
	distanceFromEdge := baseValue - math.Abs(howFarAboveAverage)
	var gradient float64
	if howFarAboveAverage > 0 {
		if centerPoint := 0.0 + distanceFromEdge; centerPoint > 0 {
			gradient = centerPoint / 1.5
		} else {
			gradient = centerPoint / 2.5
		}
	} else {
		if centerPoint := 3.0 + distanceFromEdge; centerPoint > 0 {
			gradient = centerPoint / 3.0
		} else {
			gradient = centerPoint / 10.0
		}
	}
	var noiseValue float64
	if gradient >= -2.0 && gradient <= 2.0 {
		if math.IsNaN(*barrierNoise) {
			*barrierNoise = a.od.Barrier.Compute(FunctionContext{X: float64(x), Y: float64(y), Z: float64(z)})
		}
		noiseValue = *barrierNoise
	}
	return 2.0 * (noiseValue + gradient)
}

// computeFluid decides one aquifer centre's fluid level and type.
func (a *Aquifer) computeFluid(x, y, z int) FluidStatus {
	global := a.global(x, y, z)
	lowestPreliminarySurface := math.MaxInt32
	topOfCell := y + aquiferYSpacing
	bottomOfCell := y - aquiferYSpacing
	surfaceAtCentreIsUnderFluid := false
	for _, off := range surfaceSamplingOffsets {
		sampleX := x + off[0]*16
		sampleZ := z + off[1]*16
		preliminary := a.od.PreliminarySurfaceLevelAt(sampleX, sampleZ)
		adjusted := adjustSurfaceLevel(preliminary)
		start := off[0] == 0 && off[1] == 0
		// Wholly below the terrain: an ordinary underground aquifer, whose
		// level the noise decides.
		if start && bottomOfCell > adjusted {
			return global
		}
		pokesAboveSurface := topOfCell > adjusted
		if pokesAboveSurface || start {
			if atSurface := a.global(sampleX, adjusted, sampleZ); atSurface.At(adjusted) != blockAir {
				if start {
					surfaceAtCentreIsUnderFluid = true
				}
				// Breaking the surface under an ocean: take the ocean's level,
				// so sea floors do not dry out.
				if pokesAboveSurface {
					return atSurface
				}
			}
		}
		lowestPreliminarySurface = min(lowestPreliminarySurface, preliminary)
	}
	level := a.computeSurfaceLevel(x, y, z, global, lowestPreliminarySurface, surfaceAtCentreIsUnderFluid)
	return FluidStatus{Level: level, Type: a.computeFluidType(x, y, z, global, level)}
}

func adjustSurfaceLevel(preliminarySurfaceLevel int) int { return preliminarySurfaceLevel + 8 }

// computeSurfaceLevel picks the aquifer's fluid level: the global one when the
// floodedness noise says "fully flooded", a randomised low one when it says
// "partially", and nothing at all otherwise — which is what leaves caves dry.
func (a *Aquifer) computeSurfaceLevel(x, y, z int, global FluidStatus, lowestPreliminarySurface int, surfaceAtCentreIsUnderFluid bool) int {
	ctx := FunctionContext{X: float64(x), Y: float64(y), Z: float64(z)}
	var partiallyFloodedness, fullyFloodedness float64
	if a.isDeepDarkRegion(ctx) {
		// The deep dark is never flooded.
		partiallyFloodedness, fullyFloodedness = -1.0, -1.0
	} else {
		distanceBelowSurface := lowestPreliminarySurface + 8 - y
		floodednessFactor := 0.0
		if surfaceAtCentreIsUnderFluid {
			floodednessFactor = clampedMap(float64(distanceBelowSurface), 0.0, 64.0, 1.0, 0.0)
		}
		floodednessNoise := clamp(a.od.FluidLevelFloodedness.Compute(ctx), -1.0, 1.0)
		fullyFloodedThreshold := mapRange(floodednessFactor, 1.0, 0.0, -0.3, 0.8)
		partiallyFloodedThreshold := mapRange(floodednessFactor, 1.0, 0.0, -0.8, 0.4)
		partiallyFloodedness = floodednessNoise - partiallyFloodedThreshold
		fullyFloodedness = floodednessNoise - fullyFloodedThreshold
	}
	switch {
	case fullyFloodedness > 0:
		return global.Level
	case partiallyFloodedness > 0:
		return a.computeRandomizedFluidSurfaceLevel(x, y, z, lowestPreliminarySurface)
	default:
		return WayBelowMinY
	}
}

// computeRandomizedFluidSurfaceLevel puts the water table somewhere in the
// middle of a 40-block-tall cell, nudged by the spread noise and quantised to
// three blocks so neighbouring cells share levels often enough to connect.
func (a *Aquifer) computeRandomizedFluidSurfaceLevel(x, y, z, lowestPreliminarySurface int) int {
	const cellWidth, cellHeight, maxSpread = 16, 40, 10
	cellX := floorDivInt(x, cellWidth)
	cellY := floorDivInt(y, cellHeight)
	cellZ := floorDivInt(z, cellWidth)
	middleY := cellY*cellHeight + cellHeight/2
	spread := a.od.FluidLevelSpread.Compute(FunctionContext{X: float64(cellX), Y: float64(cellY), Z: float64(cellZ)}) * maxSpread
	return min(lowestPreliminarySurface, middleY+quantizeToMultiple(spread, 3))
}

// computeFluidType turns deep aquifers into lava lakes.
func (a *Aquifer) computeFluidType(x, y, z int, global FluidStatus, fluidSurfaceLevel int) uint16 {
	if fluidSurfaceLevel > -10 || fluidSurfaceLevel == WayBelowMinY || global.Type == blockLava {
		return global.Type
	}
	const cellWidth, cellHeight = 64, 40
	lavaNoise := a.od.Lava.Compute(FunctionContext{
		X: float64(floorDivInt(x, cellWidth)),
		Y: float64(floorDivInt(y, cellHeight)),
		Z: float64(floorDivInt(z, cellWidth)),
	})
	if math.Abs(lavaNoise) > 0.3 {
		return blockLava
	}
	return global.Type
}

// isDeepDarkRegion is OverworldBiomeBuilder.isDeepDarkRegion.
func (a *Aquifer) isDeepDarkRegion(ctx FunctionContext) bool {
	if a.od.Erosion == nil || a.od.Depth == nil {
		return false
	}
	return a.od.Erosion.Compute(ctx) < deepDarkErosionMax && a.od.Depth.Compute(ctx) > deepDarkDepthMin
}

// ---- grid arithmetic ---------------------------------------------------

func aquiferGridX(blockCoord int) int       { return blockCoord >> 4 }
func aquiferGridZ(blockCoord int) int       { return blockCoord >> 4 }
func aquiferGridY(blockCoord int) int       { return floorDivInt(blockCoord, aquiferYSpacing) }
func fromAquiferGridX(grid, offset int) int { return grid<<4 + offset }
func fromAquiferGridZ(grid, offset int) int { return grid<<4 + offset }
func fromAquiferGridY(grid, offset int) int { return grid*aquiferYSpacing + offset }

// floorDivInt is Math.floorDiv: division rounding towards negative infinity.
func floorDivInt(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// quantizeToMultiple is Mth.quantize: round down to a multiple of factor.
func quantizeToMultiple(value float64, factor int) int {
	return int(math.Floor(value/float64(factor))) * factor
}

// mapRange is Mth.map: an unclamped linear remap (clampedMap is the clamped one).
func mapRange(value, from0, to0, from1, to1 float64) float64 {
	t := (value - from0) / (to0 - from0)
	return from1 + t*(to1-from1)
}
