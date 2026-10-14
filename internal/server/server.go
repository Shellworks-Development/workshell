// Package server implements Workshell's HTTP API: SDK ingestion, the
// dashboard API, live streaming, alerting, and serving the built web UI.
package server

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/workshell/workshell/internal/config"
	"github.com/workshell/workshell/internal/db"
	"github.com/workshell/workshell/internal/pipeline"
	"github.com/workshell/workshell/internal/secure"
)

type Server struct {
	cfg     config.Config
	db      *db.DB
	log     *slog.Logger
	web     fs.FS
	sdk     fs.FS
	box     *secure.Box
	queue   pipeline.Queue
	broker  pipeline.Broker
	alerts  *alerter
	limits  *limiters
	replays *replayCache
}

type Options struct {
	Config config.Config
	DB     *db.DB
	Log    *slog.Logger
	Web    fs.FS
	SDK    fs.FS
	Box    *secure.Box
	Queue  pipeline.Queue
	Broker pipeline.Broker
}

func New(o Options) *Server {
	s := &Server{
		cfg:     o.Config,
		db:      o.DB,
		log:     o.Log,
		web:     o.Web,
		sdk:     o.SDK,
		box:     o.Box,
		queue:   o.Queue,
		broker:  o.Broker,
		limits:  newLimiters(),
		replays: newReplayCache(),
	}
	s.alerts = newAlerter(s)
	return s
}

// Run starts background work until ctx ends: the ingest writer, alert
// delivery and evaluation, and retention.
func (s *Server) Run(ctx context.Context) {
	s.migrateLegacyWebhooks(ctx)
	go s.queue.Consume(ctx, func(jobs [][]byte) []error { return s.processJobs(context.WithoutCancel(ctx), jobs) })
	go s.alerts.run(ctx)
	go s.maintenanceLoop(ctx)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	h := func(pattern string, fn handlerFunc) { mux.HandleFunc(pattern, s.wrap(fn)) }
	authed := func(pattern string, fn func(w http.ResponseWriter, r *http.Request, u *user) error) {
		mux.HandleFunc(pattern, s.wrap(s.requireUser(fn)))
	}
	proj := func(pattern string, fn func(w http.ResponseWriter, r *http.Request, p *project) error) {
		mux.HandleFunc(pattern, s.wrap(s.requireUser(s.withProject(func(w http.ResponseWriter, r *http.Request, p *project, _ *user) error {
			return fn(w, r, p)
		}))))
	}
	projectUser := func(pattern string, fn func(w http.ResponseWriter, r *http.Request, p *project, u *user) error) {
		mux.HandleFunc(pattern, s.wrap(s.requireUser(s.withProject(fn))))
	}

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	// SDK and CI endpoints (authenticated by project ingest key).
	h("POST /api/v1/ingest", s.handleIngest)
	h("POST /api/v1/roblox/{pid}/webhook", s.handleRobloxWebhook)
	h("POST /api/v1/releases", s.handleCreateRelease)
	h("GET /api/sdk/Workshell.rbxmx", s.handleSDKModel)

	// Auth and profile.
	h("GET /api/auth/state", s.handleAuthState)
	h("POST /api/auth/setup", s.handleSetup)
	h("POST /api/auth/login", s.handleLogin)
	h("POST /api/auth/logout", s.handleLogout)
	authed("POST /api/auth/password", s.handleChangePassword)
	authed("PATCH /api/me", s.handleUpdateProfile)
	authed("GET /api/notifications", s.handleNotifications)
	authed("POST /api/notifications/read", s.handleReadNotifications)

	// Team.
	authed("GET /api/users", s.handleListUsers)
	authed("POST /api/users", s.handleCreateUser)
	authed("GET /api/users/{uid}", s.handleGetUser)
	authed("DELETE /api/users/{uid}", s.handleDeleteUser)

	// Projects.
	authed("GET /api/projects", s.handleListProjects)
	authed("POST /api/projects", s.handleCreateProject)
	proj("GET /api/projects/{pid}", s.handleGetProject)
	projectUser("PATCH /api/projects/{pid}", s.handleUpdateProject)
	projectUser("DELETE /api/projects/{pid}", s.handleDeleteProject)
	projectUser("POST /api/projects/{pid}/rotate-key", s.handleRotateKey)
	projectUser("POST /api/projects/{pid}/signing-secret", s.handleRotateSigningSecret)
	proj("GET /api/projects/{pid}/overview", s.handleOverview)
	proj("GET /api/projects/{pid}/search", s.handleSearch)
	proj("GET /api/projects/{pid}/live", s.handleLive)
	proj("GET /api/projects/{pid}/audit", s.handleAuditLog)

	// Issues and collaboration.
	proj("GET /api/projects/{pid}/issues", s.handleListIssues)
	projectUser("POST /api/projects/{pid}/issues/bulk", s.handleBulkIssues)
	proj("GET /api/projects/{pid}/issues/{iid}", s.handleGetIssue)
	projectUser("PATCH /api/projects/{pid}/issues/{iid}", s.handleUpdateIssue)
	proj("DELETE /api/projects/{pid}/issues/{iid}", s.handleDeleteIssue)
	proj("GET /api/projects/{pid}/issues/{iid}/events", s.handleIssueEvents)
	proj("GET /api/projects/{pid}/issues/{iid}/activity", s.handleIssueActivity)
	projectUser("POST /api/projects/{pid}/issues/{iid}/comments", s.handleAddComment)
	projectUser("PATCH /api/projects/{pid}/issues/{iid}/comments/{cid}", s.handleEditComment)
	projectUser("DELETE /api/projects/{pid}/issues/{iid}/comments/{cid}", s.handleDeleteComment)
	projectUser("POST /api/projects/{pid}/issues/{iid}/github", s.handleCreateGitHubIssue)
	proj("GET /api/projects/{pid}/issues/{iid}/github", s.handleGetGitHubIssue)

	// Logs, sessions and players.
	proj("GET /api/projects/{pid}/logs", s.handleLogs)
	proj("GET /api/projects/{pid}/sessions", s.handleListSessions)
	proj("GET /api/projects/{pid}/sessions/{sid}", s.handleGetSession)
	proj("GET /api/projects/{pid}/players", s.handleListPlayers)
	proj("GET /api/projects/{pid}/players/{userId}", s.handleGetPlayer)
	projectUser("DELETE /api/projects/{pid}/players/{userId}", s.handleErasePlayer)
	projectUser("POST /api/projects/{pid}/roblox-webhook-secret", s.handleRotateRobloxSecret)

	// Servers and performance.
	proj("GET /api/projects/{pid}/servers", s.handleListServers)
	proj("GET /api/projects/{pid}/servers/stats", s.handleServerStats)
	proj("GET /api/projects/{pid}/servers/{jobId}", s.handleGetServer)
	proj("GET /api/projects/{pid}/performance", s.handlePerformance)
	proj("GET /api/projects/{pid}/performance/spans", s.handleSpanDetail)
	proj("GET /api/projects/{pid}/traces/{traceId}", s.handleTrace)

	// Releases.
	proj("GET /api/projects/{pid}/releases", s.handleListReleases)
	proj("GET /api/projects/{pid}/releases/{version}", s.handleGetRelease)

	// Security.
	proj("GET /api/projects/{pid}/security", s.handleSecurity)
	proj("GET /api/projects/{pid}/security/detections", s.handleDetections)

	// Analytics.
	proj("GET /api/projects/{pid}/analytics/players", s.handlePlayerAnalytics)
	proj("GET /api/projects/{pid}/analytics/retention", s.handleRetention)
	proj("GET /api/projects/{pid}/analytics/breakdown", s.handleBreakdown)
	proj("GET /api/projects/{pid}/events", s.handleListEventNames)
	proj("GET /api/projects/{pid}/events/series", s.handleEventSeries)
	proj("GET /api/projects/{pid}/events/recent", s.handleRecentEvents)
	proj("POST /api/projects/{pid}/events/funnel", s.handleFunnel)
	proj("GET /api/projects/{pid}/metrics", s.handleListMetrics)
	proj("GET /api/projects/{pid}/metrics/series", s.handleMetricSeries)

	// Whiteboards and chart annotations.
	proj("GET /api/projects/{pid}/whiteboards", s.handleListBoards)
	projectUser("POST /api/projects/{pid}/whiteboards", s.handleCreateBoard)
	proj("GET /api/projects/{pid}/whiteboards/{bid}", s.handleGetBoard)
	projectUser("PATCH /api/projects/{pid}/whiteboards/{bid}", s.handleUpdateBoard)
	projectUser("DELETE /api/projects/{pid}/whiteboards/{bid}", s.handleDeleteBoard)
	projectUser("POST /api/projects/{pid}/whiteboards/{bid}/items", s.handleBoardItems)
	projectUser("POST /api/projects/{pid}/whiteboards/{bid}/presence", s.handleBoardPresence)
	proj("GET /api/projects/{pid}/annotations", s.handleListAnnotations)
	projectUser("POST /api/projects/{pid}/annotations", s.handleCreateAnnotation)
	projectUser("DELETE /api/projects/{pid}/annotations/{aid}", s.handleDeleteAnnotation)

	// Alerts.
	proj("GET /api/projects/{pid}/alerts", s.handleListAlerts)
	projectUser("POST /api/projects/{pid}/alerts/channels", s.handleCreateChannel)
	projectUser("DELETE /api/projects/{pid}/alerts/channels/{cid}", s.handleDeleteChannel)
	proj("POST /api/projects/{pid}/alerts/channels/{cid}/test", s.handleTestChannel)
	projectUser("POST /api/projects/{pid}/alerts/rules", s.handleSaveRule)
	projectUser("PATCH /api/projects/{pid}/alerts/rules/{rid}", s.handleSaveRule)
	projectUser("DELETE /api/projects/{pid}/alerts/rules/{rid}", s.handleDeleteRule)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	})
	mux.Handle("/", s.spaHandler())

	return requestLogger(s.log, securityHeaders(mux))
}

// contentSecurityPolicy keeps any injected markup inert: no inline or
// third-party scripts, no framing, no plugin content.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob: https://avatars.githubusercontent.com; font-src 'self' data:; connect-src 'self'; object-src 'none'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			h.Set("X-Frame-Options", "DENY")
			h.Set("Content-Security-Policy", contentSecurityPolicy)
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		}
		next.ServeHTTP(w, r)
	})
}

// spaHandler serves the built Vue app, falling back to index.html for
// client-side routes.
func (s *Server) spaHandler() http.Handler {
	files := http.FileServerFS(s.web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if st, err := fs.Stat(s.web, p); err == nil && !st.IsDir() {
				switch {
				case strings.HasPrefix(p, "assets/"):
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				case p == "sw.js" || p == "theme.js" || p == "manifest.webmanifest":
					// Must revalidate so new deploys (and service worker updates) are picked up.
					w.Header().Set("Cache-Control", "no-cache")
				}
				if strings.HasSuffix(p, ".webmanifest") {
					w.Header().Set("Content-Type", "application/manifest+json")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		// A hashed asset that no longer exists (an old tab after a deploy) must
		// 404 so the app can recover, not get index.html served as JavaScript.
		if strings.HasPrefix(p, "assets/") {
			http.NotFound(w, r)
			return
		}
		index, err := fs.ReadFile(s.web, "index.html")
		if err != nil {
			http.Error(w, "Workshell web UI is not built. Run `make build`.", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(index)
	})
}

func (s *Server) maintenanceLoop(ctx context.Context) {
	minute := time.NewTicker(time.Minute)
	hour := time.NewTicker(time.Hour)
	defer minute.Stop()
	defer hour.Stop()

	s.runRetention(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-minute.C:
			s.closeStaleServers(ctx)
			s.evaluateAlertRules(ctx)
			s.limits.sweep()
			s.replays.sweep()
		case <-hour.C:
			s.runRetention(ctx)
		}
	}
}

// audit records a sensitive change (key rotation, deletion, settings).
func (s *Server) audit(r *http.Request, projectID int64, u *user, action, detail string) {
	var pid, uid any
	if projectID > 0 {
		pid = projectID
	}
	if u != nil {
		uid = u.ID
	}
	_, err := s.db.W.ExecContext(r.Context(),
		`INSERT INTO audit_log (project_id, user_id, action, detail, ip, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		pid, uid, action, truncate(detail, 500), clientIP(r, s.cfg.TrustProxy), nowMs())
	if err != nil {
		s.log.Warn("audit log", "err", err)
	}
}
