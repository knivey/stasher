.PHONY: build test vet generate run clean web web-dev web-test

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

web:
	cd web && npm ci && npm run build

web-dev:
	cd web && npm run dev

web-test:
	cd web && npm ci && npm run test
