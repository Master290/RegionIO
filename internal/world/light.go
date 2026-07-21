package world

import "regionio/internal/protocol"

// Light is stored as vanilla nibble arrays: one 2048-byte array per 16^3
// section. The two protocol-only sections below and above the world are added
// while encoding.
const lightSections = SectionCount + 2

type lightVolume struct {
	minX, minZ int
	width      int
	depth      int
	blocks     []uint16
	sky        []byte
	block      []byte
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
		minX:  minCX * 16,
		minZ:  minCZ * 16,
		width: chunksWide * 16,
		depth: chunksDeep * 16,
	}
	count := v.width * WorldHeight * v.depth
	v.blocks = make([]uint16, count)
	v.sky = make([]byte, count)
	v.block = make([]byte, count)
	for key, chunk := range chunks {
		baseX := int(key[0])*16 - v.minX
		baseZ := int(key[1])*16 - v.minZ
		for y := MinY; y < MinY+WorldHeight; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					idx := v.indexLocal(baseX+x, y, baseZ+z)
					v.blocks[idx] = chunk.getBlock(x, y, z)
					if chunk.lightReady {
						v.sky[idx] = chunk.getLight(false, x, y, z)
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
	v.calculateSky()
	v.calculateBlock()
}

func (v *lightVolume) calculateSky() {
	queue := make([]int, 0, len(v.sky)/2)
	for z := 0; z < v.depth; z++ {
		for x := 0; x < v.width; x++ {
			from := StateAir
			for y := MinY + WorldHeight - 1; y >= MinY; y-- {
				idx := v.indexLocal(x, y, z)
				state := v.blocks[idx]
				if lightOpacity(state) != 0 || lightShapeOccludes(from, state, 0) {
					break
				}
				v.sky[idx] = 15
				queue = append(queue, idx)
				from = state
			}
		}
	}
	v.propagateIncreases(v.sky, queue)
}

func (v *lightVolume) calculateBlock() {
	queue := make([]int, 0, 256)
	for idx, state := range v.blocks {
		if emission := lightEmission(state); emission > 0 {
			v.block[idx] = emission
			queue = append(queue, idx)
		}
	}
	v.propagateIncreases(v.block, queue)
}

func (v *lightVolume) propagateIncreases(levels []byte, queue []int) {
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
}

func (v *lightVolume) coordinates(idx int) (x, y, z int) {
	x = idx % v.width
	row := idx / v.width
	z = row % v.depth
	y = row/v.depth + MinY
	return
}

func (v *lightVolume) relaxBlockChange(x, y, z int) {
	skySources := v.skySources()
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
	for y := MinY; y < MinY+WorldHeight; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				idx := v.indexLocal(baseX+x, y, baseZ+z)
				installNibble(&sky, x, y, z, v.sky[idx])
				installNibble(&block, x, y, z, v.block[idx])
			}
		}
	}
	if !lightLayersEqual(c.skyLight, sky) || !lightLayersEqual(c.blockLight, block) {
		changed = true
	}
	c.skyLight = sky
	c.blockLight = block
	c.lightReady = true
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
		standalone := &Chunk{X: c.X, Z: c.Z}
		standalone.installLight(v)
		sky, block = standalone.skyLight, standalone.blockLight
	}
	maxLightSection := highest + 2
	if maxLightSection < 0 {
		maxLightSection = 0
	}
	writeLightLayers(w, sky, block, maxLightSection)
}

func (c *Chunk) highestFilledSection() int {
	for si := SectionCount - 1; si >= 0; si-- {
		if c.sections[si] == nil {
			continue
		}
		for _, state := range c.sections[si] {
			if state != StateAir {
				return si
			}
		}
	}
	return -1
}

func writeLightLayers(w *protocol.Writer, sky, block [SectionCount]*[2048]byte, maxLightSection int) {
	if maxLightSection >= lightSections {
		maxLightSection = lightSections - 1
	}
	var skySections [lightSections]*[2048]byte
	var blockSections [lightSections]*[2048]byte
	for i := 0; i < SectionCount && i+1 <= maxLightSection; i++ {
		skySections[i+1] = sky[i]
		blockSections[i+1] = block[i]
	}
	if maxLightSection == lightSections-1 {
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
	writeLightArrays(w, skySections[:])
	writeLightArrays(w, blockSections[:])
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
		standalone := &Chunk{X: snapshot.X, Z: snapshot.Z}
		standalone.installLight(volume)
		sky, block = standalone.skyLight, standalone.blockLight
	}
	writeLightLayers(w, sky, block, lightSections-1)
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
