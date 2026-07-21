package world

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// regionfile.go implements the Anvil .mca (RegionFile) container format: an
// 8192-byte header (1024-entry offset table + 1024-entry timestamp table)
// followed by 4096-byte-aligned chunk data records. Each record is a 4-byte
// big-endian length, a 1-byte compression type (2 = zlib), and the compressed
// NBT payload. This is the on-disk transport only; chunk NBT encoding lives in
// store.go.
//
// A region covers 32×32 chunks. The offset table index for a chunk is
// (localZ<<5)|localX, with localX/localZ in 0..31. Offset value 0 means the
// chunk is absent.

const (
	sectorSize      = 4096
	headerSectors   = 2 // offset table + timestamp table = 8192 bytes
	chunksPerRegion = 32 * 32
	compressionZlib = 2
)

// ErrChunkNotFound is returned when a chunk's offset is 0 (not stored) or the
// region file does not exist.
var ErrChunkNotFound = errors.New("world: chunk not found in region")

// RegionFile is an open Anvil .mca file. It is safe for concurrent use: the
// mutex serializes Read/Write since both move the file offset.
type RegionFile struct {
	path    string
	f       *os.File
	mu      sync.Mutex
	offsets [chunksPerRegion]uint32 // packed: bits 0-7 sector count, 8-31 sector offset
}

// OpenRegion opens (creating if needed) r.<regionX>.<regionZ>.mca under dir.
// The 8192-byte header is read; a new file is initialized with a zeroed header.
func OpenRegion(dir string, regionX, regionZ int) (*RegionFile, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fmt.Sprintf("r.%d.%d.mca", regionX, regionZ))
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	rf := &RegionFile{path: path, f: f}

	// Initialize or load the header.
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if info.Size() < headerSectors*sectorSize {
		// New/short file: write a zeroed header (two sectors).
		if _, err := f.WriteAt(make([]byte, headerSectors*sectorSize), 0); err != nil {
			f.Close()
			return nil, err
		}
	} else {
		hdr := make([]byte, headerSectors*sectorSize)
		if _, err := f.ReadAt(hdr, 0); err != nil {
			f.Close()
			return nil, err
		}
		for i := 0; i < chunksPerRegion; i++ {
			rf.offsets[i] = binary.BigEndian.Uint32(hdr[i*4:])
		}
	}
	return rf, nil
}

// locationIndex maps a chunk's in-region coords to its offset-table slot.
func locationIndex(localX, localZ int) int { return (localZ << 5) | localX }

// ReadChunk returns the decompressed NBT payload for the chunk, or
// ErrChunkNotFound when the chunk is absent.
func (r *RegionFile) ReadChunk(localX, localZ int) ([]byte, error) {
	if localX < 0 || localX > 31 || localZ < 0 || localZ > 31 {
		return nil, fmt.Errorf("world: local coordinates out of bounds")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	loc := r.offsets[locationIndex(localX, localZ)]
	if loc == 0 {
		return nil, ErrChunkNotFound
	}
	sectorOffset := int(loc >> 8)
	sectorCount := int(loc & 0xFF)
	if sectorOffset < headerSectors {
		return nil, fmt.Errorf("world: invalid sector offset %d", sectorOffset)
	}

	// 4-byte length then payload (compression byte + compressed data).
	var lenBuf [4]byte
	if _, err := r.f.ReadAt(lenBuf[:], int64(sectorOffset)*sectorSize); err != nil {
		return nil, err
	}
	length := int(binary.BigEndian.Uint32(lenBuf[:]))
	// length covers compression-type byte + compressed payload, and must fit
	// within the allocated sectors (minus the 4 length bytes).
	maxLen := sectorCount*sectorSize - 4
	if length <= 0 || length > maxLen {
		return nil, fmt.Errorf("world: chunk length %d out of range (max %d)", length, maxLen)
	}
	raw := make([]byte, length)
	if _, err := r.f.ReadAt(raw, int64(sectorOffset)*sectorSize+4); err != nil {
		return nil, err
	}
	if raw[0] != compressionZlib {
		return nil, fmt.Errorf("world: unsupported compression type %d", raw[0])
	}
	return zlibInflate(raw[1:])
}

// WriteChunk stores the NBT payload for the chunk, allocating (or reusing)
// sectors and updating the offset + timestamp tables.
func (r *RegionFile) WriteChunk(localX, localZ int, nbt []byte) error {
	if localX < 0 || localX > 31 || localZ < 0 || localZ > 31 {
		return fmt.Errorf("world: local coordinates out of bounds")
	}
	deflated, err := zlibDeflate(nbt)
	if err != nil {
		return err
	}
	compressed := append([]byte{compressionZlib}, deflated...)
	// +4 for the length prefix; sectors needed to hold everything.
	totalLen := 4 + len(compressed)
	sectorsNeeded := (totalLen + sectorSize - 1) / sectorSize
	if sectorsNeeded > 255 {
		return fmt.Errorf("world: chunk too large: %d bytes (%d sectors)", totalLen, sectorsNeeded)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	idx := locationIndex(localX, localZ)
	old := r.offsets[idx]
	oldSectors := 0
	if old != 0 {
		oldSectors = int(old & 0xFF)
	}

	// Decide where to write. Reuse the existing allocation if it still fits;
	// otherwise append at end-of-file.
	var offset int
	switch {
	case old != 0 && oldSectors == sectorsNeeded:
		offset = int(old >> 8)
	case old != 0 && oldSectors >= sectorsNeeded:
		// Keep the old offset but record the smaller count (the tail of the old
		// allocation becomes unreferenced dead space; acceptable for now).
		offset = int(old >> 8)
	default:
		// Append after the last used sector.
		offset = r.endSectorLocked()
	}

	// Build the on-disk record: length + compression byte + compressed data,
	// zero-padded to a sector boundary.
	rec := make([]byte, sectorsNeeded*sectorSize)
	binary.BigEndian.PutUint32(rec, uint32(len(compressed)))
	copy(rec[4:], compressed)
	off := int64(offset) * sectorSize
	if _, err := r.f.WriteAt(rec, off); err != nil {
		return err
	}

	// Update the offset table and timestamp, then persist both tables.
	r.offsets[idx] = uint32(offset<<8) | uint32(sectorsNeeded)
	if err := r.writeTablesLocked(); err != nil {
		return err
	}
	return r.f.Sync()
}

// writeTablesLocked writes the offset + timestamp tables back to the header.
// Caller holds r.mu.
func (r *RegionFile) writeTablesLocked() error {
	hdr := make([]byte, headerSectors*sectorSize)
	for i, loc := range r.offsets {
		binary.BigEndian.PutUint32(hdr[i*4:], loc)
	}
	// Timestamps: leave as zero (we don't track per-chunk save time precisely;
	// the field is informational and vanilla tolerates 0).
	if _, err := r.f.WriteAt(hdr, 0); err != nil {
		return err
	}
	return nil
}

// endSectorLocked returns the sector index just past the highest used sector,
// i.e. where new chunk data can be appended. Caller holds r.mu.
func (r *RegionFile) endSectorLocked() int {
	maxUsed := headerSectors
	for _, loc := range r.offsets {
		if loc == 0 {
			continue
		}
		end := int(loc>>8) + int(loc&0xFF)
		if end > maxUsed {
			maxUsed = end
		}
	}
	return maxUsed
}

// Close releases the underlying file handle.
func (r *RegionFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Close()
}

// regionIndex computes world-chunk → (region, local) coordinates with proper
// floor division for negative chunk coordinates.
func regionIndex(cx, cz int32) (regionX, regionZ, localX, localZ int) {
	// Arithmetic shift floors toward negative infinity for negative values.
	regionX = int(cx >> 5)
	regionZ = int(cz >> 5)
	localX = int(cx) - regionX*32
	localZ = int(cz) - regionZ*32
	return
}

// ensure io.EOF is referenced to keep the import honest when ReadAt paths vary.
var _ = io.EOF

// (zlib helpers live in compress.go to keep this file format-focused; the
// references below are satisfied there.)
var _ = bytes.Equal
