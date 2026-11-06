package index

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"libdb.so/hypnoview"
	"libdb.so/hypnoview/internal/index/sqlcgen"
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
	t0 := time.Now()
	refNow := store.latestReferenceTime(ctx)
	tRef := time.Since(t0)

	t1 := time.Now()
	total, err := store.q.CountPostsAll(ctx, sqlcgen.CountPostsAllParams{
		Rating:    "",
		MediaType: "",
	})
	tCount := time.Since(t1)

	t2 := time.Now()
	rows, err := store.q.ListPostsAllSortNew(ctx, sqlcgen.ListPostsAllSortNewParams{
		NowUnix:   refNow.Unix(),
		Tau:       168.0,
		Rating:    "",
		MediaType: "",
		Limit:     48,
		Offset:    0,
	})
	tQuery := time.Since(t2)

	t.Logf("Breakdown: refNow=%s, CountPostsAll=%s (total=%d), ListPostsAllSortNew=%s (rows=%d)",
		tRef, tCount, total, tQuery, len(rows))

	start := time.Now()
	resBrowse, err := store.QueryPosts(ctx, QueryOptions{
		Window: WindowAllTime,
		Sort:   SortNew,
		Limit:  48,
	})
	elapsedBrowse := time.Since(start)
	if err != nil {
		t.Fatalf("QueryPosts(all, new) failed: %v", err)
	}
	t.Logf("QueryPosts(all, new) [Browse homepage]: %s, returned %d posts (total: %d)",
		elapsedBrowse, len(resBrowse.Posts), resBrowse.Total)
	if len(resBrowse.Posts) == 0 {
		t.Errorf("expected posts returned for Browse feed")
	}

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
