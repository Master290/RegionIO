package world

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"testing"

	"regionio/internal/nbt"
)

func TestPlayerDataNBTRoundTrip(t *testing.T) {
	var stoneItemID int32 = -1
	for id, state := range itemToBlock {
		if st, ok := stateByID(state); ok && st.Name == "minecraft:stone" {
			stoneItemID = id
			break
		}
	}
	if stoneItemID <= 0 {
		t.Fatal("failed to find network item ID for minecraft:stone")
	}

	orig := PlayerData{
		Pos:            [3]float64{142.75, 68.5, -310.25},
		Yaw:            -135.5,
		Pitch:          22.25,
		OnGround:       false,
		GameMode:       0, // Survival
		Health:         15.5,
		FoodLevel:      17,
		FoodSaturation: 3.75,
		SelectedSlot:   4,
		SpawnX:         120,
		SpawnY:         70,
		SpawnZ:         -300,
		SpawnYaw:       90.0,
		SpawnPitch:     0.0,
		SpawnForced:    true,
		SpawnDimension: "minecraft:overworld",
		HasSpawn:       true,
	}
	orig.Inventory[HotbarContainerOffset+0] = NewItemStack(stoneItemID, 64)
	orig.Inventory[HotbarContainerOffset+4] = NewNamedItemStack("minecraft:dirt", 32)
	orig.Inventory[9] = NewNamedItemStack("minecraft:oak_planks", 16)
	orig.Inventory[45] = NewItemStack(stoneItemID, 8)

	encoded, err := MarshalPlayerDataNBT(orig)
	if err != nil {
		t.Fatalf("MarshalPlayerDataNBT: %v", err)
	}
	if len(encoded) < 2 || encoded[0] != 0x1f || encoded[1] != 0x8b {
		t.Fatalf("expected gzip header (1f 8b), got %x", encoded[:2])
	}

	// Verify vanilla NBT tag structure inside the gzipped payload.
	gr, err := gzip.NewReader(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	rawNBT, err := io.ReadAll(gr)
	_ = gr.Close()
	if err != nil {
		t.Fatalf("read gunzipped NBT: %v", err)
	}
	_, rootTag, err := nbt.UnmarshalNamed(rawNBT)
	if err != nil {
		t.Fatalf("UnmarshalNamed: %v", err)
	}
	root, ok := rootTag.(*nbt.Compound)
	if !ok {
		t.Fatalf("root tag type = %T, want *nbt.Compound", rootTag)
	}
	for _, key := range []string{
		"Pos", "Rotation", "OnGround", "playerGameType",
		"Health", "foodLevel", "foodSaturationLevel",
		"SelectedItemSlot", "Inventory",
		"SpawnX", "SpawnY", "SpawnZ", "SpawnDimension", "respawn",
	} {
		if _, ok := root.Get(key); !ok {
			t.Errorf("missing expected vanilla NBT tag %q", key)
		}
	}

	decoded, err := UnmarshalPlayerDataNBT(encoded)
	if err != nil {
		t.Fatalf("UnmarshalPlayerDataNBT: %v", err)
	}

	if decoded.Pos != orig.Pos {
		t.Fatalf("Pos = %v, want %v", decoded.Pos, orig.Pos)
	}
	if decoded.Yaw != orig.Yaw || decoded.Pitch != orig.Pitch {
		t.Fatalf("Rotation = (%v, %v), want (%v, %v)", decoded.Yaw, decoded.Pitch, orig.Yaw, orig.Pitch)
	}
	if decoded.OnGround != orig.OnGround {
		t.Fatalf("OnGround = %v, want %v", decoded.OnGround, orig.OnGround)
	}
	if decoded.GameMode != orig.GameMode {
		t.Fatalf("GameMode = %d, want %d", decoded.GameMode, orig.GameMode)
	}
	if decoded.Health != orig.Health {
		t.Fatalf("Health = %v, want %v", decoded.Health, orig.Health)
	}
	if decoded.FoodLevel != orig.FoodLevel {
		t.Fatalf("FoodLevel = %d, want %d", decoded.FoodLevel, orig.FoodLevel)
	}
	if decoded.FoodSaturation != orig.FoodSaturation {
		t.Fatalf("FoodSaturation = %v, want %v", decoded.FoodSaturation, orig.FoodSaturation)
	}
	if decoded.SelectedSlot != orig.SelectedSlot {
		t.Fatalf("SelectedSlot = %d, want %d", decoded.SelectedSlot, orig.SelectedSlot)
	}
	if !decoded.HasSpawn || decoded.SpawnX != orig.SpawnX || decoded.SpawnY != orig.SpawnY || decoded.SpawnZ != orig.SpawnZ {
		t.Fatalf("Spawn = (%v, %d, %d, %d), want (%v, %d, %d, %d)",
			decoded.HasSpawn, decoded.SpawnX, decoded.SpawnY, decoded.SpawnZ,
			orig.HasSpawn, orig.SpawnX, orig.SpawnY, orig.SpawnZ)
	}
	if decoded.SpawnForced != orig.SpawnForced || decoded.SpawnDimension != orig.SpawnDimension {
		t.Fatalf("SpawnForced=%v SpawnDimension=%q, want %v %q",
			decoded.SpawnForced, decoded.SpawnDimension, orig.SpawnForced, orig.SpawnDimension)
	}
	for slot := 0; slot < PlayerInventorySlots; slot++ {
		got := decoded.Inventory[slot]
		want := orig.Inventory[slot]
		if got.IsEmpty() != want.IsEmpty() {
			t.Fatalf("slot %d empty = %v, want %v", slot, got.IsEmpty(), want.IsEmpty())
		}
		if !want.IsEmpty() {
			if got.Count != want.Count || got.ResolvedID() != want.ResolvedID() || got.ResolvedItemID() != want.ResolvedItemID() {
				t.Fatalf("slot %d = %+v, want %+v", slot, got, want)
			}
		}
	}
}

func TestStoreSaveAndLoadPlayerData(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	uuid := [16]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}

	if _, ok, err := store.LoadPlayerData(uuid); err != nil || ok {
		t.Fatalf("LoadPlayerData before save = ok:%v err:%v, want false, nil", ok, err)
	}

	want := DefaultPlayerData(32.5, 71.0, -18.5)
	want.Yaw = 45.0
	want.Pitch = -10.0
	want.GameMode = 2
	want.Health = 18.0
	want.FoodLevel = 14
	want.SelectedSlot = 2
	want.Inventory[HotbarContainerOffset+2] = NewNamedItemStack("minecraft:cobblestone", 48)

	if err := store.SavePlayerData(uuid, want); err != nil {
		t.Fatalf("SavePlayerData: %v", err)
	}

	path := store.PlayerDataPath(uuid)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected playerdata file at %s: %v", path, err)
	}

	got, ok, err := store.LoadPlayerData(uuid)
	if err != nil || !ok {
		t.Fatalf("LoadPlayerData after save = ok:%v err:%v, want true, nil", ok, err)
	}
	if got.Pos != want.Pos || got.Yaw != want.Yaw || got.Pitch != want.Pitch || got.GameMode != want.GameMode {
		t.Fatalf("loaded transform/gamemode = %+v, want %+v", got, want)
	}
	if got.SelectedSlot != want.SelectedSlot || got.Health != want.Health || got.FoodLevel != want.FoodLevel {
		t.Fatalf("loaded stats = %+v, want %+v", got, want)
	}
	if got.Inventory[HotbarContainerOffset+2].Count != 48 || got.Inventory[HotbarContainerOffset+2].ResolvedID() != "minecraft:cobblestone" {
		t.Fatalf("loaded slot = %+v, want 48x minecraft:cobblestone", got.Inventory[HotbarContainerOffset+2])
	}
}

func TestPlayerDataAbilitiesAndSlotItemMetas(t *testing.T) {
	orig := DefaultPlayerData(10, 64, 10)
	orig.GameMode = 1
	orig.Abilities = &PlayerAbilities{
		Invulnerable: true,
		Flying:       true,
		MayFly:       true,
		Instabuild:   true,
		MayBuild:     true,
		FlySpeed:     0.08,
		WalkSpeed:    0.15,
	}
	metaEntry := nbt.NewCompound().
		Set("Slot", nbt.Byte(36)).
		Set("Damage", nbt.Int(42)).
		Set("RepairCost", nbt.Int(3)).
		Set("CustomName", nbt.String("Excalibur"))
	orig.SlotItemMetas = &nbt.List{
		ElemID: nbt.TagCompound,
		Elems:  []nbt.Tag{metaEntry},
	}

	comp := PlayerDataToCompound(orig)
	got, err := PlayerDataFromCompound(comp)
	if err != nil {
		t.Fatalf("PlayerDataFromCompound: %v", err)
	}
	if got.Abilities == nil {
		t.Fatal("expected Abilities to be decoded")
	}
	if !got.Abilities.Flying || !got.Abilities.MayFly || !got.Abilities.Instabuild || got.Abilities.FlySpeed != 0.08 || got.Abilities.WalkSpeed != 0.15 {
		t.Fatalf("decoded Abilities = %+v, want %+v", got.Abilities, orig.Abilities)
	}
	if got.SlotItemMetas == nil || len(got.SlotItemMetas.Elems) != 1 {
		t.Fatalf("expected 1 SlotItemMetas entry, got %+v", got.SlotItemMetas)
	}
	ec, ok := got.SlotItemMetas.Elems[0].(*nbt.Compound)
	if !ok {
		t.Fatalf("expected compound element in SlotItemMetas")
	}
	if name, _ := nbtGetString(ec, "CustomName"); name != "Excalibur" {
		t.Fatalf("decoded CustomName = %q, want Excalibur", name)
	}
	if dmg, _ := nbtGetInt(ec, "Damage"); dmg != 42 {
		t.Fatalf("decoded Damage = %d, want 42", dmg)
	}
}

