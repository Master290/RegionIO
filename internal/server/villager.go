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

// Villager entity constants and protocol 775 (26.1.2) metadata IDs.
const (
	MobTypeVillager = "minecraft:villager"

	// EntityTypeVillagerID is BuiltInRegistries.ENTITY_TYPE ID for "minecraft:villager"
	// in Minecraft 26.1.2 (protocol 775), used as fallback if ids.go does not list it.
	EntityTypeVillagerID int32 = 139

	// VillagerWanderSpeed is the passive wandering speed for villagers (blocks/tick).
	VillagerWanderSpeed = 0.08

	// DefaultVillagerMaxHealth is the vanilla max health of a villager (20 HP = 10 hearts).
	DefaultVillagerMaxHealth float32 = 20.0

	// Villager badge levels: 1 (Novice), 2 (Apprentice), 3 (Journeyman), 4 (Expert), 5 (Master).
	MinVillagerLevel = 1
	MaxVillagerLevel = 5

	// PlaySetEntityDataCB is ClientboundSetEntityDataPacket (0x63 in protocol 775).
	PlaySetEntityDataCB int32 = 0x63

	// EntityDataVillagerDataID is the SynchedEntityData accessor ID for
	// Villager.DATA_VILLAGER_DATA in Minecraft 26.1.2 (19).
	EntityDataVillagerDataID byte = 19

	// EntityDataSerializerVillagerData is EntityDataSerializers.VILLAGER_DATA ID
	// in Minecraft 26.1.2 (18).
	EntityDataSerializerVillagerData int32 = 18

	// EntityEventVillagerHappy is ClientboundEntityEventPacket (0x22) event ID
	// for happy villager particles (green stars on level-up).
	EntityEventVillagerHappy byte = 14

	// Vanilla villager work schedule and daily restock parameters.
	VillagerWorkStartTicks    int64 = 2000
	VillagerWorkEndTicks      int64 = 9000
	MaxVillagerRestocksPerDay int   = 2
	MinRestockIntervalTicks   int64 = 2400
)

// Vanilla 26.1.2 Villager Profession identifiers (BuiltInRegistries.VILLAGER_PROFESSION 0..14).
const (
	VillagerProfessionNone          = "minecraft:none"
	VillagerProfessionArmorer       = "minecraft:armorer"
	VillagerProfessionButcher       = "minecraft:butcher"
	VillagerProfessionCartographer  = "minecraft:cartographer"
	VillagerProfessionCleric        = "minecraft:cleric"
	VillagerProfessionFarmer        = "minecraft:farmer"
	VillagerProfessionFisherman     = "minecraft:fisherman"
	VillagerProfessionFletcher      = "minecraft:fletcher"
	VillagerProfessionLeatherworker = "minecraft:leatherworker"
	VillagerProfessionLibrarian     = "minecraft:librarian"
	VillagerProfessionMason         = "minecraft:mason"
	VillagerProfessionNitwit        = "minecraft:nitwit"
	VillagerProfessionShepherd      = "minecraft:shepherd"
	VillagerProfessionToolsmith     = "minecraft:toolsmith"
	VillagerProfessionWeaponsmith   = "minecraft:weaponsmith"
)

// Vanilla 26.1.2 Villager Biome Type identifiers (BuiltInRegistries.VILLAGER_TYPE 0..6).
const (
	VillagerTypeDesert  = "minecraft:desert"
	VillagerTypeJungle  = "minecraft:jungle"
	VillagerTypePlains  = "minecraft:plains"
	VillagerTypeSavanna = "minecraft:savanna"
	VillagerTypeSnow    = "minecraft:snow"
	VillagerTypeSwamp   = "minecraft:swamp"
	VillagerTypeTaiga   = "minecraft:taiga"
)

var villagerProfessionsOrdered = [15]string{
	VillagerProfessionNone,          // 0
	VillagerProfessionArmorer,       // 1
	VillagerProfessionButcher,       // 2
	VillagerProfessionCartographer,  // 3
	VillagerProfessionCleric,        // 4
	VillagerProfessionFarmer,        // 5
	VillagerProfessionFisherman,     // 6
	VillagerProfessionFletcher,      // 7
	VillagerProfessionLeatherworker, // 8
	VillagerProfessionLibrarian,     // 9
	VillagerProfessionMason,         // 10
	VillagerProfessionNitwit,        // 11
	VillagerProfessionShepherd,      // 12
	VillagerProfessionToolsmith,     // 13
	VillagerProfessionWeaponsmith,   // 14
}

var tradingVillagerProfessions = [13]string{
	VillagerProfessionArmorer,
	VillagerProfessionButcher,
	VillagerProfessionCartographer,
	VillagerProfessionCleric,
	VillagerProfessionFarmer,
	VillagerProfessionFisherman,
	VillagerProfessionFletcher,
	VillagerProfessionLeatherworker,
	VillagerProfessionLibrarian,
	VillagerProfessionMason,
	VillagerProfessionShepherd,
	VillagerProfessionToolsmith,
	VillagerProfessionWeaponsmith,
}

var villagerTypesOrdered = [7]string{
	VillagerTypeDesert,  // 0
	VillagerTypeJungle,  // 1
	VillagerTypePlains,  // 2
	VillagerTypeSavanna, // 3
	VillagerTypeSnow,    // 4
	VillagerTypeSwamp,   // 5
	VillagerTypeTaiga,   // 6
}

// AllVillagerProfessions returns a copy of all 15 vanilla 26.1.2 villager
// profession identifiers in registry order (0..14).
func AllVillagerProfessions() []string {
	out := make([]string, len(villagerProfessionsOrdered))
	copy(out, villagerProfessionsOrdered[:])
	return out
}

// TradingVillagerProfessions returns a copy of the 13 villager professions that
// have datapack trade tables in vanilla 26.1.2.
func TradingVillagerProfessions() []string {
	out := make([]string, len(tradingVillagerProfessions))
	copy(out, tradingVillagerProfessions[:])
	return out
}

// VillagerProfessionID returns the BuiltInRegistries.VILLAGER_PROFESSION ID (0..14)
// for profession, defaulting to 0 (minecraft:none) if unknown.
func VillagerProfessionID(profession string) int32 {
	norm := canonicalResourceName(profession)
	for i, p := range villagerProfessionsOrdered {
		if p == norm {
			return int32(i)
		}
	}
	return 0
}

// VillagerProfessionByID returns the canonical profession identifier for a
// registry ID (0..14), or "minecraft:none" if out of range.
func VillagerProfessionByID(id int32) string {
	if id < 0 || int(id) >= len(villagerProfessionsOrdered) {
		return VillagerProfessionNone
	}
	return villagerProfessionsOrdered[id]
}

// CanProfessionTrade reports whether a villager with profession can open a
// merchant trading window (all valid professions except none and nitwit).
func CanProfessionTrade(profession string) bool {
	id := VillagerProfessionID(profession)
	return id > 0 && villagerProfessionsOrdered[id] != VillagerProfessionNitwit
}

// VillagerProfessionTitle returns the human-readable screen title for a
// villager's merchant UI (e.g. "Farmer", "Librarian").
func VillagerProfessionTitle(profession string) string {
	short := strings.TrimPrefix(canonicalResourceName(profession), "minecraft:")
	if short == "" || short == "none" {
		return "Villager"
	}
	return strings.ToUpper(short[:1]) + short[1:]
}

// VillagerTypeID returns the BuiltInRegistries.VILLAGER_TYPE ID (0..6) for
// villagerType, defaulting to 2 (minecraft:plains) if unknown.
func VillagerTypeID(villagerType string) int32 {
	norm := canonicalResourceName(villagerType)
	for i, t := range villagerTypesOrdered {
		if t == norm {
			return int32(i)
		}
	}
	return 2 // minecraft:plains
}

// VillagerTypeByID returns the canonical villager biome type identifier for a
// registry ID (0..6), or "minecraft:plains" if out of range.
func VillagerTypeByID(id int32) string {
	if id < 0 || int(id) >= len(villagerTypesOrdered) {
		return VillagerTypePlains
	}
	return villagerTypesOrdered[id]
}

// ResolveVillagerEntityTypeID returns the protocol 775 entity_type registry ID
// for "minecraft:villager" (139).
func ResolveVillagerEntityTypeID() int32 {
	if idx := registry.EntityTypeIndex(MobTypeVillager); idx >= 0 {
		return int32(idx)
	}
	return EntityTypeVillagerID
}

// EncodeVillagerEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// carrying the villager's VillagerData (biome type, profession, badge level).
func EncodeVillagerEntityDataPacket(entityID, villagerTypeID, professionID, level int32) []byte {
	if level < MinVillagerLevel {
		level = MinVillagerLevel
	}
	if level > MaxVillagerLevel {
		level = MaxVillagerLevel
	}
	w := protocol.NewWriter(16)
	w.VarInt(entityID)
	w.Byte(EntityDataVillagerDataID)
	w.VarInt(EntityDataSerializerVillagerData)
	w.VarInt(villagerTypeID)
	w.VarInt(professionID)
	w.VarInt(level)
	w.Byte(0xFF) // SynchedEntityData pack end marker
	return w.Bytes()
}

// DecodeVillagerEntityDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeVillagerEntityDataPacket.
func DecodeVillagerEntityDataPacket(r *protocol.Reader) (entityID, villagerTypeID, professionID, level int32, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	dataID, err := r.ReadByte()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	if dataID != EntityDataVillagerDataID {
		return 0, 0, 0, 0, errors.New("unexpected entity data accessor ID")
	}
	serID, err := r.VarInt()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	if serID != EntityDataSerializerVillagerData {
		return 0, 0, 0, 0, errors.New("unexpected entity data serializer ID")
	}
	villagerTypeID, err = r.VarInt()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	professionID, err = r.VarInt()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	level, err = r.VarInt()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	endByte, err := r.ReadByte()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	if endByte != 0xFF {
		return 0, 0, 0, 0, errors.New("missing 0xFF terminator in entity data packet")
	}
	return entityID, villagerTypeID, professionID, level, nil
}

// VillagerState holds the authoritative server-side profession, badge level,
// experience, trade offers, and daily restock state for a villager entity.
type VillagerState struct {
	EntityID            int32
	VillagerType        string
	Profession          string
	Level               int
	XP                  int32
	Offers              []MerchantOffer
	LastRestockGameTime int64
	LastRestockDay      int64
	RestocksToday       int
}

// Clone returns a deep copy of VillagerState (including its Offers slice).
func (v VillagerState) Clone() VillagerState {
	cp := v
	if len(v.Offers) > 0 {
		cp.Offers = make([]MerchantOffer, len(v.Offers))
		copy(cp.Offers, v.Offers)
	}
	return cp
}

type serverVillagerTracker struct {
	mu        sync.Mutex
	villagers map[int32]*VillagerState
	rng       *rand.Rand
}

var serverVillagers sync.Map // map[*Server]*serverVillagerTracker

func (s *Server) villagerTracker() *serverVillagerTracker {
	if v, ok := serverVillagers.Load(s); ok {
		return v.(*serverVillagerTracker)
	}
	actual, _ := serverVillagers.LoadOrStore(s, &serverVillagerTracker{
		villagers: make(map[int32]*VillagerState),
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
	})
	return actual.(*serverVillagerTracker)
}

func (s *Server) ensureVillagerState(entityID int32) *VillagerState {
	if s == nil || s.entities == nil {
		return nil
	}
	ent := s.entities.Get(entityID)
	if ent == nil || (ent.TypeName != MobTypeVillager && canonicalResourceName(ent.TypeName) != MobTypeWanderingTrader) {
		return nil
	}
	vtr := s.villagerTracker()
	vtr.mu.Lock()
	defer vtr.mu.Unlock()
	if vs, ok := vtr.villagers[entityID]; ok && vs != nil {
		return vs
	}
	if canonicalResourceName(ent.TypeName) == MobTypeWanderingTrader {
		offers := RollWanderingTraderOffers(vtr.rng)
		vs := &VillagerState{
			EntityID:       entityID,
			VillagerType:   VillagerTypePlains,
			Profession:     VillagerProfessionFarmer,
			Level:          MinVillagerLevel,
			XP:             0,
			Offers:         offers,
			LastRestockDay: -1,
		}
		vtr.villagers[entityID] = vs
		return vs
	}
	// Default a newly discovered villager entity to a Novice profession with
	// rolled offers from the 26.1.2 datapack.
	prof := tradingVillagerProfessions[int(entityID)%len(tradingVillagerProfessions)]
	offers := DefaultTradeBook().RollOffers(prof, MinVillagerLevel, vtr.rng)
	vs := &VillagerState{
		EntityID:       entityID,
		VillagerType:   VillagerTypePlains,
		Profession:     prof,
		Level:          MinVillagerLevel,
		XP:             0,
		Offers:         offers,
		LastRestockDay: -1,
	}
	vtr.villagers[entityID] = vs
	return vs
}

// SpawnVillager spawns a villager entity at (x, y, z) with the given profession
// and badge level (1..5), rolling trade offers from the 26.1.2 datapack for
// each level up to level, and broadcasts its spawn and VillagerData metadata.
func (s *Server) SpawnVillager(x, y, z float64, profession string, level int) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity list")
	}
	if level < MinVillagerLevel {
		level = MinVillagerLevel
	}
	if level > MaxVillagerLevel {
		level = MaxVillagerLevel
	}
	profNorm := canonicalResourceName(profession)
	if VillagerProfessionID(profNorm) == 0 && profNorm != VillagerProfessionNone {
		profNorm = VillagerProfessionFarmer
	}

	typeID := ResolveVillagerEntityTypeID()
	entityID := s.entities.Add(&world.Entity{
		TypeID:   int(typeID),
		TypeName: MobTypeVillager,
		X:        x,
		Y:        y,
		Z:        z,
		OnGround: true,
	})

	vtr := s.villagerTracker()
	vtr.mu.Lock()
	var offers []MerchantOffer
	if CanProfessionTrade(profNorm) {
		book := DefaultTradeBook()
		for lvl := MinVillagerLevel; lvl <= level; lvl++ {
			offers = append(offers, book.RollOffers(profNorm, lvl, vtr.rng)...)
		}
	}
	var initialXP int32
	if level > MinVillagerLevel {
		initialXP = VillagerLevelXPThresholds[level-1]
	}
	vs := &VillagerState{
		EntityID:       entityID,
		VillagerType:   VillagerTypePlains,
		Profession:     profNorm,
		Level:          level,
		XP:             initialXP,
		Offers:         offers,
		LastRestockDay: -1,
	}
	vtr.villagers[entityID] = vs
	vtr.mu.Unlock()

	if ent := s.entities.Get(entityID); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastChunk(cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastChunk(cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeVillagerEntityDataPacket(entityID, VillagerTypeID(vs.VillagerType), VillagerProfessionID(vs.Profession), int32(vs.Level)))
	}
	return entityID, nil
}

// VillagerState returns a snapshot of the villager's state for entityID.
func (s *Server) VillagerState(entityID int32) (VillagerState, bool) {
	if s == nil || s.entities == nil {
		return VillagerState{}, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || (ent.TypeName != MobTypeVillager && canonicalResourceName(ent.TypeName) != MobTypeWanderingTrader) {
		return VillagerState{}, false
	}
	vs := s.ensureVillagerState(entityID)
	if vs == nil {
		return VillagerState{}, false
	}
	vtr := s.villagerTracker()
	vtr.mu.Lock()
	cp := vs.Clone()
	vtr.mu.Unlock()
	return cp, true
}

// SetVillagerState replaces the authoritative VillagerState for entityID and
// broadcasts updated VillagerData metadata to nearby players.
func (s *Server) SetVillagerState(entityID int32, state VillagerState) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != MobTypeVillager {
		return false
	}
	cp := state.Clone()
	cp.EntityID = entityID
	if cp.Level < MinVillagerLevel {
		cp.Level = MinVillagerLevel
	}
	if cp.Level > MaxVillagerLevel {
		cp.Level = MaxVillagerLevel
	}
	if cp.VillagerType == "" {
		cp.VillagerType = VillagerTypePlains
	}
	cp.VillagerType = canonicalResourceName(cp.VillagerType)
	cp.Profession = canonicalResourceName(cp.Profession)

	vtr := s.villagerTracker()
	vtr.mu.Lock()
	vtr.villagers[entityID] = &cp
	vtr.mu.Unlock()

	chunkPos := ent.ChunkPos()
	s.BroadcastChunk(chunkPos.X, chunkPos.Z, PlaySetEntityDataCB,
		EncodeVillagerEntityDataPacket(entityID, VillagerTypeID(cp.VillagerType), VillagerProfessionID(cp.Profession), int32(cp.Level)))
	return true
}

// SetVillagerProfession updates a villager's profession and badge level (1..5),
// re-rolling trade offers from the 26.1.2 datapack and broadcasting updated
// VillagerData metadata.
func (s *Server) SetVillagerProfession(entityID int32, profession string, level int) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != MobTypeVillager {
		return false
	}
	if level < MinVillagerLevel {
		level = MinVillagerLevel
	}
	if level > MaxVillagerLevel {
		level = MaxVillagerLevel
	}
	profNorm := canonicalResourceName(profession)

	vtr := s.villagerTracker()
	vtr.mu.Lock()
	var offers []MerchantOffer
	if CanProfessionTrade(profNorm) {
		book := DefaultTradeBook()
		for lvl := MinVillagerLevel; lvl <= level; lvl++ {
			offers = append(offers, book.RollOffers(profNorm, lvl, vtr.rng)...)
		}
	}
	var xp int32
	if level > MinVillagerLevel {
		xp = VillagerLevelXPThresholds[level-1]
	}
	vs, ok := vtr.villagers[entityID]
	if !ok || vs == nil {
		vs = &VillagerState{
			EntityID:       entityID,
			VillagerType:   VillagerTypePlains,
			LastRestockDay: -1,
		}
		vtr.villagers[entityID] = vs
	}
	vs.Profession = profNorm
	vs.Level = level
	vs.XP = xp
	vs.Offers = offers
	vType := vs.VillagerType
	vtr.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastChunk(cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeVillagerEntityDataPacket(entityID, VillagerTypeID(vType), VillagerProfessionID(profNorm), int32(level)))
	return true
}

// VillagerEntityDataPacket returns the encoded ClientboundSetEntityDataPacket (0x63)
// for a villager entity's current VillagerData.
func (s *Server) VillagerEntityDataPacket(entityID int32) []byte {
	vs, ok := s.VillagerState(entityID)
	if !ok {
		return EncodeVillagerEntityDataPacket(entityID, VillagerTypeID(VillagerTypePlains), VillagerProfessionID(VillagerProfessionFarmer), MinVillagerLevel)
	}
	return EncodeVillagerEntityDataPacket(entityID, VillagerTypeID(vs.VillagerType), VillagerProfessionID(vs.Profession), int32(vs.Level))
}

// AddVillagerXP adds xp to the villager with entityID, advancing its badge
// level (1..5) and unlocking new trade tiers from the 26.1.2 datapack when
// XP thresholds are crossed.
func (s *Server) AddVillagerXP(entityID int32, xp int32) (newLevel int, leveledUp bool) {
	if s == nil || xp <= 0 {
		return 0, false
	}
	vs := s.ensureVillagerState(entityID)
	if vs == nil {
		return 0, false
	}
	vtr := s.villagerTracker()
	vtr.mu.Lock()
	vs.XP += xp
	for CanVillagerLevelUp(vs.Level, vs.XP) {
		vs.Level++
		leveledUp = true
		vs.Offers = append(vs.Offers, DefaultTradeBook().RollOffers(vs.Profession, vs.Level, vtr.rng)...)
	}
	newLevel = vs.Level
	vType := vs.VillagerType
	vProf := vs.Profession
	vXP := vs.XP
	offers := make([]MerchantOffer, len(vs.Offers))
	copy(offers, vs.Offers)
	vtr.mu.Unlock()

	if leveledUp {
		if ent := s.entities.Get(entityID); ent != nil {
			cp := ent.ChunkPos()
			s.BroadcastChunk(cp.X, cp.Z, PlaySetEntityDataCB,
				EncodeVillagerEntityDataPacket(entityID, VillagerTypeID(vType), VillagerProfessionID(vProf), int32(newLevel)))
			s.BroadcastChunk(cp.X, cp.Z, protocol.PlayEntityEvent,
				EncodeEntityEventPacket(entityID, EntityEventVillagerHappy))
		}
	}
	s.syncMerchantOffersToViewers(entityID, offers, int32(newLevel), vXP)
	return newLevel, leveledUp
}

// RestockVillager resets Uses to 0 on all offers of the villager with entityID
// (updating demand using vanilla's restock formula), records the restock time,
// and pushes updated ClientboundMerchantOffersPacket to any open trading UI.
func (s *Server) RestockVillager(entityID int32) bool {
	if s == nil {
		return false
	}
	vs := s.ensureVillagerState(entityID)
	if vs == nil {
		return false
	}
	gameTime, dayTime := s.WorldTime()
	curDay := dayTime / TicksPerDay

	vtr := s.villagerTracker()
	vtr.mu.Lock()
	restockedAny := false
	for i := range vs.Offers {
		vs.Offers[i].UpdateDemand()
		if vs.Offers[i].Uses > 0 {
			vs.Offers[i].ResetUses()
			restockedAny = true
		}
	}
	if vs.LastRestockDay != curDay {
		vs.LastRestockDay = curDay
		vs.RestocksToday = 0
	}
	if restockedAny {
		vs.RestocksToday++
		vs.LastRestockGameTime = gameTime
	}
	offers := make([]MerchantOffer, len(vs.Offers))
	copy(offers, vs.Offers)
	vLevel := int32(vs.Level)
	vXP := vs.XP
	vProf := vs.Profession
	vtr.mu.Unlock()

	if restockedAny {
		if ent := s.entities.Get(entityID); ent != nil {
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(VillagerProfessionWorkSound(vProf), SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		}
		s.syncMerchantOffersToViewers(entityID, offers, vLevel, vXP)
	}
	return restockedAny
}

func (s *Server) syncMerchantOffersToViewers(entityID int32, offers []MerchantOffer, level, xp int32) {
	if s == nil {
		return
	}
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		if sess != nil {
			sessions = append(sessions, sess)
		}
	}
	s.playersMu.RUnlock()

	for _, sess := range sessions {
		open := sess.OpenContainer()
		if open == nil || open.MenuType != MenuTypeMerchant || int32(open.BlockX) != entityID {
			continue
		}
		s.UpdateVillagerSpecialPricesForPlayer(sess, entityID)
		effOffers := s.EffectiveOffersForPlayer(sess, entityID, offers)
		if sess.send != nil {
			_ = sess.send(PlayMerchantOffersCB, EncodeMerchantOffersPacket(open.ContainerID, effOffers, level, xp, true, true))
		}
	}
}

// TickVillagers checks villager workstation POI validation/acquisition, daily
// gossip decay, work-hour restocking (2000..9000 time of day, up to 2 restocks
// per day when offers have been used), zombie villager conversions, and cleans
// up state for removed villager entities.
func (s *Server) TickVillagers() {
	if s == nil || s.entities == nil {
		return
	}
	s.TickZombieVillagers(nil)

	gameTime, dayTime := s.WorldTime()
	tod := s.TimeOfDay()
	curDay := dayTime / TicksPerDay
	inWorkHours := tod >= VillagerWorkStartTicks && tod <= VillagerWorkEndTicks

	vtr := s.villagerTracker()
	vtr.mu.Lock()
	if len(vtr.villagers) == 0 {
		vtr.mu.Unlock()
		return
	}
	ids := make([]int32, 0, len(vtr.villagers))
	for entityID := range vtr.villagers {
		ids = append(ids, entityID)
	}
	vtr.mu.Unlock()
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}

	var toRestock []int32
	var toDecay []int32
	var removed []int32

	for _, entityID := range ids {
		ent := s.entities.Get(entityID)
		if ent == nil || (ent.TypeName != MobTypeVillager && canonicalResourceName(ent.TypeName) != MobTypeWanderingTrader) {
			removed = append(removed, entityID)
			continue
		}
		if canonicalResourceName(ent.TypeName) == MobTypeWanderingTrader {
			continue
		}
		if _, hadSite := s.VillagerJobSite(entityID); hadSite {
			s.ValidateVillagerWorkstation(entityID)
		}
		vtr.mu.Lock()
		vs := vtr.villagers[entityID]
		if vs == nil {
			vtr.mu.Unlock()
			continue
		}
		prof := canonicalResourceName(vs.Profession)
		if vs.LastRestockDay != curDay {
			if vs.LastRestockDay >= 0 {
				toDecay = append(toDecay, entityID)
			}
			vs.LastRestockDay = curDay
			vs.RestocksToday = 0
		}
		vtr.mu.Unlock()

		if prof == VillagerProfessionNone || s.IsVillagerJobSiteLost(entityID) {
			s.TryAcquireVillagerWorkstation(entityID)
		}
		if s.IsVillagerJobSiteLost(entityID) {
			continue
		}

		vtr.mu.Lock()
		vs = vtr.villagers[entityID]
		if vs != nil && inWorkHours && vs.RestocksToday < MaxVillagerRestocksPerDay &&
			(vs.LastRestockGameTime <= 0 || gameTime-vs.LastRestockGameTime >= MinRestockIntervalTicks) {
			for i := range vs.Offers {
				if vs.Offers[i].Uses > 0 {
					toRestock = append(toRestock, entityID)
					break
				}
			}
		}
		vtr.mu.Unlock()
	}

	if len(removed) > 0 {
		vtr.mu.Lock()
		for _, id := range removed {
			delete(vtr.villagers, id)
		}
		vtr.mu.Unlock()
	}

	for _, id := range toDecay {
		s.DecayVillagerGossips(id)
	}
	for _, id := range toRestock {
		s.RestockVillager(id)
	}
	for _, id := range removed {
		s.ReleaseVillagerWorkstation(id)
		s.closeMerchantScreensForVillager(id)
	}
}

func (s *Server) closeMerchantScreensForVillager(entityID int32) {
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		if sess != nil {
			sessions = append(sessions, sess)
		}
	}
	s.playersMu.RUnlock()

	for _, sess := range sessions {
		open := sess.OpenContainer()
		if open != nil && open.MenuType == MenuTypeMerchant && int32(open.BlockX) == entityID {
			_ = s.CloseMerchantContainer(sess, open.ContainerID, true)
		}
	}
}

// InteractWithVillager handles a player right-clicking a villager entity:
// enforces interaction reach, plays entity.villager.no if the villager is
// unemployed ("minecraft:none") or a nitwit ("minecraft:nitwit"), or opens the
// merchant trade screen and plays entity.villager.trade.
func (s *Server) InteractWithVillager(session *PlayerSession, entityID int32) (opened bool, containerID int32, err error) {
	if s == nil || session == nil || session.IsDead() {
		return false, 0, nil
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false, 0, nil
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != MobTypeVillager {
		return false, 0, nil
	}
	dx := ent.X - snap.X
	dy := ent.Y - snap.Y
	dz := ent.Z - snap.Z
	if snap.GameMode != GameModeCreative && (dx*dx+dy*dy+dz*dz) > MaxMeleeAttackReach*MaxMeleeAttackReach {
		return false, 0, nil
	}

	vs := s.ensureVillagerState(entityID)
	cp := ent.ChunkPos()
	if vs == nil || !CanProfessionTrade(vs.Profession) || len(vs.Offers) == 0 {
		s.BroadcastChunk(cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:entity.villager.no", SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		return false, 0, nil
	}

	cid, err := s.OpenMerchant(session, entityID)
	if err != nil {
		return false, 0, err
	}
	s.BroadcastChunk(cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.villager.trade", SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true, cid, nil
}

// InteractWithEntity handles right-click entity interactions (opening the
// merchant window for a villager or wandering trader, curing a zombie villager,
// interacting with item frames/armor stands, taming/feeding/sitting pets,
// repairing iron golems, or breeding farm animals).
func (s *Server) InteractWithEntity(session *PlayerSession, targetEntityID int32) (opened bool, containerID int32, err error) {
	if s == nil || s.entities == nil || session == nil {
		return false, 0, nil
	}
	ent := s.entities.Get(targetEntityID)
	if ent == nil {
		return false, 0, nil
	}
	if ent.TypeName == MobTypeVillager {
		return s.InteractWithVillager(session, targetEntityID)
	}
	if canonicalResourceName(ent.TypeName) == MobTypeWanderingTrader {
		return s.InteractWithWanderingTrader(session, targetEntityID)
	}
	if canonicalResourceName(ent.TypeName) == MobTypeZombieVillager {
		handled := s.InteractWithZombieVillager(session, targetEntityID, nil)
		return handled, 0, nil
	}
	if IsDecorationEntity(ent.TypeName) {
		handled := s.InteractWithDecoration(session, targetEntityID, 0)
		return handled, 0, nil
	}
	if IsTameableMob(ent.TypeName) {
		handled, _ := s.InteractWithTameableMob(session, targetEntityID, nil)
		return handled, 0, nil
	}
	if canonicalResourceName(ent.TypeName) == MobTypeIronGolem {
		handled := s.InteractWithIronGolem(session, targetEntityID)
		return handled, 0, nil
	}
	if canonicalResourceName(ent.TypeName) == MobTypeSniffer {
		handled, _ := s.InteractWithSniffer(session, targetEntityID)
		return handled, 0, nil
	}
	if canonicalResourceName(ent.TypeName) == MobTypeArmadillo {
		handled, _ := s.InteractWithArmadillo(session, targetEntityID)
		return handled, 0, nil
	}
	if canonicalResourceName(ent.TypeName) == MobTypeCamel {
		handled, _ := s.InteractWithCamel(session, targetEntityID)
		return handled, 0, nil
	}
	if canonicalResourceName(ent.TypeName) == MobTypeBogged {
		handled := s.InteractWithBogged(session, targetEntityID)
		return handled, 0, nil
	}
	if canonicalResourceName(ent.TypeName) == MobTypeAllay {
		handled, _ := s.InteractWithAllay(session, targetEntityID)
		return handled, 0, nil
	}
	if canonicalResourceName(ent.TypeName) == MobTypeFrog {
		handled, _ := s.InteractWithFrog(session, targetEntityID)
		return handled, 0, nil
	}
	if canonicalResourceName(ent.TypeName) == MobTypeTadpole {
		handled, _ := s.InteractWithTadpole(session, targetEntityID)
		return handled, 0, nil
	}
	if canonicalResourceName(ent.TypeName) == MobTypeAxolotl {
		handled, _ := s.InteractWithAxolotl(session, targetEntityID)
		return handled, 0, nil
	}
	if canonicalResourceName(ent.TypeName) == MobTypeGoat {
		handled, _ := s.InteractWithGoat(session, targetEntityID)
		return handled, 0, nil
	}
	if canonicalResourceName(ent.TypeName) == MobTypeBee {
		handled, _ := s.InteractWithBee(session, targetEntityID)
		return handled, 0, nil
	}
	switch canonicalResourceName(ent.TypeName) {
	case MobTypeFox, MobTypePanda, MobTypeOcelot:
		handled, _ := s.InteractWithFoxPandaOrOcelot(session, targetEntityID, nil)
		return handled, 0, nil
	}
	if IsPassiveAnimal(ent.TypeName) {
		handled, _ := s.InteractWithAnimal(session, targetEntityID)
		return handled, 0, nil
	}
	return false, 0, nil
}

func passiveMobWanderSpeed(typeName string) float64 {
	if canonicalResourceName(typeName) == MobTypeVillager {
		return VillagerWanderSpeed
	}
	return PigWanderSpeed
}

// FaceVillagerTowardPlayer rotates a villager to look at a player who just
// opened its trading screen.
func (s *Server) FaceVillagerTowardPlayer(entityID int32, playerX, playerZ float64) {
	if s == nil || s.entities == nil {
		return
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return
	}
	yaw := yawToward(playerX-ent.X, playerZ-ent.Z)
	if math.IsNaN(float64(yaw)) {
		return
	}
	s.entities.Update(entityID, func(e *world.Entity) {
		e.Yaw = yaw
		e.HeadYaw = yaw
	})
}
