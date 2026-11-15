package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/store"
)

func profilePath(path string) (conversation, action, fact string, ok bool) {
	if path == "/api/v1/ai/profile-status" {
		return "", "status", "", true
	}
	parts := strings.Split(path, "/")
	if len(parts) < 6 || parts[1] != "api" || parts[2] != "v1" || parts[3] != "conversations" || !aiResourceID(parts[4]) || parts[5] != "profile" {
		return "", "", "", false
	}
	if len(parts) == 6 {
		return parts[4], "profile", "", true
	}
	if len(parts) == 7 && (parts[6] == "generate" || parts[6] == "enrich") {
		return parts[4], parts[6], "", true
	}
	if len(parts) == 8 && parts[6] == "facts" && aiResourceID(parts[7]) {
		return parts[4], "fact", parts[7], true
	}
	return "", "", "", false
}

func profileMutation(method, path string) bool {
	_, action, _, ok := profilePath(path)
	return ok && (method == http.MethodPatch && (action == "profile" || action == "fact") || method == http.MethodPost && (action == "generate" || action == "enrich"))
}

func profileRead(path string) bool {
	_, action, _, ok := profilePath(path)
	return ok && (action == "profile" || action == "status")
}

func (h *handler) profileModel() agent.ProfileAnalyzer {
	if h.ai == nil {
		return nil
	}
	model, _ := h.ai.client.(agent.ProfileAnalyzer)
	return model
}

func (h *handler) serveProfile(w http.ResponseWriter, r *http.Request, traceID string) {
	conversation, action, fact, ok := profilePath(r.URL.Path)
	if !ok || r.URL.RawQuery != "" {
		writeProfileError(w, store.ErrInvalidInput, traceID)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if action == "status" && r.Method == http.MethodGet {
		h.profileStatus(w, ctx, traceID)
		return
	}
	if h.inbox == nil {
		writeError(w, 503, "STORAGE_DISABLED", "请先启用本地加密存储。", traceID)
		return
	}
	if action == "profile" && r.Method == http.MethodGet {
		profile, err := h.inbox.GetProfile(ctx, conversation)
		if err != nil {
			writeProfileError(w, err, traceID)
			return
		}
		writeJSON(w, 200, profile)
		return
	}
	if action == "profile" && r.Method == http.MethodPatch {
		var input struct {
			store.ProfilePatch
			Confirmed bool   `json:"confirmed"`
			ConfigID  string `json:"model_config_id"`
		}
		if !readInboxJSON(w, r, &input, traceID) {
			return
		}
		old, err := h.inbox.GetProfile(ctx, conversation)
		if err != nil {
			writeProfileError(w, err, traceID)
			return
		}
		nextAuto, nextSearch, name := old.AutoOrganize, old.PublicSearchEnabled, old.PublicName
		if input.AutoOrganize != nil {
			nextAuto = *input.AutoOrganize
		}
		if input.PublicSearchEnabled != nil {
			nextSearch = *input.PublicSearchEnabled
		}
		if input.PublicName != nil {
			name = strings.TrimSpace(*input.PublicName)
		}
		newPermission := nextAuto && !old.AutoOrganize || nextSearch && !old.PublicSearchEnabled
		configID := old.ModelConfigID
		if old.AIPaused {
			if newPermission {
				writeError(w, 409, "AI_PAUSED", "该联系人的AI处理已暂停，请先在AI设置中恢复。", traceID)
				return
			}
		} else if nextAuto || nextSearch {
			if !h.requireProfileConsent(w, input.Confirmed, input.ConfigID, traceID) {
				return
			}
			configID = h.ai.configID
		} else {
			configID = ""
		}
		if nextSearch && (!old.AIPaused || newPermission) {
			if name == "" {
				writeError(w, 400, "PROFILE_PUBLIC_NAME_REQUIRED", "请先明确填写用于公开搜索的姓名。", traceID)
				return
			}
			if h.profiles == nil || h.profiles.search == nil {
				writeError(w, 503, "SEARCH_NOT_CONFIGURED", "公开搜索尚未配置，请在本机设置搜索服务密钥。", traceID)
				return
			}
		}
		input.ProfilePatch.ModelConfigID = &configID
		var result store.Profile
		err = h.withProfileChange(ctx, conversation, func() error {
			var updateErr error
			result, updateErr = h.inbox.UpdateProfile(ctx, conversation, input.ProfilePatch)
			return updateErr
		})
		if err != nil {
			writeProfileError(w, err, traceID)
			return
		}
		writeJSON(w, 200, result)
		return
	}
	if (action == "generate" || action == "enrich") && r.Method == http.MethodPost {
		var input struct {
			ExpectedRevision *int64 `json:"expected_revision"`
			Confirmed        bool   `json:"confirmed"`
			ConfigID         string `json:"model_config_id"`
		}
		if !readInboxJSON(w, r, &input, traceID) {
			return
		}
		if input.ExpectedRevision == nil || *input.ExpectedRevision < 0 {
			writeProfileError(w, store.ErrInvalidInput, traceID)
			return
		}
		if !h.requireProfileConsent(w, input.Confirmed, input.ConfigID, traceID) {
			return
		}
		profile, err := h.inbox.GetProfile(ctx, conversation)
		if err != nil {
			writeProfileError(w, err, traceID)
			return
		}
		if profile.AIPaused {
			writeError(w, 409, "AI_PAUSED", "该联系人的AI处理已暂停，请先在AI设置中恢复。", traceID)
			return
		}
		kind := "organize"
		if action == "enrich" {
			kind = "enrich"
			if h.profiles == nil || h.profiles.search == nil {
				writeError(w, 503, "SEARCH_NOT_CONFIGURED", "公开搜索尚未配置，请在本机设置搜索服务密钥。", traceID)
				return
			}
			if !profile.PublicSearchEnabled || profile.PublicName == "" {
				writeError(w, 409, "PROFILE_SEARCH_NOT_AUTHORIZED", "请先明确公开姓名并开启此联系人的公开资料搜索。", traceID)
				return
			}
		}
		job, err := h.inbox.EnqueueProfileJob(ctx, conversation, kind, h.ai.configID, *input.ExpectedRevision)
		if err != nil {
			writeProfileError(w, err, traceID)
			return
		}
		writeJSON(w, 202, job)
		return
	}
	if action == "fact" && r.Method == http.MethodPatch {
		var input struct {
			State            string `json:"state"`
			ExpectedRevision *int64 `json:"expected_revision"`
		}
		if !readInboxJSON(w, r, &input, traceID) {
			return
		}
		if input.ExpectedRevision == nil || *input.ExpectedRevision < 0 || input.State != "confirmed" && input.State != "rejected" {
			writeProfileError(w, store.ErrInvalidInput, traceID)
			return
		}
		var profile store.Profile
		err := h.withProfileChange(ctx, conversation, func() error {
			var err error
			profile, err = h.inbox.SetProfileFact(ctx, conversation, fact, input.State, *input.ExpectedRevision)
			return err
		})
		if err != nil {
			writeProfileError(w, err, traceID)
			return
		}
		writeJSON(w, 200, profile)
		return
	}
	writeError(w, 404, "NOT_FOUND", "档案操作不存在。", traceID)
}

func (h *handler) requireProfileConsent(w http.ResponseWriter, confirmed bool, configID, traceID string) bool {
	if h.profileModel() == nil {
		writeError(w, 503, "AI_NOT_CONFIGURED", "请先在本机配置DeepSeek或兼容模型，再进行AI整理。", traceID)
		return false
	}
	if !confirmed || configID != h.ai.configID {
		writeError(w, 409, "AI_CONSENT_REQUIRED", "请确认当前模型及处理范围后再提交。", traceID)
		return false
	}
	return true
}

func (h *handler) profileStatus(w http.ResponseWriter, ctx context.Context, traceID string) {
	var model, provider, host, configID *string
	modelConfigured := h.profileModel() != nil
	aiLimit, used, searchLimit, searchUsed := 50, 0, 20, 0
	if h.ai != nil {
		aiLimit = h.ai.dailyLimit
		if modelConfigured {
			model, provider, host, configID = &h.ai.model, &h.ai.provider, &h.ai.endpointHost, &h.ai.configID
		}
	}
	searchConfigured := h.profiles != nil && h.profiles.search != nil
	if h.profiles != nil {
		searchLimit = h.profiles.searchLimit
	}
	if h.inbox != nil {
		var err error
		used, err = h.inbox.CountAICallsContext(ctx, aiDay())
		if err == nil {
			searchUsed, err = h.inbox.CountProfileSearch(ctx, aiDay())
		}
		if err != nil {
			writeProfileError(w, err, traceID)
			return
		}
	}
	writeJSON(w, 200, map[string]any{
		"model_configured": modelConfigured, "search_configured": searchConfigured,
		"model": model, "provider": provider, "endpoint_host": host, "model_config_id": configID,
		"daily_limit": aiLimit, "used_today": used, "search_daily_limit": searchLimit, "search_used_today": searchUsed, "sending_enabled": false,
	})
}

func writeProfileError(w http.ResponseWriter, err error, traceID string) {
	switch {
	case errors.Is(err, store.ErrInvalidInput):
		writeError(w, 400, "PROFILE_INVALID_INPUT", "档案字段或分类不正确，请检查输入。", traceID)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "PROFILE_NOT_FOUND", "该联系人档案或候选不存在。", traceID)
	case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrStaleContext):
		writeError(w, 409, "PROFILE_STALE", "档案或授权已变化，请更新后再提交。", traceID)
	case errors.Is(err, store.ErrQuotaExceeded):
		writeError(w, 429, "AI_QUOTA_EXCEEDED", "今日处理额度已用完。", traceID)
	default:
		writeError(w, 503, "PROFILE_UNAVAILABLE", "暂时无法访问联系人档案，请稍后更新状态。", traceID)
	}
}
