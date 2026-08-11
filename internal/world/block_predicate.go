package world

import (
	"encoding/json"
	"fmt"

	"regionio/internal/worldgen"
)

type rawBlockPredicate struct {
	Type       string            `json:"type"`
	Offset     [3]int            `json:"offset"`
	Blocks     json.RawMessage   `json:"blocks"`
	Tag        string            `json:"tag"`
	Fluids     json.RawMessage   `json:"fluids"`
	Predicate  json.RawMessage   `json:"predicate"`
	Predicates []json.RawMessage `json:"predicates"`
}

func (r *decorationRegion) testBlockPredicate(set *worldgen.FeatureSet, raw json.RawMessage, position worldgen.FeaturePosition) (bool, error) {
	var predicate rawBlockPredicate
	if err := json.Unmarshal(raw, &predicate); err != nil {
		return false, fmt.Errorf("world: decode block predicate: %w", err)
	}
	position.X += predicate.Offset[0]
	position.Y += predicate.Offset[1]
	position.Z += predicate.Offset[2]
	switch predicate.Type {
	case "minecraft:true":
		return true, nil
	case "minecraft:inside_world_bounds":
		return position.Y >= MinY && position.Y < MinY+WorldHeight, nil
	case "minecraft:matching_blocks":
		names, err := decodeStringList(predicate.Blocks)
		if err != nil {
			return false, fmt.Errorf("world: matching_blocks: %w", err)
		}
		state, ok := stateByID(r.getBlock(position.X, position.Y, position.Z))
		if !ok {
			return false, nil
		}
		for _, name := range names {
			if state.Name == name {
				return true, nil
			}
		}
		return false, nil
	case "minecraft:matching_block_tag":
		if predicate.Tag == "" {
			return false, fmt.Errorf("world: matching_block_tag missing tag")
		}
		state, ok := stateByID(r.getBlock(position.X, position.Y, position.Z))
		if !ok {
			return false, nil
		}
		for _, name := range flattenBlockTag(set, predicate.Tag, nil) {
			if state.Name == name {
				return true, nil
			}
		}
		return false, nil
	case "minecraft:matching_fluids":
		fluids, err := decodeStringList(predicate.Fluids)
		if err != nil {
			return false, fmt.Errorf("world: matching_fluids: %w", err)
		}
		state := r.getBlock(position.X, position.Y, position.Z)
		for _, fluid := range fluids {
			if stateFlags(state)&flagFluid != 0 &&
				(fluid == "minecraft:water" || fluid == "minecraft:flowing_water") && isWaterState(state) {
				return true, nil
			}
			if stateFlags(state)&flagFluid != 0 &&
				(fluid == "minecraft:lava" || fluid == "minecraft:flowing_lava") && isLavaState(state) {
				return true, nil
			}
		}
		return false, nil
	case "minecraft:all_of":
		for _, child := range predicate.Predicates {
			ok, err := r.testBlockPredicate(set, child, position)
			if err != nil || !ok {
				return ok, err
			}
		}
		return true, nil
	case "minecraft:any_of":
		for _, child := range predicate.Predicates {
			ok, err := r.testBlockPredicate(set, child, position)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	case "minecraft:not":
		if len(predicate.Predicate) == 0 {
			return false, fmt.Errorf("world: not predicate missing child")
		}
		ok, err := r.testBlockPredicate(set, predicate.Predicate, position)
		return !ok, err
	default:
		return false, fmt.Errorf("world: unsupported block predicate %q", predicate.Type)
	}
}

func decodeStringList(raw json.RawMessage) ([]string, error) {
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil || len(many) == 0 {
		return nil, fmt.Errorf("invalid string list %s", raw)
	}
	return many, nil
}

func isWaterState(state uint16) bool {
	value, ok := stateByID(state)
	return ok && stateFlags(state)&flagFluid != 0 && value.Name != "minecraft:lava"
}

func isLavaState(state uint16) bool {
	value, ok := stateByID(state)
	return ok && value.Name == "minecraft:lava"
}
