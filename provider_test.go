package launcher

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func extraProviders(activated *[]string, mu *sync.Mutex) []Provider {
	record := func(tag string) func(q, id, a string) error {
		return func(q, id, a string) error {
			mu.Lock()
			defer mu.Unlock()
			*activated = append(*activated, tag+":"+q+":"+id+":"+a)
			return nil
		}
	}
	return []Provider{
		{Name: "Calculator", Prefix: "/calc", Glyph: "glyph:calculate", Description: "Arithmetic", Inline: true,
			Query: func(q string) []Result {
				if strings.Contains(q, "*") {
					return []Result{{Entry: Entry{ID: "calc:42", Name: "= 42"}}}
				}
				return nil
			},
			Activate: record("calc")},
		{Name: "Emoji", Prefix: "/emo", Glyph: "glyph:mood", Description: "Emoji",
			Query:    func(q string) []Result { return []Result{{Entry: Entry{ID: "🎉", Name: "party popper"}}} },
			Activate: record("emo")},
		{Name: "Dup", Prefix: "/emo", Query: func(string) []Result { return nil }},
		{Name: "NoSlash", Prefix: "x", Query: func(string) []Result { return nil }},
		{Name: "Apps again", Prefix: "/apps", Query: func(string) []Result { return nil }},
	}
}

func newProviderService(t *testing.T, activated *[]string, mu *sync.Mutex, spawned *[][]string) *Service {
	t.Helper()
	svc := NewService(ServiceConfig{
		Scan: func() []Entry {
			return []Entry{{ID: "alpha.desktop", Name: "Alpha", Argv: []string{"alpha"},
				Actions: []Action{{ID: "new", Name: "New", Argv: []string{"alpha", "--new"}}}}}
		},
		Providers:         extraProviders(activated, mu),
		ApplicationsGlyph: "glyph:apps",
		Run: func(_ context.Context, argv []string) error {
			mu.Lock()
			defer mu.Unlock()
			*spawned = append(*spawned, argv)
			return nil
		},
		Logf: func(string, ...any) {},
	})
	t.Cleanup(svc.Close)
	recvResults(t, svc)
	return svc
}

func TestOverviewListsValidProvidersInOrder(t *testing.T) {
	var mu sync.Mutex
	var activated []string
	var spawned [][]string
	svc := newProviderService(t, &activated, &mu, &spawned)
	svc.Query("/")
	got := recvResults(t, svc)
	var names []string
	for _, r := range got {
		names = append(names, r.Entry.Name+"|"+r.Entry.IconName)
	}
	want := []string{"Applications|glyph:apps", "Calculator|glyph:calculate", "Emoji|glyph:mood"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("overview %v, want %v (duplicate, slashless and /apps providers skipped)", names, want)
	}
}

// Review focus 2.
func TestInlineRowActivatesThroughItsProvider(t *testing.T) {
	var mu sync.Mutex
	var activated []string
	var spawned [][]string
	svc := newProviderService(t, &activated, &mu, &spawned)
	svc.Query("6*7")
	got := recvResults(t, svc)
	if len(got) == 0 || got[0].Entry.ID != "calc:42" {
		t.Fatalf("inline row not first: %+v", got)
	}
	if err := svc.Activate("calc:42", ""); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(activated) != 1 || activated[0] != "calc:6*7:calc:42:" || len(spawned) != 0 {
		t.Fatalf("activated %v spawned %v", activated, spawned)
	}
}

func TestPrefixedProviderGetsTheStrippedQuery(t *testing.T) {
	var mu sync.Mutex
	var activated []string
	var spawned [][]string
	svc := newProviderService(t, &activated, &mu, &spawned)
	svc.Query("/emo party")
	recvResults(t, svc)
	if err := svc.Activate("🎉", ""); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(activated) != 1 || activated[0] != "emo:party:🎉:" {
		t.Fatalf("activated %v", activated)
	}
}

func TestApplicationRowsAndActionsStillSpawn(t *testing.T) {
	var mu sync.Mutex
	var activated []string
	var spawned [][]string
	svc := newProviderService(t, &activated, &mu, &spawned)
	svc.Query("alpha")
	recvResults(t, svc)
	if err := svc.Activate("alpha.desktop", "new"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(spawned) != 1 || spawned[0][len(spawned[0])-1] != "--new" {
		t.Fatalf("spawned %v", spawned)
	}
}

// Review focus 3.
func TestActivateUnknownRowFallsBackToSpawnPath(t *testing.T) {
	var mu sync.Mutex
	var activated []string
	var spawned [][]string
	svc := newProviderService(t, &activated, &mu, &spawned)
	svc.Query("alpha")
	recvResults(t, svc)
	err := svc.Activate("calc:42", "")
	if err == nil || !strings.Contains(err.Error(), "no entry") {
		t.Fatalf("stale provider row: err = %v", err)
	}
	if errors.Is(err, ErrServiceClosed) {
		t.Fatal("wrong error")
	}
}

func TestResultActionSurvivesThePublish(t *testing.T) {
	svc := NewService(ServiceConfig{
		Scan: func() []Entry { return []Entry{{ID: "a", Name: "A"}} },
		Rank: func(es []Entry, q string, _ func(string, string) int) []Result {
			return []Result{{Entry: es[0], Action: "new"}}
		},
	})
	t.Cleanup(svc.Close)
	recvResults(t, svc)
	svc.Query("a")
	if got := recvResults(t, svc); len(got) != 1 || got[0].Action != "new" {
		t.Fatalf("got %+v", got)
	}
}

func TestProviderActivationIsRecordedInHistory(t *testing.T) {
	var mu sync.Mutex
	var activated []string
	h := loadHistory(t.TempDir()+"/history.gob", time.Now, nil)
	svc := NewService(ServiceConfig{
		Scan:      func() []Entry { return nil },
		History:   h,
		Providers: extraProviders(&activated, &mu),
		Logf:      func(string, ...any) {},
	})
	t.Cleanup(svc.Close)
	recvResults(t, svc)
	svc.Query("/emo party")
	recvResults(t, svc)
	if err := svc.Activate("🎉", ""); err != nil {
		t.Fatal(err)
	}
	if h.boost("party", "🎉") == 0 {
		t.Fatal("provider activation not recorded in history")
	}
}

// A query with no matches publishes the provider's own empty slice, not nil:
// consumers read a nil publish as "nothing arrived".
func TestNoMatchPublishesTheProvidersEmptySlice(t *testing.T) {
	svc := NewService(ServiceConfig{
		Scan: func() []Entry { return []Entry{{ID: "a", Name: "A"}} },
		Rank: func([]Entry, string, func(string, string) int) []Result { return []Result{} },
	})
	t.Cleanup(svc.Close)
	recvResults(t, svc)
	svc.Query("zzz")
	if got := recvResults(t, svc); got == nil {
		t.Fatal("no-match publish is nil, want the provider's empty slice")
	}
}
