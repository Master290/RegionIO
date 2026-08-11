package world

import (
	"reflect"
	"testing"
)

func TestDecorationSourcesCoverVanillaFeatureWriteRadius(t *testing.T) {
	got := decorationSources(4, -7)
	want := []decorationSource{
		{3, -8}, {3, -7}, {3, -6},
		{4, -8}, {4, -7}, {4, -6},
		{5, -8}, {5, -7}, {5, -6},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}
}

func TestDecorationSourcesAreRequestOrderIndependent(t *testing.T) {
	first := decorationSources(-2, 9)
	_ = decorationSources(100, -100)
	second := decorationSources(-2, 9)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("sources changed after unrelated request: %v != %v", first, second)
	}
}
