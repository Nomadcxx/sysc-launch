package launcher

import (
	"strings"
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
	// Activate runs a row this provider produced, given the routed query.
	// Nil means the Applications behaviour: spawn the entry (or its action).
	Activate func(query, id, action string) error
	// Inline providers are also queried for bare (unprefixed) text; their
	// rows are placed above the default provider's.
	Inline bool
}

// routed is the outcome of parsing one launcher query: either a provider and
// its stripped query, or an overview of providers (provider == nil).
type routed struct {
	provider *Provider
	query    string
	overview []Result
}

// applicationsProvider is the v1 default provider (D10). Its query function
// is bound to the live entry set by the service. An empty glyph uses
// PlaceholderGlyph.
func applicationsProvider(query func(string) []Result, glyph string) Provider {
	if glyph == "" {
		glyph = PlaceholderGlyph
	}
	return Provider{
		Name:        "Applications",
		Prefix:      "/apps",
		Glyph:       glyph,
		Description: "Installed desktop applications",
		Query:       query,
	}
}

// buildRegistry puts Applications first and appends the configured providers
// that have a unique "/" prefix and a query function. A rejected provider is
// logged and skipped, never fatal.
func buildRegistry(apps Provider, extra []Provider, logf func(string, ...any)) []Provider {
	registry := []Provider{apps}
	seen := map[string]bool{apps.Prefix: true}
	for _, p := range extra {
		switch {
		case p.Query == nil:
			logf("launcher: provider %q has no query function; skipped", p.Name)
		case !strings.HasPrefix(p.Prefix, "/") || len(p.Prefix) < 2:
			logf("launcher: provider %q prefix %q must start with /; skipped", p.Name, p.Prefix)
		case seen[p.Prefix]:
			logf("launcher: provider %q prefix %q is taken; skipped", p.Name, p.Prefix)
		default:
			seen[p.Prefix] = true
			registry = append(registry, p)
		}
	}
	return registry
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
		out = append(out, Result{
			Entry: Entry{ID: p.Prefix, Name: p.Name, Comment: p.Description, IconName: p.Glyph},
		})
	}
	return out
}
