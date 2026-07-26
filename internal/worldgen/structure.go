package worldgen

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"

	"regionio/internal/nbt"
)

type ChunkWriter interface {
	SetBlock(lx, y, lz int, state uint16)
	GetBlock(lx, y, lz int) uint16
}

// Template represents a loaded NBT structure.
type Template struct {
	Size    [3]int
	Blocks  []TemplateBlock
	Palette []uint16 // mapped to global block IDs
}

type TemplateBlock struct {
	Pos   [3]int
	State uint16
}

// LoadTemplate reads a vanilla structure NBT file.
func LoadTemplate(path string) (*Template, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// NBT files are usually gzipped.
	var r io.Reader = bytes.NewReader(b)
	if len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b {
		gr, err := gzip.NewReader(r)
		if err != nil {
			return nil, err
		}
		defer gr.Close()
		r = gr
	}

	out, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	_, root, err := nbt.UnmarshalNamed(out)
	if err != nil {
		return nil, err
	}

	comp, ok := root.(*nbt.Compound)
	if !ok {
		return nil, fmt.Errorf("root is not a compound")
	}

	tmpl := &Template{}

	if tag, ok := comp.Get("size"); ok {
		if sizeList, ok := tag.(nbt.List); ok && len(sizeList.Elems) == 3 {
			tmpl.Size[0] = int(sizeList.Elems[0].(nbt.Int))
			tmpl.Size[1] = int(sizeList.Elems[1].(nbt.Int))
			tmpl.Size[2] = int(sizeList.Elems[2].(nbt.Int))
		}
	}

	// Parse palette
	if tag, ok := comp.Get("palette"); ok {
		if paletteList, ok := tag.(nbt.List); ok {
			for _, v := range paletteList.Elems {
				stateComp, ok := v.(*nbt.Compound)
				if !ok {
					continue
				}
				nameTag, _ := stateComp.Get("Name")
				name := string(nameTag.(nbt.String))
				props := make(map[string]string)

				if propTag, ok := stateComp.Get("Properties"); ok {
					if propComp, ok := propTag.(*nbt.Compound); ok {
						for _, k := range propComp.Keys() {
							pval, _ := propComp.Get(k)
							if str, ok := pval.(nbt.String); ok {
								props[k] = string(str)
							}
						}
					}
				}
				id, ok := surfaceBlockID(name, props)
				if !ok {
					// Structure templates name far more blocks than the
					// surface rules do; fall back to the broader
					// default-state table.
					id = defaultBlockIDs[name]
				}
				tmpl.Palette = append(tmpl.Palette, id)
			}
		}
	}

	// Parse blocks
	if tag, ok := comp.Get("blocks"); ok {
		if blocksList, ok := tag.(nbt.List); ok {
			for _, v := range blocksList.Elems {
				blockComp, ok := v.(*nbt.Compound)
				if !ok {
					continue
				}
				stateTag, _ := blockComp.Get("state")
				stateIdx := int(stateTag.(nbt.Int))

				var pos [3]int
				if posTag, ok := blockComp.Get("pos"); ok {
					if posList, ok := posTag.(nbt.List); ok && len(posList.Elems) == 3 {
						pos[0] = int(posList.Elems[0].(nbt.Int))
						pos[1] = int(posList.Elems[1].(nbt.Int))
						pos[2] = int(posList.Elems[2].(nbt.Int))
					}
				}

				if stateIdx >= 0 && stateIdx < len(tmpl.Palette) {
					state := tmpl.Palette[stateIdx]
					tmpl.Blocks = append(tmpl.Blocks, TemplateBlock{
						Pos:   pos,
						State: state,
					})
				}
			}
		}
	}

	return tmpl, nil
}

// Place applies the template blocks to the ChunkWriter if they fall within the given chunk boundaries.
// cx, cz are the chunk coordinates we are currently generating.
// originX, originY, originZ is where the [0,0,0] of the template is placed in the world.
func (t *Template) Place(cw ChunkWriter, cx, cz int32, originX, originY, originZ int) {
	minX := int(cx) * 16
	minZ := int(cz) * 16
	maxX := minX + 15
	maxZ := minZ + 15

	for _, b := range t.Blocks {
		wx := originX + b.Pos[0]
		wy := originY + b.Pos[1]
		wz := originZ + b.Pos[2]

		if wx >= minX && wx <= maxX && wz >= minZ && wz <= maxZ {
			lx := wx - minX
			lz := wz - minZ
			// 14851 = minecraft:structure_void
			// 21736 = minecraft:jigsaw
			if b.State == 14851 || b.State == 21736 {
				continue
			}
			cw.SetBlock(lx, wy, lz, b.State)
		}
	}
}
