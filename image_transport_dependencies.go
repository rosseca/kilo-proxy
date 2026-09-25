package main

import "runtime"

const imageCloudflareInstallURL = "https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/downloads/"

type imageTransportDependency struct {
	Tool           string `json:"tool"`
	Required       bool   `json:"required"`
	Installed      bool   `json:"installed"`
	InstallCommand string `json:"installCommand,omitempty"`
	InstallURL     string `json:"installURL"`
}

// Each snapshot repeats static executable discovery outside a.mu so a slow PATH
// does not block inference. No process or tunnel is started; finding the
// executable does not assert that a public tunnel will work.
func (a *app) imageTransportDependencySnapshot() imageTransportDependency {
	a.mu.Lock()
	settings, lookup := a.config.ImageTransport, a.imageDependencyLookup
	a.mu.Unlock()
	return imageTransportDependencyFor(settings, runtime.GOOS, lookup)
}

func imageTransportDependencyFor(settings imageTransportSettings, platform string, lookup func(string) string) imageTransportDependency {
	if lookup == nil {
		lookup = imageURLFindExecutable
	}
	dependency := imageTransportDependency{
		Tool:       "cloudflared",
		Required:   normalizeImageTransportSettings(settings).Mode == "cloudflare",
		Installed:  lookup("cloudflared") != "",
		InstallURL: imageCloudflareInstallURL,
	}
	switch platform {
	case "darwin", "macos":
		dependency.InstallCommand = "brew install cloudflared"
	case "windows":
		dependency.InstallCommand = "winget install --id Cloudflare.cloudflared --exact"
	}
	return dependency
}
