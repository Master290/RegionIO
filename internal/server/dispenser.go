package server

import (
	"errors"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Dispenser and Dropper menu, slot, timing, and LevelEvent constants for Minecraft 26.1.2 (protocol 775).
const (
	// MenuTypeGeneric3x3 is BuiltInRegistries.MENU ID 6 (minecraft:generic_3x3)
	// used by both Dispenser and Dropper (9 container slots + 36 player slots).
	MenuTypeGeneric3x3 int32 = 6

	// DispenserSlotCount is the 3x3 inventory size (9 slots: 0..8) of a dispenser or dropper.
	DispenserSlotCount = 9
	// DispenserMenuSlotCount is the total number of slots in a generic_3x3 screen (9 + 27 + 9 = 45).
	DispenserMenuSlotCount = 45

	// DispenserRedstoneDelayTicks is the 4-game-tick (2 redstone tick) activation delay.
	DispenserRedstoneDelayTicks = world.DispenserTickDelay

	// Vanilla LevelEvent IDs for dispenser / dropper sounds and particles.
	LevelEventSoundDispense         int32 = 1000 // SOUND_DISPENSER_DISPENSE
	LevelEventSoundFailDispense     int32 = 1001 // SOUND_DISPENSER_FAIL
	LevelEventSoundLaunchProjectile int32 = 1002 // SOUND_DISPENSER_PROJECTILE_LAUNCH
	LevelEventSoundFireworkShoot    int32 = 1004 // SOUND_FIREWORK_SHOOT
	LevelEventSoundFireChargeShoot  int32 = 1018 // SOUND_BLAZE_FIREBALL
	LevelEventParticlesBoneMeal     int32 = 1505 // PARTICLES_PLANT_GROWTH
	LevelEventParticlesShootSmoke   int32 = 2000 // PARTICLES_SHOOT_SMOKE (data = 3D direction 0..5)
	LevelEventParticlesPotionSplash int32 = 2002 // PARTICLES_SPELL_POTION_SPLASH
	LevelEventParticlesWaxOn        int32 = 3003 // PARTICLES_AND_SOUND_WAX_ON

	// Default projectile launch parameters from ProjectileDispenseBehavior.
	DispenserDefaultProjectilePower       = 1.1
	DispenserDefaultProjectileUncertainty = 6.0
	DispenserPotionProjectilePower        = 0.55 // 1.1 * 0.5
	DispenserPotionProjectileUncertainty  = 3.0  // 6.0 * 0.5

	// Primed TNT default fuse duration (80 ticks = 4.0 seconds).
	DefaultPrimedTNTFuseTicks = 80
	// Primed TNT explosion radius (4.0 blocks).
	DefaultPrimedTNTExplosionRadius = 4.0

	// Tool max durabilities in vanilla 26.1.2 when used inside a dispenser.
	FlintAndSteelMaxDurability int32 = 64
	ShearsMaxDurability        int32 = 238
)

// Dispenser entity type identifiers for projectiles, vehicles, and primed TNT.
const (
	EntityTypeSpectralArrow    = "minecraft:spectral_arrow"
	EntityTypeSnowball         = "minecraft:snowball"
	EntityTypeEgg              = "minecraft:egg"
	EntityTypeSplashPotion     = "minecraft:splash_potion"
	EntityTypeLingeringPotion  = "minecraft:lingering_potion"
	EntityTypeExperienceBottle = "minecraft:experience_bottle"
	EntityTypeSmallFireball    = "minecraft:small_fireball"
	EntityTypeWindCharge       = "minecraft:wind_charge"
	EntityTypeTNT              = "minecraft:tnt"
	EntityTypeMinecart         = "minecraft:minecart"
	EntityTypeChestMinecart    = "minecraft:chest_minecart"
	EntityTypeFurnaceMinecart  = "minecraft:furnace_minecart"
	EntityTypeTNTMinecart      = "minecraft:tnt_minecart"
	EntityTypeHopperMinecart   = "minecraft:hopper_minecart"
	EntityTypeBoat             = "minecraft:oak_boat"
	EntityTypeChestBoat        = "minecraft:oak_chest_boat"
)

type dispenserBlockKey struct {
	dim     string
	x, y, z int
}

// DispenserBlockEntity stores the 9-slot inventory and per-slot component metadata
// for a `minecraft:dispenser` or `minecraft:dropper` block entity.
type DispenserBlockEntity struct {
	Dimension        string
	X, Y, Z          int
	IsDropper        bool
	CustomName       string
	Slots            [DispenserSlotCount]world.ItemStack
	SlotMeta         [DispenserSlotCount]ItemMeta
	SlotPotions      [DispenserSlotCount]string
	SlotFireworks    [DispenserSlotCount]FireworksComponent
	SlotShulkerItems [DispenserSlotCount][ChestSlotCount]world.ItemStack
}

// DispenserProjectile tracks a projectile launched by a dispenser (arrow,
// tipped arrow, spectral arrow, snowball, egg, splash/lingering potion,
// experience bottle, small fireball, or wind charge).
type DispenserProjectile struct {
	EntityID    int32
	TypeName    string
	Dimension   string
	X, Y, Z     float64
	VX, VY, VZ  float64
	PotionType  string
	Spectral    bool
	BaseDamage  float32
	TicksLived  int
	MaxTicks    int
	Impacted    bool
}

// PrimedTNTState tracks a primed `minecraft:tnt` entity spawned by a dispenser,
// flint and steel, redstone, or explosion.
type PrimedTNTState struct {
	EntityID  int32
	Dimension string
	X, Y, Z   float64
	VX, VY, VZ float64
	FuseTicks int
}

// DropperContainerTransferFunc is an optional callback that Agent B can register
// to intercept or extend dropper-to-container item transfers.
type DropperContainerTransferFunc func(dim string, x, y, z int, facing string, stack world.ItemStack, meta ItemMeta, potionType string) (transferred bool)

type serverDispenserState struct {
	mu                sync.Mutex
	blocks            map[dispenserBlockKey]*DispenserBlockEntity
	shulkerBoxes      map[dispenserBlockKey]*[ChestSlotCount]world.ItemStack
	mobEquipment      map[int32][EquipmentSlotCount]world.ItemStack
	horseArmor        map[int32]string
	wolfArmor         map[int32]world.ItemStack
	llamaCarpet       map[int32]string
	chestedAnimal     map[int32]bool
	projectiles       map[int32]*DispenserProjectile
	primedTNTs        map[int32]*PrimedTNTState
	customTransferFn  DropperContainerTransferFunc
	rng               *rand.Rand
}

var serverDispensers sync.Map // map[*Server]*serverDispenserState

func (s *Server) dispenserState() *serverDispenserState {
	if v, ok := serverDispensers.Load(s); ok {
		return v.(*serverDispenserState)
	}
	actual, _ := serverDispensers.LoadOrStore(s, &serverDispenserState{
		blocks:        make(map[dispenserBlockKey]*DispenserBlockEntity),
		shulkerBoxes:  make(map[dispenserBlockKey]*[ChestSlotCount]world.ItemStack),
		mobEquipment:  make(map[int32][EquipmentSlotCount]world.ItemStack),
		horseArmor:    make(map[int32]string),
		wolfArmor:     make(map[int32]world.ItemStack),
		llamaCarpet:   make(map[int32]string),
		chestedAnimal: make(map[int32]bool),
		projectiles:   make(map[int32]*DispenserProjectile),
		primedTNTs:    make(map[int32]*PrimedTNTState),
		rng:           rand.New(rand.NewSource(time.Now().UnixNano())),
	})
	return actual.(*serverDispenserState)
}

func normalizeDispenserDim(dim string) string {
	if dim == "" {
		return world.DimensionOverworld
	}
	return world.NormalizeDimension(dim)
}

// IsDispenserBlock reports whether state is `minecraft:dispenser`.
func IsDispenserBlock(state uint16) bool {
	return world.BlockName(state) == "minecraft:dispenser"
}

// IsDropperBlock reports whether state is `minecraft:dropper`.
func IsDropperBlock(state uint16) bool {
	return world.BlockName(state) == "minecraft:dropper"
}

// IsDispenserOrDropperBlock reports whether state is `minecraft:dispenser` or `minecraft:dropper`.
func IsDispenserOrDropperBlock(state uint16) bool {
	name := world.BlockName(state)
	return name == "minecraft:dispenser" || name == "minecraft:dropper"
}

// IsShulkerBoxItemOrBlock reports whether name is `minecraft:shulker_box` or any
// of the 16 dyed `minecraft:<color>_shulker_box` variants.
func IsShulkerBoxItemOrBlock(name string) bool {
	norm := canonicalResourceName(name)
	if norm == "minecraft:shulker_box" {
		return true
	}
	return strings.HasPrefix(norm, "minecraft:") && strings.HasSuffix(norm, "_shulker_box")
}

func (s *Server) getOrCreateDispenserLocked(ds *serverDispenserState, dim string, x, y, z int) *DispenserBlockEntity {
	normDim := normalizeDispenserDim(dim)
	key := dispenserBlockKey{dim: normDim, x: x, y: y, z: z}
	if be, ok := ds.blocks[key]; ok && be != nil {
		if cache := s.DimensionChunks(normDim); cache != nil {
			if world.BlockName(cache.GetBlock(x, y, z)) == "minecraft:dropper" {
				be.IsDropper = true
			} else if world.BlockName(cache.GetBlock(x, y, z)) == "minecraft:dispenser" {
				be.IsDropper = false
			}
		}
		return be
	}
	isDropper := false
	if cache := s.DimensionChunks(normDim); cache != nil {
		if world.BlockName(cache.GetBlock(x, y, z)) == "minecraft:dropper" {
			isDropper = true
		}
	}
	be := &DispenserBlockEntity{
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		IsDropper: isDropper,
	}
	ds.blocks[key] = be
	return be
}

// DispenserAt returns a snapshot of the DispenserBlockEntity at (dim, x, y, z).
func (s *Server) DispenserAt(dim string, x, y, z int) (DispenserBlockEntity, bool) {
	if s == nil {
		return DispenserBlockEntity{}, false
	}
	normDim := normalizeDispenserDim(dim)
	ds := s.dispenserState()
	ds.mu.Lock()
	defer ds.mu.Unlock()
	be, ok := ds.blocks[dispenserBlockKey{dim: normDim, x: x, y: y, z: z}]
	if !ok || be == nil {
		return DispenserBlockEntity{}, false
	}
	cp := *be
	for i := 0; i < DispenserSlotCount; i++ {
		cp.SlotMeta[i] = be.SlotMeta[i].Clone()
	}
	return cp, true
}

// DispenserSlotsInDimension returns the 9 inventory slots of the dispenser or
// dropper at (dim, x, y, z).
func (s *Server) DispenserSlotsInDimension(dim string, x, y, z int) [DispenserSlotCount]world.ItemStack {
	if s == nil {
		return [DispenserSlotCount]world.ItemStack{}
	}
	normDim := normalizeDispenserDim(dim)
	ds := s.dispenserState()
	ds.mu.Lock()
	defer ds.mu.Unlock()
	be := ds.blocks[dispenserBlockKey{dim: normDim, x: x, y: y, z: z}]
	if be == nil {
		return [DispenserSlotCount]world.ItemStack{}
	}
	return be.Slots
}

// DispenserSlots returns the 9 inventory slots of the dispenser or dropper at
// (x, y, z) in the Overworld.
func (s *Server) DispenserSlots(x, y, z int) [DispenserSlotCount]world.ItemStack {
	return s.DispenserSlotsInDimension(world.DimensionOverworld, x, y, z)
}

// DispenserSlotPotion returns the potion ID stored on slot (0..8) of the
// dispenser/dropper at (dim, x, y, z).
func (s *Server) DispenserSlotPotion(dim string, x, y, z, slot int) string {
	if s == nil || slot < 0 || slot >= DispenserSlotCount {
		return ""
	}
	normDim := normalizeDispenserDim(dim)
	ds := s.dispenserState()
	ds.mu.Lock()
	defer ds.mu.Unlock()
	be := ds.blocks[dispenserBlockKey{dim: normDim, x: x, y: y, z: z}]
	if be == nil {
		return ""
	}
	return be.SlotPotions[slot]
}

// DispenserSlotMeta returns the ItemMeta stored on slot (0..8) of the
// dispenser/dropper at (dim, x, y, z).
func (s *Server) DispenserSlotMeta(dim string, x, y, z, slot int) ItemMeta {
	if s == nil || slot < 0 || slot >= DispenserSlotCount {
		return ItemMeta{}
	}
	normDim := normalizeDispenserDim(dim)
	ds := s.dispenserState()
	ds.mu.Lock()
	defer ds.mu.Unlock()
	be := ds.blocks[dispenserBlockKey{dim: normDim, x: x, y: y, z: z}]
	if be == nil {
		return ItemMeta{}
	}
	return be.SlotMeta[slot].Clone()
}

// SetDispenserSlotInDimension sets slot (0..8) of the dispenser/dropper at
// (dim, x, y, z) and refreshes adjacent redstone comparators.
func (s *Server) SetDispenserSlotInDimension(dim string, x, y, z, slot int, stack world.ItemStack) bool {
	return s.SetDispenserSlotFull(dim, x, y, z, slot, stack, ItemMeta{}, "", FireworksComponent{})
}

// SetDispenserSlot sets slot (0..8) of the dispenser/dropper at (x, y, z) in
// the Overworld.
func (s *Server) SetDispenserSlot(x, y, z, slot int, stack world.ItemStack) bool {
	return s.SetDispenserSlotInDimension(world.DimensionOverworld, x, y, z, slot, stack)
}

// SetDispenserSlotWithPotion sets slot (0..8) of the dispenser/dropper at
// (dim, x, y, z) along with its potion ID (for potions, splash/lingering
// potions, or tipped arrows).
func (s *Server) SetDispenserSlotWithPotion(dim string, x, y, z, slot int, stack world.ItemStack, potionType string) bool {
	return s.SetDispenserSlotFull(dim, x, y, z, slot, stack, ItemMeta{}, potionType, FireworksComponent{})
}

// SetDispenserSlotWithMeta sets slot (0..8) of the dispenser/dropper at
// (dim, x, y, z) along with its ItemMeta (durability damage, enchantments, custom name).
func (s *Server) SetDispenserSlotWithMeta(dim string, x, y, z, slot int, stack world.ItemStack, meta ItemMeta) bool {
	return s.SetDispenserSlotFull(dim, x, y, z, slot, stack, meta, "", FireworksComponent{})
}

// SetDispenserSlotWithFirework sets slot (0..8) of the dispenser/dropper at
// (dim, x, y, z) along with its firework flight duration and explosions.
func (s *Server) SetDispenserSlotWithFirework(dim string, x, y, z, slot int, stack world.ItemStack, fw FireworksComponent) bool {
	return s.SetDispenserSlotFull(dim, x, y, z, slot, stack, ItemMeta{}, "", fw)
}

// SetDispenserSlotShulkerItems attaches 27-slot shulker box contents to slot
// (0..8) of the dispenser/dropper at (dim, x, y, z).
func (s *Server) SetDispenserSlotShulkerItems(dim string, x, y, z, slot int, items [ChestSlotCount]world.ItemStack) bool {
	if s == nil || slot < 0 || slot >= DispenserSlotCount {
		return false
	}
	normDim := normalizeDispenserDim(dim)
	ds := s.dispenserState()
	ds.mu.Lock()
	be := s.getOrCreateDispenserLocked(ds, normDim, x, y, z)
	be.SlotShulkerItems[slot] = items
	ds.mu.Unlock()
	return true
}

// SetDispenserSlotFull sets slot (0..8) of the dispenser/dropper at (dim, x, y, z)
// with full metadata and updates comparators.
func (s *Server) SetDispenserSlotFull(
	dim string,
	x, y, z, slot int,
	stack world.ItemStack,
	meta ItemMeta,
	potionType string,
	fw FireworksComponent,
) bool {
	if s == nil || slot < 0 || slot >= DispenserSlotCount {
		return false
	}
	normDim := normalizeDispenserDim(dim)
	ds := s.dispenserState()
	ds.mu.Lock()
	be := s.getOrCreateDispenserLocked(ds, normDim, x, y, z)
	if stack.IsEmpty() {
		be.Slots[slot] = world.ItemStack{}
		be.SlotMeta[slot] = ItemMeta{}
		be.SlotPotions[slot] = ""
		be.SlotFireworks[slot] = FireworksComponent{}
		be.SlotShulkerItems[slot] = [ChestSlotCount]world.ItemStack{}
	} else {
		normStack := world.NewItemStack(stack.ResolvedItemID(), stack.Count)
		if normStack.IsEmpty() {
			normStack = world.NewNamedItemStack(stack.ResolvedID(), stack.Count)
		}
		be.Slots[slot] = normStack
		be.SlotMeta[slot] = meta.Clone()
		be.SlotPotions[slot] = potionType
		be.SlotFireworks[slot] = fw.Clone()
	}
	ds.mu.Unlock()

	s.updateDispenserComparatorsAndBroadcast(normDim, x, y, z)
	return true
}

func (s *Server) updateDispenserComparatorsAndBroadcast(dim string, x, y, z int) {
	if s == nil {
		return
	}
	normDim := normalizeDispenserDim(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil {
		return
	}
	res := cache.UpdateComparatorsAround(x, y, z)
	if len(res.Changes) > 0 {
		s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
	}
}

// DropDispenserContentsInDimension removes the dispenser/dropper block entity
// at (dim, x, y, z) and drops all non-empty slot contents into the world.
func (s *Server) DropDispenserContentsInDimension(dim string, x, y, z int) {
	if s == nil {
		return
	}
	normDim := normalizeDispenserDim(dim)
	ds := s.dispenserState()
	ds.mu.Lock()
	key := dispenserBlockKey{dim: normDim, x: x, y: y, z: z}
	be := ds.blocks[key]
	delete(ds.blocks, key)
	ds.mu.Unlock()
	if be == nil {
		return
	}
	for i := 0; i < DispenserSlotCount; i++ {
		if !be.Slots[i].IsEmpty() {
			s.SpawnItemEntityInDimension(normDim, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, be.Slots[i])
		}
	}
	s.updateDispenserComparatorsAndBroadcast(normDim, x, y, z)
}

// DropDispenserContents removes the dispenser/dropper block entity at (x, y, z)
// in the Overworld and drops all non-empty slot contents.
func (s *Server) DropDispenserContents(x, y, z int) {
	s.DropDispenserContentsInDimension(world.DimensionOverworld, x, y, z)
}

// ShulkerBoxSlots returns the 27 inventory slots stored in a placed shulker box
// block entity at (dim, x, y, z).
func (s *Server) ShulkerBoxSlots(dim string, x, y, z int) [ChestSlotCount]world.ItemStack {
	if s == nil {
		return [ChestSlotCount]world.ItemStack{}
	}
	normDim := normalizeDispenserDim(dim)
	ds := s.dispenserState()
	ds.mu.Lock()
	defer ds.mu.Unlock()
	arr := ds.shulkerBoxes[dispenserBlockKey{dim: normDim, x: x, y: y, z: z}]
	if arr == nil {
		return [ChestSlotCount]world.ItemStack{}
	}
	return *arr
}

// SetShulkerBoxSlots stores the 27-slot inventory for a placed shulker box
// block entity at (dim, x, y, z).
func (s *Server) SetShulkerBoxSlots(dim string, x, y, z int, slots [ChestSlotCount]world.ItemStack) {
	if s == nil {
		return
	}
	normDim := normalizeDispenserDim(dim)
	ds := s.dispenserState()
	ds.mu.Lock()
	cp := slots
	ds.shulkerBoxes[dispenserBlockKey{dim: normDim, x: x, y: y, z: z}] = &cp
	ds.mu.Unlock()
	s.updateDispenserComparatorsAndBroadcast(normDim, x, y, z)
}

// DispenserComparatorSignalAt returns the vanilla redstone comparator signal
// (0..15) for a dispenser or dropper at (dim, x, y, z), matching
// AbstractContainerMenu.getRedstoneSignalFromContainer over 9 slots.
func (s *Server) DispenserComparatorSignalAt(dim string, x, y, z int) int {
	if s == nil {
		return 0
	}
	normDim := normalizeDispenserDim(dim)
	if cache := s.DimensionChunks(normDim); cache != nil {
		if !IsDispenserOrDropperBlock(cache.GetBlock(x, y, z)) {
			return 0
		}
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	be := ds.blocks[dispenserBlockKey{dim: normDim, x: x, y: y, z: z}]
	if be == nil {
		ds.mu.Unlock()
		return 0
	}
	slots := be.Slots
	ds.mu.Unlock()
	return CalculateContainerComparatorSignal(slots[:])
}

// ContainerComparatorSignalAt returns the redstone comparator signal (0..15)
// for a chest, trapped_chest, barrel, shulker_box, or furnace/blast_furnace/smoker
// at (dim, x, y, z).
func (s *Server) ContainerComparatorSignalAt(dim string, x, y, z int) int {
	if s == nil {
		return 0
	}
	normDim := normalizeDispenserDim(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return 0
	}
	state := cache.GetBlock(x, y, z)
	name := world.BlockName(state)
	switch {
	case IsChestBlock(state) || name == "minecraft:trapped_chest" || name == "minecraft:barrel":
		slots := s.ChestSlots(x, y, z)
		return CalculateContainerComparatorSignal(slots[:])
	case IsShulkerBoxItemOrBlock(name):
		slots := s.ShulkerBoxSlots(normDim, x, y, z)
		if sig := CalculateContainerComparatorSignal(slots[:]); sig > 0 {
			return sig
		}
		cSlots := s.ChestSlots(x, y, z)
		return CalculateContainerComparatorSignal(cSlots[:])
	case IsFurnaceBlock(state):
		slots := s.FurnaceSlots(x, y, z)
		return CalculateContainerComparatorSignal(slots[:])
	case name == "minecraft:hopper":
		return s.HopperComparatorSignalAt(normDim, x, y, z)
	case name == "minecraft:chiseled_bookshelf":
		return s.ChiseledBookshelfComparatorSignalAt(normDim, x, y, z)
	case name == "minecraft:crafter":
		return s.CrafterComparatorSignalAt(normDim, x, y, z)
	default:
		return 0
	}
}

// CalculateContainerComparatorSignal computes the vanilla comparator analog
// output (0..15) for a container slice using
// floor(sum(count / maxStackSize) / len(slots) * 14) + 1 when non-empty.
func CalculateContainerComparatorSignal(slots []world.ItemStack) int {
	if len(slots) == 0 {
		return 0
	}
	var sum float64
	nonEmpty := false
	for _, st := range slots {
		if st.IsEmpty() || st.Count <= 0 {
			continue
		}
		nonEmpty = true
		maxStack := DispenserItemMaxStackSize(st)
		if maxStack <= 0 {
			maxStack = 64
		}
		frac := float64(st.Count) / float64(maxStack)
		if frac > 1.0 {
			frac = 1.0
		}
		sum += frac
	}
	if !nonEmpty {
		return 0
	}
	sig := int(math.Floor((sum/float64(len(slots)))*14.0)) + 1
	if sig > 15 {
		sig = 15
	}
	return sig
}

// RefreshItemFrameComparators binds comparator signal providers for the item
// frame's dimension and updates any redstone comparators reading either the
// item frame's cell or its supporting block, returning all resulting block changes.
func (s *Server) RefreshItemFrameComparators(entityID int32) world.BlockUpdateResult {
	var out world.BlockUpdateResult
	if s == nil {
		return out
	}
	st, ok := s.ItemFrameState(entityID)
	if !ok {
		return out
	}
	normDim := normalizeDispenserDim(st.Dimension)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil {
		return out
	}
	res1 := cache.UpdateComparatorsAround(st.BlockX, st.BlockY, st.BlockZ)
	out.Changes = append(out.Changes, res1.Changes...)
	out.LightChanged = append(out.LightChanged, res1.LightChanged...)

	dx, dy, dz := DirectionOffset(st.Direction)
	supX, supY, supZ := st.BlockX-dx, st.BlockY-dy, st.BlockZ-dz
	res2 := cache.UpdateComparatorsAround(supX, supY, supZ)
	out.Changes = append(out.Changes, res2.Changes...)
	out.LightChanged = append(out.LightChanged, res2.LightChanged...)

	if len(out.Changes) > 0 {
		s.broadcastDimensionBlockChanges(normDim, cache, out.Changes, out.LightChanged)
	}
	return out
}

// DispenserItemMaxStackSize returns the vanilla maximum stack size (1, 16, or 64)
// for stack.
func DispenserItemMaxStackSize(stack world.ItemStack) int32 {
	if stack.IsEmpty() {
		return 64
	}
	id := canonicalResourceName(stack.ResolvedID())
	switch id {
	case "minecraft:snowball", "minecraft:egg", "minecraft:ender_pearl",
		"minecraft:Bucket", "minecraft:bucket", "minecraft:honey_bottle",
		"minecraft:wind_charge", "minecraft:armor_stand":
		return 16
	case "minecraft:water_bucket", "minecraft:lava_bucket", "minecraft:powder_snow_bucket",
		"minecraft:milk_bucket", "minecraft:cod_bucket", "minecraft:salmon_bucket",
		"minecraft:tropical_fish_bucket", "minecraft:pufferfish_bucket",
		"minecraft:axolotl_bucket", "minecraft:tadpole_bucket",
		"minecraft:potion", "minecraft:splash_potion", "minecraft:lingering_potion",
		"minecraft:flint_and_steel", "minecraft:shears", "minecraft:saddle",
		"minecraft:elytra", "minecraft:shield", "minecraft:carved_pumpkin",
		"minecraft:leather_horse_armor", "minecraft:iron_horse_armor",
		"minecraft:golden_horse_armor", "minecraft:diamond_horse_armor",
		"minecraft:wolf_armor",
		"minecraft:minecart", "minecraft:chest_minecart", "minecraft:furnace_minecart",
		"minecraft:tnt_minecart", "minecraft:hopper_minecart":
		if id == "minecraft:carved_pumpkin" {
			return 64
		}
		return 1
	}
	if strings.HasSuffix(id, "_boat") || strings.HasSuffix(id, "_raft") ||
		strings.HasSuffix(id, "_helmet") || strings.HasSuffix(id, "_chestplate") ||
		strings.HasSuffix(id, "_leggings") || strings.HasSuffix(id, "_boots") ||
		strings.HasSuffix(id, "_sword") || strings.HasSuffix(id, "_pickaxe") ||
		strings.HasSuffix(id, "_axe") || strings.HasSuffix(id, "_shovel") ||
		strings.HasSuffix(id, "_hoe") || IsShulkerBoxItemOrBlock(id) || IsMusicDiscItem(stack) {
		return 1
	}
	if strings.HasSuffix(id, "_banner") || strings.HasSuffix(id, "_sign") {
		return 16
	}
	return 64
}

// --- Direction, Position, Smoke Data, and Spread Helpers ---

// DispenserFacingAt returns the `facing` property ("north", "south", "west",
// "east", "up", "down") of a dispenser or dropper at (dim, x, y, z).
func (s *Server) DispenserFacingAt(dim string, x, y, z int) string {
	normDim := normalizeDispenserDim(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return "north"
	}
	props := world.BlockProperties(cache.GetBlock(x, y, z))
	if f := props["facing"]; f != "" {
		return f
	}
	return "north"
}

// DispenserFacingVector returns the unit step vector (dx, dy, dz) for a
// dispenser/dropper `facing` string.
func DispenserFacingVector(facing string) (dx, dy, dz int) {
	switch strings.ToLower(facing) {
	case "down":
		return 0, -1, 0
	case "up":
		return 0, 1, 0
	case "north":
		return 0, 0, -1
	case "south":
		return 0, 0, 1
	case "west":
		return -1, 0, 0
	case "east":
		return 1, 0, 0
	default:
		return 0, 0, -1
	}
}

// DispenserSmokeDirectionData returns the vanilla 3D direction index (0..5)
// used as the data parameter of LevelEvent.PARTICLES_SHOOT_SMOKE (2000) in 26.1.2:
// 0=down, 1=up, 2=north, 3=south, 4=west, 5=east.
func DispenserSmokeDirectionData(facing string) int32 {
	switch strings.ToLower(facing) {
	case "down":
		return 0
	case "up":
		return 1
	case "north":
		return 2
	case "south":
		return 3
	case "west":
		return 4
	case "east":
		return 5
	default:
		return 2
	}
}

// DispenserDispensePosition returns the vanilla world coordinates (px, py, pz)
// where a dispenser/dropper at (x, y, z) facing `facing` spawns items or
// projectiles, matching DispenserBlock.getDispensePosition:
//   - px = x + 0.5 + 0.7 * stepX
//   - py = y + 0.5 + 0.7 * stepY - (stepY == 0 ? 0.15625 : 0) for dropped items,
//     or y + 0.5 + 0.7 * stepY - 0.1 for projectiles
//   - pz = z + 0.5 + 0.7 * stepZ
func DispenserDispensePosition(x, y, z int, facing string, isProjectile bool) (px, py, pz float64) {
	dx, dy, dz := DispenserFacingVector(facing)
	px = float64(x) + 0.5 + 0.7*float64(dx)
	py = float64(y) + 0.5 + 0.7*float64(dy)
	pz = float64(z) + 0.5 + 0.7*float64(dz)
	if isProjectile {
		py -= 0.1
	} else if dy == 0 {
		py -= 0.15625
	}
	return px, py, pz
}

// DispenserItemVelocity computes the initial velocity (vx, vy, vz) with vanilla
// random Gaussian spread for an item dropped by a dispenser or dropper
// (DefaultDispenseItemBehavior.spawnItem with speed = 6).
func DispenserItemVelocity(facing string, rng *rand.Rand) (vx, vy, vz float64) {
	dx, dy, dz := DispenserFacingVector(facing)
	g := 0.25
	if rng != nil {
		g = rng.Float64()*0.1 + 0.2
	}
	vx = float64(dx) * g
	vy = 0.2
	if dy != 0 {
		vy = float64(dy) * g
	}
	vz = float64(dz) * g
	if rng != nil {
		const spread = 0.0075 * 6.0
		vx += rng.NormFloat64() * spread
		vy += rng.NormFloat64() * spread
		vz += rng.NormFloat64() * spread
	}
	return vx, vy, vz
}

// DispenserProjectileVelocity computes the initial velocity (vx, vy, vz) with
// vanilla Gaussian inaccuracy for a projectile fired from a dispenser
// (ProjectileDispenseBehavior: power = 1.1, uncertainty = 6.0).
func DispenserProjectileVelocity(facing string, power, uncertainty float64, rng *rand.Rand) (vx, vy, vz float64) {
	dx, dy, dz := DispenserFacingVector(facing)
	dirX := float64(dx)
	dirY := float64(dy) + 0.1
	dirZ := float64(dz)
	length := math.Sqrt(dirX*dirX + dirY*dirY + dirZ*dirZ)
	if length > 1e-6 {
		dirX /= length
		dirY /= length
		dirZ /= length
	}
	if rng != nil && uncertainty > 0 {
		const spreadScale = 0.0172275
		dirX += rng.NormFloat64() * spreadScale * uncertainty
		dirY += rng.NormFloat64() * spreadScale * uncertainty
		dirZ += rng.NormFloat64() * spreadScale * uncertainty
	}
	return dirX * power, dirY * power, dirZ * power
}

func (s *Server) broadcastDispenserEffects(dim string, x, y, z int, facing string, soundEvent int32, emitSmoke bool) {
	if s == nil {
		return
	}
	normDim := normalizeDispenserDim(dim)
	cx, cz := int32(x>>4), int32(z>>4)
	if soundEvent != 0 {
		s.BroadcastDimensionChunk(normDim, cx, cz, PlayLevelEventCB,
			EncodeLevelEventPacket(soundEvent, x, y, z, 0, false))
	}
	if emitSmoke {
		dirData := DispenserSmokeDirectionData(facing)
		s.BroadcastDimensionChunk(normDim, cx, cz, PlayLevelEventCB,
			EncodeLevelEventPacket(LevelEventParticlesShootSmoke, x, y, z, dirData, false))
	}
	var soundName string
	switch soundEvent {
	case LevelEventSoundDispense:
		soundName = "minecraft:block.dispenser.dispense"
	case LevelEventSoundFailDispense:
		soundName = "minecraft:block.dispenser.fail"
	case LevelEventSoundLaunchProjectile:
		soundName = "minecraft:block.dispenser.launch"
	case LevelEventSoundFireworkShoot:
		soundName = "minecraft:entity.firework_rocket.launch"
	case LevelEventSoundFireChargeShoot:
		soundName = "minecraft:item.firecharge.use"
	}
	if soundName != "" {
		s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(soundName, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	}
}

// ChooseRandomDispenserSlot selects a uniformly random non-empty slot index
// (0..8) from slots, matching vanilla DispenserBlockEntity.getRandomSlot.
// Returns -1 when all 9 slots are empty.
func ChooseRandomDispenserSlot(slots [DispenserSlotCount]world.ItemStack, rng *rand.Rand) int {
	chosen := -1
	nonEmptyCount := 0
	for i := 0; i < DispenserSlotCount; i++ {
		if slots[i].IsEmpty() || slots[i].Count <= 0 {
			continue
		}
		nonEmptyCount++
		if rng == nil || rng.Intn(nonEmptyCount) == 0 {
			chosen = i
		}
	}
	return chosen
}

// TryInsertIntoDispenser attempts to merge `stack` (with optional `meta` and
// `potionType`) into the 9 slots of the dispenser/dropper at (dim, x, y, z).
// Returns the uninserted remainder stack.
func (s *Server) TryInsertIntoDispenser(
	dim string,
	x, y, z int,
	stack world.ItemStack,
	meta ItemMeta,
	potionType string,
) world.ItemStack {
	if s == nil || stack.IsEmpty() || stack.Count <= 0 {
		return world.ItemStack{}
	}
	normDim := normalizeDispenserDim(dim)
	ds := s.dispenserState()
	ds.mu.Lock()
	be := s.getOrCreateDispenserLocked(ds, normDim, x, y, z)
	rem := mergeIntoDispenserLocked(be, stack, meta, potionType)
	changed := rem.Count != stack.Count
	ds.mu.Unlock()
	if changed {
		s.updateDispenserComparatorsAndBroadcast(normDim, x, y, z)
		_ = s.SyncDispenserViewers(nil, x, y, z)
	}
	return rem
}

func mergeIntoDispenserLocked(
	be *DispenserBlockEntity,
	stack world.ItemStack,
	meta ItemMeta,
	potionType string,
) world.ItemStack {
	if be == nil || stack.IsEmpty() || stack.Count <= 0 {
		return world.ItemStack{}
	}
	maxStack := DispenserItemMaxStackSize(stack)
	remaining := stack.Count
	itemID := stack.ResolvedItemID()

	// 1. Merge into existing matching non-full stacks if stackable and metadata-compatible.
	if maxStack > 1 {
		for i := 0; i < DispenserSlotCount && remaining > 0; i++ {
			cur := be.Slots[i]
			if cur.IsEmpty() || cur.ResolvedItemID() != itemID || cur.Count >= maxStack {
				continue
			}
			if be.SlotPotions[i] != potionType || !dispenserItemMetaEqual(be.SlotMeta[i], meta) {
				continue
			}
			space := maxStack - cur.Count
			take := remaining
			if take > space {
				take = space
			}
			be.Slots[i].Count += take
			remaining -= take
		}
	}

	// 2. Place into empty slots.
	for i := 0; i < DispenserSlotCount && remaining > 0; i++ {
		if !be.Slots[i].IsEmpty() {
			continue
		}
		take := remaining
		if take > maxStack {
			take = maxStack
		}
		placed := world.NewItemStack(itemID, take)
		if placed.IsEmpty() {
			placed = world.NewNamedItemStack(stack.ResolvedID(), take)
		}
		be.Slots[i] = placed
		be.SlotMeta[i] = meta.Clone()
		be.SlotPotions[i] = potionType
		remaining -= take
	}

	if remaining <= 0 {
		return world.ItemStack{}
	}
	out := world.NewItemStack(itemID, remaining)
	if out.IsEmpty() {
		out = world.NewNamedItemStack(stack.ResolvedID(), remaining)
	}
	return out
}

func dispenserItemMetaEqual(a, b ItemMeta) bool {
	if a.Damage != b.Damage || a.CustomName != b.CustomName || a.RepairCost != b.RepairCost {
		return false
	}
	if len(a.Enchantments) != len(b.Enchantments) {
		return false
	}
	for k, v := range a.Enchantments {
		if b.EnchantmentLevel(k) != v {
			return false
		}
	}
	return true
}

func clampDispenserFloat(v, minV, maxV float64) float64 {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

// consumeWithRemainderLocked decrements 1 item from `slot` in `be` and places
// `remainder` (with `remPotion`) into `slot` if it became empty, or merges it
// into another slot of `be` (returning any overflow stack to be ejected into the world).
func consumeWithRemainderLocked(
	be *DispenserBlockEntity,
	slot int,
	remainder world.ItemStack,
	remPotion string,
) (overflow world.ItemStack) {
	if be == nil || slot < 0 || slot >= DispenserSlotCount {
		return remainder
	}
	cur := be.Slots[slot]
	if cur.Count <= 1 {
		be.Slots[slot] = remainder
		be.SlotMeta[slot] = ItemMeta{}
		be.SlotPotions[slot] = remPotion
		be.SlotFireworks[slot] = FireworksComponent{}
		be.SlotShulkerItems[slot] = [ChestSlotCount]world.ItemStack{}
		return world.ItemStack{}
	}
	be.Slots[slot].Count--
	if remainder.IsEmpty() {
		return world.ItemStack{}
	}
	return mergeIntoDispenserLocked(be, remainder, ItemMeta{}, remPotion)
}

func decrementDispenserSlotLocked(be *DispenserBlockEntity, slot int) {
	if be == nil || slot < 0 || slot >= DispenserSlotCount {
		return
	}
	if be.Slots[slot].Count <= 1 {
		be.Slots[slot] = world.ItemStack{}
		be.SlotMeta[slot] = ItemMeta{}
		be.SlotPotions[slot] = ""
		be.SlotFireworks[slot] = FireworksComponent{}
		be.SlotShulkerItems[slot] = [ChestSlotCount]world.ItemStack{}
	} else {
		be.Slots[slot].Count--
	}
}

func damageDispenserToolSlotLocked(be *DispenserBlockEntity, slot int, maxDurability int32, rng *rand.Rand) (broke bool) {
	if be == nil || slot < 0 || slot >= DispenserSlotCount || be.Slots[slot].IsEmpty() {
		return false
	}
	meta := be.SlotMeta[slot]
	unbreaking := meta.EnchantmentLevel("minecraft:unbreaking")
	if unbreaking > 0 && rng != nil {
		// Vanilla tool Unbreaking formula: 1 / (level + 1) chance to consume durability.
		if rng.Intn(int(unbreaking)+1) != 0 {
			return false
		}
	}
	meta.Damage++
	if meta.Damage >= maxDurability {
		decrementDispenserSlotLocked(be, slot)
		return true
	}
	be.SlotMeta[slot] = meta
	return false
}

// spawnDispensedItemEntity spawns a dropped item entity in front of the
// dispenser/dropper at (x, y, z) with vanilla velocity and Gaussian spread.
func (s *Server) spawnDispensedItemEntity(dim string, x, y, z int, facing string, stack world.ItemStack, rng *rand.Rand) int32 {
	if s == nil || stack.IsEmpty() {
		return 0
	}
	normDim := normalizeDispenserDim(dim)
	px, py, pz := DispenserDispensePosition(x, y, z, facing, false)
	vx, vy, vz := DispenserItemVelocity(facing, rng)
	entID, ent := s.SpawnItemEntityInDimension(normDim, px, py, pz, stack)
	if ent != nil {
		ent.VelocityX = int16(clampDispenserFloat(vx*8000, -32767, 32767))
		ent.VelocityY = int16(clampDispenserFloat(vy*8000, -32767, 32767))
		ent.VelocityZ = int16(clampDispenserFloat(vz*8000, -32767, 32767))
	}
	return entID
}

// --- Main Entry Point: FireDispenserOrDropperAt & DispenseFromDispenser ---

// FireDispenserOrDropperAt activates the `minecraft:dispenser` or
// `minecraft:dropper` at (dim, x, y, z), dispatching to DispenseFromDropper for
// droppers and DispenseFromDispenser for dispensers.
func (s *Server) FireDispenserOrDropperAt(dim string, x, y, z int) bool {
	if s == nil {
		return false
	}
	normDim := normalizeDispenserDim(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	switch world.BlockName(state) {
	case "minecraft:dropper":
		return s.DispenseFromDropper(normDim, x, y, z)
	case "minecraft:dispenser":
		return s.DispenseFromDispenser(normDim, x, y, z)
	default:
		return false
	}
}

// DispenseFromDispenser executes a single activation of the `minecraft:dispenser`
// at (dim, x, y, z), selecting a random non-empty slot and running its vanilla
// 26.1.2 DispenseItemBehavior.
func (s *Server) DispenseFromDispenser(dim string, x, y, z int) bool {
	if s == nil {
		return false
	}
	normDim := normalizeDispenserDim(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	if world.BlockName(state) != "minecraft:dispenser" {
		return false
	}
	facing := s.DispenserFacingAt(normDim, x, y, z)

	ds := s.dispenserState()
	ds.mu.Lock()
	be := s.getOrCreateDispenserLocked(ds, normDim, x, y, z)
	slot := ChooseRandomDispenserSlot(be.Slots, ds.rng)
	if slot < 0 {
		ds.mu.Unlock()
		s.broadcastDispenserEffects(normDim, x, y, z, facing, LevelEventSoundFailDispense, false)
		s.EmitGameEvent(normDim, GameEventBlockActivate, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, VibrationSource{})
		return false
	}
	item := be.Slots[slot]
	meta := be.SlotMeta[slot].Clone()
	potionType := be.SlotPotions[slot]
	fireworkData := be.SlotFireworks[slot].Clone()
	shulkerItems := be.SlotShulkerItems[slot]
	rng := ds.rng
	ds.mu.Unlock()

	itemID := canonicalResourceName(item.ResolvedID())
	dx, dy, dz := DispenserFacingVector(facing)
	fx, fy, fz := x+dx, y+dy, z+dz

	var (
		succeeded   = true
		soundEvent  = LevelEventSoundDispense
		emitSmoke   = true
		consumeOne  = false
		replaceWith *world.ItemStack
		replacePot  string
		damageTool  int32
	)

	switch {
	// 1. Projectiles: Arrows, Tipped Arrows, Spectral Arrows, Snowballs, Eggs,
	//    Splash/Lingering Potions, Experience Bottles, Firework Rockets, Fire Charges, Wind Charges.
	case itemID == "minecraft:arrow" || itemID == "minecraft:tipped_arrow" || itemID == "minecraft:spectral_arrow":
		s.dispenseArrowProjectile(normDim, x, y, z, facing, itemID, potionType, rng)
		soundEvent = LevelEventSoundLaunchProjectile
		consumeOne = true

	case itemID == "minecraft:snowball" || itemID == "minecraft:egg" || itemID == "minecraft:wind_charge":
		s.dispenseThrownProjectile(normDim, x, y, z, facing, itemID, rng)
		soundEvent = LevelEventSoundLaunchProjectile
		consumeOne = true

	case itemID == "minecraft:splash_potion" || itemID == "minecraft:lingering_potion":
		s.dispensePotionProjectile(normDim, x, y, z, facing, itemID, potionType, rng)
		soundEvent = LevelEventSoundLaunchProjectile
		consumeOne = true

	case itemID == "minecraft:experience_bottle":
		s.dispenseExperienceBottleProjectile(normDim, x, y, z, facing, rng)
		soundEvent = LevelEventSoundLaunchProjectile
		consumeOne = true

	case itemID == "minecraft:firework_rocket":
		s.dispenseFireworkRocket(normDim, x, y, z, facing, fireworkData, rng)
		soundEvent = LevelEventSoundFireworkShoot
		consumeOne = true

	case itemID == "minecraft:fire_charge":
		s.dispenseFireCharge(normDim, x, y, z, facing, fx, fy, fz, rng)
		soundEvent = LevelEventSoundFireChargeShoot
		consumeOne = true

	// 2. Buckets: Water, Lava, Powder Snow, Fish/Axolotl/Tadpole Buckets, and Empty Bucket.
	case isFilledDispenserBucket(itemID):
		if s.dispenseFilledBucket(normDim, cache, fx, fy, fz, itemID) {
			emptyBucket := world.NewNamedItemStack("minecraft:bucket", 1)
			replaceWith = &emptyBucket
		} else {
			// Vanilla fallback: eject the filled bucket item.
			single := world.NewNamedItemStack(itemID, 1)
			s.spawnDispensedItemEntity(normDim, x, y, z, facing, single, rng)
			consumeOne = true
		}

	case itemID == "minecraft:bucket":
		if filledBucket, ok := s.dispenseEmptyBucketScoop(normDim, cache, fx, fy, fz); ok {
			replaceWith = &filledBucket
		} else {
			single := world.NewNamedItemStack("minecraft:bucket", 1)
			s.spawnDispensedItemEntity(normDim, x, y, z, facing, single, rng)
			consumeOne = true
		}

	// 3. Bone Meal.
	case itemID == "minecraft:bone_meal":
		applied, res := cache.ApplyBoneMeal(fx, fy, fz, rng)
		if applied {
			if len(res.Changes) > 0 {
				s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
			}
			s.BroadcastDimensionChunk(normDim, int32(fx>>4), int32(fz>>4), PlayLevelEventCB,
				EncodeLevelEventPacket(LevelEventParticlesBoneMeal, fx, fy, fz, 15, false))
			consumeOne = true
		} else {
			succeeded = false
			soundEvent = LevelEventSoundFailDispense
			emitSmoke = false
		}

	// 4. Shulker Box placement.
	case IsShulkerBoxItemOrBlock(itemID):
		if s.dispensePlaceShulkerBox(normDim, cache, fx, fy, fz, facing, itemID, shulkerItems) {
			consumeOne = true
		} else {
			succeeded = false
			soundEvent = LevelEventSoundFailDispense
			emitSmoke = false
		}

	// 5. Armor, Elytra, Shield, Heads/Skulls, and Carved Pumpkin.
	case isDispenserEquippableOrSkullOrPumpkin(itemID):
		equippedOrPlaced, isOptionalFail := s.dispenseEquipOrPlaceHeadPumpkin(normDim, cache, x, y, z, fx, fy, fz, facing, itemID, meta, rng)
		if equippedOrPlaced {
			consumeOne = true
		} else if isOptionalFail {
			succeeded = false
			soundEvent = LevelEventSoundFailDispense
			emitSmoke = false
		} else {
			// Normal armor / elytra / shield falls back to dropping the item if no entity in front can equip it.
			single := world.NewNamedItemStack(itemID, 1)
			s.spawnDispensedItemEntity(normDim, x, y, z, facing, single, rng)
			consumeOne = true
		}

	// 6. Saddles, Horse Armor, Wolf Armor, Llama Carpets, and Equine Chests.
	case isDispenserAnimalEquipment(itemID, normDim, fx, fy, fz, s):
		if s.dispenseAnimalEquipment(normDim, fx, fy, fz, itemID) {
			consumeOne = true
		} else {
			succeeded = false
			soundEvent = LevelEventSoundFailDispense
			emitSmoke = false
		}

	// 7. Minecarts & Boats / Rafts.
	case isMinecartItem(itemID):
		if s.dispenseMinecart(normDim, cache, fx, fy, fz, facing, itemID) {
			consumeOne = true
		} else {
			single := world.NewNamedItemStack(itemID, 1)
			s.spawnDispensedItemEntity(normDim, x, y, z, facing, single, rng)
			consumeOne = true
		}

	case isBoatOrRaftItem(itemID):
		if s.dispenseBoat(normDim, cache, x, y, z, fx, fy, fz, facing, itemID) {
			consumeOne = true
		} else {
			single := world.NewNamedItemStack(itemID, 1)
			s.spawnDispensedItemEntity(normDim, x, y, z, facing, single, rng)
			consumeOne = true
		}

	// 8. Flint and Steel.
	case itemID == "minecraft:flint_and_steel":
		if s.dispenseFlintAndSteel(normDim, cache, fx, fy, fz) {
			damageTool = FlintAndSteelMaxDurability
		} else {
			succeeded = false
			soundEvent = LevelEventSoundFailDispense
			emitSmoke = false
		}

	// 9. Primed TNT.
	case itemID == "minecraft:tnt":
		if fy >= world.MinY && fy < world.MinY+world.WorldHeight {
			s.SpawnPrimedTNT(normDim, float64(fx)+0.5, float64(fy), float64(fz)+0.5, DefaultPrimedTNTFuseTicks)
			consumeOne = true
		} else {
			succeeded = false
			soundEvent = LevelEventSoundFailDispense
			emitSmoke = false
		}

	// 10. Shears (Sheep, Mooshroom, Snow Golem, Bogged, Beehive/Bee Nest, Pumpkin).
	case itemID == "minecraft:shears":
		if s.dispenseShears(normDim, cache, fx, fy, fz, facing, rng) {
			damageTool = ShearsMaxDurability
		} else {
			succeeded = false
			soundEvent = LevelEventSoundFailDispense
			emitSmoke = false
		}

	// 11. Glass Bottle (Water source / waterlogged / water cauldron -> water potion; Beehive honey_level=5 -> honey_bottle).
	case itemID == "minecraft:glass_bottle":
		if filledBottle, filledPot, ok := s.dispenseGlassBottle(normDim, cache, fx, fy, fz); ok {
			replaceWith = &filledBottle
			replacePot = filledPot
		} else {
			single := world.NewNamedItemStack("minecraft:glass_bottle", 1)
			s.spawnDispensedItemEntity(normDim, x, y, z, facing, single, rng)
			consumeOne = true
		}

	// 12. Honeycomb (Waxing Copper Blocks & Signs).
	case itemID == "minecraft:honeycomb":
		if s.dispenseHoneycombWax(normDim, cache, fx, fy, fz) {
			consumeOne = true
		} else {
			single := world.NewNamedItemStack("minecraft:honeycomb", 1)
			s.spawnDispensedItemEntity(normDim, x, y, z, facing, single, rng)
			consumeOne = true
		}

	// 13. Default behavior: eject 1 item into the world.
	default:
		single := world.NewItemStack(item.ResolvedItemID(), 1)
		if single.IsEmpty() {
			single = world.NewNamedItemStack(itemID, 1)
		}
		s.spawnDispensedItemEntity(normDim, x, y, z, facing, single, rng)
		consumeOne = true
	}

	// Apply slot mutations under lock.
	var overflow world.ItemStack
	if consumeOne || replaceWith != nil || damageTool > 0 {
		ds.mu.Lock()
		be = s.getOrCreateDispenserLocked(ds, normDim, x, y, z)
		if replaceWith != nil {
			overflow = consumeWithRemainderLocked(be, slot, *replaceWith, replacePot)
		} else if damageTool > 0 {
			damageDispenserToolSlotLocked(be, slot, damageTool, ds.rng)
		} else if consumeOne {
			decrementDispenserSlotLocked(be, slot)
		}
		ds.mu.Unlock()
	}
	if !overflow.IsEmpty() {
		s.spawnDispensedItemEntity(normDim, x, y, z, facing, overflow, rng)
	}

	s.broadcastDispenserEffects(normDim, x, y, z, facing, soundEvent, emitSmoke)
	s.EmitGameEvent(normDim, GameEventBlockActivate, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, VibrationSource{})
	s.updateDispenserComparatorsAndBroadcast(normDim, x, y, z)
	_ = s.SyncDispenserViewers(nil, x, y, z)
	return succeeded
}

// --- Projectile Behaviors ---

func (s *Server) dispenseArrowProjectile(dim string, x, y, z int, facing, itemID, potionType string, rng *rand.Rand) int32 {
	px, py, pz := DispenserDispensePosition(x, y, z, facing, true)
	vx, vy, vz := DispenserProjectileVelocity(facing, DispenserDefaultProjectilePower, DispenserDefaultProjectileUncertainty, rng)
	entTypeName := MobTypeArrow
	if itemID == "minecraft:spectral_arrow" {
		entTypeName = EntityTypeSpectralArrow
	}
	entID := s.spawnDispenserProjectileEntity(dim, entTypeName, px, py, pz, vx, vy, vz)
	proj := &DispenserProjectile{
		EntityID:   entID,
		TypeName:   entTypeName,
		Dimension:  dim,
		X:          px,
		Y:          py,
		Z:          pz,
		VX:         vx,
		VY:         vy,
		VZ:         vz,
		PotionType: potionType,
		Spectral:   itemID == "minecraft:spectral_arrow",
		BaseDamage: 2.0,
		MaxTicks:   200,
	}
	if itemID == "minecraft:tipped_arrow" && proj.PotionType == "" {
		proj.PotionType = PotionWater
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	ds.projectiles[entID] = proj
	ds.mu.Unlock()

	// Resolve immediate short-range entity impact in front block (fx, fy, fz) so
	// adjacent entities hit on the dispense tick receive damage & tipped/spectral effects.
	dx, dy, dz := DispenserFacingVector(facing)
	s.resolveImmediateDispenserArrowHit(proj, x+dx, y+dy, z+dz)
	s.EmitGameEvent(dim, GameEventProjectileShoot, px, py, pz, VibrationSource{EntityID: entID})
	return entID
}

func (s *Server) resolveImmediateDispenserArrowHit(proj *DispenserProjectile, fx, fy, fz int) {
	if s == nil || proj == nil || proj.Impacted {
		return
	}
	// Check players in the front cell.
	for _, sess := range s.playersInBlockBox(proj.Dimension, fx, fy, fz) {
		proj.Impacted = true
		s.DamagePlayerWithArmor(sess, 4.0, "arrow")
		if proj.Spectral {
			s.ApplyPlayerEffect(sess, NewStatusEffectInstance(EffectGlowing, 200, 0))
		}
		if proj.PotionType != "" {
			for _, ef := range PotionEffects(proj.PotionType) {
				if def, ok := MobEffectByID(ef.ID); ok && def.Instantaneous {
					s.ApplyPlayerEffectScaled(sess, ef, 1.0)
				} else {
					scaled := ef.Clone()
					scaled.Duration = ef.Duration / 8
					if scaled.Duration < 1 {
						scaled.Duration = 1
					}
					s.ApplyPlayerEffect(sess, scaled)
				}
			}
		}
		return
	}
	// Check mobs in the front cell.
	for _, ent := range s.mobsInBlockBox(proj.Dimension, fx, fy, fz) {
		proj.Impacted = true
		curHP := s.MobHealth(ent.ID)
		if curHP > 0 {
			nextHP := curHP - 4.0
			if nextHP < 0 {
				nextHP = 0
			}
			s.SetMobHealth(ent.ID, nextHP)
		}
		if proj.Spectral {
			s.ApplyEntityEffectScaled(ent.ID, NewStatusEffectInstance(EffectGlowing, 200, 0), 1.0)
		}
		if proj.PotionType != "" {
			for _, ef := range PotionEffects(proj.PotionType) {
				if def, ok := MobEffectByID(ef.ID); ok && def.Instantaneous {
					s.ApplyEntityEffectScaled(ent.ID, ef, 1.0)
				} else {
					s.ApplyEntityEffectScaled(ent.ID, ef, 0.125)
				}
			}
		}
		return
	}
}

func (s *Server) dispenseThrownProjectile(dim string, x, y, z int, facing, itemID string, rng *rand.Rand) int32 {
	px, py, pz := DispenserDispensePosition(x, y, z, facing, true)
	vx, vy, vz := DispenserProjectileVelocity(facing, DispenserDefaultProjectilePower, DispenserDefaultProjectileUncertainty, rng)
	entID := s.spawnDispenserProjectileEntity(dim, itemID, px, py, pz, vx, vy, vz)
	proj := &DispenserProjectile{
		EntityID:  entID,
		TypeName:  itemID,
		Dimension: dim,
		X:         px,
		Y:         py,
		Z:         pz,
		VX:        vx,
		VY:        vy,
		VZ:        vz,
		MaxTicks:  100,
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	ds.projectiles[entID] = proj
	ds.mu.Unlock()

	dx, dy, dz := DispenserFacingVector(facing)
	fx, fy, fz := x+dx, y+dy, z+dz
	switch itemID {
	case "minecraft:snowball":
		for _, ent := range s.mobsInBlockBox(dim, fx, fy, fz) {
			if canonicalResourceName(ent.TypeName) == "minecraft:blaze" {
				curHP := s.MobHealth(ent.ID)
				if curHP > 0 {
					s.SetMobHealth(ent.ID, maxFloat32(0, curHP-3.0))
				}
			}
		}
	case "minecraft:egg":
		// Vanilla ThrownEgg.onHit: 1/8 chance to hatch a baby chicken (1/256 for 4).
		if rng != nil && rng.Intn(8) == 0 {
			chicks := 1
			if rng.Intn(32) == 0 {
				chicks = 4
			}
			for i := 0; i < chicks; i++ {
				_, _ = s.SpawnBabyMob(dim, float64(fx)+0.5, float64(fy), float64(fz)+0.5, MobTypeChicken)
			}
		}
	}
	s.EmitGameEvent(dim, GameEventProjectileShoot, px, py, pz, VibrationSource{EntityID: entID})
	return entID
}

func (s *Server) dispensePotionProjectile(dim string, x, y, z int, facing, itemID, potionType string, rng *rand.Rand) int32 {
	if potionType == "" {
		potionType = PotionWater
	}
	px, py, pz := DispenserDispensePosition(x, y, z, facing, true)
	vx, vy, vz := DispenserProjectileVelocity(facing, DispenserPotionProjectilePower, DispenserPotionProjectileUncertainty, rng)
	entID := s.spawnDispenserProjectileEntity(dim, itemID, px, py, pz, vx, vy, vz)

	dx, dy, dz := DispenserFacingVector(facing)
	hitX, hitY, hitZ := float64(x+dx)+0.5, float64(y+dy)+0.5, float64(z+dz)+0.5
	var directHitID int32
	if ents := s.mobsInBlockBox(dim, x+dx, y+dy, z+dz); len(ents) > 0 {
		directHitID = ents[0].ID
	}
	s.broadcastPotionBreakEffect(dim, hitX, hitY, hitZ, potionType)
	if itemID == "minecraft:lingering_potion" {
		s.SpawnLingeringPotionCloud(dim, hitX, float64(y+dy), hitZ, potionType)
	} else {
		s.ApplySplashPotionAt(dim, hitX, hitY, hitZ, potionType, directHitID)
	}
	s.EmitGameEvent(dim, GameEventProjectileShoot, px, py, pz, VibrationSource{EntityID: entID})
	return entID
}

func (s *Server) dispenseExperienceBottleProjectile(dim string, x, y, z int, facing string, rng *rand.Rand) int32 {
	px, py, pz := DispenserDispensePosition(x, y, z, facing, true)
	vx, vy, vz := DispenserProjectileVelocity(facing, DispenserPotionProjectilePower, DispenserPotionProjectileUncertainty, rng)
	entID := s.spawnDispenserProjectileEntity(dim, EntityTypeExperienceBottle, px, py, pz, vx, vy, vz)

	dx, dy, dz := DispenserFacingVector(facing)
	fx, fy, fz := x+dx, y+dy, z+dz
	xp := int32(3)
	if rng != nil {
		xp += int32(rng.Intn(5) + rng.Intn(5))
	} else {
		xp = 7
	}
	s.BroadcastDimensionChunk(dim, int32(fx>>4), int32(fz>>4), PlayLevelEventCB,
		EncodeLevelEventPacket(LevelEventParticlesPotionSplash, fx, fy, fz, 0x385DC6, false))
	s.SpawnExperienceOrbsInDimension(dim, float64(fx)+0.5, float64(fy)+0.5, float64(fz)+0.5, xp)
	s.EmitGameEvent(dim, GameEventProjectileShoot, px, py, pz, VibrationSource{EntityID: entID})
	return entID
}

func (s *Server) dispenseFireworkRocket(dim string, x, y, z int, facing string, fw FireworksComponent, rng *rand.Rand) int32 {
	dx, dy, dz := DispenserFacingVector(facing)
	px := float64(x) + 0.5 + float64(dx)*0.6
	py := float64(y) + 0.5 + float64(dy)*0.6
	pz := float64(z) + 0.5 + float64(dz)*0.6
	vx, vy, vz := DispenserProjectileVelocity(facing, 0.5, 1.0, rng)
	if fw.FlightDuration <= 0 {
		fw.FlightDuration = DefaultFireworkFlightDuration
	}
	entID, _ := s.SpawnFireworkRocket(dim, px, py, pz, vx, vy, vz, fw, nil, nil, true)
	s.EmitGameEvent(dim, GameEventProjectileShoot, px, py, pz, VibrationSource{EntityID: entID})
	return entID
}

func (s *Server) dispenseFireCharge(dim string, x, y, z int, facing string, fx, fy, fz int, rng *rand.Rand) int32 {
	px, py, pz := DispenserDispensePosition(x, y, z, facing, true)
	vx, vy, vz := DispenserProjectileVelocity(facing, 1.0, 2.0, rng)
	entID := s.spawnDispenserProjectileEntity(dim, EntityTypeSmallFireball, px, py, pz, vx, vy, vz)
	proj := &DispenserProjectile{
		EntityID:   entID,
		TypeName:   EntityTypeSmallFireball,
		Dimension:  dim,
		X:          px,
		Y:          py,
		Z:          pz,
		VX:         vx,
		VY:         vy,
		VZ:         vz,
		BaseDamage: 5.0,
		MaxTicks:   200,
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	ds.projectiles[entID] = proj
	ds.mu.Unlock()

	// If an entity is directly in front, apply 5.0 fire damage; otherwise if the
	// front cell is air with solid support below (or an unlit campfire / TNT), ignite it.
	if players := s.playersInBlockBox(dim, fx, fy, fz); len(players) > 0 {
		s.DamagePlayerWithArmor(players[0], 5.0, "fireball")
	} else if mobs := s.mobsInBlockBox(dim, fx, fy, fz); len(mobs) > 0 {
		curHP := s.MobHealth(mobs[0].ID)
		if curHP > 0 {
			s.SetMobHealth(mobs[0].ID, maxFloat32(0, curHP-5.0))
		}
	} else if cache := s.DimensionChunks(dim); cache != nil {
		_ = s.dispenseFlintAndSteel(dim, cache, fx, fy, fz)
	}
	s.EmitGameEvent(dim, GameEventProjectileShoot, px, py, pz, VibrationSource{EntityID: entID})
	return entID
}

func (s *Server) spawnDispenserProjectileEntity(dim, typeName string, x, y, z, vx, vy, vz float64) int32 {
	if s == nil || s.entities == nil {
		return 0
	}
	normDim := normalizeDispenserDim(dim)
	typeID := ResolveEntityTypeID(typeName)
	if typeID < 0 {
		typeID = EntityTypeArrowID
	}
	ent := &world.Entity{
		TypeID:    typeID,
		TypeName:  canonicalResourceName(typeName),
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		VelocityX: int16(clampDispenserFloat(vx*8000, -32767, 32767)),
		VelocityY: int16(clampDispenserFloat(vy*8000, -32767, 32767)),
		VelocityZ: int16(clampDispenserFloat(vz*8000, -32767, 32767)),
	}
	id := s.entities.Add(ent)
	if spawned := s.entities.Get(id); spawned != nil {
		cp := spawned.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, spawned))
	}
	return id
}

// DispenserProjectileByID returns the tracked DispenserProjectile state for entityID.
func (s *Server) DispenserProjectileByID(entityID int32) (DispenserProjectile, bool) {
	if s == nil {
		return DispenserProjectile{}, false
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	defer ds.mu.Unlock()
	p, ok := ds.projectiles[entityID]
	if !ok || p == nil {
		return DispenserProjectile{}, false
	}
	return *p, true
}

// --- Bucket Behaviors (Water, Lava, Powder Snow, Fish Buckets, Empty Bucket) ---

func isFilledDispenserBucket(itemID string) bool {
	switch canonicalResourceName(itemID) {
	case "minecraft:water_bucket", "minecraft:lava_bucket", "minecraft:powder_snow_bucket",
		"minecraft:cod_bucket", "minecraft:salmon_bucket", "minecraft:tropical_fish_bucket",
		"minecraft:pufferfish_bucket", "minecraft:axolotl_bucket", "minecraft:tadpole_bucket":
		return true
	default:
		return false
	}
}

func fishMobForBucket(itemID string) string {
	switch canonicalResourceName(itemID) {
	case "minecraft:cod_bucket":
		return "minecraft:cod"
	case "minecraft:salmon_bucket":
		return "minecraft:salmon"
	case "minecraft:tropical_fish_bucket":
		return "minecraft:tropical_fish"
	case "minecraft:pufferfish_bucket":
		return "minecraft:pufferfish"
	case "minecraft:axolotl_bucket":
		return "minecraft:axolotl"
	case "minecraft:tadpole_bucket":
		return "minecraft:tadpole"
	default:
		return ""
	}
}

func (s *Server) dispenseFilledBucket(dim string, cache *world.Cache, fx, fy, fz int, itemID string) bool {
	if cache == nil || fy < world.MinY || fy >= world.MinY+world.WorldHeight {
		return false
	}
	targetState := cache.GetBlock(fx, fy, fz)
	targetName := world.BlockName(targetState)
	props := world.BlockProperties(targetState)

	switch itemID {
	case "minecraft:lava_bucket":
		if targetName == "minecraft:cauldron" {
			if st, ok := world.BlockStateByName("minecraft:lava_cauldron", nil); ok {
				s.setDispenserWorldBlock(dim, cache, fx, fy, fz, st)
				return true
			}
		}
		if !isDispenserFluidReplaceable(targetState) {
			return false
		}
		s.setDispenserWorldBlock(dim, cache, fx, fy, fz, world.StateLava)
		return true

	case "minecraft:powder_snow_bucket":
		if targetName == "minecraft:cauldron" {
			if st, ok := world.BlockStateByName("minecraft:powder_snow_cauldron", map[string]string{"level": "3"}); ok {
				s.setDispenserWorldBlock(dim, cache, fx, fy, fz, st)
				return true
			}
		}
		if !isDispenserFluidReplaceable(targetState) {
			return false
		}
		if st, ok := world.BlockStateByName("minecraft:powder_snow", nil); ok {
			s.setDispenserWorldBlock(dim, cache, fx, fy, fz, st)
			return true
		}
		return false

	default: // water_bucket and fish/axolotl/tadpole buckets
		placedWater := false
		if targetName == "minecraft:cauldron" && itemID == "minecraft:water_bucket" {
			if st, ok := world.BlockStateByName("minecraft:water_cauldron", map[string]string{"level": "3"}); ok {
				s.setDispenserWorldBlock(dim, cache, fx, fy, fz, st)
				placedWater = true
			}
		} else if _, hasWL := props["waterlogged"]; hasWL && props["waterlogged"] == "false" {
			if nextSt, ok := world.WithBlockProperty(targetState, "waterlogged", "true"); ok {
				s.setDispenserWorldBlock(dim, cache, fx, fy, fz, nextSt)
				placedWater = true
			}
		} else if isDispenserFluidReplaceable(targetState) {
			if dim == world.DimensionNether {
				placedWater = true
			} else {
				s.setDispenserWorldBlock(dim, cache, fx, fy, fz, world.StateWater)
				placedWater = true
			}
		}
		if !placedWater {
			return false
		}
		if mobType := fishMobForBucket(itemID); mobType != "" {
			s.spawnBucketAquaticMob(dim, float64(fx)+0.5, float64(fy), float64(fz)+0.5, mobType)
		}
		return true
	}
}

func (s *Server) spawnBucketAquaticMob(dim string, x, y, z float64, mobType string) int32 {
	if s == nil || s.entities == nil {
		return 0
	}
	normDim := normalizeDispenserDim(dim)
	norm := canonicalResourceName(mobType)
	if id, err := s.SpawnMobInDimension(normDim, x, y, z, norm); err == nil && id > 0 {
		return id
	}
	typeID := ResolveEntityTypeID(norm)
	if typeID < 0 {
		switch norm {
		case "minecraft:axolotl":
			typeID = 8
		case "minecraft:cod":
			typeID = 27
		case "minecraft:pufferfish":
			typeID = 105
		case "minecraft:salmon":
			typeID = 109
		case "minecraft:tadpole":
			typeID = 130
		case "minecraft:tropical_fish":
			typeID = 137
		default:
			typeID = 27
		}
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  norm,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})
	s.SetMobHealth(id, 3.0)
	if spawned := s.entities.Get(id); spawned != nil {
		cp := spawned.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, spawned))
	}
	return id
}

func (s *Server) dispenseEmptyBucketScoop(dim string, cache *world.Cache, fx, fy, fz int) (world.ItemStack, bool) {
	if cache == nil || fy < world.MinY || fy >= world.MinY+world.WorldHeight {
		return world.ItemStack{}, false
	}
	targetState := cache.GetBlock(fx, fy, fz)
	targetName := world.BlockName(targetState)
	props := world.BlockProperties(targetState)

	switch targetName {
	case "minecraft:water":
		if props["level"] == "" || props["level"] == "0" {
			s.setDispenserWorldBlock(dim, cache, fx, fy, fz, world.StateAir)
			return world.NewNamedItemStack("minecraft:water_bucket", 1), true
		}
	case "minecraft:lava":
		if props["level"] == "" || props["level"] == "0" {
			s.setDispenserWorldBlock(dim, cache, fx, fy, fz, world.StateAir)
			return world.NewNamedItemStack("minecraft:lava_bucket", 1), true
		}
	case "minecraft:powder_snow":
		s.setDispenserWorldBlock(dim, cache, fx, fy, fz, world.StateAir)
		return world.NewNamedItemStack("minecraft:powder_snow_bucket", 1), true
	case "minecraft:water_cauldron":
		if props["level"] == "3" {
			if emptyCauldron, ok := world.BlockStateByName("minecraft:cauldron", nil); ok {
				s.setDispenserWorldBlock(dim, cache, fx, fy, fz, emptyCauldron)
				return world.NewNamedItemStack("minecraft:water_bucket", 1), true
			}
		}
	case "minecraft:lava_cauldron":
		if emptyCauldron, ok := world.BlockStateByName("minecraft:cauldron", nil); ok {
			s.setDispenserWorldBlock(dim, cache, fx, fy, fz, emptyCauldron)
			return world.NewNamedItemStack("minecraft:lava_bucket", 1), true
		}
	case "minecraft:powder_snow_cauldron":
		if props["level"] == "3" {
			if emptyCauldron, ok := world.BlockStateByName("minecraft:cauldron", nil); ok {
				s.setDispenserWorldBlock(dim, cache, fx, fy, fz, emptyCauldron)
				return world.NewNamedItemStack("minecraft:powder_snow_bucket", 1), true
			}
		}
	default:
		if props["waterlogged"] == "true" {
			if nextSt, ok := world.WithBlockProperty(targetState, "waterlogged", "false"); ok {
				s.setDispenserWorldBlock(dim, cache, fx, fy, fz, nextSt)
				return world.NewNamedItemStack("minecraft:water_bucket", 1), true
			}
		}
	}
	return world.ItemStack{}, false
}

func isDispenserFluidReplaceable(state uint16) bool {
	if state == world.StateAir {
		return true
	}
	name := world.BlockName(state)
	switch name {
	case "minecraft:air", "minecraft:cave_air", "minecraft:void_air",
		"minecraft:water", "minecraft:lava",
		"minecraft:short_grass", "minecraft:tall_grass", "minecraft:fern", "minecraft:large_fern",
		"minecraft:dead_bush", "minecraft:fire", "minecraft:soul_fire", "minecraft:snow",
		"minecraft:vine", "minecraft:glow_lichen":
		return true
	default:
		return false
	}
}

func (s *Server) setDispenserWorldBlock(dim string, cache *world.Cache, x, y, z int, nextState uint16) {
	if cache == nil {
		return
	}
	_, res := cache.ApplyBlockWithUpdates(x, y, z, nextState)
	if len(res.Changes) > 0 {
		s.broadcastDimensionBlockChanges(dim, cache, res.Changes, res.LightChanged)
	}
	s.updateDispenserComparatorsAndBroadcast(dim, x, y, z)
}

// --- Shulker Box Placement ---

func (s *Server) dispensePlaceShulkerBox(
	dim string,
	cache *world.Cache,
	fx, fy, fz int,
	dispenserFacing string,
	itemID string,
	shulkerItems [ChestSlotCount]world.ItemStack,
) bool {
	if cache == nil || fy < world.MinY || fy >= world.MinY+world.WorldHeight {
		return false
	}
	cur := cache.GetBlock(fx, fy, fz)
	if !isDispenserPlaceReplaceable(cur) {
		return false
	}
	placeFacing := dispenserFacing
	if placeFacing == "" {
		placeFacing = "up"
	}
	st, ok := world.BlockStateByName(itemID, map[string]string{"facing": placeFacing})
	if !ok {
		st, ok = world.BlockStateByName(itemID, nil)
		if !ok {
			return false
		}
	}
	s.setDispenserWorldBlock(dim, cache, fx, fy, fz, st)
	s.SetShulkerBoxSlots(dim, fx, fy, fz, shulkerItems)
	// Also mirror into ChestSlots for Overworld so standard chest UI can view it if opened.
	if dim == world.DimensionOverworld {
		for i := 0; i < ChestSlotCount; i++ {
			if !shulkerItems[i].IsEmpty() {
				s.SetChestSlot(fx, fy, fz, i, shulkerItems[i])
			}
		}
	}
	return true
}

func isDispenserPlaceReplaceable(state uint16) bool {
	if state == world.StateAir {
		return true
	}
	switch world.BlockName(state) {
	case "minecraft:air", "minecraft:cave_air", "minecraft:void_air",
		"minecraft:water", "minecraft:lava",
		"minecraft:short_grass", "minecraft:tall_grass", "minecraft:fern", "minecraft:large_fern",
		"minecraft:dead_bush", "minecraft:snow", "minecraft:fire", "minecraft:soul_fire":
		return true
	default:
		return false
	}
}

// --- Equipping Armor, Elytra, Shield, Heads/Skulls, and Carved Pumpkin ---

func isSkullOrHeadItem(itemID string) bool {
	switch canonicalResourceName(itemID) {
	case "minecraft:skeleton_skull", "minecraft:wither_skeleton_skull",
		"minecraft:zombie_head", "minecraft:player_head",
		"minecraft:creeper_head", "minecraft:dragon_head", "minecraft:piglin_head":
		return true
	default:
		return false
	}
}

func isDispenserEquippableOrSkullOrPumpkin(itemID string) bool {
	norm := canonicalResourceName(itemID)
	if norm == "minecraft:carved_pumpkin" || norm == "minecraft:elytra" || norm == "minecraft:shield" || norm == "minecraft:turtle_helmet" {
		return true
	}
	if isSkullOrHeadItem(norm) {
		return true
	}
	return strings.HasSuffix(norm, "_helmet") ||
		strings.HasSuffix(norm, "_chestplate") ||
		strings.HasSuffix(norm, "_leggings") ||
		strings.HasSuffix(norm, "_boots")
}

// DispenserEquipmentSlotForItem returns the EquipmentSlot (EquipmentSlotHead..EquipmentSlotOffHand)
// and player inventory slot index (5..8 or 45) for an equippable item.
func DispenserEquipmentSlotForItem(itemID string) (equipSlot int, playerInvSlot int, ok bool) {
	norm := canonicalResourceName(itemID)
	switch {
	case norm == "minecraft:carved_pumpkin" || norm == "minecraft:turtle_helmet" || isSkullOrHeadItem(norm) || strings.HasSuffix(norm, "_helmet"):
		return EquipmentSlotHead, 5, true
	case norm == "minecraft:elytra" || strings.HasSuffix(norm, "_chestplate"):
		return EquipmentSlotChest, 6, true
	case strings.HasSuffix(norm, "_leggings"):
		return EquipmentSlotLegs, 7, true
	case strings.HasSuffix(norm, "_boots"):
		return EquipmentSlotFeet, 8, true
	case norm == "minecraft:shield":
		return EquipmentSlotOffHand, 45, true
	default:
		return 0, 0, false
	}
}

func (s *Server) dispenseEquipOrPlaceHeadPumpkin(
	dim string,
	cache *world.Cache,
	x, y, z, fx, fy, fz int,
	facing string,
	itemID string,
	meta ItemMeta,
	rng *rand.Rand,
) (handled bool, isOptionalFail bool) {
	equipSlot, playerInvSlot, ok := DispenserEquipmentSlotForItem(itemID)
	if !ok {
		return false, false
	}
	single := world.NewNamedItemStack(itemID, 1)

	// 1. Try equipping onto a Player in [fx, fx+1) x [fy, fy+1) x [fz, fz+1).
	for _, sess := range s.playersInBlockBox(dim, fx, fy, fz) {
		if sess.InventorySlot(playerInvSlot).IsEmpty() {
			sess.SetInventorySlot(playerInvSlot, single)
			SetSlotItemMeta(sess, playerInvSlot, meta.Clone())
			s.syncPlayerInventorySlot(sess, playerInvSlot)
			return true, false
		}
	}

	// 2. Try equipping onto an Armor Stand in [fx, fx+1) x [fy, fy+1) x [fz, fz+1).
	for _, ent := range s.armorStandsInBlockBox(dim, fx, fy, fz) {
		st, exists := s.ArmorStandState(ent.ID)
		if !exists || st.IsMarker() || IsArmorStandSlotDisabled(st.DisabledSlots, equipSlot, false, false) {
			continue
		}
		if st.Equipment[equipSlot].IsEmpty() {
			if s.SetArmorStandEquipment(ent.ID, equipSlot, single) {
				return true, false
			}
		}
	}

	// 3. Try equipping onto a humanoid mob in [fx, fx+1) x [fy, fy+1) x [fz, fz+1).
	for _, ent := range s.mobsInBlockBox(dim, fx, fy, fz) {
		if !canMobEquipArmorFromDispenser(ent.TypeName) {
			continue
		}
		cur := s.MobEquipment(ent.ID, equipSlot)
		if cur.IsEmpty() {
			s.SetMobEquipment(ent.ID, equipSlot, single)
			return true, false
		}
	}

	// 4. For carved_pumpkin or skulls/heads: if no entity was equipped, try placing
	// the block at (fx, fy, fz) and checking Iron Golem / Snow Golem / Wither patterns!
	if itemID == "minecraft:carved_pumpkin" {
		if cache != nil && fy >= world.MinY && fy < world.MinY+world.WorldHeight && isDispenserPlaceReplaceable(cache.GetBlock(fx, fy, fz)) {
			horizFacing := facing
			if horizFacing == "up" || horizFacing == "down" {
				horizFacing = "north"
			}
			st, ok := world.BlockStateByName("minecraft:carved_pumpkin", map[string]string{"facing": horizFacing})
			if !ok {
				st, _ = world.BlockStateByName("minecraft:carved_pumpkin", nil)
			}
			if st != world.StateAir {
				s.setDispenserWorldBlock(dim, cache, fx, fy, fz, st)
				if _, ok := s.TrySummonIronGolemAt(dim, fx, fy, fz, nil); !ok {
					_, _ = s.TrySummonSnowGolemAt(dim, fx, fy, fz, nil)
				}
				return true, false
			}
		}
		return false, true
	}
	if isSkullOrHeadItem(itemID) {
		if cache != nil && fy >= world.MinY && fy < world.MinY+world.WorldHeight && isDispenserPlaceReplaceable(cache.GetBlock(fx, fy, fz)) {
			st, ok := world.BlockStateByName(itemID, map[string]string{"rotation": "0"})
			if !ok {
				st, _ = world.BlockStateByName(itemID, nil)
			}
			if st != world.StateAir {
				s.setDispenserWorldBlock(dim, cache, fx, fy, fz, st)
				if itemID == "minecraft:wither_skeleton_skull" {
					_, _ = s.TrySummonWitherAt(dim, fx, fy, fz, nil)
				}
				return true, false
			}
		}
		return false, true
	}

	return false, false
}

func canMobEquipArmorFromDispenser(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case "minecraft:zombie", "minecraft:husk", "minecraft:drowned", "minecraft:zombified_piglin",
		"minecraft:skeleton", "minecraft:stray", "minecraft:bogged", "minecraft:wither_skeleton",
		"minecraft:piglin", "minecraft:piglin_brute", "minecraft:pillager", "minecraft:vindicator",
		"minecraft:evoker", "minecraft:villager":
		return true
	default:
		return false
	}
}

// MobEquipment returns the item equipped in `equipSlot` (0..5) on mob `entityID`.
func (s *Server) MobEquipment(entityID int32, equipSlot int) world.ItemStack {
	if s == nil || equipSlot < 0 || equipSlot >= EquipmentSlotCount {
		return world.ItemStack{}
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	defer ds.mu.Unlock()
	eq := ds.mobEquipment[entityID]
	return eq[equipSlot]
}

// SetMobEquipment sets the item equipped in `equipSlot` (0..5) on mob `entityID`
// and broadcasts ClientboundSetEquipmentPacket to nearby players.
func (s *Server) SetMobEquipment(entityID int32, equipSlot int, stack world.ItemStack) bool {
	if s == nil || equipSlot < 0 || equipSlot >= EquipmentSlotCount {
		return false
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	eq := ds.mobEquipment[entityID]
	eq[equipSlot] = stack
	ds.mobEquipment[entityID] = eq
	ds.mu.Unlock()

	if s.entities != nil {
		if ent := s.entities.Get(entityID); ent != nil {
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlaySetEquipment,
				EncodeArmorStandEquipmentPacket(entityID, eq))
		}
	}
	return true
}

// --- Saddles, Horse Armor, Wolf Armor, Llama Carpets, and Equine Chests ---

func isDispenserAnimalEquipment(itemID, dim string, fx, fy, fz int, s *Server) bool {
	norm := canonicalResourceName(itemID)
	switch norm {
	case "minecraft:saddle",
		"minecraft:leather_horse_armor", "minecraft:iron_horse_armor",
		"minecraft:golden_horse_armor", "minecraft:diamond_horse_armor",
		"minecraft:wolf_armor":
		return true
	case "minecraft:chest":
		// Only acts as animal equipment if a tamed donkey/mule/llama is in front.
		for _, ent := range s.mobsInBlockBox(dim, fx, fy, fz) {
			t := canonicalResourceName(ent.TypeName)
			if (t == MobTypeDonkey || t == MobTypeMule || t == MobTypeLlama || t == MobTypeTraderLlama) && s.IsMobTamed(ent.ID) {
				return true
			}
		}
		return false
	}
	if strings.HasSuffix(norm, "_carpet") {
		for _, ent := range s.mobsInBlockBox(dim, fx, fy, fz) {
			t := canonicalResourceName(ent.TypeName)
			if (t == MobTypeLlama || t == MobTypeTraderLlama) && s.IsMobTamed(ent.ID) {
				return true
			}
		}
	}
	return false
}

func (s *Server) dispenseAnimalEquipment(dim string, fx, fy, fz int, itemID string) bool {
	norm := canonicalResourceName(itemID)
	mobs := s.mobsInBlockBox(dim, fx, fy, fz)
	for _, ent := range mobs {
		mobType := canonicalResourceName(ent.TypeName)
		switch {
		case norm == "minecraft:saddle":
			// Tamed horse/donkey/mule/camel or adult pig/strider.
			if mobType == MobTypeHorse || mobType == MobTypeDonkey || mobType == MobTypeMule || mobType == "minecraft:camel" {
				st := s.ensureTamedMobState(ent.ID, mobType)
				mgr := s.tamedManager()
				mgr.mu.Lock()
				canSaddle := st.Tamed && !st.Saddled
				if canSaddle {
					st.Saddled = true
				}
				snap := *st
				mgr.mu.Unlock()
				if canSaddle {
					s.broadcastTamedMobMetadata(ent, snap)
					return true
				}
			} else if (mobType == MobTypePig || mobType == "minecraft:strider") && !s.IsMobBaby(ent.ID) {
				st := s.ensureTamedMobState(ent.ID, mobType)
				mgr := s.tamedManager()
				mgr.mu.Lock()
				canSaddle := !st.Saddled
				if canSaddle {
					st.Saddled = true
				}
				mgr.mu.Unlock()
				if canSaddle {
					return true
				}
			}

		case norm == "minecraft:leather_horse_armor" || norm == "minecraft:iron_horse_armor" ||
			norm == "minecraft:golden_horse_armor" || norm == "minecraft:diamond_horse_armor":
			if mobType == MobTypeHorse && s.IsMobTamed(ent.ID) && s.HorseArmor(ent.ID) == "" {
				s.SetHorseArmor(ent.ID, norm)
				return true
			}

		case norm == "minecraft:wolf_armor":
			if mobType == MobTypeWolf && s.IsMobTamed(ent.ID) && s.WolfArmor(ent.ID).IsEmpty() {
				s.SetWolfArmor(ent.ID, world.NewNamedItemStack("minecraft:wolf_armor", 1))
				return true
			}

		case strings.HasSuffix(norm, "_carpet"):
			if (mobType == MobTypeLlama || mobType == MobTypeTraderLlama) && s.IsMobTamed(ent.ID) && s.LlamaCarpet(ent.ID) == "" {
				s.SetLlamaCarpet(ent.ID, norm)
				return true
			}

		case norm == "minecraft:chest":
			if (mobType == MobTypeDonkey || mobType == MobTypeMule || mobType == MobTypeLlama || mobType == MobTypeTraderLlama) &&
				s.IsMobTamed(ent.ID) && !s.IsAnimalChested(ent.ID) {
				s.SetAnimalChested(ent.ID, true)
				return true
			}
		}
	}
	return false
}

// HorseArmor returns the equipped horse armor item ID on entityID (or "" if none).
func (s *Server) HorseArmor(entityID int32) string {
	if s == nil {
		return ""
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return ds.horseArmor[entityID]
}

// SetHorseArmor sets the equipped horse armor item ID on entityID.
func (s *Server) SetHorseArmor(entityID int32, armorItemID string) {
	if s == nil {
		return
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	if armorItemID == "" {
		delete(ds.horseArmor, entityID)
	} else {
		ds.horseArmor[entityID] = canonicalResourceName(armorItemID)
	}
	ds.mu.Unlock()
}

// WolfArmor returns the equipped wolf armor stack on entityID.
func (s *Server) WolfArmor(entityID int32) world.ItemStack {
	if s == nil {
		return world.ItemStack{}
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return ds.wolfArmor[entityID]
}

// SetWolfArmor sets the equipped wolf armor stack on entityID.
func (s *Server) SetWolfArmor(entityID int32, stack world.ItemStack) {
	if s == nil {
		return
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	if stack.IsEmpty() {
		delete(ds.wolfArmor, entityID)
	} else {
		ds.wolfArmor[entityID] = stack
	}
	ds.mu.Unlock()
}

// LlamaCarpet returns the carpet item ID equipped on llama entityID.
func (s *Server) LlamaCarpet(entityID int32) string {
	if s == nil {
		return ""
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return ds.llamaCarpet[entityID]
}

// SetLlamaCarpet sets the carpet item ID equipped on llama entityID.
func (s *Server) SetLlamaCarpet(entityID int32, carpetItemID string) {
	if s == nil {
		return
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	ds.llamaCarpet[entityID] = carpetItemID
	ds.mu.Unlock()
}

// IsAnimalChested reports whether a donkey, mule, or llama has a chest equipped.
func (s *Server) IsAnimalChested(entityID int32) bool {
	if s == nil {
		return false
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return ds.chestedAnimal[entityID]
}

// SetAnimalChested sets whether a donkey, mule, or llama has a chest equipped.
func (s *Server) SetAnimalChested(entityID int32, chested bool) {
	if s == nil {
		return
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	ds.chestedAnimal[entityID] = chested
	ds.mu.Unlock()
}

// IsEntitySaddled reports whether entityID (horse, donkey, mule, camel, pig, or strider) has a saddle.
func (s *Server) IsEntitySaddled(entityID int32) bool {
	if st, ok := s.GetTamedMobState(entityID); ok {
		return st.Saddled
	}
	return false
}

// --- Minecarts & Boats ---

func isMinecartItem(itemID string) bool {
	switch canonicalResourceName(itemID) {
	case "minecraft:minecart", "minecraft:chest_minecart", "minecraft:furnace_minecart",
		"minecraft:tnt_minecart", "minecraft:hopper_minecart", "minecraft:command_block_minecart":
		return true
	default:
		return false
	}
}

func isRailBlock(state uint16) bool {
	switch world.BlockName(state) {
	case "minecraft:rail", "minecraft:powered_rail", "minecraft:detector_rail", "minecraft:activator_rail":
		return true
	default:
		return false
	}
}

func (s *Server) dispenseMinecart(dim string, cache *world.Cache, fx, fy, fz int, facing, itemID string) bool {
	if cache == nil || fy < world.MinY || fy >= world.MinY+world.WorldHeight {
		return false
	}
	frontState := cache.GetBlock(fx, fy, fz)
	var spawnY float64
	if isRailBlock(frontState) {
		spawnY = float64(fy) + 0.0625
	} else if isDispenserPlaceReplaceable(frontState) && fy-1 >= world.MinY && isRailBlock(cache.GetBlock(fx, fy-1, fz)) {
		spawnY = float64(fy-1) + 0.0625
	} else {
		return false
	}
	entID := s.spawnDispenserProjectileEntity(dim, itemID, float64(fx)+0.5, spawnY, float64(fz)+0.5, 0, 0, 0)
	return entID != 0
}

func isBoatOrRaftItem(itemID string) bool {
	norm := canonicalResourceName(itemID)
	return strings.HasSuffix(norm, "_boat") || strings.HasSuffix(norm, "_raft")
}

func (s *Server) dispenseBoat(dim string, cache *world.Cache, x, y, z, fx, fy, fz int, facing, itemID string) bool {
	if cache == nil || fy < world.MinY || fy >= world.MinY+world.WorldHeight {
		return false
	}
	frontState := cache.GetBlock(fx, fy, fz)
	frontName := world.BlockName(frontState)
	var spawnY float64
	switch {
	case frontName == "minecraft:water":
		spawnY = float64(fy) + 1.0
	case isDispenserPlaceReplaceable(frontState) && fy-1 >= world.MinY && world.BlockName(cache.GetBlock(fx, fy-1, fz)) == "minecraft:water":
		spawnY = float64(fy)
	case isDispenserPlaceReplaceable(frontState):
		spawnY = float64(fy)
	default:
		return false
	}
	dx, _, dz := DispenserFacingVector(facing)
	spawnX := float64(x) + 0.5 + float64(dx)*1.125
	spawnZ := float64(z) + 0.5 + float64(dz)*1.125
	entID := s.spawnDispenserProjectileEntity(dim, itemID, spawnX, spawnY, spawnZ, 0, 0, 0)
	return entID != 0
}

// --- Flint and Steel & Primed TNT ---

func (s *Server) dispenseFlintAndSteel(dim string, cache *world.Cache, fx, fy, fz int) bool {
	if cache == nil || fy < world.MinY || fy >= world.MinY+world.WorldHeight {
		return false
	}
	targetState := cache.GetBlock(fx, fy, fz)
	targetName := world.BlockName(targetState)
	props := world.BlockProperties(targetState)

	// 1. Light unlit campfire, soul_campfire, candle, or candle_cake.
	if (targetName == "minecraft:campfire" || targetName == "minecraft:soul_campfire" ||
		strings.HasSuffix(targetName, "_candle") || targetName == "minecraft:candle" ||
		strings.HasSuffix(targetName, "candle_cake")) && props["lit"] == "false" && props["waterlogged"] != "true" {
		if nextSt, ok := world.WithBlockProperty(targetState, "lit", "true"); ok {
			s.setDispenserWorldBlock(dim, cache, fx, fy, fz, nextSt)
			return true
		}
	}

	// 2. Prime a TNT block.
	if targetName == "minecraft:tnt" {
		s.setDispenserWorldBlock(dim, cache, fx, fy, fz, world.StateAir)
		s.SpawnPrimedTNT(dim, float64(fx)+0.5, float64(fy), float64(fz)+0.5, DefaultPrimedTNTFuseTicks)
		return true
	}

	// 3. Place fire (or light a Nether portal) if the front cell is air.
	if targetState == world.StateAir || targetName == "minecraft:air" || targetName == "minecraft:cave_air" {
		if _, lit := s.LightPortalAt(dim, fx, fy, fz); lit {
			return true
		}
		fireName := "minecraft:fire"
		if fy-1 >= world.MinY {
			belowName := world.BlockName(cache.GetBlock(fx, fy-1, fz))
			if belowName == "minecraft:soul_sand" || belowName == "minecraft:soul_soil" {
				fireName = "minecraft:soul_fire"
			}
		}
		if fireSt, ok := world.BlockStateByName(fireName, nil); ok && fireSt != world.StateAir {
			s.setDispenserWorldBlock(dim, cache, fx, fy, fz, fireSt)
			return true
		}
	}
	return false
}

// SpawnPrimedTNT spawns a primed `minecraft:tnt` entity at (x, y, z) in dim
// with the given fuse duration in ticks (default 80).
func (s *Server) SpawnPrimedTNT(dim string, x, y, z float64, fuseTicks int) int32 {
	if s == nil {
		return 0
	}
	normDim := normalizeDispenserDim(dim)
	if fuseTicks <= 0 {
		fuseTicks = DefaultPrimedTNTFuseTicks
	}
	entID := s.spawnDispenserProjectileEntity(normDim, EntityTypeTNT, x, y, z, 0, 0.2, 0)
	ds := s.dispenserState()
	ds.mu.Lock()
	ds.primedTNTs[entID] = &PrimedTNTState{
		EntityID:  entID,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		VY:        0.2,
		FuseTicks: fuseTicks,
	}
	ds.mu.Unlock()

	cx, cz := int32(math.Floor(x))>>4, int32(math.Floor(z))>>4
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.tnt.primed", SoundSourceBlocks, x, y, z, 1.0, 1.0, 0))
	s.EmitGameEvent(normDim, GameEventPrimeFuse, x, y, z, VibrationSource{EntityID: entID})
	return entID
}

// PrimedTNTByID returns a snapshot of the PrimedTNTState for entityID.
func (s *Server) PrimedTNTByID(entityID int32) (PrimedTNTState, bool) {
	if s == nil {
		return PrimedTNTState{}, false
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	defer ds.mu.Unlock()
	st, ok := ds.primedTNTs[entityID]
	if !ok || st == nil {
		return PrimedTNTState{}, false
	}
	return *st, true
}

// TickPrimedTNT advances all active primed TNT entities by 1 tick and triggers
// their explosion when FuseTicks reaches 0.
func (s *Server) TickPrimedTNT() int {
	if s == nil {
		return 0
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	var exploding []PrimedTNTState
	for id, st := range ds.primedTNTs {
		st.FuseTicks--
		if st.FuseTicks <= 0 {
			exploding = append(exploding, *st)
			delete(ds.primedTNTs, id)
		}
	}
	ds.mu.Unlock()

	for _, tnt := range exploding {
		if s.entities != nil {
			s.entities.Remove(tnt.EntityID)
		}
		cx, cz := int32(math.Floor(tnt.X))>>4, int32(math.Floor(tnt.Z))>>4
		s.BroadcastDimensionChunk(tnt.Dimension, cx, cz, protocol.PlayRemoveEntities,
			encodeRemoveEntitiesPacket(nil, []int32{tnt.EntityID}))
		s.BroadcastDimensionChunk(tnt.Dimension, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:entity.generic.explode", SoundSourceBlocks, tnt.X, tnt.Y, tnt.Z, 4.0, 1.0, 0))
		s.EmitGameEvent(tnt.Dimension, GameEventExplode, tnt.X, tnt.Y, tnt.Z, VibrationSource{EntityID: tnt.EntityID})
	}
	return len(exploding)
}

// --- Shears, Glass Bottles, and Honeycomb ---

func (s *Server) dispenseShears(dim string, cache *world.Cache, fx, fy, fz int, facing string, rng *rand.Rand) bool {
	if cache == nil || fy < world.MinY || fy >= world.MinY+world.WorldHeight {
		return false
	}
	targetState := cache.GetBlock(fx, fy, fz)
	targetName := world.BlockName(targetState)
	props := world.BlockProperties(targetState)

	// 1. Beehive / Bee Nest at honey_level == 5 -> drop 3 honeycombs and reset honey_level to 0.
	if (targetName == "minecraft:beehive" || targetName == "minecraft:bee_nest") && props["honey_level"] == "5" {
		if nextSt, ok := world.WithBlockProperty(targetState, "honey_level", "0"); ok {
			s.setDispenserWorldBlock(dim, cache, fx, fy, fz, nextSt)
			hc := world.NewNamedItemStack("minecraft:honeycomb", 3)
			s.SpawnItemEntityInDimension(dim, float64(fx)+0.5, float64(fy)+0.5, float64(fz)+0.5, hc)
			s.BroadcastDimensionChunk(dim, int32(fx>>4), int32(fz>>4), PlaySoundPacketID,
				EncodeSoundPacket("minecraft:block.beehive.shear", SoundSourceBlocks, float64(fx)+0.5, float64(fy)+0.5, float64(fz)+0.5, 1.0, 1.0, 0))
			s.EmitGameEvent(dim, GameEventShear, float64(fx)+0.5, float64(fy)+0.5, float64(fz)+0.5, VibrationSource{})
			return true
		}
	}

	// 2. Shearable entities in [fx, fx+1) x [fy, fy+1) x [fz, fz+1) (Sheep, Snow Golem, Mooshroom, Bogged).
	for _, ent := range s.mobsInBlockBox(dim, fx, fy, fz) {
		mobType := canonicalResourceName(ent.TypeName)
		switch mobType {
		case MobTypeSheep:
			if s.IsMobBaby(ent.ID) {
				continue
			}
			tr := s.entityTracker()
			tr.mu.Lock()
			mem := tr.mobStateFor(ent.ID, tr.aiTickID)
			alreadySheared := mem.sheared
			if !alreadySheared {
				mem.sheared = true
				mem.woolRegrowTimer = 0
			}
			tr.mu.Unlock()
			if !alreadySheared {
				woolCount := int32(1)
				if rng != nil {
					woolCount += int32(rng.Intn(3))
				} else {
					woolCount = 2
				}
				wool := world.NewNamedItemStack("minecraft:white_wool", woolCount)
				s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.5, ent.Z, wool)
				cp := ent.ChunkPos()
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeSheepWoolDataPacket(ent.ID, 0, true))
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket("minecraft:entity.sheep.shear", SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
				s.EmitGameEvent(dim, GameEventShear, ent.X, ent.Y, ent.Z, VibrationSource{EntityID: ent.ID})
				return true
			}
		case MobTypeSnowGolem:
			tr := s.entityTracker()
			tr.mu.Lock()
			mem := tr.mobStateFor(ent.ID, tr.aiTickID)
			alreadySheared := mem.sheared
			if !alreadySheared {
				mem.sheared = true
			}
			tr.mu.Unlock()
			if !alreadySheared {
				s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.5, ent.Z, world.NewNamedItemStack("minecraft:carved_pumpkin", 1))
				s.EmitGameEvent(dim, GameEventShear, ent.X, ent.Y, ent.Z, VibrationSource{EntityID: ent.ID})
				return true
			}
		}
	}

	// 3. Carve a `minecraft:pumpkin` block into `minecraft:carved_pumpkin` and drop 4 pumpkin_seeds.
	if targetName == "minecraft:pumpkin" {
		horizFacing := facing
		if horizFacing == "up" || horizFacing == "down" {
			horizFacing = "north"
		}
		if carvedSt, ok := world.BlockStateByName("minecraft:carved_pumpkin", map[string]string{"facing": horizFacing}); ok {
			s.setDispenserWorldBlock(dim, cache, fx, fy, fz, carvedSt)
			s.SpawnItemEntityInDimension(dim, float64(fx)+0.5, float64(fy)+0.5, float64(fz)+0.5, world.NewNamedItemStack("minecraft:pumpkin_seeds", 4))
			return true
		}
	}

	return false
}

// IsSheepSheared reports whether the sheep entity with entityID is currently sheared.
func (s *Server) IsSheepSheared(entityID int32) bool {
	if s == nil {
		return false
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	mem := tr.mobStates[entityID]
	return mem != nil && mem.sheared
}

func (s *Server) dispenseGlassBottle(dim string, cache *world.Cache, fx, fy, fz int) (filled world.ItemStack, potionType string, ok bool) {
	if cache == nil || fy < world.MinY || fy >= world.MinY+world.WorldHeight {
		return world.ItemStack{}, "", false
	}
	targetState := cache.GetBlock(fx, fy, fz)
	targetName := world.BlockName(targetState)
	props := world.BlockProperties(targetState)

	// 1. Beehive / Bee Nest at honey_level == 5 -> honey_bottle + reset honey_level to 0.
	if (targetName == "minecraft:beehive" || targetName == "minecraft:bee_nest") && props["honey_level"] == "5" {
		if nextSt, valid := world.WithBlockProperty(targetState, "honey_level", "0"); valid {
			s.setDispenserWorldBlock(dim, cache, fx, fy, fz, nextSt)
			return world.NewNamedItemStack("minecraft:honey_bottle", 1), "", true
		}
	}

	// 2. Water block or waterlogged block -> water potion.
	if targetName == "minecraft:water" || props["waterlogged"] == "true" {
		return world.NewNamedItemStack("minecraft:potion", 1), PotionWater, true
	}

	// 3. Water cauldron -> decrement cauldron level and produce water potion.
	if targetName == "minecraft:water_cauldron" {
		_, lvl, _ := ParseCauldronState(targetState)
		nextSt := CauldronWaterBlockState(lvl - 1)
		s.setDispenserWorldBlock(dim, cache, fx, fy, fz, nextSt)
		return world.NewNamedItemStack("minecraft:potion", 1), PotionWater, true
	}

	return world.ItemStack{}, "", false
}

func (s *Server) dispenseHoneycombWax(dim string, cache *world.Cache, fx, fy, fz int) bool {
	if cache == nil || fy < world.MinY || fy >= world.MinY+world.WorldHeight {
		return false
	}
	targetState := cache.GetBlock(fx, fy, fz)
	if waxedState, ok := WaxedCopperBlockState(targetState); ok {
		s.setDispenserWorldBlock(dim, cache, fx, fy, fz, waxedState)
		s.BroadcastDimensionChunk(dim, int32(fx>>4), int32(fz>>4), PlayLevelEventCB,
			EncodeLevelEventPacket(LevelEventParticlesWaxOn, fx, fy, fz, 0, false))
		return true
	}
	if IsSignBlock(targetState) {
		sign, exists := s.GetSignAt(dim, fx, fy, fz)
		if !exists || sign == nil {
			signID, _, _ := SignBlockEntityTypeForState(targetState)
			sign = NewSignBlockEntity(signID, dim, fx, fy, fz)
		}
		if !sign.IsWaxed {
			sign.IsWaxed = true
			s.SetSignAt(sign)
			s.BroadcastDimensionChunk(dim, int32(fx>>4), int32(fz>>4), PlayLevelEventCB,
				EncodeLevelEventPacket(LevelEventParticlesWaxOn, fx, fy, fz, 0, false))
			return true
		}
	}
	return false
}

// WaxedCopperBlockState returns the waxed counterpart block state for an
// unwaxed copper block state (e.g. `minecraft:copper_block` -> `minecraft:waxed_copper_block`,
// `minecraft:cut_copper_stairs` -> `minecraft:waxed_cut_copper_stairs`), preserving all properties.
func WaxedCopperBlockState(state uint16) (uint16, bool) {
	if state == world.StateAir {
		return 0, false
	}
	name := world.BlockName(state)
	if !strings.HasPrefix(name, "minecraft:") {
		return 0, false
	}
	short := strings.TrimPrefix(name, "minecraft:")
	if strings.HasPrefix(short, "waxed_") || !strings.Contains(short, "copper") {
		return 0, false
	}
	waxedName := "minecraft:waxed_" + short
	props := world.BlockProperties(state)
	waxedState, ok := world.BlockStateByName(waxedName, props)
	if !ok || waxedState == world.StateAir {
		return 0, false
	}
	return waxedState, true
}

// --- Entity Bounding-Box Lookup Helpers ---

func (s *Server) playersInBlockBox(dim string, bx, by, bz int) []*PlayerSession {
	if s == nil {
		return nil
	}
	normDim := normalizeDispenserDim(dim)
	minX, maxX := float64(bx), float64(bx+1)
	minY, maxY := float64(by), float64(by+1)
	minZ, maxZ := float64(bz), float64(bz+1)

	s.playersMu.RLock()
	defer s.playersMu.RUnlock()
	var out []*PlayerSession
	for _, sess := range s.players {
		if sess == nil || sess.IsDead() {
			continue
		}
		snap := sess.Snapshot()
		if normalizeDispenserDim(snap.Dimension) != normDim {
			continue
		}
		// Player AABB is 0.6 wide x 1.8 tall centered at (X, Z) with feet at Y.
		if snap.X+0.3 >= minX && snap.X-0.3 <= maxX &&
			snap.Y+1.8 >= minY && snap.Y <= maxY &&
			snap.Z+0.3 >= minZ && snap.Z-0.3 <= maxZ {
			out = append(out, sess)
		}
	}
	return out
}

func (s *Server) armorStandsInBlockBox(dim string, bx, by, bz int) []*world.Entity {
	if s == nil || s.entities == nil {
		return nil
	}
	normDim := normalizeDispenserDim(dim)
	minX, maxX := float64(bx), float64(bx+1)
	minY, maxY := float64(by), float64(by+1)
	minZ, maxZ := float64(bz), float64(bz+1)

	all := s.entities.AppendAll(nil)
	var out []*world.Entity
	for i := range all {
		e := &all[i]
		if canonicalResourceName(e.TypeName) != EntityTypeArmorStand {
			continue
		}
		if normalizeDispenserDim(e.EffectiveDimension()) != normDim {
			continue
		}
		if e.X+0.25 >= minX && e.X-0.25 <= maxX &&
			e.Y+1.975 >= minY && e.Y <= maxY &&
			e.Z+0.25 >= minZ && e.Z-0.25 <= maxZ {
			cp := *e
			out = append(out, &cp)
		}
	}
	return out
}

func (s *Server) mobsInBlockBox(dim string, bx, by, bz int) []*world.Entity {
	if s == nil || s.entities == nil {
		return nil
	}
	normDim := normalizeDispenserDim(dim)
	minX, maxX := float64(bx), float64(bx+1)
	minY, maxY := float64(by), float64(by+1)
	minZ, maxZ := float64(bz), float64(bz+1)

	all := s.entities.AppendAll(nil)
	var out []*world.Entity
	for i := range all {
		e := &all[i]
		if e.IsItem() || canonicalResourceName(e.TypeName) == EntityTypeArmorStand ||
			canonicalResourceName(e.TypeName) == EntityTypeItemFrame ||
			canonicalResourceName(e.TypeName) == EntityTypeGlowItemFrame ||
			canonicalResourceName(e.TypeName) == EntityTypePainting {
			continue
		}
		if normalizeDispenserDim(e.EffectiveDimension()) != normDim {
			continue
		}
		if e.X+0.4 >= minX && e.X-0.4 <= maxX &&
			e.Y+1.8 >= minY && e.Y <= maxY &&
			e.Z+0.4 >= minZ && e.Z-0.4 <= maxZ {
			cp := *e
			out = append(out, &cp)
		}
	}
	return out
}

func maxFloat32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

// --- Hopper & UI Hooks for Dispenser / Dropper (for Agent B's containers.go) ---

// HopperInsertIntoDispenser attempts to insert 1 item from a hopper into the
// dispenser or dropper at (dim, x, y, z).
func (s *Server) HopperInsertIntoDispenser(dim string, x, y, z int, stack world.ItemStack, meta ItemMeta, potionType string) bool {
	if s == nil || stack.IsEmpty() {
		return false
	}
	single := world.NewItemStack(stack.ResolvedItemID(), 1)
	if single.IsEmpty() {
		single = world.NewNamedItemStack(stack.ResolvedID(), 1)
	}
	rem := s.TryInsertIntoDispenser(dim, x, y, z, single, meta, potionType)
	return rem.IsEmpty()
}

// HopperExtractFromDispenser extracts 1 item from the first non-empty slot
// (0..8) of the dispenser or dropper at (dim, x, y, z).
func (s *Server) HopperExtractFromDispenser(dim string, x, y, z int) (stack world.ItemStack, meta ItemMeta, potionType string, ok bool) {
	if s == nil {
		return world.ItemStack{}, ItemMeta{}, "", false
	}
	normDim := normalizeDispenserDim(dim)
	ds := s.dispenserState()
	ds.mu.Lock()
	be := ds.blocks[dispenserBlockKey{dim: normDim, x: x, y: y, z: z}]
	if be == nil {
		ds.mu.Unlock()
		return world.ItemStack{}, ItemMeta{}, "", false
	}
	foundSlot := -1
	for i := 0; i < DispenserSlotCount; i++ {
		if !be.Slots[i].IsEmpty() && be.Slots[i].Count > 0 {
			foundSlot = i
			break
		}
	}
	if foundSlot < 0 {
		ds.mu.Unlock()
		return world.ItemStack{}, ItemMeta{}, "", false
	}
	cur := be.Slots[foundSlot]
	stack = world.NewItemStack(cur.ResolvedItemID(), 1)
	if stack.IsEmpty() {
		stack = world.NewNamedItemStack(cur.ResolvedID(), 1)
	}
	meta = be.SlotMeta[foundSlot].Clone()
	potionType = be.SlotPotions[foundSlot]
	decrementDispenserSlotLocked(be, foundSlot)
	ds.mu.Unlock()

	s.updateDispenserComparatorsAndBroadcast(normDim, x, y, z)
	_ = s.SyncDispenserViewers(nil, x, y, z)
	return stack, meta, potionType, true
}

// OpenDispenserOrDropper opens the 3x3 Dispenser or Dropper screen
// (`MenuTypeGeneric3x3`) at (x, y, z) for session.
func (s *Server) OpenDispenserOrDropper(session *PlayerSession, x, y, z int) (int32, error) {
	if s == nil || session == nil {
		return 0, errors.New("server: nil session")
	}
	_ = s.CloseContainer(session, 0, false)

	dim := normalizeDispenserDim(session.Dimension())
	title := "Dispenser"
	if cache := s.DimensionChunks(dim); cache != nil {
		if world.BlockName(cache.GetBlock(x, y, z)) == "minecraft:dropper" {
			title = "Dropper"
		}
	}

	tr := getContainerTracker(session)
	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	tr.open = &OpenContainerState{
		ContainerID: cid,
		MenuType:    MenuTypeGeneric3x3,
		BlockX:      x,
		BlockY:      y,
		BlockZ:      z,
	}
	carried := tr.carried
	tr.mu.Unlock()

	s.EmitGameEvent(dim, GameEventContainerOpen, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, VibrationSource{Player: session})

	if session.send != nil {
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreen(cid, MenuTypeGeneric3x3, title)); err != nil {
			return cid, err
		}
		slots := s.BuildDispenserMenuSlots(session, x, y, z)
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried)); err != nil {
			return cid, err
		}
	}
	return cid, nil
}

// BuildDispenserMenuSlots builds the 45-slot slice (9 dispenser/dropper slots +
// 27 main inventory + 9 hotbar) for the generic_3x3 screen at (x, y, z).
func (s *Server) BuildDispenserMenuSlots(session *PlayerSession, x, y, z int) []world.ItemStack {
	dim := world.DimensionOverworld
	if session != nil && session.Dimension() != "" {
		dim = session.Dimension()
	}
	dispSlots := s.DispenserSlotsInDimension(dim, x, y, z)
	out := make([]world.ItemStack, DispenserMenuSlotCount)
	copy(out[:DispenserSlotCount], dispSlots[:])
	if session != nil {
		for i := 0; i < 27; i++ {
			out[DispenserSlotCount+i] = session.InventorySlot(9 + i)
		}
		for i := 0; i < 9; i++ {
			out[DispenserSlotCount+27+i] = session.InventorySlot(36 + i)
		}
	}
	return out
}

// ApplyDispenserOrDropperClickLocked processes a container click on an open
// `MenuTypeGeneric3x3` dispenser/dropper screen while `tr.mu` is held.
func (s *Server) ApplyDispenserOrDropperClickLocked(
	session *PlayerSession,
	tr *playerContainerTracker,
	bx, by, bz, slot int,
	button int8,
	mode int32,
) {
	if s == nil || session == nil || tr == nil || slot < 0 || slot >= DispenserMenuSlotCount {
		return
	}
	dim := normalizeDispenserDim(session.Dimension())
	slots := s.BuildDispenserMenuSlots(session, bx, by, bz)

	switch mode {
	case ContainerInputPickup:
		slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)
	case ContainerInputQuickMove:
		if slot < DispenserSlotCount {
			slots[slot] = mergeIntoSlotSlice(slots, slots[slot], DispenserSlotCount, DispenserMenuSlotCount)
		} else {
			slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 0, DispenserSlotCount)
		}
	}

	for i := 0; i < DispenserSlotCount; i++ {
		s.SetDispenserSlotInDimension(dim, bx, by, bz, i, slots[i])
	}
	for i := 0; i < 27; i++ {
		session.SetInventorySlot(9+i, slots[DispenserSlotCount+i])
	}
	for i := 0; i < 9; i++ {
		session.SetInventorySlot(36+i, slots[DispenserSlotCount+27+i])
	}
}

// SyncDispenserViewers synchronizes the open generic_3x3 container screen for
// any players viewing the dispenser/dropper at (bx, by, bz).
func (s *Server) SyncDispenserViewers(actor *PlayerSession, bx, by, bz int) error {
	if s == nil {
		return nil
	}
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()

	var firstErr error
	for _, sess := range sessions {
		open := sess.OpenContainer()
		if open != nil && open.MenuType == MenuTypeGeneric3x3 &&
			open.BlockX == bx && open.BlockY == by && open.BlockZ == bz {
			if err := s.syncOpenContainer(sess); err != nil && firstErr == nil {
				firstErr = err
			}
		} else if sess == actor {
			if err := s.syncOpenContainer(sess); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// --- Persistence: SaveDispensers & LoadDispensers ---

func (s *Server) dispensersFilePath() string {
	if s == nil || s.cfg.WorldDir == "" {
		return ""
	}
	return filepath.Join(s.cfg.WorldDir, "dispensers.nbt")
}

// SaveDispensers persists all non-empty dispensers and droppers to
// `<WorldDir>/dispensers.nbt`.
func (s *Server) SaveDispensers() error {
	if s == nil {
		return nil
	}
	path := s.dispensersFilePath()
	if path == "" {
		return nil
	}
	root := s.SaveDispensersToNBT()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, nbt.MarshalNamed("", root), 0o644)
}

// LoadDispensers loads all persisted dispensers and droppers from
// `<WorldDir>/dispensers.nbt` (no-op if the file does not exist).
func (s *Server) LoadDispensers() error {
	if s == nil {
		return nil
	}
	path := s.dispensersFilePath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	_, parsedTag, err := nbt.UnmarshalNamed(data)
	if err != nil {
		return err
	}
	root, ok := parsedTag.(*nbt.Compound)
	if !ok || root == nil {
		return nil
	}
	s.LoadDispensersFromNBT(root)
	return nil
}

// SaveDispensersToNBT serializes all dispensers and droppers into an NBT compound.
func (s *Server) SaveDispensersToNBT() *nbt.Compound {
	root := nbt.NewCompound()
	if s == nil {
		return root
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	keys := make([]dispenserBlockKey, 0, len(ds.blocks))
	for k := range ds.blocks {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dim != keys[j].dim {
			return keys[i].dim < keys[j].dim
		}
		if keys[i].x != keys[j].x {
			return keys[i].x < keys[j].x
		}
		if keys[i].y != keys[j].y {
			return keys[i].y < keys[j].y
		}
		return keys[i].z < keys[j].z
	})

	var entries []nbt.Tag
	for _, k := range keys {
		be := ds.blocks[k]
		if be == nil {
			continue
		}
		c := nbt.NewCompound()
		id := "minecraft:dispenser"
		if be.IsDropper {
			id = "minecraft:dropper"
		}
		c.Set("id", nbt.String(id))
		c.Set("Dimension", nbt.String(be.Dimension))
		c.Set("x", nbt.Int(int32(be.X)))
		c.Set("y", nbt.Int(int32(be.Y)))
		c.Set("z", nbt.Int(int32(be.Z)))
		if be.CustomName != "" {
			c.Set("CustomName", nbt.String(be.CustomName))
		}
		var itemTags []nbt.Tag
		for slotIdx := 0; slotIdx < DispenserSlotCount; slotIdx++ {
			st := be.Slots[slotIdx]
			if st.IsEmpty() || st.Count <= 0 {
				continue
			}
			it := nbt.NewCompound()
			it.Set("Slot", nbt.Byte(int8(slotIdx)))
			it.Set("id", nbt.String(st.ResolvedID()))
			it.Set("count", nbt.Int(st.Count))
			if be.SlotPotions[slotIdx] != "" {
				it.Set("Potion", nbt.String(be.SlotPotions[slotIdx]))
			}
			if be.SlotMeta[slotIdx].Damage > 0 {
				it.Set("Damage", nbt.Int(be.SlotMeta[slotIdx].Damage))
			}
			if be.SlotMeta[slotIdx].CustomName != "" {
				it.Set("CustomName", nbt.String(be.SlotMeta[slotIdx].CustomName))
			}
			itemTags = append(itemTags, it)
		}
		c.Set("Items", nbt.List{ElemID: nbt.TagCompound, Elems: itemTags})
		entries = append(entries, c)
	}
	ds.mu.Unlock()

	root.Set("Dispensers", nbt.List{ElemID: nbt.TagCompound, Elems: entries})
	return root
}

func nbtGetString(c *nbt.Compound, key string) (string, bool) {
	if c == nil {
		return "", false
	}
	if t, ok := c.Get(key); ok {
		if s, ok := t.(nbt.String); ok {
			return string(s), true
		}
	}
	return "", false
}

func nbtGetInt(c *nbt.Compound, key string) (int32, bool) {
	if c == nil {
		return 0, false
	}
	if t, ok := c.Get(key); ok {
		switch v := t.(type) {
		case nbt.Int:
			return int32(v), true
		case nbt.Short:
			return int32(v), true
		case nbt.Byte:
			return int32(v), true
		}
	}
	return 0, false
}

func nbtGetByte(c *nbt.Compound, key string) (int8, bool) {
	if c == nil {
		return 0, false
	}
	if t, ok := c.Get(key); ok {
		if b, ok := t.(nbt.Byte); ok {
			return int8(b), true
		}
	}
	return 0, false
}

func nbtGetListElems(c *nbt.Compound, key string) ([]nbt.Tag, bool) {
	if c == nil {
		return nil, false
	}
	t, ok := c.Get(key)
	if !ok {
		return nil, false
	}
	switch v := t.(type) {
	case nbt.List:
		return v.Elems, true
	case *nbt.List:
		if v != nil {
			return v.Elems, true
		}
	}
	return nil, false
}

// LoadDispensersFromNBT restores dispensers and droppers from an NBT compound.
func (s *Server) LoadDispensersFromNBT(root *nbt.Compound) int {
	if s == nil || root == nil {
		return 0
	}
	listElems, ok := nbtGetListElems(root, "Dispensers")
	if !ok {
		return 0
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	loaded := 0
	var dimsToBind []string
	for _, elem := range listElems {
		c, ok := elem.(*nbt.Compound)
		if !ok || c == nil {
			continue
		}
		dim, _ := nbtGetString(c, "Dimension")
		normDim := normalizeDispenserDim(dim)
		x, _ := nbtGetInt(c, "x")
		y, _ := nbtGetInt(c, "y")
		z, _ := nbtGetInt(c, "z")
		idStr, _ := nbtGetString(c, "id")
		customName, _ := nbtGetString(c, "CustomName")

		be := &DispenserBlockEntity{
			Dimension:  normDim,
			X:          int(x),
			Y:          int(y),
			Z:          int(z),
			IsDropper:  idStr == "minecraft:dropper",
			CustomName: customName,
		}
		if itemElems, ok := nbtGetListElems(c, "Items"); ok {
			for _, itElem := range itemElems {
				itComp, ok := itElem.(*nbt.Compound)
				if !ok || itComp == nil {
					continue
				}
				slotByte, _ := nbtGetByte(itComp, "Slot")
				slotIdx := int(slotByte)
				if slotIdx < 0 || slotIdx >= DispenserSlotCount {
					continue
				}
				itemID, _ := nbtGetString(itComp, "id")
				count, _ := nbtGetInt(itComp, "count")
				if count <= 0 {
					if bCount, ok := nbtGetByte(itComp, "Count"); ok {
						count = int32(bCount)
					}
				}
				if itemID == "" || count <= 0 {
					continue
				}
				be.Slots[slotIdx] = world.NewNamedItemStack(itemID, count)
				if pot, ok := nbtGetString(itComp, "Potion"); ok {
					be.SlotPotions[slotIdx] = pot
				}
				var meta ItemMeta
				if dmg, ok := nbtGetInt(itComp, "Damage"); ok {
					meta.Damage = dmg
				}
				if cname, ok := nbtGetString(itComp, "CustomName"); ok {
					meta.CustomName = cname
				}
				be.SlotMeta[slotIdx] = meta
			}
		}
		ds.blocks[dispenserBlockKey{dim: normDim, x: int(x), y: int(y), z: int(z)}] = be
		dimsToBind = append(dimsToBind, normDim)
		loaded++
	}
	ds.mu.Unlock()

	for _, d := range dimsToBind {
		s.BindNoteBlockAndJukeboxRedstone(d)
	}
	return loaded
}

func init() {
	// Ensure strconv is referenced if needed.
	_ = strconv.Itoa
}

