package server

import (
	"errors"
	"math"
	"math/rand"
	"strings"

	"regionio/internal/world"
)

// --- 1. Biome Mob Spawn Tables (26.1.2 Overworld, Nether & End Biomes) ---

// BiomeSpawnEntry2612 represents `MobSpawnSettings.SpawnerData` in Minecraft 26.1.2.
type BiomeSpawnEntry2612 struct {
	TypeName string
	Weight   int
	MinCount int
	MaxCount int
}

func commonOverworldMonsters2612(zombieWeight, zombieVillagerWeight, zombieHorseWeight, skeletonWeight int) []BiomeSpawnEntry2612 {
	out := []BiomeSpawnEntry2612{
		{TypeName: MobTypeSpider, Weight: 100, MinCount: 4, MaxCount: 4},
		{TypeName: MobTypeZombie, Weight: zombieWeight, MinCount: 4, MaxCount: 4},
		{TypeName: MobTypeZombieVillager, Weight: zombieVillagerWeight, MinCount: 1, MaxCount: 1},
	}
	if zombieHorseWeight > 0 {
		out = append(out, BiomeSpawnEntry2612{TypeName: MobTypeZombieHorse, Weight: zombieHorseWeight, MinCount: 1, MaxCount: 1})
	}
	if skeletonWeight > 0 {
		out = append(out, BiomeSpawnEntry2612{TypeName: MobTypeSkeleton, Weight: skeletonWeight, MinCount: 4, MaxCount: 4})
	}
	out = append(out,
		BiomeSpawnEntry2612{TypeName: MobTypeCreeper, Weight: 100, MinCount: 4, MaxCount: 4},
		BiomeSpawnEntry2612{TypeName: MobTypeSlime, Weight: 100, MinCount: 4, MaxCount: 4},
		BiomeSpawnEntry2612{TypeName: MobTypeEnderman, Weight: 10, MinCount: 1, MaxCount: 4},
		BiomeSpawnEntry2612{TypeName: MobTypeWitch, Weight: 5, MinCount: 1, MaxCount: 1},
	)
	return out
}

func defaultFarmAnimals2612() []BiomeSpawnEntry2612 {
	return []BiomeSpawnEntry2612{
		{TypeName: MobTypeSheep, Weight: 12, MinCount: 4, MaxCount: 4},
		{TypeName: MobTypePig, Weight: 10, MinCount: 4, MaxCount: 4},
		{TypeName: MobTypeChicken, Weight: 10, MinCount: 4, MaxCount: 4},
		{TypeName: MobTypeCow, Weight: 8, MinCount: 4, MaxCount: 4},
	}
}

// BiomeSpawnTableForCategory2612 returns the vanilla 26.1.2 `MobSpawnSettings`
// table for `(biomeName, category)` verified against `BiomeDefaultFeatures.class`,
// `OverworldBiomes.class`, `NetherBiomes.class`, and `EndBiomes.class` in `26.1.2.jar`.
func BiomeSpawnTableForCategory2612(biomeName string, cat MobCategory2612) []BiomeSpawnEntry2612 {
	norm := canonicalResourceName(biomeName)

	// Cave ambient & underground water creature defaults for Overworld biomes.
	if cat == MobCategoryAmbient {
		switch norm {
		case "minecraft:nether_wastes", "minecraft:soul_sand_valley", "minecraft:crimson_forest",
			"minecraft:warped_forest", "minecraft:basalt_deltas", "minecraft:the_end", "minecraft:deep_dark":
			return nil
		default:
			return []BiomeSpawnEntry2612{{TypeName: MobTypeBat, Weight: 10, MinCount: 8, MaxCount: 8}}
		}
	}
	if cat == MobCategoryUndergroundWaterCreature {
		switch norm {
		case "minecraft:nether_wastes", "minecraft:soul_sand_valley", "minecraft:crimson_forest",
			"minecraft:warped_forest", "minecraft:basalt_deltas", "minecraft:the_end", "minecraft:deep_dark":
			return nil
		default:
			return []BiomeSpawnEntry2612{{TypeName: MobTypeGlowSquid, Weight: 10, MinCount: 4, MaxCount: 6}}
		}
	}
	if cat == MobCategoryAxolotls {
		if norm == "minecraft:lush_caves" {
			return []BiomeSpawnEntry2612{{TypeName: MobTypeAxolotl, Weight: 10, MinCount: 4, MaxCount: 6}}
		}
		return nil
	}

	switch norm {
	case "minecraft:mushroom_fields":
		if cat == MobCategoryCreature {
			return []BiomeSpawnEntry2612{{TypeName: MobTypeMooshroom, Weight: 8, MinCount: 4, MaxCount: 8}}
		}
		return nil

	case "minecraft:deep_dark", "minecraft:the_void":
		return nil

	case "minecraft:pale_garden":
		if cat == MobCategoryMonster {
			return commonOverworldMonsters2612(95, 5, 0, 100)
		}
		return nil

	case "minecraft:desert":
		switch cat {
		case MobCategoryCreature:
			return []BiomeSpawnEntry2612{
				{TypeName: MobTypeRabbit, Weight: 12, MinCount: 2, MaxCount: 3},
				{TypeName: MobTypeCamel, Weight: 1, MinCount: 1, MaxCount: 1},
			}
		case MobCategoryMonster:
			out := commonOverworldMonsters2612(19, 1, 0, 50)
			out = append(out,
				BiomeSpawnEntry2612{TypeName: MobTypeHusk, Weight: 80, MinCount: 4, MaxCount: 4},
				BiomeSpawnEntry2612{TypeName: MobTypeParched, Weight: 50, MinCount: 4, MaxCount: 4},
			)
			return out
		}
		return nil

	case "minecraft:plains", "minecraft:sunflower_plains":
		switch cat {
		case MobCategoryCreature:
			out := defaultFarmAnimals2612()
			out = append(out,
				BiomeSpawnEntry2612{TypeName: MobTypeHorse, Weight: 5, MinCount: 2, MaxCount: 6},
				BiomeSpawnEntry2612{TypeName: MobTypeDonkey, Weight: 1, MinCount: 1, MaxCount: 3},
			)
			return out
		case MobCategoryMonster:
			return commonOverworldMonsters2612(90, 5, 5, 100)
		}
		return nil

	case "minecraft:savanna", "minecraft:savanna_plateau":
		switch cat {
		case MobCategoryCreature:
			out := defaultFarmAnimals2612()
			out = append(out,
				BiomeSpawnEntry2612{TypeName: MobTypeHorse, Weight: 1, MinCount: 2, MaxCount: 6},
				BiomeSpawnEntry2612{TypeName: MobTypeDonkey, Weight: 1, MinCount: 1, MaxCount: 1},
				BiomeSpawnEntry2612{TypeName: MobTypeArmadillo, Weight: 10, MinCount: 2, MaxCount: 3},
			)
			return out
		case MobCategoryMonster:
			return commonOverworldMonsters2612(90, 5, 5, 100)
		}
		return nil

	case "minecraft:snowy_plains", "minecraft:ice_spikes":
		switch cat {
		case MobCategoryCreature:
			return []BiomeSpawnEntry2612{
				{TypeName: MobTypeRabbit, Weight: 10, MinCount: 2, MaxCount: 3},
				{TypeName: MobTypePolarBear, Weight: 1, MinCount: 1, MaxCount: 2},
			}
		case MobCategoryMonster:
			zhWeight := 5
			zWeight := 90
			if norm == "minecraft:ice_spikes" {
				zhWeight = 0
				zWeight = 95
			}
			out := commonOverworldMonsters2612(zWeight, 5, zhWeight, 20)
			out = append(out, BiomeSpawnEntry2612{TypeName: MobTypeStray, Weight: 80, MinCount: 4, MaxCount: 4})
			return out
		}
		return nil

	case "minecraft:swamp", "minecraft:mangrove_swamp":
		switch cat {
		case MobCategoryCreature:
			var out []BiomeSpawnEntry2612
			if norm == "minecraft:swamp" {
				out = defaultFarmAnimals2612()
			}
			out = append(out, BiomeSpawnEntry2612{TypeName: MobTypeFrog, Weight: 10, MinCount: 2, MaxCount: 5})
			return out
		case MobCategoryMonster:
			skelWeight := 70
			if norm == "minecraft:mangrove_swamp" {
				skelWeight = 50
			}
			out := commonOverworldMonsters2612(95, 5, 0, skelWeight)
			out = append(out,
				BiomeSpawnEntry2612{TypeName: MobTypeBogged, Weight: 30, MinCount: 4, MaxCount: 4},
				BiomeSpawnEntry2612{TypeName: MobTypeSlime, Weight: 1, MinCount: 1, MaxCount: 1},
			)
			return out
		case MobCategoryWaterAmbient:
			if norm == "minecraft:mangrove_swamp" {
				return []BiomeSpawnEntry2612{{TypeName: MobTypeTropicalFish, Weight: 25, MinCount: 8, MaxCount: 8}}
			}
		}
		return nil

	case "minecraft:lush_caves":
		switch cat {
		case MobCategoryWaterAmbient:
			return []BiomeSpawnEntry2612{{TypeName: MobTypeTropicalFish, Weight: 25, MinCount: 8, MaxCount: 8}}
		case MobCategoryMonster:
			return commonOverworldMonsters2612(95, 5, 0, 100)
		}
		return nil

	case "minecraft:ocean", "minecraft:deep_ocean", "minecraft:cold_ocean", "minecraft:deep_cold_ocean":
		switch cat {
		case MobCategoryWaterCreature:
			return []BiomeSpawnEntry2612{
				{TypeName: MobTypeSquid, Weight: 3, MinCount: 1, MaxCount: 4},
				{TypeName: MobTypeDolphin, Weight: 1, MinCount: 1, MaxCount: 2},
				{TypeName: MobTypeNautilus, Weight: 2, MinCount: 1, MaxCount: 3},
			}
		case MobCategoryWaterAmbient:
			if strings.Contains(norm, "cold") {
				return []BiomeSpawnEntry2612{
					{TypeName: MobTypeCod, Weight: 15, MinCount: 3, MaxCount: 6},
					{TypeName: MobTypeSalmon, Weight: 15, MinCount: 1, MaxCount: 5},
				}
			}
			return []BiomeSpawnEntry2612{{TypeName: MobTypeCod, Weight: 10, MinCount: 3, MaxCount: 6}}
		case MobCategoryMonster:
			out := commonOverworldMonsters2612(95, 5, 0, 100)
			out = append(out, BiomeSpawnEntry2612{TypeName: MobTypeDrowned, Weight: 5, MinCount: 1, MaxCount: 1})
			return out
		}
		return nil

	case "minecraft:warm_ocean", "minecraft:lukewarm_ocean", "minecraft:deep_lukewarm_ocean":
		switch cat {
		case MobCategoryWaterCreature:
			return []BiomeSpawnEntry2612{
				{TypeName: MobTypeSquid, Weight: 10, MinCount: 4, MaxCount: 4},
				{TypeName: MobTypeDolphin, Weight: 2, MinCount: 1, MaxCount: 2},
				{TypeName: MobTypeNautilus, Weight: 2, MinCount: 1, MaxCount: 3},
			}
		case MobCategoryWaterAmbient:
			out := []BiomeSpawnEntry2612{{TypeName: MobTypeTropicalFish, Weight: 25, MinCount: 8, MaxCount: 8}}
			if norm == "minecraft:warm_ocean" {
				out = append(out, BiomeSpawnEntry2612{TypeName: MobTypePufferfish, Weight: 15, MinCount: 1, MaxCount: 3})
			} else {
				out = append(out, BiomeSpawnEntry2612{TypeName: MobTypeCod, Weight: 15, MinCount: 3, MaxCount: 6})
			}
			return out
		case MobCategoryMonster:
			out := commonOverworldMonsters2612(95, 5, 0, 100)
			out = append(out, BiomeSpawnEntry2612{TypeName: MobTypeDrowned, Weight: 5, MinCount: 1, MaxCount: 1})
			return out
		}
		return nil

	case "minecraft:nether_wastes":
		switch cat {
		case MobCategoryMonster:
			return []BiomeSpawnEntry2612{
				{TypeName: MobTypeGhast, Weight: 50, MinCount: 4, MaxCount: 4},
				{TypeName: MobTypeZombifiedPiglin, Weight: 100, MinCount: 4, MaxCount: 4},
				{TypeName: MobTypeMagmaCube, Weight: 2, MinCount: 4, MaxCount: 4},
				{TypeName: MobTypeEnderman, Weight: 1, MinCount: 4, MaxCount: 4},
				{TypeName: MobTypePiglin, Weight: 15, MinCount: 4, MaxCount: 4},
			}
		case MobCategoryCreature:
			return []BiomeSpawnEntry2612{{TypeName: MobTypeStrider, Weight: 60, MinCount: 1, MaxCount: 2}}
		}
		return nil

	case "minecraft:soul_sand_valley":
		switch cat {
		case MobCategoryMonster:
			return []BiomeSpawnEntry2612{
				{TypeName: MobTypeSkeleton, Weight: 20, MinCount: 5, MaxCount: 5},
				{TypeName: MobTypeGhast, Weight: 50, MinCount: 4, MaxCount: 4},
				{TypeName: MobTypeEnderman, Weight: 1, MinCount: 4, MaxCount: 4},
			}
		case MobCategoryCreature:
			return []BiomeSpawnEntry2612{{TypeName: MobTypeStrider, Weight: 60, MinCount: 1, MaxCount: 2}}
		}
		return nil

	case "minecraft:crimson_forest":
		switch cat {
		case MobCategoryMonster:
			return []BiomeSpawnEntry2612{
				{TypeName: MobTypeZombifiedPiglin, Weight: 1, MinCount: 2, MaxCount: 4},
				{TypeName: MobTypeHoglin, Weight: 9, MinCount: 3, MaxCount: 4},
				{TypeName: MobTypePiglin, Weight: 5, MinCount: 3, MaxCount: 4},
			}
		case MobCategoryCreature:
			return []BiomeSpawnEntry2612{{TypeName: MobTypeStrider, Weight: 60, MinCount: 1, MaxCount: 2}}
		}
		return nil

	case "minecraft:warped_forest":
		switch cat {
		case MobCategoryMonster:
			return []BiomeSpawnEntry2612{{TypeName: MobTypeEnderman, Weight: 1, MinCount: 4, MaxCount: 4}}
		case MobCategoryCreature:
			return []BiomeSpawnEntry2612{{TypeName: MobTypeStrider, Weight: 60, MinCount: 1, MaxCount: 2}}
		}
		return nil

	case "minecraft:basalt_deltas":
		switch cat {
		case MobCategoryMonster:
			return []BiomeSpawnEntry2612{
				{TypeName: MobTypeGhast, Weight: 40, MinCount: 1, MaxCount: 1},
				{TypeName: MobTypeMagmaCube, Weight: 100, MinCount: 2, MaxCount: 5},
			}
		case MobCategoryCreature:
			return []BiomeSpawnEntry2612{{TypeName: MobTypeStrider, Weight: 60, MinCount: 1, MaxCount: 2}}
		}
		return nil

	case "minecraft:the_end", "minecraft:end_highlands", "minecraft:end_midlands", "minecraft:small_end_islands", "minecraft:end_barrens":
		if cat == MobCategoryMonster {
			return []BiomeSpawnEntry2612{{TypeName: MobTypeEnderman, Weight: 10, MinCount: 4, MaxCount: 4}}
		}
		return nil

	default:
		switch cat {
		case MobCategoryCreature:
			return defaultFarmAnimals2612()
		case MobCategoryMonster:
			return commonOverworldMonsters2612(95, 5, 0, 100)
		}
		return nil
	}
}

// SelectWeightedBiomeSpawn2612 selects one entry from `entries` weighted by `Weight`
// and rolls a pack size in `[MinCount, MaxCount]`.
func SelectWeightedBiomeSpawn2612(entries []BiomeSpawnEntry2612, rng *rand.Rand) (entry BiomeSpawnEntry2612, packSize int, ok bool) {
	totalWeight := 0
	for _, e := range entries {
		if e.Weight > 0 {
			totalWeight += e.Weight
		}
	}
	if totalWeight <= 0 {
		return BiomeSpawnEntry2612{}, 0, false
	}
	var roll int
	if rng != nil {
		roll = rng.Intn(totalWeight)
	} else {
		roll = rand.Intn(totalWeight)
	}
	for _, e := range entries {
		if e.Weight <= 0 {
			continue
		}
		roll -= e.Weight
		if roll < 0 {
			entry = e
			break
		}
	}
	minC := entry.MinCount
	maxC := entry.MaxCount
	if minC < 1 {
		minC = 1
	}
	if maxC < minC {
		maxC = minC
	}
	if maxC == minC {
		packSize = minC
	} else if rng != nil {
		packSize = minC + rng.Intn(maxC-minC+1)
	} else {
		packSize = minC + rand.Intn(maxC-minC+1)
	}
	return entry, packSize, true
}

// --- 2. Spawn Potential / Energy Budget (PotentialCalculator & MobSpawnCost) ---

// MobSpawnCost2612 represents `MobSpawnSettings.MobSpawnCost(energyBudget, charge)`
// in `NetherBiomes.class` from `26.1.2.jar`.
type MobSpawnCost2612 struct {
	EnergyBudget float64
	Charge       float64
}

// BiomeMobSpawnCost2612 returns the `(energyBudget, charge)` spawn cost for
// `(biomeName, mobType)` in `26.1.2`:
//   - `minecraft:soul_sand_valley`: `skeleton`, `ghast`, `enderman`, `strider`
//     have `charge = 0.7`, `energyBudget = 0.15`.
//   - `minecraft:warped_forest`: `enderman` has `charge = 1.0`, `energyBudget = 0.12`.
func BiomeMobSpawnCost2612(biomeName, mobType string) (MobSpawnCost2612, bool) {
	normBiome := canonicalResourceName(biomeName)
	normMob := canonicalResourceName(mobType)
	switch normBiome {
	case "minecraft:soul_sand_valley":
		switch normMob {
		case MobTypeSkeleton, MobTypeGhast, MobTypeEnderman, MobTypeStrider:
			return MobSpawnCost2612{EnergyBudget: 0.15, Charge: 0.7}, true
		}
	case "minecraft:warped_forest":
		if normMob == MobTypeEnderman {
			return MobSpawnCost2612{EnergyBudget: 0.12, Charge: 1.0}, true
		}
	}
	return MobSpawnCost2612{}, false
}

type pointCharge2612 struct {
	x, y, z int
	charge  float64
}

// PotentialCalculator2612 implements `net.minecraft.world.level.PotentialCalculator`
// from `26.1.2.jar`.
type PotentialCalculator2612 struct {
	charges []pointCharge2612
}

// AddCharge adds a point charge at `(x, y, z)` (`PotentialCalculator.addCharge`).
func (pc *PotentialCalculator2612) AddCharge(x, y, z int, charge float64) {
	if pc == nil || charge == 0 {
		return
	}
	pc.charges = append(pc.charges, pointCharge2612{x: x, y: y, z: z, charge: charge})
}

// GetPotentialEnergyChange computes `PotentialCalculator.getPotentialEnergyChange(pos, charge)`:
// `charge * sum_i (charge_i / sqrt(distSq(pos, pos_i)))`.
func (pc *PotentialCalculator2612) GetPotentialEnergyChange(x, y, z int, charge float64) float64 {
	if pc == nil || charge == 0 || len(pc.charges) == 0 {
		return 0
	}
	sum := 0.0
	for _, c := range pc.charges {
		dx := float64(x - c.x)
		dy := float64(y - c.y)
		dz := float64(z - c.z)
		distSq := dx*dx + dy*dy + dz*dz
		if distSq == 0 {
			return math.Inf(1)
		}
		sum += c.charge / math.Sqrt(distSq)
	}
	return sum * charge
}

// CanSpawnWithinEnergyBudget reports whether a candidate mob with `cost` at
// `(x, y, z)` satisfies `getPotentialEnergyChange(pos, cost.Charge) <= cost.EnergyBudget`.
func (pc *PotentialCalculator2612) CanSpawnWithinEnergyBudget(x, y, z int, cost MobSpawnCost2612) bool {
	if cost.Charge == 0 {
		return true
	}
	return pc.GetPotentialEnergyChange(x, y, z, cost.Charge) <= cost.EnergyBudget
}

// --- 3. Structure Spawn Overrides (Fortress, Monument, Swamp Hut, Outpost, Ocean Ruin) ---

// StructureSpawnsForCategory2612 returns the structure-specific `BiomeSpawnEntry2612`
// overrides for `(structureName, category)` in vanilla 26.1.2.
func StructureSpawnsForCategory2612(structureName string, cat MobCategory2612) []BiomeSpawnEntry2612 {
	norm := canonicalResourceName(structureName)
	switch norm {
	case "minecraft:fortress", "minecraft:nether_fortress":
		if cat == MobCategoryMonster {
			return []BiomeSpawnEntry2612{
				{TypeName: MobTypeBlaze, Weight: 10, MinCount: 2, MaxCount: 3},
				{TypeName: MobTypeZombifiedPiglin, Weight: 5, MinCount: 4, MaxCount: 4},
				{TypeName: MobTypeWitherSkeleton, Weight: 8, MinCount: 5, MaxCount: 5},
				{TypeName: MobTypeSkeleton, Weight: 2, MinCount: 5, MaxCount: 5},
				{TypeName: MobTypeMagmaCube, Weight: 3, MinCount: 4, MaxCount: 4},
			}
		}
	case "minecraft:monument", "minecraft:ocean_monument":
		if cat == MobCategoryMonster {
			return []BiomeSpawnEntry2612{
				{TypeName: MobTypeGuardian, Weight: 1, MinCount: 2, MaxCount: 4},
			}
		}
	case "minecraft:swamp_hut":
		if cat == MobCategoryMonster {
			return []BiomeSpawnEntry2612{
				{TypeName: MobTypeWitch, Weight: 1, MinCount: 1, MaxCount: 1},
			}
		}
		if cat == MobCategoryCreature {
			return []BiomeSpawnEntry2612{
				{TypeName: MobTypeCat, Weight: 1, MinCount: 1, MaxCount: 1},
			}
		}
	case "minecraft:pillager_outpost":
		if cat == MobCategoryMonster {
			return []BiomeSpawnEntry2612{
				{TypeName: MobTypePillager, Weight: 1, MinCount: 1, MaxCount: 1},
			}
		}
	case "minecraft:ocean_ruin_cold", "minecraft:ocean_ruin_warm":
		if cat == MobCategoryMonster {
			return []BiomeSpawnEntry2612{
				{TypeName: MobTypeDrowned, Weight: 1, MinCount: 1, MaxCount: 2},
			}
		}
	}
	return nil
}

// --- 4. Moon Phase & Spawn Placement Rules ---

var vanillaMoonBrightnessByPhase = [8]float32{
	1.0,  // 0: Full moon
	0.75, // 1: Waning gibbous
	0.5,  // 2: Third quarter
	0.25, // 3: Waning crescent
	0.0,  // 4: New moon
	0.25, // 5: Waxing crescent
	0.5,  // 6: First quarter
	0.75, // 7: Waxing gibbous
}

// MoonPhaseIndex2612 returns the moon phase index `0..7` for `dayTime`
// (`int(dayTime / 24000 % 8 + 8) % 8`).
func MoonPhaseIndex2612(dayTime int64) int {
	phase := int((dayTime / 24000) % 8)
	if phase < 0 {
		phase += 8
	}
	return phase
}

// MoonBrightness2612 returns `DimensionType.MOON_BRIGHTNESS_PER_PHASE[phase]`
// (`1.0` on full moon, `0.0` on new moon).
func MoonBrightness2612(dayTime int64) float32 {
	return vanillaMoonBrightnessByPhase[MoonPhaseIndex2612(dayTime)]
}

// CanSlimeSpawnAt2612 implements `Slime.checkSlimeSpawnRules` in vanilla 26.1.2:
//   - Surface swamp (`minecraft:swamp` or `minecraft:mangrove_swamp`) at `50 < y < 70`:
//     requires `internalLight <= 7`, `rng.Float32() < 0.5`, and `rng.Float32() < MoonBrightness2612(dayTime)`.
//   - Underground slime chunk (`y < 40` and `IsSlimeChunk2612(worldSeed, cx, cz)`):
//     `rng.Intn(10) == 0`.
func CanSlimeSpawnAt2612(biomeName string, worldSeed, dayTime int64, x, y, z int, internalLight byte, rng *rand.Rand) bool {
	norm := canonicalResourceName(biomeName)
	if (norm == "minecraft:swamp" || norm == "minecraft:mangrove_swamp") && y > 50 && y < 70 {
		if internalLight > 7 {
			return false
		}
		mb := MoonBrightness2612(dayTime)
		if mb <= 0 {
			return false
		}
		var r1, r2 float32
		if rng != nil {
			r1 = rng.Float32()
			r2 = rng.Float32()
		} else {
			r1 = rand.Float32()
			r2 = rand.Float32()
		}
		if r1 < 0.5 && r2 < mb {
			return true
		}
	}
	if y < 40 {
		cx := x >> 4
		cz := z >> 4
		if IsSlimeChunk2612(worldSeed, cx, cz) {
			if rng != nil {
				return rng.Intn(10) == 0
			}
			return rand.Intn(10) == 0
		}
	}
	return false
}

// CanMonsterSpawnByLight2612 checks `Monster.isDarkEnoughToSpawn` in 26.1.2:
//   - Overworld monsters require `blockLight == 0` and `skyEffective <= 7`.
//   - Nether monsters (`blaze`, `piglin`, `wither_skeleton`, `zombified_piglin`)
//     allow `blockLight <= 11`.
func CanMonsterSpawnByLight2612(dimension, mobType string, skyEffective, blockLight byte) bool {
	normDim := world.NormalizeDimension(dimension)
	normMob := canonicalResourceName(mobType)
	if normDim == world.DimensionNether {
		switch normMob {
		case MobTypeBlaze, MobTypePiglin, MobTypeWitherSkeleton, MobTypeZombifiedPiglin, MobTypeHoglin:
			return blockLight <= 11
		case MobTypeGhast, MobTypeMagmaCube:
			return true
		}
	}
	return blockLight == 0 && skyEffective <= HostileMaxSkyLight
}

// --- 5. Mob Spawn Equipment, Enchantments by Difficulty & Jockey Variants ---

// ClampedSpecialDifficultyMultiplier2612 computes `DifficultyInstance.getSpecialMultiplier()`
// (`clamp((effectiveDifficulty - 2.0) / 2.0, 0.0, 1.0)`).
func ClampedSpecialDifficultyMultiplier2612(effectiveDifficulty float32) float32 {
	if effectiveDifficulty < 2.0 {
		return 0.0
	}
	if effectiveDifficulty > 4.0 {
		return 1.0
	}
	return (effectiveDifficulty - 2.0) / 2.0
}

// GeneratedMobSpawnEquipment2612 holds the equipment and enchantment state
// rolled by `PopulateNaturalSpawnEquipment2612`.
type GeneratedMobSpawnEquipment2612 struct {
	MainHand          string
	OffHand           string
	Head              string
	Chest             string
	Legs              string
	Feet              string
	MainHandEnchanted bool
	ArmorEnchanted    [4]bool // Head, Chest, Legs, Feet
	EnchantmentLevel  int
}

// EquipmentCount returns the number of non-empty equipment slots (used for XP bonus).
func (eq GeneratedMobSpawnEquipment2612) EquipmentCount() int {
	n := 0
	for _, s := range [6]string{eq.MainHand, eq.OffHand, eq.Head, eq.Chest, eq.Legs, eq.Feet} {
		if s != "" {
			n++
		}
	}
	return n
}

var naturalArmorMaterialPrefixes2612 = [5]string{
	"minecraft:leather_",
	"minecraft:golden_",
	"minecraft:chainmail_",
	"minecraft:iron_",
	"minecraft:diamond_",
}

// PopulateNaturalSpawnEquipment2612 implements `Mob.populateDefaultEquipmentSlots`
// and `Mob.populateDefaultEquipmentEnchantments` in vanilla 26.1.2.
func PopulateNaturalSpawnEquipment2612(mobType string, difficulty Difficulty, specialMult float32, rng *rand.Rand) GeneratedMobSpawnEquipment2612 {
	if specialMult < 0 {
		specialMult = 0
	}
	if specialMult > 1 {
		specialMult = 1
	}
	f32 := func() float32 {
		if rng != nil {
			return rng.Float32()
		}
		return rand.Float32()
	}

	var eq GeneratedMobSpawnEquipment2612
	eq.EnchantmentLevel = 5 + int(specialMult*18.0)

	norm := canonicalResourceName(mobType)
	canWearRandomArmor := false

	switch norm {
	case MobTypeSkeleton, MobTypeStray, MobTypeBogged, MobTypeParched:
		eq.MainHand = "minecraft:bow"
		canWearRandomArmor = true
	case MobTypeWitherSkeleton:
		eq.MainHand = "minecraft:stone_sword"
	case MobTypePiglin:
		if f32() < 0.5 {
			eq.MainHand = "minecraft:crossbow"
		} else {
			eq.MainHand = "minecraft:golden_sword"
		}
		canWearRandomArmor = true
	case MobTypePiglinBrute:
		eq.MainHand = "minecraft:golden_axe"
	case MobTypeZombifiedPiglin:
		eq.MainHand = "minecraft:golden_sword"
	case MobTypeVindicator:
		eq.MainHand = "minecraft:iron_axe"
	case MobTypePillager:
		eq.MainHand = "minecraft:crossbow"
	case MobTypeDrowned:
		roll := f32()
		if roll < 0.0625 {
			eq.MainHand = "minecraft:trident"
		} else if roll < 0.10 {
			eq.MainHand = "minecraft:fishing_rod"
		}
		if f32() < 0.03 {
			eq.OffHand = "minecraft:nautilus_shell"
		}
	case MobTypeZombie, MobTypeZombieVillager, MobTypeHusk:
		canWearRandomArmor = true
		weaponChance := float32(0.01)
		if difficulty == DifficultyHard {
			weaponChance = 0.05
		}
		if f32() < weaponChance {
			if f32() < 0.3333 {
				eq.MainHand = "minecraft:iron_sword"
			} else {
				eq.MainHand = "minecraft:iron_shovel"
			}
		}
	}

	if canWearRandomArmor && f32() < 0.15*specialMult {
		tier := 0
		for tier < 4 && f32() < 0.095 {
			tier++
		}
		prefix := naturalArmorMaterialPrefixes2612[tier]
		stopChance := float32(0.25)
		if difficulty == DifficultyHard {
			stopChance = 0.10
		}
		eq.Head = prefix + "helmet"
		if f32() >= stopChance {
			eq.Chest = prefix + "chestplate"
			if f32() >= stopChance {
				eq.Legs = prefix + "leggings"
				if f32() >= stopChance {
					eq.Feet = prefix + "boots"
				}
			}
		}
	}

	if eq.MainHand != "" && f32() < 0.25*specialMult {
		eq.MainHandEnchanted = true
	}
	slots := [4]string{eq.Head, eq.Chest, eq.Legs, eq.Feet}
	for i, s := range slots {
		if s != "" && f32() < 0.5*specialMult {
			eq.ArmorEnchanted[i] = true
		}
	}
	return eq
}

// NaturalJockeySpawnResult2612 describes any baby/jockey rider or mount rolled
// during natural mob spawning in 26.1.2.
type NaturalJockeySpawnResult2612 struct {
	IsBaby         bool
	MountType      string
	RiderType      string
	CoRiderType    string
	RiderMainHand  string
	CoRiderWeapon  string
}

// RollNaturalJockeyVariant2612 evaluates natural spawn baby/jockey probabilities
// in vanilla 26.1.2:
//   - `minecraft:zombie_horse` (natural spawn in `plains`/`savanna`/`snowy_plains`):
//     always ridden by a `minecraft:zombie` wielding `minecraft:iron_spear` (`ZombieHorse.finalizeSpawn`).
//   - `minecraft:husk` on the surface in `minecraft:desert`: `10%` chance to spawn
//     a `CamelHusk` mount ridden by a `Husk` (`minecraft:iron_spear`) and a `Parched` (`minecraft:bow`).
//   - `minecraft:zombie` / `minecraft:husk` / `minecraft:drowned` / `minecraft:zombified_piglin`:
//     `5%` chance to be a baby, and `5%` of babies spawn as a `minecraft:chicken` jockey.
//   - `minecraft:spider`: `1%` chance to spawn as a Spider Jockey ridden by a
//     `minecraft:skeleton` (`minecraft:stray` in snowy biomes, `minecraft:wither_skeleton` in the Nether).
func RollNaturalJockeyVariant2612(mobType, biomeName, dimension string, canSeeSky bool, rng *rand.Rand) NaturalJockeySpawnResult2612 {
	f32 := func() float32 {
		if rng != nil {
			return rng.Float32()
		}
		return rand.Float32()
	}
	norm := canonicalResourceName(mobType)
	normBiome := canonicalResourceName(biomeName)
	normDim := world.NormalizeDimension(dimension)

	if norm == MobTypeZombieHorse {
		return NaturalJockeySpawnResult2612{
			MountType:     MobTypeZombieHorse,
			RiderType:     MobTypeZombie,
			RiderMainHand: IronSpearItemID,
		}
	}

	if norm == MobTypeHusk && canSeeSky && f32() < HuskCamelJockeySpawnChance {
		return NaturalJockeySpawnResult2612{
			MountType:     MobTypeCamelHusk,
			RiderType:     MobTypeHusk,
			CoRiderType:   MobTypeParched,
			RiderMainHand: IronSpearItemID,
			CoRiderWeapon: "minecraft:bow",
		}
	}

	switch norm {
	case MobTypeZombie, MobTypeZombieVillager, MobTypeHusk, MobTypeDrowned, MobTypeZombifiedPiglin:
		if f32() < 0.05 {
			res := NaturalJockeySpawnResult2612{IsBaby: true}
			if f32() < 0.05 {
				res.MountType = MobTypeChicken
				res.RiderType = norm
			}
			return res
		}
	case MobTypeSpider:
		if f32() < 0.01 {
			rider := MobTypeSkeleton
			if normDim == world.DimensionNether {
				rider = MobTypeWitherSkeleton
			} else if strings.Contains(normBiome, "snow") || strings.Contains(normBiome, "ice") || strings.Contains(normBiome, "frozen") {
				rider = MobTypeStray
			}
			weapon := "minecraft:bow"
			if rider == MobTypeWitherSkeleton {
				weapon = "minecraft:stone_sword"
			}
			return NaturalJockeySpawnResult2612{
				MountType:     MobTypeSpider,
				RiderType:     rider,
				RiderMainHand: weapon,
			}
		}
	}
	return NaturalJockeySpawnResult2612{}
}

// SpawnNaturalZombieHorseJockey2612 spawns a natural `minecraft:zombie_horse`
// at `(x, y, z)` in `dimension` together with its `minecraft:zombie` rider
// wielding `minecraft:iron_spear` (`ZombieHorse.finalizeSpawn` in `26.1.2.jar`).
func (s *Server) SpawnNaturalZombieHorseJockey2612(dimension string, x, y, z float64) (horseID, zombieID int32, err error) {
	if s == nil || s.entities == nil {
		return 0, 0, errors.New("server: nil server or entity tracker")
	}
	normDim := world.NormalizeDimension(dimension)
	horseTypeID := ResolveEntityTypeID(MobTypeZombieHorse)
	if horseTypeID < 0 {
		horseTypeID = EntityTypeZombieHorseID
	}
	horseID = s.entities.Add(&world.Entity{
		TypeID:    horseTypeID,
		TypeName:  MobTypeZombieHorse,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})
	s.SetMobHealth(horseID, ZombieHorseMaxHealth)

	zombieTypeID := ResolveEntityTypeID(MobTypeZombie)
	if zombieTypeID < 0 {
		zombieTypeID = EntityTypeZombieID
	}
	zombieID = s.entities.Add(&world.Entity{
		TypeID:    zombieTypeID,
		TypeName:  MobTypeZombie,
		Dimension: normDim,
		X:         x,
		Y:         y + 0.85,
		Z:         z,
		OnGround:  false,
	})
	s.SetMobHealth(zombieID, DefaultZombieMaxHealth)
	return horseID, zombieID, nil
}
