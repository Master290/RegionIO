package server

import (
	"math"
	"math/rand"
	"strings"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// ExplosionSourceCategory classifies an explosion for gamerule drop-decay and griefing checks.
type ExplosionSourceCategory uint8

const (
	// ExplosionSourceTNT represents TNT blocks and TNT minecarts (uses tntExplosionDropDecay, default false).
	ExplosionSourceTNT ExplosionSourceCategory = iota
	// ExplosionSourceMob represents creeper, wither, ghast fireball, etc. (uses mobExplosionDropDecay, default true, and mobGriefing).
	ExplosionSourceMob
	// ExplosionSourceBlock represents beds in Nether/End, respawn anchors outside Nether, and end crystals (uses blockExplosionDropDecay, default true).
	ExplosionSourceBlock
)

// LevelEventParticlesExplosion is the LevelEvent ID for explosion emitter particles.
const LevelEventParticlesExplosion int32 = 2008

// ExplosionOptions configures a server-side explosion detonation.
type ExplosionOptions struct {
	Dimension        string
	X, Y, Z          float64
	Radius           float64
	Category         ExplosionSourceCategory
	DamageCause      string
	SourceEntityID   int32
	SourceMobType    string
	Activator        *PlayerSession
	CausesFire       bool
	BreakBlocks      *bool // nil = automatic (true for TNT/Block, s.MobGriefing() for Mob)
	Rng              *rand.Rand
}

// ExplosionResult summarizes the effects of a detonated explosion.
type ExplosionResult struct {
	DestroyedBlocks  []world.BlockPos
	DroppedItems     []world.ItemStack
	ChainTNTEntities []int32
	FirePlaced       []world.BlockPos
	PlayersDamaged   int
	MobsDamaged      int
}

// Convenience gamerule accessors for explosions and fire spread.

// TNTExplodes returns the current value of the tntExplodes gamerule (default true).
func (s *Server) TNTExplodes() bool {
	if s == nil {
		return true
	}
	return s.GameRuleBool("tntExplodes")
}

// SetTNTExplodes updates the tntExplodes gamerule and synchronizes all dimension caches.
func (s *Server) SetTNTExplodes(v bool) {
	if s == nil {
		return
	}
	s.SetGameRuleBool("tntExplodes", v)
	s.syncTNTAndFireRulesToCaches()
}

// DoFireTick returns the current value of the doFireTick gamerule (default true).
func (s *Server) DoFireTick() bool {
	if s == nil {
		return true
	}
	return s.GameRuleBool("doFireTick")
}

// SetDoFireTick updates the doFireTick gamerule and synchronizes all dimension caches.
func (s *Server) SetDoFireTick(v bool) {
	if s == nil {
		return
	}
	s.SetGameRuleBool("doFireTick", v)
	s.syncTNTAndFireRulesToCaches()
}

// BlockExplosionDropDecay returns the blockExplosionDropDecay gamerule (default true).
func (s *Server) BlockExplosionDropDecay() bool {
	if s == nil {
		return true
	}
	return s.GameRuleBool("blockExplosionDropDecay")
}

// SetBlockExplosionDropDecay updates the blockExplosionDropDecay gamerule.
func (s *Server) SetBlockExplosionDropDecay(v bool) {
	if s == nil {
		return
	}
	s.SetGameRuleBool("blockExplosionDropDecay", v)
}

// MobExplosionDropDecay returns the mobExplosionDropDecay gamerule (default true).
func (s *Server) MobExplosionDropDecay() bool {
	if s == nil {
		return true
	}
	return s.GameRuleBool("mobExplosionDropDecay")
}

// SetMobExplosionDropDecay updates the mobExplosionDropDecay gamerule.
func (s *Server) SetMobExplosionDropDecay(v bool) {
	if s == nil {
		return
	}
	s.SetGameRuleBool("mobExplosionDropDecay", v)
}

// TNTExplosionDropDecay returns the tntExplosionDropDecay gamerule (default false).
func (s *Server) TNTExplosionDropDecay() bool {
	if s == nil {
		return false
	}
	return s.GameRuleBool("tntExplosionDropDecay")
}

// SetTNTExplosionDropDecay updates the tntExplosionDropDecay gamerule.
func (s *Server) SetTNTExplosionDropDecay(v bool) {
	if s == nil {
		return
	}
	s.SetGameRuleBool("tntExplosionDropDecay", v)
}

func (s *Server) syncTNTAndFireRulesToCaches() {
	if s == nil {
		return
	}
	tntExplodes := s.TNTExplodes()
	doFireTick := s.DoFireTick()
	for _, dim := range []string{world.DimensionOverworld, world.DimensionNether, world.DimensionEnd} {
		if c := s.DimensionChunks(dim); c != nil {
			c.SetTNTExplodes(tntExplodes)
			c.SetDoFireTick(doFireTick)
			c.SetFireDimension(dim)
		}
	}
}

// ShouldExplosionDecayDrops reports whether block drops from an explosion of
// category are subject to 1/radius probability decay under the current gamerules:
//   - ExplosionSourceTNT   -> tntExplosionDropDecay (default false -> 100% drops)
//   - ExplosionSourceMob   -> mobExplosionDropDecay (default true  -> 1/radius drops)
//   - ExplosionSourceBlock -> blockExplosionDropDecay (default true -> 1/radius drops)
func (s *Server) ShouldExplosionDecayDrops(category ExplosionSourceCategory) bool {
	switch category {
	case ExplosionSourceTNT:
		return s.TNTExplosionDropDecay()
	case ExplosionSourceMob:
		return s.MobExplosionDropDecay()
	case ExplosionSourceBlock:
		return s.BlockExplosionDropDecay()
	default:
		return true
	}
}

// RollExplosionBlockDrop determines whether a block of state destroyed by an
// explosion of radius drops its item when decayEnabled is active.
// Blocks that survive explosions in vanilla (shulker boxes, beacons, conduits,
// dragon eggs, heads) always drop. When decayEnabled is false, all eligible
// blocks drop with 100% probability. When decayEnabled is true, each block
// drops with probability 1.0 / radius.
func RollExplosionBlockDrop(state uint16, radius float64, decayEnabled bool, rng *rand.Rand) bool {
	if world.AlwaysDropsFromExplosion(state) {
		return true
	}
	if !decayEnabled || radius <= 1.0 {
		return true
	}
	if rng == nil {
		return false
	}
	return rng.Float64() <= (1.0 / radius)
}

// PlayerArmorMetas returns the ItemMeta slice for the 4 armor slots (head=5, chest=6, legs=7, feet=8)
// currently worn by session.
func PlayerArmorMetas(session *PlayerSession) []ItemMeta {
	if session == nil {
		return nil
	}
	var metas []ItemMeta
	for _, slot := range []int{5, 6, 7, 8} {
		if !session.InventorySlot(slot).IsEmpty() {
			metas = append(metas, GetSlotItemMeta(session, slot))
		}
	}
	return metas
}

// PlayerBlastProtectionReduction returns the fractional explosion damage reduction
// in [0.0, 0.80] from Protection and Blast Protection enchantments on session's armor.
func PlayerBlastProtectionReduction(session *PlayerSession) float32 {
	metas := PlayerArmorMetas(session)
	if len(metas) == 0 {
		return 0
	}
	return EnchantmentArmorProtection(metas, "explosion")
}

// PlayerMaxBlastProtectionLevel returns the highest Blast Protection level (0..4+)
// across session's worn armor pieces (used for 15%-per-level knockback reduction).
func PlayerMaxBlastProtectionLevel(session *PlayerSession) int {
	maxLvl := 0
	for _, m := range PlayerArmorMetas(session) {
		if lvl := m.EnchantmentLevel(EnchantBlastProtection); lvl > maxLvl {
			maxLvl = lvl
		}
	}
	return maxLvl
}

// DetonateExplosion executes a full vanilla 26.1.2 explosion in opts.Dimension:
//   - Casts 1352 rays through the world cache, respecting block blast resistances
//     and underwater attenuation
//   - Drops block items according to blockExplosionDropDecay / mobExplosionDropDecay /
//     tntExplosionDropDecay (and doTileDrops), plus container contents
//   - Triggers chain-detonation of any destroyed minecraft:tnt blocks with a
//     shortened 10..29 tick fuse (when tntExplodes is true)
//   - Ignites fire on 1/3 of exposed solid surfaces when opts.CausesFire is true
//   - Computes raycast exposure (shielding) for nearby players and mobs, applying
//     distance- and exposure-scaled damage and knockback reduced by Blast Protection
func (s *Server) DetonateExplosion(opts ExplosionOptions) ExplosionResult {
	var result ExplosionResult
	if s == nil || opts.Radius <= 0 {
		return result
	}
	dim := world.NormalizeDimension(opts.Dimension)
	cache := s.BindTNTAndFireMechanics(dim)

	rng := opts.Rng
	if rng == nil {
		ds := s.dispenserState()
		ds.mu.Lock()
		seed := ds.rng.Int63()
		ds.mu.Unlock()
		rng = rand.New(rand.NewSource(seed))
	}

	canGrief := true
	if opts.BreakBlocks != nil {
		canGrief = *opts.BreakBlocks
	} else if opts.Category == ExplosionSourceMob {
		canGrief = s.MobGriefing() && s.CreeperBlockGriefing()
	}

	cause := opts.DamageCause
	if cause == "" {
		switch opts.Category {
		case ExplosionSourceTNT:
			cause = "explosion"
		case ExplosionSourceMob:
			if opts.SourceMobType != "" {
				cause = opts.SourceMobType
			} else {
				cause = "explosion"
			}
		case ExplosionSourceBlock:
			cause = "bad_respawn_point"
		default:
			cause = "explosion"
		}
	}

	cx := int32(int64(math.Floor(opts.X)) >> 4)
	cz := int32(int64(math.Floor(opts.Z)) >> 4)
	soundSrc := SoundSourceBlocks
	if opts.Category == ExplosionSourceMob {
		soundSrc = SoundSourceHostile
	}
	s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.generic.explode", soundSrc, opts.X, opts.Y, opts.Z, 4.0, 0.8, 0))
	s.BroadcastDimensionChunk(dim, cx, cz, PlayLevelEventCB,
		EncodeLevelEventPacket(LevelEventParticlesExplosion, int(math.Floor(opts.X)), int(math.Floor(opts.Y)), int(math.Floor(opts.Z)), 0, false))
	s.EmitVibration(VibrationEvent{
		Dimension:      dim,
		Type:           GameEventExplode,
		X:              opts.X,
		Y:              opts.Y,
		Z:              opts.Z,
		Player:         opts.Activator,
		SourceEntityID: opts.SourceEntityID,
	})

	// 1. Damage entities (players, mobs, decorations, items, and primed TNT) BEFORE
	// removing blocks so raycast exposure reflects the shielding blocks that absorbed the blast!
	s.applyExplosionEntityEffects(dim, cache, opts, cause, &result)

	// 2. Raycast & destroy blocks, spawn block drops & chain TNT, and ignite fire.
	if cache != nil && canGrief {
		rayRes := cache.SimulateExplosionRays(opts.X, opts.Y, opts.Z, opts.Radius, opts.CausesFire, true, rng)
		decayEnabled := s.ShouldExplosionDecayDrops(opts.Category)
		tileDrops := s.DoTileDrops()
		tntExplodes := s.TNTExplodes()

		var allChanges []world.BlockChange
		var allLight []world.ChunkPos
		seenLight := make(map[world.ChunkPos]bool)
		appendUpdate := func(upd world.BlockUpdateResult) {
			allChanges = append(allChanges, upd.Changes...)
			for _, lc := range upd.LightChanged {
				if !seenLight[lc] {
					seenLight[lc] = true
					allLight = append(allLight, lc)
				}
			}
		}

		for _, pos := range rayRes.DestroyedBlocks {
			st := cache.GetBlock(pos.X, pos.Y, pos.Z)
			if st == world.StateAir {
				continue
			}

			// Chain-detonate TNT blocks caught in the blast!
			if world.IsTNTBlock(st) {
				if ok, upd := cache.BreakBlockWithUpdates(pos.X, pos.Y, pos.Z); ok {
					result.DestroyedBlocks = append(result.DestroyedBlocks, pos)
					appendUpdate(upd)
					if tntExplodes {
						fuse := world.RollChainTNTFuseTicks(rng)
						entID := s.SpawnPrimedTNTWithFuse(dim, float64(pos.X)+0.5, float64(pos.Y), float64(pos.Z)+0.5, fuse, opts.Activator)
						if entID != 0 {
							result.ChainTNTEntities = append(result.ChainTNTEntities, entID)
						}
					}
				}
				continue
			}

			// Drop any stored container / block entity items before breaking the block.
			containerDrops := s.collectAndClearBlockEntityDrops(dim, pos.X, pos.Y, pos.Z, st)
			for _, stack := range containerDrops {
				if !stack.IsEmpty() {
					result.DroppedItems = append(result.DroppedItems, stack)
					s.SpawnItemEntityInDimension(dim, float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, stack)
				}
			}

			// Roll block drop according to drop decay rules.
			if tileDrops && !isUpperHalfOfDoubleBlock(st) && RollExplosionBlockDrop(st, opts.Radius, decayEnabled, rng) {
				if dropStack, ok := world.BlockDropItem(st); ok && !dropStack.IsEmpty() {
					result.DroppedItems = append(result.DroppedItems, dropStack)
					s.SpawnItemEntityInDimension(dim, float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, dropStack)
				}
			}

			s.OnWorkstationBlockBroken(dim, pos.X, pos.Y, pos.Z)
			if ok, upd := cache.BreakBlockWithUpdates(pos.X, pos.Y, pos.Z); ok {
				result.DestroyedBlocks = append(result.DestroyedBlocks, pos)
				appendUpdate(upd)
			}
		}

		// Place fire on 1/3 of destroyed cells resting on solid blocks if CausesFire is enabled.
		if opts.CausesFire {
			for _, fpos := range rayRes.FirePositions {
				if cache.GetBlock(fpos.X, fpos.Y, fpos.Z) != world.StateAir {
					continue
				}
				if fireSt, ok := cache.ResolveFireStateForPlacement(fpos.X, fpos.Y, fpos.Z, 0); ok {
					if placed, upd := cache.ApplyBlockWithUpdates(fpos.X, fpos.Y, fpos.Z, fireSt); placed {
						result.FirePlaced = append(result.FirePlaced, fpos)
						appendUpdate(upd)
					}
				}
			}
		}

		if len(allChanges) > 0 {
			s.broadcastDimensionBlockChanges(dim, cache, allChanges, allLight)
		}
	}

	return result
}

func isUpperHalfOfDoubleBlock(state uint16) bool {
	props := world.BlockProperties(state)
	return props["half"] == "upper"
}

func (s *Server) collectAndClearBlockEntityDrops(dim string, x, y, z int, state uint16) []world.ItemStack {
	name := world.BlockName(state)
	var drops []world.ItemStack

	switch {
	case name == "minecraft:dispenser" || name == "minecraft:dropper":
		slots := s.DispenserSlotsInDimension(dim, x, y, z)
		for _, it := range slots {
			if !it.IsEmpty() {
				drops = append(drops, it)
			}
		}
		ds := s.dispenserState()
		ds.mu.Lock()
		delete(ds.blocks, dispenserBlockKey{dim: normalizeDispenserDim(dim), x: x, y: y, z: z})
		ds.mu.Unlock()

	case name == "minecraft:hopper":
		s.BreakHopperAndDropItems(dim, x, y, z)

	case name == "minecraft:crafter":
		cDrops, _, _ := s.RemoveCrafterAt(dim, x, y, z)
		for _, it := range cDrops {
			if !it.IsEmpty() {
				drops = append(drops, it)
			}
		}

	case name == "minecraft:chiseled_bookshelf":
		bDrops, _, _ := s.RemoveChiseledBookshelfAt(dim, x, y, z)
		for _, it := range bDrops {
			if !it.IsEmpty() {
				drops = append(drops, it)
			}
		}

	case name == "minecraft:decorated_pot":
		// When destroyed by an explosion, decorated pot shatters into its stored item + sherds/bricks.
		pDrops, _, _, _, _ := s.BreakDecoratedPotAt(dim, x, y, z, true, false)
		for _, it := range pDrops {
			if !it.IsEmpty() {
				drops = append(drops, it)
			}
		}

	case name == "minecraft:campfire" || name == "minecraft:soul_campfire":
		for _, it := range s.BreakCampfireAt(dim, x, y, z, false) {
			if !it.IsEmpty() {
				drops = append(drops, it)
			}
		}

	case name == "minecraft:furnace" || name == "minecraft:smoker" || name == "minecraft:blast_furnace":
		if dim == world.DimensionOverworld {
			s.DropFurnaceContents(x, y, z)
		}

	case name == "minecraft:chest" || name == "minecraft:trapped_chest" || name == "minecraft:barrel":
		if dim == world.DimensionOverworld {
			slots := s.ChestSlots(x, y, z)
			for i, it := range slots {
				if !it.IsEmpty() {
					drops = append(drops, it)
					s.SetChestSlot(x, y, z, i, world.ItemStack{})
				}
			}
		}
	}

	return drops
}

func (s *Server) applyExplosionEntityEffects(dim string, cache *world.Cache, opts ExplosionOptions, cause string, result *ExplosionResult) {
	diameter := opts.Radius * 2.0
	if diameter <= 0 {
		return
	}

	// 1. Damage decorations (Armor Stands, Item Frames, Paintings) in radius.
	s.DamageDecorationsInRadius(dim, opts.X, opts.Y, opts.Z, diameter, float32(opts.Radius*7.0), "explosion")

	// 2. Damage and knock back players in dim within 2*Radius.
	var sessions []*PlayerSession
	s.playersMu.RLock()
	for _, sess := range s.players {
		if sess != nil && !sess.IsDead() {
			sessions = append(sessions, sess)
		}
	}
	s.playersMu.RUnlock()
	if opts.Activator != nil && !opts.Activator.IsDead() {
		found := false
		for _, sess := range sessions {
			if sess == opts.Activator {
				found = true
				break
			}
		}
		if !found {
			sessions = append(sessions, opts.Activator)
		}
	}

	for _, sess := range sessions {
		snap := sess.Snapshot()
		if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator || snap.Health <= 0 {
			continue
		}
		if world.NormalizeDimension(snap.Dimension) != dim {
			continue
		}
		box := world.PlayerAABB(snap.X, snap.Y, snap.Z)
		dx := snap.X - opts.X
		dy := snap.Y - opts.Y
		dz := snap.Z - opts.Z
		if math.Sqrt(dx*dx+dy*dy+dz*dz) >= diameter {
			continue
		}

		exposure := 1.0
		if cache != nil {
			exposure = cache.ComputeExplosionExposure(opts.X, opts.Y, opts.Z, box)
		}
		impact, baseDmg := world.ComputeExplosionImpactAndDamage(opts.X, opts.Y, opts.Z, opts.Radius, box, exposure)
		if impact <= 0 || baseDmg <= 0 {
			continue
		}

		scaled := ScaleDamageByDifficulty(baseDmg, s.Difficulty())
		if scaled <= 0 {
			continue
		}

		dealt := s.damagePlayerFromExplosion(sess, scaled, cause)
		if dealt > 0 {
			result.PlayersDamaged++
			if sess.IsDead() {
				if opts.SourceMobType != "" {
					s.TriggerKilledByEntity(sess, opts.SourceMobType)
				}
				s.BroadcastDeathMessage(sess, cause, opts.SourceMobType)
			}
		}

		bpLevel := PlayerMaxBlastProtectionLevel(sess)
		vx, vy, vz := world.ComputeExplosionKnockbackVector(opts.X, opts.Y, opts.Z, box, impact, bpLevel)
		if (vx != 0 || vy != 0 || vz != 0) && sess.send != nil {
			_ = sess.send(protocol.PlaySetEntityMotion, EncodeSetEntityMotionPacket(sess.EntityID, vx, vy, vz))
		}
	}

	// 3. Damage and knock back mobs (and destroy fragile dropped item entities) within 2*Radius.
	if s.entities != nil {
		chunkRadius := int32(math.Ceil(diameter/16.0)) + 1
		cx := int32(int64(math.Floor(opts.X)) >> 4)
		cz := int32(int64(math.Floor(opts.Z)) >> 4)
		ents := s.entities.AppendInChunkRange(nil, cx, cz, chunkRadius)
		for i := range ents {
			ent := &ents[i]
			if ent.ID == opts.SourceEntityID || ent.EffectiveDimension() != dim {
				continue
			}
			dx := ent.X - opts.X
			dy := ent.Y - opts.Y
			dz := ent.Z - opts.Z
			dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
			if dist >= diameter {
				continue
			}

			if ent.IsItem() {
				if !isExplosionImmuneItem(ent.Item.ResolvedID()) {
					box := world.EntityAABB(ent.X, ent.Y, ent.Z, 0.25, 0.25)
					exp := 1.0
					if cache != nil {
						exp = cache.ComputeExplosionExposure(opts.X, opts.Y, opts.Z, box)
					}
					if exp > 0 {
						s.entities.Remove(ent.ID)
						cp := ent.ChunkPos()
						s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
							encodeRemoveEntitiesPacket(nil, []int32{ent.ID}))
					}
				}
				continue
			}

			if ent.IsExperienceOrb() {
				continue
			}
			curHP := s.MobHealth(ent.ID)
			if curHP <= 0 {
				continue
			}
			box := world.EntityAABB(ent.X, ent.Y, ent.Z, 0.6, 1.8)
			exposure := 1.0
			if cache != nil {
				exposure = cache.ComputeExplosionExposure(opts.X, opts.Y, opts.Z, box)
			}
			impact, baseDmg := world.ComputeExplosionImpactAndDamage(opts.X, opts.Y, opts.Z, opts.Radius, box, exposure)
			if impact <= 0 || baseDmg <= 0 {
				continue
			}

			nextHP := curHP - baseDmg
			if nextHP < 0 {
				nextHP = 0
			}
			s.SetMobHealth(ent.ID, nextHP)
			result.MobsDamaged++
		}
	}

	// 4. Propel any active PrimedTNT entities within 2*Radius away from the explosion center.
	ds := s.dispenserState()
	ds.mu.Lock()
	for id, tnt := range ds.primedTNTs {
		if id == opts.SourceEntityID || tnt.Dimension != dim {
			continue
		}
		box := world.EntityAABB(tnt.X, tnt.Y, tnt.Z, 0.98, 0.98)
		exp := 1.0
		if cache != nil {
			exp = cache.ComputeExplosionExposure(opts.X, opts.Y, opts.Z, box)
		}
		impact, _ := world.ComputeExplosionImpactAndDamage(opts.X, opts.Y, opts.Z, opts.Radius, box, exp)
		if impact > 0 {
			vx, vy, vz := world.ComputeExplosionKnockbackVector(opts.X, opts.Y, opts.Z, box, impact, 0)
			tnt.VX += vx
			tnt.VY += vy
			tnt.VZ += vz
		}
	}
	ds.mu.Unlock()
}

func (s *Server) damagePlayerFromExplosion(session *PlayerSession, scaledDamage float32, cause string) float32 {
	if s == nil || session == nil || scaledDamage <= 0 || session.IsDead() {
		return 0
	}
	prot := PlayerBlastProtectionReduction(session)
	if prot <= 0 {
		return s.DamagePlayerWithArmor(session, scaledDamage, cause)
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return 0
	}
	def, tough := session.ArmorDefense()
	afterArmor := DamageAfterArmor(scaledDamage, def, tough)
	afterEnch := afterArmor * (1.0 - prot)
	if afterEnch <= 0 {
		return 0
	}
	effective := afterEnch * session.ResistanceDamageMultiplier(cause)
	if effective <= 0 {
		return 0
	}
	s.DamagePlayerWithEffects(session, afterEnch, cause)
	s.RecordDamageExhaustion(session)
	s.SyncHealthObjectives(session)
	return effective
}

func isExplosionImmuneItem(itemID string) bool {
	norm := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(itemID)), "minecraft:")
	if norm == "nether_star" || norm == "ancient_debris" || norm == "netherite_ingot" || norm == "netherite_scrap" || norm == "netherite_block" {
		return true
	}
	return strings.HasPrefix(norm, "netherite_")
}
