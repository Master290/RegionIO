package server

import (
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/world"
)

type showcaseChunkKey struct {
	dim    string
	cx, cz int32
}

type serverShowcaseTracker struct {
	mu          sync.Mutex
	loadedStore bool
	loadedChunk map[showcaseChunkKey]bool
}

var showcaseTrackers sync.Map // map[*Server]*serverShowcaseTracker

func (s *Server) showcaseTracker() *serverShowcaseTracker {
	if s == nil {
		return &serverShowcaseTracker{loadedChunk: make(map[showcaseChunkKey]bool)}
	}
	if v, ok := showcaseTrackers.Load(s); ok {
		return v.(*serverShowcaseTracker)
	}
	tr := &serverShowcaseTracker{loadedChunk: make(map[showcaseChunkKey]bool)}
	actual, _ := showcaseTrackers.LoadOrStore(s, tr)
	return actual.(*serverShowcaseTracker)
}

// IsShowcaseBlockEntityID reports whether id is one of the 9 showcase /
// interactive block entity IDs in Vanilla 26.1.2:
// sign, hanging_sign, lectern, chiseled_bookshelf, jukebox, banner,
// decorated_pot, beacon, and conduit.
func IsShowcaseBlockEntityID(id string) bool {
	switch canonicalResourceName(id) {
	case "minecraft:sign",
		"minecraft:hanging_sign",
		"minecraft:lectern",
		"minecraft:chiseled_bookshelf",
		"minecraft:jukebox",
		"minecraft:banner",
		"minecraft:decorated_pot",
		"minecraft:beacon",
		"minecraft:conduit":
		return true
	default:
		return false
	}
}

// ChunkShowcaseBlockEntitiesNBT returns all vanilla 26.1.2 block_entities NBT
// compounds for showcase block entities in chunk (cx, cz) of dimension.
func (s *Server) ChunkShowcaseBlockEntitiesNBT(dimension string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	dim := world.NormalizeDimension(dimension)
	var out []*nbt.Compound
	out = append(out, s.ChunkSignAndLecternBlockEntitiesNBT(dim, cx, cz)...)
	out = append(out, s.ChunkChiseledBookshelfBlockEntitiesNBT(dim, cx, cz)...)
	out = append(out, s.ChunkJukeboxBlockEntitiesNBT(dim, cx, cz)...)
	out = append(out, s.ChunkBannerBlockEntitiesNBT(dim, cx, cz)...)
	out = append(out, s.ChunkDecoratedPotBlockEntitiesNBT(dim, cx, cz)...)
	out = append(out, s.ChunkBeaconBlockEntitiesNBT(dim, cx, cz)...)
	out = append(out, s.ChunkConduitBlockEntitiesNBT(dim, cx, cz)...)
	return out
}

// syncShowcaseChunkInMemory synchronizes the live in-memory chunk's
// rawBlockEntities for the chunk containing (x, z) in dimension and invalidates
// any cached LevelChunkWithLight frame so wire encoding reflects the update.
func (s *Server) syncShowcaseChunkInMemory(dimension string, x, z int) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return
	}
	cx := int32(x >> 4)
	cz := int32(z >> 4)
	tr := s.showcaseTracker()
	tr.mu.Lock()
	tr.loadedChunk[showcaseChunkKey{dim: normDim, cx: cx, cz: cz}] = true
	tr.mu.Unlock()
	if !cache.IsChunkLoaded(cx, cz) {
		return
	}
	list := s.ChunkShowcaseBlockEntitiesNBT(normDim, cx, cz)
	cache.SyncChunkBlockEntitiesInMemory(cx, cz, IsShowcaseBlockEntityID, list)
}

func (s *Server) applyLoadedShowcaseCompounds(dim string, comps []*nbt.Compound) {
	if s == nil || len(comps) == 0 {
		return
	}
	normDim := world.NormalizeDimension(dim)
	elems := make([]nbt.Tag, 0, len(comps))
	var beaconPositions [][3]int
	var conduitPositions [][3]int
	for _, c := range comps {
		if c == nil {
			continue
		}
		elems = append(elems, c)
		id, _ := nbtStringTag(c, "id")
		bx, _ := nbtIntTag(c, "x")
		by, _ := nbtIntTag(c, "y")
		bz, _ := nbtIntTag(c, "z")
		switch canonicalResourceName(id) {
		case "minecraft:beacon":
			beaconPositions = append(beaconPositions, [3]int{int(bx), int(by), int(bz)})
		case "minecraft:conduit":
			conduitPositions = append(conduitPositions, [3]int{int(bx), int(by), int(bz)})
		}
	}
	list := nbt.List{ElemID: nbt.TagCompound, Elems: elems}

	s.LoadChunkSignAndLecternBlockEntitiesNBT(normDim, list)
	s.LoadChunkBannerBlockEntitiesNBT(normDim, list)
	s.LoadChunkBeaconBlockEntitiesNBT(normDim, list)
	s.LoadChunkConduitBlockEntitiesNBT(normDim, list)
	s.LoadChunkChiseledBookshelfBlockEntitiesNBT(normDim, comps)
	s.LoadChunkDecoratedPotBlockEntitiesNBT(normDim, comps)
	s.LoadChunkJukeboxBlockEntitiesNBT(normDim, comps)

	for _, pos := range beaconPositions {
		_, _ = s.UpdateBeaconAtInDim(normDim, pos[0], pos[1], pos[2], nil)
	}
	for _, pos := range conduitPositions {
		_, _ = s.UpdateConduitAtInDim(normDim, pos[0], pos[1], pos[2])
	}
}

// LoadShowcaseBlockEntitiesFromStore loads all persisted showcase block entities
// across Overworld, Nether, and End Anvil (.mca) stores and synchronizes their
// world block states, pyramid/frame status, and redstone comparators.
func (s *Server) LoadShowcaseBlockEntitiesFromStore() error {
	if s == nil {
		return nil
	}
	tr := s.showcaseTracker()
	tr.mu.Lock()
	if tr.loadedStore {
		tr.mu.Unlock()
		return nil
	}
	tr.loadedStore = true
	tr.mu.Unlock()

	for _, dim := range []string{world.DimensionOverworld, world.DimensionNether, world.DimensionEnd} {
		var store *world.Store
		if dim == world.DimensionOverworld {
			store = s.activeFurnaceStore()
		}
		if store == nil {
			if cache := s.DimensionChunks(dim); cache != nil {
				store = cache.Store()
			}
		}
		if store == nil {
			continue
		}
		comps, err := store.LoadAllBlockEntities(IsShowcaseBlockEntityID)
		if err != nil || len(comps) == 0 {
			continue
		}
		tr.mu.Lock()
		for _, comp := range comps {
			bx, _ := nbtIntTag(comp, "x")
			bz, _ := nbtIntTag(comp, "z")
			tr.loadedChunk[showcaseChunkKey{dim: dim, cx: bx >> 4, cz: bz >> 4}] = true
		}
		tr.mu.Unlock()
		s.applyLoadedShowcaseCompounds(dim, comps)
	}
	return nil
}

// ReloadChunkShowcaseBlockEntities loads any persisted showcase block entities
// for chunk (cx, cz) in dimension when that chunk is loaded on demand.
func (s *Server) ReloadChunkShowcaseBlockEntities(dimension string, cx, cz int32) error {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dimension)
	ck := showcaseChunkKey{dim: normDim, cx: cx, cz: cz}
	tr := s.showcaseTracker()
	tr.mu.Lock()
	if tr.loadedChunk[ck] {
		tr.mu.Unlock()
		return nil
	}
	tr.loadedChunk[ck] = true
	tr.mu.Unlock()

	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return nil
	}
	comps, err := cache.LoadChunkBlockEntities(cx, cz, IsShowcaseBlockEntityID)
	if err != nil || len(comps) == 0 {
		return err
	}
	s.applyLoadedShowcaseCompounds(normDim, comps)
	return nil
}

// SaveShowcaseBlockEntitiesToStore flushes all showcase block entities into
// their respective Anvil (.mca) chunk `block_entities` lists.
func (s *Server) SaveShowcaseBlockEntitiesToStore() error {
	if s == nil {
		return nil
	}
	chunks := make(map[showcaseChunkKey]bool)

	tr := s.showcaseTracker()
	tr.mu.Lock()
	for ck := range tr.loadedChunk {
		chunks[ck] = true
	}
	tr.mu.Unlock()

	signSt := s.signStore()
	signSt.mu.RLock()
	for k := range signSt.signs {
		chunks[showcaseChunkKey{dim: world.NormalizeDimension(k.dim), cx: int32(k.x >> 4), cz: int32(k.z >> 4)}] = true
	}
	signSt.mu.RUnlock()

	lecSt := s.lecternStore()
	lecSt.mu.RLock()
	for k := range lecSt.lecterns {
		chunks[showcaseChunkKey{dim: world.NormalizeDimension(k.dim), cx: int32(k.x >> 4), cz: int32(k.z >> 4)}] = true
	}
	lecSt.mu.RUnlock()

	hs := s.hopperState()
	hs.mu.Lock()
	for k := range hs.bookshelves {
		chunks[showcaseChunkKey{dim: world.NormalizeDimension(k.dim), cx: int32(k.x >> 4), cz: int32(k.z >> 4)}] = true
	}
	hs.mu.Unlock()

	jbSt := s.jukeboxStore()
	jbSt.mu.RLock()
	for k := range jbSt.jukeboxes {
		chunks[showcaseChunkKey{dim: world.NormalizeDimension(k.dim), cx: int32(k.x >> 4), cz: int32(k.z >> 4)}] = true
	}
	jbSt.mu.RUnlock()

	banSt := s.bannerStore()
	banSt.mu.RLock()
	for k := range banSt.banners {
		chunks[showcaseChunkKey{dim: world.NormalizeDimension(k.dim), cx: int32(k.x >> 4), cz: int32(k.z >> 4)}] = true
	}
	banSt.mu.RUnlock()

	potSt := s.decoratedPotStore()
	potSt.mu.Lock()
	for k := range potSt.pots {
		chunks[showcaseChunkKey{dim: world.NormalizeDimension(k.dim), cx: int32(k.x >> 4), cz: int32(k.z >> 4)}] = true
	}
	potSt.mu.Unlock()

	bs := s.beaconState()
	bs.mu.Lock()
	for k := range bs.beacons {
		chunks[showcaseChunkKey{dim: world.NormalizeDimension(k.dim), cx: int32(k.x >> 4), cz: int32(k.z >> 4)}] = true
	}
	bs.mu.Unlock()

	cs := s.conduitState()
	cs.mu.Lock()
	for k := range cs.conduits {
		chunks[showcaseChunkKey{dim: world.NormalizeDimension(k.dim), cx: int32(k.x >> 4), cz: int32(k.z >> 4)}] = true
	}
	cs.mu.Unlock()

	var firstErr error
	for ck := range chunks {
		cache := s.DimensionChunks(ck.dim)
		if cache == nil {
			continue
		}
		list := s.ChunkShowcaseBlockEntitiesNBT(ck.dim, ck.cx, ck.cz)
		if err := cache.SaveChunkBlockEntities(ck.cx, ck.cz, IsShowcaseBlockEntityID, list); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
