package server

import (
	"strings"
	"sync"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func newBedTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := DefaultConfig()
	cfg.WorldDir = ""
	srv, err := NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	return srv
}

func decodeChatMessages(pkts [][]byte) []string {
	var out []string
	for _, body := range pkts {
		if len(body) >= 4 && body[0] == 0x08 {
			n := int(body[1])<<8 | int(body[2])
			if 3+n <= len(body) {
				out = append(out, string(body[3:3+n]))
				continue
			}
		}
		out = append(out, string(body))
	}
	return out
}

func TestBedPlacementAndBreakBothHalves(t *testing.T) {
	srv := newBedTestServer(t)
	p, err := srv.RegisterPlayer(Profile{Name: "Sleeper", UUID: OfflineUUID("Sleeper")}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerTransform(p, 10.5, 4.0, 10.5, 180, 0, true) // Yaw 180 -> facing north (dz = -1)
	_ = srv.SetPlayerGameMode(p, GameModeSurvival)

	redBedBaseState, ok := BedBlockState("minecraft:red_bed", "north", "foot", false)
	if !ok {
		t.Fatal("minecraft:red_bed block state not found")
	}

	// Place bed foot at (10, 4, 10) facing north -> head at (10, 4, 9).
	if !srv.PlaceBedBlock(p, 10, 4, 10, redBedBaseState) {
		t.Fatal("PlaceBedBlock returned false")
	}

	footState := srv.Chunks().GetBlock(10, 4, 10)
	headState := srv.Chunks().GetBlock(10, 4, 9)
	_, footFacing, footPart, footOcc, ok := BedStateInfo(footState)
	if !ok || footPart != "foot" || footFacing != "north" || footOcc {
		t.Fatalf("unexpected foot BedStateInfo: facing=%s part=%s occ=%v ok=%v", footFacing, footPart, footOcc, ok)
	}
	_, headFacing, headPart, headOcc, ok := BedStateInfo(headState)
	if !ok || headPart != "head" || headFacing != "north" || headOcc {
		t.Fatalf("unexpected head BedStateInfo: facing=%s part=%s occ=%v ok=%v", headFacing, headPart, headOcc, ok)
	}

	// Breaking the head block in survival should also remove the foot block and spawn 1 bed drop.
	if broken, _ := srv.BreakBlock(p, 10, 4, 9, PlayerActionStopDestroyBlock); !broken {
		t.Fatal("BreakBlock head returned false")
	}
	afterFoot := srv.Chunks().GetBlock(10, 4, 10)
	afterHead := srv.Chunks().GetBlock(10, 4, 9)
	if afterFoot != 0 || afterHead != 0 {
		t.Fatalf("expected both bed halves to be air (0), got foot=%d head=%d", afterFoot, afterHead)
	}

	var bedDrops int
	for _, e := range srv.Entities().AppendInChunkRange(nil, 0, 0, 2) {
		if e.IsItem() && e.Item.ResolvedID() == "minecraft:red_bed" {
			bedDrops += int(e.Item.Count)
		}
	}
	if bedDrops != 1 {
		t.Fatalf("expected 1 red_bed item drop after breaking bed, got %d", bedDrops)
	}
}

func TestInteractBedOverworldSpawnAndSleepChecks(t *testing.T) {
	srv := newBedTestServer(t)
	var mu sync.Mutex
	var chatBodies [][]byte
	p, err := srv.RegisterPlayer(Profile{Name: "Dreamer", UUID: OfflineUUID("Dreamer")}, func(pktID int32, body []byte) error {
		if pktID == protocol.PlaySystemChat {
			mu.Lock()
			chatBodies = append(chatBodies, append([]byte(nil), body...))
			mu.Unlock()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerTransform(p, 20.5, 4.0, 20.5, 0, 0, true) // Yaw 0 -> south (dz = +1)
	redBedBaseState, _ := BedBlockState("minecraft:red_bed", "south", "foot", false)
	if !srv.PlaceBedBlock(p, 20, 4, 20, redBedBaseState) {
		t.Fatal("PlaceBedBlock failed")
	}

	// 1. Daytime interaction: sets spawn point to bed head (20, 4, 21), sends "Respawn point set" and "You can sleep only at night or during thunderstorms".
	srv.SetDayTime(1000) // day
	if opened, _, err := srv.InteractBlock(p, 20, 4, 20); err != nil || !opened {
		t.Fatalf("InteractBlock on bed = (%v, %v)", opened, err)
	}
	snap := p.Snapshot()
	if !snap.HasSpawn || snap.SpawnX != 20 || snap.SpawnY != 4 || snap.SpawnZ != 21 || snap.SpawnDimension != world.DimensionOverworld {
		t.Fatalf("expected spawn point at bed head (20,4,21) in overworld, got %+v", snap)
	}
	mu.Lock()
	msgs := decodeChatMessages(chatBodies)
	chatBodies = nil
	mu.Unlock()
	joined := strings.Join(msgs, " | ")
	if !strings.Contains(joined, "Respawn point set") || !strings.Contains(joined, "You can sleep only at night") {
		t.Fatalf("unexpected daytime bed chat messages: %v", msgs)
	}

	// 2. Too far away at night.
	srv.SetDayTime(13000) // night
	srv.SetPlayerTransform(p, 40.5, 4.0, 40.5, 0, 0, true)
	srv.InteractBlock(p, 20, 4, 20)
	mu.Lock()
	msgs = decodeChatMessages(chatBodies)
	chatBodies = nil
	mu.Unlock()
	if !strings.Contains(strings.Join(msgs, " | "), "the bed is too far away") {
		t.Fatalf("expected too-far message, got %v", msgs)
	}

	// 3. Hostile monster nearby at night.
	srv.SetPlayerTransform(p, 20.5, 4.0, 20.5, 0, 0, true)
	_ = srv.SetPlayerGameMode(p, GameModeSurvival)
	zombieID := srv.Entities().Add(&world.Entity{
		TypeName:  "minecraft:zombie",
		Dimension: world.DimensionOverworld,
		X:         22.5,
		Y:         4.0,
		Z:         21.5,
		Health:    20,
		MaxHealth: 20,
	})
	srv.InteractBlock(p, 20, 4, 20)
	mu.Lock()
	msgs = decodeChatMessages(chatBodies)
	chatBodies = nil
	mu.Unlock()
	if !strings.Contains(strings.Join(msgs, " | "), "there are monsters nearby") {
		t.Fatalf("expected monsters-nearby message, got %v", msgs)
	}
	srv.Entities().Remove(zombieID)

	// 4. Add a second player so night isn't skipped immediately when Dreamer sleeps; verify occupied check.
	other, err := srv.RegisterPlayer(Profile{Name: "Watcher", UUID: OfflineUUID("Watcher")}, func(pktID int32, body []byte) error {
		if pktID == protocol.PlaySystemChat {
			mu.Lock()
			chatBodies = append(chatBodies, append([]byte(nil), body...))
			mu.Unlock()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer Watcher: %v", err)
	}
	srv.SetPlayerTransform(other, 21.5, 4.0, 20.5, 0, 0, true)

	srv.InteractBlock(p, 20, 4, 20)
	if !srv.IsPlayerSleeping(p) {
		t.Fatal("expected Dreamer to be sleeping")
	}
	headState := srv.Chunks().GetBlock(20, 4, 21)
	if _, _, _, occ, ok := BedStateInfo(headState); !ok || !occ {
		t.Fatalf("expected bed head occupied=true while sleeping, got occ=%v ok=%v", occ, ok)
	}

	// Watcher tries to use the same occupied bed.
	chatBodies = nil
	srv.InteractBlock(other, 20, 4, 20)
	mu.Lock()
	msgs = decodeChatMessages(chatBodies)
	chatBodies = nil
	mu.Unlock()
	if !strings.Contains(strings.Join(msgs, " | "), "This bed is occupied") {
		t.Fatalf("expected occupied message, got %v", msgs)
	}

	// Unregister Watcher -> all remaining Overworld players (Dreamer) are sleeping -> UnregisterPlayer automatically skips night, wakes Dreamer, and clears occupied.
	srv.UnregisterPlayer(other)
	if srv.IsNight() {
		t.Fatal("expected night to be skipped to morning when last awake player unregistered")
	}
	if srv.IsPlayerSleeping(p) {
		t.Fatal("expected Dreamer to be woken after night skip")
	}
	headStateAfter := srv.Chunks().GetBlock(20, 4, 21)
	if _, _, _, occ, ok := BedStateInfo(headStateAfter); !ok || occ {
		t.Fatalf("expected bed head occupied=false after wake, got occ=%v ok=%v", occ, ok)
	}
}

func TestBedExplodesInNether(t *testing.T) {
	srv := newBedTestServer(t)
	var mu sync.Mutex
	var chatBodies [][]byte
	p, err := srv.RegisterPlayer(Profile{Name: "NetherSleeper", UUID: OfflineUUID("NetherSleeper")}, func(pktID int32, body []byte) error {
		if pktID == protocol.PlaySystemChat {
			mu.Lock()
			chatBodies = append(chatBodies, append([]byte(nil), body...))
			mu.Unlock()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	p.SetDimension(world.DimensionNether)
	srv.SetPlayerTransform(p, 10.5, 64.0, 10.5, 0, 0, true)
	_ = srv.SetPlayerGameMode(p, GameModeSurvival)

	netherCache := srv.DimensionChunks(world.DimensionNether)
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 2; dz++ {
			_, _ = netherCache.SetBlockWithLight(10+dx, 64, 10+dz, world.StateAir)
			_, _ = netherCache.SetBlockWithLight(10+dx, 65, 10+dz, world.StateAir)
		}
	}

	redBedBaseState, _ := BedBlockState("minecraft:red_bed", "south", "foot", false)
	if !srv.PlaceBedBlock(p, 10, 64, 10, redBedBaseState) {
		t.Fatal("PlaceBedBlock in Nether failed")
	}

	srv.InteractBlock(p, 10, 64, 10)
	if st := netherCache.GetBlock(10, 64, 10); st != world.StateAir {
		t.Fatalf("expected bed foot to be removed on Nether explosion, got state %d", st)
	}
	if p.Snapshot().Health >= world.DefaultPlayerHealth {
		t.Fatalf("expected player to take explosion damage in Nether, health=%v", p.Snapshot().Health)
	}

	// Deal fatal bad_respawn_point damage to verify death message.
	srv.DamagePlayer(p, 100, "bad_respawn_point")
	mu.Lock()
	msgs := decodeChatMessages(chatBodies)
	mu.Unlock()
	if !strings.Contains(strings.Join(msgs, " | "), "[Intentional Game Design]") {
		t.Fatalf("expected [Intentional Game Design] death message, got %v", msgs)
	}
}

func TestRespawnAnchorChargeUseExplodeAndRespawn(t *testing.T) {
	srv := newBedTestServer(t)
	var mu sync.Mutex
	var chatBodies [][]byte
	p, err := srv.RegisterPlayer(Profile{Name: "AnchorUser", UUID: OfflineUUID("AnchorUser")}, func(pktID int32, body []byte) error {
		if pktID == protocol.PlaySystemChat {
			mu.Lock()
			chatBodies = append(chatBodies, append([]byte(nil), body...))
			mu.Unlock()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	p.SetDimension(world.DimensionNether)
	srv.SetPlayerTransform(p, 5.5, 64.0, 5.5, 0, 0, true)
	_ = srv.SetPlayerGameMode(p, GameModeSurvival)

	netherCache := srv.DimensionChunks(world.DimensionNether)
	// Carve air around (5, 64..66, 5..7) in the Nether so anchor stand-up positions are unobstructed.
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			_, _ = netherCache.SetBlockWithLight(5+dx, 63, 5+dz, 1) // solid floor
			_, _ = netherCache.SetBlockWithLight(5+dx, 64, 5+dz, world.StateAir)
			_, _ = netherCache.SetBlockWithLight(5+dx, 65, 5+dz, world.StateAir)
		}
	}
	anchor0, ok := RespawnAnchorState(0)
	if !ok {
		t.Fatal("RespawnAnchorState(0) failed")
	}
	_, _ = netherCache.SetBlockWithLight(5, 64, 5, anchor0)

	glowstone := world.NewNamedItemStack("minecraft:glowstone", 4)
	if glowstone.IsEmpty() {
		t.Fatal("minecraft:glowstone item not found")
	}
	p.SetInventorySlot(world.HotbarContainerOffset, glowstone)
	p.SetSelectedSlot(0)

	// Charge twice -> charges = 2, glowstone count = 2.
	srv.InteractBlock(p, 5, 64, 5)
	srv.InteractBlock(p, 5, 64, 5)
	st := netherCache.GetBlock(5, 64, 5)
	if charges := RespawnAnchorCharges(st); charges != 2 {
		t.Fatalf("expected respawn anchor charges=2, got %d", charges)
	}
	if gotCount := p.HeldItem().Count; gotCount != 2 {
		t.Fatalf("expected 2 glowstone remaining in hand, got %d", gotCount)
	}

	// Switch to empty hand and right-click anchor in Nether -> sets spawn point without consuming charge.
	p.SetSelectedSlot(1)
	srv.InteractBlock(p, 5, 64, 5)
	snap := p.Snapshot()
	if !snap.HasSpawn || snap.SpawnDimension != world.DimensionNether || snap.SpawnX != 5 || snap.SpawnY != 64 || snap.SpawnZ != 5 {
		t.Fatalf("expected Nether spawn point at (5,64,5), got %+v", snap)
	}

	// Die and respawn -> respawns in Nether near (5,64,5) and consumes 1 anchor charge (2 -> 1).
	srv.DamagePlayer(p, 100, "generic")
	if err := srv.RespawnPlayer(p); err != nil {
		t.Fatalf("RespawnPlayer from anchor: %v", err)
	}
	stAfter := netherCache.GetBlock(5, 64, 5)
	if charges := RespawnAnchorCharges(stAfter); charges != 1 {
		t.Fatalf("expected respawn anchor charge to decrement to 1 after respawn, got %d", charges)
	}
	if p.Dimension() != world.DimensionNether {
		t.Fatalf("expected player to respawn in Nether, got %s", p.Dimension())
	}

	// Now drain the last charge by respawning again (1 -> 0), then die a third time -> anchor has 0 charges -> fallback to Overworld world spawn with obstruction message.
	srv.DamagePlayer(p, 100, "generic")
	_ = srv.RespawnPlayer(p)
	srv.DamagePlayer(p, 100, "generic")
	mu.Lock()
	chatBodies = nil
	mu.Unlock()
	_ = srv.RespawnPlayer(p)
	if p.Dimension() != world.DimensionOverworld {
		t.Fatalf("expected fallback respawn in Overworld when anchor is uncharged, got %s", p.Dimension())
	}
	mu.Lock()
	msgs := decodeChatMessages(chatBodies)
	mu.Unlock()
	if !strings.Contains(strings.Join(msgs, " | "), "You have no home bed or charged respawn anchor, or it was obstructed") {
		t.Fatalf("expected missing/obstructed spawn message, got %v", msgs)
	}
}
