package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/webp"
)

// Leave room below the upstream's 4.5 MB body limit. This is a transport
// budget, not a model's image/context limit. Original images are never resized.
const imageUploadRequestBudget = 4_400_000
const imageUploadURLBudget = 4096
const imageUploadConcurrency = 2
const imageUploadRequestTimeout = 10 * time.Minute
const imageUploadCleanupTimeout = 30 * time.Second

type imageUploadRequestError struct {
	status  int
	message string
}

func (e *imageUploadRequestError) Error() string { return e.message }

func imageUploadProblem(status int, message string) error {
	return &imageUploadRequestError{status: status, message: message}
}

type responseImageCandidate struct {
	data  []byte
	mime  string
	parts []inferenceImagePart
	saved int
}

type responseImageUploadPlan struct {
	doc    map[string]any
	images []*responseImageCandidate
}

// Only documented Responses image positions are visited. Tool argument JSON,
// arbitrary strings, local paths, remote URLs and other protocols are untouched.
func responseImageParts(doc map[string]any) []map[string]any {
	var parts []map[string]any
	input, _ := doc["input"].([]any)
	for _, value := range input {
		item := object(value)
		var content []any
		switch stringValue(item["type"]) {
		case "function_call_output":
			content, _ = item["output"].([]any)
		case "", "message":
			switch stringValue(item["role"]) {
			case "user", "assistant", "system", "developer":
				content, _ = item["content"].([]any)
			}
		}
		for _, value := range content {
			part := object(value)
			if stringValue(part["type"]) == "input_image" && strings.HasPrefix(stringValue(part["image_url"]), "data:") {
				parts = append(parts, part)
			}
		}
	}
	return parts
}

func decodeResponseImage(value string) ([]byte, string, error) {
	header, encoded, ok := strings.Cut(value, ",")
	if !ok || !strings.HasSuffix(header, ";base64") {
		return nil, "", imageUploadProblem(400, "Temporary image URLs require base64 PNG, JPEG, GIF or WebP images.")
	}
	mime := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64"))
	if base64.StdEncoding.DecodedLen(len(encoded)) > imageAttachmentMaxBytes+2 {
		return nil, "", imageUploadProblem(413, "An image exceeds 20 MiB image transport limit. Reduce attachments or start a new conversation; no image was resized.")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 {
		return nil, "", imageUploadProblem(400, "Could not decode an inline image for temporary URL transport.")
	}
	if err := validateImageTransportData(data, mime); err != nil {
		return nil, "", err
	}
	return data, mime, nil
}

// URL backends use the same bounded validation as the inline request adapter.
// Only the image header is decoded; original pixels and animation are untouched.
func validateImageTransportData(data []byte, mime string) error {
	formats := map[string]string{"image/png": "png", "image/jpeg": "jpeg", "image/gif": "gif", "image/webp": "webp"}
	format, ok := formats[mime]
	if !ok {
		return imageUploadProblem(400, "Temporary image URLs support PNG, JPEG, GIF and WebP images only.")
	}
	if len(data) > imageAttachmentMaxBytes {
		return imageUploadProblem(413, "An image exceeds the 20 MiB image transport limit. No image was resized.")
	}
	cfg, actual, err := image.DecodeConfig(bytes.NewReader(data))
	if format == "webp" && responseImageAnimated(data, format) {
		canvas, valid := animatedWebPCanvas(data)
		if !valid {
			return imageUploadProblem(400, "The animated WebP image has an invalid or truncated container. No files were uploaded.")
		}
		cfg, actual, err = canvas, format, nil
	}
	if err != nil || actual != format || cfg.Width <= 0 || cfg.Height <= 0 {
		return imageUploadProblem(400, "An inline image does not match its declared format. No files were uploaded.")
	}
	return nil
}

func planResponseImageUploads(data []byte) (*responseImageUploadPlan, error) {
	return planInferenceImageUploads(data, "/v1/responses", imageAttachmentMaxCount)
}

func planInferenceImageUploads(data []byte, path string, maxImages int) (*responseImageUploadPlan, error) {
	if len(data) <= imageUploadRequestBudget {
		return nil, nil
	}
	doc, err := decodeObject(data)
	if err != nil {
		return nil, nil // Leave invalid JSON to the existing gateway validation.
	}
	if background, _ := doc["background"].(bool); path == "/v1/responses" && background {
		return nil, imageUploadProblem(400, "Temporary image URLs require a foreground request so images remain available until inference finishes. Disable background mode or choose local compression. No image was published.")
	}
	groups := make(map[[32]byte]*responseImageCandidate)
	var candidates []*responseImageCandidate
	for _, part := range inferenceImageParts(doc, path) {
		value := part.inline()
		raw, mime, err := decodeResponseImage(value)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(raw)
		candidate := groups[digest]
		if candidate == nil {
			candidate = &responseImageCandidate{data: raw, mime: mime}
			groups[digest] = candidate
			candidates = append(candidates, candidate)
		}
		candidate.parts = append(candidate.parts, part)
		candidate.saved += len(value) - imageUploadURLBudget
	}
	// Fewer uploads mean less remote data and latency. Duplicate occurrences
	// share one object, including copies retained in earlier tool results.
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].saved > candidates[j].saved })
	plan := &responseImageUploadPlan{doc: doc}
	placeholder := strings.Repeat("x", imageUploadURLBudget)
	for _, candidate := range candidates {
		if len(plan.images) == maxImages || candidate.saved <= 0 {
			break
		}
		for _, part := range candidate.parts {
			part.setURL(placeholder)
		}
		plan.images = append(plan.images, candidate)
		encoded, err := json.Marshal(doc)
		if err != nil {
			return nil, err
		}
		if len(encoded) <= imageUploadRequestBudget {
			return plan, nil
		}
	}
	return nil, imageUploadProblem(413, fmt.Sprintf("This request still exceeds Kilo's 4.5 MB limit with temporary image URLs (up to %d unique images with this backend). Text and other attachments still count. Compact the conversation or reduce attachments. No files were uploaded or resized.", maxImages))
}

func (a *app) prepareResponseImageUploads(r *http.Request, key, org string) (*http.Request, func(), error) {
	a.mu.Lock()
	setting := normalizeImageTransportSettings(a.config.ImageTransport)
	a.mu.Unlock()
	if setting.Mode == "off" || r.Method != http.MethodPost || !imageTransportPath(r.URL.Path) || r.Header.Get("Content-Encoding") != "" || r.Body == nil {
		return r, nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, bridgeLimit+1))
	if err != nil {
		return r, nil, err
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(data))
	if len(data) > bridgeLimit {
		return r, nil, &http.MaxBytesError{Limit: bridgeLimit}
	}
	if len(data) <= imageUploadRequestBudget {
		return r, nil, nil
	}
	a.mu.Lock()
	select {
	case <-a.quit:
		a.mu.Unlock()
		return r, nil, imageUploadProblem(503, "Kilo Proxy is closing. No image was uploaded.")
	default:
	}
	if a.imageUploadsActive >= imageUploadConcurrency {
		a.mu.Unlock()
		return r, nil, imageUploadProblem(503, "Two large image requests are already active. Wait for one to finish before retrying.")
	}
	if a.imageUploadsActive == 0 {
		a.imageUploadsDone = make(chan struct{})
	}
	a.imageUploadsActive++
	factory := a.attachmentClientFactory
	a.mu.Unlock()
	releaseSlot := func() {
		a.mu.Lock()
		a.imageUploadsActive--
		if a.imageUploadsActive == 0 {
			close(a.imageUploadsDone)
		}
		a.mu.Unlock()
	}
	if setting.Mode == "compress" {
		prepared, err := prepareCompressedResponseImages(r, setting.Profile)
		releaseSlot()
		return prepared, nil, err
	}
	maxImages := 64
	if setting.Mode == "upload" {
		maxImages = imageAttachmentMaxCount
	}
	plan, err := planInferenceImageUploads(data, r.URL.Path, maxImages)
	if err != nil || plan == nil {
		releaseSlot()
		return r, nil, err
	}
	capture := imageTraceCapture(r.Context())
	if setting.Mode == "cloudflare" || setting.Mode == "tailscale" {
		capture.beginImageTransport(setting.Mode)
	}
	ctx, cancel := context.WithTimeout(r.Context(), imageUploadRequestTimeout)
	var lease imageURLLease
	if setting.Mode == "upload" {
		if factory == nil {
			factory = newImageAttachmentClient
		}
		lease, err = factory(key, org).NewLease()
		if err != nil {
			err = errors.New("Could not prepare temporary Kilo image uploads.")
		}
	} else if a.imageURLLeaseFactory != nil {
		lease, err = a.imageURLLeaseFactory(ctx, setting.Mode, setting.LitterboxTTL)
	} else {
		lease, err = a.imageURLBackends.NewLease(ctx, setting.Mode, setting.LitterboxTTL)
	}
	if err != nil {
		cancel()
		releaseSlot()
		return r, nil, err
	}
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			reason := "request_finished"
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				reason = "request_deadline"
			} else if ctx.Err() != nil {
				reason = "request_canceled"
			}
			capture.updateImageTransport(func(d *imageTransportTrace) {
				d.CleanupStartedAt = time.Now().UnixMilli()
				d.CleanupReason = reason
				if reason == "request_finished" && d.GatewayStartedAt == 0 {
					d.CleanupReason = "preparation_failed"
				}
			})
			cancel()
			defer releaseSlot()
			// Client cancellation must not cancel deletion. No image, signed URL
			// or storage identifier is persisted to the user's request history.
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), imageUploadCleanupTimeout)
			defer cleanupCancel()
			cleanupErr := lease.Close(cleanupCtx)
			capture.updateImageTransport(func(d *imageTransportTrace) {
				d.CleanupFinishedAt = time.Now().UnixMilli()
				d.CleanupFailed = cleanupErr != nil
			})
			if cleanupErr != nil {
				a.mu.Lock()
				if setting.Mode == "upload" {
					a.imageUploadWarning = "Kilo could not confirm deletion of temporary images. Files may remain in your Kilo account until its pending-upload cleanup runs. Closing the app does not guarantee deletion."
				} else {
					a.imageUploadWarning = "Could not finish temporary image cleanup. Check the selected image backend before retrying."
				}
				a.mu.Unlock()
			}
		})
	}
	for _, candidate := range plan.images {
		url, err := lease.Upload(ctx, candidate.data, candidate.mime)
		if err != nil {
			// Only explicitly sanitized application errors may cross this boundary;
			// raw transport/process errors can contain bearer URLs or private data.
			var problem *imageUploadRequestError
			if errors.As(err, &problem) {
				return r, cleanup, problem
			}
			return r, cleanup, errors.New("The selected image backend could not publish an image. No inference was sent, no fallback was attempted, and no image was resized. Check the backend requirements in Settings.")
		}
		if len(url) > imageUploadURLBudget {
			return r, cleanup, errors.New("The image backend returned an unsupported temporary image URL. No inference was sent.")
		}
		// Register before the actual upstream body can be recorded. Include
		// JSON escaping, since signatures contain ampersands escaped by Go.
		if capture, _ := r.Context().Value(traceContextKey{}).(*traceCapture); capture != nil {
			capture.addSecrets(url)
			capture.omitImageUploadResponses()
		}
		for _, part := range candidate.parts {
			part.setURL(url)
		}
	}
	encoded, err := json.Marshal(plan.doc)
	if err != nil || len(encoded) > imageUploadRequestBudget {
		return r, cleanup, imageUploadProblem(413, "This request still exceeds Kilo's request-body limit after temporary image uploads. No inference was sent or image resized.")
	}
	if expiry := lease.ExpiresAt(); !expiry.IsZero() {
		deadline := expiry.Add(-30 * time.Second)
		if !deadline.After(time.Now()) {
			return r, cleanup, errors.New("The temporary image URLs expired before inference could start. No inference was sent.")
		}
		next, expiryCancel := context.WithDeadline(ctx, deadline)
		previousCancel := cancel
		cancel = func() { expiryCancel(); previousCancel() }
		ctx = next
	}
	prepared := r.Clone(ctx)
	prepared.Body = io.NopCloser(bytes.NewReader(encoded))
	prepared.ContentLength = int64(len(encoded))
	prepared.GetBody = nil
	prepared.TransferEncoding = nil
	prepared.Header.Del("Content-Length")
	prepared.Header.Del("Transfer-Encoding")
	return prepared, cleanup, nil
}

// Called after requestQuit and closing the proxy listener. Registration checks
// quit under the same mutex, so no new attachment lease can race this snapshot.
func (a *app) drainImageUploads(ctx context.Context) {
	a.mu.Lock()
	done, active := a.imageUploadsDone, a.imageUploadsActive
	a.mu.Unlock()
	if active == 0 {
		return
	}
	select {
	case <-done:
	case <-ctx.Done():
	}
}
