package world

import "regionio/internal/worldgen"

// multifaceDir is one of Direction.values() in declaration order:
// DOWN, UP, NORTH, SOUTH, WEST, EAST.
type multifaceDir struct {
	dx, dy, dz int
	name       string
	axis       uint8 // 0=x, 1=y, 2=z
	opposite   int   // index into allMultifaceDirs
}

var (
	multifaceDirDown  = multifaceDir{0, -1, 0, "down", 1, 1}
	multifaceDirUp    = multifaceDir{0, 1, 0, "up", 1, 0}
	multifaceDirNorth = multifaceDir{0, 0, -1, "north", 2, 3}
	multifaceDirSouth = multifaceDir{0, 0, 1, "south", 2, 2}
	multifaceDirWest  = multifaceDir{-1, 0, 0, "west", 0, 5}
	multifaceDirEast  = multifaceDir{1, 0, 0, "east", 0, 4}

	// allMultifaceDirs is Direction.values() order, used by Direction.allShuffled
	// and VinesFeature.place.
	allMultifaceDirs = [6]multifaceDir{
		multifaceDirDown,
		multifaceDirUp,
		multifaceDirNorth,
		multifaceDirSouth,
		multifaceDirWest,
		multifaceDirEast,
	}

	// horizontalMultifaceDirs is Direction.Plane.HORIZONTAL order (NORTH, EAST,
	// SOUTH, WEST), used when MultifaceGrowthConfiguration builds validDirections.
	horizontalMultifaceDirs = [4]multifaceDir{
		multifaceDirNorth,
		multifaceDirEast,
		multifaceDirSouth,
		multifaceDirWest,
	}
)

// placeMultifaceGrowth is MultifaceGrowthFeature.place, transcribed from javap -p -c
// against versions/26.1.2/server-26.1.2.jar for MultifaceGrowthFeature,
// MultifaceGrowthConfiguration, MultifaceBlock, and MultifaceSpreader.
//
// Two bytecode details are load-bearing for stream and block parity:
//   - For each searchDirection, getShuffledDirectionsExcept(random, opposite) runs
//     BEFORE the inner searchRange loop checks whether origin+searchDirection is air
//     or water, so its shuffle draws happen unconditionally per searchDirection until
//     a placement succeeds.
//   - Inside the inner searchRange loop (offsets 143-151), vanilla calls
//     mutable.setWithOffset(origin, searchDirection) rather than mutable.move, so
//     every iteration inspects the cell 1 step from origin in searchDirection.
func (r *decorationRegion) placeMultifaceGrowth(set *worldgen.FeatureSet, random worldgen.RandomSource, origin worldgen.FeaturePosition, config worldgen.MultifaceGrowthFeatureConfig) bool {
	originState := r.getBlock(origin.X, origin.Y, origin.Z)
	if !isMultifaceAirOrWater(originState) {
		return false
	}
	validDirs := multifaceValidDirections(config)
	shuffledDirs := shuffledMultifaceDirs(validDirs, random)
	canBePlacedOn := blockSetStateIDs(set, config.CanBePlacedOn)
	if r.placeMultifaceGrowthIfPossible(set, origin.X, origin.Y, origin.Z, originState, config, canBePlacedOn, random, shuffledDirs) {
		return true
	}
	for _, searchDir := range shuffledDirs {
		testDirs := shuffledMultifaceDirsExcept(validDirs, random, allMultifaceDirs[searchDir.opposite])
		for i := 0; i < config.SearchRange; i++ {
			px := origin.X + searchDir.dx
			py := origin.Y + searchDir.dy
			pz := origin.Z + searchDir.dz
			state := r.getBlock(px, py, pz)
			if !isMultifaceAirOrWater(state) && !isBlockNamed(state, config.Block) {
				break
			}
			if r.placeMultifaceGrowthIfPossible(set, px, py, pz, state, config, canBePlacedOn, random, testDirs) {
				return true
			}
		}
	}
	return false
}

func multifaceValidDirections(config worldgen.MultifaceGrowthFeatureConfig) []multifaceDir {
	dirs := make([]multifaceDir, 0, 6)
	if config.CanPlaceOnCeiling {
		dirs = append(dirs, multifaceDirUp)
	}
	if config.CanPlaceOnFloor {
		dirs = append(dirs, multifaceDirDown)
	}
	if config.CanPlaceOnWall {
		dirs = append(dirs, horizontalMultifaceDirs[:]...)
	}
	return dirs
}

func shuffledMultifaceDirs(dirs []multifaceDir, random worldgen.RandomSource) []multifaceDir {
	out := make([]multifaceDir, len(dirs))
	copy(out, dirs)
	shuffleMultifaceDirSlice(out, random)
	return out
}

func shuffledMultifaceDirsExcept(dirs []multifaceDir, random worldgen.RandomSource, exclude multifaceDir) []multifaceDir {
	out := make([]multifaceDir, 0, len(dirs))
	for _, d := range dirs {
		if d.name != exclude.name {
			out = append(out, d)
		}
	}
	shuffleMultifaceDirSlice(out, random)
	return out
}

func shuffleMultifaceDirSlice(dirs []multifaceDir, random worldgen.RandomSource) {
	for size := len(dirs); size > 1; size-- {
		pick := int(random.NextIntN(int32(size)))
		dirs[size-1], dirs[pick] = dirs[pick], dirs[size-1]
	}
}

func (r *decorationRegion) placeMultifaceGrowthIfPossible(set *worldgen.FeatureSet, x, y, z int, state uint16, config worldgen.MultifaceGrowthFeatureConfig, canBePlacedOn map[uint16]bool, random worldgen.RandomSource, dirs []multifaceDir) bool {
	for _, dir := range dirs {
		attachState := r.getBlock(x+dir.dx, y+dir.dy, z+dir.dz)
		if canBePlacedOn[attachState] {
			placedState, ok := r.multifaceStateForPlacement(config.Block, state, x, y, z, dir)
			if !ok {
				return false
			}
			r.setBlock(x, y, z, placedState)
			if random.NextFloat() < config.ChanceOfSpreading {
				r.multifaceSpreadFromFaceTowardRandomDirection(set, config.Block, placedState, x, y, z, dir, random)
			}
			return true
		}
	}
	return false
}

// multifaceStateForPlacement mirrors MultifaceBlock.getStateForPlacement:
// returns false when the face is already present on currentState or the block at
// (x+dir) cannot support a multiface attachment.
func (r *decorationRegion) multifaceStateForPlacement(blockName string, currentState uint16, x, y, z int, dir multifaceDir) (uint16, bool) {
	if isBlockNamed(currentState, blockName) && multifaceHasFace(currentState, dir.name) {
		return 0, false
	}
	neighborState := r.getBlock(x+dir.dx, y+dir.dy, z+dir.dz)
	if !canMultifaceAttachTo(neighborState) {
		return 0, false
	}
	props := map[string]string{
		"down":        "false",
		"up":          "false",
		"north":       "false",
		"south":       "false",
		"west":        "false",
		"east":        "false",
		"waterlogged": "false",
	}
	if isBlockNamed(currentState, blockName) {
		if existing, ok := stateByID(currentState); ok {
			for k, v := range existing.Properties {
				props[k] = v
			}
		}
	} else if currentState == StateWater {
		props["waterlogged"] = "true"
	}
	props[dir.name] = "true"
	return nameToStateID(blockName, props)
}

// multifaceSpreadFromFaceTowardRandomDirection mirrors
// MultifaceSpreader.spreadFromFaceTowardRandomDirection: shuffles Direction.values()
// and stops at the first direction that produces a valid spread placement.
func (r *decorationRegion) multifaceSpreadFromFaceTowardRandomDirection(set *worldgen.FeatureSet, blockName string, state uint16, x, y, z int, fromFace multifaceDir, random worldgen.RandomSource) bool {
	shuffled := shuffledMultifaceDirs(allMultifaceDirs[:], random)
	for _, spreadDir := range shuffled {
		if r.multifaceSpreadFromFaceTowardDirection(set, blockName, state, x, y, z, fromFace, spreadDir, false) {
			return true
		}
	}
	return false
}

// multifaceSpreadAll mirrors MultifaceSpreader.spreadAll over either
// [SAME_POSITION] (when sameSpaceOnly is true) or DEFAULT_SPREAD_ORDER
// (SAME_POSITION, SAME_PLANE, WRAP_AROUND).
func (r *decorationRegion) multifaceSpreadAll(set *worldgen.FeatureSet, blockName string, state uint16, x, y, z int, sameSpaceOnly bool) int {
	spreadCount := 0
	for _, fromFace := range allMultifaceDirs {
		canSpread := false
		if isBlockNamed(state, blockName) {
			canSpread = multifaceHasFace(state, fromFace.name)
		} else if blockName == "minecraft:sculk_vein" {
			canSpread = true
		}
		if !canSpread {
			continue
		}
		for _, spreadDir := range allMultifaceDirs {
			if r.multifaceSpreadFromFaceTowardDirection(set, blockName, state, x, y, z, fromFace, spreadDir, sameSpaceOnly) {
				spreadCount++
			}
		}
	}
	return spreadCount
}

// multifaceSpreadFromFaceTowardDirection mirrors
// MultifaceSpreader.spreadFromFaceTowardDirection over DEFAULT_SPREAD_ORDER
// (SAME_POSITION, SAME_PLANE, WRAP_AROUND) or [SAME_POSITION] when sameSpaceOnly is true.
func (r *decorationRegion) multifaceSpreadFromFaceTowardDirection(set *worldgen.FeatureSet, blockName string, state uint16, x, y, z int, fromFace, spreadDir multifaceDir, sameSpaceOnly bool) bool {
	if spreadDir.axis == fromFace.axis {
		return false
	}
	otherValidAsSource := blockName == "minecraft:sculk_vein" && !isBlockNamed(state, blockName)
	if !otherValidAsSource && (!isBlockNamed(state, blockName) || !multifaceHasFace(state, fromFace.name) || multifaceHasFace(state, spreadDir.name)) {
		return false
	}
	candidates := [3]struct {
		x, y, z int
		face    multifaceDir
	}{
		// SAME_POSITION
		{x, y, z, spreadDir},
		// SAME_PLANE
		{x + spreadDir.dx, y + spreadDir.dy, z + spreadDir.dz, fromFace},
		// WRAP_AROUND
		{x + spreadDir.dx + fromFace.dx, y + spreadDir.dy + fromFace.dy, z + spreadDir.dz + fromFace.dz, allMultifaceDirs[spreadDir.opposite]},
	}
	limit := 3
	if sameSpaceOnly {
		limit = 1
	}
	for i := 0; i < limit; i++ {
		c := candidates[i]
		targetState := r.getBlock(c.x, c.y, c.z)
		if !r.multifaceStateCanBeReplaced(set, blockName, x, y, z, c.x, c.y, c.z, c.face, targetState) {
			continue
		}
		placedState, ok := r.multifaceStateForPlacement(blockName, targetState, c.x, c.y, c.z, c.face)
		if !ok {
			continue
		}
		r.setBlock(c.x, c.y, c.z, placedState)
		return true
	}
	return false
}

// sculkVeinRegrow mirrors SculkVeinBlock.regrow.
func (r *decorationRegion) sculkVeinRegrow(x, y, z int, state uint16, facings []multifaceDir) bool {
	props := map[string]string{
		"down":        "false",
		"up":          "false",
		"north":       "false",
		"south":       "false",
		"west":        "false",
		"east":        "false",
		"waterlogged": "false",
	}
	if isWaterState(state) {
		props["waterlogged"] = "true"
	}
	hasAny := false
	for _, dir := range facings {
		if canMultifaceAttachTo(r.getBlock(x+dir.dx, y+dir.dy, z+dir.dz)) {
			props[dir.name] = "true"
			hasAny = true
		}
	}
	if !hasAny {
		return false
	}
	newState, ok := nameToStateID("minecraft:sculk_vein", props)
	if !ok {
		return false
	}
	r.setBlock(x, y, z, newState)
	return true
}

// sculkVeinOnDischarged mirrors SculkVeinBlock.onDischarged.
func (r *decorationRegion) sculkVeinOnDischarged(x, y, z int, state uint16) {
	if !isBlockNamed(state, "minecraft:sculk_vein") {
		return
	}
	existing, ok := stateByID(state)
	if !ok {
		return
	}
	props := make(map[string]string, len(existing.Properties))
	for k, v := range existing.Properties {
		props[k] = v
	}
	anyFace := false
	for _, dir := range allMultifaceDirs {
		if props[dir.name] == "true" {
			if isBlockNamed(r.getBlock(x+dir.dx, y+dir.dy, z+dir.dz), "minecraft:sculk") {
				props[dir.name] = "false"
			} else {
				anyFace = true
			}
		}
	}
	if !anyFace {
		if props["waterlogged"] == "true" {
			r.setBlock(x, y, z, StateWater)
		} else {
			r.setBlock(x, y, z, StateAir)
		}
		return
	}
	if newState, ok := nameToStateID("minecraft:sculk_vein", props); ok {
		r.setBlock(x, y, z, newState)
	}
}

// sculkVeinHasSubstrateAccess mirrors SculkVeinBlock.hasSubstrateAccess.
func (r *decorationRegion) sculkVeinHasSubstrateAccess(x, y, z int, state uint16, sculkReplaceable map[uint16]bool) bool {
	if !isBlockNamed(state, "minecraft:sculk_vein") {
		return false
	}
	for _, dir := range allMultifaceDirs {
		if multifaceHasFace(state, dir.name) && sculkReplaceable[r.getBlock(x+dir.dx, y+dir.dy, z+dir.dz)] {
			return true
		}
	}
	return false
}

// multifaceStateCanBeReplaced mirrors DefaultSpreaderConfig.stateCanBeReplaced and
// SculkVeinBlock$SculkVeinSpreaderConfig.stateCanBeReplaced.
func (r *decorationRegion) multifaceStateCanBeReplaced(set *worldgen.FeatureSet, blockName string, srcX, srcY, srcZ, dstX, dstY, dstZ int, face multifaceDir, targetState uint16) bool {
	if blockName == "minecraft:sculk_vein" {
		supportState := r.getBlock(dstX+face.dx, dstY+face.dy, dstZ+face.dz)
		if isBlockNamed(supportState, "minecraft:sculk") ||
			isBlockNamed(supportState, "minecraft:sculk_catalyst") ||
			isBlockNamed(supportState, "minecraft:moving_piston") {
			return false
		}
		if abs(dstX-srcX)+abs(dstY-srcY)+abs(dstZ-srcZ) == 2 {
			opp := allMultifaceDirs[face.opposite]
			if isFaceSturdy(r.getBlock(srcX+opp.dx, srcY+opp.dy, srcZ+opp.dz)) {
				return false
			}
		}
		if isLavaState(targetState) {
			return false
		}
		if name, ok := stateByID(targetState); ok {
			if flattenBlockTagContains(set, "minecraft:fire", name.Name) {
				return false
			}
			if flattenBlockTagContains(set, "minecraft:replaceable", name.Name) {
				return true
			}
		}
	}
	return isAirState(targetState) || isBlockNamed(targetState, blockName) || targetState == StateWater
}

// placeVines is VinesFeature.place: if the origin is empty, iterates
// Direction.values() skipping DOWN and places a single-face vine attached to the
// first neighbour that satisfies MultifaceBlock.canAttachTo.
func (r *decorationRegion) placeVines(origin worldgen.FeaturePosition) bool {
	if !isAirState(r.getBlock(origin.X, origin.Y, origin.Z)) {
		return false
	}
	for _, dir := range allMultifaceDirs {
		if dir.name == "down" {
			continue
		}
		if canMultifaceAttachTo(r.getBlock(origin.X+dir.dx, origin.Y+dir.dy, origin.Z+dir.dz)) {
			props := map[string]string{
				"up":    "false",
				"north": "false",
				"south": "false",
				"west":  "false",
				"east":  "false",
			}
			props[dir.name] = "true"
			state, ok := nameToStateID("minecraft:vine", props)
			if !ok {
				return false
			}
			return r.setBlock(origin.X, origin.Y, origin.Z, state)
		}
	}
	return false
}

// canMultifaceAttachTo mirrors MultifaceBlock.canAttachTo: full support shape
// (isFaceSturdy) or full collision shape (which adds LeavesBlock to the sturdy cubes).
func canMultifaceAttachTo(state uint16) bool {
	return isFaceSturdy(state) || stateFlags(state)&flagLeaves != 0
}

func isMultifaceAirOrWater(state uint16) bool {
	if isAirState(state) {
		return true
	}
	return isBlockNamed(state, "minecraft:water")
}

func isBlockNamed(state uint16, want string) bool {
	name, ok := stateByID(state)
	return ok && name.Name == want
}

func multifaceHasFace(state uint16, face string) bool {
	name, ok := stateByID(state)
	return ok && name.Properties[face] == "true"
}
