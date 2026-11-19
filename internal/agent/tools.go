package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// ToolProvider is deliberately separate from the suggestion-only Provider.
// The caller owns authorization, consent, quotas, tool execution and history.
// ChatTools performs exactly one bounded provider request and executes nothing.
type ToolProvider interface {
	ChatTools(context.Context, ToolInput) (ToolResult, error)
}

const (
	MaxToolInputBytes    = 512 << 10
	MaxToolResponseBytes = 64 << 10
	MaxToolArgumentBytes = 32 << 10
)

type ToolDefinition struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

type ToolFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolFunctionCall `json:"function"`
}

type ToolMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolInput struct {
	Messages []ToolMessage
	Tools    []ToolDefinition
}

type ToolResult struct {
	Message ToolMessage `json:"message"`
	Usage   Usage       `json:"usage"`
}

var _ ToolProvider = (*Client)(nil)

type toolRequestFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type toolRequestDefinition struct {
	Type     string              `json:"type"`
	Function toolRequestFunction `json:"function"`
}

type toolCompletionRequest struct {
	Model             string                  `json:"model"`
	Messages          []ToolMessage           `json:"messages"`
	Tools             []toolRequestDefinition `json:"tools"`
	ToolChoice        string                  `json:"tool_choice"`
	ParallelToolCalls bool                    `json:"parallel_tool_calls"`
	Temperature       float64                 `json:"temperature"`
	MaxTokens         int                     `json:"max_tokens"`
	Stream            bool                    `json:"stream"`
	Thinking          *struct {
		Type string `json:"type"`
	} `json:"thinking,omitempty"`
}

func toolIdentifier(value string, max int) bool {
	if len(value) < 1 || len(value) > max {
		return false
	}
	for _, ch := range value {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
			return false
		}
	}
	return true
}

func toolArgumentsValid(value string) bool {
	if len(value) < 2 || len(value) > MaxToolArgumentBytes || !utf8.ValidString(value) {
		return false
	}
	var fields map[string]json.RawMessage
	return decodeJSON([]byte(value), &fields, false) == nil && fields != nil
}

func validateToolInput(input ToolInput) error {
	if len(input.Messages) < 2 || len(input.Messages) > 65 || len(input.Tools) < 1 || len(input.Tools) > 64 {
		return failure(InvalidInput)
	}
	names := map[string]bool{}
	for _, definition := range input.Tools {
		if !toolIdentifier(definition.Name, 64) || names[definition.Name] || !utf8.ValidString(definition.Description) || len(definition.Description) > 4096 {
			return failure(InvalidInput)
		}
		var schema map[string]json.RawMessage
		if decodeJSON(definition.Parameters, &schema, false) != nil || schema == nil || string(schema["type"]) != `"object"` || string(schema["additionalProperties"]) != "false" {
			return failure(InvalidInput)
		}
		names[definition.Name] = true
	}
	pending := ""
	usedCalls := map[string]bool{}
	for index, message := range input.Messages {
		if !utf8.ValidString(message.Content) || len(message.Content) > 256<<10 {
			return failure(InvalidInput)
		}
		if pending != "" && message.Role != "tool" {
			return failure(InvalidInput)
		}
		switch message.Role {
		case "system":
			if index != 0 || message.Content == "" || len(message.ToolCalls) != 0 || message.ToolCallID != "" {
				return failure(InvalidInput)
			}
		case "user":
			if index == 0 || strings.TrimSpace(message.Content) == "" || len(message.ToolCalls) != 0 || message.ToolCallID != "" {
				return failure(InvalidInput)
			}
		case "assistant":
			if index == 0 || message.ToolCallID != "" || len(message.ToolCalls) > 1 || len(message.ToolCalls) == 0 && strings.TrimSpace(message.Content) == "" {
				return failure(InvalidInput)
			}
			for _, call := range message.ToolCalls {
				if call.Type != "function" || !toolIdentifier(call.ID, 128) || usedCalls[call.ID] || !names[call.Function.Name] || !toolArgumentsValid(call.Function.Arguments) {
					return failure(InvalidInput)
				}
				usedCalls[call.ID] = true
				pending = call.ID
			}
		case "tool":
			if pending == "" || message.ToolCallID != pending || len(message.ToolCalls) != 0 || message.Content == "" {
				return failure(InvalidInput)
			}
			pending = ""
		default:
			return failure(InvalidInput)
		}
	}
	last := input.Messages[len(input.Messages)-1].Role
	if input.Messages[0].Role != "system" || pending != "" || last != "user" && last != "tool" {
		return failure(InvalidInput)
	}
	return nil
}

// ChatTools uses the same validated, DNS-pinned destination and redacted error
// policy as Analyze. No provider-controlled redirect or proxy receives history.
func (c *Client) ChatTools(ctx context.Context, input ToolInput) (ToolResult, error) {
	if ctx == nil || validateToolInput(input) != nil {
		return ToolResult{}, failure(InvalidInput)
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	if ctx.Err() != nil {
		return ToolResult{}, failure(Timeout)
	}
	body := toolCompletionRequest{Model: c.cfg.model, Messages: input.Messages, Tools: make([]toolRequestDefinition, 0, len(input.Tools)), ToolChoice: "auto", Temperature: 0.2, MaxTokens: 2400}
	for _, definition := range input.Tools {
		body.Tools = append(body.Tools, toolRequestDefinition{Type: "function", Function: toolRequestFunction{Name: definition.Name, Description: definition.Description, Parameters: definition.Parameters}})
	}
	if c.cfg.host == "api.deepseek.com" {
		body.Thinking = &struct {
			Type string `json:"type"`
		}{Type: "disabled"}
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) > MaxToolInputBytes {
		return ToolResult{}, failure(InvalidInput)
	}
	if toolJSONContainsSecret(encoded, c.cfg.key) {
		return ToolResult{}, failure(InvalidInput)
	}
	destination, err := c.destination(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return ToolResult{}, failure(Timeout)
		}
		return ToolResult{}, err
	}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return c.dialContext(ctx, network, destination)
	}, TLSClientConfig: &tls.Config{ServerName: c.cfg.host, RootCAs: c.roots, MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 8 * time.Second, ResponseHeaderTimeout: 20 * time.Second, MaxResponseHeaderBytes: 32 << 10, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.endpoint.String(), bytes.NewReader(encoded))
	if err != nil {
		return ToolResult{}, failure(InvalidConfig)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if c.cfg.key != "" {
		request.Header.Set("Authorization", "Bearer "+c.cfg.key)
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ToolResult{}, failure(Timeout)
		}
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			return ToolResult{}, failure(Timeout)
		}
		return ToolResult{}, failure(Unavailable)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		switch response.StatusCode {
		case 401, 403:
			return ToolResult{}, failure(AuthFailed)
		case 402, 429:
			return ToolResult{}, failure(QuotaExceeded)
		default:
			return ToolResult{}, failure(Unavailable)
		}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, MaxToolResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return ToolResult{}, failure(Timeout)
		}
		return ToolResult{}, failure(Unavailable)
	}
	if len(raw) > MaxToolResponseBytes || toolJSONContainsSecret(raw, c.cfg.key) {
		return ToolResult{}, failure(InvalidOutput)
	}
	allowToolIndex := c.cfg.host == "api.deepseek.com" && c.cfg.port == "443"
	result, err := decodeToolResult(raw, input.Tools, allowToolIndex)
	if err != nil {
		return ToolResult{}, err
	}
	for _, call := range result.Message.ToolCalls {
		for _, message := range input.Messages {
			for _, previous := range message.ToolCalls {
				if previous.ID == call.ID {
					return ToolResult{}, failure(InvalidOutput)
				}
			}
		}
	}
	return result, nil
}

// Inspect decoded strings, including the JSON string nested in native function
// arguments. Comparing raw JSON bytes would miss Unicode/HTML-escaped keys.
func toolJSONContainsSecret(raw []byte, secret string) bool {
	if secret == "" {
		return false
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return false // Normal protocol validation rejects malformed JSON.
	}
	return toolValueContainsSecret(value, secret, 0)
}

func toolValueContainsSecret(value any, secret string, depth int) bool {
	if depth > 40 {
		return true // Fail closed; valid protocol/schema objects are shallower.
	}
	switch typed := value.(type) {
	case map[string]any:
		for name, item := range typed {
			if strings.Contains(name, secret) || toolValueContainsSecret(item, secret, depth+1) {
				return true
			}
			if name == "arguments" {
				if arguments, ok := item.(string); ok && toolJSONContainsSecret([]byte(arguments), secret) {
					return true
				}
			}
		}
	case []any:
		for _, item := range typed {
			if toolValueContainsSecret(item, secret, depth+1) {
				return true
			}
		}
	case string:
		return strings.Contains(typed, secret)
	}
	return false
}

// Parse exact-case protocol fields rather than relying on encoding/json's
// case-insensitive struct matching. Envelope metadata is intentionally ignored.
func decodeToolResult(raw []byte, definitions []ToolDefinition, allowToolIndex bool) (ToolResult, error) {
	var envelope map[string]json.RawMessage
	if decodeJSON(raw, &envelope, false) != nil {
		return ToolResult{}, failure(InvalidOutput)
	}
	var choices []map[string]json.RawMessage
	if json.Unmarshal(envelope["choices"], &choices) != nil || len(choices) != 1 {
		return ToolResult{}, failure(InvalidOutput)
	}
	var finish string
	var message map[string]json.RawMessage
	if json.Unmarshal(choices[0]["finish_reason"], &finish) != nil || json.Unmarshal(choices[0]["message"], &message) != nil || message == nil {
		return ToolResult{}, failure(InvalidOutput)
	}
	allowed := map[string]bool{"role": true, "content": true, "tool_calls": true, "refusal": true, "function_call": true, "reasoning_content": true, "annotations": true}
	for field := range message {
		if !allowed[field] {
			return ToolResult{}, failure(InvalidOutput)
		}
	}
	var role, content string
	if json.Unmarshal(message["role"], &role) != nil || role != "assistant" {
		return ToolResult{}, failure(InvalidOutput)
	}
	if value, exists := message["content"]; exists && string(value) != "null" && json.Unmarshal(value, &content) != nil {
		return ToolResult{}, failure(InvalidOutput)
	}
	if value, exists := message["function_call"]; exists && string(value) != "null" {
		return ToolResult{}, failure(InvalidOutput)
	}
	if value, exists := message["refusal"]; exists && string(value) != "null" && string(value) != `""` {
		return ToolResult{}, failure(InvalidOutput)
	}
	result := ToolResult{Message: ToolMessage{Role: "assistant", Content: content}}
	if value, exists := message["tool_calls"]; exists && string(value) != "null" {
		var calls []map[string]json.RawMessage
		if json.Unmarshal(value, &calls) != nil || len(calls) > 1 {
			return ToolResult{}, failure(InvalidOutput)
		}
		for _, fields := range calls {
			fieldCount := 3
			if index, exists := fields["index"]; exists {
				// DeepSeek includes this ordinal even in non-streaming replies.
				if !allowToolIndex || string(index) != "0" {
					return ToolResult{}, failure(InvalidOutput)
				}
				fieldCount++
			}
			if len(fields) != fieldCount || fields["id"] == nil || fields["type"] == nil || fields["function"] == nil {
				return ToolResult{}, failure(InvalidOutput)
			}
			var function map[string]json.RawMessage
			if json.Unmarshal(fields["function"], &function) != nil || len(function) != 2 || function["name"] == nil || function["arguments"] == nil {
				return ToolResult{}, failure(InvalidOutput)
			}
			var call ToolCall
			if json.Unmarshal(fields["id"], &call.ID) != nil || json.Unmarshal(fields["type"], &call.Type) != nil || json.Unmarshal(function["name"], &call.Function.Name) != nil || json.Unmarshal(function["arguments"], &call.Function.Arguments) != nil {
				return ToolResult{}, failure(InvalidOutput)
			}
			known := false
			for _, definition := range definitions {
				known = known || definition.Name == call.Function.Name
			}
			if call.Type != "function" || !toolIdentifier(call.ID, 128) || !known || !toolArgumentsValid(call.Function.Arguments) {
				return ToolResult{}, failure(InvalidOutput)
			}
			result.Message.ToolCalls = append(result.Message.ToolCalls, call)
		}
	}
	if finish == "tool_calls" {
		if len(result.Message.ToolCalls) != 1 {
			return ToolResult{}, failure(InvalidOutput)
		}
	} else if finish != "stop" || len(result.Message.ToolCalls) != 0 || strings.TrimSpace(content) == "" {
		return ToolResult{}, failure(InvalidOutput)
	}
	if value, exists := envelope["usage"]; exists && string(value) != "null" {
		var usage map[string]json.RawMessage
		if json.Unmarshal(value, &usage) != nil || usage["prompt_tokens"] == nil || usage["completion_tokens"] == nil || string(usage["prompt_tokens"]) == "null" || string(usage["completion_tokens"]) == "null" || json.Unmarshal(usage["prompt_tokens"], &result.Usage.PromptTokens) != nil || json.Unmarshal(usage["completion_tokens"], &result.Usage.CompletionTokens) != nil || result.Usage.PromptTokens < 0 || result.Usage.CompletionTokens < 0 || result.Usage.PromptTokens > 1000000 || result.Usage.CompletionTokens > 1000000 {
			return ToolResult{}, failure(InvalidOutput)
		}
	}
	return result, nil
}
