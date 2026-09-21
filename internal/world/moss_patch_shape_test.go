package world

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// mossResidualCell is one mismatched cell that involves the moss patch's ground
// state, in absolute world coordinates.
type mossResidualCell struct {
	x, y, z       int
	ours, vanilla uint16
}

// mossResidual is what the moss diagnostics share: the mismatching cells, both
// sides' full moss sets, the cells both sides agree on, and the vanilla clay
// cells that mark the waterlogged pools moss patches cluster around.
type mossResidual struct {
	cells       []mossResidualCell
	vanillaMoss [][3]int
	ourMoss     [][3]int
	agreed      [][3]int
	clay        [][3]int

	// Both full grids, so a probe can read the support cell under a mismatch
	// rather than only the mismatch itself.
	capture   fixtureCapture
	ourChunks map[[2]int32]*Chunk
}

// vanillaAt reads a captured state at absolute coordinates. The fixture holds
// four non-contiguous chunks, so a cell outside them is reported as missing
// rather than as air: a probe must not mistake "not captured" for "empty".
func (m mossResidual) vanillaAt(x, y, z int) (uint16, bool) {
	for _, ch := range m.capture.chunks {
		if int(ch.cx) == x>>4 && int(ch.cz) == z>>4 {
			return ch.at(x&15, y, z&15), true
		}
	}
	return 0, false
}

func (m mossResidual) ourAt(x, y, z int) (uint16, bool) {
	chunk, ok := m.ourChunks[[2]int32{int32(x >> 4), int32(z >> 4)}]
	if !ok {
		return 0, false
	}
	return chunk.GetBlock(x&15, y, z&15), true
}

func collectMossResidual(capture fixtureCapture, gen Generator, mossID, clayID uint16) mossResidual {
	var out mossResidual
	out.capture = capture
	out.ourChunks = make(map[[2]int32]*Chunk, len(capture.chunks))
	ours := make(map[[3]int]bool)
	for _, ch := range capture.chunks {
		for y := MinY; y < MinY+WorldHeight; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					pos := [3]int{int(ch.cx)*16 + x, y, int(ch.cz)*16 + z}
					switch ch.at(x, y, z) {
					case mossID:
						out.vanillaMoss = append(out.vanillaMoss, pos)
					case clayID:
						out.clay = append(out.clay, pos)
					}
				}
			}
		}
	}
	for _, ch := range capture.chunks {
		chunk := gen(ch.cx, ch.cz)
		out.ourChunks[[2]int32{ch.cx, ch.cz}] = chunk
		for y := MinY; y < MinY+WorldHeight; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					pos := [3]int{int(ch.cx)*16 + x, y, int(ch.cz)*16 + z}
					got, want := chunk.GetBlock(x, y, z), ch.at(x, y, z)
					if got == mossID {
						out.ourMoss = append(out.ourMoss, pos)
						ours[pos] = true
					}
					if got == want || (got != mossID && want != mossID) {
						continue
					}
					out.cells = append(out.cells, mossResidualCell{
						x: pos[0], y: pos[1], z: pos[2], ours: got, vanilla: want,
					})
				}
			}
		}
	}
	for _, p := range out.vanillaMoss {
		if ours[p] {
			out.agreed = append(out.agreed, p)
		}
	}
	return out
}

func mossStateIDs(t *testing.T) (mossID, clayID uint16) {
	t.Helper()
	var ok bool
	if mossID, ok = nameToStateID("minecraft:moss_block", nil); !ok {
		t.Fatal("moss_block is not in the state table")
	}
	if clayID, ok = nameToStateID("minecraft:clay", nil); !ok {
		t.Fatal("clay is not in the state table")
	}
	return mossID, clayID
}

// TestMossPatchMismatchShape answers how the moss-patch residual is shaped,
// which is the question a count cannot: ~100 moss_block cells could be a smaller
// radius (a ring of vanilla-only cells at one Y with no counterpart of ours), a
// displaced origin (a vanilla-only disc beside an ours-only disc of the same
// footprint), a vertical shift (both sets at different Y on the same columns),
// or a patch that never ran (vanilla-only with nothing of ours nearby). Each
// names a different bug, so the clusters are drawn per Y plane and classified
// rather than counted.
func TestMossPatchMismatchShape(t *testing.T) {
	requireDiagnostic(t, "REGIONIO_MOSS_PATCH_DIAGNOSTIC")

	capture := readFixtureCapture(t, vanillaParityFixture)
	gen := NewVanillaRegionGenerator(capture.seed)
	mossID, clayID := mossStateIDs(t)
	residual := collectMossResidual(capture, gen, mossID, clayID)
	cells := residual.cells
	t.Logf("moss_block census: vanilla %d cells, ours %d, both %d (vanilla missed %d, ours extra %d)",
		len(residual.vanillaMoss), len(residual.ourMoss), len(residual.agreed),
		len(residual.vanillaMoss)-len(residual.agreed), len(residual.ourMoss)-len(residual.agreed))
	if len(cells) == 0 {
		t.Log("no moss_block mismatches: the family is closed")
		return
	}

	// Cluster on 6-connectivity over the involved cells. A patch is a disc one
	// or two blocks thick, so adjacency is the right grain: it separates
	// neighbouring patches without splitting one.
	index := make(map[[3]int]int, len(cells))
	for i, c := range cells {
		index[[3]int{c.x, c.y, c.z}] = i
	}
	group := make([]int, len(cells))
	for i := range group {
		group[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for group[i] != i {
			group[i] = group[group[i]]
			i = group[i]
		}
		return i
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			group[rb] = ra
		}
	}
	for i, c := range cells {
		for _, d := range [6][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}} {
			if j, ok := index[[3]int{c.x + d[0], c.y + d[1], c.z + d[2]}]; ok {
				union(i, j)
			}
		}
	}
	clusters := make(map[int][]mossResidualCell)
	for i, c := range cells {
		root := find(i)
		clusters[root] = append(clusters[root], c)
	}
	roots := make([]int, 0, len(clusters))
	for root := range clusters {
		roots = append(roots, root)
	}
	sort.Slice(roots, func(i, j int) bool {
		a, b := clusters[roots[i]][0], clusters[roots[j]][0]
		if a.y != b.y {
			return a.y < b.y
		}
		if a.x != b.x {
			return a.x < b.x
		}
		return a.z < b.z
	})

	verdicts := make(map[string]int)
	t.Logf("moss_block residual: %d cells in %d clusters", len(cells), len(roots))
	for _, root := range roots {
		cluster := clusters[root]
		minX, maxX := cluster[0].x, cluster[0].x
		minY, maxY := cluster[0].y, cluster[0].y
		minZ, maxZ := cluster[0].z, cluster[0].z
		onlyVanilla, onlyOurs := 0, 0
		counterparts := make(map[string]int)
		set := make(map[[3]int]mossResidualCell, len(cluster))
		for _, c := range cluster {
			set[[3]int{c.x, c.y, c.z}] = c
			minX = min(minX, c.x)
			maxX = max(maxX, c.x)
			minY = min(minY, c.y)
			maxY = max(maxY, c.y)
			minZ = min(minZ, c.z)
			maxZ = max(maxZ, c.z)
			if c.ours == mossID {
				onlyOurs++
				counterparts["ours moss over "+stateLabel(c.vanilla)]++
			} else {
				onlyVanilla++
				counterparts["vanilla moss over "+stateLabel(c.ours)]++
			}
		}
		spanX, spanZ := maxX-minX+1, maxZ-minZ+1
		verdict := classifyMossCluster(onlyVanilla, onlyOurs, minY, maxY, spanX, spanZ)
		verdicts[verdict]++
		t.Logf("cluster at (%d,%d,%d): %d cells (vanilla-only %d, ours-only %d), bbox %dx%dx%d => %s",
			minX, minY, minZ, len(cluster), onlyVanilla, onlyOurs, spanX, maxY-minY+1, spanZ, verdict)
		for label, n := range counterparts {
			t.Logf("    %s: %d", label, n)
		}
		for y := minY; y <= maxY; y++ {
			var rows []string
			for z := minZ; z <= maxZ; z++ {
				row := ""
				for x := minX; x <= maxX; x++ {
					c, ok := set[[3]int{x, y, z}]
					switch {
					case !ok:
						row += "."
					case c.ours == mossID:
						row += "O"
					default:
						row += "V"
					}
				}
				rows = append(rows, fmt.Sprintf("      z=%3d %s", z, row))
			}
			t.Logf("    y=%d\n%s", y, strings.Join(rows, "\n"))
		}
	}
	t.Logf("cluster verdicts: %v", verdicts)
}

// TestMossPatchMismatchCorrelation separates an edge defect from a missing patch
// by distance rather than by shape. A mismatch one or two cells from a moss cell
// both sides agree on belongs to a patch we also placed, so the defect is the
// edge-column roll or the candidate test. A mismatch with no agreed moss cell
// within a patch radius belongs to a patch that does not exist on that side at
// all, which is an origin or world-state defect. Distance to the nearest clay
// cell is reported alongside because lush-cave pools are where moss concentrates,
// so a correlation there would point at the waterlogged interaction rather than
// at the patch.
func TestMossPatchMismatchCorrelation(t *testing.T) {
	requireDiagnostic(t, "REGIONIO_MOSS_PATCH_DIAGNOSTIC")

	capture := readFixtureCapture(t, vanillaParityFixture)
	gen := NewVanillaRegionGenerator(capture.seed)
	mossID, clayID := mossStateIDs(t)
	residual := collectMossResidual(capture, gen, mossID, clayID)
	if len(residual.cells) == 0 {
		t.Skip("no moss_block mismatches to correlate")
	}

	chebyshev := func(p, q [3]int) int {
		d := abs3(p[0] - q[0])
		if e := abs3(p[1] - q[1]); e > d {
			d = e
		}
		if e := abs3(p[2] - q[2]); e > d {
			d = e
		}
		return d
	}
	nearest := func(p [3]int, pool [][3]int) int {
		best := 1 << 30
		for _, q := range pool {
			if d := chebyshev(p, q); d < best {
				best = d
			}
		}
		return best
	}
	missedNear, extraNear := make([]int, 11), make([]int, 11)
	missedClay, extraClay, agreedClay := make([]int, 11), make([]int, 11), make([]int, 11)
	for _, c := range residual.cells {
		p := [3]int{c.x, c.y, c.z}
		near := min(nearest(p, residual.agreed), 10)
		clay := min(nearest(p, residual.clay), 10)
		if c.vanilla == mossID {
			missedNear[near]++
			missedClay[clay]++
		} else {
			extraNear[near]++
			extraClay[clay]++
		}
	}
	for _, p := range residual.agreed {
		agreedClay[min(nearest(p, residual.clay), 10)]++
	}
	t.Logf("chebyshev distance from moss cells to the nearest agreed moss cell and to the nearest vanilla clay cell")
	t.Logf("the agreed/clay column is the control for the two mismatch columns")
	t.Logf("%-10s %8s %8s %12s %12s %12s", "distance", "missed", "extra", "missed/clay", "extra/clay", "agreed/clay")
	for d := 0; d <= 10; d++ {
		label := fmt.Sprintf("%d", d)
		if d == 10 {
			label = "10+"
		}
		t.Logf("%-10s %8d %8d %12d %12d %12d", label,
			missedNear[d], extraNear[d], missedClay[d], extraClay[d], agreedClay[d])
	}

	// The causal question: is each mismatch sitting in a neighbourhood whose
	// fluid state already differs? The moss patch scans for its floor and tests
	// the candidate for air, so one water cell in the wrong place is enough to
	// move its rim. Moss cells themselves are excluded, because those are the
	// thing being explained, not the explanation.
	waterID, ok := nameToStateID("minecraft:water", nil)
	if !ok {
		t.Fatal("water is not in the state table")
	}
	class := func(id uint16) int {
		switch id {
		case waterID:
			return 1
		case mossID:
			return 2
		}
		if isAirState(id) {
			return 0
		}
		return 3
	}
	byCoords := make(map[[2]int32]fixtureChunk, len(capture.chunks))
	ourChunks := make(map[[2]int32]*Chunk, len(capture.chunks))
	for _, ch := range capture.chunks {
		key := [2]int32{ch.cx, ch.cz}
		byCoords[key] = ch
		ourChunks[key] = gen(ch.cx, ch.cz)
	}
	mismatchMap := make(map[[3]int]bool, len(residual.cells))
	for _, c := range residual.cells {
		mismatchMap[[3]int{c.x, c.y, c.z}] = true
	}
	neighbourDifferences := func(p [3]int, radius int) int {
		diffs := 0
		for dy := -radius; dy <= radius; dy++ {
			for dz := -radius; dz <= radius; dz++ {
				for dx := -radius; dx <= radius; dx++ {
					q := [3]int{p[0] + dx, p[1] + dy, p[2] + dz}
					if q == p || mismatchMap[q] {
						continue
					}
					key := [2]int32{int32(floorDiv(q[0], 16)), int32(floorDiv(q[2], 16))}
					ch, found := byCoords[key]
					if !found || q[1] < MinY || q[1] >= MinY+WorldHeight {
						continue
					}
					lx, lz := q[0]-int(ch.cx)*16, q[2]-int(ch.cz)*16
					if class(ch.at(lx, q[1], lz)) != class(ourChunks[key].GetBlock(lx, q[1], lz)) {
						diffs++
					}
				}
			}
		}
		return diffs
	}
	missedTotal, extraTotal := 0, 0
	missedIsolated, extraIsolated := 0, 0
	for _, c := range residual.cells {
		empty := neighbourDifferences([3]int{c.x, c.y, c.z}, 3) == 0
		if c.vanilla == mossID {
			missedTotal++
			if empty {
				missedIsolated++
			}
		} else {
			extraTotal++
			if empty {
				extraIsolated++
			}
		}
	}
	t.Logf("mismatches whose whole 7x7x7 neighbourhood agrees apart from other mismatches: missed %d/%d, extra %d/%d",
		missedIsolated, missedTotal, extraIsolated, extraTotal)
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// TestVegetationResidualOverlap answers, per decorated block, whether the
// residual is a position problem or an amount problem. Blocks whose cells mostly
// agree and only disagree at the edges have the right origins and a wrong
// footprint; blocks where barely anything agrees have a wrong stream or a wrong
// placement chain, and chasing their geometry would be wasted effort. A patch
// rolls its nested vegetation once per ground cell, so the nested blocks should
// agree less than the ground if a ground defect is cascading into them.
func TestVegetationResidualOverlap(t *testing.T) {
	requireDiagnostic(t, "REGIONIO_MOSS_PATCH_DIAGNOSTIC")

	names := []string{
		"minecraft:moss_block", "minecraft:moss_carpet", "minecraft:clay",
		"minecraft:cave_vines", "minecraft:cave_vines_plant",
		"minecraft:big_dripleaf", "minecraft:small_dripleaf",
		"minecraft:short_grass", "minecraft:tall_grass", "minecraft:large_fern",
		"minecraft:azalea", "minecraft:flowering_azalea", "minecraft:sugar_cane",
		"minecraft:sea_pickle", "minecraft:kelp_plant", "minecraft:dirt",
	}
	watch := make(map[string]bool, len(names))
	for _, name := range names {
		watch[name] = true
	}
	capture := readFixtureCapture(t, vanillaParityFixture)
	gen := NewVanillaRegionGenerator(capture.seed)
	// Counted by block, not by state: several of these carry properties, and a
	// property disagreement is a different bug from a cell that moved.
	type overlap struct{ vanilla, ours, both int }
	stats := make(map[string]*overlap, len(names))
	for _, name := range names {
		stats[name] = &overlap{}
	}
	for _, ch := range capture.chunks {
		chunk := gen(ch.cx, ch.cz)
		for y := MinY; y < MinY+WorldHeight; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					got, want := chunk.GetBlock(x, y, z), ch.at(x, y, z)
					wantLabel, gotLabel := stateLabel(want), stateLabel(got)
					if watch[wantLabel] {
						stats[wantLabel].vanilla++
					}
					if watch[gotLabel] {
						stats[gotLabel].ours++
					}
					if gotLabel == wantLabel && watch[gotLabel] {
						stats[gotLabel].both++
					}
				}
			}
		}
	}
	t.Logf("%-32s %8s %8s %8s %8s %8s %8s", "block", "vanilla", "ours", "both", "missed", "extra", "agree%")
	for _, name := range names {
		s := stats[name]
		share := 0.0
		if s.vanilla > 0 {
			share = 100 * float64(s.both) / float64(s.vanilla)
		}
		t.Logf("%-32s %8d %8d %8d %8d %8d %7.1f%%", name, s.vanilla, s.ours, s.both,
			s.vanilla-s.both, s.ours-s.both, share)
	}
}

func classifyMossCluster(onlyVanilla, onlyOurs, minY, maxY, spanX, spanZ int) string {
	switch {
	case onlyOurs == 0:
		return "vanilla-only fragment"
	case onlyVanilla == 0:
		return "ours-only fragment"
	case minY == maxY:
		return "same plane, both sides: footprint or edge roll"
	case spanX <= 2 && spanZ <= 2:
		return "same columns, different Y: the scan landed elsewhere"
	default:
		return "mixed"
	}
}

func abs3(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// TestMossResidualPlacementContext separates the two explanations the shape and
// distance tables cannot: a rim cell whose support block is the same on both
// sides was decided by a roll or a write gate, while a rim cell whose support
// differs was decided by terrain that diverged before the patch ran. It also
// breaks the residual down by target chunk, because decorationSources tunes the
// source order for (0,0) and (1,0) only — if the misses concentrate in the two
// chunks running the untuned branch, the ordering is the cause and no amount of
// patch-code reading will find it.
func TestMossResidualPlacementContext(t *testing.T) {
	requireDiagnostic(t, "REGIONIO_MOSS_PATCH_DIAGNOSTIC")

	capture := readFixtureCapture(t, vanillaParityFixture)
	gen := NewVanillaRegionGenerator(capture.seed)
	mossID, clayID := mossStateIDs(t)
	res := collectMossResidual(capture, gen, mossID, clayID)

	type perChunk struct{ vanilla, ours, agreed, missed, extra, edgeMissed int }
	byChunk := make(map[[2]int32]*perChunk, len(capture.chunks))
	for _, ch := range capture.chunks {
		byChunk[[2]int32{ch.cx, ch.cz}] = &perChunk{}
	}
	for _, p := range res.vanillaMoss {
		byChunk[[2]int32{int32(p[0] >> 4), int32(p[2] >> 4)}].vanilla++
	}
	for _, p := range res.ourMoss {
		byChunk[[2]int32{int32(p[0] >> 4), int32(p[2] >> 4)}].ours++
	}
	for _, p := range res.agreed {
		byChunk[[2]int32{int32(p[0] >> 4), int32(p[2] >> 4)}].agreed++
	}
	edge := make([]int, 8)
	edgeAgreed := make([]int, 8)
	missedTotal := 0
	supportKind := make(map[mossResidualCell]string, len(res.cells))
	terrainPairs := make(map[[2]string]int)
	var examples []string
	for _, c := range res.cells {
		cell := byChunk[[2]int32{int32(c.x >> 4), int32(c.z >> 4)}]
		d := chunkEdgeDistance(c.x, c.z)
		if c.vanilla == mossID {
			cell.missed++
			missedTotal++
			edge[d]++
		} else {
			cell.extra++
		}
		if c.vanilla == mossID && d <= 1 {
			cell.edgeMissed++
		}
		// A floor patch rests on the cell below and a ceiling patch on the one
		// above, so both are compared. The pair is classified rather than just
		// diffed: a difference where one side is air or a plant is our own
		// cascade (the nested vegetation we placed and vanilla did not), which
		// says nothing about the terrain the patch scanned. Only a solid-versus-
		// solid difference is a pre-existing terrain divergence, and only that
		// would implicate the decoration source order.
		for dy := -1; dy <= 1; dy += 2 {
			ours, okOurs := res.ourAt(c.x, c.y+dy, c.z)
			want, okWant := res.vanillaAt(c.x, c.y+dy, c.z)
			// Moss in a support cell is the same defect one block away, not
			// independent terrain: counting it as a difference would make every
			// rim cell look like a divergence.
			if !okOurs || !okWant || ours == want || ours == mossID || want == mossID {
				continue
			}
			kind := "cover"
			if isSolidState(ours) && isSolidState(want) {
				kind = "terrain"
				terrainPairs[[2]string{stateLabel(ours), stateLabel(want)}]++
				if len(examples) < 12 {
					examples = append(examples, fmt.Sprintf("(%d,%d,%d) y%+d: ours %s, vanilla %s",
						c.x, c.y, c.z, dy, stateLabel(ours), stateLabel(want)))
				}
			}
			if supportKind[c] == "" {
				supportKind[c] = kind
			}
		}
	}
	terrain, cover, identical := 0, 0, 0
	for _, c := range res.cells {
		switch supportKind[c] {
		case "terrain":
			terrain++
		case "cover":
			cover++
		default:
			identical++
		}
	}
	t.Logf("per target chunk, and which decorationSources branch it runs")
	t.Logf("%-10s %-8s %8s %8s %8s %8s %8s %8s", "chunk", "sources", "vanilla", "ours", "agree", "missed", "extra", "miss@d<=1")
	for _, ch := range capture.chunks {
		key := [2]int32{ch.cx, ch.cz}
		s := byChunk[key]
		order := "default (target-first)"
		if (key[0] == 0 && key[1] == 0) || (key[0] == 1 && key[1] == 0) {
			order = "tuned (Z-major)"
		}
		t.Logf("%-10s %-8s %8d %8d %8d %8d %8d %8d",
			fmt.Sprintf("(%d,%d)", key[0], key[1]), order,
			s.vanilla, s.ours, s.agreed, s.missed, s.extra, s.edgeMissed)
	}
	t.Logf("missed cells by distance to the edge of their own chunk (0 = on the boundary column)")
	t.Logf("%-10s %8s", "edge dist", "missed")
	for d := 0; d < 8; d++ {
		t.Logf("%-10d %8d", d, edge[d])
	}
	for _, p := range res.agreed {
		edgeAgreed[chunkEdgeDistance(p[0], p[2])]++
	}
	t.Logf("control: agreed moss cells, same histogram, as a share of each set")
	for d := 0; d < 8; d++ {
		share := 0.0
		if len(res.agreed) > 0 {
			share = 100 * float64(edgeAgreed[d]) / float64(len(res.agreed))
		}
		missedShare := 0.0
		if missedTotal > 0 {
			missedShare = 100 * float64(edge[d]) / float64(missedTotal)
		}
		t.Logf("edge %-2d  missed %5.1f%%   agreed %5.1f%%", d, missedShare, share)
	}
	t.Logf("support test: of %d mismatch cells, %d have a solid-versus-solid terrain difference below or above, %d differ only in air or plants (our own cascade), %d have identical support",
		len(res.cells), terrain, cover, identical)
	for pair, n := range terrainPairs {
		t.Logf("    terrain pair %s where vanilla has %s: %d", pair[0], pair[1], n)
	}
	for _, e := range examples {
		t.Logf("    %s", e)
	}
}

func chunkEdgeDistance(x, z int) int {
	bx, bz := x&15, z&15
	return min(min(bx, 15-bx), min(bz, 15-bz))
}
