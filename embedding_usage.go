package main

import (
	"bytes"
	"encoding/json"
	"errors"
)

const embeddingUsageMetadataLimit = 64 << 10
const embeddingUsageDepthLimit = 128

// This scanner recognizes the outer object and skips unknown values, including
// vectors, without decoding or retaining them. Only selected metadata is decoded
// by encoding/json. Each request owns its scanner; no worker or pipe is needed.
type embeddingUsageExtractor struct {
	state                      byte
	allowEnd                   bool
	key                        [256]byte
	keyLen                     int
	longKey, escaped, inString bool
	selected                   int
	kind                       byte
	stack                      [embeddingUsageDepthLimit]byte
	depth                      int
	raw                        []byte
	start                      int
	fields                     [3][]byte
	errorCount                 int
	errorNull, hasError        bool
	err                        error
	finished                   bool
	result                     []byte
}

const (
	embedRoot byte = iota
	embedKey
	embedKeyString
	embedColon
	embedValue
	embedInValue
	embedAfter
	embedDone
)

func newEmbeddingUsageExtractor() *embeddingUsageExtractor {
	return &embeddingUsageExtractor{selected: -1, raw: make([]byte, 0, embeddingUsageMetadataLimit)}
}

func embeddingSpace(b byte) bool { return b == ' ' || b == '\n' || b == '\r' || b == '\t' }

func (x *embeddingUsageExtractor) fail() {
	x.err = errors.New("invalid or limited embedding usage metadata")
	x.raw = nil
	x.fields = [3][]byte{}
}

func (x *embeddingUsageExtractor) capture(b byte) {
	if x.selected >= 0 {
		if len(x.raw) == embeddingUsageMetadataLimit {
			x.fail()
			return
		}
		x.raw = append(x.raw, b)
	} else if x.selected == -2 {
		if x.errorCount >= 4 || b != "null"[x.errorCount] {
			x.errorNull = false
		}
		if x.errorCount < 5 {
			x.errorCount++
		}
	}
}

func (x *embeddingUsageExtractor) push(b byte) {
	if x.depth == embeddingUsageDepthLimit {
		x.fail()
		return
	}
	x.stack[x.depth] = b
	x.depth++
}

func (x *embeddingUsageExtractor) endValue() {
	if x.selected >= 0 {
		value := x.raw[x.start:]
		// Validate each occurrence, not only the last duplicate. A later valid
		// value cannot repair invalid JSON earlier in the upstream response.
		if !json.Valid(value) {
			x.fail()
			return
		}
		x.fields[x.selected] = value
	}
	if x.selected == -2 {
		x.hasError = !(x.errorNull && x.errorCount == 4)
	}
	x.state = embedAfter
}

func (x *embeddingUsageExtractor) keyByte(b byte) {
	if x.keyLen < len(x.key) {
		x.key[x.keyLen] = b
		x.keyLen++
	} else {
		x.longKey = true
	}
}

func (x *embeddingUsageExtractor) feed(data []byte) {
	if x.finished || x.err != nil {
		return
	}
	for _, b := range data {
		x.consume(b)
		if x.err != nil {
			return
		}
	}
}

func (x *embeddingUsageExtractor) consume(b byte) {
	for {
		if x.err != nil {
			return
		}
		switch x.state {
		case embedRoot:
			if embeddingSpace(b) {
				return
			}
			if b != '{' {
				x.fail()
				return
			}
			x.stack[0] = '{'
			x.depth = 1
			x.state = embedKey
			x.allowEnd = true
			return
		case embedKey:
			if embeddingSpace(b) {
				return
			}
			if b == '}' && x.allowEnd {
				x.depth = 0
				x.state = embedDone
				return
			}
			if b != '"' {
				x.fail()
				return
			}
			x.keyLen = 0
			x.longKey = false
			x.escaped = false
			x.keyByte(b)
			x.state = embedKeyString
			return
		case embedKeyString:
			x.keyByte(b)
			if x.escaped {
				x.escaped = false
				return
			}
			if b == '\\' {
				x.escaped = true
				return
			}
			if b != '"' {
				return
			}
			x.selected = -1
			if !x.longKey {
				var key string
				if json.Unmarshal(x.key[:x.keyLen], &key) != nil {
					x.fail()
					return
				}
				switch key {
				case "model":
					x.selected = 0
				case "usage":
					x.selected = 1
				case "provider_metadata":
					x.selected = 2
				case "error":
					x.selected = -2
				}
			}
			x.state = embedColon
			return
		case embedColon:
			if embeddingSpace(b) {
				return
			}
			if b != ':' {
				x.fail()
				return
			}
			x.state = embedValue
			return
		case embedValue:
			if embeddingSpace(b) {
				return
			}
			if b == ',' || b == '}' || b == ']' || b == ':' {
				x.fail()
				return
			}
			x.start = len(x.raw)
			x.errorCount = 0
			x.errorNull = true
			x.escaped = false
			x.inString = false
			x.state = embedInValue
			x.capture(b)
			if x.err != nil {
				return
			}
			switch b {
			case '"':
				x.kind = 's'
				x.inString = true
			case '{', '[':
				x.kind = 'c'
				x.push(b)
			default:
				x.kind = 'p'
			}
			return
		case embedInValue:
			if x.kind == 'p' && (embeddingSpace(b) || b == ',' || b == '}') {
				x.endValue()
				continue
			}
			x.capture(b)
			if x.err != nil {
				return
			}
			if x.inString {
				if x.escaped {
					x.escaped = false
					return
				}
				if b == '\\' {
					x.escaped = true
					return
				}
				if b == '"' {
					x.inString = false
					if x.kind == 's' {
						x.endValue()
					}
				}
				return
			}
			if b == '"' {
				x.inString = true
				return
			}
			if b == '{' || b == '[' {
				x.push(b)
				return
			}
			if b == '}' || b == ']' {
				if x.depth <= 1 || (b == '}' && x.stack[x.depth-1] != '{') || (b == ']' && x.stack[x.depth-1] != '[') {
					x.fail()
					return
				}
				x.depth--
				if x.depth == 1 {
					x.endValue()
				}
			}
			return
		case embedAfter:
			if embeddingSpace(b) {
				return
			}
			switch b {
			case ',':
				x.state = embedKey
				x.allowEnd = false
			case '}':
				x.depth = 0
				x.state = embedDone
			default:
				x.fail()
			}
			return
		case embedDone:
			if !embeddingSpace(b) {
				x.fail()
			}
			return
		}
	}
}

func (x *embeddingUsageExtractor) finish() ([]byte, error) {
	if x.finished {
		return x.result, x.err
	}
	x.finished = true
	if x.err != nil {
		return nil, x.err
	}
	if x.state != embedDone {
		x.fail()
		return nil, x.err
	}
	envelope := make(map[string]json.RawMessage)
	for i, key := range []string{"model", "usage", "provider_metadata"} {
		value := x.fields[i]
		if value == nil {
			continue
		}
		if !json.Valid(value) {
			x.fail()
			return nil, x.err
		}
		if key == "usage" && !bytes.Equal(value, []byte("null")) && (len(value) == 0 || value[0] != '{') {
			x.fail()
			return nil, x.err
		}
		envelope[key] = value
	}
	if x.hasError {
		envelope["error"] = json.RawMessage("true")
	}
	x.result, x.err = json.Marshal(envelope)
	return x.result, x.err
}

func (u *usageObserver) setEmbeddingModel(model string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.embedding != nil && len(model) <= 256 {
		u.embeddingModel = model
		u.usage.Model = model
	}
}

// Called under the observer lock, only after the upstream reader returned EOF.
// HTTP delivery and metadata completeness are intentionally separate signals.
func (u *usageObserver) finishEmbedding() {
	u.embeddingResponseComplete = u.responseStatus >= 200 && u.responseStatus < 300
	data, err := u.embedding.finish()
	if err != nil {
		u.usage.Limited = true
		return
	}
	if !u.embeddingResponseComplete {
		return
	}
	root, err := decodeObject(data)
	if err != nil {
		u.usage.Limited = true
		return
	}
	usage := usageObject(root["usage"])
	if usage == nil || root["error"] != nil {
		return
	}
	u.parse(data)
	if u.embeddingModel != "" {
		u.usage.Model = u.embeddingModel
	}
	if byok, ok := usage["is_byok"].(bool); ok && u.usage.Billing != "litellm" {
		u.usage.BYOK = &byok
	}
}

func (u *usageObserver) embeddingReadError() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.embedding != nil {
		u.embedding.fail()
		u.usage.Limited = true
		u.ended = true
	}
}
