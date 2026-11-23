package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gaze.d14.pet"
	"gaze.d14.pet/httputil"
	"gaze.d14.pet/internal/index"
	"gaze.d14.pet/internal/server/web"
	"github.com/go-chi/httplog/v2"
	"github.com/lmittmann/tint"
	"github.com/spf13/pflag"
	slogjournal "github.com/systemd/slog-journal"
	"libdb.so/hserve"
)

type options struct {
	listenAddr   string
	dbPath       string
	pollInterval time.Duration
	minPosts     int
	seedOnly     bool
	noPoller     bool
	devMode      bool
	verbose      bool
}

func main() {
	opts := options{
		listenAddr:   ":8080",
		dbPath:       "hypnogaze.db",
		pollInterval: index.DefaultPollInterval,
		minPosts:     index.DefaultSeedPosts,
	}

	pflag.StringVarP(&opts.listenAddr, "listen-addr", "l", opts.listenAddr, "HTTP listen address")
	pflag.StringVarP(&opts.dbPath, "db", "d", opts.dbPath, "Path to SQLite index database")
	pflag.DurationVarP(&opts.pollInterval, "poll-interval", "i", opts.pollInterval, "Background index poll interval")
	pflag.IntVar(&opts.minPosts, "min-posts", opts.minPosts, "Minimum number of posts to maintain/seed in index")
	pflag.IntVar(&opts.minPosts, "seed-posts", opts.minPosts, "Alias for --min-posts (number of posts to seed)")
	pflag.BoolVar(&opts.seedOnly, "seed-only", false, "Seed the database with --min-posts and exit immediately")
	pflag.BoolVar(&opts.noPoller, "no-poller", false, "Disable background Hypnohub polling (serve local index only)")
	pflag.BoolVar(&opts.devMode, "dev", false, "Enable development reload mode for web assets")
	pflag.BoolVarP(&opts.verbose, "verbose", "v", opts.verbose, "Enable debug logging")
	pflag.Parse()

	logLevel := slog.LevelInfo
	if opts.verbose {
		logLevel = slog.LevelDebug
	}

	logger, err := newLogger(logLevel)
	slog.SetDefault(logger)
	if err != nil {
		logger.Warn("failed to initialize journal logger, falling back to stderr", "error", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	store, err := index.Open(ctx, opts.dbPath)
	if err != nil {
		logger.Error("failed to open index database", "path", opts.dbPath, "error", err)
		os.Exit(1)
	}
	defer store.Close()

	httpClient := httputil.UseClientMiddlewares(
		&http.Client{Timeout: 45 * time.Second},
		httputil.WithClientLogger(logger, httputil.ClientLogOpts{
			LogRequest:         opts.verbose,
			RequestLevel:       slog.LevelDebug,
			LogResponse:        opts.verbose,
			ResponseLevel:      slog.LevelDebug,
			LogResponseError:   true,
			ResponseErrorLevel: slog.LevelError,
		}),
	)
	client := hypnohub.FromHTTPClient(httpClient)
	store.SetClient(client)

	poller := index.NewPoller(store, client, index.PollerConfig{
		SeedPosts:    opts.minPosts,
		PollInterval: opts.pollInterval,
		Logger:       logger.With("component", "poller"),
	})

	if opts.seedOnly {
		logger.Info("running initial seed-only mode", "db", opts.dbPath, "target_posts", opts.minPosts)
		if _, err := poller.Seed(ctx, opts.minPosts); err != nil {
			logger.Error("seed failed", "error", err)
			os.Exit(1)
		}
		stats, _ := store.Stats(ctx)
		if stats != nil {
			logger.Info("seed completed",
				"total_posts", stats.TotalPosts,
				"total_tags", stats.TotalTags,
				"total_snapshots", stats.TotalSnapshots,
				"oldest_post", stats.OldestPostAt,
				"newest_post", stats.NewestPostAt)
		}
		return
	}

	if !opts.noPoller {
		go func() {
			if err := poller.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("background poller exited with error", "error", err)
			}
		}()
	}

	webServer := web.NewServer(store, opts.devMode)

	reqLogger := &httplog.Logger{
		Logger: logger.With("service", "http"),
		Options: httplog.Options{
			LogLevel: logLevel,
			QuietDownRoutes: []string{
				"/static/",
				"/api/v1/tags",
			},
			QuietDownPeriod: 10 * time.Second,
		},
	}

	srv := &http.Server{
		Addr:              opts.listenAddr,
		Handler:           httplog.Handler(reqLogger)(webServer),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	logger.Info("starting hypnogaze server",
		"addr", opts.listenAddr,
		"db", opts.dbPath,
		"poll_interval", opts.pollInterval)

	if err := hserve.ListenAndServeExisting(ctx, srv); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

func newLogger(level slog.Level) (*slog.Logger, error) {
	if useJournal, _ := strconv.ParseBool(os.Getenv("SLOG_JOURNAL")); useJournal {
		jh, err := slogjournal.NewHandler(&slogjournal.Options{
			Level:        level,
			ReplaceGroup: journalKey,
			ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
				a.Key = journalKey(a.Key)
				return a
			},
		})
		if err == nil {
			return slog.New(jh), nil
		}
		return slog.New(tint.NewHandler(os.Stderr, &tint.Options{
			Level:   level,
			NoColor: false,
		})), err
	}

	return slog.New(tint.NewHandler(os.Stderr, &tint.Options{
		Level:   level,
		NoColor: false,
	})), nil
}

func journalKey(k string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - ('a' - 'A')
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_':
			return r
		default:
			return '_'
		}
	}, k)
}
