package launcher

import "slices"

const PlaceholderGlyph = "□"

type Action struct {
	ID, Name, IconName string
	Argv               []string
	ExecArgv           []string
}

type Entry struct {
	ID, Name, GenericName string
	Keywords              []string
	Categories            []string
	Argv                  []string
	// ExecArgv holds the desktop Exec tokens before terminal wrapping and is
	// what ranking searches; empty falls back to Argv (provider entries).
	ExecArgv          []string
	Comment, IconName string
	// WorkDir is the desktop entry's Path= working directory, empty when unset.
	WorkDir  string
	Terminal bool
	// TerminalApp is true when this entry names a supported terminal emulator
	// whose argv can wrap Terminal=true applications with -e.
	TerminalApp bool
	Actions     []Action
}

func cloneEntries(entries []Entry) []Entry {
	out := slices.Clone(entries)
	for i := range out {
		out[i].Keywords = slices.Clone(entries[i].Keywords)
		out[i].Categories = slices.Clone(entries[i].Categories)
		out[i].Argv = slices.Clone(entries[i].Argv)
		out[i].ExecArgv = slices.Clone(entries[i].ExecArgv)
		out[i].Actions = slices.Clone(entries[i].Actions)
		for j := range out[i].Actions {
			out[i].Actions[j].Argv = slices.Clone(entries[i].Actions[j].Argv)
			out[i].Actions[j].ExecArgv = slices.Clone(entries[i].Actions[j].ExecArgv)
		}
	}
	return out
}

type Result struct {
	Entry Entry
	Score int
	// Action is a desktop action ID when this row is one of Entry's actions.
	Action string
}

// ResultBatch is a published result set tagged with the query generation that
// produced it. A consumer compares Gen against the generation returned by Query
// and drops a batch whose Gen is older, because "latest wins" buffering can
// otherwise hand a slow consumer a snapshot computed for a superseded query.
// Results is immutable after publish and may be empty (an empty batch still
// carries its Gen so staleness is decidable).
type ResultBatch struct {
	Gen     uint64
	Results []Result
}
