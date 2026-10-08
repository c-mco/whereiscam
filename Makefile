.PHONY: run build test clean

run:
	go run ./cmd/api

build:
	mkdir -p bin
	go build -o bin/whereiscam ./cmd/api

test:
	go test -v ./...

clean:
	rm -rf bin
