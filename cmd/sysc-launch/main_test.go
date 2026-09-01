package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	launcher "github.com/Nomadcxx/sysc-launch"
)

type fakeService struct {
	mu              sync.Mutex
	results         chan []launcher.Result
	queries         []string
	activations     []activation
	activateErr     error
	activateStarted chan struct{}
	activateRelease <-chan struct{}
	activateDone    chan struct{}
	closed          atomic.Bool
	closeStarted    chan struct{}
	closeRelease    <-chan struct{}
	closeDone       chan struct{}
}

type activation struct {
	id, action string
}

func newFakeService(snapshots ...[]launcher.Result) *fakeService {
	results := make(chan []launcher.Result, max(1, len(snapshots)))
	for _, snapshot := range snapshots {
		results <- snapshot
	}
	return &fakeService{results: results}
}

func (s *fakeService) Query(query string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries = append(s.queries, query)
}
func (s *fakeService) Results() <-chan []launcher.Result { return s.results }
func (s *fakeService) Activate(id, action string) error {
	s.mu.Lock()
	s.activations = append(s.activations, activation{id: id, action: action})
	err := s.activateErr
	started, release, done := s.activateStarted, s.activateRelease, s.activateDone
	s.mu.Unlock()

	if started != nil {
		close(started)
	}
	if release != nil {
		<-release
	}
	if done != nil {
		close(done)
	}
	return err
}
func (s *fakeService) Close() {
	if s.closeStarted != nil {
		close(s.closeStarted)
	}
	if s.closeRelease != nil {
		<-s.closeRelease
	}
	s.closed.Store(true)
	if s.closeDone != nil {
		close(s.closeDone)
	}
}

func (s *fakeService) queryTexts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.queries...)
}

func (s *fakeService) activationCalls() []activation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]activation(nil), s.activations...)
}

func TestRunQueryWritesOneJSONSnapshot(t *testing.T) {
	entry := launcher.Entry{
		ID:          "editor.desktop",
		Name:        "Editor",
		GenericName: "Text Editor",
		Keywords:    []string{"text", "files"},
		Argv:        []string{"editor"},
		Comment:     "Edit files",
		IconName:    "editor",
	}
	factory := func(logf func(string, ...any)) queryService {
		return launcher.NewService(launcher.ServiceConfig{
			Scan: func() []launcher.Entry { return []launcher.Entry{entry} },
			Logf: logf,
		})
	}
	var stdout, stderr bytes.Buffer

	if status := run([]string{"query"}, &stdout, &stderr, factory, time.Second); status != 0 {
		t.Fatalf("run status = %d, stderr = %q", status, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}

	got := decodeResults(t, stdout.Bytes())
	want := []launcher.Result{{Entry: entry, Score: 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("results = %+v, want %+v", got, want)
	}
}

func TestRunQueryPassesOptionalQueryUnchanged(t *testing.T) {
	initial := []launcher.Result{{Entry: launcher.Entry{Name: "Initial"}}}
	want := []launcher.Result{{
		Entry: launcher.Entry{ID: "needle.desktop", Name: "Needle"},
		Score: 37,
	}}
	service := newFakeService(initial, want)
	var stdout, stderr bytes.Buffer

	status := run(
		[]string{"query", "  Needle  "},
		&stdout,
		&stderr,
		func(func(string, ...any)) queryService { return service },
		time.Second,
	)
	if status != 0 {
		t.Fatalf("run status = %d, stderr = %q", status, stderr.String())
	}
	if got := service.queryTexts(); !reflect.DeepEqual(got, []string{"  Needle  "}) {
		t.Fatalf("queries = %q", got)
	}
	if got := decodeResults(t, stdout.Bytes()); !reflect.DeepEqual(got, want) {
		t.Fatalf("results = %+v, want %+v", got, want)
	}
	if !service.closed.Load() {
		t.Fatal("service was not closed")
	}
}

func TestRunRejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing command", want: "usage:"},
		{name: "extra query argument", args: []string{"query", "one", "two"}, want: "at most one argument"},
		{name: "missing launch ID", args: []string{"launch"}, want: "requires a desktop ID"},
		{name: "empty launch ID", args: []string{"launch", ""}, want: "requires a desktop ID"},
		{name: "extra launch argument", args: []string{"launch", "app.desktop", "action", "extra"}, want: "at most a desktop ID and action ID"},
		{name: "unknown command", args: []string{"bogus"}, want: `unknown command "bogus"`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			var stdout, stderr bytes.Buffer
			status := run(
				test.args,
				&stdout,
				&stderr,
				func(func(string, ...any)) queryService {
					called = true
					return newFakeService()
				},
				time.Second,
			)

			if status == 0 {
				t.Fatal("run returned success")
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q", stdout.String())
			}
			if !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("stderr = %q, want text %q", stderr.String(), test.want)
			}
			if called {
				t.Fatal("service was created for invalid arguments")
			}
		})
	}
}

func TestRunLaunchActivatesTarget(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want activation
	}{
		{
			name: "desktop ID",
			args: []string{"launch", "editor.desktop"},
			want: activation{id: "editor.desktop"},
		},
		{
			name: "action ID",
			args: []string{"launch", "editor.desktop", "new-window"},
			want: activation{id: "editor.desktop", action: "new-window"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newFakeService([]launcher.Result{})
			var stdout, stderr bytes.Buffer

			status := run(
				test.args,
				&stdout,
				&stderr,
				func(func(string, ...any)) queryService { return service },
				time.Second,
			)

			if status != 0 {
				t.Fatalf("run status = %d, stderr = %q", status, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q", stdout.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q", stderr.String())
			}
			if got := service.activationCalls(); !reflect.DeepEqual(got, []activation{test.want}) {
				t.Fatalf("activations = %+v, want %+v", got, test.want)
			}
			if !service.closed.Load() {
				t.Fatal("service was not closed")
			}
		})
	}
}

func TestRunLaunchReportsActivationFailure(t *testing.T) {
	service := newFakeService([]launcher.Result{})
	service.activateErr = errors.New("spawn failed")
	var stdout, stderr bytes.Buffer

	status := run(
		[]string{"launch", "editor.desktop"},
		&stdout,
		&stderr,
		func(func(string, ...any)) queryService { return service },
		time.Second,
	)

	if status != 1 {
		t.Fatalf("run status = %d, want 1", status)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "sysc-launch: activation: spawn failed") {
		t.Fatalf("stderr = %q", got)
	}
	if !service.closed.Load() {
		t.Fatal("service was not closed")
	}
}

func TestRunBoundsActivationAndCleanup(t *testing.T) {
	service := newFakeService([]launcher.Result{})
	service.activateStarted = make(chan struct{})
	service.activateDone = make(chan struct{})
	service.closeStarted = make(chan struct{})
	service.closeDone = make(chan struct{})
	release := make(chan struct{})
	service.activateRelease = release
	service.closeRelease = release
	var releaseOnce sync.Once
	releaseService := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseService()

	var stdout, stderr bytes.Buffer
	returned := make(chan int, 1)
	go func() {
		returned <- run(
			[]string{"launch", "editor.desktop"},
			&stdout,
			&stderr,
			func(func(string, ...any)) queryService { return service },
			time.Millisecond,
		)
	}()

	select {
	case <-service.activateStarted:
	case <-time.After(time.Second):
		t.Fatal("Activate was not invoked")
	}

	var status int
	select {
	case status = <-returned:
	case <-time.After(time.Second):
		t.Fatal("run did not return while Activate and Close were blocked")
	}
	if status != 1 {
		t.Fatalf("run status = %d, want 1", status)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "sysc-launch: activation: context deadline exceeded") {
		t.Fatalf("stderr = %q", got)
	}
	select {
	case <-service.closeStarted:
	case <-time.After(time.Second):
		t.Fatal("Close was not initiated")
	}
	select {
	case <-service.activateDone:
		t.Fatal("Activate returned before it was unblocked")
	case <-service.closeDone:
		t.Fatal("Close returned before it was unblocked")
	default:
	}

	releaseService()
	for name, done := range map[string]<-chan struct{}{
		"Activate": service.activateDone,
		"Close":    service.closeDone,
	} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s did not finish after it was unblocked", name)
		}
	}
	if !service.closed.Load() {
		t.Fatal("service did not finish closing")
	}
}

func TestRunBoundsResultWaits(t *testing.T) {
	tests := []struct {
		name      string
		snapshots [][]launcher.Result
		args      []string
		wantError string
	}{
		{
			name:      "initial snapshot",
			args:      []string{"query"},
			wantError: "initial results: context deadline exceeded",
		},
		{
			name:      "queried snapshot",
			snapshots: [][]launcher.Result{{}},
			args:      []string{"query", "needle"},
			wantError: "query results: context deadline exceeded",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newFakeService(test.snapshots...)
			service.closeStarted = make(chan struct{})
			release := make(chan struct{})
			service.closeRelease = release
			service.closeDone = make(chan struct{})
			var releaseOnce sync.Once
			releaseClose := func() { releaseOnce.Do(func() { close(release) }) }
			defer releaseClose()

			var stdout, stderr bytes.Buffer
			started := time.Now()
			returned := make(chan int, 1)
			go func() {
				returned <- run(
					test.args,
					&stdout,
					&stderr,
					func(func(string, ...any)) queryService { return service },
					time.Millisecond,
				)
			}()

			var status int
			select {
			case status = <-returned:
			case <-time.After(time.Second):
				releaseClose()
				t.Fatal("run did not return while Close was blocked")
			}

			if status == 0 {
				t.Fatal("run returned success")
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("run took %v after timeout", elapsed)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q", stdout.String())
			}
			if !strings.Contains(stderr.String(), test.wantError) {
				t.Fatalf("stderr = %q, want text %q", stderr.String(), test.wantError)
			}
			select {
			case <-service.closeStarted:
			case <-time.After(time.Second):
				t.Fatal("Close was not initiated")
			}
			select {
			case <-service.closeDone:
				t.Fatal("Close returned before it was unblocked")
			default:
			}

			releaseClose()
			select {
			case <-service.closeDone:
			case <-time.After(time.Second):
				t.Fatal("Close did not finish after it was unblocked")
			}
			if !service.closed.Load() {
				t.Fatal("service did not finish closing")
			}
		})
	}
}

func decodeResults(t *testing.T, output []byte) []launcher.Result {
	t.Helper()
	if !bytes.HasSuffix(output, []byte("\n")) {
		t.Fatalf("output does not end in newline: %q", output)
	}
	if len(output) == 0 || output[0] != '[' {
		t.Fatalf("output is not a JSON array: %q", output)
	}

	decoder := json.NewDecoder(bytes.NewReader(output))
	var results []launcher.Result
	if err := decoder.Decode(&results); err != nil {
		t.Fatalf("decode results: %v", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("output contains another JSON value: %v", err)
	}
	return results
}
