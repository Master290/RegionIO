package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	f, err := os.Open("../../generated/reports/blocks.json")
	if err != nil {
		panic(err)
	}
	var data map[string]struct {
		States []struct {
			ID int `json:"id"`
		} `json:"states"`
	}
	if err := json.NewDecoder(f).Decode(&data); err != nil {
		panic(err)
	}

	opacity := make([]byte, 30000)
	emission := make([]byte, 30000)

	for name, block := range data {
		op := byte(15)
		em := byte(0)

		if strings.Contains(name, "air") || strings.Contains(name, "glass") || strings.Contains(name, "leaves") || strings.Contains(name, "sapling") || strings.Contains(name, "flower") || strings.Contains(name, "grass") || strings.Contains(name, "fern") || strings.Contains(name, "torch") || strings.Contains(name, "lantern") || strings.Contains(name, "button") || strings.Contains(name, "pressure_plate") || strings.Contains(name, "rail") || strings.Contains(name, "ladder") || strings.Contains(name, "vine") || strings.Contains(name, "door") || strings.Contains(name, "trapdoor") || strings.Contains(name, "fence") || strings.Contains(name, "wall") || strings.Contains(name, "slab") || strings.Contains(name, "water") || strings.Contains(name, "ice") || strings.Contains(name, "snow") || strings.Contains(name, "carpet") || strings.Contains(name, "sign") || strings.Contains(name, "banner") || strings.Contains(name, "bed") || strings.Contains(name, "chest") || strings.Contains(name, "bell") || strings.Contains(name, "campfire") || strings.Contains(name, "chain") || strings.Contains(name, "coral") || strings.Contains(name, "end_rod") || strings.Contains(name, "glowstone") || strings.Contains(name, "lava") || strings.Contains(name, "magma") || strings.Contains(name, "sea_lantern") || strings.Contains(name, "shroomlight") || strings.Contains(name, "slime") || strings.Contains(name, "honey") {
			op = 0
		}
        if strings.Contains(name, "water") || strings.Contains(name, "ice") {
            op = 1
        }
        if strings.Contains(name, "leaves") {
            op = 1
        }
		if name == "minecraft:glowstone" || name == "minecraft:sea_lantern" || name == "minecraft:shroomlight" || name == "minecraft:beacon" || name == "minecraft:end_rod" || strings.Contains(name, "lava") {
			em = 15
		}
		if strings.Contains(name, "torch") || strings.Contains(name, "campfire") || strings.Contains(name, "lantern") || strings.Contains(name, "fire") {
			em = 14
		}

		for _, state := range block.States {
			if state.ID < len(opacity) {
				opacity[state.ID] = op
				emission[state.ID] = em
			}
		}
	}

	out, _ := os.Create("../../internal/world/light_data.go")
	fmt.Fprintln(out, "package world")
	fmt.Fprintln(out, "var blockOpacity = [29873]byte{")
	for i := 0; i < 29873; i++ {
		fmt.Fprintf(out, "%d,", opacity[i])
	}
	fmt.Fprintln(out, "}")
	fmt.Fprintln(out, "var blockEmission = [29873]byte{")
	for i := 0; i < 29873; i++ {
		fmt.Fprintf(out, "%d,", emission[i])
	}
	fmt.Fprintln(out, "}")
	out.Close()
}
