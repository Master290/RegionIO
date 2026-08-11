package world

import (
	"math"

	"regionio/internal/worldgen"
)

func (r *decorationRegion) placeScheduledOres(seed int64) error {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		return err
	}
	schedule, err := r.scheduledFeatures(undergroundOresStage)
	if err != nil {
		return err
	}
	random, decorationSeed := worldgen.DecorationRandom(seed, int(r.sourceX), int(r.sourceZ))
	origin := worldgen.FeaturePosition{X: int(r.sourceX) << 4, Y: MinY, Z: int(r.sourceZ) << 4}
	for _, scheduled := range schedule {
		placed := set.Placed[scheduled.Name]
		configured := set.Configured[placed.Feature]
		if configured.Type != "minecraft:ore" {
			continue
		}
		config, err := set.Ore(placed.Feature)
		if err != nil {
			return err
		}
		targets, ok := resolveOreTargets(set, config)
		if !ok {
			continue
		}
		random.SetFeatureSeed(decorationSeed, scheduled.Index, undergroundOresStage)
		context := r.placementContext(func(position worldgen.FeaturePosition) bool {
			return r.biomeAllowsFeature(set, scheduled.Name, undergroundOresStage, position)
		})
		positions, err := set.PlacementPositions(scheduled.Name, random, origin, context)
		if err != nil {
			return err
		}
		for _, position := range positions {
			placeOreEllipsoidRegion(r, random, position.X, position.Y, position.Z, config.Size, config.DiscardAirExposure, targets)
		}
	}
	return nil
}

func placeOreEllipsoidRegion(region *decorationRegion, random worldgen.RandomSource, originX, originY, originZ, size int, discard float64, targets []resolvedOreTarget) {
	angle := random.NextFloat() * float32(math.Pi)
	extent := float32(size) / 8.0
	x0 := float64(originX) + math.Sin(float64(angle))*float64(extent)
	x1 := float64(originX) - math.Sin(float64(angle))*float64(extent)
	z0 := float64(originZ) + math.Cos(float64(angle))*float64(extent)
	z1 := float64(originZ) - math.Cos(float64(angle))*float64(extent)
	y0 := float64(originY + int(random.NextIntN(3)) - 2)
	y1 := float64(originY + int(random.NextIntN(3)) - 2)

	type sphere struct{ x, y, z, radius float64 }
	spheres := make([]sphere, size)
	for i := 0; i < size; i++ {
		t := float32(i) / float32(size)
		randomScale := random.NextDouble() * float64(size) / 16.0
		radius := ((float64(worldgen.MthSin(float64(float32(math.Pi)*t)))+1.0)*randomScale + 1.0) / 2.0
		spheres[i] = sphere{
			x: x0 + (x1-x0)*float64(t), y: y0 + (y1-y0)*float64(t),
			z: z0 + (z1-z0)*float64(t), radius: radius,
		}
	}
	for i := range spheres {
		if spheres[i].radius < 0 {
			continue
		}
		for j := i + 1; j < len(spheres); j++ {
			if spheres[j].radius < 0 {
				continue
			}
			dx, dy, dz := spheres[i].x-spheres[j].x, spheres[i].y-spheres[j].y, spheres[i].z-spheres[j].z
			dr := spheres[i].radius - spheres[j].radius
			if dr*dr > dx*dx+dy*dy+dz*dz {
				if dr > 0 {
					spheres[j].radius = -1
				} else {
					spheres[i].radius = -1
					break
				}
			}
		}
	}
	visited := make(map[[3]int]bool)
	for _, sphere := range spheres {
		if sphere.radius < 0 {
			continue
		}
		minX, maxX := int(math.Floor(sphere.x-sphere.radius)), int(math.Floor(sphere.x+sphere.radius))
		minY, maxY := int(math.Floor(sphere.y-sphere.radius)), int(math.Floor(sphere.y+sphere.radius))
		minZ, maxZ := int(math.Floor(sphere.z-sphere.radius)), int(math.Floor(sphere.z+sphere.radius))
		for x := minX; x <= maxX; x++ {
			dx := (float64(x) + 0.5 - sphere.x) / sphere.radius
			if dx*dx >= 1 {
				continue
			}
			for y := minY; y <= maxY; y++ {
				if y < MinY || y >= MinY+WorldHeight {
					continue
				}
				dy := (float64(y) + 0.5 - sphere.y) / sphere.radius
				if dx*dx+dy*dy >= 1 {
					continue
				}
				for z := minZ; z <= maxZ; z++ {
					dz := (float64(z) + 0.5 - sphere.z) / sphere.radius
					pos := [3]int{x, y, z}
					if dx*dx+dy*dy+dz*dz >= 1 || visited[pos] {
						continue
					}
					visited[pos] = true
					current := region.getBlock(x, y, z)
					for _, target := range targets {
						if !target.replaceables[current] || discard > 0 && random.NextFloat() < float32(discard) && exposedToAirRegion(region, x, y, z) {
							continue
						}
						region.setBlock(x, y, z, target.state)
						break
					}
				}
			}
		}
	}
}

func exposedToAirRegion(region *decorationRegion, x, y, z int) bool {
	for _, offset := range [][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}} {
		if region.getBlock(x+offset[0], y+offset[1], z+offset[2]) == StateAir {
			return true
		}
	}
	return false
}
