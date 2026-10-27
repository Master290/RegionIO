package world

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"regionio/internal/worldgen"
)

// jigsaw.go ports net.minecraft.world.level.levelgen.structure.structures.JigsawStructure,
// net.minecraft.world.level.levelgen.structure.pools.JigsawPlacement (addPieces + Placer),
// StructurePoolElement (SinglePoolElement, LegacySinglePoolElement, ListPoolElement,
// FeaturePoolElement, EmptyPoolElement), PoolElementStructurePiece, StructureTemplate
// block sorting/processors/waterlogging, and Beardifier from 26.1.2 bytecode.

// JigsawJunction ports net.minecraft.world.level.levelgen.structure.pools.JigsawJunction.
type JigsawJunction struct {
	SourceX       int
	SourceGroundY int
	SourceZ       int
	DeltaY        int
	DestProj      string
}

// JigsawPiece represents one placed PoolElementStructurePiece in a Jigsaw structure start.
type JigsawPiece struct {
	Element          worldgen.PoolElementDef
	Pos              [3]int
	GroundLevelDelta int
	Rotation         int
	Box              msBox
	LiquidSettings   string
	Junctions        []JigsawJunction
}

// JigsawStart represents a generated Jigsaw structure start (Villages, Pillager Outposts,
// Ancient Cities, Trail Ruins, Trial Chambers).
type JigsawStart struct {
	StructureName     string
	Step              string
	TerrainAdaptation string
	ChunkX            int32
	ChunkZ            int32
	StubPos           [3]int
	Box               msBox
	Pieces            []*JigsawPiece
}

type cachedJigsawPalette struct {
	blocks  []worldgen.TemplateBlockInfo
	jigsaws []worldgen.TemplateBlockInfo
}

type cachedJigsawTemplate struct {
	size     [3]int
	palettes []cachedJigsawPalette
	entities []worldgen.TemplateEntityInfo
}

const (
	maxJigsawTemplateCacheEntries = 2048
	maxJigsawStartCacheEntries    = 256
)

var (
	jigsawTemplateMu    sync.RWMutex
	jigsawTemplateCache = map[string]*cachedJigsawTemplate{}
	poolMaxSizeMu       sync.RWMutex
	poolMaxSizeCache    = map[*worldgen.TemplatePoolDef]int{}
)

func loadCachedJigsawTemplate(location string) (*cachedJigsawTemplate, error) {
	clean := strings.TrimPrefix(location, "minecraft:")
	jigsawTemplateMu.RLock()
	if cached, ok := jigsawTemplateCache[clean]; ok {
		jigsawTemplateMu.RUnlock()
		return cached, nil
	}
	jigsawTemplateMu.RUnlock()

	stateByIDOnce.Do(buildStateTable)
	raw, err := worldgen.EmbeddedStructureTemplate(clean)
	if err != nil {
		return nil, err
	}
	rawPalettes, entities, size, err := worldgen.LoadResolvedTemplateFullBytes(raw, nameToStateID)
	if err != nil {
		return nil, err
	}

	// Sort each palette's blocks matching StructureTemplate.loadPalette / buildInfoList:
	// fullBlocks (0) -> otherBlocks (1) -> blocksWithNbt (2), each sorted by (Y, X, Z) ascending.
	blockBucket := func(b *worldgen.TemplateBlockInfo) int {
		if b.HasNBT {
			return 2
		}
		if fullSolidState(b.State) {
			return 0
		}
		return 1
	}
	palettes := make([]cachedJigsawPalette, len(rawPalettes))
	for pIdx, blocks := range rawPalettes {
		sort.Slice(blocks, func(i, j int) bool {
			bi, bj := blockBucket(&blocks[i]), blockBucket(&blocks[j])
			if bi != bj {
				return bi < bj
			}
			if blocks[i].Pos[1] != blocks[j].Pos[1] {
				return blocks[i].Pos[1] < blocks[j].Pos[1]
			}
			if blocks[i].Pos[0] != blocks[j].Pos[0] {
				return blocks[i].Pos[0] < blocks[j].Pos[0]
			}
			return blocks[i].Pos[2] < blocks[j].Pos[2]
		})

		jigsawCount := 0
		for i := range blocks {
			if blocks[i].Jigsaw != nil {
				jigsawCount++
			}
		}
		var jigsaws []worldgen.TemplateBlockInfo
		if jigsawCount > 0 {
			jigsaws = make([]worldgen.TemplateBlockInfo, 0, jigsawCount)
			for i := range blocks {
				b := &blocks[i]
				if b.Jigsaw != nil {
					b.Jigsaw.Name = normalizeResourceID(b.Jigsaw.Name)
					b.Jigsaw.Target = normalizeResourceID(b.Jigsaw.Target)
					b.Jigsaw.Pool = normalizeResourceID(b.Jigsaw.Pool)
					jigsaws = append(jigsaws, *b)
				}
			}
		}
		palettes[pIdx] = cachedJigsawPalette{
			blocks:  blocks,
			jigsaws: jigsaws,
		}
	}

	res := &cachedJigsawTemplate{
		size:     size,
		palettes: palettes,
		entities: entities,
	}
	jigsawTemplateMu.Lock()
	if len(jigsawTemplateCache) >= maxJigsawTemplateCacheEntries {
		clear(jigsawTemplateCache)
	}
	jigsawTemplateCache[clean] = res
	jigsawTemplateMu.Unlock()
	return res, nil
}

func selectJigsawPalette(tmpl *cachedJigsawTemplate, pos [3]int) *cachedJigsawPalette {
	if tmpl == nil || len(tmpl.palettes) == 0 {
		return nil
	}
	if len(tmpl.palettes) == 1 {
		return &tmpl.palettes[0]
	}
	var seed int64
	if pos != ([3]int{0, 0, 0}) {
		seed = worldgen.MthGetSeed(pos[0], pos[1], pos[2])
	}
	var rng worldgen.Legacy
	rng.SetSeed(seed)
	return &tmpl.palettes[int(rng.NextIntN(int32(len(tmpl.palettes))))]
}

type rotatedJigsawInfo struct {
	WorldPos          [3]int
	Front             string
	Top               string
	Name              string
	Target            string
	Pool              string
	FinalState        string
	Joint             string
	SelectionPriority int
	PlacementPriority int
}

func rotateJigsawDirection(dir string, rotation int) string {
	switch dir {
	case "north", "east", "south", "west":
		dirs := [4]string{"north", "east", "south", "west"}
		idx := 0
		switch dir {
		case "east":
			idx = 1
		case "south":
			idx = 2
		case "west":
			idx = 3
		}
		return dirs[(idx+rotation)&3]
	default:
		return dir
	}
}

func parseJigsawOrientation(orient string, rotation int) (front, top string) {
	p0, p1, ok := strings.Cut(orient, "_")
	if !ok {
		return rotateJigsawDirection("north", rotation), "up"
	}
	return rotateJigsawDirection(p0, rotation), rotateJigsawDirection(p1, rotation)
}

func jigsawOppositeDir(dir string) string {
	switch dir {
	case "north":
		return "south"
	case "south":
		return "north"
	case "west":
		return "east"
	case "east":
		return "west"
	case "down":
		return "up"
	case "up":
		return "down"
	default:
		return dir
	}
}

func jigsawDirStep(dir string) [3]int {
	switch dir {
	case "north":
		return [3]int{0, 0, -1}
	case "south":
		return [3]int{0, 0, 1}
	case "west":
		return [3]int{-1, 0, 0}
	case "east":
		return [3]int{1, 0, 0}
	case "down":
		return [3]int{0, -1, 0}
	case "up":
		return [3]int{0, 1, 0}
	default:
		return [3]int{}
	}
}

func normalizeResourceID(id string) string {
	if id == "" {
		return "minecraft:empty"
	}
	if strings.HasPrefix(id, "minecraft:") {
		return id
	}
	return "minecraft:" + id
}

func jigsawShuffleSlice[T any](list []T, random worldgen.RandomSource) {
	for i := len(list); i > 1; i-- {
		j := int(random.NextIntN(int32(i)))
		list[i-1], list[j] = list[j], list[i-1]
	}
}

func msEncapsulate(a, b msBox) msBox {
	return msBox{
		minX: min(a.minX, b.minX),
		minY: min(a.minY, b.minY),
		minZ: min(a.minZ, b.minZ),
		maxX: max(a.maxX, b.maxX),
		maxY: max(a.maxY, b.maxY),
		maxZ: max(a.maxZ, b.maxZ),
	}
}

func getElementBoundingBox(elem worldgen.PoolElementDef, pos [3]int, rotation int) msBox {
	switch elem.ElementType {
	case "minecraft:feature_pool_element":
		return msBox{pos[0], pos[1], pos[2], pos[0], pos[1], pos[2]}
	case "minecraft:list_pool_element":
		first := true
		var box msBox
		for _, sub := range elem.Elements {
			if sub.ElementType == "minecraft:empty_pool_element" {
				continue
			}
			sb := getElementBoundingBox(sub, pos, rotation)
			if first {
				box = sb
				first = false
			} else {
				box = msEncapsulate(box, sb)
			}
		}
		return box
	case "minecraft:single_pool_element", "minecraft:legacy_single_pool_element":
		tmpl, err := loadCachedJigsawTemplate(elem.Location)
		if err != nil || tmpl == nil {
			return msBox{pos[0], pos[1], pos[2], pos[0], pos[1], pos[2]}
		}
		sx, sy, sz := tmpl.size[0], tmpl.size[1], tmpl.size[2]
		if sx <= 0 || sy <= 0 || sz <= 0 {
			return msBox{pos[0], pos[1], pos[2], pos[0] - 1, pos[1] - 1, pos[2] - 1}
		}
		switch rotation & 3 {
		case 1: // CLOCKWISE_90
			return msBox{pos[0] - sz + 1, pos[1], pos[2], pos[0], pos[1] + sy - 1, pos[2] + sx - 1}
		case 2: // CLOCKWISE_180
			return msBox{pos[0] - sx + 1, pos[1], pos[2] - sz + 1, pos[0], pos[1] + sy - 1, pos[2]}
		case 3: // COUNTERCLOCKWISE_90
			return msBox{pos[0], pos[1], pos[2] - sx + 1, pos[0] + sz - 1, pos[1] + sy - 1, pos[2]}
		default: // NONE
			return msBox{pos[0], pos[1], pos[2], pos[0] + sx - 1, pos[1] + sy - 1, pos[2] + sz - 1}
		}
	default:
		return msBox{pos[0], pos[1], pos[2], pos[0] - 1, pos[1] - 1, pos[2] - 1}
	}
}

func getShuffledJigsawBlocks(elem worldgen.PoolElementDef, pos [3]int, rotation int, random worldgen.RandomSource) []rotatedJigsawInfo {
	return appendShuffledJigsawBlocks(nil, elem, pos, rotation, random)
}

func appendShuffledJigsawBlocks(dst []rotatedJigsawInfo, elem worldgen.PoolElementDef, pos [3]int, rotation int, random worldgen.RandomSource) []rotatedJigsawInfo {
	dst = dst[:0]
	switch elem.ElementType {
	case "minecraft:feature_pool_element":
		// FeaturePoolElement.getShuffledJigsawBlocks does NOT call Util.shuffle (0 RNG draws).
		front, top := parseJigsawOrientation("down_south", rotation)
		return append(dst, rotatedJigsawInfo{
			WorldPos:          pos,
			Front:             front,
			Top:               top,
			Name:              "minecraft:bottom",
			Target:            "minecraft:empty",
			Pool:              "minecraft:empty",
			FinalState:        "minecraft:air",
			Joint:             "rollable",
			SelectionPriority: 0,
			PlacementPriority: 0,
		})
	case "minecraft:list_pool_element":
		if len(elem.Elements) == 0 {
			return dst
		}
		return appendShuffledJigsawBlocks(dst, elem.Elements[0], pos, rotation, random)
	case "minecraft:single_pool_element", "minecraft:legacy_single_pool_element":
		tmpl, err := loadCachedJigsawTemplate(elem.Location)
		if err != nil || tmpl == nil {
			return dst
		}
		pal := selectJigsawPalette(tmpl, pos)
		if pal == nil || len(pal.jigsaws) == 0 {
			return dst
		}
		if cap(dst) < len(pal.jigsaws) {
			dst = make([]rotatedJigsawInfo, len(pal.jigsaws))
		} else {
			dst = dst[:len(pal.jigsaws)]
		}
		hasDiffPrio := false
		firstPrio := pal.jigsaws[0].Jigsaw.SelectionPriority
		for i, b := range pal.jigsaws {
			rel := worldgen.TransformBlockPos(b.Pos, "none", rotation, [3]int{0, 0, 0})
			front, top := parseJigsawOrientation(b.Jigsaw.Orientation, rotation)
			if b.Jigsaw.SelectionPriority != firstPrio {
				hasDiffPrio = true
			}
			dst[i] = rotatedJigsawInfo{
				WorldPos:          [3]int{pos[0] + rel[0], pos[1] + rel[1], pos[2] + rel[2]},
				Front:             front,
				Top:               top,
				Name:              b.Jigsaw.Name,
				Target:            b.Jigsaw.Target,
				Pool:              b.Jigsaw.Pool,
				FinalState:        b.Jigsaw.FinalState,
				Joint:             b.Jigsaw.Joint,
				SelectionPriority: b.Jigsaw.SelectionPriority,
				PlacementPriority: b.Jigsaw.PlacementPriority,
			}
		}
		jigsawShuffleSlice(dst, random)
		if hasDiffPrio {
			sort.SliceStable(dst, func(i, j int) bool {
				return dst[i].SelectionPriority > dst[j].SelectionPriority
			})
		}
		return dst
	default:
		return dst
	}
}

func poolMaxSize(pool *worldgen.TemplatePoolDef) int {
	if pool == nil {
		return 0
	}
	poolMaxSizeMu.RLock()
	if v, ok := poolMaxSizeCache[pool]; ok {
		poolMaxSizeMu.RUnlock()
		return v
	}
	poolMaxSizeMu.RUnlock()

	maxY := 0
	for _, we := range pool.Elements {
		if we.Element.ElementType == "minecraft:empty_pool_element" {
			continue
		}
		bb := getElementBoundingBox(we.Element, [3]int{0, 0, 0}, 0)
		if span := bb.ySpan(); span > maxY {
			maxY = span
		}
	}
	poolMaxSizeMu.Lock()
	if len(poolMaxSizeCache) >= 1024 {
		clear(poolMaxSizeCache)
	}
	poolMaxSizeCache[pool] = maxY
	poolMaxSizeMu.Unlock()
	return maxY
}

// jigsawFreeSpace models VoxelShape collision space (container minus placed piece boxes).
type jigsawFreeSpace struct {
	container  msBox
	subtracted []msBox
}

func (f *jigsawFreeSpace) collides(cand msBox) bool {
	if cand.minX < f.container.minX || cand.maxX > f.container.maxX ||
		cand.minY < f.container.minY || cand.maxY > f.container.maxY ||
		cand.minZ < f.container.minZ || cand.maxZ > f.container.maxZ {
		return true
	}
	for _, sub := range f.subtracted {
		if msBoxesIntersect(cand, sub) {
			return true
		}
	}
	return false
}

func (f *jigsawFreeSpace) subtract(cand msBox) {
	f.subtracted = append(f.subtracted, cand)
}

type jigsawPieceState struct {
	piece *JigsawPiece
	free  *jigsawFreeSpace
	depth int
}

type sequencedPriorityQueue struct {
	queues map[int][]jigsawPieceState
}

func newSequencedPriorityQueue() *sequencedPriorityQueue {
	return &sequencedPriorityQueue{queues: make(map[int][]jigsawPieceState)}
}

func (q *sequencedPriorityQueue) add(state jigsawPieceState, prio int) {
	q.queues[prio] = append(q.queues[prio], state)
}

func (q *sequencedPriorityQueue) hasNext() bool {
	for _, list := range q.queues {
		if len(list) > 0 {
			return true
		}
	}
	return false
}

func (q *sequencedPriorityQueue) next() jigsawPieceState {
	bestPrio := 0
	first := true
	for prio, list := range q.queues {
		if len(list) == 0 {
			continue
		}
		if first || prio > bestPrio {
			bestPrio = prio
			first = false
		}
	}
	list := q.queues[bestPrio]
	item := list[0]
	q.queues[bestPrio] = list[1:]
	return item
}

func jigsawCanAttach(parent, child rotatedJigsawInfo) bool {
	if parent.Front != jigsawOppositeDir(child.Front) {
		return false
	}
	if parent.Joint != "rollable" && parent.Top != child.Top {
		return false
	}
	return parent.Target == child.Name
}

type rawPoolAliasBinding struct {
	Type    string `json:"type"`
	Alias   string `json:"alias"`
	Target  string `json:"target"`
	Targets []struct {
		Data   string `json:"data"`
		Weight int    `json:"weight"`
	} `json:"targets"`
	Groups []struct {
		Data   []rawPoolAliasBinding `json:"data"`
		Weight int                   `json:"weight"`
	} `json:"groups"`
}

func applyPoolAliasBinding(b rawPoolAliasBinding, rng worldgen.RandomSource, out map[string]string) {
	switch normalizeResourceID(b.Type) {
	case "minecraft:direct":
		if b.Alias != "" && b.Target != "" {
			out[normalizeResourceID(b.Alias)] = normalizeResourceID(b.Target)
		}
	case "minecraft:random":
		if b.Alias == "" || len(b.Targets) == 0 {
			return
		}
		total := 0
		for _, t := range b.Targets {
			if t.Weight > 0 {
				total += t.Weight
			}
		}
		if total <= 0 {
			return
		}
		pick := int(rng.NextIntN(int32(total)))
		for _, t := range b.Targets {
			if t.Weight <= 0 {
				continue
			}
			if pick < t.Weight {
				out[normalizeResourceID(b.Alias)] = normalizeResourceID(t.Data)
				return
			}
			pick -= t.Weight
		}
	case "minecraft:random_group":
		if len(b.Groups) == 0 {
			return
		}
		total := 0
		for _, g := range b.Groups {
			if g.Weight > 0 {
				total += g.Weight
			}
		}
		if total <= 0 {
			return
		}
		pick := int(rng.NextIntN(int32(total)))
		for _, g := range b.Groups {
			if g.Weight <= 0 {
				continue
			}
			if pick < g.Weight {
				for _, sub := range g.Data {
					applyPoolAliasBinding(sub, rng, out)
				}
				return
			}
			pick -= g.Weight
		}
	}
}

// resolvePoolAliases ports PoolAliasLookup.create(List<PoolAliasBinding>, BlockPos, long).
func resolvePoolAliases(raw json.RawMessage, startPos [3]int, seed int64) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var bindings []rawPoolAliasBinding
	if err := json.Unmarshal(raw, &bindings); err != nil || len(bindings) == 0 {
		return nil
	}
	factorySeed := worldgen.NewLegacy(seed).NextLong()
	aliasRng := worldgen.NewLegacy(worldgen.MthGetSeed(startPos[0], startPos[1], startPos[2]) ^ factorySeed)
	out := make(map[string]string, len(bindings)*2)
	for _, b := range bindings {
		applyPoolAliasBinding(b, aliasRng, out)
	}
	return out
}

func lookupPoolAlias(aliases map[string]string, pool string) string {
	norm := normalizeResourceID(pool)
	if len(aliases) > 0 {
		if target, ok := aliases[norm]; ok {
			return target
		}
	}
	return norm
}

// BuildJigsawStart ports JigsawStructure.findGenerationPoint and JigsawPlacement.addPieces.
// Returns (nil, nil) if the start pool is empty, dimension padding fails, or biome check fails.
func BuildJigsawStart(
	od *worldgen.OverworldDensity,
	sets *worldgen.StructureSets,
	def *worldgen.StructureDef,
	seed int64,
	chunkX, chunkZ int32,
	surfaceFirstFreeAt func(x, z int, oceanFloor bool) int,
) (*JigsawStart, error) {
	if def == nil {
		return nil, nil
	}
	reg, err := worldgen.LoadJigsawDataRegistry()
	if err != nil {
		return nil, err
	}
	if surfaceFirstFreeAt == nil {
		surfaceFirstFreeAt = func(x, z int, oceanFloor bool) int {
			return preCarveFirstOccupiedHeight(od, seed, x, z, oceanFloor) + 1
		}
	}

	random := worldgen.NewLegacy(0)
	random.SetLargeFeatureSeed(seed, int(chunkX), int(chunkZ))

	startY := def.SampleStartHeight(random)
	startPos := [3]int{int(chunkX) * 16, startY, int(chunkZ) * 16}
	poolAliases := resolvePoolAliases(def.PoolAliases, startPos, seed)

	// RNG draw 1: Rotation.getRandom(random)
	rotation := int(random.NextIntN(4))

	startPool := reg.GetPool(lookupPoolAlias(poolAliases, def.StartPool))
	if startPool == nil || len(startPool.Expanded) == 0 {
		return nil, nil
	}

	// RNG draw 2: startPool.getRandomTemplate(random)
	startElem := startPool.Expanded[int(random.NextIntN(int32(len(startPool.Expanded))))]
	if startElem.ElementType == "minecraft:empty_pool_element" {
		return nil, nil
	}

	anchorPos := startPos
	if def.StartJigsawName != "" {
		wantName := normalizeResourceID(def.StartJigsawName)
		jigsaws := getShuffledJigsawBlocks(startElem, startPos, rotation, random)
		found := false
		for _, j := range jigsaws {
			if j.Name == wantName {
				anchorPos = j.WorldPos
				found = true
				break
			}
		}
		if !found {
			return nil, nil
		}
	}

	offsetToAnchor := [3]int{anchorPos[0] - startPos[0], anchorPos[1] - startPos[1], anchorPos[2] - startPos[2]}
	piecePos := [3]int{startPos[0] - offsetToAnchor[0], startPos[1] - offsetToAnchor[1], startPos[2] - offsetToAnchor[2]}
	bb := getElementBoundingBox(startElem, piecePos, rotation)

	centerX := (bb.maxX + bb.minX) / 2
	centerZ := (bb.maxZ + bb.minZ) / 2
	targetY := piecePos[1]
	if def.ProjectStartToHeightmap != "" {
		oceanFloor := strings.Contains(def.ProjectStartToHeightmap, "OCEAN_FLOOR")
		targetY = startPos[1] + surfaceFirstFreeAt(centerX, centerZ, oceanFloor)
	}
	oldGroundY := bb.minY + 1
	dy := targetY - oldGroundY
	bb.minY += dy
	bb.maxY += dy
	piecePos[1] += dy

	dimMinY := MinY
	dimHeight := WorldHeight
	if od != nil && od.Height > 0 {
		dimMinY = od.MinY
		dimHeight = od.Height
	}

	if def.DimensionPadding > 0 {
		if bb.minY < dimMinY+def.DimensionPadding || bb.maxY > dimMinY+dimHeight-1-def.DimensionPadding {
			return nil, nil
		}
	}

	stubY := targetY + offsetToAnchor[1]
	stubPos := [3]int{centerX, stubY, centerZ}

	// Structure.findValidGenerationPoint filters the stub through isValidBiome
	// before invoking the pieces builder callback.
	if od != nil && sets != nil && !isStructureBiomeAllowed(od, sets, def, stubPos[0], stubPos[1], stubPos[2]) {
		return nil, nil
	}

	liquidSettings := def.LiquidSettings
	if liquidSettings == "" {
		liquidSettings = "apply_waterlogging"
	}
	if startElem.OverrideLiquidSettings != "" {
		liquidSettings = startElem.OverrideLiquidSettings
	}

	startPiece := &JigsawPiece{
		Element:          startElem,
		Pos:              piecePos,
		GroundLevelDelta: 1,
		Rotation:         rotation,
		Box:              bb,
		LiquidSettings:   liquidSettings,
	}
	pieces := []*JigsawPiece{startPiece}

	maxDepth := def.Size
	if maxDepth > 0 {
		maxDist := def.MaxDistanceFromCenter
		if maxDist <= 0 {
			maxDist = 80
		}
		minOuterY := stubY - maxDist
		if minOuterY < dimMinY+def.DimensionPadding {
			minOuterY = dimMinY + def.DimensionPadding
		}
		maxOuterY := stubY + maxDist
		if maxOuterY > dimMinY+dimHeight-1-def.DimensionPadding {
			maxOuterY = dimMinY + dimHeight - 1 - def.DimensionPadding
		}
		outerBox := msBox{
			minX: centerX - maxDist,
			minY: minOuterY,
			minZ: centerZ - maxDist,
			maxX: centerX + maxDist,
			maxY: maxOuterY,
			maxZ: centerZ + maxDist,
		}
		free := &jigsawFreeSpace{
			container:  outerBox,
			subtracted: []msBox{bb},
		}

		placing := newSequencedPriorityQueue()
		var parentJigsawsBuf []rotatedJigsawInfo
		var childJigsawsBuf []rotatedJigsawInfo
		var candidatesBuf []worldgen.PoolElementDef
		tryPlacingChildren := func(piece *JigsawPiece, freeSpace *jigsawFreeSpace, depth int) {
			parentElem := piece.Element
			parentPos := piece.Pos
			parentRot := piece.Rotation
			parentRigid := parentElem.Projection == "rigid"
			var pieceFreeSpace *jigsawFreeSpace
			parentBB := piece.Box
			parentMinY := parentBB.minY

			parentJigsawsBuf = appendShuffledJigsawBlocks(parentJigsawsBuf, parentElem, parentPos, parentRot, random)
			parentJigsaws := parentJigsawsBuf
			for _, parentJigsaw := range parentJigsaws {
				step := jigsawDirStep(parentJigsaw.Front)
				jigsawPos := parentJigsaw.WorldPos
				targetPos := [3]int{jigsawPos[0] + step[0], jigsawPos[1] + step[1], jigsawPos[2] + step[2]}
				jigsawLocalY := jigsawPos[1] - parentMinY
				cachedSurfaceY := -1 << 30

				pool := reg.GetPool(lookupPoolAlias(poolAliases, parentJigsaw.Pool))
				if pool == nil || (len(pool.Expanded) == 0 && pool.Name != "minecraft:empty") {
					continue
				}
				fallback := reg.GetPool(pool.Fallback)
				if fallback == nil || (len(fallback.Expanded) == 0 && fallback.Name != "minecraft:empty") {
					continue
				}

				insideParent := targetPos[0] >= parentBB.minX && targetPos[0] <= parentBB.maxX &&
					targetPos[1] >= parentBB.minY && targetPos[1] <= parentBB.maxY &&
					targetPos[2] >= parentBB.minZ && targetPos[2] <= parentBB.maxZ
				var collisionShape *jigsawFreeSpace
				if insideParent {
					if pieceFreeSpace == nil {
						pieceFreeSpace = &jigsawFreeSpace{container: parentBB}
					}
					collisionShape = pieceFreeSpace
				} else {
					collisionShape = freeSpace
				}

				candidates := candidatesBuf[:0]
				if depth != maxDepth {
					startIdx := len(candidates)
					candidates = append(candidates, pool.Expanded...)
					jigsawShuffleSlice(candidates[startIdx:], random)
				}
				fbStart := len(candidates)
				candidates = append(candidates, fallback.Expanded...)
				jigsawShuffleSlice(candidates[fbStart:], random)
				candidatesBuf = candidates

				placementPrio := parentJigsaw.PlacementPriority

			candidateLoop:
				for _, candidate := range candidates {
					if candidate.ElementType == "minecraft:empty_pool_element" {
						break
					}
					rotsArr := [4]int{0, 1, 2, 3}
					rots := rotsArr[:]
					jigsawShuffleSlice(rots, random)
					for _, candidateRot := range rots {
						childJigsawsBuf = appendShuffledJigsawBlocks(childJigsawsBuf, candidate, [3]int{0, 0, 0}, candidateRot, random)
						childJigsaws := childJigsawsBuf
						zeroBB := getElementBoundingBox(candidate, [3]int{0, 0, 0}, candidateRot)
						expandY := 0
						if def.UseExpansionHack && zeroBB.ySpan() <= 16 {
							for _, cj := range childJigsaws {
								cStep := jigsawDirStep(cj.Front)
								relNext := [3]int{cj.WorldPos[0] + cStep[0], cj.WorldPos[1] + cStep[1], cj.WorldPos[2] + cStep[2]}
								if relNext[0] < zeroBB.minX || relNext[0] > zeroBB.maxX ||
									relNext[1] < zeroBB.minY || relNext[1] > zeroBB.maxY ||
									relNext[2] < zeroBB.minZ || relNext[2] > zeroBB.maxZ {
									continue
								}
								cp := reg.GetPool(lookupPoolAlias(poolAliases, cj.Pool))
								var cfb *worldgen.TemplatePoolDef
								if cp != nil {
									cfb = reg.GetPool(cp.Fallback)
								}
								m1 := poolMaxSize(cp)
								m2 := poolMaxSize(cfb)
								if m2 > m1 {
									m1 = m2
								}
								if m1 > expandY {
									expandY = m1
								}
							}
						}

						for _, childJigsaw := range childJigsaws {
							if !jigsawCanAttach(parentJigsaw, childJigsaw) {
								continue
							}
							childOffset := childJigsaw.WorldPos
							rawPos := [3]int{
								targetPos[0] - childOffset[0],
								targetPos[1] - childOffset[1],
								targetPos[2] - childOffset[2],
							}
							rawBB := getElementBoundingBox(candidate, rawPos, candidateRot)
							rawMinY := rawBB.minY
							childRigid := candidate.Projection == "rigid"
							childJigsawY := childOffset[1]
							deltaY := jigsawLocalY - childJigsawY + step[1]

							var placedMinY int
							if parentRigid && childRigid {
								placedMinY = parentMinY + deltaY
							} else {
								if cachedSurfaceY == -1<<30 {
									cachedSurfaceY = surfaceFirstFreeAt(jigsawPos[0], jigsawPos[2], false)
								}
								placedMinY = cachedSurfaceY - childJigsawY
							}
							moveY := placedMinY - rawMinY
							movedBB := rawBB
							movedBB.minY += moveY
							movedBB.maxY += moveY
							movedPos := [3]int{rawPos[0], rawPos[1] + moveY, rawPos[2]}

							if expandY > 0 {
								span := expandY + 1
								if cur := movedBB.maxY - movedBB.minY; cur > span {
									span = cur
								}
								movedBB.maxY = movedBB.minY + span
							}

							if collisionShape.collides(movedBB) {
								continue
							}
							collisionShape.subtract(movedBB)

							parentGroundDelta := piece.GroundLevelDelta
							childGroundDelta := 1
							if childRigid {
								childGroundDelta = parentGroundDelta - deltaY
							}

							childLiq := def.LiquidSettings
							if childLiq == "" {
								childLiq = "apply_waterlogging"
							}
							if candidate.OverrideLiquidSettings != "" {
								childLiq = candidate.OverrideLiquidSettings
							}

							childPiece := &JigsawPiece{
								Element:          candidate,
								Pos:              movedPos,
								GroundLevelDelta: childGroundDelta,
								Rotation:         candidateRot,
								Box:              movedBB,
								LiquidSettings:   childLiq,
							}

							var junctionY int
							if parentRigid {
								junctionY = parentMinY + jigsawLocalY
							} else if childRigid {
								junctionY = placedMinY + childJigsawY
							} else {
								if cachedSurfaceY == -1<<30 {
									cachedSurfaceY = surfaceFirstFreeAt(jigsawPos[0], jigsawPos[2], false)
								}
								junctionY = cachedSurfaceY + deltaY/2
							}

							piece.Junctions = append(piece.Junctions, JigsawJunction{
								SourceX:       targetPos[0],
								SourceGroundY: junctionY - jigsawLocalY + parentGroundDelta,
								SourceZ:       targetPos[2],
								DeltaY:        deltaY,
								DestProj:      candidate.Projection,
							})
							childPiece.Junctions = append(childPiece.Junctions, JigsawJunction{
								SourceX:       jigsawPos[0],
								SourceGroundY: junctionY - childJigsawY + childGroundDelta,
								SourceZ:       jigsawPos[2],
								DeltaY:        -deltaY,
								DestProj:      parentElem.Projection,
							})

							pieces = append(pieces, childPiece)
							if depth+1 <= maxDepth {
								placing.add(jigsawPieceState{
									piece: childPiece,
									free:  collisionShape,
									depth: depth + 1,
								}, placementPrio)
							}
							break candidateLoop
						}
					}
				}
			}
		}

		tryPlacingChildren(startPiece, free, 0)
		for placing.hasNext() {
			st := placing.next()
			tryPlacingChildren(st.piece, st.free, st.depth)
		}
	}

	totalBox := pieces[0].Box
	for _, p := range pieces[1:] {
		totalBox = msEncapsulate(totalBox, p.Box)
	}

	return &JigsawStart{
		StructureName:     def.Name,
		Step:              def.Step,
		TerrainAdaptation: def.TerrainAdaptation,
		ChunkX:            chunkX,
		ChunkZ:            chunkZ,
		StubPos:           stubPos,
		Box:               totalBox,
		Pieces:            pieces,
	}, nil
}

type jigsawCacheKey struct {
	setID string
	seed  int64
	sx    int32
	sz    int32
}

var (
	jigsawStartMu    sync.RWMutex
	jigsawStartCache = map[jigsawCacheKey]*JigsawStart{}
	jigsawInflight   = map[jigsawCacheKey]chan struct{}{}
)

func getCachedJigsawStart(seed int64, setID string, sx, sz int32) (*JigsawStart, bool) {
	jigsawStartMu.RLock()
	cached, ok := jigsawStartCache[jigsawCacheKey{setID: setID, seed: seed, sx: sx, sz: sz}]
	jigsawStartMu.RUnlock()
	return cached, ok
}

func putCachedJigsawStart(seed int64, setID string, sx, sz int32, start *JigsawStart) {
	jigsawStartMu.Lock()
	if len(jigsawStartCache) >= maxJigsawStartCacheEntries {
		clear(jigsawStartCache)
	}
	jigsawStartCache[jigsawCacheKey{setID: setID, seed: seed, sx: sx, sz: sz}] = start
	jigsawStartMu.Unlock()
}

// JigsawSetGenerationPoint checks if (sx, sz) is a valid start chunk for the
// given Jigsaw structure set (e.g. "minecraft:villages", "minecraft:pillager_outposts",
// "minecraft:ancient_cities", "minecraft:trail_ruins", "minecraft:trial_chambers")
// and returns its assembled JigsawStart.
func JigsawSetGenerationPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, setID string, seed int64, sx, sz int32) (*JigsawStart, error) {
	if sets == nil {
		return nil, nil
	}
	setID = normalizeResourceID(setID)
	set := sets.Sets[setID]
	if set == nil || !set.IsStartChunk(seed, sx, sz) {
		return nil, nil
	}
	if od != nil {
		key := jigsawCacheKey{setID: setID, seed: seed, sx: sx, sz: sz}
		for {
			jigsawStartMu.RLock()
			cached, ok := jigsawStartCache[key]
			wait := jigsawInflight[key]
			jigsawStartMu.RUnlock()
			if ok {
				return cached, nil
			}
			if wait != nil {
				<-wait
				continue
			}
			jigsawStartMu.Lock()
			if cached, ok := jigsawStartCache[key]; ok {
				jigsawStartMu.Unlock()
				return cached, nil
			}
			if wait := jigsawInflight[key]; wait != nil {
				jigsawStartMu.Unlock()
				<-wait
				continue
			}
			inflight := make(chan struct{})
			jigsawInflight[key] = inflight
			jigsawStartMu.Unlock()
			defer func() {
				jigsawStartMu.Lock()
				delete(jigsawInflight, key)
				close(inflight)
				jigsawStartMu.Unlock()
			}()
			break
		}
	}
	stub, ok := FindStructureSetStart(od, sets, setID, seed, sx, sz)
	if !ok || stub == nil {
		if od != nil {
			putCachedJigsawStart(seed, setID, sx, sz, nil)
		}
		return nil, nil
	}
	def := sets.Structures[stub.StructureName]
	if def == nil {
		return nil, nil
	}
	start, err := BuildJigsawStart(od, sets, def, seed, sx, sz, nil)
	if err == nil && od != nil {
		putCachedJigsawStart(seed, setID, sx, sz, start)
	}
	return start, err
}

// VillageGenerationPoint checks if (sx, sz) is a valid Village start chunk and
// returns its assembled JigsawStart.
func VillageGenerationPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, sx, sz int32) (*JigsawStart, error) {
	return JigsawSetGenerationPoint(od, sets, "minecraft:villages", seed, sx, sz)
}

func parseFinalStateString(finalState string) (uint16, string, bool) {
	if finalState == "" {
		return StateAir, "minecraft:air", true
	}
	name := finalState
	var props map[string]string
	if br := strings.IndexByte(finalState, '['); br >= 0 && strings.HasSuffix(finalState, "]") {
		name = finalState[:br]
		propStr := finalState[br+1 : len(finalState)-1]
		if propStr != "" {
			props = make(map[string]string)
			for _, pair := range strings.Split(propStr, ",") {
				kv := strings.SplitN(pair, "=", 2)
				if len(kv) == 2 {
					props[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
				}
			}
		}
	}
	if !strings.HasPrefix(name, "minecraft:") {
		name = "minecraft:" + name
	}
	if name == "minecraft:structure_void" {
		return 0, name, false
	}
	id, ok := nameToStateID(name, props)
	if !ok {
		id, ok = nameToStateID(name, nil)
	}
	return id, name, ok
}

func testRulePredicate(pred worldgen.RulePredicateDef, stateID uint16, rng worldgen.RandomSource, fs *worldgen.FeatureSet) bool {
	switch pred.PredicateType {
	case "", "minecraft:always_true":
		return true
	case "minecraft:block_match":
		st, ok := stateByID(stateID)
		if !ok {
			return false
		}
		want := normalizeResourceID(pred.Block)
		return st.Name == want
	case "minecraft:blockstate_match":
		wantID, ok := nameToStateID(pred.BlockState.Name, pred.BlockState.Properties)
		if !ok {
			return false
		}
		return stateID == wantID
	case "minecraft:random_block_match":
		st, ok := stateByID(stateID)
		if !ok {
			return false
		}
		want := normalizeResourceID(pred.Block)
		return st.Name == want && rng.NextFloat() < pred.Probability
	case "minecraft:tag_match":
		if fs == nil {
			return false
		}
		st, ok := stateByID(stateID)
		if !ok {
			return false
		}
		tag := strings.TrimPrefix(pred.Tag, "#")
		tag = normalizeResourceID(tag)
		return blockTagContains(fs, tag, st.Name)
	default:
		return false
	}
}

func testPosPredicate(pred worldgen.PosPredicateDef, localPos, worldPos, pivot [3]int, rng worldgen.RandomSource) bool {
	switch pred.PredicateType {
	case "", "minecraft:always_true":
		return true
	case "minecraft:axis_aligned_linear_pos":
		var dist int
		switch pred.Axis {
		case "x":
			dist = absInt(localPos[0] - pivot[0])
		case "z":
			dist = absInt(localPos[2] - pivot[2])
		default: // "y"
			dist = absInt(localPos[1] - pivot[1])
		}
		t := float32(0)
		if pred.MaxDist > pred.MinDist {
			t = (float32(dist) - float32(pred.MinDist)) / float32(pred.MaxDist-pred.MinDist)
		}
		chance := pred.MinChance + t*(pred.MaxChance-pred.MinChance)
		return rng.NextFloat() <= chance
	default:
		return true
	}
}

// rotateTemplateState applies StructureTemplate's Rotation (0=NONE, 1=CW_90, 2=CW_180, 3=CCW_90)
// to a block state ID.
func rotateTemplateState(state uint16, rotation int) uint16 {
	rot := rotation & 3
	if rot == 0 {
		return state
	}
	entry, ok := stateByID(state)
	if !ok || len(entry.Properties) == 0 {
		return state
	}
	props := make(map[string]string, len(entry.Properties))
	for k, v := range entry.Properties {
		props[k] = v
	}
	changed := false
	if f, has := props["facing"]; has {
		nf := rotateJigsawDirection(f, rot)
		if nf != f {
			props["facing"] = nf
			changed = true
		}
	}
	if ax, has := props["axis"]; has && (rot == 1 || rot == 3) {
		switch ax {
		case "x":
			props["axis"] = "z"
			changed = true
		case "z":
			props["axis"] = "x"
			changed = true
		}
	}
	_, hasN := props["north"]
	_, hasE := props["east"]
	_, hasS := props["south"]
	_, hasW := props["west"]
	if hasN && hasE && hasS && hasW {
		n, e, s, w := props["north"], props["east"], props["south"], props["west"]
		switch rot {
		case 1:
			props["east"], props["south"], props["west"], props["north"] = n, e, s, w
		case 2:
			props["south"], props["west"], props["north"], props["east"] = n, e, s, w
		case 3:
			props["west"], props["north"], props["east"], props["south"] = n, e, s, w
		}
		changed = true
	}
	if !changed {
		return state
	}
	if id, ok := nameToStateID(entry.Name, props); ok {
		return id
	}
	return state
}

func (r *decorationRegion) placeBlockPileFeature(random worldgen.RandomSource, origin [3]int, featureName string) {
	if origin[1] < MinY+5 {
		return
	}
	rx := 2 + int(random.NextIntN(2))
	rz := 2 + int(random.NextIntN(2))

	clean := strings.TrimPrefix(featureName, "minecraft:")
	samplePileState := func() uint16 {
		switch clean {
		case "pile_hay":
			axes := [3]string{"x", "y", "z"}
			ax := axes[int(random.NextIntN(3))]
			id, _ := nameToStateID("minecraft:hay_block", map[string]string{"axis": ax})
			return id
		case "pile_melon":
			id, _ := nameToStateID("minecraft:melon", nil)
			return id
		case "pile_snow":
			id, _ := nameToStateID("minecraft:snow_block", nil)
			return id
		case "pile_ice":
			// weight 5 ice, weight 1 blue_ice
			if random.NextIntN(6) == 5 {
				id, _ := nameToStateID("minecraft:blue_ice", nil)
				return id
			}
			id, _ := nameToStateID("minecraft:ice", nil)
			return id
		case "pile_pumpkin":
			// weight 19 pumpkin, weight 1 jack_o_lantern
			if random.NextIntN(20) == 19 {
				id, _ := nameToStateID("minecraft:jack_o_lantern", map[string]string{"facing": "north"})
				return id
			}
			id, _ := nameToStateID("minecraft:pumpkin", nil)
			return id
		default:
			id, _ := nameToStateID("minecraft:hay_block", map[string]string{"axis": "y"})
			return id
		}
	}

	// BlockPos.betweenClosed iterates Z outer, Y middle, X inner.
	for z := origin[2] - rz; z <= origin[2]+rz; z++ {
		for y := origin[1]; y <= origin[1]+1; y++ {
			for x := origin[0] - rx; x <= origin[0]+rx; x++ {
				dx := origin[0] - x
				dz := origin[2] - z
				distSq := float32(dx*dx + dz*dz)
				thresh := random.NextFloat()*10.0 - random.NextFloat()*6.0
				shouldTry := false
				if distSq <= thresh {
					shouldTry = true
				} else if float64(random.NextFloat()) < 0.031 {
					shouldTry = true
				}
				if !shouldTry {
					continue
				}
				if !isAirState(r.getBlock(x, y, z)) {
					continue
				}
				below := r.getBlock(x, y-1, z)
				mayPlace := false
				if st, ok := stateByID(below); ok && st.Name == "minecraft:dirt_path" {
					mayPlace = random.NextBoolean()
				} else {
					mayPlace = isFaceSturdy(below)
				}
				if mayPlace {
					state := samplePileState()
					r.setBlockGlobal(x, y, z, state)
				}
			}
		}
	}
}

var blackstoneReplacements = map[string]string{
	"minecraft:cobblestone":              "minecraft:blackstone",
	"minecraft:mossy_cobblestone":        "minecraft:blackstone",
	"minecraft:stone":                    "minecraft:polished_blackstone",
	"minecraft:stone_bricks":             "minecraft:polished_blackstone_bricks",
	"minecraft:mossy_stone_bricks":       "minecraft:polished_blackstone_bricks",
	"minecraft:cobblestone_stairs":       "minecraft:blackstone_stairs",
	"minecraft:mossy_cobblestone_stairs": "minecraft:blackstone_stairs",
	"minecraft:stone_stairs":             "minecraft:polished_blackstone_stairs",
	"minecraft:stone_brick_stairs":       "minecraft:polished_blackstone_brick_stairs",
	"minecraft:mossy_stone_brick_stairs": "minecraft:polished_blackstone_brick_stairs",
	"minecraft:cobblestone_slab":         "minecraft:blackstone_slab",
	"minecraft:mossy_cobblestone_slab":   "minecraft:blackstone_slab",
	"minecraft:smooth_stone_slab":        "minecraft:polished_blackstone_slab",
	"minecraft:stone_slab":               "minecraft:polished_blackstone_slab",
	"minecraft:stone_brick_slab":         "minecraft:polished_blackstone_brick_slab",
	"minecraft:mossy_stone_brick_slab":   "minecraft:polished_blackstone_brick_slab",
	"minecraft:stone_brick_wall":         "minecraft:polished_blackstone_brick_wall",
	"minecraft:mossy_stone_brick_wall":   "minecraft:polished_blackstone_brick_wall",
	"minecraft:cobblestone_wall":         "minecraft:blackstone_wall",
	"minecraft:mossy_cobblestone_wall":   "minecraft:blackstone_wall",
	"minecraft:chiseled_stone_bricks":    "minecraft:chiseled_polished_blackstone",
	"minecraft:cracked_stone_bricks":     "minecraft:cracked_polished_blackstone_bricks",
	"minecraft:iron_bars":                "minecraft:chain",
}

func applyBlackstoneReplacement(curState uint16, curName string) (uint16, string) {
	replName, ok := blackstoneReplacements[normalizeResourceID(curName)]
	if !ok {
		return curState, curName
	}
	if st, ok := stateByID(curState); ok && len(st.Properties) > 0 {
		if id, ok := nameToStateID(replName, st.Properties); ok {
			return id, replName
		}
	}
	if id, ok := nameToStateID(replName, nil); ok {
		return id, replName
	}
	return curState, curName
}

func matchesRottableBlocks(blockName string, rottable []string, fs *worldgen.FeatureSet) bool {
	if len(rottable) == 0 {
		return true
	}
	want := normalizeResourceID(blockName)
	for _, entry := range rottable {
		if strings.HasPrefix(entry, "#") {
			if fs != nil {
				tag := normalizeResourceID(strings.TrimPrefix(entry, "#"))
				if blockTagContains(fs, tag, want) {
					return true
				}
			}
			continue
		}
		if normalizeResourceID(entry) == want {
			return true
		}
	}
	return false
}

func placeJigsawElementInChunk(
	region *decorationRegion,
	reg *worldgen.JigsawDataRegistry,
	fs *worldgen.FeatureSet,
	elem worldgen.PoolElementDef,
	pos [3]int,
	rotation int,
	chunkBox msBox,
	random worldgen.RandomSource,
	liquidSettings string,
) {
	switch elem.ElementType {
	case "minecraft:empty_pool_element":
		return
	case "minecraft:list_pool_element":
		for _, sub := range elem.Elements {
			placeJigsawElementInChunk(region, reg, fs, sub, pos, rotation, chunkBox, random, liquidSettings)
		}
		return
	case "minecraft:feature_pool_element":
		if pos[0] < chunkBox.minX || pos[0] > chunkBox.maxX ||
			pos[1] < chunkBox.minY || pos[1] > chunkBox.maxY ||
			pos[2] < chunkBox.minZ || pos[2] > chunkBox.maxZ {
			return
		}
		clean := strings.TrimPrefix(elem.Feature, "minecraft:")
		if strings.HasPrefix(clean, "pile_") {
			region.placeBlockPileFeature(random, pos, clean)
			return
		}
		if fs != nil {
			fpos := worldgen.FeaturePosition{X: pos[0], Y: pos[1], Z: pos[2]}
			if placed, ok := fs.Placed["minecraft:"+clean]; ok {
				_ = region.placeConfiguredVegetation(fs, random, fpos, placed.Feature, region.placementContext(nil))
			} else {
				_ = region.placeConfiguredVegetation(fs, random, fpos, "minecraft:"+clean, region.placementContext(nil))
			}
		}
		return
	case "minecraft:single_pool_element", "minecraft:legacy_single_pool_element":
		tmpl, err := loadCachedJigsawTemplate(elem.Location)
		if err != nil || tmpl == nil {
			return
		}
		pal := selectJigsawPalette(tmpl, pos)
		if pal == nil {
			return
		}
		isLegacy := elem.ElementType == "minecraft:legacy_single_pool_element"
		terrainMatching := elem.Projection == "terrain_matching"
		procs := reg.ResolveProcessors(elem)

		type processedBlock struct {
			orig          worldgen.TemplateBlockInfo
			worldPos      [3]int
			curState      uint16
			curName       string
			ruleLootTable string
			ruleLootSeed  int64
		}
		survivors := make([]processedBlock, 0, len(pal.blocks))

		for _, orig := range pal.blocks {
			rel := worldgen.TransformBlockPos(orig.Pos, "none", rotation, [3]int{0, 0, 0})
			worldPos := [3]int{pos[0] + rel[0], pos[1] + rel[1], pos[2] + rel[2]}
			// StructureTemplate.processBlockInfos filters by settings.getBoundingBox()
			// (chunkBox) before running processBlock and CappedProcessor.
			if worldPos[0] < chunkBox.minX || worldPos[0] > chunkBox.maxX ||
				worldPos[2] < chunkBox.minZ || worldPos[2] > chunkBox.maxZ {
				continue
			}
			if !terrainMatching && (worldPos[1] < chunkBox.minY || worldPos[1] > chunkBox.maxY) {
				continue
			}
			curState := orig.State
			curName := orig.BlockName

			// 1. SinglePoolElement runs BlockIgnoreProcessor.STRUCTURE_BLOCK first.
			if !isLegacy && curName == "minecraft:structure_block" {
				continue
			}

			// 2. JigsawReplacementProcessor.
			if curName == "minecraft:jigsaw" {
				if orig.Jigsaw == nil {
					continue
				}
				fID, fName, keep := parseFinalStateString(orig.Jigsaw.FinalState)
				if !keep {
					continue
				}
				curState = fID
				curName = fName
			}

			// 3. Configured processBlock processors (RuleProcessor, BlockRotProcessor, ProtectedBlocksProcessor, BlackstoneReplaceProcessor, BlockIgnoreProcessor).
			discarded := false
			var ruleLootTable string
			var ruleLootSeed int64
			for _, proc := range procs {
				switch proc.ProcessorType {
				case "minecraft:rule":
					ruleRng := worldgen.NewLegacy(worldgen.MthGetSeed(worldPos[0], worldPos[1], worldPos[2]))
					existing := region.getBlock(worldPos[0], worldPos[1], worldPos[2])
					for _, rule := range proc.Rules {
						if !testRulePredicate(rule.InputPredicate, curState, ruleRng, fs) {
							continue
						}
						if !testRulePredicate(rule.LocationPredicate, existing, ruleRng, fs) {
							continue
						}
						if !testPosPredicate(rule.PositionPredicate, orig.Pos, worldPos, [3]int{0, 0, 0}, ruleRng) {
							continue
						}
						if outID, ok := nameToStateID(rule.OutputState.Name, rule.OutputState.Properties); ok {
							curState = outID
							curName = rule.OutputState.Name
						}
						if rule.BlockEntityModifier.Type == "minecraft:append_loot" && rule.BlockEntityModifier.LootTable != "" {
							ruleLootTable = rule.BlockEntityModifier.LootTable
							ruleLootSeed = ruleRng.NextLong()
						}
						break
					}
				case "minecraft:block_rot":
					if matchesRottableBlocks(curName, proc.RottableBlocks, fs) && proc.Integrity < 1.0 {
						rotRng := worldgen.NewLegacy(worldgen.MthGetSeed(worldPos[0], worldPos[1], worldPos[2]))
						if proc.Integrity <= 0.0 || rotRng.NextFloat() > proc.Integrity {
							discarded = true
						}
					}
				case "minecraft:protected_blocks":
					if fs != nil && proc.ProtectedTag != "" {
						existing := region.getBlock(worldPos[0], worldPos[1], worldPos[2])
						if exSt, ok := stateByID(existing); ok {
							tag := normalizeResourceID(strings.TrimPrefix(proc.ProtectedTag, "#"))
							if blockTagContains(fs, tag, exSt.Name) {
								discarded = true
							}
						}
					}
				case "minecraft:blackstone_replace":
					curState, curName = applyBlackstoneReplacement(curState, curName)
				case "minecraft:block_ignore":
					want := normalizeResourceID(curName)
					for _, b := range proc.Blocks {
						if normalizeResourceID(b.Name) == want {
							discarded = true
							break
						}
					}
				}
				if discarded {
					break
				}
			}
			if discarded {
				continue
			}

			// 4. Projection processors: TERRAIN_MATCHING adds GravityProcessor(WORLD_SURFACE_WG, -1).
			if terrainMatching {
				surfY := region.heightAt("WORLD_SURFACE_WG", worldPos[0], worldPos[2])
				worldPos[1] = (surfY - 1) + orig.Pos[1]
				if worldPos[1] < chunkBox.minY || worldPos[1] > chunkBox.maxY {
					continue
				}
			}

			// 5. LegacySinglePoolElement runs BlockIgnoreProcessor.STRUCTURE_AND_AIR last.
			if isLegacy && (curName == "minecraft:structure_block" || curName == "minecraft:air") {
				continue
			}

			survivors = append(survivors, processedBlock{
				orig:          orig,
				worldPos:      worldPos,
				curState:      curState,
				curName:       curName,
				ruleLootTable: ruleLootTable,
				ruleLootSeed:  ruleLootSeed,
			})
		}

		// 6. CappedProcessor.finalizeProcessing runs on the survivor list after processBlock.
		for _, proc := range procs {
			if proc.ProcessorType != "minecraft:capped" || proc.Delegate == nil || proc.Delegate.ProcessorType != "minecraft:rule" {
				continue
			}
			if proc.Limit <= 0 || len(survivors) == 0 {
				continue
			}
			factorySeed := worldgen.NewLegacy(region.biomeWorldSeed).NextLong()
			shuffle := worldgen.NewLegacy(worldgen.MthGetSeed(pos[0], pos[1], pos[2]) ^ factorySeed)
			limit := min(proc.Limit, len(survivors))
			if limit < 1 {
				continue
			}
			order := make([]int, len(survivors))
			for i := range order {
				order[i] = i
			}
			for i := len(order); i > 1; i-- {
				j := int(shuffle.NextIntN(int32(i)))
				order[i-1], order[j] = order[j], order[i-1]
			}
			converted := 0
			for _, idx := range order {
				if converted >= limit {
					break
				}
				sb := &survivors[idx]
				ruleRng := worldgen.NewLegacy(worldgen.MthGetSeed(sb.worldPos[0], sb.worldPos[1], sb.worldPos[2]))
				existing := region.getBlock(sb.worldPos[0], sb.worldPos[1], sb.worldPos[2])
				for _, rule := range proc.Delegate.Rules {
					if !testRulePredicate(rule.InputPredicate, sb.curState, ruleRng, fs) {
						continue
					}
					if !testRulePredicate(rule.LocationPredicate, existing, ruleRng, fs) {
						continue
					}
					if !testPosPredicate(rule.PositionPredicate, sb.orig.Pos, sb.worldPos, [3]int{0, 0, 0}, ruleRng) {
						continue
					}
					changed := false
					if outID, ok := nameToStateID(rule.OutputState.Name, rule.OutputState.Properties); ok {
						if outID != sb.curState {
							changed = true
						}
						sb.curState = outID
						sb.curName = rule.OutputState.Name
					}
					if rule.BlockEntityModifier.Type == "minecraft:append_loot" && rule.BlockEntityModifier.LootTable != "" {
						sb.ruleLootTable = rule.BlockEntityModifier.LootTable
						sb.ruleLootSeed = ruleRng.NextLong()
						changed = true
					}
					if changed {
						converted++
					}
					break
				}
			}
		}

		for _, sb := range survivors {
			orig := sb.orig
			worldPos := sb.worldPos
			curState := sb.curState
			curName := sb.curName
			ruleLootTable := sb.ruleLootTable
			ruleLootSeed := sb.ruleLootSeed

			if curName == "minecraft:structure_void" {
				continue
			}

			stateToPlace := rotateTemplateState(curState, rotation)

			// RandomizableContainer LootTableSeed draw on chunk's decoration random.
			// In StructureTemplate.placeInWorld, blockInfo.nbt is only applied when
			// the placed block at worldPos still creates a matching BlockEntity.
			keepTemplateNBT := orig.NBT != nil && orig.BlockName != "minecraft:jigsaw" && curName == orig.BlockName
			var containerLootSeed int64
			if keepTemplateNBT && orig.IsRandomizableContainer && ruleLootTable == "" {
				containerLootSeed = random.NextLong()
			}

			// Waterlogging check if APPLY_WATERLOGGING.
			if liquidSettings == "apply_waterlogging" && isWaterState(region.getBlock(worldPos[0], worldPos[1], worldPos[2])) {
				if st, ok := stateByID(stateToPlace); ok {
					if _, hasWL := st.Properties["waterlogged"]; hasWL && st.Properties["waterlogged"] == "false" {
						wlProps := make(map[string]string, len(st.Properties))
						for k, v := range st.Properties {
							wlProps[k] = v
						}
						wlProps["waterlogged"] = "true"
						if wlID, ok := nameToStateID(st.Name, wlProps); ok {
							stateToPlace = wlID
						}
					}
				}
			}

			region.setBlockGlobal(worldPos[0], worldPos[1], worldPos[2], stateToPlace)

			if ruleLootTable != "" {
				if keepTemplateNBT {
					if be, ok := extractTemplateBlockEntity(worldPos[0], worldPos[1], worldPos[2], curName, orig.NBT, ruleLootSeed); ok {
						be.LootTable = ruleLootTable
						be.LootTableSeed = ruleLootSeed
						be.Items = nil
						region.addStructureBlockEntity(be)
						continue
					}
				}
				beID := "minecraft:brushable_block"
				if defBE, ok := defaultBlockEntityForBlock(curName, worldPos[0], worldPos[1], worldPos[2]); ok {
					beID = defBE.ID
				}
				region.addStructureBlockEntity(StructureBlockEntity{
					ID:            beID,
					X:             worldPos[0],
					Y:             worldPos[1],
					Z:             worldPos[2],
					LootTable:     ruleLootTable,
					LootTableSeed: ruleLootSeed,
				})
			} else if keepTemplateNBT {
				if be, ok := extractTemplateBlockEntity(worldPos[0], worldPos[1], worldPos[2], curName, orig.NBT, containerLootSeed); ok {
					region.addStructureBlockEntity(be)
				} else {
					region.removeStructureBlockEntity(worldPos[0], worldPos[1], worldPos[2])
				}
			} else if be, ok := defaultBlockEntityForBlock(curName, worldPos[0], worldPos[1], worldPos[2]); ok {
				region.addStructureBlockEntity(be)
			} else {
				region.removeStructureBlockEntity(worldPos[0], worldPos[1], worldPos[2])
			}
		}

		// Place template entities (e.g. piglin_brute, piglin, hoglin in Bastion; iron_golem, allay in Outpost; villagers/animals in Village).
		for _, ent := range tmpl.entities {
			relPos := worldgen.TransformEntityPos(ent.Pos, "none", rotation, [3]int{0, 0, 0})
			relBlock := worldgen.TransformBlockPos(ent.BlockPos, "none", rotation, [3]int{0, 0, 0})
			wPos := [3]float64{float64(pos[0]) + relPos[0], float64(pos[1]) + relPos[1], float64(pos[2]) + relPos[2]}
			wBlock := [3]int{pos[0] + relBlock[0], pos[1] + relBlock[1], pos[2] + relBlock[2]}
			if terrainMatching {
				surfY := region.heightAt("WORLD_SURFACE_WG", wBlock[0], wBlock[2])
				wBlock[1] = (surfY - 1) + ent.BlockPos[1]
				wPos[1] = float64(surfY-1) + ent.Pos[1]
			}
			if wBlock[0] < chunkBox.minX || wBlock[0] > chunkBox.maxX ||
				wBlock[1] < chunkBox.minY || wBlock[1] > chunkBox.maxY ||
				wBlock[2] < chunkBox.minZ || wBlock[2] > chunkBox.maxZ {
				continue
			}
			region.addStructureEntity(StructureEntity{
				ID:                  ent.ID,
				X:                   wPos[0],
				Y:                   wPos[1],
				Z:                   wPos[2],
				BlockX:              wBlock[0],
				BlockY:              wBlock[1],
				BlockZ:              wBlock[2],
				Rotation:            rotation,
				PersistenceRequired: true,
			})
		}
	}
}

// PlaceJigsawStartsForChunk places all Jigsaw structure starts intersecting
// (chunkX, chunkZ) that belong to the given structureName and generation step.
func PlaceJigsawStartsForChunk(region *decorationRegion, starts []*JigsawStart, structureName string, step int, seed int64, chunkX, chunkZ int32) {
	if len(starts) == 0 {
		return
	}
	region.bindSeed(seed)
	stateByIDOnce.Do(buildStateTable)
	reg, err := worldgen.LoadJigsawDataRegistry()
	if err != nil {
		return
	}
	fs, _ := worldgen.LoadFeatureSet()
	chunkBox := msChunkBox(chunkX, chunkZ)

	type startCandidate struct {
		start *JigsawStart
		key   int64
	}
	var candidates []startCandidate
	var set fastutilLongSet
	wantName := normalizeResourceID(structureName)
	for _, st := range starts {
		if normalizeResourceID(st.StructureName) != wantName {
			continue
		}
		if !msBoxesIntersect(st.Box, chunkBox) {
			continue
		}
		hasIntersecting := false
		for _, p := range st.Pieces {
			if msBoxesIntersect(p.Box, chunkBox) {
				hasIntersecting = true
				break
			}
		}
		if !hasIntersecting {
			continue
		}
		key := packChunkPos(st.ChunkX, st.ChunkZ)
		set.add(key)
		candidates = append(candidates, startCandidate{start: st, key: key})
	}
	if len(candidates) == 0 {
		return
	}

	random, decorationSeed := worldgen.DecorationRandom(seed, int(chunkX), int(chunkZ))
	random.SetFeatureSeed(decorationSeed, worldgen.StructureIndexInStep(wantName), step)

	for _, key := range set.elements() {
		for _, cand := range candidates {
			if cand.key == key {
				for _, piece := range cand.start.Pieces {
					if !msBoxesIntersect(piece.Box, chunkBox) {
						continue
					}
					placeJigsawElementInChunk(region, reg, fs, piece.Element, piece.Pos, piece.Rotation, chunkBox, random, piece.LiquidSettings)
				}
				break
			}
		}
	}
}

// BeardifierForJigsawStarts computes the Beardifier density contribution at
// (x, y, z) from all provided JigsawStarts overlapping chunk (chunkX, chunkZ),
// matching net.minecraft.world.level.levelgen.Beardifier.compute.
func BeardifierForJigsawStarts(starts []*JigsawStart, chunkX, chunkZ int32, x, y, z int) float64 {
	if len(starts) == 0 {
		return 0.0
	}
	chunkMinX := int(chunkX) * 16
	chunkMinZ := int(chunkZ) * 16
	closeBox := msBox{
		minX: chunkMinX - 12,
		minY: -1 << 20,
		minZ: chunkMinZ - 12,
		maxX: chunkMinX + 15 + 12,
		maxY: 1 << 20,
		maxZ: chunkMinZ + 15 + 12,
	}

	total := 0.0
	for _, st := range starts {
		adapt := st.TerrainAdaptation
		if adapt == "" || adapt == "none" {
			continue
		}
		for _, p := range st.Pieces {
			if !msBoxesIntersect(p.Box, closeBox) {
				continue
			}
			if p.Element.Projection == "rigid" {
				box := p.Box
				dx := max(0, max(box.minX-x, x-box.maxX))
				dz := max(0, max(box.minZ-z, z-box.maxZ))
				groundY := box.minY + p.GroundLevelDelta
				dyFromGround := y - groundY
				switch adapt {
				case "bury":
					total += worldgen.BuryContribution(float64(dx), float64(dyFromGround)/2.0, float64(dz))
				case "beard_thin":
					total += worldgen.BeardContribution(dx, dyFromGround, dz, dyFromGround) * 0.8
				case "beard_box":
					dy := max(0, max(groundY-y, y-box.maxY))
					total += worldgen.BeardContribution(dx, dy, dz, dyFromGround) * 0.8
				case "encapsulate":
					dy := max(0, max(box.minY-y, y-box.maxY))
					total += worldgen.BuryContribution(float64(dx)/2.0, float64(dy)/2.0, float64(dz)/2.0) * 0.8
				}
			}
			for _, j := range p.Junctions {
				if j.SourceX > chunkMinX-12 && j.SourceZ > chunkMinZ-12 &&
					j.SourceX < chunkMinX+15+12 && j.SourceZ < chunkMinZ+15+12 {
					jdx := x - j.SourceX
					jdy := y - j.SourceGroundY
					jdz := z - j.SourceZ
					total += worldgen.BeardContribution(jdx, jdy, jdz, jdy) * 0.4
				}
			}
		}
	}
	return total
}

type beardifierRigid struct {
	box              msBox
	groundLevelDelta int
	adaptation       string
}

type chunkBeardifier struct {
	affectedBox msBox
	rigids      []beardifierRigid
	junctions   []JigsawJunction
}

func buildChunkBeardifier(od *worldgen.OverworldDensity, seed int64, cx, cz int32) *chunkBeardifier {
	if od == nil {
		return nil
	}
	sets, err := worldgen.LoadStructureSets()
	if err != nil || sets == nil {
		return nil
	}
	chunkMinX := int(cx) * 16
	chunkMinZ := int(cz) * 16
	closeBox := msBox{
		minX: chunkMinX - 12,
		minY: -1 << 20,
		minZ: chunkMinZ - 12,
		maxX: chunkMinX + 15 + 12,
		maxY: 1 << 20,
		maxZ: chunkMinZ + 15 + 12,
	}

	var rigids []beardifierRigid
	var junctions []JigsawJunction
	var affected msBox
	hasAffected := false
	includeBox := func(b msBox) {
		if !hasAffected {
			affected = b
			hasAffected = true
		} else {
			affected = msEncapsulate(affected, b)
		}
	}

	for _, jSet := range []struct {
		setID  string
		radius int32
	}{
		{"minecraft:ancient_cities", 8},
		{"minecraft:trail_ruins", 6},
		{"minecraft:trial_chambers", 8},
		{"minecraft:pillager_outposts", 6},
		{"minecraft:villages", 6},
	} {
		set := sets.Sets[jSet.setID]
		if set == nil {
			continue
		}
		for sx := cx - jSet.radius; sx <= cx+jSet.radius; sx++ {
			for sz := cz - jSet.radius; sz <= cz+jSet.radius; sz++ {
				if !set.IsStartChunk(seed, sx, sz) {
					continue
				}
				st, err := JigsawSetGenerationPoint(od, sets, jSet.setID, seed, sx, sz)
				if err != nil || st == nil {
					continue
				}
				adapt := st.TerrainAdaptation
				if adapt == "" || adapt == "none" {
					continue
				}
				if !msBoxesIntersect(st.Box, closeBox) {
					continue
				}
				for _, p := range st.Pieces {
					if !msBoxesIntersect(p.Box, closeBox) {
						continue
					}
					if p.Element.Projection == "rigid" {
						rigids = append(rigids, beardifierRigid{
							box:              p.Box,
							groundLevelDelta: p.GroundLevelDelta,
							adaptation:       adapt,
						})
						includeBox(p.Box)
					}
					for _, j := range p.Junctions {
						if j.SourceX > chunkMinX-12 && j.SourceZ > chunkMinZ-12 &&
							j.SourceX < chunkMinX+15+12 && j.SourceZ < chunkMinZ+15+12 {
							junctions = append(junctions, j)
							includeBox(msBox{
								minX: j.SourceX, minY: j.SourceGroundY, minZ: j.SourceZ,
								maxX: j.SourceX, maxY: j.SourceGroundY, maxZ: j.SourceZ,
							})
						}
					}
				}
			}
		}
	}

	if int64(cx)*int64(cx)+int64(cz)*int64(cz) >= 60*60 {
		for sx := cx - 8; sx <= cx+8; sx++ {
			for sz := cz - 8; sz <= cz+8; sz++ {
				st, err := StrongholdGenerationPoint(od, sets, seed, sx, sz)
				if err != nil || st == nil {
					continue
				}
				if st.Box != (msBox{}) && !msBoxesIntersect(st.Box, closeBox) {
					continue
				}
				for _, p := range st.Pieces {
					if !msBoxesIntersect(p.box, closeBox) {
						continue
					}
					rigids = append(rigids, beardifierRigid{
						box:              p.box,
						groundLevelDelta: 0,
						adaptation:       "bury",
					})
					includeBox(p.box)
				}
			}
		}
	}

	if !hasAffected {
		return nil
	}
	affected.minX -= 24
	affected.minY -= 24
	affected.minZ -= 24
	affected.maxX += 24
	affected.maxY += 24
	affected.maxZ += 24
	return &chunkBeardifier{
		affectedBox: affected,
		rigids:      rigids,
		junctions:   junctions,
	}
}

func (b *chunkBeardifier) compute(x, y, z int) float64 {
	if b == nil {
		return 0.0
	}
	if x < b.affectedBox.minX || x > b.affectedBox.maxX ||
		y < b.affectedBox.minY || y > b.affectedBox.maxY ||
		z < b.affectedBox.minZ || z > b.affectedBox.maxZ {
		return 0.0
	}
	total := 0.0
	for _, r := range b.rigids {
		box := r.box
		dx := max(0, max(box.minX-x, x-box.maxX))
		dz := max(0, max(box.minZ-z, z-box.maxZ))
		groundY := box.minY + r.groundLevelDelta
		dyFromGround := y - groundY
		switch r.adaptation {
		case "bury":
			total += worldgen.BuryContribution(float64(dx), float64(dyFromGround)/2.0, float64(dz))
		case "beard_thin":
			total += worldgen.BeardContribution(dx, dyFromGround, dz, dyFromGround) * 0.8
		case "beard_box":
			dy := max(0, max(groundY-y, y-box.maxY))
			total += worldgen.BeardContribution(dx, dy, dz, dyFromGround) * 0.8
		case "encapsulate":
			dy := max(0, max(box.minY-y, y-box.maxY))
			total += worldgen.BuryContribution(float64(dx)/2.0, float64(dy)/2.0, float64(dz)/2.0) * 0.8
		}
	}
	for _, j := range b.junctions {
		jdx := x - j.SourceX
		jdy := y - j.SourceGroundY
		jdz := z - j.SourceZ
		total += worldgen.BeardContribution(jdx, jdy, jdz, jdy) * 0.4
	}
	return total
}

