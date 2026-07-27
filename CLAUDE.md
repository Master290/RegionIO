# RegionIO — working notes

A Minecraft Java Edition server core in Go, targeting **26.1.2 / protocol 775**.
The goal is vanilla fidelity, not a lookalike: where vanilla behaviour is known, match it exactly.

README.md describes what the server does. This file is about how to work on it.

## Commands

```
go build ./... && go vet ./...
make test          # go test ./...
make test-race     # the race-sensitive subset
make verify        # both

go run ./cmd/regionio -seed 12345            # serves on 0.0.0.0:25565
go run ./cmd/regionio -seed 12345 -world ""  # in-memory world, nothing read from or written to disk
go run ./cmd/gendump                         # client-free generator diagnostics
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
does; the `vein_*` keys are parsed but nothing reads them yet.

The rule tree is **seed-bound**: `od.SurfaceRule()` returns a `*SurfaceRuleSet` compiled against the
world's `RandomState`, because `noise_threshold` and `vertical_gradient` cannot work without it. Get
a context from `NewContext`, call `BeginColumn` per column, then `Apply` per block.

Two things a surface rule cannot do silently: name a block that is not in `worldgen/blockids.go`
(that is a parse error now — it used to resolve to 0 and get dropped, which is how deepslate went
missing from the whole world), and be added without the world's height bounds (anchors resolve at
parse time).

Known gaps, roughly in order of how visible they are:

- **No carvers and no ore veins.** Caves come only from the density router; `configured_carver` is
  not extracted and the `OreVeinifier` over the parsed `vein_*` keys is not written.
- **No `PerlinSimplexNoise`**, so two corners of `Biome.coldEnoughToSnow` are missing: the height
  adjustment that cools a column above sea level + 17, and the `frozen` temperature modifier that
  warms patches of frozen ocean. Base temperatures are real (`worldgen/biome_temperature.go`,
  extracted from the jar's 65 biome JSONs). The overworld tree reaches `minecraft:temperature` from
  exactly one rule — whether a hole in a frozen ocean floor ices over — so neither omission is
  visible; snowy peaks come from biome selection, not from this condition.
- **`erodedBadlandsExtension` and `frozenOceanExtension` are not ported.** `SurfaceSystem` runs both
  outside the rule tree, for eroded badlands spires and frozen-ocean icebergs.
- **Decoration is hand-written heuristics**, not the vanilla feature system: oak trees only and
  without a biome check (so oaks grow in deserts), ores that cannot generate below y≈0 because they
  only replace stone and never deepslate, and no grass, flowers, lakes or springs.

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

`internal/world/vanilla_parity_test.go` compares surface heights against a capture from the official
server and skips when the capture is absent. Note it reads a hardcoded `/tmp` path, so on Windows it
never runs. A capture is produced by running the vanilla server headless at a known seed and reading
its region files back with our own `regionfile.go` + `nbt`.
