package popular

import (
	"context"
	"sync"
	"time"

	"libdb.so/hypnoview/lib/hypnohub/query"
)

// PopularQueryUpdater is a struct that contains the queries for each time
// period. It automatically updates the queries when needed. It is safe to use
// from multiple goroutines.
type PopularQueryUpdater struct {
	searcher PostsSearcher
	daily    popularQuery
	weekly   popularQuery
	montly   popularQuery
}

// NewPopularQueryUpdater creates a new PopularQueryUpdater.
func NewPopularQueryUpdater(searcher PostsSearcher) *PopularQueryUpdater {
	return &PopularQueryUpdater{
		searcher: searcher,
		daily:    popularQuery{period: Daily},
		weekly:   popularQuery{period: Weekly},
		montly:   popularQuery{period: Monthly},
	}
}

// DailyPopularQuery returns the query for the daily popular posts.
func (p *PopularQueryUpdater) DailyPopularQuery(ctx context.Context) (query.Query, error) {
	return p.daily.update(ctx, p.searcher)
}

// WeeklyPopularQuery returns the query for the weekly popular posts.
func (p *PopularQueryUpdater) WeeklyPopularQuery(ctx context.Context) (query.Query, error) {
	return p.weekly.update(ctx, p.searcher)
}

// MonthlyPopularQuery returns the query for the monthly popular posts.
func (p *PopularQueryUpdater) MonthlyPopularQuery(ctx context.Context) (query.Query, error) {
	return p.montly.update(ctx, p.searcher)
}

type popularQuery struct {
	mu    sync.Mutex
	query query.Query
	last  time.Time

	period TimePeriod // constant
}

func (q *popularQuery) update(ctx context.Context, searcher PostsSearcher) (query.Query, error) {
	now := time.Now().UTC()
	now = EarliestTimestampForPeriod(now, q.period)

	q.mu.Lock()
	defer q.mu.Unlock()

	if q.last.Equal(now) {
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
