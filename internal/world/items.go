package world

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
)

//go:embed item_blocks.json
var itemBlocksJSON []byte

// itemToBlock maps an item's network ID to the default block state placed when
// that item is used. Built from the item registry crossed with block defaults
// (items whose name is a block). Items with no block (tools, food) are absent.
var itemToBlock map[int32]uint16

func init() {
	raw := make(map[string]int)
	if err := json.Unmarshal(itemBlocksJSON, &raw); err != nil {
		panic(fmt.Sprintf("world: parsing item_blocks.json: %v", err))
	}
	itemToBlock = make(map[int32]uint16, len(raw))
	for k, v := range raw {
		id, err := strconv.Atoi(k)
		if err != nil {
			panic(fmt.Sprintf("world: bad item id %q: %v", k, err))
		}
		itemToBlock[int32(id)] = uint16(v)
	}
}

// ItemToBlock returns the block state placed by an item, and whether the item
// is a placeable block.
func ItemToBlock(itemID int32) (uint16, bool) {
	s, ok := itemToBlock[itemID]
	return s, ok
}
