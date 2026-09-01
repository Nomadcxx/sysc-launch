package launcher

import "github.com/Nomadcxx/sysc-shell/internal/ui"

const (
	IconSlotSize     = 40
	PlaceholderGlyph = "□"
)

type Action struct {
	ID, Name, IconName string
	Argv               []string
}

type Entry struct {
	ID, Name, GenericName string
	Keywords              []string
	Argv                  []string
	Comment, IconName     string
	Terminal              bool
	Actions               []Action
}

// Icon defers projection so the theme-icon slice can replace the v1 text
// placeholder without changing Entry or Result.
type Icon func() *ui.Node

func (i Icon) Paint() *ui.Node {
	if i != nil {
		if n := i(); n != nil {
			return n
		}
	}
	// ponytail: v1 reserves the final 40px slot with a text glyph; sysc-86
	// replaces this closure with a KindImage node after theme icons land.
	return &ui.Node{Kind: ui.KindColumn, Width: IconSlotSize, Children: []*ui.Node{{Kind: ui.KindText, Text: PlaceholderGlyph}}}
}

type Result struct {
	Entry Entry
	Score int
	Icon  Icon
}
