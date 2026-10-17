package server

import (
	"math"
	"math/rand"

	"regionio/internal/world"
)

// Mob entity type identifiers and AI parameters.
const (
	MobTypeZombie = "minecraft:zombie"
	MobTypePig    = "minecraft:pig"

	// Movement speeds in blocks per game tick (20 TPS).
	ZombieChaseSpeed  = 0.15
	ZombieWanderSpeed = 0.08
	PigWanderSpeed    = 0.09

	// Hostile zombie detection and melee combat parameters.
	ZombieAggroRange          = 32.0
	ZombieAttackReach         = 1.8
	ZombieAttackVerticalReach = 2.0
	ZombieAttackStopDist      = 1.1
	ZombieAttackDamage        = float32(3.0)
	ZombieAttackCooldownTicks = 20

	// Passive/idle wandering parameters.
	WanderRadius        = 8
	WanderMinMoveTicks  = 40
	WanderMaxMoveTicks  = 120
	WanderMinIdleTicks  = 20
	WanderMaxIdleTicks  = 80
	WanderGoalReachDist = 0.6

	// Pathfinding bounds.
	MaxPathSearchNodes      = 128
	MaxPathWaypoints        = 32
	PathRepathIntervalTicks = 10
	MaxStepDownBlocks       = 3
	waypointAdvanceDist     = 0.25
)

type mobBehaviorState uint8

const (
	mobStateIdle mobBehaviorState = iota
	mobStateWander
	mobStateChase
)

// PathNode represents a 3D block coordinate on a mob's navigation path (where Y
// is the feet-level block Y, one block above the supporting floor).
type PathNode struct {
	X, Y, Z int
}

type mobMemory struct {
	state           mobBehaviorState
	stateTicks      int
	goalX           float64
	goalY           float64
	goalZ           float64
	waypoints       []PathNode
	waypointIdx     int
	repathCooldown  int
	attackCooldown  int
	leapCooldown    int
	creeperFuseTicks int
	creeperSwellDir int8
	provoked        bool
	panicTicks      int
	inLoveTicks      int
	breedCooldown    int
	ageTicks         int
	loveCause        *PlayerSession
	eggTimer         int
	sheared          bool
	woolRegrowTimer  int
	seenTick         uint64
	healthInit       bool
	health           float32
	lastHealth       float32
}

type playerTarget struct {
	session  *PlayerSession
	uuid     [16]byte
	x, y, z  float64
	gameMode uint8
	health   float32
}

// IsHostileMob reports whether typeName is a hostile mob that chases and
// attacks (or explodes near) players.
func IsHostileMob(typeName string) bool {
	switch typeName {
	case MobTypeZombie, MobTypeSkeleton, MobTypeBogged, MobTypeCreeper, MobTypeSpider, MobTypePhantom, MobTypeBreeze:
		return true
	default:
		return false
	}
}

// IsPassiveMob reports whether typeName is a passive wandering mob or animal.
func IsPassiveMob(typeName string) bool {
	switch typeName {
	case MobTypePig, MobTypeCow, MobTypeSheep, MobTypeChicken, MobTypeVillager,
		MobTypeSniffer, MobTypeArmadillo, MobTypeCamel,
		MobTypeAllay, MobTypeFrog, MobTypeTadpole, MobTypeAxolotl, MobTypeGoat:
		return true
	default:
		return false
	}
}

func (s *Server) appendPlayerTargets(dst []playerTarget) []playerTarget {
	s.playersMu.RLock()
	for _, session := range s.players {
		if session == nil || session.IsDead() {
			continue
		}
		session.mu.RLock()
		pos := session.position
		gm := session.gameMode
		hp := session.health
		uuid := session.Profile.UUID
		session.mu.RUnlock()
		if hp <= 0 || gm == GameModeSpectator {
			continue
		}
		dst = append(dst, playerTarget{
			session:  session,
			uuid:     uuid,
			x:        pos[0],
			y:        pos[1],
			z:        pos[2],
			gameMode: gm,
			health:   hp,
		})
	}
	s.playersMu.RUnlock()
	return dst
}

// selectZombieTarget finds the nearest valid player within ZombieAggroRange.
// Survival and Adventure players take priority over Creative players; dead and
// Spectator players are ignored.
func selectZombieTarget(targets []playerTarget, x, y, z float64) (playerTarget, bool) {
	maxDistSq := ZombieAggroRange * ZombieAggroRange
	bestSurvivalIdx := -1
	bestSurvivalDistSq := maxDistSq
	bestFallbackIdx := -1
	bestFallbackDistSq := maxDistSq

	for i := range targets {
		pt := &targets[i]
		if pt.session == nil || pt.health <= 0 || pt.gameMode == GameModeSpectator || pt.session.IsDead() {
			continue
		}
		dx := pt.x - x
		dy := pt.y - y
		dz := pt.z - z
		distSq := dx*dx + dy*dy + dz*dz
		if distSq > maxDistSq {
			continue
		}
		if pt.gameMode == GameModeSurvival || pt.gameMode == GameModeAdventure {
			if bestSurvivalIdx < 0 || distSq < bestSurvivalDistSq {
				bestSurvivalIdx = i
				bestSurvivalDistSq = distSq
			}
		} else if bestFallbackIdx < 0 || distSq < bestFallbackDistSq {
			bestFallbackIdx = i
			bestFallbackDistSq = distSq
		}
	}
	if bestSurvivalIdx >= 0 {
		return targets[bestSurvivalIdx], true
	}
	if bestFallbackIdx >= 0 {
		return targets[bestFallbackIdx], true
	}
	return playerTarget{}, false
}

func isPassableBlock(state uint16) bool {
	if state == world.StateAir || state == world.StateSnow {
		return true
	}
	return world.IsInstantBreakBlock(state)
}

func isHazardBlock(state uint16) bool {
	return state == world.StateWater || state == world.StateLava || state == world.StatePowderSnow
}

func isSolidGroundBlock(state uint16) bool {
	return !isPassableBlock(state) && !isHazardBlock(state)
}

func isColumnPassableAt(chunks *world.Cache, x, feetY, z int) bool {
	if chunks == nil {
		return true
	}
	return isPassableBlock(chunks.GetBlock(x, feetY, z)) &&
		isPassableBlock(chunks.GetBlock(x, feetY+1, z))
}

// resolveStepY determines the resulting feet-level Y when a mob at (fromX, fromY, fromZ)
// steps into neighbor column (toX, toZ). It allows walking at the same height,
// stepping down up to maxDrop blocks onto solid non-hazard ground, or stepping
// up 1 block when head clearance permits.
func resolveStepY(chunks *world.Cache, fromX, fromY, fromZ, toX, toZ, maxDrop int) (int, bool) {
	if chunks == nil {
		return fromY, true
	}
	if fromY <= world.MinY+1 || fromY >= world.MinY+world.WorldHeight-2 {
		return 0, false
	}

	// 1. Same height or step-down into (toX, toZ).
	if isColumnPassableAt(chunks, toX, fromY, toZ) {
		for drop := 0; drop <= maxDrop; drop++ {
			candFeetY := fromY - drop
			if candFeetY <= world.MinY+1 {
				break
			}
			if drop > 0 && !isPassableBlock(chunks.GetBlock(toX, candFeetY, toZ)) {
				break
			}
			floorState := chunks.GetBlock(toX, candFeetY-1, toZ)
			if isHazardBlock(floorState) {
				return 0, false
			}
			if isSolidGroundBlock(floorState) {
				return candFeetY, true
			}
		}
		return 0, false
	}

	// 2. Step-up 1 block onto (toX, fromY+1, toZ).
	stepBlock := chunks.GetBlock(toX, fromY, toZ)
	if isSolidGroundBlock(stepBlock) {
		if isPassableBlock(chunks.GetBlock(fromX, fromY+2, fromZ)) &&
			isColumnPassableAt(chunks, toX, fromY+1, toZ) {
			return fromY + 1, true
		}
	}

	return 0, false
}

// canStepBetween checks whether a mob can move directly between adjacent block
// columns (fromX, fromZ) and (toX, toZ). Diagonal moves require both shared
// cardinal columns to be passable and supported so mobs never clip corners.
func canStepBetween(chunks *world.Cache, fromX, fromY, fromZ, toX, toZ int) (int, bool) {
	nextY, ok := resolveStepY(chunks, fromX, fromY, fromZ, toX, toZ, MaxStepDownBlocks)
	if !ok {
		return 0, false
	}
	dx := toX - fromX
	dz := toZ - fromZ
	if dx != 0 && dz != 0 {
		// Disallow diagonal step-up so 1-block ledges are climbed cardinally.
		if nextY > fromY {
			return 0, false
		}
		if !isColumnPassableAt(chunks, fromX+dx, fromY, fromZ) ||
			!isColumnPassableAt(chunks, fromX, fromY, fromZ+dz) {
			return 0, false
		}
		if _, ok1 := resolveStepY(chunks, fromX, fromY, fromZ, fromX+dx, fromZ, MaxStepDownBlocks); !ok1 {
			return 0, false
		}
		if _, ok2 := resolveStepY(chunks, fromX, fromY, fromZ, fromX, fromZ+dz, MaxStepDownBlocks); !ok2 {
			return 0, false
		}
	}
	return nextY, true
}

// hasDirectWalkableLine checks whether the straight line in XZ from
// (startX, startZ) at feetY to (goalX, goalZ) is unobstructed and walkable.
func hasDirectWalkableLine(chunks *world.Cache, startX float64, feetY int, startZ, goalX, goalZ float64) bool {
	if chunks == nil {
		return true
	}
	dx := goalX - startX
	dz := goalZ - startZ
	dist := math.Hypot(dx, dz)
	if dist < 1e-4 {
		return true
	}
	if dist > 16.0 {
		return false
	}
	steps := int(math.Ceil(dist / 0.25))
	if steps < 1 {
		steps = 1
	}
	curBX := int(math.Floor(startX))
	curBZ := int(math.Floor(startZ))
	curY := feetY

	for i := 1; i <= steps; i++ {
		frac := float64(i) / float64(steps)
		px := startX + dx*frac
		pz := startZ + dz*frac
		nbx := int(math.Floor(px))
		nbz := int(math.Floor(pz))
		if nbx == curBX && nbz == curBZ {
			continue
		}
		ny, ok := canStepBetween(chunks, curBX, curY, curBZ, nbx, nbz)
		if !ok {
			return false
		}
		curBX = nbx
		curBZ = nbz
		curY = ny
	}
	return true
}

type astarNode struct {
	x, y, z int
	g, f, h float64
	parent  int
	closed  bool
}

var neighborDirs = [8][3]int{
	{1, 0, 0},
	{-1, 0, 0},
	{0, 0, 1},
	{0, 0, -1},
	{1, 0, 1},
	{1, 0, -1},
	{-1, 0, 1},
	{-1, 0, -1},
}

func pathHeuristic(x, y, z, gx, gy, gz int) float64 {
	dx := math.Abs(float64(gx - x))
	dz := math.Abs(float64(gz - z))
	dy := math.Abs(float64(gy - y))
	minXZ := dx
	maxXZ := dz
	if dz < dx {
		minXZ = dz
		maxXZ = dx
	}
	return (maxXZ - minXZ) + minXZ*math.Sqrt2 + dy*0.5
}

// FindPath computes a bounded A* path of block coordinates from
// (startX, startY, startZ) toward (goalX, goalY, goalZ). If the exact goal is
// unreachable within maxNodes expansions, FindPath returns the path to the
// closest reachable node that makes progress toward the goal.
func FindPath(chunks *world.Cache, startX, startY, startZ, goalX, goalY, goalZ, maxNodes int) []PathNode {
	if startX == goalX && startZ == goalZ {
		return nil
	}
	if maxNodes <= 0 {
		maxNodes = MaxPathSearchNodes
	}

	nodes := make([]astarNode, 0, maxNodes)
	indexByPos := make(map[[3]int]int, maxNodes)

	startH := pathHeuristic(startX, startY, startZ, goalX, goalY, goalZ)
	nodes = append(nodes, astarNode{
		x:      startX,
		y:      startY,
		z:      startZ,
		g:      0,
		h:      startH,
		f:      startH,
		parent: -1,
	})
	indexByPos[[3]int{startX, startY, startZ}] = 0

	bestIdx := 0
	bestH := startH
	expanded := 0

	for expanded < maxNodes {
		currIdx := -1
		currF := math.MaxFloat64
		for i := range nodes {
			if !nodes[i].closed && nodes[i].f < currF {
				currF = nodes[i].f
				currIdx = i
			}
		}
		if currIdx < 0 {
			break
		}

		curr := nodes[currIdx]
		nodes[currIdx].closed = true
		expanded++

		if curr.h < bestH-1e-4 {
			bestH = curr.h
			bestIdx = currIdx
		}
		if curr.x == goalX && curr.z == goalZ {
			bestIdx = currIdx
			break
		}

		for _, dir := range neighborDirs {
			nx := curr.x + dir[0]
			nz := curr.z + dir[2]
			ny, ok := canStepBetween(chunks, curr.x, curr.y, curr.z, nx, nz)
			if !ok {
				continue
			}

			stepCost := 1.0
			if dir[0] != 0 && dir[2] != 0 {
				stepCost = math.Sqrt2
			}
			if ny != curr.y {
				stepCost += 0.5
			}
			tentativeG := curr.g + stepCost
			key := [3]int{nx, ny, nz}

			if existingIdx, exists := indexByPos[key]; exists {
				if !nodes[existingIdx].closed && tentativeG < nodes[existingIdx].g {
					nodes[existingIdx].g = tentativeG
					nodes[existingIdx].f = tentativeG + nodes[existingIdx].h
					nodes[existingIdx].parent = currIdx
				}
				continue
			}
			if len(nodes) >= maxNodes*2 {
				continue
			}
			h := pathHeuristic(nx, ny, nz, goalX, goalY, goalZ)
			newIdx := len(nodes)
			nodes = append(nodes, astarNode{
				x:      nx,
				y:      ny,
				z:      nz,
				g:      tentativeG,
				h:      h,
				f:      tentativeG + h,
				parent: currIdx,
			})
			indexByPos[key] = newIdx
		}
	}

	if bestIdx == 0 {
		return nil
	}

	var rev []PathNode
	for idx := bestIdx; idx > 0; idx = nodes[idx].parent {
		n := nodes[idx]
		rev = append(rev, PathNode{X: n.x, Y: n.y, Z: n.z})
		if len(rev) >= MaxPathWaypoints {
			break
		}
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

func (t *EntityTracker) mobStateFor(id int32, tickID uint64) *mobMemory {
	if t.mobStates == nil {
		t.mobStates = make(map[int32]*mobMemory)
	}
	st := t.mobStates[id]
	if st == nil {
		st = &mobMemory{}
		t.mobStates[id] = st
	}
	st.seenTick = tickID
	return st
}

func (t *EntityTracker) pruneMobStates(tickID uint64) {
	for id, st := range t.mobStates {
		if st.seenTick != tickID {
			delete(t.mobStates, id)
		}
	}
}

func yawToward(dx, dz float64) float32 {
	return float32(math.Atan2(-dx, dz) * (180.0 / math.Pi))
}

// moveMobToward advances a mob from (curX, curY, curZ) toward (targetX, targetY, targetZ)
// at speed, using direct line-of-sight movement when clear and A* waypoints when
// obstructed.
func moveMobToward(
	chunks *world.Cache,
	mem *mobMemory,
	curX float64,
	curY int,
	curZ float64,
	targetX float64,
	targetY int,
	targetZ float64,
	speed float64,
	stopDist float64,
) (nextX, nextY, nextZ float64, nextYaw float32, moved bool) {
	nextX = curX
	nextY = float64(curY)
	nextZ = curZ

	dxGoal := targetX - curX
	dzGoal := targetZ - curZ
	distGoal := math.Hypot(dxGoal, dzGoal)
	if distGoal > 1e-4 {
		nextYaw = yawToward(dxGoal, dzGoal)
	}
	if distGoal <= stopDist || speed <= 0 {
		return nextX, nextY, nextZ, nextYaw, false
	}

	curBX := int(math.Floor(curX))
	curBZ := int(math.Floor(curZ))
	goalBX := int(math.Floor(targetX))
	goalBZ := int(math.Floor(targetZ))

	var steerX, steerZ float64
	if hasDirectWalkableLine(chunks, curX, curY, curZ, targetX, targetZ) {
		mem.waypoints = mem.waypoints[:0]
		mem.waypointIdx = 0
		steerX = targetX
		steerZ = targetZ
	} else {
		needRepath := mem.repathCooldown <= 0 ||
			mem.waypointIdx >= len(mem.waypoints) ||
			math.Hypot(targetX-mem.goalX, targetZ-mem.goalZ) > 1.5
		if needRepath {
			mem.waypoints = FindPath(chunks, curBX, curY, curBZ, goalBX, targetY, goalBZ, MaxPathSearchNodes)
			mem.waypointIdx = 0
			mem.goalX = targetX
			mem.goalY = float64(targetY)
			mem.goalZ = targetZ
			mem.repathCooldown = PathRepathIntervalTicks
		}

		for mem.waypointIdx < len(mem.waypoints) {
			wp := mem.waypoints[mem.waypointIdx]
			wpX := float64(wp.X) + 0.5
			wpZ := float64(wp.Z) + 0.5
			if mem.waypointIdx < len(mem.waypoints)-1 && math.Hypot(wpX-curX, wpZ-curZ) <= waypointAdvanceDist {
				mem.waypointIdx++
				continue
			}
			break
		}

		if mem.waypointIdx < len(mem.waypoints) {
			wp := mem.waypoints[mem.waypointIdx]
			if mem.waypointIdx == len(mem.waypoints)-1 && wp.X == goalBX && wp.Z == goalBZ {
				steerX = targetX
				steerZ = targetZ
			} else {
				steerX = float64(wp.X) + 0.5
				steerZ = float64(wp.Z) + 0.5
			}
		} else {
			return nextX, nextY, nextZ, nextYaw, false
		}
	}

	dx := steerX - curX
	dz := steerZ - curZ
	dist := math.Hypot(dx, dz)
	if dist <= 1e-4 {
		return nextX, nextY, nextZ, nextYaw, false
	}

	step := speed
	if dist < step {
		step = dist
	}
	if distGoal-stopDist < step && steerX == targetX && steerZ == targetZ {
		step = distGoal - stopDist
	}
	if step <= 1e-4 {
		return nextX, nextY, nextZ, nextYaw, false
	}

	stepX := (dx / dist) * step
	stepZ := (dz / dist) * step
	nextYaw = yawToward(dx, dz)

	if nx, ny, nz, ok := tryApplyHorizontalStep(chunks, curX, curY, curZ, curX+stepX, curZ+stepZ); ok {
		return nx, float64(ny), nz, nextYaw, true
	}
	// Axis-aligned wall-slide fallback if diagonal movement hit an obstacle edge.
	if math.Abs(stepX) > 1e-4 {
		if nx, ny, nz, ok := tryApplyHorizontalStep(chunks, curX, curY, curZ, curX+stepX, curZ); ok {
			mem.repathCooldown = 0
			return nx, float64(ny), nz, nextYaw, true
		}
	}
	if math.Abs(stepZ) > 1e-4 {
		if nx, ny, nz, ok := tryApplyHorizontalStep(chunks, curX, curY, curZ, curX, curZ+stepZ); ok {
			mem.repathCooldown = 0
			return nx, float64(ny), nz, nextYaw, true
		}
	}

	mem.repathCooldown = 0
	return nextX, nextY, nextZ, nextYaw, false
}

func tryApplyHorizontalStep(chunks *world.Cache, curX float64, curY int, curZ, candX, candZ float64) (float64, int, float64, bool) {
	if chunks == nil {
		return candX, curY, candZ, true
	}
	curBX := int(math.Floor(curX))
	curBZ := int(math.Floor(curZ))
	candBX := int(math.Floor(candX))
	candBZ := int(math.Floor(candZ))
	if candBX == curBX && candBZ == curBZ {
		return candX, curY, candZ, true
	}
	ny, ok := canStepBetween(chunks, curBX, curY, curBZ, candBX, candBZ)
	if !ok {
		return curX, curY, curZ, false
	}
	return candX, ny, candZ, true
}

func pickWanderGoal(chunks *world.Cache, rng *rand.Rand, curX float64, curY int, curZ float64) (goalX float64, goalY int, goalZ float64, ok bool) {
	if rng == nil {
		return 0, 0, 0, false
	}
	curBX := int(math.Floor(curX))
	curBZ := int(math.Floor(curZ))

	for attempt := 0; attempt < 8; attempt++ {
		dx := rng.Intn(2*WanderRadius+1) - WanderRadius
		dz := rng.Intn(2*WanderRadius+1) - WanderRadius
		if dx == 0 && dz == 0 {
			dx = 1
		}
		tx := curBX + dx
		tz := curBZ + dz
		if chunks == nil {
			return float64(tx) + 0.5, curY, float64(tz) + 0.5, true
		}
		if ny, valid := findWalkableColumnNearY(chunks, tx, curY, tz); valid {
			return float64(tx) + 0.5, ny, float64(tz) + 0.5, true
		}
	}
	return 0, 0, 0, false
}

func findWalkableColumnNearY(chunks *world.Cache, bx, refY, bz int) (int, bool) {
	for dy := 2; dy >= -MaxStepDownBlocks; dy-- {
		feetY := refY + dy
		if feetY <= world.MinY+1 || feetY >= world.MinY+world.WorldHeight-2 {
			continue
		}
		floor := chunks.GetBlock(bx, feetY-1, bz)
		if !isSolidGroundBlock(floor) {
			continue
		}
		if isColumnPassableAt(chunks, bx, feetY, bz) {
			return feetY, true
		}
	}
	return 0, false
}

func stepWanderAI(
	chunks *world.Cache,
	rng *rand.Rand,
	mem *mobMemory,
	curX float64,
	curY int,
	curZ float64,
	curYaw float32,
	speed float64,
) (nextX, nextY, nextZ float64, nextYaw float32) {
	nextX = curX
	nextY = float64(curY)
	nextZ = curZ
	nextYaw = curYaw

	if rng == nil {
		return nextX, nextY, nextZ, nextYaw
	}

	if mem.state == mobStateChase {
		mem.state = mobStateIdle
		mem.stateTicks = 0
		mem.waypoints = mem.waypoints[:0]
		mem.waypointIdx = 0
	}

	if mem.state == mobStateIdle {
		if mem.stateTicks > 0 {
			mem.stateTicks--
			return nextX, nextY, nextZ, nextYaw
		}
		gx, gy, gz, ok := pickWanderGoal(chunks, rng, curX, curY, curZ)
		if !ok {
			mem.stateTicks = WanderMinIdleTicks
			return nextX, nextY, nextZ, nextYaw
		}
		mem.state = mobStateWander
		mem.stateTicks = WanderMinMoveTicks + rng.Intn(WanderMaxMoveTicks-WanderMinMoveTicks+1)
		mem.goalX = gx
		mem.goalY = float64(gy)
		mem.goalZ = gz
		mem.waypoints = mem.waypoints[:0]
		mem.waypointIdx = 0
		mem.repathCooldown = 0
	}

	mem.stateTicks--
	nx, ny, nz, yaw, moved := moveMobToward(
		chunks,
		mem,
		curX,
		curY,
		curZ,
		mem.goalX,
		int(mem.goalY),
		mem.goalZ,
		speed,
		0.1,
	)
	if moved {
		nextX, nextY, nextZ, nextYaw = nx, ny, nz, yaw
	}
	if !moved || mem.stateTicks <= 0 || math.Hypot(mem.goalX-nextX, mem.goalZ-nextZ) <= WanderGoalReachDist {
		mem.state = mobStateIdle
		mem.stateTicks = WanderMinIdleTicks + rng.Intn(WanderMaxIdleTicks-WanderMinIdleTicks+1)
		mem.waypoints = mem.waypoints[:0]
		mem.waypointIdx = 0
	}
	return nextX, nextY, nextZ, nextYaw
}

func stepHostileZombieAI(
	chunks *world.Cache,
	rng *rand.Rand,
	mem *mobMemory,
	targets []playerTarget,
	curX float64,
	curY int,
	curZ float64,
	curYaw float32,
) (nextX, nextY, nextZ float64, nextYaw float32, attackTarget *PlayerSession) {
	if mem.attackCooldown > 0 {
		mem.attackCooldown--
	}
	if mem.repathCooldown > 0 {
		mem.repathCooldown--
	}

	target, hasTarget := selectZombieTarget(targets, curX, float64(curY), curZ)
	if !hasTarget {
		nx, ny, nz, yaw := stepWanderAI(chunks, rng, mem, curX, curY, curZ, curYaw, ZombieWanderSpeed)
		return nx, ny, nz, yaw, nil
	}

	mem.state = mobStateChase
	targetFeetY := int(math.Floor(target.y + 0.01))
	nextX, nextY, nextZ, nextYaw, _ = moveMobToward(
		chunks,
		mem,
		curX,
		curY,
		curZ,
		target.x,
		targetFeetY,
		target.z,
		ZombieChaseSpeed,
		ZombieAttackStopDist,
	)

	// Always orient toward the chased player when close.
	dx := target.x - nextX
	dz := target.z - nextZ
	horizDist := math.Hypot(dx, dz)
	if horizDist > 1e-4 && horizDist <= ZombieAttackReach*1.5 {
		nextYaw = yawToward(dx, dz)
	}

	vertDist := math.Abs(target.y - nextY)
	if horizDist <= ZombieAttackReach && vertDist <= ZombieAttackVerticalReach && mem.attackCooldown <= 0 {
		mem.attackCooldown = ZombieAttackCooldownTicks
		attackTarget = target.session
	}
	return nextX, nextY, nextZ, nextYaw, attackTarget
}
