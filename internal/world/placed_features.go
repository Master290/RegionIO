package world

import (
	"math"

	"regionio/internal/worldgen"
)

const undergroundOresStage = 6

type resolvedOreTarget struct {
	state        uint16
	replaceables map[uint16]bool
}

func placeVanillaOres(c *Chunk, seed int64, cx, cz int32, biomes *[16][16]string) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		panic("world: loading feature datapack: " + err.Error())
	}
	random, decorationSeed := worldgen.DecorationRandom(seed, int(cx), int(cz))
	seen := make(map[string]bool)
	for bx := 0; bx < 16; bx += 4 {
		for bz := 0; bz < 16; bz += 4 {
			biome := set.Biomes[biomes[bx][bz]]
			if len(biome.Features) <= undergroundOresStage {
				continue
			}
			for featureIndex, name := range biome.Features[undergroundOresStage] {
				if seen[name] {
					continue
				}
				seen[name] = true
				placed := set.Placed[name]
				configured := set.Configured[placed.Feature]
				if configured.Type != "minecraft:ore" {
					continue
				}
				config, err := set.Ore(placed.Feature)
				if err != nil {
					panic(err)
				}
				plan, err := set.Placement(name)
				if err != nil {
					panic(err)
				}
				targets, ok := resolveOreTargets(set, config)
				if !ok {
					continue
				}
				random.SetFeatureSeed(decorationSeed, featureIndex, undergroundOresStage)
				if plan.RarityChance > 0 && random.NextIntN(int32(plan.RarityChance)) != 0 {
					continue
				}
				attempts := plan.Count.Sample(random)
				for attempt := 0; attempt < attempts; attempt++ {
					x := int(random.NextIntN(16))
					z := int(random.NextIntN(16))
					y := plan.SampleY(random, MinY, WorldHeight)
					placeOreEllipsoid(c, random, x, y, z, config.Size, config.DiscardAirExposure, targets)
				}
			}
		}
	}
}

func resolveOreTargets(set *worldgen.FeatureSet, config worldgen.OreFeatureConfig) ([]resolvedOreTarget, bool) {
	targets := make([]resolvedOreTarget, 0, len(config.Targets))
	for _, target := range config.Targets {
		state, ok := nameToStateID(target.State.Name, nil)
		if !ok || target.Target.PredicateType != "minecraft:tag_match" {
			return nil, false
		}
		replaceables := make(map[uint16]bool)
		for _, name := range flattenBlockTag(set, target.Target.Tag, nil) {
			if id, ok := nameToStateID(name, nil); ok {
				replaceables[id] = true
			}
		}
		if len(replaceables) == 0 {
			return nil, false
		}
		targets = append(targets, resolvedOreTarget{state: state, replaceables: replaceables})
	}
	return targets, true
}

func flattenBlockTag(set *worldgen.FeatureSet, tag string, visiting map[string]bool) []string {
	if visiting == nil {
		visiting = make(map[string]bool)
	}
	if visiting[tag] {
		return nil
	}
	visiting[tag] = true
	defer delete(visiting, tag)
	var names []string
	for _, value := range set.BlockTags[tag] {
		if len(value) > 0 && value[0] == '#' {
			names = append(names, flattenBlockTag(set, value[1:], visiting)...)
		} else {
			names = append(names, value)
		}
	}
	return names
}

func placeOreEllipsoid(c *Chunk, random worldgen.RandomSource, originX, originY, originZ, size int, discard float64, targets []resolvedOreTarget) {
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
			x:      x0 + (x1-x0)*float64(t),
			y:      y0 + (y1-y0)*float64(t),
			z:      z0 + (z1-z0)*float64(t),
			radius: radius,
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
			if x < 0 || x >= 16 {
				continue
			}
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
					if z < 0 || z >= 16 {
						continue
					}
					dz := (float64(z) + 0.5 - sphere.z) / sphere.radius
					pos := [3]int{x, y, z}
					if dx*dx+dy*dy+dz*dz >= 1 || visited[pos] {
						continue
					}
					visited[pos] = true
					current := c.GetBlock(x, y, z)
					for _, target := range targets {
						if !target.replaceables[current] || discard > 0 && random.NextFloat() < float32(discard) && exposedToAir(c, x, y, z) {
							continue
						}
						c.SetBlock(x, y, z, target.state)
						break
					}
				}
			}
		}
	}
}

func exposedToAir(c *Chunk, x, y, z int) bool {
	for _, offset := range [][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}} {
		nx, ny, nz := x+offset[0], y+offset[1], z+offset[2]
		if nx < 0 || nx >= 16 || nz < 0 || nz >= 16 {
			continue
		}
		if c.GetBlock(nx, ny, nz) == StateAir {
			return true
		}
	}
	return false
}
