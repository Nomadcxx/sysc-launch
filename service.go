package launcher

import (
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const defaultStaleAfter = time.Minute

type rankFunc func(entries []Entry, query string, boost func(query, identifier string) int) []Result

// ServiceConfig wires the launcher service. Nil fields take the production
// defaults: an XDG desktop scan, the rank in score.go, time.Now, and a
// 60-second rescan staleness window (D12).
type ServiceConfig struct {
	Scan       func() []Entry
	History    *history
	Rank       rankFunc
	Getenv     getenvFunc
	LookPath   lookPathFunc
	Now        func() time.Time
	Logf       logFunc
	StaleAfter time.Duration
}

type queryRequest struct {
	text string
	gen  uint64
}

// Service owns the collector and query goroutines (D12). All state crosses
// goroutine boundaries as immutable snapshots through channels; no Wayland
// types appear in this package.
type Service struct {
	cfg ServiceConfig

	gen     atomic.Uint64
	queryCh chan queryRequest
	openCh  chan struct{}
	snapCh  chan []Entry
	results chan []Result
	done    chan struct{}
	wg      sync.WaitGroup
}

func NewService(cfg ServiceConfig) *Service {
	if cfg.Getenv == nil {
		cfg.Getenv = os.Getenv
	}
	if cfg.Scan == nil {
		getenv, lookPath, logf := cfg.Getenv, cfg.LookPath, cfg.Logf
		cfg.Scan = func() []Entry { return scanApplications(getenv, lookPath, logf) }
	}
	if cfg.Rank == nil {
		cfg.Rank = rank
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.StaleAfter <= 0 {
		cfg.StaleAfter = defaultStaleAfter
	}

	s := &Service{
		cfg:     cfg,
		queryCh: make(chan queryRequest, 1),
		openCh:  make(chan struct{}, 1),
		snapCh:  make(chan []Entry),
		results: make(chan []Result, 1),
		done:    make(chan struct{}),
	}
	s.wg.Add(2)
	go s.collect()
	go s.work()
	return s
}

// Open triggers a rescan when the entry snapshot is stale.
func (s *Service) Open() {
	select {
	case s.openCh <- struct{}{}:
	case <-s.done:
	default:
	}
}

// Query submits a new query; it supersedes any queued or in-flight older one.
func (s *Service) Query(text string) {
	req := queryRequest{text: text, gen: s.gen.Add(1)}
	for {
		select {
		case s.queryCh <- req:
			return
		case <-s.done:
			return
		default:
			select {
			case <-s.queryCh:
			case <-s.done:
				return
			default:
			}
		}
	}
}

// Results publishes ranked results as immutable slices, latest wins.
func (s *Service) Results() <-chan []Result {
	return s.results
}

func (s *Service) Close() {
	close(s.done)
	s.wg.Wait()
}

// collect owns scanning and scan staleness. It scans at start and rescans on
// Open when the snapshot is older than StaleAfter (D12). Keeping the staleness
// decision on this goroutine means one ordered event stream: an Open can never
// race snapshot delivery.
func (s *Service) collect() {
	defer s.wg.Done()
	// Anchor staleness before the scan starts: once Scan is running, observers
	// already order after this timestamp.
	scannedAt := s.cfg.Now()
	if !s.publishSnapshot() {
		return
	}
	for {
		select {
		case <-s.done:
			return
		case <-s.openCh:
			if s.cfg.Now().Sub(scannedAt) <= s.cfg.StaleAfter {
				continue
			}
			scannedAt = s.cfg.Now()
			if !s.publishSnapshot() {
				return
			}
		}
	}
}

func (s *Service) publishSnapshot() bool {
	entries := s.cfg.Scan()
	select {
	case s.snapCh <- entries:
		return true
	case <-s.done:
		return false
	}
}

// work owns the current snapshot, the provider registry, and the ranking.
// Provider query functions run on this goroutine, so their closures may read
// the current entries without further synchronization.
func (s *Service) work() {
	defer s.wg.Done()

	var entries []Entry
	var lastQuery string
	var lastGen uint64

	var boost func(string, string) int
	if s.cfg.History != nil {
		boost = s.cfg.History.Boost
	}
	registry := []Provider{applicationsProvider(func(query string) []Result {
		return s.cfg.Rank(entries, query, boost)
	})}

	run := func(text string) []Result {
		r := route(registry, text)
		if r.provider == nil {
			return r.overview
		}
		return r.provider.Query(r.query)
	}

	for {
		select {
		case <-s.done:
			return
		case snap := <-s.snapCh:
			entries = snap
			// Republish the current query against the new snapshot unless a
			// newer query is already queued.
			if len(s.queryCh) == 0 && s.gen.Load() == lastGen {
				s.publishResults(run(lastQuery))
			}
		case req := <-s.queryCh:
			lastQuery, lastGen = req.text, req.gen
			out := run(req.text)
			if s.gen.Load() == req.gen {
				s.publishResults(out)
			}
		}
	}
}

func (s *Service) publishResults(out []Result) {
	for {
		select {
		case s.results <- out:
			return
		case <-s.done:
			return
		default:
			select {
			case <-s.results:
			case <-s.done:
				return
			default:
			}
		}
	}
}
