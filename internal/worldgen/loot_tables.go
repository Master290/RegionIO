package worldgen

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
)

var (
	lootRegistryMu sync.Once
	lootRegistry   *LootAndSpawnerRegistry
	lootRegErr     error
)

// NumberProvider represents a vanilla LootNumberProvider (constant, uniform, or binomial).
type NumberProvider struct {
	Type string  `json:"type"`
	Val  float64 `json:"-"`
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
	N    float64 `json:"n"`
	P    float64 `json:"p"`
}

func (np *NumberProvider) UnmarshalJSON(data []byte) error {
	var f float64
	if err := json.Unmarshal(data, &f); err == nil {
		np.Type = "minecraft:constant"
		np.Val = f
		return nil
	}
	var raw struct {
		Type  string          `json:"type"`
		Value float64         `json:"value"`
		Min   json.RawMessage `json:"min"`
		Max   json.RawMessage `json:"max"`
		N     json.RawMessage `json:"n"`
		P     json.RawMessage `json:"p"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	np.Type = raw.Type
	if np.Type == "" {
		np.Type = "minecraft:uniform"
	}
	np.Val = raw.Value
	parseSub := func(rm json.RawMessage) float64 {
		if len(rm) == 0 {
			return 0
		}
		var sub NumberProvider
		if err := json.Unmarshal(rm, &sub); err == nil {
			if sub.Type == "minecraft:constant" {
				return sub.Val
			}
			return (sub.Min + sub.Max) * 0.5
		}
		return 0
	}
	np.Min = parseSub(raw.Min)
	np.Max = parseSub(raw.Max)
	np.N = parseSub(raw.N)
	np.P = parseSub(raw.P)
	return nil
}

// SampleInt evaluates the number provider as an integer using the given RandomSource.
func (np NumberProvider) SampleInt(rng RandomSource) int {
	switch strings.TrimPrefix(np.Type, "minecraft:") {
	case "", "constant":
		return int(math.Floor(np.Val + 0.5))
	case "uniform":
		minI := int(math.Floor(np.Min + 0.5))
		maxI := int(math.Floor(np.Max + 0.5))
		if minI >= maxI {
			return minI
		}
		return minI + int(rng.NextIntN(int32(maxI-minI+1)))
	case "binomial":
		n := int(math.Floor(np.N + 0.5))
		p := float32(np.P)
		out := 0
		for i := 0; i < n; i++ {
			if rng.NextFloat() < p {
				out++
			}
		}
		return out
	default:
		return int(math.Floor(np.Val + 0.5))
	}
}

// LootConditionDef represents a condition on a loot pool or entry (e.g., minecraft:random_chance).
type LootConditionDef struct {
	Condition string  `json:"condition"`
	Chance    float32 `json:"chance"`
}

func evalConditions(conds []LootConditionDef, rng RandomSource) bool {
	for _, c := range conds {
		switch strings.TrimPrefix(c.Condition, "minecraft:") {
		case "random_chance":
			if rng.NextFloat() >= c.Chance {
				return false
			}
		}
	}
	return true
}

// LootFunctionDef represents an item modifier in a loot entry or pool.
type LootFunctionDef struct {
	Function    string         `json:"function"`
	Count       NumberProvider `json:"count"`
	Add         bool           `json:"add"`
	Damage      NumberProvider `json:"damage"`
	ID          string         `json:"id"`
	Levels      NumberProvider `json:"levels"`
	Amplifier   NumberProvider `json:"amplifier"`
	Destination string         `json:"destination"`
	Decoration  string         `json:"decoration"`
}

// LootEntryDef represents one entry in a loot pool.
type LootEntryDef struct {
	Type        string             `json:"type"`
	Name        string             `json:"name"`
	Weight      int                `json:"weight"`
	Quality     int                `json:"quality"`
	Functions   []LootFunctionDef  `json:"functions"`
	Conditions  []LootConditionDef `json:"conditions"`
	NestedTable string             `json:"-"`
	InlineTable *LootTableDef      `json:"-"`
}

func (e *LootEntryDef) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type       string             `json:"type"`
		Name       string             `json:"name"`
		Weight     *int               `json:"weight"`
		Quality    int                `json:"quality"`
		Functions  []LootFunctionDef  `json:"functions"`
		Conditions []LootConditionDef `json:"conditions"`
		Value      json.RawMessage    `json:"value"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	e.Type = raw.Type
	e.Name = raw.Name
	if raw.Weight != nil {
		e.Weight = *raw.Weight
	} else {
		e.Weight = 1
	}
	e.Quality = raw.Quality
	e.Functions = raw.Functions
	e.Conditions = raw.Conditions
	if len(raw.Value) > 0 {
		var s string
		if err := json.Unmarshal(raw.Value, &s); err == nil {
			e.NestedTable = s
		} else {
			var sub LootTableDef
			if err := json.Unmarshal(raw.Value, &sub); err == nil {
				e.InlineTable = &sub
			}
		}
	}
	return nil
}

// LootPoolDef represents one pool in a LootTableDef.
type LootPoolDef struct {
	Rolls      NumberProvider     `json:"rolls"`
	BonusRolls NumberProvider     `json:"bonus_rolls"`
	Entries    []LootEntryDef     `json:"entries"`
	Conditions []LootConditionDef `json:"conditions"`
	Functions  []LootFunctionDef  `json:"functions"`
}

// LootTableDef represents a parsed vanilla 26.1.2 loot table JSON.
type LootTableDef struct {
	Name           string            `json:"-"`
	Type           string            `json:"type"`
	RandomSequence string            `json:"random_sequence"`
	Pools          []LootPoolDef     `json:"pools"`
	Functions      []LootFunctionDef `json:"functions"`
}

// LootItemStack represents a generated item stack ready for container placement.
type LootItemStack struct {
	Slot       int
	ItemID     string
	Count      int
	PotionID   string
	Enchanted  bool
	DamageFrac float32
}

// TrialSpawnerConfigDef represents a parsed vanilla 26.1.2 trial_spawner JSON config.
type TrialSpawnerConfigDef struct {
	Name                           string                    `json:"-"`
	SimultaneousMobs               float32                   `json:"simultaneous_mobs"`
	SimultaneousMobsAddedPerPlayer float32                   `json:"simultaneous_mobs_added_per_player"`
	TotalMobs                      float32                   `json:"total_mobs"`
	TotalMobsAddedPerPlayer        float32                   `json:"total_mobs_added_per_player"`
	TicksBetweenSpawn              int                       `json:"ticks_between_spawn"`
	SpawnPotentials                []TrialSpawnPotentialDef  `json:"spawn_potentials"`
	LootTablesToEject              []TrialSpawnerWeightedRef `json:"loot_tables_to_eject"`
}

// TrialSpawnPotentialDef is one weighted entity entry in a TrialSpawnerConfigDef.
type TrialSpawnPotentialDef struct {
	Weight int `json:"weight"`
	Data   struct {
		Entity struct {
			ID string `json:"id"`
		} `json:"entity"`
		Equipment struct {
			LootTable string `json:"loot_table"`
		} `json:"equipment"`
	} `json:"data"`
}

// TrialSpawnerWeightedRef is one weighted loot table reference in loot_tables_to_eject.
type TrialSpawnerWeightedRef struct {
	Data   string `json:"data"`
	Weight int    `json:"weight"`
}

// PrimaryEntityID returns the primary mob entity ID configured for this trial spawner.
func (c *TrialSpawnerConfigDef) PrimaryEntityID() string {
	if c == nil || len(c.SpawnPotentials) == 0 {
		return ""
	}
	id := c.SpawnPotentials[0].Data.Entity.ID
	if id != "" && !strings.HasPrefix(id, "minecraft:") {
		id = "minecraft:" + id
	}
	return id
}

// EquipmentLootTable returns the equipment loot table for the primary mob, if any.
func (c *TrialSpawnerConfigDef) EquipmentLootTable() string {
	if c == nil || len(c.SpawnPotentials) == 0 {
		return ""
	}
	lt := c.SpawnPotentials[0].Data.Equipment.LootTable
	if lt != "" && !strings.HasPrefix(lt, "minecraft:") {
		lt = "minecraft:" + lt
	}
	return lt
}

// LootAndSpawnerRegistry holds all parsed structural loot tables and trial spawner configs.
type LootAndSpawnerRegistry struct {
	LootTables    map[string]*LootTableDef
	TrialSpawners map[string]*TrialSpawnerConfigDef
}

// LoadLootAndSpawnerRegistry loads and caches all loot_table and trial_spawner JSON definitions
// from structure_data.zip.
func LoadLootAndSpawnerRegistry() (*LootAndSpawnerRegistry, error) {
	lootRegistryMu.Do(func() {
		m := ensureStructureZipMap()
		reg := &LootAndSpawnerRegistry{
			LootTables:    make(map[string]*LootTableDef, 96),
			TrialSpawners: make(map[string]*TrialSpawnerConfigDef, 32),
		}
		const lootPrefix = "data/minecraft/loot_table/"
		const trialPrefix = "data/minecraft/trial_spawner/"

		for name, raw := range m {
			switch {
			case strings.HasPrefix(name, lootPrefix) && strings.HasSuffix(name, ".json"):
				rel := strings.TrimSuffix(strings.TrimPrefix(name, lootPrefix), ".json")
				key := "minecraft:" + rel
				var lt LootTableDef
				if err := json.Unmarshal(raw, &lt); err != nil {
					lootRegErr = fmt.Errorf("loot_table %s: %w", key, err)
					return
				}
				lt.Name = key
				reg.LootTables[key] = &lt

			case strings.HasPrefix(name, trialPrefix) && strings.HasSuffix(name, ".json"):
				rel := strings.TrimSuffix(strings.TrimPrefix(name, trialPrefix), ".json")
				key := "minecraft:" + rel
				var cfg TrialSpawnerConfigDef
				if err := json.Unmarshal(raw, &cfg); err != nil {
					lootRegErr = fmt.Errorf("trial_spawner %s: %w", key, err)
					return
				}
				cfg.Name = key
				if len(cfg.LootTablesToEject) == 0 {
					cfg.LootTablesToEject = []TrialSpawnerWeightedRef{
						{Data: "minecraft:spawners/trial_chamber/key", Weight: 1},
						{Data: "minecraft:spawners/trial_chamber/consumables", Weight: 1},
					}
				}
				reg.TrialSpawners[key] = &cfg
			}
		}
		lootRegistry = reg
	})
	return lootRegistry, lootRegErr
}

// GetLootTable looks up a loot table by resource location (with or without "minecraft:" prefix).
func (r *LootAndSpawnerRegistry) GetLootTable(name string) *LootTableDef {
	if r == nil || name == "" {
		return nil
	}
	key := "minecraft:" + strings.TrimPrefix(name, "minecraft:")
	return r.LootTables[key]
}

// GetTrialSpawnerConfig looks up a trial spawner config by resource location.
func (r *LootAndSpawnerRegistry) GetTrialSpawnerConfig(name string) *TrialSpawnerConfigDef {
	if r == nil || name == "" {
		return nil
	}
	key := "minecraft:" + strings.TrimPrefix(name, "minecraft:")
	return r.TrialSpawners[key]
}

func applyLootFunctions(item *LootItemStack, funcs []LootFunctionDef, rng RandomSource) {
	for _, fn := range funcs {
		switch strings.TrimPrefix(fn.Function, "minecraft:") {
		case "set_count":
			c := fn.Count.SampleInt(rng)
			if fn.Add {
				item.Count += c
			} else {
				item.Count = c
			}
			if item.Count < 1 {
				item.Count = 1
			}
			if item.Count > 64 {
				item.Count = 64
			}
		case "set_potion":
			if fn.ID != "" {
				item.PotionID = fn.ID
			}
		case "enchant_randomly", "enchant_with_levels", "set_enchantments":
			_ = fn.Levels.SampleInt(rng)
			item.Enchanted = true
			if item.ItemID == "minecraft:book" {
				item.ItemID = "minecraft:enchanted_book"
			}
		case "set_damage":
			_ = fn.Damage.SampleInt(rng)
			item.DamageFrac = float32(fn.Damage.Min+fn.Damage.Max) * 0.5
		case "set_ominous_bottle_amplifier":
			_ = fn.Amplifier.SampleInt(rng)
		}
	}
}

func (r *LootAndSpawnerRegistry) evalLootTable(lt *LootTableDef, rng RandomSource, depth int) []LootItemStack {
	if lt == nil || depth > 8 {
		return nil
	}
	var items []LootItemStack
	for _, pool := range lt.Pools {
		if !evalConditions(pool.Conditions, rng) {
			continue
		}
		rolls := pool.Rolls.SampleInt(rng)
		if rolls <= 0 {
			continue
		}
		for roll := 0; roll < rolls; roll++ {
			var valid []LootEntryDef
			totalWeight := 0
			for _, e := range pool.Entries {
				if !evalConditions(e.Conditions, rng) {
					continue
				}
				w := e.Weight
				if w <= 0 {
					w = 1
				}
				valid = append(valid, e)
				totalWeight += w
			}
			if totalWeight <= 0 || len(valid) == 0 {
				continue
			}
			pick := int(rng.NextIntN(int32(totalWeight)))
			var chosen *LootEntryDef
			for i := range valid {
				w := valid[i].Weight
				if w <= 0 {
					w = 1
				}
				pick -= w
				if pick < 0 {
					chosen = &valid[i]
					break
				}
			}
			if chosen == nil {
				continue
			}
			switch strings.TrimPrefix(chosen.Type, "minecraft:") {
			case "empty":
				continue
			case "item":
				id := chosen.Name
				if id == "" {
					continue
				}
				if !strings.HasPrefix(id, "minecraft:") {
					id = "minecraft:" + id
				}
				stack := LootItemStack{
					ItemID: id,
					Count:  1,
				}
				applyLootFunctions(&stack, chosen.Functions, rng)
				applyLootFunctions(&stack, pool.Functions, rng)
				applyLootFunctions(&stack, lt.Functions, rng)
				items = append(items, stack)
			case "loot_table":
				var sub *LootTableDef
				if chosen.InlineTable != nil {
					sub = chosen.InlineTable
				} else if chosen.NestedTable != "" {
					sub = r.GetLootTable(chosen.NestedTable)
				}
				subItems := r.evalLootTable(sub, rng, depth+1)
				for i := range subItems {
					applyLootFunctions(&subItems[i], chosen.Functions, rng)
					applyLootFunctions(&subItems[i], pool.Functions, rng)
					applyLootFunctions(&subItems[i], lt.Functions, rng)
				}
				items = append(items, subItems...)
			}
		}
	}
	return items
}

// GenerateLoot evaluates a loot table deterministically for the given seed.
func (r *LootAndSpawnerRegistry) GenerateLoot(tableName string, seed int64) []LootItemStack {
	lt := r.GetLootTable(tableName)
	if lt == nil {
		return nil
	}
	if seed == 0 {
		seed = int64(MthHashString(lt.Name))
	}
	rng := NewXoroshiro(seed)
	return r.evalLootTable(lt, rng, 0)
}

// FillContainer evaluates a loot table and assigns randomized container slots in [0, containerSize).
func (r *LootAndSpawnerRegistry) FillContainer(tableName string, seed int64, containerSize int) []LootItemStack {
	lt := r.GetLootTable(tableName)
	if lt == nil || containerSize <= 0 {
		return nil
	}
	if seed == 0 {
		seed = int64(MthHashString(lt.Name))
	}
	rng := NewXoroshiro(seed)
	raw := r.evalLootTable(lt, rng, 0)
	if len(raw) == 0 {
		return nil
	}
	slots := make([]int, containerSize)
	for i := range slots {
		slots[i] = i
	}
	for i := containerSize - 1; i > 0; i-- {
		j := int(rng.NextIntN(int32(i + 1)))
		slots[i], slots[j] = slots[j], slots[i]
	}
	if len(raw) > containerSize {
		raw = raw[:containerSize]
	}
	for i := range raw {
		raw[i].Slot = slots[i]
	}
	return raw
}

func MthHashString(s string) int32 {
	var h int32
	for i := 0; i < len(s); i++ {
		h = 31*h + int32(s[i])
	}
	return h
}
