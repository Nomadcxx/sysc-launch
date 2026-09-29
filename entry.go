package launcher

const PlaceholderGlyph = "□"

type Action struct {
	ID, Name, IconName string
	Argv               []string
}

type Entry struct {
	ID, Name, GenericName string
	Keywords              []string
	Categories            []string
	Argv                  []string
	// ExecArgv holds the desktop Exec tokens before terminal wrapping and is
	// what ranking searches; empty falls back to Argv (provider entries).
	ExecArgv []string
	Comment, IconName     string
	// WorkDir is the desktop entry's Path= working directory, empty when unset.
	WorkDir  string
	Terminal bool
	Actions  []Action
}

type Result struct {
	Entry Entry
	Score int
	// Action is a desktop action ID when this row is one of Entry's actions.
	Action string
}
