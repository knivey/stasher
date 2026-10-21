# Stasher

Self-hosted home inventory: know what you own and where it is.

- Single Go binary + SQLite (WAL) + files on disk — back up one data dir
- Items with quantity, notes, photos, documents (datasheets), tags
- Nested locations (building > cabinet > parts bin), breadcrumbs
- Projects that link items and track quantity used
- Full-text search (SQLite FTS5)
- Multi-user with separate inventories; per-item sharing (view/copy)
- Invite-code registration, admin user management, device session control
- Mobile-friendly (frontend plan); camera photo upload via the browser

## Run

    go build -o stasher ./cmd/stasher
    ./stasher                  # serves on 127.0.0.1:8080, data in ./data
    ./stasher -config stasher.yaml
    ./stasher migrate          # apply migrations and exit

Config: YAML file (`-config path`), env overrides `STASHER_*`
(`LISTEN_ADDR`, `BASE_URL`, `DATA_DIR`, `REGISTRATION`, `LOG_LEVEL`).

The first registered user becomes admin. In `registration: invite` mode the
first user needs no invite code; afterwards admins create codes in the admin UI.

Put Caddy (or any reverse proxy) in front for TLS:

    your.domain.com {
        reverse_proxy 127.0.0.1:8080
    }

## Development

    make generate   # regenerate sqlc code after editing queries.sql
    make test
    make run        # API on :8080; frontend plan adds `npm run dev` proxy
