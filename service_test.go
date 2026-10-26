package launcher

import (
	"context"
	"path/filepath"
	"reflect"
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
	case batch := <-svc.Results():
		return batch.Results
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

func TestServiceApplicationsReturnsImmutableSnapshot(t *testing.T) {
	t.Parallel()

	want := Entry{
		ID: "app.desktop", Name: "App",
		Keywords: []string{"keyword"}, Categories: []string{"Utility"},
		Argv: []string{"/usr/bin/app", "--open"}, ExecArgv: []string{"app", "--open"},
		Actions: []Action{{ID: "new", Name: "New", Argv: []string{"/usr/bin/app", "--new"}, ExecArgv: []string{"app", "--new"}}},
	}
	svc := NewService(ServiceConfig{Scan: func() []Entry { return []Entry{want} }})
	defer svc.Close()

	first, err := svc.Applications()
	if err != nil {
		t.Fatal(err)
	}
	first[0].ID = "changed.desktop"
	first[0].Keywords[0] = "changed"
	first[0].Categories[0] = "changed"
	first[0].Argv[0] = "changed"
	first[0].ExecArgv[0] = "changed"
	first[0].Actions[0].Name = "Changed"
	first[0].Actions[0].Argv[0] = "changed"
	first[0].Actions[0].ExecArgv[0] = "changed"

	second, err := svc.Applications()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second, []Entry{want}) {
		t.Fatalf("later snapshot = %+v, want %+v", second, []Entry{want})
	}
}

func TestServiceApplicationsReturnsClosedError(t *testing.T) {
	svc := NewService(ServiceConfig{Scan: func() []Entry { return nil }})
	svc.Close()
	if _, err := svc.Applications(); err != ErrServiceClosed {
		t.Fatalf("Applications after Close error = %v, want %v", err, ErrServiceClosed)
	}
	if _, err := svc.RefreshApplications(); err != ErrServiceClosed {
		t.Fatalf("RefreshApplications after Close error = %v, want %v", err, ErrServiceClosed)
	}
}

func TestServiceApplicationsWaitsForInitialScan(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseScan := func() { releaseOnce.Do(func() { close(release) }) }
	svc := NewService(ServiceConfig{Scan: func() []Entry {
		close(entered)
		<-release
		return []Entry{{ID: "app.desktop", Name: "App"}}
	}})
	defer svc.Close()
	defer releaseScan()
	<-entered

	gotCh := make(chan []Entry, 1)
	errCh := make(chan error, 1)
	go func() {
		got, err := svc.Applications()
		gotCh <- got
		errCh <- err
	}()
	select {
	case <-gotCh:
		t.Fatal("Applications returned before the initial scan completed")
	case <-time.After(20 * time.Millisecond):
	}

	releaseScan()
	select {
	case got := <-gotCh:
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].ID != "app.desktop" {
			t.Fatalf("Applications = %+v, want the completed initial scan", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Applications did not return after the initial scan")
	}
}

func TestServiceRefreshApplicationsReturnsPublishedRescan(t *testing.T) {
	t.Parallel()

	clock := &atomicClock{}
	var scans atomic.Int32
	svc := NewService(ServiceConfig{Scan: func() []Entry {
		if scans.Add(1) == 1 {
			return []Entry{{ID: "old.desktop", Name: "Old"}}
		}
		return []Entry{{ID: "new.desktop", Name: "New", Keywords: []string{"fresh"}}}
	}, Now: clock.now, StaleAfter: time.Second})
	defer svc.Close()

	recvResults(t, svc)
	fresh, err := svc.RefreshApplications()
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 1 || fresh[0].ID != "old.desktop" || scans.Load() != 1 {
		t.Fatalf("fresh applications = %+v with %d scans, want the initial snapshot and one scan", fresh, scans.Load())
	}

	clock.advance(2 * time.Second)
	got, err := svc.RefreshApplications()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "new.desktop" {
		t.Fatalf("refreshed applications = %+v, want the rescan result", got)
	}
	got[0].Keywords[0] = "mutated"

	latest, err := svc.Applications()
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 1 || latest[0].ID != "new.desktop" || latest[0].Keywords[0] != "fresh" {
		t.Fatalf("published snapshot = %+v, want an unmodified refresh result", latest)
	}
}

func TestServiceRefreshApplicationsUsesInitialScanWhenItIsInFlight(t *testing.T) {
	scanStarted := make(chan struct{})
	releaseScan := make(chan struct{})
	var releaseOnce sync.Once
	var startedOnce sync.Once
	var scans atomic.Int32
	svc := NewService(ServiceConfig{Scan: func() []Entry {
		if scans.Add(1) == 1 {
			startedOnce.Do(func() { close(scanStarted) })
			<-releaseScan
		}
		return []Entry{{ID: "app.desktop", Name: "App"}}
	}})
	defer svc.Close()
	defer releaseOnce.Do(func() { close(releaseScan) })
	<-scanStarted

	gotCh := make(chan []Entry, 1)
	errCh := make(chan error, 1)
	go func() {
		got, err := svc.RefreshApplications()
		gotCh <- got
		errCh <- err
	}()
	releaseOnce.Do(func() { close(releaseScan) })
	select {
	case got := <-gotCh:
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].ID != "app.desktop" {
			t.Fatalf("applications = %+v, want initial scan", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RefreshApplications did not return the initial scan")
	}
	if scans.Load() != 1 {
		t.Fatalf("scan count = %d, want one initial scan", scans.Load())
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

func TestServiceNewerQuerySupersedesInflightCommandProvider(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	provider := newCommandProvider(CommandProviderConfig{
		Name: "Dynamic", Prefix: "/dynamic", Command: []string{"listing", "{query}"},
	}, func(ctx context.Context, argv []string) ([]byte, error) {
		if argv[1] == "slow" {
			close(started)
			select {
			case <-release:
				return []byte("stale row\n"), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return []byte(argv[1] + " row\n"), nil
	}, time.Second)
	svc := NewService(ServiceConfig{
		Scan:      func() []Entry { return nil },
		Providers: []Provider{provider},
	})
	defer svc.Close()
	recvResults(t, svc)

	svc.Query("/dynamic slow")
	<-started
	svc.Query("/dynamic fast")
	close(release)

	got := recvResults(t, svc)
	if len(got) != 1 || got[0].Entry.Name != "fast row" {
		t.Fatalf("results = %+v, want only the newer query's row", got)
	}
	select {
	case stale := <-svc.Results():
		t.Fatalf("superseded command query published %+v", stale)
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

func TestServiceBatchCarriesQueryGeneration(t *testing.T) {
	t.Parallel()

	svc := NewService(ServiceConfig{
		Scan: func() []Entry { return []Entry{{Name: "Alpha"}, {Name: "Beta"}} },
	})
	defer svc.Close()

	recvBatch := func() ResultBatch {
		t.Helper()
		select {
		case b := <-svc.Results():
			return b
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for results")
			return ResultBatch{}
		}
	}

	recvBatch() // initial publish (generation 0: no query submitted yet)

	gen := svc.Query("alpha")
	batch := recvBatch()
	if batch.Gen != gen {
		t.Fatalf("batch generation = %d, want the query's %d", batch.Gen, gen)
	}

	// A batch with no rows still carries its generation so a consumer can
	// decide staleness for empty results too.
	gen = svc.Query("zzz-no-match")
	empty := recvBatch()
	if empty.Gen != gen {
		t.Fatalf("empty batch generation = %d, want the query's %d", empty.Gen, gen)
	}
	if len(empty.Results) != 0 {
		t.Fatalf("expected empty results, got %+v", empty.Results)
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

	h.record("zulu", "zulu.desktop")
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
