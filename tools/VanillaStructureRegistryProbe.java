// Prints the registry order of the worldgen/structure registry with each
// structure's generation step, so the setFeatureSeed(decorationSeed, index,
// step) counter for structure piece placement can be reconstructed.
//
//   CP="versions/26.1.2/server-26.1.2.jar;$(find libraries -name '*.jar' | tr '\n' ';')"
//   javac -nowarn -cp "$CP" -d <out> tools/VanillaStructureRegistryProbe.java
//   java -cp "<out>;$CP" VanillaStructureRegistryProbe
import net.minecraft.SharedConstants;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.server.Bootstrap;

public final class VanillaStructureRegistryProbe {
    public static void main(String[] args) {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();
        BuiltInRegistries.STRUCTURE.forEach(structure -> {
            System.out.println(BuiltInRegistries.STRUCTURE.getKey(structure) + " step=" + structure.step());
        });
    }
}
