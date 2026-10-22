# Installing Stasher

Stasher is a single Go binary with an embedded web UI. Only **Go 1.27** is
needed to build it — the SPA is prebuilt and committed
(`internal/webfs/dist`), so there is no Node.js requirement.

## 1. Build from source

**Alpine** (prod runs here):

    apk add go git
    git clone <repo-url> stasher && cd stasher
    go build -o stasher ./cmd/stasher

**Debian/Ubuntu**:

    apt install golang git

Debian stable often ships an older Go; if `go build` complains about the
version, install a current toolchain from <https://go.dev/dl/> (unpack the
tarball, e.g. to `/usr/local/go`, and make sure `go version` reports 1.27+),
then build as above.

**Arch**:

    pacman -S go git

Then:

    go build -o stasher ./cmd/stasher

You now have a self-contained `stasher` binary — it can be copied anywhere
(e.g. `~/stasher` or `/usr/local/bin/`); the Go toolchain is no longer needed
at runtime.

## 2. Configure

    cp stasher.yaml.example stasher.yaml
    $EDITOR stasher.yaml

The example file documents every key. The essentials:

- `listen_addr` — keep `127.0.0.1:8080` when behind a reverse proxy.
- `data_dir` — where the SQLite database and uploaded files live
  (default `./data`). **This directory holds everything: back it up.**
  A consistent backup is as simple as stopping the service (or using
  `sqlite3` online backup) and copying the directory.
- `registration` — leave `invite` (default) for internet-facing hosts.

Every key can also be set with `STASHER_*` environment variables
(`STASHER_LISTEN_ADDR`, `STASHER_BASE_URL`, `STASHER_DATA_DIR`,
`STASHER_REGISTRATION`, `STASHER_LOG_LEVEL`,
`STASHER_TRASH_RETENTION_DAYS`); env overrides the YAML file.

## 3. First run

Apply migrations, then serve:

    ./stasher -config stasher.yaml migrate   # apply migrations and exit
    ./stasher -config stasher.yaml           # serve

Open the site and **register the admin account immediately** — the first
registered user becomes admin (no invite code needed). On an
internet-facing host this is a security step, not a formality: see
[README "First-run security"](../README.md#first-run-security).
Afterwards, invites are created in the admin UI.

## 4. TLS (Caddy reverse proxy)

Stasher serves plain HTTP on localhost; put Caddy in front for HTTPS:

    your.domain.com {
        reverse_proxy 127.0.0.1:8080
    }

Caddy obtains and renews certificates automatically. Any reverse proxy
works; set `base_url` in `stasher.yaml` to the public URL.

## 5. Run as a service

Shipped service files live in the repository under `scripts/`.

### systemd (user service, no root)

Uses `scripts/systemd/stasher.service`. Paths in the unit default to the
binary at `~/stasher` and the config at `~/stasher.yaml` — copy both there,
or edit the unit's `ExecStart` (see the comments inside).

    mkdir -p ~/.config/systemd/user
    cp scripts/systemd/stasher.service ~/.config/systemd/user/
    systemctl --user daemon-reload
    systemctl --user enable --now stasher

Logs: `journalctl --user -u stasher -f`

So the service keeps running after you log out, enable lingering:

    loginctl enable-linger $USER

### OpenRC (Alpine)

Uses `scripts/openrc/stasher`. Edit the clearly-marked `EDIT ME` block at
the top (binary path, config path, run-as user), then install it:

    chmod +x scripts/openrc/stasher
    cp scripts/openrc/stasher /etc/init.d/stasher
    rc-update add stasher default
    rc-service stasher start

The script's `directory=` line is the daemon's working directory: a
relative `data_dir: "./data"` in `stasher.yaml` resolves against **it**,
not the config file's location. Keep `directory` in sync with where
Stasher is deployed, or set an absolute `data_dir` in `stasher.yaml` —
otherwise the binary would try to create `/data` as the service user and
fail to start.

Logs go to `/var/log/stasher.log` and `/var/log/stasher.err.log` (see the
script). Status/restart: `rc-service stasher status|restart`.
