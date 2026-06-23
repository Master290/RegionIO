package nbt

import (
	"errors"
	"unicode/utf16"
)

// errBadMUTF8 is returned when a modified-UTF-8 byte sequence is malformed.
var errBadMUTF8 = errors.New("nbt: invalid modified UTF-8")

// encodeModifiedUTF8 encodes s as Java modified UTF-8: ASCII stays one byte,
// NUL and code points up to U+07FF take two bytes, U+0800..U+FFFF take three,
// and supplementary code points are split into a surrogate pair (six bytes).
func encodeModifiedUTF8(dst []byte, s string) []byte {
	for _, u := range utf16.Encode([]rune(s)) {
		switch {
		case u >= 0x0001 && u <= 0x007F:
			dst = append(dst, byte(u))
		case u == 0 || u <= 0x07FF:
			dst = append(dst,
				0xC0|byte(u>>6),
				0x80|byte(u&0x3F))
		default:
			dst = append(dst,
				0xE0|byte(u>>12),
				0x80|byte((u>>6)&0x3F),
				0x80|byte(u&0x3F))
		}
	}
	return dst
}

// decodeModifiedUTF8 decodes b (length-delimited modified UTF-8) into a string.
func decodeModifiedUTF8(b []byte) (string, error) {
	units := make([]uint16, 0, len(b))
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c&0x80 == 0: // 0xxxxxxx
			units = append(units, uint16(c))
			i++
		case c&0xE0 == 0xC0: // 110xxxxx 10xxxxxx
			if i+1 >= len(b) || b[i+1]&0xC0 != 0x80 {
				return "", errBadMUTF8
			}
			units = append(units, uint16(c&0x1F)<<6|uint16(b[i+1]&0x3F))
			i += 2
		case c&0xF0 == 0xE0: // 1110xxxx 10xxxxxx 10xxxxxx
			if i+2 >= len(b) || b[i+1]&0xC0 != 0x80 || b[i+2]&0xC0 != 0x80 {
				return "", errBadMUTF8
			}
			units = append(units, uint16(c&0x0F)<<12|uint16(b[i+1]&0x3F)<<6|uint16(b[i+2]&0x3F))
			i += 3
		default:
			return "", errBadMUTF8
		}
	}
	return string(utf16.Decode(units)), nil
}
