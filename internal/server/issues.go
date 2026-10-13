package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type userRef struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Handle string `json:"handle"`
	Avatar string `json:"avatar"`
}

type issueRow struct {
	ID           int64     `json:"id"`
	Title        string    `json:"title"`
	Culprit      string    `json:"culprit"`
	Level        string    `json:"level"`
	Side         string    `json:"side"`
	Status       string    `json:"status"`
	FirstSeen    int64     `json:"firstSeen"`
	LastSeen     int64     `json:"lastSeen"`
	EventCount   int64     `json:"eventCount"`
	UserCount    int64     `json:"userCount"`
	RangeEvents  int64     `json:"rangeEvents"`
	FirstRelease string    `json:"firstRelease"`
	LastRelease  string    `json:"lastRelease"`
	ResolvedAt   *int64    `json:"resolvedAt"`
	RegressedAt  *int64    `json:"regressedAt"`
	CommentCount int64     `json:"commentCount"`
	GitHubURL    string    `json:"githubUrl"`
	GitHubNumber *int64    `json:"githubNumber"`
	Assignee     *userRef  `json:"assignee"`
	Spark        []float64 `json:"spark,omitempty"`
}

const issueSelect = `i.id, i.title, i.culprit, i.level, i.side, i.status, i.first_seen, i.last_seen,
	i.event_count, i.user_count, i.first_release, i.last_release, i.resolved_at, i.regressed_at,
	i.comment_count, i.github_url, i.github_number, u.id, u.name, u.handle, u.avatar`

func scanIssue(row interface{ Scan(...any) error }, extra ...any) (*issueRow, error) {
	var x issueRow
	var uid sql.NullInt64
	var uname, uhandle, uavatar sql.NullString
	dest := []any{&x.ID, &x.Title, &x.Culprit, &x.Level, &x.Side, &x.Status, &x.FirstSeen, &x.LastSeen,
		&x.EventCount, &x.UserCount, &x.FirstRelease, &x.LastRelease, &x.ResolvedAt, &x.RegressedAt,
		&x.CommentCount, &x.GitHubURL, &x.GitHubNumber, &uid, &uname, &uhandle, &uavatar}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return nil, err
	}
	if uid.Valid {
		x.Assignee = &userRef{ID: uid.Int64, Name: uname.String, Handle: uhandle.String, Avatar: uavatar.String}
	}
	return &x, nil
}

type issueQuery struct {
	Status   string // unresolved | resolved | ignored | all
	Side     string // server | client | ""
	Search   string
	Sort     string // last_seen | first_seen | events | users
	Assignee string // "" | me | none | <id>
	Release  string
	From     int64 // count events since; also filters to issues seen since
	Limit    int
	Offset   int
	Me       int64
}

const sparkBuckets = 24

func (s *Server) queryIssues(r *http.Request, projectID int64, q issueQuery) ([]issueRow, error) {
	where := []string{"i.project_id = ?"}
	args := []any{q.From, projectID}
	if q.Status != "" && q.Status != "all" {
		where = append(where, "i.status = ?")
		args = append(args, q.Status)
	}
	if q.Side == "server" || q.Side == "client" {
		where = append(where, "i.side = ?")
		args = append(args, q.Side)
	}
	if q.Search != "" {
		where = append(where, "(i.title LIKE ? ESCAPE '\\' OR i.culprit LIKE ? ESCAPE '\\')")
		like := "%" + escapeLike(q.Search) + "%"
		args = append(args, like, like)
	}
	switch q.Assignee {
	case "":
	case "none":
		where = append(where, "i.assignee_id IS NULL")
	case "me":
		where = append(where, "i.assignee_id = ?")
		args = append(args, q.Me)
	default:
		if id, err := strconv.ParseInt(q.Assignee, 10, 64); err == nil {
			where = append(where, "i.assignee_id = ?")
			args = append(args, id)
		}
	}
	if q.Release != "" {
		where = append(where, "i.first_release = ?")
		args = append(args, q.Release)
	}
	if q.From > 0 {
		where = append(where, "i.last_seen >= ?")
		args = append(args, q.From)
	}
	order := "i.last_seen DESC"
	switch q.Sort {
	case "first_seen":
		order = "i.first_seen DESC"
	case "events":
		order = "range_events DESC, i.last_seen DESC"
	case "users":
		order = "i.user_count DESC, i.last_seen DESC"
	}
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 50
	}
	args = append(args, q.Limit, q.Offset)

	rows, err := s.db.R.QueryContext(r.Context(), `
		SELECT `+issueSelect+`,
			COALESCE((SELECT SUM(e.count) FROM error_events e WHERE e.issue_id = i.id AND e.ts >= ?), 0) AS range_events
		FROM issues i LEFT JOIN users u ON u.id = i.assignee_id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY `+order+`
		LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []issueRow{}
	for rows.Next() {
		var rangeEvents int64
		x, err := scanIssue(rows, &rangeEvents)
		if err != nil {
			return nil, err
		}
		x.RangeEvents = rangeEvents
		out = append(out, *x)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return out, s.fillSparklines(r, out)
}

// fillSparklines attaches a 24-hour hourly event histogram to each issue.
func (s *Server) fillSparklines(r *http.Request, issues []issueRow) error {
	if len(issues) == 0 {
		return nil
	}
	const bucket = int64(time.Hour / time.Millisecond)
	to := (nowMs()/bucket + 1) * bucket
	from := to - sparkBuckets*bucket
	index := map[int64]int{}
	ids := make([]string, len(issues))
	args := []any{from, bucket}
	for i := range issues {
		issues[i].Spark = make([]float64, sparkBuckets)
		index[issues[i].ID] = i
		ids[i] = "?"
		args = append(args, issues[i].ID)
	}
	rows, err := s.db.R.QueryContext(r.Context(), `
		SELECT issue_id, (ts - ?1) / ?2, SUM(count) FROM error_events
		WHERE ts >= ?1 AND issue_id IN (`+strings.Join(ids, ",")+`)
		GROUP BY 1, 2`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, b int64
		var n float64
		if err := rows.Scan(&id, &b, &n); err != nil {
			return err
		}
		if b >= 0 && b < sparkBuckets {
			issues[index[id]].Spark[b] = n
		}
	}
	return rows.Err()
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (s *Server) handleListIssues(w http.ResponseWriter, r *http.Request, p *project) error {
	u, _ := s.currentUser(r)
	q := r.URL.Query()
	tr := parseRange(r, "30d")
	issueQ := issueQuery{
		Status:   q.Get("status"),
		Side:     q.Get("side"),
		Search:   strings.TrimSpace(q.Get("q")),
		Sort:     q.Get("sort"),
		Assignee: q.Get("assignee"),
		Release:  q.Get("release"),
		From:     tr.From,
		Limit:    int(queryInt(r, "limit", 50)),
		Offset:   int(queryInt(r, "offset", 0)),
	}
	if u != nil {
		issueQ.Me = u.ID
	}
	if issueQ.Status == "" {
		issueQ.Status = "unresolved"
	}
	issues, err := s.queryIssues(r, p.ID, issueQ)
	if err != nil {
		return err
	}
	var counts struct {
		Unresolved int `json:"unresolved"`
		Resolved   int `json:"resolved"`
		Ignored    int `json:"ignored"`
		Mine       int `json:"mine"`
	}
	err = s.db.R.QueryRowContext(r.Context(), `
		SELECT
			COALESCE(SUM(status = 'unresolved'), 0),
			COALESCE(SUM(status = 'resolved'), 0),
			COALESCE(SUM(status = 'ignored'), 0),
			COALESCE(SUM(status = 'unresolved' AND assignee_id = ?3), 0)
		FROM issues WHERE project_id = ?1 AND last_seen >= ?2`, p.ID, tr.From, issueQ.Me,
	).Scan(&counts.Unresolved, &counts.Resolved, &counts.Ignored, &counts.Mine)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"issues": issues, "counts": counts})
}

func (s *Server) loadIssue(r *http.Request, projectID int64) (*issueRow, error) {
	id, err := pathInt(r, "iid")
	if err != nil {
		return nil, err
	}
	x, err := scanIssue(s.db.R.QueryRowContext(r.Context(), `
		SELECT `+issueSelect+` FROM issues i LEFT JOIN users u ON u.id = i.assignee_id
		WHERE i.id = ? AND i.project_id = ?`, id, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	return x, err
}

type errorEvent struct {
	ID           int64           `json:"id"`
	TS           int64           `json:"ts"`
	Count        int64           `json:"count"`
	Message      string          `json:"message"`
	Stack        string          `json:"stack"`
	Frames       []frame         `json:"frames"`
	Level        string          `json:"level"`
	Side         string          `json:"side"`
	JobID        string          `json:"jobId"`
	PlaceVersion string          `json:"placeVersion"`
	Release      string          `json:"release"`
	Environment  string          `json:"environment"`
	UserID       *int64          `json:"userId"`
	SessionKey   string          `json:"sessionKey"`
	Script       string          `json:"script"`
	Tags         json.RawMessage `json:"tags"`
	Context      json.RawMessage `json:"context"`
	Breadcrumbs  json.RawMessage `json:"breadcrumbs"`
}

func (s *Server) queryErrorEvents(r *http.Request, p *project, query string, args ...any) ([]errorEvent, error) {
	rows, err := s.db.R.QueryContext(r.Context(), `
		SELECT id, ts, count, message, stack, level, side, job_id, place_version, release, environment, user_id,
			session_key, script, tags, context, breadcrumbs
		FROM error_events `+query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []errorEvent{}
	for rows.Next() {
		var e errorEvent
		var tags, ctx, crumbs string
		if err := rows.Scan(&e.ID, &e.TS, &e.Count, &e.Message, &e.Stack, &e.Level, &e.Side, &e.JobID,
			&e.PlaceVersion, &e.Release, &e.Environment, &e.UserID, &e.SessionKey, &e.Script, &tags, &ctx, &crumbs); err != nil {
			return nil, err
		}
		e.Tags, e.Context, e.Breadcrumbs = json.RawMessage(tags), json.RawMessage(ctx), json.RawMessage(crumbs)
		e.Frames = parseStack(e.Stack)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range out {
		s.resolveFrames(r.Context(), p, out[i].Release, out[i].Frames)
	}
	return out, nil
}

type tagCount struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

func (s *Server) handleGetIssue(w http.ResponseWriter, r *http.Request, p *project) error {
	issue, err := s.loadIssue(r, p.ID)
	if err != nil {
		return err
	}
	tr := parseRange(r, "7d")

	hist := newTimeseries(tr, "events")
	rows, err := s.db.R.QueryContext(r.Context(), `
		SELECT (ts - ?2) / ?4 * ?4 + ?2, SUM(count) FROM error_events
		WHERE issue_id = ?1 AND ts >= ?2 AND ts < ?3 GROUP BY 1`,
		issue.ID, tr.From, tr.To, tr.Bucket)
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
		hist.set(0, b, v)
	}
	rows.Close()

	// Tag distributions across the issue's stored events.
	breakdown := map[string][]tagCount{}
	for name, col := range map[string]string{
		"release":     "release",
		"environment": "environment",
		"server":      "job_id",
	} {
		rows, err := s.db.R.QueryContext(r.Context(), `
			SELECT `+col+`, SUM(count) FROM error_events WHERE issue_id = ?
			GROUP BY 1 ORDER BY 2 DESC LIMIT 6`, issue.ID)
		if err != nil {
			return err
		}
		list := []tagCount{}
		for rows.Next() {
			var t tagCount
			if err := rows.Scan(&t.Value, &t.Count); err != nil {
				rows.Close()
				return err
			}
			list = append(list, t)
		}
		rows.Close()
		breakdown[name] = list
	}

	latest, err := s.queryErrorEvents(r, p, `WHERE issue_id = ? ORDER BY ts DESC, id DESC LIMIT 1`, issue.ID)
	if err != nil {
		return err
	}
	var latestEvent *errorEvent
	if len(latest) > 0 {
		latestEvent = &latest[0]
	}

	var firstRelease *releaseRow
	if issue.FirstRelease != "" {
		firstRelease, _ = s.loadRelease(r.Context(), p, issue.FirstRelease)
	}
	return ok(w, map[string]any{
		"issue":        issue,
		"histogram":    hist,
		"breakdown":    breakdown,
		"latest":       latestEvent,
		"firstRelease": firstRelease,
	})
}

func (s *Server) handleIssueEvents(w http.ResponseWriter, r *http.Request, p *project) error {
	issue, err := s.loadIssue(r, p.ID)
	if err != nil {
		return err
	}
	before := queryInt(r, "before", 1<<62)
	limit := min(max(queryInt(r, "limit", 25), 1), 100)
	events, err := s.queryErrorEvents(r, p, `WHERE issue_id = ? AND id < ? ORDER BY id DESC LIMIT ?`, issue.ID, before, limit)
	if err != nil {
		return err
	}
	return ok(w, events)
}

func validStatus(status string) bool {
	return status == "unresolved" || status == "resolved" || status == "ignored"
}

// setIssueStatus updates status and records activity for each changed issue.
func (s *Server) setIssueStatus(r *http.Request, projectID int64, ids []int64, status string, u *user) error {
	if !validStatus(status) {
		return badRequest("invalid status")
	}
	now := nowMs()
	for _, id := range ids {
		res, err := s.db.W.ExecContext(r.Context(), `
			UPDATE issues SET status = ?3,
				resolved_at = CASE WHEN ?3 = 'resolved' THEN ?4 ELSE NULL END
			WHERE id = ?1 AND project_id = ?2 AND status != ?3`, id, projectID, status, now)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			if err := s.addActivity(r, id, u, "status", "", map[string]any{"status": status}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) addActivity(r *http.Request, issueID int64, u *user, kind, body string, data map[string]any) error {
	var uid any
	if u != nil {
		uid = u.ID
	}
	d, _ := json.Marshal(data)
	if data == nil {
		d = []byte("{}")
	}
	_, err := s.db.W.ExecContext(r.Context(), `INSERT INTO issue_activity (issue_id, user_id, kind, body, data, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		issueID, uid, kind, body, string(d), nowMs())
	return err
}

func (s *Server) notify(r *http.Request, userID, projectID, issueID int64, actor *user, kind, body string) {
	if actor != nil && actor.ID == userID {
		return // don't notify people about their own actions
	}
	var aid any
	if actor != nil {
		aid = actor.ID
	}
	_, err := s.db.W.ExecContext(r.Context(), `INSERT INTO notifications (user_id, project_id, issue_id, actor_id, kind, body, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		userID, projectID, issueID, aid, kind, truncate(body, 300), nowMs())
	if err != nil {
		s.log.Warn("notify", "err", err)
	}
}

func (s *Server) handleUpdateIssue(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	issue, err := s.loadIssue(r, p.ID)
	if err != nil {
		return err
	}
	var body struct {
		Status   *string `json:"status"`
		Assignee *int64  `json:"assigneeId"` // 0 = unassign
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if body.Status != nil {
		if err := s.setIssueStatus(r, p.ID, []int64{issue.ID}, *body.Status, u); err != nil {
			return err
		}
	}
	if body.Assignee != nil {
		var assignee any
		var name string
		if *body.Assignee > 0 {
			if err := s.db.R.QueryRowContext(r.Context(), `SELECT name FROM users WHERE id = ?`, *body.Assignee).Scan(&name); err != nil {
				return badRequest("no such user")
			}
			assignee = *body.Assignee
		}
		if _, err := s.db.W.ExecContext(r.Context(), `UPDATE issues SET assignee_id = ? WHERE id = ?`, assignee, issue.ID); err != nil {
			return err
		}
		if err := s.addActivity(r, issue.ID, u, "assign", "", map[string]any{"assigneeId": *body.Assignee, "assigneeName": name}); err != nil {
			return err
		}
		if *body.Assignee > 0 {
			s.notify(r, *body.Assignee, p.ID, issue.ID, u, "assigned", issue.Title)
		}
	}
	return ok(w, map[string]any{"ok": true})
}

func (s *Server) handleBulkIssues(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	var body struct {
		IDs    []int64 `json:"ids"`
		Status string  `json:"status"`
		Delete bool    `json:"delete"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if len(body.IDs) > 500 {
		return badRequest("too many issues")
	}
	if body.Delete {
		for _, id := range body.IDs {
			if _, err := s.db.W.ExecContext(r.Context(), `DELETE FROM issues WHERE id = ? AND project_id = ?`, id, p.ID); err != nil {
				return err
			}
		}
		s.audit(r, p.ID, u, "issues.delete", fmt.Sprintf("%d issues", len(body.IDs)))
	} else if err := s.setIssueStatus(r, p.ID, body.IDs, body.Status, u); err != nil {
		return err
	}
	return ok(w, map[string]any{"ok": true})
}

func (s *Server) handleDeleteIssue(w http.ResponseWriter, r *http.Request, p *project) error {
	issue, err := s.loadIssue(r, p.ID)
	if err != nil {
		return err
	}
	if _, err := s.db.W.ExecContext(r.Context(), `DELETE FROM issues WHERE id = ?`, issue.ID); err != nil {
		return err
	}
	return ok(w, map[string]any{"ok": true})
}

// ---- Comments & activity ------------------------------------------------------

type activityRow struct {
	ID        int64           `json:"id"`
	Kind      string          `json:"kind"`
	Body      string          `json:"body"`
	Data      json.RawMessage `json:"data"`
	CreatedAt int64           `json:"createdAt"`
	EditedAt  *int64          `json:"editedAt"`
	User      *userRef        `json:"user"`
}

func (s *Server) handleIssueActivity(w http.ResponseWriter, r *http.Request, p *project) error {
	issue, err := s.loadIssue(r, p.ID)
	if err != nil {
		return err
	}
	rows, err := s.db.R.QueryContext(r.Context(), `
		SELECT a.id, a.kind, a.body, a.data, a.created_at, a.edited_at, u.id, u.name, u.handle, u.avatar
		FROM issue_activity a LEFT JOIN users u ON u.id = a.user_id
		WHERE a.issue_id = ? ORDER BY a.created_at, a.id LIMIT 500`, issue.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []activityRow{}
	for rows.Next() {
		var a activityRow
		var data string
		var uid sql.NullInt64
		var name, handle, avatar sql.NullString
		if err := rows.Scan(&a.ID, &a.Kind, &a.Body, &data, &a.CreatedAt, &a.EditedAt, &uid, &name, &handle, &avatar); err != nil {
			return err
		}
		a.Data = json.RawMessage(data)
		if uid.Valid {
			a.User = &userRef{ID: uid.Int64, Name: name.String, Handle: handle.String, Avatar: avatar.String}
		}
		out = append(out, a)
	}
	return ok(w, out)
}

var mentionPattern = regexp.MustCompile(`(?:^|[^\w@])@([a-z0-9][a-z0-9_-]{1,31})`)

func (s *Server) handleAddComment(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	issue, err := s.loadIssue(r, p.ID)
	if err != nil {
		return err
	}
	var body struct {
		Body         string `json:"body"`
		PostToGitHub bool   `json:"postToGithub"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	text := strings.TrimSpace(body.Body)
	if text == "" || len(text) > 10_000 {
		return badRequest("comments must be 1-10,000 characters")
	}
	text = truncate(text, 10_000)
	if err := s.addActivity(r, issue.ID, u, "comment", text, nil); err != nil {
		return err
	}
	if _, err := s.db.W.ExecContext(r.Context(), `UPDATE issues SET comment_count = comment_count + 1 WHERE id = ?`, issue.ID); err != nil {
		return err
	}

	notified := map[int64]bool{}
	for _, m := range mentionPattern.FindAllStringSubmatch(text, 20) {
		var uid int64
		if s.db.R.QueryRowContext(r.Context(), `SELECT id FROM users WHERE handle = ?`, m[1]).Scan(&uid) == nil && !notified[uid] {
			notified[uid] = true
			s.notify(r, uid, p.ID, issue.ID, u, "mention", text)
		}
	}
	if issue.Assignee != nil && !notified[issue.Assignee.ID] {
		s.notify(r, issue.Assignee.ID, p.ID, issue.ID, u, "comment", text)
	}

	if body.PostToGitHub && issue.GitHubNumber != nil {
		if err := s.githubComment(r.Context(), p, *issue.GitHubNumber, fmt.Sprintf("**%s** commented in Workshell:\n\n%s", u.Name, text)); err != nil {
			return badRequest("comment saved, but posting to GitHub failed: %v", err)
		}
	}
	return ok(w, map[string]any{"ok": true})
}

func (s *Server) loadComment(r *http.Request, issueID int64) (*activityRow, int64, error) {
	cid, err := pathInt(r, "cid")
	if err != nil {
		return nil, 0, err
	}
	var a activityRow
	var author sql.NullInt64
	err = s.db.R.QueryRowContext(r.Context(), `SELECT id, kind, body, user_id FROM issue_activity WHERE id = ? AND issue_id = ? AND kind = 'comment'`,
		cid, issueID).Scan(&a.ID, &a.Kind, &a.Body, &author)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, errNotFound
	}
	return &a, author.Int64, err
}

func (s *Server) handleEditComment(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	issue, err := s.loadIssue(r, p.ID)
	if err != nil {
		return err
	}
	c, author, err := s.loadComment(r, issue.ID)
	if err != nil {
		return err
	}
	if author != u.ID {
		return errStatus(http.StatusForbidden, "you can only edit your own comments")
	}
	var body struct {
		Body string `json:"body"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	text := strings.TrimSpace(body.Body)
	if text == "" || len(text) > 10_000 {
		return badRequest("comments must be 1-10,000 characters")
	}
	_, err = s.db.W.ExecContext(r.Context(), `UPDATE issue_activity SET body = ?, edited_at = ? WHERE id = ?`, truncate(text, 10_000), nowMs(), c.ID)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"ok": true})
}

func (s *Server) handleDeleteComment(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	issue, err := s.loadIssue(r, p.ID)
	if err != nil {
		return err
	}
	c, author, err := s.loadComment(r, issue.ID)
	if err != nil {
		return err
	}
	if author != u.ID && !u.IsAdmin {
		return errStatus(http.StatusForbidden, "you can only delete your own comments")
	}
	if _, err := s.db.W.ExecContext(r.Context(), `DELETE FROM issue_activity WHERE id = ?`, c.ID); err != nil {
		return err
	}
	_, err = s.db.W.ExecContext(r.Context(), `UPDATE issues SET comment_count = MAX(comment_count - 1, 0) WHERE id = ?`, issue.ID)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"ok": true})
}
