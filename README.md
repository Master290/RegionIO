# <p><img src="https://remo.su/regionio-full.svg" height="60"></p>

A Minecraft Java Edition server core written in Go, targeting version
**26.1.2** (protocol **775**). RegionIO implements the connection lifecycle
(status → login → configuration → play), multiplayer chunk streaming, shared
block editing, persistent worlds, and an overworld generator built on the real
`noise_router` `final_density` tree.

## Status

- **Network**: full handshake/status/login (offline mode)/configuration/play
  state machine with zlib compression, keep-alive, and chunk streaming.
- **Registries**: 28 synchronized registries + tags, captured verbatim from the
  26.1.2 vanilla server and sent during configuration.
- **World**: revisioned, concurrency-safe chunk snapshots; memoized
  `level_chunk_with_light` frames; ticket-aware bounded LRU cache; shared frame
  admission limit; Anvil `.mca` persistence with autosave and seed metadata.
- **Generation**: vanilla-derived overworld terrain from the embedded datapack
  (`ImprovedNoise`/`PerlinNoise`/`BlendedNoise`/`NormalNoise` + the density
  function interpreter), 3D multi-noise biomes, surface-rule interpretation,
  configured carvers, aquifers, noise-router ore veins, deterministic
  decoration, and basic template structures. The complete vanilla feature and
  biome datapack graph is embedded; feature ordering, placement modifiers,
  vertical anchors/providers, biome filters, and mutable region writes are
  implemented. Production region replay now covers cross-chunk ores, disks,
  underwater magma, stage-2 amethyst geodes, and lush-cave moss ground patches
  directly from their datapack configurations.
- **Gameplay**: four-player session registry; player join/leave and movement
  synchronization; chunk-scoped visibility for players and mobs; shared
  creative block place/break; broadcast chat; and hotbar item→block mapping.
- **Lighting**: stored vanilla nibble arrays for sky and block light; horizontal
  and cross-chunk propagation; incremental updates after edits; persisted
  `SkyLight`/`BlockLight`; load-time border reconciliation; and chunk-scoped
  `light_update` broadcasts.
- **Chunk lifecycle**: per-client view and prefetch tickets, strict near-first
  ring streaming, stale-recenter cutoff, explicit client unload packets, and
  eviction only after the final owner releases a chunk.
- **Safety**: duplicate chunk generation is coalesced; corrupt stored chunks are
  not silently regenerated or overwritten; a world cannot reopen with another
  seed.

## Build & run

```
go build ./...
go run ./cmd/regionio -seed 12345 -port 25565 -viewdistance 2
```

The world seed defaults to `0`; override it with the `-seed` flag or the
`REGIONIO_SEED` environment variable. The server listens on `0.0.0.0:25565`.
Changing the seed for an existing world directory is rejected. The server caps
the client-requested chunk radius at `2` by default because cold density-based
generation is expensive; raise it with `-viewdistance 3` after the surrounding
world has been generated and cached.

## Testing

```
go test ./...
go test -race -timeout 20m ./...
# or run build, vet, ordinary tests, and race tests:
make verify

# strict block/biome comparison; requires a fixture generated with Java 25:
go run ./cmd/vanillacapture -server server.jar
make parity
```

The integration suite exercises four clients across two visibility regions:
join, movement, leaving, mob visibility, and local block/light updates. A
two-client scenario separately covers shared block edits and chat. Concurrency
tests cover simultaneous frame encoding, editing, autosave, cache misses, and
session movement/broadcasts. A 16-client lifecycle test exercises overlapping
ticket ownership, bounded global frame work, packet output, and cleanup after
disconnect. Lighting tests compare the initial flat chunk and a 31x31x31
glowstone propagation volume against fixtures captured from the official
vanilla 26.1.2 server. The committed overworld fixture exhaustively compares
393,216 block states, 6,144 biome cells, and three heightmaps across four fixed
chunks. The canonical single-chunk generator currently matches 95.378% of
fixture blocks, while the production region replay path matches 98.028%; both
match all fixture biomes and heightmaps. CI guards the 91% regression floor
while `make parity` requires exact equality. GitHub Actions runs build/vet/tests,
fixture regression checks, and the full race suite on every push and pull
request. Env-gated diagnostics in `internal/world` break the remaining gap
down per subsystem (ore paths per state, base-terrain defects with sample
coordinates).

## v0.4 scope

RegionIO v0.4 is a small creative multiplayer server core, not a complete
vanilla gameplay implementation. Player and mob visibility is chunk-scoped,
but there is no interest prioritization or delta-movement compression yet.
Lighting matches vanilla's block-state dampening, emission, and face-occlusion
properties and reconciles persisted borders when chunks re-enter the live
cache. Streaming prioritizes Chebyshev rings and abandons unstarted stale work;
an already admitted frame calculation completes atomically rather than being
interrupted halfway. Unowned clean chunks remain as an LRU warm cache until
capacity pressure evicts them. Structures, placed features, mob AI,
authentication, inventory, and survival mechanics remain intentionally partial.
The density router, configured carvers, and noise-router ore veins are
vanilla-derived. Surface and biome selection are ported but still need broader
runtime captures. The production cache now uses atomic batch publication and
the datapack-driven region replay path, with cross-chunk writes isolated per
target. Underground decoration (ores with deepslate targets, underwater magma,
disks) runs from the vanilla stage-6 schedule; the largest remaining fidelity
gap is surface decoration — trees, flora, springs, and lakes are still
hand-written — plus a few hundred underground cells where our carver or aquifer
verdict differs from vanilla. The biome parameter
finder uses an exact spatial index and overlapping region requests share a
bounded immutable terrain cache, keeping cold 3x3 generation near one second
on the reference Ryzen 5 5600X development machine.

The production cache uses atomic batch publication: a miss builds and publishes
a complete decorated 3x3 neighborhood, while persisted neighbors still take
precedence. This closes the chunk-lifecycle integration boundary without
exposing undecorated chunks.

The remaining block-parity gap is entirely inside feature replay: a vanilla
capture with every feature and structure set stripped out matches our
undecorated pipeline on all 393,216 cells, so density, surface rules, carvers,
aquifers, and noise-router veins are already bit-exact. Monster rooms now
replay from their datapack configuration between the geodes and the ores, so
the air pockets ore ellipsoids roll discards against exist where the schedule
puts them; the committed fixture happens to contain no dungeons, leaving the
measured parity at 98.028%. What still does not replay is the stage-1 lake
schedule and the structure sets — ruined portal and mineshaft pieces are
visible in the fixture — and those are the next worldgen milestones on the way
to exact equality while keeping cold batch generation within an acceptable
latency budget.

## Project layout

```
cmd/regionio/      entry point (config, listener, graceful shutdown)
internal/
  protocol/        wire primitives: VarInt, framing, compression, packet IDs
  nbt/             NBT encoder/decoder (with modified UTF-8)
  registry/        embedded synchronized registries + tags
  world/           chunk model, level_chunk encoder, cache, generators, biomes
  worldgen/        noise/density core, climate finder, feature datapack runtime
  network/         per-connection state machine (handler/conn/play/login/...)
  server/          shared core: config, status response, profiles
```

## Notes

The vanilla `server.jar` and its unpacked `libraries/`/`versions/` are **not**
included (obtain them from Mojang). The embedded data under `internal/`
(registries, biome parameters, the overworld datapack) is derived from vanilla
reports and is all that is required to build and run.
