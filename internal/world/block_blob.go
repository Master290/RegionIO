package world

import (
	"fmt"

	"regionio/internal/worldgen"
)

// placeBlockBlob is BlockBlobFeature.place: find ground the blob may sit on, then draw
// three boxes around that spot, each one moving the centre by a step in every axis.
//
// The draw count is what makes this worth transcribing rather than approximating: six
// nextInt(2) per row - three that size the box and three that move the centre afterwards,
// and the move happens on the last row too - so eighteen draws per boulder, before any
// predicate is consulted. A loop that skipped the final move, or sampled the size from
// the world instead of the generator, would shift every later feature in the chunk.
func (r *decorationRegion) placeBlockBlob(set *worldgen.FeatureSet, random worldgen.RandomSource, position worldgen.FeaturePosition, config worldgen.BlockBlobFeatureConfig) error {
	state, ok := nameToStateID(config.State.Name, config.State.Properties)
	if !ok {
		return fmt.Errorf("world: block blob names unknown block %q", config.State.Name)
	}
	x, y, z := position.X, position.Y, position.Z
	// The descent is bounded by the world floor rather than by a step count, so a
	// blob spawned over a deep cave system can fall a long way before it answers the
	// predicate or gives up.
	for y > MinY+3 {
		matched, err := r.testBlockPredicate(set, config.CanPlaceOn, worldgen.FeaturePosition{X: x, Y: y - 1, Z: z})
		if err != nil {
			return err
		}
		if matched {
			break
		}
		y--
	}
	if y <= MinY+3 {
		return nil
	}
	for row := 0; row < 3; row++ {
		a := int(random.NextIntN(2))
		b := int(random.NextIntN(2))
		c := int(random.NextIntN(2))
		// (a + b + c) * 0.333f + 0.5f, in float32, squared in float32, then widened:
		// the rounding is part of which cells fall inside the blob.
		reach := float32(a+b+c)*0.333 + 0.5
		limit := float64(reach * reach)
		for dx := -a; dx <= a; dx++ {
			for dy := -b; dy <= b; dy++ {
				for dz := -c; dz <= c; dz++ {
					distance := float64(dx*dx+dy*dy+dz*dz)
					if distance > limit {
						continue
					}
					r.setBlock(x+dx, y+dy, z+dz, state)
				}
			}
		}
		x += -1 + int(random.NextIntN(2))
		y += -1 + int(random.NextIntN(2))
		z += -1 + int(random.NextIntN(2))
	}
	return nil
}
