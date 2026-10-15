package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBindParameters(t *testing.T) {
	q, params, err := bind(`SELECT ?2, ?1, '?3', 'it''s ?', ?2`, []any{int64(42), "x' OR 1=1 --"})
	if err != nil {
		t.Fatal(err)
	}
	if q != `SELECT {p1:String}, {p0:Int64}, '?3', 'it''s ?', {p1:String}` || params.Get("param_p1") != "x' OR 1=1 --" {
		t.Fatalf("unsafe binding: %s %v", q, params)
	}
	if _, _, err := bind(`SELECT ?2`, []any{1}); err == nil {
		t.Fatal("missing parameter accepted")
	}
}

func TestHTTPQueryAndErrors(t *testing.T) {
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-ClickHouse-Key") != "secret" || r.URL.Query().Get("database") != "test" {
			t.Error("missing credentials/database")
		}
		if strings.Contains(r.URL.RawQuery, "secret") {
			t.Error("password leaked in URL")
		}
		q := r.URL.Query().Get("query")
		if fail.Load() {
			http.Error(w, "sensitive query", 500)
			return
		}
		if strings.HasPrefix(q, "SELECT") {
			if !strings.Contains(q, "FROM logs FINAL") || !strings.Contains(q, "intDiv(ts -") || !strings.Contains(q, "message ILIKE {p4:String}") {
				t.Errorf("dialect: %s", q)
			}
			io.WriteString(w, "[9007199254740991,null,\"message\",12.5]\n")
		}
	}))
	defer srv.Close()
	c, err := Open(context.Background(), srv.URL, "test", "user", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	rows, err := c.Query(context.Background(), `SELECT (ts - ?) / ? * ? + ?, user_id, message, value FROM logs WHERE message LIKE ? ESCAPE '\'`, int64(0), int64(10), int64(10), int64(0), "test%")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var id int64
	var user *int64
	var message string
	var value float64
	if !rows.Next() {
		t.Fatal("no row")
	}
	if err := rows.Scan(&id, &user, &message, &value); err != nil {
		t.Fatal(err)
	}
	if id != 9007199254740991 || user != nil || value != 12.5 {
		t.Fatal("scan lost precision or null")
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	fail.Store(true)
	if err := c.Insert(context.Background(), "logs", []Row{{"id": int64(1)}}); err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("bad error: %v", err)
	}
}

func TestRejectUnsafeEndpoints(t *testing.T) {
	for _, endpoint := range []string{"ftp://localhost", "http://user:secret@localhost", "http://localhost?password=secret", "http://localhost/#fragment"} {
		if _, err := Open(context.Background(), endpoint, "", "", ""); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("accepted or leaked endpoint %q: %v", endpoint, err)
		}
	}
}
