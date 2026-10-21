# stasher web

Vue 3 + Tailwind v4 SPA. Dev server proxies `/api` and `/files` to the Go
backend at `http://127.0.0.1:8080` (start it with `make run` in the repo root).

```bash
npm run dev        # vite dev server
npm run test       # vitest run
npm run typecheck  # vue-tsc -b
npm run build      # vue-tsc -b && vite build → ../internal/webfs/dist
```

From the repo root, `make web-dev` / `make web-test` / `make web` wrap these
(`make web` uses `npm ci` for reproducible output).

The production build is embedded into the Go binary via `internal/webfs`
(go:embed of `internal/webfs/dist`) — the built dist is committed, so rebuild
with `make web` and commit the result after any change here.
