package world

import (
	"fmt"
	"sort"
	"strconv"

	"regionio/internal/worldgen"
)

func (r *decorationRegion) placeScheduledVegetationPatches(seed int64) error {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		return err
	}
	if err := r.ensureSourceNeighborhood(); err != nil {
		return err
	}
	schedule, err := set.FeatureSchedule(possibleBiomeOrder(), r.sourceBiomes(), vegetationStage)
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
				return r.placeVegetationFeatureRef(random, position, ref, set)
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
			if r.canKelpSurvive(position.X, belowY, position.Z, set) && r.getBlock(position.X, belowY-1, position.Z) != plant {
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
	replaceable := geodeTagIDs(set, config.ReplaceableTag)
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
			current := r.getBlock(p.X, p.Y, p.Z)
			steps := 0
			for current == StateAir && steps < config.VerticalRange {
				p.Y += direction
				current = r.getBlock(p.X, p.Y, p.Z)
				steps++
			}
			steps = 0
			for current != StateAir && steps < config.VerticalRange {
				p.Y -= direction
				current = r.getBlock(p.X, p.Y, p.Z)
				steps++
			}
			// Vanilla requires the candidate cell to be empty and the adjacent
			// surface block to expose a sturdy face toward the patch.
			if r.getBlock(p.X, p.Y, p.Z) != StateAir {
				continue
			}
			groundX, groundY, groundZ := p.X, p.Y+direction, p.Z
			if !fullSolidState(r.getBlock(groundX, groundY, groundZ)) {
				continue
			}
			depth := config.DepthMin
			if config.DepthMax > config.DepthMin {
				depth += int(random.NextIntN(int32(config.DepthMax - config.DepthMin + 1)))
			}
			if config.ExtraBottomBlockChance > 0 && random.NextFloat() < config.ExtraBottomBlockChance {
				depth++
			}
			// placeGround: a column succeeds unless the FIRST cell is neither
			// replaceable nor already the ground block; hitting a wall deeper
			// down still counts, and already-ground cells are skipped in place.
			columnOK := true
			for i := 0; i < depth; i++ {
				gx, gy, gz := groundX, groundY+direction*i, groundZ
				current := r.getBlock(gx, gy, gz)
				if current == ground {
					continue
				}
				if !replaceable[current] {
					columnOK = i > 0
					break
				}
				if r.setBlock(gx, gy, gz, ground) {
					placed = true
				}
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
		// water cells. Exposure is evaluated after the whole ground pass.
		var waterCells [][3]int
		for _, g := range patchSet {
			if !r.patchColumnExposed(g) {
				waterCells = append(waterCells, g)
			}
		}
		for _, g := range waterCells {
			r.setBlock(g[0], g[1], g[2], vegetationWaterID)
		}
		patchSet = waterCells
	}
	// distributeVegetation: one draw per set position whenever the chance is
	// positive. The nested vegetation features are NOT replayed yet: the
	// moss-patch ground itself still mismatches vanilla by ~300 cells (see
	// STRUCTURE_NOTES), and placing vegetation on the wrong ground cascades
	// into both the vegetation and the later patch columns (verified: placing
	// moss_vegetation dropped fixture parity by ~100 cells net). The rolls
	// are the feature's last draws, so consuming them without the nested
	// feature draws cannot shift anything downstream. The semantics needed
	// to finish this are pinned in STRUCTURE_NOTES.md: Java HashSet
	// iteration order for the roll mapping (javaHashSetOrder below),
	// SimpleBlockFeature's always-draw-then-canSurvive order, the
	// DoublePlantBlock.placeAt both-halves path, and the waterlogged pool's
	// vegetation placing INTO the water cells with waterlog=true.
	for range patchSet {
		if config.VegetationChance > 0 {
			random.NextFloat()
		}
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

// placePatchVegetationFeature runs a patch's nested vegetation feature at the
// given position with the patch's own random: VegetationPatchFeature
// .placeVegetation delegates to the nested PlacedFeature with the surface
// offset already applied by the caller.
func (r *decorationRegion) placePatchVegetationFeature(random worldgen.RandomSource, position worldgen.FeaturePosition, ref worldgen.FeatureRef, set *worldgen.FeatureSet) {
	if len(ref.Placement) != 0 {
		return
	}
	configured, ok := set.Configured[ref.Name]
	if !ok {
		return
	}
	switch configured.Type {
	case "minecraft:simple_block":
		config, err := set.SimpleBlock(ref.Name)
		if err != nil {
			return
		}
		r.placeSimpleBlockFeature(random, position, config, set)
	}
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
// below does not present a sturdy face toward it.
func (r *decorationRegion) patchColumnExposed(g [3]int) bool {
	for _, d := range [5][3]int{{0, 0, -1}, {1, 0, 0}, {0, 0, 1}, {-1, 0, 0}, {0, -1, 0}} {
		if !fullSolidState(r.getBlock(g[0]+d[0], g[1]+d[1], g[2]+d[2])) {
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
	chosen := worldgen.BlockState{}
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
	state, ok := nameToStateID(chosen.Name, chosen.Properties)
	if !ok || !r.canVegetationSurvive(position, chosen.Name, set) {
		return false
	}
	if chosen.Name == "minecraft:tall_grass" && chosen.Properties["half"] == "lower" {
		upper, upperOK := nameToStateID(chosen.Name, map[string]string{"half": "upper"})
		if !upperOK || r.getBlock(position.X, position.Y+1, position.Z) != StateAir {
			return false
		}
		if !r.setBlock(position.X, position.Y, position.Z, state) {
			return false
		}
		return r.setBlock(position.X, position.Y+1, position.Z, upper)
	}
	return r.setBlock(position.X, position.Y, position.Z, state)
}

func (r *decorationRegion) canVegetationSurvive(position worldgen.FeaturePosition, name string, set *worldgen.FeatureSet) bool {
	if position.Y <= MinY {
		return false
	}
	below := r.getBlock(position.X, position.Y-1, position.Z)
	if name == "minecraft:moss_carpet" || name == "minecraft:pale_moss_carpet" {
		return fullSolidState(below)
	}
	for _, supported := range flattenBlockTag(set, "minecraft:supports_vegetation", nil) {
		if state, ok := stateByID(below); ok && state.Name == supported {
			return true
		}
	}
	return false
}
