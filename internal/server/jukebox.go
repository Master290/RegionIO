package server

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// JukeboxEndPaddingTicks is the vanilla 20-tick (1 second) padding added after
// floor/ceil(length_in_seconds * 20) before JukeboxSong.hasFinished returns true.
const JukeboxEndPaddingTicks int64 = 20

var (
	jukeboxBlockStatesOnce     sync.Once
	jukeboxStateHasRecordTrue  uint16
	jukeboxStateHasRecordFalse uint16
)

func initJukeboxBlockStates() {
	jukeboxBlockStatesOnce.Do(func() {
		if st, ok := world.BlockStateIDByProperties("minecraft:jukebox", map[string]string{"has_record": "true"}); ok {
			jukeboxStateHasRecordTrue = st
		}
		if st, ok := world.BlockStateIDByProperties("minecraft:jukebox", map[string]string{"has_record": "false"}); ok {
			jukeboxStateHasRecordFalse = st
		}
	})
}

// JukeboxBlockState returns the vanilla 26.1.2 block state ID for
// minecraft:jukebox[has_record=true] or minecraft:jukebox[has_record=false].
func JukeboxBlockState(hasRecord bool) uint16 {
	initJukeboxBlockStates()
	if hasRecord {
		return jukeboxStateHasRecordTrue
	}
	return jukeboxStateHasRecordFalse
}

// JukeboxSongDef describes one vanilla 26.1.2 minecraft:jukebox_song entry and
// its corresponding music disc item.
type JukeboxSongDef struct {
	SongID           string  // e.g. "minecraft:cat"
	ItemID           string  // e.g. "minecraft:music_disc_cat"
	SoundEvent       string  // e.g. "minecraft:music_disc.cat"
	ComparatorOutput int     // 1..15
	LengthInSeconds  float32 // vanilla length_in_seconds
}

// LengthInTicks returns ceil(length_in_seconds * 20.0), matching vanilla
// JukeboxSong.lengthInTicks().
func (s JukeboxSongDef) LengthInTicks() int64 {
	return int64(math.Ceil(float64(s.LengthInSeconds) * 20.0))
}

// HasFinished reports whether ticksSinceSongStarted has reached or exceeded
// LengthInTicks() + 20, matching vanilla JukeboxSong.hasFinished.
func (s JukeboxSongDef) HasFinished(ticksSinceSongStarted int64) bool {
	return ticksSinceSongStarted >= s.LengthInTicks()+JukeboxEndPaddingTicks
}

// RegistryIndex returns the numeric index of SongID in the synchronized
// "minecraft:jukebox_song" registry (used as the data field of LevelEvent 1010).
func (s JukeboxSongDef) RegistryIndex() int32 {
	return int32(registry.Index("minecraft:jukebox_song", s.SongID))
}

// vanillaJukeboxSongs lists all 21 music discs and jukebox songs in Minecraft 26.1.2
// (captured verbatim from data/minecraft/jukebox_song/*.json in server-26.1.2.jar).
var vanillaJukeboxSongs = []JukeboxSongDef{
	{SongID: "minecraft:11", ItemID: "minecraft:music_disc_11", SoundEvent: "minecraft:music_disc.11", ComparatorOutput: 11, LengthInSeconds: 71.0},
	{SongID: "minecraft:13", ItemID: "minecraft:music_disc_13", SoundEvent: "minecraft:music_disc.13", ComparatorOutput: 1, LengthInSeconds: 178.0},
	{SongID: "minecraft:5", ItemID: "minecraft:music_disc_5", SoundEvent: "minecraft:music_disc.5", ComparatorOutput: 15, LengthInSeconds: 178.0},
	{SongID: "minecraft:blocks", ItemID: "minecraft:music_disc_blocks", SoundEvent: "minecraft:music_disc.blocks", ComparatorOutput: 3, LengthInSeconds: 345.0},
	{SongID: "minecraft:cat", ItemID: "minecraft:music_disc_cat", SoundEvent: "minecraft:music_disc.cat", ComparatorOutput: 2, LengthInSeconds: 185.0},
	{SongID: "minecraft:chirp", ItemID: "minecraft:music_disc_chirp", SoundEvent: "minecraft:music_disc.chirp", ComparatorOutput: 4, LengthInSeconds: 185.0},
	{SongID: "minecraft:creator", ItemID: "minecraft:music_disc_creator", SoundEvent: "minecraft:music_disc.creator", ComparatorOutput: 12, LengthInSeconds: 176.0},
	{SongID: "minecraft:creator_music_box", ItemID: "minecraft:music_disc_creator_music_box", SoundEvent: "minecraft:music_disc.creator_music_box", ComparatorOutput: 11, LengthInSeconds: 73.0},
	{SongID: "minecraft:far", ItemID: "minecraft:music_disc_far", SoundEvent: "minecraft:music_disc.far", ComparatorOutput: 5, LengthInSeconds: 174.0},
	{SongID: "minecraft:lava_chicken", ItemID: "minecraft:music_disc_lava_chicken", SoundEvent: "minecraft:music_disc.lava_chicken", ComparatorOutput: 9, LengthInSeconds: 134.0},
	{SongID: "minecraft:mall", ItemID: "minecraft:music_disc_mall", SoundEvent: "minecraft:music_disc.mall", ComparatorOutput: 6, LengthInSeconds: 197.0},
	{SongID: "minecraft:mellohi", ItemID: "minecraft:music_disc_mellohi", SoundEvent: "minecraft:music_disc.mellohi", ComparatorOutput: 7, LengthInSeconds: 96.0},
	{SongID: "minecraft:otherside", ItemID: "minecraft:music_disc_otherside", SoundEvent: "minecraft:music_disc.otherside", ComparatorOutput: 14, LengthInSeconds: 195.0},
	{SongID: "minecraft:pigstep", ItemID: "minecraft:music_disc_pigstep", SoundEvent: "minecraft:music_disc.pigstep", ComparatorOutput: 13, LengthInSeconds: 149.0},
	{SongID: "minecraft:precipice", ItemID: "minecraft:music_disc_precipice", SoundEvent: "minecraft:music_disc.precipice", ComparatorOutput: 13, LengthInSeconds: 299.0},
	{SongID: "minecraft:relic", ItemID: "minecraft:music_disc_relic", SoundEvent: "minecraft:music_disc.relic", ComparatorOutput: 14, LengthInSeconds: 218.0},
	{SongID: "minecraft:stal", ItemID: "minecraft:music_disc_stal", SoundEvent: "minecraft:music_disc.stal", ComparatorOutput: 8, LengthInSeconds: 150.0},
	{SongID: "minecraft:strad", ItemID: "minecraft:music_disc_strad", SoundEvent: "minecraft:music_disc.strad", ComparatorOutput: 9, LengthInSeconds: 188.0},
	{SongID: "minecraft:tears", ItemID: "minecraft:music_disc_tears", SoundEvent: "minecraft:music_disc.tears", ComparatorOutput: 10, LengthInSeconds: 175.0},
	{SongID: "minecraft:wait", ItemID: "minecraft:music_disc_wait", SoundEvent: "minecraft:music_disc.wait", ComparatorOutput: 12, LengthInSeconds: 238.0},
	{SongID: "minecraft:ward", ItemID: "minecraft:music_disc_ward", SoundEvent: "minecraft:music_disc.ward", ComparatorOutput: 10, LengthInSeconds: 251.0},
}

var (
	jukeboxSongsOnce   sync.Once
	jukeboxSongByItem  map[string]JukeboxSongDef
	jukeboxSongByIDMap map[string]JukeboxSongDef
)

func initJukeboxSongTables() {
	jukeboxSongsOnce.Do(func() {
		jukeboxSongByItem = make(map[string]JukeboxSongDef, len(vanillaJukeboxSongs))
		jukeboxSongByIDMap = make(map[string]JukeboxSongDef, len(vanillaJukeboxSongs))
		for _, def := range vanillaJukeboxSongs {
			jukeboxSongByItem[def.ItemID] = def
			jukeboxSongByIDMap[def.SongID] = def
		}
	})
}

// AllJukeboxSongs returns a copy of all 21 vanilla 26.1.2 jukebox song definitions.
func AllJukeboxSongs() []JukeboxSongDef {
	out := make([]JukeboxSongDef, len(vanillaJukeboxSongs))
	copy(out, vanillaJukeboxSongs)
	return out
}

func canonicalNamespacedID(id string) string {
	id = strings.TrimSpace(strings.ToLower(id))
	if id == "" {
		return ""
	}
	if !strings.Contains(id, ":") {
		return "minecraft:" + id
	}
	return id
}

// JukeboxSongForItem looks up the jukebox song played by the given music disc
// item identifier (e.g. "minecraft:music_disc_pigstep" or "music_disc_pigstep").
func JukeboxSongForItem(itemID string) (JukeboxSongDef, bool) {
	initJukeboxSongTables()
	def, ok := jukeboxSongByItem[canonicalNamespacedID(itemID)]
	return def, ok
}

// JukeboxSongByID looks up a jukebox song definition by its song registry ID
// (e.g. "minecraft:pigstep" or "pigstep").
func JukeboxSongByID(songID string) (JukeboxSongDef, bool) {
	initJukeboxSongTables()
	def, ok := jukeboxSongByIDMap[canonicalNamespacedID(songID)]
	return def, ok
}

// IsMusicDiscItem reports whether stack is a valid 26.1.2 jukebox_playable music disc.
func IsMusicDiscItem(stack world.ItemStack) bool {
	if stack.IsEmpty() {
		return false
	}
	_, ok := JukeboxSongForItem(stack.ResolvedID())
	return ok
}

// IsJukeboxBlock reports whether state is a minecraft:jukebox block state.
func IsJukeboxBlock(state uint16) bool {
	return world.BlockName(state) == "minecraft:jukebox"
}

// JukeboxStateWithRecord returns the minecraft:jukebox block state for hasRecord.
func JukeboxStateWithRecord(state uint16, hasRecord bool) uint16 {
	if !IsJukeboxBlock(state) {
		return state
	}
	return JukeboxBlockState(hasRecord)
}

// JukeboxBlockEntity represents a runtime and persisted minecraft:jukebox block entity.
type JukeboxBlockEntity struct {
	Dimension             string
	X                     int
	Y                     int
	Z                     int
	RecordItem            world.ItemStack
	TicksSinceSongStarted int64
	Playing               bool
}

// Clone returns a copy of jb.
func (jb *JukeboxBlockEntity) Clone() *JukeboxBlockEntity {
	if jb == nil {
		return nil
	}
	cp := *jb
	return &cp
}

// HasRecord reports whether this jukebox currently contains a music disc.
func (jb *JukeboxBlockEntity) HasRecord() bool {
	return jb != nil && !jb.RecordItem.IsEmpty() && IsMusicDiscItem(jb.RecordItem)
}

// Song returns the JukeboxSongDef for the currently inserted disc, if any.
func (jb *JukeboxBlockEntity) Song() (JukeboxSongDef, bool) {
	if !jb.HasRecord() {
		return JukeboxSongDef{}, false
	}
	return JukeboxSongForItem(jb.RecordItem.ResolvedID())
}

// IsPlaying reports whether the jukebox has a record and its song is currently
// playing (has not yet finished).
func (jb *JukeboxBlockEntity) IsPlaying() bool {
	if !jb.HasRecord() || !jb.Playing {
		return false
	}
	song, ok := jb.Song()
	if !ok {
		return false
	}
	return !song.HasFinished(jb.TicksSinceSongStarted)
}

// ComparatorSignal returns the redstone comparator output (1..15) for the
// inserted music disc, or 0 if empty (matching JukeboxBlock.getAnalogOutputSignal).
func (jb *JukeboxBlockEntity) ComparatorSignal() int {
	song, ok := jb.Song()
	if !ok {
		return 0
	}
	return song.ComparatorOutput
}

// RedstoneSignal returns 15 while the jukebox song is actively playing and 0
// once playback finishes or the disc is removed (matching JukeboxBlock.getSignal).
func (jb *JukeboxBlockEntity) RedstoneSignal() int {
	if jb.IsPlaying() {
		return 15
	}
	return 0
}

// ToNBT serializes the jukebox block entity into a vanilla NBT compound
// ("id": "minecraft:jukebox", "RecordItem", "ticks_since_song_started").
func (jb *JukeboxBlockEntity) ToNBT() *nbt.Compound {
	comp := nbt.NewCompound()
	if jb == nil {
		return comp
	}
	comp.Set("id", nbt.String("minecraft:jukebox"))
	comp.Set("x", nbt.Int(int32(jb.X)))
	comp.Set("y", nbt.Int(int32(jb.Y)))
	comp.Set("z", nbt.Int(int32(jb.Z)))
	if jb.Dimension != "" {
		comp.Set("dimension", nbt.String(world.NormalizeDimension(jb.Dimension)))
	}
	if jb.HasRecord() {
		rec := nbt.NewCompound()
		rec.Set("id", nbt.String(jb.RecordItem.ResolvedID()))
		cnt := jb.RecordItem.Count
		if cnt <= 0 {
			cnt = 1
		}
		rec.Set("count", nbt.Int(cnt))
		comp.Set("RecordItem", rec)
		if jb.Playing {
			comp.Set("ticks_since_song_started", nbt.Long(jb.TicksSinceSongStarted))
		}
	}
	return comp
}

// JukeboxBlockEntityFromNBT decodes a JukeboxBlockEntity from a vanilla
// block_entities NBT compound.
func JukeboxBlockEntityFromNBT(comp *nbt.Compound) (*JukeboxBlockEntity, error) {
	if comp == nil {
		return nil, errors.New("server: nil jukebox NBT")
	}
	x, _ := nbtIntTag(comp, "x")
	y, _ := nbtIntTag(comp, "y")
	z, _ := nbtIntTag(comp, "z")
	dim, _ := nbtStringTag(comp, "dimension")
	if dim == "" {
		dim = world.DimensionOverworld
	}
	jb := &JukeboxBlockEntity{
		Dimension: world.NormalizeDimension(dim),
		X:         int(x),
		Y:         int(y),
		Z:         int(z),
	}
	if recComp, ok := nbtCompoundTag(comp, "RecordItem"); ok && recComp != nil {
		itemID, _ := nbtStringTag(recComp, "id")
		cnt, ok := nbtIntTag(recComp, "count")
		if !ok || cnt <= 0 {
			cnt = 1
		}
		if itemID != "" {
			// Ensure item IDs are registered before resolving named stack.
			_ = DefaultRecipeBook()
			jb.RecordItem = world.NewNamedItemStack(itemID, cnt)
		}
	}
	if rawTicks, ok := comp.Get("ticks_since_song_started"); ok && jb.HasRecord() {
		switch v := rawTicks.(type) {
		case nbt.Long:
			jb.TicksSinceSongStarted = int64(v)
			jb.Playing = true
		case nbt.Int:
			jb.TicksSinceSongStarted = int64(v)
			jb.Playing = true
		}
		if song, ok := jb.Song(); ok && song.HasFinished(jb.TicksSinceSongStarted) {
			jb.Playing = false
		}
	}
	return jb, nil
}

// --- Server Jukebox Store ---

type serverJukeboxStore struct {
	mu         sync.RWMutex
	loadedDisk bool
	jukeboxes  map[blockPosDimKey]*JukeboxBlockEntity
}

var jukeboxStores sync.Map // map[*Server]*serverJukeboxStore

func (s *Server) jukeboxStore() *serverJukeboxStore {
	if s == nil {
		return &serverJukeboxStore{
			jukeboxes: make(map[blockPosDimKey]*JukeboxBlockEntity),
		}
	}
	if v, ok := jukeboxStores.Load(s); ok {
		st := v.(*serverJukeboxStore)
		st.mu.Lock()
		s.ensureJukeboxesLoadedLocked(st)
		st.mu.Unlock()
		return st
	}
	st := &serverJukeboxStore{
		jukeboxes: make(map[blockPosDimKey]*JukeboxBlockEntity),
	}
	actual, _ := jukeboxStores.LoadOrStore(s, st)
	res := actual.(*serverJukeboxStore)
	res.mu.Lock()
	s.ensureJukeboxesLoadedLocked(res)
	res.mu.Unlock()
	return res
}

func (s *Server) jukeboxSidecarPath() string {
	if s == nil || s.cfg.WorldDir == "" {
		return ""
	}
	return filepath.Join(s.cfg.WorldDir, "region", "jukeboxes.nbt")
}

func (s *Server) ensureJukeboxesLoadedLocked(st *serverJukeboxStore) {
	if st.loadedDisk {
		return
	}
	st.loadedDisk = true
	path := s.jukeboxSidecarPath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_, parsedTag, err := nbt.UnmarshalNamed(data)
	if err != nil {
		return
	}
	root, ok := parsedTag.(*nbt.Compound)
	if !ok || root == nil {
		return
	}
	listTag, ok := root.Get("Jukeboxes")
	if !ok {
		return
	}
	var elems []nbt.Tag
	switch v := listTag.(type) {
	case nbt.List:
		elems = v.Elems
	case *nbt.List:
		if v != nil {
			elems = v.Elems
		}
	}
	for _, el := range elems {
		comp, ok := el.(*nbt.Compound)
		if !ok {
			continue
		}
		jb, err := JukeboxBlockEntityFromNBT(comp)
		if err != nil || jb == nil || !jb.HasRecord() {
			continue
		}
		key := blockPosDimKey{dim: jb.Dimension, x: jb.X, y: jb.Y, z: jb.Z}
		st.jukeboxes[key] = jb
	}
}

// SaveJukeboxes persists all non-empty jukebox block entities to
// <WorldDir>/region/jukeboxes.nbt when WorldDir is configured.
func (s *Server) SaveJukeboxes() error {
	if s == nil {
		return nil
	}
	path := s.jukeboxSidecarPath()
	if path == "" {
		return nil
	}
	st := s.jukeboxStore()
	st.mu.RLock()
	keys := make([]blockPosDimKey, 0, len(st.jukeboxes))
	for k, jb := range st.jukeboxes {
		if jb != nil && jb.HasRecord() {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dim != keys[j].dim {
			return keys[i].dim < keys[j].dim
		}
		if keys[i].x != keys[j].x {
			return keys[i].x < keys[j].x
		}
		if keys[i].y != keys[j].y {
			return keys[i].y < keys[j].y
		}
		return keys[i].z < keys[j].z
	})
	elems := make([]nbt.Tag, 0, len(keys))
	for _, k := range keys {
		elems = append(elems, st.jukeboxes[k].ToNBT())
	}
	st.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	root := nbt.NewCompound()
	root.Set("Jukeboxes", nbt.List{ElemID: nbt.TagCompound, Elems: elems})
	return os.WriteFile(path, nbt.MarshalNamed("", root), 0o644)
}

// LoadJukeboxes reloads jukebox block entities from <WorldDir>/region/jukeboxes.nbt.
func (s *Server) LoadJukeboxes() error {
	if s == nil {
		return nil
	}
	st := s.jukeboxStore()
	st.mu.Lock()
	st.loadedDisk = false
	st.jukeboxes = make(map[blockPosDimKey]*JukeboxBlockEntity)
	s.ensureJukeboxesLoadedLocked(st)
	st.mu.Unlock()
	return nil
}

// GetJukeboxAt returns a copy of the JukeboxBlockEntity at (dimension, x, y, z), if present.
func (s *Server) GetJukeboxAt(dimension string, x, y, z int) (*JukeboxBlockEntity, bool) {
	if s == nil {
		return nil, false
	}
	key := blockPosDimKey{dim: world.NormalizeDimension(dimension), x: x, y: y, z: z}
	st := s.jukeboxStore()
	st.mu.RLock()
	jb := st.jukeboxes[key].Clone()
	st.mu.RUnlock()
	if jb == nil {
		return nil, false
	}
	return jb, true
}

// SetJukeboxAt stores or updates the JukeboxBlockEntity at (jb.Dimension, jb.X, jb.Y, jb.Z).
func (s *Server) SetJukeboxAt(jb *JukeboxBlockEntity) {
	if s == nil || jb == nil {
		return
	}
	cp := jb.Clone()
	cp.Dimension = world.NormalizeDimension(cp.Dimension)
	key := blockPosDimKey{dim: cp.Dimension, x: cp.X, y: cp.Y, z: cp.Z}
	st := s.jukeboxStore()
	st.mu.Lock()
	if !cp.HasRecord() {
		delete(st.jukeboxes, key)
	} else {
		st.jukeboxes[key] = cp
	}
	st.mu.Unlock()
}

// JukeboxComparatorSignalAt returns the redstone comparator signal (1..15) for
// the jukebox at (dimension, x, y, z), or 0 if empty.
func (s *Server) JukeboxComparatorSignalAt(dimension string, x, y, z int) int {
	jb, ok := s.GetJukeboxAt(dimension, x, y, z)
	if !ok || jb == nil {
		return 0
	}
	return jb.ComparatorSignal()
}

// JukeboxRedstoneSignalAt returns the direct redstone power (15 while playing,
// 0 when stopped/finished/empty) emitted by a jukebox at (dimension, x, y, z).
func (s *Server) JukeboxRedstoneSignalAt(dimension string, x, y, z int) int {
	jb, ok := s.GetJukeboxAt(dimension, x, y, z)
	if !ok || jb == nil {
		return 0
	}
	return jb.RedstoneSignal()
}

// InsertJukeboxDisc inserts a music disc into an empty jukebox at (dimension, x, y, z),
// starts song playback, sets has_record=true, broadcasts LevelEvent 1010, and
// updates adjacent redstone and comparators.
func (s *Server) InsertJukeboxDisc(dimension string, x, y, z int, disc world.ItemStack) (bool, world.BlockUpdateResult) {
	var res world.BlockUpdateResult
	if s == nil || !IsMusicDiscItem(disc) {
		return false, res
	}
	dim := world.NormalizeDimension(dimension)
	song, ok := JukeboxSongForItem(disc.ResolvedID())
	if !ok {
		return false, res
	}

	cache := s.BindNoteBlockAndJukeboxRedstone(dim)
	if cache != nil {
		curState := cache.GetBlock(x, y, z)
		if curState != world.StateAir && !IsJukeboxBlock(curState) {
			return false, res
		}
	}

	key := blockPosDimKey{dim: dim, x: x, y: y, z: z}
	st := s.jukeboxStore()
	st.mu.Lock()
	if existing := st.jukeboxes[key]; existing != nil && existing.HasRecord() {
		st.mu.Unlock()
		return false, res
	}
	jb := &JukeboxBlockEntity{
		Dimension:             dim,
		X:                     x,
		Y:                     y,
		Z:                     z,
		RecordItem:            world.NewNamedItemStack(disc.ResolvedID(), 1),
		TicksSinceSongStarted: 0,
		Playing:               true,
	}
	st.jukeboxes[key] = jb
	st.mu.Unlock()

	if cache != nil {
		trueState := JukeboxBlockState(true)
		_, res = cache.ApplyBlockWithUpdates(x, y, z, trueState)
		nbRes := cache.UpdateNeighbors(x, y, z)
		res.Changes = append(res.Changes, nbRes.Changes...)
		compRes := cache.UpdateComparatorsAround(x, y, z)
		res.Changes = append(res.Changes, compRes.Changes...)
		s.broadcastBlockUpdateInDim(dim, x, y, z, trueState)
	}

	// Broadcast LevelEvent 1010 (PLAY_JUKEBOX_SONG) with the jukebox_song registry index.
	songIdx := song.RegistryIndex()
	if songIdx >= 0 {
		cx := int32(x >> 4)
		cz := int32(z >> 4)
		s.BroadcastChunk(cx, cz, PlayLevelEventCB,
			EncodeLevelEventPacket(LevelEventPlayJukeboxSong, x, y, z, songIdx, false))
	}

	s.NotifyJukeboxPlayingForAllays(dim, x, y, z, true)

	_ = s.SaveJukeboxes()
	return true, res
}

// EjectJukeboxDisc removes the music disc from the jukebox at (dimension, x, y, z),
// stops playback (broadcasting LevelEvent 1011), sets has_record=false, spawns
// the dropped disc entity above the jukebox, and updates redstone and comparators.
func (s *Server) EjectJukeboxDisc(dimension string, x, y, z int) (world.ItemStack, bool, world.BlockUpdateResult) {
	var res world.BlockUpdateResult
	if s == nil {
		return world.ItemStack{}, false, res
	}
	dim := world.NormalizeDimension(dimension)
	key := blockPosDimKey{dim: dim, x: x, y: y, z: z}

	st := s.jukeboxStore()
	st.mu.Lock()
	jb, exists := st.jukeboxes[key]
	if !exists || jb == nil || !jb.HasRecord() {
		st.mu.Unlock()
		return world.ItemStack{}, false, res
	}
	disc := jb.RecordItem
	delete(st.jukeboxes, key)
	st.mu.Unlock()

	cache := s.BindNoteBlockAndJukeboxRedstone(dim)
	if cache != nil {
		curState := cache.GetBlock(x, y, z)
		if IsJukeboxBlock(curState) {
			falseState := JukeboxBlockState(false)
			_, res = cache.ApplyBlockWithUpdates(x, y, z, falseState)
			nbRes := cache.UpdateNeighbors(x, y, z)
			res.Changes = append(res.Changes, nbRes.Changes...)
			s.broadcastBlockUpdateInDim(dim, x, y, z, falseState)
		}
		compRes := cache.UpdateComparatorsAround(x, y, z)
		res.Changes = append(res.Changes, compRes.Changes...)
	}

	// Broadcast LevelEvent 1011 (STOP_JUKEBOX_SONG).
	cx := int32(x >> 4)
	cz := int32(z >> 4)
	s.BroadcastChunk(cx, cz, PlayLevelEventCB,
		EncodeLevelEventPacket(LevelEventStopJukeboxSong, x, y, z, 0, false))

	s.NotifyJukeboxPlayingForAllays(dim, x, y, z, false)

	// Spawn the ejected disc item entity above the jukebox (matching JukeboxBlockEntity.popOutTheItem).
	_, _ = s.SpawnItemEntityInDimension(dim, float64(x)+0.5, float64(y)+1.01, float64(z)+0.5, disc)

	_ = s.SaveJukeboxes()
	return disc, true, res
}

// InteractJukebox handles a player right-clicking a minecraft:jukebox at (x, y, z):
//   - If the jukebox holds a disc, ejects it and stops playback.
//   - If the jukebox is empty and the player holds a music disc, inserts 1 disc,
//     starts playback, and checks the "Sound of Music" (play_jukebox_in_meadows)
//     advancement when the jukebox is in a minecraft:meadow biome.
func (s *Server) InteractJukebox(session *PlayerSession, hand int32, x, y, z int) (bool, world.BlockUpdateResult) {
	var res world.BlockUpdateResult
	if s == nil || session == nil || session.IsDead() {
		return false, res
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return false, res
	}
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.BindNoteBlockAndJukeboxRedstone(dim)
	if cache != nil {
		state := cache.GetBlock(x, y, z)
		if state != world.StateAir && !IsJukeboxBlock(state) {
			return false, res
		}
		// If the block state has has_record=true or the store holds a record, eject first.
		if state == JukeboxBlockState(true) {
			if _, ejected, ejRes := s.EjectJukeboxDisc(dim, x, y, z); ejected {
				return true, ejRes
			}
			// State was out of sync with store; reset to has_record=false.
			falseState := JukeboxBlockState(false)
			_, res = cache.ApplyBlockWithUpdates(x, y, z, falseState)
			s.broadcastBlockUpdateInDim(dim, x, y, z, falseState)
			return true, res
		}
	}
	if _, ejected, ejRes := s.EjectJukeboxDisc(dim, x, y, z); ejected {
		return true, ejRes
	}

	slot, held := session.handSlotAndStack(hand)
	if !IsMusicDiscItem(held) {
		return false, res
	}

	inserted, insRes := s.InsertJukeboxDisc(dim, x, y, z, held)
	if !inserted {
		return false, res
	}

	// Consume 1 disc in survival/adventure mode.
	if snap.GameMode != GameModeCreative {
		rem := held.Count - 1
		if rem <= 0 {
			session.SetInventorySlot(slot, world.ItemStack{})
		} else {
			session.SetInventorySlot(slot, world.NewItemStack(held.ResolvedItemID(), rem))
		}
		s.syncPlayerInventorySlot(session, slot)
	}

	// Check "Sound of Music" (minecraft:adventure/play_jukebox_in_meadows):
	// triggers when a jukebox is played in a minecraft:meadow biome.
	if cache != nil {
		meadowIdx := registry.Index("minecraft:worldgen/biome", "minecraft:meadow")
		if meadowIdx >= 0 && int(cache.GetBiomeAt(x, y, z)) == meadowIdx {
			s.TriggerPlayJukeboxInMeadows(session)
		}
	}

	return true, insRes
}

// BreakJukeboxAt cleans up jukebox state when a jukebox block at (dimension, x, y, z)
// is broken: stops playback (LevelEvent 1011), spawns the dropped disc if present,
// and updates redstone and comparators.
func (s *Server) BreakJukeboxAt(dimension string, x, y, z int) (world.ItemStack, world.BlockUpdateResult) {
	disc, _, res := s.EjectJukeboxDisc(dimension, x, y, z)
	return disc, res
}

// TickJukeboxes advances all actively playing jukeboxes by 1 server tick (50ms):
//   - Emits a note particle above the jukebox every 20 ticks (matching JukeboxSongPlayer.tick).
//   - When ticks_since_song_started reaches song.LengthInTicks() + 20, stops active
//     playback (Playing = false), broadcasts LevelEvent 1011, and updates adjacent
//     redstone (dropping direct redstone power from 15 to 0 while keeping has_record=true
//     and comparator output active).
func (s *Server) TickJukeboxes() {
	if s == nil {
		return
	}
	st := s.jukeboxStore()

	type finishedEntry struct {
		key blockPosDimKey
	}
	type particleEntry struct {
		key  blockPosDimKey
		note int
	}

	var finished []finishedEntry
	var particles []particleEntry

	st.mu.Lock()
	for key, jb := range st.jukeboxes {
		if jb == nil || !jb.HasRecord() || !jb.Playing {
			continue
		}
		song, ok := jb.Song()
		if !ok {
			jb.Playing = false
			continue
		}
		if song.HasFinished(jb.TicksSinceSongStarted) {
			jb.Playing = false
			finished = append(finished, finishedEntry{key: key})
			continue
		}
		if jb.TicksSinceSongStarted%20 == 0 {
			// Vanilla JukeboxSongPlayer.spawnMusicParticles uses a note index 0..24;
			// derive a deterministic cycling note from ticks_since_song_started.
			noteIdx := int((jb.TicksSinceSongStarted / 20) % 25)
			particles = append(particles, particleEntry{
				key:  key,
				note: noteIdx,
			})
		}
		jb.TicksSinceSongStarted++
	}
	st.mu.Unlock()

	for _, p := range particles {
		cx := int32(p.key.x >> 4)
		cz := int32(p.key.z >> 4)
		s.BroadcastChunk(cx, cz, PlayLevelParticlesCB,
			EncodeNoteParticlePacket(float64(p.key.x)+0.5, float64(p.key.y)+1.2, float64(p.key.z)+0.5, p.note))
	}

	if len(finished) > 0 {
		for _, f := range finished {
			cx := int32(f.key.x >> 4)
			cz := int32(f.key.z >> 4)
			s.BroadcastChunk(cx, cz, PlayLevelEventCB,
				EncodeLevelEventPacket(LevelEventStopJukeboxSong, f.key.x, f.key.y, f.key.z, 0, false))

			s.NotifyJukeboxPlayingForAllays(f.key.dim, f.key.x, f.key.y, f.key.z, false)

			if cache := s.BindNoteBlockAndJukeboxRedstone(f.key.dim); cache != nil {
				// Re-trigger redstone propagation around the jukebox now that its
				// direct redstone signal dropped from 15 to 0, while keeping
				// has_record=true on the block state.
				_ = cache.UpdateNeighbors(f.key.x, f.key.y, f.key.z)
				_ = cache.UpdateComparatorsAround(f.key.x, f.key.y, f.key.z)
			}
		}
		_ = s.SaveJukeboxes()
	}
}

// HopperTryInsertIntoJukebox attempts to insert 1 music disc from stack into an
// empty jukebox at (dimension, x, y, z), returning whether insertion succeeded
// and the remaining stack.
func (s *Server) HopperTryInsertIntoJukebox(dimension string, x, y, z int, stack world.ItemStack) (bool, world.ItemStack, world.BlockUpdateResult) {
	var res world.BlockUpdateResult
	if s == nil || !IsMusicDiscItem(stack) {
		return false, stack, res
	}
	inserted, insRes := s.InsertJukeboxDisc(dimension, x, y, z, stack)
	if !inserted {
		return false, stack, res
	}
	rem := stack.Count - 1
	var out world.ItemStack
	if rem > 0 {
		out = world.NewItemStack(stack.ResolvedItemID(), rem)
	}
	return true, out, insRes
}

// HopperTryExtractFromJukebox attempts to extract a music disc from the jukebox
// at (dimension, x, y, z) into a hopper, stopping any active playback, setting
// has_record=false, and updating redstone and comparators without spawning a
// world item entity.
func (s *Server) HopperTryExtractFromJukebox(dimension string, x, y, z int) (world.ItemStack, bool, world.BlockUpdateResult) {
	var res world.BlockUpdateResult
	if s == nil {
		return world.ItemStack{}, false, res
	}
	dim := world.NormalizeDimension(dimension)
	key := blockPosDimKey{dim: dim, x: x, y: y, z: z}

	st := s.jukeboxStore()
	st.mu.Lock()
	jb, exists := st.jukeboxes[key]
	if !exists || jb == nil || !jb.HasRecord() {
		st.mu.Unlock()
		return world.ItemStack{}, false, res
	}
	disc := jb.RecordItem
	delete(st.jukeboxes, key)
	st.mu.Unlock()

	cache := s.BindNoteBlockAndJukeboxRedstone(dim)
	if cache != nil {
		curState := cache.GetBlock(x, y, z)
		if IsJukeboxBlock(curState) {
			falseState := JukeboxBlockState(false)
			_, res = cache.ApplyBlockWithUpdates(x, y, z, falseState)
			nbRes := cache.UpdateNeighbors(x, y, z)
			res.Changes = append(res.Changes, nbRes.Changes...)
			s.broadcastBlockUpdateInDim(dim, x, y, z, falseState)
		}
		compRes := cache.UpdateComparatorsAround(x, y, z)
		res.Changes = append(res.Changes, compRes.Changes...)
	}

	cx := int32(x >> 4)
	cz := int32(z >> 4)
	s.BroadcastChunk(cx, cz, PlayLevelEventCB,
		EncodeLevelEventPacket(LevelEventStopJukeboxSong, x, y, z, 0, false))

	s.NotifyJukeboxPlayingForAllays(dim, x, y, z, false)

	_ = s.SaveJukeboxes()
	return disc, true, res
}
