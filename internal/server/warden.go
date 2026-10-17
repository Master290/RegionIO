package server

import (
	"errors"
	"math"
	"math/rand"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Warden (minecraft:warden) constants, attributes, anger thresholds, poses,
// entity events, and sounds for Minecraft 26.1.2 (protocol 775).
const (
	MobTypeWarden       = "minecraft:warden"
	EntityTypeWardenID  = 143
	SculkCatalystItemID = "minecraft:sculk_catalyst"

	// Vanilla 26.1.2 Warden attributes.
	DefaultWardenMaxHealth    float32 = 500.0
	WardenCalmWalkSpeed       float64 = 0.30
	WardenChaseSpeed          float64 = 0.38
	WardenAttackDamageEasy    float32 = 16.0
	WardenAttackDamageNormal  float32 = 30.0
	WardenAttackDamageHard    float32 = 45.0
	WardenAttackKnockback     float64 = 1.5
	WardenKnockbackResistance float64 = 1.0
	WardenMeleeReach          float64 = 2.2
	WardenMeleeVerticalReach  float64 = 3.0
	WardenMeleeCooldownTicks  int     = 18  // 0.9s between melee swings
	WardenShieldDisableTicks  int     = 100 // 5.0s shield disable on Warden melee hit
	WardenDeathXP             int32   = 5

	// Summoning, Emerging, Digging, Roaring, and Sniffing durations (in ticks at 20 TPS).
	WardenSummonCheckRadius     float64 = 48.0 // Do not summon if an alive Warden is within 48 blocks
	WardenEmergingDurationTicks int     = 134  // 6.7s emerging animation
	WardenDiggingDurationTicks  int     = 100  // 5.0s digging back into the ground
	WardenCalmDigTimeoutTicks   int     = 1200 // 60.0s without anger/suspects before digging
	WardenRoarDurationTicks     int     = 84   // 4.2s roar animation when crossing >= 80 anger
	WardenSniffDurationTicks    int     = 84   // 4.2s sniffing animation
	WardenSniffIntervalTicks    int     = 100  // 5.0s cooldown between idle sniffs

	// Anger Management per suspect (0..150).
	WardenMinAnger                    int = 0
	WardenAgitatedAngerThreshold      int = 40
	WardenAngryThreshold              int = 80
	WardenMaxAnger                    int = 150
	WardenAngerDecayIntervalTicks     int = 20  // -1 anger per second (20 ticks) for all suspects
	WardenVibrationAngerBoost         int = 35  // Standard vibration anger increase
	WardenRoarAngerBoost              int = 20  // Extra +20 anger when roaring at a new target
	WardenSniffAngerBoost             int = 35  // +35 anger to nearest smelled entity on sniff completion
	WardenTouchAngerBoost             int = 35  // +35 anger when an entity collides with the Warden
	WardenHurtAngerBoost              int = 100 // +100 anger when attacked by an entity/player
	WardenProjectileDistractAnger     int = 10  // +10 anger on first projectile distraction
	WardenProjectileRepeatAnger       int = 35  // +35 anger on second projectile within 100 ticks (5s)
	WardenProjectileRepeatWindowTicks int = 100 // 5.0s window for repeated projectile anger

	// Warden sensory detection ranges.
	WardenVibrationListenRadius float64 = 16.0
	WardenVibrationCooldown     int     = 40 // 2.0s listener cooldown after absorbing a vibration
	WardenSniffHorizontalRadius float64 = 6.0
	WardenSniffVerticalRadius   float64 = 20.0
	WardenSmellDetectRadius     float64 = 24.0
	WardenTouchCollisionRadius  float64 = 1.2

	// Sonic Boom ranged beam attack parameters.
	WardenSonicBoomHorizRange    float64 = 15.0
	WardenSonicBoomVertRange     float64 = 20.0
	WardenSonicBoomMinDist       float64 = 4.0
	WardenSonicBoomChargeTicks   int     = 34  // 1.7s wind-up before firing beam
	WardenSonicBoomTotalTicks    int     = 60  // 3.0s total sonic boom state duration
	WardenSonicBoomCooldownTicks int     = 200 // 10.0s cooldown between sonic booms
	WardenSonicBoomDamage        float32 = 10.0
	WardenSonicBoomHorizKB       float64 = 2.5
	WardenSonicBoomVertKB        float64 = 0.5

	// Darkness pulse parameters.
	WardenDarknessPulseIntervalTicks  int     = 120 // Every 6.0s (120 ticks)
	WardenDarknessPulseRadius         float64 = 20.0
	WardenDarknessEffectDurationTicks int32   = 260 // 13.0s Darkness duration

	// SynchedEntityData indices and serializers in Minecraft 26.1.2 (protocol 775).
	EntityDataPoseID              byte  = 6  // Entity.DATA_POSE (POSE serializer = 21)
	EntityDataSerializerPose      int32 = 21 // EntityDataSerializers.POSE
	EntityDataWardenClientAngerID byte  = 16 // Warden.CLIENT_ANGER_LEVEL (INT serializer = 1)

	// net.minecraft.world.entity.Pose enum ordinals in Minecraft 26.1.2.
	PoseStanding    byte = 0
	PoseFallFlying  byte = 1
	PoseSleeping    byte = 2
	PoseSwimming    byte = 3
	PoseSpinAttack  byte = 4
	PoseCrouching   byte = 5
	PoseLongJumping byte = 6
	PoseDying       byte = 7
	PoseCroaking    byte = 8
	PoseUsingTongue byte = 9
	PoseSitting     byte = 10
	PoseRoaring     byte = 11
	PoseSniffing    byte = 12
	PoseEmerging    byte = 13
	PoseDigging     byte = 14
	PoseSliding     byte = 15
	PoseShooting    byte = 16
	PoseInhaling    byte = 17

	// ClientboundEntityEventPacket (0x22) event IDs for Warden animations.
	EntityEventWardenTendrilShaking byte = 61 // EntityEvent.TENDRIL_SHAKING
	EntityEventWardenSonicBoom      byte = 62 // EntityEvent.SONIC_ATTACK

	// Canonical Warden sound events.
	SoundWardenEmerge         = "minecraft:entity.warden.emerge"
	SoundWardenDig            = "minecraft:entity.warden.dig"
	SoundWardenRoar           = "minecraft:entity.warden.roar"
	SoundWardenSniff          = "minecraft:entity.warden.sniff"
	SoundWardenListening      = "minecraft:entity.warden.listening"
	SoundWardenListeningAngry = "minecraft:entity.warden.listening_angry"
	SoundWardenTendrilClicks  = "minecraft:entity.warden.tendril_clicks"
	SoundWardenHeartbeat      = "minecraft:entity.warden.heartbeat"
	SoundWardenAttackImpact   = "minecraft:entity.warden.attack_impact"
	SoundWardenSonicCharge    = "minecraft:entity.warden.sonic_charge"
	SoundWardenSonicBoom      = "minecraft:entity.warden.sonic_boom"
	SoundWardenHurt           = "minecraft:entity.warden.hurt"
	SoundWardenDeath          = "minecraft:entity.warden.death"
	SoundWardenStep           = "minecraft:entity.warden.step"
	SoundWardenAmbient        = "minecraft:entity.warden.ambient"
	SoundWardenAgitated       = "minecraft:entity.warden.agitated"
	SoundWardenAngry          = "minecraft:entity.warden.angry"
	SoundWardenNearbyClose    = "minecraft:entity.warden.nearby_close"
	SoundWardenNearbyCloser   = "minecraft:entity.warden.nearby_closer"
	SoundWardenNearbyClosest  = "minecraft:entity.warden.nearby_closest"
)

// WardenPhase identifies the current behavioral state of a Warden.
type WardenPhase uint8

const (
	WardenPhaseEmerging WardenPhase = iota
	WardenPhaseCalm
	WardenPhaseInvestigating
	WardenPhaseSniffing
	WardenPhaseRoaring
	WardenPhaseChasing
	WardenPhaseSonicBoom
	WardenPhaseDigging
)

// String returns a human-readable name for the Warden's current phase.
func (p WardenPhase) String() string {
	switch p {
	case WardenPhaseEmerging:
		return "emerging"
	case WardenPhaseCalm:
		return "calm"
	case WardenPhaseInvestigating:
		return "investigating"
	case WardenPhaseSniffing:
		return "sniffing"
	case WardenPhaseRoaring:
		return "roaring"
	case WardenPhaseChasing:
		return "chasing"
	case WardenPhaseSonicBoom:
		return "sonic_boom"
	case WardenPhaseDigging:
		return "digging"
	default:
		return "calm"
	}
}

// WardenAngerStage classifies the Warden's anger level into Calm (0..39),
// Agitated (40..79), or Angry (80..150).
type WardenAngerStage uint8

const (
	WardenAngerStageCalm WardenAngerStage = iota
	WardenAngerStageAgitated
	WardenAngerStageAngry
)

// ClassifyWardenAnger returns the WardenAngerStage for a raw anger value.
func ClassifyWardenAnger(anger int) WardenAngerStage {
	if anger >= WardenAngryThreshold {
		return WardenAngerStageAngry
	}
	if anger >= WardenAgitatedAngerThreshold {
		return WardenAngerStageAgitated
	}
	return WardenAngerStageCalm
}

// WardenHeartbeatIntervalTicks returns the client/server heartbeat interval in
// ticks corresponding to the Warden's synced client anger level (0..150):
//   - Calm (0..39):      40 ticks (2.0s)
//   - Agitated (40..79): 20 ticks (1.0s)
//   - Angry (80..150):   10 ticks (0.5s)
func WardenHeartbeatIntervalTicks(anger int) int {
	switch ClassifyWardenAnger(anger) {
	case WardenAngerStageAngry:
		return 10
	case WardenAngerStageAgitated:
		return 20
	default:
		return 40
	}
}

// VibrationEvent describes a world game event / vibration emitted by an entity,
// player, projectile, or block action.
type VibrationEvent struct {
	Dimension          string
	Type               string
	X, Y, Z            float64
	SourceEntityID     int32
	ProjectileEntityID int32
	Player             *PlayerSession
	Sneaking           bool
	BlockOrItemID      string
}

// WardenSuspectEntry stores the anger level and metadata for a single suspect
// tracked by a Warden's AngerManagement.
type WardenSuspectEntry struct {
	EntityID       int32
	IsPlayer       bool
	PlayerUUID     [16]byte
	Anger          int
	LastKnownX     float64
	LastKnownY     float64
	LastKnownZ     float64
	HasRoaredAt    bool
	LastProjectile int // TickCounter of the most recent projectile distraction from this suspect
}

// WardenState holds the authoritative server-side AI state for an active
// minecraft:warden entity.
type WardenState struct {
	EntityID  int32
	Dimension string
	X, Y, Z   float64
	Yaw       float32
	Health    float32
	MaxHealth float32

	Phase               WardenPhase
	Pose                byte
	PhaseTicks          int
	TickCounter         int
	TicksWithoutAnger   int
	MeleeCooldown       int
	SniffCooldown       int
	VibrationCooldown   int
	SonicBoomCooldown   int
	SonicBoomTicks      int
	SonicBoomTargetID   int32
	DarknessPulseTicker int
	HeartbeatTicker     int

	HasDisturbancePos bool
	DisturbanceX      float64
	DisturbanceY      float64
	DisturbanceZ      float64

	ActiveTargetID       int32
	ActiveTargetIsPlayer bool
	ClientAngerLevel     int
	Suspects             map[int32]*WardenSuspectEntry
}

// Clone returns a deep copy of w (including a cloned Suspects map).
func (w WardenState) Clone() WardenState {
	out := w
	if len(w.Suspects) > 0 {
		out.Suspects = make(map[int32]*WardenSuspectEntry, len(w.Suspects))
		for k, v := range w.Suspects {
			if v != nil {
				cp := *v
				out.Suspects[k] = &cp
			}
		}
	} else {
		out.Suspects = make(map[int32]*WardenSuspectEntry)
	}
	return out
}

type serverWardenManager struct {
	mu          sync.RWMutex
	wardens     map[int32]*WardenState
	shieldLocks map[*PlayerSession]int // remaining ticks of disabled shield
}

var serverWardens sync.Map // map[*Server]*serverWardenManager

func (s *Server) wardenManager() *serverWardenManager {
	if v, ok := serverWardens.Load(s); ok {
		return v.(*serverWardenManager)
	}
	mgr := &serverWardenManager{
		wardens:     make(map[int32]*WardenState),
		shieldLocks: make(map[*PlayerSession]int),
	}
	actual, loaded := serverWardens.LoadOrStore(s, mgr)
	if !loaded && s != nil {
		s.EnsureWardenSummonHook()
	}
	return actual.(*serverWardenManager)
}

// EnsureWardenSummonHook registers s.SummonWardenFromShrieker with Agent C's
// sculk shrieker system if no custom hook has been set on s yet.
func (s *Server) EnsureWardenSummonHook() {
	if s == nil {
		return
	}
	st := s.sculkState()
	st.mu.Lock()
	hasHook := st.wardenSummonHook != nil
	st.mu.Unlock()
	if !hasHook {
		s.SetWardenSummonHook(s.SummonWardenFromShrieker)
	}
}

// RegisterWardenSummonHook explicitly registers s.SummonWardenFromShrieker as
// the server's Warden summon callback for sculk shriekers.
func (s *Server) RegisterWardenSummonHook() {
	if s == nil {
		return
	}
	s.SetWardenSummonHook(s.SummonWardenFromShrieker)
}

// IsWardenEntity reports whether entityID belongs to an active minecraft:warden.
func (s *Server) IsWardenEntity(entityID int32) bool {
	if s == nil || entityID == 0 {
		return false
	}
	mgr := s.wardenManager()
	mgr.mu.RLock()
	_, ok := mgr.wardens[entityID]
	mgr.mu.RUnlock()
	if ok {
		return true
	}
	if s.entities != nil {
		if ent := s.entities.Get(entityID); ent != nil {
			return canonicalResourceName(ent.TypeName) == MobTypeWarden
		}
	}
	return false
}

// CanEntityTriggerSculk reports whether movements or actions by entityID are
// allowed to trigger sculk sensors or shriekers. A Warden's own steps and
// actions never trigger sculk sensors or shriekers.
func (s *Server) CanEntityTriggerSculk(entityID int32) bool {
	if s == nil || entityID == 0 {
		return true
	}
	return !s.IsWardenEntity(entityID)
}

// EmitVibration dispatches a world vibration event to both Agent C's sculk
// sensor network (EmitGameEvent) and all nearby Wardens within 16 blocks.
// Any vibration originating from a minecraft:warden is completely suppressed so
// that a Warden's own footsteps and actions never trigger sculk sensors or shriekers.
func (s *Server) EmitVibration(ev VibrationEvent) int {
	if s == nil {
		return 0
	}
	s.EnsureWardenSummonHook()

	srcID := ev.SourceEntityID
	if srcID == 0 && ev.Player != nil {
		srcID = ev.Player.EntityID
	}
	if srcID != 0 && s.IsWardenEntity(srcID) {
		return 0
	}

	dim := world.NormalizeDimension(ev.Dimension)
	ev.Dimension = dim
	ev.SourceEntityID = srcID

	// Forward to sculk sensors & shriekers.
	notifiedSensors := s.EmitGameEvent(dim, ev.Type, ev.X, ev.Y, ev.Z, VibrationSource{
		Player:        ev.Player,
		EntityID:      srcID,
		Sneaking:      ev.Sneaking,
		BlockOrItemID: ev.BlockOrItemID,
	})

	// Also notify any alive Wardens within 16 blocks in the same dimension.
	s.NotifyWardensOfVibration(ev)
	return notifiedSensors
}

// HasWardenWithinRadius reports whether an alive minecraft:warden already
// exists within radius blocks (3D Euclidean distance) of (x, y, z) in dim.
func (s *Server) HasWardenWithinRadius(dim string, x, y, z, radius float64) bool {
	if s == nil || radius <= 0 {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	rSq := radius * radius

	mgr := s.wardenManager()
	mgr.mu.RLock()
	for _, st := range mgr.wardens {
		if st == nil || st.Health <= 0 || st.Dimension != normDim {
			continue
		}
		dx := st.X - x
		dy := st.Y - y
		dz := st.Z - z
		if dx*dx+dy*dy+dz*dz <= rSq {
			mgr.mu.RUnlock()
			return true
		}
	}
	mgr.mu.RUnlock()

	if s.entities != nil {
		for _, ent := range s.entities.AppendAll(nil) {
			if canonicalResourceName(ent.TypeName) != MobTypeWarden || ent.EffectiveDimension() != normDim {
				continue
			}
			if s.MobHealth(ent.ID) <= 0 {
				continue
			}
			dx := ent.X - x
			dy := ent.Y - y
			dz := ent.Z - z
			if dx*dx+dy*dy+dz*dz <= rSq {
				return true
			}
		}
	}
	return false
}

// SummonWardenFromShrieker is the WardenSummonHookFunc callback invoked by
// sculk_shrieker at warning level >= 4. It checks that no alive Warden is
// already within 48 blocks, resolves a valid spawn position if needed, and
// spawns the Warden in its 134-tick EMERGING pose.
func (s *Server) SummonWardenFromShrieker(
	dim string,
	spawnX, spawnY, spawnZ float64,
	shriekerX, shriekerY, shriekerZ int,
	triggerPlayer *PlayerSession,
) bool {
	if s == nil || s.Difficulty() == DifficultyPeaceful {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	centerX := float64(shriekerX) + 0.5
	centerY := float64(shriekerY)
	centerZ := float64(shriekerZ) + 0.5

	// Vanilla check: do not summon if another alive Warden is within 48 blocks.
	if s.HasWardenWithinRadius(normDim, centerX, centerY, centerZ, WardenSummonCheckRadius) {
		return false
	}

	if spawnX == 0 && spawnY == 0 && spawnZ == 0 {
		var ok bool
		spawnX, spawnY, spawnZ, ok = s.FindWardenSpawnPosition(normDim, shriekerX, shriekerY, shriekerZ)
		if !ok {
			return false
		}
	}

	id, err := s.SpawnWarden(normDim, spawnX, spawnY, spawnZ, true)
	if err != nil || id == 0 {
		return false
	}

	// Orient toward the triggering player if present.
	if triggerPlayer != nil {
		pSnap := triggerPlayer.Snapshot()
		yaw := yawToward(pSnap.X-spawnX, pSnap.Z-spawnZ)
		s.entities.Update(id, func(e *world.Entity) {
			e.Yaw = yaw
			e.HeadYaw = yaw
		})
		mgr := s.wardenManager()
		mgr.mu.Lock()
		if st := mgr.wardens[id]; st != nil {
			st.Yaw = yaw
		}
		mgr.mu.Unlock()
	}
	return true
}

// SpawnWarden spawns a minecraft:warden at (x, y, z) in dimension.
// If startEmerging is true, the Warden begins in PoseEmerging for 134 ticks
// (6.7s) and plays minecraft:entity.warden.emerge; otherwise it starts in
// PoseStanding (Calm phase).
func (s *Server) SpawnWarden(dimension string, x, y, z float64, startEmerging bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	s.EnsureWardenSummonHook()
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeWarden)
	if typeID < 0 {
		typeID = EntityTypeWardenID
	}

	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeWarden,
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
	mem.health = DefaultWardenMaxHealth
	mem.lastHealth = DefaultWardenMaxHealth
	tr.mu.Unlock()

	phase := WardenPhaseCalm
	pose := PoseStanding
	phaseTicks := 0
	if startEmerging {
		phase = WardenPhaseEmerging
		pose = PoseEmerging
		phaseTicks = WardenEmergingDurationTicks
	}

	st := &WardenState{
		EntityID:      id,
		Dimension:     normDim,
		X:             x,
		Y:             y,
		Z:             z,
		Health:        DefaultWardenMaxHealth,
		MaxHealth:     DefaultWardenMaxHealth,
		Phase:         phase,
		Pose:          pose,
		PhaseTicks:    phaseTicks,
		SniffCooldown: WardenSniffIntervalTicks,
		Suspects:      make(map[int32]*WardenSuspectEntry),
	}

	mgr := s.wardenManager()
	mgr.mu.Lock()
	mgr.wardens[id] = st
	mgr.mu.Unlock()

	cx := int32(int64(math.Floor(x)) >> 4)
	cz := int32(int64(math.Floor(z)) >> 4)
	if ent := s.entities.Get(id); ent != nil {
		s.BroadcastDimensionChunk(normDim, cx, cz, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cx, cz, PlaySetEntityDataCB,
			EncodeWardenEntityDataPacket(id, pose, 0))
	}
	if startEmerging {
		s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundWardenEmerge, SoundSourceHostile, x, y, z, 5.0, 1.0, 0))
	}
	return id, nil
}

// EncodeWardenEntityDataPacket encodes ClientboundSetEntityDataPacket (0x63)
// for a minecraft:warden in protocol 775:
//   - index 6  (POSE, serializer 21): Entity.DATA_POSE (STANDING=0, ROARING=11, SNIFFING=12, EMERGING=13, DIGGING=14, SHOOTING=16)
//   - index 16 (INT, serializer 1):   Warden.CLIENT_ANGER_LEVEL (0..150)
func EncodeWardenEntityDataPacket(entityID int32, pose byte, clientAnger int) []byte {
	if clientAnger < WardenMinAnger {
		clientAnger = WardenMinAnger
	} else if clientAnger > WardenMaxAnger {
		clientAnger = WardenMaxAnger
	}
	w := protocol.NewWriter(20)
	w.VarInt(entityID)

	w.Byte(EntityDataPoseID)
	w.VarInt(EntityDataSerializerPose)
	w.VarInt(int32(pose))

	w.Byte(EntityDataWardenClientAngerID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(int32(clientAnger))

	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeWardenEntityDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeWardenEntityDataPacket.
func DecodeWardenEntityDataPacket(r *protocol.Reader) (entityID int32, pose byte, clientAnger int, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, 0, 0, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, 0, 0, err
		}
		if idx == 0xFF {
			break
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, 0, 0, err
		}
		switch {
		case idx == EntityDataPoseID && ser == EntityDataSerializerPose:
			p, err := r.VarInt()
			if err != nil {
				return 0, 0, 0, err
			}
			pose = byte(p)
		case idx == EntityDataWardenClientAngerID && ser == EntityDataSerializerInt:
			a, err := r.VarInt()
			if err != nil {
				return 0, 0, 0, err
			}
			clientAnger = int(a)
		default:
			return 0, 0, 0, errors.New("warden: unexpected SynchedEntityData entry")
		}
	}
	return entityID, pose, clientAnger, nil
}

// WardenEntityDataPacket returns the current SynchedEntityData packet payload
// for a tracked Warden entity, or nil if unknown.
func (s *Server) WardenEntityDataPacket(entityID int32) []byte {
	st, ok := s.GetWardenState(entityID)
	if !ok {
		return nil
	}
	return EncodeWardenEntityDataPacket(entityID, st.Pose, st.ClientAngerLevel)
}

// GetWardenState returns a snapshot of the WardenState for entityID.
func (s *Server) GetWardenState(entityID int32) (WardenState, bool) {
	if s == nil || entityID == 0 {
		return WardenState{}, false
	}
	mgr := s.wardenManager()
	mgr.mu.RLock()
	st, ok := mgr.wardens[entityID]
	var out WardenState
	if ok && st != nil {
		out = st.Clone()
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// ActiveWardens returns snapshots of all currently active Wardens on the server.
func (s *Server) ActiveWardens() []WardenState {
	if s == nil {
		return nil
	}
	mgr := s.wardenManager()
	mgr.mu.RLock()
	out := make([]WardenState, 0, len(mgr.wardens))
	for _, st := range mgr.wardens {
		if st != nil {
			out = append(out, st.Clone())
		}
	}
	mgr.mu.RUnlock()
	return out
}

// WardenSuspectAnger returns the Warden's current anger (0..150) toward suspectEntityID.
func (s *Server) WardenSuspectAnger(wardenID, suspectEntityID int32) int {
	if s == nil || wardenID == 0 || suspectEntityID == 0 {
		return 0
	}
	mgr := s.wardenManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	st := mgr.wardens[wardenID]
	if st == nil || st.Suspects == nil {
		return 0
	}
	if entry := st.Suspects[suspectEntityID]; entry != nil {
		return entry.Anger
	}
	return 0
}

// AddWardenAnger increases (or sets) a Warden's anger toward suspectEntityID by
// delta, clamping to [0, 150], updating the active target (prioritizing players
// among suspects with anger >= 80), and triggering PoseRoaring (+20 bonus anger)
// the first time the Warden crosses >= 80 anger on a suspect.
func (s *Server) AddWardenAnger(
	wardenID int32,
	suspectEntityID int32,
	player *PlayerSession,
	knownX, knownY, knownZ float64,
	delta int,
	allowRoar bool,
) (newAnger int, roared bool) {
	if s == nil || wardenID == 0 || suspectEntityID == 0 || suspectEntityID == wardenID {
		return 0, false
	}
	if s.IsWardenEntity(suspectEntityID) {
		return 0, false
	}
	if player == nil {
		player = s.playerByEntityID(suspectEntityID)
	}
	if player != nil {
		snap := player.Snapshot()
		if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator || player.IsDead() {
			return 0, false
		}
		if knownX == 0 && knownY == 0 && knownZ == 0 {
			knownX, knownY, knownZ = snap.X, snap.Y, snap.Z
		}
	} else if s.entities != nil {
		if ent := s.entities.Get(suspectEntityID); ent != nil {
			if knownX == 0 && knownY == 0 && knownZ == 0 {
				knownX, knownY, knownZ = ent.X, ent.Y, ent.Z
			}
		}
	}

	mgr := s.wardenManager()
	mgr.mu.Lock()
	st := mgr.wardens[wardenID]
	if st == nil || st.Health <= 0 {
		mgr.mu.Unlock()
		return 0, false
	}
	if st.Suspects == nil {
		st.Suspects = make(map[int32]*WardenSuspectEntry)
	}
	entry := st.Suspects[suspectEntityID]
	if entry == nil {
		entry = &WardenSuspectEntry{
			EntityID: suspectEntityID,
			IsPlayer: player != nil,
		}
		if player != nil {
			entry.PlayerUUID = player.Profile.UUID
		}
		st.Suspects[suspectEntityID] = entry
	}
	if player != nil {
		entry.IsPlayer = true
		entry.PlayerUUID = player.Profile.UUID
	}
	entry.LastKnownX = knownX
	entry.LastKnownY = knownY
	entry.LastKnownZ = knownZ

	prevAnger := entry.Anger
	entry.Anger = clampWardenAnger(prevAnger + delta)
	st.TicksWithoutAnger = 0
	st.HasDisturbancePos = true
	st.DisturbanceX = knownX
	st.DisturbanceY = knownY
	st.DisturbanceZ = knownZ

	// When crossing into Angry (>= 80) for the first time on this suspect, the
	// Warden roars for 84 ticks and gains an additional +20 anger!
	var syncNeeded bool
	if allowRoar && prevAnger < WardenAngryThreshold && entry.Anger >= WardenAngryThreshold && !entry.HasRoaredAt {
		entry.HasRoaredAt = true
		entry.Anger = clampWardenAnger(entry.Anger + WardenRoarAngerBoost)
		if st.Phase != WardenPhaseEmerging && st.Phase != WardenPhaseDigging && st.Phase != WardenPhaseSonicBoom {
			st.Phase = WardenPhaseRoaring
			st.Pose = PoseRoaring
			st.PhaseTicks = WardenRoarDurationTicks
			st.Yaw = yawToward(knownX-st.X, knownZ-st.Z)
			roared = true
			syncNeeded = true
		}
	}

	prevClientAnger := st.ClientAngerLevel
	recomputeWardenTargetLocked(st)
	if st.ClientAngerLevel != prevClientAnger {
		syncNeeded = true
	}
	newAnger = entry.Anger
	snap := st.Clone()
	mgr.mu.Unlock()

	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	if roared {
		s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundWardenRoar, SoundSourceHostile, snap.X, snap.Y, snap.Z, 3.0, 1.0, 0))
	}
	if syncNeeded {
		s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySetEntityDataCB,
			EncodeWardenEntityDataPacket(wardenID, snap.Pose, snap.ClientAngerLevel))
	}
	return newAnger, roared
}

func clampWardenAnger(a int) int {
	if a < WardenMinAnger {
		return WardenMinAnger
	}
	if a > WardenMaxAnger {
		return WardenMaxAnger
	}
	return a
}

// recomputeWardenTargetLocked selects the Warden's active target and updates
// ClientAngerLevel. Among suspects with anger >= 80 (WardenAngryThreshold),
// players always take priority over non-player mobs; ties are broken by highest
// anger. ClientAngerLevel is the active target's anger (or the highest anger
// among all suspects when no suspect has reached >= 80 yet).
func recomputeWardenTargetLocked(st *WardenState) {
	if st == nil {
		return
	}
	var (
		bestAngryPlayer *WardenSuspectEntry
		bestAngryMob    *WardenSuspectEntry
		maxAngerOverall int
	)
	for id, entry := range st.Suspects {
		if entry == nil || entry.Anger <= 0 {
			delete(st.Suspects, id)
			continue
		}
		if entry.Anger > maxAngerOverall {
			maxAngerOverall = entry.Anger
		}
		if entry.Anger >= WardenAngryThreshold {
			if entry.IsPlayer {
				if bestAngryPlayer == nil || entry.Anger > bestAngryPlayer.Anger ||
					(entry.Anger == bestAngryPlayer.Anger && entry.EntityID < bestAngryPlayer.EntityID) {
					bestAngryPlayer = entry
				}
			} else {
				if bestAngryMob == nil || entry.Anger > bestAngryMob.Anger ||
					(entry.Anger == bestAngryMob.Anger && entry.EntityID < bestAngryMob.EntityID) {
					bestAngryMob = entry
				}
			}
		}
	}

	if bestAngryPlayer != nil {
		st.ActiveTargetID = bestAngryPlayer.EntityID
		st.ActiveTargetIsPlayer = true
		st.ClientAngerLevel = bestAngryPlayer.Anger
	} else if bestAngryMob != nil {
		st.ActiveTargetID = bestAngryMob.EntityID
		st.ActiveTargetIsPlayer = false
		st.ClientAngerLevel = bestAngryMob.Anger
	} else {
		st.ActiveTargetID = 0
		st.ActiveTargetIsPlayer = false
		st.ClientAngerLevel = maxAngerOverall
	}
}

// NotifyWardensOfVibration alerts all alive Wardens within 16 blocks in the
// event's dimension of a vibration, applying wool occlusion and stealth rules,
// shaking their tendrils (EntityEvent 61), orienting toward the vibration, and
// increasing anger toward the source suspect (+35 for normal vibrations, or
// +10 / +35 for projectile distractions within 100 ticks).
func (s *Server) NotifyWardensOfVibration(ev VibrationEvent) int {
	if s == nil {
		return 0
	}
	normDim := world.NormalizeDimension(ev.Dimension)
	srcID := ev.SourceEntityID
	if srcID == 0 && ev.Player != nil {
		srcID = ev.Player.EntityID
	}
	if srcID != 0 && s.IsWardenEntity(srcID) {
		return 0
	}
	if IsWoolOrCarpetName(ev.BlockOrItemID) {
		return 0
	}
	if ev.Sneaking {
		switch canonicalResourceName(ev.Type) {
		case GameEventStep, GameEventSwim, GameEventHitGround:
			return 0
		}
	}

	cache := s.DimensionChunks(normDim)
	if cache != nil && canonicalResourceName(ev.Type) == GameEventStep {
		bx := int(math.Floor(ev.X))
		by := int(math.Floor(ev.Y - 0.2))
		bz := int(math.Floor(ev.Z))
		if IsWoolOrCarpetBlock(cache.GetBlock(bx, by, bz)) {
			return 0
		}
	}

	mgr := s.wardenManager()
	mgr.mu.RLock()
	ids := make([]int32, 0, len(mgr.wardens))
	for id, st := range mgr.wardens {
		if st != nil && st.Health > 0 && st.Dimension == normDim {
			ids = append(ids, id)
		}
	}
	mgr.mu.RUnlock()

	notified := 0
	maxDistSq := WardenVibrationListenRadius * WardenVibrationListenRadius
	for _, wardenID := range ids {
		if s.notifySingleWardenVibration(wardenID, ev, srcID, cache, maxDistSq) {
			notified++
		}
	}
	return notified
}

func (s *Server) notifySingleWardenVibration(
	wardenID int32,
	ev VibrationEvent,
	srcID int32,
	cache *world.Cache,
	maxDistSq float64,
) bool {
	mgr := s.wardenManager()
	mgr.mu.Lock()
	st := mgr.wardens[wardenID]
	if st == nil || st.Health <= 0 || st.Phase == WardenPhaseEmerging || st.Phase == WardenPhaseDigging {
		mgr.mu.Unlock()
		return false
	}
	if st.VibrationCooldown > 0 {
		mgr.mu.Unlock()
		return false
	}
	dx := ev.X - st.X
	dy := ev.Y - (st.Y + 1.4)
	dz := ev.Z - st.Z
	if dx*dx+dy*dy+dz*dz > maxDistSq {
		mgr.mu.Unlock()
		return false
	}
	if cache != nil && IsVibrationOccluded(cache.GetBlock, ev.X, ev.Y, ev.Z, st.X, st.Y+1.4, st.Z) {
		mgr.mu.Unlock()
		return false
	}

	st.VibrationCooldown = WardenVibrationCooldown
	st.TicksWithoutAnger = 0
	st.HasDisturbancePos = true
	st.DisturbanceX = ev.X
	st.DisturbanceY = ev.Y
	st.DisturbanceZ = ev.Z
	st.Yaw = yawToward(ev.X-st.X, ev.Z-st.Z)

	// Interrupt sniffing when a fresh vibration is heard.
	var poseChanged bool
	if st.Phase == WardenPhaseCalm || st.Phase == WardenPhaseSniffing {
		if st.Pose != PoseStanding {
			st.Pose = PoseStanding
			poseChanged = true
		}
		st.Phase = WardenPhaseInvestigating
		st.PhaseTicks = 0
	}

	// Determine anger delta for projectile vs regular vibration.
	evType := canonicalResourceName(ev.Type)
	isProjectileEvent := evType == GameEventProjectileLand || evType == GameEventProjectileShoot || ev.ProjectileEntityID != 0
	angerDelta := WardenVibrationAngerBoost
	if isProjectileEvent && srcID != 0 {
		if st.Suspects == nil {
			st.Suspects = make(map[int32]*WardenSuspectEntry)
		}
		entry := st.Suspects[srcID]
		if entry != nil && entry.LastProjectile > 0 && (st.TickCounter-entry.LastProjectile) <= WardenProjectileRepeatWindowTicks {
			angerDelta = WardenProjectileRepeatAnger
		} else {
			angerDelta = WardenProjectileDistractAnger
		}
		if entry == nil {
			entry = &WardenSuspectEntry{
				EntityID: srcID,
				IsPlayer: ev.Player != nil,
			}
			if ev.Player != nil {
				entry.PlayerUUID = ev.Player.Profile.UUID
			}
			st.Suspects[srcID] = entry
		}
		if st.TickCounter <= 0 {
			st.TickCounter = 1
		}
		entry.LastProjectile = st.TickCounter
	}

	wX, wY, wZ := st.X, st.Y, st.Z
	wDim := st.Dimension
	wPose := st.Pose
	wAnger := st.ClientAngerLevel
	mgr.mu.Unlock()

	cx := int32(int64(math.Floor(wX)) >> 4)
	cz := int32(int64(math.Floor(wZ)) >> 4)

	// Broadcast tendril shaking (EntityEvent 61) and listening sounds.
	s.BroadcastDimensionChunk(wDim, cx, cz, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(wardenID, EntityEventWardenTendrilShaking))
	s.BroadcastDimensionChunk(wDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundWardenTendrilClicks, SoundSourceHostile, wX, wY, wZ, 2.0, 1.0, 0))
	listenSound := SoundWardenListening
	if ClassifyWardenAnger(wAnger) >= WardenAngerStageAgitated {
		listenSound = SoundWardenListeningAngry
	}
	s.BroadcastDimensionChunk(wDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(listenSound, SoundSourceHostile, wX, wY, wZ, 3.0, 1.0, 0))
	if poseChanged {
		s.BroadcastDimensionChunk(wDim, cx, cz, PlaySetEntityDataCB,
			EncodeWardenEntityDataPacket(wardenID, wPose, wAnger))
	}

	if srcID != 0 {
		s.AddWardenAnger(wardenID, srcID, ev.Player, ev.X, ev.Y, ev.Z, angerDelta, true)
	}
	return true
}

// PerformWardenSniff locates the nearest valid player or mob within the
// Warden's smell range (6 horizontal / 20 vertical blocks, or up to 24 blocks)
// and adds +35 anger toward that suspect, setting its position as the Warden's
// disturbance goal.
func (s *Server) PerformWardenSniff(wardenID int32) (smelledEntityID int32, newAnger int) {
	if s == nil || wardenID == 0 {
		return 0, 0
	}
	st, ok := s.GetWardenState(wardenID)
	if !ok || st.Health <= 0 {
		return 0, 0
	}

	bestID, bestPlayer, bestX, bestY, bestZ := s.findNearestSmellableSuspect(st.Dimension, wardenID, st.X, st.Y, st.Z)
	if bestID == 0 {
		return 0, 0
	}
	newAnger, _ = s.AddWardenAnger(wardenID, bestID, bestPlayer, bestX, bestY, bestZ, WardenSniffAngerBoost, true)
	return bestID, newAnger
}

func (s *Server) findNearestSmellableSuspect(
	dim string,
	wardenID int32,
	wx, wy, wz float64,
) (entityID int32, player *PlayerSession, tx, ty, tz float64) {
	normDim := world.NormalizeDimension(dim)
	bestDistSq := WardenSmellDetectRadius * WardenSmellDetectRadius

	// Check survival/adventure players in the same dimension.
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		snap := p.Snapshot()
		if world.NormalizeDimension(snap.Dimension) != normDim {
			continue
		}
		if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator || snap.Health <= 0 {
			continue
		}
		dx := snap.X - wx
		dy := math.Abs(snap.Y - wy)
		dz := snap.Z - wz
		horiz := math.Hypot(dx, dz)
		if dy > WardenSniffVerticalRadius || horiz > WardenSmellDetectRadius {
			continue
		}
		distSq := dx*dx + dy*dy + dz*dz
		if distSq < bestDistSq {
			bestDistSq = distSq
			entityID = snap.EntityID
			player = p
			tx, ty, tz = snap.X, snap.Y, snap.Z
		}
	}

	// If no player within primary 6x20 sniff cylinder, also check non-warden mobs.
	if s.entities != nil {
		for _, ent := range s.entities.AppendAll(nil) {
			if ent.ID == wardenID || ent.IsItem() || ent.IsExperienceOrb() ||
				IsDecorationEntity(ent.TypeName) || ent.TypeName == EntityTypeFishingBobber ||
				canonicalResourceName(ent.TypeName) == MobTypeWarden {
				continue
			}
			if ent.EffectiveDimension() != normDim {
				continue
			}
			dx := ent.X - wx
			dy := math.Abs(ent.Y - wy)
			dz := ent.Z - wz
			horiz := math.Hypot(dx, dz)
			if dy > WardenSniffVerticalRadius || horiz > WardenSmellDetectRadius {
				continue
			}
			distSq := dx*dx + dy*dy + dz*dz
			if distSq < bestDistSq {
				bestDistSq = distSq
				entityID = ent.ID
				player = nil
				tx, ty, tz = ent.X, ent.Y, ent.Z
			}
		}
	}
	return entityID, player, tx, ty, tz
}

// IsPlayerShieldDisabled reports whether session's shield is currently on
// cooldown after being struck by a Warden melee attack.
func (s *Server) IsPlayerShieldDisabled(session *PlayerSession) bool {
	if s == nil || session == nil {
		return false
	}
	mgr := s.wardenManager()
	mgr.mu.RLock()
	rem := mgr.shieldLocks[session]
	mgr.mu.RUnlock()
	return rem > 0
}

// DisablePlayerShield disables session's active shield blocking and places the
// shield on cooldown for ticks (100 ticks = 5.0s for Warden melee hits).
func (s *Server) DisablePlayerShield(session *PlayerSession, ticks int) {
	if s == nil || session == nil || ticks <= 0 {
		return
	}
	session.SetBlockingWithShield(false)
	mgr := s.wardenManager()
	mgr.mu.Lock()
	if ticks > mgr.shieldLocks[session] {
		mgr.shieldLocks[session] = ticks
	}
	mgr.mu.Unlock()

	snap := session.Snapshot()
	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.shield.break", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 1.0, 0.9, 0))
}

// WardenMeleeBaseDamageForDifficulty returns the vanilla 26.1.2 Warden melee
// damage for the given world difficulty (16 Easy, 30 Normal, 45 Hard).
func WardenMeleeBaseDamageForDifficulty(diff Difficulty) float32 {
	switch diff {
	case DifficultyPeaceful:
		return 0
	case DifficultyEasy:
		return WardenAttackDamageEasy
	case DifficultyHard:
		return WardenAttackDamageHard
	default:
		return WardenAttackDamageNormal
	}
}

// AttackFromWarden executes a Warden melee strike against a player or mob,
// disabling the player's shield for 100 ticks (5s) if blocking, dealing
// difficulty-scaled melee damage (16/30/45), applying strong knockback (1.5),
// and broadcasting minecraft:entity.warden.attack_impact.
func (s *Server) AttackFromWarden(wardenID int32, targetEntityID int32) float32 {
	if s == nil || wardenID == 0 || targetEntityID == 0 {
		return 0
	}
	st, ok := s.GetWardenState(wardenID)
	if !ok || st.Health <= 0 {
		return 0
	}
	cx := int32(int64(math.Floor(st.X)) >> 4)
	cz := int32(int64(math.Floor(st.Z)) >> 4)

	s.BroadcastDimensionChunk(st.Dimension, cx, cz, PlayAnimateCB,
		EncodeAnimatePacket(wardenID, AnimateSwingMainHand))
	s.BroadcastDimensionChunk(st.Dimension, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundWardenAttackImpact, SoundSourceHostile, st.X, st.Y, st.Z, 2.0, 1.0, 0))

	if player := s.playerByEntityID(targetEntityID); player != nil {
		if player.IsBlockingWithShield() {
			s.DisablePlayerShield(player, WardenShieldDisableTicks)
		}
		rawDmg := WardenMeleeBaseDamageForDifficulty(s.Difficulty())
		if rawDmg <= 0 {
			return 0
		}
		dealt := s.DamagePlayerWithArmor(player, rawDmg, "warden")
		if dealt > 0 {
			pSnap := player.Snapshot()
			if player.IsDead() {
				s.TriggerKilledByEntity(player, MobTypeWarden)
				s.BroadcastDeathMessage(player, "warden", MobTypeWarden)
			} else {
				vx, vy, vz := ComputeKnockback(st.X, st.Z, st.Yaw, pSnap.X, pSnap.Z, WardenAttackKnockback)
				if player.send != nil {
					_ = player.send(protocol.PlaySetEntityMotion, EncodeSetEntityMotionPacket(player.EntityID, vx, vy, vz))
				}
			}
		}
		return dealt
	}

	if s.entities != nil {
		if targetEnt := s.entities.Get(targetEntityID); targetEnt != nil {
			rawDmg := WardenMeleeBaseDamageForDifficulty(s.Difficulty())
			s.applyDamageToMob(targetEnt, rawDmg, nil, WardenAttackKnockback, false, true)
			return rawDmg
		}
	}
	return 0
}

// FireWardenSonicBoom fires the Warden's ranged sonic boom beam at targetEntityID.
// Sonic Boom travels through solid walls, cannot be blocked by shields, bypasses
// armor reduction (dealing 10.0 sonic/magic damage scaled by difficulty), and
// applies strong horizontal (2.5) and vertical (0.5) knockback.
func (s *Server) FireWardenSonicBoom(wardenID int32, targetEntityID int32) float32 {
	if s == nil || wardenID == 0 || targetEntityID == 0 {
		return 0
	}
	st, ok := s.GetWardenState(wardenID)
	if !ok || st.Health <= 0 {
		return 0
	}
	cx := int32(int64(math.Floor(st.X)) >> 4)
	cz := int32(int64(math.Floor(st.Z)) >> 4)

	s.BroadcastDimensionChunk(st.Dimension, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundWardenSonicBoom, SoundSourceHostile, st.X, st.Y+1.6, st.Z, 3.0, 1.0, 0))

	if player := s.playerByEntityID(targetEntityID); player != nil {
		if player.IsDead() {
			return 0
		}
		pSnap := player.Snapshot()
		if pSnap.GameMode == GameModeCreative || pSnap.GameMode == GameModeSpectator {
			return 0
		}
		dx := pSnap.X - st.X
		dy := math.Abs(pSnap.Y - st.Y)
		dz := pSnap.Z - st.Z
		if math.Hypot(dx, dz) > WardenSonicBoomHorizRange || dy > WardenSonicBoomVertRange {
			return 0
		}

		// Sonic boom bypasses armor and shield, reduced only by Resistance status effect.
		dmg := ScaleDamageByDifficulty(WardenSonicBoomDamage, s.Difficulty())
		if dmg <= 0 {
			return 0
		}
		effective := dmg * player.ResistanceDamageMultiplier("sonic_boom")
		s.DamagePlayerWithEffects(player, dmg, "sonic_boom")
		s.RecordDamageExhaustion(player)
		s.SyncHealthObjectives(player)

		s.EmitVibration(VibrationEvent{
			Dimension:      pSnap.Dimension,
			Type:           GameEventEntityDamage,
			X:              pSnap.X,
			Y:              pSnap.Y,
			Z:              pSnap.Z,
			Player:         player,
			SourceEntityID: pSnap.EntityID,
		})

		if player.IsDead() {
			s.IncrementCriteriaObjectives(player.Profile.Name, "deathCount", 1)
			s.TriggerKilledByEntity(player, MobTypeWarden)
			s.BroadcastDeathMessage(player, "sonic_boom", MobTypeWarden)
		} else {
			horizDist := math.Hypot(dx, dz)
			var vx, vz float64
			if horizDist > 1e-4 {
				vx = (dx / horizDist) * WardenSonicBoomHorizKB
				vz = (dz / horizDist) * WardenSonicBoomHorizKB
			}
			vy := WardenSonicBoomVertKB
			if player.send != nil {
				_ = player.send(protocol.PlaySetEntityMotion, EncodeSetEntityMotionPacket(player.EntityID, vx, vy, vz))
			}
		}
		return effective
	}

	if s.entities != nil {
		if targetEnt := s.entities.Get(targetEntityID); targetEnt != nil {
			dx := targetEnt.X - st.X
			dy := math.Abs(targetEnt.Y - st.Y)
			dz := targetEnt.Z - st.Z
			if math.Hypot(dx, dz) > WardenSonicBoomHorizRange || dy > WardenSonicBoomVertRange {
				return 0
			}
			s.applyDamageToMob(targetEnt, WardenSonicBoomDamage, nil, WardenSonicBoomHorizKB, false, true)
			return WardenSonicBoomDamage
		}
	}
	return 0
}

// PulseWardenDarkness applies minecraft:darkness (260 ticks = 13s) to all
// survival/adventure players within 20 blocks of (wx, wy, wz) in dim.
func (s *Server) PulseWardenDarkness(dim string, wx, wy, wz float64) int {
	if s == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	rSq := WardenDarknessPulseRadius * WardenDarknessPulseRadius
	affected := 0

	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		snap := p.Snapshot()
		if world.NormalizeDimension(snap.Dimension) != normDim {
			continue
		}
		if snap.GameMode != GameModeSurvival && snap.GameMode != GameModeAdventure {
			continue
		}
		dx := snap.X - wx
		dy := snap.Y - wy
		dz := snap.Z - wz
		if dx*dx+dy*dy+dz*dz > rSq {
			continue
		}
		s.ApplyPlayerEffect(p, StatusEffectInstance{
			ID:            EffectDarkness,
			Amplifier:     0,
			Duration:      WardenDarknessEffectDurationTicks,
			Ambient:       false,
			ShowParticles: false,
			ShowIcon:      true,
		})
		affected++
	}
	return affected
}

// DamageWarden applies damage to an active Warden, enforcing 100% knockback
// resistance, adding +100 anger toward the attacker, interrupting digging if
// provoked, and dropping 1x minecraft:sculk_catalyst + 5 XP on death (unless
// absorbed by a nearby sculk_catalyst).
func (s *Server) DamageWarden(entityID int32, damage float32, attacker *PlayerSession) (dealt float32, killed bool) {
	if s == nil || s.entities == nil || damage <= 0 {
		return 0, false
	}
	mgr := s.wardenManager()
	mgr.mu.Lock()
	st, ok := mgr.wardens[entityID]
	if !ok || st == nil {
		mgr.mu.Unlock()
		return 0, false
	}

	st.Health -= damage
	st.TicksWithoutAnger = 0
	var poseChanged bool
	if st.Phase == WardenPhaseDigging {
		st.Phase = WardenPhaseCalm
		st.Pose = PoseStanding
		st.PhaseTicks = 0
		poseChanged = true
	}
	if st.Health <= 0 {
		st.Health = 0
		killed = true
		delete(mgr.wardens, entityID)
	}
	snap := st.Clone()
	mgr.mu.Unlock()

	tr := s.entityTracker()
	tr.mu.Lock()
	if killed {
		delete(tr.mobStates, entityID)
	} else {
		mem := tr.mobStateFor(entityID, tr.aiTickID)
		mem.healthInit = true
		mem.health = snap.Health
		mem.lastHealth = snap.Health
	}
	tr.mu.Unlock()

	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	var (
		attackerID int32
		attackerX  = snap.X
		attackerY  = snap.Y
		attackerZ  = snap.Z
		hurtYaw    float32
	)
	if attacker != nil {
		aSnap := attacker.Snapshot()
		attackerID = aSnap.EntityID
		attackerX = aSnap.X
		attackerY = aSnap.Y
		attackerZ = aSnap.Z
		hurtYaw = yawToward(attackerX-snap.X, attackerZ-snap.Z)
	}

	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, protocol.PlayHurtAnimation,
		EncodeHurtAnimation(entityID, hurtYaw))
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, protocol.PlayDamageEvent,
		EncodeDamageEventPacket(entityID, attackerID))

	if !killed {
		s.OnEntityDamagedWithEffects(entityID, false)
		s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundWardenHurt, SoundSourceHostile, snap.X, snap.Y, snap.Z, 2.0, 1.0, 0))
		if poseChanged {
			s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySetEntityDataCB,
				EncodeWardenEntityDataPacket(entityID, snap.Pose, snap.ClientAngerLevel))
		}
		if attacker != nil && attackerID != 0 {
			s.AddWardenAnger(entityID, attackerID, attacker, attackerX, attackerY, attackerZ, WardenHurtAngerBoost, true)
			s.NotifyOwnerAttackedTarget(attacker, entityID)
		}
		return damage, false
	}

	// Warden died: broadcast death sound + event, trigger sculk catalyst / XP orbs, and drop 1x sculk_catalyst.
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundWardenDeath, SoundSourceHostile, snap.X, snap.Y, snap.Z, 3.0, 1.0, 0))
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(entityID, EntityEventDeath))
	s.OnEntityDiedWithEffects(entityID)
	s.entities.Remove(entityID)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{entityID}))

	consumed := s.TriggerSculkCatalystOnMobDeath(snap.Dimension, snap.X, snap.Y, snap.Z, int(WardenDeathXP), attacker)
	if !consumed {
		s.SpawnExperienceOrbsInDimension(snap.Dimension, snap.X, snap.Y+0.5, snap.Z, WardenDeathXP)
	}

	catalystDrop := world.NewNamedItemStack(SculkCatalystItemID, 1)
	if !catalystDrop.IsEmpty() {
		s.SpawnItemEntityInDimension(snap.Dimension, snap.X, snap.Y+0.25, snap.Z, catalystDrop)
	}

	if attacker != nil {
		s.TriggerEntityKilled(attacker, MobTypeWarden)
		s.IncrementCriteriaObjectives(attacker.Profile.Name, "totalKillCount", 1)
	}
	return damage, true
}

// TickWardens advances all active Wardens and player shield-disable timers by
// one game tick (20 TPS).
func (s *Server) TickWardens(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	s.EnsureWardenSummonHook()

	mgr := s.wardenManager()
	mgr.mu.Lock()
	for sess, rem := range mgr.shieldLocks {
		if rem <= 1 || sess == nil || sess.IsDead() {
			delete(mgr.shieldLocks, sess)
		} else {
			mgr.shieldLocks[sess] = rem - 1
		}
	}
	ids := make([]int32, 0, len(mgr.wardens))
	for id := range mgr.wardens {
		ids = append(ids, id)
	}
	mgr.mu.Unlock()

	if s.Difficulty() == DifficultyPeaceful {
		for _, id := range ids {
			s.despawnWarden(id)
		}
		return
	}

	for _, id := range ids {
		s.tickSingleWarden(id, rng)
	}
}

func (s *Server) despawnWarden(entityID int32) {
	mgr := s.wardenManager()
	mgr.mu.Lock()
	st, ok := mgr.wardens[entityID]
	delete(mgr.wardens, entityID)
	mgr.mu.Unlock()
	if s.entities != nil {
		s.entities.Remove(entityID)
	}
	if ok && st != nil {
		cx := int32(int64(math.Floor(st.X)) >> 4)
		cz := int32(int64(math.Floor(st.Z)) >> 4)
		s.BroadcastDimensionChunk(st.Dimension, cx, cz, protocol.PlayRemoveEntities,
			encodeRemoveEntitiesPacket(nil, []int32{entityID}))
	}
}

func (s *Server) tickSingleWarden(entityID int32, rng *rand.Rand) {
	ent := s.entities.Get(entityID)
	mgr := s.wardenManager()
	mgr.mu.Lock()
	st, ok := mgr.wardens[entityID]
	if !ok || st == nil || ent == nil {
		delete(mgr.wardens, entityID)
		mgr.mu.Unlock()
		return
	}

	st.X = ent.X
	st.Y = ent.Y
	st.Z = ent.Z
	st.Yaw = ent.Yaw
	st.TickCounter++

	if st.MeleeCooldown > 0 {
		st.MeleeCooldown--
	}
	if st.SniffCooldown > 0 {
		st.SniffCooldown--
	}
	if st.VibrationCooldown > 0 {
		st.VibrationCooldown--
	}
	if st.SonicBoomCooldown > 0 {
		st.SonicBoomCooldown--
	}

	// 1. Decay anger by 1 per second (every 20 ticks) for all suspects and prune dead/invalid suspects.
	prevClientAnger := st.ClientAngerLevel
	if st.TickCounter%WardenAngerDecayIntervalTicks == 0 && len(st.Suspects) > 0 {
		for id, entry := range st.Suspects {
			if entry == nil {
				delete(st.Suspects, id)
				continue
			}
			entry.Anger--
			if entry.Anger <= 0 {
				delete(st.Suspects, id)
			}
		}
	}
	s.pruneInvalidWardenSuspectsLocked(st)
	recomputeWardenTargetLocked(st)
	angerSynced := st.ClientAngerLevel != prevClientAnger

	// 2. Track calm timeout (1200 ticks = 60s without any suspects or disturbances -> Digging).
	if len(st.Suspects) == 0 && st.ActiveTargetID == 0 {
		st.TicksWithoutAnger++
	} else {
		st.TicksWithoutAnger = 0
	}

	// 3. Darkness pulse every 120 ticks (6.0s) and periodic heartbeat sound.
	st.DarknessPulseTicker++
	pulseDarknessNow := false
	if st.DarknessPulseTicker >= WardenDarknessPulseIntervalTicks &&
		st.Phase != WardenPhaseEmerging && st.Phase != WardenPhaseDigging {
		st.DarknessPulseTicker = 0
		pulseDarknessNow = true
	}

	st.HeartbeatTicker++
	playHeartbeatNow := false
	hbInterval := WardenHeartbeatIntervalTicks(st.ClientAngerLevel)
	if st.HeartbeatTicker >= hbInterval && st.Phase != WardenPhaseEmerging && st.Phase != WardenPhaseDigging {
		st.HeartbeatTicker = 0
		playHeartbeatNow = true
	}

	cx := int32(int64(math.Floor(st.X)) >> 4)
	cz := int32(int64(math.Floor(st.Z)) >> 4)

	// 4. Phase state machine.
	switch st.Phase {
	case WardenPhaseEmerging:
		st.PhaseTicks--
		if st.PhaseTicks <= 0 {
			st.Phase = WardenPhaseCalm
			st.Pose = PoseStanding
			st.PhaseTicks = 0
			snap := st.Clone()
			mgr.mu.Unlock()
			s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySetEntityDataCB,
				EncodeWardenEntityDataPacket(entityID, snap.Pose, snap.ClientAngerLevel))
			return
		}
		mgr.mu.Unlock()
		return

	case WardenPhaseDigging:
		st.PhaseTicks--
		if st.PhaseTicks <= 0 {
			mgr.mu.Unlock()
			s.despawnWarden(entityID)
			return
		}
		mgr.mu.Unlock()
		return

	case WardenPhaseRoaring:
		st.PhaseTicks--
		if st.PhaseTicks <= 0 {
			if st.ActiveTargetID != 0 {
				st.Phase = WardenPhaseChasing
			} else {
				st.Phase = WardenPhaseCalm
			}
			st.Pose = PoseStanding
			st.PhaseTicks = 0
			snap := st.Clone()
			mgr.mu.Unlock()
			s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySetEntityDataCB,
				EncodeWardenEntityDataPacket(entityID, snap.Pose, snap.ClientAngerLevel))
			return
		}
		mgr.mu.Unlock()
		return

	case WardenPhaseSniffing:
		st.PhaseTicks--
		if st.PhaseTicks <= 0 {
			st.Phase = WardenPhaseCalm
			st.Pose = PoseStanding
			st.PhaseTicks = 0
			st.SniffCooldown = WardenSniffIntervalTicks
			snap := st.Clone()
			mgr.mu.Unlock()

			s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySetEntityDataCB,
				EncodeWardenEntityDataPacket(entityID, snap.Pose, snap.ClientAngerLevel))
			smelledID, _ := s.PerformWardenSniff(entityID)
			if smelledID != 0 {
				mgr.mu.Lock()
				if cur := mgr.wardens[entityID]; cur != nil && cur.Phase == WardenPhaseCalm {
					if cur.ActiveTargetID != 0 {
						cur.Phase = WardenPhaseChasing
					} else if cur.HasDisturbancePos {
						cur.Phase = WardenPhaseInvestigating
					}
				}
				mgr.mu.Unlock()
			}
			return
		}
		mgr.mu.Unlock()
		return

	case WardenPhaseSonicBoom:
		st.SonicBoomTicks++
		fireNow := st.SonicBoomTicks == WardenSonicBoomChargeTicks
		doneNow := st.SonicBoomTicks >= WardenSonicBoomTotalTicks
		targetID := st.SonicBoomTargetID
		if doneNow {
			if st.ActiveTargetID != 0 {
				st.Phase = WardenPhaseChasing
			} else {
				st.Phase = WardenPhaseCalm
			}
			st.Pose = PoseStanding
			st.SonicBoomTicks = 0
			st.SonicBoomTargetID = 0
			st.SonicBoomCooldown = WardenSonicBoomCooldownTicks
		}
		snap := st.Clone()
		mgr.mu.Unlock()

		if fireNow && targetID != 0 {
			s.FireWardenSonicBoom(entityID, targetID)
		}
		if doneNow {
			s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySetEntityDataCB,
				EncodeWardenEntityDataPacket(entityID, snap.Pose, snap.ClientAngerLevel))
		}
		return
	}

	// Check if calm timeout (1200 ticks = 60s) reached -> start Digging!
	if st.TicksWithoutAnger >= WardenCalmDigTimeoutTicks {
		st.Phase = WardenPhaseDigging
		st.Pose = PoseDigging
		st.PhaseTicks = WardenDiggingDurationTicks
		snap := st.Clone()
		mgr.mu.Unlock()

		s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySetEntityDataCB,
			EncodeWardenEntityDataPacket(entityID, snap.Pose, snap.ClientAngerLevel))
		s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundWardenDig, SoundSourceHostile, snap.X, snap.Y, snap.Z, 5.0, 1.0, 0))
		return
	}

	// Promote to Chasing whenever an Angry (>= 80) target is active.
	if st.ActiveTargetID != 0 {
		st.Phase = WardenPhaseChasing
	} else if st.Phase == WardenPhaseChasing {
		if st.HasDisturbancePos {
			st.Phase = WardenPhaseInvestigating
		} else {
			st.Phase = WardenPhaseCalm
		}
	}

	// If Calm/Investigating without an active target and SniffCooldown == 0, start Sniffing!
	if st.ActiveTargetID == 0 && !st.HasDisturbancePos && st.SniffCooldown <= 0 {
		st.Phase = WardenPhaseSniffing
		st.Pose = PoseSniffing
		st.PhaseTicks = WardenSniffDurationTicks
		snap := st.Clone()
		mgr.mu.Unlock()

		s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySetEntityDataCB,
			EncodeWardenEntityDataPacket(entityID, snap.Pose, snap.ClientAngerLevel))
		s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundWardenSniff, SoundSourceHostile, snap.X, snap.Y, snap.Z, 3.0, 1.0, 0))
		return
	}

	snap := st.Clone()
	mgr.mu.Unlock()

	if pulseDarknessNow {
		s.PulseWardenDarkness(snap.Dimension, snap.X, snap.Y, snap.Z)
	}
	if playHeartbeatNow {
		s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundWardenHeartbeat, SoundSourceHostile, snap.X, snap.Y, snap.Z, 2.0, 1.0, 0))
	}
	if angerSynced {
		s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySetEntityDataCB,
			EncodeWardenEntityDataPacket(entityID, snap.Pose, snap.ClientAngerLevel))
	}

	// 5. Check physical contact / collision with nearby entities (+35 touch anger).
	s.checkWardenTouchCollisions(snap)

	// 6. Execute movement & combat for Chasing / Investigating / Calm.
	s.stepWardenMovementAndCombat(entityID, snap, rng)
}

func (s *Server) pruneInvalidWardenSuspectsLocked(st *WardenState) {
	if st == nil || len(st.Suspects) == 0 {
		return
	}
	for id, entry := range st.Suspects {
		if entry == nil || entry.Anger <= 0 {
			delete(st.Suspects, id)
			continue
		}
		if entry.IsPlayer {
			p := s.playerByEntityID(id)
			if p == nil || p.IsDead() {
				delete(st.Suspects, id)
				continue
			}
			pSnap := p.Snapshot()
			if world.NormalizeDimension(pSnap.Dimension) != st.Dimension ||
				pSnap.GameMode == GameModeCreative || pSnap.GameMode == GameModeSpectator {
				delete(st.Suspects, id)
				continue
			}
			entry.LastKnownX = pSnap.X
			entry.LastKnownY = pSnap.Y
			entry.LastKnownZ = pSnap.Z
		} else if s.entities != nil {
			ent := s.entities.Get(id)
			if ent == nil || ent.EffectiveDimension() != st.Dimension || s.MobHealth(id) <= 0 {
				delete(st.Suspects, id)
				continue
			}
			entry.LastKnownX = ent.X
			entry.LastKnownY = ent.Y
			entry.LastKnownZ = ent.Z
		}
	}
}

func (s *Server) checkWardenTouchCollisions(st WardenState) {
	rSq := WardenTouchCollisionRadius * WardenTouchCollisionRadius
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		pSnap := p.Snapshot()
		if world.NormalizeDimension(pSnap.Dimension) != st.Dimension ||
			pSnap.GameMode == GameModeCreative || pSnap.GameMode == GameModeSpectator {
			continue
		}
		dx := pSnap.X - st.X
		dy := math.Abs(pSnap.Y - st.Y)
		dz := pSnap.Z - st.Z
		if dx*dx+dz*dz <= rSq && dy <= 2.5 {
			if s.WardenSuspectAnger(st.EntityID, pSnap.EntityID) < WardenAngryThreshold {
				s.AddWardenAnger(st.EntityID, pSnap.EntityID, p, pSnap.X, pSnap.Y, pSnap.Z, WardenTouchAngerBoost, true)
			}
		}
	}
}

func (s *Server) stepWardenMovementAndCombat(entityID int32, st WardenState, rng *rand.Rand) {
	cache := s.DimensionChunks(st.Dimension)
	feetY := int(math.Floor(st.Y + 0.01))
	nextX, nextY, nextZ := st.X, st.Y, st.Z
	nextYaw := st.Yaw

	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(entityID, tr.aiTickID)
	if mem.repathCooldown > 0 {
		mem.repathCooldown--
	}

	var (
		hasGoal               bool
		goalX, goalY, goalZ   float64
		moveSpeed             = WardenCalmWalkSpeed
		stopDist              = 0.5
		attackTargetID        int32
		triggerSonicBoom      bool
		sonicTargetID         int32
		clearedDisturbancePos bool
	)

	if st.ActiveTargetID != 0 {
		if entry := st.Suspects[st.ActiveTargetID]; entry != nil {
			hasGoal = true
			goalX, goalY, goalZ = entry.LastKnownX, entry.LastKnownY, entry.LastKnownZ
			moveSpeed = WardenChaseSpeed
			stopDist = 1.4
		}
	} else if st.HasDisturbancePos {
		hasGoal = true
		goalX, goalY, goalZ = st.DisturbanceX, st.DisturbanceY, st.DisturbanceZ
		moveSpeed = WardenCalmWalkSpeed
		stopDist = 0.8
	}

	var moved bool
	if hasGoal {
		goalFeetY := int(math.Floor(goalY + 0.01))
		nextX, nextY, nextZ, nextYaw, moved = moveMobToward(
			cache, mem, st.X, feetY, st.Z,
			goalX, goalFeetY, goalZ,
			moveSpeed, stopDist,
		)
		horizDist := math.Hypot(goalX-nextX, goalZ-nextZ)
		vertDist := math.Abs(goalY - nextY)

		if st.ActiveTargetID != 0 {
			if horizDist <= WardenMeleeReach && vertDist <= WardenMeleeVerticalReach && st.MeleeCooldown <= 0 {
				attackTargetID = st.ActiveTargetID
			} else if st.SonicBoomCooldown <= 0 &&
				horizDist <= WardenSonicBoomHorizRange && vertDist <= WardenSonicBoomVertRange &&
				(horizDist > WardenSonicBoomMinDist || vertDist > WardenMeleeVerticalReach || !moved) {
				triggerSonicBoom = true
				sonicTargetID = st.ActiveTargetID
			}
		} else if st.HasDisturbancePos && horizDist <= 1.2 {
			clearedDisturbancePos = true
		}
	} else {
		nextX, nextY, nextZ, nextYaw = stepWanderAI(cache, rng, mem, st.X, feetY, st.Z, st.Yaw, WardenCalmWalkSpeed*0.5)
	}
	tr.mu.Unlock()

	mgr := s.wardenManager()
	mgr.mu.Lock()
	cur := mgr.wardens[entityID]
	if cur == nil {
		mgr.mu.Unlock()
		return
	}
	cur.X, cur.Y, cur.Z, cur.Yaw = nextX, nextY, nextZ, nextYaw
	if clearedDisturbancePos {
		cur.HasDisturbancePos = false
		if cur.ActiveTargetID == 0 {
			cur.Phase = WardenPhaseCalm
		}
	}
	if attackTargetID != 0 {
		cur.MeleeCooldown = WardenMeleeCooldownTicks
	}
	var startedBoom bool
	if triggerSonicBoom && sonicTargetID != 0 && cur.Phase == WardenPhaseChasing {
		cur.Phase = WardenPhaseSonicBoom
		cur.Pose = PoseShooting
		cur.SonicBoomTicks = 0
		cur.SonicBoomTargetID = sonicTargetID
		startedBoom = true
	}
	curPose := cur.Pose
	curAnger := cur.ClientAngerLevel
	mgr.mu.Unlock()

	s.entities.Update(entityID, func(e *world.Entity) {
		e.X = nextX
		e.Y = nextY
		e.Z = nextZ
		e.Yaw = nextYaw
		e.HeadYaw = nextYaw
	})

	cx := int32(int64(math.Floor(nextX)) >> 4)
	cz := int32(int64(math.Floor(nextZ)) >> 4)
	if startedBoom {
		s.BroadcastDimensionChunk(st.Dimension, cx, cz, protocol.PlayEntityEvent,
			EncodeEntityEventPacket(entityID, EntityEventWardenSonicBoom))
		s.BroadcastDimensionChunk(st.Dimension, cx, cz, PlaySetEntityDataCB,
			EncodeWardenEntityDataPacket(entityID, curPose, curAnger))
		s.BroadcastDimensionChunk(st.Dimension, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundWardenSonicCharge, SoundSourceHostile, nextX, nextY+1.6, nextZ, 3.0, 1.0, 0))
	}
	if attackTargetID != 0 {
		s.AttackFromWarden(entityID, attackTargetID)
	}
}

// MobXPReward returns the vanilla 26.1.2 experience orb reward dropped when a
// mob of typeName dies (0 for baby mobs).
func MobXPReward(typeName string, isBaby bool) int {
	if isBaby {
		if canonicalResourceName(typeName) == MobTypeZombie {
			return 12
		}
		return 0
	}
	switch canonicalResourceName(typeName) {
	case MobTypeWither:
		return int(WitherDeathXP)
	case MobTypeRavager:
		return 20
	case MobTypeEvoker:
		return 10
	case MobTypeZombie, MobTypeSkeleton, MobTypeCreeper, MobTypeSpider,
		MobTypePhantom, MobTypePillager, MobTypeVindicator, MobTypeWitch,
		MobTypeWarden, MobTypeWitherSkeleton:
		return 5
	case MobTypePig, MobTypeCow, MobTypeSheep, MobTypeChicken,
		MobTypeWolf, MobTypeCat, MobTypeParrot,
		MobTypeHorse, MobTypeDonkey, MobTypeMule, MobTypeLlama, MobTypeTraderLlama:
		return 2
	default:
		return 0
	}
}
