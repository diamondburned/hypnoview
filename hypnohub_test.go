package hypnohub

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
)

func ExampleClient_SearchPosts() {
	client := New()

	result, err := client.SearchPosts(context.TODO(), "dazed comic", 0)
	if err != nil {
		log.Fatalln(err)
	}

	fmt.Println("found", len(result.Posts), "posts")
	// Output: found 100 posts
}

func ExampleClient_SearchTags() {
	client := New()

	result, err := client.SearchTags(context.TODO(), "dazed", 0)
	if err != nil {
		log.Fatalln(err)
	}

	fmt.Println("first tag is", result.Tags[0].Name)
	// Output: first tag is dazed
}

func TestParsePostViewTags(t *testing.T) {
	fixture := `
	<div id="left-col">
		<ul id="tag-sidebar">
			<li><h6>Copyright</h6></li>
			<li class="tag-type-copyright tag">
				<a href="index.php?page=wiki&s=list&search=marvel_comics">?</a>
				<a href="index.php?page=post&amp;s=list&amp;tags=marvel_comics">marvel comics</a>
				<span class="tag-count">3284</span>
			</li>
			<li class="tag-type-copyright tag">
				<a href="index.php?page=wiki&s=list&search=d%26d">?</a>
				<a href="index.php?page=post&amp;s=list&amp;tags=d%26d">d&amp;d</a>
				<span class="tag-count">120</span>
			</li>
			<li><h6>Character</h6></li>
			<li class="tag-type-character tag">
				<a href="index.php?page=wiki&s=list&search=elise_%28nisemono%29">?</a>
				<a href="index.php?page=post&amp;s=list&amp;tags=elise_%28nisemono%29">elise (nisemono)</a>
				<span class="tag-count">25</span>
			</li>
			<li class="tag-type-character tag">
				<a href="index.php?page=wiki&s=list&search=jack_o%27_valentine">?</a>
				<a href="index.php?page=post&amp;s=list&amp;tags=jack_o%27_valentine">jack o&#039; valentine</a>
				<span class="tag-count">42</span>
			</li>
			<li><h6>Artist</h6></li>
			<li class="tag-type-artist tag">
				<a href="index.php?page=wiki&s=list&search=thedrawing">?</a>
				<a href="index.php?page=post&amp;s=list&amp;tags=thedrawing">thedrawing</a>
				<span class="tag-count">43</span>
			</li>
			<li><h6>General</h6></li>
			<li class="tag-type-general tag">
				<a href="index.php?page=wiki&s=list&search=glowing_eyes">?</a>
				<a href="index.php?page=post&amp;s=list&amp;tags=glowing_eyes">glowing eyes</a>
				<span class="tag-count">23705</span>
			</li>
			<li><h6>Meta</h6></li>
			<li class="tag-type-metadata tag">
				<a href="index.php?page=wiki&s=list&search=simple_background">?</a>
				<a href="index.php?page=post&amp;s=list&amp;tags=simple_background">simple background</a>
				<span class="tag-count">23261</span>
			</li>
			<li class="tag-type-meta tag">
				<a href="index.php?page=post&amp;s=list&amp;tags=highres">highres</a>
			</li>
		</ul>
	</div>`

	got := ParsePostViewTags(fixture)
	want := map[string]TagType{
		"marvel_comics":     TagTypeCopyright,
		"d&d":               TagTypeCopyright,
		"elise_(nisemono)":  TagTypeCharacter,
		"jack_o'_valentine": TagTypeCharacter,
		"thedrawing":        TagTypeArtist,
		"glowing_eyes":      TagTypeGeneral,
		"simple_background": TagTypeMeta,
		"highres":           TagTypeMeta,
	}

	if len(got) != len(want) {
		t.Fatalf("ParsePostViewTags len = %d, want %d (got=%v)", len(got), len(want), got)
	}
	for k, expectedType := range want {
		if actual, ok := got[k]; !ok || actual != expectedType {
			t.Errorf("ParsePostViewTags[%q] = %v (ok=%v), want %v", k, actual, ok, expectedType)
		}
	}
}

func TestTagTypeMethods(t *testing.T) {
	if TagCategoryUnknown.Normalize() != TagTypeGeneral {
		t.Errorf("TagCategoryUnknown.Normalize() = %v, want TagTypeGeneral", TagCategoryUnknown.Normalize())
	}
	order := []TagType{
		TagTypeCopyright,
		TagTypeCharacter,
		TagTypeArtist,
		TagTypeGeneral,
		TagTypeMeta,
	}
	wantLabels := []string{"Copyright", "Character", "Artist", "General", "Meta"}
	wantSlugs := []string{"copyright", "character", "artist", "general", "meta"}

	for i, cat := range order {
		if got := cat.SortOrder(); got != i {
			t.Errorf("%v.SortOrder() = %d, want %d", cat, got, i)
		}
		if got := cat.Label(); got != wantLabels[i] {
			t.Errorf("%v.Label() = %q, want %q", cat, got, wantLabels[i])
		}
		if got := cat.Slug(); got != wantSlugs[i] {
			t.Errorf("%v.Slug() = %q, want %q", cat, got, wantSlugs[i])
		}
	}
}

func TestClientPostViewTagsAndTagByName(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("page") == "post" && q.Get("s") == "view" && q.Get("id") == "287547" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<ul id="tag-sidebar">
				<li class="tag-type-copyright tag"><a href="?">?</a><a href="#">bleach</a></li>
				<li class="tag-type-character tag"><a href="?">?</a><a href="#">orihime inoue</a></li>
			</ul>`)
			return
		}
		if q.Get("page") == "dapi" && q.Get("s") == "tag" && q.Get("name") == "bleach" {
			w.Header().Set("Content-Type", "text/xml; charset=utf-8")
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><tags type="array"><tag type="3" count="934" name="bleach" ambiguous="false" id="490"/></tags>`)
			return
		}
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><tags type="array"></tags>`)
	}))
	defer ts.Close()

	client := FromHTTPClient(ts.Client())
	client.BaseURL = ts.URL

	tags, err := client.PostViewTags(context.Background(), 287547)
	if err != nil {
		t.Fatalf("PostViewTags failed: %v", err)
	}
	if tags["bleach"] != TagTypeCopyright || tags["orihime_inoue"] != TagTypeCharacter {
		t.Errorf("unexpected PostViewTags: %v", tags)
	}

	tag, err := client.TagByName(context.Background(), "bleach")
	if err != nil {
		t.Fatalf("TagByName(bleach) failed: %v", err)
	}
	if tag == nil || tag.Type != TagTypeCopyright || tag.Count != 934 {
		t.Errorf("unexpected TagByName(bleach): %+v", tag)
	}

	missing, err := client.TagByName(context.Background(), "nonexistent_tag")
	if err != nil || missing != nil {
		t.Errorf("expected (nil, nil) for missing tag, got (%+v, %v)", missing, err)
	}
}
