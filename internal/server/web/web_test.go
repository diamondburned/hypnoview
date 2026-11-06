package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gaze.d14.pet"
	"gaze.d14.pet/internal/index"
)

func TestWebFeeds(t *testing.T) {
	ctx := context.Background()
	store, err := index.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 11, 5, 20, 0, 0, 0, time.UTC)
	posts := []hypnohub.Post{
		{
			ID:            501,
			Score:         24,
			Rating:        hypnohub.RatingExplicit,
			Tags:          "hypnosis spiral animated",
			FileURL:       "https://hypnohub.net/images/501.mp4",
			SampleURL:     "https://hypnohub.net/samples/501.jpg",
			PreviewURL:    "https://hypnohub.net/thumbnails/501.jpg",
			Width:         1920,
			Height:        1080,
			PreviewWidth:  250,
			PreviewHeight: 140,
			CreatedAt:     hypnohub.Date(now.Add(-2 * time.Hour)),
			ChangedAt:     hypnohub.UnixTime(now.Add(-2 * time.Hour).Unix()),
			Status:        "active",
		},
		{
			ID:            500,
			Score:         30,
			Rating:        hypnohub.RatingSafe,
			Tags:          "hypnosis comic",
			FileURL:       "https://hypnohub.net/images/500.png",
			SampleURL:     "https://hypnohub.net/samples/500.jpg",
			PreviewURL:    "https://hypnohub.net/thumbnails/500.jpg",
			Width:         1000,
			Height:        1200,
			PreviewWidth:  208,
			PreviewHeight: 250,
			CreatedAt:     hypnohub.Date(now.Add(-20 * time.Hour)),
			ChangedAt:     hypnohub.UnixTime(now.Add(-20 * time.Hour).Unix()),
			Status:        "active",
		},
	}

	if _, err := store.IngestBatch(ctx, posts, now, now, now, 1, true); err != nil {
		t.Fatalf("IngestBatch failed: %v", err)
	}

	srv := NewServer(store, nil)

	tests := []struct {
		name          string
		path          string
		wantCode      int
		wantSubstrs   []string
		unwantSubstrs []string
	}{
		{
			name:     "browse feed defaults to all window and new sort",
			path:     "/",
			wantCode: http.StatusOK,
			wantSubstrs: []string{
				`aria-current="page">Browse</a>`,
				`action="/"`,
				`Post #501`,
				`Post #500`,
			},
			unwantSubstrs: []string{
				`aria-current="page">Popular</a>`,
			},
		},
		{
			name:     "popular feed defaults to 24h window and hot sort",
			path:     "/popular",
			wantCode: http.StatusOK,
			wantSubstrs: []string{
				`aria-current="page">Popular</a>`,
				`action="/popular"`,
				`Post #501`,
			},
			unwantSubstrs: []string{
				`aria-current="page">Browse</a>`,
			},
		},
		{
			name:     "post detail view",
			path:     "/posts/501",
			wantCode: http.StatusOK,
			wantSubstrs: []string{
				`← Back to Feed`,
				`Post #501`,
				`https://hypnohub.net/images/501.mp4`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("GET %q returned code %d, want %d", tt.path, rec.Code, tt.wantCode)
			}

			body := rec.Body.String()
			if strings.Contains(body, "<svg") {
				t.Errorf("GET %q body contains unexpected inline <svg>", tt.path)
			}
			for _, substr := range tt.wantSubstrs {
				if !strings.Contains(body, substr) {
					t.Errorf("GET %q body missing expected substring %q", tt.path, substr)
				}
			}
			for _, substr := range tt.unwantSubstrs {
				if strings.Contains(body, substr) {
					t.Errorf("GET %q body contains unexpected substring %q", tt.path, substr)
				}
			}
		})
	}
}

func TestIndexTagURLHelpers(t *testing.T) {
	data := indexPageData{
		FormAction: "/popular",
		Window:     "24h",
		Sort:       "hot",
		TagsQuery:  "hypnosis animated",
	}

	if !data.IsTagActive("hypnosis") {
		t.Errorf("expected hypnosis to be active")
	}
	if !data.IsTagActive("HYPNOSIS") {
		t.Errorf("expected case-insensitive match for HYPNOSIS")
	}
	if data.IsTagActive("spiral") {
		t.Errorf("expected spiral to not be active")
	}

	// Toggle existing tag -> removes it
	toggledRemove := data.ToggleTagURL("hypnosis")
	if strings.Contains(toggledRemove, "hypnosis") {
		t.Errorf("expected ToggleTagURL(hypnosis) to remove hypnosis, got: %s", toggledRemove)
	}

	// Toggle new tag -> appends it
	toggledAdd := data.ToggleTagURL("spiral")
	if !strings.Contains(toggledAdd, "spiral") || !strings.Contains(toggledAdd, "hypnosis") {
		t.Errorf("expected ToggleTagURL(spiral) to add spiral, got: %s", toggledAdd)
	}

	// Remove tag
	removed := data.RemoveTagURL("animated")
	if strings.Contains(removed, "animated") {
		t.Errorf("expected RemoveTagURL(animated) to remove animated, got: %s", removed)
	}
}

func TestUserPrefsCookie(t *testing.T) {
	ctx := context.Background()
	store, err := index.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 11, 5, 20, 0, 0, 0, time.UTC)
	posts := []hypnohub.Post{
		{
			ID:            501,
			Score:         24,
			Rating:        hypnohub.RatingExplicit,
			Tags:          "hypnosis spiral animated",
			FileURL:       "https://hypnohub.net/images/501.mp4",
			SampleURL:     "https://hypnohub.net/samples/501.jpg",
			PreviewURL:    "https://hypnohub.net/thumbnails/501.jpg",
			Width:         1920,
			Height:        1080,
			PreviewWidth:  250,
			PreviewHeight: 140,
			CreatedAt:     hypnohub.Date(now.Add(-2 * time.Hour)),
			ChangedAt:     hypnohub.UnixTime(now.Add(-2 * time.Hour).Unix()),
			Status:        "active",
		},
		{
			ID:            500,
			Score:         12,
			Rating:        hypnohub.RatingSafe,
			Tags:          "hypnosis portrait",
			FileURL:       "https://hypnohub.net/images/500.jpg",
			SampleURL:     "https://hypnohub.net/samples/500.jpg",
			PreviewURL:    "https://hypnohub.net/thumbnails/500.jpg",
			Width:         800,
			Height:        1200,
			PreviewWidth:  200,
			PreviewHeight: 300,
			CreatedAt:     hypnohub.Date(now.Add(-10 * time.Hour)),
			ChangedAt:     hypnohub.UnixTime(now.Add(-10 * time.Hour).Unix()),
			Status:        "active",
		},
	}
	if _, err := store.IngestBatch(ctx, posts, now, now, now, 1, true); err != nil {
		t.Fatalf("IngestBatch failed: %v", err)
	}

	srv := NewServer(store, nil)

	// 1. Without cookie: / has post 501 and 500
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "Post #501") || !strings.Contains(body, "Post #500") {
		t.Errorf("expected both posts on default /")
	}

	// 2. With cookie blocking "spiral": post 501 is excluded, post 500 is included
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "hv_prefs", Value: "%7B%22wb%22%3A%22all%22%2C%22wp%22%3A%223d%22%2C%22bt%22%3A%5B%22spiral%22%5D%2C%22r%22%3A%22all%22%7D"})
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	body = rec.Body.String()
	if strings.Contains(body, "Post #501") {
		t.Errorf("expected post 501 to be excluded because of blocked tag 'spiral'")
	}
	if !strings.Contains(body, "Post #500") {
		t.Errorf("expected post 500 to remain in feed")
	}

	// 3. Explicit user search for "spiral" overrides blocked tag
	req = httptest.NewRequest(http.MethodGet, "/?tags=spiral", nil)
	req.AddCookie(&http.Cookie{Name: "hv_prefs", Value: "%7B%22wb%22%3A%22all%22%2C%22wp%22%3A%223d%22%2C%22bt%22%3A%5B%22spiral%22%5D%2C%22r%22%3A%22all%22%7D"})
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	body = rec.Body.String()
	if !strings.Contains(body, "Post #501") {
		t.Errorf("expected post 501 when user explicitly searches for 'spiral'")
	}

	// 4. Default window from cookie: /popular with wp: "3d"
	req = httptest.NewRequest(http.MethodGet, "/popular", nil)
	req.AddCookie(&http.Cookie{Name: "hv_prefs", Value: "%7B%22wb%22%3A%22all%22%2C%22wp%22%3A%223d%22%2C%22bt%22%3A%5B%22spiral%22%5D%2C%22r%22%3A%22all%22%7D"})
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	body = rec.Body.String()
	if !strings.Contains(body, `class="pill active" aria-current="page">3 Days</a>`) {
		t.Errorf("expected 3 Days window active from cookie on /popular")
	}
}

func TestTagSuggestionsEndpoint(t *testing.T) {
	ctx := context.Background()
	store, err := index.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 11, 5, 20, 0, 0, 0, time.UTC)
	posts := []hypnohub.Post{
		{
			ID:            101,
			Score:         10,
			Rating:        hypnohub.RatingExplicit,
			Tags:          "hypnosis spiral animated",
			FileURL:       "https://hypnohub.net/images/101.mp4",
			SampleURL:     "https://hypnohub.net/samples/101.jpg",
			PreviewURL:    "https://hypnohub.net/thumbnails/101.jpg",
			Width:         1920,
			Height:        1080,
			PreviewWidth:  250,
			PreviewHeight: 140,
			CreatedAt:     hypnohub.Date(now),
			ChangedAt:     hypnohub.UnixTime(now.Unix()),
			Status:        "active",
		},
	}
	if _, err := store.IngestBatch(ctx, posts, now, now, now, 1, true); err != nil {
		t.Fatalf("IngestBatch failed: %v", err)
	}

	srv := NewServer(store, nil)

	// 1. Multi-tag with negative prefix: ?tags=hypnosis+-sp
	req := httptest.NewRequest(http.MethodGet, "/ui/tags/suggest?tags=hypnosis+-sp", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-set-value="hypnosis -spiral "`) {
		t.Errorf("expected suggestion with data-set-value='hypnosis -spiral ', got: %s", body)
	}
	if !strings.Contains(body, `<span>-spiral</span>`) {
		t.Errorf("expected suggestion with <span>-spiral</span>, got: %s", body)
	}

	// 2. Trailing space in multi-tag returns empty body
	req = httptest.NewRequest(http.MethodGet, "/ui/tags/suggest?tags=hypnosis+", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Body.Len() != 0 {
		t.Errorf("expected empty body for trailing space, got: %s", rec.Body.String())
	}

	// 3. Single-tag mode: ?mode=single&q=sp
	req = httptest.NewRequest(http.MethodGet, "/ui/tags/suggest?mode=single&q=sp", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body = rec.Body.String()
	if !strings.Contains(body, `data-add-block-tag="spiral"`) {
		t.Errorf("expected single-tag suggestion with data-add-block-tag='spiral', got: %s", body)
	}
	if !strings.Contains(body, `<span>spiral</span>`) {
		t.Errorf("expected single-tag suggestion with <span>spiral</span>, got: %s", body)
	}

	// 4. Single-tag mode with prefix < 2 returns empty body
	req = httptest.NewRequest(http.MethodGet, "/ui/tags/suggest?mode=single&q=s", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Body.Len() != 0 {
		t.Errorf("expected empty body for short prefix, got: %s", rec.Body.String())
	}
}

func TestRobotsTxtAndOpenGraph(t *testing.T) {
	ctx := context.Background()
	store, err := index.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	posts := []hypnohub.Post{
		{
			ID:            1,
			Score:         42,
			Rating:        hypnohub.RatingSafe,
			Tags:          "spiral trance hypnosis",
			Width:         1280,
			Height:        720,
			FileURL:       "https://example.com/1.jpg",
			SampleURL:     "https://example.com/sample_1.jpg",
			PreviewURL:    "https://example.com/thumb_1.jpg",
			PreviewWidth:  250,
			PreviewHeight: 140,
			CreatedAt:     hypnohub.Date(now),
			Status:        "active",
		},
	}
	if _, err := store.IngestBatch(ctx, posts, now, now, now, 1, false); err != nil {
		t.Fatalf("IngestBatch failed: %v", err)
	}

	srv := New(store, nil)

	// 1. /robots.txt
	req := httptest.NewRequest(http.MethodGet, "/robots.txt", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("expected text/plain Content-Type, got: %s", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "GPTBot") || !strings.Contains(body, "Disallow: /*?") || !strings.Contains(body, "Allow: /api/") {
		t.Errorf("robots.txt missing expected rules: %s", body)
	}

	// 2. / (clean root) has index, follow and OpenGraph tags
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	body = rec.Body.String()
	if !strings.Contains(body, `<meta name="robots" content="index, follow" />`) {
		t.Errorf("expected index, follow meta on root, got: %s", body)
	}
	if !strings.Contains(body, `property="og:site_name" content="hypnogaze"`) {
		t.Errorf("expected og:site_name on root, got: %s", body)
	}
	if !strings.Contains(body, `property="og:title" content="Hypnogaze — Browse"`) {
		t.Errorf("expected og:title on root, got: %s", body)
	}

	// 3. /?page=2 has noindex, follow
	req = httptest.NewRequest(http.MethodGet, "/?page=2", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	body = rec.Body.String()
	if !strings.Contains(body, `<meta name="robots" content="noindex, follow" />`) {
		t.Errorf("expected noindex, follow meta on paginated page, got: %s", body)
	}

	// 4. /posts/1 has OpenGraph and Twitter card metadata
	req = httptest.NewRequest(http.MethodGet, "/posts/1", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	body = rec.Body.String()
	if !strings.Contains(body, `<meta name="robots" content="index, follow" />`) {
		t.Errorf("expected index, follow on post detail, got: %s", body)
	}
	if !strings.Contains(body, `property="og:title" content="Post #1 (★ 42) — hypnogaze"`) {
		t.Errorf("expected og:title on post detail, got: %s", body)
	}
	if !strings.Contains(body, `name="twitter:card" content="summary_large_image"`) {
		t.Errorf("expected twitter:card on post detail, got: %s", body)
	}
}

func TestWebAPICORS(t *testing.T) {
	ctx := context.Background()
	store, err := index.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	srv := New(store, nil)

	// GET /api/v1/stats
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if origin := rec.Header().Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Errorf("expected Access-Control-Allow-Origin: *, got: %s", origin)
	}

	// OPTIONS /api/v1/posts
	req = httptest.NewRequest(http.MethodOptions, "/api/v1/posts", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content for OPTIONS, got %d", rec.Code)
	}
	if origin := rec.Header().Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Errorf("expected Access-Control-Allow-Origin: *, got: %s", origin)
	}
}
