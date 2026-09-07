package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

type tomlEdit struct {
	start, end int
	data       []byte
}
type tomlAddition struct {
	path  []string
	value any
}
type tomlSection struct {
	path   []string
	offset int
}

func tomlAt(root map[string]any, path []string) (any, bool) {
	var current any = root
	for _, key := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = m[key]
		if !ok {
			return nil, false
		}
	}
	return current, true
}
func pathPrefix(path, prefix []string) bool {
	return len(path) >= len(prefix) && reflect.DeepEqual(path[:len(prefix)], prefix)
}
func tomlLiteral(value any) ([]byte, error) {
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).SetTablesInline(true).Encode(map[string]any{"value": value}); err != nil {
		return nil, err
	}
	_, raw, ok := bytes.Cut(b.Bytes(), []byte(" = "))
	if !ok {
		return nil, errors.New("Cannot format TOML value")
	}
	return bytes.TrimSuffix(raw, []byte("\n")), nil
}

// Keep comments and untouched bytes. AST ranges handle multiline strings,
// quoted/dotted keys and inline tables without interpreting their contents as lines.
func editCodexTOML(data []byte, before, after map[string]any) ([]byte, error) {
	if reflect.DeepEqual(before, after) {
		return data, nil
	}
	var additions []tomlAddition
	var changes func(map[string]any, map[string]any, []string)
	changes = func(old, next map[string]any, prefix []string) {
		keys := make([]string, 0, len(next))
		for key := range next {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := next[key]
			previous, exists := old[key]
			if exists && reflect.DeepEqual(previous, value) {
				continue
			}
			path := append(append([]string{}, prefix...), key)
			if m, ok := value.(map[string]any); ok && len(m) > 0 {
				prior, _ := previous.(map[string]any)
				changes(prior, m, path)
			} else {
				additions = append(additions, tomlAddition{path, value})
			}
		}
	}
	changes(before, after, nil)
	var edits []tomlEdit
	var covered [][]string
	sections := []tomlSection{{path: []string{}, offset: 0}}
	var section []string
	var arrayRoots [][]string
	var parser unstable.Parser
	parser.Reset(data)
	for parser.NextExpression() {
		node := parser.Expression()
		if node.Kind != unstable.Table && node.Kind != unstable.ArrayTable && node.Kind != unstable.KeyValue {
			continue
		}
		var keys []string
		it := node.Key()
		for it.Next() {
			keys = append(keys, string(it.Node().Data))
		}
		if node.Kind == unstable.Table || node.Kind == unstable.ArrayTable {
			section = keys
			if node.Kind == unstable.ArrayTable {
				arrayRoots = append(arrayRoots, keys)
				continue
			}
			insideArray := false
			for _, root := range arrayRoots {
				if pathPrefix(section, root) {
					insideArray = true
					break
				}
			}
			if insideArray {
				continue
			}
			keyIt := node.Key()
			keyIt.Next()
			offset := int(keyIt.Node().Raw.Offset)
			start := bytes.LastIndexByte(data[:offset], '\n') + 1
			end := len(data)
			if i := bytes.IndexByte(data[offset:], '\n'); i >= 0 {
				end = offset + i + 1
			}
			if _, exists := tomlAt(after, keys); !exists {
				edits = append(edits, tomlEdit{start, end, nil})
			} else {
				sections = append(sections, tomlSection{keys, end})
			}
			continue
		}
		insideArray := false
		for _, root := range arrayRoots {
			if pathPrefix(section, root) {
				insideArray = true
				break
			}
		}
		if insideArray {
			continue
		}
		path := append(append([]string{}, section...), keys...)
		covered = append(covered, path)
		previous, _ := tomlAt(before, path)
		value, exists := tomlAt(after, path)
		start, end := int(node.Raw.Offset), int(node.Raw.Offset+node.Raw.Length)
		if !exists {
			edits = append(edits, tomlEdit{start, end, nil})
			continue
		}
		if reflect.DeepEqual(previous, value) {
			continue
		}
		literal, err := tomlLiteral(value)
		if err != nil {
			return nil, err
		}
		edits = append(edits, tomlEdit{int(node.Value().Raw.Offset), end, literal})
	}
	if err := parser.Error(); err != nil {
		return nil, errors.New("Cannot parse Codex settings for editing")
	}
	inserts := map[int][]byte{}
	eol := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		eol = "\r\n"
	}
	for _, addition := range additions {
		found := false
		for _, path := range covered {
			if pathPrefix(addition.path, path) {
				found = true
				break
			}
		}
		if found {
			continue
		}
		best := sections[0]
		for _, candidate := range sections {
			if pathPrefix(addition.path, candidate.path) && len(addition.path) > len(candidate.path) && len(candidate.path) > len(best.path) {
				best = candidate
			}
		}
		keys := make([]string, 0, len(addition.path)-len(best.path))
		for _, key := range addition.path[len(best.path):] {
			quoted, _ := json.Marshal(key)
			keys = append(keys, string(quoted))
		}
		literal, err := tomlLiteral(addition.value)
		if err != nil {
			return nil, err
		}
		if best.offset > 0 && data[best.offset-1] != '\n' && len(inserts[best.offset]) == 0 {
			inserts[best.offset] = []byte(eol)
		}
		inserts[best.offset] = append(inserts[best.offset], []byte(strings.Join(keys, ".")+" = "+string(literal)+eol)...)
	}
	for offset, data := range inserts {
		edits = append(edits, tomlEdit{offset, offset, data})
	}
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].start == edits[j].start {
			return edits[i].end < edits[j].end
		}
		return edits[i].start < edits[j].start
	})
	var out bytes.Buffer
	cursor := 0
	for _, edit := range edits {
		if edit.start < cursor {
			return nil, errors.New("Overlapping Codex settings edits; no changes saved")
		}
		out.Write(data[cursor:edit.start])
		out.Write(edit.data)
		cursor = edit.end
	}
	out.Write(data[cursor:])
	var checked map[string]any
	if err := toml.Unmarshal(out.Bytes(), &checked); err != nil || !reflect.DeepEqual(checked, after) {
		return nil, errors.New("Cannot safely merge this TOML layout; no profile changes saved")
	}
	if out.Len() > catalogLimit {
		return nil, errors.New("Updated Codex settings exceed the size limit")
	}
	return out.Bytes(), nil
}
