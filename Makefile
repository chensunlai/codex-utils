.PHONY: build test check snapshot clean

build:
	go build -trimpath -o bin/codex-utils ./cmd/codex-utils

test:
	go test ./...

check:
	gofmt -w cmd internal
	go vet ./...
	go test ./...

snapshot:
	./scripts/build-release.sh dev dist

clean:
	rm -rf bin dist
