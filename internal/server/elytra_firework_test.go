package server

import (
	"math"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func newTestElytraServer(t *testing.T) *Server {
	t.Helper()
	cfg := DefaultConfig()
	cfg.WorldDir = ""
	cache := world.NewCache(256, func(cx, cz int32) *world.Chunk {
		return &world.Chunk{X: cx, Z: cz}
	})
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache server: %v", err)
	}
	return srv
}

func addElytraTestPlayer(t *testing.T, srv *Server, name string, idByte byte, x, y, z float64) *PlayerSession {
	t.Helper()
	p, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{idByte, 2, 3, 4},
		Name: name,
	}, func(id int32, body []byte) error {
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer(%s): %v", name, err)
	}
	srv.SetPlayerGameMode(p, GameModeSurvival)
	srv.SetPlayerPosition(p, x, y, z)
	return p
}

func TestElytraStartFallFlyingAndConditions(t *testing.T) {
	srv := newTestElytraServer(t)
	player := addElytraTestPlayer(t, srv, "Glider", 1, 0.5, 80.0, 0.5)

	// Put player in the air.
	player.mu.Lock()
	player.onGround = false
	player.mu.Unlock()

	// Without an Elytra in ArmorSlotChest (6), cannot start fall-flying.
	if srv.CanPlayerStartFallFlying(player) {
		t.Fatalf("expected CanPlayerStartFallFlying=false without chest Elytra")
	}

	// Equip an unbroken Elytra in ArmorSlotChest (slot 6).
	elytraStack := world.NewNamedItemStack(ElytraItemID, 1)
	player.SetInventorySlot(ArmorSlotChest, elytraStack)
	SetSlotItemMeta(player, ArmorSlotChest, ItemMeta{Damage: 0})

	if !srv.CanPlayerStartFallFlying(player) {
		t.Fatalf("expected CanPlayerStartFallFlying=true with unbroken chest Elytra in air")
	}

	// Trigger via ServerboundPlayerCommandPacket with START_FALL_FLYING (action 6).
	w := protocol.NewWriter(12)
	w.VarInt(player.EntityID)
	w.VarInt(PlayerCommandStartFallFlying)
	w.VarInt(0)
	if err := srv.HandlePlayerCommandPacket(player, protocol.NewReader(w.Bytes())); err != nil {
		t.Fatalf("HandlePlayerCommandPacket error: %v", err)
	}
	if !player.IsFallFlying() {
		t.Fatalf("expected player to be fall-flying after START_FALL_FLYING packet")
	}
	if flags := ComputePlayerSharedFlags(player); flags&EntitySharedFlagFallFlying == 0 {
		t.Fatalf("expected EntitySharedFlagFallFlying (0x80) set in flags, got 0x%02x", flags)
	}

	// Levitation effect must prevent / stop fall-flying.
	srv.StopPlayerFallFlying(player)
	srv.ApplyPlayerEffect(player, NewStatusEffectInstance(EffectLevitation, 100, 0))
	if srv.CanPlayerStartFallFlying(player) {
		t.Fatalf("expected CanPlayerStartFallFlying=false while Levitating")
	}
	srv.RemovePlayerEffect(player, EffectLevitation)

	// Broken Elytra (Damage == 431 = MaxElytraDurability - 1) cannot start fall-flying.
	SetSlotItemMeta(player, ArmorSlotChest, ItemMeta{Damage: ElytraUsableMaxDamage})
	if srv.CanPlayerStartFallFlying(player) {
		t.Fatalf("expected CanPlayerStartFallFlying=false when Elytra Damage == %d", ElytraUsableMaxDamage)
	}
}

func TestElytraGlidePhysicsAndWallCollision(t *testing.T) {
	// 1. Pitching down (pitch = +30 deg) should accelerate horizontal speed while descending.
	vx0, vy0, vz0 := 0.0, -0.2, 0.2
	vx1, vy1, vz1 := StepElytraGlideVelocity(vx0, vy0, vz0, 0.0, 30.0)
	if math.Hypot(vx1, vz1) <= math.Hypot(vx0, vz0)*0.95 {
		t.Fatalf("expected horizontal glide speed to be sustained/accelerated when diving, got (%f, %f, %f)", vx1, vy1, vz1)
	}

	// 2. Pitching up (pitch = -35 deg) with high horizontal speed should convert horizontal speed into upward lift.
	uvx, uvy, uvz := StepElytraGlideVelocity(0.0, -0.05, 1.4, 0.0, -35.0)
	if uvy <= -0.05 {
		t.Fatalf("expected upward pitch with fast horizontal speed to produce upward lift, got vy=%f (from -0.05)", uvy)
	}
	if uvz >= 1.4 {
		t.Fatalf("expected upward climb to trade off horizontal speed, got vz=%f (from 1.4, vx=%f)", uvz, uvx)
	}

	// 3. Kinetic wall collision damage ("fly_into_wall").
	srv := newTestElytraServer(t)
	player := addElytraTestPlayer(t, srv, "WallFlier", 2, 0.5, 70.0, 0.5)
	player.mu.Lock()
	player.onGround = false
	player.mu.Unlock()

	player.SetInventorySlot(ArmorSlotChest, world.NewNamedItemStack(ElytraItemID, 1))
	SetSlotItemMeta(player, ArmorSlotChest, ItemMeta{Damage: 0})
	if !srv.StartPlayerFallFlying(player) {
		t.Fatalf("failed to start fall-flying")
	}
	player.SetElytraVelocity(0.0, -0.05, 0.8)

	// Place a stone wall directly ahead at (0, 70, 1) and (0, 71, 1).
	stoneState, _ := world.BlockStateByName("minecraft:stone", nil)
	if stoneState == 0 {
		stoneState = 1
	}
	srv.chunks.SetBlock(0, 70, 1, stoneState)
	srv.chunks.SetBlock(0, 71, 1, stoneState)

	startHP := player.Health()
	wallDmg, _ := srv.HandlePlayerElytraMove(player, 0.5, 70.0, 0.5, 0.5, 69.95, 0.5, false, false)
	if wallDmg < 7.5 || wallDmg > 8.5 {
		t.Fatalf("expected ~8.0 kinetic wall damage (0.8 * 10), got %f", wallDmg)
	}
	if player.Health() >= startHP {
		t.Fatalf("expected player health to decrease after wall collision (before=%f, after=%f)", startHP, player.Health())
	}

	// Verify death message translation for fly_into_wall.
	msg := srv.FormatDeathMessage(player, "fly_into_wall", "")
	if got := msg.PlainText(); got != "WallFlier experienced kinetic energy" {
		t.Fatalf("unexpected fly_into_wall death message: %q", got)
	}
}

func TestElytraDurabilityCapAndMending(t *testing.T) {
	srv := newTestElytraServer(t)
	player := addElytraTestPlayer(t, srv, "WingWearer", 3, 0.5, 90.0, 0.5)
	player.mu.Lock()
	player.onGround = false
	player.mu.Unlock()

	player.SetInventorySlot(ArmorSlotChest, world.NewNamedItemStack(ElytraItemID, 1))
	// Start at Damage = 430 (1 point away from broken threshold 431).
	SetSlotItemMeta(player, ArmorSlotChest, ItemMeta{
		Damage: 430,
		Enchantments: map[string]int{
			"minecraft:mending": 1,
		},
	})

	if !srv.StartPlayerFallFlying(player) {
		t.Fatalf("expected Elytra with Damage=430 (< 431) to start fall-flying")
	}

	// Tick 20 flight ticks -> 1 durability wear -> Damage reaches 431 (ElytraUsableMaxDamage).
	for i := 0; i < ElytraDurabilityTickInterval; i++ {
		srv.TickPlayerElytra(player)
	}

	metaAfter := GetSlotItemMeta(player, ArmorSlotChest)
	if metaAfter.Damage != ElytraUsableMaxDamage {
		t.Fatalf("expected Elytra damage to cap at %d, got %d", ElytraUsableMaxDamage, metaAfter.Damage)
	}
	if player.InventorySlot(ArmorSlotChest).IsEmpty() {
		t.Fatalf("expected broken Elytra to remain in chest slot (not vanish)")
	}
	if player.IsFallFlying() {
		t.Fatalf("expected fall-flying to stop when Elytra reaches broken state (Damage=431)")
	}

	// Repair via Mending: 10 XP points -> 20 durability repaired (431 -> 411).
	repaired, remXP := srv.RepairPlayerElytraWithMending(player, 10)
	if repaired != 20 || remXP != 0 {
		t.Fatalf("expected 20 durability repaired and 0 remaining XP, got repaired=%d remXP=%d", repaired, remXP)
	}
	metaRepaired := GetSlotItemMeta(player, ArmorSlotChest)
	if metaRepaired.Damage != 411 {
		t.Fatalf("expected Elytra damage 411 after Mending repair, got %d", metaRepaired.Damage)
	}
	if !HasUsableChestElytra(player) {
		t.Fatalf("expected repaired Elytra to be usable again")
	}
	if !IsValidRepairMaterial(ElytraItemID, ElytraRepairMaterialID) {
		t.Fatalf("expected phantom_membrane to be valid anvil repair material for elytra")
	}
}

func TestFireworkStarFadeAndRocketCraftingAndCodecs(t *testing.T) {
	// 1. Craft a Creeper-shaped Firework Star with Red + Lime dyes, Trail (diamond), and Twinkle (glowstone_dust).
	starIngs := []world.ItemStack{
		world.NewNamedItemStack("minecraft:gunpowder", 1),
		world.NewNamedItemStack("minecraft:red_dye", 1),
		world.NewNamedItemStack("minecraft:lime_dye", 1),
		world.NewNamedItemStack("minecraft:creeper_head", 1),
		world.NewNamedItemStack("minecraft:diamond", 1),
		world.NewNamedItemStack("minecraft:glowstone_dust", 1),
	}
	starStack, starExp, ok := MatchFireworkStarRecipe(starIngs)
	if !ok || starStack.IsEmpty() || starStack.Count != 1 {
		t.Fatalf("expected MatchFireworkStarRecipe to succeed, got ok=%v stack=%+v", ok, starStack)
	}
	if starExp.Shape != FireworkShapeCreeper || !starExp.HasTrail || !starExp.HasTwinkle || len(starExp.Colors) != 2 {
		t.Fatalf("unexpected FireworkExplosion: %+v", starExp)
	}

	// 2. Add Blue + White fade colors via MatchFireworkStarFadeRecipe.
	fadeIngs := []world.ItemStack{
		starStack,
		world.NewNamedItemStack("minecraft:blue_dye", 1),
		world.NewNamedItemStack("minecraft:white_dye", 1),
	}
	fadedStack, fadedExp, ok := MatchFireworkStarFadeRecipe(fadeIngs, []FireworkExplosion{starExp})
	if !ok || fadedStack.IsEmpty() || len(fadedExp.FadeColors) != 2 {
		t.Fatalf("expected MatchFireworkStarFadeRecipe to succeed with 2 fade colors, got ok=%v exp=%+v", ok, fadedExp)
	}
	if fadedExp.Shape != FireworkShapeCreeper || len(fadedExp.Colors) != 2 {
		t.Fatalf("expected original star properties preserved after fade recipe, got %+v", fadedExp)
	}

	// 3. Craft 3x Firework Rockets with FlightDuration=2 (2x gunpowder + 1x paper + 1x faded star).
	rocketIngs := []world.ItemStack{
		world.NewNamedItemStack("minecraft:paper", 1),
		world.NewNamedItemStack("minecraft:gunpowder", 1),
		world.NewNamedItemStack("minecraft:gunpowder", 1),
		fadedStack,
	}
	rocketInputs := make([]FireworkExplosion, 4)
	rocketInputs[3] = fadedExp
	rocketStack, fwComp, ok := MatchFireworkRocketRecipe(rocketIngs, rocketInputs)
	if !ok || rocketStack.Count != 3 || fwComp.FlightDuration != 2 || len(fwComp.Explosions) != 1 {
		t.Fatalf("expected 3 firework rockets with duration 2 and 1 explosion, got ok=%v count=%d fw=%+v",
			ok, rocketStack.Count, fwComp)
	}

	// 4. Round-trip stream codec for FireworksComponent & NBT serialization.
	pw := protocol.NewWriter(64)
	EncodeFireworksComponent(pw, fwComp)
	decoded, err := DecodeFireworksComponent(protocol.NewReader(pw.Bytes()))
	if err != nil {
		t.Fatalf("DecodeFireworksComponent error: %v", err)
	}
	if decoded.FlightDuration != 2 || len(decoded.Explosions) != 1 ||
		decoded.Explosions[0].Shape != FireworkShapeCreeper ||
		len(decoded.Explosions[0].FadeColors) != 2 ||
		!decoded.Explosions[0].HasTrail || !decoded.Explosions[0].HasTwinkle {
		t.Fatalf("round-trip decoded FireworksComponent mismatch: %+v", decoded)
	}

	nbtTag := WriteFireworksNBT(fwComp)
	fromNBT, ok := ReadFireworksNBT(nbtTag)
	if !ok || fromNBT.FlightDuration != 2 || len(fromNBT.Explosions) != 1 {
		t.Fatalf("NBT round-trip mismatch: %+v", fromNBT)
	}
}

func TestFireworkElytraBoostAndStarExplosionDamage(t *testing.T) {
	srv := newTestElytraServer(t)
	flier := addElytraTestPlayer(t, srv, "RocketFlier", 4, 0.5, 85.0, 0.5)
	bystander := addElytraTestPlayer(t, srv, "NearbyObserver", 5, 2.5, 85.0, 0.5)

	flier.mu.Lock()
	flier.onGround = false
	flier.yaw = 0.0
	flier.pitch = 0.0
	flier.mu.Unlock()

	flier.SetInventorySlot(ArmorSlotChest, world.NewNamedItemStack(ElytraItemID, 1))
	SetSlotItemMeta(flier, ArmorSlotChest, ItemMeta{Damage: 0})
	if !srv.StartPlayerFallFlying(flier) {
		t.Fatalf("failed to start fall-flying")
	}
	flier.SetElytraVelocity(0.0, -0.05, 0.2)

	// Put 2 firework rockets with 2 explosion stars in flier's main hand (slot 36).
	rocketSlot := world.HotbarContainerOffset + int(flier.SelectedSlot())
	flier.SetInventorySlot(rocketSlot, world.NewNamedItemStack(FireworkRocketItemID, 2))
	SetSlotFireworks(flier, rocketSlot, FireworksComponent{
		FlightDuration: 1,
		Explosions: []FireworkExplosion{
			{Shape: FireworkShapeLargeBall, Colors: []int32{0xB3312C}},
			{Shape: FireworkShapeStar, Colors: []int32{0xDECF2A}, HasTwinkle: true},
		},
	})

	rocketID, boosted := srv.UseFireworkRocketInAir(flier, HandMain)
	if !boosted || rocketID == 0 {
		t.Fatalf("expected UseFireworkRocketInAir to boost fall-flying player and spawn rocket")
	}
	if got := flier.InventorySlot(rocketSlot).Count; got != 1 {
		t.Fatalf("expected 1 rocket consumed in survival mode (remaining=1), got %d", got)
	}

	_, _, vzBoosted := flier.ElytraVelocity()
	if vzBoosted <= 0.6 {
		t.Fatalf("expected firework boost to accelerate forward Z velocity > 0.6, got %f", vzBoosted)
	}

	// Tick rocket once, then detonate it and verify both attached flier and nearby bystander take damage.
	srv.TickFireworkRockets()
	flierBefore := flier.Health()
	bystanderBefore := bystander.Health()

	pHit, _ := srv.DetonateFireworkRocket(rocketID)
	if pHit < 2 {
		t.Fatalf("expected both attached flier and nearby bystander to be hit by star explosion, got playersHit=%d", pHit)
	}
	if flier.Health() >= flierBefore {
		t.Fatalf("expected attached flier to take firework explosion damage (5 + 2*2 = 9), health stayed %f", flier.Health())
	}
	if bystander.Health() >= bystanderBefore {
		t.Fatalf("expected nearby bystander within 5 blocks to take radial firework damage, health stayed %f", bystander.Health())
	}
}

func TestCrossbowFireworkLoadAndShoot(t *testing.T) {
	srv := newTestElytraServer(t)
	shooter := addElytraTestPlayer(t, srv, "CrossbowMarksman", 6, 0.5, 70.0, 0.5)

	mainSlot := world.HotbarContainerOffset + int(shooter.SelectedSlot())
	shooter.SetInventorySlot(mainSlot, world.NewNamedItemStack(CrossbowItemID, 1))
	SetSlotItemMeta(shooter, mainSlot, ItemMeta{
		Damage: 0,
		Enchantments: map[string]int{
			"minecraft:multishot": 1,
		},
	})

	// Put a firework rocket in offhand (slot 45).
	shooter.SetInventorySlot(OffHandContainerSlot, world.NewNamedItemStack(FireworkRocketItemID, 2))
	SetSlotFireworks(shooter, OffHandContainerSlot, FireworksComponent{
		FlightDuration: 2,
		Explosions: []FireworkExplosion{
			{Shape: FireworkShapeBurst, Colors: []int32{0x253192}},
		},
	})

	// First UseCrossbow loads the firework rocket from offhand (replicated 3x by Multishot).
	if !srv.UseCrossbow(shooter, HandMain) {
		t.Fatalf("expected first UseCrossbow to load firework rocket from offhand")
	}
	if rem := shooter.InventorySlot(OffHandContainerSlot).Count; rem != 1 {
		t.Fatalf("expected 1 firework rocket consumed from offhand, got %d remaining", rem)
	}
	charged := GetSlotChargedProjectiles(shooter, mainSlot)
	if len(charged) != 3 {
		t.Fatalf("expected Multishot crossbow to hold 3 charged firework rockets, got %d", len(charged))
	}

	// Second UseCrossbow shoots the 3 angled firework rockets and applies 3 durability wear.
	ids, fired := srv.ShootCrossbow(shooter, HandMain)
	if !fired || len(ids) != 3 {
		t.Fatalf("expected ShootCrossbow to spawn 3 angled firework rockets, got fired=%v ids=%v", fired, ids)
	}
	for _, id := range ids {
		st, ok := srv.FireworkRocketStateByID(id)
		if !ok || !st.ShotAtAngle {
			t.Fatalf("expected spawned crossbow firework %d to have ShotAtAngle=true, got ok=%v st=%+v", id, ok, st)
		}
	}
	cbMeta := GetSlotItemMeta(shooter, mainSlot)
	if cbMeta.Damage != CrossbowFireworkDurabilityCost {
		t.Fatalf("expected crossbow to take %d durability damage when firing fireworks, got %d",
			CrossbowFireworkDurabilityCost, cbMeta.Damage)
	}
}

func TestElytraEquipSwapPoseVehicleAndMendingOrbPickup(t *testing.T) {
	srv := newTestElytraServer(t)
	player := addElytraTestPlayer(t, srv, "ElytraAce", 7, 0.5, 100.0, 0.5)
	srv.SetPlayerTransform(player, 0.5, 100.0, 0.5, 0, 0, false)

	mainSlot := world.HotbarContainerOffset + int(player.SelectedSlot())
	player.SetInventorySlot(mainSlot, world.NewNamedItemStack(ElytraItemID, 1))
	SetSlotItemMeta(player, mainSlot, ItemMeta{
		Damage: 120,
		Enchantments: map[string]int{
			"minecraft:mending":    1,
			"minecraft:unbreaking": 3,
		},
	})

	// 1. Right-clicking in air with Elytra equips it into ArmorSlotChest (6).
	if !srv.UseEquippableArmorOrElytra(player, HandMain) {
		t.Fatalf("expected UseEquippableArmorOrElytra to equip Elytra into slot %d", ArmorSlotChest)
	}
	if !IsElytraItem(player.InventorySlot(ArmorSlotChest).ResolvedID()) {
		t.Fatalf("expected Elytra in ArmorSlotChest, got %+v", player.InventorySlot(ArmorSlotChest))
	}
	if !player.InventorySlot(mainSlot).IsEmpty() {
		t.Fatalf("expected main hand slot to be empty after equipping Elytra, got %+v", player.InventorySlot(mainSlot))
	}
	chestMeta := GetSlotItemMeta(player, ArmorSlotChest)
	if chestMeta.Damage != 120 || chestMeta.EnchantmentLevel("minecraft:mending") != 1 {
		t.Fatalf("expected equipped Elytra meta preserved, got %+v", chestMeta)
	}

	// 2. Right-clicking with a chestplate swaps with the equipped Elytra, unless Curse of Binding is present.
	player.SetInventorySlot(mainSlot, world.NewNamedItemStack("minecraft:diamond_chestplate", 1))
	SetSlotItemMeta(player, ArmorSlotChest, ItemMeta{
		Damage: 120,
		Enchantments: map[string]int{
			"minecraft:binding_curse": 1,
		},
	})
	if srv.UseEquippableArmorOrElytra(player, HandMain) {
		t.Fatalf("expected Curse of Binding on chest slot to block right-click swap in Survival")
	}
	// Remove binding_curse and verify swap works.
	SetSlotItemMeta(player, ArmorSlotChest, chestMeta)
	if !srv.UseEquippableArmorOrElytra(player, HandMain) {
		t.Fatalf("expected chestplate right-click to swap with Elytra")
	}
	if !IsElytraItem(player.InventorySlot(mainSlot).ResolvedID()) {
		t.Fatalf("expected swapped Elytra in main hand, got %+v", player.InventorySlot(mainSlot))
	}
	// Swap Elytra back into ArmorSlotChest.
	if !srv.UseEquippableArmorOrElytra(player, HandMain) {
		t.Fatalf("expected Elytra right-click to swap back into ArmorSlotChest")
	}

	// 3. Start fall-flying and verify PoseFallFlying + EntitySharedFlagFallFlying metadata packet.
	if !srv.StartPlayerFallFlying(player) {
		t.Fatalf("expected StartPlayerFallFlying to succeed")
	}
	flagsPkt := srv.PlayerSharedFlagsAndPosePacketByEntityID(player.EntityID)
	if len(flagsPkt) == 0 {
		t.Fatalf("expected non-empty PlayerSharedFlagsAndPosePacketByEntityID while fall-flying")
	}
	decEntID, decFlags, decPose, err := DecodePlayerSharedFlagsAndPoseDataPacket(protocol.NewReader(flagsPkt))
	if err != nil || decEntID != player.EntityID || decFlags&EntitySharedFlagFallFlying == 0 || decPose != PoseFallFlying {
		t.Fatalf("decoded flags/pose = (id=%d, flags=0x%02x, pose=%d, err=%v), want flag 0x80 and PoseFallFlying=%d",
			decEntID, decFlags, decPose, err, PoseFallFlying)
	}

	// 4. Mounting a vehicle stops fall-flying and prevents starting fall-flying while mounted.
	boatID, boatEnt := srv.SpawnVehicle(world.DimensionOverworld, EntityTypeOakBoat, 0.5, 100.0, 0.5, 0)
	if boatID == 0 || boatEnt == nil {
		t.Fatalf("expected SpawnVehicle to succeed")
	}
	if handled, _, err := srv.InteractWithVehicle(player, boatID); err != nil || !handled {
		t.Fatalf("expected InteractWithVehicle to succeed: handled=%v err=%v", handled, err)
	}
	srv.TickPlayerElytra(player)
	if player.IsFallFlying() {
		t.Fatalf("expected TickPlayerElytra to stop fall-flying when mounted in a vehicle")
	}
	if srv.CanPlayerStartFallFlying(player) {
		t.Fatalf("expected CanPlayerStartFallFlying=false while mounted in a vehicle")
	}
	srv.DismountPlayerVehicle(player)

	// 5. Damage Elytra to broken texture state (431/432) and verify Mending XP orb pickup restores usability.
	SetSlotItemMeta(player, ArmorSlotChest, ItemMeta{
		Damage: ElytraUsableMaxDamage,
		Enchantments: map[string]int{
			"minecraft:mending": 1,
		},
	})
	if HasUsableChestElytra(player) {
		t.Fatalf("expected broken Elytra (431/432) to be unusable")
	}
	// Put diamond_chestplate away so only the broken Mending Elytra is a Mending candidate.
	player.SetInventorySlot(mainSlot, world.ItemStack{})
	if orbID, orbEnt := srv.SpawnExperienceOrb(0.5, 100.0, 0.5, 10); orbID == 0 || orbEnt == nil {
		t.Fatalf("expected SpawnExperienceOrb to succeed")
	}
	pickedXP := srv.PickupNearbyExperienceOrbs(player)
	if pickedXP != 10 {
		t.Fatalf("expected player to pick up 10 XP from orb, got %d", pickedXP)
	}
	repairedMeta := GetSlotItemMeta(player, ArmorSlotChest)
	if repairedMeta.Damage != ElytraUsableMaxDamage-20 {
		t.Fatalf("expected 10 XP orb to repair 20 durability on broken Elytra (431 -> 411), got %d", repairedMeta.Damage)
	}
	if !HasUsableChestElytra(player) {
		t.Fatalf("expected Elytra repaired by Mending orb pickup to be usable again")
	}
}
