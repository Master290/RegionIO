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
// the existing block is the answer - but a caller that asks the optional question,
// as placeBelowTrunkBlock and AlterGroundDecorator do, gets "write nothing".
func (r *decorationRegion) sampleStateProvider(set *worldgen.FeatureSet, spec worldgen.StateProviderSpec, random worldgen.RandomSource, position worldgen.FeaturePosition) (uint16, error) {
	state, ok, err := r.sampleOptionalStateProvider(set, spec, random, position)
	if err != nil {
		return 0, err
	}
	if !ok {
		return r.getBlock(position.X, position.Y, position.Z), nil
	}
	return state, nil
}

// sampleOptionalStateProvider is BlockStateProvider.getOptionalState: null only
// from a rule_based provider whose rules all failed and which declares no
// fallback. Every other provider answers through the default method, which just
// calls getState, so a randomised or weighted provider can never answer "nothing".
func (r *decorationRegion) sampleOptionalStateProvider(set *worldgen.FeatureSet, spec worldgen.StateProviderSpec, random worldgen.RandomSource, position worldgen.FeaturePosition) (uint16, bool, error) {
	switch spec.Type {
	case "rule_based":
		for _, rule := range spec.Rules {
			matched, err := r.testBlockPredicate(set, rule.Predicate, position)
			if err != nil {
				return 0, false, err
			}
			if !matched {
				continue
			}
			then, err := set.StateProvider(rule.Then)
			if err != nil {
				return 0, false, err
			}
			// A matched rule answers with its provider's getState, not its
			// getOptionalState, so a nested rule_based with no matching rule puts
			// the standing block back rather than declining.
			state, err := r.sampleStateProvider(set, then, random, position)
			return state, err == nil, err
		}
		if spec.Fallback != nil {
			state, err := r.sampleStateProvider(set, *spec.Fallback, random, position)
			return state, err == nil, err
		}
		return 0, false, nil
	default:
		state, ok := spec.SampleState(random)
		if !ok {
			return 0, false, fmt.Errorf("world: state provider %q produced no state", spec.Type)
		}
		id, ok := nameToStateID(state.Name, state.Properties)
		if !ok {
			return 0, false, fmt.Errorf("world: state provider names unknown block %q", state.Name)
		}
		return id, true, nil
	}
}
