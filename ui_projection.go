package main

import (
	"crypto/sha256"
	"encoding/json"
	"strings"
)

// Published under app.mu after committed connection/settings mutations. It
// contains no credential, mutable slice or map. Rendering never acquires app.mu.
type appUIProjection struct {
	Fingerprint                                                [32]byte
	HasKey, HasOrg, KiloReady, LiteLLMReady, ProviderChanging  bool
	Port                                                       int
	OrgID, Language, TrayDisplay, UsageScope, CatalogBaseScope string
	Remember                                                   bool
	CatalogRevision                                            uint64
	ImageTransport                                             imageTransportSettings
}

type chatGPTUIProjection struct {
	Connected        bool
	Status, Identity string
}

// Caller holds app.mu. Keep this independent of ChatGPT's lock: its own atomic
// projection is composed by readers, so publishing never inverts lock order.
func (a *app) publishUIProjectionLocked() {
	scope := a.catalogBaseScopeLocked()
	connection := []any{a.config.Port, a.config.OrgID, a.config.LocalKey, a.apiKey}
	if scope != a.config.OrgID {
		connection = append(connection, scope)
	}
	data, _ := json.Marshal(connection)
	a.uiProjection.Store(&appUIProjection{
		Fingerprint:      sha256.Sum256(data),
		HasKey:           strings.TrimSpace(a.apiKey) != "",
		HasOrg:           strings.TrimSpace(a.config.OrgID) != "",
		KiloReady:        a.kiloReadyLocked(),
		LiteLLMReady:     a.liteLLMReadyLocked(),
		CatalogBaseScope: scope,
		ProviderChanging: a.providerChanging,
		Port:             a.config.Port, OrgID: a.config.OrgID, Language: a.config.Language,
		Remember: a.config.Remember, TrayDisplay: normalizeTrayDisplay(a.config.TrayDisplay),
		CatalogRevision: a.catalogRevision, UsageScope: usageAccountID(a.apiKey, a.config.OrgID),
		ImageTransport: normalizeImageTransportSettings(a.config.ImageTransport),
	})
}

func (a *app) readUIProjection() appUIProjection {
	if p := a.uiProjection.Load(); p != nil {
		return *p
	}
	return appUIProjection{}
}

func (c *chatGPTConnection) publishUIProjectionLocked() {
	c.uiProjection.Store(&chatGPTUIProjection{
		Connected: validChatGPTCredentials(c.creds),
		Status:    c.state.Status, Identity: c.creds.Account,
	})
}

func (c *chatGPTConnection) readUIProjection() chatGPTUIProjection {
	if c != nil {
		if p := c.uiProjection.Load(); p != nil {
			return *p
		}
	}
	return chatGPTUIProjection{Status: "idle"}
}

func (a *app) uiConnectionReady() bool {
	p, c := a.readUIProjection(), a.chatgpt.readUIProjection()
	return p.KiloReady || p.LiteLLMReady || !p.ProviderChanging && c.Connected && c.Status != "pending"
}

func (a *app) uiCatalogScope() string {
	p, c := a.readUIProjection(), a.chatgpt.readUIProjection()
	if c.Connected {
		return p.CatalogBaseScope + "|chatgpt:" + c.Identity
	}
	return p.CatalogBaseScope
}
