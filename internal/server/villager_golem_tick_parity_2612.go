package server

import (
	"encoding/binary"
	"math"
	"math/rand"
	"sort"
	"strings"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Exact Minecraft Java Edition 26.1.2 (`server-26.1.2.jar`) constants for
// `Villager`, `GossipContainer`, `SleepInBed`, `VillagerMakeLove`,
// `HarvestFarmland`, `GolemSensor`, `VillagerHostilesSensor`, `SpawnUtil`,
// and `IronGolem`.
const (
	// VillagerInventorySlots is `Villager.inventory = new SimpleContainer(8)` in 26.1.2.
	VillagerInventorySlots = 8

	// VillagerGossipDiscardThreshold is `GossipContainer.DISCARD_THRESHOLD = 2` in 26.1.2.
	VillagerGossipDiscardThreshold = 2

	// VillagerMaxGossipTopicsPerTransfer is `GossipContainer.MAX_GOSSIP_TOPICS = 10` in 26.1.2.
	VillagerMaxGossipTopicsPerTransfer = 10

	// VillagerGossipTransferCooldownTicks is `Villager.GOSSIP_COOLDOWN = 1200` in 26.1.2.
	VillagerGossipTransferCooldownTicks int64 = 1200

	// VillagerGossipDecayIntervalTicks is `Villager.DECAY_INTERVAL = 24000` in 26.1.2.
	VillagerGossipDecayIntervalTicks int64 = 24000

	// VillagerWakeReSleepCooldownTicks is `SleepInBed.COOLDOWN_AFTER_BEING_WOKEN = 100` in 26.1.2.
	VillagerWakeReSleepCooldownTicks int64 = 100

	// VillagerLevelUpDelayTicks is `Villager.updateMerchantTimer = 40` in 26.1.2.
	VillagerLevelUpDelayTicks = 40

	// VillagerLevelUpRegenTicks is `MobEffectInstance(REGENERATION, 200, 0)` in 26.1.2.
	VillagerLevelUpRegenTicks = 200

	// VillagerMeetingScanRadius / Vertical / MaxTickets match `PoiTypes.MEETING` (`minecraft:bell`, 32 tickets, range 48).
	VillagerMeetingScanRadius     = 48
	VillagerMeetingScanVertical   = 6
	MaxVillagerMeetingPointShares = 32

	// VillagerMakeLoveBedScanRadius / Vertical match `AcquirePoi` scan range (48) for `VillagerMakeLove`.
	VillagerMakeLoveBedScanRadius   = 48
	VillagerMakeLoveBedScanVertical = 8

	// VillagerGolemSleepMaxAgeTicks (`24000`), `GolemSensor` memory (`599` ticks), and `10.0`/`16.0` boxes in 26.1.2.
	VillagerGolemSleepMaxAgeTicks  int64 = 24000
	VillagerGolemDetectedTTLTicks  int64 = 599
	VillagerGolemSummonBoxHalfSize       = 10.0
	VillagerGolemSensorBoxHalfSize       = 16.0

	// IronGolemAttackAnimationDurationTicks (`10`), `doHurtTarget` base damage (`15.0f`),
	// vertical fling (`0.4f`), `OfferFlowerGoal` chance (`1/8000`), and `PERSISTENT_ANGER_TIME` (`400..780` ticks = 20..39s).
	IronGolemAttackAnimationDurationTicks = 10
	IronGolemBaseAttackDamage2612         = float32(15.0)
	IronGolemBaseVerticalFling2612        = float64(float32(0.4))
	IronGolemOfferFlowerChanceDenom       = 8000
	IronGolemMinPersistentAngerTicks      = 400
	IronGolemMaxPersistentAngerTicks      = 780

	//VillagerPlayActivity matches `minecraft:play` in `data/minecraft/timeline/villager_schedule.json`.
	VillagerActivityPlay VillagerActivity = "play"
)

// --- 1. Villager `MEETING_POINT` (`minecraft:bell`) POI & `releaseAllPois` ---

// IsVillageMeetingBellState reports whether state is `minecraft:bell` (`PoiTypes.MEETING`).
func IsVillageMeetingBellState(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	return strings.ToLower(world.BlockName(state)) == "minecraft:bell"
}

// VillagerMeetingPoint returns the `MEETING_POINT` (`minecraft:bell`) POI claimed by entityID, if any.
func (s *Server) VillagerMeetingPoint(entityID int32) (VillagerPOISite, bool) {
	if s == nil || entityID <= 0 {
		return VillagerPOISite{}, false
	}
	sm := s.villagerSocial()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	site, ok := sm.meetingByEntity[entityID]
	return site, ok
}

// ClaimVillagerMeetingPoint binds adult villager entityID to a `minecraft:bell`
// at (dim, x, y, z), enforcing `PoiTypes.MEETING` `maxTickets = 32`.
func (s *Server) ClaimVillagerMeetingPoint(entityID int32, dim string, x, y, z int) bool {
	if s == nil || s.entities == nil || entityID <= 0 || s.IsMobBaby(entityID) {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != MobTypeVillager {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	if normDim == "" {
		normDim = ent.EffectiveDimension()
	}
	cache := s.DimensionChunks(normDim)
	if cache == nil || !IsVillageMeetingBellState(cache.GetBlock(x, y, z)) {
		return false
	}

	key := dimBlockKey{dim: normDim, pos: world.BlockPos{X: x, Y: y, Z: z}}
	sm := s.villagerSocial()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.meetingByEntity == nil {
		sm.meetingByEntity = make(map[int32]VillagerPOISite)
	}
	if sm.entitiesByMeeting == nil {
		sm.entitiesByMeeting = make(map[dimBlockKey]map[int32]struct{})
	}
	holders := sm.entitiesByMeeting[key]
	if holders != nil {
		for hid := range holders {
			if hid != entityID {
				if hent := s.entities.Get(hid); hent == nil || hent.TypeName != MobTypeVillager {
					delete(holders, hid)
					delete(sm.meetingByEntity, hid)
				}
			}
		}
		if _, already := holders[entityID]; !already && len(holders) >= MaxVillagerMeetingPointShares {
			return false
		}
	}
	if prev, had := sm.meetingByEntity[entityID]; had {
		prevKey := dimBlockKey{dim: prev.Dimension, pos: prev.Pos}
		if prevSet := sm.entitiesByMeeting[prevKey]; prevSet != nil {
			delete(prevSet, entityID)
			if len(prevSet) == 0 {
				delete(sm.entitiesByMeeting, prevKey)
			}
		}
	}
	if holders == nil {
		holders = make(map[int32]struct{})
		sm.entitiesByMeeting[key] = holders
	}
	holders[entityID] = struct{}{}
	sm.meetingByEntity[entityID] = VillagerPOISite{
		Dimension: normDim,
		Pos:       world.BlockPos{X: x, Y: y, Z: z},
	}
	return true
}

// ReleaseVillagerMeetingPoint releases any `MEETING_POINT` (`minecraft:bell`) POI held by entityID.
func (s *Server) ReleaseVillagerMeetingPoint(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	sm := s.villagerSocial()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	site, ok := sm.meetingByEntity[entityID]
	if !ok {
		return false
	}
	delete(sm.meetingByEntity, entityID)
	key := dimBlockKey{dim: site.Dimension, pos: site.Pos}
	if holders := sm.entitiesByMeeting[key]; holders != nil {
		delete(holders, entityID)
		if len(holders) == 0 {
			delete(sm.entitiesByMeeting, key)
		}
	}
	return true
}

// ValidateVillagerMeetingPoint verifies that the villager's claimed `minecraft:bell`
// block still exists; if broken, releases `MEETING_POINT`.
func (s *Server) ValidateVillagerMeetingPoint(entityID int32) bool {
	site, ok := s.VillagerMeetingPoint(entityID)
	if !ok {
		return false
	}
	cache := s.DimensionChunks(site.Dimension)
	if cache == nil {
		return true
	}
	if IsVillageMeetingBellState(cache.GetBlock(site.Pos.X, site.Pos.Y, site.Pos.Z)) {
		return true
	}
	s.ReleaseVillagerMeetingPoint(entityID)
	return false
}

// TryAcquireVillagerMeetingPoint searches within `VillagerMeetingScanRadius`
// around adult villager entityID for a `minecraft:bell` with < 32 tickets and claims it.
func (s *Server) TryAcquireVillagerMeetingPoint(entityID int32) (VillagerPOISite, bool) {
	if s == nil || s.entities == nil || entityID <= 0 || s.IsMobBaby(entityID) {
		return VillagerPOISite{}, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != MobTypeVillager {
		return VillagerPOISite{}, false
	}
	if site, ok := s.VillagerMeetingPoint(entityID); ok {
		if s.ValidateVillagerMeetingPoint(entityID) {
			return site, true
		}
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return VillagerPOISite{}, false
	}
	bx0 := int(math.Floor(ent.X))
	by0 := int(math.Floor(ent.Y))
	bz0 := int(math.Floor(ent.Z))

	bestDistSq := math.MaxFloat64
	var bestPos world.BlockPos
	found := false

	for dy := -VillagerWorkstationScanVertical; dy <= VillagerWorkstationScanVertical; dy++ {
		by := by0 + dy
		if by < world.MinY || by >= world.MinY+world.WorldHeight {
			continue
		}
		for dx := -VillagerWorkstationScanRadius; dx <= VillagerWorkstationScanRadius; dx++ {
			for dz := -VillagerWorkstationScanRadius; dz <= VillagerWorkstationScanRadius; dz++ {
				distSq := float64(dx*dx + dy*dy + dz*dz)
				if distSq >= bestDistSq {
					continue
				}
				bx := bx0 + dx
				bz := bz0 + dz
				if !IsVillageMeetingBellState(cache.GetBlock(bx, by, bz)) {
					continue
				}
				bestDistSq = distSq
				bestPos = world.BlockPos{X: bx, Y: by, Z: bz}
				found = true
			}
		}
	}
	if !found {
		return VillagerPOISite{}, false
	}
	if !s.ClaimVillagerMeetingPoint(entityID, dim, bestPos.X, bestPos.Y, bestPos.Z) {
		return VillagerPOISite{}, false
	}
	return s.VillagerMeetingPoint(entityID)
}

// ReleaseAllVillagerPOIs mirrors `Villager.releaseAllPois()` (`HOME`, `JOB_SITE`, `MEETING_POINT`)
// and clears bed `occupied` state and open merchant screens when a villager dies or converts.
func (s *Server) ReleaseAllVillagerPOIs(entityID int32) {
	if s == nil || entityID <= 0 {
		return
	}
	if bedSite, hasBed := s.VillagerBedSite(entityID); hasBed && s.IsVillagerSleeping(entityID) {
		s.setVillagerBedOccupied(bedSite.Dimension, bedSite.Pos.X, bedSite.Pos.Y, bedSite.Pos.Z, false)
	}
	// Directly delete job_site ticket from villagerSocial even if entity is already removed.
	sm := s.villagerSocial()
	sm.mu.Lock()
	if js, ok := sm.jobSiteByEntity[entityID]; ok {
		delete(sm.jobSiteByEntity, entityID)
		delete(sm.entityByJobSite, dimBlockKey{dim: js.Dimension, pos: js.Pos})
	}
	delete(sm.jobSiteLost, entityID)
	if bs, ok := sm.bedByEntity[entityID]; ok {
		delete(sm.bedByEntity, entityID)
		delete(sm.entityByBed, dimBlockKey{dim: bs.Dimension, pos: bs.Pos})
	}
	if ms, ok := sm.meetingByEntity[entityID]; ok {
		delete(sm.meetingByEntity, entityID)
		mKey := dimBlockKey{dim: ms.Dimension, pos: ms.Pos}
		if holders := sm.entitiesByMeeting[mKey]; holders != nil {
			delete(holders, entityID)
			if len(holders) == 0 {
				delete(sm.entitiesByMeeting, mKey)
			}
		}
	}
	sm.mu.Unlock()

	vp := s.villagerPolish()
	vp.mu.Lock()
	delete(vp.villagers, entityID)
	vp.mu.Unlock()

	vtr := s.villagerTracker()
	vtr.mu.Lock()
	delete(vtr.villagers, entityID)
	vtr.mu.Unlock()

	s.closeMerchantScreensForVillager(entityID)
}

func (s *Server) setVillagerBedOccupied(dim string, x, y, z int, occupied bool) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return
	}
	st := cache.GetBlock(x, y, z)
	if !IsBedBlock(st) {
		return
	}
	occVal := "false"
	if occupied {
		occVal = "true"
	}
	name, props, ok := world.BlockStateProperties(st)
	if !ok || props == nil {
		return
	}
	if props["occupied"] != occVal {
		cp := make(map[string]string, len(props))
		for k, v := range props {
			cp[k] = v
		}
		cp["occupied"] = occVal
		if nextSt, ok := world.BlockStateByName(name, cp); ok && nextSt != st {
			cache.SetBlock(x, y, z, nextSt)
			w := protocol.NewWriter(12)
			w.Position(x, y, z)
			w.VarInt(int32(nextSt))
			s.BroadcastDimensionChunk(normDim, int32(x>>4), int32(z>>4), protocol.PlayBlockUpdate, w.Bytes())
		}
	}
	// Also update the paired half (`foot` <-> `head`) if present.
	part := props["part"]
	facing := props["facing"]
	ox, oz := x, z
	step := 1
	if part == "head" {
		step = -1
	}
	switch facing {
	case "north":
		oz -= step
	case "south":
		oz += step
	case "west":
		ox -= step
	case "east":
		ox += step
	default:
		return
	}
	otherSt := cache.GetBlock(ox, y, oz)
	if IsBedBlock(otherSt) {
		oName, oProps, oOK := world.BlockStateProperties(otherSt)
		if oOK && oName == name && oProps != nil && oProps["occupied"] != occVal {
			cp2 := make(map[string]string, len(oProps))
			for k, v := range oProps {
				cp2[k] = v
			}
			cp2["occupied"] = occVal
			if nextOther, ok := world.BlockStateByName(oName, cp2); ok && nextOther != otherSt {
				cache.SetBlock(ox, y, oz, nextOther)
				w := protocol.NewWriter(12)
				w.Position(ox, y, oz)
				w.VarInt(int32(nextOther))
				s.BroadcastDimensionChunk(normDim, int32(ox>>4), int32(oz>>4), protocol.PlayBlockUpdate, w.Bytes())
			}
		}
	}
}

// --- 2. Villager 8-Slot Inventory, Item Pickup, Farmer Crops/Composter & Food Sharing ---

// WantsVillagerToPickUp2612 implements `Villager.wantsToPickUp` in `26.1.2.jar`:
//   - Any villager picks up items in `#minecraft:villager_picks_up` (`bread`, `wheat`,
//     `beetroot`, `wheat_seeds`, `potato`, `carrot`, `beetroot_seeds`,
//     `torchflower_seeds`, `pitcher_pod`).
//   - Only `minecraft:farmer` additionally picks up `minecraft:bone_meal`
//     (`VillagerProfession.FARMER.secondaryPoi()` / requested items).
func WantsVillagerToPickUp2612(profession, itemName string) bool {
	switch canonicalResourceName(itemName) {
	case "minecraft:bread",
		"minecraft:wheat",
		"minecraft:beetroot",
		"minecraft:wheat_seeds",
		"minecraft:potato",
		"minecraft:carrot",
		"minecraft:beetroot_seeds",
		"minecraft:torchflower_seeds",
		"minecraft:pitcher_pod":
		return true
	case "minecraft:bone_meal":
		return canonicalResourceName(profession) == VillagerProfessionFarmer
	default:
		return false
	}
}

// IsVillagerPlantableSeed2612 reports whether itemName is in
// `#minecraft:villager_plantable_seeds` in `26.1.2.jar`.
func IsVillagerPlantableSeed2612(itemName string) bool {
	switch canonicalResourceName(itemName) {
	case "minecraft:wheat_seeds",
		"minecraft:potato",
		"minecraft:carrot",
		"minecraft:beetroot_seeds",
		"minecraft:torchflower_seeds",
		"minecraft:pitcher_pod":
		return true
	default:
		return false
	}
}

// VillagerInventory returns a copy of the 8-slot `SimpleContainer` inventory of villager entityID.
func (s *Server) VillagerInventory(entityID int32) [VillagerInventorySlots]world.ItemStack {
	var out [VillagerInventorySlots]world.ItemStack
	if s == nil || entityID <= 0 {
		return out
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	defer vp.mu.Unlock()
	if e := vp.villagers[entityID]; e != nil {
		out = e.Inventory
	}
	return out
}

// SetVillagerInventorySlot sets slot `0..7` in villager entityID's 8-slot inventory.
func (s *Server) SetVillagerInventorySlot(entityID int32, slot int, stack world.ItemStack) bool {
	if s == nil || entityID <= 0 || slot < 0 || slot >= VillagerInventorySlots {
		return false
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	vp.ensureVillagerLocked(entityID).Inventory[slot] = stack
	vp.mu.Unlock()
	return true
}

// AddItemToVillagerInventory inserts stack into villager entityID's 8-slot
// `SimpleContainer` (merging with matching stacks up to `MaxItemStackSize = 64` first,
// then filling empty slots), returning the count added and any leftover stack.
func (s *Server) AddItemToVillagerInventory(entityID int32, stack world.ItemStack) (added int32, remainder world.ItemStack) {
	if s == nil || entityID <= 0 || stack.IsEmpty() || stack.Count <= 0 {
		return 0, stack
	}
	itemID := stack.ResolvedItemID()
	itemName := stack.ResolvedID()
	rem := stack.Count
	const maxStack int32 = 64

	vp := s.villagerPolish()
	vp.mu.Lock()
	e := vp.ensureVillagerLocked(entityID)
	for i := 0; i < VillagerInventorySlots && rem > 0; i++ {
		cur := e.Inventory[i]
		if cur.IsEmpty() || cur.ResolvedID() != itemName || cur.Count >= maxStack {
			continue
		}
		space := maxStack - cur.Count
		take := rem
		if take > space {
			take = space
		}
		cur.Count += take
		e.Inventory[i] = cur
		added += take
		rem -= take
	}
	for i := 0; i < VillagerInventorySlots && rem > 0; i++ {
		if !e.Inventory[i].IsEmpty() {
			continue
		}
		take := rem
		if take > maxStack {
			take = maxStack
		}
		if itemID > 0 {
			e.Inventory[i] = world.NewItemStack(itemID, take)
		} else {
			e.Inventory[i] = world.NewNamedItemStack(itemName, take)
		}
		added += take
		rem -= take
	}
	vp.mu.Unlock()

	if rem <= 0 {
		return added, world.ItemStack{}
	}
	if itemID > 0 {
		return added, world.NewItemStack(itemID, rem)
	}
	return added, world.NewNamedItemStack(itemName, rem)
}

func countInventoryFoodLocked(e *villagerPolishEntry) int {
	if e == nil {
		return 0
	}
	pts := 0
	for i := 0; i < VillagerInventorySlots; i++ {
		st := e.Inventory[i]
		if st.IsEmpty() || st.Count <= 0 {
			continue
		}
		if fv := VillagerFoodValue(st.ResolvedID()); fv > 0 {
			pts += fv * int(st.Count)
		}
	}
	return pts
}

func digestVillagerFoodLocked(e *villagerPolishEntry, needed int) bool {
	if e == nil {
		return false
	}
	if e.FoodPoints+countInventoryFoodLocked(e) < needed {
		return false
	}
	for e.FoodPoints < needed {
		consumedOne := false
		for i := 0; i < VillagerInventorySlots && e.FoodPoints < needed; i++ {
			st := e.Inventory[i]
			if st.IsEmpty() || st.Count <= 0 {
				continue
			}
			fv := VillagerFoodValue(st.ResolvedID())
			if fv <= 0 {
				continue
			}
			e.FoodPoints += fv
			if st.Count > 1 {
				st.Count--
				e.Inventory[i] = st
			} else {
				e.Inventory[i] = world.ItemStack{}
			}
			consumedOne = true
		}
		if !consumedOne {
			break
		}
	}
	if e.FoodPoints < needed {
		return false
	}
	e.FoodPoints -= needed
	return true
}

func (s *Server) stepVillagerItemPickupAndFarming(entityID int32, tod int64) {
	if s == nil || s.entities == nil || entityID <= 0 || !s.MobGriefing() {
		return
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != MobTypeVillager || s.IsVillagerSleeping(entityID) {
		return
	}
	vs, ok := s.VillagerState(entityID)
	if !ok {
		return
	}
	prof := canonicalResourceName(vs.Profession)
	dim := ent.EffectiveDimension()

	// 1. `Mob.aiStep` item pickup (`getPickUpBoundingBox` = `inflate(1.0, 0.0, 1.0)`).
	for _, itemEnt := range s.entities.AppendAll(nil) {
		if !itemEnt.IsItem() || itemEnt.Item.IsEmpty() || itemEnt.EffectiveDimension() != dim {
			continue
		}
		if math.Abs(itemEnt.X-ent.X) > 1.35 || math.Abs(itemEnt.Z-ent.Z) > 1.35 || itemEnt.Y < ent.Y-0.5 || itemEnt.Y > ent.Y+2.2 {
			continue
		}
		if !WantsVillagerToPickUp2612(prof, itemEnt.Item.ResolvedID()) {
			continue
		}
		added, rem := s.AddItemToVillagerInventory(entityID, itemEnt.Item)
		if added <= 0 {
			continue
		}
		cp := itemEnt.ChunkPos()
		if rem.IsEmpty() {
			s.entities.Remove(itemEnt.ID)
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
				encodeRemoveEntitiesPacket(nil, []int32{itemEnt.ID}))
		} else {
			s.entities.Update(itemEnt.ID, func(e *world.Entity) {
				e.Item = rem
			})
		}
	}

	// 2. Farmer autonomous `HarvestFarmland`, `UseBonemeal`, and `WorkAtComposter` during WORK (`2000..8999`).
	if s.IsMobBaby(entityID) || prof != VillagerProfessionFarmer || tod < VillagerWorkStartTick || tod >= VillagerWorkEndTick {
		return
	}
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return
	}
	vtr := s.villagerTracker()
	vtr.mu.Lock()
	rng := vtr.rng
	vtr.mu.Unlock()

	bx0 := int(math.Floor(ent.X))
	by0 := int(math.Floor(ent.Y))
	bz0 := int(math.Floor(ent.Z))

	// Find first plantable seed slot and first bone_meal slot in the 8-slot inventory.
	inv := s.VillagerInventory(entityID)
	seedSlot := -1
	boneMealSlot := -1
	for i := 0; i < VillagerInventorySlots; i++ {
		if inv[i].IsEmpty() {
			continue
		}
		id := inv[i].ResolvedID()
		if seedSlot < 0 && IsVillagerPlantableSeed2612(id) {
			seedSlot = i
		}
		if boneMealSlot < 0 && id == "minecraft:bone_meal" {
			boneMealSlot = i
		}
	}

	actedOnCrop := false
	for dy := -1; dy <= 1 && !actedOnCrop; dy++ {
		cy := by0 + dy
		if cy <= world.MinY+1 || cy >= world.MinY+world.WorldHeight {
			continue
		}
		for dx := -2; dx <= 2 && !actedOnCrop; dx++ {
			for dz := -2; dz <= 2 && !actedOnCrop; dz++ {
				cx := bx0 + dx
				cz := bz0 + dz
				if strings.ToLower(world.BlockName(cache.GetBlock(cx, cy-1, cz))) != "minecraft:farmland" {
					continue
				}
				curSt := cache.GetBlock(cx, cy, cz)
				if world.IsMatureFarmerCropState(curSt) || (curSt == world.StateAir && seedSlot >= 0) {
					var seedStack world.ItemStack
					if seedSlot >= 0 {
						seedStack = inv[seedSlot]
					}
					drops, nextSeed, changed, _ := s.FarmerVillagerHarvestOrPlantAt(dim, cx, cy, cz, seedStack, rng)
					if changed {
						if seedSlot >= 0 {
							s.SetVillagerInventorySlot(entityID, seedSlot, nextSeed)
						}
						for _, dr := range drops {
							if dr.IsEmpty() {
								continue
							}
							_, leftover := s.AddItemToVillagerInventory(entityID, dr)
							if !leftover.IsEmpty() && s.DoTileDrops() {
								s.SpawnItemEntityInDimension(dim, float64(cx)+0.5, float64(cy)+0.25, float64(cz)+0.5, leftover)
							}
						}
						actedOnCrop = true
					}
				} else if boneMealSlot >= 0 && curSt != world.StateAir && !world.IsMatureFarmerCropState(curSt) {
					if consumed, _ := s.FarmerVillagerApplyBoneMealAt(dim, cx, cy, cz, rng); consumed {
						bm := inv[boneMealSlot]
						if bm.Count > 1 {
							bm.Count--
							s.SetVillagerInventorySlot(entityID, boneMealSlot, bm)
						} else {
							s.SetVillagerInventorySlot(entityID, boneMealSlot, world.ItemStack{})
						}
						actedOnCrop = true
					}
				}
			}
		}
	}

	// `WorkAtComposter.makeBread`: when standing near claimed composter, craft `3x wheat -> 1x bread` (up to 36 bread).
	if jobSite, hasJob := s.VillagerJobSite(entityID); hasJob && jobSite.Dimension == dim {
		jdx := float64(jobSite.Pos.X) + 0.5 - ent.X
		jdy := float64(jobSite.Pos.Y) + 0.5 - ent.Y
		jdz := float64(jobSite.Pos.Z) + 0.5 - ent.Z
		if jdx*jdx+jdy*jdy+jdz*jdz <= 6.25 {
			s.farmerCraftBreadAtComposter(entityID)
		}
	}
}

func (s *Server) farmerCraftBreadAtComposter(entityID int32) {
	inv := s.VillagerInventory(entityID)
	var wheatCount, breadCount int32
	for i := 0; i < VillagerInventorySlots; i++ {
		if inv[i].IsEmpty() {
			continue
		}
		switch inv[i].ResolvedID() {
		case "minecraft:wheat":
			wheatCount += inv[i].Count
		case "minecraft:bread":
			breadCount += inv[i].Count
		}
	}
	if breadCount > 36 || wheatCount < 3 {
		return
	}
	loaves := (36 - breadCount)
	if maxFromWheat := wheatCount / 3; loaves > maxFromWheat {
		loaves = maxFromWheat
	}
	if loaves <= 0 {
		return
	}
	needWheat := loaves * 3
	for i := 0; i < VillagerInventorySlots && needWheat > 0; i++ {
		if inv[i].IsEmpty() || inv[i].ResolvedID() != "minecraft:wheat" {
			continue
		}
		take := inv[i].Count
		if take > needWheat {
			take = needWheat
		}
		if inv[i].Count > take {
			inv[i].Count -= take
			s.SetVillagerInventorySlot(entityID, i, inv[i])
		} else {
			s.SetVillagerInventorySlot(entityID, i, world.ItemStack{})
		}
		needWheat -= take
	}
	_, rem := s.AddItemToVillagerInventory(entityID, world.NewNamedItemStack("minecraft:bread", loaves))
	if !rem.IsEmpty() && s.entities != nil {
		if ent := s.entities.Get(entityID); ent != nil {
			s.SpawnItemEntityInDimension(ent.EffectiveDimension(), ent.X, ent.Y+0.5, ent.Z, rem)
		}
	}
}

// stepVillagerTradeExcessFood implements `TradeWithVillager.throwHalfStack` in `26.1.2.jar`:
// when `giverID` has `>= 24` food points (`countFoodPointsInInventory + FoodPoints`) and
// `receiverID` has `< 12` food points, transfers half of `giverID`'s food to `receiverID`.
func (s *Server) stepVillagerTradeExcessFood(giverID, receiverID int32) bool {
	if s == nil || giverID <= 0 || receiverID <= 0 || giverID == receiverID {
		return false
	}
	if s.ShareVillagerFood(giverID, receiverID) {
		return true
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	g := vp.ensureVillagerLocked(giverID)
	r := vp.ensureVillagerLocked(receiverID)
	gInvPts := countInventoryFoodLocked(g)
	rInvPts := countInventoryFoodLocked(r)
	if g.FoodPoints+gInvPts < VillagerBreedingFoodThreshold*2 || r.FoodPoints+rInvPts >= VillagerBreedingFoodThreshold {
		vp.mu.Unlock()
		return false
	}
	var thrownStacks []world.ItemStack
	for i := 0; i < VillagerInventorySlots; i++ {
		st := g.Inventory[i]
		if st.IsEmpty() || st.Count <= 1 || VillagerFoodValue(st.ResolvedID()) <= 0 {
			continue
		}
		half := st.Count / 2
		if half <= 0 {
			continue
		}
		st.Count -= half
		g.Inventory[i] = st
		thrownStacks = append(thrownStacks, world.NewNamedItemStack(st.ResolvedID(), half))
		break
	}
	vp.mu.Unlock()
	if len(thrownStacks) == 0 {
		return false
	}
	for _, ts := range thrownStacks {
		_, rem := s.AddItemToVillagerInventory(receiverID, ts)
		if !rem.IsEmpty() && s.entities != nil {
			if rEnt := s.entities.Get(receiverID); rEnt != nil {
				s.SpawnItemEntityInDimension(rEnt.EffectiveDimension(), rEnt.X, rEnt.Y+0.3, rEnt.Z, rem)
			}
		}
	}
	return true
}

// --- 3. `VillagerMakeLove` Reachable Unclaimed Bed Check ---

func isBedReachableForVillager(cache *world.Cache, bx, by, bz int) bool {
	if cache == nil {
		return false
	}
	// A bed is reachable if it is not buried/sealed: either the 2 blocks above the bed
	// are passable, or at least one orthogonally adjacent column `(nx, by, nz)` has 2-high passable clearance.
	if isPassableBlock(cache.GetBlock(bx, by+1, bz)) && isPassableBlock(cache.GetBlock(bx, by+2, bz)) {
		// Also require at least one horizontal neighbor not to be a solid wall at head height,
		// so a bed completely sealed inside a 1x1x1 solid box is rejected.
		dirs := [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
		for _, d := range dirs {
			nx, nz := bx+d[0], bz+d[1]
			st0 := cache.GetBlock(nx, by, nz)
			st1 := cache.GetBlock(nx, by+1, nz)
			if (isPassableBlock(st0) || IsBedBlock(st0)) && isPassableBlock(st1) {
				return true
			}
		}
	}
	dirs := [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
	for _, d := range dirs {
		nx, nz := bx+d[0], bz+d[1]
		if isPassableBlock(cache.GetBlock(nx, by, nz)) && isPassableBlock(cache.GetBlock(nx, by+1, nz)) {
			return true
		}
	}
	return false
}

func (s *Server) findVacantReachableBedForVillagerChild(dim string, cx, cy, cz float64) (world.BlockPos, bool) {
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return world.BlockPos{}, false
	}
	bx0 := int(math.Floor(cx))
	by0 := int(math.Floor(cy))
	bz0 := int(math.Floor(cz))

	bestDistSq := math.MaxFloat64
	var bestPos world.BlockPos
	found := false

	for _, rad := range [2]int{16, VillagerMakeLoveBedScanRadius} {
		for dy := -VillagerMakeLoveBedScanVertical; dy <= VillagerMakeLoveBedScanVertical; dy++ {
			by := by0 + dy
			if by < world.MinY || by >= world.MinY+world.WorldHeight-2 {
				continue
			}
			for dx := -rad; dx <= rad; dx++ {
				for dz := -rad; dz <= rad; dz++ {
					if rad > 16 && dx >= -16 && dx <= 16 && dz >= -16 && dz <= 16 {
						continue
					}
					distSq := float64(dx*dx + dy*dy + dz*dz)
					if distSq >= bestDistSq {
						continue
					}
					bx := bx0 + dx
					bz := bz0 + dz
					st := cache.GetBlock(bx, by, bz)
					if !IsBedBlock(st) {
						continue
					}
					_, props, propOK := world.BlockStateProperties(st)
					if propOK && props != nil {
						if props["occupied"] == "true" {
							continue
						}
						// If both foot and head are placed, `PoiTypes.HOME` registers on `head` in vanilla,
						// or whichever half is unclaimed; ensure the paired half is not claimed either!
						if pairedX, pairedZ, hasPair := pairedBedHalfCoords(cache, bx, by, bz, st, props); hasPair {
							if _, pairClaimed := s.BedOwnerAt(normDim, pairedX, by, pairedZ); pairClaimed {
								continue
							}
						}
					}
					if _, claimed := s.BedOwnerAt(normDim, bx, by, bz); claimed {
						continue
					}
					if !isBedReachableForVillager(cache, bx, by, bz) {
						continue
					}
					bestDistSq = distSq
					bestPos = world.BlockPos{X: bx, Y: by, Z: bz}
					found = true
				}
			}
		}
		if found {
			break
		}
	}
	return bestPos, found
}

func pairedBedHalfCoords(cache *world.Cache, x, y, z int, st uint16, props map[string]string) (int, int, bool) {
	if cache == nil || props == nil {
		return 0, 0, false
	}
	step := 1
	if props["part"] == "head" {
		step = -1
	}
	ox, oz := x, z
	switch props["facing"] {
	case "north":
		oz -= step
	case "south":
		oz += step
	case "west":
		ox -= step
	case "east":
		ox += step
	default:
		return 0, 0, false
	}
	if IsBedBlock(cache.GetBlock(ox, y, oz)) {
		return ox, oz, true
	}
	return 0, 0, false
}

// --- 4. `VillagerHostilesSensor`, `GolemSensor`, `wantsToSpawnGolem` & `LEGACY_IRON_GOLEM` ---

// VillagerHostileAcceptableDistance2612 returns the exact acceptable distance
// from `VillagerHostilesSensor.ACCEPTABLE_DISTANCE_FROM_HOSTILES` in `26.1.2.jar`.
func VillagerHostileAcceptableDistance2612(mobType string) (float64, bool) {
	switch canonicalResourceName(mobType) {
	case MobTypeDrowned, MobTypeHusk, MobTypeVex, MobTypeZombie, MobTypeZombieVillager:
		return 8.0, true
	case MobTypeVindicator, MobTypeZoglin:
		return 10.0, true
	case MobTypeEvoker, MobTypeIllusioner, MobTypeRavager:
		return 12.0, true
	case MobTypePillager:
		return 15.0, true
	default:
		return 0, false
	}
}

// HasVillagerSleptWithin24000Ticks reports whether villager entityID has a
// recorded `LAST_SLEPT` memory with `gameTime - lastSlept < 24000` (`Villager.wantsToSpawnGolem`).
func (s *Server) HasVillagerSleptWithin24000Ticks(entityID int32, gameTime int64) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	defer vp.mu.Unlock()
	e := vp.villagers[entityID]
	if e == nil || !e.HasLastSlept {
		return false
	}
	diff := gameTime - e.LastSleptTick
	return diff >= 0 && diff < VillagerGolemSleepMaxAgeTicks
}

// RecordVillagerDetectedGolem sets `MemoryModuleType.GOLEM_DETECTED_RECENTLY`
// TTL (`599` ticks in `GolemSensor.golemDetected` in `26.1.2.jar`) on villager entityID.
func (s *Server) RecordVillagerDetectedGolem(entityID int32, gameTime int64) {
	if s == nil || entityID <= 0 {
		return
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	vp.ensureVillagerLocked(entityID).GolemDetectedUntil = gameTime + VillagerGolemDetectedTTLTicks
	vp.mu.Unlock()
}

// WantsVillagerToSpawnGolem2612 implements `Villager.wantsToSpawnGolem(long gameTime)` in `26.1.2.jar`:
//   - Adult villager (`!isBaby()`)
//   - `LAST_SLEPT` present and `gameTime - lastSlept < 24000`
//   - `GOLEM_DETECTED_RECENTLY` (`599`-tick TTL from `GolemSensor`) NOT active (`gameTime >= GolemDetectedUntil`).
func (s *Server) WantsVillagerToSpawnGolem2612(entityID int32, gameTime int64) bool {
	if s == nil || entityID <= 0 || s.IsMobBaby(entityID) {
		return false
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	defer vp.mu.Unlock()
	e := vp.villagers[entityID]
	if e == nil || !e.HasLastSlept {
		return false
	}
	diff := gameTime - e.LastSleptTick
	if diff < 0 || diff >= VillagerGolemSleepMaxAgeTicks {
		return false
	}
	if e.HasGolemDetected && gameTime < e.GolemDetectedUntil {
		return false
	}
	return true
}

func (s *Server) markVillagerDetectedGolemLocked(vp *villagerPolishTracker, entityID int32, gameTime int64) {
	e := vp.ensureVillagerLocked(entityID)
	e.HasGolemDetected = true
	e.GolemDetectedUntil = gameTime + VillagerGolemDetectedTTLTicks
}

// IsLegacyIronGolemSpawnSpot2612 validates `(x, y, z)` (where `(x, y-1, z)` is the
// supporting floor block and `(x, y, z)` is the golem's feet block) against
// `SpawnUtil.trySpawnMob(..., LEGACY_IRON_GOLEM, /*checkCollision=*/false)` in `26.1.2.jar`:
//   - Rejects floor blocks: `cobweb`, `cactus`, `glass`, `tinted_glass`, `*_stained_glass`,
//     `glass_pane`, `*_stained_glass_pane`, `*_leaves`, `conduit`, `ice`, `frosted_ice`,
//     `tnt`, `glowstone`, `beacon`, `sea_lantern`, air, or fluids, and requires solid ground.
//   - Requires `(x, y, z)` to be air/liquid/passable non-solid, and `(x, y+1, z)` / `(x, y+2, z)`
//     to be passable and free of fluids (`water`/`lava`).
func IsLegacyIronGolemSpawnSpot2612(cache *world.Cache, x, y, z int) bool {
	if cache == nil || y <= world.MinY+1 || y >= world.MinY+world.WorldHeight-3 {
		return false
	}
	floor := cache.GetBlock(x, y-1, z)
	if !isSolidGroundBlock(floor) || isRejectedLegacyIronGolemFloorName(world.BlockName(floor)) {
		return false
	}
	feet := cache.GetBlock(x, y, z)
	if !isPassableBlock(feet) && feet != world.StateWater {
		return false
	}
	for dy := 1; dy <= 2; dy++ {
		st := cache.GetBlock(x, y+dy, z)
		if !isPassableBlock(st) {
			return false
		}
		name := strings.ToLower(world.BlockName(st))
		if name == "minecraft:water" || name == "minecraft:lava" {
			return false
		}
	}
	return true
}

func isRejectedLegacyIronGolemFloorName(rawName string) bool {
	name := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(rawName)), "minecraft:")
	switch name {
	case "", "air", "cave_air", "void_air", "water", "lava",
		"cobweb", "cactus", "glass", "tinted_glass", "glass_pane",
		"conduit", "ice", "frosted_ice", "tnt", "glowstone", "beacon", "sea_lantern":
		return true
	}
	if strings.HasSuffix(name, "_stained_glass") ||
		strings.HasSuffix(name, "_stained_glass_pane") ||
		strings.HasSuffix(name, "_leaves") ||
		name == "azalea_leaves" || name == "flowering_azalea_leaves" {
		return true
	}
	return false
}

// --- 5. Per-Villager `maybeDecayGossip` (`24000` `gameTime` Ticks) & Weighted Transfer ---

func (s *Server) maybeDecayVillagerGossip2612(entityID int32, gameTime int64) {
	if s == nil || entityID <= 0 {
		return
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	ve := vp.ensureVillagerLocked(entityID)
	if !ve.HasLastGossipDecay {
		ve.HasLastGossipDecay = true
		ve.LastGossipDecayTick = gameTime
		vp.mu.Unlock()
		return
	}
	if gameTime < ve.LastGossipDecayTick+VillagerGossipDecayIntervalTicks {
		vp.mu.Unlock()
		return
	}
	ve.LastGossipDecayTick = gameTime
	vp.mu.Unlock()

	s.DecayVillagerGossips(entityID)
}

type weightedGossipTopic struct {
	uuid  [16]byte
	gtype GossipType
	value int
	score int
}

// selectWeightedGossipTopics mirrors `GossipContainer.selectGossipsForTransfer(RandomSource, int)`:
// picks up to `limit` (`10`) gossip entries weighted by `abs(value * spec.Weight)`.
func selectWeightedGossipTopics(src map[[16]byte]map[GossipType]int, limit int, rng *rand.Rand) []weightedGossipTopic {
	if len(src) == 0 || limit <= 0 {
		return nil
	}
	var all []weightedGossipTopic
	for u, tm := range src {
		for gt, val := range tm {
			if val < VillagerGossipDiscardThreshold {
				continue
			}
			spec, ok := vanillaGossipTypeSpecs[gt]
			if !ok {
				continue
			}
			sc := val * spec.Weight
			if sc < 0 {
				sc = -sc
			}
			if sc <= 0 {
				continue
			}
			all = append(all, weightedGossipTopic{uuid: u, gtype: gt, value: val, score: sc})
		}
	}
	if len(all) <= limit {
		return all
	}
	// Deterministic order before weighted sampling without replacement.
	sort.Slice(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		if all[i].uuid != all[j].uuid {
			return string(all[i].uuid[:]) < string(all[j].uuid[:])
		}
		return all[i].gtype < all[j].gtype
	})
	if rng == nil {
		return all[:limit]
	}
	selected := make([]weightedGossipTopic, 0, limit)
	pool := append([]weightedGossipTopic(nil), all...)
	for len(selected) < limit && len(pool) > 0 {
		totalWeight := 0
		for _, item := range pool {
			totalWeight += item.score
		}
		if totalWeight <= 0 {
			break
		}
		pick := rng.Intn(totalWeight)
		chosenIdx := 0
		acc := 0
		for idx, item := range pool {
			acc += item.score
			if pick < acc {
				chosenIdx = idx
				break
			}
		}
		selected = append(selected, pool[chosenIdx])
		pool = append(pool[:chosenIdx], pool[chosenIdx+1:]...)
	}
	return selected
}

// IsVillagerTrading reports whether any living player currently has a merchant
// container open for villager entityID (`AbstractVillager.isTrading()`).
func (s *Server) IsVillagerTrading(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	s.playersMu.RLock()
	defer s.playersMu.RUnlock()
	for _, sess := range s.players {
		if sess == nil || sess.IsDead() {
			continue
		}
		open := sess.OpenContainer()
		if open != nil && open.MenuType == MenuTypeMerchant && int32(open.BlockX) == entityID {
			return true
		}
	}
	return false
}

// ResetVillagerSpecialPrices resets `SpecialPriceDiff = 0` on all offers of
// villager entityID (`Villager.resetSpecialPrices()` on `stopTrading()`).
func (s *Server) ResetVillagerSpecialPrices(entityID int32) {
	if s == nil || entityID <= 0 {
		return
	}
	vtr := s.villagerTracker()
	vtr.mu.Lock()
	if vs := vtr.villagers[entityID]; vs != nil {
		for i := range vs.Offers {
			vs.Offers[i].SpecialPriceDiff = 0
		}
	}
	vtr.mu.Unlock()
}

// --- 6. Iron Golem Exact Damage, Vertical Fling, `OfferFlowerGoal` & NBT Codec ---

// RollIronGolemAttackDamage2612 implements `IronGolem.getAttackDamage()` in `26.1.2.jar`:
// `float damage = (float) getAttributeValue(ATTACK_DAMAGE)` (`15.0f`);
// `return damage == 0 ? 0 : damage / 2.0f + (float) random.nextInt((int) damage)` -> `7.5 + float32(rng.Intn(15))`.
func RollIronGolemAttackDamage2612(rng *rand.Rand) float32 {
	base := IronGolemBaseAttackDamage2612
	if base <= 0 {
		return 0
	}
	if rng == nil {
		return base/2.0 + 7.0
	}
	return base/2.0 + float32(rng.Intn(int(base)))
}

// ComputeIronGolemVerticalFling2612 implements `IronGolem.doHurtTarget` vertical
// impulse in `26.1.2.jar`:
// `double scale = Math.max(0.0, 1.0 - target.getAttributeValue(KNOCKBACK_RESISTANCE));`
// `target.setDeltaMovement(delta.add(0.0, 0.4f * scale, 0.0))`.
func ComputeIronGolemVerticalFling2612(knockbackResistance float64) float64 {
	scale := 1.0 - knockbackResistance
	if scale <= 0 {
		return 0
	}
	if scale > 1.0 {
		scale = 1.0
	}
	return IronGolemBaseVerticalFling2612 * scale
}

// RollIronGolemPersistentAngerTicks2612 samples `IronGolem.PERSISTENT_ANGER_TIME`
// (`TimeUtil.rangeOfSeconds(20, 39)` = `400..780` ticks inclusive in `26.1.2.jar`).
func RollIronGolemPersistentAngerTicks2612(rng *rand.Rand) int {
	if rng == nil {
		return IronGolemPlayerAngerTicks
	}
	return IronGolemMinPersistentAngerTicks + rng.Intn(IronGolemMaxPersistentAngerTicks-IronGolemMinPersistentAngerTicks+1)
}

// IronGolemAttackAnimationTick returns the current `attackAnimationTick` (`10..0`) of golemID.
func (s *Server) IronGolemAttackAnimationTick(golemID int32) int {
	st, ok := s.GetIronGolemState(golemID)
	if !ok {
		return 0
	}
	return st.AttackAnimationTick
}

// stepIronGolemOfferFlowerGoal implements `OfferFlowerGoal` in `26.1.2.jar`:
//   - Only during daytime (`tod < 12000`), when not offering and not in combat.
//   - `1 / 8000` chance per tick to select nearest `#minecraft:candidate_for_iron_golem_gift`
//     (`minecraft:villager` or `minecraft:copper_golem`) within `AABB.inflate(6.0, 2.0, 6.0)`
//     and start a `400`-tick poppy offer (`EntityEvent = 11`).
func (s *Server) stepIronGolemOfferFlowerGoal(golem *world.Entity, rng *rand.Rand) bool {
	if s == nil || s.entities == nil || golem == nil || rng == nil {
		return false
	}
	if s.TimeOfDay() >= VillagerRestStartTick || s.IsIronGolemOfferingPoppy(golem.ID) {
		return false
	}
	if rng.Intn(IronGolemOfferFlowerChanceDenom) != 0 {
		return false
	}
	dim := golem.EffectiveDimension()
	bestDistSq := math.MaxFloat64
	var bestTargetID int32
	for _, cand := range s.entities.AppendAll(nil) {
		if cand.EffectiveDimension() != dim {
			continue
		}
		norm := canonicalResourceName(cand.TypeName)
		if norm != MobTypeVillager && norm != MobTypeCopperGolem {
			continue
		}
		dx := cand.X - golem.X
		dy := cand.Y - golem.Y
		dz := cand.Z - golem.Z
		if math.Abs(dx) > 6.7 || math.Abs(dz) > 6.7 || dy < -2.5 || dy > 4.7 {
			continue
		}
		dSq := dx*dx + dy*dy + dz*dz
		if dSq < bestDistSq {
			bestDistSq = dSq
			bestTargetID = cand.ID
		}
	}
	if bestTargetID <= 0 {
		return false
	}
	return s.StartIronGolemOfferPoppy(golem.ID, bestTargetID)
}

// --- 7. Binary NBT Save/Load for `IronGolem` & `Villager` (`26.1.2.jar`) ---

// IronGolemEntityNBT2612 represents the persistent NBT tags of `minecraft:iron_golem`
// in `26.1.2.jar` (`IronGolem.addAdditionalSaveData` + `NeutralMob.addPersistentAngerSaveData`):
//   - `PlayerCreated` (`Byte` 0/1)
//   - `anger_end_time` (`Long`, 26.1.2 `NeutralMob`) and legacy `AngerTime` (`Int`)
//   - `angry_at` (`IntArray` of 4 big-endian int32s, `UUIDUtil.CODEC`)
//   - `Health` (`Float`)
type IronGolemEntityNBT2612 struct {
	PlayerCreated bool
	Health        float32
	AngerEndTime  int64
	HasAngerEnd   bool
	AngerTicks    int32
	AngryAtUUID   [16]byte
	HasAngryAt    bool
}

func uuidToIntArrayNBT(u [16]byte) nbt.IntArray {
	return nbt.IntArray{
		int32(binary.BigEndian.Uint32(u[0:4])),
		int32(binary.BigEndian.Uint32(u[4:8])),
		int32(binary.BigEndian.Uint32(u[8:12])),
		int32(binary.BigEndian.Uint32(u[12:16])),
	}
}

func intArrayNBTToUUID(arr nbt.IntArray) ([16]byte, bool) {
	var u [16]byte
	if len(arr) != 4 {
		return u, false
	}
	binary.BigEndian.PutUint32(u[0:4], uint32(arr[0]))
	binary.BigEndian.PutUint32(u[4:8], uint32(arr[1]))
	binary.BigEndian.PutUint32(u[8:12], uint32(arr[2]))
	binary.BigEndian.PutUint32(u[12:16], uint32(arr[3]))
	return u, true
}

// EncodeIronGolemEntityNBT2612 serializes `IronGolemEntityNBT2612` to binary NBT.
func EncodeIronGolemEntityNBT2612(d IronGolemEntityNBT2612) []byte {
	var pc byte
	if d.PlayerCreated {
		pc = 1
	}
	hp := d.Health
	if hp <= 0 {
		hp = DefaultIronGolemMaxHealth
	}
	c := nbt.NewCompound().
		Set("id", nbt.String(MobTypeIronGolem)).
		Set("PlayerCreated", nbt.Byte(pc)).
		Set("Health", nbt.Float(hp))
	if d.HasAngerEnd {
		c.Set("anger_end_time", nbt.Long(d.AngerEndTime))
	} else if d.AngerTicks > 0 {
		c.Set("AngerTime", nbt.Int(d.AngerTicks))
	}
	if d.HasAngryAt {
		c.Set("angry_at", uuidToIntArrayNBT(d.AngryAtUUID))
	}
	return nbt.MarshalNamed("", c)
}

// DecodeIronGolemEntityNBT2612 deserializes `IronGolemEntityNBT2612` from binary NBT,
// supporting both 26.1.2 `anger_end_time`/`angry_at` and legacy `AngerTime`/`AngryAt`.
func DecodeIronGolemEntityNBT2612(raw []byte) (IronGolemEntityNBT2612, error) {
	_, tag, err := nbt.UnmarshalNamed(raw)
	if err != nil {
		return IronGolemEntityNBT2612{}, err
	}
	c, ok := tag.(*nbt.Compound)
	if !ok || c == nil {
		return IronGolemEntityNBT2612{}, nil
	}
	var d IronGolemEntityNBT2612
	if t, ok := c.Get("PlayerCreated"); ok {
		if b, ok := t.(nbt.Byte); ok {
			d.PlayerCreated = b != 0
		}
	}
	if t, ok := c.Get("Health"); ok {
		if f, ok := t.(nbt.Float); ok {
			d.Health = float32(f)
		}
	}
	if t, ok := c.Get("anger_end_time"); ok {
		if l, ok := t.(nbt.Long); ok {
			d.AngerEndTime = int64(l)
			d.HasAngerEnd = true
		}
	} else if t, ok := c.Get("AngerTime"); ok {
		if iv, ok := t.(nbt.Int); ok && iv > 0 {
			d.AngerTicks = int32(iv)
		}
	}
	for _, key := range [2]string{"angry_at", "AngryAt"} {
		if t, ok := c.Get(key); ok {
			if arr, ok := t.(nbt.IntArray); ok {
				if u, valid := intArrayNBTToUUID(arr); valid {
					d.AngryAtUUID = u
					d.HasAngryAt = true
					break
				}
			}
		}
	}
	return d, nil
}

// SaveIronGolemNBT2612 exports live Iron Golem state (including `PlayerCreated`,
// `Health`, and persistent anger) as a 26.1.2 binary NBT blob.
func (s *Server) SaveIronGolemNBT2612(golemID int32) ([]byte, bool) {
	st, ok := s.GetIronGolemState(golemID)
	if !ok {
		return nil, false
	}
	gameTime, _ := s.WorldTime()
	d := IronGolemEntityNBT2612{
		PlayerCreated: st.PlayerCreated,
		Health:        s.MobHealth(golemID),
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	if ge := vp.golems[golemID]; ge != nil && ge.HasAngryPlayer && ge.AngerTicks > 0 {
		d.HasAngryAt = true
		d.AngryAtUUID = ge.AngryAtPlayer
		d.HasAngerEnd = true
		d.AngerEndTime = gameTime + int64(ge.AngerTicks)
		d.AngerTicks = int32(ge.AngerTicks)
	}
	vp.mu.Unlock()
	return EncodeIronGolemEntityNBT2612(d), true
}

// LoadIronGolemNBT2612 applies decoded 26.1.2 binary NBT onto live Iron Golem golemID.
func (s *Server) LoadIronGolemNBT2612(golemID int32, raw []byte) error {
	d, err := DecodeIronGolemEntityNBT2612(raw)
	if err != nil {
		return err
	}
	mgr := s.golemManager()
	mgr.mu.Lock()
	if st := mgr.golems[golemID]; st != nil {
		st.PlayerCreated = d.PlayerCreated
	}
	mgr.mu.Unlock()
	if d.Health > 0 {
		s.SetMobHealth(golemID, d.Health)
	}
	gameTime, _ := s.WorldTime()
	remaining := int(d.AngerTicks)
	if d.HasAngerEnd {
		rem64 := d.AngerEndTime - gameTime
		if rem64 > 0 {
			remaining = int(rem64)
		} else {
			remaining = 0
		}
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	ge := vp.ensureGolemLocked(golemID)
	if d.HasAngryAt && remaining > 0 && !d.PlayerCreated && s.Difficulty() != DifficultyPeaceful {
		ge.AngryAtPlayer = d.AngryAtUUID
		ge.HasAngryPlayer = true
		ge.AngerTicks = remaining
	} else {
		ge.HasAngryPlayer = false
		ge.AngerTicks = 0
	}
	vp.mu.Unlock()
	return nil
}

// SaveVillagerNBT2612 exports live Villager state (`VillagerData`, `Xp`,
// `FoodLevel`, `LastRestock`, `LastGossipDecay`, `RestocksToday`, `Gossips`,
// `Inventory`, and `Brain.memories.minecraft:last_slept`) as 26.1.2 binary NBT.
func (s *Server) SaveVillagerNBT2612(villagerID int32) ([]byte, bool) {
	vs, ok := s.VillagerState(villagerID)
	if !ok {
		return nil, false
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	var foodPts int
	var hasLastSlept, hasLastDecay bool
	var lastSlept, lastDecay int64
	var inv [VillagerInventorySlots]world.ItemStack
	if ve := vp.villagers[villagerID]; ve != nil {
		foodPts = ve.FoodPoints
		hasLastSlept = ve.HasLastSlept
		lastSlept = ve.LastSleptTick
		hasLastDecay = ve.HasLastGossipDecay
		lastDecay = ve.LastGossipDecayTick
		inv = ve.Inventory
	}
	vp.mu.Unlock()

	vd := nbt.NewCompound().
		Set("profession", nbt.String(canonicalResourceName(vs.Profession))).
		Set("type", nbt.String(canonicalResourceName(vs.VillagerType))).
		Set("level", nbt.Int(int32(vs.Level)))

	c := nbt.NewCompound().
		Set("id", nbt.String(MobTypeVillager)).
		Set("VillagerData", vd).
		Set("FoodLevel", nbt.Byte(byte(foodPts))).
		Set("Xp", nbt.Int(vs.XP)).
		Set("LastRestock", nbt.Long(vs.LastRestockGameTime)).
		Set("RestocksToday", nbt.Int(int32(vs.RestocksToday)))
	if hasLastDecay {
		c.Set("LastGossipDecay", nbt.Long(lastDecay))
	}

	// Gossips list: `{Target: [I; ...], Type: "...", Value: int}`.
	sm := s.villagerSocial()
	sm.mu.Lock()
	var gossipElems []nbt.Tag
	for u, byType := range sm.gossips[villagerID] {
		for gt, val := range byType {
			if val >= VillagerGossipDiscardThreshold {
				ge := nbt.NewCompound().
					Set("Target", uuidToIntArrayNBT(u)).
					Set("Type", nbt.String(string(gt))).
					Set("Value", nbt.Int(int32(val)))
				gossipElems = append(gossipElems, ge)
			}
		}
	}
	sm.mu.Unlock()
	c.Set("Gossips", nbt.List{ElemID: nbt.TagCompound, Elems: gossipElems})

	// Inventory 8-slot list.
	var invElems []nbt.Tag
	for i := 0; i < VillagerInventorySlots; i++ {
		if inv[i].IsEmpty() {
			continue
		}
		ie := nbt.NewCompound().
			Set("Slot", nbt.Byte(byte(i))).
			Set("id", nbt.String(inv[i].ResolvedID())).
			Set("count", nbt.Int(inv[i].Count))
		invElems = append(invElems, ie)
	}
	c.Set("Inventory", nbt.List{ElemID: nbt.TagCompound, Elems: invElems})

	if hasLastSlept {
		memories := nbt.NewCompound().Set("minecraft:last_slept", nbt.Long(lastSlept))
		c.Set("Brain", nbt.NewCompound().Set("memories", memories))
	}
	return nbt.MarshalNamed("", c), true
}

// LoadVillagerNBT2612 restores `SaveVillagerNBT2612` binary NBT onto live villagerID.
func (s *Server) LoadVillagerNBT2612(villagerID int32, raw []byte) error {
	_, tag, err := nbt.UnmarshalNamed(raw)
	if err != nil {
		return err
	}
	c, ok := tag.(*nbt.Compound)
	if !ok || c == nil {
		return nil
	}
	vs, ok := s.VillagerState(villagerID)
	if !ok {
		return nil
	}
	if t, ok := c.Get("VillagerData"); ok {
		if vd, ok := t.(*nbt.Compound); ok && vd != nil {
			if p, ok := vd.Get("profession"); ok {
				if ps, ok := p.(nbt.String); ok {
					vs.Profession = canonicalResourceName(string(ps))
				}
			}
			if vt, ok := vd.Get("type"); ok {
				if vts, ok := vt.(nbt.String); ok {
					vs.VillagerType = canonicalResourceName(string(vts))
				}
			}
			if lv, ok := vd.Get("level"); ok {
				if lvi, ok := lv.(nbt.Int); ok {
					vs.Level = int(lvi)
				}
			}
		}
	}
	if t, ok := c.Get("Xp"); ok {
		if iv, ok := t.(nbt.Int); ok {
			vs.XP = int32(iv)
		}
	}
	if t, ok := c.Get("LastRestock"); ok {
		if lv, ok := t.(nbt.Long); ok {
			vs.LastRestockGameTime = int64(lv)
		}
	}
	if t, ok := c.Get("RestocksToday"); ok {
		if iv, ok := t.(nbt.Int); ok {
			vs.RestocksToday = int(iv)
		}
	}
	s.SetVillagerState(villagerID, vs)

	vp := s.villagerPolish()
	vp.mu.Lock()
	ve := vp.ensureVillagerLocked(villagerID)
	if t, ok := c.Get("FoodLevel"); ok {
		if b, ok := t.(nbt.Byte); ok {
			ve.FoodPoints = int(uint8(b))
		}
	}
	if t, ok := c.Get("LastGossipDecay"); ok {
		if lv, ok := t.(nbt.Long); ok {
			ve.HasLastGossipDecay = true
			ve.LastGossipDecayTick = int64(lv)
		}
	}
	ve.Inventory = [VillagerInventorySlots]world.ItemStack{}
	if t, ok := c.Get("Inventory"); ok {
		if lst, ok := t.(nbt.List); ok {
			for _, el := range lst.Elems {
				ic, ok := el.(*nbt.Compound)
				if !ok || ic == nil {
					continue
				}
				slotT, ok1 := ic.Get("Slot")
				idT, ok2 := ic.Get("id")
				cntT, ok3 := ic.Get("count")
				if !ok1 || !ok2 || !ok3 {
					continue
				}
				sb, okS := slotT.(nbt.Byte)
				ids, okI := idT.(nbt.String)
				cnt, okC := cntT.(nbt.Int)
				if okS && okI && okC && int(sb) >= 0 && int(sb) < VillagerInventorySlots && cnt > 0 {
					ve.Inventory[int(sb)] = world.NewNamedItemStack(string(ids), int32(cnt))
				}
			}
		}
	}
	if t, ok := c.Get("Brain"); ok {
		if bc, ok := t.(*nbt.Compound); ok && bc != nil {
			if mt, ok := bc.Get("memories"); ok {
				if mc, ok := mt.(*nbt.Compound); ok && mc != nil {
					if lst, ok := mc.Get("minecraft:last_slept"); ok {
						if lv, ok := lst.(nbt.Long); ok {
							ve.HasLastSlept = true
							ve.LastSleptTick = int64(lv)
						}
					}
				}
			}
		}
	}
	vp.mu.Unlock()

	if t, ok := c.Get("Gossips"); ok {
		if lst, ok := t.(nbt.List); ok {
			sm := s.villagerSocial()
			sm.mu.Lock()
			delete(sm.gossips, villagerID)
			sm.mu.Unlock()
			for _, el := range lst.Elems {
				gc, ok := el.(*nbt.Compound)
				if !ok || gc == nil {
					continue
				}
				tgtT, ok1 := gc.Get("Target")
				typT, ok2 := gc.Get("Type")
				valT, ok3 := gc.Get("Value")
				if !ok1 || !ok2 || !ok3 {
					continue
				}
				arr, okA := tgtT.(nbt.IntArray)
				gtStr, okG := typT.(nbt.String)
				valInt, okV := valT.(nbt.Int)
				if okA && okG && okV && int(valInt) >= VillagerGossipDiscardThreshold {
					if u, valid := intArrayNBTToUUID(arr); valid {
						s.AddVillagerGossip(villagerID, u, GossipType(gtStr), int(valInt))
					}
				}
			}
		}
	}
	return nil
}
