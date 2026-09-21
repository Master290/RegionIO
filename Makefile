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
diagnostics:
	go test -run TestNothingMatchesThis ./...
	REGIONIO_CLAY_TRACE=1 REGIONIO_LUSH_CLAY_DIFF=1 REGIONIO_LUSH_CLAY_PROBE=1 \
	REGIONIO_CLAY_MISMATCH_DIAG=1 REGIONIO_ORE_SCHEDULE_DIAGNOSTIC=1 \
		go test -run 'Clay|LushClay|OreSchedule' ./internal/world

verify: build vet test test-race
