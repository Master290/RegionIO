import net.minecraft.core.BlockPos;

import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Random;
import java.util.Set;

/**
 * Dumps the iteration order of a HashSet<BlockPos> so the Go port can be checked
 * against vanilla's actual hash table rather than against a reading of it.
 *
 * VegetationPatchFeature collects its ground cells into a plain HashSet and
 * distributeVegetation draws one nextFloat per entry in iteration order, so which
 * cell receives which roll - and therefore which cell grows a plant - is decided
 * by this order.
 *
 * Emitted to stdout in the fixture format the Go test parses: a "case" line with
 * the insertion count, then one "i x y z" line per inserted position in
 * insertion order, then one "o x y z" line per entry in iteration order. Every
 * case is built from distinct positions, because HashSet deduplicates on add
 * and the port is only claimed to model the deduplicated table.
 */
public final class VanillaBlockPosHashSetOrder {

    public static void main(String[] args) {
        StringBuilder out = new StringBuilder();
        // Sizes around every HashMap resize boundary (threshold = capacity * 0.75
        // for capacities 16, 32, 64, 128, 256), because that is where a port that
        // computes the final capacity wrong shows up.
        int[] sizes = {1, 2, 11, 12, 13, 23, 24, 25, 47, 48, 49, 95, 96, 97, 191, 192, 193, 250};
        int caseIndex = 0;
        for (int size : sizes) {
            emit(out, caseIndex++, discSet(1000 + size, size, 0, 0, 0, 8));
            emit(out, caseIndex++, discSet(2000 + size, size, -40000, -64, 30000, 9));
            emit(out, caseIndex++, discSet(3000 + size, size, 1, -1, 1, 8));
        }
        System.out.print(out);
    }

    private static List<BlockPos> discSet(long seed, int count, int originX, int originY, int originZ, int radius) {
        Random random = new Random(seed);
        Set<BlockPos> seen = new HashSet<>();
        List<BlockPos> inserted = new ArrayList<>();
        while (inserted.size() < count) {
            BlockPos pos = new BlockPos(
                    originX + random.nextInt(2 * radius + 1) - radius,
                    originY + random.nextInt(3) - 1,
                    originZ + random.nextInt(2 * radius + 1) - radius);
            if (seen.add(pos)) {
                inserted.add(pos);
            }
        }
        return inserted;
    }

    private static void emit(StringBuilder out, int caseIndex, List<BlockPos> inserted) {
        Set<BlockPos> table = new HashSet<>();
        table.addAll(inserted);
        out.append("case ").append(caseIndex).append(' ').append(table.size()).append('\n');
        for (BlockPos pos : inserted) {
            out.append("i ").append(pos.getX()).append(' ').append(pos.getY()).append(' ').append(pos.getZ()).append('\n');
        }
        for (BlockPos pos : table) {
            out.append("o ").append(pos.getX()).append(' ').append(pos.getY()).append(' ').append(pos.getZ()).append('\n');
        }
    }
}
