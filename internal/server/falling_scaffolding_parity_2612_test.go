package server

import (
	"testing"

	"regionio/internal/world"
)

func TestFallingAnvilTickByTickDamageHelmetAndNonFullLanding2612(t *testing.T) {
	srv, cache := newArchaeologyTestServer(t)
	dim := world.DimensionOverworld

	x, z := 12, 12
	floorY := 63
	for y := floorY; y <= floorY+30; y++ {
		cache.SetBlock(x, y, z, world.StateAir)
	}
	cache.SetBlock(x, floorY, z, world.StateStone)

	// Place Survival player with an iron_helmet standing at (x+0.5, floorY+1, z+0.5).
	victim := newArchaeologyTestPlayer(t, srv, "anvil_victim", 11)
	_ = srv.TeleportPlayer(victim, float64(x)+0.5, float64(floorY+1), float64(z)+0.5, 0, 0)
	victim.SetInventorySlot(ArmorSlotHead, world.NewNamedItemStack("minecraft:iron_helmet", 1))

	// Place anvil at startY = floorY + 6 (falls 5 blocks from 69 to 64 -> Mth.ceil(5.0 - 1.0) = 4 -> 8.0 raw damage,
	// 6.0 damage after 0.75 helmet factor, max(1, floor(8.0 / 4.0)) = 2 helmet durability wear).
	// Physics arrival: T=2 detaches (entity-tick 1), lands on T=18 (entity-tick 17)!
	southAnvil := AnvilBlockState("minecraft:anvil", "south")
	cache.ApplyBlockWithUpdates(x, floorY+6, z, southAnvil)

	for tick := 1; tick <= 17; tick++ {
		srv.TickDimensionBlockUpdates(dim)
		if victim.Snapshot().Health != 20.0 {
			t.Fatalf("player took falling anvil damage too early on tick %d (want tick 18): health=%v",
				tick, victim.Snapshot().Health)
		}
	}

	// Tick 18: anvil lands!
	// Vanilla 26.1.2 LivingEntity.hurtServer + Player.hurtArmor (falling_anvil is in #damages_helmet
	// and NOT in #bypasses_armor):
	//   - hurtHelmet(8.0f) -> max(1, floor(8.0/4.0)) = 2 wear
	//   - damage *= 0.75f -> 6.0f
	//   - hurtArmor(6.0f) -> max(1, floor(6.0/4.0)) = 1 wear (total wear = 3)
	srv.TickDimensionBlockUpdates(dim)
	if gotHP := victim.Snapshot().Health; gotHP != 14.0 {
		t.Fatalf("after 5-block anvil fall with helmet on tick 18: health=%v, want 14.0 (20 - 8*0.75)", gotHP)
	}
	if gotWear := GetSlotItemMeta(victim, ArmorSlotHead).Damage; gotWear != 3 {
		t.Fatalf("helmet durability wear = %d, want 3 (hurtHelmet=2 + hurtArmor=1)", gotWear)
	}

	// Non-damageable head item (e.g. carved_pumpkin) also gets the 0.75f helmet damage factor in 26.1.2!
	pumpkinVictim := newArchaeologyTestPlayer(t, srv, "pumpkin_victim", 12)
	_ = srv.TeleportPlayer(pumpkinVictim, float64(x)+0.5, float64(floorY+1), float64(z)+0.5, 0, 0)
	pumpkinVictim.SetInventorySlot(ArmorSlotHead, world.NewNamedItemStack("minecraft:carved_pumpkin", 1))
	cache.SetBlock(x, floorY+1, z, world.StateAir)
	cache.ApplyBlockWithUpdates(x, floorY+6, z, southAnvil)
	for tick := 0; tick < 18; tick++ {
		srv.TickDimensionBlockUpdates(dim)
	}
	if gotHP := pumpkinVictim.Snapshot().Health; gotHP != 14.0 {
		t.Fatalf("carved_pumpkin head slot should still reduce falling_anvil by 25%% (14.0 HP), got %v", gotHP)
	}
	_ = srv.TeleportPlayer(victim, float64(x)+100, float64(floorY+1), float64(z)+0.5, 0, 0)
	_ = srv.TeleportPlayer(pumpkinVictim, float64(x)+100, float64(floorY+1), float64(z)+0.5, 0, 0)

	// Damaged anvil falling 25 blocks (100% degrade -> breaks in causeFallDamage -> cancelDrop=true) onto a torch:
	// torch must NOT be overwritten and NO damaged_anvil item may drop!
	tx, tz := 18, 12
	for y := floorY; y <= floorY+30; y++ {
		cache.SetBlock(tx, y, tz, world.StateAir)
	}
	cache.SetBlock(tx, floorY, tz, world.StateStone)
	torchSt := mustArchBlockState(t, "minecraft:torch", nil)
	cache.SetBlock(tx, floorY+1, tz, torchSt)

	dmgAnvil := AnvilBlockState("minecraft:damaged_anvil", "north")
	cache.ApplyBlockWithUpdates(tx, floorY+26, tz, dmgAnvil)
	for tick := 0; tick < 60; tick++ {
		srv.TickDimensionBlockUpdates(dim)
	}
	if got := cache.GetBlock(tx, floorY+1, tz); got != torchSt {
		t.Fatalf("torch at (tx, floorY+1, tz) was overwritten: got %s, want minecraft:torch", world.BlockName(got))
	}
	if got := cache.GetBlock(tx, floorY+2, tz); got != world.StateAir {
		t.Fatalf("damaged_anvil hovered above torch at floorY+2: got %s", world.BlockName(got))
	}
	for _, ent := range srv.Entities().All() {
		if ent.IsItem() && ent.Item.ResolvedID() == "minecraft:damaged_anvil" {
			t.Fatalf("broken damaged_anvil (cancelDrop=true) should drop 0 items, found item entity %+v", ent)
		}
	}
}

func TestBrushableBlockCheckResetVsFallAndBlockEntityCleanup2612(t *testing.T) {
	srv, cache := newArchaeologyTestServer(t)
	dim := world.DimensionOverworld
	sess := newArchaeologyTestPlayer(t, srv, "arch_parity", 12)
	sess.SetInventorySlot(36, world.NewNamedItemStack(BrushItemID, 1))

	x, y, z := 26, 68, 26
	for cy := 62; cy <= 72; cy++ {
		cache.SetBlock(x, cy, z, world.StateAir)
	}
	cache.SetBlock(x, 62, z, world.StateStone)
	cache.SetBlock(x, y-1, z, world.StateStone)

	// 1. Place suspicious_sand with buried diamond on solid stone and brush twice (dusted=2).
	diamond := world.NewNamedItemStack("minecraft:diamond", 1)
	srv.PlaceBrushableBlockWithItemAt(dim, x, y, z, "minecraft:suspicious_sand", diamond, ItemMeta{}, "", 0)
	srv.BrushBlockAt(sess, dim, x, y, z, "up")
	srv.BrushBlockAt(sess, dim, x, y, z, "up")
	if got, _ := world.BrushableBlockDustedStage(cache.GetBlock(x, y, z)); got != 2 {
		t.Fatalf("expected dusted=2 after 2 brushes, got %d", got)
	}

	// Trigger a neighbor block update next to suspicious_sand and advance 39 server ticks:
	// FallingBlock/BrushableBlock TICK_DELAY=2 must NOT reset dusted prematurely before 40 idle ticks!
	cache.ApplyBlockWithUpdates(x+1, y, z, world.StateStone)
	for tick := 1; tick <= 39; tick++ {
		srv.StepServerTick(nil)
		if got, _ := world.BrushableBlockDustedStage(cache.GetBlock(x, y, z)); got != 2 {
			t.Fatalf("dusted decayed prematurely on tick %d (got %d, want 2 until tick 40)", tick, got)
		}
	}

	// Tick 40: checkReset steps dusted 2 -> 1, and tick 44 steps dusted 1 -> 0 while preserving buried diamond!
	srv.StepServerTick(nil) // tick 40
	if got, _ := world.BrushableBlockDustedStage(cache.GetBlock(x, y, z)); got != 1 {
		t.Fatalf("on tick 40 expected dusted=1, got %d", got)
	}
	for i := 0; i < 4; i++ {
		srv.StepServerTick(nil)
	}
	if got, _ := world.BrushableBlockDustedStage(cache.GetBlock(x, y, z)); got != 0 {
		t.Fatalf("on tick 44 expected dusted=0, got %d", got)
	}
	if be, ok := srv.GetBrushableBlockAt(dim, x, y, z); !ok || be.Item.ResolvedID() != "minecraft:diamond" {
		t.Fatalf("expected buried diamond to remain in BrushableBlockEntity after reset, got (%+v, %v)", be, ok)
	}

	// 2. Break supporting stone at (x, y-1, z):
	// On tick 1: still suspicious_sand at y.
	// On tick 2: detaches (cancelDrop=true) and cleans up BrushableBlockEntity!
	// Upon hitting stone at y=62: shatters leaving StateAir, 0 item drops (no suspicious_sand, no diamond), and 0 block entities!
	cache.BreakBlockWithUpdates(x, y-1, z)
	srv.TickDimensionBlockUpdates(dim) // tick 1
	if !world.IsBrushableBlock(cache.GetBlock(x, y, z)) {
		t.Fatalf("tick 1 after breaking support: suspicious_sand detached before 2-tick delay")
	}

	for tick := 2; tick <= 30; tick++ {
		srv.TickDimensionBlockUpdates(dim)
	}
	if got := cache.GetBlock(x, 63, z); got != world.StateAir {
		t.Fatalf("falling suspicious_sand did not shatter at y=63: got %s", world.BlockName(got))
	}
	if be, ok := srv.GetBrushableBlockAt(dim, x, y, z); ok {
		t.Fatalf("orphaned BrushableBlockEntity remained at origin (x,y,z) after falling: %+v", be)
	}
	if be, ok := srv.GetBrushableBlockAt(dim, x, 63, z); ok {
		t.Fatalf("unexpected BrushableBlockEntity at landing (x,63,z): %+v", be)
	}
	for _, ent := range srv.Entities().All() {
		if ent.IsItem() {
			t.Fatalf("falling suspicious_sand must drop neither block nor buried item, found item %+v", ent.Item)
		}
	}
}
