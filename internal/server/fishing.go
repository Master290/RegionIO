package server

import (
	"encoding/binary"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Fishing rod and bobber constants for Minecraft 26.1.2 (protocol 775).
const (
	FishingRodItemID = "minecraft:fishing_rod"

	// EntityEventFishingHookReel is the ClientboundEntityEventPacket (0x22)
	// event byte (31) broadcast when a fishing hook bites or reels in a hooked entity.
	EntityEventFishingHookReel byte = 31

	// SynchedEntityData indices on FishingHook (extending Projectile -> Entity,
	// where Entity defines 8 data items 0..7 and Projectile defines 0).
	EntityDataFishingHookedEntityID byte = 8 // INT serializer (1): hookedEntityID + 1 (0 = none)
	EntityDataFishingBitingID       byte = 9 // BOOLEAN serializer (8): true when fish is biting

	// Vanilla FishingHook wait/hook/nibble tick ranges at 20 TPS.
	FishingMinWaitTicks       = 100 // 5s minimum base wait
	FishingMaxWaitTicks       = 600 // 30s maximum base wait
	FishingLureTicksPerLevel  = 100 // 5s (100 ticks) reduction per Lure level
	FishingMinHookApproach    = 20  // 1s minimum approach
	FishingMaxHookApproach    = 80  // 4s maximum approach
	FishingMinNibbleTicks     = 20  // 1s minimum bite window
	FishingMaxNibbleTicks     = 40  // 2s maximum bite window
	FishingMaxDistanceSquared = 1024.0

	// Fishing rod durability wear amounts in vanilla 26.1.2 FishingHook.retrieve.
	FishingWearNone         int32 = 0
	FishingWearCatch        int32 = 1
	FishingWearGround       int32 = 2
	FishingWearHookedItem   int32 = 3
	FishingWearHookedEntity int32 = 5

	// Vanilla 26.1.2 fishing loot category base weights and qualities
	// (from data/minecraft/loot_table/gameplay/fishing.json).
	FishingWeightJunk     = 10
	FishingQualityJunk    = -2
	FishingWeightTreasure = 5
	FishingQualityTreasure = 2
	FishingWeightFish     = 85
	FishingQualityFish    = -1
)

// FishHookState represents the state machine of an active fishing bobber.
type FishHookState int8

const (
	FishHookStateFlying FishHookState = iota
	FishHookStateHookedInEntity
	FishHookStateBobbing
)

// FishingLootCategory identifies which sub-table a fishing catch came from.
type FishingLootCategory string

const (
	FishingCategoryFish     FishingLootCategory = "fish"
	FishingCategoryJunk     FishingLootCategory = "junk"
	FishingCategoryTreasure FishingLootCategory = "treasure"
)

// FishingLootDrop describes a single item stack + optional ItemMeta rolled from
// the vanilla 26.1.2 fishing loot tables.
type FishingLootDrop struct {
	Category FishingLootCategory
	Stack    world.ItemStack
	Meta     ItemMeta
}

type fishingLootEntry struct {
	ItemName          string
	Weight            int
	Count             int32
	MaxDamageFraction float64 // if > 0, random damage in [0, MaxDamageFraction * maxDurability]
	EnchantWithLevels int     // if > 0, enchant with level 30 treasure enchantments
	JungleOnly        bool
}

// Vanilla 26.1.2 data/minecraft/loot_table/gameplay/fishing/fish.json entries.
var vanillaFishingFishTable = []fishingLootEntry{
	{ItemName: "minecraft:cod", Weight: 60, Count: 1},
	{ItemName: "minecraft:salmon", Weight: 25, Count: 1},
	{ItemName: "minecraft:tropical_fish", Weight: 2, Count: 1},
	{ItemName: "minecraft:pufferfish", Weight: 13, Count: 1},
}

// Vanilla 26.1.2 data/minecraft/loot_table/gameplay/fishing/junk.json entries.
var vanillaFishingJunkTable = []fishingLootEntry{
	{ItemName: "minecraft:lily_pad", Weight: 17, Count: 1},
	{ItemName: "minecraft:leather_boots", Weight: 10, Count: 1, MaxDamageFraction: 0.9},
	{ItemName: "minecraft:leather", Weight: 10, Count: 1},
	{ItemName: "minecraft:bone", Weight: 10, Count: 1},
	{ItemName: "minecraft:potion", Weight: 10, Count: 1},
	{ItemName: "minecraft:string", Weight: 5, Count: 1},
	{ItemName: "minecraft:fishing_rod", Weight: 2, Count: 1, MaxDamageFraction: 0.9},
	{ItemName: "minecraft:bowl", Weight: 10, Count: 1},
	{ItemName: "minecraft:stick", Weight: 5, Count: 1},
	{ItemName: "minecraft:ink_sac", Weight: 1, Count: 10},
	{ItemName: "minecraft:tripwire_hook", Weight: 10, Count: 1},
	{ItemName: "minecraft:rotten_flesh", Weight: 10, Count: 1},
	{ItemName: "minecraft:bamboo", Weight: 10, Count: 1, JungleOnly: true},
}

// Vanilla 26.1.2 data/minecraft/loot_table/gameplay/fishing/treasure.json entries.
var vanillaFishingTreasureTable = []fishingLootEntry{
	{ItemName: "minecraft:name_tag", Weight: 1, Count: 1},
	{ItemName: "minecraft:saddle", Weight: 1, Count: 1},
	{ItemName: "minecraft:bow", Weight: 1, Count: 1, MaxDamageFraction: 0.25, EnchantWithLevels: 30},
	{ItemName: "minecraft:fishing_rod", Weight: 1, Count: 1, MaxDamageFraction: 0.25, EnchantWithLevels: 30},
	{ItemName: "minecraft:book", Weight: 1, Count: 1, EnchantWithLevels: 30},
	{ItemName: "minecraft:nautilus_shell", Weight: 1, Count: 1},
}

// FishingBobberState holds the server-side state of an active minecraft:fishing_bobber.
type FishingBobberState struct {
	EntityID        int32
	Owner           *PlayerSession
	OwnerEntityID   int32
	Dimension       string
	Hand            int32
	RodSlot         int
	LureLevel       int
	LureSpeedTicks  int
	LuckOfTheSea    int
	State           FishHookState
	VX, VY, VZ      float64
	OnGround        bool
	OpenWater       bool
	OutOfWaterTicks int
	TimeUntilLured  int
	TimeUntilHooked int
	NibbleTicks     int
	FishAngle       float32
	Biting          bool
	HookedEntityID  int32
}

type serverFishingTracker struct {
	mu             sync.Mutex
	byEntity       map[int32]*FishingBobberState
	byOwner        map[*PlayerSession]*FishingBobberState
	lastCatch      map[*PlayerSession]FishingLootDrop
	droppedMeta    map[int32]ItemMeta
	jungleOverride map[world.BlockPos]bool
	rng            *rand.Rand
}

var serverFishingStates sync.Map // map[*Server]*serverFishingTracker

func (s *Server) fishingTracker() *serverFishingTracker {
	if v, ok := serverFishingStates.Load(s); ok {
		return v.(*serverFishingTracker)
	}
	actual, _ := serverFishingStates.LoadOrStore(s, &serverFishingTracker{
		byEntity:       make(map[int32]*FishingBobberState),
		byOwner:        make(map[*PlayerSession]*FishingBobberState),
		lastCatch:      make(map[*PlayerSession]FishingLootDrop),
		droppedMeta:    make(map[int32]ItemMeta),
		jungleOverride: make(map[world.BlockPos]bool),
		rng:            rand.New(rand.NewSource(time.Now().UnixNano())),
	})
	return actual.(*serverFishingTracker)
}

// LastCaughtFishingLoot returns the most recent FishingLootDrop caught by session.
func (s *Server) LastCaughtFishingLoot(session *PlayerSession) (FishingLootDrop, bool) {
	if s == nil || session == nil {
		return FishingLootDrop{}, false
	}
	ft := s.fishingTracker()
	ft.mu.Lock()
	defer ft.mu.Unlock()
	drop, ok := ft.lastCatch[session]
	return drop, ok
}

// DroppedFishingItemMeta returns the ItemMeta (damage/enchantments) rolled for a
// spawned fishing catch ItemEntity.
func (s *Server) DroppedFishingItemMeta(entityID int32) (ItemMeta, bool) {
	if s == nil || entityID <= 0 {
		return ItemMeta{}, false
	}
	ft := s.fishingTracker()
	ft.mu.Lock()
	defer ft.mu.Unlock()
	meta, ok := ft.droppedMeta[entityID]
	return meta, ok
}

// SetFishingJungleAt marks a block column (x, z) as a jungle biome for fishing
// loot table evaluation (enabling bamboo drops from the junk table).
func (s *Server) SetFishingJungleAt(x, z int, isJungle bool) {
	if s == nil {
		return
	}
	ft := s.fishingTracker()
	ft.mu.Lock()
	defer ft.mu.Unlock()
	pos := world.BlockPos{X: x, Y: 0, Z: z}
	if isJungle {
		ft.jungleOverride[pos] = true
	} else {
		delete(ft.jungleOverride, pos)
	}
}

// IsJungleFishingAt reports whether (x, y, z) is in a jungle biome for fishing loot.
func (s *Server) IsJungleFishingAt(x, y, z int) bool {
	if s == nil {
		return false
	}
	ft := s.fishingTracker()
	ft.mu.Lock()
	isJ := ft.jungleOverride[world.BlockPos{X: x, Y: 0, Z: z}]
	ft.mu.Unlock()
	return isJ
}

// PlayerFishingLuck computes the effective fishing luck parameter combining
// the rod's Luck of the Sea level with the player's Luck (+1/lvl) and Bad Luck (-1/lvl) status effects.
func PlayerFishingLuck(session *PlayerSession, luckOfTheSea int) float32 {
	luck := float32(luckOfTheSea)
	if session == nil {
		return luck
	}
	if inst, ok := session.PlayerEffect(EffectLuck); ok {
		luck += float32(inst.Amplifier + 1)
	}
	if inst, ok := session.PlayerEffect(EffectUnluck); ok {
		luck -= float32(inst.Amplifier + 1)
	}
	return luck
}

// ActiveFishingBobber returns a snapshot copy of session's active fishing
// bobber state, or nil if session does not have a cast line.
func (s *Server) ActiveFishingBobber(session *PlayerSession) *FishingBobberState {
	if s == nil || session == nil {
		return nil
	}
	ft := s.fishingTracker()
	ft.mu.Lock()
	defer ft.mu.Unlock()
	st := ft.byOwner[session]
	if st == nil {
		return nil
	}
	cp := *st
	return &cp
}

// FishingBobberByEntityID returns a snapshot copy of the fishing bobber state
// for entityID, or nil if entityID is not an active fishing bobber.
func (s *Server) FishingBobberByEntityID(entityID int32) *FishingBobberState {
	if s == nil || entityID <= 0 {
		return nil
	}
	ft := s.fishingTracker()
	ft.mu.Lock()
	defer ft.mu.Unlock()
	st := ft.byEntity[entityID]
	if st == nil {
		return nil
	}
	cp := *st
	return &cp
}

// SetFishingBobberTimers configures the internal bite timers and open-water flag
// on session's active fishing bobber (useful for deterministic testing).
func (s *Server) SetFishingBobberTimers(session *PlayerSession, timeUntilLured, timeUntilHooked, nibbleTicks int, openWater bool) bool {
	if s == nil || session == nil {
		return false
	}
	ft := s.fishingTracker()
	ft.mu.Lock()
	st := ft.byOwner[session]
	if st == nil {
		ft.mu.Unlock()
		return false
	}
	st.TimeUntilLured = timeUntilLured
	st.TimeUntilHooked = timeUntilHooked
	st.NibbleTicks = nibbleTicks
	st.Biting = nibbleTicks > 0
	st.OpenWater = openWater
	if nibbleTicks > 0 || timeUntilHooked > 0 || timeUntilLured > 0 {
		st.State = FishHookStateBobbing
	}
	entityID := st.EntityID
	dim := st.Dimension
	biting := st.Biting
	hookedID := st.HookedEntityID
	ft.mu.Unlock()

	if ent := s.entities.Get(entityID); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeFishingBobberDataPacket(entityID, hookedID, biting))
	}
	return true
}

// SetFishingBobberHookedEntity attaches session's active fishing bobber to
// targetEntityID (matching vanilla FishingHook.setHookedEntity).
func (s *Server) SetFishingBobberHookedEntity(session *PlayerSession, targetEntityID int32) bool {
	if s == nil || session == nil {
		return false
	}
	ft := s.fishingTracker()
	ft.mu.Lock()
	st := ft.byOwner[session]
	if st == nil {
		ft.mu.Unlock()
		return false
	}
	st.HookedEntityID = targetEntityID
	if targetEntityID > 0 {
		st.State = FishHookStateHookedInEntity
	} else if st.State == FishHookStateHookedInEntity {
		st.State = FishHookStateFlying
	}
	entityID := st.EntityID
	dim := st.Dimension
	biting := st.Biting
	ft.mu.Unlock()

	if ent := s.entities.Get(entityID); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeFishingBobberDataPacket(entityID, targetEntityID, biting))
	}
	return true
}

// UseFishingRod handles a player right-clicking (use_item) with a fishing rod
// in hand (0 = main hand, 1 = offhand).
//   - If the player has no active bobber, casts a new minecraft:fishing_bobber
//     and returns (cast=true, reelDamage=0).
//   - If the player already has an active bobber, reels it in (pulling hooked
//     entities or rolling fishing loot + spawning XP orbs on a bite), applies
//     durability wear to the fishing rod (respecting Unbreaking), and returns
//     (cast=false, reelDamage).
func (s *Server) UseFishingRod(session *PlayerSession, hand int32) (cast bool, reelDamage int32) {
	return s.UseFishingRodWithRand(session, hand, nil)
}

// UseFishingRodWithRand is UseFishingRod with an explicit *rand.Rand for
// deterministic tests.
func (s *Server) UseFishingRodWithRand(session *PlayerSession, hand int32, rng *rand.Rand) (cast bool, reelDamage int32) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false, 0
	}
	slot := fishingRodSlotForHand(snap, hand)
	stack := session.InventorySlot(slot)
	if stack.IsEmpty() || canonicalResourceName(stack.ResolvedID()) != FishingRodItemID {
		return false, 0
	}

	ft := s.fishingTracker()
	ft.mu.Lock()
	if rng == nil {
		rng = ft.rng
	}
	existing := ft.byOwner[session]
	ft.mu.Unlock()

	if existing != nil {
		dmg := s. retrieveFishingBobber(session, slot, stack, rng)
		return false, dmg
	}

	if id := s.castFishingBobber(session, hand, slot, rng); id != 0 {
		return true, 0
	}
	return false, 0
}

func fishingRodSlotForHand(snap PlayerSnapshot, hand int32) int {
	if hand == 1 {
		return 45 // offhand container slot
	}
	return world.HotbarContainerOffset + int(snap.SelectedSlot)
}

func (s *Server) castFishingBobber(session *PlayerSession, hand int32, rodSlot int, rng *rand.Rand) int32 {
	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	meta := GetSlotItemMeta(session, rodSlot)
	lureLevel := meta.EnchantmentLevel("minecraft:lure")
	if lureLevel < 0 {
		lureLevel = 0
	}
	luckLevel := meta.EnchantmentLevel("minecraft:luck_of_the_sea")
	if luckLevel < 0 {
		luckLevel = 0
	}
	lureSpeedTicks := lureLevel * FishingLureTicksPerLevel

	// Vanilla FishingHook(Player, Level, int luck, int lureSpeed) launch geometry:
	yawRad := -float64(snap.Yaw)*(math.Pi/180.0) - math.Pi
	pitchRad := -float64(snap.Pitch) * (math.Pi / 180.0)
	f0 := math.Cos(yawRad)
	f1 := math.Sin(yawRad)
	f2 := -math.Cos(pitchRad)
	f3 := math.Sin(pitchRad)

	spawnX := snap.X - f1*0.3
	spawnY := snap.Y + 1.62
	spawnZ := snap.Z - f0*0.3

	dirX := -f1
	dirY := 0.0
	if math.Abs(f2) > 1e-6 {
		dirY = -(f3 / f2)
		if dirY < -5.0 {
			dirY = -5.0
		} else if dirY > 5.0 {
			dirY = 5.0
		}
	}
	dirZ := -f0
	length := math.Sqrt(dirX*dirX + dirY*dirY + dirZ*dirZ)
	var vx, vy, vz float64
	if length > 1e-6 {
		scale := 0.6 / length
		vx = dirX * scale
		vy = dirY * scale
		vz = dirZ * scale
	}

	typeID := ResolveEntityTypeID(EntityTypeFishingBobber)
	if typeID < 0 {
		typeID = EntityTypeFishingBobberID
	}
	ent := &world.Entity{
		TypeID:    typeID,
		TypeName:  EntityTypeFishingBobber,
		Dimension: dim,
		X:         spawnX,
		Y:         spawnY,
		Z:         spawnZ,
		Yaw:       snap.Yaw,
		Pitch:     snap.Pitch,
		HeadYaw:   snap.Yaw,
		VelocityX: clampVelocityComponent(vx),
		VelocityY: clampVelocityComponent(vy),
		VelocityZ: clampVelocityComponent(vz),
		OnGround:  false,
	}
	id := s.entities.Add(ent)

	waitTicks := ComputeInitialFishingWaitTicks(rng, lureLevel)
	st := &FishingBobberState{
		EntityID:       id,
		Owner:          session,
		OwnerEntityID:  session.EntityID,
		Dimension:      dim,
		Hand:           hand,
		RodSlot:        rodSlot,
		LureLevel:      lureLevel,
		LureSpeedTicks: lureSpeedTicks,
		LuckOfTheSea:   luckLevel,
		State:          FishHookStateFlying,
		VX:             vx,
		VY:             vy,
		VZ:             vz,
		OpenWater:      true,
		TimeUntilLured: waitTicks,
	}

	ft := s.fishingTracker()
	ft.mu.Lock()
	if prev := ft.byOwner[session]; prev != nil {
		delete(ft.byEntity, prev.EntityID)
		s.entities.Remove(prev.EntityID)
	}
	ft.byEntity[id] = st
	ft.byOwner[session] = st
	ft.mu.Unlock()

	if saved := s.entities.Get(id); saved != nil {
		cp := saved.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayAddEntity,
			s.EncodeFishingBobberAddEntityPacket(nil, saved))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:entity.fishing_bobber.throw", SoundSourceNeutral, spawnX, spawnY, spawnZ, 0.5, 0.4, 0))
	}
	return id
}

func (s *Server) retrieveFishingBobber(session *PlayerSession, rodSlot int, rodStack world.ItemStack, rng *rand.Rand) int32 {
	ft := s.fishingTracker()
	ft.mu.Lock()
	st := ft.byOwner[session]
	if st == nil {
		ft.mu.Unlock()
		return 0
	}
	delete(ft.byOwner, session)
	delete(ft.byEntity, st.EntityID)
	bobberCopy := *st
	if rng == nil {
		rng = ft.rng
	}
	ft.mu.Unlock()

	bobberEnt := s.entities.Get(bobberCopy.EntityID)
	s.entities.Remove(bobberCopy.EntityID)

	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	bx, by, bz := snap.X, snap.Y, snap.Z
	onGround := bobberCopy.OnGround
	if bobberEnt != nil {
		bx, by, bz = bobberEnt.X, bobberEnt.Y, bobberEnt.Z
		if bobberEnt.OnGround {
			onGround = true
		}
	}

	var reelDamage int32
	if bobberCopy.HookedEntityID > 0 {
		if hooked := s.entities.Get(bobberCopy.HookedEntityID); hooked != nil {
			s.pullHookedEntityTowardPlayer(hooked.ID, bx, by, bz, snap.X, snap.Y, snap.Z, dim)
			cp := hooked.ChunkPos()
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
				EncodeEntityEventPacket(bobberCopy.EntityID, EntityEventFishingHookReel))
			if hooked.IsItem() {
				reelDamage = FishingWearHookedItem
			} else {
				reelDamage = FishingWearHookedEntity
			}
		} else if hookedPlayer := s.findOnlinePlayerByEntityID(bobberCopy.HookedEntityID, dim); hookedPlayer != nil {
			s.pullHookedPlayerTowardPlayer(hookedPlayer, bx, by, bz, snap.X, snap.Y, snap.Z, dim)
			pSnap := hookedPlayer.Snapshot()
			cpX := int32(int64(math.Floor(pSnap.X)) >> 4)
			cpZ := int32(int64(math.Floor(pSnap.Z)) >> 4)
			s.BroadcastDimensionChunk(dim, cpX, cpZ, protocol.PlayEntityEvent,
				EncodeEntityEventPacket(bobberCopy.EntityID, EntityEventFishingHookReel))
			reelDamage = FishingWearHookedEntity
		}
	} else if bobberCopy.NibbleTicks > 0 {
		luck := PlayerFishingLuck(session, bobberCopy.LuckOfTheSea)
		isJungle := s.IsJungleFishingAt(int(math.Floor(bx)), int(math.Floor(by)), int(math.Floor(bz)))
		drop := RollFishingLoot(rng, luck, bobberCopy.OpenWater, isJungle)
		ft.mu.Lock()
		ft.lastCatch[session] = drop
		ft.mu.Unlock()
		if !drop.Stack.IsEmpty() {
			itemEntID, itemEnt := s.SpawnItemEntityInDimension(dim, bx, by, bz, drop.Stack)
			if itemEntID > 0 && (drop.Meta.Damage > 0 || drop.Meta.HasEnchantments() || drop.Meta.RepairCost > 0) {
				ft.mu.Lock()
				ft.droppedMeta[itemEntID] = drop.Meta.Clone()
				ft.mu.Unlock()
			}
			if itemEnt != nil {
				// Vanilla FishingHook.retrieve item velocity toward owner:
				// d0 = player.X - bobber.X; d1 = player.Y - bobber.Y; d2 = player.Z - bobber.Z
				// vel = (d0*0.1, d1*0.1 + sqrt(sqrt(d0^2+d1^2+d2^2))*0.08, d2*0.1)
				dx := snap.X - bx
				dy := snap.Y - by
				dz := snap.Z - bz
				dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
				ivx := dx * 0.1
				ivy := dy*0.1 + math.Sqrt(dist)*0.08
				ivz := dz * 0.1
				s.entities.Update(itemEntID, func(e *world.Entity) {
					e.VelocityX = clampVelocityComponent(ivx)
					e.VelocityY = clampVelocityComponent(ivy)
					e.VelocityZ = clampVelocityComponent(ivz)
				})
				cp := itemEnt.ChunkPos()
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEntityMotion,
					EncodeSetEntityMotionPacket(itemEntID, ivx, ivy, ivz))
			}
			// Grant fishing advancement criterion if applicable.
			s.triggerFishingCatchAdvancements(session, drop.Stack)
		}
		// Vanilla FishingHook.retrieve spawns ExperienceOrb(level, player.X, player.Y+0.5, player.Z, 1 + rng.nextInt(6)).
		xpAmount := int32(1)
		if rng != nil {
			xpAmount = int32(1 + rng.Intn(6))
		}
		s.SpawnExperienceOrbsInDimension(dim, snap.X, snap.Y+0.5, snap.Z, xpAmount)
		reelDamage = FishingWearCatch
	} else if onGround {
		reelDamage = FishingWearGround
	}

	if reelDamage > 0 && !rodStack.IsEmpty() {
		s.DamagePlayerItemWithRand(session, rodSlot, reelDamage, rng)
	}

	cpX := int32(int64(math.Floor(bx)) >> 4)
	cpZ := int32(int64(math.Floor(bz)) >> 4)
	s.BroadcastDimensionChunk(dim, cpX, cpZ, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.fishing_bobber.retrieve", SoundSourceNeutral, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))
	s.BroadcastDimensionChunk(dim, cpX, cpZ, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{bobberCopy.EntityID}))

	return reelDamage
}

func (s *Server) pullHookedEntityTowardPlayer(hookedID int32, bobberX, bobberY, bobberZ, playerX, playerY, playerZ float64, dim string) {
	// Vanilla FishingHook.pullEntity:
	// Vec3 delta = new Vec3(owner.getX() - this.getX(), owner.getY() - this.getY(), owner.getZ() - this.getZ()).scale(0.1);
	// entity.setDeltaMovement(entity.getDeltaMovement().add(delta));
	pullVX := (playerX - bobberX) * 0.1
	pullVY := (playerY - bobberY) * 0.1
	pullVZ := (playerZ - bobberZ) * 0.1

	var newVX, newVY, newVZ float64
	var cx, cz int32
	s.entities.Update(hookedID, func(e *world.Entity) {
		curVX := float64(e.VelocityX) / 8000.0
		curVY := float64(e.VelocityY) / 8000.0
		curVZ := float64(e.VelocityZ) / 8000.0
		newVX = curVX + pullVX
		newVY = curVY + pullVY
		newVZ = curVZ + pullVZ
		e.VelocityX = clampVelocityComponent(newVX)
		e.VelocityY = clampVelocityComponent(newVY)
		e.VelocityZ = clampVelocityComponent(newVZ)
		// Advance position along the pull vector so server-side entity coordinates reflect the pull immediately.
		e.X += pullVX * 3.0
		if pullVY > 0 {
			e.Y += pullVY
		}
		e.Z += pullVZ * 3.0
		pos := e.ChunkPos()
		cx, cz = pos.X, pos.Z
	})
	s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlaySetEntityMotion,
		EncodeSetEntityMotionPacket(hookedID, newVX, newVY, newVZ))
}

func (s *Server) pullHookedPlayerTowardPlayer(target *PlayerSession, bobberX, bobberY, bobberZ, playerX, playerY, playerZ float64, dim string) {
	if s == nil || target == nil {
		return
	}
	pullVX := (playerX - bobberX) * 0.1
	pullVY := (playerY - bobberY) * 0.1
	pullVZ := (playerZ - bobberZ) * 0.1

	tSnap := target.Snapshot()
	newX := tSnap.X + pullVX*3.0
	newY := tSnap.Y
	if pullVY > 0 {
		newY += pullVY
	}
	newZ := tSnap.Z + pullVZ*3.0
	s.SetPlayerTransform(target, newX, newY, newZ, tSnap.Yaw, tSnap.Pitch, false)

	cx := int32(int64(math.Floor(newX)) >> 4)
	cz := int32(int64(math.Floor(newZ)) >> 4)
	pkt := EncodeSetEntityMotionPacket(target.EntityID, pullVX, pullVY, pullVZ)
	if target.send != nil {
		_ = target.send(protocol.PlaySetEntityMotion, pkt)
	}
	s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlaySetEntityMotion, pkt)
}

func (s *Server) findOnlinePlayerByEntityID(entityID int32, dim string) *PlayerSession {
	if s == nil || entityID <= 0 {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	s.playersMu.RLock()
	defer s.playersMu.RUnlock()
	for _, sess := range s.players {
		if sess == nil || sess.IsDead() || sess.EntityID != entityID {
			continue
		}
		snap := sess.Snapshot()
		if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
			continue
		}
		if world.NormalizeDimension(snap.Dimension) == normDim {
			return sess
		}
	}
	return nil
}

func (s *Server) triggerFishingCatchAdvancements(session *PlayerSession, caught world.ItemStack) {
	if s == nil || session == nil || caught.IsEmpty() {
		return
	}
	s.TriggerItemAcquired(session, caught)
	name := strings.TrimPrefix(canonicalResourceName(caught.ResolvedID()), "minecraft:")
	switch name {
	case "cod", "salmon", "tropical_fish", "pufferfish":
		s.GrantCriterion(session, "minecraft:husbandry/fishy_business", name)
	}
}

// DespawnPlayerFishingBobber removes any active fishing bobber belonging to session.
func (s *Server) DespawnPlayerFishingBobber(session *PlayerSession) bool {
	if s == nil || session == nil {
		return false
	}
	ft := s.fishingTracker()
	ft.mu.Lock()
	st := ft.byOwner[session]
	if st == nil {
		ft.mu.Unlock()
		return false
	}
	delete(ft.byOwner, session)
	delete(ft.byEntity, st.EntityID)
	entityID := st.EntityID
	dim := st.Dimension
	ft.mu.Unlock()

	var cx, cz int32
	if ent := s.entities.Get(entityID); ent != nil {
		pos := ent.ChunkPos()
		cx, cz = pos.X, pos.Z
	}
	s.entities.Remove(entityID)
	s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{entityID}))
	return true
}

// ComputeInitialFishingWaitTicks calculates the initial timeUntilLured countdown
// in ticks matching vanilla 26.1.2 FishingHook:
// Mth.nextInt(rng, 100, 600) - lureLevel * 100, clamped to >= 1.
func ComputeInitialFishingWaitTicks(rng *rand.Rand, lureLevel int) int {
	if lureLevel < 0 {
		lureLevel = 0
	}
	base := 350
	if rng != nil {
		base = FishingMinWaitTicks + rng.Intn(FishingMaxWaitTicks-FishingMinWaitTicks+1)
	}
	wait := base - lureLevel*FishingLureTicksPerLevel
	if wait < 1 {
		wait = 1
	}
	return wait
}

// EffectiveFishingCategoryWeights computes the non-negative integer loot pool
// weights for (fish, junk, treasure) given luck (Luck of the Sea level + player
// luck) and whether the bobber is in open water, matching vanilla
// LootPoolSingletonContainer.getWeight(float luck):
// max(floor(weight + quality * luck), 0).
func EffectiveFishingCategoryWeights(luck float32, inOpenWater bool) (fishWeight, junkWeight, treasureWeight int) {
	fishWeight = int(math.Floor(float64(FishingWeightFish) + float64(FishingQualityFish)*float64(luck)))
	if fishWeight < 0 {
		fishWeight = 0
	}
	junkWeight = int(math.Floor(float64(FishingWeightJunk) + float64(FishingQualityJunk)*float64(luck)))
	if junkWeight < 0 {
		junkWeight = 0
	}
	if inOpenWater {
		treasureWeight = int(math.Floor(float64(FishingWeightTreasure) + float64(FishingQualityTreasure)*float64(luck)))
		if treasureWeight < 0 {
			treasureWeight = 0
		}
	}
	return fishWeight, junkWeight, treasureWeight
}

// RollFishingLoot rolls a single catch from the vanilla 26.1.2 fishing loot
// tables (`gameplay/fishing.json` -> `fish.json`, `junk.json`, or `treasure.json`).
// Treasure can only be rolled when inOpenWater is true.
func RollFishingLoot(rng *rand.Rand, luck float32, inOpenWater, isJungle bool) FishingLootDrop {
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	fishW, junkW, treasureW := EffectiveFishingCategoryWeights(luck, inOpenWater)
	totalW := junkW + treasureW + fishW
	if totalW <= 0 {
		fishW = 1
		totalW = 1
	}

	roll := rng.Intn(totalW)
	var category FishingLootCategory
	var entries []fishingLootEntry
	if roll < junkW {
		category = FishingCategoryJunk
		entries = vanillaFishingJunkTable
	} else if roll < junkW+treasureW {
		category = FishingCategoryTreasure
		entries = vanillaFishingTreasureTable
	} else {
		category = FishingCategoryFish
		entries = vanillaFishingFishTable
	}

	entry := pickFishingLootEntry(rng, entries, isJungle)
	return materializeFishingLootEntry(rng, category, entry)
}

func pickFishingLootEntry(rng *rand.Rand, entries []fishingLootEntry, isJungle bool) fishingLootEntry {
	total := 0
	for _, e := range entries {
		if e.JungleOnly && !isJungle {
			continue
		}
		if e.Weight > 0 {
			total += e.Weight
		}
	}
	if total <= 0 {
		return entries[0]
	}
	r := rng.Intn(total)
	for _, e := range entries {
		if e.JungleOnly && !isJungle {
			continue
		}
		if e.Weight <= 0 {
			continue
		}
		r -= e.Weight
		if r < 0 {
			return e
		}
	}
	return entries[0]
}

func materializeFishingLootEntry(rng *rand.Rand, category FishingLootCategory, entry fishingLootEntry) FishingLootDrop {
	itemName := entry.ItemName
	count := entry.Count
	if count <= 0 {
		count = 1
	}
	var meta ItemMeta

	if entry.MaxDamageFraction > 0 {
		if maxDur := MaxItemDurability(itemName); maxDur > 0 {
			// Vanilla set_damage sets remaining durability fraction; wear is maxDur * (1 - remaining).
			dmgFrac := rng.Float64() * entry.MaxDamageFraction
			dmg := int32(math.Floor(float64(maxDur) * dmgFrac))
			if dmg < 1 {
				dmg = 1
			}
			if dmg >= maxDur {
				dmg = maxDur - 1
			}
			meta.Damage = dmg
		}
	}

	if entry.EnchantWithLevels > 0 {
		enchs := selectTableEnchantments(itemName, entry.EnchantWithLevels, ItemEnchantability(itemName), rng)
		for _, inst := range enchs {
			meta.SetEnchantment(inst.ID, inst.Level)
		}
		if itemName == "minecraft:book" {
			itemName = "minecraft:enchanted_book"
			if !meta.HasEnchantments() {
				meta.SetEnchantment("minecraft:unbreaking", 3)
			}
		} else if !meta.HasEnchantments() {
			if itemName == "minecraft:fishing_rod" {
				meta.SetEnchantment("minecraft:lure", 2)
			} else if itemName == "minecraft:bow" {
				meta.SetEnchantment("minecraft:power", 3)
			}
		}
	}

	stack := world.NewNamedItemStack(itemName, count)
	return FishingLootDrop{
		Category: category,
		Stack:    stack,
		Meta:     meta,
	}
}

// IsOpenWaterFishingAt checks the 5x4x5 volume around (x, y, z) (dx in -2..2,
// dy in -1..2, dz in -2..2) matching vanilla 26.1.2 FishingHook.calculateOpenWater:
//   - Each horizontal 5x1x5 layer must consist uniformly of either water blocks
//     (INSIDE_WATER) or air/lily_pad blocks (ABOVE_WATER) with no solid/other blocks.
//   - Layer dy = -1 must be INSIDE_WATER, and no INSIDE_WATER layer may appear
//     above an ABOVE_WATER layer.
func (s *Server) IsOpenWaterFishingAt(x, y, z int) bool {
	if s == nil || s.chunks == nil {
		return true
	}
	return CalculateOpenWaterAt(s.chunks, x, y, z)
}

type openWaterLayerType int8

const (
	openWaterAbove   openWaterLayerType = 0
	openWaterInside  openWaterLayerType = 1
	openWaterInvalid openWaterLayerType = 2
)

// CalculateOpenWaterAt evaluates vanilla 26.1.2's 5x4x5 open-water check on chunks.
func CalculateOpenWaterAt(chunks *world.Cache, x, y, z int) bool {
	if chunks == nil {
		return true
	}
	prev := openWaterInvalid
	for dy := -1; dy <= 2; dy++ {
		layer := classifyOpenWaterLayer(chunks, x, y+dy, z)
		switch layer {
		case openWaterInvalid:
			return false
		case openWaterAbove:
			if prev == openWaterInvalid {
				return false
			}
		case openWaterInside:
			if prev == openWaterAbove {
				return false
			}
		}
		prev = layer
	}
	return true
}

func classifyOpenWaterLayer(chunks *world.Cache, centerX, y, centerZ int) openWaterLayerType {
	firstSet := false
	var uniform openWaterLayerType
	for dz := -2; dz <= 2; dz++ {
		for dx := -2; dx <= 2; dx++ {
			t := classifyOpenWaterBlock(chunks.GetBlock(centerX+dx, y, centerZ+dz))
			if t == openWaterInvalid {
				return openWaterInvalid
			}
			if !firstSet {
				firstSet = true
				uniform = t
			} else if t != uniform {
				return openWaterInvalid
			}
		}
	}
	if !firstSet {
		return openWaterInvalid
	}
	return uniform
}

func classifyOpenWaterBlock(state uint16) openWaterLayerType {
	if state == world.StateAir {
		return openWaterAbove
	}
	if state == world.StateWater {
		return openWaterInside
	}
	switch world.BlockName(state) {
	case "", "minecraft:air", "minecraft:cave_air", "minecraft:void_air", "minecraft:lily_pad":
		return openWaterAbove
	case "minecraft:water":
		if src, ok := world.BlockStateIDByProperties("minecraft:water", map[string]string{"level": "0"}); ok && state != src && state != world.StateWater {
			// Flowing water (level > 0) is not source water and invalidates open water.
			return openWaterInvalid
		}
		return openWaterInside
	case "minecraft:bubble_column":
		return openWaterInside
	default:
		return openWaterInvalid
	}
}

// CanSeeSkyAt reports whether the column at (x, y, z) has an unobstructed view
// of the sky (either sky light == 15 or no non-air blocks above y up to the surface).
func (s *Server) CanSeeSkyAt(x, y, z int) bool {
	if s == nil || s.chunks == nil {
		return true
	}
	skyLight, _ := s.LightAt(x, y, z)
	if skyLight >= 15 {
		return true
	}
	topY, ok := s.chunks.SafeSpawnY(x, z)
	if !ok {
		return true
	}
	return y >= topY
}

// ComputeFishingBiteSpeed returns the per-tick countdown speed (0, 1, or 2) for
// a fishing bobber's wait/approach timers matching vanilla 26.1.2 FishingHook.catchingFish:
//   - Base speed is 1.
//   - When raining and exposed to open sky, 25% chance to increment speed (+1 -> average 1.25 ticks/tick).
//   - When obstructed from open sky, 50% chance to decrement speed (-1 -> average 0.5 ticks/tick).
func ComputeFishingBiteSpeed(raining, openSky bool, rng *rand.Rand) int {
	speed := 1
	if rng == nil {
		if raining && openSky {
			return 2
		}
		if !openSky {
			return 0
		}
		return 1
	}
	if raining && openSky && rng.Float32() < 0.25 {
		speed++
	}
	if !openSky && rng.Float32() < 0.5 {
		speed--
	}
	return speed
}

// TickFishingBobbers advances all active fishing bobbers by one server tick (50ms).
func (s *Server) TickFishingBobbers(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	ft := s.fishingTracker()
	ft.mu.Lock()
	if len(ft.byEntity) == 0 {
		ft.mu.Unlock()
		return
	}
	if rng == nil {
		rng = ft.rng
	}
	ids := make([]int32, 0, len(ft.byEntity))
	for id := range ft.byEntity {
		ids = append(ids, id)
	}
	ft.mu.Unlock()

	for _, id := range ids {
		s.tickSingleFishingBobber(id, rng)
	}
}

func (s *Server) tickSingleFishingBobber(entityID int32, rng *rand.Rand) {
	ft := s.fishingTracker()
	ft.mu.Lock()
	st := ft.byEntity[entityID]
	if st == nil {
		ft.mu.Unlock()
		return
	}
	owner := st.Owner
	ft.mu.Unlock()

	ent := s.entities.Get(entityID)
	if ent == nil || owner == nil || s.shouldStopFishing(owner, ent, st.Dimension) {
		if owner != nil {
			s.DespawnPlayerFishingBobber(owner)
		} else {
			ft.mu.Lock()
			delete(ft.byEntity, entityID)
			ft.mu.Unlock()
			s.entities.Remove(entityID)
		}
		return
	}

	ft.mu.Lock()
	st = ft.byEntity[entityID]
	if st == nil {
		ft.mu.Unlock()
		return
	}

	bx := int(math.Floor(ent.X))
	by := int(math.Floor(ent.Y))
	bz := int(math.Floor(ent.Z))
	inWater := s.isWaterBlockAt(bx, by, bz)

	var (
		nextX             = ent.X
		nextY             = ent.Y
		nextZ             = ent.Z
		nextOnGround      = ent.OnGround
		broadcastData     bool
		broadcastBiteEvt  bool
		newHookedEntityID = st.HookedEntityID
		newBiting         = st.Biting
		dim               = st.Dimension
	)

	switch st.State {
	case FishHookStateHookedInEntity:
		if hooked := s.entities.Get(st.HookedEntityID); hooked != nil && hooked.EffectiveDimension() == dim {
			nextX = hooked.X
			nextY = hooked.Y + 0.5
			nextZ = hooked.Z
			st.VX, st.VY, st.VZ = 0, 0, 0
			nextOnGround = false
		} else if hookedPlayer := s.findOnlinePlayerByEntityID(st.HookedEntityID, dim); hookedPlayer != nil {
			pSnap := hookedPlayer.Snapshot()
			nextX = pSnap.X
			nextY = pSnap.Y + 0.8
			nextZ = pSnap.Z
			st.VX, st.VY, st.VZ = 0, 0, 0
			nextOnGround = false
		} else {
			st.HookedEntityID = 0
			newHookedEntityID = 0
			st.State = FishHookStateFlying
			broadcastData = true
		}

	case FishHookStateFlying:
		candX := ent.X + st.VX
		candY := ent.Y + st.VY
		candZ := ent.Z + st.VZ
		if hitID := s.findHookableEntityAlongSegment(ent.ID, st.OwnerEntityID, dim, ent.X, ent.Y, ent.Z, candX, candY, candZ); hitID != 0 {
			st.HookedEntityID = hitID
			newHookedEntityID = hitID
			st.State = FishHookStateHookedInEntity
			st.VX, st.VY, st.VZ = 0, 0, 0
			broadcastData = true
			break
		}
		if inWater {
			st.State = FishHookStateBobbing
			st.VX *= 0.3
			st.VY = 0.0
			st.VZ *= 0.3
			nextOnGround = false
			st.OnGround = false
			break
		}
		candBX := int(math.Floor(candX))
		candBY := int(math.Floor(candY))
		candBZ := int(math.Floor(candZ))
		if s.isWaterBlockAt(candBX, candBY, candBZ) {
			nextX, nextY, nextZ = candX, candY, candZ
			st.State = FishHookStateBobbing
			st.VX *= 0.3
			st.VY = 0.0
			st.VZ *= 0.3
			nextOnGround = false
			st.OnGround = false
		} else if s.chunks != nil && isSolidGroundBlock(s.chunks.GetBlock(candBX, candBY, candBZ)) {
			nextOnGround = true
			st.OnGround = true
			st.VX, st.VY, st.VZ = 0, 0, 0
		} else {
			nextX, nextY, nextZ = candX, candY, candZ
			nextOnGround = false
			st.OnGround = false
			st.VY -= 0.03
			st.VX *= 0.92
			st.VY *= 0.92
			st.VZ *= 0.92
		}

	case FishHookStateBobbing:
		st.VX *= 0.9
		st.VZ *= 0.9
		if math.Abs(st.VX) < 1e-4 {
			st.VX = 0
		}
		if math.Abs(st.VZ) < 1e-4 {
			st.VZ = 0
		}
		if !inWater {
			st.OutOfWaterTicks++
			st.VY -= 0.03
			nextY = ent.Y + st.VY
			if st.OutOfWaterTicks >= 10 {
				st.State = FishHookStateFlying
			}
			break
		}
		st.OutOfWaterTicks = 0
		nextOnGround = false
		st.OnGround = false

		// Drift horizontally if destination block remains water.
		if st.VX != 0 || st.VZ != 0 {
			driftX := ent.X + st.VX
			driftZ := ent.Z + st.VZ
			if s.isWaterBlockAt(int(math.Floor(driftX)), by, int(math.Floor(driftZ))) {
				nextX = driftX
				nextZ = driftZ
				bx = int(math.Floor(nextX))
				bz = int(math.Floor(nextZ))
			} else {
				st.VX, st.VZ = 0, 0
			}
		}

		// Buoyancy: if submerged under another water block above, float upward into the surface block.
		if s.isWaterBlockAt(bx, by+1, bz) {
			st.VY = (st.VY + 0.08) * 0.6
			nextY = ent.Y + st.VY
			by = int(math.Floor(nextY))
		}

		st.OpenWater = s.IsOpenWaterFishingAt(bx, by, bz)

		prevBiting := st.Biting
		s.stepCatchingFishLocked(st, bx, by, bz, rng)
		newBiting = st.Biting
		if newBiting != prevBiting {
			broadcastData = true
			if newBiting {
				broadcastBiteEvt = true
				st.VY = -0.2
			}
		} else if !newBiting && !s.isWaterBlockAt(bx, by+1, bz) {
			st.VY = 0
		}
	}

	vxFixed := clampVelocityComponent(st.VX)
	vyFixed := clampVelocityComponent(st.VY)
	vzFixed := clampVelocityComponent(st.VZ)
	ft.mu.Unlock()

	s.entities.Update(entityID, func(e *world.Entity) {
		e.X = nextX
		e.Y = nextY
		e.Z = nextZ
		e.VelocityX = vxFixed
		e.VelocityY = vyFixed
		e.VelocityZ = vzFixed
		e.OnGround = nextOnGround
	})

	if broadcastData || broadcastBiteEvt {
		cpX := int32(int64(math.Floor(nextX)) >> 4)
		cpZ := int32(int64(math.Floor(nextZ)) >> 4)
		if broadcastData {
			s.BroadcastDimensionChunk(dim, cpX, cpZ, PlaySetEntityDataCB,
				EncodeFishingBobberDataPacket(entityID, newHookedEntityID, newBiting))
		}
		if broadcastBiteEvt {
			s.BroadcastDimensionChunk(dim, cpX, cpZ, protocol.PlayEntityEvent,
				EncodeEntityEventPacket(entityID, EntityEventFishingHookReel))
			s.BroadcastDimensionChunk(dim, cpX, cpZ, PlaySoundPacketID,
				EncodeSoundPacket("minecraft:entity.fishing_bobber.splash", SoundSourceNeutral, nextX, nextY, nextZ, 0.25, 1.0, 0))
		}
	}
}

// stepCatchingFishLocked implements vanilla 26.1.2 FishingHook.catchingFish.
// Caller must hold ft.mu.
func (s *Server) stepCatchingFishLocked(st *FishingBobberState, bx, by, bz int, rng *rand.Rand) {
	openSky := s.CanSeeSkyAt(bx, by+1, bz)
	speed := ComputeFishingBiteSpeed(s.IsRaining(), openSky, rng)

	if st.NibbleTicks > 0 {
		st.NibbleTicks--
		if st.NibbleTicks <= 0 {
			st.TimeUntilLured = 0
			st.TimeUntilHooked = 0
			st.Biting = false
		}
		return
	}

	if st.TimeUntilHooked > 0 {
		st.TimeUntilHooked -= speed
		if st.TimeUntilHooked <= 0 {
			st.NibbleTicks = FishingMinNibbleTicks + rng.Intn(FishingMaxNibbleTicks-FishingMinNibbleTicks+1)
			st.Biting = true
		} else {
			st.FishAngle += float32((rng.Float64() - rng.Float64()) * 9.188)
		}
		return
	}

	if st.TimeUntilLured > 0 {
		st.TimeUntilLured -= speed
		if st.TimeUntilLured <= 0 {
			st.FishAngle = rng.Float32() * 360.0
			st.TimeUntilHooked = FishingMinHookApproach + rng.Intn(FishingMaxHookApproach-FishingMinHookApproach+1)
		}
		return
	}

	st.TimeUntilLured = ComputeInitialFishingWaitTicks(rng, st.LureLevel)
}

func (s *Server) shouldStopFishing(owner *PlayerSession, bobber *world.Entity, dim string) bool {
	if owner == nil || bobber == nil || owner.IsDead() {
		return true
	}
	// Check that owner is still online in s.players.
	s.playersMu.RLock()
	onlineCount := len(s.players)
	cur, isOnline := s.players[owner.Profile.UUID]
	s.playersMu.RUnlock()
	if (onlineCount > 0 || owner.EntityID > 0) && (!isOnline || cur != owner) {
		return true
	}

	snap := owner.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return true
	}
	if world.NormalizeDimension(snap.Dimension) != world.NormalizeDimension(dim) {
		return true
	}
	mainSlot := world.HotbarContainerOffset + int(snap.SelectedSlot)
	mainItem := owner.InventorySlot(mainSlot)
	offItem := owner.InventorySlot(45)
	holdingRod := (!mainItem.IsEmpty() && canonicalResourceName(mainItem.ResolvedID()) == FishingRodItemID) ||
		(!offItem.IsEmpty() && canonicalResourceName(offItem.ResolvedID()) == FishingRodItemID)
	if !holdingRod {
		return true
	}

	dx := bobber.X - snap.X
	dy := bobber.Y - snap.Y
	dz := bobber.Z - snap.Z
	return dx*dx+dy*dy+dz*dz > FishingMaxDistanceSquared
}

func (s *Server) isWaterBlockAt(x, y, z int) bool {
	if s == nil || s.chunks == nil {
		return false
	}
	state := s.chunks.GetBlock(x, y, z)
	return state == world.StateWater || world.BlockName(state) == "minecraft:water" || world.BlockName(state) == "minecraft:bubble_column"
}

func (s *Server) findHookableEntityAlongSegment(bobberID, ownerEntityID int32, dim string, x0, y0, z0, x1, y1, z1 float64) int32 {
	if id := s.findHookableEntityAt(bobberID, ownerEntityID, dim, x0, y0, z0); id != 0 {
		return id
	}
	mx := (x0 + x1) * 0.5
	my := (y0 + y1) * 0.5
	mz := (z0 + z1) * 0.5
	if id := s.findHookableEntityAt(bobberID, ownerEntityID, dim, mx, my, mz); id != 0 {
		return id
	}
	return s.findHookableEntityAt(bobberID, ownerEntityID, dim, x1, y1, z1)
}

func (s *Server) findHookableEntityAt(bobberID, ownerEntityID int32, dim string, x, y, z float64) int32 {
	normDim := world.NormalizeDimension(dim)
	cx := int32(int64(math.Floor(x)) >> 4)
	cz := int32(int64(math.Floor(z)) >> 4)
	nearby := s.entities.AppendInChunkRange(nil, cx, cz, 1)
	for i := range nearby {
		other := &nearby[i]
		if other.ID == bobberID || other.ID == ownerEntityID ||
			other.TypeName == EntityTypeFishingBobber || other.IsExperienceOrb() || IsHangingEntity(other.TypeName) {
			continue
		}
		if other.EffectiveDimension() != normDim {
			continue
		}
		dx := other.X - x
		dy := (other.Y + 0.5) - y
		dz := other.Z - z
		if dx*dx+dz*dz <= 0.64 && math.Abs(dy) <= 1.0 {
			return other.ID
		}
	}
	// Also check online players other than ownerEntityID in the same dimension.
	s.playersMu.RLock()
	defer s.playersMu.RUnlock()
	for _, sess := range s.players {
		if sess == nil || sess.IsDead() || sess.EntityID <= 0 || sess.EntityID == ownerEntityID {
			continue
		}
		pSnap := sess.Snapshot()
		if pSnap.GameMode == GameModeSpectator || pSnap.Health <= 0 || world.NormalizeDimension(pSnap.Dimension) != normDim {
			continue
		}
		dx := pSnap.X - x
		dy := (pSnap.Y + 0.9) - y
		dz := pSnap.Z - z
		if dx*dx+dz*dz <= 0.64 && math.Abs(dy) <= 1.1 {
			return sess.EntityID
		}
	}
	return 0
}

// EncodeFishingBobberAddEntityPacket encodes a clientbound add_entity (0x01)
// packet for a minecraft:fishing_bobber entity, setting the trailing Data
// VarInt field to the owner player's EntityID as required by vanilla 26.1.2
// FishingHook.getAddEntityPacket / recreateFromPacket.
func (s *Server) EncodeFishingBobberAddEntityPacket(dst []byte, e *world.Entity) []byte {
	var ownerID int32
	if s != nil && e != nil {
		ft := s.fishingTracker()
		ft.mu.Lock()
		if st := ft.byEntity[e.ID]; st != nil {
			ownerID = st.OwnerEntityID
		}
		ft.mu.Unlock()
	}
	if ownerID <= 0 && e != nil {
		ownerID = e.ID
	}
	dst = protocol.AppendVarInt(dst, e.ID)
	dst = append(dst, e.UUID[:]...)
	dst = protocol.AppendVarInt(dst, int32(e.TypeID))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(e.X))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(e.Y))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(e.Z))
	if e.VelocityX == 0 && e.VelocityY == 0 && e.VelocityZ == 0 {
		dst = append(dst, 0)
	} else {
		var w protocol.Writer
		w.LPVec3(
			float64(e.VelocityX)/8000.0,
			float64(e.VelocityY)/8000.0,
			float64(e.VelocityZ)/8000.0,
		)
		dst = append(dst, w.Bytes()...)
	}
	dst = append(dst,
		encodeAngleByte(e.Pitch),
		encodeAngleByte(e.Yaw),
		encodeAngleByte(e.HeadYaw),
	)
	dst = protocol.AppendVarInt(dst, ownerID)
	return dst
}

// EncodeFishingBobberDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating a fishing bobber's DATA_HOOKED_ENTITY (index 8, INT: hookedEntityID+1
// or 0) and DATA_BITING (index 9, BOOLEAN).
func EncodeFishingBobberDataPacket(entityID, hookedEntityID int32, biting bool) []byte {
	var hookedVal int32
	if hookedEntityID > 0 {
		hookedVal = hookedEntityID + 1
	}
	w := protocol.NewWriter(16)
	w.VarInt(entityID)
	w.Byte(EntityDataFishingHookedEntityID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(hookedVal)
	w.Byte(EntityDataFishingBitingID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(biting)
	w.Byte(0xFF)
	return w.Bytes()
}

// FishingBobberEntityDataPacket returns the current ClientboundSetEntityDataPacket
// payload for entityID if it is an active fishing bobber.
func (s *Server) FishingBobberEntityDataPacket(entityID int32) []byte {
	st := s.FishingBobberByEntityID(entityID)
	if st == nil {
		return nil
	}
	return EncodeFishingBobberDataPacket(entityID, st.HookedEntityID, st.Biting)
}

func clampVelocityComponent(v float64) int16 {
	scaled := math.Round(v * 8000.0)
	if scaled > math.MaxInt16 {
		return math.MaxInt16
	}
	if scaled < math.MinInt16 {
		return math.MinInt16
	}
	return int16(scaled)
}
