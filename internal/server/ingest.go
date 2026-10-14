package server

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/workshell/workshell/internal/pipeline"
	"github.com/workshell/workshell/internal/secure"
)

// envelope is the batch format the Luau SDK POSTs to /api/v1/ingest.
type envelope struct {
	SDK struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"sdk"`
	Server struct {
		JobID        string  `json:"jobId"`
		PlaceID      flexInt `json:"placeId"`
		PlaceVersion any     `json:"placeVersion"`
		Release      string  `json:"release"`
		ServerType   string  `json:"serverType"`
		Environment  string  `json:"environment"`
		MaxPlayers   flexInt `json:"maxPlayers"`
	} `json:"server"`
	Items []item `json:"items"`
}

// item is the union of every telemetry type; Type selects which fields apply.
type item struct {
	Type      string  `json:"type"`
	TS        flexInt `json:"ts"` // Unix ms; clamped server-side
	UserID    flexInt `json:"userId"`
	SessionID string  `json:"sessionId"`

	// error, log
	Level   string `json:"level"`
	Message string `json:"message"`

	// error
	Stack       string          `json:"stack"`
	Script      string          `json:"script"`
	Side        string          `json:"side"`
	Count       flexInt         `json:"count"`
	Fingerprint string          `json:"fingerprint"`
	Tags        json.RawMessage `json:"tags"`
	Context     json.RawMessage `json:"context"`
	Breadcrumbs json.RawMessage `json:"breadcrumbs"`

	// log, span, session_*
	Source string          `json:"source"`
	Attrs  json.RawMessage `json:"attrs"`

	// event, metric, span
	Name  string          `json:"name"`
	Value *float64        `json:"value"`
	Props json.RawMessage `json:"props"`

	// session_start, session_update
	Platform string `json:"platform"`
	Country  string `json:"country"`

	// heartbeat
	Players     flexInt `json:"players"`
	FPS         float64 `json:"fps"`
	HeartbeatMs float64 `json:"heartbeatMs"`
	MemoryMb    float64 `json:"memoryMb"`
	PingMs      float64 `json:"pingMs"`
	Instances   flexInt `json:"instances"`
	SendKbps    float64 `json:"sendKbps"`
	RecvKbps    float64 `json:"recvKbps"`

	// metric (pre-aggregated by the SDK per flush window)
	Kind string  `json:"kind"`
	Sum  float64 `json:"sum"`
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
	Last float64 `json:"last"`

	// span
	DurationMs float64 `json:"durationMs"`
	Status     string  `json:"status"`
	TraceID    string  `json:"traceId"`
	SpanID     string  `json:"spanId"`
	Parent     string  `json:"parent"`

	// detection
	Severity string          `json:"severity"`
	Action   string          `json:"action"`
	Details  json.RawMessage `json:"details"`
}

const maxItemsPerBatch = 5000

// flexInt decodes any JSON number (including exponent forms such as 1.7e+12,
// which Luau's JSON encoder can produce for large values) or numeric string.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("invalid number %q", s)
	}
	*f = flexInt(v)
	return nil
}

// ingestProject is the subset of project settings ingestion needs, cached.
type ingestProject struct {
	ID               int64
	Name             string
	SigningSecret    string
	RequireSignature bool
	HashUserIDs      bool
	HashKey          []byte
	IngestRate       float64
	ClientRate       float64
}

type keyCache struct {
	mu      sync.Mutex
	entries map[string]keyCacheEntry
}

type keyCacheEntry struct {
	p       *ingestProject // nil = known-invalid key
	expires time.Time
}

var ingestKeys = keyCache{entries: map[string]keyCacheEntry{}}

func (s *Server) invalidateKeyCache() {
	ingestKeys.mu.Lock()
	clear(ingestKeys.entries)
	ingestKeys.mu.Unlock()
}

const ingestProjectQuery = `SELECT id, name, signing_secret, require_signature, hash_user_ids, user_hash_secret,
	ingest_rate, client_rate FROM projects WHERE `

func (s *Server) loadIngestProject(ctx context.Context, where string, arg any) (*ingestProject, error) {
	var p ingestProject
	var signing, hashSecret string
	err := s.db.R.QueryRowContext(ctx, ingestProjectQuery+where, arg).Scan(&p.ID, &p.Name, &signing,
		&p.RequireSignature, &p.HashUserIDs, &hashSecret, &p.IngestRate, &p.ClientRate)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.SigningSecret = s.box.MustOpen(signing)
	p.HashKey = []byte(s.box.MustOpen(hashSecret))
	return &p, nil
}

func (s *Server) cachedProject(ctx context.Context, cacheKey, where string, arg any) (*ingestProject, error) {
	ingestKeys.mu.Lock()
	e, found := ingestKeys.entries[cacheKey]
	ingestKeys.mu.Unlock()
	if found && time.Now().Before(e.expires) {
		return e.p, nil
	}
	p, err := s.loadIngestProject(ctx, where, arg)
	if err != nil {
		return nil, err
	}
	ingestKeys.mu.Lock()
	if len(ingestKeys.entries) > 10000 {
		clear(ingestKeys.entries)
	}
	ingestKeys.entries[cacheKey] = keyCacheEntry{p: p, expires: time.Now().Add(30 * time.Second)}
	ingestKeys.mu.Unlock()
	return p, nil
}

func (s *Server) projectForKey(ctx context.Context, key string) (*ingestProject, error) {
	return s.cachedProject(ctx, "k:"+key, "ingest_key = ?", key)
}

func (s *Server) projectByID(ctx context.Context, id int64) (*ingestProject, error) {
	return s.cachedProject(ctx, "id:"+strconv.FormatInt(id, 10), "id = ?", id)
}

// authenticateIngest checks the ingest key and, if configured or present, the
// HMAC request signature. Returns the project and the decoded body.
func (s *Server) authenticateIngest(r *http.Request) (*ingestProject, []byte, error) {
	key := r.Header.Get("X-Workshell-Key")
	if key == "" {
		return nil, nil, errStatus(http.StatusUnauthorized, "missing X-Workshell-Key header")
	}
	p, err := s.projectForKey(r.Context(), key)
	if err != nil {
		return nil, nil, err
	}
	if p == nil {
		return nil, nil, errStatus(http.StatusUnauthorized, "invalid ingest key")
	}
	body, err := decodeBody(r, s.cfg.MaxIngestBytes)
	if err != nil {
		return nil, nil, err
	}
	if err := verifySignature(r, p, body); err != nil {
		return nil, nil, err
	}
	return p, body, nil
}

// verifySignature checks X-Workshell-Signature: v1=hex(HMAC-SHA256(secret,
// timestamp + "." + body)), with X-Workshell-Timestamp within 5 minutes, so a
// leaked ingest key alone can't be used to forge or replay data.
func verifySignature(r *http.Request, p *ingestProject, body []byte) error {
	sig := r.Header.Get("X-Workshell-Signature")
	if sig == "" {
		if p.RequireSignature {
			return errStatus(http.StatusUnauthorized, "this project requires signed requests")
		}
		return nil
	}
	if p.SigningSecret == "" {
		return nil // signatures aren't configured; ignore
	}
	tsHeader := r.Header.Get("X-Workshell-Timestamp")
	ts, err := strconv.ParseInt(tsHeader, 10, 64)
	if err != nil || math.Abs(float64(time.Now().Unix()-ts)) > 300 {
		return errStatus(http.StatusUnauthorized, "missing or stale X-Workshell-Timestamp")
	}
	want := secure.HMACHex([]byte(p.SigningSecret), append([]byte(tsHeader+"."), body...))
	got := strings.TrimPrefix(sig, "v1=")
	if !hmac.Equal([]byte(want), []byte(strings.ToLower(got))) {
		return errStatus(http.StatusUnauthorized, "invalid request signature")
	}
	return nil
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) error {
	p, body, err := s.authenticateIngest(r)
	if err != nil {
		return err
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return badRequest("invalid JSON: %v", err)
	}
	n := len(env.Items)
	if n > maxItemsPerBatch {
		return badRequest("too many items in one batch (max %d)", maxItemsPerBatch)
	}
	rate := max(p.IngestRate, 10)
	if !s.limits.ingest.take(strconv.FormatInt(p.ID, 10), float64(n), rate*10, rate) {
		w.Header().Set("Retry-After", "10")
		return errStatus(http.StatusTooManyRequests, "ingest rate limit exceeded (%d items/s); retry later", int(rate))
	}

	job := make([]byte, 16+len(body))
	binary.BigEndian.PutUint64(job, uint64(p.ID))
	binary.BigEndian.PutUint64(job[8:], uint64(nowMs()))
	copy(job[16:], body)
	if err := s.queue.Enqueue(r.Context(), job); err != nil {
		if errors.Is(err, pipeline.ErrFull) {
			w.Header().Set("Retry-After", "5")
			return errStatus(http.StatusServiceUnavailable, "ingest queue is full; retry later")
		}
		return err
	}
	return writeJSON(w, http.StatusAccepted, map[string]any{"accepted": n})
}

// processJobs is the queue consumer: it writes each batch to the database,
// then publishes live updates and triggers alerts.
func (s *Server) processJobs(ctx context.Context, jobs [][]byte) {
	for _, job := range jobs {
		if len(job) < 16 {
			continue
		}
		pid := int64(binary.BigEndian.Uint64(job))
		received := int64(binary.BigEndian.Uint64(job[8:]))
		p, err := s.projectByID(ctx, pid)
		if err != nil || p == nil {
			continue // project deleted after the batch was queued
		}
		var env envelope
		if err := json.Unmarshal(job[16:], &env); err != nil {
			continue
		}
		res, err := s.ingest(ctx, p, &env, received)
		if err != nil {
			s.log.Error("ingest batch failed", "project", pid, "err", err)
			continue
		}
		if len(res.live) > 0 {
			if b, err := json.Marshal(res.live); err == nil {
				s.broker.Publish(pid, b)
			}
		}
		for _, t := range res.triggers {
			s.alerts.trigger(ctx, pid, t)
		}
	}
}

// decodeBody reads a request body, transparently un-gzipping it. Roblox's
// HttpService:PostAsync(..., compress=true) sends gzip bodies.
func decodeBody(r *http.Request, limit int64) ([]byte, error) {
	br := bufio.NewReader(io.LimitReader(r.Body, limit+1))
	var reader io.Reader = br
	magic, _ := br.Peek(2)
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") || (len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b) {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, badRequest("invalid gzip body")
		}
		defer gz.Close()
		reader = gz
	}
	b, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, badRequest("could not read body: %v", err)
	}
	if int64(len(b)) > limit {
		return nil, errStatus(http.StatusRequestEntityTooLarge, "body exceeds %d bytes", limit)
	}
	return b, nil
}

// truncate cuts s to n bytes (on a UTF-8 boundary) and strips control
// characters other than newline and tab, which have no business in telemetry.
func truncate(s string, n int) string {
	if len(s) > n {
		s = s[:n]
		for !utf8.ValidString(s) && len(s) > 0 {
			s = s[:len(s)-1]
		}
		s += "…"
	}
	if strings.IndexFunc(s, isBadControl) >= 0 {
		s = strings.Map(func(r rune) rune {
			if isBadControl(r) {
				return -1
			}
			return r
		}, s)
	}
	return strings.ToValidUTF8(s, "")
}

func isBadControl(r rune) bool {
	return (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}

// jsonOr returns raw if it is valid JSON of the same kind (object or array)
// as def, else def. It also caps the size.
func jsonOr(raw json.RawMessage, def string, limit int) string {
	t := strings.TrimSpace(string(raw))
	if t == "" || t == "null" || len(t) > limit {
		return def
	}
	if t[0] == def[0] && json.Valid(raw) {
		return t
	}
	return def
}

func oneOf(v string, allowed ...string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return allowed[0]
}

func oneOfDefault(v, def string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return def
	}
	return v
}

func placeVersionString(v any) string {
	switch x := v.(type) {
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case string:
		return truncate(x, 64)
	}
	return ""
}

func nullableUser(id int64) any {
	if id <= 0 {
		return nil
	}
	return id
}

// liveMsg is one update streamed to dashboard viewers.
type liveMsg struct {
	T string `json:"t"`
	D any    `json:"d"`
}

// trigger is an alertable occurrence found during ingestion.
type trigger struct {
	Kind    string // new_issue | regression | detection
	IssueID int64
	Title   string
	Detail  string
	Release string
}

type ingestResult struct {
	live     []liveMsg
	triggers []trigger
}

// ingest writes one batch in a single transaction.
func (s *Server) ingest(ctx context.Context, p *ingestProject, env *envelope, received int64) (*ingestResult, error) {
	now := nowMs()
	jobID := truncate(strings.TrimSpace(env.Server.JobID), 64)
	if jobID == "" {
		jobID = "unknown"
	}
	placeVersion := placeVersionString(env.Server.PlaceVersion)
	release := truncate(strings.TrimSpace(env.Server.Release), 64)
	if release == "" {
		release = placeVersion
	}
	environment := truncate(oneOfDefault(env.Server.Environment, "production"), 32)
	serverType := oneOf(env.Server.ServerType, "public", "private", "reserved", "studio")

	res := &ingestResult{}
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		// Every batch counts as proof of life for its server.
		_, err := tx.ExecContext(ctx, `
			INSERT INTO game_servers (project_id, job_id, place_id, place_version, release, environment, server_type, max_players, started_at, last_seen)
			VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?9)
			ON CONFLICT (project_id, job_id) DO UPDATE SET
				last_seen = MAX(last_seen, excluded.last_seen),
				place_version = excluded.place_version,
				release = excluded.release,
				max_players = MAX(max_players, excluded.max_players),
				closed_at = NULL`,
			p.ID, jobID, env.Server.PlaceID, placeVersion, release, environment, serverType, env.Server.MaxPlayers, received)
		if err != nil {
			return err
		}
		if release != "" {
			_, err = tx.ExecContext(ctx, `
				INSERT INTO releases (project_id, version, place_version, created_at, first_seen, last_seen)
				VALUES (?1, ?2, ?3, ?4, ?4, ?4)
				ON CONFLICT (project_id, version) DO UPDATE SET
					first_seen = COALESCE(first_seen, excluded.first_seen),
					last_seen = MAX(COALESCE(last_seen, 0), excluded.last_seen),
					place_version = CASE WHEN place_version = '' THEN excluded.place_version ELSE place_version END`,
				p.ID, release, placeVersion, received)
			if err != nil {
				return err
			}
		}

		w := &batchWriter{s: s, ctx: ctx, tx: tx, p: p, jobID: jobID, placeVersion: placeVersion, release: release, environment: environment, res: res}
		defer w.close()
		for i := range env.Items {
			it := &env.Items[i]
			if int64(it.TS) <= 0 || int64(it.TS) > now+5*60_000 || int64(it.TS) < now-7*24*3600_000 {
				it.TS = flexInt(received)
			}
			if it.UserID > 0 && p.HashUserIDs && len(p.HashKey) > 0 {
				it.UserID = flexInt(secure.HashID(p.HashKey, int64(it.UserID)))
			}
			it.SessionID = truncate(it.SessionID, 64)
			if err := w.write(it); err != nil {
				return fmt.Errorf("item %d (%s): %w", i, it.Type, err)
			}
		}
		return nil
	})
	return res, err
}

// batchWriter writes items for a single ingest batch, reusing prepared statements.
type batchWriter struct {
	s            *Server
	ctx          context.Context
	tx           *sql.Tx
	p            *ingestProject
	jobID        string
	placeVersion string
	release      string
	environment  string
	stmts        map[string]*sql.Stmt
	res          *ingestResult
	suspects     map[int64]bool
}

func (w *batchWriter) exec(query string, args ...any) (sql.Result, error) {
	if w.stmts == nil {
		w.stmts = map[string]*sql.Stmt{}
	}
	st, found := w.stmts[query]
	if !found {
		var err error
		if st, err = w.tx.PrepareContext(w.ctx, query); err != nil {
			return nil, err
		}
		w.stmts[query] = st
	}
	return st.ExecContext(w.ctx, args...)
}

func (w *batchWriter) close() {
	for _, st := range w.stmts {
		st.Close()
	}
}

func (w *batchWriter) live(t string, d any) {
	if w.res != nil && len(w.res.live) < 300 {
		w.res.live = append(w.res.live, liveMsg{T: t, D: d})
	}
}

// fromClient reports whether an item originated on a (untrusted) game client.
func fromClient(it *item) bool {
	if it.Side == "client" {
		return true
	}
	return it.Type == "event" && strings.Contains(string(it.Props), `"$client"`)
}

// allowClient enforces the per-player budget for client-originated items.
// A player who blows through it gets one automatic "client_flood" detection.
func (w *batchWriter) allowClient(it *item) (bool, error) {
	if w.s == nil || it.UserID <= 0 {
		return true, nil
	}
	rate := max(w.p.ClientRate, 1)
	key := fmt.Sprintf("%d:%d", w.p.ID, it.UserID)
	if w.s.limits.client.take(key, 1, rate, rate/60) {
		return true, nil
	}
	if w.s.limits.flags.take("flood:"+key, 1, 1, 1.0/300) {
		details, _ := json.Marshal(map[string]any{"limitPerMinute": int(rate), "itemType": it.Type})
		_, err := w.exec(`INSERT INTO detections (project_id, ts, user_id, session_key, job_id, kind, severity, source, details)
			VALUES (?, ?, ?, ?, ?, 'client_flood', 'medium', 'workshell', ?)`,
			w.p.ID, it.TS, int64(it.UserID), it.SessionID, w.jobID, string(details))
		if err != nil {
			return false, err
		}
		w.live("detection", map[string]any{"kind": "client_flood", "userId": int64(it.UserID), "severity": "medium", "ts": int64(it.TS)})
		w.res.triggers = append(w.res.triggers, trigger{Kind: "detection", Title: "client_flood", Detail: fmt.Sprintf("Player %d exceeded %d client reports/minute", it.UserID, int(rate))})
		if err := w.quarantinePlayer(int64(it.UserID), "client_flood", int64(it.TS)); err != nil {
			return false, err
		}
	}
	return false, nil
}

// suspectWindow is how long a flagged player's client reports stay untrusted.
const suspectWindow = 24 * 60 * 60_000

// isSuspect reports whether a player was flagged (a client flood or any
// high-severity detection) recently, so their client reports aren't trusted.
func (w *batchWriter) isSuspect(uid, ts int64) (bool, error) {
	if uid <= 0 {
		return false, nil
	}
	if v, found := w.suspects[uid]; found {
		return v, nil
	}
	var v bool
	err := w.tx.QueryRowContext(w.ctx, `SELECT EXISTS (SELECT 1 FROM detections WHERE project_id = ? AND user_id = ? AND ts >= ?
		AND (kind = 'client_flood' OR severity = 'high'))`, w.p.ID, uid, ts-suspectWindow).Scan(&v)
	if err != nil {
		return false, err
	}
	if w.suspects == nil {
		w.suspects = map[int64]bool{}
	}
	w.suspects[uid] = v
	return v, nil
}

// quarantinePlayer ignores recent client-side issues that only this (now
// flagged) player has ever reported, so an exploiter can't fill the issue
// list or fire alerts with made-up errors. They stay under Ignored, with a
// note explaining why, and can be reopened.
func (w *batchWriter) quarantinePlayer(uid int64, reason string, ts int64) error {
	if w.suspects == nil {
		w.suspects = map[int64]bool{}
	}
	w.suspects[uid] = true
	rows, err := w.tx.QueryContext(w.ctx, `
		UPDATE issues SET status = 'ignored'
		WHERE project_id = ? AND side = 'client' AND status = 'unresolved' AND user_count <= 1 AND first_seen >= ?
			AND id IN (SELECT issue_id FROM issue_users WHERE user_id = ?)
		RETURNING id`, w.p.ID, ts-suspectWindow, uid)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	quarantined := map[int64]bool{}
	for _, id := range ids {
		quarantined[id] = true
		if _, err := w.exec(`INSERT INTO issue_activity (issue_id, kind, data, created_at) VALUES (?, 'quarantine', ?, ?)`,
			id, fmt.Sprintf(`{"userId":%d,"reason":%q}`, uid, reason), ts); err != nil {
			return err
		}
	}
	// Drop pending "new issue" alerts for what was just quarantined.
	if len(quarantined) > 0 {
		kept := w.res.triggers[:0]
		for _, t := range w.res.triggers {
			if !quarantined[t.IssueID] {
				kept = append(kept, t)
			}
		}
		w.res.triggers = kept
	}
	return nil
}

func (w *batchWriter) write(it *item) error {
	if fromClient(it) {
		ok, err := w.allowClient(it)
		if err != nil || !ok {
			return err
		}
	}
	uid := int64(it.UserID)
	ts := int64(it.TS)
	switch it.Type {
	case "error":
		return w.writeError(it)
	case "log":
		if w.s != nil && !w.s.limits.logs.take(fmt.Sprintf("%d:%s", w.p.ID, w.jobID), 1, 3000, 50) {
			return nil // a server flooding logs: drop beyond 3000/min
		}
		level := oneOf(it.Level, "info", "debug", "warn", "error")
		msg := truncate(it.Message, 8192)
		r, err := w.exec(`INSERT INTO logs (project_id, ts, level, message, job_id, user_id, source, attrs, session_key) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			w.p.ID, ts, level, msg, w.jobID, nullableUser(uid), truncate(it.Source, 64), jsonOr(it.Attrs, "{}", 8192), it.SessionID)
		if err == nil {
			id, _ := r.LastInsertId()
			w.live("log", map[string]any{"id": id, "ts": ts, "level": level, "message": truncate(msg, 1000), "jobId": w.jobID, "userId": nullableUser(uid), "source": it.Source, "sessionKey": it.SessionID})
		}
		return err
	case "event":
		name := truncate(strings.TrimSpace(it.Name), 100)
		if name == "" {
			return nil
		}
		_, err := w.exec(`INSERT INTO events (project_id, ts, name, user_id, job_id, value, props, session_key) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			w.p.ID, ts, name, nullableUser(uid), w.jobID, it.Value, jsonOr(it.Props, "{}", 8192), it.SessionID)
		if err == nil {
			w.live("event", map[string]any{"ts": ts, "name": name, "userId": nullableUser(uid), "value": it.Value, "sessionKey": it.SessionID})
		}
		return err
	case "session_start":
		return w.sessionStart(it)
	case "session_update":
		_, err := w.exec(`UPDATE play_sessions SET
				platform = CASE WHEN ?3 != '' THEN ?3 ELSE platform END,
				country  = CASE WHEN ?4 != '' THEN ?4 ELSE country END,
				attrs    = CASE WHEN ?5 != '{}' THEN json_patch(attrs, ?5) ELSE attrs END
			WHERE project_id = ?1 AND session_key = ?2`,
			w.p.ID, it.SessionID, truncate(it.Platform, 32), truncate(it.Country, 8), jsonOr(it.Attrs, "{}", 4096))
		return err
	case "session_end":
		return w.sessionEnd(it.SessionID, ts)
	case "heartbeat":
		return w.heartbeat(it)
	case "server_close":
		if err := w.endOpenSessions(ts); err != nil {
			return err
		}
		_, err := w.exec(`UPDATE game_servers SET closed_at = ?3, players = 0 WHERE project_id = ?1 AND job_id = ?2`, w.p.ID, w.jobID, ts)
		return err
	case "metric":
		name := truncate(strings.TrimSpace(it.Name), 100)
		if name == "" || it.Count <= 0 {
			return nil
		}
		_, err := w.exec(`INSERT INTO metrics (project_id, name, ts, job_id, kind, count, sum, min, max, last) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			w.p.ID, name, ts, w.jobID, oneOf(it.Kind, "gauge", "counter", "timing"), int64(it.Count), it.Sum, it.Min, it.Max, it.Last)
		return err
	case "span":
		name := truncate(strings.TrimSpace(it.Name), 120)
		if name == "" || it.DurationMs < 0 || math.IsNaN(it.DurationMs) {
			return nil
		}
		_, err := w.exec(`INSERT INTO spans (project_id, ts, name, duration_ms, status, job_id, session_key, user_id, release, trace_id, span_id, parent, attrs)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			w.p.ID, ts, name, math.Min(it.DurationMs, 3_600_000), oneOf(it.Status, "ok", "error", "cancelled"), w.jobID, it.SessionID,
			nullableUser(uid), w.release, truncate(it.TraceID, 64), truncate(it.SpanID, 64), truncate(it.Parent, 64), jsonOr(it.Attrs, "{}", 4096))
		return err
	case "detection":
		kind := truncate(strings.TrimSpace(it.Kind), 64)
		if kind == "" {
			return nil
		}
		severity := oneOf(it.Severity, "medium", "low", "high")
		_, err := w.exec(`INSERT INTO detections (project_id, ts, user_id, session_key, job_id, kind, severity, action, source, details)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'game', ?)`,
			w.p.ID, ts, nullableUser(uid), it.SessionID, w.jobID, kind, severity, truncate(it.Action, 32), jsonOr(it.Details, "{}", 8192))
		if err == nil {
			w.live("detection", map[string]any{"kind": kind, "userId": nullableUser(uid), "severity": severity, "ts": ts, "action": it.Action})
			if severity == "high" {
				w.res.triggers = append(w.res.triggers, trigger{Kind: "detection", Title: kind, Detail: fmt.Sprintf("Player %d · %s severity · %s", uid, severity, oneOfDefault(it.Action, "no action"))})
				if uid > 0 {
					if err := w.quarantinePlayer(uid, kind, ts); err != nil {
						return err
					}
				}
			}
		}
		return err
	case "client_perf":
		if it.SessionID == "" {
			return nil
		}
		_, err := w.exec(`INSERT INTO client_perf (project_id, session_key, ts, fps, memory_mb, ping_ms, platform) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			w.p.ID, it.SessionID, ts, clamp(it.FPS, 0, 1000), clamp(it.MemoryMb, 0, 1e6), clamp(it.PingMs, 0, 1e5), truncate(it.Platform, 32))
		return err
	default:
		// Unknown types are ignored so older servers accept newer SDKs.
		return nil
	}
}

func clamp(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return lo
	}
	return math.Max(lo, math.Min(hi, v))
}

func (w *batchWriter) writeError(it *item) error {
	side := oneOf(it.Side, "server", "client")
	level := oneOf(it.Level, "error", "warning", "fatal")
	message := truncate(it.Message, 8192)
	stack := truncate(it.Stack, 16384)
	count := max(int64(it.Count), 1)
	ts := int64(it.TS)
	uid := int64(it.UserID)
	g := groupError(side, message, stack, truncate(it.Fingerprint, 200))

	var issueID int64
	var status string
	var assignee sql.NullInt64
	err := w.tx.QueryRowContext(w.ctx, `SELECT id, status, assignee_id FROM issues WHERE project_id = ? AND fingerprint = ?`,
		w.p.ID, g.Fingerprint).Scan(&issueID, &status, &assignee)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		suspect := false
		if side == "client" {
			if suspect, err = w.isSuspect(uid, ts); err != nil {
				return err
			}
		}
		status = "unresolved"
		if suspect {
			status = "ignored"
		}
		res, err := w.tx.ExecContext(w.ctx, `
			INSERT INTO issues (project_id, fingerprint, title, culprit, level, side, status, first_seen, last_seen, event_count, first_release, last_release)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			w.p.ID, g.Fingerprint, g.Title, g.Culprit, level, side, status, ts, ts, count, w.release, w.release)
		if err != nil {
			return err
		}
		issueID, _ = res.LastInsertId()
		if _, err := w.exec(`INSERT INTO issue_activity (issue_id, kind, data, created_at) VALUES (?, 'first_seen', ?, ?)`,
			issueID, fmt.Sprintf(`{"release":%q}`, w.release), ts); err != nil {
			return err
		}
		if suspect {
			if _, err := w.exec(`INSERT INTO issue_activity (issue_id, kind, data, created_at) VALUES (?, 'quarantine', ?, ?)`,
				issueID, fmt.Sprintf(`{"userId":%d,"reason":"flagged player"}`, uid), ts); err != nil {
				return err
			}
		} else {
			w.res.triggers = append(w.res.triggers, trigger{Kind: "new_issue", IssueID: issueID, Title: g.Title, Detail: g.Culprit, Release: w.release})
		}
	case err != nil:
		return err
	default:
		regressed := status == "resolved"
		_, err := w.exec(`
			UPDATE issues SET
				last_seen = MAX(last_seen, ?2),
				event_count = event_count + ?3,
				last_release = ?4,
				status = CASE WHEN status = 'resolved' THEN 'unresolved' ELSE status END,
				regressed_at = CASE WHEN status = 'resolved' THEN ?2 ELSE regressed_at END
			WHERE id = ?1`,
			issueID, ts, count, w.release)
		if err != nil {
			return err
		}
		if regressed {
			if _, err := w.exec(`INSERT INTO issue_activity (issue_id, kind, data, created_at) VALUES (?, 'regression', ?, ?)`,
				issueID, fmt.Sprintf(`{"release":%q}`, w.release), ts); err != nil {
				return err
			}
			if assignee.Valid {
				if _, err := w.exec(`INSERT INTO notifications (user_id, project_id, issue_id, kind, body, created_at) VALUES (?, ?, ?, 'regression', ?, ?)`,
					assignee.Int64, w.p.ID, issueID, truncate(g.Title, 200), ts); err != nil {
					return err
				}
			}
			w.res.triggers = append(w.res.triggers, trigger{Kind: "regression", IssueID: issueID, Title: g.Title, Detail: g.Culprit, Release: w.release})
		}
	}

	if uid > 0 {
		res, err := w.exec(`INSERT OR IGNORE INTO issue_users (issue_id, user_id) VALUES (?, ?)`, issueID, uid)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			if _, err := w.exec(`UPDATE issues SET user_count = user_count + 1 WHERE id = ?`, issueID); err != nil {
				return err
			}
		}
	}
	if it.SessionID != "" {
		if _, err := w.exec(`UPDATE play_sessions SET error_count = error_count + ?3 WHERE project_id = ?1 AND session_key = ?2`,
			w.p.ID, it.SessionID, count); err != nil {
			return err
		}
	}

	_, err = w.exec(`
		INSERT INTO error_events (project_id, issue_id, ts, count, message, stack, level, side, job_id, place_version, release, environment, user_id, script, tags, context, breadcrumbs, session_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.p.ID, issueID, ts, count, message, stack, level, side, w.jobID, w.placeVersion, w.release, w.environment,
		nullableUser(uid), truncate(it.Script, 256),
		jsonOr(it.Tags, "{}", 4096), jsonOr(it.Context, "{}", 16384), jsonOr(it.Breadcrumbs, "[]", 32768), it.SessionID)
	if err == nil {
		w.live("error", map[string]any{"issueId": issueID, "title": g.Title, "culprit": g.Culprit, "side": side, "ts": ts, "userId": nullableUser(uid), "sessionKey": it.SessionID, "count": count})
	}
	return err
}

func (w *batchWriter) sessionStart(it *item) error {
	uid := int64(it.UserID)
	// Studio test players have negative ids; keep them out of analytics.
	if uid <= 0 || it.SessionID == "" {
		return nil
	}
	ts := int64(it.TS)
	var existing int
	err := w.tx.QueryRowContext(w.ctx, `SELECT COUNT(*) FROM players WHERE project_id = ? AND user_id = ?`, w.p.ID, uid).Scan(&existing)
	if err != nil {
		return err
	}
	res, err := w.exec(`
		INSERT OR IGNORE INTO play_sessions (project_id, session_key, user_id, job_id, started_at, is_new, platform, country, release, attrs)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.p.ID, it.SessionID, uid, w.jobID, ts, existing == 0,
		truncate(it.Platform, 32), truncate(it.Country, 8), w.release, jsonOr(it.Attrs, "{}", 4096))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // duplicate delivery
	}
	w.live("session_start", map[string]any{"sessionKey": it.SessionID, "userId": uid, "ts": ts, "isNew": existing == 0})
	_, err = w.exec(`
		INSERT INTO players (project_id, user_id, first_seen, last_seen, session_count) VALUES (?1, ?2, ?3, ?3, 1)
		ON CONFLICT (project_id, user_id) DO UPDATE SET
			first_seen = MIN(first_seen, excluded.first_seen),
			last_seen = MAX(last_seen, excluded.last_seen),
			session_count = session_count + 1`,
		w.p.ID, uid, ts)
	return err
}

func (w *batchWriter) sessionEnd(sessionID string, ts int64) error {
	var userID, started int64
	err := w.tx.QueryRowContext(w.ctx, `
		UPDATE play_sessions SET ended_at = MAX(?3, started_at)
		WHERE project_id = ?1 AND session_key = ?2 AND ended_at IS NULL
		RETURNING user_id, started_at`,
		w.p.ID, sessionID, ts).Scan(&userID, &started)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	w.live("session_end", map[string]any{"sessionKey": sessionID, "userId": userID, "ts": ts})
	_, err = w.exec(`UPDATE players SET last_seen = MAX(last_seen, ?3), playtime_ms = playtime_ms + ?4 WHERE project_id = ?1 AND user_id = ?2`,
		w.p.ID, userID, ts, max(ts-started, 0))
	return err
}

// endOpenSessions closes every still-open session on this batch's server.
func (w *batchWriter) endOpenSessions(ts int64) error {
	rows, err := w.tx.QueryContext(w.ctx, `SELECT session_key FROM play_sessions WHERE project_id = ? AND job_id = ? AND ended_at IS NULL`, w.p.ID, w.jobID)
	if err != nil {
		return err
	}
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, k)
	}
	rows.Close()
	for _, k := range keys {
		if err := w.sessionEnd(k, ts); err != nil {
			return err
		}
	}
	return nil
}

func (w *batchWriter) heartbeat(it *item) error {
	_, err := w.exec(`
		INSERT INTO heartbeats (project_id, job_id, ts, players, fps, heartbeat_ms, memory_mb, ping_ms, instances, send_kbps, recv_kbps)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.p.ID, w.jobID, int64(it.TS), int64(it.Players), it.FPS, it.HeartbeatMs, it.MemoryMb, it.PingMs, int64(it.Instances), it.SendKbps, it.RecvKbps)
	if err != nil {
		return err
	}
	_, err = w.exec(`
		UPDATE game_servers SET players = ?3, fps = ?4, heartbeat_ms = ?5, memory_mb = ?6, ping_ms = ?7
		WHERE project_id = ?1 AND job_id = ?2`,
		w.p.ID, w.jobID, int64(it.Players), it.FPS, it.HeartbeatMs, it.MemoryMb, it.PingMs)
	return err
}
