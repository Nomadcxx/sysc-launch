package launcher

import (
	"bytes"
	"encoding/gob"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Ported from Elephant's pkg/common/history (GPL-3) with the owner's blessing;
// importing it would drag xdg/fastwalk/fsnotify/toml into the shell. One
// deliberate deviation: Elephant takes the prefix-length delta of an arbitrary
// map-iteration key; this port divides by the smallest contributing delta so
// the score is deterministic.

const (
	historyAmountCap = 10
	usageBoostCap    = 25
)

type historyData struct {
	LastUsed time.Time
	Amount   int
}

type history struct {
	mu   sync.Mutex
	path string
	now  func() time.Time
	logf logFunc
	data map[string]map[string]historyData
}

func defaultHistoryPath(getenv getenvFunc) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	base := getenv("XDG_STATE_HOME")
	if base == "" {
		base = filepath.Join(getenv("HOME"), ".local", "state")
	}
	return filepath.Join(base, "sysc-shell", "launcher", "history.gob")
}

func loadHistory(path string, now func() time.Time, logf logFunc) *history {
	if now == nil {
		now = time.Now
	}
	h := &history{
		path: path,
		now:  now,
		logf: logf,
		data: make(map[string]map[string]historyData),
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) && logf != nil {
			logf("launcher: load history: %v", err)
		}
		return h
	}
	if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(&h.data); err != nil {
		if logf != nil {
			logf("launcher: decode history: %v", err)
		}
		h.data = make(map[string]map[string]historyData)
	}
	return h
}

// Record persists one successful activation of identifier under query.
// Callers invoke it only after the spawn succeeds (D6).
func (h *history) Record(query, identifier string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	queries := h.data[query]
	if queries == nil {
		queries = make(map[string]historyData)
		h.data[query] = queries
	}
	usage := queries[identifier]
	usage.LastUsed = h.now()
	usage.Amount = min(usage.Amount+1, historyAmountCap)
	queries[identifier] = usage

	h.saveLocked()
}

// Boost returns the raw usage score for identifier under query. Callers cap
// it (usageBoostCap) when adding it to the D4 textual score.
func (h *history) Boost(query, identifier string) int {
	h.mu.Lock()
	defer h.mu.Unlock()

	amount, lastUsed, delta := h.usageLocked(query, identifier)
	if amount == 0 {
		return 0
	}
	base := 10 - int(h.now().Sub(lastUsed).Hours()/24)
	return max(base*amount, 1) / max(delta, 1)
}

// usageLocked aggregates Amount and the most recent LastUsed across every
// stored query prefix-related to query (all of them when query is empty),
// returning the smallest contributing prefix-length delta.
func (h *history) usageLocked(query, identifier string) (amount int, lastUsed time.Time, delta int) {
	for stored, queries := range h.data {
		if query != "" && !strings.HasPrefix(query, stored) && !strings.HasPrefix(stored, query) {
			continue
		}
		usage, ok := queries[identifier]
		if !ok {
			continue
		}
		amount += usage.Amount
		if usage.LastUsed.After(lastUsed) {
			lastUsed = usage.LastUsed
		}
		if query != "" {
			d := len(stored) - len(query)
			if d < 0 {
				d = -d
			}
			if delta == 0 || d < delta {
				delta = d
			}
		}
	}
	return amount, lastUsed, delta
}

func (h *history) saveLocked() {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(h.data); err != nil {
		if h.logf != nil {
			h.logf("launcher: encode history: %v", err)
		}
		return
	}
	if err := os.MkdirAll(filepath.Dir(h.path), 0o755); err != nil {
		if h.logf != nil {
			h.logf("launcher: create history dir: %v", err)
		}
		return
	}
	if err := os.WriteFile(h.path, buf.Bytes(), 0o600); err != nil && h.logf != nil {
		h.logf("launcher: write history: %v", err)
	}
}
