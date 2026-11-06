// Package web provides the server-rendered HTML web UI for Hypnogaze.
package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"math"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"gaze.d14.pet"
	"gaze.d14.pet/internal/index"
	"gaze.d14.pet/internal/server/api"
	"github.com/go-chi/chi/v5"
)

func init() {
	_ = mime.AddExtensionType(".css", "text/css; charset=utf-8")
	_ = mime.AddExtensionType(".js", "application/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".svg", "image/svg+xml")
}

//go:embed static/*
var staticFS embed.FS

//go:embed templates/*
var templateFS embed.FS

var (
	pageIndexTmpl *template.Template
	pagePostTmpl  *template.Template
)

var staticFileServer http.Handler

var tmplFuncs = template.FuncMap{
	"join":      strings.Join,
	"upper":     strings.ToUpper,
	"lower":     strings.ToLower,
	"formatInt": formatInt,
	"timeAgo":   timeAgo,
	"hotCount": func(rate float64) int {
		if rate <= 0 {
			return 0
		}
		n := int(math.Round(rate))
		if n < 1 {
			return 1
		}
		return n
	},
	"rateLabel": func(rate float64) string {
		if rate >= 10 {
			return fmt.Sprintf("+%.0f/h", rate)
		}
		if rate >= 1 {
			return fmt.Sprintf("+%.1f/h", rate)
		}
		return fmt.Sprintf("+%.2f/h", rate)
	},
	"aspectRatio": func(w, h int) template.CSS {
		if w <= 0 || h <= 0 {
			return template.CSS("1 / 1")
		}
		return template.CSS(fmt.Sprintf("%d / %d", w, h))
	},
	"isVideoExt": func(ext string) bool {
		return ext == "mp4" || ext == "webm"
	},
	"isAnimatedExt": func(ext string) bool {
		return ext == "mp4" || ext == "webm" || ext == "gif"
	},
	"thumbURL":    thumbURL,
	"thumbWidth":  thumbWidth,
	"thumbHeight": thumbHeight,
	"add": func(a, b int) int {
		return a + b
	},
	"json": func(v any) template.JS {
		b, _ := json.Marshal(v)
		return template.JS(b)
	},
}

// OpenGraph contains OpenGraph and Twitter card metadata for HTML pages.
type OpenGraph struct {
	Title       string
	Description string
	Type        string
	URL         string
	Image       string
	ImageWidth  int
	ImageHeight int
	Video       string
	VideoType   string
	TwitterCard string
}

func absURL(r *http.Request, rawURL string) string {
	if rawURL == "" {
		return ""
	}
	if strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://") {
		return rawURL
	}
	proto := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		proto = "https"
	}
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	if !strings.HasPrefix(rawURL, "/") {
		rawURL = "/" + rawURL
	}
	return proto + "://" + host + rawURL
}

func thumbURL(p index.IndexedPost) string {
	ext := strings.ToLower(p.FileExt)
	sampleLower := strings.ToLower(p.SampleURL)
	if ext == "mp4" || ext == "webm" || strings.HasSuffix(sampleLower, ".mp4") || strings.HasSuffix(sampleLower, ".webm") || p.SampleURL == "" {
		return p.PreviewURL
	}
	return p.SampleURL
}

func thumbWidth(p index.IndexedPost) int {
	ext := strings.ToLower(p.FileExt)
	if ext == "mp4" || ext == "webm" || p.SampleWidth <= 0 {
		if p.PreviewWidth > 0 {
			return p.PreviewWidth
		}
		return 250
	}
	return p.SampleWidth
}

func thumbHeight(p index.IndexedPost) int {
	ext := strings.ToLower(p.FileExt)
	if ext == "mp4" || ext == "webm" || p.SampleHeight <= 0 {
		if p.PreviewHeight > 0 {
			return p.PreviewHeight
		}
		return 250
	}
	return p.SampleHeight
}

func init() {
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	staticFileServer = http.FileServer(http.FS(staticSub))

	pageIndexTmpl = template.Must(template.
		New("layout.html").
		Funcs(tmplFuncs).
		ParseFS(templateFS,
			"templates/layout.html",
			"templates/index.html"))

	pagePostTmpl = template.Must(template.
		New("layout.html").
		Funcs(tmplFuncs).
		ParseFS(templateFS,
			"templates/layout.html",
			"templates/post.html"))
}

// Server serves the mobile-friendly HTML UI for browsing indexed Hypnohub posts.
type Server struct {
	store   *index.Store
	mux     *chi.Mux
	logger  *slog.Logger
	devMode bool
}

func devWebFS() fs.FS {
	if fi, err := os.Stat("internal/server/web"); err == nil && fi.IsDir() {
		return os.DirFS("internal/server/web")
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if ok {
		dir := filepath.Dir(thisFile)
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return os.DirFS(dir)
		}
	}
	return os.DirFS("internal/server/web")
}

// NewServer initializes the web UI Server.
func NewServer(store *index.Store, devMode bool) *Server {
	s := &Server{
		store:   store,
		mux:     chi.NewMux(),
		logger:  slog.Default().With("component", "web"),
		devMode: devMode,
	}

	var staticHandler http.Handler
	if devMode {
		webFS := devWebFS()
		staticSub, err := fs.Sub(webFS, "static")
		var devFS http.Handler
		if err != nil {
			devFS = http.FileServer(http.FS(webFS))
		} else {
			devFS = http.FileServer(http.FS(staticSub))
		}
		staticHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			devFS.ServeHTTP(w, r)
		})
	} else {
		staticHandler = staticFileServer
	}

	s.mux.Handle("/static/*", http.StripPrefix("/static", staticHandler))
	s.mux.Get("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/static/spiral.svg", http.StatusMovedPermanently)
	})
	s.mux.Get("/robots.txt", s.handleRobotsTxt)
	api.RegisterRoutes(s.mux, s.store)

	s.mux.Get("/", s.handleBrowse)
	s.mux.Get("/popular", s.handlePopular)
	s.mux.Get("/posts/{id}", s.handlePost)
	s.mux.Get("/ui/tags/suggest", s.handleTagSuggestions)

	return s
}

func (s *Server) getIndexTemplate() (*template.Template, error) {
	if !s.devMode {
		return pageIndexTmpl, nil
	}
	return template.
		New("layout.html").
		Funcs(tmplFuncs).
		ParseFS(devWebFS(),
			"templates/layout.html",
			"templates/index.html")
}

func (s *Server) getPostTemplate() (*template.Template, error) {
	if !s.devMode {
		return pagePostTmpl, nil
	}
	return template.
		New("layout.html").
		Funcs(tmplFuncs).
		ParseFS(devWebFS(),
			"templates/layout.html",
			"templates/post.html")
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

type windowOption struct {
	Key    string
	Label  string
	Active bool
	URL    string
}

type sortOption struct {
	Key    string
	Label  string
	Desc   string
	Active bool
	URL    string
}

type filterOption struct {
	Key    string
	Label  string
	Active bool
	URL    string
}

type userPrefs struct {
	WindowBrowse  string   `json:"wb,omitempty"`
	WindowPopular string   `json:"wp,omitempty"`
	SortBrowse    string   `json:"sb,omitempty"`
	SortPopular   string   `json:"sp,omitempty"`
	DefaultRating string   `json:"r,omitempty"`
	BlockedTags   []string `json:"bt,omitempty"`
}

func readUserPrefs(r *http.Request) userPrefs {
	cookie, err := r.Cookie("hv_prefs")
	if err != nil || cookie.Value == "" {
		return userPrefs{}
	}
	val, err := url.QueryUnescape(cookie.Value)
	if err != nil {
		val = cookie.Value
	}
	var prefs userPrefs
	if err := json.Unmarshal([]byte(val), &prefs); err != nil {
		return userPrefs{}
	}
	return prefs
}

type indexPageData struct {
	Title                   string
	ActiveNav               string
	FormAction              string
	Window                  string
	Sort                    string
	Rating                  string
	RawRating               string
	RatingParam             string
	Media                   string
	TagsQuery               string
	ActiveTags              []string
	BlockedTagsCount        int
	ClearTagsURL            string
	Page                    int
	Limit                   int
	Total                   int
	TotalPages              int
	HasPrev                 bool
	HasNext                 bool
	PrevURL                 string
	NextURL                 string
	Posts                   []index.IndexedPost
	PopularTags             []index.TagSummary
	Stats                   index.Stats
	Windows                 []windowOption
	Sorts                   []sortOption
	Ratings                 []filterOption
	MediaTypes              []filterOption
	HypnohubContinuationURL string
	LastPostID              hypnohub.PostID
	AllWindowURL            string
	RobotsMeta              string
	OpenGraph               OpenGraph
}

func (d indexPageData) IsTagActive(tag string) bool {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return false
	}
	for _, t := range strings.Fields(d.TagsQuery) {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

func (d indexPageData) ToggleTagURL(tag string) string {
	tag = strings.TrimSpace(tag)
	fields := strings.Fields(d.TagsQuery)
	var newTags []string
	found := false
	for _, t := range fields {
		if strings.EqualFold(t, tag) {
			found = true
		} else {
			newTags = append(newTags, t)
		}
	}
	if !found && tag != "" {
		newTags = append(newTags, tag)
	}
	return d.urlWithTags(strings.Join(newTags, " "))
}

func (d indexPageData) RemoveTagURL(tag string) string {
	tag = strings.TrimSpace(tag)
	fields := strings.Fields(d.TagsQuery)
	var newTags []string
	for _, t := range fields {
		if !strings.EqualFold(t, tag) {
			newTags = append(newTags, t)
		}
	}
	return d.urlWithTags(strings.Join(newTags, " "))
}

func (d indexPageData) urlWithTags(newTagsQuery string) string {
	params := url.Values{}
	if d.Window != "" {
		params.Set("window", d.Window)
	}
	if d.Sort != "" {
		params.Set("sort", d.Sort)
	}
	if d.Rating != "" {
		params.Set("rating", d.Rating)
	} else if d.RawRating == "all" {
		params.Set("rating", "all")
	}
	if d.Media != "" {
		params.Set("media", d.Media)
	}
	if newTagsQuery != "" {
		params.Set("tags", newTagsQuery)
	}
	base := d.FormAction
	if base == "" {
		base = "/"
	}
	encoded := params.Encode()
	if encoded == "" {
		return base
	}
	return base + "?" + encoded
}

type postPageData struct {
	Title           string
	ActiveNav       string
	Post            index.IndexedPost
	Snapshots       []index.ScorePoint
	Stats           index.Stats
	BackURL         string
	GroupPosts      []index.IndexedPost
	GroupPageIndex  int
	PrevGroupPostID hypnohub.PostID
	NextGroupPostID hypnohub.PostID
	RobotsMeta      string
	OpenGraph       OpenGraph
}

func (s *Server) handleTagSuggestions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	mode := q.Get("mode")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if mode == "single" {
		raw := strings.TrimSpace(q.Get("q"))
		prefix := strings.TrimPrefix(raw, "-")
		if len(prefix) < 2 {
			w.WriteHeader(http.StatusOK)
			return
		}
		tags, err := s.store.SearchTags(ctx, prefix, 10)
		if err != nil || len(tags) == 0 {
			w.WriteHeader(http.StatusOK)
			return
		}
		var buf bytes.Buffer
		for _, t := range tags {
			fmt.Fprintf(&buf, `<button type="button" class="tag-suggestion-item" role="option" data-add-block-tag="%s"><span>%s</span><small>%s posts</small></button>`+"\n",
				template.HTMLEscapeString(t.Name),
				template.HTMLEscapeString(t.Name),
				template.HTMLEscapeString(formatInt(t.PostCount)),
			)
		}
		w.Write(buf.Bytes())
		return
	}

	// Default multi-tag mode
	raw := q.Get("tags")
	if raw == "" || strings.TrimRightFunc(raw, unicode.IsSpace) != raw {
		w.WriteHeader(http.StatusOK)
		return
	}

	fields := strings.Fields(raw)
	if len(fields) == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}

	lastToken := fields[len(fields)-1]
	prefixTokens := fields[:len(fields)-1]

	sign := ""
	prefix := lastToken
	if strings.HasPrefix(lastToken, "-") {
		sign = "-"
		prefix = strings.TrimPrefix(lastToken, "-")
	}

	if len(prefix) < 2 {
		w.WriteHeader(http.StatusOK)
		return
	}

	tags, err := s.store.SearchTags(ctx, prefix, 10)
	if err != nil || len(tags) == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}

	prefixStr := ""
	if len(prefixTokens) > 0 {
		prefixStr = strings.Join(prefixTokens, " ") + " "
	}

	var buf bytes.Buffer
	for _, t := range tags {
		replacement := prefixStr + sign + t.Name + " "
		displayName := sign + t.Name
		fmt.Fprintf(&buf, `<button type="button" class="tag-suggestion-item" role="option" data-set-value="%s"><span>%s</span><small>%s posts</small></button>`+"\n",
			template.HTMLEscapeString(replacement),
			template.HTMLEscapeString(displayName),
			template.HTMLEscapeString(formatInt(t.PostCount)),
		)
	}
	w.Write(buf.Bytes())
}

const robotsTxt = `User-agent: GPTBot
User-agent: ChatGPT-User
User-agent: ClaudeBot
User-agent: anthropic-ai
User-agent: CCBot
User-agent: Google-Extended
User-agent: Bytespider
User-agent: FacebookBot
User-agent: PerplexityBot
User-agent: Amazonbot
Disallow: /

User-agent: *
Allow: /$
Allow: /popular$
Allow: /posts/
Allow: /api/
Allow: /static/
Disallow: /*?
Disallow: /ui/
Disallow: /
`

func (s *Server) handleRobotsTxt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(robotsTxt))
}

func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	s.handleFeed(w, r, "browse")
}

func (s *Server) handlePopular(w http.ResponseWriter, r *http.Request) {
	s.handleFeed(w, r, "popular")
}

func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request, mode string) {
	ctx := r.Context()
	q := r.URL.Query()

	basePath := "/"
	if mode == "popular" {
		basePath = "/popular"
	}

	prefs := readUserPrefs(r)

	var defaultWindow index.Window
	var defaultSort index.SortMode
	var pageTitle string

	if mode == "popular" {
		defaultWindow = index.WindowDay
		if prefs.WindowPopular != "" {
			wNorm := string(index.Window(prefs.WindowPopular).Normalize())
			if _, err := index.Window(wNorm).Duration(); err == nil {
				defaultWindow = index.Window(wNorm)
			}
		}
		defaultSort = index.SortHot
		if prefs.SortPopular != "" {
			if sNorm, err := index.SortMode(prefs.SortPopular).Normalize(); err == nil {
				defaultSort = sNorm
			}
		}
		pageTitle = "Hypnogaze — Popular & Trending"
	} else {
		defaultWindow = index.WindowAllTime
		if prefs.WindowBrowse != "" {
			wNorm := string(index.Window(prefs.WindowBrowse).Normalize())
			if _, err := index.Window(wNorm).Duration(); err == nil {
				defaultWindow = index.Window(wNorm)
			}
		}
		defaultSort = index.SortNew
		if prefs.SortBrowse != "" {
			if sNorm, err := index.SortMode(prefs.SortBrowse).Normalize(); err == nil {
				defaultSort = sNorm
			}
		}
		pageTitle = "Hypnogaze — Browse"
	}

	var window string
	if rawW := q.Get("window"); rawW != "" {
		wNorm := string(index.Window(rawW).Normalize())
		if _, err := index.Window(wNorm).Duration(); err == nil {
			window = wNorm
		} else {
			window = string(defaultWindow)
		}
	} else {
		window = string(defaultWindow)
	}

	var sortMode index.SortMode
	if rawS := q.Get("sort"); rawS != "" {
		if sNorm, err := index.SortMode(rawS).Normalize(); err == nil {
			sortMode = sNorm
		} else {
			sortMode = defaultSort
		}
	} else {
		sortMode = defaultSort
	}

	rawRating := strings.ToLower(strings.TrimSpace(q.Get("rating")))
	var rating string
	if !q.Has("rating") {
		switch prefs.DefaultRating {
		case "s", "q", "e":
			rating = prefs.DefaultRating
		default:
			rating = ""
		}
	} else if rawRating == "all" {
		rating = ""
	} else {
		rating = rawRating
	}

	media := strings.ToLower(strings.TrimSpace(q.Get("media")))
	tagsQuery := strings.TrimSpace(q.Get("tags"))

	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	const limit = 48
	offset := (page - 1) * limit

	inc, exc := api.ParseTagFilterString(tagsQuery)
	for _, bt := range prefs.BlockedTags {
		bt = strings.ToLower(strings.TrimSpace(bt))
		bt = strings.TrimPrefix(bt, "-")
		if bt == "" {
			continue
		}
		if slices.Contains(inc, bt) {
			continue
		}
		if !slices.Contains(exc, bt) {
			exc = append(exc, bt)
		}
	}

	res, err := s.store.QueryPosts(ctx, index.QueryOptions{
		Window:         index.Window(window),
		Sort:           sortMode,
		Rating:         hypnohub.Rating(rating),
		MediaType:      index.NormalizeMediaType(media),
		CollapseGroups: mode == "browse",
		IncludeTags:    inc,
		ExcludeTags:    exc,
		Limit:          limit,
		Offset:         offset,
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to query posts for index page", "error", err)
		http.Error(w, "failed to query index", http.StatusInternalServerError)
		return
	}

	stats, _ := s.store.Stats(ctx)
	if stats == nil {
		stats = &index.Stats{}
	}

	popularTags, _ := s.store.SearchTags(ctx, "", 20)

	totalPages := max(1, (res.Total+limit-1)/limit)

	buildURL := func(overrides map[string]string) string {
		params := url.Values{}
		params.Set("window", window)
		params.Set("sort", string(sortMode))
		if rating != "" {
			params.Set("rating", rating)
		} else if rawRating == "all" {
			params.Set("rating", "all")
		}
		if media != "" {
			params.Set("media", media)
		}
		if tagsQuery != "" {
			params.Set("tags", tagsQuery)
		}
		for k, v := range overrides {
			if v == "" {
				params.Del(k)
			} else {
				params.Set(k, v)
			}
		}
		return basePath + "?" + params.Encode()
	}

	windows := []windowOption{
		{Key: "6h", Label: "Rising (6h)"},
		{Key: "24h", Label: "Today (24h)"},
		{Key: "3d", Label: "3 Days"},
		{Key: "7d", Label: "Week (7d)"},
		{Key: "30d", Label: "Month (30d)"},
		{Key: "all", Label: "All Indexed"},
	}
	for i := range windows {
		windows[i].Active = windows[i].Key == window
		windows[i].URL = buildURL(map[string]string{"window": windows[i].Key, "page": ""})
	}

	sorts := []sortOption{
		{Key: "hot", Label: "Hot (Rate)", Desc: "Ranked by upvote velocity (upvotes/hr)"},
		{Key: "top", Label: "Top (Delta)", Desc: "Ranked by upvotes gained in window"},
		{Key: "new", Label: "New", Desc: "Newest uploads first"},
		{Key: "score", Label: "Total Score", Desc: "Highest cumulative score"},
	}
	for i := range sorts {
		sorts[i].Active = sorts[i].Key == string(sortMode)
		sorts[i].URL = buildURL(map[string]string{"sort": sorts[i].Key, "page": ""})
	}

	ratings := []filterOption{
		{Key: "all", Label: "All Ratings"},
		{Key: "e", Label: "Explicit"},
		{Key: "q", Label: "Questionable"},
		{Key: "s", Label: "Safe"},
	}
	for i := range ratings {
		ratings[i].Active = (ratings[i].Key == "all" && rating == "") || (ratings[i].Key != "all" && ratings[i].Key == rating)
		ratings[i].URL = buildURL(map[string]string{"rating": ratings[i].Key, "page": ""})
	}

	mediaTypes := []filterOption{
		{Key: "", Label: "All Media"},
		{Key: "animated", Label: "GIF / Video"},
		{Key: "video", Label: "Video Only"},
		{Key: "image", Label: "Static Images"},
	}
	for i := range mediaTypes {
		mediaTypes[i].Active = mediaTypes[i].Key == media
		mediaTypes[i].URL = buildURL(map[string]string{"media": mediaTypes[i].Key, "page": ""})
	}

	var continuationURL string
	var minPostID hypnohub.PostID
	if page == totalPages && len(res.Posts) > 0 {
		minPostID = res.Posts[0].ID
		for _, p := range res.Posts {
			if p.ID < minPostID {
				minPostID = p.ID
			}
		}
		var tagParts []string
		if tagsQuery != "" {
			tagParts = append(tagParts, strings.TrimSpace(tagsQuery))
		}
		tagParts = append(tagParts, fmt.Sprintf("id:<=%d", minPostID))
		if rating != "" {
			switch rating {
			case "s":
				tagParts = append(tagParts, "rating:safe")
			case "q":
				tagParts = append(tagParts, "rating:questionable")
			case "e":
				tagParts = append(tagParts, "rating:explicit")
			}
		}
		if sortMode == index.SortScore {
			tagParts = append(tagParts, "order:score")
		}
		continuationURL = "https://hypnohub.net/index.php?page=post&s=list&tags=" + url.QueryEscape(strings.Join(tagParts, " "))
	}

	ratingParam := rating
	if rawRating == "all" {
		ratingParam = "all"
	}

	data := indexPageData{
		Title:                   pageTitle,
		ActiveNav:               mode,
		FormAction:              basePath,
		Window:                  window,
		Sort:                    string(sortMode),
		Rating:                  rating,
		RawRating:               rawRating,
		RatingParam:             ratingParam,
		Media:                   media,
		TagsQuery:               tagsQuery,
		ActiveTags:              strings.Fields(tagsQuery),
		BlockedTagsCount:        len(prefs.BlockedTags),
		ClearTagsURL:            buildURL(map[string]string{"tags": "", "page": ""}),
		Page:                    page,
		Limit:                   limit,
		Total:                   res.Total,
		TotalPages:              totalPages,
		HasPrev:                 page > 1,
		HasNext:                 page < totalPages,
		PrevURL:                 buildURL(map[string]string{"page": strconv.Itoa(page - 1)}),
		NextURL:                 buildURL(map[string]string{"page": strconv.Itoa(page + 1)}),
		Posts:                   res.Posts,
		PopularTags:             popularTags,
		Stats:                   *stats,
		Windows:                 windows,
		Sorts:                   sorts,
		Ratings:                 ratings,
		MediaTypes:              mediaTypes,
		HypnohubContinuationURL: continuationURL,
		LastPostID:              minPostID,
		AllWindowURL:            "/?window=all&sort=new",
		RobotsMeta:              "index, follow",
		OpenGraph: OpenGraph{
			Title:       pageTitle,
			Description: fmt.Sprintf("Browse %s indexed Hypnohub posts with fast tag filtering.", formatInt(res.Total)),
			Type:        "website",
			URL:         absURL(r, r.URL.RequestURI()),
			TwitterCard: "summary_large_image",
		},
	}

	if page > 1 || tagsQuery != "" || r.URL.RawQuery != "" {
		data.RobotsMeta = "noindex, follow"
	}
	if mode == "popular" {
		data.OpenGraph.Description = "Trending Hypnohub posts ranked by score velocity and momentum."
	}
	if len(res.Posts) > 0 {
		top := res.Posts[0]
		data.OpenGraph.Image = absURL(r, thumbURL(top))
		data.OpenGraph.ImageWidth = thumbWidth(top)
		data.OpenGraph.ImageHeight = thumbHeight(top)
	}

	tmpl, err := s.getIndexTemplate()
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to parse index template", "error", err)
		http.Error(w, "internal server error: template parse failed", http.StatusInternalServerError)
		return
	}
	s.renderTemplate(w, tmpl, http.StatusOK, data)
}

// isSafeBackURL returns true if raw is a relative same-origin path safe for redirection/navigation.
// It explicitly blocks open redirects such as //evil.example or /\evil.example, as well as /posts/ loops.
func isSafeBackURL(raw string) bool {
	if raw == "" {
		return false
	}
	if !strings.HasPrefix(raw, "/") {
		return false
	}
	if strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, "/\\") {
		return false
	}
	if strings.HasPrefix(raw, "/posts/") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Scheme == "" && u.Host == ""
}

func (s *Server) handlePost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		http.Error(w, "invalid post ID", http.StatusBadRequest)
		return
	}

	post, snapshots, err := s.store.GetPost(ctx, hypnohub.PostID(id))
	if err != nil {
		if errors.Is(err, index.ErrNotFound) {
			http.Error(w, "post not found in index", http.StatusNotFound)
			return
		}
		s.logger.ErrorContext(ctx, "failed to get post", "id", id, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	stats, _ := s.store.Stats(ctx)
	if stats == nil {
		stats = &index.Stats{}
	}

	backURL := "/"
	if qBack := r.URL.Query().Get("back"); isSafeBackURL(qBack) {
		backURL = qBack
	} else if ref := r.Header.Get("Referer"); ref != "" {
		if u, err := url.Parse(ref); err == nil && (u.Host == "" || u.Host == r.Host) {
			if uri := u.RequestURI(); isSafeBackURL(uri) {
				backURL = uri
			}
		}
	}

	var (
		groupPosts     []index.IndexedPost
		groupPageIndex int
		prevGroupID    hypnohub.PostID
		nextGroupID    hypnohub.PostID
	)
	if post.GroupID > 0 && post.GroupPageCount > 1 {
		gps, err := s.store.GetGroupPosts(ctx, post.GroupID)
		if err == nil && len(gps) > 1 {
			groupPosts = gps
			for i, gp := range gps {
				if gp.ID == post.ID {
					groupPageIndex = i + 1
					if i > 0 {
						prevGroupID = gps[i-1].ID
					}
					if i < len(gps)-1 {
						nextGroupID = gps[i+1].ID
					}
					break
				}
			}
		}
	}

	ogType := "article"
	isVid := post.FileExt == "mp4" || post.FileExt == "webm"
	if isVid {
		ogType = "video.other"
	}

	tagsList := post.Tags
	if len(tagsList) > 12 {
		tagsList = tagsList[:12]
	}
	ogDesc := fmt.Sprintf("★ %d · %d×%d %s · Tags: %s", post.Score, post.Width, post.Height, strings.ToUpper(post.FileExt), strings.Join(tagsList, ", "))

	ogImg := post.FileURL
	ogW := post.Width
	ogH := post.Height
	if isVid {
		ogImg = thumbURL(*post)
		ogW = thumbWidth(*post)
		ogH = thumbHeight(*post)
	}

	postOG := OpenGraph{
		Title:       fmt.Sprintf("Post #%d (★ %d) — hypnogaze", post.ID, post.Score),
		Description: ogDesc,
		Type:        ogType,
		URL:         absURL(r, fmt.Sprintf("/posts/%d", post.ID)),
		Image:       absURL(r, ogImg),
		ImageWidth:  ogW,
		ImageHeight: ogH,
		TwitterCard: "summary_large_image",
	}
	if isVid {
		postOG.Video = post.FileURL
		if post.FileExt == "mp4" {
			postOG.VideoType = "video/mp4"
		} else {
			postOG.VideoType = "video/webm"
		}
	}

	robotsMeta := "index, follow"
	if r.URL.RawQuery != "" {
		robotsMeta = "noindex, follow"
	}

	data := postPageData{
		Title:           fmt.Sprintf("Post #%d — Hypnogaze", post.ID),
		Post:            *post,
		Snapshots:       snapshots,
		Stats:           *stats,
		BackURL:         backURL,
		GroupPosts:      groupPosts,
		GroupPageIndex:  groupPageIndex,
		PrevGroupPostID: prevGroupID,
		NextGroupPostID: nextGroupID,
		RobotsMeta:      robotsMeta,
		OpenGraph:       postOG,
	}

	tmpl, err := s.getPostTemplate()
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to parse post template", "error", err)
		http.Error(w, "internal server error: template parse failed", http.StatusInternalServerError)
		return
	}
	s.renderTemplate(w, tmpl, http.StatusOK, data)
}

func (s *Server) renderTemplate(w http.ResponseWriter, tmpl *template.Template, status int, data any) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		s.logger.Error("failed to render template", "template", tmpl.Name(), "error", err)
		http.Error(w, "internal server error: template render failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func formatInt(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(c))
	}
	return string(out)
}

func timeAgo(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
