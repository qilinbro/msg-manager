package mailbox

import (
	"context"
	"io"
	"sort"
	"strings"

	"github.com/emersion/go-imap"
)

// Cursor is a UID generation and the first UID not yet examined. It is never a
// message sequence number or an upstream sender-controlled Message-ID.
type Cursor struct {
	UIDValidity uint32 `json:"uid_validity"`
	NextUID     uint32 `json:"next_uid"`
}
type SkippedUID struct {
	UID    uint32 `json:"uid"`
	Reason string `json:"reason"`
}
type Delta struct {
	Batch
	Next            Cursor       `json:"next"`
	SkippedUIDs     []SkippedUID `json:"skipped_uids"`
	MissingUIDCount int          `json:"missing_uid_count"`
}

const UIDWindow = 200

// FetchAfter scans a finite numeric UID window, oldest first. UID n:* is
// intentionally forbidden: IMAP includes the highest existing UID even when n
// is greater. Missing numeric slots are reported, not claimed as lost mail.
func (c *Client) FetchAfter(ctx context.Context, cursor Cursor) (Delta, error) {
	if cursor.UIDValidity == 0 || cursor.NextUID == 0 {
		return Delta{}, fail(InvalidConfig)
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	budget := int64(c.cfg.Limit)*(MaxRawMessageBytes+(16<<10)) + (1 << 20)
	s, err := c.open(ctx, budget)
	if err != nil {
		return Delta{}, err
	}
	defer s.close()
	if s.info.UIDValidity != cursor.UIDValidity {
		return Delta{}, fail(UIDValidityChanged)
	}
	if s.info.UIDNext == 0 || s.info.UIDNext < cursor.NextUID {
		return Delta{}, fail(ProtocolError)
	}
	result := Delta{Batch: Batch{Info: s.info, Mails: []Mail{}}, Next: cursor, SkippedUIDs: []SkippedUID{}}
	if cursor.NextUID == s.info.UIDNext {
		return result, nil
	}
	end := uint64(cursor.NextUID) + UIDWindow - 1
	if high := uint64(s.info.UIDNext) - 1; end > high {
		end = high
	}
	set := new(imap.SeqSet)
	set.AddRange(cursor.NextUID, uint32(end))
	metadata, err := fetchMessages(s.client, set, []imap.FetchItem{imap.FetchUid, imap.FetchRFC822Size, imap.FetchFlags, imap.FetchInternalDate}, true, UIDWindow)
	if err != nil {
		return Delta{}, err
	}
	sort.Slice(metadata, func(i, j int) bool { return metadata[i].Uid < metadata[j].Uid })
	seen := map[uint32]bool{}
	for _, meta := range metadata {
		if meta.Uid == 0 || meta.Uid < cursor.NextUID || uint64(meta.Uid) > end || seen[meta.Uid] {
			return Delta{}, fail(ProtocolError)
		}
		seen[meta.Uid] = true
	}
	count := len(metadata)
	if count > c.cfg.Limit {
		count = c.cfg.Limit
		end = uint64(metadata[count-1].Uid)
	}
	for _, meta := range metadata[:count] {
		mail, reason, err := readUID(s, meta)
		if err != nil {
			return Delta{}, err
		}
		if reason != "" {
			result.SkippedUIDs = append(result.SkippedUIDs, SkippedUID{UID: meta.Uid, Reason: reason})
			result.Skipped++
			continue
		}
		result.Mails = append(result.Mails, mail)
	}
	result.Next.NextUID = uint32(end + 1)
	result.Truncated = result.Next.NextUID < s.info.UIDNext
	result.MissingUIDCount = int(end-uint64(cursor.NextUID)+1) - count
	return result, nil
}

func readUID(s *session, meta *imap.Message) (Mail, string, error) {
	if meta.Size > MaxRawMessageBytes {
		return Mail{}, "oversized", nil
	}
	set := new(imap.SeqSet)
	set.AddNum(meta.Uid)
	section := &imap.BodySectionName{Peek: true}
	messages, err := fetchMessages(s.client, set, []imap.FetchItem{imap.FetchUid, section.FetchItem()}, true, 1)
	if err != nil {
		return Mail{}, "", err
	}
	if len(messages) == 0 {
		return Mail{}, "vanished", nil
	}
	if messages[0].Uid != meta.Uid {
		return Mail{}, "", fail(ProtocolError)
	}
	body := messages[0].GetBody(section)
	if body == nil {
		return Mail{}, "missing_body", nil
	}
	raw, err := io.ReadAll(io.LimitReader(body, MaxRawMessageBytes+1))
	if err != nil {
		return Mail{}, "", fail(ProtocolError)
	}
	if len(raw) > MaxRawMessageBytes {
		return Mail{}, "oversized", nil
	}
	seen := false
	for _, flag := range meta.Flags {
		if strings.EqualFold(flag, imap.SeenFlag) {
			seen = true
		}
	}
	mail, err := parseMail(raw, s.info.UIDValidity, meta.Uid, meta.InternalDate, seen)
	if err != nil {
		return Mail{}, "unsupported_message", nil
	}
	return mail, "", nil
}
