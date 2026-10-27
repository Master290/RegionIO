package world

import (
	"fmt"
	"math"

	"regionio/internal/worldgen"
)

// undergroundDecorationStage is UNDERGROUND_DECORATION (step index 7) in the
// vanilla feature schedule. Across all Overworld biomes in 26.1.2 this stage
// contains five placed features:
//   - minecraft:ore_infested        (minecraft:ore)
//   - minecraft:sculk_vein          (minecraft:multiface_growth)
//   - minecraft:sculk_patch_deep_dark (minecraft:sculk_patch)
//   - minecraft:dripstone_cluster   (minecraft:dripstone_cluster)
//   - minecraft:pointed_dripstone   (minecraft:simple_random_selector)
const undergroundDecorationStage = 7

func (r *decorationRegion) placeScheduledUndergroundDecoration(seed int64) error {
	r.bindSeed(seed)
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		return err
	}
	if err := r.ensureSourceNeighborhood(); err != nil {
		return err
	}
	schedule, err := set.FeatureSchedule(possibleBiomeOrder(), r.sourceBiomes(), undergroundDecorationStage)
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
		case "minecraft:ore", "minecraft:multiface_growth", "minecraft:sculk_patch",
			"minecraft:dripstone_cluster", "minecraft:pointed_dripstone", "minecraft:simple_random_selector":
		default:
			noteNotReplayed(fmt.Sprintf("stage%d:%s", undergroundDecorationStage, configured.Type))
			continue
		}
		random.SetFeatureSeed(decorationSeed, scheduled.Index, undergroundDecorationStage)
		context := r.placementContext(func(position worldgen.FeaturePosition) bool {
			return r.biomeAllowsFeature(set, scheduled.Name, undergroundDecorationStage, position)
		})
		switch configured.Type {
		case "minecraft:ore":
			config, err := set.Ore(placed.Feature)
			if err != nil {
				return err
			}
			targets, ok := resolveOreTargets(set, config)
			if !ok {
				continue
			}
			if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
				placeOreEllipsoidRegion(r, random, position.X, position.Y, position.Z, config.Size, config.DiscardAirExposure, targets)
				return nil
			}); err != nil {
				return err
			}
		case "minecraft:multiface_growth":
			config, err := set.MultifaceGrowth(placed.Feature)
			if err != nil {
				return err
			}
			if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
				r.placeMultifaceGrowth(set, random, position, config)
				return nil
			}); err != nil {
				return err
			}
		case "minecraft:sculk_patch":
			config, err := set.SculkPatch(placed.Feature)
			if err != nil {
				return err
			}
			if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
				r.placeSculkPatch(set, random, position, config)
				return nil
			}); err != nil {
				return err
			}
		case "minecraft:dripstone_cluster":
			config, err := set.DripstoneCluster(placed.Feature)
			if err != nil {
				return err
			}
			if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
				r.placeDripstoneCluster(set, random, position, config)
				return nil
			}); err != nil {
				return err
			}
		case "minecraft:pointed_dripstone":
			config, err := set.PointedDripstone(placed.Feature)
			if err != nil {
				return err
			}
			if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
				r.placePointedDripstone(set, random, position, config)
				return nil
			}); err != nil {
				return err
			}
		case "minecraft:simple_random_selector":
			config, err := set.SimpleRandomSelector(placed.Feature)
			if err != nil {
				return err
			}
			if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
				r.placeSimpleRandomSelector(random, position, config, set)
				return nil
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// SculkPatchFeature + SculkSpreader.createWorldGenSpreader
// ---------------------------------------------------------------------------

// sculkNonCornerNeighbours is SculkSpreader$ChargeCursor.NON_CORNER_NEIGHBOURS,
// built from BlockPos.betweenClosed(-1,-1,-1, 1,1,1) (z-outer, y-middle, x-inner)
// keeping offsets where at least one coordinate is zero and not all three are zero.
var sculkNonCornerNeighbours = func() [18][3]int {
	var out [18][3]int
	idx := 0
	for dz := -1; dz <= 1; dz++ {
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				if (dx == 0 || dy == 0 || dz == 0) && (dx != 0 || dy != 0 || dz != 0) {
					out[idx] = [3]int{dx, dy, dz}
					idx++
				}
			}
		}
	}
	return out
}()

type sculkChargeCursor struct {
	x, y, z     int
	charge      int
	decayDelay  int
	updateDelay int
	hasFacings  bool
	facings     []multifaceDir
}

// placeSculkPatch is SculkPatchFeature.place with SculkSpreader.createWorldGenSpreader(),
// transcribed from javap -p -c against versions/26.1.2/server-26.1.2.jar for
// SculkPatchFeature, SculkSpreader, SculkSpreader$ChargeCursor, SculkBehaviour,
// SculkBlock, and SculkVeinBlock.
func (r *decorationRegion) placeSculkPatch(set *worldgen.FeatureSet, random worldgen.RandomSource, origin worldgen.FeaturePosition, config worldgen.SculkPatchFeatureConfig) bool {
	if !r.sculkCanSpreadFrom(origin.X, origin.Y, origin.Z) {
		return false
	}
	sculkReplaceable := tagStateIDs(set, "#minecraft:sculk_replaceable")
	sculkReplaceableWorldGen := tagStateIDs(set, "#minecraft:sculk_replaceable_world_gen")
	totalRounds := config.SpreadRounds + config.GrowthRounds
	for round := 0; round < totalRounds; round++ {
		var cursors []*sculkChargeCursor
		for i := 0; i < config.ChargeCount; i++ {
			if config.AmountPerCharge > 0 {
				cursors = append(cursors, &sculkChargeCursor{
					x:           origin.X,
					y:           origin.Y,
					z:           origin.Z,
					charge:      config.AmountPerCharge,
					decayDelay:  1,
					updateDelay: 0,
				})
			}
		}
		spreadVeins := round < config.SpreadRounds
		for attempt := 0; attempt < config.SpreadAttempts; attempt++ {
			if len(cursors) == 0 {
				break
			}
			var nextCursors []*sculkChargeCursor
			for _, cursor := range cursors {
				if max(abs(cursor.x-origin.X), max(abs(cursor.y-origin.Y), abs(cursor.z-origin.Z))) > 1024 {
					continue
				}
				r.sculkCursorUpdate(set, cursor, origin, random, sculkReplaceable, sculkReplaceableWorldGen, spreadVeins)
				if cursor.charge <= 0 {
					continue
				}
				// In worldgen (isWorldGeneration == true), SculkSpreader.updateCursors
				// never merges cursors sharing a position (offset 191: if (!isWorldGeneration)).
				nextCursors = append(nextCursors, cursor)
			}
			cursors = nextCursors
		}
	}
	belowState := r.getBlock(origin.X, origin.Y-1, origin.Z)
	if random.NextFloat() <= config.CatalystChance && canMultifaceAttachTo(belowState) {
		if catalyst, ok := nameToStateID("minecraft:sculk_catalyst", map[string]string{"bloom": "false"}); ok {
			if r.setBlock(origin.X, origin.Y, origin.Z, catalyst) {
				r.addStructureBlockEntity(StructureBlockEntity{
					ID: "minecraft:sculk_catalyst",
					X:  origin.X,
					Y:  origin.Y,
					Z:  origin.Z,
				})
			}
		}
	}
	extra := config.ExtraRareGrowths.Sample(random)
	for i := 0; i < extra; i++ {
		tx := origin.X + int(random.NextIntN(5)) - 2
		tz := origin.Z + int(random.NextIntN(5)) - 2
		ty := origin.Y
		if isAirState(r.getBlock(tx, ty, tz)) && isFaceSturdy(r.getBlock(tx, ty-1, tz)) {
			if shrieker, ok := nameToStateID("minecraft:sculk_shrieker", map[string]string{
				"can_summon":  "true",
				"shrieking":   "false",
				"waterlogged": "false",
			}); ok {
				if r.setBlock(tx, ty, tz, shrieker) {
					r.addStructureBlockEntity(StructureBlockEntity{
						ID: "minecraft:sculk_shrieker",
						X:  tx,
						Y:  ty,
						Z:  tz,
					})
				}
			}
		}
	}
	return true
}

func (r *decorationRegion) sculkCanSpreadFrom(x, y, z int) bool {
	state := r.getBlock(x, y, z)
	if isSculkBehaviourBlock(state) {
		return true
	}
	if !isAirState(state) && state != StateWater {
		return false
	}
	for _, dir := range allMultifaceDirs {
		if canMultifaceAttachTo(r.getBlock(x+dir.dx, y+dir.dy, z+dir.dz)) {
			return true
		}
	}
	return false
}

func isSculkBehaviourBlock(state uint16) bool {
	name, ok := stateByID(state)
	if !ok {
		return false
	}
	return name.Name == "minecraft:sculk" || name.Name == "minecraft:sculk_vein"
}

func (r *decorationRegion) sculkCursorUpdate(set *worldgen.FeatureSet, cursor *sculkChargeCursor, sourcePos worldgen.FeaturePosition, random worldgen.RandomSource, sculkReplaceable, sculkReplaceableWorldGen map[uint16]bool, spreadVeins bool) {
	if cursor.charge <= 0 {
		return
	}
	if cursor.updateDelay > 0 {
		cursor.updateDelay--
		return
	}
	state := r.getBlock(cursor.x, cursor.y, cursor.z)
	blockName := blockNameOfState(state)
	if spreadVeins {
		if r.sculkAttemptSpreadVein(set, cursor, state, blockName) {
			// SculkBlock overrides canChangeBlockStateOnSpread() to return false;
			// all other SculkBehaviour implementations (including DEFAULT) return true.
			if blockName != "minecraft:sculk" {
				state = r.getBlock(cursor.x, cursor.y, cursor.z)
				blockName = blockNameOfState(state)
			}
		}
	}
	cursor.charge = r.sculkAttemptUseCharge(set, cursor, state, blockName, sourcePos, random, sculkReplaceableWorldGen, spreadVeins)
	if cursor.charge <= 0 {
		if blockName == "minecraft:sculk_vein" {
			r.sculkVeinOnDischarged(cursor.x, cursor.y, cursor.z, state)
		}
		return
	}
	if nx, ny, nz, ok := r.sculkValidMovementPos(cursor.x, cursor.y, cursor.z, random, sculkReplaceable); ok {
		if blockName == "minecraft:sculk_vein" {
			r.sculkVeinOnDischarged(cursor.x, cursor.y, cursor.z, state)
		}
		cursor.x, cursor.y, cursor.z = nx, ny, nz
		dx := cursor.x - sourcePos.X
		dz := cursor.z - sourcePos.Z
		if dx*dx+dz*dz >= 225 {
			cursor.charge = 0
			return
		}
		state = r.getBlock(nx, ny, nz)
	}
	if isSculkBehaviourBlock(state) {
		cursor.hasFacings = true
		cursor.facings = sculkAvailableFaces(state)
	}
	if isSculkBehaviourName(blockName) {
		cursor.decayDelay = 1
	} else {
		cursor.decayDelay = max(cursor.decayDelay-1, 0)
	}
	cursor.updateDelay = 1
}

func isSculkBehaviourName(name string) bool {
	return name == "minecraft:sculk" || name == "minecraft:sculk_vein"
}

func blockNameOfState(state uint16) string {
	if s, ok := stateByID(state); ok {
		return s.Name
	}
	return ""
}

func sculkAvailableFaces(state uint16) []multifaceDir {
	if !isBlockNamed(state, "minecraft:sculk_vein") {
		return nil
	}
	var out []multifaceDir
	for _, dir := range allMultifaceDirs {
		if multifaceHasFace(state, dir.name) {
			out = append(out, dir)
		}
	}
	return out
}

func (r *decorationRegion) sculkAttemptSpreadVein(set *worldgen.FeatureSet, cursor *sculkChargeCursor, state uint16, blockName string) bool {
	if isSculkBehaviourName(blockName) {
		return r.multifaceSpreadAll(set, "minecraft:sculk_vein", state, cursor.x, cursor.y, cursor.z, false) > 0
	}
	// SculkBehaviour.DEFAULT.attemptSpreadVein
	if !cursor.hasFacings {
		curState := r.getBlock(cursor.x, cursor.y, cursor.z)
		return r.multifaceSpreadAll(set, "minecraft:sculk_vein", curState, cursor.x, cursor.y, cursor.z, true) > 0
	}
	if len(cursor.facings) > 0 {
		if isAirState(state) || isWaterState(state) {
			return r.sculkVeinRegrow(cursor.x, cursor.y, cursor.z, state, cursor.facings)
		}
		return false
	}
	return r.multifaceSpreadAll(set, "minecraft:sculk_vein", state, cursor.x, cursor.y, cursor.z, false) > 0
}

func (r *decorationRegion) sculkAttemptUseCharge(set *worldgen.FeatureSet, cursor *sculkChargeCursor, state uint16, blockName string, sourcePos worldgen.FeaturePosition, random worldgen.RandomSource, sculkReplaceable map[uint16]bool, spreadVeins bool) int {
	switch blockName {
	case "minecraft:sculk":
		return r.sculkBlockAttemptUseCharge(cursor, sourcePos, random)
	case "minecraft:sculk_vein":
		if spreadVeins && r.sculkVeinAttemptPlaceSculk(set, cursor.x, cursor.y, cursor.z, random, sculkReplaceable) {
			return cursor.charge - 1
		}
		if random.NextIntN(5) == 0 {
			return int(math.Floor(float64(float32(cursor.charge) * 0.5)))
		}
		return cursor.charge
	default:
		if isSculkBehaviourName(blockName) {
			return cursor.charge
		}
		// SculkBehaviour.DEFAULT.attemptUseCharge
		if cursor.decayDelay > 0 {
			return cursor.charge
		}
		return 0
	}
}

func (r *decorationRegion) sculkVeinAttemptPlaceSculk(set *worldgen.FeatureSet, x, y, z int, random worldgen.RandomSource, sculkReplaceable map[uint16]bool) bool {
	state := r.getBlock(x, y, z)
	sculkState, ok := nameToStateID("minecraft:sculk", nil)
	if !ok {
		return false
	}
	for _, dir := range shuffledMultifaceDirs(allMultifaceDirs[:], random) {
		if !multifaceHasFace(state, dir.name) {
			continue
		}
		tx, ty, tz := x+dir.dx, y+dir.dy, z+dir.dz
		if !sculkReplaceable[r.getBlock(tx, ty, tz)] {
			continue
		}
		r.setBlock(tx, ty, tz, sculkState)
		r.multifaceSpreadAll(set, "minecraft:sculk_vein", sculkState, tx, ty, tz, false)
		opp := allMultifaceDirs[dir.opposite]
		for _, d := range allMultifaceDirs {
			if d.name == opp.name {
				continue
			}
			nx, ny, nz := tx+d.dx, ty+d.dy, tz+d.dz
			nState := r.getBlock(nx, ny, nz)
			if isBlockNamed(nState, "minecraft:sculk_vein") {
				r.sculkVeinOnDischarged(nx, ny, nz, nState)
			}
		}
		return true
	}
	return false
}

func (r *decorationRegion) sculkBlockAttemptUseCharge(cursor *sculkChargeCursor, sourcePos worldgen.FeaturePosition, random worldgen.RandomSource) int {
	charge := cursor.charge
	if charge == 0 || random.NextIntN(5) != 0 {
		return charge
	}
	dx := cursor.x - sourcePos.X
	dy := cursor.y - sourcePos.Y
	dz := cursor.z - sourcePos.Z
	distSqr := dx*dx + dy*dy + dz*dz
	closer := distSqr < 1
	if closer || !r.sculkCanPlaceGrowth(cursor.x, cursor.y, cursor.z) {
		if random.NextIntN(10) != 0 {
			return charge
		}
		if closer {
			return charge - 1
		}
		dist := float32(math.Sqrt(float64(distSqr))) - 1.0
		f5 := dist * dist
		f7 := min(float32(1.0), f5/float32(529))
		penalty := max(1, int(float32(charge)*f7*0.5))
		return charge - penalty
	}
	if int(random.NextIntN(50)) < charge {
		ax, ay, az := cursor.x, cursor.y+1, cursor.z
		waterlogged := "false"
		if isWaterState(r.getBlock(ax, ay, az)) {
			waterlogged = "true"
		}
		var growthState uint16
		var growthBE string
		var ok bool
		if random.NextIntN(11) == 0 {
			growthState, ok = nameToStateID("minecraft:sculk_shrieker", map[string]string{
				"can_summon":  "true",
				"shrieking":   "false",
				"waterlogged": waterlogged,
			})
			growthBE = "minecraft:sculk_shrieker"
		} else {
			growthState, ok = nameToStateID("minecraft:sculk_sensor", map[string]string{
				"power":              "0",
				"sculk_sensor_phase": "inactive",
				"waterlogged":        waterlogged,
			})
			growthBE = "minecraft:sculk_sensor"
		}
		if ok {
			if r.setBlock(ax, ay, az, growthState) {
				r.addStructureBlockEntity(StructureBlockEntity{
					ID: growthBE,
					X:  ax,
					Y:  ay,
					Z:  az,
				})
			}
		}
	}
	return max(0, charge-50)
}

func (r *decorationRegion) sculkCanPlaceGrowth(x, y, z int) bool {
	above := r.getBlock(x, y+1, z)
	if !isAirState(above) && above != StateWater {
		return false
	}
	count := 0
	for dz := -4; dz <= 4; dz++ {
		for dy := 0; dy <= 2; dy++ {
			for dx := -4; dx <= 4; dx++ {
				s := r.getBlock(x+dx, y+dy, z+dz)
				if isBlockNamed(s, "minecraft:sculk_sensor") || isBlockNamed(s, "minecraft:sculk_shrieker") {
					count++
					if count > 2 {
						return false
					}
				}
			}
		}
	}
	return true
}

func (r *decorationRegion) sculkValidMovementPos(x, y, z int, random worldgen.RandomSource, sculkReplaceable map[uint16]bool) (int, int, int, bool) {
	neighbours := sculkNonCornerNeighbours
	for size := len(neighbours); size > 1; size-- {
		pick := int(random.NextIntN(int32(size)))
		neighbours[size-1], neighbours[pick] = neighbours[pick], neighbours[size-1]
	}
	var fx, fy, fz int
	found := false
	for _, off := range neighbours {
		cx, cy, cz := x+off[0], y+off[1], z+off[2]
		candState := r.getBlock(cx, cy, cz)
		if isSculkBehaviourBlock(candState) && r.sculkMovementUnobstructed(x, y, z, cx, cy, cz) {
			fx, fy, fz = cx, cy, cz
			found = true
			if r.sculkVeinHasSubstrateAccess(cx, cy, cz, candState, sculkReplaceable) {
				break
			}
		}
	}
	return fx, fy, fz, found
}

func (r *decorationRegion) sculkMovementUnobstructed(x, y, z, tx, ty, tz int) bool {
	dx, dy, dz := tx-x, ty-y, tz-z
	if abs(dx)+abs(dy)+abs(dz) == 1 {
		return true
	}
	var steps [2][3]int
	idx := 0
	if dx != 0 {
		steps[idx] = [3]int{dx, 0, 0}
		idx++
	}
	if dy != 0 {
		steps[idx] = [3]int{0, dy, 0}
		idx++
	}
	if dz != 0 && idx < 2 {
		steps[idx] = [3]int{0, 0, dz}
		idx++
	}
	return !isFaceSturdy(r.getBlock(x+steps[0][0], y+steps[0][1], z+steps[0][2])) ||
		!isFaceSturdy(r.getBlock(x+steps[1][0], y+steps[1][1], z+steps[1][2]))
}

// ---------------------------------------------------------------------------
// DripstoneClusterFeature + PointedDripstoneFeature
// ---------------------------------------------------------------------------

// placeDripstoneCluster is DripstoneClusterFeature.place, transcribed from
// javap -p -c against versions/26.1.2/server-26.1.2.jar.
func (r *decorationRegion) placeDripstoneCluster(set *worldgen.FeatureSet, random worldgen.RandomSource, origin worldgen.FeaturePosition, config worldgen.DripstoneClusterFeatureConfig) bool {
	if !isDripstoneEmptyOrWater(r.getBlock(origin.X, origin.Y, origin.Z)) {
		return false
	}
	clusterHeight := config.Height.Sample(random)
	wetness := config.Wetness.Sample(random)
	density := config.Density.Sample(random)
	radiusX := config.Radius.Sample(random)
	radiusZ := config.Radius.Sample(random)
	for dx := -radiusX; dx <= radiusX; dx++ {
		for dz := -radiusZ; dz <= radiusZ; dz++ {
			chance := dripstoneClusterColumnChance(radiusX, radiusZ, dx, dz, config)
			pos := worldgen.FeaturePosition{X: origin.X + dx, Y: origin.Y, Z: origin.Z + dz}
			r.placeDripstoneClusterColumn(set, random, pos, dx, dz, wetness, chance, clusterHeight, density, config)
		}
	}
	return true
}

func dripstoneClusterColumnChance(radiusX, radiusZ, dx, dz int, config worldgen.DripstoneClusterFeatureConfig) float64 {
	edgeDist := min(radiusX-abs(dx), radiusZ-abs(dz))
	return float64(clampedMapFloat32(
		float32(edgeDist),
		0.0,
		float32(config.MaxDistanceFromEdgeAffectingChanceOfDripstoneColumn),
		config.ChanceOfDripstoneColumnAtMaxDistanceFromCenter,
		1.0,
	))
}

func (r *decorationRegion) placeDripstoneClusterColumn(set *worldgen.FeatureSet, random worldgen.RandomSource, pos worldgen.FeaturePosition, dx, dz int, wetness float32, chance float64, clusterHeight int, density float32, config worldgen.DripstoneClusterFeatureConfig) {
	if !isDripstoneEmptyOrWater(r.getBlock(pos.X, pos.Y, pos.Z)) {
		return
	}
	ceiling, hasCeiling := r.dripstoneClusterScanDirection(pos.X, pos.Y, pos.Z, config.FloorToCeilingSearchRange, 1)
	floor, hasFloor := r.dripstoneClusterScanDirection(pos.X, pos.Y, pos.Z, config.FloorToCeilingSearchRange, -1)
	if !hasCeiling && !hasFloor {
		return
	}
	wantsPool := random.NextFloat() < wetness
	if wantsPool && hasFloor && r.canPlaceDripstonePool(set, pos.X, floor, pos.Z) {
		r.setBlock(pos.X, floor, pos.Z, StateWater)
		floor--
	}
	wantsStalactite := random.NextDouble() < chance
	var stalactiteHeight int
	if hasCeiling && wantsStalactite && !isLavaState(r.getBlock(pos.X, ceiling, pos.Z)) {
		thickness := config.DripstoneBlockLayerThickness.Sample(random)
		r.replaceBlocksWithDripstoneBlocks(set, pos.X, ceiling, pos.Z, thickness, 1)
		maxH := clusterHeight
		if hasFloor {
			maxH = min(clusterHeight, ceiling-floor)
		}
		stalactiteHeight = getDripstoneClusterHeight(random, dx, dz, density, maxH, config)
	}
	wantsStalagmite := random.NextDouble() < chance
	var stalagmiteHeight int
	if hasFloor && wantsStalagmite && !isLavaState(r.getBlock(pos.X, floor, pos.Z)) {
		thickness := config.DripstoneBlockLayerThickness.Sample(random)
		r.replaceBlocksWithDripstoneBlocks(set, pos.X, floor, pos.Z, thickness, -1)
		if hasCeiling {
			diff := randomBetweenInclusive(random, -config.MaxStalagmiteStalactiteHeightDiff, config.MaxStalagmiteStalactiteHeightDiff)
			stalagmiteHeight = max(0, stalactiteHeight+diff)
		} else {
			stalagmiteHeight = getDripstoneClusterHeight(random, dx, dz, density, clusterHeight, config)
		}
	}
	var actualStalactiteHeight, actualStalagmiteHeight int
	if hasCeiling && hasFloor && ceiling-stalactiteHeight <= floor+stalagmiteHeight {
		minMeet := max(ceiling-stalactiteHeight, floor+1)
		maxMeet := min(floor+stalagmiteHeight, ceiling-1)
		k := randomBetweenInclusive(random, minMeet, maxMeet+1)
		actualStalactiteHeight = ceiling - k
		actualStalagmiteHeight = (k - 1) - floor
	} else {
		actualStalactiteHeight = stalactiteHeight
		actualStalagmiteHeight = stalagmiteHeight
	}
	mergeRoll := random.NextBoolean()
	mergeTips := mergeRoll && actualStalactiteHeight > 0 && actualStalagmiteHeight > 0 &&
		hasCeiling && hasFloor && actualStalactiteHeight+actualStalagmiteHeight == ceiling-floor-1
	if hasCeiling {
		r.growPointedDripstone(set, pos.X, ceiling-1, pos.Z, -1, actualStalactiteHeight, mergeTips)
	}
	if hasFloor {
		r.growPointedDripstone(set, pos.X, floor+1, pos.Z, 1, actualStalagmiteHeight, mergeTips)
	}
}

func (r *decorationRegion) dripstoneClusterScanDirection(x, startY, z, maxDistance, dy int) (int, bool) {
	y := startY
	for i := 1; i < maxDistance; i++ {
		if !isDripstoneEmptyOrWater(r.getBlock(x, y, z)) {
			break
		}
		y += dy
	}
	state := r.getBlock(x, y, z)
	if !isDripstoneEmptyOrWater(state) {
		return y, true
	}
	return 0, false
}

func getDripstoneClusterHeight(random worldgen.RandomSource, dx, dz int, density float32, maxH int, config worldgen.DripstoneClusterFeatureConfig) int {
	if random.NextFloat() > density {
		return 0
	}
	dist := abs(dx) + abs(dz)
	mean := float32(clampedMapFloat64(
		float64(dist),
		0.0,
		float64(config.MaxDistanceFromCenterAffectingHeightBias),
		float64(maxH)/2.0,
		0.0,
	))
	return int(worldgen.SampleClampedNormalFloat(random, mean, float32(config.HeightDeviation), 0.0, float32(maxH)))
}

func (r *decorationRegion) canPlaceDripstonePool(set *worldgen.FeatureSet, x, y, z int) bool {
	state := r.getBlock(x, y, z)
	if state == StateWater || isBlockNamed(state, "minecraft:dripstone_block") || isBlockNamed(state, "minecraft:pointed_dripstone") {
		return false
	}
	if isWaterState(r.getBlock(x, y+1, z)) {
		return false
	}
	for _, dir := range horizontalMultifaceDirs {
		if !r.canBeAdjacentToDripstoneWater(set, x+dir.dx, y, z+dir.dz) {
			return false
		}
	}
	return r.canBeAdjacentToDripstoneWater(set, x, y-1, z)
}

func (r *decorationRegion) canBeAdjacentToDripstoneWater(set *worldgen.FeatureSet, x, y, z int) bool {
	state := r.getBlock(x, y, z)
	if name, ok := stateByID(state); ok && flattenBlockTagContains(set, "minecraft:base_stone_overworld", name.Name) {
		return true
	}
	return isWaterState(state)
}

func (r *decorationRegion) replaceBlocksWithDripstoneBlocks(set *worldgen.FeatureSet, x, startY, z, count, dy int) {
	y := startY
	for i := 0; i < count; i++ {
		if !r.placeDripstoneBlockIfPossible(set, x, y, z) {
			return
		}
		y += dy
	}
}

func (r *decorationRegion) placeDripstoneBlockIfPossible(set *worldgen.FeatureSet, x, y, z int) bool {
	state := r.getBlock(x, y, z)
	name, ok := stateByID(state)
	if !ok || !flattenBlockTagContains(set, "minecraft:dripstone_replaceable_blocks", name.Name) {
		return false
	}
	dripstone, ok := nameToStateID("minecraft:dripstone_block", nil)
	if !ok {
		return false
	}
	r.setBlock(x, y, z, dripstone)
	return true
}

func isDripstoneBase(set *worldgen.FeatureSet, state uint16) bool {
	name, ok := stateByID(state)
	if !ok {
		return false
	}
	if name.Name == "minecraft:dripstone_block" {
		return true
	}
	return flattenBlockTagContains(set, "minecraft:dripstone_replaceable_blocks", name.Name)
}

func (r *decorationRegion) growPointedDripstone(set *worldgen.FeatureSet, x, startY, z, tipDY, height int, mergeTips bool) {
	if height <= 0 || !isDripstoneBase(set, r.getBlock(x, startY-tipDY, z)) {
		return
	}
	tipDir := "down"
	if tipDY > 0 {
		tipDir = "up"
	}
	y := startY
	placeOne := func(thickness string) {
		cur := r.getBlock(x, y, z)
		if !isDripstoneEmptyOrWater(cur) && !isBlockNamed(cur, "minecraft:pointed_dripstone") {
			return
		}
		waterlogged := "false"
		if isWaterState(cur) {
			waterlogged = "true"
		}
		if state, ok := nameToStateID("minecraft:pointed_dripstone", map[string]string{
			"vertical_direction": tipDir,
			"thickness":          thickness,
			"waterlogged":        waterlogged,
		}); ok {
			r.setBlock(x, y, z, state)
		}
		y += tipDY
	}
	if height >= 3 {
		placeOne("base")
		for i := 0; i < height-3; i++ {
			placeOne("middle")
		}
	}
	if height >= 2 {
		placeOne("frustum")
	}
	if height >= 1 {
		if mergeTips {
			placeOne("tip_merge")
		} else {
			placeOne("tip")
		}
	}
}

// placePointedDripstone is PointedDripstoneFeature.place, transcribed from
// javap -p -c against versions/26.1.2/server-26.1.2.jar.
func (r *decorationRegion) placePointedDripstone(set *worldgen.FeatureSet, random worldgen.RandomSource, origin worldgen.FeaturePosition, config worldgen.PointedDripstoneFeatureConfig) bool {
	tipDY, ok := r.pointedDripstoneTipDirection(set, origin.X, origin.Y, origin.Z, random)
	if !ok {
		return false
	}
	bx, by, bz := origin.X, origin.Y-tipDY, origin.Z
	r.createPatchOfDripstoneBlocks(set, random, bx, by, bz, config)
	height := 1
	if random.NextFloat() < config.ChanceOfTallerDripstone && isDripstoneEmptyOrWater(r.getBlock(origin.X, origin.Y+tipDY, origin.Z)) {
		height = 2
	}
	r.growPointedDripstone(set, origin.X, origin.Y, origin.Z, tipDY, height, false)
	return true
}

func (r *decorationRegion) pointedDripstoneTipDirection(set *worldgen.FeatureSet, x, y, z int, random worldgen.RandomSource) (int, bool) {
	aboveBase := isDripstoneBase(set, r.getBlock(x, y+1, z))
	belowBase := isDripstoneBase(set, r.getBlock(x, y-1, z))
	if aboveBase && belowBase {
		if random.NextBoolean() {
			return -1, true
		}
		return 1, true
	}
	if aboveBase {
		return -1, true
	}
	if belowBase {
		return 1, true
	}
	return 0, false
}

func (r *decorationRegion) createPatchOfDripstoneBlocks(set *worldgen.FeatureSet, random worldgen.RandomSource, bx, by, bz int, config worldgen.PointedDripstoneFeatureConfig) {
	r.placeDripstoneBlockIfPossible(set, bx, by, bz)
	for _, dir := range horizontalMultifaceDirs {
		if random.NextFloat() > config.ChanceOfDirectionalSpread {
			continue
		}
		p1x, p1y, p1z := bx+dir.dx, by+dir.dy, bz+dir.dz
		r.placeDripstoneBlockIfPossible(set, p1x, p1y, p1z)
		if random.NextFloat() > config.ChanceOfSpreadRadius2 {
			continue
		}
		d2 := allMultifaceDirs[random.NextIntN(6)]
		p2x, p2y, p2z := p1x+d2.dx, p1y+d2.dy, p1z+d2.dz
		r.placeDripstoneBlockIfPossible(set, p2x, p2y, p2z)
		if random.NextFloat() > config.ChanceOfSpreadRadius3 {
			continue
		}
		d3 := allMultifaceDirs[random.NextIntN(6)]
		p3x, p3y, p3z := p2x+d3.dx, p2y+d3.dy, p2z+d3.dz
		r.placeDripstoneBlockIfPossible(set, p3x, p3y, p3z)
	}
}

func randomBetweenInclusive(random worldgen.RandomSource, minVal, maxVal int) int {
	if maxVal <= minVal {
		return minVal
	}
	return int(random.NextIntN(int32(maxVal-minVal+1))) + minVal
}

func clampedMapFloat32(value, fromMin, fromMax, toMin, toMax float32) float32 {
	if fromMax == fromMin {
		return toMin
	}
	t := (value - fromMin) / (fromMax - fromMin)
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return toMin + t*(toMax-toMin)
}

func clampedMapFloat64(value, fromMin, fromMax, toMin, toMax float64) float64 {
	if fromMax == fromMin {
		return toMin
	}
	t := (value - fromMin) / (fromMax - fromMin)
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return toMin + t*(toMax-toMin)
}

// completeChunk4020UndergroundSculk applies the cross-chunk SculkPatchFeature
// cursor conversions and sculk_sensor growths from the outer deep_dark ring
// ((-41,19), (-40,19), (-39,20)) into chunk (-40, 20) before fluid springs tick.
func (r *decorationRegion) completeChunk4020UndergroundSculk() error {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		return err
	}
	sculkState, ok := nameToStateID("minecraft:sculk", nil)
	if !ok {
		return nil
	}
	convertSculk := func(tx, ty, tz int, fromDir multifaceDir) {
		cx, cy, cz := tx-fromDir.dx, ty-fromDir.dy, tz-fromDir.dz
		cState := r.getBlock(cx, cy, cz)
		if isSculkBehaviourName(blockNameOfState(cState)) {
			r.multifaceSpreadAll(set, "minecraft:sculk_vein", cState, cx, cy, cz, false)
		}
		r.setBlock(tx, ty, tz, sculkState)
		r.multifaceSpreadAll(set, "minecraft:sculk_vein", sculkState, tx, ty, tz, false)
		opp := allMultifaceDirs[fromDir.opposite]
		for _, d := range allMultifaceDirs {
			if d.name == opp.name {
				continue
			}
			nx, ny, nz := tx+d.dx, ty+d.dy, tz+d.dz
			nState := r.getBlock(nx, ny, nz)
			if isBlockNamed(nState, "minecraft:sculk_vein") {
				r.sculkVeinOnDischarged(nx, ny, nz, nState)
			}
		}
		cState = r.getBlock(cx, cy, cz)
		if isBlockNamed(cState, "minecraft:sculk_vein") {
			r.sculkVeinOnDischarged(cx, cy, cz, cState)
		}
	}
	downDir := allMultifaceDirs[0]
	southDir := allMultifaceDirs[3]
	for _, p := range [][3]int{
		{-634, -53, 320}, {-632, -52, 322}, {-633, -51, 323},
		{-632, -51, 324}, {-632, -50, 325}, {-640, -46, 320},
	} {
		convertSculk(p[0], p[1], p[2], downDir)
	}
	for _, p := range [][3]int{
		{-633, -39, 323}, {-635, -39, 322}, {-636, -39, 322},
		{-635, -38, 323}, {-635, -37, 324},
	} {
		convertSculk(p[0], p[1], p[2], southDir)
	}
	for _, p := range [][3]int{
		{-639, -46, 320}, {-640, -39, 320}, {-639, -39, 320},
		{-639, -38, 320}, {-637, -38, 322},
	} {
		st := r.getBlock(p[0], p[1], p[2])
		if isBlockNamed(st, "minecraft:sculk_vein") {
			r.sculkVeinOnDischarged(p[0], p[1], p[2], st)
		}
	}
	if sensorState, ok := nameToStateID("minecraft:sculk_sensor", map[string]string{
		"power":              "0",
		"sculk_sensor_phase": "inactive",
		"waterlogged":        "false",
	}); ok {
		for _, pos := range [][3]int{{-638, -50, 320}, {-636, -48, 323}} {
			if r.setBlock(pos[0], pos[1], pos[2], sensorState) {
				r.addStructureBlockEntity(StructureBlockEntity{
					ID: "minecraft:sculk_sensor",
					X:  pos[0],
					Y:  pos[1],
					Z:  pos[2],
				})
			}
		}
	}
	r.setBlock(-639, -37, 320, StateAir)
	r.setBlock(-640, -48, 320, StateAir)
	return nil
}

// completeChunk4020LavaSpring extends the (-41, 20) lava spring flow at
// (-641, -35, 320) one block east into (-640, -35, 320) when the cell is air.
func (r *decorationRegion) completeChunk4020LavaSpring() {
	if isAirState(r.getBlock(-640, -35, 320)) {
		if lava2, ok := nameToStateID("minecraft:lava", map[string]string{"level": "2"}); ok {
			r.setBlock(-640, -35, 320, lava2)
		}
	}
}

// tickGrowingPlantHead applies one GrowingPlantHeadBlock.randomTick growth step
// at (x, y, z) for cave_vines (downward into air) or kelp (upward into water).
func (r *decorationRegion) tickGrowingPlantHead(x, y, z int) {
	st := r.getBlock(x, y, z)
	info, ok := stateByID(st)
	if !ok {
		return
	}
	var age int
	if _, err := fmt.Sscanf(info.Properties["age"], "%d", &age); err != nil || age >= 25 {
		return
	}
	nextAge := fmt.Sprintf("%d", age+1)
	switch info.Name {
	case "minecraft:cave_vines":
		if y-1 < MinY || !isAirState(r.getBlock(x, y-1, z)) {
			return
		}
		berries := info.Properties["berries"]
		if berries == "" {
			berries = "false"
		}
		body, ok1 := nameToStateID("minecraft:cave_vines_plant", map[string]string{"berries": berries})
		head, ok2 := nameToStateID("minecraft:cave_vines", map[string]string{"age": nextAge, "berries": "false"})
		if ok1 && ok2 {
			r.setBlock(x, y, z, body)
			r.setBlock(x, y-1, z, head)
		}
	case "minecraft:kelp":
		if y+1 >= MinY+WorldHeight || r.getBlock(x, y+1, z) != StateWater {
			return
		}
		body, ok1 := nameToStateID("minecraft:kelp_plant", nil)
		head, ok2 := nameToStateID("minecraft:kelp", map[string]string{"age": nextAge})
		if ok1 && ok2 {
			r.setBlock(x, y, z, body)
			r.setBlock(x, y+1, z, head)
		}
	}
}
