package registry

import "testing"

func TestEntityTypeIndex(t *testing.T) {
	tests := map[string]int{
		"minecraft:pig":    100,
		"minecraft:player": 155,
		"minecraft:zombie": 150,
	}
	for name, want := range tests {
		if got := EntityTypeIndex(name); got != want {
			t.Fatalf("EntityTypeIndex(%q) = %d, want %d", name, got, want)
		}
	}
	if got := EntityTypeIndex("minecraft:not_a_real_entity"); got != -1 {
		t.Fatalf("unknown entity type = %d, want -1", got)
	}
}

func TestEntityTypeIsNotSyncedRegistry(t *testing.T) {
	if got := Index("minecraft:entity_type", "minecraft:pig"); got != -1 {
		t.Fatalf("synced registry unexpectedly has entity_type pig = %d", got)
	}
}
