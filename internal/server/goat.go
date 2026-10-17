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

// Goat (minecraft:goat) and Goat Horn (minecraft:goat_horn) constants,
// instruments, SynchedEntityData indices, and sounds for Minecraft 26.1.2 (protocol 775).
const (
	MobTypeGoat      = "minecraft:goat"
	EntityTypeGoatID = 62

	DefaultGoatMaxHealth      float32 = 10.0
	GoatWanderSpeed           float64 = 0.08
	GoatRamChargeSpeed        float64 = 0.35
	GoatRamMinDist            float64 = 4.0
	GoatRamMaxDist            float64 = 16.0
	GoatRamDamage             float32 = 5.0
	GoatBabyRamDamage         float32 = 1.0
	GoatRamKnockback          float64 = 2.5
	GoatFallDamageReduction   float32 = 10.0 // Goats take 10 HP less fall damage
	ScreamingGoatSpawnChance  float32 = 0.02 // 2% chance for a natural Goat to be a Screaming Goat

	// Ramming & Long Jump cooldowns (in ticks at 20 TPS).
	GoatNormalRamMinCooldownTicks    = 600  // 30s
	GoatNormalRamMaxCooldownTicks    = 6000 // 300s
	GoatScreamingRamMinCooldownTicks = 100  // 5s
	GoatScreamingRamMaxCooldownTicks = 300  // 15s
	GoatLongJumpMinCooldownTicks     = 600  // 30s
	GoatLongJumpMaxCooldownTicks     = 1200 // 60s
	GoatLongJumpVerticalImpulse      = 0.95 // Up to 10 blocks high
	GoatLongJumpHorizontalImpulse    = 0.55 // Up to 5 blocks across

	// Goat Horn item & cooldown.
	GoatHornItemID            = "minecraft:goat_horn"
	GoatHornCooldownTicks     = 140 // 7.0 seconds (140 ticks)
	WheatItemID               = "minecraft:wheat"
	GoatBreedingFoodItemID    = WheatItemID
	MilkBucketItemID          = "minecraft:milk_bucket"
	PlayCooldownCB            = 0x16 // ClientboundCooldownPacket (protocol 775)

	// Goat Horn instruments (`minecraft:instrument` registry).
	InstrumentPonderGoatHorn = "minecraft:ponder_goat_horn"
	InstrumentSingGoatHorn   = "minecraft:sing_goat_horn"
	InstrumentSeekGoatHorn   = "minecraft:seek_goat_horn"
	InstrumentFeelGoatHorn   = "minecraft:feel_goat_horn"
	InstrumentAdmireGoatHorn = "minecraft:admire_goat_horn"
	InstrumentCallGoatHorn   = "minecraft:call_goat_horn"
	InstrumentYearnGoatHorn  = "minecraft:yearn_goat_horn"
	InstrumentDreamGoatHorn  = "minecraft:dream_goat_horn"

	// SynchedEntityData indices for Goat in Minecraft 26.1.2 (protocol 775).
	// Goat extends Animal -> AgeableMob (16) -> PathfinderMob -> Mob -> LivingEntity -> Entity.
	EntityDataGoatIsScreamingID  byte = 17 // Goat.DATA_IS_SCREAMING_GOAT (BOOLEAN serializer = 8)
	EntityDataGoatHasLeftHornID  byte = 18 // Goat.DATA_HAS_LEFT_HORN (BOOLEAN serializer = 8)
	EntityDataGoatHasRightHornID byte = 19 // Goat.DATA_HAS_RIGHT_HORN (BOOLEAN serializer = 8)

	// Canonical Goat & Goat Horn sounds.
	SoundGoatAmbient              = "minecraft:entity.goat.ambient"
	SoundGoatDeath                = "minecraft:entity.goat.death"
	SoundGoatEat                  = "minecraft:entity.goat.eat"
	SoundGoatHurt                 = "minecraft:entity.goat.hurt"
	SoundGoatLongJump             = "minecraft:entity.goat.long_jump"
	SoundGoatMilk                 = "minecraft:entity.goat.milk"
	SoundGoatPrepareRam           = "minecraft:entity.goat.prepare_ram"
	SoundGoatRamImpact            = "minecraft:entity.goat.ram_impact"
	SoundGoatHornBreak            = "minecraft:entity.goat.horn_break"
	SoundGoatScreamingAmbient     = "minecraft:entity.goat.screaming.ambient"
	SoundGoatScreamingDeath       = "minecraft:entity.goat.screaming.death"
	SoundGoatScreamingHurt        = "minecraft:entity.goat.screaming.hurt"
	SoundGoatScreamingLongJump    = "minecraft:entity.goat.screaming.long_jump"
	SoundGoatScreamingMilk        = "minecraft:entity.goat.screaming.milk"
	SoundGoatScreamingPrepareRam  = "minecraft:entity.goat.screaming.prepare_ram"
	SoundGoatScreamingRamImpact   = "minecraft:entity.goat.screaming.ram_impact"
)

// NormalGoatHornInstruments returns the 4 instruments dropped by normal Goats.
func NormalGoatHornInstruments() [4]string {
	return [4]string{
		InstrumentPonderGoatHorn,
		InstrumentSingGoatHorn,
		InstrumentSeekGoatHorn,
		InstrumentFeelGoatHorn,
	}
}

// ScreamingGoatHornInstruments returns the 4 instruments dropped by Screaming Goats.
func ScreamingGoatHornInstruments() [4]string {
	return [4]string{
		InstrumentAdmireGoatHorn,
		InstrumentCallGoatHorn,
		InstrumentYearnGoatHorn,
		InstrumentDreamGoatHorn,
	}
}

// RollGoatHornInstrument picks one of the 4 normal or 4 screaming Goat Horn
// instruments depending on whether the Goat is a Screaming Goat.
func RollGoatHornInstrument(isScreaming bool, rng *rand.Rand) string {
	idx := 0
	if rng != nil {
		idx = rng.Intn(4)
	}
	if isScreaming {
		list := ScreamingGoatHornInstruments()
		return list[idx]
	}
	list := NormalGoatHornInstruments()
	return list[idx]
}

// SoundForGoatHornInstrument returns the `item.goat_horn.sound.0..7` sound
// event corresponding to instrument in Minecraft 26.1.2.
func SoundForGoatHornInstrument(instrument string) string {
	switch canonicalResourceName(instrument) {
	case InstrumentPonderGoatHorn:
		return "minecraft:item.goat_horn.sound.0"
	case InstrumentSingGoatHorn:
		return "minecraft:item.goat_horn.sound.1"
	case InstrumentSeekGoatHorn:
		return "minecraft:item.goat_horn.sound.2"
	case InstrumentFeelGoatHorn:
		return "minecraft:item.goat_horn.sound.3"
	case InstrumentAdmireGoatHorn:
		return "minecraft:item.goat_horn.sound.4"
	case InstrumentCallGoatHorn:
		return "minecraft:item.goat_horn.sound.5"
	case InstrumentYearnGoatHorn:
		return "minecraft:item.goat_horn.sound.6"
	case InstrumentDreamGoatHorn:
		return "minecraft:item.goat_horn.sound.7"
	default:
		return "minecraft:item.goat_horn.sound.0"
	}
}

// IsGoatHornSnapBlock reports whether blockName belongs to the vanilla 26.1.2
// `#minecraft:snaps_goat_horn` block tag (hard natural blocks that snap off a
// Goat's horn on ramming impact).
func IsGoatHornSnapBlock(blockName string) bool {
	switch canonicalResourceName(blockName) {
	case "minecraft:stone",
		"minecraft:deepslate",
		"minecraft:packed_ice",
		"minecraft:iron_ore",
		"minecraft:coal_ore",
		"minecraft:copper_ore",
		"minecraft:emerald_ore",
		"minecraft:oak_log",
		"minecraft:spruce_log",
		"minecraft:birch_log",
		"minecraft:jungle_log",
		"minecraft:acacia_log",
		"minecraft:dark_oak_log",
		"minecraft:mangrove_log",
		"minecraft:cherry_log",
		"minecraft:pale_oak_log":
		return true
	default:
		return false
	}
}

// ComputeGoatFallDamage computes the vanilla 26.1.2 fall damage for a Goat:
// Goats have a -10.0 fall damage reduction on top of the standard 3-block safe fall distance.
func ComputeGoatFallDamage(fallDistance float64) float32 {
	dmg := float32(math.Ceil(fallDistance-3.0)) - GoatFallDamageReduction
	if dmg < 0 {
		return 0
	}
	return dmg
}

// GoatBehaviorState holds the server-side AI, screaming flag, left/right horn
// state, ramming cooldown, and long-jump state for a `minecraft:goat` entity.
type GoatBehaviorState struct {
	EntityID              int32
	IsScreaming           bool
	HasLeftHorn           bool
	HasRightHorn          bool
	RamCooldownTicks      int
	LongJumpCooldownTicks int
	Pose                  byte
	LastDroppedInstrument string
}

type serverGoatManager struct {
	mu               sync.RWMutex
	goats            map[int32]*GoatBehaviorState
	hornInstrument   map[int32]string // dropped item entityID -> instrument
	playerHornTick   map[*PlayerSession]int64
}

var goatManagers sync.Map // map[*Server]*serverGoatManager

func (s *Server) goatManager() *serverGoatManager {
	if s == nil {
		return &serverGoatManager{
			goats:          make(map[int32]*GoatBehaviorState),
			hornInstrument: make(map[int32]string),
			playerHornTick: make(map[*PlayerSession]int64),
		}
	}
	if v, ok := goatManagers.Load(s); ok {
		return v.(*serverGoatManager)
	}
	mgr := &serverGoatManager{
		goats:          make(map[int32]*GoatBehaviorState),
		hornInstrument: make(map[int32]string),
		playerHornTick: make(map[*PlayerSession]int64),
	}
	actual, _ := goatManagers.LoadOrStore(s, mgr)
	return actual.(*serverGoatManager)
}

func initialGoatRamCooldown(isScreaming bool, rng *rand.Rand) int {
	if isScreaming {
		if rng != nil {
			return GoatScreamingRamMinCooldownTicks + rng.Intn(GoatScreamingRamMaxCooldownTicks-GoatScreamingRamMinCooldownTicks+1)
		}
		return GoatScreamingRamMinCooldownTicks
	}
	if rng != nil {
		return GoatNormalRamMinCooldownTicks + rng.Intn(GoatNormalRamMaxCooldownTicks-GoatNormalRamMinCooldownTicks+1)
	}
	return GoatNormalRamMinCooldownTicks
}

func (s *Server) ensureGoatState(entityID int32, isScreaming, isBaby bool) *GoatBehaviorState {
	if s == nil || entityID <= 0 {
		return nil
	}
	mgr := s.goatManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.goats[entityID]
	if !ok || st == nil {
		st = &GoatBehaviorState{
			EntityID:              entityID,
			IsScreaming:           isScreaming,
			HasLeftHorn:           !isBaby,
			HasRightHorn:          !isBaby,
			RamCooldownTicks:      initialGoatRamCooldown(isScreaming, nil),
			LongJumpCooldownTicks: GoatLongJumpMinCooldownTicks,
			Pose:                  PoseStanding,
		}
		mgr.goats[entityID] = st
	}
	return st
}

// GetGoatState returns a snapshot of the GoatBehaviorState for entityID.
func (s *Server) GetGoatState(entityID int32) (GoatBehaviorState, bool) {
	if s == nil || entityID <= 0 {
		return GoatBehaviorState{}, false
	}
	mgr := s.goatManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	st, ok := mgr.goats[entityID]
	if !ok || st == nil {
		return GoatBehaviorState{}, false
	}
	return *st, true
}

// IsScreamingGoat reports whether the Goat with entityID is a Screaming Goat.
func (s *Server) IsScreamingGoat(entityID int32) bool {
	st, ok := s.GetGoatState(entityID)
	return ok && st.IsScreaming
}

// SetGoatScreaming sets whether the Goat with entityID is a Screaming Goat and
// broadcasts its updated metadata.
func (s *Server) SetGoatScreaming(entityID int32, isScreaming bool) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeGoat {
		return false
	}
	st := s.ensureGoatState(entityID, isScreaming, s.IsMobBaby(entityID))
	mgr := s.goatManager()
	mgr.mu.Lock()
	st.IsScreaming = isScreaming
	if isScreaming && st.RamCooldownTicks > GoatScreamingRamMaxCooldownTicks {
		st.RamCooldownTicks = GoatScreamingRamMinCooldownTicks
	}
	snap := *st
	mgr.mu.Unlock()
	s.broadcastGoatState(ent, snap)
	return true
}

// EncodeGoatEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// for a `minecraft:goat` entity with its Pose (index 6), DATA_IS_SCREAMING_GOAT
// (index 17, BOOLEAN), DATA_HAS_LEFT_HORN (index 18, BOOLEAN), and
// DATA_HAS_RIGHT_HORN (index 19, BOOLEAN).
func EncodeGoatEntityDataPacket(entityID int32, pose byte, isScreaming, hasLeftHorn, hasRightHorn bool) []byte {
	w := protocol.NewWriter(24)
	w.VarInt(entityID)
	w.Byte(EntityDataPoseID)
	w.VarInt(EntityDataSerializerPose)
	w.VarInt(int32(pose))
	w.Byte(EntityDataGoatIsScreamingID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(isScreaming)
	w.Byte(EntityDataGoatHasLeftHornID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(hasLeftHorn)
	w.Byte(EntityDataGoatHasRightHornID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(hasRightHorn)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeGoatEntityDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeGoatEntityDataPacket.
func DecodeGoatEntityDataPacket(r *protocol.Reader) (entityID int32, pose byte, isScreaming, hasLeftHorn, hasRightHorn bool, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, 0, false, false, false, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, 0, false, false, false, err
		}
		if idx == 0xFF {
			break
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, 0, false, false, false, err
		}
		switch idx {
		case EntityDataPoseID:
			if ser != EntityDataSerializerPose {
				return 0, 0, false, false, false, errors.New("goat: unexpected DATA_POSE serializer")
			}
			v, err := r.VarInt()
			if err != nil {
				return 0, 0, false, false, false, err
			}
			pose = byte(v)
		case EntityDataGoatIsScreamingID:
			if ser != EntityDataSerializerBool {
				return 0, 0, false, false, false, errors.New("goat: unexpected DATA_IS_SCREAMING_GOAT serializer")
			}
			isScreaming, err = r.Bool()
		case EntityDataGoatHasLeftHornID:
			if ser != EntityDataSerializerBool {
				return 0, 0, false, false, false, errors.New("goat: unexpected DATA_HAS_LEFT_HORN serializer")
			}
			hasLeftHorn, err = r.Bool()
		case EntityDataGoatHasRightHornID:
			if ser != EntityDataSerializerBool {
				return 0, 0, false, false, false, errors.New("goat: unexpected DATA_HAS_RIGHT_HORN serializer")
			}
			hasRightHorn, err = r.Bool()
		default:
			return 0, 0, false, false, false, errors.New("goat: unexpected SynchedEntityData index")
		}
		if err != nil {
			return 0, 0, false, false, false, err
		}
	}
	return entityID, pose, isScreaming, hasLeftHorn, hasRightHorn, nil
}

func (s *Server) broadcastGoatState(ent *world.Entity, st GoatBehaviorState) {
	if s == nil || ent == nil {
		return
	}
	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeGoatEntityDataPacket(ent.ID, st.Pose, st.IsScreaming, st.HasLeftHorn, st.HasRightHorn))
}

// SpawnGoat spawns a `minecraft:goat` at (x, y, z) in dimension and broadcasts
// its creation and metadata to nearby players.
func (s *Server) SpawnGoat(dimension string, x, y, z float64, isScreaming, isBaby bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil server or entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeGoat)
	if typeID < 0 {
		typeID = EntityTypeGoatID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeGoat,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(id, tr.aiTickID)
	mem.healthInit = true
	mem.health = DefaultGoatMaxHealth
	mem.lastHealth = DefaultGoatMaxHealth
	if isBaby {
		mem.ageTicks = BabyMobGrowthTicks
	}
	tr.mu.Unlock()

	st := s.ensureGoatState(id, isScreaming, isBaby)
	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		if st != nil {
			s.broadcastGoatState(ent, *st)
		}
		if isBaby {
			s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
				EncodeAgeableMobDataPacket(id, true))
		}
	}
	return id, nil
}

// BreakGoatHorn snaps off one remaining horn on the Goat with entityID,
// updates DATA_HAS_LEFT_HORN / DATA_HAS_RIGHT_HORN, plays entity.goat.horn_break,
// and drops a `minecraft:goat_horn` item entity with a rolled instrument.
func (s *Server) BreakGoatHorn(entityID int32, rng *rand.Rand) (hornEntityID int32, instrument string, broke bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return 0, "", false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeGoat {
		return 0, "", false
	}
	st := s.ensureGoatState(entityID, false, s.IsMobBaby(entityID))
	mgr := s.goatManager()
	mgr.mu.Lock()
	if !st.HasLeftHorn && !st.HasRightHorn {
		mgr.mu.Unlock()
		return 0, "", false
	}
	if st.HasLeftHorn && st.HasRightHorn {
		if rng != nil && rng.Intn(2) == 0 {
			st.HasLeftHorn = false
		} else {
			st.HasRightHorn = false
		}
	} else if st.HasLeftHorn {
		st.HasLeftHorn = false
	} else {
		st.HasRightHorn = false
	}
	instrument = RollGoatHornInstrument(st.IsScreaming, rng)
	st.LastDroppedInstrument = instrument
	snap := *st
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.broadcastGoatState(ent, snap)

	hornStack := world.NewNamedItemStack(GoatHornItemID, 1)
	if !hornStack.IsEmpty() {
		hornEntityID, _ = s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.5, ent.Z, hornStack)
		if hornEntityID > 0 {
			mgr.mu.Lock()
			mgr.hornInstrument[hornEntityID] = instrument
			mgr.mu.Unlock()
		}
	}
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundGoatHornBreak, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return hornEntityID, instrument, true
}

// ExecuteGoatLongJump performs a Goat's high long-jump (up to 10 blocks high and
// 5 blocks forward) with PoseLongJumping (6).
func (s *Server) ExecuteGoatLongJump(entityID int32, rng *rand.Rand) (vx, vy, vz float64, jumped bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return 0, 0, 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeGoat {
		return 0, 0, 0, false
	}
	st := s.ensureGoatState(entityID, false, s.IsMobBaby(entityID))
	mgr := s.goatManager()
	mgr.mu.Lock()
	st.Pose = PoseLongJumping
	cd := GoatLongJumpMinCooldownTicks
	if rng != nil {
		cd += rng.Intn(GoatLongJumpMaxCooldownTicks - GoatLongJumpMinCooldownTicks + 1)
	}
	st.LongJumpCooldownTicks = cd
	snap := *st
	mgr.mu.Unlock()

	rad := float64(ent.Yaw) * (math.Pi / 180.0)
	vx = -math.Sin(rad) * GoatLongJumpHorizontalImpulse
	vy = GoatLongJumpVerticalImpulse
	vz = math.Cos(rad) * GoatLongJumpHorizontalImpulse

	s.entities.Update(entityID, func(e *world.Entity) {
		e.X += vx
		e.Z += vz
		e.VelocityX = clampVelocityShort(vx)
		e.VelocityY = clampVelocityShort(vy)
		e.VelocityZ = clampVelocityShort(vz)
		e.OnGround = false
	})
	s.broadcastGoatState(ent, snap)

	snd := SoundGoatLongJump
	if snap.IsScreaming {
		snd = SoundGoatScreamingLongJump
	}
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(snd, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return vx, vy, vz, true
}

// ExecuteGoatRam charges the Goat with entityID toward (targetX, targetY, targetZ):
//   - If a `#minecraft:snaps_goat_horn` block (stone, deepslate, packed_ice, ores,
//     or logs) is struck along the charge line, snaps off a horn and drops `minecraft:goat_horn`!
//   - Otherwise, if a player or mob is at the target position, deals ram damage
//     (5.0 HP adult / 1.0 HP baby) and strong knockback (2.5, or 1.25 if shield-blocking).
func (s *Server) ExecuteGoatRam(entityID int32, targetX, targetY, targetZ float64, rng *rand.Rand) (hitEntity bool, hornEntityID int32, instrument string) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false, 0, ""
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeGoat {
		return false, 0, ""
	}
	st := s.ensureGoatState(entityID, false, s.IsMobBaby(entityID))
	mgr := s.goatManager()
	mgr.mu.Lock()
	isScreaming := st.IsScreaming
	st.RamCooldownTicks = initialGoatRamCooldown(isScreaming, rng)
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	cp := ent.ChunkPos()

	dx := targetX - ent.X
	dz := targetZ - ent.Z
	dist := math.Hypot(dx, dz)
	if dist < 1e-4 {
		return false, 0, ""
	}
	yaw := yawToward(dx, dz)

	// 1. Raycast along the charge line for any `#minecraft:snaps_goat_horn` block.
	if cache != nil {
		steps := int(math.Ceil(dist * 2.0))
		feetY := int(math.Floor(ent.Y + 0.01))
		for step := 1; step <= steps; step++ {
			t := float64(step) / float64(steps)
			bx := int(math.Floor(ent.X + dx*t))
			bz := int(math.Floor(ent.Z + dz*t))
			for dy := 0; dy <= 1; dy++ {
				blockSt := cache.GetBlock(bx, feetY+dy, bz)
				if blockSt != world.StateAir && IsGoatHornSnapBlock(world.BlockName(blockSt)) {
					// Stop just before the hard block and snap off a horn!
					prevT := float64(step-1) / float64(steps)
					nx := ent.X + dx*prevT
					nz := ent.Z + dz*prevT
					s.entities.Update(entityID, func(e *world.Entity) {
						e.X = nx
						e.Z = nz
						e.Yaw = yaw
						e.HeadYaw = yaw
					})
					impSnd := SoundGoatRamImpact
					if isScreaming {
						impSnd = SoundGoatScreamingRamImpact
					}
					s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
						EncodeSoundPacket(impSnd, SoundSourceNeutral, nx, ent.Y, nz, 1.0, 1.0, 0))
					hornID, inst, _ := s.BreakGoatHorn(entityID, rng)
					return false, hornID, inst
				}
			}
		}
	}

	// 2. Move to target and ram any player or mob within 1.5 blocks of (targetX, targetY, targetZ).
	s.entities.Update(entityID, func(e *world.Entity) {
		e.X = targetX
		e.Z = targetZ
		e.Yaw = yaw
		e.HeadYaw = yaw
	})
	baseDmg := GoatRamDamage
	if s.IsMobBaby(entityID) {
		baseDmg = GoatBabyRamDamage
	}

	// Check players first.
	for _, sess := range s.onlinePlayersSnapshot() {
		if sess == nil || sess.IsDead() {
			continue
		}
		ps := sess.Snapshot()
		if world.NormalizeDimension(ps.Dimension) != dim || ps.GameMode == GameModeCreative || ps.GameMode == GameModeSpectator {
			continue
		}
		pdx := ps.X - targetX
		pdy := ps.Y - targetY
		pdz := ps.Z - targetZ
		if pdx*pdx+pdy*pdy+pdz*pdz <= 2.25 {
			kb := GoatRamKnockback
			if sess.IsBlockingWithShield() {
				kb *= 0.5
			} else {
				scaled := ScaleDamageByDifficulty(baseDmg, s.Difficulty())
				if scaled > 0 {
					s.DamagePlayerWithArmor(sess, scaled, "mob")
				}
			}
			vx, vy, vz := ComputeKnockback(ent.X, ent.Z, yaw, ps.X, ps.Z, kb)
			if sess.send != nil {
				_ = sess.send(protocol.PlaySetEntityMotion, EncodeSetEntityMotionPacket(sess.EntityID, vx, vy, vz))
			}
			hitEntity = true
			break
		}
	}

	// Check mobs if no player was hit.
	if !hitEntity {
		allEnts := s.entities.AppendAll(nil)
		for i := range allEnts {
			other := &allEnts[i]
			if other.ID == entityID || other.EffectiveDimension() != dim || other.IsItem() || other.IsExperienceOrb() {
				continue
			}
			odx := other.X - targetX
			ody := other.Y - targetY
			odz := other.Z - targetZ
			if odx*odx+ody*ody+odz*odz <= 2.25 {
				s.applyDamageToMob(other, baseDmg, nil, GoatRamKnockback, false, true)
				hitEntity = true
				break
			}
		}
	}

	impSnd := SoundGoatRamImpact
	if isScreaming {
		impSnd = SoundGoatScreamingRamImpact
	}
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(impSnd, SoundSourceNeutral, targetX, targetY, targetZ, 1.0, 1.0, 0))
	return hitEntity, 0, ""
}

// EncodeCooldownPacket encodes a ClientboundCooldownPacket (0x16) for protocol 775.
func EncodeCooldownPacket(cooldownGroup string, ticks int32) []byte {
	w := protocol.NewWriter(32)
	w.String(canonicalResourceName(cooldownGroup))
	w.VarInt(ticks)
	return w.Bytes()
}

// UseGoatHornItem handles a player right-clicking (`use_item`) with a
// `minecraft:goat_horn` in hand (0 = main hand, 1 = offhand):
// enforces the 140-tick (7.0s) cooldown, broadcasts the instrument sound
// (volume 16.0, audible up to 256 blocks), sends ClientboundCooldownPacket (0x16),
// and emits GameEventInstrumentPlay.
// (Can be called directly by Agent B's `play.go` `use_item` handler.)
func (s *Server) UseGoatHornItem(session *PlayerSession, hand int32, instrument string) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false
	}
	slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
	if hand == 1 {
		slotIdx = 45
	}
	held := session.InventorySlot(slotIdx)
	if held.IsEmpty() || canonicalResourceName(held.ResolvedID()) != GoatHornItemID {
		return false
	}

	gameTime, _ := s.WorldTime()
	mgr := s.goatManager()
	mgr.mu.Lock()
	if until, exists := mgr.playerHornTick[session]; exists && gameTime < until {
		mgr.mu.Unlock()
		return false
	}
	mgr.playerHornTick[session] = gameTime + int64(GoatHornCooldownTicks)
	mgr.mu.Unlock()

	if strings.TrimSpace(instrument) == "" {
		instrument = InstrumentPonderGoatHorn
	}
	snd := SoundForGoatHornInstrument(instrument)
	dim := world.NormalizeDimension(snap.Dimension)
	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)

	s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(snd, SoundSourceRecords, snap.X, snap.Y, snap.Z, 16.0, 1.0, 0))
	if session.send != nil {
		_ = session.send(PlayCooldownCB, EncodeCooldownPacket(GoatHornItemID, GoatHornCooldownTicks))
	}
	s.EmitVibration(VibrationEvent{
		Dimension:      dim,
		Type:           GameEventInstrumentPlay,
		X:              snap.X,
		Y:              snap.Y,
		Z:              snap.Z,
		Player:         session,
		SourceEntityID: session.EntityID,
	})
	return true
}

// InteractWithGoat handles a player right-clicking a `minecraft:goat`:
//   - Adult Goat + `minecraft:bucket` -> fills `minecraft:milk_bucket` and plays goat milk sound
//   - Goat + `minecraft:wheat`        -> feeds baby (+10% growth) or triggers love mode & breeding!
func (s *Server) InteractWithGoat(session *PlayerSession, entityID int32) (handled bool, babyGoatID int32) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false, 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeGoat {
		return false, 0
	}
	dx := ent.X - snap.X
	dy := ent.Y - snap.Y
	dz := ent.Z - snap.Z
	if snap.GameMode != GameModeCreative && (dx*dx+dy*dy+dz*dz) > MaxMeleeAttackReach*MaxMeleeAttackReach {
		return false, 0
	}
	held := session.HeldItem()
	if held.IsEmpty() {
		return false, 0
	}
	heldID := canonicalResourceName(held.ResolvedID())
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	st := s.ensureGoatState(entityID, false, s.IsMobBaby(entityID))

	// 1. Milking an adult Goat with an empty bucket.
	if heldID == EmptyBucketItemID && !s.IsMobBaby(entityID) {
		milk := world.NewNamedItemStack(MilkBucketItemID, 1)
		if milk.IsEmpty() {
			return false, 0
		}
		slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
		if snap.GameMode != GameModeCreative {
			if held.Count <= 1 {
				session.SetInventorySlot(slotIdx, milk)
			} else {
				session.SetInventorySlot(slotIdx, world.NewItemStack(held.ResolvedItemID(), held.Count-1))
				_, _ = session.AddItemToInventory(milk)
			}
		} else {
			_, _ = session.AddItemToInventory(milk)
		}
		milkSnd := SoundGoatMilk
		if st != nil && st.IsScreaming {
			milkSnd = SoundGoatScreamingMilk
		}
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(milkSnd, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		return true, 0
	}

	// 2. Feeding / breeding with wheat.
	if heldID != WheatItemID {
		return false, 0
	}
	if s.IsMobBaby(entityID) {
		fed, newAge := s.FeedBabyMob(session, entityID, heldID)
		if fed && newAge >= 0 && st != nil {
			mgr := s.goatManager()
			mgr.mu.Lock()
			st.HasLeftHorn = true
			st.HasRightHorn = true
			snapSt := *st
			mgr.mu.Unlock()
			s.broadcastGoatState(ent, snapSt)
		}
		return fed, 0
	}
	if s.MobBreedCooldown(entityID) > 0 || s.MobInLoveTicks(entityID) > 0 {
		return false, 0
	}
	if !s.SetMobInLove(entityID, AnimalLoveDurationTicks, session) {
		return false, 0
	}
	consumeOneHeldItemInSurvival(session)

	parentScreaming := st != nil && st.IsScreaming
	babyID, bred := s.TryBreedMobWithNearbyMate(entityID, AnimalBreedRange, nil)
	if bred && babyID > 0 {
		childSt := s.ensureGoatState(babyID, parentScreaming, true)
		if childSt != nil {
			mgr := s.goatManager()
			mgr.mu.Lock()
			if parentScreaming {
				childSt.IsScreaming = true
			}
			childSt.HasLeftHorn = false
			childSt.HasRightHorn = false
			snapChild := *childSt
			mgr.mu.Unlock()
			if childEnt := s.entities.Get(babyID); childEnt != nil {
				s.broadcastGoatState(childEnt, snapChild)
			}
		}
		return true, babyID
	}
	return true, 0
}

// TickGoats advances all `minecraft:goat` entities by one server tick.
func (s *Server) TickGoats(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	allEnts := s.entities.AppendAll(nil)
	mgr := s.goatManager()

	for i := range allEnts {
		ent := &allEnts[i]
		if canonicalResourceName(ent.TypeName) != MobTypeGoat {
			continue
		}
		isBaby := s.IsMobBaby(ent.ID)
		st := s.ensureGoatState(ent.ID, false, isBaby)
		if st == nil {
			continue
		}
		mgr.mu.Lock()
		if st.RamCooldownTicks > 0 {
			st.RamCooldownTicks--
		}
		if st.LongJumpCooldownTicks > 0 {
			st.LongJumpCooldownTicks--
		}
		if st.Pose == PoseLongJumping && ent.OnGround {
			st.Pose = PoseStanding
			snap := *st
			mgr.mu.Unlock()
			s.broadcastGoatState(ent, snap)
			continue
		}
		mgr.mu.Unlock()
	}
}
