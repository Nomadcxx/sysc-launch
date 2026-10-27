package launcher

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/junegunn/go-shellwords"
)

var ErrNoTerminal = errors.New("launcher: no terminal found")

type terminalCandidate struct {
	name     string
	execArgs []string
}

// ponytail: keep common terminals here; $TERMINAL remains the fallback for
// terminals whose execution syntax is not listed.
var terminalCandidates = [...]terminalCandidate{
	{name: "kitty", execArgs: []string{"-e"}},
	{name: "foot", execArgs: []string{"-e"}},
	{name: "alacritty", execArgs: []string{"-e"}},
	{name: "wezterm", execArgs: []string{"start", "--"}},
	{name: "ghostty", execArgs: []string{"-e"}},
	{name: "konsole", execArgs: []string{"-e"}},
	{name: "gnome-terminal", execArgs: []string{"--"}},
}

type getenvFunc func(string) string

// resolveTerminal returns the terminal's argv prefix (binary plus any flags
// from a $TERMINAL value such as "kitty --single-instance").
func resolveTerminal(getenv getenvFunc, lookPath lookPathFunc) ([]string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if configured := strings.TrimSpace(getenv("TERMINAL")); configured != "" {
		if words, err := shellwords.Parse(configured); err == nil && len(words) > 0 {
			if path, err := lookPath(words[0]); err == nil {
				argv := append([]string{path}, words[1:]...)
				if candidate, ok := terminalCandidateFor(filepath.Base(path)); ok {
					return append(argv, candidate.execArgs...), nil
				}
				return append(argv, "-e"), nil
			}
		}
	}
	for _, candidate := range terminalCandidates {
		if path, err := lookPath(candidate.name); err == nil {
			return append([]string{path}, candidate.execArgs...), nil
		}
	}
	return nil, ErrNoTerminal
}

func terminalArgv(terminal []string, argv []string) []string {
	out := make([]string, 0, len(terminal)+len(argv))
	out = append(out, terminal...)
	return append(out, argv...)
}

func terminalCandidateFor(name string) (terminalCandidate, bool) {
	for _, candidate := range terminalCandidates {
		if candidate.name == name {
			return candidate, true
		}
	}
	return terminalCandidate{}, false
}

func isTerminalEmulator(terminal bool, categories, argv []string, lookPath lookPathFunc) bool {
	if terminal || len(argv) == 0 || !slices.Contains(categories, "TerminalEmulator") {
		return false
	}
	if _, ok := terminalCandidateFor(filepath.Base(argv[0])); !ok {
		return false
	}
	if filepath.Base(argv[0]) == "foot" {
		// ponytail: parse Foot's server switches only; add a focused case if Foot adds another mode.
		for _, arg := range argv[1:] {
			if arg == "--" {
				break
			}
			if arg == "--server" || strings.HasPrefix(arg, "--server=") || strings.HasPrefix(arg, "-s") {
				return false
			}
		}
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	_, err := lookPath(argv[0])
	return err == nil
}

func resolveTerminalEntry(entries []Entry, id string, lookPath lookPathFunc) []string {
	if id == "" {
		return nil
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	for _, entry := range entries {
		if entry.ID != id || !entry.TerminalApp || len(entry.Argv) == 0 {
			continue
		}
		if _, ok := terminalCandidateFor(filepath.Base(entry.Argv[0])); !ok {
			continue
		}
		path, err := lookPath(entry.Argv[0])
		if err != nil {
			return nil
		}
		argv := slices.Clone(entry.Argv)
		argv[0] = path
		if candidate, ok := terminalCandidateFor(filepath.Base(path)); ok {
			argv = append(argv, candidate.execArgs...)
		}
		return argv
	}
	return nil
}
