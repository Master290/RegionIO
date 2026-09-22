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
  underwater magma, stage-2 amethyst geodes, lush-cave moss ground patches,
  ruined portals, and ocean ruins — the latter including the post-placement
  block-tick physics (falling gravel through water, bubble columns above
  magma, source-water refill) — directly from their datapack configurations.
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
# internal/world's binary alone is 27.5 min under -race (measured 1649.759s); the whole
# command is 27m37s wall, because packages run concurrently rather than summing.
go test -race -timeout 30m ./...
# or run build, vet, ordinary tests, race tests, and the gated diagnostics:
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
replay path matches 99.916% — 330
residual cells, dominated by the lush-caves moss and clay pools and their nested
vegetation; both match
all fixture biomes and heightmaps. CI checks the region path against a 99.7%
regression floor (91% for the legacy generator, which `make parity` does not
cover), while `make parity` sets `REGIONIO_REQUIRE_PARITY=1`, which upgrades the
same comparison to exact equality and so does not pass yet. The CI regression job
deliberately omits that variable: green CI means "no worse than 99.7%", not "bit
exact", and the two are kept apart so the gap stays visible as a number rather than
turning the whole suite red. GitHub Actions runs four jobs on every push and pull
request: build/vet/tests, the full race suite, the fixture regression check, and
`diagnostics`, which mirrors `make diagnostics` — compiling every test binary and
running the env-gated probes in `internal/world` and `internal/worldgen` that break
the remaining gap down per subsystem (ore paths per state, base-terrain defects with
sample coordinates, the lush-caves clay chain position by position). Every gate named
in the source is run there or excused with a reason, and a test enforces both halves
of that, so a probe cannot quietly become unreachable again. The findings those probes
produced are asserted rather than merely printed, so a conclusion that moves fails the
build instead of landing in a log nobody reads.

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
target. Underground decoration (stage-1 lava lakes, ores with deepslate
targets, underwater magma, disks) and the mineshaft, ocean ruin and ruined
portal structure sets run from the vanilla schedule. Trees now replay from the
schedule too, as do the fluid springs of stage 8 and the boulders of stage 2; the
percentage-based flora heuristics are gone.
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
undecorated pipeline on all 393,216 cells, so density, surface rules, carvers,
aquifers, and noise-router veins are already bit-exact. Stage-1 lava lakes,
monster rooms (from their datapack configuration, between the geodes and the
ores), ocean ruins (cell-for-cell against a dedicated `-no-features` vanilla
capture of their area, falling gravel and bubble columns included), and
mineshafts (piece-for-piece against the saved vanilla start NBT and
cell-for-cell against a structures-only capture) replay from the datapack —
the fixture's dungeon pocket, a monster room whose wall opening a mineshaft
corridor carved, now places in full. What is left in the fixture is
underground: 306 of its 330 residual cells sit below y=0 and the surface band
contributes none, led by the moss-patch cell disagreement — which per-target nets
show is about which cells a patch covers, not how far it reaches — then ground cover,
clay, and cave vines. Surface decoration was the larger structural gap and has now been
reached: the hand-written tree path is deleted and trees replay from the
datapack schedule through real trunk and foliage placers — straight, giant, fancy
and dark-oak trunks, blob, pine, spruce, mega-pine, fancy and dark-oak canopies,
with the beehive and alter-ground decorators — writing through the region's own
radius-one gate, so a canopy may cross a chunk border exactly as vanilla's does. That band is
measured rather than inferred: `testdata/vanilla_land_12345.bin` captures four
land chunks (taiga, old-growth pine taiga, two plains), where the ocean fixture
has no surface at all — top block `water` on all 1,024 columns. Land parity went
from 99.140% (3,382 residual, 1,748 of them in the surface band) with the
hand-written trees, to 99.377% (2,449 residual, 704 in the surface band) now, after
springs and boulders joined the schedule, the flora percentages were deleted, leaves
were given the distance property vanilla propagates into them, dark oaks grew their
own trunks and canopies, and three inverted guards in the ported `doPlace` were fixed —
a tree whose trunk a ceiling cut short is now refused unless `min_clipped_height` says
otherwise, a vine stops a trunk that does not ignore them, and the free-height probe
reaches one row above the trunk like vanilla's. Cells above sea level holding a trunk,
canopy or plant stand at 607 against vanilla's 789; that count went *down* when the
guards were fixed, which is the point — the earlier 577 included trees vanilla never
plants. `MOTION_BLOCKING_NO_LEAVES`, the one heightmap that ignores canopy, is at
98.4% (1008/1024), so the height under the decoration is nearly right and what remains
is placement: the gap sits in the conifer chunks (the taiga one places 229 of vanilla's
285 tree cells, the old-growth one 236 of 353) while the plains pair nearly match,
37 of 37 and 105 of 114, and the counter the diagnostic prints names what is still not
replayed — `place_on_ground` 77, stage-2 `large_dripstone` 18, mushroom selectors and
one fallen tree. The leaf litter is the next step, and with it the question of why a
correctly-shaped dark oak still lands on the wrong cells; the ocean fixture did not move
(330 cells, clay 96/22/9, ore parity zero) under any of this, which is
the check that a surface change stayed on the surface. Closing the rest is the
road to exact equality, while keeping cold batch generation within an acceptable
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
