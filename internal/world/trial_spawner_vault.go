package world

import (
	"math"
	"math/rand"
	"strings"
	"sync"
)

// Vanilla 26.1.2 constants for `minecraft:trial_spawner` and `minecraft:vault`.
const (
	// Trial spawner block states (`trial_spawner_state` property).
	TrialSpawnerStateInactive                 = "inactive"
	TrialSpawnerStateWaitingForPlayers        = "waiting_for_players"
	TrialSpawnerStateActive                   = "active"
	TrialSpawnerStateWaitingForRewardEjection = "waiting_for_reward_ejection"
	TrialSpawnerStateEjectingReward           = "ejecting_reward"
	TrialSpawnerStateCooldown                 = "cooldown"

	// Vault block states (`vault_state` property).
	VaultStateInactive  = "inactive"
	VaultStateActive    = "active"
	VaultStateUnlocking = "unlocking"
	VaultStateEjecting  = "ejecting"

	// Trial Chamber key & ominous bottle items and default loot tables.
	ItemTrialKey                        = "minecraft:trial_key"
	ItemOminousTrialKey                 = "minecraft:ominous_trial_key"
	ItemOminousBottle                   = "minecraft:ominous_bottle"
	DefaultTrialSpawnerNormalLootTable  = "minecraft:spawners/trial_chamber/key"
	DefaultTrialSpawnerOminousLootTable = "minecraft:spawners/ominous/trial_chamber/key"
	DefaultVaultNormalLootTable         = "minecraft:chests/trial_chambers/reward"
	DefaultVaultOminousLootTable        = "minecraft:chests/trial_chambers/reward_ominous"
)

type trialSpawnerStateMeta struct {
	state   string
	ominous bool
}

type vaultStateMeta struct {
	state   string
	facing  string
	ominous bool
}

var (
	trialVaultStatesOnce sync.Once
	trialSpawnerMetaByID map[uint16]trialSpawnerStateMeta
	vaultMetaByID        map[uint16]vaultStateMeta
)

func initTrialVaultStates() {
	trialVaultStatesOnce.Do(func() {
		trialSpawnerMetaByID = make(map[uint16]trialSpawnerStateMeta, 16)
		vaultMetaByID = make(map[uint16]vaultStateMeta, 64)

		spawnerStates := []string{
			TrialSpawnerStateInactive,
			TrialSpawnerStateWaitingForPlayers,
			TrialSpawnerStateActive,
			TrialSpawnerStateWaitingForRewardEjection,
			TrialSpawnerStateEjectingReward,
			TrialSpawnerStateCooldown,
		}
		vaultStates := []string{
			VaultStateInactive,
			VaultStateActive,
			VaultStateUnlocking,
			VaultStateEjecting,
		}
		facings := []string{"north", "south", "west", "east"}
		boolStrs := []string{"false", "true"}

		for _, st := range spawnerStates {
			for _, omStr := range boolStrs {
				if id, ok := nameToStateID("minecraft:trial_spawner", map[string]string{
					"trial_spawner_state": st,
					"ominous":             omStr,
				}); ok && id != StateAir {
					trialSpawnerMetaByID[id] = trialSpawnerStateMeta{
						state:   st,
						ominous: omStr == "true",
					}
				}
			}
		}

		for _, vs := range vaultStates {
			for _, fc := range facings {
				for _, omStr := range boolStrs {
					if id, ok := nameToStateID("minecraft:vault", map[string]string{
						"vault_state": vs,
						"facing":      fc,
						"ominous":     omStr,
					}); ok && id != StateAir {
						vaultMetaByID[id] = vaultStateMeta{
							state:   vs,
							facing:  fc,
							ominous: omStr == "true",
						}
					}
				}
			}
		}
	})
}

// IsTrialSpawnerBlock reports whether state is `minecraft:trial_spawner`.
func IsTrialSpawnerBlock(state uint16) bool {
	initTrialVaultStates()
	_, ok := trialSpawnerMetaByID[state]
	return ok
}

// ParseTrialSpawnerBlockState extracts `trial_spawner_state` and `ominous` from state.
func ParseTrialSpawnerBlockState(state uint16) (spawnerState string, ominous, ok bool) {
	initTrialVaultStates()
	m, exists := trialSpawnerMetaByID[state]
	if !exists {
		return "", false, false
	}
	return m.state, m.ominous, true
}

// MakeTrialSpawnerBlockState constructs the block state ID for `minecraft:trial_spawner`.
func MakeTrialSpawnerBlockState(spawnerState string, ominous bool) uint16 {
	initTrialVaultStates()
	switch spawnerState {
	case TrialSpawnerStateInactive,
		TrialSpawnerStateWaitingForPlayers,
		TrialSpawnerStateActive,
		TrialSpawnerStateWaitingForRewardEjection,
		TrialSpawnerStateEjectingReward,
		TrialSpawnerStateCooldown:
	default:
		spawnerState = TrialSpawnerStateWaitingForPlayers
	}
	if id, ok := nameToStateID("minecraft:trial_spawner", map[string]string{
		"trial_spawner_state": spawnerState,
		"ominous":             boolToProp(ominous),
	}); ok {
		return id
	}
	id, _ := nameToStateID("minecraft:trial_spawner", nil)
	return id
}

// ResolveTrialSpawnerPlacementState returns the initial block state for a placed
// `minecraft:trial_spawner`.
func ResolveTrialSpawnerPlacementState(ominous bool) uint16 {
	return MakeTrialSpawnerBlockState(TrialSpawnerStateInactive, ominous)
}

// IsVaultBlock reports whether state is `minecraft:vault`.
func IsVaultBlock(state uint16) bool {
	initTrialVaultStates()
	_, ok := vaultMetaByID[state]
	return ok
}

// ParseVaultBlockState extracts `vault_state`, `facing`, and `ominous` from state.
func ParseVaultBlockState(state uint16) (vaultState, facing string, ominous, ok bool) {
	initTrialVaultStates()
	m, exists := vaultMetaByID[state]
	if !exists {
		return "", "", false, false
	}
	return m.state, m.facing, m.ominous, true
}

// MakeVaultBlockState constructs the block state ID for `minecraft:vault`.
func MakeVaultBlockState(vaultState, facing string, ominous bool) uint16 {
	initTrialVaultStates()
	switch vaultState {
	case VaultStateInactive, VaultStateActive, VaultStateUnlocking, VaultStateEjecting:
	default:
		vaultState = VaultStateInactive
	}
	switch strings.ToLower(strings.TrimSpace(facing)) {
	case "north", "south", "west", "east":
		facing = strings.ToLower(strings.TrimSpace(facing))
	default:
		facing = "north"
	}
	if id, ok := nameToStateID("minecraft:vault", map[string]string{
		"vault_state": vaultState,
		"facing":      facing,
		"ominous":     boolToProp(ominous),
	}); ok {
		return id
	}
	id, _ := nameToStateID("minecraft:vault", nil)
	return id
}

// ResolveVaultPlacementState returns the initial block state for a player-placed
// `minecraft:vault`, orienting its keyhole (`facing`) opposite to
// `playerHorizontalFacing` so it faces the player (`VaultBlock.getStateForPlacement`).
func ResolveVaultPlacementState(playerHorizontalFacing string, ominous bool) uint16 {
	return MakeVaultBlockState(VaultStateInactive, oppositeHorizontalDir(playerHorizontalFacing), ominous)
}

// HasTrialSpawnerLineOfSight checks whether the straight line from a trial
// spawner center `(sx, sy, sz)` to a player/target `(tx, ty, tz)` (or eye
// height `ty + 1.5`) is unobstructed by opaque solid blocks (`TrialSpawner.hasLineOfSight`).
func (c *Cache) HasTrialSpawnerLineOfSight(sx, sy, sz, tx, ty, tz float64) bool {
	if c == nil {
		return true
	}
	if c.rayUnobstructedForTrialSpawner(sx, sy, sz, tx, ty+0.5, tz) {
		return true
	}
	return c.rayUnobstructedForTrialSpawner(sx, sy, sz, tx, ty+1.5, tz)
}

func (c *Cache) rayUnobstructedForTrialSpawner(x0, y0, z0, x1, y1, z1 float64) bool {
	dx := x1 - x0
	dy := y1 - y0
	dz := z1 - z0
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist < 1e-4 {
		return true
	}
	srcBX, srcBY, srcBZ := int(math.Floor(x0)), int(math.Floor(y0)), int(math.Floor(z0))
	dstBX, dstBY, dstBZ := int(math.Floor(x1)), int(math.Floor(y1)), int(math.Floor(z1))
	steps := int(math.Ceil(dist * 6.0))
	if steps < 4 {
		steps = 4
	}
	lastX, lastY, lastZ := math.MinInt32, math.MinInt32, math.MinInt32
	for i := 1; i < steps; i++ {
		t := float64(i) / float64(steps)
		bx := int(math.Floor(x0 + dx*t))
		by := int(math.Floor(y0 + dy*t))
		bz := int(math.Floor(z0 + dz*t))
		if bx == lastX && by == lastY && bz == lastZ {
			continue
		}
		lastX, lastY, lastZ = bx, by, bz
		if (bx == srcBX && by == srcBY && bz == srcBZ) || (bx == dstBX && by == dstBY && bz == dstBZ) {
			continue
		}
		if by < c.minY() || by >= c.maxY() {
			continue
		}
		st := c.GetBlock(bx, by, bz)
		if st == StateAir || IsTrialSpawnerBlock(st) || IsVaultBlock(st) || IsCopperGrateBlock(st) {
			continue
		}
		if lightOpacity(st) > 0 {
			return false
		}
	}
	return true
}

// FindTrialSpawnerMobSpawnPosition searches the 9x4x9 volume around a
// `minecraft:trial_spawner` at (sx, sy, sz) on c for a valid solid-supported
// 2-block-tall passable space with line-of-sight to the spawner.
func (c *Cache) FindTrialSpawnerMobSpawnPosition(sx, sy, sz int, rng *rand.Rand) (spawnX, spawnY, spawnZ float64, ok bool) {
	if c == nil {
		return float64(sx) + 0.5, float64(sy) + 1.0, float64(sz) + 0.5, false
	}
	var cands [][3]int
	spCenterX, spCenterY, spCenterZ := float64(sx)+0.5, float64(sy)+0.5, float64(sz)+0.5
	for dy := -1; dy <= 2; dy++ {
		y := sy + dy
		if y <= c.minY() || y+2 >= c.maxY() {
			continue
		}
		for dz := -4; dz <= 4; dz++ {
			for dx := -4; dx <= 4; dx++ {
				if dx == 0 && dy == 0 && dz == 0 {
					continue
				}
				if dx*dx+dz*dz > 16 {
					continue
				}
				x, z := sx+dx, sz+dz
				below := c.GetBlock(x, y-1, z)
				if isAirState(below) || hasWaterFluid(below) || isLavaBlock(below) {
					continue
				}
				cur := c.GetBlock(x, y, z)
				above := c.GetBlock(x, y+1, z)
				if !isAirState(cur) || !isAirState(above) {
					continue
				}
				tx, ty, tz := float64(x)+0.5, float64(y), float64(z)+0.5
				if !c.HasTrialSpawnerLineOfSight(spCenterX, spCenterY, spCenterZ, tx, ty, tz) {
					continue
				}
				cands = append(cands, [3]int{x, y, z})
			}
		}
	}
	if len(cands) > 0 {
		pick := cands[0]
		if rng != nil && len(cands) > 1 {
			pick = cands[rng.Intn(len(cands))]
		}
		return float64(pick[0]) + 0.5, float64(pick[1]), float64(pick[2]) + 0.5, true
	}
	return float64(sx) + 0.5, float64(sy) + 1.0, float64(sz) + 0.5, false
}

// VaultEjectPositionAndVelocity returns the item spawn position and initial
// velocity vector when a `minecraft:vault` at (vx, vy, vz) with `facing` ejects
// a reward item.
func VaultEjectPositionAndVelocity(vx, vy, vz int, facing string) (ex, ey, ez, velX, velY, velZ float64) {
	ex = float64(vx) + 0.5
	ey = float64(vy) + 1.0
	ez = float64(vz) + 0.5
	velY = 0.25
	switch strings.ToLower(strings.TrimSpace(facing)) {
	case "north":
		ez -= 0.35
		velZ = -0.12
	case "south":
		ez += 0.35
		velZ = 0.12
	case "west":
		ex -= 0.35
		velX = -0.12
	case "east":
		ex += 0.35
		velX = 0.12
	}
	return
}

// LoadedChunkStructureBlockEntities returns a copy of all structural block
// entities on the currently loaded chunk (cx, cz), or nil if the chunk is not
// resident in memory.
func (c *Cache) LoadedChunkStructureBlockEntities(cx, cz int32) []StructureBlockEntity {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	ch := c.chunks[[2]int32{cx, cz}]
	c.mu.Unlock()
	if ch == nil {
		return nil
	}
	return ch.StructureBlockEntities()
}

// AddChunkStructureBlockEntity records or updates a structural block entity on
// its chunk in c (loading/generating the chunk if needed).
func (c *Cache) AddChunkStructureBlockEntity(be StructureBlockEntity) bool {
	if c == nil || be.ID == "" {
		return false
	}
	cx := int32(be.X >> 4)
	cz := int32(be.Z >> 4)
	key := [2]int32{cx, cz}
	c.startUse(key)
	defer c.endUse(key)
	ch, err := c.chunkAtErr(cx, cz)
	if err != nil || ch == nil {
		return false
	}
	ch.AddStructureBlockEntity(be)
	c.markDirty(cx, cz)
	return true
}

