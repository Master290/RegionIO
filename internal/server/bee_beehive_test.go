package server

import (
	"math/rand"
	"path/filepath"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func newBeeTestPlayer(t *testing.T, srv *Server, name string, x, y, z float64) *PlayerSession {
	t.Helper()
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: name, UUID: OfflineUUID(name)}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer(%q): %v", name, err)
	}
	srv.SetPlayerTransform(player, x, y, z, 0, 0, true)
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	return player
}

func TestBeeSpawnAndMetadata(t *testing.T) {
	srv := newSurvivalTestServer(t)
	if got := ResolveEntityTypeID(MobTypeBee); got != EntityTypeBeeID {
		t.Fatalf("ResolveEntityTypeID(%q) = %d, want %d", MobTypeBee, got, EntityTypeBeeID)
	}
	beeID, err := srv.SpawnMobInDimension(world.DimensionOverworld, 10.5, 65.0, 10.5, MobTypeBee)
	if err != nil || beeID <= 0 {
		t.Fatalf("SpawnMobInDimension(bee) failed: id=%d, err=%v", beeID, err)
	}
	if hp := srv.MobHealth(beeID); hp != DefaultBeeMaxHealth {
		t.Fatalf("bee initial health = %v, want %v", hp, DefaultBeeMaxHealth)
	}
	srv.SetBeeHasNectar(beeID, true)
	st, ok := srv.BeeStateFor(beeID)
	if !ok || !st.HasNectar {
		t.Fatalf("expected BeeState.HasNectar=true, got %+v (ok=%v)", st, ok)
	}
	pkt := EncodeBeeDataPacket(beeID, false, BeeState{
		IsRolling:  true,
		HasStung:   false,
		HasNectar:  true,
		AngerTicks: 500,
	})
	if len(pkt) == 0 {
		t.Fatalf("EncodeBeeDataPacket returned empty slice")
	}
}

func TestBeeFlowerPollinationAndCropGrowth(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)

	flowerState, ok := world.BlockStateIDByProperties("minecraft:dandelion", nil)
	if !ok {
		t.Fatalf("dandelion block state not found")
	}
	cache.SetBlock(4, 65, 4, flowerState)

	beeID, err := srv.SpawnBee(dim, 4.5, 65.5, 4.5, false)
	if err != nil {
		t.Fatalf("SpawnBee failed: %v", err)
	}
	rng := rand.New(rand.NewSource(42))

	// Tick 400 times over the flower to complete pollination.
	for i := 0; i < BeePollinateTargetTicks+2; i++ {
		ent := srv.entities.Get(beeID)
		srv.TickBeeAI(ent, cache, nil, rng)
		st, _ := srv.BeeStateFor(beeID)
		if st.HasNectar {
			break
		}
	}
	st, _ := srv.BeeStateFor(beeID)
	if !st.HasNectar {
		t.Fatalf("expected bee to acquire nectar after %d ticks, got %+v", BeePollinateTargetTicks, st)
	}

	// Place a wheat crop at age=0 below the bee and verify fertilization advances age +1 up to 10 times.
	wheat0, ok := world.BlockStateIDByProperties("minecraft:wheat", map[string]string{"age": "0"})
	if !ok {
		t.Fatalf("wheat[age=0] state not found")
	}
	cache.SetBlock(4, 64, 4, wheat0)
	if !srv.TryFertilizeCropUnderBee(beeID) {
		t.Fatalf("expected TryFertilizeCropUnderBee to fertilize wheat[age=0]")
	}
	afterProps := world.BlockProperties(cache.GetBlock(4, 64, 4))
	if afterProps["age"] != "1" {
		t.Fatalf("wheat age after bee fertilization = %q, want \"1\"", afterProps["age"])
	}

	// Fertilize up to the 10-crop cap per pollination trip.
	for i := 1; i < BeeMaxCropsGrownPerPollination; i++ {
		cache.SetBlock(4, 64, 4, wheat0)
		if !srv.TryFertilizeCropUnderBee(beeID) {
			t.Fatalf("expected crop fertilization #%d to succeed", i+1)
		}
	}
	cache.SetBlock(4, 64, 4, wheat0)
	if srv.TryFertilizeCropUnderBee(beeID) {
		t.Fatalf("expected 11th crop fertilization to be blocked until next pollination")
	}
}

func TestBeehiveEnterExitHoneyLevelAndNightRainReturn(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)

	hiveState, ok := world.BlockStateIDByProperties("minecraft:beehive", map[string]string{
		"facing":      "north",
		"honey_level": "0",
	})
	if !ok {
		t.Fatalf("beehive state not found")
	}
	cache.SetBlock(10, 65, 10, hiveState)
	cache.SetBlock(10, 65, 9, world.StateAir) // clear front (north) exit

	// Verify night and rain detection.
	srv.SetWorldTime(1000, 1000)
	if srv.IsBeeNightOrRain(dim) {
		t.Fatalf("expected daytime clear weather not to trigger IsBeeNightOrRain")
	}
	srv.SetWorldTime(13000, 13000)
	if !srv.IsBeeNightOrRain(dim) {
		t.Fatalf("expected night (13000) to trigger IsBeeNightOrRain")
	}
	srv.SetWorldTime(1000, 1000)

	// Spawn 4 bees with nectar; max 3 can enter the hive.
	var beeIDs [4]int32
	for i := 0; i < 4; i++ {
		id, err := srv.SpawnBee(dim, 10.5, 65.5, 10.5, false)
		if err != nil {
			t.Fatalf("SpawnBee #%d: %v", i, err)
		}
		srv.SetBeeHasNectar(id, true)
		beeIDs[i] = id
	}
	for i := 0; i < 3; i++ {
		if !srv.TryEnterBeehive(beeIDs[i], dim, 10, 65, 10) {
			t.Fatalf("expected bee #%d to enter beehive", i)
		}
	}
	if srv.TryEnterBeehive(beeIDs[3], dim, 10, 65, 10) {
		t.Fatalf("expected 4th bee to be rejected by full beehive (max 3)")
	}
	if got := srv.BeehiveOccupantCount(dim, 10, 65, 10); got != 3 {
		t.Fatalf("BeehiveOccupantCount = %d, want 3", got)
	}

	// While it is night, bees must stay inside even after 2400 ticks.
	srv.SetWorldTime(14000, 14000)
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < BeeMinOccupationTicksNectar+5; i++ {
		srv.TickBeehives(rng)
	}
	if got := srv.BeehiveOccupantCount(dim, 10, 65, 10); got != 3 {
		t.Fatalf("expected bees to remain in hive at night, got %d occupants", got)
	}

	// Switch to daytime: all 3 nectar-carrying bees exit and increase honey_level.
	srv.SetWorldTime(1000, 1000)
	srv.TickBeehives(rng)
	if got := srv.BeehiveOccupantCount(dim, 10, 65, 10); got != 0 {
		t.Fatalf("expected all 3 bees to exit during daytime, got %d occupants", got)
	}
	honey := srv.BeehiveHoneyLevelAt(dim, 10, 65, 10)
	if honey < 3 {
		t.Fatalf("expected honey_level >= 3 after 3 nectar bees exited, got %d", honey)
	}
	compOut, ok := srv.ComparatorOutputForBeehive(dim, 10, 65, 10)
	if !ok || compOut != honey {
		t.Fatalf("ComparatorOutputForBeehive = (%d, %v), want (%d, true)", compOut, ok, honey)
	}
}

func TestBeehiveHarvestShearsAndBottleWithCampfireSmoke(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyNormal)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)

	hive5, ok := world.BlockStateIDByProperties("minecraft:beehive", map[string]string{
		"facing":      "north",
		"honey_level": "5",
	})
	if !ok {
		t.Fatalf("beehive[honey_level=5] not found")
	}
	campfireLit, ok := world.BlockStateIDByProperties("minecraft:campfire", map[string]string{
		"lit": "true",
	})
	if !ok {
		t.Fatalf("campfire[lit=true] not found")
	}
	cache.SetBlock(20, 65, 20, hive5)
	cache.SetBlock(20, 63, 20, campfireLit) // 2 blocks below -> smokes the hive

	if !srv.IsBeehiveSmokedByCampfire(dim, 20, 65, 20) {
		t.Fatalf("expected lit campfire 2 blocks below to smoke beehive")
	}

	player := newBeeTestPlayer(t, srv, "Beekeeper", 20.5, 65.0, 21.5)
	shearsSlot := world.HotbarContainerOffset + int(player.Snapshot().SelectedSlot)
	player.SetInventorySlot(shearsSlot, world.NewNamedItemStack("minecraft:shears", 1))

	nearbyBeeID, _ := srv.SpawnBee(dim, 21.5, 65.5, 20.5, false)
	rng := rand.New(rand.NewSource(99))

	// 1. Harvest honeycomb with shears while smoked: no bee anger, shears take 1 durability, honey_level -> 0.
	harvested, drops := srv.HarvestBeehiveWithShears(player, dim, 20, 65, 20, 0, rng)
	if !harvested || len(drops) != 1 || drops[0].ResolvedID() != "minecraft:honeycomb" || drops[0].Count != 3 {
		t.Fatalf("HarvestBeehiveWithShears = (%v, %+v), want 3x honeycomb", harvested, drops)
	}
	if got := srv.BeehiveHoneyLevelAt(dim, 20, 65, 20); got != 0 {
		t.Fatalf("honey_level after shearing = %d, want 0", got)
	}
	if meta := GetSlotItemMeta(player, shearsSlot); meta.Damage != 1 {
		t.Fatalf("shears damage = %d, want 1", meta.Damage)
	}
	if st, _ := srv.BeeStateFor(nearbyBeeID); st.AngerTicks != 0 {
		t.Fatalf("expected smoked harvest not to anger nearby bee, got AngerTicks=%d", st.AngerTicks)
	}
	if !player.HasCompletedAdvancement(AdvancementSafelyHarvestHoney) {
		t.Fatalf("expected %s to be granted when harvesting smoked beehive", AdvancementSafelyHarvestHoney)
	}

	// 2. Refill to honey_level=5 and harvest with glass_bottle while smoked.
	srv.SetBeehiveHoneyLevel(dim, 20, 65, 20, 5)
	player.SetInventorySlot(shearsSlot, world.NewNamedItemStack("minecraft:glass_bottle", 1))
	harvestedBottle, honeyBottle := srv.HarvestBeehiveWithBottle(player, dim, 20, 65, 20, 0, rng)
	if !harvestedBottle || honeyBottle.ResolvedID() != "minecraft:honey_bottle" || honeyBottle.Count != 1 {
		t.Fatalf("HarvestBeehiveWithBottle = (%v, %+v), want 1x honey_bottle", harvestedBottle, honeyBottle)
	}

	// 3. Drink honey_bottle: clears Poison, restores +6 food / +1.2 saturation, returns glass_bottle.
	player.mu.Lock()
	player.foodLevel = 10
	player.foodSaturation = 1.0
	player.mu.Unlock()
	srv.ApplyPlayerEffect(player, StatusEffectInstance{
		ID:       EffectPoison,
		Duration: 200,
	})
	emptyBottle := srv.ConsumeHoneyBottle(player)
	if emptyBottle.ResolvedID() != "minecraft:glass_bottle" {
		t.Fatalf("ConsumeHoneyBottle returned %q, want minecraft:glass_bottle", emptyBottle.ResolvedID())
	}
	if player.HasPlayerEffect(EffectPoison) {
		t.Fatalf("expected ConsumeHoneyBottle to cure EffectPoison")
	}
	if gotFood := player.Snapshot().FoodLevel; gotFood != 16 {
		t.Fatalf("food after honey_bottle = %d, want 16", gotFood)
	}

	// 4. Extinguish campfire and harvest un-smoked hive -> angers nearby bees!
	cache.SetBlock(20, 63, 20, world.StateAir)
	srv.SetBeehiveHoneyLevel(dim, 20, 65, 20, 5)
	srv.HarvestBeehiveWithBottle(player, dim, 20, 65, 20, 0, rng)
	if st, _ := srv.BeeStateFor(nearbyBeeID); st.AngerTicks < BeeAngerMinTicks {
		t.Fatalf("expected un-smoked harvest to anger nearby bee (>=%d ticks), got %d", BeeAngerMinTicks, st.AngerTicks)
	}
}

func TestBeeSwarmAggressionStingPoisonAndDeath(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyHard)
	dim := world.DimensionOverworld

	player := newBeeTestPlayer(t, srv, "Attacker", 30.5, 65.0, 30.5)
	bee1, _ := srv.SpawnBee(dim, 31.5, 65.5, 30.5, false)
	bee2, _ := srv.SpawnBee(dim, 35.5, 65.5, 30.5, false) // within 22 blocks

	// Attacking bee1 angers both bee1 and bee2 (swarm aggro within 22 blocks).
	ent1 := srv.entities.Get(bee1)
	srv.applyDamageToMob(ent1, 1.0, player, 0.2, false, true)

	st1, _ := srv.BeeStateFor(bee1)
	st2, _ := srv.BeeStateFor(bee2)
	if st1.AngerTicks < BeeAngerMinTicks || st2.AngerTicks < BeeAngerMinTicks {
		t.Fatalf("expected swarm anger on both bees, got st1=%d st2=%d", st1.AngerTicks, st2.AngerTicks)
	}

	// Bee1 stings player on Hard: deals damage, applies 18s (360 ticks) Poison, loses stinger, and dies after 1000..1200 ticks.
	rng := rand.New(rand.NewSource(123))
	ent1 = srv.entities.Get(bee1)
	if !srv.PerformBeeSting(ent1, player, rng) {
		t.Fatalf("expected PerformBeeSting to succeed")
	}
	poison, hasPoison := player.PlayerEffect(EffectPoison)
	if !hasPoison || poison.Duration != BeePoisonDurationHardTicks {
		t.Fatalf("expected Hard bee sting to apply %d ticks of Poison, got (%+v, %v)", BeePoisonDurationHardTicks, poison, hasPoison)
	}
	st1, _ = srv.BeeStateFor(bee1)
	if !st1.HasStung || st1.AngerTicks != 0 {
		t.Fatalf("expected bee after sting to have HasStung=true and AngerTicks=0, got %+v", st1)
	}

	// Tick bee1 until its post-sting death timer expires.
	cache := srv.DimensionChunks(dim)
	for i := 0; i < BeePostStingDeathMaxTicks+5; i++ {
		e := srv.entities.Get(bee1)
		if e == nil {
			break
		}
		srv.TickBeeAI(e, cache, nil, rng)
	}
	if srv.entities.Get(bee1) != nil {
		t.Fatalf("expected bee1 to die after losing its stinger (1000..1200 ticks)")
	}
}

func TestBeehiveSilkTouchPreservesBeesAndGrantsAdvancement(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyNormal)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)

	nestState, ok := world.BlockStateIDByProperties("minecraft:bee_nest", map[string]string{
		"facing":      "north",
		"honey_level": "4",
	})
	if !ok {
		t.Fatalf("bee_nest[honey_level=4] not found")
	}
	cache.SetBlock(40, 65, 40, nestState)

	for i := 0; i < 3; i++ {
		id, _ := srv.SpawnBee(dim, 40.5, 65.5, 40.5, false)
		srv.SetBeeHasNectar(id, i%2 == 0)
		if !srv.TryEnterBeehive(id, dim, 40, 65, 40) {
			t.Fatalf("bee #%d failed to enter bee_nest", i)
		}
	}

	player := newBeeTestPlayer(t, srv, "SilkMiner", 40.5, 65.0, 41.5)
	var silkMeta ItemMeta
	silkMeta.SetEnchantment(EnchantSilkTouch, 1)

	drops, metaStr := srv.OnBeehiveBroken(player, dim, 40, 65, 40, nestState, silkMeta, rand.New(rand.NewSource(5)))
	if len(drops) != 1 || drops[0].ResolvedID() != "minecraft:bee_nest" {
		t.Fatalf("expected Silk Touch on bee_nest to drop 1x bee_nest, got %+v", drops)
	}
	if !player.HasCompletedAdvancement(AdvancementSilkTouchNest) {
		t.Fatalf("expected %s when breaking bee_nest with 3 bees using Silk Touch", AdvancementSilkTouchNest)
	}

	// Place the Silk-Touched bee_nest at a new location and verify honey_level=4 and all 3 bees are restored.
	nest0, _ := world.BlockStateIDByProperties("minecraft:bee_nest", map[string]string{
		"facing":      "south",
		"honey_level": "0",
	})
	cache.SetBlock(45, 65, 45, nest0)
	srv.OnBeehivePlaced(dim, 45, 65, 45, metaStr)
	if got := srv.BeehiveHoneyLevelAt(dim, 45, 65, 45); got != 4 {
		t.Fatalf("restored honey_level = %d, want 4", got)
	}
	if got := srv.BeehiveOccupantCount(dim, 45, 65, 45); got != 3 {
		t.Fatalf("restored occupant count = %d, want 3", got)
	}

	// Break without Silk Touch -> releases all 3 bees angry at the player and drops nothing for bee_nest.
	noSilkDrops, _ := srv.OnBeehiveBroken(player, dim, 45, 65, 45, cache.GetBlock(45, 65, 45), ItemMeta{}, rand.New(rand.NewSource(6)))
	if len(noSilkDrops) != 0 {
		t.Fatalf("expected breaking bee_nest without Silk Touch to drop nothing, got %+v", noSilkDrops)
	}
	angryCount := 0
	for _, ent := range srv.entities.AppendAll(nil) {
		if canonicalResourceName(ent.TypeName) == MobTypeBee {
			if st, ok := srv.BeeStateFor(ent.ID); ok && st.AngerTicks >= BeeAngerMinTicks {
				angryCount++
			}
		}
	}
	if angryCount != 3 {
		t.Fatalf("expected 3 angry bees released when breaking nest without Silk Touch, got %d", angryCount)
	}
}

func TestBeeBabyBreedingGrowthHiveAgingAndCustomName(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)

	// 1. DecodeBeeDataPacket round-trip.
	origState := BeeState{
		IsRolling:  true,
		HasStung:   false,
		HasNectar:  true,
		AngerTicks: 640,
	}
	pkt := EncodeBeeDataPacket(77, true, origState)
	decID, decBaby, decSt, err := DecodeBeeDataPacket(protocol.NewReader(pkt))
	if err != nil {
		t.Fatalf("DecodeBeeDataPacket error: %v", err)
	}
	if decID != 77 || !decBaby || !decSt.IsRolling || decSt.HasStung || !decSt.HasNectar || decSt.AngerTicks != 640 {
		t.Fatalf("DecodeBeeDataPacket mismatch: id=%d baby=%v st=%+v", decID, decBaby, decSt)
	}

	// 2. Breed two adult bees with dandelion flowers via InteractWithBee.
	player := newBeeTestPlayer(t, srv, "Beekeeper", 10.5, 65.0, 10.5)
	bee1, _ := srv.SpawnBee(dim, 11.0, 65.5, 10.5, false)
	bee2, _ := srv.SpawnBee(dim, 11.8, 65.5, 10.5, false)

	slot0 := world.HotbarContainerOffset + int(player.Snapshot().SelectedSlot)
	player.SetInventorySlot(slot0, world.NewNamedItemStack("minecraft:dandelion", 16))

	handled1, off1 := srv.InteractWithBee(player, bee1)
	if !handled1 || off1 != 0 {
		t.Fatalf("expected 1st bee to enter love mode without immediate offspring, got (%v, %d)", handled1, off1)
	}
	handled2, babyID := srv.InteractWithBee(player, bee2)
	if !handled2 || babyID <= 0 {
		t.Fatalf("expected 2nd bee to complete breeding and spawn baby bee, got (%v, %d)", handled2, babyID)
	}
	if !srv.IsMobBaby(babyID) || srv.MobAge(babyID) != BabyMobGrowthTicks {
		t.Fatalf("expected baby bee age=%d, got %d (isBaby=%v)", BabyMobGrowthTicks, srv.MobAge(babyID), srv.IsMobBaby(babyID))
	}
	if !player.HasCompletedAdvancement("minecraft:husbandry/breed_an_animal") {
		t.Fatalf("expected breeding bees to grant minecraft:husbandry/breed_an_animal")
	}

	// 3. Feed the baby bee a flower to accelerate growth by 10% (-24000 -> -21600).
	handledFeed, _ := srv.InteractWithBee(player, babyID)
	if !handledFeed || srv.MobAge(babyID) != -21600 {
		t.Fatalf("expected feeding baby bee to accelerate age to -21600, got (handled=%v, age=%d)", handledFeed, srv.MobAge(babyID))
	}

	// 4. Name the baby bee with a name_tag ("Buzzy"), put it in a hive with flower_pos sharing, and verify aging + name + flower_pos on exit.
	player.SetInventorySlot(slot0, world.NewNamedItemStack("minecraft:name_tag", 1))
	SetSlotItemMeta(player, slot0, ItemMeta{CustomName: "Buzzy"})
	if ok, _ := srv.InteractWithBee(player, babyID); !ok || srv.BeeCustomName(babyID) != "Buzzy" {
		t.Fatalf("expected name_tag on bee to set CustomName='Buzzy', got %q", srv.BeeCustomName(babyID))
	}

	hiveState, _ := world.BlockStateIDByProperties("minecraft:beehive", map[string]string{
		"facing":      "north",
		"honey_level": "0",
	})
	cache.SetBlock(12, 65, 12, hiveState)
	cache.SetBlock(12, 65, 11, world.StateAir)

	// Give bee1 a flower position, let bee1 and babyID enter the hive, then tick 600 times so they exit.
	mgr := srv.BeeStates()
	mgr.mu.Lock()
	if st1 := mgr.bees[bee1]; st1 != nil {
		st1.HasFlowerPos = true
		st1.FlowerX, st1.FlowerY, st1.FlowerZ = 15, 65, 15
	}
	mgr.mu.Unlock()
	if !srv.TryEnterBeehive(bee1, dim, 12, 65, 12) {
		t.Fatalf("expected bee1 to enter beehive")
	}
	if fx, fy, fz, ok := srv.BeehiveFlowerPos(dim, 12, 65, 12); !ok || fx != 15 || fy != 65 || fz != 15 {
		t.Fatalf("expected beehive to remember bee1's FlowerPos (15,65,15), got (%d,%d,%d, ok=%v)", fx, fy, fz, ok)
	}
	if !srv.TryEnterBeehive(babyID, dim, 12, 65, 12) {
		t.Fatalf("expected babyID to enter beehive")
	}

	srv.SetWorldTime(1000, 1000)
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < BeeMinOccupationTicksNoNectar; i++ {
		srv.TickBeehives(rng)
	}
	if got := srv.BeehiveOccupantCount(dim, 12, 65, 12); got != 0 {
		t.Fatalf("expected both bees to exit after %d ticks, got %d remaining", BeeMinOccupationTicksNoNectar, got)
	}

	var exitedBabyID int32
	for _, ent := range srv.entities.AppendAll(nil) {
		if canonicalResourceName(ent.TypeName) == MobTypeBee && srv.BeeCustomName(ent.ID) == "Buzzy" {
			exitedBabyID = ent.ID
			break
		}
	}
	if exitedBabyID <= 0 {
		t.Fatalf("expected named baby bee 'Buzzy' to be restored on hive exit")
	}
	// Age in hive should advance by 600 ticks: -21600 + 600 = -21000.
	if gotAge := srv.MobAge(exitedBabyID); gotAge != -21000 {
		t.Fatalf("expected baby bee age after 600 ticks in hive to be -21000, got %d", gotAge)
	}
	// Exited baby bee should also have received the shared FlowerPos (15, 65, 15) from the hive!
	if st, ok := srv.BeeStateFor(exitedBabyID); !ok || !st.HasFlowerPos || st.FlowerX != 15 || st.FlowerY != 65 || st.FlowerZ != 15 {
		t.Fatalf("expected exited bee to inherit hive FlowerPos (15,65,15), got %+v", st)
	}
}

func TestBeeWitherRoseEyeblossomTallFlowersPitcherCropWaterAndPersistence(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)
	rng := rand.New(rand.NewSource(777))

	// 1. Verify closed_eyeblossom is NOT pollinated by bees, while open_eyeblossom IS.
	if IsBeeFlowerBlock("minecraft:closed_eyeblossom") {
		t.Fatalf("expected minecraft:closed_eyeblossom not to be pollinated by bees in 26.1.2")
	}
	if !IsBeeFlowerBlock("minecraft:open_eyeblossom") {
		t.Fatalf("expected minecraft:open_eyeblossom to be pollinated by bees in 26.1.2")
	}

	// 2. Pollinating wither_rose applies EffectWither to the bee.
	witherRose, ok := world.BlockStateIDByProperties("minecraft:wither_rose", nil)
	if !ok {
		t.Fatalf("wither_rose state not found")
	}
	cache.SetBlock(60, 65, 60, witherRose)
	beeWither, _ := srv.SpawnBee(dim, 60.5, 65.5, 60.5, false)
	srv.TickBeeAI(srv.entities.Get(beeWither), cache, nil, rng)
	if !srv.HasEntityEffect(beeWither, EffectWither) {
		t.Fatalf("expected bee pollinating wither_rose to receive EffectWither")
	}

	// 3. Pollinating open_eyeblossom applies EffectPoison to the bee.
	openEye, ok := world.BlockStateIDByProperties("minecraft:open_eyeblossom", nil)
	if !ok {
		t.Fatalf("open_eyeblossom state not found")
	}
	cache.SetBlock(70, 65, 70, openEye)
	beeEye, _ := srv.SpawnBee(dim, 70.5, 65.5, 70.5, false)
	srv.TickBeeAI(srv.entities.Get(beeEye), cache, nil, rng)
	if !srv.HasEntityEffect(beeEye, EffectPoison) {
		t.Fatalf("expected bee pollinating open_eyeblossom to receive EffectPoison")
	}

	// 4. Tall flowers (sunflower lower+upper) -> findNearbyBeeFlower targets upper half (y+1).
	sunLower, _ := world.BlockStateIDByProperties("minecraft:sunflower", map[string]string{"half": "lower"})
	sunUpper, _ := world.BlockStateIDByProperties("minecraft:sunflower", map[string]string{"half": "upper"})
	cache.SetBlock(80, 65, 80, sunLower)
	cache.SetBlock(80, 66, 80, sunUpper)
	if fx, fy, fz, found := srv.findNearbyBeeFlower(cache, 80, 65, 80, 4); !found || fx != 80 || fy != 66 || fz != 80 {
		t.Fatalf("expected findNearbyBeeFlower on tall sunflower to target upper half (80,66,80), got (%d,%d,%d, found=%v)", fx, fy, fz, found)
	}

	// 5. Torchflower crop at age=1 matures into minecraft:torchflower, and pitcher_crop at age=1 becomes double-height age=2.
	srv.SetBeeHasNectar(beeWither, true)
	tf1, _ := world.BlockStateIDByProperties("minecraft:torchflower_crop", map[string]string{"age": "1"})
	cache.SetBlock(60, 64, 60, tf1)
	if !srv.TryFertilizeCropUnderBee(beeWither) {
		t.Fatalf("expected TryFertilizeCropUnderBee on torchflower_crop[age=1] to succeed")
	}
	if got := world.BlockName(cache.GetBlock(60, 64, 60)); got != "minecraft:torchflower" {
		t.Fatalf("torchflower_crop[age=1] after bee fertilization = %q, want minecraft:torchflower", got)
	}

	pc1, _ := world.BlockStateIDByProperties("minecraft:pitcher_crop", map[string]string{"age": "1", "half": "lower"})
	cache.SetBlock(60, 64, 60, pc1)
	cache.SetBlock(60, 65, 60, world.StateAir)
	if !srv.TryFertilizeCropUnderBee(beeWither) {
		t.Fatalf("expected TryFertilizeCropUnderBee on pitcher_crop[age=1] to succeed")
	}
	lowerProps := world.BlockProperties(cache.GetBlock(60, 64, 60))
	upperProps := world.BlockProperties(cache.GetBlock(60, 65, 60))
	if lowerProps["age"] != "2" || lowerProps["half"] != "lower" || upperProps["age"] != "2" || upperProps["half"] != "upper" {
		t.Fatalf("expected pitcher_crop[age=1] to become double-height age=2, got lower=%v upper=%v", lowerProps, upperProps)
	}

	// 6. Water submersion damage: 20 ticks submerged in water deals 1.0 damage.
	cache.SetBlock(90, 65, 90, world.StateWater)
	beeWater, _ := srv.SpawnBee(dim, 90.5, 65.2, 90.5, false)
	hpBefore := srv.MobHealth(beeWater)
	for i := 0; i < 20; i++ {
		// Keep bee submerged at (90.5, 65.2, 90.5) for 20 ticks.
		srv.entities.Update(beeWater, func(e *world.Entity) {
			e.X, e.Y, e.Z = 90.5, 65.2, 90.5
		})
		srv.TickBeeAI(srv.entities.Get(beeWater), cache, nil, rng)
	}
	if hpAfter := srv.MobHealth(beeWater); hpAfter >= hpBefore {
		t.Fatalf("expected bee submerged in water for 20 ticks to take damage: before=%v after=%v", hpBefore, hpAfter)
	}

	// 7. Worldgen tree-decorator StructureBlockEntity{ID: "minecraft:beehive"} hydration + NBT & file persistence.
	nestState, _ := world.BlockStateIDByProperties("minecraft:bee_nest", map[string]string{
		"facing":      "south",
		"honey_level": "0",
	})
	cache.SetBlock(100, 65, 100, nestState)
	cache.AddStructureBlockEntity(world.StructureBlockEntity{
		ID: "minecraft:beehive",
		X:  100,
		Y:  65,
		Z:  100,
	})
	if occCount := srv.BeehiveOccupantCount(dim, 100, 65, 100); occCount < 2 || occCount > 3 {
		t.Fatalf("expected worldgen bee_nest StructureBlockEntity to hydrate 2..3 bees, got %d", occCount)
	}
	if _, stillThere := cache.StructureBlockEntityAt(100, 65, 100); stillThere {
		t.Fatalf("expected consumed worldgen StructureBlockEntity at (100,65,100) to be removed after hydration")
	}

	// Chunk NBT round-trip.
	srv.SetBeehiveFlowerPos(dim, 100, 65, 100, 102, 65, 102)
	cx, cz := int32(100>>4), int32(100>>4)
	nbtList := srv.UnloadChunkBeehiveBlockEntities(dim, cx, cz)
	if len(nbtList) != 1 {
		t.Fatalf("UnloadChunkBeehiveBlockEntities returned %d compounds, want 1", len(nbtList))
	}
	srv.LoadChunkBeehiveBlockEntitiesNBT(dim, nbtList)
	if fx, fy, fz, ok := srv.BeehiveFlowerPos(dim, 100, 65, 100); !ok || fx != 102 || fy != 65 || fz != 102 {
		t.Fatalf("restored chunk beehive FlowerPos = (%d,%d,%d, ok=%v), want (102,65,102, true)", fx, fy, fz, ok)
	}

	// File persistence round-trip across Server instances.
	savePath := filepath.Join(t.TempDir(), "bees_and_hives.json")
	if err := srv.SaveBeesAndBeehivesFile(savePath); err != nil {
		t.Fatalf("SaveBeesAndBeehivesFile failed: %v", err)
	}
	srv2 := newSurvivalTestServer(t)
	srv2.DimensionChunks(dim).SetBlock(100, 65, 100, nestState)
	if err := srv2.LoadBeesAndBeehivesFile(savePath); err != nil {
		t.Fatalf("LoadBeesAndBeehivesFile failed: %v", err)
	}
	if occCount2 := srv2.BeehiveOccupantCount(dim, 100, 65, 100); occCount2 < 2 {
		t.Fatalf("expected restored beehive in srv2 to have >= 2 occupants, got %d", occCount2)
	}
}
