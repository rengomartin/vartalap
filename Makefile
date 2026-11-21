.PHONY: build run test tidy

build:
	mkdir -p bin
	go build -trimpath -o bin/vartalap ./cmd/server

run:
	go run ./cmd/server

test:
	go test ./... -count=1

tidy:
	go mod tidy
