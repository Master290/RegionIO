// Reads the saved chunk NBT for the ocean-ruin chunks straight out of a kept
// vanilla capture world and prints the scheduled block_ticks / fluid_ticks
// plus the states of the disputed cells. This settles what physics vanilla
// scheduled during structure placement (falling gravel, bubble columns) and
// what the loaded chunks settled to.
//
//   CP="versions/26.1.2/server-26.1.2.jar;$(find libraries -name '*.jar' | tr '\n' ';')"
//   javac -nowarn -cp "$CP" -d <out> tools/VanillaRuinChunkTicksProbe.java
//   java -cp "<out>;$CP" VanillaRuinChunkTicksProbe <world-dir> <chunkX> <chunkZ>
import java.io.DataInputStream;
import java.io.File;
import java.util.List;
import java.util.Optional;

import net.minecraft.nbt.CompoundTag;
import net.minecraft.nbt.NbtAccounter;
import net.minecraft.nbt.NbtIo;
import net.minecraft.nbt.Tag;
import net.minecraft.world.level.ChunkPos;
import net.minecraft.world.level.chunk.storage.RegionFile;

public final class VanillaRuinChunkTicksProbe {

    public static void main(String[] args) throws Exception {
        net.minecraft.SharedConstants.tryDetectVersion();
        net.minecraft.server.Bootstrap.bootStrap();
        String worldDir = args[0];
        int cx = Integer.parseInt(args[1]);
        int cz = Integer.parseInt(args[2]);
        File regionDir = new File(worldDir, "dimensions/minecraft/overworld/region");
        File file = new File(regionDir, "r." + (cx >> 5) + "." + (cz >> 5) + ".mca");
        System.out.println("region file: " + file + " exists=" + file.exists());
        try (RegionFile rf = new RegionFile(new net.minecraft.world.level.chunk.storage.RegionStorageInfo("vanillaruinchunkticks", net.minecraft.world.level.Level.OVERWORLD, ""), file.toPath(), regionDir.toPath(), true)) {
            ChunkPos pos = new ChunkPos(cx, cz);
            try (DataInputStream in = rf.getChunkDataInputStream(pos)) {
                if (in == null) {
                    System.out.println("chunk (" + cx + "," + cz + ") absent");
                    return;
                }
                CompoundTag root = NbtIo.read(in, NbtAccounter.create(Long.MAX_VALUE));
                // The outer wrapper has a single "" compound.
                CompoundTag chunk = root.contains("") ? root.getCompoundOrEmpty("") : root;
                System.out.println("chunk keys: " + chunk.keySet());
                printTicks(chunk, "block_ticks");
                printTicks(chunk, "fluid_ticks");
                Tag be = chunk.get("block_entities");
                if (be != null) {
                    System.out.println("block_entities: " + be);
                }
                Tag structures = chunk.get("structures");
                if (structures != null) {
                    System.out.println("structures: " + structures);
                }
            }
        }
    }

    static void printTicks(CompoundTag chunk, String key) {
        Tag ticks = chunk.get(key);
        if (ticks == null) {
            System.out.println(key + ": <absent>");
            return;
        }
        System.out.println(key + ":");
        for (Tag t : (net.minecraft.nbt.ListTag) ticks) {
            System.out.println("  " + t);
        }
    }
}
