package hypnohubutil

import (
	"context"
	"time"

	"libdb.so/hypnoview/lib/hypnohubquery"
)

// BuildDailyPopularQuery builds a query for the daily popular posts.
// Posts that were created yesterday are also included.
func BuildDailyPopularQuery(ctx context.Context, searcher PostsSearcher, now time.Time) (hypnohubquery.Query, error) {
	now = initNow(now)
	now = truncateDay(now).Add(-24 * time.Hour)
	return buildQueryAfterEstimating(ctx, searcher, now, EstimateDay)
}

// BuildWeeklyPopularQuery builds a query for the weekly popular posts.
// Posts made from last week are included, unless the current day is Sunday,
// then posts from this week (Last Monday to Sunday) are included.
func BuildWeeklyPopularQuery(ctx context.Context, searcher PostsSearcher, now time.Time) (hypnohubquery.Query, error) {
	now = initNow(now)
	now = truncateWeek(now)

	if now.Weekday() != time.Sunday {
		// Push this back another week.
		now = now.AddDate(0, 0, -7)
	}

	return buildQueryAfterEstimating(ctx, searcher, now, EstimateWeek)
}

// BuildMonthlyPopularQuery builds a query for the monthly popular posts.
// Posts made from last month are included as well, unless we're over two weeks
// into the current month.
func BuildMonthlyPopularQuery(ctx context.Context, searcher PostsSearcher, now time.Time) (hypnohubquery.Query, error) {
	now = initNow(now)
	now = truncateDay(now)

	day := now.Day()
	now = truncateMonth(now)

	if day > 14 {
		// Push this back another month.
		now = now.AddDate(0, -1, 0)
	}

	return buildQueryAfterEstimating(ctx, searcher, now, EstimateMonth)
}

func buildQueryAfterEstimating(ctx context.Context, searcher PostsSearcher, now time.Time, limit PostHistoryEstimateLimit) (hypnohubquery.Query, error) {
	postID, err := EstimatePostHistory(ctx, searcher, EstimatePostOptions{
		Now:   now,
		Limit: limit,
	})
	if err != nil {
		return nil, err
	}
	return hypnohubquery.And(
		hypnohubquery.Sort(hypnohubquery.SortScore, hypnohubquery.SortDescending),
		hypnohubquery.ID(hypnohubquery.GreaterEqual, postID),
	), nil
}

// truncateDay truncates the time to the beginning of the day.
// It respects the timezone.
func truncateDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// truncateWeek truncates the time to the beginning of the week.
func truncateWeek(t time.Time) time.Time {
	t = truncateDay(t)

	// Sunday happens to be the first enum value in time.Weekday, so we can't
	// even subtract it normally. Any other day of the week is fine, and
	// subtracting the day by (weekday-1) will always give us the beginning of
	// the week.
	if t.Weekday() == time.Sunday {
		return t.AddDate(0, 0, -6)
	} else {
		return t.AddDate(0, 0, -int(t.Weekday()-1))
	}
}

// truncateMonth truncates the time to the beginning of the month.
func truncateMonth(t time.Time) time.Time {
	t = truncateDay(t)
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}

func initNow(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}
