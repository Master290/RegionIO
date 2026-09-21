package world

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// Debug-only replay instrumentation. Every switch here is read once, lazily, and
// is off in a normal server run. They live in one file so that "which code paths
// can print during generation" is answerable from a single name.
//
// Trace coordinates must come from the environment, never from source literals:
// hardcoded coordinate filters have twice left this package not compiling, and a
// debug helper that only a test file can call is a production build that only a
// test build proves.

// lushClayTrace turns on the position/roll/column traces of the lush-caves clay
// chain while stage-9 vegetation patches replay.
var lushClayTrace = sync.OnceValue(func() bool {
	return os.Getenv("REGIONIO_LUSH_CLAY_TRACE") == "1"
})

// setBlockTrace holds the world cells whose writes should be attributed to a
// writer, parsed from REGIONIO_SETBLOCK_TRACE="x,y,z[;x,y,z...]".
//
// Keyed by the whole 3-tuple. The first version keyed on x and packed y:z into
// the value, which broke two ways: two traced cells sharing an x silently
// overwrote each other, and an absent key returns the zero int64, which is
// exactly what pack(0, 0) yields - so every write to (x, 0, 0) for an untraced
// x printed a stack trace nobody asked for.
var setBlockTrace = sync.OnceValue(func() map[[3]int]bool {
	spec := os.Getenv("REGIONIO_SETBLOCK_TRACE")
	if spec == "" {
		return nil
	}
	trace := make(map[[3]int]bool)
	for _, entry := range strings.Split(spec, ";") {
		parts := strings.Split(strings.TrimSpace(entry), ",")
		if len(parts) != 3 {
			continue
		}
		x, errX := strconv.Atoi(parts[0])
		y, errY := strconv.Atoi(parts[1])
		z, errZ := strconv.Atoi(parts[2])
		if errX != nil || errY != nil || errZ != nil {
			continue
		}
		trace[[3]int{x, y, z}] = true
	}
	return trace
})

// traceSetBlock reports whether this cell's writes should be attributed.
func traceSetBlock(x, y, z int) bool {
	trace := setBlockTrace()
	return trace != nil && trace[[3]int{x, y, z}]
}

// lushClayColumn holds the world (x,z) columns whose full Y profile should be
// dumped while a lush-caves clay pool places, parsed from
// REGIONIO_LUSH_CLAY_COLUMN="x,z[;x,z...]". Which column to watch is a question
// about the current mismatch, so it is never baked into the source.
var lushClayColumn = sync.OnceValue(func() map[[2]int]bool {
	spec := os.Getenv("REGIONIO_LUSH_CLAY_COLUMN")
	if spec == "" {
		return nil
	}
	columns := make(map[[2]int]bool)
	for _, entry := range strings.Split(spec, ";") {
		parts := strings.Split(strings.TrimSpace(entry), ",")
		if len(parts) != 2 {
			continue
		}
		x, errX := strconv.Atoi(parts[0])
		z, errZ := strconv.Atoi(parts[1])
		if errX != nil || errZ != nil {
			continue
		}
		columns[[2]int{x, z}] = true
	}
	return columns
})

// traceLushClayColumn reports whether this column's profile should be dumped.
func traceLushClayColumn(x, z int) bool {
	columns := lushClayColumn()
	return columns != nil && columns[[2]int{x, z}]
}

// dumpTraceStack prints the world-side call chain that reached a write. The
// buffer is 64 KB because 4 KB truncated away the frames that identify the
// feature doing the writing.
func dumpTraceStack() {
	buf := make([]byte, 1<<16)
	n := runtime.Stack(buf, false)
	for _, line := range strings.Split(string(buf[:n]), "\n") {
		if strings.Contains(line, "regionio/internal/world") {
			fmt.Println(strings.TrimSpace(line))
		}
	}
}
