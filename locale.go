package main

import (
	"strings"
	"sync"
)

// Detect the user's desktop language once. Explicit application settings always
// win, and an unsupported OS preference falls back to English.
var operatingSystemLanguage = sync.OnceValue(func() string {
	return chooseApplicationLanguage("", systemLocalePreferences())
})

func initialLanguage(saved string) string {
	if saved == "en" || saved == "es" {
		return saved
	}
	return operatingSystemLanguage()
}

func chooseApplicationLanguage(saved string, preferred []string) string {
	if saved == "en" || saved == "es" {
		return saved
	}
	for _, locale := range preferred {
		base := strings.ToLower(strings.TrimSpace(locale))
		if i := strings.IndexAny(base, "-_.@"); i >= 0 {
			base = base[:i]
		}
		switch base {
		case "es":
			return "es"
		case "en", "c", "posix":
			return "en"
		}
	}
	return "en"
}

// GUI apps launched from Finder do not reliably inherit locale environment
// variables. AppleLanguages lists the user's preferred UI languages in order.
func appleLocalePreferences(read func(string) []byte) []string {
	parse := func(raw []byte) []string {
		return strings.FieldsFunc(string(raw), func(r rune) bool {
			return r == '(' || r == ')' || r == ',' || r == '"' || r == '\n' || r == '\r' || r == '\t' || r == ' '
		})
	}
	if locales := parse(read("AppleLanguages")); len(locales) != 0 {
		return locales
	}
	return parse(read("AppleLocale"))
}

func environmentLocalePreferences(getenv func(string) string) []string {
	// LC_ALL is an explicit process-wide override; LC_MESSAGES selects UI text.
	if value := strings.TrimSpace(getenv("LC_ALL")); value != "" {
		return []string{value}
	}
	if value := strings.TrimSpace(getenv("LANGUAGE")); value != "" {
		return strings.Split(value, ":")
	}
	if value := strings.TrimSpace(getenv("LC_MESSAGES")); value != "" {
		return []string{value}
	}
	return []string{getenv("LANG")}
}
