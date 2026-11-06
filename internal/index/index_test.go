package index

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gaze.d14.pet"
	"libdb.so/lazymigrate"
)

func TestStoreRateAndDeltaRanking(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 11, 5, 18, 0, 0, 0, time.UTC)

	// Post 101: uploaded 22h ago, steady 30 upvotes (rate = 30 / (22 + 2) = 1.25/h)
	// Post 102: uploaded 2h ago, viral 16 upvotes (rate = 16 / (2 + 2) = 4.00/h)
	// Post 103: uploaded 10m ago, 1 upvote (rate = 1 / (0.167 + 2) = 0.46/h, damped so it doesn't spike)
	posts := []hypnohub.Post{
		{
			ID:            101,
			Score:         30,
			Rating:        hypnohub.RatingExplicit,
			Tags:          "hypnosis spiral animated",
			FileURL:       "https://hypnohub.net/images/101.mp4",
			SampleURL:     "https://hypnohub.net/samples/101.jpg",
			PreviewURL:    "https://hypnohub.net/thumbnails/101.jpg",
			Width:         1920,
			Height:        1080,
			SampleWidth:   850,
			SampleHeight:  478,
			PreviewWidth:  250,
			PreviewHeight: 140,
			CreatedAt:     hypnohub.Date(now.Add(-22 * time.Hour)),
			ChangedAt:     hypnohub.UnixTime(now.Add(-22 * time.Hour).Unix()),
			Status:        "active",
		},
		{
			ID:            102,
			Score:         16,
			Rating:        hypnohub.RatingQuestionable,
			Tags:          "hypnosis mind_control comic",
			FileURL:       "https://hypnohub.net/images/102.png",
			SampleURL:     "https://hypnohub.net/samples/102.jpg",
			PreviewURL:    "https://hypnohub.net/thumbnails/102.jpg",
			Width:         1200,
			Height:        1600,
			SampleWidth:   850,
			SampleHeight:  1133,
			PreviewWidth:  187,
			PreviewHeight: 250,
			CreatedAt:     hypnohub.Date(now.Add(-2 * time.Hour)),
			ChangedAt:     hypnohub.UnixTime(now.Add(-2 * time.Hour).Unix()),
			Status:        "active",
		},
		{
			ID:            103,
			Score:         1,
			Rating:        hypnohub.RatingSafe,
			Tags:          "hypnosis sketch",
			FileURL:       "https://hypnohub.net/images/103.jpg",
			SampleURL:     "https://hypnohub.net/samples/103.jpg",
			PreviewURL:    "https://hypnohub.net/thumbnails/103.jpg",
			Width:         800,
			Height:        800,
			SampleWidth:   800,
			SampleHeight:  800,
			PreviewWidth:  250,
			PreviewHeight: 250,
			CreatedAt:     hypnohub.Date(now.Add(-10 * time.Minute)),
			ChangedAt:     hypnohub.UnixTime(now.Add(-10 * time.Minute).Unix()),
			Status:        "active",
		},
	}

	if _, err := store.IngestBatch(ctx, posts, now, now, now, 1, true); err != nil {
		t.Fatalf("IngestBatch failed: %v", err)
	}

	// 1. Hot sort (velocity) should rank 102 (#1) > 101 (#2) > 103 (#3)
	hotRes, err := store.QueryPosts(ctx, QueryOptions{
		Window: WindowDay,
		Sort:   SortHot,
		Now:    now,
	})
	if err != nil {
		t.Fatalf("QueryPosts(SortHot) failed: %v", err)
	}
	if len(hotRes.Posts) != 3 {
		t.Fatalf("expected 3 posts, got %d", len(hotRes.Posts))
	}
	if hotRes.Posts[0].ID != 102 || hotRes.Posts[1].ID != 101 || hotRes.Posts[2].ID != 103 {
		t.Errorf("unexpected hot order: got [%d, %d, %d], want [102, 101, 103]",
			hotRes.Posts[0].ID, hotRes.Posts[1].ID, hotRes.Posts[2].ID)
	}

	// 2. Top sort (total delta in window) should rank 101 (30) > 102 (16) > 103 (1)
	topRes, err := store.QueryPosts(ctx, QueryOptions{
		Window: WindowDay,
		Sort:   SortTop,
		Now:    now,
	})
	if err != nil {
		t.Fatalf("QueryPosts(SortTop) failed: %v", err)
	}
	if topRes.Posts[0].ID != 101 || topRes.Posts[1].ID != 102 || topRes.Posts[2].ID != 103 {
		t.Errorf("unexpected top order: got [%d, %d, %d], want [101, 102, 103]",
			topRes.Posts[0].ID, topRes.Posts[1].ID, topRes.Posts[2].ID)
	}

	// 3. Tag filtering (include + exclude)
	tagRes, err := store.QueryPosts(ctx, QueryOptions{
		Window:      WindowDay,
		Sort:        SortHot,
		IncludeTags: []string{"hypnosis"},
		ExcludeTags: []string{"animated"},
		Now:         now,
	})
	if err != nil {
		t.Fatalf("QueryPosts(tag filter) failed: %v", err)
	}
	if len(tagRes.Posts) != 2 || tagRes.Posts[0].ID != 102 || tagRes.Posts[1].ID != 103 {
		t.Errorf("unexpected filtered posts: %+v", tagRes.Posts)
	}

	// 4. Subsequent 2-hour snapshot delta calculation for older post:
	// Suppose Post 100 was created 5 days ago with score 50 at now-24h, and gained +25 in the last 24h (score 75 at now).
	oldPost := hypnohub.Post{
		ID:            100,
		Score:         50,
		Rating:        hypnohub.RatingExplicit,
		Tags:          "hypnosis classic",
		FileURL:       "https://hypnohub.net/images/100.png",
		SampleURL:     "https://hypnohub.net/samples/100.jpg",
		PreviewURL:    "https://hypnohub.net/thumbnails/100.jpg",
		Width:         1000,
		Height:        1000,
		PreviewWidth:  250,
		PreviewHeight: 250,
		CreatedAt:     hypnohub.Date(now.Add(-5 * 24 * time.Hour)),
		ChangedAt:     hypnohub.UnixTime(now.Add(-5 * 24 * time.Hour).Unix()),
		Status:        "active",
	}
	tMinus24h := now.Add(-24 * time.Hour)
	if _, err := store.IngestBatch(ctx, []hypnohub.Post{oldPost}, tMinus24h, tMinus24h, tMinus24h, 1, false); err != nil {
		t.Fatalf("IngestBatch(t-24h) failed: %v", err)
	}
	oldPost.Score = 75
	if _, err := store.IngestBatch(ctx, []hypnohub.Post{oldPost}, now, now, now, 1, false); err != nil {
		t.Fatalf("IngestBatch(now) failed: %v", err)
	}

	// Query 24h window: Post 100 was created 5d ago (outside 24h window), its earliest snapshot in 24h is 50, so delta = 75 - 50 = 25.
	dayRes, err := store.QueryPosts(ctx, QueryOptions{
		Window:      WindowDay,
		Sort:        SortTop,
		IncludeTags: []string{"classic"},
		Now:         now,
	})
	if err != nil {
		t.Fatalf("QueryPosts(WindowDay) failed: %v", err)
	}
	if len(dayRes.Posts) != 1 || dayRes.Posts[0].ScoreDelta != 25 || dayRes.Posts[0].BaselineScore != 50 {
		t.Errorf("expected ScoreDelta=25 and BaselineScore=50, got %+v", dayRes.Posts)
	}
}

func TestStoreWindowDeltaAndBaseline(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 11, 5, 20, 0, 0, 0, time.UTC)

	// Test 1: A post created at now - 10h (inside a 24h window), polled at now - 8h with score 10,
	// and polled again at now with score 25.
	// Assert querying WindowDay (24h) gives BaselineScore == 0 and ScoreDelta == 25.
	postInside := hypnohub.Post{
		ID:        201,
		Score:     10,
		Rating:    hypnohub.RatingSafe,
		Tags:      "hypno test_inside",
		CreatedAt: hypnohub.Date(now.Add(-10 * time.Hour)),
		Status:    "active",
	}
	tMinus8h := now.Add(-8 * time.Hour)
	if _, err := store.IngestBatch(ctx, []hypnohub.Post{postInside}, tMinus8h, tMinus8h, tMinus8h, 1, false); err != nil {
		t.Fatalf("IngestBatch(postInside, t-8h) failed: %v", err)
	}

	postInside.Score = 25
	if _, err := store.IngestBatch(ctx, []hypnohub.Post{postInside}, now, now, now, 1, false); err != nil {
		t.Fatalf("IngestBatch(postInside, now) failed: %v", err)
	}

	resInside, err := store.QueryPosts(ctx, QueryOptions{
		Window:      WindowDay,
		Sort:        SortTop,
		IncludeTags: []string{"test_inside"},
		Now:         now,
	})
	if err != nil {
		t.Fatalf("QueryPosts(resInside) failed: %v", err)
	}
	if len(resInside.Posts) != 1 {
		t.Fatalf("expected 1 post, got %d", len(resInside.Posts))
	}
	pInside := resInside.Posts[0]
	if pInside.BaselineScore != 0 || pInside.ScoreDelta != 25 {
		t.Errorf("postInside: want BaselineScore=0, ScoreDelta=25; got BaselineScore=%d, ScoreDelta=%d",
			pInside.BaselineScore, pInside.ScoreDelta)
	}

	// Test 2: An older post created at now - 5d (outside the 24h window), polled at now - 20h with score 50,
	// and polled at now with score 75.
	// Assert querying WindowDay (24h) includes this post with BaselineScore == 50 and ScoreDelta == 25.
	postOlder := hypnohub.Post{
		ID:        202,
		Score:     50,
		Rating:    hypnohub.RatingSafe,
		Tags:      "hypno test_older",
		CreatedAt: hypnohub.Date(now.Add(-5 * 24 * time.Hour)),
		Status:    "active",
	}
	tMinus20h := now.Add(-20 * time.Hour)
	if _, err := store.IngestBatch(ctx, []hypnohub.Post{postOlder}, tMinus20h, tMinus20h, tMinus20h, 1, false); err != nil {
		t.Fatalf("IngestBatch(postOlder, t-20h) failed: %v", err)
	}

	postOlder.Score = 75
	if _, err := store.IngestBatch(ctx, []hypnohub.Post{postOlder}, now, now, now, 1, false); err != nil {
		t.Fatalf("IngestBatch(postOlder, now) failed: %v", err)
	}

	resOlder, err := store.QueryPosts(ctx, QueryOptions{
		Window:      WindowDay,
		Sort:        SortTop,
		IncludeTags: []string{"test_older"},
		Now:         now,
	})
	if err != nil {
		t.Fatalf("QueryPosts(resOlder) failed: %v", err)
	}
	if len(resOlder.Posts) != 1 {
		t.Fatalf("expected 1 older post, got %d", len(resOlder.Posts))
	}
	pOlder := resOlder.Posts[0]
	if pOlder.BaselineScore != 50 || pOlder.ScoreDelta != 25 {
		t.Errorf("postOlder: want BaselineScore=50, ScoreDelta=25; got BaselineScore=%d, ScoreDelta=%d",
			pOlder.BaselineScore, pOlder.ScoreDelta)
	}
	if pOlder.WindowHours != 20.0 {
		t.Errorf("postOlder: want WindowHours=20.0, got %f", pOlder.WindowHours)
	}
}

func TestStoreTagRemoval(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 11, 5, 20, 0, 0, 0, time.UTC)

	post := hypnohub.Post{
		ID:        301,
		Score:     10,
		Rating:    hypnohub.RatingSafe,
		Tags:      "alpha beta gamma",
		CreatedAt: hypnohub.Date(now.Add(-1 * time.Hour)),
		Status:    "active",
	}
	if _, err := store.IngestBatch(ctx, []hypnohub.Post{post}, now.Add(-1*time.Hour), now.Add(-1*time.Hour), now.Add(-1*time.Hour), 1, false); err != nil {
		t.Fatalf("initial ingest failed: %v", err)
	}

	// Verify post has tags alpha, beta, gamma and tags have count 1
	tags, err := store.SearchTags(ctx, "", 10)
	if err != nil {
		t.Fatalf("SearchTags failed: %v", err)
	}
	for _, tg := range tags {
		if tg.PostCount != 1 {
			t.Errorf("expected tag %q post_count=1, got %d", tg.Name, tg.PostCount)
		}
	}

	// Now update post with 'gamma' removed, keeping only 'alpha beta'
	post.Tags = "alpha beta"
	if _, err := store.IngestBatch(ctx, []hypnohub.Post{post}, now, now, now, 1, false); err != nil {
		t.Fatalf("updated ingest failed: %v", err)
	}

	// Search for post with gamma - should return 0 posts
	resGamma, err := store.QueryPosts(ctx, QueryOptions{
		IncludeTags: []string{"gamma"},
		Now:         now,
	})
	if err != nil {
		t.Fatalf("QueryPosts(gamma) failed: %v", err)
	}
	if len(resGamma.Posts) != 0 {
		t.Errorf("expected 0 posts for removed tag gamma, got %d", len(resGamma.Posts))
	}

	// Verify tag count for gamma is now 0
	tagsAfter, err := store.SearchTags(ctx, "gamma", 1)
	if err != nil {
		t.Fatalf("SearchTags(gamma) failed: %v", err)
	}
	if len(tagsAfter) > 0 && tagsAfter[0].PostCount != 0 {
		t.Errorf("expected gamma post_count=0, got %d", tagsAfter[0].PostCount)
	}
}

func TestSuperlinearMomentumHotScore(t *testing.T) {
	// 1. Verify Window.DampingHours() table
	dampingTests := []struct {
		window Window
		want   float64
	}{
		{Window6Hours, 1.5},
		{WindowDay, 2.0},
		{Window3Days, 6.0},
		{WindowWeek, 12.0},
		{Window2Weeks, 24.0},
		{WindowMonth, 48.0},
		{WindowAllTime, 168.0},
		{"invalid", 2.0},
	}
	for _, tt := range dampingTests {
		if got := tt.window.DampingHours(); got != tt.want {
			t.Errorf("Window(%q).DampingHours() = %v, want %v", tt.window, got, tt.want)
		}
	}

	// 2. Verify Superlinear Momentum ranking:
	// Post 401: 2 votes in 10 minutes (raw rate = 12/h, score = 2)
	// Post 402: 24 votes in 2 hours (raw rate = 12/h, score = 80)
	// Post 403: 15 votes in 2 hours (raw rate = 7.5/h, score = 100)
	// Under F2: (delta / (t + tau)) * S^0.35
	// Post 402: (24 / 4) * 80^0.35 = 6 * 4.636 = 27.8
	// Post 403: (15 / 4) * 100^0.35 = 3.75 * 5.0118 = 18.8
	// Post 401: (2 / 2.167) * 2^0.35 = 0.923 * 1.274 = 1.17
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 11, 5, 20, 0, 0, 0, time.UTC)
	posts := []hypnohub.Post{
		{
			ID:        401,
			Score:     2,
			Rating:    hypnohub.RatingSafe,
			CreatedAt: hypnohub.Date(now.Add(-10 * time.Minute)),
			Status:    "active",
		},
		{
			ID:        402,
			Score:     40,
			Rating:    hypnohub.RatingSafe,
			CreatedAt: hypnohub.Date(now.Add(-2 * time.Hour)),
			Status:    "active",
		},
		{
			ID:        403,
			Score:     15,
			Rating:    hypnohub.RatingSafe,
			CreatedAt: hypnohub.Date(now.Add(-2 * time.Hour)),
			Status:    "active",
		},
	}
	if _, err := store.IngestBatch(ctx, posts, now, now, now, 1, false); err != nil {
		t.Fatalf("IngestBatch failed: %v", err)
	}

	res, err := store.QueryPosts(ctx, QueryOptions{
		Window: WindowDay,
		Sort:   SortHot,
		Now:    now,
	})
	if err != nil {
		t.Fatalf("QueryPosts failed: %v", err)
	}
	if len(res.Posts) != 3 {
		t.Fatalf("expected 3 posts, got %d", len(res.Posts))
	}

	// Post 402 must beat Post 403, and Post 403 must beat Post 401
	if res.Posts[0].ID != 402 || res.Posts[1].ID != 403 || res.Posts[2].ID != 401 {
		t.Errorf("unexpected hot ranking: got [%d, %d, %d], want [402, 403, 401]",
			res.Posts[0].ID, res.Posts[1].ID, res.Posts[2].ID)
	}

	if res.Posts[0].HotScore <= res.Posts[1].HotScore || res.Posts[1].HotScore <= res.Posts[2].HotScore {
		t.Errorf("expected strictly decreasing HotScore: got %v, %v, %v",
			res.Posts[0].HotScore, res.Posts[1].HotScore, res.Posts[2].HotScore)
	}
}

type mockFetcher struct {
	sync.Mutex
	posts     []hypnohub.Post
	failCount int
	calls     []hypnohub.SearchPostsOptions
}

func (m *mockFetcher) SearchPostsWithOptions(ctx context.Context, opts hypnohub.SearchPostsOptions) (*hypnohub.SearchPostsResult, error) {
	m.Lock()
	defer m.Unlock()
	m.calls = append(m.calls, opts)

	if m.failCount > 0 {
		m.failCount--
		return nil, errors.New("simulated transient 503 service unavailable")
	}

	var matched []hypnohub.Post
	maxID := math.MaxInt
	if strings.HasPrefix(opts.Query, "id:<") {
		fmt.Sscanf(opts.Query, "id:<%d", &maxID)
	}

	for _, p := range m.posts {
		if int(p.ID) < maxID {
			matched = append(matched, p)
		}
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = hypnohub.MaxSearchPostsLimit
	}
	if len(matched) > limit {
		matched = matched[:limit]
	}

	return &hypnohub.SearchPostsResult{
		Posts: matched,
		Count: len(matched),
	}, nil
}

func TestPollerSeedIncrementalAndCursor(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	// Generate 2500 mock posts (IDs 2500 down to 1)
	now := time.Date(2026, 11, 5, 20, 0, 0, 0, time.UTC)
	var mockPosts []hypnohub.Post
	for id := 2500; id >= 1; id-- {
		mockPosts = append(mockPosts, hypnohub.Post{
			ID:        hypnohub.PostID(id),
			Score:     10,
			Rating:    hypnohub.RatingSafe,
			Tags:      "hypnosis spiral",
			CreatedAt: hypnohub.Date(now.Add(-time.Duration(2500-id) * time.Hour)),
			Status:    "active",
		})
	}

	fetcher := &mockFetcher{posts: mockPosts}
	poller := NewPoller(store, fetcher, PollerConfig{
		SeedPosts: 2500,
		PageDelay: time.Millisecond,
	})

	// First run: seed 2500 posts
	upserted, err := poller.Seed(ctx, 2500)
	if err != nil {
		t.Fatalf("Seed failed: %v", err)
	}
	if upserted != 2500 {
		t.Errorf("expected 2500 upserted, got %d", upserted)
	}

	var count, minID, maxID int
	if err := store.DB().QueryRowContext(ctx, "SELECT COUNT(*), MIN(id), MAX(id) FROM posts").Scan(&count, &minID, &maxID); err != nil {
		t.Fatalf("querying stats failed: %v", err)
	}
	if count != 2500 || minID != 1 || maxID != 2500 {
		t.Errorf("expected count=2500, min=1, max=2500; got count=%d, min=%d, max=%d", count, minID, maxID)
	}

	// Verify incremental batch commits created multiple poll_runs entries
	var runsCount int
	if err := store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM poll_runs WHERE is_seed = 1").Scan(&runsCount); err != nil {
		t.Fatalf("querying poll_runs failed: %v", err)
	}
	if runsCount < 3 {
		t.Errorf("expected at least 3 incremental poll_runs commits, got %d", runsCount)
	}

	// Second run: target 2500 when already 2500 in DB -> should do 0 upserts
	upserted2, err := poller.Seed(ctx, 2500)
	if err != nil {
		t.Fatalf("Second Seed failed: %v", err)
	}
	if upserted2 != 0 {
		t.Errorf("expected 0 upserted on second seed, got %d", upserted2)
	}

	// Third run: target 3000 -> resumes from minID=1 with cursor id:<1, finds 0 more posts, completes
	upserted3, err := poller.Seed(ctx, 3000)
	if err != nil {
		t.Fatalf("Third Seed failed: %v", err)
	}
	if upserted3 != 0 {
		t.Errorf("expected 0 upserted when no older posts exist, got %d", upserted3)
	}
}

func TestPollerSeedRetry(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 11, 5, 20, 0, 0, 0, time.UTC)
	var mockPosts []hypnohub.Post
	for id := 10; id >= 1; id-- {
		mockPosts = append(mockPosts, hypnohub.Post{
			ID:        hypnohub.PostID(id),
			Score:     5,
			Rating:    hypnohub.RatingSafe,
			Tags:      "test",
			CreatedAt: hypnohub.Date(now),
			Status:    "active",
		})
	}

	// failCount = 2 means it fails twice and succeeds on the 3rd attempt
	fetcher := &mockFetcher{
		posts:     mockPosts,
		failCount: 2,
	}
	poller := NewPoller(store, fetcher, PollerConfig{
		SeedPosts: 10,
		PageDelay: time.Millisecond,
	})

	upserted, err := poller.Seed(ctx, 10)
	if err != nil {
		t.Fatalf("Seed with retry failed: %v", err)
	}
	if upserted != 10 {
		t.Errorf("expected 10 upserted, got %d", upserted)
	}
}

func TestBenchmark100kPosts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 100k post benchmark in short mode")
	}

	dbPath := "../../hypnoview.db"
	ctx := context.Background()
	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Skipf("skipping 100k post benchmark: %v", err)
	}
	defer store.Close()

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	if stats.TotalPosts < 100000 {
		t.Skipf("skipping: database only has %d posts", stats.TotalPosts)
	}
	t.Logf("Database stats: %d posts, %d tags, %d snapshots", stats.TotalPosts, stats.TotalTags, stats.TotalSnapshots)

	// 1. Homepage Browse feed: window=all, sort=new
	start := time.Now()
	resBrowse, err := store.QueryPosts(ctx, QueryOptions{
		Window: WindowAllTime,
		Sort:   SortNew,
		Limit:  48,
	})
	browseDur := time.Since(start)
	if err != nil {
		t.Fatalf("QueryPosts(window=all, sort=new) failed: %v", err)
	}
	if len(resBrowse.Posts) == 0 {
		t.Errorf("expected posts in browse query, got 0")
	}
	t.Logf("Browse feed (window=all, sort=new) took %s (total=%d, returned=%d)",
		browseDur, resBrowse.Total, len(resBrowse.Posts))

	// 2. Popular 24h: window=24h, sort=hot
	start = time.Now()
	resPop24h, err := store.QueryPosts(ctx, QueryOptions{
		Window: WindowDay,
		Sort:   SortHot,
		Limit:  48,
	})
	elapsedPop24h := time.Since(start)
	if err != nil {
		t.Fatalf("QueryPosts(24h, hot) failed: %v", err)
	}
	t.Logf("QueryPosts(24h, hot) [Popular 24h]: %s, returned %d posts (total: %d)",
		elapsedPop24h, len(resPop24h.Posts), resPop24h.Total)

	// 3. Popular 30d: window=30d, sort=hot
	start = time.Now()
	resPop30d, err := store.QueryPosts(ctx, QueryOptions{
		Window: WindowMonth,
		Sort:   SortHot,
		Limit:  48,
	})
	elapsedPop30d := time.Since(start)
	if err != nil {
		t.Fatalf("QueryPosts(30d, hot) failed: %v", err)
	}
	t.Logf("QueryPosts(30d, hot) [Popular 30d]: %s, returned %d posts (total: %d)",
		elapsedPop30d, len(resPop30d.Posts), resPop30d.Total)

	// 4. Tag filtered Browse query: window=all, sort=new, IncludeTags=["spiral"]
	start = time.Now()
	resTags, err := store.QueryPosts(ctx, QueryOptions{
		Window:      WindowAllTime,
		Sort:        SortNew,
		IncludeTags: []string{"spiral"},
		Limit:       48,
	})
	elapsedTags := time.Since(start)
	if err != nil {
		t.Fatalf("QueryPosts(spiral) failed: %v", err)
	}
	t.Logf("QueryPosts(all, new, tag=spiral): %s, returned %d posts (total: %d)",
		elapsedTags, len(resTags.Posts), resTags.Total)

	// 5. SearchTags: "hypno", limit 30
	start = time.Now()
	tags, err := store.SearchTags(ctx, "hypno", 30)
	elapsedSearchTags := time.Since(start)
	if err != nil {
		t.Fatalf("SearchTags(hypno) failed: %v", err)
	}
	t.Logf("SearchTags('hypno'): %s, returned %d tags", elapsedSearchTags, len(tags))
}

func TestOpenCorruptedDatabaseBackup(t *testing.T) {
	ctx := context.Background()

	t.Run("header_garbage", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "corrupted.db")

		garbage := []byte("not a valid sqlite database garbage content")
		if err := os.WriteFile(dbPath, garbage, 0644); err != nil {
			t.Fatalf("writing garbage file: %v", err)
		}

		store, err := Open(ctx, dbPath)
		if err != nil {
			t.Fatalf("Open failed to recover corrupted database: %v", err)
		}
		defer store.Close()

		// Verify .bak exists and has original garbage
		bakPath := dbPath + ".bak"
		bakBytes, err := os.ReadFile(bakPath)
		if err != nil {
			t.Fatalf("expected .bak file to exist at %s: %v", bakPath, err)
		}
		if !bytes.Equal(bakBytes, garbage) {
			t.Errorf("expected .bak to contain garbage bytes, got: %s", string(bakBytes))
		}

		// Verify new store is working properly
		stats, err := store.Stats(ctx)
		if err != nil {
			t.Fatalf("Store Stats failed on recreated db: %v", err)
		}
		if stats.TotalPosts != 0 {
			t.Errorf("expected 0 posts on fresh database, got %d", stats.TotalPosts)
		}
	})

	t.Run("mid_file_corruption", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "mid_corrupt.db")

		// 1. Create a populated multi-page database
		store, err := Open(ctx, dbPath)
		if err != nil {
			t.Fatalf("initial open failed: %v", err)
		}
		var posts []hypnohub.Post
		now := time.Now().UTC()
		for i := 1; i <= 60; i++ {
			posts = append(posts, hypnohub.Post{
				ID:         hypnohub.PostID(i),
				CreatedAt:  hypnohub.Date(now.Add(time.Duration(i) * time.Minute)),
				Score:      i * 5,
				Rating:     hypnohub.RatingExplicit,
				Tags:       "test tag multi page",
				FileURL:    fmt.Sprintf("https://hypnohub.net/%d.jpg", i),
				SampleURL:  fmt.Sprintf("https://hypnohub.net/%d.jpg", i),
				PreviewURL: fmt.Sprintf("https://hypnohub.net/%d.jpg", i),
				Width:      800,
				Height:     600,
				Status:     "active",
			})
		}
		if _, err := store.IngestBatch(ctx, posts, now, now, now, 1, false); err != nil {
			t.Fatalf("ingest batch failed: %v", err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("close store failed: %v", err)
		}

		// 2. Overwrite bytes in the middle of the database file
		dbBytes, err := os.ReadFile(dbPath)
		if err != nil {
			t.Fatalf("reading db file: %v", err)
		}
		if len(dbBytes) < 4096 {
			t.Fatalf("db file too small for mid-file corruption: %d bytes", len(dbBytes))
		}
		corruptedBytes := make([]byte, len(dbBytes))
		copy(corruptedBytes, dbBytes)
		// Overwrite bytes starting at page 2 (offset 4096), preserving the SQLite file header on page 1
		copy(corruptedBytes[4096:4096+256], bytes.Repeat([]byte{0xFF}, 256))
		if err := os.WriteFile(dbPath, corruptedBytes, 0644); err != nil {
			t.Fatalf("writing corrupted db: %v", err)
		}

		// 3. Open corrupted database; should detect quick_check failure, backup to .bak, and recreate
		recoveredStore, err := Open(ctx, dbPath)
		if err != nil {
			t.Fatalf("Open failed to recover mid-file corrupted database: %v", err)
		}
		defer recoveredStore.Close()

		bakPath := dbPath + ".bak"
		bakBytes, err := os.ReadFile(bakPath)
		if err != nil {
			t.Fatalf("expected .bak file at %s: %v", bakPath, err)
		}
		if !bytes.Equal(bakBytes, corruptedBytes) {
			t.Errorf("expected .bak to match corrupted file bytes")
		}

		stats, err := recoveredStore.Stats(ctx)
		if err != nil {
			t.Fatalf("Stats failed on recreated db: %v", err)
		}
		if stats.TotalPosts != 0 {
			t.Errorf("expected 0 posts on fresh database, got %d", stats.TotalPosts)
		}
	})
}

func TestPostGroupStatusTransitions(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		run  func(t *testing.T, store *Store)
	}{
		{
			name: "standalone_flagged_to_active",
			run: func(t *testing.T, store *Store) {
				now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
				p500 := hypnohub.Post{
					ID:        500,
					Score:     50,
					Rating:    hypnohub.RatingExplicit,
					Tags:      "solo",
					FileURL:   "https://hypnohub.net/500.jpg",
					SampleURL: "https://hypnohub.net/500.jpg",
					Width:     1000,
					Height:    1000,
					CreatedAt: hypnohub.Date(now),
					ChangedAt: hypnohub.UnixTime(now.Unix()),
					Status:    "flagged",
				}
				if _, err := store.IngestBatch(ctx, []hypnohub.Post{p500}, now, now, now, 1, false); err != nil {
					t.Fatalf("ingest flagged: %v", err)
				}

				// Re-ingest as active
				p500.Status = "active"
				if _, err := store.IngestBatch(ctx, []hypnohub.Post{p500}, now, now, now, 1, false); err != nil {
					t.Fatalf("ingest active: %v", err)
				}

				res, err := store.QueryPosts(ctx, QueryOptions{
					CollapseGroups: true,
					Limit:          10,
				})
				if err != nil {
					t.Fatalf("query posts: %v", err)
				}
				if res.Total != 1 {
					t.Fatalf("expected Total == 1, got %d", res.Total)
				}
				if len(res.Posts) != 1 {
					t.Fatalf("expected 1 post, got %d", len(res.Posts))
				}
				if res.Posts[0].ID != 500 {
					t.Errorf("expected post 500, got %d", res.Posts[0].ID)
				}
				if !res.Posts[0].GroupIsLatest {
					t.Errorf("expected post 500 to have GroupIsLatest == true")
				}
			},
		},
		{
			name: "children_active_parent_flagged_to_active",
			run: func(t *testing.T, store *Store) {
				now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
				posts := []hypnohub.Post{
					{
						ID:          100,
						Score:       10,
						Rating:      hypnohub.RatingExplicit,
						Tags:        "comic series",
						FileURL:     "https://hypnohub.net/100.jpg",
						SampleURL:   "https://hypnohub.net/100.jpg",
						Width:       1000,
						Height:      1000,
						CreatedAt:   hypnohub.Date(now.Add(-2 * time.Hour)),
						ChangedAt:   hypnohub.UnixTime(now.Add(-2 * time.Hour).Unix()),
						HasChildren: false,
						Status:      "flagged",
					},
					{
						ID:          101,
						ParentID:    100,
						Score:       20,
						Rating:      hypnohub.RatingExplicit,
						Tags:        "comic series",
						FileURL:     "https://hypnohub.net/101.jpg",
						SampleURL:   "https://hypnohub.net/101.jpg",
						Width:       1000,
						Height:      1000,
						CreatedAt:   hypnohub.Date(now.Add(-1 * time.Hour)),
						ChangedAt:   hypnohub.UnixTime(now.Add(-1 * time.Hour).Unix()),
						HasChildren: true,
						Status:      "active",
					},
					{
						ID:        102,
						ParentID:  100,
						Score:     30,
						Rating:    hypnohub.RatingExplicit,
						Tags:      "comic series",
						FileURL:   "https://hypnohub.net/102.jpg",
						SampleURL: "https://hypnohub.net/102.jpg",
						Width:     1000,
						Height:    1000,
						CreatedAt: hypnohub.Date(now),
						ChangedAt: hypnohub.UnixTime(now.Unix()),
						Status:    "active",
					},
				}
				if _, err := store.IngestBatch(ctx, posts, now, now, now, 1, false); err != nil {
					t.Fatalf("initial ingest: %v", err)
				}

				// Re-ingest parent 100 as active
				parent := posts[0]
				parent.Status = "active"
				if _, err := store.IngestBatch(ctx, []hypnohub.Post{parent}, now, now, now, 1, false); err != nil {
					t.Fatalf("re-ingest parent as active: %v", err)
				}

				for _, id := range []hypnohub.PostID{100, 101, 102} {
					p, _, err := store.GetPost(ctx, id)
					if err != nil {
						t.Fatalf("getting post %d: %v", id, err)
					}
					if p.GroupID != 100 {
						t.Errorf("post %d: expected GroupID == 100, got %d", id, p.GroupID)
					}
					if p.GroupPageCount != 3 {
						t.Errorf("post %d: expected GroupPageCount == 3, got %d", id, p.GroupPageCount)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := Open(ctx, ":memory:")
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer store.Close()

			tt.run(t, store)
		})
	}
}

func TestGetPostMatchesPopularWhenPollIsStale(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	// The latest poll was 48 hours ago
	latestPoll := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
	midPoll := latestPoll.Add(-12 * time.Hour)

	// Post 1: created 36h before latest poll (before 24h window), but has a snapshot at midPoll (-12h, inside window)
	// with score 10, then at latestPoll with score 35.
	p1 := hypnohub.Post{
		ID:        1,
		Score:     10,
		Rating:    hypnohub.RatingExplicit,
		Tags:      "test",
		FileURL:   "https://hypnohub.net/1.jpg",
		SampleURL: "https://hypnohub.net/1.jpg",
		Width:     800,
		Height:    600,
		CreatedAt: hypnohub.Date(latestPoll.Add(-36 * time.Hour)),
		Status:    "active",
	}

	// Post 2: created 10h before latest poll (within 24h window), but only first polled at latestPoll (no snapshots inside window).
	p2 := hypnohub.Post{
		ID:        2,
		Score:     25,
		Rating:    hypnohub.RatingExplicit,
		Tags:      "test",
		FileURL:   "https://hypnohub.net/2.jpg",
		SampleURL: "https://hypnohub.net/2.jpg",
		Width:     800,
		Height:    600,
		CreatedAt: hypnohub.Date(latestPoll.Add(-10 * time.Hour)),
		Status:    "active",
	}

	// Post 3: older post created 100h before latest poll, polled at -80h, but no snapshots in [latestPoll-24h, latestPoll).
	p3 := hypnohub.Post{
		ID:        3,
		Score:     50,
		Rating:    hypnohub.RatingExplicit,
		Tags:      "test",
		FileURL:   "https://hypnohub.net/3.jpg",
		SampleURL: "https://hypnohub.net/3.jpg",
		Width:     800,
		Height:    600,
		CreatedAt: hypnohub.Date(latestPoll.Add(-100 * time.Hour)),
		Status:    "active",
	}

	// Ingest p3 at -80h
	oldPoll := latestPoll.Add(-80 * time.Hour)
	if _, err := store.IngestBatch(ctx, []hypnohub.Post{p3}, oldPoll, oldPoll, oldPoll, 1, false); err != nil {
		t.Fatalf("ingest oldPoll: %v", err)
	}

	// Ingest p1 at midPoll (-12h)
	if _, err := store.IngestBatch(ctx, []hypnohub.Post{p1}, midPoll, midPoll, midPoll, 1, false); err != nil {
		t.Fatalf("ingest midPoll: %v", err)
	}

	// Ingest p1, p2, p3 at latestPoll (-48h ago from wall clock)
	p1.Score = 35
	if _, err := store.IngestBatch(ctx, []hypnohub.Post{p1, p2, p3}, latestPoll, latestPoll, latestPoll, 1, false); err != nil {
		t.Fatalf("ingest latestPoll: %v", err)
	}

	res, err := store.QueryPosts(ctx, QueryOptions{
		Window: WindowDay,
		Sort:   SortHot,
	})
	if err != nil {
		t.Fatalf("QueryPosts: %v", err)
	}

	resMap := make(map[hypnohub.PostID]IndexedPost, len(res.Posts))
	for _, p := range res.Posts {
		resMap[p.ID] = p
	}

	tests := []struct {
		name          string
		postID        hypnohub.PostID
		wantInPopular bool
		wantDelta     int
	}{
		{
			name:          "post_with_snapshots_in_24h_window",
			postID:        1,
			wantInPopular: true,
			wantDelta:     25, // 35 - 10
		},
		{
			name:          "post_created_in_window_without_prior_snapshots",
			postID:        2,
			wantInPopular: true,
			wantDelta:     25, // full score
		},
		{
			name:          "older_post_without_snapshots_in_window",
			postID:        3,
			wantInPopular: false,
			wantDelta:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, err := store.GetPost(ctx, tt.postID)
			if err != nil {
				t.Fatalf("GetPost failed: %v", err)
			}
			if got.ScoreDelta != tt.wantDelta {
				t.Errorf("GetPost ScoreDelta = %d, want %d", got.ScoreDelta, tt.wantDelta)
			}

			if tt.wantInPopular {
				popPost, ok := resMap[tt.postID]
				if !ok {
					t.Fatalf("expected post %d in popular results", tt.postID)
				}
				if got.HotScore != popPost.HotScore {
					t.Errorf("HotScore mismatch for post %d: GetPost=%v, Popular=%v", tt.postID, got.HotScore, popPost.HotScore)
				}
				if got.ScoreDelta != popPost.ScoreDelta {
					t.Errorf("ScoreDelta mismatch for post %d: GetPost=%v, Popular=%v", tt.postID, got.ScoreDelta, popPost.ScoreDelta)
				}
			} else {
				if got.HotScore != 0 {
					t.Errorf("expected HotScore 0 for inactive post, got %v", got.HotScore)
				}
				if _, ok := resMap[tt.postID]; ok {
					t.Errorf("inactive post %d should not appear in popular results", tt.postID)
				}
			}
		})
	}
}

func TestSearchTagsUnderscoreEscapeAndActiveCount(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	// 1. Ingest posts with tags "a_b" and "axb"
	p1 := hypnohub.Post{
		ID:        1,
		Score:     10,
		Rating:    hypnohub.RatingExplicit,
		Tags:      "a_b testtag",
		FileURL:   "https://hypnohub.net/1.jpg",
		SampleURL: "https://hypnohub.net/1.jpg",
		Width:     800,
		Height:    600,
		CreatedAt: hypnohub.Date(now),
		Status:    "active",
	}
	p2 := hypnohub.Post{
		ID:        2,
		Score:     10,
		Rating:    hypnohub.RatingExplicit,
		Tags:      "axb othertag",
		FileURL:   "https://hypnohub.net/2.jpg",
		SampleURL: "https://hypnohub.net/2.jpg",
		Width:     800,
		Height:    600,
		CreatedAt: hypnohub.Date(now),
		Status:    "active",
	}

	// 2. Ingest post with tag "inactive_only" on a flagged post
	p3 := hypnohub.Post{
		ID:        3,
		Score:     5,
		Rating:    hypnohub.RatingExplicit,
		Tags:      "inactive_only",
		FileURL:   "https://hypnohub.net/3.jpg",
		SampleURL: "https://hypnohub.net/3.jpg",
		Width:     800,
		Height:    600,
		CreatedAt: hypnohub.Date(now),
		Status:    "flagged",
	}

	// 3. Ingest post with tag "deleted_only" on a deleted post
	p4 := hypnohub.Post{
		ID:        4,
		Score:     5,
		Rating:    hypnohub.RatingExplicit,
		Tags:      "deleted_only",
		FileURL:   "https://hypnohub.net/4.jpg",
		SampleURL: "https://hypnohub.net/4.jpg",
		Width:     800,
		Height:    600,
		CreatedAt: hypnohub.Date(now),
		Status:    "deleted",
	}

	if _, err := store.IngestBatch(ctx, []hypnohub.Post{p1, p2, p3, p4}, now, now, now, 1, false); err != nil {
		t.Fatalf("ingest batch: %v", err)
	}

	// Test 1: Searching for "a_b" should only return "a_b", not "axb"
	tags, err := store.SearchTags(ctx, "a_b", 10)
	if err != nil {
		t.Fatalf("SearchTags 'a_b' failed: %v", err)
	}
	if len(tags) != 1 {
		t.Fatalf("expected exactly 1 tag for 'a_b', got %d: %+v", len(tags), tags)
	}
	if tags[0].Name != "a_b" {
		t.Errorf("expected tag 'a_b', got %q", tags[0].Name)
	}

	// Test 2: Inactive-only tags ("inactive_only", "deleted_only") must have post_count == 0 and be excluded
	for _, tag := range []string{"inactive_only", "deleted_only"} {
		res, err := store.SearchTags(ctx, tag, 10)
		if err != nil {
			t.Fatalf("SearchTags %q failed: %v", tag, err)
		}
		if len(res) != 0 {
			t.Errorf("expected tag %q to be excluded (post_count == 0), but got: %+v", tag, res)
		}
	}

	// Test 3: Active post transitioning to flagged updates tag post_count to 0
	p5 := hypnohub.Post{
		ID:        5,
		Score:     10,
		Rating:    hypnohub.RatingExplicit,
		Tags:      "dynamic_tag",
		FileURL:   "https://hypnohub.net/5.jpg",
		SampleURL: "https://hypnohub.net/5.jpg",
		Width:     800,
		Height:    600,
		CreatedAt: hypnohub.Date(now),
		Status:    "active",
	}
	if _, err := store.IngestBatch(ctx, []hypnohub.Post{p5}, now, now, now, 1, false); err != nil {
		t.Fatalf("ingest p5 active: %v", err)
	}
	resDynamic, err := store.SearchTags(ctx, "dynamic_tag", 10)
	if err != nil || len(resDynamic) != 1 {
		t.Fatalf("expected dynamic_tag to have post_count > 0, got %+v (err: %v)", resDynamic, err)
	}

	// Transition p5 to flagged
	p5.Status = "flagged"
	if _, err := store.IngestBatch(ctx, []hypnohub.Post{p5}, now, now, now, 1, false); err != nil {
		t.Fatalf("ingest p5 flagged: %v", err)
	}
	resDynamicAfter, err := store.SearchTags(ctx, "dynamic_tag", 10)
	if err != nil {
		t.Fatalf("SearchTags dynamic_tag after transition: %v", err)
	}
	if len(resDynamicAfter) != 0 {
		t.Errorf("expected dynamic_tag to be excluded after transition to flagged, got: %+v", resDynamicAfter)
	}
}

func TestPostGroupingLinearAndCircular(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 11, 5, 20, 0, 0, 0, time.UTC)

	// 1. Linear Chain: 201 <- 202 <- 203
	linearPosts := []hypnohub.Post{
		{
			ID:          201,
			Score:       10,
			Rating:      hypnohub.RatingExplicit,
			Tags:        "comic page_1 hypnosis",
			FileURL:     "https://hypnohub.net/images/201.jpg",
			SampleURL:   "https://hypnohub.net/samples/201.jpg",
			PreviewURL:  "https://hypnohub.net/thumbnails/201.jpg",
			Width:       1000,
			Height:      1500,
			CreatedAt:   hypnohub.Date(now.Add(-3 * time.Hour)),
			ChangedAt:   hypnohub.UnixTime(now.Add(-3 * time.Hour).Unix()),
			HasChildren: true,
			Status:      "active",
		},
		{
			ID:          202,
			ParentID:    201,
			Score:       15,
			Rating:      hypnohub.RatingExplicit,
			Tags:        "comic page_2 hypnosis",
			FileURL:     "https://hypnohub.net/images/202.jpg",
			SampleURL:   "https://hypnohub.net/samples/202.jpg",
			PreviewURL:  "https://hypnohub.net/thumbnails/202.jpg",
			Width:       1000,
			Height:      1500,
			CreatedAt:   hypnohub.Date(now.Add(-2 * time.Hour)),
			ChangedAt:   hypnohub.UnixTime(now.Add(-2 * time.Hour).Unix()),
			HasChildren: true,
			Status:      "active",
		},
		{
			ID:         203,
			ParentID:   202,
			Score:      25,
			Rating:     hypnohub.RatingExplicit,
			Tags:       "comic page_3 hypnosis",
			FileURL:    "https://hypnohub.net/images/203.jpg",
			SampleURL:  "https://hypnohub.net/samples/203.jpg",
			PreviewURL: "https://hypnohub.net/thumbnails/203.jpg",
			Width:      1000,
			Height:     1500,
			CreatedAt:  hypnohub.Date(now.Add(-1 * time.Hour)),
			ChangedAt:  hypnohub.UnixTime(now.Add(-1 * time.Hour).Unix()),
			Status:     "active",
		},
	}

	if _, err := store.IngestBatch(ctx, linearPosts, now, now, now, 1, false); err != nil {
		t.Fatalf("failed to ingest linear posts: %v", err)
	}

	p201, _, err := store.GetPost(ctx, 201)
	if err != nil {
		t.Fatalf("getting post 201: %v", err)
	}
	p202, _, err := store.GetPost(ctx, 202)
	if err != nil {
		t.Fatalf("getting post 202: %v", err)
	}
	p203, _, err := store.GetPost(ctx, 203)
	if err != nil {
		t.Fatalf("getting post 203: %v", err)
	}

	for _, p := range []*IndexedPost{p201, p202, p203} {
		if p.GroupID != 201 {
			t.Errorf("post %d: expected GroupID 201, got %d", p.ID, p.GroupID)
		}
		if p.GroupPageCount != 3 {
			t.Errorf("post %d: expected GroupPageCount 3, got %d", p.ID, p.GroupPageCount)
		}
	}
	if p201.GroupIsLatest || p202.GroupIsLatest {
		t.Errorf("post 201 or 202 incorrectly marked as latest")
	}
	if !p203.GroupIsLatest {
		t.Errorf("post 203 expected to be marked as latest")
	}

	groupPosts, err := store.GetGroupPosts(ctx, 201)
	if err != nil {
		t.Fatalf("GetGroupPosts(201): %v", err)
	}
	if len(groupPosts) != 3 {
		t.Fatalf("expected 3 group posts, got %d", len(groupPosts))
	}
	if groupPosts[0].ID != 201 || groupPosts[1].ID != 202 || groupPosts[2].ID != 203 {
		t.Errorf("unexpected group posts order: [%d, %d, %d]", groupPosts[0].ID, groupPosts[1].ID, groupPosts[2].ID)
	}

	// Collapsed browse query returns only latest (203)
	resCollapsed, err := store.QueryPosts(ctx, QueryOptions{
		Window:         WindowAllTime,
		Sort:           SortNew,
		CollapseGroups: true,
		Limit:          10,
	})
	if err != nil {
		t.Fatalf("QueryPosts collapsed: %v", err)
	}
	if len(resCollapsed.Posts) != 1 || resCollapsed.Posts[0].ID != 203 {
		t.Errorf("expected 1 collapsed post (203), got %d posts", len(resCollapsed.Posts))
	}
	if resCollapsed.Posts[0].GroupPageCount != 3 {
		t.Errorf("expected collapsed post 203 to have GroupPageCount 3, got %d", resCollapsed.Posts[0].GroupPageCount)
	}

	// 2. Circular Sibling Loop: 301 -> 302 -> 303 -> 301
	circularPosts := []hypnohub.Post{
		{
			ID:          301,
			ParentID:    303,
			Score:       5,
			Rating:      hypnohub.RatingExplicit,
			Tags:        "manga cycle_1 hypnosis",
			FileURL:     "https://hypnohub.net/images/301.jpg",
			SampleURL:   "https://hypnohub.net/samples/301.jpg",
			PreviewURL:  "https://hypnohub.net/thumbnails/301.jpg",
			Width:       1000,
			Height:      1500,
			CreatedAt:   hypnohub.Date(now.Add(-6 * time.Hour)),
			ChangedAt:   hypnohub.UnixTime(now.Add(-6 * time.Hour).Unix()),
			HasChildren: true,
			Status:      "active",
		},
		{
			ID:          302,
			ParentID:    301,
			Score:       8,
			Rating:      hypnohub.RatingExplicit,
			Tags:        "manga cycle_2 hypnosis",
			FileURL:     "https://hypnohub.net/images/302.jpg",
			SampleURL:   "https://hypnohub.net/samples/302.jpg",
			PreviewURL:  "https://hypnohub.net/thumbnails/302.jpg",
			Width:       1000,
			Height:      1500,
			CreatedAt:   hypnohub.Date(now.Add(-5 * time.Hour)),
			ChangedAt:   hypnohub.UnixTime(now.Add(-5 * time.Hour).Unix()),
			HasChildren: true,
			Status:      "active",
		},
		{
			ID:          303,
			ParentID:    302,
			Score:       12,
			Rating:      hypnohub.RatingExplicit,
			Tags:        "manga cycle_3 hypnosis",
			FileURL:     "https://hypnohub.net/images/303.jpg",
			SampleURL:   "https://hypnohub.net/samples/303.jpg",
			PreviewURL:  "https://hypnohub.net/thumbnails/303.jpg",
			Width:       1000,
			Height:      1500,
			CreatedAt:   hypnohub.Date(now.Add(-4 * time.Hour)),
			ChangedAt:   hypnohub.UnixTime(now.Add(-4 * time.Hour).Unix()),
			HasChildren: true,
			Status:      "active",
		},
	}

	if _, err := store.IngestBatch(ctx, circularPosts, now, now, now, 1, false); err != nil {
		t.Fatalf("failed to ingest circular posts: %v", err)
	}

	p301, _, err := store.GetPost(ctx, 301)
	if err != nil {
		t.Fatalf("getting post 301: %v", err)
	}
	p302, _, err := store.GetPost(ctx, 302)
	if err != nil {
		t.Fatalf("getting post 302: %v", err)
	}
	p303, _, err := store.GetPost(ctx, 303)
	if err != nil {
		t.Fatalf("getting post 303: %v", err)
	}

	for _, p := range []*IndexedPost{p301, p302, p303} {
		if p.GroupID != 301 {
			t.Errorf("circular post %d: expected GroupID 301, got %d", p.ID, p.GroupID)
		}
		if p.GroupPageCount != 3 {
			t.Errorf("circular post %d: expected GroupPageCount 3, got %d", p.ID, p.GroupPageCount)
		}
	}
	if p301.GroupIsLatest || p302.GroupIsLatest {
		t.Errorf("circular post 301 or 302 incorrectly marked as latest")
	}
	if !p303.GroupIsLatest {
		t.Errorf("circular post 303 expected to be marked as latest")
	}

	groupPostsCircular, err := store.GetGroupPosts(ctx, 301)
	if err != nil {
		t.Fatalf("GetGroupPosts(301): %v", err)
	}
	if len(groupPostsCircular) != 3 {
		t.Fatalf("expected 3 circular group posts, got %d", len(groupPostsCircular))
	}
	if groupPostsCircular[0].ID != 301 || groupPostsCircular[1].ID != 302 || groupPostsCircular[2].ID != 303 {
		t.Errorf("unexpected circular group posts order: [%d, %d, %d]", groupPostsCircular[0].ID, groupPostsCircular[1].ID, groupPostsCircular[2].ID)
	}

	// 3. Self-Loop: 401.ParentID = 401
	selfLoopPost := []hypnohub.Post{
		{
			ID:         401,
			ParentID:   401,
			Score:      1,
			Rating:     hypnohub.RatingExplicit,
			Tags:       "solo hypnosis",
			FileURL:    "https://hypnohub.net/images/401.jpg",
			SampleURL:  "https://hypnohub.net/samples/401.jpg",
			PreviewURL: "https://hypnohub.net/thumbnails/401.jpg",
			Width:      1000,
			Height:     1500,
			CreatedAt:  hypnohub.Date(now.Add(-7 * time.Hour)),
			ChangedAt:  hypnohub.UnixTime(now.Add(-7 * time.Hour).Unix()),
			Status:     "active",
		},
	}
	if _, err := store.IngestBatch(ctx, selfLoopPost, now, now, now, 1, false); err != nil {
		t.Fatalf("failed to ingest self loop post: %v", err)
	}

	p401, _, err := store.GetPost(ctx, 401)
	if err != nil {
		t.Fatalf("getting post 401: %v", err)
	}
	if p401.GroupID != 0 {
		t.Errorf("self loop post: expected GroupID 0, got %d", p401.GroupID)
	}
	if p401.GroupPageCount != 1 {
		t.Errorf("self loop post: expected GroupPageCount 1, got %d", p401.GroupPageCount)
	}
	if !p401.GroupIsLatest {
		t.Errorf("self loop post: expected GroupIsLatest true")
	}
}

func TestOpenCanceledContextPreservesDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test_preserve.db"
	ctx := context.Background()

	// 1. Initialize store and write a post
	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	now := time.Now().UTC()
	_, err = store.IngestBatch(ctx, []hypnohub.Post{
		{ID: 10, Score: 5, Rating: hypnohub.RatingSafe, Status: "active", CreatedAt: hypnohub.Date(now)},
	}, now, now, now, 1, false)
	if err != nil {
		t.Fatalf("IngestBatch failed: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// 2. Call Open with a canceled context
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()

	store2, err := Open(canceledCtx, dbPath)
	if err == nil {
		store2.Close()
		t.Fatalf("expected error when opening with canceled context, got nil")
	}

	// Assert that no .bak file was created
	bakPath := dbPath + ".bak"
	if _, err := os.Stat(bakPath); err == nil {
		t.Fatalf("canceled context caused .bak file to be created!")
	}

	// 3. Opening with normal context should succeed and still have the post
	store3, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open after canceled context failed: %v", err)
	}
	defer store3.Close()

	post, _, err := store3.GetPost(ctx, 10)
	if err != nil {
		t.Fatalf("GetPost failed: %v", err)
	}
	if post.ID != 10 {
		t.Errorf("expected post ID 10, got %d", post.ID)
	}
}

func TestOpenCorruptedDatabaseBackupPreservesExisting(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/corrupt.db"
	bakPath := dbPath + ".bak"

	// Create a pre-existing .bak file
	initialBakContent := "initial-backup-content"
	if err := os.WriteFile(bakPath, []byte(initialBakContent), 0644); err != nil {
		t.Fatalf("writing initial bak: %v", err)
	}

	// Create a corrupt sqlite file
	if err := os.WriteFile(dbPath, []byte("NOT_A_VALID_SQLITE_DATABASE_HEADER_GARBAGE"), 0644); err != nil {
		t.Fatalf("writing corrupt db: %v", err)
	}

	ctx := context.Background()
	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open on corrupt db failed: %v", err)
	}
	defer store.Close()

	// Verify new healthy DB works
	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats on recreated db failed: %v", err)
	}
	if stats.TotalPosts != 0 {
		t.Errorf("expected 0 posts in clean recreated db, got %d", stats.TotalPosts)
	}

	// Verify corrupt file was moved to .bak
	corruptBakData, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("reading corrupt bak: %v", err)
	}
	if !strings.Contains(string(corruptBakData), "NOT_A_VALID_SQLITE") {
		t.Errorf("expected .bak to contain corrupt data, got %s", string(corruptBakData))
	}

	// Verify the pre-existing .bak was preserved with a timestamp suffix
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("reading tmpDir: %v", err)
	}
	foundTimestampBak := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "corrupt.db.") && strings.HasSuffix(e.Name(), ".bak") {
			content, _ := os.ReadFile(tmpDir + "/" + e.Name())
			if string(content) == initialBakContent {
				foundTimestampBak = true
				break
			}
		}
	}
	if !foundTimestampBak {
		t.Errorf("pre-existing backup was not preserved with timestamp suffix!")
	}
}

func TestPostGroupParentRemoved(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	// Step 1: Ingest A (601) and B (602, parent=601)
	batch1 := []hypnohub.Post{
		{ID: 601, Score: 10, Status: "active", HasChildren: true, CreatedAt: hypnohub.Date(now.Add(-2 * time.Hour))},
		{ID: 602, Score: 15, Status: "active", ParentID: 601, CreatedAt: hypnohub.Date(now.Add(-1 * time.Hour))},
	}
	if _, err := store.IngestBatch(ctx, batch1, now, now, now, 1, false); err != nil {
		t.Fatalf("batch 1 failed: %v", err)
	}

	p601, _, err := store.GetPost(ctx, 601)
	if err != nil || p601.GroupPageCount != 2 || p601.GroupID != 601 {
		t.Fatalf("p601 unexpected: %+v (err=%v)", p601, err)
	}
	p602, _, err := store.GetPost(ctx, 602)
	if err != nil || p602.GroupPageCount != 2 || p602.GroupID != 601 || !p602.GroupIsLatest {
		t.Fatalf("p602 unexpected: %+v (err=%v)", p602, err)
	}

	// Step 2: Re-ingest 602 with parent=0 (detached) and 601 with HasChildren=false
	batch2 := []hypnohub.Post{
		{ID: 601, Score: 10, Status: "active", HasChildren: false, CreatedAt: hypnohub.Date(now.Add(-2 * time.Hour))},
		{ID: 602, Score: 15, Status: "active", ParentID: 0, CreatedAt: hypnohub.Date(now.Add(-1 * time.Hour))},
	}
	if _, err := store.IngestBatch(ctx, batch2, now, now, now, 1, false); err != nil {
		t.Fatalf("batch 2 failed: %v", err)
	}

	p601, _, err = store.GetPost(ctx, 601)
	if err != nil || p601.GroupID != 0 || p601.GroupPageCount != 1 || !p601.GroupIsLatest {
		t.Errorf("p601 after parent removal: GroupID=%d, GroupPageCount=%d, GroupIsLatest=%v", p601.GroupID, p601.GroupPageCount, p601.GroupIsLatest)
	}
	p602, _, err = store.GetPost(ctx, 602)
	if err != nil || p602.GroupID != 0 || p602.GroupPageCount != 1 || !p602.GroupIsLatest {
		t.Errorf("p602 after parent removal: GroupID=%d, GroupPageCount=%d, GroupIsLatest=%v", p602.GroupID, p602.GroupPageCount, p602.GroupIsLatest)
	}
}

func TestPostGroupUnindexedParent(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	// B (720) and C (730) both have parent 710, where 710 is NOT indexed
	batch := []hypnohub.Post{
		{ID: 720, Score: 5, Status: "active", ParentID: 710, CreatedAt: hypnohub.Date(now.Add(-2 * time.Hour))},
		{ID: 730, Score: 8, Status: "active", ParentID: 710, CreatedAt: hypnohub.Date(now.Add(-1 * time.Hour))},
	}
	if _, err := store.IngestBatch(ctx, batch, now, now, now, 1, false); err != nil {
		t.Fatalf("IngestBatch failed: %v", err)
	}

	p720, _, err := store.GetPost(ctx, 720)
	if err != nil {
		t.Fatalf("getting p720: %v", err)
	}
	p730, _, err := store.GetPost(ctx, 730)
	if err != nil {
		t.Fatalf("getting p730: %v", err)
	}

	if p720.GroupID != 720 || p720.GroupPageCount != 2 || p720.GroupIsLatest {
		t.Errorf("p720 unexpected: GroupID=%d, PageCount=%d, Latest=%v", p720.GroupID, p720.GroupPageCount, p720.GroupIsLatest)
	}
	if p730.GroupID != 720 || p730.GroupPageCount != 2 || !p730.GroupIsLatest {
		t.Errorf("p730 unexpected: GroupID=%d, PageCount=%d, Latest=%v", p730.GroupID, p730.GroupPageCount, p730.GroupIsLatest)
	}
}

func TestPostGroupDeletedMemberExcluded(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	// Ingest A (801), B (802), C (803) in group
	batch1 := []hypnohub.Post{
		{ID: 801, Score: 5, Status: "active", HasChildren: true, CreatedAt: hypnohub.Date(now.Add(-3 * time.Hour))},
		{ID: 802, Score: 6, Status: "active", ParentID: 801, CreatedAt: hypnohub.Date(now.Add(-2 * time.Hour))},
		{ID: 803, Score: 7, Status: "active", ParentID: 801, CreatedAt: hypnohub.Date(now.Add(-1 * time.Hour))},
	}
	if _, err := store.IngestBatch(ctx, batch1, now, now, now, 1, false); err != nil {
		t.Fatalf("batch1 failed: %v", err)
	}

	p803, _, err := store.GetPost(ctx, 803)
	if err != nil || p803.GroupPageCount != 3 || !p803.GroupIsLatest {
		t.Fatalf("p803 initial: %+v", p803)
	}

	// Mark C (803) as deleted
	batch2 := []hypnohub.Post{
		{ID: 803, Score: 7, Status: "deleted", ParentID: 801, CreatedAt: hypnohub.Date(now.Add(-1 * time.Hour))},
	}
	if _, err := store.IngestBatch(ctx, batch2, now, now, now, 1, false); err != nil {
		t.Fatalf("batch2 failed: %v", err)
	}

	p801, _, err := store.GetPost(ctx, 801)
	if err != nil || p801.GroupPageCount != 2 || p801.GroupID != 801 {
		t.Errorf("p801 after member deleted: %+v", p801)
	}
	p802, _, err := store.GetPost(ctx, 802)
	if err != nil || p802.GroupPageCount != 2 || p802.GroupID != 801 || !p802.GroupIsLatest {
		t.Errorf("p802 after member deleted: %+v", p802)
	}
	gps, err := store.GetGroupPosts(ctx, 801)
	if err != nil || len(gps) != 2 {
		t.Errorf("GetGroupPosts expected 2 active posts, got %d (err=%v)", len(gps), err)
	}
}

func TestCollapseGroupsWithTagAndRatingFilters(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	// Group of 2 posts:
	// Page 1 (901) has tag "mind_control" and rating "safe"
	// Page 2 (902) has tag "hypnosis" and rating "explicit"
	batch := []hypnohub.Post{
		{
			ID:          901,
			Score:       15,
			Rating:      hypnohub.RatingSafe,
			Tags:        "mind_control comic",
			Status:      "active",
			HasChildren: true,
			CreatedAt:   hypnohub.Date(now.Add(-2 * time.Hour)),
		},
		{
			ID:        902,
			Score:     25,
			Rating:    hypnohub.RatingExplicit,
			Tags:      "hypnosis trance",
			Status:    "active",
			ParentID:  901,
			CreatedAt: hypnohub.Date(now.Add(-1 * time.Hour)),
		},
	}
	if _, err := store.IngestBatch(ctx, batch, now, now, now, 1, false); err != nil {
		t.Fatalf("IngestBatch failed: %v", err)
	}

	// 1. Tag search matching only Page 1 with CollapseGroups=true
	res, err := store.QueryPosts(ctx, QueryOptions{
		Window:         WindowAllTime,
		Sort:           SortNew,
		CollapseGroups: true,
		IncludeTags:    []string{"mind_control"},
		Now:            now,
	})
	if err != nil {
		t.Fatalf("QueryPosts failed: %v", err)
	}
	if res.Total != 1 || len(res.Posts) != 1 {
		t.Fatalf("expected 1 group returned for mind_control with CollapseGroups=true, got Total=%d, Posts=%d", res.Total, len(res.Posts))
	}
	if res.Posts[0].ID != 901 {
		t.Errorf("expected matching post 901, got %d", res.Posts[0].ID)
	}

	// 2. Rating search matching only Page 1 (safe) with CollapseGroups=true
	resRating, err := store.QueryPosts(ctx, QueryOptions{
		Window:         WindowAllTime,
		Sort:           SortNew,
		CollapseGroups: true,
		Rating:         hypnohub.RatingSafe,
		Now:            now,
	})
	if err != nil {
		t.Fatalf("QueryPosts rating failed: %v", err)
	}
	if resRating.Total != 1 || len(resRating.Posts) != 1 {
		t.Fatalf("expected 1 group returned for rating=safe with CollapseGroups=true, got Total=%d, Posts=%d", resRating.Total, len(resRating.Posts))
	}
	if resRating.Posts[0].ID != 901 {
		t.Errorf("expected post 901, got %d", resRating.Posts[0].ID)
	}
}

func TestMigrationV5ToV6HealsStaleGroups(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "v5_to_v6.db")

	// 1. Prepare v5 schema by taking first 5 versions from schemaSQL
	versions := strings.Split(schemaSQL, "-- NEW VERSION --")
	if len(versions) < 6 {
		t.Fatalf("expected at least 6 versions in schemaSQL, got %d", len(versions))
	}
	v5Schema := strings.Join(versions[:5], "-- NEW VERSION --")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("opening raw db: %v", err)
	}
	if err := lazymigrate.Migrate(ctx, db, v5Schema); err != nil {
		db.Close()
		t.Fatalf("migrating to v5: %v", err)
	}

	var userVersion int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&userVersion); err != nil {
		db.Close()
		t.Fatalf("querying user_version: %v", err)
	}
	if userVersion != 5 {
		db.Close()
		t.Fatalf("expected user_version 5, got %d", userVersion)
	}

	// Insert dummy pools and pool_posts to ensure they exist at v5
	_, err = db.ExecContext(ctx, `
		INSERT INTO pools (id, name, updated_at) VALUES (1, 'Test Pool', 123456);
		INSERT INTO pool_posts (pool_id, post_id, seq) VALUES (1, 101, 1);
	`)
	if err != nil {
		db.Close()
		t.Fatalf("inserting into pools/pool_posts: %v", err)
	}

	// Insert posts with stale/erroneous grouping values
	nowUnix := time.Now().Unix()
	insertQuery := `
		INSERT INTO posts (
			id, created_at, changed_at, score, rating, creator_id, parent_id,
			md5, source, status, has_notes, has_comments, has_children,
			file_url, file_ext, width, height, sample_url, sample_width, sample_height,
			preview_url, preview_width, preview_height, tags_raw, first_seen_at, last_polled_at,
			group_id, group_is_latest, group_page_count
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	// 101 & 102 are parent/child, but set with stale group_id 999 and count 5
	_, err = db.ExecContext(ctx, insertQuery,
		101, nowUnix-200, nowUnix-200, 10, "s", 1, 0,
		"md5_101", "", "active", 0, 0, 1,
		"https://example.com/101.jpg", "jpg", 100, 100, "", 0, 0,
		"", 0, 0, "test", nowUnix, nowUnix,
		999, 0, 5,
	)
	if err != nil {
		db.Close()
		t.Fatalf("inserting post 101: %v", err)
	}

	_, err = db.ExecContext(ctx, insertQuery,
		102, nowUnix-100, nowUnix-100, 15, "s", 1, 101,
		"md5_102", "", "active", 0, 0, 0,
		"https://example.com/102.jpg", "jpg", 100, 100, "", 0, 0,
		"", 0, 0, "test", nowUnix, nowUnix,
		999, 0, 5,
	)
	if err != nil {
		db.Close()
		t.Fatalf("inserting post 102: %v", err)
	}

	// 103 is standalone active post with stale group_id 888
	_, err = db.ExecContext(ctx, insertQuery,
		103, nowUnix-50, nowUnix-50, 5, "s", 1, 0,
		"md5_103", "", "active", 0, 0, 0,
		"https://example.com/103.jpg", "jpg", 100, 100, "", 0, 0,
		"", 0, 0, "test", nowUnix, nowUnix,
		888, 0, 3,
	)
	if err != nil {
		db.Close()
		t.Fatalf("inserting post 103: %v", err)
	}

	// 104 is deleted child of 101, should be reset and excluded from active group
	_, err = db.ExecContext(ctx, insertQuery,
		104, nowUnix-10, nowUnix-10, 0, "s", 1, 101,
		"md5_104", "", "deleted", 0, 0, 0,
		"https://example.com/104.jpg", "jpg", 100, 100, "", 0, 0,
		"", 0, 0, "test", nowUnix, nowUnix,
		101, 1, 2,
	)
	if err != nil {
		db.Close()
		t.Fatalf("inserting post 104: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("closing raw db: %v", err)
	}

	// 2. Open store with index.Open, triggering migration from v5 to v6
	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open failed migrating v5 to v6: %v", err)
	}
	defer store.Close()

	// Verify user_version is at least 6 (latest schema is 7)
	if err := store.DB().QueryRowContext(ctx, "PRAGMA user_version").Scan(&userVersion); err != nil {
		t.Fatalf("querying user_version after migration: %v", err)
	}
	if userVersion < 6 {
		t.Errorf("expected user_version >= 6, got %d", userVersion)
	}

	// Verify pools and pool_posts are dropped
	var tableCount int
	err = store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name IN ('pools', 'pool_posts')").Scan(&tableCount)
	if err != nil {
		t.Fatalf("checking tables: %v", err)
	}
	if tableCount != 0 {
		t.Errorf("expected pools and pool_posts to be dropped, found %d", tableCount)
	}

	// Verify posts grouping healed
	p101, _, err := store.GetPost(ctx, 101)
	if err != nil {
		t.Fatalf("getting post 101: %v", err)
	}
	if p101.GroupID != 101 || p101.GroupPageCount != 2 || p101.GroupIsLatest {
		t.Errorf("post 101: GroupID=%d, PageCount=%d, Latest=%v", p101.GroupID, p101.GroupPageCount, p101.GroupIsLatest)
	}

	p102, _, err := store.GetPost(ctx, 102)
	if err != nil {
		t.Fatalf("getting post 102: %v", err)
	}
	if p102.GroupID != 101 || p102.GroupPageCount != 2 || !p102.GroupIsLatest {
		t.Errorf("post 102: GroupID=%d, PageCount=%d, Latest=%v", p102.GroupID, p102.GroupPageCount, p102.GroupIsLatest)
	}

	p103, _, err := store.GetPost(ctx, 103)
	if err != nil {
		t.Fatalf("getting post 103: %v", err)
	}
	if p103.GroupID != 0 || p103.GroupPageCount != 1 || !p103.GroupIsLatest {
		t.Errorf("post 103: GroupID=%d, PageCount=%d, Latest=%v", p103.GroupID, p103.GroupPageCount, p103.GroupIsLatest)
	}

	p104, _, err := store.GetPost(ctx, 104)
	if err != nil {
		t.Fatalf("getting post 104: %v", err)
	}
	if p104.GroupID != 0 || p104.GroupPageCount != 1 || p104.GroupIsLatest {
		t.Errorf("post 104 (deleted): GroupID=%d, PageCount=%d, Latest=%v", p104.GroupID, p104.GroupPageCount, p104.GroupIsLatest)
	}
}

func TestPostGroupLateArrivingParent(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()

	// 1. Ingest child post first: ParentID = 1001, but 1001 is NOT yet in DB
	childBatch := []hypnohub.Post{
		{
			ID:        1002,
			ParentID:  1001,
			Score:     20,
			Rating:    hypnohub.RatingExplicit,
			Tags:      "comic hypnosis",
			Status:    "active",
			CreatedAt: hypnohub.Date(now.Add(-1 * time.Hour)),
		},
	}
	if _, err := store.IngestBatch(ctx, childBatch, now, now, now, 1, false); err != nil {
		t.Fatalf("IngestBatch childBatch failed: %v", err)
	}

	p1002, _, err := store.GetPost(ctx, 1002)
	if err != nil {
		t.Fatalf("getting post 1002: %v", err)
	}
	// With parent unindexed and no siblings, 1002 is standalone
	if p1002.GroupID != 0 || p1002.GroupPageCount != 1 || !p1002.GroupIsLatest {
		t.Errorf("post 1002 initial: GroupID=%d, PageCount=%d, Latest=%v", p1002.GroupID, p1002.GroupPageCount, p1002.GroupIsLatest)
	}

	// 2. Late-arriving parent post 1001 is ingested
	parentBatch := []hypnohub.Post{
		{
			ID:          1001,
			ParentID:    0,
			HasChildren: true,
			Score:       10,
			Rating:      hypnohub.RatingExplicit,
			Tags:        "comic hypnosis",
			Status:      "active",
			CreatedAt:   hypnohub.Date(now.Add(-2 * time.Hour)),
		},
	}
	if _, err := store.IngestBatch(ctx, parentBatch, now, now, now, 1, false); err != nil {
		t.Fatalf("IngestBatch parentBatch failed: %v", err)
	}

	// 3. Both posts should now be linked into group 1001 with count 2
	p1001, _, err := store.GetPost(ctx, 1001)
	if err != nil {
		t.Fatalf("getting post 1001: %v", err)
	}
	if p1001.GroupID != 1001 || p1001.GroupPageCount != 2 || p1001.GroupIsLatest {
		t.Errorf("post 1001 after parent arrived: GroupID=%d, PageCount=%d, Latest=%v", p1001.GroupID, p1001.GroupPageCount, p1001.GroupIsLatest)
	}

	p1002, _, err = store.GetPost(ctx, 1002)
	if err != nil {
		t.Fatalf("getting post 1002 after parent arrived: %v", err)
	}
	if p1002.GroupID != 1001 || p1002.GroupPageCount != 2 || !p1002.GroupIsLatest {
		t.Errorf("post 1002 after parent arrived: GroupID=%d, PageCount=%d, Latest=%v", p1002.GroupID, p1002.GroupPageCount, p1002.GroupIsLatest)
	}

	groupPosts, err := store.GetGroupPosts(ctx, 1001)
	if err != nil {
		t.Fatalf("GetGroupPosts(1001) failed: %v", err)
	}
	if len(groupPosts) != 2 {
		t.Fatalf("expected 2 group posts, got %d", len(groupPosts))
	}
	if groupPosts[0].ID != 1001 || groupPosts[1].ID != 1002 {
		t.Errorf("unexpected group posts: [%d, %d]", groupPosts[0].ID, groupPosts[1].ID)
	}
}

func TestBuildTagGroupsOrdering(t *testing.T) {
	input := []PostTag{
		{Name: "text", Category: hypnohub.TagTypeMeta, Count: 500},
		{Name: "hypnosis", Category: hypnohub.TagTypeGeneral, Count: 1000},
		{Name: "armpits", Category: hypnohub.TagTypeUnknown, Count: 200},
		{Name: "thedrawing", Category: hypnohub.TagTypeArtist, Count: 43},
		{Name: "emma_frost", Category: hypnohub.TagTypeCharacter, Count: 353},
		{Name: "psylocke", Category: hypnohub.TagTypeCharacter, Count: 120},
		{Name: "marvel_rivals", Category: hypnohub.TagTypeCopyright, Count: 553},
		{Name: "marvel_comics", Category: hypnohub.TagTypeCopyright, Count: 3284},
		{Name: "marvel_comics", Category: hypnohub.TagTypeCopyright, Count: 3284}, // duplicate
		{Name: "comic", Category: hypnohub.TagTypeMeta, Count: 400},
	}

	groups := BuildTagGroups(input)
	if len(groups) != 5 {
		t.Fatalf("expected 5 groups, got %d: %+v", len(groups), groups)
	}

	wantOrder := []struct {
		cat   hypnohub.TagType
		label string
		slug  string
		tags  []string
	}{
		{hypnohub.TagTypeCopyright, "Copyright", "copyright", []string{"marvel_comics", "marvel_rivals"}},
		{hypnohub.TagTypeCharacter, "Character", "character", []string{"emma_frost", "psylocke"}},
		{hypnohub.TagTypeArtist, "Artist", "artist", []string{"thedrawing"}},
		{hypnohub.TagTypeGeneral, "General", "general", []string{"armpits", "hypnosis"}},
		{hypnohub.TagTypeMeta, "Meta", "meta", []string{"comic", "text"}},
	}

	for i, w := range wantOrder {
		g := groups[i]
		if g.Category != w.cat || g.Label != w.label || g.Slug != w.slug {
			t.Errorf("group[%d] = (%v, %q, %q), want (%v, %q, %q)", i, g.Category, g.Label, g.Slug, w.cat, w.label, w.slug)
		}
		if len(g.Tags) != len(w.tags) {
			t.Fatalf("group[%d] (%s) tags len = %d, want %d (%v)", i, g.Label, len(g.Tags), len(w.tags), g.Tags)
		}
		for j, wantTag := range w.tags {
			if g.Tags[j].Name != wantTag {
				t.Errorf("group[%d].Tags[%d] = %q, want %q", i, j, g.Tags[j].Name, wantTag)
			}
		}
	}
}

func TestMigrationV6ToV7AndOnDemandTagResolution(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "v6_to_v7.db")

	versions := strings.Split(schemaSQL, "-- NEW VERSION --")
	if len(versions) < 7 {
		t.Fatalf("expected at least 7 versions in schemaSQL, got %d", len(versions))
	}
	v6Schema := strings.Join(versions[:6], "-- NEW VERSION --")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("opening raw db: %v", err)
	}
	if err := lazymigrate.Migrate(ctx, db, v6Schema); err != nil {
		db.Close()
		t.Fatalf("migrating to v6: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO tags (id, name, post_count) VALUES (1, 'bleach', 10), (2, 'orihime_inoue', 5);`)
	if err != nil {
		db.Close()
		t.Fatalf("inserting v6 tags: %v", err)
	}
	db.Close()

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open migrating v6->v7 failed: %v", err)
	}
	defer store.Close()

	var userVersion int
	if err := store.DB().QueryRowContext(ctx, "PRAGMA user_version").Scan(&userVersion); err != nil {
		t.Fatalf("querying user_version: %v", err)
	}
	if userVersion < 7 {
		t.Errorf("expected user_version >= 7, got %d", userVersion)
	}

	var cat int
	if err := store.DB().QueryRowContext(ctx, "SELECT category FROM tags WHERE name = 'bleach'").Scan(&cat); err != nil {
		t.Fatalf("selecting bleach category: %v", err)
	}
	if cat != -1 {
		t.Errorf("expected default category -1 after v6->v7 migration, got %d", cat)
	}

	// Set up mock upstream server for on-demand resolution
	postViewHits := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("page") == "post" && q.Get("s") == "view" && q.Get("id") == "287547" {
			postViewHits++
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<ul id="tag-sidebar">
				<li class="tag-type-copyright tag"><a href="?">?</a><a href="#">bleach</a></li>
				<li class="tag-type-character tag"><a href="?">?</a><a href="#">orihime inoue</a></li>
				<li class="tag-type-artist tag"><a href="?">?</a><a href="#">kubotite</a></li>
				<li class="tag-type-general tag"><a href="?">?</a><a href="#">hypnosis</a></li>
				<li class="tag-type-metadata tag"><a href="?">?</a><a href="#">comic</a></li>
			</ul>`))
			return
		}
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><tags type="array"></tags>`))
	}))
	defer ts.Close()

	client := hypnohub.FromHTTPClient(ts.Client())
	client.BaseURL = ts.URL
	store.SetClient(client)

	now := time.Now().UTC()
	posts := []hypnohub.Post{
		{
			ID:        287547,
			Score:     42,
			Rating:    hypnohub.RatingExplicit,
			Tags:      "comic hypnosis kubotite orihime_inoue bleach uncategorized_extra",
			Status:    "active",
			CreatedAt: hypnohub.Date(now.Add(-1 * time.Hour)),
		},
	}
	if _, err := store.IngestBatch(ctx, posts, now, now, now, 1, false); err != nil {
		t.Fatalf("IngestBatch failed: %v", err)
	}

	// First GetPost resolves categories via PostViewTags (1 request) and persists them
	p, _, err := store.GetPost(ctx, 287547)
	if err != nil {
		t.Fatalf("GetPost(287547) failed: %v", err)
	}
	if postViewHits != 1 {
		t.Errorf("expected 1 PostViewTags request, got %d", postViewHits)
	}
	if len(p.TagGroups) != 5 {
		t.Fatalf("expected 5 TagGroups, got %d: %+v", len(p.TagGroups), p.TagGroups)
	}
	if p.TagGroups[0].Slug != "copyright" || p.TagGroups[1].Slug != "character" || p.TagGroups[2].Slug != "artist" || p.TagGroups[3].Slug != "general" || p.TagGroups[4].Slug != "meta" {
		t.Errorf("unexpected TagGroups order: %+v", p.TagGroups)
	}

	// Second GetPost should NOT hit upstream again because all tags on the post are now >= 0
	_, _, err = store.GetPost(ctx, 287547)
	if err != nil {
		t.Fatalf("second GetPost(287547) failed: %v", err)
	}
	if postViewHits != 1 {
		t.Errorf("expected second GetPost to use cached SQLite categories without HTTP call, got hits=%d", postViewHits)
	}
}

func TestTagResolutionOverwriteProtection(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	posts := []hypnohub.Post{
		{
			ID:        9001,
			Score:     10,
			Rating:    hypnohub.RatingExplicit,
			Tags:      "famous_artist new_tag ghost_tag",
			Status:    "active",
			CreatedAt: hypnohub.Date(now),
		},
	}
	if _, err := store.IngestBatch(ctx, posts, now, now, now, 1, false); err != nil {
		t.Fatalf("IngestBatch failed: %v", err)
	}

	// Mark famous_artist as Artist (1), while new_tag and ghost_tag remain Unknown (-1).
	if _, err := store.persistTagCategories(ctx, map[string]hypnohub.TagType{
		"famous_artist": hypnohub.TagTypeArtist,
	}); err != nil {
		t.Fatalf("persistTagCategories failed: %v", err)
	}

	var upstreamHits atomic.Int32
	// Upstream scrape returns only new_tag (Character), omits famous_artist, and ghost_tag is absent from both sidebar and DAPI.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		q := r.URL.Query()
		if q.Get("page") == "post" && q.Get("s") == "view" && q.Get("id") == "9001" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<ul id="tag-sidebar">
				<li class="tag-type-character tag"><a href="index.php?page=post&s=list&tags=new_tag">new tag</a></li>
			</ul>`))
			return
		}
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><tags type="array"></tags>`))
	}))
	defer ts.Close()

	client := hypnohub.FromHTTPClient(ts.Client())
	client.BaseURL = ts.URL
	store.SetClient(client)

	p, _, err := store.GetPost(ctx, 9001)
	if err != nil {
		t.Fatalf("GetPost(9001) failed: %v", err)
	}

	var artistCat, newTagCat, ghostCat int
	if err := store.DB().QueryRowContext(ctx, "SELECT category FROM tags WHERE name = 'famous_artist'").Scan(&artistCat); err != nil {
		t.Fatalf("querying famous_artist category: %v", err)
	}
	if artistCat != int(hypnohub.TagTypeArtist) {
		t.Errorf("famous_artist category = %d, want %d (TagTypeArtist); must not be overwritten!", artistCat, hypnohub.TagTypeArtist)
	}
	if err := store.DB().QueryRowContext(ctx, "SELECT category FROM tags WHERE name = 'new_tag'").Scan(&newTagCat); err != nil {
		t.Fatalf("querying new_tag category: %v", err)
	}
	if newTagCat != int(hypnohub.TagTypeCharacter) {
		t.Errorf("new_tag category = %d, want %d (TagTypeCharacter)", newTagCat, hypnohub.TagTypeCharacter)
	}
	if err := store.DB().QueryRowContext(ctx, "SELECT category FROM tags WHERE name = 'ghost_tag'").Scan(&ghostCat); err != nil {
		t.Fatalf("querying ghost_tag category: %v", err)
	}
	if ghostCat != int(hypnohub.TagTypeGeneral) {
		t.Errorf("ghost_tag category = %d, want %d (TagTypeGeneral)", ghostCat, hypnohub.TagTypeGeneral)
	}
	if len(p.TagGroups) != 3 || p.TagGroups[0].Slug != "character" || p.TagGroups[1].Slug != "artist" || p.TagGroups[2].Slug != "general" {
		t.Errorf("expected Character, Artist, and General groups on post, got %+v", p.TagGroups)
	}

	// Second view of 9001 must make 0 upstream requests because ghost_tag is now General (0).
	upstreamHits.Store(0)
	if _, _, err := store.GetPost(ctx, 9001); err != nil {
		t.Fatalf("second GetPost(9001) failed: %v", err)
	}
	if hits := upstreamHits.Load(); hits != 0 {
		t.Errorf("expected 0 upstream requests on second GetPost(9001), got %d", hits)
	}
}

func TestTagResolutionSingleflightAndBackoff(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	posts := []hypnohub.Post{
		{
			ID:        9002,
			Score:     10,
			Rating:    hypnohub.RatingExplicit,
			Tags:      "tag_one",
			Status:    "active",
			CreatedAt: hypnohub.Date(now),
		},
		{
			ID:        9003,
			Score:     10,
			Rating:    hypnohub.RatingExplicit,
			Tags:      "tag_two",
			Status:    "active",
			CreatedAt: hypnohub.Date(now),
		},
		{
			ID:        9004,
			Score:     10,
			Rating:    hypnohub.RatingExplicit,
			Tags:      "tag_three",
			Status:    "active",
			CreatedAt: hypnohub.Date(now),
		},
		{
			ID:        9005,
			Score:     10,
			Rating:    hypnohub.RatingExplicit,
			Tags:      "tag_four",
			Status:    "active",
			CreatedAt: hypnohub.Date(now),
		},
	}
	if _, err := store.IngestBatch(ctx, posts, now, now, now, 1, false); err != nil {
		t.Fatalf("IngestBatch failed: %v", err)
	}

	var scrapeCalls atomic.Int32
	var failMode atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("page") == "post" && q.Get("s") == "view" {
			scrapeCalls.Add(1)
			if q.Get("id") == "9004" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if q.Get("id") == "9005" {
				time.Sleep(80 * time.Millisecond)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if failMode.Load() {
				time.Sleep(40 * time.Millisecond)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			time.Sleep(40 * time.Millisecond)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<ul id="tag-sidebar">
				<li class="tag-type-copyright tag"><a href="index.php?page=post&s=list&tags=tag_one">tag one</a></li>
				<li class="tag-type-copyright tag"><a href="index.php?page=post&s=list&tags=tag_four">tag four</a></li>
			</ul>`))
			return
		}
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><tags type="array"></tags>`))
	}))
	defer ts.Close()

	client := hypnohub.FromHTTPClient(ts.Client())
	client.BaseURL = ts.URL
	store.SetClient(client)

	// 1. Fire 8 concurrent GetPost(9002) calls -> must coalesce into 1 upstream request.
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, _, err := store.GetPost(ctx, 9002)
			if err != nil {
				t.Errorf("concurrent GetPost(9002) failed: %v", err)
				return
			}
			if len(p.TagGroups) != 1 || p.TagGroups[0].Slug != "copyright" {
				t.Errorf("concurrent GetPost(9002) unexpected TagGroups: %+v", p.TagGroups)
			}
		}()
	}
	wg.Wait()

	if got := scrapeCalls.Load(); got != 1 {
		t.Errorf("expected singleflight to make 1 upstream request across 8 concurrent GetPost calls, got %d", got)
	}

	// 2. PostViewTags returning HTTP 404 (post 9004) must NOT trigger global backoff.
	if _, _, err := store.GetPost(ctx, 9004); err != nil {
		t.Fatalf("GetPost(9004) 404 post failed: %v", err)
	}
	if store.scrapeRetryAt.Load() != 0 {
		t.Errorf("expected HTTP 404 on PostViewTags NOT to set scrapeRetryAt backoff, got %d", store.scrapeRetryAt.Load())
	}

	// 3. Caller cancelling ctx at 50ms mid-scrape must NOT trigger global backoff for subsequent callers.
	cancelCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, _, _ = store.GetPost(cancelCtx, 9005)
	if store.scrapeRetryAt.Load() != 0 {
		t.Errorf("expected cancelled caller ctx NOT to set scrapeRetryAt backoff, got %d", store.scrapeRetryAt.Load())
	}

	// 4. Enable failure mode with non-cancelled ctx on post 9003 -> sets 1-minute backoff so subsequent calls return in <10ms without hitting upstream.
	failMode.Store(true)
	scrapeCalls.Store(0)

	_, _, err = store.GetPost(ctx, 9003)
	if err != nil {
		t.Fatalf("GetPost(9003) on upstream failure failed: %v", err)
	}
	if got := scrapeCalls.Load(); got != 1 {
		t.Errorf("expected 1 upstream call before backoff engaged, got %d", got)
	}

	start := time.Now()
	_, _, err = store.GetPost(ctx, 9003)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("second GetPost(9003) during backoff failed: %v", err)
	}
	if elapsed > 10*time.Millisecond {
		t.Errorf("expected GetPost during backoff to return in <10ms, took %v", elapsed)
	}
	if got := scrapeCalls.Load(); got != 1 {
		t.Errorf("expected 0 additional upstream calls during backoff, total calls = %d", got)
	}
}

func TestResolveUnknownTagCategoriesResilience(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	// Seed bad_tag on 2 posts (post_count=2) and good_tag + absent_tag on 1 post (post_count=1).
	// Order is random, so assertions must not depend on which tag is looked up first.
	posts := []hypnohub.Post{
		{ID: 1, Score: 5, Rating: hypnohub.RatingSafe, Tags: "bad_tag good_tag absent_tag", Status: "active", CreatedAt: hypnohub.Date(now)},
		{ID: 2, Score: 5, Rating: hypnohub.RatingSafe, Tags: "bad_tag", Status: "active", CreatedAt: hypnohub.Date(now)},
	}
	if _, err := store.IngestBatch(ctx, posts, now, now, now, 1, false); err != nil {
		t.Fatalf("IngestBatch failed: %v", err)
	}

	var seenBulkPIDs []string
	var failBulk atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("orderby") == "count" {
			seenBulkPIDs = append(seenBulkPIDs, q.Get("pid"))
			if failBulk.Load() {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/xml; charset=utf-8")
			if q.Get("pid") == "0" {
				// Return tags NOT in unknownSet on page 0 so updates is not polluted by unrelated tags.
				_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><tags type="array">
					<tag type="3" count="999" name="unrelated_tag_1" ambiguous="false" id="1"/>
					<tag type="4" count="888" name="unrelated_tag_2" ambiguous="false" id="2"/>
				</tags>`))
				return
			}
			// Page 1 returns empty -> resets tagSweepPage to 0.
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><tags type="array"></tags>`))
			return
		}
		if q.Get("name") == "bad_tag" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if q.Get("name") == "good_tag" {
			w.Header().Set("Content-Type", "text/xml; charset=utf-8")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><tags type="array"><tag type="1" count="50" name="good_tag" ambiguous="false" id="777"/></tags>`))
			return
		}
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><tags type="array"></tags>`))
	}))
	defer ts.Close()

	client := hypnohub.FromHTTPClient(ts.Client())
	client.BaseURL = ts.URL
	store.SetClient(client)

	resolved, err := store.ResolveUnknownTagCategories(ctx, 3)
	if err != nil {
		t.Fatalf("ResolveUnknownTagCategories failed: %v", err)
	}
	if resolved != 1 {
		t.Errorf("expected 1 resolved tag (good_tag), got %d", resolved)
	}
	if store.tagSweepPage.Load() != 1 {
		t.Errorf("expected tagSweepPage = 1 after first call, got %d", store.tagSweepPage.Load())
	}

	var goodCat, absentCat, badCat int
	if err := store.DB().QueryRowContext(ctx, "SELECT category FROM tags WHERE name = 'good_tag'").Scan(&goodCat); err != nil {
		t.Fatalf("querying good_tag category: %v", err)
	}
	if goodCat != int(hypnohub.TagTypeArtist) {
		t.Errorf("good_tag category = %d, want %d (TagTypeArtist)", goodCat, hypnohub.TagTypeArtist)
	}
	if err := store.DB().QueryRowContext(ctx, "SELECT category FROM tags WHERE name = 'absent_tag'").Scan(&absentCat); err != nil {
		t.Fatalf("querying absent_tag category: %v", err)
	}
	if absentCat != int(hypnohub.TagTypeUnknown) {
		t.Errorf("absent_tag category = %d, want %d (TagTypeUnknown)", absentCat, hypnohub.TagTypeUnknown)
	}
	if err := store.DB().QueryRowContext(ctx, "SELECT category FROM tags WHERE name = 'bad_tag'").Scan(&badCat); err != nil {
		t.Fatalf("querying bad_tag category: %v", err)
	}
	if badCat != int(hypnohub.TagTypeUnknown) {
		t.Errorf("bad_tag category = %d, want %d (TagTypeUnknown)", badCat, hypnohub.TagTypeUnknown)
	}

	// Second call fetches page 1 (which returns empty) and resets tagSweepPage to 0.
	_, _ = store.ResolveUnknownTagCategories(ctx, 3)
	if len(seenBulkPIDs) != 2 || seenBulkPIDs[0] != "0" || seenBulkPIDs[1] != "1" {
		t.Errorf("expected bulk PIDs [0, 1], got %v", seenBulkPIDs)
	}
	if store.tagSweepPage.Load() != 0 {
		t.Errorf("expected tagSweepPage reset to 0 after empty bulk page, got %d", store.tagSweepPage.Load())
	}

	// Third call with failing ListTagsPage keeps tagSweepPage on page 0 so the next cycle retries page 0.
	failBulk.Store(true)
	_, _ = store.ResolveUnknownTagCategories(ctx, 3)
	if store.tagSweepPage.Load() != 0 {
		t.Errorf("expected tagSweepPage to remain 0 after failed ListTagsPage, got %d", store.tagSweepPage.Load())
	}
	failBulk.Store(false)
	_, _ = store.ResolveUnknownTagCategories(ctx, 3)
	if len(seenBulkPIDs) != 4 || seenBulkPIDs[2] != "0" || seenBulkPIDs[3] != "0" {
		t.Errorf("expected failed page 0 to be retried on next cycle, got PIDs %v", seenBulkPIDs)
	}
	if store.tagSweepPage.Load() != 1 {
		t.Errorf("expected tagSweepPage = 1 after retrying page 0, got %d", store.tagSweepPage.Load())
	}
}

func TestResolveUnknownTagCategoriesBulkThroughput(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	for i := range 2000 {
		name := fmt.Sprintf("bulk_tag_%04d", i)
		if _, err := tx.ExecContext(ctx, `INSERT INTO tags (name, post_count, category) VALUES (?, 5, -1)`, name); err != nil {
			t.Fatalf("inserting %s: %v", name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	var bulkBody strings.Builder
	bulkBody.WriteString(`<?xml version="1.0" encoding="UTF-8"?><tags type="array">`)
	for i := range 1000 {
		fmt.Fprintf(&bulkBody, `<tag type="1" count="50" name="bulk_tag_%04d" ambiguous="false" id="%d"/>`, i, i+1)
	}
	bulkBody.WriteString(`</tags>`)
	bulkXML := []byte(bulkBody.String())

	var singleLookups atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		if q.Get("s") == "tag" && q.Get("q") == "index" && q.Get("orderby") == "count" && q.Get("limit") == "1000" && q.Get("pid") == "0" {
			_, _ = w.Write(bulkXML)
			return
		}
		if q.Get("name") != "" {
			singleLookups.Add(1)
		}
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><tags type="array"/>`))
	}))
	defer ts.Close()

	client := hypnohub.FromHTTPClient(ts.Client())
	client.BaseURL = ts.URL
	store.SetClient(client)

	resolved, err := store.ResolveUnknownTagCategories(ctx, 20)
	if err != nil {
		t.Fatalf("ResolveUnknownTagCategories failed: %v", err)
	}
	if resolved < 1000 {
		t.Errorf("expected resolved >= 1000 from bulk sweep, got %d", resolved)
	}
	if got := singleLookups.Load(); got > 20 {
		t.Errorf("expected at most 20 single-tag name= lookups, got %d", got)
	}
}
