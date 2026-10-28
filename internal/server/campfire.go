package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/world"
)

// Campfire cooking and damage constants matching vanilla Minecraft 26.1.2.
const (
	CampfireSlotCount             = 4
	CampfireDefaultCookTime       = DefaultCampfireCookTime // 600 game ticks (30 seconds)
	CampfireRegularDamage         = float32(1.0)
	CampfireSoulDamage            = float32(2.0)
	LevelEventSoundExtinguishFire = int32(1009)

	// BlockEntityTypeCampfire is BuiltInRegistries.BLOCK_ENTITY_TYPE index 33 ("minecraft:campfire").
	BlockEntityTypeCampfire int32 = 33
)

// CampfireAction describes the result of a player interacting with a campfire.
type CampfireAction uint8

const (
	CampfireActionNone CampfireAction = iota
	CampfireActionPlacedFood
	CampfireActionExtinguished
	CampfireActionLit
)

// CampfireBlockEntity represents the persistent and runtime state of a
// minecraft:campfire or minecraft:soul_campfire block entity.
type CampfireBlockEntity struct {
	ID              string // "minecraft:campfire" or "minecraft:soul_campfire"
	Dimension       string
	X, Y, Z         int
	Items           [CampfireSlotCount]world.ItemStack
	CookingProgress [CampfireSlotCount]int16 // NBT "CookingTimes" (4 ints)
	CookingTime     [CampfireSlotCount]int16 // NBT "CookingTotalTimes" (4 ints)
}

// Clone returns a copy of be.
func (be CampfireBlockEntity) Clone() CampfireBlockEntity {
	return be
}

// IsCampfireBlockID reports whether id is "minecraft:campfire" or "minecraft:soul_campfire".
func IsCampfireBlockID(id string) bool {
	switch canonicalResourceName(id) {
	case "minecraft:campfire", "minecraft:soul_campfire":
		return true
	default:
		return false
	}
}

// IsCampfireBlock reports whether state is a campfire or soul_campfire block.
func IsCampfireBlock(state uint16) bool {
	return IsCampfireBlockID(world.BlockName(state))
}

// IsSoulCampfireBlock reports whether state is a minecraft:soul_campfire block.
func IsSoulCampfireBlock(state uint16) bool {
	return world.BlockName(state) == "minecraft:soul_campfire"
}

// CampfireStateLit reports whether state has property lit="true".
func CampfireStateLit(state uint16) bool {
	if !IsCampfireBlock(state) {
		return false
	}
	return world.BlockProperties(state)["lit"] == "true"
}

// CampfireStateSignalFire reports whether state has property signal_fire="true"
// (when a hay_block is directly below the campfire).
func CampfireStateSignalFire(state uint16) bool {
	if !IsCampfireBlock(state) {
		return false
	}
	return world.BlockProperties(state)["signal_fire"] == "true"
}

// CampfireStateWaterlogged reports whether state has property waterlogged="true".
func CampfireStateWaterlogged(state uint16) bool {
	if !IsCampfireBlock(state) {
		return false
	}
	return world.BlockProperties(state)["waterlogged"] == "true"
}

// SetCampfireStateLit returns the block state ID for the campfire at state with
// its "lit" property set to lit (cannot be lit if waterlogged="true").
func SetCampfireStateLit(state uint16, lit bool) (uint16, bool) {
	if !IsCampfireBlock(state) {
		return state, false
	}
	if lit && CampfireStateWaterlogged(state) {
		return state, false
	}
	val := "false"
	if lit {
		val = "true"
	}
	return world.WithBlockProperty(state, "lit", val)
}

// SetCampfireStateSignalFire returns the block state ID for the campfire at
// state with its "signal_fire" property set to signalFire.
func SetCampfireStateSignalFire(state uint16, signalFire bool) (uint16, bool) {
	if !IsCampfireBlock(state) {
		return state, false
	}
	val := "false"
	if signalFire {
		val = "true"
	}
	return world.WithBlockProperty(state, "signal_fire", val)
}

// SetCampfireStateWaterlogged returns the block state ID for the campfire at
// state with its "waterlogged" property set to waterlogged. When waterlogged is
// true, "lit" is also forced to "false" matching vanilla CampfireBlock.placeLiquid.
func SetCampfireStateWaterlogged(state uint16, waterlogged bool) (uint16, bool) {
	if !IsCampfireBlock(state) {
		return state, false
	}
	val := "false"
	if waterlogged {
		val = "true"
	}
	cur, ok := world.WithBlockProperty(state, "waterlogged", val)
	if !ok {
		return state, false
	}
	if waterlogged {
		if unlit, ok2 := world.WithBlockProperty(cur, "lit", "false"); ok2 {
			cur = unlit
		}
	}
	return cur, true
}

// IsShovelItem reports whether itemName is a shovel tool capable of
// extinguishing a lit campfire on right-click.
func IsShovelItem(itemName string) bool {
	norm := canonicalResourceName(itemName)
	return strings.HasSuffix(norm, "_shovel")
}

// MatchCampfireRecipe finds the vanilla 26.1.2 `minecraft:campfire_cooking`
// recipe for input (e.g. raw beef, porkchop, chicken, mutton, rabbit, cod,
// salmon, potato, kelp -> cooked result in 600 ticks).
func MatchCampfireRecipe(input world.ItemStack) (*SmeltingRecipe, bool) {
	return DefaultSmeltingBook().Match("minecraft:campfire_cooking", input)
}

// CanCookOnCampfire reports whether stack can be placed on a campfire for cooking.
func CanCookOnCampfire(stack world.ItemStack) bool {
	_, ok := MatchCampfireRecipe(stack)
	return ok
}

type campfireKey struct {
	dim     string
	x, y, z int
}

type serverCampfireState struct {
	mu          sync.Mutex
	loadedStore bool
	campfires   map[campfireKey]*CampfireBlockEntity
}

var serverCampfires sync.Map // map[*Server]*serverCampfireState

func normalizeCampfireDim(dim string) string {
	switch strings.ToLower(strings.TrimSpace(dim)) {
	case "nether", "the_nether", "minecraft:the_nether":
		return world.DimensionNether
	case "end", "the_end", "minecraft:the_end":
		return world.DimensionEnd
	default:
		return world.DimensionOverworld
	}
}

func (s *Server) campfireState() *serverCampfireState {
	if v, ok := serverCampfires.Load(s); ok {
		return v.(*serverCampfireState)
	}
	actual, _ := serverCampfires.LoadOrStore(s, &serverCampfireState{
		campfires: make(map[campfireKey]*CampfireBlockEntity),
	})
	return actual.(*serverCampfireState)
}

func (s *Server) ensureCampfiresLoadedLocked(cs *serverCampfireState) {
	if cs.loadedStore {
		return
	}
	cs.loadedStore = true
	if s == nil {
		return
	}
	persisted, err := s.loadPersistedCampfires()
	if err != nil {
		return
	}
	for _, be := range persisted {
		normDim := normalizeCampfireDim(be.Dimension)
		key := campfireKey{dim: normDim, x: be.X, y: be.Y, z: be.Z}
		if _, exists := cs.campfires[key]; !exists {
			cp := be.Clone()
			cp.Dimension = normDim
			cs.campfires[key] = &cp
		}
	}
}

func (s *Server) resolveCampfireIDAt(dim string, x, y, z int, fallback string) string {
	if s != nil {
		if cache := s.DimensionChunks(dim); cache != nil {
			name := world.BlockName(cache.GetBlock(x, y, z))
			if IsCampfireBlockID(name) {
				return name
			}
		}
	}
	if IsCampfireBlockID(fallback) {
		return canonicalResourceName(fallback)
	}
	return "minecraft:campfire"
}

func (s *Server) getOrCreateCampfireLocked(cs *serverCampfireState, dim string, x, y, z int, fallbackID string) *CampfireBlockEntity {
	s.ensureCampfiresLoadedLocked(cs)
	normDim := normalizeCampfireDim(dim)
	key := campfireKey{dim: normDim, x: x, y: y, z: z}
	be := cs.campfires[key]
	resolvedID := s.resolveCampfireIDAt(normDim, x, y, z, fallbackID)
	if be == nil {
		be = &CampfireBlockEntity{
			ID:        resolvedID,
			Dimension: normDim,
			X:         x,
			Y:         y,
			Z:         z,
		}
		cs.campfires[key] = be
	} else if IsCampfireBlockID(resolvedID) {
		be.ID = resolvedID
	}
	return be
}

// CampfireAt returns a snapshot of the campfire block entity at (dim, x, y, z).
func (s *Server) CampfireAt(dim string, x, y, z int) (CampfireBlockEntity, bool) {
	if s == nil {
		return CampfireBlockEntity{}, false
	}
	normDim := normalizeCampfireDim(dim)
	cs := s.campfireState()
	cs.mu.Lock()
	s.ensureCampfiresLoadedLocked(cs)
	be := cs.campfires[campfireKey{dim: normDim, x: x, y: y, z: z}]
	if be != nil {
		cp := be.Clone()
		cs.mu.Unlock()
		return cp, true
	}
	cs.mu.Unlock()

	if cache := s.DimensionChunks(normDim); cache != nil {
		state := cache.GetBlock(x, y, z)
		if IsCampfireBlock(state) {
			return CampfireBlockEntity{
				ID:        world.BlockName(state),
				Dimension: normDim,
				X:         x,
				Y:         y,
				Z:         z,
			}, true
		}
	}
	return CampfireBlockEntity{}, false
}

// SetCampfireState replaces or inserts the campfire block entity at (be.Dimension, be.X, be.Y, be.Z).
func (s *Server) SetCampfireState(be CampfireBlockEntity) {
	if s == nil {
		return
	}
	normDim := normalizeCampfireDim(be.Dimension)
	cs := s.campfireState()
	cs.mu.Lock()
	s.ensureCampfiresLoadedLocked(cs)
	cp := be.Clone()
	cp.Dimension = normDim
	cp.ID = s.resolveCampfireIDAt(normDim, be.X, be.Y, be.Z, be.ID)
	cs.campfires[campfireKey{dim: normDim, x: be.X, y: be.Y, z: be.Z}] = &cp
	cs.mu.Unlock()
}

// CampfireSlots returns a copy of the 4 cooking slots in the campfire at (dim, x, y, z).
func (s *Server) CampfireSlots(dim string, x, y, z int) [CampfireSlotCount]world.ItemStack {
	be, ok := s.CampfireAt(dim, x, y, z)
	if !ok {
		return [CampfireSlotCount]world.ItemStack{}
	}
	return be.Items
}

// PlaceFoodOnCampfire places 1 raw food item from stack into the first empty
// cooking slot (0..3) of the campfire at (dim, x, y, z) if stack matches a
// vanilla `minecraft:campfire_cooking` recipe.
func (s *Server) PlaceFoodOnCampfire(dim string, x, y, z int, stack world.ItemStack) (int, bool) {
	if s == nil || stack.IsEmpty() || stack.Count <= 0 {
		return -1, false
	}
	normDim := normalizeCampfireDim(dim)
	if cache := s.DimensionChunks(normDim); cache != nil {
		state := cache.GetBlock(x, y, z)
		if !IsCampfireBlock(state) {
			return -1, false
		}
	}
	rec, ok := MatchCampfireRecipe(stack)
	if !ok {
		return -1, false
	}
	cookTime := rec.CookingTime
	if cookTime <= 0 {
		cookTime = CampfireDefaultCookTime
	}

	cs := s.campfireState()
	cs.mu.Lock()
	be := s.getOrCreateCampfireLocked(cs, normDim, x, y, z, "")
	placedSlot := -1
	for slot := 0; slot < CampfireSlotCount; slot++ {
		if be.Items[slot].IsEmpty() {
			be.Items[slot] = world.NewItemStack(stack.ResolvedItemID(), 1)
			be.CookingProgress[slot] = 0
			be.CookingTime[slot] = cookTime
			placedSlot = slot
			break
		}
	}
	cs.mu.Unlock()
	if placedSlot >= 0 {
		s.BroadcastCampfireBlockEntityData(normDim, x, y, z)
		return placedSlot, true
	}
	return -1, false
}

// BroadcastCampfireBlockEntityData broadcasts ClientboundBlockEntityDataPacket
// (`0x06`, `BlockEntityTypeCampfire = 33`) for the campfire at (dim, x, y, z).
func (s *Server) BroadcastCampfireBlockEntityData(dim string, x, y, z int) {
	if s == nil {
		return
	}
	normDim := normalizeCampfireDim(dim)
	be, ok := s.CampfireAt(normDim, x, y, z)
	if !ok {
		return
	}
	pkt := EncodeBlockEntityDataPacket(x, y, z, BlockEntityTypeCampfire, EncodeCampfireBlockEntityNBT(be))
	s.BroadcastDimensionChunk(normDim, int32(x>>4), int32(z>>4), PlayBlockEntityDataCB, pkt)
}

// ExtinguishCampfire extinguishes a campfire at (dim, x, y, z), optionally
// marking it waterlogged="true", drops all items currently in the 4 cooking
// slots into the world (matching vanilla CampfireBlock.dowse -> CampfireBlockEntity.dowse),
// updates block light, and broadcasts the fire extinguish level event (1009).
func (s *Server) ExtinguishCampfire(dim string, x, y, z int, waterlogged bool) bool {
	if s == nil {
		return false
	}
	normDim := normalizeCampfireDim(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	if !IsCampfireBlock(state) {
		return false
	}
	wasLit := CampfireStateLit(state)
	wasWaterlogged := CampfireStateWaterlogged(state)
	if !wasLit && (!waterlogged || wasWaterlogged) {
		return false
	}

	nextState := state
	if waterlogged {
		if st, ok := SetCampfireStateWaterlogged(nextState, true); ok {
			nextState = st
		}
	} else {
		if st, ok := SetCampfireStateLit(nextState, false); ok {
			nextState = st
		}
	}

	// Dowse the campfire block entity: pop any cooking items into the world and reset slots.
	var dropped []world.ItemStack
	cs := s.campfireState()
	cs.mu.Lock()
	s.ensureCampfiresLoadedLocked(cs)
	if be := cs.campfires[campfireKey{dim: normDim, x: x, y: y, z: z}]; be != nil {
		for i := 0; i < CampfireSlotCount; i++ {
			if !be.Items[i].IsEmpty() {
				dropped = append(dropped, be.Items[i])
			}
			be.Items[i] = world.ItemStack{}
			be.CookingProgress[i] = 0
			be.CookingTime[i] = 0
		}
	}
	cs.mu.Unlock()

	if nextState != state {
		_, res := cache.ApplyBlockWithUpdates(x, y, z, nextState)
		s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
	}
	for _, st := range dropped {
		_, _ = s.SpawnItemEntityInDimension(normDim, float64(x)+0.5, float64(y)+0.4, float64(z)+0.5, st)
	}
	if len(dropped) > 0 {
		s.BroadcastCampfireBlockEntityData(normDim, x, y, z)
	}
	if wasLit {
		s.BroadcastDimensionChunk(normDim, int32(x>>4), int32(z>>4), PlayLevelEventCB,
			EncodeLevelEventPacket(LevelEventSoundExtinguishFire, x, y, z, 0, false))
	}
	return true
}

// LightCampfire ignites an unlit, non-waterlogged campfire or soul_campfire at
// (dim, x, y, z), updating its block state to lit="true" and recalculating block light.
func (s *Server) LightCampfire(dim string, x, y, z int) bool {
	if s == nil {
		return false
	}
	normDim := normalizeCampfireDim(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	if !IsCampfireBlock(state) || CampfireStateLit(state) || CampfireStateWaterlogged(state) {
		return false
	}
	nextState, ok := SetCampfireStateLit(state, true)
	if !ok || nextState == state {
		return false
	}
	_, res := cache.ApplyBlockWithUpdates(x, y, z, nextState)
	s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
	return true
}

// TryIgniteCampfireWithProjectile ignites an unlit, non-waterlogged campfire at
// (dim, x, y, z) when hit by a burning projectile (e.g. flame arrow or small fireball).
func (s *Server) TryIgniteCampfireWithProjectile(dim string, x, y, z int, projectileOnFire bool) bool {
	if !projectileOnFire {
		return false
	}
	return s.LightCampfire(dim, x, y, z)
}

// UpdateCampfireSignalFire checks whether the block below (dim, x, y, z) is
// `minecraft:hay_block` and updates the campfire's `signal_fire` property accordingly.
func (s *Server) UpdateCampfireSignalFire(dim string, x, y, z int) bool {
	if s == nil {
		return false
	}
	normDim := normalizeCampfireDim(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	if !IsCampfireBlock(state) {
		return false
	}
	wantSignal := y > world.MinY && world.BlockName(cache.GetBlock(x, y-1, z)) == "minecraft:hay_block"
	if CampfireStateSignalFire(state) == wantSignal {
		return wantSignal
	}
	if nextState, ok := SetCampfireStateSignalFire(state, wantSignal); ok && nextState != state {
		_, res := cache.ApplyBlockWithUpdates(x, y, z, nextState)
		s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
	}
	return wantSignal
}

// InteractCampfire handles a player right-clicking a campfire at (x, y, z),
// checking main hand (0) first and falling back to off-hand (1).
func (s *Server) InteractCampfire(session *PlayerSession, x, y, z int) (CampfireAction, bool) {
	if act, ok := s.InteractCampfireWithHand(session, 0, x, y, z); ok {
		return act, true
	}
	return s.InteractCampfireWithHand(session, 1, x, y, z)
}

// InteractCampfireWithHand handles a player right-clicking a campfire at (x, y, z)
// with `hand` (0 = main hand, 1 = off-hand):
//   - Raw food matching `minecraft:campfire_cooking`: places 1 item onto an empty
//     campfire slot (0..3), consumes 1 item in Survival/Adventure, and increments
//     `minecraft:interact_with_campfire`.
//   - Shovel (`*_shovel`) on a lit campfire: extinguishes the campfire, drops
//     any cooking items, and damages the shovel by 1 durability.
//   - `minecraft:water_bucket` on a non-waterlogged campfire: waterlogs and
//     extinguishes the campfire, dropping any cooking items and returning `minecraft:bucket`.
//   - `minecraft:flint_and_steel` or `minecraft:fire_charge` on an unlit,
//     non-waterlogged campfire: relights the campfire (`lit="true"`).
func (s *Server) InteractCampfireWithHand(session *PlayerSession, hand int32, x, y, z int) (CampfireAction, bool) {
	if s == nil || session == nil || session.IsDead() {
		return CampfireActionNone, false
	}
	dim := normalizeCampfireDim(session.Dimension())
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return CampfireActionNone, false
	}
	state := cache.GetBlock(x, y, z)
	if !IsCampfireBlock(state) {
		return CampfireActionNone, false
	}
	slot, held := session.handSlotAndStack(hand)
	if held.IsEmpty() {
		return CampfireActionNone, false
	}
	heldID := canonicalResourceName(held.ResolvedID())
	creative := session.Snapshot().GameMode == GameModeCreative

	// 1. Place raw food into one of the 4 cooking slots.
	if CanCookOnCampfire(held) {
		if _, ok := s.PlaceFoodOnCampfire(dim, x, y, z, held); ok {
			if !creative {
				if held.Count <= 1 {
					session.SetInventorySlot(slot, world.ItemStack{})
				} else {
					session.SetInventorySlot(slot, world.NewItemStack(held.ResolvedItemID(), held.Count-1))
				}
			}
			s.AwardCustomStat(session, StatInteractWithCampfire, 1)
			s.EmitPlayerBlockVibration(session, GameEventBlockChange, dim, x, y, z, world.BlockName(state))
			return CampfireActionPlacedFood, true
		}
		return CampfireActionNone, false
	}

	// 2. Extinguish lit campfire with a shovel.
	if IsShovelItem(heldID) && CampfireStateLit(state) {
		if s.ExtinguishCampfire(dim, x, y, z, false) {
			if !creative {
				if hand == 0 {
					s.DamageHeldItem(session, 1)
				}
			}
			s.EmitPlayerBlockVibration(session, GameEventBlockChange, dim, x, y, z, world.BlockName(state))
			return CampfireActionExtinguished, true
		}
		return CampfireActionNone, false
	}

	// 3. Waterlog and extinguish campfire with a water_bucket.
	if heldID == "minecraft:water_bucket" && !CampfireStateWaterlogged(state) {
		if s.ExtinguishCampfire(dim, x, y, z, true) {
			if !creative {
				replaceOrGiveBucketItem(session, slot, held, world.NewNamedItemStack("minecraft:bucket", 1))
			}
			s.EmitPlayerBlockVibration(session, GameEventBlockChange, dim, x, y, z, world.BlockName(state))
			return CampfireActionExtinguished, true
		}
		return CampfireActionNone, false
	}

	// 4. Relight unlit, non-waterlogged campfire with flint_and_steel or fire_charge.
	if (heldID == "minecraft:flint_and_steel" || heldID == "minecraft:fire_charge") &&
		!CampfireStateLit(state) && !CampfireStateWaterlogged(state) {
		if s.LightCampfire(dim, x, y, z) {
			if !creative {
				if heldID == "minecraft:flint_and_steel" {
					if hand == 0 {
						s.DamageHeldItem(session, 1)
					}
				} else {
					if held.Count <= 1 {
						session.SetInventorySlot(slot, world.ItemStack{})
					} else {
						session.SetInventorySlot(slot, world.NewItemStack(held.ResolvedItemID(), held.Count-1))
					}
				}
			}
			s.EmitPlayerBlockVibration(session, GameEventBlockChange, dim, x, y, z, world.BlockName(state))
			return CampfireActionLit, true
		}
	}

	return CampfireActionNone, false
}

// ExtinguishCampfireWithWaterSplash extinguishes any lit campfire at (x, y, z)
// and its 4 horizontal neighbors in dim when a water-based splash or lingering
// potion (`PotionWater`, `PotionMundane`, `PotionThick`, `PotionAwkward`) impacts.
func (s *Server) ExtinguishCampfireWithWaterSplash(dim string, x, y, z int, potionType string) int {
	if s == nil {
		return 0
	}
	switch canonicalPotionID(potionType) {
	case "", PotionWater, PotionMundane, PotionThick, PotionAwkward:
	default:
		return 0
	}
	normDim := normalizeCampfireDim(dim)
	offsets := [][3]int{
		{0, 0, 0},
		{1, 0, 0},
		{-1, 0, 0},
		{0, 0, 1},
		{0, 0, -1},
	}
	extinguished := 0
	for _, off := range offsets {
		if s.ExtinguishCampfire(normDim, x+off[0], y+off[1], z+off[2], false) {
			extinguished++
		}
	}
	return extinguished
}

// TickCampfireAt advances the campfire block entity at (dim, x, y, z) by one
// game tick, matching vanilla 26.1.2 CampfireBlockEntity.cookTick (when lit)
// and CampfireBlockEntity.cooldownTick (when unlit). When a slot reaches its
// cooking time (600 ticks), the cooked result is spawned as an ItemEntity in
// the world and returned in cooked. If the campfire block has become waterlogged,
// any uncooked items in its slots are immediately doused and dropped into the world.
func (s *Server) TickCampfireAt(dim string, x, y, z int) (cooked []world.ItemStack, changed bool) {
	if s == nil {
		return nil, false
	}
	normDim := normalizeCampfireDim(dim)
	isLit := true
	isWaterlogged := false
	if cache := s.DimensionChunks(normDim); cache != nil {
		state := cache.GetBlock(x, y, z)
		if !IsCampfireBlock(state) {
			s.dropAndRemoveCampfireEntity(normDim, x, y, z)
			return nil, false
		}
		isWaterlogged = CampfireStateWaterlogged(state)
		isLit = CampfireStateLit(state) && !isWaterlogged
	}

	var dousedDrops []world.ItemStack
	cs := s.campfireState()
	cs.mu.Lock()
	s.ensureCampfiresLoadedLocked(cs)
	be := cs.campfires[campfireKey{dim: normDim, x: x, y: y, z: z}]
	if be == nil {
		cs.mu.Unlock()
		return nil, false
	}

	if isWaterlogged {
		for slot := 0; slot < CampfireSlotCount; slot++ {
			if !be.Items[slot].IsEmpty() {
				dousedDrops = append(dousedDrops, be.Items[slot])
				be.Items[slot] = world.ItemStack{}
				be.CookingProgress[slot] = 0
				be.CookingTime[slot] = 0
				changed = true
			}
		}
	} else if isLit {
		for slot := 0; slot < CampfireSlotCount; slot++ {
			st := be.Items[slot]
			if st.IsEmpty() {
				continue
			}
			changed = true
			be.CookingProgress[slot]++
			target := be.CookingTime[slot]
			if target <= 0 {
				target = CampfireDefaultCookTime
				be.CookingTime[slot] = target
			}
			if be.CookingProgress[slot] >= target {
				res := st
				if rec, ok := MatchCampfireRecipe(st); ok && !rec.Result.IsEmpty() {
					res = world.NewItemStack(rec.Result.ResolvedItemID(), 1)
				}
				be.Items[slot] = world.ItemStack{}
				be.CookingProgress[slot] = 0
				be.CookingTime[slot] = 0
				cooked = append(cooked, res)
			}
		}
	} else {
		// CampfireBlockEntity.cooldownTick: decay cooking progress by 2 per tick when unlit.
		for slot := 0; slot < CampfireSlotCount; slot++ {
			if be.CookingProgress[slot] > 0 {
				next := be.CookingProgress[slot] - 2
				if next < 0 {
					next = 0
				}
				be.CookingProgress[slot] = next
				changed = true
			}
		}
	}
	cs.mu.Unlock()

	for _, item := range dousedDrops {
		_, _ = s.SpawnItemEntityInDimension(normDim, float64(x)+0.5, float64(y)+0.4, float64(z)+0.5, item)
	}
	for _, item := range cooked {
		_, _ = s.SpawnItemEntityInDimension(normDim, float64(x)+0.5, float64(y)+0.4, float64(z)+0.5, item)
	}
	if len(dousedDrops) > 0 || len(cooked) > 0 {
		s.BroadcastCampfireBlockEntityData(normDim, x, y, z)
	}
	return cooked, changed
}

// TickCampfires advances all tracked campfire block entities by one game tick
// and returns the number of campfires whose cooking state changed.
func (s *Server) TickCampfires() int {
	if s == nil {
		return 0
	}
	cs := s.campfireState()
	cs.mu.Lock()
	s.ensureCampfiresLoadedLocked(cs)
	keys := make([]campfireKey, 0, len(cs.campfires))
	for k := range cs.campfires {
		keys = append(keys, k)
	}
	cs.mu.Unlock()

	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dim != keys[j].dim {
			return keys[i].dim < keys[j].dim
		}
		if keys[i].y != keys[j].y {
			return keys[i].y < keys[j].y
		}
		if keys[i].z != keys[j].z {
			return keys[i].z < keys[j].z
		}
		return keys[i].x < keys[j].x
	})

	changedCount := 0
	for _, k := range keys {
		if _, changed := s.TickCampfireAt(k.dim, k.x, k.y, k.z); changed {
			changedCount++
		}
	}
	return changedCount
}

func (s *Server) dropAndRemoveCampfireEntity(dim string, x, y, z int) []world.ItemStack {
	normDim := normalizeCampfireDim(dim)
	cs := s.campfireState()
	cs.mu.Lock()
	s.ensureCampfiresLoadedLocked(cs)
	key := campfireKey{dim: normDim, x: x, y: y, z: z}
	be := cs.campfires[key]
	delete(cs.campfires, key)
	cs.mu.Unlock()

	if be == nil {
		return nil
	}
	var dropped []world.ItemStack
	for _, st := range be.Items {
		if !st.IsEmpty() {
			dropped = append(dropped, st)
			_, _ = s.SpawnItemEntityInDimension(normDim, float64(x)+0.5, float64(y)+0.25, float64(z)+0.5, st)
		}
	}
	return dropped
}

// BreakCampfireAt removes the campfire block entity at (dim, x, y, z), drops
// all uncooked items from the 4 slots, and spawns the vanilla block loot drop:
//   - With Silk Touch: 1x `minecraft:campfire` or 1x `minecraft:soul_campfire`.
//   - Without Silk Touch: 2x `minecraft:charcoal` (for `minecraft:campfire`) or
//     1x `minecraft:soul_soil` (for `minecraft:soul_campfire`).
func (s *Server) BreakCampfireAt(dim string, x, y, z int, silkTouch bool) []world.ItemStack {
	if s == nil {
		return nil
	}
	normDim := normalizeCampfireDim(dim)
	blockID := "minecraft:campfire"
	if cache := s.DimensionChunks(normDim); cache != nil {
		st := cache.GetBlock(x, y, z)
		if IsCampfireBlock(st) {
			blockID = world.BlockName(st)
		}
	} else if be, ok := s.CampfireAt(normDim, x, y, z); ok && IsCampfireBlockID(be.ID) {
		blockID = be.ID
	}

	dropped := s.dropAndRemoveCampfireEntity(normDim, x, y, z)

	var blockDrop world.ItemStack
	if silkTouch {
		blockDrop = world.NewNamedItemStack(blockID, 1)
	} else if blockID == "minecraft:soul_campfire" {
		blockDrop = world.NewNamedItemStack("minecraft:soul_soil", 1)
	} else {
		blockDrop = world.NewNamedItemStack("minecraft:charcoal", 2)
	}
	if !blockDrop.IsEmpty() {
		dropped = append(dropped, blockDrop)
		_, _ = s.SpawnItemEntityInDimension(normDim, float64(x)+0.5, float64(y)+0.25, float64(z)+0.5, blockDrop)
	}
	return dropped
}

// CampfireContactDamageForState returns the per-tick fire contact damage for a
// campfire block state (1.0 for lit `minecraft:campfire`, 2.0 for lit
// `minecraft:soul_campfire`, 0 when unlit or waterlogged).
func CampfireContactDamageForState(state uint16) (float32, bool) {
	if !IsCampfireBlock(state) || !CampfireStateLit(state) || CampfireStateWaterlogged(state) {
		return 0, false
	}
	if IsSoulCampfireBlock(state) {
		return CampfireSoulDamage, true
	}
	return CampfireRegularDamage, true
}

// CampfireContactDamageAt returns the fire contact damage for the campfire at
// (dim, x, y, z) if it is currently lit.
func (s *Server) CampfireContactDamageAt(dim string, x, y, z int) (float32, bool) {
	if s == nil {
		return 0, false
	}
	cache := s.DimensionChunks(normalizeCampfireDim(dim))
	if cache == nil {
		return 0, false
	}
	return CampfireContactDamageForState(cache.GetBlock(x, y, z))
}

// ApplyCampfireContactDamageToPlayer checks whether session is standing on a
// lit campfire or soul_campfire and applies vanilla `in_fire` contact damage
// (1.0 HP for campfire, 2.0 HP for soul_campfire).
func (s *Server) ApplyCampfireContactDamageToPlayer(session *PlayerSession) (float32, bool) {
	if s == nil || session == nil || session.IsDead() {
		return 0, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator {
		return 0, false
	}
	if session.HasPlayerEffect(EffectFireResistance) {
		return 0, false
	}
	bx := int(math.Floor(snap.X))
	by := int(math.Floor(snap.Y))
	bz := int(math.Floor(snap.Z))
	dmg, ok := s.CampfireContactDamageAt(session.Dimension(), bx, by, bz)
	if !ok || dmg <= 0 {
		return 0, false
	}
	beforeHP := snap.Health
	s.DamagePlayer(session, dmg, "in_fire")
	afterHP := session.Snapshot().Health
	if afterHP < beforeHP {
		return beforeHP - afterHP, true
	}
	return 0, false
}

// EncodeCampfireBlockEntityNBT serializes a CampfireBlockEntity into its
// vanilla 26.1.2 block entity NBT compound (`Items`, `CookingTimes`, `CookingTotalTimes`).
func EncodeCampfireBlockEntityNBT(be CampfireBlockEntity) *nbt.Compound {
	id := canonicalResourceName(be.ID)
	if !IsCampfireBlockID(id) {
		id = "minecraft:campfire"
	}
	normDim := normalizeCampfireDim(be.Dimension)
	c := nbt.NewCompound().
		Set("id", nbt.String(id)).
		Set("Dimension", nbt.String(normDim)).
		Set("x", nbt.Int(int32(be.X))).
		Set("y", nbt.Int(int32(be.Y))).
		Set("z", nbt.Int(int32(be.Z))).
		Set("keepPacked", nbt.Byte(0))

	items := nbt.List{ElemID: nbt.TagCompound}
	for slot := 0; slot < CampfireSlotCount; slot++ {
		st := be.Items[slot]
		if st.IsEmpty() {
			continue
		}
		itemName := st.ResolvedID()
		if itemName == "" || itemName == "minecraft:air" {
			continue
		}
		entry := nbt.NewCompound().
			Set("Slot", nbt.Byte(int8(slot))).
			Set("id", nbt.String(itemName)).
			Set("count", nbt.Int(st.Count))
		items.Elems = append(items.Elems, entry)
	}
	c.Set("Items", items)

	cookingTimes := make(nbt.IntArray, CampfireSlotCount)
	cookingTotalTimes := make(nbt.IntArray, CampfireSlotCount)
	for i := 0; i < CampfireSlotCount; i++ {
		cookingTimes[i] = int32(be.CookingProgress[i])
		cookingTotalTimes[i] = int32(be.CookingTime[i])
	}
	c.Set("CookingTimes", cookingTimes)
	c.Set("CookingTotalTimes", cookingTotalTimes)
	return c
}

// DecodeCampfireBlockEntityNBT decodes a vanilla 26.1.2 campfire block entity
// NBT compound (`minecraft:campfire` or `minecraft:soul_campfire`).
func DecodeCampfireBlockEntityNBT(c *nbt.Compound) (CampfireBlockEntity, bool) {
	if c == nil {
		return CampfireBlockEntity{}, false
	}
	idTag, ok := c.Get("id")
	if !ok {
		return CampfireBlockEntity{}, false
	}
	idStr, ok := idTag.(nbt.String)
	if !ok || !IsCampfireBlockID(string(idStr)) {
		return CampfireBlockEntity{}, false
	}
	dim := world.DimensionOverworld
	if dimTag, ok := c.Get("Dimension"); ok {
		if ds, ok := dimTag.(nbt.String); ok && string(ds) != "" {
			dim = normalizeCampfireDim(string(ds))
		}
	}
	be := CampfireBlockEntity{
		ID:        canonicalResourceName(string(idStr)),
		Dimension: dim,
		X:         int(campfireNBTInt(c, "x")),
		Y:         int(campfireNBTInt(c, "y")),
		Z:         int(campfireNBTInt(c, "z")),
	}

	if itemsTag, ok := c.Get("Items"); ok {
		if list, ok := itemsTag.(nbt.List); ok && list.ElemID == nbt.TagCompound {
			for _, elem := range list.Elems {
				ic, ok := elem.(*nbt.Compound)
				if !ok {
					continue
				}
				slot := int(campfireNBTInt(ic, "Slot"))
				if slot < 0 || slot >= CampfireSlotCount {
					continue
				}
				var itemIDStr string
				if sTag, ok := ic.Get("id"); ok {
					if ns, ok := sTag.(nbt.String); ok {
						itemIDStr = string(ns)
					}
				}
				count := campfireNBTInt(ic, "count")
				if count <= 0 {
					count = campfireNBTInt(ic, "Count")
				}
				if count <= 0 || itemIDStr == "" || itemIDStr == "minecraft:air" {
					continue
				}
				be.Items[slot] = world.NewNamedItemStack(itemIDStr, count)
			}
		}
	}

	if ctTag, ok := c.Get("CookingTimes"); ok {
		if arr, ok := ctTag.(nbt.IntArray); ok {
			for i := 0; i < len(arr) && i < CampfireSlotCount; i++ {
				be.CookingProgress[i] = int16(arr[i])
			}
		}
	}
	if cttTag, ok := c.Get("CookingTotalTimes"); ok {
		if arr, ok := cttTag.(nbt.IntArray); ok {
			for i := 0; i < len(arr) && i < CampfireSlotCount; i++ {
				be.CookingTime[i] = int16(arr[i])
			}
		}
	}
	for i := 0; i < CampfireSlotCount; i++ {
		if !be.Items[i].IsEmpty() && be.CookingTime[i] <= 0 {
			be.CookingTime[i] = int16(CampfireDefaultCookTime)
		}
	}
	return be, true
}

func campfireNBTInt(c *nbt.Compound, key string) int32 {
	tag, ok := c.Get(key)
	if !ok {
		return 0
	}
	switch v := tag.(type) {
	case nbt.Int:
		return int32(v)
	case nbt.Short:
		return int32(v)
	case nbt.Byte:
		return int32(v)
	default:
		return 0
	}
}

func (s *Server) campfiresFilePath() string {
	if s == nil || s.cfg.WorldDir == "" {
		return ""
	}
	return filepath.Join(s.cfg.WorldDir, "data", "campfires.dat")
}

// SaveCampfires persists all active campfire block entities to `<worldDir>/data/campfires.dat`.
func (s *Server) SaveCampfires() error {
	path := s.campfiresFilePath()
	if path == "" {
		return nil
	}
	cs := s.campfireState()
	cs.mu.Lock()
	s.ensureCampfiresLoadedLocked(cs)
	all := make([]CampfireBlockEntity, 0, len(cs.campfires))
	for _, be := range cs.campfires {
		if be != nil {
			all = append(all, be.Clone())
		}
	}
	cs.mu.Unlock()

	sort.Slice(all, func(i, j int) bool {
		if all[i].Dimension != all[j].Dimension {
			return all[i].Dimension < all[j].Dimension
		}
		if all[i].Y != all[j].Y {
			return all[i].Y < all[j].Y
		}
		if all[i].Z != all[j].Z {
			return all[i].Z < all[j].Z
		}
		return all[i].X < all[j].X
	})

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	beList := nbt.List{
		ElemID: nbt.TagCompound,
		Elems:  make([]nbt.Tag, 0, len(all)),
	}
	for _, be := range all {
		beList.Elems = append(beList.Elems, EncodeCampfireBlockEntityNBT(be))
	}
	root := nbt.NewCompound().
		Set("block_entities", beList)

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(nbt.MarshalNamed("", root)); err != nil {
		_ = gw.Close()
		return err
	}
	if err := gw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func (s *Server) loadPersistedCampfires() ([]CampfireBlockEntity, error) {
	path := s.campfiresFilePath()
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	gr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer gr.Close()
	uncompressed, err := io.ReadAll(gr)
	if err != nil {
		return nil, err
	}
	_, tag, err := nbt.UnmarshalNamed(uncompressed)
	if err != nil {
		return nil, err
	}
	root, ok := tag.(*nbt.Compound)
	if !ok {
		return nil, nil
	}
	beTag, ok := root.Get("block_entities")
	if !ok {
		return nil, nil
	}
	list, ok := beTag.(nbt.List)
	if !ok || list.ElemID != nbt.TagCompound {
		return nil, nil
	}
	var out []CampfireBlockEntity
	for _, elem := range list.Elems {
		ec, ok := elem.(*nbt.Compound)
		if !ok {
			continue
		}
		if be, ok := DecodeCampfireBlockEntityNBT(ec); ok {
			out = append(out, be)
		}
	}
	return out, nil
}

// LoadCampfires loads all persisted campfire block entities from `<worldDir>/data/campfires.dat`
// and synchronizes their `signal_fire` property above `minecraft:hay_block`.
func (s *Server) LoadCampfires() error {
	if s == nil {
		return nil
	}
	persisted, err := s.loadPersistedCampfires()
	if err != nil {
		return err
	}
	cs := s.campfireState()
	cs.mu.Lock()
	cs.loadedStore = true
	cs.campfires = make(map[campfireKey]*CampfireBlockEntity, len(persisted))
	for _, be := range persisted {
		normDim := normalizeCampfireDim(be.Dimension)
		cp := be.Clone()
		cp.Dimension = normDim
		cp.ID = s.resolveCampfireIDAt(normDim, cp.X, cp.Y, cp.Z, cp.ID)
		cs.campfires[campfireKey{dim: normDim, x: cp.X, y: cp.Y, z: cp.Z}] = &cp
	}
	cs.mu.Unlock()

	for _, be := range persisted {
		s.UpdateCampfireSignalFire(be.Dimension, be.X, be.Y, be.Z)
	}
	return nil
}

// ChunkCampfireBlockEntitiesNBT returns vanilla `block_entities` NBT compounds
// for all campfires and soul campfires in chunk (cx, cz) of dim.
func (s *Server) ChunkCampfireBlockEntitiesNBT(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := normalizeCampfireDim(dim)
	cs := s.campfireState()
	cs.mu.Lock()
	s.ensureCampfiresLoadedLocked(cs)
	var out []*nbt.Compound
	for k, be := range cs.campfires {
		if k.dim != normDim || be == nil {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			out = append(out, EncodeCampfireBlockEntityNBT(*be))
		}
	}
	cs.mu.Unlock()
	return out
}

// UnloadChunkCampfires removes all campfire block entities in chunk (cx, cz)
// of dim from runtime memory and returns their serialized NBT compounds.
func (s *Server) UnloadChunkCampfires(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := normalizeCampfireDim(dim)
	cs := s.campfireState()
	cs.mu.Lock()
	defer cs.mu.Unlock()
	var out []*nbt.Compound
	for k, be := range cs.campfires {
		if k.dim != normDim {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			if be != nil {
				out = append(out, EncodeCampfireBlockEntityNBT(*be))
			}
			delete(cs.campfires, k)
		}
	}
	return out
}

// LoadChunkCampfireBlockEntitiesNBT loads campfire block entities from a chunk's
// `block_entities` NBT list and synchronizes their `signal_fire` block state.
func (s *Server) LoadChunkCampfireBlockEntitiesNBT(dim string, list []*nbt.Compound) int {
	if s == nil || len(list) == 0 {
		return 0
	}
	normDim := normalizeCampfireDim(dim)
	cs := s.campfireState()
	cs.mu.Lock()
	var loaded []CampfireBlockEntity
	for _, c := range list {
		be, ok := DecodeCampfireBlockEntityNBT(c)
		if !ok {
			continue
		}
		if be.Dimension == "" {
			be.Dimension = normDim
		} else {
			be.Dimension = normalizeCampfireDim(be.Dimension)
		}
		be.ID = s.resolveCampfireIDAt(be.Dimension, be.X, be.Y, be.Z, be.ID)
		cp := be.Clone()
		cs.campfires[campfireKey{dim: cp.Dimension, x: cp.X, y: cp.Y, z: cp.Z}] = &cp
		loaded = append(loaded, cp)
	}
	cs.mu.Unlock()

	for _, be := range loaded {
		s.UpdateCampfireSignalFire(be.Dimension, be.X, be.Y, be.Z)
	}
	return len(loaded)
}

// ApplyCampfireContactDamageToEntity checks whether the mob/entity `entityID`
// in `dim` is standing on a lit `minecraft:campfire` (1.0 damage) or
// `minecraft:soul_campfire` (2.0 damage) and applies contact damage.
func (s *Server) ApplyCampfireContactDamageToEntity(dim string, entityID int32) (float32, bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.IsItem() {
		return 0, false
	}
	normDim := normalizeCampfireDim(dim)
	bx := int(math.Floor(ent.X))
	by := int(math.Floor(ent.Y))
	bz := int(math.Floor(ent.Z))
	dmg, ok := s.CampfireContactDamageAt(normDim, bx, by, bz)
	if !ok || dmg <= 0 {
		return 0, false
	}
	killed := s.applyDamageToMob(ent, dmg, nil, 0, false, false)
	if killed {
		s.entities.Remove(entityID)
	}
	return dmg, true
}

// CampfireSmokeSignalInfoAt returns the smoke particle, flame particle, ember
// particle, max smoke height (`10` for normal, `24` for hay-bale signal fire),
// unobstructed reachable height, `signal_fire` flag, and whether the campfire
// at (dim, x, y, z) is actively smoking.
func (s *Server) CampfireSmokeSignalInfoAt(dim string, x, y, z int) (smokeParticle, flameParticle, emberParticle string, maxSmokeHeight, reachableHeight int, signalFire, active bool) {
	if s == nil {
		return "", "", "", 0, 0, false, false
	}
	normDim := normalizeCampfireDim(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return "", "", "", 0, 0, false, false
	}
	st := cache.GetBlock(x, y, z)
	_, _, _, signalFire, _, _ = world.ParseCampfireBlockState(st)
	smokeParticle, flameParticle, emberParticle, maxSmokeHeight, active = world.CampfireSmokeAndParticleInfo(st)
	if !active {
		return "", "", "", 0, 0, signalFire, false
	}
	reachableHeight = cache.CampfireReachableSmokeHeightAt(x, y, z)
	return smokeParticle, flameParticle, emberParticle, maxSmokeHeight, reachableHeight, signalFire, true
}

// HydrateChunkCampfires inspects all `world.StructureBlockEntity` entries on the
// loaded chunk (dim, cx, cz) and registers any `minecraft:campfire` or
// `minecraft:soul_campfire` block entities.
func (s *Server) HydrateChunkCampfires(dim string, cx, cz int32) int {
	if s == nil {
		return 0
	}
	normDim := normalizeCampfireDim(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return 0
	}
	sbes := cache.LoadedChunkStructureBlockEntities(cx, cz)
	if len(sbes) == 0 {
		return 0
	}
	cs := s.campfireState()
	cs.mu.Lock()
	s.ensureCampfiresLoadedLocked(cs)
	var hydrated []CampfireBlockEntity
	for _, sbe := range sbes {
		if !IsCampfireBlockID(sbe.ID) {
			continue
		}
		key := campfireKey{dim: normDim, x: sbe.X, y: sbe.Y, z: sbe.Z}
		if _, exists := cs.campfires[key]; exists {
			continue
		}
		be := CampfireBlockEntity{
			ID:        canonicalResourceName(sbe.ID),
			Dimension: normDim,
			X:         sbe.X,
			Y:         sbe.Y,
			Z:         sbe.Z,
		}
		for _, entry := range sbe.Items {
			if entry.Slot >= 0 && int(entry.Slot) < CampfireSlotCount && entry.ItemID != "" {
				be.Items[entry.Slot] = world.NewNamedItemStack(entry.ItemID, 1)
				be.CookingTime[entry.Slot] = int16(CampfireDefaultCookTime)
			}
		}
		cs.campfires[key] = &be
		hydrated = append(hydrated, be)
	}
	cs.mu.Unlock()

	for _, be := range hydrated {
		s.UpdateCampfireSignalFire(be.Dimension, be.X, be.Y, be.Z)
	}
	return len(hydrated)
}


