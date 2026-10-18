package server

import (
	"math/rand"
	"testing"

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
