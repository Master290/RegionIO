package worldgen

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"

	"regionio/internal/nbt"
)

// structure_template.go loads vanilla structure-template NBTs into palette-
// resolved block lists and transforms positions the way StructureTemplate does.
//
// The state resolver is injected because only the caller knows how names plus
// properties map onto the runtime's state IDs (surface rules know a subset,
// the full table knows everything).

// TemplateBlockInfo is one resolved template cell.
type TemplateBlockInfo struct {
	Pos   [3]int
	State uint16
	HasNBT bool
}

// LoadResolvedTemplate reads a template file from disk.
func LoadResolvedTemplate(path string, resolve func(name string, props map[string]string) (uint16, bool)) ([]TemplateBlockInfo, [3]int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, [3]int{}, err
	}
	raw, err = readMaybeGzipBytes(raw)
	if err != nil {
		return nil, [3]int{}, err
	}
	return LoadResolvedTemplateBytes(raw, resolve)
}

// EmbeddedStructureTemplate reads a template from the embedded datapack data
// by its resource-path name, e.g. "ruined_portal/portal_1".
func EmbeddedStructureTemplate(name string) ([]byte, error) {
	return dataFS.ReadFile("data/structure_template/" + name + ".nbt")
}

// LoadResolvedTemplateBytes parses template NBT already in memory.
func LoadResolvedTemplateBytes(raw []byte, resolve func(name string, props map[string]string) (uint16, bool)) ([]TemplateBlockInfo, [3]int, error) {
	raw, err := readMaybeGzipBytes(raw)
	if err != nil {
		return nil, [3]int{}, err
	}
	_, root, err := nbt.UnmarshalNamed(raw)
	if err != nil {
		return nil, [3]int{}, err
	}
	comp, ok := root.(*nbt.Compound)
	if !ok {
		return nil, [3]int{}, fmt.Errorf("template root is not a compound")
	}

	var size [3]int
	if rawTag, ok := comp.Get("size"); ok {
		if tag, isList := rawTag.(nbt.List); isList && len(tag.Elems) == 3 {
			size[0] = int(tag.Elems[0].(nbt.Int))
			size[1] = int(tag.Elems[1].(nbt.Int))
			size[2] = int(tag.Elems[2].(nbt.Int))
		}
	}

	var palette []struct {
		id    uint16
		valid bool
	}
	if rawTag, ok := comp.Get("palette"); ok {
		if tag, isList := rawTag.(nbt.List); isList {
			for _, entry := range tag.Elems {
				stateComp, ok := entry.(*nbt.Compound)
				if !ok {
					palette = append(palette, struct {
						id    uint16
						valid bool
					}{})
					continue
				}
				name := ""
				if nameTag, ok := stateComp.Get("Name"); ok {
					name = string(nameTag.(nbt.String))
				}
				props := map[string]string{}
				if propTag, ok := stateComp.Get("Properties"); ok {
					if propComp, isCompound := propTag.(*nbt.Compound); isCompound {
						for _, key := range propComp.Keys() {
							if v, ok := propComp.Get(key); ok {
								props[key] = string(v.(nbt.String))
							}
						}
					}
				}
				id, valid := resolve(name, props)
				palette = append(palette, struct {
					id    uint16
					valid bool
				}{id, valid})
			}
		}
	}

	var blocks []TemplateBlockInfo
	if rawTag, ok := comp.Get("blocks"); ok {
		if tag, isList := rawTag.(nbt.List); isList {
			for _, entry := range tag.Elems {
				blockComp, ok := entry.(*nbt.Compound)
				if !ok {
					continue
				}
				stateIdx := nbt.Int(0)
				if stateTag, ok := blockComp.Get("state"); ok {
					stateIdx = stateTag.(nbt.Int)
				}
				var pos [3]int
				if posTag, ok := blockComp.Get("pos"); ok {
					if posList, isList := posTag.(nbt.List); isList && len(posList.Elems) == 3 {
						pos[0] = int(posList.Elems[0].(nbt.Int))
						pos[1] = int(posList.Elems[1].(nbt.Int))
						pos[2] = int(posList.Elems[2].(nbt.Int))
					}
				}
				if int(stateIdx) < 0 || int(stateIdx) >= len(palette) || !palette[stateIdx].valid {
					continue
				}
				info := TemplateBlockInfo{Pos: pos, State: palette[stateIdx].id}
				if _, hasNBT := blockComp.Get("nbt"); hasNBT {
					info.HasNBT = true
				}
				blocks = append(blocks, info)
			}
		}
	}
	return blocks, size, nil
}

func readMaybeGzipBytes(b []byte) ([]byte, error) {
	if len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b {
		gr, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		defer gr.Close()
		return io.ReadAll(gr)
	}
	return b, nil
}

// TransformBlockPos mirrors StructureTemplate.transform(BlockPos, mirror,
// rotation, pivot): the mirror flips around the template origin first, then the
// rotation turns the point around the pivot's X/Z pair. Rotation ordinals are
// none/clockwise_90/clockwise_180/counterclockwise_90.
func TransformBlockPos(pos [3]int, mirror string, rotation int, pivot [3]int) [3]int {
	x, y, z := pos[0], pos[1], pos[2]
	switch mirror {
	case "left_right":
		z = -z
	case "front_back":
		x = -x
	}
	px, pz := pivot[0], pivot[2]
	switch rotation {
	case 1: // clockwise_90
		return [3]int{px - pz + z, y, px + pz - x}
	case 2: // clockwise_180
		return [3]int{px + pz - z, y, pz - px + x}
	case 3: // counterclockwise_90
		return [3]int{px + px - x, y, pz + pz - z}
	}
	return [3]int{x, y, z}
}

// MthGetSeed is Mth.getSeed(x,y,z), the positional seed every template
// processor derives its per-block random source from:
//
//	l = (long)(x*3129871) ^ (long)z*116129781 ^ y
//	l = l*l*42317861 + l*11
//	return l >> 16
func MthGetSeed(x, y, z int) int64 {
	l := int64(int32(x)*3129871) ^ int64(z)*116129781 ^ int64(int32(y))
	l = l*l*42317861 + l*11
	return l >> 16
}



