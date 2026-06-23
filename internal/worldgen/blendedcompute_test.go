package worldgen
import ("math";"testing")
func TestBlendedCompute(t *testing.T){
	bn:=NewBlendedNoise(NewXoroshiro(42),0.25,0.125,80.0,160.0,8.0)
	cases:=[]struct{x,y,z float64;want float64}{
		{0,64,0,-0.012282880040235755},
		{100,40,-200,0.007126388725845459},
		{1234,80,-5678,-0.13408933571823986},
		{-37,128,99,-0.0932958728408956},
		{8,200,8,0.0075622598474688885},
	}
	for _,c:=range cases{
		got:=bn.Compute(FunctionContext{X:c.x,Y:c.y,Z:c.z})
		if math.Abs(got-c.want)>1e-12 { t.Fatalf("bn(%v,%v,%v)=%v want %v (diff %v)",c.x,c.y,c.z,got,c.want,got-c.want) }
	}
}
