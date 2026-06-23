// Package nbt implements Minecraft's Named Binary Tag format, including the
// network variant used since 1.20.2 where the root tag carries no name.
//
// All numeric payloads are big-endian. Strings use Java's "modified UTF-8":
// an unsigned-short byte length followed by bytes where NUL and supplementary
// code points are escaped (see encode/decodeModifiedUTF8).
package nbt

// Tag type identifiers as defined by the NBT specification.
const (
	TagEnd       = 0x00
	TagByte      = 0x01
	TagShort     = 0x02
	TagInt       = 0x03
	TagLong      = 0x04
	TagFloat     = 0x05
	TagDouble    = 0x06
	TagByteArray = 0x07
	TagString    = 0x08
	TagList      = 0x09
	TagCompound  = 0x0A
	TagIntArray  = 0x0B
	TagLongArray = 0x0C
)

// Tag is any NBT value. ID returns its tag type identifier.
type Tag interface{ ID() byte }

// Primitive and array tag types map directly onto Go types.
type (
	Byte      int8
	Short     int16
	Int       int32
	Long      int64
	Float     float32
	Double    float64
	ByteArray []byte
	String    string
	IntArray  []int32
	LongArray []int64
)

func (Byte) ID() byte      { return TagByte }
func (Short) ID() byte     { return TagShort }
func (Int) ID() byte       { return TagInt }
func (Long) ID() byte      { return TagLong }
func (Float) ID() byte     { return TagFloat }
func (Double) ID() byte    { return TagDouble }
func (ByteArray) ID() byte { return TagByteArray }
func (String) ID() byte    { return TagString }
func (IntArray) ID() byte  { return TagIntArray }
func (LongArray) ID() byte { return TagLongArray }

// List is a homogeneous sequence of unnamed tags. ElemID must match the type
// of every element; for an empty list it is written as TagEnd, per Java.
type List struct {
	ElemID byte
	Elems  []Tag
}

func (List) ID() byte { return TagList }

// Compound is an ordered set of named tags. Insertion order is preserved so
// that encoding is deterministic (the protocol does not require any order).
type Compound struct {
	keys []string
	m    map[string]Tag
}

func (*Compound) ID() byte { return TagCompound }

// NewCompound returns an empty compound ready for Set.
func NewCompound() *Compound {
	return &Compound{m: make(map[string]Tag)}
}

// Set inserts or replaces the tag for name, preserving first-insertion order,
// and returns the compound for chaining.
func (c *Compound) Set(name string, t Tag) *Compound {
	if _, ok := c.m[name]; !ok {
		c.keys = append(c.keys, name)
	}
	c.m[name] = t
	return c
}

// Get returns the tag for name and whether it is present.
func (c *Compound) Get(name string) (Tag, bool) {
	t, ok := c.m[name]
	return t, ok
}

// Len returns the number of entries.
func (c *Compound) Len() int { return len(c.keys) }

// Keys returns the entry names in insertion order. The slice must not be
// mutated by callers.
func (c *Compound) Keys() []string { return c.keys }
