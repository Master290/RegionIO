package server

import (
	"errors"
	"math"
	"math/rand"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Phantom entity, insomnia spawning, swoop AI, and daylight burning constants
// for Minecraft 26.1.2 (protocol 775).
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
	PhantomCircleAltitudeOffset = 22.0
	PhantomCircleRadius         = 10.0
	PhantomCircleAngularSpeed   = 0.08
	PhantomFlySpeed             = 0.28
	PhantomSwoopSpeed           = 0.42
	PhantomSwoopIntervalTicks   = 100
	PhantomMaxSwoopTicks        = 100
	PhantomAttackReach          = 2.0
	PhantomAttackDamage         = float32(6.0)
	PhantomAggroRange           = 64.0
	PhantomCatScareRange        = 16.0
	PhantomSunFireTicks         = 160
	PhantomSunBurnIntervalTicks = 20
	PhantomSunBurnDamage        = float32(1.0)

	// SynchedEntityData index for Phantom.ID_SIZE (index 16, INT serializer 1).
	EntityDataPhantomSizeID byte = 16
)

// PhantomPhase identifies whether a Phantom is circling high above its target
// or swooping down to bite.
type PhantomPhase uint8

const (
	PhantomPhaseCircling PhantomPhase = iota
	PhantomPhaseSwooping
)

// PhantomState tracks the flight phase, circling anchor, target player, and
// swoop timer for an active Phantom entity.
type PhantomState struct {
	EntityID       int32
	Dimension      string
	Phase          PhantomPhase
	AnchorX        float64
	AnchorY        float64
	AnchorZ        float64
	OrbitAngle     float64
	PhaseTicks     int
	SwoopCooldown  int
	TargetPlayer   *PlayerSession
	Size           int32
	TickCounter    int
}

type phantomManager struct {
	mu            sync.RWMutex
	phantoms      map[int32]*PhantomState
	lastSpawnTick int64
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

	anchorX, anchorY, anchorZ := x, y, z
	if targetPlayer != nil {
		snap := targetPlayer.Snapshot()
		anchorX = snap.X
		anchorY = snap.Y + PhantomCircleAltitudeOffset
		anchorZ = snap.Z
	}

	mgr := s.phantomManager()
	mgr.mu.Lock()
	mgr.phantoms[id] = &PhantomState{
		EntityID:      id,
		Dimension:     normDim,
		Phase:         PhantomPhaseCircling,
		AnchorX:       anchorX,
		AnchorY:       anchorY,
		AnchorZ:       anchorZ,
		SwoopCooldown: PhantomSwoopIntervalTicks,
		TargetPlayer:  targetPlayer,
	}
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
	return true
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
//   - Difficulty != Peaceful
//   - Overworld dimension, alive, Survival or Adventure mode
//   - Night (`IsNight()`) or thunderstorm (`IsThundering()`)
//   - Player Y >= 63 with open sky access above head
//   - `minecraft:time_since_rest` statistic >= 72,000 ticks (3 in-game days)
func (s *Server) CanSpawnPhantomsForPlayer(session *PlayerSession) (timeSinceRest int32, eligible bool) {
	if s == nil || session == nil || session.IsDead() || s.Difficulty() == DifficultyPeaceful {
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
			EncodeSoundPacket("minecraft:entity.phantom.ambient", SoundSourceHostile, snap.X, snap.Y, snap.Z, 1.0, 0.9, 0))
	}
	return spawned
}

// TickPhantomSpawner runs the periodic insomnia check across all online players
// every PhantomSpawnerIntervalTicks (1200 ticks = 60s).
func (s *Server) TickPhantomSpawner(rng *rand.Rand) int {
	if s == nil || s.entities == nil || s.Difficulty() == DifficultyPeaceful {
		return 0
	}
	gameTime, _ := s.WorldTime()
	mgr := s.phantomManager()
	mgr.mu.RLock()
	last := mgr.lastSpawnTick
	mgr.mu.RUnlock()
	if last > 0 && gameTime >= last && gameTime-last < PhantomSpawnerIntervalTicks {
		return 0
	}
	mgr.mu.Lock()
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
// currently exposed to burning sunlight (Overworld daytime, clear weather, open sky).
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
	return s.HasOpenSkyAbove(s.DimensionChunks(dimension), bx, by, bz)
}

// TickPhantoms steps all active Phantom entities:
//   - Despawns phantoms in Peaceful difficulty.
//   - Ignites and damages phantoms exposed to daytime sunlight.
//   - Flies in a circle 20..30 blocks above the target player, periodically
//     swooping down to bite (`PhantomAttackDamage = 6.0`), and retreating if
//     scared by a nearby cat or after striking.
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
	if st.SwoopCooldown > 0 {
		st.SwoopCooldown--
	}
	snap := *st
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()

	// 1. Daylight burning: Phantoms are undead and catch fire under daytime open sky.
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

	// 2. Validate or acquire nearest survival/adventure target player within 64 blocks.
	target := snap.TargetPlayer
	if target == nil || target.IsDead() || world.NormalizeDimension(target.Dimension()) != dim || target.Snapshot().GameMode == GameModeSpectator {
		target = s.findNearestWitherTarget(dim, ent.X, ent.Y, ent.Z, PhantomAggroRange)
		mgr.mu.Lock()
		if cur, ok := mgr.phantoms[entityID]; ok && cur != nil {
			cur.TargetPlayer = target
		}
		mgr.mu.Unlock()
	}

	var goalX, goalY, goalZ float64
	speed := PhantomFlySpeed

	if target != nil && !target.IsDead() {
		tSnap := target.Snapshot()
		anchorX := tSnap.X
		anchorY := tSnap.Y + PhantomCircleAltitudeOffset
		anchorZ := tSnap.Z

		if snap.Phase == PhantomPhaseCircling {
			angle := snap.OrbitAngle + PhantomCircleAngularSpeed
			goalX = anchorX + math.Cos(angle)*PhantomCircleRadius
			goalY = anchorY
			goalZ = anchorZ + math.Sin(angle)*PhantomCircleRadius

			nextPhase := PhantomPhaseCircling
			if snap.SwoopCooldown <= 0 {
				nextPhase = PhantomPhaseSwooping
				cp := ent.ChunkPos()
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket("minecraft:entity.phantom.swoop", SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			}
			mgr.mu.Lock()
			if cur, ok := mgr.phantoms[entityID]; ok && cur != nil {
				cur.AnchorX, cur.AnchorY, cur.AnchorZ = anchorX, anchorY, anchorZ
				cur.OrbitAngle = angle
				if nextPhase == PhantomPhaseSwooping {
					cur.Phase = PhantomPhaseSwooping
					cur.PhaseTicks = 0
				}
			}
			mgr.mu.Unlock()
		} else {
			// Swooping down toward the target player.
			speed = PhantomSwoopSpeed
			goalX = tSnap.X
			goalY = tSnap.Y + 0.8
			goalZ = tSnap.Z

			// Cats scare swooping phantoms away (vanilla CatRelaxOnOwnerGoal / PhantomAttackPlayerTargetGoal).
			if s.hasCatNearPosition(dim, tSnap.X, tSnap.Y, tSnap.Z, PhantomCatScareRange) || snap.PhaseTicks >= PhantomMaxSwoopTicks {
				mgr.mu.Lock()
				if cur, ok := mgr.phantoms[entityID]; ok && cur != nil {
					cur.Phase = PhantomPhaseCircling
					cur.PhaseTicks = 0
					cur.SwoopCooldown = PhantomSwoopIntervalTicks
				}
				mgr.mu.Unlock()
			}
		}
	} else {
		// No player target: circle around current anchor.
		angle := snap.OrbitAngle + PhantomCircleAngularSpeed
		goalX = snap.AnchorX + math.Cos(angle)*PhantomCircleRadius
		goalY = snap.AnchorY
		goalZ = snap.AnchorZ + math.Sin(angle)*PhantomCircleRadius
		mgr.mu.Lock()
		if cur, ok := mgr.phantoms[entityID]; ok && cur != nil {
			cur.OrbitAngle = angle
		}
		mgr.mu.Unlock()
	}

	// Move 3D toward (goalX, goalY, goalZ).
	dx := goalX - ent.X
	dy := goalY - ent.Y
	dz := goalZ - ent.Z
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	nextX, nextY, nextZ := ent.X, ent.Y, ent.Z
	yaw := ent.Yaw
	pitch := ent.Pitch
	if dist > 1e-4 {
		step := speed
		if step > dist {
			step = dist
		}
		vx := (dx / dist) * step
		vy := (dy / dist) * step
		vz := (dz / dist) * step
		nextX += vx
		nextY += vy
		nextZ += vz
		yaw = yawToward(dx, dz)
		pitch = float32(-math.Atan2(dy, math.Hypot(dx, dz)) * (180.0 / math.Pi))
		s.entities.Update(entityID, func(e *world.Entity) {
			e.X = nextX
			e.Y = nextY
			e.Z = nextZ
			e.Yaw = yaw
			e.Pitch = pitch
			e.HeadYaw = yaw
			e.VelocityX = clampVelocityShort(vx)
			e.VelocityY = clampVelocityShort(vy)
			e.VelocityZ = clampVelocityShort(vz)
			e.OnGround = false
		})
	}

	// Check swoop melee bite against target player.
	if snap.Phase == PhantomPhaseSwooping && target != nil && !target.IsDead() {
		tSnap := target.Snapshot()
		adx := tSnap.X - nextX
		ady := (tSnap.Y + 0.8) - nextY
		adz := tSnap.Z - nextZ
		if adx*adx+ady*ady+adz*adz <= PhantomAttackReach*PhantomAttackReach {
			s.AttackPlayerFromMob(entityID, MobTypePhantom, nextX, nextZ, target, PhantomAttackDamage)
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket("minecraft:entity.phantom.bite", SoundSourceHostile, nextX, nextY, nextZ, 1.0, 1.0, 0))
			mgr.mu.Lock()
			if cur, ok := mgr.phantoms[entityID]; ok && cur != nil {
				cur.Phase = PhantomPhaseCircling
				cur.PhaseTicks = 0
				cur.SwoopCooldown = PhantomSwoopIntervalTicks
			}
			mgr.mu.Unlock()
		}
	}
}

func (s *Server) hasCatNearPosition(dim string, x, y, z, radius float64) bool {
	if s == nil || s.entities == nil {
		return false
	}
	radSq := radius * radius
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		e := &allEnts[i]
		if canonicalResourceName(e.TypeName) != MobTypeCat || e.EffectiveDimension() != dim {
			continue
		}
		dx, dy, dz := e.X-x, e.Y-y, e.Z-z
		if dx*dx+dy*dy+dz*dz <= radSq {
			return true
		}
	}
	return false
}
