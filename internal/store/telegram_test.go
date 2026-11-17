package store

import (
	"context"
	"errors"
	"testing"
)

func TestTelegramReaderImportRemainsPrivateAndScoped(t *testing.T) {
	s := testStore(t)
	req := ImportRequest{Platform: "telegram", AccountID: "123", AccountName: "Telegram个人账号(在线只读)", Messages: []ImportMessage{importMessage("telegram:user:456:1", "456", "synthetic body", "2026-01-01T00:00:00Z")}}
	for _, mutate := range []func(*ImportRequest){
		func(r *ImportRequest) { r.Platform = "qq" },
		func(r *ImportRequest) { r.Messages[0].ConversationKind = "group" },
		func(r *ImportRequest) { r.Messages[0].ExternalID = "telegram:user:789:1" },
		func(r *ImportRequest) { r.Messages[0].Email = &EmailHeaders{} },
	} {
		copy := req
		copy.Messages = append([]ImportMessage(nil), req.Messages...)
		mutate(&copy)
		if _, err := s.ImportTelegramContext(context.Background(), copy); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid reader source accepted", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ImportTelegramContext(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled import wrote", err)
	}
	for _, account := range []string{"123", "789"} {
		req.AccountID = account
		first, err := s.ImportTelegramContext(context.Background(), req)
		if err != nil || first.Inserted != 1 {
			t.Fatal(err)
		}
		replay, err := s.ImportTelegramContext(context.Background(), req)
		if err != nil || replay.Duplicates != 1 || replay.Inserted != 0 {
			t.Fatal("reader duplicate", err)
		}
	}
	page := mustConversations(t, s, Filter{Platform: "telegram"})
	if len(page.Items) != 2 {
		t.Fatal("accounts were merged")
	}
	for _, conversation := range page.Items {
		messages, err := s.Messages(conversation.ID, "", 50)
		if err != nil || len(messages.Items) != 1 || messages.Items[0].Source != "import" || conversation.ConversationKind != "private" {
			t.Fatal("compatibility provenance or private scope changed", err)
		}
	}
}
