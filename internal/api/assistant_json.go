package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var errAssistantJSON = errors.New("invalid assistant JSON")

// A deliberately small schema implementation. The very same definition
// publishes the provider's JSON schema and independently validates raw values.
// There are no open objects, references, coercions or case-insensitive keys.
type assistantSchema struct {
	kind          string
	properties    map[string]*assistantSchema
	required      []string
	items         *assistantSchema
	alternatives  []*assistantSchema
	choices       []string
	min, max      int64
	minProperties int
	pattern       string
	text          bool
}

func assistantObject(properties map[string]*assistantSchema, required ...string) *assistantSchema {
	if properties == nil {
		properties = map[string]*assistantSchema{}
	}
	return &assistantSchema{kind: "object", properties: properties, required: required}
}
func assistantString(min, max int) *assistantSchema {
	return &assistantSchema{kind: "string", min: int64(min), max: int64(max)}
}
func assistantText(min, max int) *assistantSchema {
	value := assistantString(min, max)
	value.text = true
	return value
}
func assistantEnum(values ...string) *assistantSchema {
	return &assistantSchema{kind: "string", choices: values}
}
func assistantInteger(min, max int64) *assistantSchema {
	return &assistantSchema{kind: "integer", min: min, max: max}
}
func assistantBoolean() *assistantSchema { return &assistantSchema{kind: "boolean"} }
func assistantArray(item *assistantSchema, min, max int) *assistantSchema {
	return &assistantSchema{kind: "array", items: item, min: int64(min), max: int64(max)}
}
func assistantIDSchema() *assistantSchema {
	return &assistantSchema{kind: "string", min: 1, max: 128, pattern: `^[A-Za-z0-9_-]{1,128}$`}
}
func assistantHashSchema() *assistantSchema {
	return &assistantSchema{kind: "string", min: 64, max: 64, pattern: `^[a-f0-9]{64}$`}
}

func (s *assistantSchema) jsonSchema() map[string]any {
	if len(s.alternatives) > 0 {
		values := make([]any, 0, len(s.alternatives))
		for _, alternative := range s.alternatives {
			values = append(values, alternative.jsonSchema())
		}
		return map[string]any{"anyOf": values}
	}
	value := map[string]any{"type": s.kind}
	switch s.kind {
	case "object":
		properties := map[string]any{}
		for name, property := range s.properties {
			properties[name] = property.jsonSchema()
		}
		required := append([]string{}, s.required...)
		value["properties"], value["required"], value["additionalProperties"] = properties, required, false
		if s.minProperties > 0 {
			value["minProperties"] = s.minProperties
		}
	case "string":
		if len(s.choices) > 0 {
			value["enum"] = s.choices
		} else {
			value["minLength"], value["maxLength"] = s.min, s.max
			// Business APIs additionally enforce UTF-8 byte bounds. This schema
			// cannot express those, so the independent validator also checks them.
			if s.pattern != "" {
				value["pattern"] = s.pattern
			}
		}
	case "integer":
		value["minimum"], value["maximum"] = s.min, s.max
	case "array":
		value["items"], value["minItems"], value["maxItems"] = s.items.jsonSchema(), s.min, s.max
		value["uniqueItems"] = false
	}
	return value
}

func (s *assistantSchema) validate(value any) bool {
	if len(s.alternatives) > 0 {
		for _, alternative := range s.alternatives {
			if alternative.validate(value) {
				return true
			}
		}
		return false
	}
	switch s.kind {
	case "object":
		fields, ok := value.(map[string]any)
		if !ok || len(fields) < s.minProperties {
			return false
		}
		for _, field := range s.required {
			if _, exists := fields[field]; !exists {
				return false
			}
		}
		for name, value := range fields {
			property := s.properties[name]
			if property == nil || !property.validate(value) {
				return false
			}
		}
		return true
	case "string":
		text, ok := value.(string)
		if !ok || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
			return false
		}
		if len(s.choices) > 0 {
			for _, choice := range s.choices {
				if text == choice {
					return true
				}
			}
			return false
		}
		if int64(utf8.RuneCountInString(text)) < s.min || int64(len(text)) > s.max || s.pattern != "" && !regexp.MustCompile(s.pattern).MatchString(text) {
			return false
		}
		return s.text || strings.IndexFunc(text, unicode.IsControl) < 0
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		integer, err := number.Int64()
		return err == nil && integer >= s.min && integer <= s.max
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "array":
		values, ok := value.([]any)
		if !ok || int64(len(values)) < s.min || int64(len(values)) > s.max {
			return false
		}
		for _, item := range values {
			if !s.items.validate(item) {
				return false
			}
		}
		return true
	}
	return false
}

// Decode tokens once to reject duplicate keys at every depth before a map or
// struct could discard them. Exact field names are then enforced by the schema.
func assistantDecode(raw []byte, max int) (any, error) {
	if len(raw) < 1 || len(raw) > max || !utf8.Valid(raw) {
		return nil, errAssistantJSON
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := assistantJSONValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errAssistantJSON
	}
	return value, nil
}

func assistantJSONValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 24 {
		return nil, errAssistantJSON
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, errAssistantJSON
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return token, nil
	}
	switch delimiter {
	case '{':
		fields := map[string]any{}
		for decoder.More() {
			token, err := decoder.Token()
			field, ok := token.(string)
			if err != nil || !ok {
				return nil, errAssistantJSON
			}
			if _, exists := fields[field]; exists {
				return nil, errAssistantJSON
			}
			value, err := assistantJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			fields[field] = value
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return nil, errAssistantJSON
		}
		return fields, nil
	case '[':
		values := []any{}
		for decoder.More() {
			value, err := assistantJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return nil, errAssistantJSON
		}
		return values, nil
	default:
		return nil, errAssistantJSON
	}
}

func readAssistantJSON(w http.ResponseWriter, r *http.Request, schema *assistantSchema, traceID string) (map[string]any, bool) {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	validParams := len(params) == 0 || len(params) == 1 && strings.EqualFold(params["charset"], "utf-8")
	if err != nil || mediaType != "application/json" || !validParams {
		writeError(w, 415, "ASSISTANT_INVALID_INPUT", "请使用UTF-8 JSON对象提交。", traceID)
		return nil, false
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, assistantMaxRequestBytes+1))
	if err != nil || len(raw) > assistantMaxRequestBytes {
		writeError(w, 413, "ASSISTANT_INVALID_INPUT", "助手输入超过本次允许范围。", traceID)
		return nil, false
	}
	value, err := assistantDecode(raw, assistantMaxRequestBytes)
	if err != nil || !schema.validate(value) {
		writeError(w, 400, "ASSISTANT_INVALID_INPUT", "助手参数不符合严格JSON合同；请检查必填项、类型与字段。", traceID)
		return nil, false
	}
	return value.(map[string]any), true
}

func assistantNumber(value any) int64 {
	number, _ := value.(json.Number)
	result, _ := strconv.ParseInt(string(number), 10, 64)
	return result
}
