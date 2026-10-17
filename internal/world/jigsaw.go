package world

import (
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

var (
	jigsawTemplateMu    sync.Mutex
	jigsawTemplateCache = map[string]*cachedJigsawTemplate{}
)

func loadCachedJigsawTemplate(location string) (*cachedJigsawTemplate, error) {
	clean := strings.TrimPrefix(location, "minecraft:")
	jigsawTemplateMu.Lock()
	if cached, ok := jigsawTemplateCache[clean]; ok {
		jigsawTemplateMu.Unlock()
		return cached, nil
	}
	jigsawTemplateMu.Unlock()

	stateByIDOnce.Do(buildStateTable)
	raw, err := worldgen.EmbeddedStructureTemplate(clean)
	if err != nil {
		return nil, err
	}
	rawPalettes, size, err := worldgen.LoadResolvedTemplatePalettesBytes(raw, nameToStateID)
	if err != nil {
		return nil, err
	}
	entities, _ := worldgen.LoadResolvedTemplateEntitiesBytes(raw)

	// Sort each palette's blocks matching StructureTemplate.loadPalette / buildInfoList:
	// fullBlocks -> otherBlocks -> blocksWithNbt, each sorted by (Y, X, Z) ascending.
	palettes := make([]cachedJigsawPalette, len(rawPalettes))
	for pIdx, blocks := range rawPalettes {
		var fullBlocks, otherBlocks, nbtBlocks []worldgen.TemplateBlockInfo
		for _, b := range blocks {
			if b.HasNBT {
				nbtBlocks = append(nbtBlocks, b)
			} else if fullSolidState(b.State) {
				fullBlocks = append(fullBlocks, b)
			} else {
				otherBlocks = append(otherBlocks, b)
			}
		}
		yxzLess := func(s []worldgen.TemplateBlockInfo) func(i, j int) bool {
			return func(i, j int) bool {
				if s[i].Pos[1] != s[j].Pos[1] {
					return s[i].Pos[1] < s[j].Pos[1]
				}
				if s[i].Pos[0] != s[j].Pos[0] {
					return s[i].Pos[0] < s[j].Pos[0]
				}
				return s[i].Pos[2] < s[j].Pos[2]
			}
		}
		sort.Slice(fullBlocks, yxzLess(fullBlocks))
		sort.Slice(otherBlocks, yxzLess(otherBlocks))
		sort.Slice(nbtBlocks, yxzLess(nbtBlocks))

		combined := make([]worldgen.TemplateBlockInfo, 0, len(blocks))
		combined = append(combined, fullBlocks...)
		combined = append(combined, otherBlocks...)
		combined = append(combined, nbtBlocks...)

		var jigsaws []worldgen.TemplateBlockInfo
		for _, b := range combined {
			if b.Jigsaw != nil {
				jigsaws = append(jigsaws, b)
			}
		}
		palettes[pIdx] = cachedJigsawPalette{
			blocks:  combined,
			jigsaws: jigsaws,
		}
	}

	res := &cachedJigsawTemplate{
		size:     size,
		palettes: palettes,
		entities: entities,
	}
	jigsawTemplateMu.Lock()
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
	rng := worldgen.NewLegacy(seed)
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
	parts := strings.SplitN(orient, "_", 2)
	if len(parts) != 2 {
		return rotateJigsawDirection("north", rotation), "up"
	}
	return rotateJigsawDirection(parts[0], rotation), rotateJigsawDirection(parts[1], rotation)
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
	switch elem.ElementType {
	case "minecraft:feature_pool_element":
		// FeaturePoolElement.getShuffledJigsawBlocks does NOT call Util.shuffle (0 RNG draws).
		front, top := parseJigsawOrientation("down_south", rotation)
		return []rotatedJigsawInfo{{
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
		}}
	case "minecraft:list_pool_element":
		if len(elem.Elements) == 0 {
			return nil
		}
		return getShuffledJigsawBlocks(elem.Elements[0], pos, rotation, random)
	case "minecraft:single_pool_element", "minecraft:legacy_single_pool_element":
		tmpl, err := loadCachedJigsawTemplate(elem.Location)
		if err != nil || tmpl == nil {
			return nil
		}
		pal := selectJigsawPalette(tmpl, pos)
		if pal == nil || len(pal.jigsaws) == 0 {
			return nil
		}
		out := make([]rotatedJigsawInfo, len(pal.jigsaws))
		for i, b := range pal.jigsaws {
			rel := worldgen.TransformBlockPos(b.Pos, "none", rotation, [3]int{0, 0, 0})
			front, top := parseJigsawOrientation(b.Jigsaw.Orientation, rotation)
			out[i] = rotatedJigsawInfo{
				WorldPos:          [3]int{pos[0] + rel[0], pos[1] + rel[1], pos[2] + rel[2]},
				Front:             front,
				Top:               top,
				Name:              normalizeResourceID(b.Jigsaw.Name),
				Target:            normalizeResourceID(b.Jigsaw.Target),
				Pool:              normalizeResourceID(b.Jigsaw.Pool),
				FinalState:        b.Jigsaw.FinalState,
				Joint:             b.Jigsaw.Joint,
				SelectionPriority: b.Jigsaw.SelectionPriority,
				PlacementPriority: b.Jigsaw.PlacementPriority,
			}
		}
		jigsawShuffleSlice(out, random)
		sort.SliceStable(out, func(i, j int) bool {
			return out[i].SelectionPriority > out[j].SelectionPriority
		})
		return out
	default:
		return nil
	}
}

func poolMaxSize(pool *worldgen.TemplatePoolDef) int {
	if pool == nil {
		return 0
	}
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

	// RNG draw 1: Rotation.getRandom(random)
	rotation := int(random.NextIntN(4))

	startPool := reg.GetPool(def.StartPool)
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
		tryPlacingChildren := func(piece *JigsawPiece, freeSpace *jigsawFreeSpace, depth int) {
			parentElem := piece.Element
			parentPos := piece.Pos
			parentRot := piece.Rotation
			parentRigid := parentElem.Projection == "rigid"
			var pieceFreeSpace *jigsawFreeSpace
			parentBB := piece.Box
			parentMinY := parentBB.minY

			parentJigsaws := getShuffledJigsawBlocks(parentElem, parentPos, parentRot, random)
			for _, parentJigsaw := range parentJigsaws {
				step := jigsawDirStep(parentJigsaw.Front)
				jigsawPos := parentJigsaw.WorldPos
				targetPos := [3]int{jigsawPos[0] + step[0], jigsawPos[1] + step[1], jigsawPos[2] + step[2]}
				jigsawLocalY := jigsawPos[1] - parentMinY
				cachedSurfaceY := -1 << 30

				pool := reg.GetPool(parentJigsaw.Pool)
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

				var candidates []worldgen.PoolElementDef
				if depth != maxDepth {
					poolCopy := make([]worldgen.PoolElementDef, len(pool.Expanded))
					copy(poolCopy, pool.Expanded)
					jigsawShuffleSlice(poolCopy, random)
					candidates = append(candidates, poolCopy...)
				}
				fbCopy := make([]worldgen.PoolElementDef, len(fallback.Expanded))
				copy(fbCopy, fallback.Expanded)
				jigsawShuffleSlice(fbCopy, random)
				candidates = append(candidates, fbCopy...)

				placementPrio := parentJigsaw.PlacementPriority

			candidateLoop:
				for _, candidate := range candidates {
					if candidate.ElementType == "minecraft:empty_pool_element" {
						break
					}
					rots := []int{0, 1, 2, 3}
					jigsawShuffleSlice(rots, random)
					for _, candidateRot := range rots {
						childJigsaws := getShuffledJigsawBlocks(candidate, [3]int{0, 0, 0}, candidateRot, random)
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
								cp := reg.GetPool(cj.Pool)
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
	jigsawStartMu    sync.Mutex
	jigsawStartCache = map[jigsawCacheKey]*JigsawStart{}
)

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
	cacheKey := jigsawCacheKey{setID: setID, seed: seed, sx: sx, sz: sz}
	if od != nil {
		jigsawStartMu.Lock()
		if cached, ok := jigsawStartCache[cacheKey]; ok {
			jigsawStartMu.Unlock()
			return cached, nil
		}
		jigsawStartMu.Unlock()
	}
	stub, ok := FindStructureSetStart(od, sets, setID, seed, sx, sz)
	if !ok || stub == nil {
		if od != nil {
			jigsawStartMu.Lock()
			jigsawStartCache[cacheKey] = nil
			jigsawStartMu.Unlock()
		}
		return nil, nil
	}
	def := sets.Structures[stub.StructureName]
	if def == nil {
		return nil, nil
	}
	start, err := BuildJigsawStart(od, sets, def, seed, sx, sz, nil)
	if err == nil && od != nil {
		jigsawStartMu.Lock()
		jigsawStartCache[cacheKey] = start
		jigsawStartMu.Unlock()
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
		cappedCounts := make([]int, len(procs))

		for _, orig := range pal.blocks {
			rel := worldgen.TransformBlockPos(orig.Pos, "none", rotation, [3]int{0, 0, 0})
			worldPos := [3]int{pos[0] + rel[0], pos[1] + rel[1], pos[2] + rel[2]}
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

			// 3. Configured processors (RuleProcessor, CappedProcessor, BlockRotProcessor, ProtectedBlocksProcessor).
			discarded := false
			var ruleLootTable string
			var ruleLootSeed int64
			for pIdx, proc := range procs {
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
				case "minecraft:capped":
					if proc.Delegate != nil && proc.Delegate.ProcessorType == "minecraft:rule" && cappedCounts[pIdx] < proc.Limit {
						ruleRng := worldgen.NewLegacy(worldgen.MthGetSeed(worldPos[0], worldPos[1], worldPos[2]))
						existing := region.getBlock(worldPos[0], worldPos[1], worldPos[2])
						for _, rule := range proc.Delegate.Rules {
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
								cappedCounts[pIdx]++
							}
							if rule.BlockEntityModifier.Type == "minecraft:append_loot" && rule.BlockEntityModifier.LootTable != "" {
								ruleLootTable = rule.BlockEntityModifier.LootTable
								ruleLootSeed = ruleRng.NextLong()
							}
							break
						}
					}
				case "minecraft:block_rot":
					if proc.Integrity < 1.0 {
						rotRng := worldgen.NewLegacy(worldgen.MthGetSeed(worldPos[0], worldPos[1], worldPos[2]))
						if rotRng.NextFloat() > proc.Integrity {
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
			}

			// 5. LegacySinglePoolElement runs BlockIgnoreProcessor.STRUCTURE_AND_AIR last.
			if isLegacy && (curName == "minecraft:structure_block" || curName == "minecraft:air") {
				continue
			}
			if curName == "minecraft:structure_void" {
				continue
			}

			// Clip to chunkBox.
			if worldPos[0] < chunkBox.minX || worldPos[0] > chunkBox.maxX ||
				worldPos[1] < chunkBox.minY || worldPos[1] > chunkBox.maxY ||
				worldPos[2] < chunkBox.minZ || worldPos[2] > chunkBox.maxZ {
				continue
			}

			stateToPlace := rotateTemplateState(curState, rotation)

			// RandomizableContainer LootTableSeed draw on chunk's decoration random.
			var containerLootSeed int64
			if orig.IsRandomizableContainer {
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
				region.addStructureBlockEntity(StructureBlockEntity{
					ID:            "minecraft:brushable_block",
					X:             worldPos[0],
					Y:             worldPos[1],
					Z:             worldPos[2],
					LootTable:     ruleLootTable,
					LootTableSeed: ruleLootSeed,
				})
			} else if orig.NBT != nil {
				if be, ok := extractTemplateBlockEntity(worldPos[0], worldPos[1], worldPos[2], curName, orig.NBT, containerLootSeed); ok {
					region.addStructureBlockEntity(be)
				}
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
