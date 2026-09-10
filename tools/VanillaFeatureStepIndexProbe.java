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
        var biomesReg = lookup.lookupOrThrow(Registries.BIOME);
        var lushCaves = biomesReg.getOrThrow(net.minecraft.resources.ResourceKey.create(Registries.BIOME, net.minecraft.resources.Identifier.parse("minecraft:lush_caves")));
        var lushFeatures = lushCaves.value().getGenerationSettings().features();
        System.out.println("LUSH CAVES STEPS:");
        for (int step = 0; step < lushFeatures.size(); step++) {
            var list = lushFeatures.get(step);
            for (var h : list) {
                System.out.printf("  Step %d: %s\n", step, h.unwrapKey().map(k -> k.identifier().toString()).orElse("?"));
            }
        }
        System.out.println("STEP 9 GLOBAL FEATURE INDICES:");
        var step9 = steps.get(9);
        var pfReg = lookup.lookupOrThrow(Registries.PLACED_FEATURE);
        var glPf = pfReg.getOrThrow(net.minecraft.resources.ResourceKey.create(Registries.PLACED_FEATURE, net.minecraft.resources.Identifier.parse("minecraft:glow_lichen"))).value();
        for (var pm : glPf.placement()) {
            System.out.println("  PM: " + pm.getClass().getSimpleName());
            for (var f : pm.getClass().getDeclaredFields()) {
                f.setAccessible(true);
                System.out.println("    " + f.getName() + " = " + f.get(pm));
            }
        }
        var cfg = glPf.feature().value().config();
        System.out.println("CFG class: " + cfg.getClass().getName());
        for (var f : cfg.getClass().getDeclaredFields()) {
            f.setAccessible(true);
            System.out.println("  cfg." + f.getName() + " = " + f.get(cfg));
        }
    }
}
