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

// ServiceConfig wires the launcher service. Nil Scan, Rank, Run, Getenv,
// LookPath, and Now use the XDG desktop scanner, built-in ranker, Niri spawn,
// os.Getenv, exec.LookPath, and time.Now. Non-positive StaleAfter and
// ActivateTimeout use one minute and five seconds. Nil History disables usage
// persistence and boosting; nil Logf suppresses scanner and provider
// diagnostics.
type ServiceConfig struct {
	Scan            func() []Entry
	History         *History
	Rank            func(entries []Entry, query string, boost func(query, identifier string) int) []Result
	Run             func(ctx context.Context, argv []string) error
	Getenv          func(string) string
	LookPath        func(string) (string, error)
	Now             func() time.Time
	Logf            func(string, ...any)
	StaleAfter      time.Duration
	ActivateTimeout time.Duration

	// Providers are appended after Applications, in order. A provider whose
	// prefix is empty, lacks the leading "/", repeats an earlier one, or is
	// "/apps" is logged and skipped. Provider functions run on the service
	// goroutine and block queries and activation while they run, so they
	// must not call back into the Service.
	Providers []Provider
	// ApplicationsGlyph is the Applications row's overview glyph; empty
	// uses PlaceholderGlyph.
	ApplicationsGlyph string
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
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
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

// Activate runs a row of the last published result set on the service
// goroutine. A row whose provider has an Activate function goes to it;
// anything else spawns the entry (or one of its desktop actions) through
// `niri msg action spawn` (D6/D9). Usage is recorded only on success.
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
		boost = s.cfg.History.boost
	}
	registry := buildRegistry(applicationsProvider(func(query string) []Result {
		return s.cfg.Rank(entries, query, boost)
	}, s.cfg.ApplicationsGlyph), s.cfg.Providers, s.cfg.Logf)

	// owners maps each row of the last published set to the registry index
	// of the provider that produced it, so activation routes to that provider.
	type rowKey struct{ id, action string }
	owners := map[rowKey]int{}

	run := func(text string) ([]Result, map[rowKey]int) {
		own := map[rowKey]int{}
		r := route(registry, text)
		if r.provider == nil {
			return r.overview, own
		}
		var out []Result
		// Bare text also asks the inline providers; their rows go first.
		if r.provider == &registry[0] && r.query != "" && !strings.HasPrefix(strings.TrimSpace(text), "/") {
			for i := 1; i < len(registry); i++ {
				if !registry[i].Inline {
					continue
				}
				for _, res := range registry[i].Query(r.query) {
					own[rowKey{res.Entry.ID, res.Action}] = i
					out = append(out, res)
				}
			}
		}
		idx := 0
		for i := range registry {
			if &registry[i] == r.provider {
				idx = i
			}
		}
		rows := r.provider.Query(r.query)
		for _, res := range rows {
			k := rowKey{res.Entry.ID, res.Action}
			if _, taken := own[k]; !taken {
				own[k] = idx
			}
		}
		if out == nil {
			return rows, own // publish the provider's own slice, empty or not
		}
		return append(out, rows...), own
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
				var out []Result
				out, owners = run(lastQuery)
				s.publishResults(out)
			}
		case req := <-s.queryCh:
			lastQuery, lastGen = req.text, req.gen
			out, own := run(req.text)
			if s.gen.Load() == req.gen {
				owners = own
				s.publishResults(out)
			}
		case req := <-s.activateCh:
			recordQuery := lastQuery
			if r := route(registry, lastQuery); r.provider != nil {
				recordQuery = r.query
			}
			if i, ok := owners[rowKey{req.id, req.action}]; ok && registry[i].Activate != nil {
				err := registry[i].Activate(recordQuery, req.id, req.action)
				if err == nil && s.cfg.History != nil {
					s.cfg.History.record(recordQuery, req.id)
				}
				req.reply <- err
				continue
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
		s.cfg.History.record(query, id)
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
