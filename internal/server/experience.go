package server

import (
	"encoding/binary"
	"errors"
	"math"
	"math/rand"
	"sort"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// Clientbound set_experience packet ID in Minecraft 26.1.2 (protocol 775).
const PlaySetExperience int32 = 0x67

// ExperienceOrbEntityTypeName is the canonical entity_type identifier for XP orbs.
const ExperienceOrbEntityTypeName = "minecraft:experience_orb"

// DefaultExperienceOrbTypeID is the vanilla 26.1.2 BuiltInRegistries.ENTITY_TYPE
// numeric ID for minecraft:experience_orb used when not present in the minimal
// built-in table.
const DefaultExperienceOrbTypeID = 44

// ExperienceBottleItemID is the canonical item ID for Bottle o' Enchanting.
const ExperienceBottleItemID = "minecraft:experience_bottle"

// SynchedEntityData index for ExperienceOrb.DATA_VALUE (INT serializer = 1).
const EntityDataExperienceOrbValueID byte = 8

// Experience orb gameplay constants (vanilla 26.1.2 ExperienceOrb).
const (
	ExperienceOrbPickupRadius      float64 = 1.75
	ExperienceOrbAttractRadius     float64 = 8.0
	ExperienceOrbMergeRadius       float64 = 0.5
	ExperienceOrbMaxAgeTicks       int     = 6000
	ExperienceOrbScanIntervalTicks int     = 20
	ExperienceOrbDefaultHealth     int32   = 5
	ExperienceBottleMinXP          int32   = 3
	ExperienceBottleMaxXP          int32   = 11
)

// vanillaOrbValues lists the standard experience orb denominations in
// descending order, matching ExperienceOrb.award in vanilla 26.1.2.
var vanillaOrbValues = [...]int32{
	2477, 1237, 617, 307, 149, 73, 37, 17, 7, 3, 1,
}

type playerXPState struct {
	mu          sync.Mutex
	level       int32
	totalXP     int32
	progress    float32
	enchantSeed int32
}

type serverXPOrbState struct {
	mu     sync.Mutex
	values map[int32]int32 // entityID -> single-orb XP denomination value
	counts map[int32]int32 // entityID -> stacked merge count (>= 1)
	health map[int32]int32 // entityID -> orb health (default 5)
}

var (
	playerXPStates sync.Map // map[*PlayerSession]*playerXPState
	serverXPOrbs   sync.Map // map[*Server]*serverXPOrbState
)

func getPlayerXPState(session *PlayerSession) *playerXPState {
	if session == nil {
		return nil
	}
	if v, ok := playerXPStates.Load(session); ok {
		return v.(*playerXPState)
	}
	seed := initialEnchantmentSeed(session)
	actual, _ := playerXPStates.LoadOrStore(session, &playerXPState{
		enchantSeed: seed,
	})
	return actual.(*playerXPState)
}

func initialEnchantmentSeed(session *PlayerSession) int32 {
	if session == nil {
		return 1
	}
	u := session.Profile.UUID
	h := int32(u[0])<<24 | int32(u[1])<<16 | int32(u[2])<<8 | int32(u[3])
	h ^= int32(u[4])<<24 | int32(u[5])<<16 | int32(u[6])<<8 | int32(u[7])
	h ^= session.EntityID * 0x45d9f3b
	if h == 0 {
		h = 0x13579bdf
	}
	return h
}

func (s *Server) xpOrbState() *serverXPOrbState {
	if v, ok := serverXPOrbs.Load(s); ok {
		return v.(*serverXPOrbState)
	}
	actual, _ := serverXPOrbs.LoadOrStore(s, &serverXPOrbState{
		values: make(map[int32]int32),
		counts: make(map[int32]int32),
		health: make(map[int32]int32),
	})
	return actual.(*serverXPOrbState)
}

// XPForNextLevel returns the experience points required to advance from level
// to level+1, matching vanilla Player.getXpNeededForNextLevel().
func XPForNextLevel(level int32) int32 {
	if level < 0 {
		return 7
	}
	if level >= 30 {
		return 112 + (level-30)*9
	}
	if level >= 15 {
		return 37 + (level-15)*5
	}
	return 7 + level*2
}

// TotalXPForLevel returns the cumulative experience points required to reach
// the start of level from level 0.
func TotalXPForLevel(level int32) int32 {
	if level <= 0 {
		return 0
	}
	if level <= 16 {
		return level*level + 6*level
	}
	if level <= 31 {
		return (5*level*level - 81*level + 720) / 2
	}
	return (9*level*level - 325*level + 4440) / 2
}

// LevelAndProgressFromTotalXP converts a cumulative totalXP value into the
// corresponding experience level, fractional bar progress in [0, 1), and
// leftover XP points within the current level.
func LevelAndProgressFromTotalXP(totalXP int32) (level int32, progress float32, xpInLevel int32) {
	if totalXP <= 0 {
		return 0, 0, 0
	}
	rem := totalXP
	for {
		needed := XPForNextLevel(level)
		if rem < needed {
			return level, float32(rem) / float32(needed), rem
		}
		rem -= needed
		level++
	}
}

// SplitExperienceOrbValues splits a positive totalXP amount into vanilla
// experience orb denominations (2477 down to 1).
func SplitExperienceOrbValues(totalXP int32) []int32 {
	if totalXP <= 0 {
		return nil
	}
	var out []int32
	rem := totalXP
	for rem > 0 {
		step := int32(1)
		for _, v := range vanillaOrbValues {
			if rem >= v {
				step = v
				break
			}
		}
		out = append(out, step)
		rem -= step
	}
	return out
}

// ExperienceOrbValueIcon returns the vanilla ExperienceOrb.getIcon() size
// index (0..10) for an orb with XP value.
func ExperienceOrbValueIcon(value int32) int {
	switch {
	case value >= 2477:
		return 10
	case value >= 1237:
		return 9
	case value >= 617:
		return 8
	case value >= 307:
		return 7
	case value >= 149:
		return 6
	case value >= 73:
		return 5
	case value >= 37:
		return 4
	case value >= 17:
		return 3
	case value >= 7:
		return 2
	case value >= 3:
		return 1
	default:
		return 0
	}
}

// EncodeSetExperience encodes a clientbound set_experience (0x67) packet body.
func EncodeSetExperience(progress float32, level, totalXP int32) []byte {
	if progress < 0 {
		progress = 0
	} else if progress > 1 {
		progress = 1
	}
	w := protocol.NewWriter(16)
	w.Float32(progress)
	w.VarInt(level)
	w.VarInt(totalXP)
	return w.Bytes()
}

// DecodeSetExperience decodes a clientbound set_experience (0x67) packet body.
func DecodeSetExperience(r *protocol.Reader) (progress float32, level, totalXP int32, err error) {
	if r == nil {
		return 0, 0, 0, errors.New("server: nil reader")
	}
	progress, err = r.Float32()
	if err != nil {
		return 0, 0, 0, err
	}
	level, err = r.VarInt()
	if err != nil {
		return progress, 0, 0, err
	}
	totalXP, err = r.VarInt()
	if err != nil {
		return progress, level, 0, err
	}
	return progress, level, totalXP, nil
}

// Experience returns the player's current experience level, cumulative total
// XP, and fractional progress toward the next level.
func (session *PlayerSession) Experience() (level, totalXP int32, progress float32) {
	if session == nil {
		return 0, 0, 0
	}
	st := getPlayerXPState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.level, st.totalXP, st.progress
}

// ExperienceLevel returns the player's current experience level.
func (session *PlayerSession) ExperienceLevel() int32 {
	level, _, _ := session.Experience()
	return level
}

// TotalExperience returns the player's cumulative experience points.
func (session *PlayerSession) TotalExperience() int32 {
	_, totalXP, _ := session.Experience()
	return totalXP
}

// ExperienceProgress returns the player's fractional progress [0, 1) toward the
// next experience level.
func (session *PlayerSession) ExperienceProgress() float32 {
	_, _, progress := session.Experience()
	return progress
}

// EnchantmentSeed returns the player's current deterministic enchantment table seed.
func (session *PlayerSession) EnchantmentSeed() int32 {
	if session == nil {
		return 0
	}
	st := getPlayerXPState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.enchantSeed
}

// SetEnchantmentSeed sets the player's enchantment table seed.
func (session *PlayerSession) SetEnchantmentSeed(seed int32) {
	if session == nil {
		return
	}
	st := getPlayerXPState(session)
	st.mu.Lock()
	if seed == 0 {
		seed = 1
	}
	st.enchantSeed = seed
	st.mu.Unlock()
}

// RollEnchantmentSeed advances the player's enchantment seed after an
// enchantment is performed, matching vanilla Player.onEnchantmentPerformed.
func (session *PlayerSession) RollEnchantmentSeed() int32 {
	if session == nil {
		return 0
	}
	st := getPlayerXPState(session)
	st.mu.Lock()
	next := st.enchantSeed*1664525 + 1013904223
	if next == 0 {
		next = 1
	}
	st.enchantSeed = next
	st.mu.Unlock()
	return next
}

// SendPlayerExperience sends a clientbound set_experience (0x67) packet with
// session's current experience progress, level, and total XP.
func (s *Server) SendPlayerExperience(session *PlayerSession) error {
	if session == nil || session.send == nil {
		return nil
	}
	level, totalXP, progress := session.Experience()
	return session.send(PlaySetExperience, EncodeSetExperience(progress, level, totalXP))
}

func (s *Server) notifyExperienceUpdated(session *PlayerSession, oldLevel, newLevel int32) {
	if s == nil || session == nil {
		return
	}
	s.SyncPlayerStateObjectives(session)
	if newLevel > oldLevel && newLevel/5 > oldLevel/5 {
		snap := session.Snapshot()
		dim := world.NormalizeDimension(snap.Dimension)
		vol := float32(0.75)
		if newLevel < 30 {
			vol = float32(newLevel) / 30.0 * 0.75
		}
		cx := int32(int64(math.Floor(snap.X)) >> 4)
		cz := int32(int64(math.Floor(snap.Z)) >> 4)
		s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:entity.player.levelup", SoundSourcePlayers, snap.X, snap.Y, snap.Z, vol, 1.0, 0))
	}
}

// SetPlayerExperience sets session's experience state explicitly and sends
// set_experience to the client.
func (s *Server) SetPlayerExperience(session *PlayerSession, level, totalXP int32, progress float32) error {
	if session == nil {
		return errors.New("server: nil player session")
	}
	if level < 0 {
		level = 0
	}
	if totalXP < 0 {
		totalXP = 0
	}
	if progress < 0 {
		progress = 0
	} else if progress >= 1 {
		progress = 0.9999
	}
	st := getPlayerXPState(session)
	st.mu.Lock()
	oldLevel := st.level
	st.level = level
	st.totalXP = totalXP
	st.progress = progress
	st.mu.Unlock()
	s.notifyExperienceUpdated(session, oldLevel, level)
	return s.SendPlayerExperience(session)
}

// SetPlayerTotalExperience sets the player's total XP, recalculates their
// level and progress bar, and synchronizes the client.
func (s *Server) SetPlayerTotalExperience(session *PlayerSession, totalXP int32) error {
	if session == nil {
		return errors.New("server: nil player session")
	}
	if totalXP < 0 {
		totalXP = 0
	}
	level, progress, _ := LevelAndProgressFromTotalXP(totalXP)
	return s.SetPlayerExperience(session, level, totalXP, progress)
}

// AddPlayerExperience adds (or subtracts) experience points for session.
// Positive XP points first repair any damaged Mending-enchanted equipment
// (2 durability repaired per 1 XP point, matching vanilla ExperienceOrb),
// and the remaining points are added to the player's experience bar.
func (s *Server) AddPlayerExperience(session *PlayerSession, points int32) error {
	if session == nil {
		return errors.New("server: nil player session")
	}
	if points > 0 {
		if s != nil {
			points, _ = s.RepairPlayerMendingItems(session, points)
		} else {
			points = applyMendingOnXP(session, points)
		}
	}
	st := getPlayerXPState(session)
	st.mu.Lock()
	oldLevel := st.level
	newTotal := st.totalXP + points
	if newTotal < 0 {
		newTotal = 0
	}
	level, progress, _ := LevelAndProgressFromTotalXP(newTotal)
	st.totalXP = newTotal
	st.level = level
	st.progress = progress
	st.mu.Unlock()
	s.notifyExperienceUpdated(session, oldLevel, level)
	return s.SendPlayerExperience(session)
}

// AddPlayerExperienceLevels adds (or deducts) whole experience levels while
// preserving the player's fractional progress bar, matching vanilla
// Player.giveExperienceLevels.
func (s *Server) AddPlayerExperienceLevels(session *PlayerSession, deltaLevels int32) error {
	if session == nil {
		return errors.New("server: nil player session")
	}
	st := getPlayerXPState(session)
	st.mu.Lock()
	oldLevel := st.level
	newLevel := st.level + deltaLevels
	progress := st.progress
	if newLevel < 0 {
		newLevel = 0
		progress = 0
	}
	needed := XPForNextLevel(newLevel)
	inLevel := int32(math.Floor(float64(progress) * float64(needed)))
	newTotal := TotalXPForLevel(newLevel) + inLevel
	st.level = newLevel
	st.totalXP = newTotal
	st.progress = progress
	st.mu.Unlock()
	s.notifyExperienceUpdated(session, oldLevel, newLevel)
	return s.SendPlayerExperience(session)
}

// RepairPlayerMendingItems repairs damaged Mending-enchanted items in session's
// 6 equipment slots (MainHand, OffHand, Helmet, Chestplate, Leggings, Boots).
// Only damaged items (Damage > 0) with minecraft:mending are eligible, so
// undamaged Mending items never waste XP rolls (vanilla 26.1.2 ItemStack::isDamaged
// filter). Each 1 XP point repairs up to 2 durability damage. Repaired slots
// are synchronized to the client via PlayContainerSetSlot.
func (s *Server) RepairPlayerMendingItems(session *PlayerSession, points int32) (remainingXP, repairedDurability int32) {
	if session == nil || points <= 0 {
		return points, 0
	}
	snap := session.Snapshot()
	mainSlot := int(snap.SelectedSlot) + world.HotbarContainerOffset
	if session.InventorySlot(mainSlot).IsEmpty() && int(snap.SelectedSlot) >= 0 && int(snap.SelectedSlot) < world.HotbarSlotCount {
		if !session.InventorySlot(int(snap.SelectedSlot)).IsEmpty() {
			mainSlot = int(snap.SelectedSlot)
		}
	}
	candidateSlots := []int{mainSlot, 45, 5, 6, 7, 8}
	remXP := points

	for _, slot := range candidateSlots {
		if remXP <= 0 {
			break
		}
		stack := session.InventorySlot(slot)
		if stack.IsEmpty() {
			continue
		}
		meta := GetSlotItemMeta(session, slot)
		if meta.Damage <= 0 || meta.EnchantmentLevel("minecraft:mending") <= 0 {
			continue
		}
		repairPossible := remXP * 2
		repairAmount := meta.Damage
		if repairAmount > repairPossible {
			repairAmount = repairPossible
		}
		meta.Damage -= repairAmount
		SetSlotItemMeta(session, slot, meta)
		repairedDurability += repairAmount
		xpUsed := (repairAmount + 1) / 2
		remXP -= xpUsed
		if s != nil && session.send != nil {
			stateID := session.NextContainerStateID()
			trim, _ := GetSlotArmorTrim(session, slot)
			_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlotWithMeta(0, stateID, slot, stack, meta, trim))
		}
	}
	if remXP < 0 {
		remXP = 0
	}
	return remXP, repairedDurability
}

// applyMendingOnXP checks the player's held item (selected hotbar slot),
// offhand slot (45), and armor slots (5..8) for damaged items with
// minecraft:mending. Each 1 XP point repairs up to 2 durability damage.
func applyMendingOnXP(session *PlayerSession, points int32) int32 {
	var srv *Server
	rem, _ := srv.RepairPlayerMendingItems(session, points)
	return rem
}

// TryMergeExperienceOrb attempts to merge an orb of value at (x, y, z) in dim
// into an existing nearby experience orb of the same denomination within
// ExperienceOrbMergeRadius (0.5 blocks), matching vanilla ExperienceOrb.tryMergeToExisting.
func (s *Server) TryMergeExperienceOrb(dim string, x, y, z float64, value int32) (int32, bool) {
	if s == nil || s.entities == nil || value <= 0 {
		return 0, false
	}
	normDim := world.NormalizeDimension(dim)
	orbs := s.xpOrbState()
	cx := int32(int64(math.Floor(x)) >> 4)
	cz := int32(int64(math.Floor(z)) >> 4)
	nearby := s.entities.AppendInChunkRange(nil, cx, cz, 1)
	maxDistSq := ExperienceOrbMergeRadius * ExperienceOrbMergeRadius

	for i := range nearby {
		ent := &nearby[i]
		if ent.TypeName != ExperienceOrbEntityTypeName || ent.EffectiveDimension() != normDim {
			continue
		}
		dx := ent.X - x
		dy := ent.Y - y
		dz := ent.Z - z
		if dx*dx+dy*dy+dz*dz > maxDistSq {
			continue
		}
		orbs.mu.Lock()
		existingVal := orbs.values[ent.ID]
		if existingVal == value {
			if orbs.counts[ent.ID] <= 0 {
				orbs.counts[ent.ID] = 1
			}
			orbs.counts[ent.ID]++
			orbs.mu.Unlock()
			s.entities.Update(ent.ID, func(e *world.Entity) {
				e.Age = 0
			})
			return ent.ID, true
		}
		orbs.mu.Unlock()
	}
	return 0, false
}

// MergeNearbyExperienceOrbsInDimension scans active experience orbs in dim and
// merges any pair of orbs with the same denomination within ExperienceOrbMergeRadius
// (0.5 blocks), incrementing the surviving orb's stack count and removing the
// merged orb entity.
func (s *Server) MergeNearbyExperienceOrbsInDimension(dim string) int {
	if s == nil || s.entities == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	orbs := s.xpOrbState()
	all := s.entities.AppendAll(nil)
	var dimOrbs []world.Entity
	for i := range all {
		if all[i].TypeName == ExperienceOrbEntityTypeName && all[i].EffectiveDimension() == normDim {
			dimOrbs = append(dimOrbs, all[i])
		}
	}
	if len(dimOrbs) < 2 {
		return 0
	}
	sort.Slice(dimOrbs, func(i, j int) bool {
		return dimOrbs[i].ID < dimOrbs[j].ID
	})

	maxDistSq := ExperienceOrbMergeRadius * ExperienceOrbMergeRadius
	removed := make(map[int32]bool)
	mergedCount := 0

	for i := 0; i < len(dimOrbs); i++ {
		a := &dimOrbs[i]
		if removed[a.ID] {
			continue
		}
		orbs.mu.Lock()
		valA := orbs.values[a.ID]
		orbs.mu.Unlock()
		if valA <= 0 {
			continue
		}

		for j := i + 1; j < len(dimOrbs); j++ {
			b := &dimOrbs[j]
			if removed[b.ID] {
				continue
			}
			dx := a.X - b.X
			dy := a.Y - b.Y
			dz := a.Z - b.Z
			if dx*dx+dy*dy+dz*dz > maxDistSq {
				continue
			}
			orbs.mu.Lock()
			valB := orbs.values[b.ID]
			if valB == valA {
				cntA := orbs.counts[a.ID]
				if cntA <= 0 {
					cntA = 1
				}
				cntB := orbs.counts[b.ID]
				if cntB <= 0 {
					cntB = 1
				}
				orbs.counts[a.ID] = cntA + cntB
				delete(orbs.values, b.ID)
				delete(orbs.counts, b.ID)
				delete(orbs.health, b.ID)
				orbs.mu.Unlock()

				removed[b.ID] = true
				mergedCount++
				s.entities.Remove(b.ID)
				s.entities.Update(a.ID, func(e *world.Entity) {
					if b.Age < e.Age {
						e.Age = b.Age
					}
				})
				pos := b.ChunkPos()
				s.BroadcastDimensionChunk(normDim, pos.X, pos.Z, protocol.PlayRemoveEntities,
					encodeRemoveEntitiesPacket(nil, []int32{b.ID}))
			} else {
				orbs.mu.Unlock()
			}
		}
	}
	return mergedCount
}

// SpawnExperienceOrb spawns a single minecraft:experience_orb entity carrying
// value XP points at (x, y, z) in the Overworld.
func (s *Server) SpawnExperienceOrb(x, y, z float64, value int32) (int32, *world.Entity) {
	return s.SpawnExperienceOrbInDimension(world.DimensionOverworld, x, y, z, value)
}

// SpawnExperienceOrbInDimension spawns a single minecraft:experience_orb entity
// carrying value XP points at (x, y, z) in dim and broadcasts its creation and
// DATA_VALUE entity metadata to nearby players.
func (s *Server) SpawnExperienceOrbInDimension(dim string, x, y, z float64, value int32) (int32, *world.Entity) {
	if s == nil || s.entities == nil || value <= 0 {
		return 0, nil
	}
	dim = world.NormalizeDimension(dim)
	typeID := registry.EntityTypeIndex(ExperienceOrbEntityTypeName)
	if typeID < 0 {
		typeID = DefaultExperienceOrbTypeID
	}
	ent := &world.Entity{
		TypeID:    typeID,
		TypeName:  ExperienceOrbEntityTypeName,
		Dimension: dim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	}
	id := s.entities.Add(ent)
	orbs := s.xpOrbState()
	orbs.mu.Lock()
	orbs.values[id] = value
	orbs.counts[id] = 1
	orbs.health[id] = ExperienceOrbDefaultHealth
	orbs.mu.Unlock()

	saved := s.entities.Get(id)
	if saved != nil {
		pos := saved.ChunkPos()
		s.BroadcastDimensionChunk(dim, pos.X, pos.Z, protocol.PlayAddEntity, s.EncodeExperienceOrbAddEntityPacket(nil, saved))
		s.BroadcastDimensionChunk(dim, pos.X, pos.Z, PlaySetEntityDataCB, EncodeExperienceOrbDataPacket(id, value))
	}
	return id, saved
}

// SpawnExperienceOrbs splits totalXP into vanilla orb denominations and spawns
// (or merges) the resulting experience orbs at (x, y, z) in the Overworld.
func (s *Server) SpawnExperienceOrbs(x, y, z float64, totalXP int32) []int32 {
	return s.SpawnExperienceOrbsInDimension(world.DimensionOverworld, x, y, z, totalXP)
}

// SpawnExperienceOrbsInDimension splits totalXP into vanilla orb denominations
// and spawns (or merges via TryMergeExperienceOrb) the resulting experience
// orbs at (x, y, z) in dim.
func (s *Server) SpawnExperienceOrbsInDimension(dim string, x, y, z float64, totalXP int32) []int32 {
	values := SplitExperienceOrbValues(totalXP)
	if len(values) == 0 {
		return nil
	}
	ids := make([]int32, 0, len(values))
	seen := make(map[int32]bool, len(values))
	for _, val := range values {
		if mergedID, ok := s.TryMergeExperienceOrb(dim, x, y, z, val); ok {
			if !seen[mergedID] {
				seen[mergedID] = true
				ids = append(ids, mergedID)
			}
			continue
		}
		if id, _ := s.SpawnExperienceOrbInDimension(dim, x, y, z, val); id != 0 {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// ExperienceOrbValue returns the single-orb XP denomination stored for an
// experience orb entity ID, or 0 if unknown.
func (s *Server) ExperienceOrbValue(entityID int32) int32 {
	if s == nil || entityID == 0 {
		return 0
	}
	orbs := s.xpOrbState()
	orbs.mu.Lock()
	val := orbs.values[entityID]
	orbs.mu.Unlock()
	return val
}

// ExperienceOrbCount returns the merged stack count (>= 1) stored for an
// experience orb entity ID, or 0 if unknown.
func (s *Server) ExperienceOrbCount(entityID int32) int32 {
	if s == nil || entityID == 0 {
		return 0
	}
	orbs := s.xpOrbState()
	orbs.mu.Lock()
	cnt := orbs.counts[entityID]
	_, exists := orbs.values[entityID]
	orbs.mu.Unlock()
	if !exists {
		return 0
	}
	if cnt <= 0 {
		return 1
	}
	return cnt
}

// ExperienceOrbTotalXP returns the total XP (value * count) stored in an
// experience orb entity ID, or 0 if unknown.
func (s *Server) ExperienceOrbTotalXP(entityID int32) int32 {
	if s == nil || entityID == 0 {
		return 0
	}
	orbs := s.xpOrbState()
	orbs.mu.Lock()
	val := orbs.values[entityID]
	cnt := orbs.counts[entityID]
	orbs.mu.Unlock()
	if val <= 0 {
		return 0
	}
	if cnt <= 0 {
		cnt = 1
	}
	return val * cnt
}

// TickExperienceOrbs advances all active experience orbs by one server tick (50ms):
//   - Decrements PickupDelay and increments Age (despawning at 6000 ticks).
//   - Applies gravity, water/lava buoyancy, and bubble column velocity.
//   - Attracts orbs toward the nearest living non-Spectator player within 8.0 blocks
//     using vanilla ExperienceOrb acceleration (d1 = 1 - dist/8, accel = d1^2 * 0.1).
//   - Periodically merges nearby same-value orbs within 0.5 blocks.
//   - Automatically collects orbs that reach ExperienceOrbPickupRadius (1.75 blocks) of a player.
func (s *Server) TickExperienceOrbs() {
	if s == nil || s.entities == nil {
		return
	}
	all := s.entities.AppendAll(nil)
	var orbEnts []world.Entity
	for i := range all {
		if all[i].TypeName == ExperienceOrbEntityTypeName {
			orbEnts = append(orbEnts, all[i])
		}
	}
	if len(orbEnts) == 0 {
		return
	}

	s.playersMu.RLock()
	players := make([]*PlayerSession, 0, len(s.players))
	for _, p := range s.players {
		if p != nil && !p.IsDead() {
			players = append(players, p)
		}
	}
	s.playersMu.RUnlock()

	orbs := s.xpOrbState()
	attractMaxSq := ExperienceOrbAttractRadius * ExperienceOrbAttractRadius
	pickupMaxSq := ExperienceOrbPickupRadius * ExperienceOrbPickupRadius
	dimsToMerge := make(map[string]bool)
	playersToPickup := make(map[*PlayerSession]bool)

	for i := range orbEnts {
		snap := &orbEnts[i]
		dim := snap.EffectiveDimension()
		newAge := snap.Age + 1
		if newAge >= ExperienceOrbMaxAgeTicks {
			s.entities.Remove(snap.ID)
			orbs.mu.Lock()
			delete(orbs.values, snap.ID)
			delete(orbs.counts, snap.ID)
			delete(orbs.health, snap.ID)
			orbs.mu.Unlock()
			pos := snap.ChunkPos()
			s.BroadcastDimensionChunk(dim, pos.X, pos.Z, protocol.PlayRemoveEntities,
				encodeRemoveEntitiesPacket(nil, []int32{snap.ID}))
			continue
		}
		if newAge%ExperienceOrbScanIntervalTicks == 0 {
			dimsToMerge[dim] = true
		}

		vx := float64(snap.VelocityX) / 8000.0
		vy := float64(snap.VelocityY) / 8000.0
		vz := float64(snap.VelocityZ) / 8000.0
		nextX, nextY, nextZ := snap.X, snap.Y, snap.Z
		onGround := snap.OnGround

		cache := s.DimensionChunks(dim)
		bx, by, bz := int(math.Floor(nextX)), int(math.Floor(nextY)), int(math.Floor(nextZ))
		inBubble := false
		if cache != nil {
			if nvx, nvy, nvz, ok := cache.BubbleColumnVelocityAt(bx, by, bz, vx, vy, vz); ok {
				vx, vy, vz = nvx, nvy, nvz
				inBubble = true
				onGround = false
			} else if cache.FluidHeightAt(bx, by, bz) > 0 {
				// Vanilla ExperienceOrb.setUnderwaterMovement: float gently upward.
				vy += 0.005
				if vy > 0.06 {
					vy = 0.06
				}
				vx *= 0.99
				vz *= 0.99
				onGround = false
			}
		}

		// Find nearest living non-Spectator player within 8.0 blocks in the same dimension.
		var nearestPlayer *PlayerSession
		bestDistSq := attractMaxSq
		var bestDX, bestDY, bestDZ float64
		for _, p := range players {
			pSnap := p.Snapshot()
			if pSnap.GameMode == GameModeSpectator || pSnap.Health <= 0 ||
				world.NormalizeDimension(pSnap.Dimension) != dim {
				continue
			}
			dx := pSnap.X - nextX
			dy := (pSnap.Y + 0.81) - nextY
			dz := pSnap.Z - nextZ
			distSq := dx*dx + dy*dy + dz*dz
			if distSq < bestDistSq {
				bestDistSq = distSq
				nearestPlayer = p
				bestDX, bestDY, bestDZ = dx, dy, dz
			}
		}

		attracted := false
		if nearestPlayer != nil && bestDistSq > 1e-8 {
			dist := math.Sqrt(bestDistSq)
			d1 := 1.0 - dist/ExperienceOrbAttractRadius
			if d1 > 0 {
				accel := d1 * d1 * 0.1
				vx += (bestDX / dist) * accel
				vy += (bestDY / dist) * accel
				vz += (bestDZ / dist) * accel
				attracted = true
				onGround = false
			}
		}

		if !inBubble && !attracted && cache != nil {
			yBelow := int(math.Floor(nextY - 0.1))
			blockBelow := cache.GetBlock(bx, yBelow, bz)
			if isPassableBlock(blockBelow) || blockBelow == world.StateWater {
				onGround = false
				if cache.FluidHeightAt(bx, by, bz) <= 0 {
					vy -= 0.03
					if vy < -0.5 {
						vy = -0.5
					}
				}
			} else {
				onGround = true
				if vy < 0 {
					vy = 0
				}
				nextY = float64(yBelow + 1)
			}
		}

		nextX += vx
		nextY += vy
		nextZ += vz
		if cache != nil {
			yBelow := int(math.Floor(nextY - 0.05))
			blockBelow := cache.GetBlock(int(math.Floor(nextX)), yBelow, int(math.Floor(nextZ)))
			if !isPassableBlock(blockBelow) && blockBelow != world.StateWater && nextY < float64(yBelow+1) {
				nextY = float64(yBelow + 1)
				if vy < 0 {
					vy = 0
				}
				onGround = true
			}
		}

		drag := 0.98
		if onGround {
			drag = 0.588
		}
		vx *= drag
		vy *= 0.98
		vz *= drag
		if math.Abs(vx) < 1e-4 {
			vx = 0
		}
		if math.Abs(vy) < 1e-4 {
			vy = 0
		}
		if math.Abs(vz) < 1e-4 {
			vz = 0
		}

		vxShort := clampVelocityShort(vx)
		vyShort := clampVelocityShort(vy)
		vzShort := clampVelocityShort(vz)
		moved := math.Abs(nextX-snap.X) > 1e-4 || math.Abs(nextY-snap.Y) > 1e-4 || math.Abs(nextZ-snap.Z) > 1e-4
		var newPickupDelay int
		s.entities.Update(snap.ID, func(e *world.Entity) {
			e.X, e.Y, e.Z = nextX, nextY, nextZ
			e.VelocityX, e.VelocityY, e.VelocityZ = vxShort, vyShort, vzShort
			e.OnGround = onGround
			if e.PickupDelay > 0 {
				e.PickupDelay--
			}
			newPickupDelay = e.PickupDelay
			e.Age = newAge
		})

		if moved && attracted {
			cpX := int32(int64(math.Floor(nextX)) >> 4)
			cpZ := int32(int64(math.Floor(nextZ)) >> 4)
			s.BroadcastDimensionChunk(dim, cpX, cpZ, protocol.PlaySetEntityMotion,
				EncodeSetEntityMotionPacket(snap.ID, vx, vy, vz))
		}

		if nearestPlayer != nil && newPickupDelay <= 0 {
			pSnap := nearestPlayer.Snapshot()
			dx := nextX - pSnap.X
			dy := nextY - pSnap.Y
			dz := nextZ - pSnap.Z
			if dx*dx+dz*dz <= pickupMaxSq && math.Abs(dy) <= ExperienceOrbPickupRadius {
				playersToPickup[nearestPlayer] = true
			}
		}
	}

	for dim := range dimsToMerge {
		s.MergeNearbyExperienceOrbsInDimension(dim)
	}
	for p := range playersToPickup {
		s.PickupNearbyExperienceOrbs(p)
	}
}

// PickupNearbyExperienceOrbs checks for experience orbs within
// ExperienceOrbPickupRadius of session, collects them (including stacked merge
// counts), applies Mending priority and XP gain, broadcasts take_item_entity +
// remove_entities + experience_orb.pickup sound, and returns the total XP collected.
func (s *Server) PickupNearbyExperienceOrbs(session *PlayerSession) int32 {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return 0
	}
	dim := world.NormalizeDimension(snap.Dimension)
	orbs := s.xpOrbState()

	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	nearby := s.entities.AppendInChunkRange(nil, cx, cz, 1)

	var collectedTotal int32
	var pickedUpAny bool
	for i := range nearby {
		ent := &nearby[i]
		if ent.TypeName != ExperienceOrbEntityTypeName {
			continue
		}
		if ent.EffectiveDimension() != dim {
			continue
		}
		if ent.PickupDelay > 0 {
			continue
		}
		dx := ent.X - snap.X
		dy := ent.Y - snap.Y
		dz := ent.Z - snap.Z
		if dx*dx+zcSq(dz) > ExperienceOrbPickupRadius*ExperienceOrbPickupRadius ||
			math.Abs(dy) > ExperienceOrbPickupRadius {
			continue
		}
		live := s.entities.Get(ent.ID)
		if live == nil || live.TypeName != ExperienceOrbEntityTypeName || live.EffectiveDimension() != dim {
			continue
		}
		s.entities.Remove(live.ID)

		orbs.mu.Lock()
		val := orbs.values[live.ID]
		cnt := orbs.counts[live.ID]
		delete(orbs.values, live.ID)
		delete(orbs.counts, live.ID)
		delete(orbs.health, live.ID)
		orbs.mu.Unlock()
		if val <= 0 {
			val = 1
		}
		if cnt <= 0 {
			cnt = 1
		}
		collectedTotal += val * cnt
		pickedUpAny = true

		pos := live.ChunkPos()
		s.BroadcastDimensionChunk(dim, pos.X, pos.Z, protocol.PlayTakeItemEntity, EncodeTakeItemEntity(live.ID, session.EntityID, 1))
		s.BroadcastDimensionChunk(dim, pos.X, pos.Z, protocol.PlayRemoveEntities, encodeRemoveEntitiesPacket(nil, []int32{live.ID}))
	}

	if pickedUpAny {
		s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:entity.experience_orb.pickup", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 0.1, 0.9, 0))
	}
	if collectedTotal > 0 {
		_ = s.AddPlayerExperience(session, collectedTotal)
	}
	return collectedTotal
}

// UseExperienceBottle handles a player right-clicking with a Bottle o' Enchanting
// (minecraft:experience_bottle): consumes 1 bottle (in non-Creative), awards
// StatTypeUsed, plays minecraft:entity.experience_bottle.throw, traces projectile
// impact, broadcasts potion break particles (2002), and spawns 3..11 XP in orbs.
func (s *Server) UseExperienceBottle(session *PlayerSession, hand int32, rng *rand.Rand) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false
	}
	slot := world.HotbarContainerOffset + int(snap.SelectedSlot)
	if hand == HandOffHand {
		slot = OffHandContainerSlot
	}
	stack := session.InventorySlot(slot)
	if stack.IsEmpty() || canonicalResourceName(stack.ResolvedID()) != ExperienceBottleItemID {
		return false
	}
	if snap.GameMode != GameModeCreative {
		if !s.consumeHandItemOne(session, hand) {
			return false
		}
	}
	s.RecordItemUsed(session, ExperienceBottleItemID, 1)

	dim := world.NormalizeDimension(snap.Dimension)
	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.experience_bottle.throw", SoundSourceNeutral, snap.X, snap.Y+1.5, snap.Z, 0.5, 0.4, 0))
	s.EmitVibration(VibrationEvent{
		Dimension:     dim,
		Type:          GameEventProjectileShoot,
		X:             snap.X,
		Y:             snap.Y + 1.5,
		Z:             snap.Z,
		Player:        session,
		Sneaking:      session.IsSneaking(),
		BlockOrItemID: ExperienceBottleItemID,
	})

	yawRad := -float64(snap.Yaw) * (math.Pi / 180.0)
	pitchRad := -float64(snap.Pitch) * (math.Pi / 180.0)
	dirX := math.Sin(yawRad) * math.Cos(pitchRad)
	dirY := math.Sin(pitchRad)
	dirZ := math.Cos(yawRad) * math.Cos(pitchRad)

	hitX, hitY, hitZ := snap.X+dirX*2.0, snap.Y+1.5+dirY*2.0, snap.Z+dirZ*2.0
	if cache := s.DimensionChunks(dim); cache != nil {
		for step := 0.5; step <= 8.0; step += 0.5 {
			tx := snap.X + dirX*step
			ty := snap.Y + 1.5 + dirY*step - 0.03*step*step
			tz := snap.Z + dirZ*step
			bx, by, bz := int(math.Floor(tx)), int(math.Floor(ty)), int(math.Floor(tz))
			if by < world.MinY || by >= world.MinY+world.WorldHeight {
				break
			}
			if !isPassableBlock(cache.GetBlock(bx, by, bz)) {
				hitX, hitY, hitZ = tx, ty, tz
				break
			}
			hitX, hitY, hitZ = tx, ty, tz
		}
	}

	xp := int32(7)
	if rng != nil {
		xp = ExperienceBottleMinXP + int32(rng.Intn(5)+rng.Intn(5))
	}
	hbx, hby, hbz := int(math.Floor(hitX)), int(math.Floor(hitY)), int(math.Floor(hitZ))
	s.BroadcastDimensionChunk(dim, int32(hbx>>4), int32(hbz>>4), PlayLevelEventCB,
		EncodeLevelEventPacket(LevelEventParticlesSpellPotionBreak, hbx, hby, hbz, DefaultPotionWaterColorRGB, false))
	s.SpawnExperienceOrbsInDimension(dim, hitX, hitY+0.25, hitZ, xp)
	return true
}

// EncodeExperienceOrbAddEntityPacket encodes a clientbound add_entity (0x01)
// packet for a minecraft:experience_orb entity, setting the trailing Data
// VarInt field to the orb's XP value.
func (s *Server) EncodeExperienceOrbAddEntityPacket(dst []byte, e *world.Entity) []byte {
	var val int32 = 1
	if s != nil && e != nil {
		if v := s.ExperienceOrbValue(e.ID); v > 0 {
			val = v
		}
	}
	dst = protocol.AppendVarInt(dst, e.ID)
	dst = append(dst, e.UUID[:]...)
	dst = protocol.AppendVarInt(dst, int32(e.TypeID))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(e.X))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(e.Y))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(e.Z))
	if e.VelocityX == 0 && e.VelocityY == 0 && e.VelocityZ == 0 {
		dst = append(dst, 0)
	} else {
		var w protocol.Writer
		w.LPVec3(
			float64(e.VelocityX)/8000.0,
			float64(e.VelocityY)/8000.0,
			float64(e.VelocityZ)/8000.0,
		)
		dst = append(dst, w.Bytes()...)
	}
	dst = append(dst,
		encodeAngleByte(e.Pitch),
		encodeAngleByte(e.Yaw),
		encodeAngleByte(e.HeadYaw),
	)
	dst = protocol.AppendVarInt(dst, val)
	return dst
}

// EncodeExperienceOrbDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// setting an experience orb's DATA_VALUE (index 8, INT serializer = 1).
func EncodeExperienceOrbDataPacket(entityID, value int32) []byte {
	if value <= 0 {
		value = 1
	}
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataExperienceOrbValueID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(value)
	w.Byte(0xFF)
	return w.Bytes()
}

// ExperienceOrbEntityDataPacket returns the ClientboundSetEntityDataPacket
// payload for entityID if it is an active experience orb.
func (s *Server) ExperienceOrbEntityDataPacket(entityID int32) []byte {
	val := s.ExperienceOrbValue(entityID)
	if val <= 0 {
		return nil
	}
	return EncodeExperienceOrbDataPacket(entityID, val)
}

func zcSq(v float64) float64 {
	return v * v
}
