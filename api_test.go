package launcher_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	launcher "github.com/Nomadcxx/sysc-launch"
)

func TestPublishedServiceAndHistoryAPI(t *testing.T) {
	history := launcher.OpenHistory(filepath.Join(t.TempDir(), "history.gob"), nil)
	ran := false
	service := launcher.NewService(launcher.ServiceConfig{
		Scan: func() []launcher.Entry {
			return []launcher.Entry{{ID: "app.desktop", Name: "App", Argv: []string{"app"}}}
		},
		History: history,
		Rank: func(entries []launcher.Entry, _ string, _ func(string, string) int) []launcher.Result {
			return []launcher.Result{{Entry: entries[0]}}
		},
		Run: func(context.Context, []string) error {
			ran = true
			return nil
		},
		Getenv:   func(string) string { return "" },
		LookPath: func(string) (string, error) { return "", nil },
		Now:      func() time.Time { return time.Unix(1, 0) },
		Logf:     func(string, ...any) {},
	})
	defer service.Close()

	select {
	case got := <-service.Results():
		if len(got) != 1 || got[0].Entry.ID != "app.desktop" {
			t.Fatalf("initial results = %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial results")
	}
	if err := service.Activate("app.desktop", ""); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if !ran {
		t.Fatal("injected runner was not called")
	}
}
