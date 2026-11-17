package world

// SetBlocksBatch applies a batch of structural block edits (such as EndPodium,
// EndSpike top rebuilds, or EndGateway placement) under a single lightMu lock,
// skipping unchanged blocks and invalidating cached chunk light once for the
// affected 3x3 chunk neighborhoods instead of running a separate 3x3
// newLightVolume solve per block.
func (c *Cache) SetBlocksBatch(edits []BlockChange) []BlockChange {
	if c == nil || len(edits) == 0 {
		return nil
	}
	minY, maxY := c.minY(), c.maxY()

	type chunkBatch struct {
		key   [2]int32
		ch    *Chunk
		edits []BlockChange
	}
	var order [][2]int32
	byChunk := make(map[[2]int32]*chunkBatch)
	for _, e := range edits {
		if e.Pos.Y < minY || e.Pos.Y >= maxY {
			continue
		}
		key := [2]int32{int32(e.Pos.X >> 4), int32(e.Pos.Z >> 4)}
		b := byChunk[key]
		if b == nil {
			b = &chunkBatch{key: key}
			byChunk[key] = b
			order = append(order, key)
		}
		b.edits = append(b.edits, e)
	}
	if len(order) == 0 {
		return nil
	}

	for _, key := range order {
		c.startUse(key)
		ch, err := c.chunkAtErr(key[0], key[1])
		if err != nil || ch == nil {
			c.endUse(key)
			continue
		}
		byChunk[key].ch = ch
	}
	defer func() {
		for _, key := range order {
			if byChunk[key].ch != nil {
				c.endUse(key)
			}
		}
	}()

	c.lightMu.Lock()
	defer c.lightMu.Unlock()

	var applied []BlockChange
	dirtyChunks := make(map[[2]int32]*Chunk)
	invalidateLight := make(map[[2]int32]*Chunk)

	for _, key := range order {
		b := byChunk[key]
		if b.ch == nil {
			continue
		}
		chunkChanged := false
		for _, e := range b.edits {
			prevState := b.ch.GetBlock(e.Pos.X, e.Pos.Y, e.Pos.Z)
			_, changed := b.ch.setBlock(e.Pos.X, e.Pos.Y, e.Pos.Z, e.NewState)
			if !changed {
				continue
			}
			if IsBellBlock(e.NewState) && !IsBellBlock(prevState) {
				b.ch.AddStructureBlockEntity(StructureBlockEntity{ID: "minecraft:bell", X: e.Pos.X, Y: e.Pos.Y, Z: e.Pos.Z})
			} else if !IsBellBlock(e.NewState) && IsBellBlock(prevState) {
				b.ch.RemoveStructureBlockEntity(e.Pos.X, e.Pos.Y, e.Pos.Z)
			}
			applied = append(applied, BlockChange{
				Pos:      e.Pos,
				OldState: prevState,
				NewState: e.NewState,
			})
			chunkChanged = true
		}
		if chunkChanged {
			dirtyChunks[key] = b.ch
			invalidateLight[key] = b.ch
			for nKey, nCh := range c.cachedLightNeighborhood(key[0], key[1]) {
				invalidateLight[nKey] = nCh
			}
		}
	}

	if len(applied) == 0 {
		return nil
	}

	for _, ch := range invalidateLight {
		ch.mu.Lock()
		ch.lightReady = false
		ch.lightValidated = false
		ch.mu.Unlock()
	}

	c.mu.Lock()
	for nKey := range invalidateLight {
		delete(c.frames, nKey)
	}
	for key, ch := range dirtyChunks {
		if c.store != nil {
			c.dirty[key] = ch.currentRevision()
		}
		c.touch(key)
	}
	c.mu.Unlock()

	return applied
}
