package world

import (
	"fmt"
	"math"
	"sort"

	"regionio/internal/worldgen"
)

type netherStructurePlan struct {
	bastions []*JigsawStart
	forts    []*NetherFortressStart
	fossils  []*NetherFossilStub
	portals  []netherPortalEntry
}

type netherPortalEntry struct {
	stub   *RuinedPortalStub
	center [2]int32
}

func collectNetherStructurePlan(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, targetX, targetZ int32) netherStructurePlan {
	var plan netherStructurePlan
	if od == nil || sets == nil {
		return plan
	}
	const complexRadius = 8
	for sx := targetX - complexRadius; sx <= targetX+complexRadius; sx++ {
		for sz := targetZ - complexRadius; sz <= targetZ+complexRadius; sz++ {
			stub, ok := FindStructureSetStart(od, sets, "minecraft:nether_complexes", seed, sx, sz)
			if !ok || stub == nil {
				continue
			}
			switch stub.StructureName {
			case "minecraft:fortress":
				if fort := BuildNetherFortressStart(seed, sx, sz); fort != nil {
					plan.forts = append(plan.forts, fort)
				}
			case "minecraft:bastion_remnant":
				jStart, cached := getCachedJigsawStart(seed, "minecraft:nether_complexes", sx, sz)
				if !cached {
					if def := sets.Structures["minecraft:bastion_remnant"]; def != nil {
						var err error
						jStart, err = BuildJigsawStart(od, sets, def, seed, sx, sz, nil)
						if err != nil {
							jStart = nil
						}
					}
					putCachedJigsawStart(seed, "minecraft:nether_complexes", sx, sz, jStart)
				}
				if jStart != nil {
					plan.bastions = append(plan.bastions, jStart)
				}
			}
		}
	}
	for sx := targetX - 5; sx <= targetX+5; sx++ {
		for sz := targetZ - 5; sz <= targetZ+5; sz++ {
			if stub := NetherFossilGenerationPoint(od, sets, seed, sx, sz); stub != nil {
				plan.fossils = append(plan.fossils, stub)
			}
		}
	}
	for sx := targetX - 5; sx <= targetX+5; sx++ {
		for sz := targetZ - 5; sz <= targetZ+5; sz++ {
			stub, err := RuinedPortalGenerationPoint(od, sets, seed, sx, sz)
			if err != nil || stub == nil {
				continue
			}
			if cx, cz, ok := ruinedPortalCenterChunk(stub); ok {
				plan.portals = append(plan.portals, netherPortalEntry{
					stub:   stub,
					center: [2]int32{cx, cz},
				})
			}
		}
	}
	return plan
}

func ruinedPortalCenterChunk(stub *RuinedPortalStub) (int32, int32, bool) {
	if stub == nil {
		return 0, 0, false
	}
	_, size, err := loadTemplateCached(stub.Template)
	if err != nil {
		return 0, 0, false
	}
	pivot := [3]int{size[0] / 2, 0, size[2] / 2}
	mirror := stub.Mirror
	if mirror == "" {
		mirror = "none"
	}
	minX, _, minZ, maxX, _, maxZ := boundingBoxOf(size, mirror, stub.Rotation, pivot, stub.X, stub.Y, stub.Z)
	centerX := minX + (maxX-minX+1)/2
	centerZ := minZ + (maxZ-minZ+1)/2
	return int32(centerX >> 4), int32(centerZ >> 4), true
}

func (p *netherStructurePlan) placeSurfaceStructuresInChunk(r *decorationRegion, seed int64, cx, cz int32) {
	if p == nil || r == nil {
		return
	}
	if len(p.bastions) > 0 {
		chunkBox := msChunkBox(cx, cz)
		var hitting []*JigsawStart
		for _, b := range p.bastions {
			if b.Box.intersects(chunkBox) {
				hitting = append(hitting, b)
			}
		}
		if len(hitting) > 0 {
			PlaceJigsawStartsForChunk(r, hitting, "minecraft:bastion_remnant", surfaceStructuresStep, seed, cx, cz)
		}
	}
	key := [2]int32{cx, cz}
	for _, entry := range p.portals {
		if entry.center == key {
			_ = PlaceRuinedPortalPiece(r, entry.stub, seed, cx, cz)
		}
	}
}

func (p *netherStructurePlan) placeUndergroundStructuresInChunk(r *decorationRegion, seed int64, cx, cz int32) {
	if p == nil || r == nil {
		return
	}
	c := r.chunks[[2]int32{cx, cz}]
	if c == nil {
		return
	}
	for _, fort := range p.forts {
		PlaceNetherFortress(c, fort, cx, cz)
	}
	for _, stub := range p.fossils {
		PlaceNetherFossilInRegion(r, stub, seed, cx, cz)
	}
}

// replayNetherDecoration replays Vanilla 26.1.2 Nether decoration across the
// target chunk's source neighborhood using FeatureSorter stage indices and exact
// per-feature placement chains, followed by post-processing and fast_lava springs.
func (r *decorationRegion) replayNetherDecoration(od *worldgen.OverworldDensity, seed int64, targetX, targetZ int32, sources []decorationSource) error {
	r.bindSeed(seed)
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		return err
	}
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		return err
	}

	var netherSources []decorationSource
	if len(sources) == 0 || sameDecorationSources(sources, decorationSources(targetX, targetZ)) {
		netherSources = make([]decorationSource, 0, 25)
		for dx := int32(-2); dx <= 2; dx++ {
			for dz := int32(-2); dz <= 2; dz++ {
				sx, sz := targetX+dx, targetZ+dz
				if r.hasSourceNeighborhood(sx, sz) {
					netherSources = append(netherSources, decorationSource{X: sx, Z: sz})
				}
			}
		}
	} else {
		netherSources = make([]decorationSource, 0, len(sources))
		for _, src := range sources {
			if r.hasSourceNeighborhood(src.X, src.Z) {
				netherSources = append(netherSources, src)
			}
		}
	}

	if len(netherSources) == 0 {
		// Fallback for small test regions without full 3x3 neighborhoods.
		if err := r.placeScheduledStructures(od, seed, targetX, targetZ); err != nil {
			return err
		}
		nreg := &netherRegion{centerX: targetX, centerZ: targetZ, chunkMap: r.chunks}
		for _, src := range sources {
			if c := r.chunks[[2]int32{src.X, src.Z}]; c != nil {
				decorateNetherRegionSource(nreg, src.X, src.Z, seed, extractNetherChunkBiomes(c, od))
			}
		}
		return nil
	}

	plan := collectNetherStructurePlan(od, sets, seed, targetX, targetZ)
	inSources := make(map[[2]int32]bool, len(netherSources))
	for _, src := range netherSources {
		inSources[[2]int32{src.X, src.Z}] = true
	}
	outerChunks := make([][2]int32, 0, len(r.chunks))
	for key := range r.chunks {
		if !inSources[key] {
			outerChunks = append(outerChunks, key)
		}
	}
	sort.Slice(outerChunks, func(i, j int) bool {
		if outerChunks[i][0] != outerChunks[j][0] {
			return outerChunks[i][0] < outerChunks[j][0]
		}
		return outerChunks[i][1] < outerChunks[j][1]
	})
	for _, key := range outerChunks {
		plan.placeSurfaceStructuresInChunk(r, seed, key[0], key[1])
		plan.placeUndergroundStructuresInChunk(r, seed, key[0], key[1])
	}

	for _, src := range netherSources {
		if err := r.setSource(src.X, src.Z); err != nil {
			return err
		}
		if err := r.placeScheduledNetherStage(set, seed, 2); err != nil {
			return err
		}
		plan.placeSurfaceStructuresInChunk(r, seed, src.X, src.Z)
		if err := r.placeScheduledNetherStage(set, seed, 4); err != nil {
			return err
		}
		plan.placeUndergroundStructuresInChunk(r, seed, src.X, src.Z)
		if err := r.placeScheduledNetherStage(set, seed, 7); err != nil {
			return err
		}
		if err := r.placeScheduledNetherStage(set, seed, 9); err != nil {
			return err
		}
	}

	r.runPostProcessing(set)
	r.flushScheduledSprings()
	return nil
}

func (r *decorationRegion) placeScheduledNetherStage(set *worldgen.FeatureSet, seed int64, stage int) error {
	schedule, err := r.scheduledFeatures(stage)
	if err != nil {
		return err
	}
	random, decorationSeed := worldgen.DecorationRandom(seed, int(r.sourceX), int(r.sourceZ))
	origin := worldgen.FeaturePosition{X: int(r.sourceX) << 4, Y: r.genMinY(), Z: int(r.sourceZ) << 4}
	context := r.placementContext(nil)

	for _, scheduled := range schedule {
		placed, ok := set.Placed[scheduled.Name]
		if !ok {
			continue
		}
		configured, ok := set.LookupConfigured(placed.Feature)
		if !ok {
			continue
		}
		random.SetFeatureSeed(decorationSeed, scheduled.Index, stage)
		if scheduled.Name == "minecraft:spring_lava" {
			springCfg, err := set.Spring(placed.Feature)
			if err != nil {
				return err
			}
			if err := r.forEachVeryBiasedSpringPosition(set, scheduled.Name, stage, random, origin, func(pos worldgen.FeaturePosition) error {
				return r.placeSpring(set, pos, springCfg)
			}); err != nil {
				return err
			}
			continue
		}

		context.BiomeAllows = func(pos worldgen.FeaturePosition) bool {
			return r.biomeAllowsFeature(set, scheduled.Name, stage, pos)
		}
		err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(pos worldgen.FeaturePosition) error {
			return r.placeNetherConfiguredFeature(set, random, placed.Feature, configured.Type, stage, pos)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *decorationRegion) placeNetherConfiguredFeature(set *worldgen.FeatureSet, random worldgen.RandomSource, featureName, featureType string, stage int, pos worldgen.FeaturePosition) error {
	switch featureType {
	case "minecraft:basalt_pillar":
		r.placeNetherBasaltPillar(random, pos)
	case "minecraft:delta_feature":
		cfg, err := set.Delta(featureName)
		if err != nil {
			return err
		}
		r.placeNetherDeltaFeature(random, pos, cfg)
	case "minecraft:basalt_columns":
		cfg, err := set.BasaltColumns(featureName)
		if err != nil {
			return err
		}
		r.placeNetherBasaltColumns(random, pos, cfg)
	case "minecraft:netherrack_replace_blobs":
		cfg, err := set.ReplaceBlobs(featureName)
		if err != nil {
			return err
		}
		r.placeNetherReplaceBlobs(random, pos, cfg)
	case "minecraft:glowstone_blob":
		r.placeNetherGlowstoneBlob(random, pos)
	case "minecraft:ore":
		cfg, err := set.Ore(featureName)
		if err != nil {
			return err
		}
		if targets, ok := resolveOreTargets(set, cfg); ok {
			placeOreEllipsoidRegion(r, random, pos.X, pos.Y, pos.Z, cfg.Size, cfg.DiscardAirExposure, targets)
		}
	case "minecraft:scattered_ore":
		cfg, err := set.ScatteredOre(featureName)
		if err != nil {
			return err
		}
		if targets, ok := resolveOreTargets(set, cfg); ok {
			r.placeNetherScatteredOre(random, pos, cfg.Size, cfg.DiscardAirExposure, targets)
		}
	case "minecraft:spring_feature":
		cfg, err := set.Spring(featureName)
		if err != nil {
			return err
		}
		return r.placeSpring(set, pos, cfg)
	case "minecraft:simple_block":
		cfg, err := set.SimpleBlock(featureName)
		if err != nil {
			return err
		}
		r.placeSimpleBlockFeature(random, pos, cfg, set)
	case "minecraft:huge_fungus":
		cfg, err := set.HugeFungus(featureName)
		if err != nil {
			return err
		}
		r.placeNetherHugeFungus(set, random, pos, cfg)
	case "minecraft:nether_forest_vegetation":
		cfg, err := set.NetherForestVegetation(featureName)
		if err != nil {
			return err
		}
		r.placeNetherForestVegetation(set, random, pos, cfg)
	case "minecraft:weeping_vines":
		r.placeNetherWeepingVines(random, pos)
	case "minecraft:twisting_vines":
		cfg, err := set.TwistingVines(featureName)
		if err != nil {
			return err
		}
		r.placeNetherTwistingVines(random, pos, cfg)
	default:
		noteNotReplayed(fmt.Sprintf("nether_stage%d:%s", stage, featureType))
	}
	return nil
}

func (r *decorationRegion) placeNetherBasaltPillar(random worldgen.RandomSource, pos worldgen.FeaturePosition) bool {
	if !isAirState(r.getBlock(pos.X, pos.Y, pos.Z)) || isAirState(r.getBlock(pos.X, pos.Y+1, pos.Z)) {
		return false
	}
	minY := r.minY()
	maxYExcl := minY + r.worldHeight()
	cy := pos.Y
	north, south, west, east := true, true, true, true
	placeHangOff := func(hx, hy, hz int) bool {
		if random.NextIntN(10) != 0 {
			r.setBlock(hx, hy, hz, StateBasalt)
			return true
		}
		return false
	}
	placeBaseHangOff := func(bx, by, bz int) {
		if random.NextBoolean() {
			r.setBlock(bx, by, bz, StateBasalt)
		}
	}
	for isAirState(r.getBlock(pos.X, cy, pos.Z)) {
		if cy < minY || cy >= maxYExcl {
			return true
		}
		r.setBlock(pos.X, cy, pos.Z, StateBasalt)
		if north {
			north = placeHangOff(pos.X, cy, pos.Z-1)
		}
		if south {
			south = placeHangOff(pos.X, cy, pos.Z+1)
		}
		if west {
			west = placeHangOff(pos.X-1, cy, pos.Z)
		}
		if east {
			east = placeHangOff(pos.X+1, cy, pos.Z)
		}
		cy--
	}
	cy++
	placeBaseHangOff(pos.X, cy, pos.Z-1)
	placeBaseHangOff(pos.X, cy, pos.Z+1)
	placeBaseHangOff(pos.X-1, cy, pos.Z)
	placeBaseHangOff(pos.X+1, cy, pos.Z)
	cy--

	for dx := -3; dx < 4; dx++ {
		for dz := -3; dz < 4; dz++ {
			mag := abs(dx) * abs(dz)
			if int(random.NextIntN(10)) >= 10-mag {
				continue
			}
			px, py, pz := pos.X+dx, cy, pos.Z+dz
			rem := 3
			for isAirState(r.getBlock(px, py-1, pz)) {
				py--
				rem--
				if rem <= 0 {
					break
				}
			}
			if !isAirState(r.getBlock(px, py-1, pz)) {
				r.setBlock(px, py, pz, StateBasalt)
			}
		}
	}
	return true
}

func deltaCannotReplaceBlock(st uint16) bool {
	if st == StateBedrock {
		return true
	}
	info, ok := stateByID(st)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:bedrock",
		"minecraft:nether_bricks",
		"minecraft:nether_brick_fence",
		"minecraft:nether_brick_stairs",
		"minecraft:nether_wart",
		"minecraft:chest",
		"minecraft:spawner":
		return true
	}
	return false
}

func deltaRegionIsClear(r *decorationRegion, x, y, z int, contentsBlockName string) bool {
	st := r.getBlock(x, y, z)
	if blockNameEquals(st, contentsBlockName) || deltaCannotReplaceBlock(st) {
		return false
	}
	for _, off := range [6][3]int{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}} {
		isAir := isAirState(r.getBlock(x+off[0], y+off[1], z+off[2]))
		isUp := off[1] == 1
		if (isAir && !isUp) || (!isAir && isUp) {
			return false
		}
	}
	return true
}

func (r *decorationRegion) placeNetherDeltaFeature(random worldgen.RandomSource, pos worldgen.FeaturePosition, cfg worldgen.DeltaFeatureConfig) bool {
	contentsState, ok := nameToStateID(cfg.Contents.Name, cfg.Contents.Properties)
	if !ok {
		return false
	}
	rimState, ok := nameToStateID(cfg.Rim.Name, cfg.Rim.Properties)
	if !ok {
		return false
	}
	spawnRim := random.NextDouble() < 0.9
	rimX, rimZ := 0, 0
	if spawnRim {
		rimX = cfg.RimSize.Sample(random)
		rimZ = cfg.RimSize.Sample(random)
	}
	hasRim := spawnRim && rimX != 0 && rimZ != 0
	radX := cfg.Size.Sample(random)
	radZ := cfg.Size.Sample(random)
	maxRad := max(radX, radZ)
	placed := false
	withinManhattan(pos.X, pos.Y, pos.Z, radX, 0, radZ, func(px, py, pz, dist int) bool {
		if dist > maxRad {
			return false
		}
		if deltaRegionIsClear(r, px, py, pz, cfg.Contents.Name) {
			if hasRim {
				placed = true
				r.setBlock(px, py, pz, rimState)
			}
			ix, iz := px+rimX, pz+rimZ
			if deltaRegionIsClear(r, ix, py, iz, cfg.Contents.Name) {
				placed = true
				r.setBlock(ix, py, iz, contentsState)
			}
		}
		return true
	})
	return placed
}

func basaltColumnCannotPlaceOn(st uint16) bool {
	if isLavaState(st) || st == StateBedrock || st == StateMagmaBlock || st == StateSoulSand {
		return true
	}
	info, ok := stateByID(st)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:lava",
		"minecraft:bedrock",
		"minecraft:magma_block",
		"minecraft:soul_sand",
		"minecraft:nether_bricks",
		"minecraft:nether_brick_fence",
		"minecraft:nether_brick_stairs",
		"minecraft:nether_wart",
		"minecraft:chest",
		"minecraft:spawner":
		return true
	}
	return false
}

func (r *decorationRegion) placeNetherBasaltColumns(random worldgen.RandomSource, pos worldgen.FeaturePosition, cfg worldgen.BasaltColumnsFeatureConfig) bool {
	seaLevel := NetherDimensionType.SeaLevel
	minYPlus1 := r.minY() + 1
	maxY := r.minY() + r.worldHeight() - 1

	isAirOrLavaOcean := func(x, y, z int) bool {
		st := r.getBlock(x, y, z)
		return isAirState(st) || (isLavaState(st) && y <= seaLevel)
	}
	canPlaceAt := func(x, y, z int) bool {
		if !isAirOrLavaOcean(x, y, z) {
			return false
		}
		below := r.getBlock(x, y-1, z)
		return !isAirState(below) && !basaltColumnCannotPlaceOn(below)
	}
	findSurface := func(x, y, z, dist int) (int, bool) {
		for y > minYPlus1 && dist > 0 {
			dist--
			if canPlaceAt(x, y, z) {
				return y, true
			}
			y--
		}
		return 0, false
	}
	findAir := func(x, y, z, dist int) (int, bool) {
		for y <= maxY && dist > 0 {
			dist--
			st := r.getBlock(x, y, z)
			if basaltColumnCannotPlaceOn(st) {
				return 0, false
			}
			if isAirState(st) {
				return y, true
			}
			y++
		}
		return 0, false
	}

	if !canPlaceAt(pos.X, pos.Y, pos.Z) {
		return false
	}
	h := cfg.Height.Sample(random)
	clustered := random.NextFloat() < 0.9
	limit := min(h, 8)
	count := 15
	if clustered {
		limit = min(h, 5)
		count = 50
	}
	placed := false
	span := int32(2*limit + 1)
	for i := 0; i < count; i++ {
		px := pos.X - limit + int(random.NextIntN(span))
		py := pos.Y + int(random.NextIntN(1))
		pz := pos.Z - limit + int(random.NextIntN(span))
		colH := h - (abs(px-pos.X) + abs(py-pos.Y) + abs(pz-pos.Z))
		if colH < 0 {
			continue
		}
		reach := cfg.Reach.Sample(random)
		for gz := pz - reach; gz <= pz+reach; gz++ {
			for gx := px - reach; gx <= px+reach; gx++ {
				dist := abs(gx-px) + abs(gz-pz)
				var sy int
				var ok bool
				if isAirOrLavaOcean(gx, py, gz) {
					sy, ok = findSurface(gx, py, gz, dist)
				} else {
					sy, ok = findAir(gx, py, gz, dist)
				}
				if !ok {
					continue
				}
				rem := colH - dist/2
				cy := sy
				for rem >= 0 {
					if isAirOrLavaOcean(gx, cy, gz) {
						r.setBlock(gx, cy, gz, StateBasalt)
						cy++
						placed = true
					} else if blockNameEquals(r.getBlock(gx, cy, gz), "minecraft:basalt") {
						cy++
					} else {
						break
					}
					rem--
				}
			}
		}
	}
	return placed
}

func (r *decorationRegion) placeNetherReplaceBlobs(random worldgen.RandomSource, pos worldgen.FeaturePosition, cfg worldgen.ReplaceBlobsFeatureConfig) bool {
	replaceState, ok := nameToStateID(cfg.State.Name, cfg.State.Properties)
	if !ok {
		return false
	}
	minYPlus1 := r.minY() + 1
	maxY := r.minY() + r.worldHeight() - 1
	cy := min(max(pos.Y, minYPlus1), maxY)
	found := false
	for cy > minYPlus1 {
		if blockNameEquals(r.getBlock(pos.X, cy, pos.Z), cfg.Target.Name) {
			found = true
			break
		}
		cy--
	}
	if !found {
		return false
	}
	rx := cfg.Radius.Sample(random)
	ry := cfg.Radius.Sample(random)
	rz := cfg.Radius.Sample(random)
	maxR := max(rx, max(ry, rz))
	placed := false
	withinManhattan(pos.X, cy, pos.Z, rx, ry, rz, func(px, py, pz, dist int) bool {
		if dist > maxR {
			return false
		}
		if blockNameEquals(r.getBlock(px, py, pz), cfg.Target.Name) {
			r.setBlock(px, py, pz, replaceState)
			placed = true
		}
		return true
	})
	return placed
}

func (r *decorationRegion) placeNetherGlowstoneBlob(random worldgen.RandomSource, pos worldgen.FeaturePosition) bool {
	if !isAirState(r.getBlock(pos.X, pos.Y, pos.Z)) {
		return false
	}
	above := r.getBlock(pos.X, pos.Y+1, pos.Z)
	if !blockNameEquals(above, "minecraft:netherrack") &&
		!blockNameEquals(above, "minecraft:basalt") &&
		!blockNameEquals(above, "minecraft:blackstone") {
		return false
	}
	r.setBlock(pos.X, pos.Y, pos.Z, StateGlowstone)
	dirs := [6][3]int{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}
	for i := 0; i < 1500; i++ {
		px := pos.X + int(random.NextIntN(8)) - int(random.NextIntN(8))
		py := pos.Y - int(random.NextIntN(12))
		pz := pos.Z + int(random.NextIntN(8)) - int(random.NextIntN(8))
		if !isAirState(r.getBlock(px, py, pz)) {
			continue
		}
		neighbors := 0
		for _, d := range dirs {
			if r.getBlock(px+d[0], py+d[1], pz+d[2]) == StateGlowstone {
				neighbors++
				if neighbors > 1 {
					break
				}
			}
		}
		if neighbors == 1 {
			r.setBlock(px, py, pz, StateGlowstone)
		}
	}
	return true
}

func javaMathRoundFloat(f float32) int {
	return int(math.Floor(float64(f) + 0.5))
}

func (r *decorationRegion) placeNetherScatteredOre(random worldgen.RandomSource, pos worldgen.FeaturePosition, size int, discard float64, targets []resolvedOreTarget) bool {
	count := int(random.NextIntN(int32(size + 1)))
	for i := 0; i < count; i++ {
		dist := min(i, 7)
		px := pos.X + javaMathRoundFloat((random.NextFloat()-random.NextFloat())*float32(dist))
		py := pos.Y + javaMathRoundFloat((random.NextFloat()-random.NextFloat())*float32(dist))
		pz := pos.Z + javaMathRoundFloat((random.NextFloat()-random.NextFloat())*float32(dist))
		current := r.getBlock(px, py, pz)
		for _, target := range targets {
			if !canPlaceOreBlock(current, target, discard, random, func() bool {
				return exposedToAirRegion(r, px, py, pz)
			}) {
				continue
			}
			r.setBlock(px, py, pz, target.state)
			break
		}
	}
	return true
}

func (r *decorationRegion) placeNetherHugeFungus(set *worldgen.FeatureSet, random worldgen.RandomSource, pos worldgen.FeaturePosition, cfg worldgen.HugeFungusFeatureConfig) bool {
	if !blockNameEquals(r.getBlock(pos.X, pos.Y-1, pos.Z), cfg.ValidBaseBlock.Name) {
		return false
	}
	height := 4 + int(random.NextIntN(10))
	if random.NextIntN(12) == 0 {
		height *= 2
	}
	if !cfg.Planted && pos.Y+height+1 >= r.genHeight() {
		return false
	}
	huge := !cfg.Planted && random.NextFloat() < 0.06
	r.setBlock(pos.X, pos.Y, pos.Z, StateAir)

	stemState, ok := nameToStateID(cfg.StemState.Name, cfg.StemState.Properties)
	if !ok {
		return false
	}
	hatState, ok := nameToStateID(cfg.HatState.Name, cfg.HatState.Properties)
	if !ok {
		return false
	}
	decorState, ok := nameToStateID(cfg.DecorState.Name, cfg.DecorState.Properties)
	if !ok {
		decorState = StateShroomlight
	}

	isReplaceable := func(x, y, z int, allowPlant bool) bool {
		st := r.getBlock(x, y, z)
		if isAirState(st) {
			return true
		}
		if info, ok := stateByID(st); ok && flattenBlockTagContains(set, "minecraft:replaceable", info.Name) {
			return true
		}
		if allowPlant && len(cfg.ReplaceableBlocks) > 0 {
			ok, err := r.testBlockPredicate(set, cfg.ReplaceableBlocks, worldgen.FeaturePosition{X: x, Y: y, Z: z})
			return err == nil && ok
		}
		return false
	}

	// placeStem
	rad := 0
	if huge {
		rad = 1
	}
	for dx := -rad; dx <= rad; dx++ {
		for dz := -rad; dz <= rad; dz++ {
			corner := huge && abs(dx) == rad && abs(dz) == rad
			for dy := 0; dy < height; dy++ {
				px, py, pz := pos.X+dx, pos.Y+dy, pos.Z+dz
				if isReplaceable(px, py, pz, true) {
					if corner {
						if random.NextFloat() < 0.1 {
							r.setBlock(px, py, pz, stemState)
						}
					} else {
						r.setBlock(px, py, pz, stemState)
					}
				}
			}
		}
	}

	// placeHat
	isCrimson := cfg.HatState.Name == "minecraft:nether_wart_block"
	hatHeight := min(int(random.NextIntN(int32(1+height/3)))+5, height)
	startY := height - hatHeight
	for dy := startY; dy <= height; dy++ {
		radius := 1
		if dy < height-int(random.NextIntN(3)) {
			radius = 2
		}
		if hatHeight > 8 && dy < startY+4 {
			radius = 3
		}
		if huge {
			radius++
		}
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				edgeX := dx == -radius || dx == radius
				edgeZ := dz == -radius || dz == radius
				inner := !edgeX && !edgeZ && dy != height
				corner := edgeX && edgeZ
				lower := dy < startY+3
				px, py, pz := pos.X+dx, pos.Y+dy, pos.Z+dz
				if !isReplaceable(px, py, pz, false) {
					continue
				}
				if lower {
					if !inner {
						r.placeNetherFungusHatDropBlock(random, px, py, pz, hatState, cfg.HatState.Name, isCrimson)
					}
				} else if inner {
					vineChance := float32(0)
					if isCrimson {
						vineChance = 0.1
					}
					r.placeNetherFungusHatBlock(random, px, py, pz, hatState, decorState, 0.1, 0.2, vineChance)
				} else if corner {
					vineChance := float32(0)
					if isCrimson {
						vineChance = 0.083
					}
					r.placeNetherFungusHatBlock(random, px, py, pz, hatState, decorState, 0.01, 0.7, vineChance)
				} else {
					vineChance := float32(0)
					if isCrimson {
						vineChance = 0.07
					}
					r.placeNetherFungusHatBlock(random, px, py, pz, hatState, decorState, 0.0005, 0.98, vineChance)
				}
			}
		}
	}
	return true
}

func (r *decorationRegion) placeNetherFungusHatBlock(random worldgen.RandomSource, x, y, z int, hatState, decorState uint16, decorChance, hatChance, vineChance float32) {
	if random.NextFloat() < decorChance {
		r.setBlock(x, y, z, decorState)
	} else if random.NextFloat() < hatChance {
		r.setBlock(x, y, z, hatState)
		if random.NextFloat() < vineChance {
			r.tryPlaceNetherFungusWeepingVines(random, x, y, z)
		}
	}
}

func (r *decorationRegion) placeNetherFungusHatDropBlock(random worldgen.RandomSource, x, y, z int, hatState uint16, hatName string, isCrimson bool) {
	if blockNameEquals(r.getBlock(x, y-1, z), hatName) {
		r.setBlock(x, y, z, hatState)
	} else if float64(random.NextFloat()) < 0.15 {
		r.setBlock(x, y, z, hatState)
		if isCrimson && random.NextIntN(11) == 0 {
			r.tryPlaceNetherFungusWeepingVines(random, x, y, z)
		}
	}
}

func (r *decorationRegion) tryPlaceNetherFungusWeepingVines(random worldgen.RandomSource, x, y, z int) {
	if !isAirState(r.getBlock(x, y-1, z)) {
		return
	}
	length := springMthNextInt(random, 1, 5)
	if random.NextIntN(7) == 0 {
		length *= 2
	}
	r.placeNetherWeepingVinesColumn(random, x, y-1, z, length, 23, 25)
}

func (r *decorationRegion) placeNetherWeepingVinesColumn(random worldgen.RandomSource, x, y, z, length, minAge, maxAge int) {
	nb := loadNetherBlocks()
	cy := y
	for i := 0; i <= length; i++ {
		if isAirState(r.getBlock(x, cy, z)) {
			if i == length || !isAirState(r.getBlock(x, cy-1, z)) {
				age := min(max(springMthNextInt(random, minAge, maxAge), 0), 25)
				r.setBlock(x, cy, z, nb.weepingVines[age])
				break
			}
			r.setBlock(x, cy, z, nb.weepingVinesPlant)
		}
		cy--
	}
}

func (r *decorationRegion) placeNetherForestVegetation(set *worldgen.FeatureSet, random worldgen.RandomSource, pos worldgen.FeaturePosition, cfg worldgen.NetherForestVegetationConfig) bool {
	below, ok := stateByID(r.getBlock(pos.X, pos.Y-1, pos.Z))
	if !ok || !flattenBlockTagContains(set, "minecraft:nylium", below.Name) {
		return false
	}
	minY := r.minY()
	maxY := minY + r.worldHeight() - 1
	if pos.Y < minY+1 || pos.Y+1 > maxY {
		return false
	}
	placed := 0
	for i := 0; i < cfg.SpreadWidth*cfg.SpreadWidth; i++ {
		px := pos.X + int(random.NextIntN(int32(cfg.SpreadWidth))) - int(random.NextIntN(int32(cfg.SpreadWidth)))
		py := pos.Y + int(random.NextIntN(int32(cfg.SpreadHeight))) - int(random.NextIntN(int32(cfg.SpreadHeight)))
		pz := pos.Z + int(random.NextIntN(int32(cfg.SpreadWidth))) - int(random.NextIntN(int32(cfg.SpreadWidth)))
		targetPos := worldgen.FeaturePosition{X: px, Y: py, Z: pz}
		chosen, ok := cfg.StateProvider.SampleStateAt(random, targetPos)
		if !ok || !isAirState(r.getBlock(px, py, pz)) || py <= minY {
			continue
		}
		if !r.simpleBlockCanSurvive(targetPos, chosen.Name, set) {
			continue
		}
		if stID, ok := nameToStateID(chosen.Name, chosen.Properties); ok {
			r.setBlock(px, py, pz, stID)
			placed++
		}
	}
	return placed > 0
}

func (r *decorationRegion) placeNetherWeepingVines(random worldgen.RandomSource, pos worldgen.FeaturePosition) bool {
	if !isAirState(r.getBlock(pos.X, pos.Y, pos.Z)) {
		return false
	}
	above := r.getBlock(pos.X, pos.Y+1, pos.Z)
	if !blockNameEquals(above, "minecraft:netherrack") && !blockNameEquals(above, "minecraft:nether_wart_block") {
		return false
	}
	r.setBlock(pos.X, pos.Y, pos.Z, StateNetherWartBlock)
	dirs := [6][3]int{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}
	for i := 0; i < 200; i++ {
		px := pos.X + int(random.NextIntN(6)) - int(random.NextIntN(6))
		py := pos.Y + int(random.NextIntN(2)) - int(random.NextIntN(5))
		pz := pos.Z + int(random.NextIntN(6)) - int(random.NextIntN(6))
		if !isAirState(r.getBlock(px, py, pz)) {
			continue
		}
		neighbors := 0
		for _, d := range dirs {
			nb := r.getBlock(px+d[0], py+d[1], pz+d[2])
			if blockNameEquals(nb, "minecraft:netherrack") || blockNameEquals(nb, "minecraft:nether_wart_block") {
				neighbors++
				if neighbors > 1 {
					break
				}
			}
		}
		if neighbors == 1 {
			r.setBlock(px, py, pz, StateNetherWartBlock)
		}
	}
	for i := 0; i < 100; i++ {
		px := pos.X + int(random.NextIntN(8)) - int(random.NextIntN(8))
		py := pos.Y + int(random.NextIntN(2)) - int(random.NextIntN(7))
		pz := pos.Z + int(random.NextIntN(8)) - int(random.NextIntN(8))
		if !isAirState(r.getBlock(px, py, pz)) {
			continue
		}
		ab := r.getBlock(px, py+1, pz)
		if !blockNameEquals(ab, "minecraft:netherrack") && !blockNameEquals(ab, "minecraft:nether_wart_block") {
			continue
		}
		length := springMthNextInt(random, 1, 8)
		if random.NextIntN(6) == 0 {
			length *= 2
		}
		if random.NextIntN(5) == 0 {
			length = 1
		}
		r.placeNetherWeepingVinesColumn(random, px, py, pz, length, 17, 25)
	}
	return true
}

func (r *decorationRegion) twistingVinesInvalid(x, y, z int) bool {
	if !isAirState(r.getBlock(x, y, z)) {
		return true
	}
	below := r.getBlock(x, y-1, z)
	return !blockNameEquals(below, "minecraft:netherrack") &&
		!blockNameEquals(below, "minecraft:warped_nylium") &&
		!blockNameEquals(below, "minecraft:warped_wart_block")
}

func (r *decorationRegion) placeNetherTwistingVines(random worldgen.RandomSource, pos worldgen.FeaturePosition, cfg worldgen.TwistingVinesFeatureConfig) bool {
	if r.twistingVinesInvalid(pos.X, pos.Y, pos.Z) {
		return false
	}
	minY := r.minY()
	maxYExcl := minY + r.worldHeight()
	nb := loadNetherBlocks()
	findFirstAirAboveGround := func(x, y, z int) (int, bool) {
		for {
			y--
			if y < minY || y >= maxYExcl {
				return 0, false
			}
			if !isAirState(r.getBlock(x, y, z)) {
				return y + 1, true
			}
		}
	}
	for i := 0; i < cfg.SpreadWidth*cfg.SpreadWidth; i++ {
		px := pos.X + springMthNextInt(random, -cfg.SpreadWidth, cfg.SpreadWidth)
		py := pos.Y + springMthNextInt(random, -cfg.SpreadHeight, cfg.SpreadHeight)
		pz := pos.Z + springMthNextInt(random, -cfg.SpreadWidth, cfg.SpreadWidth)
		airY, ok := findFirstAirAboveGround(px, py, pz)
		if !ok || r.twistingVinesInvalid(px, airY, pz) {
			continue
		}
		length := springMthNextInt(random, 1, cfg.MaxHeight)
		if random.NextIntN(6) == 0 {
			length *= 2
		}
		if random.NextIntN(5) == 0 {
			length = 1
		}
		cy := airY
		for step := 1; step <= length; step++ {
			if isAirState(r.getBlock(px, cy, pz)) {
				if step == length || !isAirState(r.getBlock(px, cy+1, pz)) {
					age := min(max(springMthNextInt(random, 17, 25), 0), 25)
					r.setBlock(px, cy, pz, nb.twistingVines[age])
					break
				}
				r.setBlock(px, cy, pz, nb.twistingVinesPlant)
			}
			cy++
		}
	}
	return true
}
