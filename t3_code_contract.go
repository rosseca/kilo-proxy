package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type t3CodeInstallation struct {
	Version               string
	SourceDigest          string
	ProtocolV2            bool
	ClaudeModelDefaults   bool
	ClaudeBuiltins        []string
	ClaudeCatalogSlugs    []string
	SupportedProviders    []t3CodeProviderSupport
	OpenCode2Supported    bool
	OpenCodeKiloSupported bool
	MuseKiloSupported     bool
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
// Only contract analysis is cached. The package index, metadata and every
// inspected source are read and hashed before this cache can be consulted.
// One entry bounds memory, and the independent mutex shares analysis across
// simultaneous requests without holding app.mu.
type t3CodeInspectionCache struct {
	mu     sync.Mutex
	digest string
	result t3CodeInstallation
	valid  bool
}

var t3CodeInstalledAnalysis t3CodeInspectionCache

func cloneT3CodeInstallation(value t3CodeInstallation) t3CodeInstallation {
	value.ClaudeBuiltins = slices.Clone(value.ClaudeBuiltins)
	value.ClaudeCatalogSlugs = slices.Clone(value.ClaudeCatalogSlugs)
	value.SupportedProviders = slices.Clone(value.SupportedProviders)
	return value
}

func (c *t3CodeInspectionCache) get(digest string, analyze func() (t3CodeInstallation, error)) (t3CodeInstallation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.valid && c.digest == digest {
		return cloneT3CodeInstallation(c.result), nil
	}
	result, err := analyze()
	if err == nil {
		c.digest, c.result, c.valid = digest, cloneT3CodeInstallation(result), true
	}
	return cloneT3CodeInstallation(result), err
}

func inspectT3CodeInstallation(executable, platform string) (t3CodeInstallation, error) {
	return inspectT3CodeInstallationWithCache(executable, platform, &t3CodeInstalledAnalysis)
}

func inspectT3CodeInstallationWithCache(executable, platform string, cache *t3CodeInspectionCache) (t3CodeInstallation, error) {
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
	// Include the index, metadata and both source sets: an in-place update
	// with the same version, size and timestamps must invalidate analysis.
	data, _ := json.Marshal([]any{string(archive.header), metadata, openDesignHash(main), openDesignHash(desktop), openDesignHash(server)})
	digest := openDesignHash(data)
	return cache.get(digest, func() (t3CodeInstallation, error) {
		return analyzeT3CodeInstallation(metadata, desktop, server, digest)
	})
}

func analyzeT3CodeInstallation(metadata t3CodePackageMetadata, desktop, server []byte, digest string) (t3CodeInstallation, error) {
	result := t3CodeInstallation{Version: metadata.Version}
	incompatible := errors.New("This T3 Code installation has an incompatible private-workspace or provider settings contract. Update T3 Code or Kilo Proxy and prepare again; no private settings were changed.")
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
	result.SupportedProviders, result.OpenCode2Supported = t3CodeOptionalProviderCapabilities(string(server))
	result.OpenCodeKiloSupported = t3CodeOpenCodeKiloSupported(string(server))
	result.MuseKiloSupported = t3CodeMuseKiloSupported(string(server))
	seen := map[string]bool{}
	for _, match := range t3CodePackagedClaudeSlug.FindAllSubmatch(server, -1) {
		slug := string(match[1])
		if catalogID.MatchString(slug) && !seen[slug] {
			result.ClaudeBuiltins = append(result.ClaudeBuiltins, slug)
			seen[slug] = true
		}
	}
	sort.Strings(result.ClaudeBuiltins)
	result.ClaudeCatalogSlugs = t3CodeBundledClaudeSlugs(server)
	result.SourceDigest = digest
	return result, nil
}

// A provider name alone is not a capability: older bundles can contain UI
// labels without shipping the corresponding server driver. Require its bound
// driver kind, settings schema, factory and inclusion in the live registry.
func t3CodeOptionalProviderCapabilities(source string) ([]t3CodeProviderSupport, bool) {
	providers := []t3CodeProviderSupport{}
	registryPattern := regexp.MustCompile(`(?s)const\s+BUILT_IN_DRIVERS\s*=\s*\[(.*?)\n\];`)
	registry := registryPattern.FindStringSubmatch(source)
	if len(registry) != 2 {
		return providers, false
	}
	for _, provider := range t3CodeOptionalProviders {
		name := map[string]string{"cursor": "Cursor", "grok": "Grok", "opencode": "OpenCode", "antigravity": "Antigravity", "muse": "Muse", "pi": "Pi"}[provider.ID]
		bindings := regexp.MustCompile(`const\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*ProviderDriverKind\.make\(["']`+regexp.QuoteMeta(provider.ID)+`["']\);`).FindAllStringSubmatch(source, -1)
		for _, binding := range bindings {
			body := source
			if provider.ID == "pi" {
				body = registry[1]
			} else {
				if !regexp.MustCompile(`\b` + name + `Driver\s*,`).MatchString(registry[1]) {
					continue
				}
				declaration := regexp.MustCompile(`const\s+` + name + `Driver\s*=\s*\{`).FindStringIndex(source)
				if len(declaration) != 2 {
					continue
				}
				body = source[declaration[1]:]
				if len(body) > 1200 {
					body = body[:1200]
				}
			}
			contract := regexp.MustCompile(`(?s)driverKind:\s*` + regexp.QuoteMeta(binding[1]) + `\s*,.{0,250}configSchema:\s*` + name + `Settings\s*,.{0,180}defaultConfig:.*?create:`)
			if contract.MatchString(body) {
				providers = append(providers, provider)
				break
			}
		}
	}
	openCode := false
	for _, provider := range providers {
		openCode = openCode || provider.ID == "opencode"
	}
	for _, marker := range []string{"classifyOpenCodeCliVersion", "selectOpenCodeRuntimeAdapter", "selectOpenCodeRuntimeTextGeneration", "decodeApiInfo", `"/api/info"`, `"@opencode/cli"`} {
		openCode = openCode && strings.Contains(source, marker)
	}
	return providers, openCode
}

// The native drivers used an arrow factory before adding Effect tracing.
// Inspect only the live create property, after strings/comments were masked:
// the traced factory's static label is therefore whitespace. Both forms must
// bind the instance environment directly; an alias or a rest object is not proof.
func t3CodeDriverHasInstanceEnvironment(body string) bool {
	factories := regexp.MustCompile(`\bcreate\s*:\s*`).FindAllStringIndex(body, -1)
	if len(factories) != 1 {
		return false
	}
	factory := body[factories[0][1]:]
	parameters := `\(\s*\{\s*(?:[^{}]*,\s*)?environment\s*(?:,[^{}]*)?\}\s*\)`
	if regexp.MustCompile(`^` + parameters + `\s*=>`).MatchString(factory) {
		return true
	}
	return regexp.MustCompile(`^(?:Effect\.)?fn(?:\$[0-9]+)?\(\s*\)\(\s*function\s*\*\s*` + parameters + `\s*\{`).MatchString(factory)
}

// A separate Kilo account needs more than an OpenCode label or a version probe:
// its live driver must allow multiple instances and pass each instance's own
// environment to both runtimes. Match the installed source contract, not a
// release/version allowlist. Unknown upstream shapes remain unavailable.
func t3CodeOpenCodeKiloSupported(source string) bool {
	providers, _ := t3CodeOptionalProviderCapabilities(source)
	found := false
	for _, provider := range providers {
		found = found || provider.ID == "opencode"
	}
	if !found {
		return false
	}
	code := t3CodeContractCode(source)
	registry := regexp.MustCompile(`(?s)const\s+BUILT_IN_DRIVERS\s*=\s*\[(.*?)\n\];`).FindAllStringSubmatch(code, -1)
	if len(registry) != 1 || !regexp.MustCompile(`\bOpenCodeDriver\s*,`).MatchString(registry[0][1]) {
		return false
	}
	declarations := regexp.MustCompile(`(?m)^const\s+OpenCodeDriver\s*=\s*\{`).FindAllStringIndex(code, -1)
	if len(declarations) != 1 {
		return false
	}
	body, ok := t3CodeContractBody(code, declarations[0][1]-1)
	if !ok || !regexp.MustCompile(`(?s)metadata:\s*\{[^{}]*\bsupportsMultipleInstances:\s*true\b`).MatchString(body) || !regexp.MustCompile(`configSchema:\s*OpenCodeSettings\s*,`).MatchString(body) || !t3CodeDriverHasInstanceEnvironment(body) {
		return false
	}
	kind := regexp.MustCompile(`driverKind:\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*,`).FindStringSubmatch(body)
	if len(kind) != 2 {
		return false
	}
	kindPattern := regexp.MustCompile(`const\s+` + regexp.QuoteMeta(kind[1]) + `\s*=\s*ProviderDriverKind\.make\(["']opencode["']\);`)
	liveKinds := 0
	for _, binding := range kindPattern.FindAllStringIndex(source, -1) {
		if strings.HasPrefix(code[binding[0]:], "const") {
			liveKinds++
		}
	}
	if liveKinds != 1 {
		return false
	}
	merged := regexp.MustCompile(`const\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*yield\*\s+(mergeProviderInstanceEnvironment[A-Za-z0-9_$]*)\(environment\)`).FindStringSubmatch(body)
	if len(merged) != 3 {
		return false
	}
	env := regexp.QuoteMeta(merged[1])
	if !t3CodeOpenCodeProbesInstanceEnvironment(source, code, body, env) {
		return false
	}
	for _, runtime := range []string{"serverOwner", "openCode2Server"} {
		calls := regexp.MustCompile(`const\s+`+runtime+`\s*=\s*yield\*\s+[A-Za-z_$][A-Za-z0-9_$]*(?:\.[A-Za-z_$][A-Za-z0-9_$]*)?\(\{`).FindAllStringIndex(body, -1)
		if len(calls) != 1 {
			return false
		}
		options, valid := t3CodeContractBody(body, calls[0][1]-1)
		if !valid || !regexp.MustCompile(`\benvironment:\s*`+env+`\b`).MatchString(options) {
			return false
		}
	}
	return t3CodeContractCopiesInstanceEnvironment(source, code, merged[2])
}

// Verify the direct legacy probe or the newer status-probe wrapper and
// per-instance fallback. The status wrapper only adds MISE_OFFLINE.
func t3CodeOpenCodeProbesInstanceEnvironment(source, code, body, env string) bool {
	if regexp.MustCompile(`probeOpenCodeRuntime\([^,]+,\s*` + env + `\)`).MatchString(body) {
		return true
	}
	probes := regexp.MustCompile(`\bconst\s+probeRuntime\s*=\s*`).FindAllStringIndex(body, -1)
	if len(probes) != 1 || !regexp.MustCompile(`^\(probeEnv\)\s*=>\s*probeOpenCodeRuntime\(effectiveConfig,\s*probeEnv\)\.pipe\(`).MatchString(body[probes[0][1]:]) {
		return false
	}
	wrapped := regexp.MustCompile(`\bmakeOpenCodeRuntimeProbe\(\s*probeRuntime\(\s*(providerProbeEnvironment(?:\$[0-9]+)?)\(\s*`+env+`\s*\)\s*\)\s*\)`).FindAllStringSubmatch(body[probes[0][1]:], -1)
	if len(wrapped) != 1 || !regexp.MustCompile(`\bonNone:\s*\(\)\s*=>\s*probeRuntime\(\s*`+env+`\s*\)\.pipe\(`).MatchString(body[probes[0][1]:]) {
		return false
	}
	helpers := regexp.MustCompile(`(?m)^function\s+`+regexp.QuoteMeta(wrapped[0][1])+`\s*\(`).FindAllStringIndex(code, -1)
	if len(helpers) != 1 {
		return false
	}
	start := helpers[0][1]
	match := regexp.MustCompile(`^environment\)\s*\{\s*return\s*\{\s*\.\.\.environment,\s*MISE_OFFLINE:\s*"1"\s*,?\s*\}\s*;\s*\}`).FindString(source[start:])
	return match != "" && code[start:start+len(match)] == t3CodeContractCode(match)
}

// Verify the newer home expansion helper, including the exact native-home
// allowlist. All other environment keys must pass through unchanged.
func t3CodeContractHomeVariableForwarded(source, code, helper string) bool {
	helpers := regexp.MustCompile(`(?m)^function\s+`+regexp.QuoteMeta(helper)+`\s*\(`).FindAllStringIndex(code, -1)
	if len(helpers) != 1 {
		return false
	}
	definition := regexp.MustCompile(`^name,\s*value,\s*home\)\s*\{\s*return\s+([A-Za-z_$][A-Za-z0-9_$]*)\.has\(name\)\s*\?\s*expandHomePath(?:\$[0-9]+)?\(value,\s*home\)\s*:\s*value\s*;\s*\}`).FindStringSubmatch(code[helpers[0][1]:])
	if len(definition) != 2 {
		return false
	}
	set := regexp.QuoteMeta(definition[1])
	sets := regexp.MustCompile(`(?m)^const\s+`+set+`\s*=\s*`).FindAllStringIndex(code, -1)
	references := regexp.MustCompile(`(?:^|[^A-Za-z0-9_$])`+set+`(?:$|[^A-Za-z0-9_$])`).FindAllStringIndex(code, -1)
	if len(sets) != 1 || sets[0][0] >= helpers[0][0] || len(references) != 2 {
		return false
	}
	start := sets[0][1]
	match := regexp.MustCompile(`^new Set\(\[\s*"CODEX_HOME",\s*"CLAUDE_CONFIG_DIR",\s*"GROK_HOME"\s*,?\s*\]\)\s*;`).FindString(source[start:])
	return match != "" && code[start:start+len(match)] == t3CodeContractCode(match)
}

// Require an assignment on the copied instance environment. Match direct
// forwarding, the inline native-home expansion, or the verified helper.
func t3CodeContractCopiesInstanceEnvironment(source, code, merger string) bool {
	mergers := regexp.MustCompile(`const\s+`+regexp.QuoteMeta(merger)+`\s*=\s*(?:[A-Za-z_$][A-Za-z0-9_$]*\.)?fn[A-Za-z0-9_$]*\(function\*\s*\(environment,\s*baseEnv\s*=\s*process\.env\)\s*\{`).FindAllStringIndex(code, -1)
	if len(mergers) != 1 {
		return false
	}
	body, ok := t3CodeContractBody(code, mergers[0][1]-1)
	if !ok {
		return false
	}
	copy := regexp.MustCompile(`const\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*\{\s*\.\.\.baseEnv\s*\}`).FindStringSubmatch(body)
	loop := regexp.MustCompile(`for\s*\(const\s+([A-Za-z_$][A-Za-z0-9_$]*)\s+of\s+environment\)`).FindStringSubmatch(body)
	if len(copy) != 2 || len(loop) != 2 {
		return false
	}
	rawBody := source[mergers[0][1]-1 : mergers[0][1]-1+len(body)]
	next, variable := copy[1], loop[1]
	nextPattern, variablePattern := regexp.QuoteMeta(next), regexp.QuoteMeta(variable)
	if !regexp.MustCompile(`\breturn\s+` + nextPattern + `\b`).MatchString(body) {
		return false
	}
	// RE2 has no lookbehind. The boundary excludes longer identifiers and
	// property accesses such as other.next.
	const boundary = `(?:^|[^.A-Za-z0-9_$\s])\s*`
	keySource := variablePattern + `\.name`
	assignments := regexp.MustCompile(boundary + `(` + nextPattern + `\[(` + keySource + `|[A-Za-z_$][A-Za-z0-9_$]*)\]\s*=\s*)`)
	for _, match := range assignments.FindAllStringSubmatchIndex(body, -1) {
		key := body[match[4]:match[5]]
		keyPattern := regexp.QuoteMeta(key)
		if key != variable+".name" {
			initializer := `(?:` + keySource + `|[A-Za-z_$][A-Za-z0-9_$]*\s*\?\s*` + keySource + `\.toUpperCase\(\)\s*:\s*` + keySource + `)`
			declaration := regexp.MustCompile(boundary + `(?:const|let|var)\s+` + keyPattern + `\s*=\s*` + initializer + `\s*;`)
			if !declaration.MatchString(body[:match[2]]) {
				continue
			}
		}

		value := variablePattern + `\.value`
		homeExpansion := keyPattern + `\s*===\s*"CODEX_HOME"\s*\|\|\s*` +
			keyPattern + `\s*===\s*"CLAUDE_CONFIG_DIR"\s*\?\s*expandHomePath\(` +
			value + `,\s*home\)\s*:\s*` + value
		start := match[1]
		// Read the exact home-name literals from the raw body. The assignment
		// prefix was matched in live code, and masking the RHS must agree with
		// the same bytes in the surrounding code.
		rhs := regexp.MustCompile(`^(?:` + value + `|` + homeExpansion + `)\s*;`).FindString(rawBody[start:])
		if rhs != "" && body[start:start+len(rhs)] == t3CodeContractCode(rhs) {
			return true
		}
		call := regexp.MustCompile(`^(expandHomeVariable(?:\$[0-9]+)?)\(\s*` + keyPattern + `,\s*` + value + `,\s*home\s*\)\s*;`).FindStringSubmatch(body[start:])
		if len(call) == 2 && t3CodeContractHomeVariableForwarded(source, code, call[1]) {
			return true
		}
	}
	return false
}

// Extract one bounded brace body after masking strings and comments. The
// same mask is also applied globally before locating live declarations, so a
// complete commented or quoted example cannot advertise a capability.
// These known driver/merge bodies contain no regular-expression literals.
func t3CodeContractBody(source string, start int) (string, bool) {
	if start < 0 || start >= len(source) || source[start] != '{' {
		return "", false
	}
	end := start + (512 << 10)
	if end > len(source) {
		end = len(source)
	}
	code := t3CodeContractCode(source[start:end])
	depth := 0
	for i, ch := range code {
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return code[:i+1], true
			}
		}
	}
	return "", false
}

func t3CodeContractCode(source string) string {
	code := []byte(source)
	var quote byte
	lineComment, blockComment := false, false
	for i, ch := range code {
		switch {
		case lineComment:
			if ch == '\n' {
				lineComment = false
			} else {
				code[i] = ' '
			}
		case blockComment:
			code[i] = ' '
			if ch == '/' && i > 0 && source[i-1] == '*' {
				blockComment = false
			}
		case quote != 0:
			code[i] = ' '
			// An ordinary JavaScript string cannot cross an unescaped line
			// ending. Recover here if a quote inside a regular expression
			// looked like a string, while preserving templates and continuations.
			lineEnd := quote != '`' && (ch == '\r' || ch == '\n')
			if ch == quote || lineEnd {
				j := i - 1
				if ch == '\n' && j >= 0 && source[j] == '\r' {
					j--
				}
				escapes := 0
				for ; j >= 0 && source[j] == '\\'; j-- {
					escapes++
				}
				if escapes%2 == 0 {
					quote = 0
					if lineEnd {
						code[i] = ch
					}
				}
			}
		case ch == '"' || ch == '\'' || ch == '`':
			quote, code[i] = ch, ' '
		case ch == '/' && i+1 < len(code) && code[i+1] == '/':
			lineComment, code[i] = true, ' '
		case ch == '/' && i+1 < len(code) && code[i+1] == '*':
			blockComment, code[i] = true, ' '
		}
	}
	return string(code)
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

// A Kilo Muse instance needs the live native driver to carry its private
// environment into discovery, interactive sessions and background generation.
// Labels, commented examples and a generic multiple-instance flag are not enough.
func t3CodeMuseKiloSupported(source string) bool {
	providers, _ := t3CodeOptionalProviderCapabilities(source)
	found := false
	for _, provider := range providers {
		found = found || provider.ID == "muse"
	}
	if !found {
		return false
	}
	code := t3CodeContractCode(source)
	registry := regexp.MustCompile(`(?s)const\s+BUILT_IN_DRIVERS\s*=\s*\[(.*?)\n\];`).FindAllStringSubmatch(code, -1)
	if len(registry) != 1 || !regexp.MustCompile(`\bMuseDriver\s*,`).MatchString(registry[0][1]) {
		return false
	}
	declarations := regexp.MustCompile(`(?m)^const\s+MuseDriver\s*=\s*\{`).FindAllStringIndex(code, -1)
	if len(declarations) != 1 {
		return false
	}
	body, ok := t3CodeContractBody(code, declarations[0][1]-1)
	if !ok || !regexp.MustCompile(`(?s)metadata:\s*\{[^{}]*\bsupportsMultipleInstances:\s*true\b`).MatchString(body) || !regexp.MustCompile(`configSchema:\s*MuseSettings\s*,`).MatchString(body) || !t3CodeDriverHasInstanceEnvironment(body) {
		return false
	}
	kind := regexp.MustCompile(`driverKind:\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*,`).FindStringSubmatch(body)
	if len(kind) != 2 {
		return false
	}
	kinds := regexp.MustCompile(`const\s+`+regexp.QuoteMeta(kind[1])+`\s*=\s*ProviderDriverKind\.make\(["']muse["']\);`).FindAllStringIndex(source, -1)
	liveKinds := 0
	for _, binding := range kinds {
		if strings.HasPrefix(code[binding[0]:], "const") {
			liveKinds++
		}
	}
	if liveKinds != 1 {
		return false
	}
	merged := regexp.MustCompile(`const\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*yield\*\s+(mergeProviderInstanceEnvironment[A-Za-z0-9_$]*)\(environment,\s*makeMuseEnvironment\(hostEnvironment\)\)`).FindStringSubmatch(body)
	if len(merged) != 3 {
		return false
	}
	env := regexp.QuoteMeta(merged[1])
	if !regexp.MustCompile(`checkMuseProviderStatus\([^,]+,\s*` + env + `,\s*cwd\)`).MatchString(body) {
		return false
	}
	for _, call := range []string{`makeMuseAdapterV2\(\{`, `makeMuseTextGeneration\([^,]+,\s*\{`} {
		calls := regexp.MustCompile(call).FindAllStringIndex(body, -1)
		if len(calls) != 1 {
			return false
		}
		options, valid := t3CodeContractBody(body, calls[0][1]-1)
		if !valid || !regexp.MustCompile(`\benvironment:\s*`+env+`\b`).MatchString(options) {
			return false
		}
	}
	return t3CodeContractCopiesInstanceEnvironment(source, code, merged[2])
}
