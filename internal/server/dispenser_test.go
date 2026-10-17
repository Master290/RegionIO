package server

import (
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

type capturedDispenserPacket struct {
	ID      int32
	Payload []byte
}

func newDispenserTestServer(t *testing.T) *Server {
	t.Helper()
	return newBrewingTestServer(t)
}

func addDispenserTestPlayer(t *testing.T, srv *Server, name string) (*PlayerSession, func() []capturedDispenserPacket) {
	t.Helper()
	var pkts []capturedDispenserPacket
	var uuid [16]byte
	copy(uuid[:], []byte(name))
	sess, err := srv.RegisterPlayer(Profile{
		UUID: uuid,
		Name: name,
	}, func(id int32, body []byte) error {
		cp := make([]byte, len(body))
		copy(cp, body)
		pkts = append(pkts, capturedDispenserPacket{ID: id, Payload: cp})
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer(%s): %v", name, err)
	}
	srv.SetPlayerGameMode(sess, GameModeSurvival)
	collect := func() []capturedDispenserPacket {
		out := pkts
		pkts = nil
		return out
	}
	return sess, collect
}

func placeDispenserOrDropperForTest(t *testing.T, srv *Server, dim string, x, y, z int, blockName, facing string) *world.Cache {
	t.Helper()
	cache := srv.BindNoteBlockAndJukeboxRedstone(dim)
	if cache == nil {
		t.Fatalf("nil cache for dimension %s", dim)
	}
	st, ok := world.BlockStateByName(blockName, map[string]string{
		"facing":    facing,
		"triggered": "false",
	})
	if !ok {
		t.Fatalf("failed to resolve block state for %s[facing=%s]", blockName, facing)
	}
	cache.SetBlock(x, y, z, st)
	return cache
}

func TestDispenserRedstoneRisingEdgeDelayAndQuasiConnectivity(t *testing.T) {
	srv := newDispenserTestServer(t)
	dim := world.DimensionOverworld
	dx, dy, dz := 20, 70, 20
	cache := placeDispenserOrDropperForTest(t, srv, dim, dx, dy, dz, "minecraft:dispenser", "south")

	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:cobblestone", 2))

	// Power the dispenser by placing a redstone_block at (dx+1, dy, dz).
	rbState, ok := world.BlockStateByName("minecraft:redstone_block", nil)
	if !ok {
		t.Fatal("missing minecraft:redstone_block state")
	}
	cache.ApplyBlockWithUpdates(dx+1, dy, dz, rbState)

	// Immediately on the rising edge, triggered must be "true", but the item is
	// not dispensed until the 4-game-tick scheduled update fires.
	props := world.BlockProperties(cache.GetBlock(dx, dy, dz))
	if props["triggered"] != "true" {
		t.Fatalf("dispenser triggered = %q on rising edge, want true", props["triggered"])
	}
	if got := srv.DispenserSlots(dx, dy, dz)[0].Count; got != 2 {
		t.Fatalf("slot[0].Count before 4-tick delay = %d, want 2", got)
	}

	// Step ticks 1..3: still waiting.
	for i := 0; i < 3; i++ {
		cache.TickBlockUpdates()
	}
	if got := srv.DispenserSlots(dx, dy, dz)[0].Count; got != 2 {
		t.Fatalf("slot[0].Count at tick 3 = %d, want 2", got)
	}

	// Tick 4: dispenser fires and ejects 1 cobblestone!
	cache.TickBlockUpdates()
	if got := srv.DispenserSlots(dx, dy, dz)[0].Count; got != 1 {
		t.Fatalf("slot[0].Count at tick 4 = %d, want 1", got)
	}

	// Staying powered does not re-trigger until unpowered and powered again.
	cache.TickBlockUpdates()
	if got := srv.DispenserSlots(dx, dy, dz)[0].Count; got != 1 {
		t.Fatalf("slot[0].Count while continuously powered = %d, want 1", got)
	}

	// Remove redstone_block -> triggered resets to "false".
	cache.ApplyBlockWithUpdates(dx+1, dy, dz, world.StateAir)
	if got := world.BlockProperties(cache.GetBlock(dx, dy, dz))["triggered"]; got != "false" {
		t.Fatalf("dispenser triggered after unpowering = %q, want false", got)
	}

	// Test quasi-connectivity: power the air cell above the dispenser by placing a
	// redstone_block at (dx+1, dy+1, dz) (adjacent to (dx, dy+1, dz)).
	cache.ApplyBlockWithUpdates(dx+1, dy+1, dz, rbState)
	if got := world.BlockProperties(cache.GetBlock(dx, dy, dz))["triggered"]; got != "true" {
		t.Fatalf("dispenser triggered via quasi-connectivity = %q, want true", got)
	}
	for i := 0; i < 4; i++ {
		cache.TickBlockUpdates()
	}
	if !srv.DispenserSlots(dx, dy, dz)[0].IsEmpty() {
		t.Fatalf("expected second cobblestone to dispense after quasi-connectivity 4-tick delay")
	}
}

func TestDispenserProjectilesArrowsPotionsXPFireworksAndFireCharge(t *testing.T) {
	srv := newDispenserTestServer(t)
	dim := world.DimensionOverworld
	dx, dy, dz := 40, 70, 40
	placeDispenserOrDropperForTest(t, srv, dim, dx, dy, dz, "minecraft:dispenser", "east")

	// Place a target player in front of the dispenser at (dx+1, dy, dz).
	target, _ := addDispenserTestPlayer(t, srv, "TargetPlayer")
	srv.SetPlayerTransform(target, float64(dx+1)+0.5, float64(dy), float64(dz)+0.5, 0, 0, true)

	// 1. Tipped arrow of poison -> damages player and applies Poison effect.
	srv.SetDispenserSlotWithPotion(dim, dx, dy, dz, 0, world.NewNamedItemStack("minecraft:tipped_arrow", 1), "minecraft:poison")
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected tipped_arrow dispense to succeed")
	}
	if !target.HasPlayerEffect(EffectPoison) {
		t.Fatal("expected target player hit by dispensed tipped_arrow of poison to have EffectPoison")
	}

	// 2. Spectral arrow -> applies EffectGlowing.
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:spectral_arrow", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected spectral_arrow dispense to succeed")
	}
	if !target.HasPlayerEffect(EffectGlowing) {
		t.Fatal("expected target player hit by dispensed spectral_arrow to have EffectGlowing")
	}

	// 3. Lingering potion -> spawns lingering potion cloud in front.
	srv.SetDispenserSlotWithPotion(dim, dx, dy, dz, 0, world.NewNamedItemStack("minecraft:lingering_potion", 1), "minecraft:regeneration")
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected lingering_potion dispense to succeed")
	}
	if len(srv.ActiveLingeringClouds()) == 0 {
		t.Fatal("expected dispensed lingering_potion to spawn a lingering cloud")
	}

	// 4. Experience bottle -> spawns XP orbs in front.
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:experience_bottle", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected experience_bottle dispense to succeed")
	}

	// 5. Firework rocket -> spawns angled firework rocket entity.
	srv.SetDispenserSlotWithFirework(dim, dx, dy, dz, 0, world.NewNamedItemStack("minecraft:firework_rocket", 1), FireworksComponent{
		FlightDuration: 2,
		Explosions: []FireworkExplosion{
			{Shape: FireworkShapeLargeBall, Colors: []int32{0xFF0000}},
		},
	})
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected firework_rocket dispense to succeed")
	}

	// 6. Fire charge -> launches small_fireball and damages player in front.
	beforeHP := target.Snapshot().Health
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:fire_charge", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected fire_charge dispense to succeed")
	}
	if afterHP := target.Snapshot().Health; afterHP >= beforeHP {
		t.Fatalf("expected fire_charge to deal damage to player in front: before=%v after=%v", beforeHP, afterHP)
	}
}

func TestDispenserBucketsPlaceAndScoopFluidsAndFish(t *testing.T) {
	srv := newDispenserTestServer(t)
	dim := world.DimensionOverworld
	dx, dy, dz := 60, 70, 60
	cache := placeDispenserOrDropperForTest(t, srv, dim, dx, dy, dz, "minecraft:dispenser", "north")
	fx, fy, fz := dx, dy, dz-1

	// 1. Place water from water_bucket -> front becomes water, slot becomes bucket.
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:water_bucket", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected water_bucket dispense to succeed")
	}
	if got := world.BlockName(cache.GetBlock(fx, fy, fz)); got != "minecraft:water" {
		t.Fatalf("front block after water_bucket dispense = %q, want minecraft:water", got)
	}
	if got := srv.DispenserSlots(dx, dy, dz)[0].ResolvedID(); got != "minecraft:bucket" {
		t.Fatalf("slot[0] after water_bucket dispense = %q, want minecraft:bucket", got)
	}

	// 2. Scoop water back with the empty bucket -> front becomes air, slot becomes water_bucket.
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected empty bucket scoop to succeed")
	}
	if got := world.BlockName(cache.GetBlock(fx, fy, fz)); got != "minecraft:air" {
		t.Fatalf("front block after scooping water = %q, want minecraft:air", got)
	}
	if got := srv.DispenserSlots(dx, dy, dz)[0].ResolvedID(); got != "minecraft:water_bucket" {
		t.Fatalf("slot[0] after scooping water = %q, want minecraft:water_bucket", got)
	}

	// 3. Place lava and scoop it back.
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:lava_bucket", 1))
	srv.DispenseFromDispenser(dim, dx, dy, dz)
	if got := world.BlockName(cache.GetBlock(fx, fy, fz)); got != "minecraft:lava" {
		t.Fatalf("front block after lava_bucket dispense = %q, want minecraft:lava", got)
	}
	srv.DispenseFromDispenser(dim, dx, dy, dz)
	if got := srv.DispenserSlots(dx, dy, dz)[0].ResolvedID(); got != "minecraft:lava_bucket" {
		t.Fatalf("slot[0] after scooping lava = %q, want minecraft:lava_bucket", got)
	}

	// 4. Place powder_snow and scoop it back.
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:powder_snow_bucket", 1))
	srv.DispenseFromDispenser(dim, dx, dy, dz)
	if got := world.BlockName(cache.GetBlock(fx, fy, fz)); got != "minecraft:powder_snow" {
		t.Fatalf("front block after powder_snow_bucket dispense = %q, want minecraft:powder_snow", got)
	}
	srv.DispenseFromDispenser(dim, dx, dy, dz)
	if got := srv.DispenserSlots(dx, dy, dz)[0].ResolvedID(); got != "minecraft:powder_snow_bucket" {
		t.Fatalf("slot[0] after scooping powder_snow = %q, want minecraft:powder_snow_bucket", got)
	}

	// 5. Fish bucket (cod_bucket) -> places water, spawns cod entity, leaves empty bucket.
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:cod_bucket", 1))
	srv.DispenseFromDispenser(dim, dx, dy, dz)
	if got := world.BlockName(cache.GetBlock(fx, fy, fz)); got != "minecraft:water" {
		t.Fatalf("front block after cod_bucket dispense = %q, want minecraft:water", got)
	}
	if got := srv.DispenserSlots(dx, dy, dz)[0].ResolvedID(); got != "minecraft:bucket" {
		t.Fatalf("slot[0] after cod_bucket dispense = %q, want minecraft:bucket", got)
	}
	mobs := srv.mobsInBlockBox(dim, fx, fy, fz)
	if len(mobs) == 0 || canonicalResourceName(mobs[0].TypeName) != "minecraft:cod" {
		t.Fatalf("expected minecraft:cod entity in front block after cod_bucket dispense, got %+v", mobs)
	}
}

func TestDispenserBoneMealShulkerBoxAndHoneycomb(t *testing.T) {
	srv := newDispenserTestServer(t)
	dim := world.DimensionOverworld
	dx, dy, dz := 80, 70, 80
	cache := placeDispenserOrDropperForTest(t, srv, dim, dx, dy, dz, "minecraft:dispenser", "south")
	fx, fy, fz := dx, dy, dz+1

	// 1. Bone meal on wheat[age=0] -> advances wheat age and consumes 1 bone_meal.
	farmland, _ := world.BlockStateByName("minecraft:farmland", map[string]string{"moisture": "7"})
	wheat0, _ := world.BlockStateByName("minecraft:wheat", map[string]string{"age": "0"})
	cache.SetBlock(fx, fy-1, fz, farmland)
	cache.SetBlock(fx, fy, fz, wheat0)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:bone_meal", 2))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected bone_meal on wheat to succeed")
	}
	if age := world.BlockProperties(cache.GetBlock(fx, fy, fz))["age"]; age == "0" || age == "" {
		t.Fatalf("expected wheat age > 0 after bone_meal dispense, got %q", age)
	}
	if got := srv.DispenserSlots(dx, dy, dz)[0].Count; got != 1 {
		t.Fatalf("remaining bone_meal count = %d, want 1", got)
	}

	// Bone meal on cobblestone -> fails (returns false) and does NOT consume bone_meal.
	cobble, _ := world.BlockStateByName("minecraft:cobblestone", nil)
	cache.SetBlock(fx, fy, fz, cobble)
	if srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected bone_meal on cobblestone to fail")
	}
	if got := srv.DispenserSlots(dx, dy, dz)[0].Count; got != 1 {
		t.Fatalf("bone_meal count after failed dispense = %d, want 1", got)
	}

	// 2. Shulker box placement with preserved inventory.
	cache.SetBlock(fx, fy, fz, world.StateAir)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:red_shulker_box", 1))
	var shulkerInv [ChestSlotCount]world.ItemStack
	shulkerInv[0] = world.NewNamedItemStack("minecraft:diamond", 12)
	srv.SetDispenserSlotShulkerItems(dim, dx, dy, dz, 0, shulkerInv)
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected red_shulker_box placement to succeed")
	}
	if got := world.BlockName(cache.GetBlock(fx, fy, fz)); got != "minecraft:red_shulker_box" {
		t.Fatalf("placed block = %q, want minecraft:red_shulker_box", got)
	}
	if got := srv.ShulkerBoxSlots(dim, fx, fy, fz)[0]; got.ResolvedID() != "minecraft:diamond" || got.Count != 12 {
		t.Fatalf("placed shulker box slot[0] = %+v, want 12x diamond", got)
	}

	// 3. Honeycomb waxing copper_block -> waxed_copper_block.
	copperSt, ok := world.BlockStateByName("minecraft:copper_block", nil)
	if !ok {
		t.Fatal("missing minecraft:copper_block state")
	}
	cache.SetBlock(fx, fy, fz, copperSt)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:honeycomb", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected honeycomb waxing to succeed")
	}
	if got := world.BlockName(cache.GetBlock(fx, fy, fz)); got != "minecraft:waxed_copper_block" {
		t.Fatalf("waxed block = %q, want minecraft:waxed_copper_block", got)
	}
}

func TestDispenserEquipPlayerArmorStandMobHorseAndSummonGolem(t *testing.T) {
	srv := newDispenserTestServer(t)
	dim := world.DimensionOverworld
	dx, dy, dz := 100, 70, 100
	cache := placeDispenserOrDropperForTest(t, srv, dim, dx, dy, dz, "minecraft:dispenser", "east")
	fx, fy, fz := dx+1, dy, dz

	// 1. Equip diamond_chestplate and elytra onto a Player in front.
	p, _ := addDispenserTestPlayer(t, srv, "ArmorReceiver")
	srv.SetPlayerTransform(p, float64(fx)+0.5, float64(fy), float64(fz)+0.5, 0, 0, true)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:elytra", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected elytra equip onto player to succeed")
	}
	if got := p.InventorySlot(6).ResolvedID(); got != "minecraft:elytra" {
		t.Fatalf("player chest slot (6) = %q, want minecraft:elytra", got)
	}
	// Move player out of the way.
	srv.SetPlayerTransform(p, float64(dx-10), float64(dy), float64(dz), 0, 0, true)

	// 2. Equip diamond_helmet onto an Armor Stand in front.
	standID, err := srv.SpawnArmorStand(dim, float64(fx)+0.5, float64(fy), float64(fz)+0.5, 0)
	if err != nil {
		t.Fatalf("SpawnArmorStand: %v", err)
	}
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:diamond_helmet", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected diamond_helmet equip onto armor stand to succeed")
	}
	st, _ := srv.ArmorStandState(standID)
	if got := st.Equipment[EquipmentSlotHead].ResolvedID(); got != "minecraft:diamond_helmet" {
		t.Fatalf("armor stand head slot = %q, want minecraft:diamond_helmet", got)
	}
	srv.entities.Remove(standID)

	// 3. Equip carved_pumpkin onto a Zombie in front.
	zombieID, err := srv.SpawnMobInDimension(dim, float64(fx)+0.5, float64(fy), float64(fz)+0.5, MobTypeZombie)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(zombie): %v", err)
	}
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:carved_pumpkin", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected carved_pumpkin equip onto zombie to succeed")
	}
	if got := srv.MobEquipment(zombieID, EquipmentSlotHead).ResolvedID(); got != "minecraft:carved_pumpkin" {
		t.Fatalf("zombie head equipment = %q, want minecraft:carved_pumpkin", got)
	}
	srv.entities.Remove(zombieID)

	// 4. Equip saddle and diamond_horse_armor onto a tamed Horse in front.
	horseID, err := srv.SpawnMobInDimension(dim, float64(fx)+0.5, float64(fy), float64(fz)+0.5, MobTypeHorse)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(horse): %v", err)
	}
	srv.TameMob(horseID, p)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:saddle", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected saddle equip onto tamed horse to succeed")
	}
	if !srv.IsEntitySaddled(horseID) || !srv.IsEquineSaddled(horseID) {
		t.Fatal("expected tamed horse to be saddled after dispenser saddle")
	}
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:diamond_horse_armor", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected diamond_horse_armor equip onto tamed horse to succeed")
	}
	if got := srv.HorseArmor(horseID); got != "minecraft:diamond_horse_armor" {
		t.Fatalf("horse armor = %q, want minecraft:diamond_horse_armor", got)
	}
	if got := srv.HorseArmorItem(horseID); got != "minecraft:diamond_horse_armor" {
		t.Fatalf("HorseArmorItem = %q, want minecraft:diamond_horse_armor", got)
	}
	srv.entities.Remove(horseID)

	// 4b. Equip chest onto a tamed Donkey and red_carpet + chest onto a tamed Llama in front.
	donkeyID, err := srv.SpawnMobInDimension(dim, float64(fx)+0.5, float64(fy), float64(fz)+0.5, MobTypeDonkey)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(donkey): %v", err)
	}
	srv.TameMob(donkeyID, p)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:chest", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected chest equip onto tamed donkey to succeed")
	}
	if !srv.HasEquineChest(donkeyID) || !srv.IsAnimalChested(donkeyID) {
		t.Fatal("expected tamed donkey to have equine chest after dispenser chest")
	}
	srv.entities.Remove(donkeyID)

	llamaID, err := srv.SpawnMobInDimension(dim, float64(fx)+0.5, float64(fy), float64(fz)+0.5, MobTypeLlama)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(llama): %v", err)
	}
	srv.TameMob(llamaID, p)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:red_carpet", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected red_carpet equip onto tamed llama to succeed")
	}
	if got := srv.LlamaCarpetItem(llamaID); got != "minecraft:red_carpet" {
		t.Fatalf("LlamaCarpetItem = %q, want minecraft:red_carpet", got)
	}
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:chest", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected chest equip onto tamed llama to succeed")
	}
	if !srv.HasEquineChest(llamaID) {
		t.Fatal("expected tamed llama to have equine chest after dispenser chest")
	}
	srv.entities.Remove(llamaID)

	// 5. Dispensing carved_pumpkin onto 2 snow_blocks summons a Snow Golem!
	snowBlock, _ := world.BlockStateByName("minecraft:snow_block", nil)
	cache.SetBlock(fx, fy-1, fz, snowBlock)
	cache.SetBlock(fx, fy-2, fz, snowBlock)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:carved_pumpkin", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected carved_pumpkin placement on snow blocks to succeed")
	}
	if got := world.BlockName(cache.GetBlock(fx, fy-1, fz)); got != "minecraft:air" {
		t.Fatalf("expected snow_block to be cleared when Snow Golem is summoned, got %q", got)
	}
}

func TestDispenserMinecartBoatFlintAndSteelPrimedTNTShearsAndBottles(t *testing.T) {
	srv := newDispenserTestServer(t)
	dim := world.DimensionOverworld
	dx, dy, dz := 120, 70, 120
	cache := placeDispenserOrDropperForTest(t, srv, dim, dx, dy, dz, "minecraft:dispenser", "west")
	fx, fy, fz := dx-1, dy, dz

	// 1. Minecart on rail.
	railSt, _ := world.BlockStateByName("minecraft:rail", nil)
	cache.SetBlock(fx, fy, fz, railSt)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:minecart", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected minecart dispense onto rail to succeed")
	}
	if !srv.DispenserSlots(dx, dy, dz)[0].IsEmpty() {
		t.Fatal("expected minecart item to be consumed")
	}

	// 2. Boat on water.
	cache.SetBlock(fx, fy, fz, world.StateWater)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:oak_boat", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected oak_boat dispense onto water to succeed")
	}

	// 3. Glass bottle from water -> fills water potion.
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:glass_bottle", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected glass_bottle dispense into water to succeed")
	}
	if got := srv.DispenserSlots(dx, dy, dz)[0].ResolvedID(); got != "minecraft:potion" {
		t.Fatalf("slot[0] after glass_bottle in water = %q, want minecraft:potion", got)
	}
	if pot := srv.DispenserSlotPotion(dim, dx, dy, dz, 0); pot != PotionWater {
		t.Fatalf("slot[0] potion type = %q, want %q", pot, PotionWater)
	}

	// 4. Beehive at honey_level=5: shears harvest 3 honeycombs, glass_bottle harvests honey_bottle.
	beehive5, ok := world.BlockStateByName("minecraft:beehive", map[string]string{
		"facing":      "north",
		"honey_level": "5",
	})
	if !ok {
		t.Fatal("missing minecraft:beehive[honey_level=5] state")
	}
	cache.SetBlock(fx, fy, fz, beehive5)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:shears", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected shears on beehive[honey_level=5] to succeed")
	}
	if hl := world.BlockProperties(cache.GetBlock(fx, fy, fz))["honey_level"]; hl != "0" {
		t.Fatalf("beehive honey_level after shears = %q, want 0", hl)
	}
	if dmg := srv.DispenserSlotMeta(dim, dx, dy, dz, 0).Damage; dmg != 1 {
		t.Fatalf("shears damage after beehive shear = %d, want 1", dmg)
	}

	cache.SetBlock(fx, fy, fz, beehive5)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:glass_bottle", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected glass_bottle on beehive[honey_level=5] to succeed")
	}
	if got := srv.DispenserSlots(dx, dy, dz)[0].ResolvedID(); got != "minecraft:honey_bottle" {
		t.Fatalf("slot[0] after glass_bottle on beehive = %q, want minecraft:honey_bottle", got)
	}

	// 5. Shears on adult sheep in front -> marks sheep sheared and damages shears.
	cache.SetBlock(fx, fy, fz, world.StateAir)
	sheepID, err := srv.SpawnMobInDimension(dim, float64(fx)+0.5, float64(fy), float64(fz)+0.5, MobTypeSheep)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(sheep): %v", err)
	}
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:shears", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected shears on sheep to succeed")
	}
	if !srv.IsSheepSheared(sheepID) {
		t.Fatal("expected sheep to be marked sheared")
	}
	srv.entities.Remove(sheepID)

	// 6. Flint and Steel lights fire in air and loses 1 durability.
	stone, _ := world.BlockStateByName("minecraft:stone", nil)
	cache.SetBlock(fx, fy-1, fz, stone)
	cache.SetBlock(fx, fy, fz, world.StateAir)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:flint_and_steel", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected flint_and_steel to light fire")
	}
	if got := world.BlockName(cache.GetBlock(fx, fy, fz)); got != "minecraft:fire" {
		t.Fatalf("front block after flint_and_steel = %q, want minecraft:fire", got)
	}
	if dmg := srv.DispenserSlotMeta(dim, dx, dy, dz, 0).Damage; dmg != 1 {
		t.Fatalf("flint_and_steel damage = %d, want 1", dmg)
	}

	// 7. Primed TNT -> spawns primed tnt entity and explodes after 80 ticks.
	cache.SetBlock(fx, fy, fz, world.StateAir)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:tnt", 1))
	if !srv.DispenseFromDispenser(dim, dx, dy, dz) {
		t.Fatal("expected tnt dispense to succeed")
	}
	for i := 0; i < DefaultPrimedTNTFuseTicks-1; i++ {
		if exploded := srv.TickPrimedTNT(); exploded != 0 {
			t.Fatalf("primed TNT exploded early at tick %d", i+1)
		}
	}
	if exploded := srv.TickPrimedTNT(); exploded != 1 {
		t.Fatalf("expected 1 primed TNT explosion on tick 80, got %d", exploded)
	}
}

func TestDropperEjectAndContainerTransfer(t *testing.T) {
	srv := newDispenserTestServer(t)
	dim := world.DimensionOverworld
	dx, dy, dz := 140, 70, 140
	cache := placeDispenserOrDropperForTest(t, srv, dim, dx, dy, dz, "minecraft:dropper", "east")
	fx, fy, fz := dx+1, dy, dz

	// 1. With air in front, dropper ejects TNT as an item (never primes it!) and emits LevelEvent 1000 + 2000.
	viewer, collect := addDispenserTestPlayer(t, srv, "DropperViewer")
	srv.SetPlayerTransform(viewer, float64(dx), float64(dy), float64(dz), 0, 0, true)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:tnt", 2))
	_ = collect()

	if !srv.DispenseFromDropper(dim, dx, dy, dz) {
		t.Fatal("expected dropper eject into air to succeed")
	}
	if got := srv.DispenserSlots(dx, dy, dz)[0].Count; got != 1 {
		t.Fatalf("dropper slot[0].Count after eject = %d, want 1", got)
	}
	var saw1000, saw2000 bool
	for _, pkt := range collect() {
		if pkt.ID == PlayLevelEventCB {
			r := protocol.NewReader(pkt.Payload)
			evType, err := r.Int32()
			if err == nil {
				if evType == LevelEventSoundDispense {
					saw1000 = true
				}
				if evType == LevelEventParticlesShootSmoke {
					saw2000 = true
				}
			}
		}
	}
	if !saw1000 || !saw2000 {
		t.Fatalf("expected LevelEvent 1000 and 2000 on dropper air eject, got 1000=%v 2000=%v", saw1000, saw2000)
	}

	// 2. Place a chest in front at (fx, fy, fz) -> dropper transfers the remaining TNT into the chest!
	chestSt, _ := world.BlockStateByName("minecraft:chest", map[string]string{"facing": "north", "type": "single"})
	cache.SetBlock(fx, fy, fz, chestSt)
	if !srv.DispenseFromDropper(dim, dx, dy, dz) {
		t.Fatal("expected dropper transfer into chest to succeed")
	}
	if !srv.DispenserSlots(dx, dy, dz)[0].IsEmpty() {
		t.Fatal("expected dropper slot[0] to be empty after transfer into chest")
	}
	if got := srv.ChestSlots(fx, fy, fz)[0]; got.ResolvedID() != "minecraft:tnt" || got.Count != 1 {
		t.Fatalf("chest slot[0] after dropper transfer = %+v, want 1x minecraft:tnt", got)
	}

	// 3. Place a furnace in front (side face) -> coal transfers into fuel slot (slot 1), raw_iron does not!
	furnaceSt, _ := world.BlockStateByName("minecraft:furnace", map[string]string{"facing": "north", "lit": "false"})
	cache.SetBlock(fx, fy, fz, furnaceSt)
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:coal", 1))
	if !srv.DispenseFromDropper(dim, dx, dy, dz) {
		t.Fatal("expected dropper side transfer of coal into furnace fuel slot to succeed")
	}
	if got := srv.FurnaceSlots(fx, fy, fz)[world.FurnaceSlotFuel]; got.ResolvedID() != "minecraft:coal" || got.Count != 1 {
		t.Fatalf("furnace fuel slot = %+v, want 1x minecraft:coal", got)
	}

	// 4. Dropper facing down into top of furnace -> transfers raw_iron into input slot (slot 0)!
	placeDispenserOrDropperForTest(t, srv, dim, fx, fy+1, fz, "minecraft:dropper", "down")
	srv.SetDispenserSlot(fx, fy+1, fz, 0, world.NewNamedItemStack("minecraft:raw_iron", 1))
	if !srv.DispenseFromDropper(dim, fx, fy+1, fz) {
		t.Fatal("expected top dropper transfer of raw_iron into furnace input slot to succeed")
	}
	if got := srv.FurnaceSlots(fx, fy, fz)[world.FurnaceSlotInput]; got.ResolvedID() != "minecraft:raw_iron" || got.Count != 1 {
		t.Fatalf("furnace input slot = %+v, want 1x minecraft:raw_iron", got)
	}

	// 5. Dropper facing another dispenser -> transfers item into target dispenser!
	placeDispenserOrDropperForTest(t, srv, dim, fx, fy, fz, "minecraft:dispenser", "east")
	srv.SetDispenserSlot(dx, dy, dz, 0, world.NewNamedItemStack("minecraft:arrow", 3))
	if !srv.DispenseFromDropper(dim, dx, dy, dz) {
		t.Fatal("expected dropper transfer into dispenser to succeed")
	}
	if got := srv.DispenserSlots(fx, fy, fz)[0]; got.ResolvedID() != "minecraft:arrow" || got.Count != 1 {
		t.Fatalf("target dispenser slot[0] = %+v, want 1x minecraft:arrow", got)
	}
}

func TestItemFrameAndDispenserComparatorSignalsAndPersistence(t *testing.T) {
	srv := newDispenserTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.BindNoteBlockAndJukeboxRedstone(dim)

	// 1. Item Frame comparator hook (ItemFrameComparatorSignalAt & ItemFrameAnalogOutput)
	// Place a solid stone support block at (10, 70, 10), an item frame on its south face at (10, 70, 11),
	// and a comparator at (10, 70, 9) with facing="south" (rear input at (10, 70, 10)).
	stone, _ := world.BlockStateByName("minecraft:stone", nil)
	cache.SetBlock(10, 70, 9, stone)
	cache.SetBlock(10, 69, 9, stone)
	cache.SetBlock(10, 70, 10, stone)
	compSt, ok := world.BlockStateByName("minecraft:comparator", map[string]string{
		"facing":  "south",
		"mode":    "compare",
		"powered": "false",
	})
	if !ok {
		t.Fatal("missing minecraft:comparator state")
	}
	cache.SetBlock(10, 70, 9, compSt)

	frameID, err := srv.SpawnItemFrame(dim, 10, 70, 11, DirectionSouth, false)
	if err != nil {
		t.Fatalf("SpawnItemFrame: %v", err)
	}
	if sig := srv.ItemFrameAnalogOutput(frameID); sig != 0 {
		t.Fatalf("empty ItemFrameAnalogOutput = %d, want 0", sig)
	}
	if sig := cache.ComparatorOutputSignalAt(10, 70, 9); sig != 0 {
		t.Fatalf("comparator output for empty item frame = %d, want 0", sig)
	}

	// Put an item into the frame and rotate it through rotations 0..7 -> signals 1..8!
	p, _ := addDispenserTestPlayer(t, srv, "FrameRotator")
	srv.SetPlayerTransform(p, 10.5, 70, 11.5, 0, 0, true)
	p.SetInventorySlot(36, world.NewNamedItemStack("minecraft:diamond_sword", 1))
	if !srv.InteractWithItemFrame(p, frameID, 0) {
		t.Fatal("InteractWithItemFrame (insert) returned false")
	}
	srv.RefreshItemFrameComparators(frameID)
	if sig := srv.ItemFrameAnalogOutput(frameID); sig != 1 {
		t.Fatalf("ItemFrameAnalogOutput at rotation 0 = %d, want 1", sig)
	}
	if sig := cache.ComparatorOutputSignalAt(10, 70, 9); sig != 1 {
		t.Fatalf("ComparatorOutputSignalAt through stone block at rotation 0 = %d, want 1", sig)
	}

	for rot := 1; rot < 8; rot++ {
		if !srv.InteractWithItemFrame(p, frameID, 0) {
			t.Fatalf("InteractWithItemFrame (rotate %d) returned false", rot)
		}
		srv.RefreshItemFrameComparators(frameID)
		wantSig := rot + 1
		if sig := srv.ItemFrameComparatorSignalAt(dim, 10, 70, 10); sig != wantSig {
			t.Fatalf("ItemFrameComparatorSignalAt at rotation %d = %d, want %d", rot, sig, wantSig)
		}
		if sig := cache.ComparatorOutputSignalAt(10, 70, 9); sig != wantSig {
			t.Fatalf("ComparatorOutputSignalAt at rotation %d = %d, want %d", rot, sig, wantSig)
		}
	}

	// 2. Dispenser comparator signal & NBT persistence.
	dx, dy, dz := 30, 70, 30
	placeDispenserOrDropperForTest(t, srv, dim, dx, dy, dz, "minecraft:dispenser", "north")
	if sig := srv.ComparatorSignalAt(dim, dx, dy, dz); sig != 0 {
		t.Fatalf("empty dispenser comparator signal = %d, want 0", sig)
	}
	// 1 non-stackable item in 1 of 9 slots -> floor(1/9 * 14) + 1 = 2.
	srv.SetDispenserSlotWithPotion(dim, dx, dy, dz, 0, world.NewNamedItemStack("minecraft:splash_potion", 1), "minecraft:healing")
	if sig := srv.ComparatorSignalAt(dim, dx, dy, dz); sig != 2 {
		t.Fatalf("1-slot full dispenser comparator signal = %d, want 2", sig)
	}
	// Fill all 9 slots -> 15.
	for i := 0; i < DispenserSlotCount; i++ {
		srv.SetDispenserSlot(dx, dy, dz, i, world.NewNamedItemStack("minecraft:cobblestone", 64))
	}
	if sig := srv.ComparatorSignalAt(dim, dx, dy, dz); sig != 15 {
		t.Fatalf("full dispenser comparator signal = %d, want 15", sig)
	}

	// Save and reload via NBT.
	srv.SetDispenserSlotWithPotion(dim, dx, dy, dz, 0, world.NewNamedItemStack("minecraft:tipped_arrow", 16), "minecraft:swiftness")
	nbtRoot := srv.SaveDispensersToNBT()

	srv2 := newDispenserTestServer(t)
	placeDispenserOrDropperForTest(t, srv2, dim, dx, dy, dz, "minecraft:dispenser", "north")
	if loaded := srv2.LoadDispensersFromNBT(nbtRoot); loaded != 1 {
		t.Fatalf("LoadDispensersFromNBT loaded = %d, want 1", loaded)
	}
	if got := srv2.DispenserSlots(dx, dy, dz)[0]; got.ResolvedID() != "minecraft:tipped_arrow" || got.Count != 16 {
		t.Fatalf("reloaded slot[0] = %+v, want 16x tipped_arrow", got)
	}
	if pot := srv2.DispenserSlotPotion(dim, dx, dy, dz, 0); pot != "minecraft:swiftness" {
		t.Fatalf("reloaded slot[0] potion = %q, want minecraft:swiftness", pot)
	}
}
