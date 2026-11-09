package server

import (
	"testing"

	"regionio/internal/world"
)

func TestDispenserSpawnEggArmorStandTridentAndShearsMooshroomCopperGolem(t *testing.T) {
	srv := newDispenserTestServer(t)
	dim := world.DimensionOverworld

	// 1. Spawn egg (`minecraft:pig_spawn_egg` and downward offset).
	if mob, ok := MobTypeForSpawnEgg("minecraft:pig_spawn_egg"); !ok || mob != "minecraft:pig" {
		t.Fatalf("MobTypeForSpawnEgg(pig_spawn_egg) = (%q, %v), want (minecraft:pig, true)", mob, ok)
	}
	placeDispenserOrDropperForTest(t, srv, dim, 10, 70, 10, "minecraft:dispenser", "south")
	srv.SetDispenserSlot(10, 70, 10, 0, world.NewNamedItemStack("minecraft:pig_spawn_egg", 2))
	if !srv.DispenseFromDispenser(dim, 10, 70, 10) {
		t.Fatal("expected dispenser with pig_spawn_egg to succeed")
	}
	if got := srv.DispenserSlots(10, 70, 10)[0].Count; got != 1 {
		t.Fatalf("pig_spawn_egg count after dispense = %d, want 1", got)
	}
	mobsInFront := srv.mobsInBlockBox(dim, 10, 70, 11)
	if len(mobsInFront) != 1 || mobsInFront[0].TypeName != "minecraft:pig" {
		t.Fatalf("expected spawned minecraft:pig at (10,70,11), got %+v", mobsInFront)
	}

	// 2. Armor stand (`minecraft:armor_stand`) facing opposite dispenser.
	placeDispenserOrDropperForTest(t, srv, dim, 20, 70, 10, "minecraft:dispenser", "south")
	srv.SetDispenserSlot(20, 70, 10, 0, world.NewNamedItemStack("minecraft:armor_stand", 1))
	if !srv.DispenseFromDispenser(dim, 20, 70, 10) {
		t.Fatal("expected dispenser with armor_stand to succeed")
	}
	if !srv.DispenserSlots(20, 70, 10)[0].IsEmpty() {
		t.Fatal("expected armor_stand slot to be empty after dispense")
	}
	standsInFront := srv.armorStandsInBlockBox(dim, 20, 70, 11)
	if len(standsInFront) != 1 || standsInFront[0].TypeName != EntityTypeArmorStand {
		t.Fatalf("expected spawned minecraft:armor_stand at (20,70,11), got %+v", standsInFront)
	}
	if standsInFront[0].Yaw != 180 {
		t.Fatalf("armor_stand yaw when dispensed south = %v, want 180 (facing north)", standsInFront[0].Yaw)
	}

	// 3. Trident (`minecraft:trident`) projectile dispense.
	placeDispenserOrDropperForTest(t, srv, dim, 30, 70, 10, "minecraft:dispenser", "east")
	srv.SetDispenserSlot(30, 70, 10, 0, world.NewNamedItemStack("minecraft:trident", 1))
	if !srv.DispenseFromDispenser(dim, 30, 70, 10) {
		t.Fatal("expected dispenser with trident to succeed")
	}
	if !srv.DispenserSlots(30, 70, 10)[0].IsEmpty() {
		t.Fatal("expected trident slot to be empty after dispense")
	}
	ds := srv.dispenserState()
	ds.mu.Lock()
	foundTrident := false
	for _, p := range ds.projectiles {
		if p != nil && p.TypeName == EntityTypeTrident && p.BaseDamage == 8.0 {
			foundTrident = true
			break
		}
	}
	ds.mu.Unlock()
	if !foundTrident {
		t.Fatal("expected active minecraft:trident projectile with BaseDamage=8.0")
	}

	// 4. Shears (`minecraft:shears`) on `minecraft:mooshroom` -> converts to `minecraft:cow` and drops 5 `red_mushroom`.
	placeDispenserOrDropperForTest(t, srv, dim, 40, 70, 10, "minecraft:dispenser", "south")
	srv.SetDispenserSlot(40, 70, 10, 0, world.NewNamedItemStack("minecraft:shears", 1))
	mooshroomID, err := srv.SpawnMobInDimension(dim, 40.5, 70.0, 11.5, MobTypeMooshroom)
	if err != nil || mooshroomID <= 0 {
		t.Fatalf("SpawnMobInDimension(mooshroom): id=%d err=%v", mooshroomID, err)
	}
	if !srv.DispenseFromDispenser(dim, 40, 70, 10) {
		t.Fatal("expected dispenser with shears on mooshroom to succeed")
	}
	afterMooshroom := srv.mobsInBlockBox(dim, 40, 70, 11)
	if len(afterMooshroom) != 1 || afterMooshroom[0].TypeName != MobTypeCow {
		t.Fatalf("expected mooshroom to convert into minecraft:cow after shearing, got %+v", afterMooshroom)
	}
	if dmg := srv.DispenserSlotMeta(dim, 40, 70, 10, 0).Damage; dmg != 1 {
		t.Fatalf("shears damage after shearing mooshroom = %d, want 1", dmg)
	}

	// 5. Shears (`minecraft:shears`) on `minecraft:copper_golem` with antenna item (`minecraft:poppy`).
	placeDispenserOrDropperForTest(t, srv, dim, 50, 70, 10, "minecraft:dispenser", "south")
	srv.SetDispenserSlot(50, 70, 10, 0, world.NewNamedItemStack("minecraft:shears", 1))
	cgID, err := srv.SpawnMobInDimension(dim, 50.5, 70.0, 11.5, MobTypeCopperGolem)
	if err != nil || cgID <= 0 {
		t.Fatalf("SpawnMobInDimension(copper_golem): id=%d err=%v", cgID, err)
	}
	srv.SetCopperGolemAntennaItem(cgID, world.NewNamedItemStack("minecraft:poppy", 1))
	if !srv.DispenseFromDispenser(dim, 50, 70, 10) {
		t.Fatal("expected dispenser with shears on copper_golem with antenna poppy to succeed")
	}
	if dmg := srv.DispenserSlotMeta(dim, 50, 70, 10, 0).Damage; dmg != 1 {
		t.Fatalf("shears damage after shearing copper_golem = %d, want 1", dmg)
	}
}
