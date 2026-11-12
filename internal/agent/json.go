package agent

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

const maxResponseBytes = 1 << 20

func decodeJSON(raw []byte, target any, strict bool) error {
	if len(raw) < 1 || len(raw) > maxResponseBytes || !utf8.Valid(raw) {
		return failure(InvalidOutput)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if !visitJSON(d, 0) {
		return failure(InvalidOutput)
	}
	if _, err := d.Token(); err != io.EOF {
		return failure(InvalidOutput)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if strict {
		decoder.DisallowUnknownFields()
	}
	if decoder.Decode(target) != nil {
		return failure(InvalidOutput)
	}
	return nil
}

func visitJSON(d *json.Decoder, depth int) bool {
	if depth > 24 {
		return false
	}
	token, err := d.Token()
	if err != nil {
		return false
	}
	delim, container := token.(json.Delim)
	if !container {
		return true
	}
	switch delim {
	case '{':
		keys := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return false
			}
			name, ok := key.(string)
			if !ok || keys[name] {
				return false
			}
			keys[name] = true
			if !visitJSON(d, depth+1) {
				return false
			}
		}
		end, err := d.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		for d.More() {
			if !visitJSON(d, depth+1) {
				return false
			}
		}
		end, err := d.Token()
		return err == nil && end == json.Delim(']')
	default:
		return false
	}
}
