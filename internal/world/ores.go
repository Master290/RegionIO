package world

import (
	"math"

	"regionio/internal/worldgen"
)

// oreVeinPlacementEvent records a single stage-6 minecraft:ore ellipsoid
// placement that wrote at least one block inside a tracked target chunk.
type oreVeinPlacementEvent struct {
	SourceOrderIndex int
	SourceX          int32
	SourceZ          int32
	FeatureName      string
	FeatureIndex     int
	Attempt          int
	OriginX          int
	OriginY          int
	OriginZ          int
	Size             int
	State            uint16
	CellsInTarget    [][3]int
}

// oreCellWriteHistory tracks the sequence of stage-6 ore vein writes to a
// single world-space block coordinate (X, Y, Z) within a target chunk.
type oreCellWriteHistory struct {
	InitialState uint16
	FinalState   uint16
	Events       []int // indices into the trace's Events slice
}

// oreChunkTrace summarizes all stage-6 minecraft:ore writes into a target
// chunk across a sequence of decoration source chunks.
type oreChunkTrace struct {
	TargetX int32
	TargetZ int32
	Events  []oreVeinPlacementEvent
	Cells   map[[3]int]*oreCellWriteHistory
}

// buildOreSpheresJavaReference implements the exact pairwise sphere pruning
// loop from net.minecraft.world.level.levelgen.feature.OreFeature.doPlace in
// versions/26.1.2/server-26.1.2.jar (bytecode offsets 295..428), which sets
// data[i*4+3] = -1.0 without breaking out of the inner j loop when dr <= 0.
// It is used to verify that buildOreSpheresFrom produces identical sphere
// radii for every vein.
func buildOreSpheresJavaReference(random worldgen.RandomSource, setup oreVeinSetup) []oreSphere {
	size := setup.size
	x0, x1 := setup.x0, setup.x1
	z0, z1 := setup.z0, setup.z1
	y0, y1 := setup.y0, setup.y1

	spheres := make([]oreSphere, size)
	for i := 0; i < size; i++ {
		t := float32(i) / float32(size)
		randomScale := random.NextDouble() * float64(size) / 16.0
		wave := worldgen.MthSin(float64(float32(math.Pi)*t)) + float32(1.0)
		radius := (float64(wave)*randomScale + 1.0) / 2.0
		spheres[i] = oreSphere{
			x:      x0 + (x1-x0)*float64(t),
			y:      y0 + (y1-y0)*float64(t),
			z:      z0 + (z1-z0)*float64(t),
			radius: radius,
		}
	}
	for i := 0; i < size-1; i++ {
		if spheres[i].radius <= 0 {
			continue
		}
		for j := i + 1; j < size; j++ {
			if spheres[j].radius <= 0 {
				continue
			}
			dx := spheres[i].x - spheres[j].x
			dy := spheres[i].y - spheres[j].y
			dz := spheres[i].z - spheres[j].z
			dr := spheres[i].radius - spheres[j].radius
			if dx*dx+dy*dy+dz*dz < dr*dr {
				if dr > 0 {
					spheres[j].radius = -1
				} else {
					spheres[i].radius = -1
				}
			}
		}
	}
	return spheres
}

// traceTargetChunkStage6Ores replays stages 1..6 (structures + per-source
// lakes, geodes, monster rooms, and stage-6 underground_ores) for the given
// source chunk sequence on a pre-initialized decorationRegion and records
// every minecraft:ore write that lands inside target chunk (targetX, targetZ).
func traceTargetChunkStage6Ores(
	od *worldgen.OverworldDensity,
	seed int64,
	region *decorationRegion,
	targetX, targetZ int32,
	sources []decorationSource,
) (*oreChunkTrace, error) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		return nil, err
	}
	if err := region.placeScheduledStructures(od, seed, targetX, targetZ); err != nil {
		return nil, err
	}
	minX, maxX := int(targetX)<<4, (int(targetX)<<4)+15
	minZ, maxZ := int(targetZ)<<4, (int(targetZ)<<4)+15

	trace := &oreChunkTrace{
		TargetX: targetX,
		TargetZ: targetZ,
		Cells:   make(map[[3]int]*oreCellWriteHistory),
	}

	magma, magmaOK := nameToStateID("minecraft:magma_block", nil)
	biomeOrder := possibleBiomeOrder()

	for srcIdx, source := range sources {
		if err := region.setSource(source.X, source.Z); err != nil {
			return nil, err
		}
		if err := region.placeScheduledLakes(seed); err != nil {
			return nil, err
		}
		if err := region.placeScheduledGeodes(seed); err != nil {
			return nil, err
		}
		if err := region.placeScheduledMonsterRooms(seed); err != nil {
			return nil, err
		}

		region.bindSeed(seed)
		if err := region.ensureSourceNeighborhood(); err != nil {
			return nil, err
		}
		schedule, err := set.FeatureSchedule(biomeOrder, region.sourceBiomes(), undergroundOresStage)
		if err != nil {
			return nil, err
		}
		random, decorationSeed := worldgen.DecorationRandom(seed, int(region.sourceX), int(region.sourceZ))
		origin := worldgen.FeaturePosition{X: int(region.sourceX) << 4, Y: MinY, Z: int(region.sourceZ) << 4}

		for _, scheduled := range schedule {
			placed, ok := set.Placed[scheduled.Name]
			if !ok {
				continue
			}
			configured, ok := set.Configured[placed.Feature]
			if !ok {
				continue
			}
			random.SetFeatureSeed(decorationSeed, scheduled.Index, undergroundOresStage)
			context := region.placementContext(func(position worldgen.FeaturePosition) bool {
				return region.biomeAllowsFeature(set, scheduled.Name, undergroundOresStage, position)
			})

			switch configured.Type {
			case "minecraft:ore":
				config, err := set.Ore(placed.Feature)
				if err != nil {
					return nil, err
				}
				targets, ok := resolveOreTargets(set, config)
				if !ok {
					continue
				}
				attempt := 0
				if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
					attempt++
					setup := drawOreVeinSetup(random, position.X, position.Y, position.Z, config.Size)
					if !oreVeinPassesHeightGate(position.X, position.Y, position.Z, config.Size, func(x, z int) (int, bool) {
						return region.heightAt("OCEAN_FLOOR_WG", x, z), true
					}) {
						return nil
					}
					spheres := buildOreSpheresFrom(random, setup)
					var writtenCells [][3]int
					var writtenState uint16
					walkOreBlocks(spheres, func(x, y, z int) {
						if y < MinY || y >= MinY+WorldHeight {
							return
						}
						current := region.getBlock(x, y, z)
						for _, target := range targets {
							if !canPlaceOreBlock(current, target, config.DiscardAirExposure, random, func() bool {
								return exposedToAirRegion(region, x, y, z)
							}) {
								continue
							}
							region.setBlock(x, y, z, target.state)
							if x >= minX && x <= maxX && z >= minZ && z <= maxZ {
								writtenCells = append(writtenCells, [3]int{x, y, z})
								writtenState = target.state
							}
							break
						}
					})
					if len(writtenCells) > 0 {
						eventIdx := len(trace.Events)
						trace.Events = append(trace.Events, oreVeinPlacementEvent{
							SourceOrderIndex: srcIdx,
							SourceX:          source.X,
							SourceZ:          source.Z,
							FeatureName:      scheduled.Name,
							FeatureIndex:     scheduled.Index,
							Attempt:          attempt,
							OriginX:          position.X,
							OriginY:          position.Y,
							OriginZ:          position.Z,
							Size:             config.Size,
							State:            writtenState,
							CellsInTarget:    writtenCells,
						})
						for _, pos := range writtenCells {
							hist, exists := trace.Cells[pos]
							if !exists {
								hist = &oreCellWriteHistory{
									InitialState: StateStone,
								}
								trace.Cells[pos] = hist
							}
							hist.FinalState = writtenState
							hist.Events = append(hist.Events, eventIdx)
						}
					}
					return nil
				}); err != nil {
					return nil, err
				}
			case "minecraft:underwater_magma":
				if !magmaOK {
					continue
				}
				config, err := set.UnderwaterMagma(placed.Feature)
				if err != nil {
					return nil, err
				}
				if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
					region.placeUnderwaterMagma(random, position, config, magma)
					return nil
				}); err != nil {
					return nil, err
				}
			case "minecraft:disk":
				config, err := set.Disk(placed.Feature)
				if err != nil {
					return nil, err
				}
				if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
					return region.placeDisk(set, random, position, config)
				}); err != nil {
					return nil, err
				}
			}
		}
		if err := region.placeScheduledUndergroundDecoration(seed); err != nil {
			return nil, err
		}
		if err := region.placeScheduledSprings(seed); err != nil {
			return nil, err
		}
		if err := region.placeScheduledVegetationPatches(seed); err != nil {
			return nil, err
		}
	}
	return trace, nil
}
