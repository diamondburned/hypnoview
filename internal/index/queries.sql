-- name: InsertPollRun :exec
INSERT INTO poll_runs (poll_time, started_at, completed_at, posts_upserted, pages_fetched, is_seed)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(poll_time) DO UPDATE SET
    started_at     = excluded.started_at,
    completed_at   = excluded.completed_at,
    posts_upserted = excluded.posts_upserted,
    pages_fetched  = excluded.pages_fetched,
    is_seed        = MAX(poll_runs.is_seed, excluded.is_seed);

-- name: UpsertPost :exec
INSERT INTO posts (
    id, created_at, changed_at, score, rating, creator_id, parent_id,
    md5, source, status, has_notes, has_comments, has_children,
    file_url, file_ext, width, height,
    sample_url, sample_width, sample_height,
    preview_url, preview_width, preview_height,
    tags_raw, first_seen_at, last_polled_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?,
    ?, ?, ?,
    ?, ?, ?,
    ?, ?, ?
)
ON CONFLICT(id) DO UPDATE SET
    changed_at     = excluded.changed_at,
    score          = excluded.score,
    rating         = excluded.rating,
    parent_id      = excluded.parent_id,
    source         = excluded.source,
    status         = excluded.status,
    has_notes      = excluded.has_notes,
    has_comments   = excluded.has_comments,
    has_children   = excluded.has_children,
    file_url       = excluded.file_url,
    file_ext       = excluded.file_ext,
    width          = excluded.width,
    height         = excluded.height,
    sample_url     = excluded.sample_url,
    sample_width   = excluded.sample_width,
    sample_height  = excluded.sample_height,
    preview_url    = excluded.preview_url,
    preview_width  = excluded.preview_width,
    preview_height = excluded.preview_height,
    tags_raw       = excluded.tags_raw,
    last_polled_at = excluded.last_polled_at;

-- name: InsertScoreSnapshot :exec
INSERT INTO score_snapshots (post_id, poll_time, score)
VALUES (?, ?, ?)
ON CONFLICT(post_id, poll_time) DO UPDATE SET score = excluded.score;

-- name: DeletePostTagsByPostID :exec
DELETE FROM post_tags WHERE post_id = ?;

-- name: GetTagIDsByPostID :many
SELECT tag_id FROM post_tags WHERE post_id = ?;

-- name: InsertTag :exec
INSERT INTO tags (name, post_count)
VALUES (?, 0)
ON CONFLICT(name) DO NOTHING;

-- name: GetTagIDByName :one
SELECT id FROM tags WHERE name = ?;

-- name: InsertPostTag :exec
INSERT INTO post_tags (post_id, tag_id)
VALUES (?, ?)
ON CONFLICT(post_id, tag_id) DO NOTHING;

-- name: UpdateTagPostCount :exec
UPDATE tags
SET post_count = (
    SELECT COUNT(*)
    FROM post_tags pt
    JOIN posts p ON p.id = pt.post_id
    WHERE pt.tag_id = sqlc.arg(id) AND p.status = 'active'
)
WHERE id = sqlc.arg(id);

-- name: PruneOldSnapshots :exec
DELETE FROM score_snapshots WHERE poll_time < ?;

-- name: GetPostByID :one
SELECT * FROM posts WHERE id = ?;

-- name: GetSnapshotsByPostID :many
SELECT poll_time, score
FROM score_snapshots
WHERE post_id = ?
ORDER BY poll_time ASC;

-- name: ListPopularTags :many
SELECT name, post_count, category
FROM tags
WHERE post_count > 0
ORDER BY post_count DESC, name ASC
LIMIT ?;

-- name: SearchTagsByPrefix :many
SELECT
    name,
    post_count,
    category,
    like(sqlc.arg(prefix), name, '\') AS is_prefix
FROM tags
WHERE post_count > 0 AND like(sqlc.arg(pattern), name, '\')
ORDER BY
    is_prefix DESC,
    post_count DESC,
    name ASC
LIMIT sqlc.arg('limit');

-- name: CountPosts :one
SELECT COUNT(*) FROM posts;

-- name: CountTags :one
SELECT COUNT(*) FROM tags WHERE post_count > 0;

-- name: CountSnapshots :one
SELECT COUNT(*) FROM score_snapshots;

-- name: CountPollRuns :one
SELECT COUNT(*) FROM poll_runs;

-- name: GetPostMinMaxCreatedAt :one
SELECT MIN(created_at) AS min_created_at, MAX(created_at) AS max_created_at FROM posts;

-- name: GetLatestPollRun :one
SELECT poll_time, started_at, completed_at
FROM poll_runs
ORDER BY poll_time DESC
LIMIT 1;

-- name: CountSeedPollRuns :one
SELECT COUNT(*) FROM poll_runs WHERE is_seed = 1;

-- name: GetMaxPollTime :one
SELECT MAX(poll_time) AS max_poll_time FROM poll_runs;

-- name: GetMaxCreatedAt :one
SELECT MAX(created_at) AS max_created_at FROM posts;

-- name: GetSeedStats :one
SELECT COUNT(*) AS total_posts, MIN(id) AS min_id FROM posts;

-- name: ListGroupPosts :many
SELECT * FROM posts
WHERE group_id = ? AND status = 'active'
ORDER BY id ASC;

-- name: UpdatePostGroup :exec
UPDATE posts
SET group_id = ?, group_page_count = ?, group_is_latest = ?
WHERE id = ?;

-- name: GetPostTagsWithCategory :many
SELECT
    t.name,
    t.category,
    t.post_count
FROM post_tags pt
JOIN tags t ON t.id = pt.tag_id
WHERE pt.post_id = ?
ORDER BY t.name ASC;

-- name: UpdateTagCategoryByName :exec
UPDATE tags
SET category = ?
WHERE name = ?;

-- name: ListUnknownCategoryTags :many
SELECT name
FROM tags
WHERE category = -1 AND post_count > 0
ORDER BY post_count DESC, name ASC
LIMIT ?;

