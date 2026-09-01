package launcher

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

var ErrNoTerminal = errors.New("launcher: no terminal found")

var terminalCandidates = [...]string{"kitty", "foot", "alacritty", "wezterm", "ghostty"}

type getenvFunc func(string) string

func resolveTerminal(getenv getenvFunc, lookPath lookPathFunc) (string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if configured := strings.TrimSpace(getenv("TERMINAL")); configured != "" {
		path, err := lookPath(configured)
		if err != nil {
			return "", ErrNoTerminal
		}
		return path, nil
	}
	for _, candidate := range terminalCandidates {
		if path, err := lookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", ErrNoTerminal
}

func terminalArgv(terminal string, argv []string) []string {
	out := make([]string, 0, len(argv)+2)
	out = append(out, terminal, "-e")
	return append(out, argv...)
}
