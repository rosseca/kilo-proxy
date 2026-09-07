package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

const vaultService = "ai.kilo.local-proxy"

type credentialVault interface {
	Get(string) (string, error)
	Set(string, string) error
	Delete(string) error
}

type systemVault struct{}

func (systemVault) Get(account string) (string, error) { return keyring.Get(vaultService, account) }
func (systemVault) Set(account, key string) error      { return keyring.Set(vaultService, account, key) }
func (systemVault) Delete(account string) error {
	err := keyring.Delete(vaultService, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Only the local proxy key is stored here. The upstream API key never enters this file.
type settings struct {
	Language string `json:"language,omitempty"`
	Port     int    `json:"port"`
	OrgID    string `json:"orgId"`
	LocalKey string `json:"localKey"`
	Remember bool   `json:"remember"`
	VaultID  string `json:"vaultId"`
}

func readSettings(dir string) (settings, error) {
	s := settings{Port: 8877, LocalKey: randomKey("kl_local_"), VaultID: randomKey("profile_")}
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	if s.Port < 1024 || s.Port > 65535 || len(s.LocalKey) < 32 || len(s.VaultID) < 32 {
		return s, errors.New("invalid settings.json; restore a valid configuration")
	}
	return s, nil
}

func writeSettings(dir string, s settings) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "settings.json"))
}
