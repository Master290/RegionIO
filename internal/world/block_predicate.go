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
	case "minecraft:solid":
		// SolidPredicate = BlockState.isSolid = the precomputed legacySolid
		// flag: force-solid overrides, else the collision shape's average
		// side >= 0.729 or ysize >= 1. In the cave context that is the full
		// opaque cubes (stone/moss/clay) and NOT the small-collision plants
		// (moss_carpet fails both tests in vanilla but blocks motion).
		return fullSolidState(r.getBlock(position.X, position.Y, position.Z)), nil
	case "minecraft:would_survive":
		// WouldSurvivePredicate: would the given state survive at the
		// position. Only sugar cane reaches this in the replayed stages
		// (SugarCaneBlock.canSurvive: below is sugar cane, or below is
		// dirt-family/sand with an adjacent water cell below the rim).
		var value struct {
			State worldgen.BlockState `json:"state"`
		}
		if err := json.Unmarshal(raw, &value); err != nil || value.State.Name == "" {
			return false, fmt.Errorf("world: would_survive missing state")
		}
		if value.State.Name == "minecraft:sugar_cane" {
			below, ok := stateByID(r.getBlock(position.X, position.Y-1, position.Z))
			if !ok {
				return false, nil
			}
			if below.Name == "minecraft:sugar_cane" {
				return true, nil
			}
			if !flattenBlockTagContains(set, "minecraft:dirt", below.Name) &&
				below.Name != "minecraft:sand" && below.Name != "minecraft:red_sand" && below.Name != "minecraft:podzol" {
				return false, nil
			}
			for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				nx, nz := position.X+d[0], position.Z+d[1]
				neighbor, ok := stateByID(r.getBlock(nx, position.Y-1, nz))
				if ok && (neighbor.Name == "minecraft:frosted_ice") {
					return true, nil
				}
				if isWaterState(r.getBlock(nx, position.Y-1, nz)) {
					return true, nil
				}
			}
			return false, nil
		}
		// Conservative default: the generic vegetation survival check.
		state, ok := nameToStateID(value.State.Name, value.State.Properties)
		if !ok {
			return false, nil
		}
		return r.getBlock(position.X, position.Y, position.Z) == state && r.canVegetationSurvive(position, value.State.Name, set), nil
	case "minecraft:has_sturdy_face":
		// HasSturdyFacePredicate: the block at pos+offset presents a sturdy
		// face in the given direction. Our full-cube approximation covers
		// every state the cave scans meet (stone/deepslate/moss/clay);
		// non-cubes (vines, lichen) present no sturdy face in vanilla.
		var value struct {
			Direction string `json:"direction"`
		}
		if err := json.Unmarshal(raw, &value); err != nil || value.Direction == "" {
			return false, fmt.Errorf("world: has_sturdy_face missing direction")
		}
		return fullSolidState(r.getBlock(position.X, position.Y, position.Z)), nil
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

var (
	airStateIDs  map[uint16]bool
	airStateOnce bool
)

// isAirState mirrors BlockState.isAir(): true for minecraft:air, cave_air,
// and void_air. Features carve with cave_air (lakes, mineshafts, monster
// rooms), so any "is this empty" check must treat all three as air exactly
// like vanilla's isAir()/isEmptyBlock - comparing against StateAir (plain
// air) silently treats carved caves as solid.
func isAirState(state uint16) bool {
	if !airStateOnce {
		airStateOnce = true
		stateByIDOnce.Do(buildStateTable)
		airStateIDs = make(map[uint16]bool, 3)
		for _, name := range []string{"minecraft:air", "minecraft:cave_air", "minecraft:void_air"} {
			if id, ok := nameToStateID(name, nil); ok {
				airStateIDs[id] = true
			}
		}
	}
	return airStateIDs[state]
}

func flattenBlockTagContains(set *worldgen.FeatureSet, tag, name string) bool {
	for _, member := range flattenBlockTag(set, tag, nil) {
		if member == name {
			return true
		}
	}
	return false
}
