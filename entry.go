package launcher

const PlaceholderGlyph = "□"

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

type Result struct {
	Entry Entry
	Score int
}
