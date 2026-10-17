package world

import (
	"strings"
	"sync"
)

// ObserverTickDelay is the vanilla 2-game-tick (1 redstone tick) delay before
// an observer emits a pulse after detecting a block state change on its front
// face, as well as the 2-game-tick duration of that pulse.
const ObserverTickDelay = 2

type observerTracker struct {
	mu                sync.Mutex
	retriggerOnFinish map[BlockPos]bool
}

var observerTrackers sync.Map // map[*Cache]*observerTracker

func (c *Cache) getObserverTracker() *observerTracker {
	if v, ok := observerTrackers.Load(c); ok {
		return v.(*observerTracker)
	}
	created := &observerTracker{
		retriggerOnFinish: make(map[BlockPos]bool),
	}
	actual, _ := observerTrackers.LoadOrStore(c, created)
	return actual.(*observerTracker)
}

// IsObserverBlock reports whether state is a `minecraft:observer`.
func IsObserverBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:observer")
}

// ObserverFacing returns the observing direction ("north", "south", "west",
// "east", "up", "down") of a `minecraft:observer` state.
func ObserverFacing(state uint16) string {
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:observer" {
		return "south"
	}
	if f := info.Properties["facing"]; f != "" {
		return f
	}
	return "south"
}

// ObserverPowered reports whether a `minecraft:observer` state has `powered="true"`.
func ObserverPowered(state uint16) bool {
	info, ok := stateByID(state)
	return ok && info.Name == "minecraft:observer" && info.Properties["facing"] != "" && info.Properties["powered"] == "true"
}

// ObserverFacingForPlacement returns the `facing` property for a newly placed
// `minecraft:observer` given the player's 3D look direction (`lookDir`),
// matching vanilla `ObserverBlock.getStateForPlacement` where the observing
// face points in the direction the player is looking (and the redstone output
// on the back faces the player).
func ObserverFacingForPlacement(lookDir string) string {
	switch strings.ToLower(strings.TrimSpace(lookDir)) {
	case "north", "south", "west", "east", "up", "down":
		return strings.ToLower(strings.TrimSpace(lookDir))
	default:
		return "south"
	}
}

// ObserverObservedPos returns the world coordinates (ox, oy, oz) of the block
// in front of a `minecraft:observer` at (x, y, z) with `facing`.
func ObserverObservedPos(x, y, z int, facing string) (ox, oy, oz int) {
	dx, dy, dz, ok := directionOffset3D(facing)
	if !ok {
		return x, y, z + 1
	}
	return x + dx, y + dy, z + dz
}

// ObserverOutputDirection returns the direction ("north", "south", "west",
// "east", "up", "down") out of which a `minecraft:observer` with `facing`
// emits its redstone pulse (`oppositeDirection(facing)`).
func ObserverOutputDirection(facing string) string {
	return oppositeDirection(facing)
}

// ObserverOutputPos returns the world coordinates (bx, by, bz) of the block
// directly behind a `minecraft:observer` at (x, y, z) with `facing` that
// receives its strong and weak redstone pulse.
func ObserverOutputPos(x, y, z int, facing string) (bx, by, bz int) {
	dx, dy, dz, ok := directionOffset3D(oppositeDirection(facing))
	if !ok {
		return x, y, z - 1
	}
	return x + dx, y + dy, z + dz
}

// notifyObserversOfChangeAt checks the 6 neighbors of (x, y, z) after a block
// state transition (`oldState -> newState`) and schedules a 2-tick pulse on any
// adjacent `minecraft:observer` whose `facing` points at (x, y, z).
func (c *Cache) notifyObserversOfChangeAt(x, y, z int, oldState, newState uint16) {
	if c == nil || oldState == newState {
		return
	}
	for _, off := range sixNeighborOffsets {
		nx, ny, nz := x+off[0], y+off[1], z+off[2]
		if ny < MinY || ny >= MinY+WorldHeight {
			continue
		}
		nState := c.GetBlock(nx, ny, nz)
		info, ok := stateByID(nState)
		if !ok || info.Name != "minecraft:observer" {
			continue
		}
		ox, oy, oz := ObserverObservedPos(nx, ny, nz, info.Properties["facing"])
		if ox != x || oy != y || oz != z {
			continue
		}
		pos := BlockPos{X: nx, Y: ny, Z: nz}
		if info.Properties["powered"] == "true" {
			tr := c.getObserverTracker()
			tr.mu.Lock()
			tr.retriggerOnFinish[pos] = true
			tr.mu.Unlock()
		}
		c.ScheduleBlockUpdate(nx, ny, nz, UpdateRedstone, ObserverTickDelay)
	}
}

func (c *Cache) stepObserverUpdate(x, y, z int, state uint16, info stateName, res *BlockUpdateResult) {
	pos := BlockPos{X: x, Y: y, Z: z}
	tr := c.getObserverTracker()

	if info.Properties["powered"] == "true" {
		// End the 2-tick pulse -> powered="false".
		nextState, ok := withStateProperty(state, "powered", "false")
		if !ok {
			return
		}
		if c.setBlockRecorded(x, y, z, nextState, res) {
			c.setInShortPulseEnd(true)
			c.propagateRedstoneAround(x, y, z, state, nextState, res)
			c.setInShortPulseEnd(false)
		}
		tr.mu.Lock()
		retrigger := tr.retriggerOnFinish[pos]
		delete(tr.retriggerOnFinish, pos)
		tr.mu.Unlock()
		if retrigger {
			c.ScheduleBlockUpdate(x, y, z, UpdateRedstone, ObserverTickDelay)
		}
		return
	}

	// Start the 2-tick pulse -> powered="true" and schedule turn-off in 2 ticks.
	tr.mu.Lock()
	delete(tr.retriggerOnFinish, pos)
	tr.mu.Unlock()

	nextState, ok := withStateProperty(state, "powered", "true")
	if !ok {
		return
	}
	if c.setBlockRecorded(x, y, z, nextState, res) {
		c.ScheduleBlockUpdate(x, y, z, UpdateRedstone, ObserverTickDelay)
		c.propagateRedstoneAround(x, y, z, state, nextState, res)
	}
}
