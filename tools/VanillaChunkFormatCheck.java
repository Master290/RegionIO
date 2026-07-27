import java.lang.reflect.Method;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.io.DataInputStream;

import net.minecraft.SharedConstants;
import net.minecraft.core.IdMap;
import net.minecraft.nbt.ByteTag;
import net.minecraft.nbt.CompoundTag;
import net.minecraft.nbt.ListTag;
import net.minecraft.nbt.LongArrayTag;
import net.minecraft.nbt.NbtAccounter;
import net.minecraft.nbt.NbtIo;
import net.minecraft.nbt.Tag;
import net.minecraft.server.Bootstrap;
import net.minecraft.util.SimpleBitStorage;
import net.minecraft.world.level.ChunkPos;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.chunk.Strategy;
import net.minecraft.world.level.chunk.storage.RegionFile;
import net.minecraft.world.level.chunk.storage.RegionStorageInfo;
import net.minecraft.resources.ResourceKey;
import net.minecraft.world.level.Level;
import net.minecraft.resources.Identifier;

// Checks that a region file RegionIO wrote is readable as vanilla Anvil, using
// vanilla's own classes rather than our idea of the format:
//
//   * RegionFile parses the sector header and decompresses the chunk stream
//   * NbtIo reads the chunk NBT
//   * the root is flat -- no "Level" wrapper, which is where chunk data lived
//     until 1.18 and where nothing has looked since
//   * every section's Y is a ByteTag, the type SerializableChunkData reads
//   * every packed palette array is accepted by vanilla's own SimpleBitStorage
//     at the width vanilla's own Strategy derives from the palette size, which
//     is the check that catches an array packed too tight
//
// It stops short of SerializableChunkData.parse, which needs a RegistryAccess
// carrying the datapack biome registry -- more scaffolding than this earns.
//
//   CP="versions/26.1.2/server-26.1.2.jar;$(find libraries -name '*.jar' | tr '\n' ';')"
//   javac -nowarn -cp "$CP" -d <out> tools/VanillaChunkFormatCheck.java
//   java -cp "<out>;$CP" VanillaChunkFormatCheck <world>/region/r.-2.-2.mca
//
// Strategy.getConfigurationForPaletteSize is protected and the server jar is
// signed, so this cannot simply live in vanilla's package -- it is reached by
// reflection instead. Restating the width table here would defeat the point of
// checking against vanilla rather than against our reading of vanilla.
public final class VanillaChunkFormatCheck {
    private static int failures = 0;

    public static void main(String[] args) throws Exception {
        if (args.length != 1) {
            System.err.println("usage: VanillaChunkFormatCheck <region file.mca>");
            System.exit(2);
        }
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();

        Path file = Paths.get(args[0]);
        Path folder = file.getParent();
        RegionStorageInfo info = new RegionStorageInfo(
                "regionio",
                ResourceKey.create(ResourceKey.createRegistryKey(Identifier.withDefaultNamespace("dimension")),
                                   Identifier.withDefaultNamespace("overworld")),
                "chunk");

        // The width maths only reads bitsInStorage(), which for a Global
        // configuration is the bit count it was handed, so the IdMap a Strategy
        // is built over does not affect it. Reusing the block-state registry for
        // both keeps this tool from needing the datapack biome registry.
        IdMap<?> anyMap = Block.BLOCK_STATE_REGISTRY;
        Strategy<?> blockStrategy = Strategy.createForBlockStates(anyMap);
        Strategy<?> biomeStrategy = Strategy.createForBiomes(anyMap);

        int chunks = 0;
        try (RegionFile region = new RegionFile(info, file, folder, true)) {
            for (int lz = 0; lz < 32; lz++) {
                for (int lx = 0; lx < 32; lx++) {
                    ChunkPos pos = new ChunkPos(lx, lz);
                    if (!region.hasChunk(pos)) continue;
                    try (DataInputStream in = region.getChunkDataInputStream(pos)) {
                        if (in == null) continue;
                        CompoundTag root = NbtIo.read(in, NbtAccounter.unlimitedHeap());
                        checkChunk(root, blockStrategy, biomeStrategy);
                        chunks++;
                    }
                }
            }
        }
        if (chunks == 0) {
            System.out.println("FAIL: the region file contains no chunks");
            System.exit(1);
        }
        System.out.printf("checked %d chunks, %d failures%n", chunks, failures);
        System.exit(failures == 0 ? 0 : 1);
    }

    private static void checkChunk(CompoundTag root, Strategy<?> blocks, Strategy<?> biomes) {
        if (root.get("Level") != null) {
            fail("chunk NBT still nests under \"Level\"");
        }
        for (String key : new String[]{"xPos", "yPos", "zPos", "Status", "sections", "Heightmaps"}) {
            if (root.get(key) == null) fail("root is missing \"" + key + "\"");
        }
        ListTag sections = root.getListOrEmpty("sections");
        if (sections.isEmpty()) fail("chunk has no sections");
        for (int i = 0; i < sections.size(); i++) {
            CompoundTag section = sections.getCompoundOrEmpty(i);
            Tag y = section.get("Y");
            if (!(y instanceof ByteTag)) {
                fail("section " + i + " Y is " + (y == null ? "absent" : y.getClass().getSimpleName())
                     + ", vanilla reads it with getByteOr");
            }
            checkContainer(section, "block_states", blocks, 4096, i);
            checkContainer(section, "biomes", biomes, 64, i);
        }
    }

    private static void checkContainer(CompoundTag section, String key, Strategy<?> strategy, int entries, int index) {
        CompoundTag container = section.getCompoundOrEmpty(key);
        if (container.isEmpty()) {
            fail("section " + index + " has no \"" + key + "\"");
            return;
        }
        int paletteSize = container.getListOrEmpty("palette").size();
        if (paletteSize == 0) {
            fail("section " + index + " " + key + " has an empty palette");
            return;
        }
        int bits = bitsInStorage(strategy, paletteSize);
        Tag data = container.get("data");
        if (bits == 0) {
            if (data != null) {
                fail("section " + index + " " + key + ": palette of " + paletteSize
                     + " needs no data array but one is present");
            }
            return;
        }
        if (!(data instanceof LongArrayTag array)) {
            fail("section " + index + " " + key + ": palette of " + paletteSize
                 + " needs a " + bits + "-bit data array, found " + (data == null ? "none" : data.getClass().getSimpleName()));
            return;
        }
        try {
            // Throws InitializationException when the long count does not match
            // the width vanilla expects -- exactly the failure a too-tightly
            // packed array produces.
            new SimpleBitStorage(bits, entries, array.getAsLongArray());
        } catch (RuntimeException e) {
            fail("section " + index + " " + key + ": palette of " + paletteSize
                 + " at " + bits + " bits: " + e.getMessage());
        }
    }

    // bitsInStorage asks vanilla how wide a container with this many palette
    // entries is stored on disk.
    private static int bitsInStorage(Strategy<?> strategy, int paletteSize) {
        try {
            Method forSize = Strategy.class.getDeclaredMethod("getConfigurationForPaletteSize", int.class);
            forSize.setAccessible(true);
            Object configuration = forSize.invoke(strategy, paletteSize);
            Method bits = configuration.getClass().getMethod("bitsInStorage");
            bits.setAccessible(true);
            return (Integer) bits.invoke(configuration);
        } catch (ReflectiveOperationException e) {
            throw new IllegalStateException("cannot reach Strategy.getConfigurationForPaletteSize", e);
        }
    }

    private static void fail(String message) {
        System.out.println("FAIL: " + message);
        failures++;
    }
}
