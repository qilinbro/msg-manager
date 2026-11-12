// Package api exposes the local inbox, identity probes and explicitly requested
// AI analysis. Model output is saved as local suggestions; no sender is installed.
package api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/connector"
	"github.com/qilinbro/msg-manager/internal/store"
)

type handler struct {
	tokenHash [32]byte
	port      string
	sequence  atomic.Uint64
	qq        *qqConnection
	inbox     *store.Store
	email     *emailConnection
	ai        *aiConnection
	away      *awayWorker
	finfold   *finfoldHandoff
}

func NewHandler(cfg config.Config) (http.Handler, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	_, port, _ := net.SplitHostPort(cfg.Addr)
	qq, err := newQQConnection(cfg.QQOneBot)
	if err != nil {
		return nil, err
	}
	email, err := newEmailConnection(cfg.Email)
	if err != nil {
		return nil, err
	}
	ai, err := newAIConnection(cfg.AI, cfg.AIDailyLimit)
	if err != nil {
		return nil, err
	}
	var inbox *store.Store
	if cfg.DatabasePath != "" {
		inbox, err = store.Open(cfg.DatabasePath, cfg.DatabaseKey)
		if err != nil {
			return nil, fmt.Errorf("encrypted inbox could not be opened; check local database configuration")
		}
	}
	h := &handler{tokenHash: sha256.Sum256([]byte(cfg.APIToken)), port: port, qq: qq, inbox: inbox, email: email, ai: ai, finfold: newFinfoldHandoff(cfg.FinfoldOrigin, cfg.FinfoldSecret)}
	if inbox != nil {
		h.startAwayWorker()
	}
	return h, nil
}

func (h *handler) Close() error {
	if h.away != nil {
		h.away.stop()
		<-h.away.done
	}
	if h.inbox != nil {
		return h.inbox.Close()
	}
	return nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	traceID := fmt.Sprintf("%x-%x", time.Now().UnixNano(), h.sequence.Add(1))
	w.Header().Set("X-Trace-ID", traceID)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	w.Header().Add("Vary", "Origin")
	if !validHost(r.Host, h.port) {
		writeError(w, http.StatusForbidden, "HOST_DENIED", "请求主机不在本地服务允许范围。", traceID)
		return
	}
	origins := r.Header.Values("Origin")
	origin := r.Header.Get("Origin")
	if len(origins) > 1 || (len(origins) == 1 && !allowedOrigins[origin]) || (len(origins) == 0 && r.Header.Get("Sec-Fetch-Site") == "cross-site") {
		writeError(w, http.StatusForbidden, "ORIGIN_DENIED", "请求来源不在允许范围。", traceID)
		return
	}
	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}
	apiPath := r.URL.Path == "/api/v1" || strings.HasPrefix(r.URL.Path, "/api/v1/")
	// Browsers omit credentials in CORS preflight. This exception returns no
	// business data. Only implemented local mutation routes are permitted in addition
	// to GET, and every actual request must authenticate.
	if apiPath && r.Method == http.MethodOptions && origin != "" {
		requestedMethod := r.Header.Get("Access-Control-Request-Method")
		if (requestedMethod != http.MethodGet && !inboxMutation(requestedMethod, r.URL.Path) && !aiMutation(requestedMethod, r.URL.Path) && !awayMutation(requestedMethod, r.URL.Path)) || !validPreflightHeaders(r) {
			writeError(w, http.StatusForbidden, "PREFLIGHT_DENIED", "该界面请求的方法或请求头不被允许。", traceID)
			return
		}
		w.Header().Add("Vary", "Access-Control-Request-Method")
		w.Header().Add("Vary", "Access-Control-Request-Headers")
		w.Header().Set("Access-Control-Allow-Methods", requestedMethod)
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if apiPath {
		if !validToken(r, h.tokenHash) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "需要有效的本机访问令牌。", traceID)
			return
		}
		r = r.WithContext(withLocalPrincipal(r.Context()))
		if !validScopeClaims(r, authenticatedWorkspace(r.Context())) {
			writeError(w, http.StatusForbidden, "SCOPE_DENIED", "请求不属于当前工作区。", traceID)
			return
		}
		if action, id, task := awayResource(r.Method, r.URL.Path); action != "" {
			h.serveAway(w, r, traceID, action, id, task)
			return
		}
		if r.Method != http.MethodGet {
			if aiMutation(r.Method, r.URL.Path) {
				h.serveAI(w, r, traceID)
				return
			}
			if r.Method == http.MethodPost && r.URL.Path == "/api/v1/connections/email-imap/sync" {
				h.syncEmail(w, r, traceID)
				return
			}
			if inboxMutation(r.Method, r.URL.Path) {
				h.serveInbox(w, r, traceID)
				return
			}
			writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "该操作尚未实现；已开放消息管理与手动AI分析，平台发送保持关闭。", traceID)
			return
		}
		h.serveRead(w, r, traceID)
		return
	}
	if r.Method == http.MethodGet {
		switch r.URL.Path {
		case "/health/live":
			stage := "scaffold"
			if h.inbox != nil {
				stage = "unified_inbox"
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "live", "version": Version, "stage": stage})
			return
		case "/health/ready":
			writeError(w, http.StatusServiceUnavailable, "NOT_READY", "无人值守处理所需的实时接收、任务与发送链路尚未就绪；本接口不代表本地消息管理可用状态。", traceID)
			return
		}
	}
	writeError(w, http.StatusNotFound, "NOT_FOUND", "请求的接口不存在。", traceID)
}

func (h *handler) serveRead(w http.ResponseWriter, r *http.Request, traceID string) {
	switch r.URL.Path {
	case "/api/v1/system":
		stage, driver := "scaffold", "sqlite"
		if h.inbox != nil {
			stage, driver = "unified_inbox", "sqlcipher"
		}
		writeJSON(w, http.StatusOK, SystemResponse{
			Version: Version, Stage: stage, WorkspaceID: authenticatedWorkspace(r.Context()),
			Automation: AutomationStatus{Paused: true, AutoSendEnabled: false},
			Storage:    StorageStatus{Configured: h.inbox != nil, Driver: driver}, Connectors: h.connections(),
		})
	case "/api/v1/connections":
		writeJSON(w, http.StatusOK, ListResponse[connector.Descriptor]{Items: h.connections()})
	case "/api/v1/connections/qq-onebot/verify":
		h.verifyQQ(w, r, traceID)
	case "/api/v1/connections/email-imap/status":
		h.emailStatus(w)
	case "/api/v1/events":
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "事件流与恢复游标尚未实现；当前不提供实时事件订阅。", traceID)
	case "/api/v1/messages", "/api/v1/contacts", "/api/v1/drafts", "/api/v1/approvals", "/api/v1/jobs", "/api/v1/audit", "/api/v1/reports":
		writeJSON(w, http.StatusOK, ListResponse[any]{Items: []any{}})
	default:
		if aiRead(r.URL.Path) {
			h.serveAI(w, r, traceID)
			return
		}
		if isInboxRead(r.URL.Path) {
			h.serveInbox(w, r, traceID)
			return
		}
		writeError(w, http.StatusNotFound, "NOT_FOUND", "请求的接口不存在或尚未实现。", traceID)
	}
}

func writeError(w http.ResponseWriter, status int, code, message, traceID string) {
	writeJSON(w, status, ErrorResponse{Error: ErrorDetail{Code: code, Message: message, TraceID: traceID}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
