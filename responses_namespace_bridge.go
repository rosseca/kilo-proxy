package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// Responses namespaces contain client tools, but some BYOK gateways classify
// every top-level type other than function/custom as a hosted paid tool. Use
// request-local, collision-checked wire names and restore the original identity
// before any argument/schema adaptation. This never turns hosted tools into
// client tools and never executes a tool.
type responsesNamespaceTool struct {
	kind, namespace, name, alias string
	original                     map[string]any
}

type responsesNamespaceBridge struct {
	tools         map[string]*responsesNamespaceTool
	aliases       map[string]*responsesNamespaceTool
	flatNames     map[string]bool
	reservedNames map[string]bool
	groups        map[string]map[string]any
	choiceRefs    map[string]map[string]any
}

func namespaceToolID(kind, namespace, name string) string {
	return kind + "\x00" + toolKey(namespace, name)
}

func namespaceCopyObject(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	copy := make(map[string]any, len(value))
	for key, value := range value {
		copy[key] = value
	}
	return copy
}

func namespaceValidName(value any) bool {
	name, ok := value.(string)
	return ok && name != "" && !strings.ContainsRune(name, '\x00')
}

func namespaceCallKind(item map[string]any) string {
	switch stringValue(item["type"]) {
	case "function_call", "function_call_output":
		return "function"
	case "custom_tool_call", "custom_tool_call_output":
		return "custom"
	}
	return ""
}

func (b *responsesNamespaceBridge) register(kind, namespace, name string, original map[string]any) error {
	if kind != "function" && kind != "custom" || namespace == "" || name == "" || strings.ContainsRune(namespace+name, '\x00') {
		return errors.New("invalid client tool namespace identity")
	}
	key := namespaceToolID(kind, namespace, name)
	if existing := b.tools[key]; existing != nil {
		if original != nil && existing.original != nil {
			return errors.New("duplicate client tool in a namespace")
		}
		if original != nil {
			existing.original = namespaceCopyObject(original)
		}
		return nil
	}
	b.tools[key] = &responsesNamespaceTool{kind: kind, namespace: namespace, name: name, original: namespaceCopyObject(original)}
	return nil
}

func prepareResponsesNamespaceBridge(r *http.Request) (*responsesNamespaceBridge, error) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" || r.Header.Get("Content-Encoding") != "" && !strings.EqualFold(strings.TrimSpace(r.Header.Get("Content-Encoding")), "identity") {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, bridgeLimit+1))
	if err != nil {
		return nil, err
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(data))
	if len(data) > bridgeLimit {
		return nil, &http.MaxBytesError{Limit: bridgeLimit}
	}
	doc, err := decodeObject(data)
	if err != nil {
		return nil, nil // Preserve the gateway's validation of unrelated JSON.
	}
	b := &responsesNamespaceBridge{
		tools: make(map[string]*responsesNamespaceTool), aliases: make(map[string]*responsesNamespaceTool),
		flatNames: make(map[string]bool), reservedNames: make(map[string]bool), groups: make(map[string]map[string]any), choiceRefs: make(map[string]map[string]any),
	}
	tools, toolsArray := doc["tools"].([]any)
	for _, value := range tools {
		tool := object(value)
		if stringValue(tool["type"]) != "namespace" {
			if name := stringValue(tool["name"]); name != "" {
				b.flatNames[stringValue(tool["type"])+"\x00"+name] = true
				b.reservedNames[name] = true
			}
			continue
		}
		if !namespaceValidName(tool["name"]) {
			return nil, errors.New("namespace tools require a non-empty name")
		}
		namespace := stringValue(tool["name"])
		if _, exists := b.groups[namespace]; exists {
			return nil, errors.New("duplicate tool namespace")
		}
		for key := range tool {
			if key != "type" && key != "name" && key != "description" && key != "tools" {
				return nil, errors.New("unsupported tool namespace property")
			}
		}
		if description, exists := tool["description"]; exists {
			if _, ok := description.(string); !ok {
				return nil, errors.New("namespace description must be a string")
			}
		}
		children, ok := tool["tools"].([]any)
		if !ok || len(children) == 0 {
			return nil, errors.New("namespace tools require a non-empty client tool list")
		}
		b.groups[namespace] = namespaceCopyObject(tool)
		names := make(map[string]bool)
		for _, value := range children {
			child := object(value)
			kind := stringValue(child["type"])
			if kind != "function" && kind != "custom" {
				return nil, errors.New("a tool namespace may contain only function or custom client tools")
			}
			if !namespaceValidName(child["name"]) || names[stringValue(child["name"])] {
				return nil, errors.New("namespace client tools require unique non-empty names")
			}
			if _, exists := child["namespace"]; exists {
				return nil, errors.New("nested tool namespace identities are unsupported")
			}
			names[stringValue(child["name"])] = true
			if err := b.register(kind, namespace, stringValue(child["name"]), child); err != nil {
				return nil, err
			}
		}
	}
	// The demonstrated gateway filter drops namespace declarations, not
	// historical calls. Without a declared group, preserve the existing wire
	// contract (including Codex delegation notifications) exactly.
	if len(b.groups) == 0 {
		return nil, nil
	}
	input, _ := doc["input"].([]any)
	for _, value := range input {
		item := object(value)
		kind := namespaceCallKind(item)
		if kind == "" || stringValue(item["name"]) == "" {
			continue
		}
		namespace, name := stringValue(item["namespace"]), stringValue(item["name"])
		if namespace == "" {
			if name != "" {
				b.reservedNames[name] = true
			}
			continue
		}
		// Full history can mention a tool no longer in this turn's declarations.
		// Its explicit identity is sufficient; no shared ID/name cache is used.
		if err := b.register(kind, namespace, name, nil); err != nil {
			return nil, err
		}
	}
	if len(b.tools) == 0 {
		return nil, nil // Exact bytes and headers survive requests without namespaces.
	}
	if stringValue(doc["previous_response_id"]) != "" {
		return nil, errors.New("namespace compatibility requires full input history; previous_response_id is unsupported")
	}
	if doc["conversation"] != nil {
		return nil, errors.New("namespace compatibility requires full input history; conversation state is unsupported")
	}
	for _, value := range tools {
		if stringValue(object(value)["type"]) == "tool_search" {
			return nil, errors.New("namespace compatibility does not support dynamic tool_search; send explicit tool declarations and full input history")
		}
	}
	for _, value := range input {
		if stringValue(object(value)["type"]) == "tool_search_output" {
			return nil, errors.New("namespace compatibility does not support dynamic tool_search_output; send explicit tool declarations and full input history")
		}
	}
	// Sort identities so request ordering cannot decide which name wins a
	// collision with an existing flat tool. Aliases reveal no argument content.
	keys := make([]string, 0, len(b.tools))
	for key := range b.tools {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entry := b.tools[key]
		for salt := 0; ; salt++ {
			sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", key, salt)))
			alias := "kilo_ns_" + hex.EncodeToString(sum[:24])
			if b.reservedNames[alias] || b.aliases[alias] != nil {
				continue
			}
			entry.alias = alias
			b.aliases[alias] = entry
			break
		}
	}
	if err := b.rewriteChoice(doc["tool_choice"], false); err != nil {
		return nil, err
	}
	flattened := make([]any, 0, len(tools))
	for _, value := range tools {
		tool := object(value)
		if stringValue(tool["type"]) != "namespace" {
			flattened = append(flattened, value)
			continue
		}
		namespace := stringValue(tool["name"])
		children, _ := tool["tools"].([]any)
		shared := stringValue(tool["description"])
		carrier := 0
		// Preserve shared guidance once, on an explicitly selectable tool
		// when there is one. Keep it at the tool-description trust level.
		if shared != "" && len(children) > 1 && len(b.choiceRefs) != 0 {
			for i, value := range children {
				child := object(value)
				entry := b.tools[namespaceToolID(stringValue(child["type"]), namespace, stringValue(child["name"]))]
				if b.choiceRefs[entry.alias] != nil {
					carrier = i
					break
				}
			}
		}
		for i, value := range children {
			child := namespaceCopyObject(object(value))
			entry := b.tools[namespaceToolID(stringValue(child["type"]), namespace, stringValue(child["name"]))]
			child["name"] = entry.alias
			context := "Client tool: " + namespace + "." + entry.name + ". Tool namespace: " + namespace + "."
			if i == carrier && shared != "" {
				if len(children) == 1 {
					context += " " + shared
				} else {
					context += "\nShared guidance for all tools in this namespace: " + shared
				}
			}
			if description := stringValue(child["description"]); description != "" {
				context += "\n" + description
			}
			child["description"] = context
			flattened = append(flattened, child)
		}
	}
	if toolsArray {
		doc["tools"] = flattened
	}
	for _, value := range input {
		item := object(value)
		kind := namespaceCallKind(item)
		if kind == "" || stringValue(item["name"]) == "" || stringValue(item["namespace"]) == "" {
			// A missing namespace denotes a flat historical identity. Do not
			// reinterpret retired flat tools using current declarations.
			continue
		}
		entry, err := b.resolveReference(kind, item)
		if err != nil {
			return nil, err
		}
		if entry != nil {
			item["name"] = entry.alias
			delete(item, "namespace")
		}
	}
	changed, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	if len(changed) > bridgeLimit {
		return nil, &http.MaxBytesError{Limit: bridgeLimit}
	}
	r.Body = io.NopCloser(bytes.NewReader(changed))
	r.ContentLength = int64(len(changed))
	r.Header.Del("Content-Length")
	return b, nil
}

func (b *responsesNamespaceBridge) resolveReference(kind string, ref map[string]any) (*responsesNamespaceTool, error) {
	namespace, name := stringValue(ref["namespace"]), stringValue(ref["name"])
	if namespace != "" {
		if entry := b.tools[namespaceToolID(kind, namespace, name)]; entry != nil {
			return entry, nil
		}
		return nil, errors.New("namespace tool reference has no matching client tool")
	}
	if b.flatNames[kind+"\x00"+name] {
		return nil, nil // A genuine flat name takes precedence over qualified syntax.
	}
	var matched *responsesNamespaceTool
	bare := false
	for _, entry := range b.tools {
		if entry.kind != kind {
			continue
		}
		if entry.name == name {
			bare = true
		}
		if entry.namespace+"."+entry.name == name {
			if matched != nil {
				return nil, errors.New("ambiguous qualified namespace tool reference")
			}
			matched = entry
		}
	}
	if matched == nil && bare {
		return nil, errors.New("namespace tool references require their qualified namespace.name")
	}
	return matched, nil
}

func (b *responsesNamespaceBridge) rewriteChoice(value any, restore bool) error {
	choice := object(value)
	if choice == nil {
		return nil
	}
	kind := stringValue(choice["type"])
	if kind == "allowed_tools" {
		refs, _ := choice["tools"].([]any)
		for _, ref := range refs {
			if err := b.rewriteChoice(ref, restore); err != nil {
				return err
			}
		}
		return nil
	}
	if kind == "namespace" && !restore {
		return errors.New("namespace tool_choice requires qualified function or custom references")
	}
	if kind != "function" && kind != "custom" {
		return nil
	}
	if restore {
		if entry := b.aliases[stringValue(choice["name"])]; entry != nil {
			if kind != entry.kind {
				return errors.New("upstream tool choice changed the namespace tool type")
			}
			choice["name"] = entry.namespace + "." + entry.name
			delete(choice, "namespace")
			if original := b.choiceRefs[entry.alias]; original != nil {
				choice["name"] = original["name"]
				if namespace, exists := original["namespace"]; exists {
					choice["namespace"] = namespace
				}
			}
		}
		return nil
	}
	entry, err := b.resolveReference(kind, choice)
	if err != nil {
		return err
	}
	if entry != nil {
		if b.choiceRefs[entry.alias] == nil {
			b.choiceRefs[entry.alias] = namespaceCopyObject(choice)
		}
		choice["name"] = entry.alias
		delete(choice, "namespace")
	}
	return nil
}

func (b *responsesNamespaceBridge) restoreItem(item map[string]any) error {
	kind := namespaceCallKind(item)
	if kind == "" {
		return nil
	}
	entry := b.aliases[stringValue(item["name"])]
	if entry == nil {
		return nil // Never interpret an unknown name using a prefix heuristic.
	}
	if kind != entry.kind {
		return errors.New("upstream call changed the namespace tool type")
	}
	if namespace := stringValue(item["namespace"]); namespace != "" && namespace != entry.namespace {
		return errors.New("upstream call has a conflicting namespace")
	}
	item["name"], item["namespace"] = entry.name, entry.namespace
	return nil
}

func (b *responsesNamespaceBridge) restoreTools(response map[string]any) error {
	tools, ok := response["tools"].([]any)
	if !ok {
		return nil
	}
	restored := make([]any, 0, len(tools))
	groups := make(map[string]map[string]any)
	for _, value := range tools {
		tool := object(value)
		entry := b.aliases[stringValue(tool["name"])]
		if entry == nil {
			restored = append(restored, value)
			continue
		}
		if stringValue(tool["type"]) != entry.kind {
			return errors.New("upstream declaration changed the namespace tool type")
		}
		child := namespaceCopyObject(tool)
		child["name"] = entry.name
		delete(child, "namespace")
		// Restore descriptions instead of exposing the private wire hint.
		if description, exists := entry.original["description"]; exists {
			child["description"] = description
		} else {
			delete(child, "description")
		}
		group := groups[entry.namespace]
		if group == nil {
			template := b.groups[entry.namespace]
			if template == nil {
				return errors.New("upstream echoed an undeclared namespace tool")
			}
			group = namespaceCopyObject(template)
			group["tools"] = []any{}
			groups[entry.namespace] = group
			restored = append(restored, group)
		}
		group["tools"] = append(group["tools"].([]any), child)
	}
	response["tools"] = restored
	return nil
}

func (b *responsesNamespaceBridge) restoreResponse(response map[string]any) error {
	output, _ := response["output"].([]any)
	for _, value := range output {
		if err := b.restoreItem(object(value)); err != nil {
			return err
		}
	}
	if err := b.restoreTools(response); err != nil {
		return err
	}
	return b.rewriteChoice(response["tool_choice"], true)
}

func (b *responsesNamespaceBridge) transformEvent(data []byte) ([]byte, error) {
	if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		return data, nil
	}
	event, err := decodeObject(data)
	if err != nil {
		return nil, errors.New("invalid Responses namespace event")
	}
	if err := b.restoreItem(object(event["item"])); err != nil {
		return nil, err
	}
	if response := object(event["response"]); response != nil {
		if err := b.restoreResponse(response); err != nil {
			return nil, err
		}
	}
	kind := stringValue(event["type"])
	if strings.HasPrefix(kind, "response.function_call_arguments.") || strings.HasPrefix(kind, "response.custom_tool_call_input.") {
		if entry := b.aliases[stringValue(event["name"])]; entry != nil {
			expected := "function"
			if strings.HasPrefix(kind, "response.custom_tool_call_input.") {
				expected = "custom"
			}
			if entry.kind != expected {
				return nil, errors.New("upstream event changed the namespace tool type")
			}
			event["name"], event["namespace"] = entry.name, entry.namespace
		}
	}
	changed, err := json.Marshal(event)
	if err == nil && len(changed) > bridgeLimit {
		return nil, errors.New("adapted SSE event exceeds namespace bridge limit")
	}
	return changed, err
}

func (b *responsesNamespaceBridge) transformSSE(source io.Reader, destination io.Writer) error {
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 4096), bridgeLimit)
	var lines []string
	size := 0
	flush := func() error {
		if len(lines) == 0 {
			return nil
		}
		var payload []string
		for _, line := range lines {
			if strings.HasPrefix(line, "data:") {
				payload = append(payload, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
		if len(payload) == 0 {
			_, err := io.WriteString(destination, strings.Join(lines, "\n")+"\n\n")
			return err
		}
		changed, err := b.transformEvent([]byte(strings.Join(payload, "\n")))
		if err != nil {
			return err
		}
		wroteData := false
		for _, line := range lines {
			if strings.HasPrefix(line, "data:") {
				if !wroteData {
					if _, err := io.WriteString(destination, "data: "+string(changed)+"\n"); err != nil {
						return err
					}
					wroteData = true
				}
				continue
			}
			if _, err := io.WriteString(destination, line+"\n"); err != nil {
				return err
			}
		}
		_, err = io.WriteString(destination, "\n")
		return err
	}
	for scanner.Scan() {
		line := scanner.Text()
		size += len(line)
		if size > bridgeLimit {
			return errors.New("SSE event exceeds namespace bridge limit")
		}
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			lines, size = nil, 0
		} else {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}

func (b *responsesNamespaceBridge) adaptResponse(r *http.Response) error {
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return nil
	}
	if encoding := strings.TrimSpace(r.Header.Get("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
		_ = r.Body.Close()
		return errors.New("encoded response cannot restore namespace tool identities")
	}
	original := r.Body
	r.Header.Del("Content-Length")
	r.Header.Del("ETag")
	r.Header.Del("Content-MD5")
	r.Header.Del("Digest")
	r.ContentLength = -1
	if strings.Contains(r.Header.Get("Content-Type"), "text/event-stream") {
		reader, writer := io.Pipe()
		r.Body = &bridgeReadCloser{Reader: reader, close: func() error { _ = reader.Close(); return original.Close() }}
		go func() {
			defer original.Close()
			_ = writer.CloseWithError(b.transformSSE(original, writer))
		}()
		return nil
	}
	defer original.Close()
	raw, err := io.ReadAll(io.LimitReader(original, bridgeLimit+1))
	if err != nil {
		return err
	}
	if len(raw) > bridgeLimit {
		return errors.New("response exceeds namespace bridge limit")
	}
	doc, err := decodeObject(raw)
	if err != nil {
		return err
	}
	if err := b.restoreResponse(doc); err != nil {
		return err
	}
	raw, err = json.Marshal(doc)
	if err != nil {
		return err
	}
	if len(raw) > bridgeLimit {
		return errors.New("adapted response exceeds namespace bridge limit")
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	r.ContentLength = int64(len(raw))
	return nil
}
