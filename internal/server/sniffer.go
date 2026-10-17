package server

import (
	"errors"
	"math"
	"math/rand"
	"strconv"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Sniffer (minecraft:sniffer) and Sniffer Egg (minecraft:sniffer_egg) constants,
// states, SynchedEntityData indices, and sounds for Minecraft 26.1.2 (protocol 775).
const (
	MobTypeSniffer      = "minecraft:sniffer"
	EntityTypeSnifferID = 119

	DefaultSnifferMaxHealth float32 = 14.0
	SnifferWanderSpeed      float64 = 0.08
	SnifferSearchSpeed      float64 = 0.095
	SnifferHealAmount       float32 = 2.0

	// Snifflet (baby sniffer) takes 40 minutes (-48000 ticks, twice normal baby duration) to mature.
	SniffletGrowthTicks = -48000

	// Sniffer scenting, searching, and digging cycle constants (in ticks at 20 TPS).
	SnifferSniffCooldownTicks     = 9600 // 8 minutes between digs
	SnifferScentingDurationTicks  = 160  // 8.0s scenting animation
	SnifferSearchingMaxTicks      = 400  // 20.0s max walk time to target dig block
	SnifferDiggingDurationTicks   = 160  // 8.0s digging animation
	SnifferDigSeedDropTick        = 120  // 6.0s into digging when seed pops out
	SnifferRisingDurationTicks    = 40   // 2.0s rising/happy animation after dig
	SnifferSearchHorizontalRadius = 20
	SnifferSearchVerticalRadius   = 3
	SnifferExploredMemoryLimit    = 20

	// Sniffer Egg block & hatching constants.
	SnifferEggBlockName              = "minecraft:sniffer_egg"
	SnifferEggItemID                 = "minecraft:sniffer_egg"
	TorchflowerSeedsItemID           = "minecraft:torchflower_seeds"
	PitcherPodItemID                 = "minecraft:pitcher_pod"
	MossBlockName                    = "minecraft:moss_block"
	SnifferEggMaxHatchStage          = 2
	SnifferEggNormalStageTicks       = 8000  // ~6m40s per stage -> 24000 ticks (~20m) total
	SnifferEggMossBoostedStageTicks  = 4000  // ~3m20s per stage -> 12000 ticks (~10m) total on moss_block
	SnifferEggNormalTotalHatchTicks  = 24000 // 3 stages * 8000 ticks
	SnifferEggMossBoostedTotalTicks  = 12000 // 3 stages * 4000 ticks

	// SynchedEntityData indices and serializers for Sniffer in 26.1.2 (protocol 775).
	EntityDataSnifferStateID          byte  = 17 // Sniffer.DATA_STATE (SNIFFER_STATE serializer = 26)
	EntityDataSnifferDropSeedAtTickID byte  = 18 // Sniffer.DATA_DROP_SEED_AT_TICK (INT serializer = 1)
	EntityDataSerializerSnifferState  int32 = 26

	// Canonical Sniffer & Sniffer Egg sounds.
	SoundSnifferIdle         = "minecraft:entity.sniffer.idle"
	SoundSnifferStep         = "minecraft:entity.sniffer.step"
	SoundSnifferEat          = "minecraft:entity.sniffer.eat"
	SoundSnifferHurt         = "minecraft:entity.sniffer.hurt"
	SoundSnifferDeath        = "minecraft:entity.sniffer.death"
	SoundSnifferScenting     = "minecraft:entity.sniffer.scenting"
	SoundSnifferSniffing     = "minecraft:entity.sniffer.sniffing"
	SoundSnifferSearching    = "minecraft:entity.sniffer.searching"
	SoundSnifferDiggingStart = "minecraft:entity.sniffer.digging_start"
	SoundSnifferDigging      = "minecraft:entity.sniffer.digging"
	SoundSnifferDropSeed     = "minecraft:entity.sniffer.drop_seed"
	SoundSnifferHappy        = "minecraft:entity.sniffer.happy"
	SoundSnifferEggPlop      = "minecraft:block.sniffer_egg.plop"
	SoundSnifferEggCrack     = "minecraft:block.sniffer_egg.crack"
	SoundSnifferEggHatch     = "minecraft:block.sniffer_egg.hatch"
)

// SnifferState represents net.minecraft.world.entity.animal.sniffer.Sniffer$State ordinals.
type SnifferState int32

const (
	SnifferStateIdling       SnifferState = 0
	SnifferStateFeelingHappy SnifferState = 1
	SnifferStateScenting     SnifferState = 2
	SnifferStateSniffing     SnifferState = 3
	SnifferStateSearching    SnifferState = 4
	SnifferStateDigging      SnifferState = 5
	SnifferStateRising       SnifferState = 6
)

// SnifferBehaviorState holds the server-side AI state for a Sniffer entity.
type SnifferBehaviorState struct {
	EntityID          int32
	State             SnifferState
	StateTicks        int
	DigCooldownTicks  int
	DropSeedAtTick    int32
	SeedDropped       bool
	TargetDigPos      [3]int
	HasTargetDigPos   bool
	ExploredPositions [][3]int
	LastDroppedSeed   string
}

type snifferEggPosKey struct {
	dim     string
	x, y, z int
}

// SnifferEggTrackerState tracks the hatching progress of a placed minecraft:sniffer_egg block.
type SnifferEggTrackerState struct {
	Dimension    string
	X, Y, Z      int
	HatchStage   int // 0, 1, or 2
	TicksInStage int
}

type serverSnifferManager struct {
	mu       sync.RWMutex
	sniffers map[int32]*SnifferBehaviorState
	eggs     map[snifferEggPosKey]*SnifferEggTrackerState
}

var snifferManagers sync.Map // map[*Server]*serverSnifferManager

func (s *Server) snifferManager() *serverSnifferManager {
	if v, ok := snifferManagers.Load(s); ok {
		return v.(*serverSnifferManager)
	}
	actual, _ := snifferManagers.LoadOrStore(s, &serverSnifferManager{
		sniffers: make(map[int32]*SnifferBehaviorState),
		eggs:     make(map[snifferEggPosKey]*SnifferEggTrackerState),
	})
	return actual.(*serverSnifferManager)
}

// IsSnifferDiggableBlock reports whether blockName is in the vanilla 26.1.2
// #minecraft:sniffer_diggable_block tag.
func IsSnifferDiggableBlock(blockName string) bool {
	switch canonicalResourceName(blockName) {
	case "minecraft:dirt",
		"minecraft:grass_block",
		"minecraft:podzol",
		"minecraft:coarse_dirt",
		"minecraft:rooted_dirt",
		"minecraft:moss_block",
		"minecraft:pale_moss_block",
		"minecraft:mud",
		"minecraft:muddy_mangrove_roots":
		return true
	default:
		return false
	}
}

// IsSnifferDiggableState reports whether block state is a sniffer-diggable block.
func IsSnifferDiggableState(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	return IsSnifferDiggableBlock(world.BlockName(state))
}

// IsSnifferEggHatchBoostBlock reports whether a block beneath a sniffer_egg
// doubles its hatching speed (minecraft:moss_block in vanilla 26.1.2).
func IsSnifferEggHatchBoostBlock(blockName string) bool {
	return canonicalResourceName(blockName) == MossBlockName
}

// SnifferEggHatchStageTicks returns the number of ticks required for one crack
// stage transition of a sniffer_egg (4000 ticks on moss_block, 8000 otherwise).
func SnifferEggHatchStageTicks(onMoss bool) int {
	if onMoss {
		return SnifferEggMossBoostedStageTicks
	}
	return SnifferEggNormalStageTicks
}

// RollSnifferDigLoot rolls the vanilla 26.1.2 gameplay/sniffer_digging loot table,
// returning either 1 minecraft:torchflower_seeds or 1 minecraft:pitcher_pod (equal weight).
func RollSnifferDigLoot(rng *rand.Rand) world.ItemStack {
	itemID := TorchflowerSeedsItemID
	if rng != nil && rng.Intn(2) == 1 {
		itemID = PitcherPodItemID
	}
	return world.NewNamedItemStack(itemID, 1)
}

// EncodeSnifferEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating Sniffer.DATA_STATE (index 17) and Sniffer.DATA_DROP_SEED_AT_TICK (index 18).
func EncodeSnifferEntityDataPacket(entityID int32, state SnifferState, dropSeedAtTick int32) []byte {
	w := protocol.NewWriter(20)
	w.VarInt(entityID)
	w.Byte(EntityDataSnifferStateID)
	w.VarInt(EntityDataSerializerSnifferState)
	w.VarInt(int32(state))
	w.Byte(EntityDataSnifferDropSeedAtTickID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(dropSeedAtTick)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeSnifferEntityDataPacket decodes a packet produced by EncodeSnifferEntityDataPacket.
func DecodeSnifferEntityDataPacket(r *protocol.Reader) (entityID int32, state SnifferState, dropSeedAtTick int32, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, 0, 0, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, 0, 0, err
		}
		if idx == 0xFF {
			break
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, 0, 0, err
		}
		switch idx {
		case EntityDataSnifferStateID:
			if ser != EntityDataSerializerSnifferState {
				return 0, 0, 0, errors.New("sniffer: unexpected state serializer")
			}
			v, err := r.VarInt()
			if err != nil {
				return 0, 0, 0, err
			}
			state = SnifferState(v)
		case EntityDataSnifferDropSeedAtTickID:
			if ser != EntityDataSerializerInt {
				return 0, 0, 0, errors.New("sniffer: unexpected dropSeedAtTick serializer")
			}
			dropSeedAtTick, err = r.VarInt()
			if err != nil {
				return 0, 0, 0, err
			}
		default:
			return 0, 0, 0, errors.New("sniffer: unexpected metadata index")
		}
	}
	return entityID, state, dropSeedAtTick, nil
}

func (s *Server) ensureSnifferState(entityID int32) *SnifferBehaviorState {
	if s == nil || entityID <= 0 {
		return nil
	}
	mgr := s.snifferManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.sniffers[entityID]
	if !ok || st == nil {
		st = &SnifferBehaviorState{
			EntityID:         entityID,
			State:            SnifferStateIdling,
			DigCooldownTicks: 400, // Initial short cooldown after spawn before first scenting
		}
		mgr.sniffers[entityID] = st
	}
	return st
}

// GetSnifferState returns a snapshot of the SnifferBehaviorState for entityID.
func (s *Server) GetSnifferState(entityID int32) (SnifferBehaviorState, bool) {
	if s == nil {
		return SnifferBehaviorState{}, false
	}
	mgr := s.snifferManager()
	mgr.mu.RLock()
	st, ok := mgr.sniffers[entityID]
	var out SnifferBehaviorState
	if ok && st != nil {
		out = *st
		if len(st.ExploredPositions) > 0 {
			out.ExploredPositions = make([][3]int, len(st.ExploredPositions))
			copy(out.ExploredPositions, st.ExploredPositions)
		}
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// SetSnifferDigCooldown sets the remaining cooldown ticks before the Sniffer
// can begin scenting/digging again.
func (s *Server) SetSnifferDigCooldown(entityID int32, ticks int) {
	if s == nil {
		return
	}
	if ticks < 0 {
		ticks = 0
	}
	st := s.ensureSnifferState(entityID)
	if st == nil {
		return
	}
	mgr := s.snifferManager()
	mgr.mu.Lock()
	st.DigCooldownTicks = ticks
	mgr.mu.Unlock()
}

// SpawnSniffer spawns an adult or baby (snifflet) Sniffer at (x, y, z) in dimension.
func (s *Server) SpawnSniffer(dimension string, x, y, z float64, baby bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	id := s.entities.Add(&world.Entity{
		TypeID:    ResolveEntityTypeID(MobTypeSniffer),
		TypeName:  MobTypeSniffer,
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
	mem.health = DefaultSnifferMaxHealth
	mem.lastHealth = DefaultSnifferMaxHealth
	if baby {
		mem.ageTicks = SniffletGrowthTicks
	}
	tr.mu.Unlock()

	st := s.ensureSnifferState(id)
	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		if baby {
			s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeAgeableMobDataPacket(id, true))
		}
		if st != nil {
			s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeSnifferEntityDataPacket(id, st.State, st.DropSeedAtTick))
		}
	}
	return id, nil
}

// SpawnSnifflet spawns a baby Sniffer (snifflet, age = -48000 ticks) at (x, y, z) in dimension.
func (s *Server) SpawnSnifflet(dimension string, x, y, z float64) (int32, error) {
	return s.SpawnSniffer(dimension, x, y, z, true)
}

func (s *Server) broadcastSnifferState(ent *world.Entity, state SnifferState, dropSeedAtTick int32) {
	if s == nil || ent == nil {
		return
	}
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeSnifferEntityDataPacket(ent.ID, state, dropSeedAtTick))
}

func isSnifferPositionExplored(explored [][3]int, pos [3]int) bool {
	for _, p := range explored {
		if p == pos {
			return true
		}
	}
	return false
}

func recordSnifferExploredPosition(st *SnifferBehaviorState, pos [3]int) {
	if st == nil {
		return
	}
	for _, p := range st.ExploredPositions {
		if p == pos {
			return
		}
	}
	if len(st.ExploredPositions) >= SnifferExploredMemoryLimit {
		copy(st.ExploredPositions, st.ExploredPositions[1:])
		st.ExploredPositions[len(st.ExploredPositions)-1] = pos
	} else {
		st.ExploredPositions = append(st.ExploredPositions, pos)
	}
}

// FindSnifferDiggableTarget searches around (x, feetY, z) for a diggable ground
// block with passable space above it that has not been dug in the Sniffer's
// recent memory (last 20 positions).
func (s *Server) FindSnifferDiggableTarget(dim string, x float64, feetY int, z float64, explored [][3]int) ([3]int, bool) {
	if s == nil {
		return [3]int{}, false
	}
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return [3]int{}, false
	}
	bx0 := int(math.Floor(x))
	bz0 := int(math.Floor(z))
	groundY0 := feetY - 1

	// First check the block directly beneath the Sniffer if not yet explored.
	directPos := [3]int{bx0, groundY0, bz0}
	if groundY0 > world.MinY+1 && IsSnifferDiggableState(cache.GetBlock(bx0, groundY0, bz0)) &&
		isColumnPassableAt(cache, bx0, groundY0+1, bz0) && !isSnifferPositionExplored(explored, directPos) {
		return directPos, true
	}

	bestDistSq := math.MaxFloat64
	var bestPos [3]int
	found := false

	for r := 1; r <= SnifferSearchHorizontalRadius; r++ {
		for dx := -r; dx <= r; dx++ {
			for dz := -r; dz <= r; dz++ {
				if absInt(dx) != r && absInt(dz) != r {
					continue
				}
				bx := bx0 + dx
				bz := bz0 + dz
				for dy := SnifferSearchVerticalRadius; dy >= -SnifferSearchVerticalRadius; dy-- {
					by := groundY0 + dy
					if by <= world.MinY+1 || by >= world.MinY+world.WorldHeight-3 {
						continue
					}
					cand := [3]int{bx, by, bz}
					if isSnifferPositionExplored(explored, cand) {
						continue
					}
					if !IsSnifferDiggableState(cache.GetBlock(bx, by, bz)) {
						continue
					}
					if !isColumnPassableAt(cache, bx, by+1, bz) {
						continue
					}
					distSq := float64(dx*dx + dy*dy + dz*dz)
					if distSq < bestDistSq {
						bestDistSq = distSq
						bestPos = cand
						found = true
					}
				}
			}
		}
		if found {
			return bestPos, true
		}
	}
	return [3]int{}, false
}

// TriggerSnifferScenting starts the Sniffer's scenting/searching/digging cycle
// if the Sniffer is an adult, not on cooldown, and has a diggable block nearby.
func (s *Server) TriggerSnifferScenting(entityID int32, rng *rand.Rand) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeSniffer || s.IsMobBaby(entityID) {
		return false
	}
	st := s.ensureSnifferState(entityID)
	if st == nil {
		return false
	}
	mgr := s.snifferManager()
	mgr.mu.RLock()
	explored := make([][3]int, len(st.ExploredPositions))
	copy(explored, st.ExploredPositions)
	mgr.mu.RUnlock()

	feetY := int(math.Floor(ent.Y + 0.01))
	targetPos, ok := s.FindSnifferDiggableTarget(ent.EffectiveDimension(), ent.X, feetY, ent.Z, explored)
	if !ok {
		return false
	}

	mgr.mu.Lock()
	st.State = SnifferStateScenting
	st.StateTicks = SnifferScentingDurationTicks
	st.TargetDigPos = targetPos
	st.HasTargetDigPos = true
	st.SeedDropped = false
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.broadcastSnifferState(ent, SnifferStateScenting, 0)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundSnifferScenting, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true
}

// CompleteSnifferDig drops a torchflower_seeds or pitcher_pod item at the
// Sniffer's position, records the dug block in the Sniffer's explored memory,
// and transitions the Sniffer to RISING/IDLING with a full dig cooldown.
func (s *Server) CompleteSnifferDig(entityID int32, rng *rand.Rand) (world.ItemStack, bool) {
	if s == nil || s.entities == nil {
		return world.ItemStack{}, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeSniffer || s.IsMobBaby(entityID) {
		return world.ItemStack{}, false
	}
	st := s.ensureSnifferState(entityID)
	if st == nil {
		return world.ItemStack{}, false
	}
	feetY := int(math.Floor(ent.Y + 0.01))
	digPos := [3]int{int(math.Floor(ent.X)), feetY - 1, int(math.Floor(ent.Z))}

	loot := RollSnifferDigLoot(rng)
	mgr := s.snifferManager()
	mgr.mu.Lock()
	if st.HasTargetDigPos {
		digPos = st.TargetDigPos
	}
	recordSnifferExploredPosition(st, digPos)
	st.SeedDropped = true
	st.LastDroppedSeed = loot.ResolvedID()
	st.State = SnifferStateRising
	st.StateTicks = SnifferRisingDurationTicks
	st.DigCooldownTicks = SnifferSniffCooldownTicks
	st.HasTargetDigPos = false
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	if !loot.IsEmpty() {
		s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.35, ent.Z, loot)
	}
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundSnifferDropSeed, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	s.broadcastSnifferState(ent, SnifferStateRising, 0)
	return loot, true
}

// TickSniffers advances all Sniffer entities by one tick, managing scenting,
// searching movement toward a diggable block, digging, seed dropping, and rising.
func (s *Server) TickSniffers(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		ent := &allEnts[i]
		if canonicalResourceName(ent.TypeName) != MobTypeSniffer {
			continue
		}
		if s.IsMobBaby(ent.ID) {
			continue
		}
		st := s.ensureSnifferState(ent.ID)
		if st == nil {
			continue
		}
		dim := ent.EffectiveDimension()
		cache := s.DimensionChunks(dim)
		feetY := int(math.Floor(ent.Y + 0.01))
		cp := ent.ChunkPos()

		mgr := s.snifferManager()
		mgr.mu.Lock()
		if st.DigCooldownTicks > 0 && st.State == SnifferStateIdling {
			st.DigCooldownTicks--
		}
		curState := st.State
		curTicks := st.StateTicks
		cooldown := st.DigCooldownTicks
		targetPos := st.TargetDigPos
		hasTarget := st.HasTargetDigPos
		seedDropped := st.SeedDropped
		mgr.mu.Unlock()

		switch curState {
		case SnifferStateIdling:
			if cooldown <= 0 {
				s.TriggerSnifferScenting(ent.ID, rng)
			}
		case SnifferStateScenting:
			curTicks--
			if curTicks <= 0 {
				mgr.mu.Lock()
				st.State = SnifferStateSearching
				st.StateTicks = SnifferSearchingMaxTicks
				mgr.mu.Unlock()
				s.broadcastSnifferState(ent, SnifferStateSearching, 0)
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket(SoundSnifferSearching, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			} else {
				mgr.mu.Lock()
				st.StateTicks = curTicks
				mgr.mu.Unlock()
			}
		case SnifferStateSearching:
			if !hasTarget {
				mgr.mu.Lock()
				st.State = SnifferStateIdling
				st.StateTicks = 0
				mgr.mu.Unlock()
				s.broadcastSnifferState(ent, SnifferStateIdling, 0)
				continue
			}
			goalX := float64(targetPos[0]) + 0.5
			goalZ := float64(targetPos[2]) + 0.5
			dist := math.Hypot(goalX-ent.X, goalZ-ent.Z)
			if dist <= 1.25 {
				// Reached diggable target block: check it is still diggable and begin digging.
				if cache == nil || IsSnifferDiggableState(cache.GetBlock(targetPos[0], targetPos[1], targetPos[2])) {
					dropTick := int32(SnifferDigSeedDropTick)
					mgr.mu.Lock()
					st.State = SnifferStateDigging
					st.StateTicks = SnifferDiggingDurationTicks
					st.DropSeedAtTick = dropTick
					st.SeedDropped = false
					mgr.mu.Unlock()
					s.broadcastSnifferState(ent, SnifferStateDigging, dropTick)
					s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
						EncodeSoundPacket(SoundSnifferDiggingStart, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
					continue
				}
			}
			curTicks--
			if curTicks <= 0 {
				mgr.mu.Lock()
				st.State = SnifferStateIdling
				st.StateTicks = 0
				st.HasTargetDigPos = false
				st.DigCooldownTicks = 200
				mgr.mu.Unlock()
				s.broadcastSnifferState(ent, SnifferStateIdling, 0)
				continue
			}
			mgr.mu.Lock()
			st.StateTicks = curTicks
			mgr.mu.Unlock()
			// Step toward target dig block.
			dx := goalX - ent.X
			dz := goalZ - ent.Z
			if dist > 1e-4 {
				step := math.Min(SnifferSearchSpeed, dist)
				nx := ent.X + (dx/dist)*step
				nz := ent.Z + (dz/dist)*step
				ny := ent.Y
				if cache != nil {
					if stepX, stepY, stepZ, ok := tryApplyHorizontalStep(cache, ent.X, feetY, ent.Z, nx, nz); ok {
						nx, ny, nz = stepX, float64(stepY), stepZ
					}
				}
				yaw := yawToward(dx, dz)
				s.entities.Update(ent.ID, func(e *world.Entity) {
					e.X, e.Y, e.Z = nx, ny, nz
					e.Yaw = yaw
					e.HeadYaw = yaw
				})
			}
		case SnifferStateDigging:
			curTicks--
			elapsed := SnifferDiggingDurationTicks - curTicks
			if !seedDropped && elapsed >= SnifferDigSeedDropTick {
				loot := RollSnifferDigLoot(rng)
				mgr.mu.Lock()
				recordSnifferExploredPosition(st, targetPos)
				st.SeedDropped = true
				st.LastDroppedSeed = loot.ResolvedID()
				mgr.mu.Unlock()
				if !loot.IsEmpty() {
					s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.35, ent.Z, loot)
				}
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket(SoundSnifferDropSeed, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			}
			if curTicks <= 0 {
				mgr.mu.Lock()
				st.State = SnifferStateRising
				st.StateTicks = SnifferRisingDurationTicks
				st.DigCooldownTicks = SnifferSniffCooldownTicks
				st.HasTargetDigPos = false
				mgr.mu.Unlock()
				s.broadcastSnifferState(ent, SnifferStateRising, 0)
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket(SoundSnifferHappy, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			} else {
				mgr.mu.Lock()
				st.StateTicks = curTicks
				mgr.mu.Unlock()
			}
		case SnifferStateRising, SnifferStateFeelingHappy:
			curTicks--
			if curTicks <= 0 {
				mgr.mu.Lock()
				st.State = SnifferStateIdling
				st.StateTicks = 0
				mgr.mu.Unlock()
				s.broadcastSnifferState(ent, SnifferStateIdling, 0)
			} else {
				mgr.mu.Lock()
				st.StateTicks = curTicks
				mgr.mu.Unlock()
			}
		}
	}
}

// InteractWithSniffer handles a player right-clicking a Sniffer with torchflower_seeds:
//   - If the Sniffer is a baby (Snifflet), speeds up its growth by 10%.
//   - If damaged, heals 2.0 HP (1 heart).
//   - If an adult at full health (or after healing) and not on breeding cooldown,
//     enters love mode; when another Sniffer in love is within AnimalBreedRange,
//     drops a minecraft:sniffer_egg item entity and spawns 1..7 XP orbs!
func (s *Server) InteractWithSniffer(session *PlayerSession, entityID int32) (handled bool, eggItemEntityID int32) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false, 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeSniffer {
		return false, 0
	}
	dx := ent.X - snap.X
	dy := ent.Y - snap.Y
	dz := ent.Z - snap.Z
	if snap.GameMode != GameModeCreative && (dx*dx+dy*dy+dz*dz) > MaxMeleeAttackReach*MaxMeleeAttackReach {
		return false, 0
	}
	held := session.HeldItem()
	if held.IsEmpty() || canonicalResourceName(held.ResolvedID()) != TorchflowerSeedsItemID {
		return false, 0
	}

	if s.IsMobBaby(entityID) {
		fed, _ := s.FeedBabyMob(session, entityID, TorchflowerSeedsItemID)
		return fed, 0
	}

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()

	// Heal injured adult Sniffer by 2.0 HP if below max health.
	curHP := s.MobHealth(entityID)
	if curHP > 0 && curHP < DefaultSnifferMaxHealth {
		newHP := curHP + SnifferHealAmount
		if newHP > DefaultSnifferMaxHealth {
			newHP = DefaultSnifferMaxHealth
		}
		s.SetMobHealth(entityID, newHP)
		if s.MobBreedCooldown(entityID) > 0 || s.MobInLoveTicks(entityID) > 0 {
			consumeOneHeldItemInSurvival(session)
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundSnifferEat, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			return true, 0
		}
	}

	if s.MobBreedCooldown(entityID) > 0 || s.MobInLoveTicks(entityID) > 0 {
		return false, 0
	}
	if !s.SetMobInLove(entityID, AnimalLoveDurationTicks, session) {
		return false, 0
	}
	consumeOneHeldItemInSurvival(session)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundSnifferEat, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))

	// Check if another adult Sniffer in love is within AnimalBreedRange; if so,
	// complete mating by dropping a minecraft:sniffer_egg item entity!
	eggID, _ := s.tryMateSnifferWithNearby(ent, session)
	return true, eggID
}

func (s *Server) tryMateSnifferWithNearby(ent *world.Entity, cause *PlayerSession) (eggItemID int32, mated bool) {
	if s == nil || s.entities == nil || ent == nil {
		return 0, false
	}
	dim := ent.EffectiveDimension()
	maxDistSq := AnimalBreedRange * AnimalBreedRange

	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStates[ent.ID]
	if mem == nil || mem.ageTicks < 0 || mem.inLoveTicks <= 0 || mem.breedCooldown > 0 {
		tr.mu.Unlock()
		return 0, false
	}

	var mateEnt *world.Entity
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		other := &allEnts[i]
		if other.ID == ent.ID || other.EffectiveDimension() != dim || canonicalResourceName(other.TypeName) != MobTypeSniffer {
			continue
		}
		dx := other.X - ent.X
		dy := other.Y - ent.Y
		dz := other.Z - ent.Z
		if dx*dx+dy*dy+dz*dz > maxDistSq {
			continue
		}
		otherMem := tr.mobStates[other.ID]
		if otherMem == nil || otherMem.ageTicks < 0 || otherMem.inLoveTicks <= 0 || otherMem.breedCooldown > 0 {
			continue
		}
		mateEnt = other
		mem.inLoveTicks = 0
		mem.breedCooldown = AnimalBreedCooldownTicks
		mem.loveCause = nil
		otherMem.inLoveTicks = 0
		otherMem.breedCooldown = AnimalBreedCooldownTicks
		otherMem.loveCause = nil
		break
	}
	tr.mu.Unlock()

	if mateEnt == nil {
		return 0, false
	}

	midX := (ent.X + mateEnt.X) * 0.5
	midY := math.Max(ent.Y, mateEnt.Y)
	midZ := (ent.Z + mateEnt.Z) * 0.5
	eggStack := world.NewNamedItemStack(SnifferEggItemID, 1)
	if !eggStack.IsEmpty() {
		eggItemID, _ = s.SpawnItemEntityInDimension(dim, midX, midY+0.35, midZ, eggStack)
	}
	s.SpawnExperienceOrbsInDimension(dim, midX, midY+0.25, midZ, 4)
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundSnifferEggPlop, SoundSourceNeutral, midX, midY, midZ, 1.0, 1.0, 0))
	if cause != nil {
		s.GrantCriterion(cause, "minecraft:husbandry/breed_an_animal", "bred")
	}
	return eggItemID, true
}

// --- Sniffer Egg Block Placement & Hatching ---

// PlaceSnifferEgg places a minecraft:sniffer_egg block (hatch=0) at (x, y, z)
// in dimension and registers it for hatching ticks.
func (s *Server) PlaceSnifferEgg(dimension string, x, y, z int) bool {
	if s == nil {
		return false
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	if cache == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return false
	}
	eggState, ok := world.BlockStateByName(SnifferEggBlockName, map[string]string{"hatch": "0"})
	if !ok {
		return false
	}
	cache.SetBlock(x, y, z, eggState)
	s.broadcastMobBlockUpdate(normDim, x, y, z, eggState)

	mgr := s.snifferManager()
	mgr.mu.Lock()
	mgr.eggs[snifferEggPosKey{dim: normDim, x: x, y: y, z: z}] = &SnifferEggTrackerState{
		Dimension:    normDim,
		X:            x,
		Y:            y,
		Z:            z,
		HatchStage:   0,
		TicksInStage: 0,
	}
	mgr.mu.Unlock()
	return true
}

func (s *Server) broadcastMobBlockUpdate(dim string, x, y, z int, state uint16) {
	if s == nil {
		return
	}
	w := protocol.NewWriter(12)
	w.Position(x, y, z)
	w.VarInt(int32(state))
	s.BroadcastDimensionChunk(world.NormalizeDimension(dim), int32(x>>4), int32(z>>4), protocol.PlayBlockUpdate, w.Bytes())
}

// SnifferEggStateAt returns the current hatching tracker state for a placed
// sniffer_egg at (x, y, z) in dimension.
func (s *Server) SnifferEggStateAt(dimension string, x, y, z int) (SnifferEggTrackerState, bool) {
	if s == nil {
		return SnifferEggTrackerState{}, false
	}
	normDim := world.NormalizeDimension(dimension)
	key := snifferEggPosKey{dim: normDim, x: x, y: y, z: z}
	mgr := s.snifferManager()
	mgr.mu.RLock()
	st, ok := mgr.eggs[key]
	var out SnifferEggTrackerState
	if ok && st != nil {
		out = *st
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// SetSnifferEggTicksInStage sets the elapsed ticks in the current hatch stage
// for a tracked sniffer_egg at (x, y, z).
func (s *Server) SetSnifferEggTicksInStage(dimension string, x, y, z, ticks int) bool {
	if s == nil {
		return false
	}
	normDim := world.NormalizeDimension(dimension)
	key := snifferEggPosKey{dim: normDim, x: x, y: y, z: z}
	mgr := s.snifferManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.eggs[key]
	if !ok || st == nil {
		return false
	}
	st.TicksInStage = ticks
	return true
}

// IsSnifferEggOnMossAt reports whether the block directly beneath (x, y, z) in
// dimension is minecraft:moss_block (which doubles sniffer_egg hatching speed).
func (s *Server) IsSnifferEggOnMossAt(dimension string, x, y, z int) bool {
	if s == nil || y <= world.MinY {
		return false
	}
	cache := s.DimensionChunks(world.NormalizeDimension(dimension))
	if cache == nil {
		return false
	}
	below := cache.GetBlock(x, y-1, z)
	return IsSnifferEggHatchBoostBlock(world.BlockName(below))
}

// TickSnifferEggs advances all tracked minecraft:sniffer_egg blocks by one tick:
//   - Checks if the block at (x, y, z) is still a sniffer_egg.
//   - Uses 4000 ticks per stage on minecraft:moss_block (~10m total) vs 8000
//     ticks per stage on other blocks (~20m total).
//   - Advances hatch=0 -> hatch=1 -> hatch=2, and after stage 2 completes,
//     breaks the egg block and spawns a baby Sniffer (Snifflet)!
func (s *Server) TickSnifferEggs(rng *rand.Rand) {
	if s == nil {
		return
	}
	mgr := s.snifferManager()
	mgr.mu.RLock()
	if len(mgr.eggs) == 0 {
		mgr.mu.RUnlock()
		return
	}
	keys := make([]snifferEggPosKey, 0, len(mgr.eggs))
	for k := range mgr.eggs {
		keys = append(keys, k)
	}
	mgr.mu.RUnlock()

	for _, key := range keys {
		cache := s.DimensionChunks(key.dim)
		if cache == nil {
			continue
		}
		curBlock := cache.GetBlock(key.x, key.y, key.z)
		if world.BlockName(curBlock) != SnifferEggBlockName {
			mgr.mu.Lock()
			delete(mgr.eggs, key)
			mgr.mu.Unlock()
			continue
		}
		onMoss := s.IsSnifferEggOnMossAt(key.dim, key.x, key.y, key.z)
		stageThreshold := SnifferEggHatchStageTicks(onMoss)

		mgr.mu.Lock()
		st := mgr.eggs[key]
		if st == nil {
			mgr.mu.Unlock()
			continue
		}
		st.TicksInStage++
		if st.TicksInStage < stageThreshold {
			mgr.mu.Unlock()
			continue
		}
		st.TicksInStage = 0
		nextStage := st.HatchStage + 1
		if nextStage <= SnifferEggMaxHatchStage {
			st.HatchStage = nextStage
			mgr.mu.Unlock()
			if nextState, ok := world.BlockStateByName(SnifferEggBlockName, map[string]string{"hatch": strconv.Itoa(nextStage)}); ok {
				cache.SetBlock(key.x, key.y, key.z, nextState)
				s.broadcastMobBlockUpdate(key.dim, key.x, key.y, key.z, nextState)
			}
			cx := int32(key.x >> 4)
			cz := int32(key.z >> 4)
			s.BroadcastDimensionChunk(key.dim, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket(SoundSnifferEggCrack, SoundSourceBlocks, float64(key.x)+0.5, float64(key.y)+0.5, float64(key.z)+0.5, 0.7, 1.0, 0))
			continue
		}
		// Hatch complete! Remove egg tracker, break block, play hatch sound, and spawn Snifflet.
		delete(mgr.eggs, key)
		mgr.mu.Unlock()

		cache.SetBlock(key.x, key.y, key.z, world.StateAir)
		s.broadcastMobBlockUpdate(key.dim, key.x, key.y, key.z, world.StateAir)
		cx := int32(key.x >> 4)
		cz := int32(key.z >> 4)
		s.BroadcastDimensionChunk(key.dim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundSnifferEggHatch, SoundSourceBlocks, float64(key.x)+0.5, float64(key.y)+0.5, float64(key.z)+0.5, 1.0, 1.0, 0))
		_, _ = s.SpawnSnifflet(key.dim, float64(key.x)+0.5, float64(key.y), float64(key.z)+0.5)
	}
}
