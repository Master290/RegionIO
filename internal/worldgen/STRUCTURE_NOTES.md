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
  consuming in_square(2) + height_range uniform(1) + random_offset y(1)
  draws with environment_scan and biome drawing nothing. The ~300-cell moss
  ground divergence is somewhere in this chain or in the patch column scan;
  it survives with vegetation disabled, so it is upstream of the vegetation.

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

Leading hypothesis for the vine/moss position divergence: the placement
position streams for stage-9 features interleave with the FEATURE placement
draws (the vine feature itself draws while producing positions of the NEXT
count iteration) - our ForEachPlacementPosition already models this - but
the moss_patch ceiling variant (count 125, same stream family) and cave
vines (count 188) both run per source chunk and both scan/cancel against
cells the clay pools may have already written, so the earlier clay-pool
position divergence cascades into the moss and vine positions. Fixing the
pool positions first (cluster 1) is the cheapest path since everything else
downstream realigns.

Next probes: (a) dump our clay-pool placement positions for source chunks
around (-1,-1) and diff against the vanilla pool at cluster 1 (y=-7..-1,
x=2..3, z=0..3 area); (b) verify the environment_scan up-walk against
vanilla's exact step semantics (allowed-condition is checked BEFORE the
first target test - our implementation matches the bytecode order, so the
suspect is the position stream feeding it).

## Clay-pool position divergence follow-up (cluster 1, chunk (-1,-1))

The misplaced pool (48 cells, y=-7..-1) comes from source chunk (-1,-1)'s
single surviving lush_caves_clay position: ours lands at (-15,-16,-13),
vanilla's pool sits ~15 blocks higher (y=-7..-1). The placement chain is
count(62, constant) -> in_square(2 nextInt) -> height_range uniform
(nextInt(321), min=-64 above_bottom, max=256 absolute) -> environment_scan
(down, max 12 steps, allowed=#air, target=solid) -> random_offset(0,-1
constants, draw-free) -> biome. All modifiers are decoded and match vanilla
byte-for-byte, so the divergence is the STREAM STATE feeding them: either
(a) the scheduled.Index for lush_caves_clay in stage 9 differs from ours,
(b) an earlier stage-9 feature on the same decoration stream consumed a
different number of draws (glow_lichen runs first at count 104..157 - its
position loop interleaves feature draws with placement draws), or (c) our
setFeatureSeed decorationSeed chain differs. The next probe: replay the
source (-1,-1) stage-9 schedule printing the draw index of every position,
and cross-check lush_caves_clay's FeatureSchedule index against
FeatureSorter's step data (set.FeatureSchedule builds indices per possible
biomes; an off-by-one in the shared step list shifts every later feature).
