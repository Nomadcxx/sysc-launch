package launcher

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func lookOnly(found map[string]string) func(string) (string, error) {
	return func(name string) (string, error) {
		if path, ok := found[name]; ok {
			return path, nil
		}
		return "", errors.New("not found")
	}
}

func TestCompositorBin(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		found map[string]string
		want  string
	}{
		{"prefers sysc-compositor", map[string]string{"sysc-compositor": "/usr/bin/sysc-compositor", "niri": "/usr/bin/niri"}, "/usr/bin/sysc-compositor"},
		{"falls back to niri", map[string]string{"niri": "/usr/bin/niri"}, "/usr/bin/niri"},
		{"neither is an error", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := compositorBin(lookOnly(tc.found))
			if got != tc.want || (err == nil) != (tc.want != "") {
				t.Fatalf("compositorBin = %q, %v; want %q", got, err, tc.want)
			}
			if err != nil && !strings.Contains(err.Error(), "sysc-compositor") {
				t.Fatalf("error %q does not name what was tried", err)
			}
		})
	}
}

// TestActivateSpawnsThroughSyscCompositor runs the production runner against
// a stand-in sysc-compositor that records its arguments: the binary the
// resolver found is the one that runs, with Niri's spawn argv.
func TestActivateSpawnsThroughSyscCompositor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	record := filepath.Join(dir, "argv")
	bin := filepath.Join(dir, "sysc-compositor")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + record + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	svc := NewService(ServiceConfig{
		Scan: func() []Entry {
			return []Entry{{ID: "firefox.desktop", Name: "Firefox", Argv: []string{"firefox"}}}
		},
		LookPath: lookOnly(map[string]string{"sysc-compositor": bin, "niri": "/nonexistent/niri"}),
	})
	defer svc.Close()
	recvResults(t, svc)
	if err := svc.Activate("firefox.desktop", ""); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("sysc-compositor never ran: %v", err)
	}
	if want := "msg\naction\nspawn\n--\nfirefox\n"; string(got) != want {
		t.Fatalf("sysc-compositor argv %q, want %q", got, want)
	}
}

func TestActivateWithoutCompositorIsErrorNotPanic(t *testing.T) {
	t.Parallel()
	svc := NewService(ServiceConfig{
		Scan: func() []Entry {
			return []Entry{{ID: "firefox.desktop", Name: "Firefox", Argv: []string{"firefox"}}}
		},
		LookPath: lookOnly(nil),
	})
	defer svc.Close()
	recvResults(t, svc)
	if err := svc.Activate("firefox.desktop", ""); err == nil {
		t.Fatal("missing compositor returned nil error")
	}
}
