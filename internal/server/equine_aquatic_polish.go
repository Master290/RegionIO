package server

import (
	"errors"
	"math"
	"math/rand"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Vanilla 26.1.2 (protocol 775) Equine, Camel / Camel Husk, Llama, Aquatic
// (Dolphin, Squid, Glow Squid, Turtle, Cod, Salmon, Pufferfish, Tropical Fish),
// and Strider constants verified against server-26.1.2.jar.
const (
	// AbstractHorse slot offsets, breeding cross factor, and movement factors in 26.1.2.
	AbstractHorseChestSlotOffset      int     = 499
	AbstractHorseInventorySlotOffset  int     = 500
	AbstractHorseInventoryRows        int     = 3
	AbstractHorseBreedingCrossFactor  float64 = 0.15
	AbstractHorseBackwardsSpeedFactor float64 = 0.25
	AbstractHorseSidewaysSpeedFactor  float64 = 0.5
	AbstractHorseBabyScale            float64 = 0.7

	// ZombieHorse 26.1.2 constants (ZombieHorse.java).
	ZombieHorseMaxHealth2612       float32 = 25.0
	ZombieHorseMinJumpStrength     float64 = 0.5
	ZombieHorseMaxJumpStrength     float64 = 0.7
	ZombieHorseSpeedDivisor        float64 = 42.16
	ZombieHorseSpeedBase           float64 = 9.0
	ZombieHorseTemptSpeed          float64 = 1.25
	ZombieHorseChargeSpeedModifier float64 = 1.4
	IronSpearItemID                        = "minecraft:iron_spear"

	// CamelHusk 26.1.2 constants (CamelHusk.java, entity ID 20).
	MobTypeCamelHusk             = "minecraft:camel_husk"
	EntityTypeCamelHuskID        = 20
	CamelHuskChargeSpeedModifier = 4.0

	SoundCamelHuskAmbient   = "minecraft:entity.camel_husk.ambient"
	SoundCamelHuskDash      = "minecraft:entity.camel_husk.dash"
	SoundCamelHuskDashReady = "minecraft:entity.camel_husk.dash_ready"
	SoundCamelHuskDeath     = "minecraft:entity.camel_husk.death"
	SoundCamelHuskEat       = "minecraft:entity.camel_husk.eat"
	SoundCamelHuskHurt      = "minecraft:entity.camel_husk.hurt"
	SoundCamelHuskSaddle    = "minecraft:entity.camel_husk.saddle"
	SoundCamelHuskSit       = "minecraft:entity.camel_husk.sit"
	SoundCamelHuskStand     = "minecraft:entity.camel_husk.stand"

	// LlamaFollowCaravanGoal 26.1.2 constants (LlamaFollowCaravanGoal.java).
	LlamaCaravanInitialSpeedModifier float64 = 2.1
	LlamaCaravanMinSpeedModifier     float64 = 1.0
	LlamaCaravanMaxLeashHops         int     = 8

	// Salmon & TropicalFish SynchedEntityData indices and constants in 26.1.2.
	EntityDataSalmonTypeID byte = 17 // Salmon.DATA_TYPE (INT serializer 1: 0=small, 1=medium, 2=large)

	SalmonVariantSmallID  int32 = 0
	SalmonVariantMediumID int32 = 1
	SalmonVariantLargeID  int32 = 2

	SalmonSmallWeight  int = 30
	SalmonMediumWeight int = 50
	SalmonLargeWeight  int = 15

	SalmonSmallScale  float32 = 0.5
	SalmonMediumScale float32 = 1.0
	SalmonLargeScale  float32 = 1.5

	SalmonMaxSchoolSize      int = 5
	DefaultFishMaxSchoolSize int = 8

	FishBoneMealDropChance float64 = 0.05

	// Strider & ItemBasedSteering 26.1.2 constants (Strider.java, ItemBasedSteering.java).
	StriderSuffocatingSpeedModifier     float64 = -0.34 // ADD_MULTIPLIED_BASE -> 0.175 * 0.66 = 0.1155
	StriderRiddenLavaSpeedFactor        float64 = 0.55
	StriderRiddenSuffocatingSpeedFactor float64 = 0.35
	StriderBoostMinTicks2612            int     = 140
	StriderBoostRandomSpan2612          int     = 841 // 140 + nextInt(841) -> [140, 980]
)

// --- 1. Equine 26.1.2 Breeding Genetics, Food Table & ZombieHorse Mechanics ---

// CreateEquineOffspringAttribute2612 implements the exact vanilla 26.1.2
// AbstractHorse.createOffspringAttribute(parentA, parentB, min, max, random)
// formula using BREEDING_CROSS_FACTOR = 0.15 and reflection clamping at [min, max].
func CreateEquineOffspringAttribute2612(parentA, parentB, minVal, maxVal float64, rng *rand.Rand) float64 {
	if minVal >= maxVal {
		return minVal
	}
	pA := math.Max(minVal, math.Min(maxVal, parentA))
	pB := math.Max(minVal, math.Min(maxVal, parentB))
	cross := AbstractHorseBreedingCrossFactor * (maxVal - minVal)
	span := math.Abs(pA-pB) + cross*2.0
	mid := (pA + pB) / 2.0
	r1, r2, r3 := 0.5, 0.5, 0.5
	if rng != nil {
		r1 = rng.Float64()
		r2 = rng.Float64()
		r3 = rng.Float64()
	}
	offset := (r1+r2+r3)/3.0 - 0.5
	val := mid + span*offset
	if val > maxVal {
		over := val - maxVal
		return math.Max(minVal, maxVal-over)
	}
	if val < minVal {
		under := minVal - val
		return math.Min(maxVal, minVal+under)
	}
	return val
}

// InheritEquineAttributes2612 computes offspring MaxHealth, MovementSpeed, and
// JumpStrength using the vanilla 26.1.2 AbstractHorse.createOffspringAttribute formula.
func InheritEquineAttributes2612(parentAHP, parentBHP float32, parentASpeed, parentBSpeed, parentAJump, parentBJump float64, rng *rand.Rand) (maxHP float32, speed, jump float64) {
	hp := CreateEquineOffspringAttribute2612(float64(parentAHP), float64(parentBHP), float64(MinEquineMaxHealth), float64(MaxEquineMaxHealth), rng)
	speed = CreateEquineOffspringAttribute2612(parentASpeed, parentBSpeed, MinHorseMovementSpeed, MaxHorseMovementSpeed, rng)
	jump = CreateEquineOffspringAttribute2612(parentAJump, parentBJump, MinHorseJumpStrength, MaxHorseJumpStrength, rng)
	return float32(hp), speed, jump
}

// EquineFoodDetails2612 returns the exact vanilla 26.1.2 AbstractHorse.handleEating
// / Llama.handleEating stats for feeding itemName to mobType:
//   - heal: HP restored
//   - ageUpSeconds: baby growth acceleration in seconds (20 ticks per second)
//   - temperGain: temper added when untamed
//   - triggersLove: whether feeding a tamed adult puts it in love mode
func EquineFoodDetails2612(mobType, itemName string) (heal float32, ageUpSeconds, temperGain int, triggersLove, ok bool) {
	mob := canonicalResourceName(mobType)
	item := canonicalResourceName(itemName)
	if mob == MobTypeLlama || mob == MobTypeTraderLlama {
		switch item {
		case "minecraft:wheat":
			return 2.0, 10, 3, false, true
		case "minecraft:hay_block":
			return 10.0, 90, 6, true, true
		default:
			return 0, 0, 0, false, false
		}
	}
	if mob == MobTypeZombieHorse {
		if item == RedMushroomItemID {
			return 3.0, 0, 3, false, true
		}
		return 0, 0, 0, false, false
	}
	if mob == MobTypeSkeletonHorse {
		return 0, 0, 0, false, false
	}
	switch item {
	case "minecraft:wheat":
		return 2.0, 20, 3, false, true
	case "minecraft:sugar":
		return 1.0, 30, 3, false, true
	case "minecraft:hay_block":
		return 20.0, 180, 0, false, true
	case "minecraft:apple", "minecraft:carrot":
		return 3.0, 60, 3, false, true
	case "minecraft:golden_carrot":
		return 4.0, 60, 5, true, true
	case "minecraft:golden_apple", "minecraft:enchanted_golden_apple":
		return 10.0, 240, 10, true, true
	default:
		return 0, 0, 0, false, false
	}
}

// IsHorseFoodItem2612 reports whether itemName is in the vanilla 26.1.2
// `#minecraft:horse_food` item tag.
func IsHorseFoodItem2612(itemName string) bool {
	switch canonicalResourceName(itemName) {
	case "minecraft:wheat", "minecraft:sugar", "minecraft:hay_block",
		"minecraft:apple", "minecraft:carrot", "minecraft:golden_carrot",
		"minecraft:golden_apple", "minecraft:enchanted_golden_apple":
		return true
	default:
		return false
	}
}

// IsZombieHorseFoodItem reports whether itemName is in the vanilla 26.1.2
// `#minecraft:zombie_horse_food` item tag (`minecraft:red_mushroom`).
func IsZombieHorseFoodItem(itemName string) bool {
	return canonicalResourceName(itemName) == RedMushroomItemID
}

// RollZombieHorseJumpStrength implements ZombieHorse.generateZombieHorseJumpStrength
// from vanilla 26.1.2: 0.5 + (r1 + r2 + r3) * (0.2 / 3.0) in [0.5, 0.7].
func RollZombieHorseJumpStrength(rng *rand.Rand) float64 {
	if rng == nil {
		return 0.6
	}
	return ZombieHorseMinJumpStrength + (rng.Float64()+rng.Float64()+rng.Float64())*(0.2/3.0)
}

// RollZombieHorseMovementSpeed implements ZombieHorse.generateZombieHorseSpeed
// from vanilla 26.1.2: (9.0 + r1 + r2 + r3) / 42.16 in [0.21347, 0.28463].
func RollZombieHorseMovementSpeed(rng *rand.Rand) float64 {
	if rng == nil {
		return (ZombieHorseSpeedBase + 1.5) / ZombieHorseSpeedDivisor
	}
	return (ZombieHorseSpeedBase + rng.Float64() + rng.Float64() + rng.Float64()) / ZombieHorseSpeedDivisor
}

// IsZombieHorseProtectedFromSun reports whether the zombie_horse with entityID
// has body armor equipped in `EquipmentSlot.BODY` (vanilla 26.1.2
// `ZombieHorse.sunProtectionSlot() == EquipmentSlot.BODY`), protecting it from
// burning in daylight.
func (s *Server) IsZombieHorseProtectedFromSun(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	return s.HorseArmorItem(entityID) != ""
}

// SpawnNaturalZombieHorseJockey implements ZombieHorse.finalizeSpawn for
// EntitySpawnReason.NATURAL in vanilla 26.1.2: spawns an adult `minecraft:zombie_horse`
// (25.0 HP) ridden by an adult `minecraft:zombie` holding `minecraft:iron_spear`
// in its main hand.
func (s *Server) SpawnNaturalZombieHorseJockey(dimension string, x, y, z float64, rng *rand.Rand) (horseID, zombieRiderID int32, err error) {
	if s == nil || s.entities == nil {
		return 0, 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	horseID, err = s.SpawnMobInDimension(normDim, x, y, z, MobTypeZombieHorse)
	if err != nil {
		return 0, 0, err
	}
	_ = s.ensureEquineState(horseID, MobTypeZombieHorse, rng)
	s.SetMobHealth(horseID, ZombieHorseMaxHealth2612)

	zombieRiderID, err = s.SpawnMobInDimension(normDim, x, y, z, MobTypeZombie)
	if err != nil {
		return horseID, 0, err
	}
	s.SetMobBaby(zombieRiderID, false)

	est := s.ensureEquineState(horseID, MobTypeZombieHorse, rng)
	mgr := s.equineManager()
	mgr.mu.Lock()
	est.RiderEntityID = zombieRiderID
	mgr.mu.Unlock()

	if ent := s.entities.Get(horseID); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetPassengersCB,
			EncodeSetPassengersPacket(horseID, []int32{zombieRiderID}))
		spear := world.NewNamedItemStack(IronSpearItemID, 1)
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlaySetEquipment,
			encodeMainHandEquipmentPacket2612(zombieRiderID, spear))
	}
	return horseID, zombieRiderID, nil
}

func encodeMainHandEquipmentPacket2612(entityID int32, stack world.ItemStack) []byte {
	w := protocol.NewWriter(24)
	w.VarInt(entityID)
	w.Byte(0) // EquipmentSlot.MAINHAND (slot 0)
	itemID := stack.ResolvedItemID()
	if stack.IsEmpty() || itemID <= 0 {
		w.VarInt(0)
	} else {
		w.VarInt(stack.Count)
		w.VarInt(itemID)
		w.VarInt(0)
		w.VarInt(0)
	}
	return w.Bytes()
}

// RollEquineDeathLoot2612 rolls the vanilla 26.1.2 death loot table for
// `horse`, `donkey`, `mule`, `llama`, `trader_llama` (`0..2` leather),
// `skeleton_horse` (`0..2` bone), and `zombie_horse` (`2..3` rotten_flesh),
// plus `0..lootingLevel` bonus.
func RollEquineDeathLoot2612(mobType string, rng *rand.Rand, lootingLevel int) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	norm := canonicalResourceName(mobType)
	switch norm {
	case MobTypeZombieHorse:
		count := 2
		if rng != nil {
			count = 2 + rng.Intn(2)
			if lootingLevel > 0 {
				count += rng.Intn(lootingLevel + 1)
			}
		}
		return []world.ItemStack{world.NewNamedItemStack("minecraft:rotten_flesh", int32(count))}
	case MobTypeSkeletonHorse:
		count := 1
		if rng != nil {
			count = rng.Intn(3)
			if lootingLevel > 0 {
				count += rng.Intn(lootingLevel + 1)
			}
		}
		if count <= 0 {
			return nil
		}
		return []world.ItemStack{world.NewNamedItemStack("minecraft:bone", int32(count))}
	case MobTypeHorse, MobTypeDonkey, MobTypeMule, MobTypeLlama, MobTypeTraderLlama:
		count := 1
		if rng != nil {
			count = rng.Intn(3)
			if lootingLevel > 0 {
				count += rng.Intn(lootingLevel + 1)
			}
		}
		if count <= 0 {
			return nil
		}
		return []world.ItemStack{world.NewNamedItemStack("minecraft:leather", int32(count))}
	default:
		return nil
	}
}

// --- 2. Camel & Camel Husk (`minecraft:camel`, `minecraft:camel_husk`) ---

// IsCamelOrCamelHuskType reports whether typeName is `minecraft:camel` or `minecraft:camel_husk`.
func IsCamelOrCamelHuskType(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypeCamel, MobTypeCamelHusk:
		return true
	default:
		return false
	}
}

// IsCamelHuskFoodItem reports whether itemName is in `#minecraft:camel_husk_food`
// (`minecraft:rabbit_foot` in vanilla 26.1.2).
func IsCamelHuskFoodItem(itemName string) bool {
	return canonicalResourceName(itemName) == RabbitFootItemID
}

// CanCamelHuskBreed returns false because in vanilla 26.1.2 `CamelHusk.canMate`
// and `CamelHusk.canFallInLove` always return false.
func CanCamelHuskBreed() bool {
	return false
}

// SpawnCamelHusk spawns an adult `minecraft:camel_husk` (entity ID 20, 32.0 HP,
// `isBaby() == false`) at (x, y, z) in dimension and broadcasts its initial
// AddEntity and Camel SynchedEntityData packets.
func (s *Server) SpawnCamelHusk(dimension string, x, y, z float64) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeCamelHusk)
	if typeID < 0 {
		typeID = EntityTypeCamelHuskID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeCamelHusk,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})

	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(id, tr.aiTickID)
	mem.healthInit = true
	mem.health = DefaultCamelMaxHealth
	mem.lastHealth = DefaultCamelMaxHealth
	tr.mu.Unlock()

	st := s.ensureCamelState(id)
	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		if st != nil {
			s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
				EncodeCamelEntityDataPacket(id, st.Saddled, st.Sitting, st.Dashing, st.LastPoseChangeTick))
		}
	}
	return id, nil
}

// MountMobOnCamel mounts a mob entity (such as a husk jockey) onto a Camel or
// Camel Husk (up to MaxCamelPassengers = 2 riders).
func (s *Server) MountMobOnCamel(camelID, mobID int32) (seatIndex int, ok bool) {
	if s == nil || s.entities == nil || camelID <= 0 || mobID <= 0 {
		return -1, false
	}
	ent := s.entities.Get(camelID)
	mobEnt := s.entities.Get(mobID)
	if ent == nil || mobEnt == nil || !IsCamelOrCamelHuskType(ent.TypeName) {
		return -1, false
	}
	st := s.ensureCamelState(camelID)
	if st == nil {
		return -1, false
	}
	mgr := s.camelManager()
	mgr.mu.Lock()
	for i, existing := range st.Passengers {
		if existing == mobID {
			mgr.mu.Unlock()
			return i, true
		}
	}
	if len(st.Passengers) >= MaxCamelPassengers {
		mgr.mu.Unlock()
		return -1, false
	}
	st.Passengers = append(st.Passengers, mobID)
	seatIndex = len(st.Passengers) - 1
	passengersCopy := make([]int32, len(st.Passengers))
	copy(passengersCopy, st.Passengers)
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetPassengersCB,
		EncodeSetPassengersPacket(camelID, passengersCopy))
	return seatIndex, true
}

// IsCamelHuskMobControlled implements CamelHusk.isMobControlled() from vanilla
// 26.1.2: returns true when the first passenger (seat 0) is a non-player Mob entity.
func (s *Server) IsCamelHuskMobControlled(camelID int32) bool {
	if s == nil || s.entities == nil || camelID <= 0 {
		return false
	}
	driverID := s.CamelDriver(camelID)
	if driverID <= 0 {
		return false
	}
	return s.entities.Get(driverID) != nil
}

// CanCamelHuskBeLeashed implements CamelHusk.canBeLeashed() from vanilla 26.1.2:
// returns `!isMobControlled()`.
func (s *Server) CanCamelHuskBeLeashed(camelID int32) bool {
	return !s.IsCamelHuskMobControlled(camelID)
}

// RollCamelHuskDeathLoot rolls the vanilla 26.1.2 `entities/camel_husk.json`
// loot table: `2..3` `minecraft:rotten_flesh` (+ `0..lootingLevel`).
func RollCamelHuskDeathLoot(rng *rand.Rand, lootingLevel int) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	count := 2
	if rng != nil {
		count = 2 + rng.Intn(2)
		if lootingLevel > 0 {
			count += rng.Intn(lootingLevel + 1)
		}
	}
	return []world.ItemStack{world.NewNamedItemStack("minecraft:rotten_flesh", int32(count))}
}

// --- 3. Llama & Trader Llama Polish (Caravan Speed Decay, Leash Hop Check, Wolf Repulsion) ---

// IsLlamaFoodItem reports whether itemName is in `#minecraft:llama_food`
// (`minecraft:wheat`, `minecraft:hay_block`).
func IsLlamaFoodItem(itemName string) bool {
	switch canonicalResourceName(itemName) {
	case "minecraft:wheat", "minecraft:hay_block":
		return true
	default:
		return false
	}
}

// IsLlamaTemptItem reports whether itemName is in `#minecraft:llama_tempt_items`
// (`minecraft:hay_block`).
func IsLlamaTemptItem(itemName string) bool {
	return canonicalResourceName(itemName) == "minecraft:hay_block"
}

// ComputeLlamaCaravanSpeedModifier implements the speed modifier adjustment in
// vanilla 26.1.2 `LlamaFollowCaravanGoal.tick()`: starts at 2.1, increases when
// distance > 3.0, and decays toward 1.0 when within 2.0 blocks of the caravan head.
func ComputeLlamaCaravanSpeedModifier(currentSpeedMod, distToCaravanAhead float64) float64 {
	if currentSpeedMod <= 0 {
		currentSpeedMod = LlamaCaravanInitialSpeedModifier
	}
	if distToCaravanAhead > 3.0 {
		if currentSpeedMod < LlamaCaravanInitialSpeedModifier {
			currentSpeedMod *= 1.2
			if currentSpeedMod > LlamaCaravanInitialSpeedModifier {
				currentSpeedMod = LlamaCaravanInitialSpeedModifier
			}
		}
	} else if distToCaravanAhead < 2.0 {
		currentSpeedMod *= 0.8
		if currentSpeedMod < LlamaCaravanMinSpeedModifier {
			currentSpeedMod = LlamaCaravanMinSpeedModifier
		}
	}
	return currentSpeedMod
}

// IsLlamaCaravanHeadLeashed implements `LlamaFollowCaravanGoal.firstIsLeashed(llama, 1)`
// from vanilla 26.1.2, walking up the caravan chain up to 8 hops to check
// whether the caravan leader is leashed.
func (s *Server) IsLlamaCaravanHeadLeashed(llamaID int32) bool {
	if s == nil || llamaID <= 0 {
		return false
	}
	cur := llamaID
	for hop := 0; hop <= LlamaCaravanMaxLeashHops; hop++ {
		if s.IsEntityLeashed(cur) {
			return true
		}
		tst, ok := s.GetTamedMobState(cur)
		if !ok || tst.CaravanHeadID <= 0 || tst.CaravanHeadID == cur {
			return false
		}
		cur = tst.CaravanHeadID
	}
	return false
}

// OnWolfHitByLlamaSpit handles the vanilla 26.1.2 llama spit vs wolf interaction:
// when a wild wolf is hit by `minecraft:llama_spit`, it clears its attack target
// and flees from the spitting llama (`WolfAvoidEntityGoal`).
func (s *Server) OnWolfHitByLlamaSpit(wolfID, llamaID int32) bool {
	if s == nil || s.entities == nil || wolfID <= 0 || llamaID <= 0 {
		return false
	}
	wEnt := s.entities.Get(wolfID)
	lEnt := s.entities.Get(llamaID)
	if wEnt == nil || lEnt == nil || canonicalResourceName(wEnt.TypeName) != MobTypeWolf {
		return false
	}
	if s.IsMobTamed(wolfID) {
		return false
	}
	if st := s.ensureTamedMobState(wolfID, MobTypeWolf); st != nil {
		mgr := s.tamedManager()
		mgr.mu.Lock()
		st.TargetEntityID = 0
		st.TargetPlayer = nil
		st.AngerTicks = 0
		mgr.mu.Unlock()
	}
	dx := wEnt.X - lEnt.X
	dz := wEnt.Z - lEnt.Z
	dist := math.Hypot(dx, dz)
	if dist < 1e-4 {
		dx, dz, dist = 1.0, 0.0, 1.0
	}
	step := WolfChaseSpeed * 2.5
	nx := wEnt.X + (dx/dist)*step
	nz := wEnt.Z + (dz/dist)*step
	s.entities.Update(wolfID, func(e *world.Entity) {
		e.X = nx
		e.Z = nz
	})
	return true
}

// SyncTraderLlamaDespawnDelay returns the despawn delay inherited by a
// `minecraft:trader_llama` from its owning `minecraft:wandering_trader`
// (`TraderLlama.aiStep` in vanilla 26.1.2).
func (s *Server) SyncTraderLlamaDespawnDelay(llamaID int32) int {
	if s == nil || llamaID <= 0 {
		return 0
	}
	est, ok := s.GetEquineState(llamaID)
	if !ok || est.TraderOwnerID <= 0 {
		return 0
	}
	tst, ok := s.GetWanderingTraderState(est.TraderOwnerID)
	if !ok {
		return 0
	}
	return tst.DespawnDelay
}

// --- 4. Fish (Salmon Variants, 22 Common Tropical Fish Variants, Schooling & Aquatic Loot) ---

// MaxSchoolSizeForFish returns the vanilla 26.1.2 `AbstractSchoolingFish.getMaxSchoolSize()`:
// 5 for `minecraft:salmon` (`Salmon.getMaxSchoolSize() == 5`), 8 for `cod` / `tropical_fish`,
// and 1 for non-schooling `pufferfish`.
func MaxSchoolSizeForFish(fishType string) int {
	switch canonicalResourceName(fishType) {
	case MobTypeSalmon:
		return SalmonMaxSchoolSize
	case MobTypeCod, MobTypeTropicalFish:
		return DefaultFishMaxSchoolSize
	default:
		return 1
	}
}

// RollSalmonVariantWeighted2612 rolls a `Salmon$Variant` using the exact vanilla
// 26.1.2 `Salmon.finalizeSpawn` weights: SMALL (weight 30), MEDIUM (weight 50),
// LARGE (weight 15), total weight 95.
func RollSalmonVariantWeighted2612(rng *rand.Rand) int32 {
	if rng == nil {
		return SalmonVariantMediumID
	}
	total := SalmonSmallWeight + SalmonMediumWeight + SalmonLargeWeight
	roll := rng.Intn(total)
	if roll < SalmonSmallWeight {
		return SalmonVariantSmallID
	}
	if roll < SalmonSmallWeight+SalmonMediumWeight {
		return SalmonVariantMediumID
	}
	return SalmonVariantLargeID
}

// SalmonBoundingBoxScale returns the vanilla 26.1.2 `Salmon$Variant.boundingBoxScale`:
// 0.5 for `small` (0), 1.0 for `medium` (1), and 1.5 for `large` (2).
func SalmonBoundingBoxScale(variant int32) float32 {
	switch variant {
	case SalmonVariantSmallID:
		return SalmonSmallScale
	case SalmonVariantLargeID:
		return SalmonLargeScale
	default:
		return SalmonMediumScale
	}
}

// SalmonVariantName returns the serialized string name (`"small"`, `"medium"`,
// `"large"`) for a `Salmon$Variant` ID.
func SalmonVariantName(variant int32) string {
	switch variant {
	case SalmonVariantSmallID:
		return SalmonVariantSmall
	case SalmonVariantLargeID:
		return SalmonVariantLarge
	default:
		return SalmonVariantMedium
	}
}

// EncodeSalmonDataPacket encodes a ClientboundSetEntityDataPacket (0x63) for
// `minecraft:salmon` with AbstractFish.FROM_BUCKET (16, bool) and Salmon.DATA_TYPE
// (17, int: 0=small, 1=medium, 2=large).
func EncodeSalmonDataPacket(entityID int32, fromBucket bool, variant int32) []byte {
	if variant < SalmonVariantSmallID || variant > SalmonVariantLargeID {
		variant = SalmonVariantMediumID
	}
	w := protocol.NewWriter(20)
	w.VarInt(entityID)
	w.Byte(EntityDataFishFromBucketID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(fromBucket)
	w.Byte(EntityDataSalmonTypeID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(variant)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeSalmonDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeSalmonDataPacket.
func DecodeSalmonDataPacket(data []byte) (entityID int32, fromBucket bool, variant int32, err error) {
	r := protocol.NewReader(data)
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, 0, err
	}
	idx0, err := r.ReadByte()
	if err != nil || idx0 != EntityDataFishFromBucketID {
		return 0, false, 0, errors.New("salmon: unexpected FROM_BUCKET index")
	}
	ser0, err := r.VarInt()
	if err != nil || ser0 != EntityDataSerializerBool {
		return 0, false, 0, errors.New("salmon: unexpected FROM_BUCKET serializer")
	}
	fromBucket, err = r.Bool()
	if err != nil {
		return 0, false, 0, err
	}
	idx1, err := r.ReadByte()
	if err != nil || idx1 != EntityDataSalmonTypeID {
		return 0, false, 0, errors.New("salmon: unexpected DATA_TYPE index")
	}
	ser1, err := r.VarInt()
	if err != nil || ser1 != EntityDataSerializerInt {
		return 0, false, 0, errors.New("salmon: unexpected DATA_TYPE serializer")
	}
	variant, err = r.VarInt()
	if err != nil {
		return 0, false, 0, err
	}
	end, err := r.ReadByte()
	if err != nil || end != 0xFF {
		return 0, false, 0, errors.New("salmon: missing 0xFF terminator")
	}
	return entityID, fromBucket, variant, nil
}

// TropicalFishCommonVariants2612 lists the 22 canonical `TropicalFish.COMMON_VARIANTS`
// in vanilla 26.1.2 (`PackTropicalFishVariant(baseSize, pattern, baseColor, patternColor)`).
//
// Base sizes: 0 = SMALL (`KOB`=0, `SUNSTREAK`=1, `SNOOPER`=2, `DASHER`=3, `BRINELY`=4, `SPOTTY`=5),
// 1 = LARGE (`FLOPPER`=0, `STRIPEY`=1, `GLITTER`=2, `BLOCKFISH`=3, `BETTY`=4, `CLAYFISH`=5).
var TropicalFishCommonVariants2612 = [22]int32{
	PackTropicalFishVariant(1, 1, 1, 7),  // STRIPEY, ORANGE, GRAY
	PackTropicalFishVariant(0, 0, 7, 7),  // KOB, GRAY, GRAY
	PackTropicalFishVariant(1, 0, 7, 11), // FLOPPER, GRAY, BLUE
	PackTropicalFishVariant(1, 5, 0, 7),  // CLAYFISH, WHITE, GRAY
	PackTropicalFishVariant(0, 1, 11, 7), // SUNSTREAK, BLUE, GRAY
	PackTropicalFishVariant(0, 0, 1, 0),  // KOB, ORANGE, WHITE
	PackTropicalFishVariant(0, 5, 6, 3),  // SPOTTY, PINK, LIGHT_BLUE
	PackTropicalFishVariant(1, 3, 10, 4), // BLOCKFISH, PURPLE, YELLOW
	PackTropicalFishVariant(1, 5, 0, 14), // CLAYFISH, WHITE, RED
	PackTropicalFishVariant(0, 5, 0, 4),  // SPOTTY, WHITE, YELLOW
	PackTropicalFishVariant(1, 2, 0, 7),  // GLITTER, WHITE, GRAY
	PackTropicalFishVariant(1, 5, 0, 1),  // CLAYFISH, WHITE, ORANGE
	PackTropicalFishVariant(0, 3, 9, 6),  // DASHER, CYAN, PINK
	PackTropicalFishVariant(0, 4, 5, 3),  // BRINELY, LIME, LIGHT_BLUE
	PackTropicalFishVariant(1, 4, 14, 0), // BETTY, RED, WHITE
	PackTropicalFishVariant(0, 2, 7, 14), // SNOOPER, GRAY, RED
	PackTropicalFishVariant(1, 3, 14, 0), // BLOCKFISH, RED, WHITE
	PackTropicalFishVariant(1, 0, 0, 4),  // FLOPPER, WHITE, YELLOW
	PackTropicalFishVariant(0, 0, 14, 0), // KOB, RED, WHITE
	PackTropicalFishVariant(0, 1, 7, 0),  // SUNSTREAK, GRAY, WHITE
	PackTropicalFishVariant(0, 3, 9, 4),  // DASHER, CYAN, YELLOW
	PackTropicalFishVariant(1, 0, 4, 4),  // FLOPPER, YELLOW, YELLOW
}

// RollTropicalFishVariant2612 rolls a packed `TropicalFish` variant using the
// vanilla 26.1.2 `TropicalFish.finalizeSpawn` rule: 90% chance to select one of
// the 22 `COMMON_VARIANTS`, 10% chance to roll a random size/pattern/color combination.
func RollTropicalFishVariant2612(rng *rand.Rand) int32 {
	if rng == nil {
		return TropicalFishCommonVariants2612[0]
	}
	if rng.Float64() < 0.9 {
		return TropicalFishCommonVariants2612[rng.Intn(len(TropicalFishCommonVariants2612))]
	}
	returnPack := PackTropicalFishVariant(
		rng.Intn(2),
		rng.Intn(6),
		rng.Intn(16),
		rng.Intn(16),
	)
	return returnPack
}

// EncodeTropicalFishDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// for `minecraft:tropical_fish` with FROM_BUCKET (16) and DATA_ID_TYPE_VARIANT (17).
func EncodeTropicalFishDataPacket(entityID int32, fromBucket bool, packedVariant int32) []byte {
	w := protocol.NewWriter(20)
	w.VarInt(entityID)
	w.Byte(EntityDataFishFromBucketID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(fromBucket)
	w.Byte(EntityDataTropicalFishVariantID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(packedVariant)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeTropicalFishDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeTropicalFishDataPacket.
func DecodeTropicalFishDataPacket(data []byte) (entityID int32, fromBucket bool, packedVariant int32, err error) {
	r := protocol.NewReader(data)
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, 0, err
	}
	idx0, err := r.ReadByte()
	if err != nil || idx0 != EntityDataFishFromBucketID {
		return 0, false, 0, errors.New("tropical_fish: unexpected FROM_BUCKET index")
	}
	ser0, err := r.VarInt()
	if err != nil || ser0 != EntityDataSerializerBool {
		return 0, false, 0, errors.New("tropical_fish: unexpected FROM_BUCKET serializer")
	}
	fromBucket, err = r.Bool()
	if err != nil {
		return 0, false, 0, err
	}
	idx1, err := r.ReadByte()
	if err != nil || idx1 != EntityDataTropicalFishVariantID {
		return 0, false, 0, errors.New("tropical_fish: unexpected DATA_ID_TYPE_VARIANT index")
	}
	ser1, err := r.VarInt()
	if err != nil || ser1 != EntityDataSerializerInt {
		return 0, false, 0, errors.New("tropical_fish: unexpected DATA_ID_TYPE_VARIANT serializer")
	}
	packedVariant, err = r.VarInt()
	if err != nil {
		return 0, false, 0, err
	}
	end, err := r.ReadByte()
	if err != nil || end != 0xFF {
		return 0, false, 0, errors.New("tropical_fish: missing 0xFF terminator")
	}
	return entityID, fromBucket, packedVariant, nil
}

// RollFishDeathLoot2612 rolls the vanilla 26.1.2 death loot for `cod`, `salmon`,
// `pufferfish`, and `tropical_fish`: 1 raw fish (or cooked for cod/salmon when
// onFire) plus a 5% (`0.05`) chance to drop 1 `minecraft:bone_meal`.
func RollFishDeathLoot2612(fishType string, onFire bool, rng *rand.Rand) []world.ItemStack {
	var fishItem string
	switch canonicalResourceName(fishType) {
	case MobTypeCod:
		if onFire {
			fishItem = "minecraft:cooked_cod"
		} else {
			fishItem = "minecraft:cod"
		}
	case MobTypeSalmon:
		if onFire {
			fishItem = "minecraft:cooked_salmon"
		} else {
			fishItem = "minecraft:salmon"
		}
	case MobTypePufferfish:
		fishItem = "minecraft:pufferfish"
	case MobTypeTropicalFish:
		fishItem = "minecraft:tropical_fish"
	default:
		return nil
	}
	out := []world.ItemStack{world.NewNamedItemStack(fishItem, 1)}
	if rng != nil && rng.Float64() < FishBoneMealDropChance {
		out = append(out, world.NewNamedItemStack("minecraft:bone_meal", 1))
	}
	return out
}

// RollSquidDeathLoot2612 rolls the vanilla 26.1.2 `entities/squid.json` and
// `entities/glow_squid.json` loot tables: `1..3` `ink_sac` or `glow_ink_sac`
// (+ `0..lootingLevel`).
func RollSquidDeathLoot2612(isGlow bool, rng *rand.Rand, lootingLevel int) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	count := 1
	if rng != nil {
		count = 1 + rng.Intn(3)
		if lootingLevel > 0 {
			count += rng.Intn(lootingLevel + 1)
		}
	}
	itemID := "minecraft:ink_sac"
	if isGlow {
		itemID = "minecraft:glow_ink_sac"
	}
	return []world.ItemStack{world.NewNamedItemStack(itemID, int32(count))}
}

// RollDolphinDeathLoot2612 rolls the vanilla 26.1.2 `entities/dolphin.json`
// loot table: `0..1` `minecraft:cod` (or `minecraft:cooked_cod` when onFire)
// (+ `0..lootingLevel`).
func RollDolphinDeathLoot2612(onFire bool, rng *rand.Rand, lootingLevel int) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	count := 1
	if rng != nil {
		count = rng.Intn(2)
		if lootingLevel > 0 {
			count += rng.Intn(lootingLevel + 1)
		}
	}
	if count <= 0 {
		return nil
	}
	itemID := "minecraft:cod"
	if onFire {
		itemID = "minecraft:cooked_cod"
	}
	return []world.ItemStack{world.NewNamedItemStack(itemID, int32(count))}
}

// RollTurtleDeathLoot2612 rolls the vanilla 26.1.2 `entities/turtle.json` loot
// table: `0..2` `minecraft:seagrass` (+ `0..lootingLevel`), plus `1`
// `minecraft:bowl` when killed by lightning (`#minecraft:is_lightning`).
func RollTurtleDeathLoot2612(killedByLightning bool, rng *rand.Rand, lootingLevel int) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	var out []world.ItemStack
	count := 1
	if rng != nil {
		count = rng.Intn(3)
		if lootingLevel > 0 {
			count += rng.Intn(lootingLevel + 1)
		}
	}
	if count > 0 {
		out = append(out, world.NewNamedItemStack("minecraft:seagrass", int32(count)))
	}
	if killedByLightning {
		out = append(out, world.NewNamedItemStack("minecraft:bowl", 1))
	}
	return out
}

// --- 5. Strider 26.1.2 Speed Modifiers, Boost Factor & Food Tags ---

// IsStriderFoodItem reports whether itemName is in `#minecraft:strider_food`
// (`minecraft:warped_fungus`).
func IsStriderFoodItem(itemName string) bool {
	return canonicalResourceName(itemName) == "minecraft:warped_fungus"
}

// IsStriderTemptItem reports whether itemName is in `#minecraft:strider_tempt_items`
// (`minecraft:warped_fungus`, `minecraft:warped_fungus_on_a_stick`).
func IsStriderTemptItem(itemName string) bool {
	switch canonicalResourceName(itemName) {
	case "minecraft:warped_fungus", "minecraft:warped_fungus_on_a_stick":
		return true
	default:
		return false
	}
}

// RollStriderBoostDuration2612 rolls the vanilla 26.1.2 `ItemBasedSteering.boost`
// duration: `140 + random.nextInt(841)` -> `[140, 980]` ticks.
func RollStriderBoostDuration2612(rng *rand.Rand) int32 {
	if rng == nil {
		return int32(StriderBoostMinTicks2612)
	}
	return int32(StriderBoostMinTicks2612 + rng.Intn(StriderBoostRandomSpan2612))
}

// ComputeStriderBoostFactor2612 implements `ItemBasedSteering.boostFactor()` from
// vanilla 26.1.2: `1.0 + 1.15 * sin(float64(boostTime) / float64(boostTimeTotal) * pi)`
// when boosting, or `1.0` when not boosting.
func ComputeStriderBoostFactor2612(boostTime, boostTimeTotal int32) float64 {
	if boostTime <= 0 || boostTimeTotal <= 0 {
		return 1.0
	}
	if boostTime > boostTimeTotal {
		boostTime = boostTimeTotal
	}
	return 1.0 + 1.15*math.Sin(float64(boostTime)/float64(boostTimeTotal)*math.Pi)
}

// ComputeStriderAttributeSpeed2612 returns the effective `MOVEMENT_SPEED`
// attribute of a `minecraft:strider` in vanilla 26.1.2: `0.175` on lava, or
// `0.175 * (1.0 - 0.34) = 0.1155` when suffocating (shivering outside lava).
func ComputeStriderAttributeSpeed2612(suffocating bool) float64 {
	if suffocating {
		return StriderLavaSpeed * (1.0 + StriderSuffocatingSpeedModifier)
	}
	return StriderLavaSpeed
}

// ComputeStriderRiddenSpeed2612 implements `Strider.getRiddenSpeed(Player)` from
// vanilla 26.1.2: `attributeSpeed * (suffocating ? 0.35 : 0.55) * boostFactor`.
func ComputeStriderRiddenSpeed2612(suffocating bool, boostTime, boostTimeTotal int32) float64 {
	attrSpeed := ComputeStriderAttributeSpeed2612(suffocating)
	factor := StriderRiddenLavaSpeedFactor
	if suffocating {
		factor = StriderRiddenSuffocatingSpeedFactor
	}
	return attrSpeed * factor * ComputeStriderBoostFactor2612(boostTime, boostTimeTotal)
}
