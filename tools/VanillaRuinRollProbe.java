// Prints, for each piece of the seed-12345 ocean ruin at origin (112,50,80),
// every template cell with the world position vanilla feeds to
// BlockRotProcessor and the keep/drop verdict — by running the real 26.1.2
// StructureTemplate.processBlockInfos with a recording StructurePlaceSettings
// and the exact processor set OceanRuinPieces uses. This settles which
// position formula vanilla actually rolls on.
//
//   CP="versions/26.1.2/server-26.1.2.jar;$(find libraries -name '*.jar' | tr '\n' ';')"
//   javac -nowarn -cp "$CP" -d <out> tools/VanillaRuinRollProbe.java
//   java -cp "<out>;$CP" VanillaRuinRollProbe
import java.io.DataInputStream;
import java.io.File;
import java.lang.reflect.Field;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import java.util.zip.ZipEntry;
import java.util.zip.ZipFile;

import net.minecraft.SharedConstants;
import net.minecraft.core.BlockPos;
import net.minecraft.core.HolderGetter;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.nbt.CompoundTag;
import net.minecraft.nbt.NbtAccounter;
import net.minecraft.nbt.NbtIo;
import net.minecraft.server.Bootstrap;
import net.minecraft.util.RandomSource;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.Rotation;
import net.minecraft.world.level.levelgen.structure.templatesystem.BlockIgnoreProcessor;
import net.minecraft.world.level.levelgen.structure.templatesystem.BlockRotProcessor;
import net.minecraft.world.level.levelgen.structure.templatesystem.StructurePlaceSettings;
import net.minecraft.world.level.levelgen.structure.templatesystem.StructureTemplate;

public final class VanillaRuinRollProbe {
    private static final String JAR = "versions/26.1.2/server-26.1.2.jar";
    private static final BlockPos ORIGIN = new BlockPos(112, 50, 80);

    // Records every getRandom(pos) the processors request, in call order.
    static final class RecordingSettings extends StructurePlaceSettings {
        final List<BlockPos> recorded = new ArrayList<>();
        @Override
        public RandomSource getRandom(BlockPos pos) {
            recorded.add(pos.immutable());
            return super.getRandom(pos);
        }
    }

    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();

        run("brick_2", "underwater_ruin/brick_2", 0.8f);
        run("cracked_2", "underwater_ruin/cracked_2", 0.7f);
        run("mossy_2", "underwater_ruin/mossy_2", 0.5f);
    }

    static void run(String label, String templatePath, float integrity) throws Exception {
        CompoundTag nbt;
        try (ZipFile zf = new ZipFile(new File(JAR))) {
            ZipEntry e = zf.getEntry("data/minecraft/structure/" + templatePath + ".nbt");
            if (e == null) throw new IllegalStateException("no template " + templatePath);
            try (DataInputStream din = new DataInputStream(zf.getInputStream(e))) {
                nbt = NbtIo.readCompressed(din, NbtAccounter.unlimitedHeap());
            }
        }
        StructureTemplate template = new StructureTemplate();
        @SuppressWarnings("unchecked")
        HolderGetter<Block> blocks = (HolderGetter<Block>) net.minecraft.core.registries.BuiltInRegistries.BLOCK;
        template.load(blocks, nbt);

        Field palettesField = StructureTemplate.class.getDeclaredField("palettes");
        palettesField.setAccessible(true);
        @SuppressWarnings("unchecked")
        List<StructureTemplate.Palette> palettes =
            (List<StructureTemplate.Palette>) palettesField.get(template);
        List<StructureTemplate.StructureBlockInfo> raw = palettes.get(0).blocks();

        RecordingSettings settings = new RecordingSettings();
        settings.setRotation(Rotation.COUNTERCLOCKWISE_90);
        settings.addProcessor(new BlockRotProcessor(integrity));
        settings.addProcessor(BlockIgnoreProcessor.STRUCTURE_AND_AIR);

        boolean allDropped = true;
        List<StructureTemplate.StructureBlockInfo> out = StructureTemplate.processBlockInfos(
            null, ORIGIN, BlockPos.ZERO, settings, raw);
        Set<Long> kept = new HashSet<>();
        for (StructureTemplate.StructureBlockInfo info : out) {
            kept.add(key(info.pos()));
            allDropped = false;
        }
        if (settings.recorded.size() != raw.size()) {
            System.err.println("unexpected call count " + settings.recorded.size() + " vs " + raw.size());
        }
        System.out.println("=== " + label + " integrity=" + integrity + " kept=" + out.size() + "/" + raw.size());
        for (int i = 0; i < raw.size(); i++) {
            StructureTemplate.StructureBlockInfo src = raw.get(i);
            BlockPos cur = settings.recorded.get(i);
            boolean k = kept.contains(key(cur));
            System.out.printf("%-9s local(%d,%d,%d) world(%d,%d,%d) keep=%b state=%s%n",
                label, src.pos().getX(), src.pos().getY(), src.pos().getZ(),
                cur.getX(), cur.getY(), cur.getZ(), k,
                BuiltInRegistries.BLOCK.getKey(src.state().getBlock()));
        }
    }

    static long key(BlockPos p) {
        return ((long) p.getX() & 0xFFFFL) << 32
            | ((long) p.getY() & 0xFFFFL) << 16
            | ((long) p.getZ() & 0xFFFFL);
    }
}