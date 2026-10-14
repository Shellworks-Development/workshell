package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Alert rule kinds. Event rules fire as things happen during ingestion;
// threshold rules are evaluated every minute over their window.
var ruleKinds = map[string]struct {
	label     string
	threshold bool // evaluated periodically against Threshold
}{
	"new_issue":   {"A new issue appears", false},
	"regression":  {"A resolved issue happens again", false},
	"error_spike": {"Errors in window exceed threshold", true},
	"detection":   {"Exploit detections in window reach threshold", true},
	"ccu_below":   {"Concurrent players drop below threshold", true},
	"fps_below":   {"Average server FPS drops below threshold", true},
}

type alert struct {
	Kind    string
	Project string
	Title   string
	Detail  string
	Link    string
	Fields  [][2]string
}

type channelRow struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	URL       string `json:"url"` // masked in API responses
	CreatedAt int64  `json:"createdAt"`
	url       string
}

type delivery struct {
	projectID int64
	ruleID    int64
	channel   *channelRow
	a         alert
}

// alerter delivers notifications in the background so ingestion never waits
// on a slow webhook, and records every alert in alert_history.
type alerter struct {
	s      *Server
	queue  chan delivery
	client *http.Client
}

func newAlerter(s *Server) *alerter {
	return &alerter{s: s, queue: make(chan delivery, 512), client: &http.Client{Timeout: 10 * time.Second}}
}

func (a *alerter) run(ctx context.Context) {
	// Space out deliveries so an error storm doesn't trip webhook rate limits.
	limiter := time.NewTicker(400 * time.Millisecond)
	defer limiter.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case d := <-a.queue:
			a.deliver(ctx, d)
			select {
			case <-limiter.C:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (a *alerter) deliver(ctx context.Context, d delivery) {
	var errMsg string
	delivered := false
	if d.channel != nil {
		if err := a.send(ctx, d.channel, d.a); err != nil {
			errMsg = err.Error()
			a.s.log.Warn("alert delivery failed", "project", d.projectID, "channel", d.channel.Name, "err", err)
		} else {
			delivered = true
		}
	}
	var rule any
	if d.ruleID > 0 {
		rule = d.ruleID
	}
	_, err := a.s.db.W.ExecContext(ctx, `INSERT INTO alert_history (project_id, rule_id, kind, title, detail, link, delivered, error, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, d.projectID, rule, d.a.Kind, truncate(d.a.Title, 300), truncate(d.a.Detail, 1000), d.a.Link, delivered, errMsg, nowMs())
	if err != nil {
		a.s.log.Warn("alert history", "err", err)
	}
}

type ruleRow struct {
	ID              int64   `json:"id"`
	Name            string  `json:"name"`
	Kind            string  `json:"kind"`
	Threshold       float64 `json:"threshold"`
	WindowMinutes   int     `json:"windowMinutes"`
	CooldownMinutes int     `json:"cooldownMinutes"`
	ChannelID       *int64  `json:"channelId"`
	Enabled         bool    `json:"enabled"`
	LastFiredAt     *int64  `json:"lastFiredAt"`
	projectID       int64
}

const ruleColumns = `id, name, kind, threshold, window_minutes, cooldown_minutes, channel_id, enabled, last_fired_at, project_id`

func scanRule(row interface{ Scan(...any) error }) (*ruleRow, error) {
	var r ruleRow
	err := row.Scan(&r.ID, &r.Name, &r.Kind, &r.Threshold, &r.WindowMinutes, &r.CooldownMinutes, &r.ChannelID, &r.Enabled, &r.LastFiredAt, &r.projectID)
	return &r, err
}

func (s *Server) loadChannel(ctx context.Context, projectID, id int64) (*channelRow, error) {
	var c channelRow
	err := s.db.R.QueryRowContext(ctx, `SELECT id, name, kind, url, created_at FROM alert_channels WHERE id = ? AND project_id = ?`, id, projectID).
		Scan(&c.ID, &c.Name, &c.Kind, &c.url, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	c.url = s.box.MustOpen(c.url)
	c.URL = maskURL(c.url)
	return &c, nil
}

func maskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "••••"
	}
	return u.Scheme + "://" + u.Host + "/••••"
}

func (s *Server) projectName(ctx context.Context, id int64) string {
	var name string
	s.db.R.QueryRowContext(ctx, `SELECT name FROM projects WHERE id = ?`, id).Scan(&name)
	return name
}

func (s *Server) link(path string) string {
	if s.cfg.PublicURL == "" {
		return ""
	}
	return strings.TrimRight(s.cfg.PublicURL, "/") + path
}

// fire queues an alert for a rule (respecting its cooldown when asked to).
func (s *Server) fire(ctx context.Context, rule *ruleRow, a alert, useCooldown bool) {
	now := nowMs()
	if useCooldown && rule.LastFiredAt != nil && now-*rule.LastFiredAt < int64(rule.CooldownMinutes)*60_000 {
		return
	}
	s.db.W.ExecContext(ctx, `UPDATE alert_rules SET last_fired_at = ? WHERE id = ?`, now, rule.ID)
	var ch *channelRow
	if rule.ChannelID != nil {
		ch, _ = s.loadChannel(ctx, rule.projectID, *rule.ChannelID)
	}
	a.Project = s.projectName(ctx, rule.projectID)
	select {
	case s.alerts.queue <- delivery{projectID: rule.projectID, ruleID: rule.ID, channel: ch, a: a}:
	default:
		s.log.Warn("alert queue full, dropping alert", "project", rule.projectID, "title", a.Title)
	}
}

// trigger handles event-style alerts raised during ingestion.
func (a *alerter) trigger(ctx context.Context, projectID int64, t trigger) {
	s := a.s
	kind := t.Kind
	rows, err := s.db.R.QueryContext(ctx, `SELECT `+ruleColumns+` FROM alert_rules WHERE project_id = ? AND kind = ? AND enabled = 1`, projectID, kind)
	if err != nil {
		return
	}
	var rules []*ruleRow
	for rows.Next() {
		if r, err := scanRule(rows); err == nil {
			rules = append(rules, r)
		}
	}
	rows.Close()
	for _, rule := range rules {
		switch kind {
		case "new_issue", "regression":
			// Cap bursts (e.g. a bad deploy creating dozens of issues at once).
			if !s.limits.flags.take(fmt.Sprintf("alert:%d", rule.ID), 1, 10, 10.0/60) {
				continue
			}
			heading := "New issue"
			if kind == "regression" {
				heading = "Regression"
			}
			fields := [][2]string{}
			if t.Release != "" {
				fields = append(fields, [2]string{"Release", t.Release})
			}
			s.fire(ctx, rule, alert{Kind: kind, Title: heading + ": " + t.Title, Detail: t.Detail,
				Link: s.link(fmt.Sprintf("/p/%d/issues/%d", projectID, t.IssueID)), Fields: fields}, false)
		case "detection":
			// High-severity detections alert immediately when threshold is 1;
			// otherwise the periodic evaluator handles the windowed count.
			if rule.Threshold <= 1 {
				s.fire(ctx, rule, alert{Kind: kind, Title: "Exploit detected: " + t.Title, Detail: t.Detail,
					Link: s.link(fmt.Sprintf("/p/%d/security", projectID))}, true)
			}
		}
	}
}

// evaluateAlertRules checks every threshold rule once.
func (s *Server) evaluateAlertRules(ctx context.Context) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT `+ruleColumns+` FROM alert_rules WHERE enabled = 1 AND kind IN ('error_spike', 'detection', 'ccu_below', 'fps_below')`)
	if err != nil {
		s.log.Error("evaluate alerts", "err", err)
		return
	}
	var rules []*ruleRow
	for rows.Next() {
		if r, err := scanRule(rows); err == nil {
			rules = append(rules, r)
		}
	}
	rows.Close()
	now := nowMs()
	cutoff := now - liveWindow.Milliseconds()
	for _, rule := range rules {
		since := now - int64(max(rule.WindowMinutes, 1))*60_000
		var v float64
		var live int
		var q string
		switch rule.Kind {
		case "error_spike":
			q = `SELECT COALESCE(SUM(count), 0), 0 FROM error_events WHERE project_id = ?1 AND ts >= ?2`
		case "detection":
			if rule.Threshold <= 1 {
				continue // handled immediately on ingest
			}
			q = `SELECT COUNT(*), 0 FROM detections WHERE project_id = ?1 AND ts >= ?2`
		case "ccu_below":
			q = `SELECT COALESCE(SUM(players), 0), COUNT(*) FROM game_servers WHERE project_id = ?1 AND closed_at IS NULL AND last_seen > ?3`
		case "fps_below":
			q = `SELECT COALESCE(AVG(fps), 0), COUNT(DISTINCT job_id) FROM heartbeats WHERE project_id = ?1 AND ts >= ?2`
		}
		if err := s.db.R.QueryRowContext(ctx, q, rule.projectID, since, cutoff).Scan(&v, &live); err != nil {
			continue
		}
		var hit bool
		var title string
		switch rule.Kind {
		case "error_spike":
			hit, title = v >= rule.Threshold, fmt.Sprintf("Error spike: %d errors in %d min", int(v), rule.WindowMinutes)
		case "detection":
			hit, title = v >= rule.Threshold, fmt.Sprintf("%d exploit detections in %d min", int(v), rule.WindowMinutes)
		case "ccu_below":
			// Only meaningful while the game is running at all.
			hit, title = live > 0 && v < rule.Threshold, fmt.Sprintf("Players dropped to %d (below %d)", int(v), int(rule.Threshold))
		case "fps_below":
			hit, title = live > 0 && v < rule.Threshold, fmt.Sprintf("Average server FPS is %.1f (below %.0f)", v, rule.Threshold)
		}
		if hit {
			path := map[string]string{"error_spike": "issues", "detection": "security", "ccu_below": "servers", "fps_below": "performance"}[rule.Kind]
			s.fire(ctx, rule, alert{Kind: rule.Kind, Title: title, Detail: rule.Name, Link: s.link(fmt.Sprintf("/p/%d/%s", rule.projectID, path))}, true)
		}
	}
}

// defuse neutralizes chat markup in untrusted text (error messages can come
// from exploiters): no @everyone/@here/role pings, no masked links.
func defuse(s string) string {
	s = strings.NewReplacer("@", "@​", "`", "'", "](", "] (", "<", "‹", ">", "›").Replace(s)
	return s
}

func (a *alerter) send(ctx context.Context, ch *channelRow, al alert) error {
	color := 0xE5484D
	switch al.Kind {
	case "regression", "ccu_below", "fps_below":
		color = 0xF5A524
	case "test":
		color = 0x3E9BFF
	}
	var payload any
	if ch.Kind == "discord" {
		fields := []map[string]any{}
		for _, f := range al.Fields {
			fields = append(fields, map[string]any{"name": f[0], "value": defuse(truncate(f[1], 200)), "inline": true})
		}
		embed := map[string]any{
			"title":       defuse(truncate(al.Title, 250)),
			"description": defuse(truncate(al.Detail, 2000)),
			"color":       color,
			"footer":      map[string]any{"text": "Workshell · " + defuse(al.Project)},
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
			"fields":      fields,
		}
		if al.Link != "" {
			embed["url"] = al.Link
		}
		payload = map[string]any{
			"username":         "Workshell",
			"embeds":           []any{embed},
			"allowed_mentions": map[string]any{"parse": []string{}},
		}
	} else {
		text := fmt.Sprintf("[%s] %s", al.Project, al.Title)
		if al.Detail != "" {
			text += "\n" + al.Detail
		}
		if al.Link != "" {
			text += "\n" + al.Link
		}
		fields := map[string]string{}
		for _, f := range al.Fields {
			fields[f[0]] = f[1]
		}
		payload = map[string]any{"text": defuse(text), "kind": al.Kind, "project": al.Project, "title": al.Title,
			"detail": al.Detail, "url": al.Link, "fields": fields}
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ch.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Workshell")
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func isDiscord(webhook string) bool {
	u, err := url.Parse(webhook)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return (h == "discord.com" || h == "discordapp.com" || strings.HasSuffix(h, ".discord.com")) &&
		strings.HasPrefix(u.Path, "/api/webhooks/")
}

// ---- API --------------------------------------------------------------------------

func (s *Server) handleListAlerts(w http.ResponseWriter, r *http.Request, p *project) error {
	ctx := r.Context()
	channels := []*channelRow{}
	rows, err := s.db.R.QueryContext(ctx, `SELECT id FROM alert_channels WHERE project_id = ? ORDER BY id`, p.ID)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if c, err := s.loadChannel(ctx, p.ID, id); err == nil {
			channels = append(channels, c)
		}
	}

	rules := []*ruleRow{}
	rows, err = s.db.R.QueryContext(ctx, `SELECT `+ruleColumns+` FROM alert_rules WHERE project_id = ? ORDER BY id`, p.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			rows.Close()
			return err
		}
		rules = append(rules, r)
	}
	rows.Close()

	type hist struct {
		ID        int64  `json:"id"`
		Kind      string `json:"kind"`
		Title     string `json:"title"`
		Detail    string `json:"detail"`
		Link      string `json:"link"`
		Delivered bool   `json:"delivered"`
		Error     string `json:"error"`
		CreatedAt int64  `json:"createdAt"`
		RuleName  string `json:"ruleName"`
	}
	history := []hist{}
	rows, err = s.db.R.QueryContext(ctx, `
		SELECT h.id, h.kind, h.title, h.detail, h.link, h.delivered, h.error, h.created_at, COALESCE(r.name, '')
		FROM alert_history h LEFT JOIN alert_rules r ON r.id = h.rule_id
		WHERE h.project_id = ? ORDER BY h.created_at DESC, h.id DESC LIMIT 100`, p.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var h hist
		if err := rows.Scan(&h.ID, &h.Kind, &h.Title, &h.Detail, &h.Link, &h.Delivered, &h.Error, &h.CreatedAt, &h.RuleName); err != nil {
			return err
		}
		history = append(history, h)
	}
	kinds := []map[string]any{}
	for _, k := range []string{"new_issue", "regression", "error_spike", "detection", "ccu_below", "fps_below"} {
		kinds = append(kinds, map[string]any{"kind": k, "label": ruleKinds[k].label, "threshold": ruleKinds[k].threshold})
	}
	return ok(w, map[string]any{"channels": channels, "rules": rules, "history": history, "kinds": kinds})
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	var body struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || len(body.Name) > 60 {
		return badRequest("channel name must be 1-60 characters")
	}
	parsed, err := url.Parse(strings.TrimSpace(body.URL))
	if err != nil || parsed.Scheme != "https" && parsed.Scheme != "http" || parsed.Host == "" {
		return badRequest("webhook URL must be an http(s) URL")
	}
	kind := "webhook"
	if isDiscord(parsed.String()) {
		kind = "discord"
	}
	res, err := s.db.W.ExecContext(r.Context(), `INSERT INTO alert_channels (project_id, name, kind, url, created_at) VALUES (?, ?, ?, ?, ?)`,
		p.ID, body.Name, kind, s.box.Seal(parsed.String()), nowMs())
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	s.audit(r, p.ID, u, "alerts.channel.create", body.Name)
	return ok(w, map[string]any{"id": id})
}

func (s *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	id, err := pathInt(r, "cid")
	if err != nil {
		return err
	}
	if _, err := s.db.W.ExecContext(r.Context(), `DELETE FROM alert_channels WHERE id = ? AND project_id = ?`, id, p.ID); err != nil {
		return err
	}
	s.audit(r, p.ID, u, "alerts.channel.delete", strconv.FormatInt(id, 10))
	return ok(w, map[string]any{"ok": true})
}

func (s *Server) handleTestChannel(w http.ResponseWriter, r *http.Request, p *project) error {
	id, err := pathInt(r, "cid")
	if err != nil {
		return err
	}
	ch, err := s.loadChannel(r.Context(), p.ID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	err = s.alerts.send(r.Context(), ch, alert{Kind: "test", Project: p.Name, Title: "Workshell test alert",
		Detail: "If you can read this, alerts for this project are working.", Link: s.link(fmt.Sprintf("/p/%d/alerts", p.ID))})
	if err != nil {
		return badRequest("webhook failed: %v", err)
	}
	return ok(w, map[string]any{"ok": true})
}

func (s *Server) handleSaveRule(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	var body struct {
		Name            *string  `json:"name"`
		Kind            *string  `json:"kind"`
		Threshold       *float64 `json:"threshold"`
		WindowMinutes   *int     `json:"windowMinutes"`
		CooldownMinutes *int     `json:"cooldownMinutes"`
		ChannelID       *int64   `json:"channelId"` // 0 = none
		Enabled         *bool    `json:"enabled"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	rule := &ruleRow{Kind: "new_issue", WindowMinutes: 5, CooldownMinutes: 30, Enabled: true, projectID: p.ID}
	if r.PathValue("rid") != "" {
		id, err := pathInt(r, "rid")
		if err != nil {
			return err
		}
		rule, err = scanRule(s.db.R.QueryRowContext(r.Context(), `SELECT `+ruleColumns+` FROM alert_rules WHERE id = ? AND project_id = ?`, id, p.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return err
		}
	}
	if body.Name != nil {
		rule.Name = strings.TrimSpace(*body.Name)
	}
	if body.Kind != nil {
		if _, found := ruleKinds[*body.Kind]; !found {
			return badRequest("unknown rule kind")
		}
		rule.Kind = *body.Kind
	}
	if body.Threshold != nil {
		rule.Threshold = max(*body.Threshold, 0)
	}
	if body.WindowMinutes != nil {
		rule.WindowMinutes = min(max(*body.WindowMinutes, 1), 1440)
	}
	if body.CooldownMinutes != nil {
		rule.CooldownMinutes = min(max(*body.CooldownMinutes, 0), 10080)
	}
	if body.ChannelID != nil {
		if *body.ChannelID == 0 {
			rule.ChannelID = nil
		} else {
			if _, err := s.loadChannel(r.Context(), p.ID, *body.ChannelID); err != nil {
				return badRequest("no such channel")
			}
			rule.ChannelID = body.ChannelID
		}
	}
	if body.Enabled != nil {
		rule.Enabled = *body.Enabled
	}
	if rule.Name == "" {
		rule.Name = ruleKinds[rule.Kind].label
	}
	if len(rule.Name) > 80 {
		return badRequest("rule name must be at most 80 characters")
	}
	if rule.ID == 0 {
		res, err := s.db.W.ExecContext(r.Context(), `INSERT INTO alert_rules (project_id, name, kind, threshold, window_minutes, cooldown_minutes, channel_id, enabled, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, p.ID, rule.Name, rule.Kind, rule.Threshold, rule.WindowMinutes, rule.CooldownMinutes, rule.ChannelID, rule.Enabled, nowMs())
		if err != nil {
			return err
		}
		rule.ID, _ = res.LastInsertId()
	} else {
		_, err := s.db.W.ExecContext(r.Context(), `UPDATE alert_rules SET name = ?, kind = ?, threshold = ?, window_minutes = ?, cooldown_minutes = ?, channel_id = ?, enabled = ? WHERE id = ?`,
			rule.Name, rule.Kind, rule.Threshold, rule.WindowMinutes, rule.CooldownMinutes, rule.ChannelID, rule.Enabled, rule.ID)
		if err != nil {
			return err
		}
	}
	s.audit(r, p.ID, u, "alerts.rule.save", rule.Name)
	return ok(w, rule)
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	id, err := pathInt(r, "rid")
	if err != nil {
		return err
	}
	if _, err := s.db.W.ExecContext(r.Context(), `DELETE FROM alert_rules WHERE id = ? AND project_id = ?`, id, p.ID); err != nil {
		return err
	}
	s.audit(r, p.ID, u, "alerts.rule.delete", strconv.FormatInt(id, 10))
	return ok(w, map[string]any{"ok": true})
}
