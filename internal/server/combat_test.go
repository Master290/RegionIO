package server

import (
	"math"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

func TestWeaponAttackStatsAndCooldownScaling(t *testing.T) {
	cases := []struct {
		item      string
		wantDmg   float32
		wantSpeed float32
		wantDelay float32
	}{
		{"", 1.0, 4.0, 5.0},
		{"minecraft:wooden_sword", 4.0, 1.6, 12.5},
		{"minecraft:copper_sword", 5.0, 1.6, 12.5},
		{"minecraft:iron_sword", 6.0, 1.6, 12.5},
		{"minecraft:diamond_sword", 7.0, 1.6, 12.5},
		{"minecraft:netherite_sword", 8.0, 1.6, 12.5},
		{"minecraft:wooden_axe", 7.0, 0.8, 25.0},
		{"minecraft:iron_axe", 9.0, 0.9, float32(20.0 / 0.9)},
		{"minecraft:diamond_axe", 9.0, 1.0, 20.0},
		{"minecraft:netherite_axe", 10.0, 1.0, 20.0},
		{"minecraft:trident", 9.0, 1.1, float32(20.0 / 1.1)},
		{"minecraft:mace", 6.0, 0.6, float32(20.0 / 0.6)},
	}

	for _, tc := range cases {
		dmg, spd := ItemAttackStats(tc.item)
		if math.Abs(float64(dmg-tc.wantDmg)) > 1e-4 {
			t.Errorf("%q: damage = %v, want %v", tc.item, dmg, tc.wantDmg)
		}
		if math.Abs(float64(spd-tc.wantSpeed)) > 1e-4 {
			t.Errorf("%q: speed = %v, want %v", tc.item, spd, tc.wantSpeed)
		}
		delay := AttackCooldownDelayTicks(spd)
		if math.Abs(float64(delay-tc.wantDelay)) > 1e-3 {
			t.Errorf("%q: cooldown delay = %v, want %v", tc.item, delay, tc.wantDelay)
		}
	}

	// Verify vanilla 0.2 + scale^2 * 0.8 multiplier curve.
	if got := AttackDamageMultiplier(0); math.Abs(float64(got-0.2)) > 1e-5 {
		t.Fatalf("AttackDamageMultiplier(0) = %v, want 0.2", got)
	}
	if got := AttackDamageMultiplier(0.5); math.Abs(float64(got-0.4)) > 1e-5 {
		t.Fatalf("AttackDamageMultiplier(0.5) = %v, want 0.4", got)
	}
	if got := AttackDamageMultiplier(1.0); math.Abs(float64(got-1.0)) > 1e-5 {
		t.Fatalf("AttackDamageMultiplier(1.0) = %v, want 1.0", got)
	}
}

func TestMeleeAttackOnPigAndZombieCooldownKnockbackAndDrops(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "Fighter", UUID: OfflineUUID("Fighter")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(player, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	feetY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(player, 8.5, feetY, 8.5, 0, 0, true)

	// Equip a diamond sword (7.0 attack damage, 1.6 attack speed -> 12.5 tick cooldown).
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:diamond_sword", 1))
	player.SetSelectedSlot(0)

	pigID := srv.Entities().Add(&world.Entity{
		TypeID:   registry.EntityTypeIndex(MobTypePig),
		TypeName: MobTypePig,
		X:        8.5,
		Y:        feetY,
		Z:        10.5, // 2 blocks +Z from player
		OnGround: true,
	})
	srv.BroadcastEntities()
	col.reset()

	// 1. First hit at full cooldown (scale = 1.0): deals 7.0 damage to the 10 HP pig -> 3.0 HP left.
	pkt := EncodeAttackPacket(pigID)
	if err := srv.HandleAttackPacket(player, protocol.NewReader(pkt)); err != nil {
		t.Fatalf("HandleAttackPacket: %v", err)
	}
	if gotHP := srv.MobHealth(pigID); math.Abs(float64(gotHP-3.0)) > 1e-4 {
		t.Fatalf("pig health after first hit = %v, want 3.0", gotHP)
	}
	if col.count(protocol.PlayHurtAnimation) == 0 {
		t.Fatal("expected PlayHurtAnimation on melee hit")
	}
	if col.count(protocol.PlayDamageEvent) == 0 {
		t.Fatal("expected PlayDamageEvent on melee hit")
	}
	if col.count(protocol.PlaySetEntityMotion) == 0 {
		t.Fatal("expected PlaySetEntityMotion on melee hit")
	}
	if col.count(PlaySoundPacketID) == 0 {
		t.Fatal("expected PlaySoundPacketID on melee hit")
	}

	// Pig should have received knockback away from the player (+Z direction).
	pigEnt := srv.Entities().Get(pigID)
	if pigEnt == nil || pigEnt.VelocityZ <= 0 {
		t.Fatalf("expected pig VelocityZ > 0 from knockback, got %+v", pigEnt)
	}

	// 2. Immediate spam-click (0 ticks elapsed -> scale = 0.5 / 12.5 = 0.04):
	// multiplier = 0.2 + 0.0016 * 0.8 = 0.20128 -> ~1.409 damage, pig survives (~1.59 HP).
	if err := srv.HandleInteractPacket(player, protocol.NewReader(EncodeInteractAttackPacket(pigID, false))); err != nil {
		t.Fatalf("HandleInteractPacket: %v", err)
	}
	hpAfterSpam := srv.MobHealth(pigID)
	dmg2 := 3.0 - hpAfterSpam
	if dmg2 >= 2.0 || dmg2 < 1.4 {
		t.Fatalf("spam-click damage = %v (remaining HP=%v), want cooldown-reduced ~1.41", dmg2, hpAfterSpam)
	}

	// 3. Advance 13 ticks so attack cooldown fully recharges, then land the killing blow.
	for i := 0; i < 13; i++ {
		srv.TickPlayerHunger(player)
	}
	col.reset()
	if err := srv.HandleAttackPacket(player, protocol.NewReader(pkt)); err != nil {
		t.Fatalf("third HandleAttackPacket: %v", err)
	}
	if srv.Entities().Get(pigID) != nil {
		t.Fatal("expected dead pig entity to be removed from world")
	}
	if col.count(protocol.PlayEntityEvent) == 0 {
		t.Fatal("expected PlayEntityEvent(3) death status packet on mob death")
	}

	// Verify that a minecraft:porkchop item entity was dropped where the pig died.
	var foundPorkchop bool
	for _, e := range srv.Entities().All() {
		if e.IsItem() && e.Item.ResolvedID() == "minecraft:porkchop" && e.Item.Count >= 1 {
			foundPorkchop = true
			break
		}
	}
	if !foundPorkchop {
		t.Fatalf("expected pig death to drop minecraft:porkchop; entities = %+v", srv.Entities().All())
	}

	// 4. Spawn a zombie (20 HP) and kill it with 3 full-strength diamond_sword hits (3 * 7.0 = 21.0 >= 20.0); verify rotten_flesh drop.
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:diamond_sword", 1))
	zombieID := srv.Entities().Add(&world.Entity{
		TypeID:   registry.EntityTypeIndex(MobTypeZombie),
		TypeName: MobTypeZombie,
		X:        8.5,
		Y:        feetY,
		Z:        10.5,
		OnGround: true,
	})
	for hit := 0; hit < 3; hit++ {
		player.SetAttackStrengthTicks(30)
		_, killedZ := srv.AttackEntity(player, zombieID)
		if hit < 2 && killedZ {
			t.Fatalf("zombie died too early on hit %d", hit+1)
		}
		if hit == 2 && !killedZ {
			t.Fatalf("expected zombie to die on 3rd full diamond_sword hit (remaining HP=%v)", srv.MobHealth(zombieID))
		}
	}

	var foundRottenFlesh bool
	for _, e := range srv.Entities().All() {
		if e.IsItem() && e.Item.ResolvedID() == "minecraft:rotten_flesh" && e.Item.Count >= 1 {
			foundRottenFlesh = true
			break
		}
	}
	if !foundRottenFlesh {
		t.Fatalf("expected zombie death to drop minecraft:rotten_flesh; entities = %+v", srv.Entities().All())
	}
}

func TestCriticalHitAndSprintKnockback(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "Critter", UUID: OfflineUUID("Critter")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(player, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	feetY := float64(world.FlatSurfaceY + 1)
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:iron_sword", 1)) // 6.0 base damage
	player.SetSelectedSlot(0)

	// Simulate player falling (airborne after descending from feetY+2 to feetY+1).
	srv.SetPlayerTransform(player, 8.5, feetY+2.0, 8.5, 0, 0, false)
	srv.SetPlayerTransform(player, 8.5, feetY+1.0, 8.5, 0, 0, false)
	srv.HandlePlayerFall(player, feetY+2.0, feetY+1.0, false, false)
	player.SetAttackStrengthTicks(20)

	pigID := srv.Entities().Add(&world.Entity{
		TypeID:   registry.EntityTypeIndex(MobTypePig),
		TypeName: MobTypePig,
		X:        8.5,
		Y:        feetY,
		Z:        10.5,
		OnGround: true,
	})
	srv.BroadcastEntities()
	col.reset()

	// Critical hit: 6.0 * 1.5 = 9.0 damage, plus PlayAnimate(AnimationCriticalHit).
	dmg, killed := srv.AttackEntity(player, pigID)
	if killed || math.Abs(float64(dmg-9.0)) > 1e-4 {
		t.Fatalf("critical hit: dmg=%v, killed=%v, want dmg=9.0, killed=false", dmg, killed)
	}
	if col.count(PlayAnimateCB) == 0 {
		t.Fatal("expected PlayAnimateCB packet for critical hit particles")
	}
}

func TestArmorReductionOnPlayerAndZombieMelee(t *testing.T) {
	// 1. Verify vanilla CombatRules.getDamageAfterAbsorb math.
	// Full diamond armor: 20 defense, 8 toughness. Against 3.0 damage:
	// f = 2.0 + 8.0/4.0 = 4.0; reduction = clamp(20 - 3/4, 4, 20) = 19.25;
	// after = 3.0 * (1 - 19.25/25) = 3.0 * 0.23 = 0.69.
	reduced := DamageAfterArmor(3.0, 20.0, 8.0)
	if math.Abs(float64(reduced-0.69)) > 1e-3 {
		t.Fatalf("DamageAfterArmor(3.0, 20, 8) = %v, want 0.69", reduced)
	}

	// 2. Equip full diamond armor on a player and verify zombie hit deals 0.69 instead of 3.0.
	srv := newSurvivalTestServer(t)
	player, err := srv.RegisterPlayer(Profile{Name: "Tank", UUID: OfflineUUID("Tank")}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(player, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	feetY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(player, 8.5, feetY, 8.5, 0, 0, true)

	player.SetInventorySlot(ArmorSlotHead, world.NewNamedItemStack("minecraft:diamond_helmet", 1))
	player.SetInventorySlot(ArmorSlotChest, world.NewNamedItemStack("minecraft:diamond_chestplate", 1))
	player.SetInventorySlot(ArmorSlotLegs, world.NewNamedItemStack("minecraft:diamond_leggings", 1))
	player.SetInventorySlot(ArmorSlotFeet, world.NewNamedItemStack("minecraft:diamond_boots", 1))

	armor, toughness := player.ArmorDefense()
	if armor != 20.0 || toughness != 8.0 {
		t.Fatalf("ArmorDefense() = (%v, %v), want (20.0, 8.0)", armor, toughness)
	}

	dealt := srv.AttackPlayerFromMob(100, MobTypeZombie, 8.5, 9.5, player, ZombieAttackDamage)
	if math.Abs(float64(dealt-0.69)) > 1e-3 {
		t.Fatalf("zombie attack dealt %v to full-diamond player, want 0.69", dealt)
	}
	wantHP := float32(20.0) - dealt
	if math.Abs(float64(player.Health()-wantHP)) > 1e-3 {
		t.Fatalf("player health = %v, want %v", player.Health(), wantHP)
	}
}
