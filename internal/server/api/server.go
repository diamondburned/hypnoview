// Package api implements the Hypnoview JSON REST API using Huma v2.
// All endpoints query the local SQLite index exclusively and never contact upstream Hypnohub.
package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/go-chi/chi/v5"
	"libdb.so/hypnoview"
	"libdb.so/hypnoview/internal/index"
)

// Server provides the REST API for Hypnoview.
type Server struct {
	store *index.Store
	mux   *http.ServeMux
	api   huma.API
}

// CORSMiddleware sets wildcard CORS headers and handles preflight OPTIONS requests.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// RegisterRoutes mounts the REST API endpoints and OpenAPI/docs on the provided Chi router
// with wildcard CORS enabled.
func RegisterRoutes(r chi.Router, store *index.Store) {
	srv := NewServer(store)
	r.Route("/api", func(sub chi.Router) {
		sub.Use(CORSMiddleware)
		sub.HandleFunc("/*", srv.ServeHTTP)
		sub.HandleFunc("/", srv.ServeHTTP)
	})
}

// NewHandler initializes an http.Handler for the REST API with CORS enabled.
func NewHandler(store *index.Store) http.Handler {
	return NewServer(store)
}

// NewServer initializes the REST API Server wired to the local SQLite index Store.
func NewServer(store *index.Store) *Server {
	s := &Server{
		store: store,
		mux:   http.NewServeMux(),
	}

	config := huma.DefaultConfig("Hypnoview API", "2.0.0")
	config.Info.Description = "Local-index REST API for browsing Hypnohub posts and rolling popularity rankings."
	config.OpenAPIPath = "/api/openapi"
	config.DocsPath = "/api/docs"
	config.SchemasPath = "/api/schemas"

	s.api = humago.New(s.mux, config)
	s.registerRoutes()
	return s
}

// ServeHTTP implements http.Handler with wildcard CORS middleware.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	CORSMiddleware(s.mux).ServeHTTP(w, r)
}

// ListPostsInput defines the query parameters for listing/ranking posts from the local index.
type ListPostsInput struct {
	Window      string `query:"window" default:"24h" enum:"6h,24h,3d,7d,14d,30d,all" doc:"Rolling time window relative to the latest poll."`
	Sort        string `query:"sort" default:"hot" enum:"hot,top,new,score" doc:"Ranking mode: hot (upvote velocity), top (window score delta), new (newest first), or score (cumulative score)."`
	Rating      string `query:"rating" default:"" enum:",s,q,e,safe,questionable,explicit" doc:"Optional rating filter."`
	MediaType   string `query:"media" default:"" enum:",image,video,animated" doc:"Optional media type filter."`
	Tags        string `query:"tags" default:"" doc:"Space- or comma-separated tags. Prefix a tag with '-' to exclude it (e.g. 'hypnosis -furry')."`
	IncludeTags string `query:"include_tags" default:"" doc:"Additional space- or comma-separated tags to require."`
	ExcludeTags string `query:"exclude_tags" default:"" doc:"Additional space- or comma-separated tags to exclude."`
	Limit       int    `query:"limit" default:"48" minimum:"1" maximum:"200" doc:"Maximum number of posts to return."`
	Offset      int    `query:"offset" default:"0" minimum:"0" doc:"Pagination offset."`
}

// ListPostsOutput wraps the paginated post query result.
type ListPostsOutput struct {
	Body index.QueryResult
}

// GetPostInput defines the path parameter for fetching a single post.
type GetPostInput struct {
	ID int `path:"id" minimum:"1" doc:"Hypnohub post ID."`
}

// PostDetailResponse contains a single indexed post and its historical score snapshots.
type PostDetailResponse struct {
	Post      index.IndexedPost  `json:"post" doc:"Indexed post metadata and URLs."`
	Snapshots []index.ScorePoint `json:"snapshots" doc:"Recorded 2-hour score snapshots for this post."`
}

// GetPostOutput wraps PostDetailResponse.
type GetPostOutput struct {
	Body PostDetailResponse
}

// ListTagsInput defines query parameters for searching local indexed tags.
type ListTagsInput struct {
	Query string `query:"q" default:"" doc:"Tag name prefix or substring to search for."`
	Limit int    `query:"limit" default:"30" minimum:"1" maximum:"200" doc:"Maximum number of tags to return."`
}

// ListTagsOutput wraps matching tags.
type ListTagsOutput struct {
	Body struct {
		Tags []index.TagSummary `json:"tags"`
	}
}

// GetStatsOutput wraps index statistics.
type GetStatsOutput struct {
	Body index.Stats
}

func (s *Server) registerRoutes() {
	huma.Register(s.api, huma.Operation{
		OperationID: "listPosts",
		Method:      http.MethodGet,
		Path:        "/api/v1/posts",
		Summary:     "List and rank indexed posts",
		Description: "Queries the local SQLite index over a rolling time window using rate-based ('hot'), delta-based ('top'), recency ('new'), or total score ('score') sorting.",
		Tags:        []string{"posts"},
		Errors:      []int{http.StatusBadRequest, http.StatusInternalServerError},
	}, s.listPosts)

	huma.Register(s.api, huma.Operation{
		OperationID: "listPopularPosts",
		Method:      http.MethodGet,
		Path:        "/api/v1/popular",
		Summary:     "List popular posts in a sliding window",
		Description: "Alias for /api/v1/posts tailored for popular post feeds.",
		Tags:        []string{"posts"},
		Errors:      []int{http.StatusBadRequest, http.StatusInternalServerError},
	}, s.listPosts)

	huma.Register(s.api, huma.Operation{
		OperationID: "getPost",
		Method:      http.MethodGet,
		Path:        "/api/v1/posts/{id}",
		Summary:     "Get an indexed post and its score history",
		Description: "Returns a single post from the local index along with its recorded 2-hour score snapshots.",
		Tags:        []string{"posts"},
		Errors:      []int{http.StatusNotFound, http.StatusInternalServerError},
	}, s.getPost)

	huma.Register(s.api, huma.Operation{
		OperationID: "listTags",
		Method:      http.MethodGet,
		Path:        "/api/v1/tags",
		Summary:     "Search indexed tags",
		Description: "Searches tags in the local SQLite index ordered by post count.",
		Tags:        []string{"tags"},
		Errors:      []int{http.StatusInternalServerError},
	}, s.listTags)

	huma.Register(s.api, huma.Operation{
		OperationID: "getStats",
		Method:      http.MethodGet,
		Path:        "/api/v1/stats",
		Summary:     "Get index statistics",
		Description: "Returns counts of indexed posts, tags, snapshots, and the last background poll timestamp.",
		Tags:        []string{"index"},
		Errors:      []int{http.StatusInternalServerError},
	}, s.getStats)
}

func (s *Server) listPosts(ctx context.Context, input *ListPostsInput) (*ListPostsOutput, error) {
	inc, exc := ParseTagFilterString(input.Tags)
	if input.IncludeTags != "" {
		inc2, exc2 := ParseTagFilterString(input.IncludeTags)
		inc = append(inc, inc2...)
		exc = append(exc, exc2...)
	}
	if input.ExcludeTags != "" {
		inc3, exc3 := ParseTagFilterString(input.ExcludeTags)
		exc = append(exc, inc3...)
		exc = append(exc, exc3...)
	}

	res, err := s.store.QueryPosts(ctx, index.QueryOptions{
		Window:      index.Window(input.Window),
		Sort:        index.SortMode(input.Sort),
		Rating:      hypnohub.Rating(input.Rating),
		MediaType:   input.MediaType,
		IncludeTags: inc,
		ExcludeTags: exc,
		Limit:       input.Limit,
		Offset:      input.Offset,
	})
	if err != nil {
		if errors.Is(err, index.ErrInvalidWindow) || errors.Is(err, index.ErrInvalidSort) {
			return nil, huma.Error400BadRequest(err.Error())
		}
		return nil, huma.Error500InternalServerError(err.Error())
	}

	return &ListPostsOutput{Body: *res}, nil
}

func (s *Server) getPost(ctx context.Context, input *GetPostInput) (*GetPostOutput, error) {
	post, snapshots, err := s.store.GetPost(ctx, hypnohub.PostID(input.ID))
	if err != nil {
		if errors.Is(err, index.ErrNotFound) {
			return nil, huma.Error404NotFound("post not found in index")
		}
		return nil, huma.Error500InternalServerError(err.Error())
	}

	return &GetPostOutput{
		Body: PostDetailResponse{
			Post:      *post,
			Snapshots: snapshots,
		},
	}, nil
}

func (s *Server) listTags(ctx context.Context, input *ListTagsInput) (*ListTagsOutput, error) {
	tags, err := s.store.SearchTags(ctx, input.Query, input.Limit)
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	out := &ListTagsOutput{}
	out.Body.Tags = tags
	return out, nil
}

func (s *Server) getStats(ctx context.Context, _ *struct{}) (*GetStatsOutput, error) {
	stats, err := s.store.Stats(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	return &GetStatsOutput{Body: *stats}, nil
}

// ParseTagFilterString splits a booru-style tag filter string (e.g. "hypnosis -furry comic")
// into included and excluded tag slices.
func ParseTagFilterString(raw string) (include, exclude []string) {
	for _, token := range strings.Fields(strings.ReplaceAll(raw, ",", " ")) {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if strings.HasPrefix(token, "-") && len(token) > 1 {
			exclude = append(exclude, strings.ToLower(token[1:]))
		} else {
			include = append(include, strings.ToLower(token))
		}
	}
	return include, exclude
}
