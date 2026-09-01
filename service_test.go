package launcher

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type atomicClock struct{ ns atomic.Int64 }

func (c *atomicClock) now() time.Time          { return time.Unix(0, c.ns.Load()) }
func (c *atomicClock) advance(d time.Duration) { c.ns.Add(int64(d)) }

func recvResults(t *testing.T, svc *Service) []Result {
	t.Helper()
	select {
	case got := <-svc.Results():
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for results")
		return nil
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestServicePublishesInitialSnapshot(t *testing.T) {
	t.Parallel()

	svc := NewService(ServiceConfig{
		Scan: func() []Entry { return []Entry{{Name: "Zulu"}, {Name: "Alpha"}} },
	})
	defer svc.Close()

	got := recvResults(t, svc)
	if len(got) != 2 || got[0].Entry.Name != "Alpha" || got[1].Entry.Name != "Zulu" {
		t.Fatalf("initial results = %+v", got)
	}
}

func TestServiceRescansWhenStaleOnOpen(t *testing.T) {
	t.Parallel()

	clock := &atomicClock{}
	clock.ns.Store(time.Now().UnixNano())
	var scans atomic.Int32
	svc := NewService(ServiceConfig{
		Scan: func() []Entry {
			scans.Add(1)
			return []Entry{{Name: "Alpha"}}
		},
		Now:        clock.now,
		StaleAfter: time.Minute,
	})
	defer svc.Close()

	waitFor(t, "initial scan", func() bool { return scans.Load() == 1 })

	clock.advance(30 * time.Second)
	svc.Open()
	time.Sleep(100 * time.Millisecond)
	if got := scans.Load(); got != 1 {
		t.Fatalf("scans after fresh open = %d, want 1", got)
	}

	clock.advance(31 * time.Second)
	svc.Open()
	waitFor(t, "stale rescan", func() bool { return scans.Load() == 2 })
}

func TestServiceNewerQuerySupersedesInflight(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})
	rankHook := func(entries []Entry, query string, boost func(string, string) int) []Result {
		if query == "slow" {
			close(started)
			<-release
			return []Result{{Entry: Entry{Name: "stale-result"}}}
		}
		return rank(entries, query, boost)
	}
	svc := NewService(ServiceConfig{
		Scan: func() []Entry { return []Entry{{Name: "Fast"}} },
		Rank: rankHook,
	})
	defer svc.Close()

	recvResults(t, svc) // initial empty-query publish

	svc.Query("slow")
	<-started
	svc.Query("fast")
	close(release)

	got := recvResults(t, svc)
	if len(got) != 1 || got[0].Entry.Name != "Fast" {
		t.Fatalf("results = %+v, want the fast query's results", got)
	}

	select {
	case stale := <-svc.Results():
		t.Fatalf("superseded query published %+v", stale)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestServiceResultsAreImmutableSlices(t *testing.T) {
	t.Parallel()

	svc := NewService(ServiceConfig{
		Scan: func() []Entry { return []Entry{{Name: "Alpha"}, {Name: "Beta"}} },
	})
	defer svc.Close()

	first := recvResults(t, svc)
	svc.Query("")
	second := recvResults(t, svc)

	if len(first) != 2 || first[0].Entry.Name != "Alpha" || first[1].Entry.Name != "Beta" {
		t.Fatalf("first results mutated after later query: %+v", first)
	}
	if &first[0] == &second[0] {
		t.Fatal("publications share a backing array")
	}
}

func TestServiceRoutesPrefixQueries(t *testing.T) {
	t.Parallel()

	svc := NewService(ServiceConfig{
		Scan: func() []Entry { return []Entry{{Name: "Alpha"}} },
	})
	defer svc.Close()

	recvResults(t, svc) // initial publish

	svc.Query("/")
	got := recvResults(t, svc)
	if len(got) != 1 || got[0].Entry.Name != "Applications" || got[0].Entry.ID != "/apps" {
		t.Fatalf("overview results = %+v", got)
	}

	svc.Query("/apps alpha")
	got = recvResults(t, svc)
	if len(got) != 1 || got[0].Entry.Name != "Alpha" {
		t.Fatalf("prefixed results = %+v", got)
	}
}

func TestServiceAppliesUsageBoost(t *testing.T) {
	t.Parallel()

	clock := &testClock{t: time.Now()}
	h := loadHistory(filepath.Join(t.TempDir(), "history.gob"), clock.now, nil)
	svc := NewService(ServiceConfig{
		Scan: func() []Entry {
			return []Entry{{ID: "alpha.desktop", Name: "Alpha"}, {ID: "zulu.desktop", Name: "Zulu"}}
		},
		History: h,
	})
	defer svc.Close()

	if got := recvResults(t, svc); len(got) != 2 || got[0].Entry.Name != "Alpha" {
		t.Fatalf("initial results = %+v", got)
	}

	h.Record("zulu", "zulu.desktop")
	svc.Query("")
	if got := recvResults(t, svc); len(got) != 2 || got[0].Entry.Name != "Zulu" {
		t.Fatalf("boosted results = %+v", got)
	}
}

func TestServiceRaceRepublishVsQueries(t *testing.T) {
	t.Parallel()

	clock := &atomicClock{}
	clock.ns.Store(time.Now().UnixNano())
	svc := NewService(ServiceConfig{
		Scan: func() []Entry {
			return []Entry{{Name: "Alpha"}, {Name: "Beta", Comment: "needle"}}
		},
		Now:        clock.now,
		StaleAfter: time.Minute,
	})
	defer svc.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			clock.advance(61 * time.Second)
			svc.Open()
		}
	}()

	for i := 0; i < 20; i++ {
		if i%2 == 0 {
			svc.Query("needle")
		} else {
			svc.Query("")
		}
		recvResults(t, svc)
	}
	wg.Wait()
}
