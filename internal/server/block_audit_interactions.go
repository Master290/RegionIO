package server

import (
	"math/rand"
	"strings"

	"regionio/internal/world"
)

// UseShovelOnBlock flattens `grass_block`, `dirt`, `podzol`, `coarse_dirt`,
// `mycelium`, or `rooted_dirt` into `minecraft:dirt_path` (when `clickedFace != "down"`
// and air is above), or douses a lit `campfire` / `soul_campfire` at `(x, y, z)`,
// damaging the shovel by 1 in Survival/Adventure mode.
func (s *Server) UseShovelOnBlock(
	session *PlayerSession,
	x, y, z int,
	clickedFace string,
	slot int,
	rng *rand.Rand,
) (changed bool, dousedCampfire bool, res world.BlockUpdateResult) {
	cache := s.worldCacheForSession(session)
	if cache == nil {
		return false, false, res
	}
	if session != nil && slot >= 0 && slot < world.PlayerInventorySlots {
		held := session.InventorySlot(slot)
		if held.IsEmpty() || !IsShovelItem(held.ResolvedID()) {
			return false, false, res
		}
	}
	changed, dousedCampfire, res = cache.FlattenBlockWithShovelAt(x, y, z, clickedFace)
	if !changed {
		return false, false, res
	}
	dim := world.DimensionOverworld
	if session != nil && session.Dimension() != "" {
		dim = world.NormalizeDimension(session.Dimension())
	}
	s.broadcastDimensionBlockChanges(dim, cache, res.Changes, res.LightChanged)
	if session != nil && slot >= 0 && slot < world.PlayerInventorySlots && session.Snapshot().GameMode != GameModeCreative {
		s.DamagePlayerItemWithRand(session, slot, 1, rng)
	}
	return true, dousedCampfire, res
}

// UseAxeOnBlock strips logs/woods/stems/hyphae/`bamboo_block` (`action = "strip"`),
// scrapes oxidation off unwaxed copper (`action = "scrape"`), or removes wax
// from waxed copper (`action = "unwax"`) at `(x, y, z)`, damaging the axe by 1
// in Survival/Adventure mode and broadcasting block/level updates.
func (s *Server) UseAxeOnBlock(
	session *PlayerSession,
	x, y, z int,
	slot int,
	rng *rand.Rand,
) (action string, newState uint16, ok bool, res world.BlockUpdateResult) {
	cache := s.worldCacheForSession(session)
	if cache == nil {
		return "", world.StateAir, false, res
	}
	if session != nil && slot >= 0 && slot < world.PlayerInventorySlots {
		held := session.InventorySlot(slot)
		if held.IsEmpty() || !world.IsAxeItemID(held.ResolvedID()) {
			return "", world.StateAir, false, res
		}
	}
	action, newState, ok, res = cache.UseAxeOnBlockAt(x, y, z)
	if !ok {
		return "", world.StateAir, false, res
	}
	dim := world.DimensionOverworld
	if session != nil && session.Dimension() != "" {
		dim = world.NormalizeDimension(session.Dimension())
	}
	s.broadcastDimensionBlockChanges(dim, cache, res.Changes, res.LightChanged)
	if session != nil && slot >= 0 && slot < world.PlayerInventorySlots && session.Snapshot().GameMode != GameModeCreative {
		s.DamagePlayerItemWithRand(session, slot, 1, rng)
	}
	return action, newState, true, res
}

// InteractBlockWithToolOrItem executes the unified Vanilla 26.1.2 item-on-block
// interaction for the item in `session`'s `slot` against `(x, y, z)` on
// `clickedFace`, updating the world, spawning any resulting item drops,
// damaging or consuming the held item in Survival/Adventure mode, and
// broadcasting block changes.
func (s *Server) InteractBlockWithToolOrItem(
	session *PlayerSession,
	x, y, z int,
	clickedFace string,
	slot int,
	rng *rand.Rand,
) (handled bool, action string, res world.BlockUpdateResult) {
	cache := s.worldCacheForSession(session)
	if cache == nil || session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return false, "", res
	}
	held := session.InventorySlot(slot)
	if held.IsEmpty() {
		return false, "", res
	}
	itemID := held.ResolvedID()
	var replacementItem string
	var drops []world.ItemStack
	handled, action, replacementItem, drops, res = cache.InteractBlockWithItemAt(x, y, z, clickedFace, itemID, rng)
	if !handled {
		return false, "", res
	}

	dim := world.DimensionOverworld
	if session.Dimension() != "" {
		dim = world.NormalizeDimension(session.Dimension())
	}
	s.broadcastDimensionBlockChanges(dim, cache, res.Changes, res.LightChanged)

	if len(drops) > 0 && s.DoTileDrops() {
		for _, drop := range drops {
			if !drop.IsEmpty() {
				s.SpawnItemEntityInDimension(dim, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, drop)
			}
		}
	}

	if session.Snapshot().GameMode != GameModeCreative {
		spec := world.ParseToolItem(itemID)
		if spec.Category != world.ToolNone || strings.HasSuffix(itemID, "flint_and_steel") {
			s.DamagePlayerItemWithRand(session, slot, 1, rng)
		} else if replacementItem != "" {
			if held.Count <= 1 {
				session.SetInventorySlot(slot, world.NewNamedItemStack(replacementItem, 1))
				s.syncPlayerInventorySlot(session, slot)
			} else {
				held.Count--
				session.SetInventorySlot(slot, held)
				s.syncPlayerInventorySlot(session, slot)
				replStack := world.NewNamedItemStack(replacementItem, 1)
				added, changedSlots := session.AddItemToInventory(replStack)
				for _, sl := range changedSlots {
					s.syncPlayerInventorySlot(session, sl)
				}
				if added < 1 {
					snap := session.Snapshot()
					s.SpawnItemEntityInDimension(dim, snap.X, snap.Y+0.25, snap.Z, replStack)
				}
			}
		} else {
			if held.Count > 1 {
				held.Count--
				session.SetInventorySlot(slot, held)
			} else {
				session.SetInventorySlot(slot, world.ItemStack{})
			}
			s.syncPlayerInventorySlot(session, slot)
		}
	}
	return true, action, res
}
