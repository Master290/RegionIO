.PHONY: build vet test test-race parity verify

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

verify: build vet test test-race
