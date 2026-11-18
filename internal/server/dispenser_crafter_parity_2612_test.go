package server

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestDispenserAndDropperRedstoneTriggerQuasiConnectivityAndUniformRandom2612(t *testing.T) {
	srv := newDispenserTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.BindNoteBlockAndJukeboxRedstone(dim)

	// 1. Uniformly random non-empty slot selection across populated slots 0, 4, 8.
	var slots [DispenserSlotCount]world.ItemStack
	slots[0] = world.NewNamedItemStack("minecraft:arrow", 10)
	slots[4] = world.NewNamedItemStack("minecraft:snowball", 1)
	slots[8] = world.NewNamedItemStack("minecraft:cobblestone", 64)

	rng := rand.New(rand.NewSource(2612))
	counts := make(map[int]int)
	const trials = 3000
	for i := 0; i < trials; i++ {
		idx := ChooseRandomDispenserSlot(slots, rng)
		counts[idx]++
	}
	for _, wantIdx := range []int{0, 4, 8} {
		if counts[wantIdx] < 850 || counts[wantIdx] > 1150 {
			t.Fatalf("slot %d chosen %d/%d times (expected ~1000 uniform regardless of stack count)", wantIdx, counts[wantIdx], trials)
		}
	}
	if len(counts) != 3 {
		t.Fatalf("unexpected slots chosen: %+v", counts)
	}

	// 2. Empty dispenser and empty dropper emit LevelEvent 1001 without smoke (2000).
	viewer, collect := addDispenserTestPlayer(t, srv, "EmptyDispViewer")
	srv.SetPlayerTransform(viewer, 10.5, 70, 10.5, 0, 0, true)
	placeDispenserOrDropperForTest(t, srv, dim, 10, 70, 10, "minecraft:dispenser", "north")
	placeDispenserOrDropperForTest(t, srv, dim, 12, 70, 10, "minecraft:dropper", "north")
	_ = collect()

	if srv.DispenseFromDispenser(dim, 10, 70, 10) {
		t.Fatal("expected empty dispenser to return false")
	}
	saw1001, saw2000 := false, false
	for _, pkt := range collect() {
		if pkt.ID == PlayLevelEventCB {
			r := protocol.NewReader(pkt.Payload)
			if ev, err := r.Int32(); err == nil {
				if ev == LevelEventSoundFailDispense {
					saw1001 = true
				}
				if ev == LevelEventParticlesShootSmoke {
					saw2000 = true
				}
			}
		}
	}
	if !saw1001 || saw2000 {
		t.Fatalf("empty dispenser events: 1001=%v 2000=%v, want 1001=true 2000=false", saw1001, saw2000)
	}

	if srv.DispenseFromDropper(dim, 12, 70, 10) {
		t.Fatal("expected empty dropper to return false")
	}
	saw1001, saw2000 = false, false
	for _, pkt := range collect() {
		if pkt.ID == PlayLevelEventCB {
			r := protocol.NewReader(pkt.Payload)
			if ev, err := r.Int32(); err == nil {
				if ev == LevelEventSoundFailDispense {
					saw1001 = true
				}
				if ev == LevelEventParticlesShootSmoke {
					saw2000 = true
				}
			}
		}
	}
	if !saw1001 || saw2000 {
		t.Fatalf("empty dropper events: 1001=%v 2000=%v, want 1001=true 2000=false", saw1001, saw2000)
	}

	// 3. Dropper 4-tick delay + quasi-connectivity + no re-fire while triggered=true.
	srv.SetDispenserSlot(12, 70, 10, 2, world.NewNamedItemStack("minecraft:diamond", 2))
	rbSt, _ := world.BlockStateByName("minecraft:redstone_block", nil)
	// Power (12, 71, 10) via redstone_block at (13, 71, 10) -> quasi-connectivity!
	cache.ApplyBlockWithUpdates(13, 71, 10, rbSt)
	if world.BlockProperties(cache.GetBlock(12, 70, 10))["triggered"] != "true" {
		t.Fatal("expected dropper triggered=true via quasi-connectivity")
	}
	for tick := 1; tick <= 3; tick++ {
		cache.TickBlockUpdates()
		if got := srv.DispenserSlots(12, 70, 10)[2].Count; got != 2 {
			t.Fatalf("dropper fired early at tick %d (count=%d)", tick, got)
		}
	}
	cache.TickBlockUpdates()
	if got := srv.DispenserSlots(12, 70, 10)[2].Count; got != 1 {
		t.Fatalf("expected dropper to fire on tick 4, count=%d", got)
	}
	// Adding direct power while already triggered=true must NOT re-trigger.
	cache.ApplyBlockWithUpdates(13, 70, 10, rbSt)
	for tick := 0; tick < 6; tick++ {
		cache.TickBlockUpdates()
	}
	if got := srv.DispenserSlots(12, 70, 10)[2].Count; got != 1 {
		t.Fatalf("dropper re-fired while triggered=true remained latched (count=%d)", got)
	}
}

func TestDispenserBehaviorsExactVanilla2612Matrix(t *testing.T) {
	srv := newDispenserTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.BindNoteBlockAndJukeboxRedstone(dim)

	countDroppedItemsNear := func(x, y, z int, itemID string) int32 {
		var total int32
		for _, ent := range srv.entities.AppendInChunkRange(nil, int32(x>>4), int32(z>>4), 1) {
			if ent.IsItem() && canonicalResourceName(ent.Item.ResolvedID()) == canonicalResourceName(itemID) {
				total += ent.Item.Count
			}
		}
		return total
	}
	clearDroppedItemsNear := func(x, z int) {
		for _, ent := range srv.entities.AppendInChunkRange(nil, int32(x>>4), int32(z>>4), 1) {
			if ent.IsItem() {
				srv.entities.Remove(ent.ID)
			}
		}
	}

	// --- 1. Buckets: waterlog slab + multi-bucket stack overflow + cauldron NOT scooped ---
	dx, dy, dz := 20, 70, 20
	fx, fy, fz := 20, 70, 19
	placeDispenserOrDropperForTest(t, srv, dim, dx, dy, dz, "minecraft:dispenser", "north")
	slabDry, _ := world.BlockStateByName("minecraft:oak_slab", map[string]string{"type": "bottom", "waterlogged": "false"})
	cache.SetBlock(fx, fy, fz, slabDry)

	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:water_bucket", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected water_bucket to waterlog oak_slab")
	}
	if world.BlockProperties(cache.GetBlock(fx, fy, fz))["waterlogged"] != "true" {
		t.Fatal("expected oak_slab waterlogged=true")
	}
	if srv.DispenserSlots(dx, dy, dz)[0].ResolvedID() != "minecraft:bucket" {
		t.Fatal("expected water_bucket replaced by bucket")
	}

	// Stack of 3 empty buckets with all other 8 slots full -> scoops waterlogged slab, shrinks slot[0] to 2, and ejects water_bucket out front!
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:bucket", 3))
	for i := 1; i < DispenserSlotCount; i++ {
		srv.SetDispenserSlot(dx, dy, dz, i, world.NewNamedItemStack("minecraft:dirt", 64))
	}
	// Force slot[0] to be tested by clearing slots 1..8 first for slot[1] insertion, then full test:
	for i := 1; i < DispenserSlotCount; i++ {
		srv.SetDispenserSlot(dx, dy, dz, i, world.ItemStack{})
	}
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected stacked bucket scoop to succeed")
	}
	if got := srv.DispenserSlots(dx, dy, dz)[0]; got.ResolvedID() != "minecraft:bucket" || got.Count != 2 {
		t.Fatalf("expected 2x bucket remaining in slot[0], got %+v", got)
	}
	if got := srv.DispenserSlots(dx, dy, dz)[1]; got.ResolvedID() != "minecraft:water_bucket" || got.Count != 1 {
		t.Fatalf("expected 1x water_bucket placed in slot[1], got %+v", got)
	}

	// Now fill slots 1..8 with unstackable shovels so only slot[0] has buckets -> actually fill with 1-count wooden_shovel? No, ChooseRandomDispenserSlot picks uniformly!
	// Put 2x bucket in ALL 9 slots so whichever slot is chosen, all 9 slots are occupied by >=2 buckets and cannot accept water_bucket!
	cache.SetBlock(fx, fy, fz, world.StateWater)
	clearDroppedItemsNear(fx, fz)
	for i := 0; i < DispenserSlotCount; i++ {
		srv.SetDispenserSlot(dx, dy, dz, i, world.NewNamedItemStack("minecraft:bucket", 2))
	}
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected full-dispenser bucket scoop to succeed")
	}
	if countDroppedItemsNear(fx, fy, fz, "minecraft:water_bucket") != 1 {
		t.Fatalf("expected overflow water_bucket to be ejected into world when dispenser slots are full")
	}
	for i := 0; i < DispenserSlotCount; i++ {
		srv.SetDispenserSlot(dx, dy, dz, i, world.ItemStack{})
	}
	clearDroppedItemsNear(fx, fz)

	// Empty bucket facing water_cauldron -> Vanilla BucketPickup is NOT implemented by cauldrons, so dispenser ejects 1 empty bucket!
	waterCauldron3, _ := world.BlockStateByName("minecraft:water_cauldron", map[string]string{"level": "3"})
	cache.SetBlock(fx, fy, fz, waterCauldron3)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:bucket", 1))
	srv.DispenseFromDispenser(dim, dx, dy, dz)
	if world.BlockName(cache.GetBlock(fx, fy, fz)) != "minecraft:water_cauldron" {
		t.Fatalf("dispenser bucket must NOT scoop water_cauldron in vanilla 26.1.2")
	}
	if countDroppedItemsNear(fx, fy, fz, "minecraft:bucket") != 1 {
		t.Fatalf("expected empty bucket to be ejected when facing water_cauldron")
	}
	clearDroppedItemsNear(fx, fz)

	// --- 2. flint_and_steel vs fire_charge ---
	// flint_and_steel into mid-air (air below and around) -> fails (false), 0 durability wear!
	cache.SetBlock(fx, fy-1, fz, world.StateAir)
	cache.SetBlock(fx, fy, fz, world.StateAir)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:flint_and_steel", 1))
	if srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected flint_and_steel into unsupported mid-air to fail in vanilla 26.1.2")
	}
	if dmg := srv.DispenserSlotMeta(dim, dx, dy, dz, 0).Damage; dmg != 0 {
		t.Fatalf("expected 0 wear on failed flint_and_steel, got %d", dmg)
	}
	// flint_and_steel at 63/64 damage lighting soul_sand -> soul_fire and breaks tool!
	soulSand, _ := world.BlockStateByName("minecraft:soul_sand", nil)
	cache.SetBlock(fx, fy-1, fz, soulSand)
	srv.SetDispenserSlotWithMeta(dim, dx, dy, dz, 0, world.NewNamedItemStack("minecraft:flint_and_steel", 1), ItemMeta{Damage: 63})
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected flint_and_steel above soul_sand to succeed")
	}
	if got := world.BlockName(cache.GetBlock(fx, fy, fz)); got != "minecraft:soul_fire" {
		t.Fatalf("expected minecraft:soul_fire above soul_sand, got %q", got)
	}
	if !srv.DispenserSlots(dx, dy, dz)[0].IsEmpty() {
		t.Fatal("expected flint_and_steel at 63/64 damage to break on use")
	}

	// fire_charge facing air above stone -> shoots SmallFireball projectile, does NOT place fire block!
	stoneSt, _ := world.BlockStateByName("minecraft:stone", nil)
	cache.SetBlock(fx, fy-1, fz, stoneSt)
	cache.SetBlock(fx, fy, fz, world.StateAir)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:fire_charge", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected fire_charge dispense to succeed")
	}
	if got := world.BlockName(cache.GetBlock(fx, fy, fz)); got != "minecraft:air" {
		t.Fatalf("fire_charge must shoot SmallFireball and NOT place fire on front block, got %q", got)
	}

	// --- 3. Shears facing pumpkin block -> fails in vanilla 26.1.2 (does NOT carve pumpkin) ---
	pumpkinSt, _ := world.BlockStateByName("minecraft:pumpkin", nil)
	cache.SetBlock(fx, fy, fz, pumpkinSt)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:shears", 1))
	if srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("shears in dispenser must NOT carve pumpkin block in vanilla 26.1.2")
	}
	if world.BlockName(cache.GetBlock(fx, fy, fz)) != "minecraft:pumpkin" {
		t.Fatal("pumpkin block should remain uncarved")
	}

	// --- 4. ShulkerBoxDispenseBehavior: orientation + full ShulkerBoxItemData preservation ---
	// With stone at (fx, fy-1, fz) (!isEmptyBlock(below)), shulker box places with facing="up"!
	cache.SetBlock(fx, fy-1, fz, stoneSt)
	cache.SetBlock(fx, fy, fz, world.StateAir)
	shData := &ShulkerBoxItemData{
		CustomName: "Supply Crate",
		Lock:       "KeyCard",
	}
	shData.Slots[3] = world.NewNamedItemStack("minecraft:diamond_pickaxe", 1)
	shData.SlotMeta[3] = ItemMeta{Damage: 42, CustomName: "Miner", Enchantments: map[string]int{"minecraft:efficiency": 5}}
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:cyan_shulker_box", 1))
	// Attach full shulker data via storage/dispenser slot helper or NBT round-trip:
	compShulker := EncodeShulkerBoxItemNBT("minecraft:cyan_shulker_box", 1, shData)
	compShulker.Set("Slot", nbt.Byte(0))
	dispNBT := nbt.NewCompound()
	dispNBT.Set("id", nbt.String("minecraft:dispenser"))
	dispNBT.Set("x", nbt.Int(int32(dx)))
	dispNBT.Set("y", nbt.Int(int32(dy)))
	dispNBT.Set("z", nbt.Int(int32(dz)))
	dispNBT.Set("Items", nbt.List{ElemID: nbt.TagCompound, Elems: []nbt.Tag{compShulker}})
	srv.LoadChunkDispenserBlockEntitiesNBT(dim, []*nbt.Compound{dispNBT})

	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected cyan_shulker_box dispense to succeed")
	}
	placedShulker := cache.GetBlock(fx, fy, fz)
	if world.BlockName(placedShulker) != "minecraft:cyan_shulker_box" {
		t.Fatalf("placed block = %q, want minecraft:cyan_shulker_box", world.BlockName(placedShulker))
	}
	if gf := world.BlockProperties(placedShulker)["facing"]; gf != "up" {
		t.Fatalf("shulker_box placed above solid floor: facing=%q, want \"up\" (DirectionalPlaceContext)", gf)
	}
	sbe, ok := srv.StorageBlockEntityAt(dim, fx, fy, fz)
	if !ok || sbe == nil {
		t.Fatal("expected StorageBlockEntityAt placed shulker_box")
	}
	if sbe.CustomName != "Supply Crate" || sbe.Lock != "KeyCard" || sbe.Slots[3].ResolvedID() != "minecraft:diamond_pickaxe" || sbe.SlotMeta[3].Damage != 42 {
		t.Fatalf("placed shulker_box lost ShulkerBoxItemData: %+v", sbe)
	}

	// With air at (fx, fy-1, fz) (isEmptyBlock(below)), shulker_box places with facing=dispenserFacing ("north")!
	cache.SetBlock(fx, fy, fz, world.StateAir)
	cache.SetBlock(fx, fy-1, fz, world.StateAir)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:lime_shulker_box", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected lime_shulker_box dispense in air to succeed")
	}
	if gf := world.BlockProperties(cache.GetBlock(fx, fy, fz))["facing"]; gf != "north" {
		t.Fatalf("shulker_box placed with empty block below: facing=%q, want \"north\"", gf)
	}
	cache.SetBlock(fx, fy, fz, world.StateAir)
	cache.SetBlock(fx, fy-1, fz, stoneSt)

	// --- 5. carved_pumpkin, wither_skeleton_skull, other skulls, and animal equipment ---
	clearDroppedItemsNear(fx, fz)
	// carved_pumpkin with NO golem pattern and NO entity -> fails (false), does NOT place block and does NOT drop item!
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:carved_pumpkin", 1))
	if srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("carved_pumpkin without golem pattern or entity must fail (1001) in vanilla 26.1.2")
	}
	if world.BlockName(cache.GetBlock(fx, fy, fz)) != "minecraft:air" || countDroppedItemsNear(fx, fy, fz, "minecraft:carved_pumpkin") != 0 {
		t.Fatal("carved_pumpkin failed dispense must NOT place block or drop item")
	}

	// wither_skeleton_skull without Wither pattern or entity -> fails (false), does NOT place block or drop item!
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:wither_skeleton_skull", 1))
	if srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("wither_skeleton_skull without wither pattern or entity must fail (1001) in vanilla 26.1.2")
	}
	if world.BlockName(cache.GetBlock(fx, fy, fz)) != "minecraft:air" || countDroppedItemsNear(fx, fy, fz, "minecraft:wither_skeleton_skull") != 0 {
		t.Fatal("wither_skeleton_skull failed dispense must NOT place block or drop item")
	}

	// wither_skeleton_skull completing soul_sand T-pattern -> summons Wither!
	cache.SetBlock(fx, fy-2, fz, soulSand)
	cache.SetBlock(fx, fy-1, fz, soulSand)
	cache.SetBlock(fx-1, fy-1, fz, soulSand)
	cache.SetBlock(fx+1, fy-1, fz, soulSand)
	wSkullSt, _ := world.BlockStateByName("minecraft:wither_skeleton_skull", map[string]string{"rotation": "0"})
	cache.SetBlock(fx-1, fy, fz, wSkullSt)
	cache.SetBlock(fx+1, fy, fz, wSkullSt)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:wither_skeleton_skull", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected wither_skeleton_skull completing soul_sand T-pattern to summon Wither")
	}
	foundWither := false
	for _, ent := range srv.mobsInBlockBox(dim, fx, fy-1, fz) {
		if canonicalResourceName(ent.TypeName) == "minecraft:wither" {
			foundWither = true
		}
	}
	if !foundWither {
		for _, ent := range srv.entities.AppendInChunkRange(nil, int32(fx>>4), int32(fz>>4), 1) {
			if canonicalResourceName(ent.TypeName) == "minecraft:wither" {
				foundWither = true
			}
		}
	}
	if !foundWither {
		t.Fatal("expected minecraft:wither entity summoned by dispenser")
	}
	for _, ent := range srv.entities.AppendInChunkRange(nil, int32(fx>>4), int32(fz>>4), 1) {
		srv.entities.Remove(ent.ID)
	}
	cache.SetBlock(fx, fy-1, fz, stoneSt)
	cache.SetBlock(fx, fy, fz, world.StateAir)

	// skeleton_skull facing air without entity -> EquipmentDispenseItemBehavior falls back to ejecting 1 item!
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:skeleton_skull", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected skeleton_skull with no entity in front to eject item")
	}
	if world.BlockName(cache.GetBlock(fx, fy, fz)) != "minecraft:air" || countDroppedItemsNear(fx, fy, fz, "minecraft:skeleton_skull") != 1 {
		t.Fatal("skeleton_skull without entity must eject item, not place block")
	}
	clearDroppedItemsNear(fx, fz)

	// saddle, wolf_armor, copper_horse_armor, chest facing air without entity -> eject 1 item!
	for _, eqItem := range []string{"minecraft:saddle", "minecraft:wolf_armor", "minecraft:copper_horse_armor", "minecraft:chest"} {
		srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack(eqItem, 1))
		if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
			t.Fatalf("expected %s facing empty air to eject item via DefaultDispenseItemBehavior", eqItem)
		}
		if countDroppedItemsNear(fx, fy, fz, eqItem) != 1 {
			t.Fatalf("expected 1 dropped %s entity in front of dispenser", eqItem)
		}
		clearDroppedItemsNear(fx, fz)
	}

	// --- 6. Boats on dry land vs water + blue_egg/brown_egg + trident ---
	// pale_oak_boat facing air over stone (dry land) -> must eject boat item, NOT spawn boat entity!
	cache.SetBlock(fx, fy-1, fz, stoneSt)
	cache.SetBlock(fx, fy, fz, world.StateAir)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:pale_oak_boat", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected pale_oak_boat dispense to succeed (by ejecting item)")
	}
	if countDroppedItemsNear(fx, fy, fz, "minecraft:pale_oak_boat") != 1 {
		t.Fatal("pale_oak_boat facing dry land must be ejected as an ItemEntity, not spawned as a Boat!")
	}
	clearDroppedItemsNear(fx, fz)

	// blue_egg & brown_egg -> launch egg projectiles (not dropped items)!
	for _, eggID := range []string{"minecraft:blue_egg", "minecraft:brown_egg"} {
		srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack(eggID, 1))
		if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
			t.Fatalf("expected %s projectile dispense to succeed", eggID)
		}
		if countDroppedItemsNear(fx, fy, fz, eggID) != 0 {
			t.Fatalf("%s must launch projectile, not eject ItemEntity", eggID)
		}
	}

	// trident -> NOT in DISPENSER_REGISTRY in 26.1.2.jar, so dispenser ejects trident as an ItemEntity!
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:trident", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected trident default dispense to succeed")
	}
	if countDroppedItemsNear(fx, fy, fz, "minecraft:trident") != 1 {
		t.Fatal("trident is NOT in 26.1.2 DISPENSER_REGISTRY and must be ejected as an ItemEntity!")
	}
	clearDroppedItemsNear(fx, fz)

	// --- 7. 26.1.2 Copper Golem summon via carved_pumpkin + lightning_rod waxing + dyed sheep shearing ---
	weatheredCopper, _ := world.BlockStateByName("minecraft:weathered_copper", nil)
	cache.SetBlock(fx, fy-1, fz, weatheredCopper)
	cache.SetBlock(fx, fy, fz, world.StateAir)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:carved_pumpkin", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected carved_pumpkin over weathered_copper to summon copper_golem in vanilla 26.1.2")
	}
	if got := world.BlockName(cache.GetBlock(fx, fy-1, fz)); got != "minecraft:weathered_copper_chest" {
		t.Fatalf("expected weathered_copper replaced by weathered_copper_chest, got %q", got)
	}
	foundCG := false
	for _, ent := range srv.entities.AppendInChunkRange(nil, int32(fx>>4), int32(fz>>4), 1) {
		if canonicalResourceName(ent.TypeName) == MobTypeCopperGolem {
			foundCG = true
			if cgSt, ok := srv.GetCopperGolemState(ent.ID); !ok || cgSt.OxidationStage != world.CopperStageWeathered {
				t.Fatalf("expected copper_golem OxidationStage=Weathered, got %+v (ok=%v)", cgSt, ok)
			}
			srv.entities.Remove(ent.ID)
		}
	}
	if !foundCG {
		t.Fatal("expected minecraft:copper_golem entity summoned by carved_pumpkin dispense")
	}
	cache.SetBlock(fx, fy-1, fz, stoneSt)

	// Honeycomb waxes `minecraft:lightning_rod` -> `minecraft:waxed_lightning_rod` (26.1.2 WAXABLES).
	rodSt, ok := world.BlockStateByName("minecraft:lightning_rod", map[string]string{"facing": "up", "powered": "false", "waterlogged": "false"})
	if ok {
		cache.SetBlock(fx, fy, fz, rodSt)
		srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:honeycomb", 1))
		if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
			t.Fatal("expected honeycomb to wax lightning_rod in vanilla 26.1.2")
		}
		if got := world.BlockName(cache.GetBlock(fx, fy, fz)); got != "minecraft:waxed_lightning_rod" {
			t.Fatalf("waxed lightning_rod = %q, want minecraft:waxed_lightning_rod", got)
		}
		cache.SetBlock(fx, fy, fz, world.StateAir)
	}

	// Shears on a dyed blue sheep drop `minecraft:blue_wool` (not hardcoded white_wool).
	sheepID, err := srv.SpawnMobInDimension(dim, float64(fx)+0.5, float64(fy), float64(fz)+0.5, MobTypeSheep)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(sheep): %v", err)
	}
	srv.SetSheepColor(sheepID, DyeColorBlue)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:shears", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected shears on blue sheep to succeed")
	}
	if countDroppedItemsNear(fx, fy, fz, "minecraft:blue_wool") < 1 {
		t.Fatal("expected shears on blue sheep to drop minecraft:blue_wool")
	}
	srv.entities.Remove(sheepID)
}

func TestDropperWorldlyContainersDoubleChestVehicleAndMetadataPreservation2612(t *testing.T) {
	srv := newDispenserTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.BindNoteBlockAndJukeboxRedstone(dim)

	dx, dy, dz := 50, 70, 50
	fx, fy, fz := 51, 70, 50
	placeDispenserOrDropperForTest(t, srv, dim, dx, dy, dz, "minecraft:dropper", "east")

	// 1. Double chest at (51,70,50) [type=left, facing=east -> partner at (51,70,51) type=right]:
	// Fill all 27 slots of (51,70,50) with cobblestone, then dispense an enchanted sword from dropper ->
	// must land in slot 0 of the second half (51,70,51) WITH its ItemMeta intact!
	leftChest, _ := world.BlockStateByName("minecraft:chest", map[string]string{"facing": "east", "type": "left", "waterlogged": "false"})
	rightChest, _ := world.BlockStateByName("minecraft:chest", map[string]string{"facing": "east", "type": "right", "waterlogged": "false"})
	cache.SetBlock(fx, fy, fz, leftChest)
	cache.SetBlock(fx, fy, fz+1, rightChest)
	for i := 0; i < ChestSlotCount; i++ {
		srv.SetChestSlotInDim(dim, fx, fy, fz, i, world.NewNamedItemStack("minecraft:cobblestone", 64))
	}

	swordMeta := ItemMeta{
		Damage:       25,
		CustomName:   "Excalibur",
		Enchantments: map[string]int{"minecraft:sharpness": 5},
	}
	srv.SetDispenserSlotWithMeta(dim, dx, dy, dz, 0, world.NewNamedItemStack("minecraft:diamond_sword", 1), swordMeta)
	if !srv.DispenseFromDropper(dim, dx, dy, dz) {
		t.Fatal("expected dropper transfer into second half of double chest to succeed")
	}
	if got := srv.ChestSlotsInDim(dim, fx, fy, fz+1)[0]; got.ResolvedID() != "minecraft:diamond_sword" {
		t.Fatalf("expected diamond_sword in partner half slot[0], got %+v", got)
	}
	gotMeta, _, _ := srv.GetChestSlotMeta(dim, fx, fy, fz+1, 0)
	if gotMeta.Damage != 25 || gotMeta.CustomName != "Excalibur" || gotMeta.Enchantments["minecraft:sharpness"] != 5 {
		t.Fatalf("double chest dropper transfer lost ItemMeta: %+v", gotMeta)
	}

	// 2. Dropper facing a chest_minecart entity in front -> transfers item into chest_minecart!
	cache.SetBlock(fx, fy, fz, world.StateAir)
	cache.SetBlock(fx, fy, fz+1, world.StateAir)
	railSt, _ := world.BlockStateByName("minecraft:rail", nil)
	cache.SetBlock(fx, fy, fz, railSt)
	cartID, cartEnt := srv.SpawnVehicle(dim, "minecraft:chest_minecart", float64(fx)+0.5, float64(fy)+0.1, float64(fz)+0.5, 0)
	if cartID == 0 || cartEnt == nil {
		t.Fatalf("SpawnVehicle(chest_minecart) failed: id=%d ent=%v", cartID, cartEnt)
	}
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:emerald", 2))
	if !srv.DispenseFromDropper(dim, dx, dy, dz) {
		t.Fatal("expected dropper to transfer emerald into chest_minecart in front")
	}
	if got := srv.DispenserSlots(dx, dy, dz)[0].Count; got != 1 {
		t.Fatalf("expected 1 emerald remaining in dropper, got %d", got)
	}
	gotCartSlot0 := srv.getVehicleInventorySlot(cartID, 0)
	if gotCartSlot0.ResolvedID() != "minecraft:emerald" || gotCartSlot0.Count != 1 {
		t.Fatalf("expected 1 emerald inside chest_minecart, got %+v", gotCartSlot0)
	}
}

func TestCrafterNoQuasiConnectivityPositional3x3RemaindersContainerVsWorldAndComparator2612(t *testing.T) {
	srv := newDispenserTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.BindNoteBlockAndJukeboxRedstone(dim)

	cx, cy, cz := 80, 70, 80
	cache.SetBlock(cx, cy, cz, CrafterBlockState("east_up", false, false))

	// 1. NO quasi-connectivity: powering (cx, cy+1, cz) via redstone_block at (cx+1, cy+1, cz) must NOT trigger crafter!
	rbSt, _ := world.BlockStateByName("minecraft:redstone_block", nil)
	cache.ApplyBlockWithUpdates(cx+1, cy+1, cz, rbSt)
	if CrafterTriggered(cache.GetBlock(cx, cy, cz)) {
		t.Fatal("CrafterBlock must NOT have quasi-connectivity in vanilla 26.1.2")
	}
	cache.ApplyBlockWithUpdates(cx+1, cy+1, cz, world.StateAir)

	// 2. Positional 3x3 grid: 2 oak_planks separated by an empty row (slots 0 and 6) must NOT craft sticks!
	srv.SetCrafterSlot(dim, cx, cy, cz, 0, world.NewNamedItemStack("minecraft:oak_planks", 1), ItemMeta{})
	srv.SetCrafterSlot(dim, cx, cy, cz, 6, world.NewNamedItemStack("minecraft:oak_planks", 1), ItemMeta{})
	if _, ok := srv.DispenseFromCrafter(dim, cx, cy, cz); ok {
		t.Fatal("oak_planks at slots 0 and 6 (with empty slot 3 between) must NOT craft sticks!")
	}
	// Move slot 6 -> slot 3 -> crafts 4x stick!
	srv.SetCrafterSlot(dim, cx, cy, cz, 6, world.ItemStack{}, ItemMeta{})
	srv.SetCrafterSlot(dim, cx, cy, cz, 3, world.NewNamedItemStack("minecraft:oak_planks", 1), ItemMeta{})

	// 3. Place a SECOND crafter in front at (cx+1, cy, cz) -> crafted 4x stick must distribute 1-by-1 across slots 0..3 of target crafter,
	// AND LevelEvent 1049/2010 must NOT be emitted when fully inserted into a front container!
	cache.SetBlock(cx+1, cy, cz, CrafterBlockState("east_up", false, false))
	viewer, collect := addDispenserTestPlayer(t, srv, "CrafterViewer")
	srv.SetPlayerTransform(viewer, float64(cx)+0.5, float64(cy), float64(cz)+0.5, 0, 0, true)
	_ = collect()

	crafted, ok := srv.DispenseFromCrafter(dim, cx, cy, cz)
	if !ok || crafted.ResolvedID() != "minecraft:stick" || crafted.Count != 4 {
		t.Fatalf("expected 4x stick crafted, got (%+v, %v)", crafted, ok)
	}
	targetCr, ok := srv.GetCrafterAt(dim, cx+1, cy, cz)
	if !ok || targetCr == nil {
		t.Fatal("expected target crafter state")
	}
	for s := 0; s < 4; s++ {
		if targetCr.Slots[s].ResolvedID() != "minecraft:stick" || targetCr.Slots[s].Count != 1 {
			t.Fatalf("expected target crafter slot[%d] = 1x stick (even 1-by-1 insertion), got %+v", s, targetCr.Slots[s])
		}
	}
	for _, pkt := range collect() {
		if pkt.ID == PlayLevelEventCB {
			r := protocol.NewReader(pkt.Payload)
			if ev, err := r.Int32(); err == nil && (ev == LevelEventCrafterCraft || ev == LevelEventCrafterShootParticles) {
				t.Fatalf("CrafterBlock.dispenseItem must NOT emit LevelEvent %d when entire stack entered front container!", ev)
			}
		}
	}

	// 4. World ejection: remove target crafter, craft cake (3x milk_bucket, 2x sugar, 1x egg, 3x wheat) ->
	// ejects 1x cake + 3x bucket, emits LevelEvent 1049 and 2010 with data = 5 (east)!
	cache.SetBlock(cx+1, cy, cz, world.StateAir)
	for s := 0; s < 3; s++ {
		srv.SetCrafterSlot(dim, cx, cy, cz, s, world.NewNamedItemStack("minecraft:milk_bucket", 1), ItemMeta{})
	}
	srv.SetCrafterSlot(dim, cx, cy, cz, 3, world.NewNamedItemStack("minecraft:sugar", 1), ItemMeta{})
	srv.SetCrafterSlot(dim, cx, cy, cz, 4, world.NewNamedItemStack("minecraft:egg", 1), ItemMeta{})
	srv.SetCrafterSlot(dim, cx, cy, cz, 5, world.NewNamedItemStack("minecraft:sugar", 1), ItemMeta{})
	for s := 6; s < 9; s++ {
		srv.SetCrafterSlot(dim, cx, cy, cz, s, world.NewNamedItemStack("minecraft:wheat", 1), ItemMeta{})
	}
	_ = collect()

	cakeRes, ok := srv.DispenseFromCrafter(dim, cx, cy, cz)
	if !ok || cakeRes.ResolvedID() != "minecraft:cake" {
		t.Fatalf("expected cake crafted, got (%+v, %v)", cakeRes, ok)
	}
	saw1049, saw2010East := false, false
	for _, pkt := range collect() {
		if pkt.ID == PlayLevelEventCB {
			r := protocol.NewReader(pkt.Payload)
			ev, err1 := r.Int32()
			_, err2 := r.Int64()
			data, err3 := r.Int32()
			if err1 == nil && err2 == nil && err3 == nil {
				if ev == LevelEventCrafterCraft {
					saw1049 = true
				}
				if ev == LevelEventCrafterShootParticles && data == 5 {
					saw2010East = true
				}
			}
		}
	}
	if !saw1049 || !saw2010East {
		t.Fatalf("expected LevelEvent 1049 and 2010(data=5 east) on world ejection, got 1049=%v 2010East=%v", saw1049, saw2010East)
	}
}

func TestDispenserDropperAndCrafterAnvilMCAPersistenceAndScheduledTicks2612(t *testing.T) {
	dir := t.TempDir()
	store, err := world.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	cache := world.NewCacheWithStore(-1, world.GenerateFlat, store)
	cfg := DefaultConfig()
	cfg.WorldDir = dir
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	srv.store = store
	dim := world.DimensionOverworld
	srv.BindNoteBlockAndJukeboxRedstone(dim)

	// 1. Dispenser at (2, 65, 2) with CustomName, Lock, enchanted bow, water potion, and active 4-tick redstone trigger!
	placeDispenserOrDropperForTest(t, srv, dim, 2, 65, 2, "minecraft:dispenser", "south")
	srv.SetDispenserSlotWithMeta(dim, 2, 65, 2, 0, world.NewNamedItemStack("minecraft:bow", 1), ItemMeta{
		Damage:       11,
		CustomName:   "Sniper",
		Enchantments: map[string]int{"minecraft:power": 3},
	})
	srv.SetDispenserSlotWithPotion(dim, 2, 65, 2, 4, world.NewNamedItemStack("minecraft:potion", 1), PotionWater)
	srv.SetDispenserSlot(2, 65, 2, 8, world.NewNamedItemStack("minecraft:cobblestone", 5))
	// Trigger rising edge via redstone_block at (3, 65, 2), step 1 tick so 3 ticks remain in block_ticks!
	rbSt, _ := world.BlockStateByName("minecraft:redstone_block", nil)
	cache.ApplyBlockWithUpdates(3, 65, 2, rbSt)
	cache.TickBlockUpdates()

	// 2. Dropper at (4, 65, 2) with packed LootTable + LootTableSeed (Items must be omitted in .mca while packed!).
	placeDispenserOrDropperForTest(t, srv, dim, 4, 65, 2, "minecraft:dropper", "north")
	packedDropperNBT := nbt.NewCompound()
	packedDropperNBT.Set("id", nbt.String("minecraft:dropper"))
	packedDropperNBT.Set("x", nbt.Int(4))
	packedDropperNBT.Set("y", nbt.Int(65))
	packedDropperNBT.Set("z", nbt.Int(2))
	packedDropperNBT.Set("CustomName", nbt.String("Dungeon Dropper"))
	packedDropperNBT.Set("lock", nbt.String("SecretKey"))
	packedDropperNBT.Set("LootTable", nbt.String("minecraft:chests/simple_dungeon"))
	packedDropperNBT.Set("LootTableSeed", nbt.Long(987654321))
	srv.LoadChunkDispenserBlockEntitiesNBT(dim, []*nbt.Compound{packedDropperNBT})

	// 3. Crafter at (6, 65, 2) powered from above at (6, 66, 2) with disabled slots (1, 7), triggered=true, crafting_ticks_remaining=4, CustomName, lock, and item with meta!
	cache.SetBlock(6, 66, 2, rbSt)
	cache.SetBlock(6, 65, 2, CrafterBlockState("south_up", true, true))
	srv.SetCrafterSlotDisabled(dim, 6, 65, 2, 1, true)
	srv.SetCrafterSlotDisabled(dim, 6, 65, 2, 7, true)
	srv.SetCrafterSlot(dim, 6, 65, 2, 0, world.NewNamedItemStack("minecraft:oak_log", 3), ItemMeta{CustomName: "Named Log"})
	tm := srv.crafterTimers()
	tm.mu.Lock()
	tm.timers[hopperBlockKey{dim: dim, x: 6, y: 65, z: 2}] = 4
	tm.mu.Unlock()

	// Save world and remove sidecar .nbt/.dat files so reload comes strictly from .mca chunk NBT!
	if err := srv.SaveAll(); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}
	_ = store.Close()
	_ = os.Remove(filepath.Join(dir, "dispensers.nbt"))
	_ = os.Remove(filepath.Join(dir, "crafters.dat"))
	_ = os.Remove(filepath.Join(dir, "redstone_runtime.nbt"))

	// Inspect raw .mca block_entities directly in Store!
	store2, err := world.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore reopen: %v", err)
	}
	t.Cleanup(func() { _ = store2.Close() })

	rawBEs, err := store2.LoadAllBlockEntities(func(id string) bool {
		return id == "minecraft:dispenser" || id == "minecraft:dropper" || id == "minecraft:crafter"
	})
	if err != nil {
		t.Fatalf("LoadAllBlockEntities: %v", err)
	}
	if len(rawBEs) != 3 {
		t.Fatalf("expected 3 block_entities (dispenser, dropper, crafter) in .mca chunk, got %d", len(rawBEs))
	}
	byID := make(map[string]*nbt.Compound)
	for _, c := range rawBEs {
		id, _ := nbtGetString(c, "id")
		byID[id] = c
		if _, hasDim := c.Get("Dimension"); hasDim {
			t.Fatalf(".mca block_entity %s must NOT contain non-vanilla Dimension key!", id)
		}
	}

	// Verify Dispenser .mca NBT uses Vanilla 26.1.2 `components` (no top-level Damage/Potion/Meta).
	dispItems, ok := nbtGetListElems(byID["minecraft:dispenser"], "Items")
	if !ok || len(dispItems) != 3 {
		t.Fatalf("expected 3 Items in dispenser .mca NBT, got %d", len(dispItems))
	}
	firstItem := dispItems[0].(*nbt.Compound)
	if _, hasLegacyDmg := firstItem.Get("Damage"); hasLegacyDmg {
		t.Fatal("dispenser item NBT must use 26.1.2 `components`, not top-level `Damage`")
	}
	if _, hasComp := firstItem.Get("components"); !hasComp {
		t.Fatal("expected `components` compound on enchanted bow in dispenser .mca NBT")
	}

	// Verify packed Dropper .mca NBT preserves LootTable/LootTableSeed/CustomName/lock and omits Items!
	dropComp := byID["minecraft:dropper"]
	if lt, _ := nbtGetString(dropComp, "LootTable"); lt != "minecraft:chests/simple_dungeon" {
		t.Fatalf("dropper .mca LootTable = %q, want minecraft:chests/simple_dungeon", lt)
	}
	if _, hasItems := dropComp.Get("Items"); hasItems {
		t.Fatal("packed LootTable dropper must omit Items in .mca NBT")
	}

	// Verify Crafter .mca NBT has triggered (Int 1), crafting_ticks_remaining (Int 4), disabled_slots (IntArray [1, 7]), and components!
	crComp := byID["minecraft:crafter"]
	if trig, ok := nbtGetInt(crComp, "triggered"); !ok || trig != 1 {
		t.Fatalf("crafter .mca triggered = (%d, %v), want (1, true)", trig, ok)
	}
	if remTicks, ok := nbtGetInt(crComp, "crafting_ticks_remaining"); !ok || remTicks != 4 {
		t.Fatalf("crafter .mca crafting_ticks_remaining = (%d, %v), want (4, true)", remTicks, ok)
	}
	rawDis, hasDis := crComp.Get("disabled_slots")
	intArr, isIntArr := rawDis.(nbt.IntArray)
	if !hasDis || !isIntArr || len(intArr) != 2 || intArr[0] != 1 || intArr[1] != 7 {
		t.Fatalf("crafter .mca disabled_slots = %T %+v, want nbt.IntArray{1, 7}", rawDis, rawDis)
	}

	// Reload into a fresh Cache + Server strictly from .mca!
	cache2 := world.NewCacheWithStore(-1, world.GenerateFlat, store2)
	cfg2 := DefaultConfig()
	cfg2.WorldDir = ""
	srv2, err := NewWithCache(cfg2, cache2)
	if err != nil {
		t.Fatalf("NewWithCache srv2: %v", err)
	}
	srv2.store = store2
	srv2.BindNoteBlockAndJukeboxRedstone(dim)
	_ = cache2.GetBlock(2, 65, 2) // ensure chunk (0,0) is loaded from store2

	if gotMeta := srv2.DispenserSlotMeta(dim, 2, 65, 2, 0); gotMeta.Damage != 11 || gotMeta.CustomName != "Sniper" || gotMeta.Enchantments["minecraft:power"] != 3 {
		t.Fatalf("reloaded dispenser slot[0] meta mismatch: %+v", gotMeta)
	}
	if gotPot := srv2.DispenserSlotPotion(dim, 2, 65, 2, 4); gotPot != PotionWater {
		t.Fatalf("reloaded dispenser slot[4] potion = %q, want %q", gotPot, PotionWater)
	}

	crReloaded, ok := srv2.GetCrafterAt(dim, 6, 65, 2)
	if !ok || !crReloaded.Disabled[1] || !crReloaded.Disabled[7] || crReloaded.Slots[0].Count != 3 || crReloaded.SlotMeta[0].CustomName != "Named Log" {
		t.Fatalf("reloaded crafter mismatch: %+v", crReloaded)
	}
	if sig := srv2.ComparatorSignalAt(dim, 6, 65, 2); sig != 3 {
		t.Fatalf("reloaded crafter comparator signal = %d, want 3 (2 disabled + 1 occupied)", sig)
	}

	// Step the remaining 3 scheduled block_ticks on cache2 -> dispenser at (2,65,2) must fire on schedule!
	beforeTotal := srv2.DispenserSlots(2, 65, 2)[0].Count + srv2.DispenserSlots(2, 65, 2)[4].Count + srv2.DispenserSlots(2, 65, 2)[8].Count
	for i := 0; i < 3; i++ {
		cache2.TickBlockUpdates()
	}
	afterTotal := srv2.DispenserSlots(2, 65, 2)[0].Count + srv2.DispenserSlots(2, 65, 2)[4].Count + srv2.DispenserSlots(2, 65, 2)[8].Count
	if afterTotal >= beforeTotal {
		// Note: if water potion dispensed into air, it ejects 1 item -> count decreases by 1!
		t.Fatalf("expected persisted 4-tick block_ticks on dispenser to fire after 3 remaining ticks (before=%d after=%d)", beforeTotal, afterTotal)
	}

	// Dispensing from the packed LootTable dropper at (4,65,2) must unpack simple_dungeon loot and dispense an item!
	if !srv2.DispenseFromDropper(dim, 4, 65, 2) {
		t.Fatal("expected packed LootTable dropper to unpack loot and dispense on activation")
	}
}
