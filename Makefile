.PHONY: build setup sync dry-run test install clean

build:
	go build -o bin/bwop-sync  ./cmd/bwop-sync
	go build -o bin/bwop-setup ./cmd/bwop-setup

setup: build
	./bin/bwop-setup

sync: build
	./bin/bwop-sync sync

dry-run: build
	./bin/bwop-sync sync --dry-run

test:
	go test ./...

install:
	go install ./cmd/bwop-sync
	go install ./cmd/bwop-setup

clean:
	rm -rf bin/
