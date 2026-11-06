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
				<a href="index.php?page=tag&amp;s=edit&amp;id=123">edit</a>
				<span class="tag-count">43</span>
			</li>
			<li class="tag-type-artist tag">
				<a href="index.php?page=wiki&s=list&search=kuro_">?</a>
				<a href="index.php?page=post&amp;s=list&amp;tags=kuro_">kuro </a>
				<a href="#">?</a>
				<span class="tag-count">19</span>
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
		"kuro_":             TagTypeArtist,
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

	// HTML page with no <ul id="tag-sidebar"> must return nil even if <li class="tag-type-artist"> appears elsewhere.
	noSidebarHTML := `<div class="comments"><ul class="other"><li class="tag-type-artist"><a href="index.php?page=post&s=list&tags=fake_artist">fake_artist</a></li></ul></div>`
	if stray := ParsePostViewTags(noSidebarHTML); stray != nil {
		t.Errorf("expected nil when <ul id=\"tag-sidebar\"> is absent, got %v", stray)
	}
}

func TestTagTypeMethods(t *testing.T) {
	if TagTypeUnknown.Normalize() != TagTypeGeneral {
		t.Errorf("TagTypeUnknown.Normalize() = %v, want TagTypeGeneral", TagTypeUnknown.Normalize())
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

	tags, _, err := client.PostView(context.Background(), 287547)
	if err != nil {
		t.Fatalf("PostView failed: %v", err)
	}
	if tags["bleach"] != TagTypeCopyright || tags["orihime_inoue"] != TagTypeCharacter {
		t.Errorf("unexpected PostView tags: %v", tags)
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

func TestParsePostViewCommentsAndClientComments(t *testing.T) {
	singleCommentHTML := `
	<div id="comment-list">
		<script type="text/javascript">
			var posts = {}; posts[287540] = {}; posts[287540].comments = {};
		</script>
		<div id="c626853">
			<div class="horizontalFlexWithMargins">
				<div class="comment-left-col">
					<a href="index.php?page=account&amp;s=profile&amp;uname=Bootyhunter69">Bootyhunter69</a>
					<br />
					<div class="avatar">
						<img src="user_avatars/avatar_65769.jpg" style="width: 125px; height: auto" />
					</div>
				</div>
				<div class="comment-right-col">
					<div>
						<b>
							Posted on 2026-11-05 21:41:10 <br />
							Score: <a id="sc626853">1</a>
							(vote <a href="#" onclick="Javascript:vote('287540', '626853', 'up'); return false;">Up</a>)
						</b>
					</div>
					<div style="padding-left: 5px; margin-top: 10px; margin-bottom: 10px;">
						Aww, how... romantic(?)
					</div>
				</div>
			</div>
			<div style="text-align: right">(<a id="rc626853"></a>)</div>
		</div>
	</div>`

	single := ParsePostViewComments(singleCommentHTML)
	if len(single) != 1 {
		t.Fatalf("ParsePostViewComments single len = %d, want 1", len(single))
	}
	if single[0].ID != 626853 || single[0].PostID != 287540 || single[0].Creator != "Bootyhunter69" || single[0].CreatorID != 65769 || single[0].CreatedAt.RFC3339() != "2026-11-05T21:41:10Z" || single[0].CreatedAt.FormatComment() != "2026-11-05 21:41" || single[0].Score != 1 || single[0].Body != "Aww, how... romantic(?)" {
		t.Errorf("unexpected parsed single comment: %+v", single[0])
	}

	multiCommentHTML := `
	<div id="comment-list">
		<script type="text/javascript">
			var posts = {}; posts[287470] = {};
		</script>
		<div id="c626842">
			<div class="horizontalFlexWithMargins">
				<div class="comment-left-col">
					<a href="index.php?page=account&amp;s=profile&amp;id=1705818">Hurricane</a>
				</div>
				<div class="comment-right-col">
					<div>
						<b>
							Posted on 2026-11-05 18:42:29 <br />
							Score: <a id="sc626842">2</a>
						</b>
					</div>
					<div style="padding-left: 5px; margin-top: 10px; margin-bottom: 10px;">
						[quote]First line<br />Second line[/quote]<br />Come on, don&#039;t provoke &amp; test!<br />Line two
					</div>
				</div>
			</div>
			<div style="text-align: right">(<a id="rc626842"></a>)</div>
		</div>
		<div id="c626844">
			<div class="horizontalFlexWithMargins">
				<div class="comment-left-col">
					<a href="index.php?page=account&amp;s=profile&amp;uname=HypnoScream">HypnoScream</a>
					<div class="avatar"><img src="user_avatars/avatar_1696342.png" /></div>
				</div>
				<div class="comment-right-col">
					<div>
						<b>
							Posted on 2026-11-05 18:46:20 <br />
							Score: <a id="sc626844">-3</a>
						</b>
					</div>
					<div style="padding-left: 5px; margin-top: 10px; margin-bottom: 10px;">
						<div class="quote">HTML quote</div>Reply text
					</div>
				</div>
			</div>
			<hr class="light"/>
		</div>
		<div id="c626845">
			<div class="horizontalFlexWithMargins">
				<div class="comment-left-col"></div>
				<div class="comment-right-col"></div>
			</div>
		</div>
	</div>`

	multi := ParsePostViewComments(multiCommentHTML)
	if len(multi) != 2 {
		t.Fatalf("ParsePostViewComments multi len = %d, want 2 (empty comment must be skipped)", len(multi))
	}
	wantBody0 := "> First line\n> Second line\n\nCome on, don't provoke & test!\nLine two"
	if multi[0].ID != 626842 || multi[0].Creator != "Hurricane" || multi[0].CreatorID != 1705818 || multi[0].Score != 2 || multi[0].Body != wantBody0 {
		t.Errorf("unexpected multi[0]: %+v (wantBody=%q)", multi[0], wantBody0)
	}
	wantBody1 := "> HTML quote\n\nReply text"
	if multi[1].ID != 626844 || multi[1].Creator != "HypnoScream" || multi[1].CreatorID != 1696342 || multi[1].Score != -3 || multi[1].Body != wantBody1 {
		t.Errorf("unexpected multi[1]: %+v (wantBody=%q)", multi[1], wantBody1)
	}

	if got := cleanCommentBody(`[quote]a &lt;b&gt; c[/quote]`); got != `> a <b> c` {
		t.Errorf("cleanCommentBody([quote]a &lt;b&gt; c[/quote]) = %q, want %q", got, `> a <b> c`)
	}
	if got := cleanCommentBody(`[quote]1 &lt; 2<br />3 &gt; 2[/quote]`); got != "> 1 < 2\n> 3 > 2" {
		t.Errorf("cleanCommentBody([quote]1 &lt; 2<br />3 &gt; 2[/quote]) = %q, want %q", got, "> 1 < 2\n> 3 > 2")
	}
	if got := cleanCommentBody(`[quote=Bob]Hello[/quote]`); got != "> Bob wrote:\n> Hello" {
		t.Errorf("cleanCommentBody([quote=Bob]Hello[/quote]) = %q, want %q", got, "> Bob wrote:\n> Hello")
	}

	// Test Client.PostView HTML comments and Client.CommentsDAPI XML endpoint
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("page") == "post" && q.Get("s") == "view" && q.Get("id") == "287540" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, singleCommentHTML)
			return
		}
		if q.Get("page") == "dapi" && q.Get("s") == "comment" && q.Get("post_id") == "287999" {
			w.Header().Set("Content-Type", "text/xml; charset=utf-8")
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><comments type="array"><comment id="700001" post_id="287999" creator="FallbackUser" creator_id="42" created_at="2026-11-06 12:00" body="Fallback &amp; clean"/></comments>`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	client := FromHTTPClient(ts.Client())
	client.BaseURL = ts.URL

	_, c1, err := client.PostView(context.Background(), 287540)
	if err != nil || len(c1) != 1 || c1[0].Creator != "Bootyhunter69" {
		t.Fatalf("client.PostView(287540) = (%+v, %v)", c1, err)
	}
	c2, err := client.CommentsDAPI(context.Background(), 287999)
	if err != nil || len(c2) != 1 || c2[0].Creator != "FallbackUser" || c2[0].Body != "Fallback & clean" || c2[0].CreatedAt.FormatComment() != "2026-11-06 12:00" {
		t.Fatalf("client.CommentsDAPI(287999) = (%+v, %v)", c2, err)
	}
}
