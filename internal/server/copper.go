package server

import (
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"regionio/internal/nbt"
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
	s.SetCopperGolemStatueAt(&CopperGolemStatueBlockEntity{
		Dimension: targetDim,
		X:         x,
		Y:         y,
		Z:         z,
		Pose:      pose,
		Stage:     world.CopperStageOxidized,
		Waxed:     waxed,
	})
	s.broadcastCopperBlockUpdateResult(targetDim, res)
	return cache.GetBlock(x, y, z), true
}

// --- Copper Golem Statue Block Entity Persistence ---

// CopperGolemStatueBlockEntity stores persistent block-entity metadata for a
// `minecraft:*copper_golem_statue` block (pose, oxidation stage, waxed flag,
// optional CustomName, and original golem EntityUUID).
type CopperGolemStatueBlockEntity struct {
	Dimension  string
	X, Y, Z    int
	Pose       string
	Stage      world.CopperOxidationStage
	Waxed      bool
	CustomName string
	EntityUUID [16]byte
	HasUUID    bool
}

// Clone returns a copy of b.
func (b *CopperGolemStatueBlockEntity) Clone() *CopperGolemStatueBlockEntity {
	if b == nil {
		return nil
	}
	cp := *b
	if cp.Dimension == "" {
		cp.Dimension = world.DimensionOverworld
	}
	if cp.Pose == "" {
		cp.Pose = "standing"
	}
	return &cp
}

// EncodeCopperGolemStatueBlockEntityNBT serializes a CopperGolemStatueBlockEntity to NBT.
func EncodeCopperGolemStatueBlockEntityNBT(b *CopperGolemStatueBlockEntity) *nbt.Compound {
	if b == nil {
		return nbt.NewCompound()
	}
	dim := b.Dimension
	if dim == "" {
		dim = world.DimensionOverworld
	}
	pose := b.Pose
	if pose == "" {
		pose = "standing"
	}
	var waxedByte nbt.Byte
	if b.Waxed {
		waxedByte = 1
	}
	comp := nbt.NewCompound().
		Set("id", nbt.String("minecraft:copper_golem_statue")).
		Set("Dimension", nbt.String(world.NormalizeDimension(dim))).
		Set("x", nbt.Int(int32(b.X))).
		Set("y", nbt.Int(int32(b.Y))).
		Set("z", nbt.Int(int32(b.Z))).
		Set("Pose", nbt.String(pose)).
		Set("Stage", nbt.Int(int32(b.Stage))).
		Set("Waxed", waxedByte)
	if b.CustomName != "" {
		comp.Set("CustomName", nbt.String(b.CustomName))
	}
	if b.HasUUID {
		comp.Set("EntityUUID", uuidToNBTIntArray(b.EntityUUID))
	}
	return comp
}

// DecodeCopperGolemStatueBlockEntityNBT parses a CopperGolemStatueBlockEntity from NBT.
func DecodeCopperGolemStatueBlockEntityNBT(comp *nbt.Compound) (*CopperGolemStatueBlockEntity, bool) {
	if comp == nil {
		return nil, false
	}
	if idStr, ok := nbtStringTag(comp, "id"); ok && idStr != "" {
		if canonicalResourceName(idStr) != "minecraft:copper_golem_statue" {
			return nil, false
		}
	}
	dim, _ := nbtStringTag(comp, "Dimension")
	if dim == "" {
		dim, _ = nbtStringTag(comp, "dimension")
	}
	if dim == "" {
		dim = world.DimensionOverworld
	}
	x, _ := nbtIntTag(comp, "x")
	y, _ := nbtIntTag(comp, "y")
	z, _ := nbtIntTag(comp, "z")
	pose, _ := nbtStringTag(comp, "Pose")
	if pose == "" {
		pose = "standing"
	}
	stageInt, _ := nbtIntTag(comp, "Stage")
	var waxed bool
	if rawW, ok := comp.Get("Waxed"); ok {
		if bv, ok := rawW.(nbt.Byte); ok {
			waxed = bv != 0
		}
	}
	customName, _ := nbtStringTag(comp, "CustomName")
	b := &CopperGolemStatueBlockEntity{
		Dimension:  world.NormalizeDimension(dim),
		X:          int(x),
		Y:          int(y),
		Z:          int(z),
		Pose:       pose,
		Stage:      world.CopperOxidationStage(stageInt),
		Waxed:      waxed,
		CustomName: customName,
	}
	if rawUUID, ok := comp.Get("EntityUUID"); ok {
		if arr, ok := rawUUID.(nbt.IntArray); ok {
			if u, valid := uuidFromNBTIntArray(arr); valid {
				b.EntityUUID = u
				b.HasUUID = true
			}
		}
	}
	return b, true
}

type serverCopperBlockEntityStore struct {
	mu         sync.RWMutex
	loadedDisk bool
	statues    map[blockPosDimKey]*CopperGolemStatueBlockEntity
}

var copperBlockEntityStores sync.Map // map[*Server]*serverCopperBlockEntityStore

func (s *Server) copperBlockEntityStore() *serverCopperBlockEntityStore {
	if s == nil {
		return &serverCopperBlockEntityStore{
			statues: make(map[blockPosDimKey]*CopperGolemStatueBlockEntity),
		}
	}
	if v, ok := copperBlockEntityStores.Load(s); ok {
		st := v.(*serverCopperBlockEntityStore)
		st.mu.Lock()
		s.ensureCopperBlockEntitiesLoadedLocked(st)
		st.mu.Unlock()
		return st
	}
	st := &serverCopperBlockEntityStore{
		statues: make(map[blockPosDimKey]*CopperGolemStatueBlockEntity),
	}
	actual, _ := copperBlockEntityStores.LoadOrStore(s, st)
	res := actual.(*serverCopperBlockEntityStore)
	res.mu.Lock()
	s.ensureCopperBlockEntitiesLoadedLocked(res)
	res.mu.Unlock()
	return res
}

func (s *Server) copperSidecarPath() string {
	if s == nil || s.cfg.WorldDir == "" {
		return ""
	}
	return filepath.Join(s.cfg.WorldDir, "region", "copper_statues.nbt")
}

func (s *Server) ensureCopperBlockEntitiesLoadedLocked(st *serverCopperBlockEntityStore) {
	if st.loadedDisk {
		return
	}
	st.loadedDisk = true
	path := s.copperSidecarPath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_, parsedTag, err := nbt.UnmarshalNamed(data)
	if err != nil {
		return
	}
	root, ok := parsedTag.(*nbt.Compound)
	if !ok || root == nil {
		return
	}
	listTag, ok := root.Get("CopperStatues")
	if !ok {
		return
	}
	for _, el := range nbtListElems(listTag) {
		comp, ok := el.(*nbt.Compound)
		if !ok {
			continue
		}
		if b, ok := DecodeCopperGolemStatueBlockEntityNBT(comp); ok && b != nil {
			key := blockPosDimKey{dim: b.Dimension, x: b.X, y: b.Y, z: b.Z}
			st.statues[key] = b
		}
	}
}

// GetCopperGolemStatueAt returns a copy of the CopperGolemStatueBlockEntity at (dim, x, y, z).
func (s *Server) GetCopperGolemStatueAt(dim string, x, y, z int) (*CopperGolemStatueBlockEntity, bool) {
	if s == nil {
		return nil, false
	}
	key := blockPosDimKey{dim: world.NormalizeDimension(dim), x: x, y: y, z: z}
	st := s.copperBlockEntityStore()
	st.mu.RLock()
	b := st.statues[key].Clone()
	st.mu.RUnlock()
	if b == nil {
		return nil, false
	}
	return b, true
}

// SetCopperGolemStatueAt stores or updates a CopperGolemStatueBlockEntity.
func (s *Server) SetCopperGolemStatueAt(b *CopperGolemStatueBlockEntity) {
	if s == nil || b == nil {
		return
	}
	cp := b.Clone()
	cp.Dimension = world.NormalizeDimension(cp.Dimension)
	key := blockPosDimKey{dim: cp.Dimension, x: cp.X, y: cp.Y, z: cp.Z}
	st := s.copperBlockEntityStore()
	st.mu.Lock()
	st.statues[key] = cp
	st.mu.Unlock()
}

// RemoveCopperGolemStatueAt removes a CopperGolemStatueBlockEntity at (dim, x, y, z).
func (s *Server) RemoveCopperGolemStatueAt(dim string, x, y, z int) {
	if s == nil {
		return
	}
	key := blockPosDimKey{dim: world.NormalizeDimension(dim), x: x, y: y, z: z}
	st := s.copperBlockEntityStore()
	st.mu.Lock()
	delete(st.statues, key)
	st.mu.Unlock()
}

// SaveCopperBlockEntities persists all CopperGolemStatueBlockEntity entries to
// <WorldDir>/region/copper_statues.nbt.
func (s *Server) SaveCopperBlockEntities() error {
	if s == nil {
		return nil
	}
	path := s.copperSidecarPath()
	if path == "" {
		return nil
	}
	st := s.copperBlockEntityStore()
	st.mu.RLock()
	keys := make([]blockPosDimKey, 0, len(st.statues))
	for k, b := range st.statues {
		if b != nil {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dim != keys[j].dim {
			return keys[i].dim < keys[j].dim
		}
		if keys[i].x != keys[j].x {
			return keys[i].x < keys[j].x
		}
		if keys[i].y != keys[j].y {
			return keys[i].y < keys[j].y
		}
		return keys[i].z < keys[j].z
	})
	elems := make([]nbt.Tag, 0, len(keys))
	for _, k := range keys {
		elems = append(elems, EncodeCopperGolemStatueBlockEntityNBT(st.statues[k]))
	}
	st.mu.RUnlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	root := nbt.NewCompound().Set("CopperStatues", nbt.List{ElemID: nbt.TagCompound, Elems: elems})
	return os.WriteFile(path, nbt.MarshalNamed("", root), 0o644)
}

// LoadCopperBlockEntities reloads CopperGolemStatueBlockEntity entries from disk.
func (s *Server) LoadCopperBlockEntities() error {
	if s == nil {
		return nil
	}
	st := s.copperBlockEntityStore()
	st.mu.Lock()
	st.loadedDisk = false
	st.statues = make(map[blockPosDimKey]*CopperGolemStatueBlockEntity)
	s.ensureCopperBlockEntitiesLoadedLocked(st)
	st.mu.Unlock()
	return nil
}

// ChunkCopperBlockEntitiesNBT returns NBT compounds for all copper golem statues in chunk (cx, cz) of dim.
func (s *Server) ChunkCopperBlockEntitiesNBT(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	st := s.copperBlockEntityStore()
	st.mu.RLock()
	var out []*nbt.Compound
	for k, b := range st.statues {
		if b == nil || k.dim != normDim {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			out = append(out, EncodeCopperGolemStatueBlockEntityNBT(b))
		}
	}
	st.mu.RUnlock()
	return out
}

// UnloadChunkCopperBlockEntities removes all copper golem statues in chunk (cx, cz) of dim
// from runtime memory and returns their serialized NBT compounds.
func (s *Server) UnloadChunkCopperBlockEntities(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	st := s.copperBlockEntityStore()
	st.mu.Lock()
	var out []*nbt.Compound
	for k, b := range st.statues {
		if k.dim != normDim {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			if b != nil {
				out = append(out, EncodeCopperGolemStatueBlockEntityNBT(b))
			}
			delete(st.statues, k)
		}
	}
	st.mu.Unlock()
	return out
}

// LoadChunkCopperBlockEntitiesNBT loads copper golem statue block entities from a chunk's block_entities NBT list.
func (s *Server) LoadChunkCopperBlockEntitiesNBT(dim string, list []*nbt.Compound) int {
	if s == nil || len(list) == 0 {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	st := s.copperBlockEntityStore()
	st.mu.Lock()
	count := 0
	for _, comp := range list {
		if b, ok := DecodeCopperGolemStatueBlockEntityNBT(comp); ok && b != nil {
			b.Dimension = normDim
			key := blockPosDimKey{dim: normDim, x: b.X, y: b.Y, z: b.Z}
			st.statues[key] = b
			count++
		}
	}
	st.mu.Unlock()
	return count
}

