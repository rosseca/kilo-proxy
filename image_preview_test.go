package main

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
)

func imagePreviewTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	state := uint32(0x62d873a1)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			index := img.PixOffset(x, y)
			if x < width/8 {
				continue // A transparent region must survive the preview.
			}
			state ^= state << 13
			state ^= state >> 17
			state ^= state << 5
			binary.LittleEndian.PutUint32(img.Pix[index:index+4], state)
		}
	}
	var output bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.NoCompression}
	if err := encoder.Encode(&output, img); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func imagePreviewTestOriginal(t *testing.T, data []byte) generatedImage {
	t.Helper()
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return generatedImage{Path: "/synthetic/generated-images/original." + format, MIME: "image/" + format, Width: config.Width, Height: config.Height, data: data}
}

func imagePreviewTestBounds(t *testing.T, preview imagePreview) image.Image {
	t.Helper()
	if len(preview.data) == 0 || len(preview.data) > imagePreviewByteLimit || preview.Bytes != len(preview.data) || preview.Width > imagePreviewSideLimit || preview.Height > imagePreviewSideLimit {
		t.Fatalf("preview exceeds its declared bounds: %+v, bytes=%d", preview.imagePreviewMetadata, len(preview.data))
	}
	decoded, format, err := image.Decode(bytes.NewReader(preview.data))
	if err != nil || preview.MIME != "image/"+format || decoded.Bounds().Dx() != preview.Width || decoded.Bounds().Dy() != preview.Height {
		t.Fatalf("invalid encoded preview or metadata: %+v, %v", preview.imagePreviewMetadata, err)
	}
	return decoded
}

func TestImagePreviewPreservesSmallPNGAndJPEG(t *testing.T) {
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 3, 2)), nil); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{imageTestPNG(t), jpg.Bytes()} {
		original := imagePreviewTestOriginal(t, data)
		preview, err := generatedImagePreview(original)
		if err != nil {
			t.Fatal(err)
		}
		imagePreviewTestBounds(t, preview)
		if preview.Resized || !bytes.Equal(preview.data, data) {
			t.Fatal("small compliant original was changed")
		}
	}
}

func TestImagePreviewBoundsEncodingAndDimensions(t *testing.T) {
	var wide bytes.Buffer
	if err := png.Encode(&wide, image.NewNRGBA(image.Rect(0, 0, 2048, 32))); err != nil {
		t.Fatal(err)
	}
	noisy := imagePreviewTestPNG(t, 1024, 768)
	decoded, _, err := image.Decode(bytes.NewReader(noisy))
	if err != nil {
		t.Fatal(err)
	}
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, decoded, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{{"wide but small PNG", wide.Bytes()}, {"noisy PNG within side limit", noisy}, {"large JPEG", jpg.Bytes()}} {
		t.Run(tc.name, func(t *testing.T) {
			original := imagePreviewTestOriginal(t, tc.data)
			preview, err := generatedImagePreview(original)
			if err != nil {
				t.Fatal(err)
			}
			imagePreviewTestBounds(t, preview)
			if !preview.Resized || preview.MIME != original.MIME || !bytes.Equal(original.data, tc.data) {
				t.Fatal("preview did not preserve the original bytes/format")
			}
			if delta := preview.Width*original.Height - preview.Height*original.Width; delta < -original.Width || delta > original.Width {
				t.Fatal("preview changed the aspect ratio beyond pixel rounding")
			}
		})
	}
}

func TestImagePreviewRejectsPixelBombAndDisclosesOmission(t *testing.T) {
	// Only the header is needed to check the allocation bound before decoding.
	data := append([]byte(nil), imageTestPNG(t)...)
	binary.BigEndian.PutUint32(data[16:20], 8192)
	binary.BigEndian.PutUint32(data[20:24], 8192)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	if config, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil || config.Width != 8192 || config.Height != 8192 {
		t.Fatalf("allocation-bound fixture needs a valid large image header: %+v, %v", config, err)
	}
	original := generatedImage{Path: "/synthetic/original.png", MIME: "image/png", Width: 8192, Height: 8192, data: data}
	if _, err := generatedImagePreview(original); err == nil {
		t.Fatal("preview accepted an image beyond the decode pixel cap")
	}
	result := &imageGenerationResult{Model: "vendor/image-model", Images: []generatedImage{original}}
	summary, previews := imageMCPPreviews(result)
	if len(previews) != 0 || len(summary.Images) != 1 || !summary.Images[0].Preview.Omitted || summary.Images[0].Path != original.Path || summary.Message == "" {
		t.Fatal("preview failure hid the saved original or omitted its explanation")
	}
}
