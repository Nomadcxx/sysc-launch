package launcher

import (
	"slices"
	"testing"

	"github.com/Nomadcxx/sysc-shell/internal/ui"
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

func TestIconPaintDefaultsToPlaceholderSlot(t *testing.T) {
	t.Parallel()

	n := (Icon(nil)).Paint()
	if n.Kind != ui.KindColumn || n.Width != IconSlotSize || len(n.Children) != 1 ||
		n.Children[0].Kind != ui.KindText || n.Children[0].Text != PlaceholderGlyph {
		t.Fatalf("placeholder icon = %+v", n)
	}

	want := &ui.Node{Kind: ui.KindText, Text: "custom"}
	if got := (Icon(func() *ui.Node { return want })).Paint(); got != want {
		t.Fatalf("custom icon = %+v, want %+v", got, want)
	}
}
