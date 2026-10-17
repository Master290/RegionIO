package world

import (
	"math"
	"math/rand"
	"strings"
)

// Vanilla 26.1.2 explosion constants.
const (
	// ExplosionGridSize is the 16x16x16 cube perimeter resolution used by vanilla ServerExplosion.
	ExplosionGridSize = 16
	// ExplosionRayStep is the step distance (0.3 blocks) along each explosion ray.
	ExplosionRayStep = 0.3
	// ExplosionStepAttenuation is the constant ray intensity loss per 0.3-block step (0.3 * 0.75 = 0.225).
	ExplosionStepAttenuation = 0.225

	// Standard vanilla explosion radii.
	TNTExplosionRadius           = 4.0
	CreeperExplosionRadius       = 3.0
	ChargedCreeperExplosionRadius = 6.0
	EndCrystalExplosionRadius    = 6.0
	BedRespawnExplosionRadius    = 5.0
	GhastFireballExplosionRadius = 1.0
	WitherSpawnExplosionRadius   = 7.0
	WitherSkullExplosionRadius   = 1.0

	// UnbreakableBlastResistance is the blast resistance of bedrock, barriers, and portal frames.
	UnbreakableBlastResistance = 3600000.0
	// ObsidianBlastResistance is the blast resistance of obsidian, crying obsidian, ancient debris, anvils, and enchanting tables.
	ObsidianBlastResistance = 1200.0
	// FluidBlastResistance is the blast resistance of water and lava source/flowing blocks.
	FluidBlastResistance = 100.0
)

// AABB represents an axis-aligned 3D bounding box in world coordinates.
type AABB struct {
	MinX, MinY, MinZ float64
	MaxX, MaxY, MaxZ float64
}

// WidthX returns the X extent of the bounding box.
func (b AABB) WidthX() float64 { return b.MaxX - b.MinX }

// HeightY returns the Y extent of the bounding box.
func (b AABB) HeightY() float64 { return b.MaxY - b.MinY }

// WidthZ returns the Z extent of the bounding box.
func (b AABB) WidthZ() float64 { return b.MaxZ - b.MinZ }

// CenterBottom returns the bottom-center point (entity feet position) of the box.
func (b AABB) CenterBottom() (x, y, z float64) {
	return (b.MinX + b.MaxX) * 0.5, b.MinY, (b.MinZ + b.MaxZ) * 0.5
}

// EyePos returns the approximate eye position (0.85 of height) of the box.
func (b AABB) EyePos() (x, y, z float64) {
	return (b.MinX + b.MaxX) * 0.5, b.MinY + b.HeightY()*0.85, (b.MinZ + b.MaxZ) * 0.5
}

// PlayerAABB constructs a standard standing player bounding box (0.6 x 1.8 x 0.6) centered at (x, y, z).
func PlayerAABB(x, y, z float64) AABB {
	return AABB{
		MinX: x - 0.3,
		MinY: y,
		MinZ: z - 0.3,
		MaxX: x + 0.3,
		MaxY: y + 1.8,
		MaxZ: z + 0.3,
	}
}

// EntityAABB constructs an entity bounding box of width w and height h centered at (x, y, z).
func EntityAABB(x, y, z, w, h float64) AABB {
	if w <= 0 {
		w = 0.6
	}
	if h <= 0 {
		h = 1.8
	}
	hw := w * 0.5
	return AABB{
		MinX: x - hw,
		MinY: y,
		MinZ: z - hw,
		MaxX: x + hw,
		MaxY: y + h,
		MaxZ: z + hw,
	}
}

type vec3 struct {
	x, y, z float64
}

// explosionRayDirs holds the 1352 unit direction vectors pointing from the center
// of a 16x16x16 grid to every cell on its outer boundary, matching vanilla ServerExplosion.
var explosionRayDirs = buildExplosionRayDirs()

func buildExplosionRayDirs() []vec3 {
	dirs := make([]vec3, 0, 1352)
	const maxIdx = ExplosionGridSize - 1
	for i := 0; i < ExplosionGridSize; i++ {
		for j := 0; j < ExplosionGridSize; j++ {
			for k := 0; k < ExplosionGridSize; k++ {
				if i != 0 && i != maxIdx && j != 0 && j != maxIdx && k != 0 && k != maxIdx {
					continue
				}
				dx := float64(i)/float64(maxIdx)*2.0 - 1.0
				dy := float64(j)/float64(maxIdx)*2.0 - 1.0
				dz := float64(k)/float64(maxIdx)*2.0 - 1.0
				length := math.Sqrt(dx*dx + dy*dy + dz*dz)
				if length <= 0 {
					continue
				}
				dirs = append(dirs, vec3{
					x: dx / length,
					y: dy / length,
					z: dz / length,
				})
			}
		}
	}
	return dirs
}

// ExplosionRayDirectionsCount returns the number of canonical explosion ray directions (1352).
func ExplosionRayDirectionsCount() int {
	return len(explosionRayDirs)
}

// BlockBlastResistance returns the vanilla 26.1.2 explosion resistance of state.
// Waterlogged blocks have at least FluidBlastResistance (100.0).
func BlockBlastResistance(state uint16) float64 {
	if isAirState(state) {
		return 0.0
	}
	if state == StateBedrock {
		return UnbreakableBlastResistance
	}
	if state == StateWater || state == StateLava {
		return FluidBlastResistance
	}
	info, ok := stateByID(state)
	if !ok {
		return 6.0
	}
	base := blockBlastResistanceByName(info.Name)
	if info.Properties["waterlogged"] == "true" && base < FluidBlastResistance {
		return FluidBlastResistance
	}
	return base
}

func blockBlastResistanceByName(name string) float64 {
	switch name {
	case "minecraft:air", "minecraft:cave_air", "minecraft:void_air":
		return 0.0

	// Unbreakable blocks (3,600,000)
	case "minecraft:bedrock", "minecraft:barrier", "minecraft:end_portal_frame",
		"minecraft:end_portal", "minecraft:end_gateway", "minecraft:command_block",
		"minecraft:chain_command_block", "minecraft:repeating_command_block",
		"minecraft:structure_block", "minecraft:structure_void", "minecraft:jigsaw",
		"minecraft:light", "minecraft:moving_piston", "minecraft:reinforced_deepslate",
		"minecraft:test_block", "minecraft:test_instance_block":
		return UnbreakableBlastResistance

	// Obsidian & heavy blast-proof tier (1,200)
	case "minecraft:obsidian", "minecraft:crying_obsidian", "minecraft:ancient_debris",
		"minecraft:netherite_block", "minecraft:respawn_anchor", "minecraft:enchanting_table",
		"minecraft:anvil", "minecraft:chipped_anvil", "minecraft:damaged_anvil":
		return ObsidianBlastResistance

	// Ender chest (600)
	case "minecraft:ender_chest":
		return 600.0

	// Fluids (100)
	case "minecraft:water", "minecraft:lava", "minecraft:bubble_column":
		return FluidBlastResistance

	// End stone (9.0)
	case "minecraft:end_stone", "minecraft:end_stone_bricks", "minecraft:end_stone_brick_stairs", "minecraft:end_stone_brick_slab", "minecraft:end_stone_brick_wall":
		return 9.0

	// Zero-resistance blocks (0.0)
	case "minecraft:tnt", "minecraft:fire", "minecraft:soul_fire",
		"minecraft:torch", "minecraft:wall_torch", "minecraft:soul_torch", "minecraft:soul_wall_torch",
		"minecraft:redstone_torch", "minecraft:redstone_wall_torch", "minecraft:copper_torch", "minecraft:copper_wall_torch",
		"minecraft:redstone_wire", "minecraft:repeater", "minecraft:comparator",
		"minecraft:lever", "minecraft:tripwire", "minecraft:tripwire_hook",
		"minecraft:short_grass", "minecraft:tall_grass", "minecraft:fern", "minecraft:large_fern",
		"minecraft:dead_bush", "minecraft:sugar_cane", "minecraft:vine", "minecraft:glow_lichen",
		"minecraft:lily_pad", "minecraft:scaffolding", "minecraft:decorated_pot",
		"minecraft:wheat", "minecraft:carrots", "minecraft:potatoes", "minecraft:beetroots",
		"minecraft:melon_stem", "minecraft:pumpkin_stem", "minecraft:attached_melon_stem", "minecraft:attached_pumpkin_stem",
		"minecraft:nether_wart", "minecraft:sweet_berry_bush", "minecraft:Slime_block", "minecraft:slime_block", "minecraft:honey_block",
		"minecraft:flower_pot", "minecraft:brown_mushroom", "minecraft:red_mushroom",
		"minecraft:crimson_fungus", "minecraft:warped_fungus", "minecraft:crimson_roots", "minecraft:warped_roots",
		"minecraft:nether_sprouts", "minecraft:spore_blossom", "minecraft:hanging_roots":
		return 0.0

	// Very fragile blocks (0.1 - 0.5)
	case "minecraft:snow", "minecraft:candle":
		return 0.1
	case "minecraft:snow_block", "minecraft:powder_snow", "minecraft:daylight_detector", "minecraft:bed":
		return 0.2
	case "minecraft:glass", "minecraft:tinted_glass", "minecraft:glass_pane",
		"minecraft:glowstone", "minecraft:sea_lantern", "minecraft:redstone_lamp",
		"minecraft:beacon", "minecraft:conduit", "minecraft:cactus":
		return 0.3
	case "minecraft:netherrack", "minecraft:ladder":
		return 0.4
	case "minecraft:dirt", "minecraft:coarse_dirt", "minecraft:podzol", "minecraft:rooted_dirt",
		"minecraft:mycelium", "minecraft:dirt_path", "minecraft:farmland", "minecraft:mud",
		"minecraft:sand", "minecraft:red_sand", "minecraft:gravel", "minecraft:soul_sand",
		"minecraft:soul_soil", "minecraft:magma_block", "minecraft:hay_block", "minecraft:target",
		"minecraft:dried_kelp_block", "minecraft:cake", "minecraft:brewing_stand",
		"minecraft:ice", "minecraft:packed_ice", "minecraft:frosted_ice":
		return 0.5
	case "minecraft:grass_block", "minecraft:clay", "minecraft:sponge", "minecraft:wet_sponge",
		"minecraft:honeycomb_block", "minecraft:beehive", "minecraft:bee_nest", "minecraft:composter":
		return 0.6
	case "minecraft:rail", "minecraft:powered_rail", "minecraft:detector_rail", "minecraft:activator_rail":
		return 0.7
	case "minecraft:sandstone", "minecraft:red_sandstone", "minecraft:chiseled_sandstone",
		"minecraft:cut_sandstone", "minecraft:smooth_sandstone", "minecraft:quartz_block",
		"minecraft:note_block", "minecraft:calcite", "minecraft:tuff", "minecraft:moss_block", "minecraft:pale_moss_block":
		return 0.8
	case "minecraft:melon", "minecraft:pumpkin", "minecraft:carved_pumpkin", "minecraft:jack_o_lantern",
		"minecraft:nether_wart_block", "minecraft:warped_wart_block", "minecraft:shroomlight":
		return 1.0
	case "minecraft:bookshelf", "minecraft:chiseled_bookshelf":
		return 1.5
	case "minecraft:bone_block", "minecraft:campfire", "minecraft:soul_campfire", "minecraft:cauldron",
		"minecraft:water_cauldron", "minecraft:lava_cauldron", "minecraft:powder_snow_cauldron":
		return 2.0
	case "minecraft:chest", "minecraft:trapped_chest", "minecraft:barrel", "minecraft:crafting_table",
		"minecraft:cartography_table", "minecraft:fletching_table", "minecraft:smithing_table",
		"minecraft:loom", "minecraft:lectern":
		return 2.5
	case "minecraft:furnace", "minecraft:smoker", "minecraft:blast_furnace",
		"minecraft:dispenser", "minecraft:dropper", "minecraft:crafter",
		"minecraft:observer", "minecraft:piston", "minecraft:sticky_piston", "minecraft:piston_head",
		"minecraft:stonecutter", "minecraft:lodestone":
		return 3.5
	case "minecraft:terracotta", "minecraft:basalt", "minecraft:polished_basalt", "minecraft:smooth_basalt":
		return 4.2
	case "minecraft:hopper":
		return 4.8
	case "minecraft:coal_block", "minecraft:redstone_block", "minecraft:lapis_block",
		"minecraft:iron_block", "minecraft:gold_block", "minecraft:diamond_block",
		"minecraft:emerald_block", "minecraft:copper_block", "minecraft:raw_iron_block",
		"minecraft:raw_gold_block", "minecraft:raw_copper_block",
		"minecraft:iron_bars", "minecraft:iron_door", "minecraft:iron_trapdoor", "minecraft:chain",
		"minecraft:bell", "minecraft:grindstone", "minecraft:jukebox", "minecraft:spawner",
		"minecraft:stone", "minecraft:cobblestone", "minecraft:mossy_cobblestone",
		"minecraft:smooth_stone", "minecraft:stone_bricks", "minecraft:mossy_stone_bricks",
		"minecraft:cracked_stone_bricks", "minecraft:chiseled_stone_bricks",
		"minecraft:granite", "minecraft:polished_granite", "minecraft:diorite", "minecraft:polished_diorite",
		"minecraft:andesite", "minecraft:polished_andesite",
		"minecraft:deepslate", "minecraft:cobbled_deepslate", "minecraft:polished_deepslate",
		"minecraft:deepslate_bricks", "minecraft:deepslate_tiles", "minecraft:chiseled_deepslate",
		"minecraft:bricks", "minecraft:nether_bricks", "minecraft:red_nether_bricks",
		"minecraft:blackstone", "minecraft:polished_blackstone", "minecraft:polished_blackstone_bricks",
		"minecraft:prismarine", "minecraft:prismarine_bricks", "minecraft:dark_prismarine",
		"minecraft:purpur_block", "minecraft:purpur_pillar", "minecraft:shulker_box":
		return 6.0
	}

	switch {
	case strings.HasSuffix(name, "_button") || strings.HasSuffix(name, "_pressure_plate") ||
		strings.HasSuffix(name, "_sapling") || strings.HasSuffix(name, "_tulip") ||
		strings.HasSuffix(name, "_flower") || strings.HasSuffix(name, "_coral") ||
		strings.HasSuffix(name, "_coral_fan") || strings.HasSuffix(name, "_coral_wall_fan"):
		return 0.0
	case strings.HasSuffix(name, "_carpet") || strings.HasSuffix(name, "_candle"):
		return 0.1
	case strings.HasSuffix(name, "_leaves") || strings.HasSuffix(name, "_bed"):
		return 0.2
	case strings.HasSuffix(name, "_stained_glass") || strings.HasSuffix(name, "_ stained_glass_pane") ||
		strings.HasSuffix(name, "_stained_glass_pane"):
		return 0.3
	case strings.HasSuffix(name, "_concrete_powder"):
		return 0.5
	case strings.HasSuffix(name, "_wool") || strings.HasSuffix(name, "_banner") || strings.HasSuffix(name, "_wall_banner"):
		return 0.8
	case strings.HasSuffix(name, "_sign") || strings.HasSuffix(name, "_wall_sign") ||
		strings.HasSuffix(name, "_hanging_sign") || strings.HasSuffix(name, "_wall_hanging_sign"):
		return 1.0
	case strings.HasSuffix(name, "_concrete"):
		return 1.8
	case strings.HasSuffix(name, "_log") || strings.HasSuffix(name, "_wood") ||
		strings.HasSuffix(name, "_stem") || strings.HasSuffix(name, "_hyphae"):
		return 2.0
	case strings.HasSuffix(name, "_planks") || strings.HasSuffix(name, "_fence") ||
		strings.HasSuffix(name, "_fence_gate") || strings.HasSuffix(name, "_door") ||
		strings.HasSuffix(name, "_trapdoor") || strings.HasSuffix(name, "_ore"):
		return 3.0
	case strings.HasSuffix(name, "_terracotta"):
		return 4.2
	case strings.HasSuffix(name, "_shulker_box") || strings.HasSuffix(name, "copper") ||
		strings.HasSuffix(name, "_stairs") || strings.HasSuffix(name, "_slab") || strings.HasSuffix(name, "_wall"):
		if isWoodenMaterialName(name) {
			return 3.0
		}
		return 6.0
	default:
		return 6.0
	}
}

func isWoodenMaterialName(name string) bool {
	for _, prefix := range []string{
		"minecraft:oak_", "minecraft:spruce_", "minecraft:birch_", "minecraft:jungle_",
		"minecraft:acacia_", "minecraft:dark_oak_", "minecraft:mangrove_", "minecraft:cherry_",
		"minecraft:pale_oak_", "minecraft:bamboo_", "minecraft:crimson_", "minecraft:warped_",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// CanExplosionDestroyBlock reports whether an explosion ray with remaining positive
// intensity can destroy state. Unbreakable blocks and standalone fluid blocks
// (water/lava) are never destroyed by explosions.
func CanExplosionDestroyBlock(state uint16) bool {
	if isAirState(state) {
		return false
	}
	if _, _, isFluid := isStandaloneFluidBlock(state); isFluid {
		return false
	}
	if BlockBlastResistance(state) >= UnbreakableBlastResistance {
		return false
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:bedrock", "minecraft:barrier", "minecraft:end_portal_frame",
		"minecraft:end_portal", "minecraft:end_gateway", "minecraft:command_block",
		"minecraft:chain_command_block", "minecraft:repeating_command_block",
		"minecraft:structure_block", "minecraft:jigsaw", "minecraft:light",
		"minecraft:moving_piston", "minecraft:reinforced_deepslate":
		return false
	default:
		return true
	}
}

// AlwaysDropsFromExplosion reports whether a block broken by an explosion
// ignores explosion drop decay (1/radius) and always drops with 100% chance
// when doTileDrops is enabled (vanilla LootItemSurvivesExplosion condition:
// shulker boxes, beacons, conduits, dragon eggs, heads/skulls).
func AlwaysDropsFromExplosion(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	if strings.HasSuffix(info.Name, "shulker_box") || strings.HasSuffix(info.Name, "_head") || strings.HasSuffix(info.Name, "_skull") {
		return true
	}
	switch info.Name {
	case "minecraft:beacon", "minecraft:conduit", "minecraft:dragon_egg":
		return true
	default:
		return false
	}
}

// ExplosionRayResult holds the ordered positions of blocks to destroy, any
// TNT blocks caught in the blast for chain detonation, and any fire positions
// ignited by a fiery explosion.
type ExplosionRayResult struct {
	DestroyedBlocks []BlockPos
	ChainTNTBlocks  []BlockPos
	FirePositions   []BlockPos
}

// SimulateExplosionRays casts the 1352 vanilla explosion rays from (x, y, z)
// with the given radius in c, returning the deduplicated blocks to destroy
// (and any fire positions if causesFire is true). Does not mutate c.
func (c *Cache) SimulateExplosionRays(x, y, z, radius float64, causesFire bool, canGrief bool, rng *rand.Rand) ExplosionRayResult {
	var out ExplosionRayResult
	if c == nil || radius <= 0 || !canGrief {
		return out
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(int64(math.Float64bits(x+y*31+z*997)) ^ 0x5DEECE66D))
	}

	toBlow := make(map[BlockPos]bool, 64)
	for _, dir := range explosionRayDirs {
		intensity := radius * (0.7 + rng.Float64()*0.6)
		px, py, pz := x, y, z
		for intensity > 0 {
			bx := int(math.Floor(px))
			by := int(math.Floor(py))
			bz := int(math.Floor(pz))
			if by < MinY || by >= MinY+WorldHeight {
				break
			}
			state := c.GetBlock(bx, by, bz)
			if !isAirState(state) {
				res := BlockBlastResistance(state)
				intensity -= (res + 0.3) * ExplosionRayStep
				if intensity > 0 && CanExplosionDestroyBlock(state) {
					toBlow[BlockPos{X: bx, Y: by, Z: bz}] = true
				}
			}
			px += dir.x * ExplosionRayStep
			py += dir.y * ExplosionRayStep
			pz += dir.z * ExplosionRayStep
			intensity -= ExplosionStepAttenuation
		}
	}

	if len(toBlow) == 0 {
		return out
	}

	out.DestroyedBlocks = make([]BlockPos, 0, len(toBlow))
	for pos := range toBlow {
		out.DestroyedBlocks = append(out.DestroyedBlocks, pos)
	}
	// Sort in deterministic order (top-to-bottom Y, then Z, then X) so supported
	// blocks above are processed cleanly and tests are reproducible.
	sortExplosionPositions(out.DestroyedBlocks)

	for _, pos := range out.DestroyedBlocks {
		st := c.GetBlock(pos.X, pos.Y, pos.Z)
		if IsTNTBlock(st) {
			out.ChainTNTBlocks = append(out.ChainTNTBlocks, pos)
		}
	}

	if causesFire {
		for _, pos := range out.DestroyedBlocks {
			if pos.Y <= MinY {
				continue
			}
			// In vanilla ServerExplosion.createFire, each destroyed air cell
			// above a solid opaque block has a 1-in-3 chance to ignite fire.
			if rng.Intn(3) == 0 {
				belowPos := BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
				if !toBlow[belowPos] && isSturdySupportBlock(c.GetBlock(belowPos.X, belowPos.Y, belowPos.Z)) {
					out.FirePositions = append(out.FirePositions, pos)
				}
			}
		}
	}

	return out
}

func sortExplosionPositions(positions []BlockPos) {
	// Insertion/shell sort by Y descending, then Z ascending, then X ascending.
	for i := 1; i < len(positions); i++ {
		cur := positions[i]
		j := i - 1
		for j >= 0 && explosionPosLess(cur, positions[j]) {
			positions[j+1] = positions[j]
			j--
		}
		positions[j+1] = cur
	}
}

func explosionPosLess(a, b BlockPos) bool {
	if a.Y != b.Y {
		return a.Y > b.Y
	}
	if a.Z != b.Z {
		return a.Z < b.Z
	}
	return a.X < b.X
}

// ComputeExplosionExposure calculates the fraction of sample rays [0.0, 1.0]
// from (ex, ey, ez) to grid points across box that are not obstructed by solid
// blocks in c, matching vanilla Explosion.getSeenPercent(Vec3, Entity).
func (c *Cache) ComputeExplosionExposure(ex, ey, ez float64, box AABB) float64 {
	wx := box.WidthX()
	hy := box.HeightY()
	wz := box.WidthZ()
	if wx <= 0 || hy <= 0 || wz <= 0 {
		if c == nil || !c.isSegmentObstructed(box.MinX, box.MinY, box.MinZ, ex, ey, ez) {
			return 1.0
		}
		return 0.0
	}

	dx := 1.0 / (wx*2.0 + 1.0)
	dy := 1.0 / (hy*2.0 + 1.0)
	dz := 1.0 / (wz*2.0 + 1.0)
	ox := (1.0 - math.Floor(1.0/dx)*dx) * 0.5
	oz := (1.0 - math.Floor(1.0/dz)*dz) * 0.5

	visible := 0
	total := 0
	for fx := 0.0; fx <= 1.0+1e-9; fx += dx {
		for fy := 0.0; fy <= 1.0+1e-9; fy += dy {
			for fz := 0.0; fz <= 1.0+1e-9; fz += dz {
				sx := box.MinX + wx*fx + ox
				sy := box.MinY + hy*fy
				sz := box.MinZ + wz*fz + oz
				if c == nil || !c.isSegmentObstructed(sx, sy, sz, ex, ey, ez) {
					visible++
				}
				total++
			}
		}
	}
	if total == 0 {
		return 0.0
	}
	return float64(visible) / float64(total)
}

func (c *Cache) isSegmentObstructed(x0, y0, z0, x1, y1, z1 float64) bool {
	dx := x1 - x0
	dy := y1 - y0
	dz := z1 - z0
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist <= 1e-6 {
		return false
	}
	const step = 0.2
	steps := int(math.Ceil(dist / step))
	if steps < 1 {
		steps = 1
	}
	invSteps := 1.0 / float64(steps)
	sx := dx * invSteps
	sy := dy * invSteps
	sz := dz * invSteps

	endBX, endBY, endBZ := int(math.Floor(x1)), int(math.Floor(y1)), int(math.Floor(z1))
	lastBX, lastBY, lastBZ := math.MinInt32, math.MinInt32, math.MinInt32

	px, py, pz := x0, y0, z0
	for i := 0; i < steps; i++ {
		bx := int(math.Floor(px))
		by := int(math.Floor(py))
		bz := int(math.Floor(pz))
		if bx == endBX && by == endBY && bz == endBZ {
			break
		}
		if bx != lastBX || by != lastBY || bz != lastBZ {
			lastBX, lastBY, lastBZ = bx, by, bz
			if by >= MinY && by < MinY+WorldHeight {
				st := c.GetBlock(bx, by, bz)
				if blocksExplosionSight(st) {
					return true
				}
			}
		}
		px += sx
		py += sy
		pz += sz
	}
	return false
}

func blocksExplosionSight(state uint16) bool {
	if isAirState(state) {
		return false
	}
	if _, _, isFluid := isStandaloneFluidBlock(state); isFluid {
		return false
	}
	return blocksMotionOrFluid(state) || isSolidState(state)
}

// ComputeExplosionImpactAndDamage calculates the entity impact factor
// (1 - dist/(2*radius)) * exposure and the resulting base (Normal difficulty)
// explosion damage: floor(((impact^2 + impact)/2 * 7 * (2*radius) + 1)).
// Returns (0, 0) if the entity is outside 2*radius or has 0 exposure.
func ComputeExplosionImpactAndDamage(ex, ey, ez, radius float64, box AABB, exposure float64) (impact float64, baseDamage float32) {
	diameter := radius * 2.0
	if diameter <= 0 || exposure <= 0 {
		return 0, 0
	}
	px, py, pz := box.CenterBottom()
	dx := px - ex
	dy := py - ey
	dz := pz - ez
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist >= diameter {
		return 0, 0
	}
	distRatio := dist / diameter
	impact = (1.0 - distRatio) * exposure
	if impact <= 0 {
		return 0, 0
	}
	raw := math.Floor(((impact*impact+impact)*0.5)*7.0*diameter + 1.0)
	return impact, float32(raw)
}

// ComputeExplosionKnockbackMultiplier returns the knockback velocity multiplier
// after applying Blast Protection knockback reduction (15% reduction per level,
// clamped to [0, 1]).
func ComputeExplosionKnockbackMultiplier(impact float64, maxBlastProtectionLevel int) float64 {
	if impact <= 0 {
		return 0
	}
	reduction := 0.15 * float64(maxBlastProtectionLevel)
	if reduction > 1.0 {
		reduction = 1.0
	}
	if reduction < 0 {
		reduction = 0
	}
	return impact * (1.0 - reduction)
}

// ComputeExplosionKnockbackVector computes the 3D knockback velocity delta
// imparted by an explosion at (ex, ey, ez) onto an entity with bounding box box
// and highest Blast Protection level maxBlastProtectionLevel.
func ComputeExplosionKnockbackVector(ex, ey, ez float64, box AABB, impact float64, maxBlastProtectionLevel int) (vx, vy, vz float64) {
	mult := ComputeExplosionKnockbackMultiplier(impact, maxBlastProtectionLevel)
	if mult <= 0 {
		return 0, 0, 0
	}
	eyeX, eyeY, eyeZ := box.EyePos()
	dx := eyeX - ex
	dy := eyeY - ey
	dz := eyeZ - ez
	len3D := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if len3D <= 1e-6 {
		return 0, mult * 0.5, 0
	}
	return (dx / len3D) * mult, (dy / len3D) * mult, (dz / len3D) * mult
}
