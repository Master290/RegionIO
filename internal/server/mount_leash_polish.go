package server

import (
	"errors"
	"math"
	"math/rand"
	"strings"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Mounts & Leash constants for Minecraft 26.1.2 (protocol 775).
const (
	LeadItemID          = "minecraft:lead"
	EntityTypeLeashKnot = "minecraft:leash_knot"
	EntityTypeLeashKnotID = 76

	// Vanilla 26.1.2 leash distance thresholds (`Leashable`).
	LeashElasticPullDistance float64 = 6.0  // Beyond 6 blocks, elastic pull velocity draws the mob toward the holder
	LeashMaxFenceTieDistance float64 = 7.0  // Max distance from a fence to tie a player-held leashed mob
	LeashSnapDistance        float64 = 10.0 // Beyond 10 blocks, the lead snaps and drops 1x minecraft:lead
	LeashPullSpeed           float64 = 0.22

	// Canonical Leash Knot sounds in 26.1.2.
	SoundLeashKnotPlace = "minecraft:entity.leash_knot.place"
	SoundLeashKnotBreak = "minecraft:entity.leash_knot.break"

	// Llama wolf-spit aggro range in 26.1.2.
	LlamaWolfAggroRange float64 = 16.0

	// Camel front/back rider longitudinal seat offsets along the Camel's facing direction.
	CamelFrontSeatForwardOffset float64 = 0.50
	CamelBackSeatForwardOffset  float64 = -0.60
)

// LeashState stores the active lead/leash attachment for a leashed entity.
type LeashState struct {
	EntityID       int32  `json:"entity_id"`
	HolderEntityID int32  `json:"holder_entity_id"`
	HasFenceKnot   bool   `json:"has_fence_knot"`
	FenceX         int    `json:"fence_x"`
	FenceY         int    `json:"fence_y"`
	FenceZ         int    `json:"fence_z"`
	Dimension      string `json:"dimension"`
}

// LeashKnotState tracks a `minecraft:leash_knot` entity attached to a fence block.
type LeashKnotState struct {
	KnotEntityID int32
	Dimension    string
	FenceX       int
	FenceY       int
	FenceZ       int
}

type serverLeashManager struct {
	mu          sync.RWMutex
	leashed     map[int32]*LeashState     // leashedEntityID -> *LeashState
	knots       map[int32]*LeashKnotState // knotEntityID -> *LeashKnotState
	horseArmors map[int32]ItemMeta        // horseEntityID -> equipped horse armor ItemMeta (dye color / enchantments)
}

var leashManagers sync.Map // map[*Server]*serverLeashManager

func (s *Server) leashManager() *serverLeashManager {
	if s == nil {
		return &serverLeashManager{
			leashed:     make(map[int32]*LeashState),
			knots:       make(map[int32]*LeashKnotState),
			horseArmors: make(map[int32]ItemMeta),
		}
	}
	if v, ok := leashManagers.Load(s); ok {
		return v.(*serverLeashManager)
	}
	mgr := &serverLeashManager{
		leashed:     make(map[int32]*LeashState),
		knots:       make(map[int32]*LeashKnotState),
		horseArmors: make(map[int32]ItemMeta),
	}
	actual, _ := leashManagers.LoadOrStore(s, mgr)
	return actual.(*serverLeashManager)
}

// --- 1. Leash / Lead & Leash Knot Protocol & Mechanics (vanilla 26.1.2) ---

// EncodeSetEntityLinkPacket encodes a ClientboundSetEntityLinkPacket (`0x64` in
// protocol 775): two big-endian int32 values `(attachedEntityID, destHolderEntityID)`.
// Setting `destHolderEntityID = 0` detaches the leash on the client.
func EncodeSetEntityLinkPacket(attachedEntityID, destHolderEntityID int32) []byte {
	w := protocol.NewWriter(8)
	w.Int32(attachedEntityID)
	w.Int32(destHolderEntityID)
	return w.Bytes()
}

// DecodeSetEntityLinkPacket decodes a ClientboundSetEntityLinkPacket (`0x64`).
func DecodeSetEntityLinkPacket(r *protocol.Reader) (attachedEntityID, destHolderEntityID int32, err error) {
	if r == nil {
		return 0, 0, errors.New("leash: nil reader")
	}
	attachedEntityID, err = r.Int32()
	if err != nil {
		return 0, 0, err
	}
	destHolderEntityID, err = r.Int32()
	if err != nil {
		return 0, 0, err
	}
	return attachedEntityID, destHolderEntityID, nil
}

// IsFenceBlockForLeash reports whether blockName is a fence block that accepts
// a `minecraft:leash_knot` in vanilla 26.1.2 (`#minecraft:fences`, excluding
// fence gates and walls).
func IsFenceBlockForLeash(blockName string) bool {
	norm := canonicalResourceName(blockName)
	return strings.HasSuffix(norm, "_fence")
}

// CanMobTypeBeLeashed reports whether a mob or boat/raft of typeName can be
// attached to a `minecraft:lead` in vanilla 26.1.2 (`Leashable.canBeLeashed`).
// Hostile monsters, bosses, villagers, wandering traders, bats, pandas, and
// turtles cannot be leashed; passive/neutral animals, mounts, golems, allays,
// dolphins, squids, axolotls, frogs, tadpoles, hoglins, zoglin, striders, and
// boats/rafts can be leashed.
func CanMobTypeBeLeashed(typeName string) bool {
	norm := canonicalResourceName(typeName)
	if isBoatOrRaftEntityType(norm) {
		return true
	}
	switch norm {
	case MobTypeHorse, MobTypeDonkey, MobTypeMule,
		MobTypeSkeletonHorse, MobTypeZombieHorse,
		MobTypeLlama, MobTypeTraderLlama,
		MobTypeCamel, MobTypePig, MobTypeCow, "minecraft:mooshroom",
		MobTypeSheep, MobTypeChicken, "minecraft:rabbit",
		MobTypeWolf, MobTypeCat, MobTypeOcelot, MobTypeParrot, MobTypeFox,
		MobTypeBee, MobTypeGoat, MobTypeArmadillo, MobTypeSniffer, MobTypeAllay,
		MobTypeFrog, MobTypeTadpole, MobTypeAxolotl,
		MobTypeDolphin, MobTypeSquid, MobTypeGlowSquid,
		MobTypeIronGolem, MobTypeSnowGolem, MobTypeCopperGolem,
		MobTypeStrider, MobTypeHoglin, MobTypeZoglin,
		"minecraft:polar_bear":
		return true
	default:
		return false
	}
}

// CanEntityBeLeashed reports whether entityID can currently be leashed in
// vanilla 26.1.2:
//   - The entity must exist and its type must satisfy `CanMobTypeBeLeashed`.
//   - Angry wolves (`AngerTicks > 0`) cannot be leashed.
//   - Mounts/vehicles currently carrying a rider/passenger cannot be leashed.
func (s *Server) CanEntityBeLeashed(entityID int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !CanMobTypeBeLeashed(ent.TypeName) {
		return false
	}
	norm := canonicalResourceName(ent.TypeName)
	if norm == MobTypeWolf {
		if tst, ok := s.GetTamedMobState(entityID); ok && tst.AngerTicks > 0 {
			return false
		}
	}
	if s.EquineRider(entityID) > 0 || len(s.CamelPassengers(entityID)) > 0 {
		return false
	}
	if st, ok := s.GetStriderState(entityID); ok && st.RiderEntityID > 0 {
		return false
	}
	return true
}

// IsEntityLeashed reports whether entityID is currently attached to a lead.
func (s *Server) IsEntityLeashed(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	mgr := s.leashManager()
	mgr.mu.RLock()
	st, ok := mgr.leashed[entityID]
	mgr.mu.RUnlock()
	return ok && st != nil && st.HolderEntityID > 0
}

// GetEntityLeashState returns a snapshot of the LeashState for entityID.
func (s *Server) GetEntityLeashState(entityID int32) (LeashState, bool) {
	if s == nil || entityID <= 0 {
		return LeashState{}, false
	}
	mgr := s.leashManager()
	mgr.mu.RLock()
	st, ok := mgr.leashed[entityID]
	var out LeashState
	if ok && st != nil {
		out = *st
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// LeashHolderID returns the entity ID holding entityID's lead (player,
// wandering_trader, or leash_knot), or 0 if not leashed.
func (s *Server) LeashHolderID(entityID int32) int32 {
	st, ok := s.GetEntityLeashState(entityID)
	if !ok {
		return 0
	}
	return st.HolderEntityID
}

// MobsLeashedToHolder returns all entity IDs currently leashed to holderEntityID.
func (s *Server) MobsLeashedToHolder(holderEntityID int32) []int32 {
	if s == nil || holderEntityID <= 0 {
		return nil
	}
	mgr := s.leashManager()
	mgr.mu.RLock()
	var ids []int32
	for id, st := range mgr.leashed {
		if st != nil && st.HolderEntityID == holderEntityID {
			ids = append(ids, id)
		}
	}
	mgr.mu.RUnlock()
	return ids
}

// LeashMobToEntity attaches mobEntityID's lead to holderEntityID (a player,
// `minecraft:wandering_trader`, or `minecraft:leash_knot`), broadcasting
// `ClientboundSetEntityLinkPacket` (0x64) and automatically forming a llama
// caravan when a llama or trader_llama is leashed.
func (s *Server) LeashMobToEntity(mobEntityID, holderEntityID int32) bool {
	if s == nil || s.entities == nil || mobEntityID <= 0 || holderEntityID <= 0 || mobEntityID == holderEntityID {
		return false
	}
	if !s.CanEntityBeLeashed(mobEntityID) {
		return false
	}
	ent := s.entities.Get(mobEntityID)
	if ent == nil {
		return false
	}
	dim := ent.EffectiveDimension()
	mgr := s.leashManager()
	mgr.mu.Lock()
	knot, isKnot := mgr.knots[holderEntityID]
	ls := &LeashState{
		EntityID:       mobEntityID,
		HolderEntityID: holderEntityID,
		Dimension:      dim,
	}
	if isKnot && knot != nil {
		ls.HasFenceKnot = true
		ls.FenceX = knot.FenceX
		ls.FenceY = knot.FenceY
		ls.FenceZ = knot.FenceZ
	}
	mgr.leashed[mobEntityID] = ls
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEntityLink,
		EncodeSetEntityLinkPacket(mobEntityID, holderEntityID))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundLeashKnotPlace, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 0.5, 1.0, 0))

	norm := canonicalResourceName(ent.TypeName)
	if norm == MobTypeLlama || norm == MobTypeTraderLlama {
		s.AutoFormLlamaCaravan(mobEntityID)
	}
	return true
}

// LeashMobToPlayer attaches mobEntityID to session using a `minecraft:lead`
// (consuming 1 lead in Survival if session is holding `minecraft:lead`).
func (s *Server) LeashMobToPlayer(session *PlayerSession, mobEntityID int32) bool {
	if s == nil || session == nil || session.IsDead() || mobEntityID <= 0 {
		return false
	}
	if s.IsEntityLeashed(mobEntityID) {
		return false
	}
	if !s.CanEntityBeLeashed(mobEntityID) {
		return false
	}
	held := session.HeldItem()
	hasLeadInHand := !held.IsEmpty() && canonicalResourceName(held.ResolvedID()) == LeadItemID
	if !s.LeashMobToEntity(mobEntityID, session.EntityID) {
		return false
	}
	if hasLeadInHand {
		consumeOneHeldItemInSurvival(session)
	}
	return true
}

// UnleashMob detaches any active lead from mobEntityID, broadcasting
// `ClientboundSetEntityLinkPacket(mobEntityID, 0)`, breaking any llama caravan
// led by mobEntityID, cleaning up an empty `minecraft:leash_knot`, and (when
// dropLead is true) spawning `1x minecraft:lead` item entity.
func (s *Server) UnleashMob(mobEntityID int32, dropLead bool) (droppedItemID int32, ok bool) {
	if s == nil || mobEntityID <= 0 {
		return 0, false
	}
	mgr := s.leashManager()
	mgr.mu.Lock()
	ls, exists := mgr.leashed[mobEntityID]
	if !exists || ls == nil {
		mgr.mu.Unlock()
		return 0, false
	}
	holderID := ls.HolderEntityID
	wasKnot := ls.HasFenceKnot
	delete(mgr.leashed, mobEntityID)

	// Check if any other mob is still attached to the same leash_knot.
	knotStillUsed := false
	if wasKnot && holderID > 0 {
		for _, other := range mgr.leashed {
			if other != nil && other.HolderEntityID == holderID {
				knotStillUsed = true
				break
			}
		}
		if !knotStillUsed {
			delete(mgr.knots, holderID)
		}
	}
	mgr.mu.Unlock()

	s.BreakLlamaCaravanFrom(mobEntityID)

	var ent *world.Entity
	if s.entities != nil {
		ent = s.entities.Get(mobEntityID)
	}
	if ent != nil {
		dim := ent.EffectiveDimension()
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEntityLink,
			EncodeSetEntityLinkPacket(mobEntityID, 0))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundLeashKnotBreak, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 0.5, 1.0, 0))
		if dropLead {
			if leadStack := world.NewNamedItemStack(LeadItemID, 1); !leadStack.IsEmpty() {
				droppedItemID, _ = s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.5, ent.Z, leadStack)
			}
		}
	}

	if wasKnot && !knotStillUsed && holderID > 0 && s.entities != nil {
		if kEnt := s.entities.Get(holderID); kEnt != nil {
			kcp := kEnt.ChunkPos()
			s.entities.Remove(holderID)
			s.BroadcastDimensionChunk(kEnt.EffectiveDimension(), kcp.X, kcp.Z, protocol.PlayRemoveEntities,
				encodeRemoveEntitiesPacket(nil, []int32{holderID}))
		}
	}
	return droppedItemID, true
}

// ShearMobLeash snips the lead on mobEntityID when a player uses `minecraft:shears`
// on a leashed mob in vanilla 26.1.2, dropping `1x minecraft:lead`.
func (s *Server) ShearMobLeash(session *PlayerSession, mobEntityID int32) (droppedItemID int32, ok bool) {
	if s == nil || !s.IsEntityLeashed(mobEntityID) {
		return 0, false
	}
	return s.UnleashMob(mobEntityID, true)
}

// GetOrCreateLeashKnotAt returns the existing `minecraft:leash_knot` entity ID
// at fence block (bx, by, bz) in dimension, or spawns a new one if the block is
// a valid fence (`IsFenceBlockForLeash`).
func (s *Server) GetOrCreateLeashKnotAt(dimension string, bx, by, bz int) (int32, bool) {
	if s == nil || s.entities == nil {
		return 0, false
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	if cache != nil && !IsFenceBlockForLeash(world.BlockName(cache.GetBlock(bx, by, bz))) {
		return 0, false
	}
	mgr := s.leashManager()
	mgr.mu.Lock()
	for id, k := range mgr.knots {
		if k != nil && k.Dimension == normDim && k.FenceX == bx && k.FenceY == by && k.FenceZ == bz {
			if s.entities.Get(id) != nil {
				mgr.mu.Unlock()
				return id, true
			}
			delete(mgr.knots, id)
		}
	}
	mgr.mu.Unlock()

	typeID := ResolveEntityTypeID(EntityTypeLeashKnot)
	if typeID < 0 {
		typeID = EntityTypeLeashKnotID
	}
	kx := float64(bx) + 0.5
	ky := float64(by) + 0.375
	kz := float64(bz) + 0.5
	knotID := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  EntityTypeLeashKnot,
		Dimension: normDim,
		X:         kx,
		Y:         ky,
		Z:         kz,
		OnGround:  true,
	})
	mgr.mu.Lock()
	mgr.knots[knotID] = &LeashKnotState{
		KnotEntityID: knotID,
		Dimension:    normDim,
		FenceX:       bx,
		FenceY:       by,
		FenceZ:       bz,
	}
	mgr.mu.Unlock()

	if ent := s.entities.Get(knotID); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundLeashKnotPlace, SoundSourceBlocks, kx, ky, kz, 1.0, 1.0, 0))
	}
	return knotID, true
}

// TieLeashedMobsToFence transfers all mobs currently leashed to session within
// `LeashMaxFenceTieDistance` (7.0 blocks) of fence block (bx, by, bz) in
// dimension onto a `minecraft:leash_knot` entity on that fence.
func (s *Server) TieLeashedMobsToFence(session *PlayerSession, dimension string, bx, by, bz int) (knotEntityID int32, tiedCount int) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return 0, 0
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	if cache != nil && !IsFenceBlockForLeash(world.BlockName(cache.GetBlock(bx, by, bz))) {
		return 0, 0
	}
	heldMobs := s.MobsLeashedToHolder(session.EntityID)
	if len(heldMobs) == 0 {
		return 0, 0
	}
	fx, fy, fz := float64(bx)+0.5, float64(by)+0.5, float64(bz)+0.5
	maxDistSq := LeashMaxFenceTieDistance * LeashMaxFenceTieDistance

	var eligible []int32
	for _, mid := range heldMobs {
		mEnt := s.entities.Get(mid)
		if mEnt == nil || mEnt.EffectiveDimension() != normDim {
			continue
		}
		dx := mEnt.X - fx
		dy := mEnt.Y - fy
		dz := mEnt.Z - fz
		if dx*dx+dy*dy+dz*dz <= maxDistSq {
			eligible = append(eligible, mid)
		}
	}
	if len(eligible) == 0 {
		return 0, 0
	}
	knotID, ok := s.GetOrCreateLeashKnotAt(normDim, bx, by, bz)
	if !ok {
		return 0, 0
	}
	for _, mid := range eligible {
		if s.LeashMobToEntity(mid, knotID) {
			tiedCount++
		}
	}
	return knotID, tiedCount
}

// InteractWithLeashKnot handles right-clicking an existing `minecraft:leash_knot`:
//   - If session has mobs leashed to themselves within 7 blocks, attaches them
//     to the knot.
//   - Otherwise, detaches all mobs tied to the knot and drops their leads.
func (s *Server) InteractWithLeashKnot(session *PlayerSession, knotEntityID int32) (tiedCount int, unleashedCount int, handled bool) {
	if s == nil || s.entities == nil || session == nil || knotEntityID <= 0 {
		return 0, 0, false
	}
	mgr := s.leashManager()
	mgr.mu.RLock()
	knot, ok := mgr.knots[knotEntityID]
	var kSnap LeashKnotState
	if ok && knot != nil {
		kSnap = *knot
	}
	mgr.mu.RUnlock()
	if !ok {
		return 0, 0, false
	}
	if _, tied := s.TieLeashedMobsToFence(session, kSnap.Dimension, kSnap.FenceX, kSnap.FenceY, kSnap.FenceZ); tied > 0 {
		return tied, 0, true
	}
	unleashed := s.BreakLeashKnot(knotEntityID, true)
	return 0, unleashed, unleashed > 0
}

// BreakLeashKnot destroys `knotEntityID`, unleashing all mobs tied to it and
// (when dropLeads is true) dropping `1x minecraft:lead` per leashed mob.
func (s *Server) BreakLeashKnot(knotEntityID int32, dropLeads bool) int {
	if s == nil || knotEntityID <= 0 {
		return 0
	}
	mobs := s.MobsLeashedToHolder(knotEntityID)
	count := 0
	for _, mid := range mobs {
		if _, ok := s.UnleashMob(mid, dropLeads); ok {
			count++
		}
	}
	mgr := s.leashManager()
	mgr.mu.Lock()
	delete(mgr.knots, knotEntityID)
	mgr.mu.Unlock()
	if s.entities != nil {
		if kEnt := s.entities.Get(knotEntityID); kEnt != nil {
			cp := kEnt.ChunkPos()
			s.entities.Remove(knotEntityID)
			s.BroadcastDimensionChunk(kEnt.EffectiveDimension(), cp.X, cp.Z, protocol.PlayRemoveEntities,
				encodeRemoveEntitiesPacket(nil, []int32{knotEntityID}))
		}
	}
	return count
}

// OnFenceBlockBrokenForLeash checks whether a `minecraft:leash_knot` is attached
// to the broken fence block at (bx, by, bz) in dimension, and if so, breaks the
// knot and drops all attached leads.
func (s *Server) OnFenceBlockBrokenForLeash(dimension string, bx, by, bz int) int {
	if s == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dimension)
	mgr := s.leashManager()
	mgr.mu.RLock()
	var knotID int32
	for id, k := range mgr.knots {
		if k != nil && k.Dimension == normDim && k.FenceX == bx && k.FenceY == by && k.FenceZ == bz {
			knotID = id
			break
		}
	}
	mgr.mu.RUnlock()
	if knotID <= 0 {
		return 0
	}
	return s.BreakLeashKnot(knotID, true)
}

// TickLeashedMobs steps all active leashes by one tick:
//   - Snaps the lead (`UnleashMob(id, true)`) if the holder disappears, changes
//     dimensions, or distance exceeds `LeashSnapDistance` (10.0 blocks).
//   - Applies elastic pull toward the holder when distance exceeds
//     `LeashElasticPullDistance` (6.0 blocks).
func (s *Server) TickLeashedMobs() {
	if s == nil || s.entities == nil {
		return
	}
	mgr := s.leashManager()
	mgr.mu.RLock()
	if len(mgr.leashed) == 0 {
		mgr.mu.RUnlock()
		return
	}
	snapshots := make([]LeashState, 0, len(mgr.leashed))
	for _, ls := range mgr.leashed {
		if ls != nil {
			snapshots = append(snapshots, *ls)
		}
	}
	mgr.mu.RUnlock()

	for _, ls := range snapshots {
		ent := s.entities.Get(ls.EntityID)
		if ent == nil {
			s.UnleashMob(ls.EntityID, false)
			continue
		}
		dim := ent.EffectiveDimension()
		if ls.HasFenceKnot {
			cache := s.DimensionChunks(dim)
			if cache != nil && !IsFenceBlockForLeash(world.BlockName(cache.GetBlock(ls.FenceX, ls.FenceY, ls.FenceZ))) {
				s.UnleashMob(ls.EntityID, true)
				continue
			}
		}

		holderX, holderY, holderZ, holderDim, holderOK := s.resolveLeashHolderPosition(ls.HolderEntityID)
		if !holderOK || holderDim != dim {
			s.UnleashMob(ls.EntityID, true)
			continue
		}

		dx := holderX - ent.X
		dy := holderY - ent.Y
		dz := holderZ - ent.Z
		dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
		if dist > LeashSnapDistance {
			s.UnleashMob(ls.EntityID, true)
			continue
		}
		if dist > LeashElasticPullDistance {
			cache := s.DimensionChunks(dim)
			feetY := int(math.Floor(ent.Y + 0.01))
			s.stepEntityTowardPoint(ent, cache, feetY, holderX, holderY, holderZ, LeashPullSpeed, 2.5)
		}
	}
}

func (s *Server) resolveLeashHolderPosition(holderID int32) (x, y, z float64, dim string, ok bool) {
	if s == nil || holderID <= 0 {
		return 0, 0, 0, "", false
	}
	if sess := s.playerByEntityID(holderID); sess != nil && !sess.IsDead() {
		ps := sess.Snapshot()
		return ps.X, ps.Y, ps.Z, world.NormalizeDimension(ps.Dimension), true
	}
	if s.entities != nil {
		if hEnt := s.entities.Get(holderID); hEnt != nil {
			return hEnt.X, hEnt.Y, hEnt.Z, hEnt.EffectiveDimension(), true
		}
	}
	return 0, 0, 0, "", false
}

// --- 2. Horse Armor Dyeing, Untamed Ride Taming & Llama Caravan Split ---

// EquipHorseArmorWithMeta equips a horse armor stack along with its ItemMeta
// (preserving custom dye RGB on `minecraft:leather_horse_armor` or enchantments).
func (s *Server) EquipHorseArmorWithMeta(entityID int32, armorStack world.ItemStack, meta ItemMeta) bool {
	if !s.EquipHorseArmor(entityID, armorStack) {
		return false
	}
	mgr := s.leashManager()
	mgr.mu.Lock()
	mgr.horseArmors[entityID] = meta.Clone()
	mgr.mu.Unlock()
	return true
}

// GetHorseArmorMeta returns the ItemMeta for the horse armor currently worn by entityID.
func (s *Server) GetHorseArmorMeta(entityID int32) (ItemMeta, bool) {
	if s == nil || entityID <= 0 || s.HorseArmorItem(entityID) == "" {
		return ItemMeta{}, false
	}
	mgr := s.leashManager()
	mgr.mu.RLock()
	meta, ok := mgr.horseArmors[entityID]
	mgr.mu.RUnlock()
	return meta.Clone(), ok
}

// GetHorseArmorDyedRGB returns the 24-bit RGB color of a dyed `minecraft:leather_horse_armor`
// worn by entityID, or (-1, false) if undyed.
func (s *Server) GetHorseArmorDyedRGB(entityID int32) (int32, bool) {
	meta, ok := s.GetHorseArmorMeta(entityID)
	if !ok {
		return -1, false
	}
	return WolfArmorDyedRGB(meta)
}

// DyeEquippedHorseArmor dyes a `minecraft:leather_horse_armor` currently worn by
// entityID using a 0..15 DyeColor index, blending RGB colors via vanilla leather
// dye math (`BlendWolfArmorDyeRGB`).
func (s *Server) DyeEquippedHorseArmor(entityID int32, dyeColorIndex int32) (int32, bool) {
	if s == nil || entityID <= 0 || s.HorseArmorItem(entityID) != "minecraft:leather_horse_armor" {
		return -1, false
	}
	mgr := s.leashManager()
	mgr.mu.Lock()
	meta := mgr.horseArmors[entityID].Clone()
	existingRGB, _ := WolfArmorDyedRGB(meta)
	blended, ok := BlendWolfArmorDyeRGB(existingRGB, dyeColorIndex)
	if !ok {
		mgr.mu.Unlock()
		return existingRGB, false
	}
	setWolfArmorDyedRGBOnMeta(&meta, blended)
	mgr.horseArmors[entityID] = meta
	mgr.mu.Unlock()
	return blended, true
}

// WashEquippedHorseArmor clears any custom dye color from `minecraft:leather_horse_armor`
// worn by entityID (e.g., when washed in a water cauldron).
func (s *Server) WashEquippedHorseArmor(entityID int32) bool {
	if s == nil || entityID <= 0 || s.HorseArmorItem(entityID) != "minecraft:leather_horse_armor" {
		return false
	}
	mgr := s.leashManager()
	mgr.mu.Lock()
	meta, ok := mgr.horseArmors[entityID]
	if !ok {
		mgr.mu.Unlock()
		return false
	}
	if _, dyed := WolfArmorDyedRGB(meta); !dyed {
		mgr.mu.Unlock()
		return false
	}
	setWolfArmorDyedRGBOnMeta(&meta, -1)
	mgr.horseArmors[entityID] = meta
	mgr.mu.Unlock()
	return true
}

// AttemptRideTamingStep performs a vanilla `RunAroundLikeCrazyGoal` taming check
// for a player currently riding an untamed equine or llama:
//   - Rolls `rng.Intn(MaxTemper) < Temper`.
//   - On success: tames the mount (`TameMob(equineID, rider)`) while keeping the
//     rider mounted!
//   - On failure: bucks the rider off (`DismountEquine(equineID)`), sets
//     `RearingTicks = 20`, increases `Temper` by `+5`, and broadcasts
//     `EntityEventTamingFailed` (smoke) + angry sound.
func (s *Server) AttemptRideTamingStep(equineID int32, rng *rand.Rand) (tamed bool, buckedOff bool) {
	if s == nil || s.entities == nil || equineID <= 0 {
		return false, false
	}
	ent := s.entities.Get(equineID)
	if ent == nil || !IsHorseLikeMob(ent.TypeName) {
		return false, false
	}
	riderID := s.EquineRider(equineID)
	rider := s.playerByEntityID(riderID)
	if rider == nil {
		return false, false
	}
	norm := canonicalResourceName(ent.TypeName)
	st := s.ensureTamedMobState(equineID, norm)
	tmgr := s.tamedManager()
	tmgr.mu.Lock()
	if st.Tamed {
		tmgr.mu.Unlock()
		return true, false
	}
	curTemper := st.Temper
	maxTemper := st.MaxTemper
	if maxTemper <= 0 {
		maxTemper = DefaultEquineMaxTemper
	}
	tmgr.mu.Unlock()

	roll := 0
	if rng != nil {
		roll = rng.Intn(maxTemper)
	}
	if curTemper > 0 && roll < curTemper {
		s.TameMob(equineID, rider)
		return true, false
	}

	// Buck the rider off and increase temper by +5.
	tmgr.mu.Lock()
	st.Temper += 5
	if st.Temper > maxTemper {
		st.Temper = maxTemper
	}
	tSnap := *st
	tmgr.mu.Unlock()

	s.DismountEquine(equineID)
	est := s.ensureEquineState(equineID, norm, nil)
	emgr := s.equineManager()
	emgr.mu.Lock()
	est.RearingTicks = 20
	emgr.mu.Unlock()

	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(equineID, EntityEventTamingFailed))
	angrySnd := SoundHorseAngry
	switch norm {
	case MobTypeDonkey:
		angrySnd = SoundDonkeyAngry
	case MobTypeMule:
		angrySnd = SoundMuleAngry
	case MobTypeLlama, MobTypeTraderLlama:
		angrySnd = SoundLlamaAngry
	}
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(angrySnd, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	s.broadcastTamedMobMetadata(ent, tSnap)
	return false, true
}

// BreakLlamaCaravanFrom detaches `llamaID` from any caravan head ahead of it and
// also detaches any follower directly behind `llamaID`.
func (s *Server) BreakLlamaCaravanFrom(llamaID int32) int {
	if s == nil || llamaID <= 0 {
		return 0
	}
	mgr := s.tamedManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	broken := 0
	if st := mgr.mobs[llamaID]; st != nil && st.CaravanHeadID > 0 {
		st.CaravanHeadID = 0
		broken++
	}
	for _, st := range mgr.mobs {
		if st != nil && st.CaravanHeadID == llamaID {
			st.CaravanHeadID = 0
			broken++
		}
	}
	return broken
}

// TickLlamaCaravans validates all active llama caravan links each tick:
//   - If the head of a caravan is no longer leashed (`!s.IsEntityLeashed(rootHeadID)`),
//     or if any follower exceeds `LeashSnapDistance` (10.0 blocks) from the
//     llama ahead of it, unlinks the broken segment.
func (s *Server) TickLlamaCaravans() {
	if s == nil || s.entities == nil {
		return
	}
	mgr := s.tamedManager()
	mgr.mu.RLock()
	var followers [][2]int32
	for id, st := range mgr.mobs {
		if st != nil && st.CaravanHeadID > 0 {
			followers = append(followers, [2]int32{id, st.CaravanHeadID})
		}
	}
	mgr.mu.RUnlock()

	for _, pair := range followers {
		fID, hID := pair[0], pair[1]
		fEnt := s.entities.Get(fID)
		hEnt := s.entities.Get(hID)
		if fEnt == nil || hEnt == nil || fEnt.EffectiveDimension() != hEnt.EffectiveDimension() {
			s.UnlinkLlamaCaravan(fID)
			continue
		}
		dx := hEnt.X - fEnt.X
		dy := hEnt.Y - fEnt.Y
		dz := hEnt.Z - fEnt.Z
		if dx*dx+dy*dy+dz*dz > LeashSnapDistance*LeashSnapDistance {
			s.BreakLlamaCaravanFrom(fID)
		}
	}
}

// ShouldWolfFleeFromLlama reports whether a wild wolf should flee from a llama
// with `llamaStrength` (1..5) in vanilla 26.1.2 (`Llama.WolvesAttackGoal`:
// wolves always flee from strength 4..5 llamas, and flee with probability
// `llamaStrength / 5` from lower-strength llamas).
func ShouldWolfFleeFromLlama(llamaStrength int32, rng *rand.Rand) bool {
	if llamaStrength >= 4 {
		return true
	}
	if llamaStrength <= 0 {
		return false
	}
	if rng == nil {
		return llamaStrength >= 3
	}
	return rng.Intn(5) < int(llamaStrength)
}

// --- 3. Camel Polish (Shear Saddle Removal, Passenger Sync & Death Drops) ---

// ShearCamelSaddle removes an equipped saddle from an unridden adult Camel when
// sheared with `minecraft:shears`, spawning `1x minecraft:saddle` item entity.
func (s *Server) ShearCamelSaddle(session *PlayerSession, camelID int32) (world.ItemStack, bool) {
	if s == nil || s.entities == nil || camelID <= 0 {
		return world.ItemStack{}, false
	}
	if len(s.CamelPassengers(camelID)) > 0 || !s.IsCamelSaddled(camelID) {
		return world.ItemStack{}, false
	}
	ent := s.entities.Get(camelID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCamel {
		return world.ItemStack{}, false
	}
	if !s.SetCamelSaddled(camelID, false) {
		return world.ItemStack{}, false
	}
	dropped := world.NewNamedItemStack(SaddleItemID, 1)
	if !dropped.IsEmpty() {
		s.SpawnItemEntityInDimension(ent.EffectiveDimension(), ent.X, ent.Y+0.5, ent.Z, dropped)
	}
	return dropped, true
}

// OnCamelKilled dismounts any passengers riding camelID, drops `1x minecraft:saddle`
// if the Camel was saddled, and cleans up its CamelBehaviorState.
func (s *Server) OnCamelKilled(camelID int32) []world.ItemStack {
	if s == nil || camelID <= 0 {
		return nil
	}
	var drops []world.ItemStack
	mgr := s.camelManager()
	mgr.mu.Lock()
	st := mgr.camels[camelID]
	delete(mgr.camels, camelID)
	mgr.mu.Unlock()
	if st != nil && st.Saddled {
		if saddle := world.NewNamedItemStack(SaddleItemID, 1); !saddle.IsEmpty() {
			drops = append(drops, saddle)
		}
	}
	return drops
}

func (s *Server) syncCamelPassengers(ent *world.Entity, snap CamelBehaviorState) {
	if s == nil || s.entities == nil || ent == nil || len(snap.Passengers) == 0 {
		return
	}
	rad := float64(ent.Yaw) * (math.Pi / 180.0)
	sinYaw := -math.Sin(rad)
	cosYaw := math.Cos(rad)
	seatY := ent.Y + 1.45
	if snap.Sitting {
		seatY = ent.Y + 0.85
	}
	for i, pid := range snap.Passengers {
		forward := CamelFrontSeatForwardOffset
		if i > 0 {
			forward = CamelBackSeatForwardOffset
		}
		px := ent.X + sinYaw*forward
		pz := ent.Z + cosYaw*forward
		if rEnt := s.entities.Get(pid); rEnt != nil {
			s.entities.Update(pid, func(e *world.Entity) {
				e.X = px
				e.Y = seatY
				e.Z = pz
			})
		}
	}
}
