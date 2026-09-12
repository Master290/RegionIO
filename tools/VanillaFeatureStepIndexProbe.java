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
import java.util.IdentityHashMap;
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

        var pfReg = lookup.lookupOrThrow(Registries.PLACED_FEATURE);
        var pfMap = new IdentityHashMap<PlacedFeature, String>();
        for (var entry : pfReg.listElements().toList()) {
            pfMap.put(entry.value(), entry.key().identifier().toString());
        }

        var steps = FeatureSorter.buildFeaturesPerStep(biomes, b -> b.value().getGenerationSettings().features(), true);
        var step9 = steps.get(9);
        var list = step9.features();
        var mapping = step9.indexMapping();
        System.out.println("STEP 9 GLOBAL FEATURE INDICES (total " + list.size() + "):");
        for (int i = 0; i < list.size(); i++) {
            var pf = list.get(i);
            var key = pfMap.get(pf);
            System.out.printf("  Index %d: %s (mapping=%d)\n", i, key, mapping.applyAsInt(pf));
        }
    }
}
