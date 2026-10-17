package server

import (
	"errors"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Ender Dragon, End Crystal, Dragon Fireball, and Area Effect Cloud constants
// for Minecraft 26.1.2 (protocol 775).
const (
	MobTypeEnderDragon        = "minecraft:ender_dragon"
	EntityTypeEndCrystal      = "minecraft:end_crystal"
	EntityTypeDragonFireball  = "minecraft:dragon_fireball"
	EntityTypeAreaEffectCloud = "minecraft:area_effect_cloud"

	// Vanilla 26.1.2 BuiltInRegistries.ENTITY_TYPE numeric network IDs (protocol 775).
	EntityTypeAreaEffectCloudID int = 3
	EntityTypeDragonFireballID  int = 37
	EntityTypeEnderDragonID     int = 43
	EntityTypeEndCrystalID      int = 45

	// Canonical item and block IDs for the End Dragon fight.
	EndCrystalItemID   = "minecraft:end_crystal"
	DragonEggBlockID   = "minecraft:dragon_egg"
	EndPortalBlockID   = "minecraft:end_portal"
	EndGatewayBlockID  = "minecraft:end_gateway"
	DragonBreathItemID = "minecraft:dragon_breath"

	// Ender Dragon health, healing, and combat parameters (vanilla EnderDragon / EndDragonFight).
	DefaultEnderDragonMaxHealth          float32 = 200.0
	EnderDragonCrystalRange              float64 = 32.0
	EnderDragonCrystalHealInterval       int     = 10
	EnderDragonCrystalHealAmount         float32 = 1.0
	EnderDragonCrystalDestructionDamage  float32 = 10.0
	EndCrystalExplosionRadius            float64 = 6.0
	EnderDragonBossBarRange              float64 = 192.0
	EnderDragonFlySpeed                  float64 = 0.65
	EnderDragonChargeSpeed               float64 = 0.95
	EnderDragonOrbitRadius               float64 = 40.0
	EnderDragonMeleeDamage               float32 = 10.0
	EnderDragonWingDamage                float32 = 5.0
	EnderDragonBreathDamage              float32 = 6.0
	DragonFireballSpeed                  float64 = 0.90
	DragonBreathCloudRadius              float64 = 3.0
	DragonBreathCloudDurationTicks       int     = 200
	EnderDragonSittingTicks              int     = 100
	EnderDragonDyingDurationTicks        int     = 200
	EnderDragonRespawnDurationTicks      int     = 100
	EnderDragonFirstKillXP               int32   = 12000
	EnderDragonSubsequentKillXP          int32   = 500
	EnderDragonMaxGateways               int     = 20
	EnderDragonGatewayRadius             float64 = 96.0
	EnderDragonGatewayY                  int     = 75
	EnderDragonDefaultPortalY            int     = 64

	// SynchedEntityData indices for EnderDragon and EndCrystal in 26.1.2 (protocol 775).
	EntityDataEnderDragonPhaseID     byte  = 16 // EnderDragon.DATA_PHASE (INT serializer = 1)
	EntityDataEndCrystalBeamTargetID byte  = 8  // EndCrystal.DATA_BEAM_TARGET (OPTIONAL_BLOCK_POS serializer = 15)
	EntityDataEndCrystalShowBottomID byte  = 9  // EndCrystal.DATA_SHOW_BOTTOM (BOOLEAN serializer = 8)
	EntityDataSerializerOptBlockPos  int32 = 15 // EntityDataSerializers.OPTIONAL_BLOCK_POS
)

// EnderDragonPhase represents the 11 phase IDs in EnderDragonPhase (0..10).
type EnderDragonPhase int32

const (
	DragonPhaseHoldingPattern   EnderDragonPhase = 0  // Circles around the obsidian pillars
	DragonPhaseStrafePlayer     EnderDragonPhase = 1  // Flies toward player and shoots a dragon_fireball
	DragonPhaseLandingApproach  EnderDragonPhase = 2  // Approaches the exit portal center
	DragonPhaseLanding          EnderDragonPhase = 3  // Descends onto the exit portal podium
	DragonPhaseTakeoff          EnderDragonPhase = 4  // Leaves the exit portal back to HoldingPattern
	DragonPhaseSittingFlaming   EnderDragonPhase = 5  // Perched on portal breathing dragon breath (arrow immune)
	DragonPhaseSittingScanning  EnderDragonPhase = 6  // Perched on portal scanning for players (arrow immune)
	DragonPhaseSittingAttacking EnderDragonPhase = 7  // Perched on portal roaring/wing-knockback (arrow immune)
	DragonPhaseChargingPlayer   EnderDragonPhase = 8  // Dives at target player
	DragonPhaseDying            EnderDragonPhase = 9  // Flies to portal and plays 200-tick death sequence
	DragonPhaseHovering         EnderDragonPhase = 10 // Hovering in place
)

// IsSitting reports whether the phase is one of the perched sitting phases
// (SittingFlaming, SittingScanning, SittingAttacking), during which arrows and
// projectiles bounce off the Ender Dragon and deal 0 damage.
func (p EnderDragonPhase) IsSitting() bool {
	return p == DragonPhaseSittingFlaming || p == DragonPhaseSittingScanning || p == DragonPhaseSittingAttacking
}

// String returns the canonical phase name.
func (p EnderDragonPhase) String() string {
	switch p {
	case DragonPhaseHoldingPattern:
		return "HoldingPattern"
	case DragonPhaseStrafePlayer:
		return "StrafePlayer"
	case DragonPhaseLandingApproach:
		return "LandingApproach"
	case DragonPhaseLanding:
		return "Landing"
	case DragonPhaseTakeoff:
		return "Takeoff"
	case DragonPhaseSittingFlaming:
		return "SittingFlaming"
	case DragonPhaseSittingScanning:
		return "SittingScanning"
	case DragonPhaseSittingAttacking:
		return "SittingAttacking"
	case DragonPhaseChargingPlayer:
		return "ChargingPlayer"
	case DragonPhaseDying:
		return "Dying"
	case DragonPhaseHovering:
		return "Hovering"
	default:
		return "HoldingPattern"
	}
}

// EndCrystalState holds the server state of a spawned minecraft:end_crystal.
type EndCrystalState struct {
	EntityID      int32
	Dimension     string
	X, Y, Z       float64
	ShowsBottom   bool
	HasBeamTarget bool
	BeamX         int
	BeamY         int
	BeamZ         int
	SpikeIndex    int // 0..9 if on an End obsidian pillar, -1 if player-placed
}

// DragonFireballProjectile holds the state of an in-flight minecraft:dragon_fireball.
type DragonFireballProjectile struct {
	EntityID      int32
	OwnerEntityID int32
	Dimension     string
	X, Y, Z       float64
	VX, VY, VZ    float64
	AgeTicks      int
}

// DragonBreathCloud holds the state of an active Dragon's Breath area_effect_cloud.
type DragonBreathCloud struct {
	EntityID       int32
	OwnerEntityID  int32
	Dimension      string
	X, Y, Z        float64
	Radius         float64
	RemainingTicks int
}

// EnderDragonFightState holds the state of the Ender Dragon and the EndDragonFight
// manager in minecraft:the_end.
type EnderDragonFightState struct {
	Initialized          bool
	DragonEntityID       int32
	DragonAlive          bool
	DragonKilled         bool
	PreviouslyKilled     bool
	PortalY              int
	Health               float32
	MaxHealth            float32
	Phase                EnderDragonPhase
	PhaseTicks           int
	DeathTicks           int
	OrbitAngle           float64
	OrbitClockwise       bool
	TargetX, TargetY, TargetZ float64
	TargetPlayerID       int32
	NearestCrystalID     int32
	CrystalsAlive        int
	SittingDamageTaken   float32
	TickCounter          int
	BossBarUUID          [16]byte
	GatewaysSpawned      int
	Respawning           bool
	RespawnTicks         int
	RespawnCrystalIDs    []int32
}

type serverDragonManager struct {
	mu        sync.RWMutex
	fight     EnderDragonFightState
	crystals  map[int32]*EndCrystalState
	fireballs map[int32]*DragonFireballProjectile
	clouds    map[int32]*DragonBreathCloud
}

var serverDragons sync.Map // map[*Server]*serverDragonManager

func (s *Server) dragonManager() *serverDragonManager {
	if v, ok := serverDragons.Load(s); ok {
		return v.(*serverDragonManager)
	}
	mgr := &serverDragonManager{
		fight: EnderDragonFightState{
			PortalY:   EnderDragonDefaultPortalY,
			MaxHealth: DefaultEnderDragonMaxHealth,
		},
		crystals:  make(map[int32]*EndCrystalState),
		fireballs: make(map[int32]*DragonFireballProjectile),
		clouds:    make(map[int32]*DragonBreathCloud),
	}
	actual, _ := serverDragons.LoadOrStore(s, mgr)
	return actual.(*serverDragonManager)
}

// ResolveEndPortalY determines the surface Y coordinate at (0, 0) in
// minecraft:the_end for placing the Exit Portal podium (clamped to [55, 75]).
func (s *Server) ResolveEndPortalY() int {
	if s == nil {
		return EnderDragonDefaultPortalY
	}
	cache := s.DimensionChunks(world.DimensionEnd)
	if cache == nil {
		return EnderDragonDefaultPortalY
	}
	for y := 100; y >= 40; y-- {
		st := cache.GetBlock(0, y, 0)
		if st != world.StateAir && !IsWitherSkullBlockState(st) {
			name := world.BlockName(st)
			if name != "minecraft:end_portal" && name != "minecraft:dragon_egg" && name != "minecraft:wall_torch" {
				if y < 55 {
					return 55
				}
				if y > 75 {
					return 75
				}
				return y
			}
		}
	}
	return EnderDragonDefaultPortalY
}

// PlaceEndPodium ports EndPodiumFeature.place (vanilla 26.1.2) centered at
// (0, originY, 0) in cache:
//   - Outer bedrock rim (2.5 < dist <= 3.5 at y = originY)
//   - Inner bedrock floor (dist <= 2.5 at y = originY - 1)
//   - Inner portal pool (dist <= 2.5 at y = originY: minecraft:end_portal when
//     active=true, or minecraft:air when active=false)
//   - Central bedrock pillar (0, originY..originY+3, 0)
//   - 4 outward-facing wall torches at (±1, originY+2, 0) and (0, originY+2, ±1)
//   - Dragon Egg at (0, originY+4, 0) when placeDragonEgg=true
func PlaceEndPodium(cache *world.Cache, originY int, active bool, placeDragonEgg bool) []world.BlockChange {
	if cache == nil {
		return nil
	}
	endStone, ok := world.BlockStateByName("minecraft:end_stone", nil)
	if !ok {
		endStone = world.StateStone
	}
	portalState := world.StateAir
	if active {
		if st, ok := world.BlockStateByName(EndPortalBlockID, nil); ok {
			portalState = st
		}
	}

	var changes []world.BlockChange
	setB := func(x, y, z int, st uint16) {
		cache.SetBlock(x, y, z, st)
		changes = append(changes, world.BlockChange{
			Pos:      world.BlockPos{X: x, Y: y, Z: z},
			NewState: st,
		})
	}

	// Inner radius = 2.5 (sq = 6.25), outer radius = 3.5 (sq = 12.25).
	for dx := -4; dx <= 4; dx++ {
		for dz := -4; dz <= 4; dz++ {
			distSq := float64(dx*dx + dz*dz)
			if distSq > 12.25 {
				continue
			}
			for dy := -1; dy <= 3; dy++ {
				by := originY + dy
				if distSq <= 6.25 {
					if dy < 0 {
						setB(dx, by, dz, world.StateBedrock)
					} else if dy > 0 {
						setB(dx, by, dz, world.StateAir)
					} else {
						setB(dx, by, dz, portalState)
					}
				} else if dy == 0 {
					setB(dx, by, dz, world.StateBedrock)
				} else if dy < 0 {
					setB(dx, by, dz, endStone)
				} else {
					setB(dx, by, dz, world.StateAir)
				}
			}
		}
	}

	// Central 4-block bedrock column at (0, originY..originY+3, 0).
	for dy := 0; dy < 4; dy++ {
		setB(0, originY+dy, 0, world.StateBedrock)
	}

	// 4 wall torches around (0, originY+2, 0).
	torchDirs := []struct {
		dx, dz int
		facing string
	}{
		{-1, 0, "west"},
		{1, 0, "east"},
		{0, -1, "north"},
		{0, 1, "south"},
	}
	for _, td := range torchDirs {
		if st, ok := world.BlockStateByName("minecraft:wall_torch", map[string]string{"facing": td.facing}); ok {
			setB(td.dx, originY+2, td.dz, st)
		}
	}

	if placeDragonEgg {
		if eggState, ok := world.BlockStateByName(DragonEggBlockID, nil); ok {
			setB(0, originY+4, 0, eggState)
		}
	}

	return changes
}

// PlaceEndGatewayStructure places a 3x5x3 End Gateway structure centered at
// (ox, oy, oz) in cache (matching EndGatewayFeature.place in vanilla 26.1.2).
func PlaceEndGatewayStructure(cache *world.Cache, ox, oy, oz int) []world.BlockChange {
	if cache == nil {
		return nil
	}
	gatewayState, ok := world.BlockStateByName(EndGatewayBlockID, nil)
	if !ok {
		gatewayState = world.StateBedrock
	}
	var changes []world.BlockChange
	setB := func(x, y, z int, st uint16) {
		cache.SetBlock(x, y, z, st)
		changes = append(changes, world.BlockChange{
			Pos:      world.BlockPos{X: x, Y: y, Z: z},
			NewState: st,
		})
	}
	for z := oz - 1; z <= oz+1; z++ {
		for y := oy - 2; y <= oy+2; y++ {
			for x := ox - 1; x <= ox+1; x++ {
				sameX := x == ox
				sameY := y == oy
				sameZ := z == oz
				capY := y-oy == 2 || oy-y == 2
				switch {
				case sameX && sameY && sameZ:
					setB(x, y, z, gatewayState)
				case sameY:
					setB(x, y, z, world.StateAir)
				case capY && sameX && sameZ:
					setB(x, y, z, world.StateBedrock)
				case (!sameX && !sameZ) || capY:
					setB(x, y, z, world.StateAir)
				default:
					setB(x, y, z, world.StateBedrock)
				}
			}
		}
	}
	return changes
}

// StartEndDragonFight initializes the EndDragonFight in minecraft:the_end if not
// already initialized: places the inactive Exit Portal podium at (0, portalY, 0),
// spawns the 10 End Crystals on the obsidian pillars from world.EndSpikesForSeed,
// and spawns the Ender Dragon with its pink boss bar.
func (s *Server) StartEndDragonFight() (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	mgr := s.dragonManager()
	mgr.mu.Lock()
	if mgr.fight.DragonAlive && mgr.fight.DragonEntityID > 0 {
		id := mgr.fight.DragonEntityID
		mgr.mu.Unlock()
		return id, nil
	}
	if mgr.fight.PortalY <= 0 {
		mgr.fight.PortalY = s.ResolveEndPortalY()
	}
	portalY := mgr.fight.PortalY
	mgr.fight.Initialized = true
	mgr.mu.Unlock()

	// Place inactive exit portal podium at (0, portalY, 0).
	if cache := s.DimensionChunks(world.DimensionEnd); cache != nil {
		changes := PlaceEndPodium(cache, portalY, false, false)
		s.broadcastDimensionBlockChanges(world.DimensionEnd, cache, changes, nil)
	}

	// Spawn 10 End Crystals on top of the obsidian pillars from world.EndSpikesForSeed.
	s.SpawnEndSpikeCrystals()

	// Spawn the Ender Dragon above the exit portal.
	return s.SpawnEnderDragon(0.5, float64(portalY)+24.0, 0.5)
}

// SpawnEndSpikeCrystals spawns (or respawns) the 10 End Crystals on top of the
// obsidian pillars returned by world.EndSpikesForSeed(s.cfg.WorldSeed) in
// minecraft:the_end.
func (s *Server) SpawnEndSpikeCrystals() []int32 {
	if s == nil || s.entities == nil {
		return nil
	}
	spikes := world.EndSpikesForSeed(s.cfg.WorldSeed)
	cache := s.DimensionChunks(world.DimensionEnd)
	fireState, hasFire := world.BlockStateByName("minecraft:fire", nil)

	// Remove any existing spike crystals before spawning fresh ones.
	mgr := s.dragonManager()
	mgr.mu.Lock()
	for id, cr := range mgr.crystals {
		if cr != nil && cr.SpikeIndex >= 0 {
			delete(mgr.crystals, id)
			s.entities.Remove(id)
		}
	}
	mgr.mu.Unlock()

	ids := make([]int32, 0, len(spikes))
	for i, sp := range spikes {
		if cache != nil {
			cache.SetBlock(sp.CenterX, sp.Height, sp.CenterZ, world.StateBedrock)
			if hasFire {
				cache.SetBlock(sp.CenterX, sp.Height+1, sp.CenterZ, fireState)
			}
		}
		x := float64(sp.CenterX) + 0.5
		y := float64(sp.Height + 1)
		z := float64(sp.CenterZ) + 0.5
		id := s.spawnEndCrystalInternal(world.DimensionEnd, x, y, z, true, i)
		if id > 0 {
			ids = append(ids, id)
		}
	}

	mgr.mu.Lock()
	mgr.fight.CrystalsAlive = len(ids)
	mgr.mu.Unlock()
	return ids
}

func (s *Server) spawnEndCrystalInternal(dimension string, x, y, z float64, showsBottom bool, spikeIndex int) int32 {
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(EntityTypeEndCrystal)
	if typeID < 0 {
		typeID = EntityTypeEndCrystalID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  EntityTypeEndCrystal,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})
	cr := &EndCrystalState{
		EntityID:    id,
		Dimension:   normDim,
		X:           x,
		Y:           y,
		Z:           z,
		ShowsBottom: showsBottom,
		SpikeIndex:  spikeIndex,
	}
	mgr := s.dragonManager()
	mgr.mu.Lock()
	mgr.crystals[id] = cr
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeEndCrystalEntityDataPacket(id, false, 0, 0, 0, showsBottom))
	}
	return id
}

// PlaceEndCrystalItem places a player-used minecraft:end_crystal item on top of
// an obsidian or bedrock block at (supportX, supportY, supportZ) in dimension.
// If placed on the 4 cardinal edges of the Exit Portal in minecraft:the_end,
// it checks and initiates Ender Dragon respawning.
func (s *Server) PlaceEndCrystalItem(session *PlayerSession, dimension string, supportX, supportY, supportZ int) (int32, bool) {
	if s == nil || s.entities == nil {
		return 0, false
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	if cache != nil {
		supportName := canonicalResourceName(world.BlockName(cache.GetBlock(supportX, supportY, supportZ)))
		if supportName != "minecraft:obsidian" && supportName != "minecraft:bedrock" {
			return 0, false
		}
		if cache.GetBlock(supportX, supportY+1, supportZ) != world.StateAir ||
			cache.GetBlock(supportX, supportY+2, supportZ) != world.StateAir {
			return 0, false
		}
	}

	cx := float64(supportX) + 0.5
	cy := float64(supportY + 1)
	cz := float64(supportZ) + 0.5

	// Ensure another End Crystal doesn't already occupy this block.
	for _, existing := range s.EndCrystalsInDimension(normDim) {
		if math.Abs(existing.X-cx) < 0.9 && math.Abs(existing.Y-cy) < 1.5 && math.Abs(existing.Z-cz) < 0.9 {
			return 0, false
		}
	}

	id := s.spawnEndCrystalInternal(normDim, cx, cy, cz, false, -1)
	if session != nil {
		s.ConsumeHeldItem(session, 1)
	}
	if normDim == world.DimensionEnd {
		s.TryRespawnEnderDragon()
	}
	return id, true
}

// EndCrystalsInDimension returns snapshots of all active End Crystals in dimension.
func (s *Server) EndCrystalsInDimension(dimension string) []EndCrystalState {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dimension)
	mgr := s.dragonManager()
	mgr.mu.RLock()
	out := make([]EndCrystalState, 0, len(mgr.crystals))
	for _, cr := range mgr.crystals {
		if cr != nil && cr.Dimension == normDim {
			out = append(out, *cr)
		}
	}
	mgr.mu.RUnlock()
	return out
}

// GetEndCrystalState returns the state of an End Crystal by entity ID.
func (s *Server) GetEndCrystalState(entityID int32) (EndCrystalState, bool) {
	if s == nil {
		return EndCrystalState{}, false
	}
	mgr := s.dragonManager()
	mgr.mu.RLock()
	cr, ok := mgr.crystals[entityID]
	var out EndCrystalState
	if ok && cr != nil {
		out = *cr
	}
	mgr.mu.RUnlock()
	return out, ok && cr != nil
}

// DestroyEndCrystal destroys an End Crystal entity (triggered by melee or projectile
// attack), causes a radius-6.0 explosion, and if the Ender Dragon was currently
// healing from this crystal, deals 10.0 backlash damage to the dragon!
func (s *Server) DestroyEndCrystal(crystalID int32, attacker *PlayerSession) bool {
	if s == nil || s.entities == nil {
		return false
	}
	mgr := s.dragonManager()
	mgr.mu.Lock()
	cr, ok := mgr.crystals[crystalID]
	if !ok || cr == nil {
		mgr.mu.Unlock()
		return false
	}
	snap := *cr
	delete(mgr.crystals, crystalID)

	// Recompute remaining spike crystals.
	alive := 0
	for _, rem := range mgr.crystals {
		if rem != nil && rem.Dimension == world.DimensionEnd && rem.SpikeIndex >= 0 {
			alive++
		}
	}
	mgr.fight.CrystalsAlive = alive

	var dragonToDamage int32
	if mgr.fight.DragonAlive && mgr.fight.NearestCrystalID == crystalID {
		mgr.fight.NearestCrystalID = 0
		dragonToDamage = mgr.fight.DragonEntityID
	}
	mgr.mu.Unlock()

	s.entities.Remove(crystalID)
	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{crystalID}))

	// Clear any fire block at the crystal position.
	if cache := s.DimensionChunks(snap.Dimension); cache != nil {
		bx, by, bz := int(math.Floor(snap.X)), int(math.Floor(snap.Y)), int(math.Floor(snap.Z))
		if world.BlockName(cache.GetBlock(bx, by, bz)) == "minecraft:fire" {
			cache.SetBlock(bx, by, bz, world.StateAir)
		}
	}

	// Radius 6.0 explosion.
	s.TriggerWitherExplosion(snap.Dimension, snap.X, snap.Y, snap.Z, EndCrystalExplosionRadius, false, crystalID)

	// If the Ender Dragon was linked to this crystal, deal 10.0 damage to the dragon!
	if dragonToDamage > 0 {
		s.DamageEnderDragon(dragonToDamage, EnderDragonCrystalDestructionDamage, attacker, false)
	}
	return true
}

// SpawnEnderDragon spawns a minecraft:ender_dragon boss at (x, y, z) in
// minecraft:the_end, initializes its phase machine in HoldingPattern, and
// creates its pink "Ender Dragon" boss bar.
func (s *Server) SpawnEnderDragon(x, y, z float64) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	typeID := ResolveEntityTypeID(MobTypeEnderDragon)
	if typeID < 0 {
		typeID = EntityTypeEnderDragonID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeEnderDragon,
		Dimension: world.DimensionEnd,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  false,
	})

	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(id, tr.aiTickID)
	mem.healthInit = true
	mem.health = DefaultEnderDragonMaxHealth
	mem.lastHealth = DefaultEnderDragonMaxHealth
	tr.mu.Unlock()

	// Create pink Ender Dragon boss bar (PlayBossMusic=true, CreateWorldFog=true).
	bar := s.CreateEntityBossBar(
		id,
		ChatComponent{Translate: "entity.minecraft.ender_dragon"},
		BossBarColorPink,
		BossBarOverlayProgress,
		false, // darkenScreen
		true,  // playBossMusic
		true,  // createWorldFog
	)
	s.UpdateRuntimeBossBarHealth(bar.UUID, DefaultEnderDragonMaxHealth, DefaultEnderDragonMaxHealth)
	s.SyncRuntimeBossBarPlayersInRange(bar.UUID, world.DimensionEnd, 0, 128, 0, EnderDragonBossBarRange)

	mgr := s.dragonManager()
	mgr.mu.Lock()
	if mgr.fight.PortalY <= 0 {
		mgr.fight.PortalY = s.ResolveEndPortalY()
	}
	aliveCrystals := 0
	for _, cr := range mgr.crystals {
		if cr != nil && cr.Dimension == world.DimensionEnd && cr.SpikeIndex >= 0 {
			aliveCrystals++
		}
	}
	mgr.fight.Initialized = true
	mgr.fight.DragonEntityID = id
	mgr.fight.DragonAlive = true
	mgr.fight.DragonKilled = false
	mgr.fight.Health = DefaultEnderDragonMaxHealth
	mgr.fight.MaxHealth = DefaultEnderDragonMaxHealth
	mgr.fight.Phase = DragonPhaseHoldingPattern
	mgr.fight.PhaseTicks = 0
	mgr.fight.DeathTicks = 0
	mgr.fight.OrbitAngle = 0
	mgr.fight.NearestCrystalID = 0
	mgr.fight.CrystalsAlive = aliveCrystals
	mgr.fight.SittingDamageTaken = 0
	mgr.fight.BossBarUUID = bar.UUID
	mgr.fight.Respawning = false
	mgr.fight.RespawnTicks = 0
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(world.DimensionEnd, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(world.DimensionEnd, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeEnderDragonEntityDataPacket(id, DefaultEnderDragonMaxHealth, DragonPhaseHoldingPattern))
	}
	_ = s.SaveDragonFightFile()
	return id, nil
}

// GetEnderDragonFightState returns a snapshot of the EndDragonFight state.
func (s *Server) GetEnderDragonFightState() EnderDragonFightState {
	if s == nil {
		return EnderDragonFightState{}
	}
	mgr := s.dragonManager()
	mgr.mu.RLock()
	out := mgr.fight
	mgr.mu.RUnlock()
	return out
}

// SetEnderDragonPhase transitions the active Ender Dragon to phase and broadcasts
// the updated DATA_PHASE SynchedEntityData packet.
func (s *Server) SetEnderDragonPhase(phase EnderDragonPhase) bool {
	if s == nil {
		return false
	}
	mgr := s.dragonManager()
	mgr.mu.Lock()
	if !mgr.fight.DragonAlive || mgr.fight.DragonEntityID <= 0 {
		mgr.mu.Unlock()
		return false
	}
	mgr.fight.Phase = phase
	mgr.fight.PhaseTicks = 0
	if phase.IsSitting() {
		mgr.fight.SittingDamageTaken = 0
	}
	id := mgr.fight.DragonEntityID
	hp := mgr.fight.Health
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(world.DimensionEnd, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeEnderDragonEntityDataPacket(id, hp, phase))
	}
	return true
}

// EncodeEnderDragonEntityDataPacket encodes ClientboundSetEntityDataPacket (0x63)
// for an EnderDragon in protocol 775:
//   - index 9  (FLOAT): DATA_HEALTH_ID
//   - index 16 (INT):   DATA_PHASE (0..10)
func EncodeEnderDragonEntityDataPacket(entityID int32, health float32, phase EnderDragonPhase) []byte {
	w := protocol.NewWriter(20)
	w.VarInt(entityID)

	w.Byte(EntityDataLivingHealthID)
	w.VarInt(EntityDataSerializerFloat)
	w.Float32(health)

	w.Byte(EntityDataEnderDragonPhaseID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(int32(phase))

	w.Byte(0xFF)
	return w.Bytes()
}

// EncodeEndCrystalEntityDataPacket encodes ClientboundSetEntityDataPacket (0x63)
// for an EndCrystal in protocol 775:
//   - index 8 (OPTIONAL_BLOCK_POS = 15): DATA_BEAM_TARGET (bool + optional Position)
//   - index 9 (BOOLEAN = 8):             DATA_SHOW_BOTTOM
func EncodeEndCrystalEntityDataPacket(entityID int32, hasBeamTarget bool, beamX, beamY, beamZ int, showBottom bool) []byte {
	w := protocol.NewWriter(24)
	w.VarInt(entityID)

	w.Byte(EntityDataEndCrystalBeamTargetID)
	w.VarInt(EntityDataSerializerOptBlockPos)
	w.Bool(hasBeamTarget)
	if hasBeamTarget {
		w.Position(beamX, beamY, beamZ)
	}

	w.Byte(EntityDataEndCrystalShowBottomID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(showBottom)

	w.Byte(0xFF)
	return w.Bytes()
}

// EnderDragonEntityDataPacket returns the SynchedEntityData packet payload for
// the active Ender Dragon, or nil if entityID does not match.
func (s *Server) EnderDragonEntityDataPacket(entityID int32) []byte {
	st := s.GetEnderDragonFightState()
	if !st.DragonAlive || st.DragonEntityID != entityID {
		return nil
	}
	return EncodeEnderDragonEntityDataPacket(entityID, st.Health, st.Phase)
}

// EndCrystalEntityDataPacket returns the SynchedEntityData packet payload for
// an End Crystal entity, or nil if unknown.
func (s *Server) EndCrystalEntityDataPacket(entityID int32) []byte {
	cr, ok := s.GetEndCrystalState(entityID)
	if !ok {
		return nil
	}
	return EncodeEndCrystalEntityDataPacket(entityID, cr.HasBeamTarget, cr.BeamX, cr.BeamY, cr.BeamZ, cr.ShowsBottom)
}

// DamageEnderDragon applies damage to the Ender Dragon:
//   - While perched (Phase.IsSitting()), arrows and projectiles bounce off and deal 0 damage!
//   - While perched, taking >= 50 HP cumulative melee damage causes the dragon to take off (`DragonPhaseTakeoff`).
//   - When Health reaches 0, transitions to `DragonPhaseDying` (or completes death if already dying).
func (s *Server) DamageEnderDragon(entityID int32, damage float32, attacker *PlayerSession, isProjectile bool) (dealt float32, killed bool) {
	if s == nil || s.entities == nil || damage <= 0 {
		return 0, false
	}
	mgr := s.dragonManager()
	mgr.mu.Lock()
	if !mgr.fight.DragonAlive || mgr.fight.DragonEntityID != entityID {
		mgr.mu.Unlock()
		return 0, false
	}
	// Perched Ender Dragon is immune to arrows and projectiles (vanilla EnderDragon.hurt).
	if isProjectile && mgr.fight.Phase.IsSitting() {
		mgr.mu.Unlock()
		return 0, false
	}
	if mgr.fight.Phase == DragonPhaseDying {
		mgr.mu.Unlock()
		return 0, false
	}

	mgr.fight.Health -= damage
	if mgr.fight.Phase.IsSitting() {
		mgr.fight.SittingDamageTaken += damage
		if mgr.fight.SittingDamageTaken >= 50.0 && mgr.fight.Health > 0 {
			mgr.fight.Phase = DragonPhaseTakeoff
			mgr.fight.PhaseTicks = 0
			mgr.fight.SittingDamageTaken = 0
		}
	}
	if attacker != nil && attacker.EntityID != 0 {
		mgr.fight.TargetPlayerID = attacker.EntityID
	}

	enteredDying := false
	if mgr.fight.Health <= 0 {
		mgr.fight.Health = 0
		mgr.fight.Phase = DragonPhaseDying
		mgr.fight.PhaseTicks = 0
		mgr.fight.DeathTicks = 0
		enteredDying = true
	}
	hp := mgr.fight.Health
	maxHP := mgr.fight.MaxHealth
	phase := mgr.fight.Phase
	barUUID := mgr.fight.BossBarUUID
	mgr.mu.Unlock()

	s.SetMobHealth(entityID, hp)
	s.UpdateRuntimeBossBarHealth(barUUID, hp, maxHP)

	ent := s.entities.Get(entityID)
	var cx, cz int32
	var ex, ey, ez float64
	if ent != nil {
		cp := ent.ChunkPos()
		cx, cz = cp.X, cp.Z
		ex, ey, ez = ent.X, ent.Y, ent.Z
	}
	var attackerID int32
	if attacker != nil {
		attackerID = attacker.EntityID
	}
	s.BroadcastDimensionChunk(world.DimensionEnd, cx, cz, protocol.PlayHurtAnimation, EncodeHurtAnimation(entityID, 0))
	s.BroadcastDimensionChunk(world.DimensionEnd, cx, cz, protocol.PlayDamageEvent, EncodeDamageEventPacket(entityID, attackerID))
	s.BroadcastDimensionChunk(world.DimensionEnd, cx, cz, PlaySetEntityDataCB,
		EncodeEnderDragonEntityDataPacket(entityID, hp, phase))

	if !enteredDying {
		s.BroadcastDimensionChunk(world.DimensionEnd, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:entity.ender_dragon.hurt", SoundSourceHostile, ex, ey, ez, 5.0, 1.0, 0))
		return damage, false
	}

	// Complete the dragon death sequence immediately when killed by direct attack so
	// callers/tests observe the portal, dragon egg, XP, and advancement right away
	// (or TickEnderDragons also completes it if triggered via tick).
	s.completeEnderDragonDeath(attacker)
	return damage, true
}

func (s *Server) completeEnderDragonDeath(killer *PlayerSession) {
	mgr := s.dragonManager()
	mgr.mu.Lock()
	if !mgr.fight.DragonAlive {
		mgr.mu.Unlock()
		return
	}
	entityID := mgr.fight.DragonEntityID
	firstKill := !mgr.fight.PreviouslyKilled
	portalY := mgr.fight.PortalY
	if portalY <= 0 {
		portalY = EnderDragonDefaultPortalY
		mgr.fight.PortalY = portalY
	}
	// Clear any crystal beam target pointing at the dead dragon.
	for _, cr := range mgr.crystals {
		if cr != nil && cr.HasBeamTarget {
			cr.HasBeamTarget = false
		}
	}
	mgr.fight.DragonAlive = false
	mgr.fight.DragonKilled = true
	mgr.fight.PreviouslyKilled = true
	mgr.fight.Health = 0
	mgr.fight.NearestCrystalID = 0
	mgr.mu.Unlock()

	tr := s.entityTracker()
	tr.mu.Lock()
	delete(tr.mobStates, entityID)
	tr.mu.Unlock()

	s.RemoveEntityBossBar(entityID)

	var ex, ey, ez float64 = 0.5, float64(portalY + 4), 0.5
	if ent := s.entities.Get(entityID); ent != nil {
		ex, ey, ez = ent.X, ent.Y, ent.Z
	}
	cx := int32(int64(math.Floor(ex)) >> 4)
	cz := int32(int64(math.Floor(ez)) >> 4)

	s.BroadcastDimensionChunk(world.DimensionEnd, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.ender_dragon.death", SoundSourceHostile, ex, ey, ez, 5.0, 1.0, 0))
	s.BroadcastDimensionChunk(world.DimensionEnd, cx, cz, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(entityID, EntityEventDeath))
	s.entities.Remove(entityID)
	s.BroadcastDimensionChunk(world.DimensionEnd, cx, cz, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{entityID}))

	// Activate Exit Portal at (0, portalY, 0) and place Dragon Egg at (0, portalY+4, 0) on first kill!
	if cache := s.DimensionChunks(world.DimensionEnd); cache != nil {
		changes := PlaceEndPodium(cache, portalY, true, firstKill)
		s.broadcastDimensionBlockChanges(world.DimensionEnd, cache, changes, nil)
	}

	// Spawn a new End Gateway on the outer ring (up to 20 gateways).
	s.SpawnNextEndGateway()

	// Award 12,000 XP on first kill, or 500 XP on subsequent kills.
	xp := EnderDragonSubsequentKillXP
	if firstKill {
		xp = EnderDragonFirstKillXP
	}
	s.SpawnExperienceOrbsInDimension(world.DimensionEnd, 0.5, float64(portalY+5), 0.5, xp)

	if killer != nil {
		s.TriggerEntityKilled(killer, MobTypeEnderDragon)
		s.GrantCriterion(killer, "minecraft:end/kill_dragon", "killed_dragon")
		s.IncrementCriteriaObjectives(killer.Profile.Name, "totalKillCount", 1)
	}
	_ = s.SaveDragonFightFile()
}

// SpawnNextEndGateway spawns the next End Gateway structure at radius 96 around
// (0, 75, 0) in minecraft:the_end (up to 20 gateways).
func (s *Server) SpawnNextEndGateway() (world.BlockPos, bool) {
	if s == nil {
		return world.BlockPos{}, false
	}
	mgr := s.dragonManager()
	mgr.mu.Lock()
	if mgr.fight.GatewaysSpawned >= EnderDragonMaxGateways {
		mgr.mu.Unlock()
		return world.BlockPos{}, false
	}
	idx := mgr.fight.GatewaysSpawned
	mgr.fight.GatewaysSpawned++
	mgr.mu.Unlock()

	angle := 2.0 * (-math.Pi + (math.Pi/float64(EnderDragonMaxGateways))*float64(idx))
	gx := int(math.Floor(EnderDragonGatewayRadius * math.Cos(angle)))
	gz := int(math.Floor(EnderDragonGatewayRadius * math.Sin(angle)))
	gy := EnderDragonGatewayY

	if cache := s.DimensionChunks(world.DimensionEnd); cache != nil {
		changes := PlaceEndGatewayStructure(cache, gx, gy, gz)
		s.broadcastDimensionBlockChanges(world.DimensionEnd, cache, changes, nil)
	}
	return world.BlockPos{X: gx, Y: gy, Z: gz}, true
}

// TryRespawnEnderDragon checks whether 4 End Crystals are placed on the 4
// cardinal edges of the Exit Portal podium ((0, portalY+1, ±3) and (±3, portalY+1, 0)).
// When all 4 crystals are present and the dragon is currently dead, it consumes
// the 4 portal crystals, deactivates the exit portal, regenerates the 10 spike
// crystals, and respawns the Ender Dragon!
func (s *Server) TryRespawnEnderDragon() (int32, bool) {
	if s == nil || s.entities == nil {
		return 0, false
	}
	mgr := s.dragonManager()
	mgr.mu.RLock()
	alive := mgr.fight.DragonAlive
	portalY := mgr.fight.PortalY
	mgr.mu.RUnlock()

	if alive {
		return 0, false
	}
	if portalY <= 0 {
		portalY = s.ResolveEndPortalY()
	}

	// Check the 4 cardinal sides of the Exit Portal rim: (0, ±3) and (±3, 0) at y = portalY..portalY+2.
	cardinalOffsets := [4][2]int{
		{0, -3},
		{0, 3},
		{-3, 0},
		{3, 0},
	}
	crystals := s.EndCrystalsInDimension(world.DimensionEnd)
	var matchedIDs [4]int32
	for i, off := range cardinalOffsets {
		tx := float64(off[0]) + 0.5
		tz := float64(off[1]) + 0.5
		found := int32(0)
		for _, cr := range crystals {
			if math.Abs(cr.X-tx) <= 1.5 && math.Abs(cr.Z-tz) <= 1.5 &&
				cr.Y >= float64(portalY) && cr.Y <= float64(portalY+3) {
				found = cr.EntityID
				break
			}
		}
		if found == 0 {
			return 0, false
		}
		matchedIDs[i] = found
	}

	// Remove the 4 summoning crystals around the portal.
	mgr.mu.Lock()
	for _, cid := range matchedIDs {
		delete(mgr.crystals, cid)
		s.entities.Remove(cid)
	}
	mgr.mu.Unlock()
	s.BroadcastDimensionChunk(world.DimensionEnd, 0, 0, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, matchedIDs[:]))

	// Deactivate the Exit Portal while the respawned dragon is alive.
	if cache := s.DimensionChunks(world.DimensionEnd); cache != nil {
		changes := PlaceEndPodium(cache, portalY, false, false)
		s.broadcastDimensionBlockChanges(world.DimensionEnd, cache, changes, nil)
	}

	// Regenerate all 10 obsidian spike crystals and spawn the new Ender Dragon.
	s.SpawnEndSpikeCrystals()
	dragonID, err := s.SpawnEnderDragon(0.5, float64(portalY)+24.0, 0.5)
	if err != nil {
		return 0, false
	}
	return dragonID, true
}

// ShootDragonFireball spawns and launches a minecraft:dragon_fireball from the
// Ender Dragon toward (targetX, targetY, targetZ).
func (s *Server) ShootDragonFireball(dragonID int32, startX, startY, startZ, targetX, targetY, targetZ float64) (int32, bool) {
	if s == nil || s.entities == nil {
		return 0, false
	}
	dx := targetX - startX
	dy := targetY - startY
	dz := targetZ - startZ
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist < 1e-4 {
		dx, dy, dz, dist = 0, -1, 0, 1
	}
	vx := (dx / dist) * DragonFireballSpeed
	vy := (dy / dist) * DragonFireballSpeed
	vz := (dz / dist) * DragonFireballSpeed

	typeID := ResolveEntityTypeID(EntityTypeDragonFireball)
	if typeID < 0 {
		typeID = EntityTypeDragonFireballID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  EntityTypeDragonFireball,
		Dimension: world.DimensionEnd,
		X:         startX,
		Y:         startY,
		Z:         startZ,
		VelocityX: clampVelocityShort(vx),
		VelocityY: clampVelocityShort(vy),
		VelocityZ: clampVelocityShort(vz),
		OnGround:  false,
	})
	fb := &DragonFireballProjectile{
		EntityID:      id,
		OwnerEntityID: dragonID,
		Dimension:     world.DimensionEnd,
		X:             startX,
		Y:             startY,
		Z:             startZ,
		VX:            vx,
		VY:            vy,
		VZ:            vz,
	}
	mgr := s.dragonManager()
	mgr.mu.Lock()
	mgr.fireballs[id] = fb
	mgr.mu.Unlock()

	cx := int32(int64(math.Floor(startX)) >> 4)
	cz := int32(int64(math.Floor(startZ)) >> 4)
	if ent := s.entities.Get(id); ent != nil {
		s.BroadcastDimensionChunk(world.DimensionEnd, cx, cz, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
	}
	s.BroadcastDimensionChunk(world.DimensionEnd, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.ender_dragon.shoot", SoundSourceHostile, startX, startY, startZ, 5.0, 1.0, 0))
	return id, true
}

// SpawnDragonBreathCloud spawns a Dragon's Breath minecraft:area_effect_cloud at
// (x, y, z) in dimension.
func (s *Server) SpawnDragonBreathCloud(dimension string, x, y, z float64, ownerID int32) int32 {
	if s == nil || s.entities == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(EntityTypeAreaEffectCloud)
	if typeID < 0 {
		typeID = EntityTypeAreaEffectCloudID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  EntityTypeAreaEffectCloud,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})
	cloud := &DragonBreathCloud{
		EntityID:       id,
		OwnerEntityID:  ownerID,
		Dimension:      normDim,
		X:              x,
		Y:              y,
		Z:              z,
		Radius:         DragonBreathCloudRadius,
		RemainingTicks: DragonBreathCloudDurationTicks,
	}
	mgr := s.dragonManager()
	mgr.mu.Lock()
	mgr.clouds[id] = cloud
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
	}
	return id
}

// TryCollectDragonBreath checks if session is holding a minecraft:glass_bottle
// near an active Dragon's Breath cloud and fills it into 1x minecraft:dragon_breath.
func (s *Server) TryCollectDragonBreath(session *PlayerSession) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	held := session.HeldItem()
	if held.IsEmpty() || canonicalResourceName(held.ResolvedID()) != "minecraft:glass_bottle" {
		return false
	}
	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)

	mgr := s.dragonManager()
	mgr.mu.Lock()
	var matchedID int32
	for id, cl := range mgr.clouds {
		if cl == nil || cl.Dimension != dim {
			continue
		}
		dx := snap.X - cl.X
		dy := snap.Y - cl.Y
		dz := snap.Z - cl.Z
		if dx*dx+dy*dy+dz*dz <= (cl.Radius+2.0)*(cl.Radius+2.0) {
			matchedID = id
			cl.Radius -= 0.5
			if cl.Radius < 1.0 {
				delete(mgr.clouds, id)
			}
			break
		}
	}
	stillExists := mgr.clouds[matchedID] != nil
	mgr.mu.Unlock()

	if matchedID == 0 {
		return false
	}
	if !stillExists && s.entities != nil {
		s.entities.Remove(matchedID)
		cx := int32(int64(math.Floor(snap.X)) >> 4)
		cz := int32(int64(math.Floor(snap.Z)) >> 4)
		s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlayRemoveEntities, encodeRemoveEntitiesPacket(nil, []int32{matchedID}))
	}

	breath := world.NewNamedItemStack(DragonBreathItemID, 1)
	if breath.IsEmpty() {
		return false
	}
	s.ConsumeHeldItem(session, 1)
	added, changed := session.AddItemToInventory(breath)
	if session.send != nil {
		for _, slot := range changed {
			stID := session.NextContainerStateID()
			_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotPacket(stID, slot, session.InventorySlot(slot)))
		}
	}
	if added < 1 {
		s.SpawnItemEntityInDimension(dim, snap.X, snap.Y+0.5, snap.Z, breath)
	}
	s.GrantCriterion(session, "minecraft:end/dragon_breath", "dragon_breath")
	return true
}

// TickEnderDragons steps the EndDragonFight manager, active Ender Dragon phase
// machine, End Crystal healing beam, Dragon Fireballs, and Dragon's Breath clouds
// by one game tick (20 TPS).
func (s *Server) TickEnderDragons(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	// Auto-start the End Dragon fight when a player enters minecraft:the_end for the first time.
	mgr := s.dragonManager()
	mgr.mu.RLock()
	initDone := mgr.fight.Initialized
	alive := mgr.fight.DragonAlive
	mgr.mu.RUnlock()

	if !initDone {
		for _, p := range s.onlinePlayersSnapshot() {
			if p != nil && !p.IsDead() && world.NormalizeDimension(p.Snapshot().Dimension) == world.DimensionEnd {
				_, _ = s.StartEndDragonFight()
				break
			}
		}
	}

	if alive {
		s.tickActiveEnderDragon(rng)
	}
	s.tickDragonFireballs()
	s.tickDragonBreathClouds()
}

func (s *Server) tickActiveEnderDragon(rng *rand.Rand) {
	mgr := s.dragonManager()
	mgr.mu.Lock()
	if !mgr.fight.DragonAlive || mgr.fight.DragonEntityID <= 0 {
		mgr.mu.Unlock()
		return
	}
	dragonID := mgr.fight.DragonEntityID
	ent := s.entities.Get(dragonID)
	if ent == nil {
		mgr.fight.DragonAlive = false
		mgr.mu.Unlock()
		s.RemoveEntityBossBar(dragonID)
		return
	}

	mgr.fight.TickCounter++
	mgr.fight.PhaseTicks++
	portalY := mgr.fight.PortalY
	if portalY <= 0 {
		portalY = EnderDragonDefaultPortalY
		mgr.fight.PortalY = portalY
	}

	// 1. Check nearest End Crystal within 32 blocks for beam link + healing (+1 HP every 10 ticks).
	var nearestID int32
	bestDistSq := EnderDragonCrystalRange * EnderDragonCrystalRange
	for id, cr := range mgr.crystals {
		if cr == nil || cr.Dimension != world.DimensionEnd {
			continue
		}
		dx := cr.X - ent.X
		dy := cr.Y - ent.Y
		dz := cr.Z - ent.Z
		dSq := dx*dx + dy*dy + dz*dz
		if dSq <= bestDistSq {
			bestDistSq = dSq
			nearestID = id
		}
	}

	prevCrystalID := mgr.fight.NearestCrystalID
	mgr.fight.NearestCrystalID = nearestID
	var beamUpdates []EndCrystalState

	if prevCrystalID != nearestID {
		if prevCr, ok := mgr.crystals[prevCrystalID]; ok && prevCr != nil && prevCr.HasBeamTarget {
			prevCr.HasBeamTarget = false
			beamUpdates = append(beamUpdates, *prevCr)
		}
	}
	if nearestID > 0 {
		if curCr, ok := mgr.crystals[nearestID]; ok && curCr != nil {
			bx, by, bz := int(math.Floor(ent.X)), int(math.Floor(ent.Y))+2, int(math.Floor(ent.Z))
			if !curCr.HasBeamTarget || curCr.BeamX != bx || curCr.BeamY != by || curCr.BeamZ != bz {
				curCr.HasBeamTarget = true
				curCr.BeamX = bx
				curCr.BeamY = by
				curCr.BeamZ = bz
				beamUpdates = append(beamUpdates, *curCr)
			}
			if mgr.fight.TickCounter%EnderDragonCrystalHealInterval == 0 &&
				mgr.fight.Health > 0 && mgr.fight.Health < mgr.fight.MaxHealth {
				mgr.fight.Health += EnderDragonCrystalHealAmount
				if mgr.fight.Health > mgr.fight.MaxHealth {
					mgr.fight.Health = mgr.fight.MaxHealth
				}
			}
		}
	}

	// 2. Step the Ender Dragon's phase state machine.
	curX, curY, curZ := ent.X, ent.Y, ent.Z
	nextX, nextY, nextZ := curX, curY, curZ
	nextYaw := ent.Yaw
	prevPhase := mgr.fight.Phase
	var shootFireball bool
	var fbTargetX, fbTargetY, fbTargetZ float64
	var spawnBreathCloud bool
	var finishDeath bool

	targetPlayer := s.findNearestWitherTarget(world.DimensionEnd, curX, curY, curZ, 128.0)

	switch mgr.fight.Phase {
	case DragonPhaseHoldingPattern:
		stepAngle := 0.035
		if mgr.fight.OrbitClockwise {
			mgr.fight.OrbitAngle -= stepAngle
		} else {
			mgr.fight.OrbitAngle += stepAngle
		}
		goalX := math.Cos(mgr.fight.OrbitAngle) * EnderDragonOrbitRadius
		goalZ := math.Sin(mgr.fight.OrbitAngle) * EnderDragonOrbitRadius
		goalY := float64(portalY) + 18.0 + 4.0*math.Sin(mgr.fight.OrbitAngle*2.0)
		nextX, nextY, nextZ, nextYaw = stepFlightToward(curX, curY, curZ, goalX, goalY, goalZ, EnderDragonFlySpeed)

		if mgr.fight.PhaseTicks >= 100 {
			if targetPlayer != nil && mgr.fight.PhaseTicks%120 == 0 {
				tSnap := targetPlayer.Snapshot()
				mgr.fight.TargetX, mgr.fight.TargetY, mgr.fight.TargetZ = tSnap.X, tSnap.Y, tSnap.Z
				mgr.fight.Phase = DragonPhaseStrafePlayer
				mgr.fight.PhaseTicks = 0
			} else if mgr.fight.PhaseTicks >= 200 {
				mgr.fight.Phase = DragonPhaseLandingApproach
				mgr.fight.PhaseTicks = 0
			}
		}

	case DragonPhaseStrafePlayer:
		if targetPlayer != nil {
			tSnap := targetPlayer.Snapshot()
			mgr.fight.TargetX, mgr.fight.TargetY, mgr.fight.TargetZ = tSnap.X, tSnap.Y, tSnap.Z
		}
		nextX, nextY, nextZ, nextYaw = stepFlightToward(curX, curY, curZ, mgr.fight.TargetX, mgr.fight.TargetY+6.0, mgr.fight.TargetZ, EnderDragonFlySpeed)
		dist := math.Hypot(mgr.fight.TargetX-nextX, mgr.fight.TargetZ-nextZ)
		if (dist <= 24.0 && mgr.fight.PhaseTicks >= 20) || mgr.fight.PhaseTicks >= 60 {
			shootFireball = true
			fbTargetX, fbTargetY, fbTargetZ = mgr.fight.TargetX, mgr.fight.TargetY, mgr.fight.TargetZ
			mgr.fight.Phase = DragonPhaseHoldingPattern
			mgr.fight.PhaseTicks = 0
		}

	case DragonPhaseLandingApproach:
		goalX, goalY, goalZ := 0.5, float64(portalY)+10.0, 0.5
		nextX, nextY, nextZ, nextYaw = stepFlightToward(curX, curY, curZ, goalX, goalY, goalZ, EnderDragonFlySpeed)
		if math.Hypot(goalX-nextX, goalZ-nextZ) <= 4.0 || mgr.fight.PhaseTicks >= 80 {
			mgr.fight.Phase = DragonPhaseLanding
			mgr.fight.PhaseTicks = 0
		}

	case DragonPhaseLanding:
		perchX, perchY, perchZ := 0.5, float64(portalY)+4.0, 0.5
		nextX, nextY, nextZ, nextYaw = stepFlightToward(curX, curY, curZ, perchX, perchY, perchZ, EnderDragonFlySpeed*0.75)
		if math.Sqrt((perchX-nextX)*(perchX-nextX)+(perchY-nextY)*(perchY-nextY)+(perchZ-nextZ)*(perchZ-nextZ)) <= 1.2 || mgr.fight.PhaseTicks >= 60 {
			nextX, nextY, nextZ = perchX, perchY, perchZ
			mgr.fight.Phase = DragonPhaseSittingScanning
			mgr.fight.PhaseTicks = 0
			mgr.fight.SittingDamageTaken = 0
		}

	case DragonPhaseSittingScanning:
		nextX, nextY, nextZ = 0.5, float64(portalY)+4.0, 0.5
		if targetPlayer != nil {
			tSnap := targetPlayer.Snapshot()
			nextYaw = yawToward(tSnap.X-nextX, tSnap.Z-nextZ)
		}
		if mgr.fight.PhaseTicks >= 30 {
			mgr.fight.Phase = DragonPhaseSittingFlaming
			mgr.fight.PhaseTicks = 0
		}

	case DragonPhaseSittingFlaming:
		nextX, nextY, nextZ = 0.5, float64(portalY)+4.0, 0.5
		if mgr.fight.PhaseTicks == 10 {
			spawnBreathCloud = true
		}
		if mgr.fight.PhaseTicks >= EnderDragonSittingTicks {
			mgr.fight.Phase = DragonPhaseTakeoff
			mgr.fight.PhaseTicks = 0
		}

	case DragonPhaseSittingAttacking:
		nextX, nextY, nextZ = 0.5, float64(portalY)+4.0, 0.5
		if mgr.fight.PhaseTicks >= 20 {
			mgr.fight.Phase = DragonPhaseSittingFlaming
			mgr.fight.PhaseTicks = 0
		}

	case DragonPhaseChargingPlayer:
		if targetPlayer != nil && mgr.fight.PhaseTicks == 1 {
			tSnap := targetPlayer.Snapshot()
			mgr.fight.TargetX, mgr.fight.TargetY, mgr.fight.TargetZ = tSnap.X, tSnap.Y, tSnap.Z
		}
		nextX, nextY, nextZ, nextYaw = stepFlightToward(curX, curY, curZ, mgr.fight.TargetX, mgr.fight.TargetY, mgr.fight.TargetZ, EnderDragonChargeSpeed)
		if math.Hypot(mgr.fight.TargetX-nextX, mgr.fight.TargetZ-nextZ) <= 2.0 || mgr.fight.PhaseTicks >= 60 {
			mgr.fight.Phase = DragonPhaseHoldingPattern
			mgr.fight.PhaseTicks = 0
		}

	case DragonPhaseTakeoff:
		goalX, goalY, goalZ := 0.5, float64(portalY)+18.0, 0.5
		nextX, nextY, nextZ, nextYaw = stepFlightToward(curX, curY, curZ, goalX, goalY, goalZ, EnderDragonFlySpeed)
		if nextY >= goalY-1.0 || mgr.fight.PhaseTicks >= 40 {
			if targetPlayer != nil && rng != nil && rng.Float32() < 0.4 {
				tSnap := targetPlayer.Snapshot()
				mgr.fight.TargetX, mgr.fight.TargetY, mgr.fight.TargetZ = tSnap.X, tSnap.Y, tSnap.Z
				mgr.fight.Phase = DragonPhaseChargingPlayer
			} else {
				mgr.fight.Phase = DragonPhaseHoldingPattern
			}
			mgr.fight.PhaseTicks = 0
		}

	case DragonPhaseDying:
		perchX, perchY, perchZ := 0.5, float64(portalY)+4.0, 0.5
		nextX, nextY, nextZ, nextYaw = stepFlightToward(curX, curY, curZ, perchX, perchY, perchZ, EnderDragonFlySpeed)
		mgr.fight.DeathTicks++
		if mgr.fight.DeathTicks >= EnderDragonDyingDurationTicks {
			finishDeath = true
		}

	case DragonPhaseHovering:
		// Remain stationary while hovering.
	}

	hp := mgr.fight.Health
	maxHP := mgr.fight.MaxHealth
	curPhase := mgr.fight.Phase
	barUUID := mgr.fight.BossBarUUID
	tickCount := mgr.fight.TickCounter
	mgr.mu.Unlock()

	if finishDeath {
		s.completeEnderDragonDeath(nil)
		return
	}

	s.entities.Update(dragonID, func(e *world.Entity) {
		e.X = nextX
		e.Y = nextY
		e.Z = nextZ
		e.Yaw = nextYaw
		e.HeadYaw = nextYaw
	})
	s.SetMobHealth(dragonID, hp)
	s.UpdateRuntimeBossBarHealth(barUUID, hp, maxHP)
	s.SyncRuntimeBossBarPlayersInRange(barUUID, world.DimensionEnd, 0, 128, 0, EnderDragonBossBarRange)

	for _, bu := range beamUpdates {
		cx := int32(int64(math.Floor(bu.X)) >> 4)
		cz := int32(int64(math.Floor(bu.Z)) >> 4)
		s.BroadcastDimensionChunk(world.DimensionEnd, cx, cz, PlaySetEntityDataCB,
			EncodeEndCrystalEntityDataPacket(bu.EntityID, bu.HasBeamTarget, bu.BeamX, bu.BeamY, bu.BeamZ, bu.ShowsBottom))
	}

	if curPhase != prevPhase || tickCount%10 == 0 {
		cx := int32(int64(math.Floor(nextX)) >> 4)
		cz := int32(int64(math.Floor(nextZ)) >> 4)
		s.BroadcastDimensionChunk(world.DimensionEnd, cx, cz, PlaySetEntityDataCB,
			EncodeEnderDragonEntityDataPacket(dragonID, hp, curPhase))
	}

	if shootFireball {
		s.ShootDragonFireball(dragonID, nextX, nextY+1.0, nextZ, fbTargetX, fbTargetY+0.5, fbTargetZ)
	}
	if spawnBreathCloud {
		s.SpawnDragonBreathCloud(world.DimensionEnd, 0.5, float64(portalY)+1.0, 2.5, dragonID)
	}

	// Melee / wing collision knockback when a survival player is within 3.0 blocks of the dragon.
	if tickCount%10 == 0 && !curPhase.IsSitting() && curPhase != DragonPhaseDying {
		for _, sess := range s.onlinePlayersSnapshot() {
			if sess == nil || sess.IsDead() {
				continue
			}
			pSnap := sess.Snapshot()
			if world.NormalizeDimension(pSnap.Dimension) != world.DimensionEnd ||
				pSnap.GameMode == GameModeCreative || pSnap.GameMode == GameModeSpectator || pSnap.Health <= 0 {
				continue
			}
			dx := pSnap.X - nextX
			dy := pSnap.Y - nextY
			dz := pSnap.Z - nextZ
			if dx*dx+dy*dy+dz*dz <= 9.0 {
				s.AttackPlayerFromMob(dragonID, MobTypeEnderDragon, nextX, nextZ, sess, EnderDragonMeleeDamage)
			}
		}
	}
}

func stepFlightToward(curX, curY, curZ, goalX, goalY, goalZ, speed float64) (nextX, nextY, nextZ float64, yaw float32) {
	dx := goalX - curX
	dy := goalY - curY
	dz := goalZ - curZ
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	yaw = yawToward(dx, dz)
	if dist <= speed || dist < 1e-4 {
		return goalX, goalY, goalZ, yaw
	}
	scale := speed / dist
	return curX + dx*scale, curY + dy*scale, curZ + dz*scale, yaw
}

func (s *Server) tickDragonFireballs() {
	mgr := s.dragonManager()
	mgr.mu.RLock()
	ids := make([]int32, 0, len(mgr.fireballs))
	for id := range mgr.fireballs {
		ids = append(ids, id)
	}
	mgr.mu.RUnlock()

	for _, id := range ids {
		mgr.mu.Lock()
		fb, ok := mgr.fireballs[id]
		if !ok || fb == nil {
			mgr.mu.Unlock()
			continue
		}
		fb.AgeTicks++
		fb.X += fb.VX
		fb.Y += fb.VY
		fb.Z += fb.VZ
		snap := *fb
		mgr.mu.Unlock()

		if snap.AgeTicks > 200 {
			s.removeDragonFireball(id, snap.Dimension, snap.X, snap.Z)
			continue
		}
		s.entities.Update(id, func(e *world.Entity) {
			e.X = snap.X
			e.Y = snap.Y
			e.Z = snap.Z
		})

		impacted := false
		for _, sess := range s.onlinePlayersSnapshot() {
			if sess == nil || sess.IsDead() {
				continue
			}
			pSnap := sess.Snapshot()
			if world.NormalizeDimension(pSnap.Dimension) != snap.Dimension || pSnap.GameMode == GameModeSpectator {
				continue
			}
			dx := pSnap.X - snap.X
			dy := (pSnap.Y + 0.9) - snap.Y
			dz := pSnap.Z - snap.Z
			if dx*dx+dy*dy+dz*dz <= 2.25 {
				impacted = true
				break
			}
		}
		if !impacted {
			if cache := s.DimensionChunks(snap.Dimension); cache != nil {
				bx, by, bz := int(math.Floor(snap.X)), int(math.Floor(snap.Y)), int(math.Floor(snap.Z))
				if by >= world.MinY && by < world.MinY+world.WorldHeight {
					if blk := cache.GetBlock(bx, by, bz); !isPassableBlock(blk) {
						_, _ = s.OnProjectileBlockImpact(snap.Dimension, bx, by, bz, snap.X-snap.VX, snap.Y-snap.VY, snap.Z-snap.VZ, snap.X, snap.Y, snap.Z, id, EntityTypeDragonFireball)
						impacted = true
					}
				}
			}
		}
		if impacted {
			s.removeDragonFireball(id, snap.Dimension, snap.X, snap.Z)
			s.SpawnDragonBreathCloud(snap.Dimension, snap.X, snap.Y, snap.Z, snap.OwnerEntityID)
		}
	}
}

func (s *Server) removeDragonFireball(id int32, dim string, x, z float64) {
	mgr := s.dragonManager()
	mgr.mu.Lock()
	delete(mgr.fireballs, id)
	mgr.mu.Unlock()
	if s.entities != nil {
		s.entities.Remove(id)
	}
	cx := int32(int64(math.Floor(x)) >> 4)
	cz := int32(int64(math.Floor(z)) >> 4)
	s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlayRemoveEntities, encodeRemoveEntitiesPacket(nil, []int32{id}))
}

func (s *Server) tickDragonBreathClouds() {
	mgr := s.dragonManager()
	mgr.mu.RLock()
	ids := make([]int32, 0, len(mgr.clouds))
	for id := range mgr.clouds {
		ids = append(ids, id)
	}
	mgr.mu.RUnlock()

	for _, id := range ids {
		mgr.mu.Lock()
		cl, ok := mgr.clouds[id]
		if !ok || cl == nil {
			mgr.mu.Unlock()
			continue
		}
		cl.RemainingTicks--
		snap := *cl
		if cl.RemainingTicks <= 0 {
			delete(mgr.clouds, id)
		}
		mgr.mu.Unlock()

		if snap.RemainingTicks <= 0 {
			if s.entities != nil {
				s.entities.Remove(id)
			}
			cx := int32(int64(math.Floor(snap.X)) >> 4)
			cz := int32(int64(math.Floor(snap.Z)) >> 4)
			s.BroadcastDimensionChunk(snap.Dimension, cx, cz, protocol.PlayRemoveEntities, encodeRemoveEntitiesPacket(nil, []int32{id}))
			continue
		}

		// Apply Dragon's Breath magic damage every 10 ticks to players standing inside the cloud.
		if snap.RemainingTicks%10 == 0 {
			rSq := snap.Radius * snap.Radius
			for _, sess := range s.onlinePlayersSnapshot() {
				if sess == nil || sess.IsDead() {
					continue
				}
				pSnap := sess.Snapshot()
				if world.NormalizeDimension(pSnap.Dimension) != snap.Dimension ||
					pSnap.GameMode == GameModeCreative || pSnap.GameMode == GameModeSpectator || pSnap.Health <= 0 {
					continue
				}
				dx := pSnap.X - snap.X
				dy := pSnap.Y - snap.Y
				dz := pSnap.Z - snap.Z
				if dx*dx+dz*dz <= rSq && math.Abs(dy) <= 2.0 {
					scaled := ScaleDamageByDifficulty(EnderDragonBreathDamage, s.Difficulty())
					s.DamagePlayerWithEffects(sess, scaled, "dragon_breath")
					if sess.IsDead() {
						s.TriggerKilledByEntity(sess, MobTypeEnderDragon)
						s.BroadcastDeathMessage(sess, "dragon_breath", MobTypeEnderDragon)
					}
				}
			}
		}
	}
}

// --- Persistence (<WorldDir>/data/end_dragon_fight.dat) ---

func (s *Server) dragonFightFilePath() string {
	if s == nil || s.cfg.WorldDir == "" {
		return ""
	}
	return filepath.Join(s.cfg.WorldDir, "data", "end_dragon_fight.dat")
}

// SaveDragonFightToNBT serializes the EndDragonFight state into an NBT compound.
func (s *Server) SaveDragonFightToNBT() *nbt.Compound {
	st := s.GetEnderDragonFightState()
	root := nbt.NewCompound()
	root.Set("DataVersion", nbt.Int(mapDataVersion26_1_2))
	var killedByte, prevKilledByte byte
	if st.DragonKilled {
		killedByte = 1
	}
	if st.PreviouslyKilled {
		prevKilledByte = 1
	}
	fightTag := nbt.NewCompound().
		Set("DragonKilled", nbt.Byte(killedByte)).
		Set("PreviouslyKilled", nbt.Byte(prevKilledByte)).
		Set("PortalY", nbt.Int(int32(st.PortalY))).
		Set("GatewaysSpawned", nbt.Int(int32(st.GatewaysSpawned)))
	root.Set("DragonFight", fightTag)
	return root
}

// LoadDragonFightFromNBT restores the EndDragonFight state from an NBT compound.
func (s *Server) LoadDragonFightFromNBT(root *nbt.Compound) {
	if s == nil || root == nil {
		return
	}
	tag, ok := root.Get("DragonFight")
	if !ok {
		return
	}
	comp, ok := tag.(*nbt.Compound)
	if !ok || comp == nil {
		return
	}
	mgr := s.dragonManager()
	mgr.mu.Lock()
	if b, ok := comp.Get("DragonKilled"); ok {
		if v, ok := b.(nbt.Byte); ok {
			mgr.fight.DragonKilled = v != 0
		}
	}
	if b, ok := comp.Get("PreviouslyKilled"); ok {
		if v, ok := b.(nbt.Byte); ok {
			mgr.fight.PreviouslyKilled = v != 0
		}
	}
	if p, ok := comp.Get("PortalY"); ok {
		if v, ok := p.(nbt.Int); ok && v > 0 {
			mgr.fight.PortalY = int(v)
		}
	}
	if g, ok := comp.Get("GatewaysSpawned"); ok {
		if v, ok := g.(nbt.Int); ok && v >= 0 {
			mgr.fight.GatewaysSpawned = int(v)
		}
	}
	mgr.mu.Unlock()
}

// SaveDragonFightFile writes <WorldDir>/data/end_dragon_fight.dat if WorldDir is configured.
func (s *Server) SaveDragonFightFile() error {
	path := s.dragonFightFilePath()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data := nbt.MarshalNamed("", s.SaveDragonFightToNBT())
	return os.WriteFile(path, data, 0o644)
}

// LoadDragonFightFile reads <WorldDir>/data/end_dragon_fight.dat if it exists.
func (s *Server) LoadDragonFightFile() error {
	path := s.dragonFightFilePath()
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	_, root, err := nbt.UnmarshalNamed(raw)
	if err != nil {
		return err
	}
	if comp, ok := root.(*nbt.Compound); ok {
		s.LoadDragonFightFromNBT(comp)
	}
	return nil
}
