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

// Vanilla 26.1.2 (protocol 775) constants for Wolf, Cat, Ocelot, Parrot, Goat,
// Mooshroom, Sheep, Cow, Chicken, and Pig behaviors.
const (
	// Mooshroom (MushroomCow) entity type and SynchedEntityData constants.
	MobTypeMooshroom              = "minecraft:mooshroom"
	EntityTypeMooshroomID   int   = 86
	DefaultMooshroomMaxHealth     = float32(10.0)
	EntityDataMooshroomTypeID byte = 17 // MushroomCow.DATA_TYPE (INT: 0=red, 1=brown)
	MooshroomVariantRed     int32 = 0
	MooshroomVariantBrown   int32 = 1
	MooshroomMutateChance   int   = 1024 // 1/1024 mutation chance when breeding same-variant parents

	// Pig ItemBasedSteering (DATA_BOOST_TIME) constants.
	EntityDataPigBoostTimeID byte    = 17 // Pig.DATA_BOOST_TIME (INT serializer = 1)
	PigBoostMinTicks         int     = 140
	PigBoostRandomRangeTicks int     = 841 // 140 + nextInt(841) -> 140..980 ticks
	PigRiddenBaseSpeedFactor float64 = 0.225
	PigBoostMaxExtraFactor   float64 = 1.15
	CarrotOnAStickItemID             = "minecraft:carrot_on_a_stick"
	CarrotOnAStickMaxDamage  int32   = 25
	CarrotOnAStickBoostWear  int32   = 7

	// Wolf water shake, begging, tail angle, and llama avoidance constants (Wolf.java).
	EntityEventWolfCancelShake byte    = 56 // EntityEvent.CANCEL_SHAKE_WETNESS
	WolfShakeDurationTicks     int     = 40 // 0.0 -> 2.0 at +0.05/tick
	WolfShakeAnimStep          float32 = 0.05
	WolfShakeParticleThreshold float32 = 0.4
	WolfBegLookupRange         float64 = 8.0
	WolfAvoidLlamaDistance     float64 = 24.0
	WolfAngryTailAngle         float32 = 1.5393804  // 0.49 * pi
	WolfWildTailAngle          float32 = 0.62831855 // 0.20 * pi
	WolfTameBoneItemID                 = "minecraft:bone"
	SoundWolfShake                     = "minecraft:entity.wolf.shake"

	// Wolf sound variants in 26.1.2 (WolfSoundVariants.java).
	WolfSoundVariantClassic = "minecraft:classic"
	WolfSoundVariantPuglin  = "minecraft:puglin"
	WolfSoundVariantSad     = "minecraft:sad"
	WolfSoundVariantAngry   = "minecraft:angry"
	WolfSoundVariantGrumpy  = "minecraft:grumpy"
	WolfSoundVariantBig     = "minecraft:big"
	WolfSoundVariantCute    = "minecraft:cute"

	// Cat sound variants & bed/block goal constants in 26.1.2 (CatSoundVariants.java, CatRelaxOnOwnerGoal.java).
	CatSoundVariantClassic           = "minecraft:classic"
	CatSoundVariantRoyal             = "minecraft:royal"
	CatRelaxOnBedTicksThreshold int  = 40
	CatMorningGiftSleepTimerMin int  = 100
	CatSitOnBlockSearchRadius   int  = 8

	// Ocelot movement speed modifiers and avoidance range (Ocelot.java).
	OcelotCrouchSpeedMod   float64 = 0.6
	OcelotWalkSpeedMod     float64 = 0.8
	OcelotSprintSpeedMod   float64 = 1.33
	OcelotAvoidPlayerRange float64 = 16.0

	// Parrot ShoulderRidingEntity constants (ShoulderRidingEntity.java, LandOnOwnersShoulderGoal.java).
	ParrotRideCooldownTicks       int     = 100
	ParrotDismountFallDistanceMin float64 = 0.5
	ParrotFlapFallMultiplier      float64 = 0.6

	// Goat ramming preparation and horn spawn constants (Goat.java, GoatAi.java).
	GoatRamPrepareTicks             int     = 40
	EntityEventGoatLowerHead        byte    = 58 // EntityEvent.GOAT_LOWER_HEAD
	EntityEventGoatStopLowerHead    byte    = 59 // EntityEvent.GOAT_STOP_LOWERING_HEAD
	GoatLowerHeadMaxTicks           int     = 20
	GoatAdultMissingHornSpawnChance float32 = 0.10

	// Vanilla 16 DyeColor ordinals (0..15).
	DyeColorWhite     byte = 0
	DyeColorOrange    byte = 1
	DyeColorMagenta   byte = 2
	DyeColorLightBlue byte = 3
	DyeColorYellow    byte = 4
	DyeColorLime      byte = 5
	DyeColorPink      byte = 6
	DyeColorGray      byte = 7
	DyeColorLightGray byte = 8
	DyeColorCyan      byte = 9
	DyeColorPurple    byte = 10
	DyeColorBlue      byte = 11
	DyeColorBrown     byte = 12
	DyeColorGreen     byte = 13
	DyeColorRed       byte = 14
	DyeColorBlack     byte = 15

	// Sheep EatBlockGoal constants (EatBlockGoal.java).
	EntityEventEatGrass         byte = 10
	SheepEatGrassAnimationTicks int  = 40
	SheepBabyEatGrassAgeBonus   int  = 1200 // 60 seconds (1200 ticks)
	SheepSpawnClimateTemperate       = "temperate"
	SheepSpawnClimateWarm            = "warm"
	SheepSpawnClimateCold            = "cold"
	SoundSheepDye                    = "minecraft:entity.sheep.dye"

	// Farm animal 26.1.2 temperature variants (CowVariants, ChickenVariants, PigVariants).
	FarmAnimalTempTemperate = "minecraft:temperate"
	FarmAnimalTempWarm      = "minecraft:warm"
	FarmAnimalTempCold      = "minecraft:cold"

	// Cow, Chicken, and Pig sound variants in 26.1.2.
	CowSoundVariantClassic     = "minecraft:classic"
	CowSoundVariantMoody       = "minecraft:moody"
	ChickenSoundVariantClassic = "minecraft:classic"
	ChickenSoundVariantPicky   = "minecraft:picky"
	PigSoundVariantClassic     = "minecraft:classic"
	PigSoundVariantMini        = "minecraft:mini"
	PigSoundVariantBig         = "minecraft:big"

	// Chicken 26.1.2 variant eggs (gameplay/chicken_lay.json) and jockey XP.
	EggItemID            = "minecraft:egg"
	BrownEggItemID       = "minecraft:brown_egg"
	BlueEggItemID        = "minecraft:blue_egg"
	ChickenJockeyXPValue = 10

	// Mooshroom sounds in 26.1.2.
	SoundMooshroomConvert          = "minecraft:entity.mooshroom.convert"
	SoundMooshroomEat              = "minecraft:entity.mooshroom.eat"
	SoundMooshroomMilk             = "minecraft:entity.mooshroom.milk"
	SoundMooshroomMilkSuspiciously = "minecraft:entity.mooshroom.milk_suspiciously"
	SoundMooshroomShear            = "minecraft:entity.mooshroom.shear"
	SoundPigSaddle                 = "minecraft:entity.pig.saddle"
)

var (
	allWolfSoundVariants = []string{
		WolfSoundVariantClassic,
		WolfSoundVariantPuglin,
		WolfSoundVariantSad,
		WolfSoundVariantAngry,
		WolfSoundVariantGrumpy,
		WolfSoundVariantBig,
		WolfSoundVariantCute,
	}

	allCatSoundVariants = []string{
		CatSoundVariantClassic,
		CatSoundVariantRoyal,
	}

	allFarmAnimalTempVariants = []string{
		FarmAnimalTempTemperate,
		FarmAnimalTempWarm,
		FarmAnimalTempCold,
	}

	allCowSoundVariants = []string{
		CowSoundVariantClassic,
		CowSoundVariantMoody,
	}

	allChickenSoundVariants = []string{
		ChickenSoundVariantClassic,
		ChickenSoundVariantPicky,
	}

	allPigSoundVariants = []string{
		PigSoundVariantClassic,
		PigSoundVariantMini,
		PigSoundVariantBig,
	}

	// Exact 42 entries from Parrot.MOB_SOUND_MAP in Minecraft 26.1.2.
	parrotMobSoundMap2612 = map[string]string{
		"minecraft:blaze":           "minecraft:entity.parrot.imitate.blaze",
		"minecraft:bogged":          "minecraft:entity.parrot.imitate.bogged",
		"minecraft:breeze":          "minecraft:entity.parrot.imitate.breeze",
		"minecraft:camel_husk":      "minecraft:entity.parrot.imitate.camel_husk",
		"minecraft:cave_spider":     "minecraft:entity.parrot.imitate.spider",
		"minecraft:creaking":        "minecraft:entity.parrot.imitate.creaking",
		"minecraft:creeper":         "minecraft:entity.parrot.imitate.creeper",
		"minecraft:drowned":         "minecraft:entity.parrot.imitate.drowned",
		"minecraft:elder_guardian":  "minecraft:entity.parrot.imitate.elder_guardian",
		"minecraft:ender_dragon":    "minecraft:entity.parrot.imitate.ender_dragon",
		"minecraft:endermite":       "minecraft:entity.parrot.imitate.endermite",
		"minecraft:evoker":          "minecraft:entity.parrot.imitate.evoker",
		"minecraft:ghast":           "minecraft:entity.parrot.imitate.ghast",
		"minecraft:happy_ghast":     "minecraft:empty",
		"minecraft:guardian":        "minecraft:entity.parrot.imitate.guardian",
		"minecraft:hoglin":          "minecraft:entity.parrot.imitate.hoglin",
		"minecraft:husk":            "minecraft:entity.parrot.imitate.husk",
		"minecraft:illusioner":      "minecraft:entity.parrot.imitate.illusioner",
		"minecraft:magma_cube":      "minecraft:entity.parrot.imitate.magma_cube",
		"minecraft:parched":         "minecraft:entity.parrot.imitate.parched",
		"minecraft:phantom":         "minecraft:entity.parrot.imitate.phantom",
		"minecraft:piglin":          "minecraft:entity.parrot.imitate.piglin",
		"minecraft:piglin_brute":    "minecraft:entity.parrot.imitate.piglin_brute",
		"minecraft:pillager":        "minecraft:entity.parrot.imitate.pillager",
		"minecraft:ravager":         "minecraft:entity.parrot.imitate.ravager",
		"minecraft:shulker":         "minecraft:entity.parrot.imitate.shulker",
		"minecraft:silverfish":      "minecraft:entity.parrot.imitate.silverfish",
		"minecraft:skeleton":        "minecraft:entity.parrot.imitate.skeleton",
		"minecraft:slime":           "minecraft:entity.parrot.imitate.slime",
		"minecraft:spider":          "minecraft:entity.parrot.imitate.spider",
		"minecraft:stray":           "minecraft:entity.parrot.imitate.stray",
		"minecraft:vex":             "minecraft:entity.parrot.imitate.vex",
		"minecraft:vindicator":      "minecraft:entity.parrot.imitate.vindicator",
		"minecraft:warden":          "minecraft:entity.parrot.imitate.warden",
		"minecraft:witch":           "minecraft:entity.parrot.imitate.witch",
		"minecraft:wither":          "minecraft:entity.parrot.imitate.wither",
		"minecraft:wither_skeleton": "minecraft:entity.parrot.imitate.wither_skeleton",
		"minecraft:zoglin":          "minecraft:entity.parrot.imitate.zoglin",
		"minecraft:zombie":          "minecraft:entity.parrot.imitate.zombie",
		"minecraft:zombie_horse":    "minecraft:entity.parrot.imitate.zombie_horse",
		"minecraft:zombie_nautilus": "minecraft:entity.parrot.imitate.zombie_nautilus",
		"minecraft:zombie_villager": "minecraft:entity.parrot.imitate.zombie_villager",
	}

	sheepWoolByColor = [16]string{
		"minecraft:white_wool",
		"minecraft:orange_wool",
		"minecraft:magenta_wool",
		"minecraft:light_blue_wool",
		"minecraft:yellow_wool",
		"minecraft:lime_wool",
		"minecraft:pink_wool",
		"minecraft:gray_wool",
		"minecraft:light_gray_wool",
		"minecraft:cyan_wool",
		"minecraft:purple_wool",
		"minecraft:blue_wool",
		"minecraft:brown_wool",
		"minecraft:green_wool",
		"minecraft:red_wool",
		"minecraft:black_wool",
	}
)

type wolfPolishState struct {
	soundVariant     string
	isWet            bool
	isShaking        bool
	shakeAnim        float32
	shakeAnimO       float32
	interested       bool
	interestedAngle  float32
	interestedAngleO float32
}

type catPolishState struct {
	soundVariant string
	onBedTicks   int
	sittingBlock [3]int
	onBlock      bool
}

type parrotPolishState struct {
	rideCooldownTicks int
}

type goatPolishState struct {
	preparingRam  bool
	prepareTicks  int
	lowerHeadTick int
}

type mooshroomState struct {
	EntityID       int32
	Variant        int32 // 0 = red, 1 = brown
	StewEffectID   string
	StewDuration   int32
	LastBoltUUID   [16]byte
	HasLastBolt    bool
}

type sheepPolishState struct {
	color         byte // 0..15 DyeColor
	eatGrassTicks int
}

type farmVariantState struct {
	tempVariant   string // minecraft:temperate, minecraft:warm, minecraft:cold
	soundVariant  string
	chickenJockey bool
	pigSaddled    bool
	pigBoostTime  int
	pigBoostTotal int
}

type farmMobPolishManager struct {
	mu          sync.RWMutex
	wolves      map[int32]*wolfPolishState
	cats        map[int32]*catPolishState
	parrots     map[int32]*parrotPolishState
	goats       map[int32]*goatPolishState
	mooshrooms  map[int32]*mooshroomState
	sheep       map[int32]*sheepPolishState
	farmAnimals map[int32]*farmVariantState
}

var serverFarmPolishManagers sync.Map // map[*Server]*farmMobPolishManager

func (s *Server) farmPolish() *farmMobPolishManager {
	if v, ok := serverFarmPolishManagers.Load(s); ok {
		return v.(*farmMobPolishManager)
	}
	actual, _ := serverFarmPolishManagers.LoadOrStore(s, &farmMobPolishManager{
		wolves:      make(map[int32]*wolfPolishState),
		cats:        make(map[int32]*catPolishState),
		parrots:     make(map[int32]*parrotPolishState),
		goats:       make(map[int32]*goatPolishState),
		mooshrooms:  make(map[int32]*mooshroomState),
		sheep:       make(map[int32]*sheepPolishState),
		farmAnimals: make(map[int32]*farmVariantState),
	})
	return actual.(*farmMobPolishManager)
}

func (s *Server) ensureWolfPolishState(entityID int32) *wolfPolishState {
	fm := s.farmPolish()
	fm.mu.Lock()
	defer fm.mu.Unlock()
	st := fm.wolves[entityID]
	if st == nil {
		st = &wolfPolishState{soundVariant: WolfSoundVariantClassic}
		fm.wolves[entityID] = st
	}
	return st
}

func (s *Server) ensureCatPolishState(entityID int32) *catPolishState {
	fm := s.farmPolish()
	fm.mu.Lock()
	defer fm.mu.Unlock()
	st := fm.cats[entityID]
	if st == nil {
		st = &catPolishState{soundVariant: CatSoundVariantClassic}
		fm.cats[entityID] = st
	}
	return st
}

func (s *Server) ensureParrotPolishState(entityID int32) *parrotPolishState {
	fm := s.farmPolish()
	fm.mu.Lock()
	defer fm.mu.Unlock()
	st := fm.parrots[entityID]
	if st == nil {
		st = &parrotPolishState{rideCooldownTicks: ParrotRideCooldownTicks + 1}
		fm.parrots[entityID] = st
	}
	return st
}

func (s *Server) ensureGoatPolishState(entityID int32) *goatPolishState {
	fm := s.farmPolish()
	fm.mu.Lock()
	defer fm.mu.Unlock()
	st := fm.goats[entityID]
	if st == nil {
		st = &goatPolishState{}
		fm.goats[entityID] = st
	}
	return st
}

func (s *Server) ensureSheepPolishState(entityID int32) *sheepPolishState {
	fm := s.farmPolish()
	fm.mu.Lock()
	defer fm.mu.Unlock()
	st := fm.sheep[entityID]
	if st == nil {
		st = &sheepPolishState{color: DyeColorWhite}
		fm.sheep[entityID] = st
	}
	return st
}

func (s *Server) ensureFarmVariantState(entityID int32, mobType string) *farmVariantState {
	fm := s.farmPolish()
	fm.mu.Lock()
	defer fm.mu.Unlock()
	st := fm.farmAnimals[entityID]
	if st == nil {
		st = &farmVariantState{
			tempVariant:  FarmAnimalTempTemperate,
			soundVariant: CowSoundVariantClassic,
		}
		fm.farmAnimals[entityID] = st
	}
	return st
}

// ============================================================================
// 1. WOLF: Sound Variants, Water Shaking, Begging, Tail Angle, Prey & Llamas
// ============================================================================

// AllWolfSoundVariants returns a copy of all 7 vanilla 26.1.2 `WolfSoundVariants`.
func AllWolfSoundVariants() []string {
	out := make([]string, len(allWolfSoundVariants))
	copy(out, allWolfSoundVariants)
	return out
}

// WolfSoundVariantRegistryIndex returns the registry index (0..6) for a
// `minecraft:wolf_sound_variant` identifier.
func WolfSoundVariantRegistryIndex(variant string) int32 {
	norm := canonicalResourceName(variant)
	for i, v := range allWolfSoundVariants {
		if v == norm {
			return int32(i)
		}
	}
	return 0
}

// SelectRandomWolfSoundVariant picks one of the 7 vanilla 26.1.2 wolf sound
// variants with uniform probability (`WolfSoundVariants.pickRandomSoundVariant`).
func SelectRandomWolfSoundVariant(rng *rand.Rand) string {
	if rng == nil {
		return WolfSoundVariantClassic
	}
	return allWolfSoundVariants[rng.Intn(len(allWolfSoundVariants))]
}

// WolfSoundVariant returns the wolf sound variant for entityID.
func (s *Server) WolfSoundVariant(entityID int32) string {
	if s == nil || entityID <= 0 {
		return WolfSoundVariantClassic
	}
	st := s.ensureWolfPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.RLock()
	v := st.soundVariant
	fm.mu.RUnlock()
	return v
}

// SetWolfSoundVariant sets the sound variant for a wolf entity.
func (s *Server) SetWolfSoundVariant(entityID int32, variant string) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	norm := canonicalResourceName(variant)
	valid := false
	for _, v := range allWolfSoundVariants {
		if v == norm {
			valid = true
			break
		}
	}
	if !valid {
		norm = WolfSoundVariantClassic
	}
	st := s.ensureWolfPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.Lock()
	st.soundVariant = norm
	fm.mu.Unlock()
	return true
}

// SetWolfWet sets whether the wolf with entityID is wet (from water or rain).
// If set to true while shaking, it cancels the current shake animation.
func (s *Server) SetWolfWet(entityID int32, wet bool) {
	if s == nil || entityID <= 0 {
		return
	}
	st := s.ensureWolfPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.Lock()
	st.isWet = wet
	wasShaking := st.isShaking
	if wet && st.isShaking {
		st.isShaking = false
		st.shakeAnim = 0
		st.shakeAnimO = 0
	}
	fm.mu.Unlock()
	if wet && wasShaking && s.entities != nil {
		if ent := s.entities.Get(entityID); ent != nil {
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayEntityEvent,
				EncodeEntityEventPacket(entityID, EntityEventWolfCancelShake))
		}
	}
}

// IsWolfWet reports whether the wolf with entityID is currently wet.
func (s *Server) IsWolfWet(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	st := s.ensureWolfPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.RLock()
	wet := st.isWet
	fm.mu.RUnlock()
	return wet
}

// IsWolfShaking reports whether the wolf with entityID is currently shaking water off.
func (s *Server) IsWolfShaking(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	st := s.ensureWolfPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.RLock()
	shaking := st.isShaking
	fm.mu.RUnlock()
	return shaking
}

// WolfShakeAnim returns the current water-shake progress (0.0..2.0) for entityID.
func (s *Server) WolfShakeAnim(entityID int32) float32 {
	if s == nil || entityID <= 0 {
		return 0
	}
	st := s.ensureWolfPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.RLock()
	a := st.shakeAnim
	fm.mu.RUnlock()
	return a
}

// WolfWetShade computes `Wolf.getWetShade(partialTick)` in vanilla 26.1.2:
// `1.0` when dry, or `min(0.75 + lerp(partialTick, shakeAnimO, shakeAnim) / 2.0 * 0.25, 1.0)` when wet.
func (s *Server) WolfWetShade(entityID int32, partialTick float32) float32 {
	if s == nil || entityID <= 0 {
		return 1.0
	}
	st := s.ensureWolfPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.RLock()
	wet := st.isWet
	animO := st.shakeAnimO
	anim := st.shakeAnim
	fm.mu.RUnlock()
	if !wet {
		return 1.0
	}
	lerp := animO + (anim-animO)*partialTick
	shade := 0.75 + (lerp/2.0)*0.25
	if shade > 1.0 {
		shade = 1.0
	}
	return shade
}

// WolfBodyRollAngle computes `Wolf.getBodyRollAngle(partialTick, offset)` in vanilla 26.1.2.
func (s *Server) WolfBodyRollAngle(entityID int32, partialTick, offset float32) float32 {
	if s == nil || entityID <= 0 {
		return 0
	}
	st := s.ensureWolfPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.RLock()
	animO := st.shakeAnimO
	anim := st.shakeAnim
	fm.mu.RUnlock()
	f := (animO + (anim-animO)*partialTick + offset) / 1.8
	if f < 0.0 {
		f = 0.0
	} else if f > 1.0 {
		f = 1.0
	}
	return float32(math.Sin(float64(f*math.Pi)) * math.Sin(float64(f*math.Pi*11.0)) * 0.15 * math.Pi)
}

// CancelWolfShake stops an active wolf water-shake animation and broadcasts
// `EntityEventWolfCancelShake` (56).
func (s *Server) CancelWolfShake(entityID int32) {
	if s == nil || entityID <= 0 {
		return
	}
	st := s.ensureWolfPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.Lock()
	wasShaking := st.isShaking
	st.isShaking = false
	st.shakeAnim = 0
	st.shakeAnimO = 0
	fm.mu.Unlock()
	if wasShaking && s.entities != nil {
		if ent := s.entities.Get(entityID); ent != nil {
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayEntityEvent,
				EncodeEntityEventPacket(entityID, EntityEventWolfCancelShake))
		}
	}
}

// TickWolfWaterShake advances the water-shake state machine for a wolf by one
// tick (`Wolf.tick` / `Wolf.aiStep` in vanilla 26.1.2):
//   - If the wolf is in water/rain, marks `isWet = true` and cancels any active shake.
//   - If `isWet && !isShaking && onGround` (out of water), starts shaking (`isShaking = true`,
//     broadcasts `EntityEventWolfShakeWater = 8`).
//   - Advances `shakeAnim` by `+0.05` per tick; at `shakeAnim == 0` plays `entity.wolf.shake`;
//     when `shakeAnim >= 2.0` (40 ticks), clears `isWet` and `isShaking`.
func (s *Server) TickWolfWaterShake(entityID int32) (shaking, finished bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeWolf {
		return false, false
	}
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()

	_, _, _, inFluid := s.EntitySubmergedFluidDepth(dim, ent.X, ent.Y, ent.Z)
	if inFluid {
		s.SetWolfWet(entityID, true)
		return false, false
	}

	st := s.ensureWolfPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.Lock()
	if st.isWet && !st.isShaking && ent.OnGround {
		st.isShaking = true
		st.shakeAnim = 0.0
		st.shakeAnimO = 0.0
		fm.mu.Unlock()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
			EncodeEntityEventPacket(entityID, EntityEventWolfShakeWater))
		fm.mu.Lock()
	}

	if !st.isShaking {
		fm.mu.Unlock()
		return false, false
	}

	playSound := st.shakeAnim == 0.0
	st.shakeAnimO = st.shakeAnim
	st.shakeAnim += WolfShakeAnimStep
	if st.shakeAnimO >= 2.0 {
		st.isWet = false
		st.isShaking = false
		st.shakeAnimO = 0.0
		st.shakeAnim = 0.0
		fm.mu.Unlock()
		return false, true
	}
	fm.mu.Unlock()

	if playSound {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundWolfShake, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	}
	return true, false
}

// IsWolfBeggingItem reports whether a wolf will beg (`BegGoal.playerHoldingInteresting`)
// when a player holds `itemName`:
//   - Always begs for `minecraft:bone` (unless angry).
//   - If tamed, also begs for any `#minecraft:wolf_food` meat item.
func IsWolfBeggingItem(tamed bool, itemName string) bool {
	norm := canonicalResourceName(itemName)
	if norm == WolfTameBoneItemID {
		return true
	}
	if tamed && IsWolfMeatItem(norm) {
		return true
	}
	return false
}

// SetWolfInterested sets `Wolf.DATA_INTERESTED_ID` (index 19, begging head tilt)
// for a wolf entity and broadcasts the updated entity data packet.
func (s *Server) SetWolfInterested(entityID int32, interested bool) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeWolf {
		return false
	}
	st := s.ensureWolfPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.Lock()
	st.interested = interested
	st.interestedAngleO = st.interestedAngle
	if interested {
		st.interestedAngle += (1.0 - st.interestedAngle) * 0.4
	} else {
		st.interestedAngle += (0.0 - st.interestedAngle) * 0.4
	}
	fm.mu.Unlock()

	tst, _ := s.GetTamedMobState(entityID)
	hp := s.MobHealth(entityID)
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeWolfEntityDataPacket(entityID, hp, tst.Tamed, tst.Sitting, tst.CollarColor, int32(tst.AngerTicks)))
	return true
}

// IsWolfInterested reports whether a wolf is currently begging (`DATA_INTERESTED_ID`).
func (s *Server) IsWolfInterested(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	st := s.ensureWolfPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.RLock()
	v := st.interested
	fm.mu.RUnlock()
	return v
}

// WolfHeadRollAngle returns `Wolf.getHeadRollAngle(partialTick)` (`interestedAngle * 0.15 * pi`).
func (s *Server) WolfHeadRollAngle(entityID int32) float32 {
	if s == nil || entityID <= 0 {
		return 0
	}
	st := s.ensureWolfPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.RLock()
	a := st.interestedAngle
	fm.mu.RUnlock()
	return a * 0.15 * math.Pi
}

// UpdateWolfBeggingFromNearbyPlayers checks if any player within `WolfBegLookupRange`
// (8.0 blocks) is holding a bone or (for tamed wolves) wolf food, and updates
// `SetWolfInterested`.
func (s *Server) UpdateWolfBeggingFromNearbyPlayers(entityID int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeWolf {
		return false
	}
	tst, _ := s.GetTamedMobState(entityID)
	if tst.AngerTicks > 0 {
		s.SetWolfInterested(entityID, false)
		return false
	}
	dim := ent.EffectiveDimension()
	maxDistSq := WolfBegLookupRange * WolfBegLookupRange
	interested := false

	s.playersMu.RLock()
	for _, p := range s.players {
		if p == nil || !p.IsJoined() || p.IsDead() {
			continue
		}
		pSnap := p.Snapshot()
		if world.NormalizeDimension(pSnap.Dimension) != dim || pSnap.GameMode == GameModeSpectator {
			continue
		}
		dx := pSnap.X - ent.X
		dy := pSnap.Y - ent.Y
		dz := pSnap.Z - ent.Z
		if dx*dx+dy*dy+dz*dz > maxDistSq {
			continue
		}
		if IsWolfBeggingItem(tst.Tamed, p.HeldItem().ResolvedID()) || IsWolfBeggingItem(tst.Tamed, p.InventorySlot(45).ResolvedID()) {
			interested = true
			break
		}
	}
	s.playersMu.RUnlock()

	s.SetWolfInterested(entityID, interested)
	return interested
}

// ForgiveWolfAnger clears a wolf's persistent anger timer (`AngerTicks = 0`) and
// angry target (`TargetEntityID = 0`, `TargetPlayer = nil`), matching `NeutralMob.stopBeingAngry()`.
func (s *Server) ForgiveWolfAnger(entityID int32) {
	if s == nil || entityID <= 0 {
		return
	}
	st := s.ensureTamedMobState(entityID, MobTypeWolf)
	if st == nil {
		return
	}
	mgr := s.tamedManager()
	mgr.mu.Lock()
	st.AngerTicks = 0
	st.TargetEntityID = 0
	st.TargetPlayer = nil
	mgr.mu.Unlock()
}

// WolfTailAngle computes `Wolf.getTailAngle()` in vanilla 26.1.2:
//   - Angry (`AngerTicks > 0`): `1.5393804` (`0.49 * pi`)
//   - Tamed: `(0.55 - (maxHealth - health)/maxHealth * 0.4) * pi`
//   - Wild: `0.62831855` (`0.20 * pi`)
func (s *Server) WolfTailAngle(entityID int32) float32 {
	if s == nil || entityID <= 0 {
		return WolfWildTailAngle
	}
	tst, ok := s.GetTamedMobState(entityID)
	if !ok {
		return WolfWildTailAngle
	}
	if tst.AngerTicks > 0 {
		return WolfAngryTailAngle
	}
	if !tst.Tamed {
		return WolfWildTailAngle
	}
	maxHP := DefaultWolfTamedMaxHealth
	curHP := s.MobHealth(entityID)
	if curHP <= 0 || curHP > maxHP {
		curHP = maxHP
	}
	missingFrac := (maxHP - curHP) / maxHP
	return (0.55 - missingFrac*0.4) * math.Pi
}

// WolfWantsToAttack implements `Wolf.wantsToAttack(target, owner)` in vanilla 26.1.2:
//   - Never attacks Creepers, Ghasts, or Armor Stands.
//   - Never attacks another tamed Wolf with the same owner.
//   - Never attacks tamed Cats, Parrots, or tamed Equines/Llamas.
func (s *Server) WolfWantsToAttack(wolfEntityID, targetEntityID int32) bool {
	if s == nil || s.entities == nil || wolfEntityID <= 0 || targetEntityID <= 0 || wolfEntityID == targetEntityID {
		return false
	}
	target := s.entities.Get(targetEntityID)
	if target == nil {
		return false
	}
	targetType := canonicalResourceName(target.TypeName)
	switch targetType {
	case MobTypeCreeper, MobTypeGhast, EntityTypeArmorStand:
		return false
	}
	wolfTst, _ := s.GetTamedMobState(wolfEntityID)
	if targetType == MobTypeWolf {
		otherTst, ok := s.GetTamedMobState(targetEntityID)
		if ok && otherTst.Tamed && otherTst.OwnerUUID == wolfTst.OwnerUUID {
			return false
		}
		return true
	}
	if targetType == MobTypeCat || targetType == MobTypeParrot {
		otherTst, ok := s.GetTamedMobState(targetEntityID)
		if ok && otherTst.Tamed {
			return false
		}
	}
	if IsEquineOrLlamaMob(targetType) && s.IsMobTamed(targetEntityID) {
		return false
	}
	return true
}

// IsWildWolfPreyType reports whether an untamed wolf naturally hunts a mob of
// `typeName` (`NonTameRandomTargetGoal` in `Wolf.registerGoals`: `#minecraft:amanita`
// prey `sheep`, `rabbit`, `fox`, or baby `turtle`).
func IsWildWolfPreyType(typeName string, isBaby bool) bool {
	switch canonicalResourceName(typeName) {
	case MobTypeSheep, MobTypeRabbit, MobTypeFox:
		return true
	case MobTypeTurtle:
		return isBaby
	default:
		return false
	}
}

// IsWolfSkeletonTargetType reports whether a wolf naturally targets `typeName`
// (`AbstractSkeleton`: `skeleton`, `wither_skeleton`, `stray`, `bogged`).
func IsWolfSkeletonTargetType(typeName string) bool {
	return IsSkeletonFamilyMob(typeName)
}

// WolfAvoidLlamaCheck checks whether any `minecraft:llama` or `minecraft:trader_llama`
// is within `WolfAvoidLlamaDistance` (24.0 blocks) of `wolfEntityID` (`WolfAvoidEntityGoal`).
func (s *Server) WolfAvoidLlamaCheck(wolfEntityID int32) (llamaID int32, shouldAvoid bool) {
	if s == nil || s.entities == nil || wolfEntityID <= 0 {
		return 0, false
	}
	wolf := s.entities.Get(wolfEntityID)
	if wolf == nil || canonicalResourceName(wolf.TypeName) != MobTypeWolf {
		return 0, false
	}
	dim := wolf.EffectiveDimension()
	maxDistSq := WolfAvoidLlamaDistance * WolfAvoidLlamaDistance
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		other := &allEnts[i]
		if other.ID == wolfEntityID || other.EffectiveDimension() != dim {
			continue
		}
		norm := canonicalResourceName(other.TypeName)
		if norm != MobTypeLlama && norm != MobTypeTraderLlama {
			continue
		}
		dx := other.X - wolf.X
		dy := other.Y - wolf.Y
		dz := other.Z - wolf.Z
		if dx*dx+dy*dy+dz*dz <= maxDistSq {
			return other.ID, true
		}
	}
	return 0, false
}

// ============================================================================
// 2. CAT & OCELOT: Sound Variants,Weighted Morning Gift, Bed/Block Goals, Stealth
// ============================================================================

// AllCatSoundVariants returns a copy of all 2 vanilla 26.1.2 `CatSoundVariants`.
func AllCatSoundVariants() []string {
	out := make([]string, len(allCatSoundVariants))
	copy(out, allCatSoundVariants)
	return out
}

// CatSoundVariantRegistryIndex returns the registry index (0..1) for a
// `minecraft:cat_sound_variant` identifier.
func CatSoundVariantRegistryIndex(variant string) int32 {
	if canonicalResourceName(variant) == CatSoundVariantRoyal {
		return 1
	}
	return 0
}

// SelectRandomCatSoundVariant picks one of the 2 vanilla 26.1.2 cat sound variants.
func SelectRandomCatSoundVariant(rng *rand.Rand) string {
	if rng != nil && rng.Intn(2) == 1 {
		return CatSoundVariantRoyal
	}
	return CatSoundVariantClassic
}

// CatSoundVariant returns the cat sound variant (`classic` or `royal`) for entityID.
func (s *Server) CatSoundVariant(entityID int32) string {
	if s == nil || entityID <= 0 {
		return CatSoundVariantClassic
	}
	st := s.ensureCatPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.RLock()
	v := st.soundVariant
	fm.mu.RUnlock()
	return v
}

// SetCatSoundVariant sets the sound variant for a cat entity.
func (s *Server) SetCatSoundVariant(entityID int32, variant string) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	norm := canonicalResourceName(variant)
	if norm != CatSoundVariantRoyal {
		norm = CatSoundVariantClassic
	}
	st := s.ensureCatPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.Lock()
	st.soundVariant = norm
	fm.mu.Unlock()
	return true
}

// RollCatMorningGiftLoot rolls 1 item from the vanilla 26.1.2
// `data/minecraft/loot_table/gameplay/cat_morning_gift.json` table:
//   - `minecraft:rabbit_hide` (weight 10)
//   - `minecraft:rabbit_foot` (weight 10)
//   - `minecraft:chicken` (weight 10)
//   - `minecraft:feather` (weight 10)
//   - `minecraft:rotten_flesh` (weight 10)
//   - `minecraft:string` (weight 10)
//   - `minecraft:phantom_membrane` (weight 2)
//   Total weight = 62.
func RollCatMorningGiftLoot(rng *rand.Rand) world.ItemStack {
	type weightedGift struct {
		itemID string
		weight int
	}
	entries := [7]weightedGift{
		{"minecraft:rabbit_hide", 10},
		{"minecraft:rabbit_foot", 10},
		{"minecraft:chicken", 10},
		{"minecraft:feather", 10},
		{"minecraft:rotten_flesh", 10},
		{"minecraft:string", 10},
		{"minecraft:phantom_membrane", 2},
	}
	const totalWeight = 62
	roll := 0
	if rng != nil {
		roll = rng.Intn(totalWeight)
	}
	acc := 0
	for _, e := range entries {
		acc += e.weight
		if roll < acc {
			return world.NewNamedItemStack(e.itemID, 1)
		}
	}
	return world.NewNamedItemStack("minecraft:string", 1)
}

// UpdateCatRelaxOnSleepingOwner implements `CatRelaxOnOwnerGoal` & `CatLieOnBedGoal`
// in vanilla 26.1.2:
//   - While `ownerSleeping` is true and the tamed, non-sitting cat is within 10 blocks
//     of its owner, sets `IS_LYING = true`, increments `onBedTicks`, and once
//     `onBedTicks >= 40` sets `RELAX_STATE_ONE = true` and plays `entity.cat.purr`.
//   - When `ownerSleeping` transitions to false, clears `IS_LYING` / `RELAX_STATE_ONE`;
//     if `sleepTimer >= 100`, attempts to deliver a morning gift (`70%` chance).
func (s *Server) UpdateCatRelaxOnSleepingOwner(catEntityID int32, owner *PlayerSession, ownerSleeping bool, sleepTimer int, rng *rand.Rand) (lying, relaxed, purred, gifted bool) {
	if s == nil || s.entities == nil || catEntityID <= 0 || owner == nil {
		return false, false, false, false
	}
	ent := s.entities.Get(catEntityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCat {
		return false, false, false, false
	}
	tst, ok := s.GetTamedMobState(catEntityID)
	if !ok || !tst.Tamed || tst.Sitting || tst.OwnerUUID != owner.Profile.UUID {
		return false, false, false, false
	}
	st := s.ensureCatPolishState(catEntityID)
	fm := s.farmPolish()

	if !ownerSleeping {
		fm.mu.Lock()
		wasOnBed := st.onBedTicks > 0
		st.onBedTicks = 0
		fm.mu.Unlock()
		s.SetCatLyingOnBed(catEntityID, false, false)
		if wasOnBed && sleepTimer >= CatMorningGiftSleepTimerMin {
			_, _, gifted = s.TryDeliverCatMorningGift(owner, false, rng)
		}
		return false, false, false, gifted
	}

	oSnap := owner.Snapshot()
	dx := ent.X - oSnap.X
	dy := ent.Y - oSnap.Y
	dz := ent.Z - oSnap.Z
	if dx*dx+dy*dy+dz*dz > CatMorningGiftSearchRange*CatMorningGiftSearchRange {
		return false, false, false, false
	}

	fm.mu.Lock()
	st.onBedTicks++
	ticks := st.onBedTicks
	fm.mu.Unlock()

	lying = true
	relaxed = ticks >= CatRelaxOnBedTicksThreshold
	s.SetCatLyingOnBed(catEntityID, lying, relaxed)
	if relaxed {
		purred = true
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundCatPurr, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	}
	return lying, relaxed, purred, false
}

// IsCatSitTargetBlock implements `CatSitOnBlockGoal.isValidTarget` in vanilla 26.1.2:
// returns true if `(bx, by, bz)` has air above it and is a `chest`/`trapped_chest`,
// a lit `furnace`/`blast_furnace`/`smoker`, or the `foot` part of any bed (`#minecraft:beds`).
func IsCatSitTargetBlock(cache *world.Cache, bx, by, bz int) bool {
	if cache == nil || by+1 >= world.MinY+world.WorldHeight {
		return false
	}
	if cache.GetBlock(bx, by+1, bz) != world.StateAir {
		return false
	}
	blk := cache.GetBlock(bx, by, bz)
	name := world.BlockName(blk)
	props := world.BlockProperties(blk)
	switch name {
	case "minecraft:chest", "minecraft:trapped_chest":
		return true
	case "minecraft:furnace", "minecraft:blast_furnace", "minecraft:smoker":
		return props["lit"] == "true"
	default:
		if strings.HasSuffix(name, "_bed") {
			return props["part"] != "head"
		}
		return false
	}
}

// TryCatSitOnNearbyBlock scans an 8x2x8 box around a tamed, non-sitting cat for
// a valid `CatSitOnBlockGoal` block (chest, lit furnace, or bed foot) and moves
// the cat onto the block in sitting pose.
func (s *Server) TryCatSitOnNearbyBlock(catEntityID int32) (bx, by, bz int, satOnBlock bool) {
	if s == nil || s.entities == nil || catEntityID <= 0 {
		return 0, 0, 0, false
	}
	ent := s.entities.Get(catEntityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCat {
		return 0, 0, 0, false
	}
	tst, ok := s.GetTamedMobState(catEntityID)
	if !ok || !tst.Tamed || tst.Sitting || s.IsEntityLeashed(catEntityID) {
		return 0, 0, 0, false
	}
	cache := s.DimensionChunks(ent.EffectiveDimension())
	if cache == nil {
		return 0, 0, 0, false
	}
	cx := int(math.Floor(ent.X))
	cy := int(math.Floor(ent.Y))
	cz := int(math.Floor(ent.Z))
	for dy := -1; dy <= 1; dy++ {
		y := cy + dy
		if y < world.MinY || y+1 >= world.MinY+world.WorldHeight {
			continue
		}
		for dx := -CatSitOnBlockSearchRadius; dx <= CatSitOnBlockSearchRadius; dx++ {
			for dz := -CatSitOnBlockSearchRadius; dz <= CatSitOnBlockSearchRadius; dz++ {
				tx, tz := cx+dx, cz+dz
				if IsCatSitTargetBlock(cache, tx, y, tz) {
					s.entities.Update(catEntityID, func(e *world.Entity) {
						e.X = float64(tx) + 0.5
						e.Y = float64(y + 1)
						e.Z = float64(tz) + 0.5
					})
					s.SetMobSitting(catEntityID, true)
					st := s.ensureCatPolishState(catEntityID)
					fm := s.farmPolish()
					fm.mu.Lock()
					st.sittingBlock = [3]int{tx, y, tz}
					st.onBlock = true
					fm.mu.Unlock()
					return tx, y, tz, true
				}
			}
		}
	}
	return 0, 0, 0, false
}

// RollCatDeathLoot rolls vanilla 26.1.2 `entities/cat.json` death loot:
// `0..2` `minecraft:string` (+ `0..lootingLevel`).
func RollCatDeathLoot(rng *rand.Rand, lootingLevel int) []world.ItemStack {
	count := int32(1)
	if rng != nil {
		count = int32(rng.Intn(3))
		if lootingLevel > 0 {
			count += int32(rng.Intn(lootingLevel + 1))
		}
	}
	if count <= 0 {
		return nil
	}
	if stack := world.NewNamedItemStack("minecraft:string", count); !stack.IsEmpty() {
		return []world.ItemStack{stack}
	}
	return nil
}

// OcelotMovementSpeedForPose returns the effective movement speed for an ocelot
// using vanilla `Ocelot.customServerAiStep` speed modifiers (`CROUCH_SPEED_MOD = 0.6`,
// `WALK_SPEED_MOD = 0.8`, `SPRINT_SPEED_MOD = 1.33`).
func OcelotMovementSpeedForPose(crouching, sprinting bool) float64 {
	if crouching {
		return OcelotWanderSpeed * OcelotCrouchSpeedMod
	}
	if sprinting {
		return OcelotWanderSpeed * OcelotSprintSpeedMod
	}
	return OcelotWanderSpeed * OcelotWalkSpeedMod
}

// ShouldOcelotAvoidPlayers reports whether the ocelot with entityID actively
// avoids players within 16 blocks (`!isTrusting()`).
func (s *Server) ShouldOcelotAvoidPlayers(entityID int32) bool {
	return !s.IsOcelotTrusting(entityID)
}

// IsOcelotPrey reports whether an ocelot hunts `typeName` (`chicken` or baby `turtle`).
func IsOcelotPrey(typeName string, isBaby bool) bool {
	switch canonicalResourceName(typeName) {
	case MobTypeChicken:
		return true
	case MobTypeTurtle:
		return isBaby
	default:
		return false
	}
}

// ============================================================================
// 3. PARROT: 42-Entry MOB_SOUND_MAP, ShoulderRidingEntity, Flapping & Loot
// ============================================================================

// ParrotMobSoundMap returns a copy of all 42 entries in `Parrot.MOB_SOUND_MAP`
// in Minecraft 26.1.2.
func ParrotMobSoundMap() map[string]string {
	out := make(map[string]string, len(parrotMobSoundMap2612))
	for k, v := range parrotMobSoundMap2612 {
		out[k] = v
	}
	return out
}

// SetParrotRideCooldown sets the `ShoulderRidingEntity.rideCooldownCounter`
// for a parrot entity (`canSitOnShoulder` requires `rideCooldownTicks > 100`).
func (s *Server) SetParrotRideCooldown(entityID int32, ticks int) {
	if s == nil || entityID <= 0 {
		return
	}
	st := s.ensureParrotPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.Lock()
	st.rideCooldownTicks = ticks
	fm.mu.Unlock()
}

// CanParrotSitOnShoulder implements `ShoulderRidingEntity.canSitOnShoulder()`
// and `LandOnOwnersShoulderGoal.canUse()` in vanilla 26.1.2:
// requires `rideCooldownCounter > 100`, tamed, not sitting, and not leashed.
func (s *Server) CanParrotSitOnShoulder(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	tst, ok := s.GetTamedMobState(entityID)
	if !ok || !tst.Tamed || tst.Sitting || s.IsEntityLeashed(entityID) {
		return false
	}
	st := s.ensureParrotPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.RLock()
	cooldown := st.rideCooldownTicks
	fm.mu.RUnlock()
	return cooldown > ParrotRideCooldownTicks
}

// TryLandParrotOnOwnerShoulder implements `LandOnOwnersShoulderGoal.tick()`:
// if the parrot can sit on a shoulder and its owner is alive, not spectator,
// not flying, and not in water or powder snow, mounts the parrot onto the
// owner's left or right shoulder.
func (s *Server) TryLandParrotOnOwnerShoulder(entityID int32, owner *PlayerSession) (perched bool, side string) {
	if s == nil || owner == nil || !s.CanParrotSitOnShoulder(entityID) {
		return false, ""
	}
	snap := owner.Snapshot()
	if owner.IsDead() || snap.GameMode == GameModeSpectator || snap.Flying {
		return false, ""
	}
	dim := world.NormalizeDimension(snap.Dimension)
	if _, _, _, submerged := s.EntitySubmergedFluidDepth(dim, snap.X, snap.Y, snap.Z); submerged {
		return false, ""
	}
	if cache := s.DimensionChunks(dim); cache != nil {
		bx, by, bz := int(math.Floor(snap.X)), int(math.Floor(snap.Y)), int(math.Floor(snap.Z))
		if world.BlockName(cache.GetBlock(bx, by, bz)) == "minecraft:powder_snow" {
			return false, ""
		}
	}
	leftBefore, _, _, _ := s.PlayerShoulderParrots(owner)
	if !s.TryPerchParrotOnShoulder(owner, entityID) {
		return false, ""
	}
	if leftBefore == nil {
		return true, "left"
	}
	return true, "right"
}

// CheckPlayerShoulderParrotDismount implements `Player.removeEntitiesOnShoulder`
// in vanilla 26.1.2: dismounts perched shoulder parrots when the player falls
// (`fallDistance > 0.5`), enters water, lava, or powder snow, takes damage, or starts flying.
func (s *Server) CheckPlayerShoulderParrotDismount(owner *PlayerSession, fallDistance float64, inWater, inLava, inPowderSnow, tookDamage, isFlying bool) []int32 {
	if s == nil || owner == nil {
		return nil
	}
	if fallDistance > ParrotDismountFallDistanceMin || inWater || inLava || inPowderSnow || tookDamage || isFlying {
		ids := s.DismountShoulderParrots(owner)
		for _, id := range ids {
			s.SetParrotRideCooldown(id, 0)
		}
		return ids
	}
	return nil
}

// ParrotCalculateFlapping implements `Parrot.aiStep` slow-falling (`vy * 0.6` when
// falling) and `Parrot.checkFallDamage` (always `0` fall damage).
func ParrotCalculateFlapping(vy float64, onGround bool) (nextVY float64, fallDamage float32) {
	nextVY = vy
	if !onGround && nextVY < 0.0 {
		nextVY *= ParrotFlapFallMultiplier
	}
	return nextVY, 0.0
}

// RollParrotDeathLoot rolls vanilla 26.1.2 `entities/parrot.json` death loot:
// `1..2` `minecraft:feather` (+ `0..lootingLevel`).
func RollParrotDeathLoot(rng *rand.Rand, lootingLevel int) []world.ItemStack {
	count := int32(1)
	if rng != nil {
		count = int32(1 + rng.Intn(2))
		if lootingLevel > 0 {
			count += int32(rng.Intn(lootingLevel + 1))
		}
	}
	if stack := world.NewNamedItemStack("minecraft:feather", count); !stack.IsEmpty() {
		return []world.ItemStack{stack}
	}
	return nil
}

// ============================================================================
// 4. GOAT: Ram Preparation, Head Lowering, Screaming Breeding & Fall Reduction
// ============================================================================

// StartGoatPrepareRam starts the 40-tick `PrepareRamNearestTarget` phase on a
// goat entity and broadcasts `EntityEventGoatLowerHead` (58).
func (s *Server) StartGoatPrepareRam(entityID int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeGoat {
		return false
	}
	st := s.ensureGoatPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.Lock()
	st.preparingRam = true
	st.prepareTicks = 0
	st.lowerHeadTick = 1
	fm.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(entityID, EntityEventGoatLowerHead))
	return true
}

// TickGoatPrepareRam advances the goat's ram preparation by one tick, returning
// `readyToRam = true` once `GoatRamPrepareTicks` (40 ticks) have elapsed.
func (s *Server) TickGoatPrepareRam(entityID int32) (preparing, readyToRam bool, headRotRad float32) {
	if s == nil || entityID <= 0 {
		return false, false, 0
	}
	st := s.ensureGoatPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.Lock()
	if !st.preparingRam {
		if st.lowerHeadTick > 0 {
			st.lowerHeadTick--
		}
		rot := GetGoatRammingXHeadRot(st.lowerHeadTick)
		fm.mu.Unlock()
		return false, false, rot
	}
	st.prepareTicks++
	if st.lowerHeadTick < GoatLowerHeadMaxTicks {
		st.lowerHeadTick++
	}
	ready := st.prepareTicks >= GoatRamPrepareTicks
	rot := GetGoatRammingXHeadRot(st.lowerHeadTick)
	fm.mu.Unlock()
	return true, ready, rot
}

// StopGoatPrepareRam ends the goat's head-lowering state and broadcasts
// `EntityEventGoatStopLowerHead` (59).
func (s *Server) StopGoatPrepareRam(entityID int32) {
	if s == nil || entityID <= 0 {
		return
	}
	st := s.ensureGoatPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.Lock()
	wasPrep := st.preparingRam
	st.preparingRam = false
	st.prepareTicks = 0
	fm.mu.Unlock()
	if wasPrep && s.entities != nil {
		if ent := s.entities.Get(entityID); ent != nil {
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayEntityEvent,
				EncodeEntityEventPacket(entityID, EntityEventGoatStopLowerHead))
		}
	}
}

// GetGoatRammingXHeadRot computes `Goat.getRammingXHeadRot()` in vanilla 26.1.2:
// `(lowerHeadTick / 20.0) * 30.0 * (pi / 180.0)`.
func GetGoatRammingXHeadRot(lowerHeadTick int) float32 {
	if lowerHeadTick <= 0 {
		return 0
	}
	if lowerHeadTick > GoatLowerHeadMaxTicks {
		lowerHeadTick = GoatLowerHeadMaxTicks
	}
	return (float32(lowerHeadTick) / float32(GoatLowerHeadMaxTicks)) * 30.0 * (math.Pi / 180.0)
}

// BreedGoatOffspringScreaming implements `Goat.getBreedOffspring` screaming
// inheritance in vanilla 26.1.2:
//   - Picks one parent at random (`rng.Intn(2) == 0`); if that parent is a
//     screaming goat, or if the other parent is a screaming goat, or with a
//     `GoatScreamingSpawnChance` (`0.02` = 2%) mutation roll, the kid is a screaming goat.
func BreedGoatOffspringScreaming(parentAScreaming, parentBScreaming bool, rng *rand.Rand) bool {
	if parentAScreaming && parentBScreaming {
		return true
	}
	if parentAScreaming || parentBScreaming {
		return true
	}
	if rng != nil && rng.Float32() < ScreamingGoatSpawnChance {
		return true
	}
	return false
}

// RollGoatInitialHorns implements `Goat.finalizeSpawn` horn initialization in
// vanilla 26.1.2:
//   - Baby goats start with `(false, false)` horns until adulthood.
//   - Adult goats have a `10%` (`nextFloat() < 0.10`) chance to spawn missing 1 random horn.
func RollGoatInitialHorns(isBaby bool, rng *rand.Rand) (leftHorn, rightHorn bool) {
	if isBaby {
		return false, false
	}
	leftHorn, rightHorn = true, true
	if rng != nil && rng.Float32() < GoatAdultMissingHornSpawnChance {
		if rng.Intn(2) == 0 {
			leftHorn = false
		} else {
			rightHorn = false
		}
	}
	return leftHorn, rightHorn
}

// RollGoatDeathLoot rolls vanilla 26.1.2 `entities/goat.json` death loot
// (which delegates to `entities/sheep.json` mutton pool: `1..2` `mutton`/`cooked_mutton` + Looting).
func RollGoatDeathLoot(rng *rand.Rand, lootingLevel int, onFire bool) []world.ItemStack {
	count := int32(1)
	if rng != nil {
		count = int32(1 + rng.Intn(2))
		if lootingLevel > 0 {
			count += int32(rng.Intn(lootingLevel + 1))
		}
	}
	itemID := "minecraft:mutton"
	if onFire {
		itemID = "minecraft:cooked_mutton"
	}
	if stack := world.NewNamedItemStack(itemID, count); !stack.IsEmpty() {
		return []world.ItemStack{stack}
	}
	return nil
}

// ============================================================================
// 5. MOOSHROOM (MushroomCow, `minecraft:mooshroom`): Variants, Stew, Shear & Bolt
// ============================================================================

// EncodeMooshroomEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating a mooshroom's `AgableMob.DATA_BABY_ID` (16, bool) and
// `MushroomCow.DATA_TYPE` (17, int: 0=red, 1=brown) in protocol 775.
func EncodeMooshroomEntityDataPacket(entityID int32, isBaby bool, variant int32) []byte {
	if variant != MooshroomVariantBrown {
		variant = MooshroomVariantRed
	}
	w := protocol.NewWriter(20)
	w.VarInt(entityID)

	w.Byte(EntityDataAgeableBabyID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(isBaby)

	w.Byte(EntityDataMooshroomTypeID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(variant)

	w.Byte(0xFF)
	return w.Bytes()
}

// SpawnMooshroom spawns a `minecraft:mooshroom` entity in `dimension` at `(x, y, z)`
// with the given variant (`0 = red`, `1 = brown`) and `isBaby` flag.
func (s *Server) SpawnMooshroom(dimension string, x, y, z float64, variant int32, isBaby bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	if variant != MooshroomVariantBrown {
		variant = MooshroomVariantRed
	}
	typeID := ResolveEntityTypeID(MobTypeMooshroom)
	if typeID < 0 {
		typeID = EntityTypeMooshroomID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeMooshroom,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})

	fm := s.farmPolish()
	fm.mu.Lock()
	fm.mooshrooms[id] = &mooshroomState{
		EntityID: id,
		Variant:  variant,
	}
	fm.mu.Unlock()

	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(id, tr.aiTickID)
	mem.healthInit = true
	mem.health = DefaultMooshroomMaxHealth
	mem.lastHealth = DefaultMooshroomMaxHealth
	if isBaby {
		mem.ageTicks = BabyMobGrowthTicks
	}
	tr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeMooshroomEntityDataPacket(id, isBaby, variant))
	}
	return id, nil
}

func (s *Server) ensureMooshroomState(entityID int32) *mooshroomState {
	fm := s.farmPolish()
	fm.mu.Lock()
	defer fm.mu.Unlock()
	st := fm.mooshrooms[entityID]
	if st == nil {
		st = &mooshroomState{
			EntityID: entityID,
			Variant:  MooshroomVariantRed,
		}
		fm.mooshrooms[entityID] = st
	}
	return st
}

// MooshroomVariant returns the variant (`0 = red`, `1 = brown`) of a mooshroom entity.
func (s *Server) MooshroomVariant(entityID int32) int32 {
	if s == nil || entityID <= 0 {
		return MooshroomVariantRed
	}
	st := s.ensureMooshroomState(entityID)
	fm := s.farmPolish()
	fm.mu.RLock()
	v := st.Variant
	fm.mu.RUnlock()
	return v
}

// SetMooshroomVariant sets the variant (`0 = red`, `1 = brown`) of a mooshroom
// entity and broadcasts `EncodeMooshroomEntityDataPacket`.
func (s *Server) SetMooshroomVariant(entityID int32, variant int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeMooshroom {
		return false
	}
	if variant != MooshroomVariantBrown {
		variant = MooshroomVariantRed
	}
	st := s.ensureMooshroomState(entityID)
	fm := s.farmPolish()
	fm.mu.Lock()
	st.Variant = variant
	fm.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeMooshroomEntityDataPacket(entityID, s.IsMobBaby(entityID), variant))
	return true
}

// BreedMooshroomOffspringVariant implements `MushroomCow.chooseBabyType` in vanilla 26.1.2:
//   - If both parents have the same variant and `rng.Intn(1024) == 0` (`MUTATE_CHANCE = 1024`),
//     mutates `RED <-> BROWN`.
//   - Otherwise picks either parent's variant with 50/50 probability.
func BreedMooshroomOffspringVariant(variantA, variantB int32, rng *rand.Rand) int32 {
	if variantA == variantB && rng != nil && rng.Intn(MooshroomMutateChance) == 0 {
		if variantA == MooshroomVariantRed {
			return MooshroomVariantBrown
		}
		return MooshroomVariantRed
	}
	if rng != nil && rng.Intn(2) == 1 {
		return variantB
	}
	return variantA
}

// OnMooshroomStruckByLightning implements `MushroomCow.thunderHit` in vanilla 26.1.2:
// toggles `RED <-> BROWN` unless `boltUUID` matches the last lightning bolt UUID
// that struck this mooshroom, and plays `minecraft:entity.mooshroom.convert`.
func (s *Server) OnMooshroomStruckByLightning(entityID int32, boltUUID [16]byte) (newVariant int32, toggled bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return MooshroomVariantRed, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeMooshroom {
		return MooshroomVariantRed, false
	}
	st := s.ensureMooshroomState(entityID)
	fm := s.farmPolish()
	fm.mu.Lock()
	var zeroUUID [16]byte
	if st.HasLastBolt && boltUUID != zeroUUID && st.LastBoltUUID == boltUUID {
		cur := st.Variant
		fm.mu.Unlock()
		return cur, false
	}
	if st.Variant == MooshroomVariantRed {
		st.Variant = MooshroomVariantBrown
	} else {
		st.Variant = MooshroomVariantRed
	}
	if boltUUID != zeroUUID {
		st.LastBoltUUID = boltUUID
		st.HasLastBolt = true
	}
	newVariant = st.Variant
	fm.mu.Unlock()

	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundMooshroomConvert, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 2.0, 1.0, 0))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeMooshroomEntityDataPacket(entityID, s.IsMobBaby(entityID), newVariant))
	return newVariant, true
}

// SuspiciousStewEffectForFlower maps a vanilla 26.1.2 small flower item ID to
// its `SuspiciousEffectHolder` potion effect identifier and duration in ticks.
func SuspiciousStewEffectForFlower(itemName string) (effectID string, durationTicks int32, ok bool) {
	switch canonicalResourceName(itemName) {
	case "minecraft:allium":
		return "minecraft:fire_resistance", 60, true
	case "minecraft:azure_bluet":
		return "minecraft:blindness", 160, true
	case "minecraft:blue_orchid", "minecraft:dandelion", "minecraft:golden_dandelion":
		return "minecraft:saturation", 7, true
	case "minecraft:cornflower":
		return "minecraft:jump_boost", 100, true
	case "minecraft:lily_of_the_valley":
		return "minecraft:poison", 220, true
	case "minecraft:orange_tulip", "minecraft:pink_tulip", "minecraft:red_tulip", "minecraft:white_tulip":
		return "minecraft:weakness", 160, true
	case "minecraft:oxeye_daisy":
		return "minecraft:regeneration", 140, true
	case "minecraft:poppy", "minecraft:torchflower":
		return "minecraft:night_vision", 100, true
	case "minecraft:wither_rose":
		return "minecraft:wither", 140, true
	case "minecraft:open_eyeblossom":
		return "minecraft:blindness", 140, true
	case "minecraft:closed_eyeblossom":
		return "minecraft:nausea", 140, true
	default:
		return "", 0, false
	}
}

// FeedBrownMooshroomFlower feeds a small flower to an adult `BROWN` mooshroom
// (`MushroomCow.mobInteract`), storing its `SuspiciousStewEffects` for the next
// bowl milking and playing `minecraft:entity.mooshroom.eat`.
func (s *Server) FeedBrownMooshroomFlower(session *PlayerSession, entityID int32, flowerItem string) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeMooshroom || s.IsMobBaby(entityID) {
		return false
	}
	effectID, dur, ok := SuspiciousStewEffectForFlower(flowerItem)
	if !ok {
		return false
	}
	st := s.ensureMooshroomState(entityID)
	fm := s.farmPolish()
	fm.mu.Lock()
	if st.Variant != MooshroomVariantBrown || st.StewEffectID != "" {
		fm.mu.Unlock()
		return false
	}
	st.StewEffectID = effectID
	st.StewDuration = dur
	fm.mu.Unlock()

	if session != nil {
		consumeOneHeldItemIfSurvival(session)
	}
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundMooshroomEat, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 2.0, 1.0, 0))
	return true
}

// MooshroomStewEffect returns the pending `SuspiciousStewEffects` stored on a
// brown mooshroom entity, if any.
func (s *Server) MooshroomStewEffect(entityID int32) (effectID string, durationTicks int32, hasEffect bool) {
	if s == nil || entityID <= 0 {
		return "", 0, false
	}
	st := s.ensureMooshroomState(entityID)
	fm := s.farmPolish()
	fm.mu.RLock()
	effectID = st.StewEffectID
	durationTicks = st.StewDuration
	fm.mu.RUnlock()
	return effectID, durationTicks, effectID != ""
}

// MilkMooshroomWithBowl milks an adult mooshroom with a `minecraft:bowl`
// (`MushroomCow.mobInteract`):
//   - If a brown mooshroom has a stored flower effect, yields `minecraft:suspicious_stew`
//     (with `SuspiciousStewEffectID` and `SuspiciousStewDuration` metadata), clears
//     the stored effect, and plays `entity.mooshroom.milk_suspiciously`.
//   - Otherwise yields `minecraft:mushroom_stew` and plays `entity.mooshroom.milk`.
func (s *Server) MilkMooshroomWithBowl(session *PlayerSession, entityID int32) (stewStack world.ItemStack, ok bool) {
	if s == nil || s.entities == nil || entityID <= 0 || s.IsMobBaby(entityID) {
		return world.ItemStack{}, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeMooshroom {
		return world.ItemStack{}, false
	}
	st := s.ensureMooshroomState(entityID)
	fm := s.farmPolish()
	fm.mu.Lock()
	effID := st.StewEffectID
	st.StewEffectID = ""
	st.StewDuration = 0
	fm.mu.Unlock()

	snd := SoundMooshroomMilk
	if effID != "" {
		stewStack = world.NewNamedItemStack("minecraft:suspicious_stew", 1)
		snd = SoundMooshroomMilkSuspiciously
	} else {
		stewStack = world.NewNamedItemStack("minecraft:mushroom_stew", 1)
	}
	if stewStack.IsEmpty() {
		return world.ItemStack{}, false
	}

	if session != nil {
		replaceOrAddFilledContainerInHand(session, stewStack)
	}
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(snd, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return stewStack, true
}

// MilkMooshroomWithBucket milks an adult mooshroom with an empty `minecraft:bucket`
// (`AbstractCow.mobInteract`), yielding `minecraft:milk_bucket` and playing `entity.cow.milk`.
func (s *Server) MilkMooshroomWithBucket(session *PlayerSession, entityID int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 || s.IsMobBaby(entityID) {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeMooshroom {
		return false
	}
	milk := world.NewNamedItemStack("minecraft:milk_bucket", 1)
	if milk.IsEmpty() {
		return false
	}
	if session != nil {
		replaceOrAddFilledContainerInHand(session, milk)
	}
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.cow.milk", SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true
}

// ShearMooshroom implements `MushroomCow.shear` in vanilla 26.1.2:
//   - Requires an adult `minecraft:mooshroom`.
//   - Plays `minecraft:entity.mooshroom.shear`.
//   - Converts the mooshroom into a `minecraft:cow` at the same position/health.
//   - Drops 5 `minecraft:red_mushroom` (for `RED`) or 5 `minecraft:brown_mushroom` (for `BROWN`).
func (s *Server) ShearMooshroom(session *PlayerSession, entityID int32) (cowEntityID int32, mushroomDrops []world.ItemStack, sheared bool) {
	if s == nil || s.entities == nil || entityID <= 0 || s.IsMobBaby(entityID) {
		return 0, nil, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeMooshroom {
		return 0, nil, false
	}
	variant := s.MooshroomVariant(entityID)
	curHP := s.MobHealth(entityID)
	dim := ent.EffectiveDimension()
	x, y, z := ent.X, ent.Y, ent.Z
	yaw, pitch := ent.Yaw, ent.Pitch
	cp := ent.ChunkPos()

	s.entities.Remove(entityID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{entityID}))
	fm := s.farmPolish()
	fm.mu.Lock()
	delete(fm.mooshrooms, entityID)
	fm.mu.Unlock()

	cowID, err := s.SpawnMobInDimension(dim, x, y, z, MobTypeCow)
	if err != nil {
		return 0, nil, false
	}
	s.entities.Update(cowID, func(e *world.Entity) {
		e.Yaw = yaw
		e.Pitch = pitch
	})
	if curHP > 0 {
		tr := s.entityTracker()
		tr.mu.Lock()
		if mem := tr.mobStates[cowID]; mem != nil {
			mem.health = curHP
			mem.lastHealth = curHP
		}
		tr.mu.Unlock()
	}

	mushroomID := "minecraft:red_mushroom"
	if variant == MooshroomVariantBrown {
		mushroomID = "minecraft:brown_mushroom"
	}
	for i := 0; i < 5; i++ {
		stack := world.NewNamedItemStack(mushroomID, 1)
		if !stack.IsEmpty() {
			mushroomDrops = append(mushroomDrops, stack)
			_, _ = s.SpawnItemEntityInDimension(dim, x, y+0.5, z, stack)
		}
	}

	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundMooshroomShear, SoundSourcePlayers, x, y, z, 1.0, 1.0, 0))
	return cowID, mushroomDrops, true
}

// InteractWithMooshroom dispatches right-click interactions on a `minecraft:mooshroom`
// (bowl milking, bucket milking, shears conversion, brown mooshroom flower feeding).
func (s *Server) InteractWithMooshroom(session *PlayerSession, entityID int32, heldItemID string) (handled bool) {
	normItem := canonicalResourceName(heldItemID)
	switch normItem {
	case "minecraft:bowl":
		_, ok := s.MilkMooshroomWithBowl(session, entityID)
		return ok
	case "minecraft:bucket":
		return s.MilkMooshroomWithBucket(session, entityID)
	case "minecraft:shears":
		_, _, ok := s.ShearMooshroom(session, entityID)
		return ok
	default:
		if _, _, isFlower := SuspiciousStewEffectForFlower(normItem); isFlower {
			return s.FeedBrownMooshroomFlower(session, entityID, normItem)
		}
		return false
	}
}

// RollMooshroomDeathLoot rolls vanilla 26.1.2 `entities/mooshroom.json` death
// loot (`0..2` `leather` + Looting, `1..3` `beef`/`cooked_beef` + Looting).
func RollMooshroomDeathLoot(rng *rand.Rand, lootingLevel int, onFire bool) []world.ItemStack {
	return RollCowDeathLoot(rng, lootingLevel, onFire)
}

// ============================================================================
// 6. SHEEP: 16 Wool Colors, Spawn Color Rules, Dye Mixing, EatBlockGoal & Loot
// ============================================================================

// SheepColor returns the DyeColor ordinal (`0..15`, default `0 = white`) of a sheep entity.
func (s *Server) SheepColor(entityID int32) byte {
	if s == nil || entityID <= 0 {
		return DyeColorWhite
	}
	st := s.ensureSheepPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.RLock()
	c := st.color & 0x0F
	fm.mu.RUnlock()
	return c
}

// SetSheepColor sets the DyeColor ordinal (`0..15`) of a sheep entity and
// broadcasts `EncodeSheepWoolDataPacket`.
func (s *Server) SetSheepColor(entityID int32, color byte) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeSheep {
		return false
	}
	color &= 0x0F
	st := s.ensureSheepPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.Lock()
	st.color = color
	fm.mu.Unlock()

	sheared := s.IsSheepSheared(entityID)
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeSheepWoolDataPacket(entityID, color, sheared))
	return true
}

// SetSheepSheared sets whether a sheep entity is sheared and broadcasts
// `EncodeSheepWoolDataPacket`.
func (s *Server) SetSheepSheared(entityID int32, sheared bool) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeSheep {
		return false
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(entityID, tr.aiTickID)
	mem.sheared = sheared
	if !sheared {
		mem.woolRegrowTimer = 0
	}
	tr.mu.Unlock()

	color := s.SheepColor(entityID)
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeSheepWoolDataPacket(entityID, color, sheared))
	return true
}

// SheepWoolItemForColor returns the `<color>_wool` item identifier for a
// `DyeColor` ordinal (`0..15`).
func SheepWoolItemForColor(color byte) string {
	return sheepWoolByColor[color&0x0F]
}

// DyeSheep dyes an unsheared sheep with `dyeItem` (`DyeItem.interactLivingEntity`),
// playing `minecraft:entity.sheep.dye` and updating `DATA_WOOL_ID`.
func (s *Server) DyeSheep(session *PlayerSession, entityID int32, dyeItem string) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeSheep {
		return false
	}
	newColor, ok := DyeColorIndexByItemName(dyeItem)
	if !ok {
		return false
	}
	if s.IsSheepSheared(entityID) || s.SheepColor(entityID) == byte(newColor) {
		return false
	}
	s.SetSheepColor(entityID, byte(newColor))
	if session != nil {
		consumeOneHeldItemIfSurvival(session)
	}
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundSheepDye, SoundSourcePlayers, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true
}

// ShearSheepColored shears an unsheared adult sheep (`Sheep.shear`), dropping
// `1..3` matching `<color>_wool` items and broadcasting `EncodeSheepWoolDataPacket`.
func (s *Server) ShearSheepColored(session *PlayerSession, entityID int32, rng *rand.Rand) (drops []world.ItemStack, sheared bool) {
	if s == nil || s.entities == nil || entityID <= 0 || s.IsMobBaby(entityID) {
		return nil, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeSheep {
		return nil, false
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(entityID, tr.aiTickID)
	if mem.sheared {
		tr.mu.Unlock()
		return nil, false
	}
	mem.sheared = true
	mem.woolRegrowTimer = 0
	tr.mu.Unlock()

	color := s.SheepColor(entityID)
	woolID := SheepWoolItemForColor(color)
	count := 2
	if rng != nil {
		count = 1 + rng.Intn(3) // 1..3 wool blocks in vanilla Sheep.shear
	}
	dim := ent.EffectiveDimension()
	stack := world.NewNamedItemStack(woolID, int32(count))
	if !stack.IsEmpty() {
		drops = append(drops, stack)
		_, _ = s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.5, ent.Z, stack)
	}
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeSheepWoolDataPacket(entityID, color, true))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.sheep.shear", SoundSourcePlayers, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return drops, true
}

// SelectSheepSpawnColor implements `SheepColorSpawnRules.getSheepColor` in
// Minecraft 26.1.2:
//   - `TEMPERATE`: `BLACK: 5`, `GRAY: 5`, `LIGHT_GRAY: 5`, `BROWN: 3`, `commonColors(WHITE): 82`
//   - `WARM`:      `GRAY: 5`, `LIGHT_GRAY: 5`, `WHITE: 5`, `BLACK: 3`, `commonColors(BROWN): 82`
//   - `COLD`:      `LIGHT_GRAY: 5`, `GRAY: 5`, `WHITE: 5`, `BROWN: 3`, `commonColors(BLACK): 82`
//   where `commonColors(defaultColor)` picks `PINK` with weight `1/500` and `defaultColor` with `499/500`.
func SelectSheepSpawnColor(climate string, rng *rand.Rand) byte {
	type colorWeight struct {
		color  byte
		weight int
	}
	var secondary [4]colorWeight
	defaultColor := byte(DyeColorWhite)
	switch strings.ToLower(strings.TrimPrefix(climate, "minecraft:")) {
	case SheepSpawnClimateWarm:
		defaultColor = DyeColorBrown
		secondary = [4]colorWeight{
			{DyeColorGray, 5},
			{DyeColorLightGray, 5},
			{DyeColorWhite, 5},
			{DyeColorBlack, 3},
		}
	case SheepSpawnClimateCold:
		defaultColor = DyeColorBlack
		secondary = [4]colorWeight{
			{DyeColorLightGray, 5},
			{DyeColorGray, 5},
			{DyeColorWhite, 5},
			{DyeColorBrown, 3},
		}
	default:
		defaultColor = DyeColorWhite
		secondary = [4]colorWeight{
			{DyeColorBlack, 5},
			{DyeColorGray, 5},
			{DyeColorLightGray, 5},
			{DyeColorBrown, 3},
		}
	}
	if rng == nil {
		return defaultColor
	}
	roll := rng.Intn(100)
	acc := 0
	for _, entry := range secondary {
		acc += entry.weight
		if roll < acc {
			return entry.color
		}
	}
	// Remaining 82% -> commonColors(defaultColor): 499 defaultColor, 1 PINK.
	if rng.Intn(500) == 0 {
		return DyeColorPink
	}
	return defaultColor
}

// SelectSheepSpawnColorForBiome maps a biome identifier to its 26.1.2
// `#minecraft:spawns_warm_variant_farm_animals` / `#minecraft:spawns_cold_variant_farm_animals`
// climate category and rolls `SelectSheepSpawnColor`.
func SelectSheepSpawnColorForBiome(biomeName string, rng *rand.Rand) byte {
	variant := ResolveFarmAnimalTempVariantForBiome(biomeName)
	switch variant {
	case FarmAnimalTempWarm:
		return SelectSheepSpawnColor(SheepSpawnClimateWarm, rng)
	case FarmAnimalTempCold:
		return SelectSheepSpawnColor(SheepSpawnClimateCold, rng)
	default:
		return SelectSheepSpawnColor(SheepSpawnClimateTemperate, rng)
	}
}

// MixSheepBreedColor implements `Sheep.getOffspringColor` / `DyeColor.getMixedColor`
// in vanilla 26.1.2: if the two parent wool colors have a crafting dye mix,
// returns the mixed color; otherwise returns one of the parents' colors at random.
func MixSheepBreedColor(colorA, colorB byte, rng *rand.Rand) byte {
	colorA &= 0x0F
	colorB &= 0x0F
	if colorA == colorB {
		return colorA
	}
	c1, c2 := colorA, colorB
	if c1 > c2 {
		c1, c2 = c2, c1
	}
	type pair struct{ a, b byte }
	switch (pair{c1, c2}) {
	case pair{DyeColorWhite, DyeColorOrange}:
		// no crafting recipe
	case pair{DyeColorWhite, DyeColorRed}:
		return DyeColorPink
	case pair{DyeColorWhite, DyeColorGreen}:
		return DyeColorLime
	case pair{DyeColorWhite, DyeColorBlue}:
		return DyeColorLightBlue
	case pair{DyeColorWhite, DyeColorGray}:
		return DyeColorLightGray
	case pair{DyeColorWhite, DyeColorBlack}:
		return DyeColorGray
	case pair{DyeColorYellow, DyeColorRed}:
		return DyeColorOrange
	case pair{DyeColorYellow, DyeColorBlue}:
		return DyeColorGreen
	case pair{DyeColorPink, DyeColorPurple}:
		return DyeColorMagenta
	case pair{DyeColorBlue, DyeColorGreen}:
		return DyeColorCyan
	case pair{DyeColorBlue, DyeColorRed}:
		return DyeColorPurple
	}
	if rng != nil && rng.Intn(2) == 1 {
		return colorB
	}
	return colorA
}

// SheepEatGrass implements `EatBlockGoal.tick` and `Sheep.ate()` in vanilla 26.1.2:
//   - Checks if the block at the sheep's feet is `#minecraft:edible_for_sheep`
//     (`short_grass`, `tall_grass`, `fern`) or the block below is `minecraft:grass_block`.
//   - Broadcasts `EntityEventEatGrass` (`10`).
//   - If `mobGriefing` is enabled, destroys the grass/fern at feet or converts the
//     `grass_block` below into `minecraft:dirt`.
//   - Calls `Sheep.ate()`: unshears the sheep (`sheared = false`), or if the sheep
//     is a baby, advances its growth age by `+1200` ticks (60 seconds).
func (s *Server) SheepEatGrass(entityID int32, mobGriefing bool) (ateGrass, regrewWool, babyAged bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false, false, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeSheep {
		return false, false, false
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return false, false, false
	}
	bx := int(math.Floor(ent.X))
	by := int(math.Floor(ent.Y))
	bz := int(math.Floor(ent.Z))
	if by <= world.MinY || by >= world.MinY+world.WorldHeight {
		return false, false, false
	}

	feetBlk := cache.GetBlock(bx, by, bz)
	feetName := world.BlockName(feetBlk)
	belowBlk := cache.GetBlock(bx, by-1, bz)
	belowName := world.BlockName(belowBlk)

	edibleAtFeet := feetName == "minecraft:short_grass" || feetName == "minecraft:grass" ||
		feetName == "minecraft:fern" || feetName == "minecraft:tall_grass"
	grassBelow := belowName == "minecraft:grass_block"
	if !edibleAtFeet && !grassBelow {
		return false, false, false
	}

	if mobGriefing {
		if edibleAtFeet {
			cache.SetBlock(bx, by, bz, world.StateAir)
		} else if grassBelow {
			if dirtState, ok := world.BlockStateByName("minecraft:dirt", nil); ok {
				cache.SetBlock(bx, by-1, bz, dirtState)
			}
		}
	}

	st := s.ensureSheepPolishState(entityID)
	fm := s.farmPolish()
	fm.mu.Lock()
	st.eatGrassTicks = SheepEatGrassAnimationTicks
	fm.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(entityID, EntityEventEatGrass))

	if s.IsMobBaby(entityID) {
		tr := s.entityTracker()
		tr.mu.Lock()
		if mem := tr.mobStates[entityID]; mem != nil && mem.ageTicks < 0 {
			mem.ageTicks += SheepBabyEatGrassAgeBonus
			if mem.ageTicks >= 0 {
				mem.ageTicks = 0
			}
			babyAged = true
		}
		tr.mu.Unlock()
		if babyAged && !s.IsMobBaby(entityID) {
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
				EncodeAgeableMobDataPacket(entityID, false))
		}
	} else if s.IsSheepSheared(entityID) {
		s.SetSheepSheared(entityID, false)
		regrewWool = true
	}
	return true, regrewWool, babyAged
}

// RollSheepDeathLoot rolls vanilla 26.1.2 `entities/sheep.json` death loot:
//   - `1..2` `minecraft:mutton` (or `minecraft:cooked_mutton` if `onFire`) + `0..lootingLevel`
//   - `1` matching `<color>_wool` if `!sheared`.
func RollSheepDeathLoot(rng *rand.Rand, lootingLevel int, onFire bool, color byte, sheared bool) []world.ItemStack {
	var out []world.ItemStack
	muttonCount := int32(1)
	if rng != nil {
		muttonCount = int32(1 + rng.Intn(2))
		if lootingLevel > 0 {
			muttonCount += int32(rng.Intn(lootingLevel + 1))
		}
	}
	meatID := "minecraft:mutton"
	if onFire {
		meatID = "minecraft:cooked_mutton"
	}
	if s1 := world.NewNamedItemStack(meatID, muttonCount); !s1.IsEmpty() {
		out = append(out, s1)
	}
	if !sheared {
		woolID := SheepWoolItemForColor(color)
		if s2 := world.NewNamedItemStack(woolID, 1); !s2.IsEmpty() {
			out = append(out, s2)
		}
	}
	return out
}

// ============================================================================
// 7. COW, CHICKEN & PIG: 26.1.2 Variants, Eggs, Pig Boost/Lightning & Loot
// ============================================================================

// AllFarmAnimalTempVariants returns a copy of the 3 vanilla 26.1.2 farm animal
// temperature variants (`minecraft:temperate`, `minecraft:warm`, `minecraft:cold`).
func AllFarmAnimalTempVariants() []string {
	out := make([]string, len(allFarmAnimalTempVariants))
	copy(out, allFarmAnimalTempVariants)
	return out
}

// FarmAnimalTempVariantIndex returns the network registry index (`0..2`) for a
// temperature variant (`temperate = 0`, `warm = 1`, `cold = 2`).
func FarmAnimalTempVariantIndex(variant string) int32 {
	switch canonicalResourceName(variant) {
	case FarmAnimalTempWarm:
		return 1
	case FarmAnimalTempCold:
		return 2
	default:
		return 0
	}
}

// ResolveFarmAnimalTempVariantForBiome maps a biome identifier to its vanilla
// 26.1.2 farm animal temperature variant (`#minecraft:spawns_warm_variant_farm_animals`
// or `#minecraft:spawns_cold_variant_farm_animals`, defaulting to `minecraft:temperate`).
func ResolveFarmAnimalTempVariantForBiome(biomeName string) string {
	switch canonicalResourceName(biomeName) {
	case "minecraft:savanna", "minecraft:savanna_plateau", "minecraft:windswept_savanna",
		"minecraft:jungle", "minecraft:sparse_jungle", "minecraft:bamboo_jungle",
		"minecraft:badlands", "minecraft:eroded_badlands", "minecraft:wooded_badlands",
		"minecraft:desert", "minecraft:mangrove_swamp", "minecraft:warm_ocean":
		return FarmAnimalTempWarm
	case "minecraft:snowy_plains", "minecraft:ice_spikes", "minecraft:snowy_taiga",
		"minecraft:snowy_beach", "minecraft:grove", "minecraft:snowy_slopes",
		"minecraft:jagged_peaks", "minecraft:frozen_peaks", "minecraft:stony_peaks",
		"minecraft:old_growth_pine_taiga", "minecraft:old_growth_spruce_taiga",
		"minecraft:taiga", "minecraft:windswept_hills", "minecraft:windswept_gravelly_hills",
		"minecraft:windswept_forest", "minecraft:frozen_river", "minecraft:frozen_ocean",
		"minecraft:deep_frozen_ocean", "minecraft:cold_ocean", "minecraft:deep_cold_ocean":
		return FarmAnimalTempCold
	default:
		return FarmAnimalTempTemperate
	}
}

func normalizeTempVariant(variant string) string {
	switch canonicalResourceName(variant) {
	case FarmAnimalTempWarm:
		return FarmAnimalTempWarm
	case FarmAnimalTempCold:
		return FarmAnimalTempCold
	default:
		return FarmAnimalTempTemperate
	}
}

// CowVariant returns the 26.1.2 temperature variant (`temperate`, `warm`, `cold`) for a cow.
func (s *Server) CowVariant(entityID int32) string {
	if s == nil || entityID <= 0 {
		return FarmAnimalTempTemperate
	}
	st := s.ensureFarmVariantState(entityID, MobTypeCow)
	fm := s.farmPolish()
	fm.mu.RLock()
	v := st.tempVariant
	fm.mu.RUnlock()
	return v
}

// SetCowVariant sets the 26.1.2 temperature variant (`temperate`, `warm`, `cold`) for a cow.
func (s *Server) SetCowVariant(entityID int32, variant string) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	st := s.ensureFarmVariantState(entityID, MobTypeCow)
	fm := s.farmPolish()
	fm.mu.Lock()
	st.tempVariant = normalizeTempVariant(variant)
	fm.mu.Unlock()
	return true
}

// CowSoundVariant returns the 26.1.2 sound variant (`classic` or `moody`) for a cow.
func (s *Server) CowSoundVariant(entityID int32) string {
	if s == nil || entityID <= 0 {
		return CowSoundVariantClassic
	}
	st := s.ensureFarmVariantState(entityID, MobTypeCow)
	fm := s.farmPolish()
	fm.mu.RLock()
	v := st.soundVariant
	fm.mu.RUnlock()
	return v
}

// SetCowSoundVariant sets the 26.1.2 sound variant (`classic` or `moody`) for a cow.
func (s *Server) SetCowSoundVariant(entityID int32, soundVariant string) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	norm := canonicalResourceName(soundVariant)
	if norm != CowSoundVariantMoody {
		norm = CowSoundVariantClassic
	}
	st := s.ensureFarmVariantState(entityID, MobTypeCow)
	fm := s.farmPolish()
	fm.mu.Lock()
	st.soundVariant = norm
	fm.mu.Unlock()
	return true
}

// RollCowDeathLoot rolls vanilla 26.1.2 `entities/cow.json` death loot:
//   - `0..2` `minecraft:leather` (+ `0..lootingLevel`)
//   - `1..3` `minecraft:beef` (or `minecraft:cooked_beef` if `onFire`) (+ `0..lootingLevel`).
func RollCowDeathLoot(rng *rand.Rand, lootingLevel int, onFire bool) []world.ItemStack {
	var out []world.ItemStack
	leatherCount := int32(1)
	if rng != nil {
		leatherCount = int32(rng.Intn(3))
		if lootingLevel > 0 {
			leatherCount += int32(rng.Intn(lootingLevel + 1))
		}
	}
	if leatherCount > 0 {
		if s1 := world.NewNamedItemStack("minecraft:leather", leatherCount); !s1.IsEmpty() {
			out = append(out, s1)
		}
	}
	beefCount := int32(1)
	if rng != nil {
		beefCount = int32(1 + rng.Intn(3))
		if lootingLevel > 0 {
			beefCount += int32(rng.Intn(lootingLevel + 1))
		}
	}
	meatID := "minecraft:beef"
	if onFire {
		meatID = "minecraft:cooked_beef"
	}
	if s2 := world.NewNamedItemStack(meatID, beefCount); !s2.IsEmpty() {
		out = append(out, s2)
	}
	return out
}

// ChickenVariant returns the 26.1.2 temperature variant (`temperate`, `warm`, `cold`) for a chicken.
func (s *Server) ChickenVariant(entityID int32) string {
	if s == nil || entityID <= 0 {
		return FarmAnimalTempTemperate
	}
	st := s.ensureFarmVariantState(entityID, MobTypeChicken)
	fm := s.farmPolish()
	fm.mu.RLock()
	v := st.tempVariant
	fm.mu.RUnlock()
	return v
}

// SetChickenVariant sets the 26.1.2 temperature variant (`temperate`, `warm`, `cold`) for a chicken.
func (s *Server) SetChickenVariant(entityID int32, variant string) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	st := s.ensureFarmVariantState(entityID, MobTypeChicken)
	fm := s.farmPolish()
	fm.mu.Lock()
	st.tempVariant = normalizeTempVariant(variant)
	fm.mu.Unlock()
	return true
}

// ChickenSoundVariant returns the 26.1.2 sound variant (`classic` or `picky`) for a chicken.
func (s *Server) ChickenSoundVariant(entityID int32) string {
	if s == nil || entityID <= 0 {
		return ChickenSoundVariantClassic
	}
	st := s.ensureFarmVariantState(entityID, MobTypeChicken)
	fm := s.farmPolish()
	fm.mu.RLock()
	v := st.soundVariant
	fm.mu.RUnlock()
	return v
}

// SetChickenSoundVariant sets the 26.1.2 sound variant (`classic` or `picky`) for a chicken.
func (s *Server) SetChickenSoundVariant(entityID int32, soundVariant string) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	norm := canonicalResourceName(soundVariant)
	if norm != ChickenSoundVariantPicky {
		norm = ChickenSoundVariantClassic
	}
	st := s.ensureFarmVariantState(entityID, MobTypeChicken)
	fm := s.farmPolish()
	fm.mu.Lock()
	st.soundVariant = norm
	fm.mu.Unlock()
	return true
}

// ChickenEggItemForVariant implements `data/minecraft/loot_table/gameplay/chicken_lay.json`
// in Minecraft 26.1.2:
//   - `minecraft:temperate` -> `minecraft:egg`
//   - `minecraft:warm`      -> `minecraft:brown_egg`
//   - `minecraft:cold`      -> `minecraft:blue_egg`
func ChickenEggItemForVariant(variant string) string {
	switch canonicalResourceName(variant) {
	case FarmAnimalTempWarm:
		return BrownEggItemID
	case FarmAnimalTempCold:
		return BlueEggItemID
	default:
		return EggItemID
	}
}

// SetChickenJockey marks whether a chicken entity is a chicken jockey (`isChickenJockey`).
// Chicken jockeys do not lay eggs, grant 10 XP on death, and can despawn when far away.
func (s *Server) SetChickenJockey(entityID int32, isJockey bool) {
	if s == nil || entityID <= 0 {
		return
	}
	st := s.ensureFarmVariantState(entityID, MobTypeChicken)
	fm := s.farmPolish()
	fm.mu.Lock()
	st.chickenJockey = isJockey
	fm.mu.Unlock()
}

// IsChickenJockey reports whether a chicken entity is a chicken jockey.
func (s *Server) IsChickenJockey(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	st := s.ensureFarmVariantState(entityID, MobTypeChicken)
	fm := s.farmPolish()
	fm.mu.RLock()
	v := st.chickenJockey
	fm.mu.RUnlock()
	return v
}

// ChickenXPReward returns `Chicken.getBaseExperienceReward` in vanilla 26.1.2:
// `10` if `IsChickenJockey`, `0` if baby, or `2` otherwise.
func (s *Server) ChickenXPReward(entityID int32, isBaby bool) int {
	if s != nil && s.IsChickenJockey(entityID) {
		return ChickenJockeyXPValue
	}
	if isBaby {
		return 0
	}
	return 2
}

// ChickenCalculateFlapping implements `Chicken.aiStep` slow-falling (`vy * 0.6`
// when airborne and falling) and 0 fall damage.
func ChickenCalculateFlapping(vy float64, onGround bool) (nextVY float64, fallDamage float32) {
	return ParrotCalculateFlapping(vy, onGround)
}

// RollChickenDeathLoot rolls vanilla 26.1.2 `entities/chicken.json` death loot:
//   - `0..2` `minecraft:feather` (+ `0..lootingLevel`)
//   - `1` `minecraft:chicken` (or `minecraft:cooked_chicken` if `onFire`) (+ `0..lootingLevel`).
func RollChickenDeathLoot(rng *rand.Rand, lootingLevel int, onFire bool) []world.ItemStack {
	var out []world.ItemStack
	featherCount := int32(1)
	if rng != nil {
		featherCount = int32(rng.Intn(3))
		if lootingLevel > 0 {
			featherCount += int32(rng.Intn(lootingLevel + 1))
		}
	}
	if featherCount > 0 {
		if s1 := world.NewNamedItemStack("minecraft:feather", featherCount); !s1.IsEmpty() {
			out = append(out, s1)
		}
	}
	meatCount := int32(1)
	if rng != nil && lootingLevel > 0 {
		meatCount += int32(rng.Intn(lootingLevel + 1))
	}
	meatID := "minecraft:chicken"
	if onFire {
		meatID = "minecraft:cooked_chicken"
	}
	if s2 := world.NewNamedItemStack(meatID, meatCount); !s2.IsEmpty() {
		out = append(out, s2)
	}
	return out
}

// PigVariant returns the 26.1.2 temperature variant (`temperate`, `warm`, `cold`) for a pig.
func (s *Server) PigVariant(entityID int32) string {
	if s == nil || entityID <= 0 {
		return FarmAnimalTempTemperate
	}
	st := s.ensureFarmVariantState(entityID, MobTypePig)
	fm := s.farmPolish()
	fm.mu.RLock()
	v := st.tempVariant
	fm.mu.RUnlock()
	return v
}

// SetPigVariant sets the 26.1.2 temperature variant (`temperate`, `warm`, `cold`) for a pig.
func (s *Server) SetPigVariant(entityID int32, variant string) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	st := s.ensureFarmVariantState(entityID, MobTypePig)
	fm := s.farmPolish()
	fm.mu.Lock()
	st.tempVariant = normalizeTempVariant(variant)
	fm.mu.Unlock()
	return true
}

// PigSoundVariant returns the 26.1.2 sound variant (`classic`, `mini`, `big`) for a pig.
func (s *Server) PigSoundVariant(entityID int32) string {
	if s == nil || entityID <= 0 {
		return PigSoundVariantClassic
	}
	st := s.ensureFarmVariantState(entityID, MobTypePig)
	fm := s.farmPolish()
	fm.mu.RLock()
	v := st.soundVariant
	fm.mu.RUnlock()
	return v
}

// SetPigSoundVariant sets the 26.1.2 sound variant (`classic`, `mini`, `big`) for a pig.
func (s *Server) SetPigSoundVariant(entityID int32, soundVariant string) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	norm := canonicalResourceName(soundVariant)
	if norm != PigSoundVariantMini && norm != PigSoundVariantBig {
		norm = PigSoundVariantClassic
	}
	st := s.ensureFarmVariantState(entityID, MobTypePig)
	fm := s.farmPolish()
	fm.mu.Lock()
	st.soundVariant = norm
	fm.mu.Unlock()
	return true
}

// EncodePigBoostDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating `Pig.DATA_BOOST_TIME` (index 17, `INT` serializer).
func EncodePigBoostDataPacket(entityID, boostTimeTotal int32) []byte {
	w := protocol.NewWriter(14)
	w.VarInt(entityID)
	w.Byte(EntityDataPigBoostTimeID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(boostTimeTotal)
	w.Byte(0xFF)
	return w.Bytes()
}

// IsPigSaddled reports whether a pig entity is currently saddled.
func (s *Server) IsPigSaddled(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	st := s.ensureFarmVariantState(entityID, MobTypePig)
	fm := s.farmPolish()
	fm.mu.RLock()
	v := st.pigSaddled
	fm.mu.RUnlock()
	return v
}

// SetPigSaddled sets whether a pig entity has a saddle equipped.
func (s *Server) SetPigSaddled(entityID int32, saddled bool) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	st := s.ensureFarmVariantState(entityID, MobTypePig)
	fm := s.farmPolish()
	fm.mu.Lock()
	st.pigSaddled = saddled
	fm.mu.Unlock()
	return true
}

// EquipPigSaddle equips a `minecraft:saddle` onto an adult, unsaddled pig and
// plays `minecraft:entity.pig.saddle`.
func (s *Server) EquipPigSaddle(session *PlayerSession, entityID int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 || s.IsMobBaby(entityID) {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypePig || s.IsPigSaddled(entityID) {
		return false
	}
	s.SetPigSaddled(entityID, true)
	if session != nil {
		consumeOneHeldItemIfSurvival(session)
	}
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundPigSaddle, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 0.5, 1.0, 0))
	return true
}

// BoostPig implements `ItemBasedSteering.boost` when a rider uses a
// `minecraft:carrot_on_a_stick` on a saddled pig in vanilla 26.1.2:
//   - Sets `boostTimeTotal = 140 + rng.Intn(841)` (`140..980` ticks).
//   - Broadcasts `EncodePigBoostDataPacket(entityID, boostTimeTotal)`.
func (s *Server) BoostPig(session *PlayerSession, entityID int32, rng *rand.Rand) (boostDurationTicks int, ok bool) {
	if s == nil || s.entities == nil || entityID <= 0 || !s.IsPigSaddled(entityID) {
		return 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypePig {
		return 0, false
	}
	st := s.ensureFarmVariantState(entityID, MobTypePig)
	fm := s.farmPolish()
	fm.mu.Lock()
	if st.pigBoostTotal > 0 && st.pigBoostTime < st.pigBoostTotal {
		fm.mu.Unlock()
		return 0, false
	}
	total := PigBoostMinTicks + 420
	if rng != nil {
		total = PigBoostMinTicks + rng.Intn(PigBoostRandomRangeTicks)
	}
	st.pigBoostTime = 0
	st.pigBoostTotal = total
	fm.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodePigBoostDataPacket(entityID, int32(total)))
	return total, true
}

// TickPigBoost advances `ItemBasedSteering.tickBoost()` by 1 tick for a pig entity.
func (s *Server) TickPigBoost(entityID int32) (boostTime, boostTimeTotal int, active bool) {
	if s == nil || entityID <= 0 {
		return 0, 0, false
	}
	st := s.ensureFarmVariantState(entityID, MobTypePig)
	fm := s.farmPolish()
	fm.mu.Lock()
	if st.pigBoostTotal <= 0 {
		fm.mu.Unlock()
		return 0, 0, false
	}
	st.pigBoostTime++
	if st.pigBoostTime >= st.pigBoostTotal {
		st.pigBoostTime = 0
		st.pigBoostTotal = 0
		fm.mu.Unlock()
		return 0, 0, false
	}
	boostTime = st.pigBoostTime
	boostTimeTotal = st.pigBoostTotal
	fm.mu.Unlock()
	return boostTime, boostTimeTotal, true
}

// PigBoostFactor computes `ItemBasedSteering.boostFactor()` in vanilla 26.1.2:
// `1.0 + 1.15 * sin(float64(boostTime) / float64(boostTimeTotal) * pi)` when boosting,
// or `1.0` when not boosting.
func PigBoostFactor(boostTime, boostTimeTotal int) float64 {
	if boostTime <= 0 || boostTimeTotal <= 0 || boostTime >= boostTimeTotal {
		return 1.0
	}
	return 1.0 + PigBoostMaxExtraFactor*math.Sin((float64(boostTime)/float64(boostTimeTotal))*math.Pi)
}

// PigRiddenSpeed computes `Pig.getRiddenSpeed(player)` in vanilla 26.1.2:
// `movementSpeed * 0.225 * PigBoostFactor(boostTime, boostTimeTotal)`.
func PigRiddenSpeed(movementSpeed float64, boostTime, boostTimeTotal int) float64 {
	return movementSpeed * PigRiddenBaseSpeedFactor * PigBoostFactor(boostTime, boostTimeTotal)
}

// OnPigStruckByLightning implements `Pig.thunderHit` in vanilla 26.1.2:
// when difficulty is not Peaceful, converts the pig into a `minecraft:zombified_piglin`
// armed with a `minecraft:golden_sword`.
func (s *Server) OnPigStruckByLightning(entityID int32) (zombifiedPiglinID int32, converted bool) {
	if s == nil || s.entities == nil || entityID <= 0 || s.Difficulty() == DifficultyPeaceful {
		return 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypePig {
		return 0, false
	}
	dim := ent.EffectiveDimension()
	x, y, z := ent.X, ent.Y, ent.Z
	yaw, pitch := ent.Yaw, ent.Pitch
	isBaby := s.IsMobBaby(entityID)
	cp := ent.ChunkPos()

	s.entities.Remove(entityID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{entityID}))

	zpID, err := s.SpawnZombifiedPiglin(dim, x, y, z, isBaby)
	if err != nil {
		return 0, false
	}
	s.entities.Update(zpID, func(e *world.Entity) {
		e.Yaw = yaw
		e.Pitch = pitch
	})
	return zpID, true
}

// RollPigDeathLoot rolls vanilla 26.1.2 `entities/pig.json` death loot:
//   - `1..3` `minecraft:porkchop` (or `minecraft:cooked_porkchop` if `onFire`) (+ `0..lootingLevel`)
//   - `1` `minecraft:saddle` if `saddled`.
func RollPigDeathLoot(rng *rand.Rand, lootingLevel int, onFire, saddled bool) []world.ItemStack {
	var out []world.ItemStack
	count := int32(1)
	if rng != nil {
		count = int32(1 + rng.Intn(3))
		if lootingLevel > 0 {
			count += int32(rng.Intn(lootingLevel + 1))
		}
	}
	meatID := "minecraft:porkchop"
	if onFire {
		meatID = "minecraft:cooked_porkchop"
	}
	if s1 := world.NewNamedItemStack(meatID, count); !s1.IsEmpty() {
		out = append(out, s1)
	}
	if saddled {
		if s2 := world.NewNamedItemStack("minecraft:saddle", 1); !s2.IsEmpty() {
			out = append(out, s2)
		}
	}
	return out
}

func consumeOneHeldItemIfSurvival(session *PlayerSession) {
	if session == nil {
		return
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeCreative {
		return
	}
	held := session.HeldItem()
	if held.IsEmpty() {
		return
	}
	slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
	rem := held.Count - 1
	if rem <= 0 {
		session.SetInventorySlot(slotIdx, world.ItemStack{})
	} else {
		session.SetInventorySlot(slotIdx, world.NewItemStack(held.ResolvedItemID(), rem))
	}
}

func replaceOrAddFilledContainerInHand(session *PlayerSession, filled world.ItemStack) {
	if session == nil || filled.IsEmpty() {
		return
	}
	snap := session.Snapshot()
	held := session.HeldItem()
	slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
	if snap.GameMode != GameModeCreative {
		if held.Count <= 1 {
			session.SetInventorySlot(slotIdx, filled)
		} else {
			session.SetInventorySlot(slotIdx, world.NewItemStack(held.ResolvedItemID(), held.Count-1))
			_, _ = session.AddItemToInventory(filled)
		}
	} else {
		_, _ = session.AddItemToInventory(filled)
	}
}
