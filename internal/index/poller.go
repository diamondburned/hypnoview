package index

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gaze.d14.pet"
	"gaze.d14.pet/query"
)

const (
	// DefaultSeedPosts is the number of most recent posts to fetch when seeding a new database.
	DefaultSeedPosts = 10000
	// DefaultPollInterval is the interval between background index polls.
	DefaultPollInterval = 2 * time.Hour
	// DefaultPollHorizon is the rolling time window swept on each periodic poll.
	DefaultPollHorizon = 30 * 24 * time.Hour
	// DefaultMaxPollPages caps the number of 1000-post pages fetched during a regular poll.
	DefaultMaxPollPages = 6
	// DefaultRetention is the snapshot retention window; 0 preserves all snapshots indefinitely.
	DefaultRetention time.Duration = 0
	// maxFetchRetries is the number of retries per page fetch on transient upstream errors.
	maxFetchRetries = 3
)

// PostsFetcher is the upstream interface used by the background Poller.
// It is satisfied by *hypnohub.Client.
type PostsFetcher interface {
	SearchPostsWithOptions(ctx context.Context, opts hypnohub.SearchPostsOptions) (*hypnohub.SearchPostsResult, error)
}

var _ PostsFetcher = (*hypnohub.Client)(nil)

// PollerConfig configures the background indexer.
type PollerConfig struct {
	SeedPosts    int
	PollInterval time.Duration
	PollHorizon  time.Duration
	MaxPollPages int
	PageDelay    time.Duration
	Logger       *slog.Logger
}

// Poller periodically sweeps Hypnohub for recent posts and records score snapshots in SQLite.
type Poller struct {
	store   *Store
	fetcher PostsFetcher
	cfg     PollerConfig
}

// NewPoller creates a new background Poller.
func NewPoller(store *Store, fetcher PostsFetcher, cfg PollerConfig) *Poller {
	if cfg.SeedPosts <= 0 {
		cfg.SeedPosts = DefaultSeedPosts
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	if cfg.PollHorizon <= 0 {
		cfg.PollHorizon = DefaultPollHorizon
	}
	if cfg.MaxPollPages <= 0 {
		cfg.MaxPollPages = DefaultMaxPollPages
	}
	if cfg.PageDelay <= 0 {
		cfg.PageDelay = 250 * time.Millisecond
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Poller{
		store:   store,
		fetcher: fetcher,
		cfg:     cfg,
	}
}

// Run starts the background polling loop. It performs an initial PollOnce immediately
// to ingest the freshest posts right away, followed by seeding if total posts in DB are below
// SeedPosts (minPosts), and continues periodic polling on PollInterval.
func (p *Poller) Run(ctx context.Context) error {
	// 1. Run an immediate poll so the freshest ~1,000 posts are available right away.
	if _, err := p.PollOnce(ctx); err != nil {
		p.cfg.Logger.ErrorContext(ctx, "startup poll failed", "error", err)
	}

	// 2. Check if the database has fewer than target SeedPosts and auto-seed if needed.
	stats, err := p.store.Stats(ctx)
	if err != nil {
		return fmt.Errorf("checking index stats: %w", err)
	}

	if stats.TotalPosts < p.cfg.SeedPosts {
		p.cfg.Logger.InfoContext(ctx, "seeding index database up to minimum posts threshold",
			"target_posts", p.cfg.SeedPosts,
			"existing_posts", stats.TotalPosts)
		if _, err := p.Seed(ctx, p.cfg.SeedPosts); err != nil {
			p.cfg.Logger.ErrorContext(ctx, "background seed failed", "error", err)
		}
	}

	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := p.PollOnce(ctx); err != nil {
				p.cfg.Logger.ErrorContext(ctx, "periodic poll failed", "error", err)
			}
		}
	}
}

// fetchPostsWithRetry executes a page fetch with exponential backoff on transient upstream errors.
func (p *Poller) fetchPostsWithRetry(ctx context.Context, opts hypnohub.SearchPostsOptions) (*hypnohub.SearchPostsResult, error) {
	var (
		res *hypnohub.SearchPostsResult
		err error
	)
	backoff := 1 * time.Second
	for attempt := 0; attempt <= maxFetchRetries; attempt++ {
		if attempt > 0 {
			p.cfg.Logger.WarnContext(ctx, "retrying fetch after transient upstream error",
				"attempt", attempt,
				"max_retries", maxFetchRetries,
				"backoff", backoff,
				"query", opts.Query,
				"pid", opts.PageOffset,
				"error", err)

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > 10*time.Second {
				backoff = 10 * time.Second
			}
		}

		res, err = p.fetcher.SearchPostsWithOptions(ctx, opts)
		if err == nil {
			return res, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("upstream request failed after %d retries: %w", maxFetchRetries, err)
}

// Seed fetches up to targetPosts posts (committing in batches of 1000 per page) and populates
// both the posts metadata table and the initial score_snapshots baseline.
//
// Seeding commits each page incrementally so progress is immediately visible in SQLite and
// persists across interruptions. When the database already contains posts, it automatically
// continues backwards using cursor pagination (id:<minID) starting from the existing MIN(id),
// avoiding re-fetching existing posts and bypassing upstream pid offset limits.
func (p *Poller) Seed(ctx context.Context, targetPosts int) (int, error) {
	if targetPosts <= 0 {
		targetPosts = p.cfg.SeedPosts
	}

	startedAt := time.Now().UTC()

	seedStats, err := p.store.q.GetSeedStats(ctx)
	if err != nil {
		return 0, fmt.Errorf("querying existing posts for seed: %w", err)
	}
	existingCount := int(seedStats.TotalPosts)

	if existingCount >= targetPosts {
		p.cfg.Logger.InfoContext(ctx, "index already has target posts; skipping seed",
			"existing_posts", existingCount,
			"target_posts", targetPosts)
		return 0, nil
	}

	var cursorID hypnohub.PostID
	if minID, ok := seedStats.MinID.(int64); ok && minID > 0 {
		cursorID = hypnohub.PostID(minID)
		p.cfg.Logger.InfoContext(ctx, "resuming seed backwards from existing oldest post",
			"existing_posts", existingCount,
			"target_posts", targetPosts,
			"cursor_min_id", cursorID)
	}

	totalUpserted := 0
	pagesFetched := 0
	lastPollUnix := startedAt.Unix()

	for existingCount+totalUpserted < targetPosts {
		if pagesFetched > 0 && p.cfg.PageDelay > 0 {
			select {
			case <-ctx.Done():
				return totalUpserted, ctx.Err()
			case <-time.After(p.cfg.PageDelay):
			}
		}

		remaining := targetPosts - (existingCount + totalUpserted)
		fetchLimit := hypnohub.MaxSearchPostsLimit
		if remaining < fetchLimit {
			fetchLimit = remaining
		}

		opts := hypnohub.SearchPostsOptions{
			Limit: fetchLimit,
		}
		if cursorID > 0 {
			opts.Query = query.ID(query.LessThan, cursorID).String()
			opts.PageOffset = 0
		} else {
			opts.Query = ""
			opts.PageOffset = 0
		}

		pageStartedAt := time.Now().UTC()
		res, err := p.fetchPostsWithRetry(ctx, opts)
		if err != nil {
			return totalUpserted, fmt.Errorf("fetching seed page %d (cursor %d): %w", pagesFetched+1, cursorID, err)
		}
		pagesFetched++

		posts := res.Posts
		if len(posts) == 0 {
			p.cfg.Logger.InfoContext(ctx, "no more posts returned by upstream; reached earliest post",
				"pages_fetched", pagesFetched,
				"total_upserted", totalUpserted)
			break
		}

		if len(posts) > remaining {
			posts = posts[:remaining]
		}

		// Ensure strictly increasing unix seconds per batch so poll_runs rows do not collide.
		pageCompletedAt := time.Now().UTC()
		batchPollUnix := pageCompletedAt.Unix()
		if batchPollUnix <= lastPollUnix {
			batchPollUnix = lastPollUnix + 1
		}
		lastPollUnix = batchPollUnix
		batchPollTime := time.Unix(batchPollUnix, 0).UTC()

		upserted, err := p.store.IngestBatch(ctx, posts, batchPollTime, pageStartedAt, pageCompletedAt, 1, true)
		if err != nil {
			return totalUpserted, fmt.Errorf("ingesting seed page %d: %w", pagesFetched, err)
		}
		totalUpserted += upserted

		minBatchID := posts[0].ID
		for _, post := range posts {
			if post.ID < minBatchID {
				minBatchID = post.ID
			}
		}
		cursorID = minBatchID

		p.cfg.Logger.InfoContext(ctx, "ingested seed page",
			"page", pagesFetched,
			"posts_in_batch", len(posts),
			"total_upserted", totalUpserted,
			"total_posts_in_db", existingCount+totalUpserted,
			"target_posts", targetPosts,
			"oldest_id_in_batch", cursorID)

		if len(res.Posts) < fetchLimit || cursorID <= 1 {
			break
		}
	}

	completedAt := time.Now().UTC()
	p.cfg.Logger.InfoContext(ctx, "completed index seed",
		"posts_upserted", totalUpserted,
		"total_posts_in_db", existingCount+totalUpserted,
		"pages_fetched", pagesFetched,
		"duration", completedAt.Sub(startedAt).Round(time.Millisecond))

	return totalUpserted, nil
}

// PollOnce performs a single rolling sweep of recent posts up to PollHorizon (or MaxPollPages)
// and records a new score snapshot for every fetched post.
func (p *Poller) PollOnce(ctx context.Context) (int, error) {
	startedAt := time.Now().UTC()
	// Align poll_time to minute granularity for clean time-series points.
	pollTime := startedAt.Truncate(time.Minute)
	cutoff := startedAt.Add(-p.cfg.PollHorizon)

	var allPosts []hypnohub.Post
	pagesFetched := 0

	for pid := 0; pid < p.cfg.MaxPollPages; pid++ {
		if pid > 0 && p.cfg.PageDelay > 0 {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(p.cfg.PageDelay):
			}
		}

		res, err := p.fetchPostsWithRetry(ctx, hypnohub.SearchPostsOptions{
			Query:      "",
			PageOffset: pid,
			Limit:      hypnohub.MaxSearchPostsLimit,
		})
		if err != nil {
			return 0, fmt.Errorf("fetching poll page %d: %w", pid, err)
		}
		pagesFetched++

		if len(res.Posts) == 0 {
			break
		}
		allPosts = append(allPosts, res.Posts...)

		// Stop pagination once the oldest post on the page is older than PollHorizon.
		oldest := res.Posts[len(res.Posts)-1].CreatedAt.Time()
		if !oldest.IsZero() && oldest.Before(cutoff) {
			break
		}
	}

	completedAt := time.Now().UTC()
	upserted, err := p.store.IngestBatch(ctx, allPosts, pollTime, startedAt, completedAt, pagesFetched, false)
	if err != nil {
		return 0, fmt.Errorf("ingesting poll batch: %w", err)
	}

	if resolved, err := p.store.ResolveUnknownTagCategories(ctx, 20); err != nil {
		p.cfg.Logger.WarnContext(ctx, "background tag category resolution failed", "error", err)
	} else if resolved > 0 {
		p.cfg.Logger.DebugContext(ctx, "resolved tag categories in background", "count", resolved)
	}

	p.cfg.Logger.InfoContext(ctx, "completed rolling index poll",
		"posts_upserted", upserted,
		"pages_fetched", pagesFetched,
		"duration", completedAt.Sub(startedAt).Round(time.Millisecond))

	return upserted, nil
}
