package api

import (
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"stasher/internal/ai"
)

// Autofill limits (spec §4): 1–4 image parts, each ≤ maxPhotoBytes (20MB,
// the photo cap), whole request bounded at 4× that by the MaxBytesReader.
const (
	maxAutofillParts        = 4
	maxAutofillRequestBytes = 4 * maxPhotoBytes
)

// handleAutofill implements POST /api/v1/autofill: 1–4 multipart "files"
// images → AI-proposed item fields, clamped server-side, nothing stored.
//
// Check order (pinned by tests + docs/api-notes.md): multipart parse and
// part validation (400/415) → per-user rate limit (429) → configured check
// (ai_not_configured) → the provider call. Cheap local checks all reject
// before any provider traffic, and rejected requests never burn a rate
// token.
func (s *Server) handleAutofill(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, maxAutofillRequestBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_upload", "bad multipart body or file too large")
		return
	}
	headers := r.MultipartForm.File["files"]
	if len(headers) == 0 {
		if len(r.MultipartForm.Value["files"]) > 0 {
			// the field exists but carries plain values, not uploads
			writeErr(w, http.StatusBadRequest, "missing_file", "images must be sent as file parts under \"files\"")
			return
		}
		writeErr(w, http.StatusBadRequest, "validation_error", "provide 1 to 4 images as \"files\" file parts")
		return
	}
	if len(headers) > maxAutofillParts {
		writeErr(w, http.StatusBadRequest, "validation_error", "too many images; at most 4")
		return
	}

	images := make([][]byte, 0, len(headers))
	mimes := make([]string, 0, len(headers))
	for _, fh := range headers {
		if fh.Size > maxPhotoBytes {
			writeErr(w, http.StatusBadRequest, "invalid_upload", "bad multipart body or file too large")
			return
		}
		f, err := fh.Open()
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_upload", "bad upload body")
			return
		}
		head := make([]byte, 512)
		// io.ReadFull instead of a bare Read: one short read is fine (small
		// files), but an actual read error must not silently produce a
		// truncated sniff buffer (same hardening as the photo path)
		n, err := io.ReadFull(f, head)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			f.Close()
			writeErr(w, http.StatusBadRequest, "invalid_upload", "bad upload body")
			return
		}
		head = head[:n]
		// sniffed type only — the client's part Content-Type is never
		// trusted; empty parts sniff as text/plain and land here too
		ct := http.DetectContentType(head)
		if !allowedPhotoTypes[ct] {
			f.Close()
			writeErr(w, http.StatusUnsupportedMediaType, "unsupported_type",
				"images must be jpeg, png, webp, or gif")
			return
		}
		// buffer the rest (bounded: the MaxBytesReader caps the whole
		// request) — images pass whole to the AI client
		rest, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_upload", "bad multipart body or file too large")
			return
		}
		images = append(images, append(head, rest...))
		mimes = append(mimes, ct)
	}

	if !s.aiRate.Allow("ai:" + strconv.FormatInt(u.ID, 10)) {
		writeErr(w, http.StatusTooManyRequests, "rate_limited",
			"too many autofill requests; try again in a few seconds")
		return
	}
	if s.aiClient == nil || !s.aiClient.Enabled() {
		writeErr(w, http.StatusBadRequest, "ai_not_configured", aiNotConfiguredMessage(s.aiClient))
		return
	}

	fields, err := s.aiClient.Autofill(r.Context(), images, mimes)
	if err != nil {
		// ai.ErrUnparseable marks provider ANSWERS we can't use (model
		// reply not JSON, or no content) — a 400-class problem. Everything
		// else (non-2xx, timeout, oversized or non-JSON response envelope)
		// maps to a generic 502. Client errors never carry provider bodies,
		// so both messages stay generic regardless.
		if errors.Is(err, ai.ErrUnparseable) {
			slog.Warn("ai autofill: unparseable model response", "err", err)
			writeErr(w, http.StatusBadRequest, "ai_bad_response",
				"AI provider returned an unparseable response")
			return
		}
		// *ai.HTTPError carries a truncated provider-body detail: log it
		// server-side (operators need the real 400 reason), but it never
		// reaches the client — the 502 message below stays generic.
		attrs := []any{"err", err}
		var httpErr *ai.HTTPError
		if errors.As(err, &httpErr) {
			attrs = append(attrs, "provider_detail", httpErr.ErrorDetail())
		}
		slog.Warn("ai autofill failed", attrs...)
		writeErr(w, http.StatusBadGateway, "ai_upstream_error", "AI provider request failed")
		return
	}
	writeJSON(w, http.StatusOK, clampAIFields(fields))
}

// aiNotConfiguredMessage names exactly which config keys are missing, so
// the operator (and the toasting frontend) can act on the message.
func aiNotConfiguredMessage(c *ai.Client) string {
	missing := make([]string, 0, 2)
	if c == nil || c.APIKey == "" {
		missing = append(missing, "ai_api_key")
	}
	if c == nil || c.Model == "" {
		missing = append(missing, "ai_model")
	}
	return "set " + strings.Join(missing, " and ") + " in config to enable autofill"
}

// clampAIFields applies the spec §4 clamps — AI output is clamped, never
// rejected — and omits every key without a usable value: the response only
// carries fields the model effectively returned. Caps are measured in
// bytes; the name cap matches the item validator (so a clamped prefill
// always survives item create), while description (10,000) and unit (50)
// follow the spec's numbers — item create enforces no length on those.
// Truncation never splits a UTF-8 rune.
// Tags are NOT clamped but dropped wholesale — see below.
func clampAIFields(f ai.Fields) map[string]any {
	out := map[string]any{}
	if name := strings.TrimSpace(f.Name); name != "" {
		out["name"] = truncateUTF8(name, 200)
	}
	if d := truncateUTF8(f.Description, 10_000); d != "" {
		out["description"] = d
	}
	if f.Qty != nil {
		// NaN/Inf can't survive JSON decoding; the guard is defense-in-depth
		if q := *f.Qty; !math.IsNaN(q) && !math.IsInf(q, 0) && q >= 0 && q <= 1e12 {
			out["qty"] = q
		}
	}
	if u := truncateUTF8(f.Unit, 50); u != "" {
		out["unit"] = u
	}
	// Tags are dropped unconditionally: tag suggestions were disabled by
	// operator feedback on 2026-10-24 (they came back noisy/meaningless).
	// Models volunteer unrequested fields, so even though the prompt no
	// longer asks for tags, anything the provider returns in f.Tags never
	// reaches the response — the key is absent, not empty. Re-enable =
	// restore the tags request in the ai system prompt AND remove this
	// strip.
	_ = f.Tags
	return out
}

// truncateUTF8 cuts s to at most limit bytes without splitting a rune
// (backs off to the preceding rune boundary when the cut lands mid-rune).
func truncateUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}
