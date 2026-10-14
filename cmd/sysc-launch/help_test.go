package main

import (
	"strings"
	"testing"
)

func TestHelpLocalePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, all, messages, lang, want string
	}{
		{"supported LC_ALL wins", "ru_RU.UTF-8", "ja", "de", "ru"},
		{"C LC_ALL stops lookup", "C", "pt", "ja", ""},
		{"POSIX LC_ALL stops lookup", "POSIX.UTF-8", "pt", "ja", ""},
		{"unsupported LC_ALL stops lookup", "de_DE.UTF-8", "pt", "ja", ""},
		{"C LC_MESSAGES stops lookup", "", "C", "ja", ""},
		{"POSIX LC_MESSAGES stops lookup", "", "POSIX", "ja", ""},
		{"unsupported LC_MESSAGES stops lookup", "", "de_DE.UTF-8", "ja", ""},
		{"empty LC_ALL falls through", "", "pt_BR.UTF-8", "ja", "pt"},
		{"empty higher variables fall through", "", "", "ja_JP.UTF-8", "ja"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LC_ALL", tc.all)
			t.Setenv("LC_MESSAGES", tc.messages)
			t.Setenv("LANG", tc.lang)
			if got := helpLocale(); got != tc.want {
				t.Fatalf("helpLocale() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHelpFallsBackToEnglish(t *testing.T) {
	t.Setenv("LC_ALL", "ru_RU.UTF-8")
	if got := help("sysc-launch: initial results: %v\n"); got != "sysc-launch: initial results: %v\n" {
		t.Fatalf("uncatalogued string changed: %q", got)
	}
	t.Setenv("LC_ALL", "")
	t.Setenv("LANG", "en_US.UTF-8")
	if got := help("sysc-launch: unknown command %q\n"); !strings.Contains(got, "%q") {
		t.Fatalf("English fallback lost placeholder: %q", got)
	}
}

func TestHelpTranslatesGuidance(t *testing.T) {
	for _, locale := range []string{"es", "pt", "ja", "ko", "ru"} {
		catalog := helpCatalog(locale)
		if catalog == nil {
			t.Fatalf("%s catalog missing", locale)
		}
		if len(catalog) != 5 {
			t.Fatalf("%s catalog has %d keys, want 5", locale, len(catalog))
		}
		for english, translated := range catalog {
			if translated == "" {
				t.Fatalf("%s: empty value for %q", locale, english)
			}
			if strings.Count(english, "%q") != strings.Count(translated, "%q") {
				t.Fatalf("%s: %q lost %%q placeholders", locale, english)
			}
			if strings.HasSuffix(english, "\n") != strings.HasSuffix(translated, "\n") {
				t.Fatalf("%s: %q newline drift", locale, english)
			}
		}
	}
}
