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

	"gaze.d14.pet"
	"gaze.d14.pet/internal/index/sqlcgen"
	"github.com/huandu/go-sqlbuilder"
	"libdb.so/lazymigrate"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

//go:embed schema.sql
var schemaSQL string

var errIntegrityCheckFailed = errors.New("sqlite integrity check failed")

// isCorruptError returns true only when the error represents a genuine unrecoverable
// corruption of the SQLite database disk image, not transient lock or context errors.
func isCorruptError(err error) bool {
	if err == nil {
		return false
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() & 0xff {
		case sqlite3.SQLITE_CORRUPT, sqlite3.SQLITE_NOTADB:
			return true
		}
	}
	return errors.Is(err, errIntegrityCheckFailed)
}

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
	switch w.Normalize() {
	case Window6Hours:
		return 6 * time.Hour, nil
	case WindowDay:
		return 24 * time.Hour, nil
	case Window3Days:
		return 3 * 24 * time.Hour, nil
	case WindowWeek:
		return 7 * 24 * time.Hour, nil
	case Window2Weeks:
		return 14 * 24 * time.Hour, nil
	case WindowMonth:
		return 30 * 24 * time.Hour, nil
	case WindowAllTime:
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
	// SortHot ranks posts by Superlinear Momentum: (score_delta / (eff_hours + tau)) * max(1, score)^0.35.
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
	db     *sql.DB
	q      *sqlcgen.Queries
	client *hypnohub.Client
}

// SetClient attaches an optional Hypnohub client for on-demand and background tag category resolution.
func (s *Store) SetClient(client *hypnohub.Client) {
	s.client = client
}

// Open opens or creates a SQLite index database at the given file path (or ":memory:").
// If an existing database file on disk is genuinely corrupted, it is backed up to .bak
// (preserving any prior .bak) and a clean new database is initialized in its place.
// Transient context cancellations or deadline errors are returned directly without touching disk.
func Open(ctx context.Context, path string) (*Store, error) {
	isMem := path == ":memory:" || strings.HasPrefix(path, "file::memory:") || strings.Contains(path, "mode=memory")
	store, err := openStore(ctx, path, isMem)
	if err == nil {
		return store, nil
	}
	if isMem || ctx.Err() != nil || !isCorruptError(err) {
		return nil, err
	}

	// Check if an existing file on disk failed to open/migrate/read due to genuine corruption
	if fi, statErr := os.Stat(path); statErr == nil && !fi.IsDir() {
		backupPath := path + ".bak"
		if _, err := os.Stat(backupPath); err == nil {
			oldBakPath := fmt.Sprintf("%s.%d.bak", path, time.Now().UnixNano())
			if err := os.Rename(backupPath, oldBakPath); err != nil {
				return nil, fmt.Errorf("preserving existing backup file: %w", err)
			}
		}

		slog.Warn("database file is corrupted; moving to .bak and recreating",
			"path", path,
			"backup", backupPath,
			"err", err)

		if err := os.Rename(path, backupPath); err != nil {
			return nil, fmt.Errorf("backing up corrupt database: %w", err)
		}
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
	pragmas = append(pragmas,
		"_pragma=busy_timeout(5000)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=foreign_keys(ON)",
		"_pragma=cache_size(-64000)",
		"_pragma=temp_store(MEMORY)",
		"_pragma=mmap_size(268435456)",
	)
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

	if err := lazymigrate.Migrate(ctx, db, schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrating sqlite schema: %w", err)
	}

	var checkResult string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check(1);").Scan(&checkResult); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("running quick_check: %w", err)
	}
	if checkResult != "ok" {
		_ = db.Close()
		return nil, fmt.Errorf("%w: %s", errIntegrityCheckFailed, checkResult)
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

// PruneOldSnapshots prunes score snapshots older than the specified cutoff timestamp.
func (s *Store) PruneOldSnapshots(ctx context.Context, before time.Time) error {
	return s.q.PruneOldSnapshots(ctx, before.UTC().Unix())
}

// IndexedPost represents a post stored in the local index along with its
// computed popularity metrics for a requested sliding window.
type IndexedPost struct {
	ID          hypnohub.PostID `json:"id"`
	CreatedAt   time.Time       `json:"created_at"`
	ChangedAt   time.Time       `json:"changed_at"`
	Score       int             `json:"score"`
	Rating      hypnohub.Rating `json:"rating"`
	CreatorID   int             `json:"creator_id"`
	ParentID    hypnohub.PostID `json:"parent_id"`
	MD5         string          `json:"md5"`
	Source      string          `json:"source"`
	Status      PostStatus      `json:"status"`
	HasNotes    bool            `json:"has_notes"`
	HasComments bool            `json:"has_comments"`
	HasChildren bool            `json:"has_children"`

	FileURL string `json:"file_url"`
	FileExt string `json:"file_ext"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`

	SampleURL    string `json:"sample_url"`
	SampleWidth  int    `json:"sample_width"`
	SampleHeight int    `json:"sample_height"`

	PreviewURL    string `json:"preview_url"`
	PreviewWidth  int    `json:"preview_width"`
	PreviewHeight int    `json:"preview_height"`

	Tags         []string           `json:"tags"`
	TagGroups    []TagCategoryGroup `json:"tag_groups,omitempty"`
	FirstSeenAt  time.Time          `json:"first_seen_at"`
	LastPolledAt time.Time          `json:"last_polled_at"`

	// Group / pool / sibling metrics:
	GroupID        hypnohub.PostID `json:"group_id"`
	GroupPageCount int             `json:"group_page_count"`
	GroupIsLatest  bool            `json:"group_is_latest"`

	// Popularity metrics for the queried window:
	BaselineScore int     `json:"baseline_score"`
	ScoreDelta    int     `json:"score_delta"`
	WindowHours   float64 `json:"window_hours"`
	ScoreRate     float64 `json:"score_rate"` // upvotes per hour (damped)
	HotScore      float64 `json:"hot_score"`
}

// PostTag represents a single tag on a post with its category and global active post count.
type PostTag struct {
	Name     string           `json:"name"`
	Category hypnohub.TagType `json:"category"`
	Count    int64            `json:"count"`
}

// TagCategoryGroup groups tags belonging to the same Hypnohub category in upstream display order.
type TagCategoryGroup struct {
	Category hypnohub.TagType `json:"category"`
	Label    string           `json:"label"`
	Slug     string           `json:"slug"`
	Tags     []PostTag        `json:"tags"`
}

var orderedTagCategories = []hypnohub.TagType{
	hypnohub.TagTypeCopyright,
	hypnohub.TagTypeCharacter,
	hypnohub.TagTypeArtist,
	hypnohub.TagTypeGeneral,
	hypnohub.TagTypeMeta,
}

// BuildTagGroups groups and sorts post tags into upstream Hypnohub category order
// (Copyright -> Character -> Artist -> General -> Meta), with tags sorted alphabetically
// and deduplicated within each category. Empty categories are omitted.
func BuildTagGroups(tags []PostTag) []TagCategoryGroup {
	if len(tags) == 0 {
		return nil
	}
	buckets := make(map[hypnohub.TagType][]PostTag, len(orderedTagCategories))
	for _, t := range tags {
		name := strings.ToLower(strings.TrimSpace(t.Name))
		if name == "" {
			continue
		}
		cat := t.Category.Normalize()
		buckets[cat] = append(buckets[cat], PostTag{
			Name:     name,
			Category: cat,
			Count:    t.Count,
		})
	}
	var groups []TagCategoryGroup
	for _, cat := range orderedTagCategories {
		b := buckets[cat]
		if len(b) == 0 {
			continue
		}
		slices.SortFunc(b, func(a, b PostTag) int {
			return strings.Compare(a.Name, b.Name)
		})
		b = slices.CompactFunc(b, func(a, b PostTag) bool {
			return a.Name == b.Name
		})
		groups = append(groups, TagCategoryGroup{
			Category: cat,
			Label:    cat.Label(),
			Slug:     cat.Slug(),
			Tags:     b,
		})
	}
	return groups
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
	Category  string `json:"category"`
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

type prevPostState struct {
	parentID hypnohub.PostID
	groupID  hypnohub.PostID
	status   PostStatus
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
	affectedGroupIDs := make(map[hypnohub.PostID]struct{}, 128)
	upserted := 0

	// Pre-query previous parent_id and group_id for all incoming posts so we detect
	// parent detachment or status changes and recompute both old and new groups.
	prevPostMap := make(map[hypnohub.PostID]prevPostState, len(posts))
	var batchIDs []int64
	for _, p := range posts {
		if p.ID.IsValid() {
			batchIDs = append(batchIDs, int64(p.ID))
		}
	}
	for i := 0; i < len(batchIDs); i += 500 {
		end := min(len(batchIDs), i+500)
		chunk := batchIDs[i:end]
		sb := sqlbuilder.SQLite.NewSelectBuilder()
		sb.Select("id", "parent_id", "group_id", "status").
			From("posts").
			Where(sb.In("id", sqlbuilder.Flatten(chunk)...))
		q, args := sb.Build()
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return 0, fmt.Errorf("querying previous post state: %w", err)
		}
		for rows.Next() {
			var id, pid, gid int64
			var st string
			if err := rows.Scan(&id, &pid, &gid, &st); err != nil {
				rows.Close()
				return 0, fmt.Errorf("scanning previous post state: %w", err)
			}
			prevPostMap[hypnohub.PostID(id)] = prevPostState{
				parentID: hypnohub.PostID(pid),
				groupID:  hypnohub.PostID(gid),
				status:   PostStatus(st),
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return 0, fmt.Errorf("iterating previous post state: %w", err)
		}
		rows.Close()
	}

	for _, p := range posts {
		if !p.ID.IsValid() {
			continue
		}

		status := PostStatus(p.Status)
		if status == "" {
			status = StatusActive
		}

		prev, existed := prevPostMap[p.ID]
		if !existed || status != prev.status || p.ParentID > 0 || p.HasChildren || prev.parentID > 0 || prev.groupID > 0 {
			affectedGroupIDs[p.ID] = struct{}{}
			if p.ParentID > 0 {
				affectedGroupIDs[p.ParentID] = struct{}{}
			}
			if prev.parentID > 0 {
				affectedGroupIDs[prev.parentID] = struct{}{}
			}
			if prev.groupID > 0 {
				affectedGroupIDs[prev.groupID] = struct{}{}
			}
		}

		tagsSlice := p.Tags.Split()
		tagsRaw := strings.Join(tagsSlice, " ")
		rating := string(p.Rating.Normalize())

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
			Status:        string(status),
			HasNotes:      boolToInt(p.HasNotes == "true"),
			HasComments:   boolToInt(p.HasComments == "true"),
			HasChildren:   boolToInt(p.HasChildren),
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

	if len(affectedGroupIDs) > 0 {
		seedIDs := make([]hypnohub.PostID, 0, len(affectedGroupIDs))
		for id := range affectedGroupIDs {
			seedIDs = append(seedIDs, id)
		}
		if err := recomputePostGroups(ctx, tx, seedIDs); err != nil {
			return 0, fmt.Errorf("recomputing post groups: %w", err)
		}
	}

	err = qtx.InsertPollRun(ctx, sqlcgen.InsertPollRunParams{
		PollTime:      pollUnix,
		StartedAt:     startedAt.UTC().Unix(),
		CompletedAt:   completedAt.UTC().Unix(),
		PostsUpserted: int64(upserted),
		PagesFetched:  int64(pagesFetched),
		IsSeed:        boolToInt(isSeed),
	})
	if err != nil {
		return 0, fmt.Errorf("recording poll run: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing batch: %w", err)
	}

	return upserted, nil
}

func recomputePostGroups(ctx context.Context, tx *sql.Tx, seedIDs []hypnohub.PostID) error {
	if len(seedIDs) == 0 {
		return nil
	}

	frontier := make([]hypnohub.PostID, 0, len(seedIDs))
	seen := make(map[hypnohub.PostID]bool, len(seedIDs)*2)
	for _, id := range seedIDs {
		if id > 0 && !seen[id] {
			seen[id] = true
			frontier = append(frontier, id)
		}
	}

	type postNode struct {
		id       hypnohub.PostID
		parentID hypnohub.PostID
		groupID  hypnohub.PostID
		status   PostStatus
	}

	allNodes := make(map[hypnohub.PostID]postNode)

	for len(frontier) > 0 {
		batch := frontier
		frontier = nil

		for i := 0; i < len(batch); i += 500 {
			end := min(len(batch), i+500)
			chunk := batch[i:end]

			sb := sqlbuilder.SQLite.NewSelectBuilder()
			sb.Select("id", "parent_id", "group_id", "status").
				From("posts").
				Where(
					sb.Or(
						sb.In("id", sqlbuilder.Flatten(chunk)...),
						sb.In("parent_id", sqlbuilder.Flatten(chunk)...),
						sb.And(
							sb.GreaterThan("group_id", 0),
							sb.In("group_id", sqlbuilder.Flatten(chunk)...),
						),
					),
				)

			query, args := sb.Build()
			rows, err := tx.QueryContext(ctx, query, args...)
			if err != nil {
				return fmt.Errorf("querying posts for group recompute: %w", err)
			}

			for rows.Next() {
				var id, pid, gid int64
				var status string
				if err := rows.Scan(&id, &pid, &gid, &status); err != nil {
					rows.Close()
					return fmt.Errorf("scanning post for group recompute: %w", err)
				}
				n := postNode{
					id:       hypnohub.PostID(id),
					parentID: hypnohub.PostID(pid),
					groupID:  hypnohub.PostID(gid),
					status:   PostStatus(status),
				}
				allNodes[n.id] = n

				if !seen[n.id] {
					seen[n.id] = true
					frontier = append(frontier, n.id)
				}
				if n.parentID > 0 && n.parentID != n.id && !seen[n.parentID] {
					seen[n.parentID] = true
					frontier = append(frontier, n.parentID)
				}
				if n.groupID > 0 && !seen[n.groupID] {
					seen[n.groupID] = true
					frontier = append(frontier, n.groupID)
				}
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}
	}

	// Build undirected adjacency graph. Active posts link to parent_id if parent is active or unindexed.
	adj := make(map[hypnohub.PostID][]hypnohub.PostID, len(allNodes))
	for id, n := range allNodes {
		if n.status == StatusActive && n.parentID > 0 && n.parentID != id {
			parent, inNodes := allNodes[n.parentID]
			if !inNodes || parent.status == StatusActive {
				adj[id] = append(adj[id], n.parentID)
				adj[n.parentID] = append(adj[n.parentID], id)
			}
		}
	}

	qtx := sqlcgen.New(tx)

	// Reset any non-active posts in allNodes to group_id = 0, group_page_count = 1, group_is_latest = 0
	for id, n := range allNodes {
		if n.status != StatusActive {
			if err := qtx.UpdatePostGroup(ctx, sqlcgen.UpdatePostGroupParams{
				GroupID:        0,
				GroupPageCount: 1,
				GroupIsLatest:  0,
				ID:             int64(id),
			}); err != nil {
				return fmt.Errorf("resetting inactive post group %d: %w", id, err)
			}
		}
	}

	// Find connected components among active posts using BFS
	visitedComp := make(map[hypnohub.PostID]bool, len(allNodes))

	for id, n := range allNodes {
		if n.status != StatusActive || visitedComp[id] {
			continue
		}

		var comp []hypnohub.PostID
		queue := []hypnohub.PostID{id}
		visitedComp[id] = true

		for len(queue) > 0 {
			curr := queue[0]
			queue = queue[1:]
			comp = append(comp, curr)

			for _, neighbor := range adj[curr] {
				if !visitedComp[neighbor] {
					visitedComp[neighbor] = true
					queue = append(queue, neighbor)
				}
			}
		}

		// Filter connected component to active posts that exist in allNodes
		var activeComp []hypnohub.PostID
		for _, pid := range comp {
			if node, ok := allNodes[pid]; ok && node.status == StatusActive {
				activeComp = append(activeComp, pid)
			}
		}
		slices.Sort(activeComp)

		if len(activeComp) <= 1 {
			for _, pid := range activeComp {
				if err := qtx.UpdatePostGroup(ctx, sqlcgen.UpdatePostGroupParams{
					GroupID:        0,
					GroupPageCount: 1,
					GroupIsLatest:  1,
					ID:             int64(pid),
				}); err != nil {
					return fmt.Errorf("resetting single post group %d: %w", pid, err)
				}
			}
			continue
		}

		groupID := activeComp[0]
		pageCount := int64(len(activeComp))
		latestID := activeComp[len(activeComp)-1]

		for _, pid := range activeComp {
			isLatest := int64(0)
			if pid == latestID {
				isLatest = 1
			}
			if err := qtx.UpdatePostGroup(ctx, sqlcgen.UpdatePostGroupParams{
				GroupID:        int64(groupID),
				GroupPageCount: pageCount,
				GroupIsLatest:  isLatest,
				ID:             int64(pid),
			}); err != nil {
				return fmt.Errorf("updating post group %d: %w", pid, err)
			}
		}
	}

	return nil
}

// MediaType filters posts by media category.
type MediaType string

const (
	MediaAll      MediaType = ""
	MediaImage    MediaType = "image"
	MediaAnimated MediaType = "animated"
	MediaVideo    MediaType = "video"
)

// NormalizeMediaType maps user strings to canonical MediaType values.
func NormalizeMediaType(m string) MediaType {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "video":
		return MediaVideo
	case "animated":
		return MediaAnimated
	case "image", "static":
		return MediaImage
	default:
		return MediaAll
	}
}

// PostStatus represents the moderation/lifecycle status of a post.
type PostStatus string

const StatusActive PostStatus = "active"

// QueryOptions configures a query against the local post index.
type QueryOptions struct {
	Window         Window
	Sort           SortMode
	Rating         hypnohub.Rating // optional: "s", "q", "e", or "" for all
	IncludeTags    []string
	ExcludeTags    []string
	MediaType      MediaType // optional: "image", "video", "animated", or ""
	CollapseGroups bool      // collapse multi-page sibling/pool groups into the latest post
	Limit          int
	Offset         int
	Now            time.Time // defaults to latest poll time (or time.Now() if empty)
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

const hotScoreMomentumExp = 0.35

var basePostColumns = []string{
	"p.id", "p.created_at", "p.changed_at", "p.score", "p.rating", "p.creator_id", "p.parent_id",
	"p.md5", "p.source", "p.status", "p.has_notes", "p.has_comments", "p.has_children",
	"p.file_url", "p.file_ext", "p.width", "p.height",
	"p.sample_url", "p.sample_width", "p.sample_height",
	"p.preview_url", "p.preview_width", "p.preview_height",
	"p.tags_raw", "p.first_seen_at", "p.last_polled_at",
	"p.group_id", "p.group_is_latest", "p.group_page_count",
}

func selectPostColumns(sb *sqlbuilder.SelectBuilder, windowStart, now time.Time, tau float64) []string {
	cols := make([]string, len(basePostColumns), len(basePostColumns)+5)
	copy(cols, basePostColumns)

	nowVar := sb.Var(now.Unix())
	tauVar := sb.Var(tau)

	if windowStart.IsZero() {
		effHours := fmt.Sprintf("MAX(0.1, (%s - p.created_at) / 3600.0)", nowVar)
		scoreRate := fmt.Sprintf("CAST(p.score AS REAL) / (%s + %s)", effHours, tauVar)
		hotScore := fmt.Sprintf("(%s) * POWER(MAX(1, p.score), %f)", scoreRate, hotScoreMomentumExp)

		cols = append(cols,
			"0 AS baseline_score",
			"p.score AS score_delta",
			effHours+" AS window_hours",
			scoreRate+" AS score_rate",
			hotScore+" AS hot_score",
		)
		return cols
	}

	startVar := sb.Var(windowStart.Unix())
	bExpr := fmt.Sprintf("CASE WHEN p.created_at >= %s THEN 0 ELSE COALESCE(b.start_score, p.score) END", startVar)
	dExpr := fmt.Sprintf("CASE WHEN p.created_at >= %s THEN p.score ELSE MAX(0, p.score - COALESCE(b.start_score, p.score)) END", startVar)
	effHours := fmt.Sprintf("MAX(0.1, (%s - CASE WHEN p.created_at >= %s THEN p.created_at ELSE COALESCE(b.start_time, %s) END) / 3600.0)", nowVar, startVar, startVar)
	scoreRate := fmt.Sprintf("CAST(%s AS REAL) / (%s + %s)", dExpr, effHours, tauVar)
	hotScore := fmt.Sprintf("(%s) * POWER(MAX(1, p.score), %f)", scoreRate, hotScoreMomentumExp)

	cols = append(cols,
		bExpr+" AS baseline_score",
		dExpr+" AS score_delta",
		effHours+" AS window_hours",
		scoreRate+" AS score_rate",
		hotScore+" AS hot_score",
	)
	return cols
}

func newBaselineCTE(windowStart, now time.Time) *sqlbuilder.CTEQueryBuilder {
	baseSB := sqlbuilder.SQLite.NewSelectBuilder()
	baseSB.Select("post_id", "score AS start_score", "MIN(poll_time) AS start_time").
		From("score_snapshots").
		Where(
			baseSB.GreaterEqualThan("poll_time", windowStart.Unix()),
			baseSB.LessThan("poll_time", now.Unix()),
		).
		GroupBy("post_id")
	return sqlbuilder.SQLite.NewCTEQueryBuilder().Table("baseline").As(baseSB)
}

type postFilter struct {
	rating         hypnohub.Rating
	media          MediaType
	include        []string
	exclude        []string
	windowStart    time.Time
	collapseGroups bool
}

func (f postFilter) hasAny() bool {
	return f.rating != "" || f.media != "" || len(f.include) > 0 || len(f.exclude) > 0 || !f.windowStart.IsZero()
}

func (f postFilter) applyBase(sb *sqlbuilder.SelectBuilder, postAlias, baselineAlias string) {
	sb.Where(sb.Equal(postAlias+".status", string(StatusActive)))
	if !f.windowStart.IsZero() {
		sb.Where(sb.Or(
			sb.GreaterEqualThan(postAlias+".created_at", f.windowStart.Unix()),
			sb.And(
				sb.IsNotNull(baselineAlias+".start_score"),
				fmt.Sprintf("%s.score > %s.start_score", postAlias, baselineAlias),
			),
		))
	}
	if f.rating != "" {
		sb.Where(sb.Equal(postAlias+".rating", string(f.rating)))
	}
	switch f.media {
	case MediaVideo:
		sb.Where(sb.In(postAlias+".file_ext", "mp4", "webm"))
	case MediaAnimated:
		sb.Where(sb.In(postAlias+".file_ext", "mp4", "webm", "gif"))
	case MediaImage:
		sb.Where(sb.NotIn(postAlias+".file_ext", "mp4", "webm", "gif"))
	}
	for _, tag := range f.include {
		sub := sqlbuilder.SQLite.NewSelectBuilder()
		sub.Select("pt.post_id").
			From("post_tags pt").
			Join("tags t", "t.id = pt.tag_id").
			Where(sub.Equal("t.name", tag))
		sb.Where(sb.In(postAlias+".id", sub))
	}
	for _, tag := range f.exclude {
		sub := sqlbuilder.SQLite.NewSelectBuilder()
		sub.Select("pt.post_id").
			From("post_tags pt").
			Join("tags t", "t.id = pt.tag_id").
			Where(sub.Equal("t.name", tag))
		sb.Where(sb.NotIn(postAlias+".id", sub))
	}
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

	var windowStart time.Time
	if winDur > 0 {
		windowStart = refNow.Add(-winDur)
	}

	tau := window.DampingHours()

	var rating hypnohub.Rating
	if opts.Rating != "" {
		r := opts.Rating.Normalize()
		if r == hypnohub.RatingSafe || r == hypnohub.RatingQuestionable || r == hypnohub.RatingExplicit {
			rating = r
		}
	}

	filter := postFilter{
		rating:         rating,
		media:          NormalizeMediaType(string(opts.MediaType)),
		include:        cleanTagList(opts.IncludeTags),
		exclude:        cleanTagList(opts.ExcludeTags),
		windowStart:    windowStart,
		collapseGroups: opts.CollapseGroups,
	}

	// 1. Compute total count
	countSB := sqlbuilder.SQLite.NewSelectBuilder()
	if !filter.windowStart.IsZero() {
		countSB.With(sqlbuilder.SQLite.NewCTEBuilder().With(newBaselineCTE(filter.windowStart, refNow)))
	}
	if filter.collapseGroups && filter.hasAny() {
		countSB.Select("COUNT(DISTINCT CASE WHEN p.group_id > 0 THEN p.group_id ELSE p.id END)").From("posts p")
	} else {
		countSB.Select("COUNT(*)").From("posts p")
	}
	if !filter.windowStart.IsZero() {
		countSB.JoinWithOption(sqlbuilder.LeftJoin, "baseline b", "b.post_id = p.id")
	}
	if filter.collapseGroups && !filter.hasAny() {
		countSB.Where(countSB.Equal("p.group_is_latest", 1))
	}
	filter.applyBase(countSB, "p", "b")

	countSQL, countArgs := countSB.Build()
	var total int
	if err := s.db.QueryRowContext(ctx, countSQL, countArgs...).Scan(&total); err != nil {
		return nil, fmt.Errorf("counting posts: %w", err)
	}

	// 2. Select page of posts
	querySB := sqlbuilder.SQLite.NewSelectBuilder()
	if !filter.windowStart.IsZero() {
		querySB.With(sqlbuilder.SQLite.NewCTEBuilder().With(newBaselineCTE(filter.windowStart, refNow)))
	}
	querySB.Select(selectPostColumns(querySB, filter.windowStart, refNow, tau)...).From("posts p")
	if !filter.windowStart.IsZero() {
		querySB.JoinWithOption(sqlbuilder.LeftJoin, "baseline b", "b.post_id = p.id")
	}

	if filter.collapseGroups {
		if !filter.hasAny() {
			querySB.Where(querySB.Equal("p.group_is_latest", 1))
		} else {
			sub := sqlbuilder.SQLite.NewSelectBuilder()
			sub.Select("1").From("posts sub_p")
			if !filter.windowStart.IsZero() {
				sub.JoinWithOption(sqlbuilder.LeftJoin, "baseline b_sub", "b_sub.post_id = sub_p.id")
			}
			sub.Where(
				sub.GreaterThan("sub_p.group_id", 0),
				"sub_p.group_id = p.group_id",
				"sub_p.id > p.id",
			)
			filter.applyBase(sub, "sub_p", "b_sub")
			querySB.Where(querySB.Or(
				querySB.Equal("p.group_is_latest", 1),
				querySB.And(
					querySB.GreaterThan("p.group_id", 0),
					querySB.NotExists(sub),
				),
			))
		}
	}
	filter.applyBase(querySB, "p", "b")

	var orderCols []string
	switch sortMode {
	case SortHot:
		orderCols = []string{"hot_score DESC", "score_rate DESC", "score_delta DESC", "p.score DESC", "p.id DESC"}
	case SortTop:
		orderCols = []string{"score_delta DESC", "hot_score DESC", "score_rate DESC", "p.score DESC", "p.id DESC"}
	case SortNew:
		orderCols = []string{"p.created_at DESC", "p.id DESC"}
	case SortScore:
		orderCols = []string{"p.score DESC", "p.id DESC"}
	}
	querySB.OrderBy(orderCols...).Limit(limit).Offset(offset)

	querySQL, queryArgs := querySB.Build()
	rows, err := s.db.QueryContext(ctx, querySQL, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("querying posts: %w", err)
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

	winStart := windowStart
	if winDur == 0 && len(posts) > 0 {
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

// GetPost retrieves a single indexed post by ID along with its score history snapshots.
func (s *Store) GetPost(ctx context.Context, id hypnohub.PostID) (*IndexedPost, []ScorePoint, error) {
	now := s.latestReferenceTime(ctx).UTC()
	tau := WindowDay.DampingHours()

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

	post := postToIndexed(postRow, now, tau)

	tagRows, err := s.q.GetPostTagsWithCategory(ctx, int64(id))
	if err != nil {
		return nil, nil, fmt.Errorf("getting tags for post %d: %w", id, err)
	}

	if s.client != nil {
		hasUnknown := false
		for _, r := range tagRows {
			if r.Category == int64(hypnohub.TagCategoryUnknown) {
				hasUnknown = true
				break
			}
		}
		if hasUnknown {
			rCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
			scraped, scrapeErr := s.client.PostViewTags(rCtx, id)
			if scrapeErr == nil && len(scraped) > 0 {
				if tx, txErr := s.db.BeginTx(ctx, nil); txErr == nil {
					qtx := s.q.WithTx(tx)
					for i := range tagRows {
						cat := hypnohub.TagTypeGeneral
						if c, ok := scraped[tagRows[i].Name]; ok {
							cat = c.Normalize()
						}
						tagRows[i].Category = int64(cat)
						_ = qtx.UpdateTagCategoryByName(ctx, sqlcgen.UpdateTagCategoryByNameParams{
							Category: int64(cat),
							Name:     tagRows[i].Name,
						})
					}
					_ = tx.Commit()
				} else {
					for i := range tagRows {
						cat := hypnohub.TagTypeGeneral
						if c, ok := scraped[tagRows[i].Name]; ok {
							cat = c.Normalize()
						}
						tagRows[i].Category = int64(cat)
					}
				}
			} else if scrapeErr == nil && len(scraped) == 0 {
				resolved := make(map[string]hypnohub.TagType, 5)
				count := 0
				for _, r := range tagRows {
					if r.Category == int64(hypnohub.TagCategoryUnknown) {
						if count >= 5 {
							break
						}
						count++
						tagInfo, tErr := s.client.TagByName(rCtx, r.Name)
						if tErr != nil {
							break
						}
						if tagInfo != nil {
							resolved[r.Name] = tagInfo.Type.Normalize()
						} else {
							resolved[r.Name] = hypnohub.TagTypeGeneral
						}
					}
				}
				if len(resolved) > 0 {
					if tx, txErr := s.db.BeginTx(ctx, nil); txErr == nil {
						qtx := s.q.WithTx(tx)
						for i := range tagRows {
							if cat, ok := resolved[tagRows[i].Name]; ok {
								tagRows[i].Category = int64(cat)
								_ = qtx.UpdateTagCategoryByName(ctx, sqlcgen.UpdateTagCategoryByNameParams{
									Category: int64(cat),
									Name:     tagRows[i].Name,
								})
							}
						}
						_ = tx.Commit()
					}
				}
			}
			cancel()
		}
	}

	if len(tagRows) > 0 {
		postTags := make([]PostTag, len(tagRows))
		for i, r := range tagRows {
			postTags[i] = PostTag{
				Name:     r.Name,
				Category: hypnohub.TagType(r.Category).Normalize(),
				Count:    r.PostCount,
			}
		}
		post.TagGroups = BuildTagGroups(postTags)
	} else if len(post.Tags) > 0 {
		postTags := make([]PostTag, len(post.Tags))
		for i, name := range post.Tags {
			postTags[i] = PostTag{
				Name:     name,
				Category: hypnohub.TagTypeGeneral,
			}
		}
		post.TagGroups = BuildTagGroups(postTags)
	}

	// Compute HotScore using the 24h window (WindowDay, matching /popular)
	windowStart := now.Add(-24 * time.Hour)
	windowStartUnix := windowStart.Unix()
	nowUnix := now.Unix()
	if postRow.CreatedAt >= windowStartUnix {
		// Created within the 24h window: full score is delta
		effHours := max(0.1, float64(nowUnix-postRow.CreatedAt)/3600.0)
		scoreRate, hotScore := hotMetrics(postRow.Score, postRow.Score, effHours, tau)
		post.BaselineScore = 0
		post.ScoreDelta = int(postRow.Score)
		post.WindowHours = math.Round(effHours*100) / 100
		post.ScoreRate = math.Round(scoreRate*1000) / 1000
		post.HotScore = math.Round(hotScore*1000) / 1000
	} else {
		// Created before 24h window: look up baseline snapshot in window
		idx := slices.IndexFunc(snapRows, func(snap sqlcgen.GetSnapshotsByPostIDRow) bool {
			return snap.PollTime >= windowStartUnix && snap.PollTime < nowUnix
		})
		if idx >= 0 {
			startScore := snapRows[idx].Score
			startTime := snapRows[idx].PollTime
			delta := max(0, postRow.Score-startScore)
			effHours := max(0.1, float64(nowUnix-startTime)/3600.0)
			scoreRate, hotScore := hotMetrics(delta, postRow.Score, effHours, tau)
			post.BaselineScore = int(startScore)
			post.ScoreDelta = int(delta)
			post.WindowHours = math.Round(effHours*100) / 100
			post.ScoreRate = math.Round(scoreRate*1000) / 1000
			post.HotScore = math.Round(hotScore*1000) / 1000
		} else {
			// No snapshot within 24h window
			post.BaselineScore = int(postRow.Score)
			post.ScoreDelta = 0
			post.WindowHours = 24.0
			post.ScoreRate = 0
			post.HotScore = 0
		}
	}

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

// GetGroupPosts retrieves all active posts belonging to groupID ordered by ID ASC (page order 1..N).
func (s *Store) GetGroupPosts(ctx context.Context, groupID hypnohub.PostID) ([]IndexedPost, error) {
	if groupID <= 0 {
		return nil, nil
	}
	now := s.latestReferenceTime(ctx).UTC()
	tau := WindowDay.DampingHours()

	posts, err := s.q.ListGroupPosts(ctx, int64(groupID))
	if err != nil {
		return nil, fmt.Errorf("listing group posts %d: %w", groupID, err)
	}

	result := make([]IndexedPost, len(posts))
	for i, p := range posts {
		result[i] = postToIndexed(p, now, tau)
	}
	return result, nil
}

// TopTags returns the top local indexed tags ordered by post count descending.
func (s *Store) TopTags(ctx context.Context, limit int) ([]TagSummary, error) {
	return s.SearchTags(ctx, "", limit)
}

// ResolveUnknownTagCategories resolves up to limit tags whose category is still TagCategoryUnknown (-1)
// using the configured Hypnohub client (or the provided tag lookup function) and commits the updates in a single transaction.
func (s *Store) ResolveUnknownTagCategories(ctx context.Context, limit int) (int, error) {
	if s.client == nil {
		return 0, nil
	}
	if limit <= 0 {
		limit = 20
	}
	names, err := s.q.ListUnknownCategoryTags(ctx, int64(limit))
	if err != nil {
		return 0, fmt.Errorf("listing unknown category tags: %w", err)
	}
	if len(names) == 0 {
		return 0, nil
	}

	resolved := make(map[string]hypnohub.TagType, len(names))
	for _, name := range names {
		if ctx.Err() != nil {
			break
		}
		tagInfo, err := s.client.TagByName(ctx, name)
		if err != nil {
			break
		}
		if tagInfo != nil {
			resolved[name] = tagInfo.Type.Normalize()
		} else {
			resolved[name] = hypnohub.TagTypeGeneral
		}
	}
	if len(resolved) == 0 {
		return 0, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tag category tx: %w", err)
	}
	defer tx.Rollback()

	qtx := s.q.WithTx(tx)
	for name, cat := range resolved {
		if err := qtx.UpdateTagCategoryByName(ctx, sqlcgen.UpdateTagCategoryByNameParams{
			Category: int64(cat),
			Name:     name,
		}); err != nil {
			return 0, fmt.Errorf("updating tag category for %q: %w", name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing tag category tx: %w", err)
	}
	return len(resolved), nil
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
			tags[i] = TagSummary{
				Name:      r.Name,
				PostCount: int(r.PostCount),
				Category:  hypnohub.TagType(r.Category).Slug(),
			}
		}
		return tags, nil
	}

	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	rows, err := s.q.SearchTagsByPrefix(ctx, sqlcgen.SearchTagsByPrefixParams{
		Pattern: "%" + escaped + "%",
		Prefix:  escaped + "%",
		Limit:   int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("searching tags: %w", err)
	}
	tags := make([]TagSummary, len(rows))
	for i, r := range rows {
		tags[i] = TagSummary{
			Name:      r.Name,
			PostCount: int(r.PostCount),
			Category:  hypnohub.TagType(r.Category).Slug(),
		}
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

func hotMetrics(delta, score int64, effHours, tau float64) (rate, hot float64) {
	rate = float64(delta) / (effHours + tau)
	hot = rate * math.Pow(float64(max(1, score)), hotScoreMomentumExp)
	return rate, hot
}

func postToIndexed(p sqlcgen.Post, now time.Time, tau float64) IndexedPost {
	effHours := max(0.1, float64(now.Unix()-p.CreatedAt)/3600.0)
	scoreRate, hotScore := hotMetrics(p.Score, p.Score, effHours, tau)

	return IndexedPost{
		ID:             hypnohub.PostID(p.ID),
		CreatedAt:      time.Unix(p.CreatedAt, 0).UTC(),
		ChangedAt:      time.Unix(p.ChangedAt, 0).UTC(),
		Score:          int(p.Score),
		Rating:         hypnohub.Rating(p.Rating).Normalize(),
		CreatorID:      int(p.CreatorID),
		ParentID:       hypnohub.PostID(p.ParentID),
		MD5:            p.MD5,
		Source:         p.Source,
		Status:         PostStatus(p.Status),
		HasNotes:       p.HasNotes != 0,
		HasComments:    p.HasComments != 0,
		HasChildren:    p.HasChildren != 0,
		FileURL:        p.FileURL,
		FileExt:        p.FileExt,
		Width:          int(p.Width),
		Height:         int(p.Height),
		SampleURL:      p.SampleURL,
		SampleWidth:    int(p.SampleWidth),
		SampleHeight:   int(p.SampleHeight),
		PreviewURL:     p.PreviewURL,
		PreviewWidth:   int(p.PreviewWidth),
		PreviewHeight:  int(p.PreviewHeight),
		Tags:           strings.Fields(p.TagsRaw),
		FirstSeenAt:    time.Unix(p.FirstSeenAt, 0).UTC(),
		LastPolledAt:   time.Unix(p.LastPolledAt, 0).UTC(),
		GroupID:        hypnohub.PostID(p.GroupID),
		GroupPageCount: int(p.GroupPageCount),
		GroupIsLatest:  p.GroupIsLatest != 0,
		BaselineScore:  0,
		ScoreDelta:     int(p.Score),
		WindowHours:    math.Round(effHours*100) / 100,
		ScoreRate:      math.Round(scoreRate*1000) / 1000,
		HotScore:       math.Round(hotScore*1000) / 1000,
	}
}

func scanIndexedPost(rows *sql.Rows) (IndexedPost, error) {
	var (
		p                                       IndexedPost
		id, parentID                            int
		createdAt, changedAt, firstSeen, polled int64
		rating                                  string
		status                                  string
		hasNotes, hasComments, hasChildren      int
		tagsRaw                                 string
		groupID, groupIsLatest, groupPageCount  int64
		windowHours, scoreRate, hotScore        float64
	)

	err := rows.Scan(
		&id, &createdAt, &changedAt, &p.Score, &rating, &p.CreatorID, &parentID,
		&p.MD5, &p.Source, &status, &hasNotes, &hasComments, &hasChildren,
		&p.FileURL, &p.FileExt, &p.Width, &p.Height,
		&p.SampleURL, &p.SampleWidth, &p.SampleHeight,
		&p.PreviewURL, &p.PreviewWidth, &p.PreviewHeight,
		&tagsRaw, &firstSeen, &polled,
		&groupID, &groupIsLatest, &groupPageCount,
		&p.BaselineScore, &p.ScoreDelta, &windowHours, &scoreRate, &hotScore,
	)
	if err != nil {
		return IndexedPost{}, err
	}

	p.ID = hypnohub.PostID(id)
	p.ParentID = hypnohub.PostID(parentID)
	p.Status = PostStatus(status)
	p.GroupID = hypnohub.PostID(groupID)
	p.GroupIsLatest = groupIsLatest != 0
	p.GroupPageCount = int(groupPageCount)
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

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
