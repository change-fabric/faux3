// Command faux3 runs a single-container S3-compatible object store for
// local development.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/change-fabric/faux3/internal/config"
	"github.com/change-fabric/faux3/internal/s3api"
	"github.com/change-fabric/faux3/internal/sigv4"
	"github.com/change-fabric/faux3/internal/store"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "version", "--version", "-v":
			fmt.Fprintln(stdout, version)
			return 0
		case "healthcheck":
			return healthcheck(stderr)
		case "serve":
		default:
			fmt.Fprintf(stderr, "faux3: unknown command %q (use serve, healthcheck, or version)\n", args[0])
			return 2
		}
	}
	return serve(stderr)
}

func serve(stderr io.Writer) int {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintf(stderr, "faux3: invalid configuration: %v\n", err)
		return 1
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	if err := probeDataDir(cfg.DataDir); err != nil {
		fmt.Fprintln(stderr, "faux3:", err)
		return 1
	}

	st, err := store.Open(store.Options{Dir: cfg.DataDir, Fsync: cfg.Fsync, MaxObjectSize: cfg.MaxObjectSize, Region: cfg.Region, Logger: log})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	srv := &http.Server{
		Addr:              net.JoinHostPort("0.0.0.0", strconv.Itoa(cfg.Port)),
		Handler:           debugLog(log, newServer(log, cfg, st)),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("faux3 listening", "addr", srv.Addr, "version", version, "region", cfg.Region,
		"data_dir", cfg.DataDir, "cors", cfg.CORSOrigins, "fsync", cfg.Fsync)

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("server failed", "err", err)
			return 1
		}
	case <-ctx.Done():
		log.Info("shutting down")
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			log.Error("shutdown failed", "err", err)
			return 1
		}
	}
	return 0
}

// probeDataDir fails fast when the storage root is missing or not writable,
// which is the usual outcome of a bind mount owned by the host user.
func probeDataDir(dir string) error {
	f, err := os.CreateTemp(dir, ".faux3-probe-*")
	if err != nil {
		return fmt.Errorf("data dir %s is not writable by uid %d (faux3 runs as uid 65532:65532): %v. "+
			"For a bind mount, chown the host directory to 65532:65532, or set `user: \"<host uid>:<host gid>\"` "+
			"on the faux3 service in compose; a named volume avoids this entirely", dir, os.Getuid(), err)
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// healthcheck probes the local server; it backs the Docker HEALTHCHECK
// because the distroless image has no shell or curl.
func healthcheck(stderr io.Writer) int {
	port := os.Getenv("FAUX3_PORT")
	if port == "" {
		port = "9000"
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/-/healthz")
	if err != nil {
		fmt.Fprintln(stderr, "faux3: healthcheck failed:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(stderr, "faux3: healthcheck failed: status", resp.StatusCode)
		return 1
	}
	return 0
}

// newServer builds the S3 API handler from the configuration.
func newServer(log *slog.Logger, cfg config.Config, st store.Store) *s3api.Server {
	return &s3api.Server{
		Store: st,
		Verifier: &sigv4.Verifier{
			AccessKeyID:     cfg.AccessKeyID,
			SecretAccessKey: cfg.SecretAccessKey,
			Region:          cfg.Region,
			ClockSkew:       cfg.ClockSkew,
			Now:             time.Now,
		},
		Region:      cfg.Region,
		CORSOrigins: cfg.CORSOrigins,
		Log:         log,
	}
}
