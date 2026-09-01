package launcher

import (
	"fmt"
	"testing"
)

func TestScoreMatchesEveryApprovedField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		entry Entry
	}{
		{name: "name", entry: Entry{Name: "Needle"}},
		{name: "generic name", entry: Entry{Name: "App", GenericName: "Needle"}},
		{name: "keywords", entry: Entry{Name: "App", Keywords: []string{"Needle"}}},
		{name: "exec", entry: Entry{Name: "App", Argv: []string{"needle"}}},
		{name: "comment", entry: Entry{Name: "App", Comment: "Needle"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rank([]Entry{tt.entry}, "needle", nil)
			if len(got) != 1 {
				t.Fatalf("rank returned %d results", len(got))
			}
		})
	}
}

func TestScoreAppliesFieldPenalty(t *testing.T) {
	t.Parallel()

	name := rank([]Entry{{Name: "Needle"}}, "needle", nil)
	comment := rank([]Entry{{Name: "Other", Comment: "Needle"}}, "needle", nil)
	if len(name) != 1 || len(comment) != 1 {
		t.Fatalf("name=%v comment=%v", name, comment)
	}
	if got := name[0].Score - comment[0].Score; got != 20 {
		t.Fatalf("name score - comment score = %d, want 20", got)
	}
}

func TestScoreSortsByScoreThenName(t *testing.T) {
	t.Parallel()

	got := rank([]Entry{
		{Name: "Zebra", GenericName: "Needle"},
		{Name: "Alpha", GenericName: "Needle"},
		{Name: "Needle"},
	}, "needle", nil)
	if len(got) != 3 || got[0].Entry.Name != "Needle" ||
		got[1].Entry.Name != "Alpha" || got[2].Entry.Name != "Zebra" {
		t.Fatalf("order = %+v", got)
	}
}

func TestScoreCapsEveryQueryAtFifty(t *testing.T) {
	t.Parallel()

	entries := make([]Entry, 60)
	for i := range entries {
		entries[i].Name = fmt.Sprintf("App %02d", 59-i)
		entries[i].Comment = "needle"
	}
	for _, query := range []string{"", "needle"} {
		got := rank(entries, query, nil)
		if len(got) != 50 {
			t.Fatalf("query %q returned %d results", query, len(got))
		}
		if got[0].Entry.Name != "App 00" || got[49].Entry.Name != "App 49" {
			t.Fatalf("query %q bounds = %q .. %q", query, got[0].Entry.Name, got[49].Entry.Name)
		}
	}
}

func TestScoreDoesNotSearchDesktopActions(t *testing.T) {
	t.Parallel()

	got := rank([]Entry{{Name: "Editor", Actions: []Action{{Name: "Needle"}}}}, "needle", nil)
	if len(got) != 0 {
		t.Fatalf("desktop action matched search: %+v", got)
	}
}
