package mailbox

import (
	"context"
	"strings"
	"testing"
)

func TestIncrementalReadUsesUIDNextNumericBoundsAndPeek(t *testing.T) {
	c, commands, mu := mockMailbox(t, testServerOptions{count: 1})
	info, err := c.Probe(context.Background())
	if err != nil || info.UIDNext != 78 {
		t.Fatal("UIDNEXT probe", info, err)
	}
	delta, err := c.FetchAfter(context.Background(), Cursor{UIDValidity: 123, NextUID: 78})
	if err != nil || len(delta.Mails) != 0 || delta.Next.NextUID != 78 || delta.MissingUIDCount != 0 {
		t.Fatal("idle cursor replayed old mail", delta, err)
	}
	mu.Lock()
	for _, cmd := range *commands {
		if strings.Contains(cmd, "UID FETCH") {
			t.Fatal("idle scan issued UID n:* or other fetch", cmd)
		}
	}
	mu.Unlock()
	delta, err = c.FetchAfter(context.Background(), Cursor{UIDValidity: 123, NextUID: 77})
	if err != nil || len(delta.Mails) != 1 || delta.Next.NextUID != 78 || delta.Skipped != 0 || delta.MissingUIDCount != 0 || delta.Truncated {
		t.Fatal("new UID not read", delta, err)
	}
	mu.Lock()
	defer mu.Unlock()
	metadata, body := false, false
	for _, cmd := range *commands {
		if strings.Contains(cmd, "UID FETCH") {
			if strings.Contains(cmd, "*") {
				t.Fatal("unbounded UID scan", cmd)
			}
			if strings.Contains(cmd, "RFC822.SIZE") {
				metadata = true
			} else if strings.Contains(cmd, "BODY.PEEK[]") {
				body = true
			} else {
				t.Fatal("unsafe read", cmd)
			}
		}
		if strings.Contains(cmd, "STORE") || strings.Contains(cmd, "EXPUNGE") || strings.Contains(cmd, "SELECT ") {
			t.Fatal("mailbox mutation", cmd)
		}
	}
	if !metadata || !body {
		t.Fatal("missing UID metadata/body reads")
	}
}
func TestIncrementalReportsHolesAndHandledSkippedUIDs(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		c, commands, mu := mockMailbox(t, testServerOptions{count: 1, oversized: oversized})
		d, err := c.FetchAfter(context.Background(), Cursor{UIDValidity: 123, NextUID: 1})
		if err != nil || d.Next.NextUID != 78 || d.MissingUIDCount != 76 {
			t.Fatal("unreported numeric UID holes", d, err)
		}
		if oversized {
			if len(d.Mails) != 0 || len(d.SkippedUIDs) != 1 || d.SkippedUIDs[0].UID != 77 || d.SkippedUIDs[0].Reason != "oversized" {
				t.Fatal("oversize advanced silently", d)
			}
			mu.Lock()
			for _, cmd := range *commands {
				if strings.Contains(cmd, "BODY.PEEK") {
					t.Fatal("downloaded oversized body")
				}
			}
			mu.Unlock()
		}
	}
}
func TestIncrementalUIDGenerationAndRegressionFailWithoutBodies(t *testing.T) {
	c, commands, mu := mockMailbox(t, testServerOptions{count: 1})
	for _, tc := range []struct {
		cursor Cursor
		kind   Kind
	}{{Cursor{UIDValidity: 124, NextUID: 77}, UIDValidityChanged}, {Cursor{UIDValidity: 123, NextUID: 79}, ProtocolError}, {Cursor{}, InvalidConfig}} {
		d, err := c.FetchAfter(context.Background(), tc.cursor)
		expectKind(t, err, tc.kind)
		if len(d.Mails) > 0 || d.Next.NextUID != 0 {
			t.Fatal("generation error returned partial progress")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, cmd := range *commands {
		if strings.Contains(cmd, "UID FETCH") {
			t.Fatal("generation failure read data")
		}
	}
}
