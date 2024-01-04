package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httplog/v2"
	"github.com/lmittmann/tint"
	"github.com/spf13/pflag"
	"libdb.so/hserve"
	"libdb.so/hypnoview/lib/httputil"
	"libdb.so/hypnoview/lib/hypnohub"
	"libdb.so/hypnoview/lib/hypnohub/popular"
	"libdb.so/hypnoview/lib/hypnohub/query"
)

//go:embed frontend/*
var frontendFS embed.FS

var (
	httpAddr = ":8080"
	verbose  = false
)

func main() {
	pflag.StringVarP(&httpAddr, "listen-address", "l", httpAddr, "HTTP address to listen on")
	pflag.BoolVarP(&verbose, "verbose", "v", verbose, "verbose logging")
	pflag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if err := run(ctx); err != nil {
		log.Fatalln(err)
	}
}

func run(ctx context.Context) error {
	minLevel := slog.LevelInfo
	if verbose {
		minLevel = slog.LevelDebug
	}

	logger := slog.New(tint.NewHandler(os.Stderr, &tint.Options{
		Level: minLevel,
	}))
	slog.SetDefault(logger)

	loggedHTTPClient := httputil.UseClientMiddlewares(
		&http.Client{Timeout: 10 * time.Second},
		httputil.WithClientLogger(logger.With("client", "hypnohub"), httputil.ClientLogOpts{
			LogResponse:        true,
			ResponseLevel:      slog.LevelDebug,
			LogResponseError:   true,
			ResponseErrorLevel: slog.LevelError,
		}),
	)

	client := hypnohub.FromHTTPClient(loggedHTTPClient)
	updater := popular.NewPopularQueryUpdater(client)

	r := chi.NewMux()
	r.Route("/api/popular", func(r chi.Router) {
		r.Use(middleware.Recoverer)
		r.Use(middleware.NoCache)
		if verbose {
			r.Use(httplog.Handler(&httplog.Logger{
				Logger:  logger.With("component", "http"),
				Options: httplog.Options{LogLevel: slog.LevelDebug},
			}))
		}

		r.Get("/daily", handlePopular(updater, popular.Daily))
		r.Get("/weekly", handlePopular(updater, popular.Weekly))
		r.Get("/monthly", handlePopular(updater, popular.Monthly))
	})

	r.Group(func(r chi.Router) {
		var rootFS fs.FS
		if _, err := os.Stat("cmd/popular-query-app/frontend"); err == nil {
			logger.Info("detected repository layout, using local frontend")
			rootFS = os.DirFS("cmd/popular-query-app/frontend")
		} else {
			r.Use(withETagCache(hashFS(frontendFS), true))
			rootFS, _ = fs.Sub(frontendFS, "frontend")
		}
		r.Mount("/", http.FileServer(http.FS(rootFS)))
	})

	logger.Info("server is listening", "addr", httpAddr)
	return hserve.ListenAndServe(ctx, httpAddr, r)
}

func handlePopular(updater *popular.PopularQueryUpdater, period popular.TimePeriod) http.HandlerFunc {
	var queryFunc func(ctx context.Context) (query.Query, error)
	switch period {
	case popular.Daily:
		queryFunc = updater.DailyPopularQuery
	case popular.Weekly:
		queryFunc = updater.WeeklyPopularQuery
	case popular.Monthly:
		queryFunc = updater.MonthlyPopularQuery
	default:
		panic("invalid period")
	}

	return func(w http.ResponseWriter, r *http.Request) {
		// Intentionally use the background context so that the query is never
		// interrupted, otherwise the user can abuse the endpoint to spam
		// the server with requests.
		query, err := queryFunc(context.Background())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, query.String())
	}
}

// hashFS returns a hash of the given filesystem.
func hashFS(filesystem fs.FS) string {
	hasher := sha256.New()
	fs.WalkDir(filesystem, ".", func(path string, d fs.DirEntry, err error) error {
		b, _ := fs.ReadFile(filesystem, path)
		hasher.Write(b)
		return nil
	})
	hash := hasher.Sum(nil)[:8]
	return base64.RawURLEncoding.EncodeToString(hash)
}

// withETagCache returns a middleware that caches the response using the given
// etag value. If the client sends the same etag, the response is not generated
// again.
func withETagCache(etag string, weak bool) func(next http.Handler) http.Handler {
	if weak {
		etag = "W/" + etag
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ifNoneMatch := r.Header.Get("If-None-Match")
			if ifNoneMatch == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}

			w.Header().Set("ETag", etag)
			next.ServeHTTP(w, r)
		})
	}
}
