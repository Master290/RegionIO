package world

import (
	"fmt"

	"regionio/internal/worldgen"
)

// Tree placement through the region. Every loop bound, draw and test below was read
// from javap -p -c output against versions/26.1.2/server-26.1.2.jar for TreeFeature,
// FoliagePlacer, StraightTrunkPlacer, GiantTrunkPlacer, BlobFoliagePlacer and
// PineFoliagePlacer; nothing here is reconstructed from what trees.go used to do.
//
// It replaces trees.go, which hand-wrote a trunk-and-blob approximation and got
// three things wrong in three different ways: it clipped canopies to x,z in [2,13)
// so a tree could never cross a chunk border (vanilla has no border test -
// TreeFeature.doPlace writes wherever it likes and WorldGenRegion only refuses
// beyond ChunkStep.blockStateWriteRadius, 1 chunk for FEATURES, which
// decorationRegion.setBlock already enforces); it wrote dirt under every trunk
// unconditionally (vanilla asks below_trunk_provider, which leaves the block alone
// when it is in cannot_replace_below_tree_trunk); and it drew positions from
// count+2*nextIntN(16) instead of the placed feature's modifier chain.
//
// Draw ordering is the reason this file must be exact rather than close. Both row
// loops use integer division on a value that can go negative, which Java truncates
// toward zero and Go also truncates toward zero - so that agrees - but a rejected
// cell must not consume a provider draw, and the clay chain showed repeatedly that
// one changed draw count moves every later position in the chunk.

// treePlacer holds one tree: the region, the RNG, the parsed config, and the two
// heights sampled before any block is written.
type treePlacer struct {
	r      *decorationRegion
	set    *worldgen.FeatureSet
	random worldgen.RandomSource
	config worldgen.TreeFeatureConfig

	// trunkHeight, foliageHeight and foliageRadius are sampled in that order by
	// TreeFeature.doPlace; the per-attachment offset comes after them, drawn once
	// per FoliageAttachment by FoliagePlacer's public createFoliage wrapper.
	trunkHeight   int
	foliageHeight int
	foliageRadius int
}

// newTreePlacer performs doPlace's sampling preamble, in doPlace's order, before a
// single block is written.
func (t *treePlacer) sampleHeights() error {
	height, err := t.trunkHeightFromPlacer()
	if err != nil {
		return err
	}
	t.trunkHeight = height

	switch t.config.FoliagePlacer.Type {
	case "minecraft:blob_foliage_placer":
		// BlobFoliagePlacer.foliageHeight returns its int field without drawing.
		h, ok := t.config.FoliagePlacer.Scalar("height")
		if !ok {
			return fmt.Errorf("world: blob foliage placer has no scalar height")
		}
		t.foliageHeight = h
	case "minecraft:pine_foliage_placer":
		// PineFoliagePlacer.foliageHeight samples its provider: one draw.
		t.foliageHeight = t.foliageProviderField("height").Sample(t.random)
	default:
		return fmt.Errorf("world: unimplemented foliage_placer %q for a tree being placed", t.config.FoliagePlacer.Type)
	}

	base, err := t.foliagePlacerBaseRadius()
	if err != nil {
		return err
	}
	t.foliageRadius = base
	if t.config.FoliagePlacer.Type == "minecraft:pine_foliage_placer" {
		// PineFoliagePlacer.foliageRadius = super.foliageRadius(...) +
		// random.nextInt(max(1, trunkHeight + 1)); an extra draw the blob has not.
		span := t.trunkHeight + 1
		if span < 1 {
			span = 1
		}
		t.foliageRadius += int(t.random.NextIntN(int32(span)))
	}
	return nil
}

// trunkHeightFromPlacer is TrunkPlacer.getTreeHeight:
// base_height + nextInt(height_rand_a + 1) + nextInt(height_rand_b + 1), where a
// zero bound still draws nextInt(1) - the draw happens because the bound is added
// before the test, so both ranges are always consumed.
func (t *treePlacer) trunkHeightFromPlacer() (int, error) {
	base, ok := t.config.TrunkPlacer.Scalar("base_height")
	if !ok {
		return 0, fmt.Errorf("world: %q has no scalar base_height", t.config.TrunkPlacer.Type)
	}
	// getTreeHeight adds both nextInt(rand+1) terms unconditionally, so a zero
	// bound still consumes a draw; skipping it here would desynchronise every
	// later position in the feature.
	height := base
	for _, field := range []string{"height_rand_a", "height_rand_b"} {
		bound, ok := t.config.TrunkPlacer.Scalar(field)
		if !ok {
			return 0, fmt.Errorf("world: %q has no scalar %s", t.config.TrunkPlacer.Type, field)
		}
		height += int(t.random.NextIntN(int32(bound + 1)))
	}
	return height, nil
}

func (t *treePlacer) foliagePlacerBaseRadius() (int, error) {
	provider, ok, err := t.config.FoliagePlacer.Provider("radius")
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("world: %q has no radius field", t.config.FoliagePlacer.Type)
	}
	return provider.Sample(t.random), nil
}

func (t *treePlacer) foliageProviderField(name string) worldgen.NestedIntProvider {
	provider, _, err := t.config.FoliagePlacer.Provider(name)
	if err != nil {
		return worldgen.NestedIntProvider{Type: "constant"}
	}
	return provider
}

func (t *treePlacer) stateAt(x, y, z int) uint16 { return t.r.getBlock(x, y, z) }

// validTreePos is TreeFeature.validTreePos: air, or in LEAVES, or in
// REPLACEABLE_BY_TREES. Membership is resolved per block, so a waterlogged, snowy
// or aged variant counts - the mistake that silently disabled the moss patches.
func (t *treePlacer) validTreePos(x, y, z int) bool {
	id := t.stateAt(x, y, z)
	if isAirState(id) {
		return true
	}
	name, ok := stateByID(id)
	if !ok {
		return false
	}
	return flattenBlockTagContains(t.set, "minecraft:leaves", name.Name) ||
		flattenBlockTagContains(t.set, "minecraft:replaceable_by_trees", name.Name)
}

// isPersistentLeaf is the guard tryPlaceLeaf tests first. getValueOrElse on a block
// without the property yields false, so ordinary terrain is never "persistent" and
// only persistent leaves are protected.
func (t *treePlacer) isPersistentLeaf(x, y, z int) bool {
	name, ok := stateByID(t.stateAt(x, y, z))
	if !ok {
		return false
	}
	return name.Properties["persistent"] == "true"
}

// placeLeaf is FoliagePlacer.tryPlaceLeaf.
//
// The order is the whole point: the position tests run BEFORE
// foliageProvider.getState, so a rejected cell never draws. Sampling first would
// shift every later draw in the feature.
func (t *treePlacer) placeLeaf(x, y, z int) (bool, error) {
	if t.isPersistentLeaf(x, y, z) {
		return false, nil
	}
	if !t.validTreePos(x, y, z) {
		return false, nil
	}
	spec, err := t.set.StateProvider(t.config.FoliageProvider.Raw)
	if err != nil {
		return false, err
	}
	state, err := t.r.sampleStateProvider(t.set, spec, t.random, worldgen.FeaturePosition{X: x, Y: y, Z: z})
	if err != nil {
		return false, err
	}
	name, ok := stateByID(state)
	if !ok {
		return false, fmt.Errorf("world: foliage provider returned unknown state %d", state)
	}
	if _, hasWaterlogged := name.Properties["waterlogged"]; hasWaterlogged {
		// isFluidAtPosition(FluidState::isSourceOfType(WATER)): source water only,
		// so flowing water beside a canopy does not wet it.
		wet := isWaterSourceState(t.stateAt(x, y, z))
		merged := map[string]string{}
		for k, v := range name.Properties {
			merged[k] = v
		}
		if wet {
			merged["waterlogged"] = "true"
		} else {
			merged["waterlogged"] = "false"
		}
		if resolved, ok := nameToStateID(name.Name, merged); ok {
			state = resolved
		}
	}
	t.r.setBlock(x, y, z, state)
	return true, nil
}

// placeLeavesRow is FoliagePlacer.placeLeavesRow: the square from -radius to
// radius+extra, extra being 1 only for a double trunk, offset from the attachment
// position by (dx, y, dz).
func (t *treePlacer) placeLeavesRow(at trunkAttachment, radius, y int) error {
	extra := 0
	if at.doubleTrunk {
		extra = 1
	}
	for dx := -radius; dx <= radius+extra; dx++ {
		for dz := -radius; dz <= radius+extra; dz++ {
			if t.shouldSkipLocationSigned(dx, dz, radius, at.doubleTrunk) {
				continue
			}
			if _, err := t.placeLeaf(at.x+dx, at.y+y, at.z+dz); err != nil {
				return err
			}
		}
	}
	return nil
}

// shouldSkipLocationSigned measures distance to the nearer of two trunk columns
// when a giant tree's canopy covers both.
func (t *treePlacer) shouldSkipLocationSigned(dx, dz, radius int, doubleTrunk bool) bool {
	ax, az := abs(dx), abs(dz)
	if doubleTrunk {
		ax = min(ax, abs(dx-1))
		az = min(az, abs(dz-1))
	}
	return t.shouldSkipLocation(ax, az, radius)
}

// shouldSkipLocation is per placer type, and both implemented cases are read from
// bytecode. Neither takes y, matching the Java signature's unused height argument.
func (t *treePlacer) shouldSkipLocation(ax, az, radius int) bool {
	switch t.config.FoliagePlacer.Type {
	case "minecraft:blob_foliage_placer":
		// Only exact corners are candidates, and only then does it draw.
		if ax != radius || az != radius {
			return false
		}
		return t.random.NextIntN(2) != 0
	case "minecraft:pine_foliage_placer":
		// Pure geometry, no draw at all.
		return ax == radius && az == radius && radius > 0
	}
	return false
}

// placeBelowTrunk asks below_trunk_provider what belongs under the trunk, including
// the "stay as you are" answer a rule-based provider gives when no rule matched and
// it declares no fallback.
func (t *treePlacer) placeBelowTrunk(x, y, z int) error {
	if len(t.config.BelowTrunkProvider) == 0 {
		return nil
	}
	spec, err := t.set.StateProvider(t.config.BelowTrunkProvider)
	if err != nil {
		return err
	}
	state, err := t.r.sampleStateProvider(t.set, spec, t.random, worldgen.FeaturePosition{X: x, Y: y, Z: z})
	if err != nil {
		return err
	}
	t.r.setBlock(x, y, z, state)
	return nil
}

func (t *treePlacer) placeLogAt(x, y, z int) error {
	spec, err := t.set.StateProvider(t.config.TrunkProvider.Raw)
	if err != nil {
		return err
	}
	state, err := t.r.sampleStateProvider(t.set, spec, t.random, worldgen.FeaturePosition{X: x, Y: y, Z: z})
	if err != nil {
		return err
	}
	t.r.setBlock(x, y, z, state)
	return nil
}

// trunkAttachment is one FoliagePlacer.FoliageAttachment.
type trunkAttachment struct {
	x, y, z     int
	radiusExtra int
	doubleTrunk bool
}

// placeTrunk runs the trunk placer, then paints each canopy it reports.
func (t *treePlacer) placeTrunk(x, y, z int) error {
	var attachments []trunkAttachment
	switch t.config.TrunkPlacer.Type {
	case "minecraft:straight_trunk_placer":
		if err := t.placeBelowTrunk(x, y-1, z); err != nil {
			return err
		}
		for i := 0; i < t.trunkHeight; i++ {
			if err := t.placeLogAt(x, y+i, z); err != nil {
				return err
			}
		}
		// StraightTrunkPlacer returns one attachment at pos.above(height) with
		// radius offset 0 and doubleTrunk false, and consumes no draws.
		attachments = []trunkAttachment{{x: x, y: y + t.trunkHeight, z: z}}
	case "minecraft:giant_trunk_placer":
		corners := [][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}}
		for _, c := range corners {
			if err := t.placeBelowTrunk(x+c[0], y-1, z+c[1]); err != nil {
				return err
			}
		}
		for i := 0; i < t.trunkHeight; i++ {
			for _, c := range corners {
				if !t.validTreePos(x+c[0], y+i, z+c[1]) {
					continue
				}
				if err := t.placeLogAt(x+c[0], y+i, z+c[1]); err != nil {
					return err
				}
			}
		}
		attachments = []trunkAttachment{{x: x, y: y + t.trunkHeight, z: z, radiusExtra: 1, doubleTrunk: true}}
	default:
		return fmt.Errorf("world: unimplemented trunk_placer %q for a tree being placed", t.config.TrunkPlacer.Type)
	}

	for _, at := range attachments {
		// FoliagePlacer's public createFoliage samples the offset provider once per
		// attachment and passes it down as the row the canopy starts at.
		offset, err := t.foliagePlacerOffset()
		if err != nil {
			return err
		}
		if err := t.placeFoliage(at, offset); err != nil {
			return err
		}
	}
	return nil
}

func (t *treePlacer) foliagePlacerOffset() (int, error) {
	provider, ok, err := t.config.FoliagePlacer.Provider("offset")
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("world: %q has no offset field", t.config.FoliagePlacer.Type)
	}
	return provider.Sample(t.random), nil
}

// placeFoliage paints one canopy. Both loops are transcribed from bytecode: rows
// are y-OFFSETS from the attachment position, counted down from the sampled offset
// to offset - radius, so the canopy hangs around the trunk top rather than above it.
func (t *treePlacer) placeFoliage(at trunkAttachment, offset int) error {
	switch t.config.FoliagePlacer.Type {
	case "minecraft:blob_foliage_placer":
		height, ok := t.config.FoliagePlacer.Scalar("height")
		if !ok {
			return fmt.Errorf("world: blob foliage placer has no scalar height")
		}
		for rowY := offset; rowY >= offset-t.foliageRadius; rowY-- {
			// Math.max(0, height + radiusOffset - 1 - rowY/2), with rowY/2
			// truncating toward zero exactly as Java's integer division does.
			radius := max(0, height+at.radiusExtra-1-rowY/2)
			if err := t.placeLeavesRow(at, radius, rowY); err != nil {
				return err
			}
		}
	case "minecraft:pine_foliage_placer":
		layerRadius := 0
		for rowY := offset; rowY >= offset-t.foliageRadius; rowY-- {
			if err := t.placeLeavesRow(at, layerRadius, rowY); err != nil {
				return err
			}
			switch {
			case layerRadius < 1:
				layerRadius++
			case rowY == offset-t.foliageRadius+1:
				// One row before the bottom the cone closes again.
				layerRadius--
			case layerRadius < t.foliageHeight+at.radiusExtra:
				layerRadius++
			}
		}
	default:
		return fmt.Errorf("world: unimplemented foliage_placer %q for a tree being placed", t.config.FoliagePlacer.Type)
	}
	return nil
}

func isWaterSourceState(id uint16) bool {
	name, ok := stateByID(id)
	if !ok {
		return false
	}
	return name.Name == "minecraft:water" && name.Properties["level"] == "0"
}
