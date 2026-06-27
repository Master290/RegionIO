package worldgen

import "sync"

var (
	templates     = make(map[string]*Template)
	templatesOnce sync.Once
)

// GetTemplate loads and caches an NBT template from disk.
func GetTemplate(name string) *Template {
	templatesOnce.Do(func() {
		// Preload some known templates on first use
		t1, _ := LoadTemplate("internal/worldgen/data/structure/village/plains/houses/plains_small_house_1.nbt")
		templates["plains_small_house_1"] = t1
	})
	return templates[name]
}
