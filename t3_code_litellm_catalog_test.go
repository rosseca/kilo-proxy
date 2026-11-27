package main

import (
	"context"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestT3LiteLLMCatalogUsesVisiblePublicAliasesAndExactGroups(t *testing.T) {
	a := testApp(t)
	const base = "https://gateway.invalid/empresa/v1"
	a.config.LiteLLM = liteLLMSettings{URL: base, Enabled: true, AuthMode: liteLLMBrowserMode, VaultRef: "fixture-browser-session"}
	a.liteLLMBrowser = &liteLLMBrowserCredential{Access: "fixture-team-access-token", RefreshAfter: time.Now().Add(time.Hour), ExpiresAt: time.Now().Add(2 * time.Hour)}
	a.apiKey = "fixture-unrelated-kilo-key"
	a.config.OrgID = "fixture-unrelated-kilo-org"
	connection, ready := a.liteLLMConnectionLocked()
	if !ready {
		t.Fatal("LiteLLM test connection unavailable")
	}
	prefix := liteLLMNamespace(connection.Base)
	var paths []string
	a.transport = liteLLMSettingsTestTransport(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		if r.Method != http.MethodGet || r.URL.Host != "gateway.invalid" || r.URL.RawQuery != "" ||
			r.Header.Get("Authorization") != "Bearer fixture-team-access-token" || r.Header.Get("X-KiloCode-OrganizationId") != "" {
			t.Fatal("catalog must use only the connected LiteLLM account at its configured address")
		}
		switch r.URL.Path {
		case "/empresa/v1/models":
			return liteLLMSettingsTestResponse(r, http.StatusOK, `{"data":[{"id":"codigo"},{"id":"claude-equipo"},{"id":"ambiguous"},{"id":"openai/unverified","owned_by":"openai"},{"id":"mixed-model"},{"id":"Case-Alias"}]}`), nil
		case "/empresa/v1/access_group":
			return liteLLMSettingsTestResponse(r, http.StatusOK, `[
				{"access_group_name":" GPT ","access_model_names":["codigo","private","ambiguous","CASE-ALIAS"]},
				{"access_group_name":"AnThRoPiC","access_model_names":["claude-equipo","ambiguous"]},
				{"access_group_name":"OpenAI","access_model_names":["ambiguous","codigo"]},
				{"access_group_name":"GPT or Claude","access_model_names":["mixed-model"]}
			]`), nil
		default:
			t.Fatalf("unexpected catalog path %q", r.URL.Path)
			return nil, nil
		}
	})
	catalog, err := a.loadT3LiteLLMCatalog(context.Background())
	if err != nil || catalog.AccountScope != liteLLMAccountScope(connection) || len(catalog.Models) != 6 {
		t.Fatalf("unexpected LiteLLM catalog: %+v, %v", catalog, err)
	}
	for _, model := range catalog.Models {
		if model.Connection != "litellm" || !strings.HasPrefix(model.ID, prefix) {
			t.Fatalf("wrong model origin %q", model.ID)
		}
	}
	if expected := (map[string]string{prefix + "codigo": "openai", prefix + "claude-equipo": "claude"}); !maps.Equal(catalog.Families, expected) {
		t.Fatalf("classified inaccessible, ambiguous or unverified aliases: %v", catalog.Families)
	}
	if len(paths) != 2 || paths[0] != "/empresa/v1/models" || paths[1] != "/empresa/v1/access_group" {
		t.Fatalf("unexpected discovery requests: %v", paths)
	}
}

func TestT3LiteLLMCatalogAllowsManualChoicesWhenGroupsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"forbidden", http.StatusForbidden, `{"error":"admin required"}`},
		{"old gateway", http.StatusNotFound, `{"detail":"not found"}`},
		{"invalid response", http.StatusOK, `{"data":[{"access_group_name":"GPT","access_model_names":["gpt-equipo"]}]}`},
		{"redirect", http.StatusTemporaryRedirect, ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			a.config.LiteLLM = liteLLMSettings{URL: "https://gateway.invalid/v1", Enabled: true}
			a.liteLLMKey = "fixture-team-key"
			a.transport = liteLLMSettingsTestTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "gateway.invalid" || r.Header.Get("Authorization") != "Bearer fixture-team-key" {
					t.Fatal("catalog must keep the credential scoped to the configured gateway")
				}
				switch r.URL.Path {
				case "/v1/models":
					return liteLLMSettingsTestResponse(r, http.StatusOK, `{"data":[{"id":"gpt-equipo"}]}`), nil
				case "/v1/access_group":
					response := liteLLMSettingsTestResponse(r, tc.status, tc.body)
					if tc.status == http.StatusTemporaryRedirect {
						response.Header.Set("Location", "https://unrelated.invalid/v1/access_group")
					}
					return response, nil
				case "/v1/model/info":
					if r.URL.RawQuery != "include_team_models=true" {
						t.Fatal("deployment lookup must stay scoped to the caller")
					}
					return liteLLMSettingsTestResponse(r, http.StatusUnauthorized, `{"error":"model info unavailable"}`), nil
				default:
					t.Fatalf("unexpected catalog path %q", r.URL.Path)
					return nil, nil
				}
			})
			catalog, err := a.loadT3LiteLLMCatalog(context.Background())
			if err != nil || len(catalog.Models) != 1 || catalog.Models[0].Name != "gpt-equipo" || len(catalog.Families) != 0 {
				t.Fatalf("optional group probe invalidated its visible model: %+v, %v", catalog, err)
			}
		})
	}
}

func TestT3LiteLLMCatalogUsesScopedDeploymentFamilyFallback(t *testing.T) {
	for _, tc := range []struct {
		name         string
		groupsStatus int
		groupsBody   string
	}{
		{"forbidden", http.StatusForbidden, `{"error":"admin required"}`},
		{"empty list", http.StatusOK, `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			a.config.LiteLLM = liteLLMSettings{URL: "https://gateway.invalid/team/v1", Enabled: true}
			a.liteLLMKey = "fixture-team-key"
			connection, ready := a.liteLLMConnectionLocked()
			if !ready {
				t.Fatal("LiteLLM test connection unavailable")
			}
			prefix := liteLLMNamespace(connection.Base)
			var paths []string
			a.transport = liteLLMSettingsTestTransport(func(r *http.Request) (*http.Response, error) {
				paths = append(paths, r.URL.Path)
				if r.Method != http.MethodGet || r.URL.Host != "gateway.invalid" || r.Header.Get("Authorization") != "Bearer fixture-team-key" {
					t.Fatal("model metadata must use the configured LiteLLM connection")
				}
				switch r.URL.Path {
				case "/team/v1/models":
					return liteLLMSettingsTestResponse(r, http.StatusOK, `{"data":[{"id":"equipo-gpt"},{"id":"equipo-claude"},{"id":"manual"},{"id":"contradiction"},{"id":"mixed"},{"id":"partial"},{"id":"openai/unverified"}]}`), nil
				case "/team/v1/access_group":
					return liteLLMSettingsTestResponse(r, tc.groupsStatus, tc.groupsBody), nil
				case "/team/v1/model/info":
					if r.URL.RawQuery != "include_team_models=true" {
						t.Fatal("deployment lookup must stay scoped to the caller")
					}
					return liteLLMSettingsTestResponse(r, http.StatusOK, `{"data":[
						{"model_name":"equipo-gpt","litellm_params":{"model":"bedrock/global.openai.gpt-example"}},
						{"model_name":"equipo-gpt","litellm_params":{"model":"bedrock/converse/openai.gpt-oss-example"}},
						{"model_name":"equipo-claude","litellm_params":{"model":"eu.anthropic.claude-example","custom_llm_provider":"bedrock"}},
						{"model_name":"manual","model_info":{"custom_metadata":{"kilo_model_family":"Anthropic"}}},
						{"model_name":"contradiction","litellm_params":{"model":"bedrock/global.anthropic.claude-example"},"model_info":{"custom_metadata":{"kilo_model_family":"openai"}}},
						{"model_name":"mixed","litellm_params":{"model":"bedrock/global.openai.gpt-example"}},
						{"model_name":"mixed","litellm_params":{"model":"bedrock/global.anthropic.claude-example"}},
						{"model_name":"partial","litellm_params":{"model":"bedrock/global.openai.gpt-example"}},
						{"model_name":"partial","litellm_params":{"model":"openai/compatible"}},
						{"model_name":"openai/unverified","litellm_params":{"model":"openai/gpt-example"}},
						{"model_name":"non-visible","litellm_params":{"model":"bedrock/global.openai.gpt-example"}}
					]}`), nil
				default:
					t.Fatalf("unexpected catalog path %q", r.URL.Path)
					return nil, nil
				}
			})
			catalog, err := a.loadT3LiteLLMCatalog(context.Background())
			if err != nil || len(catalog.Models) != 7 {
				t.Fatalf("unexpected LiteLLM metadata fallback: %+v, %v", catalog, err)
			}
			expected := map[string]string{
				prefix + "equipo-gpt":    "openai",
				prefix + "equipo-claude": "claude",
				prefix + "manual":        "claude",
			}
			if !maps.Equal(catalog.Families, expected) || len(paths) != 3 {
				t.Fatalf("unexpected deployment families %v or requests %v", catalog.Families, paths)
			}
		})
	}
}

func TestT3LiteLLMCatalogDiscardsResultsAfterAccountChange(t *testing.T) {
	a := testApp(t)
	a.config.LiteLLM = liteLLMSettings{URL: "https://gateway.invalid/v1", Enabled: true}
	a.liteLLMKey = "fixture-original-team-key"
	a.transport = liteLLMSettingsTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/v1/models" {
			return liteLLMSettingsTestResponse(r, http.StatusOK, `{"data":[{"id":"original-team-private-model"}]}`), nil
		}
		a.mu.Lock()
		a.liteLLMKey = "fixture-replacement-team-key"
		a.mu.Unlock()
		return liteLLMSettingsTestResponse(r, http.StatusOK, `[]`), nil
	})
	catalog, err := a.loadT3LiteLLMCatalog(context.Background())
	if err == nil || len(catalog.Models) != 0 || catalog.AccountScope != "" {
		t.Fatalf("published model catalog for the previous account: %+v, %v", catalog, err)
	}
}
