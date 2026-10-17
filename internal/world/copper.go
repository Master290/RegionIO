package world

import (
	"math/rand"
	"strings"
	"sync"
)

// CopperOxidationStage represents the 4 vanilla oxidation stages of copper blocks.
type CopperOxidationStage int

const (
	CopperStageUnaffected CopperOxidationStage = 0
	CopperStageExposed    CopperOxidationStage = 1
	CopperStageWeathered  CopperOxidationStage = 2
	CopperStageOxidized   CopperOxidationStage = 3
)

func (s CopperOxidationStage) String() string {
	switch s {
	case CopperStageUnaffected:
		return "unaffected"
	case CopperStageExposed:
		return "exposed"
	case CopperStageWeathered:
		return "weathered"
	case CopperStageOxidized:
		return "oxidized"
	default:
		return "unknown"
	}
}

// Vanilla 26.1.2 copper oxidation constants (WeatheringCopper / ChangeOverTimeBlock).
const (
	// CopperOxidationRandomTickNumerator and CopperOxidationRandomTickDenominator
	// define the vanilla pre-roll chance (64 / 1125 ≈ 0.05688889) on a random tick
	// before scanning the 4-block Manhattan neighborhood.
	CopperOxidationRandomTickNumerator   = 64
	CopperOxidationRandomTickDenominator = 1125
	CopperOxidationPreRollChance         = float64(CopperOxidationRandomTickNumerator) / float64(CopperOxidationRandomTickDenominator)

	// CopperOxidationScanDistance is the Manhattan distance (4 blocks) scanned
	// around a copper block during oxidation evaluation.
	CopperOxidationScanDistance = 4

	// CopperUnaffectedOxidationModifier is the stage multiplier (0.75) for
	// unaffected (stage 0) copper blocks; exposed and weathered use 1.0.
	CopperUnaffectedOxidationModifier         = 0.75
	CopperExposedOrWeatheredOxidationModifier = 1.0

	// Lightning deoxidation random-walk parameters (LightningBolt.clearCopperOnLightningStrike).
	LightningCopperMinWalks      = 3
	LightningCopperMaxWalks      = 5
	LightningCopperMaxWalkSteps  = 8
	LightningCopperStepAttempts  = 10
)

// CopperAxeAction classifies the result of using an axe on a copper block.
type CopperAxeAction string

const (
	CopperAxeActionNone   CopperAxeAction = ""
	CopperAxeActionUnwax  CopperAxeAction = "unwax"
	CopperAxeActionScrape CopperAxeAction = "scrape"
)

// CopperFamily defines the 4 unwaxed and 4 waxed block names of a single copper
// block family in vanilla 26.1.2.
type CopperFamily struct {
	Kind    string
	Unwaxed [4]string
	Waxed   [4]string
}

var copperFamilies = []CopperFamily{
	{
		Kind: "copper_block",
		Unwaxed: [4]string{
			"minecraft:copper_block",
			"minecraft:exposed_copper",
			"minecraft:weathered_copper",
			"minecraft:oxidized_copper",
		},
		Waxed: [4]string{
			"minecraft:waxed_copper_block",
			"minecraft:waxed_exposed_copper",
			"minecraft:waxed_weathered_copper",
			"minecraft:waxed_oxidized_copper",
		},
	},
	{
		Kind: "chiseled_copper",
		Unwaxed: [4]string{
			"minecraft:chiseled_copper",
			"minecraft:exposed_chiseled_copper",
			"minecraft:weathered_chiseled_copper",
			"minecraft:oxidized_chiseled_copper",
		},
		Waxed: [4]string{
			"minecraft:waxed_chiseled_copper",
			"minecraft:waxed_exposed_chiseled_copper",
			"minecraft:waxed_weathered_chiseled_copper",
			"minecraft:waxed_oxidized_chiseled_copper",
		},
	},
	{
		Kind: "cut_copper",
		Unwaxed: [4]string{
			"minecraft:cut_copper",
			"minecraft:exposed_cut_copper",
			"minecraft:weathered_cut_copper",
			"minecraft:oxidized_cut_copper",
		},
		Waxed: [4]string{
			"minecraft:waxed_cut_copper",
			"minecraft:waxed_exposed_cut_copper",
			"minecraft:waxed_weathered_cut_copper",
			"minecraft:waxed_oxidized_cut_copper",
		},
	},
	{
		Kind: "cut_copper_stairs",
		Unwaxed: [4]string{
			"minecraft:cut_copper_stairs",
			"minecraft:exposed_cut_copper_stairs",
			"minecraft:weathered_cut_copper_stairs",
			"minecraft:oxidized_cut_copper_stairs",
		},
		Waxed: [4]string{
			"minecraft:waxed_cut_copper_stairs",
			"minecraft:waxed_exposed_cut_copper_stairs",
			"minecraft:waxed_weathered_cut_copper_stairs",
			"minecraft:waxed_oxidized_cut_copper_stairs",
		},
	},
	{
		Kind: "cut_copper_slab",
		Unwaxed: [4]string{
			"minecraft:cut_copper_slab",
			"minecraft:exposed_cut_copper_slab",
			"minecraft:weathered_cut_copper_slab",
			"minecraft:oxidized_cut_copper_slab",
		},
		Waxed: [4]string{
			"minecraft:waxed_cut_copper_slab",
			"minecraft:waxed_exposed_cut_copper_slab",
			"minecraft:waxed_weathered_cut_copper_slab",
			"minecraft:waxed_oxidized_cut_copper_slab",
		},
	},
	{
		Kind: "copper_door",
		Unwaxed: [4]string{
			"minecraft:copper_door",
			"minecraft:exposed_copper_door",
			"minecraft:weathered_copper_door",
			"minecraft:oxidized_copper_door",
		},
		Waxed: [4]string{
			"minecraft:waxed_copper_door",
			"minecraft:waxed_exposed_copper_door",
			"minecraft:waxed_weathered_copper_door",
			"minecraft:waxed_oxidized_copper_door",
		},
	},
	{
		Kind: "copper_trapdoor",
		Unwaxed: [4]string{
			"minecraft:copper_trapdoor",
			"minecraft:exposed_copper_trapdoor",
			"minecraft:weathered_copper_trapdoor",
			"minecraft:oxidized_copper_trapdoor",
		},
		Waxed: [4]string{
			"minecraft:waxed_copper_trapdoor",
			"minecraft:waxed_exposed_copper_trapdoor",
			"minecraft:waxed_weathered_copper_trapdoor",
			"minecraft:waxed_oxidized_copper_trapdoor",
		},
	},
	{
		Kind: "copper_grate",
		Unwaxed: [4]string{
			"minecraft:copper_grate",
			"minecraft:exposed_copper_grate",
			"minecraft:weathered_copper_grate",
			"minecraft:oxidized_copper_grate",
		},
		Waxed: [4]string{
			"minecraft:waxed_copper_grate",
			"minecraft:waxed_exposed_copper_grate",
			"minecraft:waxed_weathered_copper_grate",
			"minecraft:waxed_oxidized_copper_grate",
		},
	},
	{
		Kind: "copper_bulb",
		Unwaxed: [4]string{
			"minecraft:copper_bulb",
			"minecraft:exposed_copper_bulb",
			"minecraft:weathered_copper_bulb",
			"minecraft:oxidized_copper_bulb",
		},
		Waxed: [4]string{
			"minecraft:waxed_copper_bulb",
			"minecraft:waxed_exposed_copper_bulb",
			"minecraft:waxed_weathered_copper_bulb",
			"minecraft:waxed_oxidized_copper_bulb",
		},
	},
	{
		Kind: "copper_bars",
		Unwaxed: [4]string{
			"minecraft:copper_bars",
			"minecraft:exposed_copper_bars",
			"minecraft:weathered_copper_bars",
			"minecraft:oxidized_copper_bars",
		},
		Waxed: [4]string{
			"minecraft:waxed_copper_bars",
			"minecraft:waxed_exposed_copper_bars",
			"minecraft:waxed_weathered_copper_bars",
			"minecraft:waxed_oxidized_copper_bars",
		},
	},
	{
		Kind: "copper_chain",
		Unwaxed: [4]string{
			"minecraft:copper_chain",
			"minecraft:exposed_copper_chain",
			"minecraft:weathered_copper_chain",
			"minecraft:oxidized_copper_chain",
		},
		Waxed: [4]string{
			"minecraft:waxed_copper_chain",
			"minecraft:waxed_exposed_copper_chain",
			"minecraft:waxed_weathered_copper_chain",
			"minecraft:waxed_oxidized_copper_chain",
		},
	},
	{
		Kind: "copper_lantern",
		Unwaxed: [4]string{
			"minecraft:copper_lantern",
			"minecraft:exposed_copper_lantern",
			"minecraft:weathered_copper_lantern",
			"minecraft:oxidized_copper_lantern",
		},
		Waxed: [4]string{
			"minecraft:waxed_copper_lantern",
			"minecraft:waxed_exposed_copper_lantern",
			"minecraft:waxed_weathered_copper_lantern",
			"minecraft:waxed_oxidized_copper_lantern",
		},
	},
	{
		Kind: "copper_chest",
		Unwaxed: [4]string{
			"minecraft:copper_chest",
			"minecraft:exposed_copper_chest",
			"minecraft:weathered_copper_chest",
			"minecraft:oxidized_copper_chest",
		},
		Waxed: [4]string{
			"minecraft:waxed_copper_chest",
			"minecraft:waxed_exposed_copper_chest",
			"minecraft:waxed_weathered_copper_chest",
			"minecraft:waxed_oxidized_copper_chest",
		},
	},
	{
		Kind: "copper_golem_statue",
		Unwaxed: [4]string{
			"minecraft:copper_golem_statue",
			"minecraft:exposed_copper_golem_statue",
			"minecraft:weathered_copper_golem_statue",
			"minecraft:oxidized_copper_golem_statue",
		},
		Waxed: [4]string{
			"minecraft:waxed_copper_golem_statue",
			"minecraft:waxed_exposed_copper_golem_statue",
			"minecraft:waxed_weathered_copper_golem_statue",
			"minecraft:waxed_oxidized_copper_golem_statue",
		},
	},
	{
		Kind: "lightning_rod",
		Unwaxed: [4]string{
			"minecraft:lightning_rod",
			"minecraft:exposed_lightning_rod",
			"minecraft:weathered_lightning_rod",
			"minecraft:oxidized_lightning_rod",
		},
		Waxed: [4]string{
			"minecraft:waxed_lightning_rod",
			"minecraft:waxed_exposed_lightning_rod",
			"minecraft:waxed_weathered_lightning_rod",
			"minecraft:waxed_oxidized_lightning_rod",
		},
	},
}

type copperLookupEntry struct {
	family *CopperFamily
	stage  CopperOxidationStage
	waxed  bool
}

var (
	copperLookupOnce sync.Once
	copperByName     map[string]copperLookupEntry
)

func initCopperLookup() {
	copperByName = make(map[string]copperLookupEntry, len(copperFamilies)*8)
	for i := range copperFamilies {
		fam := &copperFamilies[i]
		for stage := CopperStageUnaffected; stage <= CopperStageOxidized; stage++ {
			copperByName[fam.Unwaxed[stage]] = copperLookupEntry{
				family: fam,
				stage:  stage,
				waxed:  false,
			}
			copperByName[fam.Waxed[stage]] = copperLookupEntry{
				family: fam,
				stage:  stage,
				waxed:  true,
			}
		}
	}
}

// AllCopperFamilies returns a copy of all 15 oxidizable/waxable copper block families in 26.1.2.
func AllCopperFamilies() []CopperFamily {
	out := make([]CopperFamily, len(copperFamilies))
	copy(out, copperFamilies)
	return out
}

// ClassifyCopperBlockName returns the copper family, oxidation stage (0..3), and
// waxed status for a full block name (e.g. "minecraft:exposed_copper_bulb").
func ClassifyCopperBlockName(name string) (*CopperFamily, CopperOxidationStage, bool, bool) {
	copperLookupOnce.Do(initCopperLookup)
	if !strings.HasPrefix(name, "minecraft:") && name != "" {
		name = "minecraft:" + name
	}
	entry, ok := copperByName[name]
	if !ok {
		return nil, 0, false, false
	}
	return entry.family, entry.stage, entry.waxed, true
}

// ClassifyCopperBlock returns the copper family, oxidation stage (0..3), and
// waxed status for a block state ID.
func ClassifyCopperBlock(state uint16) (*CopperFamily, CopperOxidationStage, bool, bool) {
	info, ok := stateByID(state)
	if !ok {
		return nil, 0, false, false
	}
	return ClassifyCopperBlockName(info.Name)
}

// IsCopperBlock reports whether state belongs to any of the 15 oxidizable/waxable
// copper block families or is a copper torch (`copper_torch`, `copper_wall_torch`).
func IsCopperBlock(state uint16) bool {
	if _, _, _, ok := ClassifyCopperBlock(state); ok {
		return true
	}
	return IsCopperTorchBlock(state)
}

// IsWaxedCopperBlock reports whether state is a waxed copper block (`waxed_*`).
func IsWaxedCopperBlock(state uint16) bool {
	_, _, waxed, ok := ClassifyCopperBlock(state)
	return ok && waxed
}

// IsUnwaxedCopperBlock reports whether state is an unwaxed oxidizable copper block.
func IsUnwaxedCopperBlock(state uint16) bool {
	_, _, waxed, ok := ClassifyCopperBlock(state)
	return ok && !waxed
}

// CanCopperBlockOxidize reports whether state is an unwaxed copper block with
// oxidation stage < CopperStageOxidized (unaffected, exposed, or weathered).
func CanCopperBlockOxidize(state uint16) bool {
	_, stage, waxed, ok := ClassifyCopperBlock(state)
	return ok && !waxed && stage < CopperStageOxidized
}

// CopperOxidationStageOf returns the oxidation stage (0..3) of state.
func CopperOxidationStageOf(state uint16) (CopperOxidationStage, bool) {
	_, stage, _, ok := ClassifyCopperBlock(state)
	return stage, ok
}

// CopperBlockStateWithStageAndWax converts a copper block state to the specified
// oxidation stage and waxed status within the same copper family while preserving
// all block state properties (facing, lit, powered, open, half, hinge, waterlogged, etc.).
func CopperBlockStateWithStageAndWax(state uint16, stage CopperOxidationStage, waxed bool) (uint16, bool) {
	if stage < CopperStageUnaffected || stage > CopperStageOxidized {
		return 0, false
	}
	info, ok := stateByID(state)
	if !ok {
		return 0, false
	}
	fam, _, _, ok := ClassifyCopperBlockName(info.Name)
	if !ok {
		return 0, false
	}
	targetName := fam.Unwaxed[stage]
	if waxed {
		targetName = fam.Waxed[stage]
	}
	nextState, ok := nameToStateID(targetName, info.Properties)
	if !ok || nextState == StateAir {
		return 0, false
	}
	return nextState, true
}

// WaxCopperBlockState returns the waxed counterpart of an unwaxed copper block
// state at the same oxidation stage, preserving all block state properties.
func WaxCopperBlockState(state uint16) (uint16, bool) {
	_, stage, waxed, ok := ClassifyCopperBlock(state)
	if !ok || waxed {
		return 0, false
	}
	return CopperBlockStateWithStageAndWax(state, stage, true)
}

// UnwaxCopperBlockState returns the unwaxed counterpart of a waxed copper block
// state at the same oxidation stage, preserving all block state properties.
func UnwaxCopperBlockState(state uint16) (uint16, bool) {
	_, stage, waxed, ok := ClassifyCopperBlock(state)
	if !ok || !waxed {
		return 0, false
	}
	return CopperBlockStateWithStageAndWax(state, stage, false)
}

// OxidizeCopperBlockState advances an unwaxed copper block state by one oxidation
// stage (unaffected -> exposed -> weathered -> oxidized), preserving all properties.
func OxidizeCopperBlockState(state uint16) (uint16, bool) {
	_, stage, waxed, ok := ClassifyCopperBlock(state)
	if !ok || waxed || stage >= CopperStageOxidized {
		return 0, false
	}
	return CopperBlockStateWithStageAndWax(state, stage+1, false)
}

// ScrapeCopperBlockState removes one oxidation stage from an unwaxed oxidized
// copper block state (oxidized -> weathered -> exposed -> unaffected), preserving all properties.
func ScrapeCopperBlockState(state uint16) (uint16, bool) {
	_, stage, waxed, ok := ClassifyCopperBlock(state)
	if !ok || waxed || stage <= CopperStageUnaffected {
		return 0, false
	}
	return CopperBlockStateWithStageAndWax(state, stage-1, false)
}

// DeoxidizeCopperBlockStateFully resets an unwaxed oxidized copper block state
// directly to CopperStageUnaffected (stage 0), preserving all properties.
func DeoxidizeCopperBlockStateFully(state uint16) (uint16, bool) {
	_, stage, waxed, ok := ClassifyCopperBlock(state)
	if !ok || waxed || stage <= CopperStageUnaffected {
		return 0, false
	}
	return CopperBlockStateWithStageAndWax(state, CopperStageUnaffected, false)
}

// --- Random-Tick Oxidation & Neighborhood Formula ---

// ComputeCopperOxidationChance calculates the vanilla conditional probability
// P = m * ((b + 1) / (a + 1))^2 that an unwaxed copper block at (x, y, z)
// advances to its next oxidation stage after passing the 64/1125 random-tick
// pre-roll:
//   - Scans all positions within Manhattan distance <= 4 of (x, y, z).
//   - Waxed copper blocks are ignored.
//   - If any unwaxed copper block in the neighborhood has stage < myStage,
//     returns 0 (oxidation is blocked).
//   - Otherwise, let `a` be the number of unwaxed copper neighbors with
//     stage >= myStage, and `b` be the number with stage > myStage.
//   - Let c = (b + 1) / (a + 1), and m = 0.75 for unaffected (stage 0) or 1.0
//     for exposed/weathered (stages 1..2). Returns m * c * c.
func ComputeCopperOxidationChance(c *Cache, x, y, z int) float64 {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return 0
	}
	state := c.GetBlock(x, y, z)
	fam, myStage, waxed, ok := ClassifyCopperBlock(state)
	if !ok || waxed || myStage >= CopperStageOxidized {
		return 0
	}
	info, _ := stateByID(state)

	// If this is a copper door, determine its partner half so we don't count the
	// other half of the same door as a separate neighbor.
	var skipPartner BlockPos
	hasSkipPartner := false
	if fam.Kind == "copper_door" {
		if info.Properties["half"] == "lower" {
			skipPartner = BlockPos{X: x, Y: y + 1, Z: z}
			hasSkipPartner = true
		} else if info.Properties["half"] == "upper" {
			skipPartner = BlockPos{X: x, Y: y - 1, Z: z}
			hasSkipPartner = true
		}
	}

	a := 0 // unwaxed neighbors with stage >= myStage
	b := 0 // unwaxed neighbors with stage > myStage

	for dy := -CopperOxidationScanDistance; dy <= CopperOxidationScanDistance; dy++ {
		ny := y + dy
		if ny < MinY || ny >= MinY+WorldHeight {
			continue
		}
		remY := CopperOxidationScanDistance - absInt(dy)
		for dz := -remY; dz <= remY; dz++ {
			nz := z + dz
			remZ := remY - absInt(dz)
			for dx := -remZ; dx <= remZ; dx++ {
				if dx == 0 && dy == 0 && dz == 0 {
					continue
				}
				nx := x + dx
				if hasSkipPartner && nx == skipPartner.X && ny == skipPartner.Y && nz == skipPartner.Z {
					continue
				}
				nState := c.GetBlock(nx, ny, nz)
				if nState == StateAir {
					continue
				}
				nFam, nStage, nWaxed, nOK := ClassifyCopperBlock(nState)
				if !nOK || nWaxed {
					continue
				}
				// Avoid double-counting the upper half of another copper door when its lower half is also in range.
				if nFam.Kind == "copper_door" {
					if nInfo, ok := stateByID(nState); ok && nInfo.Properties["half"] == "upper" {
						if absInt(dx)+absInt(dy-1)+absInt(dz) <= CopperOxidationScanDistance {
							continue
						}
					}
				}
				if nStage < myStage {
					return 0
				}
				if nStage > myStage {
					b++
				}
				a++
			}
		}
	}

	ratio := float64(b+1) / float64(a+1)
	modifier := CopperExposedOrWeatheredOxidationModifier
	if myStage == CopperStageUnaffected {
		modifier = CopperUnaffectedOxidationModifier
	}
	return modifier * ratio * ratio
}

// OxidizeCopperAt advances the unwaxed copper block at (x, y, z) by one
// oxidation stage (updating both halves if it is a copper door, or both halves
// of a paired copper chest) and records all block & lighting changes.
func (c *Cache) OxidizeCopperAt(x, y, z int) (uint16, BlockUpdateResult, bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return 0, BlockUpdateResult{}, false
	}
	state := c.GetBlock(x, y, z)
	nextState, ok := OxidizeCopperBlockState(state)
	if !ok {
		return state, BlockUpdateResult{}, false
	}
	applied, res := c.ApplyBlockWithUpdates(x, y, z, nextState)
	if !applied {
		return state, res, false
	}
	return nextState, res, true
}

// StepCopperOxidationNeighborhood evaluates the neighborhood oxidation chance
// ComputeCopperOxidationChance(c, x, y, z) (assuming the 64/1125 pre-roll has
// already succeeded) and oxidizes the block if rng.Float64() < chance.
func (c *Cache) StepCopperOxidationNeighborhood(x, y, z int, rng *rand.Rand) (uint16, BlockUpdateResult, bool) {
	chance := ComputeCopperOxidationChance(c, x, y, z)
	if chance <= 0 {
		return 0, BlockUpdateResult{}, false
	}
	var roll float64
	if rng != nil {
		roll = rng.Float64()
	} else {
		roll = rand.Float64()
	}
	if roll >= chance {
		return c.GetBlock(x, y, z), BlockUpdateResult{}, false
	}
	return c.OxidizeCopperAt(x, y, z)
}

// TryOxidizeCopperOnRandomTick executes a full vanilla random-tick oxidation
// attempt at (x, y, z):
//  1. Checks that the block is an unwaxed copper block with stage < oxidized
//     (and for a copper_door, only the lower half ticks randomly).
//  2. Rolls the 64/1125 (0.05688889) pre-roll.
//  3. Evaluates the 4-block Manhattan neighborhood formula and advances the stage on success.
func (c *Cache) TryOxidizeCopperOnRandomTick(x, y, z int, rng *rand.Rand) (uint16, BlockUpdateResult, bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return 0, BlockUpdateResult{}, false
	}
	state := c.GetBlock(x, y, z)
	fam, stage, waxed, ok := ClassifyCopperBlock(state)
	if !ok || waxed || stage >= CopperStageOxidized {
		return state, BlockUpdateResult{}, false
	}
	// Vanilla WeatheringCopperDoorBlock only runs randomTick on DoubleBlockHalf.LOWER.
	if fam.Kind == "copper_door" {
		if info, ok := stateByID(state); ok && info.Properties["half"] == "upper" {
			return state, BlockUpdateResult{}, false
		}
	}
	var preRoll float64
	if rng != nil {
		preRoll = rng.Float64()
	} else {
		preRoll = rand.Float64()
	}
	if preRoll >= CopperOxidationPreRollChance {
		return state, BlockUpdateResult{}, false
	}
	return c.StepCopperOxidationNeighborhood(x, y, z, rng)
}

// --- Waxing, Unwaxing, Scraping, and Lightning Strike ---

// WaxCopperAt waxes the unwaxed copper block at (x, y, z) (updating both halves
// if it is a copper door).
func (c *Cache) WaxCopperAt(x, y, z int) (uint16, BlockUpdateResult, bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return 0, BlockUpdateResult{}, false
	}
	state := c.GetBlock(x, y, z)
	nextState, ok := WaxCopperBlockState(state)
	if !ok {
		return state, BlockUpdateResult{}, false
	}
	applied, res := c.ApplyBlockWithUpdates(x, y, z, nextState)
	if !applied {
		return state, res, false
	}
	return nextState, res, true
}

// UnwaxCopperAt removes wax from the waxed copper block at (x, y, z) (updating
// both halves if it is a copper door).
func (c *Cache) UnwaxCopperAt(x, y, z int) (uint16, BlockUpdateResult, bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return 0, BlockUpdateResult{}, false
	}
	state := c.GetBlock(x, y, z)
	nextState, ok := UnwaxCopperBlockState(state)
	if !ok {
		return state, BlockUpdateResult{}, false
	}
	applied, res := c.ApplyBlockWithUpdates(x, y, z, nextState)
	if !applied {
		return state, res, false
	}
	return nextState, res, true
}

// ScrapeCopperAt scrapes one oxidation stage off the unwaxed copper block at
// (x, y, z) (updating both halves if it is a copper door).
func (c *Cache) ScrapeCopperAt(x, y, z int) (uint16, BlockUpdateResult, bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return 0, BlockUpdateResult{}, false
	}
	state := c.GetBlock(x, y, z)
	nextState, ok := ScrapeCopperBlockState(state)
	if !ok {
		return state, BlockUpdateResult{}, false
	}
	applied, res := c.ApplyBlockWithUpdates(x, y, z, nextState)
	if !applied {
		return state, res, false
	}
	return nextState, res, true
}

// UseAxeOnCopperAt applies an axe interaction to the copper block at (x, y, z):
//   - If waxed, removes the wax (`CopperAxeActionUnwax`).
//   - Else if oxidized (stage > 0), scrapes one oxidation stage (`CopperAxeActionScrape`).
func (c *Cache) UseAxeOnCopperAt(x, y, z int) (uint16, CopperAxeAction, BlockUpdateResult, bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return 0, CopperAxeActionNone, BlockUpdateResult{}, false
	}
	state := c.GetBlock(x, y, z)
	if IsWaxedCopperBlock(state) {
		nextState, res, ok := c.UnwaxCopperAt(x, y, z)
		if ok {
			return nextState, CopperAxeActionUnwax, res, true
		}
		return state, CopperAxeActionNone, res, false
	}
	nextState, res, ok := c.ScrapeCopperAt(x, y, z)
	if ok {
		return nextState, CopperAxeActionScrape, res, true
	}
	return state, CopperAxeActionNone, BlockUpdateResult{}, false
}

// CleanCopperOnLightningStrike implements vanilla LightningBolt.clearCopperOnLightningStrike
// when lightning strikes at (x, y, z):
//  1. If (x, y, z) is a lightning rod (any of the 8 variants), powers the rod for
//     8 ticks, deoxidizes the rod itself to `minecraft:lightning_rod` if it was
//     unwaxed & oxidized, and selects the attached support block (pos - facing)
//     as the deoxidation start position.
//  2. If the start block is an unwaxed oxidized copper block, it is deoxidized
//     completely to CopperStageUnaffected (stage 0).
//  3. Runs 3..5 random walks of 1..8 steps each in 3x3x3 cubes from the start
//     block, scraping one oxidation stage off each unwaxed oxidized copper block
//     encountered along the walks.
func (c *Cache) CleanCopperOnLightningStrike(x, y, z int, rng *rand.Rand) ([]BlockPos, BlockUpdateResult) {
	var res BlockUpdateResult
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return nil, res
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(int64(x)*73856093 ^ int64(y)*19349663 ^ int64(z)*83492791))
	}

	var cleaned []BlockPos
	cleanedSet := make(map[BlockPos]bool)
	recordCleaned := func(p BlockPos) {
		if !cleanedSet[p] {
			cleanedSet[p] = true
			cleaned = append(cleaned, p)
		}
	}

	strikeState := c.GetBlock(x, y, z)
	startPos := BlockPos{X: x, Y: y, Z: z}

	if IsLightningRodBlock(strikeState) {
		curRod := strikeState
		if deoxRod, ok := DeoxidizeCopperBlockStateFully(curRod); ok {
			curRod = deoxRod
			recordCleaned(BlockPos{X: x, Y: y, Z: z})
		}
		if poweredRod, ok := withStateProperty(curRod, "powered", "true"); ok {
			curRod = poweredRod
		}
		if c.setBlockRecorded(x, y, z, curRod, &res) {
			c.propagateRedstoneAround(x, y, z, strikeState, curRod, &res)
		}
		c.ScheduleBlockUpdate(x, y, z, UpdateRedstone, LightningRodTickDelay)

		if info, ok := stateByID(curRod); ok {
			if dx, dy, dz, ok := directionOffset3D(oppositeDirection(info.Properties["facing"])); ok {
				if y+dy >= MinY && y+dy < MinY+WorldHeight {
					startPos = BlockPos{X: x + dx, Y: y + dy, Z: z + dz}
				}
			}
		}
	} else if !IsUnwaxedCopperBlock(strikeState) && y > MinY && IsUnwaxedCopperBlock(c.GetBlock(x, y-1, z)) {
		startPos = BlockPos{X: x, Y: y - 1, Z: z}
	}

	startState := c.GetBlock(startPos.X, startPos.Y, startPos.Z)
	if fullClean, ok := DeoxidizeCopperBlockStateFully(startState); ok {
		if applied, subRes := c.ApplyBlockWithUpdates(startPos.X, startPos.Y, startPos.Z, fullClean); applied {
			res.merge(subRes)
			recordCleaned(startPos)
		}
	}

	walkCount := rng.Intn(LightningCopperMaxWalks-LightningCopperMinWalks+1) + LightningCopperMinWalks
	for w := 0; w < walkCount; w++ {
		cur := startPos
		steps := rng.Intn(LightningCopperMaxWalkSteps) + 1
		for s := 0; s < steps; s++ {
			foundStep := false
			for attempt := 0; attempt < LightningCopperStepAttempts; attempt++ {
				nx := cur.X + rng.Intn(3) - 1
				ny := cur.Y + rng.Intn(3) - 1
				nz := cur.Z + rng.Intn(3) - 1
				if ny < MinY || ny >= MinY+WorldHeight {
					continue
				}
				if nx == cur.X && ny == cur.Y && nz == cur.Z {
					continue
				}
				if _, subRes, ok := c.ScrapeCopperAt(nx, ny, nz); ok {
					res.merge(subRes)
					cur = BlockPos{X: nx, Y: ny, Z: nz}
					recordCleaned(cur)
					foundStep = true
					break
				}
			}
			if !foundStep {
				break
			}
		}
	}

	return cleaned, res
}

// --- Copper Bulb (Rising-Edge Toggle, Light by Stage, Comparator Signal) ---

// IsCopperBulbBlock reports whether state is any of the 8 copper_bulb variants.
func IsCopperBulbBlock(state uint16) bool {
	fam, _, _, ok := ClassifyCopperBlock(state)
	return ok && fam.Kind == "copper_bulb"
}

// CopperBulbLightLevel returns the vanilla block light emission (0, 4, 8, 12, or 15)
// for a copper bulb state:
//   - lit="false" -> 0
//   - lit="true": unaffected=15, exposed=12, weathered=8, oxidized=4.
func CopperBulbLightLevel(state uint16) byte {
	info, ok := stateByID(state)
	if !ok || info.Properties["lit"] != "true" {
		return 0
	}
	fam, stage, _, ok := ClassifyCopperBlockName(info.Name)
	if !ok || fam.Kind != "copper_bulb" {
		return 0
	}
	switch stage {
	case CopperStageUnaffected:
		return 15
	case CopperStageExposed:
		return 12
	case CopperStageWeathered:
		return 8
	case CopperStageOxidized:
		return 4
	default:
		return 0
	}
}

// CopperBulbComparatorSignal returns the comparator analog output signal for a
// copper bulb state: 15 when lit="true", 0 when lit="false".
func CopperBulbComparatorSignal(state uint16) int {
	if !IsCopperBulbBlock(state) {
		return 0
	}
	if info, ok := stateByID(state); ok && info.Properties["lit"] == "true" {
		return MaxRedstonePower
	}
	return 0
}

// EvaluateCopperBulbRedstoneAt evaluates the redstone power state of a copper
// bulb at (x, y, z):
//   - On a rising edge (powered: false -> true), flips `lit` and sets `powered="true"`.
//   - On a falling edge (powered: true -> false), keeps `lit` unchanged and sets `powered="false"`.
func (c *Cache) EvaluateCopperBulbRedstoneAt(x, y, z int) (toggledLit bool, nowPowered bool, res BlockUpdateResult, changed bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false, false, res, false
	}
	state := c.GetBlock(x, y, z)
	if !IsCopperBulbBlock(state) {
		return false, false, res, false
	}
	info, ok := stateByID(state)
	if !ok {
		return false, false, res, false
	}
	powered := c.IsBlockPowered(x, y, z)
	wasPowered := info.Properties["powered"] == "true"
	if powered == wasPowered {
		return false, powered, res, false
	}

	nextLit := info.Properties["lit"]
	if !wasPowered && powered {
		toggledLit = true
		if nextLit == "true" {
			nextLit = "false"
		} else {
			nextLit = "true"
		}
	}
	nextPowered := "false"
	if powered {
		nextPowered = "true"
	}
	nextState, ok := nameToStateID(info.Name, map[string]string{
		"lit":     nextLit,
		"powered": nextPowered,
	})
	if !ok || nextState == state {
		return false, powered, res, false
	}
	if c.setBlockRecorded(x, y, z, nextState, &res) {
		c.syncComparatorsAroundCopperBlock(x, y, z, &res)
		return toggledLit, powered, res, true
	}
	return false, powered, res, false
}

// --- Copper Grate, Doors, Trapdoors, Lanterns, Torches, Chains, Bars, Chest, Statue ---

// IsCopperGrateBlock reports whether state is any of the 8 copper_grate variants.
func IsCopperGrateBlock(state uint16) bool {
	fam, _, _, ok := ClassifyCopperBlock(state)
	return ok && fam.Kind == "copper_grate"
}

// IsCopperGrateTranslucent reports whether state is a copper grate that lets
// light pass through with 0 opacity, does not suffocate mobs, and does not
// conduct redstone signals.
func IsCopperGrateTranslucent(state uint16) bool {
	return IsCopperGrateBlock(state) && lightOpacity(state) == 0
}

// CanCopperGrateSuffocate reports whether a copper grate suffocates entities inside it (always false).
func CanCopperGrateSuffocate(state uint16) bool {
	return false
}

// IsCopperGrateRedstoneConductor reports whether a copper grate conducts redstone power (always false).
func IsCopperGrateRedstoneConductor(state uint16) bool {
	return false
}

// IsCopperDoorBlock reports whether state is any of the 8 copper_door variants.
func IsCopperDoorBlock(state uint16) bool {
	fam, _, _, ok := ClassifyCopperBlock(state)
	return ok && fam.Kind == "copper_door"
}

// IsCopperTrapdoorBlock reports whether state is any of the 8 copper_trapdoor variants.
func IsCopperTrapdoorBlock(state uint16) bool {
	fam, _, _, ok := ClassifyCopperBlock(state)
	return ok && fam.Kind == "copper_trapdoor"
}

// PlaceCopperDoorAt places both the lower (x, y, z) and upper (x, y+1, z) halves
// of a copper door variant (`doorName`, e.g. "minecraft:copper_door").
func (c *Cache) PlaceCopperDoorAt(x, y, z int, doorName, facing, hinge string) (bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if c == nil || y < MinY || y+1 >= MinY+WorldHeight {
		return false, res
	}
	if !strings.HasPrefix(doorName, "minecraft:") {
		doorName = "minecraft:" + doorName
	}
	fam, _, _, ok := ClassifyCopperBlockName(doorName)
	if !ok || fam.Kind != "copper_door" {
		return false, res
	}
	if facing == "" {
		facing = "north"
	}
	if hinge == "" {
		hinge = "left"
	}
	powered := c.IsBlockPowered(x, y, z) || c.IsBlockPowered(x, y+1, z)
	powStr := "false"
	if powered {
		powStr = "true"
	}
	lowerState, ok1 := nameToStateID(doorName, map[string]string{
		"facing":  facing,
		"half":    "lower",
		"hinge":   hinge,
		"open":    powStr,
		"powered": powStr,
	})
	upperState, ok2 := nameToStateID(doorName, map[string]string{
		"facing":  facing,
		"half":    "upper",
		"hinge":   hinge,
		"open":    powStr,
		"powered": powStr,
	})
	if !ok1 || !ok2 {
		return false, res
	}
	c.setBlockRecorded(x, y, z, lowerState, &res)
	c.setBlockRecorded(x, y+1, z, upperState, &res)
	return true, res
}

// ToggleCopperDoorOrTrapdoor toggles the open state of a copper door (updating
// both halves) or a copper trapdoor at (x, y, z) on player interaction.
func (c *Cache) ToggleCopperDoorOrTrapdoor(x, y, z int) (open bool, res BlockUpdateResult, ok bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false, res, false
	}
	state := c.GetBlock(x, y, z)
	if IsCopperDoorBlock(state) {
		changed, r := c.ToggleDoor(x, y, z)
		if !changed {
			return false, r, false
		}
		nowOpen := BlockProperties(c.GetBlock(x, y, z))["open"] == "true"
		return nowOpen, r, true
	}
	if IsCopperTrapdoorBlock(state) {
		changed, r := c.ToggleTrapdoor(x, y, z)
		if !changed {
			return false, r, false
		}
		nowOpen := BlockProperties(c.GetBlock(x, y, z))["open"] == "true"
		return nowOpen, r, true
	}
	return false, res, false
}

// IsCopperLanternBlock reports whether state is any of the 8 copper_lantern variants.
func IsCopperLanternBlock(state uint16) bool {
	fam, _, _, ok := ClassifyCopperBlock(state)
	return ok && fam.Kind == "copper_lantern"
}

// CopperLanternLightLevel returns the block light emission (15) of a copper lantern.
func CopperLanternLightLevel(state uint16) byte {
	if !IsCopperLanternBlock(state) {
		return 0
	}
	return 15
}

// IsCopperTorchBlock reports whether state is `minecraft:copper_torch` or `minecraft:copper_wall_torch`.
func IsCopperTorchBlock(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	return info.Name == "minecraft:copper_torch" || info.Name == "minecraft:copper_wall_torch"
}

// CopperTorchLightLevel returns the block light emission (14) of a copper torch.
func CopperTorchLightLevel(state uint16) byte {
	if !IsCopperTorchBlock(state) {
		return 0
	}
	return 14
}

// IsCopperChainBlock reports whether state is any of the 8 copper_chain variants.
func IsCopperChainBlock(state uint16) bool {
	fam, _, _, ok := ClassifyCopperBlock(state)
	return ok && fam.Kind == "copper_chain"
}

// IsChainBlock reports whether state is an iron chain or any copper chain variant.
func IsChainBlock(state uint16) bool {
	if IsCopperChainBlock(state) {
		return true
	}
	info, ok := stateByID(state)
	return ok && (info.Name == "minecraft:chain" || info.Name == "minecraft:iron_chain")
}

// IsCopperBarsBlock reports whether state is any of the 8 copper_bars variants.
func IsCopperBarsBlock(state uint16) bool {
	fam, _, _, ok := ClassifyCopperBlock(state)
	return ok && fam.Kind == "copper_bars"
}

func canCopperBarsConnectTo(neighborState uint16) bool {
	if isAirState(neighborState) {
		return false
	}
	if IsCopperBarsBlock(neighborState) {
		return true
	}
	info, ok := stateByID(neighborState)
	if !ok {
		return false
	}
	if info.Name == "minecraft:iron_bars" || strings.HasSuffix(info.Name, "_glass_pane") ||
		info.Name == "minecraft:glass_pane" || strings.HasSuffix(info.Name, "_wall") {
		return true
	}
	return isSturdySupportBlock(neighborState)
}

// ResolveCopperBarsStateAt computes the north, south, east, west connection
// properties of a copper_bars block at (x, y, z) from its 4 horizontal neighbors.
func (c *Cache) ResolveCopperBarsStateAt(x, y, z int, baseState uint16) uint16 {
	if c == nil || !IsCopperBarsBlock(baseState) {
		return baseState
	}
	info, ok := stateByID(baseState)
	if !ok {
		return baseState
	}
	boolStr := func(b bool) string {
		if b {
			return "true"
		}
		return "false"
	}
	props := map[string]string{
		"north":       boolStr(canCopperBarsConnectTo(c.GetBlock(x, y, z-1))),
		"south":       boolStr(canCopperBarsConnectTo(c.GetBlock(x, y, z+1))),
		"west":        boolStr(canCopperBarsConnectTo(c.GetBlock(x-1, y, z))),
		"east":        boolStr(canCopperBarsConnectTo(c.GetBlock(x+1, y, z))),
		"waterlogged": info.Properties["waterlogged"],
	}
	if props["waterlogged"] == "" {
		props["waterlogged"] = "false"
	}
	if resolved, ok := nameToStateID(info.Name, props); ok {
		return resolved
	}
	return baseState
}

// IsCopperChestBlock reports whether state is any of the 8 copper_chest variants.
func IsCopperChestBlock(state uint16) bool {
	fam, _, _, ok := ClassifyCopperBlock(state)
	return ok && fam.Kind == "copper_chest"
}

// PartnerCopperChestPos returns the partner block coordinate of a double copper
// chest (`type="left"` or `type="right"`). For a single chest, returns false.
func PartnerCopperChestPos(x, y, z int, state uint16) (BlockPos, bool) {
	if !IsCopperChestBlock(state) {
		return BlockPos{}, false
	}
	info, ok := stateByID(state)
	if !ok {
		return BlockPos{}, false
	}
	chestType := info.Properties["type"]
	facing := info.Properties["facing"]
	// Vanilla ChestBlock.getConnectedDirection:
	// when facing north (0, -1): "left" connects east (+X), "right" connects west (-X).
	var dx, dz int
	switch facing {
	case "north":
		if chestType == "left" {
			dx = 1
		} else if chestType == "right" {
			dx = -1
		}
	case "south":
		if chestType == "left" {
			dx = -1
		} else if chestType == "right" {
			dx = 1
		}
	case "west":
		if chestType == "left" {
			dz = -1
		} else if chestType == "right" {
			dz = 1
		}
	case "east":
		if chestType == "left" {
			dz = 1
		} else if chestType == "right" {
			dz = -1
		}
	}
	if dx == 0 && dz == 0 {
		return BlockPos{}, false
	}
	return BlockPos{X: x + dx, Y: y, Z: z + dz}, true
}

// IsCopperGolemStatueBlock reports whether state is any of the 8 copper_golem_statue variants.
func IsCopperGolemStatueBlock(state uint16) bool {
	fam, _, _, ok := ClassifyCopperBlock(state)
	return ok && fam.Kind == "copper_golem_statue"
}

// CopperGolemStatueComparatorSignal returns the analog comparator output signal
// (1..4) of a copper_golem_statue based on its `copper_golem_pose` property:
//   - "standing" -> 1
//   - "sitting"  -> 2
//   - "running"  -> 3
//   - "star"     -> 4
func CopperGolemStatueComparatorSignal(state uint16) int {
	if !IsCopperGolemStatueBlock(state) {
		return 0
	}
	info, ok := stateByID(state)
	if !ok {
		return 0
	}
	switch info.Properties["copper_golem_pose"] {
	case "standing":
		return 1
	case "sitting":
		return 2
	case "running":
		return 3
	case "star":
		return 4
	default:
		return 1
	}
}

// CycleCopperGolemStatuePoseAt cycles the `copper_golem_pose` property of a
// copper_golem_statue at (x, y, z) (`standing -> sitting -> running -> star -> standing`)
// and updates any adjacent redstone comparators.
func (c *Cache) CycleCopperGolemStatuePoseAt(x, y, z int) (string, BlockUpdateResult, bool) {
	var res BlockUpdateResult
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return "", res, false
	}
	state := c.GetBlock(x, y, z)
	if !IsCopperGolemStatueBlock(state) {
		return "", res, false
	}
	info, ok := stateByID(state)
	if !ok {
		return "", res, false
	}
	var nextPose string
	switch info.Properties["copper_golem_pose"] {
	case "standing":
		nextPose = "sitting"
	case "sitting":
		nextPose = "running"
	case "running":
		nextPose = "star"
	default:
		nextPose = "standing"
	}
	nextState, ok := withStateProperty(state, "copper_golem_pose", nextPose)
	if !ok {
		return "", res, false
	}
	applied, r := c.ApplyBlockWithUpdates(x, y, z, nextState)
	if !applied {
		return "", r, false
	}
	c.syncComparatorsAroundCopperBlock(x, y, z, &r)
	return nextPose, r, true
}

// IsLightningRodBlock reports whether state is any of the 8 lightning_rod variants
// (unaffected, exposed, weathered, oxidized, and their waxed counterparts).
func IsLightningRodBlock(state uint16) bool {
	fam, _, _, ok := ClassifyCopperBlock(state)
	return ok && fam.Kind == "lightning_rod"
}

// FindNearestCopperLightningRod searches loaded chunks within `radius` (defaulting
// to LightningRodRedirectRadius = 128) for the nearest lightning rod of any of
// the 8 oxidation/wax variants.
func (c *Cache) FindNearestCopperLightningRod(centerX, centerY, centerZ, radius int) (BlockPos, bool) {
	if c == nil {
		return BlockPos{}, false
	}
	if radius <= 0 {
		radius = LightningRodRedirectRadius
	}
	_ = c.GetBlock(centerX, maxInt(c.minY(), minInt(c.maxY()-1, centerY)), centerZ)

	minX, maxX := centerX-radius, centerX+radius
	minY, maxY := maxInt(c.minY(), centerY-radius), minInt(c.maxY()-1, centerY+radius)
	minZ, maxZ := centerZ-radius, centerZ+radius
	minCX, maxCX := int32(minX>>4), int32(maxX>>4)
	minCZ, maxCZ := int32(minZ>>4), int32(maxZ>>4)

	c.mu.Lock()
	candidates := make([]*Chunk, 0, len(c.chunks))
	for key, ch := range c.chunks {
		if ch != nil && key[0] >= minCX && key[0] <= maxCX && key[1] >= minCZ && key[1] <= maxCZ {
			candidates = append(candidates, ch)
		}
	}
	c.mu.Unlock()

	bestDistSq := radius*radius + 1
	var bestPos BlockPos
	found := false

	for _, ch := range candidates {
		chunkBaseX := int(ch.X) * 16
		chunkBaseZ := int(ch.Z) * 16
		x0 := maxInt(minX, chunkBaseX)
		x1 := minInt(maxX, chunkBaseX+15)
		z0 := maxInt(minZ, chunkBaseZ)
		z1 := minInt(maxZ, chunkBaseZ+15)

		ch.mu.RLock()
		for y := minY; y <= maxY; y++ {
			for z := z0; z <= z1; z++ {
				for x := x0; x <= x1; x++ {
					st := ch.getBlock(x, y, z)
					if st == StateAir || !IsLightningRodBlock(st) {
						continue
					}
					dx := x - centerX
					dy := y - centerY
					dz := z - centerZ
					distSq := dx*dx + dy*dy + dz*dz
					if distSq <= radius*radius && (!found || distSq < bestDistSq || (distSq == bestDistSq && y > bestPos.Y)) {
						bestDistSq = distSq
						bestPos = BlockPos{X: x, Y: y, Z: z}
						found = true
					}
				}
			}
		}
		ch.mu.RUnlock()
	}
	return bestPos, found
}

// IsCopperWaterloggableBlock reports whether state is a copper block with a
// `waterlogged` property.
func IsCopperWaterloggableBlock(state uint16) bool {
	if _, _, _, ok := ClassifyCopperBlock(state); !ok {
		return false
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	_, hasWaterlogged := info.Properties["waterlogged"]
	return hasWaterlogged
}

// CopperBlockComparatorSignalAt returns the intrinsic comparator signal of a
// copper block at (x, y, z) (15 for lit copper_bulb, 1..4 for copper_golem_statue).
func (c *Cache) CopperBlockComparatorSignalAt(x, y, z int) int {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return 0
	}
	state := c.GetBlock(x, y, z)
	if IsCopperBulbBlock(state) {
		return CopperBulbComparatorSignal(state)
	}
	if IsCopperGolemStatueBlock(state) {
		return CopperGolemStatueComparatorSignal(state)
	}
	return 0
}

// --- Internal BlockUpdate / Redstone Integration Helpers ---

func (c *Cache) ensureCopperComparatorProvider() {
	if c == nil {
		return
	}
	if _, loaded := comparatorSignalProviders.Load(c); !loaded {
		comparatorSignalProviders.LoadOrStore(c, func(x, y, z int) int {
			return c.CopperBlockComparatorSignalAt(x, y, z)
		})
	}
}

func (c *Cache) syncCopperDoorPartner(x, y, z int, newState uint16, res *BlockUpdateResult) {
	if !IsCopperDoorBlock(newState) {
		return
	}
	info, ok := stateByID(newState)
	if !ok {
		return
	}
	half := info.Properties["half"]
	partnerY := y + 1
	wantHalf := "upper"
	if half == "upper" {
		partnerY = y - 1
		wantHalf = "lower"
	}
	if partnerY < MinY || partnerY >= MinY+WorldHeight {
		return
	}
	pState := c.GetBlock(x, partnerY, z)
	if !IsCopperDoorBlock(pState) {
		return
	}
	pInfo, ok := stateByID(pState)
	if !ok || pInfo.Properties["half"] != wantHalf {
		return
	}
	props := make(map[string]string, len(pInfo.Properties))
	for k, v := range pInfo.Properties {
		props[k] = v
	}
	props["open"] = info.Properties["open"]
	props["powered"] = info.Properties["powered"]
	if nextPartner, ok := nameToStateID(info.Name, props); ok && nextPartner != pState {
		c.setBlockRecorded(x, partnerY, z, nextPartner, res)
	}
}

func (c *Cache) checkCopperBulbsAround(x, y, z int, res *BlockUpdateResult) {
	if c == nil {
		return
	}
	c.ensureCopperComparatorProvider()
	for dy := -2; dy <= 2; dy++ {
		ny := y + dy
		if ny < MinY || ny >= MinY+WorldHeight {
			continue
		}
		for dz := -2; dz <= 2; dz++ {
			for dx := -2; dx <= 2; dx++ {
				if absInt(dx)+absInt(dy)+absInt(dz) > 2 {
					continue
				}
				bx, by, bz := x+dx, ny, z+dz
				st := c.GetBlock(bx, by, bz)
				if !IsCopperBulbBlock(st) {
					continue
				}
				info, ok := stateByID(st)
				if !ok {
					continue
				}
				powered := c.IsBlockPowered(bx, by, bz)
				wasPowered := info.Properties["powered"] == "true"
				if powered == wasPowered {
					continue
				}
				nextLit := info.Properties["lit"]
				if !wasPowered && powered {
					if nextLit == "true" {
						nextLit = "false"
					} else {
						nextLit = "true"
					}
				}
				nextPowered := "false"
				if powered {
					nextPowered = "true"
				}
				nextState, ok := nameToStateID(info.Name, map[string]string{
					"lit":     nextLit,
					"powered": nextPowered,
				})
				if !ok || nextState == st {
					continue
				}
				if c.setBlockRecorded(bx, by, bz, nextState, res) {
					c.syncComparatorsAroundCopperBlock(bx, by, bz, res)
				}
			}
		}
	}
}

func (c *Cache) syncComparatorsAroundCopperBlock(bx, by, bz int, res *BlockUpdateResult) {
	if c == nil || by < MinY || by >= MinY+WorldHeight {
		return
	}
	c.ensureCopperComparatorProvider()
	for _, d := range cardinalDirs {
		for dist := 1; dist <= 2; dist++ {
			cx, cz := bx-d.dx*dist, bz-d.dz*dist
			cState := c.GetBlock(cx, by, cz)
			cInfo, ok := stateByID(cState)
			if !ok || cInfo.Name != "minecraft:comparator" || cInfo.Properties["facing"] != d.name {
				continue
			}
			if dist == 2 {
				midState := c.GetBlock(bx-d.dx, by, bz-d.dz)
				if !isConductiveSolidBlock(midState) {
					continue
				}
			}
			rearSig := c.CopperBlockComparatorSignalAt(bx, by, bz)
			sideSig := c.comparatorSideSignal(cx, by, cz, d.name)
			var out int
			if rearSig > 0 {
				if cInfo.Properties["mode"] == "subtract" {
					out = clampPower(rearSig - sideSig)
				} else if rearSig >= sideSig {
					out = rearSig
				}
			}
			wantPowered := "false"
			if out > 0 {
				wantPowered = "true"
			}
			curComp := cState
			if cInfo.Properties["powered"] != wantPowered {
				if nextComp, ok := withStateProperty(cState, "powered", wantPowered); ok {
					if c.setBlockRecorded(cx, by, cz, nextComp, res) {
						curComp = nextComp
					}
				}
			}
			c.propagateRedstoneAround(cx, by, cz, cState, curComp, res)
		}
	}
}

func (c *Cache) stepCopperRedstoneUpdate(x, y, z int, res *BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return
	}
	state := c.GetBlock(x, y, z)
	if !IsLightningRodBlock(state) {
		return
	}
	info, ok := stateByID(state)
	if !ok || info.Name == "minecraft:lightning_rod" {
		// Unoxidized/unwaxed `minecraft:lightning_rod` is already handled by stepRedstoneUpdate.
		return
	}
	if info.Properties["powered"] == "true" {
		if nextState, ok := withStateProperty(state, "powered", "false"); ok {
			if c.setBlockRecorded(x, y, z, nextState, res) {
				c.propagateRedstoneAround(x, y, z, state, nextState, res)
			}
		}
	}
}
