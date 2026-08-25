package worldgen

import (
	_ "embed"
	"encoding/json"
)

// features_cannot_replace.json is #minecraft:features_cannot_replace, captured
// verbatim from the 26.1.2 server jar. Features guard their writes through
// Feature.safeSetBlock with this tag: an existing state in the tag stays, an
// existing state outside it may be replaced.
//
//go:embed data/tag/features_cannot_replace.json
var featuresCannotReplaceJSON []byte

// FeaturesCannotReplace returns the block names in
// #minecraft:features_cannot_replace.
func FeaturesCannotReplace() ([]string, error) {
	var doc struct {
		Values []string `json:"values"`
	}
	if err := json.Unmarshal(featuresCannotReplaceJSON, &doc); err != nil {
		return nil, err
	}
	return doc.Values, nil
}
