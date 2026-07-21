// Command vanilla_light_fixture extracts a compact block-light parity fixture
// from a vanilla world containing glowstone at (15,100,8).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"regionio/internal/nbt"
	"regionio/internal/world"
)

const (
	minX, minY, minZ    = 0, 85, -7
	sizeX, sizeY, sizeZ = 31, 31, 31
)

type lightChunk struct {
	sections map[int][]byte
}

func main() {
	worldDir := flag.String("world", "", "vanilla overworld directory")
	output := flag.String("output", "", "fixture output path")
	flag.Parse()
	if *worldDir == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "usage: go run ./tools/vanilla_light_fixture.go -world <dir> -output <file>")
		os.Exit(2)
	}

	chunks := make(map[[2]int]*lightChunk)
	data := make([]byte, 0, sizeX*sizeY*sizeZ)
	for y := minY; y < minY+sizeY; y++ {
		for z := minZ; z < minZ+sizeZ; z++ {
			for x := minX; x < minX+sizeX; x++ {
				key := [2]int{x >> 4, z >> 4}
				chunk := chunks[key]
				if chunk == nil {
					var err error
					chunk, err = readLightChunk(*worldDir, key[0], key[1])
					if err != nil {
						panic(err)
					}
					chunks[key] = chunk
				}
				section := chunk.sections[y>>4]
				if section == nil {
					data = append(data, 0)
					continue
				}
				idx := (y&15)<<8 | (z&15)<<4 | (x & 15)
				value := section[idx>>1]
				if idx&1 == 0 {
					data = append(data, value&0x0f)
				} else {
					data = append(data, value>>4)
				}
			}
		}
	}
	if err := os.WriteFile(*output, data, 0o644); err != nil {
		panic(err)
	}
}

func readLightChunk(worldDir string, cx, cz int) (*lightChunk, error) {
	rx, rz := floorDiv(cx, 32), floorDiv(cz, 32)
	regionDir := filepath.Join(worldDir, "dimensions", "minecraft", "overworld", "region")
	if _, err := os.Stat(regionDir); err != nil {
		regionDir = filepath.Join(worldDir, "region")
	}
	region, err := world.OpenRegion(regionDir, rx, rz)
	if err != nil {
		return nil, err
	}
	defer region.Close()
	raw, err := region.ReadChunk(cx-rx*32, cz-rz*32)
	if err != nil {
		return nil, fmt.Errorf("chunk (%d,%d): %w", cx, cz, err)
	}
	_, tag, err := nbt.UnmarshalNamed(raw)
	if err != nil {
		return nil, err
	}
	root, ok := tag.(*nbt.Compound)
	if !ok {
		return nil, fmt.Errorf("chunk (%d,%d): root is not a compound", cx, cz)
	}
	if level, ok := root.Get("Level"); ok {
		root, _ = level.(*nbt.Compound)
	}
	sectionsTag, ok := root.Get("sections")
	if !ok {
		return nil, fmt.Errorf("chunk (%d,%d): missing sections", cx, cz)
	}
	sections, ok := sectionsTag.(nbt.List)
	if !ok {
		return nil, fmt.Errorf("chunk (%d,%d): sections is not a list", cx, cz)
	}
	out := &lightChunk{sections: make(map[int][]byte)}
	for _, sectionTag := range sections.Elems {
		section, ok := sectionTag.(*nbt.Compound)
		if !ok {
			continue
		}
		yTag, ok := section.Get("Y")
		if !ok {
			continue
		}
		y, ok := integerTag(yTag)
		if !ok {
			continue
		}
		lightTag, ok := section.Get("BlockLight")
		if !ok {
			continue
		}
		light, ok := lightTag.(nbt.ByteArray)
		if !ok || len(light) != 2048 {
			return nil, fmt.Errorf("chunk (%d,%d) section %d: invalid BlockLight", cx, cz, y)
		}
		out.sections[y] = append([]byte(nil), light...)
	}
	return out, nil
}

func integerTag(tag nbt.Tag) (int, bool) {
	switch value := tag.(type) {
	case nbt.Byte:
		return int(value), true
	case nbt.Short:
		return int(value), true
	case nbt.Int:
		return int(value), true
	default:
		return 0, false
	}
}

func floorDiv(value, divisor int) int {
	quotient := value / divisor
	if value < 0 && value%divisor != 0 {
		quotient--
	}
	return quotient
}
