package server

import (
	"errors"
	"math/rand"
	"strings"
	"unicode/utf8"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Serverbound rename_item packet ID in Minecraft 26.1.2 (protocol 775).
const PlayRenameItemSB int32 = 0x30

// Anvil and Grindstone container layout constants.
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

	GrindstoneSlotTop        = 0
	GrindstoneSlotBottom     = 1
	GrindstoneSlotResult     = 2
	GrindstoneInputSlotCount = 2
	GrindstoneMenuSlotCount  = 39 // 3 grindstone + 27 main inventory + 9 hotbar
)

// AnvilResult holds the computed output and cost of an anvil operation.
type AnvilResult struct {
	Output              world.ItemStack
	OutputMeta          ItemMeta
	LevelCost           int32
	RepairItemCountCost int32
	TooExpensive        bool
}

// GrindstoneResult holds the computed output and XP reward of a grindstone operation.
type GrindstoneResult struct {
	Output     world.ItemStack
	OutputMeta ItemMeta
	MinXP      int32
	MaxXP      int32
	XPAward    int32
}

// IsAnvilBlock reports whether state is an anvil, chipped_anvil, or damaged_anvil block.
func IsAnvilBlock(state uint16) bool {
	switch world.BlockName(state) {
	case "minecraft:anvil", "minecraft:chipped_anvil", "minecraft:damaged_anvil":
		return true
	default:
		return false
	}
}

// IsGrindstoneBlock reports whether state is a minecraft:grindstone block.
func IsGrindstoneBlock(state uint16) bool {
	return world.BlockName(state) == "minecraft:grindstone"
}

// StepAnvilDamageState advances an anvil block state by one damage stage
// (anvil -> chipped_anvil -> damaged_anvil -> air), preserving facing offset.
// When the anvil breaks completely, nextState is world.StateAir and broken is true.
func StepAnvilDamageState(state uint16) (nextState uint16, broken bool) {
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
				if left.Count > 1 {
					opCost = MaxAnvilSurvivalCost
				}
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

// ComputeGrindstoneResult calculates the disenchanted/repaired output and
// experience reward for a grindstone with inputs top and bottom, matching
// vanilla GrindstoneMenu.
func ComputeGrindstoneResult(
	top, bottom world.ItemStack,
	topMeta, bottomMeta ItemMeta,
) (GrindstoneResult, bool) {
	topEmpty := top.IsEmpty()
	botEmpty := bottom.IsEmpty()
	if topEmpty && botEmpty {
		return GrindstoneResult{}, false
	}

	if !topEmpty && !botEmpty {
		topName := top.ResolvedID()
		botName := bottom.ResolvedID()
		if topName != botName || topName == "minecraft:enchanted_book" {
			return GrindstoneResult{}, false
		}
		maxDur := MaxItemDurability(topName)
		if maxDur <= 0 || top.Count != 1 || bottom.Count != 1 {
			return GrindstoneResult{}, false
		}

		remTop := maxDur - topMeta.Damage
		remBot := maxDur - bottomMeta.Damage
		if remTop < 0 {
			remTop = 0
		}
		if remBot < 0 {
			remBot = 0
		}
		bonus := maxDur * 5 / 100
		if bonus < 1 {
			bonus = 1
		}
		newDamage := maxDur - (remTop + remBot + bonus)
		if newDamage < 0 {
			newDamage = 0
		}

		outMeta := stripNonCurseEnchantments(topMeta)
		for _, inst := range bottomMeta.SortedEnchantments() {
			if def, ok := EnchantmentByID(inst.ID); ok && def.Curse {
				cur := outMeta.EnchantmentLevel(inst.ID)
				if inst.Level > cur {
					outMeta.SetEnchantment(inst.ID, inst.Level)
				}
			}
		}
		outMeta.Damage = newDamage
		outMeta.RepairCost = 0

		minXP := grindstoneXPFromMeta(topMeta) + grindstoneXPFromMeta(bottomMeta)
		var award int32
		if minXP > 0 {
			award = (minXP + 1) / 2
			if award < 1 {
				award = 1
			}
		}
		return GrindstoneResult{
			Output:     world.NewNamedItemStack(topName, 1),
			OutputMeta: outMeta,
			MinXP:      (minXP + 1) / 2,
			MaxXP:      minXP,
			XPAward:    award,
		}, true
	}

	// Single item disenchanting.
	src := top
	srcMeta := topMeta
	if topEmpty {
		src = bottom
		srcMeta = bottomMeta
	}
	if src.Count != 1 || !hasNonCurseEnchantments(srcMeta) {
		return GrindstoneResult{}, false
	}

	outMeta := stripNonCurseEnchantments(srcMeta)
	outMeta.RepairCost = 0
	outStack := world.NewNamedItemStack(src.ResolvedID(), 1)
	if src.ResolvedID() == "minecraft:enchanted_book" && !outMeta.HasEnchantments() {
		if book := world.NewNamedItemStack("minecraft:book", 1); !book.IsEmpty() {
			outStack = book
		}
	}

	minXP := grindstoneXPFromMeta(srcMeta)
	award := (minXP + 1) / 2
	if award < 1 {
		award = 1
	}
	return GrindstoneResult{
		Output:     outStack,
		OutputMeta: outMeta,
		MinXP:      award,
		MaxXP:      minXP,
		XPAward:    award,
	}, true
}

func hasNonCurseEnchantments(meta ItemMeta) bool {
	for id, lvl := range meta.Enchantments {
		if lvl <= 0 {
			continue
		}
		if def, ok := EnchantmentByID(id); ok && !def.Curse {
			return true
		}
	}
	return false
}

func stripNonCurseEnchantments(meta ItemMeta) ItemMeta {
	out := meta.Clone()
	out.Enchantments = nil
	for id, lvl := range meta.Enchantments {
		if lvl <= 0 {
			continue
		}
		if def, ok := EnchantmentByID(id); ok && def.Curse {
			out.SetEnchantment(def.ID, lvl)
		}
	}
	return out
}

func grindstoneXPFromMeta(meta ItemMeta) int32 {
	var sum int32
	for _, inst := range meta.SortedEnchantments() {
		def, ok := EnchantmentByID(inst.ID)
		if !ok || def.Curse {
			continue
		}
		cost := def.MinCost(inst.Level)
		if cost < 1 {
			cost = 1
		}
		sum += int32(cost)
	}
	return sum
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

// OpenGrindstone opens the Grindstone container UI (MenuTypeGrindstone = 15)
// at (x, y, z) for session.
func (s *Server) OpenGrindstone(session *PlayerSession, x, y, z int) (int32, error) {
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
		MenuType:    MenuTypeGrindstone,
		BlockX:      x,
		BlockY:      y,
		BlockZ:      z,
	}
	carried := tr.carried
	tr.mu.Unlock()

	if session.send != nil {
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreen(cid, MenuTypeGrindstone, "Repair & Disenchant")); err != nil {
			return cid, err
		}
		slots := s.buildCombinerMenuSlots(session, [2]world.ItemStack{}, world.ItemStack{})
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried)); err != nil {
			return cid, err
		}
	}
	return cid, nil
}

// DecodeRenameItemPacket parses a protocol 775 serverbound rename_item (0x30) packet body.
func DecodeRenameItemPacket(r *protocol.Reader) (string, error) {
	return r.String()
}

// HandleRenameItem updates the rename text in session's open Anvil container,
// recomputes the anvil result and level cost, and synchronizes the client.
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

func (s *Server) recomputeGrindstoneStateLocked(session *PlayerSession, tr *playerContainerTracker) (GrindstoneResult, bool) {
	if tr == nil || tr.open == nil || tr.open.MenuType != MenuTypeGrindstone {
		return GrindstoneResult{}, false
	}
	metaTr := getItemMetaTracker(session)
	metaTr.mu.Lock()
	topMeta := metaTr.combinerMeta[0].Clone()
	botMeta := metaTr.combinerMeta[1].Clone()
	metaTr.mu.Unlock()

	res, ok := ComputeGrindstoneResult(
		tr.open.CombinerInputs[0],
		tr.open.CombinerInputs[1],
		topMeta,
		botMeta,
	)
	if !ok {
		tr.open.Result = world.ItemStack{}
		metaTr.mu.Lock()
		metaTr.resultMeta = ItemMeta{}
		metaTr.mu.Unlock()
		return GrindstoneResult{}, false
	}
	tr.open.Result = res.Output
	metaTr.mu.Lock()
	metaTr.resultMeta = res.OutputMeta.Clone()
	metaTr.mu.Unlock()
	return res, true
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
		if !creative && session.ExperienceLevel() < curRes.LevelCost {
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

func (s *Server) applyGrindstoneClickLocked(session *PlayerSession, tr *playerContainerTracker, slot int, button int8, mode int32) (tookResult bool, xpAward int32) {
	if slot < 0 || slot >= GrindstoneMenuSlotCount || tr.open == nil {
		return false, 0
	}
	curRes, ok := s.recomputeGrindstoneStateLocked(session, tr)
	slots := s.buildCombinerMenuSlots(session, tr.open.CombinerInputs, tr.open.Result)
	metas := buildCombinerMenuMetas(session)

	if slot == GrindstoneSlotResult {
		if !ok || curRes.Output.IsEmpty() {
			return false, 0
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
			rem, _ := mergeIntoSlotSliceWithMeta(slots, metas, curRes.Output, curRes.OutputMeta, 30, 39)
			rem, _ = mergeIntoSlotSliceWithMeta(slots, metas, rem, curRes.OutputMeta, 3, 30)
			if rem.IsEmpty() {
				canTake = true
			}
		}
		if !canTake {
			return false, 0
		}

		slots[0] = world.ItemStack{}
		metas[0] = ItemMeta{}
		slots[1] = world.ItemStack{}
		metas[1] = ItemMeta{}
		slots[2] = world.ItemStack{}
		metas[2] = ItemMeta{}
		tookResult = true
		xpAward = curRes.XPAward
	} else {
		applyCombinerNonResultClick(session, tr, slots, metas, slot, button, mode)
	}

	tr.open.CombinerInputs[0] = slots[0]
	tr.open.CombinerInputs[1] = slots[1]
	writeBackCombinerMenuMetas(session, slots, metas)
	s.recomputeGrindstoneStateLocked(session, tr)
	return tookResult, xpAward
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
// explicit rng) at (x, y, z) in session's dimension when in survival mode.
// Returns true if the anvil broke into air (and closes the anvil screen).
func (s *Server) DamageAnvilBlockOnUse(session *PlayerSession, x, y, z int, rng *rand.Rand) bool {
	if s == nil || session == nil {
		return false
	}
	if session.Snapshot().GameMode == GameModeCreative {
		return false
	}
	cache := s.PlayerChunks(session)
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	if !IsAnvilBlock(state) {
		return false
	}
	var roll float64
	if rng != nil {
		roll = rng.Float64()
	} else {
		roll = rand.Float64()
	}
	if roll >= AnvilBreakChance {
		return false
	}
	nextState, broken := StepAnvilDamageState(state)
	cache.SetBlock(x, y, z, nextState)
	if broken {
		_ = s.CloseContainer(session, 0, true)
	}
	return broken
}
