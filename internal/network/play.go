package network

import (
	"errors"
	"math"
	"sync"
	"time"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/server"
	"regionio/internal/world"
)

// Spawn column. The feet-level Y is resolved from the generated surface when
// the player enters the play phase.
const (
	spawnX      = 8.5
	spawnZ      = 8.5
	maxPlayerXZ = 30_000_000.0
	maxPlayerY  = 20_000_000.0
)

// beginPlay sends the join sequence once the client enters the Play phase and
// hands chunk streaming off to the background streamer. The streamer stops when
// h.ctx (the connection lifetime context) is cancelled.
func (h *handler) beginPlay() error {
	session, err := h.srv.RegisterPlayer(h.conn.Profile, h.conn.Enqueue)
	if err != nil {
		return err
	}
	h.session = session
	for i := range h.hotbar {
		h.hotbar[i] = -1 // empty
	}

	if session.LoadedFromDisk() {
		snap := session.Snapshot()
		if snap.HasSpawn {
			h.spawnY = float64(snap.SpawnY)
		} else {
			_, wy, _, _ := h.srv.WorldSpawn()
			h.spawnY = float64(wy)
		}
		h.heldSlot = snap.SelectedSlot
		for i := 0; i < world.HotbarSlotCount; i++ {
			st := snap.Inventory[world.HotbarContainerOffset+i]
			if st.IsEmpty() {
				st = snap.Inventory[i]
			}
			if !st.IsEmpty() {
				h.hotbar[i] = st.ResolvedItemID()
			}
		}
	} else {
		wx, wy, wz, wyaw := h.srv.WorldSpawn()
		h.spawnY = float64(wy)
		h.srv.SetPlayerTransform(session, float64(wx)+0.5, h.spawnY, float64(wz)+0.5, wyaw, 0, true)
	}

	h.srv.SetPlayerViewDistance(session, h.visibilityRadius())
	snap := session.Snapshot()
	startCX := int32(int64(math.Floor(snap.X)) >> 4)
	startCZ := int32(int64(math.Floor(snap.Z)) >> 4)

	if err := h.sendPlayLogin(); err != nil {
		return err
	}
	h.srv.SendDifficultyToPlayer(session)
	if h.srv.ReducedDebugInfo() {
		_ = h.conn.Send(protocol.PlayEntityEvent, server.EncodeEntityEventPacket(session.EntityID, server.EntityEventReducedDebugInfo))
	}
	// "Start waiting for level chunks": tells the client to show the loading
	// screen until chunks arrive.
	if err := h.sendGameEvent(protocol.GameEventStartWaitingChunks, 0); err != nil {
		return err
	}
	if err := h.sendPlayerPosition(1); err != nil {
		return err
	}
	if err := h.sendChunkCacheCenter(startCX, startCZ); err != nil {
		return err
	}
	if err := h.sendDefaultSpawnPosition(); err != nil {
		return err
	}
	if err := h.sendPlayerAbilities(); err != nil {
		return err
	}
	if err := h.sendSetHeldSlot(snap.SelectedSlot); err != nil {
		return err
	}
	if err := h.sendContainerSetContent(); err != nil {
		return err
	}
	if err := h.sendSetHealth(snap.Health, snap.FoodLevel, snap.FoodSaturation); err != nil {
		return err
	}
	if err := h.srv.SendPlayerExperience(session); err != nil {
		return err
	}
	if err := h.srv.SendPlayerActiveEffects(h.session); err != nil {
		return err
	}
	_ = h.srv.LoadPlayerAdvancementsFile(h.session)
	if err := h.srv.SendInitialAdvancements(h.session); err != nil {
		return err
	}
	if tab := h.session.SelectedAdvancementTab(); tab != "" {
		if err := h.conn.Send(server.PlaySelectAdvancementsTab, server.EncodeSelectAdvancementsTabPacket(tab)); err != nil {
			return err
		}
	}
	if err := h.srv.SendInitialRecipeBook(h.session); err != nil {
		return err
	}
	h.srv.TriggerInventoryChanged(h.session)
	h.srv.UnlockRecipesForInventory(h.session)
	if err := h.sendCommands(); err != nil {
		return err
	}
	// The client's sky stays where it started until it is told the time.
	if err := h.conn.Send(protocol.PlaySetTime, h.srv.SetTimePacket()); err != nil {
		return err
	}
	h.srv.SendWorldBorderToPlayer(session)
	h.srv.BroadcastPlayerInfoAdd(session)
	_ = h.srv.SendFullTabListToPlayer(session)
	_ = h.srv.SendFullScoreboardToPlayer(session)
	h.srv.SendBossBarsOnPlayerJoin(session)
	h.srv.BroadcastJoinMessage(session)
	// Launch the background chunk streamer. It owns generation + sending so the
	// read loop stays free; requestRecenter is a non-blocking push.
	playerCache := h.srv.PlayerChunks(session)
	h.streamer = newStreamer(playerCache, h.conn, h.log, h.visibilityRadius())
	h.streamer.onChunkSent = func(cx, cz int32) {
		if h.srv != nil && h.session != nil {
			h.srv.SendChunkBlockEntityPackets(h.session, h.session.Dimension(), cx, cz)
		}
	}
	h.srv.SetPlayerRecenterFunc(session, h.streamer.requestRecenter)
	h.srv.SetPlayerDimensionSwitchFunc(session, h.streamer.switchDimension)
	go h.streamer.run(h.ctx)
	h.streamer.requestRecenter(startCX, startCZ)
	go h.keepAliveLoop()
	go h.entitySyncLoop()
	go h.serverTickLoop()
	return nil
}

func (h *handler) sendChunkCacheCenter(cx, cz int32) error {
	w := protocol.NewWriter(8)
	w.VarInt(cx)
	w.VarInt(cz)
	return h.conn.SendWriter(protocol.PlayChunkCacheCenter, w)
}

func (h *handler) sendDefaultSpawnPosition() error {
	// 26.1.2 wraps the spawn in LevelData.RespawnData: GlobalPos followed by
	// yaw and pitch. GlobalPos starts with the dimension resource key.
	dim := "minecraft:overworld"
	sx, sy, sz := 8, int(math.Floor(h.spawnY)), 8
	yaw, pitch := float32(0.0), float32(0.0)
	if h.srv != nil {
		wx, wy, wz, wyaw := h.srv.WorldSpawn()
		sx, sy, sz = wx, wy, wz
		yaw = wyaw
	}
	if h.session != nil {
		snap := h.session.Snapshot()
		if snap.HasSpawn {
			if snap.SpawnDimension != "" {
				dim = snap.SpawnDimension
			}
			sx, sy, sz = int(snap.SpawnX), int(snap.SpawnY), int(snap.SpawnZ)
			yaw, pitch = snap.SpawnYaw, snap.SpawnPitch
		}
	}
	w := protocol.NewWriter(40)
	w.String(dim)
	w.Position(sx, sy, sz)
	w.Float32(yaw)
	w.Float32(pitch)
	return h.conn.SendWriter(protocol.PlayDefaultSpawnPos, w)
}

func (h *handler) sendPlayerAbilities() error {
	mode := server.GameModeCreative
	if h.session != nil {
		mode = h.session.Snapshot().GameMode
	}
	var flags byte
	switch mode {
	case server.GameModeCreative:
		flags = 0x0F
	case server.GameModeSpectator:
		flags = 0x07
	default:
		flags = 0x00
	}
	w := protocol.NewWriter(9)
	w.Byte(flags)
	w.Float32(0.05)
	w.Float32(0.1)
	return h.conn.SendWriter(protocol.PlayAbilities, w)
}

func (h *handler) sendSetHeldSlot(slot int32) error {
	if slot < 0 || slot >= world.HotbarSlotCount {
		slot = 0
	}
	w := protocol.NewWriter(4)
	w.VarInt(slot)
	return h.conn.SendWriter(protocol.PlaySetHeldSlot, w)
}

func (h *handler) sendSetHealth(health float32, food int32, saturation float32) error {
	if health <= 0 {
		health = world.DefaultPlayerHealth
	}
	w := protocol.NewWriter(16)
	w.Float32(health)
	w.VarInt(food)
	w.Float32(saturation)
	return h.conn.SendWriter(protocol.PlaySetHealth, w)
}

func (h *handler) sendContainerSetContent() error {
	var inv [world.PlayerInventorySlots]world.ItemStack
	stateID := int32(1)
	if h.session != nil {
		inv = h.session.Inventory()
		stateID = h.session.NextContainerStateID()
	}
	w := protocol.NewWriter(256)
	w.VarInt(0) // containerId 0 = player inventory
	w.VarInt(stateID)
	w.VarInt(world.PlayerInventorySlots)
	for i := 0; i < world.PlayerInventorySlots; i++ {
		if h.session != nil {
			switch inv[i].ResolvedID() {
			case server.FilledMapItemID:
				if mapID, ok := server.GetSlotMapID(h.session, i); ok {
					server.EncodeSlotWithMapID(w, inv[i], mapID, true)
					continue
				}
			case server.WritableBookItemID:
				if wb, ok := server.GetSlotWritableBook(h.session, i); ok {
					server.EncodeSlotWithBookContent(w, inv[i], &wb, nil)
					continue
				}
			case server.WrittenBookItemID:
				if sb, ok := server.GetSlotWrittenBook(h.session, i); ok {
					server.EncodeSlotWithBookContent(w, inv[i], nil, &sb)
					continue
				}
			case server.FireworkRocketItemID:
				if fw, ok := server.GetSlotFireworks(h.session, i); ok {
					server.EncodeSlotWithFireworks(w, inv[i], fw)
					continue
				}
			case server.FireworkStarItemID:
				if exp, ok := server.GetSlotFireworkStar(h.session, i); ok {
					server.EncodeSlotWithFireworkStar(w, inv[i], exp)
					continue
				}
			}
			if id := inv[i].ResolvedID(); server.IsBannerItem(id) || id == "minecraft:shield" {
				pats := server.GetSlotBannerPatterns(h.session, i)
				baseCol := server.GetSlotShieldBaseColor(h.session, i)
				if !pats.IsEmpty() || baseCol != "" {
					server.EncodeSlotWithBannerPatterns(w, inv[i], pats, baseCol)
					continue
				}
			}
			meta := server.GetSlotItemMeta(h.session, i)
			trim, hasTrim := server.GetSlotArmorTrim(h.session, i)
			if !meta.IsEmpty() || hasTrim {
				server.WriteSlotWithArmorTrim(w, inv[i], meta, trim)
				continue
			}
		}
		writeItemStack(w, inv[i])
	}
	writeItemStack(w, world.ItemStack{}) // carried item
	return h.conn.SendWriter(protocol.PlayContainerSetContent, w)
}

func (h *handler) sendContainerSetSlot(slot int, stack world.ItemStack) error {
	stateID := int32(1)
	if h.session != nil {
		stateID = h.session.NextContainerStateID()
	}
	w := protocol.NewWriter(32)
	w.VarInt(0) // containerId 0 = player inventory
	w.VarInt(stateID)
	w.Uint16(uint16(int16(slot)))
	if h.session != nil {
		switch stack.ResolvedID() {
		case server.FilledMapItemID:
			if mapID, ok := server.GetSlotMapID(h.session, slot); ok {
				server.EncodeSlotWithMapID(w, stack, mapID, true)
				return h.conn.SendWriter(protocol.PlayContainerSetSlot, w)
			}
		case server.WritableBookItemID:
			if wb, ok := server.GetSlotWritableBook(h.session, slot); ok {
				server.EncodeSlotWithBookContent(w, stack, &wb, nil)
				return h.conn.SendWriter(protocol.PlayContainerSetSlot, w)
			}
		case server.WrittenBookItemID:
			if sb, ok := server.GetSlotWrittenBook(h.session, slot); ok {
				server.EncodeSlotWithBookContent(w, stack, nil, &sb)
				return h.conn.SendWriter(protocol.PlayContainerSetSlot, w)
			}
		case server.FireworkRocketItemID:
			if fw, ok := server.GetSlotFireworks(h.session, slot); ok {
				server.EncodeSlotWithFireworks(w, stack, fw)
				return h.conn.SendWriter(protocol.PlayContainerSetSlot, w)
			}
		case server.FireworkStarItemID:
			if exp, ok := server.GetSlotFireworkStar(h.session, slot); ok {
				server.EncodeSlotWithFireworkStar(w, stack, exp)
				return h.conn.SendWriter(protocol.PlayContainerSetSlot, w)
			}
		}
		if id := stack.ResolvedID(); server.IsBannerItem(id) || id == "minecraft:shield" {
			pats := server.GetSlotBannerPatterns(h.session, slot)
			baseCol := server.GetSlotShieldBaseColor(h.session, slot)
			if !pats.IsEmpty() || baseCol != "" {
				server.EncodeSlotWithBannerPatterns(w, stack, pats, baseCol)
				return h.conn.SendWriter(protocol.PlayContainerSetSlot, w)
			}
		}
		meta := server.GetSlotItemMeta(h.session, slot)
		trim, hasTrim := server.GetSlotArmorTrim(h.session, slot)
		if !meta.IsEmpty() || hasTrim {
			server.WriteSlotWithArmorTrim(w, stack, meta, trim)
			return h.conn.SendWriter(protocol.PlayContainerSetSlot, w)
		}
	}
	writeItemStack(w, stack)
	return h.conn.SendWriter(protocol.PlayContainerSetSlot, w)
}

func writeItemStack(w *protocol.Writer, stack world.ItemStack) {
	itemID := stack.ResolvedItemID()
	if stack.IsEmpty() || itemID <= 0 {
		w.VarInt(0)
		return
	}
	w.VarInt(stack.Count)
	w.VarInt(itemID)
	w.VarInt(0) // added data components
	w.VarInt(0) // removed data components
}

// onPlayerMove recenters the streamer when the player crosses into a new chunk.
// It is a non-blocking push; the read loop never waits on generation.
func (h *handler) onPlayerMove(x, y, z float64, yaw, pitch float32, onGround bool) error {
	if math.IsNaN(x) || math.IsNaN(y) || math.IsNaN(z) ||
		math.IsInf(x, 0) || math.IsInf(y, 0) || math.IsInf(z, 0) ||
		math.IsNaN(float64(yaw)) || math.IsNaN(float64(pitch)) ||
		math.IsInf(float64(yaw), 0) || math.IsInf(float64(pitch), 0) {
		return errors.New("invalid player position")
	}
	if math.Abs(x) > maxPlayerXZ || math.Abs(z) > maxPlayerXZ || math.Abs(y) > maxPlayerY {
		return errors.New("player position outside world bounds")
	}
	var prev server.PlayerSnapshot
	if h.session != nil {
		prev = h.session.Snapshot()
	}
	h.srv.SetPlayerTransform(h.session, x, y, z, yaw, pitch, onGround)
	if h.session != nil {
		if h.session.IsFallFlying() {
			h.srv.HandlePlayerElytraMove(h.session, prev.X, prev.Y, prev.Z, x, y, z, prev.OnGround, onGround)
		} else {
			h.srv.HandlePlayerFall(h.session, prev.Y, y, prev.OnGround, onGround)
		}
		h.srv.RecordPlayerMovementExhaustion(h.session, prev.X, prev.Y, prev.Z, x, y, z, prev.OnGround, onGround)
		h.srv.RecordPlayerMovementStats(h.session, prev.X, prev.Y, prev.Z, x, y, z, prev.OnGround, onGround)
		h.srv.TryPickupItems(h.session)
		h.srv.PickupNearbyExperienceOrbs(h.session)
		bx, by, bz := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
		if !prev.OnGround && onGround {
			h.srv.EmitPlayerMovementVibration(h.session, server.GameEventHitGround, x, y, z)
		}
		if (x-prev.X)*(x-prev.X)+(z-prev.Z)*(z-prev.Z) > 1e-4 {
			inWater := false
			if cache := h.srv.PlayerChunks(h.session); cache != nil {
				inWater = cache.GetBlock(bx, by, bz) == world.StateWater
			}
			if inWater {
				h.srv.EmitPlayerMovementVibration(h.session, server.GameEventSwim, x, y, z)
			} else if onGround {
				h.srv.EmitPlayerMovementVibration(h.session, server.GameEventStep, x, y, z)
			}
		}
		h.srv.HandlePlayerStepOnSculkShrieker(h.session, x, y, z)
		h.srv.StepPressurePlateAt(h.session.Dimension(), bx, by, bz)
		h.srv.OnEntityStepTripwire(h.session.Dimension(), bx, by, bz)
		h.srv.ApplyCampfireContactDamageToPlayer(h.session)
		h.srv.HandlePlayerInsideCauldron(h.session, bx, by, bz, false)
		res, err := h.srv.CheckPlayerPortal(h.session)
		if err != nil {
			return err
		}
		if res.Travelled {
			return nil
		}
	}
	cx := int32(int64(math.Floor(x)) >> 4)
	cz := int32(int64(math.Floor(z)) >> 4)
	if h.streamer != nil {
		h.streamer.requestRecenter(cx, cz)
	}
	return nil
}

func (h *handler) visibilityRadius() int {
	distance := h.viewDistance
	if distance < 2 {
		distance = defaultViewRadius
	}
	if max := h.srv.Config().MaxViewDistance; distance > max {
		distance = max
	}
	return distance
}

// sendPlayLogin writes the clientbound play "login" packet. Field layout was
// confirmed against the 26.1.2 vanilla server capture.
func (h *handler) sendPlayLogin() error {
	if h.srv != nil {
		return h.conn.Send(protocol.PlayLogin, h.srv.EncodePlayerLoginPacket(h.session, int32(h.visibilityRadius())))
	}
	entityID := int32(1)
	gameMode := byte(server.GameModeCreative)
	dim := world.DimensionOverworld
	if h.session != nil {
		snap := h.session.Snapshot()
		entityID = h.session.EntityID
		gameMode = snap.GameMode
		dim = world.NormalizeDimension(snap.Dimension)
	}
	payload := server.EncodePlayLoginPacket(
		entityID,
		gameMode,
		dim,
		20,
		int32(h.visibilityRadius()),
		0,
	)
	return h.conn.Send(protocol.PlayLogin, payload)
}

// sendGameEvent writes a game_event packet (event id + float value).
func (h *handler) sendGameEvent(event byte, value float32) error {
	w := protocol.NewWriter(5)
	w.Byte(event)
	w.Float32(value)
	return h.conn.SendWriter(protocol.PlayGameEvent, w)
}

// sendPlayerPosition teleports the player to their current/spawn position.
// The client must echo the teleport ID back via accept_teleportation.
func (h *handler) sendPlayerPosition(teleportID int32) error {
	x, y, z := spawnX, h.spawnY, spawnZ
	yaw, pitch := float32(0), float32(0)
	if h.session != nil {
		snap := h.session.Snapshot()
		x, y, z = snap.X, snap.Y, snap.Z
		yaw, pitch = snap.Yaw, snap.Pitch
	}
	w := protocol.NewWriter(64)
	w.VarInt(teleportID)
	w.Float64(x).Float64(y).Float64(z) // position
	w.Float64(0).Float64(0).Float64(0) // velocity
	w.Float32(yaw)                     // yaw
	w.Float32(pitch)                   // pitch
	w.Int32(0)                         // relative flags
	return h.conn.SendWriter(protocol.PlayPlayerPosition, w)
}

// keepAliveLoop sends a keep-alive every 15 seconds. It exits as soon as a send
// fails, which happens when the connection closes.
func (h *handler) keepAliveLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	defer func() {
		if h.srv != nil && h.session != nil {
			h.srv.DespawnPlayerFishingBobber(h.session)
			h.srv.WakePlayerFromBed(h.session)
			h.srv.DismountPlayerVehicle(h.session)
			h.srv.CloseLecternIfOpen(h.session)
			_ = h.srv.CloseLoomContainer(h.session, 0, false)
			_ = h.srv.CloseStonecutterContainer(h.session, 0, false)
			_ = h.srv.CloseSmithingContainer(h.session, 0, false)
			_ = h.srv.CloseCartographyContainer(h.session, 0, false)
			h.srv.OnPlayerQuitBossBars(h.session)
			h.srv.BroadcastPlayerInfoRemove(h.session.Profile.UUID)
			h.srv.BroadcastLeaveMessage(h.session)
		}
	}()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			h.keepAliveMu.Lock()
			if h.keepAlivePending {
				timedOut := time.Since(h.keepAliveSent) >= 30*time.Second
				h.keepAliveMu.Unlock()
				if timedOut {
					_ = h.conn.Close()
					return
				}
				continue
			}
			id := time.Now().UnixMilli()
			h.keepAlivePending = true
			h.keepAliveID = id
			h.keepAliveSent = time.Now()
			h.keepAliveMu.Unlock()
			w := protocol.NewWriter(8)
			w.Int64(id)
			if err := h.conn.SendWriter(protocol.PlayKeepAliveCB, w); err != nil {
				return
			}
		}
	}
}

// visibleEntity is the comparable state retained by one client's visibility
// tracker. OnGround is separate because mobs currently always use true.
type visibleEntity struct {
	entity   world.Entity
	onGround bool
}

// entitySyncLoop maintains tab-list membership and chunk-scoped entity state.
func (h *handler) entitySyncLoop() {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			if err := h.syncVisibleEntities(); err != nil {
				return
			}
		}
	}
}

var activeServerTickLoops sync.Map // map[*server.Server]bool

// serverTickLoop drives the 20 TPS brewing stand, world border, jukebox, random
// block, and status effect ticks for h.srv while at least one play connection is active.
func (h *handler) serverTickLoop() {
	if h.srv == nil {
		return
	}
	if _, loaded := activeServerTickLoops.LoadOrStore(h.srv, true); loaded {
		return
	}
	defer activeServerTickLoops.Delete(h.srv)

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	lastGameTime, _ := h.srv.WorldTime()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			if h.srv.Difficulty() == server.DifficultyPeaceful {
				h.srv.DespawnHostileMobs()
			}
			h.srv.TickBrewingStands()
			h.srv.TickHoppers()
			h.srv.TickCrafters()
			h.srv.TickCampfires()
			h.srv.TickAllPlayersCampfireContactDamage()
			h.srv.TickWorldBorder()
			h.srv.TickJukeboxes()
			h.srv.TickBeacons()
			h.srv.TickConduits()
			h.srv.TickAllSculk()
			h.srv.TickCauldrons(nil)
			h.srv.TickVehicles()
			h.srv.TickPortals()
			h.srv.TickPrimedTNT()
			h.srv.TickDispenserProjectiles()
			h.srv.TickAllPlayerStats()
			h.srv.TickAllBlockUpdates()
			if res := h.srv.TickRandomBlocks(); len(res.Changes) > 0 || len(res.LightChanged) > 0 {
				h.broadcastBlockChanges(world.DimensionOverworld, h.srv.Chunks(), res)
			}
			nowGameTime, _ := h.srv.WorldTime()
			if nowGameTime == lastGameTime {
				// StartSpawning is not advancing the world clock; tick player,
				// entity status effects, raids, and boss fights here.
				h.srv.TickAllPlayerEffects()
				h.srv.TickAllEntityEffects()
				h.srv.TickRaids()
				h.srv.TickWithers(nil)
				h.srv.TickEnderDragons(nil)
				h.srv.TickWardens(nil)
			}
			lastGameTime = nowGameTime
		}
	}
}

// syncVisibleEntities performs one deterministic visibility pass. Keeping it
// separate from the ticker makes the four-client workflow integration-testable.
func (h *handler) syncVisibleEntities() error {
	if h.session == nil {
		return nil
	}
	if h.knownPlayers == nil {
		h.knownPlayers = make(map[[16]byte]bool)
	}
	if h.knownEntities == nil {
		h.knownEntities = make(map[int32]visibleEntity)
	}

	viewer := h.session.Snapshot()
	players := h.srv.PlayerSnapshots()
	currentPlayers := make(map[[16]byte]bool, len(players))
	for _, player := range players {
		currentPlayers[player.Profile.UUID] = true
		if !h.knownPlayers[player.Profile.UUID] {
			if err := h.sendPlayerInfoAdd(player.Profile); err != nil {
				return err
			}
			h.knownPlayers[player.Profile.UUID] = true
		}
	}

	currentEntities := make(map[int32]visibleEntity)
	for _, player := range players {
		if player.EntityID == viewer.EntityID || !playerVisible(viewer, player, h.visibilityRadius()) ||
			!h.entityChunkResident(player.X, player.Z) {
			continue
		}
		currentEntities[player.EntityID] = visibleEntity{
			entity: world.Entity{
				ID: player.EntityID, UUID: player.Profile.UUID,
				TypeID: registry.EntityTypeIndex("minecraft:player"), TypeName: "minecraft:player",
				X: player.X, Y: player.Y, Z: player.Z,
				Yaw: player.Yaw, Pitch: player.Pitch, HeadYaw: player.Yaw,
				Dimension: world.NormalizeDimension(player.Dimension),
			},
			onGround: player.OnGround,
		}
	}
	for _, entity := range h.srv.Entities().All() {
		if entityVisible(viewer, entity, h.visibilityRadius()) && h.entityChunkResident(entity.X, entity.Z) {
			currentEntities[entity.ID] = visibleEntity{entity: entity, onGround: true}
		}
	}

	for id, current := range currentEntities {
		known, exists := h.knownEntities[id]
		if !exists {
			entity := current.entity
			if err := h.sendAddEntity(&entity); err != nil {
				return err
			}
			if entity.IsItem() && !entity.IsExperienceOrb() {
				if err := h.conn.Send(protocol.PlaySetEntityData, server.EncodeItemEntityData(entity.ID, entity.Item)); err != nil {
					return err
				}
			}
			if entity.IsVillager() && h.srv != nil {
				if err := h.conn.Send(protocol.PlaySetEntityData, h.srv.VillagerEntityDataPacket(entity.ID)); err != nil {
					return err
				}
			}
			if server.IsBossOrBossSupportEntity(entity.TypeName) && h.srv != nil {
				if metaPkt := h.srv.BossOrSupportEntityDataPacket(entity.ID, entity.TypeName); len(metaPkt) > 0 {
					if err := h.conn.Send(protocol.PlaySetEntityData, metaPkt); err != nil {
						return err
					}
				}
			}
			if server.IsDecorationEntity(entity.TypeName) && h.srv != nil {
				if metaPkt := h.srv.DecorationEntityDataPacket(entity.ID); len(metaPkt) > 0 {
					if err := h.conn.Send(protocol.PlaySetEntityData, metaPkt); err != nil {
						return err
					}
				}
				if entity.TypeName == server.EntityTypeArmorStand {
					if eqPkt := h.srv.ArmorStandEquipmentPacket(entity.ID); len(eqPkt) > 0 {
						if err := h.conn.Send(protocol.PlaySetEquipment, eqPkt); err != nil {
							return err
						}
					}
				}
			}
			if entity.TypeName == server.MobTypeWarden && h.srv != nil {
				if metaPkt := h.srv.WardenEntityDataPacket(entity.ID); len(metaPkt) > 0 {
					if err := h.conn.Send(protocol.PlaySetEntityData, metaPkt); err != nil {
						return err
					}
				}
			}
			if entity.IsVehicle() && h.srv != nil {
				if metaPkt := server.EncodeVehicleEntityDataPacket(entity.ID, 0, 1, 0.0); len(metaPkt) > 0 {
					if err := h.conn.Send(protocol.PlaySetEntityData, metaPkt); err != nil {
						return err
					}
				}
				if passengers := h.srv.VehiclePassengers(entity.ID); len(passengers) > 0 {
					if err := h.conn.Send(protocol.PlaySetPassengers, server.EncodeVehiclePassengersPacket(entity.ID, passengers)); err != nil {
						return err
					}
				}
			}
		} else if known != current {
			entity := current.entity
			if err := h.sendEntityTeleportState(&entity, current.onGround); err != nil {
				return err
			}
		}
	}
	for id := range h.knownEntities {
		if _, visible := currentEntities[id]; !visible {
			if err := h.sendRemoveEntity(id); err != nil {
				return err
			}
		}
	}
	h.knownEntities = currentEntities

	for uuid := range h.knownPlayers {
		if !currentPlayers[uuid] {
			if err := h.sendPlayerInfoRemove(uuid); err != nil {
				return err
			}
			delete(h.knownPlayers, uuid)
		}
	}
	return nil
}

func (h *handler) entityChunkResident(x, z float64) bool {
	if h.streamer == nil {
		return true
	}
	cx := int32(int64(math.Floor(x)) >> 4)
	cz := int32(int64(math.Floor(z)) >> 4)
	return h.streamer.isResident(cx, cz)
}

func playerVisible(viewer, target server.PlayerSnapshot, radius int) bool {
	if world.NormalizeDimension(viewer.Dimension) != world.NormalizeDimension(target.Dimension) {
		return false
	}
	return chunksWithin(viewer.X, viewer.Z, target.X, target.Z, radius)
}

func entityVisible(viewer server.PlayerSnapshot, target world.Entity, radius int) bool {
	if world.NormalizeDimension(viewer.Dimension) != target.EffectiveDimension() {
		return false
	}
	return chunksWithin(viewer.X, viewer.Z, target.X, target.Z, radius)
}

func chunksWithin(ax, az, bx, bz float64, radius int) bool {
	acx := int32(int64(math.Floor(ax)) >> 4)
	acz := int32(int64(math.Floor(az)) >> 4)
	bcx := int32(int64(math.Floor(bx)) >> 4)
	bcz := int32(int64(math.Floor(bz)) >> 4)
	dx := acx - bcx
	if dx < 0 {
		dx = -dx
	}
	dz := acz - bcz
	if dz < 0 {
		dz = -dz
	}
	return dx <= int32(radius) && dz <= int32(radius)
}

func (h *handler) sendPlayerInfoAdd(profile server.Profile) error {
	w := protocol.NewWriter(64)
	w.Byte(0xff) // All eight initialization actions, fixed 8-bit EnumSet.
	w.VarInt(1)
	w.UUID(profile.UUID)
	w.String(profile.Name)
	w.VarInt(0)   // profile properties
	w.Bool(false) // no signed chat session
	w.VarInt(1)   // creative game mode
	w.Bool(true)  // listed
	w.VarInt(0)   // latency
	w.Bool(false) // no custom display name
	w.VarInt(0)   // list order
	w.Bool(true)  // show hat
	return h.conn.SendWriter(protocol.PlayPlayerInfoUpdate, w)
}

func (h *handler) sendPlayerInfoRemove(uuid [16]byte) error {
	w := protocol.NewWriter(20)
	w.VarInt(1)
	w.UUID(uuid)
	return h.conn.SendWriter(protocol.PlayPlayerInfoRemove, w)
}

// sendAddEntity sends the minecraft:add_entity packet.
func (h *handler) sendAddEntity(e *world.Entity) error {
	if h.srv != nil && server.IsDecorationEntity(e.TypeName) {
		return h.conn.Send(protocol.PlayAddEntity, h.srv.EncodeDecorationAddEntityPacket(nil, e))
	}
	w := protocol.NewWriter(64)
	w.VarInt(e.ID)
	w.UUID(e.UUID)
	w.VarInt(int32(e.TypeID))
	w.Float64(e.X).Float64(e.Y).Float64(e.Z)
	w.LPVec3(
		float64(e.VelocityX)/8000.0,
		float64(e.VelocityY)/8000.0,
		float64(e.VelocityZ)/8000.0,
	)
	w.Byte(byte(e.Pitch * 256.0 / 360.0))
	w.Byte(byte(e.Yaw * 256.0 / 360.0))
	w.Byte(byte(e.HeadYaw * 256.0 / 360.0))
	w.VarInt(0) // Data
	return h.conn.SendWriter(protocol.PlayAddEntity, w)
}

func (h *handler) sendEntityTeleport(e *world.Entity) error {
	return h.sendEntityTeleportState(e, true)
}

func (h *handler) sendEntityTeleportState(e *world.Entity, onGround bool) error {
	w := protocol.NewWriter(64)
	w.VarInt(e.ID)
	// PositionMoveRotation: position, deltaMovement, yRot, xRot.
	w.Float64(e.X).Float64(e.Y).Float64(e.Z)
	w.Float64(float64(e.VelocityX) / 8000.0)
	w.Float64(float64(e.VelocityY) / 8000.0)
	w.Float64(float64(e.VelocityZ) / 8000.0)
	w.Float32(e.Yaw)
	w.Float32(e.Pitch)
	w.Int32(0) // Relative.SET_STREAM_CODEC uses ByteBufCodecs.INT; no relative flags.
	w.Bool(onGround)
	return h.conn.SendWriter(protocol.PlayTeleportEntity, w)
}

func (h *handler) sendRemoveEntity(id int32) error {
	w := protocol.NewWriter(16)
	w.VarInt(1) // count
	w.VarInt(id)
	return h.conn.SendWriter(protocol.PlayRemoveEntities, w)
}

// handlePlay dispatches serverbound play packets. Most are tolerated for now;
// teleport and keep-alive are acknowledged/logged.
func (h *handler) handlePlay(pkt protocol.Packet) error {
	switch pkt.ID {
	case protocol.PlayAcceptTeleport:
		id, err := pkt.Body().VarInt()
		if err != nil {
			return err
		}
		h.log.Debug("teleport confirmed", "id", id)
		return nil

	case protocol.PlayKeepAliveServer:
		id, err := pkt.Body().Int64()
		if err != nil {
			return err
		}
		h.keepAliveMu.Lock()
		valid := h.keepAlivePending && id == h.keepAliveID
		sentAt := h.keepAliveSent
		if valid {
			h.keepAlivePending = false
		}
		h.keepAliveMu.Unlock()
		if !valid {
			return errors.New("unexpected keep-alive response")
		}
		if h.srv != nil && h.session != nil && !sentAt.IsZero() {
			h.srv.SetPlayerLatency(h.session, int32(time.Since(sentAt).Milliseconds()))
		}
		h.log.Debug("keep-alive ack", "id", id)
		return nil

	case protocol.PlayPlayerLoaded:
		h.log.Info("player loaded into world", "name", h.conn.Profile.Name)
		return nil

	case protocol.PlayMovePos, protocol.PlayMovePosRot:
		r := pkt.Body()
		x, err := r.Float64()
		if err != nil {
			return err
		}
		y, err := r.Float64() // feet Y
		if err != nil {
			return err
		}
		z, err := r.Float64()
		if err != nil {
			return err
		}
		snapshot := h.session.Snapshot()
		yaw, pitch := snapshot.Yaw, snapshot.Pitch
		if pkt.ID == protocol.PlayMovePosRot {
			yaw, err = r.Float32()
			if err != nil {
				return err
			}
			pitch, err = r.Float32()
			if err != nil {
				return err
			}
		}
		flags, err := r.ReadByte()
		if err != nil {
			return err
		}
		return h.onPlayerMove(x, y, z, yaw, pitch, flags&1 != 0)

	case protocol.PlayMoveRot:
		r := pkt.Body()
		yaw, err := r.Float32()
		if err != nil {
			return err
		}
		pitch, err := r.Float32()
		if err != nil {
			return err
		}
		flags, err := r.ReadByte()
		if err != nil {
			return err
		}
		snapshot := h.session.Snapshot()
		return h.onPlayerMove(snapshot.X, snapshot.Y, snapshot.Z, yaw, pitch, flags&1 != 0)

	case protocol.PlayMoveStatusOnly:
		flags, err := pkt.Body().ReadByte()
		if err != nil {
			return err
		}
		snapshot := h.session.Snapshot()
		return h.onPlayerMove(snapshot.X, snapshot.Y, snapshot.Z, snapshot.Yaw, snapshot.Pitch, flags&1 != 0)

	case protocol.PlayPlayerAction:
		return h.handlePlayerAction(pkt)

	case protocol.PlayClientCommand:
		return h.handleClientCommand(pkt)

	case protocol.PlayChatMessage:
		return h.handleChat(pkt)

	case protocol.PlayChatCommand, protocol.PlayChatCommandSigned:
		return h.handleChatCommand(pkt)

	case protocol.PlayUseItemOn:
		return h.handleUseItemOn(pkt)

	case server.PlayAttackSB:
		if h.srv != nil {
			_ = h.srv.HandlePlayerAttackPacket(h.session, pkt.Body())
			h.syncHotbarFromSession()
		}
		return nil

	case server.PlayInteractSB:
		if h.srv != nil {
			_ = h.srv.HandlePlayerInteractPacket(h.session, pkt.Body())
			h.syncHotbarFromSession()
		}
		return nil

	case server.PlayUseItemSB:
		if h.srv != nil && h.session != nil {
			_ = h.srv.TryCollectDragonBreath(h.session)
			if hand, err := pkt.Body().VarInt(); err == nil {
				_ = h.srv.UseProjectileItem(h.session, hand)
			}
			if err := h.srv.HandleUseItemPacket(h.session, pkt.Body()); err != nil {
				return err
			}
			h.syncHotbarFromSession()
		}
		return nil

	case server.PlayPlayerCommandSB:
		if h.srv != nil {
			if _, action, jumpBoost, decErr := server.DecodePlayerCommandPacket(pkt.Body()); decErr == nil && h.session != nil {
				switch action {
				case 3: // START_RIDING_JUMP
					if jumpBoost >= 1 && jumpBoost <= 100 {
						if mountID := h.srv.PlayerMountedEquine(h.session); mountID > 0 && h.srv.EquineRider(mountID) == h.session.EntityID {
							_, _, _, _ = h.srv.ExecuteEquineJump(mountID, jumpBoost)
						} else if camelID := h.srv.PlayerMountedCamel(h.session); camelID > 0 && h.srv.CamelDriver(camelID) == h.session.EntityID {
							_, _, _, _ = h.srv.ExecuteCamelDash(camelID, int(jumpBoost))
						}
					}
				case 5: // OPEN_INVENTORY (also legacy test action for camel dash when jumpBoost > 0)
					if mountID := h.srv.PlayerMountedEquine(h.session); mountID > 0 && h.srv.EquineRider(mountID) == h.session.EntityID {
						if jumpBoost >= 1 && jumpBoost <= 100 {
							_, _, _, _ = h.srv.ExecuteEquineJump(mountID, jumpBoost)
						} else if h.srv.IsMobTamed(mountID) {
							_, _ = h.srv.OpenEquineInventory(h.session, mountID)
						}
					} else if camelID := h.srv.PlayerMountedCamel(h.session); camelID > 0 && h.srv.CamelDriver(camelID) == h.session.EntityID && jumpBoost > 0 {
						_, _, _, _ = h.srv.ExecuteCamelDash(camelID, int(jumpBoost))
					}
				}
			}
			_ = h.srv.HandlePlayerCommandPacket(h.session, pkt.Body())
			if h.session != nil && !h.srv.IsPlayerSleeping(h.session) {
				h.srv.ClearPlayerBedOccupancy(h.session)
			}
		}
		return nil

	case protocol.PlaySetCarriedItem:
		slot, err := pkt.Body().Uint16()
		if err != nil {
			return err
		}
		if slot < world.HotbarSlotCount {
			h.heldSlot = int32(slot)
			if h.session != nil {
				h.session.SetSelectedSlot(int32(slot))
				h.session.SetBlockingWithShield(false)
				if h.srv != nil {
					h.srv.CancelEating(h.session)
					h.srv.DespawnPlayerFishingBobber(h.session)
				}
				h.session.ResetAttackCooldown()
			}
		}
		return nil

	case protocol.PlaySetCreativeSlot:
		return h.handleCreativeSlot(pkt)

	case protocol.PlayContainerClick:
		return h.handleContainerClick(pkt)

	case protocol.PlayContainerCloseSB:
		return h.handleContainerClose(pkt)

	case protocol.PlayContainerButtonClick:
		cid, btn, err := server.DecodeContainerButtonClickPacket(pkt.Body())
		if err != nil {
			return err
		}
		if h.srv != nil && h.session != nil {
			if handled, err := h.srv.HandleLecternButtonClick(h.session, cid, btn); handled {
				h.syncHotbarFromSession()
				return err
			}
			if handled, err := h.srv.HandleLoomButtonClick(h.session, cid, btn); handled {
				h.syncHotbarFromSession()
				return err
			}
			if handled, err := h.srv.HandleStonecutterButtonClick(h.session, cid, btn); handled {
				h.syncHotbarFromSession()
				return err
			}
			prevSeed := h.session.EnchantmentSeed()
			if err := h.srv.HandleContainerButtonClick(h.session, cid, btn); err != nil {
				return err
			}
			if h.session.EnchantmentSeed() != prevSeed {
				h.srv.AwardCustomStat(h.session, server.StatEnchantItem, 1)
			}
			h.syncHotbarFromSession()
		}
		return nil

	case protocol.PlayEditBook:
		if h.srv != nil && h.session != nil {
			if err := h.srv.HandleEditBookPacket(h.session, pkt.Body()); err != nil {
				return err
			}
			h.syncHotbarFromSession()
		}
		return nil

	case protocol.PlaySignUpdate:
		if h.srv != nil && h.session != nil {
			return h.srv.HandleSignUpdatePacket(h.session, pkt.Body())
		}
		return nil

	case protocol.PlayRenameItem:
		name, err := server.DecodeRenameItemPacket(pkt.Body())
		if err != nil {
			return err
		}
		if h.srv != nil && h.session != nil {
			if err := h.srv.HandleRenameItem(h.session, name); err != nil {
				return err
			}
			h.syncHotbarFromSession()
		}
		return nil

	case protocol.PlaySelectTrade:
		if h.srv != nil && h.session != nil {
			if err := h.srv.HandleSelectTradePacket(h.session, pkt.Body()); err != nil {
				return err
			}
			h.syncHotbarFromSession()
		}
		return nil

	case server.PlaySetBeaconSB:
		prim, sec, err := server.DecodeSetBeaconPacket(pkt.Body())
		if err != nil {
			return err
		}
		if h.srv != nil && h.session != nil {
			if err := h.srv.HandleSetBeaconPacket(h.session, prim, sec); err != nil {
				return err
			}
			h.syncHotbarFromSession()
		}
		return nil

	case server.PlaySeenAdvancementsSB:
		action, tab, err := server.DecodeSeenAdvancementsPacket(pkt.Body())
		if err != nil {
			return err
		}
		if h.srv != nil && h.session != nil {
			return h.srv.HandleSeenAdvancements(h.session, action, tab)
		}
		return nil

	case protocol.PlayPlaceRecipe:
		cid, displayID, useMax, err := server.DecodePlaceRecipePacket(pkt.Body())
		if err != nil {
			return err
		}
		if h.srv != nil && h.session != nil {
			if err := h.srv.HandlePlaceRecipe(h.session, cid, displayID, useMax); err != nil {
				return err
			}
			h.syncHotbarFromSession()
		}
		return nil

	case protocol.PlayRecipeBookChangeSettings:
		bookType, isOpen, isFiltering, err := server.DecodeRecipeBookChangeSettingsPacket(pkt.Body())
		if err != nil {
			return err
		}
		if h.srv != nil && h.session != nil {
			return h.srv.HandleRecipeBookChangeSettings(h.session, bookType, isOpen, isFiltering)
		}
		return nil

	case protocol.PlayRecipeBookSeenRecipe:
		displayID, err := server.DecodeRecipeBookSeenRecipePacket(pkt.Body())
		if err != nil {
			return err
		}
		if h.srv != nil && h.session != nil {
			return h.srv.HandleRecipeBookSeenRecipe(h.session, displayID)
		}
		return nil

	case protocol.PlayContainerSlotStateChanged:
		if h.srv != nil && h.session != nil {
			r := pkt.Body()
			slotID, err := r.VarInt()
			if err != nil {
				return err
			}
			containerID, err := r.VarInt()
			if err != nil {
				return err
			}
			newState, err := r.Bool()
			if err != nil {
				return err
			}
			if open := h.session.OpenContainer(); open != nil && open.MenuType == server.CrafterMenuType && open.ContainerID == containerID {
				dim := h.session.Dimension()
				if dim == "" {
					dim = world.DimensionOverworld
				}
				if h.srv.SetCrafterSlotDisabled(dim, open.BlockX, open.BlockY, open.BlockZ, int(slotID), !newState) {
					h.srv.SyncCrafterViewers(h.session, open.BlockX, open.BlockY, open.BlockZ)
				}
			}
		}
		return nil

	case protocol.PlayChangeDifficultySB:
		if h.srv != nil {
			return h.srv.HandleChangeDifficultyPacket(h.session, pkt.Body())
		}
		return nil

	case protocol.PlayLockDifficultySB:
		if h.srv != nil {
			return h.srv.HandleLockDifficultyPacket(h.session, pkt.Body())
		}
		return nil

	case protocol.PlayPlayerInputSB:
		if h.srv != nil && h.session != nil {
			return h.srv.HandlePlayerInputPacket(h.session, pkt.Body())
		}
		return nil

	case protocol.PlayMoveVehicleSB:
		if h.srv != nil && h.session != nil {
			return h.srv.HandleMoveVehiclePacket(h.session, pkt.Body())
		}
		return nil

	case protocol.PlayPaddleBoatSB:
		if h.srv != nil && h.session != nil {
			return h.srv.HandlePaddleBoatPacket(h.session, pkt.Body())
		}
		return nil

	default:
		h.log.Debug("play packet ignored", "id", pkt.ID)
		return nil
	}
}

func (h *handler) handleContainerClick(pkt protocol.Packet) error {
	click, err := server.DecodeContainerClickPacket(pkt.Body())
	if err != nil {
		return err
	}
	if h.srv != nil && h.session != nil {
		if err := h.srv.HandleMerchantContainerClick(h.session, click.ContainerID, int(click.Slot), click.Button, click.Mode); err != nil {
			return err
		}
		h.syncHotbarFromSession()
	}
	return nil
}

func (h *handler) handleContainerClose(pkt protocol.Packet) error {
	cid, err := pkt.Body().VarInt()
	if err != nil {
		return err
	}
	if h.srv != nil && h.session != nil {
		h.srv.CloseLecternIfOpen(h.session)
		if open := h.session.OpenContainer(); open != nil && open.MenuType == server.MenuTypeCartographyTable {
			bx, by, bz := open.BlockX, open.BlockY, open.BlockZ
			if err := h.srv.CloseCartographyContainer(h.session, cid, false); err != nil {
				return err
			}
			h.srv.EmitPlayerBlockVibration(h.session, server.GameEventContainerClose, h.session.Dimension(), bx, by, bz, "")
		} else if open != nil && open.MenuType == server.MenuTypeLoom {
			bx, by, bz := open.BlockX, open.BlockY, open.BlockZ
			if err := h.srv.CloseLoomContainer(h.session, cid, false); err != nil {
				return err
			}
			h.srv.EmitPlayerBlockVibration(h.session, server.GameEventContainerClose, h.session.Dimension(), bx, by, bz, "")
		} else if open != nil && open.MenuType == server.MenuTypeStonecutter {
			bx, by, bz := open.BlockX, open.BlockY, open.BlockZ
			if err := h.srv.CloseStonecutterContainer(h.session, cid, false); err != nil {
				return err
			}
			h.srv.EmitPlayerBlockVibration(h.session, server.GameEventContainerClose, h.session.Dimension(), bx, by, bz, "")
		} else if open != nil && open.MenuType == server.MenuTypeSmithing {
			bx, by, bz := open.BlockX, open.BlockY, open.BlockZ
			if err := h.srv.CloseSmithingContainer(h.session, cid, false); err != nil {
				return err
			}
			h.srv.EmitPlayerBlockVibration(h.session, server.GameEventContainerClose, h.session.Dimension(), bx, by, bz, "")
		} else if err := h.srv.CloseMerchantContainer(h.session, cid, false); err != nil {
			return err
		}
		h.syncHotbarFromSession()
	}
	return nil
}

func (h *handler) syncHotbarFromSession() {
	if h.session == nil {
		return
	}
	inv := h.session.Inventory()
	for i := 0; i < world.HotbarSlotCount; i++ {
		st := inv[world.HotbarContainerOffset+i]
		if st.IsEmpty() {
			h.hotbar[i] = -1
		} else {
			h.hotbar[i] = st.ResolvedItemID()
		}
	}
}

func (h *handler) handleClientCommand(pkt protocol.Packet) error {
	action, err := pkt.Body().VarInt()
	if err != nil {
		return err
	}
	if h.srv != nil && h.session != nil {
		switch action {
		case server.ClientCommandPerformRespawn:
			if err := h.srv.RespawnPlayer(h.session); err != nil {
				return err
			}
			if h.conn != nil {
				snap := h.session.Snapshot()
				return h.sendSetHealth(snap.Health, snap.FoodLevel, snap.FoodSaturation)
			}
		case server.ClientCommandRequestStats:
			return h.srv.HandleRequestStats(h.session)
		}
	}
	return nil
}

// handleChat reads a chat message (only the leading text field is needed) and
// broadcasts it via the server's formatted chat system.
func (h *handler) handleChat(pkt protocol.Packet) error {
	msg, err := pkt.Body().String()
	if err != nil {
		return err
	}
	line := "<" + h.conn.Profile.Name + "> " + msg
	h.log.Info("chat", "msg", line)
	if h.srv != nil && h.session != nil {
		h.srv.BroadcastPlayerChat(h.session, msg)
		return nil
	}
	return h.broadcastSystemChat(line)
}

func (h *handler) broadcastSystemChat(text string) error {
	w := protocol.NewWriter(len(text) + 8)
	w.Raw(nbt.Marshal(nbt.String(text)))
	w.Bool(false)
	h.srv.Broadcast(protocol.PlaySystemChat, w.Bytes())
	return nil
}

// handlePlayerAction processes digging and item-use release. In creative the
// client sends START_DESTROY_BLOCK (status 0) for an instant break; survival
// sends STOP_DESTROY_BLOCK (status 2) when digging finishes and spawns an item
// drop; RELEASE_USE_ITEM (status 5) aborts active food consumption.
func (h *handler) handlePlayerAction(pkt protocol.Packet) error {
	r := pkt.Body()
	status, err := r.VarInt()
	if err != nil {
		return err
	}
	if status == server.PlayerActionReleaseUseItem {
		if h.session != nil {
			h.session.SetBlockingWithShield(false)
		}
		if h.srv != nil {
			h.srv.CancelEating(h.session)
			if h.srv.ReleaseProjectileWeapon(h.session) {
				h.syncHotbarFromSession()
			}
		}
		if r.Remaining() == 0 {
			return nil
		}
	}
	x, y, z, err := r.Position()
	if err != nil {
		return err
	}
	if _, err := r.ReadByte(); err != nil { // face
		return err
	}
	seq, err := r.VarInt()
	if err != nil {
		return err
	}
	if status == server.PlayerActionReleaseUseItem {
		return h.sendBlockChangedAck(seq)
	}

	if h.srv != nil {
		if broken, _ := h.srv.BreakBlock(h.session, x, y, z, status); broken {
			h.syncHotbarFromSession()
			h.log.Debug("block broken", "x", x, "y", y, "z", z)
		}
	}
	return h.sendBlockChangedAck(seq)
}

// hotbarInvStart is the inventory slot index of hotbar slot 0.
const hotbarInvStart = world.HotbarContainerOffset

// handleCreativeSlot records the item a creative player placed into a slot so we
// know what block to place and keeps the player's container synchronized.
// The packet is: Short slot, then an item stack (VarInt count; if non-empty,
// VarInt item id followed by components we ignore).
func (h *handler) handleCreativeSlot(pkt protocol.Packet) error {
	r := pkt.Body()
	slotRaw, err := r.Uint16()
	if err != nil {
		return err
	}
	count, err := r.VarInt()
	if err != nil {
		return err
	}
	var stack world.ItemStack
	if count > 0 {
		itemID, err := r.VarInt()
		if err != nil {
			return err
		}
		stack = world.NewItemStack(itemID, count)
	}

	slot := int(int16(slotRaw))
	if hotbarIdx := slot - hotbarInvStart; hotbarIdx >= 0 && hotbarIdx < len(h.hotbar) {
		if stack.IsEmpty() {
			h.hotbar[hotbarIdx] = -1
		} else {
			h.hotbar[hotbarIdx] = stack.ResolvedItemID()
		}
	}
	if slot >= 0 && slot < world.PlayerInventorySlots {
		if h.session != nil {
			h.session.SetInventorySlot(slot, stack)
			if h.srv != nil {
				h.srv.TriggerInventoryChanged(h.session)
				h.srv.UnlockRecipesForItem(h.session, stack)
			}
		}
		if h.conn != nil {
			if err := h.sendContainerSetSlot(slot, stack); err != nil {
				return err
			}
		}
	}
	return nil
}

// faceOffsets maps a Direction (block face) to the unit offset of the block
// placed against it: DOWN, UP, NORTH, SOUTH, WEST, EAST.
var faceOffsets = [6][3]int{
	{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0},
}

// handleUseItemOn places the held block against the clicked face. Layout
// (captured from the client): Hand, Position, Face, cursor XYZ floats,
// insideBlock bool, worldBorderHit bool, sequence.
func (h *handler) handleUseItemOn(pkt protocol.Packet) error {
	r := pkt.Body()
	hand, err := r.VarInt()
	if err != nil {
		return err
	}
	x, y, z, err := r.Position()
	if err != nil {
		return err
	}
	face, err := r.VarInt()
	if err != nil {
		return err
	}
	cursorX, err := r.Float32()
	if err != nil {
		return err
	}
	cursorY, err := r.Float32()
	if err != nil {
		return err
	}
	cursorZ, err := r.Float32()
	if err != nil {
		return err
	}
	if _, err := r.Bool(); err != nil {
		return err
	}
	if _, err := r.Bool(); err != nil {
		return err
	}
	seq, err := r.VarInt()
	if err != nil {
		return err
	}

	if h.srv != nil && h.session != nil {
		if h.srv.UseMapItemOn(h.session, hand, x, y, z) {
			h.syncHotbarFromSession()
			return h.sendBlockChangedAck(seq)
		}
		if cache := h.srv.PlayerChunks(h.session); cache != nil {
			clickedState := cache.GetBlock(x, y, z)
			if server.IsChiseledBookshelfBlock(clickedState) && !h.session.IsSneaking() {
				invSlot := h.session.HeldInventorySlot()
				if invSlot >= 0 && h.session.InventorySlot(invSlot).IsEmpty() && hand == 0 && h.heldSlot >= 0 && int(h.heldSlot) < len(h.hotbar) && h.hotbar[h.heldSlot] > 0 {
					h.session.SetInventorySlot(invSlot, world.NewItemStack(h.hotbar[h.heldSlot], 1))
				}
				if handled, _ := h.srv.InteractChiseledBookshelf(h.session, h.session.Dimension(), x, y, z, server.BlockFaceDirectionName(face), float64(cursorX), float64(cursorY), float64(cursorZ)); handled {
					h.syncHotbarFromSession()
					return h.sendBlockChangedAck(seq)
				}
			}
			if server.IsLecternBlock(clickedState) {
				if h.srv.InteractWithLectern(h.session, hand, x, y, z) {
					h.syncHotbarFromSession()
					return h.sendBlockChangedAck(seq)
				}
			}
			if server.IsSignBlock(clickedState) {
				if h.srv.InteractWithSign(h.session, hand, x, y, z, true) {
					h.syncHotbarFromSession()
					return h.sendBlockChangedAck(seq)
				}
			}
			if server.IsJukeboxBlock(clickedState) {
				invSlot := h.session.HeldInventorySlot()
				if invSlot >= 0 && h.session.InventorySlot(invSlot).IsEmpty() && hand == 0 && h.heldSlot >= 0 && int(h.heldSlot) < len(h.hotbar) && h.hotbar[h.heldSlot] > 0 {
					h.session.SetInventorySlot(invSlot, world.NewItemStack(h.hotbar[h.heldSlot], 1))
				}
				if handled, res := h.srv.InteractJukebox(h.session, hand, x, y, z); handled {
					h.broadcastBlockChanges(h.session.Dimension(), cache, res)
					h.syncHotbarFromSession()
					return h.sendBlockChangedAck(seq)
				}
			}
			if world.IsNoteBlock(clickedState) {
				if handled, res := h.srv.InteractNoteBlock(h.session, x, y, z); handled {
					h.broadcastBlockChanges(h.session.Dimension(), cache, res)
					return h.sendBlockChangedAck(seq)
				}
			}
			if server.IsCauldronBlock(clickedState) {
				invSlot := h.session.HeldInventorySlot()
				if hand == server.HandOffHand {
					invSlot = server.OffHandContainerSlot
				}
				if invSlot >= 0 && h.session.InventorySlot(invSlot).IsEmpty() && hand == 0 && h.heldSlot >= 0 && int(h.heldSlot) < len(h.hotbar) && h.hotbar[h.heldSlot] > 0 {
					h.session.SetInventorySlot(invSlot, world.NewItemStack(h.hotbar[h.heldSlot], 1))
				}
				var prevItem string
				var prevHasDyed bool
				if invSlot >= 0 {
					prevItem = h.session.InventorySlot(invSlot).ResolvedID()
					prevHasDyed = server.HasSlotDyedColor(h.session, invSlot)
				}
				if handled, _ := h.srv.InteractCauldronAtInDim(h.session, h.session.Dimension(), x, y, z, invSlot); handled {
					h.srv.RecordCauldronInteractionStat(h.session, prevItem, prevHasDyed, clickedState)
					h.syncHotbarFromSession()
					if invSlot >= 0 && invSlot < world.PlayerInventorySlots && h.conn != nil {
						_ = h.sendContainerSetSlot(invSlot, h.session.InventorySlot(invSlot))
					}
					return h.sendBlockChangedAck(seq)
				}
				if world.BlockName(clickedState) == "minecraft:water_cauldron" {
					if h.srv.CleanBannerInCauldron(h.session, hand, x, y, z) {
						h.srv.AwardCustomStat(h.session, server.StatCleanBanner, 1)
						h.syncHotbarFromSession()
						return h.sendBlockChangedAck(seq)
					}
				}
			}
		}
		opened, _, err := h.srv.InteractBlock(h.session, x, y, z)
		if err != nil {
			return err
		}
		if opened {
			h.syncHotbarFromSession()
			return h.sendBlockChangedAck(seq)
		}
	}

	if h.srv != nil {
		dim := world.DimensionOverworld
		if h.session != nil {
			dim = h.session.Dimension()
		}
		if cache := h.srv.PlayerChunks(h.session); cache != nil {
			if handled, res := cache.InteractRedstoneBlock(x, y, z); handled {
				h.broadcastBlockChanges(dim, cache, res)
				newState := cache.GetBlock(x, y, z)
				evType := server.GameEventBlockDeactivate
				if server.IsBlockActivatedState(newState) {
					evType = server.GameEventBlockActivate
				}
				h.srv.EmitPlayerBlockVibration(h.session, evType, dim, x, y, z, world.BlockName(newState))
				return h.sendBlockChangedAck(seq)
			}
			if h.session != nil && !h.session.IsDead() {
				invSlot := h.session.HeldInventorySlot()
				if hand == server.HandOffHand {
					invSlot = server.OffHandContainerSlot
				}
				if invSlot >= 0 && h.session.InventorySlot(invSlot).IsEmpty() && hand == 0 && h.heldSlot >= 0 && int(h.heldSlot) < len(h.hotbar) && h.hotbar[h.heldSlot] > 0 {
					h.session.SetInventorySlot(invSlot, world.NewItemStack(h.hotbar[h.heldSlot], 1))
				}
				heldItem := h.session.InventorySlot(invSlot)
				if server.IsEnderEyeItem(heldItem) && world.IsEndPortalFrameState(cache.GetBlock(x, y, z)) {
					if placedEye, _, _, err := h.srv.UseEyeOfEnderOnFrame(h.session, x, y, z); err != nil {
						return err
					} else if placedEye {
						h.syncHotbarFromSession()
						return h.sendBlockChangedAck(seq)
					}
				}
				if heldItem.ResolvedID() == server.EndCrystalItemID {
					if _, placed := h.srv.PlaceEndCrystalItem(h.session, dim, x, y, z); placed {
						h.syncHotbarFromSession()
						return h.sendBlockChangedAck(seq)
					}
				}
				if heldItem.ResolvedID() == "minecraft:glass_bottle" {
					if h.srv.TryCollectDragonBreath(h.session) {
						h.syncHotbarFromSession()
						return h.sendBlockChangedAck(seq)
					}
				}
				if world.BlockName(cache.GetBlock(x, y, z)) == "minecraft:sweet_berry_bush" && heldItem.ResolvedID() != "minecraft:bone_meal" {
					if berries, res := h.srv.HarvestSweetBerries(h.session, x, y, z, nil); berries > 0 {
						h.broadcastBlockChanges(dim, cache, res)
						h.syncHotbarFromSession()
						return h.sendBlockChangedAck(seq)
					}
				}
				if consumed, res := h.srv.UseBoneMeal(h.session, x, y, z, invSlot, nil); consumed {
					h.broadcastBlockChanges(dim, cache, res)
					h.syncHotbarFromSession()
					return h.sendBlockChangedAck(seq)
				}
				if tilled, res := h.srv.TillBlockWithHoe(h.session, x, y, z, invSlot, nil); tilled {
					h.broadcastBlockChanges(dim, cache, res)
					h.syncHotbarFromSession()
					return h.sendBlockChangedAck(seq)
				}
				if face >= 0 && int(face) < len(faceOffsets) {
					if planted, _, res := h.srv.PlantSeedOrCrop(h.session, x, y, z, int(face), invSlot); planted {
						h.broadcastBlockChanges(dim, cache, res)
						h.syncHotbarFromSession()
						return h.sendBlockChangedAck(seq)
					}
				}
			}
		}
	}

	if face >= 0 && int(face) < len(faceOffsets) && (h.session == nil || !h.session.IsDead()) {
		if h.isHoldingFlintAndSteel() && h.srv != nil {
			if h.session != nil && h.session.HeldItem().IsEmpty() && h.heldSlot >= 0 && int(h.heldSlot) < len(h.hotbar) {
				if flintID, ok := world.ItemNameToID("minecraft:flint_and_steel"); ok && h.hotbar[h.heldSlot] == flintID {
					h.session.SetInventorySlot(int(world.HotbarContainerOffset+h.heldSlot), world.NewItemStack(flintID, 1))
				}
			}
			lit, used, err := h.srv.UseFlintAndSteel(h.session, x, y, z, int(face))
			if err != nil {
				return err
			}
			if lit || used {
				return h.sendBlockChangedAck(seq)
			}
		}
		if h.srv != nil && h.session != nil {
			invSlot := h.session.HeldInventorySlot()
			if hand == server.HandOffHand {
				invSlot = server.OffHandContainerSlot
			}
			if invSlot >= 0 && h.session.InventorySlot(invSlot).IsEmpty() && hand == 0 && h.heldSlot >= 0 && int(h.heldSlot) < len(h.hotbar) && h.hotbar[h.heldSlot] > 0 {
				h.session.SetInventorySlot(invSlot, world.NewItemStack(h.hotbar[h.heldSlot], 1))
			}
			if rocketID, launched := h.srv.UseFireworkRocketOnBlock(h.session, hand, x, y, z, face, cursorX, cursorY, cursorZ); launched {
				snap := h.session.Snapshot()
				h.srv.EmitVibration(server.VibrationEvent{
					Dimension:          snap.Dimension,
					Type:               server.GameEventProjectileShoot,
					X:                  float64(x) + 0.5,
					Y:                  float64(y) + 0.5,
					Z:                  float64(z) + 0.5,
					SourceEntityID:     snap.EntityID,
					ProjectileEntityID: rocketID,
					Player:             h.session,
					Sneaking:           h.session.IsSneaking(),
					BlockOrItemID:      server.FireworkRocketItemID,
				})
				h.syncHotbarFromSession()
				return h.sendBlockChangedAck(seq)
			}
			if _, placed := h.srv.UseDecorationItemOn(h.session, hand, x, y, z, int(face)); placed {
				h.syncHotbarFromSession()
				return h.sendBlockChangedAck(seq)
			}
			if _, placed := h.srv.UseVehicleItemOnBlock(h.session, hand, x, y, z, face, cursorX, cursorY, cursorZ); placed {
				h.syncHotbarFromSession()
				return h.sendBlockChangedAck(seq)
			}
			off := faceOffsets[face]
			px, py, pz := x+off[0], y+off[1], z+off[2]
			dim := h.session.Dimension()
			if dim == "" {
				dim = world.DimensionOverworld
			}
			heldInHand := h.session.InventorySlot(invSlot)
			switch heldInHand.ResolvedID() {
			case server.TadpoleBucketItemID:
				if _, ok := h.srv.ReleaseTadpoleFromBucket(h.session, dim, px, py, pz); ok {
					h.syncHotbarFromSession()
					return h.sendBlockChangedAck(seq)
				}
			case server.AxolotlBucketItemID:
				if _, ok := h.srv.ReleaseAxolotlFromBucket(h.session, dim, px, py, pz); ok {
					h.syncHotbarFromSession()
					return h.sendBlockChangedAck(seq)
				}
			case server.GoatHornItemID:
				if h.srv.UseGoatHornItem(h.session, hand, "") {
					h.syncHotbarFromSession()
					return h.sendBlockChangedAck(seq)
				}
			}
			if h.srv.PlaceBannerBlock(h.session, hand, face, px, py, pz) {
				h.syncHotbarFromSession()
				return h.sendBlockChangedAck(seq)
			}
		}
		if state, ok := h.heldBlock(); ok {
			off := faceOffsets[face]
			px, py, pz := x+off[0], y+off[1], z+off[2]
			dim := world.DimensionOverworld
			if h.session != nil {
				dim = h.session.Dimension()
			}
			cache := h.srv.PlayerChunks(h.session)
			if cache != nil {
				if h.srv != nil {
					state = h.srv.ResolvePlacedBlockState(h.session, state, face)
				}
				placed := false
				if server.IsBedBlock(state) && h.srv != nil {
					placed = h.srv.PlaceBedBlock(h.session, px, py, pz, state)
				} else if server.IsDecoratedPotBlock(state) && h.srv != nil {
					sherds := h.srv.LastCraftedDecoratedPotSherds()
					if h.session != nil {
						if sh, hasSlotSherds := server.GetSlotDecoratedPotSherds(h.srv, h.session, h.session.HeldInventorySlot()); hasSlotSherds {
							sherds = sh
						}
					}
					if dp, _ := h.srv.PlaceDecoratedPotAt(dim, px, py, pz, server.DecoratedPotFacing(state), sherds); dp != nil {
						placed = true
					}
				} else if valid, lightChunks := cache.SetBlockWithLight(px, py, pz, state); valid {
					h.broadcastBlockUpdate(px, py, pz, state, lightChunks)
					placed = true
				}
				if placed {
					blockName := world.BlockName(state)
					if h.srv != nil {
						h.srv.OnSculkBlockPlaced(dim, px, py, pz, state)
						h.srv.EmitPlayerBlockVibration(h.session, server.GameEventBlockPlace, dim, px, py, pz, blockName)
					}
					if blockName == "minecraft:fire" || blockName == "minecraft:soul_fire" {
						_, _ = h.srv.LightPortalAt(dim, px, py, pz)
					}
					if server.IsCampfireBlock(state) && h.srv != nil {
						h.srv.UpdateCampfireSignalFire(dim, px, py, pz)
					}
					if blockName == "minecraft:hay_block" && h.srv != nil {
						h.srv.UpdateCampfireSignalFire(dim, px, py+1, pz)
					}
					if server.IsSignBlock(state) && h.srv != nil && h.session != nil {
						h.srv.OnSignPlaced(h.session, px, py, pz, true)
					}
					if server.IsBannerBlock(state) && h.srv != nil && h.session != nil {
						h.srv.OnBannerPlaced(h.session, h.session.HeldInventorySlot(), px, py, pz)
						h.srv.SendBannerBlockEntityPacket(h.session, px, py, pz)
					}
					if server.IsBeaconBlock(state) && h.srv != nil {
						h.srv.UpdateBeaconAtInDim(dim, px, py, pz, h.session)
					}
					if server.IsConduitBlock(state) && h.srv != nil {
						h.srv.UpdateConduitAtInDim(dim, px, py, pz)
					}
					if server.IsCauldronBlock(state) && h.srv != nil {
						if compRes := h.srv.UpdateComparatorsAt(dim, px, py, pz); len(compRes.Changes) > 0 || len(compRes.LightChanged) > 0 {
							h.broadcastBlockChanges(dim, cache, compRes)
						}
					}
					if server.IsBrewingStandBlock(state) && h.srv != nil {
						var customName string
						if h.session != nil {
							customName = server.GetSlotItemMeta(h.session, h.session.HeldInventorySlot()).CustomName
						}
						h.srv.OnBrewingStandPlacedInDim(dim, px, py, pz, customName)
					}
					if blockName == "minecraft:sniffer_egg" && h.srv != nil {
						h.srv.PlaceSnifferEgg(dim, px, py, pz)
					}
					if h.session != nil {
						h.srv.RecordItemUsed(h.session, blockName, 1)
						h.srv.TriggerPlantedSeed(h.session, blockName)
					}
					if h.session != nil && h.session.Snapshot().GameMode == server.GameModeSurvival {
						if slot, updated, consumed := h.srv.ConsumeHeldItem(h.session, 1); consumed {
							if hotbarIdx := slot - hotbarInvStart; hotbarIdx >= 0 && hotbarIdx < len(h.hotbar) {
								if updated.IsEmpty() {
									h.hotbar[hotbarIdx] = -1
								} else {
									h.hotbar[hotbarIdx] = updated.ResolvedItemID()
								}
							}
						}
					}
					if (server.IsWitherSkullBlockState(state) || server.IsWitherBaseBlockState(state)) && h.srv != nil {
						h.srv.TrySummonWitherAt(dim, px, py, pz, h.session)
					}
					if (blockName == "minecraft:carved_pumpkin" || blockName == "minecraft:jack_o_lantern") && h.srv != nil {
						h.srv.TrySummonIronGolemAt(dim, px, py, pz, h.session)
						h.srv.TrySummonSnowGolemAt(dim, px, py, pz, h.session)
					}
					h.log.Debug("block placed", "x", px, "y", py, "z", pz, "state", state)
				}
			}
		}
	}
	return h.sendBlockChangedAck(seq)
}

func (h *handler) isHoldingFlintAndSteel() bool {
	if h.session != nil {
		if held := h.session.HeldItem(); !held.IsEmpty() {
			return server.IsFlintAndSteelItem(held)
		}
	}
	flintID, ok := world.ItemNameToID("minecraft:flint_and_steel")
	if !ok {
		return false
	}
	if h.heldSlot >= 0 && int(h.heldSlot) < len(h.hotbar) {
		return h.hotbar[h.heldSlot] == flintID
	}
	return false
}

// heldBlock returns the block state for the currently held item, if it is a
// placeable block.
func (h *handler) heldBlock() (uint16, bool) {
	if h.session != nil {
		if held := h.session.HeldItem(); !held.IsEmpty() {
			if itemID := held.ResolvedItemID(); itemID >= 0 {
				return world.ItemToBlock(itemID)
			}
		}
	}
	if h.heldSlot < 0 || int(h.heldSlot) >= len(h.hotbar) {
		return 0, false
	}
	itemID := h.hotbar[h.heldSlot]
	if itemID < 0 {
		return 0, false
	}
	return world.ItemToBlock(itemID)
}

func (h *handler) broadcastBlockUpdate(x, y, z int, state uint16, lightChunks []world.ChunkPos) {
	dim := world.DimensionOverworld
	if h.session != nil {
		dim = h.session.Dimension()
	}
	cache := h.srv.PlayerChunks(h.session)
	w := protocol.NewWriter(12)
	w.Position(x, y, z)
	w.VarInt(int32(state))
	cx := int32(x >> 4)
	cz := int32(z >> 4)
	h.srv.BroadcastDimensionChunk(dim, cx, cz, protocol.PlayBlockUpdate, w.Bytes())
	if cache != nil {
		for _, chunk := range lightChunks {
			if light, err := cache.LightUpdate(chunk.X, chunk.Z); err == nil {
				h.srv.BroadcastDimensionChunk(dim, chunk.X, chunk.Z, protocol.PlayLightUpdate, light)
			}
		}
	}
}

func (h *handler) broadcastBlockChanges(dim string, cache *world.Cache, res world.BlockUpdateResult) {
	if h.srv == nil {
		return
	}
	dim = world.NormalizeDimension(dim)
	for _, ch := range res.Changes {
		w := protocol.NewWriter(12)
		w.Position(ch.Pos.X, ch.Pos.Y, ch.Pos.Z)
		w.VarInt(int32(ch.NewState))
		cx := int32(ch.Pos.X >> 4)
		cz := int32(ch.Pos.Z >> 4)
		h.srv.BroadcastDimensionChunk(dim, cx, cz, protocol.PlayBlockUpdate, w.Bytes())
	}
	if cache != nil {
		for _, chunk := range res.LightChanged {
			if light, err := cache.LightUpdate(chunk.X, chunk.Z); err == nil {
				h.srv.BroadcastDimensionChunk(dim, chunk.X, chunk.Z, protocol.PlayLightUpdate, light)
			}
		}
	}
}

// sendBlockChangedAck confirms a block-action sequence so the client does not
// roll back its predicted change.
func (h *handler) sendBlockChangedAck(sequence int32) error {
	w := protocol.NewWriter(4)
	w.VarInt(sequence)
	return h.conn.SendWriter(protocol.PlayBlockChangedAck, w)
}
