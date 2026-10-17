package server

import (
	"math"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestVehicleMountDismountAndSetPassengersPacket(t *testing.T) {
	srv := newSurvivalTestServer(t)

	var sentIDs []int32
	var lastPassengersPkt []byte
	p1, err := srv.RegisterPlayer(Profile{UUID: [16]byte{1, 1}, Name: "driver"}, func(id int32, body []byte) error {
		sentIDs = append(sentIDs, id)
		if id == protocol.PlaySetPassengers {
			lastPassengersPkt = append([]byte(nil), body...)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer p1: %v", err)
	}
	p2, err := srv.RegisterPlayer(Profile{UUID: [16]byte{1, 2}, Name: "rider2"}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer p2: %v", err)
	}
	p3, err := srv.RegisterPlayer(Profile{UUID: [16]byte{1, 3}, Name: "rider3"}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer p3: %v", err)
	}
	_ = srv.SetPlayerGameMode(p1, GameModeSurvival)
	_ = srv.SetPlayerGameMode(p2, GameModeSurvival)
	_ = srv.SetPlayerGameMode(p3, GameModeSurvival)
	srv.SetPlayerTransform(p1, 10.5, 65.0, 10.5, 0, 0, true)
	srv.SetPlayerTransform(p2, 10.5, 65.0, 10.5, 0, 0, true)
	srv.SetPlayerTransform(p3, 10.5, 65.0, 10.5, 0, 0, true)

	// 1. Standard boat accepts up to 2 passengers; 3rd passenger is rejected.
	boatID, boatEnt := srv.SpawnVehicle(world.DimensionOverworld, "minecraft:oak_boat", 10.5, 65.0, 10.5, 90.0)
	if boatID == 0 || boatEnt == nil {
		t.Fatalf("SpawnVehicle oak_boat: id=%d ent=%v", boatID, boatEnt)
	}
	handled, _, err := srv.InteractWithVehicle(p1, boatID)
	if err != nil || !handled {
		t.Fatalf("InteractWithVehicle p1: handled=%v err=%v", handled, err)
	}
	if got := srv.PlayerMountedVehicle(p1); got != boatID {
		t.Fatalf("PlayerMountedVehicle(p1) = %d, want %d", got, boatID)
	}
	if driver := srv.VehicleDriverEntityID(boatID); driver != p1.EntityID {
		t.Fatalf("VehicleDriverEntityID = %d, want %d", driver, p1.EntityID)
	}
	if len(lastPassengersPkt) == 0 {
		t.Fatal("expected PlaySetPassengers packet on mount")
	}
	decVeh, decPass, err := DecodeVehiclePassengersPacket(protocol.NewReader(lastPassengersPkt))
	if err != nil {
		t.Fatalf("DecodeVehiclePassengersPacket: %v", err)
	}
	if decVeh != boatID || len(decPass) != 1 || decPass[0] != p1.EntityID {
		t.Fatalf("decoded passengers = (%d, %v), want (%d, [%d])", decVeh, decPass, boatID, p1.EntityID)
	}

	if _, ok := srv.MountPlayerOnVehicle(p2, boatID); !ok {
		t.Fatal("expected 2nd passenger to mount oak_boat")
	}
	if _, ok := srv.MountPlayerOnVehicle(p3, boatID); ok {
		t.Fatal("expected 3rd passenger to be rejected on 2-seat oak_boat")
	}
	if got := srv.VehiclePassengers(boatID); len(got) != 2 || got[0] != p1.EntityID || got[1] != p2.EntityID {
		t.Fatalf("VehiclePassengers = %v, want [%d %d]", got, p1.EntityID, p2.EntityID)
	}

	// 2. Sneaking via PlayPlayerInputSB (0x2b) dismounts p1 and promotes p2 to driver.
	lastPassengersPkt = nil
	inputBody := EncodePlayerInputPacket(PlayerInput{Shift: true})
	if err := srv.HandlePlayerInputPacket(p1, protocol.NewReader(inputBody)); err != nil {
		t.Fatalf("HandlePlayerInputPacket(shift): %v", err)
	}
	if got := srv.PlayerMountedVehicle(p1); got != 0 {
		t.Fatalf("PlayerMountedVehicle(p1) after sneak dismount = %d, want 0", got)
	}
	if driver := srv.VehicleDriverEntityID(boatID); driver != p2.EntityID {
		t.Fatalf("VehicleDriverEntityID after p1 dismount = %d, want %d", driver, p2.EntityID)
	}
	decVeh, decPass, err = DecodeVehiclePassengersPacket(protocol.NewReader(lastPassengersPkt))
	if err != nil {
		t.Fatalf("DecodeVehiclePassengersPacket after dismount: %v", err)
	}
	if decVeh != boatID || len(decPass) != 1 || decPass[0] != p2.EntityID {
		t.Fatalf("decoded passengers after dismount = (%d, %v), want (%d, [%d])", decVeh, decPass, boatID, p2.EntityID)
	}
}

func TestVehiclePacketsMoveVehicleAndPaddleBoat(t *testing.T) {
	srv := newSurvivalTestServer(t)

	var observerMovePkt []byte
	var observerMetaPkt []byte
	driver, err := srv.RegisterPlayer(Profile{UUID: [16]byte{2, 1}, Name: "boat_driver"}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer driver: %v", err)
	}
	observer, err := srv.RegisterPlayer(Profile{UUID: [16]byte{2, 2}, Name: "boat_observer"}, func(id int32, body []byte) error {
		switch id {
		case protocol.PlayMoveVehicle:
			observerMovePkt = append([]byte(nil), body...)
		case protocol.PlaySetEntityData:
			observerMetaPkt = append([]byte(nil), body...)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer observer: %v", err)
	}
	srv.SetPlayerTransform(driver, 20.5, 65.0, 20.5, 0, 0, true)
	srv.SetPlayerTransform(observer, 22.5, 65.0, 20.5, 0, 0, true)

	boatID, _ := srv.SpawnVehicle(world.DimensionOverworld, "minecraft:spruce_boat", 20.5, 65.0, 20.5, 0)
	if boatID == 0 {
		t.Fatal("SpawnVehicle returned 0")
	}
	if _, ok := srv.MountPlayerOnVehicle(driver, boatID); !ok {
		t.Fatal("MountPlayerOnVehicle failed")
	}

	// 1. Serverbound Move Vehicle (0x22) updates vehicle position, driver position, and broadcasts PlayMoveVehicle (0x39).
	movePayload := EncodeMoveVehiclePacket(21.25, 65.0, 22.75, 45.0, 0.0)
	if err := srv.HandleMoveVehiclePacket(driver, protocol.NewReader(movePayload)); err != nil {
		t.Fatalf("HandleMoveVehiclePacket: %v", err)
	}
	ent := srv.entities.Get(boatID)
	if ent == nil {
		t.Fatal("boat entity missing")
	}
	if math.Abs(ent.X-21.25) > 1e-4 || math.Abs(ent.Z-22.75) > 1e-4 || math.Abs(float64(ent.Yaw-45.0)) > 1e-3 {
		t.Fatalf("boat transform = (%v, %v, %v, yaw=%v), want (21.25, 65.0, 22.75, 45.0)", ent.X, ent.Y, ent.Z, ent.Yaw)
	}
	if len(observerMovePkt) == 0 {
		t.Fatal("expected PlayMoveVehicle broadcast to observer")
	}
	mx, my, mz, myaw, mpitch, _, err := DecodeMoveVehiclePacket(protocol.NewReader(observerMovePkt))
	if err != nil {
		t.Fatalf("DecodeMoveVehiclePacket: %v", err)
	}
	if math.Abs(mx-21.25) > 1e-4 || math.Abs(my-65.0) > 1e-4 || math.Abs(mz-22.75) > 1e-4 || myaw != 45.0 || mpitch != 0.0 {
		t.Fatalf("decoded MoveVehicle = (%v, %v, %v, %v, %v)", mx, my, mz, myaw, mpitch)
	}

	// 2. Serverbound Paddle Boat (0x23) updates paddle booleans and broadcasts entity metadata.
	observerMetaPkt = nil
	paddlePayload := EncodePaddleBoatPacket(true, false)
	if err := srv.HandlePaddleBoatPacket(driver, protocol.NewReader(paddlePayload)); err != nil {
		t.Fatalf("HandlePaddleBoatPacket: %v", err)
	}
	st, ok := srv.VehicleStateByID(boatID)
	if !ok || !st.LeftPaddle || st.RightPaddle {
		t.Fatalf("BoatPaddles = (%v, %v), want (true, false)", st.LeftPaddle, st.RightPaddle)
	}
	if len(observerMetaPkt) == 0 {
		t.Fatal("expected PlaySetEntityData broadcast for boat paddles")
	}
}

func TestMinecartRailPhysicsPoweredDetectorAndActivatorRails(t *testing.T) {
	srv := newSurvivalTestServer(t)
	cache := srv.Chunks()

	// Build a straight east-west rail track at Y=65, Z=30, X=10..15.
	ewRail, ok := world.BlockStateByName("minecraft:rail", map[string]string{
		"shape":       "east_west",
		"waterlogged": "false",
	})
	if !ok {
		t.Fatal("missing east_west rail state")
	}
	poweredRailOn, ok := world.BlockStateByName("minecraft:powered_rail", map[string]string{
		"shape":       "east_west",
		"powered":     "true",
		"waterlogged": "false",
	})
	if !ok {
		t.Fatal("missing powered_rail powered=true state")
	}
	poweredRailOff, ok := world.BlockStateByName("minecraft:powered_rail", map[string]string{
		"shape":       "east_west",
		"powered":     "false",
		"waterlogged": "false",
	})
	if !ok {
		t.Fatal("missing powered_rail powered=false state")
	}
	detectorRailOff, ok := world.BlockStateByName("minecraft:detector_rail", map[string]string{
		"shape":       "east_west",
		"powered":     "false",
		"waterlogged": "false",
	})
	if !ok {
		t.Fatal("missing detector_rail state")
	}
	activatorRailOn, ok := world.BlockStateByName("minecraft:activator_rail", map[string]string{
		"shape":       "east_west",
		"powered":     "true",
		"waterlogged": "false",
	})
	if !ok {
		t.Fatal("missing activator_rail powered=true state")
	}

	// Place solid backing block at X=9, powered rail (on) at X=10, detector rail at X=11,
	// regular rail at X=12, activator rail (on) at X=13, powered rail (off) at X=14.
	stoneState, _ := world.BlockStateByName("minecraft:stone", nil)
	for x := 9; x <= 15; x++ {
		cache.SetBlock(x, 64, 30, stoneState)
	}
	cache.SetBlock(9, 65, 30, stoneState)
	cache.SetBlock(10, 65, 30, poweredRailOn)
	cache.SetBlock(11, 65, 30, detectorRailOff)
	cache.SetBlock(12, 65, 30, ewRail)
	cache.SetBlock(13, 65, 30, activatorRailOn)
	cache.SetBlock(14, 65, 30, poweredRailOff)

	var detectorEvents []bool
	srv.RegisterDetectorRailHook(func(s *Server, dim string, x, y, z int, powered bool) {
		if x == 11 && y == 65 && z == 30 {
			detectorEvents = append(detectorEvents, powered)
		}
	})

	rider, err := srv.RegisterPlayer(Profile{UUID: [16]byte{3, 1}, Name: "rail_rider"}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerTransform(rider, 10.5, 65.0, 30.5, 0, 0, true)

	// Spawn a stationary minecart on the powered rail at X=10 with a solid block at X=9:
	// it should launch east (+X), roll across the detector rail at X=11 (turning it powered=true
	// with comparator output 15 because a rider is mounted), and then get dismounted at the
	// powered activator rail at X=13!
	cartID, _ := srv.SpawnVehicle(world.DimensionOverworld, EntityTypeMinecart, 10.5, 65.0625, 30.5, 0)
	if cartID == 0 {
		t.Fatal("SpawnVehicle minecart returned 0")
	}
	if _, ok := srv.MountPlayerOnVehicle(rider, cartID); !ok {
		t.Fatal("failed to mount rider on minecart")
	}

	sawDetectorPowered := false
	sawComparator15 := false
	for tick := 0; tick < 25; tick++ {
		srv.TickVehicles()
		st := cache.GetBlock(11, 65, 30)
		if _, props, ok := world.BlockStateProperties(st); ok && props["powered"] == "true" {
			sawDetectorPowered = true
			if srv.DetectorRailComparatorOutput(world.DimensionOverworld, 11, 65, 30) == 15 {
				sawComparator15 = true
			}
		}
	}
	if !sawDetectorPowered {
		t.Fatal("expected detector rail at (11,65,30) to power on when minecart passed over it")
	}
	if !sawComparator15 {
		t.Fatal("expected DetectorRailComparatorOutput == 15 while occupied minecart was on detector rail")
	}
	if len(detectorEvents) == 0 {
		t.Fatal("expected DetectorRailHook to be invoked")
	}
	if got := srv.PlayerMountedVehicle(rider); got != 0 {
		t.Fatalf("expected powered activator rail at X=13 to dismount rider, still mounted on %d", got)
	}

	// Verify unpowered powered_rail at X=14 brakes a moving minecart to a stop.
	brakeCartID, _ := srv.SpawnVehicle(world.DimensionOverworld, EntityTypeMinecart, 14.2, 65.0625, 30.5, 0)
	if brakeCartID == 0 {
		t.Fatal("SpawnVehicle brakeCart returned 0")
	}
	srv.entities.Update(brakeCartID, func(e *world.Entity) {
		e.VelocityX = int16(0.004 * 8000) // after 0.5x brake -> < 0.003 threshold -> 0
	})
	srv.TickVehicles()
	brakeEnt := srv.entities.Get(brakeCartID)
	if brakeEnt == nil || brakeEnt.VelocityX != 0 {
		t.Fatalf("unpowered powered_rail VelocityX = %v, want 0", brakeEnt.VelocityX)
	}
}

func TestMinecartVariantsChestFurnaceTNTAndHopper(t *testing.T) {
	srv := newSurvivalTestServer(t)
	cache := srv.Chunks()

	player, err := srv.RegisterPlayer(Profile{UUID: [16]byte{4, 1}, Name: "variant_tester"}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerTransform(player, 40.5, 65.0, 40.5, 0, 0, true)

	ewRail, _ := world.BlockStateByName("minecraft:rail", map[string]string{
		"shape":       "east_west",
		"waterlogged": "false",
	})
	stoneState, _ := world.BlockStateByName("minecraft:stone", nil)
	for x := 38; x <= 48; x++ {
		cache.SetBlock(x, 64, 40, stoneState)
		cache.SetBlock(x, 65, 40, ewRail)
	}

	// 1. Chest Minecart: right-click opens 27-slot container menu and stores items.
	chestCartID, _ := srv.SpawnVehicle(world.DimensionOverworld, EntityTypeChestMinecart, 40.5, 65.0625, 40.5, 0)
	if chestCartID == 0 {
		t.Fatal("SpawnVehicle chest_minecart returned 0")
	}
	handled, cid, err := srv.InteractWithVehicle(player, chestCartID)
	if err != nil || !handled || cid <= 0 {
		t.Fatalf("InteractWithVehicle chest_minecart: handled=%v cid=%d err=%v", handled, cid, err)
	}
	if open := player.OpenContainer(); open == nil || open.MenuType != MenuTypeGeneric9x3 || open.VehicleEntityID != chestCartID {
		t.Fatalf("OpenContainer = %+v, want MenuTypeGeneric9x3 for vehicle %d", open, chestCartID)
	}
	player.SetCarriedItem(world.NewNamedItemStack("minecraft:diamond", 12))
	if err := srv.HandleContainerClick(player, cid, 0, 0, ContainerInputPickup); err != nil {
		t.Fatalf("HandleContainerClick chest_minecart: %v", err)
	}
	if got := srv.VehicleContainerSlot(chestCartID, 0); got.ResolvedID() != "minecraft:diamond" || got.Count != 12 {
		t.Fatalf("chest_minecart slot 0 = %+v, want 12x minecraft:diamond", got)
	}
	_ = srv.CloseContainer(player, cid, false)

	// 2. Furnace Minecart: fueling with coal adds 3600 fuel ticks and propels the minecart along the rail.
	furnaceCartID, _ := srv.SpawnVehicle(world.DimensionOverworld, EntityTypeFurnaceMinecart, 42.5, 65.0625, 40.5, 0)
	if furnaceCartID == 0 {
		t.Fatal("SpawnVehicle furnace_minecart returned 0")
	}
	srv.SetPlayerTransform(player, 41.5, 65.0, 40.5, -90, 0, true) // facing +X
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:coal", 2))
	player.SetSelectedSlot(0)
	if handled, _, err := srv.InteractWithVehicle(player, furnaceCartID); err != nil || !handled {
		t.Fatalf("InteractWithVehicle furnace_minecart: handled=%v err=%v", handled, err)
	}
	if st, ok := srv.VehicleStateByID(furnaceCartID); !ok || st.Fuel != FurnaceMinecartFuelPerCoal {
		t.Fatalf("FurnaceMinecartFuel = %d, want %d", st.Fuel, FurnaceMinecartFuelPerCoal)
	}
	beforeEnt := srv.entities.Get(furnaceCartID)
	srv.TickVehicles()
	afterEnt := srv.entities.Get(furnaceCartID)
	if afterEnt == nil || beforeEnt == nil || afterEnt.X <= beforeEnt.X {
		t.Fatalf("expected fueled furnace_minecart to move +X along rail: before=%v after=%v", beforeEnt, afterEnt)
	}

	// 3. Hopper Minecart: opens 5-slot hopper UI, picks up dropped items on rail when enabled,
	// and stops picking up when disabled by a powered activator rail.
	hopperCartID, _ := srv.SpawnVehicle(world.DimensionOverworld, EntityTypeHopperMinecart, 44.5, 65.0625, 40.5, 0)
	if hopperCartID == 0 {
		t.Fatal("SpawnVehicle hopper_minecart returned 0")
	}
	var hookCalls int
	srv.RegisterHopperMinecartHook(func(s *Server, entityID int32, dim string, x, y, z float64) bool {
		if entityID == hopperCartID {
			hookCalls++
		}
		return false
	})
	itemEntID, _ := srv.SpawnItemEntityInDimension(world.DimensionOverworld, 44.5, 65.2, 40.5, world.NewNamedItemStack("minecraft:gold_ingot", 5))
	if itemEntID == 0 {
		t.Fatal("SpawnItemEntityInDimension failed")
	}
	srv.TickVehicles()
	if hookCalls == 0 {
		t.Fatal("expected HopperMinecartHook to be called on TickVehicles")
	}
	if got := srv.VehicleContainerSlot(hopperCartID, 0); got.ResolvedID() != "minecraft:gold_ingot" || got.Count != 5 {
		t.Fatalf("hopper_minecart slot 0 after pickup = %+v, want 5x minecraft:gold_ingot", got)
	}
	srv.SetHopperMinecartEnabled(hopperCartID, false)
	if srv.IsHopperMinecartEnabled(hopperCartID) {
		t.Fatal("expected hopper_minecart to be disabled")
	}

	// 4. TNT Minecart: priming ticks down fuse and detonates.
	tntCartID, _ := srv.SpawnVehicle(world.DimensionOverworld, EntityTypeTNTMinecart, 46.5, 65.0625, 40.5, 0)
	if tntCartID == 0 {
		t.Fatal("SpawnVehicle tnt_minecart returned 0")
	}
	if !srv.PrimeTNTMinecart(tntCartID, 2) {
		t.Fatal("PrimeTNTMinecart failed")
	}
	if st, ok := srv.VehicleStateByID(tntCartID); !ok || st.FuseTicks != 2 {
		t.Fatalf("TNTMinecartFuse = %d, want 2", st.FuseTicks)
	}
	srv.TickVehicles()
	srv.TickVehicles()
	if srv.entities.Get(tntCartID) != nil {
		t.Fatal("expected primed tnt_minecart to detonate and be removed after 2 ticks")
	}
}

func TestBoatAndChestBoatWaterIcePhysicsAndLilyPadBreak(t *testing.T) {
	srv := newSurvivalTestServer(t)
	cache := srv.Chunks()

	// Verify friction coefficients for water, ice, blue ice, and land.
	if got := BoatFrictionForMedium(BoatMediumInWater); math.Abs(got-BoatFrictionWater) > 1e-6 {
		t.Fatalf("BoatFrictionForMedium(InWater) = %v, want %v", got, BoatFrictionWater)
	}
	if got := BoatFrictionForMedium(BoatMediumOnIce); math.Abs(got-BoatFrictionIce) > 1e-6 {
		t.Fatalf("BoatFrictionForMedium(OnIce) = %v, want %v", got, BoatFrictionIce)
	}
	if got := BoatFrictionForMedium(BoatMediumOnBlueIce); math.Abs(got-BoatFrictionBlueIce) > 1e-6 {
		t.Fatalf("BoatFrictionForMedium(OnBlueIce) = %v, want %v", got, BoatFrictionBlueIce)
	}
	if got := BoatFrictionForMedium(BoatMediumOnLand); math.Abs(got-BoatFrictionLand) > 1e-6 {
		t.Fatalf("BoatFrictionForMedium(OnLand) = %v, want %v", got, BoatFrictionLand)
	}

	// Fill water at Y=64, Z=60, X=60..64 and place a lily_pad at (61, 65, 60).
	lilyPadState, ok := world.BlockStateByName("minecraft:lily_pad", nil)
	if !ok {
		t.Fatal("missing lily_pad block state")
	}
	for x := 60; x <= 64; x++ {
		cache.SetBlock(x, 64, 60, world.StateWater)
		cache.SetBlock(x, 65, 60, world.StateAir)
	}
	cache.SetBlock(61, 65, 60, lilyPadState)

	if med := srv.ClassifyBoatMedium(world.DimensionOverworld, 60.5, 64.5, 60.5); med != BoatMediumInWater {
		t.Fatalf("ClassifyBoatMedium in water = %v, want BoatMediumInWater", med)
	}
	blueIceState, _ := world.BlockStateByName("minecraft:blue_ice", nil)
	cache.SetBlock(65, 64, 60, blueIceState)
	if med := srv.ClassifyBoatMedium(world.DimensionOverworld, 65.5, 65.0, 60.5); med != BoatMediumOnBlueIce {
		t.Fatalf("ClassifyBoatMedium on blue_ice = %v, want BoatMediumOnBlueIce", med)
	}

	// Spawn an oak_chest_boat moving east (+X) from X=60.5 into the lily pad at X=61.
	boatID, _ := srv.SpawnVehicle(world.DimensionOverworld, "minecraft:oak_chest_boat", 60.5, 64.5, 60.5, -90)
	if boatID == 0 {
		t.Fatal("SpawnVehicle oak_chest_boat returned 0")
	}
	srv.entities.Update(boatID, func(e *world.Entity) {
		e.VelocityX = int16(0.6 * 8000)
	})

	srv.TickVehicles()
	if got := cache.GetBlock(61, 65, 60); got != world.StateAir {
		t.Fatalf("expected moving boat to break lily_pad at (61,65,60), block state = %d", got)
	}

	// Sneaking + right-clicking a chest boat opens its 27-slot container menu instead of mounting.
	player, err := srv.RegisterPlayer(Profile{UUID: [16]byte{5, 1}, Name: "chest_boat_user"}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerTransform(player, 61.0, 65.0, 60.5, 0, 0, true)
	player.SetSneaking(true)
	handled, cid, err := srv.InteractWithVehicle(player, boatID)
	if err != nil || !handled || cid <= 0 {
		t.Fatalf("InteractWithVehicle sneaking chest_boat: handled=%v cid=%d err=%v", handled, cid, err)
	}
	if srv.PlayerMountedVehicle(player) != 0 {
		t.Fatal("expected sneaking player not to mount chest_boat")
	}
	if open := player.OpenContainer(); open == nil || open.VehicleEntityID != boatID {
		t.Fatalf("OpenContainer = %+v, want chest_boat container", open)
	}
}

func TestVehicleDamageDestructionDropsAndNBTPersistence(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.cfg.WorldDir = t.TempDir()

	attacker, err := srv.RegisterPlayer(Profile{UUID: [16]byte{6, 1}, Name: "vehicle_attacker"}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(attacker, GameModeSurvival)
	srv.SetPlayerTransform(attacker, 70.5, 65.0, 70.5, 0, 0, true)

	// 1. Spawn a chest_minecart with items, save to NBT + disk, reload into a fresh server, and verify state.
	cartID, _ := srv.SpawnVehicle(world.DimensionOverworld, EntityTypeChestMinecart, 70.5, 65.0625, 70.5, 90)
	if cartID == 0 {
		t.Fatal("SpawnVehicle chest_minecart returned 0")
	}
	srv.SetVehicleContainerSlot(cartID, 3, world.NewNamedItemStack("minecraft:emerald", 7))

	comp, ok := srv.WriteVehicleToNBT(cartID)
	if !ok || comp == nil {
		t.Fatal("WriteVehicleToNBT failed")
	}
	if err := srv.SaveVehiclesFile(); err != nil {
		t.Fatalf("SaveVehiclesFile: %v", err)
	}

	// Reload on a second server sharing WorldDir.
	srv2 := newSurvivalTestServer(t)
	srv2.cfg.WorldDir = srv.cfg.WorldDir
	if err := srv2.LoadVehiclesFile(); err != nil {
		t.Fatalf("LoadVehiclesFile: %v", err)
	}
	var restoredID int32
	for _, e := range srv2.entities.All() {
		if e.TypeName == EntityTypeChestMinecart {
			restoredID = e.ID
			break
		}
	}
	if restoredID == 0 {
		t.Fatal("expected chest_minecart to be restored from vehicles.dat")
	}
	if got := srv2.VehicleContainerSlot(restoredID, 3); got.ResolvedID() != "minecraft:emerald" || got.Count != 7 {
		t.Fatalf("restored chest_minecart slot 3 = %+v, want 7x minecraft:emerald", got)
	}

	// 2. Damaging the vehicle beyond 4.0 health destroys it and drops chest_minecart + emeralds.
	if destroyed := srv.DamageVehicle(cartID, 2.0, attacker); destroyed {
		t.Fatal("expected 2.0 damage not to destroy vehicle (max health 4.0)")
	}
	if destroyed := srv.DamageVehicle(cartID, 3.0, attacker); !destroyed {
		t.Fatal("expected cumulative 5.0 damage to destroy vehicle")
	}
	if srv.entities.Get(cartID) != nil {
		t.Fatal("expected destroyed vehicle to be removed from EntityManager")
	}

	var sawChestMinecart, sawEmerald bool
	for _, e := range srv.entities.All() {
		if !e.IsItem() {
			continue
		}
		switch e.Item.ResolvedID() {
		case "minecraft:chest_minecart":
			sawChestMinecart = true
		case "minecraft:emerald":
			if e.Item.Count == 7 {
				sawEmerald = true
			}
		}
	}
	if !sawChestMinecart || !sawEmerald {
		t.Fatalf("expected chest_minecart (%v) and 7x emerald (%v) drops on destruction", sawChestMinecart, sawEmerald)
	}
}

func TestEquineInventoryScreenAndRidingHooks(t *testing.T) {
	srv, sess := newTestPlayServer(t)

	horseID, err := srv.SpawnMob(1.5, 65, 1.5, MobTypeHorse)
	if err != nil {
		t.Fatalf("SpawnMob(horse): %v", err)
	}
	srv.TameMob(horseID, sess)

	// Right-click tamed horse while not sneaking with empty hand mounts the player.
	handled, err := srv.InteractWithEquineEntity(sess, horseID, false)
	if err != nil || !handled || srv.PlayerMountedEquine(sess) != horseID {
		t.Fatalf("expected non-sneak empty-hand interact to mount horse: handled=%v err=%v mounted=%d", handled, err, srv.PlayerMountedEquine(sess))
	}

	// Sneaking with empty hand on tamed horse opens ClientboundHorseScreenOpenPacket (MenuTypeHorse).
	srv.DismountPlayerVehicle(sess)
	if srv.PlayerMountedEquine(sess) != 0 {
		t.Fatalf("expected DismountPlayerVehicle to dismount equine")
	}
	handled, err = srv.InteractWithEquineEntity(sess, horseID, true)
	if err != nil || !handled {
		t.Fatalf("expected sneak empty-hand interact on tamed horse to open inventory: handled=%v err=%v", handled, err)
	}
	open := sess.OpenContainer()
	if open == nil || open.MenuType != MenuTypeHorse || open.VehicleEntityID != horseID {
		t.Fatalf("expected open MenuTypeHorse for horse %d, got %+v", horseID, open)
	}

	// Shift-click saddle and diamond_horse_armor from player hotbar into slots 0 and 1.
	sess.SetInventorySlot(36, world.NewNamedItemStack("minecraft:saddle", 1))
	sess.SetInventorySlot(37, world.NewNamedItemStack("minecraft:diamond_horse_armor", 1))
	if err := srv.HandleContainerClick(sess, open.ContainerID, 2+27+0, 0, ContainerInputQuickMove); err != nil {
		t.Fatalf("HandleContainerClick(saddle): %v", err)
	}
	if !srv.IsEquineSaddled(horseID) {
		t.Fatalf("expected quick-move saddle to equip horse saddle")
	}
	if err := srv.HandleContainerClick(sess, open.ContainerID, 2+27+1, 0, ContainerInputQuickMove); err != nil {
		t.Fatalf("HandleContainerClick(armor): %v", err)
	}
	if srv.HorseArmorItem(horseID) != "minecraft:diamond_horse_armor" {
		t.Fatalf("expected quick-move armor to equip diamond_horse_armor, got %q", srv.HorseArmorItem(horseID))
	}
}

