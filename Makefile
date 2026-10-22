.PHONY: build test vet generate run clean web web-setup web-dev web-test check

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

# Install web dependencies only when missing (fresh checkout / CI).
web-setup:
	@if [ ! -d web/node_modules ]; then cd web && npm ci --no-fund --no-audit; fi

web: web-setup
	cd web && npm run build

web-dev:
	cd web && npm run dev

web-test: web-setup
	cd web && npm run test && npm run typecheck

# Everything CI runs: Go vet+tests, web tests+typecheck, SPA build into dist.
check: test web-test web
	@echo "check: all green"
