import java.lang.reflect.Method;
import net.minecraft.world.level.biome.Climate;

// Prints fixed Climate.ParameterPoint fitness vectors from the official jar.
public final class VanillaClimateVectors {
    public static void main(String[] args) throws Exception {
        Method fitness = Climate.ParameterPoint.class.getDeclaredMethod("fitness", Climate.TargetPoint.class);
        fitness.setAccessible(true);
        vector(fitness, "inside", ranges(0, 10, 0), point(5, 0));
        vector(fitness, "below", ranges(0, 10, 0), point(-3, 0));
        vector(fitness, "above", ranges(0, 10, 0), point(14, 0));
        vector(fitness, "offset", ranges(0, 10, 7), point(5, 0));

        Climate.Parameter zero = new Climate.Parameter(0, 0);
        Climate.Parameter depth = new Climate.Parameter(20, 20);
        Climate.Parameter weird = new Climate.Parameter(30, 30);
        Climate.ParameterPoint axes = new Climate.ParameterPoint(zero, zero, zero, zero, depth, weird, 0);
        Climate.TargetPoint target = new Climate.TargetPoint(0, 0, 0, 0, 23, 35);
        vector(fitness, "depth-weirdness-order", axes, target);
    }

    private static Climate.ParameterPoint ranges(long min, long max, long offset) {
        Climate.Parameter range = new Climate.Parameter(min, max);
        Climate.Parameter zero = new Climate.Parameter(0, 0);
        return new Climate.ParameterPoint(range, zero, zero, zero, zero, zero, offset);
    }

    private static Climate.TargetPoint point(long temperature, long depth) {
        return new Climate.TargetPoint(temperature, 0, 0, 0, depth, 0);
    }

    private static void vector(Method fitness, String name, Climate.ParameterPoint p, Climate.TargetPoint target) throws Exception {
        System.out.println(name + "=" + fitness.invoke(p, target));
    }
}
