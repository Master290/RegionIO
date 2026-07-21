package world

import (
	_ "embed"
	"encoding/json"
	"sync"

	"regionio/internal/nbt"
)

// state_names.go bridges in-memory uint16 block-state IDs and the named palette
// form used by on-disk chunk NBT ({Name, Properties}). The wire/network format
// uses integer IDs; the disk format uses names, so serialization needs both
// directions. The table is built once from the embedded blocks.json report.

//go:embed blocks.json
var blocksReportJSON []byte

// stateName describes one block state for the on-disk palette.
type stateName struct {
	Name       string
	Properties map[string]string // nil when the block has no state properties
}

var (
	stateByIDOnce sync.Once
	stateByIDImpl map[uint16]stateName
	idsByName     map[string][]uint16
)

// stateByID returns the named form of a block-state ID, building the lookup
// table on first use. The default state of each block (or its lowest-id state)
// is recorded; this matches what our generator emits, where each StateXxx
// constant is the default-state ID. For multi-state blocks the full ID→state
// map is loaded so every state round-trips.
func stateByID(id uint16) (stateName, bool) {
	stateByIDOnce.Do(buildStateTable)
	s, ok := stateByIDImpl[id]
	return s, ok
}

func buildStateTable() {
	var blocks map[string]struct {
		States []struct {
			ID         int               `json:"id"`
			Properties map[string]string `json:"properties"`
		} `json:"states"`
	}
	if err := json.Unmarshal(blocksReportJSON, &blocks); err != nil {
		panic("world: parsing embedded blocks.json: " + err.Error())
	}
	stateByIDImpl = make(map[uint16]stateName, 30000)
	idsByName = make(map[string][]uint16, len(blocks))
	for name, b := range blocks {
		for _, s := range b.States {
			if s.ID < 0 || s.ID > 65535 {
				continue
			}
			id := uint16(s.ID)
			stateByIDImpl[id] = stateName{Name: name, Properties: s.Properties}
			idsByName[name] = append(idsByName[name], id)
		}
	}
}

// blockPaletteEntry builds the NBT compound for a block-state ID: {Name,
// Properties} (Properties omitted when empty). Unknown IDs map to air.
func blockPaletteEntry(id uint16) *nbt.Compound {
	s, ok := stateByID(id)
	if !ok {
		s = stateName{Name: "minecraft:air"}
	}
	c := nbt.NewCompound().Set("Name", nbt.String(s.Name))
	if len(s.Properties) > 0 {
		props := nbt.NewCompound()
		for k, v := range s.Properties {
			props.Set(k, nbt.String(v))
		}
		c.Set("Properties", props)
	}
	return c
}

// paletteEntryKey is a stable hashable key for deduplicating palette entries by
// (name, properties) during reverse lookup.
type paletteEntryKey struct {
	name string
	sig  string
}

// nameToStateID returns the block-state ID for a (name, properties) pair from
// the loaded table. It is used when decoding on-disk chunk NBT back into a
// Chunk. Unknown names/properties map to air (0).
func nameToStateID(name string, props map[string]string) uint16 {
	stateByIDOnce.Do(buildStateTable)
	ids := idsByName[name]
	for _, id := range ids {
		if propsMatch(stateByIDImpl[id].Properties, props) {
			return id
		}
	}
	if len(ids) > 0 {
		return ids[0]
	}
	return StateAir
}

func propsMatch(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
