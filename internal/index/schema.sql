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


