package protocol

import (
	"encoding/binary"
	"math"
)

const (
	lpVec3DataMask = uint64(1<<15 - 1)
	lpVec3MaxValue = 17179869183.0
	lpVec3MinValue = 1.0 / 32766.0
)

// LPVec3 reads Minecraft's variable-length low-precision vector encoding.
func (r *Reader) LPVec3() (x, y, z float64, err error) {
	first, err := r.ReadByte()
	if err != nil {
		return 0, 0, 0, err
	}
	if first == 0 {
		return 0, 0, 0, nil
	}
	second, err := r.ReadByte()
	if err != nil {
		return 0, 0, 0, err
	}
	upper, err := r.readN(4)
	if err != nil {
		return 0, 0, 0, err
	}

	packed := uint64(binary.BigEndian.Uint32(upper))<<16 | uint64(second)<<8 | uint64(first)
	scale := uint64(first & 3)
	if first&4 != 0 {
		continuation, err := r.VarInt()
		if err != nil {
			return 0, 0, 0, err
		}
		scale |= uint64(uint32(continuation)) << 2
	}

	unpack := func(shift uint) float64 {
		value := math.Min(float64((packed>>shift)&lpVec3DataMask), 32766.0)
		return (value*2.0/32766.0 - 1.0) * float64(scale)
	}
	return unpack(3), unpack(18), unpack(33), nil
}

// LPVec3 appends Minecraft's variable-length low-precision vector encoding.
func (w *Writer) LPVec3(x, y, z float64) *Writer {
	x = sanitizeLPVec3(x)
	y = sanitizeLPVec3(y)
	z = sanitizeLPVec3(z)
	maxAbs := math.Max(math.Abs(x), math.Max(math.Abs(y), math.Abs(z)))
	if maxAbs < lpVec3MinValue {
		return w.Byte(0)
	}

	scale := uint64(math.Ceil(maxAbs))
	header := scale
	continuation := scale > 3
	if continuation {
		header = scale&3 | 4
	}
	pack := func(value float64) uint64 {
		normalized := value / float64(scale)
		return uint64(math.Floor((normalized*0.5+0.5)*32766.0 + 0.5))
	}
	packed := header | pack(x)<<3 | pack(y)<<18 | pack(z)<<33

	w.Byte(byte(packed))
	w.Byte(byte(packed >> 8))
	w.Int32(int32(packed >> 16))
	if continuation {
		w.VarInt(int32(scale >> 2))
	}
	return w
}

func sanitizeLPVec3(value float64) float64 {
	if math.IsNaN(value) {
		return 0
	}
	return math.Max(-lpVec3MaxValue, math.Min(lpVec3MaxValue, value))
}
