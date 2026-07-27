package world

import (
	"sync"

	"regionio/internal/worldgen"
)

// carve.go is the world side of the carvers: it hands the carver a view of the
// column array it is cutting into, and answers the two questions the carver
// cannot answer for itself — which blocks it may cut through, and what the
// surface rules want under a carved-away grass block.
//
// Carving runs between the surface pass and decoration, which is where vanilla
// puts it. That order is load-bearing in both directions: the surface rules
// must already have placed grass and podzol for the cave-mouth retexturing to
// have anything to look at, and decoration must run afterwards so trees are not
// planted over a hole.

// carveView adapts the generator's column array to worldgen.CarveTarget.
type carveView struct {
	cols   *[16][16][WorldHeight]uint16
	od     *worldgen.OverworldDensity
	rules  *worldgen.SurfaceRuleSet
	sctx   *worldgen.SurfaceContext
	biomes *[16][16]string
	// worldSurface is the pre-carve heightmap the steep condition reads, as in
	// vanilla, where carving happens after the surface pass has already run.
	worldSurface *[16][16]int
	baseX, baseZ int
}

func (v *carveView) Block(lx, y, lz int) uint16 {
	i := y - MinY
	if i < 0 || i >= WorldHeight || lx < 0 || lx > 15 || lz < 0 || lz > 15 {
		return StateAir
	}
	return v.cols[lx][lz][i]
}

func (v *carveView) SetBlock(lx, y, lz int, state uint16) {
	i := y - MinY
	if i < 0 || i >= WorldHeight || lx < 0 || lx > 15 || lz < 0 || lz > 15 {
		return
	}
	v.cols[lx][lz][i] = state
}

func (v *carveView) Replaceable(state uint16) bool { return carverReplaceable(state) }

// TopMaterial is SurfaceSystem.topMaterial: the rule tree applied to a single
// position, with the stone depths pinned to 1 and the water height set only
// when the block that was carved out holds fluid.
func (v *carveView) TopMaterial(lx, y, lz int, underFluid bool) (uint16, bool) {
	if v.rules == nil {
		return 0, false
	}
	wx, wz := v.baseX+lx, v.baseZ+lz
	v.rules.BeginColumn(v.sctx, wx, wz)
	surfaceDepth := v.od.Surface.SurfaceDepth(wx, wz)
	v.sctx.SeaLevel = SeaLevel
	v.sctx.MinY = MinY
	v.sctx.BiomeName = v.biomes[lx][lz]
	v.sctx.SurfaceSecondary = v.od.Surface.SurfaceSecondary(wx, wz)
	v.sctx.SurfaceDepth = surfaceDepth
	v.sctx.MinSurfaceLevel = v.od.MinSurfaceLevelAt(wx, wz, surfaceDepth)
	v.sctx.Steep = steepAt(v.worldSurface, lx, lz)
	v.sctx.Y = y
	v.sctx.StoneDepthAbove = 1
	v.sctx.StoneDepthBelow = 1
	v.sctx.WaterHeight = worldgen.NoWaterAbove
	if underFluid {
		v.sctx.WaterHeight = y + 1
	}
	return v.rules.Apply(v.sctx)
}

var (
	carverReplaceableOnce sync.Once
	carverReplaceableSet  []bool
)

// initCarverReplaceable resolves the carver's block names to every state of
// each block. The tag names blocks, not states, so waterlogged and snowy
// variants qualify too — which is why this walks the full state table rather
// than the default-state table.
func initCarverReplaceable(names []string) {
	carverReplaceableOnce.Do(func() {
		stateByIDOnce.Do(buildStateTable)
		carverReplaceableSet = make([]bool, totalBlockStates)
		for _, name := range names {
			for _, id := range idsByName[name] {
				if int(id) < len(carverReplaceableSet) {
					carverReplaceableSet[id] = true
				}
			}
		}
	})
}

// carverReplaceable reports whether a block state is in
// #minecraft:overworld_carver_replaceables.
func carverReplaceable(state uint16) bool {
	if int(state) >= len(carverReplaceableSet) {
		return false
	}
	return carverReplaceableSet[state]
}
