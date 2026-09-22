# Structure port notes — ground truth from the 26.1.2 jar

Everything below was read out of `versions/26.1.2/server-26.1.2.jar` with
javap. It is the working map for porting structure piece generation; treat it
as reference, not as finished code.

## Placement grid (implemented, see worldgen/structures.go)

RandomSpreadStructurePlacement.getPotentialStructureChunk:

    regionX = floorDiv(chunkX, spacing)
    regionZ = floorDiv(chunkZ, spacing)
    r = WorldgenRandom(LegacyRandomSource(0))
    r.setLargeFeatureWithSalt(seed, regionX, regionZ, salt)
    span = spacing - separation
    offsetX = spread.evaluate(r, span)
    offsetZ = spread.evaluate(r, span)
    chunk = (regionX*spacing + offsetX, regionZ*spacing + offsetZ)

RandomSpreadType.evaluate: linear -> nextInt(span); triangular ->
(nextInt(span)+nextInt(span))/2.

setLargeFeatureWithSalt(seed, x, z, salt):
seed' = x*341873128712 + z*132897987541 + seed + salt, setSeed, zero draws.

applyAdditionalChunkRestrictions runs only when frequency < 1.0f and calls the
configured reducer shouldGenerate(seed, salt, x, z, frequency):

- default / probabilityReducer: setLargeFeatureWithSalt(seed, x, z, salt);
  nextFloat() < frequency.
- legacy_type_1 / legacyPillagerOutpostReducer: regionX=x>>4, regionZ=z>>4;
  setSeed((long)(regionX ^ (regionZ<<4)) ^ seed); nextInt() discarded once;
  return nextInt((int)(1.0f/frequency)) == 0. For 0.2f the bound computes to 4.
- legacy_type_2 / legacyArbitrarySaltProbabilityReducer:
  setLargeFeatureWithSalt(seed, x, z, **10387320**) — the set's own salt is
  ignored; nextFloat() < frequency. Buried treasure uses this with 0.01.
- legacy_type_3 / legacyProbabilityReducerWithDouble: setLargeFeatureSeed(seed,
  x, z) (the two-long XOR mix, no salt); nextDouble() < (double)frequency.
  Mineshafts use this with 0.004.

Verified against the fixture on seed 12345: ruined_portals claims exactly
chunk (1,0), where the capture carries the portal's obsidian/gold/crying
obsidian; mineshaft starts land at (-4,1), (-3,10), (4,-1), consistent with
the deep oak-plank cells crossing chunks (0,0)/(1,0).

Not wired yet: concentric_rings (strongholds only).

## Ruined portal (decoded, not yet ported)

### Seeding chain (ChunkGenerator.createStructures / lambda$createStructures$0)

Per set per chunk, in order:

1. Skip if any structure of the set already has a valid start here.
2. placement.isStructureChunk(state, x, z) must hold (the grid above).
3. Single-entry sets go straight to tryGenerateStructure with **zero draws**.
4. Multi-entry sets: `random = WorldgenRandom(Legacy(0));
   random.setLargeFeatureSeed(levelSeed, chunkX, chunkPos.z)` (the two-long
   XOR mix) drives ONLY the weighted picks; then loop:
   - pick = random.nextInt(totalWeight over remaining entries)
   - walk entries subtracting weight; first negative wins;
   - tryGenerateStructure it; on success stop, else REMOVE the entry,
     total -= its weight, and repeat **without reseeding** the pick stream.
5. tryGenerateStructure -> Structure.generate builds a GenerationContext whose
   random comes from `GenerationContext.makeRandom(seed, chunkPos)`:
   `new WorldgenRandom(new LegacyRandomSource(0))` +
   `setLargeFeatureSeed(seed, chunkX, chunkZ)`. Every attempt therefore starts
   from an identically seeded FRESH stream — attempts do NOT continue each
   other's streams.
6. Structure.findValidGenerationPoint runs findGenerationPoint first and filters
   by isValidBiome AFTERWARDS: the 3D noise biome at the stub position
   (quart-snapped coordinates) against the structure's biome set.

Port status: the whole chain above plus findSuitableY live in
world/ruined_portal.go, but nothing accepts in the ±3-chunk window around the
fixture's portal yet — every variant lands on ocean or lush-caves biomes at
its stub while vanilla accepted one here. Open leads, in order of suspicion:
(a) our 3D biome sampling off the fixture's 4x4x4 lattice may diverge from
vanilla's Climate sampler at arbitrary quart positions; (b) getBaseHeight
semantics on water columns; (c) the settle-scan corner sampling. A Java probe
dumping vanilla's own stub for seed 12345 chunk (1,0) would settle it
decisively.

### findGenerationPoint, in draw order

1. Setup selection — skipped entirely (zero draws) when setups.size() <= 1;
   otherwise sum weights, one nextFloat(), walk setups subtracting
   weight/total, take the first that leaves the accumulator negative.
2. properties.airPocket = sample(nextFloat vs setup.airPocketProbability),
   where sample returns false at p==0, true at p==1, else nextFloat() < p.
3. Template: nextFloat() < 0.05 picks giant_portal_{1..3}, otherwise
   portal_{1..10} (both arrays exactly these names, one nextInt(len)).
4. Rotation = Util.getRandom(Rotation.values(), random) -> one nextInt(4)
   over [none, clockwise_90, clockwise_180, counterclockwise_90].
5. Mirror: nextFloat() < 0.5 -> none, else front_back.
6. pivot = (size.x/2, 0, size.z/2) (Java truncating division).
7. box = template.getBoundingBox(chunkOriginBlockPos, rotation, pivot, mirror).
8. surfaceY = getBaseHeight(box.center.x, box.center.z,
   getHeightMapType(setup.placement)) - 1, where the type is OCEAN_FLOOR_WG
   for on_ocean_floor and WORLD_SURFACE_WG otherwise.
9. y = findSuitableY(...) below.
10. Stub position = (chunkOriginX, y, chunkOriginZ). The piece consumer sets
    properties.cold = setup.canBeCold &&
    biomeAt(quart pos).coldEnoughToSnow(pos, seaLevel), then adds one
    RuinedPortalPiece(templateManager, pos, placement, properties, templateId,
    template, rotation, mirror, pivot).

findSuitableY(random, generator, placement, airPocket, surfaceY, ySpan, box,
accessor, randomState):

    minCut = accessor.minY + 15
    switch placement:
      in_nether:
        airPocket ? y = randomBetweenInclusive(32, 100)
                  : nextFloat() < 0.5 ? randomBetweenInclusive(27, 29)
                                      : randomBetweenInclusive(29, 100)
      in_mountain:   y = getRandomWithinInterval(70, surfaceY - ySpan)
      underground:   y = getRandomWithinInterval(minCut, surfaceY - ySpan)
      partly_buried: y = surfaceY + randomBetweenInclusive(2, 8)
      default:       y = surfaceY            (on_land_surface, on_ocean_floor)
    // settle: sample the four CORNER columns of the box as raw base columns
    corners = [(minX,minZ),(maxX,minZ),(minX,maxZ),(maxX,maxZ)] as NoiseColumn
    type = on_ocean_floor ? OCEAN_FLOOR_WG : WORLD_SURFACE_WG (isOpaque test)
    while y > minCut:
        opaque = 0
        for column in corners:
            if type.isOpaque(column.getBlock(y)):
                opaque++
                if opaque == 3: return y          // stops inside the scan
        y--
    return y

getRandomWithinInterval(r, a, b): a >= b ? b : randomBetweenInclusive(a, b).
randomBetweenInclusive(r, lo, hi) = lo + nextInt(hi - lo + 1).

Remaining to read before coding the piece: ~~RuinedPortalPiece.postProcess~~
(decoded below), StructureTemplate.getBoundingBox is min/max of transformed
positions, and placeInWorld writes non-air blocks first, air last.

### RuinedPortalPiece.postProcess, in order

1. box = template bounding box under the piece settings; skip when the chunk
   box does not contain box.center; encapsulate otherwise.
2. TemplateStructurePiece.postProcess = template.placeInWorld with the
   settings below (block entities become markers only).
3. spreadNetherrack(random, level).
4. addNetherrackDripColumnsBelowPortal(random, level).
5. If properties.vines || properties.overgrown: for every position of the
   bounding box (betweenClosedStream order): vines -> maybeAddVines, overgrown
   -> maybeAddLeavesAbove.

makeSettings(mirror, rotation, placement, pos, properties):

- ignore processor: airPocket ? STRUCTURE_BLOCK : STRUCTURE_AND_AIR — without
  an air pocket template AIR cells are skipped entirely.
- RuleProcessor rules, first match wins, each test draws from the block's own
  positional Legacy stream (RandomSource.create(Mth.getSeed(x,y,z)), NOT the
  shared random):
  1. gold_block -> air with probability 0.3 (RandomBlockMatchTest:
     state matches && nextFloat() < p)
  2. lava rule: on_ocean_floor -> lava->magma always; cold ->
     lava->netherrack always; otherwise lava->magma at 0.2
  3. if !cold: netherrack -> magma at 0.07
- BlockAgeProcessor(mossiness): stone_bricks/stone/chiseled_stone_bricks ->
  maybeReplaceFullStoneBlock (nextFloat >= 0.5 bail; then mossiness roll picks
  between [cracked_stone_bricks | stone_brick_stairs(random facing+half)] and
  [mossy_stone_bricks | mossy_stone_brick_stairs]); stairs tag -> bail at
  nextFloat >= 0.5 else mossy stairs or mossy slab roll; slabs/walls ->
  mossy variant when nextFloat < mossiness; obsidian -> crying_obsidian at
  0.15.
- ProtectedBlockProcessor(FEATURES_CANNOT_REPLACE): skip placement when the
  CURRENT world block is in the tag.
- LavaSubmergedBlockProcessor: placed blocks sitting in lava become magma.
- BlackstoneReplaceProcessor appended only when replaceWithBlackstone.

spreadNetherrack weights (index by manhattan distance + jitter):
[1,1,1,1,1,1,1,0.9,0.9,0.8,0.7,0.6,0.4,0.2]; jitter =
nextInt(max(1, 8 - radius/2)) with radius = (xSpan+zSpan)/2; iterate the
square around the box center +/- 14; for each cell draw
nextDouble() < weight[idx], surfaceY = getHeight(type)-1 at that column, y2 =
surfaceY when on_land_surface/on_ocean_floor else min(box.minY, surfaceY),
require |y2 - box.minY| <= 3, replace only air/obsidian/not-in-tag/not-lava
(not in nether), then placeNetherrackOrMagma (cold -> netherrack always;
else nextFloat < 0.07 -> magma else netherrack); overgrown adds leaves above;
then addNetherrackDripColumn below (up to 8 steps, each continuing while
nextFloat < 0.5).

## Mineshafts (ported, replay matches vanilla piece-for-piece and cell-for-cell)

Everything below was read out of the 26.1.2 jar with javap (MineshaftStructure,
MineshaftPieces + all four piece classes, StructurePiece, StructurePiecesBuilder).
The port lives in world/mineshafts.go; verification: all five seed-12345 trees
match the saved vanilla start NBT (142/82/110/121/85 pieces), and the replay
matches the structures-only capture of the dungeon area 147/147 writes.

### Structure start (MineshaftStructure.findGenerationPoint / generatePiecesAndAdjust)

1. `random.nextDouble()` — ONE draw, discarded (parity leftover).
2. Room piece: `new MineShaftRoom(0, random, chunk.getBlockX(2), chunk.getBlockZ(2), type)`
   — note blockX(2)/blockZ(2) = min + 2, genDepth 0.
3. `builder.addPiece(room); room.addChildren(room, builder, random);`
4. Non-mesa: `deltaY = builder.moveBelowSeaLevel(seaLevel=63, minY=-64, random, 10)`:
   - `maxAllowedY = seaLevel - 10` (=53); `newMaxY = box.ySpan + minY + 1`;
   - if newMaxY < maxAllowedY: `newMaxY += random.nextInt(maxAllowedY - newMaxY)`
     (bottom becomes minY+1+r, top capped at 53);
   - `deltaY = newMaxY - box.maxY`; move ALL pieces (and entrance boxes) by deltaY.
   Piece tree generation happens BEFORE this move — tree draws don't depend on y.

### Piece tree (MineshaftPieces statics)

`generateAndAddPiece(parent, accessor, random, x, y, z, dir, depth)`:
- if depth > 8 → nil; if |x - parent.box.minX| > 80 or |z - parent.box.minZ| > 80 → nil
- piece = createRandomShaftPiece(accessor, random, x, y, z, dir, depth+1, type)
- if piece != nil: accessor.addPiece(piece); piece.addChildren(parent, accessor, random)
- createRandomShaftPiece: `n = random.nextInt(100)`;
  n >= 80 → Crossing.findCrossing (fail → nil, NO retry); 70..79 → Stairs.findStairs
  (fail → nil); else Corridor.findCorridorSize (retry loop: n-- while n > 0).

Piece order in the accessor matters for findCollisionPiece (any overlap → reject).

### Room (genDepth 0)

ctor: `box = BoundingBox(x, 50, z, x+7+nextInt(6), 54+nextInt(6), z+7+nextInt(6))`
(three nextInt(6) in maxX, maxY, maxZ order). No orientation (absolute coords).

addChildren (four sides in N, S, W, E order; ySpan1 = max(box.ySpan-4, 1)):
- NORTH: `i=0; while i < xSpan: i += nextInt(xSpan); if i+3 > xSpan break;
  p = generateAndAddPiece(this, acc, rnd, minX+i, minY+nextInt(ySpan1)+1, minZ-1, NORTH, depth);
  if p: entrances.add(BB(p.minX, p.minY, this.minZ, p.maxX, p.maxY, this.minZ+1)); i += 4`
- SOUTH: same loop; pos (minX+i, minY+nextInt(ySpan1)+1, maxZ+1); entrance z: this.maxZ-1..maxZ
- WEST: over zSpan; pos (minX-1, minY+nextInt(ySpan1)+1, minZ+i); entrance x: this.minX..minX+1
- EAST: over zSpan; pos (maxX+1, minY+nextInt(ySpan1)+1, minZ+i); entrance x: this.maxX-1..maxX
NOTE the draw order: nextInt(xSpan) for i, then nextInt(ySpan1) per generated piece.

postProcess (absolute coords, no orientation):
- if isInInvalidLocation → skip; carve interior (minX, minY+1, minZ)-(maxX, min(minY+3, maxY), maxZ) cave air
- carve each entrance top: (e.minX, e.maxY-2, e.minZ)-(e.maxX, e.maxY, e.maxZ) cave air
- generateUpperHalfSphere (minX, minY+4, minZ)-(maxX, maxY, maxZ) cave air

### Corridor

findCorridorSize(acc, rnd, x, y, z, dir): `n = nextInt(3)+2; while n > 0:`
len = n*5; boxes (before move(x,y,z)): N: (0,0,-(len-1))..(2,2,0); S: (0,0,0)..(2,2,len-1);
W: (-(len-1),0,0)..(0,2,2); E: (0,0,0)..(len-1,2,2). If no collision → return box; else n--.

ctor(depth, rnd, box, dir, type): setOrientation(dir);
hasRails = nextInt(3)==0; spider = !hasRails && nextInt(23)==0 (draw only if !hasRails);
numSections = (axis==Z ? zSpan : xSpan)/5.

addChildren: `n = nextInt(4)`; switch orientation:
- N: n<=1 → (minX, minY-1+nextInt(3), minZ-1, N); n==2 → (minX-1, minY-1+nextInt(3), minZ, W);
  else → (maxX+1, minY-1+nextInt(3), minZ, E)
- S: n<=1 → (minX, minY-1+nextInt(3), maxZ+1, S); n==2 → (minX-1, .., maxZ-3, W); else (maxX+1, .., maxZ-3, E)
- W: n<=1 → (minX-1, .., minZ, W); n==2 → (minX, .., minZ-1, N); else → (minX, .., maxZ+1, S)
- E: n<=1 → (maxX+1, .., minZ, E); n==2 → (maxX-3, .., minZ-1, N); else → (maxX-3, .., maxZ+1, S)
(all pass genDepth, not +1). Then if depth < 8:
- axis N/S: for z = minZ+3; z+3 <= maxZ; z += 5: r = nextInt(5);
  r==0 → (minX-1, minY, z, W, depth+1); r==1 → (maxX+1, minY, z, E, depth+1)
- axis W/E: for x = minX+3; x+3 <= maxX; x += 5: r = nextInt(5);
  r==0 → (x, minY, minZ-1, N, depth+1); r==1 → (x, minY, maxZ+1, S, depth+1)

postProcess (LOCAL coords mapped through orientation; see transforms below):
- i2 = numSections*5 - 1
- generateBox(0,0,0)-(2,1,i2) cave air (always)
- generateMaybeBox(chance 0.8, (0,2,0)-(2,2,i2), border=CAVE_AIR, interior=CAVE_AIR, replaceAir=false, requireInterior=false) — draw per cell, `nextFloat() <= 0.8`
- if spider: generateMaybeBox(0.6, (0,0,0)-(2,1,i2), border=COBWEB, interior=CAVE_AIR, false, true)
  — cobwebs on the box boundary (y0/y1 are both borders → effectively everywhere),
  requireInterior=true (isInterior check: below OCEAN_FLOOR_WG heightmap)
- for m in 0..numSections-1: n = 2 + m*5;
  - placeSupport(level, box, 0, 0, n, 2, 2, random) — see below (LOCAL coords)
  - maybePlaceCobWeb ×8: (0.1, 0,2,n-1) (0.1, 2,2,n-1) (0.1, 0,2,n+1) (0.1, 2,2,n+1)
    (0.05, 0,2,n-2) (0.05, 2,2,n-2) (0.05, 0,2,n+2) (0.05, 2,2,n+2)
  - if nextInt(100)==0: createChest(2, 0, n-1)   [chest minecart! see below]
  - if nextInt(100)==0: createChest(0, 0, n+1)
  - if spider && !hasPlacedSpider: o = n-1+nextInt(3);
    pos = world(1, 0, o); if chunkBox.isInside(pos) && isInterior(1,0,o):
    hasPlacedSpider = true; setBlock(pos, SPAWNER) (entity cave spider — no draw)
- floor: for x in 0..2: for z in 0..i2: setPlanksBlock(planks, x, -1, z)
- placeDoubleLowerOrUpperSupport(0, -1, 2); if numSections > 1: also (0, -1, i2-2)
- if hasRails: rail = RAIL[shape=north_south];
  for z in 0..i2: below = getBlock(1, -1, z); if !below.isAir && below.isSolidRender:
  chance = isInterior(1, 0, z) ? 0.7 : 0.9; maybeGenerateBlock(chance, 1, 0, z, rail)
  (maybeGenerateBlock: `nextFloat() < chance`, note STRICT < vs maybeBox's <=)

placeSupport(level, box, x1=0, y1=0, z=n, y2=2, x2=2, rnd) — call is always (0,0,n,2,2):
- isSupportingBox: for x in 0..2: if getBlock(x, 3, n).isAir() → return (no support)
  [bytecode literally checks y=x2+1=3; x range is p3..p7 = 0..2]
- west fence column: generateBox((0,0,n)-(0,1,n), fence[west=true], border=CAVE_AIR)
- east fence column: generateBox((2,0,n)-(2,1,n), fence[east=true], border=CAVE_AIR)
- if nextInt(4)==0: planks caps at (0,2,n) and (2,2,n)
- else: planks cap at (2,2,n) only; wall torches: maybeGenerateBlock(0.05, (1,2,n-1),
  WALL_TORCH[facing=south]) and maybeGenerateBlock(0.05, (1,2,n+1), WALL_TORCH[facing=north])

placeDoubleLowerOrUpperSupport(level, box, x, y, z):
- if getBlock(x, y, z).block == planks.block: fillPillarDownOrChainUp(wood, x, y, z)
- if getBlock(x+2, y, z).block == planks.block: fillPillarDownOrChainUp(wood, x+2, y, z)

fillPillarDownOrChainUp(state, x, y, z): pos = world(x,y,z); startY = pos.y;
down=true, up=true, i=1; while (down || up):
- if down: pos.y = startY-i; s = getBlock(pos);
  replaceable = isReplaceableByStructures(s) && s.block != LAVA
  (isReplaceableByStructures = isAir || liquid || GLOW_LICHEN || SEAGRASS || TALL_SEAGRASS)
  if !replaceable: if s.isFaceSturdy(UP): fillColumnBetween(state, startY-i+1, startY); return
  down = (i <= 20 && replaceable && pos.y > minY+1)
- if up: pos.y = startY+i; s = getBlock(pos); repl = isReplaceableByStructures(s)
  if !repl: if Block.canSupportCenter(level, pos, DOWN) && s.block not FallingBlock:
    setBlock(startY+1, fence); fillColumnBetween(IRON_CHAIN, startY+2, startY+i); return
  up = (i <= 50 && repl && pos.y < maxY)
- i++
fillColumnBetween(state, y1, y2): for y in [y1, y2): setBlock.

createChest(level, box, rnd, x, y, z, loot): — CHEST MINECART, not a chest block!
- pos = world(x,y,z); if !box.isInside || !current.isAir || below.isAir → false
- rail = RAIL[shape = nextBoolean() ? north_south : east_west] (ONE draw); placeBlock(rail)
- minecart entity IS created during worldgen (they exist in vanilla worlds) →
  nextLong() IS drawn (loot seed). Our replay: place rail, draw nextBoolean + nextLong.

maybePlaceCobWeb(chance, x, y, z): if isInterior && nextFloat() < chance &&
hasSturdyNeighbours(x,y,z, 2): placeBlock(COBWEB).
hasSturdyNeighbours: iterate Direction.values() (6, decl order DOWN,UP,NORTH,SOUTH,WEST,EAST);
count neighbours (moved once) where chunkBox.isInside && state.isFaceSturdy(dir.getOpposite());
return true as soon as count >= required.

### Crossing (no orientation — absolute coords)

findCrossing: h = nextInt(4)==0 ? 6 : 2 (ONE draw);
N: BB(-1,0,-4)-(3,h,0); S: BB(-1,0,0)-(3,h,4); W: BB(-4,0,-1)-(0,h,3); E: BB(0,0,-1)-(4,h,3);
move(x,y,z); collision → nil (no retry).
ctor: isTwoFloored = box.ySpan > 3.

addChildren (genDepth): by `direction` field (from ctor):
- N: (minX+1, minY, minZ-1, N), (minX-1, minY, minZ+1, W), (maxX+1, minY, minZ+1, E)
- S: (minX+1, minY, maxZ+1, S), (minX-1, minY, minZ+1, W), (maxX+1, minY, minZ+1, E)
- W: (minX+1, minY, minZ-1, N), (minX+1, minY, maxZ+1, S), (minX-1, minY, minZ+1, W)
- E: (minX+1, minY, minZ-1, N), (minX+1, minY, maxZ+1, S), (maxX+1, minY, minZ+1, E)
if isTwoFloored: four nextBoolean() draws; each true → extra piece at minY+3+1:
N:(minX+1, minY+4, minZ-1, N); W:(minX-1, minY+4, minZ+1, W); E:(maxX+1, minY+4, minZ+1, E);
S:(minX+1, minY+4, maxZ+1, S).

postProcess (absolute): if two-floored:
- (minX+1, minY, minZ)-(maxX-1, minY+2, maxZ) air; (minX, minY, minZ+1)-(maxX, minY+2, maxZ-1) air
- (minX+1, maxY-2, minZ)-(maxX-1, maxY, maxZ) air; (minX, maxY-2, minZ+1)-(maxX, maxY, maxZ-1) air
- (minX+1, minY+3, minZ+1)-(maxX-1, minY+3, maxZ-1) air  [upper floor slab]
else: (minX+1, minY, minZ)-(maxX-1, maxY, maxZ) air; (minX, minY, minZ+1)-(maxX, maxY, maxZ-1) air.
Then 4 pillars: placeSupportPillar at (minX+1, minY, minZ+1), (minX+1, minY, maxZ-1),
(maxX-1, minY, minZ+1), (maxX-1, minY, maxZ-1) with top maxY:
  if !getBlock(x, maxY+1, z).isAir(): generateBox((x,y,z)-(x,maxY,z), planks, CAVE_AIR)
Then floor: y = minY-1; for x minX..maxX, z minZ..maxZ: setPlanksBlock(planks, x, y, z).

### Stairs (setOrientation)

findStairs (NO draws): N: BB(0,-5,-8)-(2,2,0); S: BB(0,-5,0)-(2,2,8); W: BB(-8,-5,0)-(0,2,2);
E: BB(0,-5,0)-(8,2,2); move; collision → nil.
addChildren: N:(minX, minY, minZ-1, N); S:(minX, minY, maxZ+1, S); W:(minX-1, minY, minZ, W);
E:(maxX+1, minY, minZ, E).
postProcess (LOCAL): generateBox(0,5,0)-(2,7,1) air; generateBox(0,0,7)-(2,2,8) air;
for i in 0..4: generateBox(0, 5-i-(i<4?1:0), 2+i)-(2, 7-i, 2+i) air.

### StructurePiece infrastructure (needed by all pieces)

CRITICAL — the postProcess random and write box (from applyBiomeDecoration +
lambda$applyBiomeDecoration$3 + getWritableArea, decoded 26.1.2):

- The chunk's decoration random is `WorldgenRandom(Xoroshiro(generateUniqueSeed()))
  → setDecorationSeed(levelSeed, sectionPos.origin().x, sectionPos.origin().z)`
  — i.e. exactly what worldgen.DecorationRandom produces. It is passed RAW
  (no setFeatureSeed fork) to every `StructureStart.placeInChunk` call, and the
  SAME instance flows sequentially through ALL starts' pieces placing into that
  chunk (order = the chunk's structure-references iteration order — for the
  fixture's chunk (1,0) both the ruined portal and mineshaft pieces place, so
  the portal-vs-mineshaft order must be pinned; likely registry order, the same
  order placeScheduledStructures already assumes for portals/ruins).
- The write box is the chunk's OWN box: BoundingBox(minBlockX, minY+1, minBlockZ,
  minBlockX+15, maxY, minBlockZ+15). placeBlock/getBlock/isInterior/isInInvalidLocation
  all clip against it; random draws happen for the piece's full local box regardless
  (loops don't skip on box misses), so draw alignment is chunk-order-dependent but
  write alignment is per-cell.
- Feature steps fork with setFeatureSeed(decorationSeed, index, step) — a RESEED
  from the saved decoration seed, not a continuation — so piece draws never shift
  feature draws. But within one chunk, pieces of start B draw after pieces of
  start A consumed their draws.

Replay model implied: per target chunk, run every intersecting piece of every
referenced start in reference order, with that chunk's fresh DecorationRandom,
writing only cells inside the chunk box; region order across chunks does not
matter for writes (each cell belongs to one chunk) but reads see earlier writes
of the same chunk pass (vanilla processes pieces in list order within a start,
and starts in reference order — earlier pieces' writes are visible to later
pieces' isInInvalidLocation/isSupportingBox reads in the SAME chunk only... in
vanilla actually earlier CHUNks' writes are also visible once those chunks are
done; for the region replay, process chunk-by-chunk in a fixed order).

Orientation transform (piece-local → world; setOrientation also sets mirror/rotation
for BlockState.rotate/mirror in placeBlock — SOUTH: mirror LEFT_RIGHT; WEST: mirror
LEFT_RIGHT + rot CW90; EAST: rot CW90; NORTH: none):
- getWorldX(x, z): N/S: minX+x; W: maxX-z; E: minX+z
- getWorldZ(x, z): N: maxZ-z; S: minZ+z; W/E: minZ+x
- getWorldY(y): minY+y (orientation != null; pieces without orientation pass
  absolute coords directly)
- placeBlock(state, x, y, z, chunkBox): pos = world(x,y,z); if !chunkBox.isInside → skip;
  if !canBeReplaced → skip (corridor override: skip planks/wood/fence/iron_chain blocks);
  apply mirror+rotation to state; setBlock. (Fluid tick + postprocess marks: no block effect.)
- generateBox(x1,y1,z1,x2,y2,z2, state, border, replaceAir): loops y outer, x middle, z inner;
  if replaceAir && getBlock().isAir → skip; border cells (any coord on the box face) →
  border state; interior → state.
- generateMaybeBox(rnd, chance, box, borderState, state, replaceAir, requireInterior):
  same loop order; per cell `nextFloat() <= chance`; skip if replaceAir && air;
  skip if requireInterior && !isInterior; border → borderState; interior → state.
- isInterior(x, y, z, chunkBox): world(x, y+1, z) inside chunkBox && pos.y <
  getHeight(OCEAN_FLOOR_WG, pos.x, pos.z)  (strict <)
- isInInvalidLocation(chunkBox): clamp piece box ±1 to chunkBox; center biome in
  #mineshaft_blocking → invalid; then liquid() checks on the 4 face pairs:
  (x,z rows at y0/y1), (x,y at z0/z1), (z,y at x0/x1) — any liquid BLOCK → invalid.
- setPlanksBlock(state, x, y, z): if isInterior(x, y, z) && !current.isFaceSturdy(UP):
  setBlock(state).
- isSupportingBox(xFrom, xTo, y, z): for x in [xFrom, xTo]: getBlock(x, y+1, z).isAir() → false.

### Blocks involved

normal: planks=oak_planks, wood=oak_log (axis=y), fence=oak_fence; mesa: dark oak
variants (mineshaft_mesa structure json). Rails (shape north_south / east_west /
nextBoolean pick), cobweb, iron_chain (axis=y), wall_torch (facing), spawner,
cave_air. MESA uses the same algorithm with different planks/fence/log ids.

## Ocean ruins (ported, replay matches vanilla cell-for-cell)

The grid claims ocean_ruins at chunk (7,5) on seed 12345, and a fresh
capture with -chunks "6,4;7,5;8,6;7,4" confirms vanilla agrees: its
saved start minecraft:ocean_ruin_cold lives exactly there, with one
child piece:

    Template = underwater_ruin/brick_2
    Rot      = COUNTERCLOCKWISE_90
    TP       = (112, 50, 80)     (= chunk origin, y=50)
    BB       = [112,50,75] .. [118,56,80]
    BiomeType= COLD, IsLarge = 0, Integrity = 0.8, GD = 0, O = 2

Algorithm read off OceanRuinStructure + OceanRuinPieces bytecode:

- findGenerationPoint = onTopOfChunkCenter(context, OCEAN_FLOOR_WG,
  generatePieces) — no draws; stub sits at the chunk centre on the
  ocean floor.
- generatePieces: base pos = (minBlockX, **90**, minBlockZ);
  rotation = Rotation.getRandom(random) (one nextInt(4));
  OceanRuinPieces.addPieces(...).
- addPieces: large = nextFloat() <= largeProbability (0.3333);
  integrity = large ? 0.9 : 0.8; addPiece(...) always runs; when
  large, a second nextFloat() <= clusterProbability (0.1) triggers
  addClusterRuins (several extra small pieces around the first).
- addPiece picks the biome type cold/warm from the biome at the
  position, then the template array: brick/cracked/mossy x8 for cold,
  warm_1..8 for warm (large variants are big_*); template =
  arr[nextInt(arr.length)]. The piece carries an IntegrityProcessor
  (drops blocks at random until the integrity fraction remains) plus
  suspicious-sand archy rules for gravel/wall targets.

48 underwater_ruin templates are extracted under
internal/worldgen/data/structure_template/underwater_ruin/.

### Post-placement physics (the part that cost the most digging)

The -no-features capture of the ruin chunks differs from our placement by
exactly 16 cells: 12 bubble_column cells and 4 gravel/water swaps. Both are
block-tick physics that run when the generated chunks take their first
ticks (the kept vanilla world's saved chunk carries EMPTY block_ticks and
fluid_ticks lists — they were scheduled during worldgen and consumed at
load). `tools/VanillaRuinChunkTicksProbe.java` reads saved chunk NBT to
verify this. The mechanism, from bytecode:

- FallingBlock.updateShape calls scheduleTick on every gravity block the
  shape pass touches — and placeInWorld's tail runs
  Block.updateFromNeighbourShapes over EVERY placed cell, so each placed
  gravel/sand gets a fall tick.
- FallingBlock.isFree = air || FIRE || liquid() || canBeReplaced, where
  liquid() is a BLOCK-level property (Properties.liquid), NOT the fluid
  state: a waterlogged chest is NOT free, so gravel rests on the fixture's
  waterlogged chest at (113,51,78) — go by block name (water/lava), never
  by the fluid flag.
- The fall lands on the first non-free cell below; the vacated cell
  becomes the block's own fluid (air for gravel); water then flows back
  in, and cells with >=2 horizontal source-water neighbours settle to
  source water (the classic infinite-water rule — both fixture cells have
  it).
- LiquidBlock's shape update above magma schedules the fluid tick that
  grows a downward bubble_column (drag=true) upward through source water
  to the first non-water cell (the sea surface, y=62 at sea level 63).
  Only magma that SURVIVES the final placement grows a column: cracked_2's
  magma at (113,50,79) is overwritten by mossy_2's gravel and the capture
  shows no column there — so run the bubble pass after all pieces place.
- WorldGenRegion.updateNeighborsAt is a NO-OP default on LevelAccessor
  during worldgen; onPlace is never called by WorldGenRegion.setBlock —
  the shape pass is the only trigger chain.

world/ocean_ruin.go implements all of this
(applyOceanRuinPhysics); the env-gated TestOceanRuinCaptureParity
compares the whole ruin footprint against a -no-features capture and
reaches 0 diffs, and the committed TestOceanRuinFixture12345 pins the
chest, the landed gravel, and the solid share on the parity seed.

Seed 12345, committed fixture chunks (0,0) (1,0) (0,1) (-1,-1):

- (1,0): ruined portal — obsidian (17,13,3), crying obsidian (21,13,3),
  gold block (19,18,3), chest (17,13,2).
- (0,0)/(1,0): mineshaft planks/fences near y=-41..-42.
- (-1,-1): **ATTRIBUTED — mineshaft + monster room.** The pocket is a
  monster room at origin (-6,36,-19) (a 9x9 floor of mossy/cobble with two
  chests and a spawner whose center lies in chunk (-1,-2), so only its
  southern two rows are inside the fixture chunk). Our monster-room replay
  generates the exact right position but its pass-1 wall-opening validation
  fails (0 openings where vanilla had 1-5): the opening is cave air carved
  by a MINESHAFT corridor. A three-way capture of chunks around the room
  (featureless / -no-features / full) proves it: the opening cells are
  already cave_air in the structures-only capture and stone in the
  featureless one, and chunk (-1,-2)'s saved References name
  minecraft:mineshaft starts at chunks (4,-1) and (4,2) — mineshaft pieces
  reach 5-6 chunks from their start. Mineshafts place at the same
  underground_structures step as monster rooms but BEFORE the step's
  features (applyBiomeDecoration runs all structure pieces of a step, then
  the step's features), so porting mineshafts is a hard prerequisite for
  the dungeon — and for every other feature whose validation reads
  mineshaft-carved air.

Also from the same dig: stage-1 lakes are now replayed (world/lakes.go,
bytecode-faithful port of LakeFeature — blob ellipsoids, the validation
pass, the cave-air/fluid carve, the 50%-per-cell stone rim above the fluid
line). No lake lands in the fixture area (the only placement position in
the surrounding 5x15 source window is rejected at (-18,45,-39)), so the
measured parity did not move; the port is verified by unit test and by
draw-order against the bytecode, not yet by a capture.

Implementation findings that cost the most digging (all in the port now):

- applyBiomeDecoration RESEEDS the chunk's decoration random with
  setFeatureSeed(decorationSeed, structureIndexInStep, step) before each
  structure's pieces place - mineshaft is index 1 of the alphabetically
  ordered underground_structures registry list (buried_treasure, mineshaft,
  mineshaft_mesa, trail_ruins, trial_chambers). Without the reseed every
  roll-based placement (rails, cobwebs, the 0.8 ceiling carve) diverges.
- generateBox's FIRST state parameter places on the box BORDER, the second
  in the interior. The 1-wide fence/plank columns pass the block as the
  border state - read the parameters backwards and no support ever places.
- The saved NBT "O" field is Direction.getHorizontalIndex (south=0, west=1,
  north=2, east=3); the room stores -1 (no orientation, absolute coords).
- Biome tag flattening had dropped nested references (#is_ocean etc. got a
  double minecraft: prefix) - fixed in structures.go; four of the five
  seed-12345 mineshaft starts were being rejected at the biome filter.
- Chunk references decode with ChunkPos.pack (x in the LOW 32 bits) - chunk
  (-1,-2) references starts (4,-1) and (-4,1), and only referenced starts
  place pieces into the chunk.

## Surface decoration findings (session of the 99.447% milestone)

The residual ~2174-cell gap at 99.447%, by family: clay pools ~640 (both
directions + tuff), moss patches ~430, patch vegetation ~180 missing, cave
vines ~180 missing (separate top-level feature), netherrack spread ~225
(ruined-portal postProcess spreadNetherrack not replayed), state-level water
~70, mineshaft fence states ~36, small ore residue ~50.

Pinned semantics for finishing the patch vegetation (all verified against
bytecode, NOT yet enabled - placing moss_vegetation on the currently
mismatched moss ground cost ~100 cells net, so the moss ground divergence is
the root cause to fix first):

- distributeVegetation iterates a java.util.HashSet<BlockPos>, so the rolls
  map to positions in HASH-TABLE order, not scan order: Vec3i.hashCode is
  (y + 31*z)*31 + x in wrapping int32; HashMap spreads with h ^ (h >>> 16);
  slot = (capacity-1) & spread; capacity from 16 doubling past 0.75 load;
  within a slot, insertion order. javaHashSetOrder in vegetation_patches.go
  implements this.
- SimpleBlockFeature draws the state provider FIRST (weighted_state_provider
  = one nextInt(totalWeight) walk), THEN checks canSurvive - the draw happens
  even when the placement is rejected. No air precondition at all.
- DoublePlantBlock (tall_grass lower): requires pos.above() empty, then
  DoublePlantBlock.placeAt writes BOTH halves (lower + upper). MossyCarpet
  (pale only) takes a separate random-consuming path - irrelevant for the
  overworld ocean/lush fixture.
- Plain patch vegetation places at groundPos.relative(surface.opposite()) =
  the empty cell above the ground; the WATERLOGGED pool's vegetation places
  INTO the water cells (placeVegetation gets pos.below() so the nested
  feature lands on the water cell) and waterlogs the placed state when it
  has the property.
- The waterlogged pool's returned set (which distributeVegetation rolls
  over) is the WATER-FILLED SUBSET: the water cells were themselves added
  while iterating the ORIGINAL set in hash order, so the water set's own
  hash order governs the rolls.
- The moss patch position stream: count 125 per source chunk, each position
  consuming in_square(2) + height_range uniform(1) draws, with
  `random_offset`, `environment_scan` and `biome` drawing nothing. The
  `random_offset` claim here used to read "y(1) draw"; that was wrong, and the
  jar settles it: a bare int in that field is a `ConstantInt`, whose
  `sample(RandomSource)` is `return value` with no random access
  (`lush_caves_vegetation` and `lush_caves_clay` carry `y_spread: 1`,
  `lush_caves_ceiling_vegetation` `y_spread: -1`), so it is a fixed shift after
  the scan - up one for the floor patches and the clay, down one for the ceiling
  - and never a draw. `TestPlacementRandomOffsetConstantShiftsAndDrawsNothing`
  pins both halves. The ~300-cell moss ground divergence is somewhere in this
  chain or in the patch column scan; it survives with vegetation disabled, so it
  is upstream of the vegetation.
- `environment_scan` is faithful too, and this is now tested rather than assumed
  (`TestEnvironmentScanMatchesVanillaControlFlow`, which the disassembly of
  `EnvironmentScanPlacement.getPositions` was read against). Two parts of it are
  easy to get wrong and were checked specifically. The final target test is
  reached from *both* loop exits - running out of steps and failing the allowed
  condition - so the scan inspects `maxSteps + 1` cells and a floor exactly
  `maxSteps` below the start is still found. And a cell that is neither the
  allowed kind nor the target kind aborts the descent: for the lush-cave chain,
  whose allowed condition is the `minecraft:air` tag and whose target is `solid`,
  water stops the scan rather than being stepped over, so nothing below that
  water is ever considered. That closes the last reading of hypothesis (d) for
  this chain - neither offset nor scan can be off by a cell.

## Ruined portal netherrack spread (decoded; reseed parameters unresolved)

spreadNetherrack is fully decoded from the RuinedPortalPiece bytecode: a +-
14 square around BoundingBox.getCenter() (min + span/2 integer division,
NOT (min+max)/2), weights [1,1,1,1,1,1,1,0.9,0.9,0.8,0.7,0.6,0.4,0.2] indexed
by max(0, manhattan+jitter), jitter = nextInt(max(1, 8-radius/2)) with
radius = (xSpan+zSpan)/2; per cell nextDouble < weight BEFORE any surface
check; surfaceY = getHeight(WORLD_SURFACE_WG for everything except
on_ocean_floor)-1; y = surfaceY for on_land_surface/on_ocean_floor else
min(box.minY, surfaceY); |y-box.minY| <= 3; canBlockBeReplaced requires a
SOLID replaceable cell (NOT air, obsidian, #features_cannot_replace, or lava
outside the nether - so the spread replaces ground and water columns, it
never floats in air); then placeNetherrackOrMagma (nextFloat<0.07 magma when
!cold), optional jungle-leaves topper, and a drip column below (up to 8
steps, each nextFloat<0.5 then a placement draw, all from the SHARED random -
the old positional-stream drip columns were wrong).

WHAT IS NOT RESOLVED: the reseed parameters for the shared random. An
exhaustive (step 0..10 x index 0..20) scan against the fixture's chunk (1,0)
netherrack field left ~155 mismatched cells at best (best: step=2 idx=11,
or step=4 idx=1/8 with jitter=4, which the vanilla field's max manhattan
radius of 9 independently requires). The vanilla field has 65 cells on y=12
(spread) decaying geometrically down to y=3 (drip), so the SHAPE matches the
decoded algorithm; only the draw stream does not. Next step: a Java probe
running the real RuinedPortalPiece.postProcess against the captured pre-
carve state, dumping every random draw in order.

Also fixed en route: boundingBoxOf ignored the stub's Y (minY came out 0
instead of 12), which had moved every drip column 12 blocks too deep; the
portal pieces now place per-chunk like mineshafts (each chunk copy reseeds
its own decoration random and clips writes to its own 16x16 box); and the
structure pass order now follows vanilla's step order (mineshafts' step-3
pieces before the step-4 surface structures, ocean ruins before ruined
portals within step 4 by registry order).

## Residual gap census at 99.625% (seed 12345 fixture)

Total residual: 1475 cells. Per chunk: (-1,-1)=688, (0,0)=451, (1,0)=243,
(0,1)=93. Bands: deep(<0)=999, mid(0..43)=463, near-surface=13, surface=0.

Top missing (vanilla-only): deepslate 311, air 207, water 153, moss 143,
granite 74, tuff 72, short_grass 52, clay 49, cave_vines_plant 47.
Top extra (ours-only): clay 268, air 189, deepslate 175, moss 173,
netherrack 128 (portal spread overshoot into chunks vanilla leaves clean -
the spread write-clip may span more region chunks than vanilla references),
water 97, cave_vines_plant 80.

3D cluster analysis of chunk (-1,-1) (6-neighbor connected components):
- cluster 0 (228 cells): a SINGLE COLUMN x=6, z=4..10, y=-20..2 - the
  cave-vine columns plus patch vegetation on top. The vine lengths differ
  (extra cave_vines_plant 38 vs missing 41) - i.e. individual vines are one
  to two cells longer or shorter, which points at the BlockColumnFeature
  height sampling (weighted_list: nextInt(totalWeight) entry roll then the
  inner uniform sample) or at the allowed-placement truncation walk reading
  slightly different cells.
- cluster 1 (48): clay pool at y=-7..-1 fully misplaced (ours writes clay
  where vanilla keeps deepslate) - one waterlogged pool position diverges.
- cluster 2 (45): moss patch at y=-42..-39 shifted - same shape as cluster
  0 upstream: the moss patch position stream diverges by one placement.
- cluster 3 (31): granite->andesite - an ore blob boundary (count-based ore
  positions shift by the blob edge).
- clusters 4/5/7: vine columns plus moss again (~60 cells total).

Leading hypothesis at the time (now superseded, kept for the reasoning trail):
the placement position streams for stage-9 features interleave with the FEATURE
placement draws, so `glow_lichen` consuming a different number of draws could
shift `lush_caves_clay`. That cannot happen here: `placeScheduledVegetationPatches`
calls `SetFeatureSeed(decorationSeed, scheduled.Index, stage)` before every
feature and `worldgen/random.go:367` implements it as a full
`SetSeed(decorationSeed + featureIndex + 10000*stage)`, so each feature's stream
is a pure function of `(seed, index, stage)` and cross-feature draw drift is
structurally impossible. Only a wrong index, a wrong seed, or a different world
state can move a feature. `feature_schedule_golden_test.go` pins the indices,
including the fact that `lush_caves_clay` is 5th in `lush_caves` but reseeds as
29 because `FeatureSchedule`'s indices are positions in the global topological
step list over the 3x3 neighbourhood, not offsets in the decorating biome's list.

That last distinction was worth an experiment, because the two candidate readings
give different numbers and only one can be right. Vanilla decorates a chunk's
single biome and reseeds per entry of *that* list, which would put
`lush_caves_clay` at index 4; `FeatureSchedule` reports 29. Sweeping the reseed
index with everything else held fixed - same world, same placement list, same
seed, only `SetFeatureSeed`'s index varying, via
`REGIONIO_LUSH_CLAY_PROBE_INDEX` - and scoring each value on whether it reproduces
vanilla's pools for source (0,0) (well-defined, because the differential shows
chunk (0,0) with zero missing and zero extra clay cells, so vanilla's two pools are
exactly the ones we already place):

| index | result |
|---|---|
| 4 (the per-biome position) | places nothing |
| 29 (what `FeatureSchedule` reports) | **(5,-29,12) and (5,-30,13) - vanilla's pair** |
| the other 39 values | no pool, or a different one |

The full ranking, `make clay-index-sweep`, score 1 only when the pools are exactly
vanilla's pair for source (0,0):

```
index  0  0   nothing                     index 21  0   (8,-29,2) (5,-52,1)
index  1  0   nothing                     index 22  0   (6,-30,14)
index  2  0   nothing                     index 23  0   nothing
index  3  0   (14,-41,4) (6,-28,5)        index 24  0   (1,-41,12)
index  4  0   nothing                     index 25  0   (6,-52,1)
index  5  0   (5,-25,2)                   index 26  0   nothing
index  6  0   nothing                     index 27  0   (12,-42,7)
index  7  0   nothing                     index 28  0   (3,-16,7)
index  8  0   nothing                     index 29  1   (5,-29,12) (5,-30,13) <- vanilla
index  9  0   (9,-15,11)                  index 30  0   nothing
index 10  0   (7,-15,9)                   index 31  0   nothing
index 11  0   nothing                     index 32  0   nothing
index 12  0   (7,-51,1)                   index 33  0   nothing
index 13  0   (1,-47,14) (12,-41,13)      index 34  0   (5,-29,10) (3,-53,3)
            (5,-29,11)                    index 35  0   (2,-24,5)
index 14  0   (8,-51,2)                   index 36  0   nothing
index 15  0   nothing                     index 37  0   nothing
index 16  0   (3,-53,3)                   index 38  0   nothing
index 17  0   (5,-54,5)                   index 39  0   nothing
index 18  0   (13,-42,10) (0,-24,5)       index 40  0   nothing
index 19  0   nothing
index 20  0   nothing
```

Two near misses are what make the sharpness concrete rather than assumed: 13 places
`(5,-29,11)`, one block off vanilla's `(5,-29,12)`, and 34 places `(5,-29,10)`. So a
wrong index does sometimes land in the same neighbourhood, and a score counting
"near the right place" would have been ambiguous where an exact one is not.

Distribution over 0..40: 22 indices place no pool at all, 13 place one, 5 place
two, one places three. So the sweep is sharply sensitive to the index - the curve
is nowhere near flat, which is what makes a single hit mean something - and 29 is
the only value in the range that reproduces vanilla's positions, while 4 is not
merely wrong but silent. Hypothesis (a), a wrong `featureIndex`, is excluded, and
`FeatureSchedule`'s global-topological numbering is the quantity vanilla actually
uses here. Note what this does and does not establish: it settles the index for the
lush-cave fixture's biome neighbourhood, which is where all four captured chunks
live; it is not a proof that a single-biome region could not number differently.

## Superseded: the pool divergence closed, and what the residue actually is

`2c29024` (decoration source order aligned with vanilla's `rangeClosed` stream)
fixed the misplaced pool without any change to the patch or scan code, which was
itself the answer: the cause was the world state the scan walked, not the
stream. The cluster list above predates it. Re-measured against generator
version 36 the pool family is 24 vanilla-only and 9 ours-only cells out of 6,515
(`TestVegetationResidualOverlap`), block parity is 99.909%, and the largest
remaining family is the moss patch ground at 107 cells.

`TestMossPatchMismatchShape` and `TestMossPatchMismatchCorrelation`
(`REGIONIO_MOSS_PATCH_DIAGNOSTIC=1`) then measured the moss family, and the
shape table rules out the hypotheses that were still open for it. Of 1,050
vanilla `moss_block` cells we place 1,015 and agree on 979, so the origins are
right. Every one of the 107 mismatches lies within chebyshev distance 5 of a
moss cell both sides agree on, and 63 of them at distance 1: these are rim cells
of discs we also placed, not missing or displaced patches. `VegetationPatchFeature`
was re-read against the 26.1.2 bytecode to confirm the port is faithful in every
detail that could move a rim cell - `xzRadius` sampled twice at feature start,
corner columns skipped without a draw, edge columns kept when
`nextFloat() <= extraEdgeColumnChance` and skipped without a draw when the
chance is zero, the scan pair testing `BlockStateBase::isAir` then its negation
over `vertical_range` steps, the recorded ground position taken before
`placeGround` moves it, and the vegetation roll one `nextFloat` per set position.

The pool correlation needed a control and the control killed it: 55% of the
missed cells sit within 3 of a clay cell, against 58% for the cells both sides
agree on, so missed moss is not pool-related at all. Only our *extra* cells are
(83% within 3). What is left is a rim defect concentrated in the cells that the
edge-column roll and the `isEmpty`/`isFaceSturdy`/replaceable acceptance tests
decide, with the pool region contributing the cells we place and vanilla does
not. One known divergence was flagged while reading the bytecode: `placeGround` tests
the already-ground condition by BLOCK (`state.is(cur.getBlock())`) and the
replaceable test is a block-tag test, while our port compared packed state IDs and
resolved tags to each member's default state. The note that closed it - "for the
single-state blocks in `moss_replaceable` that is identical, so it does not explain
these cells, but a multi-state member (`cave_vines`, `grass_block[snowed]`) would be
wrong" - was correct about the moss rim and correct about the bug, and the bug sat
unactioned for that long. It is fixed, along with three sibling copies of the same
mistake, in "VegetationPatchFeature, now verified line for line": `cave_vines` does
have 52 states in this build - `age` takes 26 values, 0..25, and `berries`
two, which is where the "25" I kept repeating went wrong - and aged vines were
excluded, and the effect on these cells is nonetheless **zero** - which is the interesting result, not a
refutation. Read the two sections together before chasing a rim cell through
replaceability again.

Both branches of the probe that was next on the list have now run. The
`placeScheduledVegetationFeature` seam exists and the replay answers the question
it was built for: our patch, run against vanilla's column, reproduces vanilla's
rim, so the world state is the cause - see "The moss rim: what the follow-up probes
measured" and "The last 22 clay anchors are not an ordering problem either". And
the other branch was right too: `decorationSources` models only (0,0) and (1,0),
with every other source falling to an untuned default.

## The moss rim: what the follow-up probes measured, and where the first one misread

`TestMossResidualPlacementContext` (`REGIONIO_MOSS_PATCH_DIAGNOSTIC=1`) ran the
two cheap discriminations first, and both came back negative.

**The terrain the patch scanned is vanilla's terrain.** For each of the 107
mismatch cells the support cell below and above was compared on both sides, and
the pair classified rather than merely diffed: a pair where one side is air or a
plant is our own nested-vegetation cascade, and a pair involving moss is the same
defect one block away, so neither counts as terrain. **One cell of 107** has a
solid-versus-solid difference, and it is `clay` where vanilla has `deepslate` at
(-1,-10,-5) - a known pool edge. 57 cells have identical support, 49 differ only
in cover. So the scan found the same ground everywhere, and the ordering
hypothesis has no terrain left to explain the moss with.

**The draw sequence is faithful, line for line in the bytecode.** `place` samples
`xzRadius` twice (offsets 34-61); the column loop skips corners with no draw
(141-160: `onEdge`/`isCorner` booleans, `if (corner) continue` before any RNG),
rolls `nextFloat() <= extraEdgeColumnChance` for edge-only columns and skips with
no draw when the chance is zero (163-191); then samples `depth` once (348-357)
and adds the bottom block when `extraBottomBlockChance > 0 && nextFloat() < chance`
(358-386). `moss_patch`'s configured JSON ships `"depth": 1` and
`"vertical_range": 5` as **bare integers**, which `IntProvider.CODEC` resolves to
`ConstantInt` - draw-free - and `"extra_bottom_block_chance": 0.0`, so the only
per-column draw in the floor variant is the edge roll. Our port matches that
exactly, including which branches are conditional on the constant.

**A uniform source order is not vanilla's rule, and that is worth recording
because it is the obvious guess.** Applying the tuned branch (plain Z-major,
target fifth of nine) to every target instead of only (0,0) and (1,0) leaves
(0,0) and (1,0) unchanged, as it must, and moves (0,1) from 58 to 73 mismatches
and (-1,-1) from 122 to 672 - total 99.909% down to 99.765%. The special case is
therefore load-bearing for the two chunks it does not name, which means the real
rule is something other than a fixed sweep order. It is also not a
last-writer-wins effect: the number that swings is the *cascade* count, i.e.
which features saw which air when they scanned.

**The write gate is not it either, and that closes the last alternative.**
26.1.2 has no `FeatheredBlockAccess` and `Biome` has no `decorate`: decoration
runs `ChunkStatusTasks.generateFeatures` -> `WorldGenRegion` ->
`ChunkGenerator.applyBiomeDecoration` -> `PlacedFeature.placeWithBiomeCheck`, and
the feather was replaced by two radius checks on `WorldGenRegion`.
`ensureCanWrite` compares `blockToSectionCoord` of the target against the centre
with `step.blockStateWriteRadius()`, which `ChunkPyramid.FEATURES` sets to **1
chunk** and carvers/surface/noise to 0; we gate the same way
(`abs32(cx-sourceX) > 1`). Inside that chunk a feature may write the whole
16x16 column, and structures are the ones that get clipped to their own chunk by
`getWritableArea`. Reads use a different bound (the pyramid's dependency array,
nine entries), so reading is wider than writing - but the moss scan only ever
looks one block sideways, which is inside both.

### What the verdict probe then showed, overturning the section above

`TestMossResidualColumnVerdict` (`REGIONIO_MOSS_PATCH_DIAGNOSTIC=1`) asks, for
each residual cell, whether the *non-placing* side's own terrain still permits
that column - candidate air one block above (`moss_patch` is `surface: floor`),
ground face-sturdy, ground in `moss_replaceable`. On seed 12345:

| verdict | cells |
| --- | --- |
| candidate holds solid rock (21 deepslate, 6 tuff) | 27 |
| candidate holds the clay pool's own output (9 water, 6 clay) | 15 |
| candidate holds another moss disc's ground | 11 |
| candidate holds a decoration plant, which may have arrived after the scan | 45 |
| terrain permitted the column on both sides, so only a draw decided it | 9 |

The support-cell measurement that exonerated world state had read the cell
*beside* the moss, not the cell the scan had to pass *through*, and 45 of the
107 "terrain" readings there were our own nested vegetation, which is downstream
of the patch and says nothing. Of the 62 cells the verdict can decide, 52 name a
state difference at the deciding cell; 9 are draw-decided.

That is the answer to the question the clay investigation inherited: the moss rim
is hypothesis **(b') - the world state the scan walks**, amplified. Acceptance is
what consumes the `depth` sample, and `distributeVegetation` rolls once per
accepted column, so one state-driven rejection early in a patch's column loop
shifts every later draw of that instance and the rest of its rim splits on the
edge roll alone. Scattered one- and two-cell fragments on both sides is therefore
the expected shape of a *single* earlier difference per patch, not of 107
independent terrain errors - which is why no predicate fix moves the count.

The pool's own output sitting in 15 of those candidate cells names the ordering
directly: the moss patch and the clay pool contend for the same cells, and
whichever scans first decides both. That is the same mechanism as `2c29024`,
which raised parity by changing which neighbours had carved first, and it is the
same one the failed uniform-order experiment says a fixed sweep cannot express.
`decorationSources` still special-cases only (0,0) and (1,0); every other target,
including (-1,-1) which carries a third of the residual, falls to the
target-first default branch. Deriving that order once from
`ChunkStatusTasks`' scheduling, rather than adding a third `if`, is the remaining
work on this family.

### VegetationPatchFeature, now verified line for line, and the one real win

Reading the whole method pair against the jar closed every semantics question on
this path, and each of these is now matched in `vegetation_patches.go`:

- Both scan loops are bounded by `verticalRange` **independently** - the counter
  is reset between them (`istore 20` twice, offsets 207 and 249), so a column may
  travel up to twice the range. Phase 1 walks while `isAir` (bootstrap #1 =
  `BlockStateBase::isAir`) in `surface.getDirection()`; phase 2 walks while
  `!isAir` (bootstrap #2 = the `lambda$placeGroundPatch$0` synonym) in the
  opposite direction. `belowState` is read before the emptiness test, and the
  test is `isEmptyBlock(pos)` on the *candidate*, not on the ground cell.
- `placeGround` re-samples `groundState` on **every** iteration, not once per
  column. Irrelevant for this chain: `clay`, `moss_block` and both pool configs
  ship `simple_state_provider`, which is draw-free.
- `WeightedStateProvider` is `WeightedList.getRandomOrThrow`: exactly **one**
  `nextInt(totalWeight)` then a prefix walk. `totalWeight >= 64` picks `Compact`
  over `Flat`, and the two differ only in storage, so `moss_vegetation`'s 96
  points of weight are one draw and a linear scan - what we do.
- `isExposedDirection` calls `BlockState::isFaceSturdy` (`SupportType.FULL`), not
  the blocks-motion test. We used `fullSolidState` there; the two separate on
  farmland, sculk sensors and spawners, none of which the fixture puts beside a
  pool, so the fix is measured at zero cells here and kept anyway.
- The waterlogged subclass's `placeVegetation` runs super at `placementPos.below()`
  and then forces `waterlogged=true` on whatever landed there. Net effect: the
  nested feature is placed *in* the water cell rather than above the ground, and
  the surface offset is not overridden - which is what
  `patchVegetationPosition` expresses.

That list claimed the semantics of the algorithm were closed, and they were. What it
missed was the *membership* of the tag the algorithm consults, which is a different
bug in the same function and was still live: `geodeTagIDs` resolved each
`moss_replaceable` member through `nameToStateID(name, nil)`, i.e. the block's
**default state** only, while vanilla's test is `BlockState.is(TagKey<Block>)` and a
block tag is a set of **blocks** - so every state of a member matches.

That distinction is not academic here, because `moss_replaceable` includes
`#minecraft:cave_vines`, which carries 52 states in this build: `age` takes
26 values (0..25) and `berries` two. Every
cave-vine state other than the default was therefore read as non-replaceable, and
`placeGround`'s column walk breaks on a non-replaceable cell - so a vine hanging in
a lush-cave ceiling aborted a patch column that vanilla walks straight through,
which changes whether the patch is accepted and every draw after it.
`TestTagStateIDsAreBlockScoped` now asserts block scoping against an
independently built set. Two counts are worth keeping apart: over the whole tag the
default-only reading was short by **59 states**, but over the 123 distinct states
this capture actually holds the gap is **eight**, and all eight are
`minecraft:cave_vines` / `minecraft:cave_vines_plant` - seven ages of the one, the
non-default state of the other. Only the second is a number a fixture can fail on,
which is why the test restricts itself to it.

Measured effect on this fixture: **zero cells** - 99.916%, 330 mismatches, clay
chain 96/22/9, all identical before and after - because none of those states
happens to sit in a code path that consults the tag here. So `generatorVersion`
stays at 37, per the rule that it moves only when output moves.

The scope turned out to be much wider than the vine that revealed it, which is what
makes the fix worth its churn. This was not one caller's bug: the helper was shared
by geodes, lakes, ore targets and vegetation patches, and a **second copy** existed
as `lakeTagIDs` with the same defect and no fix. Resolving it once as `tagStateIDs`
and then asking every tag the configured features reference exposes seven that the
default-only reading got wrong on states this very capture holds:

| tag | non-default states of its members | of those, in this capture | subsystem |
|---|---|---|---|
| `moss_replaceable`, `lush_ground_replaceable` | 59 | 8 (`cave_vines` ages, `cave_vines_plant`) | vegetation patches |
| `geode_invalid_blocks` | 30 (`water`, `lava`: `level` 0..7 times `falling`) | 6 | geodes |
| `features_cannot_replace` | 72 | 1 (`chest`) | lakes, monster rooms |
| `lava_pool_stone_cannot_replace` | 457 | 1 (`chest`) | lakes |
| `mangrove_logs_can_grow_through` | 102 | 5 (`vine`) | mangrove features |
| `mangrove_roots_can_grow_through` | 80 | 5 (`vine`) | mangrove features |

`chest` is the one to notice: monster rooms place chests, and the block has 24 states
- 4 `facing` times 3 `type` times 2 `waterlogged` - so a default-only reading
recognised one of the 24 and was therefore asking a different question than vanilla
about the other 23, every non-default orientation included. `TestTagStateIDsAreBlockScoped`
now enumerates every `#minecraft:` reference in every configured feature rather than
the tags someone thought to list, and it was checked by regressing the helper and
watching it name all seven.

The third column is the honest one for a fixture: a tag can hold hundreds of
states the generated region never contains, and then the bug is latent rather than
absent. Measured against the two lake tags, `lava_pool_stone_cannot_replace` has 62
members and they are not what its name suggests - **logs, wood and leaves** from
eleven tree species, with only three single-state blocks (`bedrock`,
`reinforced_deepslate`, `spawner`) among them - but of all 62 only two appear in
this capture at all: `bedrock`, which cannot differ, and `chest`, in two of its 24
states. `features_cannot_replace` is the same two. So the 457 and the 72 are mostly
leaves (`distance` times `persistent`) and mostly blocks this region never places,
and a single chest state is the live part.

The mangrove rows are the ones still open. `vine` is present in five non-default
facing states, so the *states* exist here; whether any code path in this fixture
consults `mangrove_logs_can_grow_through` is not measured, so those rows are
recorded as latent rather than as reachable - the difference matters if a mangrove
capture is ever added, and nothing currently pins it either way.

The same expansion existed a third time, in `disks.go`, and two things written about
it before they were measured were wrong in opposite directions:

- No disk target list in 26.1.2 contains a `#tag` - the entries are `dirt`, `clay`,
  `mud`, `grass_block`, `podzol`, `mycelium`, `coarse_dirt`, `snow_block` and `ice`.
  So the "a tag entry resolves to nothing and is dropped silently" failure mode was
  invented, not latent. Worth recording because it was written down as fact.
- The default-state failure was genuine and only a behavioural test shows it:
  `grass_block`, `podzol` and `mycelium` each have two states, so the old
  resolution left snowy grass out of `disk_gravel`, `disk_sand` and `ice_patch`.
  Filling a chunk with a non-default state and running `placeDisk` replaces 145
  cells now and **0** with the previous code.

That contrast is the lesson. The first version of the disk check called the shared
helper and asserted about the helper's own answer, so it passed identically on the
broken and the fixed code - the precise tautology the paragraph above warns about,
committed in the same file as that warning. `TestDiskReplacesNonDefaultTargetState`
runs the feature and counts changed cells instead, and was confirmed by reverting
`disks.go` and watching it fail. A guard that has never been seen to fail has not
been shown to be capable of it.

A fourth set of the same shape was in `springs.go`, which a sweep of
`nameToStateID(name, nil)` call sites had talked me out of: SpringFeature counts
its five neighbours against `valid_blocks` and requires an exact `rock_count`, so
that set is a membership test too, not a block to place. The reachable block is
**deepslate** - `spring_water` and `spring_lava_overworld` both list it and it
carries three states in this build - and
`TestSpringCountsNonDefaultValidStateAsRock` lays a cross of non-default deepslate
and demands falling water; on the old expansion the cell stays `minecraft:air`.

The two blocks that are *not* the reachable one are the cautionary detail. A
comment named powder snow and hanging gravel before anyone looked, on the reasoning
that their property names imply several states; both have exactly one. Guessing
which blocks are multi-state from their names is how a wrong fact gets written into
the place a future reader trusts, so the state count now lives in a test that
enumerates it. Output is again unchanged on this seed - 99.916%, 330 cells, fluid
22 - because no non-default deepslate sits in a spring's cross here.

Two more lessons in "real bug, zero cells", and both are why the change is kept
anyway: reachable in the data and reachable in the code path are different
questions, and a correct port is worth having on a seed where it is silent, because
the next capture is not this one. It is also a reminder that "verified line for
line against the jar" covers the control flow and not the inputs - the semantics of
`replaceable` were never re-read, only its use - and that a duplicated helper is how
one fix leaves two bugs behind.

The one place the reading paid off in cells: `WaterloggedVegetationPatchFeature`
builds `waterSurface` by iterating the ground **HashSet**, so the water set's
insertion order is the ground set's hash order, and the roll then iterates
*that* set - within-bucket ties are decided by insertion order. We appended in
column order instead. Fixing it is worth **29 cells and 12 fluid mismatches** on
seed 12345, 99.909% -> 99.916%, `generatorVersion` 36 -> 37. Pool roll order is
not a cosmetic detail: with ~30 water cells over a 64-slot table, ties are the
normal case, and every draw after the first collision belongs to a different
cell than it did for vanilla.

### The last 22 clay anchors are not an ordering problem either

`TestVanillaLushClayDiff` is now permanent (`42c9bc8`), with the budget
`missing=96 / clay anchors=22 / extra clay=9` as a ratchet rather than an env-gated
printf. Splitting the 96 into anchors (the plain capture itself reports clay) and
cascade cells relocates the whole question: the anchors are 0 in (0,0), 0 in
(-1,-1), 2 in (1,0) and **20 in (0,1)** - at columns x=5..8, z=16..18, y=-36..-31,
which is (0,1)'s z-min edge. So vanilla's pool there is a write from source (0,0)
into its neighbour, exactly the cross-chunk case this investigation began with,
and it is the only unexplained clay left.

The full per-chunk numbers, measured on `generatorVersion` 37 with only the
`lush_caves_clay` chain removed from the capture - the ratchet's own output, so
the next person does not have to re-run it to know where the budget sits:

| target | block mismatches | missing | of which clay anchors | extra clay |
|---|---|---|---|---|
| (0,0) | 78 | 10 | 0 | 0 |
| (1,0) | 90 | 14 | 2 | 6 |
| (0,1) | 58 | 22 | **20** | 0 |
| (-1,-1) | 104 | 50 | 0 | 3 |
| total | **330** (99.916%) | 96 | 22 | 9 |

Two things that table says which the totals hide. (0,1) is the only chunk whose
missing cells are mostly *real* clay rather than cascade - 20 of its 22 - while
the other three are almost pure cascade, so 74 of the 96 missing cells are the
moss/vine/water consequence of a pool that moved, not a pool we failed to place.
And (1,0) is where we place clay vanilla does not (6 of the 9 extra cells), which
is the opposite failure and will not be fixed by the same change.

Those are *mismatch* counts, and "anchor" is doing a second job in the plan that has
to be pinned before the two numbers get read as one. Step 3 spoke of 1,863 differing
cells of which 1,109 were anchors, and that is not a count of our errors at all: it
is the **footprint of vanilla's own chain**, the cells where
`vanilla_overworld_12345.bin` and `vanilla_no_lush_clay_12345.bin` disagree.
Measured again against the shipped fixtures: still 1,863, of which 1,109 are clay in
the plain capture, 172 are water and 582 are something else. It has not moved, and
cannot, because it is a property of the captures rather than of our generator. The
22 in the table is a different quantity wearing the same word - anchors *we* are
missing, i.e. the intersection of that 1,109 with our 96 - and a cell vanilla's chain
writes that we reproduce correctly is an anchor by the first definition and nowhere
in the second table. That is why the instruction "filter to the 1,109 when judging
position" cannot be implemented as written: the ratchet has to filter on the
intersection, and does.

That table is the post-fix state. The baseline the plan asked Step 3 to record -
the same measurement on the integrated tree, before any generator change - was
taken but never written down, and only the parity half of it survived in a commit
message. It has now been reconstructed by running both tests in a worktree at
`ce1436c`, the first green commit after the upstream rebase, with the current
`data/` registry copied in (`data/` is gitignored, so a worktree would otherwise
have no datapacks and the generator would not run at all):

| chunk | v36 baseline missing / extra | v37 now missing / anchors / extra |
|---|---|---|
| (0,0) | 16 / 0 | 10 / 0 / 0 |
| (1,0) | 14 / 6 | 14 / 2 / 6 |
| (0,1) | 22 / 0 | 22 / 20 / 0 |
| (-1,-1) | 67 / 3 | 50 / 0 / 3 |
| total | **119 / 9** | **96 / 22 / 9** |

The reconstruction is not assumed, it is checked: that worktree reproduces the
v36 parity record exactly - 89/90/58/122 residual cells, 99.909%, 34 fluid
mismatches, the same numbers the `ce1436c` commit message quotes and the same
29-cell / 12-fluid delta the waterlog fix claims - so old code against the
current registry is the tree that was measured, not a near-relative of it.

What the comparison is actually worth is the third column read against the second.
The fix bought 23 clay-chain cells, all of them in (0,0) and (-1,-1), and it moved
**nothing** in (0,1): 22 missing before, 22 missing after, all 20 of them anchors.
The residual pool problem this investigation set out to solve was therefore not
touched by the one generator change that came out of it, which is why it is still
open and why it is a state-history problem rather than a water-set-ordering one.

Two probes were run against it, and both came back with numbers.

First, `REGIONIO_SETBLOCK_TRACE` on four of those cells. Same source (0,0), same
seed, same reseeded stream - yet `setBlock` is *called* for all four when the
target being generated is (0,0) or (-1,-1), and for two of them when the target is
(1,0) or (0,1). Nothing is rejected by the ±1-chunk gate or by an unloaded chunk;
the calls simply are not made. Widening it, `REGIONIO_LUSH_CLAY_TRACE` prints the
positions the `random_boolean_selector` step hands to `clay_with_dripleaves`:

| target being generated | source (0,0)'s `lush_caves_clay` positions |
|---|---|
| (0,0) | (5,-29,12), (5,-30,13) |
| (-1,-1) | (5,-29,12), (5,-30,13), (4,-7,1) |
| (1,0) | (5,-29,12), (2,-41,12) |
| (0,1) | (5,-29,12), (2,-41,12) |

The first position agrees everywhere, so the reseed and the index agree; the
stream diverges *inside* the feature. That is the mechanism the moss section
already named - `depth` is sampled only for accepted columns and
`distributeVegetation` rolls per accepted column - so a state difference mid-patch
moves every later position of that same feature. It also means our model has no
single answer for what source (0,0) places: the answer depends on which chunk
happens to be the centre of the region we are building.

Second, the ordering hypothesis that follows from that - the centre decorating
before its neighbours scrambles the neighbour's stream - was tested directly and
failed. Adding (0,1) to `decorationSources`' scanline branch, so source (0,0) runs
*before* the centre instead of after it, leaves the position table byte-identical
((5,-29,12), (2,-41,12)), leaves the anchors at exactly 20, and costs 15 parity
cells: (0,1) 58 -> 73, total 330 -> 345, 99.916% -> 99.912%. Reverted; the numbers
are the reason task 8 must not be closed by tuning the order.

So the remaining clay family is decided by something the per-target region changes
other than pass order. The window was the first candidate - a region holds ±2 base
terrain around the *target*, and a read outside it returns air (`getBlock` on a nil
chunk), which the scan and the exposure tests would consume as draws - but the
grouping does not fit it: chunk (0,1) is out of window for target (-1,-1) and in
window for target (0,0), and those two agree.

What fits that table with no counterexample is **whether the sources on (0,0)'s
south side had already decorated in the same region**:

| target | sources before (0,0) | source (0,0)'s positions |
|---|---|---|
| (0,0) | (-1,-1), (0,-1), (1,-1), (-1,0) | (5,-29,12), (5,-30,13) |
| (-1,-1) | itself, as the centre | (5,-29,12), (5,-30,13), (4,-7,1) |
| (1,0) | (0,-1), (1,-1), (2,-1) | (5,-29,12), (2,-41,12) |
| (0,1) | none, or (-1,0) in the reverted experiment | (5,-29,12), (2,-41,12) |

Every row of that table holds source (0,0) fixed and moves the target, which left
the question the plan actually complained about unasked: what does source (-1,-1)
itself place? Probing it (target (-1,-1), source (-1,-1)) answers it in one line -
a single pool at (-15,-16,-13), and note the reference:
`minecraft:clay_pool_with_dripleaves`, the *non*-waterlogged variant, where
source (0,0)'s pools all chose `clay_with_dripleaves`. That cell is the one
`REGIONIO_SETBLOCK_TRACE` was run on earlier, which is a useful cross-check: the
trace attributed the adjacent (-15,-17,-13) clay write to source (-1,-1) inside
`placeVegetationPatch`, one block below the pool position the probe now reports for
the same source. Both are asserted, so neither can quietly move.

That started as a correlation and is now a measured cause. `TestProbeLushClayStream`
shares the production dispatch through `placeScheduledVegetationFeature` (it had a
private copy of the switch, which is how it silently dropped `minecraft:kelp` and
`minecraft:seagrass` and every error), and once shared it reproduces the
generator's positions for all four targets byte for byte - in 1.3s instead of 8.7s
and with no fixture. That makes removal possible, and removal is the difference
between naming a correlation and naming a cause. For target (0,0), probed source
(0,0), each predecessor's *whole* contribution removed:

| predecessor removed | source (0,0)'s positions |
|---|---|
| none | (5,-29,12), (5,-30,13) |
| (-1,-1) | (5,-29,12), **(2,-41,12)** |
| (0,-1) | (5,-29,12), **(2,-41,12)** |
| (1,-1) | (5,-29,12), (5,-30,13) |
| (-1,0) | (5,-29,12), (5,-30,13) |
| (1,0) | (5,-29,12), (5,-30,13) |
| (-1,-1) and (0,-1) | (5,-29,12), (2,-41,12) |

So it is not (-1,-1) in particular. The two south-west sources are jointly
required and individually required: the second pool sits at (5,-30,13) only when
*both* have decorated, and losing either one moves it to (2,-41,12) - losing both
moves it no further than losing one. The west, north-west and east neighbours are
irrelevant on their own. That reading was re-measured after the seam landed, and
the "removing both leaves the feature placing nothing" row that stood here before
was wrong: with a working multi-entry skip list the result is the same two
positions, (5,-29,12) and (2,-41,12).

What the move is *not* is now measured too. Dumping both candidate columns - (5,*,13),
chosen only when both neighbours ran, and (2,*,12), chosen when either is missing -
at the moment the clay feature starts, the two runs are **byte-identical** at both
columns. So the second pool was not displaced because its floor was at a different
height. Between the two positions the only code that runs is the first pool's own
placement, and `x`/`z` of a candidate come straight off the draw stream, so
different `x`/`z` means the first pool spent a different number of draws. That is
the depth-per-accepted-column and roll-per-accepted-column coupling the moss section
already named, applied one feature to itself: (0,0)'s first pool at (5,-29,12) has a
disc whose accepted columns are decided by the cave state inside (0,0) - and sources
(-1,-1) and (0,-1) are entitled to write inside (0,0), because the feature write
radius is one chunk - so their absence changes how many columns that disc accepts,
hence how many draws it spends, hence where the second pool lands.

This is the same (b') mechanism as the moss rim, with the geography and the
consumer both named: the state that matters is not "what the scan walks over at the
candidate" but "what the first pool's footprint touches", and the neighbours that
matter are exactly the two whose Chebyshev-1 write window covers (0,0)'s south-west.

That inference is now measured. `REGIONIO_LUSH_CLAY_PROBE_STATE=1` digests every
chunk of the region at the instant the clay feature starts, so a removal can be
asked *which chunks it changed* rather than only *whether it changed the answer*:

| chunk | none removed | (-1,-1) removed | (0,-1) removed |
|---|---|---|---|
| (0,0) - the probed source itself | `aade1eb0` | `df14e32f` | `880c5fe3` |
| (0,1) - the chunk holding the 20 anchors | `1820d514` | `1820d514` | `1820d514` |

Read together with the identical-candidate-columns result above, that is the whole
mechanism in two rows: the neighbours' cross-chunk writes land inside (0,0) - not in
(0,1), whose own pass has not run yet at this point, which is why (0,1) is byte-for-byte
the same in all three - so the clay feature reads a chunk partly authored by its own
neighbours, and the columns its first pool happens to touch are exactly where those
writes are. It also explains why the removal set is not symmetric in a simple way:
(-2,1) changes when (0,-1) is removed even though (0,-1) cannot write there, because
the source that can, (-1,0), runs *after* it and reads what it left. State propagates
through the source sequence, so "which predecessors matter" has no local answer.

The prediction it leaves is correspondingly concrete: replaying (0,0) for a (0,1)
target loses the second pool for the same reason as skipping (-1,-1), and fixing it
means (0,0) seeing one world with a single history, not a window chosen per target.

That prediction has now been tested, and it holds. `REGIONIO_LUSH_CLAY_PROBE_EXTRA_SOURCES`
replays sources from outside the target's 3x3 first, widening the loaded window to
reach them, which is the one thing the production path cannot do. Target (0,1),
probed source (0,0), census of clay cells in chunk (0,1):

| build | source (0,0)'s pools | clay cells in (0,1) | of the 20 anchors |
|---|---|---|---|
| as production does it now | (5,-29,12), (2,-41,12) | 1276 | **0** |
| (-1,-1) and (0,-1) decorated first | (5,-29,12), (5,-30,13) | 1288 | **12** |

Twelve of the twenty recovered and **nothing lost** - the diff between the two
censuses is exactly those twelve cells, all of them at x=6..8, z=16..18, y=-33..-36,
the cluster the (5,-30,13) pool's disc covers. The eight still missing are at
x=14,15, z=31, the far corner of the chunk, which no pool of source (0,0)'s reaches;
the probe stops at the probed source by design, so those belong to a later source's
stage-9 pass that this run deliberately does not replay, and their count is a
lower bound on the fix rather than its ceiling.

Two consequences. First, this is not reachable by any per-target order: for target
(0,1), source (-1,-1) is not in the 3x3 whose neighbourhood the region loads, and
`ensureSourceNeighborhood` *errors* rather than generating, so a source two chunks
from the centre cannot be replayed at all without widening the window. Second, the
effect is entirely within one region: `TestGeneratedChunkOrderIndependence` checks
that asking the generator for the four chunks in reverse order returns identical
content, so `vanillaTerrainCache` and its clones are not leaking decoration between
requests, and the parity numbers do not depend on the order a test asks in. What is
left is the architecture: one shared region per batch, each chunk decorated exactly
once in one global order - then every cell has one history and "what does source
(0,0) place" stops having four answers. That is task 8, and the prediction it was
going to be judged against is no longer pending: the extra-sources run above is
that experiment, done on the probe instead of on the generator, and it recovers 12
of the 20 anchors with no regression. What the production change still has to
establish is the global order that makes this the only answer rather than one of
four.

### The verdict, in one place

The clay investigation opened with four candidate causes. Each is now closed by
measurement rather than elimination, and the ordering matters because three of
them were never going to yield to reading the patch code harder.

| hypothesis | status | what closed it |
|---|---|---|
| (a) wrong `featureIndex` | excluded | the index sweep: exactly one of 41 values reproduces vanilla's pools and it is the one `FeatureSchedule` reports; the per-biome alternative places nothing |
| (b) cross-feature draw drift | structurally impossible | `SetFeatureSeed` is a full reseed per feature, so one feature's draw count cannot move another's stream |
| (c) wrong decoration seed | excluded | one `DecorationRandom(seed, cx, cz)` feeds stages 1/2/3/6/9, and those reproduce vanilla across 99.9% of the fixture; a wrong seed breaks the ores first |
| (d) a modifier's draw count or constant | excluded for this chain | `random_offset` and `environment_scan` both pinned against the disassembly and now under test; neither can be off by one cell |
| (b') the world state the feature reads | the answer | per-target position table, predecessor-removal table, per-chunk state digest |

The shape worth remembering is that (b') acted through the *draw count of one
feature's own earlier placement*, not through the scan reading different terrain at
the candidate. That is why it survived every pass over the patch code: the
positions are decided by how many columns an earlier pool accepted, and that number
is decided by neighbours writing into the source's own chunk. Any future
"the scan must be wrong" instinct should be checked against this table first.

E2 - invert our `environment_scan` over all 321 start Y on vanilla's column, taken
from the no-clay capture, and see whether vanilla's landing is reachable - was
attempted and does not work, for a reason worth writing down before anyone retries
it. Its premise is that the differential capture is the state the scan saw. It is
not. A placed feature with `count: 62` runs its candidates in sequence, so every
pool after the first in a column scanned terrain its own chain had already altered,
and removing the whole feature removes exactly that: the capture is vanilla's world
with the chain's contribution deleted, which is no moment the chain actually saw.
Compounding it, the no-clay capture is not clay-free - `(-16, -21, -16)` is
`minecraft:clay` in it - because other features place clay too.

The symptom is that the answer tracks the definition rather than the scan, and three
variants make that concrete. Over every clay cell in the plain capture, treating the
lowest clay of a run as the scanned floor: 0 reachable, 2063 unreachable, 167
candidates that were not solid and so were never targets. Same universe, floor moved
down one cell: 35 reachable, 2194 unreachable. Over the differential universe with
the first definition, which is the correct reading of "pool clay": 1 reachable of
349. Two one-cell changes of interpretation move the verdict by two orders of
magnitude, so the number measures the probe's assumption about where a pool floor
is, not the scan. An experiment whose answer is that sensitive to an assumption it
cannot check is not measuring the thing it names. So (d) is closed the way a scan
can actually be closed: `TestEnvironmentScanMatchesVanillaControlFlow` compares the
port against the disassembly of `EnvironmentScanPlacement.getPositions` on columns
built for the purpose, where the expected landing is known rather than inferred
from a capture.

### A note on the commit numbers cited in this file

This branch's history was re-created once, before it was published, to drop
`Co-Authored-By:` trailers from three commit messages. Nothing about the commits
themselves changed - the tip tree hash is byte-identical before and after, and
author, committer and both dates are preserved - but git hashes the message, so
every commit from `51dfe93` (which was `46edf84`) onward has a new number, while
`ce1436c` and anything older kept its own.

The consequence for a reader is small and worth stating rather than discovering: a
SHA written down before the renumbering may name a commit that is not in `main`'s
ancestry at all. `42c9bc8` above is the new number of what was first recorded as
`d0b33ed`, and one commit body in this range still cites `58c886e`, a pre-rewrite
number, for the same reason. `pre-trailer-rewrite` is kept locally as the other half
of that mapping; it is not a branch to build on, only a lookup table between the two
sets of numbers.

## The first land capture, and what it showed about a number that read as a pass

Everything above this line was measured on four chunks that are, as it turns out,
**not a single one of them land**. `vanilla_overworld_12345.bin` covers (0,0), (1,0),
(0,1) and (-1,-1) at seed 12345 and every one of its 1,024 columns has `water` as its
top block; the highest non-air cell anywhere in it is **y=62**, y 63..78 is entirely
air, and no `log`, `leaves`, `sapling` or `bamboo` state occurs between y=60 and y=200.
So the parity diagnostic's `surface=0` mismatched cells - quoted in README and used to
rank the residual families - never meant "the surface is correct". It meant nothing
above the waterline was ever generated, in the one band where decoration is visible.

`testdata/vanilla_land_12345.bin` is the first capture with a surface: chunks
(16,-40) taiga, (16,-31) old-growth pine taiga, and (-40,21), (-40,20) plains, chosen
by scoring our own biome and height path over a grid first, which is a legitimate
prediction only because biome and heightmap parity are separately asserted. Captured
with the existing tool unchanged, ~30 s per run.

### Baseline, printed by `TestVanillaLandBlockParity`

| measure | value |
|---|---|
| block parity on land | 389,834/393,216 = **99.140%** (3,382 cells) vs 99.916% on ocean |
| bands | deep 700, underground 883, waterline 51, **surface 1,748** |
| biomes | **6,144/6,144 exact** - the biome prediction holds on land too |
| `WORLD_SURFACE` heightmap | 417/1,024 = 40.7% |
| `MOTION_BLOCKING` heightmap | 557/1,024 = 54.4% |
| `MOTION_BLOCKING_NO_LEAVES` | **990/1,024 = 96.7%** |

The heightmap split is the finding that decides the order of work, and it is the only
one of the three that ignores leaves. Terrain height is therefore close to right, and
the surface gap is canopy and undergrowth placement - so no terrain bug is hiding in
front of the decoration port, and the 34 columns that differ even with leaves excluded
are consistent with trunks standing in the wrong place.

### Three chunks, three different failures

| chunk | biome | vanilla tree cells | ours |
|---|---|---|---|
| (16,-40) | taiga | 285 | 296 |
| (16,-31) | old-growth pine taiga | **353** | **57** |
| (-40,21) | plains | 37 | **167** |
| (-40,20) | plains | 114 | 152 |

Aggregate counts would have averaged these three into "672 vs 789, slightly under".
They are not the same bug. Dense taiga is roughly the right *amount* in the wrong
*places*; old-growth pine taiga is a 6x shortfall, which is the hand-written path's
own admission rate made visible - it accepts only 16 of the 39 configured trees, and
mega trunks are not among them; and plains is the reverse failure, over-planting by
4.5x.

### What the plains chunks contain instead of trees

Disabling `trees_plains` removes **zero** cells from either plains chunk. Their
vanilla leaf cells are `oak_leaves` **with no logs anywhere near them**: 135 leaf
cells and 50 `leaf_litter`, i.e. bushes (`patch_bush`, firefly bush), not trees. The
data agrees: `trees_plains` opens with a `count` of
`weighted_list {0 weight 19, 1 weight 1}` - roughly one attempt per twenty chunks -
while `trees_taiga` and `trees_old_growth_pine_taiga` use `{10 weight 9, 11 weight 1}`.
Our hand-written path ignores that weighting entirely and plants full canopies on
open plains.

Per-chain footprints, from the three `-disable-placed` captures of the same chunks:

| chain disabled | cells it owns | composition |
|---|---|---|
| `trees_plains` | 0 on land (2 unrelated `cave_vines` cells, see below) | - |
| `trees_taiga` | 197 in (16,-40) | 171 spruce_leaves, 22 spruce_log, 3 dirt→grass, 1 vine |
| `trees_old_growth_pine_taiga` | 102 in (16,-31) | 85 spruce_leaves, 15 spruce_log, ~15 fern/grass churn |

Two things worth recording about the method itself, because the clay chain made it
look cleaner than it is.

1. Removing a canopy legitimately moves *other* features: the old-growth diff also
   turned fern and short_grass on and off. Not stream drift - `SetFeatureSeed`
   reseeds per feature - but cause and effect through the world: `heightmap` is a
   placement modifier, and MOTION_BLOCKING is exactly what changed. A differential
   isolates a chain's writes plus everything that reads the heightmap through the
   removed volume.
2. Disabling `trees_plains` perturbed two `cave_vines` cells in a chunk whose biome
   does not reference `trees_plains` at all. That is unexplained, small, and recorded
   rather than rationalised: either the named placed feature is reachable from
   somewhere else in the graph, or the count-0 modifier perturbs something the
   reseed-per-feature argument says it cannot. Worth knowing before the technique is
   trusted blindly on a surface chain, which is what it is about to be used for.

The biome and plant census above is also the first evidence about *flora* rather than
trees: `leaf_litter` (50 cells) exists in 26.1.2 and our generator does not place it,
and `patch_bush` is the feature responsible for most of what our tests currently
count as "vanilla has leaves here". Both belong to the flora step, not the tree step.

## Surface decoration, part 1: the model was wrong before the algorithm was

The port starts from a claim in this file that read as a limitation of effort -
"only straight trunks place" - and turns out to have been a limitation of types.
`TreeFeatureConfig` declared four of `TreeConfiguration`'s nine fields, and typed
the placer numerics as Go ints. The data does not hold ints there: pine's
`height`, spruce's `offset`, `radius` and `trunk_height`, and mega_pine's
`crown_height` are int providers. Measured effect on the 39 configured tree
features in the pack, under the old struct's own rules:

| outcome | count | which |
|---|---|---|
| decoded and validated | 34 | everything else |
| failed to decode at all | 2 | `minecraft:pine`, `minecraft:spruce` |
| decoded but rejected by validation | 1 | `minecraft:azalea_tree` (weighted foliage provider) |
| decoded, validated, and **silently wrong** | 2 | `minecraft:mega_pine`, `minecraft:mega_spruce` - radius 0, height 0 |

So 36 of 39 were "usable" and two of those were corrupt in the quiet way, which is
the shape this branch keeps hitting: a field a struct does not declare is absent
rather than missing, so nothing fails. `ea57211` carries the fix and
`TestTreePlacerSpecMatchesThePack` re-derives its field tables from the pack; the
commit message for it said "33 of 39" and that number was never measured - the
table above is, and supersedes it.

Two facts settled against the jar while doing this, both of which contradicted what
this file and the plan had asserted:

- The FEATURES write radius of 1 is set by `ChunkPyramid`'s step builder, not by
  `ChunkStatus`, which carries no radius; the step requiring `CARVERS(1)` plus
  `STRUCTURE_STARTS(8)` is the one that raises it to 1, and `WorldGenRegion`'s
  `ensureCanWrite` enforces it. This is what makes `decoration_region.go:57-75` a
  port rather than a simplification.
- `TreeFeature.place` has no preamble reading the config. It collects position
  buckets, calls `doPlace`, runs decorators only when a bucket is non-empty, and
  returns the non-emptiness of a bounding box - not a success flag. Inside
  `doPlace`: `getTreeHeight`, `foliageHeight`, `foliageRadius`, root-mapped origin,
  the `minimum_size` clip through `getMaxFreeTreeHeight`, `placeRoots`,
  `placeTrunk`, then foliage per attachment. `belowTrunkProvider` is consumed by the
  base `TrunkPlacer` (inside `placeTrunk`), and `ignoreVines` is read in
  `getMaxFreeTreeHeight` next to `FeatureSize.getSizeAtHeight` - so both belong to
  the placer implementations, not to a place() prologue that does not exist.

## Surface decoration, part 2: the placer bodies, and the filter that had been refusing every tree

Verified against `versions/26.1.2/server-26.1.2.jar` with `javap -p -c`, for the six
configured trees the taiga and plains chains reach: `oak_bees_005`,
`fancy_oak_bees_005`, `pine`, `spruce`, `mega_pine`, `mega_spruce`.

`TreeFeature.doPlace` samples three values before it tests anything —
`getTreeHeight`, then `foliageHeight(random, trunkHeight, config)`, then
`foliageRadius(random, trunkHeight - foliageHeight)` — and hands the trio to
`FoliagePlacer.createFoliage` as `(maxFreeHeight, attachment, foliageHeight,
foliageRadius, offset)`. Two consequences that are easy to get backwards:

* The row count of a canopy comes from `foliageHeight` and its width from
  `foliageRadius`. Reading them the other way shortens or widens a tree by their
  difference; blob and pine both did, and neither showed up as an error.
* The `i` that `PineFoliagePlacer.foliageRadius` bounds its extra `nextInt` with is
  the `trunkHeight - foliageHeight` argument, not the trunk height.

`doPlace` then clamps: `getMaxFreeTreeHeight` returns the tallest prefix whose every
row, widened by `minimum_size`, is free, or `y - 2` at the first row that is not. The
clamped value is what `placeTrunk` and `createFoliage` receive, while
`foliageHeight`/`foliageRadius` keep the values sampled from the unclamped height. A
config without `min_clipped_height` still places when the trunk is clipped — it does
not refuse.

`FeatureSize.getSizeAtHeight(a, b)` is called as `(selfHeight, height)` — the reverse
of its declared parameter names — and `TwoLayersFeatureSize` reads only its **second**
argument, so `lowerSize` applies while `y < limit` and `upperSize` from there on,
regardless of the configured trunk height.

Per-placer facts, all from the bodies: `GiantTrunkPlacer` returns
`new FoliageAttachment(pos.above(h), 0, true)` — radius offset **0**, the double trunk
widening rows through `placeLeavesRow`'s extra column instead. `StraightTrunkPlacer`
paints through `placeLog`, which tests `validTreePos` *before* sampling the trunk
provider; `GiantTrunkPlacer` gates with `isFree`, which additionally admits an
existing log. `TrunkPlacer.placeBelowTrunkBlock` calls
`belowTrunkProvider.getOptionalState`, and a rule-based provider with no matching rule
and no fallback returns null there — so it writes nothing, where the same provider's
`getState` would have returned the standing block. `SpruceFoliagePlacer`'s width
breathes (grow to a limit that itself grows, then collapse to a remembered value), so
it is not a cone. `MegaPineFoliagePlacer` iterates absolute y values, calls
`placeLeavesRow` with `y = 0`, and bumps the radius by one on even rows when it equals
the previous row's. `FancyTrunkPlacer` is a circle-segment outline (`treeShape`),
limbs rasterised by `makeLimb` in `max(|dx|,|dy|,|dz|)` steps with the log axis set from
the dominant horizontal component, and `trimBranches` dropping anything below
`0.2 * clusterHeight`.

`TreeDecorator.Context` builds its `logs`, `leaves` and `roots` lists from three
`HashSet`s and then sorts each with `Comparator.comparingInt(BlockPos::getY)` — a
stable merge sort with no tie-break, so the observable order is insertion order
restricted to equal-height groups. The trunk setter records the block **under** the
trunk as a log, which is why a mega pine on bare stone ends up standing on podzol: the
below-trunk provider puts dirt there first, and dirt is in
`beneath_tree_podzol_replaceable`.

The bug that made all of this moot for as long as it stood was upstream of the
placers. `minecraft:would_survive` is `state.canSurvive(level, pos)` — a question about
the *named* state, asked of a position that normally holds air. This build also required
the cell to already contain that state, which is a question about a placed block, so the
`block_predicate_filter` on `trees_plains`, `trees_taiga` and every `*_checked` wrapper
refused every candidate position and the region path placed **zero** trees.
`VegetationBlock.canSurvive` is `mayPlaceOn(getState(pos.below()))`, i.e. membership of
`#minecraft:supports_vegetation` = `#substrate_overworld` + `farmland`. `CactusBlock`
never looks below at all — it refuses when any horizontal neighbour is solid or lava —
and `SugarCaneBlock` keeps its own rule. The twelve states that use this predicate in
the pack are now named explicitly, and any thirteenth errors instead of guessing.

Measured, land fixture (four chunks, seed 12345), before to after the replay:
99.140% to 99.208% exact, surface band 1,748 to 1,339, tree-and-plant cells above sea
level **0 to 661** against vanilla's 789, `WORLD_SURFACE` 539/1024 to 604/1024,
`MOTION_BLOCKING` 734 to 788, `MOTION_BLOCKING_NO_LEAVES` 1,002 to 1,004. The ocean
fixture did not move: 330 residual cells, clay 96/22/9, ore parity zero. Still
un-replayed and now counted rather than silent: `dark_oak_foliage_placer` (32 times
across those four chunks, from a neighbouring forest), `fallen_tree` (1), the mushroom
selectors (7).

## Surface decoration, part 3: springs were never replayed, and flora was a percentage

Three things this pass settled by reading the jar rather than the code.

**`fluid_springs` is index 8 of the biome feature list, and the region walk did not
cover it.** The region replayed indices 1, 2, 3, 6 and 9; the per-chunk path replayed
springs on its own. So the two generators were not two implementations of the same
decoration - one of them simply had no springs. Springs now come from the schedule,
seeded `SetFeatureSeed(decorationSeed, index, 8)` like every other stage, and
`TestStage8ScheduleGolden` pins the pair `spring_water`, `spring_lava` for plains and
lush caves so a re-indexing cannot move them quietly.

**`SpringFeature.place` reads the cell the spring would fill.** It must be air or one of
`valid_blocks`, and it is then *not* counted - rock and holes are two independent counts
over the same five cells (west, east, north, south, below). The chunk-local version had
no self-cell test, so a spring could open inside standing water. Its other divergence
was the border: the loop returned as soon as a neighbour fell outside the chunk's own 16
columns, which suppressed every seam-straddling rock column on one side. Reads are
region reads now; `decorationRegion.setBlock` still clips the write.

**`block_blob` is eighteen draws, and index 2 was silently dropping it.**
`BlockBlobFeature.place` walks down until `can_place_on` answers for the cell below, then
runs three rows, each drawing three `nextInt(2)` to size the box and three more to step
the centre afterwards - the step happens on the last row too, before the feature returns.
`TestBlockBlobSpendsEighteenDraws` counts them through a wrapping random source, because
the number is the thing that matters and it is not derivable from the config.
`forest_rock` (mossy cobblestone) sits at index 2 beside the geodes, and the stage-2 walk
tested `configured.Type != "minecraft:geode"` and continued - so boulders, large dripstone
and the frozen-ocean icebergs were all invisible there. The walk now dispatches on the
type and counts what it cannot place; that is how `large_dripstone=18` over the four land
chunks became visible in this commit rather than staying a gap nobody named.

**The flora heuristics had no vanilla counterpart at all.** `placeFlora` took ~4% of
grassy columns and picked from a hard-coded flower list; `placeDesertFeatures` took ~3% of
sand columns; `placeRocks` scattered 2-3 stone-family blocks at ~2% of windswept columns.
Vanilla places that surface through `simple_block` (32 of them, including `flower_plains`,
`patch_grass_plain`, `patch_tall_grass_2`, the mushrooms and `patch_fire`),
`block_column` (8: cactus, sugar cane, pumpkin, bamboo) and `block_blob` - all of which
were already replayed at their scheduled index with the chunk's decoration seed. Both the
heuristics and their call sites are deleted rather than left dormant. Removing them
improved the land fixture by 11 cells (390,100 to 390,111) and the surface band by 11
(1,339 to 1,328); the ocean fixture stayed at 330 residual cells with clay 96/22/9 and ore
parity zero, and the legacy generator - used now by `cmd/gendump` and comparison only -
no longer decorates a surface.

One honest negative: the four land chunks contain **no mossy cobblestone in vanilla at
all**, so `forest_rock`'s replay is mechanically correct and unconfirmed by this fixture -
it paints two cells vanilla does not have. A capture of a windswept or old-growth column
that actually holds a boulder is what would settle it.

## Surface decoration, part 4: leaves carry a propagated property, and a decorator is not a placer

**A generated leaf's `distance` is not chosen by the placer.** `LeavesBlock` declares
`distance=7`; `getDistanceAt` answers 0 for a log, the stored value for another leaf, and
7 for anything else, and a leaf's own value is `min(7, 1 + that)` over the **six axis
neighbours** - not the twenty-six a Chebyshev ball would suggest. `updateShape` never
writes: when the value would change it schedules a one-tick recompute, and
`LeavesBlock.tick` is the thing that finally calls `updateDistance` and sets the block.
A saved region is therefore a world whose queued leaf ticks have already run, and the
comparable target is the settled fixpoint, not a single pass at placement time. That one
property was the entire taiga error: `trees_taiga` owns 194 cells, and we held a tree
state in 193 of them while matching the exact block in 23. Settling the distance takes
that to 192 of 194.

**A missing decorator must not veto its tree, and a missing placer must.** The asymmetry
is measured, not stylistic: `TreeFeature.place` runs decorators after `doPlace` has put
the trunk and canopy in the world, so refusing a tree over a decorator it cannot run
throws away a body that vanilla does place. `place_on_ground` - the leaf litter and
shrubs under birches and beeches - is un-modelled and the shipped build counts it **82**
times over the four land chunks, per occurrence, without losing the tree. Re-measured by
temporarily making `supportsAllParts` veto on it: **101** refusals (one per tree, so the
two counts differ by what a tree's several decorator slots contribute), tree cells above
sea level 577 down to **370**, surface band 945 back up to **989**, and `dark_oak_foliage_placer`
refusals 32 up to 43 because the shared decoration stream moved - cascade, not footprint.
So `supportsAllParts` checks trunk and foliage placers only, and `placeDecorators` counts
an un-modelled decorator by name and carries on.

**A pre-flight check has to be free.** The first version of `supportsAllParts` asked the
trunk placer for its height to confirm the fields were readable - which is
`getTreeHeight`, two `nextInt` calls - so merely validating a config shifted the
decoration stream. It halved the taiga match rate (192 of 194 down to 95) with no
placement logic changed at all, which is the clearest demonstration in this branch of
what a moved draw is worth. `TestSupportsAllPartsSpendsNoDraws` now compares a checked
generator against an unchecked one, and was seen to fail when the drawing call was put
back.

Land is now 390,494/393,216 exact (99.308%), surface band 945 mismatches against the
1,748 the hand-written path left, `MOTION_BLOCKING_NO_LEAVES` 97.9%. The ocean fixture
still does not move: 330 residual cells, clay 96/22/9, ore parity zero.

## Surface decoration, part 5: what the completion audit found in the pack itself

Three facts measured during the audit of this plan rather than carried over from it, each
of which changed a claim that was already written down.

**No nested feature ref in 26.1.2 points at a tree.** The plan asked for the nested-ref
switch in `world/cave_features.go` to "become an error for tree refs". Walking every
`configured_feature` JSON in the embed and resolving each `feature`/`features` member of
the two kinds that switch serves (`simple_random_selector`, `vegetation_patch`,
`waterlogged_vegetation_patch`), the referenced configured types are exactly
`simple_block` (moss_patch, moss_patch_bonemeal, pale_moss_patch),
`simple_random_selector` (clay_pool_with_dripleaves, clay_with_dripleaves) and
`block_column` (moss_patch_ceiling) - all three already handled, zero trees. The thirteen
configured-tree names that do appear as nested-looking references belong to
`random_selector` configs, whose entries name *placed* features, which is the same
ref-kind trap that made an earlier enumeration of this pack report "0 trees": configured
and placed features share their names, so the file kind has to be resolved before the
reference does. What the switch needed was not tree routing but a name on its fallthrough
(`nested:<type>` into the not-replayed counter), and it had to keep returning false,
because `placePatchVegetationFeature` waterlogs a cell only when the nested call reports
that it wrote something - which is also why this switch cannot simply delegate to the
stage-9 seam, whose counted-and-skipped path signals with a nil error.

**The decorator veto experiment had been recorded with stale numbers.** Re-running it
(vetoing a tree over an un-modelled decorator instead of counting and continuing) gives
101 refusals, tree cells 577 down to 370, surface band 945 up to 989 - not the 1,362
first written down, which predates the leaf-distance fix - and `dark_oak_foliage_placer`
refusals moving 32 up to 43 with no placer logic touched, because the shared decoration
stream shifted. The shipped build counts 82 `place_on_ground` occurrences, one per
decorator slot rather than one per tree, and that is what the diagnostic prints; the 164
these notes carried was not reproduced by today's measurement, so it has been replaced by
the reading that is rather than given an explanation.

**The legacy generator's fixture number had drifted unrecorded.** With the hand-written
surface decoration deleted from it, `REGIONIO_PARITY_GENERATOR=legacy` prints
377,329/393,216 (95.960%), where README quoted 95.959%. Re-quoted rather than explained:
the two cells are not attributed to a cause anywhere in this tree, because attributing
them would mean running the legacy path per feature and nothing asserts it.

**The not-replayed tally names one class where the work needs two.** Of the 39 configured
trees this build decodes, 26 have both placers modelled and no `root_placer`; the 13
refused break down as dark_oak trunk+foliage 5, cherry trunk+foliage 2, mangrove
`root_placer` 2, forking 1, bending 1, mega_jungle 1, `bush_foliage_placer` (`jungle_bush`)
1. Reading that off the pack rather than off the counter matters, because
`trunkHeightFromPlacer` implements the *generic* `TrunkPlacer.getTreeHeight` -
`base_height` plus two `nextInt(bound+1)` terms - so it succeeds for a trunk placer this
build cannot place, and `placeTree` reports the first un-modelled part in *sampling* order,
which is the foliage one at `tree_placers.go:128`. The dark forest trees therefore appear
as `foliage_placer:minecraft:dark_oak_foliage_placer=32` even though the same five configs
(`dark_oak`, `dark_oak_leaf_litter`, `pale_oak`, `pale_oak_bonemeal`, `pale_oak_creaking`)
also carry `minecraft:dark_oak_trunk_placer`. Porting only the foliage placer would move
the tally line to `trunk_placer:` and place nothing.
