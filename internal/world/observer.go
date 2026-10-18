package world

import (
	"sort"
	"strings"
	"sync"

	"regionio/internal/nbt"
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

// --- Observer Runtime Persistence & Chunk Unload/Reload ---

// ObserverRuntimeSnapshot captures the queued retrigger flag for an active
// `minecraft:observer` at (X, Y, Z) across save/reload or chunk unload/reload.
type ObserverRuntimeSnapshot struct {
	X, Y, Z           int
	RetriggerOnFinish bool
}

func sortObserverSnapshots(entries []ObserverRuntimeSnapshot) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Y != entries[j].Y {
			return entries[i].Y < entries[j].Y
		}
		if entries[i].Z != entries[j].Z {
			return entries[i].Z < entries[j].Z
		}
		return entries[i].X < entries[j].X
	})
}

// ExportObserverRuntimeEntries returns all observers with a pending retrigger-on-finish flag.
func (c *Cache) ExportObserverRuntimeEntries() []ObserverRuntimeSnapshot {
	if c == nil {
		return nil
	}
	tr := c.getObserverTracker()
	tr.mu.Lock()
	var out []ObserverRuntimeSnapshot
	for pos, retrig := range tr.retriggerOnFinish {
		if retrig {
			out = append(out, ObserverRuntimeSnapshot{
				X:                 pos.X,
				Y:                 pos.Y,
				Z:                 pos.Z,
				RetriggerOnFinish: true,
			})
		}
	}
	tr.mu.Unlock()
	sortObserverSnapshots(out)
	return out
}

// ImportObserverRuntimeEntries restores observer retrigger-on-finish flags onto c
// and ensures that each referenced observer has a scheduled 2-tick update queued.
func (c *Cache) ImportObserverRuntimeEntries(entries []ObserverRuntimeSnapshot) int {
	if c == nil || len(entries) == 0 {
		return 0
	}
	tr := c.getObserverTracker()
	tr.mu.Lock()
	var toSchedule []BlockPos
	imported := 0
	for _, snap := range entries {
		if snap.Y < MinY || snap.Y >= MinY+WorldHeight || !snap.RetriggerOnFinish {
			continue
		}
		pos := BlockPos{X: snap.X, Y: snap.Y, Z: snap.Z}
		tr.retriggerOnFinish[pos] = true
		toSchedule = append(toSchedule, pos)
		imported++
	}
	tr.mu.Unlock()

	for _, pos := range toSchedule {
		if !c.HasPendingBlockUpdate(pos.X, pos.Y, pos.Z, UpdateRedstone) {
			c.ScheduleBlockUpdate(pos.X, pos.Y, pos.Z, UpdateRedstone, ObserverTickDelay)
		}
	}
	return imported
}

// UnloadChunkObservers extracts and removes any observer runtime entries in chunk (cx, cz).
func (c *Cache) UnloadChunkObservers(cx, cz int32) []ObserverRuntimeSnapshot {
	if c == nil {
		return nil
	}
	tr := c.getObserverTracker()
	tr.mu.Lock()
	var out []ObserverRuntimeSnapshot
	for pos, retrig := range tr.retriggerOnFinish {
		if int32(pos.X>>4) == cx && int32(pos.Z>>4) == cz {
			if retrig {
				out = append(out, ObserverRuntimeSnapshot{
					X:                 pos.X,
					Y:                 pos.Y,
					Z:                 pos.Z,
					RetriggerOnFinish: true,
				})
			}
			delete(tr.retriggerOnFinish, pos)
		}
	}
	tr.mu.Unlock()
	sortObserverSnapshots(out)
	return out
}

// EncodeObserverRuntimeNBT encodes ObserverRuntimeSnapshot entries into an NBT list.
func EncodeObserverRuntimeNBT(entries []ObserverRuntimeSnapshot) nbt.List {
	if len(entries) == 0 {
		return nbt.List{ElemID: nbt.TagEnd}
	}
	elems := make([]nbt.Tag, 0, len(entries))
	for _, e := range entries {
		var retrigByte int8
		if e.RetriggerOnFinish {
			retrigByte = 1
		}
		comp := nbt.NewCompound().
			Set("x", nbt.Int(int32(e.X))).
			Set("y", nbt.Int(int32(e.Y))).
			Set("z", nbt.Int(int32(e.Z))).
			Set("retrigger", nbt.Byte(retrigByte))
		elems = append(elems, comp)
	}
	return nbt.List{
		ElemID: nbt.TagCompound,
		Elems:  elems,
	}
}

// DecodeObserverRuntimeNBT decodes ObserverRuntimeSnapshot entries from an NBT list.
func DecodeObserverRuntimeNBT(list nbt.List) []ObserverRuntimeSnapshot {
	if len(list.Elems) == 0 {
		return nil
	}
	out := make([]ObserverRuntimeSnapshot, 0, len(list.Elems))
	for _, elem := range list.Elems {
		comp, ok := elem.(*nbt.Compound)
		if !ok || comp == nil {
			continue
		}
		retrig := true
		if t, ok := comp.Get("retrigger"); ok {
			if v, ok := t.(nbt.Byte); ok {
				retrig = v != 0
			}
		}
		if !retrig {
			continue
		}
		out = append(out, ObserverRuntimeSnapshot{
			X:                 int(nbtAsInt(comp, "x")),
			Y:                 int(nbtAsInt(comp, "y")),
			Z:                 int(nbtAsInt(comp, "z")),
			RetriggerOnFinish: true,
		})
	}
	return out
}

