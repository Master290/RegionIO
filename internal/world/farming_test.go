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
