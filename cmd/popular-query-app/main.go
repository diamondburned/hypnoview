package main

import (
	"context"
	"crypto/sha1"
	"embed"
	"encoding/base64"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
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

//go:embed frontend
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
		if _, err := os.Stat("cmd/popular-query-api/frontend"); err == nil {
			logger.Info("detected repository layout, using local frontend")
			rootFS = os.DirFS("cmd/popular-query-api/frontend")
		} else {
			if tag := binaryTag(); tag != "" {
				logger.Debug("using binary tag", "tag", tag)
				r.Use(withETagCache(tag, true))
			} else {
				logger.Debug("no binary tag, disabling cache")
				r.Use(middleware.NoCache)
			}
			rootFS = frontendFS
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

// binaryTag returns a unique tag for the binary, identifying the version and
// build time.
func binaryTag() string {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	// Ensure that vcs tags are present in the build info.
	for _, setting := range buildInfo.Settings {
		if setting.Key == "vcs.revision" {
			goto ok
		}
	}
	return ""
ok:
	hash := sha1.Sum([]byte(buildInfo.String()))
	return base64.RawURLEncoding.EncodeToString(hash[:16])
}

// withETagCache returns a middleware that caches the response using the given
// etag value. If the client sends the same etag, the response is not generated
// again.
func withETagCache(etag string, weak bool) func(next http.Handler) http.Handler {
	headerValue := etag
	if weak {
		headerValue = "W/" + headerValue
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ifNoneMatch := r.Header.Get("If-None-Match")
			if ifNoneMatch == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}

			w.Header().Set("ETag", headerValue)
			next.ServeHTTP(w, r)
		})
	}
}
