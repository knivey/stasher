.PHONY: build test vet generate run clean

generate:
	go tool sqlc generate

build:
	go build -o stasher ./cmd/stasher

test:
	go vet ./...
	go test ./...

run:
	go run ./cmd/stasher

clean:
	rm -f stasher
