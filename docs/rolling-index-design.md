# Rolling Popularity Index & Frontend Design

## 1. Background & Problem Statement

`hypnoview` started with two goals:
1. Bring back the **Popular** feature from the old Hypnohub instance (sliding windows of top posts over a Day, Week, Month, etc.).
2. Provide a clean, mobile-friendly frontend for browsing Hypnohub directly.

### How the Current Implementation Works (`cmd/popular-query-app` & `lib/hypnohub/popular`)

Currently, `hypnoview` does not host an index or render posts itself. Instead, when a user requests a popular time period:
1. It calculates a calendar cutoff timestamp (`EarliestTimestampForPeriod` in `lib/hypnohub/popular/popularity_times.go`) truncated to midnight UTC boundaries (e.g., start of yesterday, start of last week, start of the month).
2. On a cache miss, it runs `EstimatePostHistory` (`lib/hypnohub/popular/estimate_history.go`), which executes a binary search over Hypnohub's paginated post list (`pid` offset up to `3000`) to find the first post ID created after the cutoff timestamp.
3. It returns a Hypnohub search query string (`sort:score:desc id:>=<post_id>`) and redirects the user to `https://hypnohub.net/index.php?page=post&s=list&tags=...`.

### Flaws in the Current Design

1. **Request-path latency and fragile binary search**:
   - Cache misses block the user while performing multiple sequential HTTP round-trips to Hypnohub's XML API.
   - `EstimatePostMaxOffsets` hardcodes upper page bounds (`Daily: 200`, `Weekly: 800`, `Monthly: 3000`). If upload volume spikes, the binary search hits the ceiling and returns an inaccurate boundary; if posts are deleted mid-search, page offsets shift and require oscillation heuristics (`last2IntsDifference < 3`) to avoid infinite loops.
2. **Calendar boundary cliff ("midnight reset" problem)**:
   - Because time windows are truncated to calendar boundaries (midnight UTC, Monday 00:00, 1st of the month), crossing a boundary shifts the window abruptly. `popularity_times.go` works around this with ad-hoc heuristics (such as including yesterday's posts or pushing the window back a week on non-Sundays, and pushing back a month when `now.Day() <= 14`).
   - Even with those workarounds, the window size fluctuates wildly depending on the time of day or day of the week (e.g., "Weekly" spans anywhere from 7 to 13 days; "Monthly" spans 15 to 44 days).
3. **Cumulative score bias against new posts**:
   - Delegating sorting to Hypnohub via `sort:score:desc id:>=<post_id>` sorts strictly by **total cumulative score**.
   - Within any window (e.g., 24h or 7d), a post uploaded 23 hours ago has had 23 hours of exposure to accumulate upvotes, whereas a post uploaded 2 hours ago has only had 2 hours. Older posts almost always dominate the top of the list simply because they have existed longer, burying newer posts that are gaining upvotes at a much faster rate.
4. **No native browsing experience**:
   - Redirecting back to Hypnohub's native web UI forces the user onto a desktop-oriented layout heavy with ads and lacking mobile ergonomics.

---

## 2. Live Hypnohub API Findings

Testing against the live Hypnohub `dapi` endpoint (`https://hypnohub.net/index.php?page=dapi&s=post&q=index`) reveals several key properties and constraints that shape the new design:

1. **No View Counts**:
   - Neither the XML API (`dapi`), the JSON API (`json=1`), nor the HTML post view page (`page=post&s=view`) exposes post view counts.
   - **`score` (upvotes/favorites) is the sole engagement metric available.**
2. **Batch Size (`limit=1000`)**:
   - While Hypnohub defaults to 100 posts per page when `limit` is omitted, passing `&limit=1000` works and returns up to **1,000 posts in a single HTTP request** (~970 KB of XML).
   - Pagination with `limit=1000&pid=<page>` is 0-indexed (`pid=0` returns posts 0–999, `pid=1` returns 1,000–1,999, etc.).
3. **Upload Volume**:
   - Measuring `created_at` timestamps across pages on Hypnohub shows:
     - **~80 to 150 posts/day** (~7 to 12 posts per 2-hour window).
     - **~1,000 posts every 8 to 10 days**.
     - **~3,500 to 4,000 posts per 30-day month**.
   - Consequence: **The entire 30-day rolling window fits in just 4 HTTP requests** (`pid=0..3` with `limit=1000`).
4. **XML vs. JSON API Completeness**:
   - Adding `&json=1` returns JSON, but **omits `created_at`, `creator_id`, `source`, `status`, `has_notes`, `has_comments`, and `has_children`**.
   - The XML endpoint (`dapi` without `json=1`) includes every field, including exact `created_at` (`"Mon Jan 2 15:04:05 -0700 2006"`), `change` (Unix timestamp), dimensions, and all three media URLs (`preview_url`, `sample_url`, `file_url`). We should continue using the XML endpoint.
5. **What Updates `change` (`sort:updated:desc`)**:
   - Querying `tags=sort:updated:desc` orders posts by their `change` Unix timestamp descending.
   - However, on Gelbooru 0.2 / Hypnohub, `change` is updated on tag/note/metadata edits, **not** reliably on every vote. Therefore, `sort:updated:desc` cannot be trusted to catch all score changes—scanning the rolling window via `limit=1000` pages is both necessary and cheap enough to do directly.

---

## 3. Proposed Architecture: Rolling 2-Hour Background Indexer

Instead of calculating search queries on demand, `hypnoview` will run a background indexer every **2 hours** (or 1 hour, configurable) that snapshots post metadata and scores into a local **SQLite database**.

```
+-------------------------------------------------------------------+
|                        hypnoview Server                           |
|                                                                   |
|  +----------------------+         +----------------------------+  |
|  |  Background Indexer  |         |       HTTP / Web UI        |  |
|  |  (Ticker: every 2h)  |         |  (Mobile-friendly Booru)   |  |
|  +----------+-----------+         +-------------+--------------+  |
|             |                                   ^                 |
|             | Upserts posts &                   | Queries ranked  |
|             | inserts score snapshots           | posts & URLs    |
|             v                                   |                 |
|  +----------------------------------------------+--------------+  |
|  |                      SQLite Database                        |  |
|  |   - posts (metadata, tags, preview/sample/file URLs)        |  |
|  |   - post_tags (normalized tag join table for fast filtering)|  |
|  |   - score_snapshots (post_id, poll_time, score)             |  |
|  +-------------------------------------------------------------+  |
+-------------------------------------------------------------------+
              |                                   ^
              | 4 HTTP GETs / 2h                  | Direct <img src>
              | (limit=1000, pid=0..3)            | to Hypnohub CDN
              v                                   |
+-------------------------------------------------------------------+
|                           hypnohub.net                            |
+-------------------------------------------------------------------+
```

### Key Design Properties

1. **Zero User Wait Time**:
   - Every user request is served directly from local SQLite queries in a few milliseconds.
   - No Hypnohub API requests happen on the user request path.
2. **True Sliding Windows (No Midnight Cliff)**:
   - Because polls happen continuously every 2 hours, "Today" is always a true sliding 24-hour window (`[now_bucket - 24h, now_bucket]`), "This Week" is always `[now_bucket - 7d, now_bucket]`, and "This Month" is always `[now_bucket - 30d, now_bucket]`.
   - There is no midnight reset where the window shrinks to 0 hours and produces noisy rankings.
3. **Metadata & Media URL Indexing (Zero Asset Bandwidth)**:
   - `hypnoview` stores post metadata and the three remote asset URLs (`preview_url`, `sample_url`, `file_url`) alongside their width and height (`preview_width/height`, `sample_width/height`, `width/height`).
   - The client browser loads thumbnails, samples, and full images/videos directly from `hypnohub.net` (or its CDN). `hypnoview` never downloads or proxies image/video bytes.
   - Storing dimensions in the index lets the mobile frontend reserve exact aspect-ratio boxes before images load, eliminating layout shift (CLS).

---

## 4. Polling Strategy & Cost Analysis

### What Gets Polled Every 2 Hours

At each 2-hour tick (`T_now`, truncated to the 2-hour boundary, e.g., `00:00`, `02:00`, `04:00`, ... UTC):

1. Fetch pages `pid = 0, 1, 2, ...` with `limit=1000` (unfiltered, newest first).
2. Stop pagination as soon as a page contains a post with `created_at < T_now - 30 days` (typically 4 pages = 4,000 posts).
3. In a single SQLite transaction:
   - Upsert all fetched posts into the `posts` table (updating `score`, `tags`, `rating`, `change`, URLs, etc.).
   - Record a snapshot row `(post_id, poll_time, score)` in `score_snapshots` for each fetched post.

### Bandwidth & Storage Footprint

- **Hypnohub API load**:
  - 4 HTTP requests every 2 hours = **48 requests/day** (~4 MB/day total XML transferred).
  - With a 1-hour interval: 96 requests/day (~8 MB/day).
  - This is negligible for both `hypnoview` and `hypnohub.net`.
- **SQLite Storage**:
  - ~4,000 active posts per 30 days × 12 polls/day = 48,000 snapshot rows/day.
  - Each `(post_id, poll_time, score)` row in a `WITHOUT ROWID` table is ~16 bytes → **~770 KB/day** (~23 MB/month, ~280 MB/year).
  - Downsampling/compaction (keep 2-hour resolution for 48 hours, 6-hour resolution for 14 days, 24-hour resolution for 90 days, and prune older snapshots while keeping `posts`) caps the snapshot table at **under 25 MB indefinitely**.

### Cold-Start / Bootstrap Behavior

When `hypnoview` starts for the first time on an empty database:
1. It immediately runs an initial 30-day sweep (4 HTTP requests, takes ~2–3 seconds).
2. Before the second 2-hour poll completes, we only have a single snapshot per post (`t_now`), wait—we also know each post's exact `created_at` timestamp, and we know its score at `created_at` was `0`!
3. Therefore, **even on the very first poll (cold start)**, we know:
   - `score(created_at) = 0`
   - `score(t_now) = current_score`
   - `elapsed = t_now - created_at`
   This means age-normalized rate ranking works immediately from minute zero, and becomes progressively more granular as 2-hour snapshots accumulate!

---

## 5. Popularity & Rate-Based Ranking Model

### Why Raw Score Fails

If Post A was uploaded 22 hours ago and has `score = 20`, and Post B was uploaded 2 hours ago and has `score = 12`:
- Raw `sort:score:desc` ranks Post A (`20`) above Post B (`12`).
- Yet Post A gained `0.91 upvotes/hour`, while Post B gained `6.0 upvotes/hour`. Post B is clearly much hotter right now.

### Two Complementary Signals Enabled by 2-Hour Snapshots

Because we have both `created_at` and every-2-hour score snapshots `S(p, t)`, we can measure two distinct kinds of popularity:

#### Signal 1: Windowed Score Delta (`ΔS`) — "Most Upvoted in Window"
For any sliding window `W` (e.g., `2h`, `24h`, `7d`, `30d`), a post's score gain strictly within `[t_now - W, t_now]` is:

```
ΔS(p, W) = S(p, t_now) - S_baseline(p, t_now - W)
```

Where `S_baseline(p, t_now - W)` is:
- `0` if the post was created after `t_now - W` (since it started at 0 inside the window).
- The recorded snapshot `S(p, t_earliest >= t_now - W)` if the post existed before the window started.

#### Signal 2: Superlinear Momentum Gravity (Hot / Trending Ranking)

For posts created within the window (or gaining votes within the window), raw velocity $\Delta S / \Delta t$ suffers from scale invariance ($2\text{ votes in }1\text{h} = 48\text{ votes in }24\text{h}$) and small-sample instability. Furthermore, classic sublinear time decay where $g > p$ (such as dividing by $(\Delta t + \tau)^{1.35}$) causes continuous decay over time ($v \cdot \Delta t^{-0.35}$), penalizing sustained popularity and over-rewarding tiny 1–2 hour bursts.

To reward sustained popularity over brief bursts while scaling across both short and multi-day windows, `hypnoview` uses **Superlinear Momentum Gravity ($F2$) with Window-Proportional Exposure Damping ($\tau_W$)**:

```
HotScore(p, W) = (ΔS(p, W) / (eff_age_hours + τ_W)) * max(1, S)^0.35
```

Where:
- `eff_age_hours = min(W_hours, (t_now - created_at) in hours)` is the effective exposure time inside sliding window `W`.
- `max(1, S)^0.35` provides a sublinear mass multiplier based on total score $S$. When $\Delta S \approx S$, the effective numerator exponent is $p = 1.35 > 1.0$, which decisively rewards posts that sustain a high rate over many hours.
- `τ_W` is the **window-proportional damping schedule**, scaling the baseline exposure horizon to match the window duration:
  - `6h` (`Rising`): $\tau_W = 1.5\text{h}$
  - `24h` (`Today`): $\tau_W = 2.0\text{h}$
  - `3d` (`3 Days`): $\tau_W = 6.0\text{h}$
  - `7d` (`Week`): $\tau_W = 12.0\text{h}$
  - `14d` (`2 Weeks`): $\tau_W = 24.0\text{h}$
  - `30d` (`Month`): $\tau_W = 48.0\text{h}$
  - `all` (`All Indexed`): $\tau_W = 168.0\text{h}$

For detailed mathematical proofs, failure mode analyses, literature comparisons (Hacker News, Reddit, Danbooru, Bayesian shrinkage), and empirical benchmarks on the 100,000-post `hypnoview.db`, see [Popularity Ranking & Hotness Algorithm Specification](popularity-ranking.md).

### Comparison Example (24h Window, `τ_W = 2h`)

| Post | Age | Score ($S$) | Delta ($\Delta S$) | Raw Rate ($\Delta S / \text{age}$) | HotScore ($\frac{\Delta S}{\text{age} + 2} \cdot S^{0.35}$) | Hot Rank |
|---|---|---|---|---|---|---|
| **Post C** (viral new post) | 2h | 14 | 14 | $7.00/\text{h}$ | $\frac{14}{4} \cdot 14^{0.35} = \mathbf{8.80}$ | **#2** |
| **Post B** (day's top performer) | 21h | 100 | 100 | $4.76/\text{h}$ | $\frac{100}{23} \cdot 100^{0.35} = \mathbf{21.78}$ | **#1** |
| **Post A** (solid midday post) | 8h | 28 | 28 | $3.50/\text{h}$ | $\frac{28}{10} \cdot 28^{0.35} = \mathbf{8.98}$ | **#3** |
| **Post D** (brand new, 1 vote) | 0.2h | 1 | 1 | $5.00/\text{h}$ | $\frac{1}{2.2} \cdot 1^{0.35} = \mathbf{0.45}$ | **#4** |

Notice how:
- **Post B** (21 hours old, 100 upvotes) holds **#1** decisively: its sustained momentum over 21 hours is recognized as superior to Post C's early 2-hour burst.
- **Post C** (2 hours old, 14 upvotes) still earns **#2**, demonstrating agility in surfacing genuinely hot new uploads.
- **Post D** (12 minutes old, 1 upvote) does **not** spike to the top, tamed by the $+2\text{h}$ damping constant and unit mass ($1^{0.35} = 1$).

### Sort Modes in the UI

The UI provides clear sorting controls:
- **Hot (Rate & Momentum)**: Ranked by `HotScore(p, W)` (`score_delta / (eff_age + τ_W) * score^0.35`). Default for discovering trending content across any window.
- **Top (Total Gain)**: Ranked by `score_delta` strictly within window $W$. Best for discovering highest absolute volume over `7d`, `30d`, or `all`.
- **New (Recent)**: Ranked by creation timestamp descending.
- **Score (All-Time)**: Ranked by cumulative lifetime score descending.

---

## 6. Database Schema (SQLite)

Using SQLite (`modernc.org/sqlite` or `mattn/go-sqlite3`) in WAL mode gives us single-file persistence, concurrent reads while the 2-hour indexer writes, and fast analytical queries.

```sql
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA foreign_keys = ON;

-- Core post metadata and remote asset URLs
CREATE TABLE IF NOT EXISTS posts (
    id              INTEGER PRIMARY KEY,
    created_at      INTEGER NOT NULL, -- Unix timestamp (seconds)
    changed_at      INTEGER NOT NULL, -- Unix timestamp from 'change' attr
    score           INTEGER NOT NULL,
    rating          TEXT    NOT NULL, -- 's', 'q', 'e'
    creator_id      INTEGER NOT NULL,
    parent_id       INTEGER NOT NULL DEFAULT 0,
    md5             TEXT    NOT NULL,
    source          TEXT    NOT NULL,
    status          TEXT    NOT NULL,
    has_notes       INTEGER NOT NULL DEFAULT 0,
    has_comments    INTEGER NOT NULL DEFAULT 0,
    has_children    INTEGER NOT NULL DEFAULT 0,

    -- Original file metadata (no bytes downloaded)
    file_url        TEXT    NOT NULL,
    file_ext        TEXT    NOT NULL, -- 'jpg', 'png', 'gif', 'mp4', 'webm'
    width           INTEGER NOT NULL,
    height          INTEGER NOT NULL,

    -- Sample (medium resolution) URL & dimensions
    sample_url      TEXT    NOT NULL,
    sample_width    INTEGER NOT NULL,
    sample_height   INTEGER NOT NULL,

    -- Preview (thumbnail) URL & dimensions
    preview_url     TEXT    NOT NULL,
    preview_width   INTEGER NOT NULL,
    preview_height  INTEGER NOT NULL,

    -- Raw space-separated tag string for display
    tags_raw        TEXT    NOT NULL,

    first_seen_at   INTEGER NOT NULL,
    last_polled_at  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_posts_created_at ON posts(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_posts_score      ON posts(score DESC);

-- Time-series score snapshots taken every 2 hours
CREATE TABLE IF NOT EXISTS score_snapshots (
    post_id    INTEGER NOT NULL,
    poll_time  INTEGER NOT NULL, -- Unix timestamp truncated to 2h bucket
    score      INTEGER NOT NULL,
    PRIMARY KEY (post_id, poll_time),
    FOREIGN KEY (post_id) REFERENCES posts(id) ON DELETE CASCADE
) WITHOUT ROWID;

CREATE INDEX IF NOT EXISTS idx_snapshots_poll_time ON score_snapshots(poll_time);

-- Normalized tags for local tag filtering & autocomplete
CREATE TABLE IF NOT EXISTS tags (
    id         INTEGER PRIMARY KEY,
    name       TEXT     NOT NULL UNIQUE,
    type       INTEGER NOT NULL DEFAULT 0, -- 0=general, 1=artist, 3=copyright, 4=char, 5=meta
    post_count INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS post_tags (
    post_id    INTEGER NOT NULL,
    tag_id     INTEGER NOT NULL,
    PRIMARY KEY (post_id, tag_id),
    FOREIGN KEY (post_id) REFERENCES posts(id) ON DELETE CASCADE,
    FOREIGN KEY (tag_id)  REFERENCES tags(id)  ON DELETE CASCADE
) WITHOUT ROWID;

CREATE INDEX IF NOT EXISTS idx_post_tags_tag_id ON post_tags(tag_id, post_id);
```

### Example SQL Query: Sliding Window Rate & Delta Ranking

Because `score_snapshots` is indexed by `(post_id, poll_time)`, computing the score delta and rate over any sliding window `[T_start, T_now]` is a single fast query:

```sql
WITH baseline AS (
    -- Find the earliest snapshot for each post inside the window [T_start, T_now]
    SELECT
        s.post_id,
        s.score AS start_score,
        s.poll_time AS start_poll_time
    FROM score_snapshots s
    WHERE s.poll_time >= :t_start
      AND s.poll_time = (
          SELECT MIN(s2.poll_time)
          FROM score_snapshots s2
          WHERE s2.post_id = s.post_id
            AND s2.poll_time >= :t_start
      )
)
SELECT
    p.*,
    -- If the post was created inside the window, its baseline score at creation was 0.
    -- Otherwise, use its score at the start of the window.
    CASE
        WHEN p.created_at >= :t_start THEN p.score
        ELSE p.score - COALESCE(b.start_score, p.score)
    END AS score_delta,
    -- Effective exposure hours inside the window
    MAX(0.1, (MIN(:t_now, :t_now) - MAX(p.created_at, :t_start)) / 3600.0) AS eff_hours
FROM posts p
LEFT JOIN baseline b ON b.post_id = p.id
WHERE p.created_at >= :max_post_age -- e.g., :t_start for posts created in window, or :t_now - 30d
  AND p.status = 'active'
ORDER BY
    -- Damped velocity: score_delta / (eff_hours + tau)^gravity
    (CASE
        WHEN p.created_at >= :t_start THEN p.score
        ELSE p.score - COALESCE(b.start_score, p.score)
    END) / POW(MAX(0.1, (:t_now - MAX(p.created_at, :t_start)) / 3600.0) + :tau, :gravity) DESC,
    p.score DESC,
    p.id DESC
LIMIT :limit OFFSET :offset;
```

---

## 7. Mobile-Friendly Frontend Design

Since `hypnoview` now indexes `preview_url`, `sample_url`, `file_url`, dimensions, tags, and scores locally, it can serve as a complete, fast browsing frontend instead of bouncing users to `hypnohub.net`.

### Core UI Views

1. **Popular / Trending Feed (Default Landing Page)**:
   - **Sliding Window Selector**:
     - `Hot (24h)` — Rolling 24 hours, ranked by upvote rate (`ΔS / (age + τ)^g`).
     - `Hot (6h)` / `Rising` — Rolling 6 hours, highlighting brand-new posts gaining rapid traction.
     - `Week (7d)` — Rolling 7 days (`[now - 7d, now]`).
     - `Month (30d)` — Rolling 30 days (`[now - 30d, now]`).
   - **Sort Mode Toggle**:
     - `Hot (Velocity)` vs. `Top (Total Upvotes in Window)` vs. `New`.
   - **Quick Tag / Rating Filter Bar**:
     - Filter popular posts locally by tags (e.g., `-furry`, `animated`, `english_text`) or rating (`Explicit`, `Questionable`, `Safe`)—something Hypnohub's old Popular page never supported!
2. **Responsive Masonry / Justified Grid**:
   - Uses `preview_url` (`~250px` thumbnails) and pre-allocates aspect-ratio boxes using `preview_width` and `preview_height` from the database.
   - Displays subtle badges on each card:
     - Current score & window velocity (e.g., `★ 24 (+6.2/h)` or `+14 today`).
     - Media type indicator (`GIF`, `MP4`, `WEBM`, or multi-post parent/child indicator).
3. **Mobile Lightbox / Post Viewer**:
   - Tapping a thumbnail opens an instant swipeable overlay without a full page reload:
     - Loads `sample_url` (`850px` width) by default on mobile to save user cellular bandwidth, with a one-tap "HD (`width × height`)" toggle to load `file_url`.
     - Native `<video controls loop playsinline>` for `.mp4` / `.webm` posts (replacing Hypnohub's broken/ad-wrapped FluidPlayer on mobile).
     - Collapsible bottom sheet showing categorized tags (Artist, Character, Copyright, General), source link, parent/child post navigation, and a button to open the original post on `hypnohub.net` if the user wants to vote or comment.

---

## 8. Proposed Package & Directory Layout

```
hypnoview/
├── cmd/
│   ├── hypnoview/              # New unified server (indexer + mobile frontend + API)
│   └── popular-query-app/      # Legacy query redirector (can be deprecated or kept minimal)
├── docs/
│   └── rolling-index-design.md # This architecture & design document
└── lib/
    ├── httputil/               # Existing HTTP client middlewares
    ├── hypnohub/               # Existing Hypnohub XML API client
    │   ├── hypnohub.go         # Add Limit param support (limit=1000) + FileExt helper
    │   ├── popular/            # Existing binary-search estimator (legacy)
    │   └── query/              # Existing tag query DSL
    └── index/                  # New rolling SQLite indexer & popularity engine
        ├── db.go               # SQLite schema, migrations, and queries
        ├── poller.go           # Background 2-hour ticker & pagination sweep
        └── ranking.go          # Rate/velocity & sliding window ranking queries
```

---

## 9. Open Questions & Next Steps

1. **Scope of `score` tracking for older posts**:
   - Polling the newest ~4,000 posts (30 days) every 2 hours takes only 4 HTTP requests. Should we cap the active polling window at 30 days (`Monthly`), or occasionally poll deeper? (30 days covers Day, Week, and Month completely).
2. **Tag filtering on Popular**:
   - Since we receive `tags` on every post in the XML response, indexing them into `post_tags` lets users filter the Popular page by include/exclude tags (e.g., hiding disliked tags on mobile). Should we include tag filtering in v1 of the new index?
3. **Frontend stack**:
   - The repo currently uses Go + vanilla HTML/CSS/JS (with Deno formatting). Do we want to keep the new mobile frontend server-rendered in Go (`html/template` or `templ` + HTMX / vanilla ES modules) or use a lightweight client framework?
