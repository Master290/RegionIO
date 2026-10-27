package world

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

func TestRandomTickSystemAndSpeed(t *testing.T) {
	c := NewCache(256, GenerateFlat)
	if got := c.RandomTickSpeed(); got != DefaultRandomTickSpeed {
		t.Fatalf("default RandomTickSpeed = %d, want %d", got, DefaultRandomTickSpeed)
	}

	c.SetRandomTickSpeed(0)
	if got := c.RandomTickSpeed(); got != 0 {
		t.Fatalf("RandomTickSpeed after SetRandomTickSpeed(0) = %d, want 0", got)
	}

	farmland, _ := nameToStateID("minecraft:farmland", map[string]string{"moisture": "0"})
	wheat0, _ := nameToStateID("minecraft:wheat", map[string]string{"age": "0"})
	c.SetBlock(4, FlatSurfaceY, 4, farmland)
	c.SetBlock(4, FlatSurfaceY+1, 4, wheat0)

	// With speed 0, RandomTickLoadedChunks must not change any block.
	res := c.RandomTickLoadedChunks()
	if len(res.Changes) != 0 {
		t.Fatalf("expected 0 changes when randomTickSpeed=0, got %d", len(res.Changes))
	}

	// With a high randomTickSpeed, the wheat crop in the section is picked and advances.
	c.SetRandomTickSeed(42)
	c.SetRandomTickSpeed(4096)
	grew := false
	for i := 0; i < 10; i++ {
		c.RandomTickLoadedChunks()
		st := c.GetBlock(4, FlatSurfaceY+1, 4)
		info, ok := stateByID(st)
		if ok && info.Name == "minecraft:wheat" && info.Properties["age"] != "0" {
			grew = true
			break
		}
	}
	if !grew {
		t.Fatalf("expected wheat to grow under RandomTickLoadedChunks with high randomTickSpeed")
	}
}

func TestCropGrowthWheatCarrotsPotatoesBeetroots(t *testing.T) {
	c := NewCache(256, GenerateFlat)
	dryFarmland, _ := nameToStateID("minecraft:farmland", map[string]string{"moisture": "0"})
	wetFarmland, _ := nameToStateID("minecraft:farmland", map[string]string{"moisture": "7"})

	c.SetBlock(8, FlatSurfaceY, 8, dryFarmland)
	drySpeed := c.CropGrowthSpeed(8, FlatSurfaceY+1, 8, "minecraft:wheat")
	if drySpeed != 2.0 {
		t.Fatalf("dry single farmland speed = %v, want 2.0", drySpeed)
	}

	c.SetBlock(8, FlatSurfaceY, 8, wetFarmland)
	wetSpeed := c.CropGrowthSpeed(8, FlatSurfaceY+1, 8, "minecraft:wheat")
	if wetSpeed != 4.0 {
		t.Fatalf("wet single farmland speed = %v, want 4.0", wetSpeed)
	}

	// Diagonal same-crop halves growth speed.
	wheat0, _ := nameToStateID("minecraft:wheat", map[string]string{"age": "0"})
	c.SetBlock(8, FlatSurfaceY+1, 8, wheat0)
	c.SetBlock(9, FlatSurfaceY, 9, wetFarmland)
	c.SetBlock(9, FlatSurfaceY+1, 9, wheat0)
	diagSpeed := c.CropGrowthSpeed(8, FlatSurfaceY+1, 8, "minecraft:wheat")
	if want := (4.0 + 0.75) / 2.0; diagSpeed != want {
		t.Fatalf("diagonal crop speed = %v, want %v", diagSpeed, want)
	}
	c.SetBlock(9, FlatSurfaceY+1, 9, StateAir)

	// Grow wheat, carrots, potatoes to age 7 and beetroots to age 3.
	rng := rand.New(rand.NewSource(12345))
	for _, crop := range []struct {
		name   string
		maxAge int
		x      int
	}{
		{"minecraft:wheat", 7, 2},
		{"minecraft:carrots", 7, 4},
		{"minecraft:potatoes", 7, 6},
		{"minecraft:beetroots", 3, 8},
	} {
		c.SetBlock(crop.x, FlatSurfaceY, 2, wetFarmland)
		st0, ok := nameToStateID(crop.name, map[string]string{"age": "0"})
		if !ok {
			t.Fatalf("missing state for %s", crop.name)
		}
		c.SetBlock(crop.x, FlatSurfaceY+1, 2, st0)

		for i := 0; i < 400; i++ {
			c.RandomTickBlockWithRNG(crop.x, FlatSurfaceY+1, 2, rng)
		}
		got := c.GetBlock(crop.x, FlatSurfaceY+1, 2)
		info, _ := stateByID(got)
		if info.Name != crop.name || info.Properties["age"] != strconv.Itoa(crop.maxAge) {
			t.Fatalf("%s after ticks = %s[age=%s], want age=%d", crop.name, info.Name, info.Properties["age"], crop.maxAge)
		}
	}
}

func TestMelonAndPumpkinStemGrowthAndAttachment(t *testing.T) {
	c := NewCache(256, GenerateFlat)
	wetFarmland, _ := nameToStateID("minecraft:farmland", map[string]string{"moisture": "7"})
	melonStem0, _ := nameToStateID("minecraft:melon_stem", map[string]string{"age": "0"})
	c.SetBlock(8, FlatSurfaceY, 8, wetFarmland)
	c.SetBlock(8, FlatSurfaceY+1, 8, melonStem0)

	rng := rand.New(rand.NewSource(99))
	for i := 0; i < 500; i++ {
		c.RandomTickBlockWithRNG(8, FlatSurfaceY+1, 8, rng)
		st := c.GetBlock(8, FlatSurfaceY+1, 8)
		if isBlockNamed(st, "minecraft:attached_melon_stem") {
			break
		}
	}

	stemState := c.GetBlock(8, FlatSurfaceY+1, 8)
	stemInfo, ok := stateByID(stemState)
	if !ok || stemInfo.Name != "minecraft:attached_melon_stem" {
		t.Fatalf("expected attached_melon_stem at (8, y+1, 8), got %v", stemInfo.Name)
	}
	dx, dz := facingOffset(stemInfo.Properties["facing"])
	fruitX, fruitZ := 8+dx, 8+dz
	if !isBlockNamed(c.GetBlock(fruitX, FlatSurfaceY+1, fruitZ), "minecraft:melon") {
		t.Fatalf("expected melon at (%d, y+1, %d) facing %s", fruitX, fruitZ, stemInfo.Properties["facing"])
	}

	// Breaking the melon reverts attached_melon_stem back to melon_stem[age=7].
	c.BreakBlockWithUpdates(fruitX, FlatSurfaceY+1, fruitZ)
	reverted := c.GetBlock(8, FlatSurfaceY+1, 8)
	revInfo, _ := stateByID(reverted)
	if revInfo.Name != "minecraft:melon_stem" || revInfo.Properties["age"] != "7" {
		t.Fatalf("after breaking melon, stem = %s[age=%s], want melon_stem[age=7]", revInfo.Name, revInfo.Properties["age"])
	}
}

func TestNetherWartCocoaAndSweetBerryBush(t *testing.T) {
	c := NewCache(256, GenerateFlat)
	rng := rand.New(rand.NewSource(777))

	// 1. Nether wart grows on soul_sand from age 0 to 3.
	soulSand, _ := nameToStateID("minecraft:soul_sand", nil)
	wart0, _ := nameToStateID("minecraft:nether_wart", map[string]string{"age": "0"})
	c.SetBlock(2, FlatSurfaceY, 2, soulSand)
	c.SetBlock(2, FlatSurfaceY+1, 2, wart0)
	for i := 0; i < 200; i++ {
		c.RandomTickBlockWithRNG(2, FlatSurfaceY+1, 2, rng)
	}
	wartInfo, _ := stateByID(c.GetBlock(2, FlatSurfaceY+1, 2))
	if wartInfo.Name != "minecraft:nether_wart" || wartInfo.Properties["age"] != "3" {
		t.Fatalf("nether_wart = %s[age=%s], want age=3", wartInfo.Name, wartInfo.Properties["age"])
	}
	// Breaking soul_sand breaks nether_wart above it.
	c.BreakBlockWithUpdates(2, FlatSurfaceY, 2)
	if c.GetBlock(2, FlatSurfaceY+1, 2) != StateAir {
		t.Fatalf("expected nether_wart to break when soul_sand below is broken")
	}

	// 2. Cocoa grows on jungle_log from age 0 to 2 and breaks when log is removed.
	jungleLog, _ := nameToStateID("minecraft:jungle_log", map[string]string{"axis": "y"})
	cocoa0, _ := nameToStateID("minecraft:cocoa", map[string]string{"age": "0", "facing": "north"})
	c.SetBlock(5, FlatSurfaceY+1, 4, jungleLog)
	c.SetBlock(5, FlatSurfaceY+1, 5, cocoa0)
	for i := 0; i < 100; i++ {
		c.RandomTickBlockWithRNG(5, FlatSurfaceY+1, 5, rng)
	}
	cocoaInfo, _ := stateByID(c.GetBlock(5, FlatSurfaceY+1, 5))
	if cocoaInfo.Name != "minecraft:cocoa" || cocoaInfo.Properties["age"] != "2" {
		t.Fatalf("cocoa = %s[age=%s], want age=2", cocoaInfo.Name, cocoaInfo.Properties["age"])
	}
	c.BreakBlockWithUpdates(5, FlatSurfaceY+1, 4)
	if c.GetBlock(5, FlatSurfaceY+1, 5) != StateAir {
		t.Fatalf("expected cocoa to break when supporting jungle_log is broken")
	}

	// 3. Sweet berry bush grows to age 3 and yields 2..3 berries on harvest, resetting to age 1.
	berry0, _ := nameToStateID("minecraft:sweet_berry_bush", map[string]string{"age": "0"})
	c.SetBlock(8, FlatSurfaceY+1, 8, berry0)
	for i := 0; i < 100; i++ {
		c.RandomTickBlockWithRNG(8, FlatSurfaceY+1, 8, rng)
	}
	berryInfo, _ := stateByID(c.GetBlock(8, FlatSurfaceY+1, 8))
	if berryInfo.Name != "minecraft:sweet_berry_bush" || berryInfo.Properties["age"] != "3" {
		t.Fatalf("sweet_berry_bush = %s[age=%s], want age=3", berryInfo.Name, berryInfo.Properties["age"])
	}
	berries, _ := c.HarvestSweetBerryBush(8, FlatSurfaceY+1, 8, rng)
	if berries < 2 || berries > 3 {
		t.Fatalf("HarvestSweetBerryBush at age 3 yielded %d berries, want 2..3", berries)
	}
	afterHarvest, _ := stateByID(c.GetBlock(8, FlatSurfaceY+1, 8))
	if afterHarvest.Properties["age"] != "1" {
		t.Fatalf("sweet_berry_bush after harvest age = %s, want 1", afterHarvest.Properties["age"])
	}
}

func TestFarmlandHydrationDryingTramplingAndTilling(t *testing.T) {
	c := NewCache(256, GenerateFlat)

	// 1. Hoe tilling: grass_block -> farmland[moisture=0]; coarse_dirt -> dirt.
	ok, _ := c.TillSoilAt(4, FlatSurfaceY, 4)
	if !ok {
		t.Fatalf("expected TillSoilAt on grass_block to succeed")
	}
	info, _ := stateByID(c.GetBlock(4, FlatSurfaceY, 4))
	if info.Name != "minecraft:farmland" || info.Properties["moisture"] != "0" {
		t.Fatalf("after TillSoilAt, got %s[moisture=%s], want farmland[moisture=0]", info.Name, info.Properties["moisture"])
	}

	c.SetBlock(5, FlatSurfaceY, 4, StateCoarseDirt)
	if ok, _ := c.TillSoilAt(5, FlatSurfaceY, 4); !ok || c.GetBlock(5, FlatSurfaceY, 4) != StateDirt {
		t.Fatalf("expected TillSoilAt on coarse_dirt to produce dirt")
	}

	// 2. Hydration within 4 blocks of water sets moisture=7.
	c.SetBlock(8, FlatSurfaceY, 4, StateWater)
	if !c.IsFarmlandHydrated(4, FlatSurfaceY, 4) {
		t.Fatalf("expected farmland at (4, y, 4) to be hydrated by water at (8, y, 4)")
	}
	c.RandomTickBlockAt(4, FlatSurfaceY, 4)
	info, _ = stateByID(c.GetBlock(4, FlatSurfaceY, 4))
	if info.Properties["moisture"] != "7" {
		t.Fatalf("hydrated farmland moisture = %s, want 7", info.Properties["moisture"])
	}

	// 3. Remove water: farmland dries down from 7..0 and then turns to dirt when no crop is above.
	c.SetBlock(8, FlatSurfaceY, 4, StateDirt)
	for m := 6; m >= 0; m-- {
		c.RandomTickBlockAt(4, FlatSurfaceY, 4)
		info, _ = stateByID(c.GetBlock(4, FlatSurfaceY, 4))
		if info.Name != "minecraft:farmland" || info.Properties["moisture"] != strconv.Itoa(m) {
			t.Fatalf("drying step got %s[moisture=%s], want farmland[moisture=%d]", info.Name, info.Properties["moisture"], m)
		}
	}
	// One more tick with no crop turns moisture=0 farmland into dirt.
	c.RandomTickBlockAt(4, FlatSurfaceY, 4)
	if c.GetBlock(4, FlatSurfaceY, 4) != StateDirt {
		t.Fatalf("expected dry farmland with no crop to turn into dirt")
	}

	// 4. Farmland trampling turns farmland into dirt and breaks the crop above it.
	c.TillSoilAt(6, FlatSurfaceY, 6)
	wheat0, _ := nameToStateID("minecraft:wheat", map[string]string{"age": "0"})
	c.SetBlock(6, FlatSurfaceY+1, 6, wheat0)
	trampled, _ := c.TrampleFarmland(6, FlatSurfaceY, 6)
	if !trampled {
		t.Fatalf("expected TrampleFarmland to return true")
	}
	if c.GetBlock(6, FlatSurfaceY, 6) != StateDirt {
		t.Fatalf("expected trampled farmland to become dirt")
	}
	if c.GetBlock(6, FlatSurfaceY+1, 6) != StateAir {
		t.Fatalf("expected crop above trampled farmland to break")
	}

	// 5. Placing a solid block on top of farmland turns the farmland into dirt.
	c.TillSoilAt(7, FlatSurfaceY, 7)
	c.ApplyBlockWithUpdates(7, FlatSurfaceY+1, 7, StateStone)
	if c.GetBlock(7, FlatSurfaceY, 7) != StateDirt {
		t.Fatalf("expected farmland under placed stone to become dirt")
	}
}

func TestSaplingTreeGrowth(t *testing.T) {
	c := NewCache(256, GenerateFlat)
	oakSapling0, _ := nameToStateID("minecraft:oak_sapling", map[string]string{"stage": "0"})
	c.SetBlock(8, FlatSurfaceY+1, 8, oakSapling0)

	rng := rand.New(rand.NewSource(42))
	var res BlockUpdateResult
	// First advance: stage 0 -> stage 1
	if !c.AdvanceSapling(8, FlatSurfaceY+1, 8, rng, &res) {
		t.Fatalf("expected first AdvanceSapling to advance stage to 1")
	}
	s1, _ := stateByID(c.GetBlock(8, FlatSurfaceY+1, 8))
	if s1.Name != "minecraft:oak_sapling" || s1.Properties["stage"] != "1" {
		t.Fatalf("after first AdvanceSapling, got %s[stage=%s], want oak_sapling[stage=1]", s1.Name, s1.Properties["stage"])
	}

	// Second advance: stage 1 -> grows oak tree with logs and leaves!
	if !c.AdvanceSapling(8, FlatSurfaceY+1, 8, rng, &res) {
		t.Fatalf("expected second AdvanceSapling to grow oak tree")
	}
	logs, leaves := 0, 0
	for y := FlatSurfaceY + 1; y <= FlatSurfaceY+12; y++ {
		for x := 3; x <= 13; x++ {
			for z := 3; z <= 13; z++ {
				st := c.GetBlock(x, y, z)
				if isLogOrStemBlock(st) {
					logs++
				} else if isLeafBlock(st) {
					leaves++
				}
			}
		}
	}
	if logs < 4 || leaves < 10 {
		t.Fatalf("grown oak tree has %d logs and %d leaves, want >= 4 logs and >= 10 leaves", logs, leaves)
	}
}

func TestBoneMealApplication(t *testing.T) {
	c := NewCache(256, GenerateFlat)
	rng := rand.New(rand.NewSource(101))

	farmland, _ := nameToStateID("minecraft:farmland", map[string]string{"moisture": "7"})
	wheat0, _ := nameToStateID("minecraft:wheat", map[string]string{"age": "0"})
	c.SetBlock(3, FlatSurfaceY, 3, farmland)
	c.SetBlock(3, FlatSurfaceY+1, 3, wheat0)

	if !CanBoneMeal(wheat0) {
		t.Fatalf("expected wheat[age=0] to be bonemealable")
	}
	consumed, _ := c.ApplyBoneMeal(3, FlatSurfaceY+1, 3, rng)
	if !consumed {
		t.Fatalf("expected ApplyBoneMeal on wheat[age=0] to consume bonemeal")
	}
	info, _ := stateByID(c.GetBlock(3, FlatSurfaceY+1, 3))
	age, _ := strconv.Atoi(info.Properties["age"])
	if age < 2 || age > 5 {
		t.Fatalf("wheat age after 1 bonemeal = %d, want 2..5", age)
	}

	// Mature wheat[age=7] rejects bone meal.
	wheat7, _ := nameToStateID("minecraft:wheat", map[string]string{"age": "7"})
	c.SetBlock(3, FlatSurfaceY+1, 3, wheat7)
	if CanBoneMeal(wheat7) {
		t.Fatalf("expected mature wheat[age=7] not to be bonemealable")
	}
	if consumed, _ := c.ApplyBoneMeal(3, FlatSurfaceY+1, 3, rng); consumed {
		t.Fatalf("expected ApplyBoneMeal on mature wheat[age=7] to return consumed=false")
	}

	// Bone meal on grass_block grows vegetation above surrounding grass blocks.
	consumed, res := c.ApplyBoneMeal(8, FlatSurfaceY, 8, rng)
	if !consumed || len(res.Changes) == 0 {
		t.Fatalf("expected ApplyBoneMeal on grass_block to grow grass/flowers")
	}
}

func TestSugarCaneCactusBambooAndKelpGrowth(t *testing.T) {
	c := NewCache(256, GenerateFlat)
	rng := rand.New(rand.NewSource(555))

	// 1. Sugar cane grows up to 3 blocks high.
	c.SetBlock(1, FlatSurfaceY, 2, StateWater)
	cane15, _ := nameToStateID("minecraft:sugar_cane", map[string]string{"age": "15"})
	c.SetBlock(2, FlatSurfaceY+1, 2, cane15)
	c.RandomTickBlockWithRNG(2, FlatSurfaceY+1, 2, rng)
	if !isBlockNamed(c.GetBlock(2, FlatSurfaceY+2, 2), "minecraft:sugar_cane") {
		t.Fatalf("expected sugar_cane to grow to height 2 when age=15")
	}
	c.SetBlock(2, FlatSurfaceY+2, 2, cane15)
	c.RandomTickBlockWithRNG(2, FlatSurfaceY+2, 2, rng)
	if !isBlockNamed(c.GetBlock(2, FlatSurfaceY+3, 2), "minecraft:sugar_cane") {
		t.Fatalf("expected sugar_cane to grow to height 3 when age=15")
	}
	// Height 3 does not grow to height 4.
	c.SetBlock(2, FlatSurfaceY+3, 2, cane15)
	c.RandomTickBlockWithRNG(2, FlatSurfaceY+3, 2, rng)
	if !isAirState(c.GetBlock(2, FlatSurfaceY+4, 2)) {
		t.Fatalf("expected sugar_cane not to exceed height 3")
	}

	// 2. Cactus grows up to 3 blocks high on sand.
	c.SetBlock(6, FlatSurfaceY, 6, StateSand)
	cactus15, _ := nameToStateID("minecraft:cactus", map[string]string{"age": "15"})
	c.SetBlock(6, FlatSurfaceY+1, 6, cactus15)
	c.RandomTickBlockWithRNG(6, FlatSurfaceY+1, 6, rng)
	if !isBlockNamed(c.GetBlock(6, FlatSurfaceY+2, 6), "minecraft:cactus") {
		t.Fatalf("expected cactus to grow to height 2 when age=15")
	}

	// 3. Bamboo sapling grows into bamboo column.
	bambooSapling, _ := nameToStateID("minecraft:bamboo_sapling", nil)
	c.SetBlock(10, FlatSurfaceY+1, 10, bambooSapling)
	for i := 0; i < 30; i++ {
		c.RandomTickBlockWithRNG(10, FlatSurfaceY+1, 10, rng)
	}
	if !isBlockNamed(c.GetBlock(10, FlatSurfaceY+1, 10), "minecraft:bamboo") ||
		!isBlockNamed(c.GetBlock(10, FlatSurfaceY+2, 10), "minecraft:bamboo") {
		t.Fatalf("expected bamboo_sapling to grow into 2-block bamboo")
	}

	// 4. Kelp grows into source water above and converts base to kelp_plant.
	c.SetBlock(12, FlatSurfaceY+1, 12, StateWater)
	c.SetBlock(12, FlatSurfaceY+2, 12, StateWater)
	kelp0, _ := nameToStateID("minecraft:kelp", map[string]string{"age": "0"})
	c.SetBlock(12, FlatSurfaceY+1, 12, kelp0)
	for i := 0; i < 100; i++ {
		c.RandomTickBlockWithRNG(12, FlatSurfaceY+1, 12, rng)
		if isBlockNamed(c.GetBlock(12, FlatSurfaceY+1, 12), "minecraft:kelp_plant") {
			break
		}
	}
	if !isBlockNamed(c.GetBlock(12, FlatSurfaceY+1, 12), "minecraft:kelp_plant") ||
		!isBlockNamed(c.GetBlock(12, FlatSurfaceY+2, 12), "minecraft:kelp") {
		t.Fatalf("expected kelp to convert base to kelp_plant and grow kelp tip above")
	}
}

func TestGrassAndMyceliumSpreadAndDecay(t *testing.T) {
	c := NewCache(256, GenerateFlat)
	rng := rand.New(rand.NewSource(321))

	// Place dirt next to grass_block and tick grass_block until dirt turns into grass_block.
	c.SetBlock(5, FlatSurfaceY, 5, StateDirt)
	spread := false
	for i := 0; i < 200; i++ {
		c.RandomTickBlockWithRNG(4, FlatSurfaceY, 5, rng)
		if isBlockNamed(c.GetBlock(5, FlatSurfaceY, 5), "minecraft:grass_block") {
			spread = true
			break
		}
	}
	if !spread {
		t.Fatalf("expected grass_block to spread to adjacent dirt")
	}

	// Covering grass_block with stone causes it to decay to dirt on random tick.
	c.SetBlock(4, FlatSurfaceY+1, 5, StateStone)
	c.RandomTickBlockWithRNG(4, FlatSurfaceY, 5, rng)
	if c.GetBlock(4, FlatSurfaceY, 5) != StateDirt {
		t.Fatalf("expected grass_block covered by stone to decay to dirt")
	}

	// Mycelium spreads onto adjacent dirt.
	c.SetBlock(10, FlatSurfaceY, 10, StateMycelium)
	c.SetBlock(11, FlatSurfaceY, 10, StateDirt)
	myceliumSpread := false
	for i := 0; i < 200; i++ {
		c.RandomTickBlockWithRNG(10, FlatSurfaceY, 10, rng)
		if isBlockNamed(c.GetBlock(11, FlatSurfaceY, 10), "minecraft:mycelium") {
			myceliumSpread = true
			break
		}
	}
	if !myceliumSpread {
		t.Fatalf("expected mycelium to spread to adjacent dirt")
	}
}

func TestLeafDistanceAndDecay(t *testing.T) {
	c := NewCache(256, GenerateFlat)
	rng := rand.New(rand.NewSource(888))

	logState, _ := nameToStateID("minecraft:oak_log", map[string]string{"axis": "y"})
	leaf1, _ := nameToStateID("minecraft:oak_leaves", map[string]string{
		"distance":    "1",
		"persistent":  "false",
		"waterlogged": "false",
	})
	leafPersistent, _ := nameToStateID("minecraft:oak_leaves", map[string]string{
		"distance":    "7",
		"persistent":  "true",
		"waterlogged": "false",
	})

	c.ApplyBlockWithUpdates(8, FlatSurfaceY+1, 8, logState)
	c.ApplyBlockWithUpdates(9, FlatSurfaceY+1, 8, leaf1)
	c.ApplyBlockWithUpdates(10, FlatSurfaceY+1, 8, leafPersistent)

	// Breaking the log updates the non-persistent leaf's distance to 7.
	c.BreakBlockWithUpdates(8, FlatSurfaceY+1, 8)
	leafInfo, _ := stateByID(c.GetBlock(9, FlatSurfaceY+1, 8))
	if !strings.HasSuffix(leafInfo.Name, "_leaves") || leafInfo.Properties["distance"] != "7" {
		t.Fatalf("after breaking log, leaf = %s[distance=%s], want distance=7", leafInfo.Name, leafInfo.Properties["distance"])
	}

	// Random ticking the non-persistent leaf at distance 7 decays it to air.
	c.RandomTickBlockWithRNG(9, FlatSurfaceY+1, 8, rng)
	if c.GetBlock(9, FlatSurfaceY+1, 8) != StateAir {
		t.Fatalf("expected non-persistent leaf at distance=7 to decay to air")
	}

	// Random ticking the persistent leaf at distance 7 does NOT decay it.
	c.RandomTickBlockWithRNG(10, FlatSurfaceY+1, 8, rng)
	if !isLeafBlock(c.GetBlock(10, FlatSurfaceY+1, 8)) {
		t.Fatalf("expected persistent leaf not to decay")
	}
}

func TestTorchflowerAndPitcherCropGrowthBonemealSurvivalAndDrops(t *testing.T) {
	c := NewCache(256, GenerateFlat)
	rng := rand.New(rand.NewSource(2026))

	// Hydrated farmland at (4, FlatSurfaceY, 4) and (6, FlatSurfaceY, 6).
	c.SetBlock(5, FlatSurfaceY, 5, StateWater)
	farmlandWet, _ := nameToStateID("minecraft:farmland", map[string]string{"moisture": "7"})
	c.SetBlock(4, FlatSurfaceY, 4, farmlandWet)
	c.SetBlock(6, FlatSurfaceY, 6, farmlandWet)

	// 1. Plant torchflower_seeds -> torchflower_crop[age=0].
	planted, blockName, _ := c.PlantCropItemAt(4, FlatSurfaceY, 4, 1, "minecraft:torchflower_seeds")
	if !planted || blockName != "minecraft:torchflower_crop" {
		t.Fatalf("PlantCropItemAt(torchflower_seeds) = (%v, %q), want (true, minecraft:torchflower_crop)", planted, blockName)
	}
	st0 := c.GetBlock(4, FlatSurfaceY+1, 4)
	if drops := CropDropsForBlock(st0, 0, rng); len(drops) != 1 || drops[0].ResolvedID() != "minecraft:torchflower_seeds" {
		t.Fatalf("torchflower_crop[age=0] drops = %+v, want 1x torchflower_seeds", drops)
	}

	// Bone meal once -> age=1; bone meal again -> matures into minecraft:torchflower!
	ok, _ := c.ApplyBoneMeal(4, FlatSurfaceY+1, 4, rng)
	if !ok {
		t.Fatalf("expected ApplyBoneMeal on torchflower_crop[age=0] to succeed")
	}
	info1, _ := stateByID(c.GetBlock(4, FlatSurfaceY+1, 4))
	if info1.Name != "minecraft:torchflower_crop" || info1.Properties["age"] != "1" {
		t.Fatalf("after 1st bonemeal, got %s[%v], want torchflower_crop[age=1]", info1.Name, info1.Properties)
	}
	ok, _ = c.ApplyBoneMeal(4, FlatSurfaceY+1, 4, rng)
	if !ok {
		t.Fatalf("expected ApplyBoneMeal on torchflower_crop[age=1] to succeed")
	}
	info2, _ := stateByID(c.GetBlock(4, FlatSurfaceY+1, 4))
	if info2.Name != "minecraft:torchflower" {
		t.Fatalf("after 2nd bonemeal, got %s, want minecraft:torchflower", info2.Name)
	}
	if drops := CropDropsForBlock(c.GetBlock(4, FlatSurfaceY+1, 4), 0, rng); len(drops) != 1 || drops[0].ResolvedID() != "minecraft:torchflower" {
		t.Fatalf("torchflower drops = %+v, want 1x torchflower", drops)
	}

	// 2. Plant pitcher_pod -> pitcher_crop[age=0,half=lower].
	planted, blockName, _ = c.PlantCropItemAt(6, FlatSurfaceY, 6, 1, "minecraft:pitcher_pod")
	if !planted || blockName != "minecraft:pitcher_crop" {
		t.Fatalf("PlantCropItemAt(pitcher_pod) = (%v, %q), want (true, minecraft:pitcher_crop)", planted, blockName)
	}

	// Advance pitcher_crop to age=1 (still single-block lower half).
	if ok, _ := c.ApplyBoneMeal(6, FlatSurfaceY+1, 6, rng); !ok {
		t.Fatalf("expected ApplyBoneMeal on pitcher_crop[age=0] to succeed")
	}
	pInfo1, _ := stateByID(c.GetBlock(6, FlatSurfaceY+1, 6))
	if pInfo1.Properties["age"] != "1" || pInfo1.Properties["half"] != "lower" || !isAirState(c.GetBlock(6, FlatSurfaceY+2, 6)) {
		t.Fatalf("pitcher_crop at age=1 unexpected state: %+v, upper=%d", pInfo1, c.GetBlock(6, FlatSurfaceY+2, 6))
	}

	// Advance pitcher_crop to age=2 -> becomes double-height (lower + upper)!
	if ok, _ := c.ApplyBoneMeal(6, FlatSurfaceY+1, 6, rng); !ok {
		t.Fatalf("expected ApplyBoneMeal on pitcher_crop[age=1] to succeed")
	}
	pLower2, _ := stateByID(c.GetBlock(6, FlatSurfaceY+1, 6))
	pUpper2, _ := stateByID(c.GetBlock(6, FlatSurfaceY+2, 6))
	if pLower2.Properties["age"] != "2" || pLower2.Properties["half"] != "lower" ||
		pUpper2.Name != "minecraft:pitcher_crop" || pUpper2.Properties["age"] != "2" || pUpper2.Properties["half"] != "upper" {
		t.Fatalf("pitcher_crop at age=2 expected lower+upper age=2, got lower=%+v upper=%+v", pLower2, pUpper2)
	}
	// Unripe pitcher_crop drops pitcher_pod from lower half and nil from upper half.
	if drops := CropDropsForBlock(c.GetBlock(6, FlatSurfaceY+1, 6), 0, rng); len(drops) != 1 || drops[0].ResolvedID() != "minecraft:pitcher_pod" {
		t.Fatalf("pitcher_crop[age=2,half=lower] drops = %+v, want 1x pitcher_pod", drops)
	}
	if drops := CropDropsForBlock(c.GetBlock(6, FlatSurfaceY+2, 6), 0, rng); len(drops) != 0 {
		t.Fatalf("pitcher_crop[age=2,half=upper] drops = %+v, want nil", drops)
	}

	// Bone meal clicking the UPPER half at (6, FlatSurfaceY+2, 6) advances both halves to age=3 and then age=4!
	if ok, _ := c.ApplyBoneMeal(6, FlatSurfaceY+2, 6, rng); !ok {
		t.Fatalf("expected ApplyBoneMeal on upper half of pitcher_crop[age=2] to succeed")
	}
	if ok, _ := c.ApplyBoneMeal(6, FlatSurfaceY+2, 6, rng); !ok {
		t.Fatalf("expected ApplyBoneMeal on upper half of pitcher_crop[age=3] to succeed")
	}
	pLower4, _ := stateByID(c.GetBlock(6, FlatSurfaceY+1, 6))
	pUpper4, _ := stateByID(c.GetBlock(6, FlatSurfaceY+2, 6))
	if pLower4.Properties["age"] != "4" || pUpper4.Properties["age"] != "4" {
		t.Fatalf("pitcher_crop after maturing expected age=4 on both halves, got lower=%+v upper=%+v", pLower4, pUpper4)
	}
	if CanBoneMeal(c.GetBlock(6, FlatSurfaceY+1, 6)) || CanBoneMeal(c.GetBlock(6, FlatSurfaceY+2, 6)) {
		t.Fatalf("expected pitcher_crop[age=4] not to accept further bone meal")
	}
	if drops := CropDropsForBlock(c.GetBlock(6, FlatSurfaceY+1, 6), 0, rng); len(drops) != 1 || drops[0].ResolvedID() != "minecraft:pitcher_plant" {
		t.Fatalf("pitcher_crop[age=4,half=lower] drops = %+v, want 1x pitcher_plant", drops)
	}

	// Breaking the upper half breaks the lower half as well.
	c.BreakBlockWithUpdates(6, FlatSurfaceY+2, 6)
	if !isAirState(c.GetBlock(6, FlatSurfaceY+1, 6)) || !isAirState(c.GetBlock(6, FlatSurfaceY+2, 6)) {
		t.Fatalf("expected breaking upper half of pitcher_crop to remove lower half as well")
	}
}

func TestBonemealMangroveGrassSeagrassPetalsTallFlowersAndDripleaf(t *testing.T) {
	c := NewCache(256, GenerateFlat)
	rng := rand.New(rand.NewSource(777))

	// 1. mangrove_leaves -> spawns hanging mangrove_propagule[age=0,hanging=true] below, then ages 0..4.
	mLeaves, _ := nameToStateID("minecraft:mangrove_leaves", map[string]string{
		"distance": "1", "persistent": "true", "waterlogged": "false",
	})
	c.SetBlock(4, FlatSurfaceY+3, 4, mLeaves)
	if ok, _ := c.ApplyBoneMeal(4, FlatSurfaceY+3, 4, rng); !ok {
		t.Fatalf("expected ApplyBoneMeal on mangrove_leaves with air below to succeed")
	}
	propInfo, _ := stateByID(c.GetBlock(4, FlatSurfaceY+2, 4))
	if propInfo.Name != "minecraft:mangrove_propagule" || propInfo.Properties["hanging"] != "true" || propInfo.Properties["age"] != "0" {
		t.Fatalf("expected hanging mangrove_propagule[age=0] below mangrove_leaves, got %+v", propInfo)
	}
	for wantAge := 1; wantAge <= 4; wantAge++ {
		if ok, _ := c.ApplyBoneMeal(4, FlatSurfaceY+2, 4, rng); !ok {
			t.Fatalf("expected ApplyBoneMeal on hanging mangrove_propagule to advance to age=%d", wantAge)
		}
		pi, _ := stateByID(c.GetBlock(4, FlatSurfaceY+2, 4))
		if pi.Properties["age"] != strconv.Itoa(wantAge) {
			t.Fatalf("hanging mangrove_propagule age = %s, want %d", pi.Properties["age"], wantAge)
		}
	}
	if CanBoneMeal(c.GetBlock(4, FlatSurfaceY+2, 4)) {
		t.Fatalf("expected hanging mangrove_propagule at age=4 not to accept bone meal")
	}

	// 2. short_grass -> tall_grass; fern -> large_fern; short_dry_grass -> tall_dry_grass.
	shortGrass, _ := nameToStateID("minecraft:short_grass", nil)
	c.SetBlock(8, FlatSurfaceY+1, 8, shortGrass)
	if ok, _ := c.ApplyBoneMeal(8, FlatSurfaceY+1, 8, rng); !ok {
		t.Fatalf("expected ApplyBoneMeal on short_grass to succeed")
	}
	if !isBlockNamed(c.GetBlock(8, FlatSurfaceY+1, 8), "minecraft:tall_grass") || !isBlockNamed(c.GetBlock(8, FlatSurfaceY+2, 8), "minecraft:tall_grass") {
		t.Fatalf("expected short_grass to grow into double-height tall_grass")
	}

	shortDry, _ := nameToStateID("minecraft:short_dry_grass", nil)
	c.SetBlock(10, FlatSurfaceY+1, 8, shortDry)
	if ok, _ := c.ApplyBoneMeal(10, FlatSurfaceY+1, 8, rng); !ok {
		t.Fatalf("expected ApplyBoneMeal on short_dry_grass to succeed")
	}
	if !isBlockNamed(c.GetBlock(10, FlatSurfaceY+1, 8), "minecraft:tall_dry_grass") {
		t.Fatalf("expected short_dry_grass to become tall_dry_grass")
	}

	// 3. seagrass -> tall_seagrass in 2-high source water.
	c.SetBlock(12, FlatSurfaceY+1, 8, StateWater)
	c.SetBlock(12, FlatSurfaceY+2, 8, StateWater)
	seagrassState, _ := nameToStateID("minecraft:seagrass", nil)
	c.SetBlock(12, FlatSurfaceY+1, 8, seagrassState)
	if ok, _ := c.ApplyBoneMeal(12, FlatSurfaceY+1, 8, rng); !ok {
		t.Fatalf("expected ApplyBoneMeal on seagrass with water above to succeed")
	}
	if !isBlockNamed(c.GetBlock(12, FlatSurfaceY+1, 8), "minecraft:tall_seagrass") || !isBlockNamed(c.GetBlock(12, FlatSurfaceY+2, 8), "minecraft:tall_seagrass") {
		t.Fatalf("expected seagrass to become double-height tall_seagrass")
	}

	// 4. pink_petals flower_amount 1..4 and item drop at 4; tall flower item drop.
	petals1, _ := nameToStateID("minecraft:pink_petals", map[string]string{"facing": "north", "flower_amount": "1"})
	c.SetBlock(14, FlatSurfaceY+1, 8, petals1)
	for wantAmt := 2; wantAmt <= 4; wantAmt++ {
		if ok, _ := c.ApplyBoneMeal(14, FlatSurfaceY+1, 8, rng); !ok {
			t.Fatalf("expected ApplyBoneMeal on pink_petals to reach flower_amount=%d", wantAmt)
		}
	}
	petals4 := c.GetBlock(14, FlatSurfaceY+1, 8)
	if drops := BoneMealDropsForBlock(petals4); len(drops) != 1 || drops[0].ResolvedID() != "minecraft:pink_petals" {
		t.Fatalf("BoneMealDropsForBlock(pink_petals[4]) = %+v, want 1x pink_petals", drops)
	}
	sunflowerLower, _ := nameToStateID("minecraft:sunflower", map[string]string{"half": "lower"})
	if drops := BoneMealDropsForBlock(sunflowerLower); len(drops) != 1 || drops[0].ResolvedID() != "minecraft:sunflower" {
		t.Fatalf("BoneMealDropsForBlock(sunflower) = %+v, want 1x sunflower", drops)
	}

	// 5. small_dripleaf -> big_dripleaf, and big_dripleaf extends upward.
	clayState, _ := nameToStateID("minecraft:clay", nil)
	smallDripLower, _ := nameToStateID("minecraft:small_dripleaf", map[string]string{"facing": "north", "half": "lower", "waterlogged": "false"})
	smallDripUpper, _ := nameToStateID("minecraft:small_dripleaf", map[string]string{"facing": "north", "half": "upper", "waterlogged": "false"})
	c.SetBlock(4, FlatSurfaceY, 12, clayState)
	c.SetBlock(4, FlatSurfaceY+1, 12, smallDripLower)
	c.SetBlock(4, FlatSurfaceY+2, 12, smallDripUpper)
	if ok, _ := c.ApplyBoneMeal(4, FlatSurfaceY+1, 12, rng); !ok {
		t.Fatalf("expected ApplyBoneMeal on small_dripleaf to succeed")
	}
	if !isBlockNamed(c.GetBlock(4, FlatSurfaceY+1, 12), "minecraft:big_dripleaf_stem") {
		t.Fatalf("expected base of grown small_dripleaf to be big_dripleaf_stem, got %s", BlockName(c.GetBlock(4, FlatSurfaceY+1, 12)))
	}
}

func TestEntityFarmlandTramplePredicatesAndFarmerVillagerHelpers(t *testing.T) {
	c := NewCache(256, GenerateFlat)
	rng := rand.New(rand.NewSource(1234))

	// 1. CanEntityTrampleFarmland:
	// - Player ignores mobGriefing=false
	if !CanEntityTrampleFarmland(true, false, 0.6, 1.8, 3.0, rng) {
		t.Fatalf("expected player with fallDistance=3.0 to trample farmland even when mobGriefing=false")
	}
	// - Non-player mob respects mobGriefing=false
	if CanEntityTrampleFarmland(false, false, 0.9, 1.4, 3.0, rng) {
		t.Fatalf("expected mob not to trample farmland when mobGriefing=false")
	}
	// - Small mob (chicken: 0.4 * 0.4 * 0.7 = 0.112 <= 0.512) cannot trample farmland
	if CanEntityTrampleFarmland(false, true, 0.4, 0.7, 3.0, rng) {
		t.Fatalf("expected small mob (volume <= 0.512) not to trample farmland")
	}
	// - Adult mob (cow: 0.9 * 0.9 * 1.4 = 1.134 > 0.512) tramples farmland when mobGriefing=true
	if !CanEntityTrampleFarmland(false, true, 0.9, 1.4, 3.0, rng) {
		t.Fatalf("expected cow-sized mob (volume > 0.512) to trample farmland when mobGriefing=true")
	}

	// 2. Farmer Villager harvest & replant helpers:
	farmlandWet, _ := nameToStateID("minecraft:farmland", map[string]string{"moisture": "7"})
	matureWheat, _ := nameToStateID("minecraft:wheat", map[string]string{"age": "7"})
	c.SetBlock(6, FlatSurfaceY, 10, farmlandWet)
	c.SetBlock(6, FlatSurfaceY+1, 10, matureWheat)

	harvestable, plantable := c.CanFarmerWorkAt(6, FlatSurfaceY+1, 10, true)
	if !harvestable || plantable {
		t.Fatalf("CanFarmerWorkAt on mature wheat = (%v, %v), want (true, false)", harvestable, plantable)
	}
	drops, consumedSeed, _, changed := c.FarmerHarvestAndReplantAt(6, FlatSurfaceY+1, 10, "minecraft:wheat_seeds", rng)
	if !changed || !consumedSeed || len(drops) == 0 {
		t.Fatalf("FarmerHarvestAndReplantAt = (drops=%+v, consumedSeed=%v, changed=%v), want all true/non-empty", drops, consumedSeed, changed)
	}
	replantedInfo, _ := stateByID(c.GetBlock(6, FlatSurfaceY+1, 10))
	if replantedInfo.Name != "minecraft:wheat" || replantedInfo.Properties["age"] != "0" {
		t.Fatalf("expected replanted wheat[age=0], got %+v", replantedInfo)
	}
}

func TestSaplingFlowerBeeNestAzaleaRootedDirtAndMegaSprucePodzol(t *testing.T) {
	c := NewCache(256, GenerateFlat)
	rng := rand.New(rand.NewSource(4444))

	// 1. Oak sapling with a nearby flower selects oak_bees_005 or fancy_oak_bees_005.
	dandelion, _ := nameToStateID("minecraft:dandelion", nil)
	c.SetBlock(9, FlatSurfaceY+1, 10, dandelion)
	feat, _, _, _ := c.selectSaplingTreeFeature(8, FlatSurfaceY+1, 10, "minecraft:oak_sapling", rng)
	if feat != "minecraft:oak_bees_005" && feat != "minecraft:fancy_oak_bees_005" {
		t.Fatalf("selectSaplingTreeFeature with nearby flower = %q, want oak_bees_005 or fancy_oak_bees_005", feat)
	}

	// 2. Azalea grown into an oak tree converts dirt beneath the trunk to rooted_dirt.
	azaleaState, _ := nameToStateID("minecraft:azalea", nil)
	c.SetBlock(20, FlatSurfaceY+1, 20, azaleaState)
	for i := 0; i < 20; i++ {
		c.ApplyBoneMeal(20, FlatSurfaceY+1, 20, rng)
		if isLogOrStemBlock(c.GetBlock(20, FlatSurfaceY+1, 20)) {
			break
		}
	}
	if !isBlockNamed(c.GetBlock(20, FlatSurfaceY, 20), "minecraft:rooted_dirt") {
		t.Fatalf("expected azalea tree growth to convert soil at (20, FlatSurfaceY, 20) to rooted_dirt, got %s", BlockName(c.GetBlock(20, FlatSurfaceY, 20)))
	}

	// 3. 2x2 spruce_sapling grown into mega spruce converts nearby grass_block to podzol.
	spruceSapling, _ := nameToStateID("minecraft:spruce_sapling", map[string]string{"stage": "1"})
	c.SetBlock(36, FlatSurfaceY+1, 36, spruceSapling)
	c.SetBlock(37, FlatSurfaceY+1, 36, spruceSapling)
	c.SetBlock(36, FlatSurfaceY+1, 37, spruceSapling)
	c.SetBlock(37, FlatSurfaceY+1, 37, spruceSapling)
	for i := 0; i < 20; i++ {
		c.ApplyBoneMeal(36, FlatSurfaceY+1, 36, rng)
		if isLogOrStemBlock(c.GetBlock(36, FlatSurfaceY+1, 36)) {
			break
		}
	}
	foundPodzol := false
	for dx := -3; dx <= 4 && !foundPodzol; dx++ {
		for dz := -3; dz <= 4 && !foundPodzol; dz++ {
			if isBlockNamed(c.GetBlock(36+dx, FlatSurfaceY, 36+dz), "minecraft:podzol") {
				foundPodzol = true
			}
		}
	}
	if !foundPodzol {
		t.Fatalf("expected 2x2 mega spruce tree growth to convert nearby grass_block to podzol")
	}
}
