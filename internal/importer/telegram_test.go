package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const testPeer = "9007199254740993"

func exported(t *testing.T, messages []map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"id": json.Number(testPeer), "type": "personal_chat", "name": "同名朋友",
		"messages": messages,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func textMessage(id, sender string, text any) map[string]any {
	return map[string]any{
		"id": json.Number(id), "type": "message", "from_id": "user" + sender,
		"date_unixtime": "1700000000", "date": "2023-11-14T22:13:20", "text": text,
	}
}

func TestTelegramPrivatePreservesVisibleTextAndExactIdentity(t *testing.T) {
	raw := exported(t, []map[string]any{
		textMessage("9007199254740995", testPeer, []any{
			"你好，", map[string]any{"type": "bold", "text": "世界"},
			map[string]any{"type": "text_link", "text": "查看作品", "href": "https://private.invalid/secret"},
			map[string]any{"type": "mention_name", "text": "朋友", "user_id": "hidden-identity"},
		}),
		textMessage("2", "7", "回复\n第二行"),
	})
	result, err := TelegramPrivate(raw, "0007")
	if err != nil || len(result.Messages) != 2 || result.Skipped != 0 {
		t.Fatalf("result count/skip mismatch: count=%d skipped=%d err=%v", len(result.Messages), result.Skipped, err)
	}
	in, out := result.Messages[0], result.Messages[1]
	if in.PeerID != testPeer || in.ExternalID != testPeer+":9007199254740995" || in.PeerName != "同名朋友" {
		t.Fatal("stable identity lost precision or was replaced by a display name")
	}
	if in.Direction != "incoming" || out.Direction != "outgoing" {
		t.Fatal("direction was not derived from the explicitly selected account")
	}
	if in.Body != "你好，世界查看作品朋友" || out.Body != "回复\n第二行" {
		t.Fatal("visible text was not concatenated correctly")
	}
	if in.OccurredAt != "2023-11-14T22:13:20Z" {
		t.Fatal("absolute export timestamp was not normalized to UTC")
	}
	encoded, _ := json.Marshal(result)
	if bytes.Contains(encoded, []byte("private.invalid")) || bytes.Contains(encoded, []byte("hidden-identity")) {
		t.Fatal("hidden entity metadata escaped into canonical messages")
	}
}

func TestTelegramPrivateSkipsUnsupportedContentWithCount(t *testing.T) {
	service := textMessage("1", testPeer, "服务消息")
	service["type"] = "service"
	photo := textMessage("2", testPeer, "照片说明")
	photo["photo"] = "../outside/not-read.jpg"
	file := textMessage("3", testPeer, "文件说明")
	file["file"] = "https://not-fetched.invalid/file"
	unknownSender := textMessage("4", "111", "不属于这段私聊")
	noSender := textMessage("5", testPeer, "来源未知")
	delete(noSender, "from_id")
	empty := textMessage("6", testPeer, "  \n")
	result, err := TelegramPrivate(exported(t, []map[string]any{
		service, photo, file, unknownSender, noSender, empty,
		textMessage("7", testPeer, "保留的纯文本"),
	}), "7")
	if err != nil || result.Skipped != 6 || len(result.Messages) != 1 || result.Messages[0].Body != "保留的纯文本" {
		t.Fatalf("unsupported content accounting differs: count=%d skipped=%d err=%v", len(result.Messages), result.Skipped, err)
	}
}

func TestTelegramPrivateRejectsAmbiguousOrMalformedExports(t *testing.T) {
	valid := string(exported(t, []map[string]any{textMessage("1", testPeer, "ok")}))
	cases := map[string]string{
		"full-account":     `{"chats":{"list":[]}}`,
		"group":            strings.Replace(valid, `"personal_chat"`, `"private_group"`, 1),
		"self-chat":        strings.Replace(valid, testPeer, "7", -1),
		"float-id":         strings.Replace(valid, `"id":9007199254740993`, `"id":9.007199254740993e15`, 1),
		"duplicate-key":    strings.Replace(valid, `"name":`, `"name":"other","name":`, 1),
		"trailing":         valid + `{}`,
		"invalid-utf8":     valid[:len(valid)-1] + string([]byte{0xff}) + "}",
		"ambiguous-entity": strings.Replace(valid, `"text":"ok"`, `"text":[{"text":"missing entity type"}]`, 1),
		"bad-time":         strings.Replace(valid, `"1700000000"`, `"not-a-timestamp"`, 1),
		"time-overflow":    strings.Replace(valid, `"1700000000"`, `"999999999999999999999999"`, 1),
		"future-time":      strings.Replace(valid, `"1700000000"`, `"4133980800"`, 1),
		"nested":           strings.TrimSuffix(valid, "}") + `,"extra":` + strings.Repeat("[", 34) + "0" + strings.Repeat("]", 34) + "}",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			result, err := TelegramPrivate([]byte(raw), "7")
			if !errors.Is(err, ErrInvalidExport) || len(result.Messages) != 0 || result.Skipped != 0 {
				t.Fatalf("malformed export was not rejected atomically: err=%v", err)
			}
		})
	}
	for _, account := range []string{"", "0", "-7", "user7", " 7", "7.0"} {
		if _, err := TelegramPrivate([]byte(valid), account); !errors.Is(err, ErrInvalidExport) {
			t.Fatalf("invalid explicit account accepted: %q", account)
		}
	}
}

func TestTelegramPrivateLimitsAreAtomic(t *testing.T) {
	tooMany := make([]map[string]any, maxMessages+1)
	for i := range tooMany {
		tooMany[i] = textMessage(fmt.Sprint(i+1), testPeer, "正文")
	}
	cases := map[string][]byte{
		"file-size": bytes.Repeat([]byte(" "), maxExportBytes+1),
		"messages":  exported(t, tooMany),
		"body-size": exported(t, []map[string]any{
			textMessage("1", testPeer, "有效开头"),
			textMessage("2", testPeer, strings.Repeat("字", maxBodyBytes/3+1)),
		}),
		"entity-size": exported(t, []map[string]any{textMessage("1", testPeer, []any{
			strings.Repeat("a", maxBodyBytes), map[string]any{"type": "bold", "text": "x"},
		})}),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			result, err := TelegramPrivate(raw, "7")
			if !errors.Is(err, ErrLimitExceeded) || len(result.Messages) != 0 || result.Skipped != 0 {
				t.Fatalf("limit failure returned partial messages: err=%v", err)
			}
		})
	}
}

func TestTelegramPrivateBoundaryAndConversationMessageKey(t *testing.T) {
	raw := exported(t, []map[string]any{textMessage("1", testPeer, strings.Repeat("a", maxBodyBytes))})
	first, err := TelegramPrivate(raw, "7")
	if err != nil || len(first.Messages) != 1 || len(first.Messages[0].Body) != maxBodyBytes {
		t.Fatal("exact body size boundary should be accepted")
	}
	second, err := TelegramPrivate(bytes.ReplaceAll(raw, []byte(testPeer), []byte("9007199254740997")), "7")
	if err != nil || first.Messages[0].ExternalID == second.Messages[0].ExternalID {
		t.Fatal("message IDs from separate conversations would collide")
	}
}
