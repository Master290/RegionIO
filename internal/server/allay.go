package server

import (
	"errors"
	"math"
	"math/rand"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Allay (minecraft:allay) constants, attributes, SynchedEntityData indices,
// and sound identifiers for Minecraft 26.1.2 (protocol 775).
const (
	MobTypeAllay      = "minecraft:allay"
	EntityTypeAllayID = 2

	DefaultAllayMaxHealth float32 = 20.0
	AllayFlySpeed         float64 = 0.14
	AllayRegenInterval    int     = 10  // +1.0 HP every 10 ticks (+2.0 HP/s natural regeneration)
	AllayRegenAmount      float32 = 1.0

	AllayPickupRange               float64 = 32.0
	AllayFollowPlayerRange         float64 = 64.0
	AllayDeliveryStopDist          float64 = 2.0
	AllayNoteBlockRange            float64 = 16.0
	AllayNoteBlockListenTicks      int     = 600  // 30 seconds (600 ticks) bound to a played note_block
	AllayJukeboxDanceRange         float64 = 10.0 // 10 blocks around an active jukebox
	AllayDuplicationCooldownTicks  int     = 6000 // 5 minutes (6000 ticks) between amethyst_shard duplications
	AllayInventoryMaxStack         int32   = 64
	AllayThrowCollectDelayTicks    int     = 40   // Wait up to 2s after picking up an item before throwing delivery

	AmethystShardItemID = "minecraft:amethyst_shard"
	NoteBlockName       = "minecraft:note_block"
	JukeboxBlockName    = "minecraft:jukebox"

	// SynchedEntityData indices for Allay in Minecraft 26.1.2 (protocol 775).
	// Allay extends PathfinderMob -> Mob -> LivingEntity -> Entity (first index = 16).
	EntityDataAllayDancingID      byte = 16 // Allay.DATA_DANCING (BOOLEAN serializer = 8)
	EntityDataAllayCanDuplicateID byte = 17 // Allay.DATA_CAN_DUPLICATE (BOOLEAN serializer = 8)

	// Canonical Allay sound effects.
	SoundAllayAmbientWithItem    = "minecraft:entity.allay.ambient_with_item"
	SoundAllayAmbientWithoutItem = "minecraft:entity.allay.ambient_without_item"
	SoundAllayHurt               = "minecraft:entity.allay.hurt"
	SoundAllayDeath              = "minecraft:entity.allay.death"
	SoundAllayItemGiven          = "minecraft:entity.allay.item_given"
	SoundAllayItemTaken          = "minecraft:entity.allay.item_taken"
	SoundAllayItemThrown         = "minecraft:entity.allay.item_thrown"
	SoundAmethystBlockChime      = "minecraft:block.amethyst_block.chime"
)

// AllayBehaviorState holds the server-side AI, item-collection, note-block,
// jukebox-dancing, and amethyst-duplication state for a `minecraft:allay` entity.
type AllayBehaviorState struct {
	EntityID                 int32
	HeldItem                 world.ItemStack
	CollectedCount           int32
	LikedPlayerUUID          [16]byte
	HasLikedPlayer           bool
	NoteBlockDimension       string
	NoteBlockPos             [3]int
	NoteBlockListenTicks     int
	JukeboxDimension         string
	JukeboxPos               [3]int
	HasJukeboxPos            bool
	Dancing                  bool
	CanDuplicate             bool
	DuplicationCooldownTicks int
	DeliveryDelayTicks       int
	RegenTickCounter         int
	LastDeliveredEntityID    int32
}

type serverAllayManager struct {
	mu     sync.RWMutex
	allays map[int32]*AllayBehaviorState
}

var allayManagers sync.Map // map[*Server]*serverAllayManager

func (s *Server) allayManager() *serverAllayManager {
	if s == nil {
		return &serverAllayManager{allays: make(map[int32]*AllayBehaviorState)}
	}
	if v, ok := allayManagers.Load(s); ok {
		return v.(*serverAllayManager)
	}
	mgr := &serverAllayManager{
		allays: make(map[int32]*AllayBehaviorState),
	}
	actual, _ := allayManagers.LoadOrStore(s, mgr)
	return actual.(*serverAllayManager)
}

func (s *Server) ensureAllayState(entityID int32) *AllayBehaviorState {
	if s == nil || entityID <= 0 {
		return nil
	}
	mgr := s.allayManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.allays[entityID]
	if !ok || st == nil {
		st = &AllayBehaviorState{
			EntityID:     entityID,
			CanDuplicate: true,
		}
		mgr.allays[entityID] = st
	}
	return st
}

// GetAllayState returns a snapshot of the AllayBehaviorState for entityID.
func (s *Server) GetAllayState(entityID int32) (AllayBehaviorState, bool) {
	if s == nil || entityID <= 0 {
		return AllayBehaviorState{}, false
	}
	mgr := s.allayManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	st, ok := mgr.allays[entityID]
	if !ok || st == nil {
		return AllayBehaviorState{}, false
	}
	return *st, true
}

// EncodeAllayEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// for a `minecraft:allay` entity with its DATA_DANCING (index 16) and
// DATA_CAN_DUPLICATE (index 17) flags.
func EncodeAllayEntityDataPacket(entityID int32, dancing, canDuplicate bool) []byte {
	w := protocol.NewWriter(16)
	w.VarInt(entityID)
	w.Byte(EntityDataAllayDancingID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(dancing)
	w.Byte(EntityDataAllayCanDuplicateID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(canDuplicate)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeAllayEntityDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeAllayEntityDataPacket.
func DecodeAllayEntityDataPacket(r *protocol.Reader) (entityID int32, dancing, canDuplicate bool, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, false, err
	}
	canDuplicate = true
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, false, false, err
		}
		if idx == 0xFF {
			break
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, false, false, err
		}
		switch idx {
		case EntityDataAllayDancingID:
			if ser != EntityDataSerializerBool {
				return 0, false, false, errors.New("allay: unexpected DATA_DANCING serializer")
			}
			dancing, err = r.Bool()
		case EntityDataAllayCanDuplicateID:
			if ser != EntityDataSerializerBool {
				return 0, false, false, errors.New("allay: unexpected DATA_CAN_DUPLICATE serializer")
			}
			canDuplicate, err = r.Bool()
		default:
			return 0, false, false, errors.New("allay: unexpected SynchedEntityData index")
		}
		if err != nil {
			return 0, false, false, err
		}
	}
	return entityID, dancing, canDuplicate, nil
}

// EncodeAllayMainHandEquipmentPacket encodes a ClientboundSetEquipmentPacket (0x66)
// for an Allay's main-hand item slot (slot 0).
func EncodeAllayMainHandEquipmentPacket(entityID int32, stack world.ItemStack) []byte {
	w := protocol.NewWriter(24)
	w.VarInt(entityID)
	w.Byte(0) // EquipmentSlot.MAINHAND (0), last entry (bit 7 = 0)
	itemID := stack.ResolvedItemID()
	if stack.IsEmpty() || itemID <= 0 {
		w.VarInt(0)
	} else {
		w.VarInt(stack.Count)
		w.VarInt(itemID)
		w.VarInt(0) // added components
		w.VarInt(0) // removed components
	}
	return w.Bytes()
}

func (s *Server) broadcastAllayState(ent *world.Entity, st AllayBehaviorState) {
	if s == nil || ent == nil {
		return
	}
	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeAllayEntityDataPacket(ent.ID, st.Dancing, st.CanDuplicate))
}

func (s *Server) broadcastAllayEquipment(ent *world.Entity, held world.ItemStack) {
	if s == nil || ent == nil {
		return
	}
	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEquipment,
		EncodeAllayMainHandEquipmentPacket(ent.ID, held))
}

// SpawnAllay spawns a `minecraft:allay` entity at (x, y, z) in dimension and
// broadcasts its creation and initial metadata to nearby players.
func (s *Server) SpawnAllay(dimension string, x, y, z float64) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil server or entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeAllay)
	if typeID < 0 {
		typeID = EntityTypeAllayID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeAllay,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  false,
	})
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(id, tr.aiTickID)
	mem.healthInit = true
	mem.health = DefaultAllayMaxHealth
	mem.lastHealth = DefaultAllayMaxHealth
	tr.mu.Unlock()

	st := s.ensureAllayState(id)
	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		if st != nil {
			s.broadcastAllayState(ent, *st)
		}
	}
	return id, nil
}

// IsAllayImmuneToAttacker reports whether the Allay with entityID is immune to
// damage from attacker (in vanilla 26.1.2, an Allay cannot be hurt by the
// player who gave it its held item).
func (s *Server) IsAllayImmuneToAttacker(entityID int32, attacker *PlayerSession) bool {
	if s == nil || attacker == nil || entityID <= 0 {
		return false
	}
	st, ok := s.GetAllayState(entityID)
	if !ok || !st.HasLikedPlayer {
		return false
	}
	return st.LikedPlayerUUID == attacker.Profile.UUID
}

// NotifyJukeboxPlayingForAllays notifies all Allays within 10 blocks of (x, y, z)
// in dimension that a `minecraft:jukebox` has started or stopped playing a music disc.
// (Can be called directly by Agent C's jukebox code.)
func (s *Server) NotifyJukeboxPlayingForAllays(dimension string, x, y, z int, playing bool) int {
	if s == nil || s.entities == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dimension)
	maxDistSq := AllayJukeboxDanceRange * AllayJukeboxDanceRange
	cx, cy, cz := float64(x)+0.5, float64(y)+0.5, float64(z)+0.5
	mgr := s.allayManager()

	var updated int
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		ent := &allEnts[i]
		if canonicalResourceName(ent.TypeName) != MobTypeAllay || ent.EffectiveDimension() != normDim {
			continue
		}
		dx := ent.X - cx
		dy := ent.Y - cy
		dz := ent.Z - cz
		if dx*dx+dy*dy+dz*dz > maxDistSq {
			continue
		}
		st := s.ensureAllayState(ent.ID)
		if st == nil {
			continue
		}
		mgr.mu.Lock()
		if playing {
			st.JukeboxDimension = normDim
			st.JukeboxPos = [3]int{x, y, z}
			st.HasJukeboxPos = true
			st.Dancing = true
		} else if st.HasJukeboxPos && st.JukeboxPos == ([3]int{x, y, z}) {
			st.HasJukeboxPos = false
			st.Dancing = false
		}
		snap := *st
		mgr.mu.Unlock()
		s.broadcastAllayState(ent, snap)
		updated++
	}
	return updated
}

// OnNoteBlockPlayedForAllays notifies all Allays within 16 blocks of (x, y, z)
// in dimension that a `minecraft:note_block` was played, binding them to deliver
// collected items to that note block for 30 seconds (600 ticks).
// (Can be called directly by Agent C's note_block code.)
func (s *Server) OnNoteBlockPlayedForAllays(dimension string, x, y, z int) int {
	if s == nil || s.entities == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dimension)
	maxDistSq := AllayNoteBlockRange * AllayNoteBlockRange
	cx, cy, cz := float64(x)+0.5, float64(y)+0.5, float64(z)+0.5
	mgr := s.allayManager()

	var boundCount int
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		ent := &allEnts[i]
		if canonicalResourceName(ent.TypeName) != MobTypeAllay || ent.EffectiveDimension() != normDim {
			continue
		}
		dx := ent.X - cx
		dy := ent.Y - cy
		dz := ent.Z - cz
		if dx*dx+dy*dy+dz*dz > maxDistSq {
			continue
		}
		st := s.ensureAllayState(ent.ID)
		if st == nil {
			continue
		}
		mgr.mu.Lock()
		st.NoteBlockDimension = normDim
		st.NoteBlockPos = [3]int{x, y, z}
		st.NoteBlockListenTicks = AllayNoteBlockListenTicks
		mgr.mu.Unlock()
		boundCount++
	}
	return boundCount
}

// DuplicateAllay duplicates a dancing Allay when given a `minecraft:amethyst_shard`,
// spawning a new Allay at the same location and placing both on a 6000-tick (5m)
// duplication cooldown.
func (s *Server) DuplicateAllay(entityID int32) (newAllayID int32, ok bool) {
	if s == nil || s.entities == nil {
		return 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeAllay {
		return 0, false
	}
	st := s.ensureAllayState(entityID)
	if st == nil {
		return 0, false
	}
	mgr := s.allayManager()
	mgr.mu.Lock()
	if !st.Dancing || !st.CanDuplicate || st.DuplicationCooldownTicks > 0 {
		mgr.mu.Unlock()
		return 0, false
	}
	st.CanDuplicate = false
	st.DuplicationCooldownTicks = AllayDuplicationCooldownTicks
	snapParent := *st
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	childID, err := s.SpawnAllay(dim, ent.X, ent.Y, ent.Z)
	if err != nil || childID <= 0 {
		return 0, false
	}
	childSt := s.ensureAllayState(childID)
	if childSt != nil {
		mgr.mu.Lock()
		childSt.CanDuplicate = false
		childSt.DuplicationCooldownTicks = AllayDuplicationCooldownTicks
		childSt.Dancing = snapParent.Dancing
		childSt.HasJukeboxPos = snapParent.HasJukeboxPos
		childSt.JukeboxDimension = snapParent.JukeboxDimension
		childSt.JukeboxPos = snapParent.JukeboxPos
		snapChild := *childSt
		mgr.mu.Unlock()
		if childEnt := s.entities.Get(childID); childEnt != nil {
			s.broadcastAllayState(childEnt, snapChild)
		}
	}
	s.broadcastAllayState(ent, snapParent)

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(entityID, EntityEventLoveHearts))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundAmethystBlockChime, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 2.0, 1.0, 0))
	return childID, true
}

// InteractWithAllay handles a player right-clicking a `minecraft:allay`:
//   - If the Allay is dancing, can duplicate, and the player holds `minecraft:amethyst_shard`,
//     duplicates the Allay and consumes 1 shard in survival.
//   - If the player holds an item and the Allay's hand is empty, gives 1 item to
//     the Allay and binds the Allay to the player.
//   - If the player's hand is empty and the Allay holds an item, retrieves the
//     held item (and any collected items) back to the player and unbinds the Allay.
func (s *Server) InteractWithAllay(session *PlayerSession, entityID int32) (handled bool, duplicatedAllayID int32) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false, 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeAllay {
		return false, 0
	}
	dx := ent.X - snap.X
	dy := ent.Y - snap.Y
	dz := ent.Z - snap.Z
	if snap.GameMode != GameModeCreative && (dx*dx+dy*dy+dz*dz) > MaxMeleeAttackReach*MaxMeleeAttackReach {
		return false, 0
	}

	st := s.ensureAllayState(entityID)
	if st == nil {
		return false, 0
	}
	held := session.HeldItem()
	heldID := ""
	if !held.IsEmpty() {
		heldID = canonicalResourceName(held.ResolvedID())
	}

	// 1. Amethyst shard duplication when dancing.
	if heldID == AmethystShardItemID {
		if dupID, ok := s.DuplicateAllay(entityID); ok {
			consumeOneHeldItemInSurvival(session)
			return true, dupID
		}
	}

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	mgr := s.allayManager()

	// 2. Give 1 item to an empty-handed Allay.
	if !held.IsEmpty() {
		mgr.mu.Lock()
		if !st.HeldItem.IsEmpty() {
			mgr.mu.Unlock()
			return false, 0
		}
		given := world.NewItemStack(held.ResolvedItemID(), 1)
		st.HeldItem = given
		st.LikedPlayerUUID = session.Profile.UUID
		st.HasLikedPlayer = true
		mgr.mu.Unlock()

		consumeOneHeldItemInSurvival(session)
		s.broadcastAllayEquipment(ent, given)
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundAllayItemGiven, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		return true, 0
	}

	// 3. Empty hand: take item(s) back from the Allay.
	mgr.mu.Lock()
	if st.HeldItem.IsEmpty() {
		mgr.mu.Unlock()
		return false, 0
	}
	returnedItem := st.HeldItem
	extraCount := st.CollectedCount
	st.HeldItem = world.ItemStack{}
	st.CollectedCount = 0
	st.HasLikedPlayer = false
	st.LikedPlayerUUID = [16]byte{}
	mgr.mu.Unlock()

	totalReturn := returnedItem.Count + extraCount
	returnStack := world.NewItemStack(returnedItem.ResolvedItemID(), totalReturn)
	added, _ := session.AddItemToInventory(returnStack)
	if rem := totalReturn - added; rem > 0 {
		s.SpawnItemEntityInDimension(dim, snap.X, snap.Y+0.5, snap.Z, world.NewItemStack(returnedItem.ResolvedItemID(), rem))
	}

	s.broadcastAllayEquipment(ent, world.ItemStack{})
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundAllayItemTaken, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true, 0
}

// SetAllayDuplicationCooldown sets the duplication cooldown ticks for entityID
// and updates DATA_CAN_DUPLICATE accordingly.
func (s *Server) SetAllayDuplicationCooldown(entityID int32, ticks int) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeAllay {
		return false
	}
	st := s.ensureAllayState(entityID)
	if st == nil {
		return false
	}
	if ticks < 0 {
		ticks = 0
	}
	mgr := s.allayManager()
	mgr.mu.Lock()
	st.DuplicationCooldownTicks = ticks
	st.CanDuplicate = ticks == 0
	snap := *st
	mgr.mu.Unlock()
	s.broadcastAllayState(ent, snap)
	return true
}

// TickAllays advances all `minecraft:allay` entities by one server tick:
//   - Natural health regeneration (+1 HP every 10 ticks = 2 HP/s)
//   - Duplication cooldown timer & DATA_CAN_DUPLICATE update
//   - Note block listener decay & jukebox proximity check
//   - Searching for and picking up dropped items matching HeldItem within 32 blocks
//   - Flying to the bound note block (if active) or LikedPlayer and throwing collected items.
func (s *Server) TickAllays(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	allEnts := s.entities.AppendAll(nil)
	mgr := s.allayManager()

	for i := range allEnts {
		ent := &allEnts[i]
		if canonicalResourceName(ent.TypeName) != MobTypeAllay {
			continue
		}
		st := s.ensureAllayState(ent.ID)
		if st == nil {
			continue
		}
		dim := ent.EffectiveDimension()
		cache := s.DimensionChunks(dim)

		// 1. Natural health regeneration (+1.0 HP every 10 ticks when injured).
		curHP := s.MobHealth(ent.ID)
		if curHP > 0 && curHP < DefaultAllayMaxHealth {
			mgr.mu.Lock()
			st.RegenTickCounter++
			doRegen := st.RegenTickCounter >= AllayRegenInterval
			if doRegen {
				st.RegenTickCounter = 0
			}
			mgr.mu.Unlock()
			if doRegen {
				nextHP := curHP + AllayRegenAmount
				if nextHP > DefaultAllayMaxHealth {
					nextHP = DefaultAllayMaxHealth
				}
				s.SetMobHealth(ent.ID, nextHP)
			}
		}

		// 2. Duplication cooldown & Note Block listener timer & Jukebox range check.
		mgr.mu.Lock()
		stateBroadcastNeeded := false
		if st.DuplicationCooldownTicks > 0 {
			st.DuplicationCooldownTicks--
			if st.DuplicationCooldownTicks == 0 && !st.CanDuplicate {
				st.CanDuplicate = true
				stateBroadcastNeeded = true
			}
		}
		if st.NoteBlockListenTicks > 0 {
			st.NoteBlockListenTicks--
			if cache != nil {
				nbBlock := cache.GetBlock(st.NoteBlockPos[0], st.NoteBlockPos[1], st.NoteBlockPos[2])
				if world.BlockName(nbBlock) != NoteBlockName {
					st.NoteBlockListenTicks = 0
				}
			}
		}
		if st.Dancing && st.HasJukeboxPos {
			jdx := ent.X - (float64(st.JukeboxPos[0]) + 0.5)
			jdy := ent.Y - (float64(st.JukeboxPos[1]) + 0.5)
			jdz := ent.Z - (float64(st.JukeboxPos[2]) + 0.5)
			if st.JukeboxDimension != dim || jdx*jdx+jdy*jdy+jdz*jdz > AllayJukeboxDanceRange*AllayJukeboxDanceRange {
				st.Dancing = false
				st.HasJukeboxPos = false
				stateBroadcastNeeded = true
			} else if cache != nil && world.BlockName(cache.GetBlock(st.JukeboxPos[0], st.JukeboxPos[1], st.JukeboxPos[2])) != JukeboxBlockName {
				st.Dancing = false
				st.HasJukeboxPos = false
				stateBroadcastNeeded = true
			}
		}
		if st.DeliveryDelayTicks > 0 {
			st.DeliveryDelayTicks--
		}
		snap := *st
		mgr.mu.Unlock()

		if stateBroadcastNeeded {
			s.broadcastAllayState(ent, snap)
		}

		// 3. Item pickup & delivery when holding a filter item.
		if snap.HeldItem.IsEmpty() {
			continue
		}
		wantID := canonicalResourceName(snap.HeldItem.ResolvedID())

		// Look for matching dropped item entities within 32 blocks if inventory has room.
		if snap.CollectedCount < AllayInventoryMaxStack {
			var bestItem *world.Entity
			bestDistSq := AllayPickupRange * AllayPickupRange
			for j := range allEnts {
				cand := &allEnts[j]
				if !cand.IsItem() || cand.EffectiveDimension() != dim || cand.Item.IsEmpty() {
					continue
				}
				if canonicalResourceName(cand.Item.ResolvedID()) != wantID {
					continue
				}
				dx := cand.X - ent.X
				dy := cand.Y - ent.Y
				dz := cand.Z - ent.Z
				dSq := dx*dx + dy*dy + dz*dz
				if dSq <= bestDistSq {
					bestDistSq = dSq
					bestItem = cand
				}
			}
			if bestItem != nil {
				dist := math.Sqrt(bestDistSq)
				if dist <= 1.6 {
					// Pick up the dropped item entity!
					space := AllayInventoryMaxStack - snap.CollectedCount
					take := bestItem.Item.Count
					if take > space {
						take = space
					}
					if take > 0 {
						mgr.mu.Lock()
						st.CollectedCount += take
						st.DeliveryDelayTicks = 0
						snap = *st
						mgr.mu.Unlock()

						cp := ent.ChunkPos()
						s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayTakeItemEntity,
							EncodeTakeItemEntity(bestItem.ID, ent.ID, take))
						if take >= bestItem.Item.Count {
							s.entities.Remove(bestItem.ID)
							s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
								encodeRemoveEntitiesPacket(nil, []int32{bestItem.ID}))
						} else {
							remStack := world.NewItemStack(bestItem.Item.ResolvedItemID(), bestItem.Item.Count-take)
							s.entities.Update(bestItem.ID, func(e *world.Entity) {
								e.Item = remStack
							})
						}
					}
				} else {
					// Fly toward the dropped item.
					s.flyAllayToward(ent, bestItem.X, bestItem.Y, bestItem.Z)
					continue
				}
			}
		}

		// 4. Deliver collected items to bound Note Block (priority) or LikedPlayer.
		if snap.CollectedCount > 0 && snap.DeliveryDelayTicks <= 0 {
			var targetX, targetY, targetZ float64
			hasTarget := false
			if snap.NoteBlockListenTicks > 0 && snap.NoteBlockDimension == dim {
				targetX = float64(snap.NoteBlockPos[0]) + 0.5
				targetY = float64(snap.NoteBlockPos[1]) + 1.0
				targetZ = float64(snap.NoteBlockPos[2]) + 0.5
				hasTarget = true
			} else if snap.HasLikedPlayer {
				if owner := s.findPlayerByUUID(snap.LikedPlayerUUID); owner != nil && !owner.IsDead() {
					osnap := owner.Snapshot()
					if world.NormalizeDimension(osnap.Dimension) == dim {
						targetX, targetY, targetZ = osnap.X, osnap.Y+1.0, osnap.Z
						hasTarget = true
					}
				}
			}
			if hasTarget {
				dx := targetX - ent.X
				dy := targetY - ent.Y
				dz := targetZ - ent.Z
				dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
				if dist <= AllayDeliveryStopDist {
					s.throwAllayCollectedItems(ent, st, targetX, targetY, targetZ)
				} else {
					s.flyAllayToward(ent, targetX, targetY, targetZ)
				}
			}
		}
	}
}

func (s *Server) flyAllayToward(ent *world.Entity, tx, ty, tz float64) {
	if s == nil || ent == nil {
		return
	}
	dx := tx - ent.X
	dy := ty - ent.Y
	dz := tz - ent.Z
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist <= 1e-4 {
		return
	}
	step := AllayFlySpeed
	if step > dist {
		step = dist
	}
	nx := ent.X + (dx/dist)*step
	ny := ent.Y + (dy/dist)*step
	nz := ent.Z + (dz/dist)*step
	yaw := yawToward(dx, dz)
	s.entities.Update(ent.ID, func(e *world.Entity) {
		e.X = nx
		e.Y = ny
		e.Z = nz
		e.Yaw = yaw
		e.HeadYaw = yaw
	})
}

func (s *Server) throwAllayCollectedItems(ent *world.Entity, st *AllayBehaviorState, tx, ty, tz float64) {
	if s == nil || ent == nil || st == nil {
		return
	}
	mgr := s.allayManager()
	mgr.mu.Lock()
	if st.CollectedCount <= 0 || st.HeldItem.IsEmpty() {
		mgr.mu.Unlock()
		return
	}
	dropStack := world.NewItemStack(st.HeldItem.ResolvedItemID(), st.CollectedCount)
	st.CollectedCount = 0
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	dropID, _ := s.SpawnItemEntityInDimension(dim, tx, ty, tz, dropStack)
	mgr.mu.Lock()
	st.LastDeliveredEntityID = dropID
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundAllayItemThrown, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	s.EmitVibration(VibrationEvent{
		Dimension:      dim,
		Type:           GameEventEntityInteract,
		X:              ent.X,
		Y:              ent.Y,
		Z:              ent.Z,
		SourceEntityID: ent.ID,
	})
}
