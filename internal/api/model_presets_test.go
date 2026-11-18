package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/config"
)

func TestModelPresetsAuthenticatedStaticAndConfigurationFree(t *testing.T) {
	// Even without storage or configured model credentials, authenticated users
	// can inspect references. Reading a preset never installs or activates it.
	service, err := NewHandler(config.Config{Addr: config.DefaultAddr, APIToken: emailTestToken})
	if err != nil {
		t.Fatal(err)
	}
	h := service.(*handler)
	t.Cleanup(func() { _ = h.Close() })
	for _, path := range []string{modelPresetsPath, modelPresetsPath + "?provider=openai"} {
		if w := emailTestRequest(h, http.MethodGet, path, "", false); w.Code != http.StatusUnauthorized {
			t.Fatal("presets bypassed authentication", w.Code)
		}
	}
	before := h.modelConfig
	w := emailTestRequest(h, http.MethodGet, modelPresetsPath, "", true)
	var response modelPresetsResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil || !reflect.DeepEqual(response, staticModelPresets()) {
		t.Fatal("unexpected preset response", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" || response.Protocol != "openai_chat_completions" || !response.ModelIDRequired || response.LiveVerificationPerformed || len(response.Items) != 6 {
		t.Fatal("static protocol/reference contract drift", response)
	}
	seen := map[string]bool{}
	refPattern := regexp.MustCompile(`^[a-f0-9]{40}$`)
	for _, preset := range response.Items {
		source, err := url.Parse(preset.SourceURL)
		if err != nil || source.Scheme != "https" || source.Host != "github.com" || !refPattern.MatchString(preset.SourceRef) || !strings.Contains(source.Path, "/"+preset.SourceRef) || preset.SourceLicense == "" || seen[preset.ID] {
			t.Fatal("preset lacks a unique pinned source", preset)
		}
		seen[preset.ID] = true
		if preset.ID == "custom" || preset.ID == "loopback" {
			if preset.ImplementationStatus != "template_only" {
				t.Fatal("unconfigured custom server advertised as verified", preset)
			}
		} else if preset.ImplementationStatus != "protocol_supported" {
			t.Fatal("documented wire protocol missing", preset)
		}
		if preset.BaseURL != "" {
			if _, err := agent.NewClient(agent.Config{BaseURL: preset.BaseURL, Model: "synthetic-user-supplied-model", APIKey: "synthetic-not-a-provider-key"}); err != nil {
				t.Fatal("preset rejected by actual model URL validation", preset.ID, err)
			}
		}
	}
	for _, id := range []string{"openai", "deepseek", "gemini", "dashscope", "custom", "loopback"} {
		if !seen[id] {
			t.Fatal("missing provider/reference kind", id)
		}
	}
	for _, forbidden := range []string{`"api_key":`, `"model":`, `"configured":`, `"connected":`, `"key_configured":`, emailTestToken} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Fatal("presets disclosed secrets or invented configuration/account state")
		}
	}
	if !reflect.DeepEqual(before, h.modelConfig) || h.ai.client != nil {
		t.Fatal("reading presets changed model configuration")
	}
	for _, query := range []string{"?", "?base_url=https://example.com", "?provider=openai", "?api_key=synthetic"} {
		if invalid := emailTestRequest(h, http.MethodGet, modelPresetsPath+query, "", true); invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "AI_PRESETS_INVALID") {
			t.Fatal("presets accepted a query or dynamic provider target", invalid.Code)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete} {
		if rejected := emailTestRequest(h, method, modelPresetsPath, `{}`, true); rejected.Code < 400 {
			t.Fatal("preset mutation became available", method)
		}
	}
}

func TestAssistantModelPresetsIsReadOnlyAndReturnsActualStaticData(t *testing.T) {
	operation := assistantOperationByID("get_model_presets")
	if operation == nil || operation.Method != http.MethodGet || operation.Path != modelPresetsPath || operation.Effect != "read" || operation.Availability != "available" {
		t.Fatal("preset operation is missing or effectful", operation)
	}
	for _, args := range []string{`{"api_key":"synthetic"}`, `{"base_url":"https://example.com"}`, `{"provider":"openai"}`, `{"confirmed":true}`} {
		if _, err := prepareAssistantOperation(operation, args, apiProfileConfig, 0); err == nil {
			t.Fatal("preset read accepted credential/destination arguments")
		}
	}
	for _, definition := range assistantToolDefinitions() {
		if definition.Name == "update_model_config" {
			t.Fatal("human-only credential mutation became model-executable")
		}
	}
	h, provider, _, _ := newAssistantFixture(t)
	before := h.modelConfig
	provider.run = func(_ context.Context, index int, input agent.ToolInput) (agent.ToolResult, error) {
		if index == 1 {
			return assistantFixtureCall("static_presets", "get_model_presets", `{}`), nil
		}
		var response modelPresetsResponse
		assistantChatSearchToolData(t, input, "static_presets", "get_model_presets", &response)
		if index != 2 || !reflect.DeepEqual(response, staticModelPresets()) {
			t.Fatal("Assistant fabricated or changed static references")
		}
		return assistantFixtureAnswer("配置参考未进行真实服务核验。"), nil
	}
	response := assistantFixtureStart(t, h, "")
	if response.State != "completed" || response.PendingAction != nil || len(response.ToolResults) != 1 || provider.calls.Load() != 2 || !reflect.DeepEqual(before, h.modelConfig) {
		t.Fatal("preset read caused a write, additional provider call, or confirmation", response)
	}
}
