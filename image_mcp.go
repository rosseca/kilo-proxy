package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
)

const imageMCPProtocol = "2025-11-25"
const imageMCPRequestLimit = 64 << 10

func supportedImageMCPProtocol(version string) bool {
	return version == "2025-03-26" || version == "2025-06-18" || version == imageMCPProtocol
}

func imageMCPReply(w http.ResponseWriter, id json.RawMessage, result any) {
	jsonResponse(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}
func imageMCPError(w http.ResponseWriter, status int, id json.RawMessage, code int, message string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	jsonResponse(w, status, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}

// Stateless Streamable HTTP: clients send one JSON-RPC message per POST and
// receive JSON. No session identifier, server requests, SSE stream or batching.
func (a *app) imageMCPHandler(w http.ResponseWriter, r *http.Request, key, orgID, localKey string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Header.Get("Origin") != "" {
		imageMCPError(w, 403, nil, -32600, "Browser origins are not allowed.")
		return
	}
	if localKey == "" || !secureEqual(r.Header.Get("Authorization"), "Bearer "+localKey) {
		imageMCPError(w, 401, nil, -32600, "Invalid local credentials.")
		return
	}
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(405)
		return
	}
	protocol := r.Header.Get("MCP-Protocol-Version")
	if protocol != "" && !supportedImageMCPProtocol(protocol) {
		imageMCPError(w, 400, nil, -32600, "Unsupported MCP protocol version.")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		imageMCPError(w, 415, nil, -32600, "Content-Type must be application/json.")
		return
	}
	// This server always chooses the JSON response allowed by Streamable HTTP.
	if accept := r.Header.Get("Accept"); accept != "" && !strings.Contains(accept, "application/json") && !strings.Contains(accept, "*/*") {
		imageMCPError(w, 406, nil, -32600, "Accept must allow application/json.")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, imageMCPRequestLimit))
	if err != nil {
		imageMCPError(w, 413, nil, -32600, "MCP request is too large or incomplete.")
		return
	}
	var message struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if !json.Valid(raw) {
		imageMCPError(w, 400, nil, -32700, "Invalid JSON.")
		return
	}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' || json.Unmarshal(raw, &message) != nil || message.JSONRPC != "2.0" || message.Method == "" {
		imageMCPError(w, 400, nil, -32600, "Expected one JSON-RPC 2.0 request or notification.")
		return
	}
	if len(message.ID) == 0 {
		// Notifications never receive a JSON-RPC response. This stateless server
		// accepts initialization notifications and ignores unsupported notifications.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var id any
	decoder := json.NewDecoder(bytes.NewReader(message.ID))
	decoder.UseNumber()
	if decoder.Decode(&id) != nil {
		imageMCPError(w, 400, nil, -32600, "Invalid request identifier.")
		return
	}
	switch id.(type) {
	case string, json.Number:
	default:
		imageMCPError(w, 400, nil, -32600, "A request identifier must be a string or number.")
		return
	}
	switch message.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string          `json:"protocolVersion"`
			Capabilities    json.RawMessage `json:"capabilities"`
			ClientInfo      json.RawMessage `json:"clientInfo"`
		}
		if json.Unmarshal(message.Params, &params) != nil || params.ProtocolVersion == "" || len(params.Capabilities) == 0 || len(params.ClientInfo) == 0 {
			imageMCPError(w, 200, message.ID, -32602, "Initialization requires protocolVersion, capabilities and clientInfo.")
			return
		}
		negotiated := params.ProtocolVersion
		if !supportedImageMCPProtocol(negotiated) {
			negotiated = imageMCPProtocol
		}
		imageMCPReply(w, message.ID, map[string]any{"protocolVersion": negotiated, "capabilities": map[string]any{"tools": map[string]bool{"listChanged": false}}, "serverInfo": map[string]string{"name": "kilo-proxy-images", "version": version}, "instructions": "Use generate_image for image creation or editing. The configured image model uses your Kilo organization's credits. Only previous generated image paths may be used as references."})
	case "ping":
		imageMCPReply(w, message.ID, map[string]any{})
	case "tools/list":
		imageMCPReply(w, message.ID, map[string]any{"tools": []any{map[string]any{
			"name": "generate_image", "title": "Generate an image with Kilo",
			"description": "Generate an image from a text prompt using the image model selected in Kilo Proxy. Optionally edit a previously generated image by providing its returned absolute path as reference_image. Images are saved locally and charged to the configured Kilo organization. Do not retry automatically after a timeout; generation may already have been charged.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"prompt": map[string]any{"type": "string", "minLength": 1, "maxLength": imagePromptLimit, "description": "Describe the image to generate or the changes to make."}, "reference_image": map[string]any{"type": "string", "maxLength": 8192, "description": "Optional absolute path returned by an earlier generate_image call. Arbitrary local files and URLs are not accepted."}}, "required": []string{"prompt"}, "additionalProperties": false},
			"annotations": map[string]bool{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": true},
		}}})
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(message.Params, &params) != nil || params.Name != "generate_image" {
			imageMCPError(w, 200, message.ID, -32602, "Unknown image tool.")
			return
		}
		var args imageGenerationArguments
		decoder := json.NewDecoder(bytes.NewReader(params.Arguments))
		decoder.DisallowUnknownFields()
		if len(bytes.TrimSpace(params.Arguments)) == 0 || bytes.TrimSpace(params.Arguments)[0] != '{' || decoder.Decode(&args) != nil || decoder.Decode(new(any)) != io.EOF {
			imageMCPError(w, 200, message.ID, -32602, "Expected prompt and optional reference_image arguments.")
			return
		}
		if err := validImageGenerationArguments(args); err != nil {
			imageMCPError(w, 200, message.ID, -32602, err.Error())
			return
		}
		result, err := a.generateImage(r, args, key, orgID, localKey)
		if err != nil {
			imageMCPReply(w, message.ID, map[string]any{"isError": true, "content": []any{map[string]string{"type": "text", "text": err.Error()}}})
			return
		}
		summary, _ := json.Marshal(result)
		content := []any{map[string]string{"type": "text", "text": string(summary)}}
		for _, image := range result.Images {
			content = append(content, map[string]string{"type": "image", "mimeType": image.MIME, "data": base64.StdEncoding.EncodeToString(image.data)})
		}
		imageMCPReply(w, message.ID, map[string]any{"isError": false, "structuredContent": result, "content": content})
	default:
		imageMCPError(w, 200, message.ID, -32601, "Method not found.")
	}
}
