// Package config loads Workshell's runtime configuration from the environment.
package config

import (
	"os"
	"path/filepath"
	"strconv"
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
}

func Load() Config {
	return Config{
		Addr:           str("WORKSHELL_ADDR", ":8080"),
		DataDir:        abs(str("WORKSHELL_DATA_DIR", "./data")),
		PublicURL:      str("WORKSHELL_PUBLIC_URL", ""),
		SecureCookies:  boolean("WORKSHELL_SECURE_COOKIES", false),
		TrustProxy:     boolean("WORKSHELL_TRUST_PROXY", false),
		MaxIngestBytes: int64(integer("WORKSHELL_MAX_INGEST_BYTES", 8<<20)),
		SessionTTL:     time.Duration(integer("WORKSHELL_SESSION_DAYS", 30)) * 24 * time.Hour,
		SecretKey:      str("WORKSHELL_SECRET_KEY", ""),
		RedisURL:       str("WORKSHELL_REDIS_URL", ""),
	}
}

func str(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
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
