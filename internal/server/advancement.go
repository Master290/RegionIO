package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Advancement protocol 775 (Minecraft 26.1.2) packet IDs.
const (
	PlaySelectAdvancementsTab int32 = 0x55 // clientbound select_advancements_tab
	PlayUpdateAdvancements    int32 = 0x82 // clientbound update_advancements
	PlaySeenAdvancementsSB    int32 = 0x32 // serverbound seen_advancements
)

// ServerboundSeenAdvancementsPacket action enum values.
const (
	SeenAdvancementsOpenedTab    int32 = 0
	SeenAdvancementsClosedScreen int32 = 1
)

// AdvancementType enum ordinals in DisplayInfo.serializeToNetwork.
const (
	AdvancementFrameTask      int32 = 0
	AdvancementFrameChallenge int32 = 1
	AdvancementFrameGoal      int32 = 2
)

// DisplayInfo network bitflags in DisplayInfo.serializeToNetwork.
const (
	AdvancementFlagHasBackground int32 = 1 << 0
	AdvancementFlagShowToast     int32 = 1 << 1
	AdvancementFlagHidden        int32 = 1 << 2
)

// Vanilla advancement tab background textures.
const (
	AdvancementBgStone     = "minecraft:gui/advancements/backgrounds/stone"
	AdvancementBgAdventure = "minecraft:gui/advancements/backgrounds/adventure"
	AdvancementBgHusbandry = "minecraft:gui/advancements/backgrounds/husbandry"
	AdvancementBgNether    = "minecraft:gui/advancements/backgrounds/nether"
)

const advancementTimeFormat = "2006-01-02 15:04:05 -0700"

// AdvancementDisplay describes the client UI display properties of an advancement.
type AdvancementDisplay struct {
	Title          string
	Description    string
	IconItem       string
	Frame          int32
	Background     string
	ShowToast      bool
	AnnounceToChat bool
	Hidden         bool
	X              float32
	Y              float32
}

// AdvancementDef defines one registered advancement in the server tree.
type AdvancementDef struct {
	ID                  string
	ParentID            string
	Display             *AdvancementDisplay
	Criteria            []string
	Requirements        [][]string
	RewardXP            int32
	RewardRecipes       []string
	SendsTelemetryEvent bool
}

// CriterionProgress holds the completion timestamp of a single criterion.
type CriterionProgress struct {
	Obtained time.Time
}

// IsDone reports whether this criterion has been granted.
func (c CriterionProgress) IsDone() bool {
	return !c.Obtained.IsZero()
}

// ObtainedMillis returns the UTC epoch milliseconds when this criterion was
// obtained, or 0 if not yet obtained.
func (c CriterionProgress) ObtainedMillis() int64 {
	if c.Obtained.IsZero() {
		return 0
	}
	return c.Obtained.UnixMilli()
}

// AdvancementProgress tracks per-criterion completion for one advancement.
type AdvancementProgress struct {
	Criteria     map[string]CriterionProgress
	Requirements [][]string
}

// NewAdvancementProgress constructs an initialized AdvancementProgress for def.
func NewAdvancementProgress(def *AdvancementDef) *AdvancementProgress {
	p := &AdvancementProgress{
		Criteria: make(map[string]CriterionProgress),
	}
	if def != nil {
		for _, c := range def.Criteria {
			p.Criteria[c] = CriterionProgress{}
		}
		p.Requirements = cloneRequirements(def.Requirements)
	}
	return p
}

// Clone returns a deep copy of p.
func (p *AdvancementProgress) Clone() *AdvancementProgress {
	if p == nil {
		return nil
	}
	cp := &AdvancementProgress{
		Criteria:     make(map[string]CriterionProgress, len(p.Criteria)),
		Requirements: cloneRequirements(p.Requirements),
	}
	for k, v := range p.Criteria {
		cp.Criteria[k] = v
	}
	return cp
}

// IsDone reports whether all requirement groups are satisfied.
func (p *AdvancementProgress) IsDone() bool {
	if p == nil || len(p.Requirements) == 0 {
		return false
	}
	for _, group := range p.Requirements {
		if len(group) == 0 {
			continue
		}
		groupDone := false
		for _, crit := range group {
			if prog, ok := p.Criteria[crit]; ok && prog.IsDone() {
				groupDone = true
				break
			}
		}
		if !groupDone {
			return false
		}
	}
	return true
}

// HasProgress reports whether at least one criterion has been obtained.
func (p *AdvancementProgress) HasProgress() bool {
	if p == nil {
		return false
	}
	for _, prog := range p.Criteria {
		if prog.IsDone() {
			return true
		}
	}
	return false
}

// CompletedRequirements returns the number of satisfied requirement groups and
// the total number of requirement groups.
func (p *AdvancementProgress) CompletedRequirements() (done, total int) {
	if p == nil {
		return 0, 0
	}
	total = len(p.Requirements)
	for _, group := range p.Requirements {
		for _, crit := range group {
			if prog, ok := p.Criteria[crit]; ok && prog.IsDone() {
				done++
				break
			}
		}
	}
	return done, total
}

// Percent returns the completion ratio in [0.0, 1.0].
func (p *AdvancementProgress) Percent() float32 {
	done, total := p.CompletedRequirements()
	if total <= 0 {
		return 0
	}
	return float32(done) / float32(total)
}

func cloneRequirements(reqs [][]string) [][]string {
	if len(reqs) == 0 {
		return nil
	}
	out := make([][]string, len(reqs))
	for i, g := range reqs {
		cp := make([]string, len(g))
		copy(cp, g)
		out[i] = cp
	}
	return out
}

// AdvancementRegistry holds the ordered tree of vanilla 26.1.2 advancements.
type AdvancementRegistry struct {
	defs     []*AdvancementDef
	byID     map[string]*AdvancementDef
	children map[string][]*AdvancementDef
}

var (
	defaultAdvRegOnce sync.Once
	defaultAdvReg     *AdvancementRegistry
)

// DefaultAdvancementRegistry returns the singleton advancement registry.
func DefaultAdvancementRegistry() *AdvancementRegistry {
	defaultAdvRegOnce.Do(func() {
		defaultAdvReg = buildVanillaAdvancementRegistry()
	})
	return defaultAdvReg
}

// All returns all registered advancements in parent-first topological order.
func (r *AdvancementRegistry) All() []*AdvancementDef {
	if r == nil {
		return nil
	}
	out := make([]*AdvancementDef, len(r.defs))
	copy(out, r.defs)
	return out
}

// ByID looks up an advancement definition by canonical or short ID.
func (r *AdvancementRegistry) ByID(id string) (*AdvancementDef, bool) {
	if r == nil {
		return nil, false
	}
	def, ok := r.byID[canonicalAdvancementID(id)]
	return def, ok
}

// Children returns the direct child advancements of parentID.
func (r *AdvancementRegistry) Children(parentID string) []*AdvancementDef {
	if r == nil {
		return nil
	}
	ch := r.children[canonicalAdvancementID(parentID)]
	out := make([]*AdvancementDef, len(ch))
	copy(out, ch)
	return out
}

// AdvancementByID looks up an advancement in DefaultAdvancementRegistry.
func AdvancementByID(id string) (*AdvancementDef, bool) {
	return DefaultAdvancementRegistry().ByID(id)
}

func canonicalAdvancementID(id string) string {
	id = strings.TrimSpace(strings.ToLower(id))
	if id == "" {
		return ""
	}
	if !strings.Contains(id, ":") {
		return "minecraft:" + id
	}
	return id
}

func allOfCriteria(criteria ...string) ([]string, [][]string) {
	reqs := make([][]string, len(criteria))
	for i, c := range criteria {
		reqs[i] = []string{c}
	}
	return criteria, reqs
}

func anyOfCriteria(criteria ...string) ([]string, [][]string) {
	cp := make([]string, len(criteria))
	copy(cp, criteria)
	return cp, [][]string{cp}
}

func buildVanillaAdvancementRegistry() *AdvancementRegistry {
	var defs []*AdvancementDef
	add := func(
		id, parent, title, desc, icon string,
		frame int32,
		bg string,
		toast, chat, hidden bool,
		x, y float32,
		criteria []string,
		reqs [][]string,
		rewardXP int32,
	) {
		defs = append(defs, &AdvancementDef{
			ID:       id,
			ParentID: parent,
			Display: &AdvancementDisplay{
				Title:          title,
				Description:    desc,
				IconItem:       icon,
				Frame:          frame,
				Background:     bg,
				ShowToast:      toast,
				AnnounceToChat: chat,
				Hidden:         hidden,
				X:              x,
				Y:              y,
			},
			Criteria:            criteria,
			Requirements:        reqs,
			RewardXP:            rewardXP,
			SendsTelemetryEvent: true,
		})
	}

	// --- Story tab (minecraft:story/*) ---
	cRoot, rRoot := allOfCriteria("crafting_table")
	add("minecraft:story/root", "", "Minecraft", "The heart and story of the game",
		"minecraft:grass_block", AdvancementFrameTask, AdvancementBgStone,
		false, false, false, 0, 0, cRoot, rRoot, 0)

	cStone, rStone := allOfCriteria("get_stone")
	add("minecraft:story/mine_stone", "minecraft:story/root", "Stone Age", "Mine stone with your new pickaxe",
		"minecraft:wooden_pickaxe", AdvancementFrameTask, "",
		true, true, false, 1, 0, cStone, rStone, 0)

	cUpgrade, rUpgrade := allOfCriteria("stone_pickaxe")
	add("minecraft:story/upgrade_tools", "minecraft:story/mine_stone", "Getting an Upgrade", "Construct a better pickaxe",
		"minecraft:stone_pickaxe", AdvancementFrameTask, "",
		true, true, false, 2, 0, cUpgrade, rUpgrade, 0)

	cIron, rIron := allOfCriteria("iron")
	add("minecraft:story/smelt_iron", "minecraft:story/upgrade_tools", "Acquire Hardware", "Smelt an iron ingot",
		"minecraft:iron_ingot", AdvancementFrameTask, "",
		true, true, false, 3, 0, cIron, rIron, 0)

	cArmor, rArmor := anyOfCriteria("iron_helmet", "iron_chestplate", "iron_leggings", "iron_boots")
	add("minecraft:story/obtain_armor", "minecraft:story/smelt_iron", "Suit Up", "Protect yourself with a piece of iron armor",
		"minecraft:iron_chestplate", AdvancementFrameTask, "",
		true, true, false, 4, -1, cArmor, rArmor, 0)

	cLava, rLava := allOfCriteria("lava_bucket")
	add("minecraft:story/lava_bucket", "minecraft:story/smelt_iron", "Hot Stuff", "Fill a bucket with lava",
		"minecraft:lava_bucket", AdvancementFrameTask, "",
		true, true, false, 4, 1, cLava, rLava, 0)

	cIronPick, rIronPick := allOfCriteria("iron_pickaxe")
	add("minecraft:story/iron_tools", "minecraft:story/smelt_iron", "Isn't It Iron Pick", "Upgrade your pickaxe",
		"minecraft:iron_pickaxe", AdvancementFrameTask, "",
		true, true, false, 4, 0, cIronPick, rIronPick, 0)

	cDeflect, rDeflect := allOfCriteria("deflected_projectile")
	add("minecraft:story/deflect_arrow", "minecraft:story/obtain_armor", "Not Today, Thank You", "Deflect a projectile with a shield",
		"minecraft:shield", AdvancementFrameTask, "",
		true, true, false, 5, -1, cDeflect, rDeflect, 0)

	cDiamond, rDiamond := allOfCriteria("diamond")
	add("minecraft:story/mine_diamond", "minecraft:story/iron_tools", "Diamonds!", "Acquire diamonds",
		"minecraft:diamond", AdvancementFrameTask, "",
		true, true, false, 5, 0, cDiamond, rDiamond, 0)

	cEnterNether, rEnterNether := allOfCriteria("entered_nether")
	add("minecraft:story/enter_the_nether", "minecraft:story/lava_bucket", "We Need to Go Deeper", "Build, light and enter a Nether Portal",
		"minecraft:flint_and_steel", AdvancementFrameTask, "",
		true, true, false, 5, 1, cEnterNether, rEnterNether, 0)

	cShiny, rShiny := anyOfCriteria("diamond_helmet", "diamond_chestplate", "diamond_leggings", "diamond_boots")
	add("minecraft:story/shiny_gear", "minecraft:story/mine_diamond", "Cover Me with Diamonds", "Diamond armor saves lives",
		"minecraft:diamond_chestplate", AdvancementFrameTask, "",
		true, true, false, 6, -1, cShiny, rShiny, 0)

	cEnchant, rEnchant := allOfCriteria("enchanted_item")
	add("minecraft:story/enchant_item", "minecraft:story/mine_diamond", "Enchanter", "Enchant an item at an Enchantment Table",
		"minecraft:enchanted_book", AdvancementFrameTask, "",
		true, true, false, 6, 0, cEnchant, rEnchant, 0)

	cEye, rEye := allOfCriteria("in_stronghold")
	add("minecraft:story/follow_ender_eye", "minecraft:story/enter_the_nether", "Eye Spy", "Follow an Eye of Ender",
		"minecraft:ender_eye", AdvancementFrameTask, "",
		true, true, false, 6, 1, cEye, rEye, 0)

	cEnd, rEnd := allOfCriteria("entered_end")
	add("minecraft:story/enter_the_end", "minecraft:story/follow_ender_eye", "The End?", "Enter the End Portal",
		"minecraft:end_stone", AdvancementFrameTask, "",
		true, true, false, 7, 1, cEnd, rEnd, 0)

	// --- Adventure tab (minecraft:adventure/*) ---
	cAdvRoot, rAdvRoot := anyOfCriteria("killed_something", "killed_by_something")
	add("minecraft:adventure/root", "", "Adventure", "Adventure, exploration and combat",
		"minecraft:map", AdvancementFrameTask, AdvancementBgAdventure,
		false, false, false, 0, 0, cAdvRoot, rAdvRoot, 0)

	cSleep, rSleep := allOfCriteria("slept_in_bed")
	add("minecraft:adventure/sleep_in_bed", "minecraft:adventure/root", "Sweet Dreams", "Sleep in a bed to change your respawn point",
		"minecraft:red_bed", AdvancementFrameTask, "",
		true, true, false, 1, -1, cSleep, rSleep, 0)

	hostileMobs := []string{
		"minecraft:zombie",
		"minecraft:skeleton",
		"minecraft:spider",
		"minecraft:creeper",
		"minecraft:enderman",
		"minecraft:witch",
		"minecraft:slime",
		"minecraft:blaze",
		"minecraft:ghast",
		"minecraft:wither_skeleton",
	}
	cKillMob, rKillMob := anyOfCriteria(hostileMobs...)
	add("minecraft:adventure/kill_a_mob", "minecraft:adventure/root", "Monster Hunter", "Kill any hostile monster",
		"minecraft:iron_sword", AdvancementFrameTask, "",
		true, true, false, 1, 0, cKillMob, rKillMob, 0)

	cTrade, rTrade := allOfCriteria("traded")
	add("minecraft:adventure/trade", "minecraft:adventure/root", "What a Deal!", "Successfully trade with a Villager",
		"minecraft:emerald", AdvancementFrameTask, "",
		true, true, false, 1, 1, cTrade, rTrade, 0)

	cKillAll, rKillAll := allOfCriteria(hostileMobs...)
	add("minecraft:adventure/kill_all_mobs", "minecraft:adventure/kill_a_mob", "Monsters Hunted", "Kill one of every hostile monster",
		"minecraft:diamond_sword", AdvancementFrameChallenge, "",
		true, true, false, 2, 0, cKillAll, rKillAll, 500)

	// --- Husbandry tab (minecraft:husbandry/*) ---
	cHusbRoot, rHusbRoot := allOfCriteria("consumed_item")
	add("minecraft:husbandry/root", "", "Husbandry", "The world is full of friends and food",
		"minecraft:hay_block", AdvancementFrameTask, AdvancementBgHusbandry,
		false, false, false, 0, 0, cHusbRoot, rHusbRoot, 0)

	cSeed, rSeed := anyOfCriteria("wheat", "pumpkin_stem", "melon_stem", "beetroots", "nether_wart", "carrots", "potatoes", "cocoa", "sweet_berry_bush", "torchflower", "pitcher_crop")
	add("minecraft:husbandry/plant_seed", "minecraft:husbandry/root", "A Seedy Place", "Plant a seed and watch it grow",
		"minecraft:wheat", AdvancementFrameTask, "",
		true, true, false, 1, 0, cSeed, rSeed, 0)

	dietFoods := []string{
		"apple", "golden_apple", "bread", "cooked_porkchop", "cooked_beef",
		"cooked_chicken", "cooked_mutton", "cooked_cod", "cooked_salmon",
		"carrot", "baked_potato", "melon_slice", "cookie", "pumpkin_pie", "rotten_flesh",
	}
	cDiet, rDiet := allOfCriteria(dietFoods...)
	add("minecraft:husbandry/balanced_diet", "minecraft:husbandry/plant_seed", "A Balanced Diet", "Eat everything that is edible, even if it's not good for you",
		"minecraft:apple", AdvancementFrameChallenge, "",
		true, true, false, 2, -1, cDiet, rDiet, 100)

	cNetheriteHoe, rNetheriteHoe := allOfCriteria("netherite_hoe")
	add("minecraft:husbandry/obtain_netherite_hoe", "minecraft:husbandry/plant_seed", "Serious Dedication", "Use a Netherite Ingot to upgrade a Hoe, and then reevaluate your life choices",
		"minecraft:netherite_hoe", AdvancementFrameChallenge, "",
		true, true, false, 2, 1, cNetheriteHoe, rNetheriteHoe, 100)

	cFishy, rFishy := anyOfCriteria("cod", "salmon", "tropical_fish", "pufferfish")
	add("minecraft:husbandry/fishy_business", "minecraft:husbandry/root", "Fishy Business", "Catch a fish",
		"minecraft:fishing_rod", AdvancementFrameTask, "",
		true, true, false, 1, 2, cFishy, rFishy, 0)

	// --- Nether tab (minecraft:nether/*) ---
	cNetherRoot, rNetherRoot := allOfCriteria("entered_nether")
	add("minecraft:nether/root", "", "Nether", "Bring summer clothes",
		"minecraft:red_nether_bricks", AdvancementFrameTask, AdvancementBgNether,
		false, false, false, 0, 0, cNetherRoot, rNetherRoot, 0)

	cDebris, rDebris := allOfCriteria("ancient_debris")
	add("minecraft:nether/obtain_ancient_debris", "minecraft:nether/root", "Hidden in the Depths", "Obtain Ancient Debris",
		"minecraft:ancient_debris", AdvancementFrameTask, "",
		true, true, false, 1, 0, cDebris, rDebris, 0)

	cNetheriteArmor, rNetheriteArmor := allOfCriteria("netherite_helmet", "netherite_chestplate", "netherite_leggings", "netherite_boots")
	add("minecraft:nether/netherite_armor", "minecraft:nether/obtain_ancient_debris", "Cover Me in Debris", "Get a full suit of Netherite armor",
		"minecraft:netherite_chestplate", AdvancementFrameChallenge, "",
		true, true, false, 2, 0, cNetheriteArmor, rNetheriteArmor, 100)

	cBrew, rBrew := allOfCriteria("potion")
	add("minecraft:nether/brew_potion", "minecraft:nether/root", "Local Brewery", "Brew a potion",
		"minecraft:potion", AdvancementFrameTask, "",
		true, true, false, 1, 1, cBrew, rBrew, 0)

	cAllPotions, rAllPotions := allOfCriteria("all_effects")
	add("minecraft:nether/all_potions", "minecraft:nether/brew_potion", "A Furious Cocktail", "Have every potion effect applied at the same time",
		"minecraft:milk_bucket", AdvancementFrameChallenge, "",
		true, true, false, 2, 1, cAllPotions, rAllPotions, 100)

	reg := &AdvancementRegistry{
		defs:     defs,
		byID:     make(map[string]*AdvancementDef, len(defs)),
		children: make(map[string][]*AdvancementDef),
	}
	for _, d := range defs {
		reg.byID[d.ID] = d
		if d.ParentID != "" {
			reg.children[d.ParentID] = append(reg.children[d.ParentID], d)
		}
	}
	return reg
}

type playerAdvancementTracker struct {
	mu          sync.Mutex
	progress    map[string]*AdvancementProgress
	sentDefs    map[string]bool
	selectedTab string
}

var playerAdvancements sync.Map // map[*PlayerSession]*playerAdvancementTracker

func getAdvancementTracker(session *PlayerSession) *playerAdvancementTracker {
	if session == nil {
		return nil
	}
	if v, ok := playerAdvancements.Load(session); ok {
		return v.(*playerAdvancementTracker)
	}
	tr := &playerAdvancementTracker{
		progress: make(map[string]*AdvancementProgress),
		sentDefs: make(map[string]bool),
	}
	for _, def := range DefaultAdvancementRegistry().defs {
		tr.progress[def.ID] = NewAdvancementProgress(def)
	}
	actual, _ := playerAdvancements.LoadOrStore(session, tr)
	return actual.(*playerAdvancementTracker)
}

// AdvancementProgressFor returns a copy of session's progress on advancementID.
func (session *PlayerSession) AdvancementProgressFor(advancementID string) (*AdvancementProgress, bool) {
	if session == nil {
		return nil, false
	}
	id := canonicalAdvancementID(advancementID)
	tr := getAdvancementTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	p, ok := tr.progress[id]
	if !ok {
		return nil, false
	}
	return p.Clone(), true
}

// HasCompletedAdvancement reports whether session has completed advancementID.
func (session *PlayerSession) HasCompletedAdvancement(advancementID string) bool {
	p, ok := session.AdvancementProgressFor(advancementID)
	return ok && p.IsDone()
}

// SelectedAdvancementTab returns the advancement tab identifier last opened by session.
func (session *PlayerSession) SelectedAdvancementTab() string {
	if session == nil {
		return ""
	}
	tr := getAdvancementTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.selectedTab
}

// EncodeUpdateAdvancementsPacket encodes a protocol 775 clientbound
// update_advancements (0x82) packet body matching
// ClientboundUpdateAdvancementsPacket.STREAM_CODEC.
func EncodeUpdateAdvancementsPacket(
	reset bool,
	added []*AdvancementDef,
	removed []string,
	progress map[string]*AdvancementProgress,
	showAdvancements bool,
) []byte {
	w := protocol.NewWriter(256)
	w.Bool(reset)

	// 1. Added List<AdvancementHolder>
	w.VarInt(int32(len(added)))
	for _, def := range added {
		writeAdvancementHolder(w, def)
	}

	// 2. Removed Set<Identifier>
	w.VarInt(int32(len(removed)))
	for _, remID := range removed {
		w.String(canonicalAdvancementID(remID))
	}

	// 3. Progress Map<Identifier, AdvancementProgress> (sorted keys for determinism)
	keys := make([]string, 0, len(progress))
	for k := range progress {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	w.VarInt(int32(len(keys)))
	for _, k := range keys {
		w.String(canonicalAdvancementID(k))
		writeAdvancementProgress(w, progress[k])
	}

	// 4. showAdvancements flag
	w.Bool(showAdvancements)
	return w.Bytes()
}

func writeAdvancementHolder(w *protocol.Writer, def *AdvancementDef) {
	w.String(canonicalAdvancementID(def.ID))
	if def.ParentID != "" {
		w.Bool(true)
		w.String(canonicalAdvancementID(def.ParentID))
	} else {
		w.Bool(false)
	}

	if def.Display != nil {
		w.Bool(true)
		writeAdvancementDisplay(w, def.Display)
	} else {
		w.Bool(false)
	}

	// AdvancementRequirements: List<List<String>>
	w.VarInt(int32(len(def.Requirements)))
	for _, group := range def.Requirements {
		w.VarInt(int32(len(group)))
		for _, crit := range group {
			w.String(crit)
		}
	}

	w.Bool(def.SendsTelemetryEvent)
}

func writeAdvancementDisplay(w *protocol.Writer, d *AdvancementDisplay) {
	// Title & Description as trusted NBT text components.
	w.Raw(nbt.Marshal(nbt.String(d.Title)))
	w.Raw(nbt.Marshal(nbt.String(d.Description)))

	// Icon ItemStackTemplate.STREAM_CODEC:
	// Item (VarInt registry ID, must be non-air >= 1) + count (VarInt) + empty DataComponentPatch (0, 0).
	itemID, ok := world.ItemNameToID(d.IconItem)
	if !ok || itemID <= 0 {
		itemID = 1 // fallback to stone (item ID 1) so ItemStackTemplate never sees air
	}
	w.VarInt(itemID)
	w.VarInt(1)
	w.VarInt(0) // added components
	w.VarInt(0) // removed components

	// AdvancementType enum VarInt (0=TASK, 1=CHALLENGE, 2=GOAL)
	frame := d.Frame
	if frame < 0 || frame > 2 {
		frame = AdvancementFrameTask
	}
	w.VarInt(frame)

	// Flags Int32 (bit 0 = hasBackground, bit 1 = showToast, bit 2 = hidden)
	var flags int32
	if d.Background != "" {
		flags |= AdvancementFlagHasBackground
	}
	if d.ShowToast {
		flags |= AdvancementFlagShowToast
	}
	if d.Hidden {
		flags |= AdvancementFlagHidden
	}
	w.Int32(flags)

	if d.Background != "" {
		w.String(d.Background)
	}
	w.Float32(d.X)
	w.Float32(d.Y)
}

func writeAdvancementProgress(w *protocol.Writer, p *AdvancementProgress) {
	if p == nil || len(p.Criteria) == 0 {
		w.VarInt(0)
		return
	}
	critNames := make([]string, 0, len(p.Criteria))
	for name := range p.Criteria {
		critNames = append(critNames, name)
	}
	sort.Strings(critNames)

	w.VarInt(int32(len(critNames)))
	for _, name := range critNames {
		w.String(name)
		cp := p.Criteria[name]
		if cp.IsDone() {
			w.Bool(true)
			w.Int64(cp.ObtainedMillis())
		} else {
			w.Bool(false)
		}
	}
}

// EncodeSelectAdvancementsTabPacket encodes a clientbound
// select_advancements_tab (0x55) packet body.
func EncodeSelectAdvancementsTabPacket(tab string) []byte {
	w := protocol.NewWriter(32)
	tab = strings.TrimSpace(tab)
	if tab == "" {
		w.Bool(false)
	} else {
		w.Bool(true)
		w.String(canonicalAdvancementID(tab))
	}
	return w.Bytes()
}

// DecodeSeenAdvancementsPacket parses a protocol 775 serverbound
// seen_advancements (0x32) packet body.
func DecodeSeenAdvancementsPacket(r *protocol.Reader) (action int32, tab string, err error) {
	action, err = r.VarInt()
	if err != nil {
		return 0, "", err
	}
	if action == SeenAdvancementsOpenedTab {
		tab, err = r.String()
		if err != nil {
			return action, "", err
		}
	}
	return action, tab, nil
}

// HandleSeenAdvancements processes a serverbound seen_advancements packet.
func (s *Server) HandleSeenAdvancements(session *PlayerSession, action int32, tab string) error {
	if s == nil || session == nil {
		return nil
	}
	tr := getAdvancementTracker(session)
	tr.mu.Lock()
	if action == SeenAdvancementsOpenedTab {
		tr.selectedTab = canonicalAdvancementID(tab)
	}
	selected := tr.selectedTab
	tr.mu.Unlock()

	if action == SeenAdvancementsOpenedTab && session.send != nil {
		return session.send(PlaySelectAdvancementsTab, EncodeSelectAdvancementsTabPacket(selected))
	}
	return nil
}

// SendInitialAdvancements sends a full reset update_advancements packet to
// session containing all registered advancement definitions and current progress.
func (s *Server) SendInitialAdvancements(session *PlayerSession) error {
	if session == nil {
		return errors.New("server: nil player session")
	}
	reg := DefaultAdvancementRegistry()
	tr := getAdvancementTracker(session)
	tr.mu.Lock()
	added := reg.All()
	progMap := make(map[string]*AdvancementProgress, len(tr.progress))
	for _, def := range added {
		tr.sentDefs[def.ID] = true
		if p, ok := tr.progress[def.ID]; ok && p.HasProgress() {
			progMap[def.ID] = p.Clone()
		}
	}
	tr.mu.Unlock()

	if session.send == nil {
		return nil
	}
	pkt := EncodeUpdateAdvancementsPacket(true, added, nil, progMap, false)
	return session.send(PlayUpdateAdvancements, pkt)
}

// GrantCriterion grants a single criterion on advancementID for session.
// Returns true if the criterion was newly granted. When the advancement is
// completed as a result, XP rewards are awarded, a chat announcement is
// broadcast (if configured), and an incremental update_advancements packet is sent.
func (s *Server) GrantCriterion(session *PlayerSession, advancementID, criterion string) bool {
	return s.grantCriterionAt(session, advancementID, criterion, time.Now().UTC())
}

func (s *Server) grantCriterionAt(session *PlayerSession, advancementID, criterion string, when time.Time) bool {
	if session == nil {
		return false
	}
	advID := canonicalAdvancementID(advancementID)
	def, ok := AdvancementByID(advID)
	if !ok {
		return false
	}
	if when.IsZero() {
		when = time.Now().UTC()
	}

	tr := getAdvancementTracker(session)
	tr.mu.Lock()
	prog, exists := tr.progress[advID]
	if !exists {
		prog = NewAdvancementProgress(def)
		tr.progress[advID] = prog
	}
	critProg, validCrit := prog.Criteria[criterion]
	if !validCrit || critProg.IsDone() {
		tr.mu.Unlock()
		return false
	}

	wasDone := prog.IsDone()
	prog.Criteria[criterion] = CriterionProgress{Obtained: when}
	nowDone := prog.IsDone()
	progSnapshot := prog.Clone()

	var newlyAdded []*AdvancementDef
	if !tr.sentDefs[advID] {
		newlyAdded = append(newlyAdded, def)
		tr.sentDefs[advID] = true
	}
	tr.mu.Unlock()

	if session.send != nil {
		updateProg := map[string]*AdvancementProgress{
			advID: progSnapshot,
		}
		pkt := EncodeUpdateAdvancementsPacket(false, newlyAdded, nil, updateProg, nowDone && !wasDone)
		_ = session.send(PlayUpdateAdvancements, pkt)
	}

	if !wasDone && nowDone {
		s.onAdvancementCompleted(session, def)
	}
	return true
}

func (s *Server) onAdvancementCompleted(session *PlayerSession, def *AdvancementDef) {
	if session == nil || def == nil {
		return
	}
	if s != nil && def.RewardXP > 0 {
		_ = s.AddPlayerExperience(session, def.RewardXP)
	}
	if s != nil && def.Display != nil && def.Display.AnnounceToChat {
		msg := FormatAdvancementChatAnnouncement(session.Profile.Name, def)
		if msg != "" {
			s.BroadcastSystemChat(msg)
		}
	}
}

// FormatAdvancementChatAnnouncement formats the vanilla chat message broadcast
// when playerName completes def.
func FormatAdvancementChatAnnouncement(playerName string, def *AdvancementDef) string {
	if def == nil || def.Display == nil || playerName == "" {
		return ""
	}
	switch def.Display.Frame {
	case AdvancementFrameChallenge:
		return fmt.Sprintf("%s has completed the challenge [%s]", playerName, def.Display.Title)
	case AdvancementFrameGoal:
		return fmt.Sprintf("%s has reached the goal [%s]", playerName, def.Display.Title)
	default:
		return fmt.Sprintf("%s has made the advancement [%s]", playerName, def.Display.Title)
	}
}

// GrantAdvancement grants all criteria required to complete advancementID for
// session. Returns true if any criterion was newly granted.
func (s *Server) GrantAdvancement(session *PlayerSession, advancementID string) bool {
	def, ok := AdvancementByID(advancementID)
	if !ok || session == nil {
		return false
	}
	changed := false
	now := time.Now().UTC()
	for _, crit := range def.Criteria {
		if s.grantCriterionAt(session, def.ID, crit, now) {
			changed = true
		}
	}
	return changed
}

// RevokeCriterion revokes a single criterion on advancementID for session.
// Returns true if the criterion had previously been granted.
func (s *Server) RevokeCriterion(session *PlayerSession, advancementID, criterion string) bool {
	if session == nil {
		return false
	}
	advID := canonicalAdvancementID(advancementID)
	tr := getAdvancementTracker(session)
	tr.mu.Lock()
	prog, ok := tr.progress[advID]
	if !ok {
		tr.mu.Unlock()
		return false
	}
	cp, exists := prog.Criteria[criterion]
	if !exists || !cp.IsDone() {
		tr.mu.Unlock()
		return false
	}
	prog.Criteria[criterion] = CriterionProgress{}
	progSnapshot := prog.Clone()
	tr.mu.Unlock()

	if session.send != nil {
		pkt := EncodeUpdateAdvancementsPacket(false, nil, nil, map[string]*AdvancementProgress{
			advID: progSnapshot,
		}, false)
		_ = session.send(PlayUpdateAdvancements, pkt)
	}
	return true
}

// RevokeAdvancement revokes all criteria on advancementID for session.
func (s *Server) RevokeAdvancement(session *PlayerSession, advancementID string) bool {
	def, ok := AdvancementByID(advancementID)
	if !ok || session == nil {
		return false
	}
	changed := false
	for _, crit := range def.Criteria {
		if s.RevokeCriterion(session, def.ID, crit) {
			changed = true
		}
	}
	return changed
}

// GrantAllAdvancements grants every registered advancement to session and
// returns the number of advancements that were newly modified.
func (s *Server) GrantAllAdvancements(session *PlayerSession) int {
	if session == nil {
		return 0
	}
	count := 0
	for _, def := range DefaultAdvancementRegistry().defs {
		if s.GrantAdvancement(session, def.ID) {
			count++
		}
	}
	return count
}

// RevokeAllAdvancements revokes every registered advancement from session and
// returns the number of advancements that were modified.
func (s *Server) RevokeAllAdvancements(session *PlayerSession) int {
	if session == nil {
		return 0
	}
	count := 0
	for _, def := range DefaultAdvancementRegistry().defs {
		if s.RevokeAdvancement(session, def.ID) {
			count++
		}
	}
	return count
}

// --- Gameplay Trigger Hooks ---

// TriggerInventoryChanged evaluates all inventory_changed advancement criteria
// against session's current inventory and cursor item.
func (s *Server) TriggerInventoryChanged(session *PlayerSession) {
	if session == nil {
		return
	}
	inv := session.Inventory()
	present := make(map[string]bool, len(inv)+1)
	for _, st := range inv {
		if !st.IsEmpty() {
			present[st.ResolvedID()] = true
		}
	}
	if carried := session.CarriedItem(); !carried.IsEmpty() {
		present[carried.ResolvedID()] = true
	}
	for id := range present {
		s.triggerItemIDPresent(session, id)
	}
}

// TriggerItemAcquired evaluates inventory_changed advancement criteria for a
// newly acquired item stack.
func (s *Server) TriggerItemAcquired(session *PlayerSession, stack world.ItemStack) {
	if session == nil || stack.IsEmpty() {
		return
	}
	s.triggerItemIDPresent(session, stack.ResolvedID())
}

func (s *Server) triggerItemIDPresent(session *PlayerSession, itemID string) {
	name := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(itemID)), "minecraft:")
	switch name {
	case "crafting_table":
		s.GrantCriterion(session, "minecraft:story/root", "crafting_table")
	case "cobblestone", "cobbled_deepslate", "blackstone":
		s.GrantCriterion(session, "minecraft:story/mine_stone", "get_stone")
	case "stone_pickaxe":
		s.GrantCriterion(session, "minecraft:story/upgrade_tools", "stone_pickaxe")
	case "iron_ingot":
		s.GrantCriterion(session, "minecraft:story/smelt_iron", "iron")
	case "iron_helmet", "iron_chestplate", "iron_leggings", "iron_boots":
		s.GrantCriterion(session, "minecraft:story/obtain_armor", name)
	case "iron_pickaxe":
		s.GrantCriterion(session, "minecraft:story/iron_tools", "iron_pickaxe")
	case "lava_bucket":
		s.GrantCriterion(session, "minecraft:story/lava_bucket", "lava_bucket")
	case "diamond":
		s.GrantCriterion(session, "minecraft:story/mine_diamond", "diamond")
	case "diamond_helmet", "diamond_chestplate", "diamond_leggings", "diamond_boots":
		s.GrantCriterion(session, "minecraft:story/shiny_gear", name)
	case "netherite_hoe":
		s.GrantCriterion(session, "minecraft:husbandry/obtain_netherite_hoe", "netherite_hoe")
	case "ancient_debris":
		s.GrantCriterion(session, "minecraft:nether/obtain_ancient_debris", "ancient_debris")
	case "netherite_helmet", "netherite_chestplate", "netherite_leggings", "netherite_boots":
		s.GrantCriterion(session, "minecraft:nether/netherite_armor", name)
	}
}

// TriggerRecipeCrafted fires advancement criteria when session crafts result.
func (s *Server) TriggerRecipeCrafted(session *PlayerSession, result world.ItemStack) {
	s.TriggerItemAcquired(session, result)
}

// TriggerItemSmelted fires advancement criteria when session takes result from
// a furnace, blast furnace, or smoker.
func (s *Server) TriggerItemSmelted(session *PlayerSession, result world.ItemStack) {
	s.TriggerItemAcquired(session, result)
}

// TriggerEntityKilled fires player_killed_entity advancement criteria when
// session kills an entity of entityType.
func (s *Server) TriggerEntityKilled(session *PlayerSession, entityType string) {
	if session == nil {
		return
	}
	et := canonicalAdvancementID(entityType)
	if et == "" {
		return
	}
	s.GrantCriterion(session, "minecraft:adventure/root", "killed_something")
	s.GrantCriterion(session, "minecraft:adventure/kill_a_mob", et)
	s.GrantCriterion(session, "minecraft:adventure/kill_all_mobs", et)
}

// TriggerKilledByEntity fires entity_killed_player advancement criteria when
// session is killed by an entity.
func (s *Server) TriggerKilledByEntity(session *PlayerSession, entityType string) {
	if session == nil {
		return
	}
	s.GrantCriterion(session, "minecraft:adventure/root", "killed_by_something")
}

// TriggerConsumeItem fires consume_item advancement criteria when session eats
// or consumes item.
func (s *Server) TriggerConsumeItem(session *PlayerSession, item world.ItemStack) {
	if session == nil || item.IsEmpty() {
		return
	}
	name := strings.TrimPrefix(strings.ToLower(item.ResolvedID()), "minecraft:")
	s.GrantCriterion(session, "minecraft:husbandry/root", "consumed_item")
	s.GrantCriterion(session, "minecraft:husbandry/balanced_diet", name)
}

// TriggerChangeDimension fires changed_dimension advancement criteria when
// session travels to toDim.
func (s *Server) TriggerChangeDimension(session *PlayerSession, toDim string) {
	if session == nil {
		return
	}
	switch world.NormalizeDimension(toDim) {
	case world.DimensionNether:
		s.GrantCriterion(session, "minecraft:story/enter_the_nether", "entered_nether")
		s.GrantCriterion(session, "minecraft:nether/root", "entered_nether")
	case world.DimensionEnd:
		s.GrantCriterion(session, "minecraft:story/enter_the_end", "entered_end")
	}
}

// TriggerSleptInBed fires slept_in_bed advancement criteria for session.
func (s *Server) TriggerSleptInBed(session *PlayerSession) {
	if session == nil {
		return
	}
	s.GrantCriterion(session, "minecraft:adventure/sleep_in_bed", "slept_in_bed")
}

// TriggerEnchantedItem fires enchanted_item advancement criteria for session.
func (s *Server) TriggerEnchantedItem(session *PlayerSession) {
	if session == nil {
		return
	}
	s.GrantCriterion(session, "minecraft:story/enchant_item", "enchanted_item")
}

// TriggerVillagerTrade fires villager_trade advancement criteria for session.
func (s *Server) TriggerVillagerTrade(session *PlayerSession) {
	if session == nil {
		return
	}
	s.GrantCriterion(session, "minecraft:adventure/trade", "traded")
}

// TriggerPlantedSeed fires placed_block crop advancement criteria for session.
func (s *Server) TriggerPlantedSeed(session *PlayerSession, blockOrSeedName string) {
	if session == nil {
		return
	}
	name := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(blockOrSeedName)), "minecraft:")
	switch name {
	case "wheat_seeds", "wheat":
		s.GrantCriterion(session, "minecraft:husbandry/plant_seed", "wheat")
	case "pumpkin_seeds", "pumpkin_stem":
		s.GrantCriterion(session, "minecraft:husbandry/plant_seed", "pumpkin_stem")
	case "melon_seeds", "melon_stem":
		s.GrantCriterion(session, "minecraft:husbandry/plant_seed", "melon_stem")
	case "beetroot_seeds", "beetroots":
		s.GrantCriterion(session, "minecraft:husbandry/plant_seed", "beetroots")
	case "nether_wart":
		s.GrantCriterion(session, "minecraft:husbandry/plant_seed", "nether_wart")
	case "carrot", "carrots":
		s.GrantCriterion(session, "minecraft:husbandry/plant_seed", "carrots")
	case "potato", "potatoes":
		s.GrantCriterion(session, "minecraft:husbandry/plant_seed", "potatoes")
	case "cocoa_beans", "cocoa":
		s.GrantCriterion(session, "minecraft:husbandry/plant_seed", "cocoa")
	case "sweet_berries", "sweet_berry_bush":
		s.GrantCriterion(session, "minecraft:husbandry/plant_seed", "sweet_berry_bush")
	case "torchflower_seeds", "torchflower_crop", "torchflower":
		s.GrantCriterion(session, "minecraft:husbandry/plant_seed", "torchflower")
	case "pitcher_pod", "pitcher_crop", "pitcher_plant":
		s.GrantCriterion(session, "minecraft:husbandry/plant_seed", "pitcher_crop")
	}
}

// TriggerDeflectedProjectile fires entity_hurt_player shield deflection criteria.
func (s *Server) TriggerDeflectedProjectile(session *PlayerSession) {
	if session == nil {
		return
	}
	s.GrantCriterion(session, "minecraft:story/deflect_arrow", "deflected_projectile")
}

// TriggerBrewedPotion fires brewed_potion advancement criteria ("Local Brewery")
// when session brews or removes a potion from a brewing stand.
func (s *Server) TriggerBrewedPotion(session *PlayerSession, potionID string) {
	if session == nil {
		return
	}
	s.GrantCriterion(session, "minecraft:nether/brew_potion", "potion")
}

// FuriousCocktailEffects returns the canonical list of 17 potion-brewable status
// effects required simultaneously for "A Furious Cocktail" (minecraft:nether/all_potions).
func FuriousCocktailEffects() []string {
	return []string{
		EffectSpeed,
		EffectSlowness,
		EffectStrength,
		EffectJumpBoost,
		EffectRegeneration,
		EffectFireResistance,
		EffectWaterBreathing,
		EffectInvisibility,
		EffectNightVision,
		EffectWeakness,
		EffectPoison,
		EffectSlowFalling,
		EffectResistance,
		EffectWindCharged,
		EffectWeaving,
		EffectOozing,
		EffectInfested,
	}
}

// TriggerEffectsChanged fires effects_changed advancement criteria (such as
// "A Furious Cocktail") whenever session's active status effects change.
func (s *Server) TriggerEffectsChanged(session *PlayerSession) {
	if session == nil {
		return
	}
	for _, effID := range FuriousCocktailEffects() {
		if !session.HasPlayerEffect(effID) {
			return
		}
	}
	s.GrantCriterion(session, "minecraft:nether/all_potions", "all_effects")
}

// --- Persistence: Player NBT Compound & Vanilla JSON ---

// EncodePlayerAdvancementsNBT serializes session's advancement progress into an
// NBT compound suitable for storing under the "Advancements" tag in playerdata.
func EncodePlayerAdvancementsNBT(session *PlayerSession) *nbt.Compound {
	root := nbt.NewCompound()
	if session == nil {
		return root
	}
	tr := getAdvancementTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()

	if tr.selectedTab != "" {
		root.Set("SelectedTab", nbt.String(tr.selectedTab))
	}

	advComp := nbt.NewCompound()
	keys := make([]string, 0, len(tr.progress))
	for k := range tr.progress {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, id := range keys {
		prog := tr.progress[id]
		if prog == nil || !prog.HasProgress() {
			continue
		}
		entryComp := nbt.NewCompound()
		critComp := nbt.NewCompound()

		cNames := make([]string, 0, len(prog.Criteria))
		for c := range prog.Criteria {
			cNames = append(cNames, c)
		}
		sort.Strings(cNames)
		for _, c := range cNames {
			cp := prog.Criteria[c]
			if cp.IsDone() {
				critComp.Set(c, nbt.Long(cp.ObtainedMillis()))
			}
		}
		entryComp.Set("criteria", critComp)
		if prog.IsDone() {
			entryComp.Set("done", nbt.Byte(1))
		} else {
			entryComp.Set("done", nbt.Byte(0))
		}
		advComp.Set(id, entryComp)
	}
	root.Set("Progress", advComp)
	return root
}

// DecodePlayerAdvancementsNBT restores session's advancement progress from an
// NBT compound previously produced by EncodePlayerAdvancementsNBT.
func DecodePlayerAdvancementsNBT(session *PlayerSession, comp *nbt.Compound) {
	if session == nil || comp == nil {
		return
	}
	tr := getAdvancementTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()

	if tabTag, ok := comp.Get("SelectedTab"); ok {
		if s, ok := tabTag.(nbt.String); ok {
			tr.selectedTab = string(s)
		}
	}

	progTag, ok := comp.Get("Progress")
	if !ok {
		return
	}
	progComp, ok := progTag.(*nbt.Compound)
	if !ok {
		return
	}

	for _, def := range DefaultAdvancementRegistry().defs {
		rawEntry, ok := progComp.Get(def.ID)
		if !ok {
			continue
		}
		entryComp, ok := rawEntry.(*nbt.Compound)
		if !ok {
			continue
		}
		rawCrit, ok := entryComp.Get("criteria")
		if !ok {
			continue
		}
		critComp, ok := rawCrit.(*nbt.Compound)
		if !ok {
			continue
		}
		p := tr.progress[def.ID]
		if p == nil {
			p = NewAdvancementProgress(def)
			tr.progress[def.ID] = p
		}
		for _, cName := range def.Criteria {
			tVal, ok := critComp.Get(cName)
			if !ok {
				continue
			}
			switch v := tVal.(type) {
			case nbt.Long:
				if int64(v) > 0 {
					p.Criteria[cName] = CriterionProgress{Obtained: time.UnixMilli(int64(v)).UTC()}
				}
			case nbt.String:
				if parsed, err := time.Parse(advancementTimeFormat, string(v)); err == nil {
					p.Criteria[cName] = CriterionProgress{Obtained: parsed.UTC()}
				}
			}
		}
	}
}

// SaveAdvancementsToPlayerCompound writes session's advancement state under the
// "Advancements" key of a playerdata root NBT compound.
func SaveAdvancementsToPlayerCompound(session *PlayerSession, root *nbt.Compound) {
	if session == nil || root == nil {
		return
	}
	root.Set("Advancements", EncodePlayerAdvancementsNBT(session))
}

// LoadAdvancementsFromPlayerCompound restores session's advancement state from
// the "Advancements" key of a playerdata root NBT compound, if present.
func LoadAdvancementsFromPlayerCompound(session *PlayerSession, root *nbt.Compound) {
	if session == nil || root == nil {
		return
	}
	if tag, ok := root.Get("Advancements"); ok {
		if comp, ok := tag.(*nbt.Compound); ok {
			DecodePlayerAdvancementsNBT(session, comp)
		}
	}
}

type jsonAdvancementEntry struct {
	Criteria map[string]string `json:"criteria"`
	Done     bool              `json:"done"`
}

// MarshalPlayerAdvancementsJSON serializes session's advancement progress in
// vanilla's <world>/advancements/<uuid>.json format.
func MarshalPlayerAdvancementsJSON(session *PlayerSession) ([]byte, error) {
	if session == nil {
		return nil, errors.New("server: nil player session")
	}
	tr := getAdvancementTracker(session)
	tr.mu.Lock()
	rawMap := make(map[string]any)
	for id, prog := range tr.progress {
		if prog == nil || !prog.HasProgress() {
			continue
		}
		critMap := make(map[string]string)
		for c, cp := range prog.Criteria {
			if cp.IsDone() {
				critMap[c] = cp.Obtained.UTC().Format(advancementTimeFormat)
			}
		}
		rawMap[id] = jsonAdvancementEntry{
			Criteria: critMap,
			Done:     prog.IsDone(),
		}
	}
	rawMap["DataVersion"] = 4788
	tr.mu.Unlock()
	return json.MarshalIndent(rawMap, "", "  ")
}

// UnmarshalPlayerAdvancementsJSON loads session's advancement progress from
// vanilla's <world>/advancements/<uuid>.json format.
func UnmarshalPlayerAdvancementsJSON(session *PlayerSession, data []byte) error {
	if session == nil {
		return errors.New("server: nil player session")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	tr := getAdvancementTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()

	for k, msg := range raw {
		if k == "DataVersion" {
			continue
		}
		advID := canonicalAdvancementID(k)
		def, ok := AdvancementByID(advID)
		if !ok {
			continue
		}
		var entry jsonAdvancementEntry
		if err := json.Unmarshal(msg, &entry); err != nil {
			continue
		}
		p := tr.progress[advID]
		if p == nil {
			p = NewAdvancementProgress(def)
			tr.progress[advID] = p
		}
		for cName, timeStr := range entry.Criteria {
			if _, valid := p.Criteria[cName]; !valid {
				continue
			}
			t, err := time.Parse(advancementTimeFormat, timeStr)
			if err != nil {
				t = time.UnixMilli(1).UTC()
			}
			p.Criteria[cName] = CriterionProgress{Obtained: t.UTC()}
		}
	}
	return nil
}

// SavePlayerAdvancementsFile writes session's advancement progress to
// <WorldDir>/advancements/<uuid>.json when world persistence is enabled.
func (s *Server) SavePlayerAdvancementsFile(session *PlayerSession) error {
	if s == nil || session == nil || s.cfg.WorldDir == "" {
		return nil
	}
	advDir := filepath.Join(s.cfg.WorldDir, "advancements")
	if err := os.MkdirAll(advDir, 0o755); err != nil {
		return err
	}
	data, err := MarshalPlayerAdvancementsJSON(session)
	if err != nil {
		return err
	}
	target := filepath.Join(advDir, world.FormatPlayerUUID(session.Profile.UUID)+".json")
	return os.WriteFile(target, data, 0o644)
}

// LoadPlayerAdvancementsFile loads session's advancement progress from
// <WorldDir>/advancements/<uuid>.json if the file exists on disk.
func (s *Server) LoadPlayerAdvancementsFile(session *PlayerSession) error {
	if s == nil || session == nil || s.cfg.WorldDir == "" {
		return nil
	}
	target := filepath.Join(s.cfg.WorldDir, "advancements", world.FormatPlayerUUID(session.Profile.UUID)+".json")
	data, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return UnmarshalPlayerAdvancementsJSON(session, data)
}
