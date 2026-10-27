package server

import (
	"errors"
	"math"
	"math/rand"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Axolotl (minecraft:axolotl) and Bucket of Axolotl (minecraft:axolotl_bucket)
// constants, variants, SynchedEntityData indices, and sounds for Minecraft 26.1.2 (protocol 775).
const (
	MobTypeAxolotl      = "minecraft:axolotl"
	EntityTypeAxolotlID = 7

	DefaultAxolotlMaxHealth           float32 = 14.0
	AxolotlAttackDamage               float32 = 2.0
	AxolotlSwimSpeed                  float64 = 0.12
	AxolotlHuntRange                  float64 = 12.0
	AxolotlMeleeReach                 float64 = 1.8
	AxolotlAttackCooldownTicks        int     = 20
	AxolotlPlayDeadDurationTicks      int     = 200  // 10 seconds (200 ticks) playing dead + Regeneration I
	AxolotlPassiveHuntCooldownTicks   int     = 2400 // 2 minutes (2400 ticks) cooldown after killing passive prey
	AxolotlPlayerSupportRange         float64 = 20.0
	AxolotlPlayerRegenBaseTicks       int32   = 100  // +5s (100 ticks) Regeneration I per assisted kill
	AxolotlPlayerRegenMaxTicks        int32   = 2400 // Capped at 2 minutes (2400 ticks)
	AxolotlMaxOutOfWaterTicks         int     = 6000 // 5 minutes out of water before drying out
	AxolotlRareBlueBreedingDenominator int    = 1200 // 1/1200 chance for rare blue variant on breeding

	// Axolotl variant IDs (Axolotl.Variant ordinals 0..4).
	AxolotlVariantLucy int32 = 0 // Leucistic (Pink)
	AxolotlVariantWild int32 = 1 // Wild (Brown)
	AxolotlVariantGold int32 = 2 // Gold
	AxolotlVariantCyan int32 = 3 // Cyan
	AxolotlVariantBlue int32 = 4 // Blue (Rare, 1/1200 breed chance)

	AxolotlBucketItemID      = "minecraft:axolotl_bucket"
	TropicalFishBucketItemID = "minecraft:tropical_fish_bucket"

	// SynchedEntityData indices for Axolotl in Minecraft 26.1.2 (protocol 775).
	// Axolotl extends Animal -> AgeableMob (16) -> PathfinderMob -> Mob -> LivingEntity -> Entity.
	EntityDataAxolotlVariantID     byte = 17 // Axolotl.DATA_VARIANT (INT serializer = 1)
	EntityDataAxolotlPlayingDeadID byte = 18 // Axolotl.DATA_PLAYING_DEAD (BOOLEAN serializer = 8)
	EntityDataAxolotlFromBucketID  byte = 19 // Axolotl.FROM_BUCKET (BOOLEAN serializer = 8)

	// Canonical Axolotl sounds.
	SoundAxolotlAttack      = "minecraft:entity.axolotl.attack"
	SoundAxolotlDeath       = "minecraft:entity.axolotl.death"
	SoundAxolotlHurt        = "minecraft:entity.axolotl.hurt"
	SoundAxolotlIdleAir     = "minecraft:entity.axolotl.idle_air"
	SoundAxolotlIdleWater   = "minecraft:entity.axolotl.idle_water"
	SoundAxolotlSplash      = "minecraft:entity.axolotl.splash"
	SoundAxolotlSwim        = "minecraft:entity.axolotl.swim"
	SoundBucketFillAxolotl  = "minecraft:item.bucket.fill_axolotl"
	SoundBucketEmptyAxolotl = "minecraft:item.bucket.empty_axolotl"
)

// AxolotlBehaviorState holds the server-side AI, variant, play-dead, aquatic
// hunting, and bucket state for a `minecraft:axolotl` entity.
type AxolotlBehaviorState struct {
	EntityID                int32
	Variant                 int32
	PlayingDead             bool
	PlayingDeadTicks        int
	FromBucket              bool
	PassiveHuntCooldown     int
	AttackCooldown          int
	LastTargetedMobID       int32
	OutOfWaterTicks         int
}

type bucketedAxolotlInfo struct {
	Variant  int32
	AgeTicks int
	Health   float32
}

type serverAxolotlManager struct {
	mu          sync.RWMutex
	axolotls    map[int32]*AxolotlBehaviorState
	bucketedMap map[*PlayerSession]bucketedAxolotlInfo
}

var axolotlManagers sync.Map // map[*Server]*serverAxolotlManager

func (s *Server) axolotlManager() *serverAxolotlManager {
	if s == nil {
		return &serverAxolotlManager{
			axolotls:    make(map[int32]*AxolotlBehaviorState),
			bucketedMap: make(map[*PlayerSession]bucketedAxolotlInfo),
		}
	}
	if v, ok := axolotlManagers.Load(s); ok {
		return v.(*serverAxolotlManager)
	}
	mgr := &serverAxolotlManager{
		axolotls:    make(map[int32]*AxolotlBehaviorState),
		bucketedMap: make(map[*PlayerSession]bucketedAxolotlInfo),
	}
	actual, _ := axolotlManagers.LoadOrStore(s, mgr)
	return actual.(*serverAxolotlManager)
}

// IsAxolotlPassivePrey reports whether typeName is a passive aquatic mob that
// Axolotls hunt when their 2-minute passive hunting cooldown is 0.
func IsAxolotlPassivePrey(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case "minecraft:squid", "minecraft:glow_squid",
		"minecraft:cod", "minecraft:salmon",
		"minecraft:tropical_fish", "minecraft:pufferfish",
		MobTypeTadpole:
		return true
	default:
		return false
	}
}

// IsAxolotlHostileAquaticTarget reports whether typeName is a hostile aquatic
// mob that Axolotls attack without any hunting cooldown.
func IsAxolotlHostileAquaticTarget(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case "minecraft:drowned", "minecraft:guardian", "minecraft:elder_guardian":
		return true
	default:
		return false
	}
}

// RollAxolotlOffspringVariant chooses the variant (0..4) for a baby Axolotl
// bred from parents with variants parentA and parentB:
//   - 1 in 1200 chance to mutate into the rare Blue variant (AxolotlVariantBlue = 4)
//   - Otherwise 50/50 inheritance from parentA or parentB.
func RollAxolotlOffspringVariant(parentA, parentB int32, rng *rand.Rand) int32 {
	if rng != nil {
		if rng.Intn(AxolotlRareBlueBreedingDenominator) == 0 {
			return AxolotlVariantBlue
		}
		if rng.Intn(2) == 0 {
			return parentA
		}
		return parentB
	}
	return parentA
}

func (s *Server) ensureAxolotlState(entityID int32, defaultVariant int32) *AxolotlBehaviorState {
	if s == nil || entityID <= 0 {
		return nil
	}
	if defaultVariant < AxolotlVariantLucy || defaultVariant > AxolotlVariantBlue {
		defaultVariant = AxolotlVariantLucy
	}
	mgr := s.axolotlManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.axolotls[entityID]
	if !ok || st == nil {
		st = &AxolotlBehaviorState{
			EntityID: entityID,
			Variant:  defaultVariant,
		}
		mgr.axolotls[entityID] = st
	}
	return st
}

// GetAxolotlState returns a snapshot of the AxolotlBehaviorState for entityID.
func (s *Server) GetAxolotlState(entityID int32) (AxolotlBehaviorState, bool) {
	if s == nil || entityID <= 0 {
		return AxolotlBehaviorState{}, false
	}
	mgr := s.axolotlManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	st, ok := mgr.axolotls[entityID]
	if !ok || st == nil {
		return AxolotlBehaviorState{}, false
	}
	return *st, true
}

// IsAxolotlPlayingDead reports whether the Axolotl with entityID is currently
// playing dead (causing hostile mobs to ignore it).
func (s *Server) IsAxolotlPlayingDead(entityID int32) bool {
	st, ok := s.GetAxolotlState(entityID)
	return ok && st.PlayingDead
}

// EncodeAxolotlEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// for a `minecraft:axolotl` entity with its Variant (index 17, INT),
// PlayingDead (index 18, BOOLEAN), and FromBucket (index 19, BOOLEAN).
func EncodeAxolotlEntityDataPacket(entityID int32, variant int32, playingDead, fromBucket bool) []byte {
	if variant < AxolotlVariantLucy || variant > AxolotlVariantBlue {
		variant = AxolotlVariantLucy
	}
	w := protocol.NewWriter(24)
	w.VarInt(entityID)
	w.Byte(EntityDataAxolotlVariantID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(variant)
	w.Byte(EntityDataAxolotlPlayingDeadID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(playingDead)
	w.Byte(EntityDataAxolotlFromBucketID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(fromBucket)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeAxolotlEntityDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeAxolotlEntityDataPacket.
func DecodeAxolotlEntityDataPacket(r *protocol.Reader) (entityID int32, variant int32, playingDead, fromBucket bool, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, 0, false, false, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, 0, false, false, err
		}
		if idx == 0xFF {
			break
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, 0, false, false, err
		}
		switch idx {
		case EntityDataAxolotlVariantID:
			if ser != EntityDataSerializerInt {
				return 0, 0, false, false, errors.New("axolotl: unexpected DATA_VARIANT serializer")
			}
			variant, err = r.VarInt()
		case EntityDataAxolotlPlayingDeadID:
			if ser != EntityDataSerializerBool {
				return 0, 0, false, false, errors.New("axolotl: unexpected DATA_PLAYING_DEAD serializer")
			}
			playingDead, err = r.Bool()
		case EntityDataAxolotlFromBucketID:
			if ser != EntityDataSerializerBool {
				return 0, 0, false, false, errors.New("axolotl: unexpected FROM_BUCKET serializer")
			}
			fromBucket, err = r.Bool()
		default:
			return 0, 0, false, false, errors.New("axolotl: unexpected SynchedEntityData index")
		}
		if err != nil {
			return 0, 0, false, false, err
		}
	}
	return entityID, variant, playingDead, fromBucket, nil
}

func (s *Server) broadcastAxolotlState(ent *world.Entity, st AxolotlBehaviorState) {
	if s == nil || ent == nil {
		return
	}
	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeAxolotlEntityDataPacket(ent.ID, st.Variant, st.PlayingDead, st.FromBucket))
}

// SpawnAxolotl spawns a `minecraft:axolotl` at (x, y, z) in dimension with
// variant (0..4, or random common variant 0..3 if variant < 0) and broadcasts
// its creation and initial metadata.
func (s *Server) SpawnAxolotl(dimension string, x, y, z float64, variant int32, isBaby bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil server or entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	if variant < AxolotlVariantLucy || variant > AxolotlVariantBlue {
		variant = AxolotlVariantLucy
	}
	typeID := ResolveEntityTypeID(MobTypeAxolotl)
	if typeID < 0 {
		typeID = EntityTypeAxolotlID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeAxolotl,
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
	mem.health = DefaultAxolotlMaxHealth
	mem.lastHealth = DefaultAxolotlMaxHealth
	if isBaby {
		mem.ageTicks = BabyMobGrowthTicks
	}
	tr.mu.Unlock()

	st := s.ensureAxolotlState(id, variant)
	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		if st != nil {
			s.broadcastAxolotlState(ent, *st)
		}
		if isBaby {
			s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
				EncodeAgeableMobDataPacket(id, true))
		}
	}
	return id, nil
}

// TriggerAxolotlPlayDead puts the Axolotl with entityID into the Playing Dead
// state for 200 ticks (10 seconds), applying Regeneration I (`EffectRegeneration`)
// and broadcasting `DATA_PLAYING_DEAD = true`.
func (s *Server) TriggerAxolotlPlayDead(entityID int32) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeAxolotl {
		return false
	}
	st := s.ensureAxolotlState(entityID, AxolotlVariantLucy)
	mgr := s.axolotlManager()
	mgr.mu.Lock()
	st.PlayingDead = true
	st.PlayingDeadTicks = AxolotlPlayDeadDurationTicks
	snap := *st
	mgr.mu.Unlock()

	s.ApplyEntityEffectScaled(entityID, NewStatusEffectInstance(EffectRegeneration, int32(AxolotlPlayDeadDurationTicks), 0), 1.0)
	s.broadcastAxolotlState(ent, snap)
	return true
}

// IsAxolotlInWater reports whether the Axolotl with entityID is currently in water.
func (s *Server) IsAxolotlInWater(entityID int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	cache := s.DimensionChunks(ent.EffectiveDimension())
	if cache == nil {
		return true
	}
	bx := int(math.Floor(ent.X))
	by := int(math.Floor(ent.Y))
	bz := int(math.Floor(ent.Z))
	return cache.GetBlock(bx, by, bz) == world.StateWater ||
		cache.GetBlock(bx, by-1, bz) == world.StateWater ||
		cache.FluidHeightAt(bx, by, bz) > 0
}

// OnAxolotlDamaged is invoked when an Axolotl takes damage; in vanilla 26.1.2,
// if the Axolotl is damaged in water and drops below 50% HP, takes > 2.0 damage,
// or passes a 1-in-3 random roll, it plays dead for 200 ticks and regenerates health.
func (s *Server) OnAxolotlDamaged(entityID int32, damage float32, rng *rand.Rand) bool {
	if s == nil || s.entities == nil || damage <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeAxolotl {
		return false
	}
	curHP := s.MobHealth(entityID)
	if curHP <= 0 {
		return false
	}
	if !s.IsAxolotlInWater(entityID) {
		return false
	}
	shouldPlayDead := curHP <= DefaultAxolotlMaxHealth*0.5 || damage > 2.0 || damage >= curHP
	if !shouldPlayDead {
		if rng == nil || rng.Intn(3) == 0 {
			shouldPlayDead = true
		}
	}
	if !shouldPlayDead {
		return false
	}
	return s.TriggerAxolotlPlayDead(entityID)
}

// GrantAxolotlPlayerSupport applies the vanilla Axolotl combat-assistance reward
// to player:
//   - Grants Regeneration I (`EffectRegeneration`) for 100 ticks (+100 ticks if
//     already active, capped at 2400 ticks / 2 minutes)
//   - Removes Mining Fatigue (`EffectMiningFatigue`) from the player!
func (s *Server) GrantAxolotlPlayerSupport(axolotlID int32, player *PlayerSession) bool {
	if s == nil || player == nil || player.IsDead() {
		return false
	}
	dur := AxolotlPlayerRegenBaseTicks
	if existing, ok := player.PlayerEffect(EffectRegeneration); ok && existing.Duration > 0 {
		dur += existing.Duration
		if dur > AxolotlPlayerRegenMaxTicks {
			dur = AxolotlPlayerRegenMaxTicks
		}
	}
	s.ApplyPlayerEffect(player, NewStatusEffectInstance(EffectRegeneration, dur, 0))
	s.RemovePlayerEffect(player, EffectMiningFatigue)
	s.GrantAdvancement(player, "minecraft:husbandry/kill_axolotl_target")
	return true
}

// OnMobKilledNearAxolotls checks whether any Axolotl within 20 blocks was
// actively fighting mobID when killer slew it; if so, grants the Axolotl combat
// assistance reward (Regeneration I + removal of Mining Fatigue) to killer.
func (s *Server) OnMobKilledNearAxolotls(mobID int32, killer *PlayerSession) bool {
	if s == nil || s.entities == nil || mobID <= 0 || killer == nil || killer.IsDead() {
		return false
	}
	ps := killer.Snapshot()
	dim := world.NormalizeDimension(ps.Dimension)
	maxDistSq := AxolotlPlayerSupportRange * AxolotlPlayerSupportRange
	mgr := s.axolotlManager()

	var rewarded bool
	for _, ent := range s.entities.AppendAll(nil) {
		if canonicalResourceName(ent.TypeName) != MobTypeAxolotl || ent.EffectiveDimension() != dim {
			continue
		}
		dx := ps.X - ent.X
		dy := ps.Y - ent.Y
		dz := ps.Z - ent.Z
		if dx*dx+dy*dy+dz*dz > maxDistSq {
			continue
		}
		mgr.mu.Lock()
		st := mgr.axolotls[ent.ID]
		wasTargeting := st != nil && st.LastTargetedMobID == mobID
		if wasTargeting {
			st.LastTargetedMobID = 0
		}
		mgr.mu.Unlock()
		if wasTargeting {
			if s.GrantAxolotlPlayerSupport(ent.ID, killer) {
				rewarded = true
			}
		}
	}
	return rewarded
}

// AxolotlAttackAquaticMob performs an Axolotl melee attack (2.0 damage) against
// an aquatic target entity (`squid`, `glow_squid`, `cod`, `salmon`, `tropical_fish`,
// `pufferfish`, `tadpole`, `drowned`, `guardian`, `elder_guardian`).
// If a hostile aquatic mob is killed while a player is within 20 blocks, grants
// Regeneration I and removes Mining Fatigue on that player!
func (s *Server) AxolotlAttackAquaticMob(axolotlID, targetEntityID int32) (dealt float32, killed bool) {
	if s == nil || s.entities == nil || axolotlID <= 0 || targetEntityID <= 0 {
		return 0, false
	}
	axEnt := s.entities.Get(axolotlID)
	targetEnt := s.entities.Get(targetEntityID)
	if axEnt == nil || targetEnt == nil || canonicalResourceName(axEnt.TypeName) != MobTypeAxolotl {
		return 0, false
	}
	isPassive := IsAxolotlPassivePrey(targetEnt.TypeName)
	isHostile := IsAxolotlHostileAquaticTarget(targetEnt.TypeName)
	if !isPassive && !isHostile {
		return 0, false
	}

	st := s.ensureAxolotlState(axolotlID, AxolotlVariantLucy)
	mgr := s.axolotlManager()
	mgr.mu.Lock()
	if st.PlayingDead || (isPassive && st.PassiveHuntCooldown > 0) {
		mgr.mu.Unlock()
		return 0, false
	}
	st.LastTargetedMobID = targetEntityID
	st.AttackCooldown = AxolotlAttackCooldownTicks
	mgr.mu.Unlock()

	dim := axEnt.EffectiveDimension()
	cp := axEnt.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundAxolotlAttack, SoundSourceNeutral, axEnt.X, axEnt.Y, axEnt.Z, 1.0, 1.0, 0))

	killed = s.applyDamageToMob(targetEnt, AxolotlAttackDamage, nil, 0.3, false, true)
	if killed {
		if isPassive {
			mgr.mu.Lock()
			st.PassiveHuntCooldown = AxolotlPassiveHuntCooldownTicks
			mgr.mu.Unlock()
		}
		if isHostile {
			// Reward nearby players within 20 blocks with Regeneration I and Mining Fatigue removal.
			maxDistSq := AxolotlPlayerSupportRange * AxolotlPlayerSupportRange
			for _, sess := range s.onlinePlayersSnapshot() {
				if sess == nil || sess.IsDead() {
					continue
				}
				ps := sess.Snapshot()
				if world.NormalizeDimension(ps.Dimension) != dim {
					continue
				}
				dx := ps.X - axEnt.X
				dy := ps.Y - axEnt.Y
				dz := ps.Z - axEnt.Z
				if dx*dx+dy*dy+dz*dz <= maxDistSq {
					s.GrantAxolotlPlayerSupport(axolotlID, sess)
				}
			}
		}
	}
	return AxolotlAttackDamage, killed
}

// CatchAxolotlInBucket scoops up the `minecraft:axolotl` with entityID when
// session right-clicks it while holding `minecraft:water_bucket`, replacing the
// water bucket with `minecraft:axolotl_bucket` and preserving its variant/age/health.
func (s *Server) CatchAxolotlInBucket(session *PlayerSession, entityID int32) bool {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeAxolotl {
		return false
	}
	held := session.HeldItem()
	if held.IsEmpty() || canonicalResourceName(held.ResolvedID()) != WaterBucketItemID {
		return false
	}
	axBucket := world.NewNamedItemStack(AxolotlBucketItemID, 1)
	if axBucket.IsEmpty() {
		return false
	}

	st := s.ensureAxolotlState(entityID, AxolotlVariantLucy)
	age := s.MobAge(entityID)
	hp := s.MobHealth(entityID)
	mgr := s.axolotlManager()
	mgr.mu.Lock()
	mgr.bucketedMap[session] = bucketedAxolotlInfo{
		Variant:  st.Variant,
		AgeTicks: age,
		Health:   hp,
	}
	delete(mgr.axolotls, entityID)
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	x, y, z := ent.X, ent.Y, ent.Z
	s.entities.Remove(entityID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{entityID}))

	slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
	if snap.GameMode != GameModeCreative {
		if held.Count <= 1 {
			session.SetInventorySlot(slotIdx, axBucket)
		} else {
			session.SetInventorySlot(slotIdx, world.NewItemStack(held.ResolvedItemID(), held.Count-1))
			_, _ = session.AddItemToInventory(axBucket)
		}
	} else {
		_, _ = session.AddItemToInventory(axBucket)
	}
	s.TriggerItemAcquired(session, axBucket)

	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundBucketFillAxolotl, SoundSourceNeutral, x, y, z, 1.0, 1.0, 0))
	return true
}

// ReleaseAxolotlFromBucket empties a `minecraft:axolotl_bucket` held by session
// at block (x, y, z) in dimension: places water, spawns the `minecraft:axolotl`
// with its preserved variant/age/health and FromBucket=true, and replaces the
// bucket with `minecraft:bucket` in survival mode.
// (Can be called directly by Agent B's bucket block-placement handler.)
func (s *Server) ReleaseAxolotlFromBucket(session *PlayerSession, dimension string, x, y, z int) (axolotlID int32, ok bool) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return 0, false
	}
	snap := session.Snapshot()
	held := session.HeldItem()
	if held.IsEmpty() || canonicalResourceName(held.ResolvedID()) != AxolotlBucketItemID {
		return 0, false
	}
	normDim := world.NormalizeDimension(dimension)
	if cache := s.DimensionChunks(normDim); cache != nil && y >= world.MinY && y < world.MinY+world.WorldHeight {
		cache.SetBlock(x, y, z, world.StateWater)
		s.broadcastMobBlockUpdate(normDim, x, y, z, world.StateWater)
	}

	mgr := s.axolotlManager()
	mgr.mu.Lock()
	saved, hasSaved := mgr.bucketedMap[session]
	delete(mgr.bucketedMap, session)
	mgr.mu.Unlock()

	variant := AxolotlVariantLucy
	isBaby := false
	if hasSaved {
		variant = saved.Variant
		isBaby = saved.AgeTicks < 0
	}
	aid, err := s.SpawnAxolotl(normDim, float64(x)+0.5, float64(y), float64(z)+0.5, variant, isBaby)
	if err != nil || aid <= 0 {
		return 0, false
	}
	if hasSaved {
		if saved.AgeTicks < 0 {
			s.SetMobAge(aid, saved.AgeTicks)
		}
		if saved.Health > 0 {
			s.SetMobHealth(aid, saved.Health)
		}
	}
	st := s.ensureAxolotlState(aid, variant)
	mgr.mu.Lock()
	st.FromBucket = true
	snapSt := *st
	mgr.mu.Unlock()
	if ent := s.entities.Get(aid); ent != nil {
		s.broadcastAxolotlState(ent, snapSt)
	}

	if snap.GameMode != GameModeCreative {
		slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
		session.SetInventorySlot(slotIdx, world.NewNamedItemStack(EmptyBucketItemID, 1))
	}

	cx := int32(x >> 4)
	cz := int32(z >> 4)
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundBucketEmptyAxolotl, SoundSourceNeutral, float64(x)+0.5, float64(y), float64(z)+0.5, 1.0, 1.0, 0))
	return aid, true
}

// InteractWithAxolotl handles a player right-clicking a `minecraft:axolotl`:
//   - Holding `minecraft:water_bucket`         -> scoops up into `minecraft:axolotl_bucket`
//   - Holding `minecraft:tropical_fish_bucket` -> feeds baby or triggers love mode/breeding (returning `minecraft:water_bucket` in survival).
func (s *Server) InteractWithAxolotl(session *PlayerSession, entityID int32) (handled bool, babyAxolotlID int32) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false, 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeAxolotl {
		return false, 0
	}
	dx := ent.X - snap.X
	dy := ent.Y - snap.Y
	dz := ent.Z - snap.Z
	if snap.GameMode != GameModeCreative && (dx*dx+dy*dy+dz*dz) > MaxMeleeAttackReach*MaxMeleeAttackReach {
		return false, 0
	}
	held := session.HeldItem()
	if held.IsEmpty() {
		return false, 0
	}
	heldID := canonicalResourceName(held.ResolvedID())
	if heldID == WaterBucketItemID {
		return s.CatchAxolotlInBucket(session, entityID), 0
	}
	if heldID != TropicalFishBucketItemID {
		return false, 0
	}

	// Replace tropical_fish_bucket with water_bucket in survival helper.
	replaceWithWaterBucket := func() {
		if snap.GameMode != GameModeCreative {
			slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
			wb := world.NewNamedItemStack(WaterBucketItemID, 1)
			if held.Count <= 1 {
				session.SetInventorySlot(slotIdx, wb)
			} else {
				session.SetInventorySlot(slotIdx, world.NewItemStack(held.ResolvedItemID(), held.Count-1))
				_, _ = session.AddItemToInventory(wb)
			}
		}
	}

	if s.IsMobBaby(entityID) {
		tr := s.entityTracker()
		tr.mu.Lock()
		mem := tr.mobStateFor(entityID, tr.aiTickID)
		delta := (-mem.ageTicks) / 10
		if delta < 1 {
			delta = 1
		}
		mem.ageTicks += delta
		if mem.ageTicks > 0 {
			mem.ageTicks = 0
		}
		tr.mu.Unlock()
		replaceWithWaterBucket()
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayEntityEvent,
			EncodeEntityEventPacket(entityID, EntityEventVillagerHappy))
		return true, 0
	}

	if s.MobBreedCooldown(entityID) > 0 || s.MobInLoveTicks(entityID) > 0 {
		return false, 0
	}
	if !s.SetMobInLove(entityID, AnimalLoveDurationTicks, session) {
		return false, 0
	}
	replaceWithWaterBucket()

	stA, _ := s.GetAxolotlState(entityID)
	parentBVariant := stA.Variant
	for _, other := range s.entities.AppendAll(nil) {
		if other.ID == entityID || canonicalResourceName(other.TypeName) != MobTypeAxolotl || other.EffectiveDimension() != ent.EffectiveDimension() {
			continue
		}
		odx := other.X - ent.X
		ody := other.Y - ent.Y
		odz := other.Z - ent.Z
		if odx*odx+ody*ody+odz*odz <= AnimalBreedRange*AnimalBreedRange && s.MobInLoveTicks(other.ID) > 0 {
			if stB, ok := s.GetAxolotlState(other.ID); ok {
				parentBVariant = stB.Variant
			}
			break
		}
	}
	babyID, bred := s.TryBreedMobWithNearbyMate(entityID, AnimalBreedRange, nil)
	if bred && babyID > 0 {
		// Assign inherited or rare-blue variant to the baby Axolotl.
		childVar := RollAxolotlOffspringVariant(stA.Variant, parentBVariant, nil)
		childSt := s.ensureAxolotlState(babyID, childVar)
		mgr := s.axolotlManager()
		mgr.mu.Lock()
		childSt.Variant = childVar
		snapChild := *childSt
		mgr.mu.Unlock()
		if childEnt := s.entities.Get(babyID); childEnt != nil {
			s.broadcastAxolotlState(childEnt, snapChild)
		}
		return true, babyID
	}
	return true, 0
}

// TickAxolotls advances all `minecraft:axolotl` entities by one server tick:
//   - Playing dead countdown & health regeneration
//   - Passive hunting cooldown & attack cooldown
//   - Hunting nearby hostile and passive aquatic mobs.
func (s *Server) TickAxolotls(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	allEnts := s.entities.AppendAll(nil)
	mgr := s.axolotlManager()

	for i := range allEnts {
		ent := &allEnts[i]
		if canonicalResourceName(ent.TypeName) != MobTypeAxolotl {
			continue
		}
		st := s.ensureAxolotlState(ent.ID, AxolotlVariantLucy)
		if st == nil {
			continue
		}
		mgr.mu.Lock()
		if st.PassiveHuntCooldown > 0 {
			st.PassiveHuntCooldown--
		}
		if st.AttackCooldown > 0 {
			st.AttackCooldown--
		}
		if st.PlayingDead {
			st.PlayingDeadTicks--
			// Heal +0.5 HP every 25 ticks while playing dead.
			healNow := st.PlayingDeadTicks%25 == 0
			wokeUp := st.PlayingDeadTicks <= 0
			if wokeUp {
				st.PlayingDead = false
				st.PlayingDeadTicks = 0
			}
			snap := *st
			mgr.mu.Unlock()
			if healNow {
				curHP := s.MobHealth(ent.ID)
				if curHP > 0 && curHP < DefaultAxolotlMaxHealth {
					nextHP := curHP + 1.0
					if nextHP > DefaultAxolotlMaxHealth {
						nextHP = DefaultAxolotlMaxHealth
					}
					s.SetMobHealth(ent.ID, nextHP)
				}
			}
			if wokeUp {
				s.broadcastAxolotlState(ent, snap)
			}
			continue
		}
		canAttack := st.AttackCooldown <= 0
		canHuntPassive := st.PassiveHuntCooldown <= 0
		inWater := s.IsAxolotlInWater(ent.ID)
		if inWater {
			st.OutOfWaterTicks = 0
		} else {
			st.OutOfWaterTicks++
		}
		outTicks := st.OutOfWaterTicks
		mgr.mu.Unlock()

		if !inWater && outTicks > AxolotlMaxOutOfWaterTicks && outTicks%20 == 0 && s.MobHealth(ent.ID) > 1.0 {
			_ = s.applyDamageToMob(ent, 1.0, nil, 0, false, true)
		}

		if !canAttack {
			continue
		}

		// Search for hostile aquatic mobs (priority) or passive aquatic prey within AxolotlHuntRange.
		dim := ent.EffectiveDimension()
		var bestTarget *world.Entity
		bestDistSq := AxolotlHuntRange * AxolotlHuntRange
		bestIsHostile := false
		for j := range allEnts {
			cand := &allEnts[j]
			if cand.ID == ent.ID || cand.EffectiveDimension() != dim {
				continue
			}
			isHostile := IsAxolotlHostileAquaticTarget(cand.TypeName)
			isPassive := IsAxolotlPassivePrey(cand.TypeName)
			if !isHostile && (!isPassive || !canHuntPassive) {
				continue
			}
			dx := cand.X - ent.X
			dy := cand.Y - ent.Y
			dz := cand.Z - ent.Z
			dSq := dx*dx + dy*dy + dz*dz
			if dSq > AxolotlHuntRange*AxolotlHuntRange {
				continue
			}
			if isHostile && !bestIsHostile {
				bestIsHostile = true
				bestDistSq = dSq
				bestTarget = cand
			} else if isHostile == bestIsHostile && dSq < bestDistSq {
				bestDistSq = dSq
				bestTarget = cand
			}
		}

		if bestTarget != nil {
			dist := math.Sqrt(bestDistSq)
			if dist <= AxolotlMeleeReach {
				_, _ = s.AxolotlAttackAquaticMob(ent.ID, bestTarget.ID)
			} else {
				dx := bestTarget.X - ent.X
				dy := bestTarget.Y - ent.Y
				dz := bestTarget.Z - ent.Z
				nx := ent.X + (dx/dist)*AxolotlSwimSpeed
				ny := ent.Y + (dy/dist)*AxolotlSwimSpeed
				nz := ent.Z + (dz/dist)*AxolotlSwimSpeed
				yaw := yawToward(dx, dz)
				s.entities.Update(ent.ID, func(e *world.Entity) {
					e.X = nx
					e.Y = ny
					e.Z = nz
					e.Yaw = yaw
					e.HeadYaw = yaw
				})
			}
			continue
		}

		// Follow nearby player holding tropical_fish_bucket within 10 blocks.
		for _, sess := range s.onlinePlayersSnapshot() {
			if sess == nil || sess.IsDead() {
				continue
			}
			ps := sess.Snapshot()
			if world.NormalizeDimension(ps.Dimension) != dim || ps.GameMode == GameModeSpectator {
				continue
			}
			held := sess.HeldItem()
			if held.IsEmpty() || canonicalResourceName(held.ResolvedID()) != TropicalFishBucketItemID {
				continue
			}
			dx := ps.X - ent.X
			dy := ps.Y - ent.Y
			dz := ps.Z - ent.Z
			dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
			if dist > 1.8 && dist <= 10.0 {
				nx := ent.X + (dx/dist)*AxolotlSwimSpeed
				ny := ent.Y + (dy/dist)*AxolotlSwimSpeed
				nz := ent.Z + (dz/dist)*AxolotlSwimSpeed
				yaw := yawToward(dx, dz)
				s.entities.Update(ent.ID, func(e *world.Entity) {
					e.X = nx
					e.Y = ny
					e.Z = nz
					e.Yaw = yaw
					e.HeadYaw = yaw
				})
				break
			}
		}
	}
}
