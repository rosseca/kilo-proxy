package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"reflect"
	"testing"
)

func TestSynaraWindowsEnvironmentRehydrationGuard(t *testing.T) {
	names := []string{"Path", "Home", "ANTHROPIC_API_KEY", "anthropic_api_key", "Synara_Host", "SYNARA_BETA_HOME", "SYNARA_HOME", "SYNARA_DESKTOP_SMOKE_USER_DATA", "SYNARA_DISABLE_AUTO_UPDATE", "ZDOTDIR", "BASH_ENV", "ENV", "VITE_DEV_SERVER_URL", "ELECTRON_RUN_AS_NODE"}
	want := []string{"ANTHROPIC_API_KEY", "BASH_ENV", "ELECTRON_RUN_AS_NODE", "ENV", "SYNARA_HOST", "VITE_DEV_SERVER_URL", "ZDOTDIR"}
	if got := synaraPersistedOverrides(names); !reflect.DeepEqual(got, want) {
		t.Fatalf("rehydrated overrides: %v", got)
	}
	if !synaraRegistryNamesComplete([]string{"Path"}, io.EOF) || !synaraRegistryNamesComplete(nil, io.EOF) || synaraRegistryNamesComplete(nil, errors.New("access denied")) || synaraRegistryNamesComplete(make([]string, 512), nil) {
		t.Fatal("registry enumeration completeness was misclassified")
	}
	a := synaraTestApp(t, "windows")
	prepareSynaraFixture(t, a)
	paths := synaraPaths(a.dir)
	before, err := os.ReadFile(paths.Selection)
	if err != nil {
		t.Fatal(err)
	}
	var started bool
	a.launcher.start = func(clientLaunchPlan) error { started = true; return nil }
	a.synaraCheckEnvironment = func() error { return errors.New("registered provider override") }
	w := adminRequest(a, "clients/launch", `{"client":"synara"}`)
	if w.Code != 409 || started || !a.synaraLaunchUntil.IsZero() {
		t.Fatalf("blocked registry launch mutated process/lease: %d %s started=%v", w.Code, w.Body.String(), started)
	}
	after, err := os.ReadFile(paths.Selection)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("blocked launch changed snapshot")
	}
	a.synaraCheckEnvironment = func() error { return nil }
	if _, err := a.planClientLaunch(clientLaunchRequest{Client: "synara"}, a.launchRuntime()); err != nil {
		t.Fatalf("safe Windows plan: %v", err)
	}
}
