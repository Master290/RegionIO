package server

import (
	"math"
	"math/rand"
	"strings"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// --- Vanilla 26.1.2 Villager Schedule, Breeding, Golem Aggro & Wandering Trader Constants ---

type VillagerActivity string

const (
	VillagerActivityIdle  VillagerActivity = "idle"
	VillagerActivityWork  VillagerActivity = "work"
	VillagerActivityMeet  VillagerActivity = "meet"
	VillagerActivityRest  VillagerActivity = "rest"
	VillagerActivityPanic VillagerActivity = "panic"

	VillagerWorkStartTick int64 = 2000
	VillagerWorkEndTick   int64 = 9000
	VillagerMeetStartTick int64 = 9000
	VillagerMeetEndTick   int64 = 11000
	VillagerRestStartTick int64 = 12000
	NitwitRestStartTick   int64 = 14000
	NitwitWakeTick        int64 = 2000

	VillagerBedScanRadius   = 16
	VillagerBedScanVertical = 4

	VillagerBreedingFoodThreshold = 12
	VillagerBreedingCooldownTicks = 6000
	VillagerGiftCooldownTicks     = 6000
	VillagerBellAlertRadius       = 32.0
	VillagerBellRaiderGlowRadius  = 48.0
	VillagerBellPanicTicks        = 60

	// Iron Golem crack stages & village reputation hostility threshold (vanilla 26.1.2).
	IronGolemHostileReputationThreshold = -15
	IronGolemPlayerAngerTicks           = 600
	IronGolemOfferPoppyDurationTicks    = 400

	EntityEventIronGolemWithdrawPoppy byte = 34
	SoundIronGolemDamage                   = "minecraft:entity.iron_golem.damage"

	// Wandering Trader 26.1.2 buying offer pool size.
	WanderingTraderBuyingOfferCount = 2
)

// IronGolemCrackLevel represents the 4 visual crackiness stages of an Iron Golem
// in vanilla 26.1.2 (Crackiness.Level: NONE, LOW, MEDIUM, HIGH).
type IronGolemCrackLevel int

const (
	IronGolemCrackNone   IronGolemCrackLevel = 0 // health ratio >= 0.75 (75..100 HP)
	IronGolemCrackLow    IronGolemCrackLevel = 1 // health ratio 0.50..0.74 (50..74 HP)
	IronGolemCrackMedium IronGolemCrackLevel = 2 // health ratio 0.25..0.49 (25..49 HP)
	IronGolemCrackHigh   IronGolemCrackLevel = 3 // health ratio < 0.25 (< 25 HP)
)

// String returns the canonical vanilla crackiness stage name.
func (c IronGolemCrackLevel) String() string {
	switch c {
	case IronGolemCrackLow:
		return "low"
	case IronGolemCrackMedium:
		return "medium"
	case IronGolemCrackHigh:
		return "high"
	default:
		return "none"
	}
}

type villagerPolishEntry struct {
	FoodPoints       int
	BreedCooldown    int
	GiftCooldown     int
	PanicTicks       int
	Sleeping         bool
	LastSleptTick    int64
	LastGossipedTick int64
	LastPanickedTick int64
}

type golemPolishEntry struct {
	AngryAtPlayer    [16]byte
	HasAngryPlayer   bool
	AngerTicks       int
	OfferPoppyTicks  int
	OfferTargetID    int32
}

type villagerPolishTracker struct {
	mu        sync.Mutex
	villagers map[int32]*villagerPolishEntry
	golems    map[int32]*golemPolishEntry
}

var (
	villagerPolishRegMu sync.Mutex
	villagerPolishReg   = make(map[*Server]*villagerPolishTracker)
)

func (s *Server) villagerPolish() *villagerPolishTracker {
	villagerPolishRegMu.Lock()
	defer villagerPolishRegMu.Unlock()
	tr := villagerPolishReg[s]
	if tr == nil {
		tr = &villagerPolishTracker{
			villagers: make(map[int32]*villagerPolishEntry),
			golems:    make(map[int32]*golemPolishEntry),
		}
		villagerPolishReg[s] = tr
	}
	return tr
}

func (tr *villagerPolishTracker) ensureVillagerLocked(id int32) *villagerPolishEntry {
	e := tr.villagers[id]
	if e == nil {
		e = &villagerPolishEntry{}
		tr.villagers[id] = e
	}
	return e
}

func (tr *villagerPolishTracker) ensureGolemLocked(id int32) *golemPolishEntry {
	e := tr.golems[id]
	if e == nil {
		e = &golemPolishEntry{}
		tr.golems[id] = e
	}
	return e
}

// --- 1. Villager Daily Schedule, Bed POI Claiming & Bell Ringing ---

// VillagerScheduleActivity returns the vanilla 26.1.2 daily schedule activity
// for a villager given its profession, baby status, and time of day (0..23999).
//   - Babies: Idle/Play during 0..11999, Rest during 12000..23999.
//   - Nitwits: Rest during 14000..23999 and 0..1999 (sleep 2000 ticks later and
//     wake 2000 ticks later than normal villagers), Idle during 2000..13999.
//   - Unemployed adults ("minecraft:none"): Idle during 0..8999 and 11000..11999,
//     Meet during 9000..10999, Rest during 12000..23999.
//   - Employed adults: Idle 0..1999, Work 2000..8999, Meet 9000..10999,
//     Idle 11000..11999, Rest 12000..23999.
func VillagerScheduleActivity(profession string, isBaby bool, timeOfDay int64) VillagerActivity {
	tod := ((timeOfDay % 24000) + 24000) % 24000
	if isBaby {
		if tod >= VillagerRestStartTick {
			return VillagerActivityRest
		}
		return VillagerActivityIdle
	}
	prof := canonicalResourceName(profession)
	if prof == VillagerProfessionNitwit {
		if tod >= NitwitRestStartTick || tod < NitwitWakeTick {
			return VillagerActivityRest
		}
		return VillagerActivityIdle
	}
	switch {
	case tod >= VillagerRestStartTick:
		return VillagerActivityRest
	case tod >= VillagerMeetStartTick && tod < VillagerMeetEndTick:
		return VillagerActivityMeet
	case tod >= VillagerWorkStartTick && tod < VillagerWorkEndTick:
		if CanProfessionTrade(prof) {
			return VillagerActivityWork
		}
		return VillagerActivityIdle
	default:
		return VillagerActivityIdle
	}
}

// VillagerCurrentActivity returns the active schedule state of villager entityID,
// overriding with VillagerActivityPanic if the villager is currently panicked.
func (s *Server) VillagerCurrentActivity(entityID int32) VillagerActivity {
	if s == nil || entityID <= 0 {
		return VillagerActivityIdle
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	if e := vp.villagers[entityID]; e != nil && e.PanicTicks > 0 {
		vp.mu.Unlock()
		return VillagerActivityPanic
	}
	vp.mu.Unlock()

	vs, ok := s.VillagerState(entityID)
	prof := VillagerProfessionNone
	if ok {
		prof = vs.Profession
	}
	return VillagerScheduleActivity(prof, s.IsMobBaby(entityID), s.TimeOfDay())
}

// BedOwnerAt returns the villager entityID that has claimed the bed at (dim, x, y, z), if any.
func (s *Server) BedOwnerAt(dim string, x, y, z int) (int32, bool) {
	if s == nil {
		return 0, false
	}
	normDim := world.NormalizeDimension(dim)
	key := dimBlockKey{dim: normDim, pos: world.BlockPos{X: x, Y: y, Z: z}}
	sm := s.villagerSocial()
	sm.mu.Lock()
	ownerID, ok := sm.entityByBed[key]
	if ok && s.entities != nil {
		if ent := s.entities.Get(ownerID); ent == nil || ent.TypeName != MobTypeVillager {
			delete(sm.entityByBed, key)
			delete(sm.bedByEntity, ownerID)
			ok = false
			ownerID = 0
		}
	}
	sm.mu.Unlock()
	return ownerID, ok
}

// ReleaseVillagerBed releases any bed POI claimed by entityID.
func (s *Server) ReleaseVillagerBed(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	sm := s.villagerSocial()
	sm.mu.Lock()
	site, ok := sm.bedByEntity[entityID]
	if ok {
		delete(sm.bedByEntity, entityID)
		delete(sm.entityByBed, dimBlockKey{dim: site.Dimension, pos: site.Pos})
	}
	sm.mu.Unlock()

	vp := s.villagerPolish()
	vp.mu.Lock()
	if e := vp.villagers[entityID]; e != nil {
		e.Sleeping = false
	}
	vp.mu.Unlock()
	return ok
}

// ValidateVillagerBed checks whether the villager's claimed bed block still exists.
// If broken, releases the bed POI claim.
func (s *Server) ValidateVillagerBed(entityID int32) bool {
	site, ok := s.VillagerBedSite(entityID)
	if !ok {
		return false
	}
	cache := s.DimensionChunks(site.Dimension)
	if cache == nil {
		return true
	}
	if IsBedBlock(cache.GetBlock(site.Pos.X, site.Pos.Y, site.Pos.Z)) {
		return true
	}
	s.ReleaseVillagerBed(entityID)
	return false
}

// TryAcquireVillagerBed searches within 16x4x16 blocks around the villager for
// the closest unclaimed bed block and claims it.
func (s *Server) TryAcquireVillagerBed(entityID int32) (VillagerPOISite, bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return VillagerPOISite{}, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != MobTypeVillager {
		return VillagerPOISite{}, false
	}
	if site, ok := s.VillagerBedSite(entityID); ok {
		if s.ValidateVillagerBed(entityID) {
			return site, true
		}
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return VillagerPOISite{}, false
	}
	bx0 := int(math.Floor(ent.X))
	by0 := int(math.Floor(ent.Y))
	bz0 := int(math.Floor(ent.Z))

	bestDistSq := math.MaxFloat64
	var bestPos world.BlockPos
	found := false

	for dy := -VillagerBedScanVertical; dy <= VillagerBedScanVertical; dy++ {
		by := by0 + dy
		if by < world.MinY || by >= world.MinY+world.WorldHeight {
			continue
		}
		for dx := -VillagerBedScanRadius; dx <= VillagerBedScanRadius; dx++ {
			for dz := -VillagerBedScanRadius; dz <= VillagerBedScanRadius; dz++ {
				distSq := float64(dx*dx + dy*dy + dz*dz)
				if distSq >= bestDistSq {
					continue
				}
				bx := bx0 + dx
				bz := bz0 + dz
				if !IsBedBlock(cache.GetBlock(bx, by, bz)) {
					continue
				}
				if owner, claimed := s.BedOwnerAt(dim, bx, by, bz); claimed && owner != entityID {
					continue
				}
				bestDistSq = distSq
				bestPos = world.BlockPos{X: bx, Y: by, Z: bz}
				found = true
			}
		}
	}
	if !found {
		return VillagerPOISite{}, false
	}
	if !s.ClaimVillagerBed(entityID, dim, bestPos.X, bestPos.Y, bestPos.Z) {
		return VillagerPOISite{}, false
	}
	return s.VillagerBedSite(entityID)
}

// IsVillagerSleeping reports whether the villager is currently sleeping in its bed.
func (s *Server) IsVillagerSleeping(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	defer vp.mu.Unlock()
	e := vp.villagers[entityID]
	return e != nil && e.Sleeping
}

// WakeSleepingVillager wakes a sleeping villager and heals 2.0 HP if waking at dawn.
func (s *Server) WakeSleepingVillager(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	e := vp.villagers[entityID]
	if e == nil || !e.Sleeping {
		vp.mu.Unlock()
		return false
	}
	e.Sleeping = false
	vp.mu.Unlock()

	hp := s.MobHealth(entityID)
	if hp > 0 && hp < DefaultVillagerMaxHealth {
		next := hp + 2.0
		if next > DefaultVillagerMaxHealth {
			next = DefaultVillagerMaxHealth
		}
		s.SetMobHealth(entityID, next)
	}
	return true
}

// RecordVillagerSlept records the game tick when villager entityID last slept in a bed.
func (s *Server) RecordVillagerSlept(entityID int32, tick int64) {
	if s == nil || entityID <= 0 {
		return
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	vp.ensureVillagerLocked(entityID).LastSleptTick = tick
	vp.mu.Unlock()
}

// RecordVillagerGossiped records the game tick when villager entityID last gossiped.
func (s *Server) RecordVillagerGossiped(entityID int32, tick int64) {
	if s == nil || entityID <= 0 {
		return
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	vp.ensureVillagerLocked(entityID).LastGossipedTick = tick
	vp.mu.Unlock()
}

// RecordVillagerPanicked records the game tick when villager entityID last panicked.
func (s *Server) RecordVillagerPanicked(entityID int32, tick int64) {
	if s == nil || entityID <= 0 {
		return
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	vp.ensureVillagerLocked(entityID).LastPanickedTick = tick
	vp.mu.Unlock()
}

// OnVillageBellRung handles ringing a `minecraft:bell` at (dim, x, y, z):
//   - Wakes sleeping villagers within 32 blocks and puts villagers into panic/hide
//     mode for 60 ticks.
//   - Applies Glowing (`EffectGlowing`, 60 ticks) to any raid mobs within 48 blocks
//     (vanilla 26.1.2 bell resonance).
func (s *Server) OnVillageBellRung(dim string, x, y, z int) (alertedVillagers int, glowingRaiders int) {
	if s == nil || s.entities == nil {
		return 0, 0
	}
	normDim := world.NormalizeDimension(dim)
	bx := float64(x) + 0.5
	by := float64(y) + 0.5
	bz := float64(z) + 0.5
	vilRadSq := VillagerBellAlertRadius * VillagerBellAlertRadius
	raidRadSq := VillagerBellRaiderGlowRadius * VillagerBellRaiderGlowRadius
	tick, _ := s.WorldTime()

	vp := s.villagerPolish()
	for _, ent := range s.entities.AppendAll(nil) {
		if ent.EffectiveDimension() != normDim {
			continue
		}
		dx := ent.X - bx
		dy := ent.Y - by
		dz := ent.Z - bz
		distSq := dx*dx + dy*dy + dz*dz
		if ent.TypeName == MobTypeVillager && distSq <= vilRadSq {
			s.WakeSleepingVillager(ent.ID)
			s.RecordVillagerPanicked(ent.ID, tick)
			vp.mu.Lock()
			e := vp.ensureVillagerLocked(ent.ID)
			e.PanicTicks = VillagerBellPanicTicks
			vp.mu.Unlock()
			alertedVillagers++
		} else if IsRaiderEntityType(ent.TypeName) && distSq <= raidRadSq {
			s.ApplyEntityEffectScaled(ent.ID, NewStatusEffectInstance(EffectGlowing, VillagerBellPanicTicks, 0), 1.0)
			glowingRaiders++
		}
	}
	return alertedVillagers, glowingRaiders
}

// stepVillagerPolishTick runs per-villager schedule, POI workstation acquisition,
// bed sleeping, and gossip sharing during TickVillagers.
func (s *Server) stepVillagerPolishTick(entityID int32, tod int64, tick int64) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != MobTypeVillager {
		return
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	ve := vp.ensureVillagerLocked(entityID)
	if ve.BreedCooldown > 0 {
		ve.BreedCooldown--
	}
	if ve.GiftCooldown > 0 {
		ve.GiftCooldown--
	}
	if ve.PanicTicks > 0 {
		ve.PanicTicks--
	}
	wasSleeping := ve.Sleeping
	vp.mu.Unlock()

	vs, ok := s.VillagerState(entityID)
	if !ok {
		return
	}
	isBaby := s.IsMobBaby(entityID)
	act := VillagerScheduleActivity(vs.Profession, isBaby, tod)

	switch act {
	case VillagerActivityWork:
		if wasSleeping {
			s.WakeSleepingVillager(entityID)
		}
		if !isBaby && vs.Profession != VillagerProfessionNitwit {
			_, _ = s.TryAcquireVillagerWorkstation(entityID)
		}
	case VillagerActivityIdle:
		if wasSleeping {
			s.WakeSleepingVillager(entityID)
		}
		// Unemployed adult villagers also look for unclaimed workstations during daytime.
		if !isBaby && canonicalResourceName(vs.Profession) == VillagerProfessionNone && tod >= VillagerWorkStartTick && tod < VillagerRestStartTick {
			_, _ = s.TryAcquireVillagerWorkstation(entityID)
		}
	case VillagerActivityMeet:
		if wasSleeping {
			s.WakeSleepingVillager(entityID)
		}
		// Share gossips with nearby villagers within 4 blocks during meeting hours.
		dim := ent.EffectiveDimension()
		for _, other := range s.entities.AppendAll(nil) {
			if other.ID <= entityID || other.TypeName != MobTypeVillager || other.EffectiveDimension() != dim {
				continue
			}
			dx := other.X - ent.X
			dy := other.Y - ent.Y
			dz := other.Z - ent.Z
			if dx*dx+dy*dy+dz*dz <= 16.0 {
				s.ShareVillagerGossip(entityID, other.ID)
				s.ShareVillagerGossip(other.ID, entityID)
				s.RecordVillagerGossiped(entityID, tick)
				s.RecordVillagerGossiped(other.ID, tick)
			}
		}
	case VillagerActivityRest:
		if _, hasBed := s.TryAcquireVillagerBed(entityID); hasBed {
			vp.mu.Lock()
			vp.ensureVillagerLocked(entityID).Sleeping = true
			vp.mu.Unlock()
			s.RecordVillagerSlept(entityID, tick)
		}
	}
}

// --- 2. Villager Food, Sharing, Breeding & Biome Variants ---

// VillagerFoodValue returns the vanilla 26.1.2 breeding food point value of an item:
//   - `minecraft:bread`: 4 points
//   - `minecraft:carrot`, `minecraft:potato`, `minecraft:beetroot`: 1 point
//   - all other items: 0 points
func VillagerFoodValue(itemName string) int {
	switch canonicalResourceName(itemName) {
	case "minecraft:bread":
		return 4
	case "minecraft:carrot", "minecraft:potato", "minecraft:beetroot":
		return 1
	default:
		return 0
	}
}

// VillagerFoodPoints returns the current breeding food points stored on villager entityID.
func (s *Server) VillagerFoodPoints(entityID int32) int {
	if s == nil || entityID <= 0 {
		return 0
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	defer vp.mu.Unlock()
	if e := vp.villagers[entityID]; e != nil {
		return e.FoodPoints
	}
	return 0
}

// SetVillagerFoodPoints sets the breeding food points stored on villager entityID.
func (s *Server) SetVillagerFoodPoints(entityID int32, pts int) {
	if s == nil || entityID <= 0 {
		return
	}
	if pts < 0 {
		pts = 0
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	vp.ensureVillagerLocked(entityID).FoodPoints = pts
	vp.mu.Unlock()
}

// AddVillagerFood feeds count items of itemName to villager entityID if it is a
// valid villager food item, returning the new food point total.
func (s *Server) AddVillagerFood(entityID int32, itemName string, count int) (newTotal int, accepted bool) {
	if s == nil || s.entities == nil || entityID <= 0 || count <= 0 {
		return 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != MobTypeVillager {
		return 0, false
	}
	val := VillagerFoodValue(itemName)
	if val <= 0 {
		return s.VillagerFoodPoints(entityID), false
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	e := vp.ensureVillagerLocked(entityID)
	e.FoodPoints += val * count
	newTotal = e.FoodPoints
	vp.mu.Unlock()
	return newTotal, true
}

// CanVillagerBreed reports whether villager entityID is an adult villager with
// at least 12 food points and not on breeding cooldown.
func (s *Server) CanVillagerBreed(entityID int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != MobTypeVillager || s.IsMobBaby(entityID) {
		return false
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	defer vp.mu.Unlock()
	e := vp.villagers[entityID]
	return e != nil && e.FoodPoints >= VillagerBreedingFoodThreshold && e.BreedCooldown <= 0
}

// ShareVillagerFood transfers 12 food points from giverID to receiverID when
// giverID has >= 24 food points and receiverID has < 12 food points.
func (s *Server) ShareVillagerFood(giverID, receiverID int32) bool {
	if s == nil || giverID <= 0 || receiverID <= 0 || giverID == receiverID {
		return false
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	defer vp.mu.Unlock()
	g := vp.ensureVillagerLocked(giverID)
	r := vp.ensureVillagerLocked(receiverID)
	if g.FoodPoints < VillagerBreedingFoodThreshold*2 || r.FoodPoints >= VillagerBreedingFoodThreshold {
		return false
	}
	transfer := g.FoodPoints / 2
	g.FoodPoints -= transfer
	r.FoodPoints += transfer
	return true
}

// VillagerTypeForBiome returns the canonical villager biome outfit variant
// (`minecraft:desert`, `minecraft:jungle`, `minecraft:plains`, `minecraft:savanna`,
// `minecraft:snow`, `minecraft:swamp`, `minecraft:taiga`) for a biome identifier.
func VillagerTypeForBiome(biomeName string) string {
	b := strings.TrimPrefix(canonicalResourceName(biomeName), "minecraft:")
	switch {
	case strings.Contains(b, "desert") || strings.Contains(b, "badlands") || strings.Contains(b, "eroded_badlands") || strings.Contains(b, "wooded_badlands"):
		return VillagerTypeDesert
	case strings.Contains(b, "jungle"):
		return VillagerTypeJungle
	case strings.Contains(b, "savanna"):
		return VillagerTypeSavanna
	case strings.Contains(b, "snow") || strings.Contains(b, "frozen") || strings.Contains(b, "ice_spikes") || strings.Contains(b, "jagged_peaks"):
		return VillagerTypeSnow
	case strings.Contains(b, "swamp"):
		return VillagerTypeSwamp
	case strings.Contains(b, "taiga"):
		return VillagerTypeTaiga
	default:
		return VillagerTypePlains
	}
}

func isValidVillagerTypeName(name string) bool {
	norm := canonicalResourceName(name)
	for _, t := range villagerTypesOrdered {
		if t == norm {
			return true
		}
	}
	return false
}

// SpawnVillagerInDimension spawns a `minecraft:villager` in dim at (x, y, z)
// with the specified biome variant (villagerType), profession, and badge level (1..5).
func (s *Server) SpawnVillagerInDimension(dim string, x, y, z float64, villagerType, profession string, level int) (int32, error) {
	id, err := s.SpawnVillager(x, y, z, profession, level)
	if err != nil {
		return 0, err
	}
	normDim := world.NormalizeDimension(dim)
	s.entities.Update(id, func(e *world.Entity) {
		e.Dimension = normDim
	})
	if vType := canonicalResourceName(villagerType); isValidVillagerTypeName(vType) && vType != VillagerTypePlains {
		if vs, ok := s.VillagerState(id); ok {
			vs.VillagerType = vType
			s.SetVillagerState(id, vs)
		}
	}
	return id, nil
}

// RollChildVillagerType selects a baby villager's biome variant using vanilla
// 26.1.2 genetics: 50% chance from the birth biome, 25% from parent A, 25% from parent B.
func RollChildVillagerType(biomeName, parentAType, parentBType string, rng *rand.Rand) string {
	biomeVariant := VillagerTypeForBiome(biomeName)
	pa := canonicalResourceName(parentAType)
	if !isValidVillagerTypeName(pa) {
		pa = biomeVariant
	}
	pb := canonicalResourceName(parentBType)
	if !isValidVillagerTypeName(pb) {
		pb = biomeVariant
	}
	if rng == nil {
		return biomeVariant
	}
	r := rng.Float32()
	switch {
	case r < 0.50:
		return biomeVariant
	case r < 0.75:
		return pa
	default:
		return pb
	}
}

// TryBreedVillagers breeds two willing adult villagers (each with >= 12 food
// points and off cooldown), consuming 12 food points from each parent and
// spawning a baby `minecraft:villager` with `minecraft:none` profession.
func (s *Server) TryBreedVillagers(parentA, parentB int32, rng *rand.Rand) (int32, bool) {
	if s == nil || s.entities == nil || parentA <= 0 || parentB <= 0 || parentA == parentB {
		return 0, false
	}
	entA := s.entities.Get(parentA)
	entB := s.entities.Get(parentB)
	if entA == nil || entB == nil || entA.TypeName != MobTypeVillager || entB.TypeName != MobTypeVillager {
		return 0, false
	}
	dim := entA.EffectiveDimension()
	if entB.EffectiveDimension() != dim {
		return 0, false
	}
	if !s.CanVillagerBreed(parentA) || !s.CanVillagerBreed(parentB) {
		return 0, false
	}

	vp := s.villagerPolish()
	vp.mu.Lock()
	ea := vp.ensureVillagerLocked(parentA)
	eb := vp.ensureVillagerLocked(parentB)
	if ea.FoodPoints < VillagerBreedingFoodThreshold || eb.FoodPoints < VillagerBreedingFoodThreshold || ea.BreedCooldown > 0 || eb.BreedCooldown > 0 {
		vp.mu.Unlock()
		return 0, false
	}
	ea.FoodPoints -= VillagerBreedingFoodThreshold
	eb.FoodPoints -= VillagerBreedingFoodThreshold
	ea.BreedCooldown = VillagerBreedingCooldownTicks
	eb.BreedCooldown = VillagerBreedingCooldownTicks
	vp.mu.Unlock()

	vsA, _ := s.VillagerState(parentA)
	vsB, _ := s.VillagerState(parentB)
	cx := (entA.X + entB.X) * 0.5
	cy := (entA.Y + entB.Y) * 0.5
	cz := (entA.Z + entB.Z) * 0.5

	childType := RollChildVillagerType("minecraft:plains", vsA.VillagerType, vsB.VillagerType, rng)
	childID, err := s.SpawnVillagerInDimension(dim, cx, cy, cz, childType, VillagerProfessionNone, MinVillagerLevel)
	if err != nil || childID <= 0 {
		return 0, false
	}
	s.SetMobBaby(childID, true)

	cp := entA.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(parentA, EntityEventLoveHearts))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(parentB, EntityEventLoveHearts))
	return childID, true
}

// --- 3. Hero of the Village Gifts & Lightning Conversion to Witch ---

// IsRaiderEntityType reports whether typeName is a raid-participating illager/witch/ravager.
func IsRaiderEntityType(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypePillager, MobTypeVindicator, MobTypeEvoker, MobTypeRavager, MobTypeWitch, MobTypeIllusioner:
		return true
	default:
		return false
	}
}

// TryThrowHeroOfTheVillageGift causes an employed adult villager to toss a
// profession-specific gift item toward a nearby player who has Hero of the Village.
func (s *Server) TryThrowHeroOfTheVillageGift(villagerID int32, player *PlayerSession, rng *rand.Rand) (world.ItemStack, bool) {
	if s == nil || s.entities == nil || villagerID <= 0 || player == nil || player.IsDead() {
		return world.ItemStack{}, false
	}
	if _, hasHero := player.PlayerEffect(EffectHeroOfTheVillage); !hasHero {
		return world.ItemStack{}, false
	}
	ent := s.entities.Get(villagerID)
	if ent == nil || ent.TypeName != MobTypeVillager || s.IsMobBaby(villagerID) || s.IsVillagerSleeping(villagerID) {
		return world.ItemStack{}, false
	}
	vs, ok := s.VillagerState(villagerID)
	if !ok || !CanProfessionTrade(vs.Profession) {
		return world.ItemStack{}, false
	}
	pSnap := player.Snapshot()
	if ent.EffectiveDimension() != world.NormalizeDimension(player.Dimension()) {
		return world.ItemStack{}, false
	}
	dx := pSnap.X - ent.X
	dy := pSnap.Y - ent.Y
	dz := pSnap.Z - ent.Z
	if dx*dx+dy*dy+dz*dz > 25.0 { // 5-block gift range
		return world.ItemStack{}, false
	}

	vp := s.villagerPolish()
	vp.mu.Lock()
	ve := vp.ensureVillagerLocked(villagerID)
	if ve.GiftCooldown > 0 {
		vp.mu.Unlock()
		return world.ItemStack{}, false
	}
	gift := RollHeroOfTheVillageGift(vs.Profession, rng)
	if gift.IsEmpty() {
		vp.mu.Unlock()
		return world.ItemStack{}, false
	}
	ve.GiftCooldown = VillagerGiftCooldownTicks
	vp.mu.Unlock()

	s.SpawnItemEntityInDimension(ent.EffectiveDimension(), ent.X, ent.Y+0.5, ent.Z, gift)
	return gift, true
}

// OnVillagerStruckByLightning converts a `minecraft:villager` struck by
// lightning into a `minecraft:witch` on non-Peaceful difficulty (vanilla 26.1.2).
func (s *Server) OnVillagerStruckByLightning(villagerID int32) (witchID int32, converted bool) {
	if s == nil || s.entities == nil || villagerID <= 0 {
		return 0, false
	}
	if s.Difficulty() == DifficultyPeaceful {
		return 0, false
	}
	ent := s.entities.Get(villagerID)
	if ent == nil || ent.TypeName != MobTypeVillager {
		return 0, false
	}
	dim := ent.EffectiveDimension()
	x, y, z := ent.X, ent.Y, ent.Z
	cp := ent.ChunkPos()

	s.ReleaseVillagerWorkstation(villagerID)
	s.ReleaseVillagerBed(villagerID)
	s.closeMerchantScreensForVillager(villagerID)

	s.entities.Remove(villagerID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{villagerID}))

	wID, err := s.SpawnMobInDimension(dim, x, y, z, MobTypeWitch)
	if err != nil || wID <= 0 {
		return 0, false
	}
	return wID, true
}

// --- 4. Zombie Villager Difficulty-Scaled Infection & Catalyst Count ---

// ZombieInfectionChanceForDifficulty returns the vanilla 26.1.2 probability
// (0.0..1.0) that a villager killed by a zombie-family mob converts into a
// `minecraft:zombie_villager`:
//   - Peaceful / Easy: 0% (0.0)
//   - Normal: 50% (0.5)
//   - Hard: 100% (1.0)
func ZombieInfectionChanceForDifficulty(diff Difficulty) float32 {
	switch diff {
	case DifficultyNormal:
		return 0.5
	case DifficultyHard:
		return 1.0
	default:
		return 0.0
	}
}

// TryZombifyVillagerOnZombieKill attempts to convert a villager killed by a
// zombie-family mob (`zombie`, `zombie_villager`, `husk`, `drowned`,
// `zombified_piglin`) into a `minecraft:zombie_villager` according to server
// difficulty (0% Easy, 50% Normal, 100% Hard). Preserves baby state,
// VillagerType, Profession, Level, XP, Offers, and Gossips.
func (s *Server) TryZombifyVillagerOnZombieKill(villagerID int32, killerMobType string, rng *rand.Rand) (zombieVillagerID int32, converted bool) {
	if s == nil || s.entities == nil || villagerID <= 0 {
		return 0, false
	}
	if killerMobType != "" && !IsZombieFamilyMob(killerMobType) {
		return 0, false
	}
	chance := ZombieInfectionChanceForDifficulty(s.Difficulty())
	if chance <= 0 {
		return 0, false
	}
	if chance < 1.0 {
		if rng == nil {
			sm := s.villagerSocial()
			sm.mu.Lock()
			roll := sm.rng.Float32()
			sm.mu.Unlock()
			if roll >= chance {
				return 0, false
			}
		} else if rng.Float32() >= chance {
			return 0, false
		}
	}
	return s.ZombifyVillager(villagerID)
}

// CountZombieCureCatalystsAt counts `minecraft:iron_bars` and `*bed` blocks
// within a 9x9x9 cube centered at (x, y, z) in dim, capped at
// MaxZombieVillagerCureCatalysts (14).
func (s *Server) CountZombieCureCatalystsAt(dim string, x, y, z float64) int {
	if s == nil {
		return 0
	}
	cache := s.DimensionChunks(world.NormalizeDimension(dim))
	if cache == nil {
		return 0
	}
	cx, cy, cz := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	count := 0
	for dx := -4; dx <= 4 && count < 14; dx++ {
		for dy := -4; dy <= 4 && count < 14; dy++ {
			for dz := -4; dz <= 4 && count < 14; dz++ {
				st := cache.GetBlock(cx+dx, cy+dy, cz+dz)
				if st == world.StateAir {
					continue
				}
				name := strings.ToLower(world.BlockName(st))
				if name == "minecraft:iron_bars" || IsBedBlock(st) {
					count++
				}
			}
		}
	}
	return count
}

// --- 5. Iron Golem Crack Stages, Village Reputation Aggro, Poppy Offer & Immunities ---

// IronGolemCrackLevelForHealth returns the visual crack stage of an Iron Golem
// for (health, maxHealth) matching vanilla 26.1.2 `Crackiness.BY_DAMAGE`.
func IronGolemCrackLevelForHealth(health, maxHealth float32) IronGolemCrackLevel {
	if maxHealth <= 0 {
		maxHealth = DefaultIronGolemMaxHealth
	}
	ratio := health / maxHealth
	switch {
	case ratio >= 0.75:
		return IronGolemCrackNone
	case ratio >= 0.50:
		return IronGolemCrackLow
	case ratio >= 0.25:
		return IronGolemCrackMedium
	default:
		return IronGolemCrackHigh
	}
}

// IronGolemCrackStage returns the current crack stage of Iron Golem entityID.
func (s *Server) IronGolemCrackStage(entityID int32) IronGolemCrackLevel {
	if s == nil || entityID <= 0 {
		return IronGolemCrackNone
	}
	return IronGolemCrackLevelForHealth(s.MobHealth(entityID), DefaultIronGolemMaxHealth)
}

// IsIronGolemImmuneToDamageCause reports whether an Iron Golem is immune to a
// given environmental damage cause (fall damage, drowning, powder snow freeze).
func IsIronGolemImmuneToDamageCause(cause string) bool {
	switch strings.ToLower(strings.TrimSpace(cause)) {
	case "fall", "drown", "drowning", "freeze", "freezing":
		return true
	default:
		return false
	}
}

// PlayerVillageReputationNear returns the lowest weighted villager reputation
// score that playerUUID has among villagers within radius blocks of (dim, x, y, z).
func (s *Server) PlayerVillageReputationNear(dim string, x, y, z float64, playerUUID [16]byte, radius float64) (minRep int, hasVillagers bool) {
	if s == nil || s.entities == nil || radius <= 0 {
		return 0, false
	}
	normDim := world.NormalizeDimension(dim)
	radSq := radius * radius
	for _, ent := range s.entities.AppendAll(nil) {
		if ent.TypeName != MobTypeVillager || ent.EffectiveDimension() != normDim {
			continue
		}
		dx := ent.X - x
		dy := ent.Y - y
		dz := ent.Z - z
		if dx*dx+dy*dy+dz*dz > radSq {
			continue
		}
		rep := s.VillagerPlayerReputation(ent.ID, playerUUID)
		if !hasVillagers || rep < minRep {
			minRep = rep
			hasVillagers = true
		}
	}
	return minRep, hasVillagers
}

// ShouldIronGolemAttackPlayer reports whether Iron Golem golemID is hostile
// toward player according to vanilla 26.1.2 rules:
//   - Player-created Iron Golems (`PlayerCreated == true`) NEVER attack players,
//     even if attacked or if the player has low village reputation.
//   - Village-spawned Iron Golems (`PlayerCreated == false`) attack survival/adventure
//     players who attacked a villager or golem, OR whose village reputation near
//     the golem is <= -15 (`IronGolemHostileReputationThreshold`).
func (s *Server) ShouldIronGolemAttackPlayer(golemID int32, player *PlayerSession) bool {
	if s == nil || s.entities == nil || golemID <= 0 || player == nil || player.IsDead() {
		return false
	}
	snap := player.Snapshot()
	if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false
	}
	if s.IsIronGolemPlayerCreated(golemID) {
		return false
	}
	ent := s.entities.Get(golemID)
	if ent == nil || ent.TypeName != MobTypeIronGolem {
		return false
	}

	vp := s.villagerPolish()
	vp.mu.Lock()
	ge := vp.golems[golemID]
	provoked := ge != nil && ge.HasAngryPlayer && ge.AngryAtPlayer == player.Profile.UUID && ge.AngerTicks > 0
	vp.mu.Unlock()
	if provoked {
		return true
	}

	minRep, hasVil := s.PlayerVillageReputationNear(ent.EffectiveDimension(), ent.X, ent.Y, ent.Z, player.Profile.UUID, 16.0)
	return hasVil && minRep <= IronGolemHostileReputationThreshold
}

// ProvokeVillageIronGolemsNear angers all non-player-created Iron Golems within
// 16 blocks of (dim, x, y, z) against attacker for 600 ticks.
func (s *Server) ProvokeVillageIronGolemsNear(dim string, x, y, z float64, attacker *PlayerSession) int {
	if s == nil || s.entities == nil || attacker == nil {
		return 0
	}
	snap := attacker.Snapshot()
	if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	radSq := 16.0 * 16.0
	vp := s.villagerPolish()
	count := 0
	for _, ent := range s.entities.AppendAll(nil) {
		if ent.TypeName != MobTypeIronGolem || ent.EffectiveDimension() != normDim {
			continue
		}
		if s.IsIronGolemPlayerCreated(ent.ID) {
			continue
		}
		dx := ent.X - x
		dy := ent.Y - y
		dz := ent.Z - z
		if dx*dx+dy*dy+dz*dz <= radSq {
			vp.mu.Lock()
			ge := vp.ensureGolemLocked(ent.ID)
			ge.AngryAtPlayer = attacker.Profile.UUID
			ge.HasAngryPlayer = true
			ge.AngerTicks = IronGolemPlayerAngerTicks
			vp.mu.Unlock()
			count++
		}
	}
	return count
}

// OnIronGolemDamaged handles crack-stage transition sounds (`entity.iron_golem.damage`)
// and player provocation when an Iron Golem takes damage.
func (s *Server) OnIronGolemDamaged(golemID int32, oldHP, newHP float32, attacker *PlayerSession) (crackIncreased bool) {
	if s == nil || s.entities == nil || golemID <= 0 {
		return false
	}
	ent := s.entities.Get(golemID)
	if ent == nil || ent.TypeName != MobTypeIronGolem {
		return false
	}
	oldStage := IronGolemCrackLevelForHealth(oldHP, DefaultIronGolemMaxHealth)
	newStage := IronGolemCrackLevelForHealth(newHP, DefaultIronGolemMaxHealth)
	if newHP > 0 && newStage > oldStage {
		crackIncreased = true
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundIronGolemDamage, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	}
	if attacker != nil && !s.IsIronGolemPlayerCreated(golemID) {
		s.ProvokeVillageIronGolemsNear(ent.EffectiveDimension(), ent.X, ent.Y, ent.Z, attacker)
	}
	return crackIncreased
}

// StartIronGolemOfferPoppy starts an Iron Golem's 400-tick poppy-offering pose
// toward a nearby villager (`EntityEventIronGolemOfferPoppy = 11`).
func (s *Server) StartIronGolemOfferPoppy(golemID, villagerID int32) bool {
	if s == nil || s.entities == nil || golemID <= 0 {
		return false
	}
	ent := s.entities.Get(golemID)
	if ent == nil || ent.TypeName != MobTypeIronGolem {
		return false
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	ge := vp.ensureGolemLocked(golemID)
	ge.OfferPoppyTicks = IronGolemOfferPoppyDurationTicks
	ge.OfferTargetID = villagerID
	vp.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(golemID, EntityEventIronGolemOfferPoppy))
	return true
}

// StopIronGolemOfferPoppy ends an Iron Golem's poppy-offering pose and
// broadcasts `EntityEventIronGolemWithdrawPoppy = 34`.
func (s *Server) StopIronGolemOfferPoppy(golemID int32) {
	if s == nil || s.entities == nil || golemID <= 0 {
		return
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	ge := vp.golems[golemID]
	if ge == nil || ge.OfferPoppyTicks <= 0 {
		vp.mu.Unlock()
		return
	}
	ge.OfferPoppyTicks = 0
	ge.OfferTargetID = 0
	vp.mu.Unlock()

	if ent := s.entities.Get(golemID); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayEntityEvent,
			EncodeEntityEventPacket(golemID, EntityEventIronGolemWithdrawPoppy))
	}
}

// IsIronGolemOfferingPoppy reports whether Iron Golem golemID is currently
// holding out a poppy to a villager.
func (s *Server) IsIronGolemOfferingPoppy(golemID int32) bool {
	if s == nil || golemID <= 0 {
		return false
	}
	vp := s.villagerPolish()
	vp.mu.Lock()
	defer vp.mu.Unlock()
	ge := vp.golems[golemID]
	return ge != nil && ge.OfferPoppyTicks > 0
}

// stepIronGolemPolishAndPlayerTarget ticks an Iron Golem's poppy-offering and
// player-anger timers and, if the golem is village-spawned and not chasing a
// hostile mob, pursues and attacks any hostile/low-reputation player within 16 blocks.
func (s *Server) stepIronGolemPolishAndPlayerTarget(golem *world.Entity, curCooldown int, rng *rand.Rand) bool {
	if s == nil || s.entities == nil || golem == nil {
		return false
	}
	vp := s.villagerPolish()
	var expiredPoppy bool
	vp.mu.Lock()
	if ge := vp.golems[golem.ID]; ge != nil {
		if ge.AngerTicks > 0 {
			ge.AngerTicks--
			if ge.AngerTicks == 0 {
				ge.HasAngryPlayer = false
			}
		}
		if ge.OfferPoppyTicks > 0 {
			ge.OfferPoppyTicks--
			if ge.OfferPoppyTicks == 0 {
				expiredPoppy = true
			}
		}
	}
	vp.mu.Unlock()

	if expiredPoppy {
		if ent := s.entities.Get(golem.ID); ent != nil {
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayEntityEvent,
				EncodeEntityEventPacket(golem.ID, EntityEventIronGolemWithdrawPoppy))
		}
	}

	if s.IsIronGolemPlayerCreated(golem.ID) {
		return false
	}

	dim := golem.EffectiveDimension()
	bestDistSq := IronGolemDefenseRange * IronGolemDefenseRange
	var targetPlayer *PlayerSession
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()
	for _, sess := range sessions {
		if sess == nil || world.NormalizeDimension(sess.Dimension()) != dim || !s.ShouldIronGolemAttackPlayer(golem.ID, sess) {
			continue
		}
		pSnap := sess.Snapshot()
		dx := pSnap.X - golem.X
		dy := pSnap.Y - golem.Y
		dz := pSnap.Z - golem.Z
		dSq := dx*dx + dy*dy + dz*dz
		if dSq <= bestDistSq {
			bestDistSq = dSq
			targetPlayer = sess
		}
	}
	if targetPlayer == nil {
		return false
	}

	// Stop poppy offer if entering combat.
	if s.IsIronGolemOfferingPoppy(golem.ID) {
		s.StopIronGolemOfferPoppy(golem.ID)
	}

	cache := s.DimensionChunks(dim)
	feetY := int(math.Floor(golem.Y + 0.01))
	pSnap := targetPlayer.Snapshot()
	s.stepEntityTowardPoint(golem, cache, feetY, pSnap.X, pSnap.Y, pSnap.Z, IronGolemChaseSpeed, 1.5)

	updatedGolem := s.entities.Get(golem.ID)
	if updatedGolem == nil {
		return true
	}
	dx := pSnap.X - updatedGolem.X
	dy := pSnap.Y - updatedGolem.Y
	dz := pSnap.Z - updatedGolem.Z
	if dx*dx+dy*dy+dz*dz <= IronGolemAttackReach*IronGolemAttackReach && curCooldown <= 0 {
		mgr := s.golemManager()
		mgr.mu.Lock()
		if st := mgr.golems[golem.ID]; st != nil {
			st.AttackCooldown = IronGolemAttackCooldownTicks
		}
		mgr.mu.Unlock()

		dmg := IronGolemMinAttackDamage
		if rng != nil {
			dmg += rng.Float32() * (IronGolemMaxAttackDamage - IronGolemMinAttackDamage)
		} else {
			dmg = 14.5
		}
		cp := updatedGolem.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
			EncodeEntityEventPacket(updatedGolem.ID, EntityEventIronGolemAttack))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:entity.iron_golem.attack", SoundSourceNeutral, updatedGolem.X, updatedGolem.Y, updatedGolem.Z, 1.0, 1.0, 0))
		s.DamagePlayerWithArmor(targetPlayer, ScaleDamageByDifficulty(dmg, s.Difficulty()), "mob")
	}
	return true
}

// --- 6. Wandering Trader 26.1.2 Buying Pool & Natural Spawning ---

type wanderingTraderBuyDef struct {
	WantItem     string
	WantCount    int32
	EmeraldGives int32
	MaxUses      int32
}

// wanderingTraderBuyingPool contains the vanilla 26.1.2 Wandering Trader buying
// trades where the trader purchases supplies from the player in exchange for emeralds.
var wanderingTraderBuyingPool = []wanderingTraderBuyDef{
	{WantItem: "minecraft:water_bucket", WantCount: 1, EmeraldGives: 2, MaxUses: 1},
	{WantItem: "minecraft:milk_bucket", WantCount: 1, EmeraldGives: 2, MaxUses: 1},
	{WantItem: "minecraft:fermented_spider_eye", WantCount: 1, EmeraldGives: 3, MaxUses: 1},
	{WantItem: "minecraft:baked_potato", WantCount: 4, EmeraldGives: 1, MaxUses: 1},
	{WantItem: "minecraft:hay_block", WantCount: 1, EmeraldGives: 1, MaxUses: 1},
}

// RollWanderingTraderBuyingOffers rolls 2 distinct buying offers from the
// vanilla 26.1.2 Wandering Trader buying pool (player sells item -> receives emeralds).
func RollWanderingTraderBuyingOffers(rng *rand.Rand) []MerchantOffer {
	_ = DefaultRecipeBook()
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	idx := make([]int, len(wanderingTraderBuyingPool))
	for i := range idx {
		idx[i] = i
	}
	rng.Shuffle(len(idx), func(i, j int) { idx[i], idx[j] = idx[j], idx[i] })

	offers := make([]MerchantOffer, 0, WanderingTraderBuyingOfferCount)
	for _, k := range idx {
		if len(offers) >= WanderingTraderBuyingOfferCount {
			break
		}
		d := wanderingTraderBuyingPool[k]
		costA := world.NewNamedItemStack(d.WantItem, d.WantCount)
		res := world.NewNamedItemStack("minecraft:emerald", d.EmeraldGives)
		if costA.IsEmpty() || res.IsEmpty() {
			continue
		}
		offers = append(offers, MerchantOffer{
			Name:            "minecraft:wandering_trader/buy_" + strings.TrimPrefix(d.WantItem, "minecraft:"),
			BaseCostA:       costA,
			Result:          res,
			MaxUses:         d.MaxUses,
			PriceMultiplier: 0.05,
		})
	}
	return offers
}

// RollWanderingTraderFullOffers2612 rolls the complete vanilla 26.1.2 Wandering
// Trader offer list: 2 buying offers + 5 ordinary selling offers + 1 special offer.
func RollWanderingTraderFullOffers2612(rng *rand.Rand) []MerchantOffer {
	buying := RollWanderingTraderBuyingOffers(rng)
	selling := RollWanderingTraderOffers(rng)
	out := make([]MerchantOffer, 0, len(buying)+len(selling))
	out = append(out, buying...)
	out = append(out, selling...)
	return out
}

// TrySpawnNaturalWanderingTrader attempts to spawn a natural Overworld
// `minecraft:wandering_trader` with 2 leashed `minecraft:trader_llama` escorts
// near the first eligible Overworld player when no wandering trader is currently
// active in the world.
func (s *Server) TrySpawnNaturalWanderingTrader(rng *rand.Rand) (int32, bool) {
	if s == nil || s.entities == nil {
		return 0, false
	}
	mgr := s.equineManager()
	mgr.mu.RLock()
	activeCount := len(mgr.traders)
	mgr.mu.RUnlock()
	if activeCount > 0 {
		return 0, false
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
	spawnX, spawnY, spawnZ := 0.5, 65.0, 0.5
	if targetSession != nil {
		snap := targetSession.Snapshot()
		spawnX = snap.X + 4.0
		spawnY = snap.Y
		spawnZ = snap.Z + 4.0
	}
	id, _, err := s.SpawnWanderingTrader(world.DimensionOverworld, spawnX, spawnY, spawnZ, true, rng)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
