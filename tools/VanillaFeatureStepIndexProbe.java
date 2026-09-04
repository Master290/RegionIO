// Prints the FeatureSorter step index mapping for the overworld: for each
// decoration step, the sorted feature indices and names that
// applyBiomeDecoration's setFeatureSeed(decorationSeed, index, step) uses.
//
//   CP="versions/26.1.2/server-26.1.2.jar;$(find libraries -name '*.jar' | tr '\n' ';')"
//   javac -nowarn -cp "$CP" -d tools/bin tools/VanillaFeatureStepIndexProbe.java
//   java -cp "tools/bin;$CP" VanillaFeatureStepIndexProbe
import net.minecraft.SharedConstants;
import net.minecraft.core.Holder;
import net.minecraft.core.registries.Registries;
import net.minecraft.data.registries.VanillaRegistries;
import net.minecraft.server.Bootstrap;
import net.minecraft.world.level.biome.Biome;
import net.minecraft.world.level.biome.FeatureSorter;
import net.minecraft.world.level.biome.MultiNoiseBiomeSourceParameterLists;
import net.minecraft.world.level.levelgen.placement.PlacedFeature;

import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.List;

public final class VanillaFeatureStepIndexProbe {

    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();
        var lookup = VanillaRegistries.createLookup();
        var params = lookup.lookupOrThrow(Registries.MULTI_NOISE_BIOME_SOURCE_PARAMETER_LIST)
                .getOrThrow(MultiNoiseBiomeSourceParameterLists.OVERWORLD);

        var biomes = new ArrayList<Holder<Biome>>();
        var seen = new LinkedHashSet<Holder<Biome>>();
        for (var pair : params.value().parameters().values()) {
            if (seen.add(pair.getSecond())) {
                biomes.add(pair.getSecond());
            }
        }

        var steps = FeatureSorter.buildFeaturesPerStep(biomes, b -> b.value().getGenerationSettings().features(), true);
        var placedFeatures = lookup.lookupOrThrow(Registries.PLACED_FEATURE);

        for (int step = 0; step < steps.size(); step++) {
            if (step != 9) {
                continue;
            }
            var list = steps.get(step).features();
            for (int i = 0; i < list.size(); i++) {
                var f = list.get(i);
                String name = "?";
                for (var el : placedFeatures.listElements().toList()) {
                    if (el.value() == f) {
                        name = el.key().identifier().toString();
                        break;
                    }
                }
                System.out.println("STEP9 " + i + " " + name);
            }
        }
    }
}
