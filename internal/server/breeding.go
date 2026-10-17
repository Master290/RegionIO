package server

import (
	"errors"
	"math"
	"math/rand"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Breeding, love-mode, and baby mob growth constants for Minecraft 26.1.2 (protocol 775).
const (
	// BabyMobGrowthTicks is the initial negative age of a newly bred baby mob
	// (-24000 ticks = 20 minutes at 20 TPS until reaching adult age 0).
	BabyMobGrowthTicks = -24000

	// AnimalMateCloseDist is the horizontal distance within which two animals in
	// love complete mating and spawn a baby.
	AnimalMateCloseDist = 2.5

	// BreedingMinXP and BreedingMaxXP define the 1..7 experience orb reward
	// spawned when two animals breed.
	BreedingMinXP = 1
	BreedingMaxXP = 7

	// EntityDataAgeableBabyID is the SynchedEntityData accessor index for
	// AgeableMob.DATA_BABY_ID in Minecraft 26.1.2 (index 16, BOOLEAN serializer 8).
	EntityDataAgeableBabyID byte = 16
)

// EncodeAgeableMobDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating AgeableMob.DATA_BABY_ID (index 16, boolean) for protocol 775.
func EncodeAgeableMobDataPacket(entityID int32, isBaby bool) []byte {
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataAgeableBabyID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(isBaby)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeAgeableMobDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeAgeableMobDataPacket.
func DecodeAgeableMobDataPacket(r *protocol.Reader) (entityID int32, isBaby bool, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, err
	}
	idx, err := r.ReadByte()
	if err != nil {
		return 0, false, err
	}
	if idx != EntityDataAgeableBabyID {
		return 0, false, errors.New("breeding: unexpected AgeableMob baby data index")
	}
	ser, err := r.VarInt()
	if err != nil {
		return 0, false, err
	}
	if ser != EntityDataSerializerBool {
		return 0, false, errors.New("breeding: unexpected AgeableMob baby serializer")
	}
	isBaby, err = r.Bool()
	if err != nil {
		return 0, false, err
	}
	end, err := r.ReadByte()
	if err != nil {
		return 0, false, err
	}
	if end != 0xFF {
		return 0, false, errors.New("breeding: missing SynchedEntityData end byte")
	}
	return entityID, isBaby, nil
}

// IsWolfMeatItem reports whether itemName is a meat item that can heal or breed
// a tamed wolf in vanilla 26.1.2 (#minecraft:wolf_food).
func IsWolfMeatItem(itemName string) bool {
	switch canonicalResourceName(itemName) {
	case "minecraft:beef", "minecraft:cooked_beef",
		"minecraft:porkchop", "minecraft:cooked_porkchop",
		"minecraft:chicken", "minecraft:cooked_chicken",
		"minecraft:mutton", "minecraft:cooked_mutton",
		"minecraft:rabbit", "minecraft:cooked_rabbit",
		"minecraft:rotten_flesh",
		"minecraft:cod", "minecraft:cooked_cod",
		"minecraft:salmon", "minecraft:cooked_salmon",
		"minecraft:tropical_fish":
		return true
	default:
		return false
	}
}

// IsCatFoodItem reports whether itemName is a fish item used to tempt, tame,
// heal, or breed a cat in vanilla 26.1.2 (#minecraft:cat_food).
func IsCatFoodItem(itemName string) bool {
	switch canonicalResourceName(itemName) {
	case "minecraft:cod", "minecraft:salmon":
		return true
	default:
		return false
	}
}

// IsParrotTameSeedItem reports whether itemName is a seed item that can tame a
// wild parrot in vanilla 26.1.2 (#minecraft:parrot_food).
func IsParrotTameSeedItem(itemName string) bool {
	switch canonicalResourceName(itemName) {
	case "minecraft:wheat_seeds", "minecraft:beetroot_seeds",
		"minecraft:melon_seeds", "minecraft:pumpkin_seeds",
		"minecraft:torchflower_seeds", "minecraft:pitcher_pod":
		return true
	default:
		return false
	}
}

// IsEquineBreedingFood reports whether itemName triggers love mode on a tamed
// horse or donkey in vanilla 26.1.2.
func IsEquineBreedingFood(itemName string) bool {
	switch canonicalResourceName(itemName) {
	case "minecraft:golden_apple", "minecraft:enchanted_golden_apple", "minecraft:golden_carrot":
		return true
	default:
		return false
	}
}

// IsLlamaBreedingFood reports whether itemName triggers love mode on a tamed
// llama in vanilla 26.1.2 (hay_block).
func IsLlamaBreedingFood(itemName string) bool {
	return canonicalResourceName(itemName) == "minecraft:hay_block"
}

// IsBreedingFoodForMob reports whether itemName is a valid breeding/growth food
// for a mob of typeName in vanilla 26.1.2.
func IsBreedingFoodForMob(typeName, itemName string) bool {
	mob := canonicalResourceName(typeName)
	item := canonicalResourceName(itemName)
	switch mob {
	case MobTypeCow, MobTypeSheep:
		return item == "minecraft:wheat"
	case MobTypePig:
		return item == "minecraft:carrot" || item == "minecraft:potato" || item == "minecraft:beetroot"
	case MobTypeChicken:
		return IsParrotTameSeedItem(item)
	case MobTypeWolf:
		return IsWolfMeatItem(item)
	case MobTypeCat:
		return IsCatFoodItem(item)
	case MobTypeHorse, MobTypeDonkey:
		return IsEquineBreedingFood(item)
	case MobTypeLlama, MobTypeTraderLlama:
		return IsLlamaBreedingFood(item)
	case MobTypeSniffer:
		return item == TorchflowerSeedsItemID
	case MobTypeArmadillo:
		return item == SpiderEyeItemID
	case MobTypeCamel:
		return item == CactusItemID
	case MobTypeFrog, MobTypeTadpole:
		return item == SlimeBallItemID
	case MobTypeAxolotl:
		return item == TropicalFishBucketItemID
	case MobTypeGoat:
		return item == GoatBreedingFoodItemID
	default:
		return false
	}
}

// IsBreedableMob reports whether typeName can enter love mode and produce
// baby offspring.
func IsBreedableMob(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypeCow, MobTypeSheep, MobTypePig, MobTypeChicken,
		MobTypeWolf, MobTypeCat,
		MobTypeHorse, MobTypeDonkey, MobTypeLlama, MobTypeTraderLlama,
		MobTypeSniffer, MobTypeArmadillo, MobTypeCamel,
		MobTypeFrog, MobTypeAxolotl, MobTypeGoat:
		return true
	default:
		return false
	}
}

// CanMobsMateWithEachOther reports whether two mobs of typeA and typeB can mate
// with each other, returning the resulting offspring entity type (e.g. horse +
// donkey -> mule).
func CanMobsMateWithEachOther(typeA, typeB string) (offspringType string, ok bool) {
	a := canonicalResourceName(typeA)
	b := canonicalResourceName(typeB)
	if a == b && IsBreedableMob(a) {
		return a, true
	}
	if (a == MobTypeHorse && b == MobTypeDonkey) || (a == MobTypeDonkey && b == MobTypeHorse) {
		return MobTypeMule, true
	}
	if (a == MobTypeLlama && b == MobTypeTraderLlama) || (a == MobTypeTraderLlama && b == MobTypeLlama) {
		return MobTypeLlama, true
	}
	return "", false
}

// MobAge returns the age in ticks of entityID (< 0 for babies, 0 for adults).
func (s *Server) MobAge(entityID int32) int {
	if s == nil {
		return 0
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	mem := tr.mobStates[entityID]
	if mem == nil {
		return 0
	}
	return mem.ageTicks
}

// IsMobBaby reports whether entityID is currently a baby (ageTicks < 0).
func (s *Server) IsMobBaby(entityID int32) bool {
	return s.MobAge(entityID) < 0
}

// SetMobAge sets the age in ticks of entityID (< 0 for baby, >= 0 for adult)
// and broadcasts AgeableMob.DATA_BABY_ID if its baby state changed.
func (s *Server) SetMobAge(entityID int32, ageTicks int) {
	if s == nil || s.entities == nil {
		return
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(entityID, tr.aiTickID)
	wasBaby := mem.ageTicks < 0
	mem.ageTicks = ageTicks
	nowBaby := mem.ageTicks < 0
	tr.mu.Unlock()

	if wasBaby != nowBaby || nowBaby {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeAgeableMobDataPacket(entityID, nowBaby))
	}
}

// MobInLoveTicks returns the remaining love-mode ticks (0..600) for entityID.
func (s *Server) MobInLoveTicks(entityID int32) int {
	if s == nil {
		return 0
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	mem := tr.mobStates[entityID]
	if mem == nil {
		return 0
	}
	return mem.inLoveTicks
}

// MobBreedCooldown returns the remaining breeding cooldown ticks (0..6000) for entityID.
func (s *Server) MobBreedCooldown(entityID int32) int {
	if s == nil {
		return 0
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	mem := tr.mobStates[entityID]
	if mem == nil {
		return 0
	}
	return mem.breedCooldown
}

// SetMobBreedCooldown sets the breeding cooldown ticks on entityID.
func (s *Server) SetMobBreedCooldown(entityID int32, ticks int) {
	if s == nil {
		return
	}
	if ticks < 0 {
		ticks = 0
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(entityID, tr.aiTickID)
	mem.breedCooldown = ticks
	tr.mu.Unlock()
}

// SetMobInLove places an adult mob with entityID into love mode for ticks
// (typically AnimalLoveDurationTicks = 600) and broadcasts heart particles.
func (s *Server) SetMobInLove(entityID int32, ticks int, causePlayer *PlayerSession) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(entityID, tr.aiTickID)
	if mem.ageTicks < 0 || mem.breedCooldown > 0 {
		tr.mu.Unlock()
		return false
	}
	mem.inLoveTicks = ticks
	if causePlayer != nil {
		mem.loveCause = causePlayer
	}
	tr.mu.Unlock()

	if ticks > 0 {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayEntityEvent,
			EncodeEntityEventPacket(entityID, EntityEventLoveHearts))
	}
	return true
}

// SpawnBabyMob spawns a baby mob of typeName at (x, y, z) in dimension with
// Age = BabyMobGrowthTicks (-24000) and broadcasts its baby metadata.
func (s *Server) SpawnBabyMob(dimension string, x, y, z float64, typeName string) (int32, error) {
	id, err := s.SpawnMobInDimension(dimension, x, y, z, typeName)
	if err != nil {
		return 0, err
	}
	growthTicks := BabyMobGrowthTicks
	if canonicalResourceName(typeName) == MobTypeSniffer {
		growthTicks = SniffletGrowthTicks
	}
	s.SetMobAge(id, growthTicks)
	return id, nil
}

// FeedBabyMob feeds a baby mob its species' breeding food, accelerating its
// remaining growth time by 10% (AgeableMob.ageUp((int)(-age / 20) * 2, true))
// and broadcasting happy villager growth particles.
func (s *Server) FeedBabyMob(session *PlayerSession, entityID int32, itemName string) (fed bool, newAge int) {
	if s == nil || s.entities == nil {
		return false, 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsBreedingFoodForMob(ent.TypeName, itemName) {
		return false, 0
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(entityID, tr.aiTickID)
	if mem.ageTicks >= 0 {
		tr.mu.Unlock()
		return false, mem.ageTicks
	}
	// Vanilla AgeableMob.getSpeedUpSecondsWhenFeeding: 10% of remaining baby ticks.
	delta := (-mem.ageTicks) / 10
	if delta < 1 {
		delta = 1
	}
	mem.ageTicks += delta
	if mem.ageTicks > 0 {
		mem.ageTicks = 0
	}
	newAge = mem.ageTicks
	becameAdult := newAge == 0
	tr.mu.Unlock()

	if session != nil {
		consumeOneHeldItemInSurvival(session)
	}
	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(entityID, EntityEventVillagerHappy))
	if becameAdult {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeAgeableMobDataPacket(entityID, false))
	}
	return true, newAge
}

// TickBabyMobs advances the age of all baby mobs by 1 tick toward adulthood (0)
// and broadcasts AgeableMob.DATA_BABY_ID = false when a baby grows into an adult.
func (s *Server) TickBabyMobs() {
	if s == nil || s.entities == nil {
		return
	}
	var grownUpIDs []int32
	tr := s.entityTracker()
	tr.mu.Lock()
	for id, mem := range tr.mobStates {
		if mem == nil || mem.ageTicks >= 0 {
			continue
		}
		mem.ageTicks++
		if mem.ageTicks >= 0 {
			mem.ageTicks = 0
			grownUpIDs = append(grownUpIDs, id)
		}
	}
	tr.mu.Unlock()

	for _, id := range grownUpIDs {
		if ent := s.entities.Get(id); ent != nil {
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
				EncodeAgeableMobDataPacket(id, false))
		}
	}
}

// TryBreedMobWithNearbyMate checks if entityID (currently in love) has a valid
// adult mate in love within maxDist blocks; if found, completes breeding and
// returns the spawned baby's entityID.
func (s *Server) TryBreedMobWithNearbyMate(entityID int32, maxDist float64, rng *rand.Rand) (offspringID int32, bred bool) {
	if s == nil || s.entities == nil {
		return 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return 0, false
	}
	dim := ent.EffectiveDimension()
	maxDistSq := maxDist * maxDist

	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStates[entityID]
	if mem == nil || mem.ageTicks < 0 || mem.inLoveTicks <= 0 || mem.breedCooldown > 0 {
		tr.mu.Unlock()
		return 0, false
	}

	var mateEnt *world.Entity
	var offspringType string
	var causePlayer *PlayerSession
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		other := &allEnts[i]
		if other.ID == entityID || other.EffectiveDimension() != dim {
			continue
		}
		babyType, ok := CanMobsMateWithEachOther(ent.TypeName, other.TypeName)
		if !ok {
			continue
		}
		dx := other.X - ent.X
		dy := other.Y - ent.Y
		dz := other.Z - ent.Z
		if dx*dx+dy*dy+dz*dz > maxDistSq {
			continue
		}
		otherMem := tr.mobStates[other.ID]
		if otherMem == nil || otherMem.ageTicks < 0 || otherMem.inLoveTicks <= 0 || otherMem.breedCooldown > 0 {
			continue
		}
		mateEnt = other
		offspringType = babyType
		causePlayer = mem.loveCause
		if causePlayer == nil {
			causePlayer = otherMem.loveCause
		}
		mem.inLoveTicks = 0
		mem.breedCooldown = AnimalBreedCooldownTicks
		mem.loveCause = nil
		otherMem.inLoveTicks = 0
		otherMem.breedCooldown = AnimalBreedCooldownTicks
		otherMem.loveCause = nil
		break
	}
	tr.mu.Unlock()

	if mateEnt == nil {
		return 0, false
	}

	midX := (ent.X + mateEnt.X) * 0.5
	midY := math.Max(ent.Y, mateEnt.Y)
	midZ := (ent.Z + mateEnt.Z) * 0.5
	offspringID, err := s.SpawnBabyMob(dim, midX, midY, midZ, offspringType)
	if err != nil {
		return 0, false
	}

	// Inherit ownership and attributes if parents are tamed.
	s.inheritTamedParentTraits(ent.ID, mateEnt.ID, offspringID, offspringType, rng)

	// Spawn 1..7 XP orbs at the breeding position.
	xp := int32(BreedingMinXP)
	if rng != nil {
		xp += int32(rng.Intn(BreedingMaxXP - BreedingMinXP + 1))
	} else {
		xp = 4
	}
	s.SpawnExperienceOrbsInDimension(dim, midX, midY+0.25, midZ, xp)

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(offspringID, EntityEventLoveHearts))

	if causePlayer != nil {
		s.GrantCriterion(causePlayer, "minecraft:husbandry/breed_an_animal", "bred")
	}
	return offspringID, true
}

func consumeOneHeldItemInSurvival(session *PlayerSession) {
	if session == nil {
		return
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeCreative {
		return
	}
	held := session.HeldItem()
	if held.IsEmpty() {
		return
	}
	slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
	rem := held.Count - 1
	if rem <= 0 {
		session.SetInventorySlot(slotIdx, world.ItemStack{})
	} else {
		next := held
		next.Count = rem
		session.SetInventorySlot(slotIdx, next)
	}
}
