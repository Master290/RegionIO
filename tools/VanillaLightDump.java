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
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.phys.AABB;
import net.minecraft.world.phys.shapes.VoxelShape;

// Dumps protocol-775 lighting properties directly from vanilla runtime state.
// Compile/run against the unpacked 26.1.2 server classpath and redirect stdout
// to internal/world/light_properties.bin.
public final class VanillaLightDump {
    private record MaskKey(byte[] data) {
        @Override public boolean equals(Object other) {
            return other instanceof MaskKey key && Arrays.equals(data, key.data);
        }
        @Override public int hashCode() { return Arrays.hashCode(data); }
    }

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
            if (state.propagatesSkylightDown()) stateFlags |= 1;
            if (state.canOcclude()) stateFlags |= 2;
            if (state.useShapeForLightOcclusion()) stateFlags |= 4;
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
        out.writeInt(1);
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
