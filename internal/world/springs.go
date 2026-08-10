package world

import "regionio/internal/worldgen"

const fluidsStage = 8

func placeVanillaSprings(c *Chunk, seed int64, cx, cz int32, biomes *[16][16]string) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		panic("world: loading feature datapack: " + err.Error())
	}
	random, decorationSeed := worldgen.DecorationRandom(seed, int(cx), int(cz))
	seen := make(map[string]bool)
	for bx := 0; bx < 16; bx += 4 {
		for bz := 0; bz < 16; bz += 4 {
			stages := set.Biomes[biomes[bx][bz]].Features
			if len(stages) <= fluidsStage {
				continue
			}
			for featureIndex, name := range stages[fluidsStage] {
				if seen[name] {
					continue
				}
				seen[name] = true
				placed, ok := set.Placed[name]
				if !ok {
					continue
				}
				configured, ok := set.Configured[placed.Feature]
				if !ok || configured.Type != "minecraft:spring_feature" {
					continue
				}
				config, err := set.Spring(placed.Feature)
				if err != nil {
					panic(err)
				}
				plan, err := set.Placement(name)
				if err != nil {
					panic(err)
				}
				random.SetFeatureSeed(decorationSeed, featureIndex, fluidsStage)
				for attempt := 0; attempt < plan.Count.Sample(random); attempt++ {
					x := int(random.NextIntN(16))
					z := int(random.NextIntN(16))
					y := plan.SampleY(random, MinY, WorldHeight)
					placeSpring(c, x, y, z, config)
				}
			}
		}
	}
}

func placeSpring(c *Chunk, x, y, z int, config worldgen.SpringFeatureConfig) {
	valid := make(map[uint16]bool, len(config.ValidBlocks))
	for _, name := range config.ValidBlocks {
		if state, ok := nameToStateID(name, nil); ok {
			valid[state] = true
		}
	}
	state, ok := springStateID(config.State)
	if !ok || !valid[c.GetBlock(x, y+1, z)] {
		return
	}
	if config.RequiresBlockBelow && !valid[c.GetBlock(x, y-1, z)] {
		return
	}
	rock := 0
	holes := 0
	for _, offset := range [][3]int{{-1, 0, 0}, {1, 0, 0}, {0, 0, -1}, {0, 0, 1}, {0, -1, 0}} {
		nx, ny, nz := x+offset[0], y+offset[1], z+offset[2]
		if nx < 0 || nx >= 16 || nz < 0 || nz >= 16 {
			return
		}
		if valid[c.GetBlock(nx, ny, nz)] {
			rock++
		} else if c.GetBlock(nx, ny, nz) == StateAir {
			holes++
		}
	}
	if rock != config.RockCount || holes != config.HoleCount {
		return
	}
	c.SetBlock(x, y, z, state)
}

func springStateID(state worldgen.BlockState) (uint16, bool) {
	props := state.Properties
	if (state.Name == "minecraft:water" || state.Name == "minecraft:lava") && props["falling"] == "true" {
		props = map[string]string{"level": "8"}
	}
	return nameToStateID(state.Name, props)
}
