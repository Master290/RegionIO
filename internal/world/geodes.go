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
	invalidBlocks := geodeTagIDs(set, config.InvalidBlocksTag)
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
	cannotReplace := geodeTagIDs(set, config.CannotReplaceTag)
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

func geodeTagIDs(set *worldgen.FeatureSet, tag string) map[uint16]bool {
	if strings.HasPrefix(tag, "#") {
		tag = tag[1:]
	}
	ids := make(map[uint16]bool)
	for _, name := range flattenBlockTag(set, tag, nil) {
		if id, ok := nameToStateID(name, nil); ok {
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
