package server

import (
	"errors"
	"math"
	"math/rand"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Fox, Panda, and Ocelot entity types, registry IDs, health values, gene ordinals,
// and SynchedEntityData constants for Minecraft 26.1.2 (protocol 775).
const (
	MobTypeFox    = "minecraft:fox"
	MobTypePanda  = "minecraft:panda"
	MobTypeOcelot = "minecraft:ocelot"

	// BuiltInRegistries.ENTITY_TYPE numeric network IDs in Minecraft 26.1.2 (protocol 775).
	EntityTypeFoxID    int = 54
	EntityTypeOcelotID int = 91
	EntityTypePandaID  int = 96

	// Default MaxHealth values in vanilla 26.1.2.
	DefaultFoxMaxHealth       float32 = 10.0
	DefaultPandaMaxHealth     float32 = 20.0
	DefaultWeakPandaMaxHealth float32 = 10.0 // Weak personality pandas have half max health (10.0 HP)
	DefaultOcelotMaxHealth    float32 = 10.0

	// Movement speeds.
	FoxWanderSpeed    = 0.10
	FoxFleeSpeed      = 0.18
	FoxHuntSpeed      = 0.16
	FoxPounceRange    = 4.0
	FoxPounceVY       = 0.42
	FoxBiteDamage     = float32(2.0)
	FoxFleePlayerDist = 12.0
	FoxEatHeldTicks   = 600

	PandaWanderSpeed          = 0.075
	PandaAttackDamage         = float32(6.0)
	PandaAttackReach          = 2.0
	PandaBambooRequiredBlocks = 8
	PandaBambooSearchRadius   = 5

	OcelotWanderSpeed = 0.09
	OcelotFleeSpeed   = 0.17

	// Fox variants.
	FoxVariantRed  int32 = 0
	FoxVariantSnow int32 = 1

	// SynchedEntityData indices for Fox in 26.1.2 (protocol 775).
	EntityDataFoxVariantID byte = 17 // Fox.DATA_TYPE_ID (INT serializer 1: 0=red, 1=snow)
	EntityDataFoxFlagsID   byte = 18 // Fox.DATA_FLAGS_ID (BYTE serializer 0)

	FoxFlagSitting     byte = 0x01
	FoxFlagCrouching   byte = 0x04
	FoxFlagInterested  byte = 0x08
	FoxFlagPouncing    byte = 0x10
	FoxFlagSleeping    byte = 0x20
	FoxFlagFaceplanted byte = 0x40
	FoxFlagDefending   byte = 0x80

	// SynchedEntityData indices for Panda in 26.1.2 (protocol 775).
	EntityDataPandaUnhappyCounterID byte = 17 // INT
	EntityDataPandaSneezeCounterID  byte = 18 // INT
	EntityDataPandaEatCounterID     byte = 19 // INT
	EntityDataPandaMainGeneID       byte = 20 // BYTE (0..6)
	EntityDataPandaHiddenGeneID     byte = 21 // BYTE (0..6)
	EntityDataPandaFlagsID          byte = 22 // BYTE (0x02 sneezing, 0x04 rolling, 0x08 sitting, 0x10 on_back)

	PandaFlagSneezing byte = 0x02
	PandaFlagRolling  byte = 0x04
	PandaFlagSitting  byte = 0x08
	PandaFlagOnBack   byte = 0x10

	// SynchedEntityData index for Ocelot in 26.1.2 (protocol 775).
	EntityDataOcelotTrustingID byte = 17 // Ocelot.DATA_TRUSTING (BOOLEAN serializer 8)

	// Canonical sounds for Fox, Panda, and Ocelot.
	SoundFoxAggro    = "minecraft:entity.fox.aggro"
	SoundFoxBite     = "minecraft:entity.fox.bite"
	SoundFoxEat      = "minecraft:entity.fox.eat"
	SoundFoxHurt     = "minecraft:entity.fox.hurt"
	SoundFoxDeath    = "minecraft:entity.fox.death"
	SoundFoxScreech  = "minecraft:entity.fox.screech"
	SoundFoxSleep    = "minecraft:entity.fox.sleep"
	SoundFoxSpit     = "minecraft:entity.fox.spit"
	SoundPandaEat    = "minecraft:entity.panda.eat"
	SoundPandaHurt   = "minecraft:entity.panda.hurt"
	SoundPandaDeath  = "minecraft:entity.panda.death"
	SoundPandaSneeze = "minecraft:entity.panda.sneeze"
	SoundPandaBite   = "minecraft:entity.panda.bite"
	SoundOcelotHurt  = "minecraft:entity.ocelot.hurt"
	SoundOcelotDeath = "minecraft:entity.ocelot.death"
)

// PandaGene represents a Panda gene/personality ordinal (0..6) in vanilla 26.1.2.
type PandaGene byte

const (
	PandaGeneNormal     PandaGene = 0 // dominant
	PandaGeneLazy       PandaGene = 1 // dominant
	PandaGeneWorried    PandaGene = 2 // dominant
	PandaGenePlayful    PandaGene = 3 // dominant
	PandaGeneBrown      PandaGene = 4 // recessive
	PandaGeneWeak       PandaGene = 5 // recessive (10 HP max health, higher baby sneeze rate)
	PandaGeneAggressive PandaGene = 6 // dominant (stays hostile until target dies or goes out of range)
)

// IsRecessive reports whether gene is recessive (only expressed when both
// MainGene and HiddenGene are identical: Brown=4 or Weak=5).
func (g PandaGene) IsRecessive() bool {
	return g == PandaGeneBrown || g == PandaGeneWeak
}

// String returns the canonical vanilla gene name.
func (g PandaGene) String() string {
	switch g {
	case PandaGeneNormal:
		return "normal"
	case PandaGeneLazy:
		return "lazy"
	case PandaGeneWorried:
		return "worried"
	case PandaGenePlayful:
		return "playful"
	case PandaGeneBrown:
		return "brown"
	case PandaGeneWeak:
		return "weak"
	case PandaGeneAggressive:
		return "aggressive"
	default:
		return "normal"
	}
}

// EffectivePandaPersonality computes the expressed personality of a Panda from
// its `mainGene` and `hiddenGene` per vanilla `Panda.getVariantFromGenes`:
//   - If `mainGene` is recessive (`brown` or `weak`), it is expressed ONLY if `mainGene == hiddenGene`; otherwise `normal`.
//   - If `mainGene` is dominant, `mainGene` is expressed directly.
func EffectivePandaPersonality(mainGene, hiddenGene PandaGene) PandaGene {
	if mainGene.IsRecessive() {
		if mainGene == hiddenGene {
			return mainGene
		}
		return PandaGeneNormal
	}
	return mainGene
}

// RollNaturalPandaGene rolls a random natural Panda gene using vanilla weights:
// Normal (5/16), Lazy (1/16), Worried (1/16), Playful (1/16), Brown (2/16), Weak (5/16), Aggressive (1/16).
func RollNaturalPandaGene(rng *rand.Rand) PandaGene {
	if rng == nil {
		return PandaGeneNormal
	}
	r := rng.Intn(16)
	switch {
	case r < 5:
		return PandaGeneNormal
	case r == 5:
		return PandaGeneLazy
	case r == 6:
		return PandaGeneWorried
	case r == 7:
		return PandaGenePlayful
	case r < 10:
		return PandaGeneBrown
	case r < 15:
		return PandaGeneWeak
	default:
		return PandaGeneAggressive
	}
}

// InheritPandaGenes computes a baby panda's `MainGene` and `HiddenGene` from
// two parents, picking one allele from each parent (with 50% swap order) and
// applying a 1/32 mutation roll on each gene.
func InheritPandaGenes(parentA, parentB PandaState, rng *rand.Rand) (mainGene, hiddenGene PandaGene) {
	pickFrom := func(p PandaState) PandaGene {
		if rng == nil || rng.Intn(2) == 0 {
			return p.MainGene
		}
		return p.HiddenGene
	}
	g1 := pickFrom(parentA)
	g2 := pickFrom(parentB)
	if rng != nil && rng.Intn(2) == 1 {
		g1, g2 = g2, g1
	}
	if rng != nil && rng.Intn(32) == 0 {
		g1 = RollNaturalPandaGene(rng)
	}
	if rng != nil && rng.Intn(32) == 0 {
		g2 = RollNaturalPandaGene(rng)
	}
	return g1, g2
}

// FoxState holds per-entity state for a `minecraft:fox`.
type FoxState struct {
	EntityID       int32
	Dimension      string
	Variant        int32 // 0 = red, 1 = snow
	Flags          byte
	TrustedUUIDs   [][16]byte
	HeldItem       world.ItemStack
	EatTicks       int
	DefendTargetID int32
	HuntTargetID   int32
	AttackCooldown int
}

// PandaState holds per-entity state for a `minecraft:panda`.
type PandaState struct {
	EntityID       int32
	Dimension      string
	MainGene       PandaGene
	HiddenGene     PandaGene
	Flags          byte
	EatCounter     int32
	SneezeCounter  int32
	UnhappyCounter int32
	AngerTarget    *PlayerSession
	AngerMobID     int32
	AngerTicks     int
	AttackCooldown int
}

// OcelotState holds per-entity state for a `minecraft:ocelot`.
type OcelotState struct {
	EntityID       int32
	Dimension      string
	Trusting       bool
	TrustedUUID    [16]byte
	HuntTargetID   int32
	AttackCooldown int
}

type wildPetManager struct {
	mu      sync.RWMutex
	foxes   map[int32]*FoxState
	pandas  map[int32]*PandaState
	ocelots map[int32]*OcelotState
}

var serverWildPetManagers sync.Map // map[*Server]*wildPetManager

func (s *Server) wildPetMgr() *wildPetManager {
	if v, ok := serverWildPetManagers.Load(s); ok {
		return v.(*wildPetManager)
	}
	actual, _ := serverWildPetManagers.LoadOrStore(s, &wildPetManager{
		foxes:   make(map[int32]*FoxState),
		pandas:  make(map[int32]*PandaState),
		ocelots: make(map[int32]*OcelotState),
	})
	return actual.(*wildPetManager)
}

// --- Foxes (minecraft:fox) ---

// IsFoxFoodItem reports whether itemName is a berry item used to breed or feed a
// fox in vanilla 26.1.2 (#minecraft:fox_food: sweet_berries, glow_berries).
func IsFoxFoodItem(itemName string) bool {
	switch canonicalResourceName(itemName) {
	case "minecraft:sweet_berries", "minecraft:glow_berries":
		return true
	default:
		return false
	}
}

// IsFoxEdibleMouthItem reports whether a fox prefers to pick up and eat itemName
// over a non-edible item held in its mouth.
func IsFoxEdibleMouthItem(itemName string) bool {
	if IsFoxFoodItem(itemName) {
		return true
	}
	switch canonicalResourceName(itemName) {
	case "minecraft:chicken", "minecraft:cooked_chicken",
		"minecraft:rabbit", "minecraft:cooked_rabbit",
		"minecraft:cod", "minecraft:cooked_cod",
		"minecraft:salmon", "minecraft:cooked_salmon",
		"minecraft:beef", "minecraft:cooked_beef",
		"minecraft:porkchop", "minecraft:cooked_porkchop",
		"minecraft:mutton", "minecraft:cooked_mutton",
		"minecraft:apple", "minecraft:chorus_fruit":
		return true
	default:
		return false
	}
}

// IsFoxPreyMob reports whether a fox naturally hunts and pounces on mobType.
func IsFoxPreyMob(mobType string) bool {
	switch canonicalResourceName(mobType) {
	case MobTypeChicken, "minecraft:rabbit", "minecraft:cod", "minecraft:salmon", MobTypeTadpole:
		return true
	default:
		return false
	}
}

// ResolveFoxVariantForBiome returns `FoxVariantSnow` (1) for snowy taiga / grove
// biomes and `FoxVariantRed` (0) for standard taiga / forest biomes.
func ResolveFoxVariantForBiome(biomeName string) int32 {
	switch canonicalResourceName(biomeName) {
	case "minecraft:snowy_taiga", "minecraft:grove", "minecraft:snowy_plains", "minecraft:snowy_slopes", "minecraft:frozen_peaks":
		return FoxVariantSnow
	default:
		return FoxVariantRed
	}
}

// EncodeFoxEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating Fox.DATA_TYPE_ID (17, INT) and Fox.DATA_FLAGS_ID (18, BYTE).
func EncodeFoxEntityDataPacket(entityID int32, variant int32, flags byte) []byte {
	w := protocol.NewWriter(20)
	w.VarInt(entityID)

	w.Byte(EntityDataFoxVariantID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(variant)

	w.Byte(EntityDataFoxFlagsID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)

	w.Byte(0xFF)
	return w.Bytes()
}

// SpawnFox spawns a `minecraft:fox` (ID 54, 10.0 HP) at (x, y, z) in dimension.
func (s *Server) SpawnFox(dimension string, x, y, z float64, baby bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeFox)
	if typeID < 0 {
		typeID = EntityTypeFoxID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeFox,
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
	mem.health = DefaultFoxMaxHealth
	mem.lastHealth = DefaultFoxMaxHealth
	if baby {
		mem.ageTicks = BabyMobGrowthTicks
	}
	tr.mu.Unlock()

	variant := FoxVariantRed
	if cache := s.DimensionChunks(normDim); cache != nil {
		biome := inferBiomeAtPosition(cache, int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z)))
		variant = ResolveFoxVariantForBiome(biome)
	}

	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	mgr.foxes[id] = &FoxState{
		EntityID:  id,
		Dimension: normDim,
		Variant:   variant,
	}
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		if baby {
			s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeAgeableMobDataPacket(id, true))
		}
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeFoxEntityDataPacket(id, variant, 0))
	}
	return id, nil
}

func (s *Server) ensureFoxState(entityID int32) *FoxState {
	if s == nil || entityID <= 0 {
		return nil
	}
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	fs, ok := mgr.foxes[entityID]
	if !ok || fs == nil {
		dim := world.DimensionOverworld
		variant := FoxVariantRed
		if s.entities != nil {
			if ent := s.entities.Get(entityID); ent != nil {
				dim = ent.EffectiveDimension()
				if cache := s.DimensionChunks(dim); cache != nil {
					biome := inferBiomeAtPosition(cache, int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z)))
					variant = ResolveFoxVariantForBiome(biome)
				}
			}
		}
		fs = &FoxState{
			EntityID:  entityID,
			Dimension: dim,
			Variant:   variant,
		}
		mgr.foxes[entityID] = fs
	}
	return fs
}

// GetFoxState returns a snapshot of the FoxState for entityID.
func (s *Server) GetFoxState(entityID int32) (FoxState, bool) {
	if s == nil || entityID <= 0 {
		return FoxState{}, false
	}
	mgr := s.wildPetMgr()
	mgr.mu.RLock()
	fs, ok := mgr.foxes[entityID]
	var out FoxState
	if ok && fs != nil {
		out = *fs
		if len(fs.TrustedUUIDs) > 0 {
			out.TrustedUUIDs = make([][16]byte, len(fs.TrustedUUIDs))
			copy(out.TrustedUUIDs, fs.TrustedUUIDs)
		}
	}
	mgr.mu.RUnlock()
	return out, ok && fs != nil
}

// SetFoxVariant sets the Fox variant (0=red, 1=snow) and broadcasts SynchedEntityData.
func (s *Server) SetFoxVariant(entityID int32, variant int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeFox {
		return false
	}
	if variant != FoxVariantSnow {
		variant = FoxVariantRed
	}
	fs := s.ensureFoxState(entityID)
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	fs.Variant = variant
	flags := fs.Flags
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeFoxEntityDataPacket(entityID, variant, flags))
	return true
}

// AddFoxTrustedPlayer adds playerUUID to the fox's trusted UUID list (up to 2 UUIDs).
func (s *Server) AddFoxTrustedPlayer(entityID int32, playerUUID [16]byte) bool {
	if s == nil || entityID <= 0 || playerUUID == ([16]byte{}) {
		return false
	}
	fs := s.ensureFoxState(entityID)
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	for _, u := range fs.TrustedUUIDs {
		if u == playerUUID {
			return true
		}
	}
	if len(fs.TrustedUUIDs) >= 2 {
		return false
	}
	fs.TrustedUUIDs = append(fs.TrustedUUIDs, playerUUID)
	return true
}

// IsFoxTrustingPlayer reports whether the fox with entityID trusts playerUUID.
func (s *Server) IsFoxTrustingPlayer(entityID int32, playerUUID [16]byte) bool {
	fs, ok := s.GetFoxState(entityID)
	if !ok {
		return false
	}
	for _, u := range fs.TrustedUUIDs {
		if u == playerUUID {
			return true
		}
	}
	return false
}

// IsFoxSleeping reports whether the fox with entityID is currently sleeping (`FoxFlagSleeping`).
func (s *Server) IsFoxSleeping(entityID int32) bool {
	fs, ok := s.GetFoxState(entityID)
	return ok && (fs.Flags&FoxFlagSleeping) != 0
}

// SetFoxSleeping sets whether the fox with entityID is curled up sleeping (`FoxFlagSleeping`).
func (s *Server) SetFoxSleeping(entityID int32, sleeping bool) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeFox {
		return false
	}
	fs := s.ensureFoxState(entityID)
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	if sleeping {
		fs.Flags |= FoxFlagSleeping
		fs.Flags &^= (FoxFlagSitting | FoxFlagCrouching | FoxFlagPouncing)
	} else {
		fs.Flags &^= FoxFlagSleeping
	}
	variant := fs.Variant
	flags := fs.Flags
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeFoxEntityDataPacket(entityID, variant, flags))
	return true
}

// SetFoxHeldItem sets the item held in the fox's mouth (`FoxState.HeldItem`).
func (s *Server) SetFoxHeldItem(entityID int32, stack world.ItemStack) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	fs := s.ensureFoxState(entityID)
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	fs.HeldItem = stack
	fs.EatTicks = 0
	mgr.mu.Unlock()
	return true
}

// TryFoxPickUpItem attempts to make the fox with entityID pick up a dropped
// item stack from the ground:
//   - If the fox's mouth is empty, it picks up any item.
//   - If the fox is holding a non-food item and encounters an edible food item,
//     it spits out its old item into the world and picks up the food item.
func (s *Server) TryFoxPickUpItem(entityID int32, candidate world.ItemStack) (pickedUp bool, spatOut world.ItemStack) {
	if s == nil || s.entities == nil || entityID <= 0 || candidate.IsEmpty() {
		return false, world.ItemStack{}
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeFox {
		return false, world.ItemStack{}
	}
	fs := s.ensureFoxState(entityID)
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	curHeld := fs.HeldItem
	if !curHeld.IsEmpty() {
		if IsFoxEdibleMouthItem(curHeld.ResolvedID()) || !IsFoxEdibleMouthItem(candidate.ResolvedID()) {
			mgr.mu.Unlock()
			return false, world.ItemStack{}
		}
		spatOut = curHeld
	}
	fs.HeldItem = world.NewItemStack(candidate.ResolvedItemID(), 1)
	fs.EatTicks = 0
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	if !spatOut.IsEmpty() {
		s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.25, ent.Z, spatOut)
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundFoxSpit, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	}
	return true, spatOut
}

// TryFoxPounceHunt executes a fox pounce attack on `preyEntityID` (chicken,
// rabbit, cod, salmon, or tadpole): sets `FoxFlagCrouching | FoxFlagPouncing`,
// leaps upward (`VY = 0.42`) toward the prey, and deals `FoxBiteDamage` (`2.0 HP`).
func (s *Server) TryFoxPounceHunt(foxEntityID, preyEntityID int32) (pounced bool, killed bool) {
	if s == nil || s.entities == nil || foxEntityID <= 0 || preyEntityID <= 0 {
		return false, false
	}
	foxEnt := s.entities.Get(foxEntityID)
	preyEnt := s.entities.Get(preyEntityID)
	if foxEnt == nil || preyEnt == nil || canonicalResourceName(foxEnt.TypeName) != MobTypeFox {
		return false, false
	}
	if !IsFoxPreyMob(preyEnt.TypeName) {
		return false, false
	}
	fs := s.ensureFoxState(foxEntityID)
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	fs.Flags &^= FoxFlagSleeping
	fs.Flags |= (FoxFlagCrouching | FoxFlagPouncing)
	fs.HuntTargetID = preyEntityID
	variant := fs.Variant
	flags := fs.Flags
	mgr.mu.Unlock()

	dx := preyEnt.X - foxEnt.X
	dz := preyEnt.Z - foxEnt.Z
	horiz := math.Hypot(dx, dz)
	vx, vz := 0.0, 0.0
	if horiz > 1e-4 {
		vx = (dx / horiz) * 0.35
		vz = (dz / horiz) * 0.35
	}
	yaw := yawToward(dx, dz)
	s.entities.Update(foxEntityID, func(e *world.Entity) {
		e.X = preyEnt.X
		e.Y = preyEnt.Y
		e.Z = preyEnt.Z
		e.Yaw = yaw
		e.HeadYaw = yaw
		e.VelocityX = clampVelocityShort(vx)
		e.VelocityY = clampVelocityShort(FoxPounceVY)
		e.VelocityZ = clampVelocityShort(vz)
		e.OnGround = false
	})

	dim := foxEnt.EffectiveDimension()
	cp := foxEnt.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeFoxEntityDataPacket(foxEntityID, variant, flags))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundFoxBite, SoundSourceNeutral, preyEnt.X, preyEnt.Y, preyEnt.Z, 1.0, 1.0, 0))
	killed = s.applyDamageToMob(preyEnt, FoxBiteDamage, nil, 0.3, false, false)
	return true, killed
}

// NotifyTrustedPlayerAttacked alerts any foxes that trust `player` within 16
// blocks to defend them by attacking `attackerMobID` (`FoxFlagDefending`).
func (s *Server) NotifyTrustedPlayerAttacked(player *PlayerSession, attackerMobID int32) int {
	if s == nil || s.entities == nil || player == nil || attackerMobID <= 0 {
		return 0
	}
	pSnap := player.Snapshot()
	dim := world.NormalizeDimension(pSnap.Dimension)
	const maxDistSq = 16.0 * 16.0
	alerted := 0

	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	for id, fs := range mgr.foxes {
		if fs == nil || id == attackerMobID {
			continue
		}
		trusted := false
		for _, u := range fs.TrustedUUIDs {
			if u == player.Profile.UUID {
				trusted = true
				break
			}
		}
		if !trusted {
			continue
		}
		ent := s.entities.Get(id)
		if ent == nil || ent.EffectiveDimension() != dim {
			continue
		}
		dx := ent.X - pSnap.X
		dy := ent.Y - pSnap.Y
		dz := ent.Z - pSnap.Z
		if dx*dx+dy*dy+dz*dz <= maxDistSq {
			fs.DefendTargetID = attackerMobID
			fs.Flags &^= FoxFlagSleeping
			fs.Flags |= FoxFlagDefending
			alerted++
		}
	}
	mgr.mu.Unlock()
	return alerted
}

// --- Pandas (minecraft:panda) ---

// IsPandaFoodItem reports whether itemName is bamboo or cake (`minecraft:bamboo`, `minecraft:cake`).
func IsPandaFoodItem(itemName string) bool {
	switch canonicalResourceName(itemName) {
	case "minecraft:bamboo", "minecraft:cake":
		return true
	default:
		return false
	}
}

// EncodePandaEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating a panda's Unhappy (17), Sneeze (18), Eat (19), MainGene (20),
// HiddenGene (21), and Flags (22) for protocol 775.
func EncodePandaEntityDataPacket(entityID int32, mainGene, hiddenGene PandaGene, flags byte, eatCounter, sneezeCounter, unhappyCounter int32) []byte {
	w := protocol.NewWriter(44)
	w.VarInt(entityID)

	w.Byte(EntityDataPandaUnhappyCounterID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(unhappyCounter)

	w.Byte(EntityDataPandaSneezeCounterID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(sneezeCounter)

	w.Byte(EntityDataPandaEatCounterID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(eatCounter)

	w.Byte(EntityDataPandaMainGeneID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(byte(mainGene))

	w.Byte(EntityDataPandaHiddenGeneID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(byte(hiddenGene))

	w.Byte(EntityDataPandaFlagsID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)

	w.Byte(0xFF)
	return w.Bytes()
}

// PandaMaxHealthForGenes returns `10.0 HP` when the panda's expressed personality
// is `PandaGeneWeak` (`mainGene == PandaGeneWeak && hiddenGene == PandaGeneWeak`)
// and `20.0 HP` otherwise.
func PandaMaxHealthForGenes(mainGene, hiddenGene PandaGene) float32 {
	if EffectivePandaPersonality(mainGene, hiddenGene) == PandaGeneWeak {
		return DefaultWeakPandaMaxHealth
	}
	return DefaultPandaMaxHealth
}

// SpawnPanda spawns a `minecraft:panda` (ID 96) at (x, y, z) in dimension with
// the given `mainGene` and `hiddenGene` (rolling natural genes if `rng != nil`
// and both genes are 0).
func (s *Server) SpawnPanda(dimension string, x, y, z float64, baby bool, mainGene, hiddenGene PandaGene, rng *rand.Rand) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	if rng != nil && mainGene == PandaGeneNormal && hiddenGene == PandaGeneNormal {
		mainGene = RollNaturalPandaGene(rng)
		hiddenGene = RollNaturalPandaGene(rng)
	}
	maxHP := PandaMaxHealthForGenes(mainGene, hiddenGene)
	typeID := ResolveEntityTypeID(MobTypePanda)
	if typeID < 0 {
		typeID = EntityTypePandaID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypePanda,
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
	mem.health = maxHP
	mem.lastHealth = maxHP
	if baby {
		mem.ageTicks = BabyMobGrowthTicks
	}
	tr.mu.Unlock()

	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	mgr.pandas[id] = &PandaState{
		EntityID:   id,
		Dimension:  normDim,
		MainGene:   mainGene,
		HiddenGene: hiddenGene,
	}
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		if baby {
			s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeAgeableMobDataPacket(id, true))
		}
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodePandaEntityDataPacket(id, mainGene, hiddenGene, 0, 0, 0, 0))
	}
	return id, nil
}

func (s *Server) ensurePandaState(entityID int32) *PandaState {
	if s == nil || entityID <= 0 {
		return nil
	}
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	ps, ok := mgr.pandas[entityID]
	if !ok || ps == nil {
		ps = &PandaState{
			EntityID:   entityID,
			Dimension:  world.DimensionOverworld,
			MainGene:   PandaGeneNormal,
			HiddenGene: PandaGeneNormal,
		}
		mgr.pandas[entityID] = ps
	}
	return ps
}

// GetPandaState returns a snapshot of the PandaState for entityID.
func (s *Server) GetPandaState(entityID int32) (PandaState, bool) {
	if s == nil || entityID <= 0 {
		return PandaState{}, false
	}
	mgr := s.wildPetMgr()
	mgr.mu.RLock()
	ps, ok := mgr.pandas[entityID]
	var out PandaState
	if ok && ps != nil {
		out = *ps
	}
	mgr.mu.RUnlock()
	return out, ok && ps != nil
}

// SetPandaGenes updates `MainGene` and `HiddenGene` on entityID, adjusts max
// health (`10.0 HP` if expressed `weak`, `20.0 HP` otherwise), and broadcasts
// updated SynchedEntityData.
func (s *Server) SetPandaGenes(entityID int32, mainGene, hiddenGene PandaGene) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypePanda {
		return false
	}
	ps := s.ensurePandaState(entityID)
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	ps.MainGene = mainGene
	ps.HiddenGene = hiddenGene
	flags := ps.Flags
	eat := ps.EatCounter
	sneeze := ps.SneezeCounter
	unhappy := ps.UnhappyCounter
	mgr.mu.Unlock()

	maxHP := PandaMaxHealthForGenes(mainGene, hiddenGene)
	if s.MobHealth(entityID) > maxHP {
		s.SetMobHealth(entityID, maxHP)
	}
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodePandaEntityDataPacket(entityID, mainGene, hiddenGene, flags, eat, sneeze, unhappy))
	return true
}

// CountBambooBlocksNear counts the number of `minecraft:bamboo` or
// `minecraft:bamboo_sapling` blocks within `radius` (default 5) of (cx, cy, cz).
func CountBambooBlocksNear(cache *world.Cache, cx, cy, cz, radius int) int {
	if cache == nil {
		return 0
	}
	if radius <= 0 {
		radius = PandaBambooSearchRadius
	}
	count := 0
	for dy := -2; dy <= 2; dy++ {
		by := cy + dy
		if by < world.MinY || by >= world.MinY+world.WorldHeight {
			continue
		}
		for dx := -radius; dx <= radius; dx++ {
			bx := cx + dx
			for dz := -radius; dz <= radius; dz++ {
				bz := cz + dz
				name := world.BlockName(cache.GetBlock(bx, by, bz))
				if name == "minecraft:bamboo" || name == "minecraft:bamboo_sapling" {
					count++
				}
			}
		}
	}
	return count
}

// TriggerBabyPandaSneeze triggers a sneeze on a baby panda (`PandaFlagSneezing`),
// startling nearby adult pandas into jumping and with a 1/700 chance (or 100% if
// `forceSlimeDrop == true`) dropping 1 `minecraft:slime_ball`.
func (s *Server) TriggerBabyPandaSneeze(entityID int32, forceSlimeDrop bool, rng *rand.Rand) (droppedSlimeID int32, sneezed bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypePanda || !s.IsMobBaby(entityID) {
		return 0, false
	}
	ps := s.ensurePandaState(entityID)
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	ps.Flags |= PandaFlagSneezing
	ps.SneezeCounter = 1
	mainG, hiddenG := ps.MainGene, ps.HiddenGene
	flags := ps.Flags
	eat := ps.EatCounter
	unhappy := ps.UnhappyCounter
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodePandaEntityDataPacket(entityID, mainG, hiddenG, flags, eat, 1, unhappy))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundPandaSneeze, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))

	// Nearby adult pandas within 10 blocks jump in surprise.
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		other := &allEnts[i]
		if other.ID == entityID || other.EffectiveDimension() != dim || canonicalResourceName(other.TypeName) != MobTypePanda {
			continue
		}
		dx := other.X - ent.X
		dy := other.Y - ent.Y
		dz := other.Z - ent.Z
		if dx*dx+dy*dy+dz*dz <= 100.0 {
			s.entities.Update(other.ID, func(e *world.Entity) {
				e.VelocityY = clampVelocityShort(0.32)
				e.OnGround = false
			})
		}
	}

	if forceSlimeDrop || (rng != nil && rng.Intn(700) == 0) {
		slime := world.NewNamedItemStack("minecraft:slime_ball", 1)
		droppedSlimeID, _ = s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.25, ent.Z, slime)
	}
	return droppedSlimeID, true
}

// --- Ocelots (minecraft:ocelot) ---

// EncodeOcelotEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating Ocelot.DATA_TRUSTING (index 17, BOOLEAN).
func EncodeOcelotEntityDataPacket(entityID int32, trusting bool) []byte {
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataOcelotTrustingID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(trusting)
	w.Byte(0xFF)
	return w.Bytes()
}

// SpawnOcelot spawns a `minecraft:ocelot` (ID 91, 10.0 HP) at (x, y, z) in dimension.
func (s *Server) SpawnOcelot(dimension string, x, y, z float64, baby bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeOcelot)
	if typeID < 0 {
		typeID = EntityTypeOcelotID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeOcelot,
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
	mem.health = DefaultOcelotMaxHealth
	mem.lastHealth = DefaultOcelotMaxHealth
	if baby {
		mem.ageTicks = BabyMobGrowthTicks
	}
	tr.mu.Unlock()

	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	mgr.ocelots[id] = &OcelotState{
		EntityID:  id,
		Dimension: normDim,
		Trusting:  false,
	}
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		if baby {
			s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeAgeableMobDataPacket(id, true))
		}
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeOcelotEntityDataPacket(id, false))
	}
	return id, nil
}

func (s *Server) ensureOcelotState(entityID int32) *OcelotState {
	if s == nil || entityID <= 0 {
		return nil
	}
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	os, ok := mgr.ocelots[entityID]
	if !ok || os == nil {
		os = &OcelotState{
			EntityID:  entityID,
			Dimension: world.DimensionOverworld,
		}
		mgr.ocelots[entityID] = os
	}
	return os
}

// GetOcelotState returns a snapshot of the OcelotState for entityID.
func (s *Server) GetOcelotState(entityID int32) (OcelotState, bool) {
	if s == nil || entityID <= 0 {
		return OcelotState{}, false
	}
	mgr := s.wildPetMgr()
	mgr.mu.RLock()
	os, ok := mgr.ocelots[entityID]
	var out OcelotState
	if ok && os != nil {
		out = *os
	}
	mgr.mu.RUnlock()
	return out, ok && os != nil
}

// IsOcelotTrusting reports whether the ocelot with entityID trusts players.
func (s *Server) IsOcelotTrusting(entityID int32) bool {
	os, ok := s.GetOcelotState(entityID)
	return ok && os.Trusting
}

// SetOcelotTrusting sets whether the ocelot with entityID trusts players and
// broadcasts Ocelot.DATA_TRUSTING (index 17).
func (s *Server) SetOcelotTrusting(entityID int32, trusting bool, player *PlayerSession) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeOcelot {
		return false
	}
	os := s.ensureOcelotState(entityID)
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	os.Trusting = trusting
	if player != nil {
		os.TrustedUUID = player.Profile.UUID
	}
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeOcelotEntityDataPacket(entityID, trusting))
	if trusting {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
			EncodeEntityEventPacket(entityID, EntityEventTamingSucceeded))
		if player != nil {
			s.GrantCriterion(player, "minecraft:husbandry/tame_an_animal", "tamed")
		}
	}
	return true
}

// --- Player Interaction & Per-Tick AI for Foxes, Pandas & Ocelots ---

// InteractWithFoxPandaOrOcelot handles right-clicking a fox, panda, or ocelot:
//   - Fox + sweet_berries / glow_berries -> baby growth or love mode (breeding baby trusts player!).
//   - Panda + bamboo -> baby growth, or love mode if >= 8 bamboo blocks within 5 blocks (otherwise sits & eats bamboo).
//   - Ocelot + cod / salmon -> 1/3 chance to gain trust if untrusted; if already trusting, baby growth or love mode.
func (s *Server) InteractWithFoxPandaOrOcelot(session *PlayerSession, entityID int32, rng *rand.Rand) (handled bool, offspringID int32) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false, 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false, 0
	}
	held := session.HeldItem()
	if held.IsEmpty() {
		return false, 0
	}
	heldID := canonicalResourceName(held.ResolvedID())
	norm := canonicalResourceName(ent.TypeName)

	switch norm {
	case MobTypeFox:
		if !IsFoxFoodItem(heldID) {
			return false, 0
		}
		if s.IsMobBaby(entityID) {
			fed, _ := s.FeedBabyMob(session, entityID, heldID)
			return fed, 0
		}
		if s.MobBreedCooldown(entityID) <= 0 && s.MobInLoveTicks(entityID) <= 0 {
			consumeOneHeldItemInSurvival(session)
			s.SetMobInLove(entityID, AnimalLoveDurationTicks, session)
			offspringID, _ = s.TryBreedMobWithNearbyMate(entityID, AnimalBreedRange, rng)
			return true, offspringID
		}
		return false, 0

	case MobTypePanda:
		if !IsPandaFoodItem(heldID) {
			return false, 0
		}
		if s.IsMobBaby(entityID) {
			fed, _ := s.FeedBabyMob(session, entityID, heldID)
			return fed, 0
		}
		dim := ent.EffectiveDimension()
		cache := s.DimensionChunks(dim)
		bx, by, bz := int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z))
		bambooCount := CountBambooBlocksNear(cache, bx, by, bz, PandaBambooSearchRadius)

		consumeOneHeldItemInSurvival(session)
		ps := s.ensurePandaState(entityID)
		mgr := s.wildPetMgr()
		mgr.mu.Lock()
		ps.Flags |= PandaFlagSitting
		ps.EatCounter = 32
		mainG, hiddenG, flags := ps.MainGene, ps.HiddenGene, ps.Flags
		mgr.mu.Unlock()

		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodePandaEntityDataPacket(entityID, mainG, hiddenG, flags, 32, 0, 0))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundPandaEat, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))

		// Vanilla Panda.canFallInLove: requires >= 8 bamboo blocks within 5 blocks!
		if heldID == "minecraft:bamboo" && bambooCount >= PandaBambooRequiredBlocks &&
			s.MobBreedCooldown(entityID) <= 0 && s.MobInLoveTicks(entityID) <= 0 {
			s.SetMobInLove(entityID, AnimalLoveDurationTicks, session)
			offspringID, _ = s.TryBreedMobWithNearbyMate(entityID, AnimalBreedRange, rng)
		}
		return true, offspringID

	case MobTypeOcelot:
		if !IsCatFoodItem(heldID) {
			return false, 0
		}
		if !s.IsOcelotTrusting(entityID) {
			consumeOneHeldItemInSurvival(session)
			if rng == nil || rng.Intn(3) == 0 {
				s.SetOcelotTrusting(entityID, true, session)
			} else {
				cp := ent.ChunkPos()
				s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayEntityEvent,
					EncodeEntityEventPacket(entityID, EntityEventTamingFailed))
			}
			return true, 0
		}
		if s.IsMobBaby(entityID) {
			fed, _ := s.FeedBabyMob(session, entityID, heldID)
			return fed, 0
		}
		if s.MobBreedCooldown(entityID) <= 0 && s.MobInLoveTicks(entityID) <= 0 {
			consumeOneHeldItemInSurvival(session)
			s.SetMobInLove(entityID, AnimalLoveDurationTicks, session)
			offspringID, _ = s.TryBreedMobWithNearbyMate(entityID, AnimalBreedRange, rng)
			return true, offspringID
		}
		return false, 0
	}
	return false, 0
}

func (s *Server) inheritFoxPandaOcelotTraits(parentA, parentB, offspringID int32, offspringType string, causePlayer *PlayerSession, rng *rand.Rand) {
	norm := canonicalResourceName(offspringType)
	switch norm {
	case MobTypeFox:
		fa, _ := s.GetFoxState(parentA)
		fb, _ := s.GetFoxState(parentB)
		variant := fa.Variant
		if rng != nil && rng.Intn(2) == 1 {
			variant = fb.Variant
		}
		s.SetFoxVariant(offspringID, variant)
		if causePlayer != nil {
			s.AddFoxTrustedPlayer(offspringID, causePlayer.Profile.UUID)
		}
		for _, u := range fa.TrustedUUIDs {
			s.AddFoxTrustedPlayer(offspringID, u)
		}
		for _, u := range fb.TrustedUUIDs {
			s.AddFoxTrustedPlayer(offspringID, u)
		}

	case MobTypePanda:
		pa, _ := s.GetPandaState(parentA)
		pb, _ := s.GetPandaState(parentB)
		mg, hg := InheritPandaGenes(pa, pb, rng)
		s.SetPandaGenes(offspringID, mg, hg)
		s.SetMobHealth(offspringID, PandaMaxHealthForGenes(mg, hg))

	case MobTypeOcelot:
		s.SetOcelotTrusting(offspringID, true, causePlayer)

	case MobTypeRabbit:
		ra, _ := s.GetRabbitState(parentA)
		rb, _ := s.GetRabbitState(parentB)
		biome := "minecraft:plains"
		if s.entities != nil {
			if ent := s.entities.Get(offspringID); ent != nil {
				if cache := s.DimensionChunks(ent.EffectiveDimension()); cache != nil {
					biome = inferBiomeAtPosition(cache, int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z)))
				}
			}
		}
		babyVar := InheritRabbitVariant(ra.Variant, rb.Variant, biome, rng)
		s.SetRabbitVariant(offspringID, babyVar)
	}
}

// OnPandaDamaged handles provoking a Panda when hit:
//   - Regular pandas counter-attack once (`AngerTicks = 40`).
//   - Aggressive personality pandas (`EffectivePandaPersonality == PandaGeneAggressive`)
//     stay angry for 400 ticks and alert nearby aggressive pandas!
func (s *Server) OnPandaDamaged(entityID int32, attacker *PlayerSession, attackerMobID int32) {
	if s == nil || entityID <= 0 {
		return
	}
	ps := s.ensurePandaState(entityID)
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	personality := EffectivePandaPersonality(ps.MainGene, ps.HiddenGene)
	if personality == PandaGeneWorried {
		mgr.mu.Unlock()
		return
	}
	dur := 60
	if personality == PandaGeneAggressive {
		dur = 400
	}
	ps.AngerTicks = dur
	ps.AngerTarget = attacker
	ps.AngerMobID = attackerMobID
	mgr.mu.Unlock()
}

// TickFoxesPandasAndOcelots steps all active foxes, pandas, and ocelots each tick.
func (s *Server) TickFoxesPandasAndOcelots(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	mgr := s.wildPetMgr()
	mgr.mu.RLock()
	foxIDs := make([]int32, 0, len(mgr.foxes))
	for id := range mgr.foxes {
		foxIDs = append(foxIDs, id)
	}
	pandaIDs := make([]int32, 0, len(mgr.pandas))
	for id := range mgr.pandas {
		pandaIDs = append(pandaIDs, id)
	}
	ocelotIDs := make([]int32, 0, len(mgr.ocelots))
	for id := range mgr.ocelots {
		ocelotIDs = append(ocelotIDs, id)
	}
	mgr.mu.RUnlock()

	for _, id := range foxIDs {
		s.tickSingleFox(id, rng)
	}
	for _, id := range pandaIDs {
		s.tickSinglePanda(id, rng)
	}
	for _, id := range ocelotIDs {
		s.tickSingleOcelot(id, rng)
	}
}

func (s *Server) tickSingleFox(entityID int32, rng *rand.Rand) {
	ent := s.entities.Get(entityID)
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	fs, ok := mgr.foxes[entityID]
	if !ok || fs == nil || ent == nil {
		delete(mgr.foxes, entityID)
		mgr.mu.Unlock()
		return
	}
	if fs.AttackCooldown > 0 {
		fs.AttackCooldown--
	}
	// Eat held food item after FoxEatHeldTicks (600 ticks).
	shouldEatHeld := false
	if !fs.HeldItem.IsEmpty() && IsFoxEdibleMouthItem(fs.HeldItem.ResolvedID()) {
		fs.EatTicks++
		if fs.EatTicks >= FoxEatHeldTicks {
			shouldEatHeld = true
		}
	}
	snap := *fs
	mgr.mu.Unlock()

	if shouldEatHeld {
		s.EatFoxHeldItem(entityID, rng)
		ent = s.entities.Get(entityID)
		if ent == nil {
			return
		}
	}

	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	feetY := int(math.Floor(ent.Y + 0.01))

	// 1. Check nearby dropped item entities to pick up into mouth.
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		it := &allEnts[i]
		if !it.IsItem() || it.EffectiveDimension() != dim || it.PickupDelay > 0 {
			continue
		}
		dx := it.X - ent.X
		dy := it.Y - ent.Y
		dz := it.Z - ent.Z
		if dx*dx+dy*dy+dz*dz <= 2.25 {
			if picked, _ := s.TryFoxPickUpItem(entityID, it.Item); picked {
				if it.Item.Count <= 1 {
					s.entities.Remove(it.ID)
					cp := it.ChunkPos()
					s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
						encodeRemoveEntitiesPacket(nil, []int32{it.ID}))
				} else {
					s.entities.Update(it.ID, func(e *world.Entity) {
						e.Item.Count--
					})
				}
				break
			}
		}
	}

	// 2. Defend trusted player if DefendTargetID > 0.
	if snap.DefendTargetID > 0 {
		target := s.entities.Get(snap.DefendTargetID)
		if target == nil || target.EffectiveDimension() != dim {
			mgr.mu.Lock()
			fs.DefendTargetID = 0
			fs.Flags &^= FoxFlagDefending
			mgr.mu.Unlock()
		} else {
			s.stepEntityTowardPoint(ent, cache, feetY, target.X, target.Y, target.Z, FoxHuntSpeed, 1.2)
			dx := target.X - ent.X
			dy := target.Y - ent.Y
			dz := target.Z - ent.Z
			if dx*dx+dy*dy+dz*dz <= 4.0 && snap.AttackCooldown <= 0 {
				mgr.mu.Lock()
				fs.AttackCooldown = 20
				mgr.mu.Unlock()
				if s.applyDamageToMob(target, FoxBiteDamage, nil, 0.3, false, false) {
					mgr.mu.Lock()
					fs.DefendTargetID = 0
					fs.Flags &^= FoxFlagDefending
					mgr.mu.Unlock()
				}
			}
			return
		}
	}

	// 3. Check for non-sneaking untrusted players within FoxFleePlayerDist (12 blocks) -> wake up and flee!
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() || p.IsSneaking() {
			continue
		}
		pSnap := p.Snapshot()
		if pSnap.GameMode == GameModeSpectator || pSnap.GameMode == GameModeCreative || world.NormalizeDimension(pSnap.Dimension) != dim {
			continue
		}
		if s.IsFoxTrustingPlayer(entityID, p.Profile.UUID) {
			continue
		}
		dx := ent.X - pSnap.X
		dz := ent.Z - pSnap.Z
		dist := math.Hypot(dx, dz)
		if dist <= FoxFleePlayerDist && dist > 1e-4 {
			if (snap.Flags & FoxFlagSleeping) != 0 {
				s.SetFoxSleeping(entityID, false)
			}
			goalX := ent.X + (dx/dist)*4.0
			goalZ := ent.Z + (dz/dist)*4.0
			s.stepEntityTowardPoint(ent, cache, feetY, goalX, ent.Y, goalZ, FoxFleeSpeed, 0.2)
			return
		}
	}

	// 4. Daytime sleeping behavior: during daytime (not thundering), foxes curl up and sleep.
	isDay := !s.IsNight() && !s.IsThundering()
	if isDay {
		if (snap.Flags & FoxFlagSleeping) == 0 {
			s.SetFoxSleeping(entityID, true)
		}
		return
	}
	if (snap.Flags & FoxFlagSleeping) != 0 {
		s.SetFoxSleeping(entityID, false)
	}

	// 5. Hunt nearby prey (chickens, rabbits, cod, salmon, tadpoles) within 8 blocks at night.
	if snap.AttackCooldown <= 0 {
		for i := range allEnts {
			prey := &allEnts[i]
			if prey.ID == entityID || prey.EffectiveDimension() != dim || !IsFoxPreyMob(prey.TypeName) {
				continue
			}
			dx := prey.X - ent.X
			dy := prey.Y - ent.Y
			dz := prey.Z - ent.Z
			dSq := dx*dx + dy*dy + dz*dz
			if dSq <= FoxPounceRange*FoxPounceRange {
				mgr.mu.Lock()
				if cur := mgr.foxes[entityID]; cur != nil {
					cur.AttackCooldown = 20
				}
				mgr.mu.Unlock()
				s.TryFoxPounceHunt(entityID, prey.ID)
				return
			} else if dSq <= 64.0 {
				s.stepEntityTowardPoint(ent, cache, feetY, prey.X, prey.Y, prey.Z, FoxHuntSpeed, 1.5)
				return
			}
		}
	}
}

func (s *Server) tickSinglePanda(entityID int32, rng *rand.Rand) {
	ent := s.entities.Get(entityID)
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	ps, ok := mgr.pandas[entityID]
	if !ok || ps == nil || ent == nil {
		delete(mgr.pandas, entityID)
		mgr.mu.Unlock()
		return
	}
	if ps.AttackCooldown > 0 {
		ps.AttackCooldown--
	}
	if ps.EatCounter > 0 {
		ps.EatCounter--
		if ps.EatCounter == 0 {
			ps.Flags &^= PandaFlagSitting
		}
	}
	if ps.SneezeCounter > 0 {
		ps.SneezeCounter++
		if ps.SneezeCounter > 20 {
			ps.SneezeCounter = 0
			ps.Flags &^= PandaFlagSneezing
		}
	}
	if ps.AngerTicks > 0 {
		ps.AngerTicks--
		if ps.AngerTicks == 0 {
			ps.AngerTarget = nil
			ps.AngerMobID = 0
		}
	}
	snap := *ps
	mgr.mu.Unlock()

	personality := EffectivePandaPersonality(snap.MainGene, snap.HiddenGene)

	// 1. Baby panda sneezing roll (1/500 for weak baby pandas, 1/6000 for other baby pandas).
	if s.IsMobBaby(entityID) && rng != nil {
		sneezeDenom := 6000
		if personality == PandaGeneWeak {
			sneezeDenom = 500
		}
		if rng.Intn(sneezeDenom) == 0 {
			_, _ = s.TriggerBabyPandaSneeze(entityID, false, rng)
		}
	}

	// 2. Worried pandas sit and shake / hide face during thunderstorms.
	if personality == PandaGeneWorried && s.IsThundering() {
		mgr.mu.Lock()
		ps.Flags |= PandaFlagSitting
		mgr.mu.Unlock()
		return
	}

	// 3. Playful pandas periodically roll (`PandaFlagRolling`); Lazy pandas lie on back (`PandaFlagOnBack`).
	if personality == PandaGenePlayful && rng != nil && rng.Intn(400) == 0 {
		mgr.mu.Lock()
		ps.Flags |= PandaFlagRolling
		mgr.mu.Unlock()
	} else if personality == PandaGeneLazy && rng != nil && rng.Intn(400) == 0 {
		s.SetPandaLyingOnBack(entityID, true)
	}

	// 3b. Pick up nearby dropped bamboo or cake item entities when not already eating.
	if snap.EatCounter <= 0 {
		dim := ent.EffectiveDimension()
		allEnts := s.entities.AppendAll(nil)
		for i := range allEnts {
			it := &allEnts[i]
			if !it.IsItem() || it.EffectiveDimension() != dim || it.PickupDelay > 0 || !IsPandaFoodItem(it.Item.ResolvedID()) {
				continue
			}
			dx := it.X - ent.X
			dy := it.Y - ent.Y
			dz := it.Z - ent.Z
			if dx*dx+dy*dy+dz*dz <= 4.0 {
				if it.Item.Count <= 1 {
					s.entities.Remove(it.ID)
					cp := it.ChunkPos()
					s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
						encodeRemoveEntitiesPacket(nil, []int32{it.ID}))
				} else {
					s.entities.Update(it.ID, func(e *world.Entity) {
						e.Item.Count--
					})
				}
				mgr.mu.Lock()
				ps.Flags |= PandaFlagSitting
				ps.EatCounter = 32
				mainG, hiddenG, flags := ps.MainGene, ps.HiddenGene, ps.Flags
				mgr.mu.Unlock()
				cp := ent.ChunkPos()
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
					EncodePandaEntityDataPacket(entityID, mainG, hiddenG, flags, 32, 0, 0))
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket(SoundPandaEat, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
				break
			}
		}
	}

	// 4. Provoked / Aggressive panda attacking its target.
	if snap.AngerTicks > 0 && snap.AngerTarget != nil && !snap.AngerTarget.IsDead() {
		dim := ent.EffectiveDimension()
		cache := s.DimensionChunks(dim)
		feetY := int(math.Floor(ent.Y + 0.01))
		pSnap := snap.AngerTarget.Snapshot()
		if world.NormalizeDimension(pSnap.Dimension) == dim && pSnap.GameMode != GameModeSpectator {
			s.stepEntityTowardPoint(ent, cache, feetY, pSnap.X, pSnap.Y, pSnap.Z, 0.14, 1.3)
			dx := pSnap.X - ent.X
			dy := pSnap.Y - ent.Y
			dz := pSnap.Z - ent.Z
			if dx*dx+dy*dy+dz*dz <= PandaAttackReach*PandaAttackReach && snap.AttackCooldown <= 0 {
				mgr.mu.Lock()
				ps.AttackCooldown = 20
				if personality != PandaGeneAggressive {
					ps.AngerTicks = 0
					ps.AngerTarget = nil
				}
				mgr.mu.Unlock()
				s.AttackPlayerFromMob(entityID, MobTypePanda, ent.X, ent.Z, snap.AngerTarget, PandaAttackDamage)
			}
		}
	}
}

func (s *Server) tickSingleOcelot(entityID int32, _ *rand.Rand) {
	ent := s.entities.Get(entityID)
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	os, ok := mgr.ocelots[entityID]
	if !ok || os == nil || ent == nil {
		delete(mgr.ocelots, entityID)
		mgr.mu.Unlock()
		return
	}
	if os.AttackCooldown > 0 {
		os.AttackCooldown--
	}
	trusting := os.Trusting
	mgr.mu.Unlock()

	if trusting {
		return
	}
	// Untrusted ocelots flee from non-sneaking players within 10 blocks.
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	feetY := int(math.Floor(ent.Y + 0.01))
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() || p.IsSneaking() {
			continue
		}
		pSnap := p.Snapshot()
		if pSnap.GameMode == GameModeSpectator || pSnap.GameMode == GameModeCreative || world.NormalizeDimension(pSnap.Dimension) != dim {
			continue
		}
		dx := ent.X - pSnap.X
		dz := ent.Z - pSnap.Z
		dist := math.Hypot(dx, dz)
		if dist <= 10.0 && dist > 1e-4 {
			goalX := ent.X + (dx/dist)*4.0
			goalZ := ent.Z + (dz/dist)*4.0
			s.stepEntityTowardPoint(ent, cache, feetY, goalX, ent.Y, goalZ, OcelotFleeSpeed, 0.2)
			return
		}
	}
}
