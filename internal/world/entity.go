package world

import (
	"crypto/rand"
	"math"
	"sort"
	"strings"
	"sync"
)

// ItemEntityTypeName is the canonical entity_type identifier for dropped items.
const ItemEntityTypeName = "minecraft:item"

// ExperienceOrbEntityTypeName is the canonical entity_type identifier for XP orbs.
const ExperienceOrbEntityTypeName = "minecraft:experience_orb"

// VillagerEntityTypeName is the canonical entity_type identifier for villagers.
const VillagerEntityTypeName = "minecraft:villager"

// Entity represents an in-game movable entity (mob, animal, dropped item, etc).
type Entity struct {
	ID        int32
	UUID      [16]byte
	TypeID    int // Network ID from the built-in minecraft:entity_type registry
	TypeName  string
	Dimension string

	X, Y, Z    float64
	Pitch, Yaw float32
	HeadYaw    float32
	VelocityX  int16
	VelocityY  int16
	VelocityZ  int16
	OnGround   bool
	Health     float32
	MaxHealth  float32

	// Item entity fields (populated when TypeName == ItemEntityTypeName).
	Item        ItemStack
	PickupDelay int
	Age         int
}

// NewItemEntity constructs a dropped item entity carrying stack at (x, y, z).
func NewItemEntity(x, y, z float64, stack ItemStack, typeID int) *Entity {
	return &Entity{
		TypeID:    typeID,
		TypeName:  ItemEntityTypeName,
		Dimension: DimensionOverworld,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
		Item:      stack,
	}
}

// EffectiveDimension returns the canonical dimension name for this entity,
// defaulting to DimensionOverworld when unset.
func (e *Entity) EffectiveDimension() string {
	if e == nil {
		return DimensionOverworld
	}
	return NormalizeDimension(e.Dimension)
}

// IsItem reports whether e is a dropped item entity.
func (e *Entity) IsItem() bool {
	return e != nil && (e.TypeName == ItemEntityTypeName || !e.Item.IsEmpty())
}

// IsExperienceOrb reports whether e is an experience orb entity.
func (e *Entity) IsExperienceOrb() bool {
	return e != nil && e.TypeName == ExperienceOrbEntityTypeName
}

// IsVillager reports whether e is a villager entity.
func (e *Entity) IsVillager() bool {
	return e != nil && e.TypeName == VillagerEntityTypeName
}

// IsMinecart reports whether e is any minecart variant entity.
func (e *Entity) IsMinecart() bool {
	if e == nil {
		return false
	}
	t := strings.ToLower(strings.TrimSpace(e.TypeName))
	if !strings.Contains(t, ":") && t != "" {
		t = "minecraft:" + t
	}
	return t == "minecraft:minecart" || strings.HasSuffix(t, "_minecart")
}

// IsBoat reports whether e is any boat or raft entity (including chest boats/rafts).
func (e *Entity) IsBoat() bool {
	if e == nil {
		return false
	}
	t := strings.ToLower(strings.TrimSpace(e.TypeName))
	if !strings.Contains(t, ":") && t != "" {
		t = "minecraft:" + t
	}
	return strings.HasSuffix(t, "_boat") || strings.HasSuffix(t, "_raft")
}

// IsChestBoat reports whether e is a chest boat or chest raft entity.
func (e *Entity) IsChestBoat() bool {
	if e == nil {
		return false
	}
	t := strings.ToLower(strings.TrimSpace(e.TypeName))
	if !strings.Contains(t, ":") && t != "" {
		t = "minecraft:" + t
	}
	return strings.HasSuffix(t, "_chest_boat") || strings.HasSuffix(t, "_chest_raft")
}

// IsVehicle reports whether e is a minecart or boat/raft entity.
func (e *Entity) IsVehicle() bool {
	return e.IsMinecart() || e.IsBoat()
}

// BlockName returns the canonical block identifier (e.g. "minecraft:chest")
// for a block state ID, or "" if unknown.
func BlockName(state uint16) string {
	st, ok := stateByID(state)
	if !ok {
		return ""
	}
	return st.Name
}

// BlockDropItem returns the ItemStack dropped when breaking state in survival
// mode, and false if the block does not drop a collectible item.
func BlockDropItem(state uint16) (ItemStack, bool) {
	if state == StateAir || state == StateWater || state == StateBedrock {
		return ItemStack{}, false
	}
	st, ok := stateByID(state)
	if !ok || st.Name == "" {
		return ItemStack{}, false
	}
	dropName := st.Name
	switch st.Name {
	case "minecraft:air", "minecraft:cave_air", "minecraft:void_air",
		"minecraft:water", "minecraft:lava", "minecraft:bedrock", "minecraft:fire",
		"minecraft:nether_portal", "minecraft:end_portal":
		return ItemStack{}, false
	case "minecraft:grass_block", "minecraft:dirt_path", "minecraft:farmland":
		dropName = "minecraft:dirt"
	case "minecraft:stone":
		if _, hasCobble := ItemNameToID("minecraft:cobblestone"); hasCobble {
			dropName = "minecraft:cobblestone"
		}
	}
	stack := NewNamedItemStack(dropName, 1)
	if stack.IsEmpty() || stack.ResolvedItemID() <= 0 {
		return ItemStack{}, false
	}
	return stack, true
}

// IsInstantBreakBlock reports whether state has zero hardness in survival mode
// (e.g. tall grass, flowers, saplings, torches), breaking immediately on
// START_DESTROY_BLOCK (status 0).
func IsInstantBreakBlock(state uint16) bool {
	st, ok := stateByID(state)
	if !ok || st.Name == "" {
		return false
	}
	for _, suffix := range []string{"_sapling", "_flower", "_tulip", "_mushroom", "_torch"} {
		if strings.HasSuffix(st.Name, suffix) {
			return true
		}
	}
	switch st.Name {
	case "minecraft:short_grass", "minecraft:tall_grass", "minecraft:fern",
		"minecraft:large_fern", "minecraft:dead_bush", "minecraft:dandelion",
		"minecraft:poppy", "minecraft:allium", "minecraft:azure_bluet",
		"minecraft:oxeye_daisy", "minecraft:cornflower",
		"minecraft:lily_of_the_valley", "minecraft:sunflower", "minecraft:sugar_cane",
		"minecraft:wheat", "minecraft:carrots", "minecraft:potatoes", "minecraft:beetroots",
		"minecraft:melon_stem", "minecraft:attached_melon_stem",
		"minecraft:pumpkin_stem", "minecraft:attached_pumpkin_stem",
		"minecraft:nether_wart", "minecraft:kelp", "minecraft:kelp_plant",
		"minecraft:bamboo_sapling",
		"minecraft:fire", "minecraft:soul_fire":
		return true
	}
	return false
}

// ChunkPos returns the chunk coordinates containing the entity's X/Z position.
func (e *Entity) ChunkPos() ChunkPos {
	return ChunkPos{
		X: int32(int64(math.Floor(e.X)) >> 4),
		Z: int32(int64(math.Floor(e.Z)) >> 4),
	}
}

// EntityManager tracks active entities in the server and maintains a chunk-indexed
// spatial table for visibility-scoped queries.
type EntityManager struct {
	mu       sync.RWMutex
	entities map[int32]*Entity
	byChunk  map[ChunkPos][]*Entity
	chunkOf  map[int32]ChunkPos
	nextID   int32
	onStep   func(dim string, x, y, z int)
}

// NewEntityManager returns a fresh manager starting at a high ID.
func NewEntityManager() *EntityManager {
	return &EntityManager{
		entities: make(map[int32]*Entity),
		byChunk:  make(map[ChunkPos][]*Entity),
		chunkOf:  make(map[int32]ChunkPos),
		nextID:   1000, // keep IDs above early players
	}
}

// SetStepCallback registers an optional callback invoked when an entity is
// added or moved so pressure plates at its block position can be updated.
func (em *EntityManager) SetStepCallback(fn func(dim string, x, y, z int)) {
	em.mu.Lock()
	em.onStep = fn
	em.mu.Unlock()
}

// Add assigns an ID and UUID (if missing), indexes the entity by chunk, and tracks it.
func (em *EntityManager) Add(e *Entity) int32 {
	em.mu.Lock()
	em.nextID++
	e.ID = em.nextID
	if e.Dimension == "" {
		e.Dimension = DimensionOverworld
	}
	if e.UUID == [16]byte{} {
		rand.Read(e.UUID[:])
		// Version 4 UUID
		e.UUID[6] = (e.UUID[6] & 0x0f) | 0x40
		e.UUID[8] = (e.UUID[8] & 0x3f) | 0x80
	}
	em.entities[e.ID] = e
	pos := e.ChunkPos()
	em.chunkOf[e.ID] = pos
	em.byChunk[pos] = append(em.byChunk[pos], e)
	id := e.ID
	dim := e.EffectiveDimension()
	bx, by, bz := int(math.Floor(e.X)), int(math.Floor(e.Y)), int(math.Floor(e.Z))
	onStep := em.onStep
	em.mu.Unlock()
	if onStep != nil {
		onStep(dim, bx, by, bz)
	}
	return id
}

// Remove drops the entity by its ID and removes it from the spatial index.
func (em *EntityManager) Remove(id int32) {
	em.mu.Lock()
	defer em.mu.Unlock()
	e, ok := em.entities[id]
	if !ok {
		return
	}
	delete(em.entities, id)
	if pos, hasPos := em.chunkOf[id]; hasPos {
		em.removeFromChunk(pos, e)
		delete(em.chunkOf, id)
	}
}

func (em *EntityManager) removeFromChunk(pos ChunkPos, target *Entity) {
	bucket := em.byChunk[pos]
	for i, e := range bucket {
		if e == target {
			last := len(bucket) - 1
			bucket[i] = bucket[last]
			bucket[last] = nil
			bucket = bucket[:last]
			if len(bucket) == 0 {
				delete(em.byChunk, pos)
			} else {
				em.byChunk[pos] = bucket
			}
			return
		}
	}
}

// Get retrieves a snapshot of an entity by ID, or nil if not found.
func (em *EntityManager) Get(id int32) *Entity {
	em.mu.RLock()
	defer em.mu.RUnlock()
	e := em.entities[id]
	if e == nil {
		return nil
	}
	copy := *e
	return &copy
}

// Update executes a function on an entity under a write lock and updates its
// chunk index if the entity crossed a chunk boundary.
func (em *EntityManager) Update(id int32, fn func(*Entity)) {
	em.mu.Lock()
	var (
		updated      bool
		dim          string
		bx, by, bz   int
		onStep       func(string, int, int, int)
	)
	if e, ok := em.entities[id]; ok {
		oldPos := em.chunkOf[id]
		fn(e)
		newPos := e.ChunkPos()
		if newPos != oldPos {
			em.removeFromChunk(oldPos, e)
			em.chunkOf[id] = newPos
			em.byChunk[newPos] = append(em.byChunk[newPos], e)
		}
		updated = true
		dim = e.EffectiveDimension()
		bx, by, bz = int(math.Floor(e.X)), int(math.Floor(e.Y)), int(math.Floor(e.Z))
		onStep = em.onStep
	}
	em.mu.Unlock()
	if updated && onStep != nil {
		onStep(dim, bx, by, bz)
	}
}

// All returns a snapshot slice of all active entities.
func (em *EntityManager) All() []Entity {
	em.mu.RLock()
	defer em.mu.RUnlock()
	list := make([]Entity, 0, len(em.entities))
	for _, e := range em.entities {
		list = append(list, *e)
	}
	return list
}

// AppendAll appends snapshots of all active entities to dst without allocating
// when cap(dst) is sufficient.
func (em *EntityManager) AppendAll(dst []Entity) []Entity {
	em.mu.RLock()
	defer em.mu.RUnlock()
	for _, e := range em.entities {
		dst = append(dst, *e)
	}
	return dst
}

// AppendInChunkRange appends snapshots of all entities located in chunks within
// Chebyshev distance radius of (centerX, centerZ). Reuses dst's backing array.
func (em *EntityManager) AppendInChunkRange(dst []Entity, centerX, centerZ, radius int32) []Entity {
	em.mu.RLock()
	defer em.mu.RUnlock()
	for cx := centerX - radius; cx <= centerX+radius; cx++ {
		for cz := centerZ - radius; cz <= centerZ+radius; cz++ {
			for _, e := range em.byChunk[ChunkPos{X: cx, Z: cz}] {
				dst = append(dst, *e)
			}
		}
	}
	return dst
}

// ForEachInChunkRange calls fn with a snapshot of each entity within Chebyshev
// chunk distance radius of (centerX, centerZ) without allocating a slice.
func (em *EntityManager) ForEachInChunkRange(centerX, centerZ, radius int32, fn func(Entity)) {
	em.mu.RLock()
	defer em.mu.RUnlock()
	for cx := centerX - radius; cx <= centerX+radius; cx++ {
		for cz := centerZ - radius; cz <= centerZ+radius; cz++ {
			for _, e := range em.byChunk[ChunkPos{X: cx, Z: cz}] {
				fn(*e)
			}
		}
	}
}

// ChunkCount returns the number of entities currently indexed in chunk (cx, cz).
func (em *EntityManager) ChunkCount(cx, cz int32) int {
	em.mu.RLock()
	defer em.mu.RUnlock()
	return len(em.byChunk[ChunkPos{X: cx, Z: cz}])
}

// Count returns the number of active entities.
func (em *EntityManager) Count() int {
	em.mu.RLock()
	defer em.mu.RUnlock()
	return len(em.entities)
}

var (
	blockRegOnce     sync.Once
	blockNameToRegID map[string]int32
	blockRegIDToName []string
)

func buildBlockRegistryTable() {
	stateByIDOnce.Do(buildStateTable)
	type entry struct {
		name  string
		minID uint16
	}
	entries := make([]entry, 0, len(idsByName))
	for name, states := range idsByName {
		if len(states) == 0 {
			continue
		}
		minID := states[0]
		for _, id := range states[1:] {
			if id < minID {
				minID = id
			}
		}
		entries = append(entries, entry{name: name, minID: minID})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].minID < entries[j].minID
	})
	blockNameToRegID = make(map[string]int32, len(entries))
	blockRegIDToName = make([]string, len(entries))
	for i, e := range entries {
		blockNameToRegID[e.name] = int32(i)
		blockRegIDToName[i] = e.name
	}
}

// BlockRegistryID returns the 0-based BuiltInRegistries.BLOCK numeric ID for a
// canonical block identifier (e.g. "minecraft:stone" -> 1).
func BlockRegistryID(name string) (int32, bool) {
	blockRegOnce.Do(buildBlockRegistryTable)
	id, ok := blockNameToRegID[normalizeItemName(name)]
	return id, ok
}

// BlockRegistryName returns the canonical block identifier for a 0-based
// BuiltInRegistries.BLOCK numeric ID.
func BlockRegistryName(id int32) (string, bool) {
	blockRegOnce.Do(buildBlockRegistryTable)
	if id < 0 || int(id) >= len(blockRegIDToName) {
		return "", false
	}
	return blockRegIDToName[id], true
}

