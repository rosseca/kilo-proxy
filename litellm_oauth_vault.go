package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

const liteLLMBrowserMode = "browser"

type liteLLMBrowserVaultManifest struct {
	Generation string `json:"generation"`
	Chunks     int    `json:"chunks"`
}

func liteLLMBrowserVaultSlot(profile string, cfg liteLLMSettings) string {
	return "oauth_" + liteLLMVaultSlot(profile, cfg)
}

func (manifest liteLLMBrowserVaultManifest) chunk(slot string, index int) string {
	return slot + "_" + manifest.Generation + "_" + strconv.Itoa(index)
}

func readLiteLLMBrowserManifest(vault credentialVault, slot string) liteLLMBrowserVaultManifest {
	var manifest liteLLMBrowserVaultManifest
	value, err := vault.Get(slot)
	if err != nil || json.Unmarshal([]byte(value), &manifest) != nil {
		return liteLLMBrowserVaultManifest{}
	}
	generation, err := hex.DecodeString(manifest.Generation)
	if err != nil || len(generation) != 32 || manifest.Chunks < 1 || manifest.Chunks > 32 {
		return liteLLMBrowserVaultManifest{}
	}
	return manifest
}

func removeLiteLLMBrowserChunks(vault credentialVault, slot string, manifest liteLLMBrowserVaultManifest) error {
	var result error
	for i := range manifest.Chunks {
		result = errors.Join(result, vault.Delete(manifest.chunk(slot, i)))
	}
	return result
}

func (a *app) loadLiteLLMBrowserCredential(profile string, cfg liteLLMSettings) *liteLLMBrowserCredential {
	if _, headless := a.vault.(headlessVault); headless || a.vault == nil || cfg.VaultRef == "" {
		return nil
	}
	slot := liteLLMBrowserVaultSlot(profile, cfg)
	manifest := readLiteLLMBrowserManifest(a.vault, slot)
	if manifest.Chunks == 0 {
		return nil
	}
	var encoded strings.Builder
	limit := credentialVaultLimit(a.vault)
	for i := range manifest.Chunks {
		chunk, err := a.vault.Get(manifest.chunk(slot, i))
		if err != nil || len(chunk) == 0 || len(chunk) > limit {
			return nil
		}
		encoded.WriteString(chunk)
	}
	raw, err := base64.RawStdEncoding.DecodeString(encoded.String())
	if err != nil {
		return nil
	}
	var credential liteLLMBrowserCredential
	if json.Unmarshal(raw, &credential) != nil ||
		credential.Access == "" || credential.Refresh == "" || credential.ClientID == "" || credential.ExpiresAt.IsZero() || credential.RefreshAfter.IsZero() || !credential.RefreshAfter.Before(credential.ExpiresAt) ||
		!validLiteLLMKey(credential.Access) || !validLiteLLMKey(credential.Refresh) || !validLiteLLMKey(credential.ClientID) {
		return nil
	}
	base, err := url.Parse(cfg.URL)
	if err != nil || validateLiteLLMBrowserTransport(base) != nil {
		return nil
	}
	for _, raw := range []string{credential.Resource, credential.TokenURL, credential.RevocationURL} {
		if _, err := liteLLMOAuthURL(raw, base); err != nil {
			return nil
		}
	}
	return &credential
}

// Keychain implementations impose per-entry limits. Write the new pair in a
// separate generation and publish its manifest last, so a failed write cannot
// leave an access token paired with the wrong refresh token.
func (a *app) saveLiteLLMBrowserCredential(profile string, cfg liteLLMSettings, credential liteLLMBrowserCredential) error {
	if _, headless := a.vault.(headlessVault); headless || a.vault == nil {
		return errLiteLLMOAuthStorage
	}
	raw, err := json.Marshal(credential)
	limit := credentialVaultLimit(a.vault)
	ascii := base64.RawStdEncoding.EncodeToString(raw)
	if err != nil || len(ascii) > min(64<<10, 32*limit) {
		return errLiteLLMOAuthStorage
	}
	slot := liteLLMBrowserVaultSlot(profile, cfg)
	old := readLiteLLMBrowserManifest(a.vault, slot)
	next := liteLLMBrowserVaultManifest{Generation: randomKey("")}
	for start := 0; start < len(ascii); start += limit {
		part := ascii[start:min(start+limit, len(ascii))]
		next.Chunks++
		if err := a.vault.Set(next.chunk(slot, next.Chunks-1), part); err != nil {
			_ = removeLiteLLMBrowserChunks(a.vault, slot, next)
			return errLiteLLMOAuthStorage
		}
	}
	manifest, _ := json.Marshal(next)
	if err := a.vault.Set(slot, string(manifest)); err != nil {
		_ = removeLiteLLMBrowserChunks(a.vault, slot, next)
		return errLiteLLMOAuthStorage
	}
	_ = removeLiteLLMBrowserChunks(a.vault, slot, old)
	return nil
}

func stagedLiteLLMBrowserSettings(cfg liteLLMSettings) liteLLMSettings {
	ref := sha256.Sum256([]byte(cfg.VaultRef + "\x00staged"))
	cfg.VaultRef = hex.EncodeToString(ref[:])
	return cfg
}

func (a *app) deleteLiteLLMBrowserVaultSlot(profile string, cfg liteLLMSettings) error {
	slot := liteLLMBrowserVaultSlot(profile, cfg)
	manifest := readLiteLLMBrowserManifest(a.vault, slot)
	if err := removeLiteLLMBrowserChunks(a.vault, slot, manifest); err != nil {
		return err
	}
	return a.vault.Delete(slot)
}

func (a *app) deleteLiteLLMStoredCredential(profile string, cfg liteLLMSettings) error {
	if cfg.VaultRef == "" {
		return nil
	}
	if cfg.AuthMode != liteLLMBrowserMode {
		return a.vault.Delete(liteLLMVaultSlot(profile, cfg))
	}
	return errors.Join(a.deleteLiteLLMBrowserVaultSlot(profile, cfg),
		a.deleteLiteLLMBrowserVaultSlot(profile, stagedLiteLLMBrowserSettings(cfg)))
}
