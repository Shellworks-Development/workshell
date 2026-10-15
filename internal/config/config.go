// Package config loads Workshell's runtime configuration from the environment.
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// Addr is the listen address, e.g. ":8080".
	Addr string
	// DataDir holds the SQLite database.
	DataDir string
	// PublicURL is this instance's externally reachable URL, used in SDK setup snippets.
	PublicURL string
	// SecureCookies sets the Secure flag on session cookies (enable behind HTTPS).
	SecureCookies bool
	// TrustProxy trusts X-Forwarded-For / X-Forwarded-Proto headers.
	TrustProxy bool
	// MaxIngestBytes caps the (decompressed) size of one ingest request.
	MaxIngestBytes int64
	// SessionTTL is how long a dashboard login lasts.
	SessionTTL time.Duration
	// SecretKey is the master key for encrypting stored secrets (hex or base64,
	// 32 bytes). If empty, a key is generated in DataDir/secret.key.
	SecretKey string
	// RedisURL enables Redis for the ingest queue and live streaming
	// (e.g. redis://localhost:6379/0). Empty = in-process queue.
	RedisURL string

	// Optional ClickHouse HTTP telemetry backend. The database must exist.
	ClickHouseURL      string
	ClickHouseDatabase string
	ClickHouseUser     string
	ClickHousePassword string
	ClickHouseMigrate  bool

	// OIDC single sign-on (Google, Microsoft Entra, Okta, Authentik, ...).
	// Enabled when OIDCIssuer and OIDCClientID are set; needs PublicURL.
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	// OIDCName labels the sign-in button ("Continue with <name>").
	OIDCName string
	// OIDCTrustEmail accepts emails the provider doesn't mark as verified
	// (Microsoft Entra omits email_verified). Only for a single-tenant provider.
	OIDCTrustEmail bool
	// CFAccessTeam (e.g. "shellworks" or "shellworks.cloudflareaccess.com") and
	// CFAccessAUD enable sign-in from a verified Cloudflare Access token.
	CFAccessTeam string
	CFAccessAUD  string
	// SSOAllowedDomains lets anyone with a verified email on these domains
	// create an account through SSO. Others need an account first.
	SSOAllowedDomains []string
	// NoPasswordLogin turns off email and password sign-in, requiring SSO.
	NoPasswordLogin bool
}

func Load() Config {
	return Config{
		Addr:               str("WORKSHELL_ADDR", ":8080"),
		DataDir:            abs(str("WORKSHELL_DATA_DIR", "./data")),
		PublicURL:          str("WORKSHELL_PUBLIC_URL", ""),
		SecureCookies:      boolean("WORKSHELL_SECURE_COOKIES", false),
		TrustProxy:         boolean("WORKSHELL_TRUST_PROXY", false),
		MaxIngestBytes:     int64(integer("WORKSHELL_MAX_INGEST_BYTES", 8<<20)),
		SessionTTL:         time.Duration(integer("WORKSHELL_SESSION_DAYS", 30)) * 24 * time.Hour,
		SecretKey:          str("WORKSHELL_SECRET_KEY", ""),
		RedisURL:           str("WORKSHELL_REDIS_URL", ""),
		ClickHouseURL:      str("WORKSHELL_CLICKHOUSE_URL", ""),
		ClickHouseDatabase: str("WORKSHELL_CLICKHOUSE_DATABASE", "default"),
		ClickHouseUser:     str("WORKSHELL_CLICKHOUSE_USER", "default"),
		ClickHousePassword: str("WORKSHELL_CLICKHOUSE_PASSWORD", ""),
		ClickHouseMigrate:  boolean("WORKSHELL_CLICKHOUSE_MIGRATE", false),

		OIDCIssuer:        str("WORKSHELL_OIDC_ISSUER", ""),
		OIDCClientID:      str("WORKSHELL_OIDC_CLIENT_ID", ""),
		OIDCClientSecret:  str("WORKSHELL_OIDC_CLIENT_SECRET", ""),
		OIDCName:          str("WORKSHELL_OIDC_NAME", "SSO"),
		OIDCTrustEmail:    boolean("WORKSHELL_OIDC_TRUST_EMAIL", false),
		CFAccessTeam:      str("WORKSHELL_CF_ACCESS_TEAM", ""),
		CFAccessAUD:       str("WORKSHELL_CF_ACCESS_AUD", ""),
		SSOAllowedDomains: list("WORKSHELL_SSO_ALLOWED_DOMAINS"),
		NoPasswordLogin:   !boolean("WORKSHELL_PASSWORD_LOGIN", true),
	}
}

func str(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func list(key string) []string {
	var out []string
	for _, v := range strings.Split(str(key, ""), ",") {
		if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func integer(key string, fallback int) int {
	if v, err := strconv.Atoi(str(key, "")); err == nil {
		return v
	}
	return fallback
}

func boolean(key string, fallback bool) bool {
	if v, err := strconv.ParseBool(str(key, "")); err == nil {
		return v
	}
	return fallback
}

func abs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}
