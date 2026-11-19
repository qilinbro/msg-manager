package api

import "github.com/qilinbro/msg-manager/internal/connector"

func (h *handler) connections() []connector.Descriptor {
	items := connector.Planned()
	h.email.mu.RLock()
	emailConfigured := h.email.status.Configured
	h.email.mu.RUnlock()
	for i := range items {
		if items[i].ID == "email-imap" && emailConfigured {
			items[i].Status = "configured"
			items[i].Capabilities.ReceiveText = h.inbox != nil
		}
		if items[i].ID == "wechat-db" {
			h.wechat.mu.RLock()
			items[i].Status = h.wechat.status.State
			items[i].Capabilities.ReceiveText = h.wechat.monitor.Enabled && h.wechat.reader != nil && h.inbox != nil
			h.wechat.mu.RUnlock()
			items[i].Capabilities.HistoryImport = h.wechat.reader != nil && h.inbox != nil
		}
	}
	h.applyMilky(items)
	h.applyTelegramBot(items)
	return items
}
