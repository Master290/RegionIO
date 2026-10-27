package server

import (
	"math"
	"math/rand"
	"sync"

	"regionio/internal/world"
)

// InfestedSilverfishSpawnHookFunc is an optional callback that Agent A can
// register to customize silverfish spawning when an infested block is broken
// without Silk Touch (for example, to trigger SilverfishWakeUpFriendsGoal).
type InfestedSilverfishSpawnHookFunc func(s *Server, dim string, x, y, z float64, blockState uint16) int32

var (
	infestedSilverfishHookMu sync.RWMutex
	infestedSilverfishHook   InfestedSilverfishSpawnHookFunc
)

// SetInfestedSilverfishSpawnHook registers (or clears when nil) a custom hook
// invoked when an infested block spawns a silverfish upon breaking.
func SetInfestedSilverfishSpawnHook(fn InfestedSilverfishSpawnHookFunc) {
	infestedSilverfishHookMu.Lock()
	infestedSilverfishHook = fn
	infestedSilverfishHookMu.Unlock()
}

// PlayerEffectiveHasteLevel returns the effective Haste / Conduit Power level
// (1-based: Amplifier+1) for block mining speed calculation.
func (s *Server) PlayerEffectiveHasteLevel(session *PlayerSession) int {
	if session == nil {
		return 0
	}
	lvl := 0
	if h, ok := session.PlayerEffect(EffectHaste); ok {
		if int(h.Amplifier)+1 > lvl {
			lvl = int(h.Amplifier) + 1
		}
	}
	if cp, ok := session.PlayerEffect(EffectConduitPower); ok {
		if int(cp.Amplifier)+1 > lvl {
			lvl = int(cp.Amplifier) + 1
		}
	}
	return lvl
}

// PlayerMiningFatigueLevel returns the 1-based Mining Fatigue level (Amplifier+1)
// active on session, or 0 if not present.
func (s *Server) PlayerMiningFatigueLevel(session *PlayerSession) int {
	if session == nil {
		return 0
	}
	if mf, ok := session.PlayerEffect(EffectMiningFatigue); ok {
		if mf.Amplifier >= 0 {
			return int(mf.Amplifier) + 1
		}
	}
	return 0
}

// IsPlayerEyeInWater reports whether the block at session's eye height contains
// water (either a water block or a waterlogged block).
func (s *Server) IsPlayerEyeInWater(session *PlayerSession) bool {
	if s == nil || session == nil {
		return false
	}
	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return false
	}
	eyeY := snap.Y + 1.62
	bx := int(math.Floor(snap.X))
	by := int(math.Floor(eyeY))
	bz := int(math.Floor(snap.Z))
	st := cache.GetBlock(bx, by, bz)
	kind, _, _, _, _, ok := world.ClassifyFluidState(st)
	return (ok && kind == world.FluidWater) || world.BlockName(st) == "minecraft:bubble_column"
}

// HasPlayerAquaAffinity reports whether the player is wearing a helmet with
// minecraft:aqua_affinity.
func HasPlayerAquaAffinity(session *PlayerSession) bool {
	if session == nil {
		return false
	}
	helmet := session.InventorySlot(ArmorSlotHead)
	if helmet.IsEmpty() {
		return false
	}
	meta := GetSlotItemMeta(session, ArmorSlotHead)
	return meta.EnchantmentLevel("minecraft:aqua_affinity") > 0
}

func (s *Server) playerMiningInputs(session *PlayerSession) (toolItem string, efficiencyLevel, hasteLevel, fatigueLevel int, underwaterWithoutAquaAffinity, onGround bool) {
	onGround = true
	if session == nil {
		return "", 0, 0, 0, false, true
	}
	snap := session.Snapshot()
	onGround = snap.OnGround
	if heldSlot := session.HeldInventorySlot(); heldSlot >= 0 {
		held := session.InventorySlot(heldSlot)
		if !held.IsEmpty() {
			toolItem = held.ResolvedID()
			meta := GetSlotItemMeta(session, heldSlot)
			efficiencyLevel = meta.EnchantmentLevel("minecraft:efficiency")
		}
	}
	hasteLevel = s.PlayerEffectiveHasteLevel(session)
	fatigueLevel = s.PlayerMiningFatigueLevel(session)
	underwaterWithoutAquaAffinity = s.IsPlayerEyeInWater(session) && !HasPlayerAquaAffinity(session)
	return
}

// CanPlayerHarvestBlock reports whether session's currently held tool satisfies
// the harvest requirements for blockState.
func (s *Server) CanPlayerHarvestBlock(session *PlayerSession, blockState uint16) bool {
	toolItem, _, _, _, _, _ := s.playerMiningInputs(session)
	return world.CanHarvestBlockWithTool(blockState, toolItem)
}

// PlayerBlockDestroySpeed computes session's effective destroy speed multiplier
// against blockState in vanilla 26.1.2.
func (s *Server) PlayerBlockDestroySpeed(session *PlayerSession, blockState uint16) float64 {
	toolItem, eff, haste, fatigue, uwNoAqua, onGround := s.playerMiningInputs(session)
	return world.BlockDestroySpeed(blockState, toolItem, eff, haste, fatigue, uwNoAqua, onGround)
}

// PlayerBlockDestroyProgressPerTick computes the fraction of block health
// removed per tick when session mines blockState in survival mode.
func (s *Server) PlayerBlockDestroyProgressPerTick(session *PlayerSession, blockState uint16) float64 {
	toolItem, eff, haste, fatigue, uwNoAqua, onGround := s.playerMiningInputs(session)
	return world.BlockDestroyProgressPerTick(blockState, toolItem, eff, haste, fatigue, uwNoAqua, onGround)
}

// PlayerBlockTicksToBreak returns the number of survival ticks required for
// session to break blockState (0 for instant break, -1 if unbreakable).
func (s *Server) PlayerBlockTicksToBreak(session *PlayerSession, blockState uint16) int {
	toolItem, eff, haste, fatigue, uwNoAqua, onGround := s.playerMiningInputs(session)
	return world.BlockTicksToBreak(blockState, toolItem, eff, haste, fatigue, uwNoAqua, onGround)
}

// EvaluateServerBlockBreakDropsAndXP evaluates the item drops and experience
// points produced when breaking blockState with heldStack and toolMeta.
func EvaluateServerBlockBreakDropsAndXP(
	blockState uint16,
	heldStack world.ItemStack,
	toolMeta ItemMeta,
	rng *rand.Rand,
) (drops []world.ItemStack, xp int) {
	var toolItem string
	if !heldStack.IsEmpty() {
		toolItem = heldStack.ResolvedID()
	}
	silk := HasSilkTouch(toolMeta)
	fortune := EnchantmentFortuneLevel(toolMeta)
	drops = world.EvaluateBlockDrops(blockState, toolItem, silk, fortune, rng)
	xp = world.BlockBreakExperience(blockState, toolItem, silk, rng)
	return drops, xp
}

// BreakInfestedBlockAt spawns a silverfish when an infested block is broken in
// survival mode without Silk Touch and DoTileDrops() is enabled.
func (s *Server) BreakInfestedBlockAt(dim string, x, y, z int, blockState uint16, silkTouch bool) (int32, bool) {
	if s == nil {
		return 0, false
	}
	if !world.ShouldSpawnSilverfishOnBreak(blockState, silkTouch, s.DoTileDrops()) {
		return 0, false
	}
	normDim := world.NormalizeDimension(dim)
	sx := float64(x) + 0.5
	sy := float64(y)
	sz := float64(z) + 0.5

	infestedSilverfishHookMu.RLock()
	hook := infestedSilverfishHook
	infestedSilverfishHookMu.RUnlock()
	if hook != nil {
		if id := hook(s, normDim, sx, sy, sz, blockState); id > 0 {
			return id, true
		}
	}
	id, err := s.SpawnMobInDimension(normDim, sx, sy, sz, "minecraft:silverfish")
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// SpawnBlockBreakDropsAndXPAt evaluates and spawns item drops, experience orbs,
// and infested-block silverfish at (x, y, z) in dim when DoTileDrops() is true.
func (s *Server) SpawnBlockBreakDropsAndXPAt(
	dim string,
	x, y, z int,
	blockState uint16,
	heldStack world.ItemStack,
	toolMeta ItemMeta,
	rng *rand.Rand,
) (dropEntityIDs []int32, xpAwarded int, silverfishEntityID int32) {
	if s == nil || !s.DoTileDrops() {
		return nil, 0, 0
	}
	normDim := world.NormalizeDimension(dim)
	drops, xp := EvaluateServerBlockBreakDropsAndXP(blockState, heldStack, toolMeta, rng)
	for _, st := range drops {
		if st.IsEmpty() || st.Count <= 0 {
			continue
		}
		if id, _ := s.SpawnItemEntityInDimension(normDim, float64(x)+0.5, float64(y)+0.25, float64(z)+0.5, st); id > 0 {
			dropEntityIDs = append(dropEntityIDs, id)
		}
	}
	if xp > 0 {
		s.SpawnExperienceOrbsInDimension(normDim, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, int32(xp))
		xpAwarded = xp
	}
	if world.IsInfestedBlock(blockState) {
		if sfID, ok := s.BreakInfestedBlockAt(normDim, x, y, z, blockState, HasSilkTouch(toolMeta)); ok {
			silverfishEntityID = sfID
		}
	}
	return dropEntityIDs, xpAwarded, silverfishEntityID
}

// PlaceOrStackTurtleEggAt places a new turtle egg or increments an existing
// turtle_egg block at (x, y, z) in dim up to 4 eggs, broadcasting block updates.
func (s *Server) PlaceOrStackTurtleEggAt(dim string, x, y, z int) (uint16, bool) {
	if s == nil {
		return 0, false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return 0, false
	}
	oldState := cache.GetBlock(x, y, z)
	newState, upd, ok := cache.PlaceOrIncrementTurtleEggAt(x, y, z)
	if !ok {
		return oldState, false
	}
	s.BroadcastDimensionBlocks(normDim, upd.Changes, upd.LightChanged)
	s.BroadcastDimensionChunk(normDim, int32(x>>4), int32(z>>4), PlaySoundPacketID,
		EncodeSoundPacket("minecraft:block.turtle_egg.crack", SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 0.7, 1.0, 0))
	return newState, true
}

// TrampleTurtleEggBlockAt processes an entity landing on or stepping hard on a
// turtle_egg at (x, y, z) in dim. Sneaking or fallDistance <= 0.5 does not
// trample the egg. Each trample decrements eggs by 1 (4 -> 3 -> 2 -> 1 -> air).
func (s *Server) TrampleTurtleEggBlockAt(dim string, x, y, z int, fallDistance float64, sneaking bool) (int, bool) {
	if s == nil || sneaking || fallDistance <= 0.5 {
		return 0, false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return 0, false
	}
	rem, upd, ok := cache.TrampleTurtleEggAt(x, y, z)
	if !ok {
		return 0, false
	}
	s.BroadcastDimensionBlocks(normDim, upd.Changes, upd.LightChanged)
	s.BroadcastDimensionChunk(normDim, int32(x>>4), int32(z>>4), PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.zombie.destroy_egg", SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 0.7, 0.9, 0))
	return rem, true
}

// RandomTickTurtleEggAndSpawnBabiesAt runs one vanilla random-tick hatch check
// on a turtle_egg at (x, y, z) in dim, spawning baby turtles when hatch=2 finishes.
func (s *Server) RandomTickTurtleEggAndSpawnBabiesAt(dim string, x, y, z int, dayTime int64, rng *rand.Rand) (cracked, hatched bool, spawnedTurtleIDs []int32) {
	if s == nil {
		return false, false, nil
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return false, false, nil
	}
	_, babies, changed, upd := cache.RandomTickTurtleEggAt(x, y, z, dayTime, rng)
	if !changed {
		return false, false, nil
	}
	hatched = babies > 0
	cracked = !hatched
	s.BroadcastDimensionBlocks(normDim, upd.Changes, upd.LightChanged)
	soundName := "minecraft:block.turtle_egg.crack"
	if hatched {
		soundName = "minecraft:block.turtle_egg.hatch"
	}
	s.BroadcastDimensionChunk(normDim, int32(x>>4), int32(z>>4), PlaySoundPacketID,
		EncodeSoundPacket(soundName, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 0.7, 1.0, 0))
	if hatched && babies > 0 {
		for i := 0; i < babies; i++ {
			offset := 0.3 + float64(i)*0.15
			if id, err := s.SpawnMobInDimension(normDim, float64(x)+offset, float64(y), float64(z)+0.5, "minecraft:turtle"); err == nil && id > 0 {
				spawnedTurtleIDs = append(spawnedTurtleIDs, id)
			}
		}
	}
	return cracked, hatched, spawnedTurtleIDs
}
