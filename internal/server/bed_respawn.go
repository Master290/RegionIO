package server

import (
	"math"
	"strconv"
	"strings"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Vanilla 26.1.2 bed, respawn anchor, and spawn-point constants and messages.
const (
	BedMaxInteractHorizontalDist    = 3.5
	BedMaxInteractVerticalDist      = 2.5
	BedMonsterCheckHorizontalRadius = 8.0
	BedMonsterCheckVerticalRadius   = 5.0
	RespawnPointExplosionRadius     = 5.0
	MaxRespawnAnchorCharges         = 4

	MsgNoHomeBedOrAnchor = "You have no home bed or charged respawn anchor, or it was obstructed"
	MsgRespawnPointSet   = "Respawn point set"
	MsgSleepOnlyAtNight  = "You can sleep only at night or during thunderstorms"
	MsgBedTooFarAway     = "You may not rest now; the bed is too far away"
	MsgBedMonstersNearby = "You may not rest now; there are monsters nearby"
	MsgBedOccupied       = "This bed is occupied"
)

type worldSpawnConfig struct {
	mu     sync.RWMutex
	custom bool
	x, y, z int
	yaw    float32
}

type bedOccupancyEntry struct {
	dim        string
	headPos    world.BlockPos
	footPos    world.BlockPos
	hasPartner bool
}

var (
	serverWorldSpawns sync.Map // map[*Server]*worldSpawnConfig
	playerActiveBeds  sync.Map // map[*PlayerSession]bedOccupancyEntry
)

func getWorldSpawnConfig(s *Server) *worldSpawnConfig {
	if s == nil {
		return &worldSpawnConfig{x: 8, y: world.FlatSurfaceY + 1, z: 8}
	}
	if v, ok := serverWorldSpawns.Load(s); ok {
		return v.(*worldSpawnConfig)
	}
	actual, _ := serverWorldSpawns.LoadOrStore(s, &worldSpawnConfig{})
	return actual.(*worldSpawnConfig)
}

// WorldSpawn returns the server's default overworld spawn coordinates (x, y, z)
// and yaw angle. If not explicitly overridden via SetWorldSpawn, it resolves a
// safe surface Y at (8, 8).
func (s *Server) WorldSpawn() (x, y, z int, yaw float32) {
	cfg := getWorldSpawnConfig(s)
	cfg.mu.RLock()
	if cfg.custom {
		x, y, z, yaw = cfg.x, cfg.y, cfg.z, cfg.yaw
		cfg.mu.RUnlock()
		return x, y, z, yaw
	}
	cfg.mu.RUnlock()

	wx, wz := 8, 8
	wy := world.FlatSurfaceY + 1
	if s != nil && s.chunks != nil {
		if safeY, ok := s.chunks.SafeSpawnY(wx, wz); ok {
			wy = safeY
		} else {
			wy = world.SeaLevel + 1
		}
	}
	return wx, wy, wz, 0
}

// SetWorldSpawn updates the server's default overworld spawn coordinates and
// yaw angle, broadcasting PlayDefaultSpawnPos to online players without a
// personal spawn point.
func (s *Server) SetWorldSpawn(x, y, z int, yaw float32) {
	if s == nil {
		return
	}
	cfg := getWorldSpawnConfig(s)
	cfg.mu.Lock()
	cfg.custom = true
	cfg.x = x
	cfg.y = y
	cfg.z = z
	cfg.yaw = yaw
	cfg.mu.Unlock()

	pkt := EncodeDefaultSpawnPosPacket(world.DimensionOverworld, x, y, z, yaw, 0)
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, p := range s.players {
		sessions = append(sessions, p)
	}
	s.playersMu.RUnlock()

	for _, p := range sessions {
		if p == nil || p.send == nil {
			continue
		}
		if !p.Snapshot().HasSpawn {
			_ = p.send(protocol.PlayDefaultSpawnPos, pkt)
		}
	}
	_ = s.SaveWorldSettingsFile()
}

// EncodeDefaultSpawnPosPacket encodes a protocol 775 clientbound
// set_default_spawn_position (0x5f) packet body (GlobalPos + yaw + pitch).
func EncodeDefaultSpawnPosPacket(dim string, x, y, z int, yaw, pitch float32) []byte {
	w := protocol.NewWriter(40)
	w.String(world.NormalizeDimension(dim))
	w.Position(x, y, z)
	w.Float32(yaw)
	w.Float32(pitch)
	return w.Bytes()
}

// SetPlayerDimensionSpawnPoint sets session's personal respawn point in dim and
// sends PlayDefaultSpawnPos to the client.
func (s *Server) SetPlayerDimensionSpawnPoint(session *PlayerSession, dim string, x, y, z int32, yaw, pitch float32, forced bool) {
	if session == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	session.mu.Lock()
	session.spawnX = x
	session.spawnY = y
	session.spawnZ = z
	session.spawnYaw = yaw
	session.spawnPitch = pitch
	session.spawnForced = forced
	session.spawnDimension = normDim
	session.hasSpawn = true
	send := session.send
	session.mu.Unlock()

	if send != nil {
		_ = send(protocol.PlayDefaultSpawnPos, EncodeDefaultSpawnPosPacket(normDim, int(x), int(y), int(z), yaw, pitch))
	}
}

// ClearPlayerSpawnPoint clears session's personal respawn point so the player
// falls back to the world default spawn.
func (s *Server) ClearPlayerSpawnPoint(session *PlayerSession) {
	if session == nil {
		return
	}
	session.mu.Lock()
	session.hasSpawn = false
	session.spawnForced = false
	send := session.send
	session.mu.Unlock()

	if send != nil && s != nil {
		wx, wy, wz, wyaw := s.WorldSpawn()
		_ = send(protocol.PlayDefaultSpawnPos, EncodeDefaultSpawnPosPacket(world.DimensionOverworld, wx, wy, wz, wyaw, 0))
	}
}

// HorizontalFacingFromYaw converts a player's yaw angle (degrees) into the
// cardinal horizontal facing direction ("south", "west", "north", "east")
// matching vanilla Direction.fromYRot.
func HorizontalFacingFromYaw(yaw float32) string {
	idx := int(math.Floor(float64(yaw)/90.0+0.5)) & 3
	switch idx {
	case 0:
		return "south"
	case 1:
		return "west"
	case 2:
		return "north"
	default:
		return "east"
	}
}

// HorizontalFacingOffset returns the (dx, dz) unit step for a horizontal
// cardinal direction ("north", "south", "west", "east").
func HorizontalFacingOffset(facing string) (dx, dz int) {
	switch strings.ToLower(strings.TrimSpace(facing)) {
	case "north":
		return 0, -1
	case "south":
		return 0, 1
	case "west":
		return -1, 0
	case "east":
		return 1, 0
	default:
		return 0, 1
	}
}

// BedStateInfo extracts the block name, facing ("north"/"south"/"west"/"east"),
// part ("foot"/"head"), and occupied flag from a bed block state ID.
func BedStateInfo(state uint16) (name, facing, part string, occupied, ok bool) {
	if !IsBedBlock(state) {
		return "", "", "", false, false
	}
	bName, props, found := world.BlockStateProperties(state)
	if !found || !strings.HasSuffix(bName, "_bed") {
		return "", "", "", false, false
	}
	facing = props["facing"]
	if facing == "" {
		facing = "north"
	}
	part = props["part"]
	if part == "" {
		part = "foot"
	}
	occupied = props["occupied"] == "true"
	return bName, facing, part, occupied, true
}

// BedBlockState resolves the block state ID for a bed block (e.g. "minecraft:red_bed")
// with the given facing, part ("foot" or "head"), and occupied flag.
func BedBlockState(blockName, facing, part string, occupied bool) (uint16, bool) {
	if !strings.Contains(blockName, ":") {
		blockName = "minecraft:" + blockName
	}
	if !strings.HasSuffix(blockName, "_bed") {
		return 0, false
	}
	switch facing {
	case "north", "south", "west", "east":
	default:
		facing = "north"
	}
	if part != "head" {
		part = "foot"
	}
	occStr := "false"
	if occupied {
		occStr = "true"
	}
	return world.BlockStateByName(blockName, map[string]string{
		"facing":   facing,
		"part":     part,
		"occupied": occStr,
	})
}

func isReplaceableForBedPlacement(state uint16) bool {
	if state == world.StateAir || state == world.StateWater {
		return true
	}
	switch world.BlockName(state) {
	case "minecraft:air", "minecraft:cave_air", "minecraft:void_air",
		"minecraft:water", "minecraft:short_grass", "minecraft:fern",
		"minecraft:dead_bush", "minecraft:tall_grass", "minecraft:large_fern",
		"minecraft:fire", "minecraft:soul_fire", "minecraft:vine", "minecraft:glow_lichen":
		return true
	case "minecraft:snow":
		_, props, ok := world.BlockStateProperties(state)
		return ok && props["layers"] == "1"
	default:
		return false
	}
}

// PlaceBedBlock places a two-part bed (part=foot at (x, y, z) and part=head one
// block further in the player's horizontal facing direction) if both cells are
// replaceable and within vertical world bounds.
func (s *Server) PlaceBedBlock(session *PlayerSession, x, y, z int, baseState uint16) bool {
	if s == nil || !IsBedBlock(baseState) {
		return false
	}
	dim := world.DimensionOverworld
	yaw := float32(0)
	if session != nil {
		snap := session.Snapshot()
		dim = world.NormalizeDimension(snap.Dimension)
		yaw = snap.Yaw
	}
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return false
	}
	dt, _ := world.LookupDimensionType(dim)
	minY := dt.MinY
	maxY := dt.MinY + dt.Height
	if dt.Height <= 0 {
		minY = world.MinY
		maxY = world.MinY + world.WorldHeight
	}
	if y < minY || y >= maxY {
		return false
	}

	facing := HorizontalFacingFromYaw(yaw)
	dx, dz := HorizontalFacingOffset(facing)
	hx, hy, hz := x+dx, y, z+dz

	if !isReplaceableForBedPlacement(cache.GetBlock(x, y, z)) || !isReplaceableForBedPlacement(cache.GetBlock(hx, hy, hz)) {
		return false
	}

	bedName := world.BlockName(baseState)
	footState, okFoot := BedBlockState(bedName, facing, "foot", false)
	headState, okHead := BedBlockState(bedName, facing, "head", false)
	if !okFoot || !okHead {
		return false
	}

	validFoot, lightFoot := cache.SetBlockWithLight(x, y, z, footState)
	if !validFoot {
		return false
	}
	validHead, lightHead := cache.SetBlockWithLight(hx, hy, hz, headState)
	if !validHead {
		_, _ = cache.SetBlockWithLight(x, y, z, world.StateAir)
		return false
	}

	changes := []world.BlockChange{
		{Pos: world.BlockPos{X: x, Y: y, Z: z}, NewState: footState},
		{Pos: world.BlockPos{X: hx, Y: hy, Z: hz}, NewState: headState},
	}
	seenLight := make(map[world.ChunkPos]bool, len(lightFoot)+len(lightHead))
	var lightChunks []world.ChunkPos
	for _, cp := range append(lightFoot, lightHead...) {
		if !seenLight[cp] {
			seenLight[cp] = true
			lightChunks = append(lightChunks, cp)
		}
	}
	s.broadcastDimensionBlockChanges(dim, cache, changes, lightChunks)
	return true
}

// ResolveBedPositions resolves the canonical head position and optional foot
// partner position for a bed block at (x, y, z). If a single bed block was placed
// in a test without a partner half, headPos is (x, y, z) and hasPartner is false.
func ResolveBedPositions(cache *world.Cache, x, y, z int) (headPos, footPos world.BlockPos, hasPartner bool) {
	clicked := world.BlockPos{X: x, Y: y, Z: z}
	if cache == nil {
		return clicked, clicked, false
	}
	state := cache.GetBlock(x, y, z)
	_, facing, part, _, ok := BedStateInfo(state)
	if !ok {
		return clicked, clicked, false
	}
	dx, dz := HorizontalFacingOffset(facing)
	if part == "head" {
		candFoot := world.BlockPos{X: x - dx, Y: y, Z: z - dz}
		if _, _, cPart, _, cOK := BedStateInfo(cache.GetBlock(candFoot.X, candFoot.Y, candFoot.Z)); cOK && cPart == "foot" {
			return clicked, candFoot, true
		}
		return clicked, clicked, false
	}
	// part == "foot": head should be at (x+dx, y, z+dz).
	candHead := world.BlockPos{X: x + dx, Y: y, Z: z + dz}
	if _, _, cPart, _, cOK := BedStateInfo(cache.GetBlock(candHead.X, candHead.Y, candHead.Z)); cOK && cPart == "head" {
		return candHead, clicked, true
	}
	return clicked, clicked, false
}

// BreakBedPartner removes the other half of a two-part bed when one half at
// (x, y, z) with brokenState is broken, and wakes any player sleeping in it.
func (s *Server) BreakBedPartner(dim string, cache *world.Cache, x, y, z int, brokenState uint16) {
	if s == nil || cache == nil {
		return
	}
	_, facing, part, _, ok := BedStateInfo(brokenState)
	if !ok {
		return
	}
	s.WakePlayersInBedAt(dim, x, y, z)

	dx, dz := HorizontalFacingOffset(facing)
	var px, py, pz int
	var wantPart string
	if part == "foot" {
		px, py, pz = x+dx, y, z+dz
		wantPart = "head"
	} else {
		px, py, pz = x-dx, y, z-dz
		wantPart = "foot"
	}
	partnerState := cache.GetBlock(px, py, pz)
	if _, _, pPart, _, pOK := BedStateInfo(partnerState); pOK && pPart == wantPart {
		s.WakePlayersInBedAt(dim, px, py, pz)
		if broken, upd := cache.BreakBlockWithUpdates(px, py, pz); broken {
			s.broadcastDimensionBlockChanges(dim, cache, upd.Changes, upd.LightChanged)
		}
	}
}

func (s *Server) setBedOccupiedState(dim string, cache *world.Cache, headPos, footPos world.BlockPos, hasPartner, occupied bool) {
	if s == nil || cache == nil {
		return
	}
	var changes []world.BlockChange
	var lightChunks []world.ChunkPos
	seenLight := make(map[world.ChunkPos]bool)

	updateOne := func(pos world.BlockPos) {
		st := cache.GetBlock(pos.X, pos.Y, pos.Z)
		name, facing, part, curOcc, ok := BedStateInfo(st)
		if !ok || curOcc == occupied {
			return
		}
		nextState, ok := BedBlockState(name, facing, part, occupied)
		if !ok {
			return
		}
		if valid, lc := cache.SetBlockWithLight(pos.X, pos.Y, pos.Z, nextState); valid {
			changes = append(changes, world.BlockChange{Pos: pos, OldState: st, NewState: nextState})
			for _, cp := range lc {
				if !seenLight[cp] {
					seenLight[cp] = true
					lightChunks = append(lightChunks, cp)
				}
			}
		}
	}

	updateOne(headPos)
	if hasPartner {
		updateOne(footPos)
	}
	if len(changes) > 0 {
		s.broadcastDimensionBlockChanges(dim, cache, changes, lightChunks)
	}
}

// ClearPlayerBedOccupancy clears the occupied=true block state on any bed
// currently occupied by session.
func (s *Server) ClearPlayerBedOccupancy(session *PlayerSession) {
	if s == nil || session == nil {
		return
	}
	v, ok := playerActiveBeds.LoadAndDelete(session)
	if !ok {
		return
	}
	entry := v.(bedOccupancyEntry)
	if cache := s.DimensionChunks(entry.dim); cache != nil {
		s.setBedOccupiedState(entry.dim, cache, entry.headPos, entry.footPos, entry.hasPartner, false)
	}
}

// ClearAllBedOccupancy clears occupied=true on all beds tracked for sleeping players.
func (s *Server) ClearAllBedOccupancy() {
	if s == nil {
		return
	}
	playerActiveBeds.Range(func(key, value any) bool {
		sess, _ := key.(*PlayerSession)
		entry, _ := value.(bedOccupancyEntry)
		playerActiveBeds.Delete(key)
		if sess != nil {
			if cache := s.DimensionChunks(entry.dim); cache != nil {
				s.setBedOccupiedState(entry.dim, cache, entry.headPos, entry.footPos, entry.hasPartner, false)
			}
		}
		return true
	})
}

// WakePlayerFromBed wakes session from sleeping and clears their bed's occupied state.
func (s *Server) WakePlayerFromBed(session *PlayerSession) {
	if s == nil || session == nil {
		return
	}
	s.SetPlayerSleeping(session, false)
	s.ClearPlayerBedOccupancy(session)
}

// WakePlayersInBedAt wakes any player currently sleeping in the bed at (x, y, z) in dim.
func (s *Server) WakePlayersInBedAt(dim string, x, y, z int) {
	if s == nil {
		return
	}
	dim = world.NormalizeDimension(dim)
	target := world.BlockPos{X: x, Y: y, Z: z}
	playerActiveBeds.Range(func(key, value any) bool {
		sess, _ := key.(*PlayerSession)
		entry, _ := value.(bedOccupancyEntry)
		if sess != nil && entry.dim == dim && (entry.headPos == target || (entry.hasPartner && entry.footPos == target)) {
			s.WakePlayerFromBed(sess)
		}
		return true
	})
}

// IsBedOccupied reports whether the bed at headPos in dim is currently occupied
// by another sleeping player (or has occupied=true in its block state). Stale
// occupancy from players who have already woken up is automatically cleared.
func (s *Server) IsBedOccupied(session *PlayerSession, dim string, headPos, footPos world.BlockPos, hasPartner bool) bool {
	if s == nil {
		return false
	}
	dim = world.NormalizeDimension(dim)
	occupiedByOther := false
	occupiedByStale := false

	playerActiveBeds.Range(func(key, value any) bool {
		other, _ := key.(*PlayerSession)
		entry, _ := value.(bedOccupancyEntry)
		if other == nil || entry.dim != dim {
			return true
		}
		sameBed := entry.headPos == headPos || (hasPartner && entry.headPos == footPos) || (entry.hasPartner && entry.footPos == headPos)
		if !sameBed {
			return true
		}
		if !s.IsPlayerSleeping(other) || other.IsDead() {
			playerActiveBeds.Delete(other)
			occupiedByStale = true
			return true
		}
		if other != session {
			occupiedByOther = true
			return false
		}
		return true
	})
	if occupiedByOther {
		return true
	}

	cache := s.DimensionChunks(dim)
	if cache == nil {
		return false
	}
	if occupiedByStale {
		s.setBedOccupiedState(dim, cache, headPos, footPos, hasPartner, false)
		return false
	}
	_, _, _, occ, ok := BedStateInfo(cache.GetBlock(headPos.X, headPos.Y, headPos.Z))
	if ok && occ {
		// Check if session itself is the one sleeping in it.
		if v, exists := playerActiveBeds.Load(session); exists {
			if entry := v.(bedOccupancyEntry); entry.dim == dim && entry.headPos == headPos {
				return false
			}
		}
		return true
	}
	return false
}

// IsHostileMonsterType reports whether entityType is a hostile monster that
// prevents sleeping when near a bed.
func IsHostileMonsterType(entityType string) bool {
	switch canonicalResourceName(entityType) {
	case MobTypeZombie, MobTypeSkeleton, MobTypeCreeper, MobTypeSpider,
		"minecraft:enderman", "minecraft:witch", "minecraft:slime",
		"minecraft:phantom", "minecraft:drowned", "minecraft:husk",
		"minecraft:stray", "minecraft:pillager", "minecraft:vindicator",
		"minecraft:evoker", "minecraft:ravager", "minecraft:vex",
		"minecraft:blaze", "minecraft:ghast", "minecraft:wither_skeleton",
		"minecraft:piglin_brute", "minecraft:zombified_piglin", "minecraft:warden":
		return true
	default:
		return false
	}
}

// HasHostileMonstersNearBed reports whether any living hostile monster in dim is
// within ±8 blocks horizontally and ±5 blocks vertically of (headX, headY, headZ),
// matching vanilla ServerPlayer.startSleepInBed.
func (s *Server) HasHostileMonstersNearBed(dim string, headX, headY, headZ int) bool {
	if s == nil || s.entities == nil {
		return false
	}
	dim = world.NormalizeDimension(dim)
	centerX := float64(headX) + 0.5
	centerY := float64(headY) + 0.5
	centerZ := float64(headZ) + 0.5
	cx := int32(headX >> 4)
	cz := int32(headZ >> 4)

	nearby := s.entities.AppendInChunkRange(nil, cx, cz, 1)
	for i := range nearby {
		ent := &nearby[i]
		if ent.IsItem() || ent.EffectiveDimension() != dim {
			continue
		}
		if !IsHostileMonsterType(ent.TypeName) {
			continue
		}
		if math.Abs(ent.X-centerX) <= BedMonsterCheckHorizontalRadius &&
			math.Abs(ent.Z-centerZ) <= BedMonsterCheckHorizontalRadius &&
			math.Abs(ent.Y-centerY) <= BedMonsterCheckVerticalRadius {
			return true
		}
	}
	return false
}

// IsBedInRange reports whether a player at (px, py, pz) is within vanilla bed
// interaction range (3 blocks horizontally, 2 blocks vertically) of either the
// clicked bed block or the bed head block.
func IsBedInRange(px, py, pz float64, clickX, clickY, clickZ, headX, headY, headZ int) bool {
	inBox := func(bx, by, bz int) bool {
		return math.Abs(px-(float64(bx)+0.5)) <= BedMaxInteractHorizontalDist &&
			math.Abs(py-(float64(by)+0.5)) <= BedMaxInteractVerticalDist &&
			math.Abs(pz-(float64(bz)+0.5)) <= BedMaxInteractHorizontalDist
	}
	return inBox(headX, headY, headZ) || inBox(clickX, clickY, clickZ)
}

// InteractBed handles right-clicking a bed block at (x, y, z) for session:
//   - In dimensions where BedWorks is false (Nether / End), removes the bed and
//     triggers a 5.0-power explosion ("was killed by [Intentional Game Design]").
//   - In the Overworld, checks reach distance and occupancy, sets the player's
//     respawn point to the bed head, verifies night/thunderstorm and nearby
//     hostile monsters, marks the bed occupied, and puts the player to sleep.
func (s *Server) InteractBed(session *PlayerSession, x, y, z int) (slept, skippedToMorning bool) {
	if s == nil || session == nil || session.IsDead() {
		return false, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return false, false
	}
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return false, false
	}
	state := cache.GetBlock(x, y, z)
	if !IsBedBlock(state) {
		return false, false
	}

	headPos, footPos, hasPartner := ResolveBedPositions(cache, x, y, z)

	// 1. Dimension check: beds explode in the Nether and the End.
	dt, _ := world.LookupDimensionType(dim)
	if !dt.BedWorks {
		s.WakePlayersInBedAt(dim, headPos.X, headPos.Y, headPos.Z)
		if broken, upd := cache.BreakBlockWithUpdates(headPos.X, headPos.Y, headPos.Z); broken {
			s.broadcastDimensionBlockChanges(dim, cache, upd.Changes, upd.LightChanged)
		}
		if hasPartner {
			if broken, upd := cache.BreakBlockWithUpdates(footPos.X, footPos.Y, footPos.Z); broken {
				s.broadcastDimensionBlockChanges(dim, cache, upd.Changes, upd.LightChanged)
			}
		}
		s.TriggerRespawnPointExplosion(session, dim, headPos.X, headPos.Y, headPos.Z)
		return false, false
	}

	// 2. Range check (skip only when player transform was never initialized from (0,0,0)).
	if (snap.X != 0 || snap.Y != 0 || snap.Z != 0) &&
		!IsBedInRange(snap.X, snap.Y, snap.Z, x, y, z, headPos.X, headPos.Y, headPos.Z) {
		_ = s.SendSystemChat(session, MsgBedTooFarAway)
		return false, false
	}

	// 3. Occupied check.
	if s.IsBedOccupied(session, dim, headPos, footPos, hasPartner) {
		_ = s.SendSystemChat(session, MsgBedOccupied)
		return false, false
	}

	// 4. Set respawn point to the bed head position (even during daytime).
	spawnChanged := !snap.HasSpawn ||
		snap.SpawnX != int32(headPos.X) ||
		snap.SpawnY != int32(headPos.Y) ||
		snap.SpawnZ != int32(headPos.Z) ||
		world.NormalizeDimension(snap.SpawnDimension) != dim ||
		snap.SpawnForced
	s.SetPlayerDimensionSpawnPoint(session, dim, int32(headPos.X), int32(headPos.Y), int32(headPos.Z), snap.Yaw, snap.Pitch, false)
	if spawnChanged {
		_ = s.SendSystemChat(session, MsgRespawnPointSet)
	}

	// 5. Night / thunderstorm check.
	if !s.CanSleep() {
		_ = s.SendSystemChat(session, MsgSleepOnlyAtNight)
		return false, false
	}

	// 6. Nearby hostile monsters check (in survival/adventure mode).
	if snap.GameMode != GameModeCreative && s.HasHostileMonstersNearBed(dim, headPos.X, headPos.Y, headPos.Z) {
		_ = s.SendSystemChat(session, MsgBedMonstersNearby)
		return false, false
	}

	// 7. Mark bed occupied and enter sleep.
	s.ClearPlayerBedOccupancy(session)
	s.setBedOccupiedState(dim, cache, headPos, footPos, hasPartner, true)
	playerActiveBeds.Store(session, bedOccupancyEntry{
		dim:        dim,
		headPos:    headPos,
		footPos:    footPos,
		hasPartner: hasPartner,
	})

	s.RecordPlayerSleepInBed(session)
	skipped := s.SetPlayerSleeping(session, true)
	if skipped {
		s.ClearAllBedOccupancy()
	}
	return true, skipped
}

// --- Respawn Anchor (minecraft:respawn_anchor) ---

// IsRespawnAnchorBlock reports whether state is a minecraft:respawn_anchor block.
func IsRespawnAnchorBlock(state uint16) bool {
	if state == world.StateAir || state == world.StateWater {
		return false
	}
	return world.BlockName(state) == "minecraft:respawn_anchor"
}

// RespawnAnchorCharges returns the current charge level (0..4) of a
// minecraft:respawn_anchor block state.
func RespawnAnchorCharges(state uint16) int {
	name, props, ok := world.BlockStateProperties(state)
	if !ok || name != "minecraft:respawn_anchor" {
		return 0
	}
	c, err := strconv.Atoi(props["charges"])
	if err != nil || c < 0 {
		return 0
	}
	if c > MaxRespawnAnchorCharges {
		return MaxRespawnAnchorCharges
	}
	return c
}

// RespawnAnchorState resolves the block state ID for minecraft:respawn_anchor
// with charges clamped to [0, 4].
func RespawnAnchorState(charges int) (uint16, bool) {
	if charges < 0 {
		charges = 0
	}
	if charges > MaxRespawnAnchorCharges {
		charges = MaxRespawnAnchorCharges
	}
	return world.BlockStateByName("minecraft:respawn_anchor", map[string]string{
		"charges": strconv.Itoa(charges),
	})
}

// IsGlowstoneItem reports whether stack is a minecraft:glowstone block item.
func IsGlowstoneItem(stack world.ItemStack) bool {
	return !stack.IsEmpty() && stack.ResolvedID() == "minecraft:glowstone"
}

// ChargeRespawnAnchor increments the charge of the respawn anchor at (x, y, z)
// in dim by 1 (up to 4), updates block light, and grants the
// "minecraft:nether/charge_respawn_anchor" advancement when charges reach 4.
func (s *Server) ChargeRespawnAnchor(session *PlayerSession, dim string, x, y, z int) (newCharges int, ok bool) {
	if s == nil {
		return 0, false
	}
	dim = world.NormalizeDimension(dim)
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return 0, false
	}
	state := cache.GetBlock(x, y, z)
	if !IsRespawnAnchorBlock(state) {
		return 0, false
	}
	charges := RespawnAnchorCharges(state)
	if charges >= MaxRespawnAnchorCharges {
		return charges, false
	}
	newCharges = charges + 1
	nextState, ok := RespawnAnchorState(newCharges)
	if !ok {
		return charges, false
	}
	valid, lightChunks := cache.SetBlockWithLight(x, y, z, nextState)
	if !valid {
		return charges, false
	}
	s.broadcastDimensionBlockChanges(dim, cache, []world.BlockChange{
		{Pos: world.BlockPos{X: x, Y: y, Z: z}, OldState: state, NewState: nextState},
	}, lightChunks)

	cx := int32(x >> 4)
	cz := int32(z >> 4)
	s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:block.respawn_anchor.charge", SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))

	if newCharges == MaxRespawnAnchorCharges && session != nil {
		s.GrantCriterion(session, "minecraft:nether/charge_respawn_anchor", "charge_respawn_anchor")
	}
	return newCharges, true
}

// InteractRespawnAnchor handles right-clicking a minecraft:respawn_anchor at
// (x, y, z) for session:
//   - If holding minecraft:glowstone and charges < 4: consumes 1 glowstone in
//     survival mode and increments charges (0..4).
//   - Otherwise, if charges == 0: returns false (no interaction).
//   - Otherwise (charges > 0): in dimensions where RespawnAnchorWorks is true
//     (the Nether), sets the player's respawn point to (x, y, z); outside the
//     Nether, removes the anchor and triggers a 5.0-power explosion.
func (s *Server) InteractRespawnAnchor(session *PlayerSession, x, y, z int) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return false
	}
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	if !IsRespawnAnchorBlock(state) {
		return false
	}
	charges := RespawnAnchorCharges(state)

	if IsGlowstoneItem(session.HeldItem()) && charges < MaxRespawnAnchorCharges {
		if snap.GameMode == GameModeSurvival {
			if _, _, consumed := s.ConsumeHeldItem(session, 1); !consumed {
				return false
			}
		}
		_, ok := s.ChargeRespawnAnchor(session, dim, x, y, z)
		return ok
	}

	if charges <= 0 {
		return false
	}

	dt, _ := world.LookupDimensionType(dim)
	if dt.RespawnAnchorWorks {
		spawnChanged := !snap.HasSpawn ||
			snap.SpawnX != int32(x) ||
			snap.SpawnY != int32(y) ||
			snap.SpawnZ != int32(z) ||
			world.NormalizeDimension(snap.SpawnDimension) != dim ||
			snap.SpawnForced
		s.SetPlayerDimensionSpawnPoint(session, dim, int32(x), int32(y), int32(z), snap.Yaw, snap.Pitch, false)
		if spawnChanged {
			_ = s.SendSystemChat(session, MsgRespawnPointSet)
		}
		cx := int32(x >> 4)
		cz := int32(z >> 4)
		s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:block.respawn_anchor.set_spawn", SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
		return true
	}

	// Explode when used outside the Nether.
	if broken, upd := cache.BreakBlockWithUpdates(x, y, z); broken {
		s.broadcastDimensionBlockChanges(dim, cache, upd.Changes, upd.LightChanged)
	}
	s.TriggerRespawnPointExplosion(session, dim, x, y, z)
	return true
}

// TriggerRespawnPointExplosion triggers a 5.0-power explosion at (bx+0.5, by+0.5, bz+0.5)
// in dim (caused by using a bed in Nether/End or a respawn anchor outside the Nether),
// destroying nearby fragile blocks and damaging nearby players with cause "bad_respawn_point".
func (s *Server) TriggerRespawnPointExplosion(activator *PlayerSession, dim string, bx, by, bz int) (playersDamaged, blocksDestroyed int) {
	if s == nil {
		return 0, 0
	}
	dim = world.NormalizeDimension(dim)
	cx := float64(bx) + 0.5
	cy := float64(by) + 0.5
	cz := float64(bz) + 0.5
	chunkX := int32(bx >> 4)
	chunkZ := int32(bz >> 4)

	s.BroadcastDimensionChunk(dim, chunkX, chunkZ, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.generic.explode", SoundSourceBlocks, cx, cy, cz, 4.0, 0.8, 0))
	s.DamageDecorationsInRadius(dim, cx, cy, cz, RespawnPointExplosionRadius*2.0, float32(RespawnPointExplosionRadius*7.0), "explosion")
	s.EmitVibration(VibrationEvent{
		Dimension: dim,
		Type:      GameEventExplode,
		X:         cx,
		Y:         cy,
		Z:         cz,
		Player:    activator,
	})

	// 1. Destroy nearby fragile blocks within radius 3 in dim.
	if cache := s.DimensionChunks(dim); cache != nil && s.CreeperBlockGriefing() {
		dt, _ := world.LookupDimensionType(dim)
		minY := dt.MinY + 1
		maxY := dt.MinY + dt.Height - 1
		if dt.Height <= 0 {
			minY = world.MinY + 1
			maxY = world.MinY + world.WorldHeight - 1
		}
		var changes []world.BlockChange
		var lightChunks []world.ChunkPos
		seenLight := make(map[world.ChunkPos]bool)

		for dy := -2; dy <= 3; dy++ {
			ty := by + dy
			if ty < minY || ty >= maxY {
				continue
			}
			for dx := -3; dx <= 3; dx++ {
				for dz := -3; dz <= 3; dz++ {
					if dx*dx+dy*dy+dz*dz > 9 {
						continue
					}
					tx, tz := bx+dx, bz+dz
					st := cache.GetBlock(tx, ty, tz)
					if st == world.StateAir || st == world.StateBedrock || st == world.StateWater || st == world.StateLava {
						continue
					}
					name := world.BlockName(st)
					if name == "minecraft:obsidian" || name == "minecraft:crying_obsidian" ||
						name == "minecraft:end_portal_frame" || name == "minecraft:end_portal" ||
						name == "minecraft:nether_portal" || name == "minecraft:barrier" ||
						name == "minecraft:ancient_debris" {
						continue
					}
					if valid, lc := cache.SetBlockWithLight(tx, ty, tz, world.StateAir); valid {
						blocksDestroyed++
						s.OnWorkstationBlockBroken(dim, tx, ty, tz)
						changes = append(changes, world.BlockChange{
							Pos:      world.BlockPos{X: tx, Y: ty, Z: tz},
							OldState: st,
							NewState: world.StateAir,
						})
						for _, cp := range lc {
							if !seenLight[cp] {
								seenLight[cp] = true
								lightChunks = append(lightChunks, cp)
							}
						}
					}
				}
			}
		}
		if len(changes) > 0 {
			s.broadcastDimensionBlockChanges(dim, cache, changes, lightChunks)
		}
	}

	// 2. Damage nearby players in dim within 2 * RespawnPointExplosionRadius (10.0 blocks).
	maxRange := RespawnPointExplosionRadius * 2.0
	var sessions []*PlayerSession
	s.playersMu.RLock()
	for _, sess := range s.players {
		if sess != nil && !sess.IsDead() {
			sessions = append(sessions, sess)
		}
	}
	s.playersMu.RUnlock()
	if activator != nil && !activator.IsDead() {
		foundActivator := false
		for _, sess := range sessions {
			if sess == activator {
				foundActivator = true
				break
			}
		}
		if !foundActivator {
			sessions = append(sessions, activator)
		}
	}

	for _, sess := range sessions {
		snap := sess.Snapshot()
		if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator || snap.Health <= 0 {
			continue
		}
		if world.NormalizeDimension(snap.Dimension) != dim {
			continue
		}
		dx := snap.X - cx
		dy := snap.Y - cy
		dz := snap.Z - cz
		dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
		if sess == activator && snap.X == 0 && snap.Y == 0 && snap.Z == 0 {
			// If the activator's transform was never initialized from (0,0,0), treat as adjacent.
			dist = 1.0
		}
		if dist >= maxRange {
			continue
		}
		impact := 1.0 - (dist / maxRange)
		rawDmg := float32((impact*impact+impact)/2.0*7.0*maxRange + 1.0)
		scaled := ScaleDamageByDifficulty(rawDmg, s.Difficulty())
		if scaled <= 0 {
			continue
		}
		s.DamagePlayerWithEffects(sess, scaled, "bad_respawn_point")
		playersDamaged++
		kb := impact * 0.9
		vx, vy, vz := ComputeKnockback(cx, cz, 0, snap.X, snap.Z, kb)
		if sess.send != nil {
			_ = sess.send(protocol.PlaySetEntityMotion, EncodeSetEntityMotionPacket(sess.EntityID, vx, vy, vz))
		}
	}
	return playersDamaged, blocksDestroyed
}

// --- Stand-Up Dismount & Respawn Resolution ---

func isPassableRespawnSpace(state uint16) bool {
	if state == world.StateAir {
		return true
	}
	if state == world.StateWater || state == world.StateLava {
		return false
	}
	name := world.BlockName(state)
	switch name {
	case "minecraft:air", "minecraft:cave_air", "minecraft:void_air",
		"minecraft:short_grass", "minecraft:fern", "minecraft:dead_bush",
		"minecraft:dandelion", "minecraft:poppy", "minecraft:blue_orchid",
		"minecraft:allium", "minecraft:azure_bluet", "minecraft:red_tulip",
		"minecraft:orange_tulip", "minecraft:white_tulip", "minecraft:pink_tulip",
		"minecraft:oxeye_daisy", "minecraft:cornflower", "minecraft:lily_of_the_valley",
		"minecraft:torch", "minecraft:wall_torch", "minecraft:soul_torch", "minecraft:soul_wall_torch",
		"minecraft:redstone_torch", "minecraft:redstone_wall_torch", "minecraft:redstone_wire",
		"minecraft:lever", "minecraft:rail", "minecraft:powered_rail", "minecraft:detector_rail", "minecraft:activator_rail":
		return true
	case "minecraft:snow":
		_, props, ok := world.BlockStateProperties(state)
		return ok && props["layers"] == "1"
	}
	if strings.HasSuffix(name, "_carpet") || strings.HasSuffix(name, "_pressure_plate") ||
		strings.HasSuffix(name, "_button") || strings.HasSuffix(name, "_sign") ||
		strings.HasSuffix(name, "_banner") || strings.HasSuffix(name, "_sapling") {
		return true
	}
	return false
}

func isSafeRespawnFloor(state uint16) bool {
	if state == world.StateAir || state == world.StateWater || state == world.StateLava {
		return false
	}
	if isPassableRespawnSpace(state) {
		return false
	}
	switch world.BlockName(state) {
	case "minecraft:fire", "minecraft:soul_fire", "minecraft:cactus",
		"minecraft:magma_block", "minecraft:sweet_berry_bush", "minecraft:campfire",
		"minecraft:soul_campfire", "minecraft:Lava", "minecraft:lava", "minecraft:water":
		return false
	default:
		return true
	}
}

var standUpRingOffsets = [8][2]int{
	{-1, 0},
	{1, 0},
	{0, -1},
	{0, 1},
	{-1, -1},
	{-1, 1},
	{1, -1},
	{1, 1},
}

// FindBedStandUpPosition searches for an unobstructed dismount position around
// a bed at (bx, by, bz), checking horizontal neighbors of the bed first and
// falling back to standing on the bed itself if the two blocks above it are
// passable.
func FindBedStandUpPosition(cache *world.Cache, bx, by, bz int) (x, y, z float64, ok bool) {
	if cache == nil {
		return 0, 0, 0, false
	}
	headPos, footPos, hasPartner := ResolveBedPositions(cache, bx, by, bz)
	origins := []world.BlockPos{headPos}
	if hasPartner && footPos != headPos {
		origins = append(origins, footPos)
	}

	for _, dy := range []int{0, -1, 1} {
		for _, orig := range origins {
			for _, off := range standUpRingOffsets {
				sx, sy, sz := orig.X+off[0], orig.Y+dy, orig.Z+off[1]
				cand := world.BlockPos{X: sx, Y: sy, Z: sz}
				if cand == headPos || (hasPartner && cand == footPos) {
					continue
				}
				if isSafeRespawnFloor(cache.GetBlock(sx, sy-1, sz)) &&
					isPassableRespawnSpace(cache.GetBlock(sx, sy, sz)) &&
					isPassableRespawnSpace(cache.GetBlock(sx, sy+1, sz)) {
					return float64(sx) + 0.5, float64(sy), float64(sz) + 0.5, true
				}
			}
		}
	}

	// Fallback: stand directly on top of the bed if the 2 blocks above it are clear.
	for _, orig := range origins {
		if isPassableRespawnSpace(cache.GetBlock(orig.X, orig.Y+1, orig.Z)) &&
			isPassableRespawnSpace(cache.GetBlock(orig.X, orig.Y+2, orig.Z)) {
			return float64(orig.X) + 0.5, float64(orig.Y) + 0.5625, float64(orig.Z) + 0.5, true
		}
	}
	return 0, 0, 0, false
}

// FindAnchorStandUpPosition searches for an unobstructed dismount position
// around or on top of a respawn anchor at (ax, ay, az).
func FindAnchorStandUpPosition(cache *world.Cache, ax, ay, az int) (x, y, z float64, ok bool) {
	if cache == nil {
		return 0, 0, 0, false
	}
	for _, dy := range []int{0, -1, 1} {
		for _, off := range standUpRingOffsets {
			sx, sy, sz := ax+off[0], ay+dy, az+off[1]
			if isSafeRespawnFloor(cache.GetBlock(sx, sy-1, sz)) &&
				isPassableRespawnSpace(cache.GetBlock(sx, sy, sz)) &&
				isPassableRespawnSpace(cache.GetBlock(sx, sy+1, sz)) {
				return float64(sx) + 0.5, float64(sy), float64(sz) + 0.5, true
			}
		}
	}
	// Standing on top of the respawn anchor itself.
	if isPassableRespawnSpace(cache.GetBlock(ax, ay+1, az)) &&
		isPassableRespawnSpace(cache.GetBlock(ax, ay+2, az)) {
		return float64(ax) + 0.5, float64(ay + 1), float64(az) + 0.5, true
	}
	return 0, 0, 0, false
}

// ResolvePlayerRespawn determines the target dimension and coordinates for a
// respawning player:
//   - If session has a forced spawn point (e.g. /spawnpoint), returns that position.
//   - If session has a non-forced spawn point at a valid, unobstructed bed (in a
//     dimension where BedWorks is true), returns the bed stand-up position.
//   - If session has a non-forced spawn point at a charged, unobstructed respawn
//     anchor (in a dimension where RespawnAnchorWorks is true), decrements its
//     charge by 1 and returns the anchor stand-up position.
//   - If session had a non-forced home spawn point whose bed/anchor is missing,
//     uncharged, or obstructed, clears the player's home spawn, sends
//     MsgNoHomeBedOrAnchor, and falls back to the server's default world spawn.
func (s *Server) ResolvePlayerRespawn(session *PlayerSession) (dim string, x, y, z float64, yaw, pitch float32, usedHomeSpawn, invalidHomeSpawn bool) {
	wx, wy, wz, wyaw := s.WorldSpawn()
	defaultDim := world.DimensionOverworld
	defaultX := float64(wx) + 0.5
	defaultY := float64(wy)
	defaultZ := float64(wz) + 0.5

	if s == nil || session == nil {
		return defaultDim, defaultX, defaultY, defaultZ, wyaw, 0, false, false
	}

	snap := session.Snapshot()
	if !snap.HasSpawn {
		return defaultDim, defaultX, defaultY, defaultZ, wyaw, 0, false, false
	}

	spawnDim := world.NormalizeDimension(snap.SpawnDimension)
	sx, sy, sz := int(snap.SpawnX), int(snap.SpawnY), int(snap.SpawnZ)

	if snap.SpawnForced {
		return spawnDim, float64(sx) + 0.5, float64(sy), float64(sz) + 0.5, snap.SpawnYaw, snap.SpawnPitch, true, false
	}

	cache := s.DimensionChunks(spawnDim)
	dt, _ := world.LookupDimensionType(spawnDim)
	if cache != nil {
		state := cache.GetBlock(sx, sy, sz)
		if IsBedBlock(state) && dt.BedWorks {
			if rx, ry, rz, ok := FindBedStandUpPosition(cache, sx, sy, sz); ok {
				return spawnDim, rx, ry, rz, snap.SpawnYaw, snap.SpawnPitch, true, false
			}
		} else if IsRespawnAnchorBlock(state) && dt.RespawnAnchorWorks {
			charges := RespawnAnchorCharges(state)
			if charges > 0 {
				if rx, ry, rz, ok := FindAnchorStandUpPosition(cache, sx, sy, sz); ok {
					newCharges := charges - 1
					if nextState, okState := RespawnAnchorState(newCharges); okState {
						if valid, lc := cache.SetBlockWithLight(sx, sy, sz, nextState); valid {
							s.broadcastDimensionBlockChanges(spawnDim, cache, []world.BlockChange{
								{Pos: world.BlockPos{X: sx, Y: sy, Z: sz}, OldState: state, NewState: nextState},
							}, lc)
						}
					}
					cx := int32(sx >> 4)
					cz := int32(sz >> 4)
					s.BroadcastDimensionChunk(spawnDim, cx, cz, PlaySoundPacketID,
						EncodeSoundPacket("minecraft:block.respawn_anchor.deplete", SoundSourceBlocks, float64(sx)+0.5, float64(sy)+0.5, float64(sz)+0.5, 1.0, 1.0, 0))
					return spawnDim, rx, ry, rz, snap.SpawnYaw, snap.SpawnPitch, true, false
				}
			}
		}
	}

	// Non-forced home spawn was missing, uncharged, or obstructed: clear it and notify the player.
	s.ClearPlayerSpawnPoint(session)
	_ = s.SendSystemChat(session, MsgNoHomeBedOrAnchor)
	return defaultDim, defaultX, defaultY, defaultZ, wyaw, 0, false, true
}
