package world

import (
	"testing"

	"regionio/internal/worldgen"
)

func collectRegionBlockEntities(r *decorationRegion) []StructureBlockEntity {
	var out []StructureBlockEntity
	for _, ch := range r.chunks {
		if ch != nil {
			out = append(out, ch.StructureBlockEntities()...)
		}
	}
	return out
}

func collectRegionEntities(r *decorationRegion) []StructureEntity {
	var out []StructureEntity
	for _, ch := range r.chunks {
		if ch != nil {
			out = append(out, ch.StructureEntities()...)
		}
	}
	return out
}

func TestScatteredAndCoastalStructureLootAndMobs(t *testing.T) {
	// 1. Desert Pyramid + Archaeology Cellar + Desert Well
	{
		const seed int64 = 12345
		start := BuildDesertPyramidStart(seed, 0, 0)
		region := buildFlatTestDecorationRegion(t, 0, 0, 2, 64, false)
		starts := []*DesertPyramidStart{start}
		for cx := int32(-2); cx <= 2; cx++ {
			for cz := int32(-2); cz <= 2; cz++ {
				PlaceDesertPyramidsForChunk(region, starts, seed, cx, cz)
			}
		}
		bes := collectRegionBlockEntities(region)
		chests := 0
		brushables := 0
		for _, be := range bes {
			switch be.ID {
			case "minecraft:chest":
				if be.LootTable == "minecraft:chests/desert_pyramid" && len(be.Items) > 0 {
					chests++
				}
			case "minecraft:brushable_block":
				if be.LootTable == "minecraft:archaeology/desert_pyramid" && len(be.Items) == 1 {
					brushables++
				}
			}
		}
		if chests != 4 {
			t.Errorf("desert pyramid chests with loot = %d, want 4", chests)
		}
		if brushables < 6 {
			t.Errorf("desert pyramid brushable_blocks = %d, want >= 6", brushables)
		}

		// Desert Well
		wellChunk := &Chunk{X: 0, Z: 0}
		sandID, _ := nameToStateID("minecraft:sand", nil)
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				for y := 58; y <= 64; y++ {
					wellChunk.SetBlock(x, y, z, sandID)
				}
			}
		}
		if !PlaceDesertWell(wellChunk, 8, 65, 8) {
			t.Fatalf("PlaceDesertWell returned false")
		}
		wellBrush := 0
		for _, be := range wellChunk.StructureBlockEntities() {
			if be.ID == "minecraft:brushable_block" && be.LootTable == "minecraft:archaeology/desert_well" && len(be.Items) == 1 {
				wellBrush++
			}
		}
		if wellBrush != 2 {
			t.Errorf("desert well brushable_blocks = %d, want 2", wellBrush)
		}
	}

	// 2. Jungle Temple (2 chests + 2 dispensers)
	{
		const seed int64 = 54321
		start := BuildJungleTempleStart(seed, 0, 0)
		region := buildFlatTestDecorationRegion(t, 0, 0, 2, 64, false)
		starts := []*JungleTempleStart{start}
		for cx := int32(-2); cx <= 2; cx++ {
			for cz := int32(-2); cz <= 2; cz++ {
				PlaceJungleTemplesForChunk(region, starts, seed, cx, cz)
			}
		}
		chests, dispensers := 0, 0
		for _, be := range collectRegionBlockEntities(region) {
			if be.ID == "minecraft:chest" && be.LootTable == "minecraft:chests/jungle_temple" && len(be.Items) > 0 {
				chests++
			}
			if be.ID == "minecraft:dispenser" && be.LootTable == "minecraft:chests/jungle_temple_dispenser" && len(be.Items) > 0 {
				dispensers++
			}
		}
		if chests != 2 || dispensers != 2 {
			t.Errorf("jungle temple chests=%d dispensers=%d, want 2 and 2", chests, dispensers)
		}
	}

	// 3. Igloo with basement (chest, brewing_stand, villager, zombie_villager)
	{
		var iglooStart *IglooStart
		var iglooSeed int64
		for s := int64(1); s < 200; s++ {
			cand := BuildIglooStart(s, 0, 0)
			if len(cand.Pieces) > 1 {
				iglooStart = cand
				iglooSeed = s
				break
			}
		}
		if iglooStart == nil {
			t.Fatalf("failed to find igloo with basement")
		}
		region := buildFlatTestDecorationRegion(t, 0, 0, 2, 64, false)
		starts := []*IglooStart{iglooStart}
		for cx := int32(-2); cx <= 2; cx++ {
			for cz := int32(-2); cz <= 2; cz++ {
				PlaceIgloosForChunk(region, starts, iglooSeed, cx, cz)
			}
		}
		hasChest, hasBrewing := false, false
		for _, be := range collectRegionBlockEntities(region) {
			if be.ID == "minecraft:chest" && be.LootTable == "minecraft:chests/igloo_chest" && len(be.Items) > 0 {
				hasChest = true
			}
			if be.ID == "minecraft:brewing_stand" && len(be.Items) > 0 {
				hasBrewing = true
			}
		}
		entCounts := map[string]int{}
		for _, ent := range collectRegionEntities(region) {
			entCounts[ent.ID]++
		}
		if !hasChest || !hasBrewing {
			t.Errorf("igloo basement hasChest=%v hasBrewing=%v, want true/true", hasChest, hasBrewing)
		}
		if entCounts["minecraft:villager"] == 0 || entCounts["minecraft:zombie_villager"] == 0 {
			t.Errorf("igloo basement entities = %v, want villager and zombie_villager", entCounts)
		}
	}

	// 4. Swamp Hut (witch + cat)
	{
		const seed int64 = 777
		start := BuildSwampHutStart(seed, 0, 0)
		region := buildFlatTestDecorationRegion(t, 0, 0, 2, 64, false)
		starts := []*SwampHutStart{start}
		for cx := int32(-2); cx <= 2; cx++ {
			for cz := int32(-2); cz <= 2; cz++ {
				PlaceSwampHutsForChunk(region, starts, seed, cx, cz)
			}
		}
		entCounts := map[string]int{}
		for _, ent := range collectRegionEntities(region) {
			entCounts[ent.ID]++
		}
		if entCounts["minecraft:witch"] != 1 || entCounts["minecraft:cat"] != 1 {
			t.Errorf("swamp hut entities = %v, want 1 witch and 1 cat", entCounts)
		}
	}

	// 5. Shipwreck & Buried Treasure
	{
		const seed int64 = 999
		start := BuildShipwreckStart(seed, 0, 0, false)
		start.Template = "shipwreck/with_mast"
		region := buildFlatTestDecorationRegion(t, 0, 0, 2, 45, true)
		starts := []*ShipwreckStart{start}
		for cx := int32(-2); cx <= 2; cx++ {
			for cz := int32(-2); cz <= 2; cz++ {
				PlaceShipwrecksForChunk(region, starts, "minecraft:shipwreck", seed, cx, cz)
			}
		}
		tables := map[string]int{}
		for _, be := range collectRegionBlockEntities(region) {
			if be.ID == "minecraft:chest" && len(be.Items) > 0 {
				tables[be.LootTable]++
			}
		}
		if tables["minecraft:chests/shipwreck_map"] == 0 ||
			tables["minecraft:chests/shipwreck_treasure"] == 0 ||
			tables["minecraft:chests/shipwreck_supply"] == 0 {
			t.Errorf("shipwreck chest tables = %v, want map, treasure, and supply", tables)
		}

		btRegion := buildFlatTestDecorationRegion(t, 0, 0, 1, 60, false)
		PlaceBuriedTreasureInChunk(btRegion, seed, 0, 0)
		btFound := false
		for _, be := range collectRegionBlockEntities(btRegion) {
			if be.ID == "minecraft:chest" && be.LootTable == "minecraft:chests/buried_treasure" && len(be.Items) > 0 {
				btFound = true
			}
		}
		if !btFound {
			t.Errorf("buried treasure chest block entity not found")
		}
	}

	// 6. Ocean Ruin (chest + suspicious gravel/sand brushable_block + drowned)
	{
		const seed int64 = 4242
		region := buildFlatTestDecorationRegion(t, 0, 0, 2, 48, true)
		stub := &OceanRuinStub{
			Rotation:  0,
			BiomeType: "cold",
			IsLarge:   true,
			X:         0,
			Z:         0,
			Pieces: []oceanRuinPieceSpec{
				{Template: "underwater_ruin/big_brick_1", Integrity: 0.9, IsLarge: true},
			},
		}
		rng := worldgen.NewLegacy(seed)
		if err := PlaceOceanRuinPieces(region, rng, stub, seed); err != nil {
			t.Fatalf("PlaceOceanRuinPieces: %v", err)
		}
		hasChest, hasBrush := false, false
		for _, be := range collectRegionBlockEntities(region) {
			if be.ID == "minecraft:chest" && be.LootTable == "minecraft:chests/underwater_ruin_big" && len(be.Items) > 0 {
				hasChest = true
			}
			if be.ID == "minecraft:brushable_block" && be.LootTable == "minecraft:archaeology/ocean_ruin_cold" && len(be.Items) == 1 {
				hasBrush = true
			}
		}
		drowned := 0
		for _, ent := range collectRegionEntities(region) {
			if ent.ID == "minecraft:drowned" {
				drowned++
			}
		}
		if !hasChest || !hasBrush || drowned == 0 {
			t.Errorf("ocean ruin hasChest=%v hasBrush=%v drowned=%d", hasChest, hasBrush, drowned)
		}
	}
}

func TestMonumentMansionStrongholdMineshaftDungeonLootAndMobs(t *testing.T) {
	// 1. Ocean Monument (3 elder_guardian entities)
	{
		const seed int64 = 12345
		start := BuildOceanMonumentStart(seed, 0, 0)
		region := buildFlatTestDecorationRegion(t, 0, 0, 3, 35, true)
		starts := []*OceanMonumentStart{start}
		for cx := int32(-3); cx <= 3; cx++ {
			for cz := int32(-3); cz <= 3; cz++ {
				PlaceOceanMonumentsForChunk(region, starts, seed, cx, cz)
			}
		}
		elders := 0
		for _, ent := range collectRegionEntities(region) {
			if ent.ID == "minecraft:elder_guardian" && ent.PersistenceRequired {
				elders++
			}
		}
		if elders != 3 {
			t.Errorf("ocean monument elder_guardian count = %d, want 3", elders)
		}
	}

	// 2. Woodland Mansion (chests, spider spawner, evoker, vindicator, allay)
	{
		const seed int64 = 12345
		start := BuildWoodlandMansionStart(seed, 0, 0, [3]int{7, 65, 7})
		region := buildFlatTestDecorationRegion(t, 0, 0, 3, 62, false)
		starts := []*WoodlandMansionStart{start}
		for cx := int32(-3); cx <= 3; cx++ {
			for cz := int32(-3); cz <= 3; cz++ {
				PlaceWoodlandMansionsForChunk(region, starts, seed, cx, cz)
			}
		}
		chests, spiderSpawners := 0, 0
		for _, be := range collectRegionBlockEntities(region) {
			if be.ID == "minecraft:chest" && len(be.Items) > 0 {
				chests++
			}
			if be.ID == "minecraft:mob_spawner" && be.SpawnerEntityID == "minecraft:spider" {
				spiderSpawners++
			}
		}
		entCounts := map[string]int{}
		for _, ent := range collectRegionEntities(region) {
			entCounts[ent.ID]++
		}
		if chests == 0 || spiderSpawners == 0 {
			t.Errorf("woodland mansion chests=%d spiderSpawners=%d, want >0", chests, spiderSpawners)
		}
		for _, mob := range []string{"minecraft:evoker", "minecraft:vindicator", "minecraft:allay"} {
			if entCounts[mob] == 0 {
				t.Errorf("woodland mansion missing %s (counts=%v)", mob, entCounts)
			}
		}
	}

	// 3. Stronghold (corridor/crossing/library chests + silverfish portal room spawner)
	{
		const seed int64 = 12345
		start := BuildStrongholdStart(seed, 0, 0)
		region := buildFlatTestDecorationRegion(t, 0, 0, 6, 64, false)
		starts := []*StrongholdStart{start}
		for cx := int32(-6); cx <= 6; cx++ {
			for cz := int32(-6); cz <= 6; cz++ {
				PlaceStrongholdsForChunk(region, starts, seed, cx, cz)
			}
		}
		chests := 0
		silverfishSpawner := false
		for _, be := range collectRegionBlockEntities(region) {
			if be.ID == "minecraft:chest" && len(be.Items) > 0 {
				chests++
			}
			if be.ID == "minecraft:mob_spawner" && be.SpawnerEntityID == "minecraft:silverfish" {
				silverfishSpawner = true
			}
		}
		if !silverfishSpawner || chests == 0 {
			t.Errorf("stronghold silverfishSpawner=%v chests=%d", silverfishSpawner, chests)
		}
	}

	// 4. Mineshaft (cave_spider spawner + chest_minecart)
	{
		region := buildFlatTestDecorationRegion(t, 0, 0, 3, 64, false)
		region.bindSeed(12345)
		var starts []*MineshaftStart
		for sx := int32(-2); sx <= 2; sx++ {
			for sz := int32(-2); sz <= 2; sz++ {
				st := BuildMineshaftStart(12345, sx, sz, false)
				if st != nil {
					for i := range st.Pieces {
						if st.Pieces[i].kind == msKindCorridor {
							st.Pieces[i].spider = true
							st.Pieces[i].hasRails = true
						}
					}
					starts = append(starts, st)
				}
			}
		}
		for cx := int32(-3); cx <= 3; cx++ {
			for cz := int32(-3); cz <= 3; cz++ {
				PlaceMineshaftsForChunk(region, starts, 12345, cx, cz)
			}
		}
		caveSpiderSpawners := 0
		for _, be := range collectRegionBlockEntities(region) {
			if be.ID == "minecraft:mob_spawner" && be.SpawnerEntityID == "minecraft:cave_spider" {
				caveSpiderSpawners++
			}
		}
		minecarts := 0
		for _, ent := range collectRegionEntities(region) {
			if ent.ID == "minecraft:chest_minecart" && ent.LootTable == "minecraft:chests/abandoned_mineshaft" && len(ent.Items) > 0 {
				minecarts++
			}
		}
		if caveSpiderSpawners == 0 || minecarts == 0 {
			t.Errorf("mineshaft caveSpiderSpawners=%d chest_minecarts=%d", caveSpiderSpawners, minecarts)
		}
	}
}

func TestTrialChambersTrailRuinsNetherAndEndLootAndMobs(t *testing.T) {
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatalf("LoadStructureSets: %v", err)
	}
	flatSurface := func(x, z int, oceanFloor bool) int {
		return 64
	}

	// 1. Trial Chambers (vaults, trial_spawners, chests/barrels/dispensers/decorated_pots)
	{
		def := sets.Structures["minecraft:trial_chambers"]
		var start *JigsawStart
		for sx := int32(0); sx < 10; sx++ {
			s, err := BuildJigsawStart(nil, sets, def, 1000+int64(sx), sx, 0, flatSurface)
			if err == nil && s != nil && len(s.Pieces) > 10 {
				start = s
				break
			}
		}
		if start == nil {
			t.Fatalf("failed to build trial_chambers start")
		}
		centerCX := int32((start.Box.minX + start.Box.maxX) / 32)
		centerCZ := int32((start.Box.minZ + start.Box.maxZ) / 32)
		region := buildFlatTestDecorationRegion(t, centerCX, centerCZ, 3, 64, false)
		starts := []*JigsawStart{start}
		for cx := centerCX - 3; cx <= centerCX+3; cx++ {
			for cz := centerCZ - 3; cz <= centerCZ+3; cz++ {
				PlaceJigsawStartsForChunk(region, starts, "minecraft:trial_chambers", 3, 1000, cx, cz)
			}
		}
		counts := map[string]int{}
		spawnerMobs := map[string]int{}
		vaultKeys := map[string]int{}
		for _, be := range collectRegionBlockEntities(region) {
			counts[be.ID]++
			if be.ID == "minecraft:trial_spawner" && be.SpawnerEntityID != "" {
				spawnerMobs[be.SpawnerEntityID]++
			}
			if be.ID == "minecraft:vault" && be.KeyItem != "" && len(be.Items) > 0 {
				vaultKeys[be.KeyItem]++
			}
		}
		if counts["minecraft:vault"] == 0 {
			t.Errorf("trial_chambers placed 0 vault block entities")
		}
		if counts["minecraft:trial_spawner"] == 0 || len(spawnerMobs) == 0 {
			t.Errorf("trial_chambers trial_spawners=%d spawnerMobs=%v", counts["minecraft:trial_spawner"], spawnerMobs)
		}
		if counts["minecraft:chest"]+counts["minecraft:barrel"]+counts["minecraft:dispenser"] == 0 {
			t.Errorf("trial_chambers containers=%v", counts)
		}
		if counts["minecraft:decorated_pot"] == 0 {
			t.Errorf("trial_chambers decorated_pots=0")
		}
		t.Logf("trial_chambers block entities: %v, vaultKeys=%v, spawnerMobs=%v", counts, vaultKeys, spawnerMobs)
	}

	// 2. Trail Ruins (brushable_block with trail_ruins_common / trail_ruins_rare)
	{
		def := sets.Structures["minecraft:trail_ruins"]
		start, err := BuildJigsawStart(nil, sets, def, 2024, 0, 0, flatSurface)
		if err != nil || start == nil {
			t.Fatalf("BuildJigsawStart trail_ruins: %v", err)
		}
		region := buildFlatTestDecorationRegion(t, 0, 0, 2, 64, false)
		gravelID, _ := nameToStateID("minecraft:gravel", nil)
		for cx := int32(-2); cx <= 2; cx++ {
			for cz := int32(-2); cz <= 2; cz++ {
				PlaceJigsawStartsForChunk(region, []*JigsawStart{start}, "minecraft:trail_ruins", 3, 2024, cx, cz)
			}
		}
		_ = gravelID
		brushCount := 0
		for _, be := range collectRegionBlockEntities(region) {
			if be.ID == "minecraft:brushable_block" && len(be.Items) == 1 {
				brushCount++
			}
		}
		if brushCount == 0 {
			t.Errorf("trail_ruins placed 0 brushable_block entities")
		}
	}

	// 3. Bastion Remnant (chests, magma_cube spawner, piglin_brute / piglin)
	{
		def := sets.Structures["minecraft:bastion_remnant"]
		var bastionChests, magmaSpawners, brutes, piglins int
		for attempt := int64(0); attempt < 12; attempt++ {
			start, err := BuildJigsawStart(nil, sets, def, 5000+attempt*77, 0, 0, flatSurface)
			if err != nil || start == nil {
				continue
			}
			region := buildFlatTestDecorationRegion(t, 0, 0, 3, 64, false)
			for cx := int32(-3); cx <= 3; cx++ {
				for cz := int32(-3); cz <= 3; cz++ {
					PlaceJigsawStartsForChunk(region, []*JigsawStart{start}, "minecraft:bastion_remnant", 4, 5000+attempt*77, cx, cz)
				}
			}
			for _, be := range collectRegionBlockEntities(region) {
				if be.ID == "minecraft:chest" && len(be.Items) > 0 {
					bastionChests++
				}
				if be.ID == "minecraft:mob_spawner" && be.SpawnerEntityID == "minecraft:magma_cube" {
					magmaSpawners++
				}
			}
			for _, ent := range collectRegionEntities(region) {
				if ent.ID == "minecraft:piglin_brute" {
					brutes++
				}
				if ent.ID == "minecraft:piglin" {
					piglins++
				}
			}
			if bastionChests > 0 && brutes > 0 && piglins > 0 {
				break
			}
		}
		if bastionChests == 0 || brutes == 0 || piglins == 0 {
			t.Errorf("bastion_remnant chests=%d brutes=%d piglins=%d magmaSpawners=%d", bastionChests, brutes, piglins, magmaSpawners)
		}
	}

	// 4. Nether Fortress (blaze spawner, nether_bridge chests, wither_skeleton + blaze entities)
	{
		const seed int64 = 12345
		fort := BuildNetherFortressStart(seed, 0, 0)
		blazeSpawners, chests, witherSkeletons, blazes := 0, 0, 0, 0
		minCX := int32(fort.Box.minX >> 4)
		maxCX := int32(fort.Box.maxX >> 4)
		minCZ := int32(fort.Box.minZ >> 4)
		maxCZ := int32(fort.Box.maxZ >> 4)
		for cx := minCX; cx <= maxCX; cx++ {
			for cz := minCZ; cz <= maxCZ; cz++ {
				ch := &Chunk{X: cx, Z: cz}
				PlaceNetherFortress(ch, fort, cx, cz)
				for _, be := range ch.StructureBlockEntities() {
					if be.ID == "minecraft:mob_spawner" && be.SpawnerEntityID == "minecraft:blaze" {
						blazeSpawners++
					}
					if be.ID == "minecraft:chest" && be.LootTable == "minecraft:chests/nether_bridge" && len(be.Items) > 0 {
						chests++
					}
				}
				for _, ent := range ch.StructureEntities() {
					if ent.ID == "minecraft:wither_skeleton" {
						witherSkeletons++
					}
					if ent.ID == "minecraft:blaze" {
						blazes++
					}
				}
			}
		}
		if blazeSpawners == 0 || chests == 0 || witherSkeletons == 0 || blazes == 0 {
			t.Errorf("nether fortress blazeSpawners=%d chests=%d witherSkeletons=%d blazes=%d",
				blazeSpawners, chests, witherSkeletons, blazes)
		}
	}

	// 5. End City with Ship (end_city_treasure chests, brewing_stand with healing potions, shulkers, elytra item_frame)
	{
		var cityWithShip *EndCityStart
		for s := int64(1); s < 200; s++ {
			rng := worldgen.NewLegacy(s)
			cand := BuildEndCityStart(0, 0, [3]int{7, 65, 7}, int(s&3), rng)
			if cand == nil {
				continue
			}
			for _, p := range cand.Pieces {
				if p.Name == "ship" {
					cityWithShip = cand
					break
				}
			}
			if cityWithShip != nil {
				break
			}
		}
		if cityWithShip == nil {
			t.Fatalf("failed to find EndCityStart with ship")
		}
		chests, brewingStands, shulkers, elytraFrames := 0, 0, 0, 0
		minCX := int32(cityWithShip.Box.minX >> 4)
		maxCX := int32(cityWithShip.Box.maxX >> 4)
		minCZ := int32(cityWithShip.Box.minZ >> 4)
		maxCZ := int32(cityWithShip.Box.maxZ >> 4)
		for cx := minCX; cx <= maxCX; cx++ {
			for cz := minCZ; cz <= maxCZ; cz++ {
				ch := &Chunk{X: cx, Z: cz}
				PlaceEndCity(ch, cityWithShip, cx, cz)
				for _, be := range ch.StructureBlockEntities() {
					if be.ID == "minecraft:chest" && be.LootTable == "minecraft:chests/end_city_treasure" && len(be.Items) > 0 {
						chests++
					}
					if be.ID == "minecraft:brewing_stand" && len(be.Items) >= 2 {
						brewingStands++
					}
				}
				for _, ent := range ch.StructureEntities() {
					if ent.ID == "minecraft:shulker" {
						shulkers++
					}
					if ent.ID == "minecraft:item_frame" && ent.Item == "minecraft:elytra" {
						elytraFrames++
					}
				}
			}
		}
		if chests == 0 || brewingStands == 0 || shulkers == 0 || elytraFrames == 0 {
			t.Errorf("end city chests=%d brewingStands=%d shulkers=%d elytraFrames=%d",
				chests, brewingStands, shulkers, elytraFrames)
		}
	}

	// 6. Chunk NBT Round-Trip for StructureBlockEntities
	{
		ch := &Chunk{X: 2, Z: 3}
		ch.AddStructureBlockEntity(StructureBlockEntity{
			ID:            "minecraft:chest",
			X:             35,
			Y:             40,
			Z:             52,
			LootTable:     "minecraft:chests/simple_dungeon",
			LootTableSeed: 987654321,
		})
		ch.AddStructureBlockEntity(StructureBlockEntity{
			ID:            "minecraft:trial_spawner",
			X:             36,
			Y:             40,
			Z:             52,
			NormalConfig:  "minecraft:trial_chamber/breeze/normal",
			OminousConfig: "minecraft:trial_chamber/breeze/ominous",
		})
		ch.AddStructureBlockEntity(StructureBlockEntity{
			ID:            "minecraft:vault",
			X:             37,
			Y:             40,
			Z:             52,
			LootTable:     "minecraft:chests/trial_chambers/reward",
			KeyItem:       "minecraft:trial_key",
			LootTableSeed: 12345,
		})
		root := chunkToNBT(ch)
		loaded, err := nbtToChunk(root, 0, 0, 2, 3)
		if err != nil {
			t.Fatalf("nbtToChunk: %v", err)
		}
		bes := loaded.StructureBlockEntities()
		if len(bes) != 3 {
			t.Fatalf("round-trip block entities = %d, want 3", len(bes))
		}
		if be, ok := loaded.StructureBlockEntityAt(36, 40, 52); !ok || be.SpawnerEntityID != "minecraft:breeze" {
			t.Errorf("round-trip trial_spawner = %+v, ok=%v", be, ok)
		}
		if be, ok := loaded.StructureBlockEntityAt(37, 40, 52); !ok || be.KeyItem != "minecraft:trial_key" || len(be.Items) == 0 {
			t.Errorf("round-trip vault = %+v, ok=%v", be, ok)
		}
	}
}

func TestStructureFeatureEdgeSweepAndLootSeedStability(t *testing.T) {
	// 1. BlackstoneReplaceProcessor state preservation
	{
		cobbleStairs, ok := nameToStateID("minecraft:cobblestone_stairs", map[string]string{
			"facing":      "east",
			"half":        "top",
			"shape":       "straight",
			"waterlogged": "false",
		})
		if !ok {
			t.Fatal("missing cobblestone_stairs state")
		}
		gotID, gotName := applyBlackstoneReplacement(cobbleStairs, "minecraft:cobblestone_stairs")
		if gotName != "minecraft:blackstone_stairs" {
			t.Fatalf("applyBlackstoneReplacement name = %q, want minecraft:blackstone_stairs", gotName)
		}
		wantStairs, ok := nameToStateID("minecraft:blackstone_stairs", map[string]string{
			"facing":      "east",
			"half":        "top",
			"shape":       "straight",
			"waterlogged": "false",
		})
		if !ok || gotID != wantStairs {
			t.Fatalf("applyBlackstoneReplacement stateID = %d, want %d", gotID, wantStairs)
		}
	}

	// 2. RottableBlocks filter in minecraft:block_rot
	{
		fs, err := worldgen.LoadFeatureSet()
		if err != nil {
			t.Fatalf("LoadFeatureSet: %v", err)
		}
		if !matchesRottableBlocks("minecraft:oxidized_copper", []string{"minecraft:oxidized_copper", "#minecraft:terracotta"}, fs) {
			t.Errorf("expected minecraft:oxidized_copper to match rottable_blocks")
		}
		if matchesRottableBlocks("minecraft:tuff_bricks", []string{"minecraft:oxidized_copper"}, fs) {
			t.Errorf("expected minecraft:tuff_bricks not to match rottable_blocks")
		}
	}

	// 3. Beached Shipwreck cross-chunk Y-height & LootTableSeed order invariance
	{
		const seed int64 = 77777
		buildAndPlace := func(reverse bool) ([]StructureBlockEntity, int) {
			start := BuildShipwreckStart(seed, 0, 0, true)
			start.Template = "shipwreck/with_mast"
			region := buildFlatTestDecorationRegion(t, 0, 0, 2, 64, false)
			// Add a slight slope across x so footprint columns matter.
			sandID, _ := nameToStateID("minecraft:sand", nil)
			for cx := int32(-1); cx <= 1; cx++ {
				for cz := int32(-1); cz <= 1; cz++ {
					ch := region.chunks[[2]int32{cx, cz}]
					for x := 0; x < 16; x++ {
						for z := 0; z < 16; z++ {
							wx := int(cx)*16 + x
							topY := 64 + (wx & 3)
							for y := 60; y <= topY; y++ {
								ch.SetBlock(x, y, z, sandID)
							}
						}
					}
				}
			}
			var coords [][2]int32
			for cx := int32(-2); cx <= 2; cx++ {
				for cz := int32(-2); cz <= 2; cz++ {
					coords = append(coords, [2]int32{cx, cz})
				}
			}
			if reverse {
				for i, j := 0, len(coords)-1; i < j; i, j = i+1, j-1 {
					coords[i], coords[j] = coords[j], coords[i]
				}
			}
			starts := []*ShipwreckStart{start}
			for _, c := range coords {
				PlaceShipwrecksForChunk(region, starts, "minecraft:shipwreck_beached", seed, c[0], c[1])
			}
			return collectRegionBlockEntities(region), start.Pos[1]
		}
		besFwd, yFwd := buildAndPlace(false)
		besRev, yRev := buildAndPlace(true)
		if yFwd != yRev {
			t.Fatalf("beached shipwreck Y depends on chunk order: forward=%d reverse=%d", yFwd, yRev)
		}
		if len(besFwd) == 0 || len(besFwd) != len(besRev) {
			t.Fatalf("beached shipwreck block entities count forward=%d reverse=%d", len(besFwd), len(besRev))
		}
		fwdSeeds := map[[3]int]int64{}
		for _, be := range besFwd {
			fwdSeeds[[3]int{be.X, be.Y, be.Z}] = be.LootTableSeed
		}
		for _, be := range besRev {
			pos := [3]int{be.X, be.Y, be.Z}
			if got, ok := fwdSeeds[pos]; !ok || got != be.LootTableSeed {
				t.Errorf("shipwreck chest at %v LootTableSeed mismatch: forward=%d reverse=%d", pos, got, be.LootTableSeed)
			}
		}

		// Also verify single-chunk region doesn't sink shipwreck to MinY (-64) when footprint extends into unloaded chunk.
		singleChunkStart := BuildShipwreckStart(seed, 0, 0, true)
		singleChunkStart.Template = "shipwreck/with_mast"
		singleRegion := buildFlatTestDecorationRegion(t, 0, 0, 0, 64, false)
		PlaceShipwrecksForChunk(singleRegion, []*ShipwreckStart{singleChunkStart}, "minecraft:shipwreck_beached", seed, 0, 0)
		if singleChunkStart.Pos[1] < 50 {
			t.Errorf("single-chunk beached shipwreck sank to Y=%d, want >= 50", singleChunkStart.Pos[1])
		}
	}

	// 4. Desert Pyramid & Jungle Temple cross-chunk height and LootTableSeed stability
	{
		const seed int64 = 424242
		runPyramid := func(reverse bool) ([]StructureBlockEntity, int) {
			start := BuildDesertPyramidStart(seed, 0, 0)
			region := buildFlatTestDecorationRegion(t, 0, 0, 2, 66, false)
			var coords [][2]int32
			for cx := int32(-2); cx <= 2; cx++ {
				for cz := int32(-2); cz <= 2; cz++ {
					coords = append(coords, [2]int32{cx, cz})
				}
			}
			if reverse {
				for i, j := 0, len(coords)-1; i < j; i, j = i+1, j-1 {
					coords[i], coords[j] = coords[j], coords[i]
				}
			}
			starts := []*DesertPyramidStart{start}
			for _, c := range coords {
				PlaceDesertPyramidsForChunk(region, starts, seed, c[0], c[1])
			}
			return collectRegionBlockEntities(region), start.Piece.box.minY
		}
		besFwd, yFwd := runPyramid(false)
		besRev, yRev := runPyramid(true)
		if yFwd != yRev || yFwd < 50 {
			t.Fatalf("desert pyramid minY forward=%d reverse=%d", yFwd, yRev)
		}
		fwdSeeds := map[[3]int]int64{}
		for _, be := range besFwd {
			fwdSeeds[[3]int{be.X, be.Y, be.Z}] = be.LootTableSeed
		}
		for _, be := range besRev {
			pos := [3]int{be.X, be.Y, be.Z}
			if got, ok := fwdSeeds[pos]; !ok || got != be.LootTableSeed {
				t.Errorf("desert pyramid BE at %v LootTableSeed forward=%d reverse=%d", pos, got, be.LootTableSeed)
			}
		}
	}

	// 5. Jigsaw Trail Ruins & Trial Chambers cross-chunk CappedProcessor and LootTableSeed order invariance
	{
		sets, err := worldgen.LoadStructureSets()
		if err != nil {
			t.Fatalf("LoadStructureSets: %v", err)
		}
		flatSurface := func(x, z int, oceanFloor bool) int { return 64 }
		def := sets.Structures["minecraft:trail_ruins"]
		start, err := BuildJigsawStart(nil, sets, def, 2024, 0, 0, flatSurface)
		if err != nil || start == nil {
			t.Fatalf("BuildJigsawStart trail_ruins: %v", err)
		}
		runTrailRuins := func(reverse bool) map[[3]int]StructureBlockEntity {
			region := buildFlatTestDecorationRegion(t, 0, 0, 2, 64, false)
			var coords [][2]int32
			for cx := int32(-2); cx <= 2; cx++ {
				for cz := int32(-2); cz <= 2; cz++ {
					coords = append(coords, [2]int32{cx, cz})
				}
			}
			if reverse {
				for i, j := 0, len(coords)-1; i < j; i, j = i+1, j-1 {
					coords[i], coords[j] = coords[j], coords[i]
				}
			}
			starts := []*JigsawStart{start}
			for _, c := range coords {
				PlaceJigsawStartsForChunk(region, starts, "minecraft:trail_ruins", 3, 2024, c[0], c[1])
			}
			out := map[[3]int]StructureBlockEntity{}
			for _, be := range collectRegionBlockEntities(region) {
				out[[3]int{be.X, be.Y, be.Z}] = be
			}
			return out
		}
		fwd := runTrailRuins(false)
		rev := runTrailRuins(true)
		if len(fwd) == 0 || len(fwd) != len(rev) {
			t.Fatalf("trail_ruins block entities count forward=%d reverse=%d", len(fwd), len(rev))
		}
		for pos, a := range fwd {
			b, ok := rev[pos]
			if !ok || a.LootTable != b.LootTable || a.LootTableSeed != b.LootTableSeed {
				t.Errorf("trail_ruins BE at %v mismatch: forward=%+v reverse=%+v", pos, a, b)
			}
		}
	}
}

func TestMonsterRoomSpawnerAndLootParity(t *testing.T) {
	mobs := MonsterRoomMobTypes()
	if mobs != [4]string{"minecraft:skeleton", "minecraft:zombie", "minecraft:zombie", "minecraft:spider"} {
		t.Fatalf("unexpected MonsterRoomMobTypes: %v", mobs)
	}

	region := buildFlatTestDecorationRegion(t, 0, 0, 1, 64, false)
	stoneID, _ := nameToStateID("minecraft:stone", nil)
	airID, _ := nameToStateID("minecraft:air", nil)
	for x := -16; x < 32; x++ {
		for z := -16; z < 32; z++ {
			for y := 8; y <= 36; y++ {
				region.setBlockGlobal(x, y, z, stoneID)
			}
		}
	}
	// Open a 1-block doorway so the dungeon placement check passes.
	region.setBlockGlobal(8-4, 20, 8, airID)
	region.setBlockGlobal(8-4, 21, 8, airID)
	rng := worldgen.NewLegacy(777)
	if !placeMonsterRoom(region, rng, 8, 20, 8) {
		t.Fatalf("expected first monster room to place at (8,20,8)")
	}
	bes := collectRegionBlockEntities(region)
	spawners := 0
	chests := 0
	for _, be := range bes {
		switch be.ID {
		case "minecraft:mob_spawner":
			spawners++
			if be.X != 8 || be.Y != 20 || be.Z != 8 {
				t.Errorf("spawner at (%d,%d,%d), want (8,20,8)", be.X, be.Y, be.Z)
			}
			if be.SpawnerEntityID != "minecraft:skeleton" && be.SpawnerEntityID != "minecraft:zombie" && be.SpawnerEntityID != "minecraft:spider" {
				t.Errorf("unexpected spawner entity %q", be.SpawnerEntityID)
			}
		case "minecraft:chest":
			chests++
			if be.LootTable != "minecraft:chests/simple_dungeon" || len(be.Items) == 0 {
				t.Errorf("chest BE missing simple_dungeon loot: %+v", be)
			}
		}
	}
	if spawners != 1 || chests == 0 {
		t.Fatalf("expected 1 spawner and >=1 chest, got spawners=%d chests=%d", spawners, chests)
	}

	// Verify that if an overlapping second monster room carves an unsupported wall cell to cave_air,
	// any pre-existing block entity in that wall cell is removed cleanly.
	probeRng := worldgen.NewLegacy(888)
	j2 := int(probeRng.NextIntN(2)) + 2
	wallX := 8 + j2 + 1
	for x := -16; x < 32; x++ {
		for z := -16; z < 32; z++ {
			for y := 8; y <= 36; y++ {
				region.setBlockGlobal(x, y, z, stoneID)
			}
		}
	}
	region.setBlockGlobal(wallX, 20, 8, airID)
	region.setBlockGlobal(wallX, 21, 8, airID)
	region.addStructureBlockEntity(StructureBlockEntity{
		ID:        "minecraft:chest",
		X:         wallX,
		Y:         21,
		Z:         8,
		LootTable: "minecraft:chests/simple_dungeon",
	})
	if !placeMonsterRoom(region, worldgen.NewLegacy(888), 8, 20, 8) {
		t.Fatalf("expected second monster room to place at (8,20,8)")
	}
	if ch := region.chunks[[2]int32{0, 0}]; ch != nil {
		if _, exists := ch.StructureBlockEntityAt(wallX, 21, 8); exists {
			t.Errorf("expected carved unsupported wall cell (%d,21,8) to remove stale block entity", wallX)
		}
	}
}

func TestMineshaftResidualSpawnerAndMinecartRepeatability(t *testing.T) {
	const seed int64 = 12345
	start := BuildMineshaftStart(seed, 0, 0, false)
	if start == nil {
		t.Fatalf("expected non-nil MineshaftStart")
	}
	for i := range start.Pieces {
		if start.Pieces[i].kind == msKindCorridor {
			start.Pieces[i].spider = true
			start.Pieces[i].hasRails = true
		}
	}
	starts := []*MineshaftStart{start}

	// First run in a throwaway region (simulating neighbor 3x3 pre-pass).
	preRegion := buildFlatTestDecorationRegion(t, 0, 0, 3, 64, false)
	preRegion.bindSeed(seed)
	for cx := int32(-3); cx <= 3; cx++ {
		for cz := int32(-3); cz <= 3; cz++ {
			PlaceMineshaftsForChunk(preRegion, starts, seed, cx, cz)
		}
	}

	// Second run on a fresh region using the SAME *MineshaftStart pointer must still
	// place identical cave_spider spawners and chest_minecarts.
	region2 := buildFlatTestDecorationRegion(t, 0, 0, 3, 64, false)
	region2.bindSeed(seed)
	for cx := int32(-3); cx <= 3; cx++ {
		for cz := int32(-3); cz <= 3; cz++ {
			PlaceMineshaftsForChunk(region2, starts, seed, cx, cz)
		}
	}

	bes1 := collectRegionBlockEntities(preRegion)
	bes2 := collectRegionBlockEntities(region2)
	if len(bes1) == 0 || len(bes1) != len(bes2) {
		t.Fatalf("mineshaft cave_spider spawner count mismatch across repeated runs: %d vs %d", len(bes1), len(bes2))
	}
	ents1 := collectRegionEntities(preRegion)
	ents2 := collectRegionEntities(region2)
	if len(ents1) == 0 || len(ents1) != len(ents2) {
		t.Fatalf("mineshaft chest_minecart count mismatch across repeated runs: %d vs %d", len(ents1), len(ents2))
	}
}

func TestStrongholdPortalRoomFrameEdgesAndCachedSpawner(t *testing.T) {
	const seed int64 = 12345
	start := BuildStrongholdStart(seed, 0, 0)
	if start == nil || start.PortalRoom == nil {
		t.Fatalf("expected StrongholdStart with PortalRoom")
	}
	frames, interior, spawner, ok := StrongholdPortalRoomFrames(start)
	if !ok {
		t.Fatalf("StrongholdPortalRoomFrames returned ok=false")
	}

	starts := []*StrongholdStart{start}
	// Run once on a throwaway region to simulate neighbor 3x3 pre-pass on cached StrongholdStart.
	preRegion := buildFlatTestDecorationRegion(t, 0, 0, 6, 64, false)
	for cx := int32(-6); cx <= 6; cx++ {
		for cz := int32(-6); cz <= 6; cz++ {
			PlaceStrongholdsForChunk(preRegion, starts, seed, cx, cz)
		}
	}

	// Run a second time on a fresh region with the same cached StrongholdStart.
	region := buildFlatTestDecorationRegion(t, 0, 0, 6, 64, false)
	for cx := int32(-6); cx <= 6; cx++ {
		for cz := int32(-6); cz <= 6; cz++ {
			PlaceStrongholdsForChunk(region, starts, seed, cx, cz)
		}
	}

	for i, fp := range frames {
		if BlockName(region.getBlock(fp[0], fp[1], fp[2])) != "minecraft:end_portal_frame" {
			t.Errorf("frame[%d] at %v = %q, want minecraft:end_portal_frame", i, fp, BlockName(region.getBlock(fp[0], fp[1], fp[2])))
		}
	}
	if len(interior) != 9 {
		t.Errorf("interior len = %d, want 9", len(interior))
	}
	if BlockName(region.getBlock(spawner[0], spawner[1], spawner[2])) != "minecraft:spawner" {
		t.Errorf("spawner block at %v = %q, want minecraft:spawner", spawner, BlockName(region.getBlock(spawner[0], spawner[1], spawner[2])))
	}
	foundSpawnerBE := false
	chests := 0
	for _, be := range collectRegionBlockEntities(region) {
		if be.ID == "minecraft:mob_spawner" && be.X == spawner[0] && be.Y == spawner[1] && be.Z == spawner[2] && be.SpawnerEntityID == "minecraft:silverfish" {
			foundSpawnerBE = true
		}
		if be.ID == "minecraft:chest" && len(be.Items) > 0 {
			chests++
		}
	}
	if !foundSpawnerBE || chests == 0 {
		t.Fatalf("after cached StrongholdStart re-run: foundSpawnerBE=%v chests=%d", foundSpawnerBE, chests)
	}
}

func TestJigsawFinalStateSNBTAndLecternBook(t *testing.T) {
	stateByIDOnce.Do(buildStateTable)

	// 1. Trial Spawner final_state with SNBT
	id, name, snbt, ok := parseFinalStateWithSNBT(`minecraft:trial_spawner[ominous=false,trial_spawner_state=inactive]{normal_config:"minecraft:trial_chamber/breeze/normal",ominous_config:"minecraft:trial_chamber/breeze/ominous"}`)
	if !ok || id == 0 || name != "minecraft:trial_spawner" || snbt == nil {
		t.Fatalf("parseFinalStateWithSNBT trial_spawner failed: id=%d name=%q snbt=%v ok=%v", id, name, snbt, ok)
	}
	be, valid := extractTemplateBlockEntity(10, 20, 30, name, snbt, 0)
	if !valid {
		t.Fatalf("extractTemplateBlockEntity trial_spawner valid=false")
	}
	populateBlockEntityLoot(&be)
	if be.NormalConfig != "minecraft:trial_chamber/breeze/normal" || be.SpawnerEntityID != "minecraft:breeze" {
		t.Errorf("trial_spawner BE = %+v", be)
	}

	// 2. Vault final_state with nested SNBT
	_, vName, vSNBT, vOK := parseFinalStateWithSNBT(`minecraft:vault[facing=south,ominous=true,vault_state=inactive]{config:{key_item:{count:1,id:"minecraft:ominous_trial_key"},loot_table:"minecraft:chests/trial_chambers/reward_ominous"}}`)
	if !vOK || vName != "minecraft:vault" || vSNBT == nil {
		t.Fatalf("parseFinalStateWithSNBT vault failed")
	}
	vBE, vValid := extractTemplateBlockEntity(12, 20, 30, vName, vSNBT, 0)
	if !vValid {
		t.Fatalf("extractTemplateBlockEntity vault valid=false")
	}
	populateBlockEntityLoot(&vBE)
	if vBE.KeyItem != "minecraft:ominous_trial_key" || vBE.LootTable != "minecraft:chests/trial_chambers/reward_ominous" || len(vBE.Items) == 0 {
		t.Errorf("vault BE = %+v", vBE)
	}

	// 3. Lectern with Book (Ancient City / Village library)
	_, lName, lSNBT, lOK := parseFinalStateWithSNBT(`minecraft:lectern[facing=north,has_book=true,powered=false]{Book:{Count:1b,id:"minecraft:written_book"}}`)
	if !lOK || lName != "minecraft:lectern" || lSNBT == nil {
		t.Fatalf("parseFinalStateWithSNBT lectern failed")
	}
	lBE, lValid := extractTemplateBlockEntity(14, 20, 30, lName, lSNBT, 0)
	if !lValid || len(lBE.Items) != 1 || lBE.Items[0].ItemID != "minecraft:written_book" {
		t.Errorf("lectern BE = %+v", lBE)
	}
}
