package launcher

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-freedesktop/desktopentry"
)

func TestExclusions(t *testing.T) {
	t.Parallel()

	lookPath := func(name string) (string, error) {
		if name == "present" {
			return "/usr/bin/present", nil
		}
		return "", errors.New("not found")
	}
	tests := []struct {
		name  string
		entry *desktopentry.Entry
		want  bool
	}{
		{name: "hidden", entry: &desktopentry.Entry{Hidden: true}},
		{name: "no display", entry: &desktopentry.Entry{NoDisplay: true}},
		{name: "only show elsewhere", entry: &desktopentry.Entry{OnlyShowIn: []string{"KDE"}}},
		{name: "only show intersects", entry: &desktopentry.Entry{OnlyShowIn: []string{"GNOME"}}, want: true},
		{name: "not show intersects", entry: &desktopentry.Entry{NotShowIn: []string{"GNOME"}}},
		{name: "not show elsewhere", entry: &desktopentry.Entry{NotShowIn: []string{"KDE"}}, want: true},
		{name: "try exec absent", entry: &desktopentry.Entry{TryExec: "missing"}},
		{name: "try exec present", entry: &desktopentry.Entry{TryExec: "present"}, want: true},
		{name: "clean", entry: &desktopentry.Entry{}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterEntries([]*desktopentry.Entry{tt.entry}, "niri:GNOME", lookPath)
			if (len(got) == 1) != tt.want {
				t.Fatalf("filterEntries returned %d entries, want included=%t", len(got), tt.want)
			}
		})
	}
}

func TestExclusionsPreservePrecedenceTombstonesAndActions(t *testing.T) {
	t.Parallel()

	high, low := t.TempDir(), t.TempDir()
	writeDesktop(t, low, "org.example.Editor.desktop", `[Desktop Entry]
Type=Application
Name=System Editor
Exec=editor
`)
	writeDesktop(t, high, "org.example.Editor.desktop", `[Desktop Entry]
Type=Application
Name=User Editor
Exec=editor
Actions=NewWindow;

[Desktop Action NewWindow]
Name=New Window
Exec=editor --new-window
`)
	writeDesktop(t, low, "org.example.Removed.desktop", `[Desktop Entry]
Type=Application
Name=Removed
Exec=removed
`)
	writeDesktop(t, high, "org.example.Removed.desktop", `[Desktop Entry]
Type=Application
Name=Removed
Hidden=true
`)

	raw := scanDesktopEntries([]string{high, low}, nil)
	got := filterEntries(raw, "niri", func(string) (string, error) { return "", nil })
	if len(got) != 1 || got[0].ID != "org.example.Editor" || got[0].Name != "User Editor" {
		t.Fatalf("entries = %+v", got)
	}
	if len(got[0].Actions) != 1 || got[0].Actions[0].Name != "New Window" {
		t.Fatalf("actions = %+v", got[0].Actions)
	}
	if slices.Contains(got[0].Keywords, "New Window") {
		t.Fatal("desktop action leaked into searchable keywords")
	}
}

func TestExclusionsLogUnreadableDirectoryAndContinue(t *testing.T) {
	t.Parallel()

	valid := t.TempDir()
	writeDesktop(t, valid, "org.example.App.desktop", `[Desktop Entry]
Type=Application
Name=App
Exec=app
`)
	var logs []string
	got := scanDesktopEntries([]string{filepath.Join(t.TempDir(), "missing"), valid}, func(format string, args ...any) {
		logs = append(logs, format)
	})
	if len(got) != 1 || len(logs) != 1 || !strings.Contains(logs[0], "scan") {
		t.Fatalf("entries=%d logs=%v", len(got), logs)
	}
}

func TestXDGAppDirs(t *testing.T) {
	t.Parallel()

	env := func(values map[string]string) getenvFunc {
		return func(key string) string { return values[key] }
	}

	got := xdgAppDirs(env(map[string]string{
		"XDG_DATA_HOME": "/home/u/.local/share",
		"XDG_DATA_DIRS": "/usr/share:/usr/local/share",
	}))
	want := []string{"/home/u/.local/share/applications", "/usr/share/applications", "/usr/local/share/applications"}
	if !slices.Equal(got, want) {
		t.Fatalf("xdgAppDirs = %v, want %v", got, want)
	}

	got = xdgAppDirs(env(map[string]string{"HOME": "/home/u"}))
	want = []string{"/home/u/.local/share/applications", "/usr/local/share/applications", "/usr/share/applications"}
	if !slices.Equal(got, want) {
		t.Fatalf("xdgAppDirs defaults = %v, want %v", got, want)
	}
}

func writeDesktop(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
