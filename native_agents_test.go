//go:build desktop

package main

import (
	"errors"
	"image"
	"strings"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

func nativeSeedSharedForTest(t *testing.T, u *nativeUI, models ...modelInfo) *nativeClientSelection {
	t.Helper()
	u.initModelLibrary()
	s := u.library.selection
	s.Models, s.Initial = nil, ""
	for _, model := range models {
		if err := s.add(model, 50); err != nil {
			t.Fatal(err)
		}
		u.seedClientChoice(sharedModelKey, *s.choice(model.ID))
	}
	u.persistLibraryEdits()
	u.flushModelLibrary()
	return s
}

func TestNativeAgentHomeLaunchRemembersProject(t *testing.T) {
	u, r := nativeLaunchTestUI(t, "codex", false, false)
	u.page = "agents"
	directory := t.TempDir()
	u.agentsState().chooseFolder = func(initial string) (string, error) { return directory, nil }
	u.chooseAgentProject("codex")
	nativeTestWait(t, u, func() bool { return u.agentsState().FolderBusy == "" })
	u.launchAgent("codex")
	nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
	if r.count() != 1 || r.prepares.Load() != 1 {
		t.Fatalf("home launch failed: %s", u.notice)
	}
	r.mu.Lock()
	actual := r.plans[0].Directory
	r.mu.Unlock()
	if actual != directory {
		t.Fatalf("wrong project launched: %s", actual)
	}
	p, err := readAgentPreferences(u.owner.dir)
	if err != nil || p.Projects["codex"] != directory {
		t.Fatalf("project not saved: %+v %v", p, err)
	}
	u.agents = nil
	if restored := u.agentProject("codex"); restored != directory {
		t.Fatalf("project not restored: %s", restored)
	}
}

func TestNativeAgentFolderChooserCancellationAndFailure(t *testing.T) {
	for _, failure := range []error{errNativePickerCancelled, errors.New("synthetic chooser failure")} {
		u, _ := nativeLaunchTestUI(t, "codex", false, false)
		a := u.agentsState()
		u.setValue(agentProjectField("codex"), "unchanged")
		a.chooseFolder = func(string) (string, error) { return "", failure }
		u.chooseAgentProject("codex")
		nativeTestWait(t, u, func() bool { return a.FolderBusy == "" })
		if u.value(agentProjectField("codex")) != "unchanged" {
			t.Fatal("chooser failure changed folder")
		}
		if errors.Is(failure, errNativePickerCancelled) && a.Error != "" {
			t.Fatal("cancel reported an error")
		}
		if !errors.Is(failure, errNativePickerCancelled) && !strings.Contains(a.Error, "synthetic") {
			t.Fatal("chooser failure was hidden")
		}
	}
}

func TestNativeAgentLaunchRejectsProjectAndConnectionChangesDuringPrepare(t *testing.T) {
	for _, changed := range []string{"project", "connection"} {
		t.Run(changed, func(t *testing.T) {
			u, r := nativeLaunchTestUI(t, "codex", true, false)
			u.page = "agents"
			u.launchAgent("codex")
			<-r.entered
			if changed == "project" {
				u.setValue(agentProjectField("codex"), t.TempDir())
			} else {
				u.owner.mu.Lock()
				u.owner.config.OrgID = "new-team"
				u.owner.mu.Unlock()
			}
			r.unblock()
			nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
			if r.count() != 0 || !strings.Contains(u.notice, "changed while preparing") || u.agentsState().Phase != "" {
				t.Fatalf("stale launch escaped: %s", u.notice)
			}
		})
	}
}

func TestNativeAgentHomePrimaryLaunchAboveFold(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {720, 700}} {
		for _, lang := range []string{"en", "es"} {
			t.Run(fmtSize(size)+"-"+lang, func(t *testing.T) {
				u, r := nativeLaunchTestUI(t, "codex", false, false)
				u.page, u.language = "agents", lang
				h := &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
				h.frame()
				nativeGridCapture(t, h, "native-agents-"+fmtSize(size)+"-"+lang)
				for _, label := range []string{u.tr("Open Codex", "Abrir Codex"), u.tr("Choose folder", "Elegir carpeta"), u.tr("Edit models", "Editar modelos")} {
					if !h.target(label, semantic.Button).Desc.Bounds.In(image.Rectangle{Max: size}) {
						t.Fatalf("primary control clipped: %s", label)
					}
				}
				h.click(u.tr("Open Codex", "Abrir Codex"), semantic.Button)
				nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
				if r.count() != 1 {
					t.Fatalf("pointer did not open Codex: %s", u.notice)
				}
			})
		}
	}
}

func TestNativeAgentChooserPreservesConcurrentFolderEdit(t *testing.T) {
	u, _ := nativeLaunchTestUI(t, "codex", false, false)
	a := u.agentsState()
	release := make(chan struct{})
	directory := t.TempDir()
	a.chooseFolder = func(string) (string, error) { <-release; return directory, nil }
	u.chooseAgentProject("codex")
	u.setValue(agentProjectField("codex"), "latest folder")
	close(release)
	nativeTestWait(t, u, func() bool { return a.FolderBusy == "" })
	if u.value(agentProjectField("codex")) != "latest folder" || !strings.Contains(a.Error, "changed while") {
		t.Fatalf("concurrent folder edit lost: %s", a.Error)
	}
}

func TestNativeAgentHomeModelAndIntegrationNavigation(t *testing.T) {
	u, _ := nativeLaunchTestUI(t, "codex", false, false)
	u.page = "agents"
	nativeTestFrame(t, u)
	u.clickable("agents:edit-models").Click()
	nativeTestFrame(t, u)
	if u.page != "models" {
		t.Fatal("Edit models did not open common library")
	}
	u.agentSetup("xcode-claude")
	if u.page != "clients" || u.client != "xcode" || u.clients.Variant != "claude" {
		t.Fatal("Xcode setup lost selected integration")
	}
	u.agentSetup("other")
	if u.page != "clients" || u.client != "generic" {
		t.Fatal("Other clients setup did not open")
	}
}
