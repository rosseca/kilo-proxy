package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Zed caches credentials by API URL. Migrating the helper's URL makes an
// already-running Zed load the newly stored credential, including after local
// key rotation. The digest identifies a configuration; it is not authentication.
func zedProxyPrefix(localKey string) string {
	digest := sha256.Sum256([]byte(localKey))
	return "/zed/" + hex.EncodeToString(digest[:8])
}

func zedBaseURL(baseURL, localKey string) string {
	return strings.TrimSuffix(baseURL, "/v1") + zedProxyPrefix(localKey) + "/v1"
}
