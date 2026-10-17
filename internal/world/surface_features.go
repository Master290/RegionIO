package world

import (
	"fmt"
	"math"

	"regionio/internal/worldgen"
)

const surfaceStructuresStage = 4

// placeScheduledSurfaceStructures replays the vanilla stage-4 (surface_structures)
// feature schedule: desert_well, ice_spike, ice_patch (disk), and blue_ice.
func (r *decorationRegion) placeScheduledSurfaceStructures(seed int64) error {
	r.bindSeed(seed)
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		return err
	}
	if err := r.ensureSourceNeighborhood(); err != nil {
		return err
	}
	schedule, err := set.FeatureSchedule(possibleBiomeOrder(), r.sourceBiomes(), surfaceStructuresStage)
	if err != nil {
		return err
	}
	random, decorationSeed := worldgen.DecorationRandom(seed, int(r.sourceX), int(r.sourceZ))
	origin := worldgen.FeaturePosition{X: int(r.sourceX) << 4, Y: MinY, Z: int(r.sourceZ) << 4}
	for _, scheduled := range schedule {
		placed, ok := set.Placed[scheduled.Name]
		if !ok {
			continue
		}
		configured, ok := set.Configured[placed.Feature]
		if !ok {
			continue
		}
		switch configured.Type {
		case "minecraft:desert_well", "minecraft:spike", "minecraft:disk", "minecraft:blue_ice":
		default:
			noteNotReplayed(fmt.Sprintf("stage%d:%s", surfaceStructuresStage, configured.Type))
			continue
		}
		random.SetFeatureSeed(decorationSeed, scheduled.Index, surfaceStructuresStage)
		context := r.placementContext(func(position worldgen.FeaturePosition) bool {
			return r.biomeAllowsFeature(set, scheduled.Name, surfaceStructuresStage, position)
		})
		switch configured.Type {
		case "minecraft:desert_well":
			if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
				r.placeDesertWell(random, position)
				return nil
			}); err != nil {
				return err
			}
		case "minecraft:spike":
			spikeCfg, err := set.Spike(placed.Feature)
			if err != nil {
				return err
			}
			if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
				return r.placeSpike(set, random, position, spikeCfg)
			}); err != nil {
				return err
			}
		case "minecraft:disk":
			diskCfg, err := set.Disk(placed.Feature)
			if err != nil {
				return err
			}
			if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
				return r.placeDisk(set, random, position, diskCfg)
			}); err != nil {
				return err
			}
		case "minecraft:blue_ice":
			if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
				r.placeBlueIce(random, position)
				return nil
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// placeDesertWell ports net.minecraft.world.level.levelgen.feature.DesertWellFeature.place.
func (r *decorationRegion) placeDesertWell(random worldgen.RandomSource, origin worldgen.FeaturePosition) bool {
	pos := worldgen.FeaturePosition{X: origin.X, Y: origin.Y + 1, Z: origin.Z}
	for isAirState(r.getBlock(pos.X, pos.Y, pos.Z)) && pos.Y > MinY+2 {
		pos.Y--
	}
	if !blockNameEquals(r.getBlock(pos.X, pos.Y, pos.Z), "minecraft:sand") {
		return false
	}
	for ox := -2; ox <= 2; ox++ {
		for oz := -2; oz <= 2; oz++ {
			if isAirState(r.getBlock(pos.X+ox, pos.Y-1, pos.Z+oz)) && isAirState(r.getBlock(pos.X+ox, pos.Y-2, pos.Z+oz)) {
				return false
			}
		}
	}
	sandID, ok1 := nameToStateID("minecraft:sand", nil)
	sandSlabID, ok2 := nameToStateID("minecraft:sandstone_slab", map[string]string{"type": "bottom", "waterlogged": "false"})
	sandstoneID, ok3 := nameToStateID("minecraft:sandstone", nil)
	waterID, ok4 := nameToStateID("minecraft:water", map[string]string{"level": "0"})
	susSandID, ok5 := nameToStateID("minecraft:suspicious_sand", map[string]string{"dusted": "0"})
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
		return false
	}
	for oy := -2; oy <= 0; oy++ {
		for ox := -2; ox <= 2; ox++ {
			for oz := -2; oz <= 2; oz++ {
				r.setBlockGlobal(pos.X+ox, pos.Y+oy, pos.Z+oz, sandstoneID)
			}
		}
	}
	r.setBlockGlobal(pos.X, pos.Y, pos.Z, waterID)
	horiz := [4][3]int{{0, 0, -1}, {1, 0, 0}, {0, 0, 1}, {-1, 0, 0}}
	for _, d := range horiz {
		r.setBlockGlobal(pos.X+d[0], pos.Y+d[1], pos.Z+d[2], waterID)
	}
	sandCenter := worldgen.FeaturePosition{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	r.setBlockGlobal(sandCenter.X, sandCenter.Y, sandCenter.Z, sandID)
	for _, d := range horiz {
		r.setBlockGlobal(sandCenter.X+d[0], sandCenter.Y+d[1], sandCenter.Z+d[2], sandID)
	}
	for ox := -2; ox <= 2; ox++ {
		for oz := -2; oz <= 2; oz++ {
			if ox == -2 || ox == 2 || oz == -2 || oz == 2 {
				r.setBlockGlobal(pos.X+ox, pos.Y+1, pos.Z+oz, sandstoneID)
			}
		}
	}
	r.setBlockGlobal(pos.X+2, pos.Y+1, pos.Z, sandSlabID)
	r.setBlockGlobal(pos.X-2, pos.Y+1, pos.Z, sandSlabID)
	r.setBlockGlobal(pos.X, pos.Y+1, pos.Z+2, sandSlabID)
	r.setBlockGlobal(pos.X, pos.Y+1, pos.Z-2, sandSlabID)
	for ox := -1; ox <= 1; ox++ {
		for oz := -1; oz <= 1; oz++ {
			if ox == 0 && oz == 0 {
				r.setBlockGlobal(pos.X+ox, pos.Y+4, pos.Z+oz, sandstoneID)
			} else {
				r.setBlockGlobal(pos.X+ox, pos.Y+4, pos.Z+oz, sandSlabID)
			}
		}
	}
	for oy := 1; oy <= 3; oy++ {
		r.setBlockGlobal(pos.X-1, pos.Y+oy, pos.Z-1, sandstoneID)
		r.setBlockGlobal(pos.X-1, pos.Y+oy, pos.Z+1, sandstoneID)
		r.setBlockGlobal(pos.X+1, pos.Y+oy, pos.Z-1, sandstoneID)
		r.setBlockGlobal(pos.X+1, pos.Y+oy, pos.Z+1, sandstoneID)
	}
	waterPositions := [5]worldgen.FeaturePosition{
		pos,
		{X: pos.X + 1, Y: pos.Y, Z: pos.Z},
		{X: pos.X, Y: pos.Y, Z: pos.Z + 1},
		{X: pos.X - 1, Y: pos.Y, Z: pos.Z},
		{X: pos.X, Y: pos.Y, Z: pos.Z - 1},
	}
	p1 := waterPositions[int(random.NextIntN(5))]
	p1.Y -= 1
	r.placeDesertWellSusSand(p1, susSandID)
	p2 := waterPositions[int(random.NextIntN(5))]
	p2.Y -= 2
	r.placeDesertWellSusSand(p2, susSandID)
	return true
}

func (r *decorationRegion) placeDesertWellSusSand(pos worldgen.FeaturePosition, susSandID uint16) {
	if r.setBlockGlobal(pos.X, pos.Y, pos.Z, susSandID) {
		r.addStructureBlockEntity(StructureBlockEntity{
			ID:            "minecraft:brushable_block",
			X:             pos.X,
			Y:             pos.Y,
			Z:             pos.Z,
			LootTable:     "minecraft:archaeology/desert_well",
			LootTableSeed: blockPosAsLong(pos.X, pos.Y, pos.Z),
		})
	}
}

// placeSpike ports net.minecraft.world.level.levelgen.feature.SpikeFeature.place.
func (r *decorationRegion) placeSpike(set *worldgen.FeatureSet, random worldgen.RandomSource, origin worldgen.FeaturePosition, config worldgen.SpikeFeatureConfig) error {
	for isAirState(r.getBlock(origin.X, origin.Y, origin.Z)) && origin.Y > MinY+2 {
		origin.Y--
	}
	ok, err := r.testBlockPredicate(set, config.CanPlaceOn, origin)
	if err != nil || !ok {
		return err
	}
	spikeState, ok := nameToStateID(config.State.Name, config.State.Properties)
	if !ok {
		return nil
	}
	origin.Y += int(random.NextIntN(4))
	height := int(random.NextIntN(4)) + 7
	width := height/4 + int(random.NextIntN(2))
	if width > 1 && random.NextIntN(60) == 0 {
		origin.Y += 10 + int(random.NextIntN(30))
	}
	for yOff := 0; yOff < height; yOff++ {
		scale := (float32(1.0) - float32(yOff)/float32(height)) * float32(width)
		newWidth := int(math.Ceil(float64(scale)))
		for xo := -newWidth; xo <= newWidth; xo++ {
			dx := float32(absInt(xo)) - 0.25
			for zo := -newWidth; zo <= newWidth; zo++ {
				dz := float32(absInt(zo)) - 0.25
				if (xo != 0 || zo != 0) && dx*dx+dz*dz > scale*scale {
					continue
				}
				if (xo == -newWidth || xo == newWidth || zo == -newWidth || zo == newWidth) && random.NextFloat() > 0.75 {
					continue
				}
				posP := worldgen.FeaturePosition{X: origin.X + xo, Y: origin.Y + yOff, Z: origin.Z + zo}
				stP := r.getBlock(posP.X, posP.Y, posP.Z)
				canRepP := isAirState(stP)
				if !canRepP {
					canRepP, _ = r.testBlockPredicate(set, config.CanReplace, posP)
				}
				if canRepP {
					r.setBlockGlobal(posP.X, posP.Y, posP.Z, spikeState)
				}
				if yOff != 0 && newWidth > 1 {
					posN := worldgen.FeaturePosition{X: origin.X + xo, Y: origin.Y - yOff, Z: origin.Z + zo}
					stN := r.getBlock(posN.X, posN.Y, posN.Z)
					canRepN := isAirState(stN)
					if !canRepN {
						canRepN, _ = r.testBlockPredicate(set, config.CanReplace, posN)
					}
					if canRepN {
						r.setBlockGlobal(posN.X, posN.Y, posN.Z, spikeState)
					}
				}
			}
		}
	}
	pillarWidth := width - 1
	if pillarWidth < 0 {
		pillarWidth = 0
	} else if pillarWidth > 1 {
		pillarWidth = 1
	}
	for xo := -pillarWidth; xo <= pillarWidth; xo++ {
		for zo := -pillarWidth; zo <= pillarWidth; zo++ {
			cursor := worldgen.FeaturePosition{X: origin.X + xo, Y: origin.Y - 1, Z: origin.Z + zo}
			runLength := 50
			if absInt(xo) == 1 && absInt(zo) == 1 {
				runLength = int(random.NextIntN(5))
			}
			for cursor.Y > 50 {
				st := r.getBlock(cursor.X, cursor.Y, cursor.Z)
				canRep := isAirState(st) || st == spikeState
				if !canRep {
					canRep, _ = r.testBlockPredicate(set, config.CanReplace, cursor)
				}
				if !canRep {
					break
				}
				r.setBlockGlobal(cursor.X, cursor.Y, cursor.Z, spikeState)
				cursor.Y--
				runLength--
				if runLength <= 0 {
					cursor.Y -= int(random.NextIntN(5)) + 1
					runLength = int(random.NextIntN(5))
				}
			}
		}
	}
	return nil
}

// placeBlueIce ports net.minecraft.world.level.levelgen.feature.BlueIceFeature.place.
func (r *decorationRegion) placeBlueIce(random worldgen.RandomSource, origin worldgen.FeaturePosition) bool {
	if origin.Y > SeaLevel-1 {
		return false
	}
	if !blockNameEquals(r.getBlock(origin.X, origin.Y, origin.Z), "minecraft:water") &&
		!blockNameEquals(r.getBlock(origin.X, origin.Y-1, origin.Z), "minecraft:water") {
		return false
	}
	dirs := [6][3]int{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}
	foundPackedIce := false
	for i, d := range dirs {
		if i == 0 { // Direction.DOWN
			continue
		}
		if blockNameEquals(r.getBlock(origin.X+d[0], origin.Y+d[1], origin.Z+d[2]), "minecraft:packed_ice") {
			foundPackedIce = true
			break
		}
	}
	if !foundPackedIce {
		return false
	}
	blueIceID, ok := nameToStateID("minecraft:blue_ice", nil)
	if !ok {
		return false
	}
	r.setBlockGlobal(origin.X, origin.Y, origin.Z, blueIceID)
	for i := 0; i < 200; i++ {
		yOff := int(random.NextIntN(5)) - int(random.NextIntN(6))
		xzDiff := 3
		if yOff < 2 {
			xzDiff += yOff / 2
		}
		if xzDiff < 1 {
			continue
		}
		px := origin.X + int(random.NextIntN(int32(xzDiff))) - int(random.NextIntN(int32(xzDiff)))
		py := origin.Y + yOff
		pz := origin.Z + int(random.NextIntN(int32(xzDiff))) - int(random.NextIntN(int32(xzDiff)))
		placeState := r.getBlock(px, py, pz)
		if !isAirState(placeState) &&
			!blockNameEquals(placeState, "minecraft:water") &&
			!blockNameEquals(placeState, "minecraft:packed_ice") &&
			!blockNameEquals(placeState, "minecraft:ice") {
			continue
		}
		for _, d := range dirs {
			if r.getBlock(px+d[0], py+d[1], pz+d[2]) == blueIceID {
				r.setBlockGlobal(px, py, pz, blueIceID)
				break
			}
		}
	}
	return true
}
