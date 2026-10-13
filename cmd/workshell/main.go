// Command workshell runs the Workshell server (SDK ingestion, the dashboard
// API and the web UI), and provides the `release` command for CI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/workshell/workshell"
	"github.com/workshell/workshell/internal/config"
	"github.com/workshell/workshell/internal/db"
	"github.com/workshell/workshell/internal/pipeline"
	"github.com/workshell/workshell/internal/secure"
	"github.com/workshell/workshell/internal/server"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "release":
			if err := runRelease(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "workshell release:", err)
				os.Exit(1)
			}
			return
		case "version", "-version", "--version":
			fmt.Println("workshell", version)
			return
		case "help", "-h", "--help":
			fmt.Println(`Usage:
  workshell [-debug]        run the server (configure with WORKSHELL_* environment variables)
  workshell release [flags] register a release (git commit + Rojo sourcemap); see "workshell release -h"
  workshell version`)
			return
		}
	}
	debug := flag.Bool("debug", false, "enable debug logging")
	flag.Parse()

	level := slog.LevelInfo
	if *debug || os.Getenv("WORKSHELL_DEBUG") == "true" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	if err := serve(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func serve(log *slog.Logger) error {
	cfg := config.Load()
	database, err := db.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer database.Close()

	key, err := secure.LoadKey(cfg.SecretKey, cfg.DataDir)
	if err != nil {
		return err
	}
	box, err := secure.NewBox(key)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var queue pipeline.Queue = pipeline.NewMemQueue(20_000)
	var broker pipeline.Broker = pipeline.NewMemBroker()
	if cfg.RedisURL != "" {
		rdb, err := pipeline.Connect(ctx, cfg.RedisURL)
		if err != nil {
			return err
		}
		defer rdb.Close()
		rq, err := pipeline.NewRedisQueue(ctx, rdb, log)
		if err != nil {
			return err
		}
		queue = rq
		broker = pipeline.NewRedisBroker(ctx, rdb)
		log.Info("using redis for ingest queue and live streaming")
	}
	defer broker.Close()

	srv := server.New(server.Options{
		Config: cfg, DB: database, Log: log, Web: workshell.WebFS(), SDK: workshell.SDKFS(),
		Box: box, Queue: queue, Broker: broker,
	})
	srv.Run(ctx)

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("workshell listening", "addr", cfg.Addr, "data", cfg.DataDir, "version", version)
		errc <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		httpServer.Shutdown(shutdownCtx)
		// Let the writer drain what's already queued in memory.
		for i := 0; i < 50 && queue.Pending() > 0 && cfg.RedisURL == ""; i++ {
			time.Sleep(100 * time.Millisecond)
		}
	}
	return nil
}
