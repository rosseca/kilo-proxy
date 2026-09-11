package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type openDesignManagedPaths struct {
	Root, NamespaceBase, Data, Runtime, Profiles, Config, Namespace string
}

func openDesignNamespace(appDir string) string {
	absolute, err := filepath.Abs(appDir)
	if err != nil {
		absolute = filepath.Clean(appDir)
	}
	digest := sha256.Sum256([]byte(filepath.Clean(absolute)))
	return "kilo-proxy-" + hex.EncodeToString(digest[:])[:12]
}

func openDesignProfilePaths(appDir string) openDesignManagedPaths {
	root := filepath.Join(appDir, "open-design")
	namespace := openDesignNamespace(appDir)
	base := filepath.Join(root, "namespaces")
	data := filepath.Join(base, namespace, "data")
	return openDesignManagedPaths{
		Root: root, NamespaceBase: base, Data: data,
		Runtime:  filepath.Join(base, namespace, "runtime"),
		Profiles: filepath.Join(root, "profiles"),
		Config:   filepath.Join(data, "app-config.json"), Namespace: namespace,
	}
}

func configureOpenDesignLaunch(plan *clientLaunchPlan, appDir, platform string) error {
	if plan == nil || !filepath.IsAbs(appDir) || strings.ContainsAny(appDir, "\x00\r\n") {
		return errors.New("Cannot locate the managed Open Design profile.")
	}
	if reason := launchClientPlatformReason("open-design", platform); reason != "" {
		return errors.New(reason)
	}
	if err := openDesignCompatibility(plan.Executable, platform); err != nil {
		return err
	}
	if platform == "macos" || platform == "darwin" {
		if strings.HasSuffix(plan.Executable, ".app") {
			plan.Executable = filepath.Join(plan.Executable, "Contents", "MacOS", "Open Design")
		}
		if !launchExecutable(plan.Executable) {
			return errors.New("The Open Design application does not exist or cannot be executed.")
		}
	}
	paths := openDesignProfilePaths(appDir)
	if plan.Env == nil {
		plan.Env = map[string]string{}
	}
	// Profile and process ownership overrides must be supplied by this launcher.
	// In particular an inherited supervised context wins over the namespace env.
	for _, name := range []string{
		"OD_DATA_DIR", "OD_PACKAGED_CONFIG_PATH", "OD_LEGACY_DATA_DIR",
		"OD_PACKAGED_ALLOW_WEB_OUTPUT_MODE_OVERRIDE", "OD_WEB_STANDALONE_ROOT", "OD_WEB_OUTPUT_MODE",
		"OD_SIDECAR_SUPERVISED_CONTEXT", "OD_SIDECAR_SUPERVISOR_TARGET", "OD_SIDECAR_RESOURCES", "OD_SIDECAR_CLIENT_ENDPOINT",
		"OD_SIDECAR_BASE", "OD_SIDECAR_IPC_BASE", "OD_SIDECAR_IPC_PATH", "OD_SIDECAR_NAMESPACE", "OD_SIDECAR_SOURCE",
		"ELECTRON_RUN_AS_NODE",
	} {
		delete(plan.Env, name)
		if !containsOpenDesignString(plan.Unset, name) {
			plan.Unset = append(plan.Unset, name)
		}
	}
	plan.Env["OD_PACKAGED_NAMESPACE"] = paths.Namespace
	plan.Env["OD_PACKAGED_NAMESPACE_BASE_ROOT"] = paths.NamespaceBase
	// Official update payloads are namespace-bound to release-stable. Update the
	// installed app normally; never apply that payload to the managed namespace.
	plan.Env["OD_UPDATE_ENABLED"] = "0"
	plan.Args = nil
	return nil
}

func containsOpenDesignString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

var openDesignNamespacePattern = regexp.MustCompile(`^kilo-proxy-[a-f0-9]{12}$`)

var errOpenDesignRunningCheck = errors.New("Cannot verify whether the managed Open Design workspace is closed. Close it and try again.")

// The initial desktop process exits after bootstrapping its supervisor. Its PID
// is therefore not a useful liveness marker. Inspect exact supervisor stamps and
// live private endpoints; Open Design writes no authoritative PID file.
func openDesignRunning(namespace string) (bool, error) {
	return openDesignRunningWith(namespace, openDesignProcessSnapshot, openDesignProbeRuntime)
}

func openDesignRunningWith(namespace string, snapshot func(context.Context) ([]string, error), probe func(context.Context, string) (bool, error)) (bool, error) {
	if !openDesignNamespacePattern.MatchString(namespace) {
		return false, errOpenDesignRunningCheck
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	commands, snapshotErr := snapshot(ctx)
	for _, command := range commands {
		if openDesignCommandMatches(command, namespace) {
			return true, nil
		}
	}
	running, probeErr := probe(ctx, namespace)
	if running {
		return true, nil
	}
	if snapshotErr != nil || probeErr != nil || ctx.Err() != nil {
		return false, errOpenDesignRunningCheck
	}
	return false, nil
}

func openDesignCommandMatches(command, namespace string) bool {
	fields := map[string]string{}
	for _, token := range strings.Fields(command) {
		token = strings.Trim(token, `"'`)
		if token == "--od-sidecar-lifecycle=launcher" {
			// A launcher can still be bringing this namespace up, so conservatively
			// block profile writes until its handoff or exit has completed.
			continue
		}
		for _, key := range []string{"channel", "namespace", "source", "mode", "app"} {
			if value, ok := strings.CutPrefix(token, "--od-stamp-"+key+"="); ok {
				fields[key] = value
			}
		}
	}
	if fields["namespace"] != namespace {
		return false
	}
	// An exact namespace in a partial startup stamp is also unsafe to overwrite.
	// The hash identifies this Kilo installation; no unrelated app is stopped.
	return true
}

type openDesignIPCStamp struct {
	Channel   string `json:"channel"`
	Namespace string `json:"namespace"`
	Source    string `json:"source"`
	Mode      string `json:"mode"`
	App       string `json:"app"`
}

func openDesignIPCStamps(namespace string) []openDesignIPCStamp {
	var stamps []openDesignIPCStamp
	for _, source := range []string{"packaged", "tools-pack"} {
		for _, mode := range []string{"runtime", "headless"} {
			for _, app := range []string{"desktop", "daemon", "web"} {
				stamps = append(stamps, openDesignIPCStamp{"stable", namespace, source, mode, app})
			}
		}
	}
	return stamps
}

func openDesignIPCDigest(principal string, stamp openDesignIPCStamp) string {
	key := principal + "\nchannel=" + stamp.Channel + "\nnamespace=" + stamp.Namespace + "\nsource=" + stamp.Source + "\nmode=" + stamp.Mode + "\napp=" + stamp.App
	digest := sha256.Sum256([]byte(key))
	return hex.EncodeToString(digest[:])[:32]
}

// Keep process details in memory only, with a hard output bound. Command errors
// never include captured argv or stderr, which can contain other apps' secrets.
func openDesignCommandOutput(ctx context.Context, executable string, args []string, environment []string) ([]byte, error) {
	command := exec.CommandContext(ctx, executable, args...)
	hideOpenDesignProbeWindow(command)
	command.Env = environment
	output := &openDesignLimitedOutput{limit: 16 << 20}
	command.Stdout = output
	if err := command.Run(); err != nil || output.exceeded {
		return nil, errOpenDesignRunningCheck
	}
	return output.Bytes(), nil
}

type openDesignLimitedOutput struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (output *openDesignLimitedOutput) Write(data []byte) (int, error) {
	if len(data) > output.limit-output.Len() {
		output.exceeded = true
		return 0, errors.New("process snapshot exceeds its size limit")
	}
	return output.Buffer.Write(data)
}
