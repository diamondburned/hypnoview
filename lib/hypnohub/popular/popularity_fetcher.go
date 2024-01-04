package popular

import (
	"context"
	"sync"
	"time"

	"libdb.so/hypnoview/lib/hypnohub/query"
)

// PopularityQueryUpdater is a struct that contains the queries for each time
// period. It automatically updates the queries when needed.
// It is safe to use from multiple goroutines.
type PopularityQueryUpdater struct {
	daily  popularQuery
	weekly popularQuery
	montly popularQuery
}

// DailyPopularQuery returns the query for the daily popular posts.
func (p *PopularityQueryUpdater) DailyPopularQuery(ctx context.Context, searcher PostsSearcher) (query.Query, error) {
	return p.daily.update(ctx, searcher)
}

// WeeklyPopularQuery returns the query for the weekly popular posts.
func (p *PopularityQueryUpdater) WeeklyPopularQuery(ctx context.Context, searcher PostsSearcher) (query.Query, error) {
	return p.weekly.update(ctx, searcher)
}

// MonthlyPopularQuery returns the query for the monthly popular posts.
func (p *PopularityQueryUpdater) MonthlyPopularQuery(ctx context.Context, searcher PostsSearcher) (query.Query, error) {
	return p.montly.update(ctx, searcher)
}

type popularQuery struct {
	mu    sync.Mutex
	query query.Query
	last  time.Time

	period TimePeriod // constant
}

func (q *popularQuery) update(ctx context.Context, searcher PostsSearcher) (query.Query, error) {
	now := EarliestTimestampForPeriod(time.Now(), q.period)

	q.mu.Lock()
	defer q.mu.Unlock()

	if q.last.After(now) {
		return q.query, nil
	}

	query, err := fetchQueryForPeriod(ctx, searcher, now, q.period)
	if err != nil {
		return nil, err
	}

	q.query = query
	q.last = now
	return query, nil
}

func fetchQueryForPeriod(ctx context.Context, searcher PostsSearcher, now time.Time, period TimePeriod) (query.Query, error) {
	postID, err := EstimatePostHistory(ctx, searcher, EstimatePostOptions{
		Now:    now,
		Period: period,
	})
	if err != nil {
		return nil, err
	}
	return query.And(
		query.Sort(query.SortScore, query.SortDescending),
		query.ID(query.GreaterEqual, postID),
	), nil
}
