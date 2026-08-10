package protocol

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestReadPacketCompressionThreshold(t *testing.T) {
	for _, tc := range []struct {
		name      string
		writeAt   int32
		readAt    int32
		wantError error
	}{
		{name: "compressed at threshold", writeAt: 4, readAt: 4},
		{name: "compressed below threshold", writeAt: 4, readAt: 9, wantError: ErrBadCompression},
		{name: "uncompressed below threshold", writeAt: 16, readAt: 16},
		{name: "uncompressed at threshold", writeAt: 16, readAt: 4, wantError: ErrBadCompression},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := AppendPacket(nil, tc.writeAt, 3, []byte("payload"))
			pkt, err := ReadPacket(bufio.NewReader(bytes.NewReader(frame)), tc.readAt)
			if !errors.Is(err, tc.wantError) {
				t.Fatalf("ReadPacket error = %v, want %v", err, tc.wantError)
			}
			if tc.wantError == nil && (pkt.ID != 3 || string(pkt.Data) != "payload") {
				t.Fatalf("packet = id %d data %q", pkt.ID, pkt.Data)
			}
		})
	}
}

func TestReadPacketRejectsWrongDecompressedLength(t *testing.T) {
	frame := AppendPacket(nil, 1, 3, []byte("payload"))
	r := NewReader(frame)
	length, err := r.VarInt()
	if err != nil {
		t.Fatal(err)
	}
	payload := append([]byte(nil), frame[len(frame)-int(length):]...)
	payload[0]++
	bad := AppendVarInt(nil, int32(len(payload)))
	bad = append(bad, payload...)
	if _, err := ReadPacket(bufio.NewReader(bytes.NewReader(bad)), 1); err == nil {
		t.Fatal("accepted compressed payload shorter than its declared length")
	}
}

func TestReadPacketRejectsTrailingCompressedData(t *testing.T) {
	frame := AppendPacket(nil, 1, 3, []byte("payload"))
	r := NewReader(frame)
	length, err := r.VarInt()
	if err != nil {
		t.Fatal(err)
	}
	payload := append([]byte(nil), frame[len(frame)-int(length):]...)
	payload = append(payload, 0)
	bad := AppendVarInt(nil, int32(len(payload)))
	bad = append(bad, payload...)
	if _, err := ReadPacket(bufio.NewReader(bytes.NewReader(bad)), 1); !errors.Is(err, ErrBadCompression) {
		t.Fatalf("error = %v, want ErrBadCompression", err)
	}
}

type shortWriter struct{ buf bytes.Buffer }

func (w *shortWriter) Write(p []byte) (int, error) {
	if len(p) > 2 {
		p = p[:2]
	}
	return w.buf.Write(p)
}

func TestWritePacketCompletesShortWrites(t *testing.T) {
	w := new(shortWriter)
	if err := WritePacket(w, -1, 7, []byte("body")); err != nil {
		t.Fatal(err)
	}
	pkt, err := ReadPacket(bufio.NewReader(bytes.NewReader(w.buf.Bytes())), -1)
	if err != nil {
		t.Fatal(err)
	}
	if pkt.ID != 7 || string(pkt.Data) != "body" {
		t.Fatalf("packet = id %d data %q", pkt.ID, pkt.Data)
	}
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }

func TestWritePacketRejectsNoProgress(t *testing.T) {
	if err := WritePacket(zeroWriter{}, -1, 1, nil); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("error = %v, want io.ErrShortWrite", err)
	}
}
