package nbt

import (
	"encoding/binary"
	"errors"
	"math"
)

var (
	errTruncated   = errors.New("nbt: truncated input")
	errBadTag      = errors.New("nbt: unknown tag id")
	errNegativeLen = errors.New("nbt: negative length")
	errTooDeep     = errors.New("nbt: nesting exceeds limit")
)

const maxDecodeDepth = 512

// decoder walks a byte slice, tracking a cursor.
type decoder struct {
	b     []byte
	pos   int
	depth int
}

// Unmarshal decodes a network-format payload (unnamed root) into a Tag.
func Unmarshal(b []byte) (Tag, error) {
	d := &decoder{b: b}
	id, err := d.u8()
	if err != nil {
		return nil, err
	}
	if id == TagEnd {
		return nil, nil
	}
	return d.payload(id)
}

// UnmarshalNamed decodes a classic named-format payload, returning the root
// name and tag.
func UnmarshalNamed(b []byte) (string, Tag, error) {
	d := &decoder{b: b}
	id, err := d.u8()
	if err != nil {
		return "", nil, err
	}
	if id == TagEnd {
		return "", nil, nil
	}
	name, err := d.str()
	if err != nil {
		return "", nil, err
	}
	t, err := d.payload(id)
	return name, t, err
}

func (d *decoder) need(n int) error {
	if n < 0 {
		return errNegativeLen
	}
	if d.pos+n > len(d.b) {
		return errTruncated
	}
	return nil
}

func (d *decoder) u8() (byte, error) {
	if err := d.need(1); err != nil {
		return 0, err
	}
	v := d.b[d.pos]
	d.pos++
	return v, nil
}

func (d *decoder) u16() (uint16, error) {
	if err := d.need(2); err != nil {
		return 0, err
	}
	v := binary.BigEndian.Uint16(d.b[d.pos:])
	d.pos += 2
	return v, nil
}

func (d *decoder) u32() (uint32, error) {
	if err := d.need(4); err != nil {
		return 0, err
	}
	v := binary.BigEndian.Uint32(d.b[d.pos:])
	d.pos += 4
	return v, nil
}

func (d *decoder) u64() (uint64, error) {
	if err := d.need(8); err != nil {
		return 0, err
	}
	v := binary.BigEndian.Uint64(d.b[d.pos:])
	d.pos += 8
	return v, nil
}

func (d *decoder) str() (string, error) {
	n, err := d.u16()
	if err != nil {
		return "", err
	}
	if err := d.need(int(n)); err != nil {
		return "", err
	}
	s, err := decodeModifiedUTF8(d.b[d.pos : d.pos+int(n)])
	d.pos += int(n)
	return s, err
}

// payload decodes a tag payload of the given type id.
func (d *decoder) payload(id byte) (Tag, error) {
	switch id {
	case TagByte:
		v, err := d.u8()
		return Byte(int8(v)), err
	case TagShort:
		v, err := d.u16()
		return Short(int16(v)), err
	case TagInt:
		v, err := d.u32()
		return Int(int32(v)), err
	case TagLong:
		v, err := d.u64()
		return Long(int64(v)), err
	case TagFloat:
		v, err := d.u32()
		return Float(math.Float32frombits(v)), err
	case TagDouble:
		v, err := d.u64()
		return Double(math.Float64frombits(v)), err
	case TagByteArray:
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		count, err := d.count(n, 1)
		if err != nil {
			return nil, err
		}
		out := make(ByteArray, count)
		copy(out, d.b[d.pos:d.pos+count])
		d.pos += count
		return out, nil
	case TagString:
		s, err := d.str()
		return String(s), err
	case TagIntArray:
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		count, err := d.count(n, 4)
		if err != nil {
			return nil, err
		}
		out := make(IntArray, count)
		for i := range out {
			v, err := d.u32()
			if err != nil {
				return nil, err
			}
			out[i] = int32(v)
		}
		return out, nil
	case TagLongArray:
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		count, err := d.count(n, 8)
		if err != nil {
			return nil, err
		}
		out := make(LongArray, count)
		for i := range out {
			v, err := d.u64()
			if err != nil {
				return nil, err
			}
			out[i] = int64(v)
		}
		return out, nil
	case TagList:
		return d.container(d.list)
	case TagCompound:
		return d.container(d.compound)
	default:
		return nil, errBadTag
	}
}

func (d *decoder) count(n uint32, width int) (int, error) {
	count := int64(int32(n))
	if count < 0 {
		return 0, errNegativeLen
	}
	if count*int64(width) > int64(len(d.b)-d.pos) {
		return 0, errTruncated
	}
	return int(count), nil
}

func (d *decoder) container(decode func() (Tag, error)) (Tag, error) {
	if d.depth >= maxDecodeDepth {
		return nil, errTooDeep
	}
	d.depth++
	defer func() { d.depth-- }()
	return decode()
}

func (d *decoder) list() (Tag, error) {
	elemID, err := d.u8()
	if err != nil {
		return nil, err
	}
	n, err := d.u32()
	if err != nil {
		return nil, err
	}
	count, err := d.count(n, 1) // every non-empty payload consumes at least one byte
	if err != nil {
		return nil, err
	}
	if elemID == TagEnd && count != 0 {
		return nil, errBadTag
	}
	l := List{ElemID: elemID, Elems: make([]Tag, 0, count)}
	for i := 0; i < count; i++ {
		t, err := d.payload(elemID)
		if err != nil {
			return nil, err
		}
		l.Elems = append(l.Elems, t)
	}
	return l, nil
}

func (d *decoder) compound() (Tag, error) {
	c := NewCompound()
	for {
		id, err := d.u8()
		if err != nil {
			return nil, err
		}
		if id == TagEnd {
			return c, nil
		}
		name, err := d.str()
		if err != nil {
			return nil, err
		}
		t, err := d.payload(id)
		if err != nil {
			return nil, err
		}
		c.Set(name, t)
	}
}
