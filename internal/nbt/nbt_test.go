package nbt

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
)

// TestGoldenHelloWorld checks the canonical NBT example: a named-root compound
// {"hello world": {"name": "Bananrama"}}.
func TestGoldenHelloWorld(t *testing.T) {
	root := NewCompound().Set("name", String("Bananrama"))
	got := MarshalNamed("hello world", root)

	want := []byte{
		0x0a, 0x00, 0x0b, // TAG_Compound, name len 11
		'h', 'e', 'l', 'l', 'o', ' ', 'w', 'o', 'r', 'l', 'd',
		0x08, 0x00, 0x04, 'n', 'a', 'm', 'e', // TAG_String, name len 4
		0x00, 0x09, // string len 9
		'B', 'a', 'n', 'a', 'n', 'r', 'a', 'm', 'a',
		0x00, // TAG_End
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("golden mismatch:\n got=%x\nwant=%x", got, want)
	}
}

// TestNetworkRootHasNoName verifies the 1.20.2+ network root omits the name.
func TestNetworkRootHasNoName(t *testing.T) {
	got := Marshal(NewCompound().Set("a", Byte(1)))
	want := []byte{
		0x0a,                  // TAG_Compound (no name)
		0x01, 0x00, 0x01, 'a', // TAG_Byte, name len 1
		0x01, // value 1
		0x00, // TAG_End
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("network root mismatch:\n got=%x\nwant=%x", got, want)
	}
}

// TestRoundTrip encodes a richly-nested compound and decodes it back.
func TestRoundTrip(t *testing.T) {
	root := NewCompound().
		Set("b", Byte(-7)).
		Set("s", Short(300)).
		Set("i", Int(-123456)).
		Set("l", Long(1<<40)).
		Set("f", Float(3.5)).
		Set("d", Double(-2.25)).
		Set("str", String("héllo")).
		Set("bytes", ByteArray{1, 2, 3}).
		Set("ints", IntArray{10, -20, 30}).
		Set("longs", LongArray{1, 2}).
		Set("list", List{ElemID: TagInt, Elems: []Tag{Int(1), Int(2), Int(3)}}).
		Set("empty", List{ElemID: TagString}).
		Set("nested", NewCompound().Set("x", String("y")))

	enc := Marshal(root)
	dec, err := Unmarshal(enc)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	reenc := Marshal(dec)
	if !bytes.Equal(enc, reenc) {
		t.Fatalf("round-trip bytes differ:\n first=%x\nsecond=%x", enc, reenc)
	}
}

// TestModifiedUTF8 round-trips strings including NUL and supplementary chars.
func TestModifiedUTF8(t *testing.T) {
	cases := []string{
		"",
		"plain ascii",
		"with\x00nul",
		"café — français",
		"emoji 😀 supplementary",
	}
	for _, s := range cases {
		enc := encodeModifiedUTF8(nil, s)
		// NUL must be escaped as 0xC0 0x80, never a literal zero byte.
		if bytes.IndexByte(enc, 0) >= 0 {
			t.Fatalf("encoding of %q contains a literal NUL byte", s)
		}
		dec, err := decodeModifiedUTF8(enc)
		if err != nil {
			t.Fatalf("decode %q: %v", s, err)
		}
		if dec != s {
			t.Fatalf("round-trip mismatch: got %q want %q", dec, s)
		}
	}
}

// TestDecodeNamedRoundTrip checks the named-format decode path.
func TestDecodeNamedRoundTrip(t *testing.T) {
	root := NewCompound().Set("k", Int(42))
	enc := MarshalNamed("root", root)
	name, dec, err := UnmarshalNamed(enc)
	if err != nil {
		t.Fatalf("unmarshal named: %v", err)
	}
	if name != "root" {
		t.Fatalf("root name = %q, want %q", name, "root")
	}
	if !reflect.DeepEqual(Marshal(dec), Marshal(root)) {
		t.Fatal("named decode produced different tree")
	}
}

// TestTruncatedInput ensures the decoder rejects short buffers without panic.
func TestTruncatedInput(t *testing.T) {
	enc := Marshal(NewCompound().Set("x", Long(1)))
	for n := 0; n < len(enc); n++ {
		if _, err := Unmarshal(enc[:n]); err == nil {
			t.Fatalf("expected error decoding %d/%d bytes", n, len(enc))
		}
	}
}

func TestDecodeRejectsOversizedArraysWithoutPanicking(t *testing.T) {
	for _, id := range []byte{TagByteArray, TagIntArray, TagLongArray} {
		input := []byte{id, 0, 0, 0, 0}
		binary.BigEndian.PutUint32(input[1:], ^uint32(0))
		if _, err := Unmarshal(input); err == nil {
			t.Errorf("tag %d: accepted negative array length", id)
		}
	}
}

func TestDecodeRejectsArrayLargerThanInput(t *testing.T) {
	input := []byte{TagLongArray, 0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 1}
	if _, err := Unmarshal(input); err == nil {
		t.Fatal("accepted two-long array with one long of input")
	}
}

func TestDecodeNestingLimit(t *testing.T) {
	input := []byte{TagList}
	for i := 0; i <= maxDecodeDepth; i++ {
		input = append(input, TagList, 0, 0, 0, 1)
	}
	input = append(input, TagByte, 0, 0, 0, 1, 0)
	if _, err := Unmarshal(input); err == nil {
		t.Fatal("accepted NBT deeper than the decoder limit")
	}
}

func FuzzUnmarshalNeverPanics(f *testing.F) {
	f.Add(Marshal(NewCompound().Set("value", Int(42))))
	f.Add([]byte{TagLongArray, 0xff, 0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, input []byte) {
		_, _ = Unmarshal(input)
	})
}
