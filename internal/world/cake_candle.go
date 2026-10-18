package world

import (
	"strconv"
	"strings"
)

// Vanilla 26.1.2 constants for cake, candles, candle cakes, and composter.
const (
	// CakeMaxBites is the maximum `bites` property value (6) on `minecraft:cake`
	// before eating one more slice consumes the cake completely (7 total slices).
	CakeMaxBites = 6

	// CakeTotalSlices is the total number of slices (7) in an uneaten cake.
	CakeTotalSlices = 7

	// FullCakeComparatorSignal is the redstone comparator signal (14) emitted by
	// an uneaten `minecraft:cake[bites=0]` or any `minecraft:*candle_cake`.
	FullCakeComparatorSignal = 14

	// CandleMinCount and CandleMaxCount bound the `candles` property (1..4).
	CandleMinCount = 1
	CandleMaxCount = 4

	// ComposterMaxFillLevel is the filled composter level (7) that schedules a
	// 20-tick maturation into bone-meal-ready level 8.
	ComposterMaxFillLevel   = 7
	ComposterReadyLevel     = 8
	ComposterReadyTickDelay = 20
)

var candleColors = []string{
	"",
	"white_",
	"orange_",
	"magenta_",
	"light_blue_",
	"yellow_",
	"lime_",
	"pink_",
	"gray_",
	"light_gray_",
	"cyan_",
	"purple_",
	"blue_",
	"brown_",
	"green_",
	"red_",
	"black_",
}

// AllCandleBlockNames returns all 17 vanilla candle block/item IDs
// (`minecraft:candle` and the 16 dyed `minecraft:<color>_candle` variants).
func AllCandleBlockNames() []string {
	out := make([]string, 0, len(candleColors))
	for _, prefix := range candleColors {
		out = append(out, "minecraft:"+prefix+"candle")
	}
	return out
}

// AllCandleCakeBlockNames returns all 17 vanilla candle_cake block IDs
// (`minecraft:candle_cake` and the 16 dyed `minecraft:<color>_candle_cake` variants).
func AllCandleCakeBlockNames() []string {
	out := make([]string, 0, len(candleColors))
	for _, prefix := range candleColors {
		out = append(out, "minecraft:"+prefix+"candle_cake")
	}
	return out
}

// IsCakeBlock reports whether state is `minecraft:cake`.
func IsCakeBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:cake")
}

// IsCandleBlockName reports whether name is one of the 17 vanilla candle block/item names.
func IsCandleBlockName(name string) bool {
	if !strings.HasPrefix(name, "minecraft:") && name != "" {
		name = "minecraft:" + name
	}
	for _, prefix := range candleColors {
		if name == "minecraft:"+prefix+"candle" {
			return true
		}
	}
	return false
}

// IsCandleBlock reports whether state is one of the 17 vanilla candle blocks.
func IsCandleBlock(state uint16) bool {
	info, ok := stateByID(state)
	return ok && IsCandleBlockName(info.Name)
}

// IsCandleCakeBlockName reports whether name is one of the 17 vanilla candle_cake block names.
func IsCandleCakeBlockName(name string) bool {
	if !strings.HasPrefix(name, "minecraft:") && name != "" {
		name = "minecraft:" + name
	}
	for _, prefix := range candleColors {
		if name == "minecraft:"+prefix+"candle_cake" {
			return true
		}
	}
	return false
}

// IsCandleCakeBlock reports whether state is one of the 17 vanilla candle_cake blocks.
func IsCandleCakeBlock(state uint16) bool {
	info, ok := stateByID(state)
	return ok && IsCandleCakeBlockName(info.Name)
}

// CandleCakeNameForCandle maps a candle block/item ID (e.g. `"minecraft:red_candle"`)
// to its corresponding candle_cake block ID (`"minecraft:red_candle_cake"`).
func CandleCakeNameForCandle(candleName string) (string, bool) {
	norm := strings.ToLower(strings.TrimSpace(candleName))
	if norm != "" && !strings.HasPrefix(norm, "minecraft:") {
		norm = "minecraft:" + norm
	}
	if !IsCandleBlockName(norm) {
		return "", false
	}
	return norm + "_cake", true
}

// CandleNameForCandleCake maps a candle_cake block ID (e.g. `"minecraft:red_candle_cake"`)
// back to the dropped candle item/block ID (`"minecraft:red_candle"`).
func CandleNameForCandleCake(candleCakeName string) (string, bool) {
	norm := strings.ToLower(strings.TrimSpace(candleCakeName))
	if norm != "" && !strings.HasPrefix(norm, "minecraft:") {
		norm = "minecraft:" + norm
	}
	if !IsCandleCakeBlockName(norm) {
		return "", false
	}
	return strings.TrimSuffix(norm, "_cake"), true
}

// CakeBites returns the `bites` property (0..6) of a `minecraft:cake` state, or
// (0, false) if state is not `minecraft:cake`.
func CakeBites(state uint16) (int, bool) {
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:cake" {
		return 0, false
	}
	b, err := strconv.Atoi(info.Properties["bites"])
	if err != nil || b < 0 || b > CakeMaxBites {
		return 0, false
	}
	return b, true
}

// MakeCakeBlockState returns the `minecraft:cake` block state ID for `bites` (0..6).
func MakeCakeBlockState(bites int) uint16 {
	if bites < 0 {
		bites = 0
	}
	if bites > CakeMaxBites {
		bites = CakeMaxBites
	}
	if id, ok := nameToStateID("minecraft:cake", map[string]string{"bites": strconv.Itoa(bites)}); ok {
		return id
	}
	id, _ := nameToStateID("minecraft:cake", nil)
	return id
}

// MakeCandleCakeBlockState returns the state ID for `candleCakeName` with `lit`.
func MakeCandleCakeBlockState(candleCakeName string, lit bool) (uint16, bool) {
	if !strings.HasPrefix(candleCakeName, "minecraft:") && candleCakeName != "" {
		candleCakeName = "minecraft:" + candleCakeName
	}
	if !IsCandleCakeBlockName(candleCakeName) {
		return StateAir, false
	}
	litStr := "false"
	if lit {
		litStr = "true"
	}
	return nameToStateID(candleCakeName, map[string]string{"lit": litStr})
}

// MakeCandleBlockState returns the state ID for `candleName` with `candles` (1..4),
// `lit`, and `waterlogged`. Waterlogged candles cannot be lit (`lit` is forced false).
func MakeCandleBlockState(candleName string, candles int, lit, waterlogged bool) (uint16, bool) {
	if !strings.HasPrefix(candleName, "minecraft:") && candleName != "" {
		candleName = "minecraft:" + candleName
	}
	if !IsCandleBlockName(candleName) {
		return StateAir, false
	}
	if candles < CandleMinCount {
		candles = CandleMinCount
	}
	if candles > CandleMaxCount {
		candles = CandleMaxCount
	}
	if waterlogged {
		lit = false
	}
	litStr := "false"
	if lit {
		litStr = "true"
	}
	wStr := "false"
	if waterlogged {
		wStr = "true"
	}
	return nameToStateID(candleName, map[string]string{
		"candles":     strconv.Itoa(candles),
		"lit":         litStr,
		"waterlogged": wStr,
	})
}

// CandleCount returns the number of candles (1..4) in a candle block state, or 0.
func CandleCount(state uint16) int {
	if !IsCandleBlock(state) {
		return 0
	}
	info, _ := stateByID(state)
	n, err := strconv.Atoi(info.Properties["candles"])
	if err != nil || n < CandleMinCount {
		return CandleMinCount
	}
	if n > CandleMaxCount {
		return CandleMaxCount
	}
	return n
}

// IsCandleOrCandleCakeLit reports whether a candle or candle_cake state has `lit=true`.
func IsCandleOrCandleCakeLit(state uint16) bool {
	if !IsCandleBlock(state) && !IsCandleCakeBlock(state) {
		return false
	}
	info, ok := stateByID(state)
	return ok && info.Properties["lit"] == "true"
}

// CandleLightEmission returns the vanilla light level emitted by a candle or
// candle_cake state: `3 * candles` (3, 6, 9, 12) when a candle is lit, `3` when
// a candle_cake is lit, or `0` when unlit.
func CandleLightEmission(state uint16) int {
	if !IsCandleOrCandleCakeLit(state) {
		return 0
	}
	if IsCandleCakeBlock(state) {
		return 3
	}
	return 3 * CandleCount(state)
}

// PlaceCandleOnCakeAt converts an uneaten `minecraft:cake[bites=0]` at (x, y, z)
// into the corresponding `minecraft:*candle_cake[lit=false]` when a player uses
// `candleItemName` on it.
func (c *Cache) PlaceCandleOnCakeAt(x, y, z int, candleItemName string) (uint16, bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return StateAir, false, res
	}
	cur := c.GetBlock(x, y, z)
	bites, ok := CakeBites(cur)
	if !ok || bites != 0 {
		return StateAir, false, res
	}
	candleCakeName, ok := CandleCakeNameForCandle(candleItemName)
	if !ok {
		return StateAir, false, res
	}
	nextState, ok := MakeCandleCakeBlockState(candleCakeName, false)
	if !ok {
		return StateAir, false, res
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, nextState)
	if !applied {
		return StateAir, false, res
	}
	return nextState, true, upd
}

// AddCandleAt increments the `candles` count (1 -> 2 -> 3 -> 4) of an existing
// candle block at (x, y, z) when a player uses the same `candleItemName` on it.
func (c *Cache) AddCandleAt(x, y, z int, candleItemName string) (uint16, bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return StateAir, false, res
	}
	norm := strings.ToLower(strings.TrimSpace(candleItemName))
	if norm != "" && !strings.HasPrefix(norm, "minecraft:") {
		norm = "minecraft:" + norm
	}
	cur := c.GetBlock(x, y, z)
	info, ok := stateByID(cur)
	if !ok || info.Name != norm || !IsCandleBlockName(info.Name) {
		return StateAir, false, res
	}
	count := CandleCount(cur)
	if count >= CandleMaxCount {
		return StateAir, false, res
	}
	nextState, ok := MakeCandleBlockState(
		info.Name,
		count+1,
		info.Properties["lit"] == "true",
		info.Properties["waterlogged"] == "true",
	)
	if !ok {
		return StateAir, false, res
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, nextState)
	if !applied {
		return StateAir, false, res
	}
	return nextState, true, upd
}

// LightCandleOrCandleCakeAt lights an unlit, non-waterlogged candle or
// candle_cake at (x, y, z).
func (c *Cache) LightCandleOrCandleCakeAt(x, y, z int) (uint16, bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return StateAir, false, res
	}
	cur := c.GetBlock(x, y, z)
	if !IsCandleBlock(cur) && !IsCandleCakeBlock(cur) {
		return StateAir, false, res
	}
	if IsCandleOrCandleCakeLit(cur) || isWaterloggedState(cur) {
		return StateAir, false, res
	}
	nextState, ok := withStateProperty(cur, "lit", "true")
	if !ok {
		return StateAir, false, res
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, nextState)
	if !applied {
		return StateAir, false, res
	}
	return nextState, true, upd
}

// ExtinguishCandleOrCandleCakeAt extinguishes a lit candle or candle_cake at (x, y, z).
func (c *Cache) ExtinguishCandleOrCandleCakeAt(x, y, z int) (uint16, bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return StateAir, false, res
	}
	cur := c.GetBlock(x, y, z)
	if !IsCandleOrCandleCakeLit(cur) {
		return StateAir, false, res
	}
	nextState, ok := withStateProperty(cur, "lit", "false")
	if !ok {
		return StateAir, false, res
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, nextState)
	if !applied {
		return StateAir, false, res
	}
	return nextState, true, upd
}

// EatCakeOrCandleCakeAt consumes one slice of `minecraft:cake` or
// `minecraft:*candle_cake` at (x, y, z):
//   - If the block is `*candle_cake`, it returns the dropped candle item ID
//     (e.g. `"minecraft:red_candle"`) and transitions the block to `minecraft:cake[bites=1]`.
//   - If the block is `minecraft:cake[bites=0..5]`, it increments `bites` by 1.
//   - If the block is `minecraft:cake[bites=6]`, it removes the block (`StateAir`).
func (c *Cache) EatCakeOrCandleCakeAt(x, y, z int) (newState uint16, droppedCandle string, ok bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return StateAir, "", false, res
	}
	cur := c.GetBlock(x, y, z)
	if IsCandleCakeBlock(cur) {
		info, _ := stateByID(cur)
		candleID, _ := CandleNameForCandleCake(info.Name)
		next := MakeCakeBlockState(1)
		applied, upd := c.ApplyBlockWithUpdates(x, y, z, next)
		if !applied {
			return StateAir, "", false, res
		}
		return next, candleID, true, upd
	}
	bites, isCake := CakeBites(cur)
	if !isCake {
		return StateAir, "", false, res
	}
	var next uint16 = StateAir
	if bites < CakeMaxBites {
		next = MakeCakeBlockState(bites + 1)
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, next)
	if !applied {
		return StateAir, "", false, res
	}
	return next, "", true, upd
}

// ComposterLevel returns the `level` property (0..8) of a `minecraft:composter`
// state, or (0, false) if state is not `minecraft:composter`.
func ComposterLevel(state uint16) (int, bool) {
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:composter" {
		return 0, false
	}
	lvl, err := strconv.Atoi(info.Properties["level"])
	if err != nil || lvl < 0 || lvl > ComposterReadyLevel {
		return 0, false
	}
	return lvl, true
}

// MakeComposterBlockState returns the `minecraft:composter` state ID for `level` (0..8).
func MakeComposterBlockState(level int) uint16 {
	if level < 0 {
		level = 0
	}
	if level > ComposterReadyLevel {
		level = ComposterReadyLevel
	}
	if id, ok := nameToStateID("minecraft:composter", map[string]string{"level": strconv.Itoa(level)}); ok {
		return id
	}
	id, _ := nameToStateID("minecraft:composter", nil)
	return id
}

func (c *Cache) stepComposterUpdate(x, y, z int, res *BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return
	}
	state := c.GetBlock(x, y, z)
	lvl, ok := ComposterLevel(state)
	if !ok || lvl != ComposterMaxFillLevel {
		return
	}
	readyState := MakeComposterBlockState(ComposterReadyLevel)
	if c.setBlockRecorded(x, y, z, readyState, res) {
		c.propagateRedstoneAround(x, y, z, state, readyState, res)
		c.scheduleAround(x, y, z)
	}
}
