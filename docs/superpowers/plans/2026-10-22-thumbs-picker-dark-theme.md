# Thumbnails, Main-Photo Picker, Desktop Thumbs, Dark Theme — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace full-image "thumbnails" with real ≤480px derived files (EXIF-aware, backfilled), let owners pick the main photo (★ + hero image), add a desktop thumbnail column, and ship a system-following dark theme with a persisted toggle.

**Architecture:** Backend adds a pure-Go `internal/thumbs` package (decode → EXIF-rotate → resize → re-encode) wired into upload, a nullable `item_photos.thumb_key` column + startup backfill goroutine, `COALESCE(thumb_key, storage_key)` in every list surface, a `/files` gate that accepts thumb keys, and a transactional make-main endpoint. Frontend adds the desktop thumb column, ★ gallery control + hero, and a class-based Tailwind v4 dark variant driven by a theme composable with a no-FOUC inline script.

**Tech Stack:** Go 1.27 (`stasher`), sqlc, goose, `golang.org/x/image`, `github.com/rwcarlsen/goexif`; Vue 3 + TS + Tailwind v4 (class-based `dark:` variant), vitest + happy-dom.

**Spec:** `docs/superpowers/specs/2026-10-22-thumbnails-picker-dark-theme-design.md`

## Global Constraints

- Module `stasher`, Go 1.27. New deps are pure-Go only: `golang.org/x/image`, `github.com/rwcarlsen/goexif`. No CGO.
- sqlc workflow: edit `internal/db/queries.sql`, run `make generate`, commit generated code. Query names are an API contract — don't rename existing ones.
- Migrations: goose files in `internal/migrations` with `-- +goose Up` / `-- +goose Down` (style of `00002_item_ledger.sql`). Applied automatically at startup.
- Error shape everywhere: `{"error":{"code":"...","message":"..."}}`. Foreign/private resources → 404, never 403.
- Timestamps: TEXT RFC3339 UTC, set by Go.
- Photos are capped at `maxPhotoBytes = 20 << 20` (internal/api/files_handlers.go) — buffering a whole upload in memory is bounded by this.
- Frontend: no `v-html` anywhere; no silent empty `catch`; tests mock `fetch` at the client boundary; typecheck is `vue-tsc -b` (not `--noEmit`); after changing `web/` run `make web` and commit `internal/webfs/dist`.
- TDD: failing test first, watch it fail, implement, watch it pass. Stdlib `testing` only on the Go side (no testify).

## Review Focus

Input classes the spec implies but task tests must still pin (each pinned in the owning task below):

1. **A thumb key must pass the `/files/{key}` owner-or-shared gate** — stranger fetching someone's thumb key → 404; sharee fetching a shared item's thumb → 200. (Pinned in Task 4, Step 1.)
2. **Corrupt/odd image at upload must not fail the upload** — truncated JPEG sniffs as jpeg but `thumbs.Generate` errors; photo row saved with null `thumb_key`, response 201. (Pinned in Task 3, Step 1.)
3. **Old trash snapshots (pre-migration JSON) have no `thumb_key`** — unmarshal yields empty string; purge must not attempt deleting key `""` as if real; restore inserts null thumb. (`Storage.Delete("")` is already a `ValidKey` no-op — pin the restore-null behavior in Task 5, Step 1.)
4. **EXIF orientation 6** — a 4×2 landscape JPEG tagged orientation 6 produces a 2×4 portrait thumb (and the original still displays correctly in browsers). (Pinned in Task 1, Step 1.)
5. **Photos with duplicate `sort_order`** — make-main renumbering must be deterministic: target → 0, others keep current `(sort_order, id)` order. (Pinned in Task 7, Step 1.)

---

## Batch 1 — Backend

### Task 1: `internal/thumbs` package

**Files:**
- Create: `internal/thumbs/thumbs.go`
- Test: `internal/thumbs/thumbs_test.go`
- Modify: `go.mod`, `go.sum` (new deps)

**Interfaces:**
- Consumes: none (leaf package; stdlib + x/image + goexif).
- Produces: `thumbs.Generate(src []byte) (thumb []byte, ext string, err error)` — `ext` is `"jpg"`, `"png"`, or `"gif"` (no dot). Later tasks rely on exactly this signature.

- [ ] **Step 1: Write the failing tests**

Create `internal/thumbs/thumbs_test.go`. Two verified base64 fixtures are baked in (both validated against the exact libraries the code will use — `x/image/webp` decodes the first, `goexif` reads orientation 6 from the second):

```go
package thumbs

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

// 8x8 lossy WebP, 64 bytes. Verified decodable by golang.org/x/image/webp.
const tinyWebP = "UklGRjgAAABXRUJQVlA4ICwAAACQAQCdASoIAAgAAgA0JaACdLoAA5gA/vBsr/c1FbFvQ/9pZ/9Sz/6ln+KgAA=="

// 4x2 JPEG carrying EXIF Orientation=6 (rotate 90 CW to display).
// Verified: goexif reads orientation 6; image/jpeg decodes 4x2.
const orient6JPEG = "/9j/4QAiRXhpZgAASUkqAAgAAAABABIBAwABAAAABgAAAAAAAAD/4AAQSkZJRgABAQAAAQABAAD/2wBDAAMCAgICAgMCAgIDAwMDBAYEBAQEBAgGBgUGCQgKCgkICQkKDA8MCgsOCwkJDRENDg8QEBEQCgwSExIQEw8PEBAQ/9sAQwEDAwMEAwQIBAQIEAsJCxAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBD/wAARCAACAAQDAREAAhEBAxEB/8QAFAABAAAAAAAAAAAAAAAAAAAACP/EABoQAAEFAQAAAAAAAAAAAAAAAAUAAhZVlNH/xAAVAQEBAAAAAAAAAAAAAAAAAAADCf/EABwRAAAHAQEAAAAAAAAAAAAAAAADBAYXVdKTlP/aAAwDAQACEQMRAD8AA8hP3hDS/qo1GDHpknnJwAkR4Wynubsf/9k="

// makeImage builds a w*h gradient PNG/JPEG/GIF in memory.
func makeImage(t *testing.T, w, h int, enc string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 255 / w), G: uint8(y * 255 / h), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	switch enc {
	case "jpeg":
		if err := jpeg.Encode(&buf, img, nil); err != nil {
			t.Fatal(err)
		}
	case "png":
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
	case "gif":
		if err := gif.Encode(&buf, img, nil); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

func decodeDims(t *testing.T, b []byte, ext string) (int, int) {
	t.Helper()
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("thumb does not decode as %s: %v", ext, err)
	}
	return cfg.Width, cfg.Height
}

func TestGenerateFormatMapping(t *testing.T) {
	cases := []struct {
		name string
		src  []byte
		ext  string // expected thumb extension
	}{
		{"jpeg source -> jpg", makeImage(t, 900, 600, "jpeg"), "jpg"},
		{"webp source -> jpg", mustB64(t, tinyWebP), "jpg"},
		{"png source -> png", makeImage(t, 900, 600, "png"), "png"},
		{"gif source -> gif", makeImage(t, 900, 600, "gif"), "gif"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			thumb, ext, err := Generate(tc.src)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if ext != tc.ext {
				t.Fatalf("ext = %q, want %q", ext, tc.ext)
			}
			w, h := decodeDims(t, thumb, ext)
			if w > 480 || h > 480 {
				t.Fatalf("thumb %dxh exceeds 480 long side: %dx%d", w, h, w)
			}
		})
	}
}

func TestGenerateNoUpscale(t *testing.T) {
	thumb, ext, err := Generate(mustB64(t, tinyWebP)) // 8x8 source
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if w, h := decodeDims(t, thumb, ext); w != 8 || h != 8 {
		t.Fatalf("8x8 upscaled to %dx%d; never upscale", w, h)
	}
}

func TestGenerateEXIFOrientation6(t *testing.T) {
	// 4x2 landscape + orientation 6 (rotate 90 CW) => displayed 2x4 portrait.
	thumb, ext, err := Generate(mustB64(t, orient6JPEG))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if ext != "jpg" {
		t.Fatalf("ext = %q, want jpg", ext)
	}
	if w, h := decodeDims(t, thumb, ext); w != 2 || h != 4 {
		t.Fatalf("oriented thumb = %dx%d, want 2x4", w, h)
	}
}

func TestGenerateCorruptInput(t *testing.T) {
	junk := append([]byte{}, makeImage(t, 40, 40, "jpeg")...)
	junk = junk[:len(junk)/2] // truncated mid-stream
	if _, _, err := Generate(junk); err == nil {
		t.Fatal("truncated jpeg: expected error, got nil")
	}
	if _, _, err := Generate([]byte("not an image at all")); err == nil {
		t.Fatal("garbage: expected error, got nil")
	}
}

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("bad fixture: %v", err)
	}
	return b
}
```

- [ ] **Step 2: Add dependencies and run tests to verify they fail**

```bash
go get golang.org/x/image@latest github.com/rwcarlsen/goexif@latest
go test ./internal/thumbs/ -v
```

Expected: build failure — `Generate` undefined.

- [ ] **Step 3: Implement `internal/thumbs/thumbs.go`**

```go
// Package thumbs generates small derived preview images (longest side <=
// 480px) from uploaded photos, so lists never transfer originals.
package thumbs

import (
	"bytes"
	"image"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"

	"github.com/rwcarlsen/goexif/exif"
	"golang.org/x/image/draw"

	_ "golang.org/x/image/webp" // register webp decoding
)

const MaxLongSide = 480

// Generate returns a preview of src. ext is "jpg", "png", or "gif" (no dot),
// chosen by source format: jpeg/webp encode as JPEG (photos; webp has no
// stdlib encoder), png stays PNG (alpha-safe), gif stays GIF (first frame).
func Generate(src []byte) (thumb []byte, ext string, err error) {
	img, format, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, "", err
	}
	if format == "jpeg" {
		if o := jpegOrientation(src); o > 1 {
			img = applyOrientation(img, o)
		}
	}
	img = fitLongSide(img, MaxLongSide)

	var buf bytes.Buffer
	switch format {
	case "png":
		ext = "png"
		err = png.Encode(&buf, img)
	case "gif":
		ext = "gif"
		err = gif.Encode(&buf, img, nil)
	default: // jpeg, webp
		ext = "jpg"
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 78})
	}
	if err != nil {
		return nil, "", err
	}
	return buf.Bytes(), ext, nil
}

// jpegOrientation returns the EXIF Orientation tag (1..8), or 0 when absent
// or unreadable (degrade gracefully: browsers honor EXIF on the original,
// so an unrotated thumb is the only failure mode).
func jpegOrientation(src []byte) int {
	x, err := exif.Decode(bytes.NewReader(src))
	if err != nil {
		return 0
	}
	tag, err := x.Get(exif.Orientation)
	if err != nil {
		return 0
	}
	v, err := tag.Int(0)
	if err != nil || v < 1 || v > 8 {
		return 0
	}
	return v
}

// applyOrientation returns img transformed for EXIF orientation o so it
// displays upright (browsers do this implicitly for originals).
func applyOrientation(img image.Image, o int) image.Image {
	switch o {
	case 2:
		return flipH(img)
	case 3:
		return rotate180(img)
	case 4:
		return flipV(img)
	case 5:
		return rotate90(flipH(img))
	case 6:
		return rotate90(img)
	case 7:
		return rotate270(flipH(img))
	case 8:
		return rotate270(img)
	}
	return img
}

func toRGBA(src image.Image) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

func rotate90(src image.Image) *image.RGBA { // clockwise
	s := toRGBA(src)
	b := s.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dy(), b.Dx()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.Set(b.Dy()-1-y, x, s.At(x, y))
		}
	}
	return dst
}

func rotate180(src image.Image) *image.RGBA {
	s := toRGBA(src)
	b := s.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.Set(b.Dx()-1-x, b.Dy()-1-y, s.At(x, y))
		}
	}
	return dst
}

func rotate270(src image.Image) *image.RGBA { // counterclockwise
	s := toRGBA(src)
	b := s.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dy(), b.Dx()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.Set(y, b.Dx()-1-x, s.At(x, y))
		}
	}
	return dst
}

func flipH(src image.Image) *image.RGBA {
	s := toRGBA(src)
	b := s.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.Set(b.Dx()-1-x, y, s.At(x, y))
		}
	}
	return dst
}

func flipV(src image.Image) *image.RGBA {
	s := toRGBA(src)
	b := s.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.Set(x, b.Dy()-1-y, s.At(x, y))
		}
	}
	return dst
}

// fitLongSide scales img down so its longest side is <= max; never upscales.
func fitLongSide(img image.Image, max int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	long := w
	if h > long {
		long = h
	}
	if long <= max {
		return img
	}
	scale := float64(max) / float64(long)
	dw, dh := int(float64(w)*scale), int(float64(h)*scale)
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

// ensure io import is used only if needed; drop it if go vet complains.
var _ = io.Discard
```

(If `go vet` flags the unused `io` import, delete the import and the `var _` line.)

- [ ] **Step 4: Run tests to verify they pass**

```bash
go vet ./internal/thumbs/ && go test ./internal/thumbs/ -v
```

Expected: all PASS, including `TestGenerateEXIFOrientation6` (2×4).

- [ ] **Step 5: Commit**

```bash
git add internal/thumbs/ go.mod go.sum
git commit -m "feat: pure-Go thumbnail generation (EXIF-aware, <=480px)"
```

---

### Task 2: Migration + sqlc queries for `thumb_key`

**Files:**
- Create: `internal/migrations/00004_photos_thumb_key.sql`
- Modify: `internal/db/queries.sql` (AddItemPhoto, GetPhotoByKey; add SetItemPhotoThumb, ListPhotosMissingThumbs)
- Modify (generated): `internal/db/queries.sql.go`, `internal/db/models.go` (via `make generate`)
- Modify: `internal/api/files_handlers.go:93`, `internal/api/shared_handlers.go:120`, `internal/api/trash_handlers.go:310` (call sites compile again with empty thumb)

**Interfaces:**
- Consumes: none.
- Produces (relied on by Tasks 3–7):
  - `dbpkg.AddItemPhotoParams` gains `ThumbKey sql.NullString`
  - `dbpkg.SetItemPhotoThumb(ctx, dbpkg.SetItemPhotoThumbParams{ThumbKey string, ID int64}) error`
  - `dbpkg.ListPhotosMissingThumbs(ctx) ([]dbpkg.ItemPhoto, error)`
  - `dbpkg.GetPhotoByKey(ctx, dbpkg.GetPhotoByKeyParams{Key string}) (dbpkg.GetPhotoByKeyRow, error)` — now matches `storage_key = key OR thumb_key = key`
  - `dbpkg.ItemPhoto` gains `ThumbKey sql.NullString`

- [ ] **Step 1: Write the migration**

Create `internal/migrations/00004_photos_thumb_key.sql`:

```sql
-- +goose Up
ALTER TABLE item_photos ADD COLUMN thumb_key TEXT;

-- +goose Down
ALTER TABLE item_photos DROP COLUMN thumb_key;
```

- [ ] **Step 2: Edit `internal/db/queries.sql`**

Change `AddItemPhoto` (currently line ~215):

```sql
-- name: AddItemPhoto :one
INSERT INTO item_photos (item_id, storage_key, thumb_key, caption, sort_order, created_at)
VALUES (?, ?, ?, ?, ?, ?) RETURNING *;
```

Change `GetPhotoByKey` (currently line ~225) so the `/files` gate accepts thumb keys too:

```sql
-- name: GetPhotoByKey :one
SELECT p.id AS photo_id, p.storage_key, p.caption, p.sort_order,
       i.user_id, i.visibility
FROM item_photos p JOIN items i ON i.id = p.item_id
WHERE p.storage_key = sqlc.arg('key') OR p.thumb_key = sqlc.arg('key');
```

Add after `DeleteItemPhoto`:

```sql
-- name: SetItemPhotoThumb :exec
UPDATE item_photos SET thumb_key = ? WHERE id = ?;

-- name: ListPhotosMissingThumbs :many
SELECT * FROM item_photos WHERE thumb_key IS NULL ORDER BY id;
```

- [ ] **Step 3: Regenerate and fix compile**

```bash
make generate
```

Fix the three `AddItemPhoto` call sites and the `GetPhotoByKey` call site so the tree compiles with **empty** thumbs (real values come in Tasks 3 and 5):
- `internal/api/files_handlers.go:93` — add `ThumbKey: sql.NullString{}` (keep `dbpkg.` prefix as the file already uses).
- `internal/api/shared_handlers.go:120` — same.
- `internal/api/trash_handlers.go:310` — same.
- `handleFileGet` (`internal/api/files_handlers.go`, photo branch) — `GetPhotoByKey(r.Context(), dbpkg.GetPhotoByKeyParams{Key: key})`.

- [ ] **Step 4: Run tests to verify nothing broke**

```bash
go vet ./... && go test -count=1 ./...
```

Expected: all green (nullable column, OR-gate matches existing keys exactly; thumbs are never stored yet, so behavior is identical).

- [ ] **Step 5: Commit**

```bash
git add internal/migrations/ internal/db/ internal/api/
git commit -m "feat: item_photos.thumb_key column, OR-gate, backfill queries"
```

---

### Task 3: Generate thumbs at upload

**Files:**
- Modify: `internal/api/files_handlers.go` (`handleUploadPhoto`, lines ~73–105)
- Test: `internal/api/files_handlers_test.go` (extend)

**Interfaces:**
- Consumes: `thumbs.Generate` (Task 1), `AddItemPhotoParams.ThumbKey` (Task 2).
- Produces: uploaded photos carry a derived thumb file + non-null `thumb_key`; upload response unchanged (thumb key surfaces via detail JSON in Task 4).

- [ ] **Step 1: Write the failing tests**

Add to `internal/api/files_handlers_test.go` (reuse existing helpers `newTestServer`, `registerUser`, and however existing upload tests build multipart JPEG bodies — follow the file's current conventions):

```go
func TestUploadPhotoGeneratesThumb(t *testing.T) {
	ts := newTestServer(t)
	cookie := registerUser(t, ts, "thumbgen@test.dev", "longpassword1")
	itemID := createItem(t, ts, cookie, "widget") // helper exists in this file's style

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	fw, _ := w.CreateFormFile("file", "big.jpg")
	fw.Write(jpegBytes(t, 1200, 800)) // build with image/jpeg in-test, like existing tests
	w.Close()
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/items/%d/photos", itemID), body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	ts.srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d", rec.Code)
	}

	photos, err := ts.srv.Queries().ListItemPhotos(context.Background(), itemID)
	if err != nil || len(photos) != 1 {
		t.Fatalf("photos: %v %d", err, len(photos))
	}
	if !photos[0].ThumbKey.Valid || photos[0].ThumbKey.String == "" {
		t.Fatal("thumb_key not set on upload")
	}
	if photos[0].ThumbKey.String == photos[0].StorageKey {
		t.Fatal("thumb_key must be a derived file, not the original")
	}
	tf, err := ts.srv.Storage().Open(photos[0].ThumbKey.String)
	if err != nil {
		t.Fatalf("thumb file missing: %v", err)
	}
	defer tf.Close()
	cfg, _, err := image.DecodeConfig(tf)
	if err != nil {
		t.Fatalf("thumb undecodable: %v", err)
	}
	if cfg.Width > 480 || cfg.Height > 480 {
		t.Fatalf("thumb %dx%d exceeds 480", cfg.Width, cfg.Height)
	}
}

func TestUploadPhotoCorruptStillSavesOriginal(t *testing.T) {
	// Sniffable-as-jpeg bytes that fail full decode: a valid JPEG prefix
	// followed by truncation.
	full := jpegBytes(t, 100, 100)
	trunc := full[:len(full)/2]
	// ...upload `trunc` exactly as above...
	// Expect: 201; row exists; ThumbKey.Valid == false (fallback: lists will
	// serve the original for this photo).
}
```

If `ts.srv.Queries()`/`ts.srv.Storage()` accessors don't exist on the test server type, expose them (add tiny methods in `testutil_test.go`) rather than reaching into unexported fields from a different file.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/api/ -run 'TestUploadPhoto' -v
```

Expected: FAIL — `thumb_key not set on upload`.

- [ ] **Step 3: Implement**

In `handleUploadPhoto` (`internal/api/files_handlers.go`), replace the streaming save (lines ~73–101) with buffered handling:

```go
	head := make([]byte, 512)
	n, _ := f.Read(head)
	head = head[:n]
	if n == 0 {
		writeErr(w, http.StatusBadRequest, "empty_file", "file is empty")
		return
	}
	ct := http.DetectContentType(head)
	if !allowedPhotoTypes[ct] {
		writeErr(w, http.StatusUnsupportedMediaType, "unsupported_type",
			"photos must be jpeg, png, webp, or gif")
		return
	}
	// buffer the rest (bounded by maxPhotoBytes) so the thumb generator can
	// re-read the bytes
	rest, err := io.ReadAll(f)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_upload", "bad multipart body or file too large")
		return
	}
	buf := append(head, rest...)

	key, _, err := s.st.Save(bytes.NewReader(buf), extForMime(ct))
	if err != nil {
		s.internalErr(w, err)
		return
	}

	// derived thumbnail; failure degrades to null (lists COALESCE to original)
	var thumbKey sql.NullString
	if tb, ext, err := thumbs.Generate(buf); err != nil {
		slog.Warn("thumb generation failed", "err", err)
	} else if tkey, _, err := s.st.Save(bytes.NewReader(tb), ext); err != nil {
		slog.Warn("thumb save failed", "err", err)
	} else {
		thumbKey = sql.NullString{String: tkey, Valid: true}
	}

	photo, err := s.q.AddItemPhoto(r.Context(), dbpkg.AddItemPhotoParams{
		ItemID: item.ID, StorageKey: key, ThumbKey: thumbKey, Caption: "",
		SortOrder: 0, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		s.st.Delete(key)
		s.st.Delete(thumbKey.String)
		s.internalErr(w, err)
		return
	}
```

Add imports: `"bytes"`, `"log/slog"`, `"database/sql"`, `stasher/internal/thumbs` (drop `io.MultiReader` if now unused).

- [ ] **Step 4: Run tests to verify they pass**

```bash
go vet ./... && go test -count=1 ./internal/api/ -run 'TestUploadPhoto' -v && go test -count=1 ./...
```

Expected: PASS. Also run `go test ./internal/api/ -run TestItemThumbnailPicking -v` — if that existing test (list_handlers_test.go:178) compares `thumb_key` to the photo's `storage_key`, update it to compare against the row's `ThumbKey` (the endpoint now returns the derived key; its real subject — lowest `(sort_order, id)` photo wins — is unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/api/
git commit -m "feat: generate 480px thumbnails on photo upload"
```

---

### Task 4: `COALESCE` thumb keys on every JSON surface + gate tests

**Files:**
- Modify: `internal/api/items_handlers.go` (`buildItemListSQL` line ~449; `itemDetailJSON` photos ~56–59)
- Modify: `internal/db/queries.sql` (`ListSharedItemsFromOthers` ~198) + regenerate
- Modify: `internal/api/files_handlers.go` (photo list JSON ~52–58)
- Test: `internal/api/list_handlers_test.go`, `internal/api/shared_handlers_test.go`, `internal/api/files_handlers_test.go`

**Interfaces:**
- Consumes: Task 3 uploads with real thumbs.
- Produces: `/items`, `/items/{id}`, `/items/{id}/photos`, `/shared` all expose `thumb_key` = derived thumb with original-file fallback. Frontend (Task 8/9) reads exactly this field.

- [ ] **Step 1: Write the failing tests**

Extend `list_handlers_test.go` (photo fixture via existing upload helpers):

```go
// A thumb key from a shared item must serve to the sharee (200), and any
// thumb key must 404 for a stranger — the gate applies to derived files.
func TestThumbKeyGate(t *testing.T) {
	ts := newTestServer(t)
	alice := registerUser(t, ts, "gate-a@test.dev", "longpassword1")
	bob := registerUser(t, ts, "gate-b@test.dev", "longpassword1")
	// alice uploads a photo, shares the item; GET /shared as bob yields
	// thumb_key; GET /files/{thumb_key} as bob -> 200; as mallory -> 404.
}
```

Also assert in the existing thumb-picking test's flow that `GET /items/{id}` detail JSON now includes non-null `thumb_key` inside `photos[]`.

- [ ] **Step 2: Run to verify failure**

```bash
go test ./internal/api/ -run 'TestThumbKeyGate' -v
```

Expected: FAIL (detail JSON lacks `thumb_key`; gate 404s thumb keys because no row matches… if Task 2's OR-gate landed, the shared-200 case fails only if you fetch with a stranger — stranger sees 404 already; the missing piece is the field in detail JSON and any list fallback rows).

- [ ] **Step 3: Implement the four surfaces**

`buildItemListSQL` (items_handlers.go:449):

```go
       (SELECT COALESCE(p.thumb_key, p.storage_key) FROM item_photos p WHERE p.item_id = i.id ORDER BY p.sort_order, p.id LIMIT 1) AS thumb_key,
```

`itemDetailJSON` photo map (items_handlers.go:56–59) — add helper and field:

```go
// at top of items_handlers.go near other helpers:
func thumbJSON(ns sql.NullString) any {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	return ns.String
}
// in the photo map:
			"id": p.ID, "storage_key": p.StorageKey, "thumb_key": thumbJSON(p.ThumbKey),
			"caption": p.Caption, "sort_order": p.SortOrder, "created_at": p.CreatedAt,
```

`ListSharedItemsFromOthers` (queries.sql:198) + `make generate`:

```sql
        COALESCE(tp.thumb_key, tp.storage_key) AS thumb_key,
```

Photo-list JSON (files_handlers.go:52–58, the `handleListPhotos` map): add `"thumb_key": thumbJSON(p.ThumbKey),` (move `thumbJSON` to a shared spot if the compiler wants it elsewhere — `respond.go` is fine).

- [ ] **Step 4: Run all tests**

```bash
make generate && go vet ./... && go test -count=1 ./...
```

Expected: green, including the two existing thumb tests (updated in Task 3 to expect derived keys).

- [ ] **Step 5: Update `docs/api-notes.md`**

In the thumbnails section: `thumb_key` on `/items`, `/items/{id}` photos, `/items/{id}/photos`, `/shared` is now a **derived ≤480px file** (`jpg`/`png`/`gif`); `COALESCE(thumb_key, storage_key)` — when generation failed (legacy photos until backfill, corrupt images) the original key is returned instead. `/files/{thumb_key}` passes the same owner-or-shared gate (matched against `storage_key OR thumb_key`).

- [ ] **Step 6: Commit**

```bash
git add internal/ docs/api-notes.md
git commit -m "feat: COALESCE derived thumb keys across list/detail surfaces"
```

---

### Task 5: Photo lifecycle carries thumbs (delete / trash purge / restore / copy)

**Files:**
- Modify: `internal/api/files_handlers.go` (`handleDeletePhoto` ~162–200)
- Modify: `internal/api/trash_handlers.go` (snapshot struct ~36, builder ~92, purge ~146, listing ~177, restore ~309)
- Modify: `internal/api/shared_handlers.go` (copy loop ~114–128)
- Test: `internal/api/trash_handlers_test.go`, `internal/api/shared_handlers_test.go`, `internal/api/files_handlers_test.go`

**Interfaces:**
- Consumes: `ItemPhoto.ThumbKey` (Task 2).
- Produces: no orphan thumb files on delete/purge; restored/copied items keep working thumbs; trash rows `thumb_key` remains informational.

- [ ] **Step 1: Write the failing tests**

In `trash_handlers_test.go`:

```go
// trash -> purge removes BOTH the original and the thumb file from disk
func TestPurgeDeletesThumbFiles(t *testing.T) { /* upload photo (has thumb), trash item,
	simulate retention expiry via direct DB update like existing purge tests,
	run PurgeExpiredTrash, assert st.Open(original) and st.Open(thumb) both fail */ }

// restore recreates the photo row WITH thumb_key (old snapshots without it
// restore with NULL thumb — decode a snapshot JSON lacking thumb_key by
// inserting it directly, then assert ThumbKey.Valid == false)
func TestRestoreCarriesThumbKey(t *testing.T) { /* upload, trash, capture row id+thumb,
	purge the row's trash entry? no: restore, assert new photo row has same thumb_key */ }
```

In `shared_handlers_test.go`:

```go
// copy duplicates the thumb FILE (both files exist independently) and the row
func TestCopyDuplicatesThumb(t *testing.T) { /* upload photo, copy item, assert copy's
	photo row ThumbKey.Valid, differs from source thumb key, and st.Open works */ }
```

In `files_handlers_test.go`:

```go
// deleting a photo removes original AND thumb files
func TestDeletePhotoRemovesThumb(t *testing.T) { /* upload, delete via API, both st.Open fail */ }
```

- [ ] **Step 2: Run to verify failures**

```bash
go test ./internal/api/ -run 'TestPurgeDeletesThumbFiles|TestRestoreCarriesThumbKey|TestCopyDuplicatesThumb|TestDeletePhotoRemovesThumb' -v
```

- [ ] **Step 3: Implement**

`handleDeletePhoto` — the handler already loads `photos` (line ~173); find the target row and delete both keys:

```go
	for _, p := range photos {
		if p.ID == photoID {
			s.st.Delete(p.StorageKey)
			if p.ThumbKey.Valid {
				s.st.Delete(p.ThumbKey.String)
			}
			break
		}
	}
```

(Preserve the existing flow — only the deletion calls are added; check whether the handler already deletes `p.StorageKey` and add only the thumb line.)

`trash_handlers.go`:

```go
type trashSnapshotPhoto struct {
	StorageKey string `json:"storage_key"`
	ThumbKey   string `json:"thumb_key,omitempty"`
	Caption    string `json:"caption"`
	SortOrder  int64  `json:"sort_order"`
}
```

`buildTrashSnapshot` (~92): add `ThumbKey: p.ThumbKey.String` (empty when invalid — old snapshots unmarshal to `""`, which the code below treats as absent).

`deleteSnapshotFiles` (~146):

```go
	for _, p := range snap.Photos {
		s.st.Delete(p.StorageKey)
		if p.ThumbKey != "" {
			s.st.Delete(p.ThumbKey)
		}
	}
```

Restore loop (~309): pass `ThumbKey: sql.NullString{String: p.ThumbKey, Valid: p.ThumbKey != ""}`.

`trashRowJSON` (~177): prefer the snapshot's thumb when present:

```go
	var thumb any
	if len(snap.Photos) > 0 {
		k := snap.Photos[0].ThumbKey
		if k == "" {
			k = snap.Photos[0].StorageKey
		}
		thumb = k
	}
```

`shared_handlers.go` copy loop (~114): copy the thumb file and pass it through:

```go
	for _, ph := range photos {
		newKey, err := s.st.Copy(ph.StorageKey)
		if err != nil {
			s.internalErr(w, err)
			return
		}
		var newThumb sql.NullString
		if ph.ThumbKey.Valid {
			tk, err := s.st.Copy(ph.ThumbKey.String)
			if err != nil {
				s.st.Delete(newKey)
				s.internalErr(w, err)
				return
			}
			newThumb = sql.NullString{String: tk, Valid: true}
		}
		if _, err := s.q.AddItemPhoto(r.Context(), dbpkg.AddItemPhotoParams{
			ItemID: newItem.ID, StorageKey: newKey, ThumbKey: newThumb,
			Caption: ph.Caption, SortOrder: ph.SortOrder, CreatedAt: now,
		}); err != nil {
			s.st.Delete(newKey)
			s.st.Delete(newThumb.String)
			s.internalErr(w, err)
			return
		}
	}
```

- [ ] **Step 4: Run all tests**

```bash
go vet ./... && go test -count=1 ./...
```

Expected: green.

- [ ] **Step 5: Commit**

```bash
git add internal/api/
git commit -m "feat: photo delete/trash/copy carry thumbnail files"
```

---

### Task 6: Startup backfill for existing photos

**Files:**
- Modify: `internal/api/files_handlers.go` (add `BackfillThumbs`)
- Modify: `cmd/stasher/main.go` (launch after trash purge, ~line 93)
- Test: `internal/api/files_handlers_test.go`

**Interfaces:**
- Consumes: `ListPhotosMissingThumbs`, `SetItemPhotoThumb` (Task 2), `thumbs.Generate` (Task 1).
- Produces: `(*Server) BackfillThumbs(ctx context.Context)` — idempotent, never returns error (logs per-photo failures).

- [ ] **Step 1: Write the failing tests**

```go
func TestBackfillThumbs(t *testing.T) {
	ts := newTestServer(t)
	cookie := registerUser(t, ts, "backfill@test.dev", "longpassword1")
	itemID := createItem(t, ts, cookie, "old photo item")

	// simulate a pre-migration photo: file on disk, row with NULL thumb
	key, _, err := ts.srv.Storage().Save(bytes.NewReader(jpegBytes(t, 900, 700)), "jpg")
	// ...require no error; insert row via ts.srv.Queries().AddItemPhoto with
	// ThumbKey: sql.NullString{} ...

	ts.srv.BackfillThumbs(context.Background())

	photos, _ := ts.srv.Queries().ListItemPhotos(context.Background(), itemID)
	if len(photos) != 1 || !photos[0].ThumbKey.Valid {
		t.Fatal("backfill did not set thumb_key")
	}
	// second run is a no-op (no rows missing thumbs) — call again, assert unchanged
}

func TestBackfillThumbsSkipsCorrupt(t *testing.T) {
	// row whose file is corrupt bytes saved with .jpg ext: stays NULL after run,
	// no panic, other rows still processed
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test ./internal/api/ -run TestBackfillThumbs -v
```

Expected: FAIL — `BackfillThumbs` undefined.

- [ ] **Step 3: Implement**

In `internal/api/files_handlers.go`:

```go
// BackfillThumbs generates derived thumbnails for photos uploaded before
// thumb generation existed. Safe to run repeatedly; per-photo failures are
// logged and skipped (lists fall back to the original image).
func (s *Server) BackfillThumbs(ctx context.Context) {
	rows, err := s.q.ListPhotosMissingThumbs(ctx)
	if err != nil {
		slog.Error("thumb backfill: list failed", "err", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	slog.Info("thumb backfill started", "photos", len(rows))
	done := 0
	for _, p := range rows {
		if err := ctx.Err(); err != nil {
			return
		}
		f, err := s.st.Open(p.StorageKey)
		if err != nil {
			slog.Warn("thumb backfill: open failed", "photo", p.ID, "err", err)
			continue
		}
		src, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			slog.Warn("thumb backfill: read failed", "photo", p.ID, "err", err)
			continue
		}
		tb, ext, err := thumbs.Generate(src)
		if err != nil {
			slog.Warn("thumb backfill: generate failed", "photo", p.ID, "err", err)
			continue
		}
		tkey, _, err := s.st.Save(bytes.NewReader(tb), ext)
		if err != nil {
			slog.Warn("thumb backfill: save failed", "photo", p.ID, "err", err)
			continue
		}
		if err := s.q.SetItemPhotoThumb(ctx, dbpkg.SetItemPhotoThumbParams{
			ThumbKey: tkey, ID: p.ID,
		}); err != nil {
			slog.Warn("thumb backfill: update failed", "photo", p.ID, "err", err)
			s.st.Delete(tkey)
			continue
		}
		done++
	}
	slog.Info("thumb backfill done", "generated", done, "skipped", len(rows)-done)
}
```

In `cmd/stasher/main.go`, right after the `PurgeExpiredTrash` call (~line 93):

```go
	// derive thumbnails for photos uploaded before thumb generation existed
	go srv.BackfillThumbs(context.Background())
```

- [ ] **Step 4: Run all tests + manual smoke**

```bash
go vet ./... && go test -count=1 ./... && go build -o stasher ./cmd/stasher && ./scripts/smoke-web.sh
```

Expected: green; smoke prints `smoke ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/api/ cmd/stasher/
git commit -m "feat: background startup backfill of photo thumbnails"
```

---

### Task 7: Make-main endpoint

**Files:**
- Modify: `internal/api/server.go` (route, near existing photo routes)
- Modify: `internal/api/files_handlers.go` (new `handleMakePhotoMain`)
- Modify: `docs/api-notes.md`
- Test: `internal/api/files_handlers_test.go`

**Interfaces:**
- Consumes: `ListItemPhotos`, `UpdateItemPhoto`, DB tx pattern from trash restore (`s.db` begin/rollback/commit — follow `handleRestoreTrash`).
- Produces: `POST /api/v1/items/{id}/photos/{photoId}/main` → 204 always (target already first = no-op); 404 for foreign/absent item or photo; 400 invalid id. Renumbering: target → `sort_order` 0, others 1..n-1 preserving current `(sort_order, id)` order. Frontend (Task 9) POSTs exactly this path.

- [ ] **Step 1: Write the failing tests**

```go
func TestMakePhotoMain(t *testing.T) {
	// owner uploads 3 photos (A, B, C => sort 0,1,2); POST main on C -> 204.
	// GET /items list: thumb_key == C's derived thumb key.
	// photos detail order: C, A, B with sort_order 0, 1, 2.
}

func TestMakePhotoMainNoop(t *testing.T) {
	// main on the already-first photo: 204; orders unchanged (0,1,2);
	// UpdateItemPhoto is not invoked (assert via unchanged sort_order values).
}

func TestMakePhotoMainForeignPhoto(t *testing.T) {
	// valid item, nonexistent photo id -> 404 not_found
}

func TestMakePhotoMainDeterministicWithDuplicateSorts(t *testing.T) {
	// three photos all with sort_order 0 (insert via AddItemPhoto directly):
	// main on the LAST by id => new orders: target 0; others keep (sort,id)
	// order => deterministic 1,2. Assert exact resulting orders.
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test ./internal/api/ -run TestMakePhotoMain -v
```

Expected: FAIL — 404 route not found (chi).

- [ ] **Step 3: Implement**

Route in `server.go` next to the other photo routes:

```go
	r.Post("/items/{id}/photos/{photoId}/main", s.handleMakePhotoMain)
```

Handler in `files_handlers.go`:

```go
// handleMakePhotoMain makes the given photo the item's main photo (the one
// lists thumbnail) by renumbering sort_order 0..n-1 with the target first,
// in one transaction. Idempotent: already-first is a 204 no-op.
func (s *Server) handleMakePhotoMain(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	item, ok := s.requireOwnedItem(w, r, u)
	if !ok {
		return
	}
	photoID, err := strconv.ParseInt(chi.URLParam(r, "photoId"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid photo id")
		return
	}
	photos, err := s.q.ListItemPhotos(r.Context(), item.ID) // ordered (sort_order, id)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	idx := -1
	for i := range photos {
		if photos[i].ID == photoID {
			idx = i
			break
		}
	}
	if idx == -1 {
		writeErr(w, http.StatusNotFound, "not_found", "photo not found")
		return
	}

	ordered := make([]dbpkg.ItemPhoto, 0, len(photos))
	ordered = append(ordered, photos[idx])
	for i := range photos {
		if i != idx {
			ordered = append(ordered, photos[i])
		}
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	defer tx.Rollback()
	qtx := s.q.WithTx(tx)
	changed := false
	for i := range ordered {
		if ordered[i].SortOrder == int64(i) {
			continue
		}
		if err := qtx.UpdateItemPhoto(r.Context(), dbpkg.UpdateItemPhotoParams{
			Caption: ordered[i].Caption, SortOrder: int64(i),
			ID: ordered[i].ID, ItemID: item.ID,
		}); err != nil {
			s.internalErr(w, err)
			return
		}
		changed = true
	}
	if err := tx.Commit(); err != nil {
		s.internalErr(w, err)
		return
	}
	_ = changed // orders are the contract; nothing else to report
	w.WriteHeader(http.StatusNoContent)
}
```

(If the existing trash-restore code shows a different tx helper shape, follow that shape exactly; `defer tx.Rollback()` after a successful `Commit` is a no-op by design.)

`docs/api-notes.md` — add to the photos section:

```
POST /items/{id}/photos/{photoId}/main — owner only. Renumbers the item's
photos (sort_order 0..n-1, target first, others keep current (sort_order,
id) order) in one transaction. Always 204 (already-first is a no-op);
404 for foreign/absent item or photo. This changes which photo lists
thumbnail (thumb_key follows (sort_order, id)).
```

- [ ] **Step 4: Run all tests**

```bash
go vet ./... && go test -count=1 ./...
```

Expected: green.

- [ ] **Step 5: Commit**

```bash
git add internal/api/ docs/api-notes.md
git commit -m "feat: POST /items/{id}/photos/{photoId}/main to pick main photo"
```

---

## Batch 2 — Frontend

### Task 8: Desktop table thumbnail column

**Files:**
- Modify: `web/src/views/ItemsView.vue` (desktop table ~98–119)
- Test: `web/src/views/ItemsView.test.ts`

**Interfaces:**
- Consumes: `thumb_key` on `/items` rows (Task 4; already in `web/src/api/types.ts`).
- Produces: no interface changes — visual only.

- [ ] **Step 1: Write the failing test**

In `ItemsView.test.ts` (the existing desktop-table tests show how to assert against the `hidden md:table` markup; happy-dom renders both layouts):

```ts
it('shows a thumbnail image in the desktop table when thumb_key is present', async () => {
  // mock GET /api/v1/items with one row: thumb_key '2026/10/abc.jpg'
  // mount, flush; assert: table querySelector('img[src="/files/2026/10/abc.jpg"]')
  // exists inside the table element (not just the mobile cards)
})
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd web && npx vitest run src/views/ItemsView.test.ts
```

Expected: FAIL — table contains no `img`.

- [ ] **Step 3: Implement**

In `ItemsView.vue` desktop table: add an empty header cell before Name and a thumb cell per row:

```html
<th class="w-12 px-3 py-2"></th>
```

```html
<td class="px-3 py-2">
  <img v-if="i.thumb_key" :src="`/files/${i.thumb_key}`" loading="lazy" alt=""
    class="w-10 h-10 object-cover rounded-lg" />
  <span v-else class="text-slate-400">—</span>
</td>
```

- [ ] **Step 4: Run the view tests**

```bash
cd web && npx vitest run src/views/ItemsView.test.ts
```

Expected: PASS (mobile-card thumbnail tests untouched).

- [ ] **Step 5: Commit**

```bash
git add web/src/views/ItemsView.vue web/src/views/ItemsView.test.ts
git commit -m "feat(web): thumbnail column in desktop items table"
```

---

### Task 9: ★ Make-main control + hero image

**Files:**
- Modify: `web/src/components/PhotoGallery.vue` (+ `PhotoGallery` has no test file today — create `web/src/components/PhotoGallery.test.ts`)
- Modify: `web/src/views/ItemDetailView.vue` (+ its test)
- Modify: `web/src/api/types.ts` (`Photo` gains `thumb_key?: string | null`)

**Interfaces:**
- Consumes: `POST /items/{id}/photos/{photoId}/main` (Task 7), `api.post` (`web/src/api/client.ts`), `item.photos` (Task 4 detail JSON).
- Produces: `PhotoGallery` emits `main: [p: Photo]` in addition to `delete`; `ItemDetailView` exposes `makeMain(p)` and a hero lightbox — no other consumer changes.

- [ ] **Step 1: Write the failing tests**

`web/src/components/PhotoGallery.test.ts`:

```ts
// mount with canEdit + 3 photos; assert: first cell shows ★ badge
// (aria-label "Main photo"); second/third show a "Make main" button;
// clicking it emits 'main' with that photo. canEdit=false renders neither.
```

`ItemDetailView.test.ts` additions:

```ts
it('renders a hero image of the main photo with the full-size key', async () => {
  // item.photos[0].storage_key '2026/10/full.jpg' → hero img src '/files/2026/10/full.jpg'
})

it('★ Make main POSTs to the main endpoint and refetches', async () => {
  // click make-main on second photo → assert POST /api/v1/items/1/photos/12/main
  // and a follow-up GET /api/v1/items/1
})

it('hero click opens the lightbox overlay', async () => { /* click hero → fixed overlay visible */ })
```

- [ ] **Step 2: Run to verify failures**

```bash
cd web && npx vitest run src/components/PhotoGallery.test.ts src/views/ItemDetailView.test.ts
```

- [ ] **Step 3: Implement**

`types.ts`:

```ts
export interface Photo { id: number; storage_key: string; thumb_key?: string | null; caption: string; sort_order: number; created_at: string }
```

`PhotoGallery.vue` — full new template/script (keeps existing delete + lightbox):

```vue
<template>
  <div>
    <div class="grid grid-cols-3 sm:grid-cols-4 gap-2">
      <div v-for="(p, i) in photos" :key="p.id" class="relative group">
        <img :src="`/files/${p.storage_key}`" :alt="p.caption || 'photo'" class="w-full h-24 object-cover rounded-lg cursor-zoom-in"
          @click="selected = p" />
        <span v-if="i === 0" aria-label="Main photo" title="Main photo"
          class="absolute bottom-1 left-1 text-xs bg-slate-900/80 text-white rounded px-1 py-0.5">★</span>
        <button v-if="canEdit && i > 0" @click.stop="emit('main', p)" title="Make main" aria-label="Make main"
          class="absolute bottom-1 right-1 bg-slate-900/80 text-white rounded px-1.5 py-0.5 text-xs min-w-9 min-h-9 inline-flex items-center justify-center">★</button>
        <button v-if="canEdit" @click.stop="emit('delete', p)"
          class="absolute top-1 right-1 bg-red-600 text-white rounded px-1 text-xs opacity-100 md:opacity-0 md:group-hover:opacity-100">✕</button>
      </div>
    </div>
    <div v-if="selected" class="fixed inset-0 z-50 bg-black/80 flex items-center justify-center p-4"
      @click="selected = null">
      <img :src="`/files/${selected.storage_key}`" class="max-h-full max-w-full object-contain" @click.stop />
      <button class="absolute top-4 right-4 text-white text-2xl" @click="selected = null">✕</button>
    </div>
  </div>
</template>
<script setup lang="ts">
import { ref } from 'vue'
import type { Photo } from '../api/types'
defineProps<{ photos: Photo[]; canEdit: boolean }>()
const emit = defineEmits<{ delete: [p: Photo]; main: [p: Photo] }>()
const selected = ref<Photo | null>(null)
</script>
```

`ItemDetailView.vue` — add above the fields section (first element inside the page container, after the header block):

```html
    <img v-if="mainPhoto" :src="`/files/${mainPhoto.storage_key}`" alt="" data-hero
      class="w-full max-h-80 object-contain rounded-xl bg-slate-100 cursor-zoom-in"
      @click="heroZoom = true" />
    <div v-if="heroZoom" class="fixed inset-0 z-50 bg-black/80 flex items-center justify-center p-4" @click="heroZoom = false">
      <img v-if="mainPhoto" :src="`/files/${mainPhoto.storage_key}`" class="max-h-full max-w-full object-contain" @click.stop />
      <button class="absolute top-4 right-4 text-white text-2xl" @click="heroZoom = false">✕</button>
    </div>
```

Script additions:

```ts
const heroZoom = ref(false)
const mainPhoto = computed(() => item.value?.photos?.[0] ?? null)
const mainSaving = ref<Set<number>>(new Set())
async function makeMain(p: Photo) {
  if (mainSaving.value.has(p.id)) return
  mainSaving.value.add(p.id)
  try {
    await api.post(`/api/v1/items/${route.params.id}/photos/${p.id}/main`)
    await fetchItem()
  } catch (e) {
    toast(e instanceof ApiError ? e.message : 'Failed to set main photo', 'error')
  } finally {
    mainSaving.value.delete(p.id)
  }
}
```

Wire the gallery: `<PhotoGallery ... @main="makeMain" />`. Match the file's existing imports (`computed`, `ref`, `api`, `ApiError`, `toast` — follow current usage patterns; import what's missing).

- [ ] **Step 4: Run the tests**

```bash
cd web && npx vitest run && npx vue-tsc -b
```

Expected: all green, typecheck clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/ web/src/views/ItemDetailView.vue web/src/views/ItemDetailView.test.ts web/src/api/types.ts
git commit -m "feat(web): make-main control and hero image on item detail"
```

---

### Task 10: Theme composable, toggle, no-FOUC

**Files:**
- Create: `web/src/composables/theme.ts`
- Create: `web/src/composables/theme.test.ts`
- Modify: `web/index.html` (inline pre-mount script), `web/src/assets/main.css`, `web/src/components/AppShell.vue` (+ `AppShell.test.ts`)

**Interfaces:**
- Consumes: nothing app-side.
- Produces: `useTheme(): { isDark: Ref<boolean>, toggle: () => void }` — singleton state; localStorage key `stasher-theme`; `.dark` class on `<html>`.

- [ ] **Step 1: Write the failing tests**

`web/src/composables/theme.test.ts`:

```ts
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useTheme } from './theme'

function mockMatchMedia(dark: boolean) {
  const listeners: Array<(e: { matches: boolean }) => void> = []
  const mq = {
    matches: dark,
    addEventListener: (_: string, cb: (e: { matches: boolean }) => void) => listeners.push(cb),
    removeEventListener: () => { },
  }
  ;(window as any).matchMedia = vi.fn().mockReturnValue(mq)
  return (dark2: boolean) => listeners.forEach(cb => cb({ matches: dark2 }))
}

describe('useTheme', () => {
  beforeEach(() => { localStorage.clear(); document.documentElement.className = '' })

  it('defaults to system preference', () => {
    mockMatchMedia(true)
    const { isDark } = useTheme()
    expect(isDark.value).toBe(true)
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('toggle persists an explicit choice', () => {
    mockMatchMedia(true)
    const { isDark, toggle } = useTheme()
    toggle()
    expect(isDark.value).toBe(false)
    expect(localStorage.getItem('stasher-theme')).toBe('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('follows live system changes while no explicit choice stored', () => {
    const flip = mockMatchMedia(false)
    const { isDark } = useTheme()
    expect(isDark.value).toBe(false)
    flip(true)
    expect(isDark.value).toBe(true)
  })
})
```

- [ ] **Step 2: Run to verify failure**

```bash
cd web && npx vitest run src/composables/theme.test.ts
```

Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

`web/src/composables/theme.ts`:

```ts
import { ref } from 'vue'

const KEY = 'stasher-theme'
type Mode = 'light' | 'dark'

function storedMode(): Mode | null {
  const v = localStorage.getItem(KEY)
  return v === 'light' || v === 'dark' ? v : null
}

const systemDark = () =>
  typeof matchMedia === 'function' && matchMedia('(prefers-color-scheme: dark)').matches

// singleton state: every consumer (mobile header, desktop sidebar) shares it
const isDark = ref<boolean>(
  storedMode() ? storedMode() === 'dark' : systemDark(),
)

function apply() {
  document.documentElement.classList.toggle('dark', isDark.value)
}
apply()

if (typeof matchMedia === 'function') {
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', e => {
    if (storedMode()) return // explicit choice wins
    isDark.value = e.matches
    apply()
  })
}

export function useTheme() {
  function toggle() {
    isDark.value = !isDark.value
    localStorage.setItem(KEY, isDark.value ? 'dark' : 'light')
    apply()
  }
  return { isDark, toggle }
}
```

`web/index.html` — first thing inside `<head>`, before any stylesheet/script:

```html
    <script>
      (function () {
        try {
          var m = localStorage.getItem('stasher-theme')
          var dark = m ? m === 'dark'
            : window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches
          if (dark) document.documentElement.classList.add('dark')
        } catch (e) { /* private mode: default light */ }
      })()
    </script>
```

`web/src/assets/main.css`:

```css
@import "tailwindcss";

@custom-variant dark (&:where(.dark, .dark *));

:root { color-scheme: light; }
.dark { color-scheme: dark; }
```

`AppShell.vue` — in the mobile header `<nav>` (line ~22, after the admin link if present, else at the start):

```html
        <button @click="theme.toggle()" :aria-label="theme.isDark.value ? 'Switch to light theme' : 'Switch to dark theme'"
          class="inline-flex min-w-11 min-h-11 items-center justify-center">{{ theme.isDark.value ? '☀️' : '🌙' }}</button>
```

In the desktop `<aside>` (line ~4), after the nav links, pushed to the bottom:

```html
      <button @click="theme.toggle()" :aria-label="theme.isDark.value ? 'Switch to light theme' : 'Switch to dark theme'"
        class="mt-auto inline-flex min-w-11 min-h-11 items-center justify-center rounded-lg hover:bg-slate-700">{{ theme.isDark.value ? '☀️' : '🌙' }}</button>
```

(The aside is `flex flex-col`, so `mt-auto` pins it down. Script: `import { useTheme } from '../composables/theme'` + `const theme = useTheme()`.)

Add one `AppShell.test.ts` case: renders a theme toggle button; clicking it adds `dark` to `document.documentElement`.

- [ ] **Step 4: Run the tests**

```bash
cd web && npx vitest run && npx vue-tsc -b
```

Expected: green (existing AppShell tests may pin nav children — update counts if they do).

- [ ] **Step 5: Commit**

```bash
git add web/src/composables/ web/src/components/AppShell.vue web/src/components/AppShell.test.ts web/index.html web/src/assets/main.css
git commit -m "feat(web): dark theme composable, toggle, no-FOUC boot"
```

---

### Task 11: Dark pass over every view + dist rebuild + full verification

**Files:**
- Modify: all of `web/src/views/*.vue` (15 files), `web/src/components/{ConfirmDialog,FileUpload,LocationSelect,Pagination,PhotoGallery,TagInput,Toasts}.vue`
- Modify (build output): `internal/webfs/dist/**` (via `make web`)

**Interfaces:**
- Consumes: `.dark` variant from Task 10.
- Produces: complete dark styling; no interface changes.

Class mapping — apply mechanically, keeping light styles untouched (add `dark:` variants):

| Light class (existing) | Add |
|---|---|
| `bg-white` (cards/panels/tables) | `dark:bg-slate-800` |
| `bg-slate-100` (page background in AppShell main) | `dark:bg-slate-900` |
| `bg-slate-100` (chips, hero backdrop, insets) | `dark:bg-slate-700` |
| `hover:bg-slate-50` | `dark:hover:bg-slate-700/50` |
| `hover:bg-slate-200` | `dark:hover:bg-slate-600` |
| `border` / `border-slate-200/300` | `dark:border-slate-700` |
| `text-slate-500`, `text-slate-600`, `text-slate-700` (secondary text) | `dark:text-slate-300` or `dark:text-slate-400` |
| `text-slate-400` (loading/placeholder) | `dark:text-slate-500` |
| `bg-slate-900 text-white` (primary buttons, "+ New item") | `dark:bg-slate-100 dark:text-slate-900` |
| `bg-gray-200 text-gray-500` (disposed badge) | `dark:bg-slate-700 dark:text-slate-300` |
| `text-red-600` | `dark:text-red-400` |
| `text-green-600`, `text-emerald-600` | `dark:text-green-400`, `dark:text-emerald-400` |
| `bg-slate-900/80` overlays on photos (Task 9) | keep (already dark-adaptive) |
| Login/Register page containers (`bg-…` full-page) | `dark:bg-slate-900` |

Notes: `AppShell`'s sidebar/mobile bars are already `bg-slate-900` — unchanged. Keep status badge backgrounds (red/green/emerald `bg-*`) as-is with a `dark:` text/border tweak only where contrast hurts. Do not touch `manifest.webmanifest`.

- [ ] **Step 1: Apply the pass file by file**

Work through the file list; after every 3–4 files run:

```bash
cd web && npx vitest run 2>&1 | tail -3
```

Fix any test that pinned a class string (prefer updating the assertion over weakening it).

- [ ] **Step 2: Typecheck + build the SPA**

```bash
cd web && npx vue-tsc -b && cd .. && make web
```

- [ ] **Step 3: Full verification (backend + frontend + both smokes)**

```bash
make check && go build -o stasher ./cmd/stasher && ./scripts/smoke-web.sh && (cd web && node scripts/smoke-authed.mjs)
```

Expected: `check: all green`, `smoke ok`, `smoke-authed ok`.

- [ ] **Step 4: Commit (sources + rebuilt dist)**

```bash
git add web/ internal/webfs/dist/
git commit -m "feat(web): dark theme across all views and components"
```

---

## Final wrap-up (controller)

- Run `make check` + both smokes once more on the final tree; confirm `git status` clean (dist committed).
- Update `docs/backlog.md` only if a deferred item turns out to be addressed by this work.
- Push and watch CI.
