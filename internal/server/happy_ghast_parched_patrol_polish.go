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

// Vanilla 26.1.2 (protocol 775) constants for Happy Ghast (minecraft:happy_ghast),
// Dried Ghast block hydration (minecraft:dried_ghast), Parched (minecraft:parched),
// Camel Husk (minecraft:camel_husk), Pillager Patrol Spawner (PatrolSpawner),
// and Wandering Trader Spawner (WanderingTraderSpawner).
const (
	// Happy Ghast (HappyGhast.java, EntityType ID 58 in 26.1.2).
	MobTypeHappyGhast      = "minecraft:happy_ghast"
	EntityTypeHappyGhastID = 58

	DefaultHappyGhastMaxHealth              float32 = 20.0
	HappyGhastFlyingSpeed                   float64 = 0.05
	HappyGhastMovementSpeed                 float64 = 0.05
	HappyGhastTemptRange                    float64 = 16.0
	HappyGhastFollowRange                   float64 = 16.0
	HappyGhastCameraDistance                float64 = 8.0
	HappyGhastBabyScale                     float32 = 0.2375
	HappyGhastAdultWidth                    float64 = 4.0
	HappyGhastAdultHeight                   float64 = 4.0
	HappyGhastMaxPassengers                 int     = 4
	HappyGhastFastHealingTicks              int     = 20
	HappyGhastSlowHealingTicks              int     = 600
	HappyGhastSmallRestrictionRadius        int     = 32
	HappyGhastLargeRestrictionRadius        int     = 64
	HappyGhastMaxStillTimeout               int     = 10
	HappyGhastSpeedMultiplierWhenPanicking  float64 = 2.0
	HappyGhastRiddenSpeedMultiplier         float64 = 3.9
	HappyGhastCloudsMinY                    float64 = 192.0
	HappyGhastCloudsMaxY                    float64 = 196.0

	// SynchedEntityData indices for HappyGhast (AgeableMob = 16, HappyGhast = 17, 18).
	EntityDataHappyGhastBabyID          byte = 16 // AgeableMob.DATA_BABY_ID (BOOLEAN serializer = 8)
	EntityDataHappyGhastIsLeashHolderID byte = 17 // HappyGhast.IS_LEASH_HOLDER (BOOLEAN serializer = 8)
	EntityDataHappyGhastStaysStillID    byte = 18 // HappyGhast.STAYS_STILL (BOOLEAN serializer = 8)

	// Dried Ghast block (DriedGhastBlock.java).
	DriedGhastBlockName          = "minecraft:dried_ghast"
	DriedGhastMaxHydrationLevel  = 3
	DriedGhastHydrationTickDelay = 5000

	// Happy Ghast, Ghastling & Dried Ghast sounds (SoundEvents.java in 26.1.2).
	SoundHappyGhastAmbient            = "minecraft:entity.happy_ghast.ambient"
	SoundHappyGhastDeath              = "minecraft:entity.happy_ghast.death"
	SoundHappyGhastHurt               = "minecraft:entity.happy_ghast.hurt"
	SoundHappyGhastRiding             = "minecraft:entity.happy_ghast.riding"
	SoundHappyGhastEquip              = "minecraft:entity.happy_ghast.equip"
	SoundHappyGhastUnequip            = "minecraft:entity.happy_ghast.unequip"
	SoundHappyGhastHarnessGogglesDown = "minecraft:entity.happy_ghast.harness_goggles_down"
	SoundHappyGhastHarnessGogglesUp   = "minecraft:entity.happy_ghast.harness_goggles_up"
	SoundGhastlingAmbient             = "minecraft:entity.ghastling.ambient"
	SoundGhastlingDeath               = "minecraft:entity.ghastling.death"
	SoundGhastlingHurt                = "minecraft:entity.ghastling.hurt"
	SoundGhastlingSpawn               = "minecraft:entity.ghastling.spawn"
	SoundDriedGhastBreak              = "minecraft:block.dried_ghast.break"
	SoundDriedGhastStep               = "minecraft:block.dried_ghast.step"
	SoundDriedGhastFall               = "minecraft:block.dried_ghast.fall"
	SoundDriedGhastAmbient            = "minecraft:block.dried_ghast.ambient"
	SoundDriedGhastAmbientWater       = "minecraft:block.dried_ghast.ambient_water"
	SoundDriedGhastPlace              = "minecraft:block.dried_ghast.place"
	SoundDriedGhastPlaceInWater       = "minecraft:block.dried_ghast.place_in_water"
	SoundDriedGhastTransition         = "minecraft:block.dried_ghast.transition"

	// Parched (Parched.java, EntityType ID 97 in 26.1.2).
	MobTypeParched      = "minecraft:parched"
	EntityTypeParchedID = 97

	DefaultParchedMaxHealth           float32 = 16.0
	ParchedHardAttackIntervalTicks    int     = 50
	ParchedNormalAttackIntervalTicks  int     = 70
	ParchedWeaknessArrowDurationTicks int32   = 600 // 30s (600 ticks) of Weakness I

	SoundParchedAmbient = "minecraft:entity.parched.ambient"
	SoundParchedDeath   = "minecraft:entity.parched.death"
	SoundParchedHurt    = "minecraft:entity.parched.hurt"
	SoundParchedStep    = "minecraft:entity.parched.step"

	// Camel Husk (CamelHusk.java, EntityType ID 20 in 26.1.2; base constants in equine_aquatic_polish.go).
	DefaultCamelHuskMaxHealth  float32 = 32.0
	CamelHuskChargeSpeedMod    float32 = 4.0
	CamelHuskFoodItemID                = "minecraft:rabbit_foot"
	CamelHuskHealAmount        float32 = 2.0
	HuskCamelJockeySpawnChance float32 = 0.10

	SoundCamelHuskStep     = "minecraft:entity.camel_husk.step"
	SoundCamelHuskStepSand = "minecraft:entity.camel_husk.step_sand"

	// Pillager Patrol Spawner (PatrolSpawner.java).
	PatrolSpawnerBaseDelayTicks     int = 12000 // 10 minutes
	PatrolSpawnerRandomDelayTicks   int = 1200  // +0..1199 ticks (up to 1 minute)
	PatrolSpawnerOneInXChance       int = 5     // 1/5 chance on daytime tick
	PatrolSpawnerMinOffset          int = 24
	PatrolSpawnerMaxOffset          int = 47 // 24 + nextInt(24) -> 24..47
	PatrolSpawnerVillageCheckChunks int = 2

	// Wandering Trader Spawner (WanderingTraderSpawner.java).
	WanderingTraderSpawnerTickDelay  int = 1200  // 1 minute per tick step
	WanderingTraderSpawnerSpawnDelay int = 24000 // 20 minutes (1 Minecraft day)
	WanderingTraderSpawnerMinChance  int = 25
	WanderingTraderSpawnerMaxChance  int = 75
	WanderingTraderSpawnerChanceStep int = 25
	WanderingTraderSpawnerOneInX     int = 10
	WanderingTraderSpawnSearchRadius int = 48
	WanderingTraderHomeRadius        int = 16
)

var allHarnessItemIDs2612 = [16]string{
	"minecraft:white_harness",
	"minecraft:orange_harness",
	"minecraft:magenta_harness",
	"minecraft:light_blue_harness",
	"minecraft:yellow_harness",
	"minecraft:lime_harness",
	"minecraft:pink_harness",
	"minecraft:gray_harness",
	"minecraft:light_gray_harness",
	"minecraft:cyan_harness",
	"minecraft:purple_harness",
	"minecraft:blue_harness",
	"minecraft:brown_harness",
	"minecraft:green_harness",
	"minecraft:red_harness",
	"minecraft:black_harness",
}

// AllHarnessItemIDs2612 returns a copy of all 16 colored harness items in
// `#minecraft:harnesses` (`data/minecraft/tags/item/harnesses.json`).
func AllHarnessItemIDs2612() []string {
	out := make([]string, len(allHarnessItemIDs2612))
	copy(out, allHarnessItemIDs2612[:])
	return out
}

// IsHarnessItem2612 reports whether itemID belongs to `#minecraft:harnesses`.
func IsHarnessItem2612(itemID string) bool {
	norm := canonicalResourceName(itemID)
	for _, h := range allHarnessItemIDs2612 {
		if h == norm {
			return true
		}
	}
	return false
}

// IsHappyGhastFood2612 reports whether itemID belongs to `#minecraft:happy_ghast_food`
// (`data/minecraft/tags/item/happy_ghast_food.json`: `minecraft:snowball`).
func IsHappyGhastFood2612(itemID string) bool {
	return canonicalResourceName(itemID) == "minecraft:snowball"
}

// IsHappyGhastTemptItem2612 reports whether itemID belongs to
// `#minecraft:happy_ghast_tempt_items` (`#minecraft:happy_ghast_food` or `#minecraft:harnesses`).
func IsHappyGhastTemptItem2612(itemID string) bool {
	return IsHappyGhastFood2612(itemID) || IsHarnessItem2612(itemID)
}

// CanHappyGhastBreatheUnderwater reports whether a Happy Ghast can breathe
// underwater (`HappyGhast.canBreatheUnderwater`: true only when `isBaby()`).
func CanHappyGhastBreatheUnderwater(baby bool) bool {
	return baby
}

// CanHappyGhastFallInLove reports whether an adult Happy Ghast can enter love
// mode (`HappyGhast.canFallInLove`: always false; baby ghastlings come from
// hydrated `minecraft:dried_ghast` blocks).
func CanHappyGhastFallInLove() bool {
	return false
}

// HappyGhastRestrictionRadius returns the home restriction radius for a Happy
// Ghast (`32` when baby or wearing a harness in `EquipmentSlot.BODY`, `64` when
// an unharnessed adult).
func HappyGhastRestrictionRadius(baby, harnessed bool) int {
	if baby || harnessed {
		return HappyGhastSmallRestrictionRadius
	}
	return HappyGhastLargeRestrictionRadius
}

// HappyGhastBehaviorState holds the server-side state for a `minecraft:happy_ghast`.
type HappyGhastBehaviorState struct {
	EntityID           int32
	Baby               bool
	AgeTicks           int // < 0 while baby ghastling (-24000..0), >= 0 when adult
	HarnessItem        string
	IsLeashHolder      bool
	StaysStill         bool
	ServerStillTimeout int
	Passengers         []int32 // Up to 4 player entity IDs (index 0 = pilot)
	HomeX              int
	HomeY              int
	HomeZ              int
	HasHome            bool
	HealTickCounter    int
	InPrecipitation    bool
}

// CamelHuskBehaviorState holds the server-side state for a `minecraft:camel_husk`.
type CamelHuskBehaviorState struct {
	EntityID            int32
	Saddled             bool
	Sitting             bool
	Dashing             bool
	PersistenceRequired bool
	Passengers          []int32 // Up to 2 entity IDs (e.g. Husk driver + Parched passenger, or players)
}

// ParchedBehaviorState holds the server-side state for a `minecraft:parched`.
type ParchedBehaviorState struct {
	EntityID          int32
	AttackCooldown    int
	MainHandItem      string
	RidingVehicleID   int32
}

// PatrolSpawnerState holds the server-side state for `PatrolSpawner`.
type PatrolSpawnerState struct {
	NextTick int
}

// WanderingTraderSpawnerState holds the server-side state for `WanderingTraderSpawner`.
type WanderingTraderSpawnerState struct {
	TickDelay   int
	SpawnDelay  int
	SpawnChance int
}

type happyGhastParchedPolishManager struct {
	mu              sync.RWMutex
	happyGhasts     map[int32]*HappyGhastBehaviorState
	camelHusks      map[int32]*CamelHuskBehaviorState
	parcheds        map[int32]*ParchedBehaviorState
	patrolSpawner   PatrolSpawnerState
	traderSpawner   WanderingTraderSpawnerState
}

var happyGhastParchedManagers sync.Map // map[*Server]*happyGhastParchedPolishManager

func (s *Server) happyGhastParchedMgr() *happyGhastParchedPolishManager {
	if s == nil {
		return &happyGhastParchedPolishManager{
			happyGhasts: make(map[int32]*HappyGhastBehaviorState),
			camelHusks:  make(map[int32]*CamelHuskBehaviorState),
			parcheds:    make(map[int32]*ParchedBehaviorState),
			patrolSpawner: PatrolSpawnerState{
				NextTick: PatrolSpawnerBaseDelayTicks,
			},
			traderSpawner: WanderingTraderSpawnerState{
				TickDelay:   WanderingTraderSpawnerTickDelay,
				SpawnDelay:  WanderingTraderSpawnerSpawnDelay,
				SpawnChance: WanderingTraderSpawnerMinChance,
			},
		}
	}
	if v, ok := happyGhastParchedManagers.Load(s); ok {
		return v.(*happyGhastParchedPolishManager)
	}
	mgr := &happyGhastParchedPolishManager{
		happyGhasts: make(map[int32]*HappyGhastBehaviorState),
		camelHusks:  make(map[int32]*CamelHuskBehaviorState),
		parcheds:    make(map[int32]*ParchedBehaviorState),
		patrolSpawner: PatrolSpawnerState{
			NextTick: PatrolSpawnerBaseDelayTicks,
		},
		traderSpawner: WanderingTraderSpawnerState{
			TickDelay:   WanderingTraderSpawnerTickDelay,
			SpawnDelay:  WanderingTraderSpawnerSpawnDelay,
			SpawnChance: WanderingTraderSpawnerMinChance,
		},
	}
	actual, _ := happyGhastParchedManagers.LoadOrStore(s, mgr)
	return actual.(*happyGhastParchedPolishManager)
}

// EncodeHappyGhastEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// for a `minecraft:happy_ghast` updating AgeableMob.DATA_BABY_ID (index 16),
// HappyGhast.IS_LEASH_HOLDER (index 17), and HappyGhast.STAYS_STILL (index 18).
func EncodeHappyGhastEntityDataPacket(entityID int32, baby, isLeashHolder, staysStill bool) []byte {
	w := protocol.NewWriter(24)
	w.VarInt(entityID)

	w.Byte(EntityDataHappyGhastBabyID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(baby)

	w.Byte(EntityDataHappyGhastIsLeashHolderID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(isLeashHolder)

	w.Byte(EntityDataHappyGhastStaysStillID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(staysStill)

	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeHappyGhastEntityDataPacket decodes a packet produced by EncodeHappyGhastEntityDataPacket.
func DecodeHappyGhastEntityDataPacket(r *protocol.Reader) (entityID int32, baby, isLeashHolder, staysStill bool, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, false, false, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, false, false, false, err
		}
		if idx == 0xFF {
			break
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, false, false, false, err
		}
		if ser != EntityDataSerializerBool {
			return 0, false, false, false, errors.New("happy_ghast: expected BOOLEAN serializer")
		}
		val, err := r.Bool()
		if err != nil {
			return 0, false, false, false, err
		}
		switch idx {
		case EntityDataHappyGhastBabyID:
			baby = val
		case EntityDataHappyGhastIsLeashHolderID:
			isLeashHolder = val
		case EntityDataHappyGhastStaysStillID:
			staysStill = val
		default:
			return 0, false, false, false, errors.New("happy_ghast: unexpected metadata index")
		}
	}
	return entityID, baby, isLeashHolder, staysStill, nil
}

// SpawnHappyGhast spawns a `minecraft:happy_ghast` (or baby ghastling when
// `baby == true`) at (x, y, z) in dimension.
func (s *Server) SpawnHappyGhast(dimension string, x, y, z float64, baby bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil server or entity tracker")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeHappyGhast)
	if typeID < 0 {
		typeID = EntityTypeHappyGhastID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeHappyGhast,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  false,
	})
	s.SetMobHealth(id, DefaultHappyGhastMaxHealth)

	age := 0
	if baby {
		age = BabyMobGrowthTicks
		s.SetMobAge(id, BabyMobGrowthTicks)
	}
	mgr := s.happyGhastParchedMgr()
	mgr.mu.Lock()
	mgr.happyGhasts[id] = &HappyGhastBehaviorState{
		EntityID: id,
		Baby:     baby,
		AgeTicks: age,
		HomeX:    int(math.Floor(x)),
		HomeY:    int(math.Floor(y)),
		HomeZ:    int(math.Floor(z)),
		HasHome:  true,
	}
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeHappyGhastEntityDataPacket(id, baby, false, false))
	}
	return id, nil
}

// GetHappyGhastState returns a snapshot of the HappyGhastBehaviorState for entityID.
func (s *Server) GetHappyGhastState(entityID int32) (HappyGhastBehaviorState, bool) {
	if s == nil || entityID <= 0 {
		return HappyGhastBehaviorState{}, false
	}
	mgr := s.happyGhastParchedMgr()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	st, ok := mgr.happyGhasts[entityID]
	if !ok || st == nil {
		return HappyGhastBehaviorState{}, false
	}
	out := *st
	if len(st.Passengers) > 0 {
		out.Passengers = append([]int32(nil), st.Passengers...)
	}
	return out, true
}

// SetHappyGhastPrecipitation sets whether Happy Ghast entityID is currently
// exposed to rain or snow precipitation (enabling fast 20-tick healing).
func (s *Server) SetHappyGhastPrecipitation(entityID int32, inPrecipitation bool) {
	if s == nil || entityID <= 0 {
		return
	}
	mgr := s.happyGhastParchedMgr()
	mgr.mu.Lock()
	if st, ok := mgr.happyGhasts[entityID]; ok && st != nil {
		st.InPrecipitation = inPrecipitation
	}
	mgr.mu.Unlock()
}

// SetHappyGhastLeashHolder updates the `IS_LEASH_HOLDER` (index 17) state for
// Happy Ghast entityID and broadcasts its updated entity metadata.
func (s *Server) SetHappyGhastLeashHolder(entityID int32, isHolder bool) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeHappyGhast {
		return false
	}
	mgr := s.happyGhastParchedMgr()
	mgr.mu.Lock()
	st, ok := mgr.happyGhasts[entityID]
	if !ok || st == nil {
		mgr.mu.Unlock()
		return false
	}
	st.IsLeashHolder = isHolder
	baby := st.Baby
	staysStill := st.StaysStill
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeHappyGhastEntityDataPacket(entityID, baby, isHolder, staysStill))
	return true
}

// TickDriedGhastHydration2612 executes a scheduled `minecraft:dried_ghast`
// hydration tick (`DriedGhastBlock.tick` / `tickWaterlogged`, interval 5000 ticks):
//   - When `waterlogged == true` and `currentHydration < 3`: increments hydration
//     level by 1 and returns `SoundDriedGhastTransition`.
//   - When `waterlogged == true` and `currentHydration >= 3` (`isReadyToSpawn`):
//     removes the block at (bx, by, bz), spawns a baby `minecraft:happy_ghast`
//     (ghastling) at `(bx+0.5, by, bz+0.5)`, and returns `SoundGhastlingSpawn`.
//   - When `waterlogged == false` and `currentHydration > 0`: decrements hydration
//     level by 1.
func (s *Server) TickDriedGhastHydration2612(
	dimension string,
	bx, by, bz int,
	waterlogged bool,
	currentHydration int,
	facing string,
) (nextHydration int, spawnedGhastlingID int32, soundEvent string) {
	if currentHydration < 0 {
		currentHydration = 0
	}
	if currentHydration > DriedGhastMaxHydrationLevel {
		currentHydration = DriedGhastMaxHydrationLevel
	}
	normDim := world.NormalizeDimension(dimension)
	if !waterlogged {
		if currentHydration > 0 {
			return currentHydration - 1, 0, ""
		}
		return 0, 0, ""
	}
	if currentHydration < DriedGhastMaxHydrationLevel {
		nextHydration = currentHydration + 1
		if s != nil {
			s.BroadcastDimensionChunk(normDim, int32(bx>>4), int32(bz>>4), PlaySoundPacketID,
				EncodeSoundPacket(SoundDriedGhastTransition, SoundSourceBlocks, float64(bx)+0.5, float64(by)+0.5, float64(bz)+0.5, 1.0, 1.0, 0))
		}
		return nextHydration, 0, SoundDriedGhastTransition
	}

	// Ready to spawn a baby Happy Ghast (ghastling)!
	if s != nil {
		if cache := s.DimensionChunks(normDim); cache != nil {
			cache.SetBlock(bx, by, bz, world.StateAir)
		}
		id, err := s.SpawnHappyGhast(normDim, float64(bx)+0.5, float64(by), float64(bz)+0.5, true)
		if err == nil && id > 0 {
			spawnedGhastlingID = id
			yaw := directionFacingYaw(facing)
			s.entities.Update(id, func(e *world.Entity) {
				e.Yaw = yaw
				e.HeadYaw = yaw
			})
			s.BroadcastDimensionChunk(normDim, int32(bx>>4), int32(bz>>4), PlaySoundPacketID,
				EncodeSoundPacket(SoundGhastlingSpawn, SoundSourceBlocks, float64(bx)+0.5, float64(by)+0.5, float64(bz)+0.5, 1.0, 1.0, 0))
		}
	}
	return 0, spawnedGhastlingID, SoundGhastlingSpawn
}

func directionFacingYaw(facing string) float32 {
	switch strings.ToLower(strings.TrimSpace(facing)) {
	case "south":
		return 0
	case "west":
		return 90
	case "north":
		return 180
	case "east":
		return 270
	default:
		return 0
	}
}

// InteractWithHappyGhast handles player right-click interactions with a
// `minecraft:happy_ghast` in vanilla 26.1.2:
//   - Baby ghastling: feeding `minecraft:snowball` (`#minecraft:happy_ghast_food`)
//     accelerates growth by 10% of remaining baby ticks; grows into an adult at >= 0.
//   - Adult Happy Ghast:
//     1. Holding a harness (`#minecraft:harnesses`) when unharnessed equips the
//        harness (`SoundHappyGhastEquip`, action `"equip_harness"`).
//     2. Holding `minecraft:shears` when harnessed and unmounted (`len(Passengers) == 0`)
//        shears off the harness (`SoundHappyGhastUnequip`, action `"shear_harness"`).
//     3. Interacting when harnessed and `len(Passengers) < 4` mounts the player
//        onto the Happy Ghast (`HappyGhastMaxPassengers = 4`, playing
//        `SoundHappyGhastHarnessGogglesDown` when the first passenger mounts,
//        action `"mount"`).
func (s *Server) InteractWithHappyGhast(session *PlayerSession, entityID int32) (handled bool, action string) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, ""
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeHappyGhast {
		return false, ""
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false, ""
	}
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	held := session.HeldItem()
	heldName := canonicalResourceName(held.ResolvedID())

	mgr := s.happyGhastParchedMgr()
	mgr.mu.Lock()
	st, ok := mgr.happyGhasts[entityID]
	if !ok || st == nil {
		st = &HappyGhastBehaviorState{
			EntityID: entityID,
			Baby:     s.IsMobBaby(entityID),
		}
		mgr.happyGhasts[entityID] = st
	}

	// 1. Baby ghastling: feed snowball (#minecraft:happy_ghast_food).
	if st.Baby {
		if !IsHappyGhastFood2612(heldName) {
			mgr.mu.Unlock()
			return false, ""
		}
		remaining := -st.AgeTicks
		if remaining <= 0 {
			remaining = -BabyMobGrowthTicks
		}
		boost := remaining / 10
		if boost < 200 {
			boost = 200
		}
		st.AgeTicks += boost
		grewUp := false
		if st.AgeTicks >= 0 {
			st.AgeTicks = 0
			st.Baby = false
			grewUp = true
		}
		newAge := st.AgeTicks
		isHolder := st.IsLeashHolder
		staysStill := st.StaysStill
		mgr.mu.Unlock()

		s.SetMobAge(entityID, newAge)
		if snap.GameMode != GameModeCreative {
			slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
			if held.Count > 1 {
				held.Count--
				session.SetInventorySlot(slotIdx, held)
			} else {
				session.SetInventorySlot(slotIdx, world.ItemStack{})
			}
		}
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
			EncodeEntityEventPacket(entityID, EntityEventLoveHearts))
		if grewUp {
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
				EncodeHappyGhastEntityDataPacket(entityID, false, isHolder, staysStill))
			return true, "grow_up"
		}
		return true, "feed_baby"
	}

	// 2. Adult Happy Ghast: equip harness (#minecraft:harnesses) when unharnessed.
	if st.HarnessItem == "" && IsHarnessItem2612(heldName) {
		st.HarnessItem = heldName
		mgr.mu.Unlock()

		if snap.GameMode != GameModeCreative {
			slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
			if held.Count > 1 {
				held.Count--
				session.SetInventorySlot(slotIdx, held)
			} else {
				session.SetInventorySlot(slotIdx, world.ItemStack{})
			}
		}
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundHappyGhastEquip, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		return true, "equip_harness"
	}

	// 3. Adult Happy Ghast: shear off harness when unmounted.
	if st.HarnessItem != "" && heldName == "minecraft:shears" && len(st.Passengers) == 0 {
		removedHarness := st.HarnessItem
		st.HarnessItem = ""
		mgr.mu.Unlock()

		s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+1.0, ent.Z, world.NewNamedItemStack(removedHarness, 1))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundHappyGhastUnequip, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		return true, "shear_harness"
	}

	// 4. Adult Happy Ghast: mount passenger (up to HappyGhastMaxPassengers = 4) when harnessed.
	if st.HarnessItem != "" && len(st.Passengers) < HappyGhastMaxPassengers {
		playerEntID := session.EntityID
		for _, pid := range st.Passengers {
			if pid == playerEntID {
				mgr.mu.Unlock()
				return false, ""
			}
		}
		wasUnmounted := len(st.Passengers) == 0
		st.Passengers = append(st.Passengers, playerEntID)
		st.StaysStill = false
		st.ServerStillTimeout = 0
		passengersCopy := append([]int32(nil), st.Passengers...)
		isHolder := st.IsLeashHolder
		mgr.mu.Unlock()

		if wasUnmounted {
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundHappyGhastHarnessGogglesDown, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		}
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetPassengersCB,
			EncodeSetPassengersPacket(entityID, passengersCopy))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeHappyGhastEntityDataPacket(entityID, false, isHolder, false))
		return true, "mount"
	}

	mgr.mu.Unlock()
	return false, ""
}

// DismountFromHappyGhast dismounts session from Happy Ghast entityID, playing
// `SoundHappyGhastHarnessGogglesUp` when the last passenger dismounts.
func (s *Server) DismountFromHappyGhast(session *PlayerSession, entityID int32) bool {
	if s == nil || s.entities == nil || session == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeHappyGhast {
		return false
	}
	playerEntID := session.EntityID
	mgr := s.happyGhastParchedMgr()
	mgr.mu.Lock()
	st, ok := mgr.happyGhasts[entityID]
	if !ok || st == nil {
		mgr.mu.Unlock()
		return false
	}
	idx := -1
	for i, pid := range st.Passengers {
		if pid == playerEntID {
			idx = i
			break
		}
	}
	if idx < 0 {
		mgr.mu.Unlock()
		return false
	}
	st.Passengers = append(st.Passengers[:idx], st.Passengers[idx+1:]...)
	nowEmpty := len(st.Passengers) == 0
	if nowEmpty {
		st.ServerStillTimeout = HappyGhastMaxStillTimeout
		st.StaysStill = true
	}
	passengersCopy := append([]int32(nil), st.Passengers...)
	isHolder := st.IsLeashHolder
	staysStill := st.StaysStill
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	if nowEmpty {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundHappyGhastHarnessGogglesUp, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	}
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetPassengersCB,
		EncodeSetPassengersPacket(entityID, passengersCopy))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeHappyGhastEntityDataPacket(entityID, false, isHolder, staysStill))
	return true
}

// SteerHappyGhastByDriver applies pilot flight inputs (`forward`, `strafe`,
// `vertical` in `[-1..1]`) from the first passenger (`Passengers[0]`) of an
// adult harnessed Happy Ghast, scaling by `HappyGhastRiddenSpeedMultiplier * HappyGhastFlyingSpeed`
// (`3.9 * 0.05 = 0.195` blocks/tick) and moving the Happy Ghast.
func (s *Server) SteerHappyGhastByDriver(entityID int32, driver *PlayerSession, forward, strafe, vertical float64) (vx, vy, vz float64, ok bool) {
	if s == nil || s.entities == nil || driver == nil || entityID <= 0 {
		return 0, 0, 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeHappyGhast {
		return 0, 0, 0, false
	}
	mgr := s.happyGhastParchedMgr()
	mgr.mu.RLock()
	st, exists := mgr.happyGhasts[entityID]
	if !exists || st == nil || st.Baby || st.HarnessItem == "" || len(st.Passengers) == 0 || st.Passengers[0] != driver.EntityID {
		mgr.mu.RUnlock()
		return 0, 0, 0, false
	}
	mgr.mu.RUnlock()

	speed := HappyGhastFlyingSpeed * HappyGhastRiddenSpeedMultiplier
	pSnap := driver.Snapshot()
	yawRad := float64(pSnap.Yaw) * (math.Pi / 180.0)
	sinY := math.Sin(yawRad)
	cosY := math.Cos(yawRad)

	vx = (-sinY*forward + cosY*strafe) * speed
	vy = vertical * speed
	vz = (cosY*forward + sinY*strafe) * speed

	s.entities.Update(entityID, func(e *world.Entity) {
		e.X += vx
		e.Y += vy
		e.Z += vz
		e.Yaw = pSnap.Yaw
		e.HeadYaw = pSnap.Yaw
	})
	return vx, vy, vz, true
}

// ScanPlayerAboveHappyGhast implements `HappyGhast.scanPlayerAboveGhast()`:
// checks whether any non-spectator, non-riding player is standing on or within
// the top detection box of adult Happy Ghast entityID (`Y` in `[maxY, maxY + height/2]`,
// expanded horizontally by `1.0` block around the `4x4` adult bounding box).
func (s *Server) ScanPlayerAboveHappyGhast(entityID int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeHappyGhast {
		return false
	}
	mgr := s.happyGhastParchedMgr()
	mgr.mu.RLock()
	st, ok := mgr.happyGhasts[entityID]
	if !ok || st == nil || st.Baby {
		mgr.mu.RUnlock()
		return false
	}
	passengers := append([]int32(nil), st.Passengers...)
	mgr.mu.RUnlock()

	halfW := HappyGhastAdultWidth/2.0 + 1.0 // 2.0 + 1.0 = 3.0
	minY := ent.Y + HappyGhastAdultHeight
	maxY := minY + HappyGhastAdultHeight/2.0
	dim := ent.EffectiveDimension()

	s.playersMu.RLock()
	defer s.playersMu.RUnlock()
	for _, sess := range s.players {
		if sess == nil || sess.IsDead() || world.NormalizeDimension(sess.Dimension()) != dim {
			continue
		}
		pSnap := sess.Snapshot()
		if pSnap.GameMode == GameModeSpectator || pSnap.Health <= 0 {
			continue
		}
		isPassenger := false
		for _, pid := range passengers {
			if pid == sess.EntityID {
				isPassenger = true
				break
			}
		}
		if isPassenger {
			continue
		}
		if math.Abs(pSnap.X-ent.X) <= halfW && math.Abs(pSnap.Z-ent.Z) <= halfW && pSnap.Y >= minY-0.5 && pSnap.Y <= maxY {
			return true
		}
	}
	return false
}

// TickHappyGhasts steps all active `minecraft:happy_ghast` entities:
//   - Baby ghastling growth (`AgeTicks` increments toward `0`).
//   - `scanPlayerAboveGhast()` setting `ServerStillTimeout = 10` and `StaysStill = true`
//     so players can stand and walk on top of an adult Happy Ghast in mid-air.
//   - `continuousHeal()`: regenerates `+1.0` HP every `20` ticks when in clouds
//     (`192 <= Y <= 196`) or precipitation, or every `600` ticks otherwise.
func (s *Server) TickHappyGhasts(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	mgr := s.happyGhastParchedMgr()
	mgr.mu.RLock()
	if len(mgr.happyGhasts) == 0 {
		mgr.mu.RUnlock()
		return
	}
	ids := make([]int32, 0, len(mgr.happyGhasts))
	for id := range mgr.happyGhasts {
		ids = append(ids, id)
	}
	mgr.mu.RUnlock()

	for _, id := range ids {
		ent := s.entities.Get(id)
		if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeHappyGhast {
			mgr.mu.Lock()
			delete(mgr.happyGhasts, id)
			mgr.mu.Unlock()
			continue
		}
		playerAbove := s.ScanPlayerAboveHappyGhast(id)

		mgr.mu.Lock()
		st, ok := mgr.happyGhasts[id]
		if !ok || st == nil {
			mgr.mu.Unlock()
			continue
		}
		prevBaby := st.Baby
		prevStaysStill := st.StaysStill
		if st.Baby {
			st.AgeTicks++
			if st.AgeTicks >= 0 {
				st.AgeTicks = 0
				st.Baby = false
			}
		}
		if !st.Baby {
			if playerAbove {
				st.ServerStillTimeout = HappyGhastMaxStillTimeout
			} else if st.ServerStillTimeout > 0 {
				st.ServerStillTimeout--
			}
		} else {
			st.ServerStillTimeout = 0
		}
		st.StaysStill = st.ServerStillTimeout > 0
		st.HealTickCounter++
		healInterval := HappyGhastSlowHealingTicks
		inClouds := ent.Y >= HappyGhastCloudsMinY && ent.Y <= HappyGhastCloudsMaxY
		if inClouds || st.InPrecipitation {
			healInterval = HappyGhastFastHealingTicks
		}
		shouldHeal := len(st.Passengers) == 0 && st.HealTickCounter >= healInterval
		if shouldHeal {
			st.HealTickCounter = 0
		}
		curBaby := st.Baby
		curHolder := st.IsLeashHolder
		curStill := st.StaysStill
		mgr.mu.Unlock()

		if shouldHeal {
			hp := s.MobHealth(id)
			if hp > 0 && hp < DefaultHappyGhastMaxHealth {
				newHP := hp + 1.0
				if newHP > DefaultHappyGhastMaxHealth {
					newHP = DefaultHappyGhastMaxHealth
				}
				s.SetMobHealth(id, newHP)
			}
		}
		if curBaby != prevBaby || curStill != prevStaysStill {
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
				EncodeHappyGhastEntityDataPacket(id, curBaby, curHolder, curStill))
		}
	}
}

// RollHappyGhastDeathLoot2612 returns the death drops for a `minecraft:happy_ghast`
// (`data/minecraft/loot_table/entities/happy_ghast.json` has no base pools,
// but any equipped harness in `EquipmentSlot.BODY` is dropped).
func (s *Server) RollHappyGhastDeathLoot2612(entityID int32) []world.ItemStack {
	if s == nil || entityID <= 0 {
		return nil
	}
	st, ok := s.GetHappyGhastState(entityID)
	if !ok || st.HarnessItem == "" {
		return nil
	}
	stack := world.NewNamedItemStack(st.HarnessItem, 1)
	if stack.IsEmpty() {
		return nil
	}
	return []world.ItemStack{stack}
}

// --- 2. Parched (minecraft:parched) & Camel Husk (minecraft:camel_husk) ---

// CanParchedBeAffectedByEffect2612 reports whether a `minecraft:parched` can be
// affected by `effectName` (`Parched.canBeAffected` returns `false` for `MobEffects.WEAKNESS`).
func CanParchedBeAffectedByEffect2612(effectName string) bool {
	return canonicalResourceName(effectName) != EffectWeakness
}

// ParchedAttackIntervalTicks2612 returns the bow attack interval in ticks for a
// `minecraft:parched` (`50` on Hard difficulty, `70` on Normal/Easy).
func ParchedAttackIntervalTicks2612(hardDifficulty bool) int {
	if hardDifficulty {
		return ParchedHardAttackIntervalTicks
	}
	return ParchedNormalAttackIntervalTicks
}

// SpawnParched spawns a `minecraft:parched` (EntityType ID 97, 16.0 HP, armed
// with `minecraft:bow`) at (x, y, z) in dimension.
func (s *Server) SpawnParched(dimension string, x, y, z float64) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil server or entity tracker")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeParched)
	if typeID < 0 {
		typeID = EntityTypeParchedID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeParched,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})
	s.SetMobHealth(id, DefaultParchedMaxHealth)

	mgr := s.happyGhastParchedMgr()
	mgr.mu.Lock()
	mgr.parcheds[id] = &ParchedBehaviorState{
		EntityID:     id,
		MainHandItem: "minecraft:bow",
	}
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
	}
	return id, nil
}

// ShootParchedWeaknessArrow fires a weakness-tipped arrow from `parchedID` at
// `targetPlayer` (or `targetMobID`), applying `600` ticks (30s) of `EffectWeakness`
// (`Parched.getArrow` in 26.1.2) and setting the Parched's attack cooldown to
// `50` ticks (Hard) or `70` ticks (Normal/Easy).
func (s *Server) ShootParchedWeaknessArrow(parchedID int32, targetPlayer *PlayerSession, targetMobID int32, hardDifficulty bool) (cooldownTicks int, appliedWeakness bool) {
	cooldownTicks = ParchedAttackIntervalTicks2612(hardDifficulty)
	if s == nil || s.entities == nil || parchedID <= 0 {
		return cooldownTicks, false
	}
	ent := s.entities.Get(parchedID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeParched {
		return cooldownTicks, false
	}
	mgr := s.happyGhastParchedMgr()
	mgr.mu.Lock()
	if st, ok := mgr.parcheds[parchedID]; ok && st != nil {
		st.AttackCooldown = cooldownTicks
	}
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.skeleton.shoot", SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))

	if targetPlayer != nil && !targetPlayer.IsDead() {
		s.DamagePlayerWithArmor(targetPlayer, ScaleDamageByDifficulty(SkeletonArrowBaseDamage, s.Difficulty()), "arrow")
		s.ApplyPlayerEffect(targetPlayer, NewStatusEffectInstance(EffectWeakness, ParchedWeaknessArrowDurationTicks, 0))
		appliedWeakness = true
	} else if targetMobID > 0 {
		if targetEnt := s.entities.Get(targetMobID); targetEnt != nil {
			if canonicalResourceName(targetEnt.TypeName) != MobTypeParched || CanParchedBeAffectedByEffect2612(EffectWeakness) {
				appliedWeakness = true
			}
		}
	}
	return cooldownTicks, appliedWeakness
}

// RollParchedDeathLoot2612 rolls the exact death loot table for `minecraft:parched`
// verified from `data/minecraft/loot_table/entities/parched.json`:
//   - Pool 1: `0..2` `minecraft:arrow` (+ `0..1` per Looting level)
//   - Pool 2: `0..2` `minecraft:bone` (+ `0..1` per Looting level)
//   - Pool 3 (`killed_by_player`): `0..1` `minecraft:tipped_arrow` (Weakness,
//     capped at `limit = 1` with Looting)
func RollParchedDeathLoot2612(rng *rand.Rand, lootingLevel int, killedByPlayer bool) []world.ItemStack {
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	var out []world.ItemStack

	arrowCount := int32(rng.Intn(3))
	if lootingLevel > 0 {
		arrowCount += int32(rng.Intn(lootingLevel + 1))
	}
	if arrowCount > 0 {
		if st := world.NewNamedItemStack("minecraft:arrow", arrowCount); !st.IsEmpty() {
			out = append(out, st)
		}
	}

	boneCount := int32(rng.Intn(3))
	if lootingLevel > 0 {
		boneCount += int32(rng.Intn(lootingLevel + 1))
	}
	if boneCount > 0 {
		if st := world.NewNamedItemStack("minecraft:bone", boneCount); !st.IsEmpty() {
			out = append(out, st)
		}
	}

	if killedByPlayer {
		tippedCount := int32(rng.Intn(2))
		if lootingLevel > 0 {
			tippedCount += int32(rng.Intn(lootingLevel + 1))
		}
		if tippedCount > 1 {
			tippedCount = 1 // limit: 1 in entities/parched.json
		}
		if tippedCount > 0 {
			if st := world.NewNamedItemStack("minecraft:tipped_arrow", tippedCount); !st.IsEmpty() {
				out = append(out, st)
			}
		}
	}
	return out
}

// IsCamelHuskFood2612 reports whether itemID belongs to `#minecraft:camel_husk_food`
// (`data/minecraft/tags/item/camel_husk_food.json`: `minecraft:rabbit_foot`).
func IsCamelHuskFood2612(itemID string) bool {
	return canonicalResourceName(itemID) == CamelHuskFoodItemID
}

// CanCamelHuskBreed2612 reports whether a `minecraft:camel_husk` can breed or
// enter love mode (`CamelHusk.canMate` / `canFallInLove` / `isBaby` are all `false`).
func CanCamelHuskBreed2612() bool {
	return false
}

// GetCamelHuskState returns a snapshot of the CamelHuskBehaviorState for entityID.
func (s *Server) GetCamelHuskState(entityID int32) (CamelHuskBehaviorState, bool) {
	if s == nil || entityID <= 0 {
		return CamelHuskBehaviorState{}, false
	}
	mgr := s.happyGhastParchedMgr()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	st, ok := mgr.camelHusks[entityID]
	if !ok || st == nil {
		return CamelHuskBehaviorState{}, false
	}
	out := *st
	if len(st.Passengers) > 0 {
		out.Passengers = append([]int32(nil), st.Passengers...)
	}
	return out, true
}

// InteractWithCamelHusk handles player right-click interactions with a
// `minecraft:camel_husk` (`CamelHusk.interact`):
//   - Always sets `PersistenceRequired = true` (`this.setPersistenceRequired()`).
//   - Holding `minecraft:rabbit_foot` (`#minecraft:camel_husk_food`) heals the
//     damaged Camel Husk by `+2.0` HP and plays `SoundCamelHuskEat`.
//   - Holding `minecraft:saddle` when unsaddled equips the saddle (`SoundCamelHuskSaddle`).
//   - Otherwise, if not mob-controlled and `len(Passengers) < 2`, mounts the player.
func (s *Server) InteractWithCamelHusk(session *PlayerSession, entityID int32) (handled bool, action string) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, ""
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCamelHusk {
		return false, ""
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false, ""
	}
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	held := session.HeldItem()
	heldName := canonicalResourceName(held.ResolvedID())

	mgr := s.happyGhastParchedMgr()
	mgr.mu.Lock()
	st, ok := mgr.camelHusks[entityID]
	if !ok || st == nil {
		st = &CamelHuskBehaviorState{EntityID: entityID}
		mgr.camelHusks[entityID] = st
	}
	st.PersistenceRequired = true

	// 1. Feed rabbit_foot (#minecraft:camel_husk_food) to heal.
	if IsCamelHuskFood2612(heldName) {
		mgr.mu.Unlock()
		hp := s.MobHealth(entityID)
		if hp >= DefaultCamelHuskMaxHealth {
			return true, "persistent"
		}
		newHP := hp + CamelHuskHealAmount
		if newHP > DefaultCamelHuskMaxHealth {
			newHP = DefaultCamelHuskMaxHealth
		}
		s.SetMobHealth(entityID, newHP)
		if snap.GameMode != GameModeCreative {
			slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
			if held.Count > 1 {
				held.Count--
				session.SetInventorySlot(slotIdx, held)
			} else {
				session.SetInventorySlot(slotIdx, world.ItemStack{})
			}
		}
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundCamelHuskEat, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		return true, "feed"
	}

	// 2. Equip saddle.
	if !st.Saddled && heldName == SaddleItemID {
		st.Saddled = true
		sitting := st.Sitting
		dashing := st.Dashing
		mgr.mu.Unlock()
		if snap.GameMode != GameModeCreative {
			slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
			if held.Count > 1 {
				held.Count--
				session.SetInventorySlot(slotIdx, held)
			} else {
				session.SetInventorySlot(slotIdx, world.ItemStack{})
			}
		}
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundCamelHuskSaddle, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeCamelEntityDataPacket(entityID, true, sitting, dashing, 0))
		return true, "saddle"
	}

	// 3. Mount player if not mob-controlled and capacity < 2.
	if len(st.Passengers) < MaxCamelPassengers {
		if s.IsCamelHuskMobControlled(entityID) {
			mgr.mu.Unlock()
			return true, "persistent"
		}
		playerEntID := session.EntityID
		st.Passengers = append(st.Passengers, playerEntID)
		passengersCopy := append([]int32(nil), st.Passengers...)
		mgr.mu.Unlock()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetPassengersCB,
			EncodeSetPassengersPacket(entityID, passengersCopy))
		return true, "mount"
	}
	mgr.mu.Unlock()
	return true, "persistent"
}

// TrySpawnHuskCamelJockeyTrio2612 implements the vanilla 26.1.2 `Husk.finalizeSpawn`
// desert jockey trio:
//   - On natural spawn, with a 10% (`0.10`) chance (or always when `force == true`),
//     spawns a saddled `minecraft:camel_husk` ridden by:
//     - Seat 0 (driver): `minecraft:husk` armed with `minecraft:iron_spear`
//     - Seat 1 (back passenger): `minecraft:parched` armed with `minecraft:bow`
func (s *Server) TrySpawnHuskCamelJockeyTrio2612(
	dimension string,
	x, y, z float64,
	rng *rand.Rand,
	force bool,
) (camelHuskID, huskID, parchedID int32, spawned bool) {
	if s == nil || s.entities == nil {
		return 0, 0, 0, false
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	if !force && rng.Float32() >= HuskCamelJockeySpawnChance {
		return 0, 0, 0, false
	}
	normDim := world.NormalizeDimension(dimension)
	camelID, err := s.SpawnCamelHusk(normDim, x, y, z)
	if err != nil || camelID <= 0 {
		return 0, 0, 0, false
	}
	hID, err := s.SpawnHusk(normDim, x, y, z, false)
	if err != nil || hID <= 0 {
		return camelID, 0, 0, false
	}
	// Equip the Husk driver with an iron_spear in MAINHAND (Husk.finalizeSpawn).
	if uSt := s.ensureUndeadState(hID, MobTypeHusk, rng); uSt != nil {
		uSt.MainHand = world.NewNamedItemStack(IronSpearItemID, 1)
	}
	pID, err := s.SpawnParched(normDim, x, y, z)
	if err != nil || pID <= 0 {
		return camelID, hID, 0, false
	}

	s.MountMobOnCamel(camelID, hID)
	s.MountMobOnCamel(camelID, pID)

	mgr := s.happyGhastParchedMgr()
	mgr.mu.Lock()
	mgr.camelHusks[camelID] = &CamelHuskBehaviorState{
		EntityID:   camelID,
		Saddled:    true,
		Passengers: []int32{hID, pID},
	}
	if pSt, ok := mgr.parcheds[pID]; ok && pSt != nil {
		pSt.RidingVehicleID = camelID
	}
	mgr.mu.Unlock()

	bx, bz := int(math.Floor(x)), int(math.Floor(z))
	s.BroadcastDimensionChunk(normDim, int32(bx>>4), int32(bz>>4), PlaySetPassengersCB,
		EncodeSetPassengersPacket(camelID, []int32{hID, pID}))
	return camelID, hID, pID, true
}

// RollCamelHuskDeathLoot2612 rolls the exact death loot table for `minecraft:camel_husk`
// verified from `data/minecraft/loot_table/entities/camel_husk.json`:
//   - `2..3` `minecraft:rotten_flesh` (+ `0..1` per Looting level)
func RollCamelHuskDeathLoot2612(rng *rand.Rand, lootingLevel int) []world.ItemStack {
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	count := int32(2 + rng.Intn(2)) // uniform 2..3
	if lootingLevel > 0 {
		count += int32(rng.Intn(lootingLevel + 1))
	}
	stack := world.NewNamedItemStack("minecraft:rotten_flesh", count)
	if stack.IsEmpty() {
		return nil
	}
	return []world.ItemStack{stack}
}

// --- 3. Pillager Patrol Spawner (PatrolSpawner.java) & Wandering Trader Spawner ---

// SpawnPillagerPatrolAt spawns a Pillager patrol at (x, y, z) in dimension
// matching `PatrolSpawner.tick`:
//   - Group size: `int(math.Ceil(effectiveDifficulty)) + 1` `minecraft:pillager` members
//   - Member 0 is the `PatrolLeader` (`PatrolLeader = true`, wearing an Ominous
//     Banner in the head slot) and given a patrol target offset.
func (s *Server) SpawnPillagerPatrolAt(
	dimension string,
	x, y, z float64,
	effectiveDifficulty float64,
	rng *rand.Rand,
) (leaderID int32, memberIDs []int32, err error) {
	if s == nil || s.entities == nil {
		return 0, nil, errors.New("server: nil server or entity tracker")
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	if effectiveDifficulty < 0 {
		effectiveDifficulty = 0
	}
	count := int(math.Ceil(effectiveDifficulty)) + 1
	if count < 1 {
		count = 1
	}
	normDim := world.NormalizeDimension(dimension)
	memberIDs = make([]int32, 0, count)

	for i := 0; i < count; i++ {
		px, py, pz := x, y, z
		if i > 0 {
			px += float64(rng.Intn(11) - 5)
			pz += float64(rng.Intn(11) - 5)
		}
		isLeader := i == 0
		id, spawnErr := s.SpawnMobInDimension(normDim, px, py, pz, MobTypePillager)
		if spawnErr != nil || id <= 0 {
			continue
		}
		s.ensureIllagerOrRaiderState(id, MobTypePillager, isLeader)
		if isLeader {
			s.MarkRaidCaptain(id, 0)
			leaderID = id
		}
		memberIDs = append(memberIDs, id)
	}
	if len(memberIDs) == 0 {
		return 0, nil, errors.New("patrol: failed to spawn any pillager patrol members")
	}
	return leaderID, memberIDs, nil
}

// TickPillagerPatrolSpawner steps the vanilla 26.1.2 `PatrolSpawner`:
//   - Requires Overworld daytime (`TimeOfDay()%24000 < 12000`) and non-Peaceful difficulty.
//   - Counts down `NextTick`; when `<= 0`, resets `NextTick = 12000 + rng.Intn(1200)`.
//   - Rolls `1/5` chance (`rng.Intn(5) == 0`), picks a non-spectator Overworld
//     player not within 2 chunks (32 blocks) of a village, offsets `±(24..47)`
//     blocks in X and Z, and spawns a Pillager patrol with a Patrol Leader.
func (s *Server) TickPillagerPatrolSpawner(rng *rand.Rand) (spawnedIDs []int32, spawned bool) {
	if s == nil || s.entities == nil || s.Difficulty() == DifficultyPeaceful {
		return nil, false
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	mgr := s.happyGhastParchedMgr()
	mgr.mu.Lock()
	mgr.patrolSpawner.NextTick--
	if mgr.patrolSpawner.NextTick > 0 {
		mgr.mu.Unlock()
		return nil, false
	}
	mgr.patrolSpawner.NextTick = PatrolSpawnerBaseDelayTicks + rng.Intn(PatrolSpawnerRandomDelayTicks)
	mgr.mu.Unlock()

	tod := s.TimeOfDay() % 24000
	if tod < 0 {
		tod += 24000
	}
	if tod >= 12000 {
		return nil, false // Daytime only (isBrightOutside)
	}
	if rng.Intn(PatrolSpawnerOneInXChance) != 0 {
		return nil, false
	}

	var candidates []*PlayerSession
	s.playersMu.RLock()
	for _, sess := range s.players {
		if sess == nil || sess.IsDead() || world.NormalizeDimension(sess.Dimension()) != world.DimensionOverworld {
			continue
		}
		if sess.Snapshot().GameMode == GameModeSpectator {
			continue
		}
		candidates = append(candidates, sess)
	}
	s.playersMu.RUnlock()
	if len(candidates) == 0 {
		return nil, false
	}

	chosen := candidates[rng.Intn(len(candidates))]
	pSnap := chosen.Snapshot()

	// Check not within 2 chunks (32 blocks) of a village (villager presence).
	for _, ent := range s.entities.AppendAll(nil) {
		if ent.TypeName == MobTypeVillager && ent.EffectiveDimension() == world.DimensionOverworld {
			dx := ent.X - pSnap.X
			dz := ent.Z - pSnap.Z
			if dx*dx+dz*dz <= 32.0*32.0 {
				return nil, false
			}
		}
	}

	dx := float64(PatrolSpawnerMinOffset + rng.Intn(24))
	if rng.Intn(2) == 0 {
		dx = -dx
	}
	dz := float64(PatrolSpawnerMinOffset + rng.Intn(24))
	if rng.Intn(2) == 0 {
		dz = -dz
	}
	effDiff := 2.0
	switch s.Difficulty() {
	case DifficultyEasy:
		effDiff = 1.0
	case DifficultyNormal:
		effDiff = 2.0
	case DifficultyHard:
		effDiff = 3.0
	}
	_, ids, err := s.SpawnPillagerPatrolAt(world.DimensionOverworld, pSnap.X+dx, pSnap.Y, pSnap.Z+dz, effDiff, rng)
	if err != nil || len(ids) == 0 {
		return nil, false
	}
	return ids, true
}

// SetPillagerPatrolNextTick sets the remaining tick countdown on the server's
// `PatrolSpawner` (used in tests and custom spawner scheduling).
func (s *Server) SetPillagerPatrolNextTick(ticks int) {
	if s == nil {
		return
	}
	mgr := s.happyGhastParchedMgr()
	mgr.mu.Lock()
	mgr.patrolSpawner.NextTick = ticks
	mgr.mu.Unlock()
}

// StepWanderingTraderSpawner2612 advances the vanilla 26.1.2 `WanderingTraderSpawner`
// by `ticks` ticks:
//   - Every `1200` ticks (`DEFAULT_TICK_DELAY`), decrements `SpawnDelay` by `1200`.
//   - When `SpawnDelay <= 0`, resets `SpawnDelay = 24000`, clamps `SpawnChance`
//     into `25..75` (`+25` per day), rolls `rng.Intn(100) <= oldChance` and
//     `rng.Intn(10) == 0`, and spawns a `minecraft:wandering_trader` with 2
//     leashed `minecraft:trader_llama` escorts (`DespawnDelay = 48000`).
func (s *Server) StepWanderingTraderSpawner2612(rng *rand.Rand, ticks int) (traderID int32, llamaIDs []int32, currentChance int, spawned bool) {
	if s == nil || s.entities == nil || ticks <= 0 {
		return 0, nil, 0, false
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	mgr := s.happyGhastParchedMgr()
	mgr.mu.Lock()
	ts := &mgr.traderSpawner
	ts.TickDelay -= ticks
	if ts.TickDelay > 0 {
		c := ts.SpawnChance
		mgr.mu.Unlock()
		return 0, nil, c, false
	}
	ts.TickDelay = WanderingTraderSpawnerTickDelay
	ts.SpawnDelay -= WanderingTraderSpawnerTickDelay
	if ts.SpawnDelay > 0 {
		c := ts.SpawnChance
		mgr.mu.Unlock()
		return 0, nil, c, false
	}
	ts.SpawnDelay = WanderingTraderSpawnerSpawnDelay
	oldChance := ts.SpawnChance
	nextChance := oldChance + WanderingTraderSpawnerChanceStep
	if nextChance < WanderingTraderSpawnerMinChance {
		nextChance = WanderingTraderSpawnerMinChance
	}
	if nextChance > WanderingTraderSpawnerMaxChance {
		nextChance = WanderingTraderSpawnerMaxChance
	}
	ts.SpawnChance = nextChance
	currentChance = ts.SpawnChance
	mgr.mu.Unlock()

	if rng.Intn(100) > oldChance {
		return 0, nil, currentChance, false
	}
	if rng.Intn(WanderingTraderSpawnerOneInX) != 0 {
		return 0, nil, currentChance, false
	}

	var targetSession *PlayerSession
	s.playersMu.RLock()
	for _, sess := range s.players {
		if sess != nil && !sess.IsDead() && world.NormalizeDimension(sess.Dimension()) == world.DimensionOverworld {
			targetSession = sess
			break
		}
	}
	s.playersMu.RUnlock()
	if targetSession == nil {
		return 0, nil, currentChance, false
	}
	pSnap := targetSession.Snapshot()
	tID, lIDs, err := s.SpawnWanderingTrader(world.DimensionOverworld, pSnap.X+4.0, pSnap.Y, pSnap.Z+4.0, true, rng)
	if err != nil || tID <= 0 {
		return 0, nil, currentChance, false
	}
	mgr.mu.Lock()
	mgr.traderSpawner.SpawnChance = WanderingTraderSpawnerMinChance
	currentChance = mgr.traderSpawner.SpawnChance
	mgr.mu.Unlock()
	return tID, lIDs, currentChance, true
}
