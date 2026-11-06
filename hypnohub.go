package hypnohub

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// PostID is a post ID. It is implemented as a serial number.
type PostID int

// IsValid returns whether the post ID is valid.
// A post ID is valid if it is greater than 0.
func (p PostID) IsValid() bool {
	return p > 0
}

// UnixTime is a unix time.
type UnixTime int64

// Time returns the time.Time representation of the unix time.
func (u UnixTime) Time() time.Time {
	return time.Unix(int64(u), 0)
}

// Post is a single post from the hypnohub API.
type Post struct {
	ID            PostID   `xml:"id,attr" json:"id"`
	Score         int      `xml:"score,attr" json:"score"`
	FileURL       string   `xml:"file_url,attr" json:"file_url"`
	ParentID      PostID   `xml:"parent_id,attr" json:"parent_id"`
	Rating        Rating   `xml:"rating,attr" json:"rating"`
	Tags          TagsList `xml:"tags,attr" json:"tags"`
	SampleURL     string   `xml:"sample_url,attr" json:"sample_url"`
	SampleWidth   int      `xml:"sample_width,attr" json:"sample_width"`
	SampleHeight  int      `xml:"sample_height,attr" json:"sample_height"`
	PreviewURL    string   `xml:"preview_url,attr" json:"preview_url"`
	PreviewWidth  int      `xml:"preview_width,attr" json:"preview_width"`
	PreviewHeight int      `xml:"preview_height,attr" json:"preview_height"`
	Width         int      `xml:"width,attr" json:"width"`
	Height        int      `xml:"height,attr" json:"height"`
	MD5           string   `xml:"md5,attr" json:"md5"`
	CreatorID     int      `xml:"creator_id,attr" json:"creator_id"`
	CreatedAt     Date     `xml:"created_at,attr" json:"created_at"`
	ChangedAt     UnixTime `xml:"change,attr" json:"changed_at"`
	Status        string   `xml:"status,attr" json:"status"`
	Source        string   `xml:"source,attr" json:"source"`
	HasNotes      string   `xml:"has_notes,attr" json:"has_notes"`
	HasComments   string   `xml:"has_comments,attr" json:"has_comments"`
	HasChildren   bool     `xml:"has_children,attr" json:"has_children"`
}

// Date is a date from the hypnohub API.
type Date time.Time

// Time returns the time.Time representation of the date.
func (d Date) Time() time.Time {
	return time.Time(d)
}

// UnmarshalText parses a text-encoded date from the hypnohub API.
func (d *Date) UnmarshalText(b []byte) error {
	t, err := time.Parse("Mon Jan 2 15:04:05 -0700 2006", string(b))
	if err != nil {
		return err
	}
	*d = Date(t)
	return nil
}

// Rating is the rating of a post.
type Rating string

const (
	RatingSafe         Rating = "s"
	RatingQuestionable Rating = "q"
	RatingExplicit     Rating = "e"
)

// Normalize converts full or short rating names to the canonical short form ("s", "q", "e").
func (r Rating) Normalize() Rating {
	switch strings.ToLower(strings.TrimSpace(string(r))) {
	case "s", "safe":
		return RatingSafe
	case "q", "questionable":
		return RatingQuestionable
	case "e", "explicit":
		return RatingExplicit
	default:
		return r
	}
}

// Label returns a human-readable label for the rating.
func (r Rating) Label() string {
	switch r.Normalize() {
	case RatingSafe:
		return "Safe"
	case RatingQuestionable:
		return "Questionable"
	case RatingExplicit:
		return "Explicit"
	default:
		return string(r)
	}
}

// TagsList is a list of tags from the hypnohub API. It is a space-separated
// list of tags.
type TagsList string

// Split splits the tags list into a slice of non-empty tags.
func (t TagsList) Split() []string {
	return strings.Fields(string(t))
}

// FileExt returns the lowercase file extension (without leading dot) of the post's FileURL.
func (p Post) FileExt() string {
	u := p.FileURL
	if idx := strings.IndexByte(u, '?'); idx != -1 {
		u = u[:idx]
	}
	if idx := strings.LastIndexByte(u, '.'); idx != -1 {
		return strings.ToLower(u[idx+1:])
	}
	return ""
}

// IsVideo returns whether the post's original file is a video (mp4 or webm).
func (p Post) IsVideo() bool {
	switch p.FileExt() {
	case "mp4", "webm":
		return true
	default:
		return false
	}
}

// IsAnimated returns whether the post is animated (video or gif) or tagged animated.
func (p Post) IsAnimated() bool {
	switch p.FileExt() {
	case "mp4", "webm", "gif":
		return true
	default:
		return false
	}
}

// TagID is a tag ID.
type TagID int

// Tag is a single tag from the hypnohub API.
type Tag struct {
	Type      TagType `xml:"type,attr"`
	ID        TagID   `xml:"id,attr"`
	Count     int     `xml:"count,attr"`
	Name      string  `xml:"name,attr"`
	Ambiguous bool    `xml:"ambiguous,attr"`
}

// TagType is some kind of tag type.
type TagType int

const (
	TagTypeGeneral   TagType = 0
	TagTypeArtist    TagType = 1
	TagTypeCopyright TagType = 3
	TagTypeCharacter TagType = 4
	TagTypeMeta      TagType = 5
)

// Client is a Hypnohub client.
type Client struct {
	HTTPClient *http.Client
}

// New creates a new default Hypnohub client.
func New() *Client {
	return &Client{
		HTTPClient: http.DefaultClient,
	}
}

// FromHTTPClient creates a new Hypnohub client from an existing HTTP client.
func FromHTTPClient(c *http.Client) *Client {
	return &Client{
		HTTPClient: c,
	}
}

// SearchPostsResult is the result of a search for posts on Hypnohub.
type SearchPostsResult struct {
	Posts  []Post `json:"posts"`
	Count  int    `json:"count"`
	Offset int    `json:"offset"`
}

// SearchPostsOptions specifies options for searching posts.
type SearchPostsOptions struct {
	Query      string
	PageOffset int
	Limit      int // up to MaxSearchPostsLimit (1000); 0 uses server default (100)
}

// MaxSearchPostsLimit is the maximum number of posts Hypnohub returns per page.
const MaxSearchPostsLimit = 1000

// SearchPosts searches for posts on Hypnohub using the default page size (100).
func (c *Client) SearchPosts(ctx context.Context, query string, postOffset int) (*SearchPostsResult, error) {
	return c.SearchPostsWithOptions(ctx, SearchPostsOptions{
		Query:      query,
		PageOffset: postOffset,
	})
}

// SearchPostsWithOptions searches for posts on Hypnohub with custom pagination options.
func (c *Client) SearchPostsWithOptions(ctx context.Context, opts SearchPostsOptions) (*SearchPostsResult, error) {
	q := url.Values{
		"page": {"dapi"},
		"s":    {"post"},
		"q":    {"index"},
		"tags": {opts.Query},
		"pid":  {strconv.Itoa(opts.PageOffset)},
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	endpoint := "https://hypnohub.net/index.php?" + q.Encode()

	type Response struct {
		XMLName xml.Name `xml:"posts"`
		Count   int      `xml:"count,attr"`
		Offset  int      `xml:"offset,attr"`
		Posts   []Post   `xml:"post"`
	}

	response, err := getXML[Response](ctx, c.HTTPClient, endpoint)
	if err != nil {
		return nil, err
	}

	return &SearchPostsResult{
		Posts:  response.Posts,
		Count:  response.Count,
		Offset: response.Offset,
	}, nil
}

// SearchTagsResult is the result of a search for tags on Hypnohub.
type SearchTagsResult struct {
	Tags []Tag `json:"tags"`
}

// SearchTags searches for tags on Hypnohub.
func (c *Client) SearchTags(ctx context.Context, query string, afterID int) (*SearchTagsResult, error) {
	q := url.Values{
		"page":         {"dapi"},
		"s":            {"tag"},
		"q":            {"index"},
		"name_pattern": {"%" + query + "%"},
		// json is not supported for tag search?? :D
	}
	if afterID != 0 {
		q["after_id"] = []string{strconv.Itoa(afterID)}
	}
	endpoint := "https://hypnohub.net/index.php?" + q.Encode()

	type tagResponse struct {
		XMLName xml.Name `xml:"tags"`
		Tags    []Tag    `xml:"tag"`
	}
	resp, err := getXML[tagResponse](ctx, c.HTTPClient, endpoint)
	if err != nil {
		return nil, err
	}

	// Put best tags first.
	slices.Reverse(resp.Tags)

	return &SearchTagsResult{resp.Tags}, nil
}

type xmlResponse struct {
	XMLName xml.Name `xml:"response"`
	Success *bool    `xml:"success,attr"`
	Error   string   `xml:"reason,attr"`
}

func getXML[T any](ctx context.Context, c *http.Client, rawURL string) (*T, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}

	r, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get %s: %w", rawURL, err)
	}
	defer r.Body.Close()

	if r.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get %s: %s", rawURL, r.Status)
	}

	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var v T
	if err := xml.Unmarshal(b, &v); err == nil {
		return &v, nil
	}

	var response xmlResponse
	if err := xml.Unmarshal(b, &response); err == nil {
		if response.Success != nil && !*response.Success {
			if response.Error == "" {
				return nil, errors.New("server error")
			}
			return nil, fmt.Errorf("server error: %s", response.Error)
		}
		return nil, errors.New("server errored but did not provide a reason")
	}

	return nil, fmt.Errorf("server errored with status %s", r.Status)
}
