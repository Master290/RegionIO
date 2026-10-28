package world

import (
	"fmt"

	"regionio/internal/worldgen"
)

// cave_features.go replays the stage-9 cave decoration features that write
// into cave air before the moss/clay patches scan through it: cave vines
// (block_column), the ceiling patch's in-moss vines, the clay pools' dripleaf
// selector, and the patch vegetation proper (simple_block family).
//
// BlockColumnFeature semantics (26.1.2 bytecode):
//   - heights for ALL layers are sampled first (layer order);
//   - the allowed-placement walk starts at origin+direction and checks
//     `total` cells; on the first rejection the heights are truncated to the
//     allowed run (prioritize_tip removes from layer 0 first, keeping the
//     tip layer at the end of the run);
//   - placement starts AT the origin (the origin cell is never checked) and
//     walks direction, sampling the state provider PER BLOCK.

// placeBlockColumn replays one block_column feature at the position.
func (r *decorationRegion) placeBlockColumn(random worldgen.RandomSource, position worldgen.FeaturePosition, config worldgen.BlockColumnFeatureConfig, set *worldgen.FeatureSet) bool {
	heights := make([]int, len(config.Layers))
	total := 0
	for i, layer := range config.Layers {
		heights[i] = layer.Height.Sample(random)
		total += heights[i]
	}
	if total == 0 {
		return false
	}
	dy := -1
	if config.Direction == "up" {
		dy = 1
	}
	if len(config.Allowed) > 0 {
		walkY := position.Y + dy
		for i := 0; i < total; i++ {
			ok, err := r.testBlockPredicate(set, config.Allowed, worldgen.FeaturePosition{X: position.X, Y: walkY, Z: position.Z})
			if err != nil {
				return false
			}
			if !ok {
				blockColumnTruncate(heights, total, i, config.PrioritizeTip)
				break
			}
			walkY += dy
		}
	}
	x, y, z := position.X, position.Y, position.Z
	placed := false
	for li, layer := range config.Layers {
		for j := 0; j < heights[li]; j++ {
			chosen, ok := layer.Provider.SampleState(random)
			if !ok {
				y += dy
				continue
			}
			state, stateOK := nameToStateID(chosen.Name, chosen.Properties)
			if stateOK && r.setBlock(x, y, z, state) {
				placed = true
			}
			y += dy
		}
	}
	return placed
}

// blockColumnTruncate mirrors BlockColumnFeature.truncate: remove `total-i`
// blocks from the layer heights, starting at layer 0 when the tip is
// prioritized (keeping the last layer) and at the last layer otherwise.
func blockColumnTruncate(heights []int, total, i int, prioritizeTip bool) {
	remaining := total - i
	step, start, limit := 1, 0, len(heights)
	if !prioritizeTip {
		step, start, limit = -1, len(heights)-1, -1
	}
	for idx := start; idx != limit && remaining > 0; idx += step {
		take := heights[idx]
		if take > remaining {
			take = remaining
		}
		remaining -= take
		heights[idx] -= take
	}
}

// placeSimpleRandomSelector replays simple_random_selector: one nextInt(size)
// draw picks a feature, placed at the same position with the same random.
func (r *decorationRegion) placeSimpleRandomSelector(random worldgen.RandomSource, position worldgen.FeaturePosition, config worldgen.SimpleRandomSelectorConfig, set *worldgen.FeatureSet) bool {
	if len(config.Features) == 0 {
		return false
	}
	pick := int(random.NextIntN(int32(len(config.Features))))
	return r.placeFeatureRef(random, position, config.Features[pick], set)
}

// placeFeatureRef runs an inline feature reference (configured feature name
// with optional inline placement modifiers) at the position.
func (r *decorationRegion) placeFeatureRef(random worldgen.RandomSource, position worldgen.FeaturePosition, ref worldgen.FeatureRef, set *worldgen.FeatureSet) bool {
	if len(ref.Placement) != 0 {
		placed := false
		inner := worldgen.FeatureRef{Name: ref.Name}
		_ = set.ForEachPlacementModifiers(ref.Name, ref.Placement, random, position, r.placementContext(nil), func(pos worldgen.FeaturePosition) error {
			if r.placeFeatureRef(random, pos, inner, set) {
				placed = true
			}
			return nil
		})
		return placed
	}
	if _, isConfigured := set.LookupConfigured(ref.Name); !isConfigured {
		if pf, isPlaced := set.Placed[ref.Name]; isPlaced {
			placed := false
			inner := worldgen.FeatureRef{Name: pf.Feature}
			_ = set.ForEachPlacementPosition(ref.Name, random, position, r.placementContext(nil), func(pos worldgen.FeaturePosition) error {
				if r.placeFeatureRef(random, pos, inner, set) {
					placed = true
				}
				return nil
			})
			return placed
		}
	}
	configured, ok := set.LookupConfigured(ref.Name)
	if !ok {
		return false
	}
	switch configured.Type {
	case "minecraft:simple_block":
		config, err := set.SimpleBlock(ref.Name)
		if err != nil {
			return false
		}
		return r.placeSimpleBlockFeature(random, position, config, set)
	case "minecraft:block_column":
		config, err := set.BlockColumn(ref.Name)
		if err != nil {
			return false
		}
		return r.placeBlockColumn(random, position, config, set)
	case "minecraft:simple_random_selector":
		config, err := set.SimpleRandomSelector(ref.Name)
		if err != nil {
			return false
		}
		return r.placeSimpleRandomSelector(random, position, config, set)
	case "minecraft:random_boolean_selector":
		config, err := set.RandomBooleanSelector(ref.Name)
		if err != nil {
			return false
		}
		chosen := config.FeatureFalse
		if random.NextBoolean() {
			chosen = config.FeatureTrue
		}
		return r.placeFeatureRef(random, position, chosen, set)
	case "minecraft:random_selector":
		return r.placeTreeSelector(set, random, position, ref.Name, r.placementContext(nil)) == nil
	case "minecraft:vegetation_patch", "minecraft:waterlogged_vegetation_patch":
		config, err := set.VegetationPatch(ref.Name)
		if err != nil {
			return false
		}
		return r.placeVegetationPatch(random, position, config, set, configured.Type == "minecraft:waterlogged_vegetation_patch")
	case "minecraft:pointed_dripstone":
		config, err := set.PointedDripstone(ref.Name)
		if err != nil {
			return false
		}
		return r.placePointedDripstone(set, random, position, config)
	case "minecraft:tree":
		config, err := set.Tree(ref.Name)
		if err != nil {
			return false
		}
		placed, err := r.placeTreeWithResult(set, random, position, config)
		return err == nil && placed
	case "minecraft:fallen_tree":
		config, err := set.FallenTree(ref.Name)
		if err != nil {
			return false
		}
		return r.placeFallenTree(set, random, position, config) == nil
	case "minecraft:huge_red_mushroom", "minecraft:huge_brown_mushroom":
		config, err := set.HugeMushroom(ref.Name)
		if err != nil {
			return false
		}
		return r.placeHugeMushroom(set, random, position, configured.Type, config) == nil
	case "minecraft:coral_tree", "minecraft:coral_claw", "minecraft:coral_mushroom":
		return r.placeCoralFeature(random, position, configured.Type, set)
	case "minecraft:bamboo":
		config, err := set.Probability(ref.Name)
		if err != nil {
			return false
		}
		return r.placeBamboo(random, position, config.Probability, set)
	case "minecraft:root_system":
		config, err := set.RootSystem(ref.Name)
		if err != nil {
			return false
		}
		return r.placeRootSystem(random, position, config, set)
	case "minecraft:sea_pickle":
		config, err := set.Count(ref.Name)
		if err != nil {
			return false
		}
		return r.placeSeaPickle(random, position, config, set)
	}
	noteNotReplayed("nested:" + configured.Type)
	return false
}

// placePatchVegetationFeature runs a patch's nested vegetation feature at
// the given origin (computed by the caller via patchVegetationPosition):
// VegetationPatchFeature.placeVegetation delegates to the nested placed
// feature at pos.relative(surface.opposite()) - the air cell above floor
// ground / below ceiling ground - while the waterlogged pool places INTO its
// water cells and waterlogs the placed state afterwards.
func (r *decorationRegion) placePatchVegetationFeature(random worldgen.RandomSource, origin worldgen.FeaturePosition, ref worldgen.FeatureRef, set *worldgen.FeatureSet, waterlogged bool) bool {
	if !r.placeFeatureRef(random, origin, ref, set) {
		return false
	}
	if waterlogged {
		r.waterlogStateAt(origin)
	}
	return true
}

// waterlogStateAt mirrors the waterlogged patch's post-placement fixup: if
// the state at the position has a waterlogged property set to false, flip it.
func (r *decorationRegion) waterlogStateAt(position worldgen.FeaturePosition) {
	state := r.getBlock(position.X, position.Y, position.Z)
	value, ok := stateByID(state)
	if !ok {
		return
	}
	props := value.Properties
	if props == nil {
		return
	}
	waterlogged, has := props["waterlogged"]
	if !has || waterlogged == "true" {
		return
	}
	fixed := make(map[string]string, len(props))
	for k, v := range props {
		fixed[k] = v
	}
	fixed["waterlogged"] = "true"
	if id, ok := nameToStateID(value.Name, fixed); ok {
		r.setBlock(position.X, position.Y, position.Z, id)
	}
}

// patchVegetationPosition computes where a patch's nested vegetation runs:
// for plain patches the air cell opposite the surface side of the ground
// cell; for waterlogged pools the water cell itself.
func patchVegetationPosition(ground [3]int, direction int, waterlogged bool) worldgen.FeaturePosition {
	if waterlogged {
		return worldgen.FeaturePosition{X: ground[0], Y: ground[1], Z: ground[2]}
	}
	return worldgen.FeaturePosition{X: ground[0], Y: ground[1] - direction, Z: ground[2]}
}

// extendSimpleBlockSurvival: vanilla SimpleBlockFeature delegates to each
// block's canSurvive; the cave set needs small_dripleaf (clay/moss below or
// a water source in the cell itself) on top of the shared supports tag.
func (r *decorationRegion) simpleBlockCanSurvive(position worldgen.FeaturePosition, name string, set *worldgen.FeatureSet) bool {
	if name == "minecraft:small_dripleaf" {
		if isWaterState(r.getBlock(position.X, position.Y, position.Z)) {
			return true
		}
		below, ok := stateByID(r.getBlock(position.X, position.Y-1, position.Z))
		if !ok {
			return false
		}
		return below.Name == "minecraft:clay" || below.Name == "minecraft:moss_block"
	}
	if name == "minecraft:spore_blossom" {
		if position.Y >= MinY+WorldHeight-1 {
			return false
		}
		above := r.getBlock(position.X, position.Y+1, position.Z)
		return fullSolidState(above) && !isWaterState(r.getBlock(position.X, position.Y, position.Z))
	}
	if name == "minecraft:brown_mushroom" || name == "minecraft:red_mushroom" {
		if position.Y <= MinY {
			return false
		}
		below, ok := stateByID(r.getBlock(position.X, position.Y-1, position.Z))
		return ok && flattenBlockTagContains(set, "minecraft:overrides_mushroom_light_requirement", below.Name)
	}
	if name == "minecraft:leaf_litter" {
		if position.Y <= MinY {
			return false
		}
		return isFaceSturdy(r.getBlock(position.X, position.Y-1, position.Z))
	}
	if name == "minecraft:dead_bush" || name == "minecraft:short_dry_grass" || name == "minecraft:tall_dry_grass" {
		if position.Y <= MinY {
			return false
		}
		below, ok := stateByID(r.getBlock(position.X, position.Y-1, position.Z))
		return ok && flattenBlockTagContains(set, "minecraft:supports_dry_vegetation", below.Name)
	}
	if name == "minecraft:azalea" || name == "minecraft:flowering_azalea" {
		if position.Y <= MinY {
			return false
		}
		below, ok := stateByID(r.getBlock(position.X, position.Y-1, position.Z))
		return ok && flattenBlockTagContains(set, "minecraft:supports_azalea", below.Name)
	}
	if name == "minecraft:lily_pad" {
		if position.Y <= MinY {
			return false
		}
		at := r.getBlock(position.X, position.Y, position.Z)
		if isWaterState(at) || isLavaState(at) {
			return false
		}
		belowID := r.getBlock(position.X, position.Y-1, position.Z)
		if isWaterState(belowID) {
			return true
		}
		below, ok := stateByID(belowID)
		return ok && (below.Name == "minecraft:ice" || below.Name == "minecraft:frosted_ice")
	}
	if name == "minecraft:melon" || name == "minecraft:pumpkin" {
		return true
	}
	return r.canVegetationSurvive(position, name, set)
}

// placeScheduledFreezeTopLayer ports Stage 10 (TOP_LAYER_MODIFICATION)
// SnowAndFreezeFeature (minecraft:freeze_top_layer): for each column in the
// source chunk, it checks MOTION_BLOCKING height y and belowY = y - 1 against
// Biome.shouldFreeze and Biome.shouldSnow, freezing still water to ice and
// placing snow[layers=1] (plus setting snowy=true on grass_block, podzol, or
// mycelium below).
func (r *decorationRegion) placeScheduledFreezeTopLayer(seed int64) error {
	r.bindSeed(seed)
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		return err
	}
	iceID, ok := nameToStateID("minecraft:ice", nil)
	if !ok {
		return fmt.Errorf("missing block state for minecraft:ice")
	}
	snowID, ok := nameToStateID("minecraft:snow", map[string]string{"layers": "1"})
	if !ok {
		return fmt.Errorf("missing block state for minecraft:snow[layers=1]")
	}
	baseX, baseZ := int(r.sourceX)*16, int(r.sourceZ)*16
	for dx := 0; dx < 16; dx++ {
		for dz := 0; dz < 16; dz++ {
			x, z := baseX+dx, baseZ+dz
			y := r.heightAt("MOTION_BLOCKING", x, z)
			belowY := y - 1
			biomeID, ok := r.getBiome(x, y, z)
			if !ok {
				continue
			}
			biome := biomeNameByID(biomeID)
			if belowY >= MinY && belowY < MinY+WorldHeight && worldgen.ColdEnoughToSnowAt(biome, x, belowY, z, SeaLevel) {
				if r.getBlock(x, belowY, z) == StateWater {
					r.setBlock(x, belowY, z, iceID)
				}
			}
			if y >= MinY && y < MinY+WorldHeight && belowY >= MinY && worldgen.ShouldSnowAt(biome, x, y, z, SeaLevel) {
				at := r.getBlock(x, y, z)
				below := r.getBlock(x, belowY, z)
				if isAirState(at) && snowLayerCanSurviveOn(below, set) {
					r.setBlock(x, y, z, snowID)
					if belowState, ok := stateByID(below); ok && belowState.Properties["snowy"] == "false" {
						props := make(map[string]string, len(belowState.Properties))
						for k, v := range belowState.Properties {
							props[k] = v
						}
						props["snowy"] = "true"
						if snowyID, ok := nameToStateID(belowState.Name, props); ok {
							r.setBlock(x, belowY, z, snowyID)
						}
					}
				}
			}
		}
	}
	return nil
}

func snowLayerCanSurviveOn(below uint16, set *worldgen.FeatureSet) bool {
	s, ok := stateByID(below)
	if !ok {
		return false
	}
	if flattenBlockTagContains(set, "minecraft:snow_layer_cannot_survive_on", s.Name) {
		return false
	}
	if flattenBlockTagContains(set, "minecraft:snow_layer_can_survive_on", s.Name) {
		return true
	}
	return isFaceSturdy(below)
}

