package server

import (
	"testing"

	"regionio/internal/world"
	"regionio/internal/worldgen"
)

func newArchaeologyTestServer(t *testing.T) (*Server, *world.Cache) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.WorldDir = t.TempDir()
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	srv.BindNoteBlockAndJukeboxRedstone(world.DimensionOverworld)
	return srv, cache
}

func newArchaeologyTestPlayer(t *testing.T, srv *Server, name string, idByte byte) *PlayerSession {
	t.Helper()
	sess, err := srv.RegisterPlayer(Profile{UUID: [16]byte{9, idByte}, Name: name}, func(id int32, body []byte) error {
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer(%s): %v", name, err)
	}
	_ = srv.SetPlayerGameMode(sess, GameModeSurvival)
	return sess
}

func mustArchBlockState(t *testing.T, name string, props map[string]string) uint16 {
	t.Helper()
	st, ok := world.BlockStateIDByProperties(name, props)
	if !ok {
		t.Fatalf("missing block state %s %v", name, props)
	}
	return st
}

func TestArchaeologyBrushableBlockBrushingProgressionAndCompletion(t *testing.T) {
	srv, cache := newArchaeologyTestServer(t)
	dim := world.DimensionOverworld
	sess := newArchaeologyTestPlayer(t, srv, "archaeologist", 1)
	sess.SetInventorySlot(36, world.NewNamedItemStack(BrushItemID, 1))

	// Place solid support at (10, 63, 10) and suspicious_sand at (10, 64, 10) with a pottery sherd loot item.
	cache.SetBlock(10, 63, 10, mustArchBlockState(t, "minecraft:sandstone", nil))
	be, _ := srv.PlaceBrushableBlockAt(dim, 10, 64, 10, "minecraft:suspicious_sand", "", 0)
	if be == nil {
		t.Fatalf("expected PlaceBrushableBlockAt to return non-nil BrushableBlockEntity")
	}
	sherdStack := world.NewNamedItemStack("minecraft:prize_pottery_sherd", 1)
	if !srv.SetBrushableBlockItem(dim, 10, 64, 10, sherdStack, ItemMeta{}) {
		t.Fatalf("expected SetBrushableBlockItem to succeed")
	}

	// Stage 1: dusted 0 -> 1
	stage, done, popped, ok := srv.BrushBlockAt(sess, dim, 10, 64, 10, "up")
	if !ok || done || stage != 1 || !popped.IsEmpty() {
		t.Fatalf("brush 1: got stage=%d done=%v popped=%v ok=%v, want stage=1 done=false empty", stage, done, popped, ok)
	}
	if got, _ := world.BrushableBlockDustedStage(cache.GetBlock(10, 64, 10)); got != 1 {
		t.Fatalf("expected world block dusted=1, got %d", got)
	}

	// Stage 2: dusted 1 -> 2
	stage, done, popped, ok = srv.BrushBlockAt(sess, dim, 10, 64, 10, "up")
	if !ok || done || stage != 2 || !popped.IsEmpty() {
		t.Fatalf("brush 2: got stage=%d done=%v popped=%v ok=%v, want stage=2 done=false empty", stage, done, popped, ok)
	}
	if got, _ := world.BrushableBlockDustedStage(cache.GetBlock(10, 64, 10)); got != 2 {
		t.Fatalf("expected world block dusted=2, got %d", got)
	}

	// Stage 3: dusted 2 -> 3
	stage, done, popped, ok = srv.BrushBlockAt(sess, dim, 10, 64, 10, "up")
	if !ok || done || stage != 3 || !popped.IsEmpty() {
		t.Fatalf("brush 3: got stage=%d done=%v popped=%v ok=%v, want stage=3 done=false empty", stage, done, popped, ok)
	}
	if got, _ := world.BrushableBlockDustedStage(cache.GetBlock(10, 64, 10)); got != 3 {
		t.Fatalf("expected world block dusted=3, got %d", got)
	}

	// Stage 4: completion! Block turns into minecraft:sand, item pops out, brush loses 1 durability.
	stage, done, popped, ok = srv.BrushBlockAt(sess, dim, 10, 64, 10, "up")
	if !ok || !done || stage != 3 || popped.ResolvedID() != "minecraft:prize_pottery_sherd" {
		t.Fatalf("brush 4: got stage=%d done=%v popped=%v ok=%v, want stage=3 done=true prize_pottery_sherd", stage, done, popped, ok)
	}
	if gotName := world.BlockName(cache.GetBlock(10, 64, 10)); gotName != "minecraft:sand" {
		t.Fatalf("expected completed suspicious_sand to turn into minecraft:sand, got %q", gotName)
	}
	if _, exists := srv.GetBrushableBlockAt(dim, 10, 64, 10); exists {
		t.Fatalf("expected BrushableBlockEntity to be removed after completion")
	}
	if dmg := sess.HeldItemMeta().Damage; dmg != 1 {
		t.Fatalf("expected brush durability damage=1 after completion, got %d", dmg)
	}
	if !sess.HasCompletedAdvancement("minecraft:adventure/salvage_sherd") {
		t.Fatalf("expected minecraft:adventure/salvage_sherd advancement to be granted when brushing out a pottery sherd")
	}
}

func TestArchaeologyBrushBlockTick10StrokeProgressionAndIdleDecay(t *testing.T) {
	srv, cache := newArchaeologyTestServer(t)
	dim := world.DimensionOverworld
	sess := newArchaeologyTestPlayer(t, srv, "brusher", 2)
	sess.SetInventorySlot(36, world.NewNamedItemStack(BrushItemID, 1))

	cache.SetBlock(20, 63, 20, mustArchBlockState(t, "minecraft:stone", nil))
	srv.PlaceBrushableBlockAt(dim, 20, 64, 20, "minecraft:suspicious_gravel", world.ArchaeologyLootTrailRuinsRare, 999)

	// Brush 8 strokes (reaching dusted=3 without completing):
	for i := 1; i <= 8; i++ {
		stage, done, _, ok := srv.BrushBlockTickAt(sess, dim, 20, 64, 20, "north")
		if !ok || done {
			t.Fatalf("stroke %d: expected ok=true done=false, got ok=%v done=%v stage=%d", i, ok, done, stage)
		}
	}
	if got, _ := world.BrushableBlockDustedStage(cache.GetBlock(20, 64, 20)); got != 3 {
		t.Fatalf("expected dusted=3 after 8 brush strokes, got %d", got)
	}

	// Stop brushing and tick 39 ticks -> still in 40-tick idle cooldown (stage remains 3).
	for i := 0; i < 39; i++ {
		srv.TickBrushableBlocks()
	}
	if got, _ := world.BrushableBlockDustedStage(cache.GetBlock(20, 64, 20)); got != 3 {
		t.Fatalf("expected dusted=3 after 39 idle ticks, got %d", got)
	}

	// Tick 40: decays 3 -> 2
	srv.TickBrushableBlocks()
	if got, _ := world.BrushableBlockDustedStage(cache.GetBlock(20, 64, 20)); got != 2 {
		t.Fatalf("expected dusted=2 on tick 40, got %d", got)
	}

	// 4 ticks later: decays 2 -> 1
	for i := 0; i < 4; i++ {
		srv.TickBrushableBlocks()
	}
	if got, _ := world.BrushableBlockDustedStage(cache.GetBlock(20, 64, 20)); got != 1 {
		t.Fatalf("expected dusted=1 after 4 more ticks, got %d", got)
	}

	// 4 ticks later: decays 1 -> 0
	for i := 0; i < 4; i++ {
		srv.TickBrushableBlocks()
	}
	if got, _ := world.BrushableBlockDustedStage(cache.GetBlock(20, 64, 20)); got != 0 {
		t.Fatalf("expected dusted=0 after 4 more ticks, got %d", got)
	}

	// Now brush 10 strokes to completion -> turns into minecraft:gravel and pops rare trail ruins loot!
	var popped world.ItemStack
	for i := 1; i <= 10; i++ {
		_, done, item, ok := srv.BrushBlockTickAt(sess, dim, 20, 64, 20, "north")
		if !ok {
			t.Fatalf("stroke %d failed", i)
		}
		if i < 10 && done {
			t.Fatalf("unexpected early completion at stroke %d", i)
		}
		if i == 10 {
			if !done || item.IsEmpty() {
				t.Fatalf("expected stroke 10 to complete and pop loot item, got done=%v item=%v", done, item)
			}
			popped = item
		}
	}
	if gotName := world.BlockName(cache.GetBlock(20, 64, 20)); gotName != "minecraft:gravel" {
		t.Fatalf("expected completed suspicious_gravel to become minecraft:gravel, got %q", gotName)
	}
	if popped.IsEmpty() {
		t.Fatalf("expected non-empty popped item from trail_ruins/archaeology_rare")
	}
}

func TestArchaeologyWorldgenHydrationAndBreakDropsNothing(t *testing.T) {
	srv, cache := newArchaeologyTestServer(t)
	dim := world.DimensionOverworld

	// Simulate worldgen placing a suspicious_sand block and registering a StructureBlockEntity on the chunk.
	cache.SetBlock(30, 63, 30, mustArchBlockState(t, "minecraft:sandstone", nil))
	cache.SetBlock(30, 64, 30, mustArchBlockState(t, "minecraft:suspicious_sand", map[string]string{"dusted": "0"}))
	cache.AddStructureBlockEntity(world.StructureBlockEntity{
		ID:            "minecraft:brushable_block",
		X:             30,
		Y:             64,
		Z:             30,
		LootTable:     world.ArchaeologyLootDesertWell,
		LootTableSeed: 77777,
	})

	// UnpackBrushableBlockLoot should hydrate from the chunk's StructureBlockEntity and roll desert_well loot.
	item, ok := srv.UnpackBrushableBlockLoot(dim, 30, 64, 30)
	if !ok || item.IsEmpty() {
		t.Fatalf("expected worldgen StructureBlockEntity brushable_block to hydrate and unpack loot, got item=%v ok=%v", item, ok)
	}

	// Breaking a suspicious_sand block directly drops NOTHING (neither the block nor its buried item).
	existed := srv.BreakBrushableBlockAt(dim, 30, 64, 30)
	if !existed {
		t.Fatalf("expected BreakBrushableBlockAt to return true")
	}
	if cache.GetBlock(30, 64, 30) != world.StateAir {
		t.Fatalf("expected broken suspicious_sand to become air")
	}
	if _, ok := srv.GetBrushableBlockAt(dim, 30, 64, 30); ok {
		t.Fatalf("expected brushable block entity to be deleted on break")
	}

	// Also test worldgen decorated_pot hydration and shattering:
	cache.SetBlock(32, 64, 32, DecoratedPotBlockState("south", false, false))
	wantSherds := [4]string{
		"minecraft:flow_pottery_sherd",
		"minecraft:guster_pottery_sherd",
		"minecraft:scrape_pottery_sherd",
		"minecraft:brick",
	}
	cache.AddStructureBlockEntity(world.StructureBlockEntity{
		ID:     "minecraft:decorated_pot",
		X:      32,
		Y:      64,
		Z:      32,
		Sherds: wantSherds[:],
		Items: []worldgen.LootItemStack{
			{Slot: 0, ItemID: "minecraft:emerald", Count: 5},
		},
	})

	if sig := srv.DecoratedPotComparatorSignalAt(dim, 32, 64, 32); sig <= 0 {
		t.Fatalf("expected worldgen decorated_pot with 5 emeralds to emit positive comparator signal, got %d", sig)
	}
	drops, stored, _, sherds, existed := srv.BreakDecoratedPotAt(dim, 32, 64, 32, true, false)
	if !existed {
		t.Fatalf("expected BreakDecoratedPotAt on worldgen decorated_pot to succeed")
	}
	if sherds != wantSherds {
		t.Fatalf("expected hydrated sherds %v, got %v", wantSherds, sherds)
	}
	if stored.ResolvedID() != "minecraft:emerald" || stored.Count != 5 {
		t.Fatalf("expected hydrated stored item 5x emerald, got %+v", stored)
	}
	if len(drops) != 5 {
		t.Fatalf("expected 4 sherd/brick drops + 1 stored item stack (5 drops total), got %d", len(drops))
	}
}

func TestDecoratedPotAdvancementAndDispenserProjectileShatter(t *testing.T) {
	srv, cache := newArchaeologyTestServer(t)
	dim := world.DimensionOverworld
	sess := newArchaeologyTestPlayer(t, srv, "potter", 3)

	// Crafting with 3 sherds + 1 brick should NOT grant Careful Restoration.
	mixedSherds := [4]string{
		"minecraft:angler_pottery_sherd",
		"minecraft:archer_pottery_sherd",
		"minecraft:arms_up_pottery_sherd",
		"minecraft:brick",
	}
	if IsAllSherdsDecoratedPot(mixedSherds) {
		t.Fatalf("expected IsAllSherdsDecoratedPot to be false when a side is minecraft:brick")
	}
	if srv.OnDecoratedPotCrafted(sess, mixedSherds) {
		t.Fatalf("expected OnDecoratedPotCrafted not to grant advancement for mixed brick/sherd pot")
	}

	// Crafting with 4 pottery sherds grants "minecraft:adventure/craft_decorated_pot_using_only_sherds".
	allSherds := [4]string{
		"minecraft:angler_pottery_sherd",
		"minecraft:archer_pottery_sherd",
		"minecraft:arms_up_pottery_sherd",
		"minecraft:blade_pottery_sherd",
	}
	if !IsAllSherdsDecoratedPot(allSherds) {
		t.Fatalf("expected IsAllSherdsDecoratedPot to be true for 4 pottery sherds")
	}
	if !srv.OnDecoratedPotCrafted(sess, allSherds) {
		t.Fatalf("expected OnDecoratedPotCrafted to grant Careful Restoration advancement")
	}
	if !sess.HasCompletedAdvancement("minecraft:adventure/craft_decorated_pot_using_only_sherds") {
		t.Fatalf("expected session to have minecraft:adventure/craft_decorated_pot_using_only_sherds")
	}

	// Place a decorated pot at (40, 64, 39) in front of a north-facing dispenser at (40, 64, 40) loaded with an arrow.
	srv.PlaceDecoratedPotAt(dim, 40, 64, 39, "south", allSherds)
	srv.SetDecoratedPotItem(dim, 40, 64, 39, world.NewNamedItemStack("minecraft:diamond", 2), ItemMeta{})
	dispState := mustArchBlockState(t, "minecraft:dispenser", map[string]string{"facing": "north", "triggered": "false"})
	cache.SetBlock(40, 64, 40, dispState)
	srv.SetDispenserSlotInDimension(dim, 40, 64, 40, 0, world.NewNamedItemStack("minecraft:arrow", 1))

	if !srv.DispenseFromDispenser(dim, 40, 64, 40) {
		t.Fatalf("expected DispenseFromDispenser with arrow to succeed")
	}
	if cache.GetBlock(40, 64, 39) != world.StateAir {
		t.Fatalf("expected arrow dispensed into decorated_pot to shatter it to air, got %q", world.BlockName(cache.GetBlock(40, 64, 39)))
	}
	if _, exists := srv.GetDecoratedPotAt(dim, 40, 64, 39); exists {
		t.Fatalf("expected shattered decorated_pot block entity to be removed")
	}
}

func TestConvertDirtToMudWithWaterBottlePlayerAndDispenser(t *testing.T) {
	srv, cache := newArchaeologyTestServer(t)
	dim := world.DimensionOverworld
	sess := newArchaeologyTestPlayer(t, srv, "mud_maker", 4)

	// 1. Player right-clicking coarse_dirt with a water bottle converts it to mud.
	cache.SetBlock(50, 64, 50, mustArchBlockState(t, "minecraft:coarse_dirt", nil))
	sess.SetInventorySlot(36, world.NewNamedItemStack("minecraft:potion", 1))
	if !srv.ConvertDirtToMudWithWaterBottle(sess, dim, 50, 64, 50) {
		t.Fatalf("expected ConvertDirtToMudWithWaterBottle on coarse_dirt to succeed")
	}
	if got := world.BlockName(cache.GetBlock(50, 64, 50)); got != "minecraft:mud" {
		t.Fatalf("expected coarse_dirt to become minecraft:mud, got %q", got)
	}
	if gotHeld := sess.HeldItem().ResolvedID(); gotHeld != "minecraft:glass_bottle" {
		t.Fatalf("expected player's water potion bottle to become minecraft:glass_bottle, got %q", gotHeld)
	}

	// 2. Dispenser dispensing a water potion bottle onto rooted_dirt converts it to mud and leaves a glass_bottle.
	cache.SetBlock(52, 64, 49, mustArchBlockState(t, "minecraft:rooted_dirt", nil))
	dispState := mustArchBlockState(t, "minecraft:dispenser", map[string]string{"facing": "north", "triggered": "false"})
	cache.SetBlock(52, 64, 50, dispState)
	srv.SetDispenserSlotWithPotion(dim, 52, 64, 50, 0, world.NewNamedItemStack("minecraft:potion", 1), PotionWater)

	if !srv.DispenseFromDispenser(dim, 52, 64, 50) {
		t.Fatalf("expected DispenseFromDispenser with water potion bottle to succeed")
	}
	if got := world.BlockName(cache.GetBlock(52, 64, 49)); got != "minecraft:mud" {
		t.Fatalf("expected dispenser water bottle to convert rooted_dirt to minecraft:mud, got %q", got)
	}
	slots := srv.DispenserSlotsInDimension(dim, 52, 64, 50)
	if slots[0].ResolvedID() != "minecraft:glass_bottle" || slots[0].Count != 1 {
		t.Fatalf("expected dispenser slot 0 to hold 1x minecraft:glass_bottle, got %+v", slots[0])
	}
}

func TestBrushableBlocksPersistenceAndChunkUnloadReloadRoundTrip(t *testing.T) {
	srv, cache := newArchaeologyTestServer(t)
	dim := world.DimensionOverworld

	cache.SetBlock(16, 63, 16, mustArchBlockState(t, "minecraft:sandstone", nil))
	srv.PlaceBrushableBlockAt(dim, 16, 64, 16, "minecraft:suspicious_sand", world.ArchaeologyLootDesertPyramid, 12345)
	srv.SetBrushableBlockItem(dim, 16, 64, 16, world.NewNamedItemStack("minecraft:archer_pottery_sherd", 1), ItemMeta{})

	sess := newArchaeologyTestPlayer(t, srv, "persister", 5)
	sess.SetInventorySlot(36, world.NewNamedItemStack(BrushItemID, 1))
	// Brush twice so dusted=2, BrushCount=3
	srv.BrushBlockAt(sess, dim, 16, 64, 16, "east")
	srv.BrushBlockAt(sess, dim, 16, 64, 16, "east")

	// 1. SaveBrushableBlocks / LoadBrushableBlocks round-trip
	if err := srv.SaveBrushableBlocks(); err != nil {
		t.Fatalf("SaveBrushableBlocks failed: %v", err)
	}

	// Unload chunk (1, 1) and verify runtime entry is removed
	nbtList := srv.UnloadChunkBrushableBlocks(dim, 1, 1)
	if len(nbtList) != 1 {
		t.Fatalf("expected 1 NBT compound from UnloadChunkBrushableBlocks, got %d", len(nbtList))
	}
	// Reset block state to dusted=0 to verify LoadChunkBrushableBlockEntitiesNBT restores dusted=2
	cache.SetBlock(16, 64, 16, world.BrushableBlockState("minecraft:suspicious_sand", 0))

	// Reload from chunk NBT list
	if n := srv.LoadChunkBrushableBlockEntitiesNBT(dim, nbtList); n != 1 {
		t.Fatalf("expected LoadChunkBrushableBlockEntitiesNBT to load 1 entity, got %d", n)
	}
	reloaded, ok := srv.GetBrushableBlockAt(dim, 16, 64, 16)
	if !ok || reloaded == nil {
		t.Fatalf("expected brushable block entity after chunk reload")
	}
	if reloaded.Item.ResolvedID() != "minecraft:archer_pottery_sherd" || reloaded.Dusted != 2 || reloaded.HitDirection != "east" {
		t.Fatalf("unexpected reloaded state: %+v", reloaded)
	}
	if got, _ := world.BrushableBlockDustedStage(cache.GetBlock(16, 64, 16)); got != 2 {
		t.Fatalf("expected dusted=2 after reload, got %d", got)
	}

	// Also test full file reload after clearing store
	srv.UnloadChunkBrushableBlocks(dim, 1, 1)
	if err := srv.LoadBrushableBlocks(); err != nil {
		t.Fatalf("LoadBrushableBlocks failed: %v", err)
	}
	fromDisk, ok := srv.GetBrushableBlockAt(dim, 16, 64, 16)
	if !ok || fromDisk == nil || fromDisk.Item.ResolvedID() != "minecraft:archer_pottery_sherd" {
		t.Fatalf("expected LoadBrushableBlocks from disk to restore archer_pottery_sherd, got %+v (ok=%v)", fromDisk, ok)
	}
}
