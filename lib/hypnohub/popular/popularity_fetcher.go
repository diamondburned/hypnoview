package popular

import (
	"context"
	"fmt"
	"sync"
	"time"

	"libdb.so/hypnoview/lib/hypnohub/query"
)

// PopularQueryUpdater is a struct that contains the queries for each time
// period. It automatically updates the queries when needed. It is safe to use
// from multiple goroutines.
type PopularQueryUpdater struct {
	searcher PostsSearcher
	periods  [maxTimePeriod]popularQuery
}

// NewPopularQueryUpdater creates a new PopularQueryUpdater.
func NewPopularQueryUpdater(searcher PostsSearcher) *PopularQueryUpdater {
	return &PopularQueryUpdater{
		searcher: searcher,
	}
}

// QueryPopular returns the query for the popular posts in the given time period.
func (p *PopularQueryUpdater) QueryPopular(ctx context.Context, period TimePeriod) (query.Query, error) {
	if period < 0 || period >= maxTimePeriod {
		return nil, fmt.Errorf("invalid time period %v", period)
	}
	return p.periods[period].update(ctx, p.searcher)
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
