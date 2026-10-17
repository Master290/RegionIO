package server

import (
	"errors"
	"fmt"
	"math"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// Serverbound player_action status values (protocol 775).
const (
	PlayerActionStartDestroyBlock = 0
	PlayerActionAbortDestroyBlock = 1
	PlayerActionStopDestroyBlock  = 2
)

// Serverbound client_command action values (protocol 775).
const (
	ClientCommandPerformRespawn = 0
	ClientCommandRequestStats   = 1
)

// Survival gameplay constants.
const (
	MaxItemStackSize           int32   = 64
	ItemPickupHorizontalRadius float64 = 1.5
	ItemPickupVerticalRadius   float64 = 1.5
	ItemMaxAgeTicks            int     = 6000
	FallDamageThreshold        float64 = 3.0
)

type playerSurvivalState struct {
	mu           sync.Mutex
	fallDistance float64
	dead         bool
}

var playerSurvivalStates sync.Map // map[*PlayerSession]*playerSurvivalState

func getSurvivalState(session *PlayerSession) *playerSurvivalState {
	if session == nil {
		return nil
	}
	if v, ok := playerSurvivalStates.Load(session); ok {
		return v.(*playerSurvivalState)
	}
	actual, _ := playerSurvivalStates.LoadOrStore(session, &playerSurvivalState{})
	return actual.(*playerSurvivalState)
}

// IsDead reports whether session is currently dead and awaiting respawn.
func (session *PlayerSession) IsDead() bool {
	if session == nil {
		return false
	}
	session.mu.RLock()
	hp := session.health
	session.mu.RUnlock()
	st := getSurvivalState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	if hp > 0 {
		st.dead = false
		return false
	}
	return true
}

// FallDistance returns the distance accumulated during the player's current fall.
func (session *PlayerSession) FallDistance() float64 {
	if session == nil {
		return 0
	}
	st := getSurvivalState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.fallDistance
}

// Health returns the player's current health points.
func (session *PlayerSession) Health() float32 {
	if session == nil {
		return 0
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.health
}

// AddItemToInventory merges stack into the player's inventory (preferring hotbar
// slots 36..44, then main inventory slots 9..35) and returns the number of items
// added along with the container slot indices that changed.
func (session *PlayerSession) AddItemToInventory(stack world.ItemStack) (added int32, changedSlots []int) {
	if session == nil || stack.IsEmpty() {
		return 0, nil
	}
	itemID := stack.ResolvedItemID()
	if itemID <= 0 {
		return 0, nil
	}
	remaining := stack.Count

	session.mu.Lock()
	defer session.mu.Unlock()

	// 1. Merge into existing non-full stacks of the same item: hotbar first, then main inventory.
	mergeSlotRange := func(start, end int) {
		for slot := start; slot < end && remaining > 0; slot++ {
			cur := session.inventory[slot]
			if cur.IsEmpty() || cur.ResolvedItemID() != itemID || cur.Count >= MaxItemStackSize {
				continue
			}
			space := MaxItemStackSize - cur.Count
			take := remaining
			if take > space {
				take = space
			}
			cur.Count += take
			session.inventory[slot] = world.NewItemStack(itemID, cur.Count)
			remaining -= take
			added += take
			changedSlots = append(changedSlots, slot)
		}
	}
	mergeSlotRange(world.HotbarContainerOffset, world.HotbarContainerOffset+world.HotbarSlotCount)
	mergeSlotRange(9, world.HotbarContainerOffset)

	// 2. Fill empty slots: hotbar first, then main inventory.
	fillEmptyRange := func(start, end int) {
		for slot := start; slot < end && remaining > 0; slot++ {
			if !session.inventory[slot].IsEmpty() {
				continue
			}
			take := remaining
			if take > MaxItemStackSize {
				take = MaxItemStackSize
			}
			session.inventory[slot] = world.NewItemStack(itemID, take)
			remaining -= take
			added += take
			changedSlots = append(changedSlots, slot)
		}
	}
	fillEmptyRange(world.HotbarContainerOffset, world.HotbarContainerOffset+world.HotbarSlotCount)
	fillEmptyRange(9, world.HotbarContainerOffset)

	return added, changedSlots
}

// SpawnItemEntity spawns a minecraft:item entity carrying stack at (x, y, z)
// in the Overworld and broadcasts its creation and item metadata to nearby players.
func (s *Server) SpawnItemEntity(x, y, z float64, stack world.ItemStack) (int32, *world.Entity) {
	return s.SpawnItemEntityInDimension(world.DimensionOverworld, x, y, z, stack)
}

// SpawnItemEntityInDimension spawns a minecraft:item entity carrying stack at (x, y, z)
// in dim and broadcasts its creation and item metadata to nearby players in dim.
func (s *Server) SpawnItemEntityInDimension(dim string, x, y, z float64, stack world.ItemStack) (int32, *world.Entity) {
	if s == nil || s.entities == nil || stack.IsEmpty() {
		return 0, nil
	}
	itemID := stack.ResolvedItemID()
	if itemID <= 0 {
		return 0, nil
	}
	dim = world.NormalizeDimension(dim)
	normalized := world.NewItemStack(itemID, stack.Count)
	typeID := registry.EntityTypeIndex(world.ItemEntityTypeName)
	if typeID < 0 {
		typeID = 0
	}
	ent := world.NewItemEntity(x, y, z, normalized, typeID)
	ent.Dimension = dim
	id := s.entities.Add(ent)
	saved := s.entities.Get(id)
	if saved != nil {
		pos := saved.ChunkPos()
		s.BroadcastDimensionChunk(dim, pos.X, pos.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, saved))
		s.BroadcastDimensionChunk(dim, pos.X, pos.Z, protocol.PlaySetEntityData, EncodeItemEntityData(id, normalized))
	}
	return id, saved
}

// EncodeItemEntityData encodes a clientbound set_entity_data packet body for a
// minecraft:item entity's ItemStack (synched entity data index 8, serializer 7).
func EncodeItemEntityData(entityID int32, stack world.ItemStack) []byte {
	w := protocol.NewWriter(24)
	w.VarInt(entityID)
	w.Byte(8)   // ItemEntity.DATA_ITEM accessor ID
	w.VarInt(7) // EntityDataSerializers.ITEM_STACK
	itemID := stack.ResolvedItemID()
	if stack.IsEmpty() || itemID <= 0 {
		w.VarInt(0)
	} else {
		w.VarInt(stack.Count)
		w.VarInt(itemID)
		w.VarInt(0) // added components
		w.VarInt(0) // removed components
	}
	w.Byte(0xFF) // end of synched entity data list
	return w.Bytes()
}

// EncodeTakeItemEntity encodes a clientbound take_item_entity (0x7c) packet body.
func EncodeTakeItemEntity(itemEntityID, collectorEntityID, amount int32) []byte {
	w := protocol.NewWriter(16)
	w.VarInt(itemEntityID)
	w.VarInt(collectorEntityID)
	w.VarInt(amount)
	return w.Bytes()
}

// EncodeHurtAnimation encodes a clientbound hurt_animation (0x2a) packet body.
func EncodeHurtAnimation(entityID int32, yaw float32) []byte {
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Float32(yaw)
	return w.Bytes()
}

// EncodeSetHealth encodes a clientbound set_health (0x68) packet body.
func EncodeSetHealth(health float32, food int32, saturation float32) []byte {
	w := protocol.NewWriter(16)
	w.Float32(health)
	w.VarInt(food)
	w.Float32(saturation)
	return w.Bytes()
}

// EncodePlayerCombatKill encodes a clientbound player_combat_kill (0x44) packet body.
func EncodePlayerCombatKill(playerEntityID int32, message string) []byte {
	w := protocol.NewWriter(len(message) + 16)
	w.VarInt(playerEntityID)
	w.Raw(nbt.Marshal(nbt.String(message)))
	return w.Bytes()
}

// EncodeRespawnPacket encodes a clientbound respawn (0x52) packet body for
// protocol 775 (CommonPlayerSpawnInfo + dataToKeep).
func EncodeRespawnPacket(gameMode uint8) []byte {
	return EncodeDimensionRespawnPacket(world.DimensionOverworld, gameMode, 0, 0)
}

func encodeContainerSetSlotPacket(stateID int32, slot int, stack world.ItemStack) []byte {
	w := protocol.NewWriter(32)
	w.VarInt(0) // containerId 0 = player inventory
	w.VarInt(stateID)
	w.Uint16(uint16(int16(slot)))
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

func encodeContainerSetContentPacket(stateID int32, inv [world.PlayerInventorySlots]world.ItemStack) []byte {
	w := protocol.NewWriter(256)
	w.VarInt(0) // containerId 0 = player inventory
	w.VarInt(stateID)
	w.VarInt(world.PlayerInventorySlots)
	for i := 0; i < world.PlayerInventorySlots; i++ {
		stack := inv[i]
		itemID := stack.ResolvedItemID()
		if stack.IsEmpty() || itemID <= 0 {
			w.VarInt(0)
		} else {
			w.VarInt(stack.Count)
			w.VarInt(itemID)
			w.VarInt(0)
			w.VarInt(0)
		}
	}
	w.VarInt(0) // carried item (empty)
	return w.Bytes()
}

// BreakBlock handles block digging for a player in creative or survival mode.
// In creative mode, blocks break on START_DESTROY_BLOCK (0) or STOP_DESTROY_BLOCK (2)
// without spawning drops. In survival mode, blocks break on STOP_DESTROY_BLOCK (2)
// (or START_DESTROY_BLOCK for zero-hardness plants/torches) and spawn a dropped
// item entity at the block center.
func (s *Server) BreakBlock(session *PlayerSession, x, y, z int, status int32) (broken bool, dropEntityID int32) {
	if s == nil {
		return false, 0
	}
	mode := GameModeCreative
	dim := world.DimensionOverworld
	if session != nil {
		if session.IsDead() {
			return false, 0
		}
		snap := session.Snapshot()
		mode = snap.GameMode
		dim = world.NormalizeDimension(snap.Dimension)
	}
	if mode == GameModeAdventure || mode == GameModeSpectator {
		return false, 0
	}
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return false, 0
	}

	prevState := cache.GetBlock(x, y, z)
	if prevState == world.StateAir || prevState == world.StateWater {
		return false, 0
	}

	switch mode {
	case GameModeSurvival:
		if prevState == world.StateBedrock || world.BlockName(prevState) == "minecraft:end_portal_frame" || world.BlockName(prevState) == "minecraft:end_portal" {
			return false, 0
		}
		if status != PlayerActionStopDestroyBlock &&
			!(status == PlayerActionStartDestroyBlock && world.IsInstantBreakBlock(prevState)) {
			return false, 0
		}
	case GameModeCreative:
		if status != PlayerActionStartDestroyBlock && status != PlayerActionStopDestroyBlock {
			return false, 0
		}
	default:
		return false, 0
	}

	broken, upd := cache.BreakBlockWithUpdates(x, y, z)
	if !broken {
		return false, 0
	}
	s.broadcastDimensionBlockChanges(dim, cache, upd.Changes, upd.LightChanged)
	if IsChestBlock(prevState) {
		s.DropChestContents(x, y, z)
	}
	if IsFurnaceBlock(prevState) {
		s.DropFurnaceContents(x, y, z)
	}

	if mode == GameModeSurvival {
		s.RecordBlockBreakExhaustion(session)
		if drop, ok := world.BlockDropItem(prevState); ok {
			dropID, _ := s.SpawnItemEntityInDimension(dim, float64(x)+0.5, float64(y)+0.25, float64(z)+0.5, drop)
			return true, dropID
		}
	}
	return true, 0
}

func (s *Server) broadcastBlockAndLightUpdate(x, y, z int, state uint16, lightChunks []world.ChunkPos) {
	s.broadcastDimensionBlockChanges(world.DimensionOverworld, s.chunks, []world.BlockChange{{Pos: world.BlockPos{X: x, Y: y, Z: z}, NewState: state}}, lightChunks)
}

func (s *Server) broadcastDimensionBlockChanges(dim string, cache *world.Cache, changes []world.BlockChange, lightChunks []world.ChunkPos) {
	dim = world.NormalizeDimension(dim)
	for _, ch := range changes {
		w := protocol.NewWriter(12)
		w.Position(ch.Pos.X, ch.Pos.Y, ch.Pos.Z)
		w.VarInt(int32(ch.NewState))
		cx := int32(ch.Pos.X >> 4)
		cz := int32(ch.Pos.Z >> 4)
		s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlayBlockUpdate, w.Bytes())
	}
	if cache != nil {
		for _, chunk := range lightChunks {
			if light, err := cache.LightUpdate(chunk.X, chunk.Z); err == nil {
				s.BroadcastDimensionChunk(dim, chunk.X, chunk.Z, protocol.PlayLightUpdate, light)
			}
		}
	}
}

// ConsumeHeldItem decrements the player's currently selected hotbar stack by
// count when in survival mode and sends container_set_slot to the client.
func (s *Server) ConsumeHeldItem(session *PlayerSession, count int32) (slot int, updated world.ItemStack, ok bool) {
	if session == nil || count <= 0 {
		return -1, world.ItemStack{}, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeCreative {
		return int(world.HotbarContainerOffset + snap.SelectedSlot), session.HeldItem(), true
	}
	if snap.GameMode != GameModeSurvival {
		return -1, world.ItemStack{}, false
	}

	session.mu.Lock()
	sel := int(session.selectedSlot)
	if sel < 0 || sel >= world.HotbarSlotCount {
		sel = 0
	}
	slot = world.HotbarContainerOffset + sel
	cur := session.inventory[slot]
	if cur.IsEmpty() {
		// Fallback to legacy slot index 0..8 if used.
		if !session.inventory[sel].IsEmpty() {
			slot = sel
			cur = session.inventory[sel]
		} else {
			session.mu.Unlock()
			return -1, world.ItemStack{}, false
		}
	}
	if cur.Count < count {
		session.mu.Unlock()
		return -1, world.ItemStack{}, false
	}
	remaining := cur.Count - count
	if remaining <= 0 {
		updated = world.ItemStack{}
	} else {
		updated = world.NewItemStack(cur.ResolvedItemID(), remaining)
	}
	session.inventory[slot] = updated
	session.containerStateID++
	if session.containerStateID <= 0 {
		session.containerStateID = 1
	}
	stateID := session.containerStateID
	send := session.send
	session.mu.Unlock()

	if send != nil {
		_ = send(protocol.PlayContainerSetSlot, encodeContainerSetSlotPacket(stateID, slot, updated))
	}
	return slot, updated, true
}

// TryPickupItems checks for nearby dropped item entities around session, adds
// them to the player's inventory, sends take_item_entity + container_set_slot,
// and removes or decrements the picked-up item entities. Returns total items picked up.
func (s *Server) TryPickupItems(session *PlayerSession) int32 {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return 0
	}
	playerDim := world.NormalizeDimension(snap.Dimension)

	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	nearby := s.entities.AppendInChunkRange(nil, cx, cz, 1)

	var totalPickedUp int32
	for i := range nearby {
		ent := &nearby[i]
		if !ent.IsItem() || ent.PickupDelay > 0 || ent.Item.IsEmpty() {
			continue
		}
		if ent.EffectiveDimension() != playerDim {
			continue
		}
		if math.Abs(ent.X-snap.X) > ItemPickupHorizontalRadius ||
			math.Abs(ent.Z-snap.Z) > ItemPickupHorizontalRadius ||
			math.Abs(ent.Y-snap.Y) > ItemPickupVerticalRadius {
			continue
		}
		// Re-fetch under lock to ensure the entity wasn't already picked up.
		live := s.entities.Get(ent.ID)
		if live == nil || !live.IsItem() || live.PickupDelay > 0 || live.Item.IsEmpty() || live.EffectiveDimension() != playerDim {
			continue
		}

		added, changedSlots := session.AddItemToInventory(live.Item)
		if added <= 0 {
			continue
		}
		totalPickedUp += added

		chunkPos := live.ChunkPos()
		s.BroadcastDimensionChunk(playerDim, chunkPos.X, chunkPos.Z, protocol.PlayTakeItemEntity,
			EncodeTakeItemEntity(live.ID, session.EntityID, added))

		if session.send != nil {
			for _, slot := range changedSlots {
				stateID := session.NextContainerStateID()
				slotStack := session.InventorySlot(slot)
				_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotPacket(stateID, slot, slotStack))
			}
		}

		if added >= live.Item.Count {
			s.entities.Remove(live.ID)
			s.BroadcastDimensionChunk(playerDim, chunkPos.X, chunkPos.Z, protocol.PlayRemoveEntities,
				encodeRemoveEntitiesPacket(nil, []int32{live.ID}))
		} else {
			remaining := world.NewItemStack(live.Item.ResolvedItemID(), live.Item.Count-added)
			s.entities.Update(live.ID, func(e *world.Entity) {
				e.Item = remaining
			})
			s.BroadcastDimensionChunk(playerDim, chunkPos.X, chunkPos.Z, protocol.PlaySetEntityData,
				EncodeItemEntityData(live.ID, remaining))
		}
	}
	return totalPickedUp
}

// DamagePlayer applies amount damage to session in survival or adventure mode,
// sends hurt_animation and set_health, and handles player death when health
// drops to 0 or below. Returns true if the damage killed the player.
func (s *Server) DamagePlayer(session *PlayerSession, amount float32, cause string) bool {
	if s == nil || session == nil || amount <= 0 || session.IsDead() {
		return false
	}

	dim := session.Dimension()
	session.mu.Lock()
	if session.gameMode == GameModeCreative || session.gameMode == GameModeSpectator {
		session.mu.Unlock()
		return false
	}
	if session.health <= 0 {
		session.mu.Unlock()
		return false
	}
	newHealth := session.health - amount
	died := false
	if newHealth <= 0 {
		newHealth = 0
		died = true
	}
	session.health = newHealth
	food := session.foodLevel
	saturation := session.foodSaturation
	entityID := session.EntityID
	name := session.Profile.Name
	pos := session.position
	send := session.send
	session.mu.Unlock()

	if died {
		st := getSurvivalState(session)
		st.mu.Lock()
		st.dead = true
		st.fallDistance = 0
		st.mu.Unlock()
	}

	cx := int32(int64(math.Floor(pos[0])) >> 4)
	cz := int32(int64(math.Floor(pos[2])) >> 4)
	hurtPayload := EncodeHurtAnimation(entityID, 0)
	s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlayHurtAnimation, hurtPayload)

	if send != nil {
		_ = send(protocol.PlaySetHealth, EncodeSetHealth(newHealth, food, saturation))
	}

	if died {
		if name == "" {
			name = "Player"
		}
		var deathMsg string
		switch cause {
		case "fall":
			deathMsg = fmt.Sprintf("%s fell from a high place", name)
		default:
			deathMsg = fmt.Sprintf("%s died", name)
		}
		if send != nil {
			_ = send(protocol.PlayPlayerCombatKill, EncodePlayerCombatKill(entityID, deathMsg))
		}
		chatW := protocol.NewWriter(len(deathMsg) + 8)
		chatW.Raw(nbt.Marshal(nbt.String(deathMsg)))
		chatW.Bool(false)
		s.Broadcast(protocol.PlaySystemChat, chatW.Bytes())
	}
	return died
}

// HandlePlayerFall updates session's accumulated fall distance from vertical
// movement (oldY -> newY) and applies fall damage (ceil(fallDistance - 3.0))
// upon landing on the ground. Returns the damage applied (0 if none).
func (s *Server) HandlePlayerFall(session *PlayerSession, oldY, newY float64, wasOnGround, onGround bool) float32 {
	if s == nil || session == nil || session.IsDead() {
		return 0
	}
	snap := session.Snapshot()
	st := getSurvivalState(session)

	if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator {
		st.mu.Lock()
		st.fallDistance = 0
		st.mu.Unlock()
		return 0
	}

	dy := newY - oldY
	st.mu.Lock()
	if dy < 0 {
		st.fallDistance += -dy
	} else if !onGround && wasOnGround {
		// Jumping upward from ground starts a fresh airborne trajectory.
		st.fallDistance = 0
	}
	if !onGround {
		st.mu.Unlock()
		return 0
	}
	totalFall := st.fallDistance
	st.fallDistance = 0
	st.mu.Unlock()

	if totalFall <= FallDamageThreshold {
		return 0
	}
	damage := float32(math.Ceil(totalFall - FallDamageThreshold))
	if damage <= 0 {
		return 0
	}
	s.DamagePlayer(session, damage, "fall")
	return damage
}

// RespawnPlayer restores a dead (or respawning) player's health and hunger to
// full, sends the clientbound respawn packet, teleports the player to their
// spawn point (or world default spawn), and re-synchronizes abilities, health,
// held slot, and inventory.
func (s *Server) RespawnPlayer(session *PlayerSession) error {
	if s == nil || session == nil {
		return errors.New("server: nil player session")
	}

	st := getSurvivalState(session)
	st.mu.Lock()
	st.dead = false
	st.fallDistance = 0
	st.mu.Unlock()

	session.SetFoodExhaustion(0)
	session.SetFoodTickTimer(0)
	s.CancelEating(session)

	prevDim := session.Dimension()
	session.mu.Lock()
	session.health = world.DefaultPlayerHealth
	session.foodLevel = world.DefaultPlayerFoodLevel
	session.foodSaturation = world.DefaultPlayerSaturation
	mode := session.gameMode
	selectedSlot := session.selectedSlot
	inv := session.inventory
	hasSpawn := session.hasSpawn
	spawnDim := world.NormalizeDimension(session.spawnDimension)
	spawnX, spawnY, spawnZ := session.spawnX, session.spawnY, session.spawnZ
	spawnYaw, spawnPitch := session.spawnYaw, session.spawnPitch
	session.containerStateID++
	if session.containerStateID <= 0 {
		session.containerStateID = 1
	}
	stateID := session.containerStateID
	send := session.send
	session.mu.Unlock()

	session.SetDimension(spawnDim)
	targetCache := s.DimensionChunks(spawnDim)
	targetX := 8.5
	targetY := float64(world.FlatSurfaceY + 1)
	targetZ := 8.5
	var targetYaw, targetPitch float32
	if targetCache != nil {
		if y, ok := targetCache.SafeSpawnY(8, 8); ok {
			targetY = float64(y)
		}
	}
	if hasSpawn {
		targetX = float64(spawnX) + 0.5
		targetY = float64(spawnY)
		targetZ = float64(spawnZ) + 0.5
		targetYaw = spawnYaw
		targetPitch = spawnPitch
	}

	if send != nil {
		if err := send(protocol.PlayRespawn, EncodeDimensionRespawnPacket(spawnDim, mode, s.cfg.WorldSeed, 0)); err != nil {
			return err
		}
	}
	targetCX := int32(int64(math.Floor(targetX)) >> 4)
	targetCZ := int32(int64(math.Floor(targetZ)) >> 4)
	if prevDim != spawnDim {
		if switchFn := getPlayerDimSwitchFunc(session); switchFn != nil && targetCache != nil {
			switchFn(targetCache, targetCX, targetCZ)
		}
	}
	if err := s.TeleportPlayer(session, targetX, targetY, targetZ, targetYaw, targetPitch); err != nil {
		return err
	}
	if send != nil {
		var abilityFlags byte
		switch mode {
		case GameModeCreative:
			abilityFlags = 0x0F
		case GameModeSpectator:
			abilityFlags = 0x07
		default:
			abilityFlags = 0x00
		}
		ab := protocol.NewWriter(9)
		ab.Byte(abilityFlags)
		ab.Float32(0.05)
		ab.Float32(0.1)
		_ = send(protocol.PlayAbilities, ab.Bytes())
		_ = send(protocol.PlaySetHealth, EncodeSetHealth(world.DefaultPlayerHealth, world.DefaultPlayerFoodLevel, world.DefaultPlayerSaturation))

		heldW := protocol.NewWriter(4)
		heldW.VarInt(selectedSlot)
		_ = send(protocol.PlaySetHeldSlot, heldW.Bytes())

		_ = send(protocol.PlayContainerSetContent, encodeContainerSetContentPacket(stateID, inv))
	}
	return nil
}
