package db

// migrations are applied in order; the index+1 is stored in PRAGMA user_version.
// Never edit an existing entry once released — append a new one instead.
// All timestamps are Unix milliseconds.
var migrations = []string{
	`
CREATE TABLE users (
	id            INTEGER PRIMARY KEY,
	email         TEXT NOT NULL UNIQUE COLLATE NOCASE,
	name          TEXT NOT NULL,
	password_hash TEXT NOT NULL,
	is_admin      INTEGER NOT NULL DEFAULT 0,
	created_at    INTEGER NOT NULL
);

CREATE TABLE sessions (
	token_hash TEXT PRIMARY KEY,
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL
);

CREATE TABLE projects (
	id                  INTEGER PRIMARY KEY,
	name                TEXT NOT NULL,
	ingest_key          TEXT NOT NULL UNIQUE,
	created_at          INTEGER NOT NULL,
	log_retention_days  INTEGER NOT NULL DEFAULT 14,
	data_retention_days INTEGER NOT NULL DEFAULT 90,
	webhook_url         TEXT NOT NULL DEFAULT '',
	alert_new_issues    INTEGER NOT NULL DEFAULT 1,
	alert_regressions   INTEGER NOT NULL DEFAULT 1
);

-- Errors -------------------------------------------------------------------

CREATE TABLE issues (
	id            INTEGER PRIMARY KEY,
	project_id    INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	fingerprint   TEXT NOT NULL,
	title         TEXT NOT NULL,
	culprit       TEXT NOT NULL DEFAULT '',
	level         TEXT NOT NULL DEFAULT 'error',
	side          TEXT NOT NULL DEFAULT 'server',
	status        TEXT NOT NULL DEFAULT 'unresolved',
	first_seen    INTEGER NOT NULL,
	last_seen     INTEGER NOT NULL,
	event_count   INTEGER NOT NULL DEFAULT 0,
	user_count    INTEGER NOT NULL DEFAULT 0,
	first_release TEXT NOT NULL DEFAULT '',
	last_release  TEXT NOT NULL DEFAULT '',
	resolved_at   INTEGER,
	regressed_at  INTEGER,
	UNIQUE (project_id, fingerprint)
);
CREATE INDEX issues_project_status_seen ON issues(project_id, status, last_seen DESC);

CREATE TABLE issue_users (
	issue_id INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
	user_id  INTEGER NOT NULL,
	PRIMARY KEY (issue_id, user_id)
) WITHOUT ROWID;

CREATE TABLE error_events (
	id            INTEGER PRIMARY KEY,
	project_id    INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	issue_id      INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
	ts            INTEGER NOT NULL,
	count         INTEGER NOT NULL DEFAULT 1,
	message       TEXT NOT NULL,
	stack         TEXT NOT NULL DEFAULT '',
	level         TEXT NOT NULL DEFAULT 'error',
	side          TEXT NOT NULL DEFAULT 'server',
	job_id        TEXT NOT NULL DEFAULT '',
	place_version TEXT NOT NULL DEFAULT '',
	environment   TEXT NOT NULL DEFAULT '',
	user_id       INTEGER,
	script        TEXT NOT NULL DEFAULT '',
	tags          TEXT NOT NULL DEFAULT '{}',
	context       TEXT NOT NULL DEFAULT '{}',
	breadcrumbs   TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX error_events_issue_ts ON error_events(issue_id, ts DESC);
CREATE INDEX error_events_project_ts ON error_events(project_id, ts);

-- Logs ---------------------------------------------------------------------

CREATE TABLE logs (
	id         INTEGER PRIMARY KEY,
	project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	ts         INTEGER NOT NULL,
	level      TEXT NOT NULL,
	message    TEXT NOT NULL,
	job_id     TEXT NOT NULL DEFAULT '',
	user_id    INTEGER,
	source     TEXT NOT NULL DEFAULT '',
	attrs      TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX logs_project_ts ON logs(project_id, ts DESC, id DESC);

-- Servers ------------------------------------------------------------------

CREATE TABLE game_servers (
	project_id    INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	job_id        TEXT NOT NULL,
	place_id      INTEGER NOT NULL DEFAULT 0,
	place_version TEXT NOT NULL DEFAULT '',
	environment   TEXT NOT NULL DEFAULT '',
	server_type   TEXT NOT NULL DEFAULT 'public',
	max_players   INTEGER NOT NULL DEFAULT 0,
	started_at    INTEGER NOT NULL,
	last_seen     INTEGER NOT NULL,
	closed_at     INTEGER,
	players       INTEGER NOT NULL DEFAULT 0,
	fps           REAL NOT NULL DEFAULT 0,
	heartbeat_ms  REAL NOT NULL DEFAULT 0,
	memory_mb     REAL NOT NULL DEFAULT 0,
	ping_ms       REAL NOT NULL DEFAULT 0,
	PRIMARY KEY (project_id, job_id)
);
CREATE INDEX game_servers_project_seen ON game_servers(project_id, last_seen DESC);

CREATE TABLE heartbeats (
	project_id   INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	job_id       TEXT NOT NULL,
	ts           INTEGER NOT NULL,
	players      INTEGER NOT NULL,
	fps          REAL NOT NULL,
	heartbeat_ms REAL NOT NULL,
	memory_mb    REAL NOT NULL,
	ping_ms      REAL NOT NULL,
	instances    INTEGER NOT NULL,
	send_kbps    REAL NOT NULL,
	recv_kbps    REAL NOT NULL
);
CREATE INDEX heartbeats_project_ts ON heartbeats(project_id, ts);
CREATE INDEX heartbeats_server_ts ON heartbeats(project_id, job_id, ts);

-- Analytics ----------------------------------------------------------------

CREATE TABLE players (
	project_id    INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	user_id       INTEGER NOT NULL,
	first_seen    INTEGER NOT NULL,
	last_seen     INTEGER NOT NULL,
	session_count INTEGER NOT NULL DEFAULT 0,
	playtime_ms   INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (project_id, user_id)
) WITHOUT ROWID;
CREATE INDEX players_project_first ON players(project_id, first_seen);

CREATE TABLE play_sessions (
	id          INTEGER PRIMARY KEY,
	project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	session_key TEXT NOT NULL,
	user_id     INTEGER NOT NULL,
	job_id      TEXT NOT NULL DEFAULT '',
	started_at  INTEGER NOT NULL,
	ended_at    INTEGER,
	is_new      INTEGER NOT NULL DEFAULT 0,
	platform    TEXT NOT NULL DEFAULT '',
	country     TEXT NOT NULL DEFAULT '',
	UNIQUE (project_id, session_key)
);
CREATE INDEX play_sessions_project_start ON play_sessions(project_id, started_at);
CREATE INDEX play_sessions_open ON play_sessions(project_id, job_id) WHERE ended_at IS NULL;

CREATE TABLE events (
	id         INTEGER PRIMARY KEY,
	project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	ts         INTEGER NOT NULL,
	name       TEXT NOT NULL,
	user_id    INTEGER,
	job_id     TEXT NOT NULL DEFAULT '',
	value      REAL,
	props      TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX events_project_name_ts ON events(project_id, name, ts);
CREATE INDEX events_project_ts ON events(project_id, ts);

CREATE TABLE metrics (
	project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	name       TEXT NOT NULL,
	ts         INTEGER NOT NULL,
	job_id     TEXT NOT NULL DEFAULT '',
	kind       TEXT NOT NULL,
	count      INTEGER NOT NULL,
	sum        REAL NOT NULL,
	min        REAL NOT NULL,
	max        REAL NOT NULL,
	last       REAL NOT NULL
);
CREATE INDEX metrics_project_name_ts ON metrics(project_id, name, ts);
`,
	`
-- v2: team collaboration, releases, sessions, performance, security, alerting.

ALTER TABLE users ADD COLUMN handle TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN title TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN avatar TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN prefs TEXT NOT NULL DEFAULT '{}';
UPDATE users SET handle = lower(substr(email, 1, instr(email, '@') - 1)) || CASE WHEN id > 1 THEN id ELSE '' END;
CREATE UNIQUE INDEX users_handle ON users(handle);

ALTER TABLE projects ADD COLUMN github_repo TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN github_token TEXT NOT NULL DEFAULT '';     -- encrypted
ALTER TABLE projects ADD COLUMN signing_secret TEXT NOT NULL DEFAULT '';   -- encrypted
ALTER TABLE projects ADD COLUMN require_signature INTEGER NOT NULL DEFAULT 0;
ALTER TABLE projects ADD COLUMN hash_user_ids INTEGER NOT NULL DEFAULT 0;
ALTER TABLE projects ADD COLUMN user_hash_secret TEXT NOT NULL DEFAULT ''; -- encrypted
ALTER TABLE projects ADD COLUMN ingest_rate INTEGER NOT NULL DEFAULT 2000; -- items/second
ALTER TABLE projects ADD COLUMN client_rate INTEGER NOT NULL DEFAULT 60;   -- client items/minute/player

-- Issues: assignment, GitHub link, comments and activity.
ALTER TABLE issues ADD COLUMN assignee_id INTEGER REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE issues ADD COLUMN github_number INTEGER;
ALTER TABLE issues ADD COLUMN github_url TEXT NOT NULL DEFAULT '';
ALTER TABLE issues ADD COLUMN comment_count INTEGER NOT NULL DEFAULT 0;

CREATE TABLE issue_activity (
	id         INTEGER PRIMARY KEY,
	issue_id   INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
	user_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
	kind       TEXT NOT NULL,          -- comment | status | assign | github | regression | first_seen
	body       TEXT NOT NULL DEFAULT '',
	data       TEXT NOT NULL DEFAULT '{}',
	created_at INTEGER NOT NULL,
	edited_at  INTEGER
);
CREATE INDEX issue_activity_issue ON issue_activity(issue_id, created_at);

CREATE TABLE notifications (
	id         INTEGER PRIMARY KEY,
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE,
	issue_id   INTEGER REFERENCES issues(id) ON DELETE CASCADE,
	actor_id   INTEGER REFERENCES users(id) ON DELETE SET NULL,
	kind       TEXT NOT NULL,          -- mention | assigned | comment | alert
	body       TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	read_at    INTEGER
);
CREATE INDEX notifications_user ON notifications(user_id, created_at DESC);

-- Releases (from the SDK and the workshell release CLI).
CREATE TABLE releases (
	project_id   INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	version      TEXT NOT NULL,
	commit_sha   TEXT NOT NULL DEFAULT '',
	branch       TEXT NOT NULL DEFAULT '',
	message      TEXT NOT NULL DEFAULT '',
	author       TEXT NOT NULL DEFAULT '',
	place_version TEXT NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL,
	first_seen   INTEGER,
	last_seen    INTEGER,
	sourcemap    TEXT NOT NULL DEFAULT '{}', -- instance path -> file path
	PRIMARY KEY (project_id, version)
) WITHOUT ROWID;

ALTER TABLE game_servers ADD COLUMN release TEXT NOT NULL DEFAULT '';
ALTER TABLE error_events ADD COLUMN release TEXT NOT NULL DEFAULT '';
ALTER TABLE error_events ADD COLUMN session_key TEXT NOT NULL DEFAULT '';
ALTER TABLE logs ADD COLUMN session_key TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN session_key TEXT NOT NULL DEFAULT '';
ALTER TABLE play_sessions ADD COLUMN release TEXT NOT NULL DEFAULT '';
ALTER TABLE play_sessions ADD COLUMN error_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE play_sessions ADD COLUMN attrs TEXT NOT NULL DEFAULT '{}';
UPDATE game_servers SET release = place_version;
UPDATE error_events SET release = place_version;
CREATE INDEX error_events_session ON error_events(project_id, session_key) WHERE session_key != '';
CREATE INDEX logs_session ON logs(project_id, session_key) WHERE session_key != '';
CREATE INDEX logs_user ON logs(project_id, user_id, ts) WHERE user_id IS NOT NULL;
CREATE INDEX events_session ON events(project_id, session_key) WHERE session_key != '';
CREATE INDEX events_user ON events(project_id, user_id, ts) WHERE user_id IS NOT NULL;
CREATE INDEX play_sessions_user ON play_sessions(project_id, user_id, started_at);
CREATE INDEX error_events_release ON error_events(project_id, release);
CREATE INDEX error_events_user ON error_events(project_id, user_id, ts) WHERE user_id IS NOT NULL;

-- Performance tracing.
CREATE TABLE spans (
	id          INTEGER PRIMARY KEY,
	project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	ts          INTEGER NOT NULL,
	name        TEXT NOT NULL,
	duration_ms REAL NOT NULL,
	status      TEXT NOT NULL DEFAULT 'ok',
	job_id      TEXT NOT NULL DEFAULT '',
	session_key TEXT NOT NULL DEFAULT '',
	user_id     INTEGER,
	release     TEXT NOT NULL DEFAULT '',
	trace_id    TEXT NOT NULL DEFAULT '',
	parent      TEXT NOT NULL DEFAULT '',
	attrs       TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX spans_project_name_ts ON spans(project_id, name, ts);
CREATE INDEX spans_session ON spans(project_id, session_key) WHERE session_key != '';
CREATE INDEX spans_user ON spans(project_id, user_id, ts) WHERE user_id IS NOT NULL;

-- Client performance samples (FPS, memory, ping) per session.
CREATE TABLE client_perf (
	project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	session_key TEXT NOT NULL,
	ts          INTEGER NOT NULL,
	fps         REAL NOT NULL,
	memory_mb   REAL NOT NULL,
	ping_ms     REAL NOT NULL,
	platform    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX client_perf_project_ts ON client_perf(project_id, ts);
CREATE INDEX client_perf_session ON client_perf(project_id, session_key);

-- Exploit / anti-cheat detections.
CREATE TABLE detections (
	id          INTEGER PRIMARY KEY,
	project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	ts          INTEGER NOT NULL,
	user_id     INTEGER,
	session_key TEXT NOT NULL DEFAULT '',
	job_id      TEXT NOT NULL DEFAULT '',
	kind        TEXT NOT NULL,
	severity    TEXT NOT NULL DEFAULT 'medium', -- low | medium | high
	action      TEXT NOT NULL DEFAULT '',      -- none | kicked | banned | ...
	source      TEXT NOT NULL DEFAULT 'game',  -- game | workshell (auto-detected)
	details     TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX detections_project_ts ON detections(project_id, ts);
CREATE INDEX detections_user ON detections(project_id, user_id, ts);

-- Alerting.
CREATE TABLE alert_channels (
	id         INTEGER PRIMARY KEY,
	project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	name       TEXT NOT NULL,
	kind       TEXT NOT NULL DEFAULT 'webhook', -- discord | webhook
	url        TEXT NOT NULL,                   -- encrypted
	created_at INTEGER NOT NULL
);

CREATE TABLE alert_rules (
	id             INTEGER PRIMARY KEY,
	project_id     INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	name           TEXT NOT NULL,
	kind           TEXT NOT NULL,
	threshold      REAL NOT NULL DEFAULT 0,
	window_minutes INTEGER NOT NULL DEFAULT 5,
	cooldown_minutes INTEGER NOT NULL DEFAULT 30,
	channel_id     INTEGER REFERENCES alert_channels(id) ON DELETE SET NULL,
	enabled        INTEGER NOT NULL DEFAULT 1,
	last_fired_at  INTEGER,
	created_at     INTEGER NOT NULL
);

CREATE TABLE alert_history (
	id         INTEGER PRIMARY KEY,
	project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	rule_id    INTEGER REFERENCES alert_rules(id) ON DELETE SET NULL,
	kind       TEXT NOT NULL,
	title      TEXT NOT NULL,
	detail     TEXT NOT NULL DEFAULT '',
	link       TEXT NOT NULL DEFAULT '',
	delivered  INTEGER NOT NULL DEFAULT 0,
	error      TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE INDEX alert_history_project ON alert_history(project_id, created_at DESC);

-- Login throttling and an audit trail of sensitive changes.
CREATE TABLE audit_log (
	id         INTEGER PRIMARY KEY,
	project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE,
	user_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
	action     TEXT NOT NULL,
	detail     TEXT NOT NULL DEFAULT '',
	ip         TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE INDEX audit_log_project ON audit_log(project_id, created_at DESC);
`,
	// v3: whiteboards, chart annotations, and span ids so traces can be
	// rebuilt as trees.
	`
ALTER TABLE spans ADD COLUMN span_id TEXT NOT NULL DEFAULT '';
CREATE INDEX spans_trace ON spans(project_id, trace_id) WHERE trace_id != '';

CREATE TABLE whiteboards (
	id          INTEGER PRIMARY KEY,
	project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	title       TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	created_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
	created_at  INTEGER NOT NULL,
	updated_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
	updated_at  INTEGER NOT NULL
);
CREATE INDEX whiteboards_project ON whiteboards(project_id, updated_at DESC);

-- One row per canvas element, so two people editing different elements never
-- overwrite each other. ids are generated by the client.
CREATE TABLE whiteboard_items (
	board_id   INTEGER NOT NULL REFERENCES whiteboards(id) ON DELETE CASCADE,
	id         TEXT NOT NULL,
	kind       TEXT NOT NULL,  -- sticky | text | shape | connector | embed | frame
	x          REAL NOT NULL DEFAULT 0,
	y          REAL NOT NULL DEFAULT 0,
	w          REAL NOT NULL DEFAULT 0,
	h          REAL NOT NULL DEFAULT 0,
	z          REAL NOT NULL DEFAULT 0,
	data       TEXT NOT NULL DEFAULT '{}',
	rev        INTEGER NOT NULL DEFAULT 1,
	updated_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (board_id, id)
) WITHOUT ROWID;

-- Notes pinned to a timestamp (and optionally a value) on a dashboard chart.
CREATE TABLE chart_annotations (
	id         INTEGER PRIMARY KEY,
	project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	chart_key  TEXT NOT NULL,
	ts         INTEGER NOT NULL,
	value      REAL,
	series     TEXT NOT NULL DEFAULT '',
	text       TEXT NOT NULL,
	created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX chart_annotations_key ON chart_annotations(project_id, chart_key, ts);
`,
	// v4: ingest idempotency. A batch that was written but not acknowledged
	// (a crash between commit and ack) is redelivered; its hash is skipped.
	`
CREATE TABLE ingested_batches (
	hash        BLOB PRIMARY KEY,
	received_at INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX ingested_batches_received ON ingested_batches(received_at);
`,
	// v5: sample caps. Each issue keeps a few full events (stack, breadcrumbs,
	// context) per release; the rest fold into per-minute tally rows
	// (sampled = 0) that carry only a count, so a crash loop across a big
	// fleet costs a counter, not a copy of every stack trace.
	`
ALTER TABLE error_events ADD COLUMN sampled INTEGER NOT NULL DEFAULT 1;
CREATE UNIQUE INDEX error_events_tally ON error_events(issue_id, ts, release, job_id) WHERE sampled = 0;
CREATE INDEX error_events_samples ON error_events(issue_id, ts) WHERE sampled = 1;
CREATE TABLE issue_samples (
	issue_id INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
	release  TEXT NOT NULL,
	n        INTEGER NOT NULL,
	PRIMARY KEY (issue_id, release)
) WITHOUT ROWID;
INSERT INTO issue_samples (issue_id, release, n) SELECT issue_id, release, COUNT(*) FROM error_events GROUP BY 1, 2;
`,
	// v6: player erasure (Roblox right-to-erasure webhooks and the manual
	// button) needs to find a player's issue links, and a webhook secret.
	`
ALTER TABLE projects ADD COLUMN roblox_webhook_secret TEXT NOT NULL DEFAULT '';
CREATE INDEX issue_users_user ON issue_users(user_id);
`,
	// v7: single sign-on. An identity is a provider's stable subject for a
	// user; SSO-only users have an empty password_hash.
	`
CREATE TABLE user_identities (
	provider   TEXT NOT NULL,
	subject    TEXT NOT NULL,
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	email      TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	last_used  INTEGER NOT NULL,
	PRIMARY KEY (provider, subject)
) WITHOUT ROWID;
CREATE INDEX user_identities_user ON user_identities(user_id);
`,
}
