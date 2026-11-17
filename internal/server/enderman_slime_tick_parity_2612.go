package server

import (
	"math"
	"math/rand"
	"sort"
	"strings"
	"sync"

	"regionio/internal/world"
	"regionio/internal/worldgen"
)

type slimeInheritedProps2612 struct {
	CustomName   string
	NoAI         bool
	Silent       bool
	Invulnerable bool
}

var (
	slimeSplitPropsMu       sync.RWMutex
	slimeSplitPropsByEntity = make(map[int32]slimeInheritedProps2612)

	flattenedBlockTagsOnce sync.Once
	flattenedBlockTagSets  map[string]map[string]bool
	flattenedBlockTagLists map[string][]string
)

func ensureFlattenedBlockTags2612() {
	flattenedBlockTagsOnce.Do(func() {
		flattenedBlockTagSets = make(map[string]map[string]bool)
		flattenedBlockTagLists = make(map[string][]string)
		set, err := worldgen.LoadFeatureSet()
		if err != nil || set == nil {
			return
		}
		var expand func(tag string, visiting map[string]bool) []string
		expand = func(tag string, visiting map[string]bool) []string {
			norm := canonicalResourceName(strings.TrimPrefix(strings.TrimSpace(tag), "#"))
			if visiting[norm] {
				return nil
			}
			visiting[norm] = true
			defer delete(visiting, norm)
			var out []string
			for _, raw := range set.BlockTags[norm] {
				if strings.HasPrefix(raw, "#") {
					out = append(out, expand(raw[1:], visiting)...)
				} else {
					out = append(out, canonicalResourceName(raw))
				}
			}
			return out
		}
		for tag := range set.BlockTags {
			normTag := canonicalResourceName(strings.TrimPrefix(tag, "#"))
			raw := expand(normTag, make(map[string]bool))
			m := make(map[string]bool, len(raw))
			list := make([]string, 0, len(raw))
			for _, b := range raw {
				if !m[b] {
					m[b] = true
					list = append(list, b)
				}
			}
			sort.Strings(list)
			flattenedBlockTagSets[normTag] = m
			flattenedBlockTagLists[normTag] = list
		}
	})
}

// FlattenedBlockNamesInTag2612 returns the sorted, deduplicated list of canonical
// block names in `tag` from the embedded 26.1.2 datapack (`worldgen.LoadFeatureSet().BlockTags`),
// recursively expanding all `#namespace:subtag` references.
func FlattenedBlockNamesInTag2612(tag string) []string {
	ensureFlattenedBlockTags2612()
	normTag := canonicalResourceName(strings.TrimPrefix(strings.TrimSpace(tag), "#"))
	src := flattenedBlockTagLists[normTag]
	if len(src) == 0 {
		return nil
	}
	out := make([]string, len(src))
	copy(out, src)
	return out
}

// HasFlattenedBlockNameTag2612 reports whether `blockName` belongs to `tag`
// (with recursive `#subtag` expansion) in the embedded 26.1.2 datapack.
func HasFlattenedBlockNameTag2612(blockName, tag string) bool {
	if strings.TrimSpace(blockName) == "" {
		return false
	}
	ensureFlattenedBlockTags2612()
	normBlock := canonicalResourceName(blockName)
	normTag := canonicalResourceName(strings.TrimPrefix(strings.TrimSpace(tag), "#"))
	m := flattenedBlockTagSets[normTag]
	return m != nil && m[normBlock]
}

// HasFlattenedBlockTag2612 reports whether `state` belongs to `tag` (with recursive
// `#subtag` expansion) in the embedded 26.1.2 datapack.
func HasFlattenedBlockTag2612(state uint16, tag string) bool {
	if state == world.StateAir {
		return false
	}
	return HasFlattenedBlockNameTag2612(world.BlockName(state), tag)
}

// ============================================================================
// ENDERMAN & SLIME / MAGMA CUBE TICK-BY-TICK PARITY (Vanilla 26.1.2 / Proto 775)
// Verified against server-26.1.2.jar:
//   - net.minecraft.world.entity.monster.EnderMan
//   - net.minecraft.world.entity.monster.EnderMan$EndermanFreezeWhenLookedAt
//   - net.minecraft.world.entity.monster.EnderMan$EndermanLookForPlayerGoal
//   - net.minecraft.world.entity.monster.EnderMan$EndermanTakeBlockGoal
//   - net.minecraft.world.entity.monster.EnderMan$EndermanLeaveBlockGoal
//   - net.minecraft.world.entity.LivingEntity (isLookingAtMe, isSensitiveToWater, hurtServer)
//   - net.minecraft.world.entity.monster.Slime
//   - net.minecraft.world.entity.monster.Slime$SlimeMoveControl
//   - net.minecraft.world.entity.monster.Slime$SlimeAttackGoal
//   - net.minecraft.world.entity.monster.Slime$SlimeFloatGoal
//   - net.minecraft.world.entity.monster.Slime$SlimeRandomDirectionGoal
//   - net.minecraft.world.entity.monster.Slime$SlimeKeepOnJumpingGoal
//   - net.minecraft.world.entity.monster.MagmaCube
// ============================================================================

const (
	// EnderMan attributes & timers (net.minecraft.world.entity.monster.EnderMan)
	EndermanBaseMovementSpeed            float64 = 0.30 // Attributes.MOVEMENT_SPEED = 0.30000001192092896D
	EndermanAttackingSpeedModifierAdd    float64 = 0.15 // SPEED_MODIFIER_ATTACKING = +0.15000000596046448D (ADD_VALUE)
	EndermanAggressiveMovementSpeed2612  float64 = EndermanBaseMovementSpeed + EndermanAttackingSpeedModifierAdd
	EndermanStepHeight2612               float64 = 1.0
	EndermanStareAggroDelayTicks2612     int     = 5    // EndermanLookForPlayerGoal.start: aggroTime = adjustedTickDelay(5)
	EndermanFreezeStareMaxDist           float64 = 16.0 // EndermanFreezeWhenLookedAt: distanceToSqr <= 256.0D
	EndermanFreezeStareMaxDistSqr        float64 = 256.0
	EndermanCloseStareTeleportDist       float64 = 4.0 // EndermanLookForPlayerGoal.tick: distanceToSqr < 16.0D -> teleport()
	EndermanCloseStareTeleportDistSqr    float64 = 16.0
	EndermanTeleportTowardsMinDist       float64 = 16.0 // EndermanLookForPlayerGoal.tick: distanceToSqr > 256.0D
	EndermanTeleportTowardsMinDistSqr    float64 = 256.0
	EndermanTeleportTowardsDelayTicks    int     = 30   // EndermanLookForPlayerGoal.tick: teleportTime >= adjustedTickDelay(30)
	EndermanTeleportTowardsStepDist      float64 = 16.0 // EnderMan.teleportTowards: d0 = 16.0D
	EndermanCreepyStareSoundCooldown2612 int     = 400  // EnderMan.DELAY_BETWEEN_CREEPY_STARE_SOUND = 400
	EndermanMinDeaggressionTicks2612     int     = 600  // EnderMan.MIN_DEAGGRESSION_TIME = 600
	EndermanPersistentAngerMinTicks2612  int     = 400  // TimeUtil.rangeOfSeconds(20, 39) -> 400..780 ticks
	EndermanPersistentAngerMaxTicks2612  int     = 780
	EndermanProjectileTeleportAttempts   int     = 64   // EnderMan.hurtServer: for (int i = 0; i < 64; i++) teleport()
	EndermanNonLivingTeleportDenominator int     = 10   // EnderMan.hurtServer: random.nextInt(10) != 0 -> teleport()
	EndermanWaterHurtCooldownTicks2612   int     = 10   // LivingEntity.hurtServer: invulnerableTime > 10 from 20 -> 10 ticks
	EndermanTakeBlockRollDenominator2612 int     = 20   // EndermanTakeBlockGoal: reducedTickDelay(20)
	EndermanLeaveBlockRollDenom2612      int     = 2000 // EndermanLeaveBlockGoal: reducedTickDelay(2000)
	EndermanHoldableBlockCount2612       int     = 45   // data/minecraft/tags/block/enderman_holdable.json in 26.1.2.jar

	// Slime & MagmaCube constants (Slime.java, MagmaCube.java, EntityType.java)
	SlimeBaseDimensionSize2612       float64 = 0.52  // EntityType.SLIME / MAGMA_CUBE: sized(0.52F, 0.52F)
	SlimeBaseEyeHeight2612           float64 = 0.325 // EntityType.SLIME / MAGMA_CUBE: eyeHeight(0.325F)
	SlimeJumpDelayMinTicks2612       int     = 10    // Slime.getJumpDelay: random.nextInt(20) + 10 -> 10..29
	SlimeJumpDelayMaxTicks2612       int     = 29
	MagmaCubeJumpDelayMultiplier2612 int     = 4    // MagmaCube.getJumpDelay: super.getJumpDelay() * 4 -> 40..116
	SlimeBaseJumpPower2612           float64 = 0.42 // Attributes.JUMP_STRENGTH default = 0.42F
	MagmaCubeJumpPowerPerSize2612    float64 = 0.10 // MagmaCube.jumpFromGround: getJumpPower() + size * 0.1F
	MagmaCubeLavaJumpBase2612        float64 = 0.22 // MagmaCube.jumpInLiquid(LAVA): 0.22F + size * 0.05F
	MagmaCubeLavaJumpPerSize2612     float64 = 0.05
	SlimeWaterJumpDeltaY2612         float64 = 0.04 // LivingEntity.jumpInLiquid: +0.04D
	SlimeFloatGoalJumpChance2612     float32 = 0.80 // SlimeFloatGoal.tick: random.nextFloat() < 0.8F
	SlimeFloatSpeedModifier2612      float64 = 1.2  // SlimeFloatGoal.tick: setWantedMovement(1.2D)
	SlimeAttackLookMaxTurnDeg2612    float32 = 10.0 // SlimeAttackGoal.tick: lookAt(target, 10.0F, 10.0F)
	SlimeMoveControlMaxTurnDeg2612   float32 = 90.0 // SlimeMoveControl.tick: rotlerp(yRot, targetYRot, 90.0F)
	SlimeAttackGrowTiredTicks2612    int     = 300  // SlimeAttackGoal.start: reducedTickDelay(300)
	SlimeRandomDirMinTicks2612       int     = 40   // SlimeRandomDirectionGoal.tick: 40 + random.nextInt(60)
	SlimeRandomDirMaxTicks2612       int     = 99
	SlimeTargetMaxVerticalDiff2612   float64 = 4.0 // Slime.registerGoals: Math.abs(target.getY() - this.getY()) <= 4.0D
	SlimeSplitSpawnYOffset2612       float64 = 0.5 // Slime.remove: getY() + 0.5D

	// Missing small-slime sound events in 26.1.2.
	SoundSlimeHurtSmall   = "minecraft:entity.slime.hurt_small"
	SoundSlimeDeathSmall  = "minecraft:entity.slime.death_small"
	SoundSlimeSquishSmall = "minecraft:entity.slime.squish_small"
	SoundSlimeJumpSmall   = "minecraft:entity.slime.jump_small"
)

// hasColliderLineOfSight2612 implements vanilla `ClipContext.Block.COLLIDER, ClipContext.Fluid.NONE`
// raycast between `(x0, y0, z0)` and `(x1, y1, z1)` in `cache`.
// Unlike `VISUAL`, `COLLIDER` is blocked by ANY block with a non-empty collision shape
// intersected by the ray — including `minecraft:glass`, stained glass, glass panes, and leaves!
func hasColliderLineOfSight2612(cache *world.Cache, x0, y0, z0, x1, y1, z1 float64) bool {
	if cache == nil {
		return true
	}
	dx := x1 - x0
	dy := y1 - y0
	dz := z1 - z0
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist <= 1e-6 {
		return true
	}
	steps := int(math.Ceil(dist * 8.0))
	if steps < 2 {
		steps = 2
	}
	var lastBX, lastBY, lastBZ int
	hasLast := false
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		px := x0 + dx*t
		py := y0 + dy*t
		pz := z0 + dz*t
		bx := int(math.Floor(px))
		by := int(math.Floor(py))
		bz := int(math.Floor(pz))
		if hasLast && bx == lastBX && by == lastBY && bz == lastBZ {
			continue
		}
		lastBX, lastBY, lastBZ = bx, by, bz
		hasLast = true
		if by < world.MinY || by >= world.MinY+world.WorldHeight {
			continue
		}
		state := cache.GetBlock(bx, by, bz)
		if state == world.StateAir {
			continue
		}
		boxes := world.BlockCollisionBoxesAt(bx, by, bz, state)
		for _, box := range boxes {
			if rayIntersectsAABB2612(x0, y0, z0, x1, y1, z1, box) {
				return false
			}
		}
	}
	return true
}

func rayIntersectsAABB2612(x0, y0, z0, x1, y1, z1 float64, box world.AABB) bool {
	dx := x1 - x0
	dy := y1 - y0
	dz := z1 - z0
	tmin := 0.0
	tmax := 1.0

	checkAxis := func(orig, dir, bmin, bmax float64) bool {
		if math.Abs(dir) < 1e-9 {
			return orig >= bmin && orig <= bmax
		}
		inv := 1.0 / dir
		t1 := (bmin - orig) * inv
		t2 := (bmax - orig) * inv
		if t1 > t2 {
			t1, t2 = t2, t1
		}
		if t1 > tmin {
			tmin = t1
		}
		if t2 < tmax {
			tmax = t2
		}
		return tmin <= tmax
	}

	if !checkAxis(x0, dx, box.MinX, box.MaxX) {
		return false
	}
	if !checkAxis(y0, dy, box.MinY, box.MaxY) {
		return false
	}
	if !checkAxis(z0, dz, box.MinZ, box.MaxZ) {
		return false
	}
	return tmax > 1e-6 && tmin < 1.0-1e-6
}

// CanEndermanPickUpBlockRaycast2612 implements the `ClipContext.Block.OUTLINE, ClipContext.Fluid.NONE`
// raycast check in `EnderMan$EndermanTakeBlockGoal.tick()`:
// casts from `(endermanBlockX + 0.5, candY + 0.5, endermanBlockZ + 0.5)` to
// `(candX + 0.5, candY + 0.5, candZ + 0.5)` and returns true iff the first hit block
// with an outline shape is `(candX, candY, candZ)`.
func CanEndermanPickUpBlockRaycast2612(cache *world.Cache, endermanBlockX, endermanBlockZ, candX, candY, candZ int) bool {
	if cache == nil {
		return true
	}
	if candY < world.MinY || candY >= world.MinY+world.WorldHeight {
		return false
	}
	x0 := float64(endermanBlockX) + 0.5
	y0 := float64(candY) + 0.5
	z0 := float64(endermanBlockZ) + 0.5
	x1 := float64(candX) + 0.5
	y1 := float64(candY) + 0.5
	z1 := float64(candZ) + 0.5

	dx := x1 - x0
	dz := z1 - z0
	dist := math.Hypot(dx, dz)
	if dist <= 1e-6 {
		return len(world.BlockOutlineBoxesAt(candX, candY, candZ, cache.GetBlock(candX, candY, candZ))) > 0
	}
	steps := int(math.Ceil(dist * 16.0))
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		px := x0 + dx*t
		pz := z0 + dz*t
		bx := int(math.Floor(px))
		bz := int(math.Floor(pz))
		if bx == endermanBlockX && bz == endermanBlockZ && (candX != endermanBlockX || candZ != endermanBlockZ) {
			continue
		}
		st := cache.GetBlock(bx, candY, bz)
		if st == world.StateAir {
			continue
		}
		boxes := world.BlockOutlineBoxesAt(bx, candY, bz, st)
		for _, box := range boxes {
			if rayIntersectsAABB2612(x0, y0, z0, x1, y1, z1, box) || (bx == candX && bz == candZ) {
				return bx == candX && bz == candZ
			}
		}
	}
	return true
}

func isCollisionShapeFullBlock2612(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	boxes := world.BlockCollisionBoxes(state)
	if len(boxes) != 1 {
		return false
	}
	b := boxes[0]
	return b.MinX <= 1e-6 && b.MinY <= 1e-6 && b.MinZ <= 1e-6 &&
		b.MaxX >= 1.0-1e-6 && b.MaxY >= 1.0-1e-6 && b.MaxZ >= 1.0-1e-6
}

func canEndermanCarriedBlockSurvive2612(cache *world.Cache, bx, by, bz int, carriedState, belowState uint16) bool {
	name := canonicalResourceName(world.BlockName(carriedState))
	switch {
	case name == "minecraft:wither_rose":
		return HasFlattenedBlockTag2612(belowState, "minecraft:supports_wither_rose")
	case HasFlattenedBlockTag2612(carriedState, "minecraft:small_flowers"):
		return HasFlattenedBlockTag2612(belowState, "minecraft:supports_vegetation")
	case name == "minecraft:crimson_fungus":
		return HasFlattenedBlockTag2612(belowState, "minecraft:supports_crimson_fungus") || HasFlattenedBlockTag2612(belowState, "minecraft:supports_vegetation")
	case name == "minecraft:warped_fungus":
		return HasFlattenedBlockTag2612(belowState, "minecraft:supports_warped_fungus") || HasFlattenedBlockTag2612(belowState, "minecraft:supports_vegetation")
	case name == "minecraft:crimson_roots":
		return HasFlattenedBlockTag2612(belowState, "minecraft:supports_crimson_roots") || HasFlattenedBlockTag2612(belowState, "minecraft:supports_vegetation")
	case name == "minecraft:warped_roots":
		return HasFlattenedBlockTag2612(belowState, "minecraft:supports_warped_roots") || HasFlattenedBlockTag2612(belowState, "minecraft:supports_vegetation")
	case name == "minecraft:brown_mushroom" || name == "minecraft:red_mushroom":
		if HasFlattenedBlockTag2612(belowState, "minecraft:overrides_mushroom_light_requirement") {
			return true
		}
		if !isCollisionShapeFullBlock2612(belowState) {
			return false
		}
		if cache != nil {
			// MushroomBlock.canSurvive: getRawBrightness(pos, 0) < 13 when not on mushroom grow block.
			sky := int(cache.SkyLightAt(bx, by, bz))
			blk := int(cache.BlockLightAt(bx, by, bz))
			if sky >= 13 || blk >= 13 {
				return false
			}
		}
		return true
	case name == "minecraft:cactus":
		belowName := canonicalResourceName(world.BlockName(belowState))
		if belowName != "minecraft:cactus" && !HasFlattenedBlockTag2612(belowState, "minecraft:supports_cactus") {
			return false
		}
		if cache != nil {
			dirs := [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
			for _, d := range dirs {
				adj := cache.GetBlock(bx+d[0], by, bz+d[1])
				if len(world.BlockCollisionBoxes(adj)) > 0 || canonicalResourceName(world.BlockName(adj)) == "minecraft:lava" {
					return false
				}
			}
		}
		return true
	case name == "minecraft:cactus_flower":
		belowName := canonicalResourceName(world.BlockName(belowState))
		return belowName == "minecraft:cactus" || belowName == "minecraft:farmland" ||
			HasFlattenedBlockTag2612(belowState, "minecraft:support_override_cactus_flower") ||
			isCollisionShapeFullBlock2612(belowState)
	default:
		return true
	}
}

// CanEndermanPlaceBlock2612 implements vanilla `EnderMan$EndermanLeaveBlockGoal.canPlaceBlock`
// (`enderman_2612.txt:1271-1305`):
//  1. Target position `(bx, by, bz)` is air (`destinationState.isAir()`).
//  2. Below position `(bx, by-1, bz)` is NOT air (`!belowState.isAir()`).
//  3. Below position is NOT `minecraft:bedrock` (`!belowState.is(Blocks.BEDROCK)`).
//  4. Below position has a full 1x1x1 collision shape (`belowState.isCollisionShapeFullBlock`).
//  5. Carried block `canSurvive` at `(bx, by, bz)`.
//  6. No other entity (nor player) intersects the unit cube `[bx, by, bz]..[bx+1, by+1, bz+1]`.
func (s *Server) CanEndermanPlaceBlock2612(dim string, endermanID int32, bx, by, bz int, carriedState uint16) bool {
	if s == nil || carriedState == world.StateAir {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil || by <= world.MinY || by >= world.MinY+world.WorldHeight {
		return false
	}
	destState := cache.GetBlock(bx, by, bz)
	if destState != world.StateAir {
		return false
	}
	belowState := cache.GetBlock(bx, by-1, bz)
	if belowState == world.StateAir {
		return false
	}
	if canonicalResourceName(world.BlockName(belowState)) == "minecraft:bedrock" {
		return false
	}
	if !isCollisionShapeFullBlock2612(belowState) {
		return false
	}
	if !canEndermanCarriedBlockSurvive2612(cache, bx, by, bz, carriedState, belowState) {
		return false
	}
	// Check entities inside [bx, bx+1] x [by, by+1] x [bz, bz+1] excluding the Enderman itself.
	minX, maxX := float64(bx), float64(bx+1)
	minY, maxY := float64(by), float64(by+1)
	minZ, maxZ := float64(bz), float64(bz+1)
	if s.entities != nil {
		cx := int32(int64(bx) >> 4)
		cz := int32(int64(bz) >> 4)
		for _, other := range s.entities.AppendInChunkRange(nil, cx, cz, 1) {
			if other.ID == endermanID || other.EffectiveDimension() != normDim {
				continue
			}
			halfW := 0.3
			height := 1.8
			if other.IsItem() || other.IsExperienceOrb() {
				halfW = 0.125
				height = 0.25
			}
			if other.X+halfW > minX && other.X-halfW < maxX &&
				other.Y+height > minY && other.Y < maxY &&
				other.Z+halfW > minZ && other.Z-halfW < maxZ {
				return false
			}
		}
	}
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		ps := p.Snapshot()
		if world.NormalizeDimension(ps.Dimension) != normDim || ps.GameMode == GameModeSpectator {
			continue
		}
		if ps.X+0.3 > minX && ps.X-0.3 < maxX &&
			ps.Y+1.8 > minY && ps.Y < maxY &&
			ps.Z+0.3 > minZ && ps.Z-0.3 < maxZ {
			return false
		}
	}
	return true
}

// EndermanLookForPlayerGoal2612 models vanilla 26.1.2 `EnderMan$EndermanLookForPlayerGoal`
// + `EndermanFreezeWhenLookedAt` tick-by-tick state machine:
//   - `Start(player)` sets `PendingTarget = player`, `AggroTime = 5`, `TeleportTime = 0`, `StaredAt = true`, `Creepy = false`.
//   - While `PendingTarget != nil`: if `!staring`, cancels (`Stop()`); otherwise decrements `AggroTime--`,
//     and when `AggroTime <= 0`, promotes `PendingTarget` to `ActiveTarget` and sets `Creepy = true`!
//   - While `ActiveTarget != nil`:
//   - If `staring`:
//   - `Frozen = (distSq <= 256.0)` (`EndermanFreezeWhenLookedAt` within 16 blocks)
//   - If `distSq < 16.0` (`< 4` blocks), triggers random `TeleportAway = true`
//   - Resets `TeleportTime = 0`
//   - Else (`!staring`):
//   - `Frozen = false`
//   - If `distSq > 256.0` (`> 16` blocks), increments `TeleportTime++` and when `>= 30`,
//     triggers `TeleportTowards = true` and resets `TeleportTime = 0`!
type EndermanLookForPlayerGoal2612 struct {
	PendingTarget *PlayerSession
	ActiveTarget  *PlayerSession
	AggroTime     int
	TeleportTime  int
	StaredAt      bool
	Creepy        bool
	Frozen        bool
}

// Start begins the 5-tick stare confirmation countdown (`EndermanLookForPlayerGoal.start`).
func (g *EndermanLookForPlayerGoal2612) Start(player *PlayerSession) {
	if g == nil || player == nil {
		return
	}
	g.PendingTarget = player
	g.ActiveTarget = nil
	g.AggroTime = EndermanStareAggroDelayTicks2612
	g.TeleportTime = 0
	g.StaredAt = true
	g.Creepy = false
	g.Frozen = false
}

// Stop resets the goal and clears active/pending target (`EnderMan.setTarget(null)`).
func (g *EndermanLookForPlayerGoal2612) Stop() {
	if g == nil {
		return
	}
	g.PendingTarget = nil
	g.ActiveTarget = nil
	g.AggroTime = 0
	g.TeleportTime = 0
	g.StaredAt = false
	g.Creepy = false
	g.Frozen = false
}

// Tick advances `EndermanLookForPlayerGoal` + `EndermanFreezeWhenLookedAt` by 1 tick.
func (g *EndermanLookForPlayerGoal2612) Tick(staring bool, distSq float64) (becameAngry, teleportAway, teleportTowards bool) {
	if g == nil {
		return false, false, false
	}
	if g.PendingTarget != nil {
		if !staring {
			g.PendingTarget = nil
			g.AggroTime = 0
			g.Frozen = false
			return false, false, false
		}
		g.AggroTime--
		if g.AggroTime <= 0 {
			g.ActiveTarget = g.PendingTarget
			g.PendingTarget = nil
			g.Creepy = true
			g.Frozen = distSq <= EndermanFreezeStareMaxDistSqr
			return true, false, false
		}
		return false, false, false
	}
	if g.ActiveTarget == nil {
		g.Frozen = false
		return false, false, false
	}
	if distSq > EndermanStareRange*EndermanStareRange {
		g.Stop()
		return false, false, false
	}
	if staring {
		g.Frozen = distSq <= EndermanFreezeStareMaxDistSqr
		if distSq < EndermanCloseStareTeleportDistSqr {
			teleportAway = true
		}
		g.TeleportTime = 0
		return false, teleportAway, false
	}
	g.Frozen = false
	if distSq > EndermanTeleportTowardsMinDistSqr {
		g.TeleportTime++
		if g.TeleportTime >= EndermanTeleportTowardsDelayTicks {
			g.TeleportTime = 0
			teleportTowards = true
		}
	}
	return false, false, teleportTowards
}

// TeleportEndermanTowards2612 implements vanilla `EnderMan.teleportTowards(Entity)`
// (`enderman_2612.txt:576-657`):
//   - Computes normalized vector `v = normalize((ex - tx, (ey + height*0.5) - targetEyeY, ez - tz))`
//   - Samples candidate `(ex + (nextDouble()-0.5)*8.0 - v.x*16.0, ey + (nextInt(16)-8) - v.y*16.0, ez + (nextDouble()-0.5)*8.0 - v.z*16.0)`
//   - Drops down to solid non-water ground and teleports if unobstructed.
func (s *Server) TeleportEndermanTowards2612(entityID int32, targetX, targetEyeY, targetZ float64, rng *rand.Rand) (newX, newY, newZ float64, ok bool) {
	if s == nil || s.entities == nil {
		return 0, 0, 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeEnderman {
		return 0, 0, 0, false
	}
	vx := ent.X - targetX
	vy := (ent.Y + EndermanHeight*0.5) - targetEyeY
	vz := ent.Z - targetZ
	length := math.Sqrt(vx*vx + vy*vy + vz*vz)
	if length > 1e-6 {
		vx /= length
		vy /= length
		vz /= length
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	attempts := 16
	for i := 0; i < attempts; i++ {
		var jx, jy, jz float64
		if rng != nil && i == 0 {
			jx = (rng.Float64() - 0.5) * 8.0
			jy = float64(rng.Intn(16) - 8)
			jz = (rng.Float64() - 0.5) * 8.0
		} else if i > 0 {
			angle := float64(i) * (math.Pi / 4.0)
			jx = math.Cos(angle) * 2.0
			jz = math.Sin(angle) * 2.0
		}
		candX := ent.X + jx - vx*EndermanTeleportTowardsStepDist
		candY := ent.Y + jy - vy*EndermanTeleportTowardsStepDist
		candZ := ent.Z + jz - vz*EndermanTeleportTowardsStepDist
		bx := int(math.Floor(candX))
		by := int(math.Floor(candY))
		bz := int(math.Floor(candZ))
		if cache != nil {
			for by > world.MinY+1 && isPassableBlock(cache.GetBlock(bx, by-1, bz)) {
				by--
			}
		}
		tx := float64(bx) + 0.5
		ty := float64(by)
		tz := float64(bz) + 0.5
		if !s.IsEndermanInWaterOrRain(dim, tx, ty, tz) && s.TeleportEndermanTo(entityID, tx, ty, tz) {
			return tx, ty, tz, true
		}
	}
	return ent.X, ent.Y, ent.Z, false
}

// RollSlimeJumpDelay2612 computes the exact jump delay in ticks for a `Slime`
// (`isMagmaCube == false`) or `MagmaCube` (`isMagmaCube == true`) in vanilla 26.1.2:
//   - Base `Slime.getJumpDelay()` = `10 + rng.Intn(20)` (`10..29` ticks).
//   - `MagmaCube.getJumpDelay()` = `base * 4` (`40..116` ticks).
//   - When `hasTarget && dealsDamage` (`SlimeMoveControl.isAggressive`), divides by `3`
//     (note: tiny `size == 1` `Slime` has `isDealsDamage() == false`, so its jump delay is NOT divided by 3!).
func RollSlimeJumpDelay2612(isMagmaCube bool, size int, hasTarget bool, rng *rand.Rand) int {
	base := 20
	if rng != nil {
		base = SlimeJumpDelayMinTicks2612 + rng.Intn(SlimeJumpDelayMaxTicks2612-SlimeJumpDelayMinTicks2612+1)
	}
	if isMagmaCube {
		base *= MagmaCubeJumpDelayMultiplier2612
	}
	dealsDamage := isMagmaCube || size > 1
	if hasTarget && dealsDamage {
		base /= 3
	}
	return base
}

// ComputeSlimeJumpVelocityY2612 returns the vertical velocity set by `jumpFromGround()`
// in vanilla 26.1.2:
//   - `Slime`: `0.42` (`Attributes.JUMP_STRENGTH`, independent of size)
//   - `MagmaCube`: `0.42 + 0.1 * size` (`0.52, 0.62, 0.82` for sizes `1, 2, 4`).
func ComputeSlimeJumpVelocityY2612(isMagmaCube bool, size int) float64 {
	if size < 1 {
		size = 1
	}
	if isMagmaCube {
		return SlimeBaseJumpPower2612 + float64(size)*MagmaCubeJumpPowerPerSize2612
	}
	return SlimeBaseJumpPower2612
}

// ComputeMagmaCubeLavaJumpVelocity2612 returns the vertical velocity set by
// `MagmaCube.jumpInLiquid(FluidTags.LAVA)` in vanilla 26.1.2:
//   - `0.22 + 0.05 * size` (`0.27` for size 1, `0.32` for size 2, `0.42` for size 4).
func ComputeMagmaCubeLavaJumpVelocity2612(size int) float64 {
	if size < 1 {
		size = 1
	}
	return MagmaCubeLavaJumpBase2612 + float64(size)*MagmaCubeLavaJumpPerSize2612
}

// RotlerpDegrees2612 implements vanilla `MoveControl.rotlerp(sourceAngle, targetAngle, maximumChange)`
// (`Mth.wrapDegrees(targetAngle - sourceAngle)` clamped to `[-maxTurn, +maxTurn]`).
func RotlerpDegrees2612(currentYaw, targetYaw, maxTurn float32) float32 {
	diff := float32(math.Mod(float64(targetYaw-currentYaw), 360.0))
	if diff >= 180.0 {
		diff -= 360.0
	} else if diff < -180.0 {
		diff += 360.0
	}
	if diff > maxTurn {
		diff = maxTurn
	} else if diff < -maxTurn {
		diff = -maxTurn
	}
	res := currentYaw + diff
	if res >= 180.0 {
		res -= 360.0
	} else if res < -180.0 {
		res += 360.0
	}
	return res
}

// ComputeSlimeSplitChildOffset2612 computes the `(ox, dy, oz)` child spawn offset
// for child index `i` (`0..count-1`) of a parent `Slime` or `MagmaCube` of `parentSize`
// in `Slime.remove(RemovalReason)` (`slime_magmacube_2612.txt:469-515, 927-941`):
//   - `width = 0.52 * parentSize`, `halfW = width / 2.0 = 0.26 * parentSize`
//   - `ox = (float64(i%2) - 0.5) * halfW` (`= ±0.13 * parentSize`)
//   - `dy = +0.5`
//   - `oz = (float64(i/2) - 0.5) * halfW` (`= ±0.13 * parentSize`)
func ComputeSlimeSplitChildOffset2612(parentSize, childIndex int) (ox, dy, oz float64) {
	if parentSize < 1 {
		parentSize = 1
	}
	halfW := (SlimeBaseDimensionSize2612 * float64(parentSize)) / 2.0
	ox = (float64(childIndex%2) - 0.5) * halfW
	oz = (float64(childIndex/2) - 0.5) * halfW
	return ox, SlimeSplitSpawnYOffset2612, oz
}

// SlimeSoundSet2612 returns the `(hurtSound, deathSound, squishSound, jumpSound)`
// for a `Slime` or `MagmaCube` of `size` in vanilla 26.1.2 (`isTiny() == (size <= 1)`).
func SlimeSoundSet2612(isMagmaCube bool, size int) (hurtSnd, deathSnd, squishSnd, jumpSnd string) {
	tiny := size <= 1
	if isMagmaCube {
		if tiny {
			return SoundMagmaCubeHurtSmall, SoundMagmaCubeDeathSmall, SoundMagmaCubeSquishSmall, SoundMagmaCubeJump
		}
		return SoundMagmaCubeHurt, SoundMagmaCubeDeath, SoundMagmaCubeSquish, SoundMagmaCubeJump
	}
	if tiny {
		return SoundSlimeHurtSmall, SoundSlimeDeathSmall, SoundSlimeSquishSmall, SoundSlimeJumpSmall
	}
	return SoundSlimeHurt, SoundSlimeDeath, SoundSlimeSquish, SoundSlimeJump
}

// SetSlimeOrMagmaCubeCustomProps2612 sets custom persistent properties (`CustomName`, `NoAI`, `Silent`, `Invulnerable`)
// on a tracked `Slime` or `MagmaCube` so `SplitSlimeOrMagmaCubeOnDeath` can preserve them across splits
// (`ConversionType.SPLIT_ON_DEATH`).
func (s *Server) SetSlimeOrMagmaCubeCustomProps2612(entityID int32, customName string, noAI, silent, invulnerable bool) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	norm := canonicalResourceName(ent.TypeName)
	if norm != MobTypeSlime && norm != MobTypeMagmaCube {
		return false
	}
	_ = s.ensureMagmaOrSlimeState(entityID, norm, ent.EffectiveDimension())
	slimeSplitPropsMu.Lock()
	slimeSplitPropsByEntity[entityID] = slimeInheritedProps2612{
		CustomName:   strings.TrimSpace(customName),
		NoAI:         noAI,
		Silent:       silent,
		Invulnerable: invulnerable,
	}
	slimeSplitPropsMu.Unlock()
	return true
}

// GetSlimeOrMagmaCubeCustomProps2612 returns the inherited custom properties for `entityID`.
func (s *Server) GetSlimeOrMagmaCubeCustomProps2612(entityID int32) (customName string, noAI, silent, invulnerable bool) {
	slimeSplitPropsMu.RLock()
	p := slimeSplitPropsByEntity[entityID]
	slimeSplitPropsMu.RUnlock()
	return p.CustomName, p.NoAI, p.Silent, p.Invulnerable
}
