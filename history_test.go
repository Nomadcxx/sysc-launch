package launcher

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type testClock struct{ t time.Time }

func (c *testClock) now() time.Time { return c.t }

func historyWithUsage(now time.Time, query, identifier string, amount int, lastUsed time.Time) *History {
	clock := &testClock{t: now}
	return &History{
		now: clock.now,
		data: map[string]map[string]historyData{
			query: {identifier: {Amount: amount, LastUsed: lastUsed}},
		},
	}
}

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
	if got := reloaded.boost("fire", "firefox.desktop"); got != 13 {
		t.Fatalf("reloaded boost = %d, want 13", got)
	}
	if got := reloaded.boost("term", "kitty.desktop"); got != 8 {
		t.Fatalf("reloaded boost = %d, want 8", got)
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
		{name: "exact query used today", recorded: "fire", query: "fire", amount: 3, daysAgo: 0, wantBoost: 15},
		{name: "exact query three days ago", recorded: "fire", query: "fire", amount: 2, daysAgo: 3, wantBoost: 11},
		{name: "old use keeps decaying", recorded: "fire", query: "fire", amount: 2, daysAgo: 15, wantBoost: 6},
		{name: "longer recorded query divides by delta", recorded: "firefox", query: "fire", amount: 2, daysAgo: 0, wantBoost: 4},
		{name: "shorter recorded query divides by delta", recorded: "fi", query: "fire", amount: 2, daysAgo: 0, wantBoost: 6},
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

func TestHistoryExactQueryDistanceIsZeroWithRelatedHistory(t *testing.T) {
	t.Parallel()

	now := time.Now()
	h := historyWithUsage(now, "fire", "app.desktop", 1, now)
	h.data["firefox"] = map[string]historyData{
		"app.desktop": {Amount: 1, LastUsed: now},
	}

	for i := 0; i < 64; i++ {
		if got := h.boost("fire", "app.desktop"); got != 13 {
			t.Fatalf("boost with exact and related queries = %d, want 13 (iteration %d)", got, i)
		}
	}
}

func TestHistoryUsageCurveSeparatesCountAndRecency(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 17, 12, 0, 0, 0, time.UTC)
	boost := func(identifier string, amount, daysAgo int) int {
		h := historyWithUsage(now, "browser", identifier, amount, now.Add(-time.Duration(daysAgo)*24*time.Hour))
		return h.boost("browser", identifier)
	}

	frequentRecent := boost("frequent-recent", 10, 0)
	rareRecent := boost("rare-recent", 1, 0)
	frequentOld := boost("frequent-old", 10, 20)
	oneOffOld := boost("one-off-old", 1, 20)
	if !(frequentRecent > rareRecent && rareRecent > frequentOld && frequentOld > oneOffOld) {
		t.Fatalf("usage scores frequent/recent=%d rare/recent=%d frequent/old=%d one-off/old=%d; want strictly descending count/recency order",
			frequentRecent, rareRecent, frequentOld, oneOffOld)
	}

	counts := []int{1, 2, 4, 8, 10}
	scores := make([]int, len(counts))
	for i, count := range counts {
		scores[i] = boost("count", count, 0)
		if scores[i] > usageBoostCap {
			t.Fatalf("boost at count %d = %d, exceeds cap %d", count, scores[i], usageBoostCap)
		}
		if i > 0 && scores[i] <= scores[i-1] {
			t.Fatalf("count scores %v are not strictly increasing", scores)
		}
	}
	if first, later := scores[1]-scores[0], scores[2]-scores[1]; later > first {
		t.Fatalf("count growth did not saturate: gains 1→2=%d, 2→4=%d", first, later)
	}
}

func TestHistoryUsageDecayBoundary(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 17, 12, 0, 0, 0, time.UTC)
	before := historyWithUsage(now, "browser", "app.desktop", 10, now.Add(-29*24*time.Hour))
	atBoundary := historyWithUsage(now, "browser", "app.desktop", 10, now.Add(-30*24*time.Hour))
	if got := before.boost("browser", "app.desktop"); got == 0 {
		t.Fatalf("boost one day before decay boundary = 0, want positive")
	}
	if got := atBoundary.boost("browser", "app.desktop"); got != 0 {
		t.Fatalf("boost at decay boundary = %d, want 0", got)
	}
}

func TestHistory200UsesOutrank10Uses(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 17, 12, 0, 0, 0, time.UTC)
	clock := &testClock{t: now}
	h := loadHistory(filepath.Join(t.TempDir(), "history.gob"), clock.now, nil)
	for i := 0; i < 10; i++ {
		h.record("browser", "a-ten.desktop")
	}
	for i := 0; i < 200; i++ {
		h.record("browser", "z-two-hundred.desktop")
	}
	h.record("browser", "b-one.desktop")

	score10 := h.boost("browser", "a-ten.desktop")
	score200 := h.boost("browser", "z-two-hundred.desktop")
	score1 := h.boost("browser", "b-one.desktop")
	if score200 <= score10 {
		t.Fatalf("usage scores at same age: 10=%d, 200=%d; want 200 above 10", score10, score200)
	}
	if score200 > usageBoostCap || score200-score10 >= score10-score1 {
		t.Fatalf("usage scores at same age: 10=%d, 200=%d; want bounded diminishing count gains", score10, score200)
	}

	got := rank([]Entry{
		{ID: "a-ten.desktop", Name: "Browser"},
		{ID: "z-two-hundred.desktop", Name: "Browser"},
	}, "browser", h.boost)
	if got[0].Entry.ID != "z-two-hundred.desktop" {
		t.Fatalf("rank order = %+v, want 200-use entry first", got)
	}
}

func TestHistoryRecordSanitizesAndSaturatesCounts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 17, 12, 0, 0, 0, time.UTC)
	clock := &testClock{t: now}
	path := filepath.Join(t.TempDir(), "history.gob")
	h := loadHistory(path, clock.now, nil)
	maxInt := int(^uint(0) >> 1)
	h.data["browser"] = map[string]historyData{
		"negative.desktop": {Amount: -200, LastUsed: now},
		"max.desktop":      {Amount: maxInt, LastUsed: now},
	}
	h.record("browser", "negative.desktop")
	h.record("browser", "max.desktop")

	reloaded := loadHistory(path, clock.now, nil)
	if got := reloaded.data["browser"]["negative.desktop"].Amount; got != 1 {
		t.Fatalf("count after negative persisted amount = %d, want 1", got)
	}
	if got := reloaded.data["browser"]["max.desktop"].Amount; got != maxInt {
		t.Fatalf("count after max-int record = %d, want saturated %d", got, maxInt)
	}
}

func TestHistoryCountAggregationCannotOverflow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 17, 12, 0, 0, 0, time.UTC)
	maxInt := int(^uint(0) >> 1)
	h := &History{
		now: func() time.Time { return now },
		data: map[string]map[string]historyData{
			"fire": {"app.desktop": {Amount: maxInt, LastUsed: now}},
			"f":    {"app.desktop": {Amount: maxInt, LastUsed: now}},
		},
	}
	if got := h.boost("", "app.desktop"); got != usageBoostCap {
		t.Fatalf("boost from oversized history counts = %d, want bounded %d", got, usageBoostCap)
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

	if got := h.boost("", "firefox.desktop"); got != 18 {
		t.Fatalf("empty-query boost = %d, want 18", got)
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
	if got := loadHistory(path, clock.now, nil).boost("fire", "firefox.desktop"); got != 8 {
		t.Fatalf("boost after re-record = %d, want 8", got)
	}
}

func TestRankAddsUsageBoostCappedAtTwentyFive(t *testing.T) {
	t.Parallel()

	entry := Entry{ID: "used.desktop", Name: "Needle"}
	base := rank([]Entry{entry}, "needle", nil)
	boosted := rank([]Entry{entry}, "needle", func(string, string) int { return 100 })
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

// A save must not rewrite the history in place: an in-place write truncates
// the file first, so a crash between the truncate and the write leaves a
// half-written gob and the ranking history is gone.
func TestASaveReplacesTheHistoryFileInsteadOfTruncatingIt(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "history.gob")
	clock := &testClock{t: time.Now()}
	h := loadHistory(path, clock.now, nil)
	h.record("fire", "firefox.desktop")

	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat history: %v", err)
	}

	h.record("term", "kitty.desktop")

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat history: %v", err)
	}
	if os.SameFile(before, after) {
		t.Fatal("the history file was rewritten in place: a crash mid-write would wipe it")
	}
}

// A write that fails the way a full disk does must leave the previous history
// readable, and must not leave a staged file behind.
func TestAFailedSaveKeepsThePreviousHistory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "history.gob")
	clock := &testClock{t: time.Now()}
	h := loadHistory(path, clock.now, nil)
	h.record("fire", "firefox.desktop")

	h.stage = func(string, []byte) (string, error) {
		return "", errors.New("no space left on device")
	}
	h.record("term", "kitty.desktop")

	reloaded := loadHistory(path, clock.now, nil)
	if got := reloaded.boost("fire", "firefox.desktop"); got == 0 {
		t.Fatal("the previous history was lost after a failed save")
	}
	if got := reloaded.boost("term", "kitty.desktop"); got != 0 {
		t.Fatalf("boost for the entry that failed to save = %d, want 0", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != "history.gob" {
			t.Fatalf("a failed save left %q behind", entry.Name())
		}
	}
}

// An unreadable history is moved aside, so the next save does not overwrite
// the evidence and the start after it does not read the same broken file.
func TestACorruptHistoryIsQuarantined(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "history.gob")
	if err := os.WriteFile(path, []byte("not a gob stream"), 0o600); err != nil {
		t.Fatal(err)
	}

	clock := &testClock{t: time.Now()}
	h := loadHistory(path, clock.now, nil)
	if got := h.boost("fire", "firefox.desktop"); got != 0 {
		t.Fatalf("boost from corrupt history = %d, want 0", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the corrupt history is still in place: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	quarantined := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "history.gob.") && strings.HasSuffix(entry.Name(), ".corrupt") {
			quarantined++
		}
	}
	if quarantined != 1 {
		t.Fatalf("quarantined files = %d, want 1 (%v)", quarantined, entries)
	}
}

// The state directory holds the queries the user typed, so it is not world
// readable.
func TestTheHistoryDirectoryIsPrivate(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "state")
	path := filepath.Join(dir, "history.gob")
	h := loadHistory(path, (&testClock{t: time.Now()}).now, nil)
	h.record("fire", "firefox.desktop")

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat state dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("state dir mode = %o, want 700", perm)
	}
}
