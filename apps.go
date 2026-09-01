package launcher

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-freedesktop/desktopentry"
)

type lookPathFunc func(string) (string, error)
type logFunc func(string, ...any)

func filterEntries(entries []*desktopentry.Entry, currentDesktop string, lookPath lookPathFunc) []*desktopentry.Entry {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	current := strings.FieldsFunc(currentDesktop, func(r rune) bool { return r == ':' })
	out := make([]*desktopentry.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.Hidden || entry.NoDisplay ||
			(len(entry.OnlyShowIn) > 0 && !intersects(entry.OnlyShowIn, current)) ||
			intersects(entry.NotShowIn, current) {
			continue
		}
		if entry.TryExec != "" {
			if _, err := lookPath(entry.TryExec); err != nil {
				continue
			}
		}
		out = append(out, entry)
	}
	return out
}

func intersects(left, right []string) bool {
	for _, a := range left {
		for _, b := range right {
			if a == b {
				return true
			}
		}
	}
	return false
}

// scanDesktopEntries accepts XDG application directories in decreasing
// precedence order: the user directory first, then system directories.
func scanDesktopEntries(dirs []string, logf logFunc) []*desktopentry.Entry {
	byID := make(map[string]*desktopentry.Entry)
	for i := len(dirs) - 1; i >= 0; i-- {
		entries, err := scanDesktopDir(dirs[i], logf)
		if err != nil {
			if logf != nil {
				logf("launcher: scan %s: %v", dirs[i], err)
			}
			continue
		}
		for id, entry := range entries {
			byID[id] = entry
		}
	}

	out := make([]*desktopentry.Entry, 0, len(byID))
	for _, entry := range byID {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func scanDesktopDir(dir string, logf logFunc) (map[string]*desktopentry.Entry, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, err
	}
	out := make(map[string]*desktopentry.Entry)
	err := filepath.WalkDir(dir, func(path string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if logf != nil {
				logf("launcher: scan %s: %v", path, walkErr)
			}
			return nil
		}
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".desktop") {
			return nil
		}
		entry, err := desktopentry.ParseFile(path)
		if err != nil {
			if logf != nil {
				logf("launcher: parse %s: %v", path, err)
			}
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}
		id := strings.TrimSuffix(filepath.ToSlash(rel), ".desktop")
		id = strings.ReplaceAll(id, "/", "-")
		entry.ID = id
		out[id] = entry
		return nil
	})
	return out, err
}
