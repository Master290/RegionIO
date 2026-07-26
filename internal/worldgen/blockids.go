package worldgen

// blockids.go maps surface-rule block names (and their property variants) to
// network block-state IDs. The IDs are captured verbatim from the 26.1.2
// generated blocks.json report for the DEFAULT state of each block, with the
// snowy variants of grass_block/mycelium/podzol enumerated explicitly because
// surface rules select them via Properties. Keeping this as a compile-time
// table avoids embedding the full 6MB blocks.json.
//
// Default-state IDs (from generated/reports/blocks.json, 26.1.2):
//   grass_block snowy:false=9, snowy:true=8
//   mycelium     snowy:false=8919, snowy:true=8918
//   podzol       snowy:false=13, snowy:true=12
//   dirt=10  coarse_dirt=11  stone=1  bedrock=85  water=86(level0)
//   sand=118  red_sand=123  gravel=124  sandstone=578  red_sandstone=13247
//   snow_block=6928  snow(layers:1)=6919  ice=6927  packed_ice=12914  powder_snow=24689
//   terracotta=12912  white_terracotta=11444  orange_terracotta=11445  yellow_terracotta=11448
//   calcite=24687  tuff=23452  dripstone_block=27755  moss_block=27862
//   granite=2  diorite=4  andesite=6  smooth_stone=13480

// surfaceBlockID resolves a surface-rule result_state (Name + optional
// Properties) to its network block-state ID. It handles the snowy property on
// snowable blocks and the layers property on snow.
//
// An unknown name is an error, not a fallback. It used to return 0, which the
// rule application then read as "no block" and skipped — so a name missing from
// this table silently left stone behind. That is exactly how deepslate went
// missing from the entire world: the rule fired, resolved to 0, and was dropped.
func surfaceBlockID(name string, props map[string]string) (uint16, bool) {
	switch name {
	case "minecraft:air":
		return 0, true
	case "minecraft:deepslate":
		// A pillar block; the surface rule always asks for the upright axis.
		return 27924, true
	case "minecraft:mud":
		return 27922, true
	case "minecraft:brown_terracotta":
		return 11456, true
	case "minecraft:red_terracotta":
		return 11458, true
	case "minecraft:light_gray_terracotta":
		return 11452, true
	case "minecraft:stone":
		return 1, true
	case "minecraft:granite":
		return 2, true
	case "minecraft:diorite":
		return 4, true
	case "minecraft:andesite":
		return 6, true
	case "minecraft:grass_block":
		if props["snowy"] == "true" {
			return 8, true
		}
		return 9, true
	case "minecraft:dirt":
		return 10, true
	case "minecraft:coarse_dirt":
		return 11, true
	case "minecraft:podzol":
		if props["snowy"] == "true" {
			return 12, true
		}
		return 13, true
	case "minecraft:bedrock":
		return 85, true
	case "minecraft:water":
		return 86, true
	case "minecraft:sand":
		return 118, true
	case "minecraft:red_sand":
		return 123, true
	case "minecraft:gravel":
		return 124, true
	case "minecraft:sandstone":
		return 578, true
	case "minecraft:red_sandstone":
		return 13247, true
	case "minecraft:snow_block":
		return 6928, true
	case "minecraft:snow":
		// snow has a "layers" property 1..8; default layer 1 = 6919.
		return 6919, true
	case "minecraft:ice":
		return 6927, true
	case "minecraft:packed_ice":
		return 12914, true
	case "minecraft:powder_snow":
		return 24689, true
	case "minecraft:mycelium":
		if props["snowy"] == "true" {
			return 8918, true
		}
		return 8919, true
	case "minecraft:terracotta":
		return 12912, true
	case "minecraft:white_terracotta":
		return 11444, true
	case "minecraft:orange_terracotta":
		return 11445, true
	case "minecraft:yellow_terracotta":
		return 11448, true
	case "minecraft:calcite":
		return 24687, true
	case "minecraft:tuff":
		return 23452, true
	case "minecraft:dripstone_block":
		return 27755, true
	case "minecraft:moss_block":
		return 27862, true
	case "minecraft:smooth_stone":
		return 13480, true
	}
	return 0, false
}
