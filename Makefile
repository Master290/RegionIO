.PHONY: build vet test test-race parity diagnostics verify

build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./...

# Measured on this tree: internal/world alone takes 18.4 minutes under -race and
# the whole module 18.6 wall, because the packages run concurrently rather than
# summing. The old 20m bound therefore left about ninety seconds of slack on the
# dominant package - a one-test margin, and a local 15m run has already been
# misread as a data race when it was a timeout. 30m keeps a real failure legible.
test-race:
	go test -race -timeout 30m ./...

parity:
	test -f internal/world/testdata/vanilla_overworld_12345.bin
	REGIONIO_REQUIRE_PARITY=1 REGIONIO_PARITY_DIAGNOSTIC=1 \
		go test -v ./internal/world -run TestVanillaBlockParity

# Compile every test binary, then run the env-gated worldgen diagnostics. The
# first half is the point: `go build ./...` never compiles _test.go files, and
# debug instrumentation left in internal/world has twice broken the build in a
# way the plain build could not see. (`go build ./...` never touches _test.go
# files, so this is the only step in `verify` that compiles them.)
#
# The last three lines run the probe's causal modes and its unmodelled source, not
# just its default path: SKIP deletes a predecessor's whole contribution, STATE
# digests the world the probed feature is about to read, and TARGET/SOURCE at
# (-1,-1) walks the chunk that decorationSources has no branch for and that carries
# a third of the residual. Together they are what produced the "which
# chunks does a removal actually change" table, and a mode nothing runs rots the
# way the probe's private dispatch copy already did. The modes themselves only
# print; the conclusions they produced are asserted by TestLushClayPositionsAreRecorded
# and the parity and clay fixtures, which is why they are safe to run here.
#
# Every REGIONIO_* gate the source reads is set here or excused in writing in
# excusedGates, because a gate no runner sets is unreachable code that reads like a
# test. That is not hypothetical: the consolidated trace flag is
# REGIONIO_LUSH_CLAY_TRACE, and this file set only REGIONIO_CLAY_TRACE, which gates
# the test-layer prints instead, so the in-generator traces ran in no CI job; six
# ore diagnostics and the trapezoid
# one had no runner at all either. TestEveryDiagnosticGateIsReachable derives the
# list from the source and fails on either side of the mismatch, so this target
# cannot drift again.
diagnostics:
	go test -run TestNothingMatchesThis ./...
	REGIONIO_CLAY_TRACE=1 REGIONIO_LUSH_CLAY_TRACE=1 REGIONIO_LUSH_CLAY_PROBE=1 \
	REGIONIO_CLAY_MISMATCH_DIAG=1 REGIONIO_ORE_SCHEDULE_DIAGNOSTIC=1 \
	REGIONIO_MOSS_PATCH_DIAGNOSTIC=1 REGIONIO_BASE_TERRAIN_DIAGNOSTIC=1 \
	REGIONIO_KELP_TRACE=1 \
		go test -run 'Clay|LushClay|OreSchedule|Moss|VegetationResidual|BaseTerrain|Kelp' ./internal/world
	REGIONIO_ORE_FEATURE_DIAGNOSTIC=1 REGIONIO_ORE_INDEX_DIAGNOSTIC=1 \
	REGIONIO_ORE_ORDER_DIAGNOSTIC=1 REGIONIO_ORE_ORDER_PARITY_DIAGNOSTIC=1 \
	REGIONIO_REGION_ORE_DIAGNOSTIC=1 REGIONIO_SINGLE_CHUNK_ORE_DIAGNOSTIC=1 \
		go test -run 'Ore' ./internal/world -count=1
	REGIONIO_TRAPEZOID_DIAGNOSTIC=1 \
		go test -run Trapezoid ./internal/worldgen -count=1
	REGIONIO_LUSH_CLAY_PROBE=1 REGIONIO_LUSH_CLAY_PROBE_STATE=1 \
	REGIONIO_SETBLOCK_TRACE="-15,-16,-13;-15,-17,-13" \
		go test -run TestProbeLushClayStream -count=1 -v ./internal/world
	REGIONIO_LUSH_CLAY_PROBE=1 REGIONIO_LUSH_CLAY_PROBE_STATE=1 \
	REGIONIO_LUSH_CLAY_PROBE_TARGET="-1,-1" REGIONIO_LUSH_CLAY_PROBE_SOURCE="-1,-1" \
		go test -run TestProbeLushClayStream -count=1 -v ./internal/world
	REGIONIO_LUSH_CLAY_PROBE=1 REGIONIO_LUSH_CLAY_PROBE_STATE=1 \
	REGIONIO_LUSH_CLAY_PROBE_SKIP="-1,-1" REGIONIO_LUSH_CLAY_COLUMN="5,13;2,12" \
		go test -run TestProbeLushClayStream -count=1 -v ./internal/world

# The driver behind the table that excluded hypothesis (a): the feature's reseed
# index varied over 0..40 with the world, the seed and the placement list held
# fixed. Exactly one value reproduces vanilla's pools, which is why the agreement
# counts as evidence rather than as a coincidence. Not part of `diagnostics` - it
# re-replays the schedule 41 times, about a minute - and TestLushClayPositionsAreRecorded
# asserts the two decisive points of it on every run.
clay-index-sweep:
	@for i in $$(seq 0 40); do \
	  printf 'index %2d: ' $$i; \
	  REGIONIO_LUSH_CLAY_PROBE=1 REGIONIO_LUSH_CLAY_PROBE_INDEX=$$i \
	    go test -run TestProbeLushClayStream -count=1 -v ./internal/world 2>/dev/null \
	    | sed -n 's/.*POSITION \(([-0-9,]*)\).*/\1/p' | tr '\n' ' '; \
	  echo; \
	done

# diagnostics is in here because CI runs it as its own job: a gated probe that
# only ever runs by hand is a probe that rots, and the last time that happened the
# private copy had already dropped two draw-consuming feature types while still
# reporting what the decoration stream "really" did. Keeping it out of `verify`
# would mean a diagnostic can break locally and only be caught by the pipeline.
verify: build vet test test-race diagnostics
