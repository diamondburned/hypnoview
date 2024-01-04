package hypnohub

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
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
	ID            PostID   `xml:"id,attr"`
	Score         int      `xml:"score,attr"`
	FileURL       string   `xml:"file_url,attr"`
	ParentID      PostID   `xml:"parent_id,attr"`
	Rating        Rating   `xml:"rating,attr"`
	Tags          TagsList `xml:"tags,attr"`
	SampleURL     string   `xml:"sample_url,attr"`
	SampleWidth   int      `xml:"sample_width,attr"`
	SampleHeight  int      `xml:"sample_height,attr"`
	PreviewURL    string   `xml:"preview_url,attr"`
	PreviewWidth  int      `xml:"preview_width,attr"`
	PreviewHeight int      `xml:"preview_height,attr"`
	Width         int      `xml:"width,attr"`
	Height        int      `xml:"height,attr"`
	MD5           string   `xml:"md5,attr"`
	CreatorID     int      `xml:"creator_id,attr"`
	CreatedAt     Date     `xml:"created_at,attr"`
	ChangedAt     UnixTime `xml:"change,attr"`
	Status        string   `xml:"status,attr"`
	Source        string   `xml:"source,attr"`
	HasNotes      string   `xml:"has_notes,attr"`
	HasComments   string   `xml:"has_comments,attr"`
	HasChildren   bool     `xml:"has_children,attr"`
}

// Date is a date from the hypnohub API.
type Date time.Time

// Time returns the time.Time representation of the date.
func (d Date) Time() time.Time {
	return time.Time(d)
}

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
	RatingSafe         Rating = "safe"
	RatingQuestionable Rating = "questionable"
	RatingExplicit     Rating = "explicit"
)

// TagsList is a list of tags from the hypnohub API. It is a space-separated
// list of tags.
type TagsList string

// Split splits the tags list into a slice of tags.
func (t TagsList) Split() []string {
	return strings.Split(string(t), " ")
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

// SearchPostsResult is the result of a search for posts on Hypnohub.
type SearchPostsResult struct {
	Posts  []Post `json:"posts"`
	Count  int    `json:"count"`
	Offset int    `json:"offset"`
}

// SearchPosts searches for posts on Hypnohub.
func (d *Client) SearchPosts(ctx context.Context, query string, postOffset int) (*SearchPostsResult, error) {
	q := url.Values{
		"page": {"dapi"},
		"s":    {"post"},
		"q":    {"index"},
		"tags": {query},
		"pid":  {strconv.Itoa(int(postOffset))},
	}
	url := "https://hypnohub.net/index.php?" + q.Encode()

	type Response struct {
		XMLName xml.Name `xml:"posts"`
		Count   int      `xml:"count,attr"`
		Offset  int      `xml:"offset,attr"`
		Posts   []Post   `xml:"post"`
	}

	response, err := getXML[Response](ctx, d.HTTPClient, url)
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
func (d *Client) SearchTags(ctx context.Context, query string, afterID int) (*SearchTagsResult, error) {
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
	url := "https://hypnohub.net/index.php?" + q.Encode()

	type tagResponse struct {
		XMLName xml.Name `xml:"tags"`
		Tags    []Tag    `xml:"tag"`
	}
	resp, err := getXML[tagResponse](ctx, d.HTTPClient, url)
	if err != nil {
		return nil, err
	}

	// Put best tags first.
	slices.Reverse(resp.Tags)

	return &SearchTagsResult{resp.Tags}, nil
}

func getJSON[T any](ctx context.Context, c *http.Client, url string) (*T, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	r, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get %s: %w", url, err)
	}
	defer r.Body.Close()

	if r.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get %s: %s", url, r.Status)
	}

	// The Hypnohub API (which uses rule34.xxx/gelbooru) is so god awful that
	// even when returning XML data, it still sets the Content-Type header to
	// application/json.

	rbuffered := bufio.NewReader(r.Body)
	starting, err := rbuffered.Peek(len("<?xml"))
	if err == nil && bytes.Equal(starting, []byte("<?xml")) {
		var response struct {
			Success *bool  `xml:"success,attr"`
			Error   string `xml:"reason,attr"`
		}
		if err := xml.NewDecoder(rbuffered).Decode(&response); err != nil {
			return nil, fmt.Errorf("failed to decode unexpected XML response: %w", err)
		}
		if response.Success != nil && !*response.Success {
			return nil, fmt.Errorf("server error: %s", response.Error)
		}
		return nil, fmt.Errorf("server errored but did not provide a reason")
	}

	var v T
	if err := json.NewDecoder(rbuffered).Decode(&v); err != nil {
		return nil, fmt.Errorf("failed to decode JSON response: %w", err)
	}

	return &v, nil
}

type xmlResponse struct {
	XMLName xml.Name `xml:"response"`
	Success *bool    `xml:"success,attr"`
	Error   string   `xml:"reason,attr"`
}

func getXML[T any](ctx context.Context, c *http.Client, url string) (*T, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	r, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get %s: %w", url, err)
	}
	defer r.Body.Close()

	if r.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get %s: %s", url, r.Status)
	}

	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	r.Body.Close()

	var v T
	if err := xml.Unmarshal(b, &v); err == nil {
		return &v, nil
	}

	var response xmlResponse
	if err := xml.Unmarshal(b, &response); err == nil {
		if response.Success != nil && !*response.Success {
			if response.Error == "" {
				return nil, fmt.Errorf("server error")
			}
			return nil, fmt.Errorf("server error: %s", response.Error)
		}
		return nil, fmt.Errorf("server errored but did not provide a reason")
	}

	return nil, fmt.Errorf("server errored with status %s", r.Status)
}
