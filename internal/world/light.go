package world

import (
	"sync"

	"regionio/internal/protocol"
)

// Light is stored as vanilla nibble arrays: one 2048-byte array per 16^3
// section. The two protocol-only sections below and above the world are added
// while encoding.
const lightSections = SectionCount + 2

type lightVolume struct {
	minX, minZ int
	width      int
	depth      int
	noSky      bool
	maxBlockY  int
	blocks     []uint16
	sky        []byte
	block      []byte
	queue      []int
}

var lightVolume3x3Pool = sync.Pool{
	New: func() any {
		const count = 48 * WorldHeight * 48
		return &lightVolume{
			width:  48,
			depth:  48,
			blocks: make([]uint16, count),
			sky:    make([]byte, count),
			block:  make([]byte, count),
			queue:  make([]int, 0, 16384),
		}
	},
}

func acquireLightVolume3x3(minCX, minCZ int) *lightVolume {
	v := lightVolume3x3Pool.Get().(*lightVolume)
	v.minX = minCX * 16
	v.minZ = minCZ * 16
	v.width = 48
	v.depth = 48
	v.noSky = false
	v.maxBlockY = MinY - 1
	clear(v.blocks)
	clear(v.sky)
	clear(v.block)
	return v
}

func (v *lightVolume) loadChunkBlocks(chunk *Chunk) {
	if chunk.noSkylight {
		v.noSky = true
	}
	baseX := int(chunk.X)*16 - v.minX
	baseZ := int(chunk.Z)*16 - v.minZ
	strideY := v.depth * v.width
	strideZ := v.width
	for si := 0; si < SectionCount; si++ {
		sec := chunk.sections[si]
		if sec == nil {
			continue
		}
		if topY := MinY + (si << 4) + 15; topY > v.maxBlockY {
			v.maxBlockY = topY
		}
		yBase := si << 4
		for ly := 0; ly < 16; ly++ {
			yOff := (yBase + ly) * strideY
			secYOff := ly << 8
			for z := 0; z < 16; z++ {
				dstIdx := yOff + (baseZ+z)*strideZ + baseX
				srcIdx := secYOff | (z << 4)
				copy(v.blocks[dstIdx:dstIdx+16], sec[srcIdx:srcIdx+16])
			}
		}
	}
}

func releaseLightVolume3x3(v *lightVolume) {
	if v == nil || v.width != 48 || v.depth != 48 {
		return
	}
	v.queue = v.queue[:0]
	lightVolume3x3Pool.Put(v)
}

type lightNode struct {
	x, y, z int
}

var lightDirections = [...]lightNode{
	{0, -1, 0},
	{0, 1, 0},
	{0, 0, -1},
	{0, 0, 1},
	{-1, 0, 0},
	{1, 0, 0},
}

func newLightVolume(minCX, minCZ, chunksWide, chunksDeep int, chunks map[[2]int32]*Chunk) *lightVolume {
	v := &lightVolume{
		minX:      minCX * 16,
		minZ:      minCZ * 16,
		width:     chunksWide * 16,
		depth:     chunksDeep * 16,
		maxBlockY: MinY + WorldHeight - 1,
	}
	count := v.width * WorldHeight * v.depth
	v.blocks = make([]uint16, count)
	v.sky = make([]byte, count)
	v.block = make([]byte, count)
	for key, chunk := range chunks {
		if chunk.noSkylight {
			v.noSky = true
		}
		baseX := int(key[0])*16 - v.minX
		baseZ := int(key[1])*16 - v.minZ
		for y := MinY; y < MinY+WorldHeight; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					idx := v.indexLocal(baseX+x, y, baseZ+z)
					v.blocks[idx] = chunk.getBlock(x, y, z)
					if chunk.lightReady {
						if !chunk.noSkylight {
							v.sky[idx] = chunk.getLight(false, x, y, z)
						}
						v.block[idx] = chunk.getLight(true, x, y, z)
					}
				}
			}
		}
	}
	return v
}

func (v *lightVolume) indexLocal(x, y, z int) int {
	return ((y-MinY)*v.depth+z)*v.width + x
}

func (v *lightVolume) inside(x, y, z int) bool {
	return x >= 0 && x < v.width && z >= 0 && z < v.depth && y >= MinY && y < MinY+WorldHeight
}

func (v *lightVolume) calculate() {
	if !v.noSky {
		v.calculateSky()
	}
	v.calculateBlock()
}

func (v *lightVolume) calculateSky() {
	if v.noSky {
		return
	}
	topWorldY := MinY + WorldHeight - 1
	scanStartY := topWorldY
	if v.maxBlockY >= MinY-1 && v.maxBlockY+1 < scanStartY {
		scanStartY = v.maxBlockY + 1
	}
	// Above scanStartY every block in the volume is StateAir with sky light 15.
	if scanStartY < topWorldY {
		firstLayerStart := (scanStartY + 1 - MinY) * v.depth * v.width
		topSlice := v.sky[firstLayerStart:]
		for i := range topSlice {
			topSlice[i] = 15
		}
	}

	var stopYBuf [48 * 48]int
	var stopY []int
	if v.width*v.depth <= len(stopYBuf) {
		stopY = stopYBuf[:v.width*v.depth]
	} else {
		stopY = make([]int, v.width*v.depth)
	}

	strideY := v.depth * v.width
	for z := 0; z < v.depth; z++ {
		zOff := z * v.width
		for x := 0; x < v.width; x++ {
			from := StateAir
			colStop := MinY
			colOff := zOff + x
			for y := scanStartY; y >= MinY; y-- {
				idx := (y-MinY)*strideY + colOff
				state := v.blocks[idx]
				if state != StateAir && (lightOpacity(state) != 0 || lightShapeOccludes(from, state, 0)) {
					colStop = y + 1
					break
				}
				v.sky[idx] = 15
				from = state
			}
			stopY[colOff] = colStop
		}
	}

	queue := v.queue[:0]
	for z := 0; z < v.depth; z++ {
		zOff := z * v.width
		for x := 0; x < v.width; x++ {
			colOff := zOff + x
			sY := stopY[colOff]
			if sY > topWorldY {
				continue
			}
			topSeedY := sY
			if x > 0 {
				if ny := stopY[colOff-1] - 1; ny > topSeedY {
					topSeedY = ny
				}
			}
			if x+1 < v.width {
				if ny := stopY[colOff+1] - 1; ny > topSeedY {
					topSeedY = ny
				}
			}
			if z > 0 {
				if ny := stopY[colOff-v.width] - 1; ny > topSeedY {
					topSeedY = ny
				}
			}
			if z+1 < v.depth {
				if ny := stopY[colOff+v.width] - 1; ny > topSeedY {
					topSeedY = ny
				}
			}
			if topSeedY > topWorldY {
				topSeedY = topWorldY
			}
			for y := topSeedY; y >= sY; y-- {
				queue = append(queue, (y-MinY)*strideY+colOff)
			}
		}
	}
	v.queue = v.propagateIncreases(v.sky, queue)
}

func (v *lightVolume) calculateBlock() {
	queue := v.queue[:0]
	limit := len(v.blocks)
	if v.maxBlockY < MinY {
		return
	}
	if v.maxBlockY < MinY+WorldHeight-1 {
		limit = (v.maxBlockY - MinY + 1) * v.depth * v.width
	}
	for idx, state := range v.blocks[:limit] {
		if state != StateAir {
			if emission := lightEmission(state); emission > 0 {
				v.block[idx] = emission
				queue = append(queue, idx)
			}
		}
	}
	v.queue = v.propagateIncreases(v.block, queue)
}

func (v *lightVolume) propagateIncreases(levels []byte, queue []int) []int {
	for head := 0; head < len(queue); head++ {
		idx := queue[head]
		level := levels[idx]
		if level <= 1 {
			continue
		}
		x, y, z := v.coordinates(idx)
		from := v.blocks[idx]
		for direction, delta := range lightDirections {
			nx, ny, nz := x+delta.x, y+delta.y, z+delta.z
			if !v.inside(nx, ny, nz) {
				continue
			}
			nidx := v.indexLocal(nx, ny, nz)
			into := v.blocks[nidx]
			attenuation := lightOpacity(into)
			if attenuation < 1 {
				attenuation = 1
			}
			if attenuation >= level || lightShapeOccludes(from, into, direction) {
				continue
			}
			candidate := level - attenuation
			if candidate > levels[nidx] {
				levels[nidx] = candidate
				queue = append(queue, nidx)
			}
		}
	}
	return queue
}

func (v *lightVolume) coordinates(idx int) (x, y, z int) {
	x = idx % v.width
	row := idx / v.width
	z = row % v.depth
	y = row/v.depth + MinY
	return
}

func (v *lightVolume) relaxBlockChange(x, y, z int) {
	blockSeeds := make([]int, 0, 7)
	if v.inside(x, y, z) {
		idx := v.indexLocal(x, y, z)
		blockSeeds = append(blockSeeds, idx)
		for _, delta := range lightDirections {
			if v.inside(x+delta.x, y+delta.y, z+delta.z) {
				blockSeeds = append(blockSeeds, v.indexLocal(x+delta.x, y+delta.y, z+delta.z))
			}
		}
	}
	v.relax(v.block, nil, blockSeeds)
	if v.noSky {
		return
	}

	skySources := v.skySources()
	skySeeds := make([]int, 0, WorldHeight*2)
	for sy := MinY; sy < MinY+WorldHeight; sy++ {
		idx := v.indexLocal(x, sy, z)
		skySeeds = append(skySeeds, idx)
		for _, direction := range []int{4, 5, 2, 3} {
			delta := lightDirections[direction]
			if v.inside(x+delta.x, sy, z+delta.z) {
				skySeeds = append(skySeeds, v.indexLocal(x+delta.x, sy, z+delta.z))
			}
		}
	}
	v.relax(v.sky, skySources, skySeeds)
}

func (v *lightVolume) skySources() []bool {
	sources := make([]bool, len(v.sky))
	for z := 0; z < v.depth; z++ {
		for x := 0; x < v.width; x++ {
			from := StateAir
			for y := MinY + WorldHeight - 1; y >= MinY; y-- {
				idx := v.indexLocal(x, y, z)
				state := v.blocks[idx]
				if lightOpacity(state) != 0 || lightShapeOccludes(from, state, 0) {
					break
				}
				sources[idx] = true
				from = state
			}
		}
	}
	return sources
}

func (v *lightVolume) relax(levels []byte, sources []bool, seeds []int) {
	queued := make([]bool, len(levels))
	queue := make([]int, 0, len(seeds)*2)
	for _, idx := range seeds {
		if idx >= 0 && idx < len(levels) && !queued[idx] {
			queued[idx] = true
			queue = append(queue, idx)
		}
	}
	for head := 0; head < len(queue); head++ {
		idx := queue[head]
		queued[idx] = false
		desired := v.desiredLight(levels, sources, idx)
		if desired == levels[idx] {
			continue
		}
		levels[idx] = desired
		x, y, z := v.coordinates(idx)
		for _, delta := range lightDirections {
			nx, ny, nz := x+delta.x, y+delta.y, z+delta.z
			if !v.inside(nx, ny, nz) {
				continue
			}
			nidx := v.indexLocal(nx, ny, nz)
			if !queued[nidx] {
				queued[nidx] = true
				queue = append(queue, nidx)
			}
		}
	}
}

func (v *lightVolume) desiredLight(levels []byte, sources []bool, idx int) byte {
	state := v.blocks[idx]
	desired := lightEmission(state)
	if sources != nil {
		desired = 0
		if sources[idx] {
			desired = 15
		}
	}
	attenuation := lightOpacity(state)
	if attenuation < 1 {
		attenuation = 1
	}
	x, y, z := v.coordinates(idx)
	opposite := [...]int{1, 0, 3, 2, 5, 4}
	for direction, delta := range lightDirections {
		nx, ny, nz := x+delta.x, y+delta.y, z+delta.z
		if !v.inside(nx, ny, nz) {
			continue
		}
		nidx := v.indexLocal(nx, ny, nz)
		neighbor := levels[nidx]
		if neighbor <= attenuation || lightShapeOccludes(v.blocks[nidx], state, opposite[direction]) {
			continue
		}
		candidate := neighbor - attenuation
		if candidate > desired {
			desired = candidate
		}
	}
	return desired
}

func (c *Chunk) getLight(block bool, x, y, z int) byte {
	si := (y - MinY) >> 4
	if si < 0 || si >= SectionCount {
		return 0
	}
	layers := &c.skyLight
	if block {
		layers = &c.blockLight
	}
	section := layers[si]
	if section == nil {
		return 0
	}
	idx := blockIndex(x, y, z)
	b := section[idx>>1]
	if idx&1 == 0 {
		return b & 0x0f
	}
	return b >> 4
}

func (c *Chunk) setLight(block bool, x, y, z int, value byte) bool {
	si := (y - MinY) >> 4
	if si < 0 || si >= SectionCount {
		return false
	}
	layers := &c.skyLight
	if block {
		layers = &c.blockLight
	}
	section := layers[si]
	if section == nil {
		if value == 0 {
			return false
		}
		section = new([2048]byte)
		layers[si] = section
	}
	idx := blockIndex(x, y, z)
	old := section[idx>>1]
	if idx&1 == 0 {
		section[idx>>1] = old&0xf0 | value&0x0f
	} else {
		section[idx>>1] = old&0x0f | value<<4
	}
	return old != section[idx>>1]
}

// LightAt returns the stored sky and block light at a local block coordinate.
func (c *Chunk) LightAt(x, y, z int) (sky, block byte, ready bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.getLight(false, x, y, z), c.getLight(true, x, y, z), c.lightReady
}

func (c *Chunk) installLight(v *lightVolume) bool {
	changed := !c.lightReady
	var sky [SectionCount]*[2048]byte
	var block [SectionCount]*[2048]byte
	baseX := int(c.X)*16 - v.minX
	baseZ := int(c.Z)*16 - v.minZ
	strideY := v.depth * v.width
	strideZ := v.width
	for si := 0; si < SectionCount; si++ {
		var skySec *[2048]byte
		var blockSec *[2048]byte
		yBase := si << 4
		for ly := 0; ly < 16; ly++ {
			yOff := (yBase + ly) * strideY
			secYOff := ly << 7
			for z := 0; z < 16; z++ {
				rowIdx := yOff + (baseZ+z)*strideZ + baseX
				byteIdx := secYOff | (z << 3)
				if !v.noSky {
					for x := 0; x < 16; x += 2 {
						b := (v.sky[rowIdx+x] & 0x0f) | (v.sky[rowIdx+x+1] << 4)
						if b != 0 {
							if skySec == nil {
								skySec = new([2048]byte)
							}
							skySec[byteIdx+(x>>1)] = b
						}
					}
				}
				for x := 0; x < 16; x += 2 {
					b := (v.block[rowIdx+x] & 0x0f) | (v.block[rowIdx+x+1] << 4)
					if b != 0 {
						if blockSec == nil {
							blockSec = new([2048]byte)
						}
						blockSec[byteIdx+(x>>1)] = b
					}
				}
			}
		}
		sky[si] = skySec
		block[si] = blockSec
	}
	if !lightLayersEqual(c.skyLight, sky) || !lightLayersEqual(c.blockLight, block) {
		changed = true
	}
	c.skyLight = sky
	c.blockLight = block
	c.lightReady = true
	c.lightValidated = true
	return changed
}

func installNibble(layers *[SectionCount]*[2048]byte, x, y, z int, value byte) {
	if value == 0 {
		return
	}
	si := (y - MinY) >> 4
	if layers[si] == nil {
		layers[si] = new([2048]byte)
	}
	idx := blockIndex(x, y, z)
	if idx&1 == 0 {
		layers[si][idx>>1] |= value
	} else {
		layers[si][idx>>1] |= value << 4
	}
}

func lightLayersEqual(a, b [SectionCount]*[2048]byte) bool {
	for i := 0; i < SectionCount; i++ {
		if a[i] == nil || b[i] == nil {
			if a[i] != nil || b[i] != nil {
				return false
			}
			continue
		}
		if *a[i] != *b[i] {
			return false
		}
	}
	return true
}

func (c *Chunk) writeLight(w *protocol.Writer) {
	sky, block, highest := c.skyLight, c.blockLight, c.highestFilledSection()
	if !c.lightReady {
		chunks := map[[2]int32]*Chunk{{c.X, c.Z}: c}
		v := newLightVolume(int(c.X), int(c.Z), 1, 1, chunks)
		v.calculate()
		standalone := &Chunk{
			X:            c.X,
			Z:            c.Z,
			customBounds: c.customBounds,
			dimMinY:      c.dimMinY,
			dimHeight:    c.dimHeight,
			noSkylight:   c.noSkylight,
		}
		standalone.installLight(v)
		sky, block = standalone.skyLight, standalone.blockLight
	}
	maxLightSection := highest + 2
	if maxLightSection < 0 {
		maxLightSection = 0
	}
	startSec, endSec := c.sectionBounds()
	writeLightLayersForBounds(w, sky, block, startSec, endSec, maxLightSection, c.noSkylight)
}

func (c *Chunk) highestFilledSection() int {
	startSec, endSec := c.sectionBounds()
	for si := endSec - 1; si >= startSec; si-- {
		if c.sections[si] == nil {
			continue
		}
		for _, state := range c.sections[si] {
			if state != StateAir {
				return si - startSec
			}
		}
	}
	return -1
}

func writeLightLayers(w *protocol.Writer, sky, block [SectionCount]*[2048]byte, maxLightSection int) {
	writeLightLayersForBounds(w, sky, block, 0, SectionCount, maxLightSection, false)
}

func writeLightLayersForBounds(w *protocol.Writer, sky, block [SectionCount]*[2048]byte, startSec, endSec, maxLightSection int, noSkylight bool) {
	numWorldSecs := endSec - startSec
	totalLightSecs := numWorldSecs + 2
	if totalLightSecs > lightSections {
		totalLightSecs = lightSections
	}
	if maxLightSection >= totalLightSecs {
		maxLightSection = totalLightSecs - 1
	}
	var skySections [lightSections]*[2048]byte
	var blockSections [lightSections]*[2048]byte
	for i := 0; i < numWorldSecs && i+1 <= maxLightSection; i++ {
		if !noSkylight {
			skySections[i+1] = sky[startSec+i]
		}
		blockSections[i+1] = block[startSec+i]
	}
	if !noSkylight && maxLightSection == totalLightSecs-1 {
		skySections[maxLightSection] = new([2048]byte)
		for i := range skySections[maxLightSection] {
			skySections[maxLightSection][i] = 0xff
		}
	}

	var skyMask, blockMask, emptySkyMask, emptyBlockMask uint64
	for i := 0; i <= maxLightSection; i++ {
		if skySections[i] == nil {
			emptySkyMask |= 1 << i
		} else {
			skyMask |= 1 << i
		}
		if blockSections[i] == nil {
			emptyBlockMask |= 1 << i
		} else {
			blockMask |= 1 << i
		}
	}
	writeBitSet(w, []uint64{skyMask})
	writeBitSet(w, []uint64{blockMask})
	writeBitSet(w, []uint64{emptySkyMask})
	writeBitSet(w, []uint64{emptyBlockMask})
	writeLightArrays(w, skySections[:totalLightSecs])
	writeLightArrays(w, blockSections[:totalLightSecs])
}

func writeLightArrays(w *protocol.Writer, sections []*[2048]byte) {
	count := 0
	for _, section := range sections {
		if section != nil {
			count++
		}
	}
	w.VarInt(int32(count))
	for _, section := range sections {
		if section != nil {
			w.VarInt(2048)
			w.Raw(section[:])
		}
	}
}

// EncodeLightUpdate serializes a standalone light_update packet body from a
// consistent chunk snapshot.
func (c *Chunk) EncodeLightUpdate() []byte {
	snapshot, _ := c.snapshot()
	w := protocol.NewWriter(8192)
	w.VarInt(snapshot.X)
	w.VarInt(snapshot.Z)
	sky, block := snapshot.skyLight, snapshot.blockLight
	if !snapshot.lightReady {
		chunks := map[[2]int32]*Chunk{{snapshot.X, snapshot.Z}: snapshot}
		volume := newLightVolume(int(snapshot.X), int(snapshot.Z), 1, 1, chunks)
		volume.calculate()
		standalone := &Chunk{
			X:            snapshot.X,
			Z:            snapshot.Z,
			customBounds: snapshot.customBounds,
			dimMinY:      snapshot.dimMinY,
			dimHeight:    snapshot.dimHeight,
			noSkylight:   snapshot.noSkylight,
		}
		standalone.installLight(volume)
		sky, block = standalone.skyLight, standalone.blockLight
	}
	startSec, endSec := snapshot.sectionBounds()
	writeLightLayersForBounds(w, sky, block, startSec, endSec, (endSec-startSec)+1, snapshot.noSkylight)
	return w.Bytes()
}

func allSectionsMask() []uint64 {
	return []uint64{(uint64(1) << lightSections) - 1}
}

func writeBitSet(w *protocol.Writer, longs []uint64) {
	for len(longs) > 0 && longs[len(longs)-1] == 0 {
		longs = longs[:len(longs)-1]
	}
	w.VarInt(int32(len(longs)))
	for _, value := range longs {
		w.Int64(int64(value))
	}
}
