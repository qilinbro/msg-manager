package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const FeishuTimezone = "Asia/Shanghai"
const FeishuMaxTextBytes = 8000
const FeishuScheduleGrace = 2 * time.Minute

var (
	ErrFeishuSourceChanged = errors.New("feishu source preferences changed")
	ErrFeishuConfigChanged = errors.New("feishu configuration changed")
	ErrFeishuExpired       = errors.New("feishu report authorization expired")
)

// Credentials never serialize or format, including accidental %+v logging.
type FeishuConfig struct {
	AppID           string `json:"-"`
	AppSecret       string `json:"-"`
	RecipientOpenID string `json:"-"`
	Revision        int64  `json:"-"`
}

func (FeishuConfig) String() string   { return "FeishuConfig{redacted}" }
func (FeishuConfig) GoString() string { return "FeishuConfig{redacted}" }
func (c FeishuConfig) Configured() bool {
	return c.AppID != "" && c.AppSecret != "" && c.RecipientOpenID != ""
}
func (c FeishuConfig) TargetBindingID() string {
	if !c.Configured() {
		return ""
	}
	raw, _ := json.Marshal([]string{c.AppID, c.RecipientOpenID})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type FeishuBinding struct {
	ModelConfigID       string `json:"model_config_id"`
	ModelConfigRevision int64  `json:"model_config_revision"`
	ConfigRevision      int64  `json:"config_revision"`
	TargetBindingID     string `json:"target_binding_id"`
}

func (b FeishuBinding) Valid() bool {
	return validProfileConfig(b.ModelConfigID) && b.ModelConfigRevision >= 0 && b.ConfigRevision > 0 && validProfileConfig(b.TargetBindingID)
}

type FeishuSchedule struct {
	Enabled  bool     `json:"enabled"`
	Revision int64    `json:"revision"`
	Timezone string   `json:"timezone"`
	Slots    []string `json:"slots"`
	FeishuBinding
	NextWindowEnd *string `json:"next_window_end"`
	LastWindowEnd *string `json:"last_window_end"`
}

type FeishuSourceRef struct {
	ConversationID   string `json:"conversation_id"`
	SettingsRevision int64  `json:"settings_revision"`
}

type FeishuTodo struct {
	Text        string   `json:"text"`
	EvidenceIDs []string `json:"evidence_ids"`
	NeedsReview bool     `json:"needs_review"`
}

type FeishuExcerpt struct {
	MessageID      string `json:"message_id"`
	ConversationID string `json:"conversation_id"`
	Text           string `json:"text"`
	OccurredAt     string `json:"occurred_at"`
}

type FeishuReport struct {
	ID          string `json:"id"`
	WindowStart string `json:"window_start"`
	WindowEnd   string `json:"window_end"`
	State       string `json:"state"`
	FeishuBinding
	ScheduleRevision  int64             `json:"schedule_revision"`
	Trigger           string            `json:"trigger"`
	Summary           string            `json:"summary"`
	Todos             []FeishuTodo      `json:"todos"`
	Excerpts          []FeishuExcerpt   `json:"excerpts"`
	Text              string            `json:"text"`
	ContentHash       string            `json:"content_hash"`
	Truncated         bool              `json:"truncated"`
	ConversationCount int               `json:"conversation_count"`
	ConversationIDs   []string          `json:"conversation_ids"`
	SourceRefs        []FeishuSourceRef `json:"source_refs"`
	ErrorCode         string            `json:"error_code"`
	CreatedAt         string            `json:"created_at"`
	GeneratedAt       *string           `json:"generated_at"`
	Delivery          *FeishuDelivery   `json:"delivery"`
}

type FeishuDelivery struct {
	ID          string `json:"id"`
	ReportID    string `json:"report_id"`
	WindowStart string `json:"window_start"`
	WindowEnd   string `json:"window_end"`
	State       string `json:"state"`
	FeishuBinding
	ContentHash string  `json:"content_hash"`
	MessageID   string  `json:"message_id"`
	ErrorCode   string  `json:"error_code"`
	CreatedAt   string  `json:"created_at"`
	CompletedAt *string `json:"completed_at"`
}

type FeishuMessage struct {
	DigestMessage
	SettingsRevision int64
}

// Fixed UTC+8 has no DST; deriving boundaries from local wall time never creates
// rolling, overlapping windows. At a boundary that boundary is the completed one.
func FeishuWindow(now time.Time) (start, end time.Time) {
	local := now.In(time.FixedZone(FeishuTimezone, 8*60*60))
	end = time.Date(local.Year(), local.Month(), local.Day(), 20, 0, 0, 0, local.Location())
	if local.Before(end) {
		end = end.Add(-12 * time.Hour)
	}
	if local.Before(end) {
		end = end.Add(-12 * time.Hour)
	}
	return end.Add(-12 * time.Hour).UTC(), end.UTC()
}
func FeishuNextWindow(now time.Time) time.Time {
	_, end := FeishuWindow(now)
	return end.Add(12 * time.Hour)
}
func FeishuTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
func feishuWindowValid(start, end string) bool {
	a, ea := time.Parse(time.RFC3339Nano, start)
	b, eb := time.Parse(time.RFC3339Nano, end)
	if ea != nil || eb != nil || b.Sub(a) != 12*time.Hour || start != FeishuTime(a) || end != FeishuTime(b) {
		return false
	}
	l := b.In(time.FixedZone(FeishuTimezone, 8*60*60))
	return (l.Hour() == 8 || l.Hour() == 20) && l.Minute() == 0 && l.Second() == 0 && l.Nanosecond() == 0
}
func feishuWindowID(prefix, end string) string {
	hash := sha256.Sum256([]byte(workspaceID + "\x00feishu\x00" + end))
	return prefix + hex.EncodeToString(hash[:16])
}
func FeishuReportID(end string) string   { return feishuWindowID("fr_", end) }
func FeishuDeliveryID(end string) string { return feishuWindowID("fd_", end) }
func FeishuContentHash(report FeishuReport) string {
	raw, _ := json.Marshal(struct {
		ID, Start, End, Text string
		Binding              FeishuBinding
	}{report.ID, report.WindowStart, report.WindowEnd, report.Text, report.FeishuBinding})
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}
func feishuText(s string, max int, empty bool) bool {
	return len(s) <= max && utf8.ValidString(s) && (empty || strings.TrimSpace(s) != "") && !strings.ContainsAny(s, "\x00\r")
}
func feishuTerminal(state string) bool {
	return state == "succeeded" || state == "failed" || state == "unknown"
}
