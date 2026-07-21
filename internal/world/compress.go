package world

import (
	"bytes"
	"compress/zlib"
	"io"
)

// compress.go wraps compress/zlib for region-file chunk payloads. zlib is the
// default compression for Anvil .mca chunk records (compression type 2).

// zlibDeflate compresses src into a new byte slice.
func zlibDeflate(src []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(src); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// zlibInflate decompresses src (a zlib stream). It returns an error if src is
// not a valid zlib payload.
func zlibInflate(src []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return out, nil
}
