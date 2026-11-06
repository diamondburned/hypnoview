// Package index provides the local SQLite post and popularity index.
package index

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"libdb.so/hypnoview"
	"libdb.so/hypnoview/internal/index/sqlcgen"
	"libdb.so/lazymigrate"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

const initPragma = `
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA busy_timeout = 10000;
PRAGMA cache_size = -64000;
PRAGMA temp_store = MEMORY;
PRAGMA mmap_size = 268435456;
`

var (
	// ErrNotFound is returned when a requested post does not exist in the index.
	ErrNotFound = errors.New("post not found")
	// ErrInvalidWindow is returned when an unknown time window is requested.
	ErrInvalidWindow = errors.New("invalid time window")
	// ErrInvalidSort is returned when an unknown sort mode is requested.
	ErrInvalidSort = errors.New("invalid sort mode")
)

// Window represents a rolling popularity time window.
type Window string

const (
	Window6Hours  Window = "6h"
	WindowDay     Window = "24h"
	Window3Days   Window = "3d"
	WindowWeek    Window = "7d"
	Window2Weeks  Window = "14d"
	WindowMonth   Window = "30d"
	WindowAllTime Window = "all"
)

// Duration returns the duration corresponding to the window, or 0 for all-time.
func (w Window) Duration() (time.Duration, error) {
	switch w {
	case Window6Hours, "rising":
		return 6 * time.Hour, nil
	case WindowDay, "day", "daily", "1d", "":
		return 24 * time.Hour, nil
	case Window3Days:
		return 3 * 24 * time.Hour, nil
	case WindowWeek, "week", "weekly", "1w":
		return 7 * 24 * time.Hour, nil
	case Window2Weeks, "2w":
		return 14 * 24 * time.Hour, nil
	case WindowMonth, "month", "monthly", "1m":
		return 30 * 24 * time.Hour, nil
	case WindowAllTime, "alltime":
		return 0, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrInvalidWindow, w)
	}
}

// Normalize canonicalizes window aliases to their primary Window value.
func (w Window) Normalize() Window {
	switch w {
	case Window6Hours, "rising":
		return Window6Hours
	case WindowDay, "day", "daily", "1d", "":
		return WindowDay
	case Window3Days:
		return Window3Days
	case WindowWeek, "week", "weekly", "1w":
		return WindowWeek
	case Window2Weeks, "2w":
		return Window2Weeks
	case WindowMonth, "month", "monthly", "1m":
		return WindowMonth
	case WindowAllTime, "alltime":
		return WindowAllTime
	default:
		return w
	}
}

// DampingHours returns the exposure damping constant τ (in hours) for rate calculations.
func (w Window) DampingHours() float64 {
	switch w.Normalize() {
	case Window6Hours:
		return 1.5
	case WindowDay:
		return 2.0
	case Window3Days:
		return 6.0
	case WindowWeek:
		return 12.0
	case Window2Weeks:
		return 24.0
	case WindowMonth:
		return 48.0
	case WindowAllTime:
		return 168.0
	default:
		return 2.0
	}
}

// SortMode controls how posts are ordered.
type SortMode string

const (
	// SortHot ranks posts by damped upvote velocity (score_delta / (eff_hours + tau)^gravity).
	SortHot SortMode = "hot"
	// SortTop ranks posts by total score gained within the sliding window (score_delta).
	SortTop SortMode = "top"
	// SortNew ranks posts by creation timestamp descending.
	SortNew SortMode = "new"
	// SortScore ranks posts by total cumulative score descending.
	SortScore SortMode = "score"
)

// Normalize canonicalizes a SortMode.
func (s SortMode) Normalize() (SortMode, error) {
	switch s {
	case SortHot, "rate", "velocity", "":
		return SortHot, nil
	case SortTop, "delta", "popular":
		return SortTop, nil
	case SortNew, "recent", "latest":
		return SortNew, nil
	case SortScore, "total":
		return SortScore, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidSort, s)
	}
}

// Store manages the local SQLite database for posts, tags, and score snapshots.
type Store struct {
	db *sql.DB
	q  *sqlcgen.Queries
}

// Open opens or creates a SQLite index database at the given file path (or ":memory:").
// If an existing database file on disk is corrupted or unreadable, it is backed up to .bak
// and a clean new database is initialized in its place.
func Open(ctx context.Context, path string) (*Store, error) {
	isMem := path == ":memory:" || strings.HasPrefix(path, "file::memory:") || strings.Contains(path, "mode=memory")
	store, err := openStore(ctx, path, isMem)
	if err == nil {
		return store, nil
	}
	if isMem {
		return nil, err
	}

	// Check if an existing file on disk failed to open/migrate/read
	if fi, statErr := os.Stat(path); statErr == nil && !fi.IsDir() {
		backupPath := path + ".bak"
		slog.Warn("database file is unreadable or corrupted; moving to .bak and recreating",
			"path", path,
			"backup", backupPath,
			"err", err)

		_ = os.Rename(path, backupPath)
		_ = os.Remove(path + "-wal")
		_ = os.Remove(path + "-shm")

		// Recreate brand new database
		return openStore(ctx, path, isMem)
	}

	return nil, err
}

func openStore(ctx context.Context, path string, isMem bool) (*Store, error) {
	dsn := path
	var pragmas []string
	pragmas = append(pragmas, "_pragma=foreign_keys(1)", "_pragma=synchronous(NORMAL)", "_pragma=busy_timeout(10000)")
	if !isMem {
		pragmas = append(pragmas, "_pragma=journal_mode(WAL)")
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	dsn = dsn + sep + strings.Join(pragmas, "&")

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite db: %w", err)
	}
	if isMem {
		db.SetMaxOpenConns(1)
	} else {
		// Limit writer concurrency bugs in SQLite while keeping WAL concurrent reads fast.
		db.SetMaxOpenConns(4)
	}

	execPragmas := initPragma
	if isMem {
		execPragmas = `
PRAGMA foreign_keys = ON;
PRAGMA synchronous = NORMAL;
PRAGMA busy_timeout = 10000;
PRAGMA cache_size = -64000;
PRAGMA temp_store = MEMORY;
`
	}

	if _, err := db.ExecContext(ctx, execPragmas); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("executing sqlite pragmas: %w", err)
	}

	if err := lazymigrate.Migrate(ctx, db, schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrating sqlite schema: %w", err)
	}

	var checkResult string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check;").Scan(&checkResult); err != nil || checkResult != "ok" {
		_ = db.Close()
		if err == nil {
			err = fmt.Errorf("quick_check failed: %s", checkResult)
		}
		return nil, fmt.Errorf("verifying database integrity: %w", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM posts;").Scan(&count); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("verifying posts table: %w", err)
	}

	return &Store{
		db: db,
		q:  sqlcgen.New(db),
	}, nil
}

// Close closes the underlying SQLite database.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying *sql.DB for advanced inspection or testing.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Queries returns the sqlc-generated query interface.
func (s *Store) Queries() *sqlcgen.Queries {
	return s.q
}

// PruneOldSnapshots prunes score snapshots older than the specified cutoff timestamp.
func (s *Store) PruneOldSnapshots(ctx context.Context, before time.Time) error {
	return s.q.PruneOldSnapshots(ctx, before.UTC().Unix())
}

// IndexedPost represents a post stored in the local index along with its
// computed popularity metrics for a requested sliding window.
type IndexedPost struct {
	ID            hypnohub.PostID `json:"id"`
	CreatedAt     time.Time       `json:"created_at"`
	ChangedAt     time.Time       `json:"changed_at"`
	Score         int             `json:"score"`
	Rating        hypnohub.Rating `json:"rating"`
	CreatorID     int             `json:"creator_id"`
	ParentID      hypnohub.PostID `json:"parent_id"`
	MD5           string          `json:"md5"`
	Source        string          `json:"source"`
	Status        string          `json:"status"`
	HasNotes      bool            `json:"has_notes"`
	HasComments   bool            `json:"has_comments"`
	HasChildren   bool            `json:"has_children"`

	FileURL       string          `json:"file_url"`
	FileExt       string          `json:"file_ext"`
	Width         int             `json:"width"`
	Height        int             `json:"height"`

	SampleURL     string          `json:"sample_url"`
	SampleWidth   int             `json:"sample_width"`
	SampleHeight  int             `json:"sample_height"`

	PreviewURL    string          `json:"preview_url"`
	PreviewWidth  int             `json:"preview_width"`
	PreviewHeight int             `json:"preview_height"`

	Tags          []string        `json:"tags"`
	FirstSeenAt   time.Time       `json:"first_seen_at"`
	LastPolledAt  time.Time       `json:"last_polled_at"`

	// Popularity metrics for the queried window:
	BaselineScore int             `json:"baseline_score"`
	ScoreDelta    int             `json:"score_delta"`
	WindowHours   float64         `json:"window_hours"`
	ScoreRate     float64         `json:"score_rate"` // upvotes per hour (damped)
	HotScore      float64         `json:"hot_score"`
}

// ScorePoint is a single historical score observation for a post.
type ScorePoint struct {
	PollTime time.Time `json:"poll_time"`
	Score    int       `json:"score"`
}

// TagSummary represents a tag and its indexed post count.
type TagSummary struct {
	Name      string `json:"name"`
	PostCount int    `json:"post_count"`
}

// Stats contains summary statistics for the local index.
type Stats struct {
	TotalPosts       int       `json:"total_posts"`
	TotalTags        int       `json:"total_tags"`
	TotalSnapshots   int       `json:"total_snapshots"`
	TotalPollRuns    int       `json:"total_poll_runs"`
	OldestPostAt     time.Time `json:"oldest_post_at,omitempty"`
	NewestPostAt     time.Time `json:"newest_post_at,omitempty"`
	LastPollTime     time.Time `json:"last_poll_time,omitempty"`
	LastPollDuration float64   `json:"last_poll_duration_seconds,omitempty"`
	Seeded           bool      `json:"seeded"`
}

// IngestBatch upserts a batch of posts and records their score snapshots for pollTime.
// It also records the poll run metadata in poll_runs.
func (s *Store) IngestBatch(
	ctx context.Context,
	posts []hypnohub.Post,
	pollTime time.Time,
	startedAt time.Time,
	completedAt time.Time,
	pagesFetched int,
	isSeed bool,
) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	qtx := s.q.WithTx(tx)

	pollUnix := pollTime.UTC().Unix()
	tagIDCache := make(map[string]int64, 1024)
	affectedTagIDs := make(map[int64]struct{}, 1024)
	upserted := 0

	for _, p := range posts {
		if !p.ID.IsValid() {
			continue
		}

		tagsSlice := p.Tags.Split()
		tagsRaw := strings.Join(tagsSlice, " ")
		rating := string(p.Rating.Normalize())
		status := p.Status
		if status == "" {
			status = "active"
		}

		createdAt := p.CreatedAt.Time().UTC().Unix()
		changedAt := int64(p.ChangedAt)
		if changedAt == 0 {
			changedAt = createdAt
		}

		err := qtx.UpsertPost(ctx, sqlcgen.UpsertPostParams{
			ID:            int64(p.ID),
			CreatedAt:     createdAt,
			ChangedAt:     changedAt,
			Score:         int64(p.Score),
			Rating:        rating,
			CreatorID:     int64(p.CreatorID),
			ParentID:      int64(p.ParentID),
			MD5:           p.MD5,
			Source:        p.Source,
			Status:        status,
			HasNotes:      int64(boolToInt(p.HasNotes == "true")),
			HasComments:   int64(boolToInt(p.HasComments == "true")),
			HasChildren:   int64(boolToInt(p.HasChildren)),
			FileURL:       p.FileURL,
			FileExt:       p.FileExt(),
			Width:         int64(p.Width),
			Height:        int64(p.Height),
			SampleURL:     p.SampleURL,
			SampleWidth:   int64(p.SampleWidth),
			SampleHeight:  int64(p.SampleHeight),
			PreviewURL:    p.PreviewURL,
			PreviewWidth:  int64(p.PreviewWidth),
			PreviewHeight: int64(p.PreviewHeight),
			TagsRaw:       tagsRaw,
			FirstSeenAt:   pollUnix,
			LastPolledAt:  pollUnix,
		})
		if err != nil {
			return 0, fmt.Errorf("upserting post %d: %w", p.ID, err)
		}

		err = qtx.InsertScoreSnapshot(ctx, sqlcgen.InsertScoreSnapshotParams{
			PostID:   int64(p.ID),
			PollTime: pollUnix,
			Score:    int64(p.Score),
		})
		if err != nil {
			return 0, fmt.Errorf("inserting snapshot for post %d: %w", p.ID, err)
		}

		// Delete existing post tags and record their tag IDs so post counts update properly
		existingTagIDs, err := qtx.GetTagIDsByPostID(ctx, int64(p.ID))
		if err != nil {
			return 0, fmt.Errorf("querying existing tags for post %d: %w", p.ID, err)
		}
		for _, tid := range existingTagIDs {
			affectedTagIDs[tid] = struct{}{}
		}

		if err := qtx.DeletePostTagsByPostID(ctx, int64(p.ID)); err != nil {
			return 0, fmt.Errorf("deleting existing post_tags for post %d: %w", p.ID, err)
		}

		for _, tagName := range tagsSlice {
			tagName = strings.ToLower(strings.TrimSpace(tagName))
			if tagName == "" {
				continue
			}
			tagID, ok := tagIDCache[tagName]
			if !ok {
				if err := qtx.InsertTag(ctx, tagName); err != nil {
					return 0, fmt.Errorf("inserting tag %q: %w", tagName, err)
				}
				tagID, err = qtx.GetTagIDByName(ctx, tagName)
				if err != nil {
					return 0, fmt.Errorf("selecting tag %q: %w", tagName, err)
				}
				tagIDCache[tagName] = tagID
			}
			affectedTagIDs[tagID] = struct{}{}
			if err := qtx.InsertPostTag(ctx, sqlcgen.InsertPostTagParams{PostID: int64(p.ID), TagID: tagID}); err != nil {
				return 0, fmt.Errorf("linking post %d tag %q: %w", p.ID, tagName, err)
			}
		}

		upserted++
	}

	// Refresh tag post counts for all tags touched/affected in this batch.
	for tagID := range affectedTagIDs {
		if err := qtx.UpdateTagPostCount(ctx, tagID); err != nil {
			return 0, fmt.Errorf("updating count for tag %d: %w", tagID, err)
		}
	}

	err = qtx.InsertPollRun(ctx, sqlcgen.InsertPollRunParams{
		PollTime:      pollUnix,
		StartedAt:     startedAt.UTC().Unix(),
		CompletedAt:   completedAt.UTC().Unix(),
		PostsUpserted: int64(upserted),
		PagesFetched:  int64(pagesFetched),
		IsSeed:        int64(boolToInt(isSeed)),
	})
	if err != nil {
		return 0, fmt.Errorf("recording poll run: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing batch: %w", err)
	}

	return upserted, nil
}

// QueryOptions configures a query against the local post index.
type QueryOptions struct {
	Window      Window
	Sort        SortMode
	Rating      hypnohub.Rating // optional: "s", "q", "e", or "" for all
	IncludeTags []string
	ExcludeTags []string
	MediaType   string // optional: "image", "video", "animated", or ""
	Limit       int
	Offset      int
	Now         time.Time // defaults to latest poll time (or time.Now() if empty)
}

// QueryResult holds a page of queried posts and the total matching count.
type QueryResult struct {
	Posts       []IndexedPost `json:"posts"`
	Total       int           `json:"total"`
	Window      Window        `json:"window"`
	Sort        SortMode      `json:"sort"`
	WindowStart time.Time     `json:"window_start"`
	WindowEnd   time.Time     `json:"window_end"`
}

// QueryPosts queries the local SQLite index for posts matching the given window,
// sort mode, tags, and rating filters. It never contacts upstream Hypnohub.
func (s *Store) QueryPosts(ctx context.Context, opts QueryOptions) (*QueryResult, error) {
	window := opts.Window.Normalize()
	winDur, err := window.Duration()
	if err != nil {
		return nil, err
	}

	sortMode, err := opts.Sort.Normalize()
	if err != nil {
		return nil, err
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 48
	}
	if limit > 200 {
		limit = 200
	}
	offset := max(0, opts.Offset)

	refNow := opts.Now
	if refNow.IsZero() {
		refNow = s.latestReferenceTime(ctx)
	}
	refNow = refNow.UTC()
	nowUnix := refNow.Unix()

	var windowStartUnix int64
	if winDur > 0 {
		windowStartUnix = refNow.Add(-winDur).Unix()
	}

	tau := window.DampingHours()

	ratingStr := ""
	if opts.Rating != "" {
		r := opts.Rating.Normalize()
		if r == hypnohub.RatingSafe || r == hypnohub.RatingQuestionable || r == hypnohub.RatingExplicit {
			ratingStr = string(r)
		}
	}

	mediaTypeStr := ""
	switch strings.ToLower(strings.TrimSpace(opts.MediaType)) {
	case "video":
		mediaTypeStr = "video"
	case "animated":
		mediaTypeStr = "animated"
	case "image", "static":
		mediaTypeStr = "image"
	}

	includeTags := cleanTagList(opts.IncludeTags)
	excludeTags := cleanTagList(opts.ExcludeTags)

	// Fast path: When no tag filters are requested, execute 100% type-safe sqlc queries
	// without any dynamic string concatenation or overhead.
	if len(includeTags) == 0 && len(excludeTags) == 0 {
		if winDur == 0 {
			// All-time window: No score_snapshots join required!
			total, err := s.q.CountPostsAll(ctx, sqlcgen.CountPostsAllParams{
				Rating:    ratingStr,
				MediaType: mediaTypeStr,
			})
			if err != nil {
				return nil, fmt.Errorf("counting posts: %w", err)
			}

			var posts []IndexedPost
			switch sortMode {
			case SortNew:
				rows, err := s.q.ListPostsAllSortNew(ctx, sqlcgen.ListPostsAllSortNewParams{
					NowUnix:   nowUnix,
					Tau:       tau,
					Rating:    ratingStr,
					MediaType: mediaTypeStr,
					Limit:     int64(limit),
					Offset:    int64(offset),
				})
				if err != nil {
					return nil, fmt.Errorf("querying posts all (sort new): %w", err)
				}
				posts = make([]IndexedPost, len(rows))
				for i, r := range rows {
					posts[i] = rowToIndexedPostAllSortNew(r)
				}
			case SortScore:
				rows, err := s.q.ListPostsAllSortScore(ctx, sqlcgen.ListPostsAllSortScoreParams{
					NowUnix:   nowUnix,
					Tau:       tau,
					Rating:    ratingStr,
					MediaType: mediaTypeStr,
					Limit:     int64(limit),
					Offset:    int64(offset),
				})
				if err != nil {
					return nil, fmt.Errorf("querying posts all (sort score): %w", err)
				}
				posts = make([]IndexedPost, len(rows))
				for i, r := range rows {
					posts[i] = rowToIndexedPostAllSortScore(r)
				}
			case SortHot:
				rows, err := s.q.ListPostsAllSortHot(ctx, sqlcgen.ListPostsAllSortHotParams{
					NowUnix:   nowUnix,
					Tau:       tau,
					Rating:    ratingStr,
					MediaType: mediaTypeStr,
					Limit:     int64(limit),
					Offset:    int64(offset),
				})
				if err != nil {
					return nil, fmt.Errorf("querying posts all (sort hot): %w", err)
				}
				posts = make([]IndexedPost, len(rows))
				for i, r := range rows {
					posts[i] = rowToIndexedPostAllSortHot(r)
				}
			case SortTop:
				rows, err := s.q.ListPostsAllSortTop(ctx, sqlcgen.ListPostsAllSortTopParams{
					NowUnix:   nowUnix,
					Tau:       tau,
					Rating:    ratingStr,
					MediaType: mediaTypeStr,
					Limit:     int64(limit),
					Offset:    int64(offset),
				})
				if err != nil {
					return nil, fmt.Errorf("querying posts all (sort top): %w", err)
				}
				posts = make([]IndexedPost, len(rows))
				for i, r := range rows {
					posts[i] = rowToIndexedPostAllSortTop(r)
				}
			}

			winStart := time.Unix(windowStartUnix, 0).UTC()
			if len(posts) > 0 {
				winStart = posts[len(posts)-1].CreatedAt
			}

			return &QueryResult{
				Posts:       posts,
				Total:       int(total),
				Window:      window,
				Sort:        sortMode,
				WindowStart: winStart,
				WindowEnd:   refNow,
			}, nil
		}

		// Windowed fast-path (6h, 24h, 3d, 7d, 14d, 30d):
		total, err := s.q.CountPostsWindowed(ctx, sqlcgen.CountPostsWindowedParams{
			WindowStartUnix: windowStartUnix,
			NowUnix:         nowUnix,
			Rating:          ratingStr,
			MediaType:       mediaTypeStr,
		})
		if err != nil {
			return nil, fmt.Errorf("counting windowed posts: %w", err)
		}

		var posts []IndexedPost
		switch sortMode {
		case SortHot:
			rows, err := s.q.ListPostsWindowedSortHot(ctx, sqlcgen.ListPostsWindowedSortHotParams{
				WindowStartUnix: windowStartUnix,
				NowUnix:         nowUnix,
				Tau:             tau,
				Rating:          ratingStr,
				MediaType:       mediaTypeStr,
				Limit:           int64(limit),
				Offset:          int64(offset),
			})
			if err != nil {
				return nil, fmt.Errorf("querying windowed posts (sort hot): %w", err)
			}
			posts = make([]IndexedPost, len(rows))
			for i, r := range rows {
				posts[i] = rowToIndexedPostWindowedSortHot(r)
			}
		case SortTop:
			rows, err := s.q.ListPostsWindowedSortTop(ctx, sqlcgen.ListPostsWindowedSortTopParams{
				WindowStartUnix: windowStartUnix,
				NowUnix:         nowUnix,
				Tau:             tau,
				Rating:          ratingStr,
				MediaType:       mediaTypeStr,
				Limit:           int64(limit),
				Offset:          int64(offset),
			})
			if err != nil {
				return nil, fmt.Errorf("querying windowed posts (sort top): %w", err)
			}
			posts = make([]IndexedPost, len(rows))
			for i, r := range rows {
				posts[i] = rowToIndexedPostWindowedSortTop(r)
			}
		case SortNew:
			rows, err := s.q.ListPostsWindowedSortNew(ctx, sqlcgen.ListPostsWindowedSortNewParams{
				WindowStartUnix: windowStartUnix,
				NowUnix:         nowUnix,
				Tau:             tau,
				Rating:          ratingStr,
				MediaType:       mediaTypeStr,
				Limit:           int64(limit),
				Offset:          int64(offset),
			})
			if err != nil {
				return nil, fmt.Errorf("querying windowed posts (sort new): %w", err)
			}
			posts = make([]IndexedPost, len(rows))
			for i, r := range rows {
				posts[i] = rowToIndexedPostWindowedSortNew(r)
			}
		case SortScore:
			rows, err := s.q.ListPostsWindowedSortScore(ctx, sqlcgen.ListPostsWindowedSortScoreParams{
				WindowStartUnix: windowStartUnix,
				NowUnix:         nowUnix,
				Tau:             tau,
				Rating:          ratingStr,
				MediaType:       mediaTypeStr,
				Limit:           int64(limit),
				Offset:          int64(offset),
			})
			if err != nil {
				return nil, fmt.Errorf("querying windowed posts (sort score): %w", err)
			}
			posts = make([]IndexedPost, len(rows))
			for i, r := range rows {
				posts[i] = rowToIndexedPostWindowedSortScore(r)
			}
		}

		winStart := time.Unix(windowStartUnix, 0).UTC()
		return &QueryResult{
			Posts:       posts,
			Total:       int(total),
			Window:      window,
			Sort:        sortMode,
			WindowStart: winStart,
			WindowEnd:   refNow,
		}, nil
	}

	// Dynamic path: Required when includeTags or excludeTags are present.
	var (
		whereClauses = []string{"p.status = 'active'"}
		whereArgs    []any
	)

	if winDur > 0 {
		whereClauses = append(whereClauses, "(p.created_at >= ? OR (b.start_score IS NOT NULL AND p.score > b.start_score))")
		whereArgs = append(whereArgs, windowStartUnix)
	}

	if ratingStr != "" {
		whereClauses = append(whereClauses, "p.rating = ?")
		whereArgs = append(whereArgs, ratingStr)
	}

	switch mediaTypeStr {
	case "video":
		whereClauses = append(whereClauses, "p.file_ext IN ('mp4', 'webm')")
	case "animated":
		whereClauses = append(whereClauses, "p.file_ext IN ('mp4', 'webm', 'gif')")
	case "image":
		whereClauses = append(whereClauses, "p.file_ext NOT IN ('mp4', 'webm', 'gif')")
	}

	for _, tag := range includeTags {
		whereClauses = append(whereClauses, `p.id IN (
			SELECT pt.post_id FROM post_tags pt
			JOIN tags t ON t.id = pt.tag_id
			WHERE t.name = ?
		)`)
		whereArgs = append(whereArgs, tag)
	}

	for _, tag := range excludeTags {
		whereClauses = append(whereClauses, `p.id NOT IN (
			SELECT pt.post_id FROM post_tags pt
			JOIN tags t ON t.id = pt.tag_id
			WHERE t.name = ?
		)`)
		whereArgs = append(whereArgs, tag)
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	var orderBySQL string
	switch sortMode {
	case SortHot:
		orderBySQL = "hot_score DESC, score_rate DESC, score_delta DESC, p.score DESC, p.id DESC"
	case SortTop:
		orderBySQL = "score_delta DESC, hot_score DESC, score_rate DESC, p.score DESC, p.id DESC"
	case SortNew:
		orderBySQL = "p.created_at DESC, p.id DESC"
	case SortScore:
		orderBySQL = "p.score DESC, p.id DESC"
	}

	if winDur == 0 {
		// All-time window with tag filters: Fast path avoiding score_snapshots join!
		var total int
		countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM posts p WHERE %s`, whereSQL)
		if err := s.db.QueryRowContext(ctx, countQuery, whereArgs...).Scan(&total); err != nil {
			return nil, fmt.Errorf("counting posts: %w", err)
		}

		querySQL := fmt.Sprintf(`
			SELECT
				p.id, p.created_at, p.changed_at, p.score, p.rating, p.creator_id, p.parent_id,
				p.md5, p.source, p.status, p.has_notes, p.has_comments, p.has_children,
				p.file_url, p.file_ext, p.width, p.height,
				p.sample_url, p.sample_width, p.sample_height,
				p.preview_url, p.preview_width, p.preview_height,
				p.tags_raw, p.first_seen_at, p.last_polled_at,
				0 AS baseline_score,
				p.score AS score_delta,
				MAX(0.1, (? - p.created_at) / 3600.0) AS window_hours,
				CAST(p.score AS REAL) / (MAX(0.1, (? - p.created_at) / 3600.0) + ?) AS score_rate,
				(CAST(p.score AS REAL) / (MAX(0.1, (? - p.created_at) / 3600.0) + ?)) * POWER(MAX(1, p.score), 0.35) AS hot_score
			FROM posts p
			WHERE %s
			ORDER BY %s
			LIMIT ? OFFSET ?
		`, whereSQL, orderBySQL)

		args := make([]any, 0, 5+len(whereArgs)+2)
		args = append(args,
			nowUnix,
			nowUnix, tau,
			nowUnix, tau,
		)
		args = append(args, whereArgs...)
		args = append(args, limit, offset)

		rows, err := s.db.QueryContext(ctx, querySQL, args...)
		if err != nil {
			return nil, fmt.Errorf("querying posts all dynamic: %w", err)
		}
		defer rows.Close()

		posts := make([]IndexedPost, 0, limit)
		for rows.Next() {
			post, err := scanIndexedPost(rows)
			if err != nil {
				return nil, err
			}
			posts = append(posts, post)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterating posts: %w", err)
		}

		winStart := time.Unix(windowStartUnix, 0).UTC()
		if len(posts) > 0 {
			winStart = posts[len(posts)-1].CreatedAt
		}

		return &QueryResult{
			Posts:       posts,
			Total:       total,
			Window:      window,
			Sort:        sortMode,
			WindowStart: winStart,
			WindowEnd:   refNow,
		}, nil
	}

	// Windowed dynamic query with score_snapshots baseline CTE:
	const baselineCTE = `
		WITH baseline AS (
			SELECT post_id, score AS start_score, MIN(poll_time) AS start_time
			FROM score_snapshots
			WHERE poll_time >= ? AND poll_time < ?
			GROUP BY post_id
		)
	`

	var total int
	countQuery := fmt.Sprintf(`%s SELECT COUNT(*) FROM posts p LEFT JOIN baseline b ON b.post_id = p.id WHERE %s`, baselineCTE, whereSQL)
	countArgs := make([]any, 0, 2+len(whereArgs))
	countArgs = append(countArgs, windowStartUnix, nowUnix)
	countArgs = append(countArgs, whereArgs...)
	if err := s.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return nil, fmt.Errorf("counting posts: %w", err)
	}

	const (
		baselineExpr = `CASE WHEN p.created_at >= ? THEN 0 ELSE COALESCE(b.start_score, p.score) END`
		deltaExpr    = `CASE WHEN p.created_at >= ? THEN p.score ELSE MAX(0, p.score - COALESCE(b.start_score, p.score)) END`
		effHoursExpr = `MAX(0.1, (? - CASE WHEN p.created_at >= ? THEN p.created_at ELSE COALESCE(b.start_time, ?) END) / 3600.0)`
	)

	querySQL := fmt.Sprintf(`
		%s
		SELECT
			p.id, p.created_at, p.changed_at, p.score, p.rating, p.creator_id, p.parent_id,
			p.md5, p.source, p.status, p.has_notes, p.has_comments, p.has_children,
			p.file_url, p.file_ext, p.width, p.height,
			p.sample_url, p.sample_width, p.sample_height,
			p.preview_url, p.preview_width, p.preview_height,
			p.tags_raw, p.first_seen_at, p.last_polled_at,
			%s AS baseline_score,
			%s AS score_delta,
			%s AS window_hours,
			CAST(%s AS REAL) / (%s + ?) AS score_rate,
			(CAST(%s AS REAL) / (%s + ?)) * POWER(MAX(1, p.score), 0.35) AS hot_score
		FROM posts p
		LEFT JOIN baseline b ON b.post_id = p.id
		WHERE %s
		ORDER BY %s
		LIMIT ? OFFSET ?
	`,
		baselineCTE,
		baselineExpr,
		deltaExpr,
		effHoursExpr,
		deltaExpr,
		effHoursExpr,
		deltaExpr,
		effHoursExpr,
		whereSQL,
		orderBySQL,
	)

	args := make([]any, 0, 18+len(whereArgs))
	args = append(args,
		windowStartUnix, nowUnix,                   // CTE baseline
		windowStartUnix,                            // baselineExpr
		windowStartUnix,                            // deltaExpr
		nowUnix, windowStartUnix, windowStartUnix,  // effHoursExpr
		windowStartUnix,                            // score_rate deltaExpr
		nowUnix, windowStartUnix, windowStartUnix, tau, // score_rate effHoursExpr + tau
		windowStartUnix,                            // hot_score deltaExpr
		nowUnix, windowStartUnix, windowStartUnix, tau, // hot_score effHoursExpr + tau
	)
	args = append(args, whereArgs...)
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("querying windowed posts dynamic: %w", err)
	}
	defer rows.Close()

	posts := make([]IndexedPost, 0, limit)
	for rows.Next() {
		post, err := scanIndexedPost(rows)
		if err != nil {
			return nil, err
		}
		posts = append(posts, post)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating posts: %w", err)
	}

	winStart := time.Unix(windowStartUnix, 0).UTC()
	return &QueryResult{
		Posts:       posts,
		Total:       total,
		Window:      window,
		Sort:        sortMode,
		WindowStart: winStart,
		WindowEnd:   refNow,
	}, nil
}

// GetPost retrieves a single indexed post by ID along with its score history snapshots.
func (s *Store) GetPost(ctx context.Context, id hypnohub.PostID) (*IndexedPost, []ScorePoint, error) {
	refNow := s.latestReferenceTime(ctx).UTC()
	nowUnix := refNow.Unix()
	const tau = 2.0

	postRow, err := s.q.GetPostByID(ctx, int64(id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, fmt.Errorf("getting post %d: %w", id, err)
	}

	snapRows, err := s.q.GetSnapshotsByPostID(ctx, int64(id))
	if err != nil {
		return nil, nil, fmt.Errorf("getting snapshots for post %d: %w", id, err)
	}

	post := postToIndexed(postRow, nowUnix, tau)

	var history []ScorePoint
	// Include creation point (created_at, score 0) as the synthetic origin point if before first snapshot.
	history = append(history, ScorePoint{
		PollTime: post.CreatedAt,
		Score:    0,
	})
	for _, snap := range snapRows {
		t := time.Unix(snap.PollTime, 0).UTC()
		if len(history) > 0 && !t.After(history[len(history)-1].PollTime) {
			history[len(history)-1] = ScorePoint{PollTime: t, Score: int(snap.Score)}
		} else {
			history = append(history, ScorePoint{PollTime: t, Score: int(snap.Score)})
		}
	}

	return &post, history, nil
}

// SearchTags searches local indexed tags by prefix or substring, ordered by post count descending.
func (s *Store) SearchTags(ctx context.Context, query string, limit int) ([]TagSummary, error) {
	if limit <= 0 {
		limit = 30
	}
	if limit > 200 {
		limit = 200
	}

	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		rows, err := s.q.ListPopularTags(ctx, int64(limit))
		if err != nil {
			return nil, fmt.Errorf("searching tags: %w", err)
		}
		tags := make([]TagSummary, len(rows))
		for i, r := range rows {
			tags[i] = TagSummary{Name: r.Name, PostCount: int(r.PostCount)}
		}
		return tags, nil
	}

	rows, err := s.q.SearchTagsByPrefix(ctx, sqlcgen.SearchTagsByPrefixParams{
		Pattern: "%" + q + "%",
		Prefix:  q + "%",
		Limit:   int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("searching tags: %w", err)
	}
	tags := make([]TagSummary, len(rows))
	for i, r := range rows {
		tags[i] = TagSummary{Name: r.Name, PostCount: int(r.PostCount)}
	}
	return tags, nil
}

// Stats returns summary statistics about the local index database.
func (s *Store) Stats(ctx context.Context) (*Stats, error) {
	var st Stats
	totalPosts, err := s.q.CountPosts(ctx)
	if err != nil {
		return nil, err
	}
	st.TotalPosts = int(totalPosts)

	totalTags, err := s.q.CountTags(ctx)
	if err != nil {
		return nil, err
	}
	st.TotalTags = int(totalTags)

	totalSnapshots, err := s.q.CountSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	st.TotalSnapshots = int(totalSnapshots)

	totalPollRuns, err := s.q.CountPollRuns(ctx)
	if err != nil {
		return nil, err
	}
	st.TotalPollRuns = int(totalPollRuns)

	minMax, err := s.q.GetPostMinMaxCreatedAt(ctx)
	if err != nil {
		return nil, err
	}
	if v, ok := minMax.MinCreatedAt.(int64); ok && v > 0 {
		st.OldestPostAt = time.Unix(v, 0).UTC()
	}
	if v, ok := minMax.MaxCreatedAt.(int64); ok && v > 0 {
		st.NewestPostAt = time.Unix(v, 0).UTC()
	}

	lastPoll, err := s.q.GetLatestPollRun(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil && lastPoll.PollTime > 0 {
		st.LastPollTime = time.Unix(lastPoll.PollTime, 0).UTC()
		st.LastPollDuration = float64(max(0, lastPoll.CompletedAt-lastPoll.StartedAt))
	}

	seedCount, err := s.q.CountSeedPollRuns(ctx)
	if err != nil {
		return nil, err
	}
	st.Seeded = seedCount > 0 || st.TotalPosts >= 1000

	return &st, nil
}

// latestReferenceTime returns the latest poll_time (or newest post created_at) so that
// sliding windows align with the most recent indexed state.
func (s *Store) latestReferenceTime(ctx context.Context) time.Time {
	var latest int64
	if maxPoll, err := s.q.GetMaxPollTime(ctx); err == nil {
		if v, ok := maxPoll.(int64); ok && v > latest {
			latest = v
		}
	}
	if maxCreated, err := s.q.GetMaxCreatedAt(ctx); err == nil {
		if v, ok := maxCreated.(int64); ok && v > latest {
			latest = v
		}
	}

	if latest > 0 {
		return time.Unix(latest, 0).UTC()
	}
	return time.Now().UTC()
}

func postToIndexed(p sqlcgen.Post, nowUnix int64, tau float64) IndexedPost {
	effHours := max(0.1, float64(nowUnix-p.CreatedAt)/3600.0)
	scoreRate := float64(p.Score) / (effHours + tau)
	hotScore := scoreRate * math.Pow(float64(max(1, p.Score)), 0.35)

	return IndexedPost{
		ID:            hypnohub.PostID(p.ID),
		CreatedAt:     time.Unix(p.CreatedAt, 0).UTC(),
		ChangedAt:     time.Unix(p.ChangedAt, 0).UTC(),
		Score:         int(p.Score),
		Rating:        hypnohub.Rating(p.Rating).Normalize(),
		CreatorID:     int(p.CreatorID),
		ParentID:      hypnohub.PostID(p.ParentID),
		MD5:           p.MD5,
		Source:        p.Source,
		Status:        p.Status,
		HasNotes:      p.HasNotes != 0,
		HasComments:   p.HasComments != 0,
		HasChildren:   p.HasChildren != 0,
		FileURL:       p.FileURL,
		FileExt:       p.FileExt,
		Width:         int(p.Width),
		Height:        int(p.Height),
		SampleURL:     p.SampleURL,
		SampleWidth:   int(p.SampleWidth),
		SampleHeight:  int(p.SampleHeight),
		PreviewURL:    p.PreviewURL,
		PreviewWidth:  int(p.PreviewWidth),
		PreviewHeight: int(p.PreviewHeight),
		Tags:          strings.Fields(p.TagsRaw),
		FirstSeenAt:   time.Unix(p.FirstSeenAt, 0).UTC(),
		LastPolledAt:  time.Unix(p.LastPolledAt, 0).UTC(),
		BaselineScore: 0,
		ScoreDelta:    int(p.Score),
		WindowHours:   math.Round(effHours*100) / 100,
		ScoreRate:     math.Round(scoreRate*1000) / 1000,
		HotScore:      math.Round(hotScore*1000) / 1000,
	}
}

func newIndexedPost(
	id, createdAt, changedAt, score int64,
	rating string,
	creatorID, parentID int64,
	md5, source, status string,
	hasNotes, hasComments, hasChildren int64,
	fileURL, fileExt string,
	width, height int64,
	sampleURL string, sampleWidth, sampleHeight int64,
	previewURL string, previewWidth, previewHeight int64,
	tagsRaw string,
	firstSeenAt, lastPolledAt int64,
	baselineScore, scoreDelta int64,
	windowHours, scoreRate, hotScore float64,
) IndexedPost {
	return IndexedPost{
		ID:            hypnohub.PostID(id),
		CreatedAt:     time.Unix(createdAt, 0).UTC(),
		ChangedAt:     time.Unix(changedAt, 0).UTC(),
		Score:         int(score),
		Rating:        hypnohub.Rating(rating).Normalize(),
		CreatorID:     int(creatorID),
		ParentID:      hypnohub.PostID(parentID),
		MD5:           md5,
		Source:        source,
		Status:        status,
		HasNotes:      hasNotes != 0,
		HasComments:   hasComments != 0,
		HasChildren:   hasChildren != 0,
		FileURL:       fileURL,
		FileExt:       fileExt,
		Width:         int(width),
		Height:        int(height),
		SampleURL:     sampleURL,
		SampleWidth:   int(sampleWidth),
		SampleHeight:  int(sampleHeight),
		PreviewURL:    previewURL,
		PreviewWidth:  int(previewWidth),
		PreviewHeight: int(previewHeight),
		Tags:          strings.Fields(tagsRaw),
		FirstSeenAt:   time.Unix(firstSeenAt, 0).UTC(),
		LastPolledAt:  time.Unix(lastPolledAt, 0).UTC(),
		BaselineScore: int(baselineScore),
		ScoreDelta:    int(scoreDelta),
		WindowHours:   math.Round(windowHours*100) / 100,
		ScoreRate:     math.Round(scoreRate*1000) / 1000,
		HotScore:      math.Round(hotScore*1000) / 1000,
	}
}

func rowToIndexedPostAllSortNew(r sqlcgen.ListPostsAllSortNewRow) IndexedPost {
	return newIndexedPost(
		r.ID, r.CreatedAt, r.ChangedAt, r.Score, r.Rating, r.CreatorID, r.ParentID,
		r.MD5, r.Source, r.Status, r.HasNotes, r.HasComments, r.HasChildren,
		r.FileURL, r.FileExt, r.Width, r.Height,
		r.SampleURL, r.SampleWidth, r.SampleHeight,
		r.PreviewURL, r.PreviewWidth, r.PreviewHeight,
		r.TagsRaw, r.FirstSeenAt, r.LastPolledAt,
		r.BaselineScore, r.ScoreDelta, r.WindowHours, r.ScoreRate, r.HotScore,
	)
}

func rowToIndexedPostAllSortScore(r sqlcgen.ListPostsAllSortScoreRow) IndexedPost {
	return newIndexedPost(
		r.ID, r.CreatedAt, r.ChangedAt, r.Score, r.Rating, r.CreatorID, r.ParentID,
		r.MD5, r.Source, r.Status, r.HasNotes, r.HasComments, r.HasChildren,
		r.FileURL, r.FileExt, r.Width, r.Height,
		r.SampleURL, r.SampleWidth, r.SampleHeight,
		r.PreviewURL, r.PreviewWidth, r.PreviewHeight,
		r.TagsRaw, r.FirstSeenAt, r.LastPolledAt,
		r.BaselineScore, r.ScoreDelta, r.WindowHours, r.ScoreRate, r.HotScore,
	)
}

func rowToIndexedPostAllSortHot(r sqlcgen.ListPostsAllSortHotRow) IndexedPost {
	return newIndexedPost(
		r.ID, r.CreatedAt, r.ChangedAt, r.Score, r.Rating, r.CreatorID, r.ParentID,
		r.MD5, r.Source, r.Status, r.HasNotes, r.HasComments, r.HasChildren,
		r.FileURL, r.FileExt, r.Width, r.Height,
		r.SampleURL, r.SampleWidth, r.SampleHeight,
		r.PreviewURL, r.PreviewWidth, r.PreviewHeight,
		r.TagsRaw, r.FirstSeenAt, r.LastPolledAt,
		r.BaselineScore, r.ScoreDelta, r.WindowHours, r.ScoreRate, r.HotScore,
	)
}

func rowToIndexedPostAllSortTop(r sqlcgen.ListPostsAllSortTopRow) IndexedPost {
	return newIndexedPost(
		r.ID, r.CreatedAt, r.ChangedAt, r.Score, r.Rating, r.CreatorID, r.ParentID,
		r.MD5, r.Source, r.Status, r.HasNotes, r.HasComments, r.HasChildren,
		r.FileURL, r.FileExt, r.Width, r.Height,
		r.SampleURL, r.SampleWidth, r.SampleHeight,
		r.PreviewURL, r.PreviewWidth, r.PreviewHeight,
		r.TagsRaw, r.FirstSeenAt, r.LastPolledAt,
		r.BaselineScore, r.ScoreDelta, r.WindowHours, r.ScoreRate, r.HotScore,
	)
}

func rowToIndexedPostWindowedSortHot(r sqlcgen.ListPostsWindowedSortHotRow) IndexedPost {
	return newIndexedPost(
		r.ID, r.CreatedAt, r.ChangedAt, r.Score, r.Rating, r.CreatorID, r.ParentID,
		r.MD5, r.Source, r.Status, r.HasNotes, r.HasComments, r.HasChildren,
		r.FileURL, r.FileExt, r.Width, r.Height,
		r.SampleURL, r.SampleWidth, r.SampleHeight,
		r.PreviewURL, r.PreviewWidth, r.PreviewHeight,
		r.TagsRaw, r.FirstSeenAt, r.LastPolledAt,
		r.BaselineScore, r.ScoreDelta, r.WindowHours, r.ScoreRate, r.HotScore,
	)
}

func rowToIndexedPostWindowedSortTop(r sqlcgen.ListPostsWindowedSortTopRow) IndexedPost {
	return newIndexedPost(
		r.ID, r.CreatedAt, r.ChangedAt, r.Score, r.Rating, r.CreatorID, r.ParentID,
		r.MD5, r.Source, r.Status, r.HasNotes, r.HasComments, r.HasChildren,
		r.FileURL, r.FileExt, r.Width, r.Height,
		r.SampleURL, r.SampleWidth, r.SampleHeight,
		r.PreviewURL, r.PreviewWidth, r.PreviewHeight,
		r.TagsRaw, r.FirstSeenAt, r.LastPolledAt,
		r.BaselineScore, r.ScoreDelta, r.WindowHours, r.ScoreRate, r.HotScore,
	)
}

func rowToIndexedPostWindowedSortNew(r sqlcgen.ListPostsWindowedSortNewRow) IndexedPost {
	return newIndexedPost(
		r.ID, r.CreatedAt, r.ChangedAt, r.Score, r.Rating, r.CreatorID, r.ParentID,
		r.MD5, r.Source, r.Status, r.HasNotes, r.HasComments, r.HasChildren,
		r.FileURL, r.FileExt, r.Width, r.Height,
		r.SampleURL, r.SampleWidth, r.SampleHeight,
		r.PreviewURL, r.PreviewWidth, r.PreviewHeight,
		r.TagsRaw, r.FirstSeenAt, r.LastPolledAt,
		r.BaselineScore, r.ScoreDelta, r.WindowHours, r.ScoreRate, r.HotScore,
	)
}

func rowToIndexedPostWindowedSortScore(r sqlcgen.ListPostsWindowedSortScoreRow) IndexedPost {
	return newIndexedPost(
		r.ID, r.CreatedAt, r.ChangedAt, r.Score, r.Rating, r.CreatorID, r.ParentID,
		r.MD5, r.Source, r.Status, r.HasNotes, r.HasComments, r.HasChildren,
		r.FileURL, r.FileExt, r.Width, r.Height,
		r.SampleURL, r.SampleWidth, r.SampleHeight,
		r.PreviewURL, r.PreviewWidth, r.PreviewHeight,
		r.TagsRaw, r.FirstSeenAt, r.LastPolledAt,
		r.BaselineScore, r.ScoreDelta, r.WindowHours, r.ScoreRate, r.HotScore,
	)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanIndexedPost(scanner rowScanner) (IndexedPost, error) {
	var (
		p                                       IndexedPost
		id, parentID                            int
		createdAt, changedAt, firstSeen, polled int64
		rating                                  string
		hasNotes, hasComments, hasChildren      int
		tagsRaw                                 string
		windowHours, scoreRate, hotScore        float64
	)

	err := scanner.Scan(
		&id, &createdAt, &changedAt, &p.Score, &rating, &p.CreatorID, &parentID,
		&p.MD5, &p.Source, &p.Status, &hasNotes, &hasComments, &hasChildren,
		&p.FileURL, &p.FileExt, &p.Width, &p.Height,
		&p.SampleURL, &p.SampleWidth, &p.SampleHeight,
		&p.PreviewURL, &p.PreviewWidth, &p.PreviewHeight,
		&tagsRaw, &firstSeen, &polled,
		&p.BaselineScore, &p.ScoreDelta, &windowHours, &scoreRate, &hotScore,
	)
	if err != nil {
		return IndexedPost{}, err
	}

	p.ID = hypnohub.PostID(id)
	p.ParentID = hypnohub.PostID(parentID)
	p.CreatedAt = time.Unix(createdAt, 0).UTC()
	p.ChangedAt = time.Unix(changedAt, 0).UTC()
	p.Rating = hypnohub.Rating(rating).Normalize()
	p.HasNotes = hasNotes != 0
	p.HasComments = hasComments != 0
	p.HasChildren = hasChildren != 0
	p.Tags = strings.Fields(tagsRaw)
	p.FirstSeenAt = time.Unix(firstSeen, 0).UTC()
	p.LastPolledAt = time.Unix(polled, 0).UTC()
	p.WindowHours = math.Round(windowHours*100) / 100
	p.ScoreRate = math.Round(scoreRate*1000) / 1000
	p.HotScore = math.Round(hotScore*1000) / 1000

	return p, nil
}

func cleanTagList(tags []string) []string {
	var out []string
	for _, t := range tags {
		for _, field := range strings.Fields(strings.ReplaceAll(t, ",", " ")) {
			field = strings.ToLower(strings.TrimSpace(field))
			if field != "" && !slices.Contains(out, field) {
				out = append(out, field)
			}
		}
	}
	return out
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
