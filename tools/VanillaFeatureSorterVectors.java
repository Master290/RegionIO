import java.lang.reflect.Constructor;
import java.util.ArrayList;
import java.util.IdentityHashMap;
import java.util.List;
import java.util.Map;
import java.util.function.Function;

import net.minecraft.SharedConstants;
import net.minecraft.core.Holder;
import net.minecraft.core.HolderSet;
import net.minecraft.server.Bootstrap;
import net.minecraft.world.level.biome.BiomeGenerationSettings;
import net.minecraft.world.level.biome.FeatureSorter;
import net.minecraft.world.level.levelgen.feature.ConfiguredFeature;
import net.minecraft.world.level.levelgen.feature.Feature;
import net.minecraft.world.level.levelgen.feature.configurations.NoneFeatureConfiguration;
import net.minecraft.world.level.levelgen.placement.CountPlacement;
import net.minecraft.world.level.levelgen.placement.PlacedFeature;

// Emits FeatureSorter vectors from the official 26.1.2 runtime. The graph is
// synthetic so this helper verifies ordering and identity without booting a
// world or depending on registry datapack loading.
public final class VanillaFeatureSorterVectors {
    private static final Map<PlacedFeature, String> NAMES = new IdentityHashMap<>();

    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();

        PlacedFeature f1 = feature("f1", 1);
        PlacedFeature f2 = feature("f2", 2);
        PlacedFeature f3 = feature("f3", 3);
        PlacedFeature f4 = feature("f4", 4);
        PlacedFeature f5 = feature("f5", 5);

        BiomeGenerationSettings a = settings(
                List.of(List.of(f1), List.of(f3, f4)));
        BiomeGenerationSettings b = settings(
                List.of(List.of(f2, f1), List.of(f5, f4)));

        dump("ab", List.of(a, b));
        dump("ba", List.of(b, a));
    }

    private static PlacedFeature feature(String name, int count) {
        ConfiguredFeature<NoneFeatureConfiguration, Feature<NoneFeatureConfiguration>> configured =
                new ConfiguredFeature<>(Feature.NO_OP, NoneFeatureConfiguration.INSTANCE);
        PlacedFeature placed = new PlacedFeature(Holder.direct(configured), List.of(CountPlacement.of(count)));
        NAMES.put(placed, name);
        return placed;
    }

    @SuppressWarnings("unchecked")
    private static BiomeGenerationSettings settings(List<List<PlacedFeature>> stages) throws Exception {
        List<HolderSet<PlacedFeature>> holders = new ArrayList<>();
        for (List<PlacedFeature> stage : stages) {
            Holder<PlacedFeature>[] values = stage.stream().map(Holder::direct).toArray(Holder[]::new);
            holders.add(HolderSet.direct(values));
        }
        Constructor<BiomeGenerationSettings> constructor = BiomeGenerationSettings.class
                .getDeclaredConstructor(HolderSet.class, List.class);
        constructor.setAccessible(true);
        return constructor.newInstance(HolderSet.empty(), holders);
    }

    private static void dump(String label, List<BiomeGenerationSettings> biomes) {
        Function<BiomeGenerationSettings, List<HolderSet<PlacedFeature>>> stages =
                BiomeGenerationSettings::features;
        List<FeatureSorter.StepFeatureData> steps =
                FeatureSorter.buildFeaturesPerStep(biomes, stages, true);
        for (int stage = 0; stage < steps.size(); stage++) {
            FeatureSorter.StepFeatureData step = steps.get(stage);
            List<String> values = new ArrayList<>();
            for (int index = 0; index < step.features().size(); index++) {
                PlacedFeature feature = step.features().get(index);
                values.add(NAMES.get(feature) + "@" + step.indexMapping().applyAsInt(feature));
            }
            System.out.println(label + ".stage" + stage + "=" + String.join(",", values));
        }
    }
}
