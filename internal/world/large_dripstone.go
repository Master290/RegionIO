package world

import (
	"fmt"
	"math"

	"regionio/internal/worldgen"
)

// placeLargeDripstone is LargeDripstoneFeature.place, transcribed from javap -p -c
// against versions/26.1.2/server-26.1.2.jar for LargeDripstoneFeature,
// LargeDripstoneFeature$LargeDripstone, LargeDripstoneFeature$WindOffsetter,
// DripstoneUtils, and Column.
func (r *decorationRegion) placeLargeDripstone(set *worldgen.FeatureSet, random worldgen.RandomSource, origin worldgen.FeaturePosition, config worldgen.LargeDripstoneFeatureConfig) error {
	dripstoneState, ok := nameToStateID("minecraft:dripstone_block", nil)
	if !ok {
		return fmt.Errorf("world: minecraft:dripstone_block is not a known block state")
	}
	if !isDripstoneEmptyOrWater(r.getBlock(origin.X, origin.Y, origin.Z)) {
		return nil
	}
	ceiling, hasCeiling := r.dripstoneScanDirection(set, origin.X, origin.Y, origin.Z, config.FloorToCeilingSearchRange, 1)
	floor, hasFloor := r.dripstoneScanDirection(set, origin.X, origin.Y, origin.Z, config.FloorToCeilingSearchRange, -1)
	if !hasCeiling || !hasFloor {
		return nil
	}
	rangeHeight := ceiling - floor - 1
	if rangeHeight < 4 {
		return nil
	}
	maxRadByHeight := int(float32(rangeHeight) * config.MaxColumnRadiusToCaveHeightRatio)
	maxRad := min(max(maxRadByHeight, config.ColumnRadiusMin), config.ColumnRadiusMax)
	radius := int(random.NextIntN(int32(maxRad-config.ColumnRadiusMin+1))) + config.ColumnRadiusMin

	stalactite := makeLargeDripstone(origin.X, ceiling-1, origin.Z, false, random, radius, config.StalactiteBluntness, config.HeightScale)
	stalagmite := makeLargeDripstone(origin.X, floor+1, origin.Z, true, random, radius, config.StalagmiteBluntness, config.HeightScale)

	var wind largeDripstoneWind
	if stalactite.isSuitableForWind(config) && stalagmite.isSuitableForWind(config) {
		speed := config.WindSpeed.Sample(random)
		angle := random.NextFloat() * float32(3.1415927)
		wind = largeDripstoneWind{
			active:  true,
			originY: origin.Y,
			speedX:  float64(worldgen.MthCos(float64(angle)) * speed),
			speedZ:  float64(worldgen.MthSin(float64(angle)) * speed),
		}
	}

	okStalactite := stalactite.moveBack(r, wind)
	okStalagmite := stalagmite.moveBack(r, wind)
	if okStalactite {
		stalactite.placeBlocks(r, set, random, wind, dripstoneState)
	}
	if okStalagmite {
		stalagmite.placeBlocks(r, set, random, wind, dripstoneState)
	}
	return nil
}

func (r *decorationRegion) dripstoneScanDirection(set *worldgen.FeatureSet, x, startY, z, maxDistance, dy int) (int, bool) {
	y := startY
	for i := 1; i < maxDistance; i++ {
		if !isDripstoneEmptyOrWater(r.getBlock(x, y, z)) {
			break
		}
		y += dy
	}
	if isDripstoneBaseOrLava(set, r.getBlock(x, y, z)) {
		return y, true
	}
	return 0, false
}

type largeDripstoneColumn struct {
	rootX, rootY, rootZ int
	pointingUp          bool
	radius              int
	bluntness           float64
	scale               float64
}

func makeLargeDripstone(x, y, z int, pointingUp bool, random worldgen.RandomSource, radius int, bluntnessProvider, scaleProvider worldgen.UniformFloatProvider) largeDripstoneColumn {
	bluntness := float64(bluntnessProvider.Sample(random))
	scale := float64(scaleProvider.Sample(random))
	return largeDripstoneColumn{
		rootX:      x,
		rootY:      y,
		rootZ:      z,
		pointingUp: pointingUp,
		radius:     radius,
		bluntness:  bluntness,
		scale:      scale,
	}
}

func (d *largeDripstoneColumn) isSuitableForWind(config worldgen.LargeDripstoneFeatureConfig) bool {
	return d.radius >= config.MinRadiusForWind && d.bluntness >= float64(config.MinBluntnessForWind)
}

func (d *largeDripstoneColumn) getHeight() int {
	return d.getHeightAtRadius(0)
}

func (d *largeDripstoneColumn) getHeightAtRadius(r float32) int {
	return int(getDripstoneHeight(float64(r), float64(d.radius), d.scale, d.bluntness))
}

// getDripstoneHeight is DripstoneUtils.getDripstoneHeight.
func getDripstoneHeight(r, maxRadius, scale, bluntness float64) float64 {
	if r < bluntness {
		r = bluntness
	}
	const magic = 0.384
	x := r / maxRadius * magic
	t1 := 0.75 * math.Pow(x, 1.3333333333333333)
	t2 := math.Pow(x, 0.6666666666666666)
	t3 := 0.3333333333333333 * math.Log(x)
	h := scale * (t1 - t2 - t3)
	if h < 0 {
		h = 0
	}
	return h / magic * maxRadius
}

func (d *largeDripstoneColumn) moveBack(r *decorationRegion, wind largeDripstoneWind) bool {
	for d.radius > 1 {
		posY := d.rootY
		maxSteps := min(10, d.getHeight())
		for i := 0; i < maxSteps; i++ {
			if name, ok := stateByID(r.getBlock(d.rootX, posY, d.rootZ)); ok && name.Name == "minecraft:lava" {
				return false
			}
			ox, oy, oz := wind.offset(d.rootX, posY, d.rootZ)
			if r.isCircleMostlyEmbeddedInStone(ox, oy, oz, d.radius) {
				d.rootY = posY
				return true
			}
			if d.pointingUp {
				posY--
			} else {
				posY++
			}
		}
		d.radius /= 2
	}
	return false
}

// isCircleMostlyEmbeddedInStone is DripstoneUtils.isCircleMostlyEmbeddedInStone.
func (r *decorationRegion) isCircleMostlyEmbeddedInStone(x, y, z, radius int) bool {
	if isDripstoneEmptyOrWaterOrLava(r.getBlock(x, y, z)) {
		return false
	}
	step := float32(6.0) / float32(radius)
	for angle := float32(0.0); angle < float32(6.2831855); angle += step {
		dx := int(worldgen.MthCos(float64(angle)) * float32(radius))
		dz := int(worldgen.MthSin(float64(angle)) * float32(radius))
		if isDripstoneEmptyOrWaterOrLava(r.getBlock(x+dx, y, z+dz)) {
			return false
		}
	}
	return true
}

func (d *largeDripstoneColumn) placeBlocks(r *decorationRegion, set *worldgen.FeatureSet, random worldgen.RandomSource, wind largeDripstoneWind, dripstoneState uint16) {
	for dx := -d.radius; dx <= d.radius; dx++ {
		for dz := -d.radius; dz <= d.radius; dz++ {
			dist := float32(math.Sqrt(float64(float32(dx*dx + dz*dz))))
			if dist > float32(d.radius) {
				continue
			}
			height := d.getHeightAtRadius(dist)
			if height <= 0 {
				continue
			}
			if float64(random.NextFloat()) < 0.2 {
				scale := random.NextFloat()*float32(1.0-0.8) + float32(0.8)
				height = int(float32(height) * scale)
			}
			px := d.rootX + dx
			py := d.rootY
			pz := d.rootZ + dz
			placedAny := false
			maxY := math.MaxInt32
			if d.pointingUp {
				maxY = r.heightAt("WORLD_SURFACE_WG", px, pz)
			}
			for i := 0; i < height; i++ {
				if py >= maxY {
					break
				}
				ox, oy, oz := wind.offset(px, py, pz)
				state := r.getBlock(ox, oy, oz)
				if isDripstoneEmptyOrWaterOrLava(state) {
					placedAny = true
					r.setBlock(ox, oy, oz, dripstoneState)
				} else if placedAny {
					if name, ok := stateByID(state); ok && flattenBlockTagContains(set, "minecraft:base_stone_overworld", name.Name) {
						break
					}
				}
				if d.pointingUp {
					py++
				} else {
					py--
				}
			}
		}
	}
}

type largeDripstoneWind struct {
	active         bool
	originY        int
	speedX, speedZ float64
}

func (w largeDripstoneWind) offset(x, y, z int) (int, int, int) {
	if !w.active {
		return x, y, z
	}
	dy := float64(w.originY - y)
	vx := w.speedX * dy
	vz := w.speedZ * dy
	return x + floorFloat64(vx), y, z + floorFloat64(vz)
}

func isDripstoneEmptyOrWater(id uint16) bool {
	if isAirState(id) {
		return true
	}
	name, ok := stateByID(id)
	return ok && name.Name == "minecraft:water"
}

func isDripstoneEmptyOrWaterOrLava(id uint16) bool {
	if isAirState(id) {
		return true
	}
	name, ok := stateByID(id)
	return ok && (name.Name == "minecraft:water" || name.Name == "minecraft:lava")
}

func isDripstoneBaseOrLava(set *worldgen.FeatureSet, id uint16) bool {
	name, ok := stateByID(id)
	if !ok {
		return false
	}
	if name.Name == "minecraft:dripstone_block" || name.Name == "minecraft:lava" {
		return true
	}
	return flattenBlockTagContains(set, "minecraft:dripstone_replaceable_blocks", name.Name)
}
