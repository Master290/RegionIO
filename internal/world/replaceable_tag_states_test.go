package world

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"regionio/internal/worldgen"
)

// TestMossReplaceableIsBlockScoped asserts that the tag membership the
// vegetation patch consults is scoped by block, the way vanilla's is, on the
// states a real capture actually contains.
//
// The trap is specific and was live here until it was fixed: geodeTagIDs resolved
// each tag member through nameToStateID with no properties, which yields the
// member's default state only. Vanilla's test is BlockState.is(TagKey<Block>), and
// a block tag is a set of blocks, so every state of a member matches.
// #minecraft:moss_replaceable pulls in #minecraft:cave_vines, whose age property
// spans twenty-five states - so an aged vine hanging in a lush-cave ceiling read
// as non-replaceable, and placeGround's column walk aborted on a cell vanilla walks
// through, which changes whether a patch is accepted and therefore every draw after
// it.
//
// The assertion is deliberately against an independently built block-scoped set,
// not against the function's own notion of membership: comparing the function to
// itself would pass forever once it is fixed, which is a tautology wearing a test's
// clothes. The count the default-only reading would have missed is logged as well,
// because whether the divergence is reachable in the data is the thing that
// separates a live bug from a latent one - and note it is not the same question as
// whether it changes output, which it turned out not to on this seed.
func TestMossReplaceableIsBlockScoped(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	states := readFixtureCapture(t, vanillaParityFixture)

	// Membership by block, built without asking the code under test.
	byBlock := map[uint16]string{}
	for _, name := range flattenBlockTag(set, "minecraft:moss_replaceable", nil) {
		if _, ok := nameToStateID(name, nil); !ok {
			continue
		}
		for _, id := range idsByName[name] {
			byBlock[id] = name
		}
	}
	// What the old, default-only reading would have accepted.
	byDefault := map[uint16]bool{}
	for _, name := range flattenBlockTag(set, "minecraft:moss_replaceable", nil) {
		if id, ok := nameToStateID(name, nil); ok {
			byDefault[id] = true
		}
	}

	production := geodeTagIDs(set, "#minecraft:moss_replaceable")

	var missed, absent []uint16
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
	for id := range present {
		if byBlock[id] == "" {
			continue
		}
		if !production[id] {
			missed = append(missed, id)
		}
		if !byDefault[id] {
			absent = append(absent, id)
		}
	}
	t.Logf("moss_replaceable: %d states by block, %d under the default-only reading, "+
		"%d present in the capture where the two disagree", len(byBlock), len(byDefault), len(absent))
	if len(missed) != 0 {
		sortIDs(missed)
		names := make([]string, 0, len(missed))
		for _, id := range missed {
			names = append(names, fmt.Sprintf("%d=%s", id, stateLabel(id)))
		}
		t.Errorf("geodeTagIDs rejects %d state(s) vanilla's block tag accepts: %s",
			len(missed), strings.Join(names, ", "))
	}
	if len(absent) == 0 {
		t.Skip("the capture holds no non-default member state, so this fixture cannot demonstrate the difference")
	}
}

// sortIDs keeps a failure message stable across Go's randomised map order.
func sortIDs(ids []uint16) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
}
