package server

import (
	"strconv"
	"strings"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Vanilla 26.1.2 constants and sound events for cake, candles, candle cakes, and composter.
const (
	// CakeSliceNutrition and CakeSliceSaturation are the food points (2) and
	// saturation (0.4) restored by eating 1 slice of cake or candle_cake.
	CakeSliceNutrition  int32   = 2
	CakeSliceSaturation float32 = 0.4

	SoundCakeAddCandle        = "minecraft:block.cake.add_candle"
	SoundCandlePlace          = "minecraft:block.candle.place"
	SoundCandleExtinguish     = "minecraft:block.candle.extinguish"
	SoundComposterEmpty       = "minecraft:block.composter.empty"
	SoundComposterFill        = "minecraft:block.composter.fill"
	SoundComposterFillSuccess = "minecraft:block.composter.fill_success"
	SoundComposterReady       = "minecraft:block.composter.ready"
)

// CanPlayerEatCake reports whether session can eat a slice of cake or candle_cake
// (true when alive, not in spectator mode, and either hungry `foodLevel < 20` or in creative mode).
func CanPlayerEatCake(session *PlayerSession) bool {
	if session == nil || session.IsDead() {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return false
	}
	return snap.GameMode == GameModeCreative || session.FoodLevel() < MaxFoodLevel
}

func addPlayerCakeNutrition(session *PlayerSession) {
	if session == nil {
		return
	}
	session.mu.Lock()
	newFood := session.foodLevel + CakeSliceNutrition
	if newFood > MaxFoodLevel {
		newFood = MaxFoodLevel
	}
	newSat := session.foodSaturation + CakeSliceSaturation
	if newSat > float32(newFood) {
		newSat = float32(newFood)
	}
	session.foodLevel = newFood
	session.foodSaturation = newSat
	health := session.health
	send := session.send
	session.mu.Unlock()

	if send != nil {
		_ = send(protocol.PlaySetHealth, EncodeSetHealth(health, newFood, newSat))
	}
}

func consumeOneHandItemOrDamageIgniter(s *Server, session *PlayerSession, slot int, held world.ItemStack, isFlintAndSteel bool) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots || held.IsEmpty() {
		return
	}
	if session.Snapshot().GameMode == GameModeCreative {
		return
	}
	if isFlintAndSteel {
		if broken := s.DamagePlayerItem(session, slot, 1); broken {
			return
		}
		return
	}
	nextCount := held.Count - 1
	var next world.ItemStack
	if nextCount > 0 {
		next = held
		next.Count = nextCount
	}
	session.SetInventorySlot(slot, next)
	session.mu.Lock()
	send := session.send
	session.mu.Unlock()
	if send != nil {
		stID := session.NextContainerStateID()
		_ = send(protocol.PlayContainerSetSlot, encodeContainerSetSlotPacket(stID, slot, next))
	}
}

func (s *Server) broadcastWorldUpdateResult(_ string, upd world.BlockUpdateResult) {
	if s == nil {
		return
	}
	for _, ch := range upd.Changes {
		s.broadcastBlockAndLightUpdate(ch.Pos.X, ch.Pos.Y, ch.Pos.Z, ch.NewState, ch.LightChanged)
	}
}

// InteractWithCakeCandleOrComposter handles player right-click interactions on
// `minecraft:cake`, all 17 `minecraft:*candle_cake` blocks, all 17
// `minecraft:*candle` blocks, and `minecraft:composter`.
func (s *Server) InteractWithCakeCandleOrComposter(
	session *PlayerSession,
	dim string,
	x, y, z int,
	hand int32,
) (action string, ok bool) {
	if s == nil {
		return "", false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil {
		return "", false
	}
	state := cache.GetBlock(x, y, z)
	if world.IsCakeBlock(state) || world.IsCandleCakeBlock(state) {
		return s.InteractWithCakeOrCandleCake(session, normDim, x, y, z, hand)
	}
	if world.IsCandleBlock(state) {
		return s.InteractWithCandleBlock(session, normDim, x, y, z, hand)
	}
	if world.BlockName(state) == "minecraft:composter" {
		act, _, handled := s.InteractWithComposter(session, normDim, x, y, z, hand)
		return act, handled
	}
	return "", false
}

// InteractWithCakeOrCandleCake handles player right-click on `minecraft:cake`
// or any of the 17 `minecraft:*candle_cake` blocks:
//   - Placing any candle item on `minecraft:cake[bites=0]` converts it to
//     `minecraft:*candle_cake[lit=false]` and consumes 1 candle (`action = "place_candle"`).
//   - Using `minecraft:flint_and_steel` or `minecraft:fire_charge` on an unlit
//     `*candle_cake` lights it (`lit=true`, `action = "light"`).
//   - Right-clicking a lit `*candle_cake` with an empty hand extinguishes it
//     (`lit=false`, `action = "extinguish"`).
//   - Otherwise, if `CanPlayerEatCake(session)` is true, eats 1 slice (+2 food,
//     +0.4 saturation, `action = "eat"`). Eating a `*candle_cake` drops 1
//     matching candle item and leaves `minecraft:cake[bites=1]`.
func (s *Server) InteractWithCakeOrCandleCake(
	session *PlayerSession,
	dim string,
	x, y, z int,
	hand int32,
) (action string, ok bool) {
	if s == nil || session == nil || session.IsDead() {
		return "", false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil {
		return "", false
	}
	cur := cache.GetBlock(x, y, z)
	if !world.IsCakeBlock(cur) && !world.IsCandleCakeBlock(cur) {
		return "", false
	}

	slot, held := session.handSlotAndStack(hand)
	heldID := ""
	if !held.IsEmpty() {
		heldID = strings.ToLower(strings.TrimSpace(held.ResolvedID()))
		if heldID != "" && !strings.HasPrefix(heldID, "minecraft:") {
			heldID = "minecraft:" + heldID
		}
	}
	cx, cz := int32(x>>4), int32(z>>4)

	// 1. Place candle on uneaten cake[bites=0].
	if world.IsCakeBlock(cur) && world.IsCandleBlockName(heldID) {
		if _, placed, upd := cache.PlaceCandleOnCakeAt(x, y, z, heldID); placed {
			consumeOneHandItemOrDamageIgniter(s, session, slot, held, false)
			s.broadcastWorldUpdateResult(normDim, upd)
			s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket(SoundCakeAddCandle, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
			s.EmitGameEvent(normDim, GameEventBlockChange, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, VibrationSource{Player: session})
			return "place_candle", true
		}
	}

	// 2. Light or extinguish a candle_cake.
	if world.IsCandleCakeBlock(cur) {
		isLit := world.IsCandleOrCandleCakeLit(cur)
		if !isLit && (heldID == "minecraft:flint_and_steel" || heldID == "minecraft:fire_charge") {
			if _, litOK, upd := cache.LightCandleOrCandleCakeAt(x, y, z); litOK {
				isFlint := heldID == "minecraft:flint_and_steel"
				consumeOneHandItemOrDamageIgniter(s, session, slot, held, isFlint)
				s.broadcastWorldUpdateResult(normDim, upd)
				snd := "minecraft:item.flintandsteel.use"
				if !isFlint {
					snd = "minecraft:item.firecharge.use"
				}
				s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
					EncodeSoundPacket(snd, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
				s.EmitGameEvent(normDim, GameEventBlockChange, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, VibrationSource{Player: session})
				return "light", true
			}
		}
		if isLit && held.IsEmpty() {
			if _, extOK, upd := cache.ExtinguishCandleOrCandleCakeAt(x, y, z); extOK {
				s.broadcastWorldUpdateResult(normDim, upd)
				s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
					EncodeSoundPacket(SoundCandleExtinguish, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
				s.EmitGameEvent(normDim, GameEventBlockChange, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, VibrationSource{Player: session})
				return "extinguish", true
			}
		}
	}

	// 3. Eat a slice of cake or candle_cake.
	if !CanPlayerEatCake(session) {
		return "", false
	}
	_, droppedCandle, ate, upd := cache.EatCakeOrCandleCakeAt(x, y, z)
	if !ate {
		return "", false
	}
	addPlayerCakeNutrition(session)
	s.broadcastWorldUpdateResult(normDim, upd)
	if droppedCandle != "" {
		s.SpawnItemEntity(float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, world.NewNamedItemStack(droppedCandle, 1))
	}
	s.EmitGameEvent(normDim, GameEventEat, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, VibrationSource{Player: session})
	return "eat", true
}

// InteractWithCandleBlock handles player right-click on any of the 17
// `minecraft:*candle` blocks:
//   - Holding the same candle variant when `candles < 4` increments `candles`
//     (`action = "add_candle"`).
//   - Holding `minecraft:flint_and_steel` or `minecraft:fire_charge` on an unlit,
//     non-waterlogged candle lights it (`action = "light"`).
//   - Right-clicking a lit candle with an empty hand extinguishes it
//     (`action = "extinguish"`).
func (s *Server) InteractWithCandleBlock(
	session *PlayerSession,
	dim string,
	x, y, z int,
	hand int32,
) (action string, ok bool) {
	if s == nil || session == nil || session.IsDead() {
		return "", false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil {
		return "", false
	}
	cur := cache.GetBlock(x, y, z)
	if !world.IsCandleBlock(cur) {
		return "", false
	}
	slot, held := session.handSlotAndStack(hand)
	heldID := ""
	if !held.IsEmpty() {
		heldID = strings.ToLower(strings.TrimSpace(held.ResolvedID()))
		if heldID != "" && !strings.HasPrefix(heldID, "minecraft:") {
			heldID = "minecraft:" + heldID
		}
	}
	cx, cz := int32(x>>4), int32(z>>4)

	// 1. Add another candle of the same color (up to 4).
	if heldID != "" && heldID == world.BlockName(cur) && world.CandleCount(cur) < world.CandleMaxCount {
		if _, added, upd := cache.AddCandleAt(x, y, z, heldID); added {
			consumeOneHandItemOrDamageIgniter(s, session, slot, held, false)
			s.broadcastWorldUpdateResult(normDim, upd)
			s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket(SoundCandlePlace, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
			s.EmitGameEvent(normDim, GameEventBlockChange, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, VibrationSource{Player: session})
			return "add_candle", true
		}
	}

	// 2. Light an unlit, non-waterlogged candle with flint_and_steel or fire_charge.
	isLit := world.IsCandleOrCandleCakeLit(cur)
	if !isLit && (heldID == "minecraft:flint_and_steel" || heldID == "minecraft:fire_charge") {
		if _, litOK, upd := cache.LightCandleOrCandleCakeAt(x, y, z); litOK {
			isFlint := heldID == "minecraft:flint_and_steel"
			consumeOneHandItemOrDamageIgniter(s, session, slot, held, isFlint)
			s.broadcastWorldUpdateResult(normDim, upd)
			snd := "minecraft:item.flintandsteel.use"
			if !isFlint {
				snd = "minecraft:item.firecharge.use"
			}
			s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket(snd, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
			s.EmitGameEvent(normDim, GameEventBlockChange, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, VibrationSource{Player: session})
			return "light", true
		}
	}

	// 3. Extinguish a lit candle with an empty hand.
	if isLit && held.IsEmpty() {
		if _, extOK, upd := cache.ExtinguishCandleOrCandleCakeAt(x, y, z); extOK {
			s.broadcastWorldUpdateResult(normDim, upd)
			s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket(SoundCandleExtinguish, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
			s.EmitGameEvent(normDim, GameEventBlockChange, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, VibrationSource{Player: session})
			return "extinguish", true
		}
	}
	return "", false
}

// InteractWithComposter handles a player right-clicking a `minecraft:composter`
// at (x, y, z) in dim:
//   - If `level == 8`: harvests 1 `minecraft:bone_meal` (spawning the item entity
//     above the composter), resets the composter to `level=0`, and returns
//     `("harvest", boneMealStack, true)`.
//   - If `level < 7` and the player holds a compostable item
//     (`CompostChanceForItem(id) > 0`): consumes 1 item (unless creative), rolls
//     the compost chance (`level == 0` always succeeds), increments `level` on
//     success, and when `level` reaches 7 schedules the 20-tick maturation into
//     `level=8`.
func (s *Server) InteractWithComposter(
	session *PlayerSession,
	dim string,
	x, y, z int,
	hand int32,
) (action string, harvested world.ItemStack, ok bool) {
	if s == nil || session == nil || session.IsDead() {
		return "", world.ItemStack{}, false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil {
		return "", world.ItemStack{}, false
	}
	state := cache.GetBlock(x, y, z)
	lvl, isComposter := world.ComposterLevel(state)
	if !isComposter {
		return "", world.ItemStack{}, false
	}
	cx, cz := int32(x>>4), int32(z>>4)

	// 1. Harvest bone_meal when level == 8.
	if lvl == world.ComposterReadyLevel {
		nextSt := world.MakeComposterBlockState(0)
		_, upd := cache.ApplyBlockWithUpdates(x, y, z, nextSt)
		s.broadcastWorldUpdateResult(normDim, upd)
		boneMeal := world.NewNamedItemStack("minecraft:bone_meal", 1)
		s.SpawnItemEntity(float64(x)+0.5, float64(y)+0.7, float64(z)+0.5, boneMeal)
		s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundComposterEmpty, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
		s.EmitGameEvent(normDim, GameEventBlockChange, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, VibrationSource{Player: session})
		return "harvest", boneMeal, true
	}

	if lvl >= world.ComposterMaxFillLevel {
		return "", world.ItemStack{}, false
	}

	slot, held := session.handSlotAndStack(hand)
	if held.IsEmpty() {
		return "", world.ItemStack{}, false
	}
	chance := CompostChanceForItem(held.ResolvedID())
	if chance <= 0 {
		return "", world.ItemStack{}, false
	}

	consumeOneHandItemOrDamageIgniter(s, session, slot, held, false)

	ds := s.dispenserState()
	ds.mu.Lock()
	roll := ds.rng.Float64()
	ds.mu.Unlock()

	nextLvl := lvl
	if lvl == 0 || roll < float64(chance) {
		nextLvl = lvl + 1
	}
	if nextLvl > lvl {
		nextSt := world.MakeComposterBlockState(nextLvl)
		_, upd := cache.ApplyBlockWithUpdates(x, y, z, nextSt)
		s.broadcastWorldUpdateResult(normDim, upd)
		if nextLvl == world.ComposterMaxFillLevel {
			hs := s.hopperState()
			hs.mu.Lock()
			hs.composterPending[hopperBlockKey{dim: normDim, x: x, y: y, z: z}] = 1
			hs.mu.Unlock()
		}
		s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundComposterFillSuccess, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
		s.BroadcastDimensionChunk(normDim, cx, cz, PlayLevelEventCB,
			EncodeLevelEventPacket(LevelEventComposterFill, x, y, z, 1, false))
		s.EmitGameEvent(normDim, GameEventBlockChange, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, VibrationSource{Player: session})
		_ = strconv.Itoa(nextLvl)
		return "fill_success", world.ItemStack{}, true
	}

	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundComposterFill, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	s.BroadcastDimensionChunk(normDim, cx, cz, PlayLevelEventCB,
		EncodeLevelEventPacket(LevelEventComposterFill, x, y, z, 0, false))
	return "fill_fail", world.ItemStack{}, true
}
