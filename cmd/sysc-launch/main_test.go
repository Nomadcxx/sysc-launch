package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	launcher "github.com/Nomadcxx/sysc-launch"
)

type fakeService struct {
	results chan []launcher.Result
	queries []string
	closed  bool
}

func newFakeService(snapshots ...[]launcher.Result) *fakeService {
	results := make(chan []launcher.Result, max(1, len(snapshots)))
	for _, snapshot := range snapshots {
		results <- snapshot
	}
	return &fakeService{results: results}
}

func (s *fakeService) Query(query string)                { s.queries = append(s.queries, query) }
func (s *fakeService) Results() <-chan []launcher.Result { return s.results }
func (s *fakeService) Close()                            { s.closed = true }

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
	if !reflect.DeepEqual(service.queries, []string{"  Needle  "}) {
		t.Fatalf("queries = %q", service.queries)
	}
	if got := decodeResults(t, stdout.Bytes()); !reflect.DeepEqual(got, want) {
		t.Fatalf("results = %+v, want %+v", got, want)
	}
	if !service.closed {
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
		{name: "unknown command", args: []string{"launch"}, want: `unknown command "launch"`},
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
			var stdout, stderr bytes.Buffer
			started := time.Now()

			status := run(
				test.args,
				&stdout,
				&stderr,
				func(func(string, ...any)) queryService { return service },
				time.Millisecond,
			)

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
			if !service.closed {
				t.Fatal("service was not closed")
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
