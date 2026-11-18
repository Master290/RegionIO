package world

import (
	"sort"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
)

// Vanilla 26.1.2 BuiltInRegistries.BLOCK_ENTITY_TYPE numeric IDs (0..48),
// extracted directly from net.minecraft.world.level.block.entity.BlockEntityType.<clinit>.
var blockEntityTypeIDByName = map[string]int32{
	"minecraft:furnace":                 0,
	"minecraft:chest":                   1,
	"minecraft:trapped_chest":           2,
	"minecraft:ender_chest":             3,
	"minecraft:jukebox":                 4,
	"minecraft:dispenser":               5,
	"minecraft:dropper":                 6,
	"minecraft:sign":                    7,
	"minecraft:hanging_sign":            8,
	"minecraft:mob_spawner":             9,
	"minecraft:creaking_heart":          10,
	"minecraft:piston":                  11,
	"minecraft:brewing_stand":           12,
	"minecraft:enchanting_table":        13,
	"minecraft:end_portal":              14,
	"minecraft:beacon":                  15,
	"minecraft:skull":                   16,
	"minecraft:daylight_detector":       17,
	"minecraft:hopper":                  18,
	"minecraft:comparator":              19,
	"minecraft:banner":                  20,
	"minecraft:structure_block":         21,
	"minecraft:end_gateway":             22,
	"minecraft:command_block":           23,
	"minecraft:shulker_box":             24,
	"minecraft:bed":                     25,
	"minecraft:conduit":                 26,
	"minecraft:barrel":                  27,
	"minecraft:smoker":                  28,
	"minecraft:blast_furnace":           29,
	"minecraft:lectern":                 30,
	"minecraft:bell":                    31,
	"minecraft:jigsaw":                  32,
	"minecraft:campfire":                33,
	"minecraft:beehive":                 34,
	"minecraft:sculk_sensor":            35,
	"minecraft:calibrated_sculk_sensor": 36,
	"minecraft:sculk_catalyst":          37,
	"minecraft:sculk_shrieker":          38,
	"minecraft:chiseled_bookshelf":      39,
	"minecraft:shelf":                   40,
	"minecraft:brushable_block":         41,
	"minecraft:decorated_pot":           42,
	"minecraft:crafter":                 43,
	"minecraft:trial_spawner":           44,
	"minecraft:vault":                   45,
	"minecraft:test_block":              46,
	"minecraft:test_instance_block":     47,
	"minecraft:copper_golem_statue":     48,
}

// BlockEntityTypeRegistryID returns the BuiltInRegistries.BLOCK_ENTITY_TYPE
// numeric ID (0..48) for a canonical or short block entity ID in 26.1.2.
func BlockEntityTypeRegistryID(beID string) (int32, bool) {
	id, ok := blockEntityTypeIDByName[normalizeItemName(beID)]
	return id, ok
}

var (
	showcaseStateTableOnce sync.Once
	showcaseBEIDByState    []string // indexed by block state ID; "" if not one of the 9 showcase EntityBlocks
)

func initShowcaseStateTable() {
	showcaseStateTableOnce.Do(func() {
		woodTypes := []string{
			"oak", "spruce", "birch", "acacia", "cherry", "jungle",
			"dark_oak", "pale_oak", "crimson", "warped", "mangrove", "bamboo",
		}
		dyeColors := []string{
			"white", "orange", "magenta", "light_blue", "yellow", "lime", "pink", "gray",
			"light_gray", "cyan", "purple", "blue", "brown", "green", "red", "black",
		}
		byBlockName := map[string]string{
			"minecraft:lectern":            "minecraft:lectern",
			"minecraft:chiseled_bookshelf": "minecraft:chiseled_bookshelf",
			"minecraft:jukebox":            "minecraft:jukebox",
			"minecraft:decorated_pot":      "minecraft:decorated_pot",
			"minecraft:beacon":             "minecraft:beacon",
			"minecraft:conduit":            "minecraft:conduit",
		}
		for _, w := range woodTypes {
			byBlockName["minecraft:"+w+"_sign"] = "minecraft:sign"
			byBlockName["minecraft:"+w+"_wall_sign"] = "minecraft:sign"
			byBlockName["minecraft:"+w+"_hanging_sign"] = "minecraft:hanging_sign"
			byBlockName["minecraft:"+w+"_wall_hanging_sign"] = "minecraft:hanging_sign"
		}
		for _, col := range dyeColors {
			byBlockName["minecraft:"+col+"_banner"] = "minecraft:banner"
			byBlockName["minecraft:"+col+"_wall_banner"] = "minecraft:banner"
		}

		showcaseBEIDByState = make([]string, totalBlockStates)
		for st := 0; st < totalBlockStates; st++ {
			if beID, ok := byBlockName[BlockName(uint16(st))]; ok {
				showcaseBEIDByState[st] = beID
			}
		}
	})
}

func showcaseBlockEntityIDForState(state uint16) string {
	initShowcaseStateTable()
	if int(state) >= len(showcaseBEIDByState) {
		return ""
	}
	return showcaseBEIDByState[state]
}

func defaultSignTextCompoundNBT() *nbt.Compound {
	return nbt.NewCompound().
		Set("messages", nbt.List{
			ElemID: nbt.TagString,
			Elems:  []nbt.Tag{nbt.String(""), nbt.String(""), nbt.String(""), nbt.String("")},
		}).
		Set("color", nbt.String("black")).
		Set("has_glowing_text", nbt.Byte(0))
}

func defaultShowcaseBlockEntitySavedNBT(beID string, x, y, z int) *nbt.Compound {
	comp := nbt.NewCompound().
		Set("id", nbt.String(beID)).
		Set("x", nbt.Int(int32(x))).
		Set("y", nbt.Int(int32(y))).
		Set("z", nbt.Int(int32(z))).
		Set("keepPacked", nbt.Byte(0))
	switch beID {
	case "minecraft:sign", "minecraft:hanging_sign":
		comp.Set("front_text", defaultSignTextCompoundNBT())
		comp.Set("back_text", defaultSignTextCompoundNBT())
		comp.Set("is_waxed", nbt.Byte(0))
	case "minecraft:chiseled_bookshelf":
		comp.Set("Items", nbt.List{ElemID: nbt.TagEnd})
		comp.Set("last_interacted_slot", nbt.Int(-1))
	case "minecraft:beacon":
		comp.Set("Levels", nbt.Int(0))
	}
	return comp
}

func areAllBrickSherds(sherds []string) bool {
	if len(sherds) == 0 {
		return true
	}
	if len(sherds) != 4 {
		return false
	}
	for _, s := range sherds {
		if normalizeItemName(s) != "minecraft:brick" {
			return false
		}
	}
	return true
}

// blockEntityUpdateTagForWire builds the exact 26.1.2 BlockEntity.getUpdateTag
// payload for ClientboundLevelChunkPacketData$BlockEntityInfo:
//   - Returns nil when getUpdateTag().isEmpty() (written on the wire as 0x00 TagEnd).
//   - Never leaks metadata keys ("id", "x", "y", "z", "keepPacked") or non-vanilla keys.
func blockEntityUpdateTagForWire(typeID int32, saved *nbt.Compound) *nbt.Compound {
	switch typeID {
	case 0, 1, 2, 3, 4, 5, 6, 12, 13, 14, 17, 18, 19, 24, 25, 27, 28, 29, 30, 31, 34, 35, 36, 37, 38, 39, 43:
		// Vanilla BlockEntity.getUpdateTag returns new CompoundTag() (empty -> null on wire).
		return nil
	}

	if saved == nil {
		switch typeID {
		case 7, 8:
			return nbt.NewCompound().
				Set("front_text", defaultSignTextCompoundNBT()).
				Set("back_text", defaultSignTextCompoundNBT()).
				Set("is_waxed", nbt.Byte(0))
		case 15:
			return nbt.NewCompound().Set("Levels", nbt.Int(0))
		default:
			return nil
		}
	}

	includeComponents := typeID == 16 || typeID == 20 // skull & banner call saveWithoutMetadata
	out := nbt.NewCompound()
	for _, k := range saved.Keys() {
		switch k {
		case "id", "x", "y", "z", "keepPacked", "dimension", "Dimension", "base_color":
			continue
		case "components":
			if !includeComponents {
				continue
			}
		}
		if v, ok := saved.Get(k); ok {
			out.Set(k, CloneNBTTag(v))
		}
	}
	if typeID == 15 {
		if _, hasLevels := out.Get("Levels"); !hasLevels {
			out.Set("Levels", nbt.Int(0))
		}
	}
	if out.Len() == 0 {
		return nil
	}
	return out
}

type chunkWireBEEntry struct {
	x      int
	y      int
	z      int
	typeID int32
	tag    *nbt.Compound
}

func (c *Chunk) collectWireBlockEntities() []chunkWireBEEntry {
	var entries []chunkWireBEEntry
	seen := make(map[[3]int]int) // pos -> index in entries

	baseX := int(c.X) * 16
	baseZ := int(c.Z) * 16
	inThisChunk := func(x, z int) bool {
		return (x >> 4) == int(c.X) && (z >> 4) == int(c.Z)
	}

	rawByPos := make(map[[3]int]*nbt.Compound, len(c.rawBlockEntities))
	for _, rawBE := range c.rawBlockEntities {
		if rawBE == nil {
			continue
		}
		x := int(nbtAsInt(rawBE, "x"))
		y := int(nbtAsInt(rawBE, "y"))
		z := int(nbtAsInt(rawBE, "z"))
		if !inThisChunk(x, z) {
			continue
		}
		rawByPos[[3]int{x, y, z}] = rawBE
	}

	for _, sbe := range c.blockEntities {
		x, y, z := int(sbe.X), int(sbe.Y), int(sbe.Z)
		if !inThisChunk(x, z) {
			continue
		}
		pos := [3]int{x, y, z}
		if _, dup := seen[pos]; dup {
			continue
		}
		if rawComp, ok := rawByPos[pos]; ok {
			beID := normalizeItemName(string(nbtAsString(rawComp, "id")))
			if typeID, valid := BlockEntityTypeRegistryID(beID); valid {
				seen[pos] = len(entries)
				entries = append(entries, chunkWireBEEntry{
					x:      x,
					y:      y,
					z:      z,
					typeID: typeID,
					tag:    blockEntityUpdateTagForWire(typeID, rawComp),
				})
			}
			continue
		}
		typeID, valid := BlockEntityTypeRegistryID(sbe.ID)
		if !valid {
			continue
		}
		var synth *nbt.Compound
		if sbe.ID == "minecraft:decorated_pot" && !areAllBrickSherds(sbe.Sherds) {
			synth = nbt.NewCompound()
			sh := nbt.List{ElemID: nbt.TagString}
			for _, itemID := range sbe.Sherds {
				sh.Elems = append(sh.Elems, nbt.String(normalizeItemName(itemID)))
			}
			synth.Set("sherds", sh)
		} else if sbe.SpawnerEntityID != "" {
			synth = nbt.NewCompound().Set("SpawnData", nbt.NewCompound().Set("entity", nbt.NewCompound().Set("id", nbt.String(sbe.SpawnerEntityID))))
		}
		seen[pos] = len(entries)
		entries = append(entries, chunkWireBEEntry{
			x:      x,
			y:      y,
			z:      z,
			typeID: typeID,
			tag:    blockEntityUpdateTagForWire(typeID, synth),
		})
	}

	for pos, rawComp := range rawByPos {
		if _, dup := seen[pos]; dup {
			continue
		}
		beID := normalizeItemName(string(nbtAsString(rawComp, "id")))
		// Verify block state still matches when it's a showcase block entity, or trust rawBlockEntities
		// unless replaced with air.
		lx := pos[0] - baseX
		lz := pos[2] - baseZ
		if lx >= 0 && lx < 16 && lz >= 0 && lz < 16 {
			curState := c.GetBlock(lx, pos[1], lz)
			if curState == StateAir {
				continue
			}
			if stateBEID := showcaseBlockEntityIDForState(curState); stateBEID != "" && stateBEID != beID {
				beID = stateBEID
			}
		}
		typeID, valid := BlockEntityTypeRegistryID(beID)
		if !valid {
			continue
		}
		seen[pos] = len(entries)
		entries = append(entries, chunkWireBEEntry{
			x:      pos[0],
			y:      pos[1],
			z:      pos[2],
			typeID: typeID,
			tag:    blockEntityUpdateTagForWire(typeID, rawComp),
		})
	}

	startSec, endSec := c.sectionBounds()
	for secIdx := startSec; secIdx < endSec; secIdx++ {
		sec := c.sections[secIdx]
		if sec == nil {
			continue
		}
		secBaseY := MinY + secIdx*16
		for idx, st := range sec {
			if st == StateAir {
				continue
			}
			beID := showcaseBlockEntityIDForState(st)
			if beID == "" {
				continue
			}
			lx := idx & 0x0F
			lz := (idx >> 4) & 0x0F
			ly := (idx >> 8) & 0x0F
			wx := baseX + lx
			wy := secBaseY + ly
			wz := baseZ + lz
			pos := [3]int{wx, wy, wz}
			if _, dup := seen[pos]; dup {
				continue
			}
			typeID, valid := BlockEntityTypeRegistryID(beID)
			if !valid {
				continue
			}
			seen[pos] = len(entries)
			entries = append(entries, chunkWireBEEntry{
				x:      wx,
				y:      wy,
				z:      wz,
				typeID: typeID,
				tag:    blockEntityUpdateTagForWire(typeID, nil),
			})
		}
	}

	if len(entries) > 1 {
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].y != entries[j].y {
				return entries[i].y < entries[j].y
			}
			if entries[i].z != entries[j].z {
				return entries[i].z < entries[j].z
			}
			return entries[i].x < entries[j].x
		})
	}
	return entries
}

// writeBlockEntities encodes ClientboundLevelChunkPacketData$BlockEntityInfo.LIST_STREAM_CODEC.
func (c *Chunk) writeBlockEntities(w *protocol.Writer) {
	entries := c.collectWireBlockEntities()
	w.VarInt(int32(len(entries)))
	for _, e := range entries {
		packedXZ := byte(((e.x & 15) << 4) | (e.z & 15))
		w.Byte(packedXZ)
		w.Uint16(uint16(int16(e.y)))
		w.VarInt(e.typeID)
		if e.tag == nil || e.tag.Len() == 0 {
			w.Byte(nbt.TagEnd)
		} else {
			w.Raw(nbt.Marshal(e.tag))
		}
	}
}

func (c *Chunk) appendDefaultShowcaseBlockEntitiesToList(emittedPos map[[3]int]bool, list *nbt.List) {
	if c == nil || list == nil {
		return
	}
	baseX := int(c.X) * 16
	baseZ := int(c.Z) * 16
	startSec, endSec := c.sectionBounds()
	for secIdx := startSec; secIdx < endSec; secIdx++ {
		sec := c.sections[secIdx]
		if sec == nil {
			continue
		}
		secBaseY := MinY + secIdx*16
		for idx, st := range sec {
			if st == StateAir {
				continue
			}
			beID := showcaseBlockEntityIDForState(st)
			if beID == "" {
				continue
			}
			lx := idx & 0x0F
			lz := (idx >> 4) & 0x0F
			ly := (idx >> 8) & 0x0F
			pos := [3]int{baseX + lx, secBaseY + ly, baseZ + lz}
			if emittedPos[pos] {
				continue
			}
			emittedPos[pos] = true
			list.Elems = append(list.Elems, defaultShowcaseBlockEntitySavedNBT(beID, pos[0], pos[1], pos[2]))
		}
	}
}

func (c *Chunk) fillMissingDefaultShowcaseBlockEntities(byPos map[[3]int]*nbt.Compound) {
	if c == nil || byPos == nil {
		return
	}
	baseX := int(c.X) * 16
	baseZ := int(c.Z) * 16
	startSec, endSec := c.sectionBounds()
	for secIdx := startSec; secIdx < endSec; secIdx++ {
		sec := c.sections[secIdx]
		if sec == nil {
			continue
		}
		secBaseY := MinY + secIdx*16
		for idx, st := range sec {
			if st == StateAir {
				continue
			}
			beID := showcaseBlockEntityIDForState(st)
			if beID == "" {
				continue
			}
			lx := idx & 0x0F
			lz := (idx >> 4) & 0x0F
			ly := (idx >> 8) & 0x0F
			pos := [3]int{baseX + lx, secBaseY + ly, baseZ + lz}
			if _, exists := byPos[pos]; exists {
				continue
			}
			byPos[pos] = defaultShowcaseBlockEntitySavedNBT(beID, pos[0], pos[1], pos[2])
		}
	}
}

// SyncChunkBlockEntitiesInMemory updates the live in-memory chunk's rawBlockEntities
// matching matchID, invalidates any cached LevelChunkWithLight frame, and marks the
// chunk dirty so subsequent Frame() and SaveAll() calls reflect the update.
func (c *Cache) SyncChunkBlockEntitiesInMemory(cx, cz int32, matchID func(string) bool, list []*nbt.Compound) {
	if c == nil || matchID == nil {
		return
	}
	key := [2]int32{cx, cz}
	c.mu.Lock()
	ch := c.chunks[key]
	c.mu.Unlock()
	if ch == nil {
		return
	}
	ch.ReplaceRawBlockEntities(matchID, list)
	rev := ch.currentRevision()
	c.mu.Lock()
	if c.chunks[key] == ch {
		delete(c.frames, key)
		c.dirty[key] = rev
	}
	c.mu.Unlock()
}
