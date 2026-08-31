// Prints a chunk's xPos/zPos and its structure References decoded as chunk
// coordinates, verifying which starts a chunk actually references.
//
//   CP="versions/26.1.2/server-26.1.2.jar;$(find libraries -name '*.jar' | tr '\n' ';')"
//   javac -nowarn -cp "$CP" -d <out> tools/VanillaReferencesProbe.java
//   java -cp "<out>;$CP" VanillaReferencesProbe <world-dir> <cx> <cz>
import java.io.DataInputStream;
import java.io.File;

import net.minecraft.SharedConstants;
import net.minecraft.nbt.CompoundTag;
import net.minecraft.nbt.ListTag;
import net.minecraft.nbt.NbtAccounter;
import net.minecraft.nbt.NbtIo;
import net.minecraft.nbt.Tag;
import net.minecraft.server.Bootstrap;
import net.minecraft.world.level.ChunkPos;
import net.minecraft.world.level.chunk.storage.RegionFile;
import net.minecraft.world.level.chunk.storage.RegionStorageInfo;

public final class VanillaReferencesProbe {

    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();
        String worldDir = args[0];
        int cx = Integer.parseInt(args[1]);
        int cz = Integer.parseInt(args[2]);
        File regionDir = new File(worldDir, "dimensions/minecraft/overworld/region");
        File file = new File(regionDir, "r." + (cx >> 5) + "." + (cz >> 5) + ".mca");
        try (RegionFile rf = new RegionFile(new RegionStorageInfo("refprobe", net.minecraft.world.level.Level.OVERWORLD, ""), file.toPath(), regionDir.toPath(), true)) {
            ChunkPos pos = new ChunkPos(cx, cz);
            try (DataInputStream in = rf.getChunkDataInputStream(pos)) {
                if (in == null) {
                    System.out.println("chunk (" + cx + "," + cz + ") absent");
                    return;
                }
                CompoundTag root = NbtIo.read(in, NbtAccounter.create(Long.MAX_VALUE));
                CompoundTag chunk = root.contains("") ? root.getCompoundOrEmpty("") : root;
                System.out.println("xPos=" + chunk.getIntOr("xPos", 0) + " zPos=" + chunk.getIntOr("zPos", 0));
                Tag structures = chunk.get("structures");
                if (structures instanceof CompoundTag st) {
                    Tag refs = st.get("References");
                    if (refs instanceof CompoundTag refMap) {
                        for (String key : refMap.keySet()) {
                            net.minecraft.nbt.LongArrayTag list = (net.minecraft.nbt.LongArrayTag) refMap.get(key);
                            StringBuilder sb = new StringBuilder(key + ": [");
                            for (long packed : list.getAsLongArray()) {
                                sb.append("(").append(ChunkPos.getX(packed)).append(",").append(ChunkPos.getZ(packed)).append(") ");
                            }
                            System.out.println(sb.append("]").toString());
                        }
                    }
                }
            }
        }
    }
}
