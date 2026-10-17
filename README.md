# <p><img src="https://remo.su/regionio-full.svg" height="60"></p>

A Minecraft Java Edition server core written in Go, targeting version
**26.1.2** (protocol **775**). RegionIO implements the connection lifecycle
(status → login → configuration → play), multiplayer chunk streaming, shared
block editing, persistent worlds, and an overworld generator built on the real
`noise_router` `final_density` tree.

## Status

- **Network**: full handshake/status/login (offline and online mode with RSA +
  AES/CFB8 encryption and Mojang session verification)/configuration/play state
  machine with zlib compression, keep-alive, and chunk streaming.
- **Registries**: 28 synchronized registries + tags, captured verbatim from the
  26.1.2 vanilla server and sent during configuration.
- **World**: revisioned, concurrency-safe chunk snapshots; memoized
  `level_chunk_with_light` frames; ticket-aware bounded LRU cache; shared frame
  admission limit; Anvil `.mca` persistence with autosave, player data, and seed
  metadata.
- **Generation**: vanilla-derived overworld terrain from the embedded datapack
  (`ImprovedNoise`/`PerlinNoise`/`BlendedNoise`/`NormalNoise` + the density
  function interpreter), 3D multi-noise biomes, surface-rule interpretation,
  configured carvers, aquifers, noise-router ore veins, deterministic
  decoration, and template/piece structures. The complete vanilla feature and
  biome datapack graph is embedded; feature ordering, placement modifiers,
  vertical anchors/providers, biome filters, and mutable region writes are
  implemented. Production region replay covers cross-chunk ores, disks,
  underwater magma, stage-2 amethyst geodes, boulders, and large dripstone
  columns, stage-3 monster rooms, stage-7 underground decoration (`ore_infested`,
  `sculk_vein`, `sculk_patch_deep_dark`, `dripstone_cluster`,
  `pointed_dripstone`), stage-8 springs (with 5-direction flowing water/lava
  tick spread), stage-9 lush-cave patches, `multiface_growth`, `vines`, trees,
  huge mushrooms, fallen trees, and flora, plus mineshafts, ruined portals, and
  ocean ruins — the latter including the post-placement block-tick physics
  (falling gravel through water, bubble columns above magma, source-water
  refill) — directly from their datapack configurations.
- **Gameplay & administration**: multi-player session registry; player join/leave
  and delta-movement synchronization (`move_entity_pos`, `move_entity_rot`,
  `move_entity_pos_rot`, `teleport_entity`); chunk-scoped visibility for players
  and mobs with batched `remove_entities` despawns; shared block place/break;
  broadcast chat; Brigadier command tree (`declare_commands`: `/gamemode`, `/tp`,
  `/time`, `/say`, `/list`, `/seed`, `/stop`, `/op`, `/deop`, `/whitelist`,
  `/kick`, `/ban`, `/pardon`) and interactive console stdin; `server.properties`,
  `ops.json`, `whitelist.json`, and `banned-players.json` persistence; and
  hotbar item→block mapping.
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

The server loads `server.properties`, `ops.json`, `whitelist.json`, and
`banned-players.json` from the working directory when present, with CLI flags
taking precedence. The world seed defaults to `0`; override it with the `-seed`
flag, `level-seed` in `server.properties`, or the `REGIONIO_SEED` environment
variable. The server listens on `0.0.0.0:25565`. Changing the seed for an
existing world directory is rejected. The server caps the client-requested chunk
radius at `2` by default because cold density-based generation is expensive;
raise it with `-viewdistance 3` after the surrounding world has been generated
and cached.

## Testing

```
go test -short ./...
# or run the full suite including multi-chunk parity fixtures:
go test ./...
# sharded race suite (~1-4m per shard under -short, matching CI):
make test-race
# or run build, vet, short tests, sharded race tests, and the gated diagnostics:
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
chunks. The canonical single-chunk generator currently matches 95.960% of
fixture blocks (377,329/393,216, measured with `REGIONIO_PARITY_GENERATOR=legacy`;
it no longer places any surface decoration at all, since trees, springs, flora,
desert features and rocks moved to the datapack path), while the production region
replay path matches **99.954%** (393,037/393,216 — 179
residual cells, all underground: 170 below `y=0` and 9 in `0<=y<63`, dominated by
the lush-caves moss and clay pools and their nested vegetation); both match
all fixture biomes and heightmaps. CI checks the region path against a 99.7%
regression floor (91% for the legacy generator, which `make parity` does not
cover), while `make parity` sets `REGIONIO_REQUIRE_PARITY=1`, which upgrades the
same comparison to exact equality and so does not pass yet. The CI regression job
deliberately omits that variable: green CI means "no worse than 99.7%", not "bit
exact", and the two are kept apart so the gap stays visible as a number rather than
turning the whole suite red. GitHub Actions runs four jobs on every push and pull
request: `verify` (`build`, `vet`, `go test -short ./...`), `race` (4 parallel
matrix shards running `-race -short`), `parity` (the ocean and land fixture
regression checks), and `diagnostics`, which mirrors `make diagnostics` —
compiling every test binary and running the env-gated probes in `internal/world`
and `internal/worldgen` that break the remaining gap down per subsystem (ore paths
per state, base-terrain defects with sample coordinates, the lush-caves clay chain
position by position). Every gate named in the source is run there or excused with
a reason, and `TestEveryDiagnosticGateIsReachable` enforces both halves of that,
so a probe cannot quietly become unreachable again. The findings those probes
produced are asserted rather than merely printed, so a conclusion that moves fails
the build instead of landing in a log nobody reads.

## v0.4 scope

RegionIO v0.4 is a small creative/survival multiplayer server core, not a complete
vanilla gameplay implementation. Player and mob visibility is chunk-scoped with
delta-movement encoding (`move_entity_pos`, `move_entity_rot`,
`move_entity_pos_rot`, `teleport_entity`) and batched `remove_entities` despawns.
Lighting matches vanilla's block-state dampening, emission, and face-occlusion
properties and reconciles persisted borders when chunks re-enter the live
cache. Streaming prioritizes Chebyshev rings and abandons unstarted stale work;
an already admitted frame calculation completes atomically rather than being
interrupted halfway. Unowned clean chunks remain as an LRU warm cache until
capacity pressure evicts them. Structures, placed features, mob AI,
inventory, and survival mechanics remain intentionally partial.
The density router, configured carvers, and noise-router ore veins are
vanilla-derived and parity-pinned. The production cache uses atomic batch publication and
the datapack-driven region replay path, with cross-chunk writes isolated per
target. Underground decoration (stage-1 lava lakes, stage-2 geodes/boulders/large
dripstone, stage-3 monster rooms, stage-6 ores with deepslate targets, underwater
magma, and disks, stage-7 infested stone, sculk veins/patches, and dripstone
clusters/pointed dripstone) and the mineshaft, ocean ruin, and ruined portal
structure sets run from the vanilla schedule. Trees replay from the schedule too,
as do the fluid springs of stage 8 (including their 5-direction flowing tick
spread and `VeryBiasedToBottomHeight` sampling) and the glow lichen and vines of
stage 9; the percentage-based flora heuristics are gone.
The biome parameter
finder uses an exact spatial index and overlapping region requests share a
bounded immutable terrain cache, keeping cold 3x3 generation near one second
on the reference Ryzen 5 5600X development machine.

The production cache uses atomic batch publication: a miss builds and publishes
a complete decorated 3x3 neighborhood, while persisted neighbors still take
precedence. This closes the chunk-lifecycle integration boundary without
exposing undecorated chunks.

The remaining block-parity gap is entirely inside feature replay: a vanilla
capture with every feature and structure set stripped out matches our
undecorated pipeline on all 393,216 cells, and a land one on all 786,432 (since the
surface rules and carvers read the per-block zoomed biome, as vanilla's do), so density,
surface rules, carvers, aquifers, and noise-router veins are already bit-exact. Stage-1 lava lakes,
monster rooms (from their datapack configuration, between the geodes and the
ores), ocean ruins (cell-for-cell against a dedicated `-no-features` vanilla
capture of their area, falling gravel and bubble columns included), and
mineshafts (piece-for-piece against the saved vanilla start NBT and
cell-for-cell against a structures-only capture) replay from the datapack —
the fixture's dungeon pocket, a monster room whose wall opening a mineshaft
corridor carved, now places in full. What is left in the ocean fixture is
underground: 170 of its 179 residual cells sit below `y=0` and the waterline and
surface bands contribute none (`waterline=0`, `surface=0`), led by the moss-patch
cell disagreement — which per-target nets show is about which cells a patch covers,
not how far it reaches — then ground cover, clay, and cave vines. Surface decoration
was the larger structural gap and is now **100% exact across all four chunks of the
land fixture**: the hand-written tree path is deleted and trees replay from the
datapack schedule through real trunk and foliage placers — straight, giant, fancy
and dark-oak trunks, blob, pine, spruce, mega-pine, fancy and dark-oak canopies,
with the beehive, alter-ground, place-on-ground, trunk-vine and attached-to-logs
decorators, plus huge red/brown mushrooms and fallen trees — writing through the
region's own radius-one gate, so a canopy may cross a chunk border exactly as vanilla's does. That band is
measured rather than inferred: `testdata/vanilla_land_12345.bin` captures four
land chunks (taiga, old-growth pine taiga, two plains), where the ocean fixture
has no surface at all — top block `water` on all 1,024 columns. Land parity went
from 99.140% (3,382 residual, 1,748 of them in the surface band) with the
hand-written trees, to **99.973%** (393,110/393,216 — **106** residual cells:
`deep=105`, `underground=1`, `waterline=0`, `surface=0`, and **789/789** tree
cells above sea level) now, after:
- the biome placement filter learned to read the zoomed per-block biome vanilla's
  does and `Climate.quantizeCoord` matched Java's `int` truncation toward zero;
- the remaining stage-9 decorators (`place_on_ground`, `trunk_vine`,
  `attached_to_logs`), huge mushrooms, and fallen trees joined the replay;
- stage-7 underground decoration (`ore_infested`, `sculk_vein`,
  `sculk_patch_deep_dark`, `dripstone_cluster`, `pointed_dripstone`), stage-9
  `multiface_growth` / `vines`, and inline `FeatureRef` placement chains joined
  the schedule;
- stage-8 springs (with 5-direction flowing tick spread and
  `VeryBiasedToBottomHeight`) and stage-2 boulders / large dripstone joined the
  schedule, and the flora percentages were deleted;
- leaves were given the distance property vanilla propagates into them (including
  across pre-existing logs and adjacent settled leaves from earlier trees), dark
  oaks grew their own trunks and canopies, and three inverted guards in the ported
  `doPlace` were fixed;
- `decorationRegion` snapshotted `OCEAN_FLOOR_WG` and `WORLD_SURFACE_WG` at build
  time (since `ChunkStatus.CARVERS` freezes both `*_WG` heightmaps before
  `FEATURES`), and generic `decorationSources` switched to column-major order.

All three heightmaps (`WORLD_SURFACE`, `MOTION_BLOCKING`,
`MOTION_BLOCKING_NO_LEAVES`) are now at **100%** (1,024/1,024 each) on both the
ocean and land fixtures; on land, chunk `(16,-40)` taiga is 98,304/98,304 (100%)
and chunk `(-40,21)` plains is 98,303/98,304 (99.999%). Closing the remaining 179
ocean and 106 land underground cells is the road to exact equality, while keeping
cold batch generation within an acceptable latency budget.

## Project layout

```
cmd/regionio/        entry point (config, server.properties, listener, console, graceful shutdown)
cmd/gendump/         client-free generator diagnostics (biomes, surface, bedrock, fluid census)
cmd/genblocks/       generates internal/worldgen/generated_blocks.go from the block report
cmd/genfeatures/     extracts the embedded feature/biome/tag datapack archive from server.jar
cmd/genlight/        legacy light-table generator (superseded by tools/VanillaBlockStateDump.java)
cmd/vanillacapture/  captures vanilla parity fixtures from the official 26.1.2 server (Java 25)
internal/
  protocol/          wire primitives: VarInt, framing, compression, packet IDs
  nbt/               NBT encoder/decoder (with modified UTF-8)
  registry/          embedded synchronized registries + tags
  world/             chunk model, level_chunk encoder, cache, persistence, lighting, generators
  worldgen/          noise/density core, climate finder, aquifer, surface rules, datapack runtime
  network/           per-connection state machine (login/crypto, configuration, play, commands)
  server/            shared core: config, server.properties, sessions, status, entity spawner
```

## Notes

The vanilla `server.jar` and its unpacked `libraries/`/`versions/` are **not**
included (obtain them from Mojang). The embedded data under `internal/`
(registries, biome parameters, the overworld datapack) is derived from vanilla
reports and is all that is required to build and run.
