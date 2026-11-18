package server

import (
	"math/rand"
	"strings"

	"regionio/internal/nbt"
	"regionio/internal/world"
)

// Exact Minecraft Java Edition 26.1.2 (`server-26.1.2.jar`) constants for
// `Bee` + `BeehiveBlockEntity`, `Allay` + `AllayAi` + `GoAndGiveItemsToTarget`,
// and `Armadillo` + `ArmadilloAi` + `Armadillo$ArmadilloState`.
const (
	// BeeTicksWithoutNectarBeforeGoingHome is `Bee.TICKS_WITHOUT_NECTAR_BEFORE_GOING_HOME = 3600` in 26.1.2.
	BeeTicksWithoutNectarBeforeGoingHome = 3600

	// BeeMinFindFlowerRetryCooldown / BeeMaxFindFlowerRetryCooldown match
	// `BeePollinateGoal.MIN_FIND_FLOWER_RETRY_COOLDOWN = 20` and `MAX_FIND_FLOWER_RETRY_COOLDOWN = 60`.
	BeeMinFindFlowerRetryCooldown = 20
	BeeMaxFindFlowerRetryCooldown = 60

	// BeeUnderWaterDrownThresholdTicks is `Bee.customServerAiStep`: `if (this.underWaterTicks > 20)`.
	BeeUnderWaterDrownThresholdTicks = 20

	// BeeStingDeathCountdownTicks is `Bee.STING_DEATH_COUNTDOWN = 1200` in 26.1.2.
	BeeStingDeathCountdownTicks = 1200

	// AllayPanicDurationTicks is the panic duration during which `Allay.shouldStopDancing()` returns true.
	AllayPanicDurationTicks = 100

	// ArmadilloLastHurtByTimeoutTicks is `LivingEntity.HURT_BY_TIMEOUT = 100` in 26.1.2.
	ArmadilloLastHurtByTimeoutTicks = 100
)

func isLitCampfireState2612(st uint16) bool {
	if st == world.StateAir {
		return false
	}
	base := canonicalResourceName(world.BlockName(st))
	if base != "minecraft:campfire" && base != "minecraft:soul_campfire" {
		return false
	}
	props := world.BlockProperties(st)
	return props["lit"] != "false"
}

// IsBeehiveFireNearby mirrors `BeehiveBlockEntity.isFireNearby()` in 26.1.2:
// returns true if any block in `BlockPos.betweenClosed(pos.offset(-1,-1,-1), pos.offset(1,1,1))`
// is an instance of `FireBlock` (`minecraft:fire`).
func (s *Server) IsBeehiveFireNearby(dimension string, hx, hy, hz int) bool {
	if s == nil {
		return false
	}
	cache := s.DimensionChunks(world.NormalizeDimension(dimension))
	if cache == nil {
		return false
	}
	for dy := -1; dy <= 1; dy++ {
		by := hy + dy
		if by < world.MinY || by >= world.MinY+world.WorldHeight {
			continue
		}
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				st := cache.GetBlock(hx+dx, by, hz+dz)
				if st != world.StateAir && canonicalResourceName(world.BlockName(st)) == "minecraft:fire" {
					return true
				}
			}
		}
	}
	return false
}

func isEntityTouchingWaterAt2612(s *Server, dim string, x, y, z float64) bool {
	if s == nil {
		return false
	}
	if _, _, fluidKind, submerged := s.EntitySubmergedFluidDepth(dim, x, y, z); submerged && fluidKind == world.FluidWater {
		return true
	}
	cache := s.DimensionChunks(world.NormalizeDimension(dim))
	if cache == nil {
		return false
	}
	bx, by, bz := intFloor2612(x), intFloor2612(y), intFloor2612(z)
	if by < world.MinY || by >= world.MinY+world.WorldHeight {
		return false
	}
	blk := cache.GetBlock(bx, by, bz)
	if blk == world.StateAir {
		return false
	}
	if blk == world.StateWater || canonicalResourceName(world.BlockName(blk)) == "minecraft:water" {
		return true
	}
	return world.BlockProperties(blk)["waterlogged"] == "true"
}

func isEntityTouchingLiquidAt2612(s *Server, dim string, x, y, z float64) bool {
	if isEntityTouchingWaterAt2612(s, dim, x, y, z) {
		return true
	}
	cache := s.DimensionChunks(world.NormalizeDimension(dim))
	if cache == nil {
		return false
	}
	bx, by, bz := intFloor2612(x), intFloor2612(y), intFloor2612(z)
	if by < world.MinY || by >= world.MinY+world.WorldHeight {
		return false
	}
	blk := cache.GetBlock(bx, by, bz)
	return blk != world.StateAir && canonicalResourceName(world.BlockName(blk)) == "minecraft:lava"
}

func intFloor2612(v float64) int {
	i := int(v)
	if float64(i) > v {
		return i - 1
	}
	return i
}

// OnBeeDamaged mirrors `Bee.hurtServer`: stops any active flower pollination
// (`beePollinateGoal.stopPollinating()`) even when damaged by environmental or
// non-player sources, clearing `IsRolling` and broadcasting updated metadata.
func (s *Server) OnBeeDamaged(beeEntityID int32) {
	if s == nil || beeEntityID <= 0 {
		return
	}
	mgr := s.BeeStates()
	if mgr == nil {
		return
	}
	mgr.mu.Lock()
	st := mgr.bees[beeEntityID]
	if st == nil {
		mgr.mu.Unlock()
		return
	}
	wasRolling := st.IsRolling
	st.IsRolling = false
	st.PollinatingTicks = 0
	st.PollinateAttemptTicks = 0
	if wasRolling {
		st.PollinateCooldown = BeePollinateCooldownTicks
	}
	mgr.mu.Unlock()
	if wasRolling && s.entities != nil {
		if ent := s.entities.Get(beeEntityID); ent != nil {
			s.broadcastBeeMetadata(ent)
		}
	}
}

// AllayConsidersItemEqual2612 implements vanilla 26.1.2 `Allay.allayConsidersItemEqual`:
//   - `ItemStack.isSameItem(a, b)` (same item ID; durability, enchantments, custom_name, repair_cost are ignored)
//   - `!hasNonMatchingPotion(a, b)` (`minecraft:potion_contents` potion IDs must match).
func AllayConsidersItemEqual2612(handStack world.ItemStack, handPotionID string, candStack world.ItemStack, candPotionID string) bool {
	if handStack.IsEmpty() || candStack.IsEmpty() {
		return false
	}
	if canonicalResourceName(handStack.ResolvedID()) != canonicalResourceName(candStack.ResolvedID()) {
		return false
	}
	normHandPot := normalizeOptionalPotionID2612(handPotionID)
	normCandPot := normalizeOptionalPotionID2612(candPotionID)
	return normHandPot == normCandPot
}

func normalizeOptionalPotionID2612(pot string) string {
	pot = strings.TrimSpace(pot)
	if pot == "" {
		return ""
	}
	return canonicalPotionID(pot)
}

type allayInventoryEntry struct {
	Stack    world.ItemStack
	Meta     ItemMeta
	PotionID string
}

// OnAllayDamaged sets panic state on an Allay hurt by a non-liked attacker,
// immediately stopping jukebox dancing (`Allay.shouldStopDancing`: `isPanicking()`).
func (s *Server) OnAllayDamaged(entityID int32) {
	if s == nil || entityID <= 0 {
		return
	}
	st := s.ensureAllayState(entityID)
	if st == nil {
		return
	}
	mgr := s.allayManager()
	mgr.mu.Lock()
	st.PanicTicks = AllayPanicDurationTicks
	wasDancing := st.Dancing
	if st.Dancing {
		st.Dancing = false
		st.HasJukeboxPos = false
	}
	snap := *st
	mgr.mu.Unlock()
	if wasDancing && s.entities != nil {
		if ent := s.entities.Get(entityID); ent != nil {
			s.broadcastAllayState(ent, snap)
		}
	}
}

func pickNextArmadilloPeekTimer2612(rng *rand.Rand) int {
	delay := ArmadilloPeekMinDelayTicks
	span := ArmadilloPeekMaxDelayTicks - ArmadilloPeekMinDelayTicks + 1
	if rng != nil {
		delay += rng.Intn(span)
	} else {
		delay += rand.Intn(span)
	}
	return ArmadilloScaredAnimationDurationTicks + delay
}

// --- Exact Vanilla 26.1.2 Entity NBT Round-Trip (`Bee`, `Allay`, `Armadillo`) ---

// EncodeBeeEntityNBT encodes a `BeeState` (plus health/age/inLove) into an exact
// vanilla 26.1.2 `minecraft:bee` entity NBT compound (`Bee.addAdditionalSaveData`
// + `NeutralMob.addPersistentAngerSaveData`):
//   - `hive_pos`: `IntArray[3]` (when `HasHivePos`)
//   - `flower_pos`: `IntArray[3]` (when `HasFlowerPos`)
//   - `HasNectar`: `Byte` (`0` or `1`)
//   - `HasStung`: `Byte` (`0` or `1`)
//   - `TicksSincePollination`: `Int`
//   - `CannotEnterHiveTicks`: `Int` (`stayOutOfHiveCountdown`)
//   - `CropsGrownSincePollination`: `Int`
//   - `anger_end_time`: `Long` (`-1L` when calm, `> 0` when angry)
//   - `angry_at`: `IntArray[4]` UUID (when `HasAngryAtUUID`)
func EncodeBeeEntityNBT(st BeeState, health float32, ageTicks, inLoveTicks int, currentGameTime int64) *nbt.Compound {
	c := nbt.NewCompound()
	c.Set("id", nbt.String(MobTypeBee))
	if health <= 0 {
		health = DefaultBeeMaxHealth
	}
	c.Set("Health", nbt.Float(health))
	c.Set("Age", nbt.Int(int32(ageTicks)))
	c.Set("InLove", nbt.Int(int32(inLoveTicks)))
	if st.AgeLocked {
		c.Set("AgeLocked", nbt.Byte(1))
	}
	if strings.TrimSpace(st.CustomName) != "" {
		c.Set("CustomName", nbt.String(strings.TrimSpace(st.CustomName)))
	}
	if st.HasHivePos {
		c.Set("hive_pos", nbt.IntArray{int32(st.HiveX), int32(st.HiveY), int32(st.HiveZ)})
	}
	if st.HasFlowerPos {
		c.Set("flower_pos", nbt.IntArray{int32(st.FlowerX), int32(st.FlowerY), int32(st.FlowerZ)})
	}
	if st.HasNectar {
		c.Set("HasNectar", nbt.Byte(1))
	} else {
		c.Set("HasNectar", nbt.Byte(0))
	}
	if st.HasStung {
		c.Set("HasStung", nbt.Byte(1))
	} else {
		c.Set("HasStung", nbt.Byte(0))
	}
	c.Set("TicksSincePollination", nbt.Int(int32(st.TicksSincePollination)))
	cannotEnter := st.StayOutOfHiveCountdown
	if st.CannotEnterHiveTicks > cannotEnter {
		cannotEnter = st.CannotEnterHiveTicks
	}
	c.Set("CannotEnterHiveTicks", nbt.Int(int32(cannotEnter)))
	c.Set("CropsGrownSincePollination", nbt.Int(int32(st.CropsGrownSincePollination)))

	angerEnd := int64(-1)
	if st.AngerEndTime > 0 {
		angerEnd = st.AngerEndTime
	} else if st.AngerTicks > 0 {
		angerEnd = currentGameTime + int64(st.AngerTicks)
	}
	c.Set("anger_end_time", nbt.Long(angerEnd))
	if st.HasAngryAtUUID {
		c.Set("angry_at", uuidToIntArrayNBT(st.AngryAtUUID))
	} else if st.TargetSession != nil {
		c.Set("angry_at", uuidToIntArrayNBT(st.TargetSession.Profile.UUID))
	}
	return c
}

// DecodeBeeEntityNBT decodes a vanilla 26.1.2 `minecraft:bee` entity NBT compound
// (`Bee.readAdditionalSaveData` + `NeutralMob.readPersistentAngerSaveData`, including
// legacy `AngerTime` / `AngryAt` fallback).
func DecodeBeeEntityNBT(comp *nbt.Compound, currentGameTime int64) (st BeeState, health float32, ageTicks, inLoveTicks int, ok bool) {
	if comp == nil {
		return BeeState{}, 0, 0, 0, false
	}
	if idStr, hasID := nbtGetString(comp, "id"); hasID && canonicalResourceName(idStr) != MobTypeBee {
		return BeeState{}, 0, 0, 0, false
	}
	health = DefaultBeeMaxHealth
	if hTag, okH := comp.Get("Health"); okH {
		switch hv := hTag.(type) {
		case nbt.Float:
			if hv > 0 {
				health = float32(hv)
			}
		case nbt.Double:
			if hv > 0 {
				health = float32(hv)
			}
		}
	}
	if aTag, okA := comp.Get("Age"); okA {
		if ai, okI := aTag.(nbt.Int); okI {
			ageTicks = int(ai)
		}
	}
	if lTag, okL := comp.Get("InLove"); okL {
		if li, okI := lTag.(nbt.Int); okI {
			inLoveTicks = int(li)
		}
	}
	if alTag, okAL := comp.Get("AgeLocked"); okAL {
		if b, okB := alTag.(nbt.Byte); okB && b != 0 {
			st.AgeLocked = true
		}
	}
	if cnTag, okCN := comp.Get("CustomName"); okCN && cnTag != nil {
		st.CustomName = world.ExtractCustomNameFromNBTTag(cnTag)
	}
	if nTag, okN := comp.Get("HasNectar"); okN {
		if b, okB := nTag.(nbt.Byte); okB && b != 0 {
			st.HasNectar = true
		}
	}
	if sTag, okS := comp.Get("HasStung"); okS {
		if b, okB := sTag.(nbt.Byte); okB && b != 0 {
			st.HasStung = true
		}
	}
	if tspTag, okT := comp.Get("TicksSincePollination"); okT {
		if iv, okI := tspTag.(nbt.Int); okI {
			st.TicksSincePollination = int(iv)
		}
	}
	if cehTag, okC := comp.Get("CannotEnterHiveTicks"); okC {
		if iv, okI := cehTag.(nbt.Int); okI {
			st.StayOutOfHiveCountdown = int(iv)
			st.CannotEnterHiveTicks = int(iv)
		}
	}
	if cgTag, okCG := comp.Get("CropsGrownSincePollination"); okCG {
		if iv, okI := cgTag.(nbt.Int); okI {
			st.CropsGrownSincePollination = int(iv)
		}
	}
	if hpTag, okHP := comp.Get("hive_pos"); okHP {
		if arr, okArr := hpTag.(nbt.IntArray); okArr && len(arr) == 3 {
			st.HasHivePos = true
			st.HiveX, st.HiveY, st.HiveZ = int(arr[0]), int(arr[1]), int(arr[2])
		}
	}
	if fpTag, okFP := comp.Get("flower_pos"); okFP {
		if arr, okArr := fpTag.(nbt.IntArray); okArr && len(arr) == 3 {
			st.HasFlowerPos = true
			st.FlowerX, st.FlowerY, st.FlowerZ = int(arr[0]), int(arr[1]), int(arr[2])
		}
	}
	st.AngerEndTime = -1
	if aeTag, okAE := comp.Get("anger_end_time"); okAE {
		if lv, okL := aeTag.(nbt.Long); okL {
			st.AngerEndTime = int64(lv)
			if int64(lv) > currentGameTime {
				st.AngerTicks = int(int64(lv) - currentGameTime)
			}
		}
	} else if legacyTag, okLeg := comp.Get("AngerTime"); okLeg {
		if iv, okI := legacyTag.(nbt.Int); okI && iv > 0 {
			st.AngerTicks = int(iv)
			st.AngerEndTime = currentGameTime + int64(iv)
		}
	}
	if atTag, okAT := comp.Get("angry_at"); okAT {
		if arr, okArr := atTag.(nbt.IntArray); okArr {
			if u, valid := intArrayNBTToUUID(arr); valid {
				st.AngryAtUUID = u
				st.HasAngryAtUUID = true
			}
		}
	} else if legAt, okLegAT := comp.Get("AngryAt"); okLegAT {
		if arr, okArr := legAt.(nbt.IntArray); okArr {
			if u, valid := intArrayNBTToUUID(arr); valid {
				st.AngryAtUUID = u
				st.HasAngryAtUUID = true
			}
		}
	}
	return st, health, ageTicks, inLoveTicks, true
}

func encodeAllaySingleItemNBT2612(stack world.ItemStack, meta ItemMeta, potionID string) *nbt.Compound {
	if stack.IsEmpty() {
		return nil
	}
	c := nbt.NewCompound().
		Set("id", nbt.String(canonicalResourceName(stack.ResolvedID()))).
		Set("count", nbt.Int(stack.Count))
	comps := nbt.NewCompound()
	if meta.Damage > 0 {
		comps.Set("minecraft:damage", nbt.Int(meta.Damage))
	}
	if strings.TrimSpace(meta.CustomName) != "" {
		comps.Set("minecraft:custom_name", nbt.String(meta.CustomName))
	}
	if meta.RepairCost > 0 {
		comps.Set("minecraft:repair_cost", nbt.Int(meta.RepairCost))
	}
	if len(meta.Enchantments) > 0 {
		ench := nbt.NewCompound()
		for k, v := range meta.Enchantments {
			if v > 0 {
				ench.Set(canonicalResourceName(k), nbt.Int(int32(v)))
			}
		}
		if ench.Len() > 0 {
			comps.Set("minecraft:enchantments", ench)
		}
	}
	if normPot := normalizeOptionalPotionID2612(potionID); normPot != "" {
		comps.Set("minecraft:potion_contents", nbt.NewCompound().Set("potion", nbt.String(normPot)))
	}
	if comps.Len() > 0 {
		c.Set("components", comps)
	}
	return c
}

func decodeAllaySingleItemNBT2612(c *nbt.Compound) (stack world.ItemStack, meta ItemMeta, potionID string, ok bool) {
	if c == nil {
		return world.ItemStack{}, ItemMeta{}, "", false
	}
	idStr, okID := nbtGetString(c, "id")
	if !okID || idStr == "" {
		return world.ItemStack{}, ItemMeta{}, "", false
	}
	cnt := int32(1)
	if cntTag, okC := c.Get("count"); okC {
		switch cv := cntTag.(type) {
		case nbt.Int:
			cnt = int32(cv)
		case nbt.Byte:
			cnt = int32(cv)
		}
	}
	if cnt <= 0 {
		return world.ItemStack{}, ItemMeta{}, "", false
	}
	stack = world.NewNamedItemStack(canonicalResourceName(idStr), cnt)
	if compTag, okComp := c.Get("components"); okComp {
		if cc, isComp := compTag.(*nbt.Compound); isComp && cc != nil {
			if dTag, okD := cc.Get("minecraft:damage"); okD {
				if di, okI := dTag.(nbt.Int); okI {
					meta.Damage = int32(di)
				}
			}
			if cnTag, okCN := cc.Get("minecraft:custom_name"); okCN && cnTag != nil {
				meta.CustomName = world.ExtractCustomNameFromNBTTag(cnTag)
			}
			if rcTag, okRC := cc.Get("minecraft:repair_cost"); okRC {
				if rci, okI := rcTag.(nbt.Int); okI {
					meta.RepairCost = int32(rci)
				}
			}
			if eTag, okE := cc.Get("minecraft:enchantments"); okE {
				if ec, isEC := eTag.(*nbt.Compound); isEC && ec != nil {
					for _, k := range ec.Keys() {
						if lvTag, okL := ec.Get(k); okL {
							if lvi, okI := lvTag.(nbt.Int); okI && lvi > 0 {
								meta.SetEnchantment(canonicalResourceName(k), int(lvi))
							}
						}
					}
				}
			}
			if pTag, okP := cc.Get("minecraft:potion_contents"); okP {
				if pc, isPC := pTag.(*nbt.Compound); isPC && pc != nil {
					if potStr, okPS := nbtGetString(pc, "potion"); okPS && potStr != "" {
						potionID = canonicalPotionID(potStr)
					}
				}
			}
		}
	}
	return stack, meta, potionID, true
}

// EncodeAllayEntityNBT encodes an `AllayBehaviorState` into an exact vanilla
// 26.1.2 `minecraft:allay` entity NBT compound (`Allay.addAdditionalSaveData` + `Brain`):
//   - `DuplicationCooldown`: `Long`
//   - `Inventory`: `List` of collected item stacks
//   - `equipment`: `{mainhand: ...}` (when holding filter item)
//   - `Brain.memories`: `minecraft:liked_player`, `minecraft:liked_noteblock`,
//     `minecraft:liked_noteblock_cooldown_ticks`, `minecraft:item_pickup_cooldown_ticks`.
func EncodeAllayEntityNBT(st AllayBehaviorState, health float32) *nbt.Compound {
	c := nbt.NewCompound()
	c.Set("id", nbt.String(MobTypeAllay))
	if health <= 0 {
		health = DefaultAllayMaxHealth
	}
	c.Set("Health", nbt.Float(health))
	c.Set("DuplicationCooldown", nbt.Long(int64(st.DuplicationCooldownTicks)))

	if !st.HeldItem.IsEmpty() {
		if mh := encodeAllaySingleItemNBT2612(cloneAllayItemStackWithCount(st.HeldItem, 1), st.HeldItemMeta, st.HeldPotionID); mh != nil {
			c.Set("equipment", nbt.NewCompound().Set("mainhand", mh))
		}
	}

	var invElems []nbt.Tag
	if len(st.CollectedStacks) > 0 {
		for _, entry := range st.CollectedStacks {
			if ic := encodeAllaySingleItemNBT2612(entry.Stack, entry.Meta, entry.PotionID); ic != nil {
				invElems = append(invElems, ic)
			}
		}
	} else if st.CollectedCount > 0 && !st.HeldItem.IsEmpty() {
		if ic := encodeAllaySingleItemNBT2612(cloneAllayItemStackWithCount(st.HeldItem, st.CollectedCount), ItemMeta{}, st.HeldPotionID); ic != nil {
			invElems = append(invElems, ic)
		}
	}
	elemID := byte(nbt.TagEnd)
	if len(invElems) > 0 {
		elemID = nbt.TagCompound
	}
	c.Set("Inventory", nbt.List{ElemID: elemID, Elems: invElems})

	memories := nbt.NewCompound()
	if st.HasLikedPlayer {
		memories.Set("minecraft:liked_player", nbt.NewCompound().Set("value", uuidToIntArrayNBT(st.LikedPlayerUUID)))
	}
	if st.NoteBlockListenTicks > 0 {
		dim := st.NoteBlockDimension
		if dim == "" {
			dim = world.DimensionOverworld
		}
		nbVal := nbt.NewCompound().
			Set("dimension", nbt.String(world.NormalizeDimension(dim))).
			Set("pos", nbt.IntArray{int32(st.NoteBlockPos[0]), int32(st.NoteBlockPos[1]), int32(st.NoteBlockPos[2])})
		memories.Set("minecraft:liked_noteblock", nbt.NewCompound().Set("value", nbVal))
		memories.Set("minecraft:liked_noteblock_cooldown_ticks", nbt.NewCompound().Set("value", nbt.Int(int32(st.NoteBlockListenTicks))))
	}
	if st.DeliveryDelayTicks > 0 {
		memories.Set("minecraft:item_pickup_cooldown_ticks", nbt.NewCompound().Set("value", nbt.Int(int32(st.DeliveryDelayTicks))))
	}
	c.Set("Brain", nbt.NewCompound().Set("memories", memories))
	return c
}

// DecodeAllayEntityNBT decodes a vanilla 26.1.2 `minecraft:allay` entity NBT compound.
func DecodeAllayEntityNBT(comp *nbt.Compound) (st AllayBehaviorState, health float32, ok bool) {
	if comp == nil {
		return AllayBehaviorState{}, 0, false
	}
	if idStr, hasID := nbtGetString(comp, "id"); hasID && canonicalResourceName(idStr) != MobTypeAllay {
		return AllayBehaviorState{}, 0, false
	}
	health = DefaultAllayMaxHealth
	if hTag, okH := comp.Get("Health"); okH {
		switch hv := hTag.(type) {
		case nbt.Float:
			if hv > 0 {
				health = float32(hv)
			}
		case nbt.Double:
			if hv > 0 {
				health = float32(hv)
			}
		}
	}
	st.CanDuplicate = true
	if dcTag, okDC := comp.Get("DuplicationCooldown"); okDC {
		switch dv := dcTag.(type) {
		case nbt.Long:
			if dv > 0 {
				st.DuplicationCooldownTicks = int(dv)
				st.CanDuplicate = false
			}
		case nbt.Int:
			if dv > 0 {
				st.DuplicationCooldownTicks = int(dv)
				st.CanDuplicate = false
			}
		}
	}
	if eqTag, okEQ := comp.Get("equipment"); okEQ {
		if eqc, isEQ := eqTag.(*nbt.Compound); isEQ && eqc != nil {
			if mhTag, okMH := eqc.Get("mainhand"); okMH {
				if mhc, isMH := mhTag.(*nbt.Compound); isMH && mhc != nil {
					if stack, meta, pot, okS := decodeAllaySingleItemNBT2612(mhc); okS {
						st.HeldItem = cloneAllayItemStackWithCount(stack, 1)
						st.HeldItemMeta = meta
						st.HeldPotionID = pot
					}
				}
			}
		}
	}
	if invTag, okInv := comp.Get("Inventory"); okInv {
		if lst, isLst := invTag.(nbt.List); isLst {
			for _, el := range lst.Elems {
				if ic, isIC := el.(*nbt.Compound); isIC && ic != nil {
					if stack, meta, pot, okS := decodeAllaySingleItemNBT2612(ic); okS {
						st.CollectedStacks = append(st.CollectedStacks, allayInventoryEntry{
							Stack:    stack,
							Meta:     meta,
							PotionID: pot,
						})
						st.CollectedCount += stack.Count
					}
				}
			}
		}
	}
	if brTag, okBr := comp.Get("Brain"); okBr {
		if brc, isBr := brTag.(*nbt.Compound); isBr && brc != nil {
			if memTag, okMem := brc.Get("memories"); okMem {
				if memc, isMem := memTag.(*nbt.Compound); isMem && memc != nil {
					if lpTag, okLP := memc.Get("minecraft:liked_player"); okLP {
						if arr, okArr := unwrapMemoryValueTag2612(lpTag).(nbt.IntArray); okArr {
							if u, valid := intArrayNBTToUUID(arr); valid {
								st.LikedPlayerUUID = u
								st.HasLikedPlayer = true
							}
						}
					}
					if nbTag, okNB := memc.Get("minecraft:liked_noteblock"); okNB {
						if nbc, isNB := unwrapMemoryValueTag2612(nbTag).(*nbt.Compound); isNB && nbc != nil {
							if dimStr, okD := nbtGetString(nbc, "dimension"); okD {
								st.NoteBlockDimension = world.NormalizeDimension(dimStr)
							}
							if posTag, okP := nbc.Get("pos"); okP {
								if arr, okArr := posTag.(nbt.IntArray); okArr && len(arr) == 3 {
									st.NoteBlockPos = [3]int{int(arr[0]), int(arr[1]), int(arr[2])}
								}
							}
						}
					}
					if cdTag, okCD := memc.Get("minecraft:liked_noteblock_cooldown_ticks"); okCD {
						if iv, okI := unwrapMemoryValueTag2612(cdTag).(nbt.Int); okI && iv > 0 {
							st.NoteBlockListenTicks = int(iv)
						}
					}
					if pcTag, okPC := memc.Get("minecraft:item_pickup_cooldown_ticks"); okPC {
						if iv, okI := unwrapMemoryValueTag2612(pcTag).(nbt.Int); okI && iv > 0 {
							st.DeliveryDelayTicks = int(iv)
						}
					}
				}
			}
		}
	}
	return st, health, true
}

func unwrapMemoryValueTag2612(t nbt.Tag) nbt.Tag {
	if c, ok := t.(*nbt.Compound); ok && c != nil {
		if v, hasVal := c.Get("value"); hasVal {
			return v
		}
	}
	return t
}

// EncodeArmadilloEntityNBT encodes an `ArmadilloBehaviorState` into an exact
// vanilla 26.1.2 `minecraft:armadillo` entity NBT compound (`Armadillo.addAdditionalSaveData`):
//   - `state`: `"idle" | "rolling" | "scared" | "unrolling"`
//   - `scute_time`: `Int`.
func EncodeArmadilloEntityNBT(st ArmadilloBehaviorState, health float32, ageTicks, inLoveTicks int) *nbt.Compound {
	c := nbt.NewCompound()
	c.Set("id", nbt.String(MobTypeArmadillo))
	if health <= 0 {
		health = DefaultArmadilloMaxHealth
	}
	c.Set("Health", nbt.Float(health))
	c.Set("Age", nbt.Int(int32(ageTicks)))
	c.Set("InLove", nbt.Int(int32(inLoveTicks)))
	c.Set("state", nbt.String(ArmadilloStateName(st.State)))
	c.Set("scute_time", nbt.Int(int32(st.ScuteShedTimer)))
	return c
}

// DecodeArmadilloEntityNBT decodes a vanilla 26.1.2 `minecraft:armadillo` entity NBT compound.
func DecodeArmadilloEntityNBT(comp *nbt.Compound) (st ArmadilloBehaviorState, health float32, ageTicks, inLoveTicks int, ok bool) {
	if comp == nil {
		return ArmadilloBehaviorState{}, 0, 0, 0, false
	}
	if idStr, hasID := nbtGetString(comp, "id"); hasID && canonicalResourceName(idStr) != MobTypeArmadillo {
		return ArmadilloBehaviorState{}, 0, 0, 0, false
	}
	health = DefaultArmadilloMaxHealth
	if hTag, okH := comp.Get("Health"); okH {
		switch hv := hTag.(type) {
		case nbt.Float:
			if hv > 0 {
				health = float32(hv)
			}
		case nbt.Double:
			if hv > 0 {
				health = float32(hv)
			}
		}
	}
	if aTag, okA := comp.Get("Age"); okA {
		if ai, okI := aTag.(nbt.Int); okI {
			ageTicks = int(ai)
		}
	}
	if lTag, okL := comp.Get("InLove"); okL {
		if li, okI := lTag.(nbt.Int); okI {
			inLoveTicks = int(li)
		}
	}
	st.State = ArmadilloStateIdle
	if sStr, okS := nbtGetString(comp, "state"); okS {
		if parsed, valid := ArmadilloStateFromName(sStr); valid {
			st.State = parsed
		}
	}
	st.ScuteShedTimer = ArmadilloScuteShedMinTicks
	if scTag, okSC := comp.Get("scute_time"); okSC {
		if iv, okI := scTag.(nbt.Int); okI {
			st.ScuteShedTimer = int(iv)
		}
	}
	return st, health, ageTicks, inLoveTicks, true
}
