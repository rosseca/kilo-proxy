package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

const chatGPTResponsesEndpoint = "https://chatgpt.com/backend-api/codex/responses"

func requestModel(r *http.Request) (string, error) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return "", err
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	r.ContentLength = int64(len(raw))
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", nil
	}
	body, err := decodeObject(raw)
	if err != nil {
		return "", errors.New("Invalid inference request JSON.")
	}
	model, _ := body["model"].(string)
	// Existing Kilo clients may omit a model; preserve their gateway validation
	// instead of changing the pre-subscription passthrough contract.
	return model, nil
}

// One shared catalog can contain Kilo credits and ChatGPT subscription models.
// Subscription IDs have their own namespace, so equal model names never change
// how an existing conversation is billed.
func (a *app) fetchModels(ctx context.Context, requireAccount bool) ([]modelInfo, uint64, error) {
	a.mu.Lock()
	revision, key, org := a.catalogRevision, a.apiKey, a.config.OrgID
	chatgpt, connection := a.chatGPTReadyLocked(), a.chatgpt
	kilo := key != "" && org != ""
	identity := connection.identity()
	a.mu.Unlock()
	if !chatgpt {
		models, revision, err := a.fetchKiloModels(ctx, requireAccount)
		a.mu.Lock()
		if a.catalogRevision == revision && !a.chatGPTReadyLocked() {
			a.catalogWarnings = nil
		}
		a.mu.Unlock()
		for i := range models {
			models[i].Connection = "kilo"
		}
		return models, revision, err
	}
	type result struct {
		models     []modelInfo
		err        error
		connection string
	}
	done := make(chan result, 2)
	count := 1
	go func() { models, err := connection.models(ctx); done <- result{models, err, "chatgpt"} }()
	if kilo {
		count++
		go func() { models, _, err := a.fetchKiloModels(ctx, true); done <- result{models, err, "kilo"} }()
	}
	var all []modelInfo
	var warnings []string
	for range count {
		result := <-done
		if result.err != nil {
			warnings = append(warnings, result.connection+": "+result.err.Error())
			continue
		}
		for i := range result.models {
			result.models[i].Connection = result.connection
		}
		all = append(all, result.models...)
	}
	a.mu.Lock()
	changed := a.catalogRevision != revision || a.apiKey != key || a.config.OrgID != org || connection.identity() != identity
	if !changed {
		a.catalogWarnings = warnings
	}
	a.mu.Unlock()
	if changed {
		return nil, revision, &catalogError{409, "A connection changed while loading models. Refresh the catalog."}
	}
	if len(all) == 0 && len(warnings) > 0 {
		return nil, revision, &catalogError{502, strings.Join(warnings, "; ")}
	}
	sort.SliceStable(all, func(i, j int) bool { return strings.ToLower(all[i].Name) < strings.ToLower(all[j].Name) })
	return all, revision, nil
}

// Each model identifies its connection; no global provider switch is needed.
func (a *app) kiloReadyLocked() bool {
	return a.apiKey != "" && a.config.OrgID != "" && !a.authPending() && !a.connectionNeedsSave
}
func (a *app) chatGPTReadyLocked() bool {
	state := a.chatgpt.snapshot()
	return !a.providerChanging && state.Connected && state.Status != "pending"
}
func (a *app) connectionReadyLocked() bool { return a.kiloReadyLocked() || a.chatGPTReadyLocked() }
func (a *app) catalogScopeLocked() string {
	if a.chatgpt.snapshot().Connected {
		return a.config.OrgID + "|chatgpt:" + a.chatgpt.identity()
	}
	return a.config.OrgID
}
func (a *app) claudeDesktopExperimentalLocked() bool {
	return a.chatgpt.snapshot().Connected || a.config.ClaudeDesktopExperimentalModels
}
func (a *app) clientImageSettingsLocked() imageGenerationSettings {
	if a.chatgpt.snapshot().Connected && (a.apiKey == "" || a.config.OrgID == "") {
		return imageGenerationSettings{}
	}
	return a.config.ImageGeneration
}

func (a *app) providerAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, 405, "Method not allowed.")
		return
	}
	var input struct{}
	if !decodeBody(w, r, &input) {
		return
	}
	a.mu.Lock()
	connection := a.chatgpt
	if connection == nil {
		a.mu.Unlock()
		jsonError(w, 503, "ChatGPT connection is unavailable.")
		return
	}
	if r.URL.Path != "/api/chatgpt/refresh" && (a.proxyServer != nil || a.active > 0) {
		a.mu.Unlock()
		jsonError(w, 409, "Stop the proxy before changing ChatGPT authentication.")
		return
	}
	if a.providerChanging {
		a.mu.Unlock()
		jsonError(w, 409, "Wait for the connection action to finish.")
		return
	}
	// Persist the vault slot before starting OAuth, including on first launch.
	if r.URL.Path == "/api/chatgpt/login" {
		if err := writeSettings(a.dir, a.config); err != nil {
			a.mu.Unlock()
			jsonError(w, 500, "Could not save the ChatGPT credential-store reference.")
			return
		}
	}
	changing := r.URL.Path != "/api/chatgpt/refresh"
	if changing {
		a.providerChanging = true
	}
	a.mu.Unlock()
	var err error
	switch r.URL.Path {
	case "/api/chatgpt/login":
		err = connection.begin(r.Context())
	case "/api/chatgpt/cancel":
		connection.cancel()
	case "/api/chatgpt/logout":
		err = connection.logout()
	case "/api/chatgpt/refresh":
		err = connection.refresh(r.Context())
	default:
		a.mu.Lock()
		a.providerChanging = false
		a.mu.Unlock()
		jsonError(w, 404, "Unknown ChatGPT operation.")
		return
	}
	a.mu.Lock()
	if changing {
		a.providerChanging = false
	}
	a.chatGPTQuotaDue = time.Now().Add(time.Minute)
	a.mu.Unlock()
	if err != nil {
		jsonError(w, 502, err.Error())
		return
	}
	a.mu.Lock()
	a.catalogRevision++
	a.mu.Unlock()
	a.state(w)
}

// Poll only the explicitly connected subscription, never delay UI state reads.
func (a *app) ensureChatGPTQuotaLocked() {
	if !a.billingAutoRefresh || !a.chatgpt.snapshot().Connected || !time.Now().After(a.chatGPTQuotaDue) {
		return
	}
	select {
	case <-a.quit:
		return
	default:
	}
	a.chatGPTQuotaDue = time.Now().Add(time.Minute)
	connection := a.chatgpt
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		_ = connection.refresh(ctx)
	}()
}

func normalizeChatGPTBody(raw []byte) ([]byte, error) {
	var body map[string]json.RawMessage
	if json.Unmarshal(raw, &body) != nil || body == nil {
		return nil, errors.New("Invalid Responses request JSON.")
	}
	var model string
	if json.Unmarshal(body["model"], &model) != nil {
		return nil, errors.New("Choose a ChatGPT model.")
	}
	if !strings.HasPrefix(model, "chatgpt/") {
		return nil, errors.New("Choose a model from the ChatGPT subscription catalog.")
	}
	model = strings.TrimPrefix(model, "chatgpt/")
	if model == "" || strings.Contains(model, "/") || !catalogID.MatchString(model) {
		return nil, errors.New("This model belongs to another provider. Choose a model from the ChatGPT catalog.")
	}
	if value := body["previous_response_id"]; len(value) != 0 && string(value) != "null" && string(value) != `""` {
		return nil, errors.New("ChatGPT subscription requests must include conversation history; previous_response_id is not supported with store=false.")
	}
	// Match the Codex subscription protocol used by OpenCode and Oh My Pi.
	// It always streams and does not support caller-set output/sampling budgets.
	for _, key := range []string{"previous_response_id", "max_output_tokens", "max_completion_tokens", "max_tokens", "temperature", "top_p", "stream_options", "metadata", "user", "safety_identifier", "truncation", "background"} {
		delete(body, key)
	}
	body["model"], _ = json.Marshal(model)
	body["stream"], body["store"] = json.RawMessage("true"), json.RawMessage("false")
	var inputText string
	if value := body["input"]; len(value) > 0 && value[0] == '"' && json.Unmarshal(value, &inputText) == nil {
		body["input"], _ = json.Marshal([]map[string]string{{"role": "user", "content": inputText}})
	}
	if string(body["instructions"]) == "null" || len(body["instructions"]) == 0 {
		body["instructions"] = json.RawMessage(`""`)
	}
	var include []string
	if len(body["include"]) != 0 && string(body["include"]) != "null" {
		if json.Unmarshal(body["include"], &include) != nil {
			return nil, errors.New("Invalid include list.")
		}
	}
	found := false
	for _, value := range include {
		if value == "reasoning.encrypted_content" {
			found = true
		}
	}
	if !found {
		include = append(include, "reasoning.encrypted_content")
	}
	body["include"], _ = json.Marshal(include)
	return json.Marshal(body)
}

func (a *app) serveChatGPT(w *statusWriter, r *http.Request) {

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		status := http.StatusBadRequest
		var oversized *http.MaxBytesError
		if errors.As(err, &oversized) {
			status = http.StatusRequestEntityTooLarge
		}
		jsonError(w, status, "Could not read request; the local limit is 32 MiB.")
		return
	}
	converted, protocol, err := prepareChatGPTRequest(r.URL.Path, raw)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	converted, err = normalizeChatGPTBody(converted)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	token, account, err := a.chatgpt.credentials(r.Context())
	if err != nil {
		jsonError(w, 401, err.Error())
		return
	}
	capture, _ := r.Context().Value(traceContextKey{}).(*traceCapture)
	if capture != nil {
		capture.addSecrets(token, account)
	}
	endpoint := a.chatGPTResponsesURL
	if endpoint == "" {
		endpoint = chatGPTResponsesEndpoint
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(converted))
	if err != nil {
		jsonError(w, 502, "Invalid ChatGPT connection.")
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("ChatGPT-Account-Id", account)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("Originator", "kilo-proxy")
	req.Header.Set("User-Agent", "Kilo-Proxy/"+version)
	if observer, _ := r.Context().Value(usageContextKey{}).(*usageObserver); observer != nil {
		req.Header.Set("Session_id", observer.usage.Session)
	}
	client := &http.Client{Transport: traceTransport{a.transport}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		jsonError(w, 502, "Could not connect to ChatGPT. Check your network and connection status.")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// Read a bounded error body so opt-in diagnostics can capture it using
		// the same credential redaction as successful responses.
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		// Never relay raw auth service errors, cookies or authentication headers.
		status := response.StatusCode
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		message := fmt.Sprintf("ChatGPT returned HTTP %d. Review the subscription connection and selected model.", response.StatusCode)
		if status == 401 || status == 403 {
			message = "ChatGPT rejected this account or model. Reconnect ChatGPT and check your plan's model access."
		}
		if status == 429 {
			message = "ChatGPT usage limit reached. Check your subscription quota and reset time in Settings."
		}
		if retry := response.Header.Get("Retry-After"); retry != "" {
			w.Header().Set("Retry-After", retry)
		}
		jsonError(w, status, message)
		return
	}
	// The subscription backend can omit Content-Type while sending valid SSE.
	// Its Responses endpoint always streams; the protocol parser still requires
	// a valid terminal response. Configure accounting before reading any bytes.
	if response.Header.Get("Content-Type") == "" {
		response.Header.Set("Content-Type", "text/event-stream")
		if observer, _ := r.Context().Value(usageContextKey{}).(*usageObserver); observer != nil {
			observer.configure(response)
		}
	}
	if !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		jsonError(w, 502, "ChatGPT returned an unsupported response format.")
		return
	}
	if err := protocol.serve(w, r, response); err != nil {
		if capture != nil {
			capture.setError(err.Error())
		}
		if !w.wrote {
			jsonError(w, 502, "ChatGPT response was interrupted or invalid.")
		} else {
			w.status = 502
		}
	}
}

func (a *app) serveModelList(w *statusWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		models, _, err := a.fetchModels(r.Context(), true)
		if err != nil {
			catalogFailure(w, err)
			return
		}
		data := make([]map[string]any, 0, len(models))
		for _, model := range models {
			data = append(data, map[string]any{"id": model.ID, "object": "model", "created": 0, "owned_by": model.Provider, "name": model.Name})
		}
		jsonResponse(w, 200, map[string]any{"object": "list", "data": data})
		return
	}
}
