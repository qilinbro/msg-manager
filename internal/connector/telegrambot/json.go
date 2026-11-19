package telegrambot

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The Python bridge also checks this boundary. Repeat the bounded, duplicate-
// free check here rather than relaxing the Milky transport's smaller JSON cap.
func decodeResult(data []byte, target any) error {
	if len(data) == 0 || len(data) > 4<<20 || !utf8.Valid(data) || !pairedJSONSurrogates(data) {
		return protocol()
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return protocol()
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 32 {
			return false
		}
		token, err := d.Token()
		if err != nil {
			return false
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return false
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return false
				}
				seen[name] = true
				if !value(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for d.More() {
				if !value(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim(']')
		}
		return false
	}
	if !value(0) {
		return protocol()
	}
	if _, err := d.Token(); err != io.EOF {
		return protocol()
	}
	if !exactJSONShape(data, reflect.TypeOf(target)) {
		return protocol()
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return protocol()
	}
	return nil
}

// encoding/json matches struct tags case-insensitively and accepts null scalar
// values. IPC does neither: every non-omitempty field must occur exactly once
// under its canonical tag, and an optional message must be absent, not null.
func exactJSONShape(data json.RawMessage, t reflect.Type) bool {
	if t == nil || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return false
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) != nil || fields == nil {
			return false
		}
		allowed := map[string]reflect.StructField{}
		var collect func(reflect.Type)
		collect = func(kind reflect.Type) {
			for i := 0; i < kind.NumField(); i++ {
				field := kind.Field(i)
				if field.PkgPath != "" {
					continue
				}
				if field.Anonymous {
					embedded := field.Type
					for embedded.Kind() == reflect.Pointer {
						embedded = embedded.Elem()
					}
					if embedded.Kind() == reflect.Struct {
						collect(embedded)
						continue
					}
				}
				name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
				if name != "" && name != "-" {
					allowed[name] = field
				}
			}
		}
		collect(t)
		for name, field := range allowed {
			raw, present := fields[name]
			if !present {
				if !strings.Contains(field.Tag.Get("json"), ",omitempty") {
					return false
				}
				continue
			}
			if !exactJSONShape(raw, field.Type) {
				return false
			}
		}
		for name := range fields {
			if _, ok := allowed[name]; !ok {
				return false
			}
		}
	case reflect.Slice:
		var elements []json.RawMessage
		if json.Unmarshal(data, &elements) != nil || elements == nil {
			return false
		}
		for _, raw := range elements {
			if !exactJSONShape(raw, t.Elem()) {
				return false
			}
		}
	}
	return true
}

// Reject escaped lone UTF-16 surrogates instead of letting encoding/json
// silently replace them with U+FFFD and change an original message body.
func pairedJSONSurrogates(data []byte) bool {
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return false
		}
		n, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xD800 && n <= 0xDBFF {
			if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
			if err != nil || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			i += 6
		} else if n >= 0xDC00 && n <= 0xDFFF {
			return false
		}
	}
	return true
}
