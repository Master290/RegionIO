import java.util.List;
import java.util.stream.Stream;

import net.minecraft.SharedConstants;
import net.minecraft.core.BlockPos;
import net.minecraft.server.Bootstrap;
import net.minecraft.util.RandomSource;
import net.minecraft.util.valueproviders.ClampedNormalInt;
import net.minecraft.util.valueproviders.TrapezoidInt;
import net.minecraft.world.level.levelgen.LegacyRandomSource;
import net.minecraft.world.level.levelgen.WorldgenRandom;
import net.minecraft.world.level.levelgen.XoroshiroRandomSource;
import net.minecraft.world.level.levelgen.placement.CountPlacement;
import net.minecraft.world.level.levelgen.placement.PlacementModifier;
import net.minecraft.world.level.levelgen.placement.RandomOffsetPlacement;

// Emits placement stream vectors from the official 26.1.2 runtime. These
// modifiers do not read PlacementContext, so no world or registry is required.
public final class VanillaPlacementVectors {
    public static void main(String[] args) {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();

        RandomSource offsetRandom = new LegacyRandomSource(12345L);
        List<PlacementModifier> modifiers = List.of(
                CountPlacement.of(3),
                RandomOffsetPlacement.of(
                        TrapezoidInt.of(-4, 4, 0),
                        TrapezoidInt.of(-2, 2, 0)));
        Stream<BlockPos> positions = Stream.of(new BlockPos(32, 10, -16));
        for (PlacementModifier modifier : modifiers) {
            positions = positions.flatMap(position -> modifier.getPositions(null, offsetRandom, position));
        }
        System.out.println("offset=" + positions.map(VanillaPlacementVectors::position).toList());

        RandomSource normalRandom = new LegacyRandomSource(12345L);
        ClampedNormalInt normal = ClampedNormalInt.of(0.0F, 3.0F, -10, 10);
        StringBuilder samples = new StringBuilder();
        for (int i = 0; i < 8; i++) {
            if (i > 0) samples.append(',');
            samples.append(normal.sample(normalRandom));
        }
        System.out.println("normal=" + samples);

        WorldgenRandom decoration = new WorldgenRandom(new LegacyRandomSource(0L));
        long decorationSeed = decoration.setDecorationSeed(12345L, 0, 0);
        decoration.setFeatureSeed(decorationSeed, 10, 6);
        System.out.println("legacy.decoration.seed=" + decorationSeed);
        System.out.println("legacy.feature10.stage6=" + randomVector(decoration));

        WorldgenRandom xoroshiro = new WorldgenRandom(new XoroshiroRandomSource(0L));
        long xoroshiroSeed = xoroshiro.setDecorationSeed(12345L, 0, 0);
        xoroshiro.setFeatureSeed(xoroshiroSeed, 10, 6);
        System.out.println("xoroshiro.decoration.seed=" + xoroshiroSeed);
        System.out.println("xoroshiro.feature10.stage6=" + randomVector(xoroshiro));
    }

    private static String randomVector(RandomSource random) {
        return random.nextInt(16) + "," + random.nextInt(16) + "," + random.nextInt(65)
                + "," + random.nextFloat() + "," + random.nextDouble();
    }

    private static String position(BlockPos position) {
        return position.getX() + ":" + position.getY() + ":" + position.getZ();
    }
}
