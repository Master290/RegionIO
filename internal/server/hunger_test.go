package server

import (
	"math"
	"testing"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestVanilla2612FoodValues(t *testing.T) {
	cases := []struct {
		item        string
		nutrition   int32
		satMod      float32
		wantSat     float32
		consumeTick int
		alwaysEat   bool
		convertsTo  string
	}{
		{"minecraft:apple", 4, 0.3, 2.4, DefaultConsumeTicks, false, ""},
		{"minecraft:bread", 5, 0.6, 6.0, DefaultConsumeTicks, false, ""},
		{"minecraft:cooked_porkchop", 8, 0.8, 12.8, DefaultConsumeTicks, false, ""},
		{"minecraft:cooked_beef", 8, 0.8, 12.8, DefaultConsumeTicks, false, ""},
		{"minecraft:golden_carrot", 6, 1.2, 14.4, DefaultConsumeTicks, false, ""},
		{"minecraft:golden_apple", 4, 1.2, 9.6, DefaultConsumeTicks, true, ""},
		{"minecraft:enchanted_golden_apple", 4, 1.2, 9.6, DefaultConsumeTicks, true, ""},
		{"minecraft:dried_kelp", 1, 0.3, 0.6, DriedKelpConsumeTicks, false, ""},
		{"minecraft:honey_bottle", 6, 0.1, 1.2, HoneyBottleConsumeTicks, true, "minecraft:glass_bottle"},
		{"minecraft:mushroom_stew", 6, 0.6, 7.2, DefaultConsumeTicks, false, "minecraft:bowl"},
		{"minecraft:rotten_flesh", 4, 0.1, 0.8, DefaultConsumeTicks, false, ""},
		{"minecraft:porkchop", 3, 0.3, 1.8, DefaultConsumeTicks, false, ""},
	}

	for _, tc := range cases {
		props, ok := FoodPropertiesForItem(tc.item)
		if !ok {
			t.Fatalf("FoodPropertiesForItem(%q) not found", tc.item)
		}
		if props.Nutrition != tc.nutrition {
			t.Errorf("%s: Nutrition = %d, want %d", tc.item, props.Nutrition, tc.nutrition)
		}
		if math.Abs(float64(props.SaturationModifier-tc.satMod)) > 1e-5 {
			t.Errorf("%s: SaturationModifier = %v, want %v", tc.item, props.SaturationModifier, tc.satMod)
		}
		if math.Abs(float64(props.Saturation-tc.wantSat)) > 1e-4 {
			t.Errorf("%s: Saturation = %v, want %v", tc.item, props.Saturation, tc.wantSat)
		}
		if props.ConsumeTicks != tc.consumeTick {
			t.Errorf("%s: ConsumeTicks = %d, want %d", tc.item, props.ConsumeTicks, tc.consumeTick)
		}
		if props.AlwaysEdible != tc.alwaysEat {
			t.Errorf("%s: AlwaysEdible = %v, want %v", tc.item, props.AlwaysEdible, tc.alwaysEat)
		}
		if props.ConvertRemainder != tc.convertsTo {
			t.Errorf("%s: ConvertRemainder = %q, want %q", tc.item, props.ConvertRemainder, tc.convertsTo)
		}
	}

	if IsFoodItem("minecraft:diamond_sword") {
		t.Fatal("diamond_sword should not be classified as a food item")
	}
}

func TestExhaustionSourcesAndDrain(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "Runner", UUID: OfflineUUID("Runner")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(player, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	srv.SetPlayerHealth(player, 20.0, 20, 2.0)
	srv.SetPlayerTransform(player, 0, 65, 0, 0, 0, true)
	col.reset()

	// 1. Sprinting 10 meters horizontally adds 10 * 0.1 = 1.0 exhaustion.
	player.SetSprinting(true)
	srv.RecordPlayerMovementExhaustion(player, 0, 65, 0, 10, 65, 0, true, true)
	if got := player.FoodExhaustion(); math.Abs(float64(got-1.0)) > 1e-4 {
		t.Fatalf("exhaustion after 10m sprint = %v, want 1.0", got)
	}

	// 2. Sprint-jumping (onGround true -> false with dy > 0 while sprinting) adds 0.2 exhaustion.
	srv.RecordPlayerMovementExhaustion(player, 10, 65, 0, 10, 65.5, 0, true, false)
	if got := player.FoodExhaustion(); math.Abs(float64(got-1.2)) > 1e-4 {
		t.Fatalf("exhaustion after sprint jump = %v, want 1.2", got)
	}

	// 3. Regular jump (not sprinting) adds 0.05 exhaustion.
	player.SetSprinting(false)
	srv.RecordPlayerMovementExhaustion(player, 10, 65, 0, 10, 65.5, 0, true, false)
	if got := player.FoodExhaustion(); math.Abs(float64(got-1.25)) > 1e-4 {
		t.Fatalf("exhaustion after normal jump = %v, want 1.25", got)
	}

	// 4. Attacking (+0.1), taking damage (+0.1), and breaking a block (+0.005) -> 1.455.
	srv.RecordAttackExhaustion(player)
	srv.RecordDamageExhaustion(player)
	srv.RecordBlockBreakExhaustion(player)
	if got := player.FoodExhaustion(); math.Abs(float64(got-1.455)) > 1e-4 {
		t.Fatalf("exhaustion after attack/damage/mine = %v, want 1.455", got)
	}

	// 5. Add 3.0 more exhaustion -> 4.455 (> 4.0). Tick drains 4.0 exhaustion and 1.0 saturation.
	srv.AddExhaustion(player, 3.0)
	srv.TickPlayerHunger(player)
	if got := player.FoodExhaustion(); math.Abs(float64(got-0.455)) > 1e-4 {
		t.Fatalf("exhaustion after drain = %v, want 0.455", got)
	}
	if got := player.FoodSaturation(); math.Abs(float64(got-1.0)) > 1e-4 {
		t.Fatalf("saturation after drain = %v, want 1.0", got)
	}
	if got := player.FoodLevel(); got != 20 {
		t.Fatalf("food level = %d, want 20 (saturation should drain first)", got)
	}

	// 6. Drain remaining 1.0 saturation, then drain 1 food point (20 -> 19) and verify PlaySetHealth.
	srv.AddExhaustion(player, 8.0) // total 8.455 -> drains twice (4.0 each)
	col.reset()
	srv.TickPlayerHunger(player) // drains saturation 1.0 -> 0.0
	srv.TickPlayerHunger(player) // drains food 20 -> 19
	if got := player.FoodSaturation(); got != 0 {
		t.Fatalf("saturation = %v, want 0", got)
	}
	if got := player.FoodLevel(); got != 19 {
		t.Fatalf("food level = %d, want 19", got)
	}
	if col.count(protocol.PlaySetHealth) == 0 {
		t.Fatal("expected PlaySetHealth packet when food level decreases")
	}
}

func TestFastSaturatedRegenerationAtFullFood(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "Saturated", UUID: OfflineUUID("Saturated")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(player, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	// Hurt player (14 HP) with full food (20) and 6.0 saturation.
	srv.SetPlayerHealth(player, 14.0, 20, 6.0)
	col.reset()

	// Ticks 1..9: no heal yet.
	for i := 0; i < int(HealthTickCountSaturated)-1; i++ {
		srv.TickPlayerHunger(player)
		if got := player.Health(); got != 14.0 {
			t.Fatalf("tick %d: health = %v, want 14.0", i+1, got)
		}
	}

	// Tick 10: heals min(6.0, 6.0)/6.0 = 1.0 HP (14 -> 15) and adds 6.0 exhaustion.
	srv.TickPlayerHunger(player)
	if got := player.Health(); math.Abs(float64(got-15.0)) > 1e-4 {
		t.Fatalf("tick 10: health = %v, want 15.0", got)
	}
	if got := player.FoodExhaustion(); math.Abs(float64(got-6.0)) > 1e-4 {
		t.Fatalf("tick 10: exhaustion = %v, want 6.0", got)
	}
	if col.count(protocol.PlaySetHealth) == 0 {
		t.Fatal("expected PlaySetHealth on fast saturated regeneration")
	}

	// Next tick drains 4.0 exhaustion from the 6.0, reducing saturation from 6.0 to 5.0.
	srv.TickPlayerHunger(player)
	if got := player.FoodSaturation(); math.Abs(float64(got-5.0)) > 1e-4 {
		t.Fatalf("saturation after fast regen exhaustion drain = %v, want 5.0", got)
	}
}

func TestNaturalRegenerationAtFood18And19(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "NaturalRegen", UUID: OfflineUUID("NaturalRegen")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(player, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	srv.SetPlayerHealth(player, 16.0, 18, 2.0)
	col.reset()

	// Ticks 1..79: no heal yet.
	for i := 0; i < int(HealthTickCountNormal)-1; i++ {
		srv.TickPlayerHunger(player)
		if got := player.Health(); got != 16.0 {
			t.Fatalf("tick %d: health = %v, want 16.0", i+1, got)
		}
	}

	// Tick 80: heals 1.0 HP (16 -> 17) and adds 6.0 exhaustion.
	srv.TickPlayerHunger(player)
	if got := player.Health(); got != 17.0 {
		t.Fatalf("tick 80: health = %v, want 17.0", got)
	}
	if got := player.FoodExhaustion(); math.Abs(float64(got-ExhaustionHeal)) > 1e-4 {
		t.Fatalf("tick 80: exhaustion = %v, want %v", got, ExhaustionHeal)
	}
	if col.count(protocol.PlaySetHealth) == 0 {
		t.Fatal("expected PlaySetHealth on natural regeneration")
	}

	// Disabling naturalRegeneration prevents further healing.
	srv.SetNaturalRegeneration(false)
	for i := 0; i < int(HealthTickCountNormal)*2; i++ {
		srv.TickPlayerHunger(player)
	}
	if got := player.Health(); got != 17.0 {
		t.Fatalf("health with naturalRegeneration=false = %v, want 17.0", got)
	}
}

func TestStarvationDamageRespectsDifficulty(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "Starving", UUID: OfflineUUID("Starving")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(player, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}

	// 1. Easy difficulty: starvation stops at 10.0 HP.
	srv.SetDifficulty(DifficultyEasy)
	srv.SetPlayerHealth(player, 11.0, 0, 0)
	for i := 0; i < int(HealthTickCountNormal); i++ {
		srv.TickPlayerHunger(player)
	}
	if got := player.Health(); got != 10.0 {
		t.Fatalf("Easy starvation from 11 HP: got %v, want 10.0", got)
	}
	for i := 0; i < int(HealthTickCountNormal)*2; i++ {
		srv.TickPlayerHunger(player)
	}
	if got := player.Health(); got != 10.0 {
		t.Fatalf("Easy starvation floor: got %v, want 10.0", got)
	}

	// 2. Normal difficulty: starvation stops at 1.0 HP.
	srv.SetDifficulty(DifficultyNormal)
	srv.SetPlayerHealth(player, 2.0, 0, 0)
	player.SetFoodTickTimer(0)
	for i := 0; i < int(HealthTickCountNormal); i++ {
		srv.TickPlayerHunger(player)
	}
	if got := player.Health(); got != 1.0 {
		t.Fatalf("Normal starvation from 2 HP: got %v, want 1.0", got)
	}
	for i := 0; i < int(HealthTickCountNormal)*2; i++ {
		srv.TickPlayerHunger(player)
	}
	if got := player.Health(); got != 1.0 || player.IsDead() {
		t.Fatalf("Normal starvation floor: health=%v, isDead=%v, want 1.0 and alive", got, player.IsDead())
	}

	// 3. Hard difficulty: starvation kills the player at 0 HP.
	srv.SetDifficulty(DifficultyHard)
	col.reset()
	player.SetFoodTickTimer(0)
	for i := 0; i < int(HealthTickCountNormal); i++ {
		srv.TickPlayerHunger(player)
	}
	if got := player.Health(); got != 0 || !player.IsDead() {
		t.Fatalf("Hard starvation from 1 HP: health=%v, isDead=%v, want 0 and dead", got, player.IsDead())
	}
	if col.count(protocol.PlayPlayerCombatKill) != 1 {
		t.Fatalf("expected PlayPlayerCombatKill on Hard starvation death, got %d", col.count(protocol.PlayPlayerCombatKill))
	}
}

func TestEatingFoodItemUseItemDurationAndConsumption(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "Eater", UUID: OfflineUUID("Eater")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(player, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	srv.SetPlayerHealth(player, 20.0, 10, 0.0)

	// Place 3 cooked_beef in hotbar slot 0 (container slot 36).
	steak := world.NewNamedItemStack("minecraft:cooked_beef", 3)
	player.SetInventorySlot(world.HotbarContainerOffset, steak)
	player.SetSelectedSlot(0)
	col.reset()

	// Build a ServerboundUseItemPacket for main hand (hand=0).
	w := protocol.NewWriter(16)
	w.VarInt(HandMain)
	w.VarInt(1) // sequence
	w.Float32(0)
	w.Float32(0)
	if err := srv.HandleUseItemPacket(player, protocol.NewReader(w.Bytes())); err != nil {
		t.Fatalf("HandleUseItemPacket: %v", err)
	}
	if !player.IsEating() || player.EatingTicksRemaining() != DefaultConsumeTicks {
		t.Fatalf("expected player to start eating for %d ticks, got eating=%v remaining=%d",
			DefaultConsumeTicks, player.IsEating(), player.EatingTicksRemaining())
	}

	// Tick 31 times: still eating, item not consumed yet.
	for i := 0; i < DefaultConsumeTicks-1; i++ {
		srv.TickPlayerHunger(player)
	}
	if !player.IsEating() {
		t.Fatal("player finished eating 1 tick too early")
	}
	if got := player.HeldItem().Count; got != 3 {
		t.Fatalf("held item count before completion = %d, want 3", got)
	}

	// Tick 32: finishes eating, restores +8 food (10 -> 18) and +12.8 saturation, consumes 1 steak (3 -> 2).
	srv.TickPlayerHunger(player)
	if player.IsEating() {
		t.Fatal("expected eating to finish on tick 32")
	}
	if got := player.FoodLevel(); got != 18 {
		t.Fatalf("food level after eating cooked_beef = %d, want 18", got)
	}
	if got := player.FoodSaturation(); math.Abs(float64(got-12.8)) > 1e-4 {
		t.Fatalf("saturation after eating cooked_beef = %v, want 12.8", got)
	}
	if got := player.HeldItem().Count; got != 2 {
		t.Fatalf("held item count after eating = %d, want 2", got)
	}
	if col.count(protocol.PlayEntityEvent) == 0 {
		t.Fatal("expected PlayEntityEvent(9) upon finishing eating")
	}
	if col.count(protocol.PlaySetHealth) == 0 {
		t.Fatal("expected PlaySetHealth upon finishing eating")
	}
	if col.count(protocol.PlayContainerSetSlot) == 0 {
		t.Fatal("expected PlayContainerSetSlot upon consuming food item")
	}

	// Eating mushroom_stew (count=1) converts the slot to minecraft:bowl.
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:mushroom_stew", 1))
	if !srv.EatHeldItem(player) {
		t.Fatal("EatHeldItem failed for mushroom_stew")
	}
	if got := player.HeldItem(); got.ResolvedID() != "minecraft:bowl" || got.Count != 1 {
		t.Fatalf("slot after eating mushroom_stew = %+v, want 1x minecraft:bowl", got)
	}

	// At full food (20), regular apple cannot be eaten, but golden_apple can.
	srv.SetPlayerHealth(player, 20.0, 20, 5.0)
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:apple", 1))
	if srv.StartEating(player, HandMain) {
		t.Fatal("should not be able to start eating regular apple at food=20")
	}
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:golden_apple", 1))
	if !srv.StartEating(player, HandMain) {
		t.Fatal("expected golden_apple (AlwaysEdible) to be edible at food=20")
	}
}

func TestHungerNBTRoundTrip(t *testing.T) {
	srv := newSurvivalTestServer(t)
	player, err := srv.RegisterPlayer(Profile{Name: "Persist", UUID: OfflineUUID("Persist")}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerHealth(player, 18.0, 15, 7.5)
	player.SetFoodExhaustion(2.75)
	player.SetFoodTickTimer(42)

	root := nbt.NewCompound()
	player.WriteHungerNBT(root)

	restored, err := srv.RegisterPlayer(Profile{Name: "Restored", UUID: OfflineUUID("Restored")}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	restored.ReadHungerNBT(root)

	if restored.FoodLevel() != 15 {
		t.Fatalf("restored FoodLevel = %d, want 15", restored.FoodLevel())
	}
	if math.Abs(float64(restored.FoodSaturation()-7.5)) > 1e-4 {
		t.Fatalf("restored FoodSaturation = %v, want 7.5", restored.FoodSaturation())
	}
	if math.Abs(float64(restored.FoodExhaustion()-2.75)) > 1e-4 {
		t.Fatalf("restored FoodExhaustion = %v, want 2.75", restored.FoodExhaustion())
	}
	if restored.FoodTickTimer() != 42 {
		t.Fatalf("restored FoodTickTimer = %d, want 42", restored.FoodTickTimer())
	}
}

func TestAdvancementTriggersAndEffectModifiersInCombatHungerClockTrading(t *testing.T) {
	srv := newSurvivalTestServer(t)
	player, err := srv.RegisterPlayer(Profile{Name: "Hero", UUID: OfflineUUID("Hero")}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(player, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	surfaceY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(player, 8.5, surfaceY, 8.5, 0, 0, true)

	// 1. Eating golden_apple triggers husbandry/root and grants Regeneration + Absorption;
	//    eating honey_bottle removes Poison.
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:golden_apple", 1))
	if !srv.EatHeldItem(player) {
		t.Fatal("EatHeldItem(golden_apple) failed")
	}
	if !player.HasCompletedAdvancement("minecraft:husbandry/root") {
		t.Fatal("expected eating food to complete minecraft:husbandry/root")
	}
	if !player.HasPlayerEffect(EffectRegeneration) || player.AbsorptionHealth() < 4.0 {
		t.Fatalf("expected golden_apple to grant Regeneration and Absorption, got regen=%v abs=%v",
			player.HasPlayerEffect(EffectRegeneration), player.AbsorptionHealth())
	}
	srv.ApplyPlayerEffect(player, NewStatusEffectInstance(EffectPoison, 100, 0))
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:honey_bottle", 1))
	if !srv.EatHeldItem(player) {
		t.Fatal("EatHeldItem(honey_bottle) failed")
	}
	if player.HasPlayerEffect(EffectPoison) {
		t.Fatal("expected honey_bottle to remove Poison")
	}

	// 2. Hunger effect ticks via TickPlayerHunger (delegating to TickPlayerEffects).
	srv.ClearPlayerEffects(player)
	player.SetFoodExhaustion(0)
	srv.ApplyPlayerEffect(player, NewStatusEffectInstance(EffectHunger, 20, 1)) // +0.01 exhaustion/tick
	if !HasActiveHungerOrHealthEffects(player) || math.Abs(float64(ActiveHungerExhaustionPerTick(player)-0.01)) > 1e-5 {
		t.Fatalf("unexpected ActiveHungerExhaustionPerTick = %v", ActiveHungerExhaustionPerTick(player))
	}
	srv.TickPlayerHunger(player)
	if got := player.FoodExhaustion(); math.Abs(float64(got-0.01)) > 1e-5 {
		t.Fatalf("exhaustion after 1 tick of Hunger II = %v, want 0.01", got)
	}

	// 3. Strength I (+3 melee bonus) in AttackEntity and TriggerEntityKilled on mob death.
	srv.ClearPlayerEffects(player)
	srv.ApplyPlayerEffect(player, NewStatusEffectInstance(EffectStrength, 200, 0))
	player.SetInventorySlot(world.HotbarContainerOffset, world.ItemStack{})
	zombieID, err := srv.SpawnMob(9.5, surfaceY, 8.5, MobTypeZombie)
	if err != nil {
		t.Fatalf("SpawnMob(zombie): %v", err)
	}
	srv.SetMobHealth(zombieID, 3.5)
	dealt, killed := srv.AttackEntity(player, zombieID)
	if !killed || math.Abs(float64(dealt-4.0)) > 1e-4 {
		t.Fatalf("AttackEntity with Strength I: dealt=%v killed=%v, want dealt=4.0 killed=true", dealt, killed)
	}
	if !player.HasCompletedAdvancement("minecraft:adventure/root") ||
		!player.HasCompletedAdvancement("minecraft:adventure/kill_a_mob") {
		t.Fatal("expected killing zombie to complete adventure/root and adventure/kill_a_mob")
	}

	// 4. Fire Resistance blocks fire damage in DamagePlayerWithArmor; Resistance I reduces damage by 20%.
	srv.ApplyPlayerEffect(player, NewStatusEffectInstance(EffectFireResistance, 200, 0))
	if got := srv.DamagePlayerWithArmor(player, 5.0, "lava"); got != 0 {
		t.Fatalf("DamagePlayerWithArmor(lava) with Fire Resistance = %v, want 0", got)
	}
	srv.ApplyPlayerEffect(player, NewStatusEffectInstance(EffectResistance, 200, 0))
	if got := srv.DamagePlayerWithArmor(player, 10.0, "zombie"); math.Abs(float64(got-8.0)) > 1e-4 {
		t.Fatalf("DamagePlayerWithArmor with Resistance I = %v, want 8.0", got)
	}

	// 5. Sleeping in bed at night fires TriggerSleptInBed.
	srv.SetWorldTime(13000, 13000)
	slept, _ := srv.SleepInBed(player, 8, world.FlatSurfaceY+1, 8)
	if !slept || !player.HasCompletedAdvancement("minecraft:adventure/sleep_in_bed") {
		t.Fatalf("expected SleepInBed to complete minecraft:adventure/sleep_in_bed (slept=%v)", slept)
	}
	srv.SetPlayerSleeping(player, false)

	// 6. Shield projectile deflection in combat.go fires TriggerDeflectedProjectile (minecraft:story/deflect_arrow).
	player.SetInventorySlot(45, world.NewNamedItemStack("minecraft:shield", 1))
	if !srv.UseShield(player, 1) || !player.IsBlockingWithShield() {
		t.Fatal("expected UseShield to activate shield blocking")
	}
	arrowDmg := srv.ShootSkeletonArrowAtPlayer(100, 12.5, surfaceY, 8.5, player)
	if arrowDmg != 0 {
		t.Fatalf("expected shield to block skeleton arrow damage, got %v", arrowDmg)
	}
	if !player.HasCompletedAdvancement("minecraft:story/deflect_arrow") {
		t.Fatal("expected blocking skeleton arrow with shield to complete minecraft:story/deflect_arrow")
	}
	player.SetBlockingWithShield(false)

	// 7. finishEating calls TriggerConsumeItem (balanced_diet criterion).
	srv.SetPlayerHealth(player, 20.0, 10, 0.0)
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:bread", 1))
	if !srv.StartEating(player, HandMain) || !srv.finishEating(player) {
		t.Fatal("expected StartEating + finishEating(bread) to succeed")
	}
	if prog, ok := player.AdvancementProgressFor("minecraft:husbandry/balanced_diet"); !ok || !prog.Criteria["bread"].IsDone() {
		t.Fatal("expected finishEating(bread) to grant balanced_diet criterion 'bread'")
	}
}
