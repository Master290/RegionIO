package server

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"math/rand"
	"strings"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Vanilla 26.1.2 Warden & Creaking additional constants verified against
// `Warden.class`, `WardenAi.class`, `SonicBoom.class`, `Creaking.class`,
// `CreakingAi.class`, and `CreakingHeartBlockEntity.class` in `26.1.2.jar`.
const (
	WardenSonicBoomInitialCooldownTicks int     = 200  // Warden.TIME_TO_USE_MELEE_UNTIL_SONIC_BOOM
	WardenTouchCooldownTicks            int     = 20   // Warden.TOUCH_COOLDOWN_TICKS
	WardenProjectileAngerDistance       float64 = 30.0 // Warden.PROJECTILE_ANGER_DISTANCE
	WardenRoarSoundDelayTicks           int     = 25   // Roar.TICKS_BEFORE_PLAYING_ROAR_SOUND

	CreakingActivationRange       float64 = 12.0 // sqrt(144.0) in Creaking.checkCanMove when !isActive()
	CreakingEyeHeight2612         float64 = 2.3  // EntityType.CREAKING.eyeHeight(2.3F)
	CreakingReloadGraceTicks2612  int     = 30   // CreakingHeartBlockEntity.getCreakingProtector ticksExisted < 30L
	CreakingHeartMaxClumpDrop2612 int32   = 9    // loot_table/blocks/creaking_heart.json limit_count max=9.0
	EntityEventCreakingHurt2612   byte    = 66   // Creaking.hurtServer broadcastEntityEvent 66
	EntityEventCreakingSpawn2612  byte    = 60   // CreakingHeartBlockEntity.spawnProtector broadcastEntityEvent 60
	SoundResinPlace2612                   = "minecraft:block.resin.place"
)

// WardenNBTSuspectEntry represents one `(uuid, anger)` pair in a Warden's
// vanilla 26.1.2 `"anger"` -> `"suspects"` NBT list (`AngerManagement.codec`).
type WardenNBTSuspectEntry struct {
	UUID  [16]byte
	Anger int
}

// WardenHeartbeatInterval2612 computes the exact vanilla 26.1.2 `Warden.getHeartBeatDelay()`:
//
//	float f = Mth.clamp((float)clientAnger / 80.0F, 0.0F, 1.0F);
//	return 40 - Mth.floor(f * 30.0F);
func WardenHeartbeatInterval2612(clientAnger int) int {
	f := float64(clientAnger) / float64(WardenAngryThreshold)
	if f < 0.0 {
		f = 0.0
	} else if f > 1.0 {
		f = 1.0
	}
	return 40 - int(math.Floor(f*30.0))
}

// ResolvePlayerKnockbackResistance2612 returns the player's total
// `Attributes.KNOCKBACK_RESISTANCE` (`0.0..1.0`) from equipped Netherite armor
// (`0.1` per piece: helmet, chestplate, leggings, boots).
func ResolvePlayerKnockbackResistance2612(player *PlayerSession) float64 {
	if player == nil {
		return 0.0
	}
	var kbRes float64
	for _, slot := range [4]int{ArmorSlotHead, ArmorSlotChest, ArmorSlotLegs, ArmorSlotFeet} {
		st := player.InventorySlot(slot)
		if st.IsEmpty() {
			continue
		}
		switch canonicalResourceName(st.ResolvedID()) {
		case "minecraft:netherite_helmet",
			"minecraft:netherite_chestplate",
			"minecraft:netherite_leggings",
			"minecraft:netherite_boots":
			kbRes += 0.1
		}
	}
	if kbRes > 1.0 {
		kbRes = 1.0
	}
	return kbRes
}

// ResolveMobKnockbackResistance2612 returns the vanilla 26.1.2 knockback
// resistance (`0.0..1.0`) for a mob entity (including `1.0` for frozen Creakings).
func ResolveMobKnockbackResistance2612(s *Server, ent *world.Entity) float64 {
	if ent == nil {
		return 0.0
	}
	switch canonicalResourceName(ent.TypeName) {
	case MobTypeWarden, MobTypeIronGolem, MobTypeShulker, MobTypeEnderDragon:
		return 1.0
	case MobTypeCreaking:
		if s != nil && !s.CanCreakingMove(ent.ID) {
			return 1.0
		}
		return 0.0
	case MobTypeRavager:
		return 0.75
	case MobTypeHoglin, MobTypeZoglin:
		return 0.6
	default:
		return 0.0
	}
}

// ComputeWardenSonicBoomPush2612 computes the exact 3D velocity delta applied by
// `SonicBoom.lambda$tick$2` in `26.1.2.jar` (`lines 833-931`):
//   - Ray starts at `WARDEN_CHEST` `(wx, wy + 1.6, wz)` toward `(tx, tEyeY, tz)`.
//   - `vertKB  = 0.5 * (1.0 - kbResistance)`
//   - `horizKB = 2.5 * (1.0 - kbResistance)`
//   - `push(dir.x * horizKB, dir.y * vertKB, dir.z * horizKB)`
func ComputeWardenSonicBoomPush2612(wx, wy, wz, tx, tEyeY, tz, kbResistance float64) (vx, vy, vz float64) {
	if kbResistance < 0.0 {
		kbResistance = 0.0
	} else if kbResistance >= 1.0 {
		return 0, 0, 0
	}
	dx := tx - wx
	dy := tEyeY - (wy + 1.6)
	dz := tz - wz
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist < 1e-6 {
		return 0, 0, 0
	}
	ux, uy, uz := dx/dist, dy/dist, dz/dist
	scale := 1.0 - kbResistance
	horizKB := WardenSonicBoomHorizKB * scale
	vertKB := WardenSonicBoomVertKB * scale
	return ux * horizKB, uy * vertKB, uz * horizKB
}

// IsWardenGroundLineBlocked2612 checks whether solid non-passable blocks obstruct
// direct ground-level movement (1-block step-up aware) between `(wx, feetY, wz)`
// and `(tx, goalFeetY, tz)` so a walled-off target triggers `SonicBoom` without delay.
func IsWardenGroundLineBlocked2612(cache *world.Cache, wx float64, feetY int, wz float64, tx float64, goalFeetY int, tz float64) bool {
	if cache == nil {
		return false
	}
	if absInt(goalFeetY-feetY) > 2 {
		return true
	}
	dx := tx - wx
	dz := tz - wz
	horiz := math.Hypot(dx, dz)
	if horiz < 0.5 {
		return false
	}
	steps := int(math.Ceil(horiz * 2.0))
	curY := feetY
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		sx := int(math.Floor(wx + dx*t))
		sz := int(math.Floor(wz + dz*t))
		b0 := cache.GetBlock(sx, curY, sz)
		b1 := cache.GetBlock(sx, curY+1, sz)
		b2 := cache.GetBlock(sx, curY+2, sz)
		if isPassableBlock(b0) && isPassableBlock(b1) && isPassableBlock(b2) {
			continue
		}
		// Check 1-block step-up.
		u0 := cache.GetBlock(sx, curY+1, sz)
		u1 := cache.GetBlock(sx, curY+2, sz)
		u2 := cache.GetBlock(sx, curY+3, sz)
		if !isPassableBlock(b0) && isPassableBlock(u0) && isPassableBlock(u1) && isPassableBlock(u2) {
			curY++
			continue
		}
		return true
	}
	return false
}

func parseUUIDTag2612(tag nbt.Tag) ([16]byte, bool) {
	switch v := tag.(type) {
	case nbt.IntArray:
		return intArrayToUUIDBytes2612(v)
	case nbt.String:
		clean := strings.ReplaceAll(strings.TrimSpace(string(v)), "-", "")
		if len(clean) != 32 {
			return [16]byte{}, false
		}
		raw, err := hex.DecodeString(clean)
		if err != nil || len(raw) != 16 {
			return [16]byte{}, false
		}
		var out [16]byte
		copy(out[:], raw)
		return out, true
	default:
		return [16]byte{}, false
	}
}

func syntheticEntityUUID2612(entityID int32) [16]byte {
	var u [16]byte
	u[0] = 0xEE
	u[1] = 0x26
	u[2] = 0x12
	binary.BigEndian.PutUint32(u[12:16], uint32(entityID))
	return u
}

func resolveSuspectUUID2612(s *Server, entry *WardenSuspectEntry) [16]byte {
	if entry == nil {
		return [16]byte{}
	}
	if entry.PlayerUUID != ([16]byte{}) {
		return entry.PlayerUUID
	}
	if s != nil {
		if p := s.playerByEntityID(entry.EntityID); p != nil && p.Profile.UUID != ([16]byte{}) {
			return p.Profile.UUID
		}
		if s.entities != nil {
			if ent := s.entities.Get(entry.EntityID); ent != nil && ent.UUID != ([16]byte{}) {
				return ent.UUID
			}
		}
	}
	return syntheticEntityUUID2612(entry.EntityID)
}

func (s *Server) findEntityByUUID2612(dim string, uuid [16]byte) (entityID int32, player *PlayerSession, x, y, z float64, ok bool) {
	if s == nil || uuid == ([16]byte{}) {
		return 0, nil, 0, 0, 0, false
	}
	normDim := world.NormalizeDimension(dim)
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() || p.Profile.UUID != uuid {
			continue
		}
		snap := p.Snapshot()
		if world.NormalizeDimension(snap.Dimension) != normDim ||
			snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator {
			continue
		}
		return snap.EntityID, p, snap.X, snap.Y, snap.Z, true
	}
	if s.entities != nil {
		for _, ent := range s.entities.AppendAll(nil) {
			if ent.EffectiveDimension() != normDim {
				continue
			}
			entUUID := ent.UUID
			if entUUID == ([16]byte{}) {
				entUUID = syntheticEntityUUID2612(ent.ID)
			}
			if entUUID == uuid && s.MobHealth(ent.ID) > 0 {
				return ent.ID, nil, ent.X, ent.Y, ent.Z, true
			}
		}
	}
	return 0, nil, 0, 0, 0, false
}

// EncodeWardenEntityNBT encodes a Warden's state into a vanilla 26.1.2
// `*nbt.Compound`, storing `"anger": {"suspects": [{"uuid": IntArray[4], "anger": Int}, ...]}`
// (`Warden.addAdditionalSaveData` + `AngerManagement.codec`).
func EncodeWardenEntityNBT(st WardenState) *nbt.Compound {
	return encodeWardenEntityNBTWithServer(nil, st)
}

func encodeWardenEntityNBTWithServer(s *Server, st WardenState) *nbt.Compound {
	c := nbt.NewCompound()
	c.Set("id", nbt.String(MobTypeWarden))
	c.Set("Health", nbt.Float(st.Health))
	if st.PersistenceRequired {
		c.Set("PersistenceRequired", nbt.Byte(1))
	}

	seenUUIDs := make(map[[16]byte]struct{}, len(st.Suspects)+len(st.AngerByUUID))
	elems := make([]nbt.Tag, 0, len(st.Suspects)+len(st.AngerByUUID))

	for _, entry := range st.Suspects {
		if entry == nil || entry.Anger <= 0 {
			continue
		}
		u := resolveSuspectUUID2612(s, entry)
		if u == ([16]byte{}) {
			continue
		}
		seenUUIDs[u] = struct{}{}
		sc := nbt.NewCompound()
		sc.Set("uuid", uuidBytesToIntArray2612(u))
		sc.Set("anger", nbt.Int(int32(clampWardenAnger(entry.Anger))))
		elems = append(elems, sc)
	}
	for u, anger := range st.AngerByUUID {
		if anger <= 0 || u == ([16]byte{}) {
			continue
		}
		if _, dup := seenUUIDs[u]; dup {
			continue
		}
		sc := nbt.NewCompound()
		sc.Set("uuid", uuidBytesToIntArray2612(u))
		sc.Set("anger", nbt.Int(int32(clampWardenAnger(anger))))
		elems = append(elems, sc)
	}

	angerComp := nbt.NewCompound()
	angerComp.Set("suspects", nbt.List{
		ElemID: nbt.TagCompound,
		Elems:  elems,
	})
	c.Set("anger", angerComp)
	return c
}

// DecodeWardenEntityNBT decodes a Warden's `"anger"` -> `"suspects"` list from
// a vanilla 26.1.2 `*nbt.Compound`.
func DecodeWardenEntityNBT(c *nbt.Compound) (suspects []WardenNBTSuspectEntry, persistenceRequired bool, health float32, hasHealth bool, ok bool) {
	if c == nil {
		return nil, false, 0, false, false
	}
	if pt, has := c.Get("PersistenceRequired"); has {
		if pb, ok := pt.(nbt.Byte); ok && pb != 0 {
			persistenceRequired = true
		}
	}
	if ht, has := c.Get("Health"); has {
		if hf, ok := ht.(nbt.Float); ok {
			health = float32(hf)
			hasHealth = true
		}
	}
	angerTag, hasAnger := c.Get("anger")
	if !hasAnger {
		return nil, persistenceRequired, health, hasHealth, true
	}
	angerComp, ok := angerTag.(*nbt.Compound)
	if !ok || angerComp == nil {
		return nil, persistenceRequired, health, hasHealth, false
	}
	suspTag, hasSusp := angerComp.Get("suspects")
	if !hasSusp {
		return nil, persistenceRequired, health, hasHealth, true
	}
	lst, ok := suspTag.(nbt.List)
	if !ok {
		return nil, persistenceRequired, health, hasHealth, false
	}
	for _, elem := range lst.Elems {
		sc, ok := elem.(*nbt.Compound)
		if !ok || sc == nil {
			continue
		}
		uTag, uOk := sc.Get("uuid")
		aTag, aOk := sc.Get("anger")
		if !uOk || !aOk {
			continue
		}
		uuid, validUUID := parseUUIDTag2612(uTag)
		var angerVal int
		switch av := aTag.(type) {
		case nbt.Int:
			angerVal = int(av)
		case nbt.Short:
			angerVal = int(av)
		case nbt.Byte:
			angerVal = int(av)
		default:
			continue
		}
		if !validUUID || angerVal <= 0 {
			continue
		}
		suspects = append(suspects, WardenNBTSuspectEntry{
			UUID:  uuid,
			Anger: clampWardenAnger(angerVal),
		})
	}
	return suspects, persistenceRequired, health, hasHealth, true
}

// SaveWardenEntityNBT snapshots an active Warden's state into a vanilla 26.1.2 `*nbt.Compound`.
func (s *Server) SaveWardenEntityNBT(entityID int32) (*nbt.Compound, bool) {
	st, ok := s.GetWardenState(entityID)
	if !ok {
		return nil, false
	}
	return encodeWardenEntityNBTWithServer(s, st), true
}

// LoadWardenEntityNBT restores `"anger"` (`suspects`), `"Health"`, and `"PersistenceRequired"`
// from a vanilla 26.1.2 `*nbt.Compound` onto `entityID`, resolving any live suspects by UUID
// and syncing `CLIENT_ANGER_LEVEL` (`Warden.readAdditionalSaveData`).
func (s *Server) LoadWardenEntityNBT(entityID int32, c *nbt.Compound) bool {
	if s == nil || entityID <= 0 || c == nil {
		return false
	}
	suspects, persist, hp, hasHP, ok := DecodeWardenEntityNBT(c)
	if !ok {
		return false
	}
	mgr := s.wardenManager()
	mgr.mu.Lock()
	st := mgr.wardens[entityID]
	if st == nil {
		mgr.mu.Unlock()
		return false
	}
	st.PersistenceRequired = persist
	if hasHP && hp > 0 {
		if hp > st.MaxHealth && st.MaxHealth > 0 {
			hp = st.MaxHealth
		}
		st.Health = hp
	}
	st.Suspects = make(map[int32]*WardenSuspectEntry)
	st.AngerByUUID = make(map[[16]byte]int, len(suspects))
	for _, sp := range suspects {
		st.AngerByUUID[sp.UUID] = clampWardenAnger(sp.Anger)
	}
	s.convertWardenUUIDSuspectsLocked(st)
	recomputeWardenTargetLocked(st)
	snap := st.Clone()
	mgr.mu.Unlock()

	if hasHP && snap.Health > 0 {
		s.SetMobHealth(entityID, snap.Health)
	}
	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySetEntityDataCB,
		EncodeWardenEntityDataPacket(entityID, snap.Pose, snap.ClientAngerLevel))
	return true
}

func (s *Server) convertWardenUUIDSuspectsLocked(st *WardenState) {
	if s == nil || st == nil || len(st.AngerByUUID) == 0 {
		return
	}
	if st.Suspects == nil {
		st.Suspects = make(map[int32]*WardenSuspectEntry)
	}
	for uuid, anger := range st.AngerByUUID {
		if anger <= 0 {
			delete(st.AngerByUUID, uuid)
			continue
		}
		eid, p, x, y, z, found := s.findEntityByUUID2612(st.Dimension, uuid)
		if !found || eid == 0 || eid == st.EntityID {
			continue
		}
		entry := st.Suspects[eid]
		if entry == nil {
			entry = &WardenSuspectEntry{
				EntityID:   eid,
				IsPlayer:   p != nil,
				PlayerUUID: uuid,
			}
			st.Suspects[eid] = entry
		}
		entry.Anger = clampWardenAnger(entry.Anger + anger)
		entry.LastKnownX = x
		entry.LastKnownY = y
		entry.LastKnownZ = z
		delete(st.AngerByUUID, uuid)
	}
}

// ============================================================================
// CREAKING & CREAKING HEART 26.1.2 PARITY HELPERS + BINARY NBT
// ============================================================================

// CanCreakingMove reports whether `entityID` is an unfrozen, non-tearing-down Creaking.
func (s *Server) CanCreakingMove(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return true
	}
	cm := s.creakingRabbitMgr()
	cm.mu.RLock()
	st, ok := cm.creaking[entityID]
	canMove := !ok || st == nil || (st.CanMove && !st.IsTearingDown)
	cm.mu.RUnlock()
	return canMove
}

func isVisualTransparentBlock2612(state uint16) bool {
	if state == world.StateAir || isPassableBlock(state) {
		return true
	}
	name := world.BlockName(state)
	switch {
	case name == "minecraft:glass",
		name == "minecraft:tinted_glass",
		name == "minecraft:glass_pane",
		name == "minecraft:iron_bars",
		name == "minecraft:chain",
		name == "minecraft:barrier",
		strings.HasSuffix(name, "_stained_glass"),
		strings.HasSuffix(name, "_stained_glass_pane"),
		strings.HasSuffix(name, "_leaves"),
		strings.HasSuffix(name, "_copper_grate"),
		name == "minecraft:copper_grate":
		return true
	default:
		return false
	}
}

// CreakingVisualLineOfSight2612 checks unobstructed `ClipContext.Block.VISUAL, Fluid.NONE`
// line-of-sight between `(x0, y0, z0)` and `(x1, y1, z1)` (`<= 128` blocks).
// Blocks with empty visual shape (air, passable plants/fluids, glass, panes, iron bars, leaves)
// allow player gaze to freeze a Creaking, whereas solid opaque blocks occlude gaze.
func CreakingVisualLineOfSight2612(cache *world.Cache, x0, y0, z0, x1, y1, z1 float64) bool {
	dx := x1 - x0
	dy := y1 - y0
	dz := z1 - z0
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist > 128.0 {
		return false
	}
	if cache == nil || dist < 1e-4 {
		return true
	}
	steps := int(math.Ceil(dist * 4.0))
	if steps < 1 {
		steps = 1
	}
	var lastBX, lastBY, lastBZ int
	hasLast := false
	for i := 1; i < steps; i++ {
		t := float64(i) / float64(steps)
		bx := int(math.Floor(x0 + dx*t))
		by := int(math.Floor(y0 + dy*t))
		bz := int(math.Floor(z0 + dz*t))
		if hasLast && bx == lastBX && by == lastBY && bz == lastBZ {
			continue
		}
		lastBX, lastBY, lastBZ = bx, by, bz
		hasLast = true
		if by < world.MinY || by >= world.MinY+world.WorldHeight {
			continue
		}
		if !isVisualTransparentBlock2612(cache.GetBlock(bx, by, bz)) {
			return false
		}
	}
	return true
}

func isGazeDisguiseItem2612(itemName string) bool {
	// data/minecraft/tags/item/gaze_disguise_equipment.json in 26.1.2.jar: ["minecraft:carved_pumpkin"]
	return canonicalResourceName(itemName) == "minecraft:carved_pumpkin"
}

// IsPlayerLookingAtCreakingSamplePoints2612 evaluates `LivingEntity.isLookingAtMe`
// with the exact parameters passed by `Creaking.checkCanMove` in `26.1.2.jar`:
//   - `dotThreshold = 0.5D` (`cos(60°) = 0.5`, `scaleByDistance = false`, strict `dot > 0.5`)
//   - `visualClip = true` (`ClipContext.Block.VISUAL`, `Fluid.NONE`)
//   - 3 Y sample points: `getEyeY()` (`Y + 2.3`), `getY() + 0.5`, `(getEyeY() + getY()) / 2.0` (`Y + 1.15`).
func IsPlayerLookingAtCreakingSamplePoints2612(
	cache *world.Cache,
	player *PlayerSession,
	dimension string,
	creakingX, creakingY, creakingZ float64,
	maxRange float64,
	strictRange bool,
) bool {
	if player == nil || player.IsDead() {
		return false
	}
	snap := player.Snapshot()
	normDim := world.NormalizeDimension(dimension)
	if world.NormalizeDimension(snap.Dimension) != normDim {
		return false
	}
	if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator {
		return false
	}
	if player.HasPlayerEffect(EffectBlindness) {
		return false
	}
	headStack := player.InventorySlot(ArmorSlotHead)
	if !headStack.IsEmpty() && isGazeDisguiseItem2612(headStack.ResolvedID()) {
		return false
	}

	// Distance check (Entity.distanceToSqr uses entity origin (X, Y, Z)).
	edx := creakingX - snap.X
	edy := creakingY - snap.Y
	edz := creakingZ - snap.Z
	entityDistSq := edx*edx + edy*edy + edz*edz
	limitSq := maxRange * maxRange
	if strictRange {
		if entityDistSq >= limitSq {
			return false
		}
	} else if entityDistSq > limitSq {
		return false
	}

	eyeX := snap.X
	eyeY := snap.Y + 1.62
	eyeZ := snap.Z

	yawRad := float64(snap.Yaw) * (math.Pi / 180.0)
	pitchRad := float64(snap.Pitch) * (math.Pi / 180.0)
	cosPitch := math.Cos(pitchRad)
	lookX := -math.Sin(yawRad) * cosPitch
	lookY := -math.Sin(pitchRad)
	lookZ := math.Cos(yawRad) * cosPitch

	sampleYs := [3]float64{
		creakingY + CreakingEyeHeight2612,          // getEyeY() = Y + 2.3
		creakingY + 0.5,                            // getY() + 0.5 * scale
		creakingY + (CreakingEyeHeight2612*0.5),    // (getEyeY() + getY()) / 2.0 = Y + 1.15
	}
	for _, targetY := range sampleYs {
		dx := creakingX - eyeX
		dy := targetY - eyeY
		dz := creakingZ - eyeZ
		dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
		if dist < 1e-6 {
			continue
		}
		dot := (lookX*dx + lookY*dy + lookZ*dz) / dist
		if dot > CreakingGazeCosThreshold && CreakingVisualLineOfSight2612(cache, eyeX, eyeY, eyeZ, creakingX, targetY, creakingZ) {
			return true
		}
	}
	return false
}

// SpreadResinAroundHeart2612 implements `CreakingHeartBlockEntity.spreadResin`
// (`BlockPos.breadthFirstTraversal(heartPos, maxDepth=2, maxCount=64, ...)` in `26.1.2.jar`),
// traversing ONLY connected `#minecraft:pale_oak_logs` up to depth `2` from `(hx, hy, hz)`
// and placing `count` (`2..3`) `minecraft:resin_clump` faces onto air, source water
// (`waterlogged=true`), or existing `minecraft:resin_clump` blocks missing that face.
func SpreadResinAroundHeart2612(s *Server, dimension string, hx, hy, hz int, count int, rng *rand.Rand) int {
	if s == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return 0
	}
	if count <= 0 {
		if rng != nil {
			count = 2 + rng.Intn(2) // nextIntBetweenInclusive(2, 3)
		} else {
			count = 2 + rand.Intn(2)
		}
	}

	placed := 0
	for i := 0; i < count; i++ {
		if rx, ry, rz, ok := spreadSingleResinClumpBFS2612(s, cache, normDim, hx, hy, hz, rng); ok {
			placed++
			s.BroadcastDimensionChunk(normDim, int32(rx>>4), int32(rz>>4), PlaySoundPacketID,
				EncodeSoundPacket(SoundResinPlace2612, SoundSourceBlocks, float64(rx)+0.5, float64(ry)+0.5, float64(rz)+0.5, 1.0, 1.0, 0))
		}
	}
	return placed
}

type resinDirOffset2612 struct {
	dx, dy, dz   int
	oppositeFace string
}

var allResinDirs2612 = [6]resinDirOffset2612{
	{0, -1, 0, "up"},    // Direction.DOWN  -> opposite is UP
	{0, 1, 0, "down"},   // Direction.UP    -> opposite is DOWN
	{0, 0, -1, "south"}, // Direction.NORTH -> opposite is SOUTH
	{0, 0, 1, "north"},  // Direction.SOUTH -> opposite is NORTH
	{-1, 0, 0, "east"},  // Direction.WEST  -> opposite is EAST
	{1, 0, 0, "west"},   // Direction.EAST  -> opposite is WEST
}

func shuffledResinDirs2612(rng *rand.Rand) [6]resinDirOffset2612 {
	dirs := allResinDirs2612
	for i := len(dirs) - 1; i > 0; i-- {
		var j int
		if rng != nil {
			j = rng.Intn(i + 1)
		} else {
			j = rand.Intn(i + 1)
		}
		dirs[i], dirs[j] = dirs[j], dirs[i]
	}
	return dirs
}

func isSourceWaterState2612(state uint16) bool {
	if world.BlockName(state) != "minecraft:water" {
		return false
	}
	_, props, ok := world.BlockStateProperties(state)
	return !ok || props["level"] == "" || props["level"] == "0"
}

func spreadSingleResinClumpBFS2612(s *Server, cache *world.Cache, normDim string, hx, hy, hz int, rng *rand.Rand) (rx, ry, rz int, ok bool) {
	type bfsNode struct {
		x, y, z int
		depth   int
	}
	type posKey struct {
		x, y, z int
	}

	queue := make([]bfsNode, 0, 16)
	visited := make(map[posKey]struct{}, 32)
	start := posKey{hx, hy, hz}
	visited[start] = struct{}{}
	queue = append(queue, bfsNode{x: hx, y: hy, z: hz, depth: 0})

	visitedCount := 0
	for head := 0; head < len(queue) && visitedCount < 64; head++ {
		cur := queue[head]
		visitedCount++

		curState := cache.GetBlock(cur.x, cur.y, cur.z)
		if IsPaleOakLogBlock(curState) {
			for _, d := range shuffledResinDirs2612(rng) {
				ax, ay, az := cur.x+d.dx, cur.y+d.dy, cur.z+d.dz
				if ay < world.MinY || ay >= world.MinY+world.WorldHeight {
					continue
				}
				adjState := cache.GetBlock(ax, ay, az)
				adjName := world.BlockName(adjState)
				waterlogged := false
				canPlace := false
				if adjState == world.StateAir {
					canPlace = true
				} else if isSourceWaterState2612(adjState) {
					canPlace = true
					waterlogged = true
				} else if adjName == ResinClumpItemID {
					_, props, hasProps := world.BlockStateProperties(adjState)
					if !hasProps || props[d.oppositeFace] != "true" {
						canPlace = true
						waterlogged = hasProps && props["waterlogged"] == "true"
					}
				}
				if !canPlace {
					continue
				}
				newState, _, placed := cache.PlaceMultifaceFaceAt(ax, ay, az, ResinClumpItemID, d.oppositeFace)
				if !placed || newState == world.StateAir {
					continue
				}
				if waterlogged {
					if _, props, hasProps := world.BlockStateProperties(newState); hasProps {
						cp := make(map[string]string, len(props)+1)
						for k, v := range props {
							cp[k] = v
						}
						cp["waterlogged"] = "true"
						if wlState, wlOK := world.BlockStateIDByProperties(ResinClumpItemID, cp); wlOK {
							newState = wlState
						}
					}
				}
				cache.SetBlock(ax, ay, az, newState)
				s.broadcastMobBlockUpdate(normDim, ax, ay, az, newState)
				return ax, ay, az, true
			}
		}

		if cur.depth >= 2 {
			continue
		}
		for _, d := range shuffledResinDirs2612(rng) {
			nx, ny, nz := cur.x+d.dx, cur.y+d.dy, cur.z+d.dz
			if ny < world.MinY || ny >= world.MinY+world.WorldHeight {
				continue
			}
			nk := posKey{nx, ny, nz}
			if _, seen := visited[nk]; seen {
				continue
			}
			if !IsPaleOakLogBlock(cache.GetBlock(nx, ny, nz)) {
				continue
			}
			visited[nk] = struct{}{}
			queue = append(queue, bfsNode{x: nx, y: ny, z: nz, depth: cur.depth + 1})
		}
	}
	return 0, 0, 0, false
}

// StartCreakingTearDownDeath starts the 45-tick tearing-down death sequence
// (`Creaking.creakingDeathEffects` + `setTearingDown()` + `tickDeath` `deathTime` `1..45`,
// calling `tearDown()` on tick `46` when `deathTime > 45`) when a player destroys
// the linked `minecraft:creaking_heart`.
func (s *Server) StartCreakingTearDownDeath(entityID int32, destroyer *PlayerSession) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	cm := s.creakingRabbitMgr()
	cm.mu.Lock()
	st := cm.creaking[entityID]
	if st == nil {
		cm.mu.Unlock()
		return false
	}
	if st.IsHeartBound {
		key := creakingBlockKey{dim: st.Dimension, x: st.HeartX, y: st.HeartY, z: st.HeartZ}
		if hs := cm.hearts[key]; hs != nil && hs.LinkedCreakingID == entityID {
			hs.LinkedCreakingID = 0
			hs.HasLinkedUUID = false
		}
	}
	st.CanMove = false
	st.IsTearingDown = true
	st.TearingTicks = 0
	snap := *st
	cm.mu.Unlock()

	s.SetMobHealth(entityID, 0)
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeCreakingEntityDataPacket(entityID, false, snap.IsActive, true, snap.IsHeartBound, snap.HeartX, snap.HeartY, snap.HeartZ))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundCreakingTwitch, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	if destroyer != nil {
		s.TriggerEntityKilled(destroyer, MobTypeCreaking)
		s.IncrementCriteriaObjectives(destroyer.Profile.Name, "totalKillCount", 1)
	}
	return true
}

// EncodeCreakingEntityNBT encodes a `Creaking` entity's state into a vanilla 26.1.2
// `*nbt.Compound`, writing `"home_pos": IntArray[3]` when `st.IsHeartBound == true`
// (`Creaking.addAdditionalSaveData`).
func EncodeCreakingEntityNBT(st CreakingState) *nbt.Compound {
	c := nbt.NewCompound()
	c.Set("id", nbt.String(MobTypeCreaking))
	if st.PersistenceRequired {
		c.Set("PersistenceRequired", nbt.Byte(1))
	}
	if st.EntityUUID != ([16]byte{}) {
		c.Set("UUID", uuidBytesToIntArray2612(st.EntityUUID))
	}
	if st.IsHeartBound {
		c.Set("home_pos", nbt.IntArray{int32(st.HeartX), int32(st.HeartY), int32(st.HeartZ)})
	}
	return c
}

// DecodeCreakingEntityNBT decodes `"home_pos"`, `"UUID"`, and `"PersistenceRequired"`
// from a vanilla 26.1.2 `Creaking` `*nbt.Compound`.
func DecodeCreakingEntityNBT(c *nbt.Compound) (hasHomePos bool, hx, hy, hz int, uuid [16]byte, hasUUID, persistenceRequired, ok bool) {
	if c == nil {
		return false, 0, 0, 0, [16]byte{}, false, false, false
	}
	if pt, has := c.Get("PersistenceRequired"); has {
		if pb, bOK := pt.(nbt.Byte); bOK && pb != 0 {
			persistenceRequired = true
		}
	}
	if ut, has := c.Get("UUID"); has {
		if u, uOK := parseUUIDTag2612(ut); uOK {
			uuid = u
			hasUUID = true
		}
	}
	if ht, has := c.Get("home_pos"); has {
		arr, aOK := ht.(nbt.IntArray)
		if !aOK || len(arr) != 3 {
			return false, 0, 0, 0, uuid, hasUUID, persistenceRequired, false
		}
		return true, int(arr[0]), int(arr[1]), int(arr[2]), uuid, hasUUID, persistenceRequired, true
	}
	return false, 0, 0, 0, uuid, hasUUID, persistenceRequired, true
}

// SaveCreakingEntityNBT snapshots `entityID`'s Creaking state into a vanilla 26.1.2 `*nbt.Compound`.
func (s *Server) SaveCreakingEntityNBT(entityID int32) (*nbt.Compound, bool) {
	st, ok := s.GetCreakingState(entityID)
	if !ok {
		return nil, false
	}
	if st.EntityUUID == ([16]byte{}) {
		if s.entities != nil {
			if ent := s.entities.Get(entityID); ent != nil && ent.UUID != ([16]byte{}) {
				st.EntityUUID = ent.UUID
			}
		}
		if st.EntityUUID == ([16]byte{}) {
			st.EntityUUID = syntheticEntityUUID2612(entityID)
		}
	}
	return EncodeCreakingEntityNBT(st), true
}

// LoadCreakingEntityNBT restores `"home_pos"`, `"UUID"`, and `"PersistenceRequired"`
// onto `entityID` and rebinds it to the `minecraft:creaking_heart` at `home_pos`
// (`Creaking.readAdditionalSaveData` + `setTransient`).
func (s *Server) LoadCreakingEntityNBT(entityID int32, c *nbt.Compound) bool {
	if s == nil || s.entities == nil || entityID <= 0 || c == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCreaking {
		return false
	}
	hasHome, hx, hy, hz, uuid, hasUUID, persist, ok := DecodeCreakingEntityNBT(c)
	if !ok {
		return false
	}
	if hasUUID {
		s.entities.Update(entityID, func(e *world.Entity) {
			e.UUID = uuid
		})
	}
	dim := ent.EffectiveDimension()
	cm := s.creakingRabbitMgr()
	cm.mu.Lock()
	st := cm.creaking[entityID]
	if st == nil {
		st = &CreakingState{
			EntityID:  entityID,
			Dimension: dim,
			CanMove:   true,
		}
		cm.creaking[entityID] = st
	}
	st.Dimension = dim
	st.PersistenceRequired = persist
	if hasUUID {
		st.EntityUUID = uuid
	} else if st.EntityUUID == ([16]byte{}) {
		st.EntityUUID = syntheticEntityUUID2612(entityID)
	}
	st.IsHeartBound = hasHome
	if hasHome {
		st.HeartX, st.HeartY, st.HeartZ = hx, hy, hz
		key := creakingBlockKey{dim: dim, x: hx, y: hy, z: hz}
		hs := cm.hearts[key]
		if hs == nil {
			natural := true
			if cache := s.DimensionChunks(dim); cache != nil {
				if _, props, pOK := world.BlockStateProperties(cache.GetBlock(hx, hy, hz)); pOK {
					natural = props["natural"] == "true"
				}
			}
			hs = &CreakingHeartState{
				Dimension: dim,
				X:         hx,
				Y:         hy,
				Z:         hz,
				Natural:   natural,
			}
			cm.hearts[key] = hs
		}
		// Rebind if heart is unbound or matches this Creaking's UUID (preserving conflict check if another live protector owns it!).
		if hs.LinkedCreakingID == 0 || hs.LinkedCreakingID == entityID || (hs.HasLinkedUUID && hs.LinkedCreakingUUID == st.EntityUUID) {
			hs.LinkedCreakingID = entityID
			hs.LinkedCreakingUUID = st.EntityUUID
			hs.HasLinkedUUID = true
			hs.ReloadGraceTicks = 0
		}
	} else {
		st.HeartX, st.HeartY, st.HeartZ = 0, 0, 0
	}
	snap := *st
	cm.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeCreakingEntityDataPacket(entityID, snap.CanMove, snap.IsActive, snap.IsTearingDown, snap.IsHeartBound, snap.HeartX, snap.HeartY, snap.HeartZ))
	return true
}

// EncodeCreakingHeartBlockEntityNBT encodes a `CreakingHeartBlockEntity` into a
// vanilla 26.1.2 `*nbt.Compound` (`"id": "minecraft:creaking_heart"`, `"x"`, `"y"`, `"z"`,
// and `"creaking": IntArray[4]` when a protector is linked).
func EncodeCreakingHeartBlockEntityNBT(hs CreakingHeartState) *nbt.Compound {
	c := nbt.NewCompound()
	c.Set("id", nbt.String(CreakingHeartBlockID))
	c.Set("x", nbt.Int(int32(hs.X)))
	c.Set("y", nbt.Int(int32(hs.Y)))
	c.Set("z", nbt.Int(int32(hs.Z)))
	if hs.HasLinkedUUID && hs.LinkedCreakingUUID != ([16]byte{}) {
		c.Set("creaking", uuidBytesToIntArray2612(hs.LinkedCreakingUUID))
	} else if hs.LinkedCreakingID > 0 {
		c.Set("creaking", uuidBytesToIntArray2612(syntheticEntityUUID2612(hs.LinkedCreakingID)))
	}
	return c
}

// DecodeCreakingHeartBlockEntityNBT decodes `"creaking"` (`UUIDUtil.CODEC`) from a
// vanilla 26.1.2 `CreakingHeartBlockEntity` `*nbt.Compound`.
func DecodeCreakingHeartBlockEntityNBT(c *nbt.Compound) (creakingUUID [16]byte, hasCreaking, ok bool) {
	if c == nil {
		return [16]byte{}, false, false
	}
	ct, has := c.Get("creaking")
	if !has {
		return [16]byte{}, false, true
	}
	u, uOK := parseUUIDTag2612(ct)
	if !uOK {
		return [16]byte{}, false, false
	}
	return u, true, true
}

// SaveCreakingHeartBlockEntityNBT snapshots the `CreakingHeartBlockEntity` NBT at `(hx, hy, hz)`.
func (s *Server) SaveCreakingHeartBlockEntityNBT(dimension string, hx, hy, hz int) (*nbt.Compound, bool) {
	if s == nil {
		return nil, false
	}
	normDim := world.NormalizeDimension(dimension)
	key := creakingBlockKey{dim: normDim, x: hx, y: hy, z: hz}
	cm := s.creakingRabbitMgr()
	cm.mu.RLock()
	hs, ok := cm.hearts[key]
	var snap CreakingHeartState
	if ok && hs != nil {
		snap = *hs
		if !snap.HasLinkedUUID && snap.LinkedCreakingID > 0 {
			if cst := cm.creaking[snap.LinkedCreakingID]; cst != nil && cst.EntityUUID != ([16]byte{}) {
				snap.LinkedCreakingUUID = cst.EntityUUID
				snap.HasLinkedUUID = true
			}
		}
	}
	cm.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return EncodeCreakingHeartBlockEntityNBT(snap), true
}

// LoadCreakingHeartBlockEntityNBT loads `"creaking"` (`UUID`) onto the `CreakingHeart`
// at `(hx, hy, hz)` and rebinds any matching loaded `Creaking` entity (or starts the
// vanilla 30-tick reload grace window `ticksExisted < 30` if the entity chunk loads next).
func (s *Server) LoadCreakingHeartBlockEntityNBT(dimension string, hx, hy, hz int, c *nbt.Compound) bool {
	if s == nil || c == nil {
		return false
	}
	uuid, hasCreaking, ok := DecodeCreakingHeartBlockEntityNBT(c)
	if !ok {
		return false
	}
	normDim := world.NormalizeDimension(dimension)
	natural := true
	if cache := s.DimensionChunks(normDim); cache != nil {
		if _, props, pOK := world.BlockStateProperties(cache.GetBlock(hx, hy, hz)); pOK {
			natural = props["natural"] == "true"
		}
	}
	key := creakingBlockKey{dim: normDim, x: hx, y: hy, z: hz}

	cm := s.creakingRabbitMgr()
	cm.mu.Lock()
	hs := cm.hearts[key]
	if hs == nil {
		hs = &CreakingHeartState{
			Dimension: normDim,
			X:         hx,
			Y:         hy,
			Z:         hz,
			Natural:   natural,
		}
		cm.hearts[key] = hs
	}
	if !hasCreaking {
		hs.LinkedCreakingID = 0
		hs.LinkedCreakingUUID = [16]byte{}
		hs.HasLinkedUUID = false
		hs.ReloadGraceTicks = 0
		cm.mu.Unlock()
		return true
	}
	hs.LinkedCreakingUUID = uuid
	hs.HasLinkedUUID = true
	hs.LinkedCreakingID = 0
	hs.ReloadGraceTicks = 0
	for id, cst := range cm.creaking {
		if cst == nil || cst.Dimension != normDim {
			continue
		}
		entUUID := cst.EntityUUID
		if entUUID == ([16]byte{}) && s.entities != nil {
			if ent := s.entities.Get(id); ent != nil && ent.UUID != ([16]byte{}) {
				entUUID = ent.UUID
			} else {
				entUUID = syntheticEntityUUID2612(id)
			}
		}
		if entUUID == uuid {
			hs.LinkedCreakingID = id
			cst.IsHeartBound = true
			cst.HeartX, cst.HeartY, cst.HeartZ = hx, hy, hz
			break
		}
	}
	cm.mu.Unlock()
	return true
}

func broadcastCreakingCrumbleParticles2612(s *Server, dim string, x, y, z float64) {
	if s == nil {
		return
	}
	cx := int32(int64(math.Floor(x)) >> 4)
	cz := int32(int64(math.Floor(z)) >> 4)
	s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(0, EntityEventPoof))
}
