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
