package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type finfoldHandoff struct {
	origin string
	secret string
	client *http.Client
}

type publicBrief struct {
	Event         string `json:"event"`
	Product       string `json:"product"`
	SellingPoints string `json:"selling_points"`
	CTA           string `json:"cta"`
}

func newFinfoldHandoff(origin, secret string) *finfoldHandoff {
	if origin != "https://www.finfold.app" && origin != "https://staging.finfold.app" {
		return nil
	}
	if len(secret) < 32 || len(secret) > 256 || strings.IndexFunc(secret, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return nil
	}
	return &finfoldHandoff{origin: origin, secret: secret, client: &http.Client{
		Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (h *handler) handoffFinfold(w http.ResponseWriter, r *http.Request, trace string) {
	if h.finfold == nil {
		writeError(w, 503, "FINFOLD_DISABLED", "Finfold 接入尚未启用。", trace)
		return
	}
	var input struct {
		Brief     publicBrief `json:"brief"`
		Confirmed bool        `json:"confirmed"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if !readInboxJSON(w, r, &input, trace) {
		return
	}
	if !input.Confirmed {
		writeError(w, 400, "FINFOLD_BRIEF_UNCONFIRMED", "请先确认四个字段可公开。", trace)
		return
	}
	brief := input.Brief
	brief.Event, brief.Product, brief.SellingPoints, brief.CTA = strings.TrimSpace(brief.Event), strings.TrimSpace(brief.Product), strings.TrimSpace(brief.SellingPoints), strings.TrimSpace(brief.CTA)
	for _, field := range []struct {
		value string
		limit int
	}{{brief.Event, 300}, {brief.Product, 300}, {brief.SellingPoints, 1200}, {brief.CTA, 300}} {
		if field.value == "" || utf8.RuneCountInString(field.value) > field.limit || strings.IndexFunc(field.value, unicode.IsControl) >= 0 {
			writeError(w, 400, "FINFOLD_BRIEF_INVALID", "请检查四个公开字段后再提交。", trace)
			return
		}
	}
	url, ok := h.finfold.issue(r.Context(), brief)
	if !ok {
		writeError(w, 502, "FINFOLD_UNAVAILABLE", "暂时无法取得 Finfold 导入链接。已保留本地内容，请稍后重试。", trace)
		return
	}
	writeJSON(w, 200, map[string]string{"url": url})
}

func (f *finfoldHandoff) issue(ctx context.Context, brief publicBrief) (string, bool) {
	body, _ := json.Marshal(brief)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.origin+"/api/integrations/away-desk/handoff", bytes.NewReader(body))
	if err != nil {
		return "", false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.secret)
	res, err := f.client.Do(req)
	if err != nil {
		return "", false
	}
	defer res.Body.Close()
	if res.StatusCode != 201 {
		return "", false
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 2049))
	var result struct {
		Ticket string `json:"ticket"`
	}
	if err != nil || len(data) > 2048 || json.Unmarshal(data, &result) != nil {
		return "", false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(result.Ticket)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != result.Ticket {
		return "", false
	}
	return f.origin + "/workbench/import#" + result.Ticket, true
}
