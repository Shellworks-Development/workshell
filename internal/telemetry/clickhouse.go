// Package telemetry implements the optional ClickHouse telemetry store over HTTP.
// Application and identity records remain in SQLite.
package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Tables is the set of append-only datasets that can be offloaded.
var Tables = []string{"logs", "heartbeats", "metrics", "spans", "client_perf"}

var schemas = map[string]string{
	"logs":        `ts Int64, level LowCardinality(String), message String, job_id String, user_id Nullable(Int64), source String, attrs String, session_key String`,
	"heartbeats":  `job_id String, ts Int64, players Int64, fps Float64, heartbeat_ms Float64, memory_mb Float64, ping_ms Float64, instances Int64, send_kbps Float64, recv_kbps Float64`,
	"metrics":     `name String, ts Int64, job_id String, kind LowCardinality(String), count Int64, sum Float64, min Float64, max Float64, last Float64`,
	"spans":       `ts Int64, name String, duration_ms Float64, status LowCardinality(String), job_id String, session_key String, user_id Nullable(Int64), release String, trace_id String, span_id String, parent String, attrs String`,
	"client_perf": `session_key String, ts Int64, fps Float64, memory_mb Float64, ping_ms Float64, platform LowCardinality(String)`,
}

func IsTable(table string) bool { _, ok := schemas[table]; return ok }

type Row map[string]any

type ClickHouse struct {
	endpoint                 url.URL
	database, user, password string
	client                   *http.Client
}

// Open validates settings and creates tables in an existing database. Credentials
// are sent in headers, never in URL query parameters or returned errors.
func Open(ctx context.Context, endpoint, database, user, password string) (*ClickHouse, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("ClickHouse URL must be an http(s) endpoint without credentials, query parameters or fragment")
	}
	if database == "" {
		database = "default"
	}
	c := &ClickHouse{endpoint: *u, database: database, user: user, password: password,
		client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	for _, table := range Tables {
		// Stable ids come from the SQLite outbox, so partial writes and retries
		// deduplicate both before and after background merges (queries use FINAL).
		q := `CREATE TABLE IF NOT EXISTS ` + table + ` (id Int64, project_id Int64, ` + schemas[table] + `)
   ENGINE = ReplacingMergeTree PARTITION BY toYYYYMM(fromUnixTimestamp64Milli(ts)) ORDER BY (project_id, ts, id)`
		if err := c.Exec(ctx, q); err != nil {
			c.Close()
			return nil, fmt.Errorf("initialize ClickHouse %s: %w", table, err)
		}
	}
	return c, nil
}

func (c *ClickHouse) Close() { c.client.CloseIdleConnections() }

func (c *ClickHouse) request(ctx context.Context, query string, data []byte, params url.Values) (io.ReadCloser, error) {
	u := c.endpoint
	if params == nil {
		params = url.Values{}
	}
	params.Set("database", c.database)
	params.Set("query", query)
	params.Set("wait_end_of_query", "1")
	params.Set("output_format_json_quote_64bit_integers", "0")
	params.Set("output_format_json_quote_denormals", "1")
	u.RawQuery = params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create ClickHouse request")
	}
	req.Header.Set("X-ClickHouse-User", c.user)
	req.Header.Set("X-ClickHouse-Key", c.password)
	resp, err := c.client.Do(req)
	if err != nil {
		// url.Error includes the query and possibly sensitive filter parameters.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("ClickHouse request failed (%T)", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("ClickHouse returned HTTP %d", resp.StatusCode)
	}
	return resp.Body, nil
}

func (c *ClickHouse) Exec(ctx context.Context, query string, args ...any) error {
	q, params, err := bind(query, args)
	if err != nil {
		return err
	}
	body, err := c.request(ctx, q, nil, params)
	if err != nil {
		return err
	}
	defer body.Close()
	// wait_end_of_query makes server exceptions change the HTTP status.
	_, err = io.Copy(io.Discard, body)
	return err
}

func (c *ClickHouse) Insert(ctx context.Context, table string, rows []Row) error {
	if !IsTable(table) {
		return fmt.Errorf("unknown telemetry table")
	}
	if len(rows) == 0 {
		return nil
	}
	var data bytes.Buffer
	enc := json.NewEncoder(&data)
	for _, row := range rows {
		if err := enc.Encode(row); err != nil {
			return err
		}
	}
	body, err := c.request(ctx, `INSERT INTO `+table+` FORMAT JSONEachRow`, data.Bytes(), nil)
	if err != nil {
		return err
	}
	defer body.Close()
	_, err = io.Copy(io.Discard, body)
	return err
}

func (c *ClickHouse) Delete(ctx context.Context, table, where string, args ...any) error {
	if !IsTable(table) {
		return fmt.Errorf("unknown telemetry table")
	}
	return c.Exec(ctx, `ALTER TABLE `+table+` DELETE WHERE `+where+` SETTINGS mutations_sync = 2`, args...)
}

// Rows is shared by database/sql rows and streaming ClickHouse rows.
type Rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}

type jsonRows struct {
	body   io.ReadCloser
	dec    *json.Decoder
	row    []json.RawMessage
	err    error
	closed bool
}

func (r *jsonRows) Next() bool {
	if r.closed || r.err != nil {
		return false
	}
	r.row = nil
	if err := r.dec.Decode(&r.row); err != nil {
		if err != io.EOF {
			r.err = err
		}
		r.Close()
		return false
	}
	return true
}
func (r *jsonRows) Scan(dest ...any) error {
	if len(dest) != len(r.row) {
		return fmt.Errorf("telemetry scan: expected %d values, got %d", len(r.row), len(dest))
	}
	for i, d := range dest {
		if err := json.Unmarshal(r.row[i], d); err != nil {
			return fmt.Errorf("telemetry scan column %d: %w", i, err)
		}
	}
	return nil
}
func (r *jsonRows) Err() error { return r.err }
func (r *jsonRows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	return r.body.Close()
}

var tableFrom = regexp.MustCompile(`\bFROM (logs|heartbeats|metrics|spans|client_perf)\b`)

// Query supports the deliberately small SQL subset used by telemetry readers.
// Keep dialect differences here, rather than changing the SQLite expressions.
func (c *ClickHouse) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	query = tableFrom.ReplaceAllString(query, "FROM $1 FINAL")
	query = strings.NewReplacer(
		"(ts - ?2) / ?4 * ?4 + ?2", "intDiv(ts - ?2, ?4) * ?4 + ?2",
		"(ts - ?) / ? * ? + ?", "intDiv(ts - ?, ?) * ? + ?",
		"MAX(SUM(count), 1)", "greatest(SUM(count), 1)",
		"COALESCE(AVG(fps), 0)", "ifNotFinite(AVG(fps), 0)",
		// ClickHouse LIKE is case sensitive; SQLite LIKE ignores ASCII case.
		"message LIKE ? ESCAPE '\\'", "message ILIKE ?",
	).Replace(query)
	q, params, err := bind(query, args)
	if err != nil {
		return nil, err
	}
	body, err := c.request(ctx, q+" FORMAT JSONCompactEachRow", nil, params)
	if err != nil {
		return nil, err
	}
	return &jsonRows{body: body, dec: json.NewDecoder(body)}, nil
}

// bind converts ? / ?N outside SQL string literals to typed HTTP parameters.
// Values never enter the SQL text. This is only for application-owned queries.
func bind(query string, args []any) (string, url.Values, error) {
	params := url.Values{}
	var out strings.Builder
	next := 0
	for i := 0; i < len(query); {
		if query[i] == '\'' {
			start := i
			i++
			for i < len(query) {
				if query[i] == '\'' {
					i++
					if i < len(query) && query[i] == '\'' {
						i++
						continue
					}
					break
				}
				i++
			}
			out.WriteString(query[start:i])
			continue
		}
		if query[i] != '?' {
			out.WriteByte(query[i])
			i++
			continue
		}
		i++
		start := i
		for i < len(query) && query[i] >= '0' && query[i] <= '9' {
			i++
		}
		index := next
		if i > start {
			n, _ := strconv.Atoi(query[start:i])
			index = n - 1
		}
		next = index + 1
		if index < 0 || index >= len(args) {
			return "", nil, fmt.Errorf("telemetry query parameter out of range")
		}
		typ, value := "", ""
		switch v := args[index].(type) {
		case string:
			typ, value = "String", v
		case int:
			typ, value = "Int64", strconv.Itoa(v)
		case int64:
			typ, value = "Int64", strconv.FormatInt(v, 10)
		case float64:
			typ, value = "Float64", strconv.FormatFloat(v, 'g', -1, 64)
		default:
			return "", nil, fmt.Errorf("unsupported telemetry parameter %T", v)
		}
		name := "p" + strconv.Itoa(index)
		out.WriteString("{" + name + ":" + typ + "}")
		params.Set("param_"+name, value)
	}
	return out.String(), params, nil
}
