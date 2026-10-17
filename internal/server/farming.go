package server

import (
	"math"
	"math/rand"
	"strings"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// RandomTickSpeed returns the configured randomTickSpeed gamerule on the
// server's primary world cache (default 3).
func (s *Server) RandomTickSpeed() int {
	if s == nil || s.chunks == nil {
		return world.DefaultRandomTickSpeed
	}
	return s.chunks.RandomTickSpeed()
}

// SetRandomTickSpeed sets the randomTickSpeed gamerule on the server's world
// caches.
func (s *Server) SetRandomTickSpeed(speed int) {
	if s == nil {
		return
	}
	if s.chunks != nil {
		s.chunks.SetRandomTickSpeed(speed)
	}
}

// TickRandomBlocks runs one game tick of section-based random block ticks on
// the server's loaded chunks and returns all resulting block and lighting
// updates so callers can broadcast them to nearby players.
func (s *Server) TickRandomBlocks() world.BlockUpdateResult {
	if s == nil || s.chunks == nil {
		return world.BlockUpdateResult{}
	}
	return s.chunks.RandomTickLoadedChunks()
}

func (s *Server) worldCacheForSession(session *PlayerSession) *world.Cache {
	if s == nil {
		return nil
	}
	if session != nil {
		if dim := session.Dimension(); dim != "" {
			if dc := s.DimensionChunks(dim); dc != nil {
				return dc
			}
		}
	}
	return s.chunks
}

// UseBoneMeal applies bone meal from session's inventory slot (or directly when
// slot < 0) to the block at (x, y, z). In Survival/Adventure mode, one
// minecraft:bone_meal item is consumed when the target block accepts bonemeal.
func (s *Server) UseBoneMeal(session *PlayerSession, x, y, z int, slot int, rng *rand.Rand) (consumed bool, res world.BlockUpdateResult) {
	cache := s.worldCacheForSession(session)
	if cache == nil {
		return false, res
	}
	if session != nil && slot >= 0 && slot < world.PlayerInventorySlots {
		held := session.InventorySlot(slot)
		if held.IsEmpty() || held.ResolvedID() != "minecraft:bone_meal" {
			return false, res
		}
	}
	consumed, res = cache.ApplyBoneMeal(x, y, z, rng)
	if !consumed {
		return false, res
	}
	if session != nil && slot >= 0 && slot < world.PlayerInventorySlots && session.Snapshot().GameMode != GameModeCreative {
		held := session.InventorySlot(slot)
		if held.Count > 1 {
			held.Count--
			session.SetInventorySlot(slot, held)
		} else {
			session.SetInventorySlot(slot, world.ItemStack{})
		}
		s.syncPlayerInventorySlot(session, slot)
	}
	return true, res
}

// IsHoeItem reports whether itemID represents a hoe tool.
func IsHoeItem(itemName string) bool {
	id := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(itemName)), "minecraft:")
	return strings.HasSuffix(id, "_hoe")
}

// TillBlockWithHoe tills soil at (x, y, z) (converting grass_block, dirt, or
// dirt_path into farmland[moisture=0], or coarse_dirt / rooted_dirt into dirt)
// and applies 1 durability damage to the hoe in Survival/Adventure mode.
func (s *Server) TillBlockWithHoe(session *PlayerSession, x, y, z int, slot int, rng *rand.Rand) (tilled bool, res world.BlockUpdateResult) {
	cache := s.worldCacheForSession(session)
	if cache == nil {
		return false, res
	}
	if session != nil && slot >= 0 && slot < world.PlayerInventorySlots {
		held := session.InventorySlot(slot)
		if held.IsEmpty() || !IsHoeItem(held.ResolvedID()) {
			return false, res
		}
	}
	tilled, res = cache.TillSoilAt(x, y, z)
	if !tilled {
		return false, res
	}
	if session != nil && slot >= 0 && slot < world.PlayerInventorySlots && session.Snapshot().GameMode != GameModeCreative {
		s.DamagePlayerItemWithRand(session, slot, 1, rng)
	}
	return true, res
}

// PlantSeedOrCrop plants a seed, crop, cocoa bean, sweet berry, sapling, sugar
// cane, bamboo, or kelp from session's inventory slot onto the clicked block at
// (x, y, z) on face (0..5), consumes 1 item in non-Creative mode, and fires the
// "A Seedy Place" (minecraft:husbandry/plant_seed) advancement trigger.
func (s *Server) PlantSeedOrCrop(session *PlayerSession, x, y, z int, face int, slot int) (planted bool, plantedBlock string, res world.BlockUpdateResult) {
	cache := s.worldCacheForSession(session)
	if cache == nil || session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return false, "", res
	}
	held := session.InventorySlot(slot)
	if held.IsEmpty() {
		return false, "", res
	}
	itemName := held.ResolvedID()
	planted, plantedBlock, res = cache.PlantCropItemAt(x, y, z, face, itemName)
	if !planted {
		return false, "", res
	}
	if session.Snapshot().GameMode != GameModeCreative {
		if held.Count > 1 {
			held.Count--
			session.SetInventorySlot(slot, held)
		} else {
			session.SetInventorySlot(slot, world.ItemStack{})
		}
		s.syncPlayerInventorySlot(session, slot)
	}
	s.TriggerPlantedSeed(session, itemName)
	return true, plantedBlock, res
}

// HarvestSweetBerries harvests a mature or semi-mature sweet_berry_bush at
// (x, y, z) on right-click, resetting its age to 1 and adding the harvested
// sweet_berries to session's inventory.
func (s *Server) HarvestSweetBerries(session *PlayerSession, x, y, z int, rng *rand.Rand) (berries int, res world.BlockUpdateResult) {
	cache := s.worldCacheForSession(session)
	if cache == nil {
		return 0, res
	}
	berries, res = cache.HarvestSweetBerryBush(x, y, z, rng)
	if berries <= 0 {
		return 0, res
	}
	if session != nil {
		stack := world.NewNamedItemStack("minecraft:sweet_berries", int32(berries))
		added, changedSlots := session.AddItemToInventory(stack)
		for _, sl := range changedSlots {
			s.syncPlayerInventorySlot(session, sl)
		}
		if rem := stack.Count - added; rem > 0 {
			snap := session.Snapshot()
			s.SpawnItemEntityInDimension(snap.Dimension, snap.X, snap.Y+0.25, snap.Z, world.NewNamedItemStack("minecraft:sweet_berries", rem))
		}
		s.TriggerItemAcquired(session, stack)
	}
	return berries, res
}

// CheckPlayerFarmlandTrample checks whether a player landing after falling
// fallDistance blocks tramples farmland at their feet into dirt (vanilla
// FarmBlock.fallOn: fallDistance > 0.5 && rng.Float64() < fallDistance - 0.5).
func (s *Server) CheckPlayerFarmlandTrample(session *PlayerSession, fallDistance float64, rng *rand.Rand) (trampled bool, res world.BlockUpdateResult) {
	if s == nil || session == nil || fallDistance <= 0.5 {
		return false, res
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return false, res
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	if rng.Float64() >= fallDistance-0.5 {
		return false, res
	}
	cache := s.worldCacheForSession(session)
	if cache == nil {
		return false, res
	}
	bx := int(math.Floor(snap.X))
	by := int(math.Floor(snap.Y - 0.2))
	bz := int(math.Floor(snap.Z))
	trampled, res = cache.TrampleFarmland(bx, by, bz)
	return trampled, res
}

// HarvestCropDrops returns the vanilla item stacks dropped when a crop, stem,
// melon, cocoa pod, nether wart, or sweet berry bush block state is broken.
func HarvestCropDrops(state uint16, fortuneLevel int, rng *rand.Rand) []world.ItemStack {
	return world.CropDropsForBlock(state, fortuneLevel, rng)
}

// HarvestLeafDecayDrops returns the vanilla item stacks (sapling, sticks, apple)
// dropped when a leaf block decays or is broken without silk touch or shears.
func HarvestLeafDecayDrops(state uint16, fortuneLevel int, rng *rand.Rand) []world.ItemStack {
	return world.LeafDecayDrops(state, fortuneLevel, rng)
}

func (s *Server) syncPlayerInventorySlot(session *PlayerSession, slot int) {
	if session == nil || session.send == nil {
		return
	}
	st := session.InventorySlot(slot)
	stateID := session.NextContainerStateID()
	pkt := EncodeContainerSlot(0, stateID, slot, st)
	_ = session.send(protocol.PlayContainerSetSlot, pkt)
}

// BindItemFrameComparatorSignals registers the unified comparator signal
// provider (item frames, lecterns, and jukeboxes), jukebox redstone power
// provider, and note block play listener on the world cache for dimension dim.
func (s *Server) BindItemFrameComparatorSignals(dim string) *world.Cache {
	return s.BindNoteBlockAndJukeboxRedstone(dim)
}

// UpdateItemFrameComparatorsAt binds comparator signal providers for dim and
// updates any redstone comparators reading the block or item frame at (x, y, z).
func (s *Server) UpdateItemFrameComparatorsAt(dim string, x, y, z int) world.BlockUpdateResult {
	cache := s.BindItemFrameComparatorSignals(dim)
	if cache == nil {
		return world.BlockUpdateResult{}
	}
	return cache.UpdateComparatorsAround(x, y, z)
}

// UpdateComparatorsAt binds comparator signal providers for dim and updates
// any redstone comparators reading the block at (x, y, z) (e.g. lectern,
// jukebox, item frame, composter, beehive, cauldron).
func (s *Server) UpdateComparatorsAt(dim string, x, y, z int) world.BlockUpdateResult {
	return s.UpdateItemFrameComparatorsAt(dim, x, y, z)
}

