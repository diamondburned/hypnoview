package api

import (
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

func TestRESTAPIEndpoints(t *testing.T) {
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

	srv := NewServer(store)

	// 1. GET /api/v1/posts?window=24h&sort=hot
	req := httptest.NewRequest(http.MethodGet, "/api/v1/posts?window=24h&sort=hot", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/posts returned %d: %s", rec.Code, rec.Body.String())
	}

	var listRes index.QueryResult
	if err := json.Unmarshal(rec.Body.Bytes(), &listRes); err != nil {
		t.Fatalf("unmarshal posts response: %v", err)
	}
	if listRes.Total != 2 || len(listRes.Posts) != 2 {
		t.Fatalf("expected 2 posts, got total=%d len=%d", listRes.Total, len(listRes.Posts))
	}
	if listRes.Posts[0].ID != 501 {
		t.Errorf("expected hot post #501 first, got #%d", listRes.Posts[0].ID)
	}

	// 2. GET /api/v1/posts/501
	req = httptest.NewRequest(http.MethodGet, "/api/v1/posts/501", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/posts/501 returned %d: %s", rec.Code, rec.Body.String())
	}
	var detail PostDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("unmarshal post detail: %v", err)
	}
	if detail.Post.ID != 501 || len(detail.Snapshots) < 2 {
		t.Errorf("unexpected post detail: %+v", detail)
	}

	// 3. GET /api/v1/tags?q=hyp
	req = httptest.NewRequest(http.MethodGet, "/api/v1/tags?q=hyp", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/tags returned %d: %s", rec.Code, rec.Body.String())
	}

	// 4. GET /api/v1/stats
	req = httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/stats returned %d: %s", rec.Code, rec.Body.String())
	}

	// 5. GET /api/openapi.json
	req = httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/openapi.json returned %d", rec.Code)
	}
}

func TestAPICORS(t *testing.T) {
	ctx := context.Background()
	store, err := index.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	srv := NewServer(store)

	// GET /api/v1/stats sets wildcard CORS
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if origin := rec.Header().Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Errorf("expected Access-Control-Allow-Origin: *, got: %s", origin)
	}

	// OPTIONS /api/v1/posts preflight returns 204 No Content
	req = httptest.NewRequest(http.MethodOptions, "/api/v1/posts", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content for OPTIONS, got %d", rec.Code)
	}
	if origin := rec.Header().Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Errorf("expected Access-Control-Allow-Origin: *, got: %s", origin)
	}
	if methods := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(methods, "GET") {
		t.Errorf("expected Access-Control-Allow-Methods to include GET, got: %s", methods)
	}
}
