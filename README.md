# RegionIO

A Minecraft Java Edition server core written in Go, targeting version
**26.1.2** (protocol **775**). RegionIO implements the connection lifecycle
(status → login → configuration → play), chunk streaming, block editing, and a
vanilla-faithful overworld generator built on the real `noise_router`
`final_density` tree.

## Status

- **Network**: full handshake/status/login (offline mode)/configuration/play
  state machine with zlib compression, keep-alive, and chunk streaming.
- **Registries**: 28 synchronized registries + tags, captured verbatim from the
  26.1.2 vanilla server and sent during configuration.
- **World**: in-memory chunk cache with memoized, compression-ready
  `level_chunk_with_light` frames; paletted block containers; heightmaps.
- **Generation**: bit-faithful overworld terrain from the embedded datapack
  (`ImprovedNoise`/`PerlinNoise`/`BlendedNoise`/`NormalNoise` + the density
  function interpreter), plus multi-noise **biomes** (per-chunk, surface layer)
  via the vanilla `Climate` finder over the official biome parameter table.
- **Gameplay**: creative block place/break, hotbar item→block mapping, chat,
  teleport ack, and a randomized bedrock floor / beach & gravel surface pass.

## Build & run

```
go build ./...
go run ./cmd/regionio -seed 12345
```

The world seed defaults to `0`; override it with the `-seed` flag or the
`REGIONIO_SEED` environment variable. The server listens on `0.0.0.0:25565`.

## Testing

```
go test ./...
```

Parity tests (`internal/world/vanilla_parity_test.go`) compare generated surface
heights against captures from the official server and skip when no capture is
present.

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
