package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	f, err := os.Open("generated/reports/blocks.json")
	if err != nil {
		panic(err)
	}
	var data map[string]struct {
		States []struct {
			ID      int  `json:"id"`
			Default bool `json:"default"`
		} `json:"states"`
	}
	if err := json.NewDecoder(f).Decode(&data); err != nil {
		panic(err)
	}

	out, _ := os.Create("internal/worldgen/generated_blocks.go")
	fmt.Fprintln(out, "package worldgen")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "// defaultBlockIDs maps block names to their default state ID.")
	fmt.Fprintln(out, "var defaultBlockIDs = map[string]uint16{")

	for name, block := range data {
		for _, state := range block.States {
			if state.Default {
				fmt.Fprintf(out, "\t%q: %d,\n", name, state.ID)
			}
		}
	}
	fmt.Fprintln(out, "}")
	out.Close()
}
