// Prints the worldgen/structure registry entries in Registry.stream() order -
// the HashMap(byLocation).keySet() order that applyBiomeDecoration's
// groupingBy actually sees. This is the definitive structure-index counter.
//
//   CP="versions/26.1.2/server-26.1.2.jar;$(find libraries -name '*.jar' | tr '\n' ';')"
//   javac -nowarn -cp "$CP" -d <out> tools/VanillaStructureStreamOrderProbe.java
//   java -cp "<out>;$CP" VanillaStructureStreamOrderProbe
import net.minecraft.SharedConstants;
import net.minecraft.core.registries.Registries;
import net.minecraft.data.registries.VanillaRegistries;
import net.minecraft.server.Bootstrap;

public final class VanillaStructureStreamOrderProbe {

    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();
        var lookup = VanillaRegistries.createLookup();
        var registry = lookup.lookupOrThrow(Registries.STRUCTURE);
        int idx = 0;
        // Registry.stream() iterates the spliterator of the underlying map.
        for (var structure : java.util.stream.StreamSupport.stream(registry.spliterator(), false).toList()) {
            var keyOpt = registry.getResourceKey(structure);
            var key = keyOpt.map(k -> k.identifier().toString()).orElse("?");
            System.out.println("STREAM " + idx + " " + key + " step=" + structure.step().getName());
            idx++;
        }
    }
}
