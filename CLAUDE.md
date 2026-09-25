# RegionIO — working notes

A Minecraft Java Edition server core in Go, targeting **26.1.2 / protocol 775**.
The goal is vanilla fidelity, not a lookalike: where vanilla behaviour is known, match it exactly.

README.md describes what the server does. This file is about how to work on it.

## Commands

```
go build ./... && go vet ./...
make test          # go test ./...
make test-race     # go test -race ./...
make verify        # build, vet, tests, race tests, and the gated diagnostics
make parity        # requires the committed vanilla block fixture

go run ./cmd/regionio -seed 12345            # serves on 0.0.0.0:25565
go run ./cmd/regionio -seed 12345 -world ""  # in-memory world, nothing read from or written to disk
go run ./cmd/gendump                         # client-free generator diagnostics
go run ./cmd/genfeatures                     # dumps the embedded feature/biome graph for inspection
go run ./cmd/vanillacapture                  # regenerate vanilla block parity fixture (Java 25)
```

## Hard rules

**No third-party dependencies.** `go.mod` has none. NBT, zlib framing, MD5 seeding, noise, the
density-function interpreter — all in-tree. Keep it that way.

**Bump `generatorVersion` whenever the generator's output changes.** It lives in
`internal/world/store.go` and is stamped into every saved chunk; a mismatch makes the chunk
regenerate. Without a bump, the chunks already on disk keep their old terrain and your change looks
like it did nothing in exactly the area you are standing in — `chunkAt` prefers the store over the
generator. `TestGeneratorVersionStampRejectsStaleChunks` covers the mechanism. For quick iteration,
`-world ""` sidesteps persistence entirely. (The seed is guarded separately, by the world metadata
file, and a seed mismatch is a hard error rather than a regeneration.)

**Verify vanilla behaviour against the jar; don't recall it.** See below.

## Vanilla ground truth

None of these are redistributable, so all are gitignored. Obtain `server.jar` from Mojang.

| Source | What it gives you |
|---|---|
| `.refjava/` | Decompiled classes for the parts we port: `Climate`, `SurfaceRules`, `SurfaceSystem`, `Aquifer`, `DensityFunctions`, `NoiseRouterData`, `NoiseBasedChunkGenerator`, `TerrainProvider`, `PalettedContainer`, `LevelChunkSection`, `ClientboundLevelChunkPacketData` |
| `versions/26.1.2/server-26.1.2.jar` | The real (deobfuscated) server. The outer `server.jar` is only a bundler |
| `generated/reports/` | Datagen output: `blocks.json`, `registries.json`, `packets.json`, `biome_parameters/` |

The inner jar also carries the **complete worldgen datapack**, which is the source for everything we
still approximate: 259 `placed_feature`, 222 `configured_feature`, 66 `biome` (with per-stage
`features` and `carvers`), 5 `configured_carver`, 35 `structure`, 1359 structure NBTs — 648 KB for
the feature/biome/carver set.

Three ways in, cheapest first:

```
unzip -p versions/26.1.2/server-26.1.2.jar data/minecraft/worldgen/biome/plains.json
javap -p -c -classpath versions/26.1.2/server-26.1.2.jar net.minecraft.world.level.chunk.Strategy
```

`javap` settles questions the decompiled subset does not cover. It is how the biome palette
threshold was pinned down: `Strategy$2` switches `{0..3}` and everything above falls through to the
global palette, which `.refjava/` alone could not show.

Block-state and tag counts come from the tables, never from reading a property name or
remembering a block's shape: `idsByName`, `stateByID` and `flattenBlockTag` already hold every
answer and a two-line test prints it. Claims in that shape kept being wrong — "cave_vines has 25
states" (52: `age` 0..25 times `berries`); "powder_snow and gravel are the multi-state spring
blocks" (one state each, and the reachable one is deepslate, with three); "`chest` has seven
facings" (four, and 24 states once `type` and `waterlogged` are counted);
"`lava_pool_stone_cannot_replace` is stone and deepslate" (62 members, eleven species of log and
leaf, three single-state blocks). Guesses fail quietly in both directions. A wrong count makes the
test that relies on it SKIP, which reads as passing. And a probe that resolves nothing reads as a
refutation of a correct note: asking for `moss_replaceable` instead of `minecraft:moss_replaceable`
returns the empty set, so "0 states" looked like the notes being wrong when they were right. Quote
whole-tag and capture-restricted counts separately — `moss_replaceable` differs from a
default-state reading on 59 states in principle and 8 in this fixture, and only the second is a
number a fixture can fail on — so `TestTagStateIDsAreBlockScoped` pins both.

The prose rule has a test behind it now: `TestDocumentationNamesExist` fails if a document cites a
test name that no `_test.go` defines (the placeholder in this very sentence was caught by it on the
first run), or if a mojibake signature appears, and it asserts how many files and citations it read
so that a glob which stops matching fails loudly instead of guarding nothing.

When a constant has to come from vanilla's *runtime* rather than its source or reports, dump it with
a Java program run against the jar. `tools/VanillaBlockStateDump.java` is the one that exists — it
walks the block-state registry and emits, per state, light opacity and emission, voxel face-occlusion
masks, and the `blocksMotion` / fluid / leaves flags the heightmaps need, into
`internal/world/block_properties.bin`. Rebuild it with:

```
CP="versions/26.1.2/server-26.1.2.jar;$(find libraries -name '*.jar' | tr '\n' ';')"
javac -nowarn -cp "$CP" -d <out> tools/VanillaBlockStateDump.java
java -cp "<out>;$CP" VanillaBlockStateDump > internal/world/block_properties.bin
```

The 39 jars under `libraries/` are required; the server jar alone will not boot the registry. Bump
the format version in both the Java and `internal/world/block_properties.go` whenever the layout or
a flag's meaning changes.

Six heightmaps, two lifetimes: which ones a write touches is chosen by the chunk's *status*, not by
the writer. `ChunkStatus` gives every step a `heightmapsAfter` set — `WORLDGEN_HEIGHTMAPS` =
{`OCEAN_FLOOR_WG`, `WORLD_SURFACE_WG`} for everything up to `SURFACE`, `FINAL_HEIGHTMAPS` =
{`OCEAN_FLOOR`, `WORLD_SURFACE`, `MOTION_BLOCKING`, `MOTION_BLOCKING_NO_LEAVES`} for `CARVERS` and
`FEATURES` — and `ProtoChunk.setBlockState` updates exactly that set. So during decoration the two
`*_WG` columns are frozen at the end of the carver step and no feature can raise them, which matters
because `OreFeature.place` gates every vein on `OCEAN_FLOOR_WG` per column over the vein's own box.
`decorationRegion` snapshots both maps when it is built and answers those two names from the
snapshot; a reader that scans the live region for a `*_WG` name re-introduces the bug that cost 49
cells of phantom dirt and, on land, most of what looked like an ordering preference.

The same trick verifies output, not just constants. `tools/VanillaChunkFormatCheck.java` opens a
region file we wrote with vanilla's own `RegionFile`, `NbtIo`, `Strategy` and `SimpleBitStorage` and
fails if the root is not flat, a section `Y` is not a byte, or a palette array is not the width
vanilla derives from its palette size. Note the server jar is *signed*, so a helper cannot be
declared inside a `net.minecraft.*` package — reach protected members by reflection instead.

Substring-matching block names is how the light table was wrong before (`grass_block` matched
"grass", `bedrock` matched "bed"); don't reintroduce that shape of guess anywhere.

## Layout

```
cmd/regionio/     entry point (flags, listener, graceful shutdown)
cmd/gendump/      client-free generator diagnostics — biome spread, surface blocks,
                  subsurface banding, deep-layer composition, bedrock band, fluid census,
                  cross-section
cmd/genblocks/    generates internal/worldgen/generated_blocks.go from the block report
cmd/genfeatures/  dumps the embedded placed/configured feature + biome graph
cmd/genlight/     legacy light-table generator, superseded by tools/VanillaBlockStateDump.java
tools/            Java dumpers run against the jar, plus their Go-side fixtures
internal/protocol/  VarInt, framing, compression, packet IDs
internal/nbt/       NBT codec (modified UTF-8)
internal/registry/  28 embedded synced registries + tags, verbatim from vanilla
internal/world/     chunk model, wire encoder, cache + tickets, Anvil store, lighting,
                    the chunk generator itself (vanilla.go), decoration
internal/worldgen/  the library: noise, density-function interpreter, surface rules,
                    aquifer, climate/biome finder, embedded datapack under data/
internal/network/   per-connection state machine
internal/server/    shared core: config, sessions, status, profiles, entity loops
```

Note the split: `internal/worldgen` is a *library* over the datapack; the chunk generator that drives
it is `internal/world/vanilla.go`.

## Fidelity status

Bit-exact and parity-tested — treat as settled, change only with a vanilla reference in hand:
`random.go` (Xoroshiro128++, `upgradeSeedTo128bit`, MD5 seeding), `improved_noise.go`,
`perlin_noise.go`, `normal_noise.go`, `blended.go`, `spline.go`, `density.go`, the 4×8×4 cell grid
with trilinear interpolation, the climate/biome finder, and the chunk wire encoder
(`TestGoldenAgainstVanilla` compares bytes against a real vanilla chunk).

Ported from the decompiled source and checked by behaviour rather than by bytes — faithful as far as
we can tell, but no vanilla capture confirms them: the aquifer (`worldgen/aquifer.go`, the whole of
`Aquifer.NoiseBasedAquifer` bar `shouldScheduleFluidUpdate`), the surface-rule interpreter
(`worldgen/surface.go`, every condition the overworld tree uses), the badlands clay bands
(`worldgen/bandlands.go`), and the column pass in `world/vanilla.go` that mirrors `SurfaceSystem`.

The whole `noise_router` is parsed. `preliminary_surface_level` is reachable through
`od.PreliminarySurfaceLevelAt`, which quart-aligns and memoises across chunks the way `NoiseChunk`
does. The `vein_*` keys drive `OreVeinifier` during the material pass.

The rule tree is **seed-bound**: `od.SurfaceRule()` returns a `*SurfaceRuleSet` compiled against the
world's `RandomState`, because `noise_threshold` and `vertical_gradient` cannot work without it. Get
a context from `NewContext`, call `BeginColumn` per column, then `Apply` per block.

Two things a surface rule cannot do silently: name a block that is not in `worldgen/blockids.go`
(that is a parse error now — it used to resolve to 0 and get dropped, which is how deepslate went
missing from the whole world), and be added without the world's height bounds (anchors resolve at
parse time).

Known gaps, roughly in order of how visible they are:

- **Feature replay covers most of the decoration steps, not every configured type.** `worldgen/features.go` parses
  the placed/configured feature graph (placement modifiers, anchors, biome filters) and the
  production region replay runs it for stage-1 lava lakes (`world/lakes.go`), stage-2 geodes and
  boulders (`world/geodes.go`, `world/block_blob.go`), stage-3 monster rooms (`world/monster_rooms.go`),
  the whole stage-6 schedule (ores with real deepslate targets, underwater magma between copper and the
  disks, disk features), stage-8 springs (`world/springs.go`), and stage-9 vegetation — lush-cave
  patches plus trees, flowers, grass and mushrooms (`world/vegetation_patches.go`,
  `world/tree_features.go`) — see `world/feature_scheduler.go` and `world/region_ores.go`. Of the
  structure sets, ruined portals (`world/ruined_portal.go`), ocean ruins (`world/ocean_ruin.go`,
  with the post-placement falling-gravel/bubble-column/water-refill physics vanilla's block ticks
  add), and mineshafts (`world/mineshafts.go`, verified against the saved vanilla start NBT
  piece-for-piece and against a structures-only capture cell-for-cell) replay from their datapack
  configurations. 26.1.2 has no water-lake configured feature left, so `LakeFeature`'s freeze pass
  never fires. What is still hand-written is the ore scatter of the
  **legacy per-chunk generator** (`placeVanillaOres` in `world/placed_features.go`, reached from
  `decorateGeneratedNonOre` at `world/region_generator.go:173`); after this branch deleted its
  trees, springs, flora, desert features and rocks it places no surface decoration at all, which
  is fine because that generator is a comparison fallback
  (`REGIONIO_PARITY_GENERATOR=legacy`) and not a second implementation.
- **Trees are vanilla's own algorithm**: four trunk placers (straight, giant, fancy, dark oak), six
  foliage placers (blob, pine, spruce, mega pine, fancy, dark oak) and the beehive and alter-ground
  decorators, each transcribed from `javap -p -c` output and pinned by a silhouette golden in
  `tree_placers_test.go` whose expected rows are derived from the placer arithmetic, not pasted from
  a run. An un-modelled placer or decorator is counted by name and skipped
  rather than aborting a region, because the replay walks 5×5 sources and one border forest
  must not lose a taiga chunk; `TestVanillaLandBlockParity` prints the tally. Canopies do cross
  chunk borders — vanilla has no border test, only `ChunkStep.blockStateWriteRadius`, which is
  1 chunk for FEATURES and is what `decorationRegion.setBlock` enforces.
- **`doPlace`'s three height guards were inverted, and are measured fixes.** A truncated trunk is
  refused unless `minimum_size` names a `min_clipped_height` (an absent `OptionalInt` means refuse,
  and 33 of the 39 configs name none); a vine stops a trunk whose `ignore_vines` is false (it has to,
  because `replaceable_by_trees` lists vine, so the old `isFree` short-circuit made the clause
  unreachable); and the free-height probe runs to `height + 1`, which is how a blocked crown space
  shortens the trunk rather than only the canopy. Fixing them cost 106 tree cells (577 to 471 before
  dark oaks restored them) and removed 198 surface-band mismatches — the honest direction, since
  the deleted cells are trees vanilla never plants.
- **No `PerlinSimplexNoise`**, so two corners of `Biome.coldEnoughToSnow` are missing: the height
  adjustment that cools a column above sea level + 17, and the `frozen` temperature modifier that
  warms patches of frozen ocean. Base temperatures are real (`worldgen/biome_temperature.go`,
  extracted from the jar's 65 biome JSONs). The overworld tree reaches `minecraft:temperature` from
  exactly one rule — whether a hole in a frozen ocean floor ices over — so neither omission is
  visible; snowy peaks come from biome selection, not from this condition.
- **`erodedBadlandsExtension` and `frozenOceanExtension` are not ported.** `SurfaceSystem` runs both
  outside the rule tree, for eroded badlands spires and frozen-ocean icebergs.

Parity baseline (fixture seed 12345, measured on `generatorVersion` 40): biomes and heightmaps exact
everywhere; blocks 95.960% through the legacy single-chunk path (`REGIONIO_PARITY_GENERATOR=legacy`)
and **99.916% through the production region replay** — 330 residual cells, dominated by the
lush-caves moss and clay pools and their nested vegetation. A featureless vanilla capture
(`cmd/vanillacapture -featureless -blocks-only`,
biomes stripped to their carvers) proves the undecorated pipeline bit-exact against it — density,
surface rules, carvers, aquifers, and noise-router veins match every one of the fixture's cells — so
the residual block gap is entirely inside feature replay.
That fixture cannot see a surface, so land has its own (`testdata/vanilla_land_12345.bin`, four land
chunks): **99.410%**, 2,321 residual cells, of which 687 are the surface band above sea level and 51
the waterline band, with 607 of vanilla's 789 tree cells placed. Both bands are ratcheted in
`world/vanilla_land_parity_test.go`, because the ocean floor of 99.7% leaves ~849 cells of headroom
and an entirely wrong forest fits inside it.

The underground stages are each pinned by their own capture, which is why they can be trusted while
the surface ones cannot: monster rooms (`world/monster_rooms.go`) replay between geodes and the ores
with vanilla's draw order; ocean ruins (`world/ocean_ruin.go`) replay cell-for-cell against a
dedicated `-no-features` capture of their start — integrity rolls, the capped suspicious-gravel
conversion, chest/drowned markers, and the post-placement physics (falling gravel, bubble columns,
source-water refill); mineshafts (`world/mineshafts.go`) replay piece-for-piece against the saved
vanilla start NBT and cell-for-cell against a structures-only capture, which is what closed the
fixture's (-1,-1) pocket — a monster room whose wall opening a mineshaft corridor carved.

The residual is now one family, not a tail: `moss_block`↔`deepslate` is 82 cells (59 where vanilla
has moss and we have deepslate, 23 the other way), and lush-cave clay is 22. Everything else in the
330 is a shuffle among ground cover, cave vines, kelp and dripleaves, and those counts are printed
rather than quoted here: run the parity test under `REGIONIO_PARITY_DIAGNOSTIC=1` and it lists the
net per state and the split by y band. Numbers written into a document go stale the moment the
fixture is re-measured, which has already happened to this sentence three times.

The one reading that was held and is now known wrong: a net of 35 fewer moss cells was taken as an
extent or radius difference in `moss_patch`. It is not. Per-target nets inside that family point in
both directions (+15, -8, -13, -29 across the four chunks), so the patch covers what vanilla covers
and the disagreement is which cells within it, which is a position-stream question like the rest.

The lush-cave clay pools used to be the headline defect — one waterlogged pool landing fifteen blocks
off in chunk (-1,-1), with moss and vine positions cascading from it. That is closed: `2c29024`
aligned the decoration source ordering with vanilla's `rangeClosed` stream, and the differential
capture below now shows clay at 22 cells against 268-extra/49-missing before. Two structural facts
came out of the chase and are worth keeping, because they rule whole hypothesis classes out cheaply:

- `SetFeatureSeed` (`worldgen/random.go:367`) fully reseeds from `decorationSeed + featureIndex +
  10000*stage`, so an earlier feature consuming a different number of draws **cannot** shift a later
  feature's stream. Cross-feature draw drift is impossible in this architecture; only a wrong index,
  a wrong seed, or a changed world state can move a feature.
- `FeatureSchedule`'s `Index` is a position in the global topological step list over the union of the
  3×3 neighbourhood's biomes, not an offset in the decorating biome's own list — `lush_caves_clay` is
  5th in `lush_caves` and reseeds as 29. `feature_schedule_golden_test.go` pins both numbers, so a
  reordering of the biome parameter table now fails loudly instead of silently moving every feature.

`decorationSources` (`world/feature_scheduler.go:16-43`) remains a set of special cases: only (0,0)
and (1,0) get vanilla's Z-major/X-minor order, and every other target — including the fixture's
(-1,-1) — falls through to a target-first, then X-major, default that nothing derived it. Do not
assume a chunk that is not (0,0) or (1,0) has a modelled neighbour order; the next fix of this class
should derive the order once against a multi-target capture rather than add a third `if`.

The opt-in H4 path (`NewVanillaRegionH4Generator` / `NewVanillaRegionH4BatchGenerator`) is the
first implementation of the alternative: one canonical 3×3 tile, one shared 7×7 region, and
25 source origins replayed once instead of nine independent per-target histories. It is measured
but not production: X-major gives 392,248/393,216 ocean and 391,120/393,216 land, while the
committed hybrid gives 392,886 and 390,895; the H4 clay differential is 228/36/211 against
96/22/9. `server.New` still uses the hybrid, and no ratchet was loosened. The canonical cache
entry point and ring-closure test are kept so a later switch is a wiring decision, not a new
architecture hidden in a parity edit. The hand-written `worldgen.PlaceStructures` village pass
is no longer called by the production region replay; only the explicitly legacy per-chunk
fallback retains it. Because that removal changes generated terrain, `generatorVersion` is 41:
every chunk stamped 40 or earlier regenerates rather than keeping the hand-placed village.

The canonical cache treats a stored chunk as read-only, never as a replay input. A request for a
tile probes the store for its own target first and reconciles the remaining members afterwards, so
a persisted neighbour keeps its saved blocks instead of being overwritten by a regenerated one.
The stamp is what makes this safe across generator changes: `LoadChunk` rejects anything whose
`generatorVersion` differs, so "read-only" applies only to chunks the current generator could have
written. Both directions are asserted —
`TestCanonicalBatchGeneratorPrefersPersistedNeighbors` and
`TestCanonicalBatchGeneratorRegeneratesStalePersistedChunks`.

## Testing worldgen

`make verify` is the gate, but most generator defects are invisible to it — they show up as terrain
that looks wrong. `cmd/gendump` exists for that: biome distribution, top surface blocks, subsurface
banding, deep-layer composition, the bedrock band, the underground fluid census, the badlands clay
bands, and an ASCII cross-section, with no client involved. Add an assertion to it whenever you fix
a class of defect; the bedrock-band check is the model — it prints per-layer counts and fails loudly
on any air or water in the floor. The fluid census is the same shape: it prints water as a share of
the open volume under inland chunks (3.8% now, 100% before the aquifer) and fails if caves flood
again.

Anything gendump can assert on, prefer to also assert in a test. `internal/world` carries four that
started as gendump checks and run under `make verify`: `TestCavesAreDry`, `TestNoFluidUnderBedrock`,
`TestGrassColumnsHaveDirt`, `TestDeepslateLayer`.

`go test -race` needs cgo and a C toolchain; on a Windows box without gcc, `make test-race` cannot
run at all.

`cmd/vanillacapture` runs the official bundler jar in an isolated temporary world, force-loads fixed
chunks, reads their region files, and writes `internal/world/testdata/vanilla_overworld_12345.bin`.
Pass `-server` the *bundler* jar — the one whose `manifest.json` points at a sibling `libraries/`
tree. The extracted `versions/26.1.2/server-26.1.2.jar` that every `javap` command in these notes
uses is not it: booting that alone dies at `NoClassDefFoundError: joptsimple/ValueConverter` before
the server starts and writes nothing. Check a capture by its output file, not its exit status —
piping through `tail` reports the pipeline's status, and twice here a failed capture read as a
success because of it.
The fixture contains every block state, 4x4x4 biome cell, and three heightmaps. Java 25 is required. `make parity`
requires the fixture and fails when it is absent; ordinary `go test ./...` skips that one test so a
fresh checkout remains buildable without Mojang's non-redistributable jar. Once the fixture is
committed, ordinary CI guards the measured baseline while `make parity` requires exact equality.
The older optional
`/tmp/vanilla_ground.json` height report remains diagnostic only.

`-disable-placed <feature>` captures one feature's exact ground truth. It rewrites that placed
feature's `placement` array to prepend `{"type":"minecraft:count","count":0}` inside a throwaway
datapack, which zeroes it while leaving every biome's feature list intact — so no other feature's
`FeatureSorter` index or decoration stream shifts, which is precisely what `-featureless` and
`-no-features` do. Subtracting that capture from the plain one isolates the chosen chain;
`testdata/vanilla_no_lush_clay_12345.bin` differs from the plain fixture in 3,670 bytes, all of them
block state, with zero biome and zero heightmap drift — the check that the override was clean.
`TestVanillaLushClayDiff` is built on that pair and runs always, with a ratchet on the
measured counts. Subtract only against
cells the plain capture reports as the feature's own output: the differential is the whole chain's
effect, including what downstream stages did differently because those cells had changed.

`make diagnostics` compiles every test binary and then runs the gated probes with their variables set.
The compile half matters more than it looks: `go build ./...` never touches `_test.go` files, and
debug instrumentation left in `internal/world` has twice broken the package's build in a way the
plain build could not see. GitHub Actions mirrors the target as its own `diagnostics` job, because
`make` is not on the runner - so a probe added to one list needs adding to the other.

Every gate the source reads is set by some runner, or excused in writing in
`excusedGates` inside `TestEveryDiagnosticGateIsReachable`, which compares the names
in the Go source against the names the Makefile and the workflow assign values to and
fails in both directions — a gate nothing sets, and a variable nothing reads. It was
written after an audit found two ways that had already happened: the consolidated
trace flag is `REGIONIO_LUSH_CLAY_TRACE` while both runners set only
`REGIONIO_CLAY_TRACE`, which gates the test layer instead, so the in-generator traces
ran in no CI job; and six ore diagnostics plus the trapezoid one had no runner at all.
`REGIONIO_LUSH_CLAY_PROBE_INDEX` is set by `make clay-index-sweep` rather than by CI —
the sweep replays the schedule 41 times for about a minute — and its two decisive
points are asserted on every run by `TestLushClayPositionsAreRecorded` instead. The
rule worth keeping: a diagnostic whose finding matters should end up asserted, not
merely printed - a `t.Logf` no one diffs silently survives the thing it was written
to detect.

One more convention, because a machine-local path did get committed once. A diagnostic must not
name one: write to `t.TempDir()`, or to a repo path that `.gitignore` already carries
(`internal/world/testdata/cave_box.txt` is the existing example, listed at `.gitignore:41`), or
best of all log instead of writing. The failure mode is not litter but silence - a dump guarded by
`if err == nil` to a personal scratch directory simply stops doing anything on another machine,
and reads as if it ran.

Beyond the always-on tests, `internal/world` carries env-gated diagnostics for hunting the residual
parity gap (all skip unless the variable is set): `REGIONIO_REGION_ORE_DIAGNOSTIC=1` compares the
legacy / center-only / region-replay ore paths per state, and
`REGIONIO_BASE_TERRAIN_DIAGNOSTIC=1` classifies fixture mismatches by what could have written each
side. `TestVanillaBaseTerrainParity` compares against `testdata/vanilla_base_12345.bin`, a capture
of vanilla running with features and structure sets stripped out; regenerate it after any change to
the derived-datapack logic:

```
go run ./cmd/vanillacapture -server server.jar -featureless -blocks-only \
    -port 25585 -output internal/world/testdata/vanilla_base_12345.bin
```
