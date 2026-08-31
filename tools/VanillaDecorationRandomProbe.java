// Prints the first draws of a chunk's decoration random, replicating
// applyBiomeDecoration's seeding, so the Go replay can align its structure
// piece streams.
//
//   CP="versions/26.1.2/server-26.1.2.jar;$(find libraries -name '*.jar' | tr '\n' ';')"
//   javac -nowarn -cp "$CP" -d <out> tools/VanillaDecorationRandomProbe.java
//   java -cp "<out>;$CP" VanillaDecorationRandomProbe <seed> <blockX> <blockZ> [count]
import net.minecraft.world.level.levelgen.WorldgenRandom;
import net.minecraft.world.level.levelgen.XoroshiroRandomSource;
import net.minecraft.world.level.levelgen.RandomSupport;

public final class VanillaDecorationRandomProbe {
    public static void main(String[] args) {
        long seed = Long.parseLong(args[0]);
        int blockX = Integer.parseInt(args[1]);
        int blockZ = Integer.parseInt(args[2]);
        int count = args.length > 3 ? Integer.parseInt(args[3]) : 60;
        WorldgenRandom random = new WorldgenRandom(new XoroshiroRandomSource(RandomSupport.generateUniqueSeed()));
        long decorationSeed = random.setDecorationSeed(seed, blockX, blockZ);
        System.out.println("decorationSeed=" + decorationSeed);
        for (int i = 0; i < count; i++) {
            System.out.println(i + " float=" + random.nextFloat());
        }
    }
}
