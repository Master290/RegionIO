package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	f, _ := os.Open("generated/reports/blocks.json")
	var data map[string]interface{}
	json.NewDecoder(f).Decode(&data)

	glowstone := data["minecraft:glowstone"]
	b, _ := json.MarshalIndent(glowstone, "", "  ")
	fmt.Println("glowstone:", string(b))
}
