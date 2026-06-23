// Package protocol implements the wire-level primitives of the Minecraft
// Java Edition protocol (version 26.1.2, protocol 775).
//
// All multi-byte numeric fields are big-endian. Length-prefixed and
// frequently-used integers use the LEB128-style VarInt/VarLong encoding.
package protocol

import (
	"errors"
	"io"
)

// Protocol constants for the targeted Minecraft version.
const (
	// ProtocolVersion is the handshake protocol number for 26.1.2.
	ProtocolVersion = 775
	// GameVersion is the human-readable version string.
	GameVersion = "26.1.2"
)

// State is a connection state as negotiated during the handshake. The numeric
// values of Status/Login match the "next state" field of the handshake packet.
type State int

const (
	StateHandshaking State = iota
	StateStatus
	StateLogin
	StateConfiguration
	StatePlay
)

func (s State) String() string {
	switch s {
	case StateHandshaking:
		return "handshaking"
	case StateStatus:
		return "status"
	case StateLogin:
		return "login"
	case StateConfiguration:
		return "configuration"
	case StatePlay:
		return "play"
	default:
		return "unknown"
	}
}

// Protocol limits guarding against malicious or malformed input.
const (
	// MaxVarIntLen is the maximum number of bytes a 32-bit VarInt may occupy.
	MaxVarIntLen = 5
	// MaxVarLongLen is the maximum number of bytes a 64-bit VarLong may occupy.
	MaxVarLongLen = 10
	// MaxStringLen bounds decoded strings to the protocol's 32767-char limit.
	MaxStringLen = 32767
	// MaxPacketSize bounds a single uncompressed packet body.
	MaxPacketSize = 2 * 1024 * 1024
)

var (
	// ErrVarIntTooBig is returned when a VarInt/VarLong exceeds its byte limit.
	ErrVarIntTooBig = errors.New("protocol: varint is too big")
	// ErrStringTooLong is returned when a string exceeds MaxStringLen.
	ErrStringTooLong = errors.New("protocol: string too long")
	// ErrPacketTooLarge is returned when a packet length exceeds MaxPacketSize.
	ErrPacketTooLarge = errors.New("protocol: packet too large")
)

// ReadVarInt reads a 32-bit VarInt from r, returning the value and the number
// of bytes consumed.
func ReadVarInt(r io.ByteReader) (value int32, n int, err error) {
	var result uint32
	for i := 0; i < MaxVarIntLen; i++ {
		b, e := r.ReadByte()
		if e != nil {
			return 0, i, e
		}
		result |= uint32(b&0x7F) << (7 * i)
		if b&0x80 == 0 {
			return int32(result), i + 1, nil
		}
	}
	return 0, MaxVarIntLen, ErrVarIntTooBig
}

// ReadVarLong reads a 64-bit VarLong from r.
func ReadVarLong(r io.ByteReader) (value int64, n int, err error) {
	var result uint64
	for i := 0; i < MaxVarLongLen; i++ {
		b, e := r.ReadByte()
		if e != nil {
			return 0, i, e
		}
		result |= uint64(b&0x7F) << (7 * i)
		if b&0x80 == 0 {
			return int64(result), i + 1, nil
		}
	}
	return 0, MaxVarLongLen, ErrVarIntTooBig
}

// AppendVarInt encodes v as a VarInt and appends it to dst.
func AppendVarInt(dst []byte, v int32) []byte {
	u := uint32(v)
	for {
		if u&^0x7F == 0 {
			return append(dst, byte(u))
		}
		dst = append(dst, byte(u&0x7F)|0x80)
		u >>= 7
	}
}

// AppendVarLong encodes v as a VarLong and appends it to dst.
func AppendVarLong(dst []byte, v int64) []byte {
	u := uint64(v)
	for {
		if u&^uint64(0x7F) == 0 {
			return append(dst, byte(u))
		}
		dst = append(dst, byte(u&0x7F)|0x80)
		u >>= 7
	}
}

// VarIntLen returns the number of bytes the VarInt encoding of v occupies.
func VarIntLen(v int32) int {
	u := uint32(v)
	n := 1
	for u&^0x7F != 0 {
		u >>= 7
		n++
	}
	return n
}
