package world

import (
	"math/rand"
	"sync"
)

// DefaultTNTFuseTicks is vanilla Minecraft's default PrimedTnt fuse duration (80 game ticks = 4.0 seconds).
const (
	DefaultTNTFuseTicks  = 80
	MinChainTNTFuseTicks = 10
	MaxChainTNTFuseTicks = 29
)

// TNTIgnitionCause classifies what triggered a TNT block to prime.
type TNTIgnitionCause string

const (
	TNTIgnitionRedstone      TNTIgnitionCause = "redstone"
	TNTIgnitionFlintAndSteel TNTIgnitionCause = "flint_and_steel"
	TNTIgnitionFireCharge    TNTIgnitionCause = "fire_charge"
	TNTIgnitionProjectile    TNTIgnitionCause = "projectile"
	TNTIgnitionFire          TNTIgnitionCause = "fire"
	TNTIgnitionExplosion     TNTIgnitionCause = "explosion"
	TNTIgnitionUnstableBreak TNTIgnitionCause = "unstable_break"
)

// TNTPrimeHook is an optional callback registered on a Cache by the server to
// spawn a PrimedTNT entity when a minecraft:tnt block at pos is ignited.
type TNTPrimeHook func(c *Cache, pos BlockPos, fuseTicks int, cause TNTIgnitionCause)

type cacheTNTState struct {
	mu          sync.Mutex
	tntExplodes bool
	primeHook   TNTPrimeHook
}

var cacheTNTStates sync.Map // map[*Cache]*cacheTNTState

func (c *Cache) tntState() *cacheTNTState {
	if s, ok := cacheTNTStates.Load(c); ok {
		return s.(*cacheTNTState)
	}
	created := &cacheTNTState{
		tntExplodes: true,
	}
	actual, _ := cacheTNTStates.LoadOrStore(c, created)
	return actual.(*cacheTNTState)
}

// TNTExplodes returns whether gamerule tntExplodes is enabled on c (default true).
func (c *Cache) TNTExplodes() bool {
	if c == nil {
		return false
	}
	st := c.tntState()
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.tntExplodes
}

// SetTNTExplodes sets whether gamerule tntExplodes is enabled on c.
func (c *Cache) SetTNTExplodes(enabled bool) {
	if c == nil {
		return
	}
	st := c.tntState()
	st.mu.Lock()
	st.tntExplodes = enabled
	st.mu.Unlock()
}

// SetTNTPrimeHook registers the callback invoked when a TNT block in c is primed.
func (c *Cache) SetTNTPrimeHook(hook TNTPrimeHook) {
	if c == nil {
		return
	}
	st := c.tntState()
	st.mu.Lock()
	st.primeHook = hook
	st.mu.Unlock()
}

// IsTNTBlock reports whether state is a minecraft:tnt block.
func IsTNTBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:tnt")
}

// IsUnstableTNTState reports whether state is minecraft:tnt[unstable=true].
func IsUnstableTNTState(state uint16) bool {
	info, ok := stateByID(state)
	return ok && info.Name == "minecraft:tnt" && info.Properties["unstable"] == "true"
}

// TNTBlockState returns the block state ID for minecraft:tnt with the given unstable flag.
func TNTBlockState(unstable bool) uint16 {
	u := "false"
	if unstable {
		u = "true"
	}
	if id, ok := nameToStateID("minecraft:tnt", map[string]string{"unstable": u}); ok {
		return id
	}
	if id, ok := nameToStateID("minecraft:tnt", nil); ok {
		return id
	}
	return StateAir
}

// RollChainTNTFuseTicks rolls the shortened fuse duration (10..29 game ticks,
// i.e. fuse/8 + rand(fuse/4) where default fuse = 80) when a TNT block is
// detonated by another explosion (vanilla PrimedTnt.wasExploded).
func RollChainTNTFuseTicks(rng *rand.Rand) int {
	base := DefaultTNTFuseTicks / 8 // 10
	span := DefaultTNTFuseTicks / 4 // 20
	if rng == nil {
		return base + span/2
	}
	return base + rng.Intn(span)
}

// PrimeTNTAt removes the minecraft:tnt block at (x, y, z) and invokes the
// registered TNTPrimeHook (with fuse = 80 ticks, or 10..29 ticks for
// TNTIgnitionExplosion) if gamerule tntExplodes is enabled.
func (c *Cache) PrimeTNTAt(x, y, z int, cause TNTIgnitionCause, rng *rand.Rand) (bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	if !c.TNTExplodes() {
		return false, res
	}
	if !IsTNTBlock(c.GetBlock(x, y, z)) {
		return false, res
	}
	ok, brkRes := c.BreakBlockWithUpdates(x, y, z)
	if !ok {
		return false, res
	}
	res.merge(brkRes)
	fuse := DefaultTNTFuseTicks
	if cause == TNTIgnitionExplosion {
		fuse = RollChainTNTFuseTicks(rng)
	}
	c.notifyTNTPrimed(x, y, z, fuse, cause)
	return true, res
}

func (c *Cache) notifyTNTPrimed(x, y, z int, fuse int, cause TNTIgnitionCause) {
	st := c.tntState()
	st.mu.Lock()
	hook := st.primeHook
	st.mu.Unlock()
	if hook != nil {
		hook(c, BlockPos{X: x, Y: y, Z: z}, fuse, cause)
	}
}

// evaluateTNTRedstone checks if the block at (x, y, z) is a minecraft:tnt block
// receiving redstone power while tntExplodes is true; if so, it removes the TNT
// block and primes it with an 80-tick fuse.
func (c *Cache) evaluateTNTRedstone(x, y, z int, res *BlockUpdateResult) bool {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false
	}
	if !c.TNTExplodes() {
		return false
	}
	if !IsTNTBlock(c.GetBlock(x, y, z)) {
		return false
	}
	if !c.IsBlockPowered(x, y, z) {
		return false
	}
	if c.setBlockRecorded(x, y, z, StateAir, res) {
		c.runNeighborSupportPass(x, y, z, res)
		c.scheduleAround(x, y, z)
		c.notifyTNTPrimed(x, y, z, DefaultTNTFuseTicks, TNTIgnitionRedstone)
		return true
	}
	return false
}

// checkPoweredTNTAround scans (x, y, z) and blocks within Manhattan distance <= 2
// for any minecraft:tnt block that is now powered by redstone.
func (c *Cache) checkPoweredTNTAround(x, y, z int, res *BlockUpdateResult) {
	if c == nil || !c.TNTExplodes() {
		return
	}
	for dy := -2; dy <= 2; dy++ {
		ny := y + dy
		if ny < MinY || ny >= MinY+WorldHeight {
			continue
		}
		for dz := -2; dz <= 2; dz++ {
			for dx := -2; dx <= 2; dx++ {
				if absInt(dx)+absInt(dy)+absInt(dz) > 2 {
					continue
				}
				nx, nz := x+dx, z+dz
				if IsTNTBlock(c.GetBlock(nx, ny, nz)) {
					c.evaluateTNTRedstone(nx, ny, nz, res)
				}
			}
		}
	}
}
