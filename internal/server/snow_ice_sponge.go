package server

import (
	"math"
	"math/rand"
	"sync"

	"regionio/internal/world"
)

// LevelEventLavaExtinguish is the vanilla LevelEvent ID (1501) for fizzing smoke
// particles when water/wet_sponge evaporates in the Nether.
const LevelEventLavaExtinguish int32 = 1501

// PlaceWetSpongeAt places a `minecraft:wet_sponge` at (dim, x, y, z) and, if
// `dim` is the Nether (`minecraft:the_nether`), immediately dries it into a
// dry `minecraft:sponge`, broadcasting `LevelEventLavaExtinguish` (1501) and
// `minecraft:block.fire.extinguish`.
func (s *Server) PlaceWetSpongeAt(dim string, x, y, z int) (finalState uint16, driedInNether bool) {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return world.StateAir, false
	}
	targetDim := normalizeDispenserDim(dim)
	cache := s.DimensionChunks(targetDim)
	if cache == nil {
		cache = s.chunks
	}
	if cache == nil {
		return world.StateAir, false
	}
	wetSponge, ok := world.BlockStateByName("minecraft:wet_sponge", nil)
	if !ok {
		return world.StateAir, false
	}
	if applied, upd := cache.ApplyBlockWithUpdates(x, y, z, wetSponge); applied {
		s.broadcastDimensionBlockChanges(targetDim, cache, upd.Changes, upd.LightChanged)
	}
	if dried, upd := cache.DryWetSpongeInNetherAt(targetDim, x, y, z); dried {
		s.broadcastDimensionBlockChanges(targetDim, cache, upd.Changes, upd.LightChanged)
		cx, cz := int32(x>>4), int32(z>>4)
		s.BroadcastDimensionChunk(targetDim, cx, cz, PlayLevelEventCB,
			EncodeLevelEventPacket(LevelEventLavaExtinguish, x, y, z, 0, false))
		s.BroadcastDimensionChunk(targetDim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:block.fire.extinguish", SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 2.6, 0))
		return cache.GetBlock(x, y, z), true
	}
	return cache.GetBlock(x, y, z), false
}

// BreakIceBlock handles breaking a `minecraft:ice` block at (dim, x, y, z):
//   - In Creative mode: breaks into `StateAir` with no drops or water.
//   - With Silk Touch (`minecraft:silk_touch > 0`): breaks into `StateAir` and
//     drops `1x minecraft:ice` (when `s.DoTileDrops()`).
//   - Without Silk Touch in Survival: turns into `StateWater` in the Overworld/End
//     when `(x, y-1, z)` blocks motion or is a fluid, or `StateAir` in the Nether
//     or over air.
func (s *Server) BreakIceBlock(
	dim string,
	x, y, z int,
	toolStack world.ItemStack,
	toolMeta ItemMeta,
	creative bool,
) (droppedIce bool, turnedToWater bool) {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return false, false
	}
	targetDim := normalizeDispenserDim(dim)
	cache := s.DimensionChunks(targetDim)
	if cache == nil {
		cache = s.chunks
	}
	if cache == nil || !world.IsIceBlock(cache.GetBlock(x, y, z)) {
		return false, false
	}
	if creative {
		if broken, upd := cache.BreakBlockWithUpdates(x, y, z); broken {
			s.broadcastDimensionBlockChanges(targetDim, cache, upd.Changes, upd.LightChanged)
		}
		return false, false
	}
	if !toolStack.IsEmpty() && toolMeta.EnchantmentLevel("minecraft:silk_touch") > 0 {
		if broken, upd := cache.BreakBlockWithUpdates(x, y, z); broken {
			s.broadcastDimensionBlockChanges(targetDim, cache, upd.Changes, upd.LightChanged)
			if s.DoTileDrops() {
				s.SpawnItemEntityInDimension(targetDim, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5,
					world.NewNamedItemStack("minecraft:ice", 1))
			}
			return true, false
		}
		return false, false
	}
	replacement, upd, ok := cache.BreakIceWithoutSilkTouchAt(targetDim, x, y, z)
	if !ok {
		return false, false
	}
	s.broadcastDimensionBlockChanges(targetDim, cache, upd.Changes, upd.LightChanged)
	return false, replacement == world.StateWater
}

// StackSnowLayerWithItem handles right-clicking a `minecraft:snow` block
// (`layers < 8`) with a `minecraft:snow` item stack at (dim, x, y, z),
// incrementing `layers` by 1 and consuming 1 item in Survival mode.
func (s *Server) StackSnowLayerWithItem(
	dim string,
	x, y, z int,
	heldStack world.ItemStack,
	creative bool,
) (newStack world.ItemStack, stacked bool) {
	newStack = heldStack
	if s == nil || heldStack.IsEmpty() || canonicalResourceName(heldStack.ResolvedID()) != "minecraft:snow" {
		return newStack, false
	}
	if y <= world.MinY || y >= world.MinY+world.WorldHeight {
		return newStack, false
	}
	targetDim := normalizeDispenserDim(dim)
	cache := s.DimensionChunks(targetDim)
	if cache == nil {
		cache = s.chunks
	}
	if cache == nil {
		return newStack, false
	}
	_, upd, ok := cache.StackSnowLayerAt(x, y, z)
	if !ok {
		return newStack, false
	}
	if !creative {
		if newStack.Count <= 1 {
			newStack = world.ItemStack{}
		} else {
			newStack.Count--
		}
	}
	s.broadcastDimensionBlockChanges(targetDim, cache, upd.Changes, upd.LightChanged)
	cx, cz := int32(x>>4), int32(z>>4)
	s.BroadcastDimensionChunk(targetDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:block.snow.place", SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	return newStack, true
}

// RandomTickSnowOrIceInDimension executes one random tick on `minecraft:snow`,
// `minecraft:ice`, or `minecraft:frosted_ice` at (dim, x, y, z) and broadcasts
// any resulting block and lighting changes.
func (s *Server) RandomTickSnowOrIceInDimension(dim string, x, y, z int, rng *rand.Rand) bool {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return false
	}
	targetDim := normalizeDispenserDim(dim)
	cache := s.DimensionChunks(targetDim)
	if cache == nil {
		cache = s.chunks
	}
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	var changed bool
	var upd world.BlockUpdateResult
	switch {
	case world.IsSnowLayerBlock(state):
		changed, upd = cache.RandomTickSnowLayerAt(x, y, z)
	case world.IsIceBlock(state):
		changed, upd = cache.RandomTickIceAt(targetDim, x, y, z)
	case world.IsFrostedIceBlock(state):
		changed, upd = cache.StepFrostedIceAt(targetDim, x, y, z, rng)
	}
	if changed {
		s.broadcastDimensionBlockChanges(targetDim, cache, upd.Changes, upd.LightChanged)
	}
	return changed
}

// --- Server-Side Powder Snow Entity & Projectile Hooks ---

type entityPowderSnowState struct {
	mu                sync.Mutex
	ticksFrozen       int
	freezeTickCounter int
}

var serverEntityPowderSnowStates sync.Map // map[int32]*entityPowderSnowState

func getEntityPowderSnowState(entityID int32) *entityPowderSnowState {
	if v, ok := serverEntityPowderSnowStates.Load(entityID); ok {
		return v.(*entityPowderSnowState)
	}
	actual, _ := serverEntityPowderSnowStates.LoadOrStore(entityID, &entityPowderSnowState{})
	return actual.(*entityPowderSnowState)
}

// EntityTicksFrozen returns the current powder_snow freeze ticks (0..140) for entityID.
func (s *Server) EntityTicksFrozen(entityID int32) int {
	if s == nil || entityID <= 0 {
		return 0
	}
	st := getEntityPowderSnowState(entityID)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.ticksFrozen
}

// SetEntityTicksFrozen sets the powder_snow freeze ticks (clamped to 0..140) for entityID.
func (s *Server) SetEntityTicksFrozen(entityID int32, ticks int) {
	if s == nil || entityID <= 0 {
		return
	}
	if ticks < 0 {
		ticks = 0
	} else if ticks > world.PowderSnowMinFreezeTicks {
		ticks = world.PowderSnowMinFreezeTicks
	}
	st := getEntityPowderSnowState(entityID)
	st.mu.Lock()
	st.ticksFrozen = ticks
	st.mu.Unlock()
}

// EntityPowderSnowSpeedMultiplier returns the movement speed multiplier (0.5..1.0)
// for entityID based on its current `TicksFrozen` (`LivingEntity.tryAddFrost`).
func (s *Server) EntityPowderSnowSpeedMultiplier(entityID int32) float64 {
	return world.PowderSnowFrozenSpeedMultiplier(s.EntityTicksFrozen(entityID))
}

// StepEntityPowderSnow advances one server tick of `minecraft:powder_snow`
// interaction for a mob/entity (`entityID`):
//   - If the entity is inside `powder_snow` and currently on fire (`EntityFireTicks > 0`),
//     extinguishes the entity and, when `s.MobGriefing()` is enabled, melts the
//     `powder_snow` block into `StateAir`.
//   - Advances `TicksFrozen` (`+1` inside `powder_snow` when not freeze-immune,
//     `-2` outside `powder_snow` or when wearing leather armor / freeze-immune).
//   - Deals freeze damage (`5.0` for `#freeze_hurts_extra_types` like `blaze`,
//     `strider`, `magma_cube`; `1.0` for normal mobs) every 40 ticks while fully frozen (`TicksFrozen == 140`).
func (s *Server) StepEntityPowderSnow(entityID int32, hasLeatherArmor bool) (ticksFrozen int, damageDealt float32, meltedSnow bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return 0, 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.IsItem() {
		return 0, 0, false
	}
	dim := world.NormalizeDimension(ent.Dimension)
	cache := s.DimensionChunks(dim)
	if cache == nil {
		cache = s.chunks
	}
	bx := int(math.Floor(ent.X))
	by := int(math.Floor(ent.Y))
	bz := int(math.Floor(ent.Z))
	inPowderSnow := s.IsEntityInPowderSnow(entityID)

	if inPowderSnow && s.EntityFireTicks(entityID) > 0 {
		s.SetEntityFireTicks(entityID, 0)
		if s.MobGriefing() && cache != nil {
			if melted, upd := cache.ExtinguishBurningEntityInPowderSnowAt(bx, by, bz, true, true); melted {
				s.broadcastDimensionBlockChanges(dim, cache, upd.Changes, upd.LightChanged)
				meltedSnow = true
				inPowderSnow = false
			}
		}
	}

	immune := hasLeatherArmor || world.IsFreezeImmuneEntityType(ent.TypeName)
	st := getEntityPowderSnowState(entityID)
	st.mu.Lock()
	nextFrozen, frozen := world.StepPowderSnowFreezing(inPowderSnow, immune, st.ticksFrozen)
	st.ticksFrozen = nextFrozen
	shouldDamage := false
	if frozen {
		st.freezeTickCounter++
		if st.freezeTickCounter >= world.PowderSnowFreezeDamageIntervalTicks {
			st.freezeTickCounter = 0
			shouldDamage = true
		}
	} else {
		st.freezeTickCounter = 0
	}
	st.mu.Unlock()

	if shouldDamage && s.FreezeDamage() {
		dmg := world.PowderSnowFreezeDamageForEntity(ent.TypeName)
		if dmg > 0 {
			killed := s.applyDamageToMob(ent, dmg, nil, 0, false, false)
			if killed {
				s.entities.Remove(entityID)
			}
			damageDealt = dmg
		}
	}
	return nextFrozen, damageDealt, meltedSnow
}

// ExtinguishBurningProjectileInPowderSnow handles a burning projectile (e.g.
// flaming arrow) entering a `minecraft:powder_snow` block at (dim, x, y, z):
// when `isOnFire` and `canDestroyBlock` (true for player-owned projectiles or
// when `s.MobGriefing()` is enabled for mob-owned projectiles), it melts the
// `powder_snow` block into `StateAir` and broadcasts the block update.
func (s *Server) ExtinguishBurningProjectileInPowderSnow(dim string, x, y, z int, isOnFire, canDestroyBlock bool) bool {
	if s == nil || !isOnFire || !canDestroyBlock || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return false
	}
	targetDim := normalizeDispenserDim(dim)
	cache := s.DimensionChunks(targetDim)
	if cache == nil {
		cache = s.chunks
	}
	if cache == nil {
		return false
	}
	melted, upd := cache.ExtinguishBurningEntityInPowderSnowAt(x, y, z, true, true)
	if melted {
		s.broadcastDimensionBlockChanges(targetDim, cache, upd.Changes, upd.LightChanged)
	}
	return melted
}

// CheckPlayerWalkOnPowderSnowWithLeatherBoots checks whether session is standing
// on top of a `minecraft:powder_snow` block at `y-1` while wearing `minecraft:leather_boots`
// (without sneaking) and, if so, grants the `"Light as a Rabbit"` advancement
// (`minecraft:adventure/walk_on_powder_snow_with_leather_boots`).
func (s *Server) CheckPlayerWalkOnPowderSnowWithLeatherBoots(session *PlayerSession) bool {
	if s == nil || session == nil || session.IsDead() || session.IsSneaking() {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return false
	}
	bootsID := session.InventorySlot(ArmorSlotFeet).ResolvedID()
	if !world.CanEntityWalkOnPowderSnow(bootsID) {
		return false
	}
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.DimensionChunks(dim)
	if cache == nil {
		cache = s.chunks
	}
	if cache == nil {
		return false
	}
	bx := int(math.Floor(snap.X))
	by := int(math.Floor(snap.Y)) - 1
	bz := int(math.Floor(snap.Z))
	if by < world.MinY || by >= world.MinY+world.WorldHeight {
		return false
	}
	if !world.IsPowderSnowBlock(cache.GetBlock(bx, by, bz)) {
		return false
	}
	if !world.CanStandOnPowderSnowSurface("minecraft:player", bootsID, snap.Y, by, false) {
		return false
	}
	s.TriggerWalkOnPowderSnowWithLeatherBoots(session)
	return true
}
