package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

type releaseRow struct {
	Version      string `json:"version"`
	CommitSHA    string `json:"commitSha"`
	Branch       string `json:"branch"`
	Message      string `json:"message"`
	Author       string `json:"author"`
	PlaceVersion string `json:"placeVersion"`
	CreatedAt    int64  `json:"createdAt"`
	FirstSeen    *int64 `json:"firstSeen"`
	LastSeen     *int64 `json:"lastSeen"`
	HasSourcemap bool   `json:"hasSourcemap"`
	CommitURL    string `json:"commitUrl,omitempty"`
}

const releaseColumns = `version, commit_sha, branch, message, author, place_version, created_at, first_seen, last_seen, sourcemap != '{}'`

func scanRelease(row interface{ Scan(...any) error }, p *project) (*releaseRow, error) {
	var x releaseRow
	if err := row.Scan(&x.Version, &x.CommitSHA, &x.Branch, &x.Message, &x.Author, &x.PlaceVersion, &x.CreatedAt,
		&x.FirstSeen, &x.LastSeen, &x.HasSourcemap); err != nil {
		return nil, err
	}
	if p != nil && p.GitHubRepo != "" && x.CommitSHA != "" {
		x.CommitURL = fmt.Sprintf("https://github.com/%s/commit/%s", p.GitHubRepo, x.CommitSHA)
	}
	return &x, nil
}

func (s *Server) loadRelease(ctx context.Context, p *project, version string) (*releaseRow, error) {
	return scanRelease(s.db.R.QueryRowContext(ctx, `SELECT `+releaseColumns+` FROM releases WHERE project_id = ? AND version = ?`, p.ID, version), p)
}

// sourcemapNode mirrors `rojo sourcemap` output.
type sourcemapNode struct {
	Name      string          `json:"name"`
	ClassName string          `json:"className"`
	FilePaths []string        `json:"filePaths"`
	Children  []sourcemapNode `json:"children"`
}

// flattenSourcemap maps instance paths (as they appear in Roblox stack traces,
// e.g. "ServerScriptService.Combat.Weapon") to the script's source file.
func flattenSourcemap(root sourcemapNode) map[string]string {
	out := map[string]string{}
	var walk func(n sourcemapNode, path string)
	walk = func(n sourcemapNode, path string) {
		for _, f := range n.FilePaths {
			if strings.HasSuffix(f, ".lua") || strings.HasSuffix(f, ".luau") {
				out[path] = strings.TrimPrefix(strings.ReplaceAll(f, `\`, "/"), "./")
				break
			}
		}
		for _, c := range n.Children {
			child := c.Name
			if path != "" {
				child = path + "." + c.Name
			}
			walk(c, child)
		}
	}
	if root.ClassName == "DataModel" {
		walk(root, "")
	} else {
		// A model/package project: paths are relative to wherever it's mounted.
		walk(root, root.Name)
	}
	return out
}

// handleCreateRelease registers a release from CI (`workshell release`):
// version, git metadata and optionally a Rojo sourcemap. Auth: ingest key.
func (s *Server) handleCreateRelease(w http.ResponseWriter, r *http.Request) error {
	p, body, err := s.authenticateIngest(r)
	if err != nil {
		return err
	}
	var in struct {
		Version      string          `json:"version"`
		Commit       string          `json:"commit"`
		Branch       string          `json:"branch"`
		Message      string          `json:"message"`
		Author       string          `json:"author"`
		PlaceVersion string          `json:"placeVersion"`
		Sourcemap    json.RawMessage `json:"sourcemap"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return badRequest("invalid JSON: %v", err)
	}
	in.Version = strings.TrimSpace(in.Version)
	if in.Version == "" || len(in.Version) > 64 {
		return badRequest("version must be 1-64 characters")
	}
	smap := "{}"
	if len(in.Sourcemap) > 0 && string(in.Sourcemap) != "null" {
		var root sourcemapNode
		if err := json.Unmarshal(in.Sourcemap, &root); err != nil {
			return badRequest("sourcemap is not valid `rojo sourcemap` output: %v", err)
		}
		b, _ := json.Marshal(flattenSourcemap(root))
		smap = string(b)
	}
	_, err = s.db.W.ExecContext(r.Context(), `
		INSERT INTO releases (project_id, version, commit_sha, branch, message, author, place_version, created_at, sourcemap)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9)
		ON CONFLICT (project_id, version) DO UPDATE SET
			commit_sha = excluded.commit_sha, branch = excluded.branch, message = excluded.message,
			author = excluded.author,
			place_version = CASE WHEN excluded.place_version != '' THEN excluded.place_version ELSE place_version END,
			sourcemap = CASE WHEN excluded.sourcemap != '{}' THEN excluded.sourcemap ELSE sourcemap END`,
		p.ID, in.Version, truncate(in.Commit, 64), truncate(in.Branch, 128), truncate(in.Message, 2000), truncate(in.Author, 128),
		truncate(in.PlaceVersion, 32), nowMs(), smap)
	if err != nil {
		return err
	}
	sourcemaps.forget(p.ID, in.Version)
	var m map[string]string
	json.Unmarshal([]byte(smap), &m)
	return ok(w, map[string]any{"version": in.Version, "sourcemapScripts": len(m)})
}

// sourcemapCache keeps parsed release sourcemaps in memory.
type sourcemapCache struct {
	mu sync.Mutex
	m  map[string]map[string]string
}

var sourcemaps = sourcemapCache{m: map[string]map[string]string{}}

func (c *sourcemapCache) key(pid int64, version string) string {
	return fmt.Sprintf("%d\x00%s", pid, version)
}

func (c *sourcemapCache) forget(pid int64, version string) {
	c.mu.Lock()
	delete(c.m, c.key(pid, version))
	c.mu.Unlock()
}

func (s *Server) sourcemapFor(ctx context.Context, pid int64, version string) (map[string]string, string) {
	k := sourcemaps.key(pid, version)
	sourcemaps.mu.Lock()
	m, found := sourcemaps.m[k]
	sourcemaps.mu.Unlock()
	var commit string
	if !found {
		var raw string
		err := s.db.R.QueryRowContext(ctx, `SELECT sourcemap, commit_sha FROM releases WHERE project_id = ? AND version = ?`, pid, version).Scan(&raw, &commit)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, ""
		}
		m = map[string]string{}
		json.Unmarshal([]byte(raw), &m)
		m["\x00commit"] = commit
		sourcemaps.mu.Lock()
		if len(sourcemaps.m) > 500 {
			clear(sourcemaps.m)
		}
		sourcemaps.m[k] = m
		sourcemaps.mu.Unlock()
	}
	return m, m["\x00commit"]
}

// clientPathAliases maps where client scripts run to where they live in the
// place file, so client stack traces resolve too.
var clientPathAliases = []struct{ container, starter string }{
	{"PlayerScripts", "StarterPlayer.StarterPlayerScripts"},
	{"PlayerGui", "StarterGui"},
	{"Backpack", "StarterPack"},
}

func lookupScript(m map[string]string, script string) string {
	if f, found := m[script]; found {
		return f
	}
	// Players.<name>.PlayerGui.X -> StarterGui.X, etc.
	if rest, found := strings.CutPrefix(script, "Players."); found {
		if _, after, ok := strings.Cut(rest, "."); ok {
			for _, a := range clientPathAliases {
				if tail, ok := strings.CutPrefix(after, a.container+"."); ok {
					if f, found := m[a.starter+"."+tail]; found {
						return f
					}
				}
			}
		}
	}
	// Workspace.<character>.X -> StarterPlayer.StarterCharacterScripts.X
	if rest, found := strings.CutPrefix(script, "Workspace."); found {
		if _, tail, ok := strings.Cut(rest, "."); ok {
			if f, found := m["StarterPlayer.StarterCharacterScripts."+tail]; found {
				return f
			}
		}
	}
	return ""
}

// resolveFrames fills in source files (and GitHub links) for stack frames
// using the release's Rojo sourcemap.
func (s *Server) resolveFrames(ctx context.Context, p *project, release string, frames []frame) {
	if release == "" || len(frames) == 0 {
		return
	}
	m, commit := s.sourcemapFor(ctx, p.ID, release)
	if len(m) <= 1 {
		return
	}
	for i := range frames {
		f := lookupScript(m, frames[i].Script)
		if f == "" {
			continue
		}
		frames[i].File = f
		if p.GitHubRepo != "" && commit != "" {
			frames[i].URL = fmt.Sprintf("https://github.com/%s/blob/%s/%s#L%d", p.GitHubRepo, url.PathEscape(commit), escapePath(f), max(frames[i].Line, 1))
		}
	}
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, x := range parts {
		parts[i] = url.PathEscape(x)
	}
	return strings.Join(parts, "/")
}

type releaseStats struct {
	releaseRow
	Errors        int64   `json:"errors"`
	NewIssues     int64   `json:"newIssues"`
	Sessions      int64   `json:"sessions"`
	ErrorFreeRate float64 `json:"errorFreeRate"`
	LiveServers   int64   `json:"liveServers"`
	LivePlayers   int64   `json:"livePlayers"`
	AvgSessionMs  float64 `json:"avgSessionMs"`
}

func (s *Server) releaseStats(ctx context.Context, p *project, rel *releaseRow) (*releaseStats, error) {
	st := &releaseStats{releaseRow: *rel}
	cutoff := nowMs() - liveWindow.Milliseconds()
	var errorSessions int64
	err := s.db.R.QueryRowContext(ctx, `
		SELECT
			(SELECT COALESCE(SUM(count), 0) FROM error_events WHERE project_id = ?1 AND release = ?2),
			(SELECT COUNT(*) FROM issues WHERE project_id = ?1 AND first_release = ?2),
			(SELECT COUNT(*) FROM play_sessions WHERE project_id = ?1 AND release = ?2),
			(SELECT COUNT(*) FROM play_sessions WHERE project_id = ?1 AND release = ?2 AND error_count > 0),
			(SELECT COUNT(*) FROM game_servers WHERE project_id = ?1 AND release = ?2 AND closed_at IS NULL AND last_seen > ?3),
			(SELECT COALESCE(SUM(players), 0) FROM game_servers WHERE project_id = ?1 AND release = ?2 AND closed_at IS NULL AND last_seen > ?3),
			(SELECT COALESCE(AVG(ended_at - started_at), 0) FROM play_sessions WHERE project_id = ?1 AND release = ?2 AND ended_at IS NOT NULL)`,
		p.ID, rel.Version, cutoff,
	).Scan(&st.Errors, &st.NewIssues, &st.Sessions, &errorSessions, &st.LiveServers, &st.LivePlayers, &st.AvgSessionMs)
	if err != nil {
		return nil, err
	}
	if st.Sessions > 0 {
		st.ErrorFreeRate = 1 - float64(errorSessions)/float64(st.Sessions)
	} else {
		st.ErrorFreeRate = -1
	}
	return st, nil
}

func (s *Server) handleListReleases(w http.ResponseWriter, r *http.Request, p *project) error {
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT `+releaseColumns+` FROM releases WHERE project_id = ?
		ORDER BY COALESCE(first_seen, created_at) DESC LIMIT 100`, p.ID)
	if err != nil {
		return err
	}
	var list []*releaseRow
	for rows.Next() {
		x, err := scanRelease(rows, p)
		if err != nil {
			rows.Close()
			return err
		}
		list = append(list, x)
	}
	rows.Close()
	out := []*releaseStats{}
	for _, rel := range list {
		st, err := s.releaseStats(r.Context(), p, rel)
		if err != nil {
			return err
		}
		out = append(out, st)
	}
	return ok(w, out)
}

func (s *Server) handleGetRelease(w http.ResponseWriter, r *http.Request, p *project) error {
	version := r.PathValue("version")
	rel, err := s.loadRelease(r.Context(), p, version)
	if errors.Is(err, sql.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	st, err := s.releaseStats(r.Context(), p, rel)
	if err != nil {
		return err
	}
	tr := parseRange(r, "7d")
	ts := newTimeseries(tr, "errors", "sessions")
	for i, q := range []string{
		`SELECT (ts - ?2) / ?4 * ?4 + ?2, SUM(count) FROM error_events WHERE project_id = ?1 AND release = ?5 AND ts >= ?2 AND ts < ?3 GROUP BY 1`,
		`SELECT (started_at - ?2) / ?4 * ?4 + ?2, COUNT(*) FROM play_sessions WHERE project_id = ?1 AND release = ?5 AND started_at >= ?2 AND started_at < ?3 GROUP BY 1`,
	} {
		rows, err := s.db.R.QueryContext(r.Context(), q, p.ID, tr.From, tr.To, tr.Bucket, version)
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
			ts.set(i, b, v)
		}
		rows.Close()
	}
	newIssues, err := s.queryIssues(r, p.ID, issueQuery{Status: "all", Release: version, Sort: "events", Limit: 50})
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"release": st, "series": ts, "newIssues": newIssues})
}
