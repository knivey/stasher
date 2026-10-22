# Thumbnails, Main-Photo Picker, Desktop Thumbs, Dark Theme

**Date:** 2026-10-22
**Status:** Approved design (pre-implementation)

## 1. Purpose

Four interlocking gaps found during first real deployment use:

1. **"Thumbnails" are full images.** `thumb_key` on list endpoints is literally
   the first photo's `storage_key`. Lists render multi-MB originals in 96px
   cards — expensive on mobile data, slow lists.
2. **The desktop items table has no thumbnail column** — only a `📷 N` count.
   Browsing on desktop, no picture appears in any list.
3. **No way to choose which photo represents an item.** The thumb is the
   photo with the lowest `(sort_order, id)` — implicitly the first uploaded.
4. **No dark theme.**

Success criteria:

- List pages transfer small derived images (tens of KB), never originals —
  except as an explicit fallback when thumb generation failed for a photo.
- An owner can set any photo as the item's main photo from the gallery;
  lists and the new hero image reflect it immediately.
- The desktop items table shows a leading 40px thumbnail.
- The app renders dark when the OS is dark; a toggle overrides and
  persists; no flash of light theme while loading.

## 2. Thumbnail pipeline (backend)

### New package `internal/thumbs`

```go
func Generate(src []byte) (thumb []byte, ext string, err error)
```

- Decode via `image.Decode` with stdlib `jpeg`/`png`/`gif` and
  `golang.org/x/image/webp` (decode-only, pure Go) registered.
- **JPEG sources: apply EXIF orientation before resizing.** Browsers render
  originals with `image-orientation: from-image`, so a thumb that ignores
  EXIF would appear rotated relative to its original. Read the tag with
  `github.com/rwcarlsen/goexif`; rotate/flip via small helper transforms.
  EXIF parse failure → no rotation (rare, degraded, documented).
- Resize with `x/image/draw` (CatmullRom) so the **longest side ≤ 480px**;
  never upscale. Animated GIF → first frame only.
- Re-encode by source format: jpeg → JPEG (quality 78); webp → JPEG
  (WebP has no stdlib encoder; alpha-loss acceptable for photos, noted);
  png → PNG (alpha-safe); gif → GIF (alpha-safe).
- Any error returns err — callers fall back, never fail the request.

### Upload path (`handlePhotoUpload`)

Buffer the upload (already size-limited) in memory instead of streaming:
save the original from the buffer, `thumbs.Generate`, save the thumb via
the same `Storage.Save` sharding, insert the row with both keys. Thumb
save/generate failure logs a warning and leaves `thumb_key` null — the
photo itself still uploads.

### Schema — migration `00004_photos_thumb_key.sql`

```sql
ALTER TABLE item_photos ADD COLUMN thumb_key TEXT; -- nullable, no default
```

### Backfill (startup)

After migrations, a **background goroutine** (does not block serving)
selects photos with `thumb_key IS NULL` in id order: `Storage.Open` the
original, generate, save, update the row. Logs start/completion with
count. Per-photo failure leaves null (fallback covers). This upgrades
existing deployments' photos on first boot.

### List endpoints

Every `thumb_key` expression becomes `COALESCE(p.thumb_key, p.storage_key)`:
`buildItemListSQL` (items), the `/shared` list query, and the trash
listing (from snapshot). Old/failed photos keep today's behavior — the
full image — as fallback.

### Lifecycle

- **Photo delete**: best-effort `Storage.Delete(thumb_key)` alongside the
  original (existing pattern).
- **Trash purge**: snapshot struct gains `ThumbKey`; purge deletes both
  keys. Pre-migration snapshots have null → skip thumb delete.
- **Trash restore**: recreate the photo row with `thumb_key` from the
  snapshot (files are never moved, keys unchanged).
- **Item copy** (`handleCopyItem`): `Storage.Copy` the thumb file too and
  set the copy's `thumb_key`.

### Serving & config

Unchanged — thumbs are ordinary files behind the same owner-or-shared
`/files/{key}` gate, inline image policy, nosniff. No new config knobs
(fixed 480px; YAGNI).

### New dependencies (both pure Go)

`golang.org/x/image`, `github.com/rwcarlsen/goexif`.

## 3. Main-photo picker

### API

`POST /api/v1/items/{id}/photos/{photoId}/main` — owner only; foreign or
absent item/photo → 404 (isolation convention). In one transaction:
renumber the item's photos 0..n-1 with the target first (others keep
relative order). Target already first → 204 no-op. Returns **204** always.
The existing photo PATCH (caption/sort_order) is unchanged.

### Frontend

- **PhotoGallery**: owner-only ★ control — each photo gets "Make main";
  the current main shows a filled ★ badge. Click → POST → refetch item.
- **ItemDetailView hero**: the main photo rendered large (full-size
  `/files/{storage_key}`, **not** the thumb), `object-contain`, capped
  height, click → existing lightbox. Shown above the fields, mobile and
  desktop. Shared/public viewers see the hero too (read-only).

## 4. Desktop table thumbnails

`ItemsView` desktop table gains a leading 40×40 rounded thumbnail column
(`loading="lazy"`, `thumb_key` when present). The `📷 N` count column
stays. Mobile cards already render thumbs and are unchanged.

## 5. Dark theme

- **Mechanism**: Tailwind v4 class-based — `@custom-variant dark
  (&:where(.dark, .dark *));` in `main.css`; `.dark` on `<html>`;
  `:root { color-scheme: light }` / `.dark { color-scheme: dark }` for
  native controls.
- **State** — composable `web/src/composables/theme.ts`: localStorage
  `stasher-theme` = `'light' | 'dark' | absent`; absent → follow
  `prefers-color-scheme` with a live listener; `toggle()` flips the
  effective mode and persists the explicit choice.
- **No FOUC**: inline `<script>` in `index.html` `<head>` sets `dark`
  from localStorage/matchMedia before the bundle loads.
- **UI**: 🌙/☀️ toggle in the AppShell mobile header and desktop sidebar
  footer (44px targets). Login/Register included in the dark pass.
- **Palette**: slate-800/900 surfaces, slate-700 borders, slate-300/400
  secondary text, primary buttons invert (bg-slate-100/text-slate-900);
  status/badge colors get dark equivalents. Mechanical `dark:` pass over
  all views and components.
- `manifest.webmanifest` theme_color unchanged (PWA chrome only).

## 6. Error & fallback semantics

- Thumb generation/copy failure never blocks upload, restore, or copy:
  `thumb_key` stays null, `COALESCE` serves the original.
- Old trash snapshots without `ThumbKey`: purge skips the thumb delete,
  restore inserts null.
- EXIF parse failure: no rotation applied (rare, documented degradation).

## 7. Testing

- **Backend**: thumbs unit tests (format mapping, ≤480px, no upscale,
  EXIF orientation 6 rotates, corrupt input errors); handler test (upload
  produces two files + row with thumb key; lists return the thumb key);
  backfill test (null → filled; corrupt stays null); make-main endpoint
  tests (reorder, no-op, foreign 404); purge/restore/copy carry thumbs.
- **Frontend**: ★ make-main flow, hero render, desktop thumb column,
  theme composable (system-follow default, toggle persists, live system
  change); existing tests updated where markup changed.
- Both smokes stay green.

## 8. Out of scope

WebP thumb encoding (needs a non-stdlib encoder), configurable thumb size,
drag-to-reorder gallery, hero on the shared view, thumb regeneration on
demand, manifest theme_color switching.

## 9. Docs

`docs/api-notes.md`: make-main endpoint; `thumb_key` is now the derived
thumb with COALESCE-original fallback. AGENTS.md architecture notes if
package layout changes.

## 10. Implementation

Two subagent batches with mandatory reviews (AGENTS.md): (1) backend —
thumbs package, migration, upload/backfill, lifecycle, make-main; (2)
frontend — desktop column, ★ picker + hero, dark theme. Dist rebuilt and
committed; `make check` + smokes green before push.
