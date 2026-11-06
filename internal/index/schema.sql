-- Part 1: Core Post & Tag Metadata Index

CREATE TABLE posts (
	id             INTEGER PRIMARY KEY,
	created_at     INTEGER NOT NULL,
	changed_at     INTEGER NOT NULL,
	score          INTEGER NOT NULL,
	rating         TEXT    NOT NULL,
	creator_id     INTEGER NOT NULL,
	parent_id      INTEGER NOT NULL DEFAULT 0,
	md5            TEXT    NOT NULL,
	source         TEXT    NOT NULL,
	status         TEXT    NOT NULL,
	has_notes      INTEGER NOT NULL DEFAULT 0,
	has_comments   INTEGER NOT NULL DEFAULT 0,
	has_children   INTEGER NOT NULL DEFAULT 0,

	file_url       TEXT    NOT NULL,
	file_ext       TEXT    NOT NULL,
	width          INTEGER NOT NULL,
	height         INTEGER NOT NULL,

	sample_url     TEXT    NOT NULL,
	sample_width   INTEGER NOT NULL,
	sample_height  INTEGER NOT NULL,

	preview_url    TEXT    NOT NULL,
	preview_width  INTEGER NOT NULL,
	preview_height INTEGER NOT NULL,

	tags_raw       TEXT    NOT NULL,

	first_seen_at  INTEGER NOT NULL,
	last_polled_at INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_posts_created_at        ON posts(created_at DESC, id DESC);
CREATE INDEX idx_posts_score             ON posts(score DESC, id DESC);
CREATE INDEX idx_posts_rating_created_at ON posts(rating, created_at DESC, id DESC);

CREATE TABLE tags (
	id         INTEGER PRIMARY KEY,
	name       TEXT    NOT NULL UNIQUE,
	post_count INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX idx_tags_post_count ON tags(post_count DESC, name ASC);

CREATE TABLE post_tags (
	post_id INTEGER NOT NULL,
	tag_id  INTEGER NOT NULL,
	PRIMARY KEY (post_id, tag_id),
	FOREIGN KEY (post_id) REFERENCES posts(id) ON DELETE CASCADE,
	FOREIGN KEY (tag_id)  REFERENCES tags(id)  ON DELETE CASCADE
) WITHOUT ROWID, STRICT;

CREATE INDEX idx_post_tags_tag_id ON post_tags(tag_id, post_id);

-- Part 2: Time-Series Score Snapshots & Popularity Delta Tracking

CREATE TABLE poll_runs (
	poll_time      INTEGER PRIMARY KEY,
	started_at     INTEGER NOT NULL,
	completed_at   INTEGER NOT NULL,
	posts_upserted INTEGER NOT NULL,
	pages_fetched  INTEGER NOT NULL,
	is_seed        INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE TABLE score_snapshots (
	post_id   INTEGER NOT NULL,
	poll_time INTEGER NOT NULL,
	score     INTEGER NOT NULL,
	PRIMARY KEY (post_id, poll_time),
	FOREIGN KEY (post_id) REFERENCES posts(id) ON DELETE CASCADE
) WITHOUT ROWID, STRICT;

CREATE INDEX idx_score_snapshots_poll_time ON score_snapshots(poll_time, post_id);

--------------------------------- NEW VERSION ---------------------------------

DROP INDEX IF EXISTS idx_score_snapshots_poll_time;
CREATE INDEX IF NOT EXISTS idx_score_snapshots_poll_time ON score_snapshots(poll_time, post_id, score);

--------------------------------- NEW VERSION ---------------------------------

CREATE INDEX IF NOT EXISTS idx_posts_status ON posts(status);

--------------------------------- NEW VERSION ---------------------------------

DROP INDEX IF EXISTS idx_posts_status;
CREATE INDEX IF NOT EXISTS idx_posts_status_created_at ON posts(status, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_posts_status_score ON posts(status, score DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_posts_status_rating_created_at ON posts(status, rating, created_at DESC, id DESC);

--------------------------------- NEW VERSION ---------------------------------

CREATE TABLE IF NOT EXISTS pools (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL,
    updated_at  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS pool_posts (
    pool_id     INTEGER NOT NULL REFERENCES pools(id) ON DELETE CASCADE,
    post_id     INTEGER NOT NULL,
    seq         INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (pool_id, post_id)
);
CREATE INDEX IF NOT EXISTS idx_pool_posts_post_id ON pool_posts(post_id);

ALTER TABLE posts ADD COLUMN group_id         INTEGER NOT NULL DEFAULT 0;
ALTER TABLE posts ADD COLUMN group_is_latest  INTEGER NOT NULL DEFAULT 1;
ALTER TABLE posts ADD COLUMN group_page_count INTEGER NOT NULL DEFAULT 1;

CREATE INDEX IF NOT EXISTS idx_posts_parent_id ON posts(parent_id) WHERE parent_id > 0;
CREATE INDEX IF NOT EXISTS idx_posts_group_id  ON posts(group_id, id ASC) WHERE group_id > 0;
CREATE INDEX IF NOT EXISTS idx_posts_browse_collapsed_new   ON posts(status, group_is_latest, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_posts_browse_collapsed_score ON posts(status, group_is_latest, score DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_posts_browse_collapsed_rating_new ON posts(status, group_is_latest, rating, created_at DESC, id DESC);

WITH RECURSIVE
edges(u, v) AS (
    SELECT id, parent_id FROM posts WHERE parent_id > 0 AND parent_id != id
    UNION
    SELECT parent_id, id FROM posts WHERE parent_id > 0 AND parent_id != id
),
cc(node, comp) AS (
    SELECT u, u FROM edges
    UNION
    SELECT e.v, cc.comp
    FROM edges e
    JOIN cc ON e.u = cc.node
    WHERE cc.comp < e.v
),
existing_cc AS (
    SELECT p.id AS post_id, MIN(cc.comp) AS raw_comp
    FROM cc
    JOIN posts p ON p.id = cc.node
    GROUP BY p.id
),
comp_stats AS (
    SELECT
        raw_comp,
        MIN(post_id) AS canonical_group_id,
        COUNT(*) AS page_count,
        COALESCE(MAX(CASE WHEN p.status = 'active' THEN p.id END), MAX(p.id)) AS latest_post_id
    FROM existing_cc ec
    JOIN posts p ON p.id = ec.post_id
    GROUP BY raw_comp
    HAVING COUNT(*) > 1
),
resolved AS (
    SELECT
        ec.post_id,
        cs.canonical_group_id AS group_id,
        cs.page_count AS group_page_count,
        CASE WHEN ec.post_id = cs.latest_post_id THEN 1 ELSE 0 END AS group_is_latest
    FROM existing_cc ec
    JOIN comp_stats cs ON cs.raw_comp = ec.raw_comp
)
UPDATE posts
SET
    group_id         = COALESCE((SELECT r.group_id FROM resolved r WHERE r.post_id = posts.id), 0),
    group_page_count = COALESCE((SELECT r.group_page_count FROM resolved r WHERE r.post_id = posts.id), 1),
    group_is_latest  = COALESCE((SELECT r.group_is_latest FROM resolved r WHERE r.post_id = posts.id), 1)
WHERE parent_id > 0 OR has_children = 1;



