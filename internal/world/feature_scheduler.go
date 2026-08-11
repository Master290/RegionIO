package world

import "fmt"

// decorationSource is a source chunk whose feature pass may inspect or write a
// target chunk. Vanilla FEATURES has a one-chunk block-state write radius.
type decorationSource struct {
	X, Z int32
}

// decorationSources returns source chunks in deterministic X-major/Z-minor
// order. Replaying all nine against one mutable 3x3 terrain region makes target
// output independent of cache request order while preserving each source's own
// decoration seed and placement origin.
func decorationSources(targetX, targetZ int32) []decorationSource {
	sources := make([]decorationSource, 0, 9)
	for sourceX := targetX - 1; sourceX <= targetX+1; sourceX++ {
		for sourceZ := targetZ - 1; sourceZ <= targetZ+1; sourceZ++ {
			sources = append(sources, decorationSource{X: sourceX, Z: sourceZ})
		}
	}
	return sources
}

// replayScheduledOres replays each source center in deterministic order into a
// shared region. The region must contain the target's radius-two base terrain;
// source passes themselves may write only within their own radius-one window.
// This is an explicit canonical order for isolated generation. It must not
// replace the production path until parity evidence confirms that it matches
// vanilla's chunk-status scheduling order.
func (r *decorationRegion) replayScheduledOres(seed int64, targetX, targetZ int32) error {
	for _, source := range decorationSources(targetX, targetZ) {
		if err := r.setSource(source.X, source.Z); err != nil {
			return err
		}
		if err := r.placeScheduledOres(seed); err != nil {
			return fmt.Errorf("world: replay source (%d,%d): %w", source.X, source.Z, err)
		}
	}
	return nil
}
