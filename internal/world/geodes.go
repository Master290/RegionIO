package world

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"regionio/internal/worldgen"
)

const geodesStage = 2

type geodePoint struct {
	x, y, z int
	offset  int
}

// placeScheduledGeodes replays the features of index 2, which the pack fills with the
// amethyst geodes plus the surface's local modifications - forest_rock's boulders, the
// large dripstone of carver-exposed caverns, and the frozen-ocean icebergs. The layer
// calculation follows GeodeFeature.place: sampled distance points are combined
// with vanilla normal-noise perturbation, then the nearest layer threshold
// selects filling, inner, middle, or outer material.
func (r *decorationRegion) placeScheduledGeodes(seed int64) error {
	r.bindSeed(seed)
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		return err
	}
	if err := r.ensureSourceNeighborhood(); err != nil {
		return err
	}
	schedule, err := set.FeatureSchedule(possibleBiomeOrder(), r.sourceBiomes(), geodesStage)
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
		// This step holds both the geodes and the boulders, so the walk dispatches on
		// the configured type and counts whatever it cannot place. It used to continue
		// on anything that was not a geode, which is how forest_rock stayed invisible
		// while a hand-written boulder heuristic placed a different shape instead.
		switch configured.Type {
		case "minecraft:geode", "minecraft:block_blob", "minecraft:large_dripstone", "minecraft:iceberg":
		default:
			noteNotReplayed(fmt.Sprintf("stage%d:%s", geodesStage, configured.Type))
			continue
		}
		random.SetFeatureSeed(decorationSeed, scheduled.Index, geodesStage)
		context := r.placementContext(func(position worldgen.FeaturePosition) bool {
			return r.biomeAllowsFeature(set, scheduled.Name, geodesStage, position)
		})
		switch configured.Type {
		case "minecraft:block_blob":
			blob, err := set.BlockBlob(placed.Feature)
			if err != nil {
				return err
			}
			if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
				return r.placeBlockBlob(set, random, position, blob)
			}); err != nil {
				return err
			}
			continue
		case "minecraft:large_dripstone":
			dripstone, err := set.LargeDripstone(placed.Feature)
			if err != nil {
				return err
			}
			if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
				return r.placeLargeDripstone(set, random, position, dripstone)
			}); err != nil {
				return err
			}
			continue
		case "minecraft:iceberg":
			iceCfg, err := set.BlockStateConfig(placed.Feature)
			if err != nil {
				return err
			}
			if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
				r.placeIceberg(random, position, iceCfg)
				return nil
			}); err != nil {
				return err
			}
			continue
		}
		config, err := set.Geode(placed.Feature)
		if err != nil {
			return err
		}
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			r.placeGeode(random, seed, position, config, set)
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *decorationRegion) placeGeode(random worldgen.RandomSource, seed int64, origin worldgen.FeaturePosition, config worldgen.GeodeFeatureConfig, set *worldgen.FeatureSet) bool {
	distributionPoints := sampleGeodeInt(random, config.DistributionMin, config.DistributionMax)
	pointScale := float64(distributionPoints) / float64(config.OuterWallMax)
	fillingThreshold := 1 / math.Sqrt(config.FillingLayer)
	innerThreshold := 1 / math.Sqrt(config.InnerLayer+pointScale)
	middleThreshold := 1 / math.Sqrt(config.MiddleLayer+pointScale)
	outerThreshold := 1 / math.Sqrt(config.OuterLayer+pointScale)
	crackSize := config.BaseCrackSize + random.NextDouble()/2
	if distributionPoints > 3 {
		crackSize += pointScale
	}
	crackThreshold := 1 / math.Sqrt(crackSize)
	generateCrack := random.NextFloat() < float32(config.CrackChance)

	points := make([]geodePoint, 0, distributionPoints)
	invalid := 0
	invalidBlocks := tagStateIDs(set, config.InvalidBlocksTag)
	for i := 0; i < distributionPoints; i++ {
		point := geodePoint{
			x:      origin.X + sampleGeodeInt(random, config.OuterWallMin, config.OuterWallMax),
			y:      origin.Y + sampleGeodeInt(random, config.OuterWallMin, config.OuterWallMax),
			z:      origin.Z + sampleGeodeInt(random, config.OuterWallMin, config.OuterWallMax),
			offset: sampleGeodeInt(random, config.PointOffsetMin, config.PointOffsetMax),
		}
		state := r.getBlock(point.x, point.y, point.z)
		if isAirState(state) || invalidBlocks[state] {
			invalid++
			if invalid > config.InvalidBlocksThreshold {
				return false
			}
		}
		points = append(points, point)
	}

	crackPoints := geodeCrackPoints(origin, random, distributionPoints, generateCrack)
	noise := worldgen.NewNormalNoise(worldgen.NewLegacy(seed), -4, []float64{1})
	cannotReplace := tagStateIDs(set, config.CannotReplaceTag)
	placed := false
	var potentialPlacements [][3]int
	for z := origin.Z + config.MinGenOffset; z <= origin.Z+config.MaxGenOffset; z++ {
		for y := origin.Y + config.MinGenOffset; y <= origin.Y+config.MaxGenOffset; y++ {
			for x := origin.X + config.MinGenOffset; x <= origin.X+config.MaxGenOffset; x++ {
				perturbation := noise.GetValue(float64(x), float64(y), float64(z)) * config.NoiseMultiplier
				innerDistance := geodeDistance(x, y, z, points, perturbation)
				if innerDistance < outerThreshold {
					continue
				}
				current := r.getBlock(x, y, z)
				if cannotReplace[current] {
					continue
				}
				crackDistance := 0.0
				if generateCrack {
					crackDistance = geodeDistanceWithOffset(x, y, z, crackPoints, config.CrackPointOffset, perturbation)
				}
				var state uint16
				switch {
				case generateCrack && crackDistance >= crackThreshold && innerDistance < fillingThreshold:
					state = StateAir
				case innerDistance >= fillingThreshold:
					state, _ = nameToStateID(config.Filling.Name, config.Filling.Properties)
				case innerDistance >= innerThreshold:
					alternate := random.NextFloat() < float32(config.UseAlternateLayerChance)
					provider := config.Inner
					if alternate && config.AlternateInner.Name != "" {
						provider = config.AlternateInner
					}
					state, _ = nameToStateID(provider.Name, provider.Properties)
					if (!config.PlacementsRequireAlternate || alternate) &&
						random.NextFloat() < float32(config.UsePotentialPlacementsChance) {
						potentialPlacements = append(potentialPlacements, [3]int{x, y, z})
					}
				case innerDistance >= middleThreshold:
					state, _ = nameToStateID(config.Middle.Name, config.Middle.Properties)
				case innerDistance >= outerThreshold:
					state, _ = nameToStateID(config.Outer.Name, config.Outer.Properties)
				default:
					continue
				}
				if r.setBlock(x, y, z, state) {
					placed = true
				}
			}
		}
	}
	r.placeGeodeInnerPlacements(random, potentialPlacements, config, cannotReplace)
	return placed
}

func (r *decorationRegion) placeGeodeInnerPlacements(random worldgen.RandomSource, positions [][3]int, config worldgen.GeodeFeatureConfig, cannotReplace map[uint16]bool) {
	if len(config.InnerPlacements) == 0 {
		return
	}
	directions := [...]struct {
		dx, dy, dz int
		name       string
	}{
		{0, -1, 0, "down"}, {0, 1, 0, "up"}, {0, 0, -1, "north"},
		{0, 0, 1, "south"}, {-1, 0, 0, "west"}, {1, 0, 0, "east"},
	}
	for _, position := range positions {
		placement := config.InnerPlacements[int(random.NextIntN(int32(len(config.InnerPlacements))))]
		for _, direction := range directions {
			x, y, z := position[0]+direction.dx, position[1]+direction.dy, position[2]+direction.dz
			current := r.getBlock(x, y, z)
			if !isAirState(current) && !isWaterState(current) {
				continue
			}
			props := make(map[string]string, len(placement.Properties))
			for key, value := range placement.Properties {
				props[key] = value
			}
			props["facing"] = direction.name
			props["waterlogged"] = "false"
			if isWaterState(current) {
				props["waterlogged"] = "true"
			}
			state, ok := nameToStateID(placement.Name, props)
			if ok && !cannotReplace[current] {
				if r.setBlock(x, y, z, state) {
					break
				}
			}
		}
	}
}

func sampleGeodeInt(random worldgen.RandomSource, min, max int) int {
	if max <= min {
		return min
	}
	return min + int(random.NextIntN(int32(max-min+1)))
}

// tagStateIDs resolves a block tag to the state IDs that satisfy it. Geodes, lakes, the
//
// lush-cave vegetation patches and ore targets all ask the same question of a tag, so they
// share this one helper - which is the point: a second implementation is how the bug below
// ended up fixed in one caller and left standing in two others.
//
// Vanilla's test is on the *block*: MatchingBlockTagPredicate.test is
// BlockState.is(TagKey<Block>), and a block tag is a set of blocks, so every
// state of a member matches. Keying the set by state ID alone is therefore only
// correct if each member's default state is its only state - and for
// #minecraft:moss_replaceable that is false in a way that bites: it pulls in
// #minecraft:cave_vines, which carries 52 states in this build (`age` 0..25
// times `berries`, not the 25 that counting `age` alone would suggest) - so an aged
// vine hanging in a lush-cave ceiling read as non-replaceable here while vanilla
// walks its vegetation column straight through it.
func tagStateIDs(set *worldgen.FeatureSet, tag string) map[uint16]bool {
	// Configs disagree about whether the '#' is already there - geode and
	// vegetation-patch tags arrive as "#minecraft:..." and lake tags as bare names -
	// so normalise rather than assume: prefixing an entry that already carries the
	// marker asks for "##minecraft:...", which resolves to no blocks at all and
	// silently turns the feature off.
	cacheKey := blockSetCacheKey{set: set, key: tag}
	blockSetCacheMu.RLock()
	if cached, ok := blockSetCache[cacheKey]; ok {
		blockSetCacheMu.RUnlock()
		return cached
	}
	blockSetCacheMu.RUnlock()

	normTag := tag
	if !strings.HasPrefix(normTag, "#") {
		normTag = "#" + normTag
	}
	ids := blockSetStateIDs(set, []string{normTag})
	if tag != normTag {
		blockSetCacheMu.Lock()
		if len(blockSetCache) >= maxBlockSetCacheEntries {
			clear(blockSetCache)
		}
		blockSetCache[cacheKey] = ids
		blockSetCacheMu.Unlock()
	}
	return ids
}

// blockSetStateIDs is the shared expansion underneath it: a list of the shape
// vanilla's BlockStateIngredient$Blocks accepts, where an entry is either a block
// name or a '#tag', and each denotes every state of the blocks it names.
//
// Two distinct mistakes are possible here and both were live in the disk feature,
// which had its own copy of this loop: a '#tag' entry resolved through
// nameToStateID simply fails, so the member vanished silently, and a bare block
// name resolved with no properties yields its default state, so waterlogged,
// snowy and aged variants were missing. carvers and monster rooms already expanded
// via idsByName, with a comment explaining that tags name blocks rather than
// states - the package knew; the helper had not been written down once.
type blockSetCacheKey struct {
	set *worldgen.FeatureSet
	key string
}

const maxBlockSetCacheEntries = 1024

var (
	blockSetCacheMu sync.RWMutex
	blockSetCache   = make(map[blockSetCacheKey]map[uint16]bool)
)

func blockSetStateIDs(set *worldgen.FeatureSet, entries []string) map[uint16]bool {
	cacheKey := blockSetCacheKey{set: set}
	if len(entries) == 1 {
		cacheKey.key = entries[0]
	} else {
		cacheKey.key = strings.Join(entries, "\x00")
	}
	blockSetCacheMu.RLock()
	if cached, ok := blockSetCache[cacheKey]; ok {
		blockSetCacheMu.RUnlock()
		return cached
	}
	blockSetCacheMu.RUnlock()

	ids := make(map[uint16]bool)
	var names []string
	for _, entry := range entries {
		if strings.HasPrefix(entry, "#") {
			names = append(names, flattenBlockTag(set, entry[1:], nil)...)
			continue
		}
		names = append(names, entry)
	}
	for _, name := range names {
		if _, ok := nameToStateID(name, nil); !ok {
			continue
		}
		for _, id := range idsByName[name] {
			ids[id] = true
		}
	}

	blockSetCacheMu.Lock()
	if len(blockSetCache) >= maxBlockSetCacheEntries {
		clear(blockSetCache)
	}
	blockSetCache[cacheKey] = ids
	blockSetCacheMu.Unlock()
	return ids
}

func geodeDistance(x, y, z int, points []geodePoint, perturbation float64) float64 {
	distance := 0.0
	for _, point := range points {
		dx, dy, dz := x-point.x, y-point.y, z-point.z
		distance += 1/math.Sqrt(float64(dx*dx+dy*dy+dz*dz+point.offset)) + perturbation
	}
	return distance
}

func geodeDistanceWithOffset(x, y, z int, points []geodePoint, offset int, perturbation float64) float64 {
	distance := 0.0
	for _, point := range points {
		dx, dy, dz := x-point.x, y-point.y, z-point.z
		distance += 1/math.Sqrt(float64(dx*dx+dy*dy+dz*dz+offset)) + perturbation
	}
	return distance
}

func geodeCrackPoints(origin worldgen.FeaturePosition, random worldgen.RandomSource, distributionPoints int, enabled bool) []geodePoint {
	if !enabled {
		return nil
	}
	offset := distributionPoints*2 + 1
	switch random.NextIntN(4) {
	case 0:
		return []geodePoint{{x: origin.X + offset, y: origin.Y + 7, z: origin.Z}, {x: origin.X + offset, y: origin.Y + 5, z: origin.Z}, {x: origin.X + offset, y: origin.Y + 1, z: origin.Z}}
	case 1:
		return []geodePoint{{x: origin.X, y: origin.Y + 7, z: origin.Z + offset}, {x: origin.X, y: origin.Y + 5, z: origin.Z + offset}, {x: origin.X, y: origin.Y + 1, z: origin.Z + offset}}
	case 2:
		return []geodePoint{{x: origin.X + offset, y: origin.Y + 7, z: origin.Z + offset}, {x: origin.X + offset, y: origin.Y + 5, z: origin.Z + offset}, {x: origin.X + offset, y: origin.Y + 1, z: origin.Z + offset}}
	default:
		return []geodePoint{{x: origin.X, y: origin.Y + 7, z: origin.Z}, {x: origin.X, y: origin.Y + 5, z: origin.Z}, {x: origin.X, y: origin.Y + 1, z: origin.Z}}
	}
}

func (r *decorationRegion) placeIceberg(random worldgen.RandomSource, origin worldgen.FeaturePosition, config worldgen.BlockStateFeatureConfig) bool {
	origin.Y = SeaLevel
	freehand := random.NextDouble() > 0.7
	mainState, ok := nameToStateID(config.State.Name, config.State.Properties)
	if !ok {
		return false
	}
	shapeAngle := random.NextDouble() * 2.0 * math.Pi
	ellipseA := 11 - int(random.NextIntN(5))
	ellipseC := 3 + int(random.NextIntN(3))
	isEllipse := random.NextDouble() > 0.7
	var overhangHeight int
	if isEllipse {
		overhangHeight = int(random.NextIntN(6)) + 6
	} else {
		overhangHeight = int(random.NextIntN(15)) + 3
	}
	var underWaterHeight int
	if !isEllipse && random.NextDouble() > 0.9 {
		underWaterHeight = int(random.NextIntN(19)) + 7
	} else {
		underWaterHeight = int(random.NextIntN(8)) + 7
	}
	height := min(overhangHeight+int(random.NextIntN(11)), 18)
	width := min(overhangHeight+int(random.NextIntN(7))-int(random.NextIntN(5)), 11)
	a := 11
	if isEllipse {
		a = ellipseA
	}
	for xo := -a; xo < a; xo++ {
		for zo := -a; zo < a; zo++ {
			for yOff := 0; yOff < height; yOff++ {
				var rad int
				if isEllipse {
					rad = icebergHeightDependentRadiusEllipse(yOff, height, width)
				} else {
					rad = icebergHeightDependentRadiusRound(random, yOff, height, width)
				}
				if isEllipse || xo < rad {
					r.generateIcebergBlock(random, origin, height, xo, yOff, zo, rad, a, isEllipse, ellipseC, shapeAngle, freehand, mainState)
				}
			}
		}
	}
	r.smoothIceberg(origin, width, height, isEllipse, ellipseA)
	for xo := -a; xo < a; xo++ {
		for zo := -a; zo < a; zo++ {
			for yOff := -1; yOff > -underWaterHeight; yOff-- {
				newA := a
				if isEllipse {
					newA = int(math.Ceil(float64(float32(a) * (1.0 - float32(math.Pow(float64(yOff), 2.0))/(float32(underWaterHeight)*8.0)))))
				}
				rad := icebergHeightDependentRadiusSteep(random, -yOff, underWaterHeight, width)
				if xo < rad {
					r.generateIcebergBlock(random, origin, underWaterHeight, xo, yOff, zo, rad, newA, isEllipse, ellipseC, shapeAngle, freehand, mainState)
				}
			}
		}
	}
	var isVerySteep bool
	if isEllipse {
		isVerySteep = random.NextDouble() > 0.1
	} else {
		isVerySteep = random.NextDouble() > 0.7
	}
	if isVerySteep {
		r.generateIcebergCutOut(random, width, height, origin, isEllipse, ellipseA, shapeAngle, ellipseC)
	}
	return true
}

func (r *decorationRegion) generateIcebergCutOut(random worldgen.RandomSource, width, height int, globalOrigin worldgen.FeaturePosition, isEllipse bool, shapeEllipseA int, shapeAngle float64, shapeEllipseC int) {
	randomSignX := 1
	if random.NextBoolean() {
		randomSignX = -1
	}
	randomSignZ := 1
	if random.NextBoolean() {
		randomSignZ = -1
	}
	xOff := int(random.NextIntN(int32(max(width/2-2, 1))))
	if random.NextBoolean() {
		xOff = width/2 + 1 - int(random.NextIntN(int32(max(width-width/2-1, 1))))
	}
	zOff := int(random.NextIntN(int32(max(width/2-2, 1))))
	if random.NextBoolean() {
		zOff = width/2 + 1 - int(random.NextIntN(int32(max(width-width/2-1, 1))))
	}
	if isEllipse {
		v := int(random.NextIntN(int32(max(shapeEllipseA-5, 1))))
		xOff = v
		zOff = v
	}
	localOrigin := worldgen.FeaturePosition{X: randomSignX * xOff, Y: 0, Z: randomSignZ * zOff}
	var angle float64
	if isEllipse {
		angle = shapeAngle + math.Pi/2.0
	} else {
		angle = random.NextDouble() * 2.0 * math.Pi
	}
	for yOff := 0; yOff < height-3; yOff++ {
		rad := icebergHeightDependentRadiusRound(random, yOff, height, width)
		r.carveIceberg(rad, yOff, globalOrigin, false, angle, localOrigin, shapeEllipseA, shapeEllipseC)
	}
	for yOff := -1; yOff > -height+int(random.NextIntN(5)); yOff-- {
		rad := icebergHeightDependentRadiusSteep(random, -yOff, height, width)
		r.carveIceberg(rad, yOff, globalOrigin, true, angle, localOrigin, shapeEllipseA, shapeEllipseC)
	}
}

func (r *decorationRegion) carveIceberg(radius, yOff int, globalOrigin worldgen.FeaturePosition, underWater bool, angle float64, localOrigin worldgen.FeaturePosition, shapeEllipseA, shapeEllipseC int) {
	a := radius + 1 + shapeEllipseA/3
	c := min(radius-3, 3) + shapeEllipseC/2 - 1
	for xo := -a; xo < a; xo++ {
		for zo := -a; zo < a; zo++ {
			signedDist := icebergSignedDistanceEllipse(xo, zo, localOrigin, a, c, angle)
			if signedDist < 0.0 {
				px, py, pz := globalOrigin.X+xo, globalOrigin.Y+yOff, globalOrigin.Z+zo
				cur := r.getBlock(px, py, pz)
				if isIcebergBlockState(cur) || blockNameEquals(cur, "minecraft:snow_block") {
					if underWater {
						if waterID, ok := nameToStateID("minecraft:water", map[string]string{"level": "0"}); ok {
							r.setBlockGlobal(px, py, pz, waterID)
						}
					} else {
						r.setBlockGlobal(px, py, pz, StateAir)
						if blockNameEquals(r.getBlock(px, py+1, pz), "minecraft:snow") {
							r.setBlockGlobal(px, py+1, pz, StateAir)
						}
					}
				}
			}
		}
	}
}

func (r *decorationRegion) generateIcebergBlock(random worldgen.RandomSource, origin worldgen.FeaturePosition, height, xo, yOff, zo, radius, a int, isEllipse bool, shapeEllipseC int, shapeAngle float64, snowOnTop bool, mainBlockState uint16) {
	var signedDist float64
	if isEllipse {
		signedDist = icebergSignedDistanceEllipse(xo, zo, worldgen.FeaturePosition{}, a, icebergGetEllipseC(yOff, height, shapeEllipseC), shapeAngle)
	} else {
		signedDist = icebergSignedDistanceCircle(xo, zo, worldgen.FeaturePosition{}, radius, random)
	}
	if signedDist < 0.0 {
		px, py, pz := origin.X+xo, origin.Y+yOff, origin.Z+zo
		var compareVal float64
		if isEllipse {
			compareVal = -0.5
		} else {
			compareVal = float64(-6 - int(random.NextIntN(3)))
		}
		if signedDist > compareVal && random.NextDouble() > 0.9 {
			return
		}
		r.setIcebergBlock(px, py, pz, random, height-yOff, height, isEllipse, snowOnTop, mainBlockState)
	}
}

func (r *decorationRegion) setIcebergBlock(px, py, pz int, random worldgen.RandomSource, hDiff, height int, isEllipse, snowOnTop bool, mainBlockState uint16) {
	cur := r.getBlock(px, py, pz)
	if isAirState(cur) || blockNameEquals(cur, "minecraft:snow_block") || blockNameEquals(cur, "minecraft:ice") || blockNameEquals(cur, "minecraft:water") {
		randomness := !isEllipse || random.NextDouble() > 0.05
		divisor := 2
		if isEllipse {
			divisor = 3
		}
		if snowOnTop && !blockNameEquals(cur, "minecraft:water") && float64(hDiff) <= float64(random.NextIntN(int32(max(1, height/divisor))))+float64(height)*0.6 && randomness {
			if snowID, ok := nameToStateID("minecraft:snow_block", nil); ok {
				r.setBlockGlobal(px, py, pz, snowID)
				return
			}
		}
		r.setBlockGlobal(px, py, pz, mainBlockState)
	}
}

func icebergGetEllipseC(yOff, height, shapeEllipseC int) int {
	c := shapeEllipseC
	if yOff > 0 && height-yOff <= 3 {
		c -= 4 - (height - yOff)
	}
	return c
}

func icebergSignedDistanceCircle(xo, zo int, origin worldgen.FeaturePosition, radius int, random worldgen.RandomSource) float64 {
	rf := random.NextFloat()
	if rf < 0.2 {
		rf = 0.2
	} else if rf > 0.8 {
		rf = 0.8
	}
	off := 10.0 * rf / float32(radius)
	return float64(off) + math.Pow(float64(xo-origin.X), 2.0) + math.Pow(float64(zo-origin.Z), 2.0) - math.Pow(float64(radius), 2.0)
}

func icebergSignedDistanceEllipse(xo, zo int, origin worldgen.FeaturePosition, a, c int, angle float64) float64 {
	dx := float64(xo - origin.X)
	dz := float64(zo - origin.Z)
	u := (dx*math.Cos(angle) - dz*math.Sin(angle)) / float64(a)
	v := (dx*math.Sin(angle) + dz*math.Cos(angle)) / float64(c)
	return math.Pow(u, 2.0) + math.Pow(v, 2.0) - 1.0
}

func icebergHeightDependentRadiusRound(random worldgen.RandomSource, yOff, height, width int) int {
	k := float32(3.5) - random.NextFloat()
	scale := (float32(1.0) - float32(math.Pow(float64(yOff), 2.0))/(float32(height)*k)) * float32(width)
	if height > 15+int(random.NextIntN(5)) {
		tempYOff := yOff
		if yOff < 3+int(random.NextIntN(6)) {
			tempYOff = yOff / 2
		}
		scale = (float32(1.0) - float32(tempYOff)/(float32(height)*k*float32(0.4))) * float32(width)
	}
	return int(math.Ceil(float64(scale / 2.0)))
}

func icebergHeightDependentRadiusEllipse(yOff, height, width int) int {
	scale := (float32(1.0) - float32(math.Pow(float64(yOff), 2.0))/float32(height)) * float32(width)
	return int(math.Ceil(float64(scale / 2.0)))
}

func icebergHeightDependentRadiusSteep(random worldgen.RandomSource, yOff, height, width int) int {
	k := float32(1.0) + random.NextFloat()/2.0
	scale := (float32(1.0) - float32(yOff)/(float32(height)*k)) * float32(width)
	return int(math.Ceil(float64(scale / 2.0)))
}

func isIcebergBlockState(state uint16) bool {
	return blockNameEquals(state, "minecraft:packed_ice") ||
		blockNameEquals(state, "minecraft:snow_block") ||
		blockNameEquals(state, "minecraft:blue_ice")
}

func blockNameEquals(state uint16, name string) bool {
	st, ok := stateByID(state)
	return ok && st.Name == name
}

func (r *decorationRegion) smoothIceberg(origin worldgen.FeaturePosition, width, height int, isEllipse bool, shapeEllipseA int) {
	a := width / 2
	if isEllipse {
		a = shapeEllipseA
	}
	for x := -a; x <= a; x++ {
		for z := -a; z <= a; z++ {
			for yOff := 0; yOff <= height; yOff++ {
				px, py, pz := origin.X+x, origin.Y+yOff, origin.Z+z
				cur := r.getBlock(px, py, pz)
				if isIcebergBlockState(cur) || blockNameEquals(cur, "minecraft:snow") {
					if isAirState(r.getBlock(px, py-1, pz)) {
						r.setBlockGlobal(px, py, pz, StateAir)
						r.setBlockGlobal(px, py+1, pz, StateAir)
					} else if isIcebergBlockState(cur) {
						sides := [4]uint16{
							r.getBlock(px-1, py, pz),
							r.getBlock(px+1, py, pz),
							r.getBlock(px, py, pz-1),
							r.getBlock(px, py, pz+1),
						}
						counter := 0
						for _, s := range sides {
							if !isIcebergBlockState(s) {
								counter++
							}
						}
						if counter >= 3 {
							r.setBlockGlobal(px, py, pz, StateAir)
						}
					}
				}
			}
		}
	}
}

