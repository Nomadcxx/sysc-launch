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

var terminalCandidates = [...]string{"kitty", "foot", "alacritty", "wezterm", "ghostty"}

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
				return append([]string{path}, words[1:]...), nil
			}
		}
	}
	for _, candidate := range terminalCandidates {
		if path, err := lookPath(candidate); err == nil {
			return []string{path}, nil
		}
	}
	return nil, ErrNoTerminal
}

func terminalArgv(terminal []string, argv []string) []string {
	out := make([]string, 0, len(terminal)+len(argv)+1)
	out = append(out, terminal...)
	out = append(out, "-e")
	return append(out, argv...)
}

func isTerminalEmulator(terminal bool, categories, argv []string, lookPath lookPathFunc) bool {
	if terminal || len(argv) == 0 || !slices.Contains(categories, "TerminalEmulator") ||
		!slices.Contains(terminalCandidates[:], filepath.Base(argv[0])) {
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
		if entry.ID != id || !entry.TerminalApp || len(entry.Argv) == 0 ||
			!slices.Contains(terminalCandidates[:], filepath.Base(entry.Argv[0])) {
			continue
		}
		path, err := lookPath(entry.Argv[0])
		if err != nil {
			return nil
		}
		argv := slices.Clone(entry.Argv)
		argv[0] = path
		return argv
	}
	return nil
}
