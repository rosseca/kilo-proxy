package main

import "net/http"

// Claude Desktop's gateway probe uses Electron session.fetch from the main
// process. Chromium adds this metadata even though no web origin initiates it.
// This exception grants no authentication or CORS permission; the caller still
// requires the local bearer key and rejects every browser Origin.
func claudeDesktopMessagesRequest(r *http.Request, host string) bool {
	if r.Host != host || r.Method != http.MethodPost || r.URL.Path != "/v1/messages" || r.URL.RawPath != "" || r.URL.RawQuery != "" || len(r.Header.Values("Origin")) != 0 {
		return false
	}
	for name, expected := range map[string]string{
		"Sec-Fetch-Site": "none",
		"Sec-Fetch-Mode": "no-cors",
		"Sec-Fetch-Dest": "empty",
	} {
		values := r.Header.Values(name)
		if len(values) != 1 || values[0] != expected {
			return false
		}
	}
	return true
}
