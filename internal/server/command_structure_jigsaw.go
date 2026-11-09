package server

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/world"
)

type mobSpawnerRuntimeStore struct {
	mu       sync.Mutex
	spawners map[hopperBlockKey]*world.MobSpawnerBlockEntityData
}

var (
	serverMobSpawnerStores      sync.Map // map[*Server]*mobSpawnerRuntimeStore
	serverCustomCmdBlockHandlers sync.Map // map[*Server]func(dim string, x, y, z int, cmd string) (int, string, bool)
)

func (s *Server) mobSpawnerStore() *mobSpawnerRuntimeStore {
	if s == nil {
		return &mobSpawnerRuntimeStore{spawners: make(map[hopperBlockKey]*world.MobSpawnerBlockEntityData)}
	}
	if v, ok := serverMobSpawnerStores.Load(s); ok {
		return v.(*mobSpawnerRuntimeStore)
	}
	st := &mobSpawnerRuntimeStore{
		spawners: make(map[hopperBlockKey]*world.MobSpawnerBlockEntityData),
	}
	actual, _ := serverMobSpawnerStores.LoadOrStore(s, st)
	return actual.(*mobSpawnerRuntimeStore)
}

// SetCustomCommandBlockDispatcher registers an optional fallback command
// handler invoked by `ExecuteCommandBlockCommand` for commands not handled by
// the built-in server command dispatchers.
func (s *Server) SetCustomCommandBlockDispatcher(fn func(dim string, x, y, z int, cmd string) (int, string, bool)) {
	if s == nil {
		return
	}
	if fn == nil {
		serverCustomCmdBlockHandlers.Delete(s)
		return
	}
	serverCustomCmdBlockHandlers.Store(s, fn)
}

// BindCommandBlockCaches wires the server's command block executor, game time
// provider, and `maxCommandChainLength` gamerule into all dimension caches.
func (s *Server) BindCommandBlockCaches() {
	if s == nil {
		return
	}
	for _, dim := range []string{world.DimensionOverworld, world.DimensionNether, world.DimensionEnd} {
		cache := s.DimensionChunks(dim)
		if cache == nil {
			continue
		}
		d := dim
		cache.SetCommandBlockExecutor(func(x, y, z int, command, customName string) (int, string) {
			return s.ExecuteCommandBlockCommand(d, x, y, z, command, customName)
		})
		cache.SetCommandBlockGameTimeProvider(func() int64 {
			gt, _ := s.WorldTime()
			if gt <= 0 {
				return 0
			}
			return gt
		})
		cache.SetMaxCommandChainLength(s.MaxCommandChainLength())
	}
}

func canPlayerEditGameMasterBlock(s *Server, session *PlayerSession) bool {
	if session == nil {
		return true
	}
	if s == nil || !s.HasAdminPermission(session) {
		return false
	}
	return session.Snapshot().GameMode == GameModeCreative
}

func isCommandReplyError(reply string) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{
		"unknown ", "invalid ", "usage:", "cannot ", "failed ", "error", "no player", "no entity", "no targets",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// ExecuteCommandBlockCommand executes a command string on behalf of a command
// block at `(dim, x, y, z)` with `customName`, returning `(successCount, lastOutput)`.
func (s *Server) ExecuteCommandBlockCommand(dim string, x, y, z int, command, customName string) (int, string) {
	raw := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(command), "/"))
	if raw == "" {
		return 0, ""
	}
	if strings.EqualFold(raw, world.CommandBlockEasterEggInput) {
		return 1, world.CommandBlockEasterEggOutput
	}
	if s == nil {
		return 1, "Executed: " + raw
	}
	normDim := world.NormalizeDimension(dim)
	if name := strings.TrimSpace(customName); name == "" {
		customName = world.DefaultCommandBlockName
	}

	if v, ok := serverCustomCmdBlockHandlers.Load(s); ok && v != nil {
		if fn, ok := v.(func(string, int, int, int, string) (int, string, bool)); ok && fn != nil {
			if sc, out, handled := fn(normDim, x, y, z, raw); handled {
				return sc, out
			}
		}
	}

	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return 0, ""
	}
	cmdName := strings.ToLower(strings.TrimPrefix(fields[0], "minecraft:"))
	args := fields[1:]

	// Built-in social, scoreboard, item, status, gamerule, difficulty, worldborder, recipe, advancement commands.
	if reply, handled := s.HandleSocialAndScoreboardCommand(nil, cmdName, args); handled {
		if isCommandReplyError(reply) {
			return 0, reply
		}
		if reply == "" {
			reply = fmt.Sprintf("[%s] Executed /%s", customName, cmdName)
		}
		return 1, reply
	}
	if reply, handled := s.HandleItemAndStatusCommand(nil, cmdName, args); handled {
		if isCommandReplyError(reply) {
			return 0, reply
		}
		return 1, reply
	}

	switch cmdName {
	case "gamerule":
		reply := s.HandleGameRuleCommand(nil, args)
		s.BindCommandBlockCaches()
		if isCommandReplyError(reply) {
			return 0, reply
		}
		return 1, reply

	case "difficulty":
		reply := s.HandleDifficultyCommand(nil, args)
		if isCommandReplyError(reply) {
			return 0, reply
		}
		return 1, reply

	case "worldborder":
		reply := s.HandleWorldBorderCommand(nil, args)
		if isCommandReplyError(reply) {
			return 0, reply
		}
		return 1, reply

	case "recipe":
		reply := s.HandleRecipeCommand(nil, args)
		if isCommandReplyError(reply) {
			return 0, reply
		}
		return 1, reply

	case "advancement":
		reply := s.HandleAdvancementCommand(nil, args)
		if isCommandReplyError(reply) {
			return 0, reply
		}
		return 1, reply

	case "setblock":
		if len(args) < 4 {
			return 0, "Usage: /setblock <x> <y> <z> <block>"
		}
		bx, errX := parseCommandBlockCoord(args[0], x)
		by, errY := parseCommandBlockCoord(args[1], y)
		bz, errZ := parseCommandBlockCoord(args[2], z)
		if errX != nil || errY != nil || errZ != nil {
			return 0, "Invalid setblock coordinates"
		}
		blockSpec := args[3]
		if !strings.Contains(blockSpec, ":") {
			blockSpec = "minecraft:" + blockSpec
		}
		stID, ok := world.BlockStateByName(blockSpec, nil)
		if !ok {
			return 0, "Unknown block: " + args[3]
		}
		cache := s.DimensionChunks(normDim)
		if cache == nil {
			return 0, "Dimension unavailable"
		}
		_, res := cache.ApplyBlockWithUpdates(bx, by, bz, stID)
		s.broadcastBlockUpdateResultInDimension(normDim, res)
		return 1, fmt.Sprintf("Changed the block at %d, %d, %d", bx, by, bz)

	case "summon":
		if len(args) < 1 {
			return 0, "Usage: /summon <entity> [x y z]"
		}
		sx, sy, sz := float64(x)+0.5, float64(y), float64(z)+0.5
		if len(args) >= 4 {
			if px, err := parseCommandBlockCoord(args[1], x); err == nil {
				sx = float64(px) + 0.5
			}
			if py, err := parseCommandBlockCoord(args[2], y); err == nil {
				sy = float64(py)
			}
			if pz, err := parseCommandBlockCoord(args[3], z); err == nil {
				sz = float64(pz) + 0.5
			}
		}
		ent, err := s.SummonEntityCommand(normDim, args[0], sx, sy, sz)
		if err != nil || ent == nil {
			return 0, "Unable to summon entity"
		}
		return 1, fmt.Sprintf("Summoned new %s", ent.TypeName)

	case "seed":
		return 1, "Seed: [0]"
	}

	return 1, fmt.Sprintf("Executed: %s", raw)
}

func (s *Server) broadcastBlockUpdateResultInDimension(dim string, res world.BlockUpdateResult) {
	if s == nil || len(res.Changes) == 0 {
		return
	}
	s.broadcastCopperBlockUpdateResult(world.NormalizeDimension(dim), res)
}

// PlaceCommandBlock places a command block (`minecraft:command_block`,
// `minecraft:repeating_command_block`, or `minecraft:chain_command_block`) at
// `(dim, x, y, z)` oriented opposite to `playerLook3D` and initializes its
// `CommandBlockEntityData`.
func (s *Server) PlaceCommandBlock(dim string, x, y, z int, blockName, playerLook3D string) (uint16, world.CommandBlockEntityData, bool) {
	if s == nil {
		return 0, world.CommandBlockEntityData{}, false
	}
	s.BindCommandBlockCaches()
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return 0, world.CommandBlockEntityData{}, false
	}
	st := world.ResolveCommandBlockPlacementState(blockName, playerLook3D)
	if st == 0 {
		return 0, world.CommandBlockEntityData{}, false
	}
	_, res := cache.ApplyBlockWithUpdates(x, y, z, st)
	be := cache.EnsureCommandBlockEntityAt(x, y, z)
	be.Powered = cache.IsBlockPowered(x, y, z)
	sub := cache.SetCommandBlockEntityAt(x, y, z, be)
	s.broadcastBlockUpdateResultInDimension(normDim, res)
	s.broadcastBlockUpdateResultInDimension(normDim, sub)
	return st, be, true
}

// UpdateCommandBlock applies a player or programmatic configuration update
// (`ServerboundSetCommandBlockPacket`) to a command block at `(dim, x, y, z)`.
// When `session != nil`, the player must be an operator (`HasAdminPermission`) in
// Creative mode (`GameMode == GameModeCreative`), matching vanilla
// `Player.canUseGameMasterBlocks()`.
func (s *Server) UpdateCommandBlock(
	session *PlayerSession,
	dim string,
	x, y, z int,
	command string,
	mode string,
	conditional bool,
	trackOutput bool,
	automatic bool,
	customName string,
) (world.CommandBlockEntityData, bool) {
	if s == nil || !canPlayerEditGameMasterBlock(s, session) {
		return world.CommandBlockEntityData{}, false
	}
	s.BindCommandBlockCaches()
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return world.CommandBlockEntityData{}, false
	}
	curState := cache.GetBlock(x, y, z)
	_, facing, _, ok := world.ParseCommandBlockState(curState)
	if !ok {
		return world.CommandBlockEntityData{}, false
	}
	targetBlockName := world.BlockNameForCommandBlockMode(mode)
	nextState := world.MakeCommandBlockState(targetBlockName, facing, conditional)
	be := cache.EnsureCommandBlockEntityAt(x, y, z)
	if nextState != curState {
		cache.SetBlock(x, y, z, nextState)
		s.broadcastCopperBlockUpdateResult(normDim, world.BlockUpdateResult{
			Changes: []world.BlockChange{{Pos: world.BlockPos{X: x, Y: y, Z: z}, OldState: curState, NewState: nextState}},
		})
	}
	be.Command = command
	be.TrackOutput = trackOutput
	if !trackOutput {
		be.LastOutput = ""
	}
	be.Auto = automatic
	if customName != "" {
		be.CustomName = customName
	}
	res := cache.SetCommandBlockEntityAt(x, y, z, be)
	s.broadcastBlockUpdateResultInDimension(normDim, res)
	updated, _ := cache.CommandBlockEntityAt(x, y, z)
	return updated, true
}

// TriggerCommandBlock immediately evaluates and runs the command block (and any
// downstream `chain_command_block` chain) at `(dim, x, y, z)`.
func (s *Server) TriggerCommandBlock(dim string, x, y, z int) (world.CommandBlockEntityData, bool) {
	if s == nil {
		return world.CommandBlockEntityData{}, false
	}
	s.BindCommandBlockCaches()
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return world.CommandBlockEntityData{}, false
	}
	be, res := cache.TriggerCommandBlockAt(x, y, z)
	s.broadcastBlockUpdateResultInDimension(normDim, res)
	return be, true
}

// UpdateStructureBlock applies a player or programmatic update
// (`ServerboundSetStructureBlockPacket`) to a `minecraft:structure_block` at
// `(dim, x, y, z)`. Supported `action` values: `"UPDATE_DATA"`, `"SAVE_AREA"`,
// `"LOAD_AREA"`, `"SCAN_AREA"`.
func (s *Server) UpdateStructureBlock(
	session *PlayerSession,
	dim string,
	x, y, z int,
	data world.StructureBlockEntityData,
	action string,
) (world.StructureBlockEntityData, bool) {
	if s == nil || !canPlayerEditGameMasterBlock(s, session) {
		return world.StructureBlockEntityData{}, false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return world.StructureBlockEntityData{}, false
	}
	if !world.IsStructureBlock(cache.GetBlock(x, y, z)) {
		return world.StructureBlockEntityData{}, false
	}
	res := cache.SetStructureBlockDataAt(x, y, z, data)
	s.broadcastBlockUpdateResultInDimension(normDim, res)
	actOK := true
	switch strings.ToUpper(strings.TrimSpace(action)) {
	case "SAVE_AREA", "SAVE":
		actOK = cache.SaveStructureAt(x, y, z)
	case "LOAD_AREA", "LOAD":
		var sub world.BlockUpdateResult
		actOK, sub = cache.PlaceStructureAt(x, y, z)
		s.broadcastBlockUpdateResultInDimension(normDim, sub)
	case "SCAN_AREA", "CORNER", "DETECT_SIZE":
		_, actOK = cache.DetectStructureSizeAt(x, y, z)
	}
	updated, _ := cache.StructureBlockDataAt(x, y, z)
	return updated, actOK
}

// PlaceJigsawBlock places a `minecraft:jigsaw` block at `(dim, x, y, z)` and
// initializes its `JigsawBlockEntityData`.
func (s *Server) PlaceJigsawBlock(dim string, x, y, z int, playerLook3D, playerHorizFacing string) (uint16, world.JigsawBlockEntityData, bool) {
	if s == nil {
		return 0, world.JigsawBlockEntityData{}, false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return 0, world.JigsawBlockEntityData{}, false
	}
	st := world.ResolveJigsawPlacementState(playerLook3D, playerHorizFacing)
	if st == 0 {
		return 0, world.JigsawBlockEntityData{}, false
	}
	_, res := cache.ApplyBlockWithUpdates(x, y, z, st)
	be := world.DefaultJigsawBlockEntityData(x, y, z, st)
	cache.SetJigsawBlockEntityAt(x, y, z, be)
	s.broadcastBlockUpdateResultInDimension(normDim, res)
	return st, be, true
}

// UpdateJigsawBlock applies a player or programmatic update
// (`ServerboundSetJigsawBlockPacket`) to a `minecraft:jigsaw` block at `(dim, x, y, z)`.
func (s *Server) UpdateJigsawBlock(
	session *PlayerSession,
	dim string,
	x, y, z int,
	data world.JigsawBlockEntityData,
) (world.JigsawBlockEntityData, bool) {
	if s == nil || !canPlayerEditGameMasterBlock(s, session) {
		return world.JigsawBlockEntityData{}, false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return world.JigsawBlockEntityData{}, false
	}
	if !world.IsJigsawBlock(cache.GetBlock(x, y, z)) {
		return world.JigsawBlockEntityData{}, false
	}
	cache.SetJigsawBlockEntityAt(x, y, z, data)
	updated, ok := cache.JigsawBlockEntityAt(x, y, z)
	return updated, ok
}

// --- Mob Spawner (`minecraft:spawner` / `minecraft:mob_spawner`) ---

// EnsureMobSpawnerAt returns or initializes the runtime MobSpawnerBlockEntityData
// for a `minecraft:spawner` block at `(dim, x, y, z)`, seeding its `EntityID`
// from any worldgen `StructureBlockEntity` at `(x, y, z)` if present.
func (s *Server) EnsureMobSpawnerAt(dim string, x, y, z int) (*world.MobSpawnerBlockEntityData, bool) {
	if s == nil {
		return nil, false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return nil, false
	}
	if !world.IsSpawnerBlock(cache.GetBlock(x, y, z)) {
		return nil, false
	}
	key := hopperBlockKey{dim: normDim, x: x, y: y, z: z}
	st := s.mobSpawnerStore()
	st.mu.Lock()
	defer st.mu.Unlock()
	if existing, ok := st.spawners[key]; ok && existing != nil {
		cp := *existing
		return &cp, true
	}
	d := world.DefaultMobSpawnerBlockEntityData(x, y, z)
	if sbe, ok := cache.StructureBlockEntityAt(x, y, z); ok && (sbe.ID == "minecraft:mob_spawner" || sbe.ID == "minecraft:spawner") && sbe.SpawnerEntityID != "" {
		d.EntityID = sbe.SpawnerEntityID
	}
	st.spawners[key] = &d
	cp := d
	return &cp, true
}

// SetMobSpawnerAt stores `data` for the `minecraft:spawner` at `(dim, x, y, z)`.
func (s *Server) SetMobSpawnerAt(dim string, x, y, z int, data world.MobSpawnerBlockEntityData) bool {
	if s == nil {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return false
	}
	if !world.IsSpawnerBlock(cache.GetBlock(x, y, z)) {
		return false
	}
	data.X, data.Y, data.Z = x, y, z
	st := s.mobSpawnerStore()
	st.mu.Lock()
	cp := data
	st.spawners[hopperBlockKey{dim: normDim, x: x, y: y, z: z}] = &cp
	st.mu.Unlock()
	return true
}

// SpawnEggEntityID extracts the entity registry ID (e.g. `"minecraft:zombie"`)
// from a spawn egg item ID (`"minecraft:zombie_spawn_egg"` -> `"minecraft:zombie"`).
func SpawnEggEntityID(itemID string) (string, bool) {
	canon := canonicalResourceName(itemID)
	if !strings.HasSuffix(canon, "_spawn_egg") {
		return "", false
	}
	ent := strings.TrimSuffix(canon, "_spawn_egg")
	if ent == "" || ent == "minecraft:" {
		return "", false
	}
	return ent, true
}

// InteractSpawnerWithSpawnEgg configures a `minecraft:spawner` or
// `minecraft:trial_spawner` at `(dim, x, y, z)` when right-clicked with a
// `*_spawn_egg` item, setting its spawned entity ID and consuming 1 spawn egg
// when `session` is not in Creative mode.
func (s *Server) InteractSpawnerWithSpawnEgg(session *PlayerSession, dim string, x, y, z int, itemID string) (string, bool) {
	if s == nil {
		return "", false
	}
	entID, ok := SpawnEggEntityID(itemID)
	if !ok {
		return "", false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return "", false
	}
	blk := cache.GetBlock(x, y, z)
	switch {
	case world.IsSpawnerBlock(blk):
		be, exists := s.EnsureMobSpawnerAt(normDim, x, y, z)
		if !exists || be == nil {
			return "", false
		}
		be.EntityID = entID
		be.SpawnPotentials = []world.MobSpawnerPotential{{Weight: 1, EntityID: entID}}
		s.SetMobSpawnerAt(normDim, x, y, z, *be)
	case world.IsTrialSpawnerBlock(blk):
		ts := s.EnsureTrialSpawnerAt(normDim, x, y, z)
		if ts == nil {
			return "", false
		}
		s.ConfigureTrialSpawnerAt(normDim, x, y, z, "", "", entID)
	default:
		return "", false
	}

	if session != nil && session.Snapshot().GameMode != GameModeCreative {
		slot, held := session.handSlotAndStack(0)
		if !held.IsEmpty() && canonicalResourceName(held.ResolvedID()) == canonicalResourceName(itemID) {
			consumeOneHandItemOrDamageIgniter(s, session, slot, held, false)
		}
	}
	return entID, true
}

// BreakSpawnerBlock breaks a `minecraft:spawner` at `(dim, x, y, z)`, removing
// its block entity, dropping 0 items (even with Silk Touch, matching vanilla
// `SpawnerBlock`), and spawning `15 + rand(15) + rand(15)` (15..43) XP when
// broken without Silk Touch (`!silkTouch`).
func (s *Server) BreakSpawnerBlock(dim string, x, y, z int, silkTouch bool, rng *rand.Rand) (xpDropped int, ok bool) {
	if s == nil {
		return 0, false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return 0, false
	}
	if !world.IsSpawnerBlock(cache.GetBlock(x, y, z)) {
		return 0, false
	}
	st := s.mobSpawnerStore()
	st.mu.Lock()
	delete(st.spawners, hopperBlockKey{dim: normDim, x: x, y: y, z: z})
	st.mu.Unlock()
	_, res := cache.BreakBlockWithUpdates(x, y, z)
	s.broadcastCopperBlockUpdateResult(normDim, res)
	xpDropped = world.SpawnerBreakExperienceDrop(rng, silkTouch)
	if xpDropped > 0 {
		s.SpawnExperienceOrbsInDimension(normDim, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, int32(xpDropped))
	}
	return xpDropped, true
}

// TickMobSpawnerAt advances the `minecraft:spawner` at `(dim, x, y, z)` by one
// server tick (`BaseSpawner.serverTick` in vanilla 26.1.2):
//   - Requires at least one alive, non-spectator player within `RequiredPlayerRange` (default 16).
//   - Decrements `Delay`; when `Delay <= 0`, attempts `SpawnCount` (default 4)
//     spawns of `EntityID` within `SpawnRange` (default 4) if fewer than
//     `MaxNearbyEntities` (default 6) entities of the same type are inside the
//     spawner's inflated bounding box, and resets `Delay` to `[MinSpawnDelay, MaxSpawnDelay)`.
func (s *Server) TickMobSpawnerAt(dim string, x, y, z int, rng *rand.Rand) ([]int32, bool) {
	be, ok := s.EnsureMobSpawnerAt(dim, x, y, z)
	if !ok || be == nil {
		return nil, false
	}
	normDim := world.NormalizeDimension(dim)
	playerNear := false
	s.playersMu.RLock()
	for _, p := range s.players {
		if p == nil || p.IsDead() || world.NormalizeDimension(p.Dimension()) != normDim {
			continue
		}
		snap := p.Snapshot()
		if snap.GameMode == GameModeSpectator {
			continue
		}
		if be.IsPlayerInRange(snap.X, snap.Y, snap.Z) {
			playerNear = true
			break
		}
	}
	s.playersMu.RUnlock()
	if !playerNear {
		return nil, false
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	if be.Delay == -1 {
		be.NextRandomDelay(rng)
	}
	if be.Delay > 0 {
		be.Delay--
		s.SetMobSpawnerAt(normDim, x, y, z, *be)
		return nil, true
	}
	if be.EntityID == "" {
		be.NextRandomDelay(rng)
		s.SetMobSpawnerAt(normDim, x, y, z, *be)
		return nil, true
	}

	canonEnt := canonicalResourceName(be.EntityID)
	spawnCount := be.SpawnCount
	if spawnCount <= 0 {
		spawnCount = world.DefaultSpawnerSpawnCount
	}
	maxNearby := be.MaxNearbyEntities
	if maxNearby <= 0 {
		maxNearby = world.DefaultSpawnerMaxNearbyEntities
	}
	sr := float64(be.SpawnRange)
	if sr <= 0 {
		sr = float64(world.DefaultSpawnerSpawnRange)
	}
	minX, maxX := float64(x)-sr, float64(x+1)+sr
	minY, maxY := float64(y)-sr, float64(y+1)+sr
	minZ, maxZ := float64(z)-sr, float64(z+1)+sr

	var spawnedIDs []int32
	for i := 0; i < spawnCount; i++ {
		nearby := 0
		for _, ent := range s.entities.All() {
			if ent.EffectiveDimension() == normDim && canonicalResourceName(ent.TypeName) == canonEnt {
				if ent.X >= minX && ent.X <= maxX && ent.Y >= minY && ent.Y <= maxY && ent.Z >= minZ && ent.Z <= maxZ {
					nearby++
				}
			}
		}
		if nearby >= maxNearby {
			be.NextRandomDelay(rng)
			s.SetMobSpawnerAt(normDim, x, y, z, *be)
			return spawnedIDs, true
		}
		wx, wy, wz := be.SampleCandidatePosition(rng)
		ent, err := s.SummonEntityCommand(normDim, canonEnt, wx, wy, wz)
		if err == nil && ent != nil {
			spawnedIDs = append(spawnedIDs, ent.ID)
		}
	}
	if len(spawnedIDs) > 0 {
		be.NextRandomDelay(rng)
	}
	s.SetMobSpawnerAt(normDim, x, y, z, *be)
	return spawnedIDs, true
}

// ChunkCommandStructureJigsawSpawnerBlockEntitiesNBT returns vanilla 26.1.2
// `block_entities` NBT compounds for all command blocks, structure blocks,
// jigsaw blocks, and mob spawners in chunk `(cx, cz)` of `dim`.
func (s *Server) ChunkCommandStructureJigsawSpawnerBlockEntitiesNBT(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	var out []*nbt.Compound
	if cache := s.DimensionChunks(normDim); cache != nil {
		out = append(out, cache.ChunkCommandStructureJigsawBlockEntitiesNBT(cx, cz)...)
	}
	st := s.mobSpawnerStore()
	st.mu.Lock()
	for k, sp := range st.spawners {
		if k.dim == normDim && sp != nil && int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			out = append(out, sp.ToNBT())
		}
	}
	st.mu.Unlock()
	return out
}

// UnloadChunkCommandStructureJigsawSpawnerBlockEntities extracts and removes
// all command blocks, structure blocks, jigsaw blocks, and mob spawners in
// chunk `(cx, cz)` of `dim`.
func (s *Server) UnloadChunkCommandStructureJigsawSpawnerBlockEntities(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	var out []*nbt.Compound
	if cache := s.DimensionChunks(normDim); cache != nil {
		out = append(out, cache.UnloadChunkCommandStructureJigsawBlockEntities(cx, cz)...)
	}
	st := s.mobSpawnerStore()
	st.mu.Lock()
	for k, sp := range st.spawners {
		if k.dim == normDim && int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			if sp != nil {
				out = append(out, sp.ToNBT())
			}
			delete(st.spawners, k)
		}
	}
	st.mu.Unlock()
	return out
}

// LoadChunkCommandStructureJigsawSpawnerBlockEntitiesNBT loads command blocks,
// structure blocks, jigsaw blocks, and mob spawners from a chunk's
// `block_entities` NBT compounds.
func (s *Server) LoadChunkCommandStructureJigsawSpawnerBlockEntitiesNBT(dim string, compounds []*nbt.Compound) int {
	if s == nil || len(compounds) == 0 {
		return 0
	}
	s.BindCommandBlockCaches()
	normDim := world.NormalizeDimension(dim)
	loaded := 0
	if cache := s.DimensionChunks(normDim); cache != nil {
		loaded += cache.LoadChunkCommandStructureJigsawBlockEntitiesNBT(compounds)
	}
	st := s.mobSpawnerStore()
	st.mu.Lock()
	for _, comp := range compounds {
		if comp == nil {
			continue
		}
		idTag, ok := comp.Get("id")
		if !ok {
			continue
		}
		idStr, ok := idTag.(nbt.String)
		if !ok {
			continue
		}
		if string(idStr) == "minecraft:mob_spawner" || string(idStr) == "minecraft:spawner" {
			if sp, ok := world.MobSpawnerBlockEntityFromNBT(comp); ok {
				cp := sp
				st.spawners[hopperBlockKey{dim: normDim, x: sp.X, y: sp.Y, z: sp.Z}] = &cp
				loaded++
			}
		}
	}
	st.mu.Unlock()
	return loaded
}

var _ = strconv.Itoa
var _ = math.Floor
