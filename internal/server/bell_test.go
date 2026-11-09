package server

import (
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestServerBellHandProjectileAndRedstoneRing(t *testing.T) {
	s := newDispenserTestServer(t)

	cache := s.BindBellRedstone(world.DimensionOverworld)
	cache.SetBlock(10, 63, 10, world.StateStone)
	cache.SetBlock(10, 64, 10, world.BellBlockState(world.BellAttachFloor, "north", false))

	var hookCalls int
	var lastHookDir string
	var lastRinger int32
	s.SetBellRungHook(func(dim string, x, y, z int, direction string, ringerEntityID int32) {
		hookCalls++
		lastHookDir = direction
		lastRinger = ringerEntityID
	})

	// 1. Hand interaction from invalid direction (east on a north-facing floor bell) fails.
	if s.InteractBell(nil, world.DimensionOverworld, 10, 64, 10, "east", 0.5) {
		t.Fatalf("expected InteractBell from east on north floor bell to fail")
	}
	if hookCalls != 0 {
		t.Fatalf("expected 0 hook calls, got %d", hookCalls)
	}

	// 2. Hand interaction from valid direction (south) succeeds and triggers hook.
	if !s.InteractBell(nil, world.DimensionOverworld, 10, 64, 10, "south", 0.5) {
		t.Fatalf("expected InteractBell from south on north floor bell to succeed")
	}
	if hookCalls != 1 || lastHookDir != "south" {
		t.Fatalf("expected hookCalls=1 dir=south, got calls=%d dir=%q", hookCalls, lastHookDir)
	}

	// 3. Projectile hit rings the bell and passes projectileEntityID.
	if !s.HitBellWithProjectile(world.DimensionOverworld, 10, 64, 10, "north", 777) {
		t.Fatalf("expected HitBellWithProjectile from north to succeed")
	}
	if hookCalls != 2 || lastHookDir != "north" || lastRinger != 777 {
		t.Fatalf("expected hookCalls=2 dir=north ringer=777, got calls=%d dir=%q ringer=%d", hookCalls, lastHookDir, lastRinger)
	}

	// 4. Redstone rising edge triggers RingBellAt via BindBellRedstone listener.
	redstoneBlock, _ := world.BlockStateIDByProperties("minecraft:redstone_block", nil)
	cache.ApplyBlockWithUpdates(11, 64, 10, redstoneBlock)
	if hookCalls != 3 || lastHookDir != "north" || lastRinger != 0 {
		t.Fatalf("expected redstone rising edge to trigger BellRungHook (calls=3, dir=north, ringer=0), got calls=%d dir=%q ringer=%d", hookCalls, lastHookDir, lastRinger)
	}
}

func TestServerBellBlockEntityShakingResonatingRaiderGlowAndNBT(t *testing.T) {
	s := newDispenserTestServer(t)
	dim := world.DimensionOverworld
	cache := s.DimensionChunks(dim)
	cache.SetBlock(10, 63, 10, world.StateStone)
	cache.SetBlockWithLight(10, 64, 10, world.BellBlockState(world.BellAttachFloor, "north", false))

	// Spawn two raiders: one within 32 blocks (triggers resonation) and one at 40 blocks (within 48-block glow radius).
	nearRaider, err := s.SpawnMobInDimension(dim, 20.5, 64.0, 10.5, "minecraft:pillager")
	if err != nil {
		t.Fatalf("SpawnMobInDimension nearRaider: %v", err)
	}
	farRaider, err := s.SpawnMobInDimension(dim, 50.5, 64.0, 10.5, "minecraft:vindicator")
	if err != nil {
		t.Fatalf("SpawnMobInDimension farRaider: %v", err)
	}
	tooFarRaider, err := s.SpawnMobInDimension(dim, 65.5, 64.0, 10.5, "minecraft:pillager")
	if err != nil {
		t.Fatalf("SpawnMobInDimension tooFarRaider: %v", err)
	}

	if !s.RingBellAt(dim, 10, 64, 10, "north", 0, nil) {
		t.Fatalf("expected RingBellAt to succeed")
	}
	be, ok := s.GetBellBlockEntityAt(dim, 10, 64, 10)
	if !ok || !be.Shaking || be.Ticks != 0 || be.ClickDirection != "north" {
		t.Fatalf("unexpected BellBlockEntity after ring: ok=%v be=%+v", ok, be)
	}

	// Tick 5 times: Resonating should become true because nearRaider is within 32 blocks.
	for i := 0; i < 5; i++ {
		s.TickBells(dim)
	}
	be, _ = s.GetBellBlockEntityAt(dim, 10, 64, 10)
	if !be.Shaking || be.Ticks != 5 || !be.Resonating {
		t.Fatalf("expected Shaking=true Ticks=5 Resonating=true after 5 ticks, got %+v", be)
	}

	// Tick 40 more times: resonation completes on the 40th resonation tick and applies Glowing (60 ticks) to raiders within 48 blocks.
	for i := 0; i < 40; i++ {
		s.TickBells(dim)
	}
	be, _ = s.GetBellBlockEntityAt(dim, 10, 64, 10)
	if be.Resonating {
		t.Fatalf("expected Resonating=false after 40 resonation ticks, got %+v", be)
	}
	if !s.HasEntityEffect(nearRaider, EffectGlowing) {
		t.Fatalf("expected nearRaider (dist 10) to receive Glowing effect")
	}
	if !s.HasEntityEffect(farRaider, EffectGlowing) {
		t.Fatalf("expected farRaider (dist 40 <= 48) to receive Glowing effect")
	}
	if s.HasEntityEffect(tooFarRaider, EffectGlowing) {
		t.Fatalf("expected tooFarRaider (dist 55 > 48) NOT to receive Glowing effect")
	}

	// Tick remaining 5 ticks to reach 50 total shaking ticks.
	for i := 0; i < 5; i++ {
		s.TickBells(dim)
	}
	be, _ = s.GetBellBlockEntityAt(dim, 10, 64, 10)
	if be.Shaking || be.Ticks != 0 {
		t.Fatalf("expected Shaking=false Ticks=0 after 50 total ticks, got %+v", be)
	}

	// Verify chunk block-entity NBT persistence.
	nbts := s.ChunkBellBlockEntitiesNBT(dim, 0, 0)
	foundBellNBT := false
	for _, c := range nbts {
		id, _ := nbtGetString(c, "id")
		if id == "minecraft:bell" {
			foundBellNBT = true
			break
		}
	}
	if !foundBellNBT {
		t.Fatalf("expected ChunkBellBlockEntitiesNBT(0, 0) to include minecraft:bell")
	}
	s.UnloadChunkBells(dim, 0, 0)
	if _, ok := s.GetBellBlockEntityAt(dim, 10, 64, 10); ok {
		t.Fatalf("expected UnloadChunkBells to remove bell from runtime map")
	}
	s.LoadChunkBellBlockEntitiesNBT(dim, nbts)
	if _, ok := s.GetBellBlockEntityAt(dim, 10, 64, 10); !ok {
		t.Fatalf("expected LoadChunkBellBlockEntitiesNBT to restore bell block entity")
	}
}

func TestServerRespawnAnchorChargeDispenserRespawnAndWaterExplosion(t *testing.T) {
	s := newDispenserTestServer(t)

	// 1. Charging in the Nether via player and via dispenser, plus comparator signal and respawn depletion.
	netherCache := s.DimensionChunks(world.DimensionNether)
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			netherCache.SetBlock(dx, 63, dz, world.StateStone)
			netherCache.SetBlock(dx, 64, dz, world.StateAir)
			netherCache.SetBlock(dx, 65, dz, world.StateAir)
			netherCache.SetBlock(dx, 66, dz, world.StateAir)
		}
	}
	netherCache.SetBlock(0, 64, 0, world.RespawnAnchorBlockState(0))

	if _, ok := s.ChargeRespawnAnchor(nil, world.DimensionNether, 0, 64, 0); !ok {
		t.Fatalf("expected ChargeRespawnAnchor to succeed from 0 -> 1")
	}
	if charges := RespawnAnchorCharges(netherCache.GetBlock(0, 64, 0)); charges != 1 {
		t.Fatalf("expected charges=1, got %d", charges)
	}
	if sig := RespawnAnchorScaledChargeLevel(netherCache.GetBlock(0, 64, 0), 15); sig != 3 {
		t.Fatalf("expected scaled comparator/light level 3 for charges=1, got %d", sig)
	}

	// Dispense glowstone from a dispenser at (0, 65, 0) facing down into the respawn anchor at (0, 64, 0).
	dispDown, ok := world.BlockStateByName("minecraft:dispenser", map[string]string{
		"facing":    "down",
		"triggered": "false",
	})
	if !ok {
		t.Fatalf("missing dispenser facing down state")
	}
	netherCache.SetBlock(0, 65, 0, dispDown)
	glowstoneStack := world.NewNamedItemStack("minecraft:glowstone", 4)
	s.SetDispenserSlotInDimension(world.DimensionNether, 0, 65, 0, 0, glowstoneStack)

	for wantCharges := 2; wantCharges <= 4; wantCharges++ {
		if !s.DispenseFromDispenser(world.DimensionNether, 0, 65, 0) {
			t.Fatalf("expected DispenseFromDispenser to charge respawn anchor to %d", wantCharges)
		}
		if charges := RespawnAnchorCharges(netherCache.GetBlock(0, 64, 0)); charges != wantCharges {
			t.Fatalf("expected charges=%d after dispense, got %d", wantCharges, charges)
		}
	}
	// When already at 4 charges, dispensing glowstone fails without consuming the remaining glowstone.
	if s.DispenseFromDispenser(world.DimensionNether, 0, 65, 0) {
		t.Fatalf("expected DispenseFromDispenser to fail when respawn anchor is already at 4 charges")
	}
	if rem := s.DispenserSlotsInDimension(world.DimensionNether, 0, 65, 0)[0]; rem.Count != 1 {
		t.Fatalf("expected 1 glowstone remaining in dispenser, got %d", rem.Count)
	}

	// Interact in the Nether sets player respawn point; RespawnPlayer consumes 1 charge (4 -> 3).
	session, err := s.RegisterPlayer(Profile{Name: "AnchorTester", UUID: OfflineUUID("AnchorTester")}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = s.SetPlayerGameMode(session, GameModeSurvival)
	session.SetDimension(world.DimensionNether)
	s.SetPlayerTransform(session, 0.5, 64.0, 2.5, 0, 0, true)

	if !s.InteractRespawnAnchor(session, 0, 64, 0) {
		t.Fatalf("expected InteractRespawnAnchor in Nether to succeed")
	}
	s.DamagePlayer(session, 100, "generic")
	if err := s.RespawnPlayer(session); err != nil {
		t.Fatalf("RespawnPlayer: %v", err)
	}
	if session.Dimension() != world.DimensionNether {
		t.Fatalf("expected RespawnPlayer to respawn in Nether, got %s", session.Dimension())
	}
	if charges := RespawnAnchorCharges(netherCache.GetBlock(0, 64, 0)); charges != 3 {
		t.Fatalf("expected respawn to consume 1 charge (4 -> 3), got %d", charges)
	}

	// 2. Outside the Nether (Overworld): dry explosion destroys surrounding dirt, whereas water-damped explosion destroys 0 surrounding blocks.
	owCache := s.DimensionChunks(world.DimensionOverworld)
	session.SetDimension(world.DimensionOverworld)

	// Dry anchor at (40, 64, 40) surrounded by dirt.
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			owCache.SetBlock(40+dx, 63, 40+dz, world.StateDirt)
			if dx != 0 || dz != 0 {
				owCache.SetBlock(40+dx, 64, 40+dz, world.StateDirt)
			}
		}
	}
	owCache.SetBlock(40, 64, 40, world.RespawnAnchorBlockState(1))
	if !s.InteractRespawnAnchor(session, 40, 64, 40) {
		t.Fatalf("expected dry Overworld respawn anchor interaction to explode")
	}
	if owCache.GetBlock(41, 64, 40) == world.StateDirt {
		t.Fatalf("expected dry respawn anchor explosion to destroy adjacent dirt at (41, 64, 40)")
	}

	// Water-adjacent anchor at (80, 64, 80) with water source at (81, 64, 80) and dirt at (79, 64, 80).
	owCache.SetBlock(80, 63, 80, world.StateStone)
	owCache.SetBlock(80, 64, 80, world.RespawnAnchorBlockState(1))
	owCache.SetBlock(81, 64, 80, world.StateWater)
	owCache.SetBlock(79, 64, 80, world.StateDirt)
	if !s.InteractRespawnAnchor(session, 80, 64, 80) {
		t.Fatalf("expected wet Overworld respawn anchor interaction to explode")
	}
	if owCache.GetBlock(80, 64, 80) != world.StateAir {
		t.Fatalf("expected wet respawn anchor block itself to be removed")
	}
	if owCache.GetBlock(79, 64, 80) != world.StateDirt {
		t.Fatalf("expected water-damped respawn anchor explosion NOT to destroy adjacent dirt at (79, 64, 80)")
	}
}

func TestServerLodestoneCompassBindSplitComponentCodecAndBreak(t *testing.T) {
	s := newDispenserTestServer(t)
	dim := world.DimensionOverworld
	cache := s.DimensionChunks(dim)
	cache.SetBlock(25, 64, 35, world.LodestoneBlockState())

	session, err := s.RegisterPlayer(Profile{Name: "LodestoneTester", UUID: OfflineUUID("LodestoneTester")}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = s.SetPlayerGameMode(session, GameModeSurvival)
	session.SetDimension(dim)
	s.SetPlayerTransform(session, 25.5, 64.0, 36.5, 0, 0, true)

	// 1. Survival with Count == 1 replaces the held slot in place.
	hotbar0 := world.HotbarContainerOffset
	session.SetInventorySlot(hotbar0, world.NewNamedItemStack(CompassItemID, 1))
	if !s.UseCompassOnLodestone(session, 0, 25, 64, 35) {
		t.Fatalf("expected UseCompassOnLodestone with 1 compass to succeed")
	}
	lt, ok := GetSlotLodestoneTracker(session, hotbar0)
	if !ok || !lt.HasTarget || lt.Dimension != dim || lt.X != 25 || lt.Y != 64 || lt.Z != 35 || !lt.Tracked {
		t.Fatalf("unexpected LodestoneTracker on slot %d: ok=%v lt=%+v", hotbar0, ok, lt)
	}

	// 2. Survival with Count == 3 decrements the held stack to 2 and puts a 1-count lodestone compass into the next slot.
	hotbar1 := world.HotbarContainerOffset + 1
	session.SetInventorySlot(hotbar0, world.NewNamedItemStack(CompassItemID, 3))
	ClearSlotLodestoneTracker(session, hotbar0)
	session.SetInventorySlot(hotbar1, world.ItemStack{})
	ClearSlotLodestoneTracker(session, hotbar1)

	if !s.UseCompassOnLodestone(session, 0, 25, 64, 35) {
		t.Fatalf("expected UseCompassOnLodestone with 3 compasses to succeed")
	}
	if got := session.InventorySlot(hotbar0).Count; got != 2 {
		t.Fatalf("expected held compass stack to decrement from 3 to 2, got %d", got)
	}
	if _, hasLT := GetSlotLodestoneTracker(session, hotbar0); hasLT {
		t.Fatalf("expected original unstacked compasses on hotbar0 not to have LodestoneTracker")
	}
	if got := session.InventorySlot(hotbar1); !IsCompassItem(got) || got.Count != 1 {
		t.Fatalf("expected 1 lodestone compass in hotbar1, got %+v", got)
	}
	lt1, ok := GetSlotLodestoneTracker(session, hotbar1)
	if !ok || !lt1.HasTarget || lt1.X != 25 || lt1.Y != 64 || lt1.Z != 35 {
		t.Fatalf("expected hotbar1 to hold bound LodestoneTracker, got ok=%v lt=%+v", ok, lt1)
	}

	// 3. Wire component encoding/decoding round-trip (DataComponentLodestoneTracker = 67).
	w := protocol.NewWriter(32)
	EncodeSlotWithLodestoneTracker(w, session.InventorySlot(hotbar1), &lt1)
	r := protocol.NewReader(w.Bytes())
	decStack, decLT, err := DecodeSlotWithLodestoneTracker(r)
	if err != nil {
		t.Fatalf("DecodeSlotWithLodestoneTracker failed: %v", err)
	}
	if !IsCompassItem(decStack) || decStack.Count != 1 || decLT == nil || *decLT != lt1 {
		t.Fatalf("wire round-trip mismatch: stack=%+v lt=%+v, want %+v", decStack, decLT, lt1)
	}

	// 4. Put an untracked compass in hotbar2, then break the lodestone block:
	//    hotbar1 (tracked=true) should lose its target, while hotbar2 (tracked=false) keeps its target.
	hotbar2 := world.HotbarContainerOffset + 2
	session.SetInventorySlot(hotbar2, world.NewNamedItemStack(CompassItemID, 1))
	SetSlotLodestoneTracker(session, hotbar2, NewLodestoneTracker(dim, 25, 64, 35, false))

	cache.SetBlock(25, 64, 35, world.StateAir)
	s.OnLodestoneBlockBroken(dim, 25, 64, 35)

	afterTracked, ok := GetSlotLodestoneTracker(session, hotbar1)
	if !ok || afterTracked.HasTarget {
		t.Fatalf("expected tracked compass on hotbar1 to clear HasTarget when lodestone is broken, got ok=%v lt=%+v", ok, afterTracked)
	}
	afterUntracked, ok := GetSlotLodestoneTracker(session, hotbar2)
	if !ok || !afterUntracked.HasTarget {
		t.Fatalf("expected untracked compass on hotbar2 to keep HasTarget when lodestone is broken, got ok=%v lt=%+v", ok, afterUntracked)
	}
}
