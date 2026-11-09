package server

import (
	"math"
	"strings"

	"regionio/internal/nbt"
)

// --- 1. Ender Dragon Multi-Part Hitbox, Sitting Threshold & NBT (`EnderDragon.class` in 26.1.2.jar) ---

// EnderDragonPartSpec2612 describes one of the 8 sub-hitboxes of `minecraft:ender_dragon`
// (`head`, `neck`, `body`, `tail1`, `tail2`, `tail3`, `wing1`, `wing2`) in `26.1.2.jar`.
type EnderDragonPartSpec2612 struct {
	PartName string
	Width    float32
	Height   float32
	IsHead   bool
}

var enderDragonParts2612 = [8]EnderDragonPartSpec2612{
	{PartName: "head", Width: 1.0, Height: 1.0, IsHead: true},
	{PartName: "neck", Width: 3.0, Height: 3.0},
	{PartName: "body", Width: 5.0, Height: 3.0},
	{PartName: "tail1", Width: 2.0, Height: 2.0},
	{PartName: "tail2", Width: 2.0, Height: 2.0},
	{PartName: "tail3", Width: 2.0, Height: 2.0},
	{PartName: "wing1", Width: 4.0, Height: 2.0},
	{PartName: "wing2", Width: 4.0, Height: 2.0},
}

// AllEnderDragonParts2612 returns a copy of the 8 `EnderDragonPart` hitbox specs in 26.1.2.
func AllEnderDragonParts2612() []EnderDragonPartSpec2612 {
	out := make([]EnderDragonPartSpec2612, len(enderDragonParts2612))
	copy(out, enderDragonParts2612[:])
	return out
}

// ComputeEnderDragonPartDamage2612 implements `EnderDragon.hurt(ServerLevel, EnderDragonPart, DamageSource, float)`
// in `26.1.2.jar`:
//   - Projectiles (arrows/tridents) deal `0` damage and bounce off while the dragon is in a sitting phase (`5, 6, 7`).
//   - Hitting any non-`head` part scales damage to `damage / 4.0 + min(damage, 1.0)`.
//   - While sitting, `sittingDamageReceived` accumulates; when it reaches `>= 50.0` (`25%` of max health),
//     the dragon immediately transitions to `DragonPhaseTakeoff` (`forceTakeoff = true`) and resets `sittingDamageReceived = 0`.
func ComputeEnderDragonPartDamage2612(partName string, rawDamage float32, phase EnderDragonPhase, isProjectile bool, sittingDamageAccum float32) (effectiveDamage, nextSittingAccum float32, forceTakeoff bool) {
	if rawDamage <= 0 {
		return 0, sittingDamageAccum, false
	}
	if phase.IsSitting() && isProjectile {
		return 0, sittingDamageAccum, false
	}
	effectiveDamage = rawDamage
	if partName != "head" {
		minOne := rawDamage
		if minOne > 1.0 {
			minOne = 1.0
		}
		effectiveDamage = rawDamage/4.0 + minOne
	}
	if phase.IsSitting() {
		nextSittingAccum = sittingDamageAccum + effectiveDamage
		if nextSittingAccum >= 50.0 {
			return effectiveDamage, 0, true
		}
		return effectiveDamage, nextSittingAccum, false
	}
	return effectiveDamage, sittingDamageAccum, false
}

// --- 2. Wither Boss Shield, Blue Skull Block-Breaking & BossBars (`WitherBoss.class` in 26.1.2.jar) ---

// EvaluateWitherDamageAndBlueSkull2612 evaluates Wither combat rules in `26.1.2.jar`:
//   - During `Invul > 0` (`WitherPhaseCharging`), Wither is immune to normal damage.
//   - At `health <= 150.0` (`isPowered` / Wither Armor), Wither deflects all arrows/tridents/wind charges.
//   - Blue (`dangerous`) Wither Skulls can break blocks with explosion resistance `<= 3600000`
//     (including `obsidian` and `ancient_debris`, but NOT `bedrock`, `end_portal_frame`, `barrier`, `command_block`).
func EvaluateWitherDamageAndBlueSkull2612(health float32, invulTicks int32, isProjectile bool, blockName string, isBlueSkull bool) (witherTakesDamage, skullCanBreakBlock bool) {
	witherTakesDamage = invulTicks <= 0 && !(health <= WitherArmorHealthThreshold && isProjectile)

	normBlock := canonicalResourceName(blockName)
	switch normBlock {
	case "minecraft:air", "minecraft:bedrock", "minecraft:end_portal", "minecraft:end_portal_frame",
		"minecraft:end_gateway", "minecraft:command_block", "minecraft:repeating_command_block",
		"minecraft:chain_command_block", "minecraft:barrier", "minecraft:structure_block",
		"minecraft:jigsaw", "minecraft:moving_piston", "minecraft:reinforced_deepslate":
		skullCanBreakBlock = false
	case "minecraft:obsidian", "minecraft:crying_obsidian", "minecraft:respawn_anchor", "minecraft:ancient_debris", "minecraft:enchanting_table":
		skullCanBreakBlock = isBlueSkull
	default:
		skullCanBreakBlock = normBlock != ""
	}
	return witherTakesDamage, skullCanBreakBlock
}

// BossBarSpec2612 describes the vanilla 26.1.2 `ServerBossEvent` configuration
// for a boss or raid event.
type BossBarSpec2612 struct {
	Color          string
	Overlay        string
	DarkenScreen   bool
	PlayBossMusic  bool
	CreateWorldFog bool
}

// LookupBossBarSpec2612 returns the verified `ServerBossEvent` parameters for
// `minecraft:ender_dragon`, `minecraft:wither`, and `minecraft:raid` in `26.1.2.jar`.
func LookupBossBarSpec2612(bossOrEventKey string) (BossBarSpec2612, bool) {
	switch canonicalResourceName(bossOrEventKey) {
	case MobTypeEnderDragon:
		return BossBarSpec2612{
			Color:          "pink",
			Overlay:        "progress",
			DarkenScreen:   true,
			PlayBossMusic:  true,
			CreateWorldFog: true,
		}, true
	case MobTypeWither:
		return BossBarSpec2612{
			Color:          "purple",
			Overlay:        "progress",
			DarkenScreen:   true,
			PlayBossMusic:  false,
			CreateWorldFog: false,
		}, true
	case "minecraft:raid":
		return BossBarSpec2612{
			Color:          "red",
			Overlay:        "notched_10",
			DarkenScreen:   false,
			PlayBossMusic:  false,
			CreateWorldFog: false,
		}, true
	}
	return BossBarSpec2612{}, false
}

// --- 3. Warden Anger, Sonic Boom & Elder Guardian Mining Fatigue (26.1.2.jar) ---

// StepWardenAngerAndAction2612 evaluates a Warden anger update and attack
// selection in `26.1.2.jar`:
//   - Adds `angerDelta` to `currentAnger` and clamps to `[0, 150]`.
//   - `anger >= 80` is `ANGRY` (chases target; if first time crossing `80`, adds `+20` roar anger boost).
//   - `Sonic Boom` triggers when `anger >= 80`, target is unreachable by melee or `horizDist > 2.2`,
//     within `15.0` horizontal and `20.0` vertical blocks, and `sonicBoomCooldown <= 0`.
func StepWardenAngerAndAction2612(currentAnger, angerDelta int, crossedAngryFirstTime bool, horizDist, vertDist float64, sonicBoomCooldown int) (nextAnger int, isAgitated, isAngry, fireSonicBoom bool) {
	nextAnger = currentAnger + angerDelta
	if crossedAngryFirstTime && currentAnger < WardenAngryThreshold && nextAnger >= WardenAngryThreshold {
		nextAnger += WardenRoarAngerBoost
	}
	if nextAnger < WardenMinAnger {
		nextAnger = WardenMinAnger
	}
	if nextAnger > WardenMaxAnger {
		nextAnger = WardenMaxAnger
	}
	isAgitated = nextAnger >= WardenAgitatedAngerThreshold
	isAngry = nextAnger >= WardenAngryThreshold
	if isAngry && sonicBoomCooldown <= 0 && horizDist > WardenMeleeReach && horizDist <= WardenSonicBoomHorizRange && vertDist <= WardenSonicBoomVertRange {
		fireSonicBoom = true
	}
	return nextAnger, isAgitated, isAngry, fireSonicBoom
}

// EvaluateElderGuardianMiningFatigue2612 checks whether an `ElderGuardian`
// applies `Mining Fatigue III` (`amplifier = 2`, `duration = 6000` ticks = `5m`)
// to a player at distance `dist` on server tick `serverTick`:
//   - Pulses every `1200` ticks (`60s`) within `50.0` blocks.
func EvaluateElderGuardianMiningFatigue2612(serverTick int64, dist float64, existingAmplifier int, existingDurationTicks int32) (applyEffect bool, amplifier int, durationTicks int32) {
	if serverTick%1200 != 0 || dist > 50.0 {
		return false, 0, 0
	}
	if existingAmplifier >= 2 && existingDurationTicks >= 1200 {
		return false, existingAmplifier, existingDurationTicks
	}
	return true, 2, 6000
}

// --- 4. Creaking / Creaking Heart, Ravager, Evoker, Illusioner, Breeze & Bogged (26.1.2.jar) ---

// EvaluateCreakingGazeAndHeartHit2612 evaluates `Creaking.checkCanMove()` and
// `Creaking.hurt()` in `26.1.2.jar`:
//   - If `wearingCarvedPumpkin` is true, player gaze does NOT freeze the Creaking (`canMove = true`).
//   - Otherwise, if the cosine of the angle between the player's look vector and
//     the vector to the Creaking is `> 0.5` (~60° cone) within `32` blocks, the Creaking freezes (`canMove = false`).
//   - When a heart-bound Creaking (`isHeartBound == true`) is hit by a player/weapon,
//     it takes `0` health damage (`damageApplied = 0`) and instead triggers its linked
//     `minecraft:creaking_heart` to exude `2..3` `minecraft:resin_clump` blocks if `resinCooldown <= 0`.
func EvaluateCreakingGazeAndHeartHit2612(lookCosine, dist float64, wearingCarvedPumpkin, isHeartBound bool, resinCooldown int) (canMove bool, damageApplied float32, spawnResinClumps bool) {
	canMove = true
	if !wearingCarvedPumpkin && dist <= CreakingFollowRange && lookCosine > CreakingGazeCosThreshold {
		canMove = false
	}
	if isHeartBound {
		damageApplied = 0
		spawnResinClumps = resinCooldown <= 0
	} else {
		damageApplied = 1.0
	}
	return canMove, damageApplied, spawnResinClumps
}

// EvaluateRavagerShieldBlockAndRoar2612 evaluates `Ravager.blockedByShield()`
// and `Ravager.roar()` in `26.1.2.jar`:
//   - Blocking a Ravager attack with a shield stuns the Ravager for `40` ticks (`50%` chance if Ravager is attacking).
//   - When the `40`-tick stun finishes (`stunTicksRemaining == 1`), the Ravager roars,
//     dealing `6.0` damage and `4.0` knockback to all entities within `4.0` blocks.
func EvaluateRavagerShieldBlockAndRoar2612(blockedByShield bool, stunTicksRemaining int) (nextStunTicks int, triggerRoar bool, roarDamage float32, roarRadius float64) {
	if blockedByShield && stunTicksRemaining <= 0 {
		return 40, false, 0, 0
	}
	if stunTicksRemaining > 0 {
		nextStunTicks = stunTicksRemaining - 1
		if nextStunTicks == 0 {
			return 0, true, 6.0, 4.0
		}
		return nextStunTicks, false, 0, 0
	}
	return 0, false, 0, 0
}

// --- 5. Villager & Wandering Trader Trading (`MerchantOffer` in 26.1.2.jar) ---

// VillagerXPThresholds2612 holds the exact cumulative XP thresholds for Villager
// profession levels `1..5` (`Novice`, `Apprentice`, `Journeyman`, `Expert`, `Master`)
// from `VillagerData.NEXT_LEVEL_XP_THRESHOLDS` in `26.1.2.jar`.
var VillagerXPThresholds2612 = [5]int{0, 10, 70, 150, 250}

// ComputeVillagerMerchantOfferPrice2612 implements `MerchantOffer.getCostA()`
// and `Villager.updateSpecialPrices(Player)` in `26.1.2.jar`:
//   - Demand adjustment: `max(0, floor(baseCost * demand * priceMultiplier))`.
//   - Reputation adjustment: `-floor(reputation * priceMultiplier)`.
//   - Hero of the Village adjustment (`heroAmplifier >= 0`, where level I is `0`):
//     `discountRatio = 0.3 + 0.0625 * float64(heroAmplifier)`, subtracting
//     `max(1, int(floor(discountRatio * float64(baseCost))))`.
//   - Final price is clamped to `[1, maxStackSize]`.
func ComputeVillagerMerchantOfferPrice2612(baseCost, demand, reputation int, priceMultiplier float32, hasHeroOfTheVillage bool, heroAmplifier int, maxStackSize int) int {
	if baseCost <= 0 {
		return 1
	}
	if maxStackSize <= 0 {
		maxStackSize = 64
	}
	demandDiff := 0
	if demand > 0 {
		demandDiff = int(math.Floor(float64(float32(baseCost*demand) * priceMultiplier)))
	}
	specialDiff := -int(math.Floor(float64(float32(reputation) * priceMultiplier)))
	if hasHeroOfTheVillage {
		if heroAmplifier < 0 {
			heroAmplifier = 0
		}
		d := 0.3 + 0.0625*float64(heroAmplifier)
		heroSave := int(math.Floor(d * float64(baseCost)))
		if heroSave < 1 {
			heroSave = 1
		}
		specialDiff -= heroSave
	}
	finalCost := baseCost + demandDiff + specialDiff
	if finalCost < 1 {
		finalCost = 1
	}
	if finalCost > maxStackSize {
		finalCost = maxStackSize
	}
	return finalCost
}

// UpdateMerchantOfferDemandOnRestock2612 implements `MerchantOffer.updateDemand()`
// in `26.1.2.jar`: `demand = max(0, demand + uses - (maxUses - uses))` (`demand + 2*uses - maxUses`).
func UpdateMerchantOfferDemandOnRestock2612(currentDemand, uses, maxUses int) int {
	next := currentDemand + uses - (maxUses - uses)
	if next < 0 {
		return 0
	}
	return next
}

// --- 6. Advancements & Boss/Complex Mob NBT Round-Trip (26.1.2.jar) ---

// EvaluateBossOrComplexMobAdvancement2612 returns the vanilla 26.1.2 advancement
// resource ID triggered by the given boss/mob event.
func EvaluateBossOrComplexMobAdvancement2612(eventKind, mobOrItemID string) string {
	norm := canonicalResourceName(mobOrItemID)
	switch eventKind {
	case "kill_mob":
		switch norm {
		case MobTypeEnderDragon:
			return "minecraft:end/kill_dragon"
		case MobTypeBreeze:
			return "minecraft:adventure/blowback"
		default:
			return "minecraft:adventure/kill_a_mob"
		}
	case "summon_mob":
		switch norm {
		case MobTypeWither:
			return "minecraft:nether/summon_wither"
		case MobTypeIronGolem:
			return "minecraft:adventure/summon_iron_golem"
		case MobTypeEnderDragon:
			return "minecraft:end/respawn_dragon"
		}
	case "obtain_item":
		switch norm {
		case DragonEggBlockID:
			return "minecraft:end/dragon_egg"
		case DragonBreathItemID:
			return "minecraft:end/dragon_breath"
		}
	case "use_totem":
		return "minecraft:adventure/totem_of_undying"
	case "villager_trade":
		return "minecraft:adventure/trade"
	case "sneak_near_warden_or_sensor":
		return "minecraft:adventure/avoid_vibration"
	}
	return ""
}

// BossAndComplexMobNBT2612 holds the species-specific persistent NBT fields for
// `ender_dragon`, `wither`, `warden`, `creaking`, and `villager` in `26.1.2.jar`.
type BossAndComplexMobNBT2612 struct {
	EntityID            string
	DragonPhase         int32
	WitherInvul         int32
	WardenSuspectUUID   string
	WardenSuspectAnger  int32
	CreakingHomeX       int32
	CreakingHomeY       int32
	CreakingHomeZ       int32
	HasCreakingHome     bool
	VillagerProfession  string
	VillagerType        string
	VillagerLevel       int32
	VillagerXp          int32
}

// EncodeBossAndComplexMobNBT2612 encodes `BossAndComplexMobNBT2612` to an NBT compound blob.
func EncodeBossAndComplexMobNBT2612(d BossAndComplexMobNBT2612) []byte {
	c := nbt.NewCompound().
		Set("id", nbt.String(canonicalResourceName(d.EntityID))).
		Set("DragonPhase", nbt.Int(d.DragonPhase)).
		Set("Invul", nbt.Int(d.WitherInvul)).
		Set("Xp", nbt.Int(d.VillagerXp))

	if d.WardenSuspectUUID != "" {
		suspect := nbt.NewCompound().
			Set("uuid", nbt.String(d.WardenSuspectUUID)).
			Set("anger", nbt.Int(d.WardenSuspectAnger))
		angerComp := nbt.NewCompound().
			Set("suspects", nbt.List{ElemID: nbt.TagCompound, Elems: []nbt.Tag{suspect}})
		c.Set("anger", angerComp)
	}
	if d.HasCreakingHome {
		c.Set("home_pos", nbt.IntArray{d.CreakingHomeX, d.CreakingHomeY, d.CreakingHomeZ})
	}
	if d.VillagerProfession != "" || d.VillagerLevel > 0 {
		vd := nbt.NewCompound().
			Set("profession", nbt.String(canonicalResourceName(d.VillagerProfession))).
			Set("type", nbt.String(canonicalResourceName(d.VillagerType))).
			Set("level", nbt.Int(d.VillagerLevel))
		c.Set("VillagerData", vd)
	}
	return nbt.MarshalNamed("", c)
}

// DecodeBossAndComplexMobNBT2612 decodes `BossAndComplexMobNBT2612` from an NBT compound blob.
func DecodeBossAndComplexMobNBT2612(raw []byte) (BossAndComplexMobNBT2612, error) {
	_, tag, err := nbt.UnmarshalNamed(raw)
	if err != nil {
		return BossAndComplexMobNBT2612{}, err
	}
	c, ok := tag.(*nbt.Compound)
	if !ok || c == nil {
		return BossAndComplexMobNBT2612{}, nil
	}
	var d BossAndComplexMobNBT2612
	if t, ok := c.Get("id"); ok {
		if v, ok := t.(nbt.String); ok {
			d.EntityID = strings.TrimSpace(string(v))
		}
	}
	if t, ok := c.Get("DragonPhase"); ok {
		if v, ok := t.(nbt.Int); ok {
			d.DragonPhase = int32(v)
		}
	}
	if t, ok := c.Get("Invul"); ok {
		if v, ok := t.(nbt.Int); ok {
			d.WitherInvul = int32(v)
		}
	}
	if t, ok := c.Get("Xp"); ok {
		if v, ok := t.(nbt.Int); ok {
			d.VillagerXp = int32(v)
		}
	}
	if t, ok := c.Get("anger"); ok {
		if ac, ok := t.(*nbt.Compound); ok && ac != nil {
			if st, ok := ac.Get("suspects"); ok {
				if lst, ok := st.(nbt.List); ok && len(lst.Elems) > 0 {
					if sc, ok := lst.Elems[0].(*nbt.Compound); ok && sc != nil {
						if u, ok := sc.Get("uuid"); ok {
							if s, ok := u.(nbt.String); ok {
								d.WardenSuspectUUID = string(s)
							}
						}
						if a, ok := sc.Get("anger"); ok {
							if iv, ok := a.(nbt.Int); ok {
								d.WardenSuspectAnger = int32(iv)
							}
						}
					}
				}
			}
		}
	}
	if t, ok := c.Get("home_pos"); ok {
		if arr, ok := t.(nbt.IntArray); ok && len(arr) == 3 {
			d.HasCreakingHome = true
			d.CreakingHomeX = arr[0]
			d.CreakingHomeY = arr[1]
			d.CreakingHomeZ = arr[2]
		}
	}
	if t, ok := c.Get("VillagerData"); ok {
		if vd, ok := t.(*nbt.Compound); ok && vd != nil {
			if p, ok := vd.Get("profession"); ok {
				if s, ok := p.(nbt.String); ok {
					d.VillagerProfession = string(s)
				}
			}
			if vt, ok := vd.Get("type"); ok {
				if s, ok := vt.(nbt.String); ok {
					d.VillagerType = string(s)
				}
			}
			if lv, ok := vd.Get("level"); ok {
				if iv, ok := lv.(nbt.Int); ok {
					d.VillagerLevel = int32(iv)
				}
			}
		}
	}
	return d, nil
}
