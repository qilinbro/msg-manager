package adapter

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"
)

// DecodeJSON rejects duplicate keys, trailing objects, excessive nesting and
// invalid UTF-8. Numeric identifiers must be decoded from RawMessage, never
// through float64.
func DecodeJSON(raw []byte, target any) error {
	if len(raw) == 0 || len(raw) > maxResponseBytes || !utf8.Valid(raw) {
		return Failure(ProtocolError)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := validateValue(d, 0); err != nil {
		return Failure(ProtocolError)
	}
	if _, err := d.Token(); err != io.EOF {
		return Failure(ProtocolError)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return Failure(ProtocolError)
	}
	return nil
}

func validateValue(d *json.Decoder, depth int) error {
	if depth > 32 {
		return Failure(ProtocolError)
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
				return Failure(ProtocolError)
			}
			seen[name] = true
			if err := validateValue(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return Failure(ProtocolError)
		}
	case '[':
		for d.More() {
			if err := validateValue(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return Failure(ProtocolError)
		}
	default:
		return Failure(ProtocolError)
	}
	return nil
}

// ExactID accepts a JSON integer or a quoted decimal string and returns its
// canonical decimal form. It never uses floating point conversion.
func ExactID(raw json.RawMessage) (string, bool) {
	s := strings.TrimSpace(string(raw))
	if strings.HasPrefix(s, `"`) {
		if json.Unmarshal(raw, &s) != nil {
			return "", false
		}
	}
	if s == "" || len(s) > 128 {
		return "", false
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return "", false
		}
	}
	s = strings.TrimLeft(s, "0")
	return s, s != ""
}
