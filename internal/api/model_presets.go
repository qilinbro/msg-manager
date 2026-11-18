package api

import "net/http"

const modelPresetsPath = "/api/v1/ai/model-presets"

type modelPreset struct {
	ID                   string `json:"id"`
	DisplayName          string `json:"display_name"`
	BaseURL              string `json:"base_url"`
	APIKeyRequired       bool   `json:"api_key_required"`
	ImplementationStatus string `json:"implementation_status"`
	SourceURL            string `json:"source_url"`
	SourceRef            string `json:"source_ref"`
	SourceLicense        string `json:"source_license"`
}

type modelPresetsResponse struct {
	Protocol                  string        `json:"protocol"`
	ModelIDRequired           bool          `json:"model_id_required"`
	LiveVerificationPerformed bool          `json:"live_verification_performed"`
	Items                     []modelPreset `json:"items"`
}

// These are static configuration references, not discovered models, account
// capabilities, saved settings, connection checks, or endorsements. The source
// license applies only to the linked source, not provider services/trademarks.
// No upstream SDK/code/assets are imported and no provider request is made.
func staticModelPresets() modelPresetsResponse {
	const openAIRef = "d5292055f5d00d99d949017745a81d31cd186222"
	const deepSeekRef = "172dc8b74a0d549f4bce9850f7fe6a25b37a5ac7"
	const geminiRef = "853a69ac49e21a917e7cdd178cadac193652c627"
	const qwenRef = "31a4d36d123688581a9e9744427272b33ce940e0"
	const openAISource = "https://github.com/openai/openai-go/blob/" + openAIRef + "/README.md"
	return modelPresetsResponse{
		Protocol: "openai_chat_completions", ModelIDRequired: true, LiveVerificationPerformed: false,
		Items: []modelPreset{
			{ID: "openai", DisplayName: "OpenAI", BaseURL: "https://api.openai.com/v1", APIKeyRequired: true, ImplementationStatus: "protocol_supported", SourceURL: openAISource, SourceRef: openAIRef, SourceLicense: "Apache-2.0"},
			{ID: "deepseek", DisplayName: "DeepSeek", BaseURL: "https://api.deepseek.com", APIKeyRequired: true, ImplementationStatus: "protocol_supported", SourceURL: "https://github.com/deepseek-ai/awesome-deepseek-integration/tree/" + deepSeekRef, SourceRef: deepSeekRef, SourceLicense: "CC0-1.0"},
			{ID: "gemini", DisplayName: "Gemini · OpenAI compatible", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", APIKeyRequired: true, ImplementationStatus: "protocol_supported", SourceURL: "https://github.com/google-gemini/cookbook/blob/" + geminiRef + "/quickstarts/Get_started_OpenAI_Compatibility.ipynb", SourceRef: geminiRef, SourceLicense: "Apache-2.0"},
			{ID: "dashscope", DisplayName: "DashScope · OpenAI compatible", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", APIKeyRequired: true, ImplementationStatus: "protocol_supported", SourceURL: "https://github.com/QwenLM/Qwen-Agent/blob/" + qwenRef + "/examples/assistant_qwen3_coder.py", SourceRef: qwenRef, SourceLicense: "Apache-2.0"},
			{ID: "custom", DisplayName: "Custom · public HTTPS", BaseURL: "", APIKeyRequired: true, ImplementationStatus: "template_only", SourceURL: openAISource, SourceRef: openAIRef, SourceLicense: "Apache-2.0"},
			{ID: "loopback", DisplayName: "Local · loopback HTTP", BaseURL: "http://127.0.0.1:11434/v1", APIKeyRequired: false, ImplementationStatus: "template_only", SourceURL: openAISource, SourceRef: openAIRef, SourceLicense: "Apache-2.0"},
		},
	}
}

func (h *handler) serveModelPresets(w http.ResponseWriter, r *http.Request, traceID string) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, http.StatusBadRequest, "AI_PRESETS_INVALID", "模型预设不接受查询参数。", traceID)
		return
	}
	writeJSON(w, http.StatusOK, staticModelPresets())
}
