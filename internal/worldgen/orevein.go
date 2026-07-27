package worldgen

import "math"

// orevein.go ports net.minecraft.world.level.levelgen.OreVeinifier.
//
// Ore veins are not a decoration feature: they run during the density pass, as
// the second entry of the same MaterialRuleList the aquifer heads. Where the
// aquifer says "this position is solid rock", the veinifier gets a chance to
// replace what would have been plain stone with copper or iron and its
// surrounding filler. That is why veins are long branching sheets rather than
// blobs, and why they never open into a cave: a position the aquifer hollowed
// out never reaches this code.

// Vein constants, as widened from OreVeinifier's float fields. Written out
// rather than computed so the double each float comparison widens to is
// unambiguous.
const (
	veininessThreshold       = 0.4000000059604645  // (double)(float)0.4f
	edgeRoundoffBegin        = 20.0                // an int field, used as a double
	maxEdgeRoundoff          = -0.2                // a real double literal, not a widened float
	minRichness              = 0.10000000149011612 // (double)(float)0.1f
	maxRichness              = 0.30000001192092896 // (double)(float)0.3f
	maxRichnessThreshold     = 0.6000000238418579  // (double)(float)0.6f
	skipOreIfGapNoiseIsBelow = -0.3000000119209290 // (double)(float)-0.3f

	veinSolidness       float32 = 0.7  // compared as a float
	chanceOfRawOreBlock float32 = 0.02 // compared as a float
)

// veinType is OreVeinifier.VeinType. Note the asymmetry: copper's ore is the
// plain stone variant while iron's is the deepslate one, and neither switches
// with depth — the block is fixed per type.
type veinType struct {
	ore, rawOreBlock, filler uint16
	minY, maxY               int
}

var (
	veinCopper = veinType{ore: 25313, rawOreBlock: 29578, filler: 2, minY: 0, maxY: 50}
	veinIron   = veinType{ore: 132, rawOreBlock: 29577, filler: 23452, minY: -60, maxY: -8}
)

// OreVeinifier decides whether a solid position becomes part of an ore vein.
type OreVeinifier struct {
	toggle, ridged, gap DensityFunction
	random              PositionalRandomFactory
}

// NewOreVeinifier returns nil when the settings or the router leave veins off,
// which the caller reads as "always place the default block".
func NewOreVeinifier(od *OverworldDensity) *OreVeinifier {
	if !od.OreVeinsEnabled || od.VeinToggle == nil || od.VeinRidged == nil || od.VeinGap == nil || od.OreRandom == nil {
		return nil
	}
	return &OreVeinifier{toggle: od.VeinToggle, ridged: od.VeinRidged, gap: od.VeinGap, random: od.OreRandom}
}

// Calculate returns the block a vein places at this position, or ok=false to
// leave the default block. ctx must carry the cell-interpolated values: both
// vein_toggle and vein_ridged are minecraft:interpolated in the datapack, while
// vein_gap is a plain per-block noise.
//
// The three random draws happen in a fixed order and are the whole shape of the
// output; reordering them, or hoisting the vein_gap compute above the second
// draw, changes every vein in the world.
func (o *OreVeinifier) Calculate(ctx FunctionContext, x, y, z int) (uint16, bool) {
	veininess := o.toggle.Compute(ctx)
	vein := &veinIron
	if veininess > 0 {
		vein = &veinCopper
	}
	// The type comes from the sign of the noise but the Y window comes from the
	// type, so a position outside its type's band is simply not a vein — which
	// is what keeps copper above 0 and iron below -8.
	distanceToTop := vein.maxY - y
	distanceToBottom := y - vein.minY
	if distanceToBottom < 0 || distanceToTop < 0 {
		return 0, false
	}
	edgeDistance := min(distanceToTop, distanceToBottom)
	edgeRoundoff := clampedMap(float64(edgeDistance), 0.0, edgeRoundoffBegin, maxEdgeRoundoff, 0.0)
	absVeininess := math.Abs(veininess)
	if absVeininess+edgeRoundoff < veininessThreshold {
		return 0, false
	}

	random := o.random.At(x, y, z)
	if random.NextFloat() > veinSolidness {
		return 0, false
	}
	if o.ridged.Compute(ctx) >= 0 {
		return 0, false
	}
	richness := clampedMap(absVeininess, veininessThreshold, maxRichnessThreshold, minRichness, maxRichness)
	if float64(random.NextFloat()) < richness && o.gap.Compute(ctx) > skipOreIfGapNoiseIsBelow {
		if random.NextFloat() < chanceOfRawOreBlock {
			return vein.rawOreBlock, true
		}
		return vein.ore, true
	}
	return vein.filler, true
}
