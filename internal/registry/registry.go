// Package registry holds the set of synchronized registries the server sends
// to the client during the configuration phase.
//
// The data in synced_registries.json was captured verbatim from the official
// 26.1.2 server: 28 registries in exact send order, each entry flagged
// has_data=false. Because we advertise the same "minecraft:core" known pack
// that a matching client already has, the client fills in each entry's data
// from its built-in pack, so we transmit only the entry identifiers. Entry
// order is significant: it defines the network (numeric) IDs used later in the
// play phase (biome IDs in chunks, dimension-type indices, and so on).
package registry

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed synced_registries.json
var syncedJSON []byte

// syncedTags is the verbatim body of the vanilla 26.1.2 update_tags packet,
// captured from the official server. Tags reference registry entries by their
// numeric (network) index, so this blob is only valid because our synchronized
// registries are sent in the exact same order vanilla uses.
//
//go:embed synced_tags.bin
var syncedTags []byte

// Tags returns the update_tags packet body. The slice is shared and must not
// be mutated.
func Tags() []byte { return syncedTags }

// Registry is one synchronized registry and its ordered entry identifiers.
type Registry struct {
	Name    string   `json:"name"`
	Entries []string `json:"entries"`
}

// synced is the parsed, ordered list loaded once at init.
var synced []Registry

func init() {
	if err := json.Unmarshal(syncedJSON, &synced); err != nil {
		panic(fmt.Sprintf("registry: parsing embedded synced_registries.json: %v", err))
	}
}

// Synced returns the ordered synchronized registries. The slice is shared and
// must not be mutated.
func Synced() []Registry { return synced }

// Index returns the zero-based position of entry within the named registry,
// which is the numeric (network) ID the client assigns it. It returns -1 if
// the registry or entry is unknown.
func Index(registryName, entry string) int {
	for _, reg := range synced {
		if reg.Name != registryName {
			continue
		}
		for i, e := range reg.Entries {
			if e == entry {
				return i
			}
		}
	}
	return -1
}

// KnownPack identifies a resource/data pack advertised via select_known_packs.
type KnownPack struct {
	Namespace string
	ID        string
	Version   string
}

// CorePack is the vanilla built-in pack. Advertising it lets a matching client
// supply registry contents from its own copy.
var CorePack = KnownPack{Namespace: "minecraft", ID: "core", Version: "26.1.2"}
