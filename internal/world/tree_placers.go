package world

import (
	"errors"
	"fmt"
	"math"

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
	placements []treePlacement

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
	case "minecraft:blob_foliage_placer", "minecraft:fancy_foliage_placer":
		// BlobFoliagePlacer.foliageHeight returns its int field without drawing, and
		// FancyFoliagePlacer inherits it.
		h, ok := t.config.FoliagePlacer.Scalar("height")
		if !ok {
			return fmt.Errorf("world: %s has no scalar height", t.config.FoliagePlacer.Type)
		}
		t.foliageHeight = h
	case "minecraft:pine_foliage_placer":
		// PineFoliagePlacer.foliageHeight samples its provider: one draw.
		h, err := t.foliageProviderSample("height")
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
	default:
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

// shouldSkipLocationSigned measures distance to the nearer of two trunk columns
// when a giant tree's canopy covers both.
func (t *treePlacer) shouldSkipLocationSigned(dx, y, dz, radius int, doubleTrunk bool) bool {
	ax, az := abs(dx), abs(dz)
	if doubleTrunk {
		ax = min(ax, abs(dx-1))
		az = min(az, abs(dz-1))
	}
	return t.shouldSkipLocation(ax, y, az, radius)
}

// shouldSkipLocation is per placer type, and each implemented case is read from
// bytecode. The y argument is the row offset, which blob uses and the cone
// placers do not.
func (t *treePlacer) shouldSkipLocation(ax, y, az, radius int) bool {
	switch t.config.FoliagePlacer.Type {
	case "minecraft:blob_foliage_placer":
		// Only exact corners are candidates, and only then does it draw. The corner
		// is dropped when the draw says so, but kept on every non-bottom row:
		// nextInt(2) == 0 || y == 0.
		if ax != radius || az != radius {
			return false
		}
		return t.random.NextIntN(2) == 0 || y == 0
	case "minecraft:fancy_foliage_placer":
		// A round canopy: skip outside the circle of the row radius, measured from
		// the cell centre rather than the corner.
		return square(float32(ax)+0.5)+square(float32(az)+0.5) > float32(radius*radius)
	case "minecraft:pine_foliage_placer", "minecraft:spruce_foliage_placer":
		// Pure geometry, no draw at all, and identical in both classes.
		return ax == radius && az == radius && radius > 0
	case "minecraft:mega_pine_foliage_placer":
		// Two tests: the far corner of the double-trunk square, then the circle.
		return ax+az >= 7 || ax*ax+az*az > radius*radius
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
	return t.placeLogWithAxis(x, y, z, "")
}

// placeLogWithAxis is placeLog with the Function<BlockState,BlockState> argument,
// which FancyTrunkPlacer uses to re-orient each log along its limb. A block without
// an axis property is left alone, because the bytecode calls trySetValue.
func (t *treePlacer) placeLogWithAxis(x, y, z int, axis string) error {
	if !t.validTreePos(x, y, z) {
		return nil
	}
	spec, err := t.set.StateProvider(t.config.TrunkProvider.Raw)
	if err != nil {
		return err
	}
	state, err := t.r.sampleStateProvider(t.set, spec, t.random, worldgen.FeaturePosition{X: x, Y: y, Z: z})
	if err != nil {
		return err
	}
	if axis != "" {
		name, ok := stateByID(state)
		if !ok {
			return fmt.Errorf("world: trunk provider returned unknown state %d", state)
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
	default:
		return &unmodelledPart{kind: "foliage_placer", name: t.config.FoliagePlacer.Type}
	}
	return nil
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
