package world

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"regionio/internal/worldgen"
)

// TestTagStateIDsAreBlockScoped asserts that tag membership, as the generator
// resolves it, is scoped by block for every tag it consults - not only the one
// where the bug was found.
//
// The trap was specific and was live: tagStateIDs used to resolve each member
// through nameToStateID with no properties, which yields the member's *default
// state*. Vanilla's test is MatchingBlockTagPredicate.test ->
// BlockState.is(TagKey<Block>), and a block tag is a set of blocks, so every state
// of a member satisfies it. The two readings differ for any member that has a
// property, and #minecraft:moss_replaceable pulls in #minecraft:cave_vines, whose
// age property spans twenty-five states. So an aged vine in a lush-cave ceiling read
// as non-replaceable, and placeGround's column walk breaks on the first
// non-replaceable cell - it aborted a vegetation column that vanilla walks straight
// through, changing whether the patch is accepted and every draw after it.
//
// The same helper feeds lakes and ore targets too, so this walks every tag any
// configured feature references plus the three production names that appear in code
// rather than in a config. Membership is computed against a block-scoped set built
// independently of the function under test: asking that function what it believes
// would pass forever once it is fixed, which is a tautology wearing a test's
// clothes.
//
// The measured effect on this fixture was zero cells - reachable in the data is not
// the same question as reachable in the code path - which is precisely why a check
// like this is worth keeping. It is the difference between a bug that is silent and
// a bug that is gone.
func TestTagStateIDsAreBlockScoped(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	states := readFixtureCapture(t, vanillaParityFixture)

	present := map[uint16]bool{}
	for _, chunk := range states.chunks {
		for y := MinY; y < MinY+WorldHeight; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					present[chunk.at(x, y, z)] = true
				}
			}
		}
	}

	tags := configuredFeatureTags(set)
	// Named in production code rather than in a config, so the scan above cannot
	// find them: the vegetation patch's replaceable test, and the two lake tables.
	tags = append(tags,
		"minecraft:moss_replaceable",
		"minecraft:features_cannot_replace",
		"minecraft:lava_pool_stone_cannot_replace")

	var checked, disagreed, rejectedTotal int
	var failures []string
	for _, tag := range dedupeTags(tags) {
		byBlock, byDefault := tagMembership(t, set, tag)
		if len(byBlock) == 0 {
			continue // a tag the datapack does not define; nothing to assert
		}
		production := tagStateIDs(set, "#"+tag)
		checked++
		var rejected, nonDefault []uint16
		for id := range present {
			if !byBlock[id] {
				continue
			}
			if !production[id] {
				rejected = append(rejected, id)
			}
			if !byDefault[id] {
				nonDefault = append(nonDefault, id)
			}
		}
		disagreed += len(nonDefault)
		if len(rejected) != 0 {
			sortIDs(rejected)
			rejectedTotal += len(rejected)
			failures = append(failures, fmt.Sprintf("%s: tagStateIDs rejects %d state(s) the block tag accepts: %s",
				tag, len(rejected), describeStates(rejected)))
		}
	}
	t.Logf("%d tags checked against %d distinct states in the capture: %d of them are "+
		"non-default states of a member, which the old default-only reading excluded, "+
		"and %d are still rejected by tagStateIDs", checked, len(present), disagreed, rejectedTotal)
	for _, line := range failures {
		t.Error(line)
	}
	if disagreed == 0 {
		t.Skip("no tag member with a non-default state appears in this capture, so the fixture " +
			"cannot demonstrate the difference; the assertions above still hold")
	}
}

// configuredFeatureTags returns every block tag referenced by any configured
// feature, read from the raw config JSON. Only a name behind a '#' counts, so
// block names inside state providers do not widen the set into nonsense.
func configuredFeatureTags(set *worldgen.FeatureSet) []string {
	tagPattern := regexp.MustCompile(`#(minecraft:[a-z0-9_./]+)`)
	var tags []string
	for _, configured := range set.Configured {
		for _, match := range tagPattern.FindAllStringSubmatch(string(configured.Config), -1) {
			tags = append(tags, match[1])
		}
	}
	return tags
}

// dedupeTags sorts and removes repeats, so the tag list is stable across runs.
func dedupeTags(tags []string) []string {
	sort.Strings(tags)
	var out []string
	for i, tag := range tags {
		if i > 0 && tag == tags[i-1] {
			continue
		}
		out = append(out, tag)
	}
	return out
}

// tagMembership builds both readings without consulting the code under test: a
// state is a member if its block is in the tag, and separately if it is that
// member's default state.
func tagMembership(t *testing.T, set *worldgen.FeatureSet, tag string) (byBlock, byDefault map[uint16]bool) {
	t.Helper()
	byBlock, byDefault = map[uint16]bool{}, map[uint16]bool{}
	for _, name := range flattenBlockTag(set, tag, nil) {
		defaultID, ok := nameToStateID(name, nil)
		if !ok {
			continue
		}
		byDefault[defaultID] = true
		for _, id := range idsByName[name] {
			byBlock[id] = true
		}
	}
	return byBlock, byDefault
}

// describeStates names each state, because a bare ID in a failure message sends the
// reader to a lookup table for no reason.
func describeStates(ids []uint16) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("%d=%s", id, stateLabel(id)))
	}
	return strings.Join(parts, ", ")
}

// sortIDs keeps a failure message stable across Go's randomised map order.
func sortIDs(ids []uint16) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
}
