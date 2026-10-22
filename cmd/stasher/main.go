package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
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

	// Validate arguments before touching any data: Go's flag package stops
	// parsing at the first non-flag arg, so a flag AFTER the subcommand
	// (`stasher migrate -config x.yaml`) would be silently ignored and the
	// command could act on the wrong data dir.
	cmd := ""
	if args := flag.Args(); len(args) > 0 {
		cmd = args[0]
		if len(args) > 1 {
			for _, a := range args[1:] {
				if strings.HasPrefix(a, "-") {
					fmt.Fprintln(os.Stderr, "flags must precede the subcommand (e.g. stasher -config stasher.yaml migrate)")
					usage()
					os.Exit(2)
				}
			}
			fmt.Fprintf(os.Stderr, "unknown argument %q\n", args[1])
			usage()
			os.Exit(2)
		}
	}
	switch cmd {
	case "", "serve", "migrate":
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", cmd)
		usage()
		os.Exit(2)
	}

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

	// sweep trash whose retention window closed while the server was down
	// (entries trashed while running are swept on GET /trash)
	if err := srv.PurgeExpiredTrash(context.Background()); err != nil {
		slog.Error("startup trash purge failed", "err", err)
	}

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
	if err := httpSrv.Shutdown(ctx); err != nil {
		slog.Warn("shutdown error", "err", err)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: stasher [-config path] [serve|migrate]

  stasher [-config path]           start the server (default)
  stasher [-config path] serve     start the server
  stasher [-config path] migrate   apply migrations and exit

flags:
  -config path   path to YAML config file (must precede the subcommand)
`)
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
