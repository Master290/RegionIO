package protocol

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"io"
)

// Packet is a decoded frame: a packet ID plus its raw body bytes.
// The body excludes the ID and any length/compression prefixes.
type Packet struct {
	ID   int32
	Data []byte
}

// Body returns a Reader positioned at the start of the packet body.
func (p Packet) Body() *Reader { return NewReader(p.Data) }

// ReadPacket reads one frame from br.
//
// When threshold < 0 the uncompressed format is used:
//
//	VarInt length | VarInt packet ID | body
//
// When threshold >= 0 the compressed format is used:
//
//	VarInt packet length | VarInt data length | (zlib or raw) packet ID + body
//
// A data length of 0 means the payload is stored uncompressed (its
// uncompressed size was below the threshold).
func ReadPacket(br *bufio.Reader, threshold int32) (Packet, error) {
	length, _, err := ReadVarInt(br)
	if err != nil {
		return Packet{}, err
	}
	if length < 0 || int(length) > MaxPacketSize {
		return Packet{}, ErrPacketTooLarge
	}

	frame := make([]byte, length)
	if _, err := io.ReadFull(br, frame); err != nil {
		return Packet{}, err
	}

	if threshold < 0 {
		return parseIDBody(frame)
	}
	return parseCompressed(frame, threshold)
}

// parseCompressed handles a frame that begins with a Data Length VarInt.
func parseCompressed(frame []byte, threshold int32) (Packet, error) {
	r := NewReader(frame)
	dataLen, err := r.VarInt()
	if err != nil {
		return Packet{}, err
	}
	payload := frame[r.pos:]

	if dataLen == 0 {
		// Stored uncompressed.
		if len(payload) >= int(threshold) {
			return Packet{}, ErrBadCompression
		}
		return parseIDBody(payload)
	}
	if dataLen < 0 || int(dataLen) > MaxPacketSize {
		return Packet{}, ErrPacketTooLarge
	}
	if dataLen < threshold {
		return Packet{}, ErrBadCompression
	}

	compressed := bytes.NewReader(payload)
	zr, err := zlib.NewReader(compressed)
	if err != nil {
		return Packet{}, err
	}
	if multistream, ok := zr.(interface{ Multistream(bool) }); ok {
		multistream.Multistream(false)
	}

	out := make([]byte, dataLen)
	if _, err := io.ReadFull(zr, out); err != nil {
		zr.Close()
		return Packet{}, err
	}
	var extra [1]byte
	if n, err := zr.Read(extra[:]); n != 0 || err != io.EOF {
		zr.Close()
		return Packet{}, ErrBadCompression
	}
	if err := zr.Close(); err != nil || compressed.Len() != 0 {
		return Packet{}, ErrBadCompression
	}
	return parseIDBody(out)
}

// parseIDBody splits a VarInt packet ID off the front of buf.
func parseIDBody(buf []byte) (Packet, error) {
	r := NewReader(buf)
	id, err := r.VarInt()
	if err != nil {
		return Packet{}, err
	}
	return Packet{ID: id, Data: buf[r.pos:]}, nil
}

// WritePacket writes one frame to w with the given ID and body, using the
// uncompressed format when threshold < 0 and the compressed format otherwise.
func WritePacket(w io.Writer, threshold int32, id int32, body []byte) error {
	frame := AppendPacket(nil, threshold, id, body)
	for len(frame) > 0 {
		n, err := w.Write(frame)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(frame) {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}

// AppendPacket appends one fully-framed packet to dst and returns the result.
// The produced bytes are identical to what WritePacket would write, so callers
// may cache them and replay via a raw write.
func AppendPacket(dst []byte, threshold int32, id int32, body []byte) []byte {
	if threshold < 0 {
		return appendUncompressed(dst, id, body)
	}
	return appendCompressed(dst, threshold, id, body)
}

func appendUncompressed(dst []byte, id int32, body []byte) []byte {
	total := VarIntLen(id) + len(body)
	dst = AppendVarInt(dst, int32(total))
	dst = AppendVarInt(dst, id)
	return append(dst, body...)
}

func appendCompressed(dst []byte, threshold int32, id int32, body []byte) []byte {
	// raw = packet ID + body, the unit that compression applies to.
	raw := make([]byte, 0, VarIntLen(id)+len(body))
	raw = AppendVarInt(raw, id)
	raw = append(raw, body...)

	var payload []byte
	if len(raw) >= int(threshold) {
		var buf bytes.Buffer
		zw := zlib.NewWriter(&buf)
		zw.Write(raw)
		zw.Close()
		// Data Length = uncompressed size, then the compressed bytes.
		payload = AppendVarInt(make([]byte, 0, VarIntLen(int32(len(raw)))+buf.Len()), int32(len(raw)))
		payload = append(payload, buf.Bytes()...)
	} else {
		// Below threshold: Data Length = 0, raw stored verbatim.
		payload = AppendVarInt(make([]byte, 0, 1+len(raw)), 0)
		payload = append(payload, raw...)
	}

	dst = AppendVarInt(dst, int32(len(payload)))
	return append(dst, payload...)
}
