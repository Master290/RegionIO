package server

import (
	"math/rand"

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
