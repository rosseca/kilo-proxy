package main

import (
	"reflect"
	"testing"
)

func TestChooseApplicationLanguage(t *testing.T) {
	for _, test := range []struct {
		name, saved string
		preferred   []string
		want        string
	}{
		{"saved Spanish wins", "es", []string{"en-US"}, "es"},
		{"saved English wins", "en", []string{"es-ES"}, "en"},
		{"macOS preferred languages", "", []string{"fr-FR", "es-ES", "en-US"}, "es"},
		{"English precedes Spanish", "", []string{"en-GB", "es-ES"}, "en"},
		{"POSIX region and encoding", "", []string{"es_MX.UTF-8"}, "es"},
		{"locale modifier", "", []string{"es_ES@euro"}, "es"},
		{"case and whitespace", "", []string{" ES-ar "}, "es"},
		{"C locale", "", []string{"C.UTF-8", "es-ES"}, "en"},
		{"POSIX locale", "", []string{"POSIX", "es-ES"}, "en"},
		{"unsupported OS", "", []string{"de-DE", "ja-JP"}, "en"},
		{"missing OS", "", nil, "en"},
		{"invalid saved choice redetects", "de", []string{"es-ES"}, "es"},
		{"no substring matches", "", []string{"esperanto", "english", ""}, "en"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := chooseApplicationLanguage(test.saved, test.preferred); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestInitialLanguageKeepsExplicitChoice(t *testing.T) {
	for _, language := range []string{"en", "es"} {
		if got := initialLanguage(language); got != language {
			t.Fatalf("saved %s became %s", language, got)
		}
	}
	if got := initialLanguage(""); got != "en" && got != "es" {
		t.Fatalf("unsupported OS fallback %q", got)
	}
}

func TestAppleLocalePreferences(t *testing.T) {
	for _, test := range []struct {
		name, languages, locale string
		want                    []string
		calls                   int
	}{
		{"quoted array", "(\n    \"fr-FR\",\n    \"es-ES\",\n    en\n)\n", "en_US", []string{"fr-FR", "es-ES", "en"}, 1},
		{"single language", "(es)\n", "en_US", []string{"es"}, 1},
		{"empty language fallback", "()\n", "es_MX\n", []string{"es_MX"}, 2},
		{"failed query fallback", "", "en_GB\n", []string{"en_GB"}, 2},
		{"no preferences", "", "", nil, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			got := appleLocalePreferences(func(key string) []byte {
				calls = append(calls, key)
				if key == "AppleLanguages" {
					return []byte(test.languages)
				}
				if key == "AppleLocale" {
					return []byte(test.locale)
				}
				t.Fatalf("unexpected preferences key %q", key)
				return nil
			})
			if len(got) != len(test.want) || len(got) > 0 && !reflect.DeepEqual(got, test.want) {
				t.Fatalf("locales = %#v, want %#v", got, test.want)
			}
			if len(calls) != test.calls || calls[0] != "AppleLanguages" {
				t.Fatalf("unexpected queries: %v", calls)
			}
		})
	}
}

func TestEnvironmentLocalePreferences(t *testing.T) {
	for _, test := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"explicit override", map[string]string{"LC_ALL": "en_US.UTF-8", "LANGUAGE": "es", "LC_MESSAGES": "es_ES", "LANG": "es_ES"}, "en"},
		{"C override", map[string]string{"LC_ALL": "C", "LANGUAGE": "es"}, "en"},
		{"ordered language list", map[string]string{"LANGUAGE": "fr:es_MX:en", "LANG": "en_US.UTF-8"}, "es"},
		{"messages before region", map[string]string{"LC_MESSAGES": "es_ES", "LANG": "en_US.UTF-8"}, "es"},
		{"language region", map[string]string{"LANG": "es_AR.UTF-8"}, "es"},
		{"unset", nil, "en"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := chooseApplicationLanguage("", environmentLocalePreferences(func(key string) string { return test.env[key] }))
			if got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}
