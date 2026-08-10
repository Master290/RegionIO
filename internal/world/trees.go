package world

import "regionio/internal/worldgen"

const vegetationStage = 9

func placeVanillaTrees(c *Chunk, seed int64, cx, cz int32, biomes *[16][16]string, surfTop *[16][16]int) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		panic(err)
	}
	random, decorationSeed := worldgen.DecorationRandom(seed, int(cx), int(cz))
	seen := make(map[string]bool)
	for bx := 0; bx < 16; bx += 4 {
		for bz := 0; bz < 16; bz += 4 {
			stages := set.Biomes[biomes[bx][bz]].Features
			if len(stages) <= vegetationStage {
				continue
			}
			for featureIndex, name := range stages[vegetationStage] {
				if seen[name] {
					continue
				}
				seen[name] = true
				placed, ok := set.Placed[name]
				if !ok || !treeFeatureType(set.Configured[placed.Feature].Type) {
					continue
				}
				plan, err := set.Placement(name)
				if err != nil {
					continue
				}
				random.SetFeatureSeed(decorationSeed, featureIndex, vegetationStage)
				count := plan.Count.Sample(random)
				for attempt := 0; attempt < count; attempt++ {
					x, z := int(random.NextIntN(16)), int(random.NextIntN(16))
					y := MinY + surfTop[x][z] + 1
					placeTreeReference(c, set, random, placed.Feature, x, y, z)
				}
			}
		}
	}
}

func treeFeatureType(kind string) bool {
	return kind == "minecraft:tree" || kind == "minecraft:random_selector"
}

func placeTreeReference(c *Chunk, set *worldgen.FeatureSet, random worldgen.RandomSource, name string, x, y, z int) bool {
	configured, ok := set.Configured[name]
	if !ok {
		if placed, exists := set.Placed[name]; exists {
			return placeTreeReference(c, set, random, placed.Feature, x, y, z)
		}
		return false
	}
	switch configured.Type {
	case "minecraft:random_selector":
		selector, err := set.RandomSelector(name)
		if err != nil {
			return false
		}
		for _, entry := range selector.Features {
			if random.NextFloat() < entry.Chance {
				return placeTreeReference(c, set, random, entry.Feature.Name, x, y, z)
			}
		}
		return placeTreeReference(c, set, random, selector.Default.Name, x, y, z)
	case "minecraft:tree":
		config, err := set.Tree(name)
		if err != nil || config.TrunkPlacer.Type != "minecraft:straight_trunk_placer" || config.FoliagePlacer.Type != "minecraft:blob_foliage_placer" {
			return false
		}
		return placeStraightBlobTree(c, random, x, y, z, config)
	}
	return false
}

func placeStraightBlobTree(c *Chunk, random worldgen.RandomSource, x, y, z int, config worldgen.TreeFeatureConfig) bool {
	if x < 2 || x >= 14 || z < 2 || z >= 14 || y <= MinY || y+12 >= MinY+WorldHeight {
		return false
	}
	floor := c.GetBlock(x, y-1, z)
	if floor != StateGrass && floor != StateDirt && floor != StateCoarseDirt && floor != StatePodzol {
		return false
	}
	height := config.TrunkPlacer.BaseHeight
	if config.TrunkPlacer.HeightRandA > 0 {
		height += int(random.NextIntN(int32(config.TrunkPlacer.HeightRandA + 1)))
	}
	if config.TrunkPlacer.HeightRandB > 0 {
		height += int(random.NextIntN(int32(config.TrunkPlacer.HeightRandB + 1)))
	}
	for dy := 0; dy <= height+config.FoliagePlacer.Height; dy++ {
		if c.GetBlock(x, y+dy, z) != StateAir {
			return false
		}
	}
	logState, okLog := nameToStateID(config.TrunkProvider.State.Name, config.TrunkProvider.State.Properties)
	leafState, okLeaf := nameToStateID(config.FoliageProvider.State.Name, config.FoliageProvider.State.Properties)
	if !okLog || !okLeaf {
		return false
	}
	c.SetBlock(x, y-1, z, StateDirt)
	for dy := 0; dy < height; dy++ {
		c.SetBlock(x, y+dy, z, logState)
	}
	centerY := y + height - 1 + config.FoliagePlacer.Offset
	for layer := 0; layer < config.FoliagePlacer.Height; layer++ {
		radius := config.FoliagePlacer.Radius
		if layer == config.FoliagePlacer.Height-1 {
			radius--
		}
		ly := centerY - layer
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				if abs(dx) == radius && abs(dz) == radius && random.NextIntN(2) == 0 {
					continue
				}
				if c.GetBlock(x+dx, ly, z+dz) == StateAir {
					c.SetBlock(x+dx, ly, z+dz, leafState)
				}
			}
		}
	}
	return true
}
