package main

import (
	"fmt"
	"regionio/internal/worldgen"
)

func main() {
	tmpl, _ := worldgen.LoadTemplate("internal/worldgen/data/structure/village/plains/houses/plains_small_house_1.nbt")
	for i, b := range tmpl.Palette {
		fmt.Printf("Palette[%d] = %d\n", i, b)
	}
}
