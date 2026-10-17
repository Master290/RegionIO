package server

import (
	"errors"
	"math"
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

// Experience orb gameplay constants.
const (
	ExperienceOrbPickupRadius float64 = 1.75
	ExperienceOrbMaxAgeTicks  int     = 6000
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
	values map[int32]int32 // entityID -> XP value
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
	st.level = level
	st.totalXP = totalXP
	st.progress = progress
	st.mu.Unlock()
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
		points = applyMendingOnXP(session, points)
	}
	st := getPlayerXPState(session)
	st.mu.Lock()
	newTotal := st.totalXP + points
	if newTotal < 0 {
		newTotal = 0
	}
	level, progress, _ := LevelAndProgressFromTotalXP(newTotal)
	st.totalXP = newTotal
	st.level = level
	st.progress = progress
	st.mu.Unlock()
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
	return s.SendPlayerExperience(session)
}

// applyMendingOnXP checks the player's held item (selected hotbar slot),
// offhand slot (45), and armor slots (5..8) for damaged items with
// minecraft:mending. Each 1 XP point repairs up to 2 durability damage.
func applyMendingOnXP(session *PlayerSession, points int32) int32 {
	if session == nil || points <= 0 {
		return points
	}
	snap := session.Snapshot()
	hotbarSlot := int(snap.SelectedSlot) + world.HotbarContainerOffset
	candidateSlots := []int{hotbarSlot, 5, 6, 7, 8, 45}
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
		xpUsed := (repairAmount + 1) / 2
		remXP -= xpUsed
	}
	if remXP < 0 {
		remXP = 0
	}
	return remXP
}

// SpawnExperienceOrb spawns a single minecraft:experience_orb entity carrying
// value XP points at (x, y, z) in the Overworld.
func (s *Server) SpawnExperienceOrb(x, y, z float64, value int32) (int32, *world.Entity) {
	return s.SpawnExperienceOrbInDimension(world.DimensionOverworld, x, y, z, value)
}

// SpawnExperienceOrbInDimension spawns a single minecraft:experience_orb entity
// carrying value XP points at (x, y, z) in dim and broadcasts its creation to
// nearby players.
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
	orbs.mu.Unlock()

	saved := s.entities.Get(id)
	if saved != nil {
		pos := saved.ChunkPos()
		s.BroadcastDimensionChunk(dim, pos.X, pos.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, saved))
	}
	return id, saved
}

// SpawnExperienceOrbs splits totalXP into vanilla orb denominations and spawns
// the resulting experience orbs at (x, y, z) in the Overworld.
func (s *Server) SpawnExperienceOrbs(x, y, z float64, totalXP int32) []int32 {
	return s.SpawnExperienceOrbsInDimension(world.DimensionOverworld, x, y, z, totalXP)
}

// SpawnExperienceOrbsInDimension splits totalXP into vanilla orb denominations
// and spawns the resulting experience orbs at (x, y, z) in dim.
func (s *Server) SpawnExperienceOrbsInDimension(dim string, x, y, z float64, totalXP int32) []int32 {
	values := SplitExperienceOrbValues(totalXP)
	if len(values) == 0 {
		return nil
	}
	ids := make([]int32, 0, len(values))
	for _, val := range values {
		if id, _ := s.SpawnExperienceOrbInDimension(dim, x, y, z, val); id != 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

// ExperienceOrbValue returns the XP point value stored for an experience orb
// entity ID, or 0 if unknown.
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

// PickupNearbyExperienceOrbs checks for experience orbs within
// ExperienceOrbPickupRadius of session, collects them, applies Mending and XP
// gain, broadcasts take_item_entity + remove_entities, and returns the total XP
// collected.
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
		delete(orbs.values, live.ID)
		orbs.mu.Unlock()
		if val <= 0 {
			val = 1
		}
		collectedTotal += val

		pos := live.ChunkPos()
		s.BroadcastDimensionChunk(dim, pos.X, pos.Z, protocol.PlayTakeItemEntity, EncodeTakeItemEntity(live.ID, session.EntityID, 1))
		s.BroadcastDimensionChunk(dim, pos.X, pos.Z, protocol.PlayRemoveEntities, encodeRemoveEntitiesPacket(nil, []int32{live.ID}))
	}

	if collectedTotal > 0 {
		_ = s.AddPlayerExperience(session, collectedTotal)
	}
	return collectedTotal
}

func zcSq(v float64) float64 {
	return v * v
}
