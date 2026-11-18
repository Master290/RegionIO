package server

import (
	"errors"
	"math"
	"math/rand"
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
	GuardianMagicLaserDamage      float32 = 1.0  // Base indirectMagic laser damage for Guardian (+2.0 on HARD)
	ElderGuardianMagicLaserDamage float32 = 3.0  // Base indirectMagic laser damage for Elder Guardian (1.0 + 2.0, +2.0 on HARD = 5.0)
	GuardianFlopVelocityY         float64 = 0.50 // Land flop upward hop velocity (+0.5D in Guardian.aiStep)
	GuardianFlopIntervalTicks     int     = 10
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
	ShulkerClosedArmorBonus   int     = 20   // +20 armor when Peek == 0 (COVERED_ARMOR_MODIFIER ADD_VALUE)
	ShulkerClosedDamageMult   float32 = 0.20 // Legacy flat helper ratio
	ShulkerPeekClosed         byte    = 0    // Shell completely closed
	ShulkerPeekPeeking        byte    = 30   // Peeking (0.3 target peek)
	ShulkerPeekOpen           byte    = 100  // Fully open (1.0 target peek while targeting/firing)
	ShulkerDefaultColor       byte    = 16   // 0..15 = DyeColor (white..black), 16 = default undyed purple
	ShulkerAggroRange         float64 = 16.0
	ShulkerTeleportRadius     int     = 8 // +/- 8 blocks (17x17x17 search cube)
	ShulkerMaxTeleportTries   int     = 5
	ShulkerShootCooldownMin   int     = 20  // 20 + 0*10 = 20 ticks min
	ShulkerShootCooldownMax   int     = 110 // 20 + 9*10 = 110 ticks max (`20 + nextInt(10)*20/2`)
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
	AttachFace          byte    // 0..5 (Down, Up, North, South, West, East)
	Peek                byte    // Raw `DATA_PEEK_ID`: 0 (closed), 30 (peeking), 100 (open)
	CurrentPeek         float32 // Interpolated `[0.0 .. 1.0]` (`PEEK_PER_TICK = 0.05`)
	PrevPeek            float32 // Previous tick's `CurrentPeek` (`currentPeekAmountO`)
	Color               byte    // 0..15 (DyeColor) or 16 (default purple)
	AnchorX             int
	AnchorY             int
	AnchorZ             int
	TargetPlayer        *PlayerSession
	TargetMobID         int32
	TargetAcquired      bool
	UnseenTicks         int
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
	TargetUUID     [16]byte
	HasTargetUUID  bool
	MoveDir        int8 // -1 = direct/none, 0..5 = Direction (`Dir` in NBT)
	TargetDeltaX   float64
	TargetDeltaY   float64
	TargetDeltaZ   float64
	VX             float64
	VY             float64
	VZ             float64
	StepDirX       float64
	StepDirY       float64
	StepDirZ       float64
	FlightSteps    int
	AgeTicks       int
	LeftOwner      bool
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
	if !ok || st.LaserChargeTicks <= 0 {
		return 0
	}
	req := GuardianLaserChargeTicks
	if st.IsElder {
		req = ElderGuardianLaserChargeTicks
	}
	if st.AttackTargetID != 0 {
		scale := float32(st.AttackTime+1) / float32(req)
		if scale < 0.01 {
			scale = 0.01
		}
		if scale > 1.0 {
			scale = 1.0
		}
		return scale
	}
	if !st.AttackGoalActive || st.GoalTargetID == 0 {
		return 0
	}
	scale := float32(st.LaserChargeTicks) / float32(req-GuardianAttackGoalInitialTime)
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
	dmg := GuardianThornsDamage
	if canonicalResourceName(attEnt.TypeName) == MobTypeWitch {
		dmg = ComputeWitchDamageAfterMagicAbsorb2612(attEnt.ID, entityID, "thorns", dmg)
	}
	s.applyDamageToMob(attEnt, dmg, nil, 0.2, false, false)
	return true, dmg
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

// CanShulkerAttachToFace implements `Shulker.canStayAt(BlockPos, Direction)` from `26.1.2.jar`:
//   - `(bx, by, bz)` must not be blocked (`BlockState.isAir()`, allowing `moving_piston` — water & lava block staying!).
//   - Adjacent block at `(bx, by, bz) + face` must have a sturdy full support face on `face.getOpposite()`.
//   - Expanded `getProgressAabb(..., 1.0F)` along `face.getOpposite()` must be unobstructed.
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
	if !isShulkerBlockEmptyAir2612(curState) {
		curName := canonicalResourceName(world.BlockName(curState))
		if curName != "minecraft:moving_piston" {
			return false
		}
	}
	dx, dy, dz := shulkerFaceDelta(face)
	ax, ay, az := bx+dx, by+dy, bz+dz
	if ay < world.MinY || ay >= world.MinY+world.WorldHeight {
		return false
	}
	opp := shulkerOppositeDirection(face)
	anchorState := cache.GetBlock(ax, ay, az)
	if !isSturdyShulkerSupportFace2612(anchorState, opp) {
		return false
	}
	odx, ody, odz := shulkerFaceDelta(opp)
	openX, openY, openZ := bx+odx, by+ody, bz+odz
	if openY >= world.MinY && openY < world.MinY+world.WorldHeight {
		if isColliderBlockState2612(cache.GetBlock(openX, openY, openZ)) {
			return false
		}
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

// TryTeleportShulker implements `Shulker.teleportSomewhere()` from `server-26.1.2.jar`:
// searches up to 5 times within a `17x17x17` (`+/- 8` blocks) cube for an empty air block
// (`isEmptyBlock`) with no entity collision and a valid sturdy attachable surface (`canStayAt`),
// snapping to `(bx+0.5, by, bz+0.5)`, closing its shell (`Peek = 0`), clearing target,
// and broadcasting `SoundShulkerTeleport`.
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
	allEnts := s.entities.AppendAll(nil)

	tryCandidate := func(tx, ty, tz int) (byte, bool) {
		if ty <= world.MinY || ty >= world.MinY+world.WorldHeight-1 {
			return 0, false
		}
		if tx == curBX && ty == curBY && tz == curBZ {
			return 0, false
		}
		if cache != nil && !isShulkerBlockEmptyAir2612(cache.GetBlock(tx, ty, tz)) {
			return 0, false
		}
		// `findAttachableSurface` iterates `Direction.values()` (`0..5`).
		face, ok := FindValidShulkerAttachFace(cache, tx, ty, tz, ShulkerFaceDown)
		if !ok {
			return 0, false
		}
		for i := range allEnts {
			other := &allEnts[i]
			if other.ID != entityID && other.EffectiveDimension() == dim && !other.IsItem() && !other.IsExperienceOrb() &&
				int(math.Floor(other.X)) == tx && int(math.Floor(other.Y)) == ty && int(math.Floor(other.Z)) == tz {
				return 0, false
			}
		}
		return face, true
	}

	var chosenX, chosenY, chosenZ int
	var chosenFace byte
	found := false

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
		if f, ok := tryCandidate(curBX+dx, curBY+dy, curBZ+dz); ok {
			chosenX, chosenY, chosenZ = curBX+dx, curBY+dy, curBZ+dz
			chosenFace = f
			found = true
			break
		}
	}
	// If all 5 random 3D samples missed a thin platform in `[-8..+8]`, check planar offsets within `[-8..+8]`.
	if !found {
		for attempt := 0; attempt < ShulkerMaxTeleportTries; attempt++ {
			dx := attempt + 2
			dz := 0
			if rng != nil {
				dx = rng.Intn(2*ShulkerTeleportRadius+1) - ShulkerTeleportRadius
				dz = rng.Intn(2*ShulkerTeleportRadius+1) - ShulkerTeleportRadius
			}
			if f, ok := tryCandidate(curBX+dx, curBY, curBZ+dz); ok {
				chosenX, chosenY, chosenZ = curBX+dx, curBY, curBZ+dz
				chosenFace = f
				found = true
				break
			}
		}
	}
	if !found {
		return ent.X, ent.Y, ent.Z, 0, false
	}

	newX = float64(chosenX) + 0.5
	newY = float64(chosenY)
	newZ = float64(chosenZ) + 0.5
	newFace = chosenFace
	adx, ady, adz := shulkerFaceDelta(chosenFace)

	st := s.ensureShulkerState(entityID)
	sm := s.shulkerMgr()
	sm.mu.Lock()
	st.AttachFace = chosenFace
	st.Peek = ShulkerPeekClosed
	st.CurrentPeek = 0
	st.PrevPeek = 0
	st.TargetPlayer = nil
	st.TargetMobID = 0
	st.TargetAcquired = false
	st.AttackCooldown = 0
	st.PeekTimer = 0
	st.AnchorX = chosenX + adx
	st.AnchorY = chosenY + ady
	st.AnchorZ = chosenZ + adz
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
			EncodeShulkerEntityDataPacket(entityID, chosenFace, ShulkerPeekClosed, color))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundShulkerTeleport, SoundSourceHostile, newX, newY, newZ, 1.0, 1.0, 0))
	}
	return newX, newY, newZ, newFace, true
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
//   - Returns `(0, false)` immediately when `isClosed()` (`Peek == 0`) or `teleportSomewhere()` fails!
//   - Counts alive Shulkers in `oldBoundingBox.inflate(8.0D)` (`i`); if `nextFloat() < (i - 1) / 5.0F`
//     (i.e. `otherCount / 5.0F`), returns without duplicating; otherwise spawns a new Shulker of the
//     same `Color` at `oldPos`!
func (s *Server) OnShulkerHitByShulkerBullet(entityID int32, rng *rand.Rand) (newShulkerID int32, duplicated bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeShulker {
		return 0, false
	}
	st, ok := s.GetShulkerState(entityID)
	if !ok || st.Peek == 0 {
		return 0, false
	}
	oldX, oldY, oldZ := ent.X, ent.Y, ent.Z
	dim := ent.EffectiveDimension()

	_, _, _, _, tele := s.TryTeleportShulker(entityID, rng)
	if !tele {
		return 0, false
	}

	// Count alive Shulkers in `oldBox.inflate(8.0D)` (`[oldX-8.5..oldX+8.5] x [oldY-8.0..oldY+9.0] x [oldZ-8.5..oldZ+8.5]`).
	totalInBox := 0
	otherCount := 0
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		other := &allEnts[i]
		if other.EffectiveDimension() != dim || canonicalResourceName(other.TypeName) != MobTypeShulker || s.MobHealth(other.ID) <= 0 {
			continue
		}
		if math.Abs(other.X-oldX) <= ShulkerDuplicateCheckDist+0.5 &&
			other.Y+1.0 >= oldY-ShulkerDuplicateCheckDist && other.Y <= oldY+1.0+ShulkerDuplicateCheckDist &&
			math.Abs(other.Z-oldZ) <= ShulkerDuplicateCheckDist+0.5 {
			totalInBox++
			if other.ID != entityID {
				otherCount++
			}
		}
	}
	effectiveOthers := totalInBox - 1
	if effectiveOthers < otherCount {
		effectiveOthers = otherCount
	}
	if effectiveOthers >= ShulkerMaxNearbyForDup {
		return 0, false
	}
	failChance := float64(effectiveOthers) / float64(ShulkerMaxNearbyForDup)
	if rng != nil && rng.Float64() < failChance {
		return 0, false
	}
	newID, err := s.SpawnShulkerWithFaceAndColor(dim, oldX, oldY, oldZ, st.AttachFace, st.Color)
	if err != nil || newID <= 0 {
		return 0, false
	}
	return newID, true
}

// SpawnShulkerBullet spawns a guided `minecraft:shulker_bullet` projectile fired
// by `shooterID` toward `targetPlayer` (or `targetEntityID`), selecting its initial
// discrete axis step via `SelectShulkerBulletNextMoveDirection2612(shooterAxis, target)`
// and broadcasting `SoundShulkerShoot`.
func (s *Server) SpawnShulkerBullet(dimension string, shooterID, targetEntityID int32, targetPlayer *PlayerSession, rng *rand.Rand) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	sx, sy, sz := 0.5, 64.5, 0.5
	shooterAxis := ShulkerAxisNone
	if shooter := s.entities.Get(shooterID); shooter != nil {
		normDim = shooter.EffectiveDimension()
		sx = shooter.X
		sy = shooter.Y + 0.5
		sz = shooter.Z
		if sst, ok := s.GetShulkerState(shooterID); ok {
			shooterAxis = shulkerDirectionAxis(sst.AttachFace)
		}
	}
	var targetUUID [16]byte
	var hasTargetUUID bool
	if targetPlayer != nil {
		if targetEntityID == 0 {
			targetEntityID = targetPlayer.EntityID
		}
		targetUUID = targetPlayer.Profile.UUID
		hasTargetUUID = true
	} else if targetEntityID > 0 {
		if tent := s.entities.Get(targetEntityID); tent != nil {
			targetUUID = tent.UUID
			hasTargetUUID = true
		}
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

	cache := s.DimensionChunks(normDim)
	tx, ty, tz, tHeight, hasTarget := s.resolveShulkerBulletTargetBaseAndHeight(normDim, targetPlayer, targetEntityID, hasTargetUUID, targetUUID)
	moveDir, txd, tyd, tzd, steps := SelectShulkerBulletNextMoveDirection2612(
		cache, rng, sx, sy, sz, shooterAxis, hasTarget, tx, ty, tz, tHeight,
	)
	var stepX, stepY, stepZ float64
	if moveDir >= 0 && moveDir <= 5 {
		sdx, sdy, sdz := shulkerFaceDelta(byte(moveDir))
		stepX, stepY, stepZ = float64(sdx), float64(sdy), float64(sdz)
	}

	sm := s.shulkerMgr()
	sm.mu.Lock()
	sm.bullets[bulletID] = &ShulkerBulletState{
		EntityID:       bulletID,
		Dimension:      normDim,
		ShooterID:      shooterID,
		TargetPlayer:   targetPlayer,
		TargetEntityID: targetEntityID,
		TargetUUID:     targetUUID,
		HasTargetUUID:  hasTargetUUID,
		MoveDir:        moveDir,
		TargetDeltaX:   txd,
		TargetDeltaY:   tyd,
		TargetDeltaZ:   tzd,
		StepDirX:       stepX,
		StepDirY:       stepY,
		StepDirZ:       stepZ,
		FlightSteps:    steps,
		LeftOwner:      shooterID <= 0,
	}
	sm.mu.Unlock()

	if ent := s.entities.Get(bulletID); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		pitch := float32(1.0)
		if rng != nil {
			pitch = (rng.Float32()-rng.Float32())*0.2 + 1.0
		}
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundShulkerShoot, SoundSourceHostile, sx, sy, sz, 2.0, pitch, 0))
	}
	return bulletID, nil
}

// selectShulkerBulletAxisStep selects the unit axis step (`+/-1` along X, Y, or Z)
// corresponding to the largest coordinate delta toward `(tx, ty, tz)`.
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

func (s *Server) resolveShulkerBulletTargetBaseAndHeight(dim string, targetPlayer *PlayerSession, targetEntityID int32, hasTargetUUID bool, targetUUID [16]byte) (x, y, z, height float64, ok bool) {
	if targetPlayer != nil && !targetPlayer.IsDead() {
		snap := targetPlayer.Snapshot()
		if world.NormalizeDimension(snap.Dimension) == dim && snap.GameMode != GameModeSpectator {
			return snap.X, snap.Y, snap.Z, 1.8, true
		}
	}
	if targetEntityID > 0 && s.entities != nil {
		if ent := s.entities.Get(targetEntityID); ent != nil && ent.EffectiveDimension() == dim && s.MobHealth(ent.ID) > 0 {
			_, h := shulkerEntityBoxDimensions2612(ent.TypeName)
			if h <= 0 {
				h = 1.0
			}
			return ent.X, ent.Y, ent.Z, h, true
		}
	}
	if hasTargetUUID {
		for _, p := range s.onlinePlayersSnapshot() {
			if p == nil || p.IsDead() || p.Profile.UUID != targetUUID {
				continue
			}
			snap := p.Snapshot()
			if world.NormalizeDimension(snap.Dimension) == dim && snap.GameMode != GameModeSpectator {
				return snap.X, snap.Y, snap.Z, 1.8, true
			}
		}
		if s.entities != nil {
			allEnts := s.entities.AppendAll(nil)
			for i := range allEnts {
				ent := &allEnts[i]
				if ent.UUID == targetUUID && ent.EffectiveDimension() == dim && s.MobHealth(ent.ID) > 0 {
					_, h := shulkerEntityBoxDimensions2612(ent.TypeName)
					if h <= 0 {
						h = 1.0
					}
					return ent.X, ent.Y, ent.Z, h, true
				}
			}
		}
	}
	return 0, 0, 0, 0, false
}

func (s *Server) resolveShulkerBulletTargetPos(dim string, targetPlayer *PlayerSession, targetEntityID int32) (x, y, z float64, ok bool) {
	bx, by, bz, h, ok := s.resolveShulkerBulletTargetBaseAndHeight(dim, targetPlayer, targetEntityID, false, [16]byte{})
	if !ok {
		return 0, 0, 0, false
	}
	return bx, by + h*0.5, bz, true
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

	// 1. `Shulker.findNewAttachment()`: validate empty air cell, sturdy anchor face, and unobstructed `peek=1.0` space.
	if cache != nil && !CanShulkerAttachToFace(cache, bx, by, bz, snap.AttachFace) {
		if altFace, ok := FindValidShulkerAttachFace(cache, bx, by, bz, ShulkerFaceDown); ok {
			s.SetShulkerAttachFace(entityID, altFace)
			snap.AttachFace = altFace
		} else {
			_, _, _, _, _ = s.TryTeleportShulker(entityID, rng)
			if curEnt := s.entities.Get(entityID); curEnt != nil {
				s.stepShulkerPeekInterpolation(entityID, curEnt)
			}
			return
		}
	}

	if s.Difficulty() == DifficultyPeaceful {
		if snap.TargetAcquired || snap.TargetPlayer != nil {
			sm.mu.Lock()
			if cur := sm.shulkers[entityID]; cur != nil {
				cur.TargetAcquired = false
				cur.TargetPlayer = nil
				cur.AttackCooldown = 0
			}
			sm.mu.Unlock()
			s.SetShulkerPeek(entityID, ShulkerPeekClosed)
		} else if snap.Peek != ShulkerPeekClosed {
			s.SetShulkerPeek(entityID, ShulkerPeekClosed)
		}
		s.stepShulkerPeekInterpolation(entityID, ent)
		return
	}

	// 2. Resolve or acquire Survival/Adventure target (`ShulkerNearestAttackGoal` + `ShulkerAttackGoal` `< 400.0D`).
	var bestPlayer *PlayerSession
	if snap.TargetAcquired && snap.TargetPlayer != nil && !snap.TargetPlayer.IsDead() {
		pSnap := snap.TargetPlayer.Snapshot()
		if world.NormalizeDimension(pSnap.Dimension) == dim &&
			pSnap.GameMode != GameModeCreative && pSnap.GameMode != GameModeSpectator {
			dx := pSnap.X - ent.X
			dy := pSnap.Y - ent.Y
			dz := pSnap.Z - ent.Z
			if dx*dx+dy*dy+dz*dz < ShulkerAttackMaxContinueDistSq {
				bestPlayer = snap.TargetPlayer
			}
		}
	}
	if bestPlayer == nil {
		bestDistSq := ShulkerAttackMaxContinueDistSq
		for _, p := range s.onlinePlayersSnapshot() {
			if p == nil || p.IsDead() {
				continue
			}
			pSnap := p.Snapshot()
			if world.NormalizeDimension(pSnap.Dimension) != dim ||
				pSnap.GameMode == GameModeCreative || pSnap.GameMode == GameModeSpectator {
				continue
			}
			if !shulkerTargetWithinSearchArea2612(ent.X, ent.Y, ent.Z, snap.AttachFace, ShulkerAggroRange, pSnap.X, pSnap.Y, pSnap.Z) {
				continue
			}
			dx := pSnap.X - ent.X
			dy := pSnap.Y - ent.Y
			dz := pSnap.Z - ent.Z
			dSq := dx*dx + dy*dy + dz*dz
			if dSq < bestDistSq && HasGuardianLaserLineOfSight(cache, ent.X, ent.Y+0.5, ent.Z, pSnap.X, pSnap.Y+0.9, pSnap.Z) {
				bestDistSq = dSq
				bestPlayer = p
			}
		}
	}

	if bestPlayer != nil {
		if !snap.TargetAcquired {
			// `ShulkerAttackGoal.start()` (`attackTime = 20`, `setRawPeekAmount(100)`) + first `tick()` (`--attackTime -> 19`):
			// first bullet spawns on tick 20!
			sm.mu.Lock()
			if cur := sm.shulkers[entityID]; cur != nil {
				cur.TargetAcquired = true
				cur.TargetPlayer = bestPlayer
				cur.AttackCooldown = ShulkerAttackInitialDelayTicks - 1
				cur.PeekTimer = 0
			}
			sm.mu.Unlock()
			s.SetShulkerPeek(entityID, ShulkerPeekOpen)
		} else {
			if snap.Peek != ShulkerPeekOpen {
				s.SetShulkerPeek(entityID, ShulkerPeekOpen)
			}
			if snap.AttackCooldown <= 0 {
				// `ShulkerAttackGoal.tick`: `attackTime = 20 + random.nextInt(10) * 20 / 2` (`20, 30, ..., 110`).
				nextCD := 40
				if rng != nil {
					nextCD = 20 + (rng.Intn(10)*20)/2
				}
				sm.mu.Lock()
				if cur := sm.shulkers[entityID]; cur != nil {
					cur.AttackCooldown = nextCD
					cur.TargetPlayer = bestPlayer
				}
				sm.mu.Unlock()
				_, _ = s.SpawnShulkerBullet(dim, entityID, bestPlayer.EntityID, bestPlayer, rng)
			}
		}
		s.stepShulkerPeekInterpolation(entityID, ent)
		return
	}

	// 3. No target: `ShulkerAttackGoal.stop()` or idle `ShulkerPeekGoal` (`nextInt(40) == 0`, `peekTime = 20 * (1 + nextInt(3))`).
	if snap.TargetAcquired {
		sm.mu.Lock()
		if cur := sm.shulkers[entityID]; cur != nil {
			cur.TargetAcquired = false
			cur.TargetPlayer = nil
			cur.AttackCooldown = 0
		}
		sm.mu.Unlock()
		s.SetShulkerPeek(entityID, ShulkerPeekClosed)
	} else if snap.Peek == ShulkerPeekPeeking && snap.PeekTimer == 0 {
		s.SetShulkerPeek(entityID, ShulkerPeekClosed)
	} else if snap.Peek == ShulkerPeekClosed && rng != nil && rng.Intn(40) == 0 {
		peekDur := 20 * (1 + rng.Intn(3))
		sm.mu.Lock()
		if cur := sm.shulkers[entityID]; cur != nil {
			cur.PeekTimer = peekDur
		}
		sm.mu.Unlock()
		s.SetShulkerPeek(entityID, ShulkerPeekPeeking)
	}
	s.stepShulkerPeekInterpolation(entityID, ent)
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
	dim := ent.EffectiveDimension()
	// `ShulkerBullet.checkDespawn()`: discard immediately on Peaceful!
	if s.Difficulty() == DifficultyPeaceful {
		delete(sm.bullets, bulletID)
		sm.mu.Unlock()
		cp := ent.ChunkPos()
		s.entities.Remove(bulletID)
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
			encodeRemoveEntitiesPacket(nil, []int32{bulletID}))
		return
	}
	b.AgeTicks++
	if b.AgeTicks > ShulkerBulletMaxLifetime {
		delete(sm.bullets, bulletID)
		sm.mu.Unlock()
		cp := ent.ChunkPos()
		s.entities.Remove(bulletID)
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
			encodeRemoveEntitiesPacket(nil, []int32{bulletID}))
		return
	}
	snap := *b
	sm.mu.Unlock()

	tx, ty, tz, tHeight, hasTarget := s.resolveShulkerBulletTargetBaseAndHeight(
		dim, snap.TargetPlayer, snap.TargetEntityID, snap.HasTargetUUID, snap.TargetUUID,
	)

	// `ShulkerBullet.tick()`: smooth targetDelta acceleration (`*1.025`, clamp `[-1, 1]`, lerp `0.2`) or gravity (`0.04`).
	txd, tyd, tzd := snap.TargetDeltaX, snap.TargetDeltaY, snap.TargetDeltaZ
	vx, vy, vz := snap.VX, snap.VY, snap.VZ
	if hasTarget {
		txd = math.Max(-1.0, math.Min(1.0, txd*ShulkerBulletAccelFactor))
		tyd = math.Max(-1.0, math.Min(1.0, tyd*ShulkerBulletAccelFactor))
		tzd = math.Max(-1.0, math.Min(1.0, tzd*ShulkerBulletAccelFactor))
		vx += (txd - vx) * ShulkerBulletSteerLerp
		vy += (tyd - vy) * ShulkerBulletSteerLerp
		vz += (tzd - vz) * ShulkerBulletSteerLerp
	} else {
		vy -= ShulkerBulletGravity
	}
	nx := ent.X + vx
	ny := ent.Y + vy
	nz := ent.Z + vz

	// `Projectile.checkLeftOwner()`: once the bullet leaves its shooter's hit volume, it can hit its own shooter if it loops back!
	hitRadiusSq := ShulkerBulletHitboxRadius * ShulkerBulletHitboxRadius
	leftOwner := snap.LeftOwner
	if !leftOwner {
		shooter := s.entities.Get(snap.ShooterID)
		if shooter == nil {
			leftOwner = true
		} else {
			sdx := shooter.X - nx
			sdy := (shooter.Y + 0.5) - ny
			sdz := shooter.Z - nz
			outsideHitSphere := (sdx*sdx + sdy*sdy + sdz*sdz) > hitRadiusSq
			outsideBox := true
			if sMinX, sMinY, sMinZ, sMaxX, sMaxY, sMaxZ, okBox := s.GetShulkerBoundingBox(snap.ShooterID); okBox {
				const margin = 0.35
				if nx >= sMinX-margin && nx <= sMaxX+margin &&
					ny >= sMinY-margin && ny <= sMaxY+margin &&
					nz >= sMinZ-margin && nz <= sMaxZ+margin {
					outsideBox = false
				}
			}
			if outsideHitSphere && outsideBox {
				leftOwner = true
			}
		}
	}

	sm.mu.Lock()
	if cur := sm.bullets[bulletID]; cur != nil {
		cur.TargetDeltaX = txd
		cur.TargetDeltaY = tyd
		cur.TargetDeltaZ = tzd
		cur.VX = vx
		cur.VY = vy
		cur.VZ = vz
		cur.LeftOwner = leftOwner
	}
	sm.mu.Unlock()

	cache := s.DimensionChunks(dim)
	// 1. Check solid block impact (`onHitBlock`: `SHULKER_BULLET_HIT` + discard).
	if cache != nil {
		bx, by, bz := int(math.Floor(nx)), int(math.Floor(ny)), int(math.Floor(nz))
		if by >= world.MinY && by < world.MinY+world.WorldHeight {
			st := cache.GetBlock(bx, by, bz)
			if isColliderBlockState2612(st) {
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
	}

	// 2. Check player impact (deals `4.0` mobProjectile damage + Levitation I for `200` ticks = 10s).
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

	// 3. Check entity/Shulker impact (`Projectile.canHitEntity`: skips shooter only while `!leftOwner`).
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		other := &allEnts[i]
		if other.ID == bulletID || other.IsItem() || other.IsExperienceOrb() || other.EffectiveDimension() != dim {
			continue
		}
		if other.ID == snap.ShooterID && !leftOwner {
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

	// 5. Post-move step countdown & axis re-selection (`ShulkerBullet.tick` lines 4366-4446 in 26.1.2.jar).
	if hasTarget {
		sm.mu.Lock()
		cur := sm.bullets[bulletID]
		if cur == nil {
			sm.mu.Unlock()
			return
		}
		reselect := false
		curAxis := ShulkerAxisNone
		if cur.MoveDir >= 0 && cur.MoveDir <= 5 {
			curAxis = shulkerDirectionAxis(byte(cur.MoveDir))
		}
		if cur.FlightSteps > 0 {
			cur.FlightSteps--
			if cur.FlightSteps == 0 {
				reselect = true
			}
		}
		if !reselect && cur.MoveDir >= 0 && cur.MoveDir <= 5 {
			curBX, curBY, curBZ := int(math.Floor(nx)), int(math.Floor(ny)), int(math.Floor(nz))
			sdx, sdy, sdz := shulkerFaceDelta(byte(cur.MoveDir))
			if cache != nil {
				nbx, nby, nbz := curBX+sdx, curBY+sdy, curBZ+sdz
				if nby >= world.MinY && nby < world.MinY+world.WorldHeight && isColliderBlockState2612(cache.GetBlock(nbx, nby, nbz)) {
					reselect = true
				}
			}
			if !reselect {
				tBlockX, tBlockY, tBlockZ := int(math.Floor(tx)), int(math.Floor(ty)), int(math.Floor(tz))
				if (curAxis == ShulkerAxisX && curBX == tBlockX) ||
					(curAxis == ShulkerAxisZ && curBZ == tBlockZ) ||
					(curAxis == ShulkerAxisY && curBY == tBlockY) {
					reselect = true
				}
			}
		}
		if reselect {
			nextDir, nextTXD, nextTYD, nextTZD, nextSteps := SelectShulkerBulletNextMoveDirection2612(
				cache, rng, nx, ny, nz, curAxis, true, tx, ty, tz, tHeight,
			)
			cur.MoveDir = nextDir
			cur.TargetDeltaX = nextTXD
			cur.TargetDeltaY = nextTYD
			cur.TargetDeltaZ = nextTZD
			cur.FlightSteps = nextSteps
			if nextDir >= 0 && nextDir <= 5 {
				sdx, sdy, sdz := shulkerFaceDelta(byte(nextDir))
				cur.StepDirX, cur.StepDirY, cur.StepDirZ = float64(sdx), float64(sdy), float64(sdz)
			} else {
				cur.StepDirX, cur.StepDirY, cur.StepDirZ = 0, 0, 0
			}
		}
		sm.mu.Unlock()
	}
}
