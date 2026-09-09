//go:build desktop

package main

import (
	"encoding/json"
	"image"
	"reflect"
	"strings"
	"testing"
	"time"

	"gioui.org/io/semantic"
	"github.com/pelletier/go-toml/v2"
)

func nativeImagesForTest() []modelInfo {
	no := false
	return []modelInfo{
		{ID: "google/gemini-3.1-flash-image", Name: "Gemini 3.1 Flash Image", OutputModalities: []string{"text", "image"}},
		{ID: "openai/gpt-5.4-image-2", Name: "GPT Image 2", OutputModalities: []string{"image"}, Tools: &no},
		{ID: "google/gemini-3-pro-image", Name: "Gemini 3 Pro Image", OutputModalities: []string{"image"}},
		{ID: "vendor/input-only", Name: "Image input only", InputModalities: []string{"image"}, OutputModalities: []string{"text"}},
	}
}

func TestNativeImageModelsAndAvailability(t *testing.T) {
	catalog := nativeImagesForTest()
	before, _ := json.Marshal(catalog)
	got := nativeImageModels(catalog)
	if len(got) != 3 || got[2].ID != "openai/gpt-5.4-image-2" {
		t.Fatalf("image-only model lost or sorting wrong: %+v", got)
	}
	after, _ := json.Marshal(catalog)
	if string(before) != string(after) {
		t.Fatal("image filtering changed the coding catalog")
	}
	s := &nativeClientSelection{}
	if !nativeClientImagesReady(s, nil) {
		t.Fatal("unloaded optional settings blocked a legacy profile")
	}
	for _, model := range []string{"", "bad id", "vendor/input-only", "vendor/missing"} {
		s.ImageGeneration = &imageGenerationSettings{Enabled: true, Model: model}
		if nativeClientImagesReady(s, catalog) {
			t.Fatalf("unsupported image model accepted: %q", model)
		}
	}
	s.ImageGeneration.Model = "openai/gpt-5.4-image-2"
	if !nativeClientImagesReady(s, catalog) {
		t.Fatal("image-only model required tools")
	}
	s.ImageGeneration.Enabled = false
	if !nativeClientImagesReady(s, nil) {
		t.Fatal("disabling missing model did not recover")
	}
}

func TestNativeClientImagesDraftsSharedSaveAndPolling(t *testing.T) {
	u := nativeTestUI(t)
	u.state = map[string]any{}
	a := u.clientState().selection("codex")
	b := u.clientState().selection("codex-cli")
	u.seedClientImages("codex", a)
	if a.ImageGeneration != nil {
		t.Fatal("missing authenticated state was treated as a saved setting")
	}
	u.state["imageGeneration"] = imageGenerationSettings{Enabled: true, Model: "vendor/saved"}
	u.seedClientImages("codex", a)
	u.seedClientImages("codex-cli", b)
	nativeEditClientImages(a, func(s *imageGenerationSettings) { s.Model = "vendor/draft" })
	u.seedClientImages("codex", a)
	if a.ImageGeneration.Model != "vendor/draft" || b.ImageGeneration.Model != "vendor/saved" || a.imageGenerationBaseline.Model != "vendor/saved" {
		t.Fatal("draft aliased another view or was lost to polling")
	}
	sent := cloneClientImageSettings(a.ImageGeneration)
	nativeEditClientImages(a, func(s *imageGenerationSettings) { s.Model = "vendor/newer-draft" })
	u.acceptClientImages(sent)
	if b.ImageGeneration.Model != "vendor/draft" || a.ImageGeneration.Model != "vendor/newer-draft" || a.imageGenerationBaseline.Model != "vendor/draft" {
		t.Fatal("save response clobbered a later edit or failed to update untouched sibling")
	}
	nativeEditClientImages(b, func(s *imageGenerationSettings) { s.Enabled = false })
	u.acceptClientImages(&imageGenerationSettings{Enabled: true, Model: "vendor/external"})
	if b.ImageGeneration.Enabled || b.ImageGeneration.Model != "vendor/draft" {
		t.Fatal("saving the other profile clobbered pending disable")
	}
	s := &nativeClientSelection{ImageGeneration: &imageGenerationSettings{Enabled: true, Model: "bad id"}}
	nativeEditClientImages(s, func(v *imageGenerationSettings) { v.Enabled = false })
	if s.ImageGeneration.Model != "" {
		t.Fatal("invalid saved ID prevented disabling")
	}
}

func TestNativeClientImagesPayloadLoadAndFingerprint(t *testing.T) {
	for _, key := range []string{"codex", "codex-cli", "xcode-codex"} {
		t.Run(key, func(t *testing.T) {
			s := (&nativeClients{}).selection(key)
			if err := s.add(nativeClientModelsForTest()[0], 50); err != nil {
				t.Fatal(err)
			}
			s.ImageGeneration = &imageGenerationSettings{Enabled: true, Model: "vendor/image"}
			payload, err := nativeClientPayload(key, s)
			if err != nil {
				t.Fatal(err)
			}
			p := payload.(map[string]any)
			_, present := p["imageGeneration"]
			if present != (key != "xcode-codex") {
				t.Fatal("image tool leaked into Xcode or was omitted from Codex")
			}
			data, _ := json.Marshal(p)
			loaded, err := decodeNativeClientSelection(key, data, nativeClientModelsForTest())
			if err != nil {
				t.Fatal(err)
			}
			if key == "xcode-codex" {
				if loaded.ImageGeneration != nil {
					t.Fatal("Xcode loaded a Codex image tool")
				}
				return
			}
			if loaded.ImageGeneration == nil || *loaded.ImageGeneration != *s.ImageGeneration || loaded.imageGenerationBaseline == loaded.ImageGeneration {
				t.Fatal("saved image setting was lost or aliased on Load")
			}
			before := nativeSelectionFingerprint(key, s, "base", "synthetic", claudeCapabilities{})
			nativeEditClientImages(s, func(v *imageGenerationSettings) { v.Model = "vendor/other" })
			if nativeSelectionFingerprint(key, s, "base", "synthetic", claudeCapabilities{}) == before {
				t.Fatal("changed image model did not require auto-prepare")
			}
			if p["imageGeneration"].(imageGenerationSettings).Model != "vendor/image" {
				t.Fatal("in-flight prepare payload mutated with the draft")
			}
			delete(p, "imageGeneration")
			data, _ = json.Marshal(p)
			legacy, err := decodeNativeClientSelection(key, data, nativeClientModelsForTest())
			if err != nil || legacy.ImageGeneration != nil {
				t.Fatal("legacy catalog invented disabled settings")
			}
		})
	}
}

func TestNativeClientImagesExportAndPointer(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "models"
	u.expanded["library.images"] = true
	u.models = append(nativeClientModelsForTest(), nativeImagesForTest()...)
	nativeSeedSharedForTest(t, u, u.models[0])
	s := u.sharedClientSelection("codex")
	nativeTestFrame(t, u)
	u.clickable("client:codex:images:model.toggle").Click()
	nativeEditClientImages(s, func(v *imageGenerationSettings) { v.Enabled = true })
	nativeTestFrame(t, u)
	u.clickable("client:codex:images:model:openai/gpt-5.4-image-2").Click()
	nativeTestFrame(t, u)
	if s.ImageGeneration.Model != "openai/gpt-5.4-image-2" || !reflect.DeepEqual(s.ids(), []string{"vendor/one"}) {
		t.Fatal("image choice changed the coding selection or failed")
	}
	for _, key := range []string{"codex", "codex-cli"} {
		config, err := u.clientExport(key, s, false)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := toml.Unmarshal([]byte(config), &document); err != nil {
			t.Fatal(err)
		}
		server := document["mcp_servers"].(map[string]any)["kilo_images"].(map[string]any)
		if !managedCodexImages(server) || server["tool_timeout_sec"] != int64(360) || server["startup_timeout_sec"] != int64(15) || document["model"] != "vendor/one" {
			t.Fatalf("incorrect MCP export: %+v", document)
		}
		for _, secret := range []string{u.owner.config.LocalKey, u.owner.apiKey, s.ImageGeneration.Model} {
			if secret != "" && strings.Contains(config, secret) {
				t.Fatal("export embedded credentials or used image model as the coding model")
			}
		}
	}
	nativeEditClientImages(s, func(v *imageGenerationSettings) { v.Enabled = false })
	config, err := u.clientExport("codex", s, false)
	var disabled map[string]any
	if err != nil || toml.Unmarshal([]byte(config), &disabled) != nil || disabled["mcp_servers"] != nil {
		t.Fatal("disabled export retained the tool", err)
	}
}

func TestNativeClientImagesResponsivePanel(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {720, 700}} {
		for _, language := range []string{"en", "es"} {
			t.Run(fmtSize(size)+"/"+language, func(t *testing.T) {
				u := nativeTestUI(t)
				u.page, u.language = "models", language
				u.expanded["library.images"] = true
				u.models = append(nativeClientModelsForTest(), nativeImagesForTest()...)
				s := u.clientState().selection("codex")
				s.ImageGeneration = &imageGenerationSettings{Enabled: true, Model: "openai/gpt-5.4-image-2"}
				h := &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
				h.frame()
				nativeScrollClientControlIntoView(h, "GPT Image 2  ▾", semantic.Button)
				button := h.target("GPT Image 2  ▾", semantic.Button)
				if !button.Desc.Bounds.In(image.Rectangle{Max: size}) {
					t.Fatal("image model selector clipped")
				}
				h.click("GPT Image 2  ▾", semantic.Button)
				h.click("Gemini 3 Pro Image", semantic.Button)
				if s.ImageGeneration.Model != "google/gemini-3-pro-image" {
					t.Fatal("pointer picked wrong image model")
				}
				nativeGridCapture(t, h, "native-images-"+fmtSize(size)+"-"+language)
			})
		}
	}
}

// Use the production semantic target position instead of a fixed page height.
// Optional client sections can grow, but the control must still be reachable
// through a bounded number of real pointer wheel events and fully visible.
func nativeScrollClientControlIntoView(h *nativePointerHarness, label string, class semantic.ClassOp) {
	h.t.Helper()
	viewport := image.Rect(0, 220, h.size.X, h.size.Y-48)
	if h.size.X >= 940 {
		viewport.Min.Y = 160
	}
	for attempt := 0; attempt < 6; attempt++ {
		delta := float32(h.size.Y / 2)
		nodes := h.nodes()
		for _, text := range nodes {
			if text.Desc.Label != label {
				continue
			}
			for _, node := range nodes {
				if node.Desc.Class != class || node.Desc.Disabled || !text.Desc.Bounds.Min.In(node.Desc.Bounds) {
					continue
				}
				if node.Desc.Bounds.In(viewport) {
					return
				}
				delta = float32(node.Desc.Bounds.Min.Y - (viewport.Min.Y + viewport.Dy()/2))
				break
			}
			break
		}
		nativeMenuWheel(h, image.Pt(h.size.X-60, 250), delta)
	}
	h.t.Fatalf("client control %q was not fully visible after bounded scrolling", label)
}
