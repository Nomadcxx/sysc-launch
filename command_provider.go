package launcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

// ponytail: fixed bounds cap one provider query at 2s, 256 KiB, 4 KiB per
// row, and 200 results; make them configurable only if real providers need more.
const (
	commandProviderTimeout    = 2 * time.Second
	commandProviderMaxOutput  = 256 << 10
	commandProviderMaxResults = 200
	commandProviderMaxLine    = 4 << 10
)

var errCommandProviderOutputLimit = errors.New("command provider output limit exceeded")

// CommandProviderConfig describes one argv-based source of launcher rows.
// Command emits one row per line as `label\tcomment`; each argv token may
// contain `{query}`, which is replaced with the query as data. OnSelect gets
// the original query and the full selected line. Nil OnSelect reports an
// activation error instead of falling through to application spawning.
type CommandProviderConfig struct {
	Name, Prefix, Glyph, Description string
	Command                          []string
	OnSelect                         func(query, selection string) error
}

// CommandProvider adapts a bounded stdout command to the existing provider
// path. Results use their source line as a stable selection identity.
func CommandProvider(cfg CommandProviderConfig) Provider {
	return newCommandProvider(cfg, runCommandProvider, commandProviderTimeout)
}

type commandProviderRunner func(context.Context, []string) ([]byte, error)

func newCommandProvider(cfg CommandProviderConfig, run commandProviderRunner, timeout time.Duration) Provider {
	cfg.Command = append([]string(nil), cfg.Command...)
	rowPrefix := cfg.Prefix + ":row:"
	errorID := cfg.Prefix + ":error"
	return Provider{
		Name: cfg.Name, Prefix: cfg.Prefix, Glyph: cfg.Glyph, Description: cfg.Description,
		Query: func(query string) []Result {
			if len(cfg.Command) == 0 || cfg.Command[0] == "" {
				return commandProviderError(cfg, errorID, errors.New("command is empty"))
			}
			argv := append([]string(nil), cfg.Command...)
			for i := range argv {
				argv[i] = strings.ReplaceAll(argv[i], "{query}", query)
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			out, err := run(ctx, argv)
			if err != nil {
				return commandProviderError(cfg, errorID, err)
			}
			if len(out) > commandProviderMaxOutput {
				return commandProviderError(cfg, errorID, errCommandProviderOutputLimit)
			}
			entries, err := parseCommandProviderRows(cfg, rowPrefix, out)
			if err != nil {
				return commandProviderError(cfg, errorID, err)
			}
			return rank(entries, query, nil)
		},
		Activate: func(query, id, _ string) error {
			if id == errorID {
				return errors.New("command provider query failed; see the result message")
			}
			selection, ok := strings.CutPrefix(id, rowPrefix)
			if !ok {
				return fmt.Errorf("launcher: invalid command provider result %q", id)
			}
			if cfg.OnSelect == nil {
				return errors.New("launcher: command provider has no selection handler")
			}
			return cfg.OnSelect(query, selection)
		},
	}
}

func commandProviderError(cfg CommandProviderConfig, id string, err error) []Result {
	name := cfg.Name
	if name == "" {
		name = "Command provider"
	}
	return []Result{{Entry: Entry{
		ID: id, Name: name + " unavailable", Comment: err.Error(), IconName: cfg.Glyph,
	}}}
}

func parseCommandProviderRows(cfg CommandProviderConfig, idPrefix string, out []byte) ([]Entry, error) {
	if !utf8.Valid(out) {
		return nil, errors.New("command provider output is not valid UTF-8")
	}
	text := string(out)
	entries := make([]Entry, 0, min(commandProviderMaxResults, strings.Count(text, "\n")+1))
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		if len(line) > commandProviderMaxLine {
			return nil, errors.New("command provider row exceeds the 4 KiB limit")
		}
		name, comment, _ := strings.Cut(line, "\t")
		if name == "" {
			return nil, errors.New("command provider row has an empty label")
		}
		if len(entries) < commandProviderMaxResults {
			entries = append(entries, Entry{
				ID: idPrefix + line, Name: name, Comment: comment, IconName: cfg.Glyph,
			})
		}
	}
	return entries, nil
}

func runCommandProvider(ctx context.Context, argv []string) ([]byte, error) {
	if len(argv) == 0 || argv[0] == "" {
		return nil, errors.New("command provider command is empty")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out commandProviderBuffer
	out.cancel = cancel
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if out.exceeded {
			return nil, errCommandProviderOutputLimit
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s: %w", argv[0], err)
	}
	if out.exceeded {
		return nil, errCommandProviderOutputLimit
	}
	return append([]byte(nil), out.Bytes()...), nil
}

type commandProviderBuffer struct {
	bytes.Buffer
	cancel   context.CancelFunc
	exceeded bool
}

func (b *commandProviderBuffer) Write(p []byte) (int, error) {
	remaining := commandProviderMaxOutput - b.Len()
	if len(p) > remaining {
		if remaining > 0 {
			_, _ = b.Buffer.Write(p[:remaining])
		}
		b.exceeded = true
		b.cancel()
		return remaining, errCommandProviderOutputLimit
	}
	return b.Buffer.Write(p)
}
