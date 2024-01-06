package popular

import (
	"time"
)

// EarliestTimestampForPeriod returns the earliest timestamp for the given
// time period. If now is zero, then [time.Now] is used.
//
// For each time period:
//   - Day: posts that were created yesterday are also included.
//   - Week: posts made from last week are included, unless the current day
//     is Sunday, then posts from this week (Last Monday to Sunday) are
//     included.
//   - Month: posts made from last month are included as well, unless we're
//     over two weeks into the current month.
func EarliestTimestampForPeriod(now time.Time, period TimePeriod) time.Time {
	now = initNow(now)
	early := now
	switch period {
	case Daily:
		early = truncateDay(early).AddDate(0, 0, -1)
	case Weekly:
		early = truncateWeek(early)
		if now.Weekday() != time.Sunday {
			// Push this back another week.
			early = early.AddDate(0, 0, -7)
		}
	case Monthly:
		early = truncateMonth(early)
		if now.Day() <= 14 {
			// Push this back another month.
			early = early.AddDate(0, -1, 0)
		}
	default:
		panic("invalid period")
	}
	return early
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
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}

func initNow(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t
}
