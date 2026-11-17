package main

import (
	"net/http"
	"strings"
)

func validateEmbeddingProvider(model, key, orgID string) (status int, code, message string) {
	if key == "" || orgID == "" {
		return http.StatusUnauthorized, "kilo_connection_required", "Connect Kilo and select an organization to use embeddings."
	}
	if strings.HasPrefix(model, "chatgpt/") {
		return http.StatusBadRequest, "unsupported_embedding_provider", "Embedding requests require a Kilo model; ChatGPT subscription models are not supported."
	}
	return 0, "", ""
}

func embeddingError(w http.ResponseWriter, status int, code, message string) {
	jsonResponse(w, status, map[string]any{"error": map[string]string{"type": "kilo_local_error", "code": code, "message": message}})
}
