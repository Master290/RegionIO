package world

import (
	"math"
	"strconv"
	"strings"
	"sync"
)

// NoteBlockRegistryID is the BuiltInRegistries.BLOCK numeric ID for
// "minecraft:note_block" in Minecraft 26.1.2 (protocol 775).
const NoteBlockRegistryID int32 = 109

// JukeboxBlockRegistryID is the BuiltInRegistries.BLOCK numeric ID for
// "minecraft:jukebox" in Minecraft 26.1.2 (protocol 775).
const JukeboxBlockRegistryID int32 = 283

// NoteBlockNoteCount is the number of discrete pitch steps on a note block (0..24).
const NoteBlockNoteCount = 25

// Vanilla 26.1.2 NoteBlockInstrument property values.
const (
	NoteInstrumentHarp             = "harp"
	NoteInstrumentBasedrum         = "basedrum"
	NoteInstrumentSnare            = "snare"
	NoteInstrumentHat              = "hat"
	NoteInstrumentBass             = "bass"
	NoteInstrumentFlute            = "flute"
	NoteInstrumentBell             = "bell"
	NoteInstrumentGuitar           = "guitar"
	NoteInstrumentChime            = "chime"
	NoteInstrumentXylophone        = "xylophone"
	NoteInstrumentIronXylophone    = "iron_xylophone"
	NoteInstrumentCowBell          = "cow_bell"
	NoteInstrumentDidgeridoo       = "didgeridoo"
	NoteInstrumentBit              = "bit"
	NoteInstrumentBanjo            = "banjo"
	NoteInstrumentPling            = "pling"
	NoteInstrumentTrumpet          = "trumpet"
	NoteInstrumentTrumpetExposed   = "trumpet_exposed"
	NoteInstrumentTrumpetOxidized  = "trumpet_oxidized"
	NoteInstrumentTrumpetWeathered = "trumpet_weathered"
	NoteInstrumentZombie           = "zombie"
	NoteInstrumentSkeleton         = "skeleton"
	NoteInstrumentCreeper          = "creeper"
	NoteInstrumentDragon           = "dragon"
	NoteInstrumentWitherSkeleton   = "wither_skeleton"
	NoteInstrumentPiglin           = "piglin"
	NoteInstrumentCustomHead       = "custom_head"
)

// NoteBlockPlayListener is called whenever a note block at (x, y, z) plays a
// note (via right-click tuning, left-click attack, or rising-edge redstone).
type NoteBlockPlayListener func(x, y, z, note int, instrument string)

var noteBlockListeners sync.Map // map[*Cache]NoteBlockPlayListener

// SetNoteBlockPlayListener registers an optional callback invoked when a note
// block at (x, y, z) triggers in c.
func (c *Cache) SetNoteBlockPlayListener(fn NoteBlockPlayListener) {
	if c == nil {
		return
	}
	if fn == nil {
		noteBlockListeners.Delete(c)
		return
	}
	noteBlockListeners.Store(c, fn)
}

func (c *Cache) notifyNoteBlockPlayed(x, y, z, note int, instrument string) {
	if c == nil {
		return
	}
	if v, ok := noteBlockListeners.Load(c); ok && v != nil {
		if fn, ok := v.(NoteBlockPlayListener); ok && fn != nil {
			fn(x, y, z, note, instrument)
		}
	}
}

// IsNoteBlock reports whether state is a minecraft:note_block.
func IsNoteBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:note_block")
}

// IsJukeboxBlock reports whether state is a minecraft:jukebox.
func IsJukeboxBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:jukebox")
}

// IsMobHeadNoteInstrument reports whether instrument is a MOB_HEAD or CUSTOM_HEAD
// instrument placed above a note block (matching NoteBlockInstrument.worksAboveNoteBlock).
func IsMobHeadNoteInstrument(instrument string) bool {
	switch instrument {
	case NoteInstrumentZombie,
		NoteInstrumentSkeleton,
		NoteInstrumentCreeper,
		NoteInstrumentDragon,
		NoteInstrumentWitherSkeleton,
		NoteInstrumentPiglin,
		NoteInstrumentCustomHead:
		return true
	default:
		return false
	}
}

// IsNoteInstrumentTunable reports whether instrument scales pitch by the note
// property (0..24) and spawns a note particle (true for all BASE_BLOCK
// instruments, false for mob heads).
func IsNoteInstrumentTunable(instrument string) bool {
	return !IsMobHeadNoteInstrument(instrument)
}

// NoteBlockInstrumentSound returns the vanilla 26.1.2 SoundEvent identifier
// for a note block instrument property value.
func NoteBlockInstrumentSound(instrument string) string {
	switch instrument {
	case NoteInstrumentBasedrum:
		return "minecraft:block.note_block.basedrum"
	case NoteInstrumentSnare:
		return "minecraft:block.note_block.snare"
	case NoteInstrumentHat:
		return "minecraft:block.note_block.hat"
	case NoteInstrumentBass:
		return "minecraft:block.note_block.bass"
	case NoteInstrumentFlute:
		return "minecraft:block.note_block.flute"
	case NoteInstrumentBell:
		return "minecraft:block.note_block.bell"
	case NoteInstrumentGuitar:
		return "minecraft:block.note_block.guitar"
	case NoteInstrumentChime:
		return "minecraft:block.note_block.chime"
	case NoteInstrumentXylophone:
		return "minecraft:block.note_block.xylophone"
	case NoteInstrumentIronXylophone:
		return "minecraft:block.note_block.iron_xylophone"
	case NoteInstrumentCowBell:
		return "minecraft:block.note_block.cow_bell"
	case NoteInstrumentDidgeridoo:
		return "minecraft:block.note_block.didgeridoo"
	case NoteInstrumentBit:
		return "minecraft:block.note_block.bit"
	case NoteInstrumentBanjo:
		return "minecraft:block.note_block.banjo"
	case NoteInstrumentPling:
		return "minecraft:block.note_block.pling"
	case NoteInstrumentTrumpet:
		return "minecraft:block.note_block.trumpet"
	case NoteInstrumentTrumpetExposed:
		return "minecraft:block.note_block.trumpet_exposed"
	case NoteInstrumentTrumpetOxidized:
		return "minecraft:block.note_block.trumpet_oxidized"
	case NoteInstrumentTrumpetWeathered:
		return "minecraft:block.note_block.trumpet_weathered"
	case NoteInstrumentZombie:
		return "minecraft:block.note_block.imitate.zombie"
	case NoteInstrumentSkeleton:
		return "minecraft:block.note_block.imitate.skeleton"
	case NoteInstrumentCreeper:
		return "minecraft:block.note_block.imitate.creeper"
	case NoteInstrumentDragon:
		return "minecraft:block.note_block.imitate.ender_dragon"
	case NoteInstrumentWitherSkeleton:
		return "minecraft:block.note_block.imitate.wither_skeleton"
	case NoteInstrumentPiglin:
		return "minecraft:block.note_block.imitate.piglin"
	case NoteInstrumentCustomHead:
		return "minecraft:ui.button.click"
	default:
		return "minecraft:block.note_block.harp"
	}
}

// NoteBlockPitch computes the vanilla pitch multiplier 2^((note - 12) / 12)
// for a note value in 0..24 (matching NoteBlock.getPitchFromNote).
func NoteBlockPitch(note int) float32 {
	if note < 0 {
		note = 0
	}
	if note >= NoteBlockNoteCount {
		note = NoteBlockNoteCount - 1
	}
	return float32(math.Pow(2.0, float64(note-12)/12.0))
}

// NoteParticleColor returns the xDist color parameter (note / 24.0) used by
// vanilla ParticleTypes.NOTE when a note block plays.
func NoteParticleColor(note int) float32 {
	if note < 0 {
		note = 0
	}
	if note >= NoteBlockNoteCount {
		note = NoteBlockNoteCount - 1
	}
	return float32(note) / 24.0
}

// NoteInstrumentForBlock returns the NoteBlockInstrument associated with a
// block state in vanilla 26.1.2 (either as a mob head placed on top of a note
// block or as a base block placed beneath a note block).
func NoteInstrumentForBlock(state uint16) string {
	info, ok := stateByID(state)
	if !ok {
		return NoteInstrumentHarp
	}
	return NoteInstrumentForBlockName(info.Name)
}

// NoteInstrumentForBlockName returns the NoteBlockInstrument for a canonical
// block name in vanilla 26.1.2.
func NoteInstrumentForBlockName(name string) string {
	if !strings.HasPrefix(name, "minecraft:") {
		name = "minecraft:" + name
	}
	short := strings.TrimPrefix(name, "minecraft:")

	// 1. Mob heads / skulls (work above note blocks when floor-standing; in
	// vanilla 26.1.2 SkullBlock has MOB_HEAD instruments while WallSkullBlock
	// does not, though we support floor heads and check above in ResolveNoteBlockInstrumentAt).
	switch short {
	case "zombie_head", "zombie_wall_head":
		return NoteInstrumentZombie
	case "skeleton_skull", "skeleton_wall_skull":
		return NoteInstrumentSkeleton
	case "creeper_head", "creeper_wall_head":
		return NoteInstrumentCreeper
	case "dragon_head", "dragon_wall_head":
		return NoteInstrumentDragon
	case "wither_skeleton_skull", "wither_skeleton_wall_skull":
		return NoteInstrumentWitherSkeleton
	case "piglin_head", "piglin_wall_head":
		return NoteInstrumentPiglin
	case "player_head", "player_wall_head":
		return NoteInstrumentCustomHead
	}

	// 2. Special single-block instruments.
	switch short {
	case "gold_block":
		return NoteInstrumentBell
	case "emerald_block":
		return NoteInstrumentBit
	case "packed_ice":
		return NoteInstrumentChime
	case "soul_sand":
		return NoteInstrumentCowBell
	case "pumpkin", "carved_pumpkin", "jack_o_lantern":
		return NoteInstrumentDidgeridoo
	case "clay", "infested_stone", "infested_cobblestone", "infested_stone_bricks",
		"infested_mossy_stone_bricks", "infested_cracked_stone_bricks",
		"infested_chiseled_stone_bricks", "infested_deepslate":
		return NoteInstrumentFlute
	case "iron_block":
		return NoteInstrumentIronXylophone
	case "glowstone":
		return NoteInstrumentPling
	case "hay_block":
		return NoteInstrumentBanjo
	case "bone_block":
		return NoteInstrumentXylophone
	}

	// 3. Copper trumpet instruments (26.1.2).
	if fam, stage, _, ok := ClassifyCopperBlockName(name); ok && fam.Kind != "" {
		switch stage {
		case CopperStageExposed:
			return NoteInstrumentTrumpetExposed
		case CopperStageWeathered:
			return NoteInstrumentTrumpetWeathered
		case CopperStageOxidized:
			return NoteInstrumentTrumpetOxidized
		default:
			return NoteInstrumentTrumpet
		}
	}
	switch short {
	case "copper_block", "chiseled_copper", "cut_copper", "cut_copper_stairs", "cut_copper_slab",
		"waxed_copper_block", "waxed_chiseled_copper", "waxed_cut_copper", "waxed_cut_copper_stairs", "waxed_cut_copper_slab":
		return NoteInstrumentTrumpet
	case "exposed_copper", "exposed_chiseled_copper", "exposed_cut_copper", "exposed_cut_copper_stairs", "exposed_cut_copper_slab",
		"waxed_exposed_copper", "waxed_exposed_chiseled_copper", "waxed_exposed_cut_copper", "waxed_exposed_cut_copper_stairs", "waxed_exposed_cut_copper_slab":
		return NoteInstrumentTrumpetExposed
	case "weathered_copper", "weathered_chiseled_copper", "weathered_cut_copper", "weathered_cut_copper_stairs", "weathered_cut_copper_slab",
		"waxed_weathered_copper", "waxed_weathered_chiseled_copper", "waxed_weathered_cut_copper", "waxed_weathered_cut_copper_stairs", "waxed_weathered_cut_copper_slab":
		return NoteInstrumentTrumpetWeathered
	case "oxidized_copper", "oxidized_chiseled_copper", "oxidized_cut_copper", "oxidized_cut_copper_stairs", "oxidized_cut_copper_slab",
		"waxed_oxidized_copper", "waxed_oxidized_chiseled_copper", "waxed_oxidized_cut_copper", "waxed_oxidized_cut_copper_stairs", "waxed_oxidized_cut_copper_slab":
		return NoteInstrumentTrumpetOxidized
	}

	// 4. Wool -> guitar.
	if strings.HasSuffix(short, "_wool") {
		return NoteInstrumentGuitar
	}

	// 5. Sand / gravel / concrete powder -> snare.
	if short == "sand" || short == "red_sand" || short == "suspicious_sand" ||
		short == "gravel" || short == "suspicious_gravel" || short == "heavy_core" ||
		strings.HasSuffix(short, "_concrete_powder") {
		return NoteInstrumentSnare
	}

	// 6. Glass / sea lantern / beacon / conduit -> hat.
	if short == "glass" || short == "glass_pane" || short == "tinted_glass" ||
		short == "sea_lantern" || short == "beacon" || short == "conduit" ||
		strings.HasSuffix(short, "_stained_glass") || strings.HasSuffix(short, "_stained_glass_pane") {
		return NoteInstrumentHat
	}

	// 7. Wood / logs / planks / hyphae / stems / bamboo / mushroom blocks -> bass.
	if isBassInstrumentBlock(short) {
		return NoteInstrumentBass
	}

	// 8. Stone / bricks / ores / concrete / terracotta / quartz / deepslate / blackstone / netherrack / obsidian -> basedrum.
	if isBasedrumInstrumentBlock(short) {
		return NoteInstrumentBasedrum
	}

	return NoteInstrumentHarp
}

func isBassInstrumentBlock(short string) bool {
	switch short {
	case "note_block", "jukebox", "bookshelf", "chiseled_bookshelf", "chest", "trapped_chest",
		"crafting_table", "Cartography_table", "cartography_table", "fletching_table",
		"smithing_table", "loom", "barrel", "lectern", "composter", "daylight_detector",
		"campfire", "soul_campfire", "bee_nest", "beehive", "mangrove_roots",
		"brown_mushroom_block", "red_mushroom_block", "mushroom_stem",
		"bamboo_mosaic", "bamboo_mosaic_slab", "bamboo_mosaic_stairs", "bamboo_block", "stripped_bamboo_block":
		return true
	}
	for _, suffix := range []string{
		"_planks", "_log", "_wood", "_stem", "_hyphae",
		"_sign", "_wall_sign", "_hanging_sign", "_wall_hanging_sign",
		"_banner", "_wall_banner", "_shelf",
	} {
		if strings.HasSuffix(short, suffix) {
			return true
		}
	}
	for _, wood := range []string{
		"oak_", "spruce_", "birch_", "jungle_", "acacia_",
		"cherry_", "dark_oak_", "pale_oak_", "mangrove_",
		"bamboo_", "crimson_", "warped_",
	} {
		if strings.HasPrefix(short, wood) {
			rest := strings.TrimPrefix(short, wood)
			switch rest {
			case "slab", "stairs", "fence", "fence_gate", "door", "trapdoor", "pressure_plate", "button":
				return true
			}
		}
	}
	return false
}

func isBasedrumInstrumentBlock(short string) bool {
	switch short {
	case "stone", "cobblestone", "mossy_cobblestone", "smooth_stone", "smooth_stone_slab",
		"stone_bricks", "mossy_stone_bricks", "cracked_stone_bricks", "chiseled_stone_bricks",
		"granite", "polished_granite", "diorite", "polished_diorite", "andesite", "polished_andesite",
		"deepslate", "cobbled_deepslate", "polished_deepslate", "deepslate_bricks", "cracked_deepslate_bricks",
		"deepslate_tiles", "cracked_deepslate_tiles", "chiseled_deepslate", "reinforced_deepslate",
		"tuff", "polished_tuff", "tuff_bricks", "chiseled_tuff", "chiseled_tuff_bricks", "calcite", "dripstone_block",
		"pointed_dripstone", "bricks", "mud_bricks", "packed_mud", "resin_bricks", "chiseled_resin_bricks",
		"sandstone", "chiseled_sandstone", "cut_sandstone", "smooth_sandstone",
		"red_sandstone", "chiseled_red_sandstone", "cut_red_sandstone", "smooth_red_sandstone",
		"prismarine", "prismarine_bricks", "dark_prismarine",
		"netherrack", "nether_bricks", "cracked_nether_bricks", "chiseled_nether_bricks", "red_nether_bricks",
		"crimson_nylium", "warped_nylium", "basalt", "smooth_basalt", "polished_basalt",
		"blackstone", "gilded_blackstone", "polished_blackstone", "chiseled_polished_blackstone",
		"polished_blackstone_bricks", "cracked_polished_blackstone_bricks",
		"end_stone", "end_stone_bricks", "purpur_block", "purpur_pillar",
		"quartz_block", "chiseled_quartz_block", "quartz_pillar", "quartz_bricks", "smooth_quartz",
		"obsidian", "crying_obsidian", "respawn_anchor", "magma_block", "ancient_debris", "lodestone",
		"coal_block", "raw_iron_block", "raw_copper_block", "raw_gold_block", "lapis_block",
		"diamond_block", "netherite_block", "amethyst_block", "budding_amethyst",
		"terracotta", "furnace", "blast_furnace", "smoker", "dispenser", "dropper", "observer",
		"piston", "sticky_piston", "crafter", "stonecutter", "grindstone",
		"cauldron", "water_cauldron", "lava_cauldron", "powder_snow_cauldron",
		"hopper", "brewing_stand", "decorated_pot", "anvil", "chipped_anvil", "damaged_anvil",
		"spawner", "trial_spawner", "vault", "enchanting_table", "ender_chest", "end_portal_frame", "bedrock":
		return true
	}
	if strings.HasSuffix(short, "_ore") ||
		strings.HasSuffix(short, "_concrete") ||
		strings.HasSuffix(short, "_terracotta") ||
		strings.HasSuffix(short, "_glazed_terracotta") ||
		strings.HasSuffix(short, "coral_block") {
		return true
	}
	for _, prefix := range []string{
		"stone_", "cobblestone_", "mossy_cobblestone_", "stone_brick_", "mossy_stone_brick_",
		"granite_", "polished_granite_", "diorite_", "polished_diorite_", "andesite_", "polished_andesite_",
		"cobbled_deepslate_", "polished_deepslate_", "deepslate_brick_", "deepslate_tile_",
		"tuff_", "polished_tuff_", "tuff_brick_", "brick_", "mud_brick_", "resin_brick_",
		"sandstone_", "cut_sandstone_", "smooth_sandstone_",
		"red_sandstone_", "cut_red_sandstone_", "smooth_red_sandstone_",
		"prismarine_", "prismarine_brick_", "dark_prismarine_",
		"nether_brick_", "red_nether_brick_", "blackstone_", "polished_blackstone_", "polished_blackstone_brick_",
		"end_stone_brick_", "purpur_", "quartz_", "smooth_quartz_",
	} {
		if strings.HasPrefix(short, prefix) {
			return true
		}
	}
	return false
}

// ResolveNoteBlockInstrumentAt determines the instrument property that a
// minecraft:note_block at (x, y, z) should have based on the block directly
// above (y+1, for mob heads) and the block directly below (y-1, for base blocks),
// matching vanilla NoteBlock.setInstrument.
func (c *Cache) ResolveNoteBlockInstrumentAt(x, y, z int) string {
	if c == nil {
		return NoteInstrumentHarp
	}
	if y+1 < MinY+WorldHeight {
		aboveState := c.GetBlock(x, y+1, z)
		aboveInst := NoteInstrumentForBlock(aboveState)
		if IsMobHeadNoteInstrument(aboveInst) {
			return aboveInst
		}
	}
	if y-1 >= MinY {
		belowState := c.GetBlock(x, y-1, z)
		belowInst := NoteInstrumentForBlock(belowState)
		if !IsMobHeadNoteInstrument(belowInst) {
			return belowInst
		}
	}
	return NoteInstrumentHarp
}

// CanNoteBlockPlayAt reports whether a minecraft:note_block at (x, y, z) can
// emit sound when triggered: true if the block above (y+1) is air OR if the
// note block's instrument is a mob head above it (matching vanilla NoteBlock.playNote).
func (c *Cache) CanNoteBlockPlayAt(x, y, z int) bool {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false
	}
	inst := c.ResolveNoteBlockInstrumentAt(x, y, z)
	if IsMobHeadNoteInstrument(inst) {
		return true
	}
	if y+1 >= MinY+WorldHeight {
		return true
	}
	return isAirState(c.GetBlock(x, y+1, z))
}

// NoteBlockPropertiesAt returns the current (instrument, note, powered, ok)
// properties of a minecraft:note_block at (x, y, z).
func (c *Cache) NoteBlockPropertiesAt(x, y, z int) (instrument string, note int, powered bool, ok bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return "", 0, false, false
	}
	state := c.GetBlock(x, y, z)
	info, valid := stateByID(state)
	if !valid || info.Name != "minecraft:note_block" {
		return "", 0, false, false
	}
	instrument = info.Properties["instrument"]
	if instrument == "" {
		instrument = NoteInstrumentHarp
	}
	note, _ = strconv.Atoi(info.Properties["note"])
	if note < 0 || note >= NoteBlockNoteCount {
		note = 0
	}
	powered = info.Properties["powered"] == "true"
	return instrument, note, powered, true
}

// SyncNoteBlockInstrumentAt updates the "instrument" property of a
// minecraft:note_block at (x, y, z) to match the blocks at (x, y+1, z) and
// (x, y-1, z).
func (c *Cache) SyncNoteBlockInstrumentAt(x, y, z int) (bool, BlockUpdateResult) {
	var res BlockUpdateResult
	changed := c.syncNoteBlockInstrumentRecorded(x, y, z, &res)
	return changed, res
}

func (c *Cache) syncNoteBlockInstrumentRecorded(x, y, z int, res *BlockUpdateResult) bool {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:note_block" {
		return false
	}
	wantInst := c.ResolveNoteBlockInstrumentAt(x, y, z)
	if info.Properties["instrument"] == wantInst {
		return false
	}
	nextState, ok := withStateProperty(state, "instrument", wantInst)
	if !ok {
		return false
	}
	return c.setBlockRecorded(x, y, z, nextState, res)
}

func (c *Cache) syncVerticalNoteBlocksAt(x, y, z int, res *BlockUpdateResult) {
	if c == nil {
		return
	}
	if y+1 < MinY+WorldHeight && IsNoteBlock(c.GetBlock(x, y+1, z)) {
		c.syncNoteBlockInstrumentRecorded(x, y+1, z, res)
	}
	if y-1 >= MinY && IsNoteBlock(c.GetBlock(x, y-1, z)) {
		c.syncNoteBlockInstrumentRecorded(x, y-1, z, res)
	}
}

// PlayNoteBlockAt triggers the minecraft:note_block at (x, y, z) if it is not
// muffled by a non-air block above, returning its note (0..24), instrument, and
// whether the note played.
func (c *Cache) PlayNoteBlockAt(x, y, z int) (note int, instrument string, played bool) {
	inst, n, _, ok := c.NoteBlockPropertiesAt(x, y, z)
	if !ok {
		return 0, "", false
	}
	resolved := c.ResolveNoteBlockInstrumentAt(x, y, z)
	if resolved != "" {
		inst = resolved
	}
	if !c.CanNoteBlockPlayAt(x, y, z) {
		return n, inst, false
	}
	c.notifyNoteBlockPlayed(x, y, z, n, inst)
	return n, inst, true
}

// TuneNoteBlock handles a right-click on a minecraft:note_block at (x, y, z):
// increments its note property modulo 25 (0..24), refreshes its instrument from
// neighboring vertical blocks, and plays the note if unblocked above.
func (c *Cache) TuneNoteBlock(x, y, z int) (note int, instrument string, played bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return 0, "", false, res
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:note_block" {
		return 0, "", false, res
	}
	curNote, _ := strconv.Atoi(info.Properties["note"])
	nextNote := (curNote + 1) % NoteBlockNoteCount
	if nextNote < 0 {
		nextNote = 0
	}
	wantInst := c.ResolveNoteBlockInstrumentAt(x, y, z)
	nextState, ok := nameToStateID("minecraft:note_block", map[string]string{
		"instrument": wantInst,
		"note":       strconv.Itoa(nextNote),
		"powered":    info.Properties["powered"],
	})
	if !ok {
		return curNote, wantInst, false, res
	}
	c.setBlockRecorded(x, y, z, nextState, &res)
	_, _, played = c.PlayNoteBlockAt(x, y, z)
	return nextNote, wantInst, played, res
}

// AttackNoteBlock handles a left-click (attack) on a minecraft:note_block at
// (x, y, z): refreshes its instrument if needed and plays the current note
// without advancing the note counter.
func (c *Cache) AttackNoteBlock(x, y, z int) (note int, instrument string, played bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return 0, "", false, res
	}
	if !IsNoteBlock(c.GetBlock(x, y, z)) {
		return 0, "", false, res
	}
	c.syncNoteBlockInstrumentRecorded(x, y, z, &res)
	note, instrument, played = c.PlayNoteBlockAt(x, y, z)
	return note, instrument, played, res
}

// GetBiomeAt returns the biome network ID at world coordinates (x, y, z),
// loading or generating the chunk if necessary.
func (c *Cache) GetBiomeAt(x, y, z int) uint16 {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return BiomePlains
	}
	cx := int32(x >> 4)
	cz := int32(z >> 4)
	key := [2]int32{cx, cz}
	c.startUse(key)
	defer c.endUse(key)
	ch, err := c.chunkAtErr(cx, cz)
	if err != nil || ch == nil {
		return BiomePlains
	}
	return ch.GetBiome(x, y, z)
}

// SetBiomeAt sets the biome network ID for the 4x4x4 cell containing world
// coordinates (x, y, z).
func (c *Cache) SetBiomeAt(x, y, z int, biome uint16) bool {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false
	}
	cx := int32(x >> 4)
	cz := int32(z >> 4)
	key := [2]int32{cx, cz}
	c.startUse(key)
	defer c.endUse(key)
	ch, err := c.chunkAtErr(cx, cz)
	if err != nil || ch == nil {
		return false
	}
	ch.SetBiome(x, y, z, biome)
	c.mu.Lock()
	delete(c.frames, key)
	if c.store != nil {
		c.dirty[key] = ch.currentRevision()
	}
	c.touch(key)
	c.mu.Unlock()
	return true
}

