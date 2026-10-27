package server

import (
	"strings"
	"testing"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestAdvancementRegistryStructureAndIcons(t *testing.T) {
	reg := DefaultAdvancementRegistry()
	all := reg.All()
	if len(all) < 20 {
		t.Fatalf("DefaultAdvancementRegistry count = %d, want >= 20", len(all))
	}

	seen := make(map[string]bool, len(all))
	for _, def := range all {
		if def.ID == "" {
			t.Fatal("empty advancement ID")
		}
		if seen[def.ID] {
			t.Fatalf("duplicate advancement ID %q", def.ID)
		}
		seen[def.ID] = true

		if def.ParentID != "" && !seen[def.ParentID] {
			t.Fatalf("advancement %q references parent %q before parent appears in topological order", def.ID, def.ParentID)
		}
		if len(def.Criteria) == 0 || len(def.Requirements) == 0 {
			t.Fatalf("advancement %q has empty criteria or requirements", def.ID)
		}
		if def.Display == nil {
			t.Fatalf("advancement %q has nil Display", def.ID)
		}
		itemID, ok := world.ItemNameToID(def.Display.IconItem)
		if !ok || itemID <= 0 {
			t.Fatalf("advancement %q icon %q did not resolve to a non-air item ID (got %d, %v)",
				def.ID, def.Display.IconItem, itemID, ok)
		}
	}
}

func TestUpdateAdvancementsPacketWireFormatAndSeenTab(t *testing.T) {
	srv, _ := newTestEnchantServer(t)

	var sentIDs []int32
	var lastUpdateBody []byte
	var lastTabBody []byte
	sess, err := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("AdvWire"),
		Name: "AdvWire",
	}, func(id int32, body []byte) error {
		sentIDs = append(sentIDs, id)
		cp := append([]byte(nil), body...)
		switch id {
		case PlayUpdateAdvancements:
			lastUpdateBody = cp
		case PlaySelectAdvancementsTab:
			lastTabBody = cp
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}

	if err := srv.SendInitialAdvancements(sess); err != nil {
		t.Fatalf("SendInitialAdvancements: %v", err)
	}
	if len(lastUpdateBody) == 0 {
		t.Fatal("expected initial PlayUpdateAdvancements packet")
	}

	// Parse header of initial reset packet.
	r := protocol.NewReader(lastUpdateBody)
	reset, err := r.Bool()
	if err != nil || !reset {
		t.Fatalf("initial packet reset = %v (err=%v), want true", reset, err)
	}
	addedCount, err := r.VarInt()
	if err != nil || int(addedCount) != len(DefaultAdvancementRegistry().All()) {
		t.Fatalf("initial packet addedCount = %d (err=%v), want %d", addedCount, err, len(DefaultAdvancementRegistry().All()))
	}
	firstID, err := r.String()
	if err != nil || firstID != "minecraft:story/root" {
		t.Fatalf("first advancement ID = %q (err=%v), want minecraft:story/root", firstID, err)
	}

	// Test ServerboundSeenAdvancementsPacket decode + HandleSeenAdvancements.
	w := protocol.NewWriter(32)
	w.VarInt(SeenAdvancementsOpenedTab)
	w.String("minecraft:story/root")
	action, tab, err := DecodeSeenAdvancementsPacket(protocol.NewReader(w.Bytes()))
	if err != nil || action != SeenAdvancementsOpenedTab || tab != "minecraft:story/root" {
		t.Fatalf("DecodeSeenAdvancementsPacket = (%d, %q, %v)", action, tab, err)
	}
	if err := srv.HandleSeenAdvancements(sess, action, tab); err != nil {
		t.Fatalf("HandleSeenAdvancements: %v", err)
	}
	if sess.SelectedAdvancementTab() != "minecraft:story/root" {
		t.Fatalf("SelectedAdvancementTab = %q, want minecraft:story/root", sess.SelectedAdvancementTab())
	}
	if len(lastTabBody) == 0 {
		t.Fatal("expected PlaySelectAdvancementsTab packet")
	}
	tabReader := protocol.NewReader(lastTabBody)
	hasTab, _ := tabReader.Bool()
	gotTab, _ := tabReader.String()
	if !hasTab || gotTab != "minecraft:story/root" {
		t.Fatalf("decoded select_advancements_tab = (%v, %q), want (true, minecraft:story/root)", hasTab, gotTab)
	}
}

func TestAdvancementGameplayTriggersRewardsAndChat(t *testing.T) {
	srv, _ := newTestEnchantServer(t)

	var chatMessages []string
	sess, err := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("Hero"),
		Name: "Hero",
	}, func(id int32, body []byte) error {
		if id == protocol.PlaySystemChat {
			tag, err := nbt.Unmarshal(body[:len(body)-1])
			if err == nil {
				if s, ok := tag.(nbt.String); ok {
					chatMessages = append(chatMessages, string(s))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(sess, GameModeSurvival)

	// 1. Story progression via inventory & crafting/smelting triggers.
	sess.SetInventorySlot(36, world.NewNamedItemStack("minecraft:crafting_table", 1))
	sess.SetInventorySlot(37, world.NewNamedItemStack("minecraft:cobblestone", 4))
	sess.SetInventorySlot(38, world.NewNamedItemStack("minecraft:stone_pickaxe", 1))
	srv.TriggerInventoryChanged(sess)

	for _, id := range []string{
		"minecraft:story/root",
		"minecraft:story/mine_stone",
		"minecraft:story/upgrade_tools",
	} {
		if !sess.HasCompletedAdvancement(id) {
			t.Fatalf("expected %q to be completed after TriggerInventoryChanged", id)
		}
	}

	// Smelting iron & obtaining iron armor (OR-requirement: any 1 piece suffices).
	srv.TriggerItemSmelted(sess, world.NewNamedItemStack("minecraft:iron_ingot", 1))
	srv.TriggerRecipeCrafted(sess, world.NewNamedItemStack("minecraft:iron_boots", 1))
	if !sess.HasCompletedAdvancement("minecraft:story/smelt_iron") {
		t.Fatal("expected story/smelt_iron to be completed")
	}
	if !sess.HasCompletedAdvancement("minecraft:story/obtain_armor") {
		t.Fatal("expected story/obtain_armor to be completed from iron_boots alone")
	}

	// 2. Challenge advancement with XP reward: Serious Dedication (+100 XP).
	beforeXP := sess.TotalExperience()
	srv.TriggerItemAcquired(sess, world.NewNamedItemStack("minecraft:netherite_hoe", 1))
	if !sess.HasCompletedAdvancement("minecraft:husbandry/obtain_netherite_hoe") {
		t.Fatal("expected husbandry/obtain_netherite_hoe to be completed")
	}
	if sess.TotalExperience() != beforeXP+100 {
		t.Fatalf("TotalExperience after Serious Dedication = %d, want %d", sess.TotalExperience(), beforeXP+100)
	}

	// 3. Combat, sleeping, enchanting, trading, food, and Nether portal travel.
	srv.TriggerEntityKilled(sess, "minecraft:zombie")
	if !sess.HasCompletedAdvancement("minecraft:adventure/root") ||
		!sess.HasCompletedAdvancement("minecraft:adventure/kill_a_mob") {
		t.Fatal("expected adventure/root and adventure/kill_a_mob to complete on killing zombie")
	}

	srv.TriggerSleptInBed(sess)
	srv.TriggerEnchantedItem(sess)
	srv.TriggerVillagerTrade(sess)
	srv.TriggerConsumeItem(sess, world.NewNamedItemStack("minecraft:bread", 1))
	srv.TriggerChangeDimension(sess, world.DimensionNether)

	for _, id := range []string{
		"minecraft:adventure/sleep_in_bed",
		"minecraft:story/enchant_item",
		"minecraft:adventure/trade",
		"minecraft:husbandry/root",
		"minecraft:story/enter_the_nether",
		"minecraft:nether/root",
	} {
		if !sess.HasCompletedAdvancement(id) {
			t.Fatalf("expected %q to be completed", id)
		}
	}

	// Verify chat announcements were broadcast for completed advancements.
	foundChallengeChat := false
	for _, msg := range chatMessages {
		if strings.Contains(msg, "Hero has completed the challenge [Serious Dedication]") {
			foundChallengeChat = true
		}
	}
	if !foundChallengeChat {
		t.Fatalf("expected challenge chat broadcast in %v", chatMessages)
	}
}

func TestAdvancementNBTRoundTripAndJSONPersistence(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = t.TempDir()
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}

	uuid := OfflineUUID("PersistPlayer")
	sess1, err := srv.RegisterPlayer(Profile{UUID: uuid, Name: "PersistPlayer"}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer sess1: %v", err)
	}

	srv.GrantAdvancement(sess1, "minecraft:story/mine_diamond")
	srv.GrantCriterion(sess1, "minecraft:nether/netherite_armor", "netherite_helmet")
	_ = srv.HandleSeenAdvancements(sess1, SeenAdvancementsOpenedTab, "minecraft:nether/root")

	// 1. Test PlayerData NBT compound round-trip.
	playerRoot := nbt.NewCompound()
	SaveAdvancementsToPlayerCompound(sess1, playerRoot)
	nbtBytes := nbt.MarshalNamed("", playerRoot)
	_, decodedTag, err := nbt.UnmarshalNamed(nbtBytes)
	if err != nil {
		t.Fatalf("UnmarshalNamed: %v", err)
	}

	sess2 := &PlayerSession{EntityID: 2, Profile: Profile{UUID: uuid, Name: "PersistPlayer"}}
	LoadAdvancementsFromPlayerCompound(sess2, decodedTag.(*nbt.Compound))
	if !sess2.HasCompletedAdvancement("minecraft:story/mine_diamond") {
		t.Fatal("NBT round-trip lost completed story/mine_diamond")
	}
	prog2, ok := sess2.AdvancementProgressFor("minecraft:nether/netherite_armor")
	if !ok || !prog2.Criteria["netherite_helmet"].IsDone() || prog2.IsDone() {
		t.Fatalf("NBT round-trip partial progress = %+v, want netherite_helmet done and overall not done", prog2)
	}
	if sess2.SelectedAdvancementTab() != "minecraft:nether/root" {
		t.Fatalf("NBT round-trip SelectedAdvancementTab = %q, want minecraft:nether/root", sess2.SelectedAdvancementTab())
	}

	// 2. Test <WorldDir>/advancements/<uuid>.json file persistence.
	if err := srv.SavePlayerAdvancementsFile(sess1); err != nil {
		t.Fatalf("SavePlayerAdvancementsFile: %v", err)
	}
	sess3 := &PlayerSession{EntityID: 3, Profile: Profile{UUID: uuid, Name: "PersistPlayer"}}
	if err := srv.LoadPlayerAdvancementsFile(sess3); err != nil {
		t.Fatalf("LoadPlayerAdvancementsFile: %v", err)
	}
	if !sess3.HasCompletedAdvancement("minecraft:story/mine_diamond") {
		t.Fatal("JSON file round-trip lost completed story/mine_diamond")
	}
	prog3, _ := sess3.AdvancementProgressFor("minecraft:nether/netherite_armor")
	if !prog3.Criteria["netherite_helmet"].IsDone() || prog3.IsDone() {
		t.Fatalf("JSON file round-trip partial progress = %+v", prog3)
	}
}

func TestAdvancementsRecipesStatisticsAndScoreboardRemainder26_1_2(t *testing.T) {
	srv, _ := newTestEnchantServer(t)

	var chatMessages []string
	alice, err := srv.RegisterPlayer(Profile{UUID: OfflineUUID("AliceRem"), Name: "AliceRem"}, func(id int32, body []byte) error {
		if id == protocol.PlaySystemChat && len(body) > 1 {
			if tag, err := nbt.Unmarshal(body[:len(body)-1]); err == nil {
				if s, ok := tag.(nbt.String); ok {
					chatMessages = append(chatMessages, string(s))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer(AliceRem): %v", err)
	}
	bob, _ := srv.RegisterPlayer(Profile{UUID: OfflineUUID("BobRem"), Name: "BobRem"}, nil)
	_ = srv.GrantOp(alice.Profile)
	_ = srv.SetPlayerGameMode(alice, GameModeSurvival)

	// 1. Advancement triggers across Story / Adventure / Husbandry / Nether / End.
	srv.TriggerUsedTotem(alice)
	srv.TriggerShotArrow(alice, "minecraft:skeleton", true, 55.0)
	srv.TriggerShotCrossbow(alice, []string{"minecraft:pillager", "minecraft:zombie", "minecraft:skeleton", "minecraft:spider", "minecraft:creeper"})
	srv.TriggerTargetHit(alice, 15, 35.0)
	srv.TriggerSlideDownBlock(alice, "minecraft:honey_block")
	srv.TriggerWalkOnPowderSnowWithLeatherBoots(alice)
	srv.TriggerFallFromHeight(alice, 320.0, -62.0)
	srv.TriggerLightningStrike(alice, true, true, false)
	srv.TriggerThrownTridentHit(alice, true)
	srv.TriggerSummonedEntity(alice, "minecraft:iron_golem")
	srv.TriggerSummonedEntity(alice, "minecraft:wither")
	srv.TriggerSummonedEntity(alice, "minecraft:ender_dragon")
	srv.TriggerCrafterRecipeCrafted(alice, "minecraft:crafter")
	srv.TriggerBredAnimals(alice, "minecraft:cow")
	srv.TriggerTamedAnimal(alice, "minecraft:wolf")
	srv.TriggerSafelyHarvestHoney(alice)
	srv.TriggerSilkTouchBeeNest(alice, 3)
	srv.TriggerWaxOn(alice)
	srv.TriggerWaxOff(alice)
	srv.TriggerMakeSignGlow(alice)
	srv.TriggerRideBoatWithGoat(alice)
	srv.TriggerNetherTravel(alice, 0, 0, 7200, 0)
	srv.TriggerChargeRespawnAnchor(alice, 4)
	srv.TriggerUseLodestone(alice)
	srv.TriggerRideStrider(alice)
	srv.TriggerRideStriderInLava(alice, world.DimensionOverworld, 5200)
	srv.TriggerChangeDimension(alice, world.DimensionEnd)
	srv.TriggerEntityKilled(alice, "minecraft:ender_dragon")
	srv.TriggerEnterEndGateway(alice)
	srv.TriggerLevitationDistance(alice, 55.0)
	srv.TriggerBrushArmadillo(alice)
	srv.TriggerLightenUpCopperBulb(alice)
	srv.TriggerWhoNeedsRockets(alice, 9.0)
	srv.TriggerMaceOverOverkill(alice, 110.0)
	srv.TriggerItemAcquired(alice, world.NewNamedItemStack("minecraft:obsidian", 1))
	srv.TriggerItemAcquired(alice, world.NewNamedItemStack("minecraft:elytra", 1))
	srv.TriggerItemAcquired(alice, world.NewNamedItemStack("minecraft:dragon_egg", 1))

	for _, id := range []string{
		"minecraft:story/form_obsidian",
		"minecraft:adventure/totem_of_undying",
		"minecraft:adventure/shoot_arrow",
		"minecraft:adventure/sniper_duel",
		"minecraft:adventure/ol_betsy",
		"minecraft:adventure/whos_the_pillager_now",
		"minecraft:adventure/arbalistic",
		"minecraft:adventure/bullseye",
		"minecraft:adventure/honey_block_slide",
		"minecraft:adventure/walk_on_powder_snow_with_leather_boots",
		"minecraft:adventure/fall_from_world_height",
		"minecraft:adventure/lightning_rod_with_villager_no_fire",
		"minecraft:adventure/throw_trident",
		"minecraft:adventure/very_very_frightening",
		"minecraft:adventure/summon_iron_golem",
		"minecraft:adventure/crafters_crafting_crafters",
		"minecraft:adventure/brush_armadillo",
		"minecraft:adventure/lighten_up",
		"minecraft:adventure/who_needs_rockets",
		"minecraft:adventure/overoverkill",
		"minecraft:husbandry/breed_an_animal",
		"minecraft:husbandry/tame_an_animal",
		"minecraft:husbandry/safely_harvest_honey",
		"minecraft:husbandry/silk_touch_nest",
		"minecraft:husbandry/wax_on",
		"minecraft:husbandry/wax_off",
		"minecraft:husbandry/make_a_sign_glow",
		"minecraft:husbandry/ride_a_boat_with_a_goat",
		"minecraft:nether/fast_travel",
		"minecraft:nether/summon_wither",
		"minecraft:nether/charge_respawn_anchor",
		"minecraft:nether/use_lodestone",
		"minecraft:nether/ride_strider",
		"minecraft:nether/ride_strider_in_overworld_lava",
		"minecraft:end/root",
		"minecraft:end/kill_dragon",
		"minecraft:end/dragon_egg",
		"minecraft:end/enter_end_gateway",
		"minecraft:end/respawn_dragon",
		"minecraft:end/elytra",
		"minecraft:end/levitate",
	} {
		if !alice.HasCompletedAdvancement(id) {
			t.Fatalf("expected advancement %q to be completed", id)
		}
	}

	// 2. /advancement (until|from|through) tree traversal and announceAdvancements=false.
	srv.SetAnnounceAdvancements(false)
	chatMessages = nil
	_ = srv.HandleAdvancementCommand(alice, []string{"grant", "BobRem", "until", "minecraft:story/iron_tools"})
	for _, id := range []string{"minecraft:story/root", "minecraft:story/mine_stone", "minecraft:story/upgrade_tools", "minecraft:story/smelt_iron", "minecraft:story/iron_tools"} {
		if !bob.HasCompletedAdvancement(id) {
			t.Fatalf("expected BobRem to have ancestor %q from 'until'", id)
		}
	}
	if len(chatMessages) != 0 {
		t.Fatalf("expected no chat messages when announceAdvancements=false, got %v", chatMessages)
	}
	_ = srv.HandleAdvancementCommand(alice, []string{"revoke", "BobRem", "from", "minecraft:story/upgrade_tools"})
	if !bob.HasCompletedAdvancement("minecraft:story/mine_stone") || bob.HasCompletedAdvancement("minecraft:story/upgrade_tools") || bob.HasCompletedAdvancement("minecraft:story/iron_tools") {
		t.Fatalf("unexpected state after revoke 'from' upgrade_tools")
	}

	// 3. Recipe book: entering water unlocks boats, doLimitedCrafting check.
	srv.TriggerEnterBlock(bob, "minecraft:water")
	if !bob.HasUnlockedRecipe("minecraft:oak_boat") {
		t.Fatal("expected entering water to unlock minecraft:oak_boat")
	}
	srv.SetDoLimitedCrafting(true)
	if !srv.CanPlayerCraftRecipe(bob, "minecraft:oak_boat") || srv.CanPlayerCraftRecipe(bob, "minecraft:beacon") {
		t.Fatal("expected CanPlayerCraftRecipe to enforce doLimitedCrafting")
	}
	srv.SetDoLimitedCrafting(false)

	// 4. Scoreboard compound stat criteria, state sync, and team sidebar resolution.
	_, _ = srv.CreateObjective("jumps", "minecraft.custom:minecraft.jump", TextComponent("Jumps"))
	_, _ = srv.CreateObjective("mined_stone", "minecraft.mined:minecraft.stone", TextComponent("Mined Stone"))
	_, _ = srv.CreateObjective("deaths_obj", "deathCount", TextComponent("Deaths"))
	_, _ = srv.CreateObjective("kills_obj", "totalKillCount", TextComponent("Kills"))
	_, _ = srv.CreateObjective("food_obj", "food", TextComponent("Food"))

	srv.RecordPlayerMovementStats(alice, 0, 64, 0, 1.0, 64.5, 0, true, false)
	srv.RecordBlockMined(alice, "minecraft:stone")
	srv.RecordPlayerDeath(alice)
	srv.RecordEntityKilled(alice, "minecraft:zombie")
	srv.SyncPlayerStateObjectives(alice)

	if sc, _ := srv.GetScore("AliceRem", "jumps"); sc != 1 {
		t.Fatalf("expected jumps score 1, got %d", sc)
	}
	if sc, _ := srv.GetScore("AliceRem", "mined_stone"); sc != 1 {
		t.Fatalf("expected mined_stone score 1, got %d", sc)
	}
	if sc, _ := srv.GetScore("AliceRem", "deaths_obj"); sc != 1 {
		t.Fatalf("expected deaths_obj score 1, got %d", sc)
	}
	if sc, _ := srv.GetScore("AliceRem", "kills_obj"); sc != 1 {
		t.Fatalf("expected kills_obj score 1, got %d", sc)
	}
	if sc, _ := srv.GetScore("AliceRem", "food_obj"); sc != 20 {
		t.Fatalf("expected food_obj score 20, got %d", sc)
	}

	// Team-colored sidebar slot resolution.
	_, _ = srv.CreateTeam("red_team", TextComponent("Red Team"))
	_, _ = srv.UpdateTeam("red_team", func(t *ScoreboardTeam) error {
		t.Color = ChatColorRed
		return nil
	})
	_, _ = srv.JoinTeam("red_team", "AliceRem")
	_ = srv.SetDisplayObjective(DisplaySlotSidebar, "jumps")
	_ = srv.SetDisplayObjective(DisplaySlotSidebarTeamRed, "kills_obj")
	if slot, obj := srv.ResolveSidebarObjectiveForPlayer(alice); slot != DisplaySlotSidebarTeamRed || obj != "kills_obj" {
		t.Fatalf("ResolveSidebarObjectiveForPlayer(alice) = (%d, %q), want (%d, 'kills_obj')", slot, obj, DisplaySlotSidebarTeamRed)
	}
	if slot, obj := srv.ResolveSidebarObjectiveForPlayer(bob); slot != DisplaySlotSidebar || obj != "jumps" {
		t.Fatalf("ResolveSidebarObjectiveForPlayer(bob) = (%d, %q), want (%d, 'jumps')", slot, obj, DisplaySlotSidebar)
	}
}
