// Prints the worldgen/structure registry entries in REGISTRY ORDER (the
// order the registry holder list iterates - the order that structure
// groupingBy + list indexing in applyBiomeDecoration uses), with each
// structure's step ordinal. This pins the setFeatureSeed index counter for
// every structure step.
//
//   CP="versions/26.1.2/server-26.1.2.jar;$(find libraries -name '*.jar' | tr '\n' ';')"
//   javac -nowarn -cp "$CP" -d <out> tools/VanillaStructureOrderProbe.java
//   java -cp "<out>;$CP" VanillaStructureOrderProbe
import net.minecraft.SharedConstants;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.core.registries.Registries;
import net.minecraft.data.registries.VanillaRegistries;
import net.minecraft.server.Bootstrap;
import net.minecraft.world.level.levelgen.GenerationStep;

public final class VanillaStructureOrderProbe {

    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();
        var lookup = VanillaRegistries.createLookup();
        var registry = lookup.lookupOrThrow(Registries.STRUCTURE);
        int idx = 0;
        for (var holder : registry.listElements().toList()) {
            var key = holder.key().identifier();
            var structure = holder.value();
            System.out.println("REG " + idx + " " + key + " step=" + structure.step().getName()
                + " (ordinal " + structure.step().ordinal() + ")");
            idx++;
        }
    }
}
