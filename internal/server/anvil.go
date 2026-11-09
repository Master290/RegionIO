package server

import (
	"errors"
	"math"
	"math/rand"
	"strings"
	"unicode/utf8"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Serverbound rename_item packet ID in Minecraft 26.1.2 (protocol 775).
const PlayRenameItemSB int32 = 0x30

// Anvil container layout and mechanic constants (MenuTypeAnvil = 8).
const (
	AnvilSlotLeft        = 0
	AnvilSlotRight       = 1
	AnvilSlotResult      = 2
	AnvilInputSlotCount  = 2
	AnvilMenuSlotCount   = 39 // 3 anvil + 27 main inventory + 9 hotbar
	AnvilDataLevelCost   = 0  // ContainerData property 0 = level cost
	MaxAnvilSurvivalCost = 40 // "Too Expensive!" threshold in survival mode
	MaxAnvilNameRunes    = 50
	AnvilBreakChance     = 0.12

	// AnvilDamageChanceOnUse aliases AnvilBreakChance (12% chance per anvil use in survival mode).
	AnvilDamageChanceOnUse = AnvilBreakChance

	// Falling anvil damage and degradation constants (vanilla 26.1.2 AnvilBlock / FallingBlockEntity).
	AnvilFallDamagePerBlock       = 2.0
	MaxAnvilFallDamage            = 40.0
	AnvilFallDegradeChancePerStep = 0.05
	AnvilHelmetDamageFactor       = 0.75

	// Vanilla LevelEvent IDs for Anvil sounds and particles (ClientboundLevelEventPacket 0x2e).
	LevelEventSoundAnvilBroken int32 = 1029
	LevelEventSoundAnvilUsed   int32 = 1030
	LevelEventSoundAnvilLand   int32 = 1031
)

// AnvilResult holds the computed output and cost of an anvil operation.
type AnvilResult struct {
	Output              world.ItemStack
	OutputMeta          ItemMeta
	LevelCost           int32
	RepairItemCountCost int32
	TooExpensive        bool
}

// AnvilCombineResult aliases AnvilResult.
type AnvilCombineResult = AnvilResult

// IsAnvilBlock reports whether state is an anvil, chipped_anvil, or damaged_anvil block.
func IsAnvilBlock(state uint16) bool {
	if world.IsAnvilBlockState(state) {
		return true
	}
	switch world.BlockName(state) {
	case "minecraft:anvil", "minecraft:chipped_anvil", "minecraft:damaged_anvil":
		return true
	default:
		return false
	}
}

// AnvilBlockState returns the block state ID for an anvil variant
// ("minecraft:anvil", "minecraft:chipped_anvil", "minecraft:damaged_anvil")
// with the given horizontal facing ("north", "south", "west", "east").
func AnvilBlockState(blockName, facing string) uint16 {
	name := canonicalEnchantmentID(blockName)
	switch name {
	case "minecraft:anvil", "minecraft:chipped_anvil", "minecraft:damaged_anvil":
	default:
		name = "minecraft:anvil"
	}
	f := strings.ToLower(strings.TrimSpace(facing))
	switch f {
	case "north", "south", "west", "east":
	default:
		f = "north"
	}
	if st, ok := world.BlockStateIDByProperties(name, map[string]string{"facing": f}); ok {
		return st
	}
	return blockBaseStateByName(name)
}

// AnvilFacing returns the horizontal facing ("north", "south", "west", "east")
// of an anvil block state, defaulting to "north".
func AnvilFacing(state uint16) string {
	if props := world.BlockProperties(state); props != nil {
		if f := props["facing"]; f != "" {
			return f
		}
	}
	return "north"
}

// StepAnvilDamageState advances an anvil block state by one damage stage
// (anvil -> chipped_anvil -> damaged_anvil -> air), preserving facing offset.
// When the anvil breaks completely, nextState is world.StateAir and broken is true.
func StepAnvilDamageState(state uint16) (nextState uint16, broken bool) {
	if next, brk := world.StepAnvilDamageBlockState(state); brk || next != state {
		return next, brk
	}
	curName := world.BlockName(state)
	var nextName string
	switch curName {
	case "minecraft:anvil":
		nextName = "minecraft:chipped_anvil"
	case "minecraft:chipped_anvil":
		nextName = "minecraft:damaged_anvil"
	case "minecraft:damaged_anvil":
		return world.StateAir, true
	default:
		return state, false
	}

	curBase := blockBaseStateByName(curName)
	nextBase := blockBaseStateByName(nextName)
	if nextBase == world.StateAir {
		return state, false
	}
	if curBase != world.StateAir && state >= curBase && state-curBase < 4 {
		candidate := nextBase + (state - curBase)
		if world.BlockName(candidate) == nextName {
			return candidate, false
		}
	}
	return nextBase, false
}

func blockBaseStateByName(blockName string) uint16 {
	itemID, ok := world.ItemNameToID(blockName)
	if !ok || itemID <= 0 {
		return world.StateAir
	}
	base, ok := world.ItemToBlock(itemID)
	if !ok {
		return world.StateAir
	}
	whileBase := base
	for whileBase > 0 && world.BlockName(whileBase-1) == blockName {
		whileBase--
	}
	return whileBase
}

// CalculateFallingAnvilDamage computes the vanilla entity damage dealt by an
// anvil landing after falling fallDistance blocks:
//   - 0 damage if fallDistance <= 1.0
//   - min(floor(ceil(fallDistance - 1.0) * 2.0), 40.0) otherwise
func CalculateFallingAnvilDamage(fallDistance float64) float32 {
	if fallDistance <= 1.0 {
		return 0
	}
	blocksAfterFirst := math.Ceil(fallDistance - 1.0)
	if blocksAfterFirst <= 0 {
		return 0
	}
	dmg := math.Floor(blocksAfterFirst * AnvilFallDamagePerBlock)
	if dmg > MaxAnvilFallDamage {
		dmg = MaxAnvilFallDamage
	}
	return float32(dmg)
}

// FallingAnvilDegradeChance returns the vanilla probability in [0.0, 1.0] that
// a falling anvil degrades by one damage stage when landing after falling
// fallDistance blocks:
//   - 0.0 if fallDistance <= 1.0
//   - min((ceil(fallDistance - 1.0) + 1) * 0.05, 1.0) otherwise
func FallingAnvilDegradeChance(fallDistance float64) float64 {
	if fallDistance <= 1.0 {
		return 0
	}
	steps := int(math.Ceil(fallDistance-1.0)) + 1
	if steps <= 1 {
		return 0
	}
	chance := float64(steps) * AnvilFallDegradeChancePerStep
	if chance > 1.0 {
		chance = 1.0
	}
	return chance
}

// DamageAnvilOnFallLanding rolls whether an anvil block state degrades when
// landing from a fall of fallDistance blocks.
func DamageAnvilOnFallLanding(state uint16, fallDistance float64, rng *rand.Rand) (nextState uint16, degraded bool, broken bool) {
	if !IsAnvilBlock(state) {
		return state, false, false
	}
	chance := FallingAnvilDegradeChance(fallDistance)
	if chance <= 0 {
		return state, false, false
	}
	roll := rand.Float64()
	if rng != nil {
		roll = rng.Float64()
	}
	if roll >= chance {
		return state, false, false
	}
	next, brk := StepAnvilDamageState(state)
	return next, true, brk
}

// HandleFallingAnvilLanding processes a falling anvil landing at (x, y, z) in dim
// after falling fallDistance blocks:
//   - Deals falling-anvil damage (2 HP per block after the first, max 40 HP) to
//     mobs and survival/adventure players in the landing block, reducing damage
//     by 25% if a player wears a helmet while damaging the helmet.
//   - Rolls fall degradation (5% * blocks fallen when fallDistance > 1) to advance
//     anvil -> chipped_anvil -> damaged_anvil -> air.
//   - Emits LevelEventSoundAnvilBroken (1029) if destroyed or LevelEventSoundAnvilLand (1031) otherwise.
func (s *Server) HandleFallingAnvilLanding(dim string, x, y, z int, state uint16, fallDistance float64, rng *rand.Rand) (finalState uint16, degraded bool, broken bool, damagedEntities int) {
	if s == nil {
		return state, false, false, 0
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if state == 0 && cache != nil {
		state = cache.GetBlock(x, y, z)
	}
	if !IsAnvilBlock(state) {
		return state, false, false, 0
	}

	dmg := CalculateFallingAnvilDamage(fallDistance)
	if dmg > 0 {
		minX, maxX := float64(x), float64(x+1)
		minY, maxY := float64(y), float64(y+2)
		minZ, maxZ := float64(z), float64(z+1)

		// Damage mobs overlapping the landing cell.
		if s.entities != nil {
			nearby := s.entities.AppendInChunkRange(nil, int32(x>>4), int32(z>>4), 1)
			for i := range nearby {
				ent := &nearby[i]
				if ent.IsItem() || ent.IsExperienceOrb() || ent.EffectiveDimension() != normDim {
					continue
				}
				if ent.X >= minX-0.3 && ent.X <= maxX+0.3 &&
					ent.Y >= minY-0.5 && ent.Y <= maxY &&
					ent.Z >= minZ-0.3 && ent.Z <= maxZ+0.3 {
					hp := s.MobHealth(ent.ID)
					if hp <= 0 {
						continue
					}
					damagedEntities++
					rem := hp - dmg
					if rem > 0 {
						s.SetMobHealth(ent.ID, rem)
					} else {
						s.SetMobHealth(ent.ID, 0)
						s.entities.Remove(ent.ID)
						cp := ent.ChunkPos()
						s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayRemoveEntities,
							encodeRemoveEntitiesPacket(nil, []int32{ent.ID}))
					}
				}
			}
		}

		// Damage survival/adventure players overlapping the landing cell.
		s.playersMu.RLock()
		sessions := make([]*PlayerSession, 0, len(s.players))
		for _, ps := range s.players {
			sessions = append(sessions, ps)
		}
		s.playersMu.RUnlock()

		for _, sess := range sessions {
			if sess == nil || sess.IsDead() {
				continue
			}
			snap := sess.Snapshot()
			if world.NormalizeDimension(snap.Dimension) != normDim {
				continue
			}
			if snap.GameMode != GameModeSurvival && snap.GameMode != GameModeAdventure {
				continue
			}
			if snap.X >= minX-0.3 && snap.X <= maxX+0.3 &&
				snap.Y >= minY-0.5 && snap.Y <= maxY &&
				snap.Z >= minZ-0.3 && snap.Z <= maxZ+0.3 {
				playerDmg := dmg
				helmet := sess.InventorySlot(ArmorSlotHead)
				if !helmet.IsEmpty() && IsArmorItem(helmet.ResolvedID()) {
					playerDmg *= AnvilHelmetDamageFactor
					helmetMeta := GetSlotItemMeta(sess, ArmorSlotHead)
					wear := int32(math.Max(1, math.Floor(float64(dmg)/4.0)))
					newHelmet, newMeta, broke := DamageItem(helmet, helmetMeta, wear, rng)
					sess.SetInventorySlot(ArmorSlotHead, newHelmet)
					if broke {
						SetSlotItemMeta(sess, ArmorSlotHead, ItemMeta{})
					} else {
						SetSlotItemMeta(sess, ArmorSlotHead, newMeta)
					}
				}
				beforeHP := sess.Snapshot().Health
				s.DamagePlayer(sess, playerDmg, "falling_anvil")
				if sess.Snapshot().Health < beforeHP {
					damagedEntities++
				}
			}
		}
	}

	nextState, didDegrade, didBreak := DamageAnvilOnFallLanding(state, fallDistance, rng)
	if cache != nil && didDegrade && nextState != state {
		_, res := cache.ApplyBlockWithUpdates(x, y, z, nextState)
		s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
	}
	cx, cz := int32(x>>4), int32(z>>4)
	if didBreak {
		s.BroadcastDimensionChunk(normDim, cx, cz, PlayLevelEventCB,
			EncodeLevelEventPacket(LevelEventSoundAnvilBroken, x, y, z, 0, false))
	} else {
		s.BroadcastDimensionChunk(normDim, cx, cz, PlayLevelEventCB,
			EncodeLevelEventPacket(LevelEventSoundAnvilLand, x, y, z, 0, false))
	}
	return nextState, didDegrade, didBreak, damagedEntities
}

func slotIdxSession(sess *PlayerSession) *PlayerSession {
	return sess
}

// ProcessFallingBlockLandings processes all anvil landings recorded in a
// world.BlockUpdateResult.Landings slice.
func (s *Server) ProcessFallingBlockLandings(dim string, landings []world.FallingBlockLanding, rng *rand.Rand) {
	if s == nil || len(landings) == 0 {
		return
	}
	for _, l := range landings {
		if IsAnvilBlock(l.State) {
			s.HandleFallingAnvilLanding(dim, l.Pos.X, l.Pos.Y, l.Pos.Z, l.State, float64(l.FallDistance), rng)
		}
	}
}

// IsValidRepairMaterial reports whether materialItemName is the valid anvil
// repair unit material for targetItemName (e.g. iron_ingot for iron tools/armor,
// diamond for diamond tools/armor, planks for wooden tools/shields).
func IsValidRepairMaterial(targetItemName, materialItemName string) bool {
	target := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(targetItemName)), "minecraft:")
	mat := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(materialItemName)), "minecraft:")
	if target == "" || mat == "" {
		return false
	}

	switch {
	case strings.HasPrefix(target, "wooden_") || target == "shield":
		return strings.HasSuffix(mat, "_planks")
	case strings.HasPrefix(target, "stone_"):
		return mat == "cobblestone" || mat == "cobbled_deepslate" || mat == "blackstone"
	case strings.HasPrefix(target, "copper_"):
		return mat == "copper_ingot"
	case strings.HasPrefix(target, "iron_") || strings.HasPrefix(target, "chainmail_"):
		return mat == "iron_ingot"
	case strings.HasPrefix(target, "golden_"):
		return mat == "gold_ingot"
	case strings.HasPrefix(target, "diamond_"):
		return mat == "diamond"
	case strings.HasPrefix(target, "netherite_"):
		return mat == "netherite_ingot"
	case strings.HasPrefix(target, "leather_"):
		return mat == "leather"
	case target == "turtle_helmet":
		return mat == "turtle_scute" || mat == "scute"
	case target == "elytra":
		return mat == "phantom_membrane"
	case target == "mace":
		return mat == "breeze_rod"
	case target == "wolf_armor":
		return mat == "armadillo_scute"
	default:
		return false
	}
}

// NextAnvilRepairCost returns the updated prior-work penalty cost (2*cost + 1),
// matching vanilla AnvilMenu.calculateIncreasedRepairCost.
func NextAnvilRepairCost(cost int32) int32 {
	if cost < 0 {
		cost = 0
	}
	return cost*2 + 1
}

// ComputeAnvilResult calculates the output stack, metadata, XP level cost, and
// material consumption for an anvil with inputs left and right, matching
// vanilla AnvilMenu.createResult.
func ComputeAnvilResult(
	left, right world.ItemStack,
	leftMeta, rightMeta ItemMeta,
	renameText string,
	creative bool,
) AnvilResult {
	if left.IsEmpty() {
		return AnvilResult{}
	}

	out := world.NewNamedItemStack(left.ResolvedID(), left.Count)
	if out.IsEmpty() {
		out = left
	}
	outMeta := leftMeta.Clone()
	baseWorkPenalty := leftMeta.RepairCost
	if !right.IsEmpty() {
		baseWorkPenalty += rightMeta.RepairCost
	}

	var opCost int32
	var repairItemCountCost int32
	leftName := left.ResolvedID()
	maxDur := MaxItemDurability(leftName)

	if !right.IsEmpty() {
		if left.Count != 1 {
			return AnvilResult{}
		}
		rightName := right.ResolvedID()
		isBook := rightName == "minecraft:enchanted_book" && rightMeta.HasEnchantments()

		if maxDur > 0 && outMeta.Damage > 0 && IsValidRepairMaterial(leftName, rightName) {
			// Mode 1: Unit material repair (25% max durability per unit).
			step := maxDur / 4
			if step < 1 {
				step = 1
			}
			remDamage := outMeta.Damage
			units := int32(0)
			for remDamage > 0 && units < right.Count {
				remDamage -= step
				if remDamage < 0 {
					remDamage = 0
				}
				opCost++
				units++
			}
			outMeta.Damage = remDamage
			repairItemCountCost = units
		} else {
			// Mode 2: Same-item combination or enchanted book application.
			if !isBook && (leftName != rightName || maxDur <= 0) {
				return AnvilResult{}
			}
			if right.Count != 1 {
				return AnvilResult{}
			}

			if !isBook && maxDur > 0 && outMeta.Damage > 0 {
				remLeft := maxDur - outMeta.Damage
				remRight := maxDur - rightMeta.Damage
				if remRight < 0 {
					remRight = 0
				}
				bonus := maxDur * 12 / 100
				combinedRem := remLeft + remRight + bonus
				newDamage := maxDur - combinedRem
				if newDamage < 0 {
					newDamage = 0
				}
				if newDamage < outMeta.Damage {
					outMeta.Damage = newDamage
					opCost += 2
				}
			}

			rightEnchs := rightMeta.SortedEnchantments()
			anyCompatible := false
			anyIncompatible := false

			for _, rEnch := range rightEnchs {
				def, ok := EnchantmentByID(rEnch.ID)
				if !ok {
					continue
				}
				canApply := CanEnchantItem(def.ID, leftName, true) || creative || leftName == "minecraft:enchanted_book" || leftName == "minecraft:book"
				for _, existing := range outMeta.SortedEnchantments() {
					if existing.ID != def.ID && !AreEnchantmentsCompatible(def.ID, existing.ID) {
						canApply = false
						opCost++
						break
					}
				}
				if !canApply {
					anyIncompatible = true
					continue
				}
				anyCompatible = true
				lLevel := outMeta.EnchantmentLevel(def.ID)
				newLevel := rEnch.Level
				if lLevel == rEnch.Level {
					newLevel = lLevel + 1
					if newLevel > def.MaxLevel {
						newLevel = def.MaxLevel
					}
				} else if lLevel > rEnch.Level {
					newLevel = lLevel
				}
				outMeta.SetEnchantment(def.ID, newLevel)

				fee := int32(def.AnvilCost)
				if isBook {
					fee /= 2
					if fee < 1 {
						fee = 1
					}
				}
				opCost += fee * int32(newLevel)
			}

			if anyIncompatible && !anyCompatible {
				return AnvilResult{}
			}
			if isBook && leftName == "minecraft:book" && outMeta.HasEnchantments() {
				if eb := world.NewNamedItemStack("minecraft:enchanted_book", 1); !eb.IsEmpty() {
					out = eb
				}
			}
		}
	}

	// Handle item renaming.
	trimmedName := sanitizeAnvilName(renameText)
	var renameCost int32
	if trimmedName == "" {
		if leftMeta.CustomName != "" {
			renameCost = 1
			opCost += renameCost
			outMeta.CustomName = ""
		}
	} else if trimmedName != leftMeta.CustomName {
		renameCost = 1
		opCost += renameCost
		outMeta.CustomName = trimmedName
	}

	if opCost <= 0 {
		return AnvilResult{}
	}

	totalCost := baseWorkPenalty + opCost
	if renameCost == opCost && renameCost > 0 && totalCost >= MaxAnvilSurvivalCost {
		// Vanilla caps rename-only operations at 39 levels.
		totalCost = MaxAnvilSurvivalCost - 1
	}

	if totalCost >= MaxAnvilSurvivalCost && !creative {
		return AnvilResult{
			LevelCost:    totalCost,
			TooExpensive: true,
		}
	}

	nextPenalty := leftMeta.RepairCost
	if !right.IsEmpty() && rightMeta.RepairCost > nextPenalty {
		nextPenalty = rightMeta.RepairCost
	}
	if renameCost != opCost || renameCost == 0 {
		nextPenalty = NextAnvilRepairCost(nextPenalty)
	}
	outMeta.RepairCost = nextPenalty

	return AnvilResult{
		Output:              out,
		OutputMeta:          outMeta,
		LevelCost:           totalCost,
		RepairItemCountCost: repairItemCountCost,
	}
}

func sanitizeAnvilName(raw string) string {
	s := strings.TrimSpace(raw)
	if utf8.RuneCountInString(s) > MaxAnvilNameRunes {
		runes := []rune(s)
		s = string(runes[:MaxAnvilNameRunes])
	}
	return s
}

// OpenAnvil opens the Anvil container UI (MenuTypeAnvil = 8) at (x, y, z) for
// session and sends open_screen, container_set_content, and property 0 = 0.
func (s *Server) OpenAnvil(session *PlayerSession, x, y, z int) (int32, error) {
	if s == nil || session == nil {
		return 0, errors.New("server: nil session")
	}
	_ = s.CloseContainer(session, 0, false)

	metaTr := getItemMetaTracker(session)
	metaTr.mu.Lock()
	metaTr.combinerMeta = [2]ItemMeta{}
	metaTr.resultMeta = ItemMeta{}
	metaTr.mu.Unlock()

	tr := getContainerTracker(session)
	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	tr.open = &OpenContainerState{
		ContainerID: cid,
		MenuType:    MenuTypeAnvil,
		BlockX:      x,
		BlockY:      y,
		BlockZ:      z,
	}
	carried := tr.carried
	tr.mu.Unlock()

	if session.send != nil {
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreen(cid, MenuTypeAnvil, "Repair & Name")); err != nil {
			return cid, err
		}
		slots := s.buildCombinerMenuSlots(session, [2]world.ItemStack{}, world.ItemStack{})
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried)); err != nil {
			return cid, err
		}
		if err := session.send(protocol.PlayContainerSetData, EncodeContainerSetData(cid, AnvilDataLevelCost, 0)); err != nil {
			return cid, err
		}
	}
	return cid, nil
}

// DecodeRenameItemPacket parses a protocol 775 serverbound rename_item (0x30) packet body.
func DecodeRenameItemPacket(r *protocol.Reader) (string, error) {
	return r.String()
}

// HandleRenameItem updates the rename text on session's open Anvil container
// and recomputes the output slot and level cost.
func (s *Server) HandleRenameItem(session *PlayerSession, name string) error {
	if s == nil || session == nil || session.IsDead() {
		return nil
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.MenuType != MenuTypeAnvil {
		tr.mu.Unlock()
		return nil
	}
	tr.open.AnvilRename = sanitizeAnvilName(name)
	s.recomputeAnvilStateLocked(session, tr)
	tr.mu.Unlock()
	return s.syncOpenContainer(session)
}

func (s *Server) buildCombinerMenuSlots(session *PlayerSession, inputs [2]world.ItemStack, result world.ItemStack) []world.ItemStack {
	inv := session.Inventory()
	slots := make([]world.ItemStack, AnvilMenuSlotCount)
	slots[0] = inputs[0]
	slots[1] = inputs[1]
	slots[2] = result
	copy(slots[3:30], inv[9:36])
	copy(slots[30:39], inv[36:45])
	return slots
}

func buildCombinerMenuMetas(session *PlayerSession) []ItemMeta {
	metas := make([]ItemMeta, AnvilMenuSlotCount)
	metaTr := getItemMetaTracker(session)
	metaTr.mu.Lock()
	metas[0] = metaTr.combinerMeta[0].Clone()
	metas[1] = metaTr.combinerMeta[1].Clone()
	metas[2] = metaTr.resultMeta.Clone()
	for i := 0; i < 27; i++ {
		metas[3+i] = metaTr.invMeta[9+i].Clone()
	}
	for i := 0; i < 9; i++ {
		metas[30+i] = metaTr.invMeta[36+i].Clone()
	}
	metaTr.mu.Unlock()
	return metas
}

func writeBackCombinerMenuMetas(session *PlayerSession, slots []world.ItemStack, metas []ItemMeta) {
	for i := 0; i < 27; i++ {
		session.SetInventorySlot(9+i, slots[3+i])
	}
	for i := 0; i < 9; i++ {
		session.SetInventorySlot(36+i, slots[30+i])
	}
	metaTr := getItemMetaTracker(session)
	metaTr.mu.Lock()
	for i := 0; i < 2; i++ {
		if slots[i].IsEmpty() {
			metaTr.combinerMeta[i] = ItemMeta{}
		} else {
			metaTr.combinerMeta[i] = metas[i].Clone()
		}
	}
	if slots[2].IsEmpty() {
		metaTr.resultMeta = ItemMeta{}
	} else {
		metaTr.resultMeta = metas[2].Clone()
	}
	for i := 0; i < 27; i++ {
		if slots[3+i].IsEmpty() {
			metaTr.invMeta[9+i] = ItemMeta{}
		} else {
			metaTr.invMeta[9+i] = metas[3+i].Clone()
		}
	}
	for i := 0; i < 9; i++ {
		if slots[30+i].IsEmpty() {
			metaTr.invMeta[36+i] = ItemMeta{}
		} else {
			metaTr.invMeta[36+i] = metas[30+i].Clone()
		}
	}
	metaTr.mu.Unlock()
}

func (s *Server) recomputeAnvilStateLocked(session *PlayerSession, tr *playerContainerTracker) AnvilResult {
	if tr == nil || tr.open == nil || tr.open.MenuType != MenuTypeAnvil {
		return AnvilResult{}
	}
	metaTr := getItemMetaTracker(session)
	metaTr.mu.Lock()
	leftMeta := metaTr.combinerMeta[0].Clone()
	rightMeta := metaTr.combinerMeta[1].Clone()
	metaTr.mu.Unlock()

	creative := session.Snapshot().GameMode == GameModeCreative
	res := ComputeAnvilResult(
		tr.open.CombinerInputs[0],
		tr.open.CombinerInputs[1],
		leftMeta,
		rightMeta,
		tr.open.AnvilRename,
		creative,
	)
	tr.open.Result = res.Output
	tr.open.AnvilCost = int16(res.LevelCost)

	metaTr.mu.Lock()
	metaTr.resultMeta = res.OutputMeta.Clone()
	metaTr.mu.Unlock()
	return res
}

func (s *Server) applyAnvilClickLocked(session *PlayerSession, tr *playerContainerTracker, slot int, button int8, mode int32) (tookResult bool, costPaid int32, anvilBroke bool) {
	if slot < 0 || slot >= AnvilMenuSlotCount || tr.open == nil {
		return false, 0, false
	}
	curRes := s.recomputeAnvilStateLocked(session, tr)
	slots := s.buildCombinerMenuSlots(session, tr.open.CombinerInputs, tr.open.Result)
	metas := buildCombinerMenuMetas(session)

	if slot == AnvilSlotResult {
		if curRes.Output.IsEmpty() || curRes.LevelCost <= 0 {
			return false, 0, false
		}
		creative := session.Snapshot().GameMode == GameModeCreative
		if !creative && (curRes.LevelCost >= MaxAnvilSurvivalCost || session.ExperienceLevel() < curRes.LevelCost) {
			return false, 0, false
		}

		canTake := false
		switch mode {
		case ContainerInputPickup:
			if tr.carried.IsEmpty() {
				tr.carried = curRes.Output
				SetCarriedItemMeta(session, curRes.OutputMeta)
				canTake = true
			}
		case ContainerInputQuickMove:
			rem, remMeta := mergeIntoSlotSliceWithMeta(slots, metas, curRes.Output, curRes.OutputMeta, 30, 39)
			rem, remMeta = mergeIntoSlotSliceWithMeta(slots, metas, rem, remMeta, 3, 30)
			if rem.IsEmpty() {
				_ = remMeta
				canTake = true
			}
		}
		if !canTake {
			return false, 0, false
		}

		// Consume left input and required right input count.
		slots[0] = world.ItemStack{}
		metas[0] = ItemMeta{}
		if !slots[1].IsEmpty() {
			if curRes.RepairItemCountCost > 0 && slots[1].Count > curRes.RepairItemCountCost {
				slots[1] = world.NewItemStack(slots[1].ResolvedItemID(), slots[1].Count-curRes.RepairItemCountCost)
			} else {
				slots[1] = world.ItemStack{}
				metas[1] = ItemMeta{}
			}
		}
		slots[2] = world.ItemStack{}
		metas[2] = ItemMeta{}
		tr.open.AnvilRename = ""
		tookResult = true
		if !creative {
			costPaid = curRes.LevelCost
		}
	} else {
		applyCombinerNonResultClick(session, tr, slots, metas, slot, button, mode)
	}

	tr.open.CombinerInputs[0] = slots[0]
	tr.open.CombinerInputs[1] = slots[1]
	writeBackCombinerMenuMetas(session, slots, metas)
	s.recomputeAnvilStateLocked(session, tr)
	return tookResult, costPaid, anvilBroke
}

func applyCombinerNonResultClick(
	session *PlayerSession,
	tr *playerContainerTracker,
	slots []world.ItemStack,
	metas []ItemMeta,
	slot int,
	button int8,
	mode int32,
) {
	switch mode {
	case ContainerInputPickup:
		carriedMeta := GetCarriedItemMeta(session)
		slots[slot], metas[slot], tr.carried, carriedMeta = applyPickupSlotWithMeta(slots[slot], metas[slot], tr.carried, carriedMeta, button)
		SetCarriedItemMeta(session, carriedMeta)
	case ContainerInputQuickMove:
		if slot < 2 {
			slots[slot], metas[slot] = mergeIntoSlotSliceWithMeta(slots, metas, slots[slot], metas[slot], 30, 39)
			slots[slot], metas[slot] = mergeIntoSlotSliceWithMeta(slots, metas, slots[slot], metas[slot], 3, 30)
		} else if slot >= 3 {
			slots[slot], metas[slot] = mergeIntoSlotSliceWithMeta(slots, metas, slots[slot], metas[slot], 0, 2)
		}
	case ContainerInputSwap:
		if button >= 0 && button < 9 {
			hotbarIdx := 30 + int(button)
			slots[slot], slots[hotbarIdx] = slots[hotbarIdx], slots[slot]
			metas[slot], metas[hotbarIdx] = metas[hotbarIdx], metas[slot]
		}
	}
}

// DamageAnvilBlockOnUse applies the vanilla 12% anvil degradation roll (or an
// explicit rng) at (x, y, z) in session's dimension when in survival mode,
// and emits LevelEventSoundAnvilBroken (1029) or LevelEventSoundAnvilUsed (1030).
// Returns true if the anvil broke into air (and closes the anvil screen).
func (s *Server) DamageAnvilBlockOnUse(session *PlayerSession, x, y, z int, rng *rand.Rand) bool {
	if s == nil || session == nil {
		return false
	}
	dim := world.NormalizeDimension(session.Dimension())
	cx, cz := int32(x>>4), int32(z>>4)
	if session.Snapshot().GameMode == GameModeCreative {
		s.BroadcastDimensionChunk(dim, cx, cz, PlayLevelEventCB,
			EncodeLevelEventPacket(LevelEventSoundAnvilUsed, x, y, z, 0, false))
		return false
	}
	cache := s.PlayerChunks(session)
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	if !IsAnvilBlock(state) {
		s.BroadcastDimensionChunk(dim, cx, cz, PlayLevelEventCB,
			EncodeLevelEventPacket(LevelEventSoundAnvilUsed, x, y, z, 0, false))
		return false
	}
	var roll float64
	if rng != nil {
		roll = rng.Float64()
	} else {
		roll = rand.Float64()
	}
	if roll >= AnvilBreakChance {
		s.BroadcastDimensionChunk(dim, cx, cz, PlayLevelEventCB,
			EncodeLevelEventPacket(LevelEventSoundAnvilUsed, x, y, z, 0, false))
		return false
	}
	nextState, broken := StepAnvilDamageState(state)
	_, res := cache.ApplyBlockWithUpdates(x, y, z, nextState)
	s.broadcastDimensionBlockChanges(dim, cache, res.Changes, res.LightChanged)
	if broken {
		s.BroadcastDimensionChunk(dim, cx, cz, PlayLevelEventCB,
			EncodeLevelEventPacket(LevelEventSoundAnvilBroken, x, y, z, 0, false))
		_ = s.CloseContainer(session, 0, true)
		return true
	}
	s.BroadcastDimensionChunk(dim, cx, cz, PlayLevelEventCB,
		EncodeLevelEventPacket(LevelEventSoundAnvilUsed, x, y, z, 0, false))
	return false
}
