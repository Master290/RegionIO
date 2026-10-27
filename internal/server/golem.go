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

// Iron Golem and Snow Golem constants for Minecraft 26.1.2 (protocol 775).
const (
	MobTypeIronGolem = "minecraft:iron_golem"
	MobTypeSnowGolem = "minecraft:snow_golem"

	// BuiltInRegistries.ENTITY_TYPE numeric network IDs in Minecraft 26.1.2 (protocol 775).
	EntityTypeIronGolemID int = 70
	EntityTypeSnowGolemID int = 121

	// Vanilla MaxHealth values.
	DefaultIronGolemMaxHealth float32 = 100.0 // 50 hearts
	DefaultSnowGolemMaxHealth float32 = 4.0

	// Iron Golem movement, village defense, attack, and repair parameters.
	IronGolemWanderSpeed         = 0.08
	IronGolemChaseSpeed          = 0.16
	IronGolemDefenseRange        = 16.0
	IronGolemAttackReach         = 2.5
	IronGolemAttackCooldownTicks = 20
	IronGolemMinAttackDamage     = float32(7.5)
	IronGolemMaxAttackDamage     = float32(21.5)
	IronGolemFlingVelocityY      = 0.45
	IronGolemRepairHealAmount    = float32(25.0)

	// Villager panic/gossip Iron Golem spawning parameters.
	VillagerGolemPanicCount        = 3
	VillagerGolemGossipCount       = 5
	VillagerGolemGroupRadius       = 16.0
	VillagerGolemCheckRadius       = 16.0
	VillagerGolemSpawnCooldownTick = 600 // 30s between village golem spawns

	// ClientboundEntityEventPacket (0x22) event IDs for Iron Golem.
	EntityEventIronGolemAttack     byte = 4
	EntityEventIronGolemOfferPoppy byte = 11

	// SynchedEntityData index for IronGolem.DATA_FLAGS_ID in 26.1.2 (index 16, BYTE: 0x01 playerCreated).
	EntityDataIronGolemFlagsID  byte = 16
	IronGolemFlagPlayerCreated  byte = 0x01
)

// IronGolemState tracks per-golem attributes (PlayerCreated flag, active target,
// and attack cooldown).
type IronGolemState struct {
	EntityID       int32
	Dimension      string
	PlayerCreated  bool
	TargetMobID    int32
	TargetPlayer   *PlayerSession
	AttackCooldown int
}

// IronGolemPatternMatch describes a matched 4-iron-block T-shape + pumpkin on
// top in the world.
type IronGolemPatternMatch struct {
	CenterX    int
	TopY       int
	CenterZ    int
	ArmsAlongX bool
}

// Blocks returns the 5 block positions comprising the Iron Golem pattern
// (1 pumpkin at TopY, 3 upper iron blocks at TopY-1, and 1 lower stem iron block at TopY-2).
func (m IronGolemPatternMatch) Blocks() [5]world.BlockPos {
	dx, dz := 0, 1
	if m.ArmsAlongX {
		dx, dz = 1, 0
	}
	return [5]world.BlockPos{
		{X: m.CenterX, Y: m.TopY, Z: m.CenterZ},
		{X: m.CenterX - dx, Y: m.TopY - 1, Z: m.CenterZ - dz},
		{X: m.CenterX, Y: m.TopY - 1, Z: m.CenterZ},
		{X: m.CenterX + dx, Y: m.TopY - 1, Z: m.CenterZ + dz},
		{X: m.CenterX, Y: m.TopY - 2, Z: m.CenterZ},
	}
}

// SnowGolemPatternMatch describes a matched 2-snow-block + pumpkin stack.
type SnowGolemPatternMatch struct {
	X    int
	TopY int
	Z    int
}

// Blocks returns the 3 block positions comprising the Snow Golem pattern.
func (m SnowGolemPatternMatch) Blocks() [3]world.BlockPos {
	return [3]world.BlockPos{
		{X: m.X, Y: m.TopY, Z: m.Z},
		{X: m.X, Y: m.TopY - 1, Z: m.Z},
		{X: m.X, Y: m.TopY - 2, Z: m.Z},
	}
}

type golemManager struct {
	mu                     sync.RWMutex
	golems                 map[int32]*IronGolemState
	lastVillageSpawnTick   int64
}

var (
	serverGolemManagers sync.Map // map[*Server]*golemManager
	golemBlocksOnce     sync.Once
	ironBlockStateID    uint16
	snowBlockStateID    uint16
	pumpkinBlockStates  map[uint16]bool
)

func initGolemBlockStates() {
	golemBlocksOnce.Do(func() {
		if st, ok := world.BlockStateIDByProperties("minecraft:iron_block", nil); ok {
			ironBlockStateID = st
		}
		if st, ok := world.BlockStateIDByProperties("minecraft:snow_block", nil); ok {
			snowBlockStateID = st
		}
		pumpkinBlockStates = make(map[uint16]bool)
		if st, ok := world.BlockStateIDByProperties("minecraft:pumpkin", nil); ok && st != world.StateAir {
			pumpkinBlockStates[st] = true
		}
		for _, dir := range []string{"north", "south", "west", "east"} {
			props := map[string]string{"facing": dir}
			if st, ok := world.BlockStateIDByProperties("minecraft:carved_pumpkin", props); ok && st != world.StateAir {
				pumpkinBlockStates[st] = true
			}
			if st, ok := world.BlockStateIDByProperties("minecraft:jack_o_lantern", props); ok && st != world.StateAir {
				pumpkinBlockStates[st] = true
			}
		}
	})
}

func (s *Server) golemManager() *golemManager {
	if v, ok := serverGolemManagers.Load(s); ok {
		return v.(*golemManager)
	}
	actual, _ := serverGolemManagers.LoadOrStore(s, &golemManager{
		golems: make(map[int32]*IronGolemState),
	})
	return actual.(*golemManager)
}

// IsGolemPumpkinBlockState reports whether state is a carved_pumpkin,
// jack_o_lantern, or pumpkin block state that can complete an Iron or Snow Golem.
func IsGolemPumpkinBlockState(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	initGolemBlockStates()
	if pumpkinBlockStates[state] {
		return true
	}
	name := strings.ToLower(world.BlockName(state))
	return name == "minecraft:carved_pumpkin" || name == "minecraft:jack_o_lantern" || name == "minecraft:pumpkin"
}

// IsIronBlockState reports whether state is `minecraft:iron_block`.
func IsIronBlockState(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	initGolemBlockStates()
	if ironBlockStateID != 0 && state == ironBlockStateID {
		return true
	}
	return strings.ToLower(world.BlockName(state)) == "minecraft:iron_block"
}

// IsSnowBlockState reports whether state is `minecraft:snow_block`.
func IsSnowBlockState(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	initGolemBlockStates()
	if snowBlockStateID != 0 && state == snowBlockStateID {
		return true
	}
	return strings.ToLower(world.BlockName(state)) == "minecraft:snow_block"
}

// EncodeIronGolemDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating IronGolem.DATA_FLAGS_ID (index 16, byte: 0x01 playerCreated).
func EncodeIronGolemDataPacket(entityID int32, playerCreated bool) []byte {
	var flags byte
	if playerCreated {
		flags |= IronGolemFlagPlayerCreated
	}
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataIronGolemFlagsID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)
	w.Byte(0xFF)
	return w.Bytes()
}

// FindIronGolemPatternAt searches around (placedX, placedY, placedZ) in cache
// for a complete 4-iron-block T-shape topped with a carved pumpkin / jack o'lantern.
func FindIronGolemPatternAt(cache *world.Cache, placedX, placedY, placedZ int) (IronGolemPatternMatch, bool) {
	if cache == nil {
		return IronGolemPatternMatch{}, false
	}
	for dy := 0; dy <= 2; dy++ {
		topY := placedY + dy
		if topY-2 < world.MinY || topY >= world.MinY+world.WorldHeight {
			continue
		}
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				cx := placedX + dx
				cz := placedZ + dz
				if checkIronGolemPatternCenter(cache, cx, topY, cz, true) {
					return IronGolemPatternMatch{
						CenterX:    cx,
						TopY:       topY,
						CenterZ:    cz,
						ArmsAlongX: true,
					}, true
				}
				if checkIronGolemPatternCenter(cache, cx, topY, cz, false) {
					return IronGolemPatternMatch{
						CenterX:    cx,
						TopY:       topY,
						CenterZ:    cz,
						ArmsAlongX: false,
					}, true
				}
			}
		}
	}
	return IronGolemPatternMatch{}, false
}

func checkIronGolemPatternCenter(cache *world.Cache, cx, topY, cz int, armsAlongX bool) bool {
	if !IsGolemPumpkinBlockState(cache.GetBlock(cx, topY, cz)) {
		return false
	}
	dx, dz := 0, 1
	if armsAlongX {
		dx, dz = 1, 0
	}
	if !IsIronBlockState(cache.GetBlock(cx-dx, topY-1, cz-dz)) ||
		!IsIronBlockState(cache.GetBlock(cx, topY-1, cz)) ||
		!IsIronBlockState(cache.GetBlock(cx+dx, topY-1, cz+dz)) ||
		!IsIronBlockState(cache.GetBlock(cx, topY-2, cz)) {
		return false
	}
	if !isPassableBlock(cache.GetBlock(cx-dx, topY, cz-dz)) ||
		!isPassableBlock(cache.GetBlock(cx+dx, topY, cz+dz)) ||
		!isPassableBlock(cache.GetBlock(cx-dx, topY-2, cz-dz)) ||
		!isPassableBlock(cache.GetBlock(cx+dx, topY-2, cz+dz)) {
		return false
	}
	return true
}

// FindSnowGolemPatternAt checks whether (placedX, placedY, placedZ) completes a
// 2-snow-block + pumpkin vertical stack.
func FindSnowGolemPatternAt(cache *world.Cache, placedX, placedY, placedZ int) (SnowGolemPatternMatch, bool) {
	if cache == nil {
		return SnowGolemPatternMatch{}, false
	}
	for dy := 0; dy <= 2; dy++ {
		topY := placedY + dy
		if topY-2 < world.MinY || topY >= world.MinY+world.WorldHeight {
			continue
		}
		if IsGolemPumpkinBlockState(cache.GetBlock(placedX, topY, placedZ)) &&
			IsSnowBlockState(cache.GetBlock(placedX, topY-1, placedZ)) &&
			IsSnowBlockState(cache.GetBlock(placedX, topY-2, placedZ)) {
			return SnowGolemPatternMatch{X: placedX, TopY: topY, Z: placedZ}, true
		}
	}
	return SnowGolemPatternMatch{}, false
}

// TrySummonIronGolemAt checks if a block placement at (x, y, z) in dimension
// completes an Iron Golem T-shape pattern. If matched, clears the 5 blocks,
// spawns a player-created Iron Golem, and grants `adventure/summon_iron_golem`.
func (s *Server) TrySummonIronGolemAt(dimension string, x, y, z int, summoner *PlayerSession) (int32, bool) {
	if s == nil {
		return 0, false
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return 0, false
	}
	match, ok := FindIronGolemPatternAt(cache, x, y, z)
	if !ok {
		return 0, false
	}
	for _, pos := range match.Blocks() {
		cache.SetBlock(pos.X, pos.Y, pos.Z, world.StateAir)
		w := protocol.NewWriter(12)
		w.Position(pos.X, pos.Y, pos.Z)
		w.VarInt(int32(world.StateAir))
		s.BroadcastDimensionChunk(normDim, int32(pos.X>>4), int32(pos.Z>>4), protocol.PlayBlockUpdate, w.Bytes())
	}

	spawnX := float64(match.CenterX) + 0.5
	spawnY := float64(match.TopY-2) + 0.05
	spawnZ := float64(match.CenterZ) + 0.5
	id, err := s.SpawnIronGolem(normDim, spawnX, spawnY, spawnZ, true)
	if err != nil {
		return 0, false
	}
	if summoner != nil {
		s.GrantCriterion(summoner, "minecraft:adventure/summon_iron_golem", "summoned_golem")
	}
	return id, true
}

// TrySummonSnowGolemAt checks if a block placement at (x, y, z) in dimension
// completes a Snow Golem pattern. If matched, clears the 3 blocks and spawns a
// `minecraft:snow_golem`.
func (s *Server) TrySummonSnowGolemAt(dimension string, x, y, z int, summoner *PlayerSession) (int32, bool) {
	if s == nil {
		return 0, false
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return 0, false
	}
	match, ok := FindSnowGolemPatternAt(cache, x, y, z)
	if !ok {
		return 0, false
	}
	for _, pos := range match.Blocks() {
		cache.SetBlock(pos.X, pos.Y, pos.Z, world.StateAir)
		w := protocol.NewWriter(12)
		w.Position(pos.X, pos.Y, pos.Z)
		w.VarInt(int32(world.StateAir))
		s.BroadcastDimensionChunk(normDim, int32(pos.X>>4), int32(pos.Z>>4), protocol.PlayBlockUpdate, w.Bytes())
	}
	id, err := s.SpawnMobInDimension(normDim, float64(match.X)+0.5, float64(match.TopY-2)+0.05, float64(match.Z)+0.5, MobTypeSnowGolem)
	if err != nil {
		return 0, false
	}
	return id, true
}

// SpawnIronGolem spawns a `minecraft:iron_golem` at (x, y, z) in dimension with
// 100.0 HP and records whether it was player-created.
func (s *Server) SpawnIronGolem(dimension string, x, y, z float64, playerCreated bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeIronGolem)
	if typeID < 0 {
		typeID = EntityTypeIronGolemID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeIronGolem,
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
	mem.health = DefaultIronGolemMaxHealth
	mem.lastHealth = DefaultIronGolemMaxHealth
	tr.mu.Unlock()

	mgr := s.golemManager()
	mgr.mu.Lock()
	mgr.golems[id] = &IronGolemState{
		EntityID:      id,
		Dimension:     normDim,
		PlayerCreated: playerCreated,
	}
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeIronGolemDataPacket(id, playerCreated))
	}
	return id, nil
}

// GetIronGolemState returns a snapshot of the IronGolemState for entityID.
func (s *Server) GetIronGolemState(entityID int32) (IronGolemState, bool) {
	if s == nil {
		return IronGolemState{}, false
	}
	mgr := s.golemManager()
	mgr.mu.RLock()
	st, ok := mgr.golems[entityID]
	var out IronGolemState
	if ok && st != nil {
		out = *st
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// IsIronGolemPlayerCreated reports whether the Iron Golem with entityID was
// built by a player from blocks.
func (s *Server) IsIronGolemPlayerCreated(entityID int32) bool {
	st, ok := s.GetIronGolemState(entityID)
	return ok && st.PlayerCreated
}

// InteractWithIronGolem handles a player right-clicking an Iron Golem:
// holding `minecraft:iron_ingot` when the golem's health is < 100.0 heals +25.0 HP,
// consumes 1 iron ingot in survival, and plays `entity.iron_golem.repair`.
func (s *Server) InteractWithIronGolem(session *PlayerSession, entityID int32) bool {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeIronGolem {
		return false
	}
	dx := ent.X - snap.X
	dy := ent.Y - snap.Y
	dz := ent.Z - snap.Z
	if snap.GameMode != GameModeCreative && (dx*dx+dy*dy+dz*dz) > MaxMeleeAttackReach*MaxMeleeAttackReach {
		return false
	}
	held := session.HeldItem()
	if held.IsEmpty() || canonicalResourceName(held.ResolvedID()) != "minecraft:iron_ingot" {
		return false
	}
	curHP := s.MobHealth(entityID)
	if curHP >= DefaultIronGolemMaxHealth {
		return false
	}
	nextHP := curHP + IronGolemRepairHealAmount
	if nextHP > DefaultIronGolemMaxHealth {
		nextHP = DefaultIronGolemMaxHealth
	}
	s.SetMobHealth(entityID, nextHP)
	consumeOneHeldItemInSurvival(session)

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.iron_golem.repair", SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true
}

// IsIronGolemTargetableHostile reports whether a mob of typeName is targeted by
// Iron Golems defending a village (all hostile monsters except creepers).
func IsIronGolemTargetableHostile(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypeZombie, MobTypeZombieVillager, MobTypeHusk, MobTypeDrowned,
		MobTypeSkeleton, MobTypeStray, MobTypeBogged, "minecraft:parched", MobTypeWitherSkeleton,
		MobTypeSpider, "minecraft:cave_spider", MobTypeSilverfish, "minecraft:endermite",
		MobTypeSlime, MobTypeMagmaCube,
		MobTypePillager, MobTypeVindicator, MobTypeEvoker, MobTypeRavager, MobTypeVex, MobTypeIllusioner, MobTypeWitch,
		MobTypePhantom, MobTypeWither, "minecraft:blaze", "minecraft:breeze",
		MobTypePiglin, MobTypePiglinBrute, MobTypeHoglin, MobTypeZoglin:
		return true
	default:
		return false
	}
}

// TickVillagerGolemSpawning checks villager clusters for panic (3+ villagers
// near a hostile monster) or gossip gathering (5+ villagers) with no Iron Golem
// within 16 blocks, and spawns a village Iron Golem when needed.
func (s *Server) TickVillagerGolemSpawning(rng *rand.Rand) (spawnedGolemID int32, spawned bool) {
	if s == nil || s.entities == nil {
		return 0, false
	}
	gameTime, _ := s.WorldTime()
	mgr := s.golemManager()
	mgr.mu.RLock()
	lastTick := mgr.lastVillageSpawnTick
	mgr.mu.RUnlock()
	if lastTick > 0 && gameTime >= lastTick && gameTime-lastTick < VillagerGolemSpawnCooldownTick {
		return 0, false
	}

	allEnts := s.entities.AppendAll(nil)
	var villagers []world.Entity
	var hostiles []world.Entity
	var golems []world.Entity
	for i := range allEnts {
		e := allEnts[i]
		norm := canonicalResourceName(e.TypeName)
		if norm == MobTypeVillager && !s.IsMobBaby(e.ID) {
			villagers = append(villagers, e)
		} else if norm == MobTypeIronGolem {
			golems = append(golems, e)
		} else if IsIronGolemTargetableHostile(norm) {
			hostiles = append(hostiles, e)
		}
	}
	if len(villagers) < VillagerGolemPanicCount {
		return 0, false
	}

	groupRadSq := VillagerGolemGroupRadius * VillagerGolemGroupRadius
	golemCheckSq := VillagerGolemCheckRadius * VillagerGolemCheckRadius

	for i := range villagers {
		v := &villagers[i]
		dim := v.EffectiveDimension()

		// Skip if an Iron Golem is already within 16 blocks.
		hasGolem := false
		for j := range golems {
			g := &golems[j]
			if g.EffectiveDimension() != dim {
				continue
			}
			dx, dy, dz := g.X-v.X, g.Y-v.Y, g.Z-v.Z
			if dx*dx+dy*dy+dz*dz <= golemCheckSq {
				hasGolem = true
				break
			}
		}
		if hasGolem {
			continue
		}

		// Count nearby adult villagers in the same dimension.
		nearbyVillagers := 0
		for j := range villagers {
			other := &villagers[j]
			if other.EffectiveDimension() != dim {
				continue
			}
			dx, dy, dz := other.X-v.X, other.Y-v.Y, other.Z-v.Z
			if dx*dx+dy*dy+dz*dz <= groupRadSq {
				nearbyVillagers++
			}
		}

		// Check if a hostile monster is within 16 blocks of this villager group.
		panicking := false
		for j := range hostiles {
			h := &hostiles[j]
			if h.EffectiveDimension() != dim {
				continue
			}
			dx, dy, dz := h.X-v.X, h.Y-v.Y, h.Z-v.Z
			if dx*dx+dy*dy+dz*dz <= groupRadSq {
				panicking = true
				break
			}
		}

		if (panicking && nearbyVillagers >= VillagerGolemPanicCount) || nearbyVillagers >= VillagerGolemGossipCount {
			if id, ok := s.TrySpawnVillageIronGolemNear(dim, v.X, v.Y, v.Z, rng); ok {
				mgr.mu.Lock()
				mgr.lastVillageSpawnTick = gameTime
				mgr.mu.Unlock()
				return id, true
			}
		}
	}
	return 0, false
}

// TrySpawnVillageIronGolemNear finds a safe walkable position near (vx, vy, vz)
// in dimension and spawns a village-created Iron Golem (PlayerCreated = false).
func (s *Server) TrySpawnVillageIronGolemNear(dimension string, vx, vy, vz float64, rng *rand.Rand) (int32, bool) {
	if s == nil || s.entities == nil {
		return 0, false
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	baseX := int(math.Floor(vx))
	baseY := int(math.Floor(vy))
	baseZ := int(math.Floor(vz))

	spawnX, spawnY, spawnZ := vx, vy, vz
	if cache != nil {
		found := false
		for attempt := 0; attempt < 10 && !found; attempt++ {
			var dx, dz int
			if rng != nil {
				dx = rng.Intn(17) - 8
				dz = rng.Intn(17) - 8
			} else {
				dx = (attempt % 5) - 2
				dz = (attempt / 5) - 1
			}
			cx := baseX + dx
			cz := baseZ + dz
			for dy := 3; dy >= -3; dy-- {
				cy := baseY + dy
				if cy <= world.MinY+1 || cy >= world.MinY+world.WorldHeight-3 {
					continue
				}
				floor := cache.GetBlock(cx, cy-1, cz)
				if isSolidGroundBlock(floor) &&
					isColumnPassableAt(cache, cx, cy, cz) &&
					isPassableBlock(cache.GetBlock(cx, cy+2, cz)) {
					spawnX = float64(cx) + 0.5
					spawnY = float64(cy)
					spawnZ = float64(cz) + 0.5
					found = true
					break
				}
			}
		}
	}
	id, err := s.SpawnIronGolem(normDim, spawnX, spawnY, spawnZ, false)
	if err != nil {
		return 0, false
	}
	return id, true
}

// TickIronGolems steps all active Iron Golems: scans for nearby hostile mobs
// within 16 blocks, chases the closest hostile target, and attacks with
// 7.5..21.5 damage + upward knockback fling (vy = 0.45).
func (s *Server) TickIronGolems(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	allEnts := s.entities.AppendAll(nil)
	var golemEnts []world.Entity
	var hostileEnts []world.Entity
	for i := range allEnts {
		e := allEnts[i]
		norm := canonicalResourceName(e.TypeName)
		if norm == MobTypeIronGolem {
			golemEnts = append(golemEnts, e)
		} else if IsIronGolemTargetableHostile(norm) {
			hostileEnts = append(hostileEnts, e)
		}
	}
	if len(golemEnts) == 0 {
		return
	}

	for i := range golemEnts {
		s.tickSingleIronGolem(&golemEnts[i], hostileEnts, rng)
	}
}

func (s *Server) tickSingleIronGolem(golem *world.Entity, hostiles []world.Entity, rng *rand.Rand) {
	mgr := s.golemManager()
	mgr.mu.Lock()
	st, ok := mgr.golems[golem.ID]
	if !ok || st == nil {
		st = &IronGolemState{
			EntityID:  golem.ID,
			Dimension: golem.EffectiveDimension(),
		}
		mgr.golems[golem.ID] = st
	}
	if st.AttackCooldown > 0 {
		st.AttackCooldown--
	}
	curTargetID := st.TargetMobID
	curCooldown := st.AttackCooldown
	mgr.mu.Unlock()

	dim := golem.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	feetY := int(math.Floor(golem.Y + 0.01))

	// Find or validate nearest hostile mob within IronGolemDefenseRange (16 blocks).
	var targetEnt *world.Entity
	bestDistSq := IronGolemDefenseRange * IronGolemDefenseRange
	for i := range hostiles {
		h := &hostiles[i]
		if h.EffectiveDimension() != dim {
			continue
		}
		dx := h.X - golem.X
		dy := h.Y - golem.Y
		dz := h.Z - golem.Z
		dSq := dx*dx + dy*dy + dz*dz
		if h.ID == curTargetID && dSq <= bestDistSq*1.5 {
			targetEnt = h
			break
		}
		if dSq <= bestDistSq {
			bestDistSq = dSq
			targetEnt = h
		}
	}

	if targetEnt == nil {
		if curTargetID != 0 {
			mgr.mu.Lock()
			st.TargetMobID = 0
			mgr.mu.Unlock()
		}
		s.stepIronGolemPolishAndPlayerTarget(golem, curCooldown, rng)
		return
	}

	mgr.mu.Lock()
	st.TargetMobID = targetEnt.ID
	mgr.mu.Unlock()

	s.stepEntityTowardPoint(golem, cache, feetY, targetEnt.X, targetEnt.Y, targetEnt.Z, IronGolemChaseSpeed, 1.5)
	updatedGolem := s.entities.Get(golem.ID)
	if updatedGolem == nil {
		return
	}
	dx := targetEnt.X - updatedGolem.X
	dy := targetEnt.Y - updatedGolem.Y
	dz := targetEnt.Z - updatedGolem.Z
	if dx*dx+dy*dy+dz*dz <= IronGolemAttackReach*IronGolemAttackReach && curCooldown <= 0 {
		mgr.mu.Lock()
		st.AttackCooldown = IronGolemAttackCooldownTicks
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

		if targetEnt.TypeName == MobTypeWither {
			s.DamageWither(targetEnt.ID, dmg, nil, false)
			return
		}
		killed := s.applyDamageToMob(targetEnt, dmg, nil, 0.5, false, true)
		if !killed {
			// Apply Iron Golem's signature upward fling (vy = 0.45).
			velY := clampVelocityShort(IronGolemFlingVelocityY)
			s.entities.Update(targetEnt.ID, func(e *world.Entity) {
				e.OnGround = false
				e.VelocityY = velY
				e.Y += IronGolemFlingVelocityY
			})
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEntityMotion,
				EncodeSetEntityMotionPacket(targetEnt.ID, 0, IronGolemFlingVelocityY, 0))
		} else {
			mgr.mu.Lock()
			st.TargetMobID = 0
			mgr.mu.Unlock()
		}
	}
}

// --- Copper Golem & Copper Golem Statue Hooks (Minecraft 26.1.2) ---

const (
	// MobTypeCopperGolem is the canonical 26.1.2 entity identifier for Copper Golem (`minecraft:copper_golem`, ID 28).
	MobTypeCopperGolem = "minecraft:copper_golem"

	// EntityTypeCopperGolemID is the protocol 775 numeric entity type ID for `minecraft:copper_golem`.
	EntityTypeCopperGolemID int = 28

	// DefaultCopperGolemMaxHealth is the vanilla max health for a Copper Golem (12.0 HP = 6 hearts).
	DefaultCopperGolemMaxHealth float32 = 12.0

	// CopperGolemStageAdvanceTicks is the oxidation stage interval in ticks for an unwaxed Copper Golem.
	CopperGolemStageAdvanceTicks = 12000
)

// CopperGolemState tracks per-entity oxidation stage, wax state, pose, facing, and custom name for a `minecraft:copper_golem`.
type CopperGolemState struct {
	EntityID       int32
	Dimension      string
	OxidationStage world.CopperOxidationStage
	Waxed          bool
	Pose           string
	Facing         string
	CustomName     string
	OxidationTicks int
}

var (
	copperGolemStatesMap sync.Map // map[*Server]*copperGolemManager
)

type copperGolemManager struct {
	mu         sync.RWMutex
	golems     map[int32]*CopperGolemState
	hookInited bool
}

func (s *Server) copperGolemMgr() *copperGolemManager {
	if v, ok := copperGolemStatesMap.Load(s); ok {
		return v.(*copperGolemManager)
	}
	actual, _ := copperGolemStatesMap.LoadOrStore(s, &copperGolemManager{
		golems: make(map[int32]*CopperGolemState),
	})
	return actual.(*copperGolemManager)
}

// EnsureCopperGolemStatueReviveHook registers the server's `SetCopperGolemStatueReviveHook`
// callback so scraping an unoxidized, unwaxed `minecraft:copper_golem_statue` with an axe
// revives it into a living `minecraft:copper_golem` entity, restoring any saved
// `CustomName` and `EntityUUID` from `GetCopperGolemStatueAt` and clearing the statue record.
func (s *Server) EnsureCopperGolemStatueReviveHook() {
	if s == nil {
		return
	}
	mgr := s.copperGolemMgr()
	mgr.mu.Lock()
	if mgr.hookInited {
		mgr.mu.Unlock()
		return
	}
	mgr.hookInited = true
	mgr.mu.Unlock()

	s.SetCopperGolemStatueReviveHook(func(dim string, x, y, z int, facing string) (int32, bool) {
		statueBE, hasBE := s.GetCopperGolemStatueAt(dim, x, y, z)
		s.RemoveCopperGolemStatueAt(dim, x, y, z)

		id, err := s.SpawnCopperGolem(dim, float64(x)+0.5, float64(y), float64(z)+0.5, facing)
		if err != nil || id <= 0 {
			return 0, false
		}
		if hasBE && statueBE != nil {
			if statueBE.HasUUID && statueBE.EntityUUID != ([16]byte{}) && s.entities != nil {
				uuid := statueBE.EntityUUID
				s.entities.Update(id, func(e *world.Entity) {
					e.UUID = uuid
				})
			}
			if statueBE.CustomName != "" {
				s.SetCopperGolemCustomName(id, statueBE.CustomName)
			}
			if statueBE.Pose != "" {
				mgr := s.copperGolemMgr()
				mgr.mu.Lock()
				if st := mgr.golems[id]; st != nil {
					st.Pose = statueBE.Pose
				}
				mgr.mu.Unlock()
			}
		}
		return id, true
	})
}

func yawForCardinalFacing(facing string) float32 {
	switch strings.ToLower(strings.TrimSpace(facing)) {
	case "north":
		return 180.0
	case "south":
		return 0.0
	case "west":
		return 90.0
	case "east":
		return -90.0
	default:
		return 0.0
	}
}

func cardinalFacingFromYaw(yaw float32) string {
	norm := math.Mod(float64(yaw), 360.0)
	if norm < 0 {
		norm += 360.0
	}
	switch {
	case norm >= 315.0 || norm < 45.0:
		return "south"
	case norm >= 45.0 && norm < 135.0:
		return "west"
	case norm >= 135.0 && norm < 225.0:
		return "north"
	default:
		return "east"
	}
}

// SpawnCopperGolem spawns a `minecraft:copper_golem` (ID 28) at (x, y, z) in dimension.
func (s *Server) SpawnCopperGolem(dimension string, x, y, z float64, facing string) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	s.EnsureCopperGolemStatueReviveHook()
	normDim := world.NormalizeDimension(dimension)
	if facing == "" {
		facing = "north"
	}
	yaw := yawForCardinalFacing(facing)
	typeID := ResolveEntityTypeID(MobTypeCopperGolem)
	if typeID < 0 {
		typeID = EntityTypeCopperGolemID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeCopperGolem,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		Yaw:       yaw,
		HeadYaw:   yaw,
		OnGround:  true,
	})

	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(id, tr.aiTickID)
	mem.healthInit = true
	mem.health = DefaultCopperGolemMaxHealth
	mem.lastHealth = DefaultCopperGolemMaxHealth
	tr.mu.Unlock()

	mgr := s.copperGolemMgr()
	mgr.mu.Lock()
	mgr.golems[id] = &CopperGolemState{
		EntityID:       id,
		Dimension:      normDim,
		OxidationStage: world.CopperStageUnaffected,
		Waxed:          false,
		Pose:           "standing",
		Facing:         facing,
	}
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
	}
	return id, nil
}

// GetCopperGolemState returns a snapshot of the CopperGolemState for entityID.
func (s *Server) GetCopperGolemState(entityID int32) (CopperGolemState, bool) {
	if s == nil {
		return CopperGolemState{}, false
	}
	mgr := s.copperGolemMgr()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	st, ok := mgr.golems[entityID]
	if !ok || st == nil {
		return CopperGolemState{}, false
	}
	return *st, true
}

// SetCopperGolemState updates the oxidation stage, wax state, and pose of a Copper Golem.
func (s *Server) SetCopperGolemState(entityID int32, stage world.CopperOxidationStage, waxed bool, pose string) bool {
	if s == nil {
		return false
	}
	mgr := s.copperGolemMgr()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.golems[entityID]
	if !ok || st == nil {
		return false
	}
	st.OxidationStage = stage
	st.Waxed = waxed
	if pose != "" {
		st.Pose = pose
	}
	return true
}

// SetCopperGolemCustomName sets the CustomName on a living Copper Golem.
func (s *Server) SetCopperGolemCustomName(entityID int32, name string) bool {
	if s == nil {
		return false
	}
	mgr := s.copperGolemMgr()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.golems[entityID]
	if !ok || st == nil {
		return false
	}
	st.CustomName = name
	return true
}

// PetrifyCopperGolemIntoStatue petrifies a fully oxidized `minecraft:copper_golem`
// into an `oxidized_copper_golem_statue` (or `waxed_oxidized_copper_golem_statue`)
// block by invoking `s.TurnCopperGolemIntoStatue(dimension, x, y, z, facing, pose, waxed)`,
// saving `CustomName` and `EntityUUID` via `s.SetCopperGolemStatueAt`, and despawning the mob entity.
func (s *Server) PetrifyCopperGolemIntoStatue(entityID int32, rng *rand.Rand) (statueState uint16, ok bool) {
	if s == nil || s.entities == nil {
		return 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCopperGolem {
		return 0, false
	}
	entUUID := ent.UUID
	hasUUID := entUUID != ([16]byte{})

	mgr := s.copperGolemMgr()
	mgr.mu.Lock()
	st := mgr.golems[entityID]
	var (
		facing     = cardinalFacingFromYaw(ent.Yaw)
		pose       = "standing"
		waxed      = false
		customName string
	)
	if st != nil {
		if st.Facing != "" {
			facing = st.Facing
		}
		if st.Pose != "" {
			pose = st.Pose
		} else if rng != nil {
			poses := [4]string{"standing", "sitting", "running", "star"}
			pose = poses[rng.Intn(len(poses))]
		}
		waxed = st.Waxed
		customName = st.CustomName
		delete(mgr.golems, entityID)
	}
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	bx := int(math.Floor(ent.X))
	by := int(math.Floor(ent.Y))
	bz := int(math.Floor(ent.Z))

	statueState, ok = s.TurnCopperGolemIntoStatue(dim, bx, by, bz, facing, pose, waxed)
	if !ok {
		return 0, false
	}
	if customName != "" || hasUUID {
		s.SetCopperGolemStatueAt(&CopperGolemStatueBlockEntity{
			Dimension:  dim,
			X:          bx,
			Y:          by,
			Z:          bz,
			Pose:       pose,
			Stage:      world.CopperStageOxidized,
			Waxed:      waxed,
			CustomName: customName,
			EntityUUID: entUUID,
			HasUUID:    hasUUID,
		})
	}
	cp := ent.ChunkPos()
	s.entities.Remove(entityID)
	tr := s.entityTracker()
	tr.mu.Lock()
	delete(tr.mobStates, entityID)
	tr.mu.Unlock()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities, encodeRemoveEntitiesPacket(nil, []int32{entityID}))
	return statueState, true
}

// TickCopperGolems advances oxidation for unwaxed `minecraft:copper_golem` entities
// and petrifies fully oxidized golems into statues via `PetrifyCopperGolemIntoStatue`.
func (s *Server) TickCopperGolems(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	s.EnsureCopperGolemStatueReviveHook()
	mgr := s.copperGolemMgr()
	var toPetrify []int32

	mgr.mu.Lock()
	for id, st := range mgr.golems {
		if st == nil {
			delete(mgr.golems, id)
			continue
		}
		if s.entities.Get(id) == nil {
			delete(mgr.golems, id)
			continue
		}
		if st.Waxed {
			continue
		}
		st.OxidationTicks++
		if st.OxidationTicks >= CopperGolemStageAdvanceTicks {
			st.OxidationTicks = 0
			switch st.OxidationStage {
			case world.CopperStageUnaffected:
				st.OxidationStage = world.CopperStageExposed
			case world.CopperStageExposed:
				st.OxidationStage = world.CopperStageWeathered
			case world.CopperStageWeathered:
				st.OxidationStage = world.CopperStageOxidized
			case world.CopperStageOxidized:
				toPetrify = append(toPetrify, id)
			}
		}
	}
	mgr.mu.Unlock()

	for _, id := range toPetrify {
		_, _ = s.PetrifyCopperGolemIntoStatue(id, rng)
	}
}
