package server

import (
	"errors"
	"math/rand"
	"strings"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Grindstone container layout and level event constants (MenuTypeGrindstone = 15).
const (
	GrindstoneSlotTop        = 0
	GrindstoneSlotBottom     = 1
	GrindstoneSlotResult     = 2
	GrindstoneInputSlotCount = 2
	GrindstoneMenuSlotCount  = 39 // 3 grindstone + 27 main inventory + 9 hotbar

	// LevelEventSoundGrindstoneUsed is the vanilla LevelEvent ID (1042) played when
	// an item is disenchanted or combined on a grindstone.
	LevelEventSoundGrindstoneUsed int32 = 1042
)

// GrindstoneResult holds the computed output and XP reward of a grindstone operation.
type GrindstoneResult struct {
	Output     world.ItemStack
	OutputMeta ItemMeta
	MinXP      int32
	MaxXP      int32
	XPAward    int32
}

// IsGrindstoneBlock reports whether state is a minecraft:grindstone block.
func IsGrindstoneBlock(state uint16) bool {
	return world.BlockName(state) == "minecraft:grindstone"
}

// GrindstoneBlockState returns the block state ID for minecraft:grindstone with
// the given face ("floor", "wall", "ceiling") and facing ("north", "south", "west", "east").
func GrindstoneBlockState(face, facing string) uint16 {
	fc := strings.ToLower(strings.TrimSpace(face))
	switch fc {
	case "floor", "wall", "ceiling":
	default:
		fc = "floor"
	}
	fg := strings.ToLower(strings.TrimSpace(facing))
	switch fg {
	case "north", "south", "west", "east":
	default:
		fg = "north"
	}
	if st, ok := world.BlockStateIDByProperties("minecraft:grindstone", map[string]string{
		"face":   fc,
		"facing": fg,
	}); ok {
		return st
	}
	return blockBaseStateByName("minecraft:grindstone")
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
		if topName == "" || topName != botName || topName == "minecraft:enchanted_book" {
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

// GrindstoneItemMinXP returns the sum of minimum enchantment levels
// (EnchantmentDef.MinCost(level)) across all non-curse enchantments on meta.
func GrindstoneItemMinXP(meta ItemMeta) int32 {
	return grindstoneXPFromMeta(meta)
}

// RollGrindstoneXP rolls the experience point award in [res.MinXP, res.MaxXP]
// for taking the output of a Grindstone operation.
func RollGrindstoneXP(res GrindstoneResult, rng *rand.Rand) int32 {
	if res.MinXP <= 0 {
		return 0
	}
	xp := res.MinXP
	if res.MaxXP > res.MinXP {
		span := int(res.MaxXP - res.MinXP + 1)
		if rng != nil {
			xp += int32(rng.Intn(span))
		} else {
			xp += int32(rand.Intn(span))
		}
	}
	return xp
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

// OpenGrindstone opens the Grindstone container UI (MenuTypeGrindstone = 15) at
// (x, y, z) for session and sends open_screen and container_set_content.
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
		dim := world.NormalizeDimension(session.Dimension())
		bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
		s.BroadcastDimensionChunk(dim, int32(bx>>4), int32(bz>>4), PlayLevelEventCB,
			EncodeLevelEventPacket(LevelEventSoundGrindstoneUsed, bx, by, bz, 0, false))
	} else {
		applyCombinerNonResultClick(session, tr, slots, metas, slot, button, mode)
	}

	tr.open.CombinerInputs[0] = slots[0]
	tr.open.CombinerInputs[1] = slots[1]
	writeBackCombinerMenuMetas(session, slots, metas)
	s.recomputeGrindstoneStateLocked(session, tr)
	return tookResult, xpAward
}
