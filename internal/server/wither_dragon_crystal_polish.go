package server

import (
	"math"
	"math/rand"
	"strings"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// --- Wither, Ender Dragon & End Crystal Polish (Minecraft 26.1.2, protocol 775) ---

const (
	WitherRoseBlockID = "minecraft:wither_rose"
	WitherRoseItemID  = "minecraft:wither_rose"

	WitherIdleBlueSkullThreshold = 15
	WitherRoseEffectDuration     = 40 // 2.0s Wither I on contact
	WitherRoseContactDamage      = float32(1.0)

	EnderDragonRespawnSpikeIntervalTicks = 8
	EnderDragonRespawnSkyBeamY           = 128
)

// --- 1. MAIN (7bf5170) End Podium, Ring Gateways & End Spawn Platform Hooks ---

// EndGatewayOrderForServer returns the deterministic 20-element ring gateway
// slot order for this server's world seed using `world.EndRingGatewayOrder`.
func (s *Server) EndGatewayOrderForServer() [20]int {
	var seed int64
	if s != nil {
		seed = s.cfg.WorldSeed
	}
	return world.EndRingGatewayOrder(seed)
}

// NextEndGatewaySlotAndPos returns the ring slot index (0..19) and world block
// coordinates `(gx, 75, gz)` for the next End Gateway to be spawned on dragon
// death, without incrementing `GatewaysSpawned`.
func (s *Server) NextEndGatewaySlotAndPos() (slot int, pos world.BlockPos, ok bool) {
	if s == nil {
		return 0, world.BlockPos{}, false
	}
	mgr := s.dragonManager()
	mgr.mu.RLock()
	idx := mgr.fight.GatewaysSpawned
	mgr.mu.RUnlock()
	if idx < 0 || idx >= EnderDragonMaxGateways {
		return 0, world.BlockPos{}, false
	}
	order := world.EndRingGatewayOrder(s.cfg.WorldSeed)
	slot = order[idx]
	gp := world.EndRingGatewayPos(slot)
	return slot, world.BlockPos{X: gp[0], Y: gp[1], Z: gp[2]}, true
}

// EnsureEndSpawnPlatform places the vanilla 26.1.2 5x5 obsidian End spawn
// platform centered at (100, 49, 0) (`y=48` obsidian, `y=49..51` air) in
// `minecraft:the_end`, syncing both the chunk structure via `world.PlaceEndPlatform`
// and broadcasting block updates.
func (s *Server) EnsureEndSpawnPlatform() []world.BlockChange {
	if s == nil {
		return nil
	}
	cache := s.DimensionChunks(world.DimensionEnd)
	if cache == nil {
		return nil
	}
	obsidian, ok := world.BlockStateByName("minecraft:obsidian", nil)
	if !ok {
		obsidian = world.StateBedrock
	}
	var changes []world.BlockChange
	for dz := -2; dz <= 2; dz++ {
		for dx := -2; dx <= 2; dx++ {
			for dy := -1; dy < 3; dy++ {
				wx, wy, wz := 100+dx, 49+dy, 0+dz
				st := world.StateAir
				if dy == -1 {
					st = obsidian
				}
				cache.SetBlock(wx, wy, wz, st)
				changes = append(changes, world.BlockChange{
					Pos:      world.BlockPos{X: wx, Y: wy, Z: wz},
					NewState: st,
				})
			}
		}
	}
	s.broadcastDimensionBlockChanges(world.DimensionEnd, cache, changes, nil)
	return changes
}

var (
	endGatewayBERegMu sync.Mutex
	endGatewayBEReg   = make(map[*Server]map[[3]int]world.StructureBlockEntity)
)

// SetEndGatewayBlockEntityAt stores a `world.StructureBlockEntity` for an
// `minecraft:end_gateway` at `(be.X, be.Y, be.Z)` in `minecraft:the_end`.
func (s *Server) SetEndGatewayBlockEntityAt(be world.StructureBlockEntity) {
	if s == nil {
		return
	}
	endGatewayBERegMu.Lock()
	m := endGatewayBEReg[s]
	if m == nil {
		m = make(map[[3]int]world.StructureBlockEntity)
		endGatewayBEReg[s] = m
	}
	m[[3]int{be.X, be.Y, be.Z}] = be
	endGatewayBERegMu.Unlock()
}

// GetEndGatewayBlockEntityAt returns the stored `world.StructureBlockEntity`
// for an `minecraft:end_gateway` at `(x, y, z)` in `minecraft:the_end`.
func (s *Server) GetEndGatewayBlockEntityAt(x, y, z int) (world.StructureBlockEntity, bool) {
	if s == nil {
		return world.StructureBlockEntity{}, false
	}
	endGatewayBERegMu.Lock()
	defer endGatewayBERegMu.Unlock()
	m := endGatewayBEReg[s]
	if m == nil {
		return world.StructureBlockEntity{}, false
	}
	be, ok := m[[3]int{x, y, z}]
	return be, ok
}

// PlaceEndGatewayWithBlockEntity places a 3x5x3 End Gateway structure at
// `(ox, oy, oz)` in `minecraft:the_end` and attaches the `minecraft:end_gateway`
// `StructureBlockEntity` (`world.NewEndGatewayBlockEntity`) with `exitPortal`
// and `exact` teleport configuration.
func (s *Server) PlaceEndGatewayWithBlockEntity(ox, oy, oz int, exitPortal *[3]int, exact bool) []world.BlockChange {
	if s == nil {
		return nil
	}
	cache := s.DimensionChunks(world.DimensionEnd)
	if cache == nil {
		return nil
	}
	changes := PlaceEndGatewayStructure(cache, ox, oy, oz)
	s.SetEndGatewayBlockEntityAt(world.NewEndGatewayBlockEntity(ox, oy, oz, exitPortal, exact))
	s.broadcastDimensionBlockChanges(world.DimensionEnd, cache, changes, nil)
	return changes
}

// ResolveEndGatewayExitAt resolves the destination block coordinates for an
// `minecraft:end_gateway` at `(gx, gy, gz)` in `minecraft:the_end`:
//   - If a `minecraft:end_gateway` block entity with an exit target
//     (`world.ParseEndGatewayBlockEntity`) is stored at `(gx, gy, gz)`, uses
//     that target; if `exact == true` (e.g., an outer-island return gateway
//     targeting `world.EndSpawnPointPos = (100, 50, 0)`), ensures the 5x5
//     obsidian spawn platform is generated via `EnsureEndSpawnPlatform()`.
//   - Otherwise (for a central ring gateway), computes the outer-island landing
//     target `(exitPos, returnGatewayPos)` radial 1024 blocks outward, places
//     the paired return gateway at `returnGatewayPos` targeting `(100, 50, 0)`
//     (`exact=true`), and saves `exitPos` onto the central gateway's block entity.
func (s *Server) ResolveEndGatewayExitAt(gx, gy, gz int) (exitPos [3]int, returnGatewayPos [3]int, exact bool, ok bool) {
	if s == nil {
		return [3]int{}, [3]int{}, false, false
	}
	cache := s.DimensionChunks(world.DimensionEnd)
	if cache == nil {
		return [3]int{}, [3]int{}, false, false
	}
	if be, exists := s.GetEndGatewayBlockEntityAt(gx, gy, gz); exists {
		if target, isExact, hasExit := world.ParseEndGatewayBlockEntity(be); hasExit {
			if isExact && target == world.EndSpawnPointPos {
				s.EnsureEndSpawnPlatform()
			}
			return target, [3]int{}, isExact, true
		}
	}

	// Central ring gateway without pre-assigned exit: compute radial outer-island target (~1024 blocks out).
	length := math.Hypot(float64(gx), float64(gz))
	dirX, dirZ := 1.0, 0.0
	if length > 1e-6 {
		dirX = float64(gx) / length
		dirZ = float64(gz) / length
	}
	tx := int(math.Round(dirX * 1024.0))
	tz := int(math.Round(dirZ * 1024.0))
	ty := 64
	for y := 100; y >= 30; y-- {
		st := cache.GetBlock(tx, y, tz)
		if st != world.StateAir {
			ty = y
			break
		}
	}
	exitPos = [3]int{tx, ty + 1, tz}
	returnGatewayPos = [3]int{tx, ty + 10, tz}

	// Place paired return gateway at returnGatewayPos targeting EndSpawnPointPos (100, 50, 0) with exact=true.
	spawnTarget := world.EndSpawnPointPos
	s.PlaceEndGatewayWithBlockEntity(returnGatewayPos[0], returnGatewayPos[1], returnGatewayPos[2], &spawnTarget, true)

	// Persist exitPos on the central ring gateway's block entity.
	s.SetEndGatewayBlockEntityAt(world.NewEndGatewayBlockEntity(gx, gy, gz, &exitPos, false))
	return exitPos, returnGatewayPos, false, true
}

// TeleportPlayerThroughEndGateway teleports session through the `minecraft:end_gateway`
// block at `(gx, gy, gz)` in `minecraft:the_end`, resolving its destination via
// `ResolveEndGatewayExitAt` and granting `minecraft:end/enter_end_gateway`.
func (s *Server) TeleportPlayerThroughEndGateway(session *PlayerSession, gx, gy, gz int) (destPos [3]int, ok bool) {
	if s == nil || session == nil || session.IsDead() {
		return [3]int{}, false
	}
	exitPos, _, exact, resolved := s.ResolveEndGatewayExitAt(gx, gy, gz)
	if !resolved {
		return [3]int{}, false
	}
	session.SetDimension(world.DimensionEnd)
	s.SetPlayerPosition(session, float64(exitPos[0])+0.5, float64(exitPos[1]), float64(exitPos[2])+0.5)
	if !exact {
		s.GrantCriterion(session, "minecraft:end/enter_end_gateway", "entered_end_gateway")
	}
	return exitPos, true
}

// --- 2. Guarded Spike Iron Bars, End Crystal Damage & Staged Respawn Sequence ---

// RebuildEndSpikeTop reconstructs the top of an End obsidian pillar from
// `world.EndSpike`, including the obsidian cap, bedrock base, fire block, and
// (when `sp.Guarded == true`) the 5x4x5 `minecraft:iron_bars` cage around the crystal.
func RebuildEndSpikeTop(cache *world.Cache, sp world.EndSpike) []world.BlockChange {
	if cache == nil {
		return nil
	}
	obsidian, ok := world.BlockStateByName("minecraft:obsidian", nil)
	if !ok {
		obsidian = world.StateBedrock
	}
	fireState, hasFire := world.BlockStateByName("minecraft:fire", nil)
	barsState, hasBars := world.BlockStateByName("minecraft:iron_bars", map[string]string{
		"east":        "false",
		"north":       "false",
		"south":       "false",
		"waterlogged": "false",
		"west":        "false",
	})
	if !hasBars {
		barsState, hasBars = world.BlockStateByName("minecraft:iron_bars", nil)
	}

	var changes []world.BlockChange
	setB := func(x, y, z int, st uint16) {
		cache.SetBlock(x, y, z, st)
		changes = append(changes, world.BlockChange{
			Pos:      world.BlockPos{X: x, Y: y, Z: z},
			NewState: st,
		})
	}

	rSq := sp.Radius*sp.Radius + 1
	for dz := -sp.Radius; dz <= sp.Radius; dz++ {
		for dx := -sp.Radius; dx <= sp.Radius; dx++ {
			if dx*dx+dz*dz <= rSq {
				setB(sp.CenterX+dx, sp.Height-1, sp.CenterZ+dz, obsidian)
				setB(sp.CenterX+dx, sp.Height, sp.CenterZ+dz, obsidian)
			}
		}
	}

	if sp.Guarded && hasBars {
		for dx := -2; dx <= 2; dx++ {
			for dz := -2; dz <= 2; dz++ {
				edgeX := dx == -2 || dx == 2
				edgeZ := dz == -2 || dz == 2
				for dy := 0; dy <= 3; dy++ {
					topY := dy == 3
					if edgeX || edgeZ || topY {
						setB(sp.CenterX+dx, sp.Height+dy, sp.CenterZ+dz, barsState)
					}
				}
			}
		}
	}

	setB(sp.CenterX, sp.Height, sp.CenterZ, world.StateBedrock)
	if hasFire {
		setB(sp.CenterX, sp.Height+1, sp.CenterZ, fireState)
	}
	return changes
}

// SetEndCrystalBeamTarget updates the beam target position (`DATA_BEAM_TARGET`)
// on an End Crystal entity and broadcasts its updated `SynchedEntityData`.
func (s *Server) SetEndCrystalBeamTarget(crystalID int32, hasBeam bool, beamX, beamY, beamZ int) bool {
	if s == nil {
		return false
	}
	mgr := s.dragonManager()
	mgr.mu.Lock()
	cr, ok := mgr.crystals[crystalID]
	if !ok || cr == nil {
		mgr.mu.Unlock()
		return false
	}
	cr.HasBeamTarget = hasBeam
	cr.BeamX = beamX
	cr.BeamY = beamY
	cr.BeamZ = beamZ
	snap := *cr
	mgr.mu.Unlock()

	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySetEntityDataCB,
		EncodeEndCrystalEntityDataPacket(crystalID, snap.HasBeamTarget, snap.BeamX, snap.BeamY, snap.BeamZ, snap.ShowsBottom))
	return true
}

// DamageEndCrystal handles damage dealt to an `minecraft:end_crystal` from any
// source (melee, projectile, explosion):
//   - Ignores damage caused by `minecraft:ender_dragon` itself (vanilla rule).
//   - Destroys the crystal (`DestroyEndCrystal`), triggering its radius-6.0
//     explosion, dealing 10.0 backlash damage to a linked Ender Dragon, and
//     chain-detonating any other End Crystals within 6.0 blocks!
func (s *Server) DamageEndCrystal(crystalID int32, damage float32, cause string, attacker *PlayerSession) bool {
	if s == nil || damage <= 0 {
		return false
	}
	if canonicalResourceName(cause) == MobTypeEnderDragon {
		return false
	}
	cr, ok := s.GetEndCrystalState(crystalID)
	if !ok {
		return false
	}
	if !s.DestroyEndCrystal(crystalID, attacker) {
		return false
	}
	// Chain-detonate any other End Crystals within the 6.0-block blast radius.
	radSq := EndCrystalExplosionRadius * EndCrystalExplosionRadius
	for _, other := range s.EndCrystalsInDimension(cr.Dimension) {
		if other.EntityID == crystalID {
			continue
		}
		dx := other.X - cr.X
		dy := other.Y - cr.Y
		dz := other.Z - cr.Z
		if dx*dx+dy*dy+dz*dz <= radSq {
			s.DestroyEndCrystal(other.EntityID, attacker)
		}
	}
	return true
}

// FindPortalRespawnCrystals checks whether 4 End Crystals are positioned on the
// 4 cardinal edges of the Exit Portal podium `((0, ±3) and (±3, 0))` in
// `minecraft:the_end`.
func (s *Server) FindPortalRespawnCrystals() ([4]int32, bool) {
	var matched [4]int32
	if s == nil {
		return matched, false
	}
	mgr := s.dragonManager()
	mgr.mu.RLock()
	portalY := mgr.fight.PortalY
	mgr.mu.RUnlock()
	if portalY <= 0 {
		portalY = s.ResolveEndPortalY()
	}

	cardinalOffsets := [4][2]int{
		{0, -3},
		{0, 3},
		{-3, 0},
		{3, 0},
	}
	crystals := s.EndCrystalsInDimension(world.DimensionEnd)
	for i, off := range cardinalOffsets {
		tx := float64(off[0]) + 0.5
		tz := float64(off[1]) + 0.5
		var found int32
		for _, cr := range crystals {
			if math.Abs(cr.X-tx) <= 1.5 && math.Abs(cr.Z-tz) <= 1.5 &&
				cr.Y >= float64(portalY) && cr.Y <= float64(portalY+3) {
				found = cr.EntityID
				break
			}
		}
		if found == 0 {
			return [4]int32{}, false
		}
		matched[i] = found
	}
	return matched, true
}

// StartEnderDragonRespawnSequence begins the staged 100-tick Ender Dragon
// respawn ceremony when the dragon is dead and 4 End Crystals are placed on the
// cardinal edges of the Exit Portal:
//   - Deactivates the Exit Portal (`PlaceEndPodium(..., active=false)`).
//   - Points the 4 portal crystals' beams skyward to `(0, 128, 0)`.
//   - Sets `fight.Respawning = true` and `fight.RespawnTicks = 0` so
//     `StepEnderDragonRespawnSequence` (and `TickEnderDragons`) sequentially
//     rebuilds all 10 obsidian pillars and spawns the new Ender Dragon at tick 100.
func (s *Server) StartEnderDragonRespawnSequence() ([]int32, bool) {
	if s == nil || s.entities == nil {
		return nil, false
	}
	mgr := s.dragonManager()
	mgr.mu.RLock()
	alive := mgr.fight.DragonAlive
	respawning := mgr.fight.Respawning
	portalY := mgr.fight.PortalY
	mgr.mu.RUnlock()
	if alive || respawning {
		return nil, false
	}
	if portalY <= 0 {
		portalY = s.ResolveEndPortalY()
	}
	matched, ok := s.FindPortalRespawnCrystals()
	if !ok {
		return nil, false
	}

	mgr.mu.Lock()
	mgr.fight.Respawning = true
	mgr.fight.RespawnTicks = 0
	mgr.fight.RespawnCrystalIDs = append([]int32(nil), matched[:]...)
	mgr.mu.Unlock()

	if cache := s.DimensionChunks(world.DimensionEnd); cache != nil {
		changes := PlaceEndPodium(cache, portalY, false, false)
		s.broadcastDimensionBlockChanges(world.DimensionEnd, cache, changes, nil)
	}
	for _, cid := range matched {
		s.SetEndCrystalBeamTarget(cid, true, 0, EnderDragonRespawnSkyBeamY, 0)
	}
	return matched[:], true
}

// StepEnderDragonRespawnSequence advances an active Ender Dragon respawn
// ceremony by 1 tick:
//   - Ticks 10..89 (every 8 ticks): rebuilds the next obsidian spike top &
//     iron bars cage (`RebuildEndSpikeTop`), spawns its crystal, and points the
//     4 portal crystals' beams at that spike's top.
//   - Tick 100 (`EnderDragonRespawnDurationTicks`): removes the 4 portal
//     crystals, finishes regenerating all 10 spike crystals, and spawns the new
//     Ender Dragon!
func (s *Server) StepEnderDragonRespawnSequence() (spawnedDragonID int32, completed bool) {
	if s == nil || s.entities == nil {
		return 0, false
	}
	mgr := s.dragonManager()
	mgr.mu.Lock()
	if !mgr.fight.Respawning {
		mgr.mu.Unlock()
		return 0, false
	}
	mgr.fight.RespawnTicks++
	ticks := mgr.fight.RespawnTicks
	portalY := mgr.fight.PortalY
	if portalY <= 0 {
		portalY = EnderDragonDefaultPortalY
		mgr.fight.PortalY = portalY
	}
	portalCrystals := append([]int32(nil), mgr.fight.RespawnCrystalIDs...)
	mgr.mu.Unlock()

	spikes := world.EndSpikesForSeed(s.cfg.WorldSeed)
	if ticks >= 10 && ticks < 10+len(spikes)*EnderDragonRespawnSpikeIntervalTicks &&
		(ticks-10)%EnderDragonRespawnSpikeIntervalTicks == 0 {
		spikeIdx := (ticks - 10) / EnderDragonRespawnSpikeIntervalTicks
		if spikeIdx >= 0 && spikeIdx < len(spikes) {
			sp := spikes[spikeIdx]
			if cache := s.DimensionChunks(world.DimensionEnd); cache != nil {
				changes := RebuildEndSpikeTop(cache, sp)
				s.broadcastDimensionBlockChanges(world.DimensionEnd, cache, changes, nil)
			}
			for _, cid := range portalCrystals {
				s.SetEndCrystalBeamTarget(cid, true, sp.CenterX, sp.Height+1, sp.CenterZ)
			}
		}
	}

	if ticks < EnderDragonRespawnDurationTicks {
		return 0, false
	}

	// Ceremony finished at tick 100: consume the 4 portal crystals, regenerate all 10 spike crystals, and spawn the dragon!
	mgr.mu.Lock()
	for _, cid := range portalCrystals {
		delete(mgr.crystals, cid)
		s.entities.Remove(cid)
	}
	mgr.fight.Respawning = false
	mgr.fight.RespawnTicks = 0
	mgr.fight.RespawnCrystalIDs = nil
	mgr.mu.Unlock()

	if len(portalCrystals) > 0 {
		s.BroadcastDimensionChunk(world.DimensionEnd, 0, 0, protocol.PlayRemoveEntities,
			encodeRemoveEntitiesPacket(nil, portalCrystals))
	}

	s.SpawnEndSpikeCrystals()
	dragonID, err := s.SpawnEnderDragon(0.5, float64(portalY)+24.0, 0.5)
	if err != nil {
		return 0, false
	}
	return dragonID, true
}

// --- 3. Wither Multi-Head Mob Targeting, Wither Rose & Boss Immunities ---

// IsValidWitherTargetMob reports whether a mob of typeName is targeted by a
// Wither boss in vanilla 26.1.2 (all living mobs that are NOT undead, NOT
// `minecraft:wither`, and NOT `minecraft:ghast`).
func IsValidWitherTargetMob(typeName string) bool {
	norm := canonicalResourceName(typeName)
	if norm == "" || norm == MobTypeWither || norm == MobTypeEnderDragon || norm == "minecraft:ghast" ||
		norm == EntityTypeWitherSkull || norm == EntityTypeEndCrystal || norm == EntityTypeDragonFireball ||
		norm == EntityTypeAreaEffectCloud || norm == "minecraft:item" || norm == "minecraft:experience_orb" ||
		norm == "minecraft:armor_stand" {
		return false
	}
	return !IsUndeadMobType(norm)
}

// FindNearestWitherMobTarget finds the closest living non-undead mob within
// maxRange of `(x, y, z)` in dim, optionally excluding excludeEntityID.
func (s *Server) FindNearestWitherMobTarget(dim string, x, y, z, maxRange float64, excludeEntityID int32) *world.Entity {
	if s == nil || s.entities == nil || maxRange <= 0 {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	bestDistSq := maxRange * maxRange
	var best *world.Entity
	for _, ent := range s.entities.AppendAll(nil) {
		if ent.ID == excludeEntityID || ent.IsItem() || ent.EffectiveDimension() != normDim {
			continue
		}
		if !IsValidWitherTargetMob(ent.TypeName) {
			continue
		}
		if s.MobHealth(ent.ID) <= 0 {
			continue
		}
		dx := ent.X - x
		dy := ent.Y - y
		dz := ent.Z - z
		dSq := dx*dx + dy*dy + dz*dz
		if dSq <= bestDistSq {
			bestDistSq = dSq
			cp := ent
			best = &cp
		}
	}
	return best
}

// CanPlaceWitherRoseOn reports whether groundBlockName can support a
// `minecraft:wither_rose` block in vanilla 26.1.2.
func CanPlaceWitherRoseOn(groundBlockName string) bool {
	switch canonicalResourceName(groundBlockName) {
	case "minecraft:grass_block", "minecraft:dirt", "minecraft:coarse_dirt",
		"minecraft:podzol", "minecraft:mycelium", "minecraft:farmland",
		"minecraft:mud", "minecraft:rooted_dirt", "minecraft:moss_block",
		"minecraft:pale_moss_block", "minecraft:netherrack",
		"minecraft:soul_sand", "minecraft:soul_soil", "minecraft:end_stone":
		return true
	default:
		return false
	}
}

// OnMobKilledByWither handles the vanilla 26.1.2 `WitherBoss` / `WitherSkull`
// rule when a non-undead mob is killed by a Wither:
//   - If `mobGriefing` is enabled and the block at `(x, y, z)` is air above a
//     valid soil/netherrack/soul_sand/end_stone block, plants a
//     `minecraft:wither_rose` block at `(bx, by, bz)`.
//   - Otherwise spawns `1x minecraft:wither_rose` item entity at `(x, y+0.25, z)`.
func (s *Server) OnMobKilledByWither(dim string, x, y, z float64) (placedBlock bool, droppedItemID int32) {
	if s == nil {
		return false, 0
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	bx, by, bz := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))

	if cache != nil && s.MobGriefing() && s.CreeperBlockGriefing() {
		if roseState, ok := world.BlockStateByName(WitherRoseBlockID, nil); ok {
			if cache.GetBlock(bx, by, bz) == world.StateAir &&
				CanPlaceWitherRoseOn(world.BlockName(cache.GetBlock(bx, by-1, bz))) {
				cache.SetBlock(bx, by, bz, roseState)
				w := protocol.NewWriter(12)
				w.Position(bx, by, bz)
				w.VarInt(int32(roseState))
				s.BroadcastDimensionChunk(normDim, int32(bx>>4), int32(bz>>4), protocol.PlayBlockUpdate, w.Bytes())
				return true, 0
			}
		}
	}

	roseItem := world.NewNamedItemStack(WitherRoseItemID, 1)
	if !roseItem.IsEmpty() {
		id, _ := s.SpawnItemEntityInDimension(normDim, x, y+0.25, z, roseItem)
		return false, id
	}
	return false, 0
}

// ApplyWitherRoseContact applies the vanilla 26.1.2 `WitherRoseBlock.entityInside`
// effect (`1.0` wither damage + `40` ticks of `EffectWither` I) when a survival
// player or non-wither-immune mob stands inside a `minecraft:wither_rose` block.
func (s *Server) ApplyWitherRoseContact(session *PlayerSession, mobEntityID int32) bool {
	if s == nil || s.Difficulty() == DifficultyPeaceful {
		return false
	}
	if session != nil && !session.IsDead() {
		snap := session.Snapshot()
		if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator || snap.Health <= 0 {
			return false
		}
		s.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectWither, WitherRoseEffectDuration, 0))
		s.DamagePlayerWithEffects(session, WitherRoseContactDamage, "wither")
		return true
	}
	if mobEntityID > 0 && s.entities != nil {
		ent := s.entities.Get(mobEntityID)
		if ent == nil || !IsValidWitherTargetMob(ent.TypeName) {
			return false
		}
		s.ApplyEntityEffectScaled(mobEntityID, NewStatusEffectInstance(EffectWither, WitherRoseEffectDuration, 0), 1.0)
		s.applyDamageToMob(ent, WitherRoseContactDamage, nil, 0, false, false)
		return true
	}
	return false
}

// SetWitherCustomName sets a custom name on an active Wither boss and updates
// its purple boss bar title to match.
func (s *Server) SetWitherCustomName(witherID int32, customName string) bool {
	if s == nil {
		return false
	}
	st, ok := s.GetWitherState(witherID)
	if !ok {
		return false
	}
	comp := ChatComponent{Text: customName}
	if strings.TrimSpace(customName) == "" {
		comp = ChatComponent{Translate: "entity.minecraft.wither"}
	}
	return s.UpdateRuntimeBossBarName(st.BossBarUUID, comp)
}

// IsBossImmuneToStatusEffect reports whether a boss entity (`minecraft:wither`
// or `minecraft:ender_dragon`) is immune to a status effect in vanilla 26.1.2
// (both bosses are completely immune to all status effects).
func IsBossImmuneToStatusEffect(entityTypeName string, _ string) bool {
	switch canonicalResourceName(entityTypeName) {
	case MobTypeWither, MobTypeEnderDragon:
		return true
	default:
		return false
	}
}

// --- 4. Ender Dragon Block Destruction, Part Damage, Wing Knockback & Dragon Egg ---

// IsDragonImmuneBlock reports whether blockName is in `#minecraft:dragon_immune`
// in vanilla 26.1.2 (blocks that the Ender Dragon cannot destroy on contact).
func IsDragonImmuneBlock(blockName string) bool {
	switch canonicalResourceName(blockName) {
	case "minecraft:air", "minecraft:cave_air", "minecraft:void_air",
		"minecraft:bedrock", "minecraft:end_stone", "minecraft:obsidian",
		"minecraft:crying_obsidian", "minecraft:iron_bars",
		"minecraft:end_portal", "minecraft:end_portal_frame", "minecraft:end_gateway",
		"minecraft:command_block", "minecraft:repeating_command_block", "minecraft:chain_command_block",
		"minecraft:barrier", "minecraft:structure_block", "minecraft:jigsaw",
		"minecraft:moving_piston", "minecraft:reinforced_deepslate",
		"minecraft:respawn_anchor", "minecraft:dragon_egg", "minecraft:fire",
		"minecraft:soul_fire", "minecraft:light":
		return true
	default:
		return false
	}
}

// DestroyDragonCollidingBlocks destroys any non-`dragon_immune` blocks
// intersecting the Ender Dragon's bounding box around `(dragonX, dragonY, dragonZ)`
// in `minecraft:the_end` when `mobGriefing` is enabled (vanilla `EnderDragon.checkWalls`).
func (s *Server) DestroyDragonCollidingBlocks(dragonX, dragonY, dragonZ float64) int {
	if s == nil || !s.MobGriefing() || !s.CreeperBlockGriefing() {
		return 0
	}
	cache := s.DimensionChunks(world.DimensionEnd)
	if cache == nil {
		return 0
	}
	bx0 := int(math.Floor(dragonX))
	by0 := int(math.Floor(dragonY))
	bz0 := int(math.Floor(dragonZ))

	var changes []world.BlockChange
	for dy := 0; dy <= 3; dy++ {
		by := by0 + dy
		if by <= world.MinY+1 || by >= world.MinY+world.WorldHeight-1 {
			continue
		}
		for dx := -2; dx <= 2; dx++ {
			for dz := -2; dz <= 2; dz++ {
				bx, bz := bx0+dx, bz0+dz
				st := cache.GetBlock(bx, by, bz)
				if st == world.StateAir || IsDragonImmuneBlock(world.BlockName(st)) {
					continue
				}
				cache.SetBlock(bx, by, bz, world.StateAir)
				changes = append(changes, world.BlockChange{
					Pos:      world.BlockPos{X: bx, Y: by, Z: bz},
					NewState: world.StateAir,
				})
			}
		}
	}
	if len(changes) > 0 {
		s.broadcastDimensionBlockChanges(world.DimensionEnd, cache, changes, nil)
	}
	return len(changes)
}

// ScaleEnderDragonPartDamage scales incoming damage against a specific Ender
// Dragon sub-hitbox part matching vanilla `EnderDragon.hurt(EnderDragonPart, ...)`:
//   - `"head"`: full damage (`1.0x`)
//   - `"neck"`, `"body"`, `"tail"`, `"wing"`: `damage * 0.25 + 1.0`
func ScaleEnderDragonPartDamage(damage float32, partName string) float32 {
	if damage <= 0 {
		return 0
	}
	switch strings.ToLower(strings.TrimSpace(partName)) {
	case "", "head":
		return damage
	default:
		return damage*0.25 + 1.0
	}
}

// TeleportDragonEggBlock teleports a `minecraft:dragon_egg` block at `(x, y, z)`
// in dim to a random air position within `±15` blocks horizontally and `±7`
// blocks vertically (matching vanilla `DragonEggBlock.teleport`).
func (s *Server) TeleportDragonEggBlock(dim string, x, y, z int, rng *rand.Rand) (world.BlockPos, bool) {
	if s == nil {
		return world.BlockPos{}, false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return world.BlockPos{}, false
	}
	curState := cache.GetBlock(x, y, z)
	if canonicalResourceName(world.BlockName(curState)) != DragonEggBlockID {
		return world.BlockPos{}, false
	}
	eggState, ok := world.BlockStateByName(DragonEggBlockID, nil)
	if !ok {
		eggState = curState
	}

	for attempt := 0; attempt < 1000; attempt++ {
		var dx, dy, dz int
		if rng != nil {
			dx = rng.Intn(31) - 15
			dy = rng.Intn(15) - 7
			dz = rng.Intn(31) - 15
		} else {
			dx = ((attempt * 7) % 31) - 15
			dy = ((attempt * 3) % 15) - 7
			dz = ((attempt*11 + 5) % 31) - 15
		}
		if dx == 0 && dy == 0 && dz == 0 {
			continue
		}
		tx, ty, tz := x+dx, y+dy, z+dz
		if ty <= world.MinY+1 || ty >= world.MinY+world.WorldHeight-1 {
			continue
		}
		if cache.GetBlock(tx, ty, tz) == world.StateAir {
			cache.SetBlock(x, y, z, world.StateAir)
			cache.SetBlock(tx, ty, tz, eggState)
			changes := []world.BlockChange{
				{Pos: world.BlockPos{X: x, Y: y, Z: z}, NewState: world.StateAir},
				{Pos: world.BlockPos{X: tx, Y: ty, Z: tz}, NewState: eggState},
			}
			s.broadcastDimensionBlockChanges(normDim, cache, changes, nil)
			return world.BlockPos{X: tx, Y: ty, Z: tz}, true
		}
	}
	return world.BlockPos{}, false
}
