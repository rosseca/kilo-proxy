package main

import (
	"context"
	"net/http"
	"time"
)

// Metadata only: never retain URLs, paths, headers, addresses, image data, or
// raw errors. Image IDs are request-local ordinals, not public access tokens.
type imageTransportTrace struct {
	TunnelStartupStartedAt int64                `json:"tunnelStartupStartedAt,omitempty"`
	TunnelReadyAt          int64                `json:"tunnelReadyAt,omitempty"`
	Backend                string               `json:"backend"`
	TunnelReused           bool                 `json:"tunnelReused"`
	Probe                  imageTransportProbe  `json:"startupProbe"`
	GatewayStartedAt       int64                `json:"gatewayStartedAt,omitempty"`
	GatewayFinishedAt      int64                `json:"gatewayFinishedAt,omitempty"`
	CleanupStartedAt       int64                `json:"cleanupStartedAt,omitempty"`
	CleanupFinishedAt      int64                `json:"cleanupFinishedAt,omitempty"`
	CleanupReason          string               `json:"cleanupReason,omitempty"`
	CleanupFailed          bool                 `json:"cleanupFailed"`
	Images                 []imageDeliveryTrace `json:"images"`
}

type imageTransportProbe struct {
	Attempts       int   `json:"attempts"`
	LastHTTPStatus int   `json:"lastHTTPStatus,omitempty"`
	LastFailed     bool  `json:"lastFailed"`
	VerifiedAt     int64 `json:"verifiedAt,omitempty"`
}

type imageDeliveryTrace struct {
	ID             int    `json:"id"`
	Bytes          int    `json:"bytes"`
	RegisteredAt   int64  `json:"registeredAt"`
	RemovedAt      int64  `json:"removedAt,omitempty"`
	RemovalReason  string `json:"removalReason,omitempty"`
	Requests       int64  `json:"requests"`
	GETs           int64  `json:"gets"`
	HEADs          int64  `json:"heads"`
	HTTP200        int64  `json:"http200"`
	HTTP404        int64  `json:"http404"`
	HTTP405        int64  `json:"http405"`
	HTTP503        int64  `json:"http503"`
	LastRequestAt  int64  `json:"lastRequestAt,omitempty"`
	LastDurationMS int64  `json:"lastDurationMs"`
	WrittenBytes   int64  `json:"writtenBytes"`
	WriteFailures  int64  `json:"writeFailures"`
}

type imageDeliveryObservation struct {
	capture *traceCapture
	index   int
}

func imageTraceCapture(ctx context.Context) *traceCapture {
	c, _ := ctx.Value(traceContextKey{}).(*traceCapture)
	return c
}

func (c *traceCapture) updateImageTransport(update func(*imageTransportTrace)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.disabled && c.imageTransport != nil {
		update(c.imageTransport)
	}
}

func (c *traceCapture) beginImageTransport(backend string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.disabled {
		c.imageTransport = &imageTransportTrace{Backend: backend, Images: []imageDeliveryTrace{}}
	}
}

func (c *traceCapture) imageProbe(status int, failed, verified bool) {
	c.updateImageTransport(func(d *imageTransportTrace) {
		d.Probe.Attempts++
		d.Probe.LastHTTPStatus, d.Probe.LastFailed = status, failed
		if verified {
			d.Probe.VerifiedAt = time.Now().UnixMilli()
		}
	})
}

func (c *traceCapture) registerImage(bytes int) *imageDeliveryObservation {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Matches the transport's per-lease limit. Accesses update fixed counters,
	// not an unbounded list of events or caller-controlled status/method keys.
	if c.disabled || c.imageTransport == nil || len(c.imageTransport.Images) >= 64 {
		return nil
	}
	index := len(c.imageTransport.Images)
	c.imageTransport.Images = append(c.imageTransport.Images, imageDeliveryTrace{ID: index + 1, Bytes: bytes, RegisteredAt: time.Now().UnixMilli()})
	return &imageDeliveryObservation{capture: c, index: index}
}

func (o *imageDeliveryObservation) update(update func(*imageDeliveryTrace)) {
	if o == nil {
		return
	}
	o.capture.updateImageTransport(func(d *imageTransportTrace) {
		if o.index < len(d.Images) {
			update(&d.Images[o.index])
		}
	})
}

func (o *imageDeliveryObservation) removed(reason string) {
	o.update(func(d *imageDeliveryTrace) { d.RemovedAt = time.Now().UnixMilli(); d.RemovalReason = reason })
}

func (o *imageDeliveryObservation) served(method string, status, bytes int, failed bool, started time.Time) {
	o.update(func(d *imageDeliveryTrace) {
		d.Requests++
		switch method {
		case http.MethodGet:
			d.GETs++
		case http.MethodHead:
			d.HEADs++
		}
		switch status {
		case 200:
			d.HTTP200++
		case 404:
			d.HTTP404++
		case 405:
			d.HTTP405++
		case 503:
			d.HTTP503++
		}
		d.LastRequestAt = time.Now().UnixMilli()
		d.LastDurationMS = time.Since(started).Milliseconds()
		d.WrittenBytes += int64(bytes)
		if failed {
			d.WriteFailures++
		}
	})
}

// Caller holds c.mu. Deep-copy the only slice so completed snapshots cannot
// race late accesses that were already in progress during image removal.
func (c *traceCapture) imageTransportSnapshot() *imageTransportTrace {
	if c.imageTransport == nil {
		return nil
	}
	d := *c.imageTransport
	d.Images = append([]imageDeliveryTrace{}, d.Images...)
	return &d
}
