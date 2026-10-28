package server

import (
	"errors"
	"math"
	"math/rand"
	"strings"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Guardian, Elder Guardian, Shulker & Shulker Bullet constants for Minecraft 26.1.2 (protocol 775).
const (
	MobTypeShulker          = "minecraft:shulker"
	EntityTypeShulkerBullet = "minecraft:shulker_bullet"

	// BuiltInRegistries.ENTITY_TYPE fallback numeric IDs in 26.1.2 (protocol 775).
	EntityTypeShulkerID       = 112
	EntityTypeShulkerBulletID = 113

	// Guardian & Elder Guardian additional constants (vanilla 26.1.2).
	GuardianFollowRange           float64 = 16.0
	GuardianMagicLaserDamage      float32 = 1.0  // Secondary magic/armor-bypassing damage from Guardian laser
	ElderGuardianMagicLaserDamage float32 = 2.0  // Secondary magic/armor-bypassing damage from Elder Guardian laser
	GuardianFlopVelocityY         float64 = 0.40 // Land flop upward hop velocity when out of water
	GuardianFlopIntervalTicks     int     = 10   // Every 10 ticks while out of water on land
	GuardianXPReward              int32   = 10
	ElderGuardianXPReward         int32   = 10
	ElderGuardianHomeRadius       float64 = 16.0 // Ocean Monument room patrol radius
	TideArmorTrimTemplateItemID   string  = "minecraft:tide_armor_trim_smithing_template"

	// ClientboundGameEventPacket (0x26) event 10: plays Elder Guardian ghost appearance & curse sound.
	GameEventGuardianElderEffect byte = 10

	// Shulker stats & shell mechanics (Shulker.java in 26.1.2).
	DefaultShulkerMaxHealth   float32 = 30.0
	ShulkerXPReward           int32   = 5
	ShulkerShellItemID        string  = "minecraft:shulker_shell"
	ShulkerClosedArmorBonus   int     = 20   // +20 armor when Peek == 0 (80% melee damage reduction, deflects projectiles)
	ShulkerClosedDamageMult   float32 = 0.20 // 1.0 - (20 * 0.04) = 0.20
	ShulkerPeekClosed         byte    = 0    // Shell completely closed
	ShulkerPeekPeeking        byte    = 30   // Peeking (0.3 blocks open)
	ShulkerPeekOpen           byte    = 100  // Fully open (1.0 block open while targeting/firing)
	ShulkerDefaultColor       byte    = 16   // 0..15 = DyeColor (white..black), 16 = default undyed purple
	ShulkerAggroRange         float64 = 16.0
	ShulkerTeleportRadius     int     = 8 // +/- 8 blocks (17x17x17 search cube)
	ShulkerMaxTeleportTries   int     = 5
	ShulkerShootCooldownMin   int     = 20  // 1.0s min between bullet volleys
	ShulkerShootCooldownMax   int     = 100 // 5.0s max between bullet volleys
	ShulkerPeekDurationTicks  int     = 40
	ShulkerDuplicateCheckDist float64 = 8.0
	ShulkerMaxNearbyForDup    int     = 5

	// Shulker attachment Direction enum values (net.minecraft.core.Direction 0..5).
	ShulkerFaceDown  byte = 0 // Attached to block at (bx, by-1, bz)
	ShulkerFaceUp    byte = 1 // Attached to block at (bx, by+1, bz)
	ShulkerFaceNorth byte = 2 // Attached to block at (bx, by, bz-1)
	ShulkerFaceSouth byte = 3 // Attached to block at (bx, by, bz+1)
	ShulkerFaceWest  byte = 4 // Attached to block at (bx-1, by, bz)
	ShulkerFaceEast  byte = 5 // Attached to block at (bx+1, by, bz)

	// SynchedEntityData indices and serializers for Shulker in protocol 775 (26.1.2):
	// Shulker extends AbstractGolem -> PathfinderMob -> Mob (15) -> LivingEntity (8..14) -> Entity (0..7).
	EntityDataShulkerAttachFaceID byte = 16 // Shulker.DATA_ATTACH_FACE_ID (DIRECTION serializer = 12)
	EntityDataShulkerPeekID       byte = 17 // Shulker.DATA_PEEK_ID (BYTE serializer = 0)
	EntityDataShulkerColorID      byte = 18 // Shulker.DATA_COLOR_ID (BYTE serializer = 0)
	// EntityDataSerializerDirection (12) is already defined in hanging.go.

	// Shulker Bullet constants (ShulkerBullet.java in 26.1.2).
	ShulkerBulletSpeed           float64 = 0.30 // Homing speed along X, Y, Z axes
	ShulkerBulletDamage          float32 = 4.0  // 4.0 HP (2 hearts) on impact
	ShulkerBulletLevitationTicks int32   = 200  // 10 seconds (200 ticks) of Levitation I
	ShulkerBulletHitboxRadius    float64 = 0.85
	ShulkerBulletMaxLifetime     int     = 600 // 30 seconds max lifetime

	// Canonical Shulker & Shulker Bullet sounds in 26.1.2.
	SoundShulkerAmbient    = "minecraft:entity.shulker.ambient"
	SoundShulkerClose      = "minecraft:entity.shulker.close"
	SoundShulkerDeath      = "minecraft:entity.shulker.death"
	SoundShulkerHurt       = "minecraft:entity.shulker.hurt"
	SoundShulkerHurtClosed = "minecraft:entity.shulker.hurt_closed"
	SoundShulkerOpen       = "minecraft:entity.shulker.open"
	SoundShulkerShoot      = "minecraft:entity.shulker.shoot"
	SoundShulkerTeleport   = "minecraft:entity.shulker.teleport"
	SoundShulkerBulletHit  = "minecraft:entity.shulker_bullet.hit"
	SoundShulkerBulletHurt = "minecraft:entity.shulker_bullet.hurt"
)

// ShulkerState holds per-entity AI, attachment face, peek amount, dye color,
// and combat state for a `minecraft:shulker`.
type ShulkerState struct {
	EntityID            int32
	Dimension           string
	AttachFace          byte // 0..5 (Down, Up, North, South, West, East)
	Peek                byte // 0 (closed), 30 (peeking), 100 (open)
	Color               byte // 0..15 (DyeColor) or 16 (default purple)
	AnchorX             int
	AnchorY             int
	AnchorZ             int
	TargetPlayer        *PlayerSession
	TargetMobID         int32
	AttackCooldown      int
	PeekTimer           int
	PersistenceRequired bool
}

// ShulkerBulletState holds per-entity guided homing state for a `minecraft:shulker_bullet`.
type ShulkerBulletState struct {
	EntityID       int32
	Dimension      string
	ShooterID      int32
	TargetPlayer   *PlayerSession
	TargetEntityID int32
	StepDirX       float64
	StepDirY       float64
	StepDirZ       float64
	FlightSteps    int
	AgeTicks       int
}

type shulkerManager struct {
	mu               sync.RWMutex
	shulkers         map[int32]*ShulkerState
	bullets          map[int32]*ShulkerBulletState
	levitationStartY map[*PlayerSession]float64
}

var serverShulkerManagers sync.Map // map[*Server]*shulkerManager

func (s *Server) shulkerMgr() *shulkerManager {
	if s == nil {
		return &shulkerManager{
			shulkers:         make(map[int32]*ShulkerState),
			bullets:          make(map[int32]*ShulkerBulletState),
			levitationStartY: make(map[*PlayerSession]float64),
		}
	}
	if v, ok := serverShulkerManagers.Load(s); ok {
		return v.(*shulkerManager)
	}
	actual, _ := serverShulkerManagers.LoadOrStore(s, &shulkerManager{
		shulkers:         make(map[int32]*ShulkerState),
		bullets:          make(map[int32]*ShulkerBulletState),
		levitationStartY: make(map[*PlayerSession]float64),
	})
	return actual.(*shulkerManager)
}

// GuardianElderShulkerXPReward returns the vanilla 26.1.2 XP drop for
// `minecraft:guardian` (10), `minecraft:elder_guardian` (10), and `minecraft:shulker` (5).
func GuardianElderShulkerXPReward(typeName string) int32 {
	switch canonicalResourceName(typeName) {
	case MobTypeGuardian:
		return GuardianXPReward
	case MobTypeElderGuardian:
		return ElderGuardianXPReward
	case MobTypeShulker:
		return ShulkerXPReward
	default:
		return 0
	}
}

// ============================================================================
// 1. GUARDIAN & ELDER GUARDIAN POLISH (Laser LOS, Scale, Monument Hooks, Drops)
// ============================================================================

// HasGuardianLaserLineOfSight checks whether the straight segment from
// `(x0, y0, z0)` to `(x1, y1, z1)` in `cache` is unobstructed by solid blocks
// (air, water, bubble columns, and passable aquatic plants do not block the laser).
func HasGuardianLaserLineOfSight(cache *world.Cache, x0, y0, z0, x1, y1, z1 float64) bool {
	if cache == nil {
		return true
	}
	dx := x1 - x0
	dy := y1 - y0
	dz := z1 - z0
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist <= 0.5 {
		return true
	}
	steps := int(math.Ceil(dist * 2.0))
	startBX, startBY, startBZ := int(math.Floor(x0)), int(math.Floor(y0)), int(math.Floor(z0))
	endBX, endBY, endBZ := int(math.Floor(x1)), int(math.Floor(y1)), int(math.Floor(z1))
	for i := 1; i < steps; i++ {
		t := float64(i) / float64(steps)
		px := x0 + dx*t
		py := y0 + dy*t
		pz := z0 + dz*t
		bx, by, bz := int(math.Floor(px)), int(math.Floor(py)), int(math.Floor(pz))
		if (bx == startBX && by == startBY && bz == startBZ) || (bx == endBX && by == endBY && bz == endBZ) {
			continue
		}
		st := cache.GetBlock(bx, by, bz)
		if st == world.StateAir || st == world.StateWater {
			continue
		}
		name := world.BlockName(st)
		if name == "minecraft:water" || name == "minecraft:bubble_column" ||
			name == "minecraft:kelp" || name == "minecraft:kelp_plant" ||
			name == "minecraft:seagrass" || name == "minecraft:tall_seagrass" ||
			isPassableBlock(st) {
			continue
		}
		return false
	}
	return true
}

// GuardianLaserScale returns the normalized `[0.0 .. 1.0]` laser beam charge
// progress for a Guardian (`80` ticks) or Elder Guardian (`60` ticks).
func (s *Server) GuardianLaserScale(entityID int32) float32 {
	st, ok := s.GetGuardianState(entityID)
	if !ok || st.AttackTargetID == 0 || st.LaserChargeTicks <= 0 {
		return 0
	}
	req := GuardianLaserChargeTicks
	if st.IsElder {
		req = ElderGuardianLaserChargeTicks
	}
	scale := float32(st.LaserChargeTicks) / float32(req)
	if scale > 1.0 {
		scale = 1.0
	}
	return scale
}

// SetGuardianHome configures the Ocean Monument home anchor `(hx, hy, hz)` and
// patrol radius for a Guardian or Elder Guardian.
func (s *Server) SetGuardianHome(entityID int32, hx, hy, hz, radius float64) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	if radius <= 0 {
		radius = ElderGuardianHomeRadius
	}
	mgr := s.aquaticManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st := mgr.guardians[entityID]
	if st == nil {
		return false
	}
	st.HasHome = true
	st.HomeX = hx
	st.HomeY = hy
	st.HomeZ = hz
	st.HomeRadius = radius
	return true
}

// SetGuardianPersistenceRequired marks a Guardian or Elder Guardian as persistent
// (`PersistenceRequired = true`, as used for the 3 Ocean Monument Elder Guardians).
func (s *Server) SetGuardianPersistenceRequired(entityID int32, persistent bool) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	mgr := s.aquaticManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st := mgr.guardians[entityID]
	if st == nil {
		return false
	}
	st.PersistenceRequired = persistent
	return true
}

// SpawnMonumentElderGuardiansAt spawns the 3 persistent `minecraft:elder_guardian`
// bosses of an Ocean Monument (penthouse, left wing, right wing) around `(centerX, centerY, centerZ)`
// and anchors each to its wing/room with `PersistenceRequired = true`.
func (s *Server) SpawnMonumentElderGuardiansAt(dimension string, centerX, centerY, centerZ float64) ([]int32, error) {
	if s == nil || s.entities == nil {
		return nil, errors.New("server: nil entity manager")
	}
	offsets := [3][3]float64{
		{0.0, 14.0, 0.0},   // Penthouse top room
		{-16.0, 0.0, -14.0}, // Left wing
		{17.0, 0.0, -14.0},  // Right wing
	}
	ids := make([]int32, 0, 3)
	for _, off := range offsets {
		gx := centerX + off[0]
		gy := centerY + off[1]
		gz := centerZ + off[2]
		id, err := s.SpawnElderGuardian(dimension, gx, gy, gz)
		if err != nil {
			return ids, err
		}
		s.SetGuardianHome(id, gx, gy, gz, ElderGuardianHomeRadius)
		s.SetGuardianPersistenceRequired(id, true)
		ids = append(ids, id)
	}
	return ids, nil
}

// OnGuardianThornsRetaliateMob applies `2.0 HP` (`GuardianThornsDamage`) spike
// retaliation damage to a non-player melee attacker (e.g. Axolotl or Iron Golem)
// when hitting a Guardian or Elder Guardian whose spikes are extended (`!Moving`).
func (s *Server) OnGuardianThornsRetaliateMob(entityID, attackerMobID int32) (retaliated bool, dealt float32) {
	if s == nil || s.entities == nil || entityID <= 0 || attackerMobID <= 0 {
		return false, 0
	}
	st, ok := s.GetGuardianState(entityID)
	if !ok || st.Moving {
		return false, 0
	}
	attEnt := s.entities.Get(attackerMobID)
	if attEnt == nil {
		return false, 0
	}
	s.applyDamageToMob(attEnt, GuardianThornsDamage, nil, 0.2, false, false)
	return true, GuardianThornsDamage
}

// OnGuardianOrElderKilled cleans up GuardianBehaviorState when a Guardian or
// Elder Guardian dies.
func (s *Server) OnGuardianOrElderKilled(entityID int32) {
	if s == nil || entityID <= 0 {
		return
	}
	mgr := s.aquaticManager()
	mgr.mu.Lock()
	delete(mgr.guardians, entityID)
	mgr.mu.Unlock()
}

func rollGuardianRareFish(rng *rand.Rand) string {
	if rng == nil {
		return "minecraft:cod"
	}
	// Vanilla Fishing/Guardian rare fish weights: cod 60%, salmon 25%, tropical_fish 2%, pufferfish 13%.
	r := rng.Intn(100)
	switch {
	case r < 60:
		return "minecraft:cod"
	case r < 85:
		return "minecraft:salmon"
	case r < 87:
		return "minecraft:tropical_fish"
	default:
		return "minecraft:pufferfish"
	}
}

// RollGuardianDeathLoot rolls vanilla 26.1.2 `minecraft:guardian` death loot:
//   - Pool 1: `0..2` `minecraft:prismarine_shard` (+ `0..lootingLevel`)
//   - Pool 2 (`weight 2:2:1`):
//     - 40% `1` `minecraft:cod` (`minecraft:cooked_cod` if `onFire`) (+ `0..lootingLevel`)
//     - 40% `1` `minecraft:prismarine_crystals` (+ `0..lootingLevel`)
//     - 20% empty
//   - Pool 3 (when `killedByPlayer`): `2.5% + 1% * lootingLevel` rare fish drop.
func RollGuardianDeathLoot(rng *rand.Rand, lootingLevel int, onFire, killedByPlayer bool) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	var out []world.ItemStack

	shards := int32(1)
	if rng != nil {
		shards = int32(rng.Intn(3)) // 0..2
		if lootingLevel > 0 {
			shards += int32(rng.Intn(lootingLevel + 1))
		}
	}
	if shards > 0 {
		if s1 := world.NewNamedItemStack(PrismarineShardItemID, shards); !s1.IsEmpty() {
			out = append(out, s1)
		}
	}

	poolRoll := 0
	if rng != nil {
		poolRoll = rng.Intn(5) // 0,1 = fish; 2,3 = crystals; 4 = empty
	}
	if poolRoll < 2 {
		cnt := int32(1)
		if rng != nil && lootingLevel > 0 {
			cnt += int32(rng.Intn(lootingLevel + 1))
		}
		fishID := "minecraft:cod"
		if onFire {
			fishID = "minecraft:cooked_cod"
		}
		if fStack := world.NewNamedItemStack(fishID, cnt); !fStack.IsEmpty() {
			out = append(out, fStack)
		}
	} else if poolRoll < 4 {
		cnt := int32(1)
		if rng != nil && lootingLevel > 0 {
			cnt += int32(rng.Intn(lootingLevel + 1))
		}
		if cStack := world.NewNamedItemStack(PrismarineCrystalsItemID, cnt); !cStack.IsEmpty() {
			out = append(out, cStack)
		}
	}

	if killedByPlayer && rng != nil {
		chance := 0.025 + 0.01*float64(lootingLevel)
		if rng.Float64() < chance {
			if rf := world.NewNamedItemStack(rollGuardianRareFish(rng), 1); !rf.IsEmpty() {
				out = append(out, rf)
			}
		}
	}
	return out
}

// RollElderGuardianDeathLoot rolls vanilla 26.1.2 `minecraft:elder_guardian` death loot:
//   - Pool 1: `0..2` `minecraft:prismarine_shard` (+ `0..lootingLevel`)
//   - Pool 2 (`weight 3:2:1`): 50% `cod`/`cooked_cod`, 33.3% `prismarine_crystals`, 16.7% empty
//   - Pool 3 (when `killedByPlayer`): `1` `minecraft:wet_sponge`
//   - Pool 4 (when `killedByPlayer`): `2.5% + 1% * lootingLevel` rare fish drop
//   - Pool 5: `20%` (`1/5`) `1` `minecraft:tide_armor_trim_smithing_template`.
func RollElderGuardianDeathLoot(rng *rand.Rand, lootingLevel int, onFire, killedByPlayer bool) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	var out []world.ItemStack

	shards := int32(2)
	if rng != nil {
		shards = int32(rng.Intn(3)) // 0..2
		if lootingLevel > 0 {
			shards += int32(rng.Intn(lootingLevel + 1))
		}
	}
	if shards > 0 {
		if s1 := world.NewNamedItemStack(PrismarineShardItemID, shards); !s1.IsEmpty() {
			out = append(out, s1)
		}
	}

	if killedByPlayer {
		if sponge := world.NewNamedItemStack(WetSpongeItemID, 1); !sponge.IsEmpty() {
			out = append(out, sponge)
		}
	}

	poolRoll := 0
	if rng != nil {
		poolRoll = rng.Intn(6) // 0,1,2 = fish; 3,4 = crystals; 5 = empty
	}
	if poolRoll < 3 {
		cnt := int32(1)
		if rng != nil && lootingLevel > 0 {
			cnt += int32(rng.Intn(lootingLevel + 1))
		}
		fishID := "minecraft:cod"
		if onFire {
			fishID = "minecraft:cooked_cod"
		}
		if fStack := world.NewNamedItemStack(fishID, cnt); !fStack.IsEmpty() {
			out = append(out, fStack)
		}
	} else if poolRoll < 5 {
		cnt := int32(1)
		if rng != nil && lootingLevel > 0 {
			cnt += int32(rng.Intn(lootingLevel + 1))
		}
		if cStack := world.NewNamedItemStack(PrismarineCrystalsItemID, cnt); !cStack.IsEmpty() {
			out = append(out, cStack)
		}
	}

	if killedByPlayer && rng != nil {
		chance := 0.025 + 0.01*float64(lootingLevel)
		if rng.Float64() < chance {
			if rf := world.NewNamedItemStack(rollGuardianRareFish(rng), 1); !rf.IsEmpty() {
				out = append(out, rf)
			}
		}
	}

	// 20% chance (1 in 5) for Tide Armor Trim Smithing Template.
	if rng != nil && rng.Intn(5) == 0 {
		if trim := world.NewNamedItemStack(TideArmorTrimTemplateItemID, 1); !trim.IsEmpty() {
			out = append(out, trim)
		}
	}
	return out
}

// ============================================================================
// 2. SHULKER (minecraft:shulker) & SHULKER BULLET (minecraft:shulker_bullet)
// ============================================================================

// EncodeShulkerEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating `Shulker.DATA_ATTACH_FACE_ID` (16, DIRECTION), `Shulker.DATA_PEEK_ID`
// (17, BYTE), and `Shulker.DATA_COLOR_ID` (18, BYTE) for protocol 775.
func EncodeShulkerEntityDataPacket(entityID int32, attachFace, peek, color byte) []byte {
	if attachFace > 5 {
		attachFace = ShulkerFaceDown
	}
	if color > ShulkerDefaultColor {
		color = ShulkerDefaultColor
	}
	w := protocol.NewWriter(24)
	w.VarInt(entityID)

	w.Byte(EntityDataShulkerAttachFaceID)
	w.VarInt(EntityDataSerializerDirection)
	w.VarInt(int32(attachFace))

	w.Byte(EntityDataShulkerPeekID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(peek)

	w.Byte(EntityDataShulkerColorID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(color)

	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeShulkerEntityDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeShulkerEntityDataPacket.
func DecodeShulkerEntityDataPacket(data []byte) (entityID int32, attachFace, peek, color byte, err error) {
	r := protocol.NewReader(data)
	entityID, err = r.VarInt()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, 0, 0, 0, err
		}
		if idx == 0xFF {
			return entityID, attachFace, peek, color, nil
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, 0, 0, 0, err
		}
		switch idx {
		case EntityDataShulkerAttachFaceID:
			if ser != EntityDataSerializerDirection {
				return 0, 0, 0, 0, errors.New("shulker: unexpected attach_face serializer")
			}
			v, err := r.VarInt()
			if err != nil {
				return 0, 0, 0, 0, err
			}
			attachFace = byte(v)
		case EntityDataShulkerPeekID:
			if ser != EntityDataSerializerByte {
				return 0, 0, 0, 0, errors.New("shulker: unexpected peek serializer")
			}
			peek, err = r.ReadByte()
			if err != nil {
				return 0, 0, 0, 0, err
			}
		case EntityDataShulkerColorID:
			if ser != EntityDataSerializerByte {
				return 0, 0, 0, 0, errors.New("shulker: unexpected color serializer")
			}
			color, err = r.ReadByte()
			if err != nil {
				return 0, 0, 0, 0, err
			}
		default:
			return 0, 0, 0, 0, errors.New("shulker: unexpected entity data index")
		}
	}
}

// shulkerFaceDelta returns the unit block offset `(dx, dy, dz)` from a Shulker's
// block position `(bx, by, bz)` to the adjacent support block it attaches to.
func shulkerFaceDelta(face byte) (dx, dy, dz int) {
	switch face {
	case ShulkerFaceDown:
		return 0, -1, 0
	case ShulkerFaceUp:
		return 0, 1, 0
	case ShulkerFaceNorth:
		return 0, 0, -1
	case ShulkerFaceSouth:
		return 0, 0, 1
	case ShulkerFaceWest:
		return -1, 0, 0
	case ShulkerFaceEast:
		return 1, 0, 0
	default:
		return 0, -1, 0
	}
}

// CanShulkerAttachToFace reports whether a Shulker at block `(bx, by, bz)` in
// `cache` can attach its base to the adjacent block in direction `face` (`0..5`).
func CanShulkerAttachToFace(cache *world.Cache, bx, by, bz int, face byte) bool {
	if face > 5 {
		return false
	}
	if cache == nil {
		return true
	}
	if by < world.MinY || by >= world.MinY+world.WorldHeight {
		return false
	}
	curState := cache.GetBlock(bx, by, bz)
	if curState != world.StateAir && !isPassableBlock(curState) {
		return false
	}
	dx, dy, dz := shulkerFaceDelta(face)
	ax, ay, az := bx+dx, by+dy, bz+dz
	if ay < world.MinY || ay >= world.MinY+world.WorldHeight {
		return false
	}
	anchorState := cache.GetBlock(ax, ay, az)
	if anchorState == world.StateAir || anchorState == world.StateWater || anchorState == world.StateLava || isPassableBlock(anchorState) {
		return false
	}
	anchorName := world.BlockName(anchorState)
	if anchorName == "minecraft:water" || anchorName == "minecraft:lava" ||
		anchorName == "minecraft:Moving_piston" || strings.HasSuffix(anchorName, "shulker_box") {
		return false
	}
	return true
}

// FindValidShulkerAttachFace checks `preferredFace` first and then all 6 block
// faces (`down`, `up`, `north`, `south`, `west`, `east`) around `(bx, by, bz)`
// to find a solid adjacent block face that a Shulker can attach to.
func FindValidShulkerAttachFace(cache *world.Cache, bx, by, bz int, preferredFace byte) (byte, bool) {
	if preferredFace <= 5 && CanShulkerAttachToFace(cache, bx, by, bz, preferredFace) {
		return preferredFace, true
	}
	for face := byte(0); face <= 5; face++ {
		if face == preferredFace {
			continue
		}
		if CanShulkerAttachToFace(cache, bx, by, bz, face) {
			return face, true
		}
	}
	return ShulkerFaceDown, false
}

// SpawnShulker spawns a default-colored (`16` = purple) `minecraft:shulker`
// (`30.0 HP`) at `(x, y, z)` in `dimension`, attaching to the nearest valid
// adjacent solid block face (defaulting to `ShulkerFaceDown`).
func (s *Server) SpawnShulker(dimension string, x, y, z float64) (int32, error) {
	return s.SpawnShulkerWithFaceAndColor(dimension, x, y, z, ShulkerFaceDown, ShulkerDefaultColor)
}

// SpawnShulkerWithFaceAndColor spawns a `minecraft:shulker` at `(x, y, z)` in
// `dimension` with the given `attachFace` (`0..5`) and `color` (`0..16`).
func (s *Server) SpawnShulkerWithFaceAndColor(dimension string, x, y, z float64, attachFace, color byte) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	bx := int(math.Floor(x))
	by := int(math.Floor(y))
	bz := int(math.Floor(z))
	snapX := float64(bx) + 0.5
	snapY := float64(by)
	snapZ := float64(bz) + 0.5

	if attachFace > 5 {
		attachFace = ShulkerFaceDown
	}
	if color > ShulkerDefaultColor {
		color = ShulkerDefaultColor
	}
	if cache := s.DimensionChunks(normDim); cache != nil {
		if validFace, ok := FindValidShulkerAttachFace(cache, bx, by, bz, attachFace); ok {
			attachFace = validFace
		}
	}
	dx, dy, dz := shulkerFaceDelta(attachFace)

	typeID := ResolveEntityTypeID(MobTypeShulker)
	if typeID < 0 {
		typeID = EntityTypeShulkerID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeShulker,
		Dimension: normDim,
		X:         snapX,
		Y:         snapY,
		Z:         snapZ,
		OnGround:  true,
	})
	s.SetMobHealth(id, DefaultShulkerMaxHealth)

	sm := s.shulkerMgr()
	sm.mu.Lock()
	sm.shulkers[id] = &ShulkerState{
		EntityID:   id,
		Dimension:  normDim,
		AttachFace: attachFace,
		Peek:       ShulkerPeekClosed,
		Color:      color,
		AnchorX:    bx + dx,
		AnchorY:    by + dy,
		AnchorZ:    bz + dz,
	}
	sm.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeShulkerEntityDataPacket(id, attachFace, ShulkerPeekClosed, color))
	}
	return id, nil
}

func (s *Server) ensureShulkerState(entityID int32) *ShulkerState {
	if s == nil || entityID <= 0 {
		return nil
	}
	sm := s.shulkerMgr()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	st, ok := sm.shulkers[entityID]
	if !ok || st == nil {
		dim := world.DimensionEnd
		bx, by, bz := 0, 64, 0
		if s.entities != nil {
			if ent := s.entities.Get(entityID); ent != nil {
				dim = ent.EffectiveDimension()
				bx = int(math.Floor(ent.X))
				by = int(math.Floor(ent.Y))
				bz = int(math.Floor(ent.Z))
			}
		}
		st = &ShulkerState{
			EntityID:   entityID,
			Dimension:  dim,
			AttachFace: ShulkerFaceDown,
			Peek:       ShulkerPeekClosed,
			Color:      ShulkerDefaultColor,
			AnchorX:    bx,
			AnchorY:    by - 1,
			AnchorZ:    bz,
		}
		sm.shulkers[entityID] = st
	}
	return st
}

// GetShulkerState returns a snapshot of the ShulkerState for entityID.
func (s *Server) GetShulkerState(entityID int32) (ShulkerState, bool) {
	if s == nil || entityID <= 0 {
		return ShulkerState{}, false
	}
	sm := s.shulkerMgr()
	sm.mu.RLock()
	st, ok := sm.shulkers[entityID]
	var out ShulkerState
	if ok && st != nil {
		out = *st
	}
	sm.mu.RUnlock()
	return out, ok && st != nil
}

// SetShulkerPeek updates a Shulker's shell peek state (`0` = closed, `30` = peeking,
// `100` = fully open), broadcasting `EncodeShulkerEntityDataPacket` and playing
// `SoundShulkerOpen` / `SoundShulkerClose`.
func (s *Server) SetShulkerPeek(entityID int32, peek byte) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeShulker {
		return false
	}
	st := s.ensureShulkerState(entityID)
	sm := s.shulkerMgr()
	sm.mu.Lock()
	prev := st.Peek
	st.Peek = peek
	face := st.AttachFace
	color := st.Color
	sm.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeShulkerEntityDataPacket(entityID, face, peek, color))
	if prev == 0 && peek > 0 {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundShulkerOpen, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	} else if prev > 0 && peek == 0 {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundShulkerClose, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	}
	return true
}

// SetShulkerAttachFace updates the block face (`0..5`: down, up, north, south,
// west, east) that a Shulker is attached to and broadcasts `EncodeShulkerEntityDataPacket`.
func (s *Server) SetShulkerAttachFace(entityID int32, face byte) bool {
	if s == nil || s.entities == nil || entityID <= 0 || face > 5 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeShulker {
		return false
	}
	bx, by, bz := int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z))
	dx, dy, dz := shulkerFaceDelta(face)
	st := s.ensureShulkerState(entityID)
	sm := s.shulkerMgr()
	sm.mu.Lock()
	st.AttachFace = face
	st.AnchorX = bx + dx
	st.AnchorY = by + dy
	st.AnchorZ = bz + dz
	peek := st.Peek
	color := st.Color
	sm.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeShulkerEntityDataPacket(entityID, face, peek, color))
	return true
}

// SetShulkerColor sets a Shulker's dye color variant (`0..15` for the 16 vanilla
// dye colors, or `16` for default undyed purple) and broadcasts `EncodeShulkerEntityDataPacket`.
func (s *Server) SetShulkerColor(entityID int32, color byte) bool {
	if s == nil || s.entities == nil || entityID <= 0 || color > ShulkerDefaultColor {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeShulker {
		return false
	}
	st := s.ensureShulkerState(entityID)
	sm := s.shulkerMgr()
	sm.mu.Lock()
	st.Color = color
	face := st.AttachFace
	peek := st.Peek
	sm.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeShulkerEntityDataPacket(entityID, face, peek, color))
	return true
}

// DyeColorFromItemID maps a `minecraft:<color>_dye` item ID to its `0..15`
// `DyeColor` byte index.
func DyeColorFromItemID(itemID string) (byte, bool) {
	switch canonicalResourceName(itemID) {
	case "minecraft:white_dye":
		return 0, true
	case "minecraft:orange_dye":
		return 1, true
	case "minecraft:magenta_dye":
		return 2, true
	case "minecraft:light_blue_dye":
		return 3, true
	case "minecraft:yellow_dye":
		return 4, true
	case "minecraft:lime_dye":
		return 5, true
	case "minecraft:pink_dye":
		return 6, true
	case "minecraft:gray_dye":
		return 7, true
	case "minecraft:light_gray_dye":
		return 8, true
	case "minecraft:cyan_dye":
		return 9, true
	case "minecraft:purple_dye":
		return 10, true
	case "minecraft:blue_dye":
		return 11, true
	case "minecraft:brown_dye":
		return 12, true
	case "minecraft:green_dye":
		return 13, true
	case "minecraft:red_dye":
		return 14, true
	case "minecraft:black_dye":
		return 15, true
	default:
		return 0, false
	}
}

// DyeShulker dyes a `minecraft:shulker` using a dye item (`minecraft:<color>_dye`),
// updating `DATA_COLOR_ID` (`0..15`).
func (s *Server) DyeShulker(session *PlayerSession, entityID int32, dyeItemID string) (newColor byte, dyed bool) {
	colorIdx, ok := DyeColorFromItemID(dyeItemID)
	if !ok {
		return 0, false
	}
	st, exists := s.GetShulkerState(entityID)
	if !exists || st.Color == colorIdx {
		return st.Color, false
	}
	if !s.SetShulkerColor(entityID, colorIdx) {
		return 0, false
	}
	if session != nil {
		consumeOneHeldItemInSurvival(session)
	}
	return colorIdx, true
}

// ScaleShulkerDamage evaluates incoming damage against a `minecraft:shulker`:
//   - When the Shulker's shell is closed (`Peek == 0`):
//     - Projectiles (`isProjectile == true`) are completely deflected (`scaledDamage = 0`, `deflected = true`).
//     - Melee/other attacks are reduced by +20 armor (`scaledDamage = rawDamage * 0.20`).
//   - When peeking or open (`Peek > 0`), takes full `rawDamage`.
func (s *Server) ScaleShulkerDamage(entityID int32, rawDamage float32, isProjectile bool) (scaledDamage float32, deflected bool) {
	if s == nil || rawDamage <= 0 {
		return 0, false
	}
	st, ok := s.GetShulkerState(entityID)
	if !ok || st.Peek > 0 {
		return rawDamage, false
	}
	if isProjectile {
		if s.entities != nil {
			if ent := s.entities.Get(entityID); ent != nil {
				cp := ent.ChunkPos()
				s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket(SoundShulkerHurtClosed, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			}
		}
		return 0, true
	}
	return rawDamage * ShulkerClosedDamageMult, false
}

// TryTeleportShulker attempts up to 5 times to teleport a `minecraft:shulker`
// within a 17x17x17 cube (`+/- 8` blocks) to an air block that has a valid solid
// adjacent block face (`FindValidShulkerAttachFace`), snapping to block-center
// coordinates, closing its shell (`Peek = 0`), and broadcasting `SoundShulkerTeleport`.
func (s *Server) TryTeleportShulker(entityID int32, rng *rand.Rand) (newX, newY, newZ float64, newFace byte, teleported bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return 0, 0, 0, 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeShulker {
		return 0, 0, 0, 0, false
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	curBX := int(math.Floor(ent.X))
	curBY := int(math.Floor(ent.Y))
	curBZ := int(math.Floor(ent.Z))

	for attempt := 0; attempt < ShulkerMaxTeleportTries; attempt++ {
		var dx, dy, dz int
		if rng != nil {
			dx = rng.Intn(2*ShulkerTeleportRadius+1) - ShulkerTeleportRadius
			dy = rng.Intn(2*ShulkerTeleportRadius+1) - ShulkerTeleportRadius
			dz = rng.Intn(2*ShulkerTeleportRadius+1) - ShulkerTeleportRadius
		} else {
			dx = attempt + 2
			dy = 0
			dz = 0
		}
		if dx == 0 && dy == 0 && dz == 0 {
			dx = 2
		}
		tx, ty, tz := curBX+dx, curBY+dy, curBZ+dz
		if ty < world.MinY+1 || ty >= world.MinY+world.WorldHeight-1 {
			continue
		}
		prefFace := byte(0)
		if rng != nil {
			prefFace = byte(rng.Intn(6))
		}
		face, ok := FindValidShulkerAttachFace(cache, tx, ty, tz, prefFace)
		if !ok {
			continue
		}
		// Ensure no other Shulker already occupies (tx, ty, tz).
		occupied := false
		allEnts := s.entities.AppendAll(nil)
		for i := range allEnts {
			other := &allEnts[i]
			if other.ID != entityID && other.EffectiveDimension() == dim &&
				int(math.Floor(other.X)) == tx && int(math.Floor(other.Y)) == ty && int(math.Floor(other.Z)) == tz {
				occupied = true
				break
			}
		}
		if occupied {
			continue
		}

		newX = float64(tx) + 0.5
		newY = float64(ty)
		newZ = float64(tz) + 0.5
		newFace = face
		adx, ady, adz := shulkerFaceDelta(face)

		st := s.ensureShulkerState(entityID)
		sm := s.shulkerMgr()
		sm.mu.Lock()
		st.AttachFace = face
		st.Peek = ShulkerPeekClosed
		st.AnchorX = tx + adx
		st.AnchorY = ty + ady
		st.AnchorZ = tz + adz
		color := st.Color
		sm.mu.Unlock()

		s.entities.Update(entityID, func(e *world.Entity) {
			e.X = newX
			e.Y = newY
			e.Z = newZ
			e.VelocityX = 0
			e.VelocityY = 0
			e.VelocityZ = 0
			e.OnGround = true
		})
		if updated := s.entities.Get(entityID); updated != nil {
			cp := updated.ChunkPos()
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayTeleportEntity, encodeTeleportEntityPacket(nil, updated))
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
				EncodeShulkerEntityDataPacket(entityID, face, ShulkerPeekClosed, color))
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundShulkerTeleport, SoundSourceHostile, newX, newY, newZ, 1.0, 1.0, 0))
		}
		return newX, newY, newZ, newFace, true
	}
	return ent.X, ent.Y, ent.Z, 0, false
}

// OnBlockBrokenNearShulker checks whether any `minecraft:shulker` was anchored
// to the broken/moved block at `(bx, by, bz)` in `dimension`; if so, re-attaches
// the Shulker to another adjacent solid block face or teleports it away.
func (s *Server) OnBlockBrokenNearShulker(dimension string, bx, by, bz int, rng *rand.Rand) int {
	if s == nil || s.entities == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	sm := s.shulkerMgr()
	sm.mu.RLock()
	var affected []int32
	for id, st := range sm.shulkers {
		if st != nil && st.Dimension == normDim && st.AnchorX == bx && st.AnchorY == by && st.AnchorZ == bz {
			affected = append(affected, id)
		}
	}
	sm.mu.RUnlock()

	relocated := 0
	for _, id := range affected {
		ent := s.entities.Get(id)
		if ent == nil {
			continue
		}
		sbx, sby, sbz := int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z))
		if altFace, ok := FindValidShulkerAttachFace(cache, sbx, sby, sbz, ShulkerFaceDown); ok {
			s.SetShulkerAttachFace(id, altFace)
			relocated++
		} else if _, _, _, _, tele := s.TryTeleportShulker(id, rng); tele {
			relocated++
		}
	}
	return relocated
}

// OnShulkerDamaged handles post-damage reactions for a `minecraft:shulker`:
//   - Sets `TargetPlayer` to `attacker`
//   - When remaining health is `< 50%` of `maxHP` (`< 15.0 HP`), teleports away
//     with a 25% chance (`rng.Intn(4) == 0`, or deterministically when `rng == nil`)
//     matching vanilla `Shulker.hurtServer`.
func (s *Server) OnShulkerDamaged(entityID int32, remainingHP, maxHP float32, attacker *PlayerSession, rng *rand.Rand) (teleported bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	st := s.ensureShulkerState(entityID)
	if st == nil {
		return false
	}
	sm := s.shulkerMgr()
	sm.mu.Lock()
	if attacker != nil {
		st.TargetPlayer = attacker
	}
	sm.mu.Unlock()

	if maxHP <= 0 {
		maxHP = DefaultShulkerMaxHealth
	}
	if remainingHP > 0 && remainingHP < maxHP*0.5 {
		shouldTeleport := true
		if rng != nil {
			shouldTeleport = rng.Intn(4) == 0
		}
		if shouldTeleport {
			_, _, _, _, teleported = s.TryTeleportShulker(entityID, rng)
		}
	}
	return teleported
}

// OnShulkerHitByShulkerBullet implements vanilla 26.1.2 `Shulker.hitByShulkerBullet`:
// when a Shulker is struck by a `minecraft:shulker_bullet`, it teleports away
// and, if fewer than 5 other Shulkers are within 8 blocks, has a `(5 - count)/5`
// chance to spawn a new Shulker of the same color at its previous position!
func (s *Server) OnShulkerHitByShulkerBullet(entityID int32, rng *rand.Rand) (newShulkerID int32, duplicated bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeShulker {
		return 0, false
	}
	oldX, oldY, oldZ := ent.X, ent.Y, ent.Z
	dim := ent.EffectiveDimension()
	st, _ := s.GetShulkerState(entityID)

	_, _, _, _, tele := s.TryTeleportShulker(entityID, rng)
	if !tele {
		return 0, false
	}

	// Count other Shulkers within 8 blocks of old position.
	nearbyCount := 0
	maxDistSq := ShulkerDuplicateCheckDist * ShulkerDuplicateCheckDist
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		other := &allEnts[i]
		if other.ID == entityID || other.EffectiveDimension() != dim || canonicalResourceName(other.TypeName) != MobTypeShulker {
			continue
		}
		dx := other.X - oldX
		dy := other.Y - oldY
		dz := other.Z - oldZ
		if dx*dx+dy*dy+dz*dz <= maxDistSq {
			nearbyCount++
		}
	}
	if nearbyCount >= ShulkerMaxNearbyForDup {
		return 0, false
	}
	chance := float64(ShulkerMaxNearbyForDup-nearbyCount) / float64(ShulkerMaxNearbyForDup)
	if rng != nil && rng.Float64() >= chance {
		return 0, false
	}
	newID, err := s.SpawnShulkerWithFaceAndColor(dim, oldX, oldY, oldZ, st.AttachFace, st.Color)
	if err != nil || newID <= 0 {
		return 0, false
	}
	return newID, true
}

// SpawnShulkerBullet spawns a guided `minecraft:shulker_bullet` projectile fired
// by `shooterID` toward `targetPlayer` (or `targetEntityID`), selecting an initial
// axis-aligned step direction (`X`, `Y`, or `Z`) and broadcasting `SoundShulkerShoot`.
func (s *Server) SpawnShulkerBullet(dimension string, shooterID, targetEntityID int32, targetPlayer *PlayerSession, rng *rand.Rand) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	sx, sy, sz := 0.5, 64.5, 0.5
	if shooter := s.entities.Get(shooterID); shooter != nil {
		normDim = shooter.EffectiveDimension()
		sx = shooter.X
		sy = shooter.Y + 0.5
		sz = shooter.Z
	}
	if targetPlayer != nil && targetEntityID == 0 {
		targetEntityID = targetPlayer.EntityID
	}

	typeID := ResolveEntityTypeID(EntityTypeShulkerBullet)
	if typeID < 0 {
		typeID = EntityTypeShulkerBulletID
	}
	bulletID := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  EntityTypeShulkerBullet,
		Dimension: normDim,
		X:         sx,
		Y:         sy,
		Z:         sz,
		OnGround:  false,
	})

	dirX, dirY, dirZ := selectShulkerBulletAxisStep(sx, sy, sz, sx+1, sy, sz)
	if tx, ty, tz, ok := s.resolveShulkerBulletTargetPos(normDim, targetPlayer, targetEntityID); ok {
		dirX, dirY, dirZ = selectShulkerBulletAxisStep(sx, sy, sz, tx, ty, tz)
	}

	sm := s.shulkerMgr()
	sm.mu.Lock()
	sm.bullets[bulletID] = &ShulkerBulletState{
		EntityID:       bulletID,
		Dimension:      normDim,
		ShooterID:      shooterID,
		TargetPlayer:   targetPlayer,
		TargetEntityID: targetEntityID,
		StepDirX:       dirX,
		StepDirY:       dirY,
		StepDirZ:       dirZ,
		FlightSteps:    5,
	}
	sm.mu.Unlock()

	if ent := s.entities.Get(bulletID); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundShulkerShoot, SoundSourceHostile, sx, sy, sz, 2.0, 1.0, 0))
	}
	_ = rng
	return bulletID, nil
}

// selectShulkerBulletAxisStep selects the unit axis step (`+/-1` along X, Y, or Z)
// corresponding to the largest coordinate delta toward `(tx, ty, tz)` (matching
// vanilla `ShulkerBullet.selectNextMoveDirection` axis-guided pathing).
func selectShulkerBulletAxisStep(bx, by, bz, tx, ty, tz float64) (dx, dy, dz float64) {
	rx := tx - bx
	ry := ty - by
	rz := tz - bz
	ax := math.Abs(rx)
	ay := math.Abs(ry)
	az := math.Abs(rz)
	if ax >= ay && ax >= az {
		if rx < 0 {
			return -1, 0, 0
		}
		return 1, 0, 0
	}
	if ay >= ax && ay >= az {
		if ry < 0 {
			return 0, -1, 0
		}
		return 0, 1, 0
	}
	if rz < 0 {
		return 0, 0, -1
	}
	return 0, 0, 1
}

func (s *Server) resolveShulkerBulletTargetPos(dim string, targetPlayer *PlayerSession, targetEntityID int32) (x, y, z float64, ok bool) {
	if targetPlayer != nil && !targetPlayer.IsDead() {
		snap := targetPlayer.Snapshot()
		if world.NormalizeDimension(snap.Dimension) == dim && snap.GameMode != GameModeSpectator {
			return snap.X, snap.Y + 0.9, snap.Z, true
		}
	}
	if targetEntityID > 0 && s.entities != nil {
		if ent := s.entities.Get(targetEntityID); ent != nil && ent.EffectiveDimension() == dim {
			return ent.X, ent.Y + 0.5, ent.Z, true
		}
	}
	return 0, 0, 0, false
}

// DestroyShulkerBullet destroys a `minecraft:shulker_bullet` when a player or
// projectile hits it in mid-air (`SoundShulkerBulletHurt`).
func (s *Server) DestroyShulkerBullet(bulletID int32) bool {
	if s == nil || s.entities == nil || bulletID <= 0 {
		return false
	}
	ent := s.entities.Get(bulletID)
	if ent == nil || canonicalResourceName(ent.TypeName) != EntityTypeShulkerBullet {
		return false
	}
	sm := s.shulkerMgr()
	sm.mu.Lock()
	delete(sm.bullets, bulletID)
	sm.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.entities.Remove(bulletID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{bulletID}))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundShulkerBulletHurt, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true
}

// TrackPlayerShulkerLevitation updates the vertical distance a player has
// levitated while under `EffectLevitation` from a Shulker Bullet and triggers
// `minecraft:end/levitate` ("Great View From Up Here") at `>= 50.0` blocks.
func (s *Server) TrackPlayerShulkerLevitation(session *PlayerSession, currentY float64) (verticalDistance float64, granted bool) {
	if s == nil || session == nil || session.IsDead() {
		return 0, false
	}
	sm := s.shulkerMgr()
	if _, hasLev := session.PlayerEffect(EffectLevitation); !hasLev {
		sm.mu.Lock()
		delete(sm.levitationStartY, session)
		sm.mu.Unlock()
		return 0, false
	}
	sm.mu.Lock()
	startY, recorded := sm.levitationStartY[session]
	if !recorded {
		startY = currentY
		sm.levitationStartY[session] = startY
	}
	sm.mu.Unlock()

	verticalDistance = currentY - startY
	if verticalDistance >= 50.0 {
		s.TriggerLevitationDistance(session, verticalDistance)
		return verticalDistance, true
	}
	return verticalDistance, false
}

// OnShulkerKilled cleans up ShulkerState when a `minecraft:shulker` dies.
func (s *Server) OnShulkerKilled(entityID int32) {
	if s == nil || entityID <= 0 {
		return
	}
	sm := s.shulkerMgr()
	sm.mu.Lock()
	delete(sm.shulkers, entityID)
	sm.mu.Unlock()
}

// RollShulkerDeathLoot rolls vanilla 26.1.2 `minecraft:shulker` death loot:
// `50%` base chance (`+6.25%` per looting level) to drop `1x` `minecraft:shulker_shell`.
func RollShulkerDeathLoot(rng *rand.Rand, lootingLevel int) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	chance := 0.50 + 0.0625*float64(lootingLevel)
	drop := true
	if rng != nil {
		drop = rng.Float64() < chance
	}
	if drop {
		if shell := world.NewNamedItemStack(ShulkerShellItemID, 1); !shell.IsEmpty() {
			return []world.ItemStack{shell}
		}
	}
	return nil
}

// SpawnStructureEntity materializes a `world.StructureEntity` generated by an
// Ocean Monument (`minecraft:elder_guardian`), End City (`minecraft:shulker`),
// or other structure into the live entity manager with `PersistenceRequired`.
func (s *Server) SpawnStructureEntity(dimension string, se world.StructureEntity) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	switch canonicalResourceName(se.ID) {
	case MobTypeElderGuardian:
		id, err := s.SpawnElderGuardian(normDim, se.X, se.Y, se.Z)
		if err != nil {
			return 0, err
		}
		s.SetGuardianHome(id, se.X, se.Y, se.Z, ElderGuardianHomeRadius)
		s.SetGuardianPersistenceRequired(id, se.PersistenceRequired)
		return id, nil
	case MobTypeShulker:
		id, err := s.SpawnShulker(normDim, se.X, se.Y, se.Z)
		if err != nil {
			return 0, err
		}
		sm := s.shulkerMgr()
		sm.mu.Lock()
		if st := sm.shulkers[id]; st != nil {
			st.PersistenceRequired = se.PersistenceRequired
		}
		sm.mu.Unlock()
		return id, nil
	default:
		return s.SpawnMobInDimension(normDim, se.X, se.Y, se.Z, se.ID)
	}
}

// TickShulkersAndBullets steps all active `minecraft:shulker` and
// `minecraft:shulker_bullet` entities by 1 server tick (20 TPS).
func (s *Server) TickShulkersAndBullets(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	sm := s.shulkerMgr()
	sm.mu.RLock()
	shulkerIDs := make([]int32, 0, len(sm.shulkers))
	for id := range sm.shulkers {
		shulkerIDs = append(shulkerIDs, id)
	}
	bulletIDs := make([]int32, 0, len(sm.bullets))
	for id := range sm.bullets {
		bulletIDs = append(bulletIDs, id)
	}
	sm.mu.RUnlock()

	for _, id := range shulkerIDs {
		s.tickSingleShulker(id, rng)
	}
	for _, id := range bulletIDs {
		s.tickSingleShulkerBullet(id, rng)
	}
}

func (s *Server) tickSingleShulker(entityID int32, rng *rand.Rand) {
	ent := s.entities.Get(entityID)
	sm := s.shulkerMgr()
	sm.mu.Lock()
	st, ok := sm.shulkers[entityID]
	if !ok || st == nil || ent == nil {
		delete(sm.shulkers, entityID)
		sm.mu.Unlock()
		return
	}
	if st.AttackCooldown > 0 {
		st.AttackCooldown--
	}
	if st.PeekTimer > 0 {
		st.PeekTimer--
	}
	snap := *st
	sm.mu.Unlock()

	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	bx, by, bz := int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z))

	// 1. Validate support block; if broken or submerged in fluid, re-attach or teleport!
	if cache != nil && !CanShulkerAttachToFace(cache, bx, by, bz, snap.AttachFace) {
		if altFace, ok := FindValidShulkerAttachFace(cache, bx, by, bz, ShulkerFaceDown); ok {
			s.SetShulkerAttachFace(entityID, altFace)
		} else {
			_, _, _, _, _ = s.TryTeleportShulker(entityID, rng)
			return
		}
	}

	if s.Difficulty() == DifficultyPeaceful {
		if snap.Peek != ShulkerPeekClosed {
			s.SetShulkerPeek(entityID, ShulkerPeekClosed)
		}
		return
	}

	// 2. Acquire nearest Survival/Adventure player target within 16 blocks.
	var bestPlayer *PlayerSession
	bestDistSq := ShulkerAggroRange*ShulkerAggroRange + 1.0
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		pSnap := p.Snapshot()
		if world.NormalizeDimension(pSnap.Dimension) != dim ||
			pSnap.GameMode == GameModeCreative || pSnap.GameMode == GameModeSpectator {
			continue
		}
		dx := pSnap.X - ent.X
		dy := pSnap.Y - ent.Y
		dz := pSnap.Z - ent.Z
		dSq := dx*dx + dy*dy + dz*dz
		if dSq <= ShulkerAggroRange*ShulkerAggroRange && dSq < bestDistSq {
			bestDistSq = dSq
			bestPlayer = p
		}
	}

	if bestPlayer != nil {
		if snap.Peek != ShulkerPeekOpen {
			s.SetShulkerPeek(entityID, ShulkerPeekOpen)
		}
		if snap.AttackCooldown <= 0 {
			nextCD := 40
			if rng != nil {
				nextCD = ShulkerShootCooldownMin + rng.Intn(ShulkerShootCooldownMax-ShulkerShootCooldownMin+1)
			}
			sm.mu.Lock()
			if cur := sm.shulkers[entityID]; cur != nil {
				cur.AttackCooldown = nextCD
				cur.TargetPlayer = bestPlayer
			}
			sm.mu.Unlock()
			_, _ = s.SpawnShulkerBullet(dim, entityID, bestPlayer.EntityID, bestPlayer, rng)
		}
		return
	}

	// 3. Idle peeking cycle when no target is in range.
	if snap.Peek == ShulkerPeekOpen {
		s.SetShulkerPeek(entityID, ShulkerPeekClosed)
	} else if snap.Peek == ShulkerPeekPeeking && snap.PeekTimer == 0 {
		s.SetShulkerPeek(entityID, ShulkerPeekClosed)
	} else if snap.Peek == ShulkerPeekClosed && rng != nil && rng.Intn(200) == 0 {
		sm.mu.Lock()
		if cur := sm.shulkers[entityID]; cur != nil {
			cur.PeekTimer = ShulkerPeekDurationTicks
		}
		sm.mu.Unlock()
		s.SetShulkerPeek(entityID, ShulkerPeekPeeking)
	}
}

func (s *Server) tickSingleShulkerBullet(bulletID int32, rng *rand.Rand) {
	ent := s.entities.Get(bulletID)
	sm := s.shulkerMgr()
	sm.mu.Lock()
	b, ok := sm.bullets[bulletID]
	if !ok || b == nil || ent == nil {
		delete(sm.bullets, bulletID)
		sm.mu.Unlock()
		return
	}
	b.AgeTicks++
	if b.AgeTicks > ShulkerBulletMaxLifetime {
		delete(sm.bullets, bulletID)
		sm.mu.Unlock()
		cp := ent.ChunkPos()
		s.entities.Remove(bulletID)
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayRemoveEntities,
			encodeRemoveEntitiesPacket(nil, []int32{bulletID}))
		return
	}

	dim := ent.EffectiveDimension()
	b.FlightSteps--
	if b.FlightSteps <= 0 {
		if tx, ty, tz, hasTarget := s.resolveShulkerBulletTargetPos(dim, b.TargetPlayer, b.TargetEntityID); hasTarget {
			b.StepDirX, b.StepDirY, b.StepDirZ = selectShulkerBulletAxisStep(ent.X, ent.Y, ent.Z, tx, ty, tz)
		}
		b.FlightSteps = 3
		if rng != nil {
			b.FlightSteps = 2 + rng.Intn(4)
		}
	}
	snap := *b
	sm.mu.Unlock()

	vx := snap.StepDirX * ShulkerBulletSpeed
	vy := snap.StepDirY * ShulkerBulletSpeed
	vz := snap.StepDirZ * ShulkerBulletSpeed
	nx := ent.X + vx
	ny := ent.Y + vy
	nz := ent.Z + vz

	// 1. Check solid block impact.
	if cache := s.DimensionChunks(dim); cache != nil {
		bx, by, bz := int(math.Floor(nx)), int(math.Floor(ny)), int(math.Floor(nz))
		st := cache.GetBlock(bx, by, bz)
		if st != world.StateAir && !isPassableBlock(st) {
			sm.mu.Lock()
			delete(sm.bullets, bulletID)
			sm.mu.Unlock()
			cp := ent.ChunkPos()
			s.entities.Remove(bulletID)
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
				encodeRemoveEntitiesPacket(nil, []int32{bulletID}))
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundShulkerBulletHit, SoundSourceHostile, nx, ny, nz, 1.0, 1.0, 0))
			return
		}
	}

	// 2. Check player impact (deals 4.0 HP damage + Levitation I for 200 ticks = 10s).
	hitRadiusSq := ShulkerBulletHitboxRadius * ShulkerBulletHitboxRadius
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		pSnap := p.Snapshot()
		if world.NormalizeDimension(pSnap.Dimension) != dim || pSnap.GameMode == GameModeSpectator {
			continue
		}
		dx := pSnap.X - nx
		dy := (pSnap.Y + 0.9) - ny
		dz := pSnap.Z - nz
		if dx*dx+dy*dy+dz*dz <= hitRadiusSq {
			sm.mu.Lock()
			delete(sm.bullets, bulletID)
			if _, exists := sm.levitationStartY[p]; !exists {
				sm.levitationStartY[p] = pSnap.Y
			}
			sm.mu.Unlock()

			cp := ent.ChunkPos()
			s.entities.Remove(bulletID)
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
				encodeRemoveEntitiesPacket(nil, []int32{bulletID}))
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundShulkerBulletHit, SoundSourceHostile, nx, ny, nz, 1.0, 1.0, 0))

			if pSnap.GameMode != GameModeCreative {
				s.AttackPlayerFromMob(snap.ShooterID, MobTypeShulker, nx, nz, p, ShulkerBulletDamage)
				s.ApplyPlayerEffect(p, NewStatusEffectInstance(EffectLevitation, ShulkerBulletLevitationTicks, 0))
			}
			return
		}
	}

	// 3. Check entity/Shulker impact (triggers Shulker Bullet duplication if hitting a Shulker!).
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		other := &allEnts[i]
		if other.ID == bulletID || other.IsItem() || other.IsExperienceOrb() || other.EffectiveDimension() != dim {
			continue
		}
		if other.ID == snap.ShooterID && snap.AgeTicks < 10 {
			continue
		}
		oNorm := canonicalResourceName(other.TypeName)
		if oNorm == EntityTypeShulkerBullet || IsDecorationEntity(oNorm) {
			continue
		}
		dx := other.X - nx
		dy := (other.Y + 0.5) - ny
		dz := other.Z - nz
		if dx*dx+dy*dy+dz*dz <= hitRadiusSq {
			sm.mu.Lock()
			delete(sm.bullets, bulletID)
			sm.mu.Unlock()

			cp := ent.ChunkPos()
			s.entities.Remove(bulletID)
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
				encodeRemoveEntitiesPacket(nil, []int32{bulletID}))
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundShulkerBulletHit, SoundSourceHostile, nx, ny, nz, 1.0, 1.0, 0))

			s.applyDamageToMob(other, ShulkerBulletDamage, nil, 0.2, false, false)
			if oNorm == MobTypeShulker {
				_, _ = s.OnShulkerHitByShulkerBullet(other.ID, rng)
			} else {
				s.ApplyEntityEffectScaled(other.ID, NewStatusEffectInstance(EffectLevitation, ShulkerBulletLevitationTicks, 0), 1.0)
			}
			return
		}
	}

	// 4. Advance bullet position along its guided vector.
	s.entities.Update(bulletID, func(e *world.Entity) {
		e.X = nx
		e.Y = ny
		e.Z = nz
		e.VelocityX = clampVelocityShort(vx)
		e.VelocityY = clampVelocityShort(vy)
		e.VelocityZ = clampVelocityShort(vz)
	})
}
