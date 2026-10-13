package server

import (
	"net/http"
	"strconv"
	"strings"
)

type searchHit struct {
	Kind   string `json:"kind"` // issue | player | session | server | release | event
	ID     string `json:"id"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
}

// handleSearch powers the command palette: one query across issues, players,
// sessions, servers, releases and event names.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request, p *project) error {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	hits := []searchHit{}
	if q == "" || len(q) > 200 {
		return ok(w, hits)
	}
	ctx := r.Context()
	like := "%" + escapeLike(q) + "%"
	prefix := escapeLike(q) + "%"

	add := func(kind, query string, args ...any) error {
		rows, err := s.db.R.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var h searchHit
			if err := rows.Scan(&h.ID, &h.Title, &h.Detail); err != nil {
				return err
			}
			h.Kind = kind
			hits = append(hits, h)
		}
		return rows.Err()
	}

	if err := add("issue", `SELECT CAST(id AS TEXT), title, culprit FROM issues WHERE project_id = ? AND (title LIKE ? ESCAPE '\' OR culprit LIKE ? ESCAPE '\')
		ORDER BY status = 'unresolved' DESC, last_seen DESC LIMIT 6`, p.ID, like, like); err != nil {
		return err
	}
	if _, err := strconv.ParseInt(q, 10, 64); err == nil {
		ids := []any{q}
		if id, okID := lookupUserID(p, q, true); okID {
			ids = append(ids, strconv.FormatInt(id, 10))
		}
		for _, id := range ids {
			if err := add("player", `SELECT CAST(user_id AS TEXT), 'Player ' || user_id, session_count || ' sessions' FROM players WHERE project_id = ? AND user_id = ?`, p.ID, id); err != nil {
				return err
			}
		}
	}
	if len(q) >= 4 {
		if err := add("session", `SELECT session_key, 'Session ' || substr(session_key, 1, 8), 'Player ' || user_id FROM play_sessions
			WHERE project_id = ? AND session_key LIKE ? ESCAPE '\' ORDER BY started_at DESC LIMIT 4`, p.ID, prefix); err != nil {
			return err
		}
		if err := add("server", `SELECT job_id, 'Server ' || substr(job_id, 1, 8), release FROM game_servers
			WHERE project_id = ? AND job_id LIKE ? ESCAPE '\' ORDER BY last_seen DESC LIMIT 4`, p.ID, prefix); err != nil {
			return err
		}
	}
	if err := add("release", `SELECT version, version, commit_sha FROM releases WHERE project_id = ? AND (version LIKE ? ESCAPE '\' OR commit_sha LIKE ? ESCAPE '\')
		ORDER BY COALESCE(first_seen, created_at) DESC LIMIT 4`, p.ID, prefix, prefix); err != nil {
		return err
	}
	if err := add("event", `SELECT DISTINCT name, name, '' FROM events WHERE project_id = ? AND ts >= ? AND name LIKE ? ESCAPE '\' LIMIT 4`,
		p.ID, nowMs()-7*dayMs, like); err != nil {
		return err
	}
	return ok(w, hits)
}
