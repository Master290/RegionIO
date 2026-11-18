package server

import (
	"errors"
	"math"
	"math/rand"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Phantom entity, insomnia spawning, swoop AI, cat scare, and daylight burning
// constants for Minecraft 26.1.2 (protocol 775).
const (
	MobTypePhantom = "minecraft:phantom"

	// EntityTypePhantomID is BuiltInRegistries.ENTITY_TYPE ID for "minecraft:phantom"
	// in Minecraft 26.1.2 (protocol 775).
	EntityTypePhantomID int = 99

	// DefaultPhantomMaxHealth is the vanilla max health of a phantom (20 HP = 10 hearts).
	DefaultPhantomMaxHealth float32 = 20.0

	// PhantomInsomniaMinTicks is the minimum minecraft:time_since_rest statistic
	// required before phantoms can spawn (72,000 ticks = 3 in-game days = 1 real hour).
	PhantomInsomniaMinTicks int32 = 72000

	// PhantomSpawnerIntervalTicks is the vanilla interval between phantom spawner
	// checks (1200 ticks = 60 seconds).
	PhantomSpawnerIntervalTicks int64 = 1200

	// PhantomMinPlayerY is the minimum player Y coordinate (sea level 63) required
	// for natural phantom insomnia spawning.
	PhantomMinPlayerY = 63

	// PhantomSpawnMinHeightAbove and PhantomSpawnMaxHeightAbove define the
	// 20..34 block vertical offset above the player where phantoms spawn.
	PhantomSpawnMinHeightAbove = 20
	PhantomSpawnMaxHeightAbove = 34

	// Phantom flight, circling, swoop attack, and daylight burn parameters.
	PhantomCircleAltitudeOffset  = 22.0
	PhantomCircleRadius          = 10.0
	PhantomCircleAngularSpeed    = 0.08
	PhantomFlySpeed              = 0.28
	PhantomSwoopSpeed            = 0.42
	PhantomSwoopIntervalTicks    = 100
	PhantomSwoopMaxIntervalTicks = 200
	PhantomMaxSwoopTicks         = 100
	PhantomAttackReach           = 2.0
	PhantomAttackDamage          = float32(6.0)
	PhantomAggroRange            = 64.0
	PhantomCatScareRange         = 16.0
	PhantomSunFireTicks          = 160
	PhantomSunBurnIntervalTicks  = 20
	PhantomSunBurnDamage         = float32(1.0)
	PhantomMaxSize               = 64

	// SynchedEntityData index for Phantom.ID_SIZE (index 16, INT serializer 1).
	EntityDataPhantomSizeID byte = 16

	// Sound events (vanilla 26.1.2).
	SoundPhantomAmbient = "minecraft:entity.phantom.ambient"
	SoundPhantomBite    = "minecraft:entity.phantom.bite"
	SoundPhantomDeath   = "minecraft:entity.phantom.death"
	SoundPhantomFlap    = "minecraft:entity.phantom.flap"
	SoundPhantomHurt    = "minecraft:entity.phantom.hurt"
	SoundPhantomSwoop   = "minecraft:entity.phantom.swoop"
)

// PhantomPhase identifies whether a Phantom is circling high above its target
// or swooping down to bite.
type PhantomPhase uint8

const (
	PhantomPhaseCircling PhantomPhase = iota
	PhantomPhaseSwooping
)

// PhantomState tracks the flight phase, circling anchor, target player, size,
// cat-scare state, and swoop timer for an active Phantom entity.
type PhantomState struct {
	EntityID          int32
	Dimension         string
	Phase             PhantomPhase
	AnchorX           float64
	AnchorY           float64
	AnchorZ           float64
	OrbitAngle        float64
	CircleDistance    float64
	CircleHeight      float64
	Clockwise         float64
	MoveTargetX       float64
	MoveTargetY       float64
	MoveTargetZ       float64
	MoveSpeed         float64
	VX, VY, VZ        float64
	PhaseTicks        int
	SwoopCooldown     int
	NextScanTick      int
	CatSearchTick     int
	StrategyStarted   bool
	CircleInitialized bool
	TargetPlayer      *PlayerSession
	Size              int32
	ScaredByCat       bool
	TickCounter       int
}

type phantomManager struct {
	mu                  sync.RWMutex
	phantoms            map[int32]*PhantomState
	lastSpawnTick       int64
	ticksSinceLastCheck int64
	spawnerInitialized  bool
}

var serverPhantomManagers sync.Map // map[*Server]*phantomManager

func (s *Server) phantomManager() *phantomManager {
	if v, ok := serverPhantomManagers.Load(s); ok {
		return v.(*phantomManager)
	}
	actual, _ := serverPhantomManagers.LoadOrStore(s, &phantomManager{
		phantoms: make(map[int32]*PhantomState),
	})
	return actual.(*phantomManager)
}

// DoInsomnia reports whether the `doInsomnia` game rule is enabled (default true).
func (s *Server) DoInsomnia() bool {
	return s.GameRuleBool("doInsomnia")
}

// SetDoInsomnia sets the `doInsomnia` game rule.
func (s *Server) SetDoInsomnia(enabled bool) {
	s.SetGameRuleBool("doInsomnia", enabled)
}

// PlayerTimeSinceRest returns session's current `minecraft:time_since_rest`
// custom statistic in ticks.
func (s *Server) PlayerTimeSinceRest(session *PlayerSession) int32 {
	if s == nil || session == nil {
		return 0
	}
	return s.GetPlayerStat(session, StatTypeCustom, StatTimeSinceRest)
}

// SetPlayerTimeSinceRest sets session's `minecraft:time_since_rest` custom
// statistic in ticks (clamped to >= 0).
func (s *Server) SetPlayerTimeSinceRest(session *PlayerSession, ticks int32) {
	if s == nil || session == nil {
		return
	}
	if ticks < 0 {
		ticks = 0
	}
	s.SetPlayerStat(session, StatTypeCustom, StatTimeSinceRest, ticks)
}

// IncrementPlayerInsomnia advances session's `minecraft:time_since_rest`
// statistic by ticks when the player is alive and not sleeping in a bed,
// returning the updated value.
func (s *Server) IncrementPlayerInsomnia(session *PlayerSession, ticks int32) int32 {
	if s == nil || session == nil || session.IsDead() || s.IsPlayerSleeping(session) || ticks <= 0 {
		return s.PlayerTimeSinceRest(session)
	}
	s.AwardCustomStat(session, StatTimeSinceRest, ticks)
	return s.PlayerTimeSinceRest(session)
}

// ResetPlayerInsomniaOnSleep resets session's `minecraft:time_since_rest`
// statistic to 0 when the player enters a bed or completes sleep.
func (s *Server) ResetPlayerInsomniaOnSleep(session *PlayerSession) {
	if s == nil || session == nil {
		return
	}
	s.SetPlayerStat(session, StatTypeCustom, StatTimeSinceRest, 0)
}

// ResetPhantomSpawnerTimer resets the phantom spawner's internal interval timer
// so the next call to TickPhantomSpawner immediately evaluates insomnia spawning.
func (s *Server) ResetPhantomSpawnerTimer() {
	if s == nil {
		return
	}
	mgr := s.phantomManager()
	mgr.mu.Lock()
	mgr.lastSpawnTick = 0
	mgr.ticksSinceLastCheck = 0
	mgr.spawnerInitialized = false
	mgr.mu.Unlock()
}

// PhantomSpawnerTicksSinceLastCheck returns the number of spawner ticks elapsed
// since the last phantom insomnia spawn evaluation.
func (s *Server) PhantomSpawnerTicksSinceLastCheck() int64 {
	if s == nil {
		return 0
	}
	mgr := s.phantomManager()
	mgr.mu.RLock()
	ticks := mgr.ticksSinceLastCheck
	mgr.mu.RUnlock()
	return ticks
}

// EncodePhantomSizeDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating Phantom.ID_SIZE (index 16, int) for protocol 775.
func EncodePhantomSizeDataPacket(entityID, size int32) []byte {
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataPhantomSizeID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(size)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodePhantomSizeDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodePhantomSizeDataPacket.
func DecodePhantomSizeDataPacket(data []byte) (entityID, size int32, err error) {
	r := protocol.NewReader(data)
	entityID, err = r.VarInt()
	if err != nil {
		return 0, 0, err
	}
	idx, err := r.ReadByte()
	if err != nil {
		return 0, 0, err
	}
	if idx != EntityDataPhantomSizeID {
		return 0, 0, errors.New("phantom: unexpected entity data index")
	}
	ser, err := r.VarInt()
	if err != nil {
		return 0, 0, err
	}
	if ser != EntityDataSerializerInt {
		return 0, 0, errors.New("phantom: unexpected entity data serializer")
	}
	size, err = r.VarInt()
	if err != nil {
		return 0, 0, err
	}
	term, err := r.ReadByte()
	if err != nil || term != 0xFF {
		return 0, 0, errors.New("phantom: missing 0xFF terminator")
	}
	return entityID, size, nil
}

// PhantomAttackDamageForSize returns the base melee attack damage for a Phantom
// of the given size (`6.0 + size` before difficulty scaling).
func PhantomAttackDamageForSize(size int32) float32 {
	if size < 0 {
		size = 0
	}
	if size > PhantomMaxSize {
		size = PhantomMaxSize
	}
	return PhantomAttackDamage + float32(size)
}

// SetPhantomSize updates a Phantom's size (0..64) and broadcasts its updated
// SynchedEntityData (index 16) to nearby viewers.
func (s *Server) SetPhantomSize(entityID, size int32) bool {
	if s == nil || s.entities == nil {
		return false
	}
	if size < 0 {
		size = 0
	}
	if size > PhantomMaxSize {
		size = PhantomMaxSize
	}
	mgr := s.phantomManager()
	mgr.mu.Lock()
	st, ok := mgr.phantoms[entityID]
	if !ok || st == nil {
		mgr.mu.Unlock()
		return false
	}
	st.Size = size
	dim := st.Dimension
	mgr.mu.Unlock()

	if ent := s.entities.Get(entityID); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodePhantomSizeDataPacket(entityID, size))
	}
	return true
}

func nextPhantomSwoopCooldown(rng *rand.Rand) int {
	if rng == nil {
		return PhantomSwoopIntervalTicks
	}
	span := PhantomSwoopMaxIntervalTicks - PhantomSwoopIntervalTicks + 1
	if span <= 1 {
		return PhantomSwoopIntervalTicks
	}
	return PhantomSwoopIntervalTicks + rng.Intn(span)
}

// SpawnPhantom spawns a `minecraft:phantom` at (x, y, z) in dimension,
// initializing its circling anchor above targetPlayer (or its spawn position).
func (s *Server) SpawnPhantom(dimension string, x, y, z float64, targetPlayer *PlayerSession) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypePhantom)
	if typeID < 0 {
		typeID = EntityTypePhantomID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypePhantom,
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
	mem.health = DefaultPhantomMaxHealth
	mem.lastHealth = DefaultPhantomMaxHealth
	tr.mu.Unlock()

	anchorX, anchorY, anchorZ := math.Floor(x), math.Floor(y)+5.0, math.Floor(z)
	strategyStarted := false
	nextScanTick := 10
	if targetPlayer != nil {
		snap := targetPlayer.Snapshot()
		anchorX, anchorY, anchorZ = computePhantomAnchorAboveTarget2612(snap.X, snap.Y, snap.Z, OverworldSeaLevel2612, nil)
		strategyStarted = true
		nextScanTick = PhantomTargetScanCooldownTicks2612
	}

	st := &PhantomState{
		EntityID:        id,
		Dimension:       normDim,
		Phase:           PhantomPhaseCircling,
		AnchorX:         anchorX,
		AnchorY:         anchorY,
		AnchorZ:         anchorZ,
		MoveSpeed:       0.1,
		SwoopCooldown:   PhantomInitialSweepDelayTicks2612,
		NextScanTick:    nextScanTick,
		StrategyStarted: strategyStarted,
		TargetPlayer:    targetPlayer,
	}
	initPhantomCircleGoal2612(st, nil)

	mgr := s.phantomManager()
	mgr.mu.Lock()
	mgr.phantoms[id] = st
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodePhantomSizeDataPacket(id, 0))
	}
	return id, nil
}

// GetPhantomState returns a snapshot of the PhantomState for entityID.
func (s *Server) GetPhantomState(entityID int32) (PhantomState, bool) {
	if s == nil {
		return PhantomState{}, false
	}
	mgr := s.phantomManager()
	mgr.mu.RLock()
	st, ok := mgr.phantoms[entityID]
	var out PhantomState
	if ok && st != nil {
		out = *st
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// SetPhantomPhase sets the flight phase (Circling or Swooping) for a Phantom.
func (s *Server) SetPhantomPhase(entityID int32, phase PhantomPhase) bool {
	if s == nil {
		return false
	}
	mgr := s.phantomManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.phantoms[entityID]
	if !ok || st == nil {
		return false
	}
	st.Phase = phase
	st.PhaseTicks = 0
	if phase == PhantomPhaseSwooping {
		st.ScaredByCat = false
		st.StrategyStarted = true
		st.NextScanTick = 0
		st.CatSearchTick = 0
	}
	return true
}

// OnPhantomDamaged handles a Phantom taking damage: in vanilla
// `PhantomSweepAttackGoal.tick()`, taking damage (`hurtTime > 0`)
// immediately aborts an active swoop dive and returns the Phantom to circling.
func (s *Server) OnPhantomDamaged(entityID int32) bool {
	if s == nil {
		return false
	}
	mgr := s.phantomManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.phantoms[entityID]
	if !ok || st == nil {
		return false
	}
	aborted := st.Phase == PhantomPhaseSwooping
	st.Phase = PhantomPhaseCircling
	st.PhaseTicks = 0
	if st.SwoopCooldown < PhantomResweepMinSeconds2612*20 {
		st.SwoopCooldown = PhantomResweepMinSeconds2612 * 20
	}
	return aborted
}

// OnPhantomKilled cleans up state for a killed Phantom entity.
func (s *Server) OnPhantomKilled(entityID int32) {
	if s == nil {
		return
	}
	mgr := s.phantomManager()
	mgr.mu.Lock()
	delete(mgr.phantoms, entityID)
	mgr.mu.Unlock()
}

// RollPhantomDeathLoot rolls vanilla 26.1.2 `minecraft:phantom` death loot:
// 0..1 `minecraft:phantom_membrane` + 0..lootingLevel bonus (when rng == nil,
// returns 1 + max(0, lootingLevel) membranes deterministically).
func RollPhantomDeathLoot(rng *rand.Rand, lootingLevel int) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	count := int32(1)
	if rng != nil {
		count = int32(rng.Intn(2)) // 0..1
		if lootingLevel > 0 {
			count += int32(rng.Intn(lootingLevel + 1))
		}
	} else if lootingLevel > 0 {
		count += int32(lootingLevel)
	}
	if count <= 0 {
		return nil
	}
	if stack := world.NewNamedItemStack("minecraft:phantom_membrane", count); !stack.IsEmpty() {
		return []world.ItemStack{stack}
	}
	return nil
}

// HasOpenSkyAbove reports whether all blocks strictly above (x, y, z) up to the
// top of the world (or the highest non-air block in the column) are passable/transparent.
func (s *Server) HasOpenSkyAbove(cache *world.Cache, x, y, z int) bool {
	if cache == nil {
		return true
	}
	topY, ok := cache.SafeSpawnY(x, z)
	if !ok {
		return true
	}
	return y >= topY
}

// CanSpawnPhantomsForPlayer checks whether session currently satisfies vanilla
// 26.1.2 `PhantomSpawner` conditions:
//   - Difficulty != Peaceful, `doInsomnia`, `doMobSpawning`, and `spawnMonsters` enabled
//   - Overworld dimension, alive, Survival or Adventure mode
//   - Night (`IsNight()`) or thunderstorm (`IsThundering()`)
//   - Player Y >= 63 with open sky access above head
//   - `minecraft:time_since_rest` statistic >= 72,000 ticks (3 in-game days)
func (s *Server) CanSpawnPhantomsForPlayer(session *PlayerSession) (timeSinceRest int32, eligible bool) {
	if s == nil || session == nil || session.IsDead() || s.Difficulty() == DifficultyPeaceful {
		return 0, false
	}
	if !s.DoInsomnia() || !s.DoMobSpawning() || !s.SpawnMonsters() {
		return 0, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return 0, false
	}
	if world.NormalizeDimension(snap.Dimension) != world.DimensionOverworld {
		return 0, false
	}
	if !s.IsNight() && !s.IsThundering() {
		return 0, false
	}
	by := int(math.Floor(snap.Y))
	if by < PhantomMinPlayerY {
		return 0, false
	}
	bx := int(math.Floor(snap.X))
	bz := int(math.Floor(snap.Z))
	if !s.HasOpenSkyAbove(s.chunks, bx, by+1, bz) {
		return 0, false
	}
	timeSinceRest = s.GetPlayerStat(session, StatTypeCustom, StatTimeSinceRest)
	if timeSinceRest < PhantomInsomniaMinTicks {
		return timeSinceRest, false
	}
	return timeSinceRest, true
}

// TrySpawnPhantomsForPlayer evaluates the vanilla insomnia roll for session
// (`rng.Int31n(timeSinceRest) >= 72000`) and spawns a pack of 1..4 Phantoms
// 20..34 blocks above the player when the check succeeds.
func (s *Server) TrySpawnPhantomsForPlayer(session *PlayerSession, rng *rand.Rand) []int32 {
	timeSinceRest, ok := s.CanSpawnPhantomsForPlayer(session)
	if !ok {
		return nil
	}
	if rng != nil && rng.Int31n(timeSinceRest) < PhantomInsomniaMinTicks {
		return nil
	}
	snap := session.Snapshot()
	heightOffset := float64(PhantomSpawnMinHeightAbove)
	if rng != nil {
		heightOffset += float64(rng.Intn(PhantomSpawnMaxHeightAbove - PhantomSpawnMinHeightAbove + 1))
	} else {
		heightOffset = 24.0
	}
	spawnX := snap.X
	spawnY := snap.Y + heightOffset
	spawnZ := snap.Z

	// Pack size scales with difficulty: Easy 1..2, Normal 1..3, Hard 1..4.
	maxPack := int(s.Difficulty()) + 1
	if maxPack < 1 {
		maxPack = 1
	}
	if maxPack > 4 {
		maxPack = 4
	}
	packCount := 1
	if rng != nil && maxPack > 1 {
		packCount = 1 + rng.Intn(maxPack)
	}

	spawned := make([]int32, 0, packCount)
	for i := 0; i < packCount; i++ {
		offX := float64(i) * 1.5
		id, err := s.SpawnPhantom(world.DimensionOverworld, spawnX+offX, spawnY, spawnZ, session)
		if err == nil && id > 0 {
			spawned = append(spawned, id)
		}
	}
	if len(spawned) > 0 {
		cx := int32(int64(math.Floor(snap.X)) >> 4)
		cz := int32(int64(math.Floor(snap.Z)) >> 4)
		s.BroadcastDimensionChunk(world.DimensionOverworld, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundPhantomAmbient, SoundSourceHostile, snap.X, snap.Y, snap.Z, 1.0, 0.9, 0))
	}
	return spawned
}

// TickPhantomSpawner runs the periodic insomnia check across all online players
// every PhantomSpawnerIntervalTicks (1200 ticks = 60s).
func (s *Server) TickPhantomSpawner(rng *rand.Rand) int {
	if s == nil || s.entities == nil || s.Difficulty() == DifficultyPeaceful {
		return 0
	}
	if !s.DoInsomnia() || !s.DoMobSpawning() || !s.SpawnMonsters() {
		return 0
	}
	gameTime, _ := s.WorldTime()
	mgr := s.phantomManager()
	mgr.mu.Lock()
	mgr.ticksSinceLastCheck++
	shouldRun := !mgr.spawnerInitialized ||
		mgr.ticksSinceLastCheck >= PhantomSpawnerIntervalTicks ||
		(mgr.lastSpawnTick > 0 && gameTime >= mgr.lastSpawnTick && gameTime-mgr.lastSpawnTick >= PhantomSpawnerIntervalTicks)
	if !shouldRun {
		mgr.mu.Unlock()
		return 0
	}
	mgr.spawnerInitialized = true
	mgr.ticksSinceLastCheck = 0
	mgr.lastSpawnTick = gameTime
	mgr.mu.Unlock()

	totalSpawned := 0
	for _, sess := range s.onlinePlayersSnapshot() {
		ids := s.TrySpawnPhantomsForPlayer(sess, rng)
		totalSpawned += len(ids)
	}
	return totalSpawned
}

// IsPhantomInDaylight reports whether a Phantom at (x, y, z) in dimension is
// currently exposed to burning sunlight (Overworld daytime, clear weather, not
// submerged in water, open sky above).
func (s *Server) IsPhantomInDaylight(dimension string, x, y, z float64) bool {
	if s == nil || world.NormalizeDimension(dimension) != world.DimensionOverworld {
		return false
	}
	if s.IsNight() || s.IsRaining() || s.IsThundering() {
		return false
	}
	bx := int(math.Floor(x))
	by := int(math.Floor(y))
	bz := int(math.Floor(z))
	cache := s.DimensionChunks(dimension)
	if cache != nil {
		if cache.FluidHeightAt(bx, by, bz) > 0 || cache.GetBlock(bx, by, bz) == world.StateWater {
			return false
		}
	}
	return s.HasOpenSkyAbove(cache, bx, by, bz)
}

// TickPhantoms steps all active Phantom entities:
//   - Despawns phantoms in Peaceful difficulty.
//   - Ignites and damages phantoms exposed to daytime sunlight.
//   - Flies in a circle 20..34 blocks above the target player, periodically
//     swooping down to bite (`6.0 + size`), and immediately aborting its swoop
//     and retreating if hurt or scared by a nearby cat/ocelot (within 16 blocks).
func (s *Server) TickPhantoms(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	mgr := s.phantomManager()
	mgr.mu.RLock()
	if len(mgr.phantoms) == 0 {
		mgr.mu.RUnlock()
		return
	}
	ids := make([]int32, 0, len(mgr.phantoms))
	for id := range mgr.phantoms {
		ids = append(ids, id)
	}
	mgr.mu.RUnlock()

	if s.Difficulty() == DifficultyPeaceful {
		for _, id := range ids {
			if ent := s.entities.Get(id); ent != nil {
				cp := ent.ChunkPos()
				s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayRemoveEntities,
					encodeRemoveEntitiesPacket(nil, []int32{id}))
			}
			s.entities.Remove(id)
			mgr.mu.Lock()
			delete(mgr.phantoms, id)
			mgr.mu.Unlock()
		}
		return
	}

	for _, id := range ids {
		s.tickSinglePhantom(id, rng)
	}
}

func (s *Server) tickSinglePhantom(entityID int32, rng *rand.Rand) {
	ent := s.entities.Get(entityID)
	mgr := s.phantomManager()
	mgr.mu.Lock()
	st, ok := mgr.phantoms[entityID]
	if !ok || st == nil || ent == nil {
		delete(mgr.phantoms, entityID)
		mgr.mu.Unlock()
		return
	}
	st.TickCounter++
	st.PhaseTicks++
	snap := *st
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)

	// 1. Daylight burning (`Mob.burnUndead`): Phantoms are in `#minecraft:burn_in_daylight` and `#minecraft:undead`.
	if s.IsPhantomInDaylight(dim, ent.X, ent.Y, ent.Z) {
		s.SetEntityFireTicks(entityID, PhantomSunFireTicks)
		if snap.TickCounter%PhantomSunBurnIntervalTicks == 0 {
			killed := s.applyDamageToMob(ent, PhantomSunBurnDamage, nil, 0, false, false)
			if killed {
				mgr.mu.Lock()
				delete(mgr.phantoms, entityID)
				mgr.mu.Unlock()
				return
			}
		}
	}

	// Wing flap sound (`TICKS_PER_FLAP = ceil(24.166098F) = 25`):
	if snap.TickCounter%25 == 0 {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundPhantomFlap, SoundSourceHostile, ent.X, ent.Y, ent.Z, 10.0, 0.95, 0))
	}

	// 2. `targetSelector`: `PhantomAttackPlayerTargetGoal` (runs BEFORE `goalSelector`!):
	target := snap.TargetPlayer
	if target != nil {
		tSnap := target.Snapshot()
		if target.IsDead() || tSnap.Health <= 0 ||
			world.NormalizeDimension(tSnap.Dimension) != dim ||
			tSnap.GameMode == GameModeSpectator || tSnap.GameMode == GameModeCreative {
			target = nil
		}
	}
	nextScan := snap.NextScanTick
	if target == nil {
		if nextScan > 0 {
			nextScan--
		} else {
			nextScan = PhantomTargetScanCooldownTicks2612
			target = s.findPhantomNearestAttackablePlayer2612(dim, ent.X, ent.Y, ent.Z, snap.Size)
		}
	}

	// 3. `goalSelector`: `PhantomAttackStrategyGoal` + (`PhantomSweepAttackGoal` | `PhantomCircleAroundAnchorGoal`):
	local := snap
	local.TargetPlayer = target
	local.NextScanTick = nextScan

	var playSwoopSound bool
	var playCatHiss bool
	var catHissX, catHissY, catHissZ float64
	var bitTargetNow bool

	if target != nil {
		tSnap := target.Snapshot()
		if !local.StrategyStarted {
			// `PhantomAttackStrategyGoal.start()`:
			local.StrategyStarted = true
			local.Phase = PhantomPhaseCircling
			local.PhaseTicks = 0
			local.SwoopCooldown = PhantomInitialSweepDelayTicks2612
			local.AnchorX, local.AnchorY, local.AnchorZ = computePhantomAnchorAboveTarget2612(tSnap.X, tSnap.Y, tSnap.Z, OverworldSeaLevel2612, rng)
			initPhantomCircleGoal2612(&local, rng)
		} else if local.Phase == PhantomPhaseCircling {
			// Ensure legacy/unclamped anchor respects `seaLevel + 1` (`64`):
			if local.AnchorY < float64(OverworldSeaLevel2612+1) {
				local.AnchorX, local.AnchorY, local.AnchorZ = computePhantomAnchorAboveTarget2612(tSnap.X, tSnap.Y, tSnap.Z, OverworldSeaLevel2612, rng)
			}
			local.SwoopCooldown--
			if local.SwoopCooldown <= 0 {
				local.Phase = PhantomPhaseSwooping
				local.PhaseTicks = 0
				local.ScaredByCat = false
				local.CatSearchTick = 0
				local.AnchorX, local.AnchorY, local.AnchorZ = computePhantomAnchorAboveTarget2612(tSnap.X, tSnap.Y, tSnap.Z, OverworldSeaLevel2612, rng)
				local.SwoopCooldown = nextPhantomResweepDelay2612(rng)
				playSwoopSound = true
			}
		}
	} else if local.StrategyStarted {
		// `PhantomAttackStrategyGoal.stop()`:
		local.StrategyStarted = false
		local.Phase = PhantomPhaseCircling
		local.PhaseTicks = 0
		if cache != nil {
			ax, az := int(math.Floor(local.AnchorX)), int(math.Floor(local.AnchorZ))
			if topY, ok := cache.SafeSpawnY(ax, az); ok {
				addY := 10
				if rng != nil {
					addY += rng.Intn(20)
				}
				local.AnchorY = float64(topY + addY)
			}
		}
	}

	if local.Phase == PhantomPhaseSwooping && target != nil {
		// `PhantomSweepAttackGoal.canContinueToUse()` (`Phantom.java:2456-2472`):
		// checks ONLY `Cat.class` (`minecraft:cat`, NOT `ocelot`) in `boundingBox.inflate(16.0D)` and calls `cat.hiss()`:
		if local.TickCounter > local.CatSearchTick {
			local.CatSearchTick = local.TickCounter + 20
			_, _, _, _, foundCat := s.HissAllCatsNearPhantom2612(dim, ent.X, ent.Y, ent.Z, local.Size)
			local.ScaredByCat = foundCat
		}
		if local.ScaredByCat {
			// `PhantomSweepAttackGoal.stop()` (`Phantom.java:2439-2449`):
			// `this.setTarget(null); this.attackPhase = AttackPhase.CIRCLE;`!
			local.TargetPlayer = nil
			target = nil
			local.Phase = PhantomPhaseCircling
			local.PhaseTicks = 0
			local.StrategyStarted = false
			local.NextScanTick = PhantomTargetScanCooldownTicks2612
			stepPhantomCircleGoal2612(cache, ent, &local, rng)
		} else {
			tSnap := target.Snapshot()
			local.MoveTargetX = tSnap.X
			local.MoveTargetY = tSnap.Y + 0.9 // `target.getY(0.5D)`
			local.MoveTargetZ = tSnap.Z
			if phantomSwoopIntersectsPlayer2612(ent.X, ent.Y, ent.Z, local.Size, tSnap) {
				bitTargetNow = true
				local.Phase = PhantomPhaseCircling
				local.PhaseTicks = 0
				local.CircleInitialized = false
			}
		}
	} else {
		stepPhantomCircleGoal2612(cache, ent, &local, rng)
	}

	if playSwoopSound && !playCatHiss {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundPhantomSwoop, SoundSourceHostile, ent.X, ent.Y, ent.Z, 10.0, 1.0, 0))
	}
	if playCatHiss {
		cx := int32(int64(math.Floor(catHissX)) >> 4)
		cz := int32(int64(math.Floor(catHissZ)) >> 4)
		s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundCatHiss, SoundSourceNeutral, catHissX, catHissY, catHissZ, 1.0, 1.0, 0))
	}

	// 4. `PhantomMoveControl` + `travelFlying`:
	nextX, nextY, nextZ, yaw, pitch, horizCollided := stepPhantomMoveControl2612(cache, ent, &local)
	s.entities.Update(entityID, func(e *world.Entity) {
		e.X = nextX
		e.Y = nextY
		e.Z = nextZ
		e.Yaw = yaw
		e.Pitch = pitch
		e.HeadYaw = yaw
		e.VelocityX = clampVelocityShort(local.VX)
		e.VelocityY = clampVelocityShort(local.VY)
		e.VelocityZ = clampVelocityShort(local.VZ)
		e.OnGround = false
	})

	if !bitTargetNow && local.Phase == PhantomPhaseSwooping && target != nil && !target.IsDead() {
		tSnap := target.Snapshot()
		if phantomSwoopIntersectsPlayer2612(nextX, nextY, nextZ, local.Size, tSnap) {
			bitTargetNow = true
			local.Phase = PhantomPhaseCircling
			local.PhaseTicks = 0
			local.CircleInitialized = false
		} else if horizCollided {
			local.Phase = PhantomPhaseCircling
			local.PhaseTicks = 0
		}
	}

	mgr.mu.Lock()
	if cur, ok := mgr.phantoms[entityID]; ok && cur != nil {
		*cur = local
	}
	mgr.mu.Unlock()

	if bitTargetNow && target != nil && !target.IsDead() {
		dmg := PhantomAttackDamageForSize(local.Size)
		s.AttackPlayerFromMob(entityID, MobTypePhantom, nextX, nextZ, target, dmg)
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundPhantomBite, SoundSourceHostile, nextX, nextY, nextZ, 1.0, 1.0, 0))
	}
}

// FindCatOrOcelotNearPosition locates the nearest `minecraft:cat` or
// `minecraft:ocelot` within radius blocks of (x, y, z) in dim.
func (s *Server) FindCatOrOcelotNearPosition(dim string, x, y, z, radius float64) (catEntityID int32, catX, catY, catZ float64, found bool) {
	if s == nil || s.entities == nil || radius <= 0 {
		return 0, 0, 0, 0, false
	}
	normDim := world.NormalizeDimension(dim)
	bestDistSq := radius * radius
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		e := &allEnts[i]
		normType := canonicalResourceName(e.TypeName)
		if (normType != MobTypeCat && normType != MobTypeOcelot) || e.EffectiveDimension() != normDim {
			continue
		}
		dx, dy, dz := e.X-x, e.Y-y, e.Z-z
		distSq := dx*dx + dy*dy + dz*dz
		if distSq <= bestDistSq {
			bestDistSq = distSq
			catEntityID = e.ID
			catX, catY, catZ = e.X, e.Y, e.Z
			found = true
		}
	}
	return catEntityID, catX, catY, catZ, found
}

func (s *Server) hasCatNearPosition(dim string, x, y, z, radius float64) bool {
	_, _, _, _, ok := s.FindCatOrOcelotNearPosition(dim, x, y, z, radius)
	return ok
}
