package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Whiteboards are infinite canvases for investigating issues: sticky notes,
// text, shapes, connectors, and snapshots of telemetry sent from elsewhere in
// the dashboard. Items are stored one row each and synced to everyone viewing
// the board through the project's live stream.

const (
	maxBoardItems    = 5000
	maxItemBatch     = 500
	maxItemDataBytes = 256 << 10
	maxCoord         = 1e7
	maxSize          = 1e5
	maxBoardsPerProj = 500
)

var (
	itemKinds  = map[string]bool{"sticky": true, "text": true, "shape": true, "connector": true, "embed": true, "frame": true}
	itemIDRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)
	clientIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{0,40}$`)
)

type boardRow struct {
	ID          int64      `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	CreatedBy   *userRef   `json:"createdBy"`
	CreatedAt   int64      `json:"createdAt"`
	UpdatedBy   *userRef   `json:"updatedBy"`
	UpdatedAt   int64      `json:"updatedAt"`
	ItemCount   int64      `json:"itemCount"`
	Preview     []previewR `json:"preview,omitempty"`
}

// previewR is an item's outline, enough to draw a thumbnail of the board.
type previewR struct {
	Kind  string  `json:"kind"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	W     float64 `json:"w"`
	H     float64 `json:"h"`
	Color string  `json:"color,omitempty"`
}

type boardItem struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	X         float64         `json:"x"`
	Y         float64         `json:"y"`
	W         float64         `json:"w"`
	H         float64         `json:"h"`
	Z         float64         `json:"z"`
	Data      json.RawMessage `json:"data"`
	Rev       int64           `json:"rev"`
	UpdatedBy *int64          `json:"updatedBy"`
	UpdatedAt int64           `json:"updatedAt"`
}

const boardColumns = `b.id, b.title, b.description, b.created_at, b.updated_at,
	cu.id, cu.name, cu.handle, cu.avatar, uu.id, uu.name, uu.handle, uu.avatar,
	(SELECT COUNT(*) FROM whiteboard_items i WHERE i.board_id = b.id)`

const boardJoins = ` FROM whiteboards b
	LEFT JOIN users cu ON cu.id = b.created_by
	LEFT JOIN users uu ON uu.id = b.updated_by`

func scanBoard(row interface{ Scan(...any) error }) (*boardRow, error) {
	var b boardRow
	var cID, uID sql.NullInt64
	var cName, cHandle, cAvatar, uName, uHandle, uAvatar sql.NullString
	if err := row.Scan(&b.ID, &b.Title, &b.Description, &b.CreatedAt, &b.UpdatedAt,
		&cID, &cName, &cHandle, &cAvatar, &uID, &uName, &uHandle, &uAvatar, &b.ItemCount); err != nil {
		return nil, err
	}
	b.CreatedBy = refOf(cID, cName, cHandle, cAvatar)
	b.UpdatedBy = refOf(uID, uName, uHandle, uAvatar)
	return &b, nil
}

func refOf(id sql.NullInt64, name, handle, avatar sql.NullString) *userRef {
	if !id.Valid {
		return nil
	}
	return &userRef{ID: id.Int64, Name: name.String, Handle: handle.String, Avatar: avatar.String}
}

func (s *Server) loadBoard(ctx context.Context, projectID int64, r *http.Request) (*boardRow, error) {
	id, err := pathInt(r, "bid")
	if err != nil {
		return nil, err
	}
	b, err := scanBoard(s.db.R.QueryRowContext(ctx, `SELECT `+boardColumns+boardJoins+` WHERE b.id = ? AND b.project_id = ?`, id, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	return b, err
}

func (s *Server) handleListBoards(w http.ResponseWriter, r *http.Request, p *project) error {
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT `+boardColumns+boardJoins+` WHERE b.project_id = ? ORDER BY b.updated_at DESC`, p.ID)
	if err != nil {
		return err
	}
	boards := []*boardRow{}
	byID := map[int64]*boardRow{}
	for rows.Next() {
		b, err := scanBoard(rows)
		if err != nil {
			rows.Close()
			return err
		}
		b.Preview = []previewR{}
		boards = append(boards, b)
		byID[b.ID] = b
	}
	rows.Close()

	// Outlines for thumbnails; connectors and huge boards are skipped.
	rows, err = s.db.R.QueryContext(r.Context(), `SELECT i.board_id, i.kind, i.x, i.y, i.w, i.h, COALESCE(json_extract(i.data, '$.color'), '')
		FROM whiteboard_items i JOIN whiteboards b ON b.id = i.board_id
		WHERE b.project_id = ? AND i.kind != 'connector' ORDER BY i.board_id, i.z`, p.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var bid int64
		var pr previewR
		var color any
		if err := rows.Scan(&bid, &pr.Kind, &pr.X, &pr.Y, &pr.W, &pr.H, &color); err != nil {
			return err
		}
		if c, ok := color.(string); ok && len(c) <= 24 {
			pr.Color = c
		}
		if b := byID[bid]; b != nil && len(b.Preview) < 200 {
			b.Preview = append(b.Preview, pr)
		}
	}
	return ok(w, boards)
}

func cleanTitle(t string, fallback string) string {
	t = truncate(strings.TrimSpace(t), 120)
	if t == "" {
		return fallback
	}
	return t
}

func (s *Server) handleCreateBoard(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	var n int
	if err := s.db.R.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM whiteboards WHERE project_id = ?`, p.ID).Scan(&n); err != nil {
		return err
	}
	if n >= maxBoardsPerProj {
		return badRequest("this project has too many whiteboards (max %d)", maxBoardsPerProj)
	}
	now := nowMs()
	res, err := s.db.W.ExecContext(r.Context(), `INSERT INTO whiteboards (project_id, title, description, created_by, created_at, updated_by, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, p.ID, cleanTitle(body.Title, "Untitled board"), truncate(strings.TrimSpace(body.Description), 2000), u.ID, now, u.ID, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	b, err := scanBoard(s.db.R.QueryRowContext(r.Context(), `SELECT `+boardColumns+boardJoins+` WHERE b.id = ?`, id))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, b)
}

func (s *Server) handleGetBoard(w http.ResponseWriter, r *http.Request, p *project) error {
	b, err := s.loadBoard(r.Context(), p.ID, r)
	if err != nil {
		return err
	}
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT id, kind, x, y, w, h, z, data, rev, updated_by, updated_at
		FROM whiteboard_items WHERE board_id = ? ORDER BY z, id`, b.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []boardItem{}
	for rows.Next() {
		var it boardItem
		var data string
		if err := rows.Scan(&it.ID, &it.Kind, &it.X, &it.Y, &it.W, &it.H, &it.Z, &data, &it.Rev, &it.UpdatedBy, &it.UpdatedAt); err != nil {
			return err
		}
		it.Data = json.RawMessage(data)
		items = append(items, it)
	}
	return ok(w, map[string]any{"board": b, "items": items})
}

func (s *Server) handleUpdateBoard(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	b, err := s.loadBoard(r.Context(), p.ID, r)
	if err != nil {
		return err
	}
	var body struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if body.Title != nil {
		b.Title = cleanTitle(*body.Title, b.Title)
	}
	if body.Description != nil {
		b.Description = truncate(strings.TrimSpace(*body.Description), 2000)
	}
	if _, err := s.db.W.ExecContext(r.Context(), `UPDATE whiteboards SET title = ?, description = ?, updated_by = ?, updated_at = ? WHERE id = ?`,
		b.Title, b.Description, u.ID, nowMs(), b.ID); err != nil {
		return err
	}
	s.publishBoard(p.ID, map[string]any{"board": b.ID, "by": u.ID, "title": b.Title, "description": b.Description})
	return ok(w, b)
}

func (s *Server) handleDeleteBoard(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	b, err := s.loadBoard(r.Context(), p.ID, r)
	if err != nil {
		return err
	}
	if _, err := s.db.W.ExecContext(r.Context(), `DELETE FROM whiteboards WHERE id = ?`, b.ID); err != nil {
		return err
	}
	s.audit(r, p.ID, u, "whiteboard.delete", b.Title)
	s.publishBoard(p.ID, map[string]any{"board": b.ID, "by": u.ID, "deletedBoard": true})
	return ok(w, map[string]any{"ok": true})
}

// validItem checks an incoming item and normalizes its numbers.
func validItem(it *boardItem) error {
	if !itemIDRe.MatchString(it.ID) {
		return badRequest("invalid item id")
	}
	if !itemKinds[it.Kind] {
		return badRequest("unknown item kind %q", truncate(it.Kind, 20))
	}
	for _, v := range []*float64{&it.X, &it.Y, &it.Z} {
		if math.IsNaN(*v) || math.IsInf(*v, 0) {
			return badRequest("item %s has an invalid position", it.ID)
		}
		*v = math.Max(-maxCoord, math.Min(maxCoord, *v))
	}
	for _, v := range []*float64{&it.W, &it.H} {
		if math.IsNaN(*v) || math.IsInf(*v, 0) {
			return badRequest("item %s has an invalid size", it.ID)
		}
		*v = math.Max(0, math.Min(maxSize, *v))
	}
	data := bytes.TrimSpace(it.Data)
	if len(data) == 0 || string(data) == "null" {
		data = []byte("{}")
	}
	if len(data) > maxItemDataBytes {
		return badRequest("item %s is too large", it.ID)
	}
	var obj map[string]json.RawMessage
	if data[0] != '{' || json.Unmarshal(data, &obj) != nil {
		return badRequest("item %s data must be a JSON object", it.ID)
	}
	// A link back to the dashboard must stay inside it (no javascript: or
	// off-site URLs). The client renders it as a router link as well.
	if raw, found := obj["link"]; found {
		var link string
		if json.Unmarshal(raw, &link) != nil || !safeLink(link) {
			return badRequest("item %s has an invalid link", it.ID)
		}
	}
	it.Data = data
	return nil
}

func safeLink(l string) bool {
	return l == "" || (strings.HasPrefix(l, "/") && !strings.HasPrefix(l, "//") && !strings.ContainsAny(l, "\\\x00\r\n") && len(l) <= 2048)
}

// handleBoardItems applies a batch of upserts and deletes. With place=true the
// batch is moved, as a group, to free space below everything on the board
// (used when sending telemetry from elsewhere in the dashboard).
func (s *Server) handleBoardItems(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	b, err := s.loadBoard(r.Context(), p.ID, r)
	if err != nil {
		return err
	}
	var body struct {
		Client string      `json:"client"`
		Upsert []boardItem `json:"upsert"`
		Delete []string    `json:"delete"`
		Place  bool        `json:"place"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		return errStatus(http.StatusUnsupportedMediaType, "expected application/json")
	}
	if err := dec.Decode(&body); err != nil {
		return badRequest("invalid JSON body")
	}
	if len(body.Upsert)+len(body.Delete) > maxItemBatch {
		return badRequest("too many changes in one request (max %d)", maxItemBatch)
	}
	if !clientIDRe.MatchString(body.Client) {
		return badRequest("invalid client id")
	}
	for i := range body.Upsert {
		if err := validItem(&body.Upsert[i]); err != nil {
			return err
		}
	}
	for _, id := range body.Delete {
		if !itemIDRe.MatchString(id) {
			return badRequest("invalid item id")
		}
	}

	now := nowMs()
	err = s.db.Tx(r.Context(), func(tx *sql.Tx) error {
		if body.Place && len(body.Upsert) > 0 {
			var minX, maxY sql.NullFloat64
			if err := tx.QueryRowContext(r.Context(), `SELECT MIN(x), MAX(y + h) FROM whiteboard_items WHERE board_id = ? AND kind != 'connector'`, b.ID).Scan(&minX, &maxY); err != nil {
				return err
			}
			bx, by := math.Inf(1), math.Inf(1)
			for _, it := range body.Upsert {
				if it.Kind != "connector" {
					bx, by = math.Min(bx, it.X), math.Min(by, it.Y)
				}
			}
			if math.IsInf(bx, 1) {
				bx, by = 0, 0
			}
			dx, dy := -bx, -by
			if minX.Valid {
				dx, dy = minX.Float64-bx, maxY.Float64+80-by
			}
			var maxZ sql.NullFloat64
			tx.QueryRowContext(r.Context(), `SELECT MAX(z) FROM whiteboard_items WHERE board_id = ?`, b.ID).Scan(&maxZ)
			for i := range body.Upsert {
				body.Upsert[i].X += dx
				body.Upsert[i].Y += dy
				body.Upsert[i].Z += maxZ.Float64 + 1
			}
		}

		var count int
		if err := tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM whiteboard_items WHERE board_id = ?`, b.ID).Scan(&count); err != nil {
			return err
		}
		for _, id := range body.Delete {
			res, err := tx.ExecContext(r.Context(), `DELETE FROM whiteboard_items WHERE board_id = ? AND id = ?`, b.ID, id)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			count -= int(n)
		}
		for i := range body.Upsert {
			it := &body.Upsert[i]
			var rev int64
			err := tx.QueryRowContext(r.Context(), `SELECT rev FROM whiteboard_items WHERE board_id = ? AND id = ?`, b.ID, it.ID).Scan(&rev)
			if errors.Is(err, sql.ErrNoRows) {
				if count >= maxBoardItems {
					return badRequest("this board is full (max %d items)", maxBoardItems)
				}
				count++
			} else if err != nil {
				return err
			}
			it.Rev = rev + 1
			it.UpdatedBy = &u.ID
			it.UpdatedAt = now
			if _, err := tx.ExecContext(r.Context(), `INSERT INTO whiteboard_items (board_id, id, kind, x, y, w, h, z, data, rev, updated_by, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (board_id, id) DO UPDATE SET kind = excluded.kind, x = excluded.x, y = excluded.y, w = excluded.w, h = excluded.h,
					z = excluded.z, data = excluded.data, rev = excluded.rev, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
				b.ID, it.ID, it.Kind, it.X, it.Y, it.W, it.H, it.Z, string(it.Data), it.Rev, u.ID, now); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(r.Context(), `UPDATE whiteboards SET updated_by = ?, updated_at = ? WHERE id = ?`, u.ID, now, b.ID)
		return err
	})
	if err != nil {
		return err
	}
	if body.Upsert == nil {
		body.Upsert = []boardItem{}
	}
	if body.Delete == nil {
		body.Delete = []string{}
	}
	s.publishBoard(p.ID, map[string]any{"board": b.ID, "by": u.ID, "client": body.Client, "items": body.Upsert, "deleted": body.Delete})
	return ok(w, map[string]any{"items": body.Upsert, "deleted": body.Delete})
}

// handleBoardPresence relays a viewer's cursor and selection to the others on
// the board. Nothing is stored.
func (s *Server) handleBoardPresence(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	id, err := pathInt(r, "bid")
	if err != nil {
		return err
	}
	var body struct {
		Client   string      `json:"client"`
		Cursor   *[2]float64 `json:"cursor"`
		Selected []string    `json:"selected"`
		Leaving  bool        `json:"leaving"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if !clientIDRe.MatchString(body.Client) {
		return badRequest("invalid client id")
	}
	if body.Cursor != nil {
		for _, v := range body.Cursor {
			if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > maxCoord {
				body.Cursor = nil
				break
			}
		}
	}
	sel := []string{}
	for _, sid := range body.Selected {
		if itemIDRe.MatchString(sid) && len(sel) < 50 {
			sel = append(sel, sid)
		}
	}
	msg, _ := json.Marshal([]map[string]any{{"t": "wb_presence", "d": map[string]any{
		"board": id, "client": body.Client, "leaving": body.Leaving, "cursor": body.Cursor, "selected": sel,
		"user": userRef{ID: u.ID, Name: u.Name, Handle: u.Handle, Avatar: u.Avatar},
	}}})
	s.broker.Publish(p.ID, msg)
	return ok(w, map[string]any{"ok": true})
}

// publishBoard sends a board change to live viewers. Very large changes are
// sent as a "reload" hint instead of inline, to keep the stream light.
func (s *Server) publishBoard(projectID int64, d map[string]any) {
	msg, err := json.Marshal([]map[string]any{{"t": "wb", "d": d}})
	if err != nil {
		return
	}
	if len(msg) > 256<<10 {
		delete(d, "items")
		delete(d, "deleted")
		d["reload"] = true
		if msg, err = json.Marshal([]map[string]any{{"t": "wb", "d": d}}); err != nil {
			return
		}
	}
	s.broker.Publish(projectID, msg)
}

// ---- Chart annotations ------------------------------------------------------

type annotation struct {
	ID        int64    `json:"id"`
	ChartKey  string   `json:"chartKey"`
	TS        int64    `json:"ts"`
	Value     *float64 `json:"value"`
	Series    string   `json:"series"`
	Text      string   `json:"text"`
	Author    *userRef `json:"author"`
	CreatedAt int64    `json:"createdAt"`
}

func (s *Server) handleListAnnotations(w http.ResponseWriter, r *http.Request, p *project) error {
	keys := []any{p.ID}
	for _, k := range strings.Split(r.URL.Query().Get("keys"), ",") {
		if k = strings.TrimSpace(k); k != "" && len(k) <= 200 && len(keys) <= 40 {
			keys = append(keys, k)
		}
	}
	out := []annotation{}
	if len(keys) == 1 {
		return ok(w, out)
	}
	q := `SELECT a.id, a.chart_key, a.ts, a.value, a.series, a.text, a.created_at, u.id, u.name, u.handle, u.avatar
		FROM chart_annotations a LEFT JOIN users u ON u.id = a.created_by
		WHERE a.project_id = ? AND a.chart_key IN (?` + strings.Repeat(",?", len(keys)-2) + `) ORDER BY a.ts LIMIT 2000`
	rows, err := s.db.R.QueryContext(r.Context(), q, keys...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a annotation
		var uid sql.NullInt64
		var name, handle, avatar sql.NullString
		if err := rows.Scan(&a.ID, &a.ChartKey, &a.TS, &a.Value, &a.Series, &a.Text, &a.CreatedAt, &uid, &name, &handle, &avatar); err != nil {
			return err
		}
		a.Author = refOf(uid, name, handle, avatar)
		out = append(out, a)
	}
	return ok(w, out)
}

func (s *Server) handleCreateAnnotation(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	var body struct {
		ChartKey string   `json:"chartKey"`
		TS       int64    `json:"ts"`
		Value    *float64 `json:"value"`
		Series   string   `json:"series"`
		Text     string   `json:"text"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	key := strings.TrimSpace(body.ChartKey)
	text := truncate(strings.TrimSpace(body.Text), 500)
	if key == "" || len(key) > 200 {
		return badRequest("invalid chart key")
	}
	if text == "" {
		return badRequest("annotation text is required")
	}
	if body.TS <= 0 {
		return badRequest("invalid timestamp")
	}
	if body.Value != nil && (math.IsNaN(*body.Value) || math.IsInf(*body.Value, 0)) {
		body.Value = nil
	}
	now := nowMs()
	res, err := s.db.W.ExecContext(r.Context(), `INSERT INTO chart_annotations (project_id, chart_key, ts, value, series, text, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, p.ID, key, body.TS, body.Value, truncate(body.Series, 64), text, u.ID, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	return writeJSON(w, http.StatusCreated, annotation{ID: id, ChartKey: key, TS: body.TS, Value: body.Value, Series: truncate(body.Series, 64), Text: text,
		Author: &userRef{ID: u.ID, Name: u.Name, Handle: u.Handle, Avatar: u.Avatar}, CreatedAt: now})
}

func (s *Server) handleDeleteAnnotation(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	id, err := pathInt(r, "aid")
	if err != nil {
		return err
	}
	var author sql.NullInt64
	err = s.db.R.QueryRowContext(r.Context(), `SELECT created_by FROM chart_annotations WHERE id = ? AND project_id = ?`, id, p.ID).Scan(&author)
	if errors.Is(err, sql.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	if !u.IsAdmin && (!author.Valid || author.Int64 != u.ID) {
		return errStatus(http.StatusForbidden, "only the author or an admin can delete this note")
	}
	if _, err := s.db.W.ExecContext(r.Context(), `DELETE FROM chart_annotations WHERE id = ?`, id); err != nil {
		return err
	}
	return ok(w, map[string]any{"ok": true, "id": strconv.FormatInt(id, 10)})
}
