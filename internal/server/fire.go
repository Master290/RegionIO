package server

import (
	"math"
	"math/rand"
	"strings"

	"regionio/internal/world"
)

// TickFireAt advances one fire tick at (x, y, z) in dim and broadcasts any
// resulting block or lighting changes (aging, rain extinguish, burnout, TNT
// priming, or spread) to viewers.
func (s *Server) TickFireAt(dim string, x, y, z int, rng *rand.Rand) world.BlockUpdateResult {
	if s == nil {
		return world.BlockUpdateResult{}
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.BindTNTAndFireMechanics(normDim)
	if cache == nil {
		return world.BlockUpdateResult{}
	}
	res := cache.TickFireAt(x, y, z, rng)
	if len(res.Changes) > 0 {
		s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
	}
	return res
}

// ExtinguishFireAt extinguishes a `minecraft:fire` or `minecraft:soul_fire`
// block at (x, y, z) in dim, broadcasting LevelEvent 1009 and block updates.
func (s *Server) ExtinguishFireAt(dim string, x, y, z int) bool {
	if s == nil {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.BindTNTAndFireMechanics(normDim)
	if cache == nil {
		return false
	}
	ok, res := cache.ExtinguishFireAt(x, y, z)
	if !ok {
		return false
	}
	if len(res.Changes) > 0 {
		s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
	}
	s.BroadcastDimensionChunk(normDim, int32(x>>4), int32(z>>4), PlayLevelEventCB,
		EncodeLevelEventPacket(LevelEventSoundExtinguishFire, x, y, z, 0, false))
	return true
}

// SetDimensionRaining sets whether rain is active in dim (used for fire extinguishment).
func (s *Server) SetDimensionRaining(dim string, raining bool) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	if cache := s.BindTNTAndFireMechanics(normDim); cache != nil {
		cache.SetRaining(raining)
	}
}

// RandomTickLavaInDimension executes one vanilla LavaFluid.randomTick at
// (x, y, z) in dim and broadcasts any resulting fire ignitions or block changes.
func (s *Server) RandomTickLavaInDimension(dim string, x, y, z int, rng *rand.Rand) bool {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.BindTNTAndFireMechanics(normDim)
	if cache == nil {
		return false
	}
	ignited, res := cache.RandomTickLavaAt(x, y, z, rng)
	if len(res.Changes) > 0 {
		s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
	}
	return ignited
}

// TryLightNetherPortalInDimension attempts to light an unlit obsidian Nether
// portal frame at or adjacent to (x, y, z) in dim and broadcasts all resulting
// portal block and lighting updates.
func (s *Server) TryLightNetherPortalInDimension(dim string, x, y, z int) (world.PortalShape, bool) {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return world.PortalShape{}, false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.BindTNTAndFireMechanics(normDim)
	if cache == nil {
		return world.PortalShape{}, false
	}
	shape, res, ok := cache.TryLightNetherPortalAt(x, y, z)
	if !ok {
		return world.PortalShape{}, false
	}
	if len(res.Changes) > 0 {
		s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
	}
	return shape, true
}

// IgniteBlockWithItem handles a player right-clicking a block at (x, y, z) on
// face ("up", "down", "north", "south", "east", "west") with `minecraft:flint_and_steel`
// or `minecraft:fire_charge`:
//   - Primes `minecraft:tnt` if clicked directly (when tntExplodes is true)
//   - Lights an unlit campfire, soul_campfire, candle, or candle_cake
//   - Lights a Nether portal or places `minecraft:fire` / `minecraft:soul_fire`
//     in the adjacent air cell on face
func (s *Server) IgniteBlockWithItem(
	session *PlayerSession,
	dim string,
	x, y, z int,
	face string,
	stack world.ItemStack,
	meta ItemMeta,
) (updatedStack world.ItemStack, updatedMeta ItemMeta, ok bool) {
	updatedStack = stack
	updatedMeta = meta.Clone()
	if s == nil || stack.IsEmpty() {
		return updatedStack, updatedMeta, false
	}

	itemID := canonicalResourceName(stack.ResolvedID())
	if itemID != "minecraft:flint_and_steel" && itemID != "minecraft:fire_charge" {
		return updatedStack, updatedMeta, false
	}

	normDim := world.NormalizeDimension(dim)
	cache := s.BindTNTAndFireMechanics(normDim)
	if cache == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return updatedStack, updatedMeta, false
	}

	targetState := cache.GetBlock(x, y, z)
	targetName := world.BlockName(targetState)
	props := world.BlockProperties(targetState)

	// 1. Clicking directly on a TNT block primes it.
	if targetName == "minecraft:tnt" {
		_, nextStack, nextMeta, primed := s.IgniteTNTBlockWithItem(session, normDim, x, y, z, stack, meta)
		return nextStack, nextMeta, primed
	}

	// 2. Clicking an unlit campfire, soul_campfire, candle, or candle_cake lights it.
	if (targetName == "minecraft:campfire" || targetName == "minecraft:soul_campfire" ||
		strings.HasSuffix(targetName, "_candle") || targetName == "minecraft:candle" ||
		strings.HasSuffix(targetName, "candle_cake")) && props["lit"] == "false" && props["waterlogged"] != "true" {
		if nextSt, valid := world.WithBlockProperty(targetState, "lit", "true"); valid {
			if placed, upd := cache.ApplyBlockWithUpdates(x, y, z, nextSt); placed {
				if len(upd.Changes) > 0 {
					s.broadcastDimensionBlockChanges(normDim, cache, upd.Changes, upd.LightChanged)
				}
				updatedStack, updatedMeta = s.consumeIgniteTool(session, stack, meta, itemID)
				return updatedStack, updatedMeta, true
			}
		}
	}

	// 3. Place fire or light a Nether portal in the adjacent cell along face.
	dx, dy, dz := DispenserFacingVector(face)
	if dx == 0 && dy == 0 && dz == 0 {
		dy = 1
	}
	fx, fy, fz := x+dx, y+dy, z+dz
	if fy < world.MinY || fy >= world.MinY+world.WorldHeight {
		return updatedStack, updatedMeta, false
	}
	frontState := cache.GetBlock(fx, fy, fz)
	frontName := world.BlockName(frontState)
	if frontState != world.StateAir && frontName != "minecraft:air" && frontName != "minecraft:cave_air" && frontName != "minecraft:void_air" {
		return updatedStack, updatedMeta, false
	}

	if _, lit := s.LightPortalAt(normDim, fx, fy, fz); lit {
		updatedStack, updatedMeta = s.consumeIgniteTool(session, stack, meta, itemID)
		return updatedStack, updatedMeta, true
	}

	fireState, canPlace := cache.ResolveFireStateForPlacement(fx, fy, fz, 0)
	if !canPlace || fireState == world.StateAir {
		return updatedStack, updatedMeta, false
	}
	placed, upd := cache.ApplyBlockWithUpdates(fx, fy, fz, fireState)
	if !placed {
		return updatedStack, updatedMeta, false
	}
	if len(upd.Changes) > 0 {
		s.broadcastDimensionBlockChanges(normDim, cache, upd.Changes, upd.LightChanged)
	}
	soundName := "minecraft:item.flintandsteel.use"
	if itemID == "minecraft:fire_charge" {
		soundName = "minecraft:item.firecharge.use"
	}
	s.BroadcastDimensionChunk(normDim, int32(fx>>4), int32(fz>>4), PlaySoundPacketID,
		EncodeSoundPacket(soundName, SoundSourceBlocks, float64(fx)+0.5, float64(fy)+0.5, float64(fz)+0.5, 1.0, 1.0, 0))
	updatedStack, updatedMeta = s.consumeIgniteTool(session, stack, meta, itemID)
	return updatedStack, updatedMeta, true
}

func (s *Server) consumeIgniteTool(session *PlayerSession, stack world.ItemStack, meta ItemMeta, itemID string) (world.ItemStack, ItemMeta) {
	outStack := stack
	outMeta := meta.Clone()
	if session != nil && session.Snapshot().GameMode == GameModeCreative {
		return outStack, outMeta
	}
	if itemID == "minecraft:flint_and_steel" {
		ds := s.dispenserState()
		ds.mu.Lock()
		unbreaking := outMeta.EnchantmentLevel(EnchantUnbreaking)
		wear := 1
		if unbreaking > 0 && ds.rng.Intn(unbreaking+1) != 0 {
			wear = 0
		}
		ds.mu.Unlock()
		if wear > 0 {
			outMeta.Damage += int32(wear)
			if outMeta.Damage >= FlintAndSteelMaxDurability {
				return world.ItemStack{}, ItemMeta{}
			}
		}
		return outStack, outMeta
	}
	if stack.Count <= 1 {
		return world.ItemStack{}, ItemMeta{}
	}
	return world.NewItemStack(stack.ResolvedItemID(), stack.Count-1), outMeta
}

// ApplyPlayerFireBlockContactDamage checks whether session is standing inside a
// `minecraft:fire` (1.0 damage) or `minecraft:soul_fire` (2.0 damage) block and
// applies fire damage (respecting gamerule fireDamage and Fire Resistance).
func (s *Server) ApplyPlayerFireBlockContactDamage(session *PlayerSession) float32 {
	if s == nil || session == nil || session.IsDead() || !s.FireDamage() {
		return 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return 0
	}
	if session.HasFireResistance() {
		return 0
	}
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return 0
	}
	bx := int(math.Floor(snap.X))
	by := int(math.Floor(snap.Y))
	bz := int(math.Floor(snap.Z))
	if by < world.MinY || by >= world.MinY+world.WorldHeight {
		return 0
	}
	dmg := world.FireDamageForBlock(cache.GetBlock(bx, by, bz))
	if dmg <= 0 {
		return 0
	}
	prot := EnchantmentArmorProtection(PlayerArmorMetas(session), "in_fire")
	def, tough := session.ArmorDefense()
	afterArmor := DamageAfterArmor(dmg, def, tough) * (1.0 - prot)
	if afterArmor <= 0 {
		return 0
	}
	s.DamagePlayerWithEffects(session, afterArmor, "in_fire")
	return afterArmor
}
