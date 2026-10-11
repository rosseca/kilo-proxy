package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type t3CodeInstallation struct {
	Version             string
	SourceDigest        string
	ProtocolV2          bool
	ClaudeModelDefaults bool
	ClaudeBuiltins      []string
}

type t3CodePackageMetadata struct{ Name, Version, Main string }
type t3CodeArchive struct {
	file       *os.File
	tree       t3CodeASAREntry
	base, size int64
	header     []byte
}

func t3CodeOpenPackage(executable, platform string) (*t3CodeArchive, error) {
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	root := filepath.Dir(executable)
	if (platform == "darwin" || platform == "macos") && strings.HasSuffix(executable, ".app") {
		root = filepath.Join(executable, "Contents")
	}
	for _, resources := range []string{"Resources", "resources"} {
		if archive, err := t3CodeOpenASAR(filepath.Join(root, resources, "app.asar")); err == nil {
			return archive, nil
		}
	}
	return nil, errors.New("Cannot read the installed T3 Code desktop package.")
}

func t3CodeOpenASAR(path string) (*t3CodeArchive, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fail := func() (*t3CodeArchive, error) { file.Close(); return nil, errors.New("Invalid T3 Code package index.") }
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return fail()
	}
	var prefix [16]byte
	if _, err := io.ReadFull(file, prefix[:]); err != nil {
		return fail()
	}
	headerSize, jsonSize := int64(binary.LittleEndian.Uint32(prefix[4:8])), int64(binary.LittleEndian.Uint32(prefix[12:16]))
	if binary.LittleEndian.Uint32(prefix[:4]) != 4 || headerSize < 8 || headerSize > 8<<20 || jsonSize < 2 || jsonSize > headerSize-8 || headerSize+8 > info.Size() {
		return fail()
	}
	header := make([]byte, jsonSize)
	if _, err := io.ReadFull(file, header); err != nil {
		return fail()
	}
	archive := &t3CodeArchive{file: file, base: headerSize + 8, size: info.Size(), header: header}
	if json.Unmarshal(header, &archive.tree) != nil {
		return fail()
	}
	return archive, nil
}

func (a *t3CodeArchive) read(path string, limit int64) ([]byte, error) {
	// ASAR paths always use '/', on every host. Reject drive/URI spellings too.
	if path == "" || strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\\:\x00\r\n") {
		return nil, errors.New("Unsafe T3 Code package entry.")
	}
	entry := a.tree
	for _, component := range strings.Split(path, "/") {
		if component == "" || component == "." || component == ".." || entry.Link != "" {
			return nil, errors.New("Unsafe T3 Code package entry.")
		}
		var ok bool
		entry, ok = entry.Files[component]
		if !ok {
			return nil, errors.New("Missing T3 Code package entry.")
		}
	}
	offset, err := strconv.ParseInt(entry.Offset, 10, 64)
	if err != nil || offset < 0 || entry.Size < 1 || entry.Size > limit || entry.Unpacked || entry.Link != "" || entry.Files != nil || offset > a.size-a.base || entry.Size > a.size-a.base-offset {
		return nil, errors.New("Invalid T3 Code package entry.")
	}
	data := make([]byte, entry.Size)
	_, err = a.file.ReadAt(data, a.base+offset)
	return data, err
}

func (a *t3CodeArchive) metadata() (t3CodePackageMetadata, error) {
	data, err := a.read("package.json", 64<<10)
	var metadata t3CodePackageMetadata
	if err != nil || json.Unmarshal(data, &metadata) != nil || metadata.Name != "t3code" || !t3CodeVersionValid(metadata.Version) {
		return metadata, errors.New("Cannot verify the T3 Code desktop package metadata.")
	}
	return metadata, nil
}

func (a *t3CodeArchive) sources(prefix string, limit int64) ([]byte, error) {
	entry := a.tree
	for _, component := range strings.Split(strings.TrimSuffix(prefix, "/"), "/") {
		entry = entry.Files[component]
	}
	names := []string{}
	// The desktop main and server entry bundles live directly in these folders;
	// dependencies and web assets are neither executed nor inspected.
	for name, entry := range entry.Files {
		if entry.Files == nil && (strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".cjs") || strings.HasSuffix(name, ".mjs")) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var result []byte
	for _, name := range names {
		data, err := a.read(prefix+name, limit-int64(len(result))-int64(len(name))-2)
		if err != nil {
			return nil, err
		}
		result = append(result, name...)
		result = append(result, '\n')
		result = append(result, data...)
		result = append(result, '\n')
	}
	if len(result) == 0 {
		return nil, errors.New("Missing T3 Code desktop or server sources.")
	}
	return result, nil
}

var t3CodePackagedClaudeSlug = regexp.MustCompile(`["'](claude-[A-Za-z0-9][A-Za-z0-9_.-]*)["']`)

// Accept release channels by identity and the installed settings contract, not
// by a version, commit or digest allowlist. These source checks are an early
// compatibility guard; they cannot guarantee arbitrary future upstream changes.
func inspectT3CodeInstallation(executable, platform string) (t3CodeInstallation, error) {
	var result t3CodeInstallation
	archive, err := t3CodeOpenPackage(executable, platform)
	if err != nil {
		return result, err
	}
	defer archive.file.Close()
	metadata, err := archive.metadata()
	if err != nil {
		return result, err
	}
	result.Version = metadata.Version
	incompatible := errors.New("This T3 Code installation has an incompatible private-workspace or provider settings contract. Update T3 Code or Kilo Proxy and prepare again; no private settings were changed.")
	main, err := archive.read(metadata.Main, 16<<20)
	if err != nil {
		return result, incompatible
	}
	desktop, err := archive.sources("apps/desktop/dist-electron/", 24<<20)
	if err != nil {
		return result, incompatible
	}
	server, err := archive.sources("apps/server/dist/", 32<<20)
	if err != nil {
		return result, incompatible
	}
	contains := func(data []byte, tokens ...string) bool {
		source := string(data)
		for _, token := range tokens {
			if !strings.Contains(source, token) {
				return false
			}
		}
		return true
	}
	if !contains(desktop, "T3CODE_HOME", "T3CODE_DISABLE_AUTO_UPDATE", "userData", "client-settings.json", "providerModelPreferences", "hiddenModels", "modelOrder") || !contains(server, "providerInstances", "homePath", "binaryPath", "customModels", "claudeAgent", "valueRedacted", "provider-env-", "base64url", "secretsDir", ".bin", "settings.json", "server-runtime.json", "CODEX_HOME", "CLAUDE_CONFIG_DIR") {
		return result, incompatible
	}
	if contains(server, "message.dispatch") != contains(server, "provider-session.detach") {
		return result, incompatible
	}
	result.ProtocolV2 = contains(server, "message.dispatch", "provider-session.detach")
	if !result.ProtocolV2 && !contains(server, "thread.turn.start") {
		return result, incompatible
	}
	// V2 resolves Claude effort through the builtin catalog. For exact custom
	// gateway IDs use the CLI's per-model defaults instead of an ineffective picker.
	result.ClaudeModelDefaults = result.ProtocolV2
	seen := map[string]bool{}
	for _, match := range t3CodePackagedClaudeSlug.FindAllSubmatch(server, -1) {
		slug := string(match[1])
		if catalogID.MatchString(slug) && !seen[slug] {
			result.ClaudeBuiltins = append(result.ClaudeBuiltins, slug)
			seen[slug] = true
		}
	}
	sort.Strings(result.ClaudeBuiltins)
	// Include the index, metadata and both source sets: an in-place update with
	// an unchanged version must also invalidate a prepared workspace.
	data, _ := json.Marshal([]any{string(archive.header), metadata, openDesignHash(main), openDesignHash(desktop), openDesignHash(server)})
	result.SourceDigest = openDesignHash(data)
	return result, nil
}

func t3CodeInstallationName(name string) bool {
	normalized := strings.NewReplacer(" ", "", "-", "", "_", "").Replace(strings.ToLower(name))
	return strings.HasPrefix(normalized, "t3code")
}

func t3CodeDiscoveredCandidates(platform, home, localAppData string) []string {
	roots := []string{"/Applications", filepath.Join(home, "Applications")}
	if platform == "windows" {
		roots = []string{filepath.Join(localAppData, "Programs"), localAppData, os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")}
	}
	if platform != "windows" && platform != "darwin" && platform != "macos" {
		roots = []string{"/opt", filepath.Join(home, ".local", "share"), filepath.Join(home, "Applications")}
	}
	result := []string{}
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !t3CodeInstallationName(entry.Name()) {
				continue
			}
			path := filepath.Join(root, entry.Name())
			if platform == "darwin" || platform == "macos" {
				if strings.HasSuffix(strings.ToLower(entry.Name()), ".app") {
					result = append(result, path)
				}
			} else if platform == "windows" {
				for _, name := range []string{entry.Name() + ".exe", "T3 Code.exe", "t3code.exe", "t3-code.exe"} {
					result = append(result, filepath.Join(path, name))
				}
			} else {
				for _, name := range []string{entry.Name(), "t3code", "t3-code"} {
					result = append(result, filepath.Join(path, name))
				}
			}
		}
	}
	if platform != "darwin" && platform != "macos" {
		// Native package-manager and portable installs may expose a channel-
		// suffixed executable through PATH rather than an application folder.
		for _, root := range filepath.SplitList(os.Getenv("PATH")) {
			if !filepath.IsAbs(root) {
				continue
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if !t3CodeInstallationName(entry.Name()) || platform == "windows" && !strings.HasSuffix(strings.ToLower(entry.Name()), ".exe") {
					continue
				}
				path := filepath.Join(root, entry.Name())
				if launchExecutable(path) {
					result = append(result, path)
				}
			}
		}
	}
	return result
}
