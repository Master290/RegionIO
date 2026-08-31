// Scans every chunk of a kept vanilla capture world for saved structure
// starts and prints each start's piece-count and bounding box, so every
// structure that contributed writes to the captured area is known.
//
//   CP="versions/26.1.2/server-26.1.2.jar;$(find libraries -name '*.jar' | tr '\n' ';')"
//   javac -nowarn -cp "$CP" -d <out> tools/VanillaStartScan.java
//   java -cp "<out>;$CP" VanillaStartScan <world-dir>
import java.io.DataInputStream;
import java.util.List;
import java.util.Map;

import net.minecraft.SharedConstants;
import net.minecraft.nbt.CompoundTag;
import net.minecraft.nbt.NbtAccounter;
import net.minecraft.nbt.NbtIo;
import net.minecraft.nbt.Tag;
import net.minecraft.server.Bootstrap;
import net.minecraft.world.level.ChunkPos;
import net.minecraft.world.level.chunk.storage.RegionFile;
import net.minecraft.world.level.chunk.storage.RegionStorageInfo;

public final class VanillaStartScan {

    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();
        String worldDir = args[0];
        java.io.File regionDir = new java.io.File(worldDir, "dimensions/minecraft/overworld/region");
        for (java.io.File file : regionDir.listFiles()) {
            if (!file.getName().endsWith(".mca")) {
                continue;
            }
            try (RegionFile rf = new RegionFile(new RegionStorageInfo("startscan", net.minecraft.world.level.Level.OVERWORLD, ""), file.toPath(), regionDir.toPath(), true)) {
                for (int i = 0; i < 32; i++) {
                    for (int j = 0; j < 32; j++) {
                        // filled below
                    }
                }
            }
        }
        // Iterate the region coordinates from the file name.
        for (java.io.File file : regionDir.listFiles()) {
            if (!file.getName().endsWith(".mca")) {
                continue;
            }
            String[] parts = file.getName().replace(".mca", "").split("\\.");
            int rx = Integer.parseInt(parts[1]);
            int rz = Integer.parseInt(parts[2]);
            try (RegionFile rf = new RegionFile(new RegionStorageInfo("startscan", net.minecraft.world.level.Level.OVERWORLD, ""), file.toPath(), regionDir.toPath(), true)) {
                for (int i = 0; i < 32; i++) {
                    for (int j = 0; j < 32; j++) {
                        ChunkPos pos = new ChunkPos(rx * 32 + i, rz * 32 + j);
                        try (DataInputStream in = rf.getChunkDataInputStream(pos)) {
                            if (in == null) {
                                continue;
                            }
                            CompoundTag root = NbtIo.read(in, NbtAccounter.create(Long.MAX_VALUE));
                            CompoundTag chunk = root.contains("") ? root.getCompoundOrEmpty("") : root;
                            Tag structures = chunk.get("structures");
                            if (structures == null) {
                                continue;
                            }
                            CompoundTag st = (CompoundTag) structures;
                            Tag starts = st.get("starts");
                            if (starts == null) {
                                continue;
                            }
                            CompoundTag startMap = (CompoundTag) starts;
                            for (String key : startMap.keySet()) {
                                Tag startTag = startMap.get(key);
                                if (startTag instanceof CompoundTag) {
                                    CompoundTag start = (CompoundTag) startTag;
                                    Tag children = start.get("Children");
                                    int n = children == null ? 0 : ((net.minecraft.nbt.ListTag) children).size();
                                    if (n > 0) {
                                        System.out.println("chunk (" + pos.getMinBlockX() / 16 + "," + pos.getMinBlockZ() / 16 + ") start " + key
                                            + " children=" + n);
                                    }
                                }
                            }
                        } catch (Exception e) {
                            // absent chunk
                        }
                    }
                }
            }
        }
    }
}
