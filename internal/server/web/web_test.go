package web

import (
	"bytes"
	"context"
	"encoding/json"
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

	srv := NewServer(store, false)

	tests := []struct {
		name          string
		path          string
		headers       map[string]string
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
				`id="post-overlay-slot"`,
				`href="/posts/501"`,
				`href="/posts/500"`,
			},
			unwantSubstrs: []string{
				`aria-current="page">Popular</a>`,
				`id="lightbox"`,
				`class="lightbox"`,
				`?back=`,
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
				`id="post-overlay-slot"`,
				`href="/posts/501"`,
			},
			unwantSubstrs: []string{
				`aria-current="page">Browse</a>`,
				`id="lightbox"`,
				`class="lightbox"`,
				`?back=`,
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
				`id="post-detail"`,
				`id="sheet-handle-zone"`,
				`post-back-btn`,
				`post-close-btn`,
				`id="sheet-backdrop"`,
			},
			unwantSubstrs: []string{
				`id="lightbox"`,
				`class="lightbox"`,
			},
		},
		{
			name:     "post detail view with referer",
			path:     "/posts/501",
			headers:  map[string]string{"Referer": "http://example.com/popular?window=7d"},
			wantCode: http.StatusOK,
			wantSubstrs: []string{
				`href="/popular?window=7d"`,
				`← Back to Feed`,
				`Post #501`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
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

	srv := NewServer(store, false)

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

	srv := NewServer(store, false)

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

	srv := NewServer(store, false)

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

	srv := NewServer(store, false)

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

func TestPWAManifestAndServiceWorker(t *testing.T) {
	ctx := context.Background()
	store, err := index.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	srv := NewServer(store, false)

	// 1. GET /manifest.webmanifest
	manifestReq := httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil)
	manifestRec := httptest.NewRecorder()
	srv.ServeHTTP(manifestRec, manifestReq)

	if manifestRec.Code != http.StatusOK {
		t.Fatalf("GET /manifest.webmanifest code = %d, want %d", manifestRec.Code, http.StatusOK)
	}
	ct := manifestRec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/manifest+json") {
		t.Errorf("expected Content-Type application/manifest+json, got: %s", ct)
	}
	var manifestData map[string]any
	if err := json.Unmarshal(manifestRec.Body.Bytes(), &manifestData); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if manifestData["name"] != "hypnogaze" {
		t.Errorf("manifest name = %v, want hypnogaze", manifestData["name"])
	}
	if manifestData["display"] != "standalone" {
		t.Errorf("manifest display = %v, want standalone", manifestData["display"])
	}
	if manifestData["start_url"] != "/?pwa=1" {
		t.Errorf("manifest start_url = %v, want /?pwa=1", manifestData["start_url"])
	}

	// 2. GET /sw.js
	swReq := httptest.NewRequest(http.MethodGet, "/sw.js", nil)
	swRec := httptest.NewRecorder()
	srv.ServeHTTP(swRec, swReq)

	if swRec.Code != http.StatusOK {
		t.Fatalf("GET /sw.js code = %d, want %d", swRec.Code, http.StatusOK)
	}
	swCT := swRec.Header().Get("Content-Type")
	if !strings.Contains(swCT, "application/javascript") {
		t.Errorf("sw.js Content-Type = %s, want application/javascript", swCT)
	}
	if cc := swRec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Errorf("sw.js Cache-Control = %s, want no-cache", cc)
	}
	if swa := swRec.Header().Get("Service-Worker-Allowed"); swa != "/" {
		t.Errorf("sw.js Service-Worker-Allowed = %s, want /", swa)
	}
	swBody := swRec.Body.String()
	if strings.Contains(swBody, "__CACHE_VERSION__") {
		t.Errorf("sw.js still contains unresolved __CACHE_VERSION__")
	}
	if !strings.Contains(swBody, "hypnogaze-v-") {
		t.Errorf("sw.js does not contain resolved content hash cache name")
	}
	if !strings.Contains(swBody, "referrer: request.referrer") {
		t.Errorf("sw.js missing referrer: request.referrer preservation")
	}

	// 3. GET /static/shell.html and verify /shell.html is 404
	redundantShellReq := httptest.NewRequest(http.MethodGet, "/shell.html", nil)
	redundantShellRec := httptest.NewRecorder()
	srv.ServeHTTP(redundantShellRec, redundantShellReq)
	if redundantShellRec.Code != http.StatusNotFound {
		t.Errorf("GET /shell.html code = %d, want 404 Not Found", redundantShellRec.Code)
	}

	shellReq := httptest.NewRequest(http.MethodGet, "/static/shell.html", nil)
	shellRec := httptest.NewRecorder()
	srv.ServeHTTP(shellRec, shellReq)

	if shellRec.Code != http.StatusOK {
		t.Fatalf("GET /static/shell.html code = %d, want %d", shellRec.Code, http.StatusOK)
	}
	shellBody := shellRec.Body.String()
	if strings.Contains(shellBody, "<svg") {
		t.Errorf("/static/shell.html contains inline <svg> tag (forbidden by rule)")
	}
	if !strings.Contains(shellBody, `id="pwa-shell-stage"`) {
		t.Errorf("/static/shell.html missing #pwa-shell-stage")
	}
	if !strings.Contains(shellBody, `id="pwa-shell-loading"`) {
		t.Errorf("/static/shell.html missing #pwa-shell-loading")
	}
	if !strings.Contains(shellBody, `id="pwa-shell-offline"`) {
		t.Errorf("/static/shell.html missing #pwa-shell-offline")
	}
	if !strings.Contains(shellBody, `X-Hypnogaze-Shell`) {
		t.Errorf("/static/shell.html missing X-Hypnogaze-Shell transition logic")
	}
	if !strings.Contains(shellBody, `<meta name="htmx-config"`) {
		t.Errorf("/static/shell.html missing <meta name=\"htmx-config\"")
	}
	if !strings.Contains(shellBody, `AbortSignal.timeout`) {
		t.Errorf("/static/shell.html missing AbortSignal.timeout")
	}
	if !strings.Contains(shellBody, `transitioned = true`) {
		t.Errorf("/static/shell.html missing transitioned guard")
	}
	if !strings.Contains(shellBody, `removeEventListener("online", attemptTransition)`) {
		t.Errorf("/static/shell.html missing removeEventListener for online event")
	}

	// Verify app.js contains controllerchange reload logic
	appReq := httptest.NewRequest(http.MethodGet, "/static/app.js", nil)
	appRec := httptest.NewRecorder()
	srv.ServeHTTP(appRec, appReq)
	if appRec.Code != http.StatusOK {
		t.Fatalf("GET /static/app.js code = %d, want %d", appRec.Code, http.StatusOK)
	}
	appBody := appRec.Body.String()
	if !strings.Contains(appBody, "window.location.reload()") {
		t.Errorf("app.js missing window.location.reload() in controllerchange handler")
	}
	if !strings.Contains(appBody, "controllerchange") {
		t.Errorf("app.js missing controllerchange listener")
	}

	// 4. PNG icons magic bytes and Content-Type
	pngIcons := []string{
		"/static/icon-192.png",
		"/static/icon-512.png",
		"/static/icon-maskable-512.png",
		"/static/apple-touch-icon.png",
	}
	pngMagic := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	for _, iconPath := range pngIcons {
		iconReq := httptest.NewRequest(http.MethodGet, iconPath, nil)
		iconRec := httptest.NewRecorder()
		srv.ServeHTTP(iconRec, iconReq)

		if iconRec.Code != http.StatusOK {
			t.Fatalf("GET %s code = %d, want %d", iconPath, iconRec.Code, http.StatusOK)
		}
		if ct := iconRec.Header().Get("Content-Type"); !strings.Contains(ct, "image/png") {
			t.Errorf("%s Content-Type = %s, want image/png", iconPath, ct)
		}
		data := iconRec.Body.Bytes()
		if len(data) < len(pngMagic) || !bytes.Equal(data[:len(pngMagic)], pngMagic) {
			t.Errorf("%s missing valid PNG magic bytes", iconPath)
		}
	}

	// 5. Layout template includes PWA meta tags and manifest link
	rootReq := httptest.NewRequest(http.MethodGet, "/", nil)
	rootRec := httptest.NewRecorder()
	srv.ServeHTTP(rootRec, rootReq)
	rootBody := rootRec.Body.String()
	for _, tag := range []string{
		`<link rel="manifest" href="/manifest.webmanifest" />`,
		`<link rel="apple-touch-icon" href="/static/apple-touch-icon.png" />`,
		`<meta name="mobile-web-app-capable" content="yes" />`,
		`<meta name="apple-mobile-web-app-capable" content="yes" />`,
	} {
		if !strings.Contains(rootBody, tag) {
			t.Errorf("root HTML missing expected PWA tag %q", tag)
		}
	}

	// 6. devMode generates stable hash from static directory
	devSrv := NewServer(store, true)
	devReq1 := httptest.NewRequest(http.MethodGet, "/sw.js", nil)
	devRec1 := httptest.NewRecorder()
	devSrv.ServeHTTP(devRec1, devReq1)
	devReq2 := httptest.NewRequest(http.MethodGet, "/sw.js", nil)
	devRec2 := httptest.NewRecorder()
	devSrv.ServeHTTP(devRec2, devReq2)
	if devRec1.Body.String() != devRec2.Body.String() {
		t.Errorf("devMode /sw.js cache version changed between sequential requests without file modifications")
	}
	if !strings.Contains(devRec1.Body.String(), "dev-") {
		t.Errorf("devMode /sw.js does not contain dev- prefix: %s", devRec1.Body.String())
	}
}

func TestSafeBackURL(t *testing.T) {
	cases := []struct {
		url  string
		safe bool
	}{
		{"", false},
		{"//evil.example/x", false},
		{"/\\evil.example", false},
		{"https://evil.example", false},
		{"javascript:alert(1)", false},
		{"/posts/100", false},
		{"/posts/100?back=%2F", false},
		{"/sw.js", false},
		{"/static/style.css", false},
		{"/", true},
		{"/popular", true},
		{"/?window=7d&sort=hot", true},
		{"/popular?rating=safe", true},
	}

	for _, tc := range cases {
		if got := isSafeBackURL(tc.url); got != tc.safe {
			t.Errorf("isSafeBackURL(%q) = %v, want %v", tc.url, got, tc.safe)
		}
	}
}

func TestBackURLAndRobotsMeta(t *testing.T) {
	ctx := context.Background()
	store, err := index.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	posts := []hypnohub.Post{
		{
			ID:        1,
			Score:     10,
			Rating:    hypnohub.RatingSafe,
			Tags:      "test",
			Width:     800,
			Height:    600,
			FileURL:   "https://example.com/1.jpg",
			SampleURL: "https://example.com/1.jpg",
			CreatedAt: hypnohub.Date(now),
			Status:    "active",
		},
	}
	if _, err := store.IngestBatch(ctx, posts, now, now, now, 1, false); err != nil {
		t.Fatalf("IngestBatch failed: %v", err)
	}

	srv := NewServer(store, false)

	// 1. Safe back URL is preserved in ← Back to Feed link
	req := httptest.NewRequest(http.MethodGet, "/posts/1?back=%2Fpopular%3Fwindow%3D7d", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `href="/popular?window=7d"`) {
		t.Errorf("expected back link /popular?window=7d, got body: %s", body)
	}
	// And with query params, robots is noindex, follow
	if !strings.Contains(body, `<meta name="robots" content="noindex, follow" />`) {
		t.Errorf("expected noindex, follow on post detail with query param, got: %s", body)
	}

	// 2. Open redirect attempts fall back to href="/"
	req = httptest.NewRequest(http.MethodGet, "/posts/1?back=%2F%2Fevil.example%2Fexploit", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	body = rec.Body.String()
	if strings.Contains(body, "evil.example") {
		t.Errorf("expected open redirect payload to be rejected, got: %s", body)
	}
	if !strings.Contains(body, `href="/"`) {
		t.Errorf("expected fallback href=\"/\", got: %s", body)
	}

	// 3. /?rating=all preserves rating=all in the search form
	req = httptest.NewRequest(http.MethodGet, "/?rating=all", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	body = rec.Body.String()
	if !strings.Contains(body, `<input type="hidden" name="rating" value="all" />`) {
		t.Errorf("expected hidden input rating=all in search form, got: %s", body)
	}
}

func TestCategorizedPostTagsOnWebAndAPI(t *testing.T) {
	ctx := context.Background()
	store, err := index.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("page") == "post" && q.Get("s") == "view" && q.Get("id") == "287547" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<ul id="tag-sidebar">
				<li class="tag-type-copyright tag"><a href="?">?</a><a href="#">marvel comics</a></li>
				<li class="tag-type-character tag"><a href="?">?</a><a href="#">emma frost</a></li>
				<li class="tag-type-artist tag"><a href="?">?</a><a href="#">thedrawing</a></li>
				<li class="tag-type-general tag"><a href="?">?</a><a href="#">glowing eyes</a></li>
				<li class="tag-type-metadata tag"><a href="?">?</a><a href="#">simple background</a></li>
			</ul>
			<div id="comment-list">
				<div id="c626853">
					<div class="horizontalFlexWithMargins">
						<div class="comment-left-col">
							<a href="index.php?page=account&amp;s=profile&amp;uname=Bootyhunter69">Bootyhunter69</a>
						</div>
						<div class="comment-right-col">
							<div><b>Posted on 2026-11-05 21:41:10 <br />Score: <a id="sc626853">4</a></b></div>
							<div style="padding-left: 5px;">Mesmerizingspiral work!</div>
						</div>
					</div>
				</div>
			</div>`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := hypnohub.FromHTTPClient(ts.Client())
	client.BaseURL = ts.URL
	store.SetClient(client)

	now := time.Now().UTC()
	posts := []hypnohub.Post{
		{
			ID:          287547,
			Score:       55,
			Rating:      hypnohub.RatingExplicit,
			Tags:        "simple_background glowing_eyes thedrawing emma_frost marvel_comics",
			Width:       1200,
			Height:      1600,
			FileURL:     "https://example.com/287547.jpg",
			SampleURL:   "https://example.com/287547_sample.jpg",
			CreatedAt:   hypnohub.Date(now),
			Status:      "active",
			HasComments: "true",
		},
	}
	if _, err := store.IngestBatch(ctx, posts, now, now, now, 1, false); err != nil {
		t.Fatalf("IngestBatch failed: %v", err)
	}

	srv := NewServer(store, false)

	// 1. Check Web /posts/287547 renders all 5 category groups in upstream order
	req := httptest.NewRequest(http.MethodGet, "/posts/287547", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /posts/287547 status = %d", rec.Code)
	}
	body := rec.Body.String()

	idxCopyright := strings.Index(body, "tag-category-copyright")
	idxCharacter := strings.Index(body, "tag-category-character")
	idxArtist := strings.Index(body, "tag-category-artist")
	idxGeneral := strings.Index(body, "tag-category-general")
	idxMeta := strings.Index(body, "tag-category-meta")

	if idxCopyright == -1 || idxCharacter == -1 || idxArtist == -1 || idxGeneral == -1 || idxMeta == -1 {
		t.Fatalf("missing expected tag category classes in post HTML: copyright=%d character=%d artist=%d general=%d meta=%d",
			idxCopyright, idxCharacter, idxArtist, idxGeneral, idxMeta)
	}
	if !(idxCopyright < idxCharacter && idxCharacter < idxArtist && idxArtist < idxGeneral && idxGeneral < idxMeta) {
		t.Errorf("tag categories not rendered in upstream order (Copyright -> Character -> Artist -> General -> Meta): %d, %d, %d, %d, %d",
			idxCopyright, idxCharacter, idxArtist, idxGeneral, idxMeta)
	}
	if !strings.Contains(body, `id="post-comments-card"`) || !strings.Contains(body, `Bootyhunter69`) || !strings.Contains(body, `Mesmerizingspiral work!`) {
		t.Errorf("post HTML missing rendered comments card: %s", body)
	}

	// 2. Check REST API /api/v1/posts/287547 includes tag_groups and comments, and /api/v1/posts/287547/comments works
	apiReq := httptest.NewRequest(http.MethodGet, "/api/v1/posts/287547", nil)
	apiRec := httptest.NewRecorder()
	srv.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/posts/287547 status = %d", apiRec.Code)
	}
	if !strings.Contains(apiRec.Body.String(), `"tag_groups"`) || !strings.Contains(apiRec.Body.String(), `"slug":"copyright"`) || !strings.Contains(apiRec.Body.String(), `"comments"`) {
		t.Errorf("API response missing tag_groups / copyright slug / comments: %s", apiRec.Body.String())
	}

	commentsReq := httptest.NewRequest(http.MethodGet, "/api/v1/posts/287547/comments", nil)
	commentsRec := httptest.NewRecorder()
	srv.ServeHTTP(commentsRec, commentsReq)
	if commentsRec.Code != http.StatusOK || !strings.Contains(commentsRec.Body.String(), `"Bootyhunter69"`) {
		t.Errorf("GET /api/v1/posts/287547/comments status=%d body=%s", commentsRec.Code, commentsRec.Body.String())
	}
}
