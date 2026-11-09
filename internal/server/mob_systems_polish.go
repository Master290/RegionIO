package server

import (
	"container/heap"
	"math"
	"math/rand"
	"strings"
	"sync"

	"regionio/internal/world"
)

// --- 1. PathType (27 vanilla 26.1.2 constants & default maluses) and Node Evaluators ---

// PathType represents net.minecraft.world.level.pathfinder.PathType in Minecraft 26.1.2.
type PathType uint8

const (
	PathTypeBlocked              PathType = iota // BLOCKED (-1.0F)
	PathTypeOpen                                 // OPEN (0.0F)
	PathTypeWalkable                             // WALKABLE (0.0F)
	PathTypeWalkableDoor                         // WALKABLE_DOOR (0.0F)
	PathTypeTrapdoor                             // TRAPDOOR (0.0F)
	PathTypePowderSnow                           // POWDER_SNOW (-1.0F)
	PathTypeOnTopOfPowderSnow                    // ON_TOP_OF_POWDER_SNOW (0.0F)
	PathTypeFence                                // FENCE (-1.0F)
	PathTypeLava                                 // LAVA (-1.0F)
	PathTypeWater                                // WATER (8.0F)
	PathTypeWaterBorder                          // WATER_BORDER (8.0F)
	PathTypeRail                                 // RAIL (0.0F)
	PathTypeUnpassableRail                       // UNPASSABLE_RAIL (-1.0F)
	PathTypeFireInNeighbor                       // FIRE_IN_NEIGHBOR (8.0F)
	PathTypeFire                                 // FIRE (-1.0F)
	PathTypeDamagingInNeighbor                   // DAMAGING_IN_NEIGHBOR (8.0F)
	PathTypeDamaging                             // DAMAGING (-1.0F)
	PathTypeDoorOpen                             // DOOR_OPEN (0.0F)
	PathTypeDoorWoodClosed                       // DOOR_WOOD_CLOSED (-1.0F)
	PathTypeDoorIronClosed                       // DOOR_IRON_CLOSED (-1.0F)
	PathTypeBreach                               // BREACH (4.0F)
	PathTypeLeaves                               // LEAVES (-1.0F)
	PathTypeStickyHoney                          // STICKY_HONEY (8.0F)
	PathTypeCocoa                                // COCOA (0.0F)
	PathTypeDamageCautious                       // DAMAGE_CAUTIOUS (0.0F)
	PathTypeOnTopOfTrapdoor                      // ON_TOP_OF_TRAPDOOR (0.0F)
	PathTypeBigMobsCloseToDanger                 // BIG_MOBS_CLOSE_TO_DANGER (4.0F)

	PathTypeCount = 27
)

type pathTypeSpec struct {
	name  string
	malus float32
}

var vanillaPathTypeTable = [PathTypeCount]pathTypeSpec{
	PathTypeBlocked:              {name: "BLOCKED", malus: -1.0},
	PathTypeOpen:                 {name: "OPEN", malus: 0.0},
	PathTypeWalkable:             {name: "WALKABLE", malus: 0.0},
	PathTypeWalkableDoor:         {name: "WALKABLE_DOOR", malus: 0.0},
	PathTypeTrapdoor:             {name: "TRAPDOOR", malus: 0.0},
	PathTypePowderSnow:           {name: "POWDER_SNOW", malus: -1.0},
	PathTypeOnTopOfPowderSnow:    {name: "ON_TOP_OF_POWDER_SNOW", malus: 0.0},
	PathTypeFence:                {name: "FENCE", malus: -1.0},
	PathTypeLava:                 {name: "LAVA", malus: -1.0},
	PathTypeWater:                {name: "WATER", malus: 8.0},
	PathTypeWaterBorder:          {name: "WATER_BORDER", malus: 8.0},
	PathTypeRail:                 {name: "RAIL", malus: 0.0},
	PathTypeUnpassableRail:       {name: "UNPASSABLE_RAIL", malus: -1.0},
	PathTypeFireInNeighbor:       {name: "FIRE_IN_NEIGHBOR", malus: 8.0},
	PathTypeFire:                 {name: "FIRE", malus: -1.0},
	PathTypeDamagingInNeighbor:   {name: "DAMAGING_IN_NEIGHBOR", malus: 8.0},
	PathTypeDamaging:             {name: "DAMAGING", malus: -1.0},
	PathTypeDoorOpen:             {name: "DOOR_OPEN", malus: 0.0},
	PathTypeDoorWoodClosed:       {name: "DOOR_WOOD_CLOSED", malus: -1.0},
	PathTypeDoorIronClosed:       {name: "DOOR_IRON_CLOSED", malus: -1.0},
	PathTypeBreach:               {name: "BREACH", malus: 4.0},
	PathTypeLeaves:               {name: "LEAVES", malus: -1.0},
	PathTypeStickyHoney:          {name: "STICKY_HONEY", malus: 8.0},
	PathTypeCocoa:                {name: "COCOA", malus: 0.0},
	PathTypeDamageCautious:       {name: "DAMAGE_CAUTIOUS", malus: 0.0},
	PathTypeOnTopOfTrapdoor:      {name: "ON_TOP_OF_TRAPDOOR", malus: 0.0},
	PathTypeBigMobsCloseToDanger: {name: "BIG_MOBS_CLOSE_TO_DANGER", malus: 4.0},
}

// String returns the canonical enum constant name from PathType.class in 26.1.2.
func (pt PathType) String() string {
	if int(pt) >= 0 && int(pt) < len(vanillaPathTypeTable) {
		return vanillaPathTypeTable[pt].name
	}
	return "BLOCKED"
}

// DefaultMalus returns the default pathfinding malus for pt in vanilla 26.1.2.
func (pt PathType) DefaultMalus() float32 {
	return DefaultPathTypeMalus(pt)
}

// DefaultPathTypeMalus returns the vanilla 26.1.2 default malus for pt (`< 0`
// means impassable by default; `0.0` means no penalty; `> 0` adds extra cost).
func DefaultPathTypeMalus(pt PathType) float32 {
	if int(pt) >= 0 && int(pt) < len(vanillaPathTypeTable) {
		return vanillaPathTypeTable[pt].malus
	}
	return -1.0
}

// AllPathTypes2612 returns all 27 PathType enum values in declaration order.
func AllPathTypes2612() []PathType {
	out := make([]PathType, PathTypeCount)
	for i := 0; i < PathTypeCount; i++ {
		out[i] = PathType(i)
	}
	return out
}

// MobPathfindingMalus returns the effective pathfinding malus for a specific mob
// type and PathType, applying vanilla 26.1.2 per-mob `setPathfindingMalus`
// overrides on top of `DefaultPathTypeMalus`.
func MobPathfindingMalus(mobType string, pt PathType) float32 {
	norm := canonicalResourceName(mobType)
	switch norm {
	case MobTypeBlaze, MobTypeStrider, MobTypeMagmaCube, MobTypeZombifiedPiglin, MobTypeWitherSkeleton:
		switch pt {
		case PathTypeLava, PathTypeFire, PathTypeFireInNeighbor:
			return 0.0
		case PathTypeWater:
			if norm == MobTypeBlaze || norm == MobTypeStrider {
				return -1.0
			}
		}
	case MobTypeEnderman, MobTypeSnowGolem:
		if pt == PathTypeWater {
			return -1.0
		}
	case MobTypeFrog, MobTypeTurtle, MobTypeAxolotl, MobTypeDrowned,
		MobTypeGuardian, MobTypeElderGuardian, MobTypeCod, MobTypeSalmon,
		MobTypeTropicalFish, MobTypePufferfish, MobTypeDolphin, MobTypeSquid,
		MobTypeGlowSquid, MobTypeTadpole, MobTypeNautilus, MobTypeZombieNautilus:
		if pt == PathTypeWater {
			return 0.0
		}
		if pt == PathTypeWaterBorder {
			return 0.0
		}
	case MobTypeGoat:
		if pt == PathTypePowderSnow || pt == PathTypeOnTopOfPowderSnow {
			return 0.0
		}
	case MobTypeFox:
		if pt == PathTypeDamaging || pt == PathTypeDamagingInNeighbor || pt == PathTypePowderSnow {
			return 0.0
		}
	case MobTypeBee, MobTypeParrot, MobTypeAllay, MobTypeHappyGhast, MobTypePhantom, MobTypeGhast, MobTypeBat, MobTypeVex, MobTypeBreeze:
		if pt == PathTypeOpen || pt == PathTypeLeaves || pt == PathTypeCocoa {
			return 0.0
		}
		if norm == MobTypeBee && pt == PathTypeWater {
			return -1.0
		}
	case MobTypeVillager, MobTypeVindicator, MobTypePillager, MobTypeWitch:
		if pt == PathTypeDoorWoodClosed || pt == PathTypeDoorOpen || pt == PathTypeWalkableDoor {
			return 0.0
		}
	}
	return DefaultPathTypeMalus(pt)
}

// NodeEvaluatorKind identifies the vanilla 26.1.2 pathfinder NodeEvaluator family.
type NodeEvaluatorKind uint8

const (
	NodeEvaluatorWalk NodeEvaluatorKind = iota
	NodeEvaluatorSwim
	NodeEvaluatorFly
	NodeEvaluatorAmphibious
	NodeEvaluatorSpiderClimb
)

// NodeEvaluatorForMobType returns the NodeEvaluatorKind used by mobType in 26.1.2.
func NodeEvaluatorForMobType(mobType string) NodeEvaluatorKind {
	switch canonicalResourceName(mobType) {
	case MobTypeCod, MobTypeSalmon, MobTypeTropicalFish, MobTypePufferfish,
		MobTypeSquid, MobTypeGlowSquid, MobTypeDolphin, MobTypeTadpole,
		MobTypeGuardian, MobTypeElderGuardian, MobTypeNautilus, MobTypeZombieNautilus:
		return NodeEvaluatorSwim
	case MobTypeBee, MobTypeParrot, MobTypeAllay, MobTypeHappyGhast,
		MobTypePhantom, MobTypeGhast, MobTypeBat, MobTypeVex, MobTypeWither, MobTypeEnderDragon:
		return NodeEvaluatorFly
	case MobTypeFrog, MobTypeTurtle, MobTypeAxolotl, MobTypeDrowned:
		return NodeEvaluatorAmphibious
	case MobTypeSpider, MobTypeCaveSpider:
		return NodeEvaluatorSpiderClimb
	default:
		return NodeEvaluatorWalk
	}
}

func classifyRawBlockPathType2612(state uint16) PathType {
	if state == world.StateAir {
		return PathTypeOpen
	}
	name := world.BlockName(state)
	switch {
	case state == world.StateWater || name == "minecraft:water" || name == "minecraft:bubble_column":
		return PathTypeWater
	case state == world.StateLava || name == "minecraft:lava":
		return PathTypeLava
	case state == world.StatePowderSnow || name == "minecraft:powder_snow":
		return PathTypePowderSnow
	case name == "minecraft:fire" || name == "minecraft:soul_fire" || name == "minecraft:magma_block" ||
		(strings.HasSuffix(name, "campfire") && world.BlockProperties(state)["lit"] != "false"):
		return PathTypeFire
	case name == "minecraft:cactus" || name == "minecraft:sweet_berry_bush":
		return PathTypeDamaging
	case name == "minecraft:wither_rose" || name == "minecraft:pointed_dripstone":
		return PathTypeDamageCautious
	case name == "minecraft:honey_block":
		return PathTypeStickyHoney
	case name == "minecraft:cocoa":
		return PathTypeCocoa
	case strings.HasSuffix(name, "_leaves"):
		return PathTypeLeaves
	case strings.HasSuffix(name, "_fence") || strings.HasSuffix(name, "_wall") ||
		(strings.HasSuffix(name, "_fence_gate") && world.BlockProperties(state)["open"] != "true"):
		return PathTypeFence
	case strings.HasSuffix(name, "_trapdoor"):
		return PathTypeTrapdoor
	case strings.HasSuffix(name, "_door"):
		open := world.BlockProperties(state)["open"] == "true"
		if open {
			return PathTypeDoorOpen
		}
		if name == "minecraft:iron_door" {
			return PathTypeDoorIronClosed
		}
		return PathTypeDoorWoodClosed
	case strings.HasSuffix(name, "rail"):
		return PathTypeRail
	case name == "minecraft:ladder" || name == "minecraft:vine" || name == "minecraft:scaffolding" ||
		name == "minecraft:weeping_vines" || name == "minecraft:twisting_vines" || name == "minecraft:cave_vines":
		return PathTypeWalkable
	case isPassableBlock(state):
		return PathTypeOpen
	default:
		return PathTypeBlocked
	}
}

func isClimbableBlockState2612(state uint16) bool {
	switch world.BlockName(state) {
	case "minecraft:ladder", "minecraft:vine", "minecraft:scaffolding",
		"minecraft:weeping_vines", "minecraft:weeping_vines_plant",
		"minecraft:twisting_vines", "minecraft:twisting_vines_plant",
		"minecraft:cave_vines", "minecraft:cave_vines_plant":
		return true
	default:
		return false
	}
}

func hasAdjacentSolidWall2612(cache *world.Cache, x, y, z int) bool {
	if cache == nil {
		return false
	}
	dirs := [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
	for _, d := range dirs {
		st := cache.GetBlock(x+d[0], y, z+d[1])
		if isSolidGroundBlock(st) {
			return true
		}
	}
	return false
}

// EvaluateBlockPathType2612 evaluates the effective PathType at feet position
// `(x, y, z)` using the specified NodeEvaluatorKind (`WalkNodeEvaluator`,
// `SwimNodeEvaluator`, `FlyNodeEvaluator`, `AmphibiousNodeEvaluator`, or
// `SpiderClimbEvaluator`) and neighbor hazard checks (`FIRE_IN_NEIGHBOR`,
// `DAMAGING_IN_NEIGHBOR`, `WATER_BORDER`).
func EvaluateBlockPathType2612(cache *world.Cache, x, y, z int, evaluator NodeEvaluatorKind, canOpenDoors, canPassDoors bool) PathType {
	if cache == nil {
		return PathTypeWalkable
	}
	if y <= world.MinY || y >= world.MinY+world.WorldHeight-1 {
		return PathTypeBlocked
	}

	feetState := cache.GetBlock(x, y, z)
	headState := cache.GetBlock(x, y+1, z)
	rawFeet := classifyRawBlockPathType2612(feetState)
	rawHead := classifyRawBlockPathType2612(headState)

	// SwimNodeEvaluator: requires water; air above water is BREACH.
	if evaluator == NodeEvaluatorSwim {
		if rawFeet == PathTypeWater && (rawHead == PathTypeWater || rawHead == PathTypeOpen) {
			return PathTypeWater
		}
		if rawFeet == PathTypeOpen && classifyRawBlockPathType2612(cache.GetBlock(x, y-1, z)) == PathTypeWater {
			return PathTypeBreach
		}
		return PathTypeBlocked
	}

	// Head clearance check (doors occupy 2 blocks, so open/wooden door in head is handled with feet).
	if rawHead != PathTypeOpen && rawHead != PathTypeWater && rawHead != PathTypeDoorOpen &&
		!(rawHead == PathTypeDoorWoodClosed && canOpenDoors && canPassDoors) {
		return PathTypeBlocked
	}

	// Handle doors at feet.
	if rawFeet == PathTypeDoorOpen {
		if !canPassDoors {
			return PathTypeBlocked
		}
		return PathTypeWalkableDoor
	}
	if rawFeet == PathTypeDoorWoodClosed {
		if canOpenDoors && canPassDoors {
			return PathTypeWalkableDoor
		}
		return PathTypeDoorWoodClosed
	}
	if rawFeet == PathTypeDoorIronClosed {
		return PathTypeDoorIronClosed
	}

	// Direct hazards at feet.
	switch rawFeet {
	case PathTypeLava, PathTypeFire, PathTypeDamaging, PathTypeDamageCautious, PathTypeFence, PathTypeLeaves:
		return rawFeet
	case PathTypePowderSnow:
		return PathTypePowderSnow
	case PathTypeWater:
		if evaluator == NodeEvaluatorAmphibious {
			return PathTypeWater
		}
		return PathTypeWater
	}

	// Flying mobs can traverse OPEN air nodes directly.
	if evaluator == NodeEvaluatorFly {
		if rawFeet == PathTypeOpen || rawFeet == PathTypeWalkable {
			return checkNeighborHazards2612(cache, x, y, z, PathTypeOpen)
		}
	}

	// Climbable blocks (ladder/vine) or spider wall climbing.
	if isClimbableBlockState2612(feetState) {
		return PathTypeWalkable
	}
	if evaluator == NodeEvaluatorSpiderClimb && rawFeet == PathTypeOpen && hasAdjacentSolidWall2612(cache, x, y, z) {
		return PathTypeWalkable
	}

	if rawFeet != PathTypeOpen && rawFeet != PathTypeRail && rawFeet != PathTypeCocoa && rawFeet != PathTypeTrapdoor {
		return rawFeet
	}

	// Check supporting block below feet (y - 1).
	belowState := cache.GetBlock(x, y-1, z)
	rawBelow := classifyRawBlockPathType2612(belowState)
	switch rawBelow {
	case PathTypeOpen, PathTypeWater:
		return PathTypeOpen
	case PathTypeLava:
		return PathTypeLava
	case PathTypeFire:
		return PathTypeFire
	case PathTypeDamaging:
		return PathTypeDamaging
	case PathTypePowderSnow:
		return PathTypeOnTopOfPowderSnow
	case PathTypeTrapdoor:
		return PathTypeOnTopOfTrapdoor
	case PathTypeStickyHoney:
		return PathTypeStickyHoney
	case PathTypeFence:
		return PathTypeFence
	case PathTypeLeaves:
		return checkNeighborHazards2612(cache, x, y, z, PathTypeWalkable)
	default:
		if isSolidGroundBlock(belowState) {
			if rawFeet == PathTypeRail {
				return PathTypeRail
			}
			return checkNeighborHazards2612(cache, x, y, z, PathTypeWalkable)
		}
		return PathTypeOpen
	}
}

func checkNeighborHazards2612(cache *world.Cache, x, y, z int, base PathType) PathType {
	if base != PathTypeWalkable && base != PathTypeOpen {
		return base
	}
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			if dx == 0 && dz == 0 {
				continue
			}
			for dy := -1; dy <= 1; dy++ {
				pt := classifyRawBlockPathType2612(cache.GetBlock(x+dx, y+dy, z+dz))
				switch pt {
				case PathTypeFire, PathTypeLava:
					return PathTypeFireInNeighbor
				case PathTypeDamaging:
					return PathTypeDamagingInNeighbor
				case PathTypeWater:
					if dy == -1 && base == PathTypeWalkable {
						return PathTypeWaterBorder
					}
				}
			}
		}
	}
	return base
}

type astarNode2612 struct {
	pos   PathNode
	g     float32
	f     float32
	parent int
	index int
}

type astarQueue2612 []*astarNode2612

func (pq astarQueue2612) Len() int           { return len(pq) }
func (pq astarQueue2612) Less(i, j int) bool { return pq[i].f < pq[j].f }
func (pq astarQueue2612) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}
func (pq *astarQueue2612) Push(x any) {
	n := x.(*astarNode2612)
	n.index = len(*pq)
	*pq = append(*pq, n)
}
func (pq *astarQueue2612) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*pq = old[:n-1]
	return item
}

func octileHeuristic3D(a, b PathNode) float32 {
	dx := float64(a.X - b.X)
	dy := float64(a.Y - b.Y)
	dz := float64(a.Z - b.Z)
	return float32(math.Sqrt(dx*dx + dy*dy + dz*dz))
}

// FindPathWithEvaluator2612 performs a malus-aware A* search for mobType from
// `start` to `goal`, returning the path nodes, total path cost (including
// PathType maluses), and whether the exact goal was reached.
func FindPathWithEvaluator2612(
	cache *world.Cache,
	mobType string,
	startX, startY, startZ int,
	goalX, goalY, goalZ int,
	canOpenDoors, canPassDoors bool,
	maxVisited int,
) (path []PathNode, totalCost float32, reached bool) {
	start := PathNode{X: startX, Y: startY, Z: startZ}
	goal := PathNode{X: goalX, Y: goalY, Z: goalZ}
	if start == goal {
		return []PathNode{start}, 0, true
	}
	if maxVisited <= 0 {
		maxVisited = MaxPathSearchNodes
	}
	evaluator := NodeEvaluatorForMobType(mobType)

	var nodes []astarNode2612
	nodes = append(nodes, astarNode2612{
		pos:    start,
		g:      0,
		f:      octileHeuristic3D(start, goal),
		parent: -1,
	})

	open := make(astarQueue2612, 0, 32)
	heap.Init(&open)
	heap.Push(&open, &nodes[0])

	bestG := map[PathNode]float32{start: 0}
	closed := make(map[PathNode]bool, 64)

	bestIdx := 0
	bestH := octileHeuristic3D(start, goal)

	cardinalDirs := [8][2]int{
		{1, 0}, {-1, 0}, {0, 1}, {0, -1},
		{1, 1}, {1, -1}, {-1, 1}, {-1, -1},
	}

	visited := 0
	for open.Len() > 0 && visited < maxVisited {
		cur := heap.Pop(&open).(*astarNode2612)
		if closed[cur.pos] {
			continue
		}
		closed[cur.pos] = true
		visited++

		curIdx := -1
		for i := range nodes {
			if nodes[i].pos == cur.pos {
				curIdx = i
				break
			}
		}
		h := octileHeuristic3D(cur.pos, goal)
		if h < bestH {
			bestH = h
			bestIdx = curIdx
		}
		if cur.pos == goal {
			return reconstructPath2612(nodes, curIdx), cur.g, true
		}

		// Candidate neighbors: horizontal + step up/down + vertical (swim/fly/climb).
		var candidates []PathNode
		for _, d := range cardinalDirs {
			nx := cur.pos.X + d[0]
			nz := cur.pos.Z + d[1]
			isDiag := d[0] != 0 && d[1] != 0
			switch evaluator {
			case NodeEvaluatorSwim, NodeEvaluatorFly:
				for dy := -1; dy <= 1; dy++ {
					if isDiag && dy != 0 {
						continue
					}
					candidates = append(candidates, PathNode{X: nx, Y: cur.pos.Y + dy, Z: nz})
				}
			default:
				// Same Y, step up +1, or step down up to MaxStepDownBlocks.
				candidates = append(candidates, PathNode{X: nx, Y: cur.pos.Y, Z: nz})
				if !isDiag {
					candidates = append(candidates, PathNode{X: nx, Y: cur.pos.Y + 1, Z: nz})
				}
				for drop := 1; drop <= MaxStepDownBlocks; drop++ {
					candidates = append(candidates, PathNode{X: nx, Y: cur.pos.Y - drop, Z: nz})
				}
			}
		}
		// Pure vertical transitions for swim, fly, ladder/vine climbing, or spider wall climbing.
		if evaluator == NodeEvaluatorSwim || evaluator == NodeEvaluatorFly || evaluator == NodeEvaluatorSpiderClimb ||
			(cache != nil && isClimbableBlockState2612(cache.GetBlock(cur.pos.X, cur.pos.Y, cur.pos.Z))) {
			candidates = append(candidates,
				PathNode{X: cur.pos.X, Y: cur.pos.Y + 1, Z: cur.pos.Z},
				PathNode{X: cur.pos.X, Y: cur.pos.Y - 1, Z: cur.pos.Z},
			)
		}

		for _, nextPos := range candidates {
			if closed[nextPos] {
				continue
			}
			pt := EvaluateBlockPathType2612(cache, nextPos.X, nextPos.Y, nextPos.Z, evaluator, canOpenDoors, canPassDoors)
			// For WalkNodeEvaluator, OPEN (unsupported mid-air) is not directly walkable unless climbing/falling onto ground.
			if evaluator == NodeEvaluatorWalk && pt == PathTypeOpen {
				continue
			}
			malus := MobPathfindingMalus(mobType, pt)
			if malus < 0 {
				continue
			}
			stepDist := octileHeuristic3D(cur.pos, nextPos)
			tentativeG := cur.g + stepDist + malus
			if prev, exists := bestG[nextPos]; exists && tentativeG >= prev {
				continue
			}
			bestG[nextPos] = tentativeG
			nodes = append(nodes, astarNode2612{
				pos:    nextPos,
				g:      tentativeG,
				f:      tentativeG + octileHeuristic3D(nextPos, goal),
				parent: curIdx,
			})
			heap.Push(&open, &nodes[len(nodes)-1])
		}
	}

	return reconstructPath2612(nodes, bestIdx), nodes[bestIdx].g, bestIdx >= 0 && nodes[bestIdx].pos == goal
}

func reconstructPath2612(nodes []astarNode2612, idx int) []PathNode {
	if idx < 0 || idx >= len(nodes) {
		return nil
	}
	var rev []PathNode
	for c := idx; c >= 0; c = nodes[c].parent {
		rev = append(rev, nodes[c].pos)
	}
	out := make([]PathNode, len(rev))
	for i := range rev {
		out[i] = rev[len(rev)-1-i]
	}
	return out
}

// --- 2. Brain / Goal Infrastructure (GoalFlag, GoalSelector2612, MemoryModuleType, Sensor, Activity) ---

// GoalFlag represents vanilla Goal.Flag bitmask controls (`MOVE`, `LOOK`, `JUMP`, `TARGET`).
type GoalFlag uint8

const (
	GoalFlagMove   GoalFlag = 1 << 0
	GoalFlagLook   GoalFlag = 1 << 1
	GoalFlagJump   GoalFlag = 1 << 2
	GoalFlagTarget GoalFlag = 1 << 3
)

// PrioritizedGoal2612 represents a Goal registered in GoalSelector2612.
type PrioritizedGoal2612 struct {
	Name          string
	Priority      int // Lower number = higher priority in vanilla GoalSelector
	Flags         GoalFlag
	Interruptible bool
	CanUse        func() bool
	CanContinue   func() bool
	OnStart       func()
	OnStop        func()
	OnTick        func()
	Running       bool
}

// GoalSelector2612 manages prioritized goals with Goal.Flag conflict resolution
// matching vanilla `net.minecraft.world.entity.ai.goal.GoalSelector`.
type GoalSelector2612 struct {
	Goals        []*PrioritizedGoal2612
	DisabledMask GoalFlag
}

// AddGoal registers a goal with the given priority and flags.
func (gs *GoalSelector2612) AddGoal(priority int, name string, flags GoalFlag, interruptible bool, canUse, canContinue func() bool, onStart, onStop, onTick func()) *PrioritizedGoal2612 {
	g := &PrioritizedGoal2612{
		Name:          name,
		Priority:      priority,
		Flags:         flags,
		Interruptible: interruptible,
		CanUse:        canUse,
		CanContinue:   canContinue,
		OnStart:       onStart,
		OnStop:        onStop,
		OnTick:        onTick,
	}
	gs.Goals = append(gs.Goals, g)
	return g
}

func goalsConflict2612(a, b *PrioritizedGoal2612) bool {
	return (a.Flags & b.Flags) != 0
}

func canGoalBeReplacedBy2612(running, candidate *PrioritizedGoal2612) bool {
	return running.Interruptible && candidate.Priority < running.Priority
}

// Tick evaluates goal start/continue/preempt conditions and ticks all active goals.
func (gs *GoalSelector2612) Tick() {
	if gs == nil {
		return
	}
	// 1. Cleanup goals that are disabled or can no longer continue.
	for _, g := range gs.Goals {
		if !g.Running {
			continue
		}
		cont := g.CanContinue
		if cont == nil {
			cont = g.CanUse
		}
		if (g.Flags&gs.DisabledMask) != 0 || (cont != nil && !cont()) {
			g.Running = false
			if g.OnStop != nil {
				g.OnStop()
			}
		}
	}

	// 2. Try starting goals that can run and either don't conflict with running
	// goals or preempt higher-priority-number running goals.
	for _, cand := range gs.Goals {
		if cand.Running || (cand.Flags&gs.DisabledMask) != 0 {
			continue
		}
		if cand.CanUse != nil && !cand.CanUse() {
			continue
		}
		blocked := false
		for _, other := range gs.Goals {
			if other == cand || !other.Running {
				continue
			}
			if goalsConflict2612(other, cand) && !canGoalBeReplacedBy2612(other, cand) {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		for _, other := range gs.Goals {
			if other != cand && other.Running && goalsConflict2612(other, cand) {
				other.Running = false
				if other.OnStop != nil {
					other.OnStop()
				}
			}
		}
		cand.Running = true
		if cand.OnStart != nil {
			cand.OnStart()
		}
	}

	// 3. Tick all running goals.
	for _, g := range gs.Goals {
		if g.Running && g.OnTick != nil {
			g.OnTick()
		}
	}
}

// MemoryModuleType identifies a Brain memory slot in Minecraft 26.1.2.
type MemoryModuleType string

const (
	MemoryAttackTarget          MemoryModuleType = "minecraft:attack_target"
	MemoryHurtBy                MemoryModuleType = "minecraft:hurt_by"
	MemoryHurtByEntity          MemoryModuleType = "minecraft:hurt_by_entity"
	MemoryNearestLivingEntities MemoryModuleType = "minecraft:nearest_living_entities"
	MemoryNearestVisiblePlayers MemoryModuleType = "minecraft:nearest_visible_player"
	MemoryWalkTarget            MemoryModuleType = "minecraft:walk_target"
	MemoryLookTarget            MemoryModuleType = "minecraft:look_target"
	MemoryPath                  MemoryModuleType = "minecraft:path"
	MemoryAngryAt               MemoryModuleType = "minecraft:angry_at"
	MemoryUniversalAnger        MemoryModuleType = "minecraft:universal_anger"
	MemoryAdmiringItem          MemoryModuleType = "minecraft:admiring_item"
	MemoryHome                  MemoryModuleType = "minecraft:home"
	MemoryJobSite               MemoryModuleType = "minecraft:job_site"
	MemoryMeetingPoint          MemoryModuleType = "minecraft:meeting_point"
	MemoryIsPanicking           MemoryModuleType = "minecraft:is_panicking"
	MemoryRoarTarget            MemoryModuleType = "minecraft:roar_target"
	MemoryDisturbanceLocation   MemoryModuleType = "minecraft:disturbance_location"
	MemoryRamCooldownTicks      MemoryModuleType = "minecraft:ram_cooldown_ticks"
	MemoryLongJumpCoolingDown   MemoryModuleType = "minecraft:long_jump_cooling_down"
	MemoryIsTempted             MemoryModuleType = "minecraft:is_tempted"
	MemoryBreedTarget           MemoryModuleType = "minecraft:breed_target"
	MemoryAvoidTarget           MemoryModuleType = "minecraft:avoid_target"
)

// ActivityType identifies a Brain Activity in Minecraft 26.1.2.
type ActivityType string

const (
	ActivityCore        ActivityType = "minecraft:core"
	ActivityIdle        ActivityType = "minecraft:idle"
	ActivityWork        ActivityType = "minecraft:work"
	ActivityPlay        ActivityType = "minecraft:play"
	ActivityRest        ActivityType = "minecraft:rest"
	ActivityMeet        ActivityType = "minecraft:meet"
	ActivityPanic       ActivityType = "minecraft:panic"
	ActivityRaid        ActivityType = "minecraft:raid"
	ActivityPreRaid     ActivityType = "minecraft:pre_raid"
	ActivityHide        ActivityType = "minecraft:hide"
	ActivityFight       ActivityType = "minecraft:fight"
	ActivityCelebrate   ActivityType = "minecraft:celebrate"
	ActivityAdmireItem  ActivityType = "minecraft:admire_item"
	ActivityAvoid       ActivityType = "minecraft:avoid"
	ActivityRide        ActivityType = "minecraft:ride"
	ActivityRoar        ActivityType = "minecraft:roar"
	ActivityInvestigate ActivityType = "minecraft:investigate"
	ActivitySniff       ActivityType = "minecraft:sniff"
	ActivityEmerge      ActivityType = "minecraft:emerge"
	ActivityDig         ActivityType = "minecraft:dig"
	ActivityLongJump    ActivityType = "minecraft:long_jump"
	ActivityRam         ActivityType = "minecraft:ram"
	ActivityTongue      ActivityType = "minecraft:tongue"
	ActivitySwim        ActivityType = "minecraft:swim"
	ActivityLaySpawn    ActivityType = "minecraft:lay_spawn"
)

// DefaultSensorScanRateTicks is `Sensor.DEFAULT_SCAN_RATE` (20 ticks) in 26.1.2.
const DefaultSensorScanRateTicks = 20

// ExpirableMemory stores a Brain memory value with an optional TTL in ticks
// (`ExpirableValue<T>` in vanilla 26.1.2).
type ExpirableMemory struct {
	Value       any
	HasExpiry   bool
	TimeToLive  int64
}

// BrainSensor2612 represents a periodic Brain Sensor (`Sensor<E>`).
type BrainSensor2612 struct {
	Name          string
	ScanRateTicks int
	CooldownTicks int
	Scan          func(brain *MobBrain2612)
}

// MobBrain2612 models `net.minecraft.world.entity.ai.Brain` with expirable
// memories, periodic sensors, and active activities.
type MobBrain2612 struct {
	Memories         map[MemoryModuleType]ExpirableMemory
	Sensors          []*BrainSensor2612
	CoreActivities   map[ActivityType]bool
	ActiveActivities map[ActivityType]bool
	DefaultActivity  ActivityType
}

// NewMobBrain2612 creates a new MobBrain2612 with `core` and `idle` default activities.
func NewMobBrain2612() *MobBrain2612 {
	return &MobBrain2612{
		Memories:         make(map[MemoryModuleType]ExpirableMemory),
		CoreActivities:   map[ActivityType]bool{ActivityCore: true},
		ActiveActivities: map[ActivityType]bool{ActivityCore: true, ActivityIdle: true},
		DefaultActivity:  ActivityIdle,
	}
}

// SetMemory stores a non-expiring value in key.
func (b *MobBrain2612) SetMemory(key MemoryModuleType, value any) {
	if b == nil {
		return
	}
	if b.Memories == nil {
		b.Memories = make(map[MemoryModuleType]ExpirableMemory)
	}
	b.Memories[key] = ExpirableMemory{Value: value, HasExpiry: false}
}

// SetMemoryWithTTL stores an expiring value in key that automatically expires
// after `ttlTicks` ticks.
func (b *MobBrain2612) SetMemoryWithTTL(key MemoryModuleType, value any, ttlTicks int64) {
	if b == nil {
		return
	}
	if ttlTicks <= 0 {
		b.EraseMemory(key)
		return
	}
	if b.Memories == nil {
		b.Memories = make(map[MemoryModuleType]ExpirableMemory)
	}
	b.Memories[key] = ExpirableMemory{
		Value:      value,
		HasExpiry:  true,
		TimeToLive: ttlTicks,
	}
}

// GetMemory returns the value stored in key and whether it is present.
func (b *MobBrain2612) GetMemory(key MemoryModuleType) (any, bool) {
	if b == nil || b.Memories == nil {
		return nil, false
	}
	entry, ok := b.Memories[key]
	if !ok {
		return nil, false
	}
	return entry.Value, true
}

// HasMemoryValue reports whether key is currently set in the Brain.
func (b *MobBrain2612) HasMemoryValue(key MemoryModuleType) bool {
	_, ok := b.GetMemory(key)
	return ok
}

// EraseMemory removes key from the Brain.
func (b *MobBrain2612) EraseMemory(key MemoryModuleType) {
	if b == nil || b.Memories == nil {
		return
	}
	delete(b.Memories, key)
}

// SetActiveActivity switches the primary non-core activity to act.
func (b *MobBrain2612) SetActiveActivity(act ActivityType) {
	if b == nil {
		return
	}
	b.ActiveActivities = make(map[ActivityType]bool, len(b.CoreActivities)+1)
	for k := range b.CoreActivities {
		b.ActiveActivities[k] = true
	}
	b.ActiveActivities[act] = true
}

// IsActivityActive reports whether act is currently active.
func (b *MobBrain2612) IsActivityActive(act ActivityType) bool {
	if b == nil {
		return false
	}
	return b.ActiveActivities[act]
}

// AddSensor attaches a periodic sensor to the Brain.
func (b *MobBrain2612) AddSensor(name string, scanRateTicks int, scan func(brain *MobBrain2612)) {
	if b == nil {
		return
	}
	if scanRateTicks <= 0 {
		scanRateTicks = DefaultSensorScanRateTicks
	}
	b.Sensors = append(b.Sensors, &BrainSensor2612{
		Name:          name,
		ScanRateTicks: scanRateTicks,
		CooldownTicks: 0,
		Scan:          scan,
	})
}

// Tick advances all expirable memory TTLs (`tickMemories`) and runs any sensors
// whose scan cooldown has elapsed (`tickSensors`).
func (b *MobBrain2612) Tick() {
	if b == nil {
		return
	}
	for k, mem := range b.Memories {
		if !mem.HasExpiry {
			continue
		}
		mem.TimeToLive--
		if mem.TimeToLive <= 0 {
			delete(b.Memories, k)
		} else {
			b.Memories[k] = mem
		}
	}
	for _, s := range b.Sensors {
		s.CooldownTicks--
		if s.CooldownTicks <= 0 {
			s.CooldownTicks = s.ScanRateTicks
			if s.Scan != nil {
				s.Scan(b)
			}
		}
	}
}

// --- 3. TargetingConditions, Visibility Multiplier, Line-of-Sight & NeutralMob Anger ---

// Vanilla 26.1.2 `NeutralMob` persistent anger timer bounds (`TimeUtil.rangeOfSeconds(20, 39)`).
const (
	NeutralMobAngerMinTicks = 400 // 20 seconds
	NeutralMobAngerMaxTicks = 780 // 39 seconds
)

// ComputeTargetVisibilityMultiplier2612 computes `LivingEntity.getVisibilityPercent`
// in vanilla 26.1.2:
//   - Base visibility is `1.0`.
//   - Discrete sneaking (`isDiscrete()`) multiplies visibility by `0.8`.
//   - Invisibility (`isInvisible()`) multiplies visibility by `max(0.07, armorCoverFraction * 0.7)`
//     where `armorCoverFraction = float64(armorEquippedSlots) / 4.0`.
//   - Wearing the skull/head matching the observer mob type (`wearingMatchingMobHead`)
//     multiplies visibility by `0.5`.
func ComputeTargetVisibilityMultiplier2612(sneaking, invisible bool, armorEquippedSlots int, wearingMatchingMobHead bool) float64 {
	mult := 1.0
	if sneaking {
		mult *= 0.8
	}
	if invisible {
		if armorEquippedSlots < 0 {
			armorEquippedSlots = 0
		}
		if armorEquippedSlots > 4 {
			armorEquippedSlots = 4
		}
		cover := float64(armorEquippedSlots) / 4.0
		if cover < 0.1 {
			cover = 0.1
		}
		mult *= 0.7 * cover
	}
	if wearingMatchingMobHead {
		mult *= 0.5
	}
	return mult
}

// EffectiveFollowRange2612 returns the effective detection distance for a mob
// with `baseFollowRange` targeting an entity with visibility multiplier `visMult`
// (`max(baseFollowRange * visMult, 2.0)` in `TargetingConditions.test`).
func EffectiveFollowRange2612(baseFollowRange, visMult float64) float64 {
	if baseFollowRange <= 0 {
		return 0
	}
	scaled := baseFollowRange * visMult
	if scaled < 2.0 {
		return 2.0
	}
	return scaled
}

// HasLineOfSight2612 performs a 3D DDA voxel raycast between eye positions
// `(x0, y0, z0)` and `(x1, y1, z1)` (`Sensing.hasLineOfSight` / `ClipContext.Block.COLLIDER`
// in vanilla 26.1.2). Distances > 128 blocks immediately return false.
func HasLineOfSight2612(cache *world.Cache, x0, y0, z0, x1, y1, z1 float64) bool {
	dx := x1 - x0
	dy := y1 - y0
	dz := z1 - z0
	distSq := dx*dx + dy*dy + dz*dz
	if distSq > 128.0*128.0 {
		return false
	}
	if cache == nil || distSq < 1e-6 {
		return true
	}
	dist := math.Sqrt(distSq)
	steps := int(math.Ceil(dist * 4.0))
	if steps < 1 {
		steps = 1
	}
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		bx := int(math.Floor(x0 + dx*t))
		by := int(math.Floor(y0 + dy*t))
		bz := int(math.Floor(z0 + dz*t))
		if by < world.MinY || by >= world.MinY+world.WorldHeight {
			continue
		}
		st := cache.GetBlock(bx, by, bz)
		if st == world.StateAir || st == world.StateWater || isPassableBlock(st) {
			continue
		}
		return false
	}
	return true
}

// RollPersistentAngerTime2612 rolls a random persistent anger duration in
// `[400, 780]` ticks (`TimeUtil.rangeOfSeconds(20, 39)` in `26.1.2`).
func RollPersistentAngerTime2612(rng *rand.Rand) int {
	span := NeutralMobAngerMaxTicks - NeutralMobAngerMinTicks + 1
	if rng == nil {
		return NeutralMobAngerMinTicks + rand.Intn(span)
	}
	return NeutralMobAngerMinTicks + rng.Intn(span)
}

// NeutralMobAngerState tracks `NeutralMob` persistent anger in vanilla 26.1.2
// (used by Wolf, Bee, IronGolem, PolarBear, ZombifiedPiglin, Enderman, Spider, Llama, Goat, Dolphin).
type NeutralMobAngerState struct {
	RemainingAngerTime int
	AngryAtTargetID    int32
	AngryAtPlayerUUID  [16]byte
	HasAngryAtPlayer   bool
}

// StartAnger initializes or refreshes the persistent anger timer (`400..780` ticks)
// against targetEntityID / playerUUID.
func (a *NeutralMobAngerState) StartAnger(targetEntityID int32, playerUUID [16]byte, isPlayer bool, rng *rand.Rand) {
	if a == nil {
		return
	}
	a.RemainingAngerTime = RollPersistentAngerTime2612(rng)
	a.AngryAtTargetID = targetEntityID
	a.AngryAtPlayerUUID = playerUUID
	a.HasAngryAtPlayer = isPlayer
}

// StopAnger clears all anger state (`NeutralMob.stopBeingAngry`).
func (a *NeutralMobAngerState) StopAnger() {
	if a == nil {
		return
	}
	a.RemainingAngerTime = 0
	a.AngryAtTargetID = 0
	a.AngryAtPlayerUUID = [16]byte{}
	a.HasAngryAtPlayer = false
}

// IsAngry reports whether the NeutralMob is currently angry (`RemainingAngerTime > 0`).
func (a *NeutralMobAngerState) IsAngry() bool {
	return a != nil && a.RemainingAngerTime > 0
}

// IsAngryAtPlayer reports whether the NeutralMob is angry at `playerUUID`
// (either directly or via `universalAnger` gamerule when already angry).
func (a *NeutralMobAngerState) IsAngryAtPlayer(playerUUID [16]byte, universalAnger bool) bool {
	if !a.IsAngry() {
		return false
	}
	if universalAnger {
		return true
	}
	return a.HasAngryAtPlayer && a.AngryAtPlayerUUID == playerUUID
}

// UpdatePersistentAnger ticks `NeutralMob.updatePersistentAnger`:
//   - If `targetDead && forgiveDeadPlayers`, clears the dead target; if `!universalAnger`,
//     stops anger completely (`RemainingAngerTime = 0`).
//   - Otherwise decrements `RemainingAngerTime` by 1 tick and clears the target
//     when the timer reaches 0.
func (a *NeutralMobAngerState) UpdatePersistentAnger(targetDead, forgiveDeadPlayers, universalAnger bool) {
	if a == nil || a.RemainingAngerTime <= 0 {
		return
	}
	if targetDead && forgiveDeadPlayers {
		a.AngryAtTargetID = 0
		a.AngryAtPlayerUUID = [16]byte{}
		a.HasAngryAtPlayer = false
		if !universalAnger {
			a.RemainingAngerTime = 0
			return
		}
	}
	a.RemainingAngerTime--
	if a.RemainingAngerTime <= 0 {
		a.StopAnger()
	}
}

// --- 4. NameTag (minecraft:name_tag), CustomName & PersistenceRequired ---

const NameTagItemID = "minecraft:name_tag"

type mobCustomMetaState struct {
	CustomName          string
	CustomNameVisible   bool
	PersistenceRequired bool
	NoActionTicks       int
	Anger               NeutralMobAngerState
}

type serverMobSystemsManager struct {
	mu   sync.RWMutex
	meta map[int32]*mobCustomMetaState
}

var mobSystemsManagers sync.Map // map[*Server]*serverMobSystemsManager

func (s *Server) mobSystemsMgr() *serverMobSystemsManager {
	if s == nil {
		return &serverMobSystemsManager{meta: make(map[int32]*mobCustomMetaState)}
	}
	if v, ok := mobSystemsManagers.Load(s); ok {
		return v.(*serverMobSystemsManager)
	}
	mgr := &serverMobSystemsManager{meta: make(map[int32]*mobCustomMetaState)}
	actual, _ := mobSystemsManagers.LoadOrStore(s, mgr)
	return actual.(*serverMobSystemsManager)
}

func (mgr *serverMobSystemsManager) getOrCreate(entityID int32) *mobCustomMetaState {
	st, ok := mgr.meta[entityID]
	if !ok {
		st = &mobCustomMetaState{}
		mgr.meta[entityID] = st
	}
	return st
}

// SetMobCustomName2612 sets the CustomName on any mob entity and propagates to
// species-specific name handlers (e.g. Rabbit `"Toast"`, Vindicator `"Johnny"`,
// Bee, Wither, CopperGolem).
func (s *Server) SetMobCustomName2612(entityID int32, customName string) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	mgr := s.mobSystemsMgr()
	mgr.mu.Lock()
	st := mgr.getOrCreate(entityID)
	st.CustomName = customName
	mgr.mu.Unlock()

	switch canonicalResourceName(ent.TypeName) {
	case MobTypeRabbit:
		s.ApplyRabbitCustomName(entityID, customName)
	case MobTypeVindicator:
		s.SetVindicatorCustomName(entityID, customName)
	case MobTypeBee:
		s.SetBeeCustomName(entityID, customName)
	case MobTypeWither:
		s.SetWitherCustomName(entityID, customName)
	case MobTypeCopperGolem:
		s.SetCopperGolemCustomName(entityID, customName)
	}
	return true
}

// GetMobCustomName2612 returns the CustomName assigned to entityID.
func (s *Server) GetMobCustomName2612(entityID int32) string {
	if s == nil || entityID <= 0 {
		return ""
	}
	mgr := s.mobSystemsMgr()
	mgr.mu.RLock()
	st := mgr.meta[entityID]
	var name string
	if st != nil {
		name = st.CustomName
	}
	mgr.mu.RUnlock()
	if name != "" {
		return name
	}
	if s.entities != nil {
		if ent := s.entities.Get(entityID); ent != nil {
			switch canonicalResourceName(ent.TypeName) {
			case MobTypeBee:
				return s.BeeCustomName(entityID)
			}
		}
	}
	return ""
}

// SetMobPersistenceRequired2612 sets `PersistenceRequired` on entityID so it
// never despawns naturally.
func (s *Server) SetMobPersistenceRequired2612(entityID int32, persistent bool) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	mgr := s.mobSystemsMgr()
	mgr.mu.Lock()
	st := mgr.getOrCreate(entityID)
	st.PersistenceRequired = persistent
	mgr.mu.Unlock()

	switch canonicalResourceName(ent.TypeName) {
	case MobTypeEndermite:
		s.SetEndermitePersistenceRequired(entityID, persistent)
	case MobTypeGuardian, MobTypeElderGuardian:
		s.SetGuardianPersistenceRequired(entityID, persistent)
	case MobTypeCamelHusk:
		chMgr := s.happyGhastParchedMgr()
		chMgr.mu.Lock()
		if ch, ok := chMgr.camelHusks[entityID]; ok && ch != nil {
			ch.PersistenceRequired = persistent
		}
		chMgr.mu.Unlock()
	}
	return true
}

// IsMobPersistenceRequired2612 reports whether entityID has `PersistenceRequired`
// set (including nametagged mobs, tamed mobs, leashed mobs, or species-specific persistence).
func (s *Server) IsMobPersistenceRequired2612(entityID int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	mgr := s.mobSystemsMgr()
	mgr.mu.RLock()
	st := mgr.meta[entityID]
	pers := st != nil && (st.PersistenceRequired || st.CustomName != "")
	mgr.mu.RUnlock()
	if pers {
		return true
	}
	if s.IsEntityLeashed(entityID) {
		return true
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	switch canonicalResourceName(ent.TypeName) {
	case MobTypeCamelHusk:
		if ch, ok := s.GetCamelHuskState(entityID); ok && ch.PersistenceRequired {
			return true
		}
	}
	return false
}

// ApplyNameTagToMob applies a renamed `minecraft:name_tag` (`NameTagItem.interactLivingEntity`)
// to `entityID`:
//   - Requires non-empty `customName` and a living non-player, non-EnderDragon target.
//   - Sets `CustomName = customName` and `PersistenceRequired = true`.
//   - Consumes 1 item from `session`'s selected hotbar slot when not in Creative mode.
func (s *Server) ApplyNameTagToMob(session *PlayerSession, entityID int32, customName string) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	trimmed := strings.TrimSpace(customName)
	if trimmed == "" {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.IsItem() || ent.IsExperienceOrb() || IsDecorationEntity(ent.TypeName) ||
		IsStandardProjectileEntityType(ent.TypeName) || IsVehicleEntity(ent.TypeName) ||
		canonicalResourceName(ent.TypeName) == MobTypeEnderDragon {
		return false
	}
	if !s.SetMobCustomName2612(entityID, trimmed) {
		return false
	}
	s.SetMobPersistenceRequired2612(entityID, true)

	if session != nil {
		snap := session.Snapshot()
		if snap.GameMode != GameModeCreative {
			slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
			held := session.InventorySlot(slotIdx)
			if canonicalResourceName(held.ResolvedID()) == NameTagItemID && held.Count > 0 {
				if held.Count <= 1 {
					session.SetInventorySlot(slotIdx, world.ItemStack{})
				} else {
					held.Count--
					session.SetInventorySlot(slotIdx, held)
				}
			}
		}
	}
	return true
}

// --- 5. MobCategory, NaturalSpawner (289 chunk cap), LocalMobCapCalculator & Despawn ---

// MobCategory2612 represents `net.minecraft.world.entity.MobCategory` in 26.1.2.
type MobCategory2612 uint8

const (
	MobCategoryMonster MobCategory2612 = iota
	MobCategoryCreature
	MobCategoryAmbient
	MobCategoryAxolotls
	MobCategoryUndergroundWaterCreature
	MobCategoryWaterCreature
	MobCategoryWaterAmbient
	MobCategoryMisc

	MobCategoryCount2612 = 8

	// NaturalSpawner.MAGIC_NUMBER = 17 * 17 = 289 in 26.1.2.
	NaturalSpawnerMagicNumber = 289

	// Mob.checkDespawn idle threshold (600 ticks = 30s) and random chance (1/800).
	MobNoActionDespawnThresholdTicks = 600
	MobRandomDespawnChanceReciprocal = 800
)

type mobCategorySpec2612 struct {
	name              string
	maxPerChunkCap    int
	friendly          bool
	persistent        bool
	despawnDistance   int
	noDespawnDistance int
}

var vanillaMobCategoryTable = [MobCategoryCount2612]mobCategorySpec2612{
	MobCategoryMonster: {
		name:              "monster",
		maxPerChunkCap:    70,
		friendly:          false,
		persistent:        false,
		despawnDistance:   128,
		noDespawnDistance: 32,
	},
	MobCategoryCreature: {
		name:              "creature",
		maxPerChunkCap:    10,
		friendly:          true,
		persistent:        true,
		despawnDistance:   128,
		noDespawnDistance: 32,
	},
	MobCategoryAmbient: {
		name:              "ambient",
		maxPerChunkCap:    15,
		friendly:          true,
		persistent:        false,
		despawnDistance:   128,
		noDespawnDistance: 32,
	},
	MobCategoryAxolotls: {
		name:              "axolotls",
		maxPerChunkCap:    5,
		friendly:          true,
		persistent:        false,
		despawnDistance:   128,
		noDespawnDistance: 32,
	},
	MobCategoryUndergroundWaterCreature: {
		name:              "underground_water_creature",
		maxPerChunkCap:    5,
		friendly:          true,
		persistent:        false,
		despawnDistance:   128,
		noDespawnDistance: 32,
	},
	MobCategoryWaterCreature: {
		name:              "water_creature",
		maxPerChunkCap:    5,
		friendly:          true,
		persistent:        false,
		despawnDistance:   128,
		noDespawnDistance: 32,
	},
	MobCategoryWaterAmbient: {
		name:              "water_ambient",
		maxPerChunkCap:    20,
		friendly:          true,
		persistent:        false,
		despawnDistance:   64,
		noDespawnDistance: 32,
	},
	MobCategoryMisc: {
		name:              "misc",
		maxPerChunkCap:    -1,
		friendly:          true,
		persistent:        true,
		despawnDistance:   128,
		noDespawnDistance: 32,
	},
}

func (c MobCategory2612) String() string {
	if int(c) >= 0 && int(c) < len(vanillaMobCategoryTable) {
		return vanillaMobCategoryTable[c].name
	}
	return "misc"
}

// MaxInstancesPerChunk returns `MobCategory.getMaxInstancesPerChunk()` (`70`, `10`, `15`, `5`, `5`, `5`, `20`, `-1`).
func (c MobCategory2612) MaxInstancesPerChunk() int {
	if int(c) >= 0 && int(c) < len(vanillaMobCategoryTable) {
		return vanillaMobCategoryTable[c].maxPerChunkCap
	}
	return -1
}

// IsFriendly returns `MobCategory.isFriendly()`.
func (c MobCategory2612) IsFriendly() bool {
	if int(c) >= 0 && int(c) < len(vanillaMobCategoryTable) {
		return vanillaMobCategoryTable[c].friendly
	}
	return true
}

// IsPersistent returns `MobCategory.isPersistent()`.
func (c MobCategory2612) IsPersistent() bool {
	if int(c) >= 0 && int(c) < len(vanillaMobCategoryTable) {
		return vanillaMobCategoryTable[c].persistent
	}
	return true
}

// DespawnDistance returns `MobCategory.getDespawnDistance()` (`128` for most categories, `64` for `WATER_AMBIENT`).
func (c MobCategory2612) DespawnDistance() int {
	if int(c) >= 0 && int(c) < len(vanillaMobCategoryTable) {
		return vanillaMobCategoryTable[c].despawnDistance
	}
	return 128
}

// NoDespawnDistance returns `MobCategory.getNoDespawnDistance()` (`32` for all categories).
func (c MobCategory2612) NoDespawnDistance() int {
	if int(c) >= 0 && int(c) < len(vanillaMobCategoryTable) {
		return vanillaMobCategoryTable[c].noDespawnDistance
	}
	return 32
}

// MobCategoryForEntityType returns the vanilla 26.1.2 MobCategory2612 for an entity type.
func MobCategoryForEntityType(typeName string) MobCategory2612 {
	switch canonicalResourceName(typeName) {
	case MobTypeBat:
		return MobCategoryAmbient
	case MobTypeAxolotl:
		return MobCategoryAxolotls
	case MobTypeGlowSquid:
		return MobCategoryUndergroundWaterCreature
	case MobTypeSquid, MobTypeDolphin, MobTypeNautilus:
		return MobCategoryWaterCreature
	case MobTypeCod, MobTypeSalmon, MobTypeTropicalFish, MobTypePufferfish:
		return MobCategoryWaterAmbient
	case MobTypeZombie, MobTypeZombieVillager, MobTypeHusk, MobTypeDrowned,
		MobTypeSkeleton, MobTypeStray, MobTypeBogged, MobTypeParched, MobTypeWitherSkeleton,
		MobTypeCreeper, MobTypeSpider, MobTypeCaveSpider, MobTypeEnderman, MobTypeEndermite,
		MobTypeSilverfish, MobTypeSlime, MobTypeMagmaCube, MobTypeBlaze, MobTypeGhast,
		MobTypePhantom, MobTypeBreeze, MobTypeWitch, MobTypeVindicator, MobTypePillager,
		MobTypeEvoker, MobTypeRavager, MobTypeVex, MobTypeIllusioner,
		MobTypeGuardian, MobTypeElderGuardian, MobTypeShulker,
		MobTypePiglin, MobTypePiglinBrute, MobTypeZombifiedPiglin, MobTypeHoglin, MobTypeZoglin,
		MobTypeWarden, MobTypeWither, MobTypeEnderDragon, MobTypeCreaking, MobTypeZombieNautilus, MobTypeCamelHusk:
		return MobCategoryMonster
	case MobTypePig, MobTypeCow, MobTypeMooshroom, MobTypeSheep, MobTypeChicken,
		MobTypeRabbit, MobTypeHorse, MobTypeDonkey, MobTypeMule, MobTypeSkeletonHorse, MobTypeZombieHorse,
		MobTypeLlama, MobTypeTraderLlama, MobTypeCamel, MobTypeSniffer, MobTypeArmadillo,
		MobTypeWolf, MobTypeCat, MobTypeOcelot, MobTypeParrot, MobTypeFox, MobTypePanda,
		MobTypeBee, MobTypeGoat, MobTypeFrog, MobTypeTadpole, MobTypeTurtle, MobTypeStrider,
		MobTypeAllay, MobTypeHappyGhast, "minecraft:polar_bear":
		return MobCategoryCreature
	default:
		return MobCategoryMisc
	}
}

// GlobalMobCategoryCap2612 computes the global NaturalSpawner cap for `cat` across
// `spawnableChunkCount` chunks: `cat.MaxInstancesPerChunk() * spawnableChunkCount / 289`.
func GlobalMobCategoryCap2612(cat MobCategory2612, spawnableChunkCount int) int {
	maxPerChunk := cat.MaxInstancesPerChunk()
	if maxPerChunk < 0 || spawnableChunkCount <= 0 {
		return 0
	}
	return (maxPerChunk * spawnableChunkCount) / NaturalSpawnerMagicNumber
}

// LocalMobCapCalculator2612 models vanilla `LocalMobCapCalculator` (per-player
// mob counts per category within an 8-chunk Chebyshev radius).
type LocalMobCapCalculator2612 struct {
	playerCounts map[int32][MobCategoryCount2612]int
}

// NewLocalMobCapCalculator2612 creates a new LocalMobCapCalculator2612.
func NewLocalMobCapCalculator2612() *LocalMobCapCalculator2612 {
	return &LocalMobCapCalculator2612{
		playerCounts: make(map[int32][MobCategoryCount2612]int),
	}
}

// AddMob records one mob of category `cat` for all players in `nearbyPlayerIDs`.
func (lc *LocalMobCapCalculator2612) AddMob(cat MobCategory2612, nearbyPlayerIDs []int32) {
	if lc == nil || int(cat) >= MobCategoryCount2612 {
		return
	}
	for _, pid := range nearbyPlayerIDs {
		arr := lc.playerCounts[pid]
		arr[cat]++
		lc.playerCounts[pid] = arr
	}
}

// CanSpawn reports whether `cat` can spawn for `nearbyPlayerIDs` (returns true
// if at least one nearby player has `count[cat] < cat.MaxInstancesPerChunk()`).
func (lc *LocalMobCapCalculator2612) CanSpawn(cat MobCategory2612, nearbyPlayerIDs []int32) bool {
	if lc == nil || cat == MobCategoryMisc || cat.MaxInstancesPerChunk() < 0 {
		return false
	}
	limit := cat.MaxInstancesPerChunk()
	for _, pid := range nearbyPlayerIDs {
		arr := lc.playerCounts[pid]
		if arr[cat] < limit {
			return true
		}
	}
	return false
}

// CanSpawnForCategory2612 checks both the global `NaturalSpawner` category cap
// (`globalCount < max * spawnableChunks / 289`) and the per-player `LocalMobCapCalculator2612`.
func CanSpawnForCategory2612(cat MobCategory2612, globalCount, spawnableChunks int, localCalc *LocalMobCapCalculator2612, nearbyPlayerIDs []int32) bool {
	if cat == MobCategoryMisc || cat.MaxInstancesPerChunk() < 0 {
		return false
	}
	if globalCount >= GlobalMobCategoryCap2612(cat, spawnableChunks) {
		return false
	}
	if localCalc != nil && len(nearbyPlayerIDs) > 0 {
		return localCalc.CanSpawn(cat, nearbyPlayerIDs)
	}
	return true
}

func requiresCustomPersistence2612(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypeEnderDragon, MobTypeWither, MobTypeWarden, MobTypeElderGuardian,
		MobTypeVillager, MobTypeWanderingTrader, MobTypeIronGolem, MobTypeSnowGolem,
		MobTypeCopperGolem, MobTypeAllay, MobTypeShulker, MobTypePiglinBrute:
		return true
	default:
		return false
	}
}

// EvaluateMobDespawn2612 evaluates `Mob.checkDespawn()` for a mob of `typeName`
// at `nearestPlayerDist` blocks from the closest non-spectator player:
//   - On Peaceful (`difficulty == DifficultyPeaceful`), hostile monsters (`MobCategoryMonster`,
//     except Piglin/Hoglin conversions or Shulker/EnderDragon if applicable; in vanilla
//     `Monster.isDisallowedInPeaceful()` is true for all `MobCategoryMonster` except
//     `EnderDragon` and `Piglin` in certain contexts; standard hostile monsters despawn immediately).
//   - If `persistenceRequired`, `hasCustomName`, `tamed`, `leashed`, `isPassenger`,
//     `requiresCustomPersistence2612(typeName)`, or `cat.IsPersistent()` (`CREATURE`, `MISC`),
//     the mob never despawns from player distance.
//   - If `nearestPlayerDist > float64(cat.DespawnDistance())` (`128`, or `64` for `WATER_AMBIENT`),
//     the mob despawns immediately (`shouldDespawn = true`).
//   - If `nearestPlayerDist > float64(cat.NoDespawnDistance())` (`> 32`):
//     `nextNoActionTicks = noActionTicks + 1`; if `nextNoActionTicks > 600` and
//     `rng.Intn(800) == 0`, the mob despawns (`shouldDespawn = true`).
//   - If `nearestPlayerDist <= float64(cat.NoDespawnDistance())` (`<= 32`),
//     `nextNoActionTicks` resets to `0` and the mob does not despawn.
func EvaluateMobDespawn2612(
	typeName string,
	nearestPlayerDist float64,
	noActionTicks int,
	persistenceRequired, hasCustomName, tamed, leashed, isPassenger bool,
	difficulty Difficulty,
	rng *rand.Rand,
) (shouldDespawn bool, nextNoActionTicks int) {
	cat := MobCategoryForEntityType(typeName)
	norm := canonicalResourceName(typeName)
	if difficulty == DifficultyPeaceful && cat == MobCategoryMonster && norm != MobTypeEnderDragon {
		return true, noActionTicks
	}
	if persistenceRequired || hasCustomName || tamed || leashed || isPassenger ||
		requiresCustomPersistence2612(norm) || cat.IsPersistent() {
		return false, 0
	}
	if nearestPlayerDist < 0 {
		return false, noActionTicks
	}
	despawnDist := float64(cat.DespawnDistance())
	if nearestPlayerDist > despawnDist {
		return true, noActionTicks
	}
	noDespawnDist := float64(cat.NoDespawnDistance())
	if nearestPlayerDist > noDespawnDist {
		nextNoActionTicks = noActionTicks + 1
		if nextNoActionTicks > MobNoActionDespawnThresholdTicks {
			roll := 0
			if rng != nil {
				roll = rng.Intn(MobRandomDespawnChanceReciprocal)
			} else {
				roll = rand.Intn(MobRandomDespawnChanceReciprocal)
			}
			if roll == 0 {
				return true, nextNoActionTicks
			}
		}
		return false, nextNoActionTicks
	}
	return false, 0
}

// --- 6. Unified Mob Conversions, Mob Armor/Toughness, Knockback Resistance & XP Rewards ---

// Vanilla 26.1.2 mob conversion timer constants (ZombieDrownedConversionTicks,
// SkeletonPowderSnowFreezeTicks, and SkeletonStrayConversionTicks are defined in undead_silverfish.go).
const (
	ZombieInWaterStartConversionTicks = 600 // 30s in water before 15s (300t) conversion
	PiglinHoglinZombifyTicks          = 300 // 15s in Overworld/End -> ZombifiedPiglin / Zoglin
	TadpoleToFrogAgeTicks             = 24000
)

// EvaluateMobConversionStep2612 evaluates one tick of environmental mob conversion
// (Husk -> Zombie -> Drowned in water, Skeleton -> Stray in powder snow,
// Piglin/PiglinBrute -> ZombifiedPiglin and Hoglin -> Zoglin in Overworld/End),
// returning the updated exposure timer, the converted target typeName (if complete),
// and whether conversion completed on this tick.
func EvaluateMobConversionStep2612(
	typeName string,
	dimension string,
	inWaterUnderEye bool,
	inPowderSnow bool,
	immuneToZombification bool,
	exposureTicks int,
) (nextExposureTicks int, convertedType string, converted bool) {
	norm := canonicalResourceName(typeName)
	normDim := world.NormalizeDimension(dimension)

	switch norm {
	case MobTypeHusk:
		if inWaterUnderEye {
			nextExposureTicks = exposureTicks + 1
			if nextExposureTicks >= ZombieInWaterStartConversionTicks+ZombieDrownedConversionTicks {
				return 0, MobTypeZombie, true
			}
			return nextExposureTicks, "", false
		}
		if exposureTicks > 0 {
			return exposureTicks - 1, "", false
		}
		return 0, "", false

	case MobTypeZombie:
		if inWaterUnderEye {
			nextExposureTicks = exposureTicks + 1
			if nextExposureTicks >= ZombieInWaterStartConversionTicks+ZombieDrownedConversionTicks {
				return 0, MobTypeDrowned, true
			}
			return nextExposureTicks, "", false
		}
		if exposureTicks > 0 {
			return exposureTicks - 1, "", false
		}
		return 0, "", false

	case MobTypeSkeleton:
		if inPowderSnow {
			nextExposureTicks = exposureTicks + 1
			if nextExposureTicks >= SkeletonPowderSnowFreezeTicks+SkeletonStrayConversionTicks {
				return 0, MobTypeStray, true
			}
			return nextExposureTicks, "", false
		}
		if exposureTicks > 0 {
			return exposureTicks - 1, "", false
		}
		return 0, "", false

	case MobTypePiglin, MobTypePiglinBrute:
		if !immuneToZombification && normDim != world.DimensionNether {
			nextExposureTicks = exposureTicks + 1
			if nextExposureTicks >= PiglinHoglinZombifyTicks {
				return 0, MobTypeZombifiedPiglin, true
			}
			return nextExposureTicks, "", false
		}
		return 0, "", false

	case MobTypeHoglin:
		if !immuneToZombification && normDim != world.DimensionNether {
			nextExposureTicks = exposureTicks + 1
			if nextExposureTicks >= PiglinHoglinZombifyTicks {
				return 0, MobTypeZoglin, true
			}
			return nextExposureTicks, "", false
		}
		return 0, "", false
	}

	return 0, "", false
}

// ApplyMobArmorAndToughness2612 computes `CombatRules.getDamageAfterAbsorb(damage, armor, toughness)`
// in vanilla 26.1.2:
//
//	f = 2.0 + toughness / 4.0
//	g = clamp(armor - damage / f, armor * 0.2, 20.0)
//	return damage * (1.0 - g / 25.0)
func ApplyMobArmorAndToughness2612(damage, armor, toughness float32) float32 {
	if damage <= 0 {
		return 0
	}
	if armor <= 0 {
		return damage
	}
	f := float32(2.0) + toughness/4.0
	g := armor - damage/f
	minG := armor * 0.2
	if g < minG {
		g = minG
	}
	if g > 20.0 {
		g = 20.0
	}
	return damage * (1.0 - g/25.0)
}

// ComputeMobKnockbackWithResistance2612 computes the knockback velocity delta
// applied to a mob with `knockbackResistance` in `[0.0, 1.0]` (`LivingEntity.knockback`).
func ComputeMobKnockbackWithResistance2612(strength, knockbackResistance, ratioX, ratioZ float64) (vx, vy, vz float64) {
	if knockbackResistance >= 1.0 || strength <= 0 {
		return 0, 0, 0
	}
	if knockbackResistance < 0 {
		knockbackResistance = 0
	}
	effStrength := strength * (1.0 - knockbackResistance)
	mag := math.Hypot(ratioX, ratioZ)
	if mag < 1e-6 {
		return 0, 0, 0
	}
	nx := ratioX / mag
	nz := ratioZ / mag
	return -nx * effStrength, math.Min(0.4, effStrength), -nz * effStrength
}

// NaturalMobArmorAndResistance2612 returns the base natural `generic.armor`,
// `generic.armor_toughness`, and `generic.knockback_resistance` attributes for
// a mob type in vanilla 26.1.2.
func NaturalMobArmorAndResistance2612(typeName string) (armor, toughness float32, knockbackResistance float64) {
	switch canonicalResourceName(typeName) {
	case MobTypeZombie, MobTypeZombieVillager, MobTypeHusk, MobTypeDrowned, MobTypeZombifiedPiglin:
		return 2.0, 0.0, 0.0
	case MobTypeShulker:
		return 20.0, 0.0, 1.0 // Closed shell provides +20 armor
	case MobTypeWither:
		return 4.0, 0.0, 0.0
	case MobTypeMagmaCube:
		return 6.0, 0.0, 0.0
	case MobTypeIronGolem:
		return 0.0, 0.0, 1.0
	case MobTypeWarden:
		return 0.0, 0.0, 1.0
	case MobTypeRavager:
		return 0.0, 0.0, 0.75
	case MobTypeHoglin, MobTypeZoglin:
		return 0.0, 0.0, 0.60
	default:
		return 0.0, 0.0, 0.0
	}
}

// CalculateMobXPReward2612 returns the total experience points dropped by a mob
// of `typeName` when killed by a player (or tamed wolf) in vanilla 26.1.2,
// including the `+1..3` bonus per naturally-spawned equipment piece.
func CalculateMobXPReward2612(typeName string, isBaby bool, naturallySpawnedEquipmentCount int, rng *rand.Rand) int32 {
	norm := canonicalResourceName(typeName)
	if isBaby {
		switch norm {
		case MobTypeZombie, MobTypeZombieVillager, MobTypeHusk, MobTypeDrowned, MobTypeZombifiedPiglin, MobTypePiglin:
			base := int32(12)
			return base + rollEquipmentXPBonus2612(naturallySpawnedEquipmentCount, rng)
		default:
			return 0
		}
	}

	var base int32
	switch norm {
	case MobTypeEnderDragon:
		base = 500
	case MobTypeWither:
		base = 50
	case MobTypePiglinBrute, MobTypeRavager:
		base = 20
	case MobTypeBlaze, MobTypeGuardian, MobTypeElderGuardian, MobTypeEvoker, MobTypeHusk, MobTypeSkeleton, MobTypeZombie:
		if norm == MobTypeBlaze || norm == MobTypeGuardian || norm == MobTypeElderGuardian || norm == MobTypeEvoker {
			base = 10
		} else {
			base = 5
		}
	case MobTypeCreeper, MobTypeSpider, MobTypeCaveSpider, MobTypeEnderman,
		MobTypeDrowned, MobTypeStray, MobTypeBogged, MobTypeParched, MobTypeWitherSkeleton,
		MobTypeGhast, MobTypePhantom, MobTypeBreeze, MobTypeWitch, MobTypeVindicator,
		MobTypePillager, MobTypeVex, MobTypeIllusioner, MobTypeShulker,
		MobTypePiglin, MobTypeZombifiedPiglin, MobTypeHoglin, MobTypeZoglin,
		MobTypeSilverfish, MobTypeWarden, MobTypeZombieNautilus, MobTypeCamelHusk:
		base = 5
	case MobTypeEndermite:
		base = 3
	case MobTypeSlime, MobTypeMagmaCube:
		base = 4
	case MobTypeBat, MobTypeVillager, MobTypeWanderingTrader, MobTypeIronGolem,
		MobTypeSnowGolem, MobTypeCopperGolem, MobTypeAllay, MobTypeTadpole, MobTypeCreaking:
		return 0
	default:
		// Passive/neutral animals drop 1..3 XP.
		if rng != nil {
			base = int32(1 + rng.Intn(3))
		} else {
			base = int32(1 + rand.Intn(3))
		}
	}

	return base + rollEquipmentXPBonus2612(naturallySpawnedEquipmentCount, rng)
}

func rollEquipmentXPBonus2612(equipmentCount int, rng *rand.Rand) int32 {
	if equipmentCount <= 0 {
		return 0
	}
	var bonus int32
	for i := 0; i < equipmentCount; i++ {
		if rng != nil {
			bonus += int32(1 + rng.Intn(3))
		} else {
			bonus += int32(1 + rand.Intn(3))
		}
	}
	return bonus
}
