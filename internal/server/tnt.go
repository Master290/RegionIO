package server

import (
	"math"

	"regionio/internal/world"
)

// BindTNTAndFireMechanics binds the TNT PrimeHook and synchronizes tntExplodes,
// doFireTick, and dimension rules onto the world.Cache for dimension.
func (s *Server) BindTNTAndFireMechanics(dimension string) *world.Cache {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil {
		return nil
	}
	cache.SetTNTExplodes(s.TNTExplodes())
	cache.SetDoFireTick(s.DoFireTick())
	cache.SetFireDimension(normDim)
	cache.SetTNTPrimeHook(func(_ *world.Cache, pos world.BlockPos, fuseTicks int, _ world.TNTIgnitionCause) {
		if !s.TNTExplodes() {
			return
		}
		s.SpawnPrimedTNTWithFuse(normDim, float64(pos.X)+0.5, float64(pos.Y), float64(pos.Z)+0.5, fuseTicks, nil)
	})
	return cache
}

// SpawnPrimedTNTWithFuse spawns a primed `minecraft:tnt` entity at (x, y, z) in
// dim with the given fuse duration in ticks (default 80) and optional igniter.
func (s *Server) SpawnPrimedTNTWithFuse(dim string, x, y, z float64, fuseTicks int, igniter *PlayerSession) int32 {
	if s == nil || !s.TNTExplodes() {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	if fuseTicks <= 0 {
		fuseTicks = DefaultPrimedTNTFuseTicks
	}

	ds := s.dispenserState()
	ds.mu.Lock()
	angle := ds.rng.Float64() * 2.0 * math.Pi
	vx := -math.Sin(angle) * 0.02
	vy := 0.2
	vz := -math.Cos(angle) * 0.02
	ds.mu.Unlock()

	entID := s.spawnDispenserProjectileEntity(normDim, EntityTypeTNT, x, y, z, vx, vy, vz)
	var igniterID int32
	if igniter != nil {
		igniterID = igniter.EntityID
	}

	ds.mu.Lock()
	ds.primedTNTs[entID] = &PrimedTNTState{
		EntityID:  entID,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		VX:        vx,
		VY:        vy,
		VZ:        vz,
		FuseTicks: fuseTicks,
		IgniterID: igniterID,
	}
	ds.mu.Unlock()

	cx, cz := int32(math.Floor(x))>>4, int32(math.Floor(z))>>4
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.tnt.primed", SoundSourceBlocks, x, y, z, 1.0, 1.0, 0))
	s.EmitGameEvent(normDim, GameEventPrimeFuse, x, y, z, VibrationSource{EntityID: entID, Player: igniter})
	return entID
}

// ActivePrimedTNTs returns a snapshot slice of all currently active PrimedTNTState entities.
func (s *Server) ActivePrimedTNTs() []PrimedTNTState {
	if s == nil {
		return nil
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	defer ds.mu.Unlock()
	out := make([]PrimedTNTState, 0, len(ds.primedTNTs))
	for _, st := range ds.primedTNTs {
		if st != nil {
			out = append(out, *st)
		}
	}
	return out
}

// IgniteTNTAt primes a `minecraft:tnt` block at (x, y, z) in dim with the given
// cause and optional igniter player, respecting gamerule tntExplodes.
func (s *Server) IgniteTNTAt(dim string, x, y, z int, cause world.TNTIgnitionCause, igniter *PlayerSession) (int32, bool) {
	if s == nil || !s.TNTExplodes() {
		return 0, false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.BindTNTAndFireMechanics(normDim)
	if cache == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return 0, false
	}
	if !world.IsTNTBlock(cache.GetBlock(x, y, z)) {
		return 0, false
	}
	ok, upd := cache.BreakBlockWithUpdates(x, y, z)
	if !ok {
		return 0, false
	}
	if len(upd.Changes) > 0 {
		s.broadcastDimensionBlockChanges(normDim, cache, upd.Changes, upd.LightChanged)
	}
	fuse := DefaultPrimedTNTFuseTicks
	if cause == world.TNTIgnitionExplosion {
		ds := s.dispenserState()
		ds.mu.Lock()
		fuse = world.RollChainTNTFuseTicks(ds.rng)
		ds.mu.Unlock()
	}
	entID := s.SpawnPrimedTNTWithFuse(normDim, float64(x)+0.5, float64(y), float64(z)+0.5, fuse, igniter)
	return entID, entID != 0
}

// IgniteTNTBlockWithItem handles right-clicking a `minecraft:tnt` block at (x, y, z)
// with `minecraft:flint_and_steel` (damaging durability by 1 in Survival) or
// `minecraft:fire_charge` (consuming 1 item in Survival).
// Respects gamerule tntExplodes.
func (s *Server) IgniteTNTBlockWithItem(
	session *PlayerSession,
	dim string,
	x, y, z int,
	stack world.ItemStack,
	meta ItemMeta,
) (entityID int32, updatedStack world.ItemStack, updatedMeta ItemMeta, ok bool) {
	updatedStack = stack
	updatedMeta = meta.Clone()
	if s == nil || stack.IsEmpty() || !s.TNTExplodes() {
		return 0, updatedStack, updatedMeta, false
	}

	itemID := canonicalResourceName(stack.ResolvedID())
	var cause world.TNTIgnitionCause
	switch itemID {
	case "minecraft:flint_and_steel":
		cause = world.TNTIgnitionFlintAndSteel
	case "minecraft:fire_charge":
		cause = world.TNTIgnitionFireCharge
	default:
		return 0, updatedStack, updatedMeta, false
	}

	entID, primed := s.IgniteTNTAt(dim, x, y, z, cause, session)
	if !primed {
		return 0, updatedStack, updatedMeta, false
	}

	creative := session != nil && session.Snapshot().GameMode == GameModeCreative
	if !creative {
		if itemID == "minecraft:flint_and_steel" {
			ds := s.dispenserState()
			ds.mu.Lock()
			unbreaking := updatedMeta.EnchantmentLevel(EnchantUnbreaking)
			wear := 1
			if unbreaking > 0 && ds.rng.Intn(unbreaking+1) != 0 {
				wear = 0
			}
			ds.mu.Unlock()
			if wear > 0 {
				updatedMeta.Damage += int32(wear)
				if updatedMeta.Damage >= FlintAndSteelMaxDurability {
					updatedStack = world.ItemStack{}
					updatedMeta = ItemMeta{}
				}
			}
		} else if itemID == "minecraft:fire_charge" {
			if stack.Count <= 1 {
				updatedStack = world.ItemStack{}
				updatedMeta = ItemMeta{}
			} else {
				updatedStack = world.NewItemStack(stack.ResolvedItemID(), stack.Count-1)
			}
		}
	}

	return entID, updatedStack, updatedMeta, true
}

// IgniteTNTWithProjectile handles a projectile hitting a `minecraft:tnt` block
// at (x, y, z). A burning projectile (e.g. arrow shot through lava/fire or with
// Flame enchantment) or a fire projectile (small_fireball / fireball) primes the
// TNT block when gamerules tntExplodes and projectilesCanBreakBlocks are true.
func (s *Server) IgniteTNTWithProjectile(
	dim string,
	x, y, z int,
	onFire bool,
	projectileType string,
	shooter *PlayerSession,
) (int32, bool) {
	if s == nil || !s.TNTExplodes() || !s.GameRuleBool("projectilesCanBreakBlocks") {
		return 0, false
	}
	normProj := canonicalResourceName(projectileType)
	isFiery := onFire || normProj == "minecraft:small_fireball" || normProj == "minecraft:fireball"
	if !isFiery {
		return 0, false
	}
	return s.IgniteTNTAt(dim, x, y, z, world.TNTIgnitionProjectile, shooter)
}

// HandleTNTBlockBrokenByPlayer handles breaking a `minecraft:tnt` block at (x, y, z):
// if the TNT has `unstable=true` (and was not broken with shears or in Creative),
// it primes immediately instead of dropping an item; otherwise it drops 1 `minecraft:tnt`
// when doTileDrops is enabled.
func (s *Server) HandleTNTBlockBrokenByPlayer(
	session *PlayerSession,
	dim string,
	x, y, z int,
	brokenState uint16,
	toolStack world.ItemStack,
) (primedEntityID int32, dropped world.ItemStack) {
	if !world.IsTNTBlock(brokenState) {
		return 0, world.ItemStack{}
	}
	creative := session != nil && session.Snapshot().GameMode == GameModeCreative
	if creative {
		return 0, world.ItemStack{}
	}
	isShears := canonicalResourceName(toolStack.ResolvedID()) == "minecraft:shears"
	if world.IsUnstableTNTState(brokenState) && !isShears {
		if s.TNTExplodes() {
			entID := s.SpawnPrimedTNTWithFuse(dim, float64(x)+0.5, float64(y), float64(z)+0.5, DefaultPrimedTNTFuseTicks, session)
			return entID, world.ItemStack{}
		}
		return 0, world.ItemStack{}
	}
	if s.DoTileDrops() {
		return 0, world.NewNamedItemStack("minecraft:tnt", 1)
	}
	return 0, world.ItemStack{}
}

func (s *Server) stepPrimedTNTPhysics(tnt *PrimedTNTState) {
	if s == nil || tnt == nil {
		return
	}
	tnt.VY -= 0.04
	nextX := tnt.X + tnt.VX
	nextY := tnt.Y + tnt.VY
	nextZ := tnt.Z + tnt.VZ

	onGround := false
	if cache := s.DimensionChunks(tnt.Dimension); cache != nil {
		bx := int(math.Floor(nextX))
		by := int(math.Floor(nextY))
		bz := int(math.Floor(nextZ))
		if by >= world.MinY && by < world.MinY+world.WorldHeight {
			st := cache.GetBlock(bx, by, bz)
			if st != world.StateAir && world.BlockBlastResistance(st) > 0 && st != world.StateWater && st != world.StateLava {
				nextY = float64(by + 1)
				tnt.VY = 0
				onGround = true
			}
		}
	}

	tnt.X = nextX
	tnt.Y = nextY
	tnt.Z = nextZ
	tnt.VX *= 0.98
	tnt.VY *= 0.98
	tnt.VZ *= 0.98
	if onGround {
		tnt.VX *= 0.7
		tnt.VZ *= 0.7
	}
}

func (s *Server) detonatePrimedTNT(tnt PrimedTNTState) ExplosionResult {
	if s == nil || !s.TNTExplodes() {
		return ExplosionResult{}
	}
	var activator *PlayerSession
	if tnt.IgniterID != 0 {
		activator = s.playerByEntityID(tnt.IgniterID)
	}
	return s.DetonateExplosion(ExplosionOptions{
		Dimension:      tnt.Dimension,
		X:              tnt.X,
		Y:              tnt.Y + 0.0625,
		Z:              tnt.Z,
		Radius:         DefaultPrimedTNTExplosionRadius,
		Category:       ExplosionSourceTNT,
		DamageCause:    "explosion",
		SourceEntityID: tnt.EntityID,
		Activator:      activator,
	})
}
