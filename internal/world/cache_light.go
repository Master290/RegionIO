package world

import (
	"errors"
	"fmt"
	"sort"
)

// ensureLight calculates an exact center-chunk solution from a 3x3 block
// neighborhood. Light attenuates to zero within 15 blocks, so chunks outside
// that neighborhood cannot affect the center chunk.
func (c *Cache) ensureLight(chunk *Chunk) error {
	c.lightMu.RLock()
	defer c.lightMu.RUnlock()
	return c.ensureLightLocked(chunk)
}

func (c *Cache) ensureLightLocked(chunk *Chunk) error {
	for {
		center, revision := chunk.snapshot()
		if center.lightReady && center.lightValidated {
			return nil
		}

		chunks := make(map[[2]int32]*Chunk, 9)
		for dz := int32(-1); dz <= 1; dz++ {
			for dx := int32(-1); dx <= 1; dx++ {
				key := [2]int32{chunk.X + dx, chunk.Z + dz}
				if dx == 0 && dz == 0 {
					chunks[key] = center
					continue
				}
				snapshot, err := c.lightInputSnapshot(key[0], key[1])
				if err != nil {
					return err
				}
				chunks[key] = snapshot
			}
		}

		volume := newLightVolume(int(chunk.X-1), int(chunk.Z-1), 3, 3, chunks)
		clear(volume.sky)
		clear(volume.block)
		volume.calculate()

		chunk.mu.Lock()
		if chunk.revision.Load() != revision {
			chunk.mu.Unlock()
			continue
		}
		changed := chunk.installLight(volume)
		if center.lightReady && changed {
			revision = chunk.revision.Add(1)
		}
		chunk.mu.Unlock()

		c.mu.Lock()
		key := [2]int32{chunk.X, chunk.Z}
		delete(c.frames, key)
		if c.store != nil && center.lightReady && changed {
			c.dirty[key] = revision
		}
		c.mu.Unlock()
		return nil
	}
}

// lightInputSnapshot returns a stable neighbor snapshot. Cache misses are kept
// in the LRU so adjacent frames reuse the same expensive terrain instead of
// regenerating up to eight neighbors for every lighting calculation.
func (c *Cache) lightInputSnapshot(cx, cz int32) (*Chunk, error) {
	key := [2]int32{cx, cz}
	// A cache smaller than the required 3x3 neighborhood cannot retain these
	// inputs usefully. Keep misses detached to avoid evicting the requested
	// center chunk and churning the LRU on every frame.
	if c.maxChunks > 0 && c.maxChunks < 9 {
		c.mu.Lock()
		if chunk := c.chunks[key]; chunk != nil {
			c.touch(key)
			c.mu.Unlock()
			snapshot, _ := chunk.snapshot()
			return snapshot, nil
		}
		if pending := c.loads[key]; pending != nil {
			c.mu.Unlock()
			<-pending.done
			if pending.err != nil {
				return nil, pending.err
			}
			snapshot, _ := pending.ch.snapshot()
			return snapshot, nil
		}
		c.mu.Unlock()
		if c.store != nil {
			loaded, err := c.store.LoadChunk(cx, cz)
			if err == nil {
				snapshot, _ := loaded.snapshot()
				return snapshot, nil
			}
			if !errors.Is(err, ErrChunkNotFound) {
				return nil, fmt.Errorf("world: load light neighbor (%d,%d): %w", cx, cz, err)
			}
		}
		generated := c.gen(cx, cz)
		if generated == nil {
			return nil, fmt.Errorf("world: generator returned nil light neighbor (%d,%d)", cx, cz)
		}
		snapshot, _ := generated.snapshot()
		return snapshot, nil
	}

	release := c.beginUse(key)
	defer release()
	chunk, err := c.chunkAtErr(cx, cz)
	if err != nil {
		return nil, fmt.Errorf("world: load light neighbor (%d,%d): %w", cx, cz, err)
	}
	snapshot, _ := chunk.snapshot()
	return snapshot, nil
}

func (c *Cache) cachedLightNeighborhood(cx, cz int32) map[[2]int32]*Chunk {
	c.mu.Lock()
	defer c.mu.Unlock()
	chunks := make(map[[2]int32]*Chunk, 9)
	for dz := int32(-1); dz <= 1; dz++ {
		for dx := int32(-1); dx <= 1; dx++ {
			key := [2]int32{cx + dx, cz + dz}
			if chunk := c.chunks[key]; chunk != nil {
				chunks[key] = chunk
				c.touch(key)
			}
		}
	}
	return chunks
}

func (c *Cache) updateLightAfterBlockLocked(x, y, z int, live map[[2]int32]*Chunk) ([]ChunkPos, error) {
	cx, cz := int32(x>>4), int32(z>>4)
	inputs := make(map[[2]int32]*Chunk, 9)
	for dz := int32(-1); dz <= 1; dz++ {
		for dx := int32(-1); dx <= 1; dx++ {
			key := [2]int32{cx + dx, cz + dz}
			if chunk := live[key]; chunk != nil {
				snapshot, _ := chunk.snapshot()
				inputs[key] = snapshot
				continue
			}
			snapshot, err := c.lightInputSnapshot(key[0], key[1])
			if err != nil {
				return nil, err
			}
			inputs[key] = snapshot
		}
	}

	volume := newLightVolume(int(cx-1), int(cz-1), 3, 3, inputs)
	volume.relaxBlockChange(x-volume.minX, y, z-volume.minZ)

	keys := make([][2]int32, 0, len(live))
	for key := range live {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})

	changed := make([]ChunkPos, 0, len(keys))
	for _, key := range keys {
		chunk := live[key]
		chunk.mu.Lock()
		didChange := chunk.installLight(volume)
		if didChange {
			chunk.revision.Add(1)
			changed = append(changed, ChunkPos{X: key[0], Z: key[1]})
		}
		revision := chunk.revision.Load()
		chunk.mu.Unlock()

		if didChange {
			c.mu.Lock()
			delete(c.frames, key)
			if c.store != nil {
				c.dirty[key] = revision
			}
			c.touch(key)
			c.mu.Unlock()
		}
	}
	return changed, nil
}
