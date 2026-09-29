package launcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

type recordRunner struct {
	mu          sync.Mutex
	argvs       [][]string
	err         error
	sawDeadline bool
}

func (r *recordRunner) run(ctx context.Context, argv []string) error {
	_, r.sawDeadline = ctx.Deadline()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.argvs = append(r.argvs, append([]string(nil), argv...))
	return r.err
}

func (r *recordRunner) lastArgv() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.argvs) == 0 {
		return nil
	}
	return r.argvs[len(r.argvs)-1]
}

func activateService(t *testing.T, runner *recordRunner, h *History) *Service {
	t.Helper()
	svc := NewService(ServiceConfig{
		Scan: func() []Entry {
			return []Entry{{
				ID:      "firefox.desktop",
				Name:    "Firefox",
				Argv:    []string{"firefox"},
				Actions: []Action{{ID: "new-window", Name: "New Window", Argv: []string{"firefox", "--new-window"}}},
			}}
		},
		Run:     runner.run,
		History: h,
	})
	t.Cleanup(svc.Close)
	recvResults(t, svc)
	return svc
}

func TestActivateSpawnsThroughNiri(t *testing.T) {
	t.Parallel()

	runner := &recordRunner{}
	svc := activateService(t, runner, nil)

	if err := svc.Activate("firefox.desktop", ""); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	want := []string{"niri", "msg", "action", "spawn", "--", "firefox"}
	if got := runner.lastArgv(); !slices.Equal(got, want) {
		t.Fatalf("spawn argv = %v, want %v", got, want)
	}
	if !runner.sawDeadline {
		t.Fatal("spawn ran without a bounded context")
	}
}

func TestActivateActionUsesActionArgv(t *testing.T) {
	t.Parallel()

	runner := &recordRunner{}
	svc := activateService(t, runner, nil)

	if err := svc.Activate("firefox.desktop", "new-window"); err != nil {
		t.Fatalf("Activate action: %v", err)
	}
	want := []string{"niri", "msg", "action", "spawn", "--", "firefox", "--new-window"}
	if got := runner.lastArgv(); !slices.Equal(got, want) {
		t.Fatalf("action argv = %v, want %v", got, want)
	}
}

func TestActivateRecordsUsageOnSuccessOnly(t *testing.T) {
	t.Parallel()

	clock := &testClock{t: time.Now()}
	h := loadHistory(filepath.Join(t.TempDir(), "history.gob"), clock.now, nil)
	runner := &recordRunner{}
	svc := activateService(t, runner, h)

	svc.Query("fire")
	recvResults(t, svc)

	runner.err = errors.New("spawn failed")
	if err := svc.Activate("firefox.desktop", ""); err == nil {
		t.Fatal("failed spawn returned nil error")
	}
	if got := h.boost("fire", "firefox.desktop"); got != 0 {
		t.Fatalf("failed activation recorded usage: boost = %d", got)
	}

	runner.err = nil
	if err := svc.Activate("firefox.desktop", ""); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if got := h.boost("fire", "firefox.desktop"); got != 10 {
		t.Fatalf("successful activation boost = %d, want 10", got)
	}
}

func TestActivateUnknownEntryOrAction(t *testing.T) {
	t.Parallel()

	runner := &recordRunner{}
	svc := activateService(t, runner, nil)

	if err := svc.Activate("missing.desktop", ""); err == nil {
		t.Fatal("unknown entry returned nil error")
	}
	if err := svc.Activate("firefox.desktop", "no-such-action"); err == nil {
		t.Fatal("unknown action returned nil error")
	}
	if runner.lastArgv() != nil {
		t.Fatalf("runner invoked for unknown targets: %v", runner.lastArgv())
	}
}

func TestActivateWithoutNiriIsErrorNotPanic(t *testing.T) {
	t.Parallel()

	svc := NewService(ServiceConfig{
		Scan: func() []Entry {
			return []Entry{{ID: "firefox.desktop", Name: "Firefox", Argv: []string{"firefox"}}}
		},
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
	})
	defer svc.Close()
	recvResults(t, svc)

	if err := svc.Activate("firefox.desktop", ""); err == nil {
		t.Fatal("missing niri returned nil error")
	}
}

func TestActivateRealScanAction(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	appDir := filepath.Join(dir, "applications")
	if err := os.Mkdir(appDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "org.example.Editor.desktop"), []byte(`[Desktop Entry]
Type=Application
Name=Editor
Exec=editor
Actions=NewWindow;

[Desktop Action NewWindow]
Name=New Window
Exec=editor --new-window
`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := func(key string) string {
		switch key {
		case "XDG_DATA_HOME":
			return dir
		case "XDG_DATA_DIRS":
			return "/nonexistent"
		}
		return ""
	}
	entries := scanApplications(env, func(string) (string, error) { return "", errors.New("not found") }, nil)

	runner := &recordRunner{}
	svc := NewService(ServiceConfig{Scan: func() []Entry { return entries }, Run: runner.run})
	t.Cleanup(svc.Close)
	recvResults(t, svc)

	if err := svc.Activate("org.example.Editor", "NewWindow"); err != nil {
		t.Fatalf("Activate scanned action: %v", err)
	}
	want := []string{"niri", "msg", "action", "spawn", "--", "editor", "--new-window"}
	if got := runner.lastArgv(); !slices.Equal(got, want) {
		t.Fatalf("action argv = %q, want %q", got, want)
	}
}

func TestActivateWorkDirSpawnsViaSh(t *testing.T) {
	t.Parallel()

	runner := &recordRunner{}
	svc := NewService(ServiceConfig{
		Scan: func() []Entry {
			return []Entry{{ID: "game", Name: "Game", Argv: []string{"./game"}, WorkDir: "/srv/game"}}
		},
		Run: runner.run,
	})
	t.Cleanup(svc.Close)
	recvResults(t, svc)

	if err := svc.Activate("game", ""); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	want := []string{"niri", "msg", "action", "spawn", "--",
		"sh", "-c", `cd "$1" && shift && exec "$@"`, "sh", "/srv/game", "./game"}
	if got := runner.lastArgv(); !slices.Equal(got, want) {
		t.Fatalf("spawn argv = %q, want %q", got, want)
	}
}

func TestActivateRefusesEmptyArgv(t *testing.T) {
	t.Parallel()

	runner := &recordRunner{}
	svc := NewService(ServiceConfig{
		Scan: func() []Entry {
			return []Entry{{ID: "broken.desktop", Name: "Broken"}}
		},
		Run: runner.run,
	})
	t.Cleanup(svc.Close)
	recvResults(t, svc)

	if err := svc.Activate("broken.desktop", ""); err == nil {
		t.Fatal("empty argv must not spawn")
	}
	if len(runner.argvs) != 0 {
		t.Fatalf("runner called with %v", runner.argvs)
	}
}
