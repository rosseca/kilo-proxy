package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/pelletier/go-toml/v2"
)

const codexImagesServer = "kilo_images"

// Only the dedicated local server is managed. A same-name user server is a
// conflict, never permission to replace an unrelated command or remote URL.
func managedCodexImages(raw any) bool {
	table, ok := raw.(map[string]any)
	if !ok || table["bearer_token_env_var"] != "KILO_LOCAL_API_KEY" {
		return false
	}
	for _, field := range []string{"command", "args", "env"} {
		if _, exists := table[field]; exists {
			return false
		}
	}
	value, ok := table["url"].(string)
	if !ok {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Path != "/mcp/images" || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return false
	}
	port, err := strconv.Atoi(u.Port())
	return err == nil && port >= 1024 && port <= 65535
}

func mergeCodexImages(data []byte, images imageGenerationSettings, port int) ([]byte, error) {
	if err := validateImageGenerationSettings(images); err != nil {
		return nil, err
	}
	if images.Enabled && (port < 1024 || port > 65535) {
		return nil, errors.New("Invalid local proxy port")
	}
	var before, after map[string]any
	if toml.Unmarshal(data, &before) != nil || toml.Unmarshal(data, &after) != nil {
		return nil, errors.New("Invalid Codex TOML; image settings were not saved")
	}
	if after == nil {
		after = map[string]any{}
	}
	if _, exists := after["mcp_servers"]; !exists && !images.Enabled {
		return data, nil
	}
	servers, err := configTable(after, "mcp_servers")
	if err != nil {
		return nil, err
	}
	raw, exists := servers[codexImagesServer]
	if exists && !managedCodexImages(raw) {
		if !images.Enabled {
			return data, nil
		}
		return nil, errors.New("MCP server kilo_images is already used by another configuration. Rename that server before enabling Kilo images; no files were changed")
	}
	if images.Enabled {
		servers[codexImagesServer] = map[string]any{
			"url":                  "http://127.0.0.1:" + strconv.Itoa(port) + "/mcp/images",
			"bearer_token_env_var": "KILO_LOCAL_API_KEY",
			"startup_timeout_sec":  int64(15),
			"tool_timeout_sec":     int64(360),
			"enabled":              true,
		}
	} else {
		delete(servers, codexImagesServer)
	}
	return editCodexTOML(data, before, after)
}

// Caller holds a.mu. Profile and app settings are validated together, backed
// up, and restored on an ordinary write failure. As with profile preparation,
// this is not a crash-atomic transaction across multiple files.
func (a *app) saveCodexImageProfile(dir string, catalog []byte, draft *imageGenerationSettings) (bool, bool, error) {
	images := a.config.ImageGeneration
	if draft != nil {
		images = *draft
	}
	if err := validateImageGenerationSettings(images); err != nil {
		return false, false, err
	}
	var extra []profileFile
	cfg := a.config
	cfg.ImageGeneration = images
	if images != a.config.ImageGeneration {
		if err := os.MkdirAll(a.dir, 0700); err != nil {
			return false, false, errors.New("Cannot create the Kilo Proxy settings directory")
		}
		info, err := os.Lstat(a.dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false, false, errors.New("Kilo Proxy settings must use a real directory")
		}
		body, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return false, false, err
		}
		file, err := prepareProfileFile(filepath.Join(a.dir, "settings.json"), body)
		if err != nil {
			return false, false, err
		}
		extra = append(extra, file)
	}
	configChanged, catalogChanged, err := saveCodexProfileOptions(dir, catalog, a.config.Port, "", &images, extra)
	if err == nil {
		a.config = cfg
	}
	return configChanged, catalogChanged, err
}
