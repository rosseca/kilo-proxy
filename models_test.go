package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCatalogNormalizesPricesAndCapabilities(t *testing.T) {
	models, err := parseModels([]byte(`{"data":[
 {"id":"provider/paid","name":"Paid","context_length":200000,"top_provider":{"max_completion_tokens":32000},"pricing":{"prompt":"0.000003","completion":0.000015},"supported_parameters":["tools","reasoning"],"architecture":{"input_modalities":["text","image","unknown"],"output_modalities":["text"]},"mayTrainOnYourPrompts":false,"secret":"never-forward"},
 {"id":"kilo-auto/test","name":"Auto","pricing":{"prompt":"-1","completion":"-1"}},
 {"id":"provider/free","name":"Free","pricing":{"prompt":"0","completion":0},"supported_parameters":[]},
 {"id":"provider/free","name":"Duplicate"},{"id":"bad id"}
 ]}`))
	if err != nil || len(models) != 3 {
		t.Fatalf("models=%v err=%v", models, err)
	}
	auto, free, paid := models[0], models[1], models[2]
	if auto.InputPrice != nil || auto.OutputPrice != nil || auto.Tools != nil {
		t.Fatal("unknown capabilities or variable prices misrepresented")
	}
	if free.InputPrice == nil || *free.InputPrice != 0 || free.Tools == nil || *free.Tools {
		t.Fatal("explicit zero/false lost")
	}
	if paid.InputPrice == nil || *paid.InputPrice != 3 || *paid.OutputPrice != 15 || !*paid.Tools || !*paid.Reasoning || paid.ContextWindow != 200000 || len(paid.InputModalities) != 2 {
		t.Fatalf("incorrect normalization: %+v", paid)
	}
	encoded, _ := json.Marshal(models)
	if strings.Contains(string(encoded), "never-forward") {
		t.Fatal("raw catalog fields leaked")
	}
	for _, body := range []string{`{}`, `{"data":null}`, `{"data":{}}`, `{"data":[{"id":"bad id"}]}`, `{"data":[]} trailing`} {
		if _, err := parseModels([]byte(body)); err == nil {
			t.Errorf("accepted invalid catalog %s", body)
		}
	}
	for _, price := range []string{`"NaN"`, `"Inf"`, `null`, `"-1"`, `{}`} {
		if tokenPrice(json.RawMessage(price)) != nil {
			t.Errorf("accepted invalid price %s", price)
		}
	}
}

func TestCatalogPublicAndOrganizationAuthentication(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "public", true: "organization"}[configured], func(t *testing.T) {
			a := testApp(t)
			a.apiKey = "personal-secret"
			if configured {
				a.config.OrgID = "team"
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/gateway/models" {
					t.Error("wrong catalog route")
				}
				wantAuth, wantOrg := "", ""
				if configured {
					wantAuth, wantOrg = "Bearer personal-secret", "team"
				}
				if r.Header.Get("Authorization") != wantAuth || r.Header.Get("X-KiloCode-OrganizationId") != wantOrg {
					t.Error("incorrect catalog authentication")
				}
				io.WriteString(w, `{"data":[{"id":"vendor/model"}]}`)
			}))
			defer upstream.Close()
			setUpstream(a, upstream.URL)
			w := adminRequest(a, "models", `{}`)
			if w.Code != 200 || strings.Contains(w.Body.String(), "personal-secret") {
				t.Fatalf("unsafe response: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestCatalogRejectsStaleConnection(t *testing.T) {
	a := testApp(t)
	started, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		io.WriteString(w, `{"data":[{"id":"vendor/model"}]}`)
	}))
	defer upstream.Close()
	setUpstream(a, upstream.URL)
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- adminRequest(a, "models", `{}`) }()
	<-started
	a.mu.Lock()
	a.catalogRevision++
	a.mu.Unlock()
	close(release)
	if w := <-result; w.Code != 409 {
		t.Fatalf("stale catalog accepted: %d", w.Code)
	}
}

func TestCatalogLimitsAndRedactsUpstreamErrors(t *testing.T) {
	for _, mode := range []string{"oversize", "invalid", "unauthorized"} {
		t.Run(mode, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "oversize":
					io.WriteString(w, strings.Repeat(" ", (8<<20)+1))
				case "invalid":
					io.WriteString(w, `{"private":"upstream-secret"}`)
				case "unauthorized":
					w.WriteHeader(401)
					io.WriteString(w, "upstream-secret")
				}
			}))
			defer upstream.Close()
			a := testApp(t)
			setUpstream(a, upstream.URL)
			w := adminRequest(a, "models", `{}`)
			if w.Code != 502 || strings.Contains(w.Body.String(), "upstream-secret") {
				t.Fatalf("unsafe failure %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestCatalogPreservesPublishedReasoningVariants(t *testing.T) {
	models, err := parseModels([]byte(`{"data":[
 {"id":"openai/gpt-5.6-sol-discounted","opencode":{"variants":{"off":{"reasoning":{"enabled":false,"effort":"none"}},"low":{"reasoning":{"enabled":true,"effort":"low"}},"medium":{"reasoning":{"effort":"medium"}},"high":{"reasoning":{"effort":"high"}},"xhigh":{"reasoning":{"effort":"xhigh"}},"max":{"reasoning":{"effort":"max"}}}}},
 {"id":"z-ai/glm-5.3","supported_parameters":["reasoning_effort"],"opencode":{"variants":{"low":{"reasoning":{"effort":"low"}},"high":{"reasoning":{"effort":"high"}},"max":{"reasoning":{"effort":"max"}},"duplicate":{"reasoning":{"effort":"max"}},"ultra":{"reasoning":{"enabled":false,"effort":"ultra"}},"invalid":{"reasoning":{"effort":"invalid"}},"budget":{"reasoning":{"max_tokens":1000}},"broken":42}}}
 ]}`))
	if err != nil || len(models) != 2 {
		t.Fatal(err)
	}
	if got := strings.Join(models[0].ReasoningEfforts, ","); got != "none,low,medium,high,xhigh,max" {
		t.Fatal(got)
	}
	if got := strings.Join(models[1].ReasoningEfforts, ","); got != "low,high,max" {
		t.Fatal(got)
	}
	if models[1].Reasoning == nil || !*models[1].Reasoning {
		t.Fatal("reasoning_effort not recognized")
	}
	for _, raw := range []string{`null`, `[]`, `{"variants":42}`, `{"variants":{"xhigh":{"reasoning":{"max_tokens":1000}}}}`} {
		if got := catalogReasoningEfforts(json.RawMessage(raw)); len(got) != 0 {
			t.Fatal(got)
		}
	}
}
