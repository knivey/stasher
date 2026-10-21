# stasher web

Vue 3 + Tailwind v4 SPA. Dev server proxies `/api` and `/files` to the Go
backend at `http://127.0.0.1:8080` (start it with `make run` in the repo root).

```bash
npm install
npm run dev        # vite dev server
npx vitest run     # unit tests
npm run build      # vue-tsc -b && vite build → ../internal/webfs/dist
```

The production build is embedded into the Go binary via `internal/webfs`
(go:embed of `internal/webfs/dist`).
