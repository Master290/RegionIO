package world

import (
	"os"
	"path/filepath"
	"testing"

	"regionio/internal/nbt"
)

func TestDimensionRegistryAndTypes26_1_2(t *testing.T) {
	reg := DefaultDimensionRegistry()
	dims := reg.Dimensions()
	wantDims := []string{DimensionOverworld, DimensionNether, DimensionEnd}
	if len(dims) != len(wantDims) {
		t.Fatalf("Dimensions() = %v, want %v", dims, wantDims)
	}
	for i, w := range wantDims {
		if dims[i] != w {
			t.Fatalf("Dimensions()[%d] = %q, want %q", i, dims[i], w)
		}
	}

	ow, ok := reg.Get("overworld")
	if !ok {
		t.Fatal("expected overworld in registry")
	}
	if ow.MinY != -64 || ow.Height != 384 || ow.LogicalHeight != 384 || !ow.HasSkylight || ow.HasCeiling || ow.CoordinateScale != 1.0 || ow.AmbientLight != 0.0 || ow.SeaLevel != 63 {
		t.Fatalf("unexpected overworld dimension type: %+v", ow)
	}
	if ow.SectionCount() != 24 {
		t.Fatalf("overworld SectionCount() = %d, want 24", ow.SectionCount())
	}

	nether, ok := reg.Get("the_nether")
	if !ok {
		t.Fatal("expected the_nether in registry")
	}
	if nether.MinY != 0 || nether.Height != 256 || nether.LogicalHeight != 128 || nether.HasSkylight || !nether.HasCeiling || nether.CoordinateScale != 8.0 || nether.AmbientLight != 0.1 || nether.SeaLevel != 32 {
		t.Fatalf("unexpected the_nether dimension type: %+v", nether)
	}
	if nether.SectionCount() != 16 {
		t.Fatalf("the_nether SectionCount() = %d, want 16", nether.SectionCount())
	}

	end, ok := reg.Get("the_end")
	if !ok {
		t.Fatal("expected the_end in registry")
	}
	if end.MinY != 0 || end.Height != 256 || end.LogicalHeight != 256 || !end.HasSkylight || end.HasCeiling || end.CoordinateScale != 1.0 || end.AmbientLight != 0.25 || !end.HasEnderDragonFight {
		t.Fatalf("unexpected the_end dimension type: %+v", end)
	}
	if end.SectionCount() != 16 {
		t.Fatalf("the_end SectionCount() = %d, want 16", end.SectionCount())
	}

	// Verify NBT serialization of DimensionType.
	netherNBT := nether.ToNBT()
	if tag, ok := netherNBT.Get("min_y"); !ok || tag != nbt.Int(0) {
		t.Fatalf("nether min_y NBT = %v, want 0", tag)
	}
	if tag, ok := netherNBT.Get("height"); !ok || tag != nbt.Int(256) {
		t.Fatalf("nether height NBT = %v, want 256", tag)
	}
	if tag, ok := netherNBT.Get("coordinate_scale"); !ok || tag != nbt.Double(8.0) {
		t.Fatalf("nether coordinate_scale NBT = %v, want 8.0", tag)
	}
	if tag, ok := netherNBT.Get("has_skylight"); !ok || tag != nbt.Byte(0) {
		t.Fatalf("nether has_skylight NBT = %v, want 0", tag)
	}
	if tag, ok := netherNBT.Get("has_ceiling"); !ok || tag != nbt.Byte(1) {
		t.Fatalf("nether has_ceiling NBT = %v, want 1", tag)
	}
}

func TestScalePortalCoordinates(t *testing.T) {
	// Overworld -> Nether divides X/Z by 8.
	nx, ny, nz, scale := ScalePortalCoordinates(DimensionOverworld, DimensionNether, 800, 70, -160)
	if scale != 0.125 {
		t.Fatalf("Overworld->Nether scale = %v, want 0.125", scale)
	}
	if nx != 100 || ny != 70 || nz != -20 {
		t.Fatalf("Overworld->Nether coords = (%v, %v, %v), want (100, 70, -20)", nx, ny, nz)
	}

	// Nether -> Overworld multiplies X/Z by 8.
	ox, oy, oz, scale := ScalePortalCoordinates(DimensionNether, DimensionOverworld, 100, 70, -20)
	if scale != 8.0 {
		t.Fatalf("Nether->Overworld scale = %v, want 8.0", scale)
	}
	if ox != 800 || oy != 70 || oz != -160 {
		t.Fatalf("Nether->Overworld coords = (%v, %v, %v), want (800, 70, -160)", ox, oy, oz)
	}

	// Y clamping to Nether logical height (128).
	_, clampedY, _, _ := ScalePortalCoordinates(DimensionOverworld, DimensionNether, 0, 220, 0)
	if clampedY > float64(NetherLogicalHeight-6) {
		t.Fatalf("expected Y clamped below Nether ceiling, got %v", clampedY)
	}
}

func TestPerDimensionChunkStorageLayout(t *testing.T) {
	dir := t.TempDir()
	rootStore, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer rootStore.Close()

	// Save a Nether chunk and an End chunk via SaveDimensionChunk.
	netherChunk := GenerateNether(0, 0)
	obsidian, _ := BlockStateByName("minecraft:obsidian", nil)
	netherChunk.SetBlock(5, 65, 5, obsidian)
	if err := rootStore.SaveDimensionChunk(DimensionNether, netherChunk); err != nil {
		t.Fatalf("SaveDimensionChunk(nether): %v", err)
	}

	endChunk := GenerateEnd(6, 0) // contains X=96..111, Z=0..15 (End spawn platform at 100,48,0)
	if err := rootStore.SaveDimensionChunk(DimensionEnd, endChunk); err != nil {
		t.Fatalf("SaveDimensionChunk(end): %v", err)
	}

	// Verify vanilla 26.1.2 region file layout on disk:
	// <worldDir>/dimensions/minecraft/the_nether/region/r.0.0.mca
	// <worldDir>/dimensions/minecraft/the_end/region/r.0.0.mca
	netherRegion := filepath.Join(dir, "dimensions", "minecraft", "the_nether", "region", "r.0.0.mca")
	if _, err := os.Stat(netherRegion); err != nil {
		t.Fatalf("expected Nether region file at %s: %v", netherRegion, err)
	}
	endRegion := filepath.Join(dir, "dimensions", "minecraft", "the_end", "region", "r.0.0.mca")
	if _, err := os.Stat(endRegion); err != nil {
		t.Fatalf("expected End region file at %s: %v", endRegion, err)
	}

	// Load back and verify block states and 16-section bounds.
	loadedNether, err := rootStore.LoadDimensionChunk(DimensionNether, 0, 0)
	if err != nil {
		t.Fatalf("LoadDimensionChunk(nether): %v", err)
	}
	if loadedNether.GetBlock(5, 65, 5) != obsidian {
		t.Fatalf("loaded Nether block at (5,65,5) = %d, want obsidian (%d)", loadedNether.GetBlock(5, 65, 5), obsidian)
	}
	if loadedNether.GetBlock(0, 0, 0) != StateBedrock || loadedNether.GetBlock(0, NetherRoofY, 0) != StateBedrock {
		t.Fatalf("expected Nether floor and roof bedrock preserved after reload")
	}

	loadedEnd, err := rootStore.LoadDimensionChunk(DimensionEnd, 6, 0)
	if err != nil {
		t.Fatalf("LoadDimensionChunk(end): %v", err)
	}
	// Local coords for (100, 48, 0) in chunk (6, 0) are lx=4, y=48, lz=0.
	if loadedEnd.GetBlock(4, EndPlatformFloorY, 0) != obsidian {
		t.Fatalf("expected End obsidian spawn platform at (100, 48, 0), got state %d", loadedEnd.GetBlock(4, EndPlatformFloorY, 0))
	}
	if loadedEnd.GetBlock(4, EndSpawnY, 0) != StateAir {
		t.Fatalf("expected air above End spawn platform at (100, 49, 0)")
	}
}

func TestNetherPortalDetectionLightingAndCascadeBreak(t *testing.T) {
	cache := NewCache(256, GenerateNether)
	cache.SetDimensionType(NetherDimensionType)

	obsidian, ok := BlockStateByName("minecraft:obsidian", nil)
	if !ok {
		t.Fatal("missing minecraft:obsidian state")
	}

	// Build a 4x5 obsidian frame on the X axis with bottom-left interior at (10, 65, 10).
	baseX, baseY, baseZ := 10, 65, 10
	width, height := 2, 3
	for step := -1; step <= width; step++ {
		cache.SetBlockWithLight(baseX+step, baseY-1, baseZ, obsidian)
		cache.SetBlockWithLight(baseX+step, baseY+height, baseZ, obsidian)
	}
	for dy := 0; dy < height; dy++ {
		cache.SetBlockWithLight(baseX-1, baseY+dy, baseZ, obsidian)
		cache.SetBlockWithLight(baseX+width, baseY+dy, baseZ, obsidian)
		for step := 0; step < width; step++ {
			cache.SetBlockWithLight(baseX+step, baseY+dy, baseZ, StateAir)
		}
	}

	shape, found := FindEmptyPortalShapeNear(cache, baseX, baseY, baseZ)
	if !found {
		t.Fatal("expected FindEmptyPortalShapeNear to detect 2x3 obsidian portal frame")
	}
	if shape.Axis != PortalAxisX || shape.Width != 2 || shape.Height != 3 || shape.BottomLeft != (BlockPos{X: baseX, Y: baseY, Z: baseZ}) {
		t.Fatalf("unexpected portal shape: %+v", shape)
	}

	litShape, _, lit := LightNetherPortal(cache, baseX, baseY, baseZ)
	if !lit {
		t.Fatal("expected LightNetherPortal to succeed")
	}
	for _, pos := range litShape.InteriorPositions() {
		if !IsNetherPortalState(cache.GetBlock(pos.X, pos.Y, pos.Z)) {
			t.Fatalf("expected nether_portal block at %+v, got %s", pos, BlockName(cache.GetBlock(pos.X, pos.Y, pos.Z)))
		}
	}

	// Breaking one obsidian frame block via BreakBlockWithUpdates should cascade and break all 6 portal blocks.
	broken, _ := cache.BreakBlockWithUpdates(baseX, baseY-1, baseZ)
	if !broken {
		t.Fatal("expected BreakBlockWithUpdates to break bottom obsidian frame block")
	}
	for _, pos := range litShape.InteriorPositions() {
		if cache.GetBlock(pos.X, pos.Y, pos.Z) != StateAir {
			t.Fatalf("expected portal block at %+v to break when obsidian frame was broken, got %s", pos, BlockName(cache.GetBlock(pos.X, pos.Y, pos.Z)))
		}
	}
}

func TestPlayerDataDimensionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer st.Close()

	var uuid [16]byte
	uuid[0] = 0x42
	uuid[15] = 0x99

	pd := DefaultPlayerData(12.5, 64.0, -8.5)
	pd.Dimension = DimensionNether

	if err := st.SavePlayerData(uuid, pd); err != nil {
		t.Fatalf("SavePlayerData: %v", err)
	}
	loaded, ok, err := st.LoadPlayerData(uuid)
	if err != nil || !ok {
		t.Fatalf("LoadPlayerData: ok=%v err=%v", ok, err)
	}
	if loaded.Dimension != DimensionNether {
		t.Fatalf("loaded.Dimension = %q, want %q", loaded.Dimension, DimensionNether)
	}
}
