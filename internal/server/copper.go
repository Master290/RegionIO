package server

import (
	"math/rand"
	"strings"
	"sync"
	"time"

	"regionio/internal/world"
)

// Vanilla 26.1.2 LevelEvent IDs for copper waxing, unwaxing, scraping, and
// lightning electric sparks (ClientboundLevelEventPacket 0x2e).
// Note: LevelEventParticlesWaxOn (3003) is defined in dispenser.go.
const (
	LevelEventParticlesElectricSpark int32 = 3002 // PARTICLES_ELECTRIC_SPARK
	LevelEventParticlesWaxOff        int32 = 3004 // PARTICLES_WAX_OFF
	LevelEventParticlesScrape        int32 = 3005 // PARTICLES_SCRAPE
)

// CopperGolemStatueReviveHook is an optional callback (for A) invoked when a
// player uses an axe on an unwaxed, unoxidized `minecraft:copper_golem_statue`
// at (dim, x, y, z) to revive it into a living Copper Golem entity.
type CopperGolemStatueReviveHook func(dim string, x, y, z int, facing string) (entityID int32, ok bool)

var (
	copperServerRngMu sync.Mutex
	copperServerRng   = rand.New(rand.NewSource(time.Now().UnixNano()))

	copperGolemReviveHooks sync.Map // map[*Server]CopperGolemStatueReviveHook
)

func nextCopperRandFloat64() float64 {
	copperServerRngMu.Lock()
	v := copperServerRng.Float64()
	copperServerRngMu.Unlock()
	return v
}

func nextCopperRandIntn(n int) int {
	if n <= 1 {
		return 0
	}
	copperServerRngMu.Lock()
	v := copperServerRng.Intn(n)
	copperServerRngMu.Unlock()
	return v
}

// SetCopperGolemStatueReviveHook registers an optional callback invoked when an
// unwaxed `minecraft:copper_golem_statue` is scraped with an axe to revive it
// into a Copper Golem mob.
func (s *Server) SetCopperGolemStatueReviveHook(hook CopperGolemStatueReviveHook) {
	if s == nil {
		return
	}
	if hook == nil {
		copperGolemReviveHooks.Delete(s)
		return
	}
	copperGolemReviveHooks.Store(s, hook)
}

func (s *Server) copperGolemReviveHook() CopperGolemStatueReviveHook {
	if s == nil {
		return nil
	}
	if v, ok := copperGolemReviveHooks.Load(s); ok && v != nil {
		if fn, ok := v.(CopperGolemStatueReviveHook); ok {
			return fn
		}
	}
	return nil
}

// IsAxeItem reports whether stack is any vanilla axe (`*_axe`).
func IsAxeItem(stack world.ItemStack) bool {
	if stack.IsEmpty() {
		return false
	}
	name := canonicalResourceName(stack.ResolvedID())
	return strings.HasSuffix(name, "_axe")
}

// AxeMaxDurability returns the vanilla 26.1.2 max durability of an axe item.
func AxeMaxDurability(stack world.ItemStack) int32 {
	if stack.IsEmpty() {
		return 0
	}
	name := canonicalResourceName(stack.ResolvedID())
	switch name {
	case "minecraft:wooden_axe":
		return 59
	case "minecraft:golden_axe":
		return 32
	case "minecraft:stone_axe":
		return 131
	case "minecraft:copper_axe":
		return 190
	case "minecraft:iron_axe":
		return 250
	case "minecraft:diamond_axe":
		return 1561
	case "minecraft:netherite_axe":
		return 2031
	default:
		if strings.HasSuffix(name, "_axe") {
			return 250
		}
		return 0
	}
}

func damageAxeStack(stack world.ItemStack, meta ItemMeta, creative bool) (world.ItemStack, ItemMeta) {
	if creative || stack.IsEmpty() {
		return stack, meta
	}
	maxDur := AxeMaxDurability(stack)
	if maxDur <= 0 {
		return stack, meta
	}
	unbreaking := meta.EnchantmentLevel("minecraft:unbreaking")
	if unbreaking > 0 && nextCopperRandIntn(int(unbreaking)+1) != 0 {
		return stack, meta
	}
	meta.Damage++
	if meta.Damage >= maxDur {
		if stack.Count <= 1 {
			return world.ItemStack{}, ItemMeta{}
		}
		stack.Count--
		meta.Damage = 0
	}
	return stack, meta
}

// BindCopperRedstone ensures that redstone and comparator callbacks on the
// world cache for `dim` include copper_bulb (15 when lit), copper_golem_statue
// (1..4 by pose), and copper_chest container fill signals.
func (s *Server) BindCopperRedstone(dim string) *world.Cache {
	if s == nil {
		return nil
	}
	targetDim := normalizeDispenserDim(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(targetDim)
	if cache == nil {
		return nil
	}
	cache.SetComparatorSignalProvider(func(bx, by, bz int) int {
		sig := s.ComparatorSignalAt(targetDim, bx, by, bz)
		if cSig := s.CopperComparatorSignalAt(targetDim, bx, by, bz); cSig > sig {
			sig = cSig
		}
		return sig
	})
	return cache
}

// CopperComparatorSignalAt returns the comparator signal (0..15) for a copper
// block at (dim, x, y, z):
//   - `*copper_bulb`: 15 when `lit="true"`, else 0.
//   - `*copper_golem_statue`: 1 (`standing`), 2 (`sitting`), 3 (`running`), 4 (`star`).
//   - `*copper_chest`: 0..15 container fullness signal.
func (s *Server) CopperComparatorSignalAt(dim string, x, y, z int) int {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return 0
	}
	targetDim := normalizeDispenserDim(dim)
	cache := s.DimensionChunks(targetDim)
	if cache == nil {
		cache = s.chunks
	}
	if cache == nil {
		return 0
	}
	state := cache.GetBlock(x, y, z)
	if world.IsCopperBulbBlock(state) {
		return world.CopperBulbComparatorSignal(state)
	}
	if world.IsCopperGolemStatueBlock(state) {
		return world.CopperGolemStatueComparatorSignal(state)
	}
	if world.IsCopperChestBlock(state) {
		return s.ContainerComparatorSignalAt(targetDim, x, y, z)
	}
	return 0
}

func (s *Server) broadcastCopperBlockUpdateResult(dim string, res world.BlockUpdateResult) {
	if s == nil {
		return
	}
	targetDim := normalizeDispenserDim(dim)
	for _, ch := range res.Changes {
		s.broadcastBlockUpdateInDim(targetDim, ch.Pos.X, ch.Pos.Y, ch.Pos.Z, ch.NewState)
	}
}

// syncPartnerCopperChestState synchronizes the partner half of a double copper
// chest (`type="left"` or `type="right"`) when one half is waxed, unwaxed,
// oxidized, or scraped.
func (s *Server) syncPartnerCopperChestState(dim string, cache *world.Cache, x, y, z int, newState uint16, levelEvent int32) {
	if cache == nil || !world.IsCopperChestBlock(newState) {
		return
	}
	partnerPos, ok := world.PartnerCopperChestPos(x, y, z, newState)
	if !ok {
		return
	}
	pState := cache.GetBlock(partnerPos.X, partnerPos.Y, partnerPos.Z)
	if !world.IsCopperChestBlock(pState) {
		return
	}
	_, stage, waxed, ok := world.ClassifyCopperBlock(newState)
	if !ok {
		return
	}
	nextPartner, ok := world.CopperBlockStateWithStageAndWax(pState, stage, waxed)
	if !ok || nextPartner == pState {
		return
	}
	if applied, res := cache.ApplyBlockWithUpdates(partnerPos.X, partnerPos.Y, partnerPos.Z, nextPartner); applied {
		s.broadcastCopperBlockUpdateResult(dim, res)
		if levelEvent != 0 {
			s.BroadcastDimensionChunk(dim, int32(partnerPos.X>>4), int32(partnerPos.Z>>4), PlayLevelEventCB,
				EncodeLevelEventPacket(levelEvent, partnerPos.X, partnerPos.Y, partnerPos.Z, 0, false))
		}
	}
}

// WaxCopperBlockWithHoneycomb waxes an unwaxed copper block at (dim, x, y, z)
// when right-clicked with `minecraft:honeycomb`, broadcasting the new block
// state (and partner door/chest half if applicable) and `LevelEventParticlesWaxOn` (3003).
func (s *Server) WaxCopperBlockWithHoneycomb(
	dim string,
	x, y, z int,
	heldStack world.ItemStack,
	creative bool,
) (newStack world.ItemStack, changed bool) {
	newStack = heldStack
	if s == nil || heldStack.IsEmpty() || canonicalResourceName(heldStack.ResolvedID()) != "minecraft:honeycomb" {
		return newStack, false
	}
	targetDim := normalizeDispenserDim(dim)
	cache := s.BindCopperRedstone(targetDim)
	if cache == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return newStack, false
	}
	waxedState, res, ok := cache.WaxCopperAt(x, y, z)
	if !ok {
		return newStack, false
	}
	s.broadcastCopperBlockUpdateResult(targetDim, res)
	s.BroadcastDimensionChunk(targetDim, int32(x>>4), int32(z>>4), PlayLevelEventCB,
		EncodeLevelEventPacket(LevelEventParticlesWaxOn, x, y, z, 0, false))
	s.syncPartnerCopperChestState(targetDim, cache, x, y, z, waxedState, LevelEventParticlesWaxOn)

	if !creative {
		if newStack.Count <= 1 {
			newStack = world.ItemStack{}
		} else {
			newStack.Count--
		}
	}
	return newStack, true
}

// UseAxeOnCopperBlock applies an axe right-click to a copper block at (dim, x, y, z):
//   - If the block is waxed (`waxed_*`), removes wax (`CopperAxeActionUnwax`),
//     broadcasts `LevelEventParticlesWaxOff` (3004), and consumes 1 axe durability.
//   - If the block is unwaxed and oxidized (`exposed`, `weathered`, `oxidized`),
//     scrapes 1 oxidation stage (`CopperAxeActionScrape`), broadcasts
//     `LevelEventParticlesScrape` (3005), and consumes 1 axe durability.
//   - If the block is an unwaxed, unoxidized `minecraft:copper_golem_statue` and
//     a `CopperGolemStatueReviveHook` is registered, breaks the statue and spawns
//     a Copper Golem mob.
func (s *Server) UseAxeOnCopperBlock(
	dim string,
	x, y, z int,
	heldStack world.ItemStack,
	heldMeta ItemMeta,
	creative bool,
) (newStack world.ItemStack, newMeta ItemMeta, action world.CopperAxeAction, changed bool) {
	newStack, newMeta = heldStack, heldMeta
	if s == nil || !IsAxeItem(heldStack) {
		return newStack, newMeta, world.CopperAxeActionNone, false
	}
	targetDim := normalizeDispenserDim(dim)
	cache := s.BindCopperRedstone(targetDim)
	if cache == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return newStack, newMeta, world.CopperAxeActionNone, false
	}

	curState := cache.GetBlock(x, y, z)
	// Check Copper Golem Statue revival when scraping an already-unaffected, unwaxed statue.
	if world.IsCopperGolemStatueBlock(curState) && world.IsUnwaxedCopperBlock(curState) {
		if stage, ok := world.CopperOxidationStageOf(curState); ok && stage == world.CopperStageUnaffected {
			if hook := s.copperGolemReviveHook(); hook != nil {
				facing := world.BlockProperties(curState)["facing"]
				if facing == "" {
					facing = "north"
				}
				if _, revived := hook(targetDim, x, y, z, facing); revived {
					_, breakRes := cache.BreakBlockWithUpdates(x, y, z)
					s.broadcastCopperBlockUpdateResult(targetDim, breakRes)
					s.BroadcastDimensionChunk(targetDim, int32(x>>4), int32(z>>4), PlayLevelEventCB,
						EncodeLevelEventPacket(LevelEventParticlesScrape, x, y, z, 0, false))
					newStack, newMeta = damageAxeStack(heldStack, heldMeta, creative)
					return newStack, newMeta, world.CopperAxeActionScrape, true
				}
			}
		}
	}

	nextState, act, res, ok := cache.UseAxeOnCopperAt(x, y, z)
	if !ok {
		return newStack, newMeta, world.CopperAxeActionNone, false
	}
	s.broadcastCopperBlockUpdateResult(targetDim, res)

	evID := LevelEventParticlesScrape
	if act == world.CopperAxeActionUnwax {
		evID = LevelEventParticlesWaxOff
	}
	s.BroadcastDimensionChunk(targetDim, int32(x>>4), int32(z>>4), PlayLevelEventCB,
		EncodeLevelEventPacket(evID, x, y, z, 0, false))
	s.syncPartnerCopperChestState(targetDim, cache, x, y, z, nextState, evID)

	newStack, newMeta = damageAxeStack(heldStack, heldMeta, creative)
	return newStack, newMeta, act, true
}

// TryInteractCopperBlock is a unified right-click hook for B (`play.go`) when a
// player right-clicks a block at (dim, x, y, z):
//   - Honeycomb on unwaxed copper -> waxes the block (`block.honeycomb.wax_on`).
//   - Axe on waxed or oxidized copper -> unwaxes (`item.axe.wax_off`) or scrapes (`item.axe.scrape`).
//   - Hand click (when not sneaking) on copper_door / copper_trapdoor -> toggles open/close.
//   - Hand click (when not sneaking) on copper_golem_statue -> cycles pose (`standing -> sitting -> running -> star`).
func (s *Server) TryInteractCopperBlock(
	dim string,
	x, y, z int,
	heldStack world.ItemStack,
	heldMeta ItemMeta,
	sneaking bool,
	creative bool,
) (newStack world.ItemStack, newMeta ItemMeta, soundEvent string, handled bool) {
	newStack, newMeta = heldStack, heldMeta
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return newStack, newMeta, "", false
	}
	targetDim := normalizeDispenserDim(dim)
	cache := s.BindCopperRedstone(targetDim)
	if cache == nil {
		return newStack, newMeta, "", false
	}

	if !heldStack.IsEmpty() && canonicalResourceName(heldStack.ResolvedID()) == "minecraft:honeycomb" {
		if updatedStack, ok := s.WaxCopperBlockWithHoneycomb(targetDim, x, y, z, heldStack, creative); ok {
			return updatedStack, heldMeta, "minecraft:item.honeycomb.wax_on", true
		}
	}

	if IsAxeItem(heldStack) {
		if updatedStack, updatedMeta, act, ok := s.UseAxeOnCopperBlock(targetDim, x, y, z, heldStack, heldMeta, creative); ok {
			if act == world.CopperAxeActionUnwax {
				return updatedStack, updatedMeta, "minecraft:item.axe.wax_off", true
			}
			return updatedStack, updatedMeta, "minecraft:item.axe.scrape", true
		}
	}

	if !sneaking {
		state := cache.GetBlock(x, y, z)
		if world.IsCopperDoorBlock(state) || world.IsCopperTrapdoorBlock(state) {
			openNow, res, ok := cache.ToggleCopperDoorOrTrapdoor(x, y, z)
			if ok {
				s.broadcastCopperBlockUpdateResult(targetDim, res)
				if world.IsCopperDoorBlock(state) {
					if openNow {
						return newStack, newMeta, "minecraft:block.copper_door.open", true
					}
					return newStack, newMeta, "minecraft:block.copper_door.close", true
				}
				if openNow {
					return newStack, newMeta, "minecraft:block.copper_trapdoor.open", true
				}
				return newStack, newMeta, "minecraft:block.copper_trapdoor.close", true
			}
		}
		if world.IsCopperGolemStatueBlock(state) {
			if _, res, ok := cache.CycleCopperGolemStatuePoseAt(x, y, z); ok {
				s.broadcastCopperBlockUpdateResult(targetDim, res)
				return newStack, newMeta, "minecraft:block.copper.place", true
			}
		}
	}

	return newStack, newMeta, "", false
}

// RandomTickCopperBlockAt runs a full vanilla random-tick oxidation attempt at
// (dim, x, y, z) (64/1125 pre-roll + Manhattan-4 neighborhood check) and
// broadcasts any resulting state/light changes.
func (s *Server) RandomTickCopperBlockAt(dim string, x, y, z int) bool {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return false
	}
	targetDim := normalizeDispenserDim(dim)
	cache := s.BindCopperRedstone(targetDim)
	if cache == nil {
		return false
	}
	copperServerRngMu.Lock()
	nextState, res, ok := cache.TryOxidizeCopperOnRandomTick(x, y, z, copperServerRng)
	copperServerRngMu.Unlock()
	if !ok {
		return false
	}
	s.broadcastCopperBlockUpdateResult(targetDim, res)
	s.syncPartnerCopperChestState(targetDim, cache, x, y, z, nextState, 0)
	return true
}

// StepCopperOxidationAt evaluates the Manhattan-4 neighborhood oxidation chance
// at (dim, x, y, z) (assuming the 64/1125 pre-roll has succeeded) and advances
// the copper block's oxidation stage if the neighborhood roll passes.
func (s *Server) StepCopperOxidationAt(dim string, x, y, z int) bool {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return false
	}
	targetDim := normalizeDispenserDim(dim)
	cache := s.BindCopperRedstone(targetDim)
	if cache == nil {
		return false
	}
	copperServerRngMu.Lock()
	nextState, res, ok := cache.StepCopperOxidationNeighborhood(x, y, z, copperServerRng)
	copperServerRngMu.Unlock()
	if !ok {
		return false
	}
	s.broadcastCopperBlockUpdateResult(targetDim, res)
	s.syncPartnerCopperChestState(targetDim, cache, x, y, z, nextState, 0)
	return true
}

// RedirectLightningToCopperRod searches within `radius` (defaulting to 128) of
// (x, y, z) in `dim` for the nearest lightning rod of any oxidation/wax variant.
func (s *Server) RedirectLightningToCopperRod(dim string, x, y, z, radius int) (targetX, targetY, targetZ int, redirected bool) {
	if s == nil {
		return x, y, z, false
	}
	targetDim := normalizeDispenserDim(dim)
	cache := s.DimensionChunks(targetDim)
	if cache == nil {
		cache = s.chunks
	}
	if cache == nil {
		return x, y, z, false
	}
	if pos, ok := cache.FindNearestCopperLightningRod(x, y, z, radius); ok {
		return pos.X, pos.Y, pos.Z, true
	}
	return x, y, z, false
}

// OnLightningStrikeCopper handles a lightning bolt striking at (dim, x, y, z):
//   - Powers any lightning rod at (x, y, z) for 8 ticks and deoxidizes the rod if unwaxed.
//   - Deoxidizes the attached copper block completely to `unaffected` and runs
//     3..5 random walks in 3x3x3 cubes to scrape nearby unwaxed oxidized copper blocks.
//   - Broadcasts block updates and `LevelEventParticlesElectricSpark` (3002) packets.
func (s *Server) OnLightningStrikeCopper(dim string, x, y, z int) (cleaned []world.BlockPos, struckRod bool) {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return nil, false
	}
	targetDim := normalizeDispenserDim(dim)
	cache := s.BindCopperRedstone(targetDim)
	if cache == nil {
		return nil, false
	}
	struckRod = world.IsLightningRodBlock(cache.GetBlock(x, y, z))

	copperServerRngMu.Lock()
	cleaned, res := cache.CleanCopperOnLightningStrike(x, y, z, copperServerRng)
	copperServerRngMu.Unlock()

	s.broadcastCopperBlockUpdateResult(targetDim, res)
	for _, pos := range cleaned {
		s.BroadcastDimensionChunk(targetDim, int32(pos.X>>4), int32(pos.Z>>4), PlayLevelEventCB,
			EncodeLevelEventPacket(LevelEventParticlesElectricSpark, pos.X, pos.Y, pos.Z, 0, false))
	}
	return cleaned, struckRod
}

// TurnCopperGolemIntoStatue is a hook for A when a fully oxidized Copper Golem
// entity petrifies into an `oxidized_copper_golem_statue` (or `waxed_oxidized_copper_golem_statue`)
// block at (dim, x, y, z) with the given `facing` and `pose` ("standing", "sitting", "running", "star").
func (s *Server) TurnCopperGolemIntoStatue(dim string, x, y, z int, facing, pose string, waxed bool) (uint16, bool) {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return 0, false
	}
	targetDim := normalizeDispenserDim(dim)
	cache := s.BindCopperRedstone(targetDim)
	if cache == nil {
		return 0, false
	}
	switch facing {
	case "north", "south", "west", "east":
	default:
		facing = "north"
	}
	switch pose {
	case "standing", "sitting", "running", "star":
	default:
		pose = "standing"
	}
	blockName := "minecraft:oxidized_copper_golem_statue"
	if waxed {
		blockName = "minecraft:waxed_oxidized_copper_golem_statue"
	}
	waterlogged := "false"
	if cache.GetBlock(x, y, z) == world.StateWater {
		waterlogged = "true"
	}
	state, ok := world.BlockStateByName(blockName, map[string]string{
		"copper_golem_pose": pose,
		"facing":            facing,
		"waterlogged":       waterlogged,
	})
	if !ok || state == world.StateAir {
		return 0, false
	}
	applied, res := cache.ApplyBlockWithUpdates(x, y, z, state)
	if !applied {
		return 0, false
	}
	s.broadcastCopperBlockUpdateResult(targetDim, res)
	return cache.GetBlock(x, y, z), true
}
