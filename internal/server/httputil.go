package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// httpError is an error with an HTTP status that is safe to show to the client.
type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

func errStatus(status int, format string, args ...any) error {
	return &httpError{status: status, msg: fmt.Sprintf(format, args...)}
}

func badRequest(format string, args ...any) error {
	return errStatus(http.StatusBadRequest, format, args...)
}

var (
	errNotFound     = errStatus(http.StatusNotFound, "not found")
	errUnauthorized = errStatus(http.StatusUnauthorized, "not signed in")
	errForbidden    = errStatus(http.StatusForbidden, "forbidden")
)

// handlerFunc is an http handler that may fail; errors are rendered as JSON.
type handlerFunc func(w http.ResponseWriter, r *http.Request) error

func (s *Server) wrap(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := h(w, r)
		if err == nil {
			return
		}
		var he *httpError
		if errors.As(err, &he) {
			writeJSON(w, he.status, map[string]string{"error": he.msg})
			return
		}
		s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter, v any) error {
	return writeJSON(w, http.StatusOK, v)
}

// readJSON decodes a small JSON request body into v.
func readJSON(r *http.Request, v any) error {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		// Also acts as CSRF protection: cross-site forms cannot send JSON
		// without a CORS preflight, which we never approve.
		return errStatus(http.StatusUnsupportedMediaType, "expected application/json")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return badRequest("invalid JSON body: %v", err)
	}
	return nil
}

func pathInt(r *http.Request, name string) (int64, error) {
	v, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		return 0, errNotFound
	}
	return v, nil
}

func queryInt(r *http.Request, name string, fallback int64) int64 {
	if v, err := strconv.ParseInt(r.URL.Query().Get(name), 10, 64); err == nil {
		return v
	}
	return fallback
}

func nowMs() int64 { return time.Now().UnixMilli() }

// timeRange is a dashboard query window with a bucket size for time series.
type timeRange struct {
	From, To, Bucket int64 // ms
	Label            string
}

var rangePresets = map[string]struct{ span, bucket time.Duration }{
	"1h":  {time.Hour, time.Minute},
	"6h":  {6 * time.Hour, 5 * time.Minute},
	"24h": {24 * time.Hour, 15 * time.Minute},
	"7d":  {7 * 24 * time.Hour, 2 * time.Hour},
	"30d": {30 * 24 * time.Hour, 12 * time.Hour},
	"90d": {90 * 24 * time.Hour, 24 * time.Hour},
}

func parseRange(r *http.Request, fallback string) timeRange {
	label := r.URL.Query().Get("range")
	p, found := rangePresets[label]
	if !found {
		label = fallback
		p = rangePresets[fallback]
	}
	bucket := p.bucket.Milliseconds()
	to := nowMs()
	// Align to bucket boundaries so the series doesn't shift on every refresh.
	to = (to/bucket + 1) * bucket
	return timeRange{From: to - p.span.Milliseconds(), To: to, Bucket: bucket, Label: label}
}

// Points returns the bucket start times covering the range.
func (t timeRange) Points() []int64 {
	n := (t.To - t.From) / t.Bucket
	out := make([]int64, n)
	for i := range out {
		out[i] = t.From + int64(i)*t.Bucket
	}
	return out
}

// series is a dense time series: one value per bucket in a timeRange.
type series struct {
	Name   string    `json:"name"`
	Values []float64 `json:"values"`
}

type timeseries struct {
	Bucket int64    `json:"bucket"`
	Points []int64  `json:"points"`
	Series []series `json:"series"`
}

func newTimeseries(t timeRange, names ...string) *timeseries {
	ts := &timeseries{Bucket: t.Bucket, Points: t.Points()}
	for _, n := range names {
		ts.Series = append(ts.Series, series{Name: n, Values: make([]float64, len(ts.Points))})
	}
	return ts
}

// set writes v at the bucket containing ms for series i; out-of-range is ignored.
func (ts *timeseries) set(i int, ms int64, v float64) {
	if len(ts.Points) == 0 {
		return
	}
	idx := (ms - ts.Points[0]) / ts.Bucket
	if idx >= 0 && idx < int64(len(ts.Points)) {
		ts.Series[i].Values[idx] = v
	}
}

func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			ip, _, _ := strings.Cut(xff, ",")
			return strings.TrimSpace(ip)
		}
	}
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return strings.Trim(host, "[]")
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func requestLogger(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		defer func() {
			if p := recover(); p != nil {
				log.Error("panic", "path", r.URL.Path, "panic", p)
				http.Error(rec, "internal server error", http.StatusInternalServerError)
			}
			if strings.HasPrefix(r.URL.Path, "/api/") {
				log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", rec.status, "dur", time.Since(start))
			}
		}()
		next.ServeHTTP(rec, r)
	})
}
