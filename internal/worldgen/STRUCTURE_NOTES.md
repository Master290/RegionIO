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

## Ocean ruins (ground truth captured, port pending)

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
internal/worldgen/data/structure_template/underwater_ruin/. Open
mechanics for the port: how the piece descends from pos.y=90 to the
saved TPY=50 (ocean-floor height at some reference column), and the
exact IntegrityProcessor draw order (ChunkPos-forked positional random,
not the shared stream).

Seed 12345, committed fixture chunks (0,0) (1,0) (0,1) (-1,-1):

- (1,0): ruined portal — obsidian (17,13,3), crying obsidian (21,13,3),
  gold block (19,18,3), chest (17,13,2).
- (0,0)/(1,0): mineshaft planks/fences near y=-41..-42.
- (-1,-1): unexplained cobblestone+mossy floor at y=35 under gravel/water with
  two chests nearby — attributed to no replayed feature yet. Candidate leads:
  a dungeon whose placement stream we mismatch, or an ocean ruin whose pieces
  reach across the chunk border.
