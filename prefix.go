package launcher

import (
	"strings"

	"github.com/Nomadcxx/sysc-shell/internal/ui"
)

// Provider is one launcher source behind a "/" prefix. The registry is an
// ordered slice; the first entry is the default provider for bare queries.
// Later providers append without any layout or routing change.
type Provider struct {
	Name        string
	Prefix      string
	Glyph       string
	Description string
	Query       func(query string) []Result
}

// routed is the outcome of parsing one launcher query: either a provider and
// its stripped query, or an overview of providers (provider == nil).
type routed struct {
	provider *Provider
	query    string
	overview []Result
}

// applicationsProvider is the v1 default provider (D10). Its query function
// is bound to the live entry set by the service.
func applicationsProvider(query func(string) []Result) Provider {
	return Provider{
		Name:        "Applications",
		Prefix:      "/apps",
		Glyph:       PlaceholderGlyph,
		Description: "Installed desktop applications",
		Query:       query,
	}
}

func route(registry []Provider, query string) routed {
	query = strings.TrimSpace(query)
	if !strings.HasPrefix(query, "/") {
		return routed{provider: &registry[0], query: query}
	}

	prefix, rest, _ := strings.Cut(query[1:], " ")
	prefix = "/" + prefix
	for i := range registry {
		if registry[i].Prefix == prefix {
			return routed{provider: &registry[i], query: strings.TrimSpace(rest)}
		}
	}
	return routed{overview: overviewRows(registry, prefix[1:])}
}

// overviewRows projects the registry onto results, filtered case-insensitively
// by the unknown prefix text (empty text lists every provider).
func overviewRows(registry []Provider, filter string) []Result {
	filter = strings.ToLower(filter)
	out := make([]Result, 0, len(registry))
	for _, p := range registry {
		if filter != "" &&
			!strings.Contains(strings.ToLower(p.Name), filter) &&
			!strings.Contains(strings.ToLower(p.Prefix), filter) {
			continue
		}
		glyph := p.Glyph
		out = append(out, Result{
			Entry: Entry{ID: p.Prefix, Name: p.Name, Comment: p.Description},
			Icon: Icon(func() *ui.Node {
				return &ui.Node{Kind: ui.KindColumn, Width: IconSlotSize, Children: []*ui.Node{{Kind: ui.KindText, Text: glyph}}}
			}),
		})
	}
	return out
}
