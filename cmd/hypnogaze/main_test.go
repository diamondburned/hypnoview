package main

import (
	"log/slog"
	"testing"

	slogjournal "github.com/systemd/slog-journal"
)

func TestJournalKey(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"addr", "ADDR"},
		{"poll_interval", "POLL_INTERVAL"},
		{"user-agent", "USER_AGENT"},
		{"httpRequest", "HTTPREQUEST"},
		{"TOTAL_POSTS", "TOTAL_POSTS"},
	}
	for _, tc := range tests {
		if got := journalKey(tc.in); got != tc.want {
			t.Errorf("journalKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNewLogger(t *testing.T) {
	t.Run("default stderr handler", func(t *testing.T) {
		t.Setenv("SLOG_JOURNAL", "")
		l, err := newLogger(slog.LevelInfo)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := l.Handler().(*slogjournal.Handler); ok {
			t.Fatalf("expected non-journal handler when SLOG_JOURNAL is unset")
		}
	})

	t.Run("journal handler enabled", func(t *testing.T) {
		t.Setenv("SLOG_JOURNAL", "1")
		l, err := newLogger(slog.LevelInfo)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := l.Handler().(*slogjournal.Handler); !ok {
			t.Fatalf("expected *slogjournal.Handler when SLOG_JOURNAL=1, got %T", l.Handler())
		}
	})
}
