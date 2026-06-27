package world

import "regionio/internal/protocol"

// lightSections is the number of light subchunks: one below the world and one
// above, plus one per block section.
const lightSections = SectionCount + 2

// writeLight computes and emits simple lighting data. It does a vertical pass
// for sky light (sunlight propagating downward) and a single-block pass for
// block light (emissive blocks), without horizontal flood-fill.
func (c *Chunk) writeLight(w *protocol.Writer) {
	skyLight := make([]*[2048]byte, lightSections)
	blockLight := make([]*[2048]byte, lightSections)

	// Section lightSections-1 is above the world, fully lit by the sky.
	skyLight[lightSections-1] = new([2048]byte)
	for i := range skyLight[lightSections-1] {
		skyLight[lightSections-1][i] = 0xFF
	}

	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			// Sky light pass
			currentSky := byte(15)
			for y := MinY + WorldHeight - 1; y >= MinY; y-- {
				block := c.GetBlock(lx, y, lz)
				op := blockOpacity[block]
				if op >= currentSky {
					currentSky = 0
				} else {
					currentSky -= op
				}

				if currentSky > 0 {
					si := (y - MinY) >> 4
					lsi := si + 1
					if skyLight[lsi] == nil {
						skyLight[lsi] = new([2048]byte)
					}
					idx := blockIndex(lx, y, lz)
					if idx%2 == 0 {
						skyLight[lsi][idx/2] |= currentSky
					} else {
						skyLight[lsi][idx/2] |= currentSky << 4
					}
				}

				// Block light pass
				em := blockEmission[block]
				if em > 0 {
					si := (y - MinY) >> 4
					lsi := si + 1
					if blockLight[lsi] == nil {
						blockLight[lsi] = new([2048]byte)
					}
					idx := blockIndex(lx, y, lz)
					if idx%2 == 0 {
						blockLight[lsi][idx/2] |= em
					} else {
						blockLight[lsi][idx/2] |= em << 4
					}
				}
			}
		}
	}

	var skyMask, blockMask, emptySkyMask, emptyBlockMask uint64
	var skyCount, blockCount int

	for i := 0; i < lightSections; i++ {
		if skyLight[i] != nil {
			skyMask |= 1 << i
			skyCount++
		} else {
			emptySkyMask |= 1 << i
		}

		if blockLight[i] != nil {
			blockMask |= 1 << i
			blockCount++
		} else {
			emptyBlockMask |= 1 << i
		}
	}

	writeBitSet(w, []uint64{skyMask})
	writeBitSet(w, []uint64{blockMask})
	writeBitSet(w, []uint64{emptySkyMask})
	writeBitSet(w, []uint64{emptyBlockMask})

	w.VarInt(int32(skyCount))
	for i := 0; i < lightSections; i++ {
		if skyLight[i] != nil {
			w.VarInt(2048)
			w.Raw(skyLight[i][:])
		}
	}

	w.VarInt(int32(blockCount))
	for i := 0; i < lightSections; i++ {
		if blockLight[i] != nil {
			w.VarInt(2048)
			w.Raw(blockLight[i][:])
		}
	}
}

// allSectionsMask returns a bitset (as longs) with the low lightSections bits set.
func allSectionsMask() []uint64 {
	return []uint64{(uint64(1) << lightSections) - 1}
}

// writeBitSet emits a length-prefixed array of longs.
func writeBitSet(w *protocol.Writer, longs []uint64) {
	w.VarInt(int32(len(longs)))
	for _, v := range longs {
		w.Int64(int64(v))
	}
}
