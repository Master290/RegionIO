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

RuinedPortalStructure.findGenerationPoint, in draw order:

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

Remaining to read before coding the piece: RuinedPortalPiece.postProcess
(mossiness block swaps, air-pocket carving bounds, lava under portal blocks,
vine/overgrown decoration, cold cracked-cobble substitution), and
StructureTemplate's bounding-box/palette transform math (rotation + front_back
mirror around the pivot). The 13 ruined_portal templates are extracted under
internal/worldgen/data/structure_template/ruined_portal/.

## Fixture markers worth remembering

Seed 12345, committed fixture chunks (0,0) (1,0) (0,1) (-1,-1):

- (1,0): ruined portal — obsidian (17,13,3), crying obsidian (21,13,3),
  gold block (19,18,3), chest (17,13,2).
- (0,0)/(1,0): mineshaft planks/fences near y=-41..-42.
- (-1,-1): unexplained cobblestone+mossy floor at y=35 under gravel/water with
  two chests nearby — attributed to no replayed feature yet. Candidate leads:
  a dungeon whose placement stream we mismatch, or an ocean ruin whose pieces
  reach across the chunk border.
