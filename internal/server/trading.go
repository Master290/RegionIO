package server

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"path"
	"sort"
	"strings"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Protocol 775 (Minecraft 26.1.2) packet IDs and menu constants for villager trading.
const (
	// PlayMerchantOffersCB is ClientboundMerchantOffersPacket (0x34 in protocol 775).
	PlayMerchantOffersCB int32 = 0x34
	// PlaySelectTradeSB is ServerboundSelectTradePacket (0x33 in protocol 775).
	PlaySelectTradeSB int32 = 0x33

	// MenuTypeMerchant is BuiltInRegistries.MENU ID for "minecraft:merchant" (19 in protocol 775).
	MenuTypeMerchant int32 = 19

	// MerchantMenu slot indices matching net.minecraft.world.inventory.MerchantMenu.
	MerchantSlotPayment1  = 0
	MerchantSlotPayment2  = 1
	MerchantSlotResult    = 2
	MerchantSlotCount     = 3
	MerchantMenuSlotCount = 39 // 3 merchant slots + 27 main inventory (3..29) + 9 hotbar (30..38)
)

// VillagerLevelXPThresholds matches VillagerData.NEXT_LEVEL_XP_THRESHOLDS in
// Minecraft 26.1.2: cumulative XP required for levels 1..5.
var VillagerLevelXPThresholds = [5]int32{0, 10, 70, 150, 250}

// NextLevelXPThreshold returns the cumulative villager XP required to advance
// from level (1..4) to level+1, or 0 if level is already >= 5 (Master).
func NextLevelXPThreshold(level int) int32 {
	if level < 1 || level >= MaxVillagerLevel {
		return 0
	}
	return VillagerLevelXPThresholds[level]
}

// CanVillagerLevelUp reports whether a villager at level with totalXP has
// reached the threshold to advance to the next level.
func CanVillagerLevelUp(level int, totalXP int32) bool {
	if level < 1 || level >= MaxVillagerLevel {
		return false
	}
	return totalXP >= VillagerLevelXPThresholds[level]
}

// NumberRange represents a parsed vanilla NumberProvider (either a constant
// float64 or a uniform/binomial min..max range).
type NumberRange struct {
	Min float64
	Max float64
}

// UnmarshalJSON parses a vanilla NumberProvider JSON value (number or object).
func (nr *NumberRange) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*nr = NumberRange{}
		return nil
	}
	if trimmed[0] != '{' {
		var v float64
		if err := json.Unmarshal(trimmed, &v); err != nil {
			return err
		}
		nr.Min = v
		nr.Max = v
		return nil
	}
	var obj struct {
		Type string  `json:"type"`
		Min  float64 `json:"min"`
		Max  float64 `json:"max"`
		N    float64 `json:"n"`
		P    float64 `json:"p"`
	}
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return err
	}
	switch canonicalResourceName(obj.Type) {
	case "minecraft:binomial":
		est := math.Max(1, math.Round(obj.N*obj.P))
		nr.Min = est
		nr.Max = math.Max(est, obj.N)
	default:
		nr.Min = obj.Min
		nr.Max = obj.Max
		if nr.Max < nr.Min {
			nr.Max = nr.Min
		}
	}
	return nil
}

// EvalInt samples an integer in [round(Min), round(Max)] using rng (or returns
// round(Min) when rng is nil).
func (nr NumberRange) EvalInt(rng *rand.Rand) int32 {
	minVal := int32(math.Round(nr.Min))
	maxVal := int32(math.Round(nr.Max))
	if maxVal <= minVal || rng == nil {
		return minVal
	}
	return minVal + rng.Int31n(maxVal-minVal+1)
}

// TradeItemSpec represents the "wants", "additional_wants", or "gives" item
// specification inside a 26.1.2 villager_trade JSON definition.
type TradeItemSpec struct {
	ID    string      `json:"id"`
	Count NumberRange `json:"count"`
}

// VillagerTradeEntry represents a single parsed trade definition from
// data/minecraft/villager_trade/<profession>/<level>/<trade>.json.
type VillagerTradeEntry struct {
	Name               string         `json:"-"`
	Wants              TradeItemSpec  `json:"wants"`
	AdditionalWants    *TradeItemSpec `json:"additional_wants,omitempty"`
	Gives              TradeItemSpec  `json:"gives"`
	MaxUses            NumberRange    `json:"max_uses"`
	XP                 NumberRange    `json:"xp"`
	ReputationDiscount float32        `json:"reputation_discount"`
}

// Instantiate creates a concrete MerchantOffer from this datapack trade entry.
func (e *VillagerTradeEntry) Instantiate(rng *rand.Rand) (MerchantOffer, bool) {
	if e == nil || e.Wants.ID == "" || e.Gives.ID == "" {
		return MerchantOffer{}, false
	}
	wantCount := e.Wants.Count.EvalInt(rng)
	if wantCount <= 0 {
		// Enchanted book and dynamic-cost trades specify count 0.0 in JSON and
		// compute emerald price from enchantment level (vanilla range 5..19).
		if rng != nil {
			wantCount = 5 + rng.Int31n(15)
		} else {
			wantCount = 8
		}
	}
	if wantCount > MaxItemStackSize {
		wantCount = MaxItemStackSize
	}
	costA := world.NewNamedItemStack(e.Wants.ID, wantCount)
	if costA.IsEmpty() {
		return MerchantOffer{}, false
	}

	var costB world.ItemStack
	if e.AdditionalWants != nil && e.AdditionalWants.ID != "" {
		bCount := e.AdditionalWants.Count.EvalInt(rng)
		if bCount <= 0 {
			bCount = 1
		}
		if bCount > MaxItemStackSize {
			bCount = MaxItemStackSize
		}
		costB = world.NewNamedItemStack(e.AdditionalWants.ID, bCount)
		if costB.IsEmpty() {
			return MerchantOffer{}, false
		}
	}

	giveCount := e.Gives.Count.EvalInt(rng)
	if giveCount <= 0 {
		giveCount = 1
	}
	if giveCount > MaxItemStackSize {
		giveCount = MaxItemStackSize
	}
	result := world.NewNamedItemStack(e.Gives.ID, giveCount)
	if result.IsEmpty() {
		return MerchantOffer{}, false
	}

	maxUses := e.MaxUses.EvalInt(rng)
	if maxUses <= 0 {
		maxUses = 12
	}
	xp := e.XP.EvalInt(rng)
	if xp < 0 {
		xp = 0
	}
	mult := e.ReputationDiscount
	if mult <= 0 {
		mult = 0.05
	}

	return MerchantOffer{
		Name:            e.Name,
		BaseCostA:       costA,
		CostB:           costB,
		Result:          result,
		Uses:            0,
		MaxUses:         maxUses,
		XP:              xp,
		SpecialPriceDiff: 0,
		PriceMultiplier: mult,
		Demand:          0,
	}, true
}

// TradeSet represents a parsed data/minecraft/trade_set/<profession>/level_<n>.json
// entry linking a villager profession and level to a tag of villager trades.
type TradeSet struct {
	Name           string      `json:"-"`
	Profession     string      `json:"-"`
	Level          int         `json:"-"`
	Amount         NumberRange `json:"amount"`
	Trades         string      `json:"trades"`
	RandomSequence string      `json:"random_sequence"`
}

// MerchantOffer represents a concrete trade offer on a villager, matching
// net.minecraft.world.item.trading.MerchantOffer in protocol 775.
type MerchantOffer struct {
	Name             string
	BaseCostA        world.ItemStack
	CostB            world.ItemStack
	Result           world.ItemStack
	Uses             int32
	MaxUses          int32
	XP               int32
	SpecialPriceDiff int32
	PriceMultiplier  float32
	Demand           int32
}

// IsOutOfStock reports whether this offer has exhausted its allowed uses until restock.
func (o MerchantOffer) IsOutOfStock() bool {
	return o.Uses >= o.MaxUses
}

// AdjustedCostA returns the primary payment ItemStack with demand and special
// price adjustments applied, clamped to [1, MaxItemStackSize] (matching vanilla
// MerchantOffer.getCostA / getModifiedCostCount).
func (o MerchantOffer) AdjustedCostA() world.ItemStack {
	if o.BaseCostA.IsEmpty() {
		return world.ItemStack{}
	}
	base := o.BaseCostA.Count
	demandDiff := int32(math.Floor(float64(base*o.Demand) * float64(o.PriceMultiplier)))
	if demandDiff < 0 {
		demandDiff = 0
	}
	adjusted := base + demandDiff + o.SpecialPriceDiff
	if adjusted < 1 {
		adjusted = 1
	}
	if adjusted > MaxItemStackSize {
		adjusted = MaxItemStackSize
	}
	return world.NewItemStack(o.BaseCostA.ResolvedItemID(), adjusted)
}

// MatchesPayment reports whether payment slot stacks (payA, payB) satisfy this
// offer's adjusted costA and optional costB and the offer is in stock.
func (o MerchantOffer) MatchesPayment(payA, payB world.ItemStack) bool {
	if o.IsOutOfStock() {
		return false
	}
	costA := o.AdjustedCostA()
	if costA.IsEmpty() || payA.IsEmpty() {
		return false
	}
	if payA.ResolvedItemID() != costA.ResolvedItemID() || payA.Count < costA.Count {
		return false
	}
	if !o.CostB.IsEmpty() {
		if payB.IsEmpty() || payB.ResolvedItemID() != o.CostB.ResolvedItemID() || payB.Count < o.CostB.Count {
			return false
		}
	}
	return true
}

// UpdateDemand recalculates demand using vanilla's restock formula:
// demand = demand + uses - (maxUses - uses).
func (o *MerchantOffer) UpdateDemand() {
	if o == nil {
		return
	}
	o.Demand = o.Demand + o.Uses - (o.MaxUses - o.Uses)
}

// ResetUses resets the offer's use counter to 0 (called on restock).
func (o *MerchantOffer) ResetUses() {
	if o == nil {
		return
	}
	o.Uses = 0
}

// TradeBook holds all villager trade entries, trade tags, and trade sets loaded
// from the vanilla 26.1.2 datapack.
type TradeBook struct {
	trades    map[string]*VillagerTradeEntry
	tags      map[string][]string
	tradeSets map[string]*TradeSet // key: "minecraft:<profession>/level_<1..5>"
}

var (
	defaultTradeBookOnce sync.Once
	defaultTradeBook     *TradeBook
)

// DefaultTradeBook returns the singleton TradeBook loaded from the embedded
// vanilla 26.1.2 datapack trade tables.
func DefaultTradeBook() *TradeBook {
	defaultTradeBookOnce.Do(func() {
		// Ensure all item names/IDs are registered from DefaultRecipeBook first.
		_ = DefaultRecipeBook()
		book, err := loadEmbeddedTradeBook()
		if err != nil {
			panic(fmt.Sprintf("server: loading embedded villager trade datapack: %v", err))
		}
		defaultTradeBook = book
	})
	return defaultTradeBook
}

// TradeCount returns the number of individual villager_trade entries in the book.
func (b *TradeBook) TradeCount() int {
	if b == nil {
		return 0
	}
	return len(b.trades)
}

// TradeSetCount returns the number of trade_set definitions in the book.
func (b *TradeBook) TradeSetCount() int {
	if b == nil {
		return 0
	}
	return len(b.tradeSets)
}

// Trade looks up a single datapack trade entry by canonical ID (e.g.
// "minecraft:farmer/1/wheat_emerald").
func (b *TradeBook) Trade(name string) (*VillagerTradeEntry, bool) {
	if b == nil {
		return nil, false
	}
	e, ok := b.trades[canonicalResourceName(name)]
	return e, ok
}

// TradeSetFor returns the TradeSet for a given profession and level (1..5), e.g.
// ("minecraft:farmer", 1) -> "minecraft:farmer/level_1".
func (b *TradeBook) TradeSetFor(profession string, level int) (*TradeSet, bool) {
	if b == nil || level < 1 || level > MaxVillagerLevel {
		return nil, false
	}
	prof := strings.TrimPrefix(canonicalResourceName(profession), "minecraft:")
	if prof == "" || prof == "none" || prof == "nitwit" {
		return nil, false
	}
	key := fmt.Sprintf("minecraft:%s/level_%d", prof, level)
	ts, ok := b.tradeSets[key]
	return ts, ok
}

// TradesForProfessionLevel returns all resolved VillagerTradeEntry definitions
// in the pool for (profession, level).
func (b *TradeBook) TradesForProfessionLevel(profession string, level int) []*VillagerTradeEntry {
	ts, ok := b.TradeSetFor(profession, level)
	if !ok || ts == nil {
		return nil
	}
	tagKey := canonicalResourceName(strings.TrimPrefix(ts.Trades, "#"))
	names := b.tags[tagKey]
	if len(names) == 0 {
		return nil
	}
	out := make([]*VillagerTradeEntry, 0, len(names))
	for _, name := range names {
		if entry, ok := b.trades[name]; ok && entry != nil {
			out = append(out, entry)
		}
	}
	return out
}

// RollOffers selects and instantiates trade offers for (profession, level)
// according to the vanilla 26.1.2 trade_set (typically 2 random offers without
// replacement from the level's villager_trade tag pool).
func (b *TradeBook) RollOffers(profession string, level int, rng *rand.Rand) []MerchantOffer {
	ts, ok := b.TradeSetFor(profession, level)
	if !ok || ts == nil {
		return nil
	}
	pool := b.TradesForProfessionLevel(profession, level)
	if len(pool) == 0 {
		return nil
	}
	amount := int(ts.Amount.EvalInt(rng))
	if amount <= 0 {
		amount = 2
	}
	if amount > len(pool) {
		amount = len(pool)
	}

	indices := make([]int, len(pool))
	for i := range indices {
		indices[i] = i
	}
	if rng != nil && len(indices) > 1 {
		rng.Shuffle(len(indices), func(i, j int) {
			indices[i], indices[j] = indices[j], indices[i]
		})
	}

	offers := make([]MerchantOffer, 0, amount)
	for _, idx := range indices {
		if len(offers) >= amount {
			break
		}
		if offer, ok := pool[idx].Instantiate(rng); ok {
			offers = append(offers, offer)
		}
	}
	return offers
}

// LoadTradeBookFromZip loads villager_trade, tags/villager_trade, and trade_set
// JSON files from a datapack or server jar zip archive.
func LoadTradeBookFromZip(zipBytes []byte) (*TradeBook, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, err
	}
	files := make(map[string]json.RawMessage, 540)
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !strings.HasSuffix(f.Name, ".json") {
			continue
		}
		if !strings.HasPrefix(f.Name, "data/minecraft/villager_trade/") &&
			!strings.HasPrefix(f.Name, "data/minecraft/tags/villager_trade/") &&
			!strings.HasPrefix(f.Name, "data/minecraft/trade_set/") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		files[f.Name] = json.RawMessage(data)
	}
	return LoadTradeBookFromFiles(files)
}

// LoadTradeBookFromFiles parses a map of datapack JSON paths into a TradeBook.
func LoadTradeBookFromFiles(files map[string]json.RawMessage) (*TradeBook, error) {
	book := &TradeBook{
		trades:    make(map[string]*VillagerTradeEntry, 512),
		tags:      make(map[string][]string, 96),
		tradeSets: make(map[string]*TradeSet, 80),
	}
	rawTags := make(map[string][]string, 96)

	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, name := range keys {
		data := files[name]
		switch {
		case strings.HasPrefix(name, "data/minecraft/villager_trade/"):
			rel := strings.TrimPrefix(name, "data/minecraft/villager_trade/")
			id := "minecraft:" + strings.TrimSuffix(path.Clean(rel), ".json")
			var entry VillagerTradeEntry
			if err := json.Unmarshal(data, &entry); err != nil {
				return nil, fmt.Errorf("parsing villager_trade %s: %w", name, err)
			}
			entry.Name = canonicalResourceName(id)
			entry.Wants.ID = canonicalResourceName(entry.Wants.ID)
			if entry.AdditionalWants != nil && entry.AdditionalWants.ID != "" {
				entry.AdditionalWants.ID = canonicalResourceName(entry.AdditionalWants.ID)
			}
			entry.Gives.ID = canonicalResourceName(entry.Gives.ID)
			book.trades[entry.Name] = &entry

		case strings.HasPrefix(name, "data/minecraft/tags/villager_trade/"):
			rel := strings.TrimPrefix(name, "data/minecraft/tags/villager_trade/")
			tagID := "minecraft:" + strings.TrimSuffix(path.Clean(rel), ".json")
			var tf rawTagFile
			if err := json.Unmarshal(data, &tf); err != nil {
				return nil, fmt.Errorf("parsing villager_trade tag %s: %w", name, err)
			}
			rawTags[canonicalResourceName(tagID)] = tf.Values

		case strings.HasPrefix(name, "data/minecraft/trade_set/"):
			rel := strings.TrimPrefix(name, "data/minecraft/trade_set/")
			cleanRel := strings.TrimSuffix(path.Clean(rel), ".json")
			setID := "minecraft:" + cleanRel
			var ts TradeSet
			if err := json.Unmarshal(data, &ts); err != nil {
				return nil, fmt.Errorf("parsing trade_set %s: %w", name, err)
			}
			ts.Name = canonicalResourceName(setID)
			if slash := strings.IndexByte(cleanRel, '/'); slash > 0 {
				ts.Profession = "minecraft:" + cleanRel[:slash]
				lvlStr := strings.TrimPrefix(cleanRel[slash+1:], "level_")
				if len(lvlStr) == 1 && lvlStr[0] >= '1' && lvlStr[0] <= '5' {
					ts.Level = int(lvlStr[0] - '0')
				}
			}
			book.tradeSets[ts.Name] = &ts
		}
	}

	book.tags = resolveAllItemTags(rawTags)
	return book, nil
}

func loadEmbeddedTradeBook() (*TradeBook, error) {
	cleaned := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, embeddedVillagerTradesGzipBase64)
	gzBytes, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	gr, err := gzip.NewReader(bytes.NewReader(gzBytes))
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()
	rawJSON, err := io.ReadAll(gr)
	if err != nil {
		return nil, fmt.Errorf("gzip read: %w", err)
	}
	var files map[string]json.RawMessage
	if err := json.Unmarshal(rawJSON, &files); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}
	return LoadTradeBookFromFiles(files)
}

// MerchantOffersPacket holds the decoded fields of a protocol 775
// ClientboundMerchantOffersPacket (0x34).
type MerchantOffersPacket struct {
	ContainerID   int32
	Offers        []MerchantOffer
	VillagerLevel int32
	VillagerXP    int32
	ShowProgress  bool
	CanRestock    bool
}

// EncodeMerchantOffersPacket encodes a protocol 775 ClientboundMerchantOffersPacket (0x34).
func EncodeMerchantOffersPacket(
	containerID int32,
	offers []MerchantOffer,
	villagerLevel int32,
	villagerXP int32,
	showProgress bool,
	canRestock bool,
) []byte {
	w := protocol.NewWriter(32 + len(offers)*36)
	w.VarInt(containerID)
	w.VarInt(int32(len(offers)))
	for _, off := range offers {
		writeMerchantItemCost(w, off.BaseCostA)
		writeContainerItemStack(w, off.Result)
		if !off.CostB.IsEmpty() && off.CostB.ResolvedItemID() > 0 {
			w.Bool(true)
			writeMerchantItemCost(w, off.CostB)
		} else {
			w.Bool(false)
		}
		w.Bool(off.IsOutOfStock())
		w.Int32(off.Uses)
		w.Int32(off.MaxUses)
		w.Int32(off.XP)
		w.Int32(off.SpecialPriceDiff)
		w.Float32(off.PriceMultiplier)
		w.Int32(off.Demand)
	}
	w.VarInt(villagerLevel)
	w.VarInt(villagerXP)
	w.Bool(showProgress)
	w.Bool(canRestock)
	return w.Bytes()
}

func writeMerchantItemCost(w *protocol.Writer, stack world.ItemStack) {
	itemID := stack.ResolvedItemID()
	count := stack.Count
	if count <= 0 {
		count = 1
	}
	w.VarInt(itemID)
	w.VarInt(count)
	w.VarInt(0) // empty DataComponentExactPredicate
}

// DecodeMerchantOffersPacket decodes a protocol 775 ClientboundMerchantOffersPacket (0x34).
func DecodeMerchantOffersPacket(r *protocol.Reader) (MerchantOffersPacket, error) {
	var pkt MerchantOffersPacket
	cid, err := r.VarInt()
	if err != nil {
		return pkt, err
	}
	pkt.ContainerID = cid
	count, err := r.VarInt()
	if err != nil {
		return pkt, err
	}
	if count < 0 || count > 256 {
		return pkt, fmt.Errorf("invalid merchant offers count %d", count)
	}
	pkt.Offers = make([]MerchantOffer, int(count))
	for i := 0; i < int(count); i++ {
		costA, err := readMerchantItemCost(r)
		if err != nil {
			return pkt, err
		}
		result, err := readContainerItemStack(r)
		if err != nil {
			return pkt, err
		}
		hasCostB, err := r.Bool()
		if err != nil {
			return pkt, err
		}
		var costB world.ItemStack
		if hasCostB {
			costB, err = readMerchantItemCost(r)
			if err != nil {
				return pkt, err
			}
		}
		outOfStock, err := r.Bool()
		if err != nil {
			return pkt, err
		}
		uses, err := r.Int32()
		if err != nil {
			return pkt, err
		}
		maxUses, err := r.Int32()
		if err != nil {
			return pkt, err
		}
		if outOfStock && uses < maxUses {
			uses = maxUses
		}
		xp, err := r.Int32()
		if err != nil {
			return pkt, err
		}
		specialDiff, err := r.Int32()
		if err != nil {
			return pkt, err
		}
		priceMult, err := r.Float32()
		if err != nil {
			return pkt, err
		}
		demand, err := r.Int32()
		if err != nil {
			return pkt, err
		}
		pkt.Offers[i] = MerchantOffer{
			BaseCostA:        costA,
			CostB:            costB,
			Result:           result,
			Uses:             uses,
			MaxUses:          maxUses,
			XP:               xp,
			SpecialPriceDiff: specialDiff,
			PriceMultiplier:  priceMult,
			Demand:           demand,
		}
	}
	lvl, err := r.VarInt()
	if err != nil {
		return pkt, err
	}
	xp, err := r.VarInt()
	if err != nil {
		return pkt, err
	}
	showProg, err := r.Bool()
	if err != nil {
		return pkt, err
	}
	canRestock, err := r.Bool()
	if err != nil {
		return pkt, err
	}
	pkt.VillagerLevel = lvl
	pkt.VillagerXP = xp
	pkt.ShowProgress = showProg
	pkt.CanRestock = canRestock
	return pkt, nil
}

func readMerchantItemCost(r *protocol.Reader) (world.ItemStack, error) {
	itemID, err := r.VarInt()
	if err != nil {
		return world.ItemStack{}, err
	}
	count, err := r.VarInt()
	if err != nil {
		return world.ItemStack{}, err
	}
	compCount, err := r.VarInt()
	if err != nil {
		return world.ItemStack{}, err
	}
	if compCount != 0 {
		return world.ItemStack{}, fmt.Errorf("unsupported non-empty ItemCost component predicate (%d)", compCount)
	}
	return world.NewItemStack(itemID, count), nil
}

func readContainerItemStack(r *protocol.Reader) (world.ItemStack, error) {
	count, err := r.VarInt()
	if err != nil {
		return world.ItemStack{}, err
	}
	if count <= 0 {
		return world.ItemStack{}, nil
	}
	itemID, err := r.VarInt()
	if err != nil {
		return world.ItemStack{}, err
	}
	added, err := r.VarInt()
	if err != nil {
		return world.ItemStack{}, err
	}
	removed, err := r.VarInt()
	if err != nil {
		return world.ItemStack{}, err
	}
	if added != 0 || removed != 0 {
		return world.ItemStack{}, fmt.Errorf("unsupported item component patch (%d, %d)", added, removed)
	}
	return world.NewItemStack(itemID, count), nil
}

// EncodeSelectTradePacket encodes a ServerboundSelectTradePacket (0x33) body.
func EncodeSelectTradePacket(shopItem int32) []byte {
	w := protocol.NewWriter(4)
	w.VarInt(shopItem)
	return w.Bytes()
}

// DecodeSelectTradePacket decodes a ServerboundSelectTradePacket (0x33) body.
func DecodeSelectTradePacket(r *protocol.Reader) (int32, error) {
	return r.VarInt()
}

// OpenMerchant opens the merchant trade screen for the villager with entityID
// and sends OpenScreen (MenuTypeMerchant=19), ContainerSetContent (39 slots),
// and ClientboundMerchantOffersPacket (0x34) to session.
//
// In OpenContainerState:
//   - BlockX stores the target villager's entityID
//   - BlockY stores the currently selected offer index (-1 when none selected)
//   - Crafting[0] and Crafting[1] store the two merchant payment slots so that
//     CloseContainer automatically returns any left-over payment items to the
//     player's inventory when the window closes.
//   - Result stores the merchant result slot (slot 2).
func (s *Server) OpenMerchant(session *PlayerSession, entityID int32) (int32, error) {
	if s == nil || session == nil {
		return 0, errors.New("server: nil session")
	}
	if session.IsDead() {
		return 0, errors.New("server: dead player cannot trade")
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != MobTypeVillager {
		return 0, errors.New("server: entity is not a villager")
	}

	vs := s.ensureVillagerState(entityID)
	if vs == nil || !CanProfessionTrade(vs.Profession) || len(vs.Offers) == 0 {
		return 0, errors.New("server: villager has no trades")
	}

	_ = s.CloseMerchantContainer(session, 0, false)

	tr := getContainerTracker(session)
	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	tr.open = &OpenContainerState{
		ContainerID: cid,
		MenuType:    MenuTypeMerchant,
		BlockX:      int(entityID),
		BlockY:      0, // default to first trade index
	}
	carried := tr.carried
	tr.mu.Unlock()

	if session.send != nil {
		title := VillagerProfessionTitle(vs.Profession)
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreen(cid, MenuTypeMerchant, title)); err != nil {
			return cid, err
		}
		slots := s.buildMerchantMenuSlots(session, world.ItemStack{}, world.ItemStack{}, world.ItemStack{})
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried)); err != nil {
			return cid, err
		}
		offersPkt := EncodeMerchantOffersPacket(cid, vs.Offers, int32(vs.Level), vs.XP, true, true)
		if err := session.send(PlayMerchantOffersCB, offersPkt); err != nil {
			return cid, err
		}
	}
	return cid, nil
}

// CloseMerchantContainer safely returns any cursor stack and payment slot items
// to session's inventory (or drops overflow) and closes the open container.
func (s *Server) CloseMerchantContainer(session *PlayerSession, containerID int32, notifyClient bool) error {
	if s == nil || session == nil {
		return nil
	}
	if carried := session.CarriedItem(); !carried.IsEmpty() {
		session.SetCarriedItem(world.ItemStack{})
		added, _ := session.AddItemToInventory(carried)
		if rem := carried.Count - added; rem > 0 {
			snap := session.Snapshot()
			s.SpawnItemEntity(snap.X, snap.Y+0.25, snap.Z, world.NewItemStack(carried.ResolvedItemID(), rem))
		}
	}
	return s.CloseContainer(session, containerID, notifyClient)
}

func (s *Server) buildMerchantMenuSlots(session *PlayerSession, pay1, pay2, result world.ItemStack) []world.ItemStack {
	inv := session.Inventory()
	slots := make([]world.ItemStack, MerchantMenuSlotCount)
	slots[MerchantSlotPayment1] = pay1
	slots[MerchantSlotPayment2] = pay2
	slots[MerchantSlotResult] = result
	copy(slots[3:30], inv[9:36])
	copy(slots[30:39], inv[36:45])
	return slots
}

func writeBackMerchantPlayerSlots(session *PlayerSession, slots []world.ItemStack) {
	for i := 0; i < 27; i++ {
		session.SetInventorySlot(9+i, slots[3+i])
	}
	for i := 0; i < 9; i++ {
		session.SetInventorySlot(36+i, slots[30+i])
	}
}

// HandleSelectTradePacket decodes a ServerboundSelectTradePacket (0x33) and
// selects the requested trade index on session's open merchant window.
func (s *Server) HandleSelectTradePacket(session *PlayerSession, r *protocol.Reader) error {
	shopItem, err := DecodeSelectTradePacket(r)
	if err != nil {
		return err
	}
	return s.SelectMerchantTrade(session, int(shopItem))
}

// SelectMerchantTrade selects tradeIndex on session's currently open merchant
// menu, moves matching payment items from the player's inventory into payment
// slots 0 and 1 (matching vanilla MerchantMenu.tryMoveItems), updates the
// result slot, and synchronizes the container to the client.
func (s *Server) SelectMerchantTrade(session *PlayerSession, tradeIndex int) error {
	if s == nil || session == nil || session.IsDead() {
		return nil
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.MenuType != MenuTypeMerchant {
		tr.mu.Unlock()
		return nil
	}
	entityID := int32(tr.open.BlockX)
	vs, ok := s.VillagerState(entityID)
	if !ok || tradeIndex < 0 || tradeIndex >= len(vs.Offers) {
		tr.mu.Unlock()
		return nil
	}
	tr.open.BlockY = tradeIndex

	slots := s.buildMerchantMenuSlots(session, tr.open.Crafting[0], tr.open.Crafting[1], tr.open.Result)

	// 1. Return any existing payment slot items into player inventory slots 3..38.
	if !slots[MerchantSlotPayment1].IsEmpty() {
		slots[MerchantSlotPayment1] = mergeIntoSlotSlice(slots, slots[MerchantSlotPayment1], 3, MerchantMenuSlotCount)
	}
	if !slots[MerchantSlotPayment2].IsEmpty() {
		slots[MerchantSlotPayment2] = mergeIntoSlotSlice(slots, slots[MerchantSlotPayment2], 3, MerchantMenuSlotCount)
	}

	// 2. If both payment slots are clear and the selected offer is in stock,
	// pull matching items from the player's inventory (3..38) into slots 0 and 1.
	offer := vs.Offers[tradeIndex]
	if !offer.IsOutOfStock() && slots[MerchantSlotPayment1].IsEmpty() && slots[MerchantSlotPayment2].IsEmpty() {
		costA := offer.AdjustedCostA()
		if !costA.IsEmpty() {
			slots[MerchantSlotPayment1] = pullMatchingItemsFromSlice(slots, 3, MerchantMenuSlotCount, costA.ResolvedItemID(), MaxItemStackSize)
		}
		if !offer.CostB.IsEmpty() {
			slots[MerchantSlotPayment2] = pullMatchingItemsFromSlice(slots, 3, MerchantMenuSlotCount, offer.CostB.ResolvedItemID(), MaxItemStackSize)
		}
	}

	tr.open.Crafting[0] = slots[MerchantSlotPayment1]
	tr.open.Crafting[1] = slots[MerchantSlotPayment2]
	writeBackMerchantPlayerSlots(session, slots)
	s.recomputeMerchantResultLocked(tr, vs.Offers)
	slots[MerchantSlotResult] = tr.open.Result
	cid := tr.open.ContainerID
	carried := tr.carried
	tr.mu.Unlock()

	if session.send != nil {
		stateID := session.NextContainerStateID()
		return session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried))
	}
	return nil
}

func pullMatchingItemsFromSlice(slots []world.ItemStack, start, end int, itemID int32, maxCount int32) world.ItemStack {
	if itemID <= 0 || maxCount <= 0 {
		return world.ItemStack{}
	}
	var total int32
	for i := start; i < end && total < maxCount; i++ {
		if slots[i].IsEmpty() || slots[i].ResolvedItemID() != itemID {
			continue
		}
		take := slots[i].Count
		if total+take > maxCount {
			take = maxCount - total
		}
		total += take
		rem := slots[i].Count - take
		if rem <= 0 {
			slots[i] = world.ItemStack{}
		} else {
			slots[i] = world.NewItemStack(itemID, rem)
		}
	}
	if total <= 0 {
		return world.ItemStack{}
	}
	return world.NewItemStack(itemID, total)
}

func (s *Server) recomputeMerchantResultLocked(tr *playerContainerTracker, offers []MerchantOffer) (int, bool) {
	if tr.open == nil || len(offers) == 0 {
		if tr.open != nil {
			tr.open.Result = world.ItemStack{}
		}
		return -1, false
	}
	pay1 := tr.open.Crafting[0]
	pay2 := tr.open.Crafting[1]
	if pay1.IsEmpty() && pay2.IsEmpty() {
		tr.open.Result = world.ItemStack{}
		return -1, false
	}

	// Check selected offer first (and also allow swapped pay1/pay2 if only one matches).
	sel := tr.open.BlockY
	if sel >= 0 && sel < len(offers) {
		if offers[sel].MatchesPayment(pay1, pay2) {
			tr.open.Result = offers[sel].Result
			return sel, true
		}
	}
	// Fallback: check all offers in order (matching vanilla MerchantContainer.updateSellItem).
	for i := range offers {
		if offers[i].MatchesPayment(pay1, pay2) {
			tr.open.BlockY = i
			tr.open.Result = offers[i].Result
			return i, true
		}
	}
	tr.open.Result = world.ItemStack{}
	return -1, false
}

// HandleMerchantContainerClick processes a container_click on a merchant window
// (MenuTypeMerchant), or delegates to HandleContainerClick for all other menus.
// This allows play.go (or tests) to call HandleMerchantContainerClick for all
// container click packets without needing to edit containers.go.
func (s *Server) HandleMerchantContainerClick(session *PlayerSession, containerID int32, slot int, button int8, mode int32) error {
	if s == nil || session == nil || session.IsDead() {
		return nil
	}
	if containerID != 0 && slot != -999 {
		open := session.OpenContainer()
		if open != nil && open.ContainerID == containerID && open.MenuType == MenuTypeMerchant {
			return s.handleMerchantClick(session, containerID, slot, button, mode)
		}
	}
	return s.HandleContainerClick(session, containerID, slot, button, mode)
}

func (s *Server) handleMerchantClick(session *PlayerSession, containerID int32, slot int, button int8, mode int32) error {
	if slot < 0 || slot >= MerchantMenuSlotCount {
		return nil
	}

	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.ContainerID != containerID || tr.open.MenuType != MenuTypeMerchant {
		tr.mu.Unlock()
		return nil
	}

	entityID := int32(tr.open.BlockX)
	vs := s.ensureVillagerState(entityID)
	if vs == nil {
		tr.mu.Unlock()
		return s.CloseMerchantContainer(session, containerID, true)
	}

	vtr := s.villagerTracker()
	vtr.mu.Lock()
	s.recomputeMerchantResultLocked(tr, vs.Offers)
	slots := s.buildMerchantMenuSlots(session, tr.open.Crafting[0], tr.open.Crafting[1], tr.open.Result)

	traded := false
	var xpGained int32

	if slot == MerchantSlotResult {
		// Result slot (slot 2): only taking out is allowed.
		offerIdx, ok := s.recomputeMerchantResultLocked(tr, vs.Offers)
		if ok && offerIdx >= 0 && offerIdx < len(vs.Offers) {
			switch mode {
			case ContainerInputPickup:
				res := vs.Offers[offerIdx].Result
				if canAddMerchantResultToCursor(tr.carried, res) {
					if tr.carried.IsEmpty() {
						tr.carried = world.NewItemStack(res.ResolvedItemID(), res.Count)
					} else {
						tr.carried = world.NewItemStack(res.ResolvedItemID(), tr.carried.Count+res.Count)
					}
					xpGained += consumeMerchantOfferPayment(&vs.Offers[offerIdx], &slots[MerchantSlotPayment1], &slots[MerchantSlotPayment2])
					traded = true
				}
			case ContainerInputQuickMove:
				for !vs.Offers[offerIdx].IsOutOfStock() &&
					vs.Offers[offerIdx].MatchesPayment(slots[MerchantSlotPayment1], slots[MerchantSlotPayment2]) {
					res := vs.Offers[offerIdx].Result
					// Try merging into hotbar (30..38) then main inventory (3..29).
					testSlots := make([]world.ItemStack, len(slots))
					copy(testSlots, slots)
					rem := mergeIntoSlotSlice(testSlots, res, 30, 39)
					rem = mergeIntoSlotSlice(testSlots, rem, 3, 30)
					if !rem.IsEmpty() {
						break
					}
					slots = testSlots
					xpGained += consumeMerchantOfferPayment(&vs.Offers[offerIdx], &slots[MerchantSlotPayment1], &slots[MerchantSlotPayment2])
					traded = true
				}
			}
		}
	} else {
		switch mode {
		case ContainerInputPickup:
			slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)
		case ContainerInputQuickMove:
			if slot == MerchantSlotPayment1 || slot == MerchantSlotPayment2 {
				// Payment slot -> Player inventory (hotbar 30..38, then main 3..29).
				slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 30, 39)
				slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 3, 30)
			} else if slot >= 3 && slot < 30 {
				// Main inventory -> try payment slots 0..2 first, else hotbar 30..38.
				prev := slots[slot].Count
				slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 0, 2)
				if slots[slot].Count == prev {
					slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 30, 39)
				}
			} else if slot >= 30 && slot < 39 {
				// Hotbar -> try payment slots 0..2 first, else main inventory 3..29.
				prev := slots[slot].Count
				slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 0, 2)
				if slots[slot].Count == prev {
					slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 3, 30)
				}
			}
		case ContainerInputSwap:
			if button >= 0 && button < 9 {
				hotbarIdx := 30 + int(button)
				slots[slot], slots[hotbarIdx] = slots[hotbarIdx], slots[slot]
			}
		case ContainerInputClone:
			if session.Snapshot().GameMode == GameModeCreative && tr.carried.IsEmpty() && !slots[slot].IsEmpty() {
				tr.carried = world.NewItemStack(slots[slot].ResolvedItemID(), MaxItemStackSize)
			}
		case ContainerInputThrow:
			if !slots[slot].IsEmpty() {
				dropCount := int32(1)
				if button == 1 {
					dropCount = slots[slot].Count
				}
				dropStack := world.NewItemStack(slots[slot].ResolvedItemID(), dropCount)
				rem := slots[slot].Count - dropCount
				if rem <= 0 {
					slots[slot] = world.ItemStack{}
				} else {
					slots[slot] = world.NewItemStack(slots[slot].ResolvedItemID(), rem)
				}
				snap := session.Snapshot()
				s.SpawnItemEntity(snap.X, snap.Y+0.5, snap.Z, dropStack)
			}
		}
	}

	tr.open.Crafting[0] = slots[MerchantSlotPayment1]
	tr.open.Crafting[1] = slots[MerchantSlotPayment2]
	writeBackMerchantPlayerSlots(session, slots)

	leveledUp := false
	if traded {
		vs.XP += xpGained
		for CanVillagerLevelUp(vs.Level, vs.XP) {
			vs.Level++
			leveledUp = true
			newOffers := DefaultTradeBook().RollOffers(vs.Profession, vs.Level, vtr.rng)
			vs.Offers = append(vs.Offers, newOffers...)
		}
	}

	s.recomputeMerchantResultLocked(tr, vs.Offers)
	slots[MerchantSlotResult] = tr.open.Result

	offersSnapshot := make([]MerchantOffer, len(vs.Offers))
	copy(offersSnapshot, vs.Offers)
	vLevel := vs.Level
	vXP := vs.XP
	vType := vs.VillagerType
	vProf := vs.Profession
	cid := tr.open.ContainerID
	carried := tr.carried
	vtr.mu.Unlock()
	tr.mu.Unlock()

	if traded {
		if ent := s.entities.Get(entityID); ent != nil {
			cp := ent.ChunkPos()
			s.BroadcastChunk(cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket("minecraft:entity.villager.yes", SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			if leveledUp {
				s.BroadcastChunk(cp.X, cp.Z, PlaySetEntityDataCB,
					EncodeVillagerEntityDataPacket(entityID, VillagerTypeID(vType), VillagerProfessionID(vProf), int32(vLevel)))
				s.BroadcastChunk(cp.X, cp.Z, protocol.PlayEntityEvent,
					EncodeEntityEventPacket(entityID, EntityEventVillagerHappy))
				// Vanilla grants villagers a brief regeneration boost on level-up (+4 HP capped at max).
				hp := s.MobHealth(entityID)
				if hp > 0 && hp < DefaultVillagerMaxHealth {
					nextHP := hp + 4.0
					if nextHP > DefaultVillagerMaxHealth {
						nextHP = DefaultVillagerMaxHealth
					}
					s.SetMobHealth(entityID, nextHP)
				}
			}
		}
	}

	if session.send != nil {
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried)); err != nil {
			return err
		}
		if traded {
			offersPkt := EncodeMerchantOffersPacket(cid, offersSnapshot, int32(vLevel), vXP, true, true)
			if err := session.send(PlayMerchantOffersCB, offersPkt); err != nil {
				return err
			}
		}
	}
	return nil
}

func consumeMerchantOfferPayment(offer *MerchantOffer, pay1, pay2 *world.ItemStack) int32 {
	costA := offer.AdjustedCostA()
	remA := pay1.Count - costA.Count
	if remA <= 0 {
		*pay1 = world.ItemStack{}
	} else {
		*pay1 = world.NewItemStack(pay1.ResolvedItemID(), remA)
	}
	if !offer.CostB.IsEmpty() {
		remB := pay2.Count - offer.CostB.Count
		if remB <= 0 {
			*pay2 = world.ItemStack{}
		} else {
			*pay2 = world.NewItemStack(pay2.ResolvedItemID(), remB)
		}
	}
	offer.Uses++
	return offer.XP
}

func canAddMerchantResultToCursor(carried, result world.ItemStack) bool {
	if result.IsEmpty() {
		return false
	}
	if carried.IsEmpty() {
		return true
	}
	if carried.ResolvedItemID() != result.ResolvedItemID() {
		return false
	}
	return carried.Count+result.Count <= MaxItemStackSize
}

// embeddedVillagerTradesGzipBase64 contains all 528 vanilla 26.1.2 datapack JSON
// files from data/minecraft/trade_set/*, data/minecraft/tags/villager_trade/*,
// and data/minecraft/villager_trade/* compressed with gzip and base64-encoded.
const embeddedVillagerTradesGzipBase64 = `
H4sIAHgd02oC/+1da5PjptL+L/N+nTre0WWTs3/lVMqFLWyTkYSiy3pnU/nvL6AbkpHUjWWb2fhLUjvuRs8D3dDcmr9fIlKSTcJS
us/JodyU5FhsvrM4Jkeab8ucRHRD8oTnNN/E9DuNt2//+bPg6cu3v1++k7iixcu3/738X1fAtz1PEp5ui4SVp1bj5fWlF2hLe9vQ
hOYkjrYsFwoxPR5ZeiwgsjvOS5DgicYJLSGS+xMtyiwmJX35459XfK146FrxjLC8Dtb+RFiaEBbP0DVJdxVpxcNH8/CNyPxNTL6T
7a7av9Ny26CckLzkMNNuJnGt8eZVihOjkzAiRgS3qANrVX8Buv4CI5ygw0xTQTQtabRtAc66ypxibUhWvEI0r9AIL5yBN9Pqc2p4
191VpVCa69B6BK3s22Z/YsKYU6Mx91IZz9/3J54tiOVkt2PlglDLuREuSnq2o+iBKHqbPSfxLCatw+H8XTRCSxcm3VShHQkfRMLf
JFVZ8vlm8jc7Sg9YTx/CCUBwgk2UM8H8ncbZdhfz/ft1Xw1BXw03xZmKfndHc/H1AvvJPclLfsxJBnWSgYLwAZKJskwNMBJszSMh
2RXYPDg2b3OMSVFsM5LSZYC9/RLZz/Akk7oNoG1J2JEo6MgC6I8slr3atjiTJLMpoYVQpPz8cVUBYpBOU3JNEaLnZWlxVT38WaXH
mF4DIqIFzcsr7ciH25G/6UgsGZFvBM73lKR9DcxSN5dQ5ozI0Icku6YAe+YBnHkfX7CSJttDThI6Db2XPp+E/HYnrI3mEHmek/SI
UUgExbQkCI2doIoQj9nxVGKVPmgc8zPqKwmm/Iyl7whx8ZcPhPj+QxgpAkyVZzEG/S7n5xQFn1KMfC5GXow9kH1XmfbOFMKdqQ9r
jzHftTUnRqeypHk6jTk09ghnzqNY/iEhacFECITpFGKasz1ssK9FRQzLBcx0e4hpcTL3g62k1h6FiM2oFSoPgkoM8Fz2TemRl3OY
+uEkJhkrxH9/VjGzwuVDcPltxH/g88B83STO9rUVQFAFm7LKS+Gyxb4q6RysoAmcdqLN4wXJfqoWSWOmJI+tKIQQCuEmpaXwie1Z
OMgcLm0K+UOEp0xMJGlDB4zOsLY1i7GWfBvOq+w/5gE+5m3Ueta0A7RiXa8nRqgrMBms376wYM3CQuvCDiRPgDOfRvRtcz5RYq7w
TiTjJSn5vIzo5nO+UI6Yu5Y5X5Lqx1hKwEY3YO5BmHubrEqyd5bOofG0EKEWzhidFyRZBnfMAWwfAtsfLE2YwfibhMbaSoINmAAC
Rgu4yLsRSi9RVEXG9oxXBWo9aAAqhIDSYhMxoopxvjZNEzo9jGECVSp6n21dd0XM9vBmZIXoyUX0AnO9TvptU4jZkPim0QQ1scn1
LV0oJ2dhFHVoNV6/4tNqvWjULHvb0vaAtOV6XbRAx9sUJJY9o4FN/cukYm+TSXZguXUr+kA6fgt1npFvWAqWv8r2z3lkizIAohQh
U86FBwpDkn9Edw6jz4bAz4aimz0caD745uuEKCdiGsOnhiNNsMjySkVBANlmkQYkS/ZkzwhMNiL5+/YCMbQ6Ywpfyu+EZX/BtJXY
V7NQNxSJfu88JSSmZPLLunsdYpaWlgw8GANvoz4yz0CL8fjZEo8Pw+PP9sC9VNen5Lwo7FEFMFTB5iCCstPEWrQmdtmj2GMLYdhC
0Y+w7Cz61e1JdMfzCE3bX10VTmgoqx3YZcmyjLbmDCQX1xV45vk70MuGGm9tCUaCY9kWafRB5TZnrWjc7gRp4ncGTXQ9BN05vxxL
GiEb9kBBeqitXRNLH8GyW8w4sYgCuPq3bJ4AARyw3jFWMLcSzwu6VZvT10APEdD7LqAgURTTGdDhrGlB8bJdTnIGjMV76bndP11K
X8DcqQ5Q63v5+6KeFBJBRHywJeQBCXmb3VT/rAvZ8dGXIVO15mvJxgey8Tdyw6Ag+wVCviUhffmSFIUtnQBIJ9icc1aSnQpMF1sp
GFhPPTZCSGnzc7mDvyxVL8jbkg+B5MPxLtNeMBr3DAZpuSvSiAIRJkQAAnUEtaSYacdqoyk2T7dbqX6NSoTjNmA8ABgxBZar6DNA
9GN0rKCxnBsrFYWrsAHmA4D5cvKQsnIOmr8RDUWLJaGI8XxJphsURNzZ0Luw5rFsxmM5VVO9gEIBk27gwISbSrCp5QBQy8Hmr4rk
5c+ZurnYdRY9cU72vCzJrHS9pw0UVhvHQFltqxmooTZ2UaVjNOqdUaCw7F6Aomr3Gijb7u+DSSaIypPbykBhtScO5VdviAOlm14c
2iZq7xwo3Ni1GJN/wlunNm+cjrJZnIpm7DhFZcE237JQrD0ApyMdAaeh/AGn0roFth4SfG1LJ8HpKF9BVkHtMjilxnOQTaoc6FLH
YhwKAeNQH38141Emy8sNEC8k63EaiEuMqfKMSATboG6F5Qam9PYz5+aQTZOsKw4iqRwNVKRyL4hkf5SfkrywrBQPVileUyliGj0P
y6uZAuRqogBBrVcESScguWHXLut7SbIZOSCibVcEkVXEIIJaPUDEm54AVnICK1P1yhDBztxhpMDiqhsFIa07T1ADdE68zEp2/BBJ
OeDBGr/1dpid7kmejRZFpy0VJtzaKkxamR9MVLNXmEI3d4eVnkDLVVYLE1WGiKGHUFC2C0RcWy+wSZT9QvlRCpWtF0dgRiGtuJG1
G4d82DjkbxojWezh/Y3WQADpxmcAkrJeAGLK6gByo2PhdFGwPQ++LNkdBF8WrQ9zL8vpR7+Xpdsz35ByE1CJ9SnvZbn6eDeUD1S6
PgUOQNkc/wbUe33uG0JIHfheFlQnvSENro5408jSYQOYwwZNjLzoCMGmqTSAJCwWDDZ1nQEEW0cBiCoTAMgt3vYwyE7f9DAIz9zy
MEhPXdYwiM7f7jAoTN/sMJaeQMudutFhEJ26zTFJD6EwdfXDhHjy2oepSaaufBj5TVz3MMhOXPUwGgX+mseoGwhh3UA/kc8IS0uW
HqHfK0VsvHTMHJN0oS9Pm0GrPQDygwIlixMXBQOFM7Z/hxd94tS2ZmwSL1h+6trcCH15/swxEV3q8tSPOtw/XbGTKrONN6k134ra
llJ7g9++Ha9NnNCXN5cBYZrMnNZs5QW2iSPGNXBtioW+vLlsCW2TAkGe5cafOtmn/p1vdtWH+Nds/3ehcyalOqI0eXVoSmM6e8iF
RsLid5TCgeYJVVVTZExeVKIfQHA7Umc+mLzNcaFxksOuzeX/i5JqK8BVf3cuVZ4dbo6mv0IUtCPHGLWCEmVmo6MI0/JNdBTHMPn+
YhxIPCVVyWJ5X+JEoZ84jK9fToOvjiSXxykotE7VBRiYsMwaAZPck31ZFTDZSP4QMw4EkfEs+4CJqvCZ5/sTi2AKos1ZlQBlf1Y5
VZ8A2qHaiK5iBqzCdu8frtHs/8MV6v1uBKQfslOKCCuADbDneXoQ7jGKgiflYxZ/bPlhW57oVvQlMQV+h2cyEc4HFZ1aUfAEWl3y
ilxBaQQ01O6iG0KnvWCGUGluKsEVmusNBcliGdPDuDDhFjiV7mYESqu5p4HSQX+kuTiC0pHn0vMPnE5GBBU0ukQ4ci5Ctm2W84wc
K+hA1KxxYpwfLN6u3iA6CkTZzaYicPxsF4igfYS+ogxtgW5pCabQL3BDOWDg9ItsGMq4Jmu3YFGDDbyJ2/VEoK8163XQwonoMcXY
QWLDmcBptWonD96i9eRlQrzWSQxteK2y2ll86zsDh3OygxJTrKO68ioGQ0xzJlVxEuNbAu+dcBpqdM8IMBYrEnlcV54OjSk5AHXE
D3D0cOmMs3rW2p5VBX5EBAtqrpsDQ0TZYhjDUM2NVTqzOKqDMmiALobJUjaG6OWgFiWd6iCaeycsBNiF1te6kbOBlMhzleRoPYOt
0mvmsJkY6eTq1B4z7IKlj1Wa8XMEjZ4zHv3kMSpmjDkqXgSLd7EiWKOJE8HyqMKb+BAs38SGYPkuwgNrdJEkWAOwFjrd83LxN5Z+
ZwXbsZiVH3K5SPoY1GsoyXi65g6AXuIo9/EFK6PoeEH5zPPIns4Nl+0vP3btwr1e4njp3hrUtQveeomYJe+hnuXa9SWda1ev9RLn
1q+n7U4CE7P4cjJfuSigSsuXb95/vry+iPg74omQ/6uSmaBevukr6VNFCcTqN8FOJzeWgoPz1gPngcB5CHD+euB8EDgfAS5YD1wA
AhcgwIXrgQtB4MJZcFP5ri3AjYqaADeWgoPz1gPngcB5CHD+euB8EDgfAS5YD1wAAhcgwIXrgQtB4OYdYjbBtQVCU3kTMI2iSKze
ylg9OFYPi9VfGasPx+pjsQYrYw3gWAMs1nBlrCEc64JvmTPJ2qAclDSFbygERuathsyDIPPgyPzVkPkQZD4cWbAasgCCLIAjC1dD
FkKQzXvARPpQC2TDkiaQjYTAyLzVkHkQZB4cmb8aMh+CzIcjC1ZDFkCQBXBk4WrIQgiyBQ+YTORpA25c2BS+CzkMRG9NiB4QooeC
6K8J0QdC9FEQgzUhBkCIAQpiuCbEEAhxwV2msljaIByVNQVwLIbA562Iz4Ph8zD4/BXx+TB8PgZfsCK+AIYvwOALV8QXwvDN+8d8
EkoLkMYCJ5CaZbFwvbXhegi4HhquvzZcHwHXR8MN1oYbIOAGaLjh2nBDBNwFV5tM+2gDdVzYFMwLOQxEb02IHhCih4LorwnRB0L0
URCDNSEGQIgBCuKl2/j2EEMgxHl3MSdGtKjBQUET0IYyUFjeWrA8ACwPDMtfC5YPgOWDYQVrwQoAsAIwrHAtWCEA1rzJT+aXskA2
LmsC3IUYAp+3Ij4Phs/D4PNXxOfD8PkYfMGK+AIYvgCDL1wRXwjDN+8f01e3LQBeFDaB8FIOA9FbE6IHhOihIPprQvSBEH0UxGBN
iAEQYoCCGK4JMQRCnHeXpQvGFkAnipyAOyWNAj060tyCDq8AXRcJBd1Io0BfnMReo67bQqHAO/lZ6DPHYW1AXxY3BdcgiQPqrQvU
AwP1kED9dYH6YKA+EmiwLtAADDRAAg3XBRqCgZp63NEB3vb44OgouHpGpoN9ZN/lx/5+YdEAZS8qP5SQH9uqkIJvNT+aVSWRp923
ESv2NfMv//FeX4Tzl6q85o+BFB+V3R5E/seWQv+qDIyH9grNVWT+ewsy9ZstMCLN+y5XkQhvQaJ9wAlGo3vu6Soiv61CRH8YgrA0
ISwGushI3pLMFIHXlx+ZaqyrmIDb5VLlusbx5xoHz80f32JYJKR9DMjiSzjdLM3XW+jeFxT2y3ZB9GAmpRv2yTXBt2sJAnu1scJN
nMiSTHFiFGBotdgN+2QrEjH5Tka5ku7sMhoCK7eZu/B02UGnW1bSZJvwiB2YvIX77X9/vxzEzENdyRuwrEvbnkUwtVXBlLx+y9J9
XIm4jEQRkypEuBsvSvXOE0+ppFvmFX19aTTEZ0UdiepRMYEovWnE8iMbhntVyg48T2QV8EyWPArtRI0q3oLmXxXLZL4qITuB/cDi
kuYqB6qiW/9bVb74Z2HqsprB6fUlE3psLzqPGvy4PuSHVa3984ccPAWsg3BLKWpEIq2A5HKg/eN10qCGEAZm5eMc5HeAg4QrGZdx
zHza15R9aW93PtDEzFEL0sregpXNbO6W40QI8DS0KUPTwp+HmtpUGIY1tq/LxuZ/WcnYDOHY09CmDK17oPeBRmYKh9cfM0H21d66
e9vsT0yEc+mVAeVXaEA51ys3ULrIEsWjyyVTP3FclPS8yEWTXSlAhq9V9Mgznr/vTzy7cxOYllpaKHZN0FTnfWmYDKkGYkPC2+w5
ie/tDKaposRhx6BPKsllzp3Gqy65tF2K4dO6oi256xeKJim1ZjrJKZzipBv4Y0n5mx2lh3tb2heDpUkcqNl8TyGpypKnDnRcNRAr
FsEmyhmtk/YOsz3fenFlrlHGmKxG93BTnCkttzuai+IKF3gNAKFIDW40v2kppbJFPkLmSi7X7UuMoGckk3nL79oYnmmcVEAwg8yA
iDd4a755PH1Lf2SxnLq0icr09tGmFVM9avsG+z+v0xOaiMrMkBexePNBIZ+pzLGRmNKx9FJO1FYj2mEVIOX0xzxTUkJ1LUtDen0p
KJFJ3kSNsEq2xZcvk1OVQhi7TL0ntNT/BGFRk2mhZprfXsTMRb5aLutoVG+yAkqSH8V84Vs9y1H6q8yJ+q/OzIfEr1vZNn//s9o8
aMINhRHVc9EOSm23aW0so2lrqbKy5VxYbim7kNeX+m9CrDyxwYqVLGaKXmvYm+/q1Hg5egrmTJJhru+CfCdpOnyZVhiYAPHyh6qh
Vedw4cq+qPjc0RXr752qct4NazEnvbC2gKcTPtAJS8KOQ4crUn4e/KHpK931wEZjW3cVd/TA4Yfn3bAB16o44IYj9E83fORYaBj5
Po3fZTFhafEAv6s/PO93DTgH/a5B//Q7t4e/TxmUtjbWgH+Ab/bVNhebNvAc9M4W/9M9H+ieXQd/MSp+Tm8UfcvHI3xRfnfBExU0
F/1QYX96oVuDpJq4u+9wCvkDHK6tsRmHq6E56HA19qfDubUyOo5Km5HRDRc8xtLpMpLSe+9+vRn49GhQR8AHnPxNt7z7kDPgWh8E
3msdETD1i3xPSTpYC75Tx5jwtFLnzWb7xEt4j+4UO9z/nv4QcRDTX+n2BcByS9FNyos8JNnd1XAH311YahrKumC+I/RPIzYYsXcL
Iw5G74/uSJrSfHEQ0YUdjlUuQpW5KMR4UGKl6wKTdV5ReJV3sp8lOhzHgnXE/odrjaCe8YS2gib86demXGsJ9eAtsCE02U/SDt3m
68hDXGsF9SozsBU02c8xDLR7yK5VuXzMG1znvfAnNn3LjeQHtE4dU+YiBF1OftGJ3vKI6fWUtLfRgVZ3ofFZgxDAkvADbEw9PA9u
ioR+ev93MxZMhEhaEmhLDMU/yUSo3S92q+Z5TtIj2AUG0p/46BRkR/gBjZGxFLwUoMl+2m1CyCbFI5qhyrMY7BMD6V95x/YBLSHI
QZuhF/20EVLTUbnWCOeTCK+hzaALf9aGcLRb+qBxzM/QhhhIP5fMVmmL0Lj1dOY8iuUfEpIWzYbN+rtPk7tK3eed2hetq+K5o2SZ
DAl/Gbe3zWPMd20XvM1IKWowXV7dMihduZayVpqU5vXYNz0qKEqe0snMA96rITpQKnfPMNKBz7moU2F/MS1Od76r75s6Qx0P6vZx
Tag/VxeTjBXivz+rmC2voWjCa7cF4nhSy+HI5TpjeuTlnQ8mmU5G9GBw+8o1GV/3//OEf1w6/fkqt1g572lHpUlic+B3bxhvMpON
QoM7cFXT0VMiRrJ3FQNuvEynl72SUrhWKNTSqQ/Q7UQHEt/7QN9/J8/z1XBsBpZgU1a5pFLsq/LehEyuo8OxIaTlq/sh4nUmM+U3
9bPMaqxxg47uiiAg3KRUvsoogun87l2DqW/Q4KD4NA+Cv8kERGV+fT+3SrqrFgsmHuiIiGg0vzsNY5vUSKxI9IdTKIkm48uvlzUn
5dfOZwWHnXHxDX7vuv9qTF4nkVjV/flEyd3tx5QeSQGxoKDdNcmyeHp6Elx8Uck/Lh3aBf6sSrJ3lm4zhmGhaT0utu+4tGjuO0gY
faJGggqFGxr+IOveTGu8mTMIsjuM3zg+CY156kCQpXCgwvmGgnZujrwDXpkQQndog9CGQFEVGdszXhWXqVqNy5D0cKD7slmSrNrj
8OZsw6k6ufKdNeuAusLvRoU/hZvIjPZFORL/zSh+puQ9pWrdVBf+ahTexSyNDNL1WtyFeMZZcYHaDKMgZSshVw0n101lBW/r+nuZ
W14ctcltOlKMuegLi0xASuUDdLUXFzHbT3dJvmGmJgvIRwXcfvEBE5FrdHkcCTeoo0kMS13PkenTgRVimpKQVAbqD0iu+2WF5Lo6
h35UjJqnUJY74k702uXUq9JQ6ixyclYMSP/8UpcwmEeYPSStmZYnK/0nrqyJr8ZWxdZCUaoewYHAv0ZiZ5MyaXXkRs5qZGSmUegj
myQ7sBwS3dSCN1h1tYn9NSoFiRPxZYNr1b/c2rvqr9zAwfqC8dXiG16ykL9K/8t59HzGYmHHuaupx7xhoQO45gGL1SZ1mmE1/nbn
DtBfdBAPRyTYlDkXAbiwVvlHB1YPBngsg75wQ/Zkz4iYXpFVls1/gfPV4yW4voKsKzki+fuWk/dfuZpx93nHCfX1CrKu5iZx+C9W
yQsXN9AH4UYVoFWadcX/2qbd5tu0rOCrzTqrDgeaP2DUMb5K0IGx5lNkebX/pd10IjuotY9qNYar9ZiOX+CSi0/n6d2Cy2mLUrj7
8TgN+TEnYh4wmLAdYpaWK03U3i5tXBV/i/dVaio4+kXJrn70ZpWDgQoIag2kJdGvH+z48rtvO3tzW3nVoMevLOLex0pMQX9nmvC5
WMtC2zLMeVFAmqIVdGWvsKfykLVB4wHt0dogiofpUeRxuzyXW8bLLTt1LecByywXnuCv3zG9hTgDOlCiDprdeZXc5AkNFLsIJdyo
QGMwzJcsy+g4ZDGN9rDqxXmT3JvNBRqebDOufp2wc0WE7OQ5nT9eAY8d6qxucIK3K9emDS67I+NY8eyTxn1SN1I+pGMyj9P+o46T
9PYkxsbszHK6PXF+75cbfzeumWp4UE4S153bmefvg/lU9CGMqfnx+hfmtU5HFCxdJa1U4kp+qP8gSiyqRMyjI1mKzGL790va9HyZ
rInfQtMpF5byhMld9T9MJ1WqRE5JVvCCy3qY8QdVc3se83zVa3gGDFfdyrvucUmQ2cT0eBQxZPHvNpq2Fh5nMh2CqwzGX9Vg4ofE
d6apZzwM7zx0p+mZrX/HefkvN31VBY+z+/rzVxl9sM6SC8hgTjRORue6/nUWU9fB40ym+f5VNhPexmY+8VrdkIr/DDKfQSY+Zuhu
SZ9Y5MI1XA0NapV0yCqYGAt4XtAtyROe/8sHhL4iHjgqaCCu8oevKy1FjG3oV7nQPeQVPuOkZ5xkbTMFiSJAAoBa7OZuDTN/tsvV
uQv9YEMq987kKqO2lcbfMacEds2iYMSrnXBL9a1tlrM93Q4Xb/Ul5Uvhl1fLRfLa3+IPzAo5T+OPOuNYyeQexLcDiQu6vFWxhpMN
a3rGyYqSyzSMN1wBH0G5ykq/XBeMGWxTYipEV3AAHH5oJG8QiNlxyEjmxvaiAoJafepYeJvd/fcfTBx22rbD2xckh2dP9+zp7tjT
ISILg5HGEiMgv2Aj97i8ED14/+lhTw+7v4chxwE9nx8pCkQaFiV/o7wBWA4ySX1B9ncekk0zqQYJbmmqYxIMYrz69NKzz3j2Gbfu
M0JLK93HfP++fO5XSjmSXdLIos6MDUjt0GXQvnEWUyyTc85K6QjbB0xMTMckB3gs12LCwfMMe9GZAJaVelFHMqKY+DQ584GUBtKO
sEqIAC1zvMTqgcDYjUQvLRjMrL5l0mdkZKYebfp2j1K4e0rGGraWSePECirz1Ks8yzULTDBn1H9cpsCWXo3GiZQtTfpqcHhaU/A3
wm1pwcp7s3gzLY63WFDRaUskYjx3hEcDxYpGt7eVs6wxdnMQM+krI83beMmbDaeMx/JirFpxUA2Nyao51nWRV9PwNrQ0m3GN1VHM
lCxZNaoOsGqQuNA9aJWC7B70t9vJ/l2uifwUtSymjDnZ87IkgHfcjXoPzK9oZoam5CiXilo1kknNNV5YQm4yyfk5tWkis55zzNCU
nOSy/yA2jWRWc40XlpCTTMSY9mHRQmY113hhCTnKhNLUqomMes4xQ1Nykov20D2+qeaU3eRoR85hVpb94JyymxztyDnKKrHztsR1
P0sompCTTBLxW1oSi0aa1HSQnQUtJ/nwnKRHG4+aUnSPG56Uk2wyeRwE305mNdd4YQm5yaTKs9jGl6YU3eOGJ+UkG7m1j28mo5Zj
rJB0nORxPsnFf3z7TOg5xwxNyUkuzVkSfDNNKbrHDU/KKTZ/VSQvf975CNeb6QxXjcTiWFB/0KkhswOdEtSFb9MQ13DIpFQOJVFL
P5BFcaLZieaRfPlXbd+dOb/7IS1TyrQeDeaUlk5H7aI4Q6dDY0mnuzZ7oiRfPoJai90yoTEGvFpycqUpOjCWLVEP266w6dFY0fEa
t48+7n4iw5v0egEGdbREYzM8vLAneUZLxHkZXe1xpxon+chmBh7DaC3CIQYVtWmQTss9NsDmqKiDraHGI3xzaGoO8oE1yGAodoaB
OlKAbhBNyz02oOboJJ3Cr4IEdGtoWu6xAbXGIDhyCD+lqU1z9GoO8gE2SCvqFAPtEAC6WS50XWUGaqCRvINc7PqyC11XmSFayc3e
TW33W7RP4qj/JGDPSRz0mXZHH90gQ0UnOYGaRRd2ikWzgY9umIGei4xAzaLJOsVBbdmj20TTco8NqD06Sbfw1xvz+PbQ9VxkBGuT
XtYpDnW2AWST9ErOcQE1RivoFPp6uRzdFrqag3xA7THcKXCGQZe2AtkkAz0XGYEaRZN1goOaITmyGdRisd0L0mbmjjAaIrLnlTjE
KLmKS90pOUKmA2PHxh/t1u1oBNysE5KOvHVrJCPMFcalFnSYitr6AXFpJd0lozZOIFxaQXepqJ4eQqUVdJmKXK+HcWkk3SWjjVcQ
RkNx12mBjW4o7jKtBNpOieMt1K5BQthosu4SalbvIHx6UXfpqMUvCJlW0GEq9ZoRiEwn6i4dueoC4dLIuUukDsYhVDpJd8k003wI
m17UPTraSOjUjHq8UuAheTVdriOcejS2fFSv6wibFostF9lROUKlgWLLpPFsR8j0aOz4BBuXVtbGa2o+loua77tCpgVjy0bN+B0h
02Kx5TJaUyNpSnPospoSvsMwGtpSkjNlKKNO1mFC9aoZkJEm7C6levEMxkiTdZdQvY4BI6TJukxIraNBGfXC7lLSF9JgvC40XCeH
McMLDZfJJYg2S9xvrW51DcZpKO4urXaRDcZqIO0uqXqxDUZJk3WYULPgBqSkS7tLSq28wRj1ou7SaVbfYIR0YXcptUtwME4DafdI
1RGPM0d6GjC2s8F2fHGEjwbHllHTa7myWtehsePT50PJCEtLlh4nj/T5hqeWa5Vbpq3AkEpYeZKPV3Fy91QPpmfsJA5UkgeFX7sj
LTqq5Vk5jePr3hH0v67zvG8Ln+Xim8IqeOnAS9o9GNQeSsl53JpTl8lFPQ1Efiy/7NZJWjaMdQqXadgnDoUtJN2BnbH9O7zGG2l3
4Bcn/p3GQPS18OPAa09oMZLwNAIZjSY7hO7jeqFgpd1PE5/+1VXVHYwNCv8C7lmUv42paK4C8whuoyHfnCU/BJ//Ss6i9OYV1PIj
o4PPVSk78DyR/I3v5AouirRg+FfFMvlg7Tov5rbVNPNW7g0fye2+bmVP97EhU8f0tCOjHbW98uNsyTguIPsn/379k2HUeJqW0bSa
IfNxlmUas5GG5d3AsA6xmJjdOfr2Tc+rKiCo8wo9jWDTxhZ3ImKObFBTUB39pXe3fJ4Dx5J3azX1GO/WAVzj3W/eSiuEMMt6Dh0I
43rk6DHCcI2JhatbWDhjYc/AF2NiD419xyCu6sf8lRZpz/IZ6pylx/rf+WZH3ql8M1ng4NeN9GusF+poAKsnF2xaxyF7smdkG/Pp
9fTfL59x75QksWvukl8NvCBZbNwMMENuxIft8TuyPcKr0ldPM4pjViXLTJTY2qndr4D9s8qpOiFULmPvZd0hsKO0zDkvtwWlUQHY
yNDFHaLB8v0J6cidzuP8uIYAdeOBtJNe3GUaR+xPDrKTO2JNzbl3FIv+qLxDJNiewg5BC8EhdOzm6tdbWZQAx4XdM2CqhFrW0jtu
gD8nLBUxsvgH8CWWC43rPN2/Ubu0tykwPqLfwHDESRSkpCpOYlRL0FxaxQeOINVuJ1PMYQzsQsVJCxMxa1ktRyS1mJsMTjTPP5BR
Sa/0OKNqMEDjkqG4k4GJMPb0EPMz5L5JJ+pON9Ve90J0UPoNMTdIRPKHWHx0+bhCK+kS+Px9y8k70pt1tcf5c4cC6tFjBSd9OhJ9
TkniWN5hKZYHCiUqdZS4O5b1PLPwi51ZeLv1abBJUzrQfLlzlULuWP+B5bjodazgZOQnQR7iDxGdFycQo1b4ljcdrqFTnAS8/Ttg
lbPMufAK0TqaEnTkuy2Lowjq1CHO5fusreSwOcK7vvQ4zYPHkbzvAQ6nxgoPfcBymlaTbgYR4+oZatzozrqrOCgW2vUdR2hUacbP
kWm2NP0uVKtjud+3PouTmMmhxpaxgpNjy59Veowpch7SKz1uFtJggM5BhuJOzkDeaZwt8pBCbg7rw8TZiB7rMuO2G93WMG8ZmpB7
w0nMRFzID9vyRLffxfyVfgDyH4xVhnR+ezCbjESTDRO+GtkIFWfGlDY1O8q4Etf8hIu/sfQ7K9iOxaz8kGdpxpEkfNGhoGVTwMsl
+fGnXuYm1U0p4On0Hft67VIyou1HV5ndaP6EpMecf8fGELra46KIDkUmJprkWMUUkD5lrOJkNJHQWF5WAJ3D0WQdsiteFFORdjt3
vLSqTslysr0+j5SIDrskyyFqK+gQ8qpkcVVsixPkWvpQ/LrFjhs5BX7L5eG7LZiNFtf3WHgm4gD6QYWDFgVfPp85kncm9Oyz/yIG
72HKYDf62AZTWcUsg2bYUsIOUfghDGQbEVYsz2Y0WXcIZGQvz59Dztb1opan626BPqbbkzAM+ZscfVG5ZEa6j+tlFRSriGOk6czg
rXDhxztd7cHNgRn5xgpODn9tmnWMi2iZ2R3priQi2IDRizoEn0c/TW8+TreAUrjuNPONVlwzzup7ejnLJrYip7utsa47HRfPsuWh
XEk5ZFbV4UBzzM72hYYb29pZlWTvLAXAV3IPHCJqAMAVjoG0S2bTpvLD9EiD/H9uEGme3UCw0B7qcIeCxdF9Xe1x3iBRFOJH5Nvl
UsWZqYQEBAssOkmHrIfz+sp8XiJGYU3LmfF31owuJwyXJvTA+1EFJeoUJWAZvxd18lhW0WSeB6y79qLXMQluxCSpj0ozMWiRA8I5
horu+EeWV3vsblev9LgxosEAnVIPxZ2cUBfVkeTbPQEcwuxFnemsymqHO6w8VnDyQNl3lmLCQCn+OJc4nyiBJivQZN0JO+pk/bjI
u9NxjQYs+NNkHSLA4qi+5gcwpF7WHQL9U4MISxq+T/hYIgeay4s5clKTMfEHuW84nVtoktJ1WYYuL8dcYLLhdpKvKske/07JksYH
mdvP22BPWPzerHAtNod32+bQoNgwOZNS/LjjZRnTu2Wtaq7UjW6i1SfbRCwgjKv5sTnsNuhoJGDFdOJgnHUdONGcOhYIF0pEXY6T
to/z8J55Hj1vSwLS8MqKemQWXvX9qx7BuHLmbjQnY7J5eMJz9Dseq1F4TAJhL1glgbBO5DOmEB7ifyYRfiYRXi+JsG5bc0lenyMf
Iovw4wa/IYRrLOx3YHbXf/4f6WAZZLUQAgA=
`
