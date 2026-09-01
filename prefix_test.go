package launcher

import (
	"testing"

	"github.com/Nomadcxx/sysc-shell/internal/ui"
)

func stubProvider(name, prefix string) Provider {
	return Provider{
		Name:        name,
		Prefix:      prefix,
		Glyph:       PlaceholderGlyph,
		Description: name + " provider",
		Query: func(query string) []Result {
			return []Result{{Entry: Entry{ID: prefix, Name: name + ":" + query}}}
		},
	}
}

func testRegistry() []Provider {
	return []Provider{
		stubProvider("Applications", "/apps"),
		stubProvider("Calculator", "/calc"),
	}
}

func TestRouteEmptyQueryDefaultsToApplications(t *testing.T) {
	t.Parallel()

	got := route(testRegistry(), "")
	if got.provider == nil || got.provider.Prefix != "/apps" || got.query != "" || got.overview != nil {
		t.Fatalf("route(\"\") = %+v", got)
	}
}

func TestRouteBareTextDefaultsToApplications(t *testing.T) {
	t.Parallel()

	got := route(testRegistry(), "firefox")
	if got.provider == nil || got.provider.Prefix != "/apps" || got.query != "firefox" {
		t.Fatalf("route(firefox) = %+v", got)
	}
}

func TestRouteBareSlashReturnsOverview(t *testing.T) {
	t.Parallel()

	got := route(testRegistry(), "/")
	if got.provider != nil || len(got.overview) != 2 {
		t.Fatalf("route(/) = %+v", got)
	}
	row := got.overview[0]
	if row.Entry.Name != "Applications" || row.Entry.ID != "/apps" ||
		row.Entry.Comment != "Applications provider" {
		t.Fatalf("overview row = %+v", row)
	}
	icon := row.Icon.Paint()
	if len(icon.Children) != 1 || icon.Children[0].Kind != ui.KindText ||
		icon.Children[0].Text != PlaceholderGlyph {
		t.Fatalf("overview icon = %+v", icon)
	}
}

func TestRoutePrefixedQueryStripsPrefix(t *testing.T) {
	t.Parallel()

	got := route(testRegistry(), "/apps firefox")
	if got.provider == nil || got.provider.Prefix != "/apps" || got.query != "firefox" {
		t.Fatalf("route(/apps firefox) = %+v", got)
	}

	got = route(testRegistry(), "/calc")
	if got.provider == nil || got.provider.Prefix != "/calc" || got.query != "" {
		t.Fatalf("route(/calc) = %+v", got)
	}
}

func TestRouteUnknownPrefixReturnsFilteredOverview(t *testing.T) {
	t.Parallel()

	got := route(testRegistry(), "/cal 1+1")
	if got.provider != nil || len(got.overview) != 1 || got.overview[0].Entry.Name != "Calculator" {
		t.Fatalf("route(/cal 1+1) = %+v", got)
	}
}

func TestRouteUnknownPrefixWithNoMatchReturnsEmptyOverview(t *testing.T) {
	t.Parallel()

	got := route(testRegistry(), "/zzz foo")
	if got.provider != nil || len(got.overview) != 0 {
		t.Fatalf("route(/zzz foo) = %+v", got)
	}
}

func TestRegistryAppendKeepsRoutingAndOverview(t *testing.T) {
	t.Parallel()

	registry := append(testRegistry(), stubProvider("Emoji", "/emo"))

	if got := route(registry, "/emo grin"); got.provider == nil || got.provider.Prefix != "/emo" || got.query != "grin" {
		t.Fatalf("route(/emo grin) = %+v", got)
	}
	if got := route(registry, "/"); len(got.overview) != 3 || got.overview[2].Entry.Name != "Emoji" {
		t.Fatalf("overview after append = %+v", got.overview)
	}
	if got := route(registry, "firefox"); got.provider == nil || got.provider.Prefix != "/apps" {
		t.Fatalf("default route after append = %+v", got)
	}
}
