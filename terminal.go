package launcher

import (
	"errors"
	"os"
	"os/exec"
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
		words, err := shellwords.Parse(configured)
		if err != nil || len(words) == 0 {
			return nil, ErrNoTerminal
		}
		path, err := lookPath(words[0])
		if err != nil {
			return nil, ErrNoTerminal
		}
		return append([]string{path}, words[1:]...), nil
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
