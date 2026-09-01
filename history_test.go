package launcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

type testClock struct{ t time.Time }

func (c *testClock) now() time.Time { return c.t }

func TestDefaultHistoryPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "XDG_STATE_HOME",
			env:  map[string]string{"XDG_STATE_HOME": "/state"},
			want: "/state/sysc-launch/history.gob",
		},
		{
			name: "HOME fallback",
			env:  map[string]string{"HOME": "/home/user"},
			want: "/home/user/.local/state/sysc-launch/history.gob",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			getenv := func(key string) string {
				return tt.env[key]
			}
			if got := defaultHistoryPath(getenv); got != tt.want {
				t.Fatalf("defaultHistoryPath = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHistoryRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "history.gob")
	clock := &testClock{t: time.Now()}

	h := loadHistory(path, clock.now, nil)
	h.record("fire", "firefox.desktop")
	h.record("fire", "firefox.desktop")
	h.record("term", "kitty.desktop")

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("history file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("history file mode = %o, want 600", perm)
	}

	reloaded := loadHistory(path, clock.now, nil)
	if got := reloaded.boost("fire", "firefox.desktop"); got != 20 {
		t.Fatalf("reloaded boost = %d, want 20", got)
	}
	if got := reloaded.boost("term", "kitty.desktop"); got != 10 {
		t.Fatalf("reloaded boost = %d, want 10", got)
	}
}

func TestHistoryCapsAmountAtTen(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "history.gob")
	clock := &testClock{t: time.Now()}
	h := loadHistory(path, clock.now, nil)
	for i := 0; i < 12; i++ {
		h.record("q", "app.desktop")
	}

	if got := loadHistory(path, clock.now, nil).boost("q", "app.desktop"); got != 100 {
		t.Fatalf("boost after 12 records = %d, want 100 (amount capped at 10)", got)
	}
}

func TestHistoryUsageScoreFormula(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		recorded  string
		query     string
		amount    int
		daysAgo   int
		wantBoost int
	}{
		{name: "exact query used today", recorded: "fire", query: "fire", amount: 3, daysAgo: 0, wantBoost: 30},
		{name: "exact query three days ago", recorded: "fire", query: "fire", amount: 2, daysAgo: 3, wantBoost: 14},
		{name: "stale use floors at one", recorded: "fire", query: "fire", amount: 2, daysAgo: 15, wantBoost: 1},
		{name: "longer recorded query divides by delta", recorded: "firefox", query: "fire", amount: 2, daysAgo: 0, wantBoost: 6},
		{name: "shorter recorded query divides by delta", recorded: "fi", query: "fire", amount: 2, daysAgo: 0, wantBoost: 10},
		{name: "unknown identifier", recorded: "", query: "fire", amount: 0, daysAgo: 0, wantBoost: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			now := time.Now()
			clock := &testClock{t: now.Add(-time.Duration(tt.daysAgo) * 24 * time.Hour)}
			h := loadHistory(filepath.Join(t.TempDir(), "history.gob"), clock.now, nil)
			for i := 0; i < tt.amount; i++ {
				h.record(tt.recorded, "app.desktop")
			}
			clock.t = now

			if got := h.boost(tt.query, "app.desktop"); got != tt.wantBoost {
				t.Fatalf("boost(%q) = %d, want %d", tt.query, got, tt.wantBoost)
			}
		})
	}
}

func TestHistoryEmptyQueryAggregatesAcrossQueries(t *testing.T) {
	t.Parallel()

	clock := &testClock{t: time.Now()}
	h := loadHistory(filepath.Join(t.TempDir(), "history.gob"), clock.now, nil)
	h.record("fi", "firefox.desktop")
	h.record("fi", "firefox.desktop")
	h.record("firefox", "firefox.desktop")
	h.record("firefox", "firefox.desktop")
	h.record("firefox", "firefox.desktop")

	if got := h.boost("", "firefox.desktop"); got != 50 {
		t.Fatalf("empty-query boost = %d, want 50", got)
	}
}

func TestHistoryCorruptFileStartsEmpty(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "history.gob")
	if err := os.WriteFile(path, []byte("not a gob stream"), 0o600); err != nil {
		t.Fatal(err)
	}

	clock := &testClock{t: time.Now()}
	h := loadHistory(path, clock.now, nil)
	if got := h.boost("fire", "firefox.desktop"); got != 0 {
		t.Fatalf("boost from corrupt history = %d, want 0", got)
	}

	h.record("fire", "firefox.desktop")
	if got := loadHistory(path, clock.now, nil).boost("fire", "firefox.desktop"); got != 10 {
		t.Fatalf("boost after re-record = %d, want 10", got)
	}
}

func TestRankAddsUsageBoostCappedAtTwentyFive(t *testing.T) {
	t.Parallel()

	clock := &testClock{t: time.Now()}
	h := loadHistory(filepath.Join(t.TempDir(), "history.gob"), clock.now, nil)
	for i := 0; i < 10; i++ {
		h.record("needle", "used.desktop")
	}

	entry := Entry{ID: "used.desktop", Name: "Needle"}
	base := rank([]Entry{entry}, "needle", nil)
	boosted := rank([]Entry{entry}, "needle", h.boost)
	if got := boosted[0].Score - base[0].Score; got != 25 {
		t.Fatalf("boost applied = %d, want 25 (raw usage 100 capped)", got)
	}
}

func TestRankEmptyQueryOrdersByUsageThenName(t *testing.T) {
	t.Parallel()

	clock := &testClock{t: time.Now()}
	h := loadHistory(filepath.Join(t.TempDir(), "history.gob"), clock.now, nil)
	h.record("zulu", "zulu.desktop")

	got := rank([]Entry{
		{ID: "alpha.desktop", Name: "Alpha"},
		{ID: "zulu.desktop", Name: "Zulu"},
	}, "", h.boost)
	if len(got) != 2 || got[0].Entry.Name != "Zulu" || got[1].Entry.Name != "Alpha" {
		t.Fatalf("empty-query order = %+v, want Zulu then Alpha", got)
	}
}
