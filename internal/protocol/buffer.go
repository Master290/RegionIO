package protocol

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
)

// ErrShortBuffer is returned when a read would exceed the buffer's contents.
var ErrShortBuffer = errors.New("protocol: unexpected end of buffer")

// Reader decodes typed protocol values from an in-memory packet body.
// It tracks a cursor and never reads past the underlying slice.
type Reader struct {
	buf []byte
	pos int
}

// NewReader returns a Reader over buf. The slice is not copied.
func NewReader(buf []byte) *Reader { return &Reader{buf: buf} }

// Remaining returns the number of unread bytes.
func (r *Reader) Remaining() int { return len(r.buf) - r.pos }

// ReadByte implements io.ByteReader so VarInt helpers can consume the Reader.
func (r *Reader) ReadByte() (byte, error) {
	if r.pos >= len(r.buf) {
		return 0, ErrShortBuffer
	}
	b := r.buf[r.pos]
	r.pos++
	return b, nil
}

// readN returns the next n bytes as a sub-slice of the underlying buffer.
func (r *Reader) readN(n int) ([]byte, error) {
	if n < 0 || r.Remaining() < n {
		return nil, ErrShortBuffer
	}
	b := r.buf[r.pos : r.pos+n]
	r.pos += n
	return b, nil
}

// VarInt reads a 32-bit VarInt.
func (r *Reader) VarInt() (int32, error) {
	v, _, err := ReadVarInt(r)
	return v, err
}

// VarLong reads a 64-bit VarLong.
func (r *Reader) VarLong() (int64, error) {
	v, _, err := ReadVarLong(r)
	return v, err
}

// Bool reads a single-byte boolean.
func (r *Reader) Bool() (bool, error) {
	b, err := r.ReadByte()
	return b != 0, err
}

// Uint16 reads a big-endian unsigned short.
func (r *Reader) Uint16() (uint16, error) {
	b, err := r.readN(2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b), nil
}

// Int32 reads a big-endian signed int.
func (r *Reader) Int32() (int32, error) {
	b, err := r.readN(4)
	if err != nil {
		return 0, err
	}
	return int32(binary.BigEndian.Uint32(b)), nil
}

// Int64 reads a big-endian signed long.
func (r *Reader) Int64() (int64, error) {
	b, err := r.readN(8)
	if err != nil {
		return 0, err
	}
	return int64(binary.BigEndian.Uint64(b)), nil
}

// Float64 reads a big-endian IEEE-754 double.
func (r *Reader) Float64() (float64, error) {
	b, err := r.readN(8)
	if err != nil {
		return 0, err
	}
	return math.Float64frombits(binary.BigEndian.Uint64(b)), nil
}

// Float32 reads a big-endian IEEE-754 float.
func (r *Reader) Float32() (float32, error) {
	b, err := r.readN(4)
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(binary.BigEndian.Uint32(b)), nil
}

// String reads a VarInt-length-prefixed UTF-8 string.
func (r *Reader) String() (string, error) {
	n, err := r.VarInt()
	if err != nil {
		return "", err
	}
	if n < 0 || int(n) > MaxStringLen*3 {
		return "", ErrStringTooLong
	}
	b, err := r.readN(int(n))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Position reads a block position packed into a single long (x:26, z:26, y:12).
func (r *Reader) Position() (x, y, z int, err error) {
	v, err := r.Int64()
	if err != nil {
		return 0, 0, 0, err
	}
	x = int(v >> 38)       // top 26 bits, sign-extended
	y = int(v << 52 >> 52) // low 12 bits, sign-extended
	z = int(v << 26 >> 38) // middle 26 bits, sign-extended
	return x, y, z, nil
}

// UUID reads a 128-bit UUID as two big-endian longs (16 bytes).
func (r *Reader) UUID() ([16]byte, error) {
	var u [16]byte
	b, err := r.readN(16)
	if err != nil {
		return u, err
	}
	copy(u[:], b)
	return u, nil
}

// Writer accumulates typed protocol values into a byte buffer that becomes a
// packet body. The zero value is ready to use.
type Writer struct {
	buf []byte
}

// NewWriter returns a Writer with an optional initial capacity hint.
func NewWriter(capacity int) *Writer {
	return &Writer{buf: make([]byte, 0, capacity)}
}

// Bytes returns the accumulated body. The slice aliases internal storage.
func (w *Writer) Bytes() []byte { return w.buf }

// Len returns the current body length.
func (w *Writer) Len() int { return len(w.buf) }

// VarInt appends a 32-bit VarInt.
func (w *Writer) VarInt(v int32) *Writer { w.buf = AppendVarInt(w.buf, v); return w }

// VarLong appends a 64-bit VarLong.
func (w *Writer) VarLong(v int64) *Writer { w.buf = AppendVarLong(w.buf, v); return w }

// Bool appends a single-byte boolean.
func (w *Writer) Bool(v bool) *Writer {
	if v {
		w.buf = append(w.buf, 1)
	} else {
		w.buf = append(w.buf, 0)
	}
	return w
}

// Byte appends a raw byte.
func (w *Writer) Byte(v byte) *Writer { w.buf = append(w.buf, v); return w }

// Uint16 appends a big-endian unsigned short.
func (w *Writer) Uint16(v uint16) *Writer {
	w.buf = binary.BigEndian.AppendUint16(w.buf, v)
	return w
}

// Int32 appends a big-endian signed int.
func (w *Writer) Int32(v int32) *Writer {
	w.buf = binary.BigEndian.AppendUint32(w.buf, uint32(v))
	return w
}

// Int64 appends a big-endian signed long.
func (w *Writer) Int64(v int64) *Writer {
	w.buf = binary.BigEndian.AppendUint64(w.buf, uint64(v))
	return w
}

// Float32 appends a big-endian IEEE-754 float.
func (w *Writer) Float32(v float32) *Writer {
	w.buf = binary.BigEndian.AppendUint32(w.buf, math.Float32bits(v))
	return w
}

// Float64 appends a big-endian IEEE-754 double.
func (w *Writer) Float64(v float64) *Writer {
	w.buf = binary.BigEndian.AppendUint64(w.buf, math.Float64bits(v))
	return w
}

// String appends a VarInt-length-prefixed UTF-8 string.
func (w *Writer) String(s string) *Writer {
	w.buf = AppendVarInt(w.buf, int32(len(s)))
	w.buf = append(w.buf, s...)
	return w
}

// Position appends a block position packed into a single long (x:26, z:26, y:12).
func (w *Writer) Position(x, y, z int) *Writer {
	v := (int64(x)&0x3FFFFFF)<<38 | (int64(z)&0x3FFFFFF)<<12 | (int64(y) & 0xFFF)
	return w.Int64(v)
}

// UUID appends a 128-bit UUID verbatim (16 bytes).
func (w *Writer) UUID(u [16]byte) *Writer { w.buf = append(w.buf, u[:]...); return w }

// Raw appends bytes verbatim.
func (w *Writer) Raw(b []byte) *Writer { w.buf = append(w.buf, b...); return w }

// WriteTo writes the accumulated body to dst.
func (w *Writer) WriteTo(dst io.Writer) (int64, error) {
	n, err := dst.Write(w.buf)
	return int64(n), err
}
