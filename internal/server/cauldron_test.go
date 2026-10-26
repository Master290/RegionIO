package server

import (
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func newBeaconTestServer(t *testing.T) (*Server, *world.Cache) {
	t.Helper()
	srv, _ := newTestEnchantServer(t)
	cache := srv.DimensionChunks(world.DimensionOverworld)
	// Clear sky column around test area (0..16, 60..319, 0..16)
	for x := 0; x <= 16; x++ {
		for z := 0; z <= 16; z++ {
			for y := 60; y <= BeaconDefaultMaxBuild; y++ {
				cache.SetBlock(x, y, z, world.StateAir)
			}
		}
	}
	return srv, cache
}

func newBeaconTestPlayer(s *Server, name string, x, y, z float64) *PlayerSession {
	sess, err := s.RegisterPlayer(Profile{
		UUID: OfflineUUID(name),
		Name: name,
	}, func(id int32, body []byte) error {
		return nil
	})
	if err != nil {
		panic(err)
	}
	sess.mu.Lock()
	sess.gameMode = GameModeSurvival
	sess.mu.Unlock()
	setTestPlayerPos(sess, x, y, z)
	return sess
}

func mustBlockState(t *testing.T, name string) uint16 {
	t.Helper()
	st, ok := mustTestBlockState(name)
	if !ok {
		t.Fatalf("missing block state for %s", name)
	}
	return st
}

func TestCauldronBlockStatesAndComparatorSignal(t *testing.T) {
	dim := world.DimensionOverworld
	s, cache := newBeaconTestServer(t)
	s.BindNoteBlockAndJukeboxRedstone(dim)

	x, y, z := 10, 64, 10
	// Place a redstone comparator at (x+1, y, z) with facing="west" (rear input at (x, y, z)).
	compState, ok := world.BlockStateIDByProperties("minecraft:comparator", map[string]string{
		"facing":  "west",
		"mode":    "compare",
		"powered": "false",
	})
	if !ok {
		t.Fatalf("missing comparator block state")
	}
	stone := mustBlockState(t, "minecraft:stone")
	dust := mustBlockState(t, "minecraft:redstone_wire")
	cache.SetBlock(x+1, y-1, z, stone)
	cache.SetBlock(x+2, y-1, z, stone)
	cache.SetBlock(x+1, y, z, compState)
	cache.SetBlock(x+2, y, z, dust)

	// 1. Empty cauldron -> signal 0
	s.setCauldronBlockAndNotify(dim, cache, x, y, z, EmptyCauldronBlockState(), "")
	if sig := s.CauldronComparatorSignalAt(dim, x, y, z); sig != 0 {
		t.Fatalf("empty cauldron signal = %d, want 0", sig)
	}
	if sig := s.ComparatorSignalAt(dim, x, y, z); sig != 0 {
		t.Fatalf("ComparatorSignalAt(empty cauldron) = %d, want 0", sig)
	}

	// 2. Water cauldron levels 1..3 -> signals 1..3
	for lvl := 1; lvl <= 3; lvl++ {
		st := CauldronWaterBlockState(lvl)
		kind, gotLvl, isCauld := ParseCauldronState(st)
		if !isCauld || kind != CauldronKindWater || gotLvl != lvl {
			t.Fatalf("ParseCauldronState(water %d) = (%v, %d, %v)", lvl, kind, gotLvl, isCauld)
		}
		s.setCauldronBlockAndNotify(dim, cache, x, y, z, st, "")
		if sig := s.CauldronComparatorSignalAt(dim, x, y, z); sig != lvl {
			t.Fatalf("water cauldron level %d signal = %d, want %d", lvl, sig, lvl)
		}
		if sig := s.ComparatorSignalAt(dim, x, y, z); sig != lvl {
			t.Fatalf("ComparatorSignalAt(water %d) = %d, want %d", lvl, sig, lvl)
		}
		// Check that the adjacent comparator at (x+1, y, z) became powered.
		poweredComp, _ := world.BlockStateIDByProperties("minecraft:comparator", map[string]string{
			"facing":  "west",
			"mode":    "compare",
			"powered": "true",
		})
		if gotComp := cache.GetBlock(x+1, y, z); gotComp != poweredComp {
			t.Fatalf("water cauldron level %d: comparator state = %d, want powered (%d)", lvl, gotComp, poweredComp)
		}
	}

	// 3. Powder snow cauldron levels 1..3 -> signals 1..3
	for lvl := 1; lvl <= 3; lvl++ {
		st := CauldronPowderSnowBlockState(lvl)
		kind, gotLvl, isCauld := ParseCauldronState(st)
		if !isCauld || kind != CauldronKindPowderSnow || gotLvl != lvl {
			t.Fatalf("ParseCauldronState(powder_snow %d) = (%v, %d, %v)", lvl, kind, gotLvl, isCauld)
		}
		if sig := CauldronComparatorSignal(st); sig != lvl {
			t.Fatalf("powder snow cauldron level %d signal = %d, want %d", lvl, sig, lvl)
		}
	}

	// 4. Lava cauldron -> always level 3 and comparator signal 3
	lavaSt := CauldronLavaBlockState()
	if kind, gotLvl, isCauld := ParseCauldronState(lavaSt); !isCauld || kind != CauldronKindLava || gotLvl != 3 {
		t.Fatalf("ParseCauldronState(lava) = (%v, %d, %v), want (lava, 3, true)", kind, gotLvl, isCauld)
	}
	s.setCauldronBlockAndNotify(dim, cache, x, y, z, lavaSt, "")
	if sig := s.CauldronComparatorSignalAt(dim, x, y, z); sig != 3 {
		t.Fatalf("lava cauldron signal = %d, want 3", sig)
	}
}

func TestCauldronBucketInteractions(t *testing.T) {
	s, cache := newBeaconTestServer(t)
	session := newBeaconTestPlayer(s, "CauldronBucketeer", 0, 64, 0)
	slot := world.HotbarContainerOffset

	x, y, z := 2, 64, 2
	cache.SetBlock(x, y, z, EmptyCauldronBlockState())

	// 1. Fill empty cauldron with water_bucket -> water_cauldron[level=3], item becomes bucket.
	session.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:water_bucket", 1))
	handled, _ := s.InteractCauldronAt(session, x, y, z, slot)
	if !handled {
		t.Fatalf("expected water_bucket on empty cauldron to be handled")
	}
	if got := cache.GetBlock(x, y, z); got != CauldronWaterBlockState(3) {
		t.Fatalf("after water_bucket, block = %s, want water_cauldron[level=3]", world.BlockName(got))
	}
	if held := session.InventorySlot(slot); held.ResolvedID() != "minecraft:bucket" || held.Count != 1 {
		t.Fatalf("after water_bucket, held = %s x%d, want minecraft:bucket x1", held.ResolvedID(), held.Count)
	}

	// 2. Empty bucket on level-3 water_cauldron -> empty cauldron, item becomes water_bucket.
	handled, _ = s.InteractCauldronAt(session, x, y, z, slot)
	if !handled {
		t.Fatalf("expected empty bucket on level-3 water_cauldron to be handled")
	}
	if got := cache.GetBlock(x, y, z); got != EmptyCauldronBlockState() {
		t.Fatalf("after scooping water_cauldron, block = %s, want cauldron", world.BlockName(got))
	}
	if held := session.InventorySlot(slot); held.ResolvedID() != "minecraft:water_bucket" {
		t.Fatalf("after scooping water_cauldron, held = %s, want minecraft:water_bucket", held.ResolvedID())
	}

	// 3. Fill with lava_bucket and scoop with empty bucket.
	session.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:lava_bucket", 1))
	if handled, _ = s.InteractCauldronAt(session, x, y, z, slot); !handled {
		t.Fatalf("expected lava_bucket on cauldron to be handled")
	}
	if got := cache.GetBlock(x, y, z); got != CauldronLavaBlockState() {
		t.Fatalf("after lava_bucket, block = %s, want lava_cauldron", world.BlockName(got))
	}
	if handled, _ = s.InteractCauldronAt(session, x, y, z, slot); !handled {
		t.Fatalf("expected empty bucket on lava_cauldron to be handled")
	}
	if held := session.InventorySlot(slot); held.ResolvedID() != "minecraft:lava_bucket" {
		t.Fatalf("after scooping lava_cauldron, held = %s, want minecraft:lava_bucket", held.ResolvedID())
	}

	// 4. Fill with powder_snow_bucket, lower to level 2, verify empty bucket cannot scoop level 2.
	session.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:powder_snow_bucket", 1))
	if handled, _ = s.InteractCauldronAt(session, x, y, z, slot); !handled {
		t.Fatalf("expected powder_snow_bucket on cauldron to be handled")
	}
	if got := cache.GetBlock(x, y, z); got != CauldronPowderSnowBlockState(3) {
		t.Fatalf("after powder_snow_bucket, block = %s, want powder_snow_cauldron[level=3]", world.BlockName(got))
	}
	nextSt, _ := LowerCauldronFillLevel(cache.GetBlock(x, y, z))
	cache.SetBlock(x, y, z, nextSt)
	if handled, _ = s.InteractCauldronAt(session, x, y, z, slot); handled {
		t.Fatalf("empty bucket should NOT scoop level-2 powder_snow_cauldron")
	}
}

func TestCauldronWaterBottlesAndNoPotionCauldron(t *testing.T) {
	s, cache := newBeaconTestServer(t)
	session := newBeaconTestPlayer(s, "CauldronBottler", 0, 64, 0)
	slot := world.HotbarContainerOffset

	x, y, z := 4, 64, 4
	cache.SetBlock(x, y, z, EmptyCauldronBlockState())

	// 1. Non-water potions (swiftness, healing, awkward, splash water, lingering water) must be rejected!
	// Java Edition has NO potion cauldrons.
	type badCase struct {
		item       string
		potionType string
	}
	badPotions := []badCase{
		{"minecraft:potion", PotionSwiftness},
		{"minecraft:potion", PotionHealing},
		{"minecraft:potion", PotionAwkward},
		{"minecraft:splash_potion", PotionWater},
		{"minecraft:lingering_potion", PotionWater},
	}
	for _, bc := range badPotions {
		session.SetInventorySlot(slot, world.NewNamedItemStack(bc.item, 1))
		SetSlotPotionType(session, slot, bc.potionType)
		if handled, _ := s.InteractCauldronAt(session, x, y, z, slot); handled {
			t.Fatalf("expected non-water/splash/lingering potion %s (%s) to be rejected by cauldron", bc.item, bc.potionType)
		}
		if got := cache.GetBlock(x, y, z); got != EmptyCauldronBlockState() {
			t.Fatalf("cauldron state changed after rejected potion: %s", world.BlockName(got))
		}
	}

	// 2. Pour 3 water bottles into empty cauldron -> levels 1, 2, 3, returning glass_bottle each time.
	for wantLvl := 1; wantLvl <= 3; wantLvl++ {
		session.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:potion", 1))
		SetSlotPotionType(session, slot, PotionWater)
		handled, _ := s.InteractCauldronAt(session, x, y, z, slot)
		if !handled {
			t.Fatalf("expected water bottle #%d to fill cauldron", wantLvl)
		}
		if got := cache.GetBlock(x, y, z); got != CauldronWaterBlockState(wantLvl) {
			t.Fatalf("after water bottle #%d, state = %d, want level %d", wantLvl, got, wantLvl)
		}
		if held := session.InventorySlot(slot); held.ResolvedID() != "minecraft:glass_bottle" {
			t.Fatalf("after pouring water bottle, held = %s, want minecraft:glass_bottle", held.ResolvedID())
		}
	}

	// 3. A 4th water bottle on a full level-3 cauldron must fail.
	session.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:potion", 1))
	SetSlotPotionType(session, slot, PotionWater)
	if handled, _ := s.InteractCauldronAt(session, x, y, z, slot); handled {
		t.Fatalf("expected water bottle on full level-3 cauldron to fail")
	}

	// 4. Scoop 3 glass bottles from level-3 water_cauldron -> levels 2, 1, empty cauldron.
	for step := 1; step <= 3; step++ {
		session.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:glass_bottle", 1))
		handled, _ := s.InteractCauldronAt(session, x, y, z, slot)
		if !handled {
			t.Fatalf("expected glass_bottle step %d to scoop water from cauldron", step)
		}
		held := session.InventorySlot(slot)
		if held.ResolvedID() != "minecraft:potion" || GetSlotPotionType(session, slot) != PotionWater {
			t.Fatalf("step %d: expected water potion bottle, got %s (%s)", step, held.ResolvedID(), GetSlotPotionType(session, slot))
		}
	}
	if got := cache.GetBlock(x, y, z); got != EmptyCauldronBlockState() {
		t.Fatalf("after 3 glass bottles, cauldron should be empty, got %s", world.BlockName(got))
	}
}

func TestCauldronWashingLeatherBannerAndShulkerBox(t *testing.T) {
	s, cache := newBeaconTestServer(t)
	session := newBeaconTestPlayer(s, "CauldronWasher", 0, 64, 0)
	slot := world.HotbarContainerOffset

	x, y, z := 6, 64, 6
	cache.SetBlock(x, y, z, CauldronWaterBlockState(3))

	// 1. Dyed leather armor washing:
	session.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:leather_chestplate", 1))
	// Undyed leather armor should NOT wash or consume water.
	if handled, _ := s.InteractCauldronAt(session, x, y, z, slot); handled {
		t.Fatalf("undyed leather_chestplate should not be washed")
	}
	SetSlotDyedColor(session, slot, 0xFF3366)
	if !HasSlotDyedColor(session, slot) {
		t.Fatalf("expected leather_chestplate to have dyed color")
	}
	if handled, _ := s.InteractCauldronAt(session, x, y, z, slot); !handled {
		t.Fatalf("expected dyed leather_chestplate to be washed in water_cauldron")
	}
	if HasSlotDyedColor(session, slot) {
		t.Fatalf("expected dyed color to be cleared after washing")
	}
	if got := cache.GetBlock(x, y, z); got != CauldronWaterBlockState(2) {
		t.Fatalf("after washing leather armor, expected water level 2, got %d", got)
	}

	// 2. Patterned banner washing (removes top layer only):
	session.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:white_banner", 1))
	SetSlotBannerPatterns(session, slot, BannerPatternsComponent{
		Layers: []BannerPatternLayer{
			{Pattern: "minecraft:stripe_bottom", Color: "red"},
			{Pattern: "minecraft:cross", Color: "blue"},
		},
	})
	if handled, _ := s.InteractCauldronAt(session, x, y, z, slot); !handled {
		t.Fatalf("expected 2-layer banner to be washed")
	}
	comp := GetSlotBannerPatterns(session, slot)
	if len(comp.Layers) != 1 || comp.Layers[0].Pattern != "minecraft:stripe_bottom" {
		t.Fatalf("expected 1 remaining bottom layer after washing, got %+v", comp.Layers)
	}
	if got := cache.GetBlock(x, y, z); got != CauldronWaterBlockState(1) {
		t.Fatalf("after washing banner, expected water level 1, got %d", got)
	}

	// 3. Dyed shulker box washing (preserves ItemMeta and converts to minecraft:shulker_box):
	session.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:red_shulker_box", 1))
	SetSlotItemMeta(session, slot, ItemMeta{CustomName: "My Loot"})
	if handled, _ := s.InteractCauldronAt(session, x, y, z, slot); !handled {
		t.Fatalf("expected red_shulker_box to be washed in level-1 water_cauldron")
	}
	washed := session.InventorySlot(slot)
	if washed.ResolvedID() != "minecraft:shulker_box" {
		t.Fatalf("washed shulker = %s, want minecraft:shulker_box", washed.ResolvedID())
	}
	if gotMeta := GetSlotItemMeta(session, slot); gotMeta.CustomName != "My Loot" {
		t.Fatalf("expected shulker ItemMeta to be preserved after washing, got %+v", gotMeta)
	}
	if got := cache.GetBlock(x, y, z); got != EmptyCauldronBlockState() {
		t.Fatalf("after washing 3rd item, cauldron should be empty, got %s", world.BlockName(got))
	}
}

func TestCauldronPrecipitationAndDripstoneFilling(t *testing.T) {
	dim := world.DimensionOverworld
	s, cache := newBeaconTestServer(t)

	x, y, z := 8, 64, 8
	cache.SetBlock(x, y, z, EmptyCauldronBlockState())

	// 1. Rain fills empty cauldron -> water_cauldron[level=1] when sky is clear.
	if ok, _ := s.TryFillCauldronFromPrecipitation(dim, x, y, z, "rain", nil, true); !ok {
		t.Fatalf("expected rain (force=true) to fill cauldron to level 1")
	}
	if got := cache.GetBlock(x, y, z); got != CauldronWaterBlockState(1) {
		t.Fatalf("after rain fill, block = %s, want water_cauldron[level=1]", world.BlockName(got))
	}

	// Snow should not fill a water_cauldron.
	if ok, _ := s.TryFillCauldronFromPrecipitation(dim, x, y, z, "snow", nil, true); ok {
		t.Fatalf("snow should not fill a water_cauldron")
	}

	// Roof above cauldron blocks precipitation.
	cache.SetBlock(x, y+5, z, mustBlockState(t, "minecraft:stone"))
	if ok, _ := s.TryFillCauldronFromPrecipitation(dim, x, y, z, "rain", nil, true); ok {
		t.Fatalf("rain should not fill cauldron when a solid block is above it")
	}
	cache.SetBlock(x, y+5, z, world.StateAir)

	// 2. Snow fills empty cauldron -> powder_snow_cauldron[level=1].
	cache.SetBlock(x, y, z, EmptyCauldronBlockState())
	rng := rand.New(rand.NewSource(1))
	if ok, _ := s.TryFillCauldronFromPrecipitation(dim, x, y, z, "snow", rng, true); !ok {
		t.Fatalf("expected snow (force=true) to fill empty cauldron")
	}
	if got := cache.GetBlock(x, y, z); got != CauldronPowderSnowBlockState(1) {
		t.Fatalf("after snow fill, block = %s, want powder_snow_cauldron[level=1]", world.BlockName(got))
	}

	// 3. Pointed dripstone (stalactite tip hanging under stone with water/lava above):
	cache.SetBlock(x, y, z, EmptyCauldronBlockState())
	tipDown, ok := world.BlockStateIDByProperties("minecraft:pointed_dripstone", map[string]string{
		"thickness":          "tip",
		"vertical_direction": "down",
		"waterlogged":        "false",
	})
	if !ok {
		t.Fatalf("missing pointed_dripstone block state")
	}
	cache.SetBlock(x, y+3, z, tipDown)
	cache.SetBlock(x, y+4, z, mustBlockState(t, "minecraft:stone"))
	cache.SetBlock(x, y+5, z, mustBlockState(t, "minecraft:water"))

	if fluid := FindDripstoneFluidAboveCauldron(cache.GetBlock, x, y, z); fluid != "water" {
		t.Fatalf("FindDripstoneFluidAboveCauldron = %q, want \"water\"", fluid)
	}
	if filled, _ := s.TryFillCauldronFromDripstone(dim, x, y, z, nil, true); !filled {
		t.Fatalf("expected pointed dripstone with water source above to fill cauldron")
	}
	if got := cache.GetBlock(x, y, z); got != CauldronWaterBlockState(1) {
		t.Fatalf("after dripstone water fill, block = %s, want water_cauldron[level=1]", world.BlockName(got))
	}

	// Switch source above stalactite to lava and reset cauldron to empty -> fills with lava_cauldron.
	cache.SetBlock(x, y, z, EmptyCauldronBlockState())
	cache.SetBlock(x, y+5, z, mustBlockState(t, "minecraft:lava"))
	if filled, _ := s.TryFillCauldronFromDripstone(dim, x, y, z, nil, true); !filled {
		t.Fatalf("expected pointed dripstone with lava source above to fill cauldron")
	}
	if got := cache.GetBlock(x, y, z); got != CauldronLavaBlockState() {
		t.Fatalf("after dripstone lava fill, block = %s, want lava_cauldron", world.BlockName(got))
	}
}

func TestCauldronExtinguishingAndLavaBurning(t *testing.T) {
	dim := world.DimensionOverworld
	s, cache := newBeaconTestServer(t)
	session := newBeaconTestPlayer(s, "BurningPlayer", 12.5, 64.25, 12.5)

	x, y, z := 12, 64, 12
	cache.SetBlock(x, y, z, CauldronWaterBlockState(2))

	// 1. Non-burning player inside water_cauldron does not consume water.
	if extinguished, _ := s.HandlePlayerInsideCauldron(session, x, y, z, false); extinguished {
		t.Fatalf("non-burning player should not consume water_cauldron level")
	}

	// 2. Burning player inside water_cauldron[level=2] is extinguished and lowers level to 1.
	s.SetPlayerFireTicks(session, 100)
	if !s.IsPlayerOnFire(session) {
		t.Fatalf("expected player to be on fire")
	}
	if extinguished, _ := s.HandlePlayerInsideCauldron(session, x, y, z, false); !extinguished {
		t.Fatalf("expected burning player inside water_cauldron to be extinguished")
	}
	if s.IsPlayerOnFire(session) {
		t.Fatalf("expected player fire to be extinguished by water_cauldron")
	}
	if got := cache.GetBlock(x, y, z); got != CauldronWaterBlockState(1) {
		t.Fatalf("expected water_cauldron level to drop from 2 to 1, got %d", got)
	}

	// 3. Burning mob entity inside powder_snow_cauldron[level=1] is extinguished and empties cauldron.
	cache.SetBlock(x, y, z, CauldronPowderSnowBlockState(1))
	mobID := int32(9001)
	s.SetEntityFireTicks(mobID, 80)
	if extinguished, _ := s.HandleEntityInsideCauldron(dim, mobID, x, y, z, false); !extinguished {
		t.Fatalf("expected burning mob inside powder_snow_cauldron to be extinguished")
	}
	if s.IsEntityOnFire(mobID) {
		t.Fatalf("expected mob fire to be extinguished by powder_snow_cauldron")
	}
	if got := cache.GetBlock(x, y, z); got != EmptyCauldronBlockState() {
		t.Fatalf("expected powder_snow_cauldron[level=1] to become empty cauldron after extinguishing mob")
	}

	// 4. Player inside lava_cauldron is set on fire and takes lava damage.
	cache.SetBlock(x, y, z, CauldronLavaBlockState())
	beforeHP := session.Snapshot().Health
	_, _ = s.HandlePlayerInsideCauldron(session, x, y, z, false)
	if !s.IsPlayerOnFire(session) {
		t.Fatalf("expected player inside lava_cauldron to be set on fire")
	}
	afterHP := session.Snapshot().Health
	if afterHP >= beforeHP {
		t.Fatalf("expected player health to decrease in lava_cauldron: before=%v after=%v", beforeHP, afterHP)
	}
}

func TestCauldronWashDyedWolfArmorMeta(t *testing.T) {
	s, cache := newBeaconTestServer(t)
	session := newBeaconTestPlayer(s, "WolfArmorWasher", 14.5, 64, 14.5)

	x, y, z := 14, 64, 14
	cache.SetBlock(x, y, z, CauldronWaterBlockState(2))

	const slot = 36
	session.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:wolf_armor", 1))
	dyedMeta, wantRGB, ok := DyeWolfArmorItem(ItemMeta{Damage: 5}, "minecraft:blue_dye")
	if !ok {
		t.Fatalf("expected DyeWolfArmorItem to succeed")
	}
	SetSlotItemMeta(session, slot, dyedMeta)

	if gotRGB, has := GetSlotDyedColor(session, slot); !has || gotRGB != wantRGB {
		t.Fatalf("expected GetSlotDyedColor to report WolfArmorDyedRGB (%#x, true), got (%#x, %v)", wantRGB, gotRGB, has)
	}

	ok, st := s.InteractCauldronAt(session, x, y, z, slot)
	if !ok || st != CauldronWaterBlockState(1) {
		t.Fatalf("expected washing dyed wolf_armor to lower water_cauldron from 2 to 1 (ok=%v, st=%d)", ok, st)
	}
	if HasSlotDyedColor(session, slot) {
		t.Fatalf("expected dyed color to be cleared after cauldron wash")
	}
	washedMeta := GetSlotItemMeta(session, slot)
	if _, stillDyed := WolfArmorDyedRGB(washedMeta); stillDyed {
		t.Fatalf("expected minecraft:dyed_color to be removed from wolf_armor ItemMeta")
	}
	if washedMeta.Damage != 5 {
		t.Fatalf("expected wolf_armor Damage=5 to be preserved after washing, got %d", washedMeta.Damage)
	}
}
