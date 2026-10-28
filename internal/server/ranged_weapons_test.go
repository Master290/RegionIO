package server

import (
	"math"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

func TestBowDrawCurveEnchantmentsAndArrowPickup(t *testing.T) {
	// 1. Verify vanilla BowPowerForTime curve.
	if got := BowPowerForTime(0); got != 0 {
		t.Fatalf("BowPowerForTime(0) = %v, want 0", got)
	}
	if got := BowPowerForTime(2); got != 0 {
		t.Fatalf("BowPowerForTime(2) = %v, want 0 (< 0.1 threshold)", got)
	}
	// At 10 ticks: f = 0.5 -> (0.25 + 1.0) / 3 = 1.25 / 3 = 0.4166667
	if got := BowPowerForTime(10); math.Abs(float64(got-float32(1.25/3.0))) > 1e-5 {
		t.Fatalf("BowPowerForTime(10) = %v, want ~0.41667", got)
	}
	if got := BowPowerForTime(20); got != 1.0 {
		t.Fatalf("BowPowerForTime(20) = %v, want 1.0", got)
	}
	if got := BowPowerForTime(45); got != 1.0 {
		t.Fatalf("BowPowerForTime(45) = %v, want clamped 1.0", got)
	}

	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	archer, err := srv.RegisterPlayer(Profile{Name: "Archer", UUID: OfflineUUID("Archer")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(archer, GameModeSurvival)
	feetY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(archer, 8.5, feetY, 8.5, 0, 0, true) // Looking +Z

	bowSlot := world.HotbarContainerOffset
	arrowSlot := world.HotbarContainerOffset + 1
	archer.SetInventorySlot(bowSlot, world.NewNamedItemStack(BowItemID, 1))
	archer.SetInventorySlot(arrowSlot, world.NewNamedItemStack("minecraft:arrow", 1))
	archer.SetSelectedSlot(0)

	// 2. Releasing bow too early (2 ticks -> power < 0.1) must abort without firing or consuming arrow.
	if !srv.StartDrawingRangedWeapon(archer, HandMain) {
		t.Fatal("expected StartDrawingRangedWeapon(bow) to succeed")
	}
	srv.TickRangedWeaponDraw(archer)
	srv.TickRangedWeaponDraw(archer)
	if srv.ReleaseProjectileWeapon(archer) {
		t.Fatal("expected ReleaseProjectileWeapon at 2 ticks to abort without shooting")
	}
	if got := archer.InventorySlot(arrowSlot).Count; got != 1 {
		t.Fatalf("expected arrow count to remain 1 after aborted draw, got %d", got)
	}

	// 3. Enchant bow with Power V, Punch II, Flame I, Infinity I and shoot a zombie at (8.5, feetY, 12.5).
	SetSlotItemMeta(archer, bowSlot, ItemMeta{
		Enchantments: map[string]int{
			"minecraft:power":    5,
			"minecraft:punch":    2,
			"minecraft:flame":    1,
			"minecraft:infinity": 1,
		},
	})
	zombieID := srv.Entities().Add(&world.Entity{
		TypeID:   registry.EntityTypeIndex(MobTypeZombie),
		TypeName: MobTypeZombie,
		X:        8.5,
		Y:        feetY,
		Z:        12.5,
		OnGround: true,
	})
	srv.SetMobHealth(zombieID, 40.0)

	srv.StartDrawingRangedWeapon(archer, HandMain)
	srv.SetWeaponDrawTicks(archer, 20)
	if !srv.ReleaseProjectileWeapon(archer) {
		t.Fatal("expected full-draw ReleaseProjectileWeapon(bow) to fire")
	}
	// Infinity preserves regular arrow!
	if got := archer.InventorySlot(arrowSlot).Count; got != 1 {
		t.Fatalf("expected Infinity bow to preserve regular arrow (count=1), got %d", got)
	}
	// Power V full charge: 6.0 + (5*1.5 + 0.5) = 14.0 damage -> zombie HP drops from 40 to 26.
	if gotHP := srv.MobHealth(zombieID); math.Abs(float64(gotHP-26.0)) > 1e-4 {
		t.Fatalf("zombie HP after Power V arrow = %v, want 26.0", gotHP)
	}
	// Flame I ignites the hit zombie for 100 ticks (5s).
	if !srv.IsEntityOnFire(zombieID) {
		t.Fatal("expected Flame I arrow to ignite target zombie")
	}
	// Punch II adds strong knockback (+Z velocity).
	zEnt := srv.Entities().Get(zombieID)
	if zEnt == nil || zEnt.VelocityZ <= clampVelocityShort(0.4) {
		t.Fatalf("expected Punch II arrow to apply amplified +Z knockback, got VelocityZ=%d", zEnt.VelocityZ)
	}
	if !archer.HasCompletedAdvancement("minecraft:adventure/shoot_arrow") {
		t.Fatal("expected hitting zombie with bow arrow to grant minecraft:adventure/shoot_arrow")
	}
	srv.Entities().Remove(zombieID)

	// 4. Tipped arrow (consumed even with Infinity!) & grounded arrow pickup when Infinity is removed.
	SetSlotItemMeta(archer, bowSlot, ItemMeta{}) // No Infinity -> arrows are PickupAllowed
	archer.SetInventorySlot(arrowSlot, world.NewNamedItemStack(TippedArrowItemID, 1))
	SetSlotPotionType(archer, arrowSlot, "minecraft:poison")

	// Place a stone wall at (8, surfaceY+2, 12) so the shot arrow embeds in the block at (8.5, feetY+1.5, 12.0).
	stoneState, _ := world.BlockStateByName("minecraft:stone", nil)
	if stoneState == 0 {
		stoneState = 1
	}
	srv.chunks.SetBlock(8, int(feetY)+1, 12, stoneState)

	arrowID, shotOK := srv.ShootBow(archer, HandMain, 20, nil)
	if !shotOK || arrowID <= 0 {
		t.Fatalf("expected ShootBow with tipped_arrow to succeed, got id=%d ok=%v", arrowID, shotOK)
	}
	if !archer.InventorySlot(arrowSlot).IsEmpty() {
		t.Fatalf("expected tipped_arrow to be consumed, got %+v", archer.InventorySlot(arrowSlot))
	}
	grounded, hasGrounded := srv.GroundedArrowByID(arrowID)
	if !hasGrounded || grounded.PickupRule != ArrowPickupAllowed || grounded.PotionType != "minecraft:poison" {
		t.Fatalf("expected embedded arrow to be registered with PickupAllowed and poison potion, got ok=%v st=%+v", hasGrounded, grounded)
	}

	// Move archer to the embedded arrow and pick it up.
	col.reset()
	srv.SetPlayerTransform(archer, grounded.X, grounded.Y-1.0, grounded.Z, 0, 0, true)
	picked := srv.TryPickupGroundedProjectiles(archer)
	if picked != 1 {
		t.Fatalf("expected TryPickupGroundedProjectiles to pick up 1 arrow, got %d", picked)
	}
	if col.count(protocol.PlayTakeItemEntity) == 0 {
		t.Fatal("expected PlayTakeItemEntity packet on grounded arrow pickup")
	}
}

func TestCrossbowQuickChargeMultishotPiercingAndUnload(t *testing.T) {
	// 1. Verify Quick Charge duration math (25 - 5*level).
	if got := CrossbowChargeDuration(0); got != 25 {
		t.Fatalf("CrossbowChargeDuration(0) = %d, want 25", got)
	}
	if got := CrossbowChargeDuration(1); got != 20 {
		t.Fatalf("CrossbowChargeDuration(1) = %d, want 20", got)
	}
	if got := CrossbowChargeDuration(2); got != 15 {
		t.Fatalf("CrossbowChargeDuration(2) = %d, want 15", got)
	}
	if got := CrossbowChargeDuration(3); got != 10 {
		t.Fatalf("CrossbowChargeDuration(3) = %d, want 10", got)
	}

	srv := newSurvivalTestServer(t)
	shooter, err := srv.RegisterPlayer(Profile{Name: "Arbalist", UUID: OfflineUUID("Arbalist")}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(shooter, GameModeSurvival)
	feetY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(shooter, 8.5, feetY, 8.5, 0, 0, true)

	cbSlot := world.HotbarContainerOffset
	arrowSlot := world.HotbarContainerOffset + 1
	shooter.SetInventorySlot(cbSlot, world.NewNamedItemStack(CrossbowItemID, 1))
	shooter.SetInventorySlot(arrowSlot, world.NewNamedItemStack("minecraft:arrow", 2))
	shooter.SetSelectedSlot(0)

	SetSlotItemMeta(shooter, cbSlot, ItemMeta{
		Enchantments: map[string]int{
			"minecraft:quick_charge": 2, // 15 ticks required
			"minecraft:multishot":    1,
			"minecraft:piercing":     2, // pierces up to 3 entities
		},
	})

	// 2. Releasing progressive crossbow charge early (10 ticks < 15 ticks) does NOT load or consume ammo.
	if !srv.StartDrawingRangedWeapon(shooter, HandMain) {
		t.Fatal("expected StartDrawingRangedWeapon(crossbow) to succeed")
	}
	for i := 0; i < 10; i++ {
		srv.TickRangedWeaponDraw(shooter)
	}
	if srv.ReleaseProjectileWeapon(shooter) {
		t.Fatal("expected early crossbow release at 10/15 ticks to abort")
	}
	if len(GetSlotChargedProjectiles(shooter, cbSlot)) != 0 || shooter.InventorySlot(arrowSlot).Count != 2 {
		t.Fatal("expected crossbow to remain uncharged and ammo unconsumed after early release")
	}

	// 3. Charging for the full 15 ticks (Quick Charge II) and releasing loads 3 arrows (Multishot) for 1 ammo.
	srv.StartDrawingRangedWeapon(shooter, HandMain)
	for i := 0; i < 15; i++ {
		srv.TickRangedWeaponDraw(shooter)
	}
	if !srv.ReleaseProjectileWeapon(shooter) {
		t.Fatal("expected crossbow release at 15/15 ticks to load projectiles")
	}
	if got := len(GetSlotChargedProjectiles(shooter, cbSlot)); got != 3 {
		t.Fatalf("expected 3 charged projectiles with Multishot, got %d", got)
	}
	if got := shooter.InventorySlot(arrowSlot).Count; got != 1 {
		t.Fatalf("expected 1 arrow consumed (1 remaining), got %d", got)
	}

	// 4. Verify ChargedProjectiles stream codec round-trip.
	pw := protocol.NewWriter(64)
	EncodeChargedProjectilesComponent(pw, GetSlotChargedProjectiles(shooter, cbSlot))
	decProjs, err := DecodeChargedProjectilesComponent(protocol.NewReader(pw.Bytes()))
	if err != nil || len(decProjs) != 3 {
		t.Fatalf("DecodeChargedProjectilesComponent: len=%d err=%v, want 3", len(decProjs), err)
	}

	// 5. Line up 3 zombies along +Z (at Z=10.5, 12.0, 13.5) and verify Piercing II hits all 3 in one shot!
	var zIDs [3]int32
	for i, zPos := range []float64{10.5, 12.0, 13.5} {
		id := srv.Entities().Add(&world.Entity{
			TypeID:   registry.EntityTypeIndex(MobTypeZombie),
			TypeName: MobTypeZombie,
			X:        8.5,
			Y:        feetY,
			Z:        zPos,
			OnGround: true,
		})
		srv.SetMobHealth(id, 20.0)
		zIDs[i] = id
	}

	ids, fired := srv.ShootCrossbow(shooter, HandMain)
	if !fired || len(ids) != 3 {
		t.Fatalf("expected ShootCrossbow to fire 3 arrows, got fired=%v ids=%v", fired, ids)
	}
	for i, id := range zIDs {
		if gotHP := srv.MobHealth(id); gotHP >= 20.0 {
			t.Fatalf("expected zombie[%d] (id=%d) to be pierced and damaged by Piercing II arrow, HP stayed %v", i, id, gotHP)
		}
	}
	if !shooter.HasCompletedAdvancement("minecraft:adventure/ol_betsy") {
		t.Fatal("expected ShootCrossbow to grant minecraft:adventure/ol_betsy")
	}
}

func TestTridentThrowLoyaltyRiptideChannelingAndImpaling(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	poseidon, err := srv.RegisterPlayer(Profile{Name: "Poseidon", UUID: OfflineUUID("Poseidon")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(poseidon, GameModeSurvival)
	feetY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(poseidon, 8.5, feetY, 8.5, 0, 0, true)

	tridentSlot := world.HotbarContainerOffset
	poseidon.SetInventorySlot(tridentSlot, world.NewNamedItemStack(TridentItemID, 1))
	poseidon.SetSelectedSlot(0)
	SetSlotItemMeta(poseidon, tridentSlot, ItemMeta{
		Damage: 5,
		Enchantments: map[string]int{
			"minecraft:loyalty":    3,
			"minecraft:impaling":   4, // +10.0 damage against aquatic mobs
			"minecraft:channeling": 1,
		},
	})

	// 1. Melee attack with Impaling IV trident against a Guardian (aquatic mob):
	// Base 9.0 + Impaling IV (4 * 2.5 = 10.0) = 19.0 damage!
	guardianID := srv.Entities().Add(&world.Entity{
		TypeID:   registry.EntityTypeIndex(MobTypeGuardian),
		TypeName: MobTypeGuardian,
		X:        8.5,
		Y:        feetY,
		Z:        10.5,
		OnGround: true,
	})
	srv.SetMobHealth(guardianID, 30.0)
	poseidon.SetAttackStrengthTicks(30)
	meleeDmg, _ := srv.AttackEntity(poseidon, guardianID)
	if math.Abs(float64(meleeDmg-19.0)) > 1e-4 {
		t.Fatalf("Impaling IV melee damage on Guardian = %v, want 19.0 (9.0 + 10.0)", meleeDmg)
	}
	srv.Entities().Remove(guardianID)

	// 2. Thrown trident with Loyalty III + Channeling I during a thunderstorm against a Villager:
	// Summons lightning, converts Villager -> Witch, grants throw_trident + very_very_frightening,
	// removes trident from hand on throw, and returns it via Loyalty III with preserved ItemMeta!
	srv.SetWeather(WeatherThunder, 6000) // Thundering in Overworld
	villagerID, err := srv.SpawnMobInDimension(world.DimensionOverworld, 8.5, feetY, 12.5, MobTypeVillager)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(villager): %v", err)
	}
	srv.SetMobHealth(villagerID, 20.0)

	tridentEntID, thrown := srv.ThrowTrident(poseidon, HandMain, TridentMinChargeTicks)
	if !thrown || tridentEntID <= 0 {
		t.Fatalf("expected ThrowTrident to succeed, got id=%d thrown=%v", tridentEntID, thrown)
	}
	if !poseidon.InventorySlot(tridentSlot).IsEmpty() {
		t.Fatalf("expected thrown trident to be removed from hand slot while in flight, got %+v", poseidon.InventorySlot(tridentSlot))
	}
	if !poseidon.HasCompletedAdvancement("minecraft:adventure/throw_trident") {
		t.Fatal("expected ThrowTrident hit to grant minecraft:adventure/throw_trident")
	}
	if !poseidon.HasCompletedAdvancement("minecraft:adventure/very_very_frightening") {
		t.Fatal("expected Channeling trident striking villager in thunderstorm to grant minecraft:adventure/very_very_frightening")
	}

	// Verify Villager converted into a Witch.
	foundWitch := false
	for _, e := range srv.Entities().All() {
		if canonicalResourceName(e.TypeName) == MobTypeWitch {
			foundWitch = true
			break
		}
	}
	if !foundWitch {
		t.Fatal("expected Channeling lightning strike on Villager to convert it into a Witch")
	}

	// Tick Loyalty III return until trident returns to poseidon's original slot.
	for i := 0; i < 15; i++ {
		srv.TickThrownTridents()
	}
	returnedStack := poseidon.InventorySlot(tridentSlot)
	if returnedStack.IsEmpty() || canonicalItemName(returnedStack.ResolvedID()) != TridentItemID {
		t.Fatalf("expected Loyalty III trident to return to slot %d, got %+v", tridentSlot, returnedStack)
	}
	returnedMeta := GetSlotItemMeta(poseidon, tridentSlot)
	if returnedMeta.Damage != 6 || returnedMeta.EnchantmentLevel("minecraft:loyalty") != 3 || returnedMeta.EnchantmentLevel("minecraft:impaling") != 4 {
		t.Fatalf("expected returned trident to preserve ItemMeta with Damage=6 (5+1), got %+v", returnedMeta)
	}

	// 3. Riptide III trident: cannot throw when dry, launches player when in water/rain!
	srv.SetWeather(WeatherClear, 6000) // Clear weather (dry)
	SetSlotItemMeta(poseidon, tridentSlot, ItemMeta{
		Enchantments: map[string]int{
			"minecraft:riptide": 3,
		},
	})
	if _, ok := srv.ThrowTrident(poseidon, HandMain, TridentMinChargeTicks); ok {
		t.Fatal("expected Riptide trident to fail when player is dry (not in water or rain)")
	}

	// Turn on rain -> CanUseRiptide becomes true and launches the player!
	srv.SetWeather(WeatherRain, 6000)
	col.reset()
	if _, ok := srv.ThrowTrident(poseidon, HandMain, TridentMinChargeTicks); !ok {
		t.Fatal("expected Riptide III trident to launch player when in rain")
	}
	if col.count(protocol.PlaySetEntityMotion) == 0 {
		t.Fatal("expected Riptide launch to send PlaySetEntityMotion to player")
	}
	if poseidon.InventorySlot(tridentSlot).IsEmpty() {
		t.Fatal("expected Riptide trident to stay in player's hand after launch")
	}
}

func TestShieldBlockAngleAxeDisableArrowDeflectionAndDurability(t *testing.T) {
	// 1. Verify 180-degree horizontal hemisphere check (player at (8.5, 8.5) looking +Z, yaw=0).
	if !CanShieldBlockFrom(8.5, 8.5, 0.0, 8.5, 11.5) {
		t.Fatal("expected shield to block source directly in front (+Z)")
	}
	if !CanShieldBlockFrom(8.5, 8.5, 0.0, 10.5, 9.5) {
		t.Fatal("expected shield to block source at front-left (+X, +Z)")
	}
	if CanShieldBlockFrom(8.5, 8.5, 0.0, 8.5, 5.5) {
		t.Fatal("expected shield NOT to block source directly behind (-Z)")
	}

	srv := newSurvivalTestServer(t)
	defCol := &survivalPacketCollector{}
	defender, err := srv.RegisterPlayer(Profile{Name: "Defender", UUID: OfflineUUID("Defender")}, defCol.send)
	if err != nil {
		t.Fatalf("RegisterPlayer(Defender): %v", err)
	}
	attacker, err := srv.RegisterPlayer(Profile{Name: "Attacker", UUID: OfflineUUID("Attacker")}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer(Attacker): %v", err)
	}
	_ = srv.SetPlayerGameMode(defender, GameModeSurvival)
	_ = srv.SetPlayerGameMode(attacker, GameModeSurvival)
	feetY := float64(world.FlatSurfaceY + 1)

	// Defender at (8.5, feetY, 8.5) looking +Z (yaw = 0); Attacker in front at (8.5, feetY, 10.5).
	srv.SetPlayerTransform(defender, 8.5, feetY, 8.5, 0, 0, true)
	srv.SetPlayerTransform(attacker, 8.5, feetY, 10.5, 180, 0, true)

	defender.SetInventorySlot(OffHandContainerSlot, world.NewNamedItemStack(ShieldItemID, 1))
	SetSlotItemMeta(defender, OffHandContainerSlot, ItemMeta{Damage: 0})
	if !srv.UseShield(defender, HandOffHand) || !defender.IsBlockingWithShield() {
		t.Fatal("expected defender to start blocking with offhand shield")
	}

	// 2. Front melee hit with iron_sword (6.0 damage >= 3.0 threshold):
	// Blocked completely (0 HP lost), shield takes 1 + floor(6.0) = 7 durability wear!
	attacker.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:iron_sword", 1))
	attacker.SetSelectedSlot(0)
	attacker.SetAttackStrengthTicks(30)

	dealt, _ := srv.AttackEntity(attacker, defender.EntityID)
	if dealt != 0 || defender.Health() != 20.0 {
		t.Fatalf("expected front sword hit to be blocked by shield (dealt=%v, hp=%v)", dealt, defender.Health())
	}
	if gotWear := GetSlotItemMeta(defender, OffHandContainerSlot).Damage; gotWear != 7 {
		t.Fatalf("expected shield durability damage = 7 (1 + floor(6.0)), got %d", gotWear)
	}

	// 3. Attack from behind (Attacker moves to Z=6.5, behind Defender who is still looking +Z):
	// Bypasses the shield and deals damage to Defender!
	srv.SetPlayerTransform(attacker, 8.5, feetY, 6.5, 0, 0, true)
	attacker.SetAttackStrengthTicks(30)
	dealtBack, _ := srv.AttackEntity(attacker, defender.EntityID)
	if dealtBack <= 0 || defender.Health() >= 20.0 {
		t.Fatalf("expected attack from behind to bypass shield (dealt=%v, hp=%v)", dealtBack, defender.Health())
	}
	srv.HealPlayer(defender, 20.0)

	// 4. Front arrow vs Piercing arrow against Defender's shield:
	srv.SetPlayerTransform(attacker, 8.5, feetY, 12.5, 180, 0, true)
	cbSlot := world.HotbarContainerOffset
	arrowSlot := world.HotbarContainerOffset + 1
	attacker.SetInventorySlot(cbSlot, world.NewNamedItemStack(CrossbowItemID, 1))
	attacker.SetInventorySlot(arrowSlot, world.NewNamedItemStack("minecraft:arrow", 4))

	// 4a. Non-piercing crossbow arrow from front is deflected by shield!
	srv.LoadCrossbowProjectile(attacker, HandMain)
	srv.ShootCrossbow(attacker, HandMain)
	if defender.Health() != 20.0 {
		t.Fatalf("expected non-piercing arrow from front to be deflected by shield, HP=%v", defender.Health())
	}
	if !defender.HasCompletedAdvancement("minecraft:story/deflect_arrow") {
		t.Fatal("expected deflecting arrow with shield to grant minecraft:story/deflect_arrow")
	}

	// 4b. Piercing I crossbow arrow from front pierces through Defender's shield!
	SetSlotItemMeta(attacker, cbSlot, ItemMeta{
		Enchantments: map[string]int{"minecraft:piercing": 1},
	})
	srv.LoadCrossbowProjectile(attacker, HandMain)
	srv.ShootCrossbow(attacker, HandMain)
	if defender.Health() >= 20.0 {
		t.Fatalf("expected Piercing I arrow to bypass shield and damage defender, HP stayed %v", defender.Health())
	}
	srv.HealPlayer(defender, 20.0)

	// 5. Front melee hit with diamond_axe disables Defender's shield for 100 ticks (5s) and sends PlayCooldown + EntityEvent(30)!
	srv.SetPlayerTransform(attacker, 8.5, feetY, 10.5, 180, 0, true)
	attacker.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:diamond_axe", 1))
	attacker.SetAttackStrengthTicks(30)
	defCol.reset()

	dealtAxe, _ := srv.AttackEntity(attacker, defender.EntityID)
	if dealtAxe != 0 {
		t.Fatalf("expected shield to absorb the axe hit that disables it, got dealt=%v", dealtAxe)
	}
	if defender.IsBlockingWithShield() || !srv.IsPlayerShieldDisabled(defender) || !defender.IsItemOnCooldown(ShieldItemID) {
		t.Fatal("expected diamond_axe hit to disable defender's shield and place it on 100-tick cooldown")
	}
	if defCol.count(protocol.PlayCooldown) == 0 {
		t.Fatal("expected PlayCooldown packet sent to defender on axe shield disable")
	}
	if defCol.count(protocol.PlayEntityEvent) == 0 {
		t.Fatal("expected PlayEntityEvent(EntityEventShieldDisabled=30) on axe shield disable")
	}
	if srv.UseShield(defender, HandOffHand) {
		t.Fatal("expected UseShield to fail while shield is disabled on cooldown")
	}
}
