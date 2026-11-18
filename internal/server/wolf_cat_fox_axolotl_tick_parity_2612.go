package server

import (
	"encoding/binary"
	"errors"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Exact protocol 775 (Minecraft 26.1.2) EntityDataSerializers IDs and SynchedEntityData
// indices verified against `net.minecraft.network.syncher.EntityDataSerializers` and
// `Entity` -> `LivingEntity` -> `Mob` -> `AgeableMob` -> `Animal` -> `TamableAnimal`.
const (
	EntityDataSerializerOptionalLivingEntityRef int32 = 13 // EntityDataSerializers.OPTIONAL_LIVING_ENTITY_REFERENCE (bool + UUID)
	EntityDataSerializerCatVariant2612          int32 = 21 // EntityDataSerializers.CAT_VARIANT
	EntityDataSerializerCatSoundVariant2612     int32 = 22 // EntityDataSerializers.CAT_SOUND_VARIANT
	EntityDataSerializerWolfVariant2612         int32 = 25 // EntityDataSerializers.WOLF_VARIANT
	EntityDataSerializerWolfSoundVariant2612    int32 = 26 // EntityDataSerializers.WOLF_SOUND_VARIANT

	EntityDataAgeLockedID        byte = 17 // AgeableMob.AGE_LOCKED (BOOLEAN = 8)
	EntityDataTamableOwnerUUIDID byte = 19 // TamableAnimal.DATA_OWNERUUID_ID (OPTIONAL_LIVING_ENTITY_REFERENCE = 13)
	EntityDataWolfSoundVariantID byte = 24 // Wolf.DATA_SOUND_VARIANT_ID (WOLF_SOUND_VARIANT = 26)
	EntityDataCatSoundVariantID  byte = 24 // Cat.DATA_SOUND_VARIANT_ID (CAT_SOUND_VARIANT = 22)
	EntityDataFoxTrustedID0      byte = 20 // Fox.DATA_TRUSTED_ID_0 (OPTIONAL_LIVING_ENTITY_REFERENCE = 13)
	EntityDataFoxTrustedID1      byte = 21 // Fox.DATA_TRUSTED_ID_1 (OPTIONAL_LIVING_ENTITY_REFERENCE = 13)

	SoundCatRoyalHiss = "minecraft:entity.cat_royal.hiss"

	// Vanilla 26.1.2 Wolf persistent anger range (`UniformInt.of(20, 39)` seconds = 400..780 ticks) & pack alert radius.
	WolfPersistentAngerMinTicks = 400
	WolfPersistentAngerMaxTicks = 780
	WolfPackAlertRadius         = 16.0

	// Vanilla 26.1.2 Wild Cat stealth tempt / scare constants (`CatTemptGoal`, `CatAvoidEntityGoal`).
	WildCatAvoidRange          = 16.0
	WildCatWalkSpeedMod        = 0.8
	WildCatSprintSpeedMod      = 1.33
	WildCatCrouchTemptSpeedMod = 0.6
	WildCatScareCooldownTicks  = 100

	// Vanilla 26.1.2 Fox berry-harvesting, pounce, and snow faceplant constants.
	FoxBerrySearchRadiusXZ = 12
	FoxBerryWaitTicksMax   = 40
	FoxSpitPickupDelay     = 40
	FoxFaceplantDuration   = 40
	FoxFaceplantPitch      = float32(60.0)
	SoundSweetBerryPick    = "minecraft:block.sweet_berry_bush.pick_berries"
	SoundCaveVinesPick     = "minecraft:block.cave_vines.pick_berries"

	// Vanilla 26.1.2 Axolotl dry-out damage & splash water rehydrate bonus.
	AxolotlDryOutDamage      float32 = 2.0
	AxolotlRehydrateAirBonus int     = 1800
)

// AxolotlBucketData2612 stores per-item-stack entity data for `minecraft:axolotl_bucket`
// matching vanilla `Bucketable.saveDefaultDataToBucketTag` + `Axolotl.saveToBucketTag`.
type AxolotlBucketData2612 struct {
	Variant         int32
	AgeTicks        int
	AgeLocked       bool
	Health          float32
	HuntingCooldown int64
	NoAI            bool
	Silent          bool
	NoGravity       bool
	Glowing         bool
	Invulnerable    bool
	CustomName      string
}

// Clone returns a value copy of d.
func (d *AxolotlBucketData2612) Clone() *AxolotlBucketData2612 {
	if d == nil {
		return nil
	}
	cp := *d
	return &cp
}

type wildCatScareTracker struct {
	mu          sync.Mutex
	scaredUntil map[int32]int // catEntityID -> remaining scare ticks
}

var serverWildCatScareTrackers sync.Map // map[*Server]*wildCatScareTracker

func (s *Server) wildCatScareMgr() *wildCatScareTracker {
	if v, ok := serverWildCatScareTrackers.Load(s); ok {
		return v.(*wildCatScareTracker)
	}
	actual, _ := serverWildCatScareTrackers.LoadOrStore(s, &wildCatScareTracker{
		scaredUntil: make(map[int32]int),
	})
	return actual.(*wildCatScareTracker)
}

// RollWolfSoundVariant2612 picks a random `minecraft:wolf_sound_variant` per `WolfSoundVariants.pickRandomSoundVariant`.
func RollWolfSoundVariant2612(rng *rand.Rand) string {
	return SelectRandomWolfSoundVariant(rng)
}

// RollCatSoundVariant2612 picks a random `minecraft:cat_sound_variant` per `CatSoundVariants.pickRandomSoundVariant`.
func RollCatSoundVariant2612(rng *rand.Rand) string {
	return SelectRandomCatSoundVariant(rng)
}

// CatHissSoundForEntity2612 returns the hiss sound event for a cat entity based on its `sound_variant`.
func (s *Server) CatHissSoundForEntity2612(entityID int32) string {
	if s != nil && s.CatSoundVariant(entityID) == CatSoundVariantRoyal {
		return SoundCatRoyalHiss
	}
	return SoundCatHiss
}

// HissAllCatsNearPhantom2612 implements `Phantom$PhantomSweepAttackGoal.canContinueToUse()` (`Phantom.java:2456-2472`):
// scans ONLY `Cat.class` (`minecraft:cat`, NOT `minecraft:ocelot`) within `getBoundingBox().inflate(16.0D)`,
// calls `cat.hiss()` on all cats in range, and returns whether at least one cat scared the swooping Phantom.
func (s *Server) HissAllCatsNearPhantom2612(dim string, px, py, pz float64, phantomSize int32) (catX, catY, catZ float64, hissSound string, foundCat bool) {
	if s == nil || s.entities == nil {
		return 0, 0, 0, "", false
	}
	normDim := world.NormalizeDimension(dim)
	w, h, _, _ := PhantomDimensionsAndDamage2612(phantomSize)
	halfW := float64(w) * 0.5
	minX, maxX := px-halfW-PhantomCatScareRange, px+halfW+PhantomCatScareRange
	minY, maxY := py-PhantomCatScareRange, py+float64(h)+PhantomCatScareRange
	minZ, maxZ := pz-halfW-PhantomCatScareRange, pz+halfW+PhantomCatScareRange
	const catHalfW = 0.3
	const catHeight = 0.7

	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		ent := &allEnts[i]
		if ent.EffectiveDimension() != normDim || canonicalResourceName(ent.TypeName) != MobTypeCat {
			continue
		}
		if s.MobHealth(ent.ID) <= 0 {
			continue
		}
		if ent.X+catHalfW < minX || ent.X-catHalfW > maxX ||
			ent.Y+catHeight < minY || ent.Y > maxY ||
			ent.Z+catHalfW < minZ || ent.Z-catHalfW > maxZ {
			continue
		}
		snd := s.CatHissSoundForEntity2612(ent.ID)
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(snd, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		if !foundCat {
			foundCat = true
			catX, catY, catZ = ent.X, ent.Y, ent.Z
			hissSound = snd
		}
	}
	return catX, catY, catZ, hissSound, foundCat
}

// EncodeWolfVariantAndSoundDataPacket2612 encodes Wolf.DATA_VARIANT_ID (23, WOLF_VARIANT=25)
// and Wolf.DATA_SOUND_VARIANT_ID (24, WOLF_SOUND_VARIANT=26).
func EncodeWolfVariantAndSoundDataPacket2612(entityID int32, variant, soundVariant string) []byte {
	w := protocol.NewWriter(20)
	w.VarInt(entityID)

	w.Byte(EntityDataWolfVariantID)
	w.VarInt(EntityDataSerializerWolfVariant2612)
	w.VarInt(WolfVariantRegistryIndex(variant))

	w.Byte(EntityDataWolfSoundVariantID)
	w.VarInt(EntityDataSerializerWolfSoundVariant2612)
	w.VarInt(WolfSoundVariantRegistryIndex(soundVariant))

	w.Byte(0xFF)
	return w.Bytes()
}

// EncodeCatFullEntityDataPacket2612 encodes Cat TamableAnimal flags (18), Optional OwnerUUID (19),
// CatVariant (20, CAT_VARIANT=21), IsLying (21), RelaxStateOne (22), CollarColor (23), and
// CatSoundVariant (24, CAT_SOUND_VARIANT=22).
func EncodeCatFullEntityDataPacket2612(entityID int32, tamed, sitting bool, ownerUUID [16]byte, variant string, lying, relaxed bool, collarColor int32, soundVariant string) []byte {
	var flags byte
	if sitting {
		flags |= TamableFlagSitting
	}
	if tamed {
		flags |= TamableFlagTamed
	}
	w := protocol.NewWriter(64)
	w.VarInt(entityID)

	w.Byte(EntityDataTamableFlagsID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)

	if tamed && ownerUUID != ([16]byte{}) {
		w.Byte(EntityDataTamableOwnerUUIDID)
		w.VarInt(EntityDataSerializerOptionalLivingEntityRef)
		w.Bool(true)
		w.UUID(ownerUUID)
	}

	w.Byte(EntityDataCatVariantID)
	w.VarInt(EntityDataSerializerCatVariant2612)
	w.VarInt(CatVariantRegistryIndex(variant))

	w.Byte(EntityDataCatIsLyingID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(lying)

	w.Byte(EntityDataCatRelaxStateOneID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(relaxed)

	w.Byte(EntityDataCatCollarColor26ID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(collarColor)

	w.Byte(EntityDataCatSoundVariantID)
	w.VarInt(EntityDataSerializerCatSoundVariant2612)
	w.VarInt(CatSoundVariantRegistryIndex(soundVariant))

	w.Byte(0xFF)
	return w.Bytes()
}

// CanWolfArmorAbsorbCause2612 implements `Wolf.canArmorAbsorb(DamageSource)` against
// vanilla 26.1.2 `data/minecraft/tags/damage_type/bypasses_wolf_armor.json`:
// returns false for `out_of_world`, `generic_kill`, `cramming`, `drown`, `dry_out`,
// `freeze`, `in_wall`, `indirect_magic`, `magic`, `outside_border`, `starve`, `thorns`, `wither`.
func CanWolfArmorAbsorbCause2612(cause string) bool {
	norm := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(cause)), "minecraft:")
	switch norm {
	case "out_of_world", "void", "generic_kill", "cramming", "drown", "dry_out",
		"freeze", "in_wall", "suffocation", "indirect_magic", "magic",
		"outside_border", "starve", "thorns", "wither":
		return false
	default:
		return true
	}
}

// MixCollarDyeColors2612 implements `DyeColor.getMixedColor(ServerLevel, DyeColor, DyeColor)`
// used by `Wolf.getBreedOffspring` and `Cat.getBreedOffspring` in vanilla 26.1.2:
// blends the 9 shapeless 2-dye crafting combinations, or picks randomly between parentA and parentB.
func MixCollarDyeColors2612(colorA, colorB int32, rng *rand.Rand) int32 {
	if colorA == colorB {
		return colorA
	}
	lo, hi := colorA, colorB
	if lo > hi {
		lo, hi = hi, lo
	}
	switch [2]int32{lo, hi} {
	case [2]int32{11, 13}: // blue + green -> cyan
		return 9
	case [2]int32{0, 15}: // white + black -> gray
		return 7
	case [2]int32{0, 11}: // white + blue -> light_blue
		return 3
	case [2]int32{0, 7}: // white + gray -> light_gray
		return 8
	case [2]int32{0, 13}: // white + green -> lime
		return 5
	case [2]int32{6, 10}: // pink + purple -> magenta
		return 2
	case [2]int32{4, 14}: // yellow + red -> orange
		return 1
	case [2]int32{0, 14}: // white + red -> pink
		return 6
	case [2]int32{11, 14}: // blue + red -> purple
		return 10
	}
	if rng != nil && rng.Intn(2) == 1 {
		return colorB
	}
	return colorA
}

// RollWolfAngerTicks2612 rolls `Wolf.PERSISTENT_ANGER_TIME = TimeUtil.rangeOfSeconds(20, 39)` (400..780 ticks).
func RollWolfAngerTicks2612(rng *rand.Rand) int {
	if rng == nil {
		return WolfPersistentAngerMinTicks
	}
	return WolfPersistentAngerMinTicks + rng.Intn(WolfPersistentAngerMaxTicks-WolfPersistentAngerMinTicks+1)
}

// AlertWildWolfPack2612 alerts nearby untamed wolves within `WolfPackAlertRadius` (16 blocks)
// when an untamed wolf is attacked (`HurtByTargetGoal.setAlertOthers()`).
func (s *Server) AlertWildWolfPack2612(provokedWolfID int32, attacker *PlayerSession, rng *rand.Rand) int {
	if s == nil || s.entities == nil || provokedWolfID <= 0 || attacker == nil {
		return 0
	}
	src := s.entities.Get(provokedWolfID)
	if src == nil {
		return 0
	}
	dim := src.EffectiveDimension()
	maxDistSq := WolfPackAlertRadius * WolfPackAlertRadius
	allEnts := s.entities.AppendAll(nil)
	alerted := 0
	for i := range allEnts {
		other := &allEnts[i]
		if other.ID == provokedWolfID || other.EffectiveDimension() != dim || canonicalResourceName(other.TypeName) != MobTypeWolf {
			continue
		}
		if s.IsMobTamed(other.ID) {
			continue
		}
		dx := other.X - src.X
		dy := other.Y - src.Y
		dz := other.Z - src.Z
		if dx*dx+dy*dy+dz*dz > maxDistSq {
			continue
		}
		st := s.ensureTamedMobState(other.ID, MobTypeWolf)
		mgr := s.tamedManager()
		mgr.mu.Lock()
		if !st.Tamed && st.AngerTicks <= 0 {
			st.AngerTicks = RollWolfAngerTicks2612(rng)
			st.TargetPlayer = attacker
			snap := *st
			mgr.mu.Unlock()
			s.broadcastTamedMobMetadata(other, snap)
			alerted++
		} else {
			mgr.mu.Unlock()
		}
	}
	return alerted
}

// IsWildWolfPreyOrSkeletonTarget2612 reports whether untamed wolves naturally hunt `mobType`
// in vanilla `Wolf.registerGoals` (`sheep`, `rabbit`, `fox`, baby `turtle`, and `AbstractSkeleton`).
func (s *Server) IsWildWolfPreyOrSkeletonTarget2612(target *world.Entity) bool {
	if target == nil {
		return false
	}
	switch canonicalResourceName(target.TypeName) {
	case "minecraft:sheep", "minecraft:rabbit", MobTypeFox,
		"minecraft:skeleton", "minecraft:stray", "minecraft:wither_skeleton", "minecraft:bogged", "minecraft:parched":
		return true
	case MobTypeTurtle:
		return s != nil && s.IsMobBaby(target.ID) && !IsMobEntityInWater2612(s, target)
	default:
		return false
	}
}

// IsCatSittingOnChestAt implements `ChestBlock.isCatSittingOnChest(LevelAccessor, BlockPos)`:
// checks whether any living `minecraft:cat` in `dimension` with `IsMobSitting(id) == true`
// intersects the `[x, y+1, z] .. [x+1, y+2, z+1]` AABB above the chest.
func (s *Server) IsCatSittingOnChestAt(dimension string, x, y, z int) bool {
	if s == nil || s.entities == nil {
		return false
	}
	normDim := world.NormalizeDimension(dimension)
	minX, maxX := float64(x), float64(x+1)
	minY, maxY := float64(y+1), float64(y+2)
	minZ, maxZ := float64(z), float64(z+1)
	const catHalfW = 0.3
	const catHeight = 0.7

	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		ent := &allEnts[i]
		if ent.EffectiveDimension() != normDim || canonicalResourceName(ent.TypeName) != MobTypeCat {
			continue
		}
		if s.MobHealth(ent.ID) <= 0 || !s.IsMobSitting(ent.ID) {
			continue
		}
		if ent.X+catHalfW > minX && ent.X-catHalfW < maxX &&
			ent.Y+catHeight > minY && ent.Y < maxY &&
			ent.Z+catHalfW > minZ && ent.Z-catHalfW < maxZ {
			return true
		}
	}
	return false
}

// EvaluateWildCatTemptAndScare2612 checks `Cat.CatTemptGoal` & `Cat.CatAvoidEntityGoal`:
// returns false (and marks the wild cat scared for 100 ticks + nudges it away) if the
// player is sprinting when trying to feed/tame an untamed cat.
func (s *Server) EvaluateWildCatTemptAndScare2612(session *PlayerSession, catEnt *world.Entity) bool {
	if s == nil || session == nil || catEnt == nil {
		return false
	}
	tr := s.wildCatScareMgr()
	if session.IsSprinting() {
		tr.mu.Lock()
		tr.scaredUntil[catEnt.ID] = WildCatScareCooldownTicks
		tr.mu.Unlock()
		pSnap := session.Snapshot()
		dx := catEnt.X - pSnap.X
		dz := catEnt.Z - pSnap.Z
		dist := math.Hypot(dx, dz)
		if dist > 1e-4 {
			cache := s.DimensionChunks(catEnt.EffectiveDimension())
			feetY := int(math.Floor(catEnt.Y + 0.01))
			s.stepEntityTowardPoint(catEnt, cache, feetY, catEnt.X+(dx/dist)*1.5, catEnt.Y, catEnt.Z+(dz/dist)*1.5, 0.24, 0.1)
		}
		return false
	}
	if session.IsSneaking() {
		tr.mu.Lock()
		delete(tr.scaredUntil, catEnt.ID)
		tr.mu.Unlock()
		return true
	}
	tr.mu.Lock()
	rem := tr.scaredUntil[catEnt.ID]
	if rem > 0 {
		tr.scaredUntil[catEnt.ID] = rem - 1
		tr.mu.Unlock()
		return false
	}
	tr.mu.Unlock()
	return true
}

// HasFoxDaytimeShelter2612 implements `Fox.FoxBehaviorGoal.hasShelter()`:
// returns true when `!level.canSeeSky(blockPos)` (there is at least one non-air block above `(bx, by, bz)`).
func HasFoxDaytimeShelter2612(cache *world.Cache, bx, by, bz int) bool {
	if cache == nil {
		return true
	}
	maxY := world.MinY + world.WorldHeight
	for y := by + 1; y < maxY; y++ {
		if cache.GetBlock(bx, y, bz) != world.StateAir {
			return true
		}
	}
	return false
}

// FoxLeftoverContainerAfterEat2612 returns the leftover container (`minecraft:bowl` or `minecraft:glass_bottle`)
// that remains in a fox's mouth after `Fox.finishUsingItem` consumes a stew/soup or honey bottle.
func FoxLeftoverContainerAfterEat2612(itemID string) world.ItemStack {
	switch canonicalResourceName(itemID) {
	case "minecraft:mushroom_stew", "minecraft:rabbit_stew", "minecraft:beetroot_soup", "minecraft:suspicious_stew":
		return world.NewNamedItemStack("minecraft:bowl", 1)
	case "minecraft:honey_bottle":
		return world.NewNamedItemStack("minecraft:glass_bottle", 1)
	default:
		return world.ItemStack{}
	}
}

// EncodeFoxFullEntityDataPacket2612 encodes Fox.DATA_TYPE_ID (18, INT), Fox.DATA_FLAGS_ID (19, BYTE),
// Fox.DATA_TRUSTED_ID_0 (20, OPTIONAL_LIVING_ENTITY_REFERENCE), and Fox.DATA_TRUSTED_ID_1 (21).
func EncodeFoxFullEntityDataPacket2612(entityID int32, variant int32, flags byte, trusted [][16]byte) []byte {
	w := protocol.NewWriter(64)
	w.VarInt(entityID)

	w.Byte(EntityDataFoxVariantID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(variant)

	w.Byte(EntityDataFoxFlagsID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)

	w.Byte(EntityDataFoxTrustedID0)
	w.VarInt(EntityDataSerializerOptionalLivingEntityRef)
	if len(trusted) >= 1 && trusted[0] != ([16]byte{}) {
		w.Bool(true)
		w.UUID(trusted[0])
	} else {
		w.Bool(false)
	}

	w.Byte(EntityDataFoxTrustedID1)
	w.VarInt(EntityDataSerializerOptionalLivingEntityRef)
	if len(trusted) >= 2 && trusted[1] != ([16]byte{}) {
		w.Bool(true)
		w.UUID(trusted[1])
	} else {
		w.Bool(false)
	}

	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeFoxFullEntityDataPacket2612 decodes a Fox metadata packet produced by EncodeFoxFullEntityDataPacket2612.
func DecodeFoxFullEntityDataPacket2612(r *protocol.Reader) (entityID int32, variant int32, flags byte, trusted [][16]byte, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, 0, 0, nil, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, 0, 0, nil, err
		}
		if idx == 0xFF {
			return entityID, variant, flags, trusted, nil
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, 0, 0, nil, err
		}
		switch idx {
		case EntityDataFoxVariantID:
			if ser != EntityDataSerializerInt {
				return 0, 0, 0, nil, errors.New("fox: unexpected DATA_TYPE_ID serializer")
			}
			variant, err = r.VarInt()
		case EntityDataFoxFlagsID:
			if ser != EntityDataSerializerByte {
				return 0, 0, 0, nil, errors.New("fox: unexpected DATA_FLAGS_ID serializer")
			}
			flags, err = r.ReadByte()
		case EntityDataFoxTrustedID0, EntityDataFoxTrustedID1:
			if ser != EntityDataSerializerOptionalLivingEntityRef {
				return 0, 0, 0, nil, errors.New("fox: unexpected DATA_TRUSTED serializer")
			}
			var present bool
			present, err = r.Bool()
			if err == nil && present {
				var u [16]byte
				u, err = r.UUID()
				if err == nil {
					trusted = append(trusted, u)
				}
			}
		default:
			return 0, 0, 0, nil, errors.New("fox: unexpected metadata index")
		}
		if err != nil {
			return 0, 0, 0, nil, err
		}
	}
}

func isHarvestableFoxBerryBlock2612(state uint16) (isBush, isVine bool, age int) {
	name := world.BlockName(state)
	props := world.BlockProperties(state)
	switch name {
	case "minecraft:sweet_berry_bush":
		a, err := strconv.Atoi(props["age"])
		if err == nil && a >= 2 {
			return true, false, a
		}
	case "minecraft:cave_vines", "minecraft:cave_vines_plant":
		if props["berries"] == "true" {
			return false, true, 0
		}
	}
	return false, false, 0
}

// TryFoxHarvestNearbyBerries2612 implements `Fox.FoxEatBerriesGoal` (`Fox.java:2507-2779`):
//   - Scans a `12x3x12` region around the non-sleeping fox for `sweet_berry_bush[age>=2]`
//     or `cave_vines`/`cave_vines_plant[berries=true]`.
//   - Moves within `2.0` blocks (`MoveToBlockGoal.acceptedDistance`), waits `40` ticks, and
//     when `s.MobGriefing()` is true harvests berries (`age -> 1` or `berries -> false`),
//     placing 1 berry in the fox's mouth if empty and dropping the remainder.
func (s *Server) TryFoxHarvestNearbyBerries2612(foxEntityID int32, rng *rand.Rand) bool {
	if s == nil || s.entities == nil || foxEntityID <= 0 {
		return false
	}
	ent := s.entities.Get(foxEntityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeFox {
		return false
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return false
	}
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	fs := mgr.foxes[foxEntityID]
	if fs == nil || (fs.Flags&(FoxFlagSleeping|FoxFlagFaceplanted)) != 0 {
		mgr.mu.Unlock()
		return false
	}
	mgr.mu.Unlock()

	bx0 := int(math.Floor(ent.X))
	by0 := int(math.Floor(ent.Y))
	bz0 := int(math.Floor(ent.Z))

	found := false
	bestX, bestY, bestZ := 0, 0, 0
	bestDistSq := float64(FoxBerrySearchRadiusXZ*FoxBerrySearchRadiusXZ*4 + 100)
	for dy := -1; dy <= 1; dy++ {
		y := by0 + dy
		if y < world.MinY || y >= world.MinY+world.WorldHeight {
			continue
		}
		for dx := -FoxBerrySearchRadiusXZ; dx <= FoxBerrySearchRadiusXZ; dx++ {
			for dz := -FoxBerrySearchRadiusXZ; dz <= FoxBerrySearchRadiusXZ; dz++ {
				tx, tz := bx0+dx, bz0+dz
				isBush, isVine, _ := isHarvestableFoxBerryBlock2612(cache.GetBlock(tx, y, tz))
				if !isBush && !isVine {
					continue
				}
				ddx := (float64(tx) + 0.5) - ent.X
				ddy := float64(y) - ent.Y
				ddz := (float64(tz) + 0.5) - ent.Z
				dSq := ddx*ddx + ddy*ddy + ddz*ddz
				if dSq < bestDistSq {
					bestDistSq = dSq
					bestX, bestY, bestZ = tx, y, tz
					found = true
				}
			}
		}
	}
	if !found {
		mgr.mu.Lock()
		if cur := mgr.foxes[foxEntityID]; cur != nil {
			cur.BerryWaitTicks = 0
		}
		mgr.mu.Unlock()
		return false
	}

	targetCX := float64(bestX) + 0.5
	targetCZ := float64(bestZ) + 0.5
	if bestDistSq > 4.0 {
		feetY := int(math.Floor(ent.Y + 0.01))
		s.stepEntityTowardPoint(ent, cache, feetY, targetCX, float64(bestY), targetCZ, FoxHuntSpeed, 1.2)
		return false
	}

	mgr.mu.Lock()
	fs = mgr.foxes[foxEntityID]
	if fs == nil {
		mgr.mu.Unlock()
		return false
	}
	fs.Flags |= FoxFlagSitting
	fs.BerryWaitTicks++
	waitTicks := fs.BerryWaitTicks
	mgr.mu.Unlock()

	if waitTicks < FoxBerryWaitTicksMax {
		return false
	}

	mgr.mu.Lock()
	if cur := mgr.foxes[foxEntityID]; cur != nil {
		cur.BerryWaitTicks = 0
		cur.Flags &^= FoxFlagSitting
	}
	mgr.mu.Unlock()

	if !s.MobGriefing() {
		return false
	}

	curState := cache.GetBlock(bestX, bestY, bestZ)
	isBush, isVine, age := isHarvestableFoxBerryBlock2612(curState)
	if !isBush && !isVine {
		return false
	}

	if isBush {
		count := int32(1)
		if rng != nil {
			count += int32(rng.Intn(2))
		}
		if age == 3 {
			count++
		}
		mgr.mu.Lock()
		if cur := mgr.foxes[foxEntityID]; cur != nil && cur.HeldItem.IsEmpty() {
			cur.HeldItem = world.NewNamedItemStack("minecraft:sweet_berries", 1)
			cur.EatTicks = 0
			count--
		}
		mgr.mu.Unlock()
		if count > 0 {
			s.SpawnItemEntityInDimension(dim, targetCX, float64(bestY)+0.25, targetCZ, world.NewNamedItemStack("minecraft:sweet_berries", count))
		}
		if nextBush, ok := world.BlockStateByName("minecraft:sweet_berry_bush", map[string]string{"age": "1"}); ok {
			cache.SetBlock(bestX, bestY, bestZ, nextBush)
			s.broadcastMobBlockUpdate(dim, bestX, bestY, bestZ, nextBush)
		}
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundSweetBerryPick, SoundSourceBlocks, targetCX, float64(bestY), targetCZ, 1.0, 1.0, 0))
		return true
	}

	// Cave vines / cave_vines_plant (`CaveVines.use(null, ...)`):
	props := world.BlockProperties(curState)
	newProps := make(map[string]string, len(props)+1)
	for k, v := range props {
		newProps[k] = v
	}
	newProps["berries"] = "false"
	if nextVine, ok := world.BlockStateByName(world.BlockName(curState), newProps); ok {
		cache.SetBlock(bestX, bestY, bestZ, nextVine)
		s.broadcastMobBlockUpdate(dim, bestX, bestY, bestZ, nextVine)
	}
	dropGlow := int32(1)
	mgr.mu.Lock()
	if cur := mgr.foxes[foxEntityID]; cur != nil && cur.HeldItem.IsEmpty() {
		cur.HeldItem = world.NewNamedItemStack("minecraft:glow_berries", 1)
		cur.EatTicks = 0
		dropGlow = 0
	}
	mgr.mu.Unlock()
	if dropGlow > 0 {
		s.SpawnItemEntityInDimension(dim, targetCX, float64(bestY)+0.25, targetCZ, world.NewNamedItemStack("minecraft:glow_berries", 1))
	}
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundCaveVinesPick, SoundSourceBlocks, targetCX, float64(bestY), targetCZ, 1.0, 1.0, 0))
	return true
}

func isFoxSnowFaceplantBlock2612(state uint16) bool {
	switch world.BlockName(state) {
	case "minecraft:snow", "minecraft:powder_snow", "minecraft:snow_block":
		return true
	default:
		return false
	}
}

// TriggerFoxSnowFaceplant2612 puts a pouncing fox that missed its prey and landed in snow
// into `FoxFlagFaceplanted` (`0x40`, `Pitch = 60.0`) for `40` ticks (`FaceplantGoal`).
func (s *Server) TriggerFoxSnowFaceplant2612(foxEntityID int32) bool {
	if s == nil || s.entities == nil || foxEntityID <= 0 {
		return false
	}
	ent := s.entities.Get(foxEntityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeFox {
		return false
	}
	cache := s.DimensionChunks(ent.EffectiveDimension())
	bx := int(math.Floor(ent.X))
	by := int(math.Floor(ent.Y))
	bz := int(math.Floor(ent.Z))
	if cache != nil && !isFoxSnowFaceplantBlock2612(cache.GetBlock(bx, by, bz)) && !isFoxSnowFaceplantBlock2612(cache.GetBlock(bx, by-1, bz)) {
		return false
	}
	fs := s.ensureFoxState(foxEntityID)
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	fs.Flags &^= (FoxFlagPouncing | FoxFlagCrouching | FoxFlagSitting)
	fs.Flags |= FoxFlagFaceplanted
	fs.FaceplantTicks = FoxFaceplantDuration
	variant := fs.Variant
	flags := fs.Flags
	trusted := append([][16]byte(nil), fs.TrustedUUIDs...)
	mgr.mu.Unlock()

	s.entities.Update(foxEntityID, func(e *world.Entity) {
		e.Pitch = FoxFaceplantPitch
		e.VelocityX = 0
		e.VelocityY = 0
		e.VelocityZ = 0
		e.OnGround = true
	})
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeFoxFullEntityDataPacket2612(foxEntityID, variant, flags, trusted))
	return true
}

// ShouldAxolotlPlayDead2612 implements the exact condition in `Axolotl.hurtServer` (`26.1.2`):
// `!noAI && !isPlayingDead && inWater && hasAttacker && damage < curHP && rng.Intn(3) == 0 && (float32(rng.Intn(3)) < damage || curHP/maxHP < 0.5)`.
func ShouldAxolotlPlayDead2612(noAI, isPlayingDead, inWater, hasAttacker bool, damage, curHP, maxHP float32, rng *rand.Rand) bool {
	if noAI || isPlayingDead || !inWater || !hasAttacker || damage <= 0 || curHP <= 0 || damage >= curHP {
		return false
	}
	if maxHP <= 0 {
		maxHP = DefaultAxolotlMaxHealth
	}
	if rng == nil {
		return damage > 0 || curHP/maxHP < 0.5
	}
	if rng.Intn(3) != 0 {
		return false
	}
	secondRoll := rng.Intn(3)
	return float32(secondRoll) < damage || (curHP/maxHP) < 0.5
}

// IsAxolotlHydratedAt2612 reports whether an Axolotl is in water, bubble column, or rain (`isInWaterOrRain()`).
func IsAxolotlHydratedAt2612(s *Server, ent *world.Entity) bool {
	if s == nil || ent == nil {
		return true
	}
	if s.IsAxolotlInWater(ent.ID) {
		return true
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	if cache != nil {
		bx := int(math.Floor(ent.X))
		by := int(math.Floor(ent.Y))
		bz := int(math.Floor(ent.Z))
		if world.BlockName(cache.GetBlock(bx, by, bz)) == "minecraft:bubble_column" {
			return true
		}
		if s.IsRaining() && !HasFoxDaytimeShelter2612(cache, bx, by, bz) {
			return true
		}
	}
	return false
}

// RehydrateAxolotl2612 implements `Axolotl.rehydrate()` (+1800 ticks of air supply when hit by splash/lingering water).
func (s *Server) RehydrateAxolotl2612(entityID int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeAxolotl {
		return false
	}
	st := s.ensureAxolotlState(entityID, AxolotlVariantLucy)
	mgr := s.axolotlManager()
	mgr.mu.Lock()
	st.OutOfWaterTicks -= AxolotlRehydrateAirBonus
	if st.OutOfWaterTicks < 0 {
		st.OutOfWaterTicks = 0
	}
	mgr.mu.Unlock()
	return true
}

// ClearEnemyTargetsOnPlayingDeadAxolotl2612 drops active mob targets pointing at `axolotlID`
// when the Axolotl enters PlayDead (`Axolotl.canBeSeenAsEnemy() == false`).
func (s *Server) ClearEnemyTargetsOnPlayingDeadAxolotl2612(axolotlID int32) {
	if s == nil || axolotlID <= 0 {
		return
	}
	tm := s.tamedManager()
	tm.mu.Lock()
	for _, st := range tm.mobs {
		if st != nil && st.TargetEntityID == axolotlID {
			st.TargetEntityID = 0
		}
	}
	tm.mu.Unlock()
}

// --- Full 26.1.2 NBT Codecs for Wolf, Cat, Fox, Axolotl, and Axolotl Bucket ---

func petUUIDToNBTIntArray(u [16]byte) nbt.IntArray {
	return nbt.IntArray{
		int32(binary.BigEndian.Uint32(u[0:4])),
		int32(binary.BigEndian.Uint32(u[4:8])),
		int32(binary.BigEndian.Uint32(u[8:12])),
		int32(binary.BigEndian.Uint32(u[12:16])),
	}
}

func petNBTIntArrayToUUID(tag nbt.Tag) ([16]byte, bool) {
	ia, ok := tag.(nbt.IntArray)
	if !ok || len(ia) != 4 {
		return [16]byte{}, false
	}
	var out [16]byte
	binary.BigEndian.PutUint32(out[0:4], uint32(ia[0]))
	binary.BigEndian.PutUint32(out[4:8], uint32(ia[1]))
	binary.BigEndian.PutUint32(out[8:12], uint32(ia[2]))
	binary.BigEndian.PutUint32(out[12:16], uint32(ia[3]))
	return out, true
}

// EncodeWolfNBT2612 serializes a Wolf entity's 26.1.2 persistent NBT fields (`Owner`, `Sitting`,
// `CollarColor`, `variant`, `sound_variant`, `anger_end_time`, `angry_at`, `body_armor_item`).
func (s *Server) EncodeWolfNBT2612(entityID int32) (*nbt.Compound, bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return nil, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeWolf {
		return nil, false
	}
	tst, _ := s.GetTamedMobState(entityID)
	ws, _ := s.GetWolfDetailState(entityID)

	c := nbt.NewCompound().
		Set("id", nbt.String(MobTypeWolf)).
		Set("Health", nbt.Float(s.MobHealth(entityID))).
		Set("Age", nbt.Int(int32(s.MobAge(entityID)))).
		Set("Sitting", nbt.Byte(boolToByte2612(tst.Sitting))).
		Set("CollarColor", nbt.Byte(int8(tst.CollarColor))).
		Set("variant", nbt.String(s.WolfVariant(entityID))).
		Set("sound_variant", nbt.String(s.WolfSoundVariant(entityID)))
	if tst.Tamed && tst.OwnerUUID != ([16]byte{}) {
		c.Set("Owner", petUUIDToNBTIntArray(tst.OwnerUUID))
	}
	if tst.AngerTicks > 0 {
		c.Set("anger_end_time", nbt.Long(int64(tst.AngerTicks)))
		if tst.TargetPlayer != nil {
			c.Set("angry_at", petUUIDToNBTIntArray(tst.TargetPlayer.Profile.UUID))
		}
	} else {
		c.Set("anger_end_time", nbt.Long(-1))
	}
	if ws.HasArmor && !ws.ArmorStack.IsEmpty() {
		meta := ws.ArmorMeta.Clone()
		if ws.DyedRGB >= 0 {
			setWolfArmorDyedRGBOnMeta(&meta, ws.DyedRGB)
		}
		if itemComp := encodeAllaySingleItemNBT2612(ws.ArmorStack, meta, ""); itemComp != nil {
			c.Set("body_armor_item", itemComp)
		}
	}
	return c, true
}

// DecodeWolfNBT2612 restores a Wolf entity's 26.1.2 state from NBT onto `entityID`.
func (s *Server) DecodeWolfNBT2612(entityID int32, c *nbt.Compound) bool {
	if s == nil || s.entities == nil || entityID <= 0 || c == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeWolf {
		return false
	}
	st := s.ensureTamedMobState(entityID, MobTypeWolf)
	mgr := s.tamedManager()
	mgr.mu.Lock()
	if ownerTag, ok := c.Get("Owner"); ok {
		if u, okU := petNBTIntArrayToUUID(ownerTag); okU && u != ([16]byte{}) {
			st.Tamed = true
			st.OwnerUUID = u
		}
	}
	if sitB, ok := nbtGetByte(c, "Sitting"); ok {
		st.Sitting = sitB != 0
	}
	if colB, ok := nbtGetByte(c, "CollarColor"); ok {
		st.CollarColor = int32(uint8(colB) & 0x0F)
	}
	if endT, ok := nbtInt64(c, "anger_end_time"); ok && endT > 0 {
		st.AngerTicks = int(endT)
	} else {
		st.AngerTicks = 0
	}
	mgr.mu.Unlock()

	if hpTag, ok := c.Get("Health"); ok {
		if f, okF := hpTag.(nbt.Float); okF && f > 0 {
			s.SetMobHealth(entityID, float32(f))
		}
	}
	if age, ok := nbtGetInt(c, "Age"); ok {
		s.SetMobAge(entityID, int(age))
	}
	if v, ok := nbtGetString(c, "variant"); ok && v != "" {
		s.SetWolfVariant(entityID, v)
	}
	if sv, ok := nbtGetString(c, "sound_variant"); ok && sv != "" {
		s.SetWolfSoundVariant(entityID, sv)
	}
	if armorTag, ok := c.Get("body_armor_item"); ok {
		if ac, isC := armorTag.(*nbt.Compound); isC && ac != nil {
			if stack, meta, _, okItem := decodeAllaySingleItemNBT2612(ac); okItem {
				s.EquipWolfArmor(entityID, stack, meta)
			}
		}
	}
	return true
}

// EncodeCatNBT2612 serializes a Cat entity's 26.1.2 persistent NBT fields (`Owner`, `Sitting`,
// `variant`, `sound_variant`, `CollarColor`).
func (s *Server) EncodeCatNBT2612(entityID int32) (*nbt.Compound, bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return nil, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCat {
		return nil, false
	}
	tst, _ := s.GetTamedMobState(entityID)
	c := nbt.NewCompound().
		Set("id", nbt.String(MobTypeCat)).
		Set("Health", nbt.Float(s.MobHealth(entityID))).
		Set("Age", nbt.Int(int32(s.MobAge(entityID)))).
		Set("Sitting", nbt.Byte(boolToByte2612(tst.Sitting))).
		Set("CollarColor", nbt.Byte(int8(tst.CollarColor))).
		Set("variant", nbt.String(s.CatVariant(entityID))).
		Set("sound_variant", nbt.String(s.CatSoundVariant(entityID)))
	if tst.Tamed && tst.OwnerUUID != ([16]byte{}) {
		c.Set("Owner", petUUIDToNBTIntArray(tst.OwnerUUID))
	}
	return c, true
}

// DecodeCatNBT2612 restores a Cat entity's 26.1.2 state from NBT onto `entityID`.
func (s *Server) DecodeCatNBT2612(entityID int32, c *nbt.Compound) bool {
	if s == nil || s.entities == nil || entityID <= 0 || c == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCat {
		return false
	}
	st := s.ensureTamedMobState(entityID, MobTypeCat)
	mgr := s.tamedManager()
	mgr.mu.Lock()
	if ownerTag, ok := c.Get("Owner"); ok {
		if u, okU := petNBTIntArrayToUUID(ownerTag); okU && u != ([16]byte{}) {
			st.Tamed = true
			st.OwnerUUID = u
		}
	}
	if sitB, ok := nbtGetByte(c, "Sitting"); ok {
		st.Sitting = sitB != 0
	}
	if colB, ok := nbtGetByte(c, "CollarColor"); ok {
		st.CollarColor = int32(uint8(colB) & 0x0F)
	}
	mgr.mu.Unlock()

	if v, ok := nbtGetString(c, "variant"); ok && v != "" {
		s.SetCatVariant(entityID, v)
	}
	if sv, ok := nbtGetString(c, "sound_variant"); ok && sv != "" {
		s.SetCatSoundVariant(entityID, sv)
	}
	return true
}

// EncodeFoxNBT2612 serializes a Fox entity's 26.1.2 persistent NBT fields (`Trusted`, `Sleeping`,
// `Type`, `Sitting`, `Crouching`, and mouth `HeldItem`).
func (s *Server) EncodeFoxNBT2612(entityID int32) (*nbt.Compound, bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return nil, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeFox {
		return nil, false
	}
	fst, _ := s.GetFoxState(entityID)
	typeStr := "red"
	if fst.Variant == FoxVariantSnow {
		typeStr = "snow"
	}
	c := nbt.NewCompound().
		Set("id", nbt.String(MobTypeFox)).
		Set("Type", nbt.String(typeStr)).
		Set("Sleeping", nbt.Byte(boolToByte2612((fst.Flags&FoxFlagSleeping) != 0))).
		Set("Sitting", nbt.Byte(boolToByte2612((fst.Flags&FoxFlagSitting) != 0))).
		Set("Crouching", nbt.Byte(boolToByte2612((fst.Flags&FoxFlagCrouching) != 0)))
	if len(fst.TrustedUUIDs) > 0 {
		elems := make([]nbt.Tag, 0, len(fst.TrustedUUIDs))
		for _, u := range fst.TrustedUUIDs {
			if u != ([16]byte{}) {
				elems = append(elems, petUUIDToNBTIntArray(u))
			}
		}
		if len(elems) > 0 {
			c.Set("Trusted", nbt.List{ElemID: nbt.TagIntArray, Elems: elems})
		}
	}
	if !fst.HeldItem.IsEmpty() {
		if itemComp := encodeAllaySingleItemNBT2612(fst.HeldItem, ItemMeta{}, ""); itemComp != nil {
			c.Set("HeldItem", itemComp)
		}
	}
	return c, true
}

// DecodeFoxNBT2612 restores a Fox entity's 26.1.2 state from NBT onto `entityID`.
func (s *Server) DecodeFoxNBT2612(entityID int32, c *nbt.Compound) bool {
	if s == nil || s.entities == nil || entityID <= 0 || c == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeFox {
		return false
	}
	fs := s.ensureFoxState(entityID)
	if tStr, ok := nbtGetString(c, "Type"); ok {
		if strings.EqualFold(tStr, "snow") {
			s.SetFoxVariant(entityID, FoxVariantSnow)
		} else {
			s.SetFoxVariant(entityID, FoxVariantRed)
		}
	}
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	if slp, ok := nbtGetByte(c, "Sleeping"); ok {
		if slp != 0 {
			fs.Flags |= FoxFlagSleeping
		} else {
			fs.Flags &^= FoxFlagSleeping
		}
	}
	if sit, ok := nbtGetByte(c, "Sitting"); ok {
		if sit != 0 {
			fs.Flags |= FoxFlagSitting
		} else {
			fs.Flags &^= FoxFlagSitting
		}
	}
	if cr, ok := nbtGetByte(c, "Crouching"); ok {
		if cr != 0 {
			fs.Flags |= FoxFlagCrouching
		} else {
			fs.Flags &^= FoxFlagCrouching
		}
	}
	fs.TrustedUUIDs = nil
	if elems, ok := nbtGetListElems(c, "Trusted"); ok {
		for _, el := range elems {
			if u, okU := petNBTIntArrayToUUID(el); okU && u != ([16]byte{}) && len(fs.TrustedUUIDs) < 2 {
				fs.TrustedUUIDs = append(fs.TrustedUUIDs, u)
			}
		}
	}
	if heldTag, ok := c.Get("HeldItem"); ok {
		if hc, isC := heldTag.(*nbt.Compound); isC && hc != nil {
			if stack, _, _, okItem := decodeAllaySingleItemNBT2612(hc); okItem {
				fs.HeldItem = stack
			}
		}
	}
	mgr.mu.Unlock()
	return true
}

// EncodeAxolotlBucketNBT2612 serializes an `axolotl_bucket` item's 26.1.2 components
// (`minecraft:bucket_entity_data`, `minecraft:axolotl/variant`, `minecraft:custom_name`).
func EncodeAxolotlBucketNBT2612(data AxolotlBucketData2612) *nbt.Compound {
	be := nbt.NewCompound().
		Set("Age", nbt.Int(int32(data.AgeTicks))).
		Set("Health", nbt.Float(data.Health)).
		Set("HuntingCooldown", nbt.Long(data.HuntingCooldown)).
		Set("Variant", nbt.Int(data.Variant))
	if data.AgeLocked {
		be.Set("AgeLocked", nbt.Byte(1))
	}
	if data.NoAI {
		be.Set("NoAI", nbt.Byte(1))
	}
	if data.Silent {
		be.Set("Silent", nbt.Byte(1))
	}
	if data.NoGravity {
		be.Set("NoGravity", nbt.Byte(1))
	}
	if data.Glowing {
		be.Set("Glowing", nbt.Byte(1))
	}
	if data.Invulnerable {
		be.Set("Invulnerable", nbt.Byte(1))
	}
	comps := nbt.NewCompound().
		Set("minecraft:bucket_entity_data", be).
		Set("minecraft:axolotl/variant", nbt.Int(data.Variant))
	if strings.TrimSpace(data.CustomName) != "" {
		comps.Set("minecraft:custom_name", nbt.String(data.CustomName))
	}
	return nbt.NewCompound().
		Set("id", nbt.String(AxolotlBucketItemID)).
		Set("count", nbt.Int(1)).
		Set("components", comps)
}

// DecodeAxolotlBucketNBT2612 parses `AxolotlBucketData2612` from an `axolotl_bucket` item NBT compound.
func DecodeAxolotlBucketNBT2612(c *nbt.Compound) (AxolotlBucketData2612, bool) {
	if c == nil {
		return AxolotlBucketData2612{}, false
	}
	out := AxolotlBucketData2612{
		Variant: AxolotlVariantLucy,
		Health:  DefaultAxolotlMaxHealth,
	}
	compTag, ok := c.Get("components")
	if !ok {
		return out, false
	}
	cc, isC := compTag.(*nbt.Compound)
	if !isC || cc == nil {
		return out, false
	}
	if v, okV := nbtGetInt(cc, "minecraft:axolotl/variant"); okV {
		out.Variant = v
	}
	if cnTag, okCN := cc.Get("minecraft:custom_name"); okCN && cnTag != nil {
		out.CustomName = world.ExtractCustomNameFromNBTTag(cnTag)
	}
	if beTag, okBE := cc.Get("minecraft:bucket_entity_data"); okBE {
		if be, isBEC := beTag.(*nbt.Compound); isBEC && be != nil {
			if age, okA := nbtGetInt(be, "Age"); okA {
				out.AgeTicks = int(age)
			}
			if hpTag, okHP := be.Get("Health"); okHP {
				if f, okF := hpTag.(nbt.Float); okF && f > 0 {
					out.Health = float32(f)
				}
			}
			if hc, okHC := nbtInt64(be, "HuntingCooldown"); okHC {
				out.HuntingCooldown = hc
			}
			if v2, okV2 := nbtGetInt(be, "Variant"); okV2 {
				out.Variant = v2
			}
			if b, okB := nbtGetByte(be, "AgeLocked"); okB {
				out.AgeLocked = b != 0
			}
			if b, okB := nbtGetByte(be, "NoAI"); okB {
				out.NoAI = b != 0
			}
			if b, okB := nbtGetByte(be, "Silent"); okB {
				out.Silent = b != 0
			}
			if b, okB := nbtGetByte(be, "NoGravity"); okB {
				out.NoGravity = b != 0
			}
			if b, okB := nbtGetByte(be, "Glowing"); okB {
				out.Glowing = b != 0
			}
			if b, okB := nbtGetByte(be, "Invulnerable"); okB {
				out.Invulnerable = b != 0
			}
		}
	}
	return out, true
}

// EncodeAxolotlNBT2612 serializes a live `minecraft:axolotl` entity's 26.1.2 persistent NBT
// (`Variant`, `FromBucket`, `Age`, `Health`, `HuntingCooldown`).
func (s *Server) EncodeAxolotlNBT2612(entityID int32) (*nbt.Compound, bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return nil, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeAxolotl {
		return nil, false
	}
	st, _ := s.GetAxolotlState(entityID)
	c := nbt.NewCompound().
		Set("id", nbt.String(MobTypeAxolotl)).
		Set("Variant", nbt.Int(st.Variant)).
		Set("FromBucket", nbt.Byte(boolToByte2612(st.FromBucket))).
		Set("Age", nbt.Int(int32(s.MobAge(entityID)))).
		Set("Health", nbt.Float(s.MobHealth(entityID)))
	if st.PassiveHuntCooldown > 0 {
		c.Set("HuntingCooldown", nbt.Long(int64(st.PassiveHuntCooldown)))
	}
	return c, true
}

// DecodeAxolotlNBT2612 restores an Axolotl entity's 26.1.2 state from NBT onto `entityID`.
func (s *Server) DecodeAxolotlNBT2612(entityID int32, c *nbt.Compound) bool {
	if s == nil || s.entities == nil || entityID <= 0 || c == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeAxolotl {
		return false
	}
	st := s.ensureAxolotlState(entityID, AxolotlVariantLucy)
	mgr := s.axolotlManager()
	mgr.mu.Lock()
	if v, ok := nbtGetInt(c, "Variant"); ok && v >= AxolotlVariantLucy && v <= AxolotlVariantBlue {
		st.Variant = v
	}
	if fb, ok := nbtGetByte(c, "FromBucket"); ok {
		st.FromBucket = fb != 0
	}
	if hc, ok := nbtInt64(c, "HuntingCooldown"); ok && hc > 0 {
		st.PassiveHuntCooldown = int(hc)
	}
	snap := *st
	mgr.mu.Unlock()

	if age, ok := nbtGetInt(c, "Age"); ok {
		s.SetMobAge(entityID, int(age))
	}
	if hpTag, ok := c.Get("Health"); ok {
		if f, okF := hpTag.(nbt.Float); okF && f > 0 {
			s.SetMobHealth(entityID, float32(f))
		}
	}
	s.broadcastAxolotlState(ent, snap)
	return true
}

func boolToByte2612(b bool) int8 {
	if b {
		return 1
	}
	return 0
}
