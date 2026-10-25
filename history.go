package launcher

import (
	"bytes"
	"encoding/gob"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// A launch is remembered against the query that selected it, so the launcher
// can put an entry the user picked before back at the top when the same query
// is typed again. Two effects shape the result:
//
//   - Frequency saturates. The factor uses/(uses+usageSaturation) climbs quickly
//     over the first few launches and then flattens, so the hundredth launch
//     counts for far less than the second.
//   - Recency decays linearly to nothing at historyDecayDays, after which a
//     remembered launch carries no weight at all.
//
// Remembered queries are matched by prefix in either direction, so typing
// "fire" benefits from a remembered "firefox" and the reverse, diluted by how
// far apart the two queries are in length.
//
// For one identifier the contribution is
//
//	round(usageBoostCap * uses/(uses+usageSaturation)
//	      * (historyDecayDays-ageDays)/historyDecayDays / max(distance, 1))
//
// where uses is the total launch count, ageDays is whole days since the most
// recent launch, and distance is the smallest length gap to a remembered query.

const (
	// historyDecayDays is the age at which a remembered launch stops scoring.
	historyDecayDays = 30
	// usageBoostCap bounds what remembered launches may add to a rank score.
	usageBoostCap = 25
	// usageSaturation softens the gain from repeat launches. It is added to the
	// use count before the ratio is taken, so one launch scores a third of the
	// cap and further launches converge towards it.
	usageSaturation = 2
	// usageDay is the unit a remembered launch ages in.
	usageDay = 24 * time.Hour
)

// historyData is one identifier's remembered launches under one query. The
// field names are the on-disk shape of the history file, so renaming them would
// silently discard existing history; change them only alongside a migration.
type historyData struct {
	LastUsed time.Time
	Amount   int
}

// History remembers which entries were launched for which queries and keeps
// them across runs.
type History struct {
	mu   sync.Mutex
	path string
	now  func() time.Time
	logf logFunc
	data map[string]map[string]historyData
}

// DefaultHistory loads usage history from
// $XDG_STATE_HOME/sysc-launch/history.gob, falling back to
// $HOME/.local/state/sysc-launch/history.gob. The logf argument receives
// history load and save diagnostics; nil suppresses those diagnostics.
func DefaultHistory(logf func(string, ...any)) *History {
	return loadHistory(defaultHistoryPath(os.Getenv), time.Now, logf)
}

// OpenHistory loads the on-disk usage history at path. The logf argument
// receives history load and save diagnostics; nil suppresses those diagnostics.
func OpenHistory(path string, logf func(string, ...any)) *History {
	return loadHistory(path, time.Now, logf)
}

func defaultHistoryPath(getenv getenvFunc) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	base := getenv("XDG_STATE_HOME")
	if base == "" {
		base = filepath.Join(getenv("HOME"), ".local", "state")
	}
	return filepath.Join(base, "sysc-launch", "history.gob")
}

func loadHistory(path string, now func() time.Time, logf func(string, ...any)) *History {
	if now == nil {
		now = time.Now
	}
	h := &History{
		path: path,
		now:  now,
		logf: logf,
		data: make(map[string]map[string]historyData),
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			h.report("launcher: load history: %v", err)
		}
		return h
	}
	if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(&h.data); err != nil {
		h.report("launcher: decode history: %v", err)
		h.data = make(map[string]map[string]historyData)
	}
	return h
}

// record remembers one successful launch of identifier under query and writes
// the history back to disk. Callers invoke it only once the launch succeeded.
func (h *History) record(query, identifier string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	byIdentifier := h.data[query]
	if byIdentifier == nil {
		byIdentifier = make(map[string]historyData)
		h.data[query] = byIdentifier
	}
	entry := byIdentifier[identifier]
	// A negative count can only come from a file written by an older or
	// damaged version of this code; start it at zero rather than letting it
	// pull the tally down.
	if entry.Amount < 0 {
		entry.Amount = 0
	}
	if maxInt := int(^uint(0) >> 1); entry.Amount < maxInt {
		entry.Amount++
	}
	entry.LastUsed = h.now()
	byIdentifier[identifier] = entry

	h.saveLocked()
}

// boost returns what remembered launches add to identifier's rank score once
// query has been typed, from zero up to usageBoostCap.
func (h *History) boost(query, identifier string) int {
	h.mu.Lock()
	defer h.mu.Unlock()

	tally := h.tallyLocked(query, identifier)
	if tally.uses <= 0 {
		return 0
	}
	age := h.now().Sub(tally.lastUsed)
	if age < 0 {
		age = 0
	}
	if age >= historyDecayDays*usageDay {
		return 0
	}
	return int(math.Round(usageScore(tally.uses, int(age/usageDay), tally.distance)))
}

// usageScore is the ranking curve: saturating in the use count, linear in
// recency down to zero at historyDecayDays, and diluted by how far the query
// drifted from the one that was remembered.
func usageScore(uses, ageDays, distance int) float64 {
	saturation := float64(uses) / (float64(uses) + usageSaturation)
	recency := float64(historyDecayDays-ageDays) / float64(historyDecayDays)
	return float64(usageBoostCap) * saturation * recency / float64(max(distance, 1))
}

// usageTally summarises how one query relates to an identifier's remembered
// launches.
type usageTally struct {
	// uses is the total launch count, saturating instead of wrapping.
	uses int
	// lastUsed is the most recent launch among the matching queries.
	lastUsed time.Time
	// distance is the smallest length gap between the typed query and a
	// matching remembered query, and zero for an exact match.
	distance int
	// compared reports whether any query was measured by length, so that an
	// exact match keeps outranking a remembered query of equal length.
	compared bool
}

// tallyLocked summarises every remembered query prefix-related to query for
// identifier; an empty query relates to all of them. Keeping the smallest
// distance rather than whichever key the map happens to yield last is what
// makes the score stable between runs.
func (h *History) tallyLocked(query, identifier string) usageTally {
	var tally usageTally
	for recorded, byIdentifier := range h.data {
		if !prefixRelated(query, recorded) {
			continue
		}
		entry, ok := byIdentifier[identifier]
		if !ok {
			continue
		}
		tally.uses = saturatingAdd(tally.uses, entry.Amount)
		if entry.LastUsed.After(tally.lastUsed) {
			tally.lastUsed = entry.LastUsed
		}
		if query == "" {
			continue
		}
		if distance := lengthGap(recorded, query); !tally.compared || distance < tally.distance {
			tally.distance = distance
			tally.compared = true
		}
	}
	return tally
}

// prefixRelated reports whether the query being typed and a remembered query
// share a prefix in either direction: typing further into a remembered query,
// or deleting back out of one.
func prefixRelated(query, recorded string) bool {
	if query == "" {
		return true
	}
	return strings.HasPrefix(query, recorded) || strings.HasPrefix(recorded, query)
}

// lengthGap returns how many characters apart two related queries are.
func lengthGap(a, b string) int {
	gap := len(a) - len(b)
	if gap < 0 {
		return -gap
	}
	return gap
}

// saturatingAdd adds a remembered count to a running total. Counts that are not
// positive are ignored, and the total clamps at the largest int so an oversized
// history cannot wrap it negative.
func saturatingAdd(total, count int) int {
	if count <= 0 {
		return total
	}
	if maxInt := int(^uint(0) >> 1); total > maxInt-count {
		return maxInt
	}
	return total + count
}

func (h *History) saveLocked() {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(h.data); err != nil {
		h.report("launcher: encode history: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(h.path), 0o755); err != nil {
		h.report("launcher: create history dir: %v", err)
		return
	}
	if err := os.WriteFile(h.path, buf.Bytes(), 0o600); err != nil {
		h.report("launcher: write history: %v", err)
	}
}

// report passes a diagnostic to the logger the caller supplied, if any.
func (h *History) report(format string, args ...any) {
	if h.logf != nil {
		h.logf(format, args...)
	}
}
