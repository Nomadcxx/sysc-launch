package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultStaleAfter      = time.Minute
	defaultActivateTimeout = 5 * time.Second
)

type rankFunc func(entries []Entry, query string, boost func(query, identifier string) int) []Result

// runFunc executes an already-built argv with a bounded context.
type runFunc func(ctx context.Context, argv []string) error

// ServiceConfig wires the launcher service. Nil fields take the production
// defaults: an XDG desktop scan, the rank in score.go, time.Now, and a
// 60-second rescan staleness window (D12).
type ServiceConfig struct {
	Scan            func() []Entry
	History         *history
	Rank            rankFunc
	Run             runFunc
	Getenv          getenvFunc
	LookPath        lookPathFunc
	Now             func() time.Time
	Logf            logFunc
	StaleAfter      time.Duration
	ActivateTimeout time.Duration
}

var ErrServiceClosed = errors.New("launcher: service closed")

type queryRequest struct {
	text string
	gen  uint64
}

type activateRequest struct {
	id, action string
	reply      chan error
}

// Service owns the collector and query goroutines (D12). All state crosses
// goroutine boundaries as immutable snapshots through channels; no Wayland
// types appear in this package.
type Service struct {
	cfg ServiceConfig

	gen        atomic.Uint64
	queryCh    chan queryRequest
	activateCh chan activateRequest
	openCh     chan struct{}
	snapCh     chan []Entry
	results    chan []Result
	done       chan struct{}
	wg         sync.WaitGroup
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
	if cfg.ActivateTimeout <= 0 {
		cfg.ActivateTimeout = defaultActivateTimeout
	}

	s := &Service{
		cfg:        cfg,
		queryCh:    make(chan queryRequest, 1),
		activateCh: make(chan activateRequest),
		openCh:     make(chan struct{}, 1),
		snapCh:     make(chan []Entry),
		results:    make(chan []Result, 1),
		done:       make(chan struct{}),
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

// Activate spawns the entry (or one of its desktop actions) through
// `niri msg action spawn` on the service goroutine and records usage only
// when the spawn succeeds (D6/D9).
func (s *Service) Activate(id, action string) error {
	req := activateRequest{id: id, action: action, reply: make(chan error, 1)}
	select {
	case s.activateCh <- req:
	case <-s.done:
		return ErrServiceClosed
	}
	select {
	case err := <-req.reply:
		return err
	case <-s.done:
		return ErrServiceClosed
	}
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
		case req := <-s.activateCh:
			recordQuery := lastQuery
			if r := route(registry, lastQuery); r.provider != nil {
				recordQuery = r.query
			}
			req.reply <- s.activate(entries, recordQuery, req.id, req.action)
		}
	}
}

func (s *Service) activate(entries []Entry, query, id, action string) error {
	var argv []string
	found := false
	for _, entry := range entries {
		if entry.ID != id {
			continue
		}
		argv = entry.Argv
		found = true
		if action != "" {
			argv, found = nil, false
			for _, a := range entry.Actions {
				if a.ID == action {
					argv, found = a.Argv, true
					break
				}
			}
		}
		break
	}
	if !found {
		return fmt.Errorf("launcher: no entry %q action %q", id, action)
	}

	run := s.cfg.Run
	if run == nil {
		run = s.niriRun
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ActivateTimeout)
	defer cancel()
	spawn := append([]string{"niri", "msg", "action", "spawn", "--"}, argv...)
	if err := run(ctx, spawn); err != nil {
		return err
	}
	if s.cfg.History != nil {
		s.cfg.History.Record(query, id)
	}
	return nil
}

// niriRun is the production spawn path: argv only, no shell (D9). A missing
// niri is an activation error, never a panic.
func (s *Service) niriRun(ctx context.Context, argv []string) error {
	lookPath := s.cfg.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if _, err := lookPath("niri"); err != nil {
		return fmt.Errorf("launcher: niri not in PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("launcher: %s: %w: %s", argv[0], err, strings.TrimSpace(string(out)))
	}
	return nil
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
