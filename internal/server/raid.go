package server

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// Raider mob entity type identifiers and protocol 775 (Minecraft 26.1.2)
// BuiltInRegistries.ENTITY_TYPE network IDs.
const (
	MobTypePillager   = "minecraft:pillager"
	MobTypeVindicator = "minecraft:vindicator"
	MobTypeEvoker     = "minecraft:evoker"
	MobTypeRavager    = "minecraft:ravager"
	MobTypeWitch      = "minecraft:witch"

	EntityTypeEvokerID     int = 46
	EntityTypePillagerID   int = 101
	EntityTypeRavagerID    int = 106
	EntityTypeVindicatorID int = 140
	EntityTypeWitchID      int = 143

	// Vanilla 26.1.2 raider max health values.
	DefaultPillagerMaxHealth   float32 = 24.0
	DefaultVindicatorMaxHealth float32 = 24.0
	DefaultEvokerMaxHealth     float32 = 24.0
	DefaultRavagerMaxHealth    float32 = 100.0
	DefaultWitchMaxHealth      float32 = 26.0

	// Vanilla 26.1.2 raider base attack damages (Normal difficulty).
	PillagerBaseDamage   float32 = 4.0
	VindicatorBaseDamage float32 = 7.5
	EvokerBaseDamage     float32 = 6.0
	RavagerBaseDamage    float32 = 12.0
	WitchBaseDamage      float32 = 6.0

	// Raider movement and combat parameters.
	RaiderChaseSpeed          = 0.16
	RaiderWanderSpeed         = 0.09
	RaiderAggroRange          = 48.0
	RaiderMeleeReach          = 2.0
	RaiderRangedReach         = 14.0
	RaiderAttackCooldownTicks = 25
)

// Ominous bottle, Bad Omen, Raid Omen, Hero of the Village, and Raid lifecycle constants.
const (
	ItemOminousBottle = "minecraft:ominous_bottle"
	ItemWhiteBanner   = "minecraft:white_banner"

	// OminousBottleDurationTicks is 100 minutes (6000s = 120000 ticks) of Bad Omen.
	OminousBottleDurationTicks int32 = 120000

	// RaidOmenDurationTicks is 30 seconds (600 ticks) before Raid Omen triggers a raid in 26.1.2.
	RaidOmenDurationTicks int32 = 600

	// HeroOfTheVillageDurationTicks is 40 minutes (2400s = 48000 ticks).
	HeroOfTheVillageDurationTicks int32 = 48000

	// MaxBadOmenAmplifier is the highest 0-based Bad Omen / Raid Omen amplifier (level V = 4).
	MaxBadOmenAmplifier int32 = 4

	// MaxBadOmenLevel is the maximum raid Bad Omen level (1..5).
	MaxBadOmenLevel int = 5

	// Village and Raid radii (in blocks).
	VillageDetectionRadius = 48.0
	DefaultRaidRadius      = 96.0
	RaidWaveSpawnMinRadius = 20.0
	RaidWaveSpawnMaxRadius = 32.0

	// Raid timing constants (in game ticks at 20 TPS).
	RaidPreWaveWarmupTicks      int32 = 300   // 15 seconds warmup before each wave
	RaidPostEndCelebrationTicks int32 = 200   // 10 seconds victory/defeat display before removal
	RaidMaxActiveTicks          int64 = 48000 // 40 minutes raid timeout
)

// RaidStatus represents the lifecycle state of a Raid.
type RaidStatus string

const (
	RaidStatusOngoing RaidStatus = "ongoing"
	RaidStatusVictory RaidStatus = "victory"
	RaidStatusDefeat  RaidStatus = "defeat"
	RaidStatusStopped RaidStatus = "stopped"
)

var (
	raiderMobTypes = [5]string{
		MobTypePillager,
		MobTypeVindicator,
		MobTypeEvoker,
		MobTypeRavager,
		MobTypeWitch,
	}
	fallbackRaiderEntityTypeIDs = map[string]int{
		MobTypeEvoker:     EntityTypeEvokerID,
		MobTypePillager:   EntityTypePillagerID,
		MobTypeRavager:    EntityTypeRavagerID,
		MobTypeVindicator: EntityTypeVindicatorID,
		MobTypeWitch:      EntityTypeWitchID,
	}
)

// AllRaiderMobTypes returns a copy of the 5 vanilla raider mob type identifiers.
func AllRaiderMobTypes() []string {
	out := make([]string, len(raiderMobTypes))
	copy(out, raiderMobTypes[:])
	return out
}

// IsRaiderMobType reports whether typeName is a raid member mob (pillager,
// vindicator, evoker, ravager, or witch).
func IsRaiderMobType(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypePillager, MobTypeVindicator, MobTypeEvoker, MobTypeRavager, MobTypeWitch:
		return true
	default:
		return false
	}
}

// ResolveRaiderEntityTypeID returns the protocol 775 BuiltInRegistries.ENTITY_TYPE
// numeric ID for a raider mob type (or any mob supported by ResolveEntityTypeID).
func ResolveRaiderEntityTypeID(typeName string) int {
	norm := canonicalResourceName(typeName)
	if id := ResolveEntityTypeID(norm); id >= 0 {
		return id
	}
	if idx := registry.EntityTypeIndex(norm); idx >= 0 {
		return idx
	}
	if id, ok := fallbackRaiderEntityTypeIDs[norm]; ok {
		return id
	}
	return -1
}

// RaiderMaxHealth returns the vanilla 26.1.2 maximum health for a raider mob.
func RaiderMaxHealth(typeName string) float32 {
	switch canonicalResourceName(typeName) {
	case MobTypePillager:
		return DefaultPillagerMaxHealth
	case MobTypeVindicator:
		return DefaultVindicatorMaxHealth
	case MobTypeEvoker:
		return DefaultEvokerMaxHealth
	case MobTypeRavager:
		return DefaultRavagerMaxHealth
	case MobTypeWitch:
		return DefaultWitchMaxHealth
	default:
		return ResolveMobMaxHealth(typeName)
	}
}

// RaiderBaseAttackDamage returns the base attack damage (Normal difficulty) for a raider mob.
func RaiderBaseAttackDamage(typeName string) float32 {
	switch canonicalResourceName(typeName) {
	case MobTypePillager:
		return PillagerBaseDamage
	case MobTypeVindicator:
		return VindicatorBaseDamage
	case MobTypeEvoker:
		return EvokerBaseDamage
	case MobTypeRavager:
		return RavagerBaseDamage
	case MobTypeWitch:
		return WitchBaseDamage
	default:
		return ZombieAttackDamage
	}
}

// --- Ominous Bottle Item & Per-Slot Amplifier Metadata ---

// IsOminousBottleItem reports whether stack is a minecraft:ominous_bottle.
func IsOminousBottleItem(stack world.ItemStack) bool {
	return !stack.IsEmpty() && canonicalResourceName(stack.ResolvedID()) == ItemOminousBottle
}

type playerOminousBottleMeta struct {
	mu         sync.Mutex
	amplifiers [world.PlayerInventorySlots]int32
	hasCustom  [world.PlayerInventorySlots]bool
}

var playerOminousBottles sync.Map // map[*PlayerSession]*playerOminousBottleMeta

func getOminousBottleMeta(session *PlayerSession) *playerOminousBottleMeta {
	if session == nil {
		return nil
	}
	if v, ok := playerOminousBottles.Load(session); ok {
		return v.(*playerOminousBottleMeta)
	}
	actual, _ := playerOminousBottles.LoadOrStore(session, &playerOminousBottleMeta{})
	return actual.(*playerOminousBottleMeta)
}

// GetSlotOminousBottleAmplifier returns the 0-based Bad Omen amplifier (0..4)
// stored on an ominous_bottle in session's inventory slot (0..45). Defaults to 0.
func GetSlotOminousBottleAmplifier(session *PlayerSession, slot int) int32 {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return 0
	}
	if !IsOminousBottleItem(session.InventorySlot(slot)) {
		return 0
	}
	tr := getOminousBottleMeta(session)
	tr.mu.Lock()
	amp := int32(0)
	if tr.hasCustom[slot] {
		amp = tr.amplifiers[slot]
	}
	tr.mu.Unlock()
	return clampBadOmenAmplifier(amp)
}

// SetSlotOminousBottleAmplifier sets the 0-based Bad Omen amplifier (0..4) for
// an ominous_bottle in session's inventory slot (0..45).
func SetSlotOminousBottleAmplifier(session *PlayerSession, slot int, amplifier int32) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	amp := clampBadOmenAmplifier(amplifier)
	tr := getOminousBottleMeta(session)
	tr.mu.Lock()
	tr.amplifiers[slot] = amp
	tr.hasCustom[slot] = true
	tr.mu.Unlock()
}

func clearSlotOminousBottleAmplifier(session *PlayerSession, slot int) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	tr := getOminousBottleMeta(session)
	tr.mu.Lock()
	tr.amplifiers[slot] = 0
	tr.hasCustom[slot] = false
	tr.mu.Unlock()
}

// SwapSlotOminousBottleAmplifier swaps any tracked ominous_bottle amplifier
// metadata between slotA and slotB on session.
func SwapSlotOminousBottleAmplifier(session *PlayerSession, slotA, slotB int) {
	if session == nil || slotA == slotB ||
		slotA < 0 || slotA >= world.PlayerInventorySlots ||
		slotB < 0 || slotB >= world.PlayerInventorySlots {
		return
	}
	tr := getOminousBottleMeta(session)
	tr.mu.Lock()
	tr.amplifiers[slotA], tr.amplifiers[slotB] = tr.amplifiers[slotB], tr.amplifiers[slotA]
	tr.hasCustom[slotA], tr.hasCustom[slotB] = tr.hasCustom[slotB], tr.hasCustom[slotA]
	tr.mu.Unlock()
}

// AssociateDroppedOminousBottleAmplifier records the Bad Omen amplifier (0..4)
// on a spawned ominous_bottle item entity so picking it up restores the amplifier.
func (s *Server) AssociateDroppedOminousBottleAmplifier(itemEntityID int32, amplifier int32) {
	if s == nil || itemEntityID <= 0 {
		return
	}
	amp := clampBadOmenAmplifier(amplifier)
	rm := s.raidManager()
	rm.mu.Lock()
	if rm.droppedOminousBottles == nil {
		rm.droppedOminousBottles = make(map[int32]int32)
	}
	rm.droppedOminousBottles[itemEntityID] = amp
	rm.mu.Unlock()
}

// DroppedOminousBottleAmplifier returns the Bad Omen amplifier (0..4) associated
// with a dropped ominous_bottle item entity, if any.
func (s *Server) DroppedOminousBottleAmplifier(itemEntityID int32) (int32, bool) {
	if s == nil || itemEntityID <= 0 {
		return 0, false
	}
	rm := s.raidManager()
	rm.mu.Lock()
	amp, ok := rm.droppedOminousBottles[itemEntityID]
	rm.mu.Unlock()
	return clampBadOmenAmplifier(amp), ok
}

// TakeDroppedOminousBottleAmplifier removes and returns the Bad Omen amplifier
// (0..4) associated with a dropped ominous_bottle item entity, if any.
func (s *Server) TakeDroppedOminousBottleAmplifier(itemEntityID int32) (int32, bool) {
	if s == nil || itemEntityID <= 0 {
		return 0, false
	}
	rm := s.raidManager()
	rm.mu.Lock()
	amp, ok := rm.droppedOminousBottles[itemEntityID]
	if ok {
		delete(rm.droppedOminousBottles, itemEntityID)
	}
	rm.mu.Unlock()
	return clampBadOmenAmplifier(amp), ok
}

// SetGrantBadOmenOnCaptainKill configures whether killing a Raid/Patrol Captain
// also directly grants or upgrades EffectBadOmen on the killer in addition to
// dropping an ominous_bottle.
func (s *Server) SetGrantBadOmenOnCaptainKill(enabled bool) {
	if s == nil {
		return
	}
	rm := s.raidManager()
	rm.mu.Lock()
	rm.grantBadOmenOnCaptainKill = enabled
	rm.mu.Unlock()
}

// GrantBadOmenOnCaptainKill reports whether killing a Raid/Patrol Captain
// directly grants EffectBadOmen on the killer.
func (s *Server) GrantBadOmenOnCaptainKill() bool {
	if s == nil {
		return false
	}
	rm := s.raidManager()
	rm.mu.Lock()
	enabled := rm.grantBadOmenOnCaptainKill
	rm.mu.Unlock()
	return enabled
}

// GrantBadOmenFromCaptainKill grants or upgrades EffectBadOmen (120000 ticks = 100m,
// clamped to amplifier 0..4 = Bad Omen I..V) on session when killing a captain.
func (s *Server) GrantBadOmenFromCaptainKill(session *PlayerSession, captainAmplifier int32) int32 {
	if s == nil || session == nil || session.IsDead() {
		return 0
	}
	amp := clampBadOmenAmplifier(captainAmplifier)
	if existing, ok := session.PlayerEffect(EffectBadOmen); ok {
		next := existing.Amplifier + 1
		if amp > next {
			next = amp
		}
		amp = clampBadOmenAmplifier(next)
	}
	eff := NewStatusEffectInstance(EffectBadOmen, OminousBottleDurationTicks, amp)
	s.ApplyPlayerEffect(session, eff)
	return amp
}

func clampBadOmenAmplifier(amp int32) int32 {
	if amp < 0 {
		return 0
	}
	if amp > MaxBadOmenAmplifier {
		return MaxBadOmenAmplifier
	}
	return amp
}

// DrinkOminousBottle consumes one minecraft:ominous_bottle from session's
// specified hand (HandMain or HandOffHand), grants minecraft:bad_omen for
// 100 minutes (120000 ticks) at the bottle's amplifier (0..4), plays the
// ominous bottle dispose sound, and fires consume_item advancements.
// Unlike regular potions, ominous bottles shatter on use and do not leave a glass bottle.
func (s *Server) DrinkOminousBottle(session *PlayerSession, hand int32) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	slot, stack := session.handSlotAndStack(hand)
	if !IsOminousBottleItem(stack) {
		return false
	}
	amp := GetSlotOminousBottleAmplifier(session, slot)
	snap := session.Snapshot()
	if snap.GameMode != GameModeCreative {
		rem := stack.Count - 1
		if rem <= 0 {
			clearSlotOminousBottleAmplifier(session, slot)
			session.SetInventorySlot(slot, world.ItemStack{})
		} else {
			session.SetInventorySlot(slot, world.NewItemStack(stack.ResolvedItemID(), rem))
		}
	}
	if session.send != nil {
		_ = session.send(protocol.PlayEntityEvent, EncodeEntityEventPacket(session.EntityID, EntityEventUseItemComplete))
		stateID := session.NextContainerStateID()
		_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotPacket(stateID, slot, session.InventorySlot(slot)))
	}

	s.DrinkOminousBottleWithAmplifier(session, amp)
	return true
}

// DrinkOminousBottleWithAmplifier applies Bad Omen (120000 ticks, amplifier 0..4)
// to session, broadcasts the ominous bottle dispose sound, and fires consume_item
// advancement criteria.
func (s *Server) DrinkOminousBottleWithAmplifier(session *PlayerSession, amplifier int32) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	amp := clampBadOmenAmplifier(amplifier)
	if existing, ok := session.PlayerEffect(EffectBadOmen); ok && existing.Amplifier >= amp {
		// In vanilla, drinking an ominous bottle or killing a captain upgrades
		// Bad Omen by +1 up to amplifier 4 (Bad Omen V) if already active.
		amp = clampBadOmenAmplifier(existing.Amplifier + 1)
	}
	eff := NewStatusEffectInstance(EffectBadOmen, OminousBottleDurationTicks, amp)
	s.ApplyPlayerEffect(session, eff)

	snap := session.Snapshot()
	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastChunk(cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.ominous_bottle.dispose", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))

	s.TriggerConsumeItem(session, world.NewNamedItemStack(ItemOminousBottle, 1))
	return true
}

// --- Village Detection (Villagers & Beds) ---

func isBedBlockState(state uint16) bool {
	return IsBedBlock(state)
}

// FindVillageCenterNear searches for a village near (x, y, z) in dim within
// radius by locating living villager entities and/or bed blocks in loaded chunks.
// Returns the centroid (centerX, centerY, centerZ), counts of villagers and beds,
// and whether a valid village was found.
func (s *Server) FindVillageCenterNear(dim string, x, y, z, radius float64) (centerX, centerY, centerZ float64, villagerCount, bedCount int, found bool) {
	if s == nil {
		return 0, 0, 0, 0, 0, false
	}
	if radius <= 0 {
		radius = VillageDetectionRadius
	}
	normDim := world.NormalizeDimension(dim)
	rSq := radius * radius

	var sumX, sumY, sumZ float64
	var weight int

	if s.entities != nil {
		allEnts := s.entities.AppendAll(nil)
		for i := range allEnts {
			ent := &allEnts[i]
			if ent.TypeName != MobTypeVillager || ent.EffectiveDimension() != normDim {
				continue
			}
			dx := ent.X - x
			dy := ent.Y - y
			dz := ent.Z - z
			if dx*dx+dy*dy+dz*dz <= rSq {
				villagerCount++
				sumX += ent.X
				sumY += ent.Y
				sumZ += ent.Z
				weight++
			}
		}
	}

	// Also include any claimed villager beds and claimed workstations in normDim within radius.
	if sm := s.villagerSocial(); sm != nil {
		sm.mu.Lock()
		for _, site := range sm.bedByEntity {
			if world.NormalizeDimension(site.Dimension) != normDim {
				continue
			}
			sx := float64(site.Pos.X) + 0.5
			sy := float64(site.Pos.Y)
			sz := float64(site.Pos.Z) + 0.5
			dx := sx - x
			dy := sy - y
			dz := sz - z
			if dx*dx+dy*dy+dz*dz <= rSq {
				bedCount++
				sumX += sx
				sumY += sy
				sumZ += sz
				weight++
			}
		}
		for _, site := range sm.jobSiteByEntity {
			if world.NormalizeDimension(site.Dimension) != normDim {
				continue
			}
			sx := float64(site.Pos.X) + 0.5
			sy := float64(site.Pos.Y)
			sz := float64(site.Pos.Z) + 0.5
			dx := sx - x
			dy := sy - y
			dz := sz - z
			if dx*dx+dy*dy+dz*dz <= rSq {
				sumX += sx
				sumY += sy
				sumZ += sz
				weight++
			}
		}
		sm.mu.Unlock()
	}

	// If no villagers or claimed POIs were found nearby, also check for bed blocks within a
	// compact neighborhood around (x, y, z) so bed POIs can anchor a village.
	cache := s.DimensionChunks(normDim)
	if weight == 0 && cache != nil {
		bedScanH := int(math.Min(radius, 8))
		bedScanV := 4
		bx0 := int(math.Floor(x))
		by0 := int(math.Floor(y))
		bz0 := int(math.Floor(z))
		for dy := -bedScanV; dy <= bedScanV; dy++ {
			by := by0 + dy
			if by < world.MinY || by >= world.MinY+world.WorldHeight {
				continue
			}
			for dx := -bedScanH; dx <= bedScanH; dx++ {
				for dz := -bedScanH; dz <= bedScanH; dz++ {
					if float64(dx*dx+dy*dy+dz*dz) > rSq {
						continue
					}
					bx, bz := bx0+dx, bz0+dz
					if isBedBlockState(cache.GetBlock(bx, by, bz)) {
						bedCount++
						sumX += float64(bx) + 0.5
						sumY += float64(by)
						sumZ += float64(bz) + 0.5
						weight++
					}
				}
			}
		}
	}

	if weight == 0 {
		return 0, 0, 0, 0, 0, false
	}
	inv := 1.0 / float64(weight)
	return sumX * inv, sumY * inv, sumZ * inv, villagerCount, bedCount, true
}

// HasVillagePresenceAt reports whether at least one living villager or claimed
// bed still exists within radius of (centerX, centerY, centerZ) in dim.
func (s *Server) HasVillagePresenceAt(dim string, centerX, centerY, centerZ, radius float64) bool {
	_, _, _, vCount, bCount, found := s.FindVillageCenterNear(dim, centerX, centerY, centerZ, radius)
	return found && (vCount > 0 || bCount > 0)
}

// --- Raid Wave Composition & Difficulty Scaling ---

// RaidWaveMember describes one raider to spawn in a raid wave.
type RaidWaveMember struct {
	TypeName  string
	IsCaptain bool
}

// RaidWaveCountForDifficulty returns the base number of raid waves for a world
// difficulty (matching vanilla Raid.getNumGroups):
//   - Peaceful: 0
//   - Easy:     3
//   - Normal:   5
//   - Hard:     7
func RaidWaveCountForDifficulty(diff Difficulty) int {
	switch diff {
	case DifficultyPeaceful:
		return 0
	case DifficultyEasy:
		return 3
	case DifficultyNormal:
		return 5
	case DifficultyHard:
		return 7
	default:
		return 5
	}
}

// TotalRaidWaveCount returns the total number of waves for a raid at diff and
// badOmenLevel (1..5), adding +1 bonus wave when badOmenLevel > 1.
func TotalRaidWaveCount(diff Difficulty, badOmenLevel int) int {
	base := RaidWaveCountForDifficulty(diff)
	if base <= 0 {
		return 0
	}
	if badOmenLevel > 1 {
		return base + 1
	}
	return base
}

// vanillaRaidWaveTable matches net.minecraft.world.entity.raid.Raid.RaiderType.spawnsPerWaveBeforeBonus
// for waves 1..7 (index 0..6) plus index 7 for the bonus wave.
type waveSpawnCounts struct {
	pillagers   int
	vindicators int
	evokers     int
	ravagers    int
	witches     int
}

var vanillaRaidWaveTable = [8]waveSpawnCounts{
	// Wave 1
	{pillagers: 4, vindicators: 0, evokers: 0, ravagers: 0, witches: 0},
	// Wave 2
	{pillagers: 3, vindicators: 2, evokers: 0, ravagers: 0, witches: 0},
	// Wave 3
	{pillagers: 3, vindicators: 0, evokers: 0, ravagers: 1, witches: 0},
	// Wave 4
	{pillagers: 4, vindicators: 1, evokers: 0, ravagers: 0, witches: 3},
	// Wave 5
	{pillagers: 4, vindicators: 4, evokers: 1, ravagers: 1, witches: 0},
	// Wave 6
	{pillagers: 4, vindicators: 2, evokers: 1, ravagers: 0, witches: 1},
	// Wave 7
	{pillagers: 2, vindicators: 5, evokers: 2, ravagers: 2, witches: 1},
	// Wave 8 (Bonus wave for Bad Omen II+)
	{pillagers: 4, vindicators: 5, evokers: 2, ravagers: 2, witches: 2},
}

// RollRaidWaveMembers returns the list of raiders to spawn for 1-indexed wave
// (1..8). Exactly one raider in the wave is marked as the wave's Raid Captain.
func RollRaidWaveMembers(wave int, diff Difficulty, bonusWave bool, rng *rand.Rand) []RaidWaveMember {
	if diff == DifficultyPeaceful || wave < 1 {
		return nil
	}
	idx := wave - 1
	if bonusWave && idx >= 7 {
		idx = 7
	} else if idx > 6 {
		idx = 6
	}
	counts := vanillaRaidWaveTable[idx]

	// Add vanilla random bonus (+0..1 pillager/vindicator, +1 witch on Normal/Hard after wave 3).
	if rng != nil {
		counts.pillagers += rng.Intn(2)
		if wave >= 2 && diff >= DifficultyNormal {
			counts.vindicators += rng.Intn(2)
		}
		if wave >= 3 && diff == DifficultyHard && counts.witches == 0 {
			counts.witches = 1
		}
	}

	var members []RaidWaveMember
	appendType := func(typeName string, n int) {
		for i := 0; i < n; i++ {
			members = append(members, RaidWaveMember{TypeName: typeName})
		}
	}
	appendType(MobTypePillager, counts.pillagers)
	appendType(MobTypeVindicator, counts.vindicators)
	appendType(MobTypeEvoker, counts.evokers)
	appendType(MobTypeRavager, counts.ravagers)
	appendType(MobTypeWitch, counts.witches)

	if len(members) > 0 {
		// Designate the first illager (pillager/vindicator/evoker) as wave captain.
		captainAssigned := false
		for i := range members {
			switch members[i].TypeName {
			case MobTypePillager, MobTypeVindicator, MobTypeEvoker:
				members[i].IsCaptain = true
				captainAssigned = true
			}
			if captainAssigned {
				break
			}
		}
		if !captainAssigned {
			members[0].IsCaptain = true
		}
	}
	return members
}

// --- Raid Struct & Server Raid Manager ---

// RaiderState tracks a single spawned raider entity participating in a Raid or Patrol.
type RaiderState struct {
	EntityID         int32
	TypeName         string
	RaidID           int32
	Wave             int
	IsCaptain        bool
	BadOmenAmplifier int32
	MaxHealth        float32
	AttackCooldown   int
}

// Raid represents an active or recently concluded village raid in a dimension.
type Raid struct {
	ID              int32
	Dimension       string
	CenterX         float64
	CenterY         float64
	CenterZ         float64
	Status          RaidStatus
	BadOmenLevel    int
	GroupsSpawned   int
	TotalWaves      int
	TicksActive     int64
	PreWaveTicks    int32
	PostEndTicks    int32
	TotalWaveHealth float32
	BossBarUUID     [16]byte
	HasBossBar      bool
	Raiders         map[int32]*RaiderState // entityID -> RaiderState
	Heroes          map[[16]byte]bool      // participating player UUIDs
}

// Clone returns a deep copy of r.
func (r *Raid) Clone() *Raid {
	if r == nil {
		return nil
	}
	cp := *r
	cp.Raiders = make(map[int32]*RaiderState, len(r.Raiders))
	for k, v := range r.Raiders {
		if v != nil {
			rv := *v
			cp.Raiders[k] = &rv
		}
	}
	cp.Heroes = make(map[[16]byte]bool, len(r.Heroes))
	for u, ok := range r.Heroes {
		cp.Heroes[u] = ok
	}
	return &cp
}

// AliveRaiderCount returns the number of raiders currently tracked in this raid.
func (r *Raid) AliveRaiderCount() int {
	if r == nil {
		return 0
	}
	return len(r.Raiders)
}

type raidOmenPending struct {
	Dimension string
	CenterX   float64
	CenterY   float64
	CenterZ   float64
	Amplifier int32
}

type serverRaidManager struct {
	mu                        sync.Mutex
	nextRaidID                int32
	raids                     map[int32]*Raid
	raiderIndex               map[int32]*RaiderState       // entityID -> RaiderState (both raid members and patrol captains)
	omenCenters               map[[16]byte]raidOmenPending // player UUID -> pending Raid Omen village center
	droppedOminousBottles     map[int32]int32              // itemEntityID -> Bad Omen amplifier (0..4)
	grantBadOmenOnCaptainKill bool
	rng                       *rand.Rand
}

var serverRaids sync.Map // map[*Server]*serverRaidManager

func (s *Server) raidManager() *serverRaidManager {
	if v, ok := serverRaids.Load(s); ok {
		return v.(*serverRaidManager)
	}
	actual, _ := serverRaids.LoadOrStore(s, &serverRaidManager{
		raids:                 make(map[int32]*Raid),
		raiderIndex:           make(map[int32]*RaiderState),
		omenCenters:           make(map[[16]byte]raidOmenPending),
		droppedOminousBottles: make(map[int32]int32),
		rng:                   rand.New(rand.NewSource(time.Now().UnixNano())),
	})
	return actual.(*serverRaidManager)
}

// ActiveRaids returns snapshots of all currently tracked raids sorted by ID.
func (s *Server) ActiveRaids() []*Raid {
	if s == nil {
		return nil
	}
	rm := s.raidManager()
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if len(rm.raids) == 0 {
		return nil
	}
	out := make([]*Raid, 0, len(rm.raids))
	for _, r := range rm.raids {
		if r != nil {
			out = append(out, r.Clone())
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

// RaidByID returns a snapshot of the raid with raidID, if present.
func (s *Server) RaidByID(raidID int32) (*Raid, bool) {
	if s == nil {
		return nil, false
	}
	rm := s.raidManager()
	rm.mu.Lock()
	defer rm.mu.Unlock()
	r, ok := rm.raids[raidID]
	if !ok || r == nil {
		return nil, false
	}
	return r.Clone(), true
}

// RaidAt looks up an active raid whose center is within DefaultRaidRadius (96 blocks)
// of (x, y, z) in dim.
func (s *Server) RaidAt(dim string, x, y, z float64) (*Raid, bool) {
	if s == nil {
		return nil, false
	}
	normDim := world.NormalizeDimension(dim)
	maxDistSq := DefaultRaidRadius * DefaultRaidRadius

	rm := s.raidManager()
	rm.mu.Lock()
	defer rm.mu.Unlock()

	var best *Raid
	bestDistSq := maxDistSq
	for _, r := range rm.raids {
		if r == nil || r.Status == RaidStatusStopped || world.NormalizeDimension(r.Dimension) != normDim {
			continue
		}
		dx := r.CenterX - x
		dy := r.CenterY - y
		dz := r.CenterZ - z
		dSq := dx*dx + dy*dy + dz*dz
		if dSq <= bestDistSq {
			best = r
			bestDistSq = dSq
		}
	}
	if best == nil {
		return nil, false
	}
	return best.Clone(), true
}

// --- Spawning Raiders & Raid Captains ---

// SpawnRaiderMob spawns a raider mob of typeName at (x, y, z), initializes its
// vanilla raider max health (e.g. 100 HP for ravager, 24 HP for illagers, 26 HP
// for witch), registers it in the raid/captain tracker, and broadcasts PlayAddEntity.
func (s *Server) SpawnRaiderMob(x, y, z float64, typeName string, isCaptain bool, raidID int32, wave int) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	norm := canonicalResourceName(typeName)
	if !IsRaiderMobType(norm) {
		return 0, fmt.Errorf("server: %q is not a raider mob type", norm)
	}

	// Try Agent A's SpawnMob first; if the entity type ID is not in mobs.go's
	// fallback table, add the entity directly using ResolveRaiderEntityTypeID.
	entityID, err := s.SpawnMob(x, y, z, norm)
	if err != nil || entityID <= 0 {
		typeID := ResolveRaiderEntityTypeID(norm)
		if typeID < 0 {
			return 0, fmt.Errorf("server: unknown raider entity type %q", norm)
		}
		entityID = s.entities.Add(&world.Entity{
			TypeID:   typeID,
			TypeName: norm,
			X:        x,
			Y:        y,
			Z:        z,
			OnGround: true,
		})
		if ent := s.entities.Get(entityID); ent != nil {
			cp := ent.ChunkPos()
			s.BroadcastChunk(cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		}
	}

	maxHP := RaiderMaxHealth(norm)
	s.SetMobHealth(entityID, maxHP)
	s.ensureIllagerOrRaiderState(entityID, norm, isCaptain)

	rm := s.raidManager()
	rm.mu.Lock()
	rs := &RaiderState{
		EntityID:         entityID,
		TypeName:         norm,
		RaidID:           raidID,
		Wave:             wave,
		IsCaptain:        isCaptain,
		BadOmenAmplifier: 0,
		MaxHealth:        maxHP,
	}
	rm.raiderIndex[entityID] = rs
	if raidID > 0 {
		if r, ok := rm.raids[raidID]; ok && r != nil {
			r.Raiders[entityID] = rs
		}
	}
	rm.mu.Unlock()

	return entityID, nil
}

// SpawnPatrolCaptain spawns a standalone raid captain (e.g. a pillager patrol
// leader) at (x, y, z) whose death drops an ominous_bottle with badOmenAmplifier
// (0..4) and grants the "Voluntary Exile" advancement.
func (s *Server) SpawnPatrolCaptain(x, y, z float64, typeName string, badOmenAmplifier int32) (int32, error) {
	if typeName == "" {
		typeName = MobTypePillager
	}
	id, err := s.SpawnRaiderMob(x, y, z, typeName, true, 0, 0)
	if err != nil {
		return 0, err
	}
	s.MarkRaidCaptain(id, badOmenAmplifier)
	return id, nil
}

// MarkRaidCaptain marks entityID as a raid captain carrying an Ominous Banner
// with the specified Bad Omen amplifier (0..4).
func (s *Server) MarkRaidCaptain(entityID int32, badOmenAmplifier int32) {
	if s == nil || entityID <= 0 {
		return
	}
	amp := clampBadOmenAmplifier(badOmenAmplifier)
	typeName := MobTypePillager
	if s.entities != nil {
		if ent := s.entities.Get(entityID); ent != nil {
			typeName = canonicalResourceName(ent.TypeName)
		}
	}
	s.ensureIllagerOrRaiderState(entityID, typeName, true)
	rm := s.raidManager()
	rm.mu.Lock()
	rs, ok := rm.raiderIndex[entityID]
	if !ok || rs == nil {
		rs = &RaiderState{
			EntityID:  entityID,
			TypeName:  typeName,
			MaxHealth: RaiderMaxHealth(typeName),
		}
		rm.raiderIndex[entityID] = rs
	}
	rs.IsCaptain = true
	rs.BadOmenAmplifier = amp
	rm.mu.Unlock()
}

// IsRaidCaptain reports whether entityID is a Raid Captain and returns its
// Bad Omen amplifier (0..4).
func (s *Server) IsRaidCaptain(entityID int32) (isCaptain bool, badOmenAmplifier int32) {
	if s == nil || entityID <= 0 {
		return false, 0
	}
	rm := s.raidManager()
	rm.mu.Lock()
	rs, ok := rm.raiderIndex[entityID]
	if ok && rs != nil && rs.IsCaptain {
		amp := rs.BadOmenAmplifier
		rm.mu.Unlock()
		return true, amp
	}
	rm.mu.Unlock()
	if s.IsIllagerCaptain(entityID) {
		return true, 0
	}
	return false, 0
}

// IsIllagerCaptain reports whether entityID is marked as a captain in the
// server's illager behavior tracker.
func (s *Server) IsIllagerCaptain(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	st, ok := s.GetIllagerState(entityID)
	return ok && st.IsCaptain
}

// RaiderEntityDataPacket returns the SynchedEntityData packet payload for a
// raider/illager/witch/vex entity (celebrating flag, crossbow charge, spell
// casting, witch potion drinking, vex charging), or nil if not applicable.
func (s *Server) RaiderEntityDataPacket(entityID int32) []byte {
	if s == nil || s.entities == nil || entityID <= 0 {
		return nil
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return nil
	}
	norm := canonicalResourceName(ent.TypeName)
	switch norm {
	case MobTypePillager:
		mgr := s.illagerManager()
		mgr.mu.RLock()
		st := mgr.illagers[entityID]
		celebrating := false
		charging := false
		if st != nil {
			celebrating = st.Celebrating
			charging = st.ChargingCrossbow
		}
		mgr.mu.RUnlock()
		return EncodePillagerDataPacket(entityID, celebrating, charging)
	case MobTypeVindicator, MobTypeEvoker, MobTypeIllusioner:
		mgr := s.illagerManager()
		mgr.mu.RLock()
		st := mgr.illagers[entityID]
		celebrating := false
		spell := byte(IllagerSpellNone)
		if st != nil {
			celebrating = st.Celebrating
			spell = st.ActiveSpell
		}
		mgr.mu.RUnlock()
		return EncodeSpellcasterDataPacket(entityID, celebrating, spell)
	case MobTypeWitch:
		mgr := s.illagerManager()
		mgr.mu.RLock()
		st := mgr.witches[entityID]
		drinking := false
		if st != nil {
			drinking = st.Drinking
		}
		mgr.mu.RUnlock()
		return EncodeWitchDataPacket(entityID, false, drinking)
	case MobTypeVex:
		mgr := s.illagerManager()
		mgr.mu.RLock()
		st := mgr.vexes[entityID]
		charging := false
		if st != nil {
			charging = st.Charging
		}
		mgr.mu.RUnlock()
		return EncodeVexDataPacket(entityID, charging)
	default:
		return nil
	}
}

// RaiderEquipmentPacket returns the PlaySetEquipment packet payload for an
// illager/raider/vex (mainhand crossbow/iron_axe/iron_sword and Ominous Banner
// on head when the entity is a Raid or Patrol Captain), or nil if none.
func (s *Server) RaiderEquipmentPacket(entityID int32) []byte {
	if s == nil || s.entities == nil || entityID <= 0 {
		return nil
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return nil
	}
	norm := canonicalResourceName(ent.TypeName)
	switch norm {
	case MobTypePillager, MobTypeVindicator, MobTypeEvoker, MobTypeIllusioner:
		isCap, _ := s.IsRaidCaptain(entityID)
		return EncodeIllagerEquipmentPacket(entityID, DefaultIllagerMainHandWeapon(norm), isCap)
	case MobTypeVex:
		return EncodeIllagerEquipmentPacket(entityID, DefaultIllagerMainHandWeapon(MobTypeVex), false)
	default:
		return nil
	}
}

// SetRaiderCelebrating sets the Celebrating state on an illager or witch and
// broadcasts its updated SynchedEntityData packet to nearby viewers.
func (s *Server) SetRaiderCelebrating(entityID int32, celebrating bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return
	}
	norm := canonicalResourceName(ent.TypeName)
	mgr := s.illagerManager()
	mgr.mu.Lock()
	switch norm {
	case MobTypePillager, MobTypeVindicator, MobTypeEvoker, MobTypeIllusioner:
		if st := mgr.illagers[entityID]; st != nil {
			st.Celebrating = celebrating
		}
	}
	mgr.mu.Unlock()
	if pkt := s.RaiderEntityDataPacket(entityID); len(pkt) > 0 {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB, pkt)
	}
}

// TrySpawnNaturalIllagerPatrol attempts to spawn a natural Overworld pillager
// patrol (captain with Ominous Banner + 1..4 pillager followers) 24..48 blocks
// from an eligible Overworld player who is not inside a village, respecting
// Peaceful difficulty and the doPatrolSpawning game rule.
func (s *Server) TrySpawnNaturalIllagerPatrol(rng *rand.Rand) (patrolID int32, memberIDs []int32, ok bool) {
	if s == nil || s.entities == nil {
		return 0, nil, false
	}
	diff := s.Difficulty()
	if diff == DifficultyPeaceful || !s.GameRuleBool("doPatrolSpawning") {
		return 0, nil, false
	}
	if rng == nil {
		rm := s.raidManager()
		rm.mu.Lock()
		rng = rm.rng
		rm.mu.Unlock()
	}

	var targetPlayer *PlayerSession
	for _, sess := range s.onlinePlayersSnapshot() {
		if sess == nil || sess.IsDead() {
			continue
		}
		snap := sess.Snapshot()
		if snap.GameMode == GameModeSpectator || world.NormalizeDimension(snap.Dimension) != world.DimensionOverworld {
			continue
		}
		if _, _, _, _, _, inVillage := s.FindVillageCenterNear(world.DimensionOverworld, snap.X, snap.Y, snap.Z, VillageDetectionRadius); inVillage {
			continue
		}
		targetPlayer = sess
		break
	}
	if targetPlayer == nil {
		return 0, nil, false
	}
	snap := targetPlayer.Snapshot()

	// Avoid spawning another patrol if one is already active near this player.
	mgr := s.illagerManager()
	mgr.mu.RLock()
	for _, pat := range mgr.patrols {
		if pat == nil || world.NormalizeDimension(pat.Dimension) != world.DimensionOverworld {
			continue
		}
		if lead := s.entities.Get(pat.LeaderID); lead != nil {
			dx := lead.X - snap.X
			dz := lead.Z - snap.Z
			if dx*dx+dz*dz <= 64.0*64.0 {
				mgr.mu.RUnlock()
				return 0, nil, false
			}
		}
	}
	mgr.mu.RUnlock()

	const (
		patrolSpawnMinRadius = 24.0
		patrolSpawnMaxRadius = 48.0
	)
	dist := patrolSpawnMinRadius
	if rng != nil {
		dist += rng.Float64() * (patrolSpawnMaxRadius - patrolSpawnMinRadius)
	}
	angle := 0.0
	if rng != nil {
		angle = rng.Float64() * 2.0 * math.Pi
	}
	sx := snap.X + math.Cos(angle)*dist
	sz := snap.Z + math.Sin(angle)*dist
	sy := s.findRaidSpawnY(int(math.Floor(sx)), int(math.Floor(snap.Y)), int(math.Floor(sz)))

	memberCount := 2
	switch diff {
	case DifficultyNormal:
		memberCount = 3
	case DifficultyHard:
		memberCount = 4
	}
	pid, _, ids, err := s.SpawnIllagerPatrol(world.DimensionOverworld, sx, sy, sz, memberCount, 0, rng)
	if err != nil || pid <= 0 || len(ids) == 0 {
		return 0, nil, false
	}
	s.MarkRaidCaptain(ids[0], 0)
	return pid, ids, true
}

// StepIllagerPatrols advances active pillager patrols: promotes surviving
// followers or banner pickers when a patrol leader dies, and marches the patrol
// leader toward its patrol waypoint when not engaged in combat.
func (s *Server) StepIllagerPatrols() {
	if s == nil || s.entities == nil {
		return
	}
	mgr := s.illagerManager()
	mgr.mu.RLock()
	if len(mgr.patrols) == 0 {
		mgr.mu.RUnlock()
		return
	}
	patrolIDs := make([]int32, 0, len(mgr.patrols))
	for id := range mgr.patrols {
		patrolIDs = append(patrolIDs, id)
	}
	mgr.mu.RUnlock()

	for _, pid := range patrolIDs {
		mgr.mu.Lock()
		pat, exists := mgr.patrols[pid]
		if !exists || pat == nil {
			mgr.mu.Unlock()
			continue
		}
		alive := make([]int32, 0, len(pat.MemberIDs))
		for _, mid := range pat.MemberIDs {
			if ent := s.entities.Get(mid); ent != nil && s.MobHealth(mid) > 0 {
				alive = append(alive, mid)
			}
		}
		if len(alive) == 0 {
			delete(mgr.patrols, pid)
			mgr.mu.Unlock()
			continue
		}
		pat.MemberIDs = alive
		leaderID := pat.LeaderID
		leaderAlive := false
		for _, mid := range alive {
			if mid == leaderID {
				leaderAlive = true
				break
			}
		}
		targetX, targetZ := pat.TargetX, pat.TargetZ
		dim := pat.Dimension
		mgr.mu.Unlock()

		if !leaderAlive {
			promoted := int32(0)
			for _, mid := range alive {
				if s.TryPromoteRaiderFromDroppedBanner(mid) {
					promoted = mid
					break
				}
			}
			if promoted == 0 {
				promoted = alive[0]
				s.PromoteRaiderToCaptain(promoted, 0)
			}
			s.MarkRaidCaptain(promoted, 0)
			mgr.mu.Lock()
			if live := mgr.patrols[pid]; live != nil {
				live.LeaderID = promoted
			}
			mgr.mu.Unlock()
			leaderID = promoted
		}

		leaderEnt := s.entities.Get(leaderID)
		if leaderEnt == nil {
			continue
		}
		// Only march along the waypoint if no survival player is within aggro range.
		targets := s.appendPlayerTargets(nil)
		if _, engaged := selectTargetWithinRange(targets, leaderEnt.X, leaderEnt.Y, leaderEnt.Z, RaiderAggroRange); engaged {
			continue
		}
		dx := targetX - leaderEnt.X
		dz := targetZ - leaderEnt.Z
		if dx*dx+dz*dz <= 6.25 {
			mgr.mu.Lock()
			if live := mgr.patrols[pid]; live != nil {
				live.TargetX = leaderEnt.X + 24.0
				live.TargetZ = leaderEnt.Z + 24.0
				targetX, targetZ = live.TargetX, live.TargetZ
			}
			mgr.mu.Unlock()
		}
		cache := s.DimensionChunks(dim)
		feetY := int(math.Floor(leaderEnt.Y + 0.01))
		s.stepEntityTowardPoint(leaderEnt, cache, feetY, targetX, leaderEnt.Y, targetZ, RaiderChaseSpeed*0.75, 1.5)
	}
}

// RaidLootDropsForRaider returns the vanilla item drops for a killed raider of
// typeName, including 1x ominous_bottle and 1x white_banner (Ominous Banner)
// when isCaptain is true.
func RaidLootDropsForRaider(typeName string, isCaptain bool, badOmenAmp int32, rng *rand.Rand) []world.ItemStack {
	var drops []world.ItemStack
	switch canonicalResourceName(typeName) {
	case MobTypePillager:
		arrows := int32(1)
		if rng != nil {
			arrows = int32(rng.Intn(3))
		}
		if arrows > 0 {
			if st := world.NewNamedItemStack("minecraft:arrow", arrows); !st.IsEmpty() {
				drops = append(drops, st)
			}
		}
	case MobTypeVindicator:
		if st := world.NewNamedItemStack("minecraft:emerald", 1); !st.IsEmpty() {
			drops = append(drops, st)
		}
	case MobTypeEvoker:
		if st := world.NewNamedItemStack("minecraft:totem_of_undying", 1); !st.IsEmpty() {
			drops = append(drops, st)
		}
		if st := world.NewNamedItemStack("minecraft:emerald", 1); !st.IsEmpty() {
			drops = append(drops, st)
		}
	case MobTypeRavager:
		if st := world.NewNamedItemStack("minecraft:saddle", 1); !st.IsEmpty() {
			drops = append(drops, st)
		}
	case MobTypeWitch:
		for _, itemID := range []string{"minecraft:redstone", "minecraft:glowstone_dust", "minecraft:gunpowder", "minecraft:glass_bottle"} {
			if st := world.NewNamedItemStack(itemID, 1); !st.IsEmpty() {
				drops = append(drops, st)
				break
			}
		}
	}

	if isCaptain {
		if bottle := world.NewNamedItemStack(ItemOminousBottle, 1); !bottle.IsEmpty() {
			drops = append(drops, bottle)
		}
		if banner := world.NewNamedItemStack(ItemWhiteBanner, 1); !banner.IsEmpty() {
			drops = append(drops, banner)
		}
	}
	return drops
}

// --- Bad Omen -> Raid Omen -> Raid Start & Merging ---

// CheckPlayerBadOmenInVillage checks whether session currently has
// minecraft:bad_omen while inside a village (within 48 blocks of a villager or
// bed). In Minecraft 26.1.2, entering a village with Bad Omen converts
// Bad Omen into Raid Omen (600 ticks = 30s) at the same amplifier.
func (s *Server) CheckPlayerBadOmenInVillage(session *PlayerSession) (converted bool, centerX, centerY, centerZ float64) {
	if s == nil || session == nil || session.IsDead() {
		return false, 0, 0, 0
	}
	if s.Difficulty() == DifficultyPeaceful || s.GameRuleBool("disableRaids") {
		return false, 0, 0, 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return false, 0, 0, 0
	}
	badOmen, ok := session.PlayerEffect(EffectBadOmen)
	if !ok {
		return false, 0, 0, 0
	}
	cx, cy, cz, _, _, found := s.FindVillageCenterNear(snap.Dimension, snap.X, snap.Y, snap.Z, VillageDetectionRadius)
	if !found {
		return false, 0, 0, 0
	}

	amp := clampBadOmenAmplifier(badOmen.Amplifier)
	s.RemovePlayerEffect(session, EffectBadOmen)
	raidOmen := NewStatusEffectInstance(EffectRaidOmen, RaidOmenDurationTicks, amp)
	s.ApplyPlayerEffect(session, raidOmen)

	rm := s.raidManager()
	rm.mu.Lock()
	rm.omenCenters[session.Profile.UUID] = raidOmenPending{
		Dimension: world.NormalizeDimension(snap.Dimension),
		CenterX:   cx,
		CenterY:   cy,
		CenterZ:   cz,
		Amplifier: amp,
	}
	rm.mu.Unlock()

	chunkX := int32(int64(math.Floor(snap.X)) >> 4)
	chunkZ := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastChunk(chunkX, chunkZ, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:event.mob_effect.raid_omen", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))

	return true, cx, cy, cz
}

// TriggerRaidFromBadOmen starts or upgrades a village raid for session if
// session has Bad Omen or Raid Omen and is near a village. Both Bad Omen and
// Raid Omen are consumed when the raid starts/absorbs the omen.
func (s *Server) TriggerRaidFromBadOmen(session *PlayerSession) (*Raid, bool) {
	if s == nil || session == nil || session.IsDead() {
		return nil, false
	}
	if s.Difficulty() == DifficultyPeaceful || s.GameRuleBool("disableRaids") {
		return nil, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return nil, false
	}

	var amp int32
	hasOmen := false
	if inst, ok := session.PlayerEffect(EffectRaidOmen); ok {
		amp = inst.Amplifier
		hasOmen = true
	} else if inst, ok := session.PlayerEffect(EffectBadOmen); ok {
		amp = inst.Amplifier
		hasOmen = true
	}
	if !hasOmen {
		return nil, false
	}

	cx, cy, cz, _, _, found := s.FindVillageCenterNear(snap.Dimension, snap.X, snap.Y, snap.Z, VillageDetectionRadius)
	if !found {
		// Check if a pending raid omen center was already recorded for this player.
		rm := s.raidManager()
		rm.mu.Lock()
		pending, ok := rm.omenCenters[session.Profile.UUID]
		rm.mu.Unlock()
		if !ok || pending.Dimension != world.NormalizeDimension(snap.Dimension) {
			return nil, false
		}
		cx, cy, cz = pending.CenterX, pending.CenterY, pending.CenterZ
	}

	s.RemovePlayerEffect(session, EffectBadOmen)
	s.RemovePlayerEffect(session, EffectRaidOmen)

	rm := s.raidManager()
	rm.mu.Lock()
	delete(rm.omenCenters, session.Profile.UUID)
	rm.mu.Unlock()

	raid := s.StartOrAbsorbRaid(snap.Dimension, cx, cy, cz, int(clampBadOmenAmplifier(amp))+1, session)
	if raid == nil {
		return nil, false
	}
	return raid, true
}

// StartOrAbsorbRaid creates a new Raid at (centerX, centerY, centerZ) in dim or
// merges the incoming omenLevel (1..5) into an existing ongoing raid within
// DefaultRaidRadius (96 blocks), initializing and synchronizing the Raid Boss Bar.
func (s *Server) StartOrAbsorbRaid(dim string, centerX, centerY, centerZ float64, omenLevel int, starter *PlayerSession) *Raid {
	if s == nil || s.Difficulty() == DifficultyPeaceful || s.GameRuleBool("disableRaids") {
		return nil
	}
	if omenLevel < 1 {
		omenLevel = 1
	}
	if omenLevel > MaxBadOmenLevel {
		omenLevel = MaxBadOmenLevel
	}
	normDim := world.NormalizeDimension(dim)
	diff := s.Difficulty()

	rm := s.raidManager()
	rm.mu.Lock()

	// Check for an existing ongoing raid within DefaultRaidRadius.
	maxDistSq := DefaultRaidRadius * DefaultRaidRadius
	var target *Raid
	for _, r := range rm.raids {
		if r == nil || r.Status != RaidStatusOngoing || world.NormalizeDimension(r.Dimension) != normDim {
			continue
		}
		dx := r.CenterX - centerX
		dy := r.CenterY - centerY
		dz := r.CenterZ - centerZ
		if dx*dx+dy*dy+dz*dz <= maxDistSq {
			target = r
			break
		}
	}

	isNew := false
	if target == nil {
		rm.nextRaidID++
		raidID := rm.nextRaidID
		totalWaves := TotalRaidWaveCount(diff, omenLevel)
		target = &Raid{
			ID:            raidID,
			Dimension:     normDim,
			CenterX:       centerX,
			CenterY:       centerY,
			CenterZ:       centerZ,
			Status:        RaidStatusOngoing,
			BadOmenLevel:  omenLevel,
			GroupsSpawned: 0,
			TotalWaves:    totalWaves,
			TicksActive:   0,
			PreWaveTicks:  RaidPreWaveWarmupTicks,
			Raiders:       make(map[int32]*RaiderState),
			Heroes:        make(map[[16]byte]bool),
		}
		rm.raids[raidID] = target
		isNew = true
	} else {
		newLevel := target.BadOmenLevel + omenLevel
		if newLevel > MaxBadOmenLevel {
			newLevel = MaxBadOmenLevel
		}
		target.BadOmenLevel = newLevel
		target.TotalWaves = TotalRaidWaveCount(diff, target.BadOmenLevel)
	}

	if starter != nil {
		target.Heroes[starter.Profile.UUID] = true
	}

	needBossBar := !target.HasBossBar
	raidID := target.ID
	rm.mu.Unlock()

	if needBossBar {
		bar := s.CreateRuntimeBossBar(
			TextComponent("Raid"),
			BossBarColorRed,
			BossBarOverlayNotched10,
			false,
			false,
			false,
		)
		if bar != nil {
			s.UpdateRuntimeBossBarProgress(bar.UUID, 0.0)
			rm.mu.Lock()
			if live, ok := rm.raids[raidID]; ok && live != nil {
				live.BossBarUUID = bar.UUID
				live.HasBossBar = true
			}
			rm.mu.Unlock()
		}
	}

	if isNew {
		cx := int32(int64(math.Floor(centerX)) >> 4)
		cz := int32(int64(math.Floor(centerZ)) >> 4)
		s.BroadcastChunk(cx, cz, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:event.raid.horn", SoundSourceHostile, centerX, centerY, centerZ, 16.0, 1.0, 0))
	}

	s.syncRaidBossBar(raidID)
	snapRaid, _ := s.RaidByID(raidID)
	return snapRaid
}

// --- Raid Wave Spawning & Boss Bar Synchronization ---

// SpawnNextRaidWave immediately spawns the next wave of raiders for raidID,
// updating GroupsSpawned, TotalWaveHealth, and the Raid Boss Bar.
func (s *Server) SpawnNextRaidWave(raidID int32) (spawnedIDs []int32, err error) {
	if s == nil {
		return nil, errors.New("server: nil server")
	}
	diff := s.Difficulty()
	if diff == DifficultyPeaceful {
		return nil, errors.New("server: cannot spawn raid wave in peaceful difficulty")
	}

	rm := s.raidManager()
	rm.mu.Lock()
	r, ok := rm.raids[raidID]
	if !ok || r == nil {
		rm.mu.Unlock()
		return nil, errors.New("server: raid not found")
	}
	if r.Status != RaidStatusOngoing {
		rm.mu.Unlock()
		return nil, errors.New("server: raid is not ongoing")
	}
	nextWave := r.GroupsSpawned + 1
	baseWaves := RaidWaveCountForDifficulty(diff)
	isBonus := nextWave > baseWaves
	members := RollRaidWaveMembers(nextWave, diff, isBonus, rm.rng)
	centerX, centerY, centerZ := r.CenterX, r.CenterY, r.CenterZ
	baseAngle := 0.0
	if rm.rng != nil {
		baseAngle = rm.rng.Float64() * 2.0 * math.Pi
	}
	rm.mu.Unlock()

	if len(members) == 0 {
		return nil, errors.New("server: empty wave member list")
	}

	// Pick a wave spawn origin on the perimeter around the village center.
	spawnRadius := RaidWaveSpawnMinRadius
	originX := centerX + math.Cos(baseAngle)*spawnRadius
	originZ := centerZ + math.Sin(baseAngle)*spawnRadius
	originY := s.findRaidSpawnY(int(math.Floor(originX)), int(math.Floor(centerY)), int(math.Floor(originZ)))

	var totalHP float32
	for i, m := range members {
		offsetX := float64((i%3)-1) * 1.5
		offsetZ := float64((i/3)-1) * 1.5
		sx := originX + offsetX
		sz := originZ + offsetZ
		sy := s.findRaidSpawnY(int(math.Floor(sx)), int(math.Floor(originY)), int(math.Floor(sz)))

		id, spawnErr := s.SpawnRaiderMob(sx, sy, sz, m.TypeName, m.IsCaptain, raidID, nextWave)
		if spawnErr != nil {
			continue
		}
		spawnedIDs = append(spawnedIDs, id)
		totalHP += RaiderMaxHealth(m.TypeName)
	}

	rm.mu.Lock()
	if live, exists := rm.raids[raidID]; exists && live != nil {
		live.GroupsSpawned = nextWave
		live.PreWaveTicks = 0
		live.TotalWaveHealth = totalHP
	}
	rm.mu.Unlock()

	cx := int32(int64(math.Floor(originX)) >> 4)
	cz := int32(int64(math.Floor(originZ)) >> 4)
	s.BroadcastChunk(cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:event.raid.horn", SoundSourceHostile, originX, originY, originZ, 16.0, 1.0, 0))

	s.syncRaidBossBar(raidID)
	return spawnedIDs, nil
}

func (s *Server) findRaidSpawnY(bx, refY, bz int) float64 {
	if s == nil || s.chunks == nil {
		return float64(refY)
	}
	if ny, ok := findWalkableColumnNearY(s.chunks, bx, refY, bz); ok {
		return float64(ny)
	}
	return float64(refY)
}

func raidBossBarTitle(status RaidStatus, aliveRaiders int, preWaveTicks int32) ChatComponent {
	switch status {
	case RaidStatusVictory:
		return TextComponent("Raid - Victory")
	case RaidStatusDefeat:
		return TextComponent("Raid - Defeat")
	default:
		if preWaveTicks <= 0 && aliveRaiders > 0 && aliveRaiders <= 2 {
			return TextComponent(fmt.Sprintf("Raid - Raiders remaining: %d", aliveRaiders))
		}
		return TextComponent("Raid")
	}
}

func (s *Server) syncRaidBossBar(raidID int32) {
	if s == nil {
		return
	}
	rm := s.raidManager()
	rm.mu.Lock()
	r, ok := rm.raids[raidID]
	if !ok || r == nil || !r.HasBossBar {
		rm.mu.Unlock()
		return
	}

	barUUID := r.BossBarUUID
	dim := r.Dimension
	cx, cy, cz := r.CenterX, r.CenterY, r.CenterZ
	status := r.Status
	preWave := r.PreWaveTicks
	totalWaveHP := r.TotalWaveHealth

	var aliveCount int
	var sumHP float32
	var staleIDs []int32
	for entityID, rs := range r.Raiders {
		if s.entities == nil || s.entities.Get(entityID) == nil {
			staleIDs = append(staleIDs, entityID)
			continue
		}
		hp := s.MobHealth(entityID)
		if hp <= 0 {
			staleIDs = append(staleIDs, entityID)
			continue
		}
		aliveCount++
		sumHP += hp
		_ = rs
	}
	for _, id := range staleIDs {
		delete(r.Raiders, id)
		delete(rm.raiderIndex, id)
	}
	rm.mu.Unlock()

	var progress float32
	switch status {
	case RaidStatusVictory:
		progress = 1.0
	case RaidStatusDefeat:
		progress = 0.0
	default:
		if preWave > 0 {
			progress = 1.0 - float32(preWave)/float32(RaidPreWaveWarmupTicks)
		} else if totalWaveHP > 0 {
			progress = sumHP / totalWaveHP
		} else {
			progress = 0.0
		}
	}
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}

	title := raidBossBarTitle(status, aliveCount, preWave)
	s.UpdateRuntimeBossBarName(barUUID, title)
	s.UpdateRuntimeBossBarProgress(barUUID, progress)
	s.SyncRuntimeBossBarPlayersInRange(barUUID, dim, cx, cy, cz, DefaultRaidRadius)
}

// --- Raider Death, Victory & Defeat Handling ---

// OnRaiderKilled handles the death of a raider or patrol captain with entityID,
// optionally killed by killer:
//   - If the entity was a Raid Captain, spawns ominous_bottle + white_banner drops
//     and grants "minecraft:adventure/voluntary_exile" to killer.
//   - If the entity belonged to an active Raid, records killer as a raid hero,
//     removes the raider from the wave, updates the Raid Boss Bar, and triggers
//     next wave warmup or Victory if the wave was cleared.
func (s *Server) OnRaiderKilled(entityID int32, killer *PlayerSession) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	var entPos [3]float64
	entDim := world.DimensionOverworld
	var entTypeName string
	hasPos := false
	if s.entities != nil {
		if ent := s.entities.Get(entityID); ent != nil {
			entPos = [3]float64{ent.X, ent.Y, ent.Z}
			entDim = ent.EffectiveDimension()
			entTypeName = canonicalResourceName(ent.TypeName)
			hasPos = true
			// Ensure entity is removed if not already removed by combat.go.
			cp := ent.ChunkPos()
			s.entities.Remove(entityID)
			s.BroadcastDimensionChunk(entDim, cp.X, cp.Z, protocol.PlayRemoveEntities, encodeRemoveEntitiesPacket(nil, []int32{entityID}))
		}
	} else if killer != nil {
		snap := killer.Snapshot()
		entPos = [3]float64{snap.X, snap.Y, snap.Z}
		entDim = world.NormalizeDimension(snap.Dimension)
		hasPos = true
	}

	illagerCap := s.IsIllagerCaptain(entityID)

	rm := s.raidManager()
	rm.mu.Lock()
	rs, tracked := rm.raiderIndex[entityID]
	if (!tracked || rs == nil) && !illagerCap && !IsRaiderMobType(entTypeName) {
		rm.mu.Unlock()
		return false
	}
	delete(rm.raiderIndex, entityID)

	var raidID int32
	isCaptain := illagerCap
	var omenAmp int32
	typeName := entTypeName
	if tracked && rs != nil {
		raidID = rs.RaidID
		if rs.IsCaptain {
			isCaptain = true
		}
		omenAmp = rs.BadOmenAmplifier
		if rs.TypeName != "" {
			typeName = rs.TypeName
		}
	}
	if typeName == "" {
		typeName = MobTypePillager
	}
	directGrant := rm.grantBadOmenOnCaptainKill

	if raidID > 0 {
		if r, ok := rm.raids[raidID]; ok && r != nil {
			delete(r.Raiders, entityID)
			if killer != nil {
				r.Heroes[killer.Profile.UUID] = true
			}
		}
	}
	rm.mu.Unlock()

	if hasPos {
		drops := RaidLootDropsForRaider(typeName, isCaptain, omenAmp, rm.rng)
		for _, drop := range drops {
			dropID, _ := s.SpawnItemEntityInDimension(entDim, entPos[0], entPos[1]+0.25, entPos[2], drop)
			if isCaptain && IsOminousBottleItem(drop) && dropID > 0 {
				s.AssociateDroppedOminousBottleAmplifier(dropID, omenAmp)
			}
		}
	}

	if killer != nil {
		s.TriggerEntityKilled(killer, typeName)
		if isCaptain {
			s.TriggerVoluntaryExile(killer)
			if directGrant {
				s.GrantBadOmenFromCaptainKill(killer, omenAmp)
			}
		}
	}

	if raidID > 0 {
		s.evaluateRaidStateAfterWaveChange(raidID)
	}
	return true
}

func (s *Server) evaluateRaidStateAfterWaveChange(raidID int32) {
	if s == nil {
		return
	}
	rm := s.raidManager()
	rm.mu.Lock()
	r, ok := rm.raids[raidID]
	if !ok || r == nil || r.Status != RaidStatusOngoing {
		rm.mu.Unlock()
		return
	}

	// Prune any raiders whose entities have died or been removed via AttackEntity.
	for eid := range r.Raiders {
		if s.entities == nil || s.entities.Get(eid) == nil || s.MobHealth(eid) <= 0 {
			delete(r.Raiders, eid)
			delete(rm.raiderIndex, eid)
		}
	}

	alive := len(r.Raiders)
	var becameVictory bool
	var heroes map[[16]byte]bool
	var omenLevel int
	var dim string
	var cx, cy, cz float64

	if alive == 0 && r.PreWaveTicks <= 0 && r.GroupsSpawned > 0 {
		if r.GroupsSpawned < r.TotalWaves {
			r.PreWaveTicks = RaidPreWaveWarmupTicks
		} else {
			r.Status = RaidStatusVictory
			r.PostEndTicks = 0
			becameVictory = true
			omenLevel = r.BadOmenLevel
			dim = r.Dimension
			cx, cy, cz = r.CenterX, r.CenterY, r.CenterZ
			heroes = make(map[[16]byte]bool, len(r.Heroes))
			for u, v := range r.Heroes {
				heroes[u] = v
			}
		}
	}
	rm.mu.Unlock()

	s.syncRaidBossBar(raidID)

	if becameVictory {
		s.grantRaidVictoryRewards(dim, cx, cy, cz, omenLevel, heroes)
	}
}

func (s *Server) grantRaidVictoryRewards(dim string, cx, cy, cz float64, badOmenLevel int, heroes map[[16]byte]bool) {
	if s == nil {
		return
	}
	amp := int32(badOmenLevel - 1)
	if amp < 0 {
		amp = 0
	}
	if amp > MaxBadOmenAmplifier {
		amp = MaxBadOmenAmplifier
	}
	normDim := world.NormalizeDimension(dim)
	rSq := DefaultRaidRadius * DefaultRaidRadius

	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		snap := p.Snapshot()
		inRadius := false
		if world.NormalizeDimension(snap.Dimension) == normDim {
			dx := snap.X - cx
			dy := snap.Y - cy
			dz := snap.Z - cz
			if dx*dx+dy*dy+dz*dz <= rSq {
				inRadius = true
			}
		}
		if !heroes[p.Profile.UUID] && !inRadius {
			continue
		}
		eff := NewStatusEffectInstance(EffectHeroOfTheVillage, HeroOfTheVillageDurationTicks, amp)
		s.ApplyPlayerEffect(p, eff)
		s.TriggerHeroOfTheVillage(p)
	}

	// Villagers in the village celebrate victory with happy particles, celebration sounds, and fireworks.
	chunkX := int32(int64(math.Floor(cx)) >> 4)
	chunkZ := int32(int64(math.Floor(cz)) >> 4)
	s.BroadcastDimensionChunk(normDim, chunkX, chunkZ, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.firework_rocket.launch", SoundSourceNeutral, cx, cy+1.0, cz, 3.0, 1.0, 0))
	s.BroadcastDimensionChunk(normDim, chunkX, chunkZ, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.firework_rocket.blast", SoundSourceNeutral, cx, cy+6.0, cz, 3.0, 1.0, 0))

	if s.entities != nil {
		for _, ent := range s.entities.AppendAll(nil) {
			if ent.TypeName != MobTypeVillager || ent.EffectiveDimension() != normDim {
				continue
			}
			dx := ent.X - cx
			dy := ent.Y - cy
			dz := ent.Z - cz
			if dx*dx+dy*dy+dz*dz <= rSq {
				cp := ent.ChunkPos()
				s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayEntityEvent,
					EncodeEntityEventPacket(ent.ID, EntityEventVillagerHappy))
				s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket("minecraft:entity.villager.celebrate", SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			}
		}
	}
}

func (s *Server) celebrateRaidDefeatRaiders(raidID int32) {
	if s == nil || s.entities == nil {
		return
	}
	rm := s.raidManager()
	rm.mu.Lock()
	r, ok := rm.raids[raidID]
	if !ok || r == nil {
		rm.mu.Unlock()
		return
	}
	ids := make([]int32, 0, len(r.Raiders))
	for eid := range r.Raiders {
		ids = append(ids, eid)
	}
	rm.mu.Unlock()

	for _, eid := range ids {
		s.SetRaiderCelebrating(eid, true)
		if ent := s.entities.Get(eid); ent != nil {
			snd := SoundPillagerCelebrate
			switch canonicalResourceName(ent.TypeName) {
			case MobTypeVindicator:
				snd = SoundVindicatorCelebrate
			case MobTypeEvoker:
				snd = SoundEvokerCelebrate
			case MobTypeWitch:
				snd = SoundWitchCelebrate
			case MobTypeRavager:
				snd = SoundRavagerCelebrate
			}
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(snd, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		}
	}
}

func (s *Server) stepHeroOfTheVillageGifts() {
	if s == nil || s.entities == nil {
		return
	}
	var heroes []*PlayerSession
	for _, p := range s.onlinePlayersSnapshot() {
		if p != nil && !p.IsDead() && p.HasPlayerEffect(EffectHeroOfTheVillage) {
			heroes = append(heroes, p)
		}
	}
	if len(heroes) == 0 {
		return
	}
	rm := s.raidManager()
	rm.mu.Lock()
	rng := rm.rng
	rm.mu.Unlock()

	vp := s.villagerPolish()
	for _, ent := range s.entities.AppendAll(nil) {
		if ent.TypeName != MobTypeVillager {
			continue
		}
		vp.mu.Lock()
		ve := vp.ensureVillagerLocked(ent.ID)
		if ve.GiftCooldown > 0 {
			ve.GiftCooldown--
		}
		vp.mu.Unlock()

		for _, hero := range heroes {
			if _, ok := s.TryThrowHeroOfTheVillageGift(ent.ID, hero, rng); ok {
				cp := ent.ChunkPos()
				dim := ent.EffectiveDimension()
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
					EncodeEntityEventPacket(ent.ID, EntityEventVillagerHappy))
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket("minecraft:entity.villager.celebrate", SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
				break
			}
		}
	}
}

// StopRaid stops and removes the raid with raidID, removing its runtime boss bar.
func (s *Server) StopRaid(raidID int32) bool {
	if s == nil {
		return false
	}
	rm := s.raidManager()
	rm.mu.Lock()
	r, ok := rm.raids[raidID]
	if !ok || r == nil {
		rm.mu.Unlock()
		return false
	}
	r.Status = RaidStatusStopped
	hasBar := r.HasBossBar
	barUUID := r.BossBarUUID
	for eid := range r.Raiders {
		delete(rm.raiderIndex, eid)
	}
	delete(rm.raids, raidID)
	rm.mu.Unlock()

	if hasBar {
		s.RemoveRuntimeBossBar(barUUID)
	}
	return true
}

// --- Server Raid Tick (Omen Conversion, Waves, Raider AI, Boss Bar, Victory/Defeat) ---

// TickRaids advances the server's raid system by one game tick:
//  1. Checks online players for Bad Omen inside villages (converting to Raid Omen)
//     and triggers/absorbs a raid when Raid Omen finishes its countdown.
//  2. Steps active Pillager Patrols and Hero of the Village gift throwing.
//  3. Updates all active raids: checks Peaceful/disableRaids stop, village loss
//     (Defeat), pre-wave warmup -> wave spawn, raider health/death reconciliation,
//     raider movement & combat AI, wave completion -> next wave or Victory, and
//     post-raid cleanup.
func (s *Server) TickRaids() {
	if s == nil {
		return
	}

	if s.Difficulty() == DifficultyPeaceful || s.GameRuleBool("disableRaids") {
		for _, r := range s.ActiveRaids() {
			s.StopRaid(r.ID)
		}
		return
	}

	s.StepIllagerPatrols()
	s.stepHeroOfTheVillageGifts()

	// 1. Check online players for Bad Omen -> Raid Omen and expiring Raid Omen -> Raid start.
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		if p.HasPlayerEffect(EffectBadOmen) {
			s.CheckPlayerBadOmenInVillage(p)
		}
		if inst, ok := p.PlayerEffect(EffectRaidOmen); ok && !inst.IsInfinite() && inst.Duration <= 2 {
			s.TriggerRaidFromBadOmen(p)
		}
	}

	// 2. Snapshot raid IDs to tick.
	rm := s.raidManager()
	rm.mu.Lock()
	if len(rm.raids) == 0 {
		rm.mu.Unlock()
		return
	}
	raidIDs := make([]int32, 0, len(rm.raids))
	for id := range rm.raids {
		raidIDs = append(raidIDs, id)
	}
	rm.mu.Unlock()
	sort.Slice(raidIDs, func(i, j int) bool { return raidIDs[i] < raidIDs[j] })

	for _, id := range raidIDs {
		s.tickSingleRaid(id)
	}
}

func (s *Server) tickSingleRaid(raidID int32) {
	rm := s.raidManager()
	rm.mu.Lock()
	r, ok := rm.raids[raidID]
	if !ok || r == nil {
		rm.mu.Unlock()
		return
	}

	r.TicksActive++
	if r.TicksActive >= RaidMaxActiveTicks && r.Status == RaidStatusOngoing {
		rm.mu.Unlock()
		s.StopRaid(raidID)
		return
	}

	if r.Status == RaidStatusVictory || r.Status == RaidStatusDefeat {
		r.PostEndTicks++
		done := r.PostEndTicks >= RaidPostEndCelebrationTicks
		rm.mu.Unlock()
		if done {
			s.StopRaid(raidID)
		} else {
			s.syncRaidBossBar(raidID)
		}
		return
	}

	if r.Status != RaidStatusOngoing {
		rm.mu.Unlock()
		s.StopRaid(raidID)
		return
	}

	dim := r.Dimension
	cx, cy, cz := r.CenterX, r.CenterY, r.CenterZ
	preWave := r.PreWaveTicks
	groupsSpawned := r.GroupsSpawned
	rm.mu.Unlock()

	// Check if the village still has at least one living villager or bed.
	if groupsSpawned > 0 && !s.HasVillagePresenceAt(dim, cx, cy, cz, DefaultRaidRadius) {
		becameDefeat := false
		rm.mu.Lock()
		if live, exists := rm.raids[raidID]; exists && live != nil && live.Status == RaidStatusOngoing {
			live.Status = RaidStatusDefeat
			live.PostEndTicks = 0
			becameDefeat = true
		}
		rm.mu.Unlock()
		if becameDefeat {
			s.celebrateRaidDefeatRaiders(raidID)
		}
		s.syncRaidBossBar(raidID)
		return
	}

	// Advance pre-wave warmup timer and spawn wave when it reaches 0.
	if preWave > 0 {
		shouldSpawn := false
		rm.mu.Lock()
		if live, exists := rm.raids[raidID]; exists && live != nil && live.Status == RaidStatusOngoing {
			live.PreWaveTicks--
			if live.PreWaveTicks <= 0 {
				live.PreWaveTicks = 0
				shouldSpawn = true
			}
		}
		rm.mu.Unlock()

		if shouldSpawn {
			_, _ = s.SpawnNextRaidWave(raidID)
		} else {
			s.syncRaidBossBar(raidID)
		}
		return
	}

	// Step active raiders and reconcile any killed raiders.
	s.stepRaidRaidersAI(raidID, dim, cx, cy, cz)
	s.evaluateRaidStateAfterWaveChange(raidID)
}

func (s *Server) stepRaidRaidersAI(raidID int32, dim string, centerX, centerY, centerZ float64) {
	if s == nil || s.entities == nil {
		return
	}
	rm := s.raidManager()
	rm.mu.Lock()
	r, ok := rm.raids[raidID]
	if !ok || r == nil || len(r.Raiders) == 0 {
		rm.mu.Unlock()
		return
	}
	hasAliveCaptain := false
	raiderList := make([]*RaiderState, 0, len(r.Raiders))
	for _, rs := range r.Raiders {
		if rs != nil {
			if rs.IsCaptain {
				hasAliveCaptain = true
			}
			cp := *rs
			raiderList = append(raiderList, &cp)
		}
	}
	rm.mu.Unlock()

	targets := s.appendPlayerTargets(nil)
	tr := s.entityTracker()

	for _, rs := range raiderList {
		ent := s.entities.Get(rs.EntityID)
		if ent == nil {
			continue
		}
		hp := s.MobHealth(rs.EntityID)
		if hp <= 0 {
			continue
		}

		// If the raid wave's captain died and dropped an Ominous Banner, allow a
		// surviving illager to pick up the banner and become the new wave captain.
		if !hasAliveCaptain && !rs.IsCaptain && s.TryPromoteRaiderFromDroppedBanner(rs.EntityID) {
			s.MarkRaidCaptain(rs.EntityID, 0)
			hasAliveCaptain = true
		}

		// Determine goal: nearest survival player within RaiderAggroRange, or village center.
		goalX, goalY, goalZ := centerX, centerY, centerZ
		var attackPlayer *PlayerSession
		if pt, found := selectTargetWithinRange(targets, ent.X, ent.Y, ent.Z, RaiderAggroRange); found {
			goalX, goalY, goalZ = pt.x, pt.y, pt.z
			attackPlayer = pt.session
		}

		feetY := int(math.Floor(ent.Y + 0.01))
		stopDist := 1.4
		if rs.TypeName == MobTypePillager || rs.TypeName == MobTypeEvoker || rs.TypeName == MobTypeWitch {
			stopDist = 6.0
		}

		tr.mu.Lock()
		mem := tr.mobStateFor(rs.EntityID, tr.aiTickID)
		if mem.attackCooldown > 0 {
			mem.attackCooldown--
		}
		nx, ny, nz, nyaw := stepHostileChaseToward(
			s.chunks, mem, ent.X, feetY, ent.Z, ent.Yaw,
			goalX, goalY, goalZ, RaiderChaseSpeed, stopDist,
		)
		canAttack := mem.attackCooldown <= 0
		if canAttack && attackPlayer != nil {
			dist := math.Sqrt((goalX-nx)*(goalX-nx) + (goalY-ny)*(goalY-ny) + (goalZ-nz)*(goalZ-nz))
			reach := RaiderMeleeReach
			if rs.TypeName == MobTypePillager || rs.TypeName == MobTypeEvoker || rs.TypeName == MobTypeWitch {
				reach = RaiderRangedReach
			}
			if dist <= reach {
				mem.attackCooldown = RaiderAttackCooldownTicks
			} else {
				canAttack = false
			}
		} else {
			canAttack = false
		}
		tr.mu.Unlock()

		if nx != ent.X || ny != ent.Y || nz != ent.Z || nyaw != ent.Yaw {
			s.entities.Update(rs.EntityID, func(e *world.Entity) {
				e.X = nx
				e.Y = ny
				e.Z = nz
				e.Yaw = nyaw
			})
		}

		if canAttack && attackPlayer != nil {
			s.AttackPlayerFromMob(rs.EntityID, rs.TypeName, nx, nz, attackPlayer, RaiderBaseAttackDamage(rs.TypeName))
		}
	}
}

// --- Hero of the Village Trade Discounts & Gifts ---

// HeroOfTheVillageDiscountRatio returns the vanilla trade price discount ratio
// for a given 0-based Hero of the Village amplifier (0..4):
//   - Level I (amp 0):   0.30   (30%)
//   - Level II (amp 1):  0.3625 (36.25%)
//   - Level III (amp 2): 0.425  (42.5%)
//   - Level IV (amp 3):  0.4875 (48.75%)
//   - Level V (amp 4):   0.55   (55%)
func HeroOfTheVillageDiscountRatio(amplifier int32) float64 {
	if amplifier < 0 {
		return 0
	}
	if amplifier > MaxBadOmenAmplifier {
		amplifier = MaxBadOmenAmplifier
	}
	return 0.3 + 0.0625*float64(amplifier)
}

// HeroOfTheVillagePriceDiff computes the negative SpecialPriceDiff applied to a
// villager offer with baseCost when trading with a player who has Hero of the
// Village at the given 0-based amplifier (matching Villager.updateSpecialPrices).
func HeroOfTheVillagePriceDiff(baseCost int32, amplifier int32) int32 {
	if baseCost <= 0 || amplifier < 0 {
		return 0
	}
	ratio := HeroOfTheVillageDiscountRatio(amplifier)
	discount := int32(math.Floor(ratio * float64(baseCost)))
	if discount < 1 {
		discount = 1
	}
	return -discount
}

// ApplyHeroOfTheVillageToOffers returns a copy of offers with SpecialPriceDiff
// reduced according to session's active minecraft:hero_of_the_village effect (if any).
func ApplyHeroOfTheVillageToOffers(session *PlayerSession, offers []MerchantOffer) []MerchantOffer {
	if len(offers) == 0 {
		return nil
	}
	out := make([]MerchantOffer, len(offers))
	copy(out, offers)
	if session == nil {
		return out
	}
	inst, ok := session.PlayerEffect(EffectHeroOfTheVillage)
	if !ok {
		return out
	}
	for i := range out {
		out[i].SpecialPriceDiff += HeroOfTheVillagePriceDiff(out[i].BaseCostA.Count, inst.Amplifier)
	}
	return out
}

// ApplyHeroOfTheVillageToVillager applies session's Hero of the Village trade
// discount to the villager with villagerEntityID and resends
// ClientboundMerchantOffersPacket if session has that villager's merchant menu open.
func (s *Server) ApplyHeroOfTheVillageToVillager(session *PlayerSession, villagerEntityID int32) bool {
	if s == nil || session == nil {
		return false
	}
	inst, ok := session.PlayerEffect(EffectHeroOfTheVillage)
	if !ok {
		return false
	}
	vs, ok := s.VillagerState(villagerEntityID)
	if !ok || len(vs.Offers) == 0 {
		return false
	}
	for i := range vs.Offers {
		vs.Offers[i].SpecialPriceDiff = HeroOfTheVillagePriceDiff(vs.Offers[i].BaseCostA.Count, inst.Amplifier)
	}
	if !s.SetVillagerState(villagerEntityID, vs) {
		return false
	}
	s.syncMerchantOffersToViewers(villagerEntityID, vs.Offers, int32(vs.Level), vs.XP)
	return true
}

// RollHeroOfTheVillageGift returns a profession-specific gift ItemStack that a
// villager throws to a player with Hero of the Village (matching vanilla
// GiveGiftToHero loot tables).
func RollHeroOfTheVillageGift(profession string, rng *rand.Rand) world.ItemStack {
	var pool []string
	switch canonicalResourceName(profession) {
	case VillagerProfessionArmorer:
		pool = []string{"minecraft:chainmail_helmet", "minecraft:chainmail_chestplate", "minecraft:chainmail_leggings", "minecraft:chainmail_boots"}
	case VillagerProfessionButcher:
		pool = []string{"minecraft:cooked_porkchop", "minecraft:cooked_chicken", "minecraft:cooked_beef", "minecraft:cooked_mutton"}
	case VillagerProfessionCartographer:
		pool = []string{"minecraft:paper", "minecraft:map"}
	case VillagerProfessionCleric:
		pool = []string{"minecraft:redstone", "minecraft:lapis_lazuli"}
	case VillagerProfessionFarmer:
		pool = []string{"minecraft:bread", "minecraft:pumpkin_pie", "minecraft:cookie"}
	case VillagerProfessionFisherman:
		pool = []string{"minecraft:cod", "minecraft:salmon"}
	case VillagerProfessionFletcher:
		pool = []string{"minecraft:arrow"}
	case VillagerProfessionLeatherworker:
		pool = []string{"minecraft:leather"}
	case VillagerProfessionLibrarian:
		pool = []string{"minecraft:book"}
	case VillagerProfessionMason:
		pool = []string{"minecraft:clay"}
	case VillagerProfessionShepherd:
		pool = []string{"minecraft:white_wool"}
	case VillagerProfessionToolsmith:
		pool = []string{"minecraft:stone_pickaxe", "minecraft:stone_shovel", "minecraft:stone_hoe"}
	case VillagerProfessionWeaponsmith:
		pool = []string{"minecraft:stone_axe", "minecraft:iron_axe", "minecraft:golden_axe"}
	default:
		pool = []string{"minecraft:wheat_seeds"}
	}
	pick := pool[0]
	if rng != nil && len(pool) > 1 {
		pick = pool[rng.Intn(len(pool))]
	}
	return world.NewNamedItemStack(pick, 1)
}

// --- Persistence: Raid NBT Serialization (<WorldDir>/region/raids.nbt) ---

// ToNBT serializes r into a vanilla-compatible Raid NBT compound.
func (r *Raid) ToNBT() *nbt.Compound {
	c := nbt.NewCompound()
	if r == nil {
		return c
	}
	c.Set("Id", nbt.Int(r.ID))
	c.Set("Dimension", nbt.String(world.NormalizeDimension(r.Dimension)))
	c.Set("CX", nbt.Int(int32(math.Floor(r.CenterX))))
	c.Set("CY", nbt.Int(int32(math.Floor(r.CenterY))))
	c.Set("CZ", nbt.Int(int32(math.Floor(r.CenterZ))))
	c.Set("CenterX", nbt.Double(r.CenterX))
	c.Set("CenterY", nbt.Double(r.CenterY))
	c.Set("CenterZ", nbt.Double(r.CenterZ))
	c.Set("Status", nbt.String(string(r.Status)))
	c.Set("BadOmenLevel", nbt.Int(int32(r.BadOmenLevel)))
	c.Set("GroupsSpawned", nbt.Int(int32(r.GroupsSpawned)))
	c.Set("NumGroups", nbt.Int(int32(r.TotalWaves)))
	c.Set("TicksActive", nbt.Long(r.TicksActive))
	c.Set("PreWaveTicks", nbt.Int(r.PreWaveTicks))
	c.Set("PostRaidTicks", nbt.Int(r.PostEndTicks))
	c.Set("TotalHealth", nbt.Float(r.TotalWaveHealth))

	uuids := make([][16]byte, 0, len(r.Heroes))
	for u, ok := range r.Heroes {
		if ok {
			uuids = append(uuids, u)
		}
	}
	sort.Slice(uuids, func(i, j int) bool {
		return string(uuids[i][:]) < string(uuids[j][:])
	})
	heroTags := make([]nbt.Tag, 0, len(uuids))
	for _, u := range uuids {
		heroTags = append(heroTags, uuidToNBTIntArray(u))
	}
	elemID := byte(nbt.TagIntArray)
	if len(heroTags) == 0 {
		elemID = nbt.TagEnd
	}
	c.Set("HeroesOfTheVillage", nbt.List{ElemID: elemID, Elems: heroTags})
	return c
}

// RaidFromNBT deserializes a Raid from an NBT compound produced by ToNBT.
func RaidFromNBT(c *nbt.Compound) (*Raid, error) {
	if c == nil {
		return nil, errors.New("server: nil raid NBT compound")
	}
	r := &Raid{
		Dimension:    world.DimensionOverworld,
		Status:       RaidStatusOngoing,
		BadOmenLevel: 1,
		TotalWaves:   5,
		Raiders:      make(map[int32]*RaiderState),
		Heroes:       make(map[[16]byte]bool),
	}
	if v, ok := c.Get("Id"); ok {
		if iv, ok := v.(nbt.Int); ok {
			r.ID = int32(iv)
		}
	}
	if v, ok := c.Get("Dimension"); ok {
		if sv, ok := v.(nbt.String); ok && string(sv) != "" {
			r.Dimension = world.NormalizeDimension(string(sv))
		}
	}
	if v, ok := c.Get("CX"); ok {
		if iv, ok := v.(nbt.Int); ok {
			r.CenterX = float64(iv) + 0.5
		}
	}
	if v, ok := c.Get("CY"); ok {
		if iv, ok := v.(nbt.Int); ok {
			r.CenterY = float64(iv)
		}
	}
	if v, ok := c.Get("CZ"); ok {
		if iv, ok := v.(nbt.Int); ok {
			r.CenterZ = float64(iv) + 0.5
		}
	}
	if v, ok := c.Get("CenterX"); ok {
		if dv, ok := v.(nbt.Double); ok {
			r.CenterX = float64(dv)
		}
	}
	if v, ok := c.Get("CenterY"); ok {
		if dv, ok := v.(nbt.Double); ok {
			r.CenterY = float64(dv)
		}
	}
	if v, ok := c.Get("CenterZ"); ok {
		if dv, ok := v.(nbt.Double); ok {
			r.CenterZ = float64(dv)
		}
	}
	if v, ok := c.Get("Status"); ok {
		if sv, ok := v.(nbt.String); ok && string(sv) != "" {
			r.Status = RaidStatus(sv)
		}
	}
	if v, ok := c.Get("BadOmenLevel"); ok {
		if iv, ok := v.(nbt.Int); ok {
			r.BadOmenLevel = int(iv)
		}
	}
	if v, ok := c.Get("GroupsSpawned"); ok {
		if iv, ok := v.(nbt.Int); ok {
			r.GroupsSpawned = int(iv)
		}
	}
	if v, ok := c.Get("NumGroups"); ok {
		if iv, ok := v.(nbt.Int); ok {
			r.TotalWaves = int(iv)
		}
	}
	if v, ok := c.Get("TicksActive"); ok {
		if lv, ok := v.(nbt.Long); ok {
			r.TicksActive = int64(lv)
		}
	}
	if v, ok := c.Get("PreWaveTicks"); ok {
		if iv, ok := v.(nbt.Int); ok {
			r.PreWaveTicks = int32(iv)
		}
	}
	if v, ok := c.Get("PostRaidTicks"); ok {
		if iv, ok := v.(nbt.Int); ok {
			r.PostEndTicks = int32(iv)
		}
	}
	if v, ok := c.Get("TotalHealth"); ok {
		if fv, ok := v.(nbt.Float); ok {
			r.TotalWaveHealth = float32(fv)
		}
	}
	if v, ok := c.Get("HeroesOfTheVillage"); ok {
		if list, ok := v.(nbt.List); ok {
			for _, elem := range list.Elems {
				if arr, ok := elem.(nbt.IntArray); ok {
					if u, valid := uuidFromNBTIntArray(arr); valid {
						r.Heroes[u] = true
					}
				}
			}
		}
	}
	return r, nil
}

// SaveRaids writes all active raids to <WorldDir>/region/raids.nbt.
func (s *Server) SaveRaids() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	rm := s.raidManager()
	rm.mu.Lock()
	nextID := rm.nextRaidID
	raids := make([]*Raid, 0, len(rm.raids))
	for _, r := range rm.raids {
		if r != nil {
			raids = append(raids, r.Clone())
		}
	}
	rm.mu.Unlock()

	sort.Slice(raids, func(i, j int) bool {
		return raids[i].ID < raids[j].ID
	})

	root := nbt.NewCompound()
	dataComp := nbt.NewCompound()
	dataComp.Set("NextAvailableID", nbt.Int(nextID))

	elems := make([]nbt.Tag, 0, len(raids))
	for _, r := range raids {
		elems = append(elems, r.ToNBT())
	}
	elemID := byte(nbt.TagCompound)
	if len(elems) == 0 {
		elemID = nbt.TagEnd
	}
	dataComp.Set("Raids", nbt.List{ElemID: elemID, Elems: elems})
	root.Set("data", dataComp)

	dir := filepath.Join(s.cfg.WorldDir, "region")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "raids.nbt")
	return os.WriteFile(path, nbt.MarshalNamed("", root), 0o644)
}

// LoadRaids loads saved raids from <WorldDir>/region/raids.nbt if present and
// recreates their runtime boss bars.
func (s *Server) LoadRaids() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	path := filepath.Join(s.cfg.WorldDir, "region", "raids.nbt")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	_, tag, err := nbt.UnmarshalNamed(raw)
	if err != nil {
		return err
	}
	root, ok := tag.(*nbt.Compound)
	if !ok {
		return errors.New("server: raids.nbt root is not a compound")
	}
	dataTag, ok := root.Get("data")
	if !ok {
		return nil
	}
	dataComp, ok := dataTag.(*nbt.Compound)
	if !ok {
		return nil
	}

	rm := s.raidManager()
	rm.mu.Lock()
	if v, ok := dataComp.Get("NextAvailableID"); ok {
		if iv, ok := v.(nbt.Int); ok && int32(iv) > rm.nextRaidID {
			rm.nextRaidID = int32(iv)
		}
	}
	var loadedIDs []int32
	if listTag, ok := dataComp.Get("Raids"); ok {
		if list, ok := listTag.(nbt.List); ok {
			for _, elem := range list.Elems {
				if rc, ok := elem.(*nbt.Compound); ok {
					r, err := RaidFromNBT(rc)
					if err != nil || r == nil || r.ID <= 0 {
						continue
					}
					if r.ID > rm.nextRaidID {
						rm.nextRaidID = r.ID
					}
					rm.raids[r.ID] = r
					loadedIDs = append(loadedIDs, r.ID)
				}
			}
		}
	}
	rm.mu.Unlock()

	for _, id := range loadedIDs {
		bar := s.CreateRuntimeBossBar(
			TextComponent("Raid"),
			BossBarColorRed,
			BossBarOverlayNotched10,
			false,
			false,
			false,
		)
		if bar != nil {
			rm.mu.Lock()
			if r, ok := rm.raids[id]; ok && r != nil {
				r.BossBarUUID = bar.UUID
				r.HasBossBar = true
			}
			rm.mu.Unlock()
			s.syncRaidBossBar(id)
		}
	}
	return nil
}
