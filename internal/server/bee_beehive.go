package server

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

const (
	// MobTypeBee is the canonical 26.1.2 entity identifier for bees (`minecraft:bee`, ID 11).
	MobTypeBee = "minecraft:bee"

	// EntityTypeBeeID is the protocol 775 numeric entity type ID for `minecraft:bee`.
	EntityTypeBeeID int = 11

	// DefaultBeeMaxHealth is the vanilla 26.1.2 max health of a bee (10 HP = 5 hearts).
	DefaultBeeMaxHealth float32 = 10.0

	// BeeCollisionWidth and BeeCollisionHeight are the vanilla adult bee AABB dimensions.
	BeeCollisionWidth  = 0.7
	BeeCollisionHeight = 0.6

	// Bee SynchedEntityData metadata indices (protocol 775 / MC 26.1.2):
	//  - Index 16 (AgeableMob DATA_BABY_ID, boolean): is baby
	//  - Index 17 (Bee DATA_FLAGS_ID, byte): 0x02 = isRolling, 0x04 = hasStung, 0x08 = hasNectar
	//  - Index 18 (Bee DATA_REMAINING_ANGER_TIME_ID, varint): anger time in ticks
	BeeMetaIndexBaby      byte = 16
	BeeMetaIndexFlags     byte = 17
	BeeMetaIndexAngerTime byte = 18

	BeeFlagIsRolling byte = 0x02
	BeeFlagHasStung  byte = 0x04
	BeeFlagHasNectar byte = 0x08

	// Pollination & hive constants (vanilla 26.1.2).
	BeePollinateTargetTicks        = 400  // 20 seconds of flower pollination
	BeePollinateMaxDurationTicks   = 600  // max 30 seconds per pollination attempt
	BeePollinateCooldownTicks      = 200  // cooldown between flower checks
	BeeMaxCropsGrownPerPollination = 10   // max crops fertilized while returning with nectar
	BeeCropGrowChance              = 0.05 // 5% chance per tick when flying over a crop with nectar
	BeeMaxHiveOccupants            = 3    // max 3 bees inside a beehive or bee_nest
	BeeMinOccupationTicksNectar    = 2400 // 2 minutes inside hive when entering with nectar
	BeeMinOccupationTicksNoNectar  = 600  // 30 seconds inside hive when entering without nectar
	BeeMaxHoneyLevel               = 5    // honey_level 0..5
	BeeHiveSearchRadius            = 22   // search radius for flowers and hives
	BeeCampfireSmokeMaxDistance    = 5    // campfire up to 5 blocks below calms hive

	// Combat, sting, poison, and post-sting death constants (vanilla 26.1.2).
	BeeBaseAttackDamage          float32 = 2.0
	BeeSwarmAlertRadius          float64 = 22.0
	BeeAngerMinTicks                     = 400  // 20 seconds
	BeeAngerMaxTicks                     = 800  // 40 seconds
	BeePostStingDeathMinTicks            = 1000 // 50 seconds after losing stinger
	BeePostStingDeathMaxTicks            = 1200 // 60 seconds after losing stinger
	BeePoisonDurationNormalTicks         = 200  // 10s Poison I on Normal
	BeePoisonDurationHardTicks           = 360  // 18s Poison I on Hard
	BeeFlySpeed                  float64 = 0.24
	BeeAngryFlySpeed             float64 = 0.32
	BeeAttackReach               float64 = 1.6

	// Bee & Beehive sound effects.
	SoundBeeLoop           = "minecraft:entity.bee.loop"
	SoundBeeLoopAggressive = "minecraft:entity.bee.loop_aggressive"
	SoundBeeSting          = "minecraft:entity.bee.sting"
	SoundBeeDeath          = "minecraft:entity.bee.death"
	SoundBeeHurt           = "minecraft:entity.bee.hurt"
	SoundBeePollinate      = "minecraft:entity.bee.pollinate"
	SoundBeehiveEnter      = "minecraft:block.beehive.enter"
	SoundBeehiveExit       = "minecraft:block.beehive.exit"
	SoundBeehiveShear      = "minecraft:block.beehive.shear"
	SoundBeehiveWork       = "minecraft:block.beehive.work"
	SoundHoneyBottleDrink  = "minecraft:item.honey_bottle.drink"

	// Advancements for bees & beehives.
	AdvancementSafelyHarvestHoney = "minecraft:husbandry/safely_harvest_honey"
	CriterionSafelyHarvestHoney   = "safely_harvest_honey"
	AdvancementSilkTouchNest      = "minecraft:husbandry/silk_touch_nest"
	CriterionSilkTouchNest        = "silk_touch_nest"
)

var registerBeeAdvancementsOnce sync.Once

func ensureBeeAdvancementsRegistered() {
	registerBeeAdvancementsOnce.Do(func() {
		reg := DefaultAdvancementRegistry()
		if reg == nil {
			return
		}
		if _, ok := reg.ByID(AdvancementSafelyHarvestHoney); !ok {
			def := &AdvancementDef{
				ID:       AdvancementSafelyHarvestHoney,
				ParentID: "minecraft:husbandry/root",
				Criteria: []string{CriterionSafelyHarvestHoney},
				Requirements: [][]string{
					{CriterionSafelyHarvestHoney},
				},
			}
			reg.defs = append(reg.defs, def)
			reg.byID[AdvancementSafelyHarvestHoney] = def
		}
		if _, ok := reg.ByID(AdvancementSilkTouchNest); !ok {
			def := &AdvancementDef{
				ID:       AdvancementSilkTouchNest,
				ParentID: "minecraft:husbandry/root",
				Criteria: []string{CriterionSilkTouchNest},
				Requirements: [][]string{
					{CriterionSilkTouchNest},
				},
			}
			reg.defs = append(reg.defs, def)
			reg.byID[AdvancementSilkTouchNest] = def
		}
	})
}

type beeHivePosKey struct {
	Dimension string
	X, Y, Z   int
}

// BeeState holds per-entity state for a `minecraft:bee` (ID 11).
type BeeState struct {
	EntityID                   int32
	HasNectar                  bool
	HasStung                   bool
	IsRolling                  bool
	AngerTicks                 int
	TargetSession              *PlayerSession
	TargetMobID                int32
	AttackCooldown             int
	TimeSinceSting             int
	DeathAfterStingTicks       int
	HiveDimension              string
	HasHivePos                 bool
	HiveX, HiveY, HiveZ        int
	HasFlowerPos               bool
	FlowerX, FlowerY, FlowerZ  int
	PollinatingTicks           int
	PollinateCooldown          int
	CropsGrownSincePollination int
	StayOutOfHiveCountdown     int
	CannotEnterHiveTicks       int
}

// BeehiveOccupant represents a single bee stored inside a `minecraft:beehive` or `minecraft:bee_nest`.
type BeehiveOccupant struct {
	TypeName           string  `json:"id"`
	TicksInHive        int     `json:"ticks_in_hive"`
	MinOccupationTicks int     `json:"min_occupation_ticks"`
	HasNectar          bool    `json:"has_nectar"`
	Health             float32 `json:"health,omitempty"`
	IsBaby             bool    `json:"is_baby,omitempty"`
	AgeTicks           int     `json:"age_ticks,omitempty"`
	CustomName         string  `json:"custom_name,omitempty"`
}

// BeehiveBlockEntity stores the server-side block entity state for a `minecraft:beehive` or `minecraft:bee_nest`.
type BeehiveBlockEntity struct {
	Dimension string
	X, Y, Z   int
	BlockName string
	Occupants []BeehiveOccupant
	FlowerPos *[3]int
}

// BeeStateManager manages active `minecraft:bee` entities and `minecraft:beehive` / `minecraft:bee_nest` block entities.
type BeeStateManager struct {
	mu          sync.RWMutex
	bees        map[int32]*BeeState
	hives       map[beeHivePosKey]*BeehiveBlockEntity
	droppedMeta map[string]string
}

var (
	beeManagersMu sync.Mutex
	beeManagers   = make(map[*Server]*BeeStateManager)
)

// BeeStates returns the server's BeeStateManager.
func (s *Server) BeeStates() *BeeStateManager {
	if s == nil {
		return nil
	}
	beeManagersMu.Lock()
	defer beeManagersMu.Unlock()
	mgr := beeManagers[s]
	if mgr == nil {
		mgr = &BeeStateManager{
			bees:        make(map[int32]*BeeState),
			hives:       make(map[beeHivePosKey]*BeehiveBlockEntity),
			droppedMeta: make(map[string]string),
		}
		beeManagers[s] = mgr
	}
	return mgr
}

// BeeStateFor returns a snapshot of the bee state for entityID.
func (s *Server) BeeStateFor(entityID int32) (BeeState, bool) {
	mgr := s.BeeStates()
	if mgr == nil {
		return BeeState{}, false
	}
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	st, ok := mgr.bees[entityID]
	if !ok || st == nil {
		return BeeState{}, false
	}
	return *st, true
}

func (s *Server) ensureBeeState(entityID int32) *BeeState {
	mgr := s.BeeStates()
	if mgr == nil {
		return nil
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.bees[entityID]
	if !ok || st == nil {
		st = &BeeState{
			EntityID:             entityID,
			DeathAfterStingTicks: 1100,
		}
		mgr.bees[entityID] = st
	}
	return st
}

// RemoveBeeState removes tracking for a despawned or dead bee.
func (s *Server) RemoveBeeState(entityID int32) {
	mgr := s.BeeStates()
	if mgr == nil {
		return
	}
	mgr.mu.Lock()
	delete(mgr.bees, entityID)
	mgr.mu.Unlock()
}

// SetBeeHasNectar sets whether a bee is currently carrying nectar and broadcasts its metadata.
func (s *Server) SetBeeHasNectar(entityID int32, hasNectar bool) {
	if s == nil {
		return
	}
	st := s.ensureBeeState(entityID)
	if st == nil {
		return
	}
	mgr := s.BeeStates()
	mgr.mu.Lock()
	st.HasNectar = hasNectar
	if hasNectar {
		st.CropsGrownSincePollination = 0
	}
	mgr.mu.Unlock()
	if s.entities != nil {
		if ent := s.entities.Get(entityID); ent != nil {
			s.broadcastBeeMetadata(ent)
		}
	}
}

// EncodeBeeDataPacket builds the ClientboundSetEntityDataPacket (0x63) payload for a `minecraft:bee` (ID 11).
func EncodeBeeDataPacket(entityID int32, isBaby bool, st BeeState) []byte {
	var flags byte
	if st.IsRolling {
		flags |= BeeFlagIsRolling
	}
	if st.HasStung {
		flags |= BeeFlagHasStung
	}
	if st.HasNectar {
		flags |= BeeFlagHasNectar
	}
	anger := st.AngerTicks
	if anger < 0 {
		anger = 0
	}
	w := protocol.NewWriter(24)
	w.VarInt(entityID)
	w.Byte(BeeMetaIndexBaby)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(isBaby)
	w.Byte(BeeMetaIndexFlags)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)
	w.Byte(BeeMetaIndexAngerTime)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(int32(anger))
	w.Byte(0xFF)
	return w.Bytes()
}

func (s *Server) broadcastBeeMetadata(ent *world.Entity) {
	if s == nil || ent == nil {
		return
	}
	st, _ := s.BeeStateFor(ent.ID)
	isBaby := s.IsMobBaby(ent.ID)
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeBeeDataPacket(ent.ID, isBaby, st))
}

// SpawnBee spawns a `minecraft:bee` (ID 11) in dimension at (x, y, z).
func (s *Server) SpawnBee(dimension string, x, y, z float64, isBaby bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, fmt.Errorf("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeBee)
	if typeID < 0 {
		typeID = EntityTypeBeeID
	}
	entityID := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeBee,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  false,
	})
	s.ensureBeeState(entityID)

	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(entityID, tr.aiTickID)
	mem.healthInit = true
	mem.health = DefaultBeeMaxHealth
	mem.lastHealth = DefaultBeeMaxHealth
	if isBaby {
		mem.ageTicks = BabyMobGrowthTicks
	}
	tr.mu.Unlock()

	if ent := s.entities.Get(entityID); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.broadcastBeeMetadata(ent)
	}
	return entityID, nil
}

// IsBeehiveOrNestBlockState reports whether state is `minecraft:beehive` or `minecraft:bee_nest`.
func IsBeehiveOrNestBlockState(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	return IsBeehiveOrNestBlock(world.BlockName(state))
}

// IsBeehiveOrNestBlock returns true if blockStateName is `minecraft:beehive` or `minecraft:bee_nest`.
func IsBeehiveOrNestBlock(stateName string) bool {
	base := canonicalResourceName(strings.SplitN(stateName, "[", 2)[0])
	return base == "minecraft:beehive" || base == "minecraft:bee_nest"
}

// IsBeeFlowerBlockState reports whether state is a flower or flowering block that bees can pollinate in 26.1.2.
func IsBeeFlowerBlockState(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	return IsBeeFlowerBlock(world.BlockName(state))
}

// IsBeeFlowerBlock returns true if stateName is a flower or flowering block that bees can pollinate in 26.1.2.
func IsBeeFlowerBlock(stateName string) bool {
	base := canonicalResourceName(strings.SplitN(stateName, "[", 2)[0])
	switch base {
	case "minecraft:dandelion",
		"minecraft:poppy",
		"minecraft:blue_orchid",
		"minecraft:allium",
		"minecraft:azure_bluet",
		"minecraft:red_tulip",
		"minecraft:orange_tulip",
		"minecraft:white_tulip",
		"minecraft:pink_tulip",
		"minecraft:oxeye_daisy",
		"minecraft:cornflower",
		"minecraft:lily_of_the_valley",
		"minecraft:wither_rose",
		"minecraft:torchflower",
		"minecraft:closed_eyeblossom",
		"minecraft:open_eyeblossom",
		"minecraft:sunflower",
		"minecraft:lilac",
		"minecraft:rose_bush",
		"minecraft:peony",
		"minecraft:pitcher_plant",
		"minecraft:pink_petals",
		"minecraft:wildflowers",
		"minecraft:cactus_flower",
		"minecraft:flowering_azalea",
		"minecraft:flowering_azalea_leaves",
		"minecraft:mangrove_propagule",
		"minecraft:cherry_leaves",
		"minecraft:spore_blossom",
		"minecraft:chorus_flower":
		return true
	}
	return false
}

// IsBeeBreedItem returns true if itemName is a flower item that can breed bees and accelerate baby bee growth.
func IsBeeBreedItem(itemName string) bool {
	return IsBeeFlowerBlock(canonicalResourceName(itemName))
}

// IsBeeFertilizableCropState checks whether block state can be fertilized (+1 age) by a bee carrying nectar.
func IsBeeFertilizableCropState(state uint16) (nextState uint16, canGrow bool) {
	if state == world.StateAir {
		return 0, false
	}
	base := canonicalResourceName(world.BlockName(state))
	props := world.BlockProperties(state)
	switch base {
	case "minecraft:wheat", "minecraft:carrots", "minecraft:potatoes",
		"minecraft:melon_stem", "minecraft:pumpkin_stem":
		age, _ := strconv.Atoi(props["age"])
		if age < 7 {
			return world.WithBlockProperty(state, "age", strconv.Itoa(age+1))
		}
	case "minecraft:beetroots", "minecraft:torchflower_crop", "minecraft:sweet_berry_bush":
		age, _ := strconv.Atoi(props["age"])
		if age < 3 {
			return world.WithBlockProperty(state, "age", strconv.Itoa(age+1))
		}
	case "minecraft:pitcher_crop":
		age, _ := strconv.Atoi(props["age"])
		if age < 4 {
			return world.WithBlockProperty(state, "age", strconv.Itoa(age+1))
		}
	case "minecraft:cave_vines", "minecraft:cave_vines_plant":
		if props["berries"] != "true" {
			return world.WithBlockProperty(state, "berries", "true")
		}
	}
	return 0, false
}

// BeehiveHoneyLevelAt returns the `honey_level` (0..5) of a beehive or bee_nest at (x, y, z) in dimension.
func (s *Server) BeehiveHoneyLevelAt(dimension string, x, y, z int) int {
	if s == nil {
		return 0
	}
	cache := s.DimensionChunks(world.NormalizeDimension(dimension))
	if cache == nil {
		return 0
	}
	return BeehiveHoneyLevelState(cache.GetBlock(x, y, z))
}

// BeehiveHoneyLevelState parses the `honey_level` property (0..5) from a beehive or bee_nest block state ID.
func BeehiveHoneyLevelState(state uint16) int {
	if !IsBeehiveOrNestBlockState(state) {
		return 0
	}
	props := world.BlockProperties(state)
	lvl, err := strconv.Atoi(props["honey_level"])
	if err != nil {
		return 0
	}
	if lvl < 0 {
		return 0
	}
	if lvl > BeeMaxHoneyLevel {
		return BeeMaxHoneyLevel
	}
	return lvl
}

// SetBeehiveHoneyLevel updates the `honey_level` property (0..5) of a beehive or bee_nest at (x, y, z)
// and notifies adjacent comparators (comparator output = honey_level).
func (s *Server) SetBeehiveHoneyLevel(dimension string, x, y, z, honeyLevel int) bool {
	if s == nil {
		return false
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return false
	}
	curState := cache.GetBlock(x, y, z)
	if !IsBeehiveOrNestBlockState(curState) {
		return false
	}
	if honeyLevel < 0 {
		honeyLevel = 0
	}
	if honeyLevel > BeeMaxHoneyLevel {
		honeyLevel = BeeMaxHoneyLevel
	}
	nextState, ok := world.WithBlockProperty(curState, "honey_level", strconv.Itoa(honeyLevel))
	if !ok {
		return false
	}
	s.setDispenserWorldBlock(normDim, cache, x, y, z, nextState)
	s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
	return true
}

// ComparatorOutputForBeehive returns the redstone comparator signal (0..5) for a beehive or bee_nest.
func (s *Server) ComparatorOutputForBeehive(dimension string, x, y, z int) (int, bool) {
	if s == nil {
		return 0, false
	}
	cache := s.DimensionChunks(world.NormalizeDimension(dimension))
	if cache == nil {
		return 0, false
	}
	st := cache.GetBlock(x, y, z)
	if !IsBeehiveOrNestBlockState(st) {
		return 0, false
	}
	return BeehiveHoneyLevelState(st), true
}

// IsBeehiveSmokedByCampfire returns true if there is a lit `minecraft:campfire` or `minecraft:soul_campfire`
// within 5 blocks directly below (x, y, z) without an obstructing solid block blocking smoke.
func (s *Server) IsBeehiveSmokedByCampfire(dimension string, x, y, z int) bool {
	if s == nil {
		return false
	}
	cache := s.DimensionChunks(world.NormalizeDimension(dimension))
	if cache == nil {
		return false
	}
	for dy := 1; dy <= BeeCampfireSmokeMaxDistance; dy++ {
		by := y - dy
		if by < world.MinY {
			break
		}
		st := cache.GetBlock(x, by, z)
		if st == world.StateAir {
			continue
		}
		base := canonicalResourceName(world.BlockName(st))
		if base == "minecraft:campfire" || base == "minecraft:soul_campfire" {
			props := world.BlockProperties(st)
			return props["lit"] != "false"
		}
		if base == "minecraft:hay_block" {
			continue
		}
		if !isPassableBlock(st) {
			return false
		}
	}
	return false
}

// GetOrCreateBeehive returns (or creates) the BeehiveBlockEntity for a beehive/bee_nest at (x, y, z).
func (s *Server) GetOrCreateBeehive(dimension string, x, y, z int) *BeehiveBlockEntity {
	mgr := s.BeeStates()
	if mgr == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dimension)
	key := beeHivePosKey{Dimension: normDim, X: x, Y: y, Z: z}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	be := mgr.hives[key]
	if be == nil {
		blockName := "minecraft:beehive"
		if cache := s.DimensionChunks(normDim); cache != nil {
			if st := cache.GetBlock(x, y, z); IsBeehiveOrNestBlockState(st) {
				blockName = canonicalResourceName(world.BlockName(st))
			}
		}
		be = &BeehiveBlockEntity{
			Dimension: normDim,
			X:         x,
			Y:         y,
			Z:         z,
			BlockName: blockName,
		}
		mgr.hives[key] = be
	}
	return be
}

// BeehiveOccupantCount returns the number of bees currently stored in the hive at (x, y, z).
func (s *Server) BeehiveOccupantCount(dimension string, x, y, z int) int {
	mgr := s.BeeStates()
	if mgr == nil {
		return 0
	}
	key := beeHivePosKey{Dimension: world.NormalizeDimension(dimension), X: x, Y: y, Z: z}
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	be := mgr.hives[key]
	if be == nil {
		return 0
	}
	return len(be.Occupants)
}

// BeehiveOccupants returns a copy of the occupants stored inside the hive at (x, y, z).
func (s *Server) BeehiveOccupants(dimension string, x, y, z int) []BeehiveOccupant {
	mgr := s.BeeStates()
	if mgr == nil {
		return nil
	}
	key := beeHivePosKey{Dimension: world.NormalizeDimension(dimension), X: x, Y: y, Z: z}
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	be := mgr.hives[key]
	if be == nil || len(be.Occupants) == 0 {
		return nil
	}
	out := make([]BeehiveOccupant, len(be.Occupants))
	copy(out, be.Occupants)
	return out
}

// IsBeeNightOrRain returns true when bees in dimension should return to their hive
// because it is night (time of day 12542..23459 in Overworld) or raining/thundering.
func (s *Server) IsBeeNightOrRain(dimension string) bool {
	if s == nil {
		return false
	}
	normDim := world.NormalizeDimension(dimension)
	if s.IsRaining() || s.IsThundering() {
		return true
	}
	if normDim != world.DimensionOverworld {
		return false
	}
	_, dayTime := s.WorldTime()
	tod := ((dayTime % 24000) + 24000) % 24000
	return tod >= 12542 && tod < 23460
}

// TryEnterBeehive attempts to move a living `minecraft:bee` entity into the beehive/bee_nest at (hx, hy, hz).
// Returns true if the bee entered the hive and was despawned from the world.
func (s *Server) TryEnterBeehive(beeEntityID int32, dimension string, hx, hy, hz int) bool {
	if s == nil || s.entities == nil {
		return false
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return false
	}
	hiveState := cache.GetBlock(hx, hy, hz)
	if !IsBeehiveOrNestBlockState(hiveState) {
		return false
	}
	ent := s.entities.Get(beeEntityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeBee || ent.EffectiveDimension() != normDim {
		return false
	}
	st, _ := s.BeeStateFor(beeEntityID)
	if st.AngerTicks > 0 || st.CannotEnterHiveTicks > 0 || st.StayOutOfHiveCountdown > 0 {
		return false
	}
	mgr := s.BeeStates()
	if mgr == nil {
		return false
	}
	key := beeHivePosKey{Dimension: normDim, X: hx, Y: hy, Z: hz}
	isBaby := s.IsMobBaby(beeEntityID)
	ageTicks := s.MobAge(beeEntityID)
	hp := s.MobHealth(beeEntityID)
	if hp <= 0 {
		hp = DefaultBeeMaxHealth
	}
	minTicks := BeeMinOccupationTicksNoNectar
	if st.HasNectar {
		minTicks = BeeMinOccupationTicksNectar
	}
	mgr.mu.Lock()
	be := mgr.hives[key]
	if be == nil {
		be = &BeehiveBlockEntity{
			Dimension: normDim,
			X:         hx,
			Y:         hy,
			Z:         hz,
			BlockName: canonicalResourceName(world.BlockName(hiveState)),
		}
		mgr.hives[key] = be
	}
	if len(be.Occupants) >= BeeMaxHiveOccupants {
		mgr.mu.Unlock()
		return false
	}
	be.Occupants = append(be.Occupants, BeehiveOccupant{
		TypeName:           MobTypeBee,
		TicksInHive:        0,
		MinOccupationTicks: minTicks,
		HasNectar:          st.HasNectar,
		Health:             hp,
		IsBaby:             isBaby,
		AgeTicks:           ageTicks,
	})
	delete(mgr.bees, beeEntityID)
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	s.entities.Remove(beeEntityID)
	tr := s.entityTracker()
	tr.mu.Lock()
	delete(tr.mobStates, beeEntityID)
	tr.mu.Unlock()

	s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayRemoveEntities, encodeRemoveEntitiesPacket(nil, []int32{beeEntityID}))
	s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundBeehiveEnter, SoundSourceBlocks, float64(hx)+0.5, float64(hy)+0.5, float64(hz)+0.5, 1.0, 1.0, 0))
	s.EmitVibration(VibrationEvent{
		Dimension: normDim,
		Type:      GameEventBlockChange,
		X:         float64(hx) + 0.5,
		Y:         float64(hy) + 0.5,
		Z:         float64(hz) + 0.5,
	})
	return true
}

func beehiveFrontSpawnPos(hiveState uint16, hx, hy, hz int) (float64, float64, float64, int, int) {
	props := world.BlockProperties(hiveState)
	dx, dz := 0, -1
	switch props["facing"] {
	case "south":
		dx, dz = 0, 1
	case "west":
		dx, dz = -1, 0
	case "east":
		dx, dz = 1, 0
	default:
		dx, dz = 0, -1
	}
	return float64(hx) + 0.5 + float64(dx)*0.75, float64(hy) + 0.25, float64(hz) + 0.5 + float64(dz)*0.75, dx, dz
}

func (s *Server) canReleaseBeeInFrontOfHive(cache *world.Cache, hiveState uint16, hx, hy, hz int) bool {
	if cache == nil {
		return true
	}
	_, _, _, dx, dz := beehiveFrontSpawnPos(hiveState, hx, hy, hz)
	frontState := cache.GetBlock(hx+dx, hy, hz+dz)
	return isPassableBlock(frontState)
}

func (s *Server) spawnBeeFromOccupant(dimension string, hx, hy, hz int, hiveState uint16, occ BeehiveOccupant, emergency bool) int32 {
	sx, sy, sz, _, _ := beehiveFrontSpawnPos(hiveState, hx, hy, hz)
	if emergency {
		sx, sy, sz = float64(hx)+0.5, float64(hy)+0.5, float64(hz)+0.5
	}
	beeID, err := s.SpawnBee(dimension, sx, sy, sz, occ.IsBaby)
	if err != nil || beeID <= 0 {
		return 0
	}
	hp := occ.Health
	if hp <= 0 {
		hp = DefaultBeeMaxHealth
	}
	s.SetMobHealth(beeID, hp)
	if occ.IsBaby && occ.AgeTicks < 0 {
		newAge := occ.AgeTicks + occ.TicksInHive
		if newAge >= 0 {
			s.SetMobAge(beeID, 0)
		} else {
			s.SetMobAge(beeID, newAge)
		}
	}
	mgr := s.BeeStates()
	if mgr != nil {
		mgr.mu.Lock()
		if st := mgr.bees[beeID]; st != nil {
			st.HiveDimension = world.NormalizeDimension(dimension)
			st.HasHivePos = true
			st.HiveX, st.HiveY, st.HiveZ = hx, hy, hz
			st.StayOutOfHiveCountdown = 400
			if emergency {
				st.HasNectar = occ.HasNectar
			}
		}
		mgr.mu.Unlock()
	}
	if ent := s.entities.Get(beeID); ent != nil {
		s.broadcastBeeMetadata(ent)
	}
	return beeID
}

// TickBeehives ticks all active `minecraft:beehive` and `minecraft:bee_nest` block entities,
// incrementing occupant time and releasing bees when `TicksInHive >= MinOccupationTicks`
// (during daytime and clear weather), incrementing `honey_level` (+1, max 5) when `HasNectar` is true.
func (s *Server) TickBeehives(rng *rand.Rand) {
	mgr := s.BeeStates()
	if s == nil || mgr == nil {
		return
	}
	type releaseItem struct {
		dim       string
		x, y, z   int
		hiveState uint16
		occ       BeehiveOccupant
	}
	var toRelease []releaseItem

	mgr.mu.Lock()
	for key, be := range mgr.hives {
		if be == nil || len(be.Occupants) == 0 {
			continue
		}
		cache := s.DimensionChunks(key.Dimension)
		if cache == nil {
			continue
		}
		hiveState := cache.GetBlock(key.X, key.Y, key.Z)
		if !IsBeehiveOrNestBlockState(hiveState) {
			delete(mgr.hives, key)
			continue
		}
		nightOrRain := s.IsBeeNightOrRain(key.Dimension)
		canExitFront := s.canReleaseBeeInFrontOfHive(cache, hiveState, key.X, key.Y, key.Z)
		remaining := be.Occupants[:0]
		for _, occ := range be.Occupants {
			occ.TicksInHive++
			minTicks := occ.MinOccupationTicks
			if minTicks <= 0 {
				if occ.HasNectar {
					minTicks = BeeMinOccupationTicksNectar
				} else {
					minTicks = BeeMinOccupationTicksNoNectar
				}
			}
			if occ.TicksInHive >= minTicks && !nightOrRain && canExitFront {
				toRelease = append(toRelease, releaseItem{
					dim:       key.Dimension,
					x:         key.X,
					y:         key.Y,
					z:         key.Z,
					hiveState: hiveState,
					occ:       occ,
				})
			} else {
				remaining = append(remaining, occ)
			}
		}
		be.Occupants = remaining
	}
	mgr.mu.Unlock()

	for _, rel := range toRelease {
		if rel.occ.HasNectar {
			curHoney := s.BeehiveHoneyLevelAt(rel.dim, rel.x, rel.y, rel.z)
			if curHoney < BeeMaxHoneyLevel {
				inc := 1
				if rng != nil && curHoney+1 < BeeMaxHoneyLevel && rng.Intn(100) == 0 {
					inc = 2
				}
				nextHoney := curHoney + inc
				if nextHoney > BeeMaxHoneyLevel {
					nextHoney = BeeMaxHoneyLevel
				}
				s.SetBeehiveHoneyLevel(rel.dim, rel.x, rel.y, rel.z, nextHoney)
			}
		}
		s.spawnBeeFromOccupant(rel.dim, rel.x, rel.y, rel.z, rel.hiveState, rel.occ, false)
		cx, cz := int32(rel.x>>4), int32(rel.z>>4)
		s.BroadcastDimensionChunk(rel.dim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundBeehiveExit, SoundSourceBlocks, float64(rel.x)+0.5, float64(rel.y)+0.5, float64(rel.z)+0.5, 1.0, 1.0, 0))
		s.EmitVibration(VibrationEvent{
			Dimension: rel.dim,
			Type:      GameEventBlockChange,
			X:         float64(rel.x) + 0.5,
			Y:         float64(rel.y) + 0.5,
			Z:         float64(rel.z) + 0.5,
		})
	}
}

// ReleaseBeesFromHive releases all bees currently inside the hive at (hx, hy, hz).
// If angerAtSession is non-nil (and difficulty != Peaceful), all released bees + nearby bees
// become angry at angerAtSession for 400..800 ticks.
func (s *Server) ReleaseBeesFromHive(dimension string, hx, hy, hz int, angerAtSession *PlayerSession, emergency bool, rng *rand.Rand) []int32 {
	mgr := s.BeeStates()
	if s == nil || mgr == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dimension)
	key := beeHivePosKey{Dimension: normDim, X: hx, Y: hy, Z: hz}
	var hiveState uint16
	if cache := s.DimensionChunks(normDim); cache != nil {
		hiveState = cache.GetBlock(hx, hy, hz)
	}
	mgr.mu.Lock()
	be := mgr.hives[key]
	if be == nil || len(be.Occupants) == 0 {
		mgr.mu.Unlock()
		return nil
	}
	occupants := make([]BeehiveOccupant, len(be.Occupants))
	copy(occupants, be.Occupants)
	be.Occupants = nil
	mgr.mu.Unlock()

	spawned := make([]int32, 0, len(occupants))
	for _, occ := range occupants {
		id := s.spawnBeeFromOccupant(normDim, hx, hy, hz, hiveState, occ, emergency)
		if id > 0 {
			spawned = append(spawned, id)
			if angerAtSession != nil && s.Difficulty() != DifficultyPeaceful {
				s.AngerBeeAtPlayer(id, angerAtSession, rng)
			}
		}
	}
	return spawned
}

// HarvestBeehiveWithShears harvests `3x minecraft:honeycomb` from a beehive/bee_nest at `honey_level=5`,
// resets `honey_level` to 0, damages shears by 1 (in Survival/Adventure), and angers bees if not smoked by a campfire.
func (s *Server) HarvestBeehiveWithShears(session *PlayerSession, dimension string, x, y, z int, hand int32, rng *rand.Rand) (bool, []world.ItemStack) {
	if s == nil {
		return false, nil
	}
	ensureBeeAdvancementsRegistered()
	normDim := world.NormalizeDimension(dimension)
	if s.BeehiveHoneyLevelAt(normDim, x, y, z) < BeeMaxHoneyLevel {
		return false, nil
	}
	smoked := s.IsBeehiveSmokedByCampfire(normDim, x, y, z)
	s.SetBeehiveHoneyLevel(normDim, x, y, z, 0)

	drop := world.NewNamedItemStack("minecraft:honeycomb", 3)
	s.SpawnItemEntityInDimension(normDim, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, drop)

	cx, cz := int32(x>>4), int32(z>>4)
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundBeehiveShear, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	s.EmitVibration(VibrationEvent{
		Dimension: normDim,
		Type:      GameEventShear,
		X:         float64(x) + 0.5,
		Y:         float64(y) + 0.5,
		Z:         float64(z) + 0.5,
		Player:    session,
	})

	if session != nil {
		snap := session.Snapshot()
		if snap.GameMode != GameModeCreative {
			invSlot := world.HotbarContainerOffset + int(snap.SelectedSlot)
			if hand == 1 {
				invSlot = 45
			}
			slotItem := session.InventorySlot(invSlot)
			if canonicalResourceName(slotItem.ResolvedID()) == "minecraft:shears" && !slotItem.IsEmpty() {
				meta := GetSlotItemMeta(session, invSlot)
				newStack, newMeta, _ := DamageItem(slotItem, meta, 1, rng)
				session.SetInventorySlot(invSlot, newStack)
				SetSlotItemMeta(session, invSlot, newMeta)
			}
		}
		if smoked {
			s.GrantCriterion(session, AdvancementSafelyHarvestHoney, CriterionSafelyHarvestHoney)
		} else if s.Difficulty() != DifficultyPeaceful {
			s.ReleaseBeesFromHive(normDim, x, y, z, session, true, rng)
			s.AngerBeesNear(normDim, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, BeeSwarmAlertRadius, session, rng)
		}
	}
	return true, []world.ItemStack{drop}
}

// DispenserShearBeehive is the dispenser hook for shearing a `honey_level=5` beehive/bee_nest
// without angering bees. Returns (harvested, toolBroken).
func (s *Server) DispenserShearBeehive(dimension string, x, y, z int, shearsStack *world.ItemStack, shearsMeta *ItemMeta, rng *rand.Rand) (bool, bool) {
	if s == nil {
		return false, false
	}
	normDim := world.NormalizeDimension(dimension)
	if s.BeehiveHoneyLevelAt(normDim, x, y, z) < BeeMaxHoneyLevel {
		return false, false
	}
	s.SetBeehiveHoneyLevel(normDim, x, y, z, 0)
	s.SpawnItemEntityInDimension(normDim, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5,
		world.NewNamedItemStack("minecraft:honeycomb", 3))
	cx, cz := int32(x>>4), int32(z>>4)
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundBeehiveShear, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	s.EmitVibration(VibrationEvent{
		Dimension: normDim,
		Type:      GameEventShear,
		X:         float64(x) + 0.5,
		Y:         float64(y) + 0.5,
		Z:         float64(z) + 0.5,
	})

	broken := false
	if shearsStack != nil {
		var m ItemMeta
		if shearsMeta != nil {
			m = *shearsMeta
		}
		ns, nm, broke := DamageItem(*shearsStack, m, 1, rng)
		*shearsStack = ns
		if shearsMeta != nil {
			*shearsMeta = nm
		}
		broken = broke
	}
	return true, broken
}

// HarvestBeehiveWithBottle harvests `1x minecraft:honey_bottle` from a beehive/bee_nest at `honey_level=5`,
// resets `honey_level` to 0, consumes 1 `minecraft:glass_bottle` (in Survival/Adventure), grants
// `"minecraft:husbandry/safely_harvest_honey"` if calmed by campfire smoke, or angers bees if un-smoked.
func (s *Server) HarvestBeehiveWithBottle(session *PlayerSession, dimension string, x, y, z int, hand int32, rng *rand.Rand) (bool, world.ItemStack) {
	if s == nil {
		return false, world.ItemStack{}
	}
	ensureBeeAdvancementsRegistered()
	normDim := world.NormalizeDimension(dimension)
	if s.BeehiveHoneyLevelAt(normDim, x, y, z) < BeeMaxHoneyLevel {
		return false, world.ItemStack{}
	}
	smoked := s.IsBeehiveSmokedByCampfire(normDim, x, y, z)
	s.SetBeehiveHoneyLevel(normDim, x, y, z, 0)

	honeyBottle := world.NewNamedItemStack("minecraft:honey_bottle", 1)
	cx, cz := int32(x>>4), int32(z>>4)
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundBottleFillCauldron, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	s.EmitVibration(VibrationEvent{
		Dimension: normDim,
		Type:      GameEventFluidPickup,
		X:         float64(x) + 0.5,
		Y:         float64(y) + 0.5,
		Z:         float64(z) + 0.5,
		Player:    session,
	})

	if session != nil {
		snap := session.Snapshot()
		if snap.GameMode != GameModeCreative {
			invSlot := world.HotbarContainerOffset + int(snap.SelectedSlot)
			if hand == 1 {
				invSlot = 45
			}
			slotItem := session.InventorySlot(invSlot)
			if canonicalResourceName(slotItem.ResolvedID()) == "minecraft:glass_bottle" && !slotItem.IsEmpty() {
				if slotItem.Count <= 1 {
					session.SetInventorySlot(invSlot, honeyBottle)
				} else {
					session.SetInventorySlot(invSlot, world.NewItemStack(slotItem.ResolvedItemID(), slotItem.Count-1))
					if rem, _ := session.AddItemToInventory(honeyBottle); rem > 0 {
						s.SpawnItemEntityInDimension(normDim, snap.X, snap.Y+0.5, snap.Z, honeyBottle)
					}
				}
			} else if rem, _ := session.AddItemToInventory(honeyBottle); rem > 0 {
				s.SpawnItemEntityInDimension(normDim, snap.X, snap.Y+0.5, snap.Z, honeyBottle)
			}
		} else {
			_, _ = session.AddItemToInventory(honeyBottle)
		}

		if smoked {
			s.GrantCriterion(session, AdvancementSafelyHarvestHoney, CriterionSafelyHarvestHoney)
		} else if s.Difficulty() != DifficultyPeaceful {
			s.ReleaseBeesFromHive(normDim, x, y, z, session, true, rng)
			s.AngerBeesNear(normDim, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, BeeSwarmAlertRadius, session, rng)
		}
	}
	return true, honeyBottle
}

// DispenserBottleBeehive is the dispenser hook for filling a `minecraft:glass_bottle` from a `honey_level=5`
// beehive/bee_nest without angering bees.
func (s *Server) DispenserBottleBeehive(dimension string, x, y, z int) (world.ItemStack, bool) {
	if s == nil {
		return world.ItemStack{}, false
	}
	normDim := world.NormalizeDimension(dimension)
	if s.BeehiveHoneyLevelAt(normDim, x, y, z) < BeeMaxHoneyLevel {
		return world.ItemStack{}, false
	}
	s.SetBeehiveHoneyLevel(normDim, x, y, z, 0)
	cx, cz := int32(x>>4), int32(z>>4)
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundBottleFillCauldron, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	s.EmitVibration(VibrationEvent{
		Dimension: normDim,
		Type:      GameEventFluidPickup,
		X:         float64(x) + 0.5,
		Y:         float64(y) + 0.5,
		Z:         float64(z) + 0.5,
	})
	return world.NewNamedItemStack("minecraft:honey_bottle", 1), true
}

// ConsumeHoneyBottle applies the vanilla 26.1.2 effects of drinking `minecraft:honey_bottle`:
// restores 6 hunger + 1.2 saturation, clears `minecraft:poison`, and returns `1x minecraft:glass_bottle`.
func (s *Server) ConsumeHoneyBottle(session *PlayerSession) world.ItemStack {
	if session != nil {
		session.mu.Lock()
		food := session.foodLevel + 6
		if food > MaxFoodLevel {
			food = MaxFoodLevel
		}
		sat := session.foodSaturation + 1.2
		if sat > float32(food) {
			sat = float32(food)
		}
		session.foodLevel = food
		session.foodSaturation = sat
		hp := session.health
		send := session.send
		session.mu.Unlock()
		if send != nil {
			_ = send(protocol.PlaySetHealth, EncodeSetHealth(hp, food, sat))
		}
		if s != nil {
			s.RemovePlayerEffect(session, EffectPoison)
		}
	}
	return world.NewNamedItemStack("minecraft:glass_bottle", 1)
}

// SerializeBeehiveItemMeta encodes a beehive's honey_level and occupants into a compact metadata string
// for Silk Touch drops (`honey_level=<n>;bees=<hasNectar>,<ticks>,<minTicks>,<isBaby>|...`).
func SerializeBeehiveItemMeta(honeyLevel int, occupants []BeehiveOccupant) string {
	var parts []string
	if honeyLevel > 0 {
		parts = append(parts, fmt.Sprintf("honey_level=%d", honeyLevel))
	}
	if len(occupants) > 0 {
		beeStrs := make([]string, 0, len(occupants))
		for _, occ := range occupants {
			nFlag := 0
			if occ.HasNectar {
				nFlag = 1
			}
			bFlag := 0
			if occ.IsBaby {
				bFlag = 1
			}
			beeStrs = append(beeStrs, fmt.Sprintf("%d,%d,%d,%d", nFlag, occ.TicksInHive, occ.MinOccupationTicks, bFlag))
		}
		parts = append(parts, "bees="+strings.Join(beeStrs, "|"))
	}
	return strings.Join(parts, ";")
}

// ParseBeehiveItemMeta parses `honey_level` and `[]BeehiveOccupant` from a serialized beehive metadata string.
func ParseBeehiveItemMeta(meta string) (int, []BeehiveOccupant) {
	meta = strings.TrimSpace(meta)
	if meta == "" {
		return 0, nil
	}
	var honeyLevel int
	var occupants []BeehiveOccupant
	for _, token := range strings.Split(meta, ";") {
		token = strings.TrimSpace(token)
		if strings.HasPrefix(token, "honey_level=") {
			lvl, _ := strconv.Atoi(strings.TrimPrefix(token, "honey_level="))
			if lvl < 0 {
				lvl = 0
			}
			if lvl > BeeMaxHoneyLevel {
				lvl = BeeMaxHoneyLevel
			}
			honeyLevel = lvl
		} else if strings.HasPrefix(token, "bees=") {
			rawBees := strings.TrimPrefix(token, "bees=")
			for _, entry := range strings.Split(rawBees, "|") {
				if entry == "" {
					continue
				}
				fields := strings.Split(entry, ",")
				occ := BeehiveOccupant{
					TypeName:           MobTypeBee,
					MinOccupationTicks: BeeMinOccupationTicksNoNectar,
					Health:             DefaultBeeMaxHealth,
				}
				if len(fields) >= 1 && fields[0] == "1" {
					occ.HasNectar = true
					occ.MinOccupationTicks = BeeMinOccupationTicksNectar
				}
				if len(fields) >= 2 {
					occ.TicksInHive, _ = strconv.Atoi(fields[1])
				}
				if len(fields) >= 3 {
					if m, err := strconv.Atoi(fields[2]); err == nil && m > 0 {
						occ.MinOccupationTicks = m
					}
				}
				if len(fields) >= 4 && fields[3] == "1" {
					occ.IsBaby = true
					occ.AgeTicks = BabyMobGrowthTicks
				}
				if len(occupants) < BeeMaxHiveOccupants {
					occupants = append(occupants, occ)
				}
			}
		}
	}
	return honeyLevel, occupants
}

// OnBeehivePlaced restores `honey_level` and stored bees from `itemMetaStr` (or `ItemMeta.CustomName`)
// when a player places a beehive or bee_nest.
func (s *Server) OnBeehivePlaced(dimension string, x, y, z int, itemMetaStr string) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dimension)
	honeyLevel, occupants := ParseBeehiveItemMeta(itemMetaStr)
	if honeyLevel > 0 {
		s.SetBeehiveHoneyLevel(normDim, x, y, z, honeyLevel)
	}
	if len(occupants) > 0 {
		be := s.GetOrCreateBeehive(normDim, x, y, z)
		if be != nil {
			mgr := s.BeeStates()
			mgr.mu.Lock()
			be.Occupants = append(be.Occupants[:0], occupants...)
			mgr.mu.Unlock()
		}
	}
}

// OnBeehiveBroken handles breaking a `minecraft:beehive` or `minecraft:bee_nest` at (x, y, z):
//   - With Silk Touch: keeps all bees inside the dropped item (`droppedMetaStr`),
//     does NOT anger bees, and grants `"minecraft:husbandry/silk_touch_nest"` ("Total Beelocation")
//     when a `minecraft:bee_nest` with 3 bees is broken with Silk Touch.
//   - Without Silk Touch: releases all stored bees and angers them + nearby bees within 22 blocks
//     at the player (even if a campfire is underneath!). `bee_nest` drops nothing without Silk Touch;
//     `beehive` drops `1x minecraft:beehive`.
func (s *Server) OnBeehiveBroken(session *PlayerSession, dimension string, x, y, z int, blockState uint16, toolMeta ItemMeta, rng *rand.Rand) (drops []world.ItemStack, droppedMetaStr string) {
	if s == nil {
		return nil, ""
	}
	ensureBeeAdvancementsRegistered()
	normDim := world.NormalizeDimension(dimension)
	base := canonicalResourceName(world.BlockName(blockState))
	if base != "minecraft:beehive" && base != "minecraft:bee_nest" {
		base = "minecraft:beehive"
	}
	honeyLevel := BeehiveHoneyLevelState(blockState)
	occupants := s.BeehiveOccupants(normDim, x, y, z)

	if HasSilkTouch(toolMeta) {
		mgr := s.BeeStates()
		if mgr != nil {
			mgr.mu.Lock()
			delete(mgr.hives, beeHivePosKey{Dimension: normDim, X: x, Y: y, Z: z})
			mgr.mu.Unlock()
		}
		if session != nil && base == "minecraft:bee_nest" && len(occupants) == BeeMaxHiveOccupants {
			s.GrantCriterion(session, AdvancementSilkTouchNest, CriterionSilkTouchNest)
		}
		droppedMetaStr = SerializeBeehiveItemMeta(honeyLevel, occupants)
		drop := world.NewNamedItemStack(base, 1)
		return []world.ItemStack{drop}, droppedMetaStr
	}

	// Broken without Silk Touch: release all bees and anger swarm!
	s.ReleaseBeesFromHive(normDim, x, y, z, session, true, rng)
	mgr := s.BeeStates()
	if mgr != nil {
		mgr.mu.Lock()
		delete(mgr.hives, beeHivePosKey{Dimension: normDim, X: x, Y: y, Z: z})
		mgr.mu.Unlock()
	}
	if session != nil && s.Difficulty() != DifficultyPeaceful {
		s.AngerBeesNear(normDim, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, BeeSwarmAlertRadius, session, rng)
	}
	if base == "minecraft:bee_nest" {
		return nil, ""
	}
	return []world.ItemStack{world.NewNamedItemStack("minecraft:beehive", 1)}, ""
}

func randomBeeAngerTicks(rng *rand.Rand) int {
	if rng == nil {
		return 600
	}
	return BeeAngerMinTicks + rng.Intn(BeeAngerMaxTicks-BeeAngerMinTicks+1)
}

// AngerBeeAtPlayer makes a single bee angry at targetSession (unless the bee already stung or difficulty is Peaceful).
func (s *Server) AngerBeeAtPlayer(beeEntityID int32, targetSession *PlayerSession, rng *rand.Rand) bool {
	if s == nil || targetSession == nil || s.Difficulty() == DifficultyPeaceful {
		return false
	}
	snap := targetSession.Snapshot()
	if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false
	}
	ent := s.entities.Get(beeEntityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeBee {
		return false
	}
	st := s.ensureBeeState(beeEntityID)
	mgr := s.BeeStates()
	mgr.mu.Lock()
	if st.HasStung {
		mgr.mu.Unlock()
		return false
	}
	st.AngerTicks = randomBeeAngerTicks(rng)
	st.TargetSession = targetSession
	st.TargetMobID = 0
	st.IsRolling = false
	st.PollinatingTicks = 0
	mgr.mu.Unlock()
	s.broadcastBeeMetadata(ent)
	return true
}

// AngerBeeAtMob makes a single bee angry at another mob entity.
func (s *Server) AngerBeeAtMob(beeEntityID, targetMobID int32, rng *rand.Rand) bool {
	if s == nil || targetMobID <= 0 || beeEntityID == targetMobID || s.Difficulty() == DifficultyPeaceful {
		return false
	}
	ent := s.entities.Get(beeEntityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeBee {
		return false
	}
	st := s.ensureBeeState(beeEntityID)
	mgr := s.BeeStates()
	mgr.mu.Lock()
	if st.HasStung {
		mgr.mu.Unlock()
		return false
	}
	st.AngerTicks = randomBeeAngerTicks(rng)
	st.TargetMobID = targetMobID
	st.TargetSession = nil
	st.IsRolling = false
	st.PollinatingTicks = 0
	mgr.mu.Unlock()
	s.broadcastBeeMetadata(ent)
	return true
}

// AngerBeesNear alerts all bees within `radius` (vanilla 22.0 blocks) in dimension to attack targetSession.
func (s *Server) AngerBeesNear(dimension string, x, y, z, radius float64, targetSession *PlayerSession, rng *rand.Rand) int {
	if s == nil || s.entities == nil || targetSession == nil || s.Difficulty() == DifficultyPeaceful {
		return 0
	}
	normDim := world.NormalizeDimension(dimension)
	rSq := radius * radius
	count := 0
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		ent := &allEnts[i]
		if canonicalResourceName(ent.TypeName) != MobTypeBee || ent.EffectiveDimension() != normDim {
			continue
		}
		dx := ent.X - x
		dy := ent.Y - y
		dz := ent.Z - z
		if dx*dx+dy*dy+dz*dz <= rSq {
			if s.AngerBeeAtPlayer(ent.ID, targetSession, rng) {
				count++
			}
		}
	}
	return count
}

// AngerBeesNearAtMob alerts all bees within `radius` in dimension to attack targetMobID.
func (s *Server) AngerBeesNearAtMob(dimension string, x, y, z, radius float64, targetMobID int32, rng *rand.Rand) int {
	if s == nil || s.entities == nil || targetMobID <= 0 || s.Difficulty() == DifficultyPeaceful {
		return 0
	}
	normDim := world.NormalizeDimension(dimension)
	rSq := radius * radius
	count := 0
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		ent := &allEnts[i]
		if canonicalResourceName(ent.TypeName) != MobTypeBee || ent.EffectiveDimension() != normDim || ent.ID == targetMobID {
			continue
		}
		dx := ent.X - x
		dy := ent.Y - y
		dz := ent.Z - z
		if dx*dx+dy*dy+dz*dz <= rSq {
			if s.AngerBeeAtMob(ent.ID, targetMobID, rng) {
				count++
			}
		}
	}
	return count
}

func randomBeePostStingDeathTicks(rng *rand.Rand) int {
	if rng == nil {
		return 1100
	}
	return BeePostStingDeathMinTicks + rng.Intn(BeePostStingDeathMaxTicks-BeePostStingDeathMinTicks+1)
}

// PerformBeeSting executes a bee's sting attack on targetSession:
//   - Deals 2.0 base melee damage (scaled by server difficulty)
//   - On Normal (10s = 200 ticks) or Hard (18s = 360 ticks), applies Poison I (`minecraft:poison`)
//   - Sets `HasStung = true`, clears `AngerTicks` & `HasNectar`, and starts the 50..60s (1000..1200 ticks) death timer.
func (s *Server) PerformBeeSting(bee *world.Entity, targetSession *PlayerSession, rng *rand.Rand) bool {
	if s == nil || bee == nil || targetSession == nil || canonicalResourceName(bee.TypeName) != MobTypeBee {
		return false
	}
	mgr := s.BeeStates()
	if mgr == nil {
		return false
	}
	st := s.ensureBeeState(bee.ID)
	mgr.mu.Lock()
	if st.HasStung {
		mgr.mu.Unlock()
		return false
	}
	mgr.mu.Unlock()

	dealt := s.AttackPlayerFromMob(bee.ID, MobTypeBee, bee.X, bee.Z, targetSession, BeeBaseAttackDamage)
	cp := bee.ChunkPos()
	s.BroadcastDimensionChunk(bee.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundBeeSting, SoundSourceNeutral, bee.X, bee.Y, bee.Z, 1.0, 1.0, 0))

	if dealt > 0 || !targetSession.IsBlockingWithShield() {
		switch s.Difficulty() {
		case DifficultyNormal:
			s.ApplyPlayerEffect(targetSession, StatusEffectInstance{
				ID:            EffectPoison,
				Amplifier:     0,
				Duration:      BeePoisonDurationNormalTicks,
				ShowParticles: true,
				ShowIcon:      true,
			})
		case DifficultyHard:
			s.ApplyPlayerEffect(targetSession, StatusEffectInstance{
				ID:            EffectPoison,
				Amplifier:     0,
				Duration:      BeePoisonDurationHardTicks,
				ShowParticles: true,
				ShowIcon:      true,
			})
		}
		mgr.mu.Lock()
		st.HasStung = true
		st.HasNectar = false
		st.IsRolling = false
		st.AngerTicks = 0
		st.TargetSession = nil
		st.TargetMobID = 0
		st.TimeSinceSting = 0
		st.DeathAfterStingTicks = randomBeePostStingDeathTicks(rng)
		mgr.mu.Unlock()
		s.broadcastBeeMetadata(bee)
	}
	return true
}

// PerformBeeStingOnMob executes a bee's sting attack against another mob entity.
func (s *Server) PerformBeeStingOnMob(bee, targetMob *world.Entity, rng *rand.Rand) bool {
	if s == nil || bee == nil || targetMob == nil || canonicalResourceName(bee.TypeName) != MobTypeBee {
		return false
	}
	mgr := s.BeeStates()
	if mgr == nil {
		return false
	}
	st := s.ensureBeeState(bee.ID)
	mgr.mu.Lock()
	if st.HasStung {
		mgr.mu.Unlock()
		return false
	}
	mgr.mu.Unlock()

	dmg := ScaleDamageByDifficulty(BeeBaseAttackDamage, s.Difficulty())
	if dmg <= 0 {
		return false
	}
	s.applyDamageToMob(targetMob, dmg, nil, 0.3, false, true)
	switch s.Difficulty() {
	case DifficultyNormal:
		s.ApplyEntityEffectScaled(targetMob.ID, StatusEffectInstance{
			ID:            EffectPoison,
			Amplifier:     0,
			Duration:      BeePoisonDurationNormalTicks,
			ShowParticles: true,
			ShowIcon:      true,
		}, 1.0)
	case DifficultyHard:
		s.ApplyEntityEffectScaled(targetMob.ID, StatusEffectInstance{
			ID:            EffectPoison,
			Amplifier:     0,
			Duration:      BeePoisonDurationHardTicks,
			ShowParticles: true,
			ShowIcon:      true,
		}, 1.0)
	}
	cp := bee.ChunkPos()
	s.BroadcastDimensionChunk(bee.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundBeeSting, SoundSourceNeutral, bee.X, bee.Y, bee.Z, 1.0, 1.0, 0))

	mgr.mu.Lock()
	st.HasStung = true
	st.HasNectar = false
	st.IsRolling = false
	st.AngerTicks = 0
	st.TargetSession = nil
	st.TargetMobID = 0
	st.TimeSinceSting = 0
	st.DeathAfterStingTicks = randomBeePostStingDeathTicks(rng)
	mgr.mu.Unlock()
	s.broadcastBeeMetadata(bee)
	return true
}

// TryFertilizeCropUnderBee attempts to advance the growth stage (+1 age) of a fertilizable crop
// 0..2 blocks below a bee that has `HasNectar == true` and `CropsGrownSincePollination < 10`.
func (s *Server) TryFertilizeCropUnderBee(beeEntityID int32) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(beeEntityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeBee {
		return false
	}
	mgr := s.BeeStates()
	if mgr == nil {
		return false
	}
	mgr.mu.RLock()
	st := mgr.bees[beeEntityID]
	if st == nil || !st.HasNectar || st.CropsGrownSincePollination >= BeeMaxCropsGrownPerPollination {
		mgr.mu.RUnlock()
		return false
	}
	mgr.mu.RUnlock()

	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return false
	}
	bx := int(math.Floor(ent.X))
	by := int(math.Floor(ent.Y))
	bz := int(math.Floor(ent.Z))
	for dy := 0; dy <= 2; dy++ {
		cy := by - dy
		if cy < world.MinY || cy >= world.MinY+world.WorldHeight {
			continue
		}
		curState := cache.GetBlock(bx, cy, bz)
		nextState, canGrow := IsBeeFertilizableCropState(curState)
		if !canGrow {
			continue
		}
		cache.SetBlock(bx, cy, bz, nextState)
		w := protocol.NewWriter(12)
		w.Position(bx, cy, bz)
		w.VarInt(int32(nextState))
		s.BroadcastDimensionChunk(dim, int32(bx>>4), int32(bz>>4), protocol.PlayBlockUpdate, w.Bytes())
		mgr.mu.Lock()
		if st2 := mgr.bees[beeEntityID]; st2 != nil {
			st2.CropsGrownSincePollination++
		}
		mgr.mu.Unlock()
		s.EmitVibration(VibrationEvent{
			Dimension:      dim,
			Type:           GameEventBlockChange,
			X:              float64(bx) + 0.5,
			Y:              float64(cy) + 0.5,
			Z:              float64(bz) + 0.5,
			SourceEntityID: beeEntityID,
		})
		return true
	}
	return false
}

func (s *Server) findNearbyBeeFlower(cache *world.Cache, cx, cy, cz, radius int) (int, int, int, bool) {
	if cache == nil {
		return 0, 0, 0, false
	}
	bestDistSq := math.MaxInt32
	var fx, fy, fz int
	found := false
	for dy := -3; dy <= 3; dy++ {
		y := cy + dy
		if y < world.MinY || y >= world.MinY+world.WorldHeight {
			continue
		}
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				distSq := dx*dx + dy*dy + dz*dz
				if distSq >= bestDistSq {
					continue
				}
				if IsBeeFlowerBlockState(cache.GetBlock(cx+dx, y, cz+dz)) {
					bestDistSq = distSq
					fx, fy, fz = cx+dx, y, cz+dz
					found = true
				}
			}
		}
	}
	return fx, fy, fz, found
}

func (s *Server) findNearbyAvailableBeehive(dimension string, cache *world.Cache, cx, cy, cz, radius int) (int, int, int, bool) {
	if cache == nil {
		return 0, 0, 0, false
	}
	bestDistSq := math.MaxInt32
	var hx, hy, hz int
	found := false
	for dy := -4; dy <= 4; dy++ {
		y := cy + dy
		if y < world.MinY || y >= world.MinY+world.WorldHeight {
			continue
		}
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				distSq := dx*dx + dy*dy + dz*dz
				if distSq >= bestDistSq {
					continue
				}
				x, z := cx+dx, cz+dz
				if !IsBeehiveOrNestBlockState(cache.GetBlock(x, y, z)) {
					continue
				}
				if s.BeehiveOccupantCount(dimension, x, y, z) >= BeeMaxHiveOccupants {
					continue
				}
				bestDistSq = distSq
				hx, hy, hz = x, y, z
				found = true
			}
		}
	}
	return hx, hy, hz, found
}

func (s *Server) flyBeeToward(ent *world.Entity, tx, ty, tz, speed float64) {
	if s == nil || ent == nil {
		return
	}
	dx := tx - ent.X
	dy := ty - ent.Y
	dz := tz - ent.Z
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist < 1e-4 {
		return
	}
	step := speed
	if dist < step {
		step = dist
	}
	nx := ent.X + (dx/dist)*step
	ny := ent.Y + (dy/dist)*step
	nz := ent.Z + (dz/dist)*step
	yaw := yawToward(dx, dz)

	s.entities.Update(ent.ID, func(e *world.Entity) {
		e.X = nx
		e.Y = ny
		e.Z = nz
		e.Yaw = yaw
		e.HeadYaw = yaw
		e.OnGround = false
	})
}

// TickBeeAI advances one tick of AI for a `minecraft:bee` entity:
//   - Post-sting countdown (`HasStung == true` -> dies after 1000..1200 ticks)
//   - Swarm anger pursuit & sting attack (`PerformBeeSting` / `PerformBeeStingOnMob`)
//   - Returning to `beehive` / `bee_nest` at night, in rain, or with nectar (`HasNectar == true`)
//   - Crop fertilization while flying home with nectar (`TryFertilizeCropUnderBee`)
//   - Flower search & 400-tick pollination (`BeePollinateTargetTicks`)
func (s *Server) TickBeeAI(ent *world.Entity, cache *world.Cache, targets []playerTarget, rng *rand.Rand) {
	if s == nil || ent == nil || canonicalResourceName(ent.TypeName) != MobTypeBee {
		return
	}
	st := s.ensureBeeState(ent.ID)
	mgr := s.BeeStates()
	if st == nil || mgr == nil {
		return
	}
	dim := ent.EffectiveDimension()
	if cache == nil {
		cache = s.DimensionChunks(dim)
	}

	// 1. Post-sting death timer (50..60 seconds after losing stinger).
	mgr.mu.Lock()
	if st.StayOutOfHiveCountdown > 0 {
		st.StayOutOfHiveCountdown--
	}
	if st.CannotEnterHiveTicks > 0 {
		st.CannotEnterHiveTicks--
	}
	if st.PollinateCooldown > 0 {
		st.PollinateCooldown--
	}
	if st.AttackCooldown > 0 {
		st.AttackCooldown--
	}
	if st.HasStung {
		st.TimeSinceSting++
		limit := st.DeathAfterStingTicks
		if limit <= 0 {
			limit = 1100
		}
		if st.TimeSinceSting >= limit {
			mgr.mu.Unlock()
			s.applyDamageToMob(ent, s.MobHealth(ent.ID)+100.0, nil, 0, false, false)
			return
		}
		mgr.mu.Unlock()
		return
	}

	// 2. Angry bee pursuit & sting.
	if s.Difficulty() == DifficultyPeaceful && st.AngerTicks > 0 {
		st.AngerTicks = 0
		st.TargetSession = nil
		st.TargetMobID = 0
		mgr.mu.Unlock()
		s.broadcastBeeMetadata(ent)
		return
	}
	if st.AngerTicks > 0 {
		st.AngerTicks--
		targetSess := st.TargetSession
		targetMobID := st.TargetMobID
		cooldown := st.AttackCooldown
		stillAngry := st.AngerTicks > 0
		if !stillAngry {
			st.TargetSession = nil
			st.TargetMobID = 0
		}
		mgr.mu.Unlock()
		if !stillAngry {
			s.broadcastBeeMetadata(ent)
			return
		}
		if targetSess != nil {
			snap := targetSess.Snapshot()
			if targetSess.IsDead() || snap.Health <= 0 || snap.Dimension != dim || snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator {
				mgr.mu.Lock()
				st.AngerTicks = 0
				st.TargetSession = nil
				mgr.mu.Unlock()
				s.broadcastBeeMetadata(ent)
				return
			}
			s.flyBeeToward(ent, snap.X, snap.Y+1.0, snap.Z, BeeAngryFlySpeed)
			updated := s.entities.Get(ent.ID)
			if updated == nil {
				return
			}
			dx := snap.X - updated.X
			dy := (snap.Y + 0.9) - updated.Y
			dz := snap.Z - updated.Z
			if dx*dx+dy*dy+dz*dz <= BeeAttackReach*BeeAttackReach && cooldown <= 0 {
				mgr.mu.Lock()
				st.AttackCooldown = 20
				mgr.mu.Unlock()
				s.PerformBeeSting(updated, targetSess, rng)
			}
			return
		}
		if targetMobID > 0 {
			targetMob := s.entities.Get(targetMobID)
			if targetMob == nil || targetMob.EffectiveDimension() != dim || s.MobHealth(targetMobID) <= 0 {
				mgr.mu.Lock()
				st.AngerTicks = 0
				st.TargetMobID = 0
				mgr.mu.Unlock()
				s.broadcastBeeMetadata(ent)
				return
			}
			s.flyBeeToward(ent, targetMob.X, targetMob.Y+0.5, targetMob.Z, BeeAngryFlySpeed)
			updated := s.entities.Get(ent.ID)
			if updated == nil {
				return
			}
			dx := targetMob.X - updated.X
			dy := targetMob.Y - updated.Y
			dz := targetMob.Z - updated.Z
			if dx*dx+dy*dy+dz*dz <= BeeAttackReach*BeeAttackReach && cooldown <= 0 {
				mgr.mu.Lock()
				st.AttackCooldown = 20
				mgr.mu.Unlock()
				s.PerformBeeStingOnMob(updated, targetMob, rng)
			}
			return
		}
		return
	}
	mgr.mu.Unlock()

	// 3. Temptation by player holding a flower within 10 blocks.
	if temptTarget, ok := findTemptingPlayer(targets, MobTypeBee, ent.X, ent.Y, ent.Z); ok {
		if math.Hypot(temptTarget.x-ent.X, temptTarget.z-ent.Z) > 1.8 {
			s.flyBeeToward(ent, temptTarget.x, temptTarget.y+1.2, temptTarget.z, BeeFlySpeed)
		}
		return
	}

	// 4. If carrying nectar, fertilize crops below while flying.
	mgr.mu.RLock()
	hasNectar := st.HasNectar
	stayOut := st.StayOutOfHiveCountdown
	cannotEnter := st.CannotEnterHiveTicks
	mgr.mu.RUnlock()

	if hasNectar && (rng == nil || rng.Float64() < BeeCropGrowChance) {
		s.TryFertilizeCropUnderBee(ent.ID)
	}

	// 5. Return to hive if: (HasNectar || Night || Rain) && allowed to enter.
	wantsHive := (hasNectar || s.IsBeeNightOrRain(dim)) && stayOut <= 0 && cannotEnter <= 0
	if wantsHive && cache != nil {
		mgr.mu.RLock()
		hasHive := st.HasHivePos && st.HiveDimension == dim
		hx, hy, hz := st.HiveX, st.HiveY, st.HiveZ
		mgr.mu.RUnlock()

		if hasHive {
			if !IsBeehiveOrNestBlockState(cache.GetBlock(hx, hy, hz)) || s.BeehiveOccupantCount(dim, hx, hy, hz) >= BeeMaxHiveOccupants {
				hasHive = false
				mgr.mu.Lock()
				st.HasHivePos = false
				mgr.mu.Unlock()
			}
		}
		if !hasHive {
			if fx, fy, fz, ok := s.findNearbyAvailableBeehive(dim, cache, int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z)), 12); ok {
				hasHive = true
				hx, hy, hz = fx, fy, fz
				mgr.mu.Lock()
				st.HiveDimension = dim
				st.HasHivePos = true
				st.HiveX, st.HiveY, st.HiveZ = hx, hy, hz
				mgr.mu.Unlock()
			}
		}
		if hasHive {
			tx, ty, tz := float64(hx)+0.5, float64(hy)+0.5, float64(hz)+0.5
			dist := math.Sqrt((tx-ent.X)*(tx-ent.X) + (ty-ent.Y)*(ty-ent.Y) + (tz-ent.Z)*(tz-ent.Z))
			if dist <= 1.75 {
				if s.TryEnterBeehive(ent.ID, dim, hx, hy, hz) {
					return
				}
			} else {
				s.flyBeeToward(ent, tx, ty, tz, BeeFlySpeed)
				return
			}
		}
	}

	// 6. Flower pollination (during daytime and clear weather when not holding nectar).
	if !hasNectar && !s.IsBeeNightOrRain(dim) && cache != nil {
		mgr.mu.RLock()
		hasFlower := st.HasFlowerPos
		fx, fy, fz := st.FlowerX, st.FlowerY, st.FlowerZ
		cooldown := st.PollinateCooldown
		mgr.mu.RUnlock()

		if hasFlower {
			if !IsBeeFlowerBlockState(cache.GetBlock(fx, fy, fz)) {
				hasFlower = false
				mgr.mu.Lock()
				st.HasFlowerPos = false
				st.PollinatingTicks = 0
				if st.IsRolling {
					st.IsRolling = false
					mgr.mu.Unlock()
					s.broadcastBeeMetadata(ent)
				} else {
					mgr.mu.Unlock()
				}
			}
		}
		if !hasFlower && cooldown <= 0 {
			if nfx, nfy, nfz, ok := s.findNearbyBeeFlower(cache, int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z)), 8); ok {
				hasFlower = true
				fx, fy, fz = nfx, nfy, nfz
				mgr.mu.Lock()
				st.HasFlowerPos = true
				st.FlowerX, st.FlowerY, st.FlowerZ = fx, fy, fz
				mgr.mu.Unlock()
			} else {
				mgr.mu.Lock()
				st.PollinateCooldown = BeePollinateCooldownTicks
				mgr.mu.Unlock()
			}
		}
		if hasFlower {
			tx, ty, tz := float64(fx)+0.5, float64(fy)+0.6, float64(fz)+0.5
			dist := math.Sqrt((tx-ent.X)*(tx-ent.X) + (ty-ent.Y)*(ty-ent.Y) + (tz-ent.Z)*(tz-ent.Z))
			if dist <= 1.2 {
				mgr.mu.Lock()
				wasRolling := st.IsRolling
				st.IsRolling = true
				st.PollinatingTicks++
				done := st.PollinatingTicks >= BeePollinateTargetTicks
				if done {
					st.HasNectar = true
					st.IsRolling = false
					st.PollinatingTicks = 0
					st.CropsGrownSincePollination = 0
					st.PollinateCooldown = BeePollinateCooldownTicks
				}
				mgr.mu.Unlock()
				if !wasRolling || done {
					s.broadcastBeeMetadata(ent)
				}
				return
			}
			s.flyBeeToward(ent, tx, ty, tz, BeeFlySpeed)
		}
	}
}

// InteractWithBee handles a player right-clicking a `minecraft:bee` with a flower item
// (accelerating baby growth by 10% or entering love mode on adults to breed a baby bee).
func (s *Server) InteractWithBee(session *PlayerSession, entityID int32) (handled bool, offspringID int32) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeBee {
		return false, 0
	}
	held := session.HeldItem()
	if held.IsEmpty() || !IsBeeBreedItem(held.ResolvedID()) {
		return false, 0
	}
	return s.InteractWithAnimal(session, entityID)
}

// TickBees advances one tick of AI for all living `minecraft:bee` entities across all dimensions.
func (s *Server) TickBees(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	targets := s.appendPlayerTargets(nil)
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		ent := allEnts[i]
		if canonicalResourceName(ent.TypeName) != MobTypeBee {
			continue
		}
		cache := s.DimensionChunks(ent.EffectiveDimension())
		s.TickBeeAI(&ent, cache, targets, rng)
	}
}
