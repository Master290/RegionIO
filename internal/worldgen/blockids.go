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
// snowable blocks and the layers property on snow; unknown blocks return 0
// (air) so a missing entry is visually obvious rather than crashing.
func surfaceBlockID(name string, props map[string]string) uint16 {
	switch name {
	case "minecraft:stone":
		return 1
	case "minecraft:granite":
		return 2
	case "minecraft:diorite":
		return 4
	case "minecraft:andesite":
		return 6
	case "minecraft:grass_block":
		if props["snowy"] == "true" {
			return 8
		}
		return 9
	case "minecraft:dirt":
		return 10
	case "minecraft:coarse_dirt":
		return 11
	case "minecraft:podzol":
		if props["snowy"] == "true" {
			return 12
		}
		return 13
	case "minecraft:bedrock":
		return 85
	case "minecraft:water":
		return 86
	case "minecraft:sand":
		return 118
	case "minecraft:red_sand":
		return 123
	case "minecraft:gravel":
		return 124
	case "minecraft:sandstone":
		return 578
	case "minecraft:red_sandstone":
		return 13247
	case "minecraft:snow_block":
		return 6928
	case "minecraft:snow":
		// snow has a "layers" property 1..8; default layer 1 = 6919.
		return 6919
	case "minecraft:ice":
		return 6927
	case "minecraft:packed_ice":
		return 12914
	case "minecraft:powder_snow":
		return 24689
	case "minecraft:mycelium":
		if props["snowy"] == "true" {
			return 8918
		}
		return 8919
	case "minecraft:terracotta":
		return 12912
	case "minecraft:white_terracotta":
		return 11444
	case "minecraft:orange_terracotta":
		return 11445
	case "minecraft:yellow_terracotta":
		return 11448
	case "minecraft:calcite":
		return 24687
	case "minecraft:tuff":
		return 23452
	case "minecraft:dripstone_block":
		return 27755
	case "minecraft:moss_block":
		return 27862
	case "minecraft:smooth_stone":
		return 13480
	}
	return 0
}
