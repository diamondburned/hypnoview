package popular

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"libdb.so/hypnoview/lib/hypnohub"
)

// TimePeriod is the maximum time period to estimate the post
// history for.
type TimePeriod int

const (
	Daily TimePeriod = iota
	Weekly
	Monthly
	DailyYesterday

	maxTimePeriod
)

// EstimatePostMaxOffsets hard codes the post offsets for each time period.
// This offset is dependent on how active the site is, so it is not guaranteed
// to be accurate. Because of this, it is overestimated to be safe.
var EstimatePostMaxOffsets = map[TimePeriod]int{
	Monthly:        3000,
	Weekly:         800,
	Daily:          200,
	DailyYesterday: 400,
}

// MaxOffset returns the maximum offset for the given time period.
// See [EstimatePostMaxOffsets] for more information.
func (e TimePeriod) MaxOffset() int {
	return EstimatePostMaxOffsets[e]
}

// PostsSearcher is an interface that allows searching for posts.
// It is implemented by [hypnohub.Client].
type PostsSearcher interface {
	SearchPosts(ctx context.Context, query string, postOffset int) (*hypnohub.SearchPostsResult, error)
}

var _ PostsSearcher = (*hypnohub.Client)(nil)

// EstimatePostOptions is a struct that contains options for estimating the
// post history.
type EstimatePostOptions struct {
	// Now is the current time. If zero, then [time.Now] is used.
	Now time.Time
	// Timezone is the timezone to use for the estimate.
	// If zero, then [time.UTC] is used.
	Timezone *time.Location
	// Period is the maximum time period to estimate the post history for.
	Period TimePeriod
	// Accuracy is the accuracy of the estimate. If 0, then the estimate is
	// as accurate as possible.
	Accuracy time.Duration
}

// EstimatePostHistory estimates the post history for the given client.
// It employs a semi-binary search to find the earliest post for each time
// period.
func EstimatePostHistory(ctx context.Context, searcher PostsSearcher, opts EstimatePostOptions) (hypnohub.PostID, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}

	if opts.Timezone == nil {
		opts.Timezone = time.UTC
	}
	opts.Now = opts.Now.In(opts.Timezone)
	timeThreshold := EarliestTimestampForPeriod(opts.Now, opts.Period)

	const offsetCount = 2
	offsets := make([]int, 0, offsetCount)
	var postID hypnohub.PostID

	_, err := binarySearch(opts.Period.MaxOffset(), func(i int) (bool, error) {
		page, err := searcher.SearchPosts(ctx, "", i)
		if err != nil {
			// Can't do anything about this error, so just ignore it.
			return false, fmt.Errorf("searching posts: %w", err)
		}

		if len(page.Posts) == 0 {
			// We've gone too far back.
			return true, nil
		}

		if len(offsets) == offsetCount {
			// Ensure we're not going back and forth between the same page.
			// We determine this by checking if the gap between the last offset
			// and the current offset is less than half the page size.
			if last2IntsDifference(offsets) < 3 {
				return false, binarySearchBreak
			}

			copy(offsets, offsets[1:])
			offsets[len(offsets)-1] = i
		} else {
			offsets = append(offsets, i)
		}

		j := sort.Search(len(page.Posts), func(i int) bool {
			return page.Posts[i].CreatedAt.Time().Before(timeThreshold)
		})

		post := page.Posts[min(j, len(page.Posts)-1)]

		if opts.Accuracy > 0 {
			if timeWithinAccuracy(post.CreatedAt.Time(), timeThreshold, opts.Accuracy) {
				return false, binarySearchBreak
			}
		}

		if j == len(page.Posts) {
			// All posts are before the given time.
			postID = post.ID
			return false, nil
		} else {
			// Add 1 like how binarySearch does i + 1.
			postID = post.ID + 1
			return true, nil
		}
	})
	if err != nil {
		return 0, err
	}

	return postID, nil
}

// timeWithinAccuracy returns whether the given time is within the given
// accuracy (range) of the given time.
func timeWithinAccuracy(t, now time.Time, accuracy time.Duration) bool {
	return t.After(now.Add(-accuracy)) && t.Before(now.Add(accuracy))
}

func last2IntsDifference(ints []int) int {
	if len(ints) < 2 {
		return 0
	}
	a := ints[len(ints)-2]
	b := ints[len(ints)-1]
	if a > b {
		return a - b
	}
	return b - a
}

var binarySearchBreak = errors.New("binary search break")

func binarySearch(n int, f func(int) (bool, error)) (int, error) {
	// Define f(-1) == false and f(n) == true.
	// Invariant: f(i-1) == false, f(j) == true.
	i, j := 0, n
	for i < j {
		h := int(uint(i+j) >> 1) // avoid overflow when computing h

		l, err := f(h)
		if err != nil && !errors.Is(err, binarySearchBreak) {
			return 0, err
		}

		// i ≤ h < j
		if !l {
			i = h + 1 // preserves f(i-1) == false
		} else {
			j = h // preserves f(j) == true
		}

		if errors.Is(err, binarySearchBreak) {
			break
		}
	}
	// i == j, f(i-1) == false, and f(j) (= f(i)) == true  =>  answer is i.
	return i, nil
}
