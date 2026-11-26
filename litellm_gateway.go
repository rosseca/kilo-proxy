package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

const liteLLMModelPrefix = "litellm/"

// A saved model must never silently migrate to a different API base when the
// connection is edited. Key rotation at the same endpoint preserves its IDs.
func liteLLMNamespace(base *url.URL) string {
	digest := sha256.Sum256([]byte(base.String()))
	return liteLLMModelPrefix + hex.EncodeToString(digest[:16]) + "/"
}

func liteLLMAccountScope(connection liteLLMConnection) string {
	digest := sha256.Sum256([]byte(connection.Base.String() + "\x00" + connection.Key))
	return hex.EncodeToString(digest[:])
}

func liteLLMAPIURL(base *url.URL, path string) *url.URL {
	target := *base
	target.Path = strings.TrimRight(target.Path, "/") + strings.TrimPrefix(path, "/v1")
	target.RawPath = ""
	return &target
}

func (a *app) fetchLiteLLMModels(ctx context.Context, connection liteLLMConnection) ([]modelInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := a.checkLiteLLMTarget(ctx, connection.Base); err != nil {
		return nil, &catalogError{http.StatusBadRequest, err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, liteLLMAPIURL(connection.Base, "/v1/models").String(), nil)
	if err != nil {
		return nil, &catalogError{http.StatusBadRequest, "Invalid LiteLLM API address."}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Kilo-Local/"+version)
	if connection.Key != "" {
		req.Header.Set("Authorization", "Bearer "+connection.Key)
	}
	client := &http.Client{Transport: a.transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	response, err := client.Do(req)
	if err != nil {
		return nil, &catalogError{http.StatusBadGateway, "Could not reach the LiteLLM API."}
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, &catalogError{response.StatusCode, "LiteLLM rejected the virtual key or its permissions."}
	case http.StatusNotFound:
		return nil, &catalogError{response.StatusCode, "The LiteLLM model endpoint was not found. Check the API address."}
	default:
		return nil, &catalogError{http.StatusBadGateway, "LiteLLM did not return a model catalog. Check the API address and gateway status."}
	}
	const limit = 8 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || len(data) > limit {
		return nil, &catalogError{http.StatusBadGateway, "Could not read the LiteLLM model catalog."}
	}
	models, err := parseModels(data)
	if err != nil {
		return nil, &catalogError{http.StatusBadGateway, "LiteLLM returned an invalid model catalog."}
	}
	prefix := liteLLMNamespace(connection.Base)
	routable := models[:0]
	for _, model := range models {
		model.ID = prefix + model.ID
		if !catalogID.MatchString(model.ID) {
			continue
		}
		model.Connection = "litellm"
		model.Provider = "LiteLLM"
		model.InputPrice, model.OutputPrice = nil, nil
		model.MayTrain, model.HasUserByokAvailable = nil, nil
		routable = append(routable, model)
	}
	if len(models) > 0 && len(routable) == 0 {
		return nil, &catalogError{http.StatusBadGateway, "The LiteLLM catalog has no usable model aliases."}
	}
	return routable, nil
}

func prepareLiteLLMModel(r *http.Request, alias string) error {
	raw, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		return err
	}
	doc, err := decodeClaudeDesktopObject(raw)
	if err != nil {
		return errors.New("LiteLLM requests require valid JSON with unique keys.")
	}
	doc["model"], _ = json.Marshal(alias)
	data, err := json.Marshal(doc)
	if err != nil {
		return errors.New("Could not prepare the LiteLLM request.")
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	r.GetBody = nil
	r.ContentLength = int64(len(data))
	r.Header.Del("Content-Length")
	return nil
}

func (a *app) serveLiteLLM(w http.ResponseWriter, r *http.Request, connection liteLLMConnection) {
	if err := a.checkLiteLLMTarget(r.Context(), connection.Base); err != nil {
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	proxy := &httputil.ReverseProxy{
		Transport:     traceTransport{base: a.transport},
		FlushInterval: -1,
		ErrorLog:      log.New(io.Discard, "", 0),
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(connection.Base)
			p.Out.URL.Path = liteLLMAPIURL(connection.Base, p.In.URL.Path).Path
			p.Out.URL.RawPath = ""
			p.Out.Header = make(http.Header)
			for _, name := range []string{"Content-Type", "Accept", "Anthropic-Version", "Anthropic-Beta", "OpenAI-Beta"} {
				for _, value := range p.In.Header.Values(name) {
					p.Out.Header.Add(name, value)
				}
			}
			p.Out.Header.Set("User-Agent", "Kilo-Local/"+version)
			if connection.Key != "" {
				p.Out.Header.Set("Authorization", "Bearer "+connection.Key)
			}
		},
		ModifyResponse: func(response *http.Response) error {
			if response.StatusCode >= 300 && response.StatusCode < 400 {
				return errors.New("LiteLLM inference cannot be redirected")
			}
			response.Header.Del("Set-Cookie")
			response.Header.Del("Location")
			for name := range response.Header {
				if strings.HasPrefix(strings.ToLower(name), "access-control-") {
					response.Header.Del(name)
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			jsonError(w, http.StatusBadGateway, "Could not complete the request to LiteLLM. Check the configured gateway.")
		},
	}
	proxy.ServeHTTP(w, r)
}
