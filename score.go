package launcher

import (
	"sort"
	"strings"
	"sync"

	"github.com/junegunn/fzf/src/algo"
	"github.com/junegunn/fzf/src/util"
)

const resultLimit = 50

var initFZF sync.Once

func rank(entries []Entry, query string) []Result {
	query = strings.TrimSpace(query)
	slab := util.MakeSlab(100*1024, 2048)
	results := make([]Result, 0, min(len(entries), resultLimit))
	for _, entry := range entries {
		score, matched := entryScore(entry, query, slab)
		if !matched {
			continue
		}
		results = append(results, Result{Entry: entry, Score: score})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		left, right := strings.ToLower(results[i].Entry.Name), strings.ToLower(results[j].Entry.Name)
		if left != right {
			return left < right
		}
		if results[i].Entry.Name != results[j].Entry.Name {
			return results[i].Entry.Name < results[j].Entry.Name
		}
		return results[i].Entry.ID < results[j].Entry.ID
	})
	if len(results) > resultLimit {
		results = results[:resultLimit]
	}
	return results
}

func entryScore(entry Entry, query string, slab *util.Slab) (int, bool) {
	if query == "" {
		return 0, true
	}
	fields := [...]string{
		entry.Name,
		entry.GenericName,
		strings.Join(entry.Keywords, " "),
		strings.Join(entry.Argv, " "),
		entry.Comment,
	}
	best, matched := 0, false
	for i, field := range fields {
		raw, ok := fuzzyScore(field, query, slab)
		if !ok {
			continue
		}
		score := raw - min(i*5, 50)
		if !matched || score > best {
			best, matched = score, true
		}
	}
	return best, matched
}

func fuzzyScore(candidate, query string, slab *util.Slab) (int, bool) {
	if candidate == "" || query == "" {
		return 0, false
	}
	initFZF.Do(func() { algo.Init("default") })
	chars := util.ToChars([]byte(candidate))
	result, _ := algo.FuzzyMatchV2(false, true, true, &chars, []rune(strings.ToLower(query)), false, slab)
	return result.Score, result.Start >= 0
}
