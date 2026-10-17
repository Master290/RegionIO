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

// JigsawBlockTag holds the NBT attributes of a minecraft:jigsaw block inside a StructureTemplate.
type JigsawBlockTag struct {
	Orientation       string
	Name              string
	Target            string
	Pool              string
	FinalState        string
	Joint             string
	SelectionPriority int
	PlacementPriority int
}

// TemplateBlockInfo is one resolved template cell.
type TemplateBlockInfo struct {
	Pos    [3]int
	State  uint16
	HasNBT bool
	// Marker is the data-marker metadata string for structure_block blocks
	// with a nested NBT compound (e.g. "chest", "drowned"), empty otherwise.
	Marker                  string
	BlockName               string
	BlockProps              map[string]string
	Jigsaw                  *JigsawBlockTag
	IsRandomizableContainer bool
	NBT                     *nbt.Compound
}

// TemplateEntityInfo is one entity entry from a StructureTemplate's "entities" list.
type TemplateEntityInfo struct {
	Pos      [3]float64
	BlockPos [3]int
	ID       string
	NBT      *nbt.Compound
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
// by its resource-path name, e.g. "ruined_portal/portal_1" or "village/plains/houses/plains_small_house_1".
func EmbeddedStructureTemplate(name string) ([]byte, error) {
	if b, ok := EmbeddedStructureTemplateZip(name); ok {
		return b, nil
	}
	return dataFS.ReadFile("data/structure_template/" + name + ".nbt")
}

type resolvedPaletteEntry struct {
	id    uint16
	valid bool
	name  string
	props map[string]string
}

func parsePaletteList(tag nbt.List, resolve func(name string, props map[string]string) (uint16, bool)) []resolvedPaletteEntry {
	var palette []resolvedPaletteEntry
	for _, entry := range tag.Elems {
		stateComp, ok := entry.(*nbt.Compound)
		if !ok {
			palette = append(palette, resolvedPaletteEntry{})
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
		palette = append(palette, resolvedPaletteEntry{
			id:    id,
			valid: valid,
			name:  name,
			props: props,
		})
	}
	return palette
}

func nbtStringOr(nc *nbt.Compound, key, def string) string {
	if v, ok := nc.Get(key); ok {
		if s, isStr := v.(nbt.String); isStr && string(s) != "" {
			return string(s)
		}
	}
	return def
}

func nbtIntOr(nc *nbt.Compound, key string, def int) int {
	if v, ok := nc.Get(key); ok {
		switch iv := v.(type) {
		case nbt.Int:
			return int(iv)
		case nbt.Short:
			return int(iv)
		case nbt.Byte:
			return int(iv)
		case nbt.Long:
			return int(iv)
		}
	}
	return def
}

func isRandomizableContainerBlock(name string) bool {
	switch name {
	case "minecraft:chest", "minecraft:trapped_chest", "minecraft:barrel",
		"minecraft:dispenser", "minecraft:dropper", "minecraft:hopper",
		"minecraft:furnace", "minecraft:blast_furnace", "minecraft:smoker",
		"minecraft:brewing_stand", "minecraft:shulker_box", "minecraft:decorated_pot",
		"minecraft:crafter":
		return true
	}
	return false
}

// LoadResolvedTemplatePalettesBytes parses a template NBT (handling both single
// "palette" and multi-palette "palettes") into one []TemplateBlockInfo per palette.
func LoadResolvedTemplatePalettesBytes(raw []byte, resolve func(name string, props map[string]string) (uint16, bool)) ([][]TemplateBlockInfo, [3]int, error) {
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

	var palettes [][]resolvedPaletteEntry
	if rawPalettes, ok := comp.Get("palettes"); ok {
		if palList, isList := rawPalettes.(nbt.List); isList {
			for _, pElem := range palList.Elems {
				if subList, ok := pElem.(nbt.List); ok {
					palettes = append(palettes, parsePaletteList(subList, resolve))
				}
			}
		}
	}
	if len(palettes) == 0 {
		if rawTag, ok := comp.Get("palette"); ok {
			if tag, isList := rawTag.(nbt.List); isList {
				palettes = append(palettes, parsePaletteList(tag, resolve))
			}
		}
	}
	if len(palettes) == 0 {
		return nil, size, nil
	}

	out := make([][]TemplateBlockInfo, len(palettes))
	if rawTag, ok := comp.Get("blocks"); ok {
		if tag, isList := rawTag.(nbt.List); isList {
			for pIdx, palette := range palettes {
				var blocks []TemplateBlockInfo
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
					pe := palette[stateIdx]
					info := TemplateBlockInfo{
						Pos:        pos,
						State:      pe.id,
						BlockName:  pe.name,
						BlockProps: pe.props,
					}
					if nbtTag, hasNBT := blockComp.Get("nbt"); hasNBT {
						info.HasNBT = true
						info.IsRandomizableContainer = isRandomizableContainerBlock(pe.name)
						if nc, isCompound := nbtTag.(*nbt.Compound); isCompound {
							info.NBT = nc
							if mt, ok := nc.Get("metadata"); ok {
								info.Marker = string(mt.(nbt.String))
							}
							if pe.name == "minecraft:jigsaw" {
								orient := pe.props["orientation"]
								if orient == "" {
									orient = "north_up"
								}
								joint := nbtStringOr(nc, "joint", "")
								if joint != "rollable" && joint != "aligned" {
									if orient == "up_north" || orient == "up_east" || orient == "up_south" || orient == "up_west" ||
										orient == "down_north" || orient == "down_east" || orient == "down_south" || orient == "down_west" {
										joint = "rollable"
									} else {
										joint = "aligned"
									}
								}
								info.Jigsaw = &JigsawBlockTag{
									Orientation:       orient,
									Name:              nbtStringOr(nc, "name", "minecraft:empty"),
									Target:            nbtStringOr(nc, "target", "minecraft:empty"),
									Pool:              nbtStringOr(nc, "pool", "minecraft:empty"),
									FinalState:        nbtStringOr(nc, "final_state", "minecraft:air"),
									Joint:             joint,
									SelectionPriority: nbtIntOr(nc, "selection_priority", 0),
									PlacementPriority: nbtIntOr(nc, "placement_priority", 0),
								}
							}
						}
					}
					blocks = append(blocks, info)
				}
				out[pIdx] = blocks
			}
		}
	}
	return out, size, nil
}

// LoadResolvedTemplateEntitiesBytes parses the "entities" list of a StructureTemplate NBT.
func LoadResolvedTemplateEntitiesBytes(raw []byte) ([]TemplateEntityInfo, error) {
	raw, err := readMaybeGzipBytes(raw)
	if err != nil {
		return nil, err
	}
	_, root, err := nbt.UnmarshalNamed(raw)
	if err != nil {
		return nil, err
	}
	comp, ok := root.(*nbt.Compound)
	if !ok {
		return nil, fmt.Errorf("template root is not a compound")
	}
	rawTag, ok := comp.Get("entities")
	if !ok {
		return nil, nil
	}
	tag, isList := rawTag.(nbt.List)
	if !isList || len(tag.Elems) == 0 {
		return nil, nil
	}
	out := make([]TemplateEntityInfo, 0, len(tag.Elems))
	for _, entry := range tag.Elems {
		ec, ok := entry.(*nbt.Compound)
		if !ok {
			continue
		}
		var info TemplateEntityInfo
		if posTag, ok := ec.Get("pos"); ok {
			if posList, isList := posTag.(nbt.List); isList && len(posList.Elems) == 3 {
				for i := 0; i < 3; i++ {
					switch v := posList.Elems[i].(type) {
					case nbt.Double:
						info.Pos[i] = float64(v)
					case nbt.Float:
						info.Pos[i] = float64(v)
					}
				}
			}
		}
		if bpTag, ok := ec.Get("blockPos"); ok {
			if bpList, isList := bpTag.(nbt.List); isList && len(bpList.Elems) == 3 {
				for i := 0; i < 3; i++ {
					if iv, ok := bpList.Elems[i].(nbt.Int); ok {
						info.BlockPos[i] = int(iv)
					}
				}
			}
		}
		if nbtTag, ok := ec.Get("nbt"); ok {
			if nc, isCompound := nbtTag.(*nbt.Compound); isCompound {
				info.NBT = nc
				info.ID = nbtStringOr(nc, "id", "")
			}
		}
		if info.ID != "" {
			out = append(out, info)
		}
	}
	return out, nil
}

// LoadResolvedTemplateBytes parses template NBT already in memory.
func LoadResolvedTemplateBytes(raw []byte, resolve func(name string, props map[string]string) (uint16, bool)) ([]TemplateBlockInfo, [3]int, error) {
	palettes, size, err := LoadResolvedTemplatePalettesBytes(raw, resolve)
	if err != nil {
		return nil, [3]int{}, err
	}
	if len(palettes) == 0 {
		return nil, size, nil
	}
	return palettes[0], size, nil
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
	case 1: // clockwise_90 ($SwitchMap ordinal 1 -> bytecode target 172)
		return [3]int{px + pz - z, y, pz - px + x}
	case 2: // clockwise_180 (target 120)
		return [3]int{px + px - x, y, pz + pz - z}
	case 3: // counterclockwise_90 (target 146)
		return [3]int{px - pz + z, y, px + pz - x}
	}
	return [3]int{x, y, z}
}

// TransformEntityPos mirrors StructureTemplate.transform(Vec3, mirror, rotation, pivot).
func TransformEntityPos(pos [3]float64, mirror string, rotation int, pivot [3]int) [3]float64 {
	x, y, z := pos[0], pos[1], pos[2]
	switch mirror {
	case "left_right":
		z = 1.0 - z
	case "front_back":
		x = 1.0 - x
	}
	px, pz := float64(pivot[0]), float64(pivot[2])
	switch rotation {
	case 1: // clockwise_90
		return [3]float64{px + pz + 1.0 - z, y, pz - px + x}
	case 2: // clockwise_180
		return [3]float64{px + px + 1.0 - x, y, pz + pz + 1.0 - z}
	case 3: // counterclockwise_90
		return [3]float64{px - pz + z, y, px + pz + 1.0 - x}
	}
	return [3]float64{x, y, z}
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



