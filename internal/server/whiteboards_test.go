package server

import (
	"encoding/json"
	"testing"
)

func TestWhiteboardLifecycle(t *testing.T) {
	e := newTestEnv(t)
	pid, _ := e.setup()
	base := "/api/projects/" + itoa(pid) + "/whiteboards"

	var board boardRow
	if code := e.do("POST", base, map[string]any{"title": "  Lag spike investigation  "}, &board); code != 201 {
		t.Fatalf("create board: %d", code)
	}
	if board.Title != "Lag spike investigation" || board.CreatedBy == nil {
		t.Fatalf("unexpected board %+v", board)
	}
	bpath := base + "/" + itoa(board.ID)

	items := []map[string]any{
		{"id": "note1", "kind": "sticky", "x": 10, "y": 20, "w": 200, "h": 160, "data": map[string]any{"text": "<img src=x onerror=alert(1)>", "color": "yellow"}},
		{"id": "chart1", "kind": "embed", "x": 300, "y": 20, "w": 420, "h": 260, "data": map[string]any{"type": "chart", "title": "FPS", "link": "/p/1/servers"}},
		{"id": "c1", "kind": "connector", "data": map[string]any{"from": map[string]any{"id": "note1"}, "to": map[string]any{"id": "chart1"}}},
	}
	var res struct {
		Items []boardItem
	}
	if code := e.do("POST", bpath+"/items", map[string]any{"client": "tab1", "upsert": items}, &res); code != 200 || len(res.Items) != 3 || res.Items[0].Rev != 1 {
		t.Fatalf("upsert: %d %+v", code, res)
	}

	// Updating bumps the revision; data is stored verbatim (rendered as text).
	items[0]["x"] = 50
	e.do("POST", bpath+"/items", map[string]any{"upsert": items[:1]}, &res)
	if res.Items[0].Rev != 2 {
		t.Fatalf("expected rev 2, got %d", res.Items[0].Rev)
	}

	// Bad input is rejected.
	for name, bad := range map[string]map[string]any{
		"kind":        {"id": "x", "kind": "script"},
		"id":          {"id": "../etc", "kind": "sticky"},
		"js link":     {"id": "x", "kind": "embed", "data": map[string]any{"link": "javascript:alert(1)"}},
		"remote link": {"id": "x", "kind": "embed", "data": map[string]any{"link": "//evil.example"}},
		"not object":  {"id": "x", "kind": "sticky", "data": []int{1}},
	} {
		if code := e.do("POST", bpath+"/items", map[string]any{"upsert": []any{bad}}, nil); code != 400 {
			t.Errorf("%s: expected 400, got %d", name, code)
		}
	}

	// Placing a sent item puts it below everything already on the board.
	if code := e.do("POST", bpath+"/items", map[string]any{"place": true, "upsert": []any{
		map[string]any{"id": "log1", "kind": "embed", "x": 0, "y": 0, "w": 300, "h": 120, "data": map[string]any{"type": "log"}},
	}}, &res); code != 200 {
		t.Fatalf("place: %d", code)
	}
	if res.Items[0].Y < 280 {
		t.Fatalf("placed item overlaps existing items: y=%v", res.Items[0].Y)
	}

	var full struct {
		Board boardRow
		Items []boardItem
	}
	e.do("POST", bpath+"/items", map[string]any{"delete": []string{"c1"}}, nil)
	e.do("GET", bpath, nil, &full)
	if len(full.Items) != 3 || full.Board.ItemCount != 3 {
		t.Fatalf("expected 3 items after delete, got %d", len(full.Items))
	}
	var data map[string]string
	for _, it := range full.Items {
		if it.ID == "note1" {
			json.Unmarshal(it.Data, &data)
		}
	}
	if data["text"] != "<img src=x onerror=alert(1)>" {
		t.Fatalf("sticky text not preserved: %q", data["text"])
	}

	var list []boardRow
	e.do("GET", base, nil, &list)
	if len(list) != 1 || len(list[0].Preview) != 3 {
		t.Fatalf("list: %+v", list)
	}

	if code := e.do("DELETE", bpath, nil, nil); code != 200 {
		t.Fatalf("delete board: %d", code)
	}
	if code := e.do("GET", bpath, nil, nil); code != 404 {
		t.Fatalf("deleted board still readable: %d", code)
	}
}

func TestChartAnnotations(t *testing.T) {
	e := newTestEnv(t)
	pid, _ := e.setup()
	base := "/api/projects/" + itoa(pid) + "/annotations"

	var a annotation
	if code := e.do("POST", base, map[string]any{"chartKey": "servers:fps", "ts": 1_700_000_000_000, "value": 31.5, "series": "fps", "text": "Deploy v42"}, &a); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if code := e.do("POST", base, map[string]any{"chartKey": "servers:fps", "ts": 1, "text": "  "}, nil); code != 400 {
		t.Fatalf("empty text accepted: %d", code)
	}
	var list []annotation
	e.do("GET", base+"?keys=servers:fps,other", nil, &list)
	if len(list) != 1 || list[0].Text != "Deploy v42" || list[0].Value == nil || *list[0].Value != 31.5 {
		t.Fatalf("list: %+v", list)
	}

	// Only the author (or an admin) may delete.
	e.do("POST", "/api/users", map[string]any{"name": "Bob", "email": "bob@example.com", "password": "hunter2hunter2"}, nil)
	e.do("POST", "/api/auth/logout", map[string]any{}, nil)
	e.do("POST", "/api/auth/login", map[string]any{"email": "bob@example.com", "password": "hunter2hunter2"}, nil)
	if code := e.do("DELETE", base+"/"+itoa(a.ID), nil, nil); code != 403 {
		t.Fatalf("non-author delete: %d", code)
	}
}

func TestTraceTree(t *testing.T) {
	e := newTestEnv(t)
	pid, key := e.setup()
	e.ingest(key, map[string]any{"server": map[string]any{"jobId": "j"}, "items": []any{
		map[string]any{"type": "span", "name": "player.join", "durationMs": 300, "traceId": "t1", "spanId": "t1"},
		map[string]any{"type": "span", "name": "datastore.load", "durationMs": 200, "traceId": "t1", "spanId": "s2", "parent": "t1"},
		map[string]any{"type": "span", "name": "other", "durationMs": 5, "traceId": "t2", "spanId": "t2"},
	}})
	e.settle()
	var tr struct {
		Spans []struct {
			SpanID, Parent, Name string
			DurationMs           float64
		}
	}
	if code := e.do("GET", "/api/projects/"+itoa(pid)+"/traces/t1", nil, &tr); code != 200 || len(tr.Spans) != 2 {
		t.Fatalf("trace: %d %+v", code, tr)
	}
	if code := e.do("GET", "/api/projects/"+itoa(pid)+"/traces/nope", nil, nil); code != 404 {
		t.Fatalf("missing trace: %d", code)
	}
}
