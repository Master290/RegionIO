package world

import (
	"fmt"

	"regionio/internal/worldgen"
)

// sampleStateProvider resolves a block-state provider against this region.
//
// It has to live here rather than in worldgen because the rule_based provider -
// which is what 38 of the 39 tree configs use for below_trunk_provider - asks a
// block predicate about the block standing at a position, and only this package
// holds a world to ask.
//
// The empty-fallback case is the one worth reading twice. RuleBasedStateProvider
// .getState calls getOptionalState and, when that returns null, hands back
// level.getBlockState(pos): if no rule matched and the JSON declared no default,
// the existing block is the answer. Returning false here instead would let a
// caller skip the write, which looks the same and is not - the difference shows up
// the moment a caller treats "no state" as "do not place", while vanilla places the
// block that was already there and counts it as a placed cell.
func (r *decorationRegion) sampleStateProvider(set *worldgen.FeatureSet, spec worldgen.StateProviderSpec, random worldgen.RandomSource, position worldgen.FeaturePosition) (uint16, error) {
	switch spec.Type {
	case "rule_based":
		for _, rule := range spec.Rules {
			matched, err := r.testBlockPredicate(set, rule.Predicate, position)
			if err != nil {
				return 0, err
			}
			if !matched {
				continue
			}
			then, err := set.StateProvider(rule.Then)
			if err != nil {
				return 0, err
			}
			return r.sampleStateProvider(set, then, random, position)
		}
		if spec.Fallback != nil {
			return r.sampleStateProvider(set, *spec.Fallback, random, position)
		}
		return r.getBlock(position.X, position.Y, position.Z), nil
	default:
		state, ok := spec.SampleState(random)
		if !ok {
			return 0, fmt.Errorf("world: state provider %q produced no state", spec.Type)
		}
		id, ok := nameToStateID(state.Name, state.Properties)
		if !ok {
			return 0, fmt.Errorf("world: state provider names unknown block %q", state.Name)
		}
		return id, nil
	}
}
