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
SET post_count = (SELECT COUNT(*) FROM post_tags WHERE tag_id = sqlc.arg(id))
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
SELECT name, post_count
FROM tags
WHERE post_count > 0
ORDER BY post_count DESC, name ASC
LIMIT ?;

-- name: SearchTagsByPrefix :many
SELECT
    name,
    post_count,
    (name LIKE sqlc.arg(prefix)) AS is_prefix
FROM tags
WHERE post_count > 0 AND name LIKE sqlc.arg(pattern)
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

-- name: CountPostsAll :one
SELECT COUNT(*)
FROM posts p
WHERE p.status = 'active'
    AND (CAST(sqlc.arg(collapse_groups) AS INTEGER) = 0 OR p.group_is_latest = 1)
    AND (CAST(sqlc.arg(rating) AS TEXT) = '' OR p.rating = CAST(sqlc.arg(rating) AS TEXT))
    AND (
        CAST(sqlc.arg(media_type) AS TEXT) = ''
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'video' AND p.file_ext IN ('mp4', 'webm'))
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'animated' AND p.file_ext IN ('mp4', 'webm', 'gif'))
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'image' AND p.file_ext NOT IN ('mp4', 'webm', 'gif'))
    );

-- name: ListPostsAllSortNew :many
SELECT
    p.id, p.created_at, p.changed_at, p.score, p.rating, p.creator_id, p.parent_id,
    p.md5, p.source, p.status, p.has_notes, p.has_comments, p.has_children,
    p.file_url, p.file_ext, p.width, p.height,
    p.sample_url, p.sample_width, p.sample_height,
    p.preview_url, p.preview_width, p.preview_height,
    p.tags_raw, p.first_seen_at, p.last_polled_at,
    p.group_id, p.group_is_latest, p.group_page_count,
    0 AS baseline_score,
    p.score AS score_delta,
    CAST(MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - p.created_at) / 3600.0) AS REAL) AS window_hours,
    CAST(CAST(p.score AS REAL) / (MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - p.created_at) / 3600.0) + CAST(sqlc.arg(tau) AS REAL)) AS REAL) AS score_rate,
    CAST((CAST(p.score AS REAL) / (MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - p.created_at) / 3600.0) + CAST(sqlc.arg(tau) AS REAL))) * POWER(MAX(1, p.score), 0.35) AS REAL) AS hot_score
FROM posts p
WHERE p.status = 'active'
    AND (CAST(sqlc.arg(collapse_groups) AS INTEGER) = 0 OR p.group_is_latest = 1)
    AND (CAST(sqlc.arg(rating) AS TEXT) = '' OR p.rating = CAST(sqlc.arg(rating) AS TEXT))
    AND (
        CAST(sqlc.arg(media_type) AS TEXT) = ''
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'video' AND p.file_ext IN ('mp4', 'webm'))
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'animated' AND p.file_ext IN ('mp4', 'webm', 'gif'))
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'image' AND p.file_ext NOT IN ('mp4', 'webm', 'gif'))
    )
ORDER BY p.created_at DESC, p.id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: ListPostsAllSortScore :many
SELECT
    p.id, p.created_at, p.changed_at, p.score, p.rating, p.creator_id, p.parent_id,
    p.md5, p.source, p.status, p.has_notes, p.has_comments, p.has_children,
    p.file_url, p.file_ext, p.width, p.height,
    p.sample_url, p.sample_width, p.sample_height,
    p.preview_url, p.preview_width, p.preview_height,
    p.tags_raw, p.first_seen_at, p.last_polled_at,
    p.group_id, p.group_is_latest, p.group_page_count,
    0 AS baseline_score,
    p.score AS score_delta,
    CAST(MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - p.created_at) / 3600.0) AS REAL) AS window_hours,
    CAST(CAST(p.score AS REAL) / (MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - p.created_at) / 3600.0) + CAST(sqlc.arg(tau) AS REAL)) AS REAL) AS score_rate,
    CAST((CAST(p.score AS REAL) / (MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - p.created_at) / 3600.0) + CAST(sqlc.arg(tau) AS REAL))) * POWER(MAX(1, p.score), 0.35) AS REAL) AS hot_score
FROM posts p
WHERE p.status = 'active'
    AND (CAST(sqlc.arg(collapse_groups) AS INTEGER) = 0 OR p.group_is_latest = 1)
    AND (CAST(sqlc.arg(rating) AS TEXT) = '' OR p.rating = CAST(sqlc.arg(rating) AS TEXT))
    AND (
        CAST(sqlc.arg(media_type) AS TEXT) = ''
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'video' AND p.file_ext IN ('mp4', 'webm'))
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'animated' AND p.file_ext IN ('mp4', 'webm', 'gif'))
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'image' AND p.file_ext NOT IN ('mp4', 'webm', 'gif'))
    )
ORDER BY p.score DESC, p.id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: ListPostsAllSortHot :many
SELECT
    p.id, p.created_at, p.changed_at, p.score, p.rating, p.creator_id, p.parent_id,
    p.md5, p.source, p.status, p.has_notes, p.has_comments, p.has_children,
    p.file_url, p.file_ext, p.width, p.height,
    p.sample_url, p.sample_width, p.sample_height,
    p.preview_url, p.preview_width, p.preview_height,
    p.tags_raw, p.first_seen_at, p.last_polled_at,
    p.group_id, p.group_is_latest, p.group_page_count,
    0 AS baseline_score,
    p.score AS score_delta,
    CAST(MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - p.created_at) / 3600.0) AS REAL) AS window_hours,
    CAST(CAST(p.score AS REAL) / (MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - p.created_at) / 3600.0) + CAST(sqlc.arg(tau) AS REAL)) AS REAL) AS score_rate,
    CAST((CAST(p.score AS REAL) / (MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - p.created_at) / 3600.0) + CAST(sqlc.arg(tau) AS REAL))) * POWER(MAX(1, p.score), 0.35) AS REAL) AS hot_score
FROM posts p
WHERE p.status = 'active'
    AND (CAST(sqlc.arg(collapse_groups) AS INTEGER) = 0 OR p.group_is_latest = 1)
    AND (CAST(sqlc.arg(rating) AS TEXT) = '' OR p.rating = CAST(sqlc.arg(rating) AS TEXT))
    AND (
        CAST(sqlc.arg(media_type) AS TEXT) = ''
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'video' AND p.file_ext IN ('mp4', 'webm'))
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'animated' AND p.file_ext IN ('mp4', 'webm', 'gif'))
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'image' AND p.file_ext NOT IN ('mp4', 'webm', 'gif'))
    )
ORDER BY hot_score DESC, score_rate DESC, score_delta DESC, p.score DESC, p.id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: ListPostsAllSortTop :many
SELECT
    p.id, p.created_at, p.changed_at, p.score, p.rating, p.creator_id, p.parent_id,
    p.md5, p.source, p.status, p.has_notes, p.has_comments, p.has_children,
    p.file_url, p.file_ext, p.width, p.height,
    p.sample_url, p.sample_width, p.sample_height,
    p.preview_url, p.preview_width, p.preview_height,
    p.tags_raw, p.first_seen_at, p.last_polled_at,
    p.group_id, p.group_is_latest, p.group_page_count,
    0 AS baseline_score,
    p.score AS score_delta,
    CAST(MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - p.created_at) / 3600.0) AS REAL) AS window_hours,
    CAST(CAST(p.score AS REAL) / (MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - p.created_at) / 3600.0) + CAST(sqlc.arg(tau) AS REAL)) AS REAL) AS score_rate,
    CAST((CAST(p.score AS REAL) / (MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - p.created_at) / 3600.0) + CAST(sqlc.arg(tau) AS REAL))) * POWER(MAX(1, p.score), 0.35) AS REAL) AS hot_score
FROM posts p
WHERE p.status = 'active'
    AND (CAST(sqlc.arg(collapse_groups) AS INTEGER) = 0 OR p.group_is_latest = 1)
    AND (CAST(sqlc.arg(rating) AS TEXT) = '' OR p.rating = CAST(sqlc.arg(rating) AS TEXT))
    AND (
        CAST(sqlc.arg(media_type) AS TEXT) = ''
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'video' AND p.file_ext IN ('mp4', 'webm'))
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'animated' AND p.file_ext IN ('mp4', 'webm', 'gif'))
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'image' AND p.file_ext NOT IN ('mp4', 'webm', 'gif'))
    )
ORDER BY score_delta DESC, hot_score DESC, score_rate DESC, p.score DESC, p.id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountPostsWindowed :one
WITH baseline AS (
    SELECT post_id, score AS start_score
    FROM score_snapshots
    WHERE poll_time >= CAST(sqlc.arg(window_start_unix) AS INTEGER) AND poll_time < CAST(sqlc.arg(now_unix) AS INTEGER)
    GROUP BY post_id
)
SELECT COUNT(*)
FROM posts p
LEFT JOIN baseline b ON b.post_id = p.id
WHERE p.status = 'active'
    AND (CAST(sqlc.arg(collapse_groups) AS INTEGER) = 0 OR p.group_is_latest = 1)
    AND (p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) OR (b.start_score IS NOT NULL AND p.score > b.start_score))
    AND (CAST(sqlc.arg(rating) AS TEXT) = '' OR p.rating = CAST(sqlc.arg(rating) AS TEXT))
    AND (
        CAST(sqlc.arg(media_type) AS TEXT) = ''
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'video' AND p.file_ext IN ('mp4', 'webm'))
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'animated' AND p.file_ext IN ('mp4', 'webm', 'gif'))
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'image' AND p.file_ext NOT IN ('mp4', 'webm', 'gif'))
    );

-- name: ListPostsWindowedSortHot :many
WITH baseline AS (
    SELECT post_id, score AS start_score, MIN(poll_time) AS start_time
    FROM score_snapshots
    WHERE poll_time >= CAST(sqlc.arg(window_start_unix) AS INTEGER) AND poll_time < CAST(sqlc.arg(now_unix) AS INTEGER)
    GROUP BY post_id
)
SELECT
    p.id, p.created_at, p.changed_at, p.score, p.rating, p.creator_id, p.parent_id,
    p.md5, p.source, p.status, p.has_notes, p.has_comments, p.has_children,
    p.file_url, p.file_ext, p.width, p.height,
    p.sample_url, p.sample_width, p.sample_height,
    p.preview_url, p.preview_width, p.preview_height,
    p.tags_raw, p.first_seen_at, p.last_polled_at,
    p.group_id, p.group_is_latest, p.group_page_count,
    CAST(CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN 0 ELSE COALESCE(b.start_score, p.score) END AS INTEGER) AS baseline_score,
    CAST(CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.score ELSE MAX(0, p.score - COALESCE(b.start_score, p.score)) END AS INTEGER) AS score_delta,
    CAST(MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.created_at ELSE COALESCE(b.start_time, CAST(sqlc.arg(window_start_unix) AS INTEGER)) END) / 3600.0) AS REAL) AS window_hours,
    CAST(CAST(CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.score ELSE MAX(0, p.score - COALESCE(b.start_score, p.score)) END AS REAL) / (MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.created_at ELSE COALESCE(b.start_time, CAST(sqlc.arg(window_start_unix) AS INTEGER)) END) / 3600.0) + CAST(sqlc.arg(tau) AS REAL)) AS REAL) AS score_rate,
    CAST((CAST(CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.score ELSE MAX(0, p.score - COALESCE(b.start_score, p.score)) END AS REAL) / (MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.created_at ELSE COALESCE(b.start_time, CAST(sqlc.arg(window_start_unix) AS INTEGER)) END) / 3600.0) + CAST(sqlc.arg(tau) AS REAL))) * POWER(MAX(1, p.score), 0.35) AS REAL) AS hot_score
FROM posts p
LEFT JOIN baseline b ON b.post_id = p.id
WHERE p.status = 'active'
    AND (CAST(sqlc.arg(collapse_groups) AS INTEGER) = 0 OR p.group_is_latest = 1)
    AND (p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) OR (b.start_score IS NOT NULL AND p.score > b.start_score))
    AND (CAST(sqlc.arg(rating) AS TEXT) = '' OR p.rating = CAST(sqlc.arg(rating) AS TEXT))
    AND (
        CAST(sqlc.arg(media_type) AS TEXT) = ''
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'video' AND p.file_ext IN ('mp4', 'webm'))
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'animated' AND p.file_ext IN ('mp4', 'webm', 'gif'))
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'image' AND p.file_ext NOT IN ('mp4', 'webm', 'gif'))
    )
ORDER BY hot_score DESC, score_rate DESC, score_delta DESC, p.score DESC, p.id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: ListPostsWindowedSortTop :many
WITH baseline AS (
    SELECT post_id, score AS start_score, MIN(poll_time) AS start_time
    FROM score_snapshots
    WHERE poll_time >= CAST(sqlc.arg(window_start_unix) AS INTEGER) AND poll_time < CAST(sqlc.arg(now_unix) AS INTEGER)
    GROUP BY post_id
)
SELECT
    p.id, p.created_at, p.changed_at, p.score, p.rating, p.creator_id, p.parent_id,
    p.md5, p.source, p.status, p.has_notes, p.has_comments, p.has_children,
    p.file_url, p.file_ext, p.width, p.height,
    p.sample_url, p.sample_width, p.sample_height,
    p.preview_url, p.preview_width, p.preview_height,
    p.tags_raw, p.first_seen_at, p.last_polled_at,
    p.group_id, p.group_is_latest, p.group_page_count,
    CAST(CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN 0 ELSE COALESCE(b.start_score, p.score) END AS INTEGER) AS baseline_score,
    CAST(CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.score ELSE MAX(0, p.score - COALESCE(b.start_score, p.score)) END AS INTEGER) AS score_delta,
    CAST(MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.created_at ELSE COALESCE(b.start_time, CAST(sqlc.arg(window_start_unix) AS INTEGER)) END) / 3600.0) AS REAL) AS window_hours,
    CAST(CAST(CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.score ELSE MAX(0, p.score - COALESCE(b.start_score, p.score)) END AS REAL) / (MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.created_at ELSE COALESCE(b.start_time, CAST(sqlc.arg(window_start_unix) AS INTEGER)) END) / 3600.0) + CAST(sqlc.arg(tau) AS REAL)) AS REAL) AS score_rate,
    CAST((CAST(CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.score ELSE MAX(0, p.score - COALESCE(b.start_score, p.score)) END AS REAL) / (MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.created_at ELSE COALESCE(b.start_time, CAST(sqlc.arg(window_start_unix) AS INTEGER)) END) / 3600.0) + CAST(sqlc.arg(tau) AS REAL))) * POWER(MAX(1, p.score), 0.35) AS REAL) AS hot_score
FROM posts p
LEFT JOIN baseline b ON b.post_id = p.id
WHERE p.status = 'active'
    AND (CAST(sqlc.arg(collapse_groups) AS INTEGER) = 0 OR p.group_is_latest = 1)
    AND (p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) OR (b.start_score IS NOT NULL AND p.score > b.start_score))
    AND (CAST(sqlc.arg(rating) AS TEXT) = '' OR p.rating = CAST(sqlc.arg(rating) AS TEXT))
    AND (
        CAST(sqlc.arg(media_type) AS TEXT) = ''
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'video' AND p.file_ext IN ('mp4', 'webm'))
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'animated' AND p.file_ext IN ('mp4', 'webm', 'gif'))
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'image' AND p.file_ext NOT IN ('mp4', 'webm', 'gif'))
    )
ORDER BY score_delta DESC, hot_score DESC, score_rate DESC, p.score DESC, p.id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: ListPostsWindowedSortNew :many
WITH baseline AS (
    SELECT post_id, score AS start_score, MIN(poll_time) AS start_time
    FROM score_snapshots
    WHERE poll_time >= CAST(sqlc.arg(window_start_unix) AS INTEGER) AND poll_time < CAST(sqlc.arg(now_unix) AS INTEGER)
    GROUP BY post_id
)
SELECT
    p.id, p.created_at, p.changed_at, p.score, p.rating, p.creator_id, p.parent_id,
    p.md5, p.source, p.status, p.has_notes, p.has_comments, p.has_children,
    p.file_url, p.file_ext, p.width, p.height,
    p.sample_url, p.sample_width, p.sample_height,
    p.preview_url, p.preview_width, p.preview_height,
    p.tags_raw, p.first_seen_at, p.last_polled_at,
    p.group_id, p.group_is_latest, p.group_page_count,
    CAST(CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN 0 ELSE COALESCE(b.start_score, p.score) END AS INTEGER) AS baseline_score,
    CAST(CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.score ELSE MAX(0, p.score - COALESCE(b.start_score, p.score)) END AS INTEGER) AS score_delta,
    CAST(MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.created_at ELSE COALESCE(b.start_time, CAST(sqlc.arg(window_start_unix) AS INTEGER)) END) / 3600.0) AS REAL) AS window_hours,
    CAST(CAST(CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.score ELSE MAX(0, p.score - COALESCE(b.start_score, p.score)) END AS REAL) / (MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.created_at ELSE COALESCE(b.start_time, CAST(sqlc.arg(window_start_unix) AS INTEGER)) END) / 3600.0) + CAST(sqlc.arg(tau) AS REAL)) AS REAL) AS score_rate,
    CAST((CAST(CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.score ELSE MAX(0, p.score - COALESCE(b.start_score, p.score)) END AS REAL) / (MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.created_at ELSE COALESCE(b.start_time, CAST(sqlc.arg(window_start_unix) AS INTEGER)) END) / 3600.0) + CAST(sqlc.arg(tau) AS REAL))) * POWER(MAX(1, p.score), 0.35) AS REAL) AS hot_score
FROM posts p
LEFT JOIN baseline b ON b.post_id = p.id
WHERE p.status = 'active'
    AND (CAST(sqlc.arg(collapse_groups) AS INTEGER) = 0 OR p.group_is_latest = 1)
    AND (p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) OR (b.start_score IS NOT NULL AND p.score > b.start_score))
    AND (CAST(sqlc.arg(rating) AS TEXT) = '' OR p.rating = CAST(sqlc.arg(rating) AS TEXT))
    AND (
        CAST(sqlc.arg(media_type) AS TEXT) = ''
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'video' AND p.file_ext IN ('mp4', 'webm'))
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'animated' AND p.file_ext IN ('mp4', 'webm', 'gif'))
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'image' AND p.file_ext NOT IN ('mp4', 'webm', 'gif'))
    )
ORDER BY p.created_at DESC, p.id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: ListPostsWindowedSortScore :many
WITH baseline AS (
    SELECT post_id, score AS start_score, MIN(poll_time) AS start_time
    FROM score_snapshots
    WHERE poll_time >= CAST(sqlc.arg(window_start_unix) AS INTEGER) AND poll_time < CAST(sqlc.arg(now_unix) AS INTEGER)
    GROUP BY post_id
)
SELECT
    p.id, p.created_at, p.changed_at, p.score, p.rating, p.creator_id, p.parent_id,
    p.md5, p.source, p.status, p.has_notes, p.has_comments, p.has_children,
    p.file_url, p.file_ext, p.width, p.height,
    p.sample_url, p.sample_width, p.sample_height,
    p.preview_url, p.preview_width, p.preview_height,
    p.tags_raw, p.first_seen_at, p.last_polled_at,
    p.group_id, p.group_is_latest, p.group_page_count,
    CAST(CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN 0 ELSE COALESCE(b.start_score, p.score) END AS INTEGER) AS baseline_score,
    CAST(CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.score ELSE MAX(0, p.score - COALESCE(b.start_score, p.score)) END AS INTEGER) AS score_delta,
    CAST(MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.created_at ELSE COALESCE(b.start_time, CAST(sqlc.arg(window_start_unix) AS INTEGER)) END) / 3600.0) AS REAL) AS window_hours,
    CAST(CAST(CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.score ELSE MAX(0, p.score - COALESCE(b.start_score, p.score)) END AS REAL) / (MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.created_at ELSE COALESCE(b.start_time, CAST(sqlc.arg(window_start_unix) AS INTEGER)) END) / 3600.0) + CAST(sqlc.arg(tau) AS REAL)) AS REAL) AS score_rate,
    CAST((CAST(CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.score ELSE MAX(0, p.score - COALESCE(b.start_score, p.score)) END AS REAL) / (MAX(0.1, (CAST(sqlc.arg(now_unix) AS INTEGER) - CASE WHEN p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) THEN p.created_at ELSE COALESCE(b.start_time, CAST(sqlc.arg(window_start_unix) AS INTEGER)) END) / 3600.0) + CAST(sqlc.arg(tau) AS REAL))) * POWER(MAX(1, p.score), 0.35) AS REAL) AS hot_score
FROM posts p
LEFT JOIN baseline b ON b.post_id = p.id
WHERE p.status = 'active'
    AND (CAST(sqlc.arg(collapse_groups) AS INTEGER) = 0 OR p.group_is_latest = 1)
    AND (p.created_at >= CAST(sqlc.arg(window_start_unix) AS INTEGER) OR (b.start_score IS NOT NULL AND p.score > b.start_score))
    AND (CAST(sqlc.arg(rating) AS TEXT) = '' OR p.rating = CAST(sqlc.arg(rating) AS TEXT))
    AND (
        CAST(sqlc.arg(media_type) AS TEXT) = ''
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'video' AND p.file_ext IN ('mp4', 'webm'))
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'animated' AND p.file_ext IN ('mp4', 'webm', 'gif'))
        OR (CAST(sqlc.arg(media_type) AS TEXT) = 'image' AND p.file_ext NOT IN ('mp4', 'webm', 'gif'))
    )
ORDER BY p.score DESC, p.id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: ListGroupPosts :many
SELECT * FROM posts
WHERE group_id = ? AND status = 'active'
ORDER BY id ASC;

-- name: UpdatePostGroup :exec
UPDATE posts
SET group_id = ?, group_page_count = ?, group_is_latest = ?
WHERE id = ?;
