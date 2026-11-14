// Package connector defines the capabilities of local platform adapters.
package connector

import (
	"context"
	"time"

	"github.com/qilinbro/msg-manager/internal/domain"
)

type Capabilities struct {
	ReceiveText   bool `json:"receive_text"`
	SendText      bool `json:"send_text"`
	HistoryImport bool `json:"history_import"`
}

type Descriptor struct {
	ID                   string       `json:"id"`
	DisplayName          string       `json:"display_name"`
	Platform             string       `json:"platform"`
	Status               string       `json:"status"`
	ImplementationStatus string       `json:"implementation_status"`
	Capabilities         Capabilities `json:"capabilities"`
	Description          string       `json:"description"`
}

// Planned returns fresh values so callers cannot mutate the catalog globally.
func Planned() []Descriptor {
	return []Descriptor{
		{ID: "qq-onebot", DisplayName: "QQ 个人号（OneBot）", Platform: "qq", Status: "not_configured", ImplementationStatus: "probe_ready", Description: "通过已有 OneBot v11 服务核验自己的 QQ 账号；消息持久化与自动发送尚未启用。"},
		{ID: "weixin-clawbot", DisplayName: "微信 Clawbot", Platform: "weixin", Status: "not_configured", ImplementationStatus: "planned", Description: "尚未接入。计划验证微信 Clawbot 授权能力；不代表接管个人微信全部现有好友或聊天。"},
		{ID: "telegram-bot", DisplayName: "Telegram 机器人", Platform: "telegram", Status: "not_configured", ImplementationStatus: "planned", Description: "尚未接入。计划通过 Bot API 处理机器人实际可见的授权会话，不读取个人账号全部私聊。"},
		{ID: "email-imap", DisplayName: "邮箱（IMAP只读）", Platform: "email", Status: "not_configured", ImplementationStatus: "manual_sync_ready", Description: "支持本机配置邮箱后手动收取最近邮件；未启用后台自动收件或发信。"},
		{ID: "wechat-db", DisplayName: "微信（本地数据库只读）", Platform: "weixin", Status: "not_configured", ImplementationStatus: "manual_read_ready", Description: "配置自己的本机微信4.x数据库后手动读取，或明确选定私聊开启后台定时读取；后台需保持运行，不代表完整历史同步。"},
	}
}

type Event struct {
	Scope           domain.ConnectionScope
	ExternalEventID string
	Kind            string
	Message         *domain.Message
	ReceivedAt      time.Time
}

type Batch struct {
	Events     []Event
	NextCursor string
}

type OutgoingMessage struct {
	OperationID string
	Scope       domain.ConversationScope
	Body        string
}

type SendState string

const (
	SendSent    SendState = "sent"
	SendFailed  SendState = "failed"
	SendUnknown SendState = "unknown"
)

type SendResult struct {
	State             SendState
	ExternalMessageID string
	RetryAfter        time.Duration
	// DefinitelyNotSent is the only safe retry basis; a timeout alone is insufficient.
	DefinitelyNotSent bool
}

type Receiver interface {
	Receive(context.Context, domain.ConnectionScope, string) (Batch, error)
}

// Sender is called only by a future policy-checked outbox executor, never a model.
type Sender interface {
	Send(context.Context, OutgoingMessage) (SendResult, error)
}
