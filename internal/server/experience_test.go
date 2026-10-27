package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestExperienceLevelMathAndSetExperiencePacket(t *testing.T) {
	// Verify XPForNextLevel & TotalXPForLevel across all 3 vanilla brackets (0..15, 16..30, 31+).
	var runningTotal int32
	for lvl := int32(0); lvl <= 40; lvl++ {
		if got := TotalXPForLevel(lvl); got != runningTotal {
			t.Fatalf("TotalXPForLevel(%d) = %d, want %d", lvl, got, runningTotal)
		}
		gotLvl, gotProg, gotRem := LevelAndProgressFromTotalXP(runningTotal)
		if gotLvl != lvl || math.Abs(float64(gotProg)) > 1e-5 || gotRem != 0 {
			t.Fatalf("LevelAndProgressFromTotalXP(%d) = (lvl=%d, prog=%v, rem=%d), want (%d, 0, 0)",
				runningTotal, gotLvl, gotProg, gotRem, lvl)
		}
		runningTotal += XPForNextLevel(lvl)
	}

	// Verify EncodeSetExperience / DecodeSetExperience round-trip.
	pkt := EncodeSetExperience(0.625, 24, 850)
	prog, lvl, total, err := DecodeSetExperience(protocol.NewReader(pkt))
	if err != nil {
		t.Fatalf("DecodeSetExperience failed: %v", err)
	}
	if math.Abs(float64(prog-0.625)) > 1e-5 || lvl != 24 || total != 850 {
		t.Fatalf("DecodeSetExperience = (prog=%v, lvl=%d, total=%d), want (0.625, 24, 850)", prog, lvl, total)
	}

	// Verify SplitExperienceOrbValues and ExperienceOrbValueIcon.
	split := SplitExperienceOrbValues(2488) // 2477 + 7 + 3 + 1
	wantSplit := []int32{2477, 7, 3, 1}
	if len(split) != len(wantSplit) {
		t.Fatalf("SplitExperienceOrbValues(2488) = %v, want %v", split, wantSplit)
	}
	for i := range wantSplit {
		if split[i] != wantSplit[i] {
			t.Fatalf("SplitExperienceOrbValues(2488)[%d] = %d, want %d", i, split[i], wantSplit[i])
		}
	}
	if ExperienceOrbValueIcon(2477) != 10 || ExperienceOrbValueIcon(7) != 2 || ExperienceOrbValueIcon(1) != 0 {
		t.Fatalf("unexpected ExperienceOrbValueIcon values: 2477->%d, 7->%d, 1->%d",
			ExperienceOrbValueIcon(2477), ExperienceOrbValueIcon(7), ExperienceOrbValueIcon(1))
	}
}

func TestExperienceOrbSpawnMergeAndPackets(t *testing.T) {
	srv := newSurvivalTestServer(t)
	var addPkt, dataPkt, removePkt []byte
	_ = newFishingTestPlayer(t, srv, "OrbWatcher", 0.5, 65.0, 0.5, func(pktID int32, payload []byte) error {
		switch pktID {
		case protocol.PlayAddEntity:
			addPkt = append([]byte(nil), payload...)
		case PlaySetEntityDataCB:
			dataPkt = append([]byte(nil), payload...)
		case protocol.PlayRemoveEntities:
			removePkt = append([]byte(nil), payload...)
		}
		return nil
	})

	// 1. Spawn 15 XP (denominations: 7, 7, 1) at the same position:
	// the two 7-value orbs must merge into a single entity with Count=2, TotalXP=14.
	ids := srv.SpawnExperienceOrbs(4.5, 65.0, 4.5, 15)
	if len(ids) != 2 {
		t.Fatalf("SpawnExperienceOrbs(15) merged entity count = %d, want 2 (one 7x2 orb and one 1x1 orb)", len(ids))
	}
	if srv.ExperienceOrbValue(ids[0]) != 7 || srv.ExperienceOrbCount(ids[0]) != 2 || srv.ExperienceOrbTotalXP(ids[0]) != 14 {
		t.Fatalf("orb[0] = (val=%d, cnt=%d, total=%d), want (7, 2, 14)",
			srv.ExperienceOrbValue(ids[0]), srv.ExperienceOrbCount(ids[0]), srv.ExperienceOrbTotalXP(ids[0]))
	}
	if srv.ExperienceOrbValue(ids[1]) != 1 || srv.ExperienceOrbCount(ids[1]) != 1 || srv.ExperienceOrbTotalXP(ids[1]) != 1 {
		t.Fatalf("orb[1] = (val=%d, cnt=%d, total=%d), want (1, 1, 1)",
			srv.ExperienceOrbValue(ids[1]), srv.ExperienceOrbCount(ids[1]), srv.ExperienceOrbTotalXP(ids[1]))
	}
	if len(addPkt) == 0 || len(dataPkt) == 0 {
		t.Fatalf("expected PlayAddEntity (%d bytes) and PlaySetEntityData (%d bytes) for spawned XP orb", len(addPkt), len(dataPkt))
	}

	// 2. Spawn two separate 37-value orbs within 0.5 blocks and run MergeNearbyExperienceOrbsInDimension.
	idA, _ := srv.SpawnExperienceOrb(10.0, 65.0, 10.0, 37)
	idB, _ := srv.SpawnExperienceOrb(10.2, 65.0, 10.1, 37)
	merged := srv.MergeNearbyExperienceOrbsInDimension(world.DimensionOverworld)
	if merged != 1 {
		t.Fatalf("MergeNearbyExperienceOrbsInDimension = %d, want 1", merged)
	}
	if srv.entities.Get(idB) != nil {
		t.Fatal("expected merged orb idB to be removed from EntityManager")
	}
	if srv.ExperienceOrbCount(idA) != 2 || srv.ExperienceOrbTotalXP(idA) != 74 {
		t.Fatalf("surviving orb idA = (cnt=%d, total=%d), want (2, 74)",
			srv.ExperienceOrbCount(idA), srv.ExperienceOrbTotalXP(idA))
	}
	if len(removePkt) == 0 {
		t.Fatal("expected PlayRemoveEntities packet when merging nearby XP orbs")
	}
}

func TestExperienceOrbAttractAutoPickupAndDespawn(t *testing.T) {
	srv := newSurvivalTestServer(t)
	// Build a solid stone floor at Y=64 so the orb rests at Y=65.
	for x := -2; x <= 10; x++ {
		for z := -2; z <= 10; z++ {
			srv.chunks.SetBlock(x, 64, z, world.StateStone)
		}
	}

	var sawMotion, sawTake, sawPickupSound, sawSetXP bool
	session := newFishingTestPlayer(t, srv, "MagnetPlayer", 0.5, 65.0, 0.5, func(pktID int32, _ []byte) error {
		switch pktID {
		case protocol.PlaySetEntityMotion:
			sawMotion = true
		case protocol.PlayTakeItemEntity:
			sawTake = true
		case PlaySoundPacketID:
			sawPickupSound = true
		case PlaySetExperience:
			sawSetXP = true
		}
		return nil
	})

	// 1. Spawn an orb 5.0 blocks away (inside 8.0-block attract radius, outside 1.75-block pickup radius).
	orbID, _ := srv.SpawnExperienceOrb(5.5, 65.0, 0.5, 17)
	for tick := 0; tick < 80; tick++ {
		srv.TickExperienceOrbs()
		if srv.entities.Get(orbID) == nil {
			break
		}
	}
	if srv.entities.Get(orbID) != nil {
		t.Fatal("expected orb within 8 blocks to be attracted to player and automatically picked up")
	}
	if !sawMotion || !sawTake || !sawPickupSound || !sawSetXP {
		t.Fatalf("expected motion=%v, take=%v, sound=%v, setXP=%v on orb attraction & pickup",
			sawMotion, sawTake, sawPickupSound, sawSetXP)
	}
	if session.TotalExperience() != 17 {
		t.Fatalf("player TotalExperience after attracted orb pickup = %d, want 17", session.TotalExperience())
	}

	// 2. Verify an orb despawns at ExperienceOrbMaxAgeTicks (6000 ticks).
	oldOrbID, _ := srv.SpawnExperienceOrb(50.5, 65.0, 50.5, 7)
	srv.entities.Update(oldOrbID, func(e *world.Entity) {
		e.Age = ExperienceOrbMaxAgeTicks - 1
	})
	srv.TickExperienceOrbs()
	if srv.entities.Get(oldOrbID) != nil || srv.ExperienceOrbValue(oldOrbID) != 0 {
		t.Fatal("expected experience orb at 6000 ticks to despawn and clean up state")
	}
}

func TestMendingPriorityMultiSlotLevelUpSoundAndScoreboard(t *testing.T) {
	srv := newSurvivalTestServer(t)
	var sawSlotSync, sawLevelUpSound bool
	session := newFishingTestPlayer(t, srv, "MendingKnight", 0.5, 65.0, 0.5, func(pktID int32, _ []byte) error {
		switch pktID {
		case protocol.PlayContainerSetSlot:
			sawSlotSync = true
		case PlaySoundPacketID:
			sawLevelUpSound = true
		}
		return nil
	})

	_, _ = srv.CreateObjective("pl_lvl", "level", ChatComponent{Text: "Level"})
	_, _ = srv.CreateObjective("pl_xp", "xp", ChatComponent{Text: "XP"})

	hotbarSlot := world.HotbarContainerOffset + int(session.Snapshot().SelectedSlot)

	// Put an UNDAMAGED Mending sword in MainHand (slot 36): must NOT steal XP!
	sword := world.NewNamedItemStack("minecraft:diamond_sword", 1)
	session.SetInventorySlot(hotbarSlot, sword)
	var swordMeta ItemMeta
	swordMeta.Damage = 0
	swordMeta.SetEnchantment("minecraft:mending", 1)
	SetSlotItemMeta(session, hotbarSlot, swordMeta)

	// Put a DAMAGED Mending shield in OffHand (slot 45, Damage=6 -> needs 3 XP)
	// and DAMAGED Mending helmet in Head (slot 5, Damage=10 -> needs 5 XP).
	shield := world.NewNamedItemStack("minecraft:shield", 1)
	session.SetInventorySlot(45, shield)
	var shieldMeta ItemMeta
	shieldMeta.Damage = 6
	shieldMeta.SetEnchantment("minecraft:mending", 1)
	SetSlotItemMeta(session, 45, shieldMeta)

	helmet := world.NewNamedItemStack("minecraft:diamond_helmet", 1)
	session.SetInventorySlot(5, helmet)
	var helmetMeta ItemMeta
	helmetMeta.Damage = 10
	helmetMeta.SetEnchantment("minecraft:mending", 1)
	SetSlotItemMeta(session, 5, helmetMeta)

	// Put a DAMAGED Mending pickaxe in non-equipped main inventory slot 15: must NOT be repaired!
	bagPick := world.NewNamedItemStack("minecraft:diamond_pickaxe", 1)
	session.SetInventorySlot(15, bagPick)
	var bagMeta ItemMeta
	bagMeta.Damage = 10
	bagMeta.SetEnchantment("minecraft:mending", 1)
	SetSlotItemMeta(session, 15, bagMeta)

	// Spawn 63 XP at player: 3 XP repairs OffHand shield, 5 XP repairs Helmet, 55 XP goes to bar (Level 5 = 55 XP!).
	srv.SpawnExperienceOrbs(0.5, 65.0, 0.5, 63)
	collected := srv.PickupNearbyExperienceOrbs(session)
	if collected != 63 {
		t.Fatalf("PickupNearbyExperienceOrbs = %d, want 63", collected)
	}
	if GetSlotItemMeta(session, 45).Damage != 0 {
		t.Fatalf("OffHand Mending shield damage = %d, want 0", GetSlotItemMeta(session, 45).Damage)
	}
	if GetSlotItemMeta(session, 5).Damage != 0 {
		t.Fatalf("Helmet Mending damage = %d, want 0", GetSlotItemMeta(session, 5).Damage)
	}
	if GetSlotItemMeta(session, 15).Damage != 10 {
		t.Fatalf("non-equipped slot 15 damage = %d, want 10 (untouched)", GetSlotItemMeta(session, 15).Damage)
	}
	if !sawSlotSync {
		t.Fatal("expected PlayContainerSetSlot when Mending repairs equipped items")
	}
	if session.TotalExperience() != 55 || session.ExperienceLevel() != 5 {
		t.Fatalf("after Mending overflow: level=%d totalXP=%d, want level=5 totalXP=55",
			session.ExperienceLevel(), session.TotalExperience())
	}
	if !sawLevelUpSound {
		t.Fatal("expected level-up chime sound when crossing level 5")
	}

	// Verify scoreboard "level" and "xp" criteria were synced automatically.
	if sc, ok := srv.GetScore("MendingKnight", "pl_lvl"); !ok || sc != 5 {
		t.Fatalf("scoreboard level = (%d, %v), want (5, true)", sc, ok)
	}
	if sc, ok := srv.GetScore("MendingKnight", "pl_xp"); !ok || sc != 55 {
		t.Fatalf("scoreboard xp = (%d, %v), want (55, true)", sc, ok)
	}

	// Verify UseExperienceBottle throws Bottle o' Enchanting and spawns XP orbs.
	bottle := world.NewNamedItemStack(ExperienceBottleItemID, 2)
	session.SetInventorySlot(hotbarSlot, bottle)
	rng := rand.New(rand.NewSource(42))
	if !srv.UseExperienceBottle(session, HandMain, rng) {
		t.Fatal("expected UseExperienceBottle to succeed")
	}
	if session.InventorySlot(hotbarSlot).Count != 1 {
		t.Fatalf("experience_bottle count after throw = %d, want 1", session.InventorySlot(hotbarSlot).Count)
	}
	if used := srv.GetPlayerStat(session, StatTypeUsed, ExperienceBottleItemID); used != 1 {
		t.Fatalf("StatTypeUsed(experience_bottle) = %d, want 1", used)
	}
}
