package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/workshell/workshell/internal/secure"
)

// liveWindow is how recently a game server must have sent a heartbeat to count as live.
const liveWindow = 90 * time.Second

type project struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	IngestKey         string `json:"ingestKey"`
	CreatedAt         int64  `json:"createdAt"`
	LogRetentionDays  int    `json:"logRetentionDays"`
	DataRetentionDays int    `json:"dataRetentionDays"`
	GitHubRepo        string `json:"githubRepo"`
	HasGitHubToken    bool   `json:"hasGithubToken"`
	SigningSecret     string `json:"signingSecret"`
	RequireSignature  bool   `json:"requireSignature"`
	HashUserIDs       bool   `json:"hashUserIds"`
	IngestRate        int    `json:"ingestRate"`
	ClientRate        int    `json:"clientRate"`
	HasRobloxWebhook  bool   `json:"hasRobloxWebhook"`

	githubToken string
	hashSecret  string
}

const projectColumns = `id, name, ingest_key, created_at, log_retention_days, data_retention_days,
	github_repo, github_token, signing_secret, require_signature, hash_user_ids, user_hash_secret, ingest_rate, client_rate, roblox_webhook_secret != ''`

func (s *Server) scanProject(row interface{ Scan(...any) error }) (*project, error) {
	var p project
	var token, signing, hash string
	err := row.Scan(&p.ID, &p.Name, &p.IngestKey, &p.CreatedAt, &p.LogRetentionDays, &p.DataRetentionDays,
		&p.GitHubRepo, &token, &signing, &p.RequireSignature, &p.HashUserIDs, &hash, &p.IngestRate, &p.ClientRate, &p.HasRobloxWebhook)
	if err != nil {
		return nil, err
	}
	p.githubToken = s.box.MustOpen(token)
	p.HasGitHubToken = p.githubToken != ""
	p.SigningSecret = s.box.MustOpen(signing)
	p.hashSecret = s.box.MustOpen(hash)
	return &p, nil
}

func newIngestKey() string { return "wsk_" + randomHex(24) }

func (s *Server) withProject(fn func(w http.ResponseWriter, r *http.Request, p *project, u *user) error) func(http.ResponseWriter, *http.Request, *user) error {
	return func(w http.ResponseWriter, r *http.Request, u *user) error {
		id, err := pathInt(r, "pid")
		if err != nil {
			return err
		}
		p, err := s.scanProject(s.db.R.QueryRowContext(r.Context(), `SELECT `+projectColumns+` FROM projects WHERE id = ?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return err
		}
		return fn(w, r, p, u)
	}
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request, u *user) error {
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT `+projectColumns+` FROM projects ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type summary struct {
		*project
		LivePlayers   int       `json:"livePlayers"`
		LiveServers   int       `json:"liveServers"`
		OpenIssues    int       `json:"openIssues"`
		LatestRelease string    `json:"latestRelease"`
		LastEventAt   int64     `json:"lastEventAt"`
		Activity      []float64 `json:"activity"` // players per hour, last 24h
	}
	out := []summary{}
	for rows.Next() {
		p, err := s.scanProject(rows)
		if err != nil {
			return err
		}
		out = append(out, summary{project: p})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	cutoff := nowMs() - liveWindow.Milliseconds()
	tr := timeRange{Bucket: int64(time.Hour / time.Millisecond)}
	tr.To = (nowMs()/tr.Bucket + 1) * tr.Bucket
	tr.From = tr.To - 24*tr.Bucket
	for i := range out {
		err := s.db.R.QueryRowContext(r.Context(), `
			SELECT
				(SELECT COALESCE(SUM(players), 0) FROM game_servers WHERE project_id = ?1 AND closed_at IS NULL AND last_seen > ?2),
				(SELECT COUNT(*) FROM game_servers WHERE project_id = ?1 AND closed_at IS NULL AND last_seen > ?2),
				(SELECT COUNT(*) FROM issues WHERE project_id = ?1 AND status = 'unresolved'),
				COALESCE((SELECT version FROM releases WHERE project_id = ?1 ORDER BY COALESCE(first_seen, created_at) DESC LIMIT 1), ''),
				COALESCE((SELECT MAX(last_seen) FROM game_servers WHERE project_id = ?1), 0)`,
			out[i].ID, cutoff,
		).Scan(&out[i].LivePlayers, &out[i].LiveServers, &out[i].OpenIssues, &out[i].LatestRelease, &out[i].LastEventAt)
		if err != nil {
			return err
		}
		ts, err := s.ccuSeries(r, out[i].ID, tr, "")
		if err != nil {
			return err
		}
		out[i].Activity = ts.Series[0].Values
	}
	return ok(w, out)
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request, u *user) error {
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || len(body.Name) > 100 {
		return badRequest("project name must be 1-100 characters")
	}
	res, err := s.db.W.ExecContext(r.Context(),
		`INSERT INTO projects (name, ingest_key, created_at, user_hash_secret) VALUES (?, ?, ?, ?)`,
		body.Name, newIngestKey(), nowMs(), s.box.Seal(secure.RandomToken(32)))
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	if err := s.createDefaultRules(r.Context(), id, 0); err != nil {
		return err
	}
	s.audit(r, id, u, "project.create", body.Name)
	return ok(w, map[string]any{"id": id})
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request, p *project) error {
	return ok(w, p)
}

var githubRepoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	var body struct {
		Name              *string `json:"name"`
		LogRetentionDays  *int    `json:"logRetentionDays"`
		DataRetentionDays *int    `json:"dataRetentionDays"`
		GitHubRepo        *string `json:"githubRepo"`
		GitHubToken       *string `json:"githubToken"`
		RequireSignature  *bool   `json:"requireSignature"`
		HashUserIDs       *bool   `json:"hashUserIds"`
		IngestRate        *int    `json:"ingestRate"`
		ClientRate        *int    `json:"clientRate"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	var changed []string
	if body.Name != nil {
		p.Name = strings.TrimSpace(*body.Name)
		if p.Name == "" || len(p.Name) > 100 {
			return badRequest("project name must be 1-100 characters")
		}
		changed = append(changed, "name")
	}
	if body.LogRetentionDays != nil {
		if *body.LogRetentionDays < 1 || *body.LogRetentionDays > 3650 {
			return badRequest("log retention must be between 1 and 3650 days")
		}
		p.LogRetentionDays = *body.LogRetentionDays
		changed = append(changed, "logRetention")
	}
	if body.DataRetentionDays != nil {
		if *body.DataRetentionDays < 1 || *body.DataRetentionDays > 3650 {
			return badRequest("data retention must be between 1 and 3650 days")
		}
		p.DataRetentionDays = *body.DataRetentionDays
		changed = append(changed, "dataRetention")
	}
	if body.GitHubRepo != nil {
		v := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(*body.GitHubRepo, "https://github.com/"), ".git"))
		if v != "" && !githubRepoPattern.MatchString(v) {
			return badRequest("GitHub repository must look like owner/name")
		}
		p.GitHubRepo = v
		changed = append(changed, "githubRepo")
	}
	if body.GitHubToken != nil {
		p.githubToken = strings.TrimSpace(*body.GitHubToken)
		changed = append(changed, "githubToken")
	}
	if body.RequireSignature != nil {
		if *body.RequireSignature && p.SigningSecret == "" {
			return badRequest("generate a signing secret first")
		}
		p.RequireSignature = *body.RequireSignature
		changed = append(changed, "requireSignature")
	}
	if body.HashUserIDs != nil {
		p.HashUserIDs = *body.HashUserIDs
		changed = append(changed, "hashUserIds")
	}
	if body.IngestRate != nil {
		if *body.IngestRate < 10 || *body.IngestRate > 1_000_000 {
			return badRequest("ingest rate must be between 10 and 1,000,000 items/second")
		}
		p.IngestRate = *body.IngestRate
		changed = append(changed, "ingestRate")
	}
	if body.ClientRate != nil {
		if *body.ClientRate < 1 || *body.ClientRate > 10_000 {
			return badRequest("client rate must be between 1 and 10,000 items/minute")
		}
		p.ClientRate = *body.ClientRate
		changed = append(changed, "clientRate")
	}
	_, err := s.db.W.ExecContext(r.Context(), `
		UPDATE projects SET name = ?, log_retention_days = ?, data_retention_days = ?, github_repo = ?, github_token = ?,
			require_signature = ?, hash_user_ids = ?, ingest_rate = ?, client_rate = ?
		WHERE id = ?`,
		p.Name, p.LogRetentionDays, p.DataRetentionDays, p.GitHubRepo, s.box.Seal(p.githubToken),
		p.RequireSignature, p.HashUserIDs, p.IngestRate, p.ClientRate, p.ID)
	if err != nil {
		return err
	}
	p.HasGitHubToken = p.githubToken != ""
	s.invalidateKeyCache()
	s.audit(r, p.ID, u, "project.update", strings.Join(changed, ", "))
	return ok(w, p)
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	if _, err := s.db.W.ExecContext(r.Context(), `DELETE FROM projects WHERE id = ?`, p.ID); err != nil {
		return err
	}
	s.invalidateKeyCache()
	s.audit(r, 0, u, "project.delete", p.Name)
	return ok(w, map[string]any{"ok": true})
}

func (s *Server) handleRotateKey(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	p.IngestKey = newIngestKey()
	if _, err := s.db.W.ExecContext(r.Context(), `UPDATE projects SET ingest_key = ? WHERE id = ?`, p.IngestKey, p.ID); err != nil {
		return err
	}
	s.invalidateKeyCache()
	s.audit(r, p.ID, u, "key.rotate", "")
	return ok(w, p)
}

func (s *Server) handleRotateSigningSecret(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	p.SigningSecret = "wss_" + randomHex(32)
	if _, err := s.db.W.ExecContext(r.Context(), `UPDATE projects SET signing_secret = ? WHERE id = ?`, s.box.Seal(p.SigningSecret), p.ID); err != nil {
		return err
	}
	s.invalidateKeyCache()
	s.audit(r, p.ID, u, "signing_secret.rotate", "")
	return ok(w, p)
}

// migrateLegacyWebhooks turns the v1 per-project webhook + toggles into an
// alert channel with rules, now that alerting is rule-based.
func (s *Server) migrateLegacyWebhooks(ctx context.Context) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, webhook_url, alert_new_issues, alert_regressions FROM projects
		WHERE id NOT IN (SELECT DISTINCT project_id FROM alert_rules)`)
	if err != nil {
		s.log.Error("migrate webhooks", "err", err)
		return
	}
	type legacy struct {
		id           int64
		url          string
		newIss, regr bool
	}
	var list []legacy
	for rows.Next() {
		var l legacy
		if rows.Scan(&l.id, &l.url, &l.newIss, &l.regr) == nil {
			list = append(list, l)
		}
	}
	rows.Close()
	for _, l := range list {
		var channel int64
		if l.url != "" {
			kind := "webhook"
			if isDiscord(l.url) {
				kind = "discord"
			}
			res, err := s.db.W.ExecContext(ctx, `INSERT INTO alert_channels (project_id, name, kind, url, created_at) VALUES (?, ?, ?, ?, ?)`,
				l.id, "Default", kind, s.box.Seal(l.url), nowMs())
			if err != nil {
				s.log.Error("migrate webhooks", "err", err)
				continue
			}
			channel, _ = res.LastInsertId()
		}
		if err := s.createDefaultRules(ctx, l.id, channel); err != nil {
			s.log.Error("migrate webhooks", "err", err)
		}
		s.db.W.ExecContext(ctx, `UPDATE projects SET webhook_url = '' WHERE id = ?`, l.id)
	}
}

func (s *Server) createDefaultRules(ctx context.Context, projectID, channel int64) error {
	var ch any
	if channel > 0 {
		ch = channel
	}
	now := nowMs()
	for _, rule := range []struct {
		name, kind string
		threshold  float64
		window     int
	}{
		{"New issue", "new_issue", 0, 0},
		{"Regression", "regression", 0, 0},
		{"Error spike", "error_spike", 100, 5},
		{"High-severity exploit", "detection", 1, 5},
	} {
		_, err := s.db.W.ExecContext(ctx, `INSERT INTO alert_rules (project_id, name, kind, threshold, window_minutes, channel_id, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, projectID, rule.name, rule.kind, rule.threshold, max(rule.window, 1), ch, now)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) handleAuditLog(w http.ResponseWriter, r *http.Request, p *project) error {
	rows, err := s.db.R.QueryContext(r.Context(), `
		SELECT a.id, a.action, a.detail, a.ip, a.created_at, COALESCE(u.name, ''), COALESCE(u.id, 0)
		FROM audit_log a LEFT JOIN users u ON u.id = a.user_id
		WHERE a.project_id = ? ORDER BY a.created_at DESC LIMIT 100`, p.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, at, uid int64
		var action, detail, ip, name string
		if err := rows.Scan(&id, &action, &detail, &ip, &at, &name, &uid); err != nil {
			return err
		}
		out = append(out, map[string]any{"id": id, "action": action, "detail": detail, "ip": ip, "createdAt": at, "userName": name, "userId": uid})
	}
	return ok(w, out)
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request, p *project) error {
	ctx := r.Context()
	tr := parseRange(r, "24h")
	cutoff := nowMs() - liveWindow.Milliseconds()

	var out struct {
		LivePlayers    int         `json:"livePlayers"`
		LiveServers    int         `json:"liveServers"`
		UniquePlayers  int         `json:"uniquePlayers"`
		NewPlayers     int         `json:"newPlayers"`
		Sessions       int         `json:"sessions"`
		AvgSessionMs   float64     `json:"avgSessionMs"`
		ErrorEvents    int         `json:"errorEvents"`
		NewIssues      int         `json:"newIssues"`
		OpenIssues     int         `json:"openIssues"`
		PeakPlayers    float64     `json:"peakPlayers"`
		Revenue        float64     `json:"revenue"`
		Detections     int         `json:"detections"`
		LatestRelease  string      `json:"latestRelease"`
		Players        *timeseries `json:"players"`
		Errors         *timeseries `json:"errors"`
		TopIssues      []issueRow  `json:"topIssues"`
		ServerVersions []struct {
			Version string `json:"version"`
			Servers int    `json:"servers"`
			Players int    `json:"players"`
		} `json:"serverVersions"`
	}

	err := s.db.R.QueryRowContext(ctx, `
		SELECT
			(SELECT COALESCE(SUM(players), 0) FROM game_servers WHERE project_id = ?1 AND closed_at IS NULL AND last_seen > ?4),
			(SELECT COUNT(*) FROM game_servers WHERE project_id = ?1 AND closed_at IS NULL AND last_seen > ?4),
			(SELECT COUNT(DISTINCT user_id) FROM play_sessions WHERE project_id = ?1 AND started_at >= ?2 AND started_at < ?3),
			(SELECT COUNT(*) FROM players WHERE project_id = ?1 AND first_seen >= ?2 AND first_seen < ?3),
			(SELECT COUNT(*) FROM play_sessions WHERE project_id = ?1 AND started_at >= ?2 AND started_at < ?3),
			(SELECT COALESCE(AVG(ended_at - started_at), 0) FROM play_sessions WHERE project_id = ?1 AND started_at >= ?2 AND started_at < ?3 AND ended_at IS NOT NULL),
			(SELECT COALESCE(SUM(count), 0) FROM error_events WHERE project_id = ?1 AND ts >= ?2 AND ts < ?3),
			(SELECT COUNT(*) FROM issues WHERE project_id = ?1 AND first_seen >= ?2 AND first_seen < ?3),
			(SELECT COUNT(*) FROM issues WHERE project_id = ?1 AND status = 'unresolved'),
			(SELECT COALESCE(SUM(value), 0) FROM events WHERE project_id = ?1 AND name = '$purchase' AND ts >= ?2 AND ts < ?3),
			(SELECT COUNT(*) FROM detections WHERE project_id = ?1 AND ts >= ?2 AND ts < ?3),
			COALESCE((SELECT version FROM releases WHERE project_id = ?1 ORDER BY COALESCE(first_seen, created_at) DESC LIMIT 1), '')`,
		p.ID, tr.From, tr.To, cutoff,
	).Scan(&out.LivePlayers, &out.LiveServers, &out.UniquePlayers, &out.NewPlayers, &out.Sessions,
		&out.AvgSessionMs, &out.ErrorEvents, &out.NewIssues, &out.OpenIssues, &out.Revenue, &out.Detections, &out.LatestRelease)
	if err != nil {
		return err
	}

	if out.Players, err = s.ccuSeries(r, p.ID, tr, ""); err != nil {
		return err
	}
	for _, v := range out.Players.Series[0].Values {
		out.PeakPlayers = max(out.PeakPlayers, v)
	}

	out.Errors = newTimeseries(tr, "events")
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT (ts - ?2) / ?4 * ?4 + ?2, SUM(count) FROM error_events
		WHERE project_id = ?1 AND ts >= ?2 AND ts < ?3 GROUP BY 1`,
		p.ID, tr.From, tr.To, tr.Bucket)
	if err != nil {
		return err
	}
	for rows.Next() {
		var b int64
		var v float64
		if err := rows.Scan(&b, &v); err != nil {
			rows.Close()
			return err
		}
		out.Errors.set(0, b, v)
	}
	rows.Close()

	out.TopIssues, err = s.queryIssues(r, p.ID, issueQuery{Status: "unresolved", Sort: "events", From: tr.From, Limit: 5})
	if err != nil {
		return err
	}

	rows, err = s.db.R.QueryContext(ctx, `
		SELECT release, COUNT(*), SUM(players) FROM game_servers
		WHERE project_id = ? AND closed_at IS NULL AND last_seen > ?
		GROUP BY release ORDER BY 3 DESC, 2 DESC`, p.ID, cutoff)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var v struct {
			Version string `json:"version"`
			Servers int    `json:"servers"`
			Players int    `json:"players"`
		}
		if err := rows.Scan(&v.Version, &v.Servers, &v.Players); err != nil {
			return err
		}
		out.ServerVersions = append(out.ServerVersions, v)
	}
	return ok(w, out)
}

// ccuSeries returns concurrent players over time, derived from server heartbeats:
// each server's average player count per bucket, summed across servers.
// If jobID is set, only that server is included.
func (s *Server) ccuSeries(r *http.Request, projectID int64, tr timeRange, jobID string) (*timeseries, error) {
	ts := newTimeseries(tr, "players")
	rows, err := s.db.R.QueryContext(r.Context(), `
		SELECT b, SUM(p) FROM (
			SELECT (ts - ?2) / ?4 * ?4 + ?2 AS b, job_id, AVG(players) AS p
			FROM heartbeats
			WHERE project_id = ?1 AND ts >= ?2 AND ts < ?3 AND (?5 = '' OR job_id = ?5)
			GROUP BY b, job_id
		) GROUP BY b`,
		projectID, tr.From, tr.To, tr.Bucket, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b int64
		var v float64
		if err := rows.Scan(&b, &v); err != nil {
			return nil, err
		}
		ts.set(0, b, v)
	}
	return ts, rows.Err()
}
