package server

import (
	"errors"
	"math"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

type serverDimensionState struct {
	mu       sync.Mutex
	registry *world.DimensionRegistry
	caches   map[string]*world.Cache
}

type playerDimensionState struct {
	mu          sync.RWMutex
	dimension   string
	switchDimFn func(cache *world.Cache, cx, cz int32)
}

var (
	serverDimensions sync.Map // map[*Server]*serverDimensionState
	playerDimensions sync.Map // map[*PlayerSession]*playerDimensionState
)

func (s *Server) dimState() *serverDimensionState {
	if s == nil {
		return nil
	}
	if v, ok := serverDimensions.Load(s); ok {
		return v.(*serverDimensionState)
	}
	initCaches := make(map[string]*world.Cache, 3)
	if s.chunks != nil {
		s.chunks.SetDimensionType(world.OverworldDimensionType)
		initCaches[world.DimensionOverworld] = s.chunks
	}
	created := &serverDimensionState{
		registry: world.DefaultDimensionRegistry(),
		caches:   initCaches,
	}
	actual, _ := serverDimensions.LoadOrStore(s, created)
	return actual.(*serverDimensionState)
}

func getPlayerDimState(session *PlayerSession) *playerDimensionState {
	if session == nil {
		return nil
	}
	if v, ok := playerDimensions.Load(session); ok {
		return v.(*playerDimensionState)
	}
	actual, _ := playerDimensions.LoadOrStore(session, &playerDimensionState{
		dimension: world.DimensionOverworld,
	})
	return actual.(*playerDimensionState)
}

// DimensionRegistry returns the server's dimension registry containing
// minecraft:overworld, minecraft:the_nether, and minecraft:the_end.
func (s *Server) DimensionRegistry() *world.DimensionRegistry {
	if s == nil {
		return world.DefaultDimensionRegistry()
	}
	return s.dimState().registry
}

// Dimensions returns the playable dimension resource keys on this server.
func (s *Server) Dimensions() []string {
	return s.DimensionRegistry().Dimensions()
}

// DimensionType looks up the vanilla 26.1.2 DimensionType for dim.
func (s *Server) DimensionType(dim string) (world.DimensionType, bool) {
	return s.DimensionRegistry().Get(dim)
}

// DimensionChunks returns the world.Cache for the given dimension
// ("minecraft:overworld", "minecraft:the_nether", or "minecraft:the_end"),
// initializing the Nether or End cache lazily on first access.
func (s *Server) DimensionChunks(dim string) *world.Cache {
	if s == nil {
		return nil
	}
	norm := world.NormalizeDimension(dim)
	if norm == world.DimensionOverworld && s.chunks != nil {
		s.chunks.SetDimensionType(world.OverworldDimensionType)
		return s.chunks
	}
	ds := s.dimState()
	ds.mu.Lock()
	if c, ok := ds.caches[norm]; ok && c != nil {
		ds.mu.Unlock()
		return c
	}

	var gen world.Generator
	var batchGen world.BatchGenerator
	var dt world.DimensionType
	switch norm {
	case world.DimensionNether:
		gen, batchGen = world.NewNetherGenerators(s.cfg.WorldSeed)
		dt = world.NetherDimensionType
	case world.DimensionEnd:
		gen, batchGen = world.NewEndGenerators(s.cfg.WorldSeed)
		dt = world.EndDimensionType
	default:
		if s.chunks != nil {
			s.chunks.SetDimensionType(world.OverworldDimensionType)
			ds.caches[world.DimensionOverworld] = s.chunks
			ds.mu.Unlock()
			return s.chunks
		}
		gen, batchGen = world.NewVanillaRegionGenerators(s.cfg.WorldSeed)
		dt = world.OverworldDimensionType
	}

	var dimStore *world.Store
	if s.store != nil {
		if st, err := s.store.DimensionStore(norm); err == nil {
			dimStore = st
		}
	}
	cache := world.NewCacheWithLimit(int32(s.cfg.CompressionThreshold), gen, dimStore, s.cfg.MaxCachedChunks)
	if batchGen != nil {
		cache.SetBatchGenerator(batchGen)
	}
	if dimStore != nil {
		cache.SetPersistGenerated(true)
	}
	cache.SetDimensionType(dt)
	cache.SetWaterSourceConversion(s.WaterSourceConversion())
	cache.SetLavaSourceConversion(s.LavaSourceConversion())
	cache.SetPressurePlateEntityCounter(func(x, y, z int) int {
		return s.CountEntitiesAtBlock(norm, x, y, z)
	})
	ds.caches[norm] = cache
	ds.mu.Unlock()
	s.BindPistonAndRedstoneHooks(norm)
	s.BindBellRedstone(norm)
	return cache
}

// PlayerChunks returns the world.Cache for the dimension the player is currently in.
func (s *Server) PlayerChunks(session *PlayerSession) *world.Cache {
	if s == nil {
		return nil
	}
	if session == nil {
		return s.chunks
	}
	return s.DimensionChunks(session.Dimension())
}

// SaveAllDimensions flushes dirty chunks across all active dimension caches.
func (s *Server) SaveAllDimensions() error {
	if s == nil {
		return nil
	}
	ds := s.dimState()
	ds.mu.Lock()
	caches := make(map[string]*world.Cache, len(ds.caches))
	for k, c := range ds.caches {
		caches[k] = c
	}
	ds.mu.Unlock()

	var firstErr error
	for _, c := range caches {
		if c == nil {
			continue
		}
		if err := c.SaveAll(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Dimension returns the canonical dimension resource key for this player session
// (defaulting to "minecraft:overworld").
func (session *PlayerSession) Dimension() string {
	if session == nil {
		return world.DimensionOverworld
	}
	st := getPlayerDimState(session)
	st.mu.RLock()
	dim := st.dimension
	st.mu.RUnlock()
	return world.NormalizeDimension(dim)
}

// SetDimension updates the player session's current dimension.
func (session *PlayerSession) SetDimension(dim string) {
	if session == nil {
		return
	}
	norm := world.NormalizeDimension(dim)
	st := getPlayerDimState(session)
	st.mu.Lock()
	st.dimension = norm
	st.mu.Unlock()
}

// SetPlayerDimension updates a player session's current dimension on the server.
func (s *Server) SetPlayerDimension(session *PlayerSession, dim string) {
	if session == nil {
		return
	}
	session.SetDimension(dim)
}

// SetPlayerDimensionSwitchFunc binds a streamer callback invoked when a player
// transitions to a new dimension so chunk streaming switches to the destination
// dimension's cache and resets client chunk residency.
func (s *Server) SetPlayerDimensionSwitchFunc(session *PlayerSession, fn func(cache *world.Cache, cx, cz int32)) {
	if session == nil {
		return
	}
	st := getPlayerDimState(session)
	st.mu.Lock()
	st.switchDimFn = fn
	st.mu.Unlock()
}

func getPlayerDimSwitchFunc(session *PlayerSession) func(cache *world.Cache, cx, cz int32) {
	if session == nil {
		return nil
	}
	st := getPlayerDimState(session)
	st.mu.RLock()
	fn := st.switchDimFn
	st.mu.RUnlock()
	return fn
}

// DimensionTypeRegistryIndex resolves the synced minecraft:dimension_type
// registry index for dim (0 = overworld, 1 = overworld_caves, 2 = the_end, 3 = the_nether).
func DimensionTypeRegistryIndex(dim string) int32 {
	norm := world.NormalizeDimension(dim)
	if idx := registry.Index("minecraft:dimension_type", norm); idx >= 0 {
		return int32(idx)
	}
	switch norm {
	case world.DimensionOverworldCaves:
		return 1
	case world.DimensionEnd:
		return 2
	case world.DimensionNether:
		return 3
	default:
		return 0
	}
}

// EncodeDimensionRegistryData encodes the ConfigRegistryData (0x07) packet body
// for the "minecraft:dimension_type" registry. When includeNBT is true, each
// entry includes its full vanilla 26.1.2 DimensionType NBT compound.
func EncodeDimensionRegistryData(includeNBT bool) []byte {
	entries := []string{
		world.DimensionOverworld,
		world.DimensionOverworldCaves,
		world.DimensionEnd,
		world.DimensionNether,
	}
	for _, reg := range registry.Synced() {
		if reg.Name == "minecraft:dimension_type" && len(reg.Entries) > 0 {
			entries = reg.Entries
			break
		}
	}
	w := protocol.NewWriter(256)
	w.String("minecraft:dimension_type")
	w.VarInt(int32(len(entries)))
	for _, entry := range entries {
		w.String(entry)
		if includeNBT {
			dt, _ := world.LookupDimensionType(entry)
			w.Bool(true)
			w.Raw(nbt.Marshal(dt.ToNBT()))
		} else {
			w.Bool(false)
		}
	}
	return w.Bytes()
}

// EncodePlayLoginPacket encodes the clientbound play login (0x31) packet body
// for protocol 775 (26.1.2), including the server's dimension list and the
// player's current dimension type, dimension key, and sea level.
func EncodePlayLoginPacket(entityID int32, gameMode uint8, dim string, maxPlayers, viewDistance int32, hashedSeed int64) []byte {
	return EncodePlayLoginPacketWithFlags(entityID, gameMode, dim, maxPlayers, viewDistance, hashedSeed, false, false, true, false)
}

// EncodePlayLoginPacketWithFlags encodes the clientbound play login (0x31)
// packet body for protocol 775 (26.1.2) with explicit hardcore and gamerule
// flags (reducedDebugInfo, enableRespawnScreen, doLimitedCrafting).
func EncodePlayLoginPacketWithFlags(
	entityID int32,
	gameMode uint8,
	dim string,
	maxPlayers, viewDistance int32,
	hashedSeed int64,
	isHardcore, reducedDebugInfo, enableRespawnScreen, doLimitedCrafting bool,
) []byte {
	normDim := world.NormalizeDimension(dim)
	dimTypeIdx := DimensionTypeRegistryIndex(normDim)
	dt, _ := world.LookupDimensionType(normDim)

	w := protocol.NewWriter(128)
	w.Int32(entityID)
	w.Bool(isHardcore)

	dims := []string{
		world.DimensionOverworld,
		world.DimensionNether,
		world.DimensionEnd,
	}
	w.VarInt(int32(len(dims)))
	for _, d := range dims {
		w.String(d)
	}

	w.VarInt(maxPlayers)
	w.VarInt(viewDistance)
	w.VarInt(viewDistance)
	w.Bool(reducedDebugInfo)
	w.Bool(enableRespawnScreen)
	w.Bool(doLimitedCrafting)

	// CommonPlayerSpawnInfo
	w.VarInt(dimTypeIdx)
	w.String(normDim)
	w.Int64(hashedSeed)
	w.Byte(gameMode)
	w.Byte(0xFF)  // previous game mode (-1 = none)
	w.Bool(false) // is debug
	w.Bool(false) // is flat
	w.Bool(false) // has last death location
	w.VarInt(0)   // portal cooldown
	w.VarInt(int32(dt.SeaLevel))
	w.Bool(false) // enforces secure chat

	return w.Bytes()
}

// EncodeDimensionRespawnPacket encodes a clientbound respawn (0x52) packet body
// for protocol 775 (CommonPlayerSpawnInfo + dataToKeep) for the target dimension.
func EncodeDimensionRespawnPacket(dim string, gameMode uint8, hashedSeed int64, dataToKeep byte) []byte {
	normDim := world.NormalizeDimension(dim)
	dimTypeIdx := DimensionTypeRegistryIndex(normDim)
	dt, _ := world.LookupDimensionType(normDim)
	prevGameMode := byte(0xFF)
	if dataToKeep != 0 {
		prevGameMode = gameMode
	}

	w := protocol.NewWriter(64)
	w.VarInt(dimTypeIdx)
	w.String(normDim)
	w.Int64(hashedSeed)
	w.Byte(gameMode)
	w.Byte(prevGameMode)
	w.Bool(false) // is debug
	w.Bool(false) // is flat
	w.Bool(false) // has last death location
	w.VarInt(0)   // portal cooldown
	w.VarInt(int32(dt.SeaLevel))
	w.Byte(dataToKeep)
	return w.Bytes()
}

// BroadcastDimensionChunk sends a packet only to players in dim whose chunk
// view contains (cx, cz).
func (s *Server) BroadcastDimensionChunk(dim string, cx, cz int32, id int32, body []byte) {
	s.BroadcastDimensionChunkExcept(dim, cx, cz, [16]byte{}, id, body)
}

// BroadcastDimensionChunkExcept sends a packet to players in dim whose chunk
// view contains (cx, cz), excluding the player with exceptUUID.
func (s *Server) BroadcastDimensionChunkExcept(dim string, cx, cz int32, exceptUUID [16]byte, id int32, body []byte) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	s.playersMu.RLock()
	players := make([]*PlayerSession, 0, len(s.players))
	for _, player := range s.players {
		if player != nil && player.IsJoined() {
			players = append(players, player)
		}
	}
	s.playersMu.RUnlock()

	for _, player := range players {
		if exceptUUID != ([16]byte{}) && player.Profile.UUID == exceptUUID {
			continue
		}
		if player.Dimension() != normDim {
			continue
		}
		snapshot := player.Snapshot()
		pcx := int32(int64(math.Floor(snapshot.X)) >> 4)
		pcz := int32(int64(math.Floor(snapshot.Z)) >> 4)
		if chunkDistance(pcx, pcz, cx, cz) <= int32(snapshot.ViewDistance) && player.send != nil {
			_ = player.send(id, body)
		}
	}
}

// ChangePlayerDimension transitions session to targetDim at (x, y, z, yaw, pitch),
// sending the vanilla 26.1.2 respawn/dimension-change packet sequence and
// switching the player's chunk streamer to the destination dimension's cache.
func (s *Server) ChangePlayerDimension(session *PlayerSession, targetDim string, x, y, z float64, yaw, pitch float32) error {
	if s == nil || session == nil {
		return errors.New("server: nil player session")
	}
	normTarget := world.NormalizeDimension(targetDim)
	if !world.IsValidDimension(normTarget) {
		return errors.New("server: invalid target dimension: " + targetDim)
	}
	oldDim := session.Dimension()
	if oldDim == normTarget {
		return s.TeleportPlayer(session, x, y, z, yaw, pitch)
	}

	// Close any open container window before changing dimensions.
	if open := session.OpenContainer(); open != nil {
		_ = s.CloseContainer(session, open.ContainerID, true)
	}

	// Reset fall distance so changing dimensions never causes fall damage on arrival.
	st := getSurvivalState(session)
	st.mu.Lock()
	st.fallDistance = 0
	st.mu.Unlock()

	destCache := s.DimensionChunks(normTarget)
	if normTarget == world.DimensionEnd && destCache != nil {
		world.EnsureEndSpawnPlatform(destCache)
	}

	session.SetDimension(normTarget)

	session.mu.Lock()
	oldX, oldZ := session.position[0], session.position[2]
	session.position = [3]float64{x, y, z}
	session.yaw = yaw
	session.pitch = pitch
	session.onGround = false
	mode := session.gameMode
	health := session.health
	food := session.foodLevel
	saturation := session.foodSaturation
	selectedSlot := session.selectedSlot
	inv := session.inventory
	hasSpawn := session.hasSpawn
	spawnX, spawnY, spawnZ := session.spawnX, session.spawnY, session.spawnZ
	spawnYaw, spawnPitch := session.spawnYaw, session.spawnPitch
	spawnDim := session.spawnDimension
	if spawnDim == "" {
		spawnDim = world.DimensionOverworld
	}
	session.containerStateID++
	if session.containerStateID <= 0 {
		session.containerStateID = 1
	}
	stateID := session.containerStateID
	recenter := session.recenterFn
	send := session.send
	session.mu.Unlock()

	// Remove player entity from viewers in the old dimension.
	oldCX := int32(int64(math.Floor(oldX)) >> 4)
	oldCZ := int32(int64(math.Floor(oldZ)) >> 4)
	s.BroadcastDimensionChunk(oldDim, oldCX, oldCZ, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{session.EntityID}))

	cx := int32(int64(math.Floor(x)) >> 4)
	cz := int32(int64(math.Floor(z)) >> 4)

	dimSt := getPlayerDimState(session)
	dimSt.mu.RLock()
	switchDim := dimSt.switchDimFn
	dimSt.mu.RUnlock()

	if send != nil {
		wasJoined := session.IsJoined()
		if wasJoined {
			session.SetJoined(false)
			defer session.SetJoined(true)
		}
		// 1. ClientboundRespawnPacket (with KEEP_ALL_DATA = 0x03 for dimension change).
		respawnPkt := EncodeDimensionRespawnPacket(normTarget, mode, s.cfg.WorldSeed, protocol.RespawnKeepAllData)
		if err := send(protocol.PlayRespawn, respawnPkt); err != nil {
			return err
		}

		// 2. GameEvent START_WAITING_FOR_LEVEL_CHUNKS (13).
		ev := protocol.NewWriter(5)
		ev.Byte(protocol.GameEventStartWaitingChunks)
		ev.Float32(0)
		if err := send(protocol.PlayGameEvent, ev.Bytes()); err != nil {
			return err
		}

		// 3. ChunkCacheCenter for the new dimension's arrival chunk.
		centerW := protocol.NewWriter(8)
		centerW.VarInt(cx)
		centerW.VarInt(cz)
		_ = send(protocol.PlayChunkCacheCenter, centerW.Bytes())

		// 4. DefaultSpawnPosition.
		defSpawnW := protocol.NewWriter(40)
		if hasSpawn {
			defSpawnW.String(world.NormalizeDimension(spawnDim))
			defSpawnW.Position(int(spawnX), int(spawnY), int(spawnZ))
			defSpawnW.Float32(spawnYaw)
			defSpawnW.Float32(spawnPitch)
		} else {
			defSpawnW.String(world.DimensionOverworld)
			defSpawnW.Position(8, world.SeaLevel+1, 8)
			defSpawnW.Float32(0)
			defSpawnW.Float32(0)
		}
		_ = send(protocol.PlayDefaultSpawnPos, defSpawnW.Bytes())

		// 5. PlayerPosition in the new dimension.
		posW := protocol.NewWriter(64)
		posW.VarInt(1)
		posW.Float64(x).Float64(y).Float64(z)
		posW.Float64(0).Float64(0).Float64(0)
		posW.Float32(yaw)
		posW.Float32(pitch)
		posW.Int32(0)
		if err := send(protocol.PlayPlayerPosition, posW.Bytes()); err != nil {
			return err
		}

		// 6. Re-sync abilities, health, held slot, inventory, and time.
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

		_ = send(protocol.PlaySetHealth, EncodeSetHealth(health, food, saturation))
		_ = s.SendPlayerExperience(session)
		_ = s.SendPlayerActiveEffects(session)

		heldW := protocol.NewWriter(4)
		heldW.VarInt(selectedSlot)
		_ = send(protocol.PlaySetHeldSlot, heldW.Bytes())

		_ = send(protocol.PlayContainerSetContent, encodeContainerSetContentPacket(stateID, inv))
		_ = send(protocol.PlaySetTime, s.SetTimePacket())
		s.SendWorldBorderToPlayer(session)
		s.SendDifficultyToPlayer(session)
	}

	if switchDim != nil {
		switchDim(destCache, cx, cz)
	} else if recenter != nil {
		recenter(cx, cz)
	}

	s.TriggerChangeDimension(session, normTarget)
	_ = s.SavePlayerData(session)
	return nil
}
