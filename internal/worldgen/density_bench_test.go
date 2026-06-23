package worldgen
import "testing"
func BenchmarkFinalDensity(b *testing.B) {
	od,_ := LoadOverworldFinalDensity(0)
	b.ReportAllocs()
	for i:=0;i<b.N;i++ { _ = od.Final.Compute(FunctionContext{X:float64(i&255),Y:64,Z:float64(i>>8)}) }
}
