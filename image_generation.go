package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const imageGenerationEndpoint = "https://api.kilo.ai/api/openrouter/chat/completions"
const imageGenerationTimeout = 5 * time.Minute
const imageFileLimit = 12 << 20
const imageResponseLimit = 24 << 20
const imagePromptLimit = 16 << 10
const imageGenerationConcurrency = 2

var generatedImageName = regexp.MustCompile(`^image_[0-9a-f]{64}\.(png|jpg)$`)

type imageGenerationSettings struct {
	Enabled bool   `json:"enabled"`
	Model   string `json:"model"`
}

func validateImageGenerationSettings(s imageGenerationSettings) error {
	if s.Model == "" && !s.Enabled {
		return nil
	}
	if !catalogID.MatchString(s.Model) {
		return errors.New("Choose an exact image-output model before enabling image generation.")
	}
	return nil
}

type imageGenerationArguments struct {
	Prompt         string `json:"prompt"`
	ReferenceImage string `json:"reference_image,omitempty"`
}

type generatedImage struct {
	Path   string `json:"path"`
	MIME   string `json:"mimeType"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	data   []byte
}

type imageGenerationResult struct {
	Model      string           `json:"model"`
	Images     []generatedImage `json:"images"`
	CostUSD    *string          `json:"costUSD,omitempty"`
	CostSource string           `json:"costSource,omitempty"`
}

// Keep only the gateway's monetary scalar, preserving its decimal spelling.
// Other provider metadata can contain large or sensitive image payloads and is
// unnecessary for either accounting or Activity.
func imageProviderCostMetadata(raw json.RawMessage) map[string]any {
	var metadata, gateway map[string]json.RawMessage
	if json.Unmarshal(raw, &metadata) != nil || json.Unmarshal(metadata["gateway"], &gateway) != nil {
		return nil
	}
	value := gateway["marketCost"]
	var cost any
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	if decoder.Decode(&cost) != nil {
		return nil
	}
	if _, valid := money(cost, false); !valid {
		return nil
	}
	return map[string]any{"gateway": map[string]any{"marketCost": value}}
}

type imageGenerationActivity struct {
	owner   *app
	id      string
	epoch   uint64
	started time.Time
	capture *traceCapture
	usage   *usageObserver
	status  int
}

func (a *app) beginImageGeneration(r *http.Request, args imageGenerationArguments, key, org, localKey string) *imageGenerationActivity {
	id, epoch, capture := a.beginActivity(r, key, localKey)
	activity := &imageGenerationActivity{owner: a, id: id, epoch: epoch, started: time.Now(), capture: capture, usage: newUsageObserver(r, org), status: 502}
	if capture != nil {
		body, _ := json.Marshal(args)
		capture.request.write(body)
	}
	return activity
}

func (activity *imageGenerationActivity) finish(result *imageGenerationResult, err error) {
	a := activity.owner
	var detail *requestTrace
	if activity.capture != nil {
		if err != nil {
			activity.capture.traceError = err.Error()
			b, _ := json.Marshal(map[string]string{"error": err.Error()})
			activity.capture.response.write(b)
		} else {
			b, _ := json.Marshal(result)
			activity.capture.response.write(b)
		}
		detail = activity.capture.finish(activity.id, http.Header{"Content-Type": []string{"application/json"}})
	}
	usage := activity.usage.snapshot()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.active--
	a.requests++
	if err != nil {
		a.failures++
	}
	if activity.capture != nil {
		a.activeTraces--
	}
	a.recordUsage(usage)
	if activity.epoch != a.activityEpoch {
		return
	}
	if detail != nil && a.captureEnabled {
		a.traces[activity.id] = detail
	}
	a.events = append([]event{{ID: activity.id, At: time.Now().UTC().Format(time.RFC3339), Method: "POST", Path: "/mcp/images", Status: activity.status, Duration: time.Since(activity.started).Milliseconds(), Usage: &usage.usage, HasDetails: a.traces[activity.id] != nil}}, a.events...)
	for _, old := range a.events[min(len(a.events), traceCountLimit):] {
		delete(a.traces, old.ID)
	}
	a.events = a.events[:min(len(a.events), traceCountLimit)]
}

func validImageGenerationArguments(args imageGenerationArguments) error {
	if strings.TrimSpace(args.Prompt) == "" || len(args.Prompt) > imagePromptLimit || !utf8.ValidString(args.Prompt) || strings.ContainsRune(args.Prompt, 0) {
		return errors.New("Provide a nonempty image prompt of at most 16 KiB.")
	}
	if len(args.ReferenceImage) > 8192 || strings.ContainsAny(args.ReferenceImage, "\x00\r\n") {
		return errors.New("The reference must be a previously generated image path.")
	}
	return nil
}

func (a *app) generateImage(r *http.Request, args imageGenerationArguments, key, org, localKey string) (result *imageGenerationResult, resultErr error) {
	if err := validImageGenerationArguments(args); err != nil {
		return nil, err
	}
	a.mu.Lock()
	settings, endpoint, transport := a.config.ImageGeneration, a.imageGenerationURL, a.transport
	a.mu.Unlock()
	if !settings.Enabled {
		return nil, errors.New("Enable image generation in Kilo Proxy first.")
	}
	if err := validateImageGenerationSettings(settings); err != nil {
		return nil, err
	}
	if key == "" || org == "" {
		return nil, errors.New("Connect a Kilo account and organization before generating images.")
	}
	a.imageGenerationMu.Lock()
	if a.imageGenerationActive >= imageGenerationConcurrency {
		a.imageGenerationMu.Unlock()
		return nil, errors.New("Two images are already being generated. Wait for one to finish.")
	}
	a.imageGenerationActive++
	a.imageGenerationMu.Unlock()
	defer func() { a.imageGenerationMu.Lock(); a.imageGenerationActive--; a.imageGenerationMu.Unlock() }()
	ctx, cancel := context.WithTimeout(r.Context(), imageGenerationTimeout)
	defer cancel()
	models, revision, err := a.fetchModels(ctx, true)
	if err != nil {
		return nil, errors.New("Cannot verify the image model catalog. Check your Kilo connection and try again.")
	}
	found, acceptsReference := false, false
	for _, m := range models {
		if m.ID == settings.Model {
			for _, modality := range m.InputModalities {
				if modality == "image" {
					acceptsReference = true
				}
			}
			for _, modality := range m.OutputModalities {
				if modality == "image" {
					found = true
				}
			}
		}
	}
	if !found {
		return nil, errors.New("The selected model is not listed with image output. Choose an image model in Kilo Proxy.")
	}
	if args.ReferenceImage != "" && !acceptsReference {
		return nil, errors.New("The selected model is not listed with image input. Choose an image model that accepts reference images before editing.")
	}
	a.mu.Lock()
	current := a.config.ImageGeneration == settings && a.catalogRevision == revision && secureEqual(a.apiKey, key) && a.config.OrgID == org
	a.mu.Unlock()
	if !current {
		return nil, errors.New("The image settings or Kilo connection changed. Try again.")
	}
	root, dir, err := a.generatedImagesRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	content := []map[string]any{{"type": "text", "text": args.Prompt}}
	traceContent := []map[string]any{{"type": "text", "text": args.Prompt}}
	if args.ReferenceImage != "" {
		data, mime, err := readGeneratedReference(root, dir, args.ReferenceImage)
		if err != nil {
			return nil, err
		}
		content = append(content, map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)}})
		traceContent = append(traceContent, map[string]any{"type": "image_url", "image_url": map[string]string{"url": "[image payload omitted]"}, "reference_image": args.ReferenceImage})
	}
	bodyFor := func(parts []map[string]any) []byte {
		data, _ := json.Marshal(map[string]any{"model": settings.Model, "modalities": []string{"image", "text"}, "stream": false, "messages": []any{map[string]any{"role": "user", "content": parts}}})
		return data
	}
	if endpoint == "" {
		endpoint = imageGenerationEndpoint
	}
	request, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyFor(content)))
	if err != nil {
		return nil, errors.New("The image generation endpoint is unavailable.")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("X-KiloCode-OrganizationId", org)
	request.Header.Set("User-Agent", "Kilo-Proxy/"+version)
	activity := a.beginImageGeneration(r, args, key, org, localKey)
	defer func() { activity.finish(result, resultErr) }()
	if activity.capture != nil {
		activity.capture.upRequestHeaders = request.Header.Clone()
		activity.capture.upRequestHeaders.Set("Host", request.URL.Host)
		activity.capture.upRequest.write(bodyFor(traceContent))
	}
	// Image generation is nonstreaming and may need the full image timeout
	// before sending response headers. Keep the normal proxy's shorter header
	// budget and connection pool untouched; custom transports retain their own
	// behavior (including synthetic transports used by tests).
	if shared, ok := transport.(*http.Transport); ok {
		isolated := shared.Clone()
		isolated.ResponseHeaderTimeout = imageGenerationTimeout
		transport = isolated
		defer isolated.CloseIdleConnections()
	}
	client := &http.Client{Transport: transport, Timeout: imageGenerationTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("Image generation could not reach Kilo or timed out. Check Activity before retrying to avoid duplicate charges.")
	}
	defer response.Body.Close()
	activity.status = response.StatusCode
	if activity.capture != nil {
		activity.capture.upstreamStatus = response.StatusCode
		activity.capture.upResponseHeaders = response.Header.Clone()
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, imageResponseLimit+1))
	if err != nil || len(raw) > imageResponseLimit {
		return nil, errors.New("Kilo returned an incomplete or oversized image response.")
	}
	var output struct {
		Model            string          `json:"model"`
		Usage            json.RawMessage `json:"usage"`
		ProviderMetadata json.RawMessage `json:"provider_metadata"`
		Choices          []struct {
			Message struct {
				Images []struct {
					Type     string `json:"type"`
					ImageURL struct {
						URL string `json:"url"`
					} `json:"image_url"`
				} `json:"images"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(raw, &output) != nil {
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("Kilo rejected image generation (HTTP %d). Check the image model and your organization's access.", response.StatusCode)
		}
		activity.status = 502
		return nil, errors.New("Kilo returned an invalid image response.")
	}
	// Report the validated configured route, not arbitrary upstream strings.
	output.Model = settings.Model
	// Extract usage and the gateway's inference price before decoding images.
	// The normal stream observer intentionally drops large frames, so feeding it
	// base64 or unrelated provider metadata loses cost.
	observed := map[string]any{"model": output.Model, "usage": output.Usage}
	if metadata := imageProviderCostMetadata(output.ProviderMetadata); metadata != nil {
		observed["provider_metadata"] = metadata
	}
	usageData, _ := json.Marshal(observed)
	activity.usage.feed(usageData)
	activity.usage.eof()
	if activity.capture != nil {
		observed["image_payloads"] = "omitted from activity details"
		summary, _ := json.Marshal(observed)
		activity.capture.upResponse.write(summary)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		activity.status = response.StatusCode
		return nil, fmt.Errorf("Kilo rejected image generation (HTTP %d). Check the image model and your organization's access.", response.StatusCode)
	}
	activity.status = 502
	images := []generatedImage{}
	for _, choice := range output.Choices {
		for _, item := range choice.Message.Images {
			if len(images) >= 4 {
				return nil, errors.New("Kilo returned too many images; no files were saved.")
			}
			data, mime, width, height, err := decodeGeneratedDataURL(item.ImageURL.URL)
			if err != nil {
				return nil, err
			}
			images = append(images, generatedImage{MIME: mime, Width: width, Height: height, data: data})
		}
	}
	if len(images) == 0 {
		return nil, errors.New("Kilo returned no image. The chosen model or organization may not support image generation.")
	}
	saved := []string{}
	defer func() {
		if resultErr != nil {
			for _, name := range saved {
				_ = root.Remove(name)
			}
		}
	}()
	for i := range images {
		ext := ".png"
		if images[i].MIME == "image/jpeg" {
			ext = ".jpg"
		}
		name := randomKey("image_") + ext
		file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, errors.New("Cannot save the generated image.")
		}
		saved = append(saved, name)
		_, writeErr := file.Write(images[i].data)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			return nil, errors.New("Cannot save the complete generated image.")
		}
		images[i].Path = filepath.Join(dir, name)
	}
	activity.status = 200
	usage := activity.usage.snapshot().usage
	return &imageGenerationResult{Model: output.Model, Images: images, CostUSD: usage.CostUSD, CostSource: usage.CostSource}, nil
}

func (a *app) generatedImagesRoot() (*os.Root, string, error) {
	dir, err := filepath.Abs(a.dir)
	if err != nil {
		return nil, "", errors.New("Cannot locate the generated image directory.")
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, "", errors.New("Cannot create the generated image directory.")
	}
	base, err := os.OpenRoot(dir)
	if err != nil {
		return nil, "", errors.New("Cannot open the image storage directory.")
	}
	defer base.Close()
	if err = base.Mkdir("generated-images", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, "", errors.New("Cannot create the generated image directory.")
	}
	info, err := base.Lstat("generated-images")
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, "", errors.New("The generated image directory must be a real directory, not a symbolic link.")
	}
	root, err := base.OpenRoot("generated-images")
	if err != nil {
		return nil, "", errors.New("Cannot safely open the generated image directory.")
	}
	return root, filepath.Join(dir, "generated-images"), nil
}

func readGeneratedReference(root *os.Root, dir, path string) ([]byte, string, error) {
	invalid := errors.New("The reference must be a previously generated PNG or JPEG in Kilo Proxy's generated-images directory.")
	name := filepath.Base(path)
	if !filepath.IsAbs(path) || filepath.Clean(path) != filepath.Join(dir, name) || !generatedImageName.MatchString(name) {
		return nil, "", invalid
	}
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > imageFileLimit {
		return nil, "", invalid
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, "", invalid
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, "", invalid
	}
	data, err := io.ReadAll(io.LimitReader(file, imageFileLimit+1))
	if err != nil || len(data) > imageFileLimit {
		return nil, "", invalid
	}
	mime, _, _, err := validateGeneratedImage(data)
	if err != nil {
		return nil, "", invalid
	}
	return data, mime, nil
}

func decodeGeneratedDataURL(value string) ([]byte, string, int, int, error) {
	invalid := errors.New("Kilo returned an invalid or oversized image. Only embedded PNG and JPEG results are supported.")
	mime, encoded, found := strings.Cut(value, ";base64,")
	if !found || (mime != "data:image/png" && mime != "data:image/jpeg") || len(encoded) > base64.StdEncoding.EncodedLen(imageFileLimit) {
		return nil, "", 0, 0, invalid
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) > imageFileLimit {
		return nil, "", 0, 0, invalid
	}
	actual, width, height, err := validateGeneratedImage(data)
	if err != nil || "data:"+actual != mime {
		return nil, "", 0, 0, invalid
	}
	return data, actual, width, height, nil
}

func validateGeneratedImage(data []byte) (string, int, int, error) {
	invalid := errors.New("Invalid image data")
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") || config.Width < 1 || config.Height < 1 || config.Width > 8192 || config.Height > 8192 || int64(config.Width)*int64(config.Height) > 16<<20 {
		return "", 0, 0, invalid
	}
	// Validate the compressed body too, within the checked pixel allocation bound.
	if _, decoded, err := image.Decode(bytes.NewReader(data)); err != nil || decoded != format {
		return "", 0, 0, invalid
	}
	mime := "image/png"
	if format == "jpeg" {
		mime = "image/jpeg"
	}
	return mime, config.Width, config.Height, nil
}
