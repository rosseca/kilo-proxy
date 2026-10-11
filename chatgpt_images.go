package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// Image generation is a built-in Responses tool, not an image-output model in
// the subscription's conversational catalog. Prefer a verified orchestrator,
// then let another available GPT model try the tool without crossing providers.
func chatGPTImageOrchestrator(models []modelInfo) string {
	for _, preferred := range []string{"chatgpt/gpt-5.5", "chatgpt/gpt-5.4"} {
		for _, model := range models {
			if model.ID == preferred {
				return model.ID
			}
		}
	}
	for _, model := range models {
		if strings.HasPrefix(model.ID, "chatgpt/gpt-") && !strings.Contains(model.ID, "codex") {
			return model.ID
		}
	}
	return ""
}

func (a *app) generateChatGPTImage(r *http.Request, args imageGenerationArguments, settings imageGenerationSettings, localKey string) (result *imageGenerationResult, resultErr error) {
	a.mu.Lock()
	connection, endpoint, transport := a.chatgpt, a.chatGPTResponsesURL, a.transport
	ready := a.chatGPTReadyLocked()
	a.mu.Unlock()
	if !ready {
		return nil, errors.New("Connect ChatGPT before generating images with your subscription.")
	}
	identity := connection.identity()
	models, err := connection.models(r.Context())
	if err != nil {
		return nil, errors.New("Cannot verify the ChatGPT model catalog. Check the subscription connection and try again.")
	}
	model := chatGPTImageOrchestrator(models)
	if model == "" {
		return nil, errors.New("No compatible GPT model is available to run ChatGPT's image tool.")
	}
	root, dir, err := a.generatedImagesRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	content := []map[string]any{{"type": "input_text", "text": args.Prompt}}
	traceContent := []map[string]any{{"type": "input_text", "text": args.Prompt}}
	if args.ReferenceImage != "" {
		data, mime, err := readGeneratedReference(root, dir, args.ReferenceImage)
		if err != nil {
			return nil, err
		}
		content = append(content, map[string]any{"type": "input_image", "image_url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)})
		traceContent = append(traceContent, map[string]any{"type": "input_image", "image_url": "[image payload omitted]", "reference_image": args.ReferenceImage})
	}
	bodyFor := func(parts []map[string]any) []byte {
		data, _ := json.Marshal(map[string]any{
			"model": strings.TrimPrefix(model, "chatgpt/"), "stream": true, "store": false,
			"instructions": "Use the image_generation tool to create or edit the requested image. Return one image.",
			"input":        []any{map[string]any{"role": "user", "content": parts}},
			"tools":        []any{map[string]any{"type": "image_generation", "output_format": "png"}},
			"tool_choice":  map[string]string{"type": "image_generation"},
		})
		return data
	}
	token, account, err := connection.credentials(r.Context())
	if err != nil {
		return nil, errors.New("Reconnect ChatGPT before generating images with your subscription.")
	}
	a.mu.Lock()
	current := a.chatgpt == connection && a.config.ImageGeneration == settings && a.chatGPTReadyLocked() && identity == account
	a.mu.Unlock()
	if !current {
		return nil, errors.New("The image settings or ChatGPT connection changed. Try again.")
	}
	if endpoint == "" {
		endpoint = chatGPTResponsesEndpoint
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, bytes.NewReader(bodyFor(content)))
	if err != nil {
		return nil, errors.New("The ChatGPT image generation endpoint is unavailable.")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("ChatGPT-Account-Id", account)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("OpenAI-Beta", "responses=experimental")
	request.Header.Set("Originator", "kilo-proxy")
	request.Header.Set("User-Agent", "Kilo-Proxy/"+version)
	activity := a.beginImageGeneration(r, args, token, "chatgpt:"+account, localKey)
	activity.usage.historyAccount = usageAccountID(account, "chatgpt")
	activity.usage.org = "chatgpt"
	activity.usage.usage.Billing = "subscription"
	activity.usage.usage.Model = model
	request.Header.Set("Session_id", activity.usage.usage.Session)
	defer func() { activity.finish(result, resultErr) }()
	if activity.capture != nil {
		activity.capture.addSecrets(token, account)
		activity.capture.upstreamRequest(request, false)
		activity.capture.upRequest.write(bodyFor(traceContent))
	}
	if shared, ok := transport.(*http.Transport); ok {
		isolated := shared.Clone()
		isolated.ResponseHeaderTimeout = imageGenerationTimeout
		transport = isolated
		defer isolated.CloseIdleConnections()
	}
	client := &http.Client{Transport: transport, Timeout: imageGenerationTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("ChatGPT image generation could not connect or timed out. Check Activity before retrying; the request may have consumed subscription quota.")
	}
	defer response.Body.Close()
	activity.status = response.StatusCode
	if activity.capture != nil {
		activity.capture.upstreamResponse(response, false)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ChatGPT rejected image generation (HTTP %d). Check your subscription access and quota. Kilo was not used.", response.StatusCode)
	}
	activity.status = http.StatusBadGateway
	if contentType := response.Header.Get("Content-Type"); contentType != "" && !strings.HasPrefix(strings.ToLower(contentType), "text/event-stream") {
		return nil, errors.New("ChatGPT returned an unsupported image response format.")
	}
	images, usage, err := readChatGPTImages(response.Body)
	// Account for completed calls even when the image is invalid or absent.
	if len(usage) != 0 {
		observed, _ := json.Marshal(map[string]any{"model": model, "usage": usage})
		activity.usage.feed(observed)
		activity.usage.eof()
		if activity.capture != nil {
			activity.capture.upResponse.write(observed)
		}
	}
	if err != nil {
		return nil, err
	}
	if err := r.Context().Err(); err != nil {
		return nil, errors.New("ChatGPT image generation was cancelled; no files were saved.")
	}
	if err := saveGeneratedImages(root, dir, images); err != nil {
		return nil, err
	}
	activity.status = http.StatusOK
	return &imageGenerationResult{Provider: "chatgpt", Billing: "subscription", Model: model, Images: images}, nil
}

type chatGPTImageOutput struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Result string `json:"result"`
}

// Store only final image items, never partial previews. The backend may send
// output_item.done followed by an empty terminal output; both forms are valid.
// Quota comes from the terminal usage, separate from large base64 event bodies.
func readChatGPTImages(reader io.Reader) ([]generatedImage, json.RawMessage, error) {
	items := map[int]chatGPTImageOutput{}
	var usage json.RawMessage
	var invalid error
	complete := errors.New("image response complete")
	limit := &io.LimitedReader{R: reader, N: 4*imageResponseLimit + 1}
	err := cgReadSSE(limit, func(raw []byte) error {
		var event struct {
			Type     string             `json:"type"`
			Index    int                `json:"output_index"`
			Item     chatGPTImageOutput `json:"item"`
			Response struct {
				Status string               `json:"status"`
				Usage  json.RawMessage      `json:"usage"`
				Output []chatGPTImageOutput `json:"output"`
			} `json:"response"`
		}
		if len(raw) > imageResponseLimit || json.Unmarshal(raw, &event) != nil {
			return errors.New("ChatGPT returned an invalid or oversized image event.")
		}
		switch event.Type {
		case "response.output_item.done":
			if event.Item.Type == "image_generation_call" {
				_, duplicate := items[event.Index]
				if event.Index < 0 || event.Index > 1024 || (!duplicate && len(items) >= 4) {
					invalid = errors.New("ChatGPT returned too many image items; no files were saved.")
				} else {
					items[event.Index] = event.Item
				}
			}
		case "error", "response.failed", "response.cancelled":
			usage = event.Response.Usage
			return errors.New("ChatGPT image generation failed. Check your subscription access and quota. Kilo was not used.")
		case "response.completed", "response.done", "response.incomplete":
			usage = event.Response.Usage
			if event.Type == "response.incomplete" || event.Response.Status != "completed" {
				return errors.New("ChatGPT image generation did not complete; no files were saved.")
			}
			if len(event.Response.Output) > 0 {
				items = map[int]chatGPTImageOutput{}
				for index, item := range event.Response.Output {
					if item.Type == "image_generation_call" {
						items[index] = item
					}
				}
			}
			return complete
		}
		// Bound retained result strings across all items, even before decoding.
		size := 0
		for _, item := range items {
			size += len(item.Result)
		}
		if size > imageResponseLimit {
			return errors.New("ChatGPT image results exceed the local size limit.")
		}
		return nil
	})
	if limit.N <= 0 {
		return nil, usage, errors.New("ChatGPT image stream exceeds the local size limit.")
	}
	if err != complete {
		// Never return untrusted upstream error messages or base64 data.
		return nil, usage, errors.New("ChatGPT image response was interrupted or invalid. Check your subscription access and quota before retrying. Kilo was not used.")
	}
	if invalid != nil {
		return nil, usage, invalid
	}
	if len(items) == 0 {
		return nil, usage, errors.New("ChatGPT returned no image. The subscription or selected GPT model may not support the image tool.")
	}
	if len(items) > 4 {
		return nil, usage, errors.New("ChatGPT returned too many images; no files were saved.")
	}
	indexes := make([]int, 0, len(items))
	for index := range items {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	var images []generatedImage
	for _, index := range indexes {
		item := items[index]
		if item.Status != "completed" || item.Result == "" || len(item.Result) > base64.StdEncoding.EncodedLen(imageFileLimit) {
			return nil, usage, errors.New("ChatGPT returned an incomplete or oversized image; no files were saved.")
		}
		data, err := base64.StdEncoding.DecodeString(item.Result)
		if err != nil {
			return nil, usage, errors.New("ChatGPT returned invalid image data.")
		}
		mime, width, height, err := validateGeneratedImage(data)
		if err != nil {
			return nil, usage, err
		}
		images = append(images, generatedImage{MIME: mime, Width: width, Height: height, data: data})
	}
	return images, usage, nil
}
