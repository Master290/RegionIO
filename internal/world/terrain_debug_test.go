package world

import (
	"fmt"
	"regionio/internal/worldgen"
	"testing"
)

func TestTerrainHeightProfile(t *testing.T) {
	d := worldgen.SimpleTerrain(0)
	minH, maxH := 1000, -1000
	// surface height along z=8 for x in [-32,32]
	line := ""
	for x := -32; x <= 32; x += 4 {
		top := MinY - 1
		for y := MinY; y < MinY+WorldHeight; y++ {
			if d.Compute(worldgen.FunctionContext{X: float64(x), Y: float64(y), Z: 8}) > 0 {
				top = y
			}
		}
		line += fmt.Sprintf("%d ", top)
		if top < minH {
			minH = top
		}
		if top > maxH {
			maxH = top
		}
	}
	t.Logf("surface heights (z=8): %s", line)
	t.Logf("min=%d max=%d range=%d", minH, maxH, maxH-minH)
	if minH < MinY || maxH > 120 {
		t.Fatalf("implausible terrain heights")
	}
}
