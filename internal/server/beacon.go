package server

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Protocol 775 (Minecraft 26.1.2) beacon container, packet, data property,
// sound, and gameplay constants.
const (
	// MenuTypeBeacon is BuiltInRegistries.MENU index 9 ("minecraft:beacon").
	MenuTypeBeacon int32 = 9

	// PlaySetBeaconSB is ServerboundSetBeaconPacket (0x34 in protocol 775).
	PlaySetBeaconSB int32 = 0x34

	// BlockEntityTypeBeacon is BuiltInRegistries.BLOCK_ENTITY_TYPE index 14 ("minecraft:beacon").
	BlockEntityTypeBeacon int32 = 14

	// BeaconSlotPayment is slot 0 in BeaconMenu (max stack size 1).
	BeaconSlotPayment     = 0
	BeaconSlotCount       = 1
	BeaconMenuSlotCount   = 37 // 1 payment slot + 27 main inventory + 9 hotbar
	BeaconMaxLevel        = 4
	BeaconTickInterval    = 80  // server ticks between pyramid/sky checks and effect pulses (4s)
	BeaconDefaultMaxBuild = 319 // default Overworld top Y for sky check

	// BeaconMenu ContainerSetData property indices (ClientboundContainerSetDataPacket 0x13).
	BeaconDataLevels          int16 = 0
	BeaconDataPrimaryEffect   int16 = 1
	BeaconDataSecondaryEffect int16 = 2

	// Vanilla 26.1.2 beacon sound event identifiers.
	SoundBeaconActivate    = "minecraft:block.beacon.activate"
	SoundBeaconDeactivate  = "minecraft:block.beacon.deactivate"
	SoundBeaconAmbient     = "minecraft:block.beacon.ambient"
	SoundBeaconPowerSelect = "minecraft:block.beacon.power_select"
)

// BeaconBeamSection represents one colored vertical segment of a beacon's beam.
type BeaconBeamSection struct {
	Color  [3]float32 // RGB components in [0, 1]
	Height int        // Height in blocks of this beam segment
}

// BeaconBlockEntity represents the server-side state of a minecraft:beacon block entity.
type BeaconBlockEntity struct {
	ID              string // "minecraft:beacon"
	Dimension       string // e.g. "minecraft:overworld"
	X, Y, Z         int
	Levels          int                 // Active pyramid level (0..4; 0 if sky is blocked or no pyramid)
	PrimaryEffect   string              // Canonical mob_effect ID (e.g. "minecraft:speed") or ""
	SecondaryEffect string              // Canonical mob_effect ID (e.g. "minecraft:regeneration" or same as PrimaryEffect for Level II) or ""
	CustomName      string              // Optional custom container title
	BeamSections    []BeaconBeamSection // Active vertical beam sections when sky is clear and Levels > 0
	TickCounter     int                 // Ticks accumulated toward BeaconTickInterval (80)
	LastBuilder     *PlayerSession      // Optional player who placed/updated the beacon
}

// Clone returns a deep copy of b.
func (b BeaconBlockEntity) Clone() BeaconBlockEntity {
	cp := b
	if b.Dimension == "" {
		cp.Dimension = world.DimensionOverworld
	}
	if b.ID == "" {
		cp.ID = "minecraft:beacon"
	}
	if len(b.BeamSections) > 0 {
		cp.BeamSections = make([]BeaconBeamSection, len(b.BeamSections))
		copy(cp.BeamSections, b.BeamSections)
	}
	return cp
}

// IsActive reports whether the beacon has at least 1 valid pyramid level and
// an unobstructed beam to the sky.
func (b BeaconBlockEntity) IsActive() bool {
	return b.Levels > 0 && len(b.BeamSections) > 0
}

// EffectRadius returns the horizontal/downward effect radius in blocks for the
// beacon's current level: Levels*10 + 10 (20, 30, 40, 50 blocks for levels 1..4),
// or 0 when inactive.
func (b BeaconBlockEntity) EffectRadius() float64 {
	if b.Levels <= 0 {
		return 0
	}
	return float64(b.Levels*10 + 10)
}

// EffectDurationTicks returns the status effect duration in ticks for the
// beacon's current level: (9 + Levels*2) * 20 (220, 260, 300, 340 ticks for
// levels 1..4), matching vanilla BeaconBlockEntity.applyEffects.
func (b BeaconBlockEntity) EffectDurationTicks() int32 {
	if b.Levels <= 0 {
		return 0
	}
	return int32((9 + b.Levels*2) * 20)
}

// PrimaryAmplifier returns 1 (Level II) when the beacon is at level 4 and
// SecondaryEffect equals PrimaryEffect, or 0 (Level I) otherwise.
func (b BeaconBlockEntity) PrimaryAmplifier() int32 {
	if b.Levels >= BeaconMaxLevel && b.PrimaryEffect != "" &&
		canonicalEffectID(b.SecondaryEffect) == canonicalEffectID(b.PrimaryEffect) {
		return 1
	}
	return 0
}

// HasDistinctSecondary reports whether the beacon is at level 4 and has a
// secondary effect distinct from PrimaryEffect (e.g. Regeneration I).
func (b BeaconBlockEntity) HasDistinctSecondary() bool {
	if b.Levels < BeaconMaxLevel || b.SecondaryEffect == "" {
		return false
	}
	return canonicalEffectID(b.SecondaryEffect) != canonicalEffectID(b.PrimaryEffect)
}

// --- Beacon Block, Pyramid Base, Sky/Beam & Payment Validation ---

// IsBeaconBlock reports whether state is a minecraft:beacon block.
func IsBeaconBlock(state uint16) bool {
	return world.BlockName(state) == "minecraft:beacon"
}

// IsBeaconBaseBlockName reports whether blockName is one of the 5 vanilla
// `#minecraft:beacon_base_blocks` (iron, gold, emerald, diamond, netherite block).
func IsBeaconBaseBlockName(blockName string) bool {
	switch canonicalBeaconResource(blockName) {
	case "minecraft:iron_block",
		"minecraft:gold_block",
		"minecraft:emerald_block",
		"minecraft:diamond_block",
		"minecraft:netherite_block":
		return true
	default:
		return false
	}
}

// IsBeaconBaseBlockState reports whether state is a valid beacon pyramid base block.
func IsBeaconBaseBlockState(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	return IsBeaconBaseBlockName(world.BlockName(state))
}

// IsBeaconPaymentItemName reports whether itemName is one of the 5 vanilla
// `#minecraft:beacon_payment_items` (iron_ingot, gold_ingot, emerald, diamond,
// netherite_ingot).
func IsBeaconPaymentItemName(itemName string) bool {
	switch canonicalBeaconResource(itemName) {
	case "minecraft:iron_ingot",
		"minecraft:gold_ingot",
		"minecraft:emerald",
		"minecraft:diamond",
		"minecraft:netherite_ingot":
		return true
	default:
		return false
	}
}

// IsBeaconPaymentItem reports whether stack is a valid beacon payment item.
func IsBeaconPaymentItem(stack world.ItemStack) bool {
	if stack.IsEmpty() {
		return false
	}
	return IsBeaconPaymentItemName(stack.ResolvedID())
}

// CheckBeaconPyramidLevel computes the valid pyramid level (0..4) beneath a
// beacon at (bx, by, bz) using getBlock(x, y, z).
// Layer k (1..4) at y = by - k must be a solid (2k+1)x(2k+1) square of beacon
// base blocks centered at (bx, bz):
//   - Level 1: 3x3 (9 blocks)
//   - Level 2: + 5x5 (25 blocks, 34 total)
//   - Level 3: + 7x7 (49 blocks, 83 total)
//   - Level 4: + 9x9 (81 blocks, 164 total)
func CheckBeaconPyramidLevel(getBlock func(x, y, z int) uint16, bx, by, bz int) int {
	if getBlock == nil {
		return 0
	}
	levels := 0
	for layer := 1; layer <= BeaconMaxLevel; layer++ {
		py := by - layer
		if py < -64 {
			break
		}
		layerValid := true
		for x := bx - layer; x <= bx+layer && layerValid; x++ {
			for z := bz - layer; z <= bz+layer; z++ {
				if !IsBeaconBaseBlockState(getBlock(x, py, z)) {
					layerValid = false
					break
				}
			}
		}
		if !layerValid {
			break
		}
		levels = layer
	}
	return levels
}

// stainedGlassRGB maps stained glass / stained glass pane block names to their
// vanilla DyeColor textureDiffuseColors RGB values in [0, 1].
var stainedGlassRGB = map[string][3]float32{
	"minecraft:white_stained_glass":             {0.976, 1.000, 0.996},
	"minecraft:white_stained_glass_pane":        {0.976, 1.000, 0.996},
	"minecraft:orange_stained_glass":            {0.976, 0.502, 0.114},
	"minecraft:orange_stained_glass_pane":       {0.976, 0.502, 0.114},
	"minecraft:magenta_stained_glass":           {0.780, 0.306, 0.741},
	"minecraft:magenta_stained_glass_pane":      {0.780, 0.306, 0.741},
	"minecraft:light_blue_stained_glass":        {0.227, 0.702, 0.855},
	"minecraft:light_blue_stained_glass_pane":   {0.227, 0.702, 0.855},
	"minecraft:yellow_stained_glass":            {0.996, 0.847, 0.239},
	"minecraft:yellow_stained_glass_pane":       {0.996, 0.847, 0.239},
	"minecraft:lime_stained_glass":              {0.502, 0.780, 0.122},
	"minecraft:lime_stained_glass_pane":         {0.502, 0.780, 0.122},
	"minecraft:pink_stained_glass":              {0.953, 0.545, 0.667},
	"minecraft:pink_stained_glass_pane":         {0.953, 0.545, 0.667},
	"minecraft:gray_stained_glass":              {0.278, 0.310, 0.322},
	"minecraft:gray_stained_glass_pane":         {0.278, 0.310, 0.322},
	"minecraft:light_gray_stained_glass":        {0.616, 0.616, 0.592},
	"minecraft:light_gray_stained_glass_pane":   {0.616, 0.616, 0.592},
	"minecraft:cyan_stained_glass":              {0.086, 0.612, 0.612},
	"minecraft:cyan_stained_glass_pane":         {0.086, 0.612, 0.612},
	"minecraft:purple_stained_glass":            {0.537, 0.196, 0.722},
	"minecraft:purple_stained_glass_pane":       {0.537, 0.196, 0.722},
	"minecraft:blue_stained_glass":              {0.235, 0.267, 0.667},
	"minecraft:blue_stained_glass_pane":         {0.235, 0.267, 0.667},
	"minecraft:brown_stained_glass":             {0.514, 0.329, 0.196},
	"minecraft:brown_stained_glass_pane":        {0.514, 0.329, 0.196},
	"minecraft:green_stained_glass":             {0.369, 0.486, 0.086},
	"minecraft:green_stained_glass_pane":        {0.369, 0.486, 0.086},
	"minecraft:red_stained_glass":               {0.690, 0.180, 0.149},
	"minecraft:red_stained_glass_pane":          {0.690, 0.180, 0.149},
	"minecraft:black_stained_glass":             {0.114, 0.114, 0.129},
	"minecraft:black_stained_glass_pane":        {0.114, 0.114, 0.129},
}

// StainedGlassBeamColor returns the RGB beam color for a stained glass or
// stained glass pane block name, if applicable.
func StainedGlassBeamColor(blockName string) ([3]float32, bool) {
	col, ok := stainedGlassRGB[canonicalBeaconResource(blockName)]
	return col, ok
}

// IsBeaconBeamPassableBlockName reports whether a beacon beam can pass through
// blockName. In vanilla 26.1.2, air, glass, stained glass, glass panes, water,
// bedrock (for Nether ceiling parity), and non-occluding transparent blocks
// allow the beam through, whereas tinted_glass and opaque solid blocks block it.
func IsBeaconBeamPassableBlockName(blockName string) bool {
	name := canonicalBeaconResource(blockName)
	if name == "" || name == "minecraft:air" || name == "minecraft:cave_air" || name == "minecraft:void_air" {
		return true
	}
	if name == "minecraft:tinted_glass" {
		return false
	}
	if _, ok := stainedGlassRGB[name]; ok {
		return true
	}
	switch name {
	case "minecraft:glass",
		"minecraft:glass_pane",
		"minecraft:bedrock",
		"minecraft:water",
		"minecraft:bubble_column",
		"minecraft:iron_bars",
		"minecraft:chain",
		"minecraft:end_rod",
		"minecraft:lightning_rod",
		"minecraft:ladder",
		"minecraft:vine",
		"minecraft:scaffolding",
		"minecraft:torch",
		"minecraft:wall_torch",
		"minecraft:soul_torch",
		"minecraft:soul_wall_torch",
		"minecraft:redstone_torch",
		"minecraft:redstone_wall_torch",
		"minecraft:lever",
		"minecraft:redstone_wire",
		"minecraft:repeater",
		"minecraft:comparator",
		"minecraft:tripwire",
		"minecraft:tripwire_hook",
		"minecraft:conduit",
		"minecraft:beacon":
		return true
	}
	if strings.HasSuffix(name, "_carpet") ||
		strings.HasSuffix(name, "_banner") ||
		strings.HasSuffix(name, "_sign") ||
		strings.HasSuffix(name, "_button") ||
		strings.HasSuffix(name, "_pressure_plate") ||
		strings.HasSuffix(name, "_fence") ||
		strings.HasSuffix(name, "_fence_gate") ||
		strings.HasSuffix(name, "_wall") ||
		strings.HasSuffix(name, "_trapdoor") ||
		strings.HasSuffix(name, "_door") ||
		strings.HasSuffix(name, "_pane") {
		return true
	}
	return false
}

// IsBeaconBeamPassableBlockState reports whether state allows a beacon beam to pass.
func IsBeaconBeamPassableBlockState(state uint16) bool {
	if state == world.StateAir {
		return true
	}
	return IsBeaconBeamPassableBlockName(world.BlockName(state))
}

// CheckBeaconSkyAndBeam checks every block from y = by+1 up to maxY above
// (bx, by, bz) and builds the colored BeaconBeamSection slice. If any block
// obstructs the beam, clear is false and sections is nil.
func CheckBeaconSkyAndBeam(getBlock func(x, y, z int) uint16, bx, by, bz, maxY int) (clear bool, sections []BeaconBeamSection) {
	if getBlock == nil {
		return false, nil
	}
	if maxY <= by {
		maxY = BeaconDefaultMaxBuild
	}
	white := [3]float32{1.0, 1.0, 1.0}
	sections = []BeaconBeamSection{{Color: white, Height: 1}}
	hasStained := false

	for y := by + 1; y <= maxY; y++ {
		st := getBlock(bx, y, bz)
		if st == world.StateAir {
			sections[len(sections)-1].Height++
			continue
		}
		name := world.BlockName(st)
		if !IsBeaconBeamPassableBlockName(name) {
			return false, nil
		}
		if glassCol, isStained := StainedGlassBeamColor(name); isStained {
			last := &sections[len(sections)-1]
			if !hasStained {
				hasStained = true
				if len(sections) == 1 && last.Color == white && last.Height == 1 && y == by+1 {
					last.Color = glassCol
				} else {
					sections = append(sections, BeaconBeamSection{Color: glassCol, Height: 1})
				}
			} else {
				blended := [3]float32{
					(last.Color[0] + glassCol[0]) * 0.5,
					(last.Color[1] + glassCol[1]) * 0.5,
					(last.Color[2] + glassCol[2]) * 0.5,
				}
				if math.Abs(float64(blended[0]-last.Color[0])) < 1e-4 &&
					math.Abs(float64(blended[1]-last.Color[1])) < 1e-4 &&
					math.Abs(float64(blended[2]-last.Color[2])) < 1e-4 {
					last.Height++
				} else {
					sections = append(sections, BeaconBeamSection{Color: blended, Height: 1})
				}
			}
		} else {
			sections[len(sections)-1].Height++
		}
	}
	return true, sections
}

// --- Beacon Effect Tiers & Menu/Packet Encoding ---

// ValidBeaconPrimaryEffectsForLevel returns the allowed primary effect IDs for
// a beacon of the given pyramid level (1..4).
func ValidBeaconPrimaryEffectsForLevel(level int) []string {
	if level < 1 {
		return nil
	}
	out := []string{EffectSpeed, EffectHaste}
	if level >= 2 {
		out = append(out, EffectResistance, EffectJumpBoost)
	}
	if level >= 3 {
		out = append(out, EffectStrength)
	}
	return out
}

// IsValidBeaconPrimaryEffect reports whether effectID is a valid primary beacon
// power at the given pyramid level (1..4). Empty string ("") is allowed when
// clearing/leaving primary unset.
func IsValidBeaconPrimaryEffect(level int, effectID string) bool {
	if effectID == "" {
		return true
	}
	if level < 1 {
		return false
	}
	id := canonicalEffectID(effectID)
	switch id {
	case EffectSpeed, EffectHaste:
		return level >= 1
	case EffectResistance, EffectJumpBoost:
		return level >= 2
	case EffectStrength:
		return level >= 3
	default:
		return false
	}
}

// IsValidBeaconSecondaryEffect reports whether secondaryID is a valid secondary
// beacon power at the given pyramid level and primaryID.
// At level 4, secondary may be EffectRegeneration ("minecraft:regeneration") or
// identical to a valid non-empty primaryID (upgrading primary to Level II).
func IsValidBeaconSecondaryEffect(level int, primaryID, secondaryID string) bool {
	if secondaryID == "" {
		return true
	}
	if level < BeaconMaxLevel {
		return false
	}
	sec := canonicalEffectID(secondaryID)
	if sec == EffectRegeneration {
		return true
	}
	prim := canonicalEffectID(primaryID)
	return prim != "" && IsValidBeaconPrimaryEffect(level, prim) && sec == prim
}

// EncodeBeaconMenuEffectData encodes a status effect ID into the int16 value
// sent in BeaconMenu's ContainerSetData slots 1 and 2, matching vanilla
// BeaconMenu.encodeEffect: 0 when empty, or RegistryIndex + 1 when set.
func EncodeBeaconMenuEffectData(effectID string) int16 {
	if effectID == "" {
		return 0
	}
	def, ok := DefaultMobEffectRegistry().ByID(effectID)
	if !ok || def == nil {
		return 0
	}
	return int16(def.RegistryIndex + 1)
}

// DecodeBeaconMenuEffectData decodes the 1-indexed int16 value from BeaconMenu
// ContainerSetData back to a canonical effect ID ("" when <= 0).
func DecodeBeaconMenuEffectData(encoded int16) string {
	if encoded <= 0 {
		return ""
	}
	def, ok := DefaultMobEffectRegistry().ByIndex(int32(encoded - 1))
	if !ok || def == nil {
		return ""
	}
	return def.ID
}

// EncodeSetBeaconPacket encodes a protocol 775 ServerboundSetBeaconPacket (0x34):
//   - hasPrimary (bool) + [primary effect 0-indexed registry VarInt]
//   - hasSecondary (bool) + [secondary effect 0-indexed registry VarInt]
func EncodeSetBeaconPacket(primaryEffectID, secondaryEffectID string) []byte {
	w := protocol.NewWriter(12)
	if def, ok := DefaultMobEffectRegistry().ByID(primaryEffectID); ok && def != nil && primaryEffectID != "" {
		w.Bool(true)
		w.VarInt(def.RegistryIndex)
	} else {
		w.Bool(false)
	}
	if def, ok := DefaultMobEffectRegistry().ByID(secondaryEffectID); ok && def != nil && secondaryEffectID != "" {
		w.Bool(true)
		w.VarInt(def.RegistryIndex)
	} else {
		w.Bool(false)
	}
	return w.Bytes()
}

// DecodeSetBeaconPacket parses a protocol 775 ServerboundSetBeaconPacket (0x34)
// and returns the canonical primary and secondary effect IDs (or "" when unset).
func DecodeSetBeaconPacket(r *protocol.Reader) (primaryEffectID, secondaryEffectID string, err error) {
	if r == nil {
		return "", "", errors.New("server: nil reader for set_beacon packet")
	}
	hasPrimary, err := r.Bool()
	if err != nil {
		return "", "", err
	}
	if hasPrimary {
		idx, err := r.VarInt()
		if err != nil {
			return "", "", err
		}
		if def, ok := DefaultMobEffectRegistry().ByIndex(idx); ok && def != nil {
			primaryEffectID = def.ID
		}
	}
	hasSecondary, err := r.Bool()
	if err != nil {
		return primaryEffectID, "", err
	}
	if hasSecondary {
		idx, err := r.VarInt()
		if err != nil {
			return primaryEffectID, "", err
		}
		if def, ok := DefaultMobEffectRegistry().ByIndex(idx); ok && def != nil {
			secondaryEffectID = def.ID
		}
	}
	return primaryEffectID, secondaryEffectID, nil
}

// --- Beacon Block Entity NBT Serialization ---

// EncodeBeaconBlockEntityNBT serializes b into a vanilla 26.1.2 block entity
// NBT compound (`id: "minecraft:beacon"`, `x`, `y`, `z`, `Levels`,
// `primary_effect`, `secondary_effect`, `CustomName`, `Dimension`).
func EncodeBeaconBlockEntityNBT(b BeaconBlockEntity) *nbt.Compound {
	id := b.ID
	if id == "" {
		id = "minecraft:beacon"
	}
	dim := b.Dimension
	if dim == "" {
		dim = world.DimensionOverworld
	}
	comp := nbt.NewCompound().
		Set("id", nbt.String(id)).
		Set("Dimension", nbt.String(world.NormalizeDimension(dim))).
		Set("x", nbt.Int(int32(b.X))).
		Set("y", nbt.Int(int32(b.Y))).
		Set("z", nbt.Int(int32(b.Z))).
		Set("Levels", nbt.Int(int32(b.Levels)))

	if b.PrimaryEffect != "" {
		comp.Set("primary_effect", nbt.String(canonicalEffectID(b.PrimaryEffect)))
	}
	if b.SecondaryEffect != "" {
		comp.Set("secondary_effect", nbt.String(canonicalEffectID(b.SecondaryEffect)))
	}
	if b.CustomName != "" {
		comp.Set("CustomName", nbt.String(b.CustomName))
	}
	if b.TickCounter > 0 {
		comp.Set("TickCounter", nbt.Int(int32(b.TickCounter)))
	}
	return comp
}

// ToNBT is a convenience method returning EncodeBeaconBlockEntityNBT(b).
func (b BeaconBlockEntity) ToNBT() *nbt.Compound {
	return EncodeBeaconBlockEntityNBT(b)
}

// DecodeBeaconBlockEntityNBT parses a BeaconBlockEntity from an NBT compound.
func DecodeBeaconBlockEntityNBT(comp *nbt.Compound) (BeaconBlockEntity, bool) {
	if comp == nil {
		return BeaconBlockEntity{}, false
	}
	b := BeaconBlockEntity{
		ID:        "minecraft:beacon",
		Dimension: world.DimensionOverworld,
	}
	if v, ok := comp.Get("id"); ok {
		if s, ok := v.(nbt.String); ok && string(s) != "" {
			if canonicalBeaconResource(string(s)) != "minecraft:beacon" {
				return BeaconBlockEntity{}, false
			}
			b.ID = "minecraft:beacon"
		}
	}
	if v, ok := comp.Get("Dimension"); ok {
		if s, ok := v.(nbt.String); ok && string(s) != "" {
			b.Dimension = world.NormalizeDimension(string(s))
		}
	}
	if v, ok := comp.Get("x"); ok {
		if iv, ok := v.(nbt.Int); ok {
			b.X = int(iv)
		}
	}
	if v, ok := comp.Get("y"); ok {
		if iv, ok := v.(nbt.Int); ok {
			b.Y = int(iv)
		}
	}
	if v, ok := comp.Get("z"); ok {
		if iv, ok := v.(nbt.Int); ok {
			b.Z = int(iv)
		}
	}
	if v, ok := comp.Get("Levels"); ok {
		if iv, ok := v.(nbt.Int); ok {
			lvl := int(iv)
			if lvl < 0 {
				lvl = 0
			} else if lvl > BeaconMaxLevel {
				lvl = BeaconMaxLevel
			}
			b.Levels = lvl
		}
	}
	if v, ok := comp.Get("primary_effect"); ok {
		if s, ok := v.(nbt.String); ok && string(s) != "" {
			if _, valid := DefaultMobEffectRegistry().ByID(string(s)); valid {
				b.PrimaryEffect = canonicalEffectID(string(s))
			}
		}
	} else if v, ok := comp.Get("Primary"); ok {
		if s, ok := v.(nbt.String); ok && string(s) != "" {
			if _, valid := DefaultMobEffectRegistry().ByID(string(s)); valid {
				b.PrimaryEffect = canonicalEffectID(string(s))
			}
		}
	}
	if v, ok := comp.Get("secondary_effect"); ok {
		if s, ok := v.(nbt.String); ok && string(s) != "" {
			if _, valid := DefaultMobEffectRegistry().ByID(string(s)); valid {
				b.SecondaryEffect = canonicalEffectID(string(s))
			}
		}
	} else if v, ok := comp.Get("Secondary"); ok {
		if s, ok := v.(nbt.String); ok && string(s) != "" {
			if _, valid := DefaultMobEffectRegistry().ByID(string(s)); valid {
				b.SecondaryEffect = canonicalEffectID(string(s))
			}
		}
	}
	if v, ok := comp.Get("CustomName"); ok {
		if s, ok := v.(nbt.String); ok {
			b.CustomName = string(s)
		}
	}
	if v, ok := comp.Get("TickCounter"); ok {
		if iv, ok := v.(nbt.Int); ok && iv >= 0 {
			b.TickCounter = int(iv)
		}
	}
	return b, true
}

// BeaconFromNBT parses a *BeaconBlockEntity from an NBT compound.
func BeaconFromNBT(comp *nbt.Compound) (*BeaconBlockEntity, bool) {
	b, ok := DecodeBeaconBlockEntityNBT(comp)
	if !ok {
		return nil, false
	}
	return &b, true
}

// --- Server-Side Beacon Registry, Persistence, Tick & Container UI ---

type beaconKey struct {
	dim     string
	x, y, z int
}

type serverBeaconState struct {
	mu      sync.Mutex
	loaded  bool
	beacons map[beaconKey]*BeaconBlockEntity
}

var serverBeacons sync.Map // map[*Server]*serverBeaconState

func (s *Server) beaconState() *serverBeaconState {
	if v, ok := serverBeacons.Load(s); ok {
		return v.(*serverBeaconState)
	}
	actual, _ := serverBeacons.LoadOrStore(s, &serverBeaconState{
		beacons: make(map[beaconKey]*BeaconBlockEntity),
	})
	return actual.(*serverBeaconState)
}

func (s *Server) beaconsFilePath() string {
	if s == nil || s.cfg.WorldDir == "" {
		return ""
	}
	return filepath.Join(s.cfg.WorldDir, "region", "beacons.nbt")
}

func (s *Server) ensureBeaconsLoadedLocked(bs *serverBeaconState) {
	if bs.loaded {
		return
	}
	bs.loaded = true
	path := s.beaconsFilePath()
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_, tag, err := nbt.UnmarshalNamed(raw)
	if err != nil {
		return
	}
	root, ok := tag.(*nbt.Compound)
	if !ok {
		return
	}
	listTag, ok := root.Get("block_entities")
	if !ok {
		return
	}
	list, ok := listTag.(nbt.List)
	if !ok {
		return
	}
	for _, elem := range list.Elems {
		comp, ok := elem.(*nbt.Compound)
		if !ok {
			continue
		}
		if b, ok := DecodeBeaconBlockEntityNBT(comp); ok {
			cp := b.Clone()
			bs.beacons[beaconKey{dim: cp.Dimension, x: cp.X, y: cp.Y, z: cp.Z}] = &cp
		}
	}
}

// LoadBeacons reloads persisted beacon block entities from <WorldDir>/region/beacons.nbt.
func (s *Server) LoadBeacons() error {
	if s == nil {
		return nil
	}
	bs := s.beaconState()
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.loaded = false
	s.ensureBeaconsLoadedLocked(bs)
	return nil
}

// SaveBeacons flushes all active beacon block entities to <WorldDir>/region/beacons.nbt.
func (s *Server) SaveBeacons() error {
	if s == nil {
		return nil
	}
	path := s.beaconsFilePath()
	if path == "" {
		return nil
	}
	bs := s.beaconState()
	bs.mu.Lock()
	s.ensureBeaconsLoadedLocked(bs)
	keys := make([]beaconKey, 0, len(bs.beacons))
	for k := range bs.beacons {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dim != keys[j].dim {
			return keys[i].dim < keys[j].dim
		}
		if keys[i].y != keys[j].y {
			return keys[i].y < keys[j].y
		}
		if keys[i].z != keys[j].z {
			return keys[i].z < keys[j].z
		}
		return keys[i].x < keys[j].x
	})
	elems := make([]nbt.Tag, 0, len(keys))
	for _, k := range keys {
		b := bs.beacons[k]
		if b == nil {
			continue
		}
		elems = append(elems, EncodeBeaconBlockEntityNBT(*b))
	}
	bs.mu.Unlock()

	elemID := byte(nbt.TagCompound)
	if len(elems) == 0 {
		elemID = nbt.TagEnd
	}
	root := nbt.NewCompound().Set("block_entities", nbt.List{
		ElemID: elemID,
		Elems:  elems,
	})
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, nbt.MarshalNamed("", root), 0o644)
}

// ChunkBeaconBlockEntitiesNBT returns all beacon block entity NBT compounds
// located in chunk (cx, cz) of dimension for Anvil chunk serialization.
func (s *Server) ChunkBeaconBlockEntitiesNBT(dimension string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	dim := world.NormalizeDimension(dimension)
	bs := s.beaconState()
	bs.mu.Lock()
	s.ensureBeaconsLoadedLocked(bs)
	var out []*nbt.Compound
	for k, b := range bs.beacons {
		if b == nil || k.dim != dim {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			out = append(out, EncodeBeaconBlockEntityNBT(*b))
		}
	}
	bs.mu.Unlock()
	return out
}

// UnloadChunkBeacons removes all beacon block entities in chunk (cx, cz) of
// dimension from runtime memory and returns their serialized NBT compounds.
func (s *Server) UnloadChunkBeacons(dimension string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	dim := world.NormalizeDimension(dimension)
	bs := s.beaconState()
	bs.mu.Lock()
	s.ensureBeaconsLoadedLocked(bs)
	var out []*nbt.Compound
	for k, b := range bs.beacons {
		if k.dim != dim {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			if b != nil {
				out = append(out, EncodeBeaconBlockEntityNBT(*b))
			}
			delete(bs.beacons, k)
		}
	}
	bs.mu.Unlock()
	return out
}

// LoadChunkBeaconBlockEntitiesNBT restores any minecraft:beacon block entities
// from a chunk's `block_entities` NBT list.
func (s *Server) LoadChunkBeaconBlockEntitiesNBT(dimension string, list nbt.List) {
	if s == nil {
		return
	}
	dim := world.NormalizeDimension(dimension)
	bs := s.beaconState()
	bs.mu.Lock()
	s.ensureBeaconsLoadedLocked(bs)
	for _, elem := range list.Elems {
		comp, ok := elem.(*nbt.Compound)
		if !ok {
			continue
		}
		if b, ok := DecodeBeaconBlockEntityNBT(comp); ok {
			b.Dimension = dim
			cp := b.Clone()
			bs.beacons[beaconKey{dim: dim, x: cp.X, y: cp.Y, z: cp.Z}] = &cp
		}
	}
	bs.mu.Unlock()
}

func (s *Server) getOrCreateBeaconLocked(bs *serverBeaconState, dim string, x, y, z int) *BeaconBlockEntity {
	s.ensureBeaconsLoadedLocked(bs)
	normDim := world.NormalizeDimension(dim)
	key := beaconKey{dim: normDim, x: x, y: y, z: z}
	b := bs.beacons[key]
	if b == nil {
		b = &BeaconBlockEntity{
			ID:        "minecraft:beacon",
			Dimension: normDim,
			X:         x,
			Y:         y,
			Z:         z,
		}
		bs.beacons[key] = b
	}
	return b
}

// BeaconAtInDim returns a snapshot of the beacon block entity at (x, y, z) in dim.
func (s *Server) BeaconAtInDim(dim string, x, y, z int) (BeaconBlockEntity, bool) {
	if s == nil {
		return BeaconBlockEntity{}, false
	}
	normDim := world.NormalizeDimension(dim)
	bs := s.beaconState()
	bs.mu.Lock()
	s.ensureBeaconsLoadedLocked(bs)
	b := bs.beacons[beaconKey{dim: normDim, x: x, y: y, z: z}]
	if b == nil {
		bs.mu.Unlock()
		cache := s.DimensionChunks(normDim)
		if cache != nil && IsBeaconBlock(cache.GetBlock(x, y, z)) {
			return BeaconBlockEntity{
				ID:        "minecraft:beacon",
				Dimension: normDim,
				X:         x,
				Y:         y,
				Z:         z,
			}, true
		}
		return BeaconBlockEntity{}, false
	}
	cp := b.Clone()
	bs.mu.Unlock()
	return cp, true
}

// BeaconAt returns a snapshot of the beacon block entity at (x, y, z) in the Overworld.
func (s *Server) BeaconAt(x, y, z int) (BeaconBlockEntity, bool) {
	return s.BeaconAtInDim(world.DimensionOverworld, x, y, z)
}

// SetBeaconState replaces or inserts the beacon block entity at (b.Dimension, b.X, b.Y, b.Z)
// and persists it.
func (s *Server) SetBeaconState(b BeaconBlockEntity) {
	if s == nil {
		return
	}
	cp := b.Clone()
	bs := s.beaconState()
	bs.mu.Lock()
	s.ensureBeaconsLoadedLocked(bs)
	bs.beacons[beaconKey{dim: cp.Dimension, x: cp.X, y: cp.Y, z: cp.Z}] = &cp
	bs.mu.Unlock()
	_ = s.SaveBeacons()
}

// RemoveBeacon removes the beacon block entity at (x, y, z) in dim, closes any
// open BeaconMenu screens targeting it, and plays the deactivation sound if it
// was active.
func (s *Server) RemoveBeaconInDim(dim string, x, y, z int) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	bs := s.beaconState()
	bs.mu.Lock()
	s.ensureBeaconsLoadedLocked(bs)
	key := beaconKey{dim: normDim, x: x, y: y, z: z}
	prev := bs.beacons[key]
	delete(bs.beacons, key)
	bs.mu.Unlock()

	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()
	for _, sess := range sessions {
		open := sess.OpenContainer()
		if open != nil && open.MenuType == MenuTypeBeacon &&
			open.BlockX == x && open.BlockY == y && open.BlockZ == z {
			_ = s.CloseContainer(sess, open.ContainerID, true)
		}
	}

	if prev != nil {
		if prev.Levels > 0 {
			s.playBeaconSound(normDim, x, y, z, SoundBeaconDeactivate)
		}
		_ = s.SaveBeacons()
	}
}

// RemoveBeacon removes the beacon block entity at (x, y, z) in the Overworld.
func (s *Server) RemoveBeacon(x, y, z int) {
	s.RemoveBeaconInDim(world.DimensionOverworld, x, y, z)
}

// UpdateBeaconAtInDim evaluates the pyramid level (1..4) and sky/beam clearance
// for the beacon at (x, y, z) in dim, triggers activate/deactivate sounds and
// construct_beacon / create_full_beacon advancements when transitioning levels,
// and returns the updated BeaconBlockEntity.
func (s *Server) UpdateBeaconAtInDim(dim string, x, y, z int, builder *PlayerSession) (BeaconBlockEntity, bool) {
	if s == nil {
		return BeaconBlockEntity{}, false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return BeaconBlockEntity{}, false
	}

	pyramidLevel := CheckBeaconPyramidLevel(cache.GetBlock, x, y, z)
	skyClear, beamSections := CheckBeaconSkyAndBeam(cache.GetBlock, x, y, z, BeaconDefaultMaxBuild)
	newLevels := 0
	if pyramidLevel > 0 && skyClear {
		newLevels = pyramidLevel
	} else {
		beamSections = nil
	}

	bs := s.beaconState()
	bs.mu.Lock()
	b := s.getOrCreateBeaconLocked(bs, normDim, x, y, z)
	if builder != nil {
		b.LastBuilder = builder
	}
	prevLevels := b.Levels
	prevSections := make([]BeaconBeamSection, len(b.BeamSections))
	copy(prevSections, b.BeamSections)
	b.Levels = newLevels
	b.BeamSections = beamSections
	lastBuilder := b.LastBuilder
	cp := b.Clone()
	bs.mu.Unlock()

	if prevLevels <= 0 && newLevels > 0 {
		s.playBeaconSound(normDim, x, y, z, SoundBeaconActivate)
		s.notifyConstructBeaconAdvancement(normDim, x, y, z, newLevels, lastBuilder)
	} else if prevLevels > 0 && newLevels <= 0 {
		s.playBeaconSound(normDim, x, y, z, SoundBeaconDeactivate)
	} else if newLevels > prevLevels && newLevels > 0 {
		s.notifyConstructBeaconAdvancement(normDim, x, y, z, newLevels, lastBuilder)
	}

	sectionsChanged := !equalBeaconBeamSections(prevSections, beamSections)
	if prevLevels != newLevels || sectionsChanged {
		_ = s.SaveBeacons()
		s.broadcastBeaconDataToViewers(normDim, x, y, z)
		s.BroadcastBeaconBlockEntityData(normDim, x, y, z)
	}
	return cp, prevLevels != newLevels || sectionsChanged
}

func equalBeaconBeamSections(a, b []BeaconBeamSection) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// UpdateBeaconAt evaluates the pyramid and sky/beam state for the Overworld
// beacon at (x, y, z).
func (s *Server) UpdateBeaconAt(x, y, z int, builder *PlayerSession) (BeaconBlockEntity, bool) {
	return s.UpdateBeaconAtInDim(world.DimensionOverworld, x, y, z, builder)
}

func (s *Server) notifyConstructBeaconAdvancement(dim string, bx, by, bz, level int, builder *PlayerSession) {
	if s == nil || level <= 0 {
		return
	}
	normDim := world.NormalizeDimension(dim)
	notified := make(map[*PlayerSession]bool)
	if builder != nil {
		s.TriggerConstructBeacon(builder, level)
		notified[builder] = true
	}

	// Vanilla BeaconBlockEntity triggers ConstructBeaconTrigger for all players
	// within a 10x10x10 box (inflate(10.0, 5.0, 10.0)) around the beacon.
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()

	cx := float64(bx) + 0.5
	cy := float64(by) + 0.5
	cz := float64(bz) + 0.5
	for _, sess := range sessions {
		if sess == nil || notified[sess] || sess.IsDead() {
			continue
		}
		if world.NormalizeDimension(sess.Dimension()) != normDim {
			continue
		}
		snap := sess.Snapshot()
		if math.Abs(snap.X-cx) <= 10.5 && math.Abs(snap.Y-cy) <= 5.5 && math.Abs(snap.Z-cz) <= 10.5 {
			s.TriggerConstructBeacon(sess, level)
			notified[sess] = true
		}
	}
}

// ApplyBeaconEffectsAtInDim applies the primary (and optional level-4 secondary)
// status effects of the beacon at (bx, by, bz) in dim to all non-dead players
// within its effect box (horizontal/downward radius = Levels*10 + 10, upward
// radius + 384), returning the number of players affected.
func (s *Server) ApplyBeaconEffectsAtInDim(dim string, bx, by, bz int) int {
	if s == nil {
		return 0
	}
	b, ok := s.BeaconAtInDim(dim, bx, by, bz)
	if !ok || b.Levels <= 0 || b.PrimaryEffect == "" {
		return 0
	}
	if !IsValidBeaconPrimaryEffect(b.Levels, b.PrimaryEffect) {
		return 0
	}

	normDim := world.NormalizeDimension(b.Dimension)
	radius := b.EffectRadius()
	duration := b.EffectDurationTicks()
	primaryAmp := b.PrimaryAmplifier()
	hasSecondary := b.HasDistinctSecondary() && IsValidBeaconSecondaryEffect(b.Levels, b.PrimaryEffect, b.SecondaryEffect)

	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()

	cx := float64(bx) + 0.5
	cy := float64(by) + 0.5
	cz := float64(bz) + 0.5
	affected := 0
	for _, sess := range sessions {
		if sess == nil || sess.IsDead() {
			continue
		}
		if world.NormalizeDimension(sess.Dimension()) != normDim {
			continue
		}
		snap := sess.Snapshot()
		dx := math.Abs(snap.X - cx)
		dz := math.Abs(snap.Z - cz)
		dy := snap.Y - cy
		if dx > radius || dz > radius || dy < -radius || dy > radius+384.0 {
			continue
		}

		s.ApplyPlayerEffect(sess, StatusEffectInstance{
			ID:            b.PrimaryEffect,
			Amplifier:     primaryAmp,
			Duration:      duration,
			Ambient:       true,
			ShowParticles: true,
			ShowIcon:      true,
		})
		if hasSecondary {
			s.ApplyPlayerEffect(sess, StatusEffectInstance{
				ID:            b.SecondaryEffect,
				Amplifier:     0,
				Duration:      duration,
				Ambient:       true,
				ShowParticles: true,
				ShowIcon:      true,
			})
		}
		affected++
	}
	return affected
}

// ApplyBeaconEffectsAt applies the Overworld beacon's effects at (bx, by, bz)
// to all players in range.
func (s *Server) ApplyBeaconEffectsAt(bx, by, bz int) int {
	return s.ApplyBeaconEffectsAtInDim(world.DimensionOverworld, bx, by, bz)
}

// TickBeaconAtInDim advances the beacon at (x, y, z) in dim by one server tick.
// Every 80 ticks (BeaconTickInterval), it re-evaluates the pyramid & sky beam,
// plays the ambient sound when active, and applies beacon status effects to
// players in range.
func (s *Server) TickBeaconAtInDim(dim string, x, y, z int) bool {
	if s == nil {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache != nil {
		state := cache.GetBlock(x, y, z)
		if !IsBeaconBlock(state) {
			s.RemoveBeaconInDim(normDim, x, y, z)
			return false
		}
	}

	bs := s.beaconState()
	bs.mu.Lock()
	b := s.getOrCreateBeaconLocked(bs, normDim, x, y, z)
	b.TickCounter++
	shouldPulse := b.TickCounter >= BeaconTickInterval
	if shouldPulse {
		b.TickCounter = 0
	}
	bs.mu.Unlock()

	if !shouldPulse {
		return false
	}

	updated, changed := s.UpdateBeaconAtInDim(normDim, x, y, z, nil)
	if updated.Levels > 0 {
		s.playBeaconSound(normDim, x, y, z, SoundBeaconAmbient)
		s.ApplyBeaconEffectsAtInDim(normDim, x, y, z)
		return true
	}
	return changed
}

// TickBeaconAt advances the Overworld beacon at (x, y, z) by one server tick.
func (s *Server) TickBeaconAt(x, y, z int) bool {
	return s.TickBeaconAtInDim(world.DimensionOverworld, x, y, z)
}

// TickBeacons advances all tracked beacons by one server tick and returns the
// number of beacons that pulsed or changed state.
func (s *Server) TickBeacons() int {
	if s == nil {
		return 0
	}
	bs := s.beaconState()
	bs.mu.Lock()
	s.ensureBeaconsLoadedLocked(bs)
	keys := make([]beaconKey, 0, len(bs.beacons))
	for k := range bs.beacons {
		keys = append(keys, k)
	}
	bs.mu.Unlock()

	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dim != keys[j].dim {
			return keys[i].dim < keys[j].dim
		}
		if keys[i].y != keys[j].y {
			return keys[i].y < keys[j].y
		}
		if keys[i].z != keys[j].z {
			return keys[i].z < keys[j].z
		}
		return keys[i].x < keys[j].x
	})

	pulsed := 0
	for _, k := range keys {
		if s.TickBeaconAtInDim(k.dim, k.x, k.y, k.z) {
			pulsed++
		}
	}
	return pulsed
}

// StartBeaconTicker runs a 20 TPS (50ms) background goroutine ticking all
// beacons until ctx is cancelled, then saves beacon state.
func (s *Server) StartBeaconTicker(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if s == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = s.SaveBeacons()
				return
			case <-ticker.C:
				s.TickBeacons()
			}
		}
	}()
	return done
}

// --- Beacon Container UI (Open / Click / SetBeaconPacket / Sync) ---

// OpenBeacon opens the beacon UI at (x, y, z) in session's dimension and sends
// open_screen (MenuTypeBeacon = 9), container_set_content (37 slots), and the
// three container_set_data properties (levels, primary effect, secondary effect).
func (s *Server) OpenBeacon(session *PlayerSession, x, y, z int) (int32, error) {
	if s == nil || session == nil {
		return 0, errors.New("server: nil session")
	}
	_ = s.CloseContainer(session, 0, false)

	dim := session.Dimension()
	if dim == "" {
		dim = world.DimensionOverworld
	}
	b, _ := s.UpdateBeaconAtInDim(dim, x, y, z, session)
	title := "Beacon"
	if b.CustomName != "" {
		title = b.CustomName
	}

	tr := getContainerTracker(session)
	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	tr.open = &OpenContainerState{
		ContainerID: cid,
		MenuType:    MenuTypeBeacon,
		BlockX:      x,
		BlockY:      y,
		BlockZ:      z,
	}
	carried := tr.carried
	tr.mu.Unlock()

	if session.send != nil {
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreen(cid, MenuTypeBeacon, title)); err != nil {
			return cid, err
		}
		slots := s.buildBeaconMenuSlots(session, world.ItemStack{})
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried)); err != nil {
			return cid, err
		}
		if err := s.sendBeaconDataProperties(session, cid, dim, x, y, z); err != nil {
			return cid, err
		}
	}
	return cid, nil
}

func (s *Server) buildBeaconMenuSlots(session *PlayerSession, paymentSlot world.ItemStack) []world.ItemStack {
	inv := session.Inventory()
	slots := make([]world.ItemStack, BeaconMenuSlotCount)
	slots[BeaconSlotPayment] = paymentSlot
	copy(slots[1:28], inv[9:36])
	copy(slots[28:37], inv[36:45])
	return slots
}

func (s *Server) sendBeaconDataProperties(session *PlayerSession, containerID int32, dim string, x, y, z int) error {
	if session == nil || session.send == nil {
		return nil
	}
	b, _ := s.BeaconAtInDim(dim, x, y, z)
	if err := session.send(protocol.PlayContainerSetData, EncodeContainerSetData(containerID, BeaconDataLevels, int16(b.Levels))); err != nil {
		return err
	}
	if err := session.send(protocol.PlayContainerSetData, EncodeContainerSetData(containerID, BeaconDataPrimaryEffect, EncodeBeaconMenuEffectData(b.PrimaryEffect))); err != nil {
		return err
	}
	return session.send(protocol.PlayContainerSetData, EncodeContainerSetData(containerID, BeaconDataSecondaryEffect, EncodeBeaconMenuEffectData(b.SecondaryEffect)))
}

// HandleBeaconClick processes a ContainerClick on an open BeaconMenu screen,
// enforcing the 1-item payment slot filter (`#minecraft:beacon_payment_items`)
// on slot 0 and syncing slot state.
func (s *Server) HandleBeaconClick(session *PlayerSession, click ContainerClick) error {
	if s == nil || session == nil || session.IsDead() {
		return nil
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.ContainerID != click.ContainerID || tr.open.MenuType != MenuTypeBeacon {
		tr.mu.Unlock()
		return nil
	}
	slot := int(click.Slot)

	if slot == -999 {
		if !tr.carried.IsEmpty() {
			dropCount := tr.carried.Count
			if click.Button == 1 {
				dropCount = 1
			}
			dropStack := world.NewItemStack(tr.carried.ResolvedItemID(), dropCount)
			rem := tr.carried.Count - dropCount
			if rem <= 0 {
				tr.carried = world.ItemStack{}
			} else {
				tr.carried = world.NewItemStack(tr.carried.ResolvedItemID(), rem)
			}
			snap := session.Snapshot()
			s.SpawnItemEntity(snap.X, snap.Y+0.5, snap.Z, dropStack)
		}
		tr.mu.Unlock()
		return s.syncOpenBeacon(session)
	}

	s.applyBeaconClickLocked(session, tr, slot, click.Button, click.Mode)
	tr.mu.Unlock()
	return s.syncOpenBeacon(session)
}

func (s *Server) applyBeaconClickLocked(
	session *PlayerSession,
	tr *playerContainerTracker,
	slot int,
	button int8,
	mode int32,
) {
	if slot < 0 || slot >= BeaconMenuSlotCount || tr.open == nil {
		return
	}
	// tr.open.Crafting[0] stores the transient 1-item payment slot so
	// CloseContainer automatically returns unconsumed payment items on close.
	slots := s.buildBeaconMenuSlots(session, tr.open.Crafting[0])

	switch mode {
	case ContainerInputPickup:
		if slot == BeaconSlotPayment {
			// Payment slot max count = 1, only beacon payment items allowed.
			if tr.carried.IsEmpty() {
				if !slots[0].IsEmpty() {
					tr.carried = slots[0]
					slots[0] = world.ItemStack{}
				}
			} else if IsBeaconPaymentItem(tr.carried) {
				if slots[0].IsEmpty() {
					slots[0] = world.NewItemStack(tr.carried.ResolvedItemID(), 1)
					rem := tr.carried.Count - 1
					if rem <= 0 {
						tr.carried = world.ItemStack{}
					} else {
						tr.carried = world.NewItemStack(tr.carried.ResolvedItemID(), rem)
					}
				} else if tr.carried.Count == 1 {
					slots[0], tr.carried = tr.carried, slots[0]
				}
			}
		} else {
			slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)
		}

	case ContainerInputQuickMove:
		if slot == BeaconSlotPayment {
			if !slots[0].IsEmpty() {
				slots[0] = mergeIntoSlotSlice(slots, slots[0], 1, BeaconMenuSlotCount)
			}
		} else {
			clicked := slots[slot]
			if !clicked.IsEmpty() {
				if slots[0].IsEmpty() && IsBeaconPaymentItem(clicked) && clicked.Count >= 1 {
					slots[0] = world.NewItemStack(clicked.ResolvedItemID(), 1)
					rem := clicked.Count - 1
					if rem <= 0 {
						slots[slot] = world.ItemStack{}
					} else {
						slots[slot] = world.NewItemStack(clicked.ResolvedItemID(), rem)
					}
				} else if slot >= 1 && slot < 28 {
					slots[slot] = mergeIntoSlotSlice(slots, clicked, 28, BeaconMenuSlotCount)
				} else if slot >= 28 && slot < BeaconMenuSlotCount {
					slots[slot] = mergeIntoSlotSlice(slots, clicked, 1, 28)
				}
			}
		}

	case ContainerInputSwap:
		if button >= 0 && button < 9 && slot != BeaconSlotPayment {
			hotbarIdx := 28 + int(button)
			slots[slot], slots[hotbarIdx] = slots[hotbarIdx], slots[slot]
		}
	}

	tr.open.Crafting[0] = slots[BeaconSlotPayment]
	for i := 0; i < 27; i++ {
		session.SetInventorySlot(9+i, slots[1+i])
	}
	for i := 0; i < 9; i++ {
		session.SetInventorySlot(36+i, slots[28+i])
	}
}

// HandleSetBeaconPacket processes a ServerboundSetBeaconPacket (0x34) from
// session while a BeaconMenu is open: validates that a payment item is present
// in slot 0 and that the requested primary/secondary effects are valid for the
// beacon's pyramid level, consumes 1 payment item, updates the beacon's effects,
// plays "minecraft:block.beacon.power_select", applies effects immediately, and
// syncs the container.
func (s *Server) HandleSetBeaconPacket(session *PlayerSession, primaryEffectID, secondaryEffectID string) error {
	if s == nil || session == nil || session.IsDead() {
		return nil
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.MenuType != MenuTypeBeacon {
		tr.mu.Unlock()
		return nil
	}
	bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
	payment := tr.open.Crafting[0]
	if payment.IsEmpty() || !IsBeaconPaymentItem(payment) {
		tr.mu.Unlock()
		return nil
	}
	tr.mu.Unlock()

	dim := session.Dimension()
	if dim == "" {
		dim = world.DimensionOverworld
	}
	normDim := world.NormalizeDimension(dim)

	b, _ := s.UpdateBeaconAtInDim(normDim, bx, by, bz, session)
	if b.Levels <= 0 {
		return nil
	}

	prim := ""
	if primaryEffectID != "" {
		prim = canonicalEffectID(primaryEffectID)
		if !IsValidBeaconPrimaryEffect(b.Levels, prim) {
			return nil
		}
	}
	sec := ""
	if secondaryEffectID != "" {
		sec = canonicalEffectID(secondaryEffectID)
		if !IsValidBeaconSecondaryEffect(b.Levels, prim, sec) {
			return nil
		}
	}

	// Consume 1 item from payment slot 0.
	tr.mu.Lock()
	if tr.open == nil || tr.open.MenuType != MenuTypeBeacon {
		tr.mu.Unlock()
		return nil
	}
	curPay := tr.open.Crafting[0]
	if curPay.IsEmpty() || !IsBeaconPaymentItem(curPay) {
		tr.mu.Unlock()
		return nil
	}
	rem := curPay.Count - 1
	if rem <= 0 {
		tr.open.Crafting[0] = world.ItemStack{}
	} else {
		tr.open.Crafting[0] = world.NewItemStack(curPay.ResolvedItemID(), rem)
	}
	tr.mu.Unlock()

	bs := s.beaconState()
	bs.mu.Lock()
	be := s.getOrCreateBeaconLocked(bs, normDim, bx, by, bz)
	be.PrimaryEffect = prim
	be.SecondaryEffect = sec
	be.LastBuilder = session
	bs.mu.Unlock()

	_ = s.SaveBeacons()
	s.playBeaconSound(normDim, bx, by, bz, SoundBeaconPowerSelect)
	s.ApplyBeaconEffectsAtInDim(normDim, bx, by, bz)
	s.broadcastBeaconDataToViewers(normDim, bx, by, bz)
	s.BroadcastBeaconBlockEntityData(normDim, bx, by, bz)
	return s.syncOpenBeacon(session)
}

// HandleSetBeaconRawPacket decodes a ServerboundSetBeaconPacket (`0x34`) from
// `r` and dispatches it to `HandleSetBeaconPacket(session, primaryID, secondaryID)`.
func (s *Server) HandleSetBeaconRawPacket(session *PlayerSession, r *protocol.Reader) error {
	if s == nil || session == nil || r == nil {
		return nil
	}
	primaryID, secondaryID, err := DecodeSetBeaconPacket(r)
	if err != nil {
		return err
	}
	return s.HandleSetBeaconPacket(session, primaryID, secondaryID)
}

// BroadcastBeaconBlockEntityData broadcasts ClientboundBlockEntityDataPacket
// (`0x06`, `BlockEntityTypeBeacon = 14`) for the beacon at (dim, x, y, z).
func (s *Server) BroadcastBeaconBlockEntityData(dim string, x, y, z int) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	be, ok := s.BeaconAtInDim(normDim, x, y, z)
	if !ok {
		return
	}
	pkt := EncodeBlockEntityDataPacket(x, y, z, BlockEntityTypeBeacon, EncodeBeaconBlockEntityNBT(be))
	s.BroadcastDimensionChunk(normDim, int32(x>>4), int32(z>>4), PlayBlockEntityDataCB, pkt)
}

// OnBeaconPlaced registers or updates a `minecraft:beacon` block entity at
// (dim, x, y, z) when placed by `builder` (optionally with `customName`),
// immediately evaluates its pyramid/beam state, and broadcasts its BE data.
func (s *Server) OnBeaconPlaced(builder *PlayerSession, dim string, x, y, z int, customName string) BeaconBlockEntity {
	if s == nil {
		return BeaconBlockEntity{}
	}
	normDim := world.NormalizeDimension(dim)
	bs := s.beaconState()
	bs.mu.Lock()
	be := s.getOrCreateBeaconLocked(bs, normDim, x, y, z)
	if customName != "" {
		be.CustomName = customName
	}
	if builder != nil {
		be.LastBuilder = builder
	}
	bs.mu.Unlock()

	updated, _ := s.UpdateBeaconAtInDim(normDim, x, y, z, builder)
	s.BroadcastBeaconBlockEntityData(normDim, x, y, z)
	return updated
}

// HydrateChunkBeacons inspects all `world.StructureBlockEntity` entries on the
// loaded chunk (dim, cx, cz) and registers any `minecraft:beacon` block entities.
func (s *Server) HydrateChunkBeacons(dim string, cx, cz int32) int {
	if s == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return 0
	}
	sbes := cache.LoadedChunkStructureBlockEntities(cx, cz)
	if len(sbes) == 0 {
		return 0
	}
	bs := s.beaconState()
	bs.mu.Lock()
	s.ensureBeaconsLoadedLocked(bs)
	var hydrated [][3]int
	for _, sbe := range sbes {
		if canonicalBeaconResource(sbe.ID) != "minecraft:beacon" {
			continue
		}
		key := beaconKey{dim: normDim, x: sbe.X, y: sbe.Y, z: sbe.Z}
		if _, exists := bs.beacons[key]; exists {
			continue
		}
		be := &BeaconBlockEntity{
			ID:        "minecraft:beacon",
			Dimension: normDim,
			X:         sbe.X,
			Y:         sbe.Y,
			Z:         sbe.Z,
		}
		bs.beacons[key] = be
		hydrated = append(hydrated, [3]int{sbe.X, sbe.Y, sbe.Z})
	}
	bs.mu.Unlock()

	for _, pos := range hydrated {
		s.UpdateBeaconAtInDim(normDim, pos[0], pos[1], pos[2], nil)
	}
	return len(hydrated)
}

func (s *Server) syncOpenBeacon(session *PlayerSession) error {
	if s == nil || session == nil || session.send == nil {
		return nil
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.MenuType != MenuTypeBeacon {
		tr.mu.Unlock()
		return nil
	}
	cid := tr.open.ContainerID
	bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
	pay := tr.open.Crafting[0]
	carried := tr.carried
	tr.mu.Unlock()

	dim := session.Dimension()
	if dim == "" {
		dim = world.DimensionOverworld
	}
	slots := s.buildBeaconMenuSlots(session, pay)
	stateID := session.NextContainerStateID()
	if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried)); err != nil {
		return err
	}
	return s.sendBeaconDataProperties(session, cid, dim, bx, by, bz)
}

func (s *Server) broadcastBeaconDataToViewers(dim string, bx, by, bz int) {
	normDim := world.NormalizeDimension(dim)
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()

	for _, sess := range sessions {
		if sess == nil || world.NormalizeDimension(sess.Dimension()) != normDim {
			continue
		}
		open := sess.OpenContainer()
		if open == nil || open.MenuType != MenuTypeBeacon ||
			open.BlockX != bx || open.BlockY != by || open.BlockZ != bz {
			continue
		}
		_ = s.sendBeaconDataProperties(sess, open.ContainerID, normDim, bx, by, bz)
	}
}

func (s *Server) playBeaconSound(dim string, x, y, z int, soundName string) {
	if s == nil || soundName == "" {
		return
	}
	normDim := world.NormalizeDimension(dim)
	pkt := EncodeSoundPacket(soundName, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0)
	cx := int32(x >> 4)
	cz := int32(z >> 4)

	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()

	for _, sess := range sessions {
		if sess == nil || sess.send == nil || world.NormalizeDimension(sess.Dimension()) != normDim {
			continue
		}
		snap := sess.Snapshot()
		dx := snap.X - (float64(x) + 0.5)
		dz := snap.Z - (float64(z) + 0.5)
		if dx*dx+dz*dz <= 64*64 || (int32( int(math.Floor(snap.X))>>4 ) == cx && int32( int(math.Floor(snap.Z))>>4 ) == cz) {
			_ = sess.send(PlaySoundPacketID, pkt)
		}
	}
}

func canonicalBeaconResource(id string) string {
	id = strings.TrimSpace(strings.ToLower(id))
	if id == "" {
		return ""
	}
	if !strings.Contains(id, ":") {
		return "minecraft:" + id
	}
	return id
}
