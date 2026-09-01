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
// with an empty placement) at the position.
func (r *decorationRegion) placeFeatureRef(random worldgen.RandomSource, position worldgen.FeaturePosition, ref worldgen.FeatureRef, set *worldgen.FeatureSet) bool {
	if len(ref.Placement) != 0 {
		return false
	}
	configured, ok := set.Configured[ref.Name]
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
	case "minecraft:vegetation_patch", "minecraft:waterlogged_vegetation_patch":
		config, err := set.VegetationPatch(ref.Name)
		if err != nil {
			return false
		}
		return r.placeVegetationPatch(random, position, config, set, configured.Type == "minecraft:waterlogged_vegetation_patch")
	}
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
	return r.canVegetationSurvive(position, name, set)
}

var _ = fmt.Sprintf
