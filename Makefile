.PHONY: build setup sync dry-run test install clean

VERSION := $(shell git describe --tags --always --dirty)
INSTALL_DIR := $(HOME)/.local/bin

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/bwop-sync  ./cmd/bwop-sync
	go build -ldflags "-X main.version=$(VERSION)" -o bin/bwop-setup ./cmd/bwop-setup

setup: build
	./bin/bwop-setup

sync: build
	./bin/bwop-sync sync

dry-run: build
	./bin/bwop-sync sync --dry-run

test:
	go test ./...

install: build
	cp bin/bwop-sync  $(INSTALL_DIR)/bwop-sync
	cp bin/bwop-setup $(INSTALL_DIR)/bwop-setup

clean:
	rm -rf bin/
