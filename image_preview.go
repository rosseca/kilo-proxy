package main

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"math"

	"golang.org/x/image/draw"
)

const imagePreviewByteLimit = 256 << 10
const imagePreviewSideLimit = 1024
const imagePreviewPixelLimit = 16 << 20

type imagePreviewMetadata struct {
	MIME    string `json:"mimeType,omitempty"`
	Width   int    `json:"width,omitempty"`
	Height  int    `json:"height,omitempty"`
	Bytes   int    `json:"bytes,omitempty"`
	Resized bool   `json:"resized"`
	Omitted bool   `json:"omitted,omitempty"`
}

type imagePreview struct {
	imagePreviewMetadata
	data []byte
}

// Inline MCP images become part of the next model request. Bound those bytes
// independently of the original file, which remains full resolution for edits.
func generatedImagePreview(original generatedImage) (imagePreview, error) {
	invalid := errors.New("The original image was saved, but a bounded preview could not be created")
	if len(original.data) == 0 || len(original.data) > imageFileLimit {
		return imagePreview{}, invalid
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(original.data))
	if err != nil || (format != "png" && format != "jpeg") || config.Width < 1 || config.Height < 1 || config.Width > 8192 || config.Height > 8192 || int64(config.Width)*int64(config.Height) > imagePreviewPixelLimit {
		return imagePreview{}, invalid
	}
	mime := "image/" + format
	if original.MIME != mime || original.Width != config.Width || original.Height != config.Height {
		return imagePreview{}, invalid
	}
	if len(original.data) <= imagePreviewByteLimit && config.Width <= imagePreviewSideLimit && config.Height <= imagePreviewSideLimit {
		// generateImage already validates the complete compressed body. Preserve
		// compliant PNG/JPEG bytes exactly, including transparency and metadata.
		return imagePreview{imagePreviewMetadata: imagePreviewMetadata{MIME: mime, Width: config.Width, Height: config.Height, Bytes: len(original.data)}, data: original.data}, nil
	}
	decoded, decodedFormat, err := image.Decode(bytes.NewReader(original.data))
	if err != nil || decodedFormat != format {
		return imagePreview{}, invalid
	}
	side := min(imagePreviewSideLimit, max(config.Width, config.Height))
	for {
		width := max(1, config.Width*side/max(config.Width, config.Height))
		height := max(1, config.Height*side/max(config.Width, config.Height))
		// NRGBA and PNG retain alpha; transparent inputs are never flattened
		// onto a JPEG background. Each attempt scales the original, not a prior
		// preview, to avoid compounding resampling losses.
		resized := image.NewNRGBA(image.Rect(0, 0, width, height))
		draw.ApproxBiLinear.Scale(resized, resized.Bounds(), decoded, decoded.Bounds(), draw.Src, nil)
		var encoded bytes.Buffer
		if format == "png" {
			err = png.Encode(&encoded, resized)
		} else {
			err = jpeg.Encode(&encoded, resized, &jpeg.Options{Quality: 85})
		}
		if err != nil {
			return imagePreview{}, invalid
		}
		if encoded.Len() <= imagePreviewByteLimit {
			return imagePreview{imagePreviewMetadata: imagePreviewMetadata{MIME: mime, Width: width, Height: height, Bytes: encoded.Len(), Resized: true}, data: encoded.Bytes()}, nil
		}
		if side == 1 {
			return imagePreview{}, invalid
		}
		// Entropy determines PNG size. Reduce dimensions according to the
		// measured encoding and leave a margin, while guaranteeing progress.
		ratio := min(0.8, math.Sqrt(float64(imagePreviewByteLimit)/float64(encoded.Len()))*0.9)
		side = max(1, min(side-1, int(float64(side)*ratio)))
	}
}

type imageMCPGeneratedImage struct {
	generatedImage
	Preview imagePreviewMetadata `json:"preview"`
}

type imageMCPGenerationResult struct {
	Model      string                   `json:"model"`
	Images     []imageMCPGeneratedImage `json:"images"`
	CostUSD    *string                  `json:"costUSD,omitempty"`
	CostSource string                   `json:"costSource,omitempty"`
	Message    string                   `json:"message,omitempty"`
}

func imageMCPPreviews(result *imageGenerationResult) (imageMCPGenerationResult, []imagePreview) {
	summary := imageMCPGenerationResult{Model: result.Model, CostUSD: result.CostUSD, CostSource: result.CostSource}
	previews := make([]imagePreview, 0, len(result.Images))
	resized, omitted := false, false
	for _, original := range result.Images {
		preview, err := generatedImagePreview(original)
		metadata := preview.imagePreviewMetadata
		if err != nil {
			metadata = imagePreviewMetadata{Omitted: true}
			omitted = true
		} else {
			previews = append(previews, preview)
			resized = resized || metadata.Resized
		}
		summary.Images = append(summary.Images, imageMCPGeneratedImage{generatedImage: original, Preview: metadata})
	}
	if resized {
		summary.Message = "Inline images are bounded previews. The original full-resolution files remain unchanged at images[].path; use those paths for editing or copying."
	}
	if omitted {
		summary.Message = "One or more inline previews could not be created and were omitted. The original full-resolution files were saved at images[].path; use those paths for editing or copying. Do not regenerate solely to recover a preview."
	}
	return summary, previews
}
