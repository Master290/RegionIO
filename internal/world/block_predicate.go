package world

import (
	"encoding/json"
	"fmt"
	"sync"

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

const maxCompiledBlockPredicates = 2048

var (
	compiledBlockPredicatesMu sync.RWMutex
	compiledBlockPredicates   = make(map[string]*compiledBlockPredicate)
)

type compiledBlockPredicate struct {
	typ              string
	offset           [3]int
	surviveStateName string
	direction        string
	blocks           []string
	tag              string
	matchWater       bool
	matchLava        bool
	child            *compiledBlockPredicate
	children         []*compiledBlockPredicate
	err              error
}

func getCompiledBlockPredicate(raw json.RawMessage) *compiledBlockPredicate {
	compiledBlockPredicatesMu.RLock()
	if p, ok := compiledBlockPredicates[string(raw)]; ok {
		compiledBlockPredicatesMu.RUnlock()
		return p
	}
	compiledBlockPredicatesMu.RUnlock()

	p := parseBlockPredicate(raw)
	compiledBlockPredicatesMu.Lock()
	if len(compiledBlockPredicates) >= maxCompiledBlockPredicates {
		clear(compiledBlockPredicates)
	}
	compiledBlockPredicates[string(raw)] = p
	compiledBlockPredicatesMu.Unlock()
	return p
}

func parseBlockPredicate(raw json.RawMessage) *compiledBlockPredicate {
	var predicate rawBlockPredicate
	if err := json.Unmarshal(raw, &predicate); err != nil {
		return &compiledBlockPredicate{err: fmt.Errorf("world: decode block predicate: %w", err)}
	}
	p := &compiledBlockPredicate{
		typ:    predicate.Type,
		offset: predicate.Offset,
	}
	switch predicate.Type {
	case "minecraft:true", "minecraft:solid", "minecraft:inside_world_bounds":
		return p
	case "minecraft:would_survive":
		var value struct {
			State worldgen.BlockState `json:"state"`
		}
		if err := json.Unmarshal(raw, &value); err != nil || value.State.Name == "" {
			p.err = fmt.Errorf("world: would_survive missing state")
			return p
		}
		switch value.State.Name {
		case "minecraft:sugar_cane", "minecraft:cactus",
			"minecraft:oak_sapling", "minecraft:spruce_sapling", "minecraft:birch_sapling",
			"minecraft:jungle_sapling", "minecraft:acacia_sapling", "minecraft:dark_oak_sapling",
			"minecraft:cherry_sapling", "minecraft:pale_oak_sapling", "minecraft:mangrove_propagule",
			"minecraft:firefly_bush":
			p.surviveStateName = value.State.Name
		default:
			p.err = fmt.Errorf("world: would_survive for %q has no survival rule modelled; the states that use it in the pack are the nine saplings, the mangrove propagule, the firefly bush, sugar cane and cactus", value.State.Name)
		}
		return p
	case "minecraft:has_sturdy_face":
		var value struct {
			Direction string `json:"direction"`
		}
		if err := json.Unmarshal(raw, &value); err != nil || value.Direction == "" {
			p.err = fmt.Errorf("world: has_sturdy_face missing direction")
			return p
		}
		p.direction = value.Direction
		return p
	case "minecraft:matching_blocks":
		names, err := decodeStringList(predicate.Blocks)
		if err != nil {
			p.err = fmt.Errorf("world: matching_blocks: %w", err)
			return p
		}
		p.blocks = names
		return p
	case "minecraft:matching_block_tag":
		if predicate.Tag == "" {
			p.err = fmt.Errorf("world: matching_block_tag missing tag")
			return p
		}
		p.tag = predicate.Tag
		return p
	case "minecraft:matching_fluids":
		fluids, err := decodeStringList(predicate.Fluids)
		if err != nil {
			p.err = fmt.Errorf("world: matching_fluids: %w", err)
			return p
		}
		for _, fluid := range fluids {
			if fluid == "minecraft:water" || fluid == "minecraft:flowing_water" {
				p.matchWater = true
			}
			if fluid == "minecraft:lava" || fluid == "minecraft:flowing_lava" {
				p.matchLava = true
			}
		}
		return p
	case "minecraft:all_of", "minecraft:any_of":
		p.children = make([]*compiledBlockPredicate, len(predicate.Predicates))
		for i, child := range predicate.Predicates {
			p.children[i] = parseBlockPredicate(child)
		}
		return p
	case "minecraft:not":
		if len(predicate.Predicate) == 0 {
			p.err = fmt.Errorf("world: not predicate missing child")
			return p
		}
		p.child = parseBlockPredicate(predicate.Predicate)
		return p
	default:
		p.err = fmt.Errorf("world: unsupported block predicate %q", predicate.Type)
		return p
	}
}

func (r *decorationRegion) testBlockPredicate(set *worldgen.FeatureSet, raw json.RawMessage, position worldgen.FeaturePosition) (bool, error) {
	return r.evalBlockPredicate(set, getCompiledBlockPredicate(raw), position)
}

func (r *decorationRegion) evalBlockPredicate(set *worldgen.FeatureSet, p *compiledBlockPredicate, position worldgen.FeaturePosition) (bool, error) {
	if p.err != nil {
		return false, p.err
	}
	position.X += p.offset[0]
	position.Y += p.offset[1]
	position.Z += p.offset[2]
	switch p.typ {
	case "minecraft:true":
		return true, nil
	case "minecraft:solid":
		// SolidPredicate = BlockState.isSolid = the precomputed legacySolid
		// flag: force-solid overrides, else the collision shape's average
		// side >= 0.729 or ysize >= 1. In the cave context that is the full
		// opaque cubes (stone/moss/clay) and NOT the small-collision plants
		// (moss_carpet fails both tests in vanilla but blocks motion).
		return isSolidState(r.getBlock(position.X, position.Y, position.Z)), nil
	case "minecraft:would_survive":
		// WouldSurvivePredicate: `state.canSurvive(level, pos)` - a question about the
		// named state, asked of a position that normally holds air. It never asks what
		// stands there, which is why the check below must not look at the cell itself.
		switch p.surviveStateName {
		case "minecraft:sugar_cane":
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
				nb := r.getBlock(nx, position.Y-1, nz)
				neighbor, ok := stateByID(nb)
				if ok && (neighbor.Name == "minecraft:frosted_ice") {
					return true, nil
				}
				if isWaterState(nb) {
					return true, nil
				}
			}
			return false, nil
		case "minecraft:cactus":
			// CactusBlock.canSurvive: no solid neighbour and no lava neighbour in any
			// horizontal direction. The block below is not examined at all, which is
			// the opposite of every other state that uses this predicate.
			for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				neighbor := r.getBlock(position.X+d[0], position.Y, position.Z+d[1])
				if isSolidState(neighbor) {
					return false, nil
				}
				if name, ok := stateByID(neighbor); ok &&
					(name.Name == "minecraft:lava" || name.Name == "minecraft:flowing_lava") {
					return false, nil
				}
			}
			return true, nil
		default:
			// VegetationBlock.canSurvive, which is mayPlaceOn of the block below, which
			// is membership of #minecraft:supports_vegetation. These ten are every tree
			// chain's placement filter: with the position-contents test that used to sit
			// in front of them, no tree in a plains or taiga chunk could ever be placed.
			return r.canVegetationSurvive(position, p.surviveStateName, set), nil
		}
	case "minecraft:has_sturdy_face":
		// HasSturdyFacePredicate: the block at pos+offset presents a sturdy
		// face in the given direction. Our full-cube approximation covers
		// every state the cave scans meet (stone/deepslate/moss/clay);
		// non-cubes (vines, lichen) present no sturdy face in vanilla.
		return fullSolidState(r.getBlock(position.X, position.Y, position.Z)), nil
	case "minecraft:inside_world_bounds":
		return position.Y >= MinY && position.Y < MinY+WorldHeight, nil
	case "minecraft:matching_blocks":
		state, ok := stateByID(r.getBlock(position.X, position.Y, position.Z))
		if !ok {
			return false, nil
		}
		for _, name := range p.blocks {
			if state.Name == name {
				return true, nil
			}
		}
		return false, nil
	case "minecraft:matching_block_tag":
		state, ok := stateByID(r.getBlock(position.X, position.Y, position.Z))
		if !ok {
			return false, nil
		}
		return flattenBlockTagContains(set, p.tag, state.Name), nil
	case "minecraft:matching_fluids":
		state := r.getBlock(position.X, position.Y, position.Z)
		if stateFlags(state)&flagFluid == 0 {
			return false, nil
		}
		if p.matchWater && isWaterState(state) {
			return true, nil
		}
		if p.matchLava && isLavaState(state) {
			return true, nil
		}
		return false, nil
	case "minecraft:all_of":
		for _, child := range p.children {
			ok, err := r.evalBlockPredicate(set, child, position)
			if err != nil || !ok {
				return ok, err
			}
		}
		return true, nil
	case "minecraft:any_of":
		for _, child := range p.children {
			ok, err := r.evalBlockPredicate(set, child, position)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	case "minecraft:not":
		ok, err := r.evalBlockPredicate(set, p.child, position)
		return !ok, err
	default:
		return false, fmt.Errorf("world: unsupported block predicate %q", p.typ)
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
	return int(state) < totalBlockStates && stateFlags(state)&flagFluid != 0 && !isLavaState(state)
}

func isLavaState(state uint16) bool {
	stateByIDOnce.Do(buildStateTable)
	return int(state) < totalBlockStates && stateSpecialTable[state]&specialLava != 0
}

// isAirState mirrors BlockState.isAir(): true for minecraft:air, cave_air,
// and void_air. Features carve with cave_air (lakes, mineshafts, monster
// rooms), so any "is this empty" check must treat all three as air exactly
// like vanilla's isAir()/isEmptyBlock - comparing against StateAir (plain
// air) silently treats carved caves as solid.
func isAirState(state uint16) bool {
	stateByIDOnce.Do(buildStateTable)
	return int(state) < totalBlockStates && stateSpecialTable[state]&specialAir != 0
}

func flattenBlockTagContains(set *worldgen.FeatureSet, tag, name string) bool {
	return getCachedBlockTag(set, tag).set[name]
}
