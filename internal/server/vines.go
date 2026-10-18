package server

import (
	"math/rand"

	"regionio/internal/world"
)

// HarvestCaveVinesBerries harvests `1x minecraft:glow_berries` from a
// `minecraft:cave_vines` or `minecraft:cave_vines_plant` with `berries="true"`
// at (dim, x, y, z), updating the block state and lighting, spawning the glow
// berry item entity (when `spawnDrop && s.DoTileDrops()`), playing
// `minecraft:block.cave_vines.pick_berries`, and emitting a `block_change` vibration.
func (s *Server) HarvestCaveVinesBerries(
	session *PlayerSession,
	dim string,
	x, y, z int,
	spawnDrop bool,
) (drop world.ItemStack, harvested bool) {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return world.ItemStack{}, false
	}
	targetDim := normalizeDispenserDim(dim)
	if session != nil && dim == "" {
		if d := session.Dimension(); d != "" {
			targetDim = normalizeDispenserDim(d)
		}
	}
	cache := s.DimensionChunks(targetDim)
	if cache == nil {
		cache = s.chunks
	}
	if cache == nil {
		return world.ItemStack{}, false
	}
	_, upd, ok := cache.HarvestCaveVinesBerriesAt(x, y, z)
	if !ok {
		return world.ItemStack{}, false
	}
	s.broadcastDimensionBlockChanges(targetDim, cache, upd.Changes, upd.LightChanged)

	drop = world.NewNamedItemStack("minecraft:glow_berries", 1)
	if spawnDrop && s.DoTileDrops() {
		s.SpawnItemEntityInDimension(targetDim, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, drop)
	}
	cx, cz := int32(x>>4), int32(z>>4)
	s.BroadcastDimensionChunk(targetDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:block.cave_vines.pick_berries", SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	s.EmitVibration(VibrationEvent{
		Dimension: targetDim,
		Type:      GameEventBlockChange,
		X:         float64(x) + 0.5,
		Y:         float64(y) + 0.5,
		Z:         float64(z) + 0.5,
		Player:    session,
	})
	return drop, true
}

// BoneMealHangingFloraAt applies `minecraft:bone_meal` to `cave_vines` /
// `cave_vines_plant` (grows glow berries), `weeping_vines` / `weeping_vines_plant`
// (extends downward), `twisting_vines` / `twisting_vines_plant` (extends upward),
// `pale_hanging_moss` (extends downward), or `glow_lichen` (spreads to an
// adjacent sturdy surface) at (dim, x, y, z).
func (s *Server) BoneMealHangingFloraAt(
	dim string,
	x, y, z int,
	heldStack world.ItemStack,
	creative bool,
	rng *rand.Rand,
) (newStack world.ItemStack, grown bool) {
	newStack = heldStack
	if s == nil || heldStack.IsEmpty() || canonicalResourceName(heldStack.ResolvedID()) != "minecraft:bone_meal" {
		return newStack, false
	}
	if y < world.MinY || y >= world.MinY+world.WorldHeight {
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
	state := cache.GetBlock(x, y, z)
	var upd world.BlockUpdateResult
	switch {
	case world.IsCaveVinesBlock(state):
		_, upd, grown = cache.BoneMealCaveVinesAt(x, y, z)
	case world.BlockName(state) == "minecraft:weeping_vines" ||
		world.BlockName(state) == "minecraft:weeping_vines_plant" ||
		world.BlockName(state) == "minecraft:twisting_vines" ||
		world.BlockName(state) == "minecraft:twisting_vines_plant":
		var n int
		n, upd = cache.BoneMealNetherVinesAt(x, y, z, 0, rng)
		grown = n > 0
	case world.IsPaleHangingMossBlock(state):
		grown, upd = cache.BoneMealPaleHangingMossAt(x, y, z)
	case world.IsGlowLichenBlock(state):
		grown, upd = cache.BoneMealGlowLichenAt(x, y, z)
	default:
		return newStack, false
	}
	if !grown {
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
	s.BroadcastDimensionChunk(targetDim, cx, cz, PlayLevelEventCB,
		EncodeLevelEventPacket(LevelEventParticlesBoneMeal, x, y, z, 15, false))
	s.BroadcastDimensionChunk(targetDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.bone_meal.use", SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	return newStack, true
}

func damageShearsStack(stack world.ItemStack, meta ItemMeta, creative bool) (world.ItemStack, ItemMeta) {
	if creative || stack.IsEmpty() {
		return stack, meta
	}
	unbreaking := meta.EnchantmentLevel("minecraft:unbreaking")
	if unbreaking > 0 && nextCopperRandIntn(int(unbreaking)+1) != 0 {
		return stack, meta
	}
	meta.Damage++
	if meta.Damage >= ShearsMaxDurability {
		if stack.Count <= 1 {
			return world.ItemStack{}, ItemMeta{}
		}
		stack.Count--
		meta.Damage = 0
	}
	return stack, meta
}

// ShearGrowingPlantTip shears a growing plant tip (`cave_vines`,
// `weeping_vines`, `twisting_vines`, or `kelp`) at (dim, x, y, z) when
// right-clicked with `minecraft:shears`, setting its `age` to 25, playing
// `minecraft:block.growing_plant.crop`, and damaging the shears by 1 durability.
func (s *Server) ShearGrowingPlantTip(
	dim string,
	x, y, z int,
	heldStack world.ItemStack,
	heldMeta ItemMeta,
	creative bool,
) (newStack world.ItemStack, newMeta ItemMeta, sheared bool) {
	newStack = heldStack
	newMeta = heldMeta.Clone()
	if s == nil || heldStack.IsEmpty() || canonicalResourceName(heldStack.ResolvedID()) != "minecraft:shears" {
		return newStack, newMeta, false
	}
	if y < world.MinY || y >= world.MinY+world.WorldHeight {
		return newStack, newMeta, false
	}
	targetDim := normalizeDispenserDim(dim)
	cache := s.DimensionChunks(targetDim)
	if cache == nil {
		cache = s.chunks
	}
	if cache == nil {
		return newStack, newMeta, false
	}
	_, upd, ok := cache.ShearGrowingPlantTipAt(x, y, z)
	if !ok {
		return newStack, newMeta, false
	}
	newStack, newMeta = damageShearsStack(newStack, newMeta, creative)
	s.broadcastDimensionBlockChanges(targetDim, cache, upd.Changes, upd.LightChanged)
	cx, cz := int32(x>>4), int32(z>>4)
	s.BroadcastDimensionChunk(targetDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:block.growing_plant.crop", SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	return newStack, newMeta, true
}

// RandomTickVinesAndFloraInDimension executes one random tick on a vine or
// hanging plant at (dim, x, y, z) and broadcasts any resulting block and light
// changes.
func (s *Server) RandomTickVinesAndFloraInDimension(dim string, x, y, z int, rng *rand.Rand) bool {
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
	changed, upd := cache.RandomTickVinesAndFloraAt(x, y, z, rng)
	if changed {
		s.broadcastDimensionBlockChanges(targetDim, cache, upd.Changes, upd.LightChanged)
	}
	return changed
}
