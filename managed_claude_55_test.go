package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestManagedClaude55LaunchChecksEveryModelAndPreservesPreparation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX version executable; per-model preparation guards run on all platforms")
	}
	for _, client := range []string{"t3-code", "synara"} {
		t.Run(client, func(t *testing.T) {
			var a *app
			var root, uiHome string
			if client == "synara" {
				a = synaraTestApp(t, runtime.GOOS)
				paths := synaraPaths(a.dir)
				root, uiHome = paths.Root, paths.UIHome
			} else {
				a = t3CodeTestApp(t, runtime.GOOS, t3CodeNightlyVersion)
				paths := t3CodePaths(a.dir)
				root, uiHome = paths.Root, paths.UIHome
			}
			claude, err := a.launchRuntime().resolve("claude", "")
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("KILO_VERSION_EXPECTED_HOME", uiHome)
			writeVersion := func(version string) {
				t.Helper()
				script := "#!/bin/sh\n[ \"$1\" = --version ] || exit 2\n[ \"$HOME\" = \"$KILO_VERSION_EXPECTED_HOME\" ] || exit 3\nprintf '" + version + " (Claude Code)\\n'\n"
				if err := os.WriteFile(claude, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
			}
			writeVersion("2.1.267")
			library := modelLibrary{SchemaVersion: 1, DefaultModel: "anthropic/claude-opus-5.5", Models: []modelLibraryItem{
				{ID: "anthropic/claude-opus-4-6", ReasoningEffort: "low", ReasoningCustom: true, ReasoningLevels: []string{"low", "high"}, ContextWindow: 200000, MaxOutputTokens: 4096},
				{ID: "anthropic/claude-opus-5.5", ReasoningEffort: "high", ReasoningCustom: true, ReasoningLevels: []string{"low", "high"}, ContextWindow: 200000, MaxOutputTokens: 4096},
			}}
			if _, err := a.modelLibrary.save(library, a.modelLibrary.snapshot().Revision, false); err != nil {
				t.Fatal(err)
			}
			if client == "synara" {
				prepareSynaraFixture(t, a)
			} else {
				prepareT3CodeFixture(t, a)
			}
			if _, err := a.planClientLaunch(clientLaunchRequest{Client: client}, a.launchRuntime()); err != nil {
				t.Fatal(err)
			}
			before := map[string][]byte{}
			if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				data, err := os.ReadFile(path)
				if err == nil {
					before[path] = data
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			starts := 0
			a.launcher.start = func(clientLaunchPlan) error { starts++; return nil }
			writeVersion("2.1.266")
			response := adminRequest(a, "clients/launch", `{"client":"`+client+`"}`)
			if response.Code != 409 || !strings.Contains(response.Body.String(), claude55EffortMinVersion) {
				t.Fatal("first compatible model hid a later unsupported 5.5 default", response.Code, response.Body.String())
			}
			if starts != 0 || a.proxyListener != nil || !a.t3CodeLaunchUntil.IsZero() || !a.synaraLaunchUntil.IsZero() {
				t.Fatal("rejected downgrade dispatched side effects")
			}
			for path, expected := range before {
				actual, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(actual, expected) {
					t.Fatal("rejected downgrade changed prepared files", path, err)
				}
			}
			writeVersion("2.1.288")
			if _, err := a.planClientLaunch(clientLaunchRequest{Client: client}, a.launchRuntime()); err != nil {
				t.Fatal("compatible update rejected", err)
			}
		})
	}
}

func TestManagedClaude55PreparationCarriesVerifiedVersion(t *testing.T) {
	for _, synara := range []bool{false, true} {
		options := t3ProfileTestOptions(t)
		options.Version = t3CodeNightlyVersion
		options.Library = modelLibrary{SchemaVersion: 1, DefaultModel: "anthropic/claude-opus-5.5", Models: []modelLibraryItem{{ID: "anthropic/claude-opus-5.5", ReasoningEffort: "high", ReasoningCustom: true, ReasoningLevels: []string{"low", "high"}, ContextWindow: 200000}}}
		for _, version := range []string{"2.1.266", "2.1.267"} {
			options.ClaudeCaps = claudeCaps(version)
			var err error
			if synara {
				s := synaraProfileTestOptions(t)
				s.Library, s.ClaudeCaps = options.Library, options.ClaudeCaps
				_, err = planSynaraProfiles(s)
			} else {
				_, err = prepareT3CodeProfiles(options)
			}
			if version == "2.1.266" && (err == nil || !strings.Contains(err.Error(), claude55EffortMinVersion)) {
				t.Fatalf("old 5.5 CLI accepted: %v", err)
			}
			if version == "2.1.267" && err != nil {
				t.Fatal("verified 5.5 CLI rejected", err)
			}
		}
	}
}
