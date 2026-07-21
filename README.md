# RegionIO

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
  `level_chunk_with_light` frames; bounded LRU cache; Anvil `.mca` persistence
  with autosave and seed metadata.
- **Generation**: vanilla-derived overworld terrain from the embedded datapack
  (`ImprovedNoise`/`PerlinNoise`/`BlendedNoise`/`NormalNoise` + the density
  function interpreter), 3D multi-noise biomes, surface-rule interpretation,
  deterministic decoration, and basic template structures.
- **Gameplay**: four-player session registry; player join/leave and movement
  synchronization; chunk-scoped visibility for players and mobs; shared
  creative block place/break; broadcast chat; and hotbar item→block mapping.
- **Lighting**: stored vanilla nibble arrays for sky and block light; horizontal
  and cross-chunk propagation; incremental updates after edits; persisted
  `SkyLight`/`BlockLight`; and chunk-scoped `light_update` broadcasts.
- **Safety**: duplicate chunk generation is coalesced; corrupt stored chunks are
  not silently regenerated or overwritten; a world cannot reopen with another
  seed.

## Build & run

```
go build ./...
go run ./cmd/regionio -seed 12345
```

The world seed defaults to `0`; override it with the `-seed` flag or the
`REGIONIO_SEED` environment variable. The server listens on `0.0.0.0:25565`.
Changing the seed for an existing world directory is rejected.

## Testing

```
go test ./...
go test -race ./internal/network ./internal/server ./internal/world \
  -run 'Test(Integration|BoundaryEdit|PlayerInfo|PlayerRegistry|Concurrent|Incremental|EncodeLight|Cache|Store|Eviction|Region)'
# or run both gates:
make verify
```

The integration suite exercises four clients across two visibility regions:
join, movement, leaving, mob visibility, and local block/light updates. A
two-client scenario separately covers shared block edits and chat. Concurrency
tests cover simultaneous frame encoding, editing, autosave, cache misses, and
session movement/broadcasts. Lighting tests compare the initial flat chunk and a
31x31x31 glowstone propagation volume against fixtures captured from the
official vanilla 26.1.2 server. Optional terrain parity diagnostics compare
surface heights against `/tmp/vanilla_ground.json` when that capture is present.

## v0.3 scope

RegionIO v0.3 is a small creative multiplayer server core, not a complete
vanilla gameplay implementation. Player and mob visibility is chunk-scoped,
but there is no interest prioritization or delta-movement compression yet.
Lighting matches vanilla's block-state dampening, emission, and face-occlusion
properties and propagates across loaded chunk boundaries. Chunks outside the
live cache are recalculated exactly when loaded rather than retained as active
light-engine state. Structures, placed features, mob AI, authentication,
inventory, and survival mechanics remain intentionally partial. The density
router is vanilla-derived, while biome/surface/decoration layers still contain
approximations and require stricter parity fixtures.

## Project layout

```
cmd/regionio/      entry point (config, listener, graceful shutdown)
internal/
  protocol/        wire primitives: VarInt, framing, compression, packet IDs
  nbt/             NBT encoder/decoder (with modified UTF-8)
  registry/        embedded synchronized registries + tags
  world/           chunk model, level_chunk encoder, cache, generators, biomes
  worldgen/        noise core + density-function interpreter + climate finder
  network/         per-connection state machine (handler/conn/play/login/...)
  server/          shared core: config, status response, profiles
```

## Notes

The vanilla `server.jar` and its unpacked `libraries/`/`versions/` are **not**
included (obtain them from Mojang). The embedded data under `internal/`
(registries, biome parameters, the overworld datapack) is derived from vanilla
reports and is all that is required to build and run.
