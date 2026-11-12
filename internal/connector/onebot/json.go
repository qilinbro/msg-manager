package onebot

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"
)

const maxResponseBytes = 1 << 20

// decodeJSON rejects ambiguous duplicate keys, trailing objects and excessive
// nesting. IDs are subsequently decoded from RawMessage, never float64.
func decodeJSON(raw []byte, target any) error {
	if len(raw) == 0 || len(raw) > maxResponseBytes || !utf8.Valid(raw) {
		return failure(ProtocolError)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := validateValue(d, 0); err != nil {
		return failure(ProtocolError)
	}
	if _, err := d.Token(); err != io.EOF {
		return failure(ProtocolError)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return failure(ProtocolError)
	}
	return nil
}

func validateValue(d *json.Decoder, depth int) error {
	if depth > 32 {
		return failure(ProtocolError)
	}
	tok, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return failure(ProtocolError)
			}
			seen[name] = true
			if err := validateValue(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return failure(ProtocolError)
		}
	case '[':
		for d.More() {
			if err := validateValue(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return failure(ProtocolError)
		}
	default:
		return failure(ProtocolError)
	}
	return nil
}

func exactID(raw json.RawMessage, signed bool) (string, bool) {
	s := strings.TrimSpace(string(raw))
	if strings.HasPrefix(s, `"`) {
		if json.Unmarshal(raw, &s) != nil {
			return "", false
		}
	}
	negative := signed && strings.HasPrefix(s, "-")
	if negative {
		s = strings.TrimPrefix(s, "-")
	}
	value, ok := positiveID(s)
	if !ok {
		return "", false
	}
	if negative {
		return "-" + value, true
	}
	return value, true
}
