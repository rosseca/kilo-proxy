//go:build desktop

package main

import (
	"encoding/json"
	"image"
	"reflect"
	"testing"

	"gioui.org/io/key"
	"gioui.org/io/semantic"
)

func TestNativeLabFilterCombinesViewsWithoutChangingProfile(t *testing.T) {
	s := (&nativeClients{}).selection("codex")
	models := nativeClientModelsForTest()
	models = append(models, modelInfo{ID: "~anthropic/other", Name: "Other Claude"}, modelInfo{ID: "google/image", Name: "Image"})
	for _, m := range append(append([]modelInfo(nil), models[:2]...), modelInfo{ID: "manual-lab/saved", Name: "Saved model"}) {
		if err := s.add(m, 50); err != nil {
			t.Fatal(err)
		}
	}
	s.Models[1].DisplayName = "My Claude"
	s.Initial = models[0].ID
	before, _ := json.Marshal(s)
	for _, tc := range []struct {
		lab, query       string
		selected, coding bool
		want             []string
	}{
		{"anthropic", "", false, false, []string{"anthropic/claude-sonnet-4.6", "~anthropic/other"}},
		{"anthropic", "My Claude", true, true, []string{"anthropic/claude-sonnet-4.6"}},
		{"anthropic", "", false, true, []string{"anthropic/claude-sonnet-4.6"}},
		{"manual-lab", "saved", true, true, []string{"manual-lab/saved"}},
		{"google", "", true, false, []string{}},
		{"missing-lab", "", false, false, []string{}},
	} {
		view := nativeVisibleModels(models, s, tc.query, tc.selected, tc.coding, "name", tc.lab)
		ids := make([]string, len(view))
		for i, model := range view {
			ids[i] = model.ID
		}
		if !reflect.DeepEqual(ids, tc.want) {
			t.Errorf("%+v: got %v", tc, ids)
		}
	}
	after, _ := json.Marshal(s)
	if string(before) != string(after) {
		t.Fatal("lab view changed profile, custom name or initial model")
	}
	if got := nativeVisibleModels(models, s, "", true, false, "name", ""); len(got) != 3 {
		t.Fatal("All labs did not restore hidden selections")
	}
}

func TestNativeLabMenuPointerAndCrossClientState(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {780, 700}, {720, 700}} {
		t.Run(fmtSize(size), func(t *testing.T) {
			h := newNativePointerHarness(t, size)
			h.u.models = nativeClientModelsForTest()
			selection := h.u.clientState().selection("codex")
			if err := selection.add(h.u.models[0], 50); err != nil {
				t.Fatal(err)
			}
			label := "Clients & models"
			if size.X < 940 {
				label = "Clients"
			}
			h.click(label, semantic.Button)
			before, _ := json.Marshal(selection)
			h.click("All labs  ▾", semantic.Button)
			h.click("Anthropic", semantic.Button)
			if h.u.value("models.lab") != "anthropic" || h.u.expanded["models.lab"] {
				t.Fatal("lab selection did not apply or close menu")
			}
			h.click("Anthropic  ▾", semantic.Button)
			h.click("Code Mode Rank  ▾", semantic.Button)
			if h.u.expanded["models.lab"] || !h.u.expanded["models.sort"] {
				t.Fatal("opening sort left both menus open")
			}
			h.click("Speed", semantic.Button)
			h.click("Codex CLI", semantic.Button)
			if h.u.value("models.lab") != "anthropic" || h.u.value("models.sort") != "speed" {
				t.Fatal("switching client lost catalog preferences")
			}
			h.click("Anthropic  ▾", semantic.Button)
			h.click("provider/model", semantic.Editor)
			if h.u.expanded["models.lab"] || !h.router.Source().Focused(h.u.editor("client:codex-cli:search")) {
				t.Fatal("outside click did not close labs and focus search")
			}
			h.click("Anthropic  ▾", semantic.Button)
			h.router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
			h.frame()
			h.frame()
			if h.u.expanded["models.lab"] {
				t.Fatal("Escape did not dismiss labs")
			}
			h.u.models = nil
			h.frame()
			h.click("Anthropic  ▾", semantic.Button)
			h.click("All labs", semantic.Button)
			if h.u.value("models.lab") != "" {
				t.Fatal("could not reset lab after catalog changed")
			}
			after, _ := json.Marshal(selection)
			if string(before) != string(after) {
				t.Fatal("filtering or switching client changed the Codex profile")
			}
		})
	}
}
