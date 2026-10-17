package world

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"regionio/internal/worldgen"
)

// Tree placement through the region. Every loop bound, draw and test below was read
// from javap -p -c output against versions/26.1.2/server-26.1.2.jar for TreeFeature,
// FoliagePlacer, TrunkPlacer, StraightTrunkPlacer, GiantTrunkPlacer,
// BlobFoliagePlacer, PineFoliagePlacer, SpruceFoliagePlacer, MegaPineFoliagePlacer
// and FancyFoliagePlacer; nothing here is reconstructed from what trees.go used to
// do. The header of a method in that dump is the only argument list that counts:
// createFoliage's trailing (foliageHeight, foliageRadius, offset) trio is easy to
// read backwards, and doing so shortens or lengthens a canopy by the difference
// between those two numbers.
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

// drawFreeCell marks a cell whose processing must not consume a draw, because
// vanilla refuses it before reaching any provider: a persistent leaf, or a position
// that fails the placer's own skip test.
//
// The rule that a rejected cell is free is what keeps a tree's later positions - and
// therefore every later feature in the chunk - stable. It is easy to break by
// sampling a provider to find out whether the result would fit, and every breakage
// shows up far away from where it was introduced.
type drawFreeCell int

const (
	cellIsPlaceable drawFreeCell = iota
	cellIsPersistentLeaf
	cellIsSkippedByPlacer
)

// treePlacement is one recorded cell of TreeDecorator.Context, tagged so a decorator
// that has to look at logs can ignore leaves without a second world query.
type treePlacement struct {
	x, y, z int
	isLog   bool
	isRoot  bool
	draw    drawFreeCell
}

// treePlacer holds one tree: the region, the RNG, the parsed config, and the two
// heights sampled before any block is written.
type treePlacer struct {
	r      *decorationRegion
	set    *worldgen.FeatureSet
	random worldgen.RandomSource
	config worldgen.TreeFeatureConfig

	// placements records what this tree wrote, in write order. TreeDecorator.Context
	// rebuilds its logs and leaves lists from exactly this, and both AlterGround-
	// Decorator and BeehiveDecorator read y values out of the lists.
	placements    []treePlacement
	leafPositions map[[3]int]bool

	// trunkHeight, foliageHeight and foliageRadius are sampled in that order by
	// TreeFeature.doPlace; the per-attachment offset comes after them, drawn once
	// per FoliageAttachment by FoliagePlacer's public createFoliage wrapper.
	//
	// doPlace passes them down as createFoliage(level, setter, random, config,
	// height, attachment, foliageHeight, foliageRadius, offset), so a placer body
	// reads the row count from foliageHeight and the width from foliageRadius - not
	// the other way round, which is how this file first had them.
	trunkHeight        int
	foliageHeight      int
	foliageRadius      int
	heightAboveFoliage int // doPlace's (trunkHeight - foliageHeight), the foliageRadius argument
}

// sampleHeights performs doPlace's sampling preamble, in doPlace's order, before a
// single block is written.
func (t *treePlacer) sampleHeights() error {
	height, err := t.trunkHeightFromPlacer()
	if err != nil {
		return err
	}
	t.trunkHeight = height

	switch t.config.FoliagePlacer.Type {
	case "minecraft:blob_foliage_placer", "minecraft:fancy_foliage_placer",
		"minecraft:bush_foliage_placer", "minecraft:jungle_foliage_placer":
		// BlobFoliagePlacer.foliageHeight returns its int field without drawing, and
		// FancyFoliagePlacer, BushFoliagePlacer, and MegaJungleFoliagePlacer follow suit.
		h, ok := t.config.FoliagePlacer.Scalar("height")
		if !ok {
			return fmt.Errorf("world: %s has no scalar height", t.config.FoliagePlacer.Type)
		}
		t.foliageHeight = h
	case "minecraft:pine_foliage_placer", "minecraft:cherry_foliage_placer":
		// PineFoliagePlacer and CherryFoliagePlacer sample their height provider.
		h, err := t.foliageProviderSample("height")
		if err != nil {
			return err
		}
		t.foliageHeight = h
	case "minecraft:random_spread_foliage_placer":
		h, err := t.foliageProviderSample("foliage_height")
		if err != nil {
			return err
		}
		t.foliageHeight = h
	case "minecraft:spruce_foliage_placer":
		// Math.max(4, trunkHeight - trunk_height.sample(random)): one draw, and the
		// 4 floors the value, not the draw.
		h, err := t.foliageProviderSample("trunk_height")
		if err != nil {
			return err
		}
		t.foliageHeight = max(4, t.trunkHeight-h)
	case "minecraft:mega_pine_foliage_placer":
		h, err := t.foliageProviderSample("crown_height")
		if err != nil {
			return err
		}
		t.foliageHeight = h
	case "minecraft:acacia_foliage_placer":
		t.foliageHeight = 0
	case "minecraft:dark_oak_foliage_placer":
		// DarkOakFoliagePlacer.foliageHeight is `iconst_4; ireturn`: the RandomSource
		// argument is never read, so this is a constant, not a sample. Its
		// createFoliage then ignores the value too - the rows are fixed - but
		// doPlace's own bookkeeping still uses it.
		t.foliageHeight = 4
	default:
		// What this reports is the first un-modelled part in sampling order, not the set
		// of parts that would fail: trunkHeightFromPlacer implements the generic
		// TrunkPlacer.getTreeHeight and so succeeds even for a trunk this build cannot
		// place, which is why a dark_oak tree is counted here as foliage_placer and never
		// reaches the trunk refusal in placeTrunk. Measured in the pack, the 5 configs
		// carrying dark_oak_foliage_placer are exactly the 5 carrying
		// dark_oak_trunk_placer - so a tally line names one class and the work needs two.
		return &unmodelledPart{kind: "foliage_placer", name: t.config.FoliagePlacer.Type}
	}
	t.heightAboveFoliage = t.trunkHeight - t.foliageHeight

	base, err := t.foliagePlacerBaseRadius()
	if err != nil {
		return err
	}
	t.foliageRadius = base
	if t.config.FoliagePlacer.Type == "minecraft:pine_foliage_placer" {
		// PineFoliagePlacer.foliageRadius = super.foliageRadius(random, i) +
		// random.nextInt(max(1, i + 1)), and the i doPlace hands it is
		// trunkHeight - foliageHeight rather than the trunk height: an extra draw
		// the blob has not, on a bound that differs from the trunk height almost
		// always.
		span := t.heightAboveFoliage + 1
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

func (t *treePlacer) foliageProviderSample(name string) (int, error) {
	provider, ok, err := t.config.FoliagePlacer.Provider(name)
	if err != nil {
		return 0, fmt.Errorf("world: %s: %s is unreadable: %w", t.config.FoliagePlacer.Type, name, err)
	}
	if !ok {
		return 0, fmt.Errorf("world: %s has no %s", t.config.FoliagePlacer.Type, name)
	}
	return provider.Sample(t.random), nil
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
// shift every later draw in the feature. A cell the placer's own skip test refused
// never even reaches tryPlaceLeaf, which is why it arrives as a tag rather than as a
// world query.
func (t *treePlacer) placeLeaf(x, y, z int, skip drawFreeCell) (bool, error) {
	if skip != cellIsPlaceable {
		return false, nil
	}
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
	if t.leafPositions == nil {
		t.leafPositions = make(map[[3]int]bool)
	}
	t.leafPositions[[3]int{x, y, z}] = true
	// TreeFeature$1.set records every leaf it writes, whether or not the world
	// accepted it, and BeehiveDecorator reads the first of them.
	t.placements = append(t.placements, treePlacement{x: x, y: y, z: z})
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
			skip := cellIsPlaceable
			if t.shouldSkipLocationSigned(dx, y, dz, radius, at.doubleTrunk) {
				skip = cellIsSkippedByPlacer
			}
			if _, err := t.placeLeaf(at.x+dx, at.y+y, at.z+dz, skip); err != nil {
				return err
			}
		}
	}
	return nil
}

// placeLeavesRowWithHangingLeavesBelow is FoliagePlacer.placeLeavesRowWithHangingLeavesBelow,
// used by CherryFoliagePlacer on its bottom two rows.
func (t *treePlacer) placeLeavesRowWithHangingLeavesBelow(at trunkAttachment, radius, y int, hangingLeavesChance, hangingLeavesExtensionChance float32) error {
	if err := t.placeLeavesRow(at, radius, y); err != nil {
		return err
	}
	extra := 0
	if at.doubleTrunk {
		extra = 1
	}
	logPos := [3]int{at.x, at.y - 1, at.z}
	for dirIdx, alongEdge := range darkOakDirections {
		toEdge := darkOakDirections[(dirIdx+1)&3]
		offsetToEdge := radius
		if toEdge[0] > 0 || toEdge[1] > 0 {
			offsetToEdge = radius + extra
		}
		px := at.x + toEdge[0]*offsetToEdge - alongEdge[0]*radius
		py := at.y + y - 1
		pz := at.z + toEdge[1]*offsetToEdge - alongEdge[1]*radius
		for offsetAlongEdge := -radius; offsetAlongEdge < radius+extra; offsetAlongEdge++ {
			if t.leafPositions[[3]int{px, py + 1, pz}] {
				placed, err := t.tryPlaceExtension(hangingLeavesChance, logPos, px, py, pz)
				if err != nil {
					return err
				}
				if placed {
					if _, err := t.tryPlaceExtension(hangingLeavesExtensionChance, logPos, px, py-1, pz); err != nil {
						return err
					}
				}
			}
			px += alongEdge[0]
			pz += alongEdge[1]
		}
	}
	return nil
}

func (t *treePlacer) tryPlaceExtension(chance float32, logPos [3]int, x, y, z int) (bool, error) {
	if abs(x-logPos[0])+abs(y-logPos[1])+abs(z-logPos[2]) >= 7 {
		return false, nil
	}
	if t.random.NextFloat() > chance {
		return false, nil
	}
	return t.placeLeaf(x, y, z, cellIsPlaceable)
}

// shouldSkipLocationSigned measures distance to the nearer of two trunk columns
// when a giant tree's canopy covers both.
func (t *treePlacer) shouldSkipLocationSigned(dx, y, dz, radius int, doubleTrunk bool) bool {
	// DarkOakFoliagePlacer overrides this method, and its rule reads the SIGNED
	// coordinates against the row's own radius - the fold below happens in super,
	// which it then still calls. It applies only to the middle row of a double
	// trunk, and only in both axes at once.
	if t.config.FoliagePlacer.Type == "minecraft:dark_oak_foliage_placer" && y == 0 && doubleTrunk &&
		(dx == -radius || dx >= radius) && (dz == -radius || dz >= radius) {
		return true
	}
	ax, az := abs(dx), abs(dz)
	if doubleTrunk {
		ax = min(ax, abs(dx-1))
		az = min(az, abs(dz-1))
	}
	return t.shouldSkipLocation(ax, y, az, radius, doubleTrunk)
}

// shouldSkipLocation is per placer type, and each implemented case is read from
// bytecode. The y argument is the row offset, which blob uses and the cone
// placers do not.
func (t *treePlacer) shouldSkipLocation(ax, y, az, radius int, doubleTrunk bool) bool {
	switch t.config.FoliagePlacer.Type {
	case "minecraft:blob_foliage_placer":
		// Only exact corners are candidates, and only then does it draw. The corner
		// is dropped when the draw says so, but kept on every non-bottom row:
		// nextInt(2) == 0 || y == 0.
		if ax != radius || az != radius {
			return false
		}
		return t.random.NextIntN(2) == 0 || y == 0
	case "minecraft:bush_foliage_placer":
		return ax == radius && az == radius && t.random.NextIntN(2) == 0
	case "minecraft:acacia_foliage_placer":
		if y == 0 {
			return (ax > 1 || az > 1) && ax != 0 && az != 0
		}
		return ax == radius && az == radius && radius > 0
	case "minecraft:fancy_foliage_placer":
		// A round canopy: skip outside the circle of the row radius, measured from
		// the cell centre rather than the corner.
		return square(float32(ax)+0.5)+square(float32(az)+0.5) > float32(radius*radius)
	case "minecraft:pine_foliage_placer", "minecraft:spruce_foliage_placer":
		// Pure geometry, no draw at all, and identical in both classes.
		return ax == radius && az == radius && radius > 0
	case "minecraft:mega_pine_foliage_placer", "minecraft:jungle_foliage_placer":
		// Two tests: the far corner of the double-trunk square, then the circle.
		return ax+az >= 7 || ax*ax+az*az > radius*radius
	case "minecraft:cherry_foliage_placer":
		wideBottom, _ := t.config.FoliagePlacer.Float("wide_bottom_layer_hole_chance")
		cornerHole, _ := t.config.FoliagePlacer.Float("corner_hole_chance")
		if y == -1 && (ax == radius || az == radius) && t.random.NextFloat() < float32(wideBottom) {
			return true
		}
		corner := ax == radius && az == radius
		if radius > 2 {
			return corner || (ax+az > radius*2-2 && t.random.NextFloat() < float32(cornerHole))
		}
		return corner && t.random.NextFloat() < float32(cornerHole)
	case "minecraft:dark_oak_foliage_placer":
		// Two row rules and nothing else, both on folded coordinates against the
		// row's own radius. The bottom row of a SINGLE trunk loses exactly its far
		// corner; and the row one above the middle loses a diagonal band, whether or
		// not the trunk is double - which is why doubleTrunk is tested only in the
		// first arm.
		if y == -1 {
			return !doubleTrunk && ax == radius && az == radius
		}
		if y == 1 {
			return ax+az > 2*radius-2
		}
		return false
	}
	return false
}

func square(v float32) float32 { return v * v }

// unmodelledPart says a configured tree named a placer or decorator this build has not
// read from the jar. It is a distinct error rather than a fmt.Errorf because the
// region decorator walks a 5x5 block of sources, so a tree chain belonging to a
// neighbouring biome must not abort the chunk being generated: the arm turns this into
// a counted gap and carries on, while every other error still propagates.
type unmodelledPart struct {
	kind string
	name string
}

func (u *unmodelledPart) Error() string {
	return fmt.Sprintf("world: %s %q is not modelled", u.kind, u.name)
}

func (u *unmodelledPart) Is(target error) bool { return target == errUnmodelled }

// errUnmodelled is the sentinel unmodelledPart matches.
var errUnmodelled = errors.New("unmodelled")

// placeBelowTrunk is TrunkPlacer.placeBelowTrunkBlock, which asks
// below_trunk_provider's getOptionalState: when no rule matched and the provider
// declares no fallback it writes nothing at all, rather than writing back the
// block that is already there.
func (t *treePlacer) placeBelowTrunk(x, y, z int) error {
	if len(t.config.BelowTrunkProvider) == 0 {
		return nil
	}
	spec, err := t.set.StateProvider(t.config.BelowTrunkProvider)
	if err != nil {
		return err
	}
	position := worldgen.FeaturePosition{X: x, Y: y, Z: z}
	state, ok, err := t.r.sampleOptionalStateProvider(t.set, spec, t.random, position)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	t.r.setBlock(x, y, z, state)
	t.placements = append(t.placements, treePlacement{x: x, y: y, z: z, isLog: true})
	return nil
}

// placeLogAt is TrunkPlacer.placeLog: validTreePos is tested before the trunk
// provider is sampled, so a cell the trunk cannot grow through costs no draw.
func (t *treePlacer) placeLogAt(x, y, z int) error {
	_, err := t.placeLogBoolWithAxis(x, y, z, "")
	return err
}

func (t *treePlacer) placeLogBoolAt(x, y, z int) (bool, error) {
	return t.placeLogBoolWithAxis(x, y, z, "")
}

// placeLogWithAxis is placeLog with the Function<BlockState,BlockState> argument,
// which FancyTrunkPlacer uses to re-orient each log along its limb. A block without
// an axis property is left alone, because the bytecode calls trySetValue.
func (t *treePlacer) placeLogWithAxis(x, y, z int, axis string) error {
	_, err := t.placeLogBoolWithAxis(x, y, z, axis)
	return err
}

func (t *treePlacer) placeLogBoolWithAxis(x, y, z int, axis string) (bool, error) {
	if !t.validTreePos(x, y, z) {
		return false, nil
	}
	spec, err := t.set.StateProvider(t.config.TrunkProvider.Raw)
	if err != nil {
		return false, err
	}
	state, err := t.r.sampleStateProvider(t.set, spec, t.random, worldgen.FeaturePosition{X: x, Y: y, Z: z})
	if err != nil {
		return false, err
	}
	if axis != "" {
		name, ok := stateByID(state)
		if !ok {
			return false, fmt.Errorf("world: trunk provider returned unknown state %d", state)
		}
		if _, has := name.Properties["axis"]; has {
			merged := map[string]string{}
			for k, v := range name.Properties {
				merged[k] = v
			}
			merged["axis"] = axis
			if resolved, ok := nameToStateID(name.Name, merged); ok {
				state = resolved
			}
		}
	}
	t.r.setBlock(x, y, z, state)
	// The trunk placer's setter is the BiConsumer that fills Context's logs list, so
	// every log - and the block under it - is part of what a decorator sees.
	t.placements = append(t.placements, treePlacement{x: x, y: y, z: z, isLog: true})
	return true, nil
}

// supportsAllParts answers, before a single block is written, whether every placer and
// decorator this config names has been modelled.
//
// Without it an un-modelled canopy left a finished trunk behind: placeTrunk paints the
// logs and only then does the foliage switch fail, so a dark oak in a border forest put
// 57 log cells into the land chunks and no leaves anywhere, which is worse than not
// placing the tree - it is a trunk with nothing on it, plus a stream that has already
// spent its draws. The check is pure, so the tree either places as a whole or not at all.
func (t *treePlacer) supportsAllParts() error {
	// Type lookups only. Asking the placer for its height would sample the two
	// nextInt calls getTreeHeight makes, and a check that runs before the decision has
	// to leave the stream exactly where it found it - the same rule that makes a
	// rejected cell free everywhere else in this file.
	switch t.config.TrunkPlacer.Type {
	case "minecraft:straight_trunk_placer", "minecraft:giant_trunk_placer",
		"minecraft:fancy_trunk_placer", "minecraft:dark_oak_trunk_placer",
		"minecraft:forking_trunk_placer", "minecraft:mega_jungle_trunk_placer",
		"minecraft:cherry_trunk_placer", "minecraft:bending_trunk_placer",
		"minecraft:upwards_branching_trunk_placer":
	default:
		return &unmodelledPart{kind: "trunk_placer", name: t.config.TrunkPlacer.Type}
	}
	switch t.config.FoliagePlacer.Type {
	case "minecraft:blob_foliage_placer", "minecraft:fancy_foliage_placer", "minecraft:pine_foliage_placer",
		"minecraft:spruce_foliage_placer", "minecraft:mega_pine_foliage_placer",
		"minecraft:dark_oak_foliage_placer", "minecraft:acacia_foliage_placer",
		"minecraft:bush_foliage_placer", "minecraft:jungle_foliage_placer",
		"minecraft:cherry_foliage_placer", "minecraft:random_spread_foliage_placer":
	default:
		return &unmodelledPart{kind: "foliage_placer", name: t.config.FoliagePlacer.Type}
	}
	if len(t.config.RootPlacer) > 0 {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(t.config.RootPlacer, &head); err != nil {
			return err
		}
		if head.Type != "minecraft:mangrove_root_placer" {
			return &unmodelledPart{kind: "root_placer", name: head.Type}
		}
	}
	return nil
}

// isFree is TrunkPlacer.isFree: a trunk may also grow through an existing log,
// which validTreePos alone would refuse. Giant trunks test this, straight ones test
// validTreePos, and the difference is visible on the second column of a mega tree
// that overlaps a previous tree's leaves-turned-logs.
func (t *treePlacer) isFree(x, y, z int) bool {
	if t.validTreePos(x, y, z) {
		return true
	}
	name, ok := stateByID(t.stateAt(x, y, z))
	return ok && flattenBlockTagContains(t.set, "minecraft:logs", name.Name)
}

// placeLogIfFreeAt is TrunkPlacer.placeLogIfFree, the gate GiantTrunkPlacer uses.
func (t *treePlacer) placeLogIfFreeAt(x, y, z int) error {
	if !t.isFree(x, y, z) {
		return nil
	}
	return t.placeLogAt(x, y, z)
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
				if err := t.placeLogIfFreeAt(x+c[0], y+i, z+c[1]); err != nil {
					return err
				}
			}
		}
		// new FoliageAttachment(pos.above(height), 0, true): the offset is 0, not 1.
		// A double trunk widens its rows through placeLeavesRow's extra column, and
		// nothing else, so adding a radius here would square the canopy twice.
		attachments = []trunkAttachment{{x: x, y: y + t.trunkHeight, z: z, doubleTrunk: true}}
	case "minecraft:fancy_trunk_placer":
		var err error
		attachments, err = t.placeFancyTrunk(x, y, z)
		if err != nil {
			return err
		}
	case "minecraft:dark_oak_trunk_placer":
		var err error
		attachments, err = t.placeDarkOakTrunk(x, y, z)
		if err != nil {
			return err
		}
	case "minecraft:forking_trunk_placer":
		var err error
		attachments, err = t.placeForkingTrunk(x, y, z)
		if err != nil {
			return err
		}
	case "minecraft:mega_jungle_trunk_placer":
		var err error
		attachments, err = t.placeMegaJungleTrunk(x, y, z)
		if err != nil {
			return err
		}
	case "minecraft:cherry_trunk_placer":
		var err error
		attachments, err = t.placeCherryTrunk(x, y, z)
		if err != nil {
			return err
		}
	case "minecraft:bending_trunk_placer":
		var err error
		attachments, err = t.placeBendingTrunk(x, y, z)
		if err != nil {
			return err
		}
	case "minecraft:upwards_branching_trunk_placer":
		var err error
		attachments, err = t.placeUpwardsBranchingTrunk(x, y, z)
		if err != nil {
			return err
		}
	default:
		return &unmodelledPart{kind: "trunk_placer", name: t.config.TrunkPlacer.Type}
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

// darkOakDirections is Direction.Plane.HORIZONTAL in iteration order - NORTH, EAST,
// SOUTH, WEST - the order Util.getRandom indexes with nextInt(4). Each entry is the
// (stepX, stepZ) the trunk drifts by.
var darkOakDirections = [][2]int{{0, -1}, {1, 0}, {0, 1}, {-1, 0}}

// placeDarkOakTrunk is DarkOakTrunkPlacer.placeTrunk: a 2x2 column that leans by at
// most two cells near the top, a canopy on the drifted crown, and one branch per cell
// of a ring that actually rolled the die.
//
// Three details are what make this match rather than merely look like a fat oak:
//   - the four below-trunk writes happen at the UNDRIFTED corners and before any
//     draw, so the dirt patch under a leaning tree stays where the tree started;
//   - a cell that fails the die spends one nextInt(3) and no length draw, and the
//     ring is walked dx-major over -1..2 in both axes, skipping only the 2x2
//     footprint - so the attachment order, and therefore the canopy order, is fixed;
//   - the branches hang from the ORIGINAL x/z, not the drifted column, while the
//     crown attachment sits at the drifted one and at topY = y + height - 1, i.e. on
//     the topmost trunk cell rather than above it.
func (t *treePlacer) placeDarkOakTrunk(x, y, z int) ([]trunkAttachment, error) {
	corners := [][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}}
	for _, c := range corners {
		if err := t.placeBelowTrunk(x+c[0], y-1, z+c[1]); err != nil {
			return nil, err
		}
	}
	direction := darkOakDirections[int(t.random.NextIntN(4))]
	// The lean starts at this row and costs the budget one cell at a time.
	threshold := t.trunkHeight - int(t.random.NextIntN(4))
	budget := 2 - int(t.random.NextIntN(3))
	topY := y + t.trunkHeight - 1

	curX, curZ := x, z
	for row := 0; row < t.trunkHeight; row++ {
		if row >= threshold && budget > 0 {
			curX += direction[0]
			curZ += direction[1]
			budget--
		}
		if !t.isAirOrLeaves(curX, y+row, curZ) {
			// The whole 2x2 row is skipped, but the drift already happened, so a
			// blocked row still moves the column - the lean survives its gaps.
			continue
		}
		for _, c := range corners {
			if err := t.placeLogAt(curX+c[0], y+row, curZ+c[1]); err != nil {
				return nil, err
			}
		}
	}

	attachments := []trunkAttachment{{x: curX, y: topY, z: curZ, doubleTrunk: true}}
	for dx := -1; dx <= 2; dx++ {
		for dz := -1; dz <= 2; dz++ {
			if dx >= 0 && dx <= 1 && dz >= 0 && dz <= 1 {
				continue
			}
			if t.random.NextIntN(3) > 0 {
				continue
			}
			length := 2 + int(t.random.NextIntN(3))
			for k := 0; k < length; k++ {
				if err := t.placeLogAt(x+dx, topY-k-1, z+dz); err != nil {
					return nil, err
				}
			}
			attachments = append(attachments, trunkAttachment{x: x + dx, y: topY, z: z + dz})
		}
	}
	return attachments, nil
}

// isAirOrLeaves is TreeFeature.isAirOrLeaves - air or the LEAVES tag, and unlike
// validTreePos NOT the replaceable_by_trees tag. DarkOakTrunkPlacer guards a whole
// trunk row with it, testing only the drifted corner and then skipping all four cells.
func (t *treePlacer) isAirOrLeaves(x, y, z int) bool {
	id := t.stateAt(x, y, z)
	if isAirState(id) {
		return true
	}
	name, ok := stateByID(id)
	return ok && flattenBlockTagContains(t.set, "minecraft:leaves", name.Name)
}

// fancyFoliageCoord is FancyTrunkPlacer.FoliageCoords: an attachment plus the y of
// the trunk cell that branch started from. The pair travels together because
// makeBranches draws the branch again after the canopy list has been filtered.
type fancyFoliageCoord struct {
	at         trunkAttachment
	branchBase int
}

// placeFancyTrunk is FancyTrunkPlacer.placeTrunk - the curved oak that a third of
// plains trees are, and the reason this file cannot stay blob-only.
//
// It works in doubles and in that order: two nextFloat draws per candidate branch,
// one per (m) iteration, and the draws are consumed before makeLimb decides whether
// the limb fits. treeShape's 0.3 cut then skips the lowest rows without drawing.
func (t *treePlacer) placeFancyTrunk(x, y, z int) ([]trunkAttachment, error) {
	const (
		trunkHeightScale  = 0.618
		clusterDensity    = 13.0
		branchSlope       = 0.381
		branchLengthMagic = 0.328
	)
	cluster := t.trunkHeight + 2
	top := floorFloat64(float64(cluster) * trunkHeightScale)
	if err := t.placeBelowTrunk(x, y-1, z); err != nil {
		return nil, err
	}
	limbs := min(1, floorFloat64(1.382+math.Pow(float64(cluster)/clusterDensity, 2.0)))
	limit := y + top

	coords := []fancyFoliageCoord{{at: trunkAttachment{x: x, y: y + cluster - 5, z: z}, branchBase: limit}}
	for row := cluster - 5; row >= 0; row-- {
		shape := fancyTreeShape(cluster, row)
		if shape < 0 {
			continue
		}
		for limb := 0; limb < limbs; limb++ {
			radius := float64(shape) * (float64(t.random.NextFloat()) + branchLengthMagic)
			angle := float64(t.random.NextFloat()*2.0) * math.Pi
			bx := x + floorFloat64(radius*math.Sin(angle)+0.5)
			by := y + row - 1
			bz := z + floorFloat64(radius*math.Cos(angle)+0.5)
			ok, err := t.makeLimb(bx, by, bz, bx, by+5, bz, false)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			dx, dz := x-bx, z-bz
			reach := float64(by) - math.Sqrt(float64(dx*dx+dz*dz))*branchSlope
			base := int(reach)
			if reach > float64(limit) {
				base = limit
			}
			ok, err = t.makeLimb(x, base, z, bx, by, bz, false)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			coords = append(coords, fancyFoliageCoord{at: trunkAttachment{x: bx, y: by, z: bz}, branchBase: base})
		}
	}

	// The trunk itself, drawn last of the unbranching limbs and with its result
	// discarded: makeLimb's return value only matters to the branch tests.
	if _, err := t.makeLimb(x, y, z, x, y+top, z, true); err != nil {
		return nil, err
	}
	for _, c := range coords {
		if c.at.x == x && c.at.y == c.branchBase && c.at.z == z {
			continue
		}
		if !t.trimFancyBranches(cluster, c.branchBase-y) {
			continue
		}
		if _, err := t.makeLimb(x, c.branchBase, z, c.at.x, c.at.y, c.at.z, true); err != nil {
			return nil, err
		}
	}
	var out []trunkAttachment
	for _, c := range coords {
		if t.trimFancyBranches(cluster, c.branchBase-y) {
			out = append(out, c.at)
		}
	}
	return out, nil
}

// makeLimb is FancyTrunkPlacer.makeLimb: a line rasterised in unit steps. With
// adjustRadius it paints each step through placeLog and so can never fail; without
// it, it is a pure probe that stops at the first cell the tree cannot occupy.
func (t *treePlacer) makeLimb(fromX, fromY, fromZ, toX, toY, toZ int, adjustRadius bool) (bool, error) {
	if !adjustRadius && fromX == toX && fromY == toY && fromZ == toZ {
		return true, nil
	}
	dx, dy, dz := toX-fromX, toY-fromY, toZ-fromZ
	steps := max(abs(dx), max(abs(dy), abs(dz)))
	// steps can be 0 only when the endpoints coincide, which the guard above has
	// already handled for the probe case. For a painting limb it has not: Java
	// divides by zero there, and Mth.floor turns NaN into 0, so the single step
	// lands on the origin.
	var fx, fy, fz float32
	if steps > 0 {
		fx, fy, fz = float32(dx)/float32(steps), float32(dy)/float32(steps), float32(dz)/float32(steps)
	}
	for i := 0; i <= steps; i++ {
		px := fromX + floorFloat32(0.5+float32(i)*fx)
		py := fromY + floorFloat32(0.5+float32(i)*fy)
		pz := fromZ + floorFloat32(0.5+float32(i)*fz)
		if !adjustRadius {
			if !t.isFree(px, py, pz) {
				return false, nil
			}
			continue
		}
		if err := t.placeLogWithAxis(px, py, pz, fancyLogAxis(fromX, fromZ, px, pz)); err != nil {
			return false, err
		}
	}
	return true, nil
}

// fancyLogAxis is getLogAxis: whichever horizontal axis moved further, or Y when the
// limb is vertical.
func fancyLogAxis(fromX, fromZ, toX, toZ int) string {
	dx, dz := abs(toX-fromX), abs(toZ-fromZ)
	if max(dx, dz) == 0 {
		return "y"
	}
	if dx == max(dx, dz) {
		return "x"
	}
	return "z"
}

// trimFancyBranches is trimBranches: a branch whose base sits below 20% of the
// cluster height is dropped, both from the redraw and from the canopy list.
func (t *treePlacer) trimFancyBranches(cluster, offset int) bool {
	return float64(offset) >= float64(cluster)*0.2
}

// fancyTreeShape is treeShape: the trunk is a circle segment, and rows below the
// 0.3 cut are refused with -1 rather than 0, which is what distinguishes "do not
// even draw" from "draw nothing".
func fancyTreeShape(cluster, row int) float32 {
	if float32(row) < float32(cluster)*0.3 {
		return -1
	}
	f := float32(cluster) / 2.0
	g := f - float32(row)
	h := float32(math.Sqrt(float64(f*f - g*g)))
	switch {
	case g == 0:
		h = f
	case absF32(g) >= f:
		return 0
	}
	return h * 0.5
}

func absF32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// floorFloat64 is Mth.floor(double), same truncating-and-saturating cast as the
// float case.
func floorFloat64(v float64) int {
	switch {
	case math.IsNaN(v):
		return 0
	case math.IsInf(v, 1), v >= math.MaxInt32:
		return math.MaxInt32
	case math.IsInf(v, -1), v <= math.MinInt32:
		return math.MinInt32
	}
	i := int(v)
	if v < 0 {
		i--
	}
	return i
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

// placeFoliage paints one canopy. Each loop is transcribed from its placer's
// createFoliage body, whose arguments arrive as (maxFreeHeight, attachment,
// foliageHeight, foliageRadius, offset) - so the row count comes from
// foliageHeight and the width from foliageRadius.
//
// MegaPineFoliagePlacer is the exception: it iterates absolute y values and calls
// placeLeavesRow with y = 0, which is why it builds a fresh attachment per row
// rather than passing an offset.
func (t *treePlacer) placeFoliage(at trunkAttachment, offset int) error {
	switch t.config.FoliagePlacer.Type {
	case "minecraft:blob_foliage_placer":
		for rowY := offset; rowY >= offset-t.foliageHeight; rowY-- {
			// Math.max(0, radius + radiusOffset - 1 - rowY/2), with rowY/2
			// truncating toward zero exactly as Java's integer division does.
			radius := max(0, t.foliageRadius+at.radiusExtra-1-rowY/2)
			if err := t.placeLeavesRow(at, radius, rowY); err != nil {
				return err
			}
		}
	case "minecraft:fancy_foliage_placer":
		// A sphere: the full radius on every row but the first and last.
		for rowY := offset; rowY >= offset-t.foliageHeight; rowY-- {
			radius := t.foliageRadius
			if rowY != offset && rowY != offset-t.foliageHeight {
				radius++
			}
			if err := t.placeLeavesRow(at, radius, rowY); err != nil {
				return err
			}
		}
	case "minecraft:spruce_foliage_placer":
		// The width breathes: it grows until it reaches a limit that itself grows,
		// then collapses to a recorded value. Which row it collapses on is not the
		// bottom row, so this cannot be written as a cone.
		current, limit, nextLimit := int(t.random.NextIntN(2)), 1, 0
		for rowY := offset; rowY >= -t.foliageHeight; rowY-- {
			if err := t.placeLeavesRow(at, current, rowY); err != nil {
				return err
			}
			if current < limit {
				current++
				continue
			}
			current = nextLimit
			nextLimit = 1
			limit = min(limit+1, t.foliageRadius+at.radiusExtra)
		}
	case "minecraft:mega_pine_foliage_placer":
		if t.foliageHeight == 0 {
			// The crown slope divides by it. No config in the pack can reach this -
			// crown_height is at least 3 - so it is an error rather than a
			// reproduction of Java's saturating division.
			return fmt.Errorf("world: %s at (%d,%d,%d) has foliageHeight 0, which divides the crown slope by zero", t.config.FoliagePlacer.Type, at.x, at.y, at.z)
		}
		lastRadius := 0
		for rowY := at.y - t.foliageHeight + offset; rowY <= at.y+offset; rowY++ {
			distance := at.y - rowY
			radius := t.foliageRadius + at.radiusExtra + floorFloat32(float32(distance)/float32(t.foliageHeight)*3.5)
			if distance > 0 && radius == lastRadius && rowY&1 == 0 {
				radius++
			}
			row := trunkAttachment{x: at.x, y: rowY, z: at.z, radiusExtra: at.radiusExtra, doubleTrunk: at.doubleTrunk}
			if err := t.placeLeavesRow(row, radius, 0); err != nil {
				return err
			}
			lastRadius = radius
		}
	case "minecraft:pine_foliage_placer":
		layerRadius := 0
		for rowY := offset; rowY >= offset-t.foliageHeight; rowY-- {
			if err := t.placeLeavesRow(at, layerRadius, rowY); err != nil {
				return err
			}
			if layerRadius >= 1 && rowY == offset-t.foliageHeight+1 {
				// One row before the bottom the cone closes again.
				layerRadius--
			} else if layerRadius < t.foliageRadius+at.radiusExtra {
				layerRadius++
			}
		}
	case "minecraft:dark_oak_foliage_placer":
		// DarkOakFoliagePlacer.createFoliage reads neither foliageHeight nor the
		// maxFreeHeight argument nor the attachment's radiusOffset: the rows are a
		// fixed shape around the attachment and only the offset provider shifts
		// them. A double trunk paints three rows and then spends ONE nextBoolean
		// that gates a fourth - drawn even when it declines it - and a single trunk
		// paints two, with no draw at all. The order is the stream's order.
		//
		// The offset goes into the attachment's own y, exactly as vanilla folds it
		// into `attachment.pos().above(offset)` before the row loop, because this
		// placer's skip rules read the row offset itself (-1, 0, +1, +2). Folding it
		// into the row argument instead - which is what the other cases here do,
		// harmlessly for them because their rules ignore y - would silently move
		// those rules if a pack ever gave dark oak a non-zero offset. All five of
		// 26.1.2's configs use 0, so this choice is measured as output-neutral here
		// and load-bearing only for a pack that is not this one.
		base := trunkAttachment{x: at.x, y: at.y + offset, z: at.z, radiusExtra: at.radiusExtra, doubleTrunk: at.doubleTrunk}
		if at.doubleTrunk {
			if err := t.placeLeavesRow(base, t.foliageRadius+2, -1); err != nil {
				return err
			}
			if err := t.placeLeavesRow(base, t.foliageRadius+3, 0); err != nil {
				return err
			}
			if err := t.placeLeavesRow(base, t.foliageRadius+2, 1); err != nil {
				return err
			}
			if t.random.NextBoolean() {
				// The cap row keeps the base radius: no +2, no +3.
				if err := t.placeLeavesRow(base, t.foliageRadius, 2); err != nil {
					return err
				}
			}
		} else {
			if err := t.placeLeavesRow(base, t.foliageRadius+2, -1); err != nil {
				return err
			}
			if err := t.placeLeavesRow(base, t.foliageRadius+1, 0); err != nil {
				return err
			}
		}
	case "minecraft:acacia_foliage_placer":
		base := trunkAttachment{x: at.x, y: at.y + offset, z: at.z, radiusExtra: at.radiusExtra, doubleTrunk: at.doubleTrunk}
		if err := t.placeLeavesRow(base, t.foliageRadius+at.radiusExtra, -1-t.foliageHeight); err != nil {
			return err
		}
		if err := t.placeLeavesRow(base, t.foliageRadius-1, -t.foliageHeight); err != nil {
			return err
		}
		if err := t.placeLeavesRow(base, t.foliageRadius+at.radiusExtra-1, 0); err != nil {
			return err
		}
	case "minecraft:bush_foliage_placer":
		for rowY := offset; rowY >= offset-t.foliageHeight; rowY-- {
			radius := t.foliageRadius + at.radiusExtra - 1 - rowY
			if err := t.placeLeavesRow(at, radius, rowY); err != nil {
				return err
			}
		}
	case "minecraft:jungle_foliage_placer":
		leafHeight := 1 + int(t.random.NextIntN(2))
		if at.doubleTrunk {
			leafHeight = t.foliageHeight
		}
		for rowY := offset; rowY >= offset-leafHeight; rowY-- {
			radius := t.foliageRadius + at.radiusExtra + 1 - rowY
			if err := t.placeLeavesRow(at, radius, rowY); err != nil {
				return err
			}
		}
	case "minecraft:cherry_foliage_placer":
		if _, ok := t.config.FoliagePlacer.Float("corner_hole_chance"); !ok {
			return fmt.Errorf("world: cherry_foliage_placer has no corner_hole_chance")
		}
		if _, ok := t.config.FoliagePlacer.Float("wide_bottom_layer_hole_chance"); !ok {
			return fmt.Errorf("world: cherry_foliage_placer has no wide_bottom_layer_hole_chance")
		}
		hangingChance, ok := t.config.FoliagePlacer.Float("hanging_leaves_chance")
		if !ok {
			return fmt.Errorf("world: cherry_foliage_placer has no hanging_leaves_chance")
		}
		hangingExtChance, ok := t.config.FoliagePlacer.Float("hanging_leaves_extension_chance")
		if !ok {
			return fmt.Errorf("world: cherry_foliage_placer has no hanging_leaves_extension_chance")
		}
		base := trunkAttachment{x: at.x, y: at.y + offset, z: at.z, radiusExtra: at.radiusExtra, doubleTrunk: at.doubleTrunk}
		currentRadius := t.foliageRadius + at.radiusExtra - 1
		if err := t.placeLeavesRow(base, currentRadius-2, t.foliageHeight-3); err != nil {
			return err
		}
		if err := t.placeLeavesRow(base, currentRadius-1, t.foliageHeight-4); err != nil {
			return err
		}
		for rowY := t.foliageHeight - 5; rowY >= 0; rowY-- {
			if err := t.placeLeavesRow(base, currentRadius, rowY); err != nil {
				return err
			}
		}
		if err := t.placeLeavesRowWithHangingLeavesBelow(base, currentRadius, -1, float32(hangingChance), float32(hangingExtChance)); err != nil {
			return err
		}
		if err := t.placeLeavesRowWithHangingLeavesBelow(base, currentRadius-1, -2, float32(hangingChance), float32(hangingExtChance)); err != nil {
			return err
		}
	case "minecraft:random_spread_foliage_placer":
		attempts, ok := t.config.FoliagePlacer.Scalar("leaf_placement_attempts")
		if !ok {
			return fmt.Errorf("world: random_spread_foliage_placer has no leaf_placement_attempts")
		}
		for i := 0; i < attempts; i++ {
			dx := int(t.random.NextIntN(int32(t.foliageRadius))) - int(t.random.NextIntN(int32(t.foliageRadius)))
			dy := int(t.random.NextIntN(int32(t.foliageHeight))) - int(t.random.NextIntN(int32(t.foliageHeight)))
			dz := int(t.random.NextIntN(int32(t.foliageRadius))) - int(t.random.NextIntN(int32(t.foliageRadius)))
			if _, err := t.placeLeaf(at.x+dx, at.y+dy, at.z+dz, cellIsPlaceable); err != nil {
				return err
			}
		}
	default:
		return &unmodelledPart{kind: "foliage_placer", name: t.config.FoliagePlacer.Type}
	}
	return nil
}

// placeForkingTrunk is ForkingTrunkPlacer.placeTrunk.
func (t *treePlacer) placeForkingTrunk(x, y, z int) ([]trunkAttachment, error) {
	if err := t.placeBelowTrunk(x, y-1, z); err != nil {
		return nil, err
	}
	leanDirIdx := int(t.random.NextIntN(4))
	leanDir := darkOakDirections[leanDirIdx]
	leanHeight := t.trunkHeight - int(t.random.NextIntN(4)) - 1
	leanSteps := 3 - int(t.random.NextIntN(3))

	var attachments []trunkAttachment
	tx, tz := x, z
	eySet := false
	ey := 0
	for yo := 0; yo < t.trunkHeight; yo++ {
		yy := y + yo
		if yo >= leanHeight && leanSteps > 0 {
			tx += leanDir[0]
			tz += leanDir[1]
			leanSteps--
		}
		placed, err := t.placeLogBoolAt(tx, yy, tz)
		if err != nil {
			return nil, err
		}
		if placed {
			ey = yy + 1
			eySet = true
		}
	}
	if eySet {
		attachments = append(attachments, trunkAttachment{x: tx, y: ey, z: tz, radiusExtra: 1})
	}

	tx, tz = x, z
	branchDirIdx := int(t.random.NextIntN(4))
	if branchDirIdx != leanDirIdx {
		branchDir := darkOakDirections[branchDirIdx]
		branchPos := leanHeight - int(t.random.NextIntN(2)) - 1
		branchSteps := 1 + int(t.random.NextIntN(3))
		eySet = false
		for yo := branchPos; yo < t.trunkHeight && branchSteps > 0; yo, branchSteps = yo+1, branchSteps-1 {
			if yo >= 1 {
				yy := y + yo
				tx += branchDir[0]
				tz += branchDir[1]
				placed, err := t.placeLogBoolAt(tx, yy, tz)
				if err != nil {
					return nil, err
				}
				if placed {
					ey = yy + 1
					eySet = true
				}
			}
		}
		if eySet {
			attachments = append(attachments, trunkAttachment{x: tx, y: ey, z: tz})
		}
	}
	return attachments, nil
}

// placeMegaJungleTrunk is MegaJungleTrunkPlacer.placeTrunk.
func (t *treePlacer) placeMegaJungleTrunk(x, y, z int) ([]trunkAttachment, error) {
	corners := [][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}}
	for _, c := range corners {
		if err := t.placeBelowTrunk(x+c[0], y-1, z+c[1]); err != nil {
			return nil, err
		}
	}
	for i := 0; i < t.trunkHeight; i++ {
		for _, c := range corners {
			if err := t.placeLogIfFreeAt(x+c[0], y+i, z+c[1]); err != nil {
				return nil, err
			}
		}
	}
	attachments := []trunkAttachment{{x: x, y: y + t.trunkHeight, z: z, doubleTrunk: true}}
	for branchHeight := t.trunkHeight - 2 - int(t.random.NextIntN(4)); branchHeight > t.trunkHeight/2; branchHeight -= 2 + int(t.random.NextIntN(4)) {
		angle := t.random.NextFloat() * (float32(math.Pi) * 2.0)
		bx, bz := 0, 0
		for b := 0; b < 5; b++ {
			bx = int(1.5 + worldgen.MthCos(float64(angle))*float32(b))
			bz = int(1.5 + worldgen.MthSin(float64(angle))*float32(b))
			if err := t.placeLogAt(x+bx, y+branchHeight-3+b/2, z+bz); err != nil {
				return nil, err
			}
		}
		attachments = append(attachments, trunkAttachment{x: x + bx, y: y + branchHeight, z: z + bz, radiusExtra: -2})
	}
	return attachments, nil
}

// placeCherryTrunk is CherryTrunkPlacer.placeTrunk.
func (t *treePlacer) placeCherryTrunk(x, y, z int) ([]trunkAttachment, error) {
	branchCountProv, ok, err := t.config.TrunkPlacer.Provider("branch_count")
	if err != nil || !ok {
		return nil, fmt.Errorf("world: cherry_trunk_placer missing branch_count")
	}
	branchHorizProv, ok, err := t.config.TrunkPlacer.Provider("branch_horizontal_length")
	if err != nil || !ok {
		return nil, fmt.Errorf("world: cherry_trunk_placer missing branch_horizontal_length")
	}
	branchStartProv, ok, err := t.config.TrunkPlacer.Provider("branch_start_offset_from_top")
	if err != nil || !ok {
		return nil, fmt.Errorf("world: cherry_trunk_placer missing branch_start_offset_from_top")
	}
	branchEndProv, ok, err := t.config.TrunkPlacer.Provider("branch_end_offset_from_top")
	if err != nil || !ok {
		return nil, fmt.Errorf("world: cherry_trunk_placer missing branch_end_offset_from_top")
	}

	if err := t.placeBelowTrunk(x, y-1, z); err != nil {
		return nil, err
	}
	firstBranchOffset := max(0, t.trunkHeight-1+branchStartProv.Sample(t.random))
	secondBranchStartProv := worldgen.NestedIntProvider{
		Type: "uniform",
		Min:  branchStartProv.Min,
		Max:  branchStartProv.Max - 1,
	}
	secondBranchOffset := max(0, t.trunkHeight-1+secondBranchStartProv.Sample(t.random))
	if secondBranchOffset >= firstBranchOffset {
		secondBranchOffset++
	}
	branchCount := branchCountProv.Sample(t.random)
	hasMiddle := branchCount == 3
	hasBoth := branchCount >= 2

	var actualTrunkHeight int
	if hasMiddle {
		actualTrunkHeight = t.trunkHeight
	} else if hasBoth {
		actualTrunkHeight = max(firstBranchOffset, secondBranchOffset) + 1
	} else {
		actualTrunkHeight = firstBranchOffset + 1
	}
	for i := 0; i < actualTrunkHeight; i++ {
		if err := t.placeLogAt(x, y+i, z); err != nil {
			return nil, err
		}
	}
	var attachments []trunkAttachment
	if hasMiddle {
		attachments = append(attachments, trunkAttachment{x: x, y: y + actualTrunkHeight, z: z})
	}
	treeDirIdx := int(t.random.NextIntN(4))
	treeDir := darkOakDirections[treeDirIdx]
	sidewaysAxis := "z"
	if treeDir[0] != 0 {
		sidewaysAxis = "x"
	}
	firstAt, err := t.generateCherryBranch(x, y, z, sidewaysAxis, treeDir, firstBranchOffset, firstBranchOffset < actualTrunkHeight-1, branchHorizProv, branchEndProv)
	if err != nil {
		return nil, err
	}
	attachments = append(attachments, firstAt)
	if hasBoth {
		oppDir := [2]int{-treeDir[0], -treeDir[1]}
		secondAt, err := t.generateCherryBranch(x, y, z, sidewaysAxis, oppDir, secondBranchOffset, secondBranchOffset < actualTrunkHeight-1, branchHorizProv, branchEndProv)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, secondAt)
	}
	return attachments, nil
}

func (t *treePlacer) generateCherryBranch(
	originX, originY, originZ int,
	sidewaysAxis string,
	branchDir [2]int,
	offsetFromOrigin int,
	middleContinuesUpwards bool,
	branchHorizProv, branchEndProv worldgen.NestedIntProvider,
) (trunkAttachment, error) {
	logX, logY, logZ := originX, originY+offsetFromOrigin, originZ
	branchEndPosOffsetFromOrigin := t.trunkHeight - 1 + branchEndProv.Sample(t.random)
	extendBranchAwayFromTrunk := middleContinuesUpwards || branchEndPosOffsetFromOrigin < offsetFromOrigin
	distanceToTrunk := branchHorizProv.Sample(t.random)
	if extendBranchAwayFromTrunk {
		distanceToTrunk++
	}
	endX := originX + branchDir[0]*distanceToTrunk
	endY := originY + branchEndPosOffsetFromOrigin
	endZ := originZ + branchDir[1]*distanceToTrunk

	stepsHorizontally := 1
	if extendBranchAwayFromTrunk {
		stepsHorizontally = 2
	}
	for i := 0; i < stepsHorizontally; i++ {
		logX += branchDir[0]
		logZ += branchDir[1]
		if err := t.placeLogWithAxis(logX, logY, logZ, sidewaysAxis); err != nil {
			return trunkAttachment{}, err
		}
	}
	vertStep := -1
	if endY > logY {
		vertStep = 1
	}
	for {
		distance := abs(endX-logX) + abs(endY-logY) + abs(endZ-logZ)
		if distance == 0 {
			break
		}
		chanceToGrowVertically := float32(abs(endY-logY)) / float32(distance)
		growVertically := t.random.NextFloat() < chanceToGrowVertically
		if growVertically {
			logY += vertStep
			if err := t.placeLogAt(logX, logY, logZ); err != nil {
				return trunkAttachment{}, err
			}
		} else {
			logX += branchDir[0]
			logZ += branchDir[1]
			if err := t.placeLogWithAxis(logX, logY, logZ, sidewaysAxis); err != nil {
				return trunkAttachment{}, err
			}
		}
	}
	return trunkAttachment{x: endX, y: endY + 1, z: endZ}, nil
}

// placeBendingTrunk is BendingTrunkPlacer.placeTrunk.
func (t *treePlacer) placeBendingTrunk(x, y, z int) ([]trunkAttachment, error) {
	minHeightForLeaves, ok := t.config.TrunkPlacer.Scalar("min_height_for_leaves")
	if !ok {
		return nil, fmt.Errorf("world: bending_trunk_placer missing min_height_for_leaves")
	}
	bendLengthProv, ok, err := t.config.TrunkPlacer.Provider("bend_length")
	if err != nil || !ok {
		return nil, fmt.Errorf("world: bending_trunk_placer missing bend_length")
	}
	dir := darkOakDirections[int(t.random.NextIntN(4))]
	logHeight := t.trunkHeight - 1
	px, py, pz := x, y, z
	if err := t.placeBelowTrunk(px, py-1, pz); err != nil {
		return nil, err
	}
	var attachments []trunkAttachment
	for i := 0; i <= logHeight; i++ {
		if i+1 >= logHeight+int(t.random.NextIntN(2)) {
			px += dir[0]
			pz += dir[1]
		}
		if t.validTreePos(px, py, pz) {
			if err := t.placeLogAt(px, py, pz); err != nil {
				return nil, err
			}
		}
		if i >= minHeightForLeaves {
			attachments = append(attachments, trunkAttachment{x: px, y: py, z: pz})
		}
		py++
	}
	dirLength := bendLengthProv.Sample(t.random)
	for i := 0; i <= dirLength; i++ {
		if t.validTreePos(px, py, pz) {
			if err := t.placeLogAt(px, py, pz); err != nil {
				return nil, err
			}
		}
		attachments = append(attachments, trunkAttachment{x: px, y: py, z: pz})
		px += dir[0]
		pz += dir[1]
	}
	return attachments, nil
}

// placeUpwardsBranchingTrunk is UpwardsBranchingTrunkPlacer.placeTrunk.
func (t *treePlacer) placeUpwardsBranchingTrunk(x, y, z int) ([]trunkAttachment, error) {
	extraBranchSteps, ok, err := t.config.TrunkPlacer.Provider("extra_branch_steps")
	if err != nil || !ok {
		return nil, fmt.Errorf("world: upwards_branching_trunk_placer missing extra_branch_steps")
	}
	extraBranchLength, ok, err := t.config.TrunkPlacer.Provider("extra_branch_length")
	if err != nil || !ok {
		return nil, fmt.Errorf("world: upwards_branching_trunk_placer missing extra_branch_length")
	}
	branchProb, ok := t.config.TrunkPlacer.Float("place_branch_per_log_probability")
	if !ok {
		return nil, fmt.Errorf("world: upwards_branching_trunk_placer missing place_branch_per_log_probability")
	}
	rawGrowThrough, ok := t.config.TrunkPlacer.Fields["can_grow_through"]
	if !ok {
		return nil, fmt.Errorf("world: upwards_branching_trunk_placer missing can_grow_through")
	}
	growThroughList, err := parseBlockListOrTag(rawGrowThrough)
	if err != nil {
		return nil, err
	}

	var attachments []trunkAttachment
	for heightPos := 0; heightPos < t.trunkHeight; heightPos++ {
		cy := y + heightPos
		placed, err := t.placeLogUpwardsBranching(x, cy, z, growThroughList)
		if err != nil {
			return nil, err
		}
		if placed && heightPos < t.trunkHeight-1 && t.random.NextFloat() < float32(branchProb) {
			branchDir := darkOakDirections[int(t.random.NextIntN(4))]
			branchLen := extraBranchLength.Sample(t.random)
			branchPos := max(0, branchLen-extraBranchLength.Sample(t.random)-1)
			branchSteps := extraBranchSteps.Sample(t.random)
			if err := t.placeUpwardsBranch(x, cy, z, branchDir, branchPos, branchSteps, growThroughList, &attachments); err != nil {
				return nil, err
			}
		}
		if heightPos == t.trunkHeight-1 {
			attachments = append(attachments, trunkAttachment{x: x, y: cy + 1, z: z})
		}
	}
	return attachments, nil
}

func (t *treePlacer) placeUpwardsBranch(
	x, cy, z int,
	dir [2]int,
	branchPos, branchSteps int,
	growThroughList []string,
	attachments *[]trunkAttachment,
) error {
	yPos := cy
	eySet := false
	ey := 0
	for j := branchPos; j < t.trunkHeight && branchSteps > 0 && j >= 1; j, branchSteps = j+1, branchSteps-1 {
		yPos++
		bx := x + dir[0]*j
		bz := z + dir[1]*j
		placed, err := t.placeLogUpwardsBranching(bx, yPos, bz, growThroughList)
		if err != nil {
			return err
		}
		if placed {
			ey = yPos + 1
			eySet = true
		}
	}
	if eySet {
		*attachments = append(*attachments, trunkAttachment{
			x: x + dir[0]*branchPos,
			y: ey,
			z: z + dir[1]*branchPos,
		})
	}
	return nil
}

func (t *treePlacer) placeLogUpwardsBranching(x, y, z int, growThroughList []string) (bool, error) {
	if !t.validTreePosOrGrowThrough(x, y, z, growThroughList) {
		return false, nil
	}
	spec, err := t.set.StateProvider(t.config.TrunkProvider.Raw)
	if err != nil {
		return false, err
	}
	state, err := t.r.sampleStateProvider(t.set, spec, t.random, worldgen.FeaturePosition{X: x, Y: y, Z: z})
	if err != nil {
		return false, err
	}
	t.r.setBlock(x, y, z, state)
	t.placements = append(t.placements, treePlacement{x: x, y: y, z: z, isLog: true})
	return true, nil
}

func (t *treePlacer) validTreePosOrGrowThrough(x, y, z int, growThroughList []string) bool {
	if t.validTreePos(x, y, z) {
		return true
	}
	return t.matchesBlockListOrTag(x, y, z, growThroughList)
}

func (t *treePlacer) matchesBlockListOrTag(x, y, z int, entries []string) bool {
	name, ok := stateByID(t.stateAt(x, y, z))
	if !ok {
		return false
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry, "#") {
			if flattenBlockTagContains(t.set, strings.TrimPrefix(entry, "#"), name.Name) {
				return true
			}
		} else if name.Name == entry {
			return true
		}
	}
	return false
}

func parseBlockListOrTag(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if raw[0] == '"' {
		var single string
		if err := json.Unmarshal(raw, &single); err != nil {
			return nil, err
		}
		return []string{single}, nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	return list, nil
}

type mangroveRootPlacerConfig struct {
	Type               string          `json:"type"`
	TrunkOffsetY       json.RawMessage `json:"trunk_offset_y"`
	RootProvider       json.RawMessage `json:"root_provider"`
	AboveRootPlacement *struct {
		AboveRootProvider        json.RawMessage `json:"above_root_provider"`
		AboveRootPlacementChance float32         `json:"above_root_placement_chance"`
	} `json:"above_root_placement"`
	MangroveRootPlacement struct {
		MaxRootWidth       int             `json:"max_root_width"`
		MaxRootLength      int             `json:"max_root_length"`
		MuddyRootsIn       json.RawMessage `json:"muddy_roots_in"`
		MuddyRootsProvider json.RawMessage `json:"muddy_roots_provider"`
		CanGrowThrough     json.RawMessage `json:"can_grow_through"`
		RandomSkewChance   float32         `json:"random_skew_chance"`
	} `json:"mangrove_root_placement"`
}

func (t *treePlacer) sampleTrunkOriginY(originY int) (int, error) {
	var cfg mangroveRootPlacerConfig
	if err := json.Unmarshal(t.config.RootPlacer, &cfg); err != nil {
		return 0, err
	}
	var dummy worldgen.TreeTrunkPlacer
	dummy.Fields = map[string]json.RawMessage{"trunk_offset_y": cfg.TrunkOffsetY}
	prov, ok, err := dummy.Provider("trunk_offset_y")
	if err != nil || !ok {
		return 0, fmt.Errorf("world: mangrove_root_placer missing trunk_offset_y")
	}
	return originY + prov.Sample(t.random), nil
}

func (t *treePlacer) placeMangroveRoots(origin worldgen.FeaturePosition, trunkOriginY int) (bool, error) {
	var cfg mangroveRootPlacerConfig
	if err := json.Unmarshal(t.config.RootPlacer, &cfg); err != nil {
		return false, err
	}
	canGrowThrough, err := parseBlockListOrTag(cfg.MangroveRootPlacement.CanGrowThrough)
	if err != nil {
		return false, err
	}
	muddyRootsIn, err := parseBlockListOrTag(cfg.MangroveRootPlacement.MuddyRootsIn)
	if err != nil {
		return false, err
	}
	rootSpec, err := t.set.StateProvider(cfg.RootProvider)
	if err != nil {
		return false, err
	}
	muddySpec, err := t.set.StateProvider(cfg.MangroveRootPlacement.MuddyRootsProvider)
	if err != nil {
		return false, err
	}
	var aboveSpec worldgen.StateProviderSpec
	hasAbove := cfg.AboveRootPlacement != nil
	if hasAbove {
		aboveSpec, err = t.set.StateProvider(cfg.AboveRootPlacement.AboveRootProvider)
		if err != nil {
			return false, err
		}
	}

	canPlaceRoot := func(x, y, z int) bool {
		return t.validTreePosOrGrowThrough(x, y, z, canGrowThrough)
	}

	for cy := origin.Y; cy < trunkOriginY; cy++ {
		if !canPlaceRoot(origin.X, cy, origin.Z) {
			return false, nil
		}
	}

	rootPositions := [][3]int{{origin.X, trunkOriginY - 1, origin.Z}}
	trunkOrigin := [3]int{origin.X, trunkOriginY, origin.Z}

	var simulateRoots func(pos [3]int, dir [2]int, positions *[][3]int, layer int) bool
	simulateRoots = func(pos [3]int, dir [2]int, positions *[][3]int, layer int) bool {
		maxLen := cfg.MangroveRootPlacement.MaxRootLength
		if layer == maxLen || len(*positions) > maxLen {
			return false
		}
		potentials := t.potentialMangroveRootPositions(pos, dir, trunkOrigin, cfg.MangroveRootPlacement.MaxRootWidth, cfg.MangroveRootPlacement.RandomSkewChance)
		for _, p := range potentials {
			if canPlaceRoot(p[0], p[1], p[2]) {
				*positions = append(*positions, p)
				if !simulateRoots(p, dir, positions, layer+1) {
					return false
				}
			}
		}
		return true
	}

	for _, dir := range darkOakDirections {
		startPos := [3]int{trunkOrigin[0] + dir[0], trunkOrigin[1], trunkOrigin[2] + dir[1]}
		var positionsInDir [][3]int
		if !simulateRoots(startPos, dir, &positionsInDir, 0) {
			return false, nil
		}
		rootPositions = append(rootPositions, positionsInDir...)
		rootPositions = append(rootPositions, startPos)
	}

	for _, pos := range rootPositions {
		px, py, pz := pos[0], pos[1], pos[2]
		if t.matchesBlockListOrTag(px, py, pz, muddyRootsIn) {
			state, err := t.r.sampleStateProvider(t.set, muddySpec, t.random, worldgen.FeaturePosition{X: px, Y: py, Z: pz})
			if err != nil {
				return false, err
			}
			state = t.waterlogRootState(px, py, pz, state)
			t.r.setBlock(px, py, pz, state)
			t.placements = append(t.placements, treePlacement{x: px, y: py, z: pz, isRoot: true})
			continue
		}
		if !canPlaceRoot(px, py, pz) {
			continue
		}
		state, err := t.r.sampleStateProvider(t.set, rootSpec, t.random, worldgen.FeaturePosition{X: px, Y: py, Z: pz})
		if err != nil {
			return false, err
		}
		state = t.waterlogRootState(px, py, pz, state)
		t.r.setBlock(px, py, pz, state)
		t.placements = append(t.placements, treePlacement{x: px, y: py, z: pz, isRoot: true})
		if hasAbove {
			ax, ay, az := px, py+1, pz
			if t.random.NextFloat() < cfg.AboveRootPlacement.AboveRootPlacementChance && isAirState(t.stateAt(ax, ay, az)) {
				aboveState, err := t.r.sampleStateProvider(t.set, aboveSpec, t.random, worldgen.FeaturePosition{X: ax, Y: ay, Z: az})
				if err != nil {
					return false, err
				}
				aboveState = t.waterlogRootState(ax, ay, az, aboveState)
				t.r.setBlock(ax, ay, az, aboveState)
				t.placements = append(t.placements, treePlacement{x: ax, y: ay, z: az, isRoot: true})
			}
		}
	}
	return true, nil
}

func (t *treePlacer) potentialMangroveRootPositions(pos [3]int, prevDir [2]int, rootOrigin [3]int, maxRootWidth int, randomSkewChance float32) [][3]int {
	below := [3]int{pos[0], pos[1] - 1, pos[2]}
	nextTo := [3]int{pos[0] + prevDir[0], pos[1], pos[2] + prevDir[1]}
	width := abs(pos[0]-rootOrigin[0]) + abs(pos[1]-rootOrigin[1]) + abs(pos[2]-rootOrigin[2])
	if width > maxRootWidth-3 && width <= maxRootWidth {
		if t.random.NextFloat() < randomSkewChance {
			return [][3]int{below, {nextTo[0], nextTo[1] - 1, nextTo[2]}}
		}
		return [][3]int{below}
	}
	if width > maxRootWidth {
		return [][3]int{below}
	}
	if t.random.NextFloat() < randomSkewChance {
		return [][3]int{below}
	}
	if t.random.NextBoolean() {
		return [][3]int{nextTo}
	}
	return [][3]int{below}
}

func (t *treePlacer) waterlogRootState(x, y, z int, state uint16) uint16 {
	name, ok := stateByID(state)
	if !ok {
		return state
	}
	if _, has := name.Properties["waterlogged"]; !has {
		return state
	}
	wet := isWaterFluidState(t.stateAt(x, y, z))
	merged := make(map[string]string, len(name.Properties))
	for k, v := range name.Properties {
		merged[k] = v
	}
	if wet {
		merged["waterlogged"] = "true"
	} else {
		merged["waterlogged"] = "false"
	}
	if resolved, ok := nameToStateID(name.Name, merged); ok {
		return resolved
	}
	return state
}

func isWaterFluidState(id uint16) bool {
	name, ok := stateByID(id)
	if !ok {
		return false
	}
	if name.Name == "minecraft:water" || name.Name == "minecraft:kelp" || name.Name == "minecraft:kelp_plant" ||
		name.Name == "minecraft:seagrass" || name.Name == "minecraft:tall_seagrass" || name.Name == "minecraft:bubble_column" {
		return true
	}
	return name.Properties["waterlogged"] == "true"
}

// floorFloat32 is Mth.floor(float): Java's narrowing cast truncates toward zero and
// saturates - NaN to 0, +Infinity to Integer.MAX_VALUE - and the -1 for negatives
// turns that into a true floor.
func floorFloat32(v float32) int {
	f := float64(v)
	switch {
	case math.IsNaN(f):
		// Java's narrowing cast gives 0 for NaN.
		return 0
	case math.IsInf(f, 1):
		return math.MaxInt32
	case math.IsInf(f, -1):
		return math.MinInt32
	case f >= math.MaxInt32:
		return math.MaxInt32
	case f <= math.MinInt32:
		return math.MinInt32
	}
	i := int(v)
	if v < 0 {
		i--
	}
	return i
}

func isWaterSourceState(id uint16) bool {
	name, ok := stateByID(id)
	if !ok {
		return false
	}
	return name.Name == "minecraft:water" && name.Properties["level"] == "0"
}
