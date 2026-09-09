//go:build desktop

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNativeZedPreviewMatchesPreparedURLWithoutRevealingCredential(t *testing.T) {
	u := nativeTestUI(t)
	nativeSeedSharedForTest(t, u, nativeClientModelsForTest()...)
	s := u.sharedClientSelection("zed")
	base, localKey, _ := u.clientBase()
	for _, reveal := range []bool{false, true} {
		text, err := u.clientExport("zed", s, reveal)
		if err != nil {
			t.Fatal(err)
		}
		var config struct {
			LanguageModels struct {
				OpenAICompatible map[string]struct {
					APIURL string `json:"api_url"`
				} `json:"openai_compatible"`
			} `json:"language_models"`
		}
		if err := json.Unmarshal([]byte(text), &config); err != nil {
			t.Fatal(err)
		}
		if config.LanguageModels.OpenAICompatible["kilo-local"].APIURL != zedBaseURL(base, localKey) {
			t.Fatal("Zed preview/export uses a different URL from the prepared credential")
		}
		if strings.Contains(text, localKey) || strings.Contains(text, "kl_local_") {
			t.Fatal("Zed config contains a credential instead of only a versioned URL")
		}
	}
}
