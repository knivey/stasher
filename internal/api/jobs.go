package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
	"stasher/internal/fetcher"
)

// One fetch job runs end-to-end within jobDeadline; it covers BOTH the wait
// for the single-flight semaphore and the fetch itself (direct redirect
// chains have their own 30s per-fetch cap, chromium renders 60s — the job
// deadline is the outer bound the frontend polls against).
const jobDeadline = 90 * time.Second

// jobTTL: finished jobs are pruned from the in-memory map after an hour
// (pruned on each submission, under the mutex).
const jobTTL = time.Hour

// jobState is one in-memory fetch job. Jobs are never persisted: a restart
// abandons running jobs (client polling then 404s → "fetch interrupted").
type jobState struct {
	id     string
	userID int64
	kind   string // "item-doc" | "bookmark-archive"
	itemID int64
	// bookmark-archive target (kind == "bookmark-archive")
	bookmarkProject int64
	bookmarkID      int64
	title           string
	status          string // running | done | failed
	err             string
	docID           int64
	created         time.Time
}

// newJobID: 16 crypto/rand bytes, URL-safe, unambiguous in paths.
func newJobID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

type fetchReq struct {
	URL  string `json:"url"`
	Mode string `json:"mode"`
}

// handleItemFetch queues a fetch job for an owned item (POST
// /items/{id}/fetch). Foreign/absent item → 404 (same as every owned-item
// route); bad URL/mode → 400. The job runs in the background; the response
// is 202 with its id for polling.
func (s *Server) handleItemFetch(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	item, ok := s.requireOwnedItem(w, r, u)
	if !ok {
		return
	}
	var req fetchReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if !validExternalURL(req.URL) {
		writeErr(w, http.StatusBadRequest, "validation_error",
			"url must be an http(s) URL of at most 2000 chars")
		return
	}
	var eng fetcher.Engine
	switch req.Mode {
	case "direct":
		// compare the CONCRETE pointer: a nil *DirectEngine tucked into
		// the Engine interface would look non-nil and crash the job
		if s.fetchDirect == nil {
			writeErr(w, http.StatusServiceUnavailable, "fetching_disabled",
				"fetching is not available on this server")
			return
		}
		eng = s.fetchDirect
	case "archive":
		eng = s.fetchArchive
	default:
		writeErr(w, http.StatusBadRequest, "validation_error", "mode must be direct or archive")
		return
	}
	if eng == nil {
		writeErr(w, http.StatusServiceUnavailable, "fetching_disabled",
			"fetching is not available on this server")
		return
	}

	id, err := newJobID()
	if err != nil {
		s.internalErr(w, err)
		return
	}
	j := &jobState{
		id: id, userID: u.ID, kind: "item-doc", itemID: item.ID,
		status: "running", created: time.Now(),
	}
	s.jobsMu.Lock()
	pruneJobsLocked(s.jobs, time.Now())
	s.jobs[id] = j
	s.jobsMu.Unlock()

	// The job must NOT bind to the request context: it outlives the 202
	// response, and a request ctx would cancel it the moment this handler
	// returns. The job owns its lifecycle; jobDeadline bounds it.
	go s.runJob(context.Background(), j, eng, req.URL)

	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": id})
}

// runJob executes one fetch job: single-flight (one fetch server-wide, later
// jobs queue in "running" state), fetch, then store the result — as an
// ordinary item document (item-doc) or as a bookmark's archived page
// (bookmark-archive). Any failure marks the job failed and touches nothing:
// the item/bookmark and their existing attachments stay clean.
func (s *Server) runJob(base context.Context, j *jobState, eng fetcher.Engine, rawURL string) {
	ctx, cancel := context.WithTimeout(base, jobDeadline)
	defer cancel()

	select {
	case s.fetchSem <- struct{}{}:
		defer func() { <-s.fetchSem }()
	case <-ctx.Done():
		s.failJob(j, "fetch queue timed out")
		return
	}

	res, err := eng.Fetch(ctx, rawURL)
	if err != nil {
		s.failJob(j, err.Error())
		return
	}
	// store like an upload: files pipeline + the kind's row. The write
	// runs past any fetch deadline pressure, hence WithoutCancel.
	storeCtx := context.WithoutCancel(ctx)
	key, size, err := s.st.Save(bytes.NewReader(res.Body), extForContent(res.ContentType))
	if err != nil {
		s.failJob(j, "could not store fetched file")
		return
	}
	switch j.kind {
	case "item-doc":
		doc, err := s.q.AddItemDocument(storeCtx, dbpkg.AddItemDocumentParams{
			ItemID: j.itemID, StorageKey: key, Filename: sanitizeFilename(res.Filename),
			MimeType: res.ContentType, SizeBytes: size,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		})
		if err != nil {
			// row insert failed: roll back the file we already saved
			if derr := s.st.Delete(key); derr != nil {
				slog.Warn("file delete failed", "key", key, "err", derr)
			}
			s.failJob(j, "could not attach document")
			return
		}
		s.jobsMu.Lock()
		j.status, j.docID = "done", doc.ID
		s.jobsMu.Unlock()
	case "bookmark-archive":
		if err := s.completeBookmarkArchive(storeCtx, j, key, size); err != nil {
			// nothing was linked (or the link itself failed and was rolled
			// back): the just-saved bytes are an orphan — remove them
			if derr := s.st.Delete(key); derr != nil {
				slog.Warn("file delete failed", "key", key, "err", derr)
			}
			s.failJob(j, "could not attach archive")
			return
		}
		s.jobsMu.Lock()
		j.status = "done"
		s.jobsMu.Unlock()
	}
}

func (s *Server) failJob(j *jobState, msg string) {
	s.jobsMu.Lock()
	j.status = "failed"
	j.err = msg
	s.jobsMu.Unlock()
}

// pruneJobsLocked drops finished jobs older than jobTTL. Caller holds
// jobsMu; running jobs are never pruned (they hold the only pointers that
// matter and would resurface as done/failed anyway).
func pruneJobsLocked(m map[string]*jobState, now time.Time) {
	for id, j := range m {
		if j.status != "running" && now.Sub(j.created) > jobTTL {
			delete(m, id)
		}
	}
}

// handleGetJob reports a job's status (GET /jobs/{id}). Owner-scoped: another
// user's (or unknown) id is 404, never 403 — no existence leak.
func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id := chi.URLParam(r, "id")
	s.jobsMu.Lock()
	j, ok := s.jobs[id]
	if ok && j.userID != u.ID {
		ok = false
	}
	var snap jobState
	if ok {
		snap = *j // copy under the lock: the goroutine may still mutate it
	}
	s.jobsMu.Unlock()
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	body := map[string]any{"status": snap.status}
	if snap.err != "" {
		body["error"] = snap.err
	}
	if snap.docID != 0 {
		body["document_id"] = snap.docID
	}
	writeJSON(w, http.StatusOK, body)
}

// extForContent maps a fetch result's (allowlisted, sniffed) content type to
// a storage extension. Extends the photo-type table with the fetch-specific
// pdf/text types; the shared default stays "bin".
func extForContent(ct string) string {
	switch ct {
	case "application/pdf":
		return "pdf"
	case "text/plain", "text/plain; charset=utf-8":
		return "txt"
	}
	return extForMime(ct)
}
