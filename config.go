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
	CodexCLIExecutable              string                  `json:"codexCLIExecutable,omitempty"`
	ClaudeCodeExecutable            string                  `json:"claudeCodeExecutable,omitempty"`
	MuseCodeExecutable              string                  `json:"museCodeExecutable,omitempty"`
	MuseCode                        musePreferences         `json:"museCode"`
	Synara                          synaraPreferences       `json:"synara,omitempty"`
	SynaraExecutable                string                  `json:"synaraExecutable,omitempty"`
	Omnigent                        omnigentPreferences     `json:"omnigent,omitempty"`
	OmnigentExecutable              string                  `json:"omnigentExecutable,omitempty"`
	OpenCodeMajor                   int                     `json:"openCodeMajor,omitempty"`
	OpenCode1Executable             string                  `json:"openCode1Executable,omitempty"`
	OpenCode2Executable             string                  `json:"openCode2Executable,omitempty"`
	T3Code                          t3CodePreferences       `json:"t3Code"`
	T3CodeTelemetryDisabled         bool                    `json:"t3CodeTelemetryDisabled,omitempty"`
	ClaudeDesktopExperimentalModels bool                    `json:"claudeDesktopExperimentalModels,omitempty"`
	ImageTransport                  imageTransportSettings  `json:"imageTransport"`
	CaptureActivity                 bool                    `json:"captureActivity"`
	ImageGeneration                 imageGenerationSettings `json:"imageGeneration"`
	TrayDisplay                     string                  `json:"trayDisplay,omitempty"`
	Language                        string                  `json:"language,omitempty"`
	Port                            int                     `json:"port"`
	OrgID                           string                  `json:"orgId"`
	OrgName                         string                  `json:"orgName,omitempty"`
	LocalKey                        string                  `json:"localKey"`
	Remember                        bool                    `json:"remember"`
	VaultID                         string                  `json:"vaultId"`
}

func readSettings(dir string) (settings, error) {
	s := settings{Omnigent: normalizeOmnigentPreferences(omnigentPreferences{}), MuseCode: normalizeMusePreferences(musePreferences{}), T3Code: normalizeT3CodePreferences(t3CodePreferences{}), Port: 8877, LocalKey: randomKey("kl_local_"), VaultID: randomKey("profile_"), TrayDisplay: trayDisplayIcon, ImageTransport: normalizeImageTransportSettings(imageTransportSettings{})}
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		s.Synara = defaultSynaraPreferences()
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	if s.OpenCodeMajor != 0 && s.OpenCodeMajor != 1 && s.OpenCodeMajor != 2 {
		return s, errors.New("invalid OpenCode version preference")
	}
	if validateOpenCodeExecutable(s.OpenCode1Executable) != nil || validateOpenCodeExecutable(s.OpenCode2Executable) != nil {
		return s, errors.New("invalid OpenCode executable preference")
	}
	if validateCLIExecutable(s.CodexCLIExecutable) != nil || validateCLIExecutable(s.ClaudeCodeExecutable) != nil || validateCLIExecutable(s.MuseCodeExecutable) != nil {
		return s, errors.New("Invalid CLI executable preferences.")
	}
	if validateSynaraExecutablePath(s.SynaraExecutable) != nil {
		return s, errors.New("Invalid Synara executable preference.")
	}
	if validateCLIExecutable(s.OmnigentExecutable) != nil {
		return s, errors.New("Invalid Omnigent executable preference.")
	}
	var members map[string]json.RawMessage
	_ = json.Unmarshal(b, &members)
	if raw, present := members["omnigent"]; present {
		p, err := decodeOmnigentPreferences(raw)
		if err != nil {
			return s, errors.New("Invalid Omnigent settings; restore a valid configuration.")
		}
		s.Omnigent = p
	}
	if _, present := members["synara"]; !present && s.Synara.Version == 0 {
		if _, legacyErr := os.Lstat(synaraPaths(dir).Settings); errors.Is(legacyErr, os.ErrNotExist) {
			s.Synara = defaultSynaraPreferences()
		}
	}
	if s.Synara.Version != 0 {
		s.Synara = normalizeSynaraPreferences(s.Synara)
		if err := validateSynaraPreferences(s.Synara); err != nil {
			return s, errors.New("invalid Synara settings; restore a valid configuration")
		}
	}
	s.TrayDisplay = normalizeTrayDisplay(s.TrayDisplay)
	s.MuseCode = normalizeMusePreferences(s.MuseCode)
	if err := validateMusePreferences(s.MuseCode); err != nil {
		return s, errors.New("invalid Muse Code connection settings; restore a valid configuration")
	}
	s.ImageTransport = normalizeImageTransportSettings(s.ImageTransport)
	if raw, present := members["t3Code"]; present {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if policy, present := fields["museModels"]; present {
			var value string
			if json.Unmarshal(policy, &value) != nil || (value != "official" && value != "shared") {
				return s, errors.New("invalid T3 Code settings; restore a valid configuration")
			}
		} else {
			// The seeded defaults above are for new accounts. A persisted
			// pre-policy Kilo Muse account used every shared model.
			s.T3Code.MuseModels = ""
		}
	}
	s.T3Code = normalizeT3CodePreferences(s.T3Code)
	if err := validateT3CodePreferences(s.T3Code); err != nil {
		return s, errors.New("invalid T3 Code settings; restore a valid configuration")
	}
	if err := validateImageTransportSettings(s.ImageTransport); err != nil {
		return s, errors.New("invalid image transport settings; restore a valid configuration")
	}
	if s.Port < 1024 || s.Port > 65535 || len(s.LocalKey) < 32 || len(s.VaultID) < 32 {
		return s, errors.New("invalid settings.json; restore a valid configuration")
	}
	if err := validateImageGenerationSettings(s.ImageGeneration); err != nil {
		return s, errors.New("invalid image generation settings; restore a valid configuration")
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
