package world

import (
	"math"
	"strings"

	"regionio/internal/worldgen"
)

const geodesStage = 2

type geodePoint struct {
	x, y, z int
	offset  int
}

// placeScheduledGeodes replays the stage-2 amethyst geode feature. The layer
// calculation follows GeodeFeature.place: sampled distance points are combined
// with vanilla normal-noise perturbation, then the nearest layer threshold
// selects filling, inner, middle, or outer material.
func (r *decorationRegion) placeScheduledGeodes(seed int64) error {
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
		if !ok || configured.Type != "minecraft:geode" {
			continue
		}
		config, err := set.Geode(placed.Feature)
		if err != nil {
			return err
		}
		random.SetFeatureSeed(decorationSeed, scheduled.Index, geodesStage)
		context := r.placementContext(func(position worldgen.FeaturePosition) bool {
			return r.biomeAllowsFeature(set, scheduled.Name, geodesStage, position)
		})
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
		if state == StateAir || invalidBlocks[state] {
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
			if current != StateAir && !isWaterState(current) {
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
	if !strings.HasPrefix(tag, "#") {
		tag = "#" + tag
	}
	return blockSetStateIDs(set, []string{tag})
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
func blockSetStateIDs(set *worldgen.FeatureSet, entries []string) map[uint16]bool {
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
