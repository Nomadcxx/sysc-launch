package launcher

import (
	"errors"
	"slices"
	"testing"

	"github.com/go-freedesktop/desktopentry"
)

func TestExpandExecDropsEmptyFieldCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		exec string
	}{
		{name: "unquoted", exec: "app %f %F %u %U %d %D %n %N %i %c %k %v %m"},
		{name: "quoted", exec: `app "%f" "%u" "%i" "%c" "%k"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry, err := expandDesktopEntry(&desktopentry.Entry{ID: "app", Exec: tt.exec}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(entry.Argv, []string{"app"}) {
				t.Fatalf("argv = %q", entry.Argv)
			}
		})
	}
}

func TestExpandExecIncludesDesktopActions(t *testing.T) {
	t.Parallel()

	raw := &desktopentry.Entry{
		ID: "editor", Name: "Editor", Exec: "editor",
		Actions: []desktopentry.Action{{Name: "New Window", Exec: "editor --new-window"}},
	}
	entry, err := expandDesktopEntry(raw, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.Actions) != 1 || entry.Actions[0].Name != "New Window" ||
		!slices.Equal(entry.Actions[0].Argv, []string{"editor", "--new-window"}) {
		t.Fatalf("actions = %+v", entry.Actions)
	}
}

func TestTerminalEnvironmentWins(t *testing.T) {
	t.Parallel()

	entry, err := expandDesktopEntry(
		&desktopentry.Entry{Exec: "editor file", Terminal: true},
		func(key string) string {
			if key == "TERMINAL" {
				return "custom-terminal"
			}
			return ""
		},
		func(name string) (string, error) {
			if name == "custom-terminal" {
				return "/opt/bin/custom-terminal", nil
			}
			return "", errors.New("not found")
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/opt/bin/custom-terminal", "-e", "editor", "file"}
	if !slices.Equal(entry.Argv, want) {
		t.Fatalf("argv = %q, want %q", entry.Argv, want)
	}
}

func TestTerminalFallsBackInPolicyOrder(t *testing.T) {
	t.Parallel()

	var tried []string
	entry, err := expandDesktopEntry(
		&desktopentry.Entry{Exec: "editor", Terminal: true},
		func(string) string { return "" },
		func(name string) (string, error) {
			tried = append(tried, name)
			if name == "alacritty" {
				return "/usr/bin/alacritty", nil
			}
			return "", errors.New("not found")
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tried, []string{"kitty", "foot", "alacritty"}) ||
		!slices.Equal(entry.Argv, []string{"/usr/bin/alacritty", "-e", "editor"}) {
		t.Fatalf("tried=%q argv=%q", tried, entry.Argv)
	}
}

func TestTerminalMissingExcludesEntryAtScanTime(t *testing.T) {
	t.Parallel()

	got := expandDesktopEntries(
		[]*desktopentry.Entry{{ID: "editor", Exec: "editor", Terminal: true}},
		func(string) string { return "" },
		func(string) (string, error) { return "", errors.New("not found") },
		nil,
	)
	if len(got) != 0 {
		t.Fatalf("entries = %+v", got)
	}
}
