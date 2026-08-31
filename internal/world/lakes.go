package world

import (
	"encoding/json"
	"fmt"

	"regionio/internal/worldgen"
)

// lakes.go ports net.minecraft.world.level.levelgen.feature.LakeFeature, the
// stage-1 local-modifications feature behind lake_lava_underground and
// lake_lava_surface (26.1.2 has no water lake configured feature left, so the
// freeze pass at the end of vanilla's place() never fires here).
//
// The lakes matter beyond their own lava: they are the first air-writing
// feature in the schedule, so the monster rooms at stage 3 validate their
// wall openings against a world the lakes already carved — the fixture's
// dungeon at (-6,36,-19) exists only because a lava lake opened its wall.
//
// The port follows the bytecode draw-for-draw: the blob ellipsoids (six
// nextDouble per blob after one nextInt(4) for the blob count), the
// validation pass (no draws), the carve pass (no draws; cave air above the
// fluid line, fluid below), and the stone barrier pass whose upper edge
// cells roll nextInt(2) before the solid check.

const localModificationsStage = 1

// lakeFeatureConfig is the resolved LakeFeature.Configuration: the fluid the
// lower half fills with and the barrier state the rim is lined with.
type lakeFeatureConfig struct {
	Fluid   uint16
	Barrier uint16
}

type lakeStateProviderJSON struct {
	State struct {
		Name       string            `json:"Name"`
		Properties map[string]string `json:"Properties"`
	} `json:"state"`
}

// placeScheduledLakes replays the stage-1 lake features from one source
// center into the mutable decoration region.
func (r *decorationRegion) placeScheduledLakes(seed int64) error {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		return err
	}
	if err := r.ensureSourceNeighborhood(); err != nil {
		return err
	}
	schedule, err := set.FeatureSchedule(possibleBiomeOrder(), r.sourceBiomes(), localModificationsStage)
	if err != nil {
		return err
	}
	initMonsterRoomTables() // monsterIsSolid / the features_cannot_replace table
	lakeCannotReplace := lakeTagIDs(set, "minecraft:features_cannot_replace")
	lavaPoolStone := lakeTagIDs(set, "minecraft:lava_pool_stone_cannot_replace")
	random, decorationSeed := worldgen.DecorationRandom(seed, int(r.sourceX), int(r.sourceZ))
	origin := worldgen.FeaturePosition{X: int(r.sourceX) << 4, Y: MinY, Z: int(r.sourceZ) << 4}
	for _, scheduled := range schedule {
		placed, ok := set.Placed[scheduled.Name]
		if !ok {
			continue
		}
		configured, ok := set.Configured[placed.Feature]
		if !ok || configured.Type != "minecraft:lake" {
			continue
		}
		config, err := parseLakeConfig(configured.Config)
		if err != nil {
			return fmt.Errorf("world: lake feature %s: %w", placed.Feature, err)
		}
		random.SetFeatureSeed(decorationSeed, scheduled.Index, localModificationsStage)
		context := r.placementContext(func(position worldgen.FeaturePosition) bool {
			return r.biomeAllowsFeature(set, scheduled.Name, localModificationsStage, position)
		})
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			placeLake(r, random, position.X, position.Y, position.Z, config, lakeCannotReplace, lavaPoolStone)
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func parseLakeConfig(raw json.RawMessage) (lakeFeatureConfig, error) {
	var doc struct {
		Fluid   lakeStateProviderJSON `json:"fluid"`
		Barrier lakeStateProviderJSON `json:"barrier"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return lakeFeatureConfig{}, err
	}
	stateByIDOnce.Do(buildStateTable)
	fluid, ok := nameToStateID(doc.Fluid.State.Name, doc.Fluid.State.Properties)
	if !ok {
		return lakeFeatureConfig{}, fmt.Errorf("missing fluid state %s", doc.Fluid.State.Name)
	}
	barrier, ok := nameToStateID(doc.Barrier.State.Name, doc.Barrier.State.Properties)
	if !ok {
		return lakeFeatureConfig{}, fmt.Errorf("missing barrier state %s", doc.Barrier.State.Name)
	}
	return lakeFeatureConfig{Fluid: fluid, Barrier: barrier}, nil
}

// isLiquidBlockState mirrors BlockState.liquid(): a block-level property true
// only for real fluid blocks (water, lava) — a waterlogged chest is NOT
// liquid, the same distinction FallingBlock.isFree makes.
func isLiquidBlockState(state uint16) bool {
	if state == 0 {
		return false
	}
	n, ok := stateByID(state)
	return ok && (n.Name == "minecraft:water" || n.Name == "minecraft:lava")
}

// placeLake ports LakeFeature.place for one origin. The volume is 16 x 8 x 16
// (x, y vertical, z) anchored at origin + (-8, -4, -8); index (x*16+z)*8+y.
func placeLake(r *decorationRegion, random worldgen.RandomSource, ox, oy, oz int, config lakeFeatureConfig, cannotReplace, lavaPoolStone map[uint16]bool) bool {
	if oy <= MinY+4 {
		return false
	}
	bx, by, bz := ox-8, oy-4, oz-8

	// Blob ellipsoids: 4..7 blobs, six doubles each in rx/ry/rz then
	// cx/cy/cz order, carving every in-ellipsoid cell of the interior.
	var carve [16 * 16 * 8]bool
	blobs := int(random.NextIntN(4)) + 4
	for i := 0; i < blobs; i++ {
		rx := random.NextDouble()*6.0 + 3.0
		ry := random.NextDouble()*4.0 + 2.0
		rz := random.NextDouble()*6.0 + 3.0
		cx := random.NextDouble()*(16.0-rx-2.0) + rx/2.0 + 1.0
		cy := random.NextDouble()*(8.0-ry-4.0) + 2.0 + ry/2.0
		cz := random.NextDouble()*(16.0-rz-2.0) + rz/2.0 + 1.0
		for x := 1; x < 15; x++ {
			for z := 1; z < 15; z++ {
				for y := 1; y < 7; y++ {
					nx := (float64(x) - cx) / (rx / 2.0)
					ny := (float64(y) - cy) / (ry / 2.0)
					nz := (float64(z) - cz) / (rz / 2.0)
					if nx*nx+ny*ny+nz*nz < 1.0 {
						carve[(x*16+z)*8+y] = true
					}
				}
			}
		}
	}

	// Validation over non-carved cells adjacent to a carved one: no liquid
	// above the fluid line, no holes below it. No draws.
	edge := func(x, z, y int) bool {
		return (x < 15 && carve[((x+1)*16+z)*8+y]) ||
			(x > 0 && carve[((x-1)*16+z)*8+y]) ||
			(z < 15 && carve[(x*16+z+1)*8+y]) ||
			(z > 0 && carve[(x*16+z-1)*8+y]) ||
			(y < 7 && carve[(x*16+z)*8+y+1]) ||
			(y > 0 && carve[(x*16+z)*8+y-1])
	}
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			for y := 0; y < 8; y++ {
				if carve[(x*16+z)*8+y] || !edge(x, z, y) {
					continue
				}
				state := r.getBlock(bx+x, by+y, bz+z)
				if y >= 4 && isLiquidBlockState(state) {
					return false
				}
				if y < 4 && !monsterIsSolid(state) && state != config.Fluid {
					return false
				}
			}
		}
	}

	// Carve pass: replaceable carved cells become the fluid below the line
	// and cave air above it. Vanilla also schedules an air tick and marks
	// the column for post-processing; neither changes block states.
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			for y := 0; y < 8; y++ {
				if !carve[(x*16+z)*8+y] {
					continue
				}
				px, py, pz := bx+x, by+y, bz+z
				if cannotReplace[r.getBlock(px, py, pz)] {
					continue
				}
				if y >= 4 {
					r.setBlock(px, py, pz, monsterCaveAirID)
				} else {
					r.setBlock(px, py, pz, config.Fluid)
				}
			}
		}
	}

	// Barrier pass: rim cells (non-carved, carved-adjacent) become the
	// barrier stone when solid and not in #lava_pool_stone_cannot_replace.
	// Upper rim cells only line 50% of the time; the roll happens before
	// the solid check, exactly like vanilla.
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			for y := 0; y < 8; y++ {
				if carve[(x*16+z)*8+y] || !edge(x, z, y) {
					continue
				}
				if y >= 4 && random.NextIntN(2) == 0 {
					continue
				}
				px, py, pz := bx+x, by+y, bz+z
				state := r.getBlock(px, py, pz)
				if !monsterIsSolid(state) || lavaPoolStone[state] {
					continue
				}
				r.setBlock(px, py, pz, config.Barrier)
			}
		}
	}
	return true
}

func lakeTagIDs(set *worldgen.FeatureSet, tag string) map[uint16]bool {
	ids := make(map[uint16]bool)
	for _, name := range flattenBlockTag(set, tag, nil) {
		if id, ok := nameToStateID(name, nil); ok {
			ids[id] = true
		}
	}
	return ids
}
