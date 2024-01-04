package hypnohubutil

import (
	"context"
	"fmt"
	"log"
	"testing"
	"time"

	"libdb.so/hypnoview/lib/hypnohub"
)

func TestEstimatePostHistory(t *testing.T) {
	today := time.Date(2020, time.February, 1, 21, 0, 0, 0, time.UTC)
	searcher := newPostsSearcher([]mockPost{
		{2000, testDate("01-02-2020 21:00")}, // today
		{1999, testDate("01-02-2020 02:00")}, // yesterday
		{1998, testDate("31-01-2020 23:00")}, // yesterday
		{1997, testDate("31-01-2020 22:00")}, // yesterday
		{1996, testDate("31-01-2020 21:00")}, // last week
		{1995, testDate("30-01-2020 21:00")}, // last week
		{1994, testDate("25-01-2020 21:00")}, // last week
		{1993, testDate("25-01-2020 20:00")}, // last month
		{1992, testDate("17-01-2020 21:00")}, // last month
		{1991, testDate("10-01-2020 21:00")}, // last month
		{1990, testDate("03-01-2020 21:00")}, // last month
		{1989, testDate("27-12-2019 21:00")},
		{1988, testDate("20-12-2019 21:00")},
		{1987, testDate("13-12-2019 21:00")},
	})

	tests := []struct {
		name     string
		limit    PostHistoryEstimateLimit
		accuracy time.Duration
		wantID   hypnohub.PostID
		requests int
	}{
		{
			name:     "day",
			limit:    EstimateDay,
			accuracy: 0,
			wantID:   1997,
			requests: 8,
		},
		{
			name:     "day rough estimate",
			limit:    EstimateDay,
			accuracy: 30 * time.Hour,
			wantID:   1995,
			requests: 6,
		},
		{
			name:     "week",
			limit:    EstimateWeek,
			wantID:   1994,
			requests: 10,
		},
		{
			name:     "month",
			limit:    EstimateMonth,
			wantID:   1990,
			requests: 12,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			searcher := searcher.withResetCounter()
			id, _ := EstimatePostHistory(context.Background(), searcher, EstimatePostOptions{
				Now:      today,
				Limit:    test.limit,
				Accuracy: test.accuracy,
			})
			if id != test.wantID {
				t.Errorf("expected %v, got %v", test.wantID, id)
			}
			if searcher.counter != test.requests {
				t.Errorf("expected %v requests, got %v", test.requests, searcher.counter)
			}
		})
	}
}

func testDate(str string) time.Time {
	t, err := time.Parse("02-01-2006 15:04", str)
	if err != nil {
		panic(err)
	}
	return t
}

type mockPost struct {
	ID   hypnohub.PostID
	Time time.Time
}

func (p mockPost) String() string {
	return fmt.Sprintf("%d (%s)", p.ID, p.Time.Format("02-01-2006 15:04"))
}

type mockPostsSearcher struct {
	posts   []mockPost
	counter int
}

func newPostsSearcher(posts []mockPost) *mockPostsSearcher {
	return &mockPostsSearcher{posts: posts}
}

func (s *mockPostsSearcher) withResetCounter() *mockPostsSearcher {
	return &mockPostsSearcher{posts: s.posts}
}

func (s *mockPostsSearcher) SearchPosts(ctx context.Context, query string, postOffset int) (*hypnohub.SearchPostsResult, error) {
	log.Println("searching posts after", postOffset)
	s.counter++

	if postOffset >= len(s.posts) {
		return &hypnohub.SearchPostsResult{
			Posts:  []hypnohub.Post{},
			Count:  len(s.posts),
			Offset: postOffset,
		}, nil
	}

	const limit = 3
	posts := make([]hypnohub.Post, 0, limit)
	for i := postOffset; i < len(s.posts) && len(posts) < limit; i++ {
		log.Printf("  adding post %s", s.posts[i])
		posts = append(posts, hypnohub.Post{
			ID:        s.posts[i].ID,
			CreatedAt: hypnohub.Date(s.posts[i].Time),
		})
	}

	return &hypnohub.SearchPostsResult{
		Posts:  posts,
		Count:  len(s.posts),
		Offset: postOffset,
	}, nil
}
