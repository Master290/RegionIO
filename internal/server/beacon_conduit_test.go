package server

import (
	"testing"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

func mustTestBlockState(name string) (uint16, bool) {
	return world.BlockStateIDByProperties(name, nil)
}

func setTestPlayerPos(sess *PlayerSession, x, y, z float64) {
	sess.mu.Lock()
	sess.position = [3]float64{x, y, z}
	sess.mu.Unlock()
}

func buildTestBeaconPyramid(cache *world.Cache, bx, by, bz, maxLayer int, blockNames ...string) {
	if len(blockNames) == 0 {
		blockNames = []string{"minecraft:iron_block"}
	}
	beaconState, ok := mustTestBlockState("minecraft:beacon")
	if !ok {
		panic("missing minecraft:beacon state")
	}
	cache.SetBlock(bx, by, bz, beaconState)
	idx := 0
	for layer := 1; layer <= maxLayer; layer++ {
		py := by - layer
		for x := bx - layer; x <= bx+layer; x++ {
			for z := bz - layer; z <= bz+layer; z++ {
				bName := blockNames[idx%len(blockNames)]
				idx++
				st, ok := mustTestBlockState(bName)
				if !ok {
					panic("missing base block state: " + bName)
				}
				cache.SetBlock(x, py, z, st)
			}
		}
	}
}

func TestBeaconPyramidLevelsAndSkyBeam(t *testing.T) {
	srv, _ := newTestEnchantServer(t)
	cache := srv.DimensionChunks(world.DimensionOverworld)
	bx, by, bz := 16, 80, 16

	// Clear column above (bx, by, bz) up to 319.
	for y := by; y <= BeaconDefaultMaxBuild; y++ {
		cache.SetBlock(bx, y, bz, world.StateAir)
	}

	// Test levels 1..4 with all 5 base block types.
	baseTypes := []string{
		"minecraft:iron_block",
		"minecraft:gold_block",
		"minecraft:emerald_block",
		"minecraft:diamond_block",
		"minecraft:netherite_block",
	}
	for wantLevel := 1; wantLevel <= 4; wantLevel++ {
		buildTestBeaconPyramid(cache, bx, by, bz, wantLevel, baseTypes...)
		got := CheckBeaconPyramidLevel(cache.GetBlock, bx, by, bz)
		if got != wantLevel {
			t.Fatalf("CheckBeaconPyramidLevel(layer=%d) = %d, want %d", wantLevel, got, wantLevel)
		}
	}

	// Verify stained glass beam passability and color blending.
	redGlass, ok := mustTestBlockState("minecraft:red_stained_glass")
	if !ok {
		t.Fatal("missing minecraft:red_stained_glass")
	}
	bluePane, ok := mustTestBlockState("minecraft:blue_stained_glass_pane")
	if !ok {
		t.Fatal("missing minecraft:blue_stained_glass_pane")
	}
	bedrock, ok := mustTestBlockState("minecraft:bedrock")
	if !ok {
		t.Fatal("missing minecraft:bedrock")
	}
	cache.SetBlock(bx, by+1, bz, redGlass)
	cache.SetBlock(bx, by+5, bz, bluePane)
	cache.SetBlock(bx, by+10, bz, bedrock)

	clear, sections := CheckBeaconSkyAndBeam(cache.GetBlock, bx, by, bz, BeaconDefaultMaxBuild)
	if !clear || len(sections) < 2 {
		t.Fatalf("expected clear beam with >= 2 colored sections, got clear=%v len=%d", clear, len(sections))
	}

	// Opaque stone block above beacon blocks the beam and deactivates the beacon.
	stone, _ := mustTestBlockState("minecraft:stone")
	cache.SetBlock(bx, by+20, bz, stone)
	clearBlocked, sectionsBlocked := CheckBeaconSkyAndBeam(cache.GetBlock, bx, by, bz, BeaconDefaultMaxBuild)
	if clearBlocked || len(sectionsBlocked) != 0 {
		t.Fatalf("expected stone to block beacon beam, got clear=%v len=%d", clearBlocked, len(sectionsBlocked))
	}
	bBlocked, _ := srv.UpdateBeaconAt(bx, by, bz, nil)
	if bBlocked.Levels != 0 || bBlocked.IsActive() {
		t.Fatalf("expected blocked sky to yield Levels=0 and IsActive=false, got Levels=%d IsActive=%v", bBlocked.Levels, bBlocked.IsActive())
	}

	// Removing the stone restores Level 4 activation.
	cache.SetBlock(bx, by+20, bz, world.StateAir)
	bActive, _ := srv.UpdateBeaconAt(bx, by, bz, nil)
	if bActive.Levels != 4 || !bActive.IsActive() {
		t.Fatalf("expected unblocked 4-layer pyramid to yield Levels=4 and IsActive=true, got Levels=%d IsActive=%v", bActive.Levels, bActive.IsActive())
	}
}

func TestBeaconMenuPaymentSetBeaconAndEffects(t *testing.T) {
	srv, _ := newTestEnchantServer(t)
	cache := srv.DimensionChunks(world.DimensionOverworld)
	bx, by, bz := 32, 75, 32
	for y := by; y <= BeaconDefaultMaxBuild; y++ {
		cache.SetBlock(bx, y, bz, world.StateAir)
	}
	buildTestBeaconPyramid(cache, bx, by, bz, 4, "minecraft:diamond_block")

	var sentPackets []int32
	sess, err := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("BeaconTester"),
		Name: "BeaconTester",
	}, func(id int32, body []byte) error {
		sentPackets = append(sentPackets, id)
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	setTestPlayerPos(sess, float64(bx)+0.5, float64(by)+1.0, float64(bz)+0.5)

	// Open the beacon container UI; activating at level 4 should also grant
	// both construct_beacon (create_beacon) and create_full_beacon advancements.
	cid, err := srv.OpenBeacon(sess, bx, by, bz)
	if err != nil || cid <= 0 {
		t.Fatalf("OpenBeacon = (%d, %v)", cid, err)
	}
	if !sess.HasCompletedAdvancement("minecraft:nether/create_beacon") {
		t.Fatal("expected Bring Home the Beacon (minecraft:nether/create_beacon) to be completed")
	}
	if !sess.HasCompletedAdvancement("minecraft:nether/create_full_beacon") {
		t.Fatal("expected Beaconator (minecraft:nether/create_full_beacon) to be completed at level 4")
	}

	// Give player 2 iron ingots in hotbar slot 36 (menu slot 28) and quick-move 1 into payment slot 0.
	sess.SetInventorySlot(36, world.NewNamedItemStack("minecraft:iron_ingot", 2))
	if err := srv.HandleBeaconClick(sess, ContainerClick{
		ContainerID: cid,
		Slot:        28,
		Button:      0,
		Mode:        ContainerInputQuickMove,
	}); err != nil {
		t.Fatalf("HandleBeaconClick quick-move payment: %v", err)
	}
	if got := sess.InventorySlot(36).Count; got != 1 {
		t.Fatalf("expected 1 iron_ingot remaining in hotbar after quick-moving 1 to beacon payment slot, got %d", got)
	}

	// Round-trip encode/decode ServerboundSetBeaconPacket for Strength + Regeneration.
	rawPkt := EncodeSetBeaconPacket(EffectStrength, EffectRegeneration)
	primID, secID, err := DecodeSetBeaconPacket(protocol.NewReader(rawPkt))
	if err != nil || primID != EffectStrength || secID != EffectRegeneration {
		t.Fatalf("DecodeSetBeaconPacket = (%q, %q, %v), want (%q, %q, nil)",
			primID, secID, err, EffectStrength, EffectRegeneration)
	}

	if err := srv.HandleSetBeaconPacket(sess, primID, secID); err != nil {
		t.Fatalf("HandleSetBeaconPacket: %v", err)
	}

	// Payment slot should now be empty (consumed 1 iron_ingot), and player should
	// have Strength I and Regeneration I with duration 340 ticks ((9 + 4*2)*20) and Ambient=true.
	bState, ok := srv.BeaconAt(bx, by, bz)
	if !ok || bState.PrimaryEffect != EffectStrength || bState.SecondaryEffect != EffectRegeneration {
		t.Fatalf("BeaconAt after SetBeaconPacket = %+v, ok=%v", bState, ok)
	}
	strInst, hasStr := sess.PlayerEffect(EffectStrength)
	regInst, hasReg := sess.PlayerEffect(EffectRegeneration)
	if !hasStr || strInst.Amplifier != 0 || strInst.Duration != 340 || !strInst.Ambient {
		t.Fatalf("expected Strength I (duration=340, ambient=true), got has=%v inst=%+v", hasStr, strInst)
	}
	if !hasReg || regInst.Amplifier != 0 || regInst.Duration != 340 || !regInst.Ambient {
		t.Fatalf("expected Regeneration I (duration=340, ambient=true), got has=%v inst=%+v", hasReg, regInst)
	}

	// Test upgrading primary to Level II (SecondaryEffect == PrimaryEffect = Speed) and 80-tick pulse.
	if err := srv.HandleBeaconClick(sess, ContainerClick{
		ContainerID: cid,
		Slot:        28,
		Button:      0,
		Mode:        ContainerInputQuickMove,
	}); err != nil {
		t.Fatalf("HandleBeaconClick second payment: %v", err)
	}
	if err := srv.HandleSetBeaconPacket(sess, EffectSpeed, EffectSpeed); err != nil {
		t.Fatalf("HandleSetBeaconPacket Speed II: %v", err)
	}
	srv.ClearPlayerEffects(sess)
	for i := 0; i < BeaconTickInterval; i++ {
		srv.TickBeacons()
	}
	spdInst, hasSpd := sess.PlayerEffect(EffectSpeed)
	if !hasSpd || spdInst.Amplifier != 1 || spdInst.Duration != 340 {
		t.Fatalf("expected Speed II (amplifier=1, duration=340) after 80-tick pulse, got has=%v inst=%+v", hasSpd, spdInst)
	}
}

func TestBeaconNBTRoundTripAndPersistence(t *testing.T) {
	srv, _ := newTestEnchantServer(t)
	orig := BeaconBlockEntity{
		ID:              "minecraft:beacon",
		Dimension:       world.DimensionOverworld,
		X:               -12,
		Y:               70,
		Z:               44,
		Levels:          3,
		PrimaryEffect:   EffectHaste,
		SecondaryEffect: "",
		CustomName:      "Mine Haste Beacon",
	}
	nbtComp := orig.ToNBT()
	decoded, ok := BeaconFromNBT(nbtComp)
	if !ok || decoded == nil {
		t.Fatal("BeaconFromNBT returned false")
	}
	if decoded.X != orig.X || decoded.Y != orig.Y || decoded.Z != orig.Z ||
		decoded.Levels != orig.Levels || decoded.PrimaryEffect != orig.PrimaryEffect ||
		decoded.CustomName != orig.CustomName {
		t.Fatalf("decoded beacon = %+v, want %+v", *decoded, orig)
	}

	srv.SetBeaconState(orig)
	if err := srv.SaveBeacons(); err != nil {
		t.Fatalf("SaveBeacons: %v", err)
	}
	chunkNBT := srv.ChunkBeaconBlockEntitiesNBT(world.DimensionOverworld, int32(orig.X>>4), int32(orig.Z>>4))
	if len(chunkNBT) != 1 {
		t.Fatalf("ChunkBeaconBlockEntitiesNBT len = %d, want 1", len(chunkNBT))
	}
	srv.RemoveBeacon(orig.X, orig.Y, orig.Z)
	srv.LoadChunkBeaconBlockEntitiesNBT(world.DimensionOverworld, nbt.List{
		ElemID: nbt.TagCompound,
		Elems:  []nbt.Tag{chunkNBT[0]},
	})
	loaded, ok := srv.BeaconAt(orig.X, orig.Y, orig.Z)
	if !ok || loaded.PrimaryEffect != EffectHaste || loaded.CustomName != "Mine Haste Beacon" {
		t.Fatalf("loaded beacon from chunk NBT = %+v, ok=%v", loaded, ok)
	}
}

func buildTestConduitSetup(cache *world.Cache, cx, cy, cz, frameBlocks int) {
	waterState := world.StateWater
	for dx := -1; dx <= 1; dx++ {
		for dy := -1; dy <= 1; dy++ {
			for dz := -1; dz <= 1; dz++ {
				cache.SetBlock(cx+dx, cy+dy, cz+dz, waterState)
			}
		}
	}
	conduitState, ok := mustTestBlockState("minecraft:conduit")
	if !ok {
		panic("missing minecraft:conduit state")
	}
	cache.SetBlock(cx, cy, cz, conduitState)

	frameNames := []string{
		"minecraft:prismarine",
		"minecraft:dark_prismarine",
		"minecraft:prismarine_bricks",
		"minecraft:sea_lantern",
	}
	positions := ConduitFramePositions(cx, cy, cz)
	for i, pos := range positions {
		if i < frameBlocks {
			st, ok := mustTestBlockState(frameNames[i%len(frameNames)])
			if !ok {
				panic("missing conduit frame state")
			}
			cache.SetBlock(pos[0], pos[1], pos[2], st)
		} else {
			cache.SetBlock(pos[0], pos[1], pos[2], world.StateAir)
		}
	}
}

func TestConduitFrameActivationEffectRadiusAndMobAttack(t *testing.T) {
	srv, _ := newTestEnchantServer(t)
	cache := srv.DimensionChunks(world.DimensionOverworld)
	cx, cy, cz := 64, 60, 64

	if got := len(ConduitFramePositions(cx, cy, cz)); got != ConduitFullFrameBlocks {
		t.Fatalf("len(ConduitFramePositions) = %d, want %d", got, ConduitFullFrameBlocks)
	}

	// 15 blocks -> inactive (< 16 minimum).
	buildTestConduitSetup(cache, cx, cy, cz, 15)
	c15, _ := srv.UpdateConduitAt(cx, cy, cz)
	if c15.IsActive() || c15.FrameCount != 15 || c15.EffectRadius() != 0 {
		t.Fatalf("expected 15-block conduit to be inactive, got %+v", c15)
	}

	// 16 blocks -> active with radius 32 ((16/7)*16).
	buildTestConduitSetup(cache, cx, cy, cz, 16)
	c16, _ := srv.UpdateConduitAt(cx, cy, cz)
	if !c16.IsActive() || c16.IsHunting() || c16.EffectRadius() != 32 {
		t.Fatalf("expected 16-block conduit active with radius 32, got %+v (radius=%v)", c16, c16.EffectRadius())
	}

	// Full 42-block frame -> active and hunting with radius 96 ((42/7)*16).
	buildTestConduitSetup(cache, cx, cy, cz, 42)
	c42, _ := srv.UpdateConduitAt(cx, cy, cz)
	if !c42.IsActive() || !c42.IsHunting() || c42.EffectRadius() != 96 {
		t.Fatalf("expected 42-block conduit active+hunting with radius 96, got %+v (radius=%v)", c42, c42.EffectRadius())
	}

	// Register a player in the water next to the conduit and a hostile zombie in water 3 blocks away.
	sess, err := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("ConduitSwimmer"),
		Name: "ConduitSwimmer",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	setTestPlayerPos(sess, float64(cx)+1.5, float64(cy)+0.5, float64(cz)+0.5)

	// Place water at (cx+4, cy, cz) and spawn a hostile pillager there.
	cache.SetBlock(cx+4, cy, cz, world.StateWater)
	mobID, err := srv.SpawnRaiderMob(float64(cx)+4.5, float64(cy), float64(cz)+0.5, MobTypePillager, false, 0, 0)
	if err != nil {
		t.Fatalf("SpawnRaiderMob: %v", err)
	}
	srv.SetMobHealth(mobID, 6.0)

	// Advance 40 ticks -> should grant Conduit Power (260 ticks) to player and deal 4.0 damage to mob.
	for i := 0; i < ConduitTickInterval; i++ {
		srv.TickConduits()
	}
	cpInst, hasCP := sess.PlayerEffect(EffectConduitPower)
	if !hasCP || cpInst.Duration != ConduitEffectDurationTicks || !cpInst.Ambient {
		t.Fatalf("expected Conduit Power (duration=%d, ambient=true), got has=%v inst=%+v",
			ConduitEffectDurationTicks, hasCP, cpInst)
	}
	if hp := srv.MobHealth(mobID); hp != 2.0 {
		t.Fatalf("expected mob health to drop from 6.0 to 2.0 after 1 conduit attack, got %v", hp)
	}

	// Next 40-tick pulse finishes off the mob (2.0 - 4.0 <= 0) and despawns it.
	for i := 0; i < ConduitTickInterval; i++ {
		srv.TickConduits()
	}
	if srv.entities.Get(mobID) != nil {
		t.Fatal("expected mob to be despawned after lethal conduit attack")
	}

	// Test Conduit NBT round-trip and chunk NBT persistence.
	stateAfter, ok := srv.ConduitAt(cx, cy, cz)
	if !ok {
		t.Fatal("expected ConduitAt to return true")
	}
	decoded, ok := ConduitFromNBT(stateAfter.ToNBT())
	if !ok || decoded == nil || !decoded.IsActive() || !decoded.IsHunting() || decoded.FrameCount != 42 {
		t.Fatalf("ConduitFromNBT = %+v, ok=%v", decoded, ok)
	}
}
