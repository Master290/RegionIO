package server

import (
	"errors"
	"math"
	"math/rand"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Tadpole (minecraft:tadpole) and Bucket of Tadpole (minecraft:tadpole_bucket)
// constants and sounds for Minecraft 26.1.2 (protocol 775).
const (
	MobTypeTadpole      = "minecraft:tadpole"
	EntityTypeTadpoleID = 130

	DefaultTadpoleMaxHealth    float32 = 6.0
	TadpoleSwimSpeed           float64 = 0.08
	TadpoleGrowthTicks         int     = -24000 // 20 minutes (-24000 ticks) until growing into an adult Frog
	TadpoleMaxOutOfWaterTicks  int     = 200    // 10 seconds out of water before taking dry-out damage

	TadpoleBucketItemID = "minecraft:tadpole_bucket"
	WaterBucketItemID   = "minecraft:water_bucket"
	EmptyBucketItemID   = "minecraft:bucket"

	// SynchedEntityData index for Tadpole/AbstractFish FROM_BUCKET (index 16, BOOLEAN serializer = 8).
	EntityDataTadpoleFromBucketID byte = 16

	// Canonical Tadpole sounds.
	SoundTadpoleDeath       = "minecraft:entity.tadpole.death"
	SoundTadpoleFlop        = "minecraft:entity.tadpole.flop"
	SoundTadpoleGrowUp      = "minecraft:entity.tadpole.grow_up"
	SoundTadpoleHurt        = "minecraft:entity.tadpole.hurt"
	SoundBucketFillTadpole  = "minecraft:item.bucket.fill_tadpole"
	SoundBucketEmptyTadpole = "minecraft:item.bucket.empty_tadpole"
)

// TadpoleBehaviorState holds the server-side growth, biome override, and bucket
// state for a `minecraft:tadpole` entity.
type TadpoleBehaviorState struct {
	EntityID        int32
	AgeTicks        int // Starts at -24000; grows into a Frog when reaching >= 0
	FromBucket      bool
	OutOfWaterTicks int
	BiomeOverride   string
}

type serverTadpoleManager struct {
	mu          sync.RWMutex
	tadpoles    map[int32]*TadpoleBehaviorState
	bucketedAge map[*PlayerSession]int
}

var tadpoleManagers sync.Map // map[*Server]*serverTadpoleManager

func (s *Server) tadpoleManager() *serverTadpoleManager {
	if s == nil {
		return &serverTadpoleManager{
			tadpoles:    make(map[int32]*TadpoleBehaviorState),
			bucketedAge: make(map[*PlayerSession]int),
		}
	}
	if v, ok := tadpoleManagers.Load(s); ok {
		return v.(*serverTadpoleManager)
	}
	mgr := &serverTadpoleManager{
		tadpoles:    make(map[int32]*TadpoleBehaviorState),
		bucketedAge: make(map[*PlayerSession]int),
	}
	actual, _ := tadpoleManagers.LoadOrStore(s, mgr)
	return actual.(*serverTadpoleManager)
}

func (s *Server) ensureTadpoleState(entityID int32) *TadpoleBehaviorState {
	if s == nil || entityID <= 0 {
		return nil
	}
	mgr := s.tadpoleManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.tadpoles[entityID]
	if !ok || st == nil {
		st = &TadpoleBehaviorState{
			EntityID: entityID,
			AgeTicks: TadpoleGrowthTicks,
		}
		mgr.tadpoles[entityID] = st
	}
	return st
}

// GetTadpoleState returns a snapshot of the TadpoleBehaviorState for entityID.
func (s *Server) GetTadpoleState(entityID int32) (TadpoleBehaviorState, bool) {
	if s == nil || entityID <= 0 {
		return TadpoleBehaviorState{}, false
	}
	mgr := s.tadpoleManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	st, ok := mgr.tadpoles[entityID]
	if !ok || st == nil {
		return TadpoleBehaviorState{}, false
	}
	return *st, true
}

// TadpoleAge returns the current age in ticks of a Tadpole (starts at -24000,
// transforms into an adult Frog at >= 0).
func (s *Server) TadpoleAge(entityID int32) int {
	if st, ok := s.GetTadpoleState(entityID); ok {
		return st.AgeTicks
	}
	return TadpoleGrowthTicks
}

// SetTadpoleAge sets the age in ticks of a Tadpole.
func (s *Server) SetTadpoleAge(entityID int32, ageTicks int) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeTadpole {
		return false
	}
	st := s.ensureTadpoleState(entityID)
	mgr := s.tadpoleManager()
	mgr.mu.Lock()
	st.AgeTicks = ageTicks
	mgr.mu.Unlock()
	return true
}

// SetTadpoleBiome sets an explicit biome identifier for a Tadpole to use when
// maturing into a Frog (useful for biome-variant growth tests).
func (s *Server) SetTadpoleBiome(entityID int32, biomeName string) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	st := s.ensureTadpoleState(entityID)
	if st == nil {
		return false
	}
	mgr := s.tadpoleManager()
	mgr.mu.Lock()
	st.BiomeOverride = biomeName
	mgr.mu.Unlock()
	return true
}

// SpawnTadpole spawns a `minecraft:tadpole` at (x, y, z) in dimension with
// initial Age = -24000 ticks and broadcasts its creation to nearby players.
func (s *Server) SpawnTadpole(dimension string, x, y, z float64) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil server or entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeTadpole)
	if typeID < 0 {
		typeID = EntityTypeTadpoleID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeTadpole,
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
	mem.health = DefaultTadpoleMaxHealth
	mem.lastHealth = DefaultTadpoleMaxHealth
	tr.mu.Unlock()

	_ = s.ensureTadpoleState(id)
	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
	}
	return id, nil
}

// GrowTadpoleIntoFrog matures the `minecraft:tadpole` with entityID into an
// adult `minecraft:frog` whose variant (`cold`, `temperate`, or `warm`) matches
// biomeName (or the Tadpole's BiomeOverride / dimension).
func (s *Server) GrowTadpoleIntoFrog(entityID int32, biomeName string) (frogID int32, variant int32, ok bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return 0, 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeTadpole {
		return 0, 0, false
	}
	mgr := s.tadpoleManager()
	mgr.mu.Lock()
	st := mgr.tadpoles[entityID]
	if biomeName == "" && st != nil && st.BiomeOverride != "" {
		biomeName = st.BiomeOverride
	}
	delete(mgr.tadpoles, entityID)
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	x, y, z := ent.X, ent.Y, ent.Z
	cp := ent.ChunkPos()

	if biomeName != "" {
		variant = ResolveFrogVariantForBiome(biomeName)
	} else if dim == world.DimensionNether {
		variant = FrogVariantWarm
	} else if dim == world.DimensionEnd {
		variant = FrogVariantCold
	} else {
		variant = FrogVariantTemperate
	}

	s.entities.Remove(entityID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{entityID}))

	tr := s.entityTracker()
	tr.mu.Lock()
	delete(tr.mobStates, entityID)
	tr.mu.Unlock()

	frogID, err := s.SpawnFrog(dim, x, y, z, variant)
	if err != nil || frogID <= 0 {
		return 0, 0, false
	}
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundTadpoleGrowUp, SoundSourceNeutral, x, y, z, 1.0, 1.0, 0))
	return frogID, variant, true
}

// FeedTadpole feeds a `minecraft:tadpole` with `minecraft:slime_ball`,
// accelerating its remaining growth time by 10% (`(-ageTicks)/10`), broadcasting
// happy villager particles, and transforming it into an adult Frog if age >= 0.
func (s *Server) FeedTadpole(session *PlayerSession, entityID int32) (fed bool, newAge int, grownFrogID int32) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false, 0, 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeTadpole {
		return false, 0, 0
	}
	st := s.ensureTadpoleState(entityID)
	mgr := s.tadpoleManager()
	mgr.mu.Lock()
	delta := (-st.AgeTicks) / 10
	if delta < 1 {
		delta = 1
	}
	st.AgeTicks += delta
	if st.AgeTicks > 0 {
		st.AgeTicks = 0
	}
	newAge = st.AgeTicks
	biome := st.BiomeOverride
	mgr.mu.Unlock()

	if session != nil {
		consumeOneHeldItemInSurvival(session)
	}
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(entityID, EntityEventVillagerHappy))

	if newAge >= 0 {
		frogID, _, ok := s.GrowTadpoleIntoFrog(entityID, biome)
		if ok {
			return true, 0, frogID
		}
	}
	return true, newAge, 0
}

// CatchTadpoleInBucket scoops up the `minecraft:tadpole` with entityID when
// session right-clicks it while holding `minecraft:water_bucket`, replacing the
// water bucket with `minecraft:tadpole_bucket` and preserving the Tadpole's age.
func (s *Server) CatchTadpoleInBucket(session *PlayerSession, entityID int32) bool {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeTadpole {
		return false
	}
	held := session.HeldItem()
	if held.IsEmpty() || canonicalResourceName(held.ResolvedID()) != WaterBucketItemID {
		return false
	}
	tadpoleBucket := world.NewNamedItemStack(TadpoleBucketItemID, 1)
	if tadpoleBucket.IsEmpty() {
		return false
	}

	st := s.ensureTadpoleState(entityID)
	mgr := s.tadpoleManager()
	mgr.mu.Lock()
	savedAge := st.AgeTicks
	mgr.bucketedAge[session] = savedAge
	delete(mgr.tadpoles, entityID)
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	x, y, z := ent.X, ent.Y, ent.Z
	s.entities.Remove(entityID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{entityID}))

	slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
	if snap.GameMode != GameModeCreative {
		if held.Count <= 1 {
			session.SetInventorySlot(slotIdx, tadpoleBucket)
		} else {
			session.SetInventorySlot(slotIdx, world.NewItemStack(held.ResolvedItemID(), held.Count-1))
			_, _ = session.AddItemToInventory(tadpoleBucket)
		}
	} else {
		_, _ = session.AddItemToInventory(tadpoleBucket)
	}

	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundBucketFillTadpole, SoundSourceNeutral, x, y, z, 1.0, 1.0, 0))
	return true
}

// ReleaseTadpoleFromBucket empties a `minecraft:tadpole_bucket` held by session
// at block (x, y, z) in dimension: places water, spawns a `minecraft:tadpole`
// with its preserved age and FromBucket=true, and replaces the item with
// `minecraft:bucket` in survival mode.
// (Can be called directly by Agent B's bucket block-placement handler.)
func (s *Server) ReleaseTadpoleFromBucket(session *PlayerSession, dimension string, x, y, z int) (tadpoleID int32, ok bool) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return 0, false
	}
	snap := session.Snapshot()
	held := session.HeldItem()
	if held.IsEmpty() || canonicalResourceName(held.ResolvedID()) != TadpoleBucketItemID {
		return 0, false
	}
	normDim := world.NormalizeDimension(dimension)
	if cache := s.DimensionChunks(normDim); cache != nil && y >= world.MinY && y < world.MinY+world.WorldHeight {
		cache.SetBlock(x, y, z, world.StateWater)
		s.broadcastMobBlockUpdate(normDim, x, y, z, world.StateWater)
	}

	tid, err := s.SpawnTadpole(normDim, float64(x)+0.5, float64(y), float64(z)+0.5)
	if err != nil || tid <= 0 {
		return 0, false
	}
	mgr := s.tadpoleManager()
	mgr.mu.Lock()
	savedAge, hasSaved := mgr.bucketedAge[session]
	delete(mgr.bucketedAge, session)
	if st := mgr.tadpoles[tid]; st != nil {
		st.FromBucket = true
		if hasSaved {
			st.AgeTicks = savedAge
		}
	}
	mgr.mu.Unlock()

	if snap.GameMode != GameModeCreative {
		slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
		emptyBucket := world.NewNamedItemStack(EmptyBucketItemID, 1)
		session.SetInventorySlot(slotIdx, emptyBucket)
	}

	cx := int32(x >> 4)
	cz := int32(z >> 4)
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundBucketEmptyTadpole, SoundSourceNeutral, float64(x)+0.5, float64(y), float64(z)+0.5, 1.0, 1.0, 0))
	return tid, true
}

// InteractWithTadpole handles a player right-clicking a `minecraft:tadpole`:
//   - Holding `minecraft:water_bucket` -> scoops up into `minecraft:tadpole_bucket`
//   - Holding `minecraft:slime_ball`   -> accelerates growth by 10%.
func (s *Server) InteractWithTadpole(session *PlayerSession, entityID int32) (handled bool, grownFrogID int32) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false, 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeTadpole {
		return false, 0
	}
	dx := ent.X - snap.X
	dy := ent.Y - snap.Y
	dz := ent.Z - snap.Z
	if snap.GameMode != GameModeCreative && (dx*dx+dy*dy+dz*dz) > MaxMeleeAttackReach*MaxMeleeAttackReach {
		return false, 0
	}
	held := session.HeldItem()
	if held.IsEmpty() {
		return false, 0
	}
	switch canonicalResourceName(held.ResolvedID()) {
	case WaterBucketItemID:
		if s.CatchTadpoleInBucket(session, entityID) {
			return true, 0
		}
	case SlimeBallItemID:
		fed, _, frogID := s.FeedTadpole(session, entityID)
		return fed, frogID
	}
	return false, 0
}

// TickTadpoles advances all `minecraft:tadpole` entities by one server tick:
// increments AgeTicks toward 0 and matures them into biome-appropriate Frogs.
func (s *Server) TickTadpoles(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	allEnts := s.entities.AppendAll(nil)
	mgr := s.tadpoleManager()

	for i := range allEnts {
		ent := &allEnts[i]
		if canonicalResourceName(ent.TypeName) != MobTypeTadpole {
			continue
		}
		st := s.ensureTadpoleState(ent.ID)
		if st == nil {
			continue
		}
		mgr.mu.Lock()
		st.AgeTicks++
		readyToGrow := st.AgeTicks >= 0
		biome := st.BiomeOverride
		mgr.mu.Unlock()

		if readyToGrow {
			_, _, _ = s.GrowTadpoleIntoFrog(ent.ID, biome)
			continue
		}

		// Simple water-seeking / flopping check.
		dim := ent.EffectiveDimension()
		cache := s.DimensionChunks(dim)
		if cache != nil {
			bx := int(math.Floor(ent.X))
			by := int(math.Floor(ent.Y))
			bz := int(math.Floor(ent.Z))
			inWater := cache.GetBlock(bx, by, bz) == world.StateWater || cache.GetBlock(bx, by-1, bz) == world.StateWater
			mgr.mu.Lock()
			if inWater {
				st.OutOfWaterTicks = 0
			} else {
				st.OutOfWaterTicks++
			}
			mgr.mu.Unlock()
		}
	}
}
