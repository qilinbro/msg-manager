package onebot

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/domain"
)

func privateEvent(message string) []byte {
	return []byte(fmt.Sprintf(`{"time":1794467648,"self_id":%s,"post_type":"message","message_type":"private","sub_type":"friend","message_id":-9007199254740997,"user_id":9007199254740995,"sender":{"user_id":9007199254740995,"nickname":"not-an-id"},"message":%s}`, testSelfID, message))
}

func TestNormalizePrivateTextPreservesExactIdentifiersAndEvidenceTime(t *testing.T) {
	scope := domain.ConnectionScope{WorkspaceID: "workspace", ConnectionID: "qq-local-account"}
	received := time.Date(2026, time.November, 12, 16, 0, 0, 0, time.FixedZone("test", 8*3600))
	for _, test := range []struct{ raw, body string }{
		{`[{"type":"text","data":{"text":"你好"}},{"type":"text","data":{"text":"，世界"}}]`, "你好，世界"},
		{`"hello &#91;literal&#93; &amp; text"`, "hello [literal] & text"},
	} {
		event, err := NormalizePrivateText(privateEvent(test.raw), scope, testSelfID, received)
		if err != nil {
			t.Fatal(err)
		}
		message := event.Message
		if event.Scope != scope || message == nil || message.Scope.ConnectionScope != scope || message.Scope.ContactID != "qq:"+testSelfID+":9007199254740995" || message.Scope.ConversationID != "qq:private:"+testSelfID+":9007199254740995" {
			t.Fatal("normalization failed account/contact isolation")
		}
		if message.ID != "" || message.ExternalMessageID != "-9007199254740997" || !strings.Contains(event.ExternalEventID, "9007199254740995:-9007199254740997") || message.Body != test.body || message.Direction != "inbound" || message.Version != 1 {
			t.Fatalf("normalization lost ID precision or text: %+v", message)
		}
		if !message.ReceivedAt.Equal(received) || message.ReceivedAt.Location() != time.UTC || message.OccurredAt.Unix() != 1794467648 || event.Kind != "message.private.text" {
			t.Fatal("normalization lost event time or kind")
		}
		// Quoted decimal IDs are also accepted without float conversion.
		quoted := strings.ReplaceAll(string(privateEvent(test.raw)), `"self_id":`+testSelfID, `"self_id":"`+testSelfID+`"`)
		if _, err := NormalizePrivateText([]byte(quoted), scope, testSelfID, received); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNormalizePrivateTextRejectsUnsupportedAndAmbiguousEvents(t *testing.T) {
	scope := domain.ConnectionScope{WorkspaceID: "workspace", ConnectionID: "qq-local-account"}
	valid := string(privateEvent(`"hello"`))
	cases := []struct {
		name, raw   string
		unsupported bool
		kind        Kind
	}{
		{"group", strings.Replace(valid, `"message_type":"private"`, `"message_type":"group"`, 1), true, ""},
		{"temporary-private", strings.Replace(valid, `"sub_type":"friend"`, `"sub_type":"group"`, 1), true, ""},
		{"message-sent", strings.Replace(valid, `"post_type":"message"`, `"post_type":"message_sent"`, 1), true, ""},
		{"spoofed-private-with-group", strings.Replace(valid, `"post_type"`, `"group_id":123,"post_type"`, 1), true, ""},
		{"other-account", strings.Replace(valid, `"self_id":`+testSelfID, `"self_id":123`, 1), false, IdentityMismatch},
		{"self-echo", strings.ReplaceAll(valid, "9007199254740995", testSelfID), true, ""},
		{"sender-mismatch", strings.Replace(valid, `"sender":{"user_id":9007199254740995`, `"sender":{"user_id":123`, 1), false, ProtocolError},
		{"float-id", strings.Replace(valid, `"self_id":`+testSelfID, `"self_id":9.007199254740993e15`, 1), false, ProtocolError},
		{"duplicate-account", strings.Replace(valid, `"self_id":`, `"self_id":123,"self_id":`, 1), false, ProtocolError},
		{"trailing-json", valid + ` {}`, false, ProtocolError},
		{"cq-image", string(privateEvent(`"caption[CQ:image,file=secret.png]"`)), true, ""},
		{"escaped-json-cq", string(privateEvent(`"\u005bCQ:at,qq=all]"`)), true, ""},
		{"mixed-image", string(privateEvent(`[{"type":"text","data":{"text":"caption"}},{"type":"image","data":{"file":"secret.png"}}]`)), true, ""},
		{"reply-segment", string(privateEvent(`[{"type":"reply","data":{"id":"123"}},{"type":"text","data":{"text":"caption"}}]`)), true, ""},
		{"empty-segments", string(privateEvent(`[]`)), true, ""},
		{"bad-text-type", string(privateEvent(`[{"type":"text","data":{"text":123}}]`)), false, ProtocolError},
		{"null-sender", strings.Replace(valid, `{"user_id":9007199254740995,"nickname":"not-an-id"}`, `null`, 1), false, ProtocolError},
		{"invalid-time", strings.Replace(valid, `"time":1794467648`, `"time":1.5`, 1), false, ProtocolError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event, err := NormalizePrivateText([]byte(tc.raw), scope, testSelfID, time.Now())
			if event.Message != nil || event.ExternalEventID != "" {
				t.Fatal("rejected event produced partially trusted data")
			}
			if tc.unsupported {
				if !errors.Is(err, ErrUnsupported) {
					t.Fatalf("expected unsupported, got %v", err)
				}
			} else {
				expectKind(t, err, tc.kind)
			}
		})
	}
}

func TestNormalizePrivateTextRequiresBoundScopeAndLimitsInput(t *testing.T) {
	raw := privateEvent(`"hello"`)
	_, err := NormalizePrivateText(raw, domain.ConnectionScope{}, testSelfID, time.Now())
	if !errors.Is(err, domain.ErrScopeDenied) {
		t.Fatal("accepted an unbound connection scope")
	}
	scope := domain.ConnectionScope{WorkspaceID: "workspace", ConnectionID: "connection"}
	_, err = NormalizePrivateText(raw, scope, "0", time.Now())
	expectKind(t, err, InvalidConfig)
	_, err = NormalizePrivateText(raw, scope, testSelfID, time.Time{})
	expectKind(t, err, InvalidConfig)
	large, _ := json.Marshal(strings.Repeat("x", maxResponseBytes+1))
	_, err = NormalizePrivateText(privateEvent(string(large)), scope, testSelfID, time.Now())
	expectKind(t, err, ProtocolError)
}
