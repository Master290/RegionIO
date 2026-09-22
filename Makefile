.PHONY: build vet test test-race parity diagnostics verify

build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./...

test-race:
	go test -race -timeout 20m ./...

parity:
	test -f internal/world/testdata/vanilla_overworld_12345.bin
	REGIONIO_REQUIRE_PARITY=1 go test ./internal/world -run TestVanillaBlockParity

# Compile every test binary, then run the env-gated worldgen diagnostics. The
# first half is the point: `go build ./...` never compiles _test.go files, and
# debug instrumentation left in internal/world has twice broken the build in a
# way `make verify` did not see until a package failed to compile.
#
# The last two lines run the probe's causal modes, not just its default path:
# SKIP deletes a predecessor's whole contribution and STATE digests the world the
# probed feature is about to read. Together they are what produced the "which
# chunks does a removal actually change" table, and a mode nothing runs rots the
# way the probe's private dispatch copy already did. These are printf diagnostics
# - `TestVanillaLushClayDiff` and `TestVanillaBlockParity` are what assert.
diagnostics:
	go test -run TestNothingMatchesThis ./...
	REGIONIO_CLAY_TRACE=1 REGIONIO_LUSH_CLAY_PROBE=1 \
	REGIONIO_CLAY_MISMATCH_DIAG=1 REGIONIO_ORE_SCHEDULE_DIAGNOSTIC=1 \
	REGIONIO_MOSS_PATCH_DIAGNOSTIC=1 \
		go test -run 'Clay|LushClay|OreSchedule|Moss|VegetationResidual' ./internal/world
	REGIONIO_LUSH_CLAY_PROBE=1 REGIONIO_LUSH_CLAY_PROBE_STATE=1 \
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

verify: build vet test test-race
