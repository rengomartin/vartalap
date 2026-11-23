.PHONY: build run test test-js tidy

build:
	mkdir -p bin
	go build -trimpath -o bin/vartalap ./cmd/server

run:
	go run ./cmd/server

test:
	go test ./... -count=1

test-js:
	node scripts/test-crypto.mjs

tidy:
	go mod tidy
