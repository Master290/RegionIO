.PHONY: test test-race verify

test:
	go test ./...

test-race:
	go test -race ./internal/network ./internal/server ./internal/world \
		-run 'Test(Integration|BoundaryEdit|PlayerInfo|PlayerRegistry|Concurrent|Incremental|EncodeLight|Cache|Store|Eviction|Region)'

verify: test test-race
