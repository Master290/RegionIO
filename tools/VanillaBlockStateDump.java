import java.io.BufferedOutputStream;
import java.io.DataOutputStream;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

import net.minecraft.SharedConstants;
import net.minecraft.core.Direction;
import net.minecraft.server.Bootstrap;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.LeavesBlock;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.phys.AABB;
import net.minecraft.world.phys.shapes.VoxelShape;

// Dumps the per-block-state properties the chunk pipeline needs, straight from
// vanilla runtime state. generated/reports/blocks.json carries none of them:
// they only exist once the block-state registry is built.
//
// Compile and run against the unpacked 26.1.2 server classpath and redirect
// stdout to internal/world/block_properties.bin:
//
//   CP="versions/26.1.2/server-26.1.2.jar;$(find libraries -name '*.jar' | tr '\n' ';')"
//   javac -nowarn -cp "$CP" -d <out> tools/VanillaBlockStateDump.java
//   java -cp "<out>;$CP" VanillaBlockStateDump > internal/world/block_properties.bin
//
// Was VanillaLightDump: it dumped only lighting until the heightmaps needed
// blocksMotion and the leaves test, neither of which is derivable from any
// report or tag file.
public final class VanillaBlockStateDump {
    private record MaskKey(byte[] data) {
        @Override public boolean equals(Object other) {
            return other instanceof MaskKey key && Arrays.equals(data, key.data);
        }
        @Override public int hashCode() { return Arrays.hashCode(data); }
    }

    // Format version. Bump whenever the byte layout or the meaning of a flag
    // bit changes; the Go decoder rejects anything it does not know.
    private static final int FORMAT_VERSION = 2;

    // Flag bits. 1..4 drive lighting, 8..32 drive the heightmaps.
    private static final int FLAG_PROPAGATES_SKYLIGHT = 1;
    private static final int FLAG_CAN_OCCLUDE = 2;
    private static final int FLAG_SHAPE_FOR_OCCLUSION = 4;
    private static final int FLAG_BLOCKS_MOTION = 8;
    private static final int FLAG_FLUID = 16;
    private static final int FLAG_LEAVES = 32;

    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();

        int count = Block.BLOCK_STATE_REGISTRY.size();
        byte[] dampening = new byte[count];
        byte[] emission = new byte[count];
        byte[] flags = new byte[count];
        int[] shapeIndex = new int[count];
        List<byte[]> shapes = new ArrayList<>();
        Map<MaskKey, Integer> indices = new HashMap<>();

        for (BlockState state : Block.BLOCK_STATE_REGISTRY) {
            int id = Block.getId(state);
            dampening[id] = (byte) state.getLightDampening();
            emission[id] = (byte) state.getLightEmission();
            int stateFlags = 0;
            if (state.propagatesSkylightDown()) stateFlags |= FLAG_PROPAGATES_SKYLIGHT;
            if (state.canOcclude()) stateFlags |= FLAG_CAN_OCCLUDE;
            if (state.useShapeForLightOcclusion()) stateFlags |= FLAG_SHAPE_FOR_OCCLUSION;
            // Heightmap.Types: MOTION_BLOCKING is blocksMotion || fluid, and
            // MOTION_BLOCKING_NO_LEAVES additionally excludes LeavesBlock --
            // an instanceof, not the minecraft:leaves tag, so the two differ.
            if (state.blocksMotion()) stateFlags |= FLAG_BLOCKS_MOTION;
            if (!state.getFluidState().isEmpty()) stateFlags |= FLAG_FLUID;
            if (state.getBlock() instanceof LeavesBlock) stateFlags |= FLAG_LEAVES;
            flags[id] = (byte) stateFlags;

            byte[] masks = faceMasks(state);
            MaskKey key = new MaskKey(masks);
            Integer index = indices.get(key);
            if (index == null) {
                index = shapes.size();
                indices.put(key, index);
                shapes.add(masks);
            }
            shapeIndex[id] = index;
        }

        DataOutputStream out = new DataOutputStream(new BufferedOutputStream(System.out));
        out.writeInt(0x52494f4c); // RIOL
        out.writeInt(FORMAT_VERSION);
        out.writeInt(count);
        out.writeInt(shapes.size());
        for (int id = 0; id < count; id++) {
            out.writeByte(dampening[id]);
            out.writeByte(emission[id]);
            out.writeByte(flags[id]);
            out.writeShort(shapeIndex[id]);
        }
        for (byte[] shape : shapes) out.write(shape);
        out.flush();
    }

    private static byte[] faceMasks(BlockState state) {
        byte[] result = new byte[Direction.values().length * 32];
        for (Direction direction : Direction.values()) {
            VoxelShape shape = state.getFaceOcclusionShape(direction);
            List<AABB> boxes = shape.toAabbs();
            int base = direction.get3DDataValue() * 32;
            for (int v = 0; v < 16; v++) {
                for (int u = 0; u < 16; u++) {
                    double du = (u + 0.5) / 16.0;
                    double dv = (v + 0.5) / 16.0;
                    if (covered(boxes, direction, du, dv)) {
                        int bit = v * 16 + u;
                        result[base + bit / 8] |= (byte) (1 << (bit % 8));
                    }
                }
            }
        }
        return result;
    }

    private static boolean covered(List<AABB> boxes, Direction direction, double u, double v) {
        for (AABB box : boxes) {
            boolean inside = switch (direction.getAxis()) {
                case Y -> contains(box.minX, box.maxX, u) && contains(box.minZ, box.maxZ, v);
                case Z -> contains(box.minX, box.maxX, u) && contains(box.minY, box.maxY, v);
                case X -> contains(box.minZ, box.maxZ, u) && contains(box.minY, box.maxY, v);
            };
            if (inside) return true;
        }
        return false;
    }

    private static boolean contains(double min, double max, double value) {
        return value >= min - 1.0e-7 && value <= max + 1.0e-7;
    }
}
