package agent

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAssistantChatToolsRejectsHistoricCallIDReplay(t *testing.T) {
	client, _ := localClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, assistantToolCompletion()) })
	input := assistantToolInput()
	input.Messages = append(input.Messages,
		ToolMessage{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Type: "function", Function: ToolFunctionCall{Name: "get_system_status", Arguments: "{}"}}}},
		ToolMessage{Role: "tool", ToolCallID: "call_1", Content: `{"ok":true}`},
		ToolMessage{Role: "user", Content: "重新检查，不得重用旧调用ID"})
	_, err := client.ChatTools(context.Background(), input)
	kindIs(t, err, InvalidOutput)
}

func TestAssistantChatToolsCredentialEscapesCannotBypassRedaction(t *testing.T) {
	for _, body := range []string{
		strings.Replace(assistantToolCompletion(), `"content":null`, `"content":"\u0073ynthetic-provider-secret"`, 1),
		strings.Replace(assistantToolCompletion(), `"arguments":"{}"`, `"arguments":"{\"q\":\"\\u0073ynthetic-provider-secret\"}"`, 1),
		strings.Replace(assistantToolCompletion(), `"content":null`, `"content":"\u0073ynthetic-provider-secret","annotations":{"ignored_number":1e9999}`, 1),
	} {
		client, _ := localClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) })
		result, err := client.ChatTools(context.Background(), assistantToolInput())
		kindIs(t, err, InvalidOutput)
		if result.Message.Content != "" || len(result.Message.ToolCalls) != 0 {
			t.Fatal("escaped credential reached caller")
		}
	}
	var calls atomic.Int32
	client, _ := localClient(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	input := assistantToolInput()
	input.Messages[1].Content = "用户粘贴了synthetic-provider-secret，不应发送给模型"
	_, err := client.ChatTools(context.Background(), input)
	kindIs(t, err, InvalidInput)
	if calls.Load() != 0 {
		t.Fatal("provider key was sent in model request body")
	}
}
