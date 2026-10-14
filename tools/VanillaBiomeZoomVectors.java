import net.minecraft.world.level.biome.BiomeManager;

/**
 * Prints golden vectors for BiomeManager.getBiome, the block-resolution biome zoom
 * the surface rules and carvers read. The noise source records the quart it was
 * asked for and returns null, so no registry or bootstrap is needed: the vector is
 * the quart, which is exactly what the Go port (zoomQuart in
 * internal/world/biome_zoom.go) computes.
 *
 * Build and run (Windows separators; see CLAUDE.md for the classpath):
 *   javac -nowarn -cp "$CP" -d <out> tools/VanillaBiomeZoomVectors.java
 *   java -cp "<out>;$CP" VanillaBiomeZoomVectors
 */
public final class VanillaBiomeZoomVectors {
    private static int qx, qy, qz;

    public static void main(String[] args) {
        long seed = 12345L;
        long zoom = BiomeManager.obfuscateSeed(seed);
        System.out.println("obfuscateSeed " + seed + " = " + zoom);
        BiomeManager manager = new BiomeManager((x, y, z) -> {
            qx = x;
            qy = y;
            qz = z;
            return null;
        }, zoom);
        int[][] positions = {
            {0, 0, 0}, {1, 64, -1}, {-1, -64, 1}, {263, 89, -483}, {263, 93, -476},
            {261, 93, -474}, {264, 91, -481}, {-627, 63, 321}, {15, 319, 15}, {-17, -33, 1000},
            {123456, 70, -654321}, {2, 2, 2}, {3, 3, 3}, {5, 6, 7}, {-5, -6, -7},
        };
        for (int[] p : positions) {
            manager.getBiome(new net.minecraft.core.BlockPos(p[0], p[1], p[2]));
            System.out.printf("{%d, %d, %d, %d, %d, %d},%n", p[0], p[1], p[2], qx, qy, qz);
        }
    }
}
