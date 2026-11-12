package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const t3CodeRuntimeRevision = 6
const t3CodeRuntimeFileLimit int64 = 32 << 20

// T3 Code bundles office rendering WebAssembly and fonts in its web assets.
const t3CodeRuntimeTotalLimit int64 = 192 << 20

// The original Electron executable, ASAR, server and conversations stay intact.
// Its official development-assets contract serves these private public files
// under the original desktop protocol and original bootstrap/authentication.
type t3CodePrivateRuntime struct {
	Revision              int                 `json:"revision"`
	Root                  string              `json:"root"`
	SourceDigest          string              `json:"sourceDigest"`
	SourceArchive         string              `json:"sourceArchive"`
	SourceFiles           map[string]string   `json:"sourceFiles"`
	Files                 map[string]string   `json:"files"`
	ProvidersAsset        string              `json:"providersAsset"`
	ModelsAsset           string              `json:"modelsAsset,omitempty"`
	ModelAllowlist        map[string][]string `json:"modelAllowlist"`
	ModelDefaults         map[string]string   `json:"modelDefaults"`
	OpenCodeVariants      map[string][]string `json:"openCodeVariants"`
	MuseReasoningLevels   map[string][]string `json:"museReasoningLevels"`
	MuseReasoningDefaults map[string]string   `json:"museReasoningDefaults"`
}

var t3CodeRuntimeDefaultRow = regexp.MustCompile(`([A-Za-z_$][A-Za-z0-9_$]*)\.push\(\{instanceId:([A-Za-z_$][A-Za-z0-9_$]*),instance:([A-Za-z_$][A-Za-z0-9_$]*),driver:([A-Za-z_$][A-Za-z0-9_$]*),isDefault:!0,isDirty:([A-Za-z_$][A-Za-z0-9_$]*)\}\)`)

var t3CodeRuntimeExplicitDefaultRow = regexp.MustCompile(`([A-Za-z_$][A-Za-z0-9_$]*)\.push\(\{instanceId:([A-Za-z_$][A-Za-z0-9_$]*),explicitInstance:([A-Za-z_$][A-Za-z0-9_$]*),driver:([A-Za-z_$][A-Za-z0-9_$]*),isDefault:!0,isDirty:([A-Za-z_$][A-Za-z0-9_$]*)\}\)`)

func patchT3CodeProvidersAsset(source []byte) ([]byte, error) {
	matches := t3CodeRuntimeDefaultRow.FindAllSubmatchIndex(source, -1)
	explicitMatches := t3CodeRuntimeExplicitDefaultRow.FindAllSubmatchIndex(source, -1)
	if len(matches)+len(explicitMatches) != 1 {
		return nil, errors.New("This T3 Code Providers interface has an incompatible account-list contract.")
	}
	explicit := len(explicitMatches) == 1
	if explicit {
		matches = explicitMatches
	}
	m := matches[0]
	id, instance, driver := string(source[m[4]:m[5]]), string(source[m[6]:m[7]]), string(source[m[8]:m[9]])
	start := m[0] - 1400
	if start < 0 {
		start = 0
	}
	lookupPattern := `([A-Za-z_$][A-Za-z0-9_$]*)\.providerInstances\?\.\[` + regexp.QuoteMeta(id) + `\]`
	if explicit {
		// Newer panels keep an absent default slot undefined until rendering.
		// Bind that exact optional instance to its settings lookup before patching.
		lookupPattern = `(?:^|[,;])` + regexp.QuoteMeta(instance) + `=` + lookupPattern
	}
	lookup := regexp.MustCompile(lookupPattern).FindAllSubmatch(source[start:m[0]], -1)
	if len(lookup) != 1 {
		return nil, errors.New("Cannot verify the private T3 Code account-list settings lookup.")
	}
	settings := string(lookup[0][1])
	// Hide only the disabled synthesized native duplicate when the matching
	// managed direct account still exists. Its legacy ID keeps old chats usable.
	enabled := instance + ".enabled"
	if explicit {
		enabled = instance + "?.enabled"
	}
	condition := fmt.Sprintf(`!((%s==="codex"||%s==="claudeAgent")&&%s===false&&%s.providerInstances?.[%s==="codex"?"%s":"%s"]?.driver===%s)&&`, driver, driver, enabled, settings, driver, t3CodeCodexNormalID, t3CodeClaudeNormalID, driver)
	result := make([]byte, 0, len(source)+len(condition))
	result = append(result, source[:m[0]]...)
	result = append(result, condition...)
	result = append(result, source[m[0]:]...)
	return result, nil
}

func normalizeT3CodeRuntimeModelAllowlist(input map[string][]string) (map[string][]string, error) {
	result := map[string][]string{}
	for id, models := range input {
		if id != t3CodeCodexProxyID && id != t3CodeClaudeProxyID && id != t3CodeOpenCodeProxyID && id != t3CodeMuseProxyID || len(models) > 2048 {
			return nil, errors.New("Invalid private T3 Code model policy.")
		}
		seen := map[string]bool{}
		result[id] = []string{}
		for _, model := range models {
			if !catalogID.MatchString(model) || seen[model] {
				return nil, errors.New("Invalid private T3 Code model allowlist.")
			}
			seen[model] = true
			result[id] = append(result[id], model)
		}
		sort.Strings(result[id])
	}
	return result, nil
}

func normalizeT3CodeRuntimeModelDefaults(defaults map[string]string, policy map[string][]string) (map[string]string, error) {
	result := map[string]string{}
	for id, model := range defaults {
		models, exists := policy[id]
		if !exists || id != t3CodeCodexProxyID && id != t3CodeClaudeProxyID && id != t3CodeOpenCodeProxyID && id != t3CodeMuseProxyID {
			return nil, errors.New("Invalid private T3 Code default model account.")
		}
		valid := model == "" && len(models) == 0
		for _, allowed := range models {
			valid = valid || allowed == model
		}
		if !valid {
			return nil, errors.New("The private T3 Code default model is outside its account's model selection.")
		}
		result[id] = model
	}
	return result, nil
}

func normalizeT3CodeRuntimeOpenCodeVariants(input map[string][]string, policy map[string][]string) (map[string][]string, error) {
	result := map[string][]string{}
	for model, levels := range input {
		if !helperContains(policy[t3CodeOpenCodeProxyID], model) || len(levels) > len(nativeReasoningLevels) {
			return nil, errors.New("Invalid private T3 OpenCode variant policy.")
		}
		seen := map[string]bool{}
		result[model] = []string{}
		for _, level := range levels {
			if !helperContains(nativeReasoningLevels, level) || seen[level] {
				return nil, errors.New("Invalid private T3 OpenCode reasoning level.")
			}
			seen[level] = true
			result[model] = append(result[model], level)
		}
	}
	return result, nil
}

type t3CodeMuseRuntimePolicy struct {
	Levels   map[string][]string
	Defaults map[string]string
}

func normalizeT3CodeRuntimeMusePolicy(input t3CodeMuseRuntimePolicy, policy map[string][]string) (t3CodeMuseRuntimePolicy, error) {
	result := t3CodeMuseRuntimePolicy{Levels: map[string][]string{}, Defaults: map[string]string{}}
	allowed := policy[t3CodeMuseProxyID]
	if len(input.Levels) != len(allowed) || len(input.Defaults) != len(allowed) {
		return result, errors.New("Invalid private T3 Muse reasoning policy.")
	}
	for model, levels := range input.Levels {
		if !helperContains(allowed, model) || len(levels) > len(nativeReasoningLevels) {
			return result, errors.New("Invalid private T3 Muse model policy.")
		}
		seen := map[string]bool{}
		result.Levels[model] = []string{}
		for _, level := range levels {
			if !helperContains(nativeReasoningLevels, level) || seen[level] {
				return result, errors.New("Invalid private T3 Muse reasoning level.")
			}
			seen[level] = true
			result.Levels[model] = append(result.Levels[model], level)
		}
		initial, present := input.Defaults[model]
		if !present || (len(levels) == 0 && initial != "") || (len(levels) > 0 && !helperContains(levels, initial)) {
			return result, errors.New("Invalid private T3 Muse default reasoning level.")
		}
		result.Defaults[model] = initial
	}
	return result, nil
}

func t3CodeRuntimeModelFilter(allowlist map[string][]string, defaults map[string]string, requestedVariants ...map[string][]string) []byte {
	var variants map[string][]string
	if len(requestedVariants) > 0 {
		variants = requestedVariants[0]
	}
	return t3CodeRuntimeModelFilterWithMuse(allowlist, defaults, variants, t3CodeMuseRuntimePolicy{})
}

func t3CodeRuntimeModelFilterWithMuse(allowlist map[string][]string, defaults map[string]string, variants map[string][]string, muse t3CodeMuseRuntimePolicy) []byte {
	policy, _ := json.Marshal(allowlist)
	initial, _ := json.Marshal(defaults)
	if variants == nil {
		variants = map[string][]string{}
	}
	if muse.Levels == nil {
		muse.Levels = map[string][]string{}
	}
	if muse.Defaults == nil {
		muse.Defaults = map[string]string{}
	}
	variantJSON, _ := json.Marshal(variants)
	museLevels, _ := json.Marshal(muse.Levels)
	museDefaults, _ := json.Marshal(muse.Defaults)
	return []byte(`function __kiloT3OpenCodeVariants(model){const variants=` + string(variantJSON) + `,allowed=variants[model.slug]??[],cap=model.capabilities;if(!cap||!Array.isArray(cap.optionDescriptors))return model;let changed=false;const descriptors=cap.optionDescriptors.flatMap(descriptor=>{if(descriptor.id!=="variant")return [descriptor];changed=true;const options=Array.isArray(descriptor.options)?descriptor.options.filter(option=>allowed.includes(option.id)):[];if(options.length===0)return [];const result={...descriptor,options};if(Object.prototype.hasOwnProperty.call(result,"currentValue")&&!allowed.includes(result.currentValue))delete result.currentValue;return [result];});return changed?{...model,capabilities:{...cap,optionDescriptors:descriptors}}:model;}
function __kiloT3MuseReasoning(model){const levels=` + string(museLevels) + `,defaults=` + string(museDefaults) + `,allowed=levels[model.slug]??[],initial=defaults[model.slug],cap=model.capabilities;if(!cap||!Array.isArray(cap.optionDescriptors))return model;let changed=false;const descriptors=cap.optionDescriptors.flatMap(descriptor=>{if(descriptor.id!=="reasoningEffort")return [descriptor];changed=true;if(descriptor.type!=="select")return [];const options=Array.isArray(descriptor.options)?descriptor.options.filter(option=>allowed.includes(option.id)).map(option=>{const result={...option};delete result.isDefault;if(option.id===initial)result.isDefault=true;return result;}):[];if(options.length===0)return [];const result={...descriptor,options};if(options.some(option=>option.id===initial))result.currentValue=initial;else delete result.currentValue;return [result];});return changed?{...model,capabilities:{...cap,optionDescriptors:descriptors}}:model;}
function __kiloT3InstanceModels(id,driver,models){const policy=` + string(policy) + `,defaults=` + string(initial) + `;const expected=id==="` + t3CodeCodexProxyID + `"?"codex":id==="` + t3CodeClaudeProxyID + `"?"claudeAgent":id==="` + t3CodeOpenCodeProxyID + `"?"opencode":id==="` + t3CodeMuseProxyID + `"?"muse":null;if(expected===null||expected!==driver||!Object.prototype.hasOwnProperty.call(policy,id))return models;return Array.isArray(models)?models.filter(model=>policy[id].includes(model.slug)).map(model=>id==="` + t3CodeOpenCodeProxyID + `"?__kiloT3OpenCodeVariants(model):id==="` + t3CodeMuseProxyID + `"?__kiloT3MuseReasoning(model):model).map(model=>defaults[id]&&((model.isDefault===true)!==(model.slug===defaults[id]))?{...model,isDefault:model.slug===defaults[id]}:model):models;}` + "\n" + `function __kiloT3InstanceSnapshot(snapshot){const models=__kiloT3InstanceModels(snapshot.instanceId,snapshot.driver,snapshot.models);return models===snapshot.models?snapshot:{...snapshot,models};}` + "\n")
}

var t3CodeRuntimeModelsProjection = regexp.MustCompile(`snapshot:([A-Za-z_$][A-Za-z0-9_$]*),models:([A-Za-z_$][A-Za-z0-9_$]*)\.models`)
var t3CodeRuntimeModelsCard = regexp.MustCompile(`liveModels:([A-Za-z_$][A-Za-z0-9_$]*)\?\.models,customModels:([A-Za-z_$][A-Za-z0-9_$]*)`)
var t3CodeRuntimeCardArguments = regexp.MustCompile(`\{instanceId:([A-Za-z_$][A-Za-z0-9_$]*),instance:([A-Za-z_$][A-Za-z0-9_$]*),driverOption:[A-Za-z_$][A-Za-z0-9_$]*,liveProvider:([A-Za-z_$][A-Za-z0-9_$]*),`)
var t3CodeRuntimeDisplayNameCall = regexp.MustCompile(`displayName:([A-Za-z_$][A-Za-z0-9_$]*)\(([A-Za-z_$][A-Za-z0-9_$]*)\)`)

func patchT3CodeDirectDisplayNames(source, projection []byte, snapshot string) ([]byte, error) {
	calls := t3CodeRuntimeDisplayNameCall.FindAllSubmatch(projection, -1)
	if len(calls) != 1 || string(calls[0][2]) != snapshot {
		return nil, errors.New("Cannot correlate the T3 Code account display name.")
	}
	identifier := `[A-Za-z_$][A-Za-z0-9_$]*`
	declaration := regexp.MustCompile(`function `+regexp.QuoteMeta(string(calls[0][1]))+`\((`+identifier+`)\)\{`).FindAllSubmatchIndex(source, -1)
	if len(declaration) != 1 {
		return nil, errors.New("Cannot verify the T3 Code account display name resolver.")
	}
	header := declaration[0]
	argument := string(source[header[2]:header[3]])
	arg := regexp.QuoteMeta(argument)
	prefix := regexp.MustCompile(`^let (` + identifier + `)=` + arg + `\.displayName\?\.trim\(\),(` + identifier + `)=(` + identifier + `)\[` + arg + `\.driver\]\?\?(` + identifier + `)\(` + arg + `\.driver\);`).FindSubmatchIndex(source[header[1]:])
	if prefix == nil {
		return nil, errors.New("This T3 Code account display name resolver is incompatible.")
	}
	body := source[header[1]:]
	label, native, formatter := string(body[prefix[2]:prefix[3]]), string(body[prefix[4]:prefix[5]]), string(body[prefix[8]:prefix[9]])
	l, n, f := regexp.QuoteMeta(label), regexp.QuoteMeta(native), regexp.QuoteMeta(formatter)
	remainder := regexp.MustCompile(`^if\(` + l + `&&` + l + `!==` + n + `\)return ` + l + `;if\(` + arg + `\.instanceId!==(` + identifier + `)\(` + arg + `\.driver\)\)\{let ` + l + `=` + f + `\(` + arg + `\.instanceId\);if\(` + l + `\.length>0\)return ` + l + `\}return ` + l + `\|\|` + n + `\}`).FindIndex(body[prefix[1]:])
	if remainder == nil {
		return nil, errors.New("This T3 Code account display name fallback is incompatible.")
	}
	// Upstream intentionally derives names from non-default instance IDs when an
	// explicit name equals the driver name. Keep the old direct IDs for chats,
	// but honor their managed Codex/Claude names without altering custom labels.
	condition := fmt.Sprintf(`if(%s===%s&&((%s.instanceId==="%s"&&%s.driver==="codex"&&%s==="Codex")||(%s.instanceId==="%s"&&%s.driver==="claudeAgent"&&%s==="Claude")))return %s;`, label, native, argument, t3CodeCodexNormalID, argument, label, argument, t3CodeClaudeNormalID, argument, label, label)
	insert := header[1] + prefix[1]
	result := make([]byte, 0, len(source)+len(condition))
	result = append(result, source[:insert]...)
	result = append(result, condition...)
	return append(result, source[insert:]...), nil
}

func patchT3CodeModelsProjection(source []byte, policy map[string][]string, defaults ...map[string]string) ([]byte, error) {
	initial := map[string]string{}
	if len(defaults) > 0 {
		initial = defaults[0]
	}
	return patchT3CodeModelsProjectionWithVariants(source, policy, initial, nil)
}

func patchT3CodeModelsProjectionWithVariants(source []byte, policy map[string][]string, initial map[string]string, variants map[string][]string) ([]byte, error) {
	return patchT3CodeModelsProjectionWithMuse(source, policy, initial, variants, t3CodeMuseRuntimePolicy{})
}

func patchT3CodeModelsProjectionWithMuse(source []byte, policy map[string][]string, initial map[string]string, variants map[string][]string, muse t3CodeMuseRuntimePolicy) ([]byte, error) {
	matches := t3CodeRuntimeModelsProjection.FindAllSubmatchIndex(source, -1)
	if len(matches) != 1 || bytes.Contains(source, []byte("__kiloT3InstanceModels")) {
		return nil, errors.New("This T3 Code model picker has an incompatible instance projection.")
	}
	m := matches[0]
	snapshot, models := string(source[m[2]:m[3]]), string(source[m[4]:m[5]])
	start := m[0] - 800
	if start < 0 {
		start = 0
	}
	context := source[start:m[0]]
	for _, token := range []string{"driverKind:", "continuationGroupKey:", "isAvailable:"} {
		if !bytes.Contains(context, []byte(token)) {
			return nil, errors.New("Cannot verify the T3 Code instance model projection.")
		}
	}
	if snapshot != models {
		return nil, errors.New("Cannot correlate the T3 Code model picker snapshot.")
	}
	labelSource, err := patchT3CodeDirectDisplayNames(source, context, snapshot)
	if err != nil {
		return nil, err
	}
	// The display-name resolver precedes or follows the projection depending on
	// bundling. Locate the unchanged projection again after the narrow insertion.
	m = t3CodeRuntimeModelsProjection.FindSubmatchIndex(labelSource)
	filter := fmt.Sprintf("__kiloT3InstanceModels(%s.instanceId,%s.driver,%s.models)", snapshot, snapshot, snapshot)
	replacement := []byte("snapshot:__kiloT3InstanceSnapshot(" + snapshot + "),models:" + filter)
	result := append(t3CodeRuntimeModelFilterWithMuse(policy, initial, variants, muse), labelSource[:m[0]]...)
	result = append(result, replacement...)
	return append(result, labelSource[m[1]:]...), nil
}

func patchT3CodeModelsCard(source []byte, policy map[string][]string, defaults ...map[string]string) ([]byte, error) {
	initial := map[string]string{}
	if len(defaults) > 0 {
		initial = defaults[0]
	}
	return patchT3CodeModelsCardWithVariants(source, policy, initial, nil)
}

func patchT3CodeModelsCardWithVariants(source []byte, policy map[string][]string, initial map[string]string, variants map[string][]string) ([]byte, error) {
	return patchT3CodeModelsCardWithMuse(source, policy, initial, variants, t3CodeMuseRuntimePolicy{})
}

func patchT3CodeModelsCardWithMuse(source []byte, policy map[string][]string, initial map[string]string, variants map[string][]string, muse t3CodeMuseRuntimePolicy) ([]byte, error) {
	matches := t3CodeRuntimeModelsCard.FindAllSubmatchIndex(source, -1)
	if len(matches) != 1 || bytes.Contains(source, []byte("__kiloT3InstanceModels")) {
		return nil, errors.New("This T3 Code Models settings interface has an incompatible contract.")
	}
	m := matches[0]
	if m[1] >= len(source) || source[m[1]] != '}' {
		return nil, errors.New("Cannot verify the T3 Code Models settings custom model collection.")
	}
	start := bytes.LastIndex(source[:m[0]], []byte("function "))
	if start < 0 {
		return nil, errors.New("Cannot verify the T3 Code Models settings component.")
	}
	arguments := t3CodeRuntimeCardArguments.FindAllSubmatch(source[start:m[0]], -1)
	live, custom := string(source[m[2]:m[3]]), string(source[m[4]:m[5]])
	if len(arguments) != 1 || string(arguments[0][3]) != live {
		return nil, errors.New("Cannot correlate the T3 Code Models settings account.")
	}
	id, instance := string(arguments[0][1]), string(arguments[0][2])
	replacement := fmt.Sprintf("liveModels:__kiloT3InstanceModels(%s,%s.driver,%s?.models),customModels:__kiloT3InstanceModels(%s,%s.driver,%s)", id, instance, live, id, instance, custom)
	result := append(t3CodeRuntimeModelFilterWithMuse(policy, initial, variants, muse), source[:m[0]]...)
	result = append(result, []byte(replacement)...)
	return append(result, source[m[1]:]...), nil
}

// T3 can bundle the Models card in the Providers route or its imported dialog chunk.
var t3CodeRuntimeModelsCardImport = regexp.MustCompile(`\bimport\s*\{[^;\r\n]*\}\s*from\s*["']\./(AddProviderInstanceDialog-[A-Za-z0-9_-]+\.js)["']\s*;`)

func t3CodeRuntimeModelsCardAsset(files map[string][]byte, providersAsset string) (string, error) {
	source := files[providersAsset]
	if t3CodeRuntimeModelsCard.Match(source) {
		return providersAsset, nil
	}
	imports := t3CodeRuntimeModelsCardImport.FindAllSubmatchIndex(source, -1)
	if len(imports) == 1 {
		match := imports[0]
		code := t3CodeContractCode(string(source))
		name := "assets/" + string(source[match[2]:match[3]])
		if strings.HasPrefix(code[match[0]:], "import") && t3CodeRuntimeModelsCard.Match(files[name]) {
			return name, nil
		}
	}
	return "", errors.New("This T3 Code Models settings interface has an incompatible contract.")
}

func t3CodeRuntimeIdentity(record t3CodePrivateRuntime) []byte {
	identity, _ := json.Marshal([]any{record.Revision, record.SourceDigest, record.SourceArchive, record.SourceFiles, record.ModelAllowlist, record.ModelDefaults, record.OpenCodeVariants, record.MuseReasoningLevels, record.MuseReasoningDefaults})
	return identity
}

func validT3CodeRuntimeRoot(record t3CodePrivateRuntime, privateRoot string) bool {
	policy, err := normalizeT3CodeRuntimeModelAllowlist(record.ModelAllowlist)
	if err != nil || record.Revision != t3CodeRuntimeRevision || !packageUpdateAbsolute(privateRoot) || !managedToolCatalogSHA256.MatchString(record.SourceDigest) || len(record.SourceFiles) != len(record.Files) || len(record.Files) < 2 || len(record.Files) > 4096 {
		return false
	}
	for name, digest := range record.SourceFiles {
		if !t3CodeRuntimeSafeAsset(name) || !managedToolCatalogSHA256.MatchString(digest) || !managedToolCatalogSHA256.MatchString(record.Files[name]) {
			return false
		}
	}
	record.ModelDefaults, err = normalizeT3CodeRuntimeModelDefaults(record.ModelDefaults, policy)
	if err != nil {
		return false
	}
	record.OpenCodeVariants, err = normalizeT3CodeRuntimeOpenCodeVariants(record.OpenCodeVariants, policy)
	if err != nil {
		return false
	}
	muse, err := normalizeT3CodeRuntimeMusePolicy(t3CodeMuseRuntimePolicy{Levels: record.MuseReasoningLevels, Defaults: record.MuseReasoningDefaults}, policy)
	if err != nil {
		return false
	}
	record.MuseReasoningLevels, record.MuseReasoningDefaults = muse.Levels, muse.Defaults
	record.ModelAllowlist = policy
	expected := filepath.Join(privateRoot, "runtime-web", openDesignHash(t3CodeRuntimeIdentity(record)))
	return record.Root == expected && safeLaunchDir(privateRoot, filepath.Dir(privateRoot)) && safeLaunchDir(record.Root, privateRoot)
}

func t3CodeRuntimeDesktopContract(archive *t3CodeArchive) error {
	source, err := archive.sources("apps/desktop/dist-electron/", 24<<20)
	if err != nil {
		return err
	}
	for _, token := range []string{"VITE_DEV_SERVER_URL", "T3CODE_PORT", "configuredBackendPort", "getDesktopScheme", "targetOrigin", "proxyRequest", "DesktopDevelopmentBackendPortRequiredError", "t3code-dev"} {
		if !bytes.Contains(source, []byte(token)) {
			return errors.New("This T3 Code desktop does not support the private asset server contract. Update T3 Code and prepare again.")
		}
	}
	return nil
}

func t3CodeRuntimeWebArchive(executable, platform string) (*t3CodeArchive, error) {
	archive, err := t3CodeOpenPackage(executable, platform)
	if err != nil {
		return nil, err
	}
	if err = t3CodeRuntimeDesktopContract(archive); err != nil {
		archive.file.Close()
		return nil, err
	}
	if _, err = archive.read("apps/server/dist/client/index.html", 2<<20); err == nil {
		return archive, nil
	}
	// Windows releases can ship the server tree in a separate signed archive.
	serverPath := filepath.Join(filepath.Dir(archive.file.Name()), "server.asar")
	archive.file.Close()
	archive, err = t3CodeOpenASAR(serverPath)
	if err != nil {
		return nil, errors.New("Cannot read the installed T3 Code public web assets.")
	}
	return archive, nil
}

func t3CodeRuntimeWebFiles(archive *t3CodeArchive) (map[string][]byte, error) {
	entry := archive.tree
	for _, component := range []string{"apps", "server", "dist", "client"} {
		entry = entry.Files[component]
		if entry.Link != "" || entry.Unpacked {
			return nil, errors.New("Unsafe T3 Code web assets.")
		}
	}
	files := map[string][]byte{}
	var size int64
	var walk func(t3CodeASAREntry, string, int) error
	walk = func(entry t3CodeASAREntry, prefix string, depth int) error {
		if depth > 12 || entry.Link != "" || entry.Unpacked {
			return errors.New("Unsafe T3 Code web asset tree.")
		}
		names := []string{}
		for name := range entry.Files {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\:\x00\r\n") {
				return errors.New("Unsafe T3 Code web asset name.")
			}
			child, relative := entry.Files[name], prefix+name
			if child.Files != nil {
				if err := walk(child, relative+"/", depth+1); err != nil {
					return err
				}
				continue
			}
			if len(files) >= 4096 || child.Size > t3CodeRuntimeTotalLimit-size {
				return errors.New("T3 Code web assets exceed the private runtime limit.")
			}
			data, err := archive.read("apps/server/dist/client/"+relative, t3CodeRuntimeFileLimit)
			if err != nil {
				return nilAssetError()
			}
			size += int64(len(data))
			files[relative] = data
		}
		return nil
	}
	if err := walk(entry, "", 0); err != nil {
		return nil, err
	}
	if len(files["index.html"]) == 0 {
		return nil, errors.New("Missing T3 Code web entry.")
	}
	return files, nil
}

func nilAssetError() error { return errors.New("Cannot verify a bundled T3 Code public web asset.") }

func prepareT3CodeRuntime(privateRoot, executable, platform string, allowlists ...map[string][]string) (t3CodePrivateRuntime, error) {
	var result t3CodePrivateRuntime
	var input map[string][]string
	if len(allowlists) > 1 {
		return result, errors.New("Ambiguous private T3 Code model policy.")
	}
	if len(allowlists) == 1 {
		input = allowlists[0]
	}
	return prepareT3CodeRuntimeWithModelDefaults(privateRoot, executable, platform, input, nil)
}

func prepareT3CodeRuntimeWithModelDefaults(privateRoot, executable, platform string, input map[string][]string, requestedDefaults map[string]string, requestedVariants ...map[string][]string) (t3CodePrivateRuntime, error) {
	if len(requestedVariants) > 1 {
		return t3CodePrivateRuntime{}, errors.New("Ambiguous private T3 OpenCode variant policy.")
	}
	var variants map[string][]string
	if len(requestedVariants) == 1 {
		variants = requestedVariants[0]
	}
	return prepareT3CodeRuntimeWithMuse(privateRoot, executable, platform, input, requestedDefaults, variants, t3CodeMuseRuntimePolicy{})
}

func prepareT3CodeRuntimeWithMuse(privateRoot, executable, platform string, input map[string][]string, requestedDefaults map[string]string, variantInput map[string][]string, requestedMuse t3CodeMuseRuntimePolicy) (t3CodePrivateRuntime, error) {
	var result t3CodePrivateRuntime
	policy, err := normalizeT3CodeRuntimeModelAllowlist(input)
	if err != nil {
		return result, err
	}
	defaults, err := normalizeT3CodeRuntimeModelDefaults(requestedDefaults, policy)
	if err != nil {
		return result, err
	}
	variants, err := normalizeT3CodeRuntimeOpenCodeVariants(variantInput, policy)
	if err != nil {
		return result, err
	}
	muse, err := normalizeT3CodeRuntimeMusePolicy(requestedMuse, policy)
	if err != nil {
		return result, err
	}
	installation, err := inspectT3CodeInstallation(executable, platform)
	if err != nil {
		return result, err
	}
	archive, err := t3CodeRuntimeWebArchive(executable, platform)
	if err != nil {
		return result, err
	}
	defer archive.file.Close()
	files, err := t3CodeRuntimeWebFiles(archive)
	if err != nil {
		return result, err
	}
	result = t3CodePrivateRuntime{Revision: t3CodeRuntimeRevision, SourceDigest: installation.SourceDigest, SourceArchive: archive.file.Name(), ModelAllowlist: policy, ModelDefaults: defaults, OpenCodeVariants: variants, MuseReasoningLevels: muse.Levels, MuseReasoningDefaults: muse.Defaults, Files: map[string]string{}, SourceFiles: map[string]string{}}
	for name, data := range files {
		result.SourceFiles[name] = openDesignHash(data)
		isProvidersAsset := strings.HasPrefix(name, "assets/settings.providers-") ||
			strings.HasPrefix(name, "assets/_chat.settings.providers-")
		if isProvidersAsset && strings.HasSuffix(name, ".js") {
			if result.ProvidersAsset != "" {
				return t3CodePrivateRuntime{}, errors.New("Ambiguous T3 Code Providers interface assets.")
			}
			patched, err := patchT3CodeProvidersAsset(data)
			if err != nil {
				return t3CodePrivateRuntime{}, err
			}
			files[name], result.ProvidersAsset = patched, name
		}
	}
	if result.ProvidersAsset == "" {
		return t3CodePrivateRuntime{}, errors.New("Cannot locate the installed T3 Code Providers interface.")
	}
	if len(policy) > 0 {
		cardAsset, err := t3CodeRuntimeModelsCardAsset(files, result.ProvidersAsset)
		if err != nil {
			return t3CodePrivateRuntime{}, err
		}
		files[cardAsset], err = patchT3CodeModelsCardWithMuse(files[cardAsset], policy, defaults, variants, muse)
		if err != nil {
			return t3CodePrivateRuntime{}, err
		}
		for name, data := range files {
			if !strings.HasPrefix(name, "assets/") || !strings.HasSuffix(name, ".js") || !t3CodeRuntimeModelsProjection.Match(data) {
				continue
			}
			if result.ModelsAsset != "" {
				return t3CodePrivateRuntime{}, errors.New("Ambiguous T3 Code instance model projection assets.")
			}
			patched, err := patchT3CodeModelsProjectionWithMuse(data, policy, defaults, variants, muse)
			if err != nil {
				return t3CodePrivateRuntime{}, err
			}
			files[name], result.ModelsAsset = patched, name
		}
		if result.ModelsAsset == "" {
			return t3CodePrivateRuntime{}, errors.New("Cannot locate the installed T3 Code instance model projection.")
		}
	}
	identity := t3CodeRuntimeIdentity(result)
	cache := filepath.Join(privateRoot, "runtime-web")
	if !packageUpdateAbsolute(privateRoot) || !safeLaunchDir(privateRoot, filepath.Dir(privateRoot)) {
		return t3CodePrivateRuntime{}, errors.New("Unsafe private T3 Code runtime directory.")
	}
	if err := os.MkdirAll(cache, 0700); err != nil || !safeLaunchDir(cache, privateRoot) {
		return t3CodePrivateRuntime{}, errors.New("Cannot create the private T3 Code web runtime.")
	}
	result.Root = filepath.Join(cache, openDesignHash(identity))
	for name, data := range files {
		result.Files[name] = openDesignHash(data)
	}
	if _, err := os.Lstat(result.Root); err == nil {
		if !verifyT3CodeRuntimeFiles(result) {
			return t3CodePrivateRuntime{}, errors.New("The private T3 Code web runtime changed. Remove only its runtime-web cache and prepare again.")
		}
		return result, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return t3CodePrivateRuntime{}, err
	}
	stage, err := os.MkdirTemp(cache, ".preparing-")
	if err != nil {
		return t3CodePrivateRuntime{}, err
	}
	defer os.RemoveAll(stage)
	for name, data := range files {
		target := filepath.Join(stage, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return t3CodePrivateRuntime{}, err
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			return t3CodePrivateRuntime{}, err
		}
	}
	if err := os.Rename(stage, result.Root); err != nil {
		return t3CodePrivateRuntime{}, err
	}
	return result, nil
}

func t3CodeRuntimeSafeAsset(name string) bool {
	if name == "" || name == "." || path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\:\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." || part == "." || part == "" {
			return false
		}
	}
	return true
}

func readT3CodeRuntimeAsset(runtime t3CodePrivateRuntime, name string) ([]byte, error) {
	expected, exists := runtime.Files[name]
	if !exists || !t3CodeRuntimeSafeAsset(name) || len(expected) != 64 || !safeLaunchDir(runtime.Root, filepath.Dir(runtime.Root)) {
		return nil, nilAssetError()
	}
	target := filepath.Join(runtime.Root, filepath.FromSlash(name))
	if !safeLaunchDir(filepath.Dir(target), runtime.Root) {
		return nil, nilAssetError()
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > t3CodeRuntimeFileLimit {
		return nil, nilAssetError()
	}
	file, err := os.Open(target)
	if err != nil {
		return nil, nilAssetError()
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, t3CodeRuntimeFileLimit+1))
	if err != nil || int64(len(data)) > t3CodeRuntimeFileLimit || openDesignHash(data) != expected {
		return nil, nilAssetError()
	}
	return data, nil
}

func verifyT3CodeRuntimeFiles(runtime t3CodePrivateRuntime) bool {
	if runtime.Revision != t3CodeRuntimeRevision || !packageUpdateAbsolute(runtime.Root) || len(runtime.Files) < 2 || len(runtime.Files) > 4096 || len(runtime.SourceFiles) != len(runtime.Files) || runtime.ProvidersAsset == "" || runtime.Files[runtime.ProvidersAsset] == "" || runtime.Files["index.html"] == "" {
		return false
	}
	if _, err := normalizeT3CodeRuntimeModelAllowlist(runtime.ModelAllowlist); err != nil {
		return false
	}
	if _, err := normalizeT3CodeRuntimeModelDefaults(runtime.ModelDefaults, runtime.ModelAllowlist); err != nil {
		return false
	}
	if _, err := normalizeT3CodeRuntimeOpenCodeVariants(runtime.OpenCodeVariants, runtime.ModelAllowlist); err != nil {
		return false
	}
	if _, err := normalizeT3CodeRuntimeMusePolicy(t3CodeMuseRuntimePolicy{Levels: runtime.MuseReasoningLevels, Defaults: runtime.MuseReasoningDefaults}, runtime.ModelAllowlist); err != nil {
		return false
	}
	if len(runtime.ModelAllowlist) > 0 && (runtime.ModelsAsset == "" || runtime.Files[runtime.ModelsAsset] == "") {
		return false
	}
	var size int64
	for name := range runtime.Files {
		data, err := readT3CodeRuntimeAsset(runtime, name)
		if err != nil {
			return false
		}
		size += int64(len(data))
		if size > t3CodeRuntimeTotalLimit {
			return false
		}
	}
	return true
}

func t3CodeRuntimeReady(runtime t3CodePrivateRuntime, executable, platform string) bool {
	installation, err := inspectT3CodeInstallation(executable, platform)
	if err != nil || installation.SourceDigest != runtime.SourceDigest || !verifyT3CodeRuntimeFiles(runtime) {
		return false
	}
	archive, err := t3CodeRuntimeWebArchive(executable, platform)
	if err != nil {
		return false
	}
	defer archive.file.Close()
	if archive.file.Name() != runtime.SourceArchive {
		return false
	}
	files, err := t3CodeRuntimeWebFiles(archive)
	if err != nil || len(files) != len(runtime.SourceFiles) {
		return false
	}
	muse := t3CodeMuseRuntimePolicy{Levels: runtime.MuseReasoningLevels, Defaults: runtime.MuseReasoningDefaults}
	cardAsset := ""
	if len(runtime.ModelAllowlist) > 0 {
		cardAsset, err = t3CodeRuntimeModelsCardAsset(files, runtime.ProvidersAsset)
		if err != nil {
			return false
		}
	}
	for name, data := range files {
		if openDesignHash(data) != runtime.SourceFiles[name] {
			return false
		}
		expected := data
		if name == runtime.ProvidersAsset {
			expected, err = patchT3CodeProvidersAsset(data)
			if err != nil {
				return false
			}
		}
		if name == cardAsset {
			expected, err = patchT3CodeModelsCardWithMuse(expected, runtime.ModelAllowlist, runtime.ModelDefaults, runtime.OpenCodeVariants, muse)
			if err != nil {
				return false
			}
		}
		if name == runtime.ModelsAsset {
			expected, err = patchT3CodeModelsProjectionWithMuse(data, runtime.ModelAllowlist, runtime.ModelDefaults, runtime.OpenCodeVariants, muse)
			if err != nil {
				return false
			}
		}
		if openDesignHash(expected) != runtime.Files[name] {
			return false
		}
	}
	return true
}

type t3CodeRuntimeAssets struct {
	URL         string
	BackendPort int
	server      *http.Server
	done        chan struct{}
	once        sync.Once
}

func (assets *t3CodeRuntimeAssets) Close() error {
	var err error
	if assets != nil {
		assets.once.Do(func() { err = assets.server.Close(); close(assets.done) })
	}
	return err
}

func (assets *t3CodeRuntimeAssets) closeOnQuit(quit <-chan struct{}) {
	select {
	case <-quit:
		assets.Close()
	case <-assets.done:
	}
}

func startT3CodeRuntimeAssets(runtime t3CodePrivateRuntime) (*t3CodeRuntimeAssets, error) {
	if !verifyT3CodeRuntimeFiles(runtime) {
		return nil, errors.New("Prepare T3 Code again: its private interface assets changed.")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("Cannot start the private T3 Code interface server.")
	}
	reservation, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		listener.Close()
		return nil, err
	}
	backendPort := reservation.Addr().(*net.TCPAddr).Port
	reservation.Close() // T3 rechecks availability before binding this port itself.
	assets := &t3CodeRuntimeAssets{URL: "http://" + listener.Addr().String(), BackendPort: backendPort, done: make(chan struct{})}
	host := listener.Addr().String()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != host || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "t3code-dev://app") {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if !t3CodeRuntimeSafeAsset(name) {
			http.NotFound(w, r)
			return
		}
		if _, exists := runtime.Files[name]; !exists {
			if strings.HasPrefix(name, "api/") || strings.HasPrefix(name, "auth/") || name == "ws" || path.Ext(name) != "" || !strings.Contains(r.Header.Get("Accept"), "text/html") {
				http.NotFound(w, r)
				return
			}
			name = "index.html"
		}
		data, err := readT3CodeRuntimeAsset(runtime, name)
		if err != nil {
			http.Error(w, "Private interface asset unavailable", http.StatusServiceUnavailable)
			return
		}
		kind := mime.TypeByExtension(path.Ext(name))
		if kind == "" {
			kind = "application/octet-stream"
		}
		w.Header().Set("Content-Type", kind)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method == http.MethodGet {
			_, _ = w.Write(data)
		}
	})
	assets.server = &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() { _ = assets.server.Serve(listener) }()
	return assets, nil
}
