package world

import (
	"fmt"
	"sort"
	"strconv"

	"regionio/internal/worldgen"
)

// vegetationStage is the index of the VEGETATION_DECORATION step in the vanilla
// feature schedule, which is the step that carries trees, flora and the rest of the
// surface. It is the stage this file's dispatcher replays, and the number is pinned by
// TestStage9ScheduleGolden rather than trusted.
const vegetationStage = 9

func (r *decorationRegion) placeScheduledVegetationPatches(seed int64) error {
	r.bindSeed(seed)
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		return err
	}
	if err := r.ensureSourceNeighborhood(); err != nil {
		return err
	}
	biomes := r.sourceBiomes()
	schedule, err := set.FeatureSchedule(possibleBiomeOrder(), biomes, vegetationStage)
	if err != nil {
		return err
	}
	random, decorationSeed := worldgen.DecorationRandom(seed, int(r.sourceX), int(r.sourceZ))
	origin := worldgen.FeaturePosition{X: int(r.sourceX) << 4, Y: MinY, Z: int(r.sourceZ) << 4}
	for _, scheduled := range schedule {
		if err := r.placeScheduledVegetationFeature(set, random, scheduled, origin, decorationSeed); err != nil {
			return err
		}
	}
	return nil
}

func (r *decorationRegion) placeScheduledSurfaceTrees(seed int64) error {
	r.bindSeed(seed)
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		return err
	}
	if err := r.ensureSourceNeighborhood(); err != nil {
		return err
	}
	biomes := r.sourceBiomes()
	schedule, err := set.FeatureSchedule(possibleBiomeOrder(), biomes, vegetationStage)
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
		if configured.Type != "minecraft:tree" && configured.Type != "minecraft:random_selector" {
			continue
		}
		if err := r.placeScheduledVegetationFeature(set, random, scheduled, origin, decorationSeed); err != nil {
			return err
		}
	}
	return nil
}

// placeScheduledVegetationFeature replays one scheduled stage-9 feature against
// the region's current source. It is the loop body of
// placeScheduledVegetationPatches, split out so a probe can walk a source's real
// schedule up to a chosen feature: the whole-schedule function has no
// interception point, and a replica of the dispatcher drifts from production. An
// earlier replica in lush_clay_probe_test.go handled six of the eight configured
// types here and silently dropped minecraft:kelp and minecraft:seagrass - both of
// which consume draws - so it had diverged from the stream before reaching
// lush_caves_clay while still reporting what the stream "really" did.
func (r *decorationRegion) placeScheduledVegetationFeature(set *worldgen.FeatureSet, random *worldgen.WorldgenRandom, scheduled worldgen.ScheduledFeature, origin worldgen.FeaturePosition, decorationSeed int64) error {
	placed, ok := set.Placed[scheduled.Name]
	if !ok {
		return nil
	}
	configured, ok := set.Configured[placed.Feature]
	if !ok {
		return nil
	}
	random.SetFeatureSeed(decorationSeed, scheduled.Index, vegetationStage)
	context := r.placementContext(func(position worldgen.FeaturePosition) bool {
		return r.biomeAllowsFeature(set, scheduled.Name, vegetationStage, position)
	})
	switch configured.Type {
	case "minecraft:vegetation_patch":
		config, err := set.VegetationPatch(placed.Feature)
		if err != nil {
			return err
		}
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			r.placeVegetationPatch(random, position, config, set, false)
			if lushClayTrace() {
				fmt.Printf("VGPATCH src=(%d,%d) feature=%s idx=%d pos=(%d,%d,%d) ground=%s\n",
					r.sourceX, r.sourceZ, scheduled.Name, scheduled.Index, position.X, position.Y, position.Z, config.Ground.Name)
			}
			return nil
		}); err != nil {
			return err
		}
	case "minecraft:waterlogged_vegetation_patch":
		config, err := set.VegetationPatch(placed.Feature)
		if err != nil {
			return err
		}
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			r.placeVegetationPatch(random, position, config, set, true)
			if lushClayTrace() {
				fmt.Printf("VGPATCH src=(%d,%d) feature=%s idx=%d pos=(%d,%d,%d) ground=%s waterlogged\n",
					r.sourceX, r.sourceZ, scheduled.Name, scheduled.Index, position.X, position.Y, position.Z, config.Ground.Name)
			}
			return nil
		}); err != nil {
			return err
		}
	case "minecraft:random_boolean_selector":
		config, err := set.RandomBooleanSelector(placed.Feature)
		if err != nil {
			return err
		}
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			ref := config.FeatureFalse
			if random.NextBoolean() {
				ref = config.FeatureTrue
			}
			if lushClayTrace() && scheduled.Name == "minecraft:lush_caves_clay" {
				fmt.Printf("LUSHCLAY src=(%d,%d) pos=(%d,%d,%d) ref=%s\n",
					r.sourceX, r.sourceZ, position.X, position.Y, position.Z, ref.Name)
				if traceLushClayColumn(position.X, position.Z) {
					for y := position.Y; y >= MinY; y-- {
						fmt.Printf("  LUSHCLAY col (%d,*,%d) y=%d: %s\n",
							position.X, position.Z, y, stateLabel(r.getBlock(position.X, y, position.Z)))
					}
				}
			}
			r.placeFeatureRef(random, position, ref, set)
			return nil
		}); err != nil {
			return err
		}
	case "minecraft:block_column":
		config, err := set.BlockColumn(placed.Feature)
		if err != nil {
			return err
		}
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			r.placeBlockColumn(random, position, config, set)
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
	case "minecraft:kelp":
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			r.placeKelp(random, position, set)
			return nil
		}); err != nil {
			return err
		}
	case "minecraft:seagrass":
		config, err := set.Probability(placed.Feature)
		if err != nil {
			return err
		}
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			r.placeSeagrass(random, position, config.Probability, set)
			return nil
		}); err != nil {
			return err
		}
	case "minecraft:simple_block":
		config, err := set.SimpleBlock(placed.Feature)
		if err != nil {
			return err
		}
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			r.placeSimpleBlockFeature(random, position, config, set)
			return nil
		}); err != nil {
			return err
		}
	case "minecraft:tree":
		config, err := set.Tree(placed.Feature)
		if err != nil {
			return err
		}
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			return r.placeTree(set, random, position, config)
		}); err != nil {
			return err
		}
	case "minecraft:random_selector":
		// The tree chains of every temperate biome are one of these, and the entries
		// are placed features rather than configured ones, so the selector's own
		// position list is applied first and each winner's is applied after.
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			return r.placeTreeSelector(set, random, position, placed.Feature, context)
		}); err != nil {
			return err
		}
	case "minecraft:multiface_growth":
		config, err := set.MultifaceGrowth(placed.Feature)
		if err != nil {
			return err
		}
		if scheduled.Name == "minecraft:glow_lichen" {
			r.clearPreStageGlowLichen()
		}
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			r.placeMultifaceGrowth(set, random, position, config)
			return nil
		}); err != nil {
			return err
		}
	case "minecraft:vines":
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			r.placeVines(position)
			return nil
		}); err != nil {
			return err
		}
	case "minecraft:bamboo":
		config, err := set.Probability(placed.Feature)
		if err != nil {
			return err
		}
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			r.placeBamboo(random, position, config.Probability, set)
			return nil
		}); err != nil {
			return err
		}
	case "minecraft:root_system":
		config, err := set.RootSystem(placed.Feature)
		if err != nil {
			return err
		}
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			r.placeRootSystem(random, position, config, set)
			return nil
		}); err != nil {
			return err
		}
	case "minecraft:sea_pickle":
		config, err := set.Count(placed.Feature)
		if err != nil {
			return err
		}
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			r.placeSeaPickle(random, position, config, set)
			return nil
		}); err != nil {
			return err
		}
	case "minecraft:fallen_tree":
		config, err := set.FallenTree(placed.Feature)
		if err != nil {
			return err
		}
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			return r.placeFallenTree(set, random, position, config)
		}); err != nil {
			return err
		}
	case "minecraft:huge_red_mushroom", "minecraft:huge_brown_mushroom":
		config, err := set.HugeMushroom(placed.Feature)
		if err != nil {
			return err
		}
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			return r.placeHugeMushroom(set, random, position, configured.Type, config)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *decorationRegion) placeKelp(random worldgen.RandomSource, position worldgen.FeaturePosition, set *worldgen.FeatureSet) bool {
	position.Y = r.heightAt("OCEAN_FLOOR", position.X, position.Z)
	if r.getBlock(position.X, position.Y, position.Z) != StateWater {
		return false
	}
	plant, plantOK := nameToStateID("minecraft:kelp_plant", nil)
	if !plantOK {
		return false
	}
	height := 1 + int(random.NextIntN(10))
	placed := false
	for i := 0; i <= height; i++ {
		y := position.Y + i
		if r.getBlock(position.X, y, position.Z) == StateWater && r.getBlock(position.X, y+1, position.Z) == StateWater &&
			r.canKelpSurvive(position.X, y, position.Z, set) {
			if i == height {
				age := 20 + int(random.NextIntN(4))
				head, ok := nameToStateID("minecraft:kelp", map[string]string{"age": strconv.Itoa(age)})
				if !ok || !r.setBlock(position.X, y, position.Z, head) {
					break
				}
			} else if !r.setBlock(position.X, y, position.Z, plant) {
				break
			}
			placed = true
			continue
		}
		if i > 0 {
			belowY := y - 1
			belowBelowState, _ := stateByID(r.getBlock(position.X, belowY-1, position.Z))
			if r.canKelpSurvive(position.X, belowY, position.Z, set) && belowBelowState.Name != "minecraft:kelp" {
				age := 20 + int(random.NextIntN(4))
				head, ok := nameToStateID("minecraft:kelp", map[string]string{"age": strconv.Itoa(age)})
				if ok {
					placed = r.setBlock(position.X, belowY, position.Z, head) || placed
				}
			}
		}
		break
	}
	return placed
}

func (r *decorationRegion) canKelpSurvive(x, y, z int, set *worldgen.FeatureSet) bool {
	below := r.getBlock(x, y-1, z)
	state, ok := stateByID(below)
	if !ok || blockTagContains(set, "minecraft:cannot_support_kelp", state.Name) {
		return false
	}
	return state.Name == "minecraft:kelp" || state.Name == "minecraft:kelp_plant" || fullSolidState(below)
}

func (r *decorationRegion) placeSeagrass(random worldgen.RandomSource, position worldgen.FeaturePosition, probability float32, set *worldgen.FeatureSet) bool {
	position.X += int(random.NextIntN(8)) - int(random.NextIntN(8))
	position.Z += int(random.NextIntN(8)) - int(random.NextIntN(8))
	position.Y = r.heightAt("OCEAN_FLOOR", position.X, position.Z)
	if r.getBlock(position.X, position.Y, position.Z) != StateWater {
		return false
	}
	tall := random.NextDouble() < float64(probability)
	if tall {
		lower, lowerOK := nameToStateID("minecraft:tall_seagrass", map[string]string{"half": "lower"})
		upper, upperOK := nameToStateID("minecraft:tall_seagrass", map[string]string{"half": "upper"})
		if !lowerOK || !upperOK || r.getBlock(position.X, position.Y+1, position.Z) != StateWater || !r.canSeagrassSurvive(position.X, position.Y, position.Z, set) {
			return false
		}
		return r.setBlock(position.X, position.Y, position.Z, lower) && r.setBlock(position.X, position.Y+1, position.Z, upper)
	}
	short, ok := nameToStateID("minecraft:seagrass", nil)
	if !ok || !r.canSeagrassSurvive(position.X, position.Y, position.Z, set) {
		return false
	}
	return r.setBlock(position.X, position.Y, position.Z, short)
}

func (r *decorationRegion) canSeagrassSurvive(x, y, z int, set *worldgen.FeatureSet) bool {
	below := r.getBlock(x, y-1, z)
	state, ok := stateByID(below)
	return ok && fullSolidState(below) && !blockTagContains(set, "minecraft:cannot_support_seagrass", state.Name)
}

func blockTagContains(set *worldgen.FeatureSet, tag, name string) bool {
	for _, member := range flattenBlockTag(set, tag, nil) {
		if member == name {
			return true
		}
	}
	return false
}

// placeVegetationFeatureRef runs an inline feature reference produced by a
// selector feature (random_boolean_selector and friends): the reference names
// a configured feature with an empty inline placement, so the configured
// feature runs directly at the position.
func (r *decorationRegion) placeVegetationFeatureRef(random worldgen.RandomSource, position worldgen.FeaturePosition, ref worldgen.FeatureRef, set *worldgen.FeatureSet) error {
	if len(ref.Placement) != 0 {
		return fmt.Errorf("world: vegetation selector feature %s has non-empty placement", ref.Name)
	}
	configured, ok := set.Configured[ref.Name]
	if !ok {
		return fmt.Errorf("world: vegetation selector feature %s missing", ref.Name)
	}
	switch configured.Type {
	case "minecraft:vegetation_patch":
		config, err := set.VegetationPatch(ref.Name)
		if err != nil {
			return err
		}
		r.placeVegetationPatch(random, position, config, set, false)
	case "minecraft:waterlogged_vegetation_patch":
		config, err := set.VegetationPatch(ref.Name)
		if err != nil {
			return err
		}
		r.placeVegetationPatch(random, position, config, set, true)
	default:
		return fmt.Errorf("world: vegetation selector feature %s has unsupported type %s", ref.Name, configured.Type)
	}
	return nil
}

func (r *decorationRegion) placeVegetationPatch(random worldgen.RandomSource, origin worldgen.FeaturePosition, config worldgen.VegetationPatchFeatureConfig, set *worldgen.FeatureSet, waterlogged bool) bool {
	radiusX := config.XZRadiusMin + 1
	if config.XZRadiusMax > config.XZRadiusMin {
		radiusX += int(random.NextIntN(int32(config.XZRadiusMax - config.XZRadiusMin + 1)))
	}
	radiusZ := config.XZRadiusMin + 1
	if config.XZRadiusMax > config.XZRadiusMin {
		radiusZ += int(random.NextIntN(int32(config.XZRadiusMax - config.XZRadiusMin + 1)))
	}
	direction := -1
	if config.Surface == "ceiling" {
		direction = 1
	}
	replaceable := tagStateIDs(set, config.ReplaceableTag)
	ground, groundOK := nameToStateID(config.Ground.Name, config.Ground.Properties)
	if !groundOK {
		return false
	}
	placed := false
	// The patch set holds the GROUND positions (the top block of each placed
	// column), matching VegetationPatchFeature.placeGroundPatch's return set;
	// the waterlogged subclass's water fill and the vegetation roll count both
	// key off it.
	var patchSet [][3]int
	for dx := -radiusX; dx <= radiusX; dx++ {
		for dz := -radiusZ; dz <= radiusZ; dz++ {
			onXEdge := dx == -radiusX || dx == radiusX
			onZEdge := dz == -radiusZ || dz == radiusZ
			if onXEdge && onZEdge {
				continue
			}
			// Vanilla skips edge columns without a draw when the chance is 0
			// and keeps them when nextFloat() <= chance.
			if onXEdge || onZEdge {
				if config.ExtraEdgeColumnChance == 0 {
					continue
				}
				if random.NextFloat() > config.ExtraEdgeColumnChance {
					continue
				}
			}
			p := origin
			p.X += dx
			p.Z += dz
			// Both scan phases use BlockState::isAir semantics: cave_air
			// carved by lakes, mineshafts, and monster rooms counts as air.
			// Vanilla walks phase 1 while the cell is AIR (bootstrap #1 =
			// BlockStateBase::isAir) moving in the surface direction, then
			// phase 2 while the cell is SOLID (bootstrap #2 = !isAir) moving
			// in the opposite direction — so the candidate ends on the first
			// air cell at the cave surface within ±vertical_range of origin.
			current := r.getBlock(p.X, p.Y, p.Z)
			steps := 0
			for isAirState(current) && steps < config.VerticalRange {
				p.Y += direction
				current = r.getBlock(p.X, p.Y, p.Z)
				steps++
			}
			steps = 0
			for !isAirState(current) && steps < config.VerticalRange {
				p.Y -= direction
				current = r.getBlock(p.X, p.Y, p.Z)
				steps++
			}
			// Vanilla requires the candidate cell to be empty (isEmptyBlock =
			// isAir, cave_air included) and the adjacent surface block to
			// expose a sturdy face toward the patch.
			if !isAirState(r.getBlock(p.X, p.Y, p.Z)) {
				continue
			}
			groundX, groundY, groundZ := p.X, p.Y+direction, p.Z
			if !isFaceSturdy(r.getBlock(groundX, groundY, groundZ)) {
				continue
			}

			depth := config.DepthMin
			if config.DepthMax > config.DepthMin {
				depth += int(random.NextIntN(int32(config.DepthMax - config.DepthMin + 1)))
			}
			if config.ExtraBottomBlockChance > 0 && random.NextFloat() < config.ExtraBottomBlockChance {
				depth++
			}
			// placeGround: in vanilla, pos ONLY moves when a block is replaced.
			// If already the ground block, pos does not move. If not replaceable,
			// returns i > 0.
			columnOK := true
			gy := groundY
			for i := 0; i < depth; i++ {
				current := r.getBlock(groundX, gy, groundZ)
				if current == ground {
					continue
				}
				if !replaceable[current] {
					columnOK = i > 0
					break
				}
				if r.setBlock(groundX, gy, groundZ, ground) {
					placed = true
				}
				gy += direction
			}
			if columnOK {
				patchSet = append(patchSet, [3]int{groundX, groundY, groundZ})
			}
		}
	}

	if waterlogged {
		initVegetationWater()
		// WaterloggedVegetationPatchFeature.placeGroundPatch: enclosed ground
		// cells become water and the returned (vegetation-rolled) set is the
		// water cells. Exposure is evaluated after the whole ground pass, in
		// the ground HashSet's own iteration order - which therefore, not the
		// column order, is the water set's insertion order, and insertion
		// order decides within-bucket ties when the roll iterates it.
		var waterCells [][3]int
		for _, g := range javaHashSetOrder(patchSet) {
			if !r.patchColumnExposed(g) {
				waterCells = append(waterCells, g)
			}
		}
		for _, g := range javaHashSetOrder(waterCells) {
			r.setBlock(g[0], g[1], g[2], vegetationWaterID)
		}
		patchSet = waterCells
	}
	// distributeVegetation: one draw per set position whenever the chance is
	// positive; a passing roll runs the nested placed feature. Vanilla
	// iterates the patch set (a HashSet<BlockPos>) in hash-table order - the
	// plain patch's ground set or the waterlogged pool's water subset, each
	// in its OWN hash order (the water set is a fresh HashSet).
	for _, g := range javaHashSetOrder(patchSet) {
		if config.VegetationChance <= 0 {
			continue
		}
		fl := random.NextFloat()
		if lushClayTrace() && len(patchSet) > 0 {
			fmt.Printf("  VEGROLL src=(%d,%d) origin=(%d,%d,%d) cell=(%d,%d,%d) roll=%.3f pass=%v\n",
				r.sourceX, r.sourceZ, origin.X, origin.Y, origin.Z, g[0], g[1], g[2], fl, fl < config.VegetationChance)
		}
		if fl >= config.VegetationChance {
			continue
		}
		vegPos := patchVegetationPosition(g, direction, waterlogged)
		r.placePatchVegetationFeature(random, vegPos, config.Vegetation, set, waterlogged)
	}
	return placed
}

// javaHashSetOrder reorders positions the way Java's HashSet<BlockPos>
// iterates them: by table slot ((capacity-1) & spread(hash)), and within a
// slot in insertion order. Vec3i.hashCode is (y + 31*z)*31 + x in wrapping
// int32 arithmetic, and HashMap spreads the hash with h ^ (h >>> 16) before
// masking. The capacity grows from 16 by doubling at a load factor of 0.75.
func javaHashSetOrder(positions [][3]int) [][3]int {
	if len(positions) <= 1 {
		return positions
	}
	capacity := 16
	for len(positions) > capacity*3/4 {
		capacity *= 2
	}
	type entry struct {
		slot, seq int
		pos       [3]int
	}
	entries := make([]entry, len(positions))
	for i, p := range positions {
		h := int32(p[1]) + 31*int32(p[2])
		h = h*31 + int32(p[0])
		spread := uint32(h) ^ (uint32(h) >> 16)
		entries[i] = entry{slot: int(spread & uint32(capacity-1)), seq: i, pos: p}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].slot < entries[j].slot })
	out := make([][3]int, len(positions))
	for i, e := range entries {
		out[i] = e.pos
	}
	return out
}

var vegetationWaterID uint16
var vegetationWaterOnce bool

func initVegetationWater() {
	if !vegetationWaterOnce {
		vegetationWaterOnce = true
		stateByIDOnce.Do(buildStateTable)
		id, ok := nameToStateID("minecraft:water", nil)
		if !ok {
			panic("world: missing water state for vegetation patches")
		}
		vegetationWaterID = id
	}
}

// patchColumnExposed mirrors WaterloggedVegetationPatchFeature.isExposed: the
// position is exposed when any of the four horizontal neighbours or the block
// below does not present a sturdy face toward it. isExposedDirection calls
// BlockState::isFaceSturdy, the same predicate the ground acceptance test
// above uses; blocksMotion ("full solid") is a different set - farmland,
// sculk sensors and spawners block motion without bounding a face.
func (r *decorationRegion) patchColumnExposed(g [3]int) bool {
	for _, d := range [5][3]int{{0, 0, -1}, {1, 0, 0}, {0, 0, 1}, {-1, 0, 0}, {0, -1, 0}} {
		if !isFaceSturdy(r.getBlock(g[0]+d[0], g[1]+d[1], g[2]+d[2])) {
			return true
		}
	}
	return false
}

// placeSimpleBlockFeature implements the simple_block feature variants used
// by the moss vegetation patches. Vanilla draws the state provider FIRST and
// then checks canSurvive, so the draw happens even when the placement is
// rejected - the patch's later vegetation rolls depend on that.
func (r *decorationRegion) placeSimpleBlockFeature(random worldgen.RandomSource, position worldgen.FeaturePosition, config worldgen.SimpleBlockFeatureConfig, set *worldgen.FeatureSet) bool {
	var chosen worldgen.BlockState
	switch config.Provider.Type {
	case "noise", "noise_threshold", "dual_noise", "randomized_int":
		var ok bool
		chosen, ok = config.Provider.SampleStateAt(random, position)
		if !ok {
			return false
		}
	case "simple":
		if len(config.States) == 1 {
			chosen = config.States[0].State
			break
		}
		fallthrough
	default:
		total := 0
		for _, entry := range config.States {
			if entry.Weight > 0 {
				total += entry.Weight
			}
		}
		if total <= 0 {
			return false
		}
		roll := int(random.NextIntN(int32(total)))
		for _, entry := range config.States {
			if entry.Weight <= 0 {
				continue
			}
			if roll < entry.Weight {
				chosen = entry.State
				break
			}
			roll -= entry.Weight
		}
	}
	state, ok := nameToStateID(chosen.Name, chosen.Properties)
	if !ok || !r.simpleBlockCanSurvive(position, chosen.Name, set) {
		return false
	}
	// DoublePlantBlock.placeAt path: any lower-half state places both halves,
	// copying waterlogged from the target cells via DoublePlantBlock.copyWaterloggedFrom.
	if chosen.Properties["half"] == "lower" {
		if !isAirState(r.getBlock(position.X, position.Y+1, position.Z)) {
			return false
		}
		lowerState := state
		if _, hasWater := chosen.Properties["waterlogged"]; hasWater {
			lowerProps := make(map[string]string, len(chosen.Properties))
			for k, v := range chosen.Properties {
				lowerProps[k] = v
			}
			lowerProps["waterlogged"] = strconv.FormatBool(isWaterState(r.getBlock(position.X, position.Y, position.Z)))
			if id, ok := nameToStateID(chosen.Name, lowerProps); ok {
				lowerState = id
			}
		}
		upperProps := make(map[string]string, len(chosen.Properties))
		for k, v := range chosen.Properties {
			upperProps[k] = v
		}
		upperProps["half"] = "upper"
		if _, hasWater := chosen.Properties["waterlogged"]; hasWater {
			upperProps["waterlogged"] = strconv.FormatBool(isWaterState(r.getBlock(position.X, position.Y+1, position.Z)))
		}
		upper, upperOK := nameToStateID(chosen.Name, upperProps)
		if !upperOK {
			return false
		}
		if !r.setBlock(position.X, position.Y, position.Z, lowerState) {
			return false
		}
		return r.setBlock(position.X, position.Y+1, position.Z, upper)
	}
	return r.setBlock(position.X, position.Y, position.Z, state)
}

// clearPreStageGlowLichen reverts any premature structure-stage waterlogged
// glow_lichen in the interior (lx, lz in 2..13) of the current source chunk
// back to StateWater before Stage 9's minecraft:glow_lichen (index 0) runs.
// Because glow_lichen uses in_square and can step at most 2 blocks across a
// chunk boundary, no prior source chunk can legitimately place glow_lichen in
// [2..13]x[2..13]; leaving a premature glow_lichen there causes
// MultifaceGrowthFeature to skip placement and omit its ChanceOfSpreading RNG
// draw, desynchronizing subsequent glow_lichen and moss_patch_ceiling rolls.
func (r *decorationRegion) clearPreStageGlowLichen() {
	chunk := r.chunks[[2]int32{r.sourceX, r.sourceZ}]
	if chunk == nil {
		return
	}
	for y := MinY; y < MinY+WorldHeight; y++ {
		for lz := 2; lz <= 13; lz++ {
			for lx := 2; lx <= 13; lx++ {
				id := chunk.GetBlock(lx, y, lz)
				if st, ok := stateByID(id); ok && st.Name == "minecraft:glow_lichen" {
					if st.Properties["waterlogged"] == "true" {
						chunk.SetBlock(lx, y, lz, StateWater)
					} else {
						chunk.SetBlock(lx, y, lz, StateAir)
					}
				}
			}
		}
	}
}

func (r *decorationRegion) canVegetationSurvive(position worldgen.FeaturePosition, name string, set *worldgen.FeatureSet) bool {
	if position.Y <= MinY {
		return false
	}
	below := r.getBlock(position.X, position.Y-1, position.Z)
	if name == "minecraft:moss_carpet" || name == "minecraft:pale_moss_carpet" {
		return !isAirState(below)
	}
	for _, supported := range flattenBlockTag(set, "minecraft:supports_vegetation", nil) {
		if state, ok := stateByID(below); ok && state.Name == supported {
			return true
		}
	}
	return false
}

// placeBamboo ports BambooFeature.place (26.1.2 bytecode).
func (r *decorationRegion) placeBamboo(random worldgen.RandomSource, origin worldgen.FeaturePosition, probability float32, set *worldgen.FeatureSet) bool {
	if !isAirState(r.getBlock(origin.X, origin.Y, origin.Z)) {
		return false
	}
	if !r.canBambooSurvive(origin.X, origin.Y, origin.Z, set) {
		return false
	}
	height := int(random.NextIntN(12)) + 5
	if random.NextFloat() < probability {
		rad := int(random.NextIntN(4)) + 1
		podzol, podzolOK := nameToStateID("minecraft:podzol", nil)
		for xx := origin.X - rad; xx <= origin.X+rad; xx++ {
			for zz := origin.Z - rad; zz <= origin.Z+rad; zz++ {
				dx, dz := xx-origin.X, zz-origin.Z
				if dx*dx+dz*dz <= rad*rad {
					py := r.heightAt("WORLD_SURFACE", xx, zz) - 1
					if st, ok := stateByID(r.getBlock(xx, py, zz)); ok && podzolOK && flattenBlockTagContains(set, "minecraft:beneath_bamboo_podzol_replaceable", st.Name) {
						r.setBlock(xx, py, zz, podzol)
					}
				}
			}
		}
	}
	stalk, _ := nameToStateID("minecraft:bamboo", map[string]string{"age": "1", "leaves": "none", "stage": "0"})
	topLarge, _ := nameToStateID("minecraft:bamboo", map[string]string{"age": "1", "leaves": "large", "stage": "1"})
	midLarge, _ := nameToStateID("minecraft:bamboo", map[string]string{"age": "1", "leaves": "large", "stage": "0"})
	smallLeaves, _ := nameToStateID("minecraft:bamboo", map[string]string{"age": "1", "leaves": "small", "stage": "0"})
	by := origin.Y
	for i := 0; i < height && isAirState(r.getBlock(origin.X, by, origin.Z)); i++ {
		r.setBlock(origin.X, by, origin.Z, stalk)
		by++
	}
	if by-origin.Y >= 3 {
		r.setBlock(origin.X, by, origin.Z, topLarge)
		by--
		r.setBlock(origin.X, by, origin.Z, midLarge)
		by--
		r.setBlock(origin.X, by, origin.Z, smallLeaves)
	}
	return true
}

func (r *decorationRegion) canBambooSurvive(x, y, z int, set *worldgen.FeatureSet) bool {
	if y <= MinY {
		return false
	}
	below, ok := stateByID(r.getBlock(x, y-1, z))
	return ok && flattenBlockTagContains(set, "minecraft:supports_bamboo", below.Name)
}

// placeRootSystem ports RootSystemFeature.place (26.1.2 bytecode).
func (r *decorationRegion) placeRootSystem(random worldgen.RandomSource, origin worldgen.FeaturePosition, config worldgen.RootSystemFeatureConfig, set *worldgen.FeatureSet) bool {
	if !isAirState(r.getBlock(origin.X, origin.Y, origin.Z)) {
		return false
	}
	workingPos := origin
	treePlaced := false
	for y := 0; y < config.RootColumnMaxHeight; y++ {
		workingPos.Y++
		allowed, err := r.testBlockPredicate(set, config.AllowedTreePosition, workingPos)
		if err != nil || !allowed {
			continue
		}
		if !r.spaceForRootSystemTree(config, workingPos) {
			continue
		}
		belowState := r.getBlock(workingPos.X, workingPos.Y-1, workingPos.Z)
		if isLavaState(belowState) || !isSolidState(belowState) {
			break
		}
		if r.placeFeatureRef(random, workingPos, config.Feature, set) {
			targetHeight := origin.Y + y
			replaceable := tagStateIDs(set, config.RootReplaceable)
			for py := origin.Y; py < targetHeight; py++ {
				for i := 0; i < config.RootPlacementAttempts; i++ {
					wx := origin.X + int(random.NextIntN(int32(config.RootRadius))) - int(random.NextIntN(int32(config.RootRadius)))
					wz := origin.Z + int(random.NextIntN(int32(config.RootRadius))) - int(random.NextIntN(int32(config.RootRadius)))
					if replaceable[r.getBlock(wx, py, wz)] {
						if chosen, ok := config.RootStateProvider.SampleStateAt(random, worldgen.FeaturePosition{X: wx, Y: py, Z: wz}); ok {
							if st, stOK := nameToStateID(chosen.Name, chosen.Properties); stOK {
								r.setBlock(wx, py, wz, st)
							}
						}
					}
				}
			}
			treePlaced = true
			break
		}
	}
	if !treePlaced {
		return false
	}
	for i := 0; i < config.HangingRootPlacementAttempts; i++ {
		wx := origin.X + int(random.NextIntN(int32(config.HangingRootRadius))) - int(random.NextIntN(int32(config.HangingRootRadius)))
		wy := origin.Y + int(random.NextIntN(int32(config.HangingRootsVerticalSpan))) - int(random.NextIntN(int32(config.HangingRootsVerticalSpan)))
		wz := origin.Z + int(random.NextIntN(int32(config.HangingRootRadius))) - int(random.NextIntN(int32(config.HangingRootRadius)))
		if isAirState(r.getBlock(wx, wy, wz)) {
			chosen, ok := config.HangingRootStateProvider.SampleStateAt(random, worldgen.FeaturePosition{X: wx, Y: wy, Z: wz})
			if ok && wy+1 < MinY+WorldHeight && isFaceSturdy(r.getBlock(wx, wy+1, wz)) {
				if st, stOK := nameToStateID(chosen.Name, chosen.Properties); stOK {
					r.setBlock(wx, wy, wz, st)
				}
			}
		}
	}
	return true
}

func (r *decorationRegion) spaceForRootSystemTree(config worldgen.RootSystemFeatureConfig, pos worldgen.FeaturePosition) bool {
	for i := 1; i <= config.RequiredVerticalSpaceForTree; i++ {
		st := r.getBlock(pos.X, pos.Y+i, pos.Z)
		if !isAirState(st) && (i+1 > config.AllowedVerticalWaterForTree || !isWaterState(st)) {
			return false
		}
	}
	return true
}

// placeSeaPickle ports SeaPickleFeature.place (26.1.2 bytecode).
func (r *decorationRegion) placeSeaPickle(random worldgen.RandomSource, origin worldgen.FeaturePosition, config worldgen.CountFeatureConfig, set *worldgen.FeatureSet) bool {
	placed := 0
	count := config.Count.Sample(random)
	for i := 0; i < count; i++ {
		dx := int(random.NextIntN(8)) - int(random.NextIntN(8))
		dz := int(random.NextIntN(8)) - int(random.NextIntN(8))
		px, pz := origin.X+dx, origin.Z+dz
		py := r.heightAt("OCEAN_FLOOR", px, pz)
		pickles := int(random.NextIntN(4)) + 1
		if r.getBlock(px, py, pz) == StateWater && py > MinY && isFaceSturdy(r.getBlock(px, py-1, pz)) {
			if st, ok := nameToStateID("minecraft:sea_pickle", map[string]string{"pickles": strconv.Itoa(pickles), "waterlogged": "true"}); ok {
				if r.setBlock(px, py, pz, st) {
					placed++
				}
			}
		}
	}
	return placed > 0
}

// placeCoralFeature ports CoralFeature.place (26.1.2 bytecode).
func (r *decorationRegion) placeCoralFeature(random worldgen.RandomSource, origin worldgen.FeaturePosition, kind string, set *worldgen.FeatureSet) bool {
	coralBlocks := flattenBlockTag(set, "minecraft:coral_blocks", nil)
	if len(coralBlocks) == 0 {
		return false
	}
	chosenName := coralBlocks[int(random.NextIntN(int32(len(coralBlocks))))]
	coralBlockState, ok := nameToStateID(chosenName, nil)
	if !ok {
		return false
	}
	switch kind {
	case "minecraft:coral_tree":
		return r.placeCoralTree(random, origin, coralBlockState, set)
	case "minecraft:coral_claw":
		return r.placeCoralClaw(random, origin, coralBlockState, set)
	case "minecraft:coral_mushroom":
		return r.placeCoralMushroom(random, origin, coralBlockState, set)
	}
	return false
}

func (r *decorationRegion) placeCoralTree(random worldgen.RandomSource, origin worldgen.FeaturePosition, coralBlockState uint16, set *worldgen.FeatureSet) bool {
	x, y, z := origin.X, origin.Y, origin.Z
	trunkHeight := int(random.NextIntN(3)) + 1
	for i := 0; i < trunkHeight; i++ {
		if !r.placeCoralBlock(random, x, y, z, coralBlockState, set) {
			return true
		}
		y++
	}
	topX, topY, topZ := x, y, z
	numBranches := int(random.NextIntN(3)) + 2
	dirs := [][2]int{{0, -1}, {1, 0}, {0, 1}, {-1, 0}}
	for i := len(dirs); i > 1; i-- {
		j := int(random.NextIntN(int32(i)))
		dirs[i-1], dirs[j] = dirs[j], dirs[i-1]
	}
	for _, d := range dirs[:numBranches] {
		bx, by, bz := topX+d[0], topY, topZ+d[1]
		branchLen := int(random.NextIntN(5)) + 2
		segLen := 0
		for j := 0; j < branchLen && r.placeCoralBlock(random, bx, by, bz, coralBlockState, set); j++ {
			segLen++
			by++
			if j == 0 || (segLen >= 2 && random.NextFloat() < 0.25) {
				bx += d[0]
				bz += d[1]
				segLen = 0
			}
		}
	}
	return true
}

func (r *decorationRegion) placeCoralClaw(random worldgen.RandomSource, origin worldgen.FeaturePosition, coralBlockState uint16, set *worldgen.FeatureSet) bool {
	if !r.placeCoralBlock(random, origin.X, origin.Y, origin.Z, coralBlockState, set) {
		return false
	}
	type horizDir struct {
		dx, dz   int
		cwX, cwZ int
		ccwX     int
		ccwZ     int
	}
	horiz := [4]horizDir{
		{0, -1, 1, 0, -1, 0},
		{1, 0, 0, 1, 0, -1},
		{0, 1, -1, 0, 1, 0},
		{-1, 0, 0, -1, 0, 1},
	}
	clawDir := horiz[int(random.NextIntN(4))]
	numBranches := int(random.NextIntN(2)) + 2
	dirs := [][2]int{{clawDir.dx, clawDir.dz}, {clawDir.cwX, clawDir.cwZ}, {clawDir.ccwX, clawDir.ccwZ}}
	for i := len(dirs); i > 1; i-- {
		j := int(random.NextIntN(int32(i)))
		dirs[i-1], dirs[j] = dirs[j], dirs[i-1]
	}
	for _, branchDir := range dirs[:numBranches] {
		bx, by, bz := origin.X+branchDir[0], origin.Y, origin.Z+branchDir[1]
		sidewayLen := int(random.NextIntN(2)) + 1
		var inwayX, inwayY, inwayZ, upLen int
		if branchDir[0] == clawDir.dx && branchDir[1] == clawDir.dz {
			inwayX, inwayY, inwayZ = clawDir.dx, 0, clawDir.dz
			upLen = int(random.NextIntN(3)) + 2
		} else {
			by++
			if random.NextIntN(2) == 0 {
				inwayX, inwayY, inwayZ = branchDir[0], 0, branchDir[1]
			} else {
				inwayX, inwayY, inwayZ = 0, 1, 0
			}
			upLen = int(random.NextIntN(3)) + 3
		}
		placed := true
		for i := 0; i < sidewayLen; i++ {
			if !r.placeCoralBlock(random, bx, by, bz, coralBlockState, set) {
				placed = false
				break
			}
			bx += inwayX
			by += inwayY
			bz += inwayZ
		}
		if !placed {
			continue
		}
		bx -= inwayX
		by -= inwayY
		bz -= inwayZ
		by++
		for i := 0; i < upLen; i++ {
			bx += clawDir.dx
			bz += clawDir.dz
			if !r.placeCoralBlock(random, bx, by, bz, coralBlockState, set) {
				break
			}
			if random.NextFloat() < 0.25 {
				by++
			}
		}
	}
	return true
}

func (r *decorationRegion) placeCoralMushroom(random worldgen.RandomSource, origin worldgen.FeaturePosition, coralBlockState uint16, set *worldgen.FeatureSet) bool {
	height := int(random.NextIntN(3)) + 3
	width := int(random.NextIntN(3)) + 3
	length := int(random.NextIntN(3)) + 3
	sink := int(random.NextIntN(3)) + 1
	for x := 0; x <= width; x++ {
		for y := 0; y <= height; y++ {
			for z := 0; z <= length; z++ {
				if ((x == 0 || x == width) && (y == 0 || y == height)) ||
					((z == 0 || z == length) && (y == 0 || y == height)) ||
					((x == 0 || x == width) && (z == 0 || z == length)) ||
					(x != 0 && x != width && y != 0 && y != height && z != 0 && z != length) ||
					random.NextFloat() < 0.1 {
					continue
				}
				r.placeCoralBlock(random, origin.X+x, origin.Y+y-sink, origin.Z+z, coralBlockState, set)
			}
		}
	}
	return true
}

func (r *decorationRegion) placeCoralBlock(random worldgen.RandomSource, x, y, z int, coralBlockState uint16, set *worldgen.FeatureSet) bool {
	aboveY := y + 1
	cur := r.getBlock(x, y, z)
	coralTag := tagStateIDs(set, "minecraft:corals")
	if (cur == StateWater || coralTag[cur]) && r.getBlock(x, aboveY, z) == StateWater {
		r.setBlock(x, y, z, coralBlockState)
		if random.NextFloat() < 0.25 {
			corals := flattenBlockTag(set, "minecraft:corals", nil)
			if len(corals) > 0 {
				cName := corals[int(random.NextIntN(int32(len(corals))))]
				if cState, ok := nameToStateID(cName, nil); ok {
					r.setBlock(x, aboveY, z, cState)
				}
			}
		} else if random.NextFloat() < 0.05 {
			pickles := int(random.NextIntN(4)) + 1
			if pState, ok := nameToStateID("minecraft:sea_pickle", map[string]string{"pickles": strconv.Itoa(pickles), "waterlogged": "true"}); ok {
				r.setBlock(x, aboveY, z, pState)
			}
		}
		wallCorals := flattenBlockTag(set, "minecraft:wall_corals", nil)
		horizDirs := [4]struct {
			dx, dz int
			name   string
		}{{0, -1, "north"}, {1, 0, "east"}, {0, 1, "south"}, {-1, 0, "west"}}
		for _, d := range horizDirs {
			if random.NextFloat() < 0.2 && r.getBlock(x+d.dx, y, z+d.dz) == StateWater && len(wallCorals) > 0 {
				wName := wallCorals[int(random.NextIntN(int32(len(wallCorals))))]
				if wState, ok := nameToStateID(wName, map[string]string{"facing": d.name, "waterlogged": "true"}); ok {
					r.setBlock(x+d.dx, y, z+d.dz, wState)
				}
			}
		}
		return true
	}
	return false
}

