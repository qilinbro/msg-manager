package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/connector/feishu"
	"github.com/qilinbro/msg-manager/internal/store"
)

type feishuInputGroup struct {
	Title string
	Input agent.Input
}

func feishuNeutralText(value string) string { return store.FeishuSafeText(value) }
func feishuCutBytes(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	if limit < 0 {
		limit = 0
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit], true
}
func feishuBoundText(value string, limit int) (string, bool) {
	return store.FeishuBoundText(value, limit)
}
func feishuExcerptText(value string, limit int) (string, bool) {
	return store.FeishuQuote(value, limit)
}
func selectFeishuInput(messages []store.FeishuMessage) (store.FeishuReport, []feishuInputGroup) {
	report := store.FeishuReport{Todos: []store.FeishuTodo{}, Excerpts: []store.FeishuExcerpt{}, SourceRefs: []store.FeishuSourceRef{}, ConversationIDs: []string{}}
	if len(messages) > 500 {
		messages = messages[:500]
		report.Truncated = true
	}
	groups := []feishuInputGroup{}
	indexes := map[string]int{}
	counts := map[string]int{}
	// Input rows are newest-first. Select at most four distinct conversations in
	// that stable order, then reverse each bounded group to chronological order.
	for _, message := range messages {
		if digestRelevant(message.DigestMessage) < 3 {
			continue
		}
		// Do not let an empty/control-only row consume one of four source slots.
		body, cut := feishuBoundText(message.Message.Body, 600)
		report.Truncated = report.Truncated || cut
		if body == "" {
			continue
		}
		index, exists := indexes[message.ConversationID]
		if !exists {
			if len(groups) >= 4 {
				report.Truncated = true
				continue
			}
			index = len(groups)
			indexes[message.ConversationID] = index
			groups = append(groups, feishuInputGroup{Title: message.Title, Input: agent.Input{ConversationID: message.ConversationID, Tone: "natural", Messages: []agent.Evidence{}, Memories: []agent.Memory{}}})
			report.SourceRefs = append(report.SourceRefs, store.FeishuSourceRef{ConversationID: message.ConversationID, SettingsRevision: message.SettingsRevision})
			report.ConversationIDs = append(report.ConversationIDs, message.ConversationID)
		}
		if counts[message.ConversationID] >= 20 {
			report.Truncated = true
			continue
		}
		evidence := message.Message
		evidence.Body = body
		groups[index].Input.Messages = append(groups[index].Input.Messages, evidence)
		counts[message.ConversationID]++
		if len(report.Excerpts) < 3 {
			text, cut := feishuExcerptText(message.Message.Body, 120)
			report.Truncated = report.Truncated || cut
			report.Excerpts = append(report.Excerpts, store.FeishuExcerpt{MessageID: evidence.ID, ConversationID: message.ConversationID, Text: text, OccurredAt: evidence.OccurredAt})
		}
		if len(report.Todos) < 4 && evidence.Direction == "incoming" && feishuTaskCandidate(body) {
			text, cut := feishuExcerptText(body, 100)
			report.Truncated = report.Truncated || cut
			report.Todos = append(report.Todos, store.FeishuTodo{Text: "待人工核对：" + text, EvidenceIDs: []string{evidence.ID}, NeedsReview: true})
		}
	}
	// Empty/invalid bodies cannot create source-less provider calls. Preserve refs
	// only for actual model input; all remaining sources were claimed before IO.
	filtered := groups[:0]
	refs := report.SourceRefs[:0]
	ids := report.ConversationIDs[:0]
	for index, group := range groups {
		if len(group.Input.Messages) == 0 {
			continue
		}
		for left, right := 0, len(group.Input.Messages)-1; left < right; left, right = left+1, right-1 {
			group.Input.Messages[left], group.Input.Messages[right] = group.Input.Messages[right], group.Input.Messages[left]
		}
		filtered = append(filtered, group)
		refs = append(refs, report.SourceRefs[index])
		ids = append(ids, group.Input.ConversationID)
	}
	report.SourceRefs = refs
	report.ConversationIDs = ids
	report.ConversationCount = len(filtered)
	return report, filtered
}
func feishuTaskCandidate(body string) bool { return store.FeishuTaskCandidate(body) }
func feishuContainsSecrets(value any, config store.FeishuConfig, extra ...string) bool {
	raw, err := json.Marshal(value)
	if err != nil {
		return true
	}
	var decoded any
	// Match JSON-escaped and decoded strings alike; keys are not secrets. Walking
	// scalar values avoids being confused by escaped quotes/backslashes in keys.
	if json.Unmarshal(raw, &decoded) != nil {
		return true
	}
	secrets := append([]string{config.AppSecret, config.RecipientOpenID}, extra...)
	var visit func(any) bool
	visit = func(value any) bool {
		switch item := value.(type) {
		case string:
			for _, secret := range secrets {
				if secret != "" && strings.Contains(item, secret) {
					return true
				}
			}
		case []any:
			for _, child := range item {
				if visit(child) {
					return true
				}
			}
		case map[string]any:
			for _, child := range item {
				if visit(child) {
					return true
				}
			}
		}
		return false
	}
	return visit(decoded)
}
func feishuInputContainsSecrets(input agent.Input, config store.FeishuConfig, extra ...string) bool {
	return feishuContainsSecrets(input, config, extra...)
}
func feishuOutputContainsSecrets(output agent.Output, config store.FeishuConfig, extra ...string) bool {
	return feishuContainsSecrets(output, config, extra...)
}

func renderFeishuReport(report *store.FeishuReport, config store.FeishuConfig) error {
	start, err := time.Parse(time.RFC3339Nano, report.WindowStart)
	if err != nil {
		return store.ErrInvalidInput
	}
	end, err := time.Parse(time.RFC3339Nano, report.WindowEnd)
	if err != nil {
		return store.ErrInvalidInput
	}
	zone := time.FixedZone(store.FeishuTimezone, 8*60*60)
	var text strings.Builder
	fmt.Fprintf(&text, "消息摘要 · %s—%s（Asia/Shanghai）\n窗口含起点、不含终点；仅发本人私聊，接收归属需本人核对。\n\n摘要（AI生成，需核实）\n%s\n", start.In(zone).Format("01-02 15:04"), end.In(zone).Format("01-02 15:04"), report.Summary)
	text.WriteString("\n待办候选（原文线索，均待人工核对；非已确认任务）\n")
	if len(report.Todos) == 0 {
		text.WriteString("未提取到明确线索，不代表没有待办。\n")
	}
	for index, todo := range report.Todos {
		fmt.Fprintf(&text, "%d. %s [证据 %s]\n", index+1, todo.Text, strings.Join(todo.EvidenceIDs, ","))
	}
	text.WriteString("\n原文短摘录（标签已中和，长文截断）\n")
	if len(report.Excerpts) == 0 {
		text.WriteString("无可用摘录。\n")
	}
	for _, excerpt := range report.Excerpts {
		fmt.Fprintf(&text, "「%s」[消息 %s]\n", excerpt.Text, excerpt.MessageID)
	}
	fmt.Fprintf(&text, "\n覆盖 %d 个会话；最多500条候选、4会话×20条，不代表完整历史。", report.ConversationCount)
	if report.Truncated {
		text.WriteString("\n已截断或省略超出预算的消息、会话或文字。")
	}
	text.WriteString("\n不会自动重算、重发或离线补发；平台受理不等于送达/已读。")
	report.Text = feishuNeutralText(text.String())
	clipped, cut := feishuCutBytes(report.Text, store.FeishuMaxTextBytes-100)
	if cut {
		report.Text = clipped + "\n[正文已截断，存在省略]"
		report.Truncated = true
	}
	credentials := feishu.Credentials{AppID: config.AppID, AppSecret: config.AppSecret}
	for {
		err = feishu.ValidateInput(credentials, feishu.SendInput{RecipientOpenID: config.RecipientOpenID, Text: report.Text, UUID: store.FeishuDeliveryID(report.WindowEnd)})
		if err == nil {
			break
		}
		var platformErr *feishu.Error
		if !errors.As(err, &platformErr) || platformErr.Error() != feishu.CodeMessageTooLarge || len(report.Text) < 400 {
			return err
		}
		report.Text, _ = feishuCutBytes(report.Text, len(report.Text)*9/10-100)
		report.Text += "\n[两层JSON预算限制，正文已截断并省略]"
		report.Truncated = true
	}
	if feishuContainsSecrets(struct {
		Summary  string
		Todos    []store.FeishuTodo
		Excerpts []store.FeishuExcerpt
		Text     string
	}{report.Summary, report.Todos, report.Excerpts, report.Text}, config) {
		return store.ErrInvalidInput
	}
	report.ContentHash = store.FeishuContentHash(*report)
	return nil
}
