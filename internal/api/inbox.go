package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/qilinbro/msg-manager/internal/importer"
	"github.com/qilinbro/msg-manager/internal/store"
)

const maxImportBytes = 8 << 20

func conversationRoute(path string) (string, bool, bool) {
	parts := strings.Split(path, "/")
	if len(parts) < 5 || parts[1] != "api" || parts[2] != "v1" || parts[3] != "conversations" {
		return "", false, false
	}
	id := parts[4]
	if id == "" || len(id) > 128 || strings.IndexFunc(id, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	}) >= 0 {
		return "", false, false
	}
	if len(parts) == 5 {
		return id, false, true
	}
	if len(parts) == 6 && parts[5] == "messages" {
		return id, true, true
	}
	return "", false, false
}

func inboxMutation(method, path string) bool {
	if method == http.MethodPost {
		return path == "/api/v1/imports/messages" || path == "/api/v1/imports/telegram" || path == "/api/v1/connections/email-imap/sync" || path == "/api/v1/connections/wechat-db/read"
	}
	_, messages, ok := conversationRoute(path)
	return method == http.MethodPatch && ok && !messages
}

func isInboxRead(path string) bool {
	if path == "/api/v1/inbox" || path == "/api/v1/inbox/connections" || path == "/api/v1/conversations" {
		return true
	}
	_, messages, ok := conversationRoute(path)
	return ok && messages
}

type inboxPlatform struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	LiveStatus   string `json:"live_status"`
	MessageCount int    `json:"message_count"`
}

func (h *handler) serveInbox(w http.ResponseWriter, r *http.Request, traceID string) {
	if r.Method == http.MethodGet && r.URL.Path == "/api/v1/inbox" {
		platforms := []inboxPlatform{{ID: "qq", Name: "QQ", LiveStatus: "probe_only"}, {ID: "weixin", Name: "微信", LiveStatus: "not_connected"}, {ID: "telegram", Name: "Telegram", LiveStatus: "not_connected"}, {ID: "shanliao", Name: "闪聊", LiveStatus: "unsupported"}, {ID: "email", Name: "邮箱", LiveStatus: "not_connected"}}
		sourceMode := "import"
		if h.wechat.reader != nil {
			platforms[1].LiveStatus = "configured"
			sourceMode = "mixed"
		}
		if h.email.client != nil {
			platforms[4].LiveStatus = "configured"
			sourceMode = "mixed"
		}
		if h.inbox != nil {
			connections, err := h.inbox.Connections()
			if err != nil {
				writeInboxError(w, err, traceID)
				return
			}
			for i := range platforms {
				for _, connection := range connections {
					if connection.Source != "import" {
						sourceMode = "mixed"
					}
					if connection.Platform == platforms[i].ID {
						platforms[i].MessageCount += connection.MessageCount
					}
				}
			}
		}
		writeJSON(w, http.StatusOK, struct {
			StorageEnabled bool            `json:"storage_enabled"`
			SourceMode     string          `json:"source_mode"`
			Platforms      []inboxPlatform `json:"platforms"`
			ImportLimit    int             `json:"import_limit"`
		}{h.inbox != nil, sourceMode, platforms, 1000})
		return
	}
	if h.inbox == nil {
		writeError(w, http.StatusServiceUnavailable, "STORAGE_DISABLED", "请先在本地服务中配置加密消息存储。", traceID)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/v1/inbox/connections" {
		items, err := h.inbox.Connections()
		if err != nil {
			writeInboxError(w, err, traceID)
			return
		}
		writeJSON(w, http.StatusOK, ListResponse[store.Connection]{Items: items})
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/v1/conversations" {
		filter, err := readInboxFilter(r)
		if err != nil {
			writeInboxError(w, err, traceID)
			return
		}
		page, err := h.inbox.Conversations(filter)
		if err != nil {
			writeInboxError(w, err, traceID)
			return
		}
		writeJSON(w, http.StatusOK, page)
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/v1/imports/messages" {
		var input store.ImportRequest
		if !readInboxJSON(w, r, &input, traceID) {
			return
		}
		result, err := h.inbox.Import(input)
		if err != nil {
			writeInboxError(w, err, traceID)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/v1/imports/telegram" {
		var input struct {
			AccountID   string          `json:"account_id"`
			AccountName string          `json:"account_name"`
			Export      json.RawMessage `json:"export"`
		}
		if !readInboxJSON(w, r, &input, traceID) {
			return
		}
		converted, err := importer.TelegramPrivate(input.Export, input.AccountID)
		if err != nil {
			if errors.Is(err, importer.ErrLimitExceeded) {
				writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "导出文件或消息数量超过限制，请分批导入。", traceID)
			} else {
				writeInboxError(w, store.ErrInvalidInput, traceID)
			}
			return
		}
		batch := store.ImportRequest{Platform: "telegram", AccountID: input.AccountID, AccountName: input.AccountName, Messages: make([]store.ImportMessage, 0, len(converted.Messages))}
		for _, message := range converted.Messages {
			batch.Messages = append(batch.Messages, store.ImportMessage{ExternalID: message.ExternalID, PeerID: message.PeerID, PeerName: message.PeerName, Direction: message.Direction, Body: message.Body, OccurredAt: message.OccurredAt})
		}
		if len(batch.Messages) == 0 {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "该导出文件没有可导入的好友私聊纯文本。", traceID)
			return
		}
		result, err := h.inbox.Import(batch)
		if err != nil {
			writeInboxError(w, err, traceID)
			return
		}
		writeJSON(w, http.StatusOK, struct {
			store.ImportResult
			Skipped int `json:"skipped"`
		}{result, converted.Skipped})
		return
	}
	id, messages, ok := conversationRoute(r.URL.Path)
	if ok && messages && r.Method == http.MethodGet {
		filter, err := readInboxFilter(r)
		if err != nil || filter.Q != "" || filter.Platform != "" || filter.Unread || filter.Archived || filter.Pinned {
			writeInboxError(w, store.ErrInvalidInput, traceID)
			return
		}
		page, err := h.inbox.Messages(id, filter.Cursor, filter.Limit)
		if err != nil {
			writeInboxError(w, err, traceID)
			return
		}
		writeJSON(w, http.StatusOK, page)
		return
	}
	if ok && !messages && r.Method == http.MethodPatch {
		var patch store.ConversationPatch
		if !readInboxJSON(w, r, &patch, traceID) {
			return
		}
		if patch.Read == nil && patch.Pinned == nil && patch.Archived == nil {
			writeInboxError(w, store.ErrInvalidInput, traceID)
			return
		}
		if err := h.inbox.UpdateConversation(id, patch); err != nil {
			writeInboxError(w, err, traceID)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	writeError(w, http.StatusNotFound, "NOT_FOUND", "消息接口不存在。", traceID)
}

func readInboxFilter(r *http.Request) (store.Filter, error) {
	values, err := parseInboxQuery(r)
	if err != nil {
		return store.Filter{}, err
	}
	f := store.Filter{Q: values.Get("q"), Platform: values.Get("platform"), Cursor: values.Get("cursor"), Limit: 50}
	if len(f.Q) > 256 || !utf8.ValidString(f.Q) || len(f.Cursor) > 2048 || (f.Platform != "" && f.Platform != "qq" && f.Platform != "weixin" && f.Platform != "telegram" && f.Platform != "shanliao" && f.Platform != "email") {
		return f, store.ErrInvalidInput
	}
	for _, field := range []struct {
		name string
		dest *bool
	}{{"unread", &f.Unread}, {"archived", &f.Archived}, {"pinned", &f.Pinned}} {
		if v, exists := values[field.name]; exists {
			if v[0] != "true" && v[0] != "false" {
				return f, store.ErrInvalidInput
			}
			*field.dest = v[0] == "true"
		}
	}
	if v, exists := values["limit"]; exists {
		n, err := strconv.Atoi(v[0])
		if err != nil || n < 1 || n > 100 {
			return f, store.ErrInvalidInput
		}
		f.Limit = n
	}
	return f, nil
}

func parseInboxQuery(r *http.Request) (url.Values, error) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, store.ErrInvalidInput
	}
	for name, items := range values {
		switch name {
		case "q", "platform", "cursor", "limit", "unread", "archived", "pinned", "workspace_id":
		default:
			return nil, store.ErrInvalidInput
		}
		if len(items) != 1 {
			return nil, store.ErrInvalidInput
		}
	}
	return values, nil
}

func writeInboxError(w http.ResponseWriter, err error, traceID string) {
	switch {
	case errors.Is(err, store.ErrInvalidCursor):
		writeError(w, http.StatusBadRequest, "INVALID_CURSOR", "消息记录已有变化，请刷新后继续查看。", traceID)
	case errors.Is(err, store.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "消息格式或筛选条件不正确，请检查导入说明。", traceID)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "CONVERSATION_NOT_FOUND", "该会话不存在。", traceID)
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "IMPORT_CONFLICT", "同一消息标识存在不同内容，本批未导入，请核对来源。", traceID)
	default:
		writeError(w, http.StatusServiceUnavailable, "STORE_UNAVAILABLE", "暂时无法访问加密消息存储，请稍后重试。", traceID)
	}
}

func readInboxJSON(w http.ResponseWriter, r *http.Request, target any, traceID string) bool {
	contentType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
		writeInboxError(w, store.ErrInvalidInput, traceID)
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxImportBytes+1))
	if err != nil {
		writeInboxError(w, store.ErrInvalidInput, traceID)
		return false
	}
	if len(body) > maxImportBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "导入文件不能超过 8 MiB，请分批导入。", traceID)
		return false
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' || !utf8.Valid(body) || !uniqueJSON(body) {
		writeInboxError(w, store.ErrInvalidInput, traceID)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeInboxError(w, store.ErrInvalidInput, traceID)
		return false
	}
	return true
}

// Bound JSON depth and reject duplicate object keys before decoding structs.
// Ambiguous input must never select a different account depending on a parser.
func uniqueJSON(body []byte) bool {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var visit func(int) bool
	visit = func(depth int) bool {
		if depth > 24 {
			return false
		}
		token, err := d.Token()
		if err != nil {
			return false
		}
		delimiter, isContainer := token.(json.Delim)
		if !isContainer {
			return true
		}
		switch delimiter {
		case '{':
			keys := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return false
				}
				name, ok := key.(string)
				if !ok || keys[name] {
					return false
				}
				keys[name] = true
				if !visit(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for d.More() {
				if !visit(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim(']')
		default:
			return false
		}
	}
	if !visit(0) {
		return false
	}
	_, err := d.Token()
	return errors.Is(err, io.EOF)
}
