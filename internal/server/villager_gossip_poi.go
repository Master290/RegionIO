package server

import (
	"errors"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// Zombie Villager entity constants and protocol 775 (Minecraft 26.1.2) IDs.
const (
	MobTypeZombieVillager = "minecraft:zombie_villager"

	// EntityTypeZombieVillagerID is BuiltInRegistries.ENTITY_TYPE ID for
	// "minecraft:zombie_villager" in Minecraft 26.1.2 (protocol 775).
	EntityTypeZombieVillagerID int = 152

	// DefaultZombieVillagerMaxHealth is the vanilla max health of a zombie villager (20 HP).
	DefaultZombieVillagerMaxHealth float32 = 20.0

	// SynchedEntityData indices on ZombieVillager in Minecraft 26.1.2.
	EntityDataZombieVillagerConvertingID byte = 19 // BOOLEAN serializer (8): true while curing
	EntityDataZombieVillagerDataID       byte = 20 // VILLAGER_DATA serializer (18): type, profession, level

	// ClientboundEntityEventPacket (0x22) event IDs for Zombie Villager and Villager gossip.
	EntityEventVillagerAngry      byte = 13
	EntityEventZombieVillagerCure byte = 16

	// Vanilla ZombieVillager conversion timer bounds (in game ticks at 20 TPS: 3..5 minutes).
	ZombieVillagerMinCureTicks = 3600
	ZombieVillagerMaxCureTicks = 6000
	ZombieVillagerNauseaTicks  = 200 // 10s of nausea upon conversion

	// Villager POI & Workstation scan parameters.
	VillagerWorkstationScanRadius   = 16
	VillagerWorkstationScanVertical = 4
	VillagerGossipShareRadius       = 2.0
	VillagerWitnessRadius           = 16.0
)

// GossipType identifies one of the 5 vanilla 26.1.2 villager gossip categories
// from net.minecraft.world.entity.ai.gossip.GossipType.
type GossipType string

const (
	GossipMajorNegative GossipType = "major_negative"
	GossipMinorNegative GossipType = "minor_negative"
	GossipMinorPositive GossipType = "minor_positive"
	GossipMajorPositive GossipType = "major_positive"
	GossipTrading       GossipType = "trading"
)

// GossipTypeSpec defines the vanilla 26.1.2 parameters for a GossipType:
// Weight (reputation multiplier), Max (per-villager cap), DecayPerDay (daily decay),
// and DecayPerTransfer (loss when shared with another villager).
type GossipTypeSpec struct {
	Type             GossipType
	Weight           int
	Max              int
	DecayPerDay      int
	DecayPerTransfer int
}

// vanillaGossipTypeSpecs matches net.minecraft.world.entity.ai.gossip.GossipType in 26.1.2:
//   - MAJOR_NEGATIVE("major_negative", -5, 100, 10, 10)
//   - MINOR_NEGATIVE("minor_negative", -1, 200, 20, 20)
//   - MINOR_POSITIVE("minor_positive", 1, 25, 1, 5)
//   - MAJOR_POSITIVE("major_positive", 5, 20, 0, 100)
//   - TRADING("trading", 1, 25, 2, 20)
var vanillaGossipTypeSpecs = map[GossipType]GossipTypeSpec{
	GossipMajorNegative: {Type: GossipMajorNegative, Weight: -5, Max: 100, DecayPerDay: 10, DecayPerTransfer: 10},
	GossipMinorNegative: {Type: GossipMinorNegative, Weight: -1, Max: 200, DecayPerDay: 20, DecayPerTransfer: 20},
	GossipMinorPositive: {Type: GossipMinorPositive, Weight: 1, Max: 25, DecayPerDay: 1, DecayPerTransfer: 5},
	GossipMajorPositive: {Type: GossipMajorPositive, Weight: 5, Max: 20, DecayPerDay: 0, DecayPerTransfer: 100},
	GossipTrading:       {Type: GossipTrading, Weight: 1, Max: 25, DecayPerDay: 2, DecayPerTransfer: 20},
}

var allGossipTypesOrdered = [5]GossipType{
	GossipMajorNegative,
	GossipMinorNegative,
	GossipMinorPositive,
	GossipMajorPositive,
	GossipTrading,
}

// AllGossipTypes returns all 5 vanilla 26.1.2 GossipTypes in order.
func AllGossipTypes() []GossipType {
	out := make([]GossipType, len(allGossipTypesOrdered))
	copy(out, allGossipTypesOrdered[:])
	return out
}

// LookupGossipTypeSpec returns the vanilla 26.1.2 GossipTypeSpec for t.
func LookupGossipTypeSpec(t GossipType) (GossipTypeSpec, bool) {
	spec, ok := vanillaGossipTypeSpecs[t]
	return spec, ok
}

// --- Workstation Block <-> Villager Profession POI Mapping ---

var workstationBlockToProfession = map[string]string{
	"minecraft:blast_furnace":        VillagerProfessionArmorer,
	"minecraft:smoker":               VillagerProfessionButcher,
	"minecraft:cartography_table":    VillagerProfessionCartographer,
	"minecraft:brewing_stand":        VillagerProfessionCleric,
	"minecraft:composter":            VillagerProfessionFarmer,
	"minecraft:barrel":               VillagerProfessionFisherman,
	"minecraft:fletching_table":      VillagerProfessionFletcher,
	"minecraft:cauldron":             VillagerProfessionLeatherworker,
	"minecraft:water_cauldron":       VillagerProfessionLeatherworker,
	"minecraft:lava_cauldron":        VillagerProfessionLeatherworker,
	"minecraft:powder_snow_cauldron": VillagerProfessionLeatherworker,
	"minecraft:lectern":              VillagerProfessionLibrarian,
	"minecraft:stonecutter":          VillagerProfessionMason,
	"minecraft:loom":                 VillagerProfessionShepherd,
	"minecraft:smithing_table":       VillagerProfessionToolsmith,
	"minecraft:grindstone":           VillagerProfessionWeaponsmith,
}

var professionToWorkstationBlocks = map[string][]string{
	VillagerProfessionArmorer:       {"minecraft:blast_furnace"},
	VillagerProfessionButcher:       {"minecraft:smoker"},
	VillagerProfessionCartographer:  {"minecraft:cartography_table"},
	VillagerProfessionCleric:        {"minecraft:brewing_stand"},
	VillagerProfessionFarmer:        {"minecraft:composter"},
	VillagerProfessionFisherman:     {"minecraft:barrel"},
	VillagerProfessionFletcher:      {"minecraft:fletching_table"},
	VillagerProfessionLeatherworker: {"minecraft:cauldron", "minecraft:water_cauldron", "minecraft:lava_cauldron", "minecraft:powder_snow_cauldron"},
	VillagerProfessionLibrarian:     {"minecraft:lectern"},
	VillagerProfessionMason:         {"minecraft:stonecutter"},
	VillagerProfessionShepherd:      {"minecraft:loom"},
	VillagerProfessionToolsmith:     {"minecraft:smithing_table"},
	VillagerProfessionWeaponsmith:   {"minecraft:grindstone"},
}

// ProfessionForWorkstationBlock returns the canonical villager profession
// associated with blockName (e.g. "minecraft:lectern" -> "minecraft:librarian"),
// or ("", false) if blockName is not a villager job-site block.
func ProfessionForWorkstationBlock(blockName string) (string, bool) {
	norm := canonicalResourceName(blockName)
	prof, ok := workstationBlockToProfession[norm]
	return prof, ok
}

// ProfessionForWorkstationState returns the canonical villager profession for a
// block state ID, or ("", false) if state is not a villager job-site block.
func ProfessionForWorkstationState(state uint16) (string, bool) {
	if state == world.StateAir {
		return "", false
	}
	return ProfessionForWorkstationBlock(world.BlockName(state))
}

// IsVillagerWorkstationBlock reports whether state is a villager job-site block.
func IsVillagerWorkstationBlock(state uint16) bool {
	_, ok := ProfessionForWorkstationState(state)
	return ok
}

// WorkstationBlocksForProfession returns the canonical block names that serve
// as job sites for profession.
func WorkstationBlocksForProfession(profession string) []string {
	norm := canonicalResourceName(profession)
	blocks := professionToWorkstationBlocks[norm]
	if len(blocks) == 0 {
		return nil
	}
	out := make([]string, len(blocks))
	copy(out, blocks)
	return out
}

// VillagerProfessionWorkSound returns the sound event played when a villager
// works/restocks at its job site.
func VillagerProfessionWorkSound(profession string) string {
	switch canonicalResourceName(profession) {
	case VillagerProfessionArmorer:
		return "minecraft:entity.villager.work_armorer"
	case VillagerProfessionButcher:
		return "minecraft:entity.villager.work_butcher"
	case VillagerProfessionCartographer:
		return "minecraft:entity.villager.work_cartographer"
	case VillagerProfessionCleric:
		return "minecraft:entity.villager.work_cleric"
	case VillagerProfessionFarmer:
		return "minecraft:entity.villager.work_farmer"
	case VillagerProfessionFisherman:
		return "minecraft:entity.villager.work_fisherman"
	case VillagerProfessionFletcher:
		return "minecraft:entity.villager.work_fletcher"
	case VillagerProfessionLeatherworker:
		return "minecraft:entity.villager.work_leatherworker"
	case VillagerProfessionLibrarian:
		return "minecraft:entity.villager.work_librarian"
	case VillagerProfessionMason:
		return "minecraft:entity.villager.work_mason"
	case VillagerProfessionShepherd:
		return "minecraft:entity.villager.work_shepherd"
	case VillagerProfessionToolsmith:
		return "minecraft:entity.villager.work_toolsmith"
	case VillagerProfessionWeaponsmith:
		return "minecraft:entity.villager.work_weaponsmith"
	default:
		return "minecraft:entity.villager.ambient"
	}
}

// --- POI & Gossip State Tracking ---

type dimBlockKey struct {
	dim string
	pos world.BlockPos
}

// VillagerPOISite represents a claimed job site or home bed block in a dimension.
type VillagerPOISite struct {
	Dimension  string
	Pos        world.BlockPos
	Profession string
}

// ZombieVillagerState tracks the villager data, gossip, and curing conversion
// progress of a `minecraft:zombie_villager` entity.
type ZombieVillagerState struct {
	EntityID        int32
	Dimension       string
	VillagerType    string
	Profession      string
	Level           int
	XP              int32
	Offers          []MerchantOffer
	Gossips         map[[16]byte]map[GossipType]int
	Weakened        bool
	Converting      bool
	ConversionTicks int
	CurerUUID       [16]byte
	HasCurer        bool
}

type serverVillagerSocialManager struct {
	mu              sync.Mutex
	jobSiteByEntity map[int32]VillagerPOISite
	entityByJobSite map[dimBlockKey]int32
	jobSiteLost     map[int32]bool
	bedByEntity     map[int32]VillagerPOISite
	entityByBed     map[dimBlockKey]int32
	gossips         map[int32]map[[16]byte]map[GossipType]int // entityID -> playerUUID -> GossipType -> value
	lastDecayDay    map[int32]int64
	zombieVillagers map[int32]*ZombieVillagerState
	rng             *rand.Rand
}

var serverVillagerSocial sync.Map // map[*Server]*serverVillagerSocialManager

func (s *Server) villagerSocial() *serverVillagerSocialManager {
	if v, ok := serverVillagerSocial.Load(s); ok {
		return v.(*serverVillagerSocialManager)
	}
	actual, _ := serverVillagerSocial.LoadOrStore(s, &serverVillagerSocialManager{
		jobSiteByEntity: make(map[int32]VillagerPOISite),
		entityByJobSite: make(map[dimBlockKey]int32),
		jobSiteLost:     make(map[int32]bool),
		bedByEntity:     make(map[int32]VillagerPOISite),
		entityByBed:     make(map[dimBlockKey]int32),
		gossips:         make(map[int32]map[[16]byte]map[GossipType]int),
		lastDecayDay:    make(map[int32]int64),
		zombieVillagers: make(map[int32]*ZombieVillagerState),
		rng:             rand.New(rand.NewSource(time.Now().UnixNano())),
	})
	return actual.(*serverVillagerSocialManager)
}

// VillagerJobSite returns the workstation POI currently claimed by the villager
// with entityID, if any.
func (s *Server) VillagerJobSite(entityID int32) (VillagerPOISite, bool) {
	if s == nil || entityID <= 0 {
		return VillagerPOISite{}, false
	}
	sm := s.villagerSocial()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	site, ok := sm.jobSiteByEntity[entityID]
	return site, ok
}

// WorkstationOwnerAt returns the entityID of the villager that has claimed the
// workstation block at (dim, x, y, z), or (0, false) if unclaimed.
func (s *Server) WorkstationOwnerAt(dim string, x, y, z int) (int32, bool) {
	if s == nil {
		return 0, false
	}
	normDim := world.NormalizeDimension(dim)
	key := dimBlockKey{dim: normDim, pos: world.BlockPos{X: x, Y: y, Z: z}}
	sm := s.villagerSocial()
	sm.mu.Lock()
	ownerID, ok := sm.entityByJobSite[key]
	if ok && s.entities != nil {
		if ent := s.entities.Get(ownerID); ent == nil || ent.TypeName != MobTypeVillager {
			delete(sm.entityByJobSite, key)
			delete(sm.jobSiteByEntity, ownerID)
			ok = false
			ownerID = 0
		}
	}
	sm.mu.Unlock()
	return ownerID, ok
}

// IsVillagerJobSiteLost reports whether an experienced villager (XP > 0) lost
// its previously claimed workstation and has not yet claimed a replacement.
func (s *Server) IsVillagerJobSiteLost(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	sm := s.villagerSocial()
	sm.mu.Lock()
	lost := sm.jobSiteLost[entityID]
	sm.mu.Unlock()
	return lost
}

// ClaimVillagerWorkstation binds the villager with entityID to the workstation
// block at (dim, x, y, z).
//   - Rejects baby villagers, nitwits, invalid workstation blocks, and blocks
//     already claimed by another villager.
//   - If the villager is unemployed ("minecraft:none"), assigns the workstation's
//     profession at Level 1 and rolls Novice trade offers.
//   - If the villager already has a profession (e.g. locked by XP > 0), only
//     claims the workstation if its profession matches the villager's profession.
func (s *Server) ClaimVillagerWorkstation(entityID int32, dim string, x, y, z int) (profession string, acquired bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return "", false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != MobTypeVillager || s.IsMobBaby(entityID) {
		return "", false
	}
	normDim := world.NormalizeDimension(dim)
	if normDim == "" {
		normDim = ent.EffectiveDimension()
	}
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return "", false
	}
	wsProf, ok := ProfessionForWorkstationState(cache.GetBlock(x, y, z))
	if !ok {
		return "", false
	}

	vs := s.ensureVillagerState(entityID)
	if vs == nil {
		return "", false
	}

	vtr := s.villagerTracker()
	vtr.mu.Lock()
	curProf := canonicalResourceName(vs.Profession)
	vtr.mu.Unlock()

	if curProf == VillagerProfessionNitwit {
		return "", false
	}
	if curProf != VillagerProfessionNone && curProf != wsProf {
		return "", false
	}

	key := dimBlockKey{dim: normDim, pos: world.BlockPos{X: x, Y: y, Z: z}}
	sm := s.villagerSocial()
	sm.mu.Lock()
	if existingOwner, taken := sm.entityByJobSite[key]; taken && existingOwner != entityID {
		if otherEnt := s.entities.Get(existingOwner); otherEnt != nil && otherEnt.TypeName == MobTypeVillager {
			sm.mu.Unlock()
			return "", false
		}
		delete(sm.jobSiteByEntity, existingOwner)
	}
	// Release any previous job site held by this villager.
	if prevSite, hadPrev := sm.jobSiteByEntity[entityID]; hadPrev {
		delete(sm.entityByJobSite, dimBlockKey{dim: prevSite.Dimension, pos: prevSite.Pos})
	}
	site := VillagerPOISite{
		Dimension:  normDim,
		Pos:        world.BlockPos{X: x, Y: y, Z: z},
		Profession: wsProf,
	}
	sm.jobSiteByEntity[entityID] = site
	sm.entityByJobSite[key] = entityID
	delete(sm.jobSiteLost, entityID)
	sm.mu.Unlock()

	if curProf == VillagerProfessionNone {
		s.SetVillagerProfession(entityID, wsProf, MinVillagerLevel)
	}
	return wsProf, true
}

// TryAcquireVillagerWorkstation searches within VillagerWorkstationScanRadius (16)
// horizontally and VillagerWorkstationScanVertical (4) vertically around the
// villager with entityID for the closest unclaimed workstation block and claims it.
func (s *Server) TryAcquireVillagerWorkstation(entityID int32) (VillagerPOISite, bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return VillagerPOISite{}, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != MobTypeVillager || s.IsMobBaby(entityID) {
		return VillagerPOISite{}, false
	}
	if site, ok := s.VillagerJobSite(entityID); ok {
		// Validate that the existing job site block is still intact.
		if s.ValidateVillagerWorkstation(entityID) {
			return site, true
		}
	}

	vs := s.ensureVillagerState(entityID)
	if vs == nil {
		return VillagerPOISite{}, false
	}
	vtr := s.villagerTracker()
	vtr.mu.Lock()
	curProf := canonicalResourceName(vs.Profession)
	vtr.mu.Unlock()
	if curProf == VillagerProfessionNitwit {
		return VillagerPOISite{}, false
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

	for dy := -VillagerWorkstationScanVertical; dy <= VillagerWorkstationScanVertical; dy++ {
		by := by0 + dy
		if by < world.MinY || by >= world.MinY+world.WorldHeight {
			continue
		}
		for dx := -VillagerWorkstationScanRadius; dx <= VillagerWorkstationScanRadius; dx++ {
			for dz := -VillagerWorkstationScanRadius; dz <= VillagerWorkstationScanRadius; dz++ {
				distSq := float64(dx*dx + dy*dy + dz*dz)
				if distSq >= bestDistSq {
					continue
				}
				bx := bx0 + dx
				bz := bz0 + dz
				wsProf, ok := ProfessionForWorkstationState(cache.GetBlock(bx, by, bz))
				if !ok {
					continue
				}
				if curProf != VillagerProfessionNone && curProf != wsProf {
					continue
				}
				if owner, claimed := s.WorkstationOwnerAt(dim, bx, by, bz); claimed && owner != entityID {
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
	if _, ok := s.ClaimVillagerWorkstation(entityID, dim, bestPos.X, bestPos.Y, bestPos.Z); !ok {
		return VillagerPOISite{}, false
	}
	return s.VillagerJobSite(entityID)
}

// ValidateVillagerWorkstation checks whether the villager's claimed workstation
// block still exists and matches its profession. If the workstation was removed
// or replaced, releases the POI claim and reverts the villager to unemployed
// ("minecraft:none") if XP == 0 and Level == 1, or keeps its locked profession
// if XP > 0 or Level > 1.
func (s *Server) ValidateVillagerWorkstation(entityID int32) bool {
	site, ok := s.VillagerJobSite(entityID)
	if !ok {
		return false
	}
	cache := s.DimensionChunks(site.Dimension)
	if cache == nil {
		return true
	}
	wsProf, valid := ProfessionForWorkstationState(cache.GetBlock(site.Pos.X, site.Pos.Y, site.Pos.Z))
	if valid && wsProf == site.Profession {
		return true
	}
	s.ReleaseVillagerWorkstation(entityID)
	return false
}

// ReleaseVillagerWorkstation releases the workstation POI held by entityID.
// Returns lostProfession=true if the villager had 0 XP (Level 1) and reverted
// to "minecraft:none", or false if the villager's profession was locked by XP > 0.
func (s *Server) ReleaseVillagerWorkstation(entityID int32) (lostProfession bool) {
	if s == nil || entityID <= 0 {
		return false
	}
	sm := s.villagerSocial()
	sm.mu.Lock()
	site, hadSite := sm.jobSiteByEntity[entityID]
	if hadSite {
		delete(sm.jobSiteByEntity, entityID)
		delete(sm.entityByJobSite, dimBlockKey{dim: site.Dimension, pos: site.Pos})
	}
	sm.mu.Unlock()

	vs := s.ensureVillagerState(entityID)
	if vs == nil {
		return false
	}

	vtr := s.villagerTracker()
	vtr.mu.Lock()
	xp := vs.XP
	lvl := vs.Level
	prof := canonicalResourceName(vs.Profession)
	vType := vs.VillagerType
	if xp <= 0 && lvl <= MinVillagerLevel && prof != VillagerProfessionNone && prof != VillagerProfessionNitwit {
		vs.Profession = VillagerProfessionNone
		vs.Level = MinVillagerLevel
		vs.Offers = nil
		lostProfession = true
	}
	vtr.mu.Unlock()

	if lostProfession {
		sm.mu.Lock()
		delete(sm.jobSiteLost, entityID)
		sm.mu.Unlock()
		if ent := s.entities.Get(entityID); ent != nil {
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
				EncodeVillagerEntityDataPacket(entityID, VillagerTypeID(vType), VillagerProfessionID(VillagerProfessionNone), MinVillagerLevel))
		}
		s.closeMerchantScreensForVillager(entityID)
	} else if hadSite && CanProfessionTrade(prof) {
		sm.mu.Lock()
		sm.jobSiteLost[entityID] = true
		sm.mu.Unlock()
	}
	return lostProfession
}

// OnWorkstationBlockBroken notifies the villager POI manager that a block at
// (dim, x, y, z) was broken or changed. If a villager owned that job site, its
// claim is released and its profession is updated according to vanilla rules.
func (s *Server) OnWorkstationBlockBroken(dim string, x, y, z int) (affectedVillagerID int32, lostProfession bool) {
	ownerID, claimed := s.WorkstationOwnerAt(dim, x, y, z)
	if !claimed || ownerID <= 0 {
		return 0, false
	}
	lost := s.ReleaseVillagerWorkstation(ownerID)
	return ownerID, lost
}

// --- Home Bed POI Claiming ---

// VillagerBedSite returns the bed POI claimed by the villager with entityID, if any.
func (s *Server) VillagerBedSite(entityID int32) (VillagerPOISite, bool) {
	if s == nil || entityID <= 0 {
		return VillagerPOISite{}, false
	}
	sm := s.villagerSocial()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	site, ok := sm.bedByEntity[entityID]
	return site, ok
}

// ClaimVillagerBed binds the villager with entityID to a bed block at (dim, x, y, z).
func (s *Server) ClaimVillagerBed(entityID int32, dim string, x, y, z int) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != MobTypeVillager {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	if normDim == "" {
		normDim = ent.EffectiveDimension()
	}
	cache := s.DimensionChunks(normDim)
	if cache == nil || !IsBedBlock(cache.GetBlock(x, y, z)) {
		return false
	}
	key := dimBlockKey{dim: normDim, pos: world.BlockPos{X: x, Y: y, Z: z}}
	sm := s.villagerSocial()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if existing, taken := sm.entityByBed[key]; taken && existing != entityID {
		if other := s.entities.Get(existing); other != nil && other.TypeName == MobTypeVillager {
			return false
		}
		delete(sm.bedByEntity, existing)
	}
	if prev, had := sm.bedByEntity[entityID]; had {
		delete(sm.entityByBed, dimBlockKey{dim: prev.Dimension, pos: prev.Pos})
	}
	site := VillagerPOISite{Dimension: normDim, Pos: world.BlockPos{X: x, Y: y, Z: z}}
	sm.bedByEntity[entityID] = site
	sm.entityByBed[key] = entityID
	return true
}

// --- Villager Gossip System & Reputation Pricing ---

// AddVillagerGossip adds amount to the villager's gossip entry of gossipType
// for targetUUID, clamping to [0, spec.Max]. Returns the resulting gossip value.
func (s *Server) AddVillagerGossip(entityID int32, targetUUID [16]byte, gossipType GossipType, amount int) int {
	if s == nil || entityID <= 0 || amount <= 0 {
		return 0
	}
	spec, ok := LookupGossipTypeSpec(gossipType)
	if !ok {
		return 0
	}
	sm := s.villagerSocial()
	sm.mu.Lock()
	byPlayer := sm.gossips[entityID]
	if byPlayer == nil {
		byPlayer = make(map[[16]byte]map[GossipType]int)
		sm.gossips[entityID] = byPlayer
	}
	byType := byPlayer[targetUUID]
	if byType == nil {
		byType = make(map[GossipType]int)
		byPlayer[targetUUID] = byType
	}
	next := byType[gossipType] + amount
	if next > spec.Max {
		next = spec.Max
	}
	if next <= 0 {
		delete(byType, gossipType)
		next = 0
	} else {
		byType[gossipType] = next
	}
	sm.mu.Unlock()
	return next
}

// VillagerGossipValue returns the current raw gossip value (0..Max) stored on
// villager entityID for (targetUUID, gossipType).
func (s *Server) VillagerGossipValue(entityID int32, targetUUID [16]byte, gossipType GossipType) int {
	if s == nil || entityID <= 0 {
		return 0
	}
	sm := s.villagerSocial()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if byPlayer := sm.gossips[entityID]; byPlayer != nil {
		if byType := byPlayer[targetUUID]; byType != nil {
			return byType[gossipType]
		}
	}
	return 0
}

// VillagerGossipsForPlayer returns a copy of all active gossip values stored on
// villager entityID for targetUUID.
func (s *Server) VillagerGossipsForPlayer(entityID int32, targetUUID [16]byte) map[GossipType]int {
	out := make(map[GossipType]int)
	if s == nil || entityID <= 0 {
		return out
	}
	sm := s.villagerSocial()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if byPlayer := sm.gossips[entityID]; byPlayer != nil {
		for k, v := range byPlayer[targetUUID] {
			if v > 0 {
				out[k] = v
			}
		}
	}
	return out
}

// VillagerPlayerReputation calculates the total weighted reputation score for
// targetUUID on villager entityID:
// sum(value * spec.Weight) across all 5 gossip types.
func (s *Server) VillagerPlayerReputation(entityID int32, targetUUID [16]byte) int {
	if s == nil || entityID <= 0 {
		return 0
	}
	sm := s.villagerSocial()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	byPlayer := sm.gossips[entityID]
	if byPlayer == nil {
		return 0
	}
	byType := byPlayer[targetUUID]
	if len(byType) == 0 {
		return 0
	}
	total := 0
	for gt, val := range byType {
		if val <= 0 {
			continue
		}
		if spec, ok := vanillaGossipTypeSpecs[gt]; ok {
			total += val * spec.Weight
		}
	}
	return total
}

// DecayVillagerGossips performs one daily decay pass on all gossip entries of
// villager entityID, subtracting each GossipType's DecayPerDay and removing
// entries that drop to <= 0. Note that GossipMajorPositive has DecayPerDay == 0
// and therefore never decays.
func (s *Server) DecayVillagerGossips(entityID int32) {
	if s == nil || entityID <= 0 {
		return
	}
	sm := s.villagerSocial()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	decayGossipMapLocked(sm.gossips[entityID])
}

// DecayAllVillagerGossips performs one daily decay pass across all tracked villagers.
func (s *Server) DecayAllVillagerGossips() {
	if s == nil {
		return
	}
	sm := s.villagerSocial()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for _, byPlayer := range sm.gossips {
		decayGossipMapLocked(byPlayer)
	}
}

func decayGossipMapLocked(byPlayer map[[16]byte]map[GossipType]int) {
	if len(byPlayer) == 0 {
		return
	}
	for uuid, byType := range byPlayer {
		for gt, val := range byType {
			spec, ok := vanillaGossipTypeSpecs[gt]
			if !ok {
				delete(byType, gt)
				continue
			}
			next := val - spec.DecayPerDay
			if next <= 0 {
				delete(byType, gt)
			} else {
				byType[gt] = next
			}
		}
		if len(byType) == 0 {
			delete(byPlayer, uuid)
		}
	}
}

// ShareVillagerGossip transfers gossip entries from sourceEntityID to
// targetEntityID, subtracting each GossipType's DecayPerTransfer and keeping
// max(existing, transferred) clamped to spec.Max (matching vanilla
// GossipContainer.transferFrom).
func (s *Server) ShareVillagerGossip(sourceEntityID, targetEntityID int32) {
	if s == nil || sourceEntityID <= 0 || targetEntityID <= 0 || sourceEntityID == targetEntityID {
		return
	}
	sm := s.villagerSocial()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	srcByPlayer := sm.gossips[sourceEntityID]
	if len(srcByPlayer) == 0 {
		return
	}
	dstByPlayer := sm.gossips[targetEntityID]
	if dstByPlayer == nil {
		dstByPlayer = make(map[[16]byte]map[GossipType]int)
		sm.gossips[targetEntityID] = dstByPlayer
	}

	for uuid, srcTypes := range srcByPlayer {
		for gt, val := range srcTypes {
			spec, ok := vanillaGossipTypeSpecs[gt]
			if !ok {
				continue
			}
			transferred := val - spec.DecayPerTransfer
			if transferred <= 0 {
				continue
			}
			if transferred > spec.Max {
				transferred = spec.Max
			}
			dstTypes := dstByPlayer[uuid]
			if dstTypes == nil {
				dstTypes = make(map[GossipType]int)
				dstByPlayer[uuid] = dstTypes
			}
			if transferred > dstTypes[gt] {
				dstTypes[gt] = transferred
			}
		}
	}
}

// ReputationPriceDiff computes the SpecialPriceDiff contribution from a player's
// reputation with a villager on an offer with priceMultiplier, matching vanilla
// Villager.updateSpecialPrices:
// -floor(reputation * priceMultiplier).
// Positive reputation yields a negative SpecialPriceDiff (discount); negative
// reputation yields a positive SpecialPriceDiff (surcharge).
func ReputationPriceDiff(reputation int, priceMultiplier float32) int32 {
	if reputation == 0 || priceMultiplier <= 0 {
		return 0
	}
	return -int32(math.Floor(float64(reputation) * float64(priceMultiplier)))
}

// EffectiveOffersForPlayer returns a copy of offers with SpecialPriceDiff
// updated for session's villager reputation with villagerEntityID and session's
// active Hero of the Village status effect.
func (s *Server) EffectiveOffersForPlayer(session *PlayerSession, villagerEntityID int32, offers []MerchantOffer) []MerchantOffer {
	if len(offers) == 0 {
		return nil
	}
	out := make([]MerchantOffer, len(offers))
	copy(out, offers)
	if s == nil || session == nil {
		return out
	}

	rep := s.VillagerPlayerReputation(villagerEntityID, session.Profile.UUID)
	heroInst, hasHero := session.PlayerEffect(EffectHeroOfTheVillage)
	if rep == 0 && !hasHero {
		return out
	}

	for i := range out {
		var diff int32
		if rep != 0 {
			diff += ReputationPriceDiff(rep, out[i].PriceMultiplier)
		}
		if hasHero {
			diff += HeroOfTheVillagePriceDiff(out[i].BaseCostA.Count, heroInst.Amplifier)
		}
		out[i].SpecialPriceDiff = diff
	}
	return out
}

// UpdateVillagerSpecialPricesForPlayer updates SpecialPriceDiff on villagerEntityID's
// offers for session (combining villager gossip reputation and Hero of the Village)
// so that payment matching and trade execution use the player's reputation-adjusted prices.
func (s *Server) UpdateVillagerSpecialPricesForPlayer(session *PlayerSession, villagerEntityID int32) {
	if s == nil || session == nil || villagerEntityID <= 0 {
		return
	}
	rep := s.VillagerPlayerReputation(villagerEntityID, session.Profile.UUID)
	heroInst, hasHero := session.PlayerEffect(EffectHeroOfTheVillage)
	if rep == 0 && !hasHero {
		return
	}
	vtr := s.villagerTracker()
	vtr.mu.Lock()
	vs, ok := vtr.villagers[villagerEntityID]
	if ok && vs != nil {
		for i := range vs.Offers {
			var diff int32
			if rep != 0 {
				diff += ReputationPriceDiff(rep, vs.Offers[i].PriceMultiplier)
			}
			if hasHero {
				diff += HeroOfTheVillagePriceDiff(vs.Offers[i].BaseCostA.Count, heroInst.Amplifier)
			}
			vs.Offers[i].SpecialPriceDiff = diff
		}
	}
	vtr.mu.Unlock()
}

// --- Reputation Event Hooks (Trade, Hurt, Killed, Zombie Villager Cured) ---

// OnVillagerTradedWithPlayer records a trade reputation event (+2 GossipTrading)
// on villager entityID for player.
func (s *Server) OnVillagerTradedWithPlayer(entityID int32, player *PlayerSession) {
	if s == nil || player == nil || entityID <= 0 {
		return
	}
	s.AddVillagerGossip(entityID, player.Profile.UUID, GossipTrading, 2)
	s.UpdateVillagerSpecialPricesForPlayer(player, entityID)
}

// OnVillagerHurtByPlayer records a minor_negative gossip event (+25 on the hurt
// villager and +15 on nearby witnessing villagers within 16 blocks) when a
// player damages a villager, and broadcasts angry villager particles.
func (s *Server) OnVillagerHurtByPlayer(entityID int32, attacker *PlayerSession) {
	if s == nil || s.entities == nil || attacker == nil || entityID <= 0 {
		return
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != MobTypeVillager {
		return
	}
	uuid := attacker.Profile.UUID
	s.AddVillagerGossip(entityID, uuid, GossipMinorNegative, 25)

	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(entityID, EntityEventVillagerAngry))

	radSq := VillagerWitnessRadius * VillagerWitnessRadius
	for _, other := range s.entities.AppendAll(nil) {
		if other.ID == entityID || other.TypeName != MobTypeVillager || other.EffectiveDimension() != dim {
			continue
		}
		dx := other.X - ent.X
		dy := other.Y - ent.Y
		dz := other.Z - ent.Z
		if dx*dx+dy*dy+dz*dz <= radSq {
			s.AddVillagerGossip(other.ID, uuid, GossipMinorNegative, 15)
		}
	}
}

// OnVillagerKilledByPlayer records a major_negative gossip event (+25 on all
// witnessing villagers within 16 blocks) when a player kills a villager.
func (s *Server) OnVillagerKilledByPlayer(entityID int32, dim string, x, y, z float64, killer *PlayerSession) {
	if s == nil || s.entities == nil || killer == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	uuid := killer.Profile.UUID
	radSq := VillagerWitnessRadius * VillagerWitnessRadius
	for _, other := range s.entities.AppendAll(nil) {
		if other.ID == entityID || other.TypeName != MobTypeVillager || other.EffectiveDimension() != normDim {
			continue
		}
		dx := other.X - x
		dy := other.Y - y
		dz := other.Z - z
		if dx*dx+dy*dy+dz*dz <= radSq {
			s.AddVillagerGossip(other.ID, uuid, GossipMajorNegative, 25)
		}
	}
}

// OnZombieVillagerCuredByPlayer applies the vanilla 26.1.2 curing gossip
// discounts for curerUUID on curedVillagerID (+20 major_positive permanent and
// +25 minor_positive decaying) as well as +20 minor_positive on nearby
// witnessing villagers within 16 blocks.
func (s *Server) OnZombieVillagerCuredByPlayer(curedVillagerID int32, curerUUID [16]byte) {
	if s == nil || curedVillagerID <= 0 {
		return
	}
	s.AddVillagerGossip(curedVillagerID, curerUUID, GossipMajorPositive, 20)
	s.AddVillagerGossip(curedVillagerID, curerUUID, GossipMinorPositive, 25)

	if s.entities == nil {
		return
	}
	ent := s.entities.Get(curedVillagerID)
	if ent == nil {
		return
	}
	dim := ent.EffectiveDimension()
	radSq := VillagerWitnessRadius * VillagerWitnessRadius
	for _, other := range s.entities.AppendAll(nil) {
		if other.ID == curedVillagerID || other.TypeName != MobTypeVillager || other.EffectiveDimension() != dim {
			continue
		}
		dx := other.X - ent.X
		dy := other.Y - ent.Y
		dz := other.Z - ent.Z
		if dx*dx+dy*dy+dz*dz <= radSq {
			s.AddVillagerGossip(other.ID, curerUUID, GossipMinorPositive, 20)
		}
	}
}

// --- Zombie Villager Spawning, Infection & Curing ---

// ResolveZombieVillagerEntityTypeID returns the protocol 775 entity_type ID for
// "minecraft:zombie_villager" (152).
func ResolveZombieVillagerEntityTypeID() int {
	if idx := registry.EntityTypeIndex(MobTypeZombieVillager); idx >= 0 {
		return idx
	}
	return EntityTypeZombieVillagerID
}

// SpawnZombieVillager spawns a `minecraft:zombie_villager` at (x, y, z) with the
// specified villager profession and level (1..5).
func (s *Server) SpawnZombieVillager(x, y, z float64, profession string, level int) (int32, error) {
	return s.SpawnZombieVillagerInDimension(world.DimensionOverworld, x, y, z, profession, level)
}

// SpawnZombieVillagerInDimension spawns a `minecraft:zombie_villager` in dim.
func (s *Server) SpawnZombieVillagerInDimension(dim string, x, y, z float64, profession string, level int) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	if level < MinVillagerLevel {
		level = MinVillagerLevel
	}
	if level > MaxVillagerLevel {
		level = MaxVillagerLevel
	}
	normDim := world.NormalizeDimension(dim)
	profNorm := canonicalResourceName(profession)
	if VillagerProfessionID(profNorm) == 0 && profNorm != VillagerProfessionNone {
		profNorm = VillagerProfessionFarmer
	}

	typeID := ResolveZombieVillagerEntityTypeID()
	entityID := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeZombieVillager,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})
	s.SetMobHealth(entityID, DefaultZombieVillagerMaxHealth)

	sm := s.villagerSocial()
	sm.mu.Lock()
	sm.zombieVillagers[entityID] = &ZombieVillagerState{
		EntityID:     entityID,
		Dimension:    normDim,
		VillagerType: VillagerTypePlains,
		Profession:   profNorm,
		Level:        level,
	}
	sm.mu.Unlock()

	if ent := s.entities.Get(entityID); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
	}
	return entityID, nil
}

// ZombifyVillager converts a living villager entity into a `minecraft:zombie_villager`,
// preserving its VillagerType, Profession, Level, XP, Offers, and Gossips.
func (s *Server) ZombifyVillager(villagerEntityID int32) (int32, bool) {
	if s == nil || s.entities == nil || villagerEntityID <= 0 {
		return 0, false
	}
	ent := s.entities.Get(villagerEntityID)
	if ent == nil || ent.TypeName != MobTypeVillager {
		return 0, false
	}
	vs, _ := s.VillagerState(villagerEntityID)
	dim := ent.EffectiveDimension()
	x, y, z := ent.X, ent.Y, ent.Z

	// Copy existing gossips before removing villager.
	sm := s.villagerSocial()
	sm.mu.Lock()
	var savedGossips map[[16]byte]map[GossipType]int
	if src := sm.gossips[villagerEntityID]; len(src) > 0 {
		savedGossips = make(map[[16]byte]map[GossipType]int, len(src))
		for u, tm := range src {
			cpMap := make(map[GossipType]int, len(tm))
			for k, v := range tm {
				cpMap[k] = v
			}
			savedGossips[u] = cpMap
		}
	}
	delete(sm.gossips, villagerEntityID)
	sm.mu.Unlock()

	s.ReleaseVillagerWorkstation(villagerEntityID)
	s.closeMerchantScreensForVillager(villagerEntityID)

	cp := ent.ChunkPos()
	s.entities.Remove(villagerEntityID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{villagerEntityID}))

	zvID, err := s.SpawnZombieVillagerInDimension(dim, x, y, z, vs.Profession, vs.Level)
	if err != nil {
		return 0, false
	}
	sm.mu.Lock()
	if zv := sm.zombieVillagers[zvID]; zv != nil {
		zv.VillagerType = vs.VillagerType
		zv.Profession = vs.Profession
		zv.Level = vs.Level
		zv.XP = vs.XP
		if len(vs.Offers) > 0 {
			zv.Offers = make([]MerchantOffer, len(vs.Offers))
			copy(zv.Offers, vs.Offers)
		}
		zv.Gossips = savedGossips
	}
	sm.mu.Unlock()

	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.zombie.infect", SoundSourceHostile, x, y, z, 1.0, 1.0, 0))
	return zvID, true
}

// ZombieVillagerState returns a snapshot of the ZombieVillagerState for entityID.
func (s *Server) ZombieVillagerState(entityID int32) (ZombieVillagerState, bool) {
	if s == nil || entityID <= 0 {
		return ZombieVillagerState{}, false
	}
	sm := s.villagerSocial()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	zv, ok := sm.zombieVillagers[entityID]
	if !ok || zv == nil {
		return ZombieVillagerState{}, false
	}
	cp := *zv
	if len(zv.Offers) > 0 {
		cp.Offers = make([]MerchantOffer, len(zv.Offers))
		copy(cp.Offers, zv.Offers)
	}
	return cp, true
}

// SetZombieVillagerWeakened marks a zombie villager as having the Weakness
// status effect so it can be cured with a golden apple.
func (s *Server) SetZombieVillagerWeakened(entityID int32, weakened bool) {
	if s == nil || entityID <= 0 {
		return
	}
	if weakened {
		s.ApplyEntityEffectScaled(entityID, NewStatusEffectInstance(EffectWeakness, 1800, 0), 1.0)
	}
	sm := s.villagerSocial()
	sm.mu.Lock()
	if zv, ok := sm.zombieVillagers[entityID]; ok && zv != nil {
		zv.Weakened = weakened
	}
	sm.mu.Unlock()
}

// IsZombieVillagerWeakened reports whether the zombie villager with entityID
// currently has Weakness active.
func (s *Server) IsZombieVillagerWeakened(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	for _, eff := range s.EntityEffects(entityID) {
		if eff.ID == EffectWeakness && (eff.IsInfinite() || eff.Duration > 0) {
			return true
		}
	}
	sm := s.villagerSocial()
	sm.mu.Lock()
	zv := sm.zombieVillagers[entityID]
	weak := zv != nil && zv.Weakened
	sm.mu.Unlock()
	return weak
}

// InteractWithZombieVillager handles a player right-clicking a
// `minecraft:zombie_villager` while holding `minecraft:golden_apple`:
// if the zombie villager has Weakness, consumes 1 golden apple in survival and
// starts the 3600..6000 tick curing conversion.
func (s *Server) InteractWithZombieVillager(session *PlayerSession, entityID int32, rng *rand.Rand) bool {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeZombieVillager {
		return false
	}
	dx := ent.X - snap.X
	dy := ent.Y - snap.Y
	dz := ent.Z - snap.Z
	if snap.GameMode != GameModeCreative && (dx*dx+dy*dy+dz*dz) > MaxMeleeAttackReach*MaxMeleeAttackReach {
		return false
	}
	held := session.HeldItem()
	if held.IsEmpty() || canonicalResourceName(held.ResolvedID()) != "minecraft:golden_apple" {
		return false
	}
	if !s.IsZombieVillagerWeakened(entityID) {
		return false
	}

	sm := s.villagerSocial()
	sm.mu.Lock()
	if rng == nil {
		rng = sm.rng
	}
	zv, ok := sm.zombieVillagers[entityID]
	if !ok || zv == nil {
		zv = &ZombieVillagerState{
			EntityID:     entityID,
			Dimension:    ent.EffectiveDimension(),
			VillagerType: VillagerTypePlains,
			Profession:   VillagerProfessionFarmer,
			Level:        MinVillagerLevel,
		}
		sm.zombieVillagers[entityID] = zv
	}
	if zv.Converting {
		sm.mu.Unlock()
		return false
	}
	cureTicks := ZombieVillagerMinCureTicks + rng.Intn(ZombieVillagerMaxCureTicks-ZombieVillagerMinCureTicks+1)
	zv.Converting = true
	zv.ConversionTicks = cureTicks
	zv.CurerUUID = session.Profile.UUID
	zv.HasCurer = true
	sm.mu.Unlock()

	consumeOneHeldItemInSurvival(session)
	// Vanilla grants Strength I during conversion (3600 ticks on Normal).
	s.ApplyEntityEffectScaled(entityID, NewStatusEffectInstance(EffectStrength, 3600, 0), 1.0)

	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(entityID, EntityEventZombieVillagerCure))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.zombie_villager.cure", SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.5, 0.7, 0))
	return true
}

// SetZombieVillagerConversionTicks sets the remaining conversion ticks on a
// curing zombie villager (useful for deterministic tests).
func (s *Server) SetZombieVillagerConversionTicks(entityID int32, ticks int) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	sm := s.villagerSocial()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	zv, ok := sm.zombieVillagers[entityID]
	if !ok || zv == nil {
		return false
	}
	zv.Converting = true
	zv.ConversionTicks = ticks
	return true
}

// CompleteZombieVillagerCure finishes the conversion of a curing zombie villager,
// replacing it with a `minecraft:villager`, applying the permanent +20
// major_positive and decaying +25 minor_positive gossip discounts for the curer,
// and granting the "minecraft:story/cure_zombie_villager" advancement.
func (s *Server) CompleteZombieVillagerCure(entityID int32) (villagerID int32, ok bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeZombieVillager {
		return 0, false
	}

	sm := s.villagerSocial()
	sm.mu.Lock()
	zv := sm.zombieVillagers[entityID]
	delete(sm.zombieVillagers, entityID)
	sm.mu.Unlock()

	dim := ent.EffectiveDimension()
	x, y, z := ent.X, ent.Y, ent.Z
	cp := ent.ChunkPos()
	s.entities.Remove(entityID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{entityID}))

	prof := VillagerProfessionFarmer
	lvl := MinVillagerLevel
	vType := VillagerTypePlains
	if zv != nil {
		if zv.Profession != "" {
			prof = zv.Profession
		}
		if zv.Level >= MinVillagerLevel {
			lvl = zv.Level
		}
		if zv.VillagerType != "" {
			vType = zv.VillagerType
		}
	}

	vid, err := s.SpawnVillager(x, y, z, prof, lvl)
	if err != nil {
		return 0, false
	}
	s.entities.Update(vid, func(e *world.Entity) {
		e.Dimension = dim
	})

	if zv != nil {
		if zv.XP > 0 || len(zv.Offers) > 0 || vType != VillagerTypePlains {
			vs, exists := s.VillagerState(vid)
			if exists {
				vs.VillagerType = vType
				if zv.XP > 0 {
					vs.XP = zv.XP
				}
				if len(zv.Offers) > 0 {
					vs.Offers = zv.Offers
				}
				s.SetVillagerState(vid, vs)
			}
		}
		if len(zv.Gossips) > 0 {
			sm.mu.Lock()
			sm.gossips[vid] = zv.Gossips
			sm.mu.Unlock()
		}
		if zv.HasCurer {
			s.OnZombieVillagerCuredByPlayer(vid, zv.CurerUUID)
			s.TriggerCuredZombieVillagerByUUID(zv.CurerUUID)
			if curer := s.findPlayerByUUID(zv.CurerUUID); curer != nil {
				s.UpdateVillagerSpecialPricesForPlayer(curer, vid)
			}
		}
	}

	s.ApplyEntityEffectScaled(vid, NewStatusEffectInstance(EffectNausea, ZombieVillagerNauseaTicks, 0), 1.0)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.zombie_villager.converted", SoundSourceNeutral, x, y, z, 2.0, 1.0, 0))
	return vid, true
}

func (s *Server) findPlayerByUUID(uuid [16]byte) *PlayerSession {
	if s == nil {
		return nil
	}
	s.playersMu.RLock()
	defer s.playersMu.RUnlock()
	return s.players[uuid]
}

// TickZombieVillagers advances all converting zombie villagers by one server
// tick, accelerating conversion when iron bars or beds are nearby (matching
// vanilla ZombieVillager.getConversionProgress).
func (s *Server) TickZombieVillagers(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	sm := s.villagerSocial()
	sm.mu.Lock()
	if len(sm.zombieVillagers) == 0 {
		sm.mu.Unlock()
		return
	}
	if rng == nil {
		rng = sm.rng
	}
	var toComplete []int32
	for entityID, zv := range sm.zombieVillagers {
		ent := s.entities.Get(entityID)
		if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeZombieVillager {
			delete(sm.zombieVillagers, entityID)
			continue
		}
		if !zv.Converting {
			continue
		}
		step := 1
		if rng != nil && rng.Float32() < 0.01 {
			step += s.countZombieCureCatalystsAt(ent.EffectiveDimension(), int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z)), rng)
		}
		zv.ConversionTicks -= step
		if zv.ConversionTicks <= 0 {
			toComplete = append(toComplete, entityID)
		}
	}
	sm.mu.Unlock()

	for _, id := range toComplete {
		s.CompleteZombieVillagerCure(id)
	}
}

func (s *Server) countZombieCureCatalystsAt(dim string, cx, cy, cz int, rng *rand.Rand) int {
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return 0
	}
	extra := 0
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
					if rng != nil && rng.Float32() < 0.3 {
						extra++
					}
				}
			}
		}
	}
	return extra
}
