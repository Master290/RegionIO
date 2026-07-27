package worldgen

import (
	"encoding/json"
	"fmt"
	"math"
)

// carver.go ports net.minecraft.world.level.levelgen.carver: the cave and
// canyon carvers, and the driver that replays them.
//
// Carving is the step between the surface rules and decoration. The density
// router already opens cheese, spaghetti and noodle caves; carvers cut the
// other kind — the long winding tunnels with rooms and branches, and the
// ravines that slice down through the terrain. Everything the router makes is
// noise-shaped; everything here is walked, step by step, by a random source.
//
// The shape of the work is unusual and worth stating plainly: to carve one
// chunk, vanilla replays every carver seeded in the 17x17 chunks around it and
// keeps only what lands inside. The same tunnel is therefore walked up to 289
// times across a world. That redundancy is not an accident to be optimised
// away — it is what lets a chunk be carved without generating its neighbours,
// which is the only reason carving fits into a generator that produces one
// chunk at a time.

// carverRange is WorldCarver.getRange(); neither overworld carver overrides it.
const carverRange = 4

// carveDistance is the tunnel length budget, SectionPos.sectionToBlockCoord(getRange()*2-1).
const carveDistance = (carverRange*2 - 1) * 16

// carverNeighbourhood is applyCarvers' loop bound: dx and dz each run -8..8
// inclusive, so 289 source chunks feed every carved chunk. It is a fixed 8, not
// derived from getRange().
const carverNeighbourhood = 8

// ---- providers ---------------------------------------------------------

// floatProvider is FloatProvider: a bare number is a constant, an object
// dispatches on "type". The number of draws each kind makes is part of the
// contract — a constant draws nothing, and that silence is load-bearing.
type floatProvider interface {
	sample(r RandomSource) float32
}

type constantFloat float32

func (c constantFloat) sample(RandomSource) float32 { return float32(c) }

type uniformFloat struct{ lo, hi float32 }

func (u uniformFloat) sample(r RandomSource) float32 { return randomBetween(r, u.lo, u.hi) }

// trapezoidFloat draws twice, in this order.
type trapezoidFloat struct{ min, max, plateau float32 }

func (t trapezoidFloat) sample(r RandomSource) float32 {
	span := t.max - t.min
	slope := (span - t.plateau) / 2.0
	flat := span - slope
	return t.min + r.NextFloat()*flat + r.NextFloat()*slope
}

func parseFloatProvider(raw json.RawMessage) (floatProvider, error) {
	var number float32
	if err := json.Unmarshal(raw, &number); err == nil {
		return constantFloat(number), nil
	}
	var obj struct {
		Type         string  `json:"type"`
		Value        float32 `json:"value"`
		MinInclusive float32 `json:"min_inclusive"`
		MaxExclusive float32 `json:"max_exclusive"`
		Min          float32 `json:"min"`
		Max          float32 `json:"max"`
		Plateau      float32 `json:"plateau"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	switch obj.Type {
	case "minecraft:constant":
		return constantFloat(obj.Value), nil
	case "minecraft:uniform":
		return uniformFloat{obj.MinInclusive, obj.MaxExclusive}, nil
	case "minecraft:trapezoid":
		return trapezoidFloat{obj.Min, obj.Max, obj.Plateau}, nil
	}
	return nil, fmt.Errorf("carver: unsupported float provider %q", obj.Type)
}

// heightProvider is HeightProvider. A bare vertical anchor is a constant.
type heightProvider interface {
	sample(r RandomSource, minY, height int) int
}

type constantHeight struct{ anchor anchorJSON }

func (c constantHeight) sample(_ RandomSource, minY, height int) int {
	return resolveAnchorY(c.anchor, minY, height)
}

type uniformHeight struct{ lo, hi anchorJSON }

func (u uniformHeight) sample(r RandomSource, minY, height int) int {
	lo := resolveAnchorY(u.lo, minY, height)
	hi := resolveAnchorY(u.hi, minY, height)
	if lo > hi {
		// Vanilla logs and returns the low bound without drawing.
		return lo
	}
	return randomBetweenInclusive(r, lo, hi)
}

func parseHeightProvider(raw json.RawMessage) (heightProvider, error) {
	var obj struct {
		Type         string     `json:"type"`
		MinInclusive anchorJSON `json:"min_inclusive"`
		MaxInclusive anchorJSON `json:"max_inclusive"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Type == "minecraft:uniform" {
		return uniformHeight{obj.MinInclusive, obj.MaxInclusive}, nil
	}
	var anchor anchorJSON
	if err := json.Unmarshal(raw, &anchor); err != nil {
		return nil, fmt.Errorf("carver: unsupported height provider: %w", err)
	}
	return constantHeight{anchor}, nil
}

// ---- configuration -----------------------------------------------------

type carverKind int

const (
	carverCave carverKind = iota
	carverCanyon
)

type canyonShape struct {
	distanceFactor              floatProvider
	thickness                   floatProvider
	horizontalRadiusFactor      floatProvider
	verticalRadiusDefaultFactor float32
	verticalRadiusCenterFactor  float32
	widthSmoothness             int
}

type carverConfig struct {
	kind        carverKind
	name        string
	probability float32
	y           heightProvider
	lavaLevel   anchorJSON
	yScale      floatProvider

	// cave
	horizontalRadiusMultiplier floatProvider
	verticalRadiusMultiplier   floatProvider
	floorLevel                 floatProvider

	// canyon
	verticalRotation floatProvider
	shape            canyonShape
}

func loadCarverConfig(name string) (*carverConfig, error) {
	raw, err := dataFS.ReadFile("data/carver/" + name + ".json")
	if err != nil {
		return nil, err
	}
	var doc struct {
		Type   string `json:"type"`
		Config struct {
			Probability                float32         `json:"probability"`
			Y                          json.RawMessage `json:"y"`
			LavaLevel                  anchorJSON      `json:"lava_level"`
			YScale                     json.RawMessage `json:"yScale"`
			HorizontalRadiusMultiplier json.RawMessage `json:"horizontal_radius_multiplier"`
			VerticalRadiusMultiplier   json.RawMessage `json:"vertical_radius_multiplier"`
			FloorLevel                 json.RawMessage `json:"floor_level"`
			VerticalRotation           json.RawMessage `json:"vertical_rotation"`
			Shape                      struct {
				DistanceFactor              json.RawMessage `json:"distance_factor"`
				Thickness                   json.RawMessage `json:"thickness"`
				HorizontalRadiusFactor      json.RawMessage `json:"horizontal_radius_factor"`
				VerticalRadiusDefaultFactor float32         `json:"vertical_radius_default_factor"`
				VerticalRadiusCenterFactor  float32         `json:"vertical_radius_center_factor"`
				WidthSmoothness             int             `json:"width_smoothness"`
			} `json:"shape"`
		} `json:"config"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("carver %s: %w", name, err)
	}
	c := &carverConfig{name: name, probability: doc.Config.Probability, lavaLevel: doc.Config.LavaLevel}
	if c.y, err = parseHeightProvider(doc.Config.Y); err != nil {
		return nil, fmt.Errorf("carver %s y: %w", name, err)
	}
	if c.yScale, err = parseFloatProvider(doc.Config.YScale); err != nil {
		return nil, fmt.Errorf("carver %s yScale: %w", name, err)
	}
	switch doc.Type {
	case "minecraft:cave":
		c.kind = carverCave
		for _, f := range []struct {
			raw json.RawMessage
			dst *floatProvider
			key string
		}{
			{doc.Config.HorizontalRadiusMultiplier, &c.horizontalRadiusMultiplier, "horizontal_radius_multiplier"},
			{doc.Config.VerticalRadiusMultiplier, &c.verticalRadiusMultiplier, "vertical_radius_multiplier"},
			{doc.Config.FloorLevel, &c.floorLevel, "floor_level"},
		} {
			if *f.dst, err = parseFloatProvider(f.raw); err != nil {
				return nil, fmt.Errorf("carver %s %s: %w", name, f.key, err)
			}
		}
	case "minecraft:canyon":
		c.kind = carverCanyon
		if c.verticalRotation, err = parseFloatProvider(doc.Config.VerticalRotation); err != nil {
			return nil, fmt.Errorf("carver %s vertical_rotation: %w", name, err)
		}
		s := doc.Config.Shape
		c.shape.verticalRadiusDefaultFactor = s.VerticalRadiusDefaultFactor
		c.shape.verticalRadiusCenterFactor = s.VerticalRadiusCenterFactor
		c.shape.widthSmoothness = s.WidthSmoothness
		for _, f := range []struct {
			raw json.RawMessage
			dst *floatProvider
			key string
		}{
			{s.DistanceFactor, &c.shape.distanceFactor, "distance_factor"},
			{s.Thickness, &c.shape.thickness, "thickness"},
			{s.HorizontalRadiusFactor, &c.shape.horizontalRadiusFactor, "horizontal_radius_factor"},
		} {
			if *f.dst, err = parseFloatProvider(f.raw); err != nil {
				return nil, fmt.Errorf("carver %s shape.%s: %w", name, f.key, err)
			}
		}
	default:
		return nil, fmt.Errorf("carver %s: unsupported type %q", name, doc.Type)
	}
	return c, nil
}

// overworldCarvers is the carver list every one of the 54 overworld biomes
// carries, in the order the biome files list them. The index is part of the
// per-chunk seed, so the order matters as much as the contents.
var overworldCarvers = []string{"cave", "cave_extra_underground", "canyon"}

// ---- the carving mask --------------------------------------------------

// carvingMask is CarvingMask: one bit per block of the target chunk, so a
// position already opened by an earlier carver is not reconsidered. It is what
// makes the replay order matter.
type carvingMask struct {
	bits []uint64
	minY int
}

func newCarvingMask(minY, height int) *carvingMask {
	return &carvingMask{bits: make([]uint64, (256*height+63)/64), minY: minY}
}

func (m *carvingMask) index(lx, y, lz int) int {
	return (lx & 15) | ((lz & 15) << 4) | ((y - m.minY) << 8)
}

func (m *carvingMask) get(lx, y, lz int) bool {
	i := m.index(lx, y, lz)
	return m.bits[i>>6]&(1<<uint(i&63)) != 0
}

func (m *carvingMask) set(lx, y, lz int) {
	i := m.index(lx, y, lz)
	m.bits[i>>6] |= 1 << uint(i&63)
}

// ---- the target --------------------------------------------------------

// CarveTarget is the chunk being carved. Coordinates are chunk-local in x and
// z and absolute in y; a carver never addresses a block outside the chunk it
// was handed, however far its tunnel wandered to get there.
type CarveTarget interface {
	Block(lx, y, lz int) uint16
	SetBlock(lx, y, lz int, state uint16)
	// Replaceable reports whether a state is in
	// #minecraft:overworld_carver_replaceables. It is a block-level test, so
	// every state of a listed block qualifies.
	Replaceable(state uint16) bool
	// TopMaterial re-runs the surface rule at one position, to retexture the
	// dirt left exposed under a carved-away grass block. ok=false leaves it.
	TopMaterial(lx, y, lz int, underFluid bool) (uint16, bool)
}

// ---- the carver --------------------------------------------------------

// Carver replays the overworld's configured carvers over a chunk. Build one per
// generator; it holds no per-chunk state.
type Carver struct {
	configs []*carverConfig
	seed    int64
	minY    int
	height  int
	// grassBlocks and mycelium are the states whose removal exposes dirt worth
	// retexturing; dirt is the state that gets retextured.
	replaceableNames []string
}

// NewCarver loads the overworld carver configs. A parse failure is returned
// rather than swallowed: silently generating an uncarved world would look like
// the carvers simply do not work.
func NewCarver(od *OverworldDensity, seed int64) (*Carver, error) {
	c := &Carver{seed: seed, minY: od.MinY, height: od.Height}
	for _, name := range overworldCarvers {
		cfg, err := loadCarverConfig(name)
		if err != nil {
			return nil, err
		}
		c.configs = append(c.configs, cfg)
	}
	names, err := loadCarverReplaceables()
	if err != nil {
		return nil, err
	}
	c.replaceableNames = names
	return c, nil
}

// ReplaceableBlocks returns the block names a carver may cut through, so the
// caller can resolve them to every state of each block.
func (c *Carver) ReplaceableBlocks() []string { return c.replaceableNames }

func loadCarverReplaceables() ([]string, error) {
	raw, err := dataFS.ReadFile("data/carver/replaceable.json")
	if err != nil {
		return nil, err
	}
	var doc struct {
		Values []string `json:"values"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return doc.Values, nil
}

// carveState is the per-chunk scratch a carve pass needs.
type carveState struct {
	c      *Carver
	target CarveTarget
	aq     *Aquifer
	mask   *carvingMask
	// chunkX and chunkZ are the chunk being written, which is not the chunk a
	// tunnel was seeded in.
	chunkX, chunkZ int
	lavaY          int
}

// CarveChunk replays every carver seeded in the 17x17 chunks around (chunkX,
// chunkZ) and applies whatever reaches this chunk.
//
// The order is fixed and cannot be parallelised inside a chunk: a later carve
// reads blocks an earlier one wrote, and they share the mask.
func (c *Carver) CarveChunk(target CarveTarget, aq *Aquifer, chunkX, chunkZ int) {
	st := &carveState{
		c: c, target: target, aq: aq,
		mask:   newCarvingMask(c.minY, c.height),
		chunkX: chunkX, chunkZ: chunkZ,
	}
	random := NewLegacy(0)
	for dx := -carverNeighbourhood; dx <= carverNeighbourhood; dx++ {
		for dz := -carverNeighbourhood; dz <= carverNeighbourhood; dz++ {
			sourceX, sourceZ := chunkX+dx, chunkZ+dz
			for index, cfg := range c.configs {
				// The carver's index in the biome's list is part of the seed,
				// which is why the list order matters as much as its contents.
				random.SetLargeFeatureSeed(c.seed+int64(index), sourceX, sourceZ)
				if random.NextFloat() > cfg.probability {
					continue
				}
				st.lavaY = resolveAnchorY(cfg.lavaLevel, c.minY, c.height)
				switch cfg.kind {
				case carverCave:
					st.carveCave(cfg, random, sourceX, sourceZ)
				case carverCanyon:
					st.carveCanyon(cfg, random, sourceX, sourceZ)
				}
			}
		}
	}
}

// ---- cave --------------------------------------------------------------

func (s *carveState) carveCave(cfg *carverConfig, random *Legacy, sourceX, sourceZ int) {
	const caveBound = 15
	tunnelSystems := int(random.NextIntN(random.NextIntN(random.NextIntN(caveBound)+1) + 1))
	for k := 0; k < tunnelSystems; k++ {
		x := float64(sourceX<<4 + int(random.NextIntN(16)))
		y := float64(cfg.y.sample(random, s.c.minY, s.c.height))
		z := float64(sourceZ<<4 + int(random.NextIntN(16)))
		horizontalMul := float64(cfg.horizontalRadiusMultiplier.sample(random))
		verticalMul := float64(cfg.verticalRadiusMultiplier.sample(random))
		floorLevel := float64(cfg.floorLevel.sample(random))
		skip := caveSkip(floorLevel)

		tunnels := 1
		if random.NextIntN(4) == 0 {
			// A room. It is the only place the cave carver reads yScale, it
			// ignores both radius multipliers, and it sits one block east of
			// where the tunnels start.
			roomYScale := float64(cfg.yScale.sample(random))
			radius := 1.0 + random.NextFloat()*6.0
			horizontal := 1.5 + float64(MthSin(math.Pi/2)*radius)
			s.carveEllipsoid(cfg, x+1.0, y, z, horizontal, horizontal*roomYScale, skip)
			tunnels += int(random.NextIntN(4))
		}
		for i := 0; i < tunnels; i++ {
			yaw := random.NextFloat() * 6.2831855
			pitch := (random.NextFloat() - 0.5) / 4.0
			thickness := caveThickness(random)
			branchCount := carveDistance - int(random.NextIntN(carveDistance/4))
			s.caveTunnel(cfg, random.NextLong(), x, y, z, horizontalMul, verticalMul,
				thickness, yaw, pitch, 0, branchCount, 1.0, skip)
		}
	}
}

func caveThickness(r RandomSource) float32 {
	f := r.NextFloat()*2.0 + r.NextFloat()
	if r.NextIntN(10) == 0 {
		f *= r.NextFloat()*r.NextFloat()*3.0 + 1.0
	}
	return f
}

// caveSkip is CaveWorldCarver's CarveSkipChecker: an ellipsoid, with everything
// below the sampled floor level cut flat so tunnels have a floor to walk on.
func caveSkip(floorLevel float64) skipChecker {
	return func(relX, relY, relZ float64, _ int) bool {
		if relY <= floorLevel {
			return true
		}
		return relX*relX+relY*relY+relZ*relZ >= 1.0
	}
}

func (s *carveState) caveTunnel(cfg *carverConfig, seed int64, x, y, z, horizontalMul, verticalMul float64,
	thickness, yaw, pitch float32, branchIndex, branchCount int, yScale float64, skip skipChecker) {
	r := NewLegacy(seed)
	branchAt := int(r.NextIntN(int32(branchCount/2))) + branchCount/4
	gentle := r.NextIntN(6) == 0
	var yawDelta, pitchDelta float32

	for j := branchIndex; j < branchCount; j++ {
		horizontal := 1.5 + float64(MthSin(float64(math.Pi*float32(j)/float32(branchCount)))*thickness)
		vertical := horizontal * yScale
		cosPitch := MthCos(float64(pitch))
		x += float64(MthCos(float64(yaw)) * cosPitch)
		y += float64(MthSin(float64(pitch)))
		z += float64(MthSin(float64(yaw)) * cosPitch)
		if gentle {
			pitch *= 0.92
		} else {
			pitch *= 0.7
		}
		pitch += pitchDelta * 0.1
		yaw += yawDelta * 0.1
		pitchDelta *= 0.9
		yawDelta *= 0.75
		pitchDelta += (r.NextFloat() - r.NextFloat()) * r.NextFloat() * 2.0
		yawDelta += (r.NextFloat() - r.NextFloat()) * r.NextFloat() * 4.0

		if j == branchAt && thickness > 1.0 {
			// Two branches at right angles, and the parent stops. Branch
			// thickness is always below 1, so this never recurses further.
			s.caveTunnel(cfg, r.NextLong(), x, y, z, horizontalMul, verticalMul,
				r.NextFloat()*0.5+0.5, yaw-1.5707964, pitch/3.0, j, branchCount, 1.0, skip)
			s.caveTunnel(cfg, r.NextLong(), x, y, z, horizontalMul, verticalMul,
				r.NextFloat()*0.5+0.5, yaw+1.5707964, pitch/3.0, j, branchCount, 1.0, skip)
			return
		}
		if r.NextIntN(4) == 0 {
			continue // the walk advanced but carves nothing
		}
		if !s.canReach(x, z, j, branchCount, thickness) {
			return
		}
		s.carveEllipsoid(cfg, x, y, z, horizontal*horizontalMul, vertical*verticalMul, skip)
	}
}

// ---- canyon ------------------------------------------------------------

func (s *carveState) carveCanyon(cfg *carverConfig, random *Legacy, sourceX, sourceZ int) {
	x := float64(sourceX<<4 + int(random.NextIntN(16)))
	y := float64(cfg.y.sample(random, s.c.minY, s.c.height))
	z := float64(sourceZ<<4 + int(random.NextIntN(16)))
	yaw := random.NextFloat() * 6.2831855
	pitch := cfg.verticalRotation.sample(random)
	yScale := float64(cfg.yScale.sample(random))
	thickness := cfg.shape.thickness.sample(random)
	branchCount := int(float32(carveDistance) * cfg.shape.distanceFactor.sample(random))
	s.canyonWalk(cfg, random.NextLong(), x, y, z, thickness, yaw, pitch, 0, branchCount, yScale)
}

func (s *carveState) canyonWalk(cfg *carverConfig, seed int64, x, y, z float64,
	thickness, yaw, pitch float32, branchIndex, branchCount int, yScale float64) {
	r := NewLegacy(seed)
	widthFactors := s.canyonWidthFactors(cfg, r)
	skip := canyonSkip(widthFactors, s.c.minY)
	var yawDelta, pitchDelta float32

	for i := branchIndex; i < branchCount; i++ {
		horizontal := 1.5 + float64(MthSin(float64(float32(i)*3.1415927/float32(branchCount)))*thickness)
		vertical := horizontal * yScale
		horizontal *= float64(cfg.shape.horizontalRadiusFactor.sample(r))
		vertical = s.canyonVerticalRadius(cfg, r, vertical, float32(branchCount), float32(i))
		cosPitch := MthCos(float64(pitch))
		sinPitch := MthSin(float64(pitch))
		x += float64(MthCos(float64(yaw)) * cosPitch)
		y += float64(sinPitch)
		z += float64(MthSin(float64(yaw)) * cosPitch)
		pitch *= 0.7
		pitch += pitchDelta * 0.05
		yaw += yawDelta * 0.05
		pitchDelta *= 0.8
		yawDelta *= 0.5
		pitchDelta += (r.NextFloat() - r.NextFloat()) * r.NextFloat() * 2.0
		yawDelta += (r.NextFloat() - r.NextFloat()) * r.NextFloat() * 4.0
		if r.NextIntN(4) == 0 {
			continue
		}
		if !s.canReach(x, z, i, branchCount, thickness) {
			return
		}
		s.carveEllipsoid(cfg, x, y, z, horizontal, vertical, skip)
	}
}

// canyonWidthFactors is initWidthFactors: a per-Y width multiplier that only
// changes every few levels, which is what gives a ravine its ledges. It runs
// once per ravine and consumes the front of the inner random stream.
func (s *carveState) canyonWidthFactors(cfg *carverConfig, r RandomSource) []float32 {
	factors := make([]float32, s.c.height)
	value := float32(1.0)
	for i := range factors {
		if i == 0 || r.NextIntN(int32(cfg.shape.widthSmoothness)) == 0 {
			value = 1.0 + r.NextFloat()*r.NextFloat()
		}
		factors[i] = value * value
	}
	return factors
}

// canyonVerticalRadius is updateVerticalRadius. With the shipped canyon config
// the factor works out to exactly 1, but the draw still happens and removing it
// would shift every subsequent value.
func (s *carveState) canyonVerticalRadius(cfg *carverConfig, r RandomSource, vertical float64, branchCount, index float32) float64 {
	taper := 1.0 - float32(math.Abs(float64(0.5-index/branchCount)))*2.0
	factor := cfg.shape.verticalRadiusDefaultFactor + cfg.shape.verticalRadiusCenterFactor*taper
	return float64(factor) * vertical * float64(randomBetween(r, 0.75, 1.0))
}

func canyonSkip(widthFactors []float32, minY int) skipChecker {
	return func(relX, relY, relZ float64, blockY int) bool {
		index := blockY - minY - 1
		if index < 0 || index >= len(widthFactors) {
			return true
		}
		return (relX*relX+relZ*relZ)*float64(widthFactors[index])+(relY*relY)/6.0 >= 1.0
	}
}

// ---- shared carving ----------------------------------------------------

// skipChecker is CarveSkipChecker: given a position's offset from the ellipsoid
// centre, decide whether to leave it alone.
type skipChecker func(relX, relY, relZ float64, blockY int) bool

// canReach abandons a tunnel once it can no longer reach the chunk being
// carved, even walking straight at it for every remaining step.
func (s *carveState) canReach(x, z float64, branchIndex, branchCount int, thickness float32) bool {
	middleX := float64(s.chunkX<<4 + 8)
	middleZ := float64(s.chunkZ<<4 + 8)
	dx := x - middleX
	dz := z - middleZ
	remaining := float64(branchCount - branchIndex)
	reach := float64(thickness + 2.0 + 16.0)
	return dx*dx+dz*dz-remaining*remaining <= reach*reach
}

// carveEllipsoid cuts one blob out of the target chunk. Everything a carver
// writes goes through here, which is why a tunnel seeded eight chunks away can
// never touch a block outside the chunk being generated.
func (s *carveState) carveEllipsoid(cfg *carverConfig, x, y, z, horizontal, vertical float64, skip skipChecker) {
	middleX := float64(s.chunkX<<4 + 8)
	middleZ := float64(s.chunkZ<<4 + 8)
	bound := 16.0 + horizontal*2.0
	if math.Abs(x-middleX) > bound || math.Abs(z-middleZ) > bound {
		return
	}
	minBlockX := s.chunkX << 4
	minBlockZ := s.chunkZ << 4
	x0 := max(mthFloor(x-horizontal)-minBlockX-1, 0)
	x1 := min(mthFloor(x+horizontal)-minBlockX, 15)
	z0 := max(mthFloor(z-horizontal)-minBlockZ-1, 0)
	z1 := min(mthFloor(z+horizontal)-minBlockZ, 15)
	// The seven-block margin below the world roof is vanilla's, for chunks that
	// are not being upgraded from an older world.
	yLo := max(mthFloor(y-vertical)-1, s.c.minY+1)
	yHi := min(mthFloor(y+vertical)+1, s.c.minY+s.c.height-1-7)

	for lx := x0; lx <= x1; lx++ {
		blockX := minBlockX + lx
		relX := (float64(blockX) + 0.5 - x) / horizontal
		for lz := z0; lz <= z1; lz++ {
			blockZ := minBlockZ + lz
			relZ := (float64(blockZ) + 0.5 - z) / horizontal
			if relX*relX+relZ*relZ >= 1.0 {
				continue
			}
			// Reset per column: a tunnel that breaks the surface in one column
			// has not broken it in the next.
			reachedSurface := false
			for by := yHi; by > yLo; by-- {
				relY := (float64(by) - 0.5 - y) / vertical
				if skip(relX, relY, relZ, by) {
					continue
				}
				if s.mask.get(lx, by, lz) {
					continue
				}
				s.mask.set(lx, by, lz)
				s.carveBlock(cfg, lx, by, lz, blockX, blockZ, &reachedSurface)
			}
		}
	}
}

func (s *carveState) carveBlock(cfg *carverConfig, lx, by, lz, blockX, blockZ int, reachedSurface *bool) {
	old := s.target.Block(lx, by, lz)
	if isSurfaceTop(old) {
		*reachedSurface = true
	}
	if !s.target.Replaceable(old) {
		return
	}
	carved, ok := s.carveStateAt(blockX, by, blockZ)
	if !ok {
		return
	}
	s.target.SetBlock(lx, by, lz, carved)
	if !*reachedSurface {
		return
	}
	// Cutting a grass block away leaves plain dirt showing. Vanilla re-runs the
	// surface rule on it so a cave mouth in a podzol forest is not a dirt scar.
	if s.target.Block(lx, by-1, lz) != blockDirt {
		return
	}
	if top, ok := s.target.TopMaterial(lx, by-1, lz, isCarvedFluid(carved)); ok {
		s.target.SetBlock(lx, by-1, lz, top)
	}
}

// carveStateAt is getCarveState: lava below the configured level, otherwise
// whatever the aquifer would put in an empty position. A nil answer from the
// aquifer means the rock stays.
func (s *carveState) carveStateAt(x, y, z int) (uint16, bool) {
	if y <= s.lavaY {
		return blockLava, true
	}
	if s.aq == nil {
		return blockAir, true
	}
	return s.aq.ComputeSubstance(x, y, z, 0.0)
}

// Block states the carver compares against directly. Grass and mycelium are
// block-level tests in vanilla, so every state counts.
const (
	blockDirt          uint16 = 10
	blockGrassSnowy    uint16 = 8
	blockGrass         uint16 = 9
	blockMyceliumSnowy uint16 = 8918
	blockMycelium      uint16 = 8919
)

func isSurfaceTop(state uint16) bool {
	switch state {
	case blockGrass, blockGrassSnowy, blockMycelium, blockMyceliumSnowy:
		return true
	}
	return false
}

func isCarvedFluid(state uint16) bool { return state == blockWater || state == blockLava }
