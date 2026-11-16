package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/qilinbro/msg-manager/internal/store"
)

func profileLinksPath(path string) (conversation, target string, ok bool) {
	parts := strings.Split(path, "/")
	if len(parts) != 6 && len(parts) != 7 || parts[1] != "api" || parts[2] != "v1" || parts[3] != "conversations" || !aiResourceID(parts[4]) || parts[5] != "profile-links" {
		return "", "", false
	}
	if len(parts) == 7 {
		if !aiResourceID(parts[6]) {
			return "", "", false
		}
		target = parts[6]
	}
	return parts[4], target, true
}

func profileLinksRead(path string) bool {
	_, target, ok := profileLinksPath(path)
	return ok && target == ""
}

func profileLinksMutation(method, path string) bool {
	_, target, ok := profileLinksPath(path)
	return ok && (method == http.MethodPost && target == "" || method == http.MethodDelete && target != "")
}

func (h *handler) serveProfileLinks(w http.ResponseWriter, r *http.Request, traceID string) {
	id, target, ok := profileLinksPath(r.URL.Path)
	if !ok || r.URL.RawQuery != "" {
		writeProfileError(w, store.ErrInvalidInput, traceID)
		return
	}
	if h.inbox == nil {
		writeError(w, 503, "STORAGE_DISABLED", "请先启用本地加密消息存储。", traceID)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var out store.ProfileLinks
	var err error
	switch r.Method {
	case http.MethodGet:
		out, err = h.inbox.GetProfileLinks(ctx, id)
	case http.MethodPost:
		var input struct {
			Target    string `json:"target_conversation_id"`
			Confirmed bool   `json:"confirmed"`
		}
		if !readInboxJSON(w, r, &input, traceID) {
			return
		}
		if !input.Confirmed {
			writeError(w, 409, "PROFILE_LINK_CONFIRMATION_REQUIRED", "请核对两份来源档案，确认属于同一人后再关联。", traceID)
			return
		}
		out, err = h.inbox.SetProfileLink(ctx, id, input.Target, true)
	case http.MethodDelete:
		out, err = h.inbox.SetProfileLink(ctx, id, target, false)
	default:
		writeError(w, 404, "NOT_FOUND", "档案关联操作不存在。", traceID)
		return
	}
	if err != nil {
		writeProfileError(w, err, traceID)
		return
	}
	writeJSON(w, 200, out)
}
