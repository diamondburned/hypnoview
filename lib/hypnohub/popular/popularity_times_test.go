package popular

import (
	"testing"
	"time"
)

func TestTruncateWeek(t *testing.T) {
	tests := []struct {
		input  time.Time
		output time.Time
	}{
		{
			time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			time.Date(2024, time.January, 2, 0, 0, 0, 0, time.UTC),
			time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			time.Date(2024, time.January, 3, 0, 0, 0, 0, time.UTC),
			time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			time.Date(2024, time.January, 4, 0, 0, 0, 0, time.UTC),
			time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			time.Date(2024, time.January, 5, 0, 0, 0, 0, time.UTC),
			time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			time.Date(2024, time.January, 6, 0, 0, 0, 0, time.UTC),
			time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			time.Date(2024, time.January, 7, 0, 0, 0, 0, time.UTC),
			time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			time.Date(2024, time.January, 8, 0, 0, 0, 0, time.UTC),
			time.Date(2024, time.January, 8, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, test := range tests {
		output := truncateWeek(test.input)
		if output != test.output {
			t.Errorf("Expected %v, got %v", test.output, output)
		}
	}
}

func TestEarliestTimestampForPeriod(t *testing.T) {
	tests := []struct {
		period TimePeriod
		input  time.Time
		output time.Time
	}{
		{
			Daily,
			time.Date(2024, time.January, 2, 23, 59, 59, 999, time.UTC),
			time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			Daily,
			time.Date(2024, time.January, 2, 0, 0, 0, 0, time.UTC),
			time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			Weekly,
			time.Date(2024, time.January, 6, 1, 2, 3, 456, time.UTC),
			time.Date(2023, time.December, 25, 0, 0, 0, 0, time.UTC),
		},
		{
			Weekly,
			time.Date(2024, time.January, 7, 8, 9, 10, 11, time.UTC),
			time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			Monthly,
			time.Date(2024, time.January, 14, 0, 0, 0, 0, time.UTC),
			time.Date(2023, time.December, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			Monthly,
			time.Date(2024, time.January, 15, 0, 0, 0, 0, time.UTC),
			time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, test := range tests {
		output := EarliestTimestampForPeriod(test.input, test.period)
		if output != test.output {
			t.Errorf("%v %v: expected %v, got %v", test.period, test.input, test.output, output)
		}
	}
}
