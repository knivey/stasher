package main

import (
	"context"
	"errors"
	"flag"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"stasher/internal/api"
	"stasher/internal/config"
	"stasher/internal/db"
	"stasher/internal/files"
	"stasher/internal/webfs"
)

func main() {
	// Go's mime table has no .webmanifest entry — without this the PWA
	// manifest is served as text/plain and browsers reject the install.
	mime.AddExtensionType(".webmanifest", "application/manifest+json")

	configPath := flag.String("config", "", "path to YAML config file")
	flag.Parse()
	cmd := flag.Arg(0) // "" or "serve" runs the server; "migrate" only migrates

	cfg, err := config.Load(*configPath)
	if err != nil {
		fatal(err)
	}
	setupLogging(cfg.LogLevel)

	dbPath := filepath.Join(cfg.DataDir, "stasher.db")
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		fatal(err)
	}
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		fatal(err)
	}
	defer sqlDB.Close()
	if err := db.Migrate(sqlDB); err != nil {
		fatal(err)
	}
	slog.Info("database ready", "path", dbPath)
	if cmd == "migrate" {
		return
	}

	storage, err := files.New(filepath.Join(cfg.DataDir, "uploads"))
	if err != nil {
		fatal(err)
	}
	srv := api.New(&cfg, sqlDB, storage)

	dist, err := fs.Sub(webfs.FS, "dist")
	if err != nil {
		fatal(err)
	}
	fileServer := http.FileServer(http.FS(dist))

	// SPA history-mode fallback: real static files (built assets, manifest,
	// icons) are served when they exist; any other unmatched non-API path
	// rewrites to index.html so history-mode routing works.
	// (chi's Mount would swallow an outer NotFound, so the fallback is
	// registered inside the api router itself.)
	srv.SetSPAFallback(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, "/api/") || strings.HasPrefix(req.URL.Path, "/files/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":{"code":"not_found","message":"route not found"}}`))
			return
		}
		if st, err := fs.Stat(dist, strings.TrimPrefix(req.URL.Path, "/")); err == nil && !st.IsDir() {
			fileServer.ServeHTTP(w, req)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		req.URL.Path = "/"
		fileServer.ServeHTTP(w, req)
	})

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		slog.Info("listening", "addr", cfg.ListenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpSrv.Shutdown(ctx)
}

func fatal(err error) {
	slog.Error(err.Error())
	os.Exit(1)
}

func setupLogging(level string) {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lv})))
}
