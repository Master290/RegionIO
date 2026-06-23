package nbt

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Marshal encodes root in the network format: the root tag's type byte followed
// by its payload, with no root name (Minecraft 1.20.2+).
func Marshal(root Tag) []byte {
	dst := []byte{root.ID()}
	return appendPayload(dst, root)
}

// MarshalNamed encodes root in the classic named format: type byte, root name,
// then payload. Used for files and legacy framing.
func MarshalNamed(name string, root Tag) []byte {
	dst := []byte{root.ID()}
	dst = appendString(dst, name)
	return appendPayload(dst, root)
}

// appendString writes a modified-UTF-8 string with an unsigned-short length.
func appendString(dst []byte, s string) []byte {
	start := len(dst)
	dst = append(dst, 0, 0) // length placeholder
	dst = encodeModifiedUTF8(dst, s)
	binary.BigEndian.PutUint16(dst[start:], uint16(len(dst)-start-2))
	return dst
}

// appendPayload writes a tag's payload (no type byte, no name).
func appendPayload(dst []byte, t Tag) []byte {
	switch v := t.(type) {
	case Byte:
		return append(dst, byte(v))
	case Short:
		return binary.BigEndian.AppendUint16(dst, uint16(v))
	case Int:
		return binary.BigEndian.AppendUint32(dst, uint32(v))
	case Long:
		return binary.BigEndian.AppendUint64(dst, uint64(v))
	case Float:
		return binary.BigEndian.AppendUint32(dst, math.Float32bits(float32(v)))
	case Double:
		return binary.BigEndian.AppendUint64(dst, math.Float64bits(float64(v)))
	case ByteArray:
		dst = binary.BigEndian.AppendUint32(dst, uint32(len(v)))
		return append(dst, v...)
	case String:
		return appendString(dst, string(v))
	case IntArray:
		dst = binary.BigEndian.AppendUint32(dst, uint32(len(v)))
		for _, n := range v {
			dst = binary.BigEndian.AppendUint32(dst, uint32(n))
		}
		return dst
	case LongArray:
		dst = binary.BigEndian.AppendUint32(dst, uint32(len(v)))
		for _, n := range v {
			dst = binary.BigEndian.AppendUint64(dst, uint64(n))
		}
		return dst
	case List:
		return appendList(dst, v)
	case *Compound:
		return appendCompound(dst, v)
	default:
		panic(fmt.Sprintf("nbt: cannot encode %T", t))
	}
}

func appendList(dst []byte, l List) []byte {
	elemID := l.ElemID
	if len(l.Elems) == 0 {
		elemID = TagEnd // Java writes TagEnd for empty lists.
	}
	dst = append(dst, elemID)
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(l.Elems)))
	for _, e := range l.Elems {
		dst = appendPayload(dst, e)
	}
	return dst
}

func appendCompound(dst []byte, c *Compound) []byte {
	for _, k := range c.keys {
		t := c.m[k]
		dst = append(dst, t.ID())
		dst = appendString(dst, k)
		dst = appendPayload(dst, t)
	}
	return append(dst, TagEnd)
}
