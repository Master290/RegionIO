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
a throwaway Java program run against the jar. `tools/VanillaLightDump.java` is the precedent — it
walks the block-state registry and emits opacity, emission and voxel face shapes into
`internal/world/light_properties.bin`. Substring-matching block names is how the light table was
wrong before (`grass_block` matched "grass", `bedrock` matched "bed"); don't reintroduce that shape
of guess anywhere.

## Layout

```
cmd/regionio/     entry point (flags, listener, graceful shutdown)
cmd/gendump/      client-free generator diagnostics — biome spread, surface blocks,
                  subsurface banding, deep-layer composition, bedrock band, fluid census,
                  cross-section
cmd/genblocks/    generates internal/worldgen/generated_blocks.go from the block report
cmd/genlight/     legacy light-table generator, superseded by tools/VanillaLightDump.java
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
(`worldgen/surface.go`), and the column pass in `world/vanilla.go` that mirrors `SurfaceSystem`.

The whole `noise_router` is parsed. `preliminary_surface_level` is reachable through
`od.PreliminarySurfaceLevelAt`, which quart-aligns and memoises across chunks the way `NoiseChunk`
does; the `vein_*` keys are parsed but nothing reads them yet.

Known gaps, roughly in order of how visible they are:

- **`above_preliminary_surface` is wrong**, so there is no subsurface banding: every land column is
  one grass block directly on stone, no dirt, no sandstone under sand. Vanilla is
  `blockY >= preliminarySurfaceLevel + surfaceDepth - 8` with the level bilinearly interpolated from
  the four corners of the 16-block cell; we compare against the actual top block, which gates the
  whole biome surface subtree to a single block per column. `gendump` prints this. The router value
  it needs is already available.
- **`surfaceDepth` is always 0.** Vanilla is `surfaceNoise*2.75 + 3 + rand*0.25`, so the dirt band is
  three-ish blocks deep; ours is one. This is the other half of the missing banding, and it also
  neuters every `add_surface_depth`/`surface_depth_multiplier` term in the rule tree.
- **Several surface-rule conditions are stubs**: `hole` is hardcoded false (vanilla is
  `surfaceDepth <= 0`), `steep` is never assigned, the `minecraft:surface` noise is a per-column
  random draw rather than the real noise, `surface_secondary` is not sampled at all (so
  `secondary_depth_range` is ignored), and 6 of the 7 `noise_threshold` noises are unsupported so
  calcite, ice, packed ice, powder snow, swamp water and gravel patches never appear. `bandlands` is
  a 4-colour cycle rather than the 192-band array.
- **`vertical_gradient` ignores absolute anchors**, reading only `above_bottom`. The bedrock floor
  works because its anchors are `above_bottom`; the deepslate rule's are `absolute` 0..8, so both
  collapse to y=-64 and **no deepslate is ever placed** — `gendump`'s deep-layer line reads
  `deepslate=0` everywhere.
- **No carvers and no ore veins.** Caves come only from the density router; `configured_carver` is
  not extracted and the `OreVeinifier` over the parsed `vein_*` keys is not written.
- **Decoration is hand-written heuristics**, not the vanilla feature system: oak trees only and
  without a biome check (so oaks grow in deserts), ores that cannot generate below y≈0 because they
  only replace stone and never deepslate, and no grass, flowers, lakes or springs.

## Testing worldgen

`make verify` is the gate, but most generator defects are invisible to it — they show up as terrain
that looks wrong. `cmd/gendump` exists for that: biome distribution, top surface blocks, subsurface
banding, deep-layer composition, the bedrock band, the underground fluid census, and an ASCII
cross-section, with no client involved. Add an assertion to it whenever you fix a class of defect;
the bedrock-band check is the model — it prints per-layer counts and fails loudly on any air or
water in the floor. The fluid census is the same shape: it prints water as a share of the open
volume under inland chunks (3.8% now, 100% before the aquifer) and fails if caves flood again.

Anything gendump can assert on, prefer to also assert in a test — `TestCavesAreDry` and
`TestNoFluidUnderBedrock` in `internal/world` are gendump checks that run under `make verify`.

`go test -race` needs cgo and a C toolchain; on a Windows box without gcc, `make test-race` cannot
run at all.

`internal/world/vanilla_parity_test.go` compares surface heights against a capture from the official
server and skips when the capture is absent. Note it reads a hardcoded `/tmp` path, so on Windows it
never runs. A capture is produced by running the vanilla server headless at a known seed and reading
its region files back with our own `regionfile.go` + `nbt`.
