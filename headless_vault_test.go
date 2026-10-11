//go:build !windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestHeadlessVaultPrivatePersistenceAndIsolation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profile")
	vault, err := newHeadlessVault(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Get("missing"); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("missing slot: %v", err)
	}
	key := strings.Repeat("private-key-", 500)
	if err := vault.Set("profile_one", key); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newHeadlessVault(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reloaded.Get("profile_one")
	if err != nil || got != key {
		t.Fatalf("private key did not persist: %v", err)
	}
	if got, err := reloaded.Get("profile_two"); got != "" || !errors.Is(err, keyring.ErrNotFound) {
		t.Fatal("another profile read a credential")
	}
	for path, want := range map[string]os.FileMode{dir: 0700, filepath.Join(dir, "headless-secrets"): 0700, filepath.Join(dir, "headless-secrets", "profile_one"): 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("private permission at %s: %v", path, err)
		}
	}
	if err := reloaded.Delete("profile_one"); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Delete("profile_one"); err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.Get("profile_one"); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatal("deleted credential still exists")
	}
	if credentialVaultLimit(vault) < len(key) || credentialVaultLimit(systemVault{}) != 2400 {
		t.Fatal("incorrect vault size contract")
	}
}

func TestHeadlessVaultRejectsUnsafeFilesAndSlots(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profile")
	vault, err := newHeadlessVault(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, slot := range []string{"../escaped", "", "a/b", "a\nprivate"} {
		if err := vault.Set(slot, "private"); err == nil {
			t.Fatal("unsafe slot accepted")
		}
	}
	target := filepath.Join(t.TempDir(), "unrelated")
	if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "headless-secrets", "alias")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Get("alias"); err == nil {
		t.Fatal("credential alias read")
	}
	if err := vault.Set("alias", "replacement"); err == nil {
		t.Fatal("credential alias replaced")
	}
	if err := vault.Delete("alias"); err == nil {
		t.Fatal("credential alias deleted")
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "unchanged" {
		t.Fatal("unrelated file changed")
	}
	if err := vault.Set("public", "private"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "headless-secrets", "public"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Get("public"); err == nil {
		t.Fatal("public credential file read")
	}
	if err := vault.Set("public", "replacement"); err == nil {
		t.Fatal("public credential file replaced")
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := newHeadlessVault(dir); err == nil {
		t.Fatal("public profile accepted")
	}
}
