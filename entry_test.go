package launcher

import (
	"slices"
	"testing"
)

func TestEntryCarriesLauncherFields(t *testing.T) {
	t.Parallel()

	action := Action{ID: "new-window", Name: "New Window", Argv: []string{"browser", "--new-window"}}
	entry := Entry{
		ID:          "org.example.Browser",
		Name:        "Browser",
		GenericName: "Web Browser",
		Keywords:    []string{"web", "internet"},
		Argv:        []string{"browser"},
		Comment:     "Browse the web",
		IconName:    "browser",
		Terminal:    true,
		Actions:     []Action{action},
	}
	result := Result{Entry: entry, Score: 42}

	if result.Entry.ID != entry.ID || result.Entry.Name != entry.Name ||
		result.Entry.GenericName != entry.GenericName || result.Entry.Comment != entry.Comment ||
		result.Entry.IconName != entry.IconName || !result.Entry.Terminal || result.Score != 42 ||
		!slices.Equal(result.Entry.Keywords, entry.Keywords) || !slices.Equal(result.Entry.Argv, entry.Argv) ||
		len(result.Entry.Actions) != 1 || !slices.Equal(result.Entry.Actions[0].Argv, action.Argv) {
		t.Fatalf("result lost entry fields: %+v", result)
	}
}
