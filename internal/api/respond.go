package api

import (
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
)

type errDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errBody struct {
	Error errDetail `json:"error"`
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errBody{Error: errDetail{Code: code, Message: msg}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return false
	}
	return true
}

type listEnvelope struct {
	Data  any   `json:"data"`
	Total int64 `json:"total"`
}

type pageParams struct {
	Page     int64
	PageSize int64
}

func parsePage(r *http.Request) pageParams {
	p := pageParams{Page: 1, PageSize: 50}
	if v, err := strconv.ParseInt(r.URL.Query().Get("page"), 10, 64); err == nil && v > 0 {
		p.Page = v
	}
	if v, err := strconv.ParseInt(r.URL.Query().Get("page_size"), 10, 64); err == nil && v > 0 {
		p.PageSize = v
	}
	if p.PageSize > 200 {
		p.PageSize = 200
	}
	return p
}

func (p pageParams) Limit() int64  { return p.PageSize }
func (p pageParams) Offset() int64 { return (p.Page - 1) * p.PageSize }

func slogError(msg string, err error) { slog.Error(msg, "err", err) }

func sqlNullInt64(i int64) sql.NullInt64 { return sql.NullInt64{Int64: i, Valid: true} }

func sqlNullString(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

func sqlNullFloat64(f float64) sql.NullFloat64 { return sql.NullFloat64{Float64: f, Valid: true} }

// nullString renders a nullable string column for JSON: the string, or an
// explicit null when the column is NULL or empty (empty is not a meaningful
// value for detail-only fields like product_url).
func nullString(ns sql.NullString) any {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	return ns.String
}

// thumbJSON renders a photo's thumb_key for JSON: the derived key, or an
// explicit null when generation failed (list SQL COALESCEs to the original
// storage_key instead; photo objects sit next to their storage_key).
func thumbJSON(ns sql.NullString) any {
	return nullString(ns)
}
