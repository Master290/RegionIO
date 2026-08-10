package protocol

import (
	"bufio"
	"bytes"
	"testing"
)

func FuzzReadPacketNeverPanics(f *testing.F) {
	f.Add(AppendPacket(nil, -1, 0, nil))
	f.Add(AppendPacket(nil, 1, 42, []byte("payload")))
	f.Fuzz(func(t *testing.T, input []byte) {
		_, _ = ReadPacket(bufio.NewReader(bytes.NewReader(input)), 256)
		_, _ = ReadPacket(bufio.NewReader(bytes.NewReader(input)), -1)
	})
}
