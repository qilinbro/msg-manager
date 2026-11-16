package qqmilky

import (
	"context"
	"encoding/json"

	"github.com/qilinbro/msg-manager/internal/connector/adapter"
)

type Client struct {
	base         adapter.Base
	milkyVersion string
	historyLimit int
}

func NewClient(cfg Config) (*Client, error) {
	cfg = cfg.normalized()
	if !cfg.valid() {
		return nil, adapter.Failure(adapter.InvalidConfig)
	}
	base, err := adapter.New(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	return &Client{base: base, milkyVersion: cfg.MilkyVersion, historyLimit: cfg.HistoryLimit}, nil
}

func (c *Client) SelfID() string { return c.base.SelfID() }

// Probe verifies the logged-in account and the protocol version. It is a
// one-shot check, not a statement of continuous availability or permission
// to receive/send messages.
func (c *Client) Probe(ctx context.Context) (Verification, error) {
	if c == nil {
		return Verification{}, adapter.Failure(adapter.NotConfigured)
	}
	loginData, err := c.base.Call(ctx, "get_login_info", nil)
	if err != nil {
		return Verification{}, err
	}
	var login struct {
		UIN      json.RawMessage `json:"uin"`
		Nickname string          `json:"nickname"`
	}
	if err := adapter.DecodeJSON(loginData, &login); err != nil {
		return Verification{}, adapter.Failure(adapter.ProtocolError)
	}
	accountID, ok := adapter.ExactID(login.UIN)
	if !ok || accountID != c.base.SelfID() {
		return Verification{}, adapter.Failure(adapter.IdentityMismatch)
	}
	implData, err := c.base.Call(ctx, "get_impl_info", nil)
	if err != nil {
		return Verification{}, err
	}
	var impl struct {
		Name         string `json:"impl_name"`
		Version      string `json:"impl_version"`
		MilkyVersion string `json:"milky_version"`
		ProtocolType string `json:"qq_protocol_type"`
	}
	if err := adapter.DecodeJSON(implData, &impl); err != nil {
		return Verification{}, adapter.Failure(adapter.ProtocolError)
	}
	if impl.MilkyVersion != c.milkyVersion {
		return Verification{}, adapter.Failure(adapter.VersionMismatch)
	}
	return Verification{
		AccountID: accountID, Nickname: login.Nickname, ImplName: impl.Name,
		ImplVersion: impl.Version, MilkyVersion: impl.MilkyVersion,
		ProtocolType: impl.ProtocolType, SelfIDVerified: true,
	}, nil
}

func (c *Client) Friends(ctx context.Context) ([]Friend, error) {
	if c == nil {
		return nil, adapter.Failure(adapter.NotConfigured)
	}
	data, err := c.base.Call(ctx, "get_friend_list", struct {
		NoCache bool `json:"no_cache"`
	}{NoCache: false})
	if err != nil {
		return nil, err
	}
	var response struct {
		Friends []Friend `json:"friends"`
	}
	if err := adapter.DecodeJSON(data, &response); err != nil {
		return nil, adapter.Failure(adapter.ProtocolError)
	}
	friends := make([]Friend, 0, len(response.Friends))
	for _, friend := range response.Friends {
		if _, ok := adapter.ExactID(friend.UserID); !ok {
			return nil, adapter.Failure(adapter.ProtocolError)
		}
		friends = append(friends, friend)
	}
	return friends, nil
}

// History performs one bounded page. The caller owns the cursor: only after a
// successful transactional write may next_message_seq become the next cursor.
func (c *Client) History(ctx context.Context, scene, peerID string, startSeq *int64, limit int) ([]IncomingMessage, *int64, error) {
	if c == nil {
		return nil, nil, adapter.Failure(adapter.NotConfigured)
	}
	if scene != "friend" || limit < 1 || limit > MaxHistoryLimit {
		return nil, nil, adapter.Failure(adapter.InvalidConfig)
	}
	peer, err := parseID(peerID)
	if err != nil {
		return nil, nil, err
	}
	request := struct {
		Scene    string `json:"message_scene"`
		PeerID   int64  `json:"peer_id"`
		StartSeq *int64 `json:"start_message_seq,omitempty"`
		Limit    int    `json:"limit"`
	}{Scene: scene, PeerID: peer, StartSeq: startSeq, Limit: limit}
	data, err := c.base.Call(ctx, "get_history_messages", request)
	if err != nil {
		return nil, nil, err
	}
	var response struct {
		Messages []IncomingMessage `json:"messages"`
		Next     *int64            `json:"next_message_seq"`
	}
	if err := adapter.DecodeJSON(data, &response); err != nil {
		return nil, nil, adapter.Failure(adapter.ProtocolError)
	}
	for _, message := range response.Messages {
		if _, ok := adapter.ExactID(message.MessageSeq); !ok {
			return nil, nil, adapter.Failure(adapter.ProtocolError)
		}
	}
	return response.Messages, response.Next, nil
}

// SendPrivate implements the protocol send call for the future policy-checked
// outbox executor. It is intentionally NOT wired to any HTTP route: sending
// stays disabled until T15/T16/T17 are reviewed and accepted.
func (c *Client) SendPrivate(ctx context.Context, userID string, text string) (SendReceipt, error) {
	if c == nil {
		return SendReceipt{}, adapter.Failure(adapter.NotConfigured)
	}
	peer, err := parseID(userID)
	if err != nil {
		return SendReceipt{}, err
	}
	request := struct {
		UserID  int64             `json:"user_id"`
		Message []outgoingSegment `json:"message"`
	}{UserID: peer, Message: []outgoingSegment{{Type: "text", Data: outgoingText{Text: text}}}}
	data, err := c.base.Call(ctx, "send_private_message", request)
	if err != nil {
		return SendReceipt{}, err
	}
	var response struct {
		MessageSeq json.RawMessage `json:"message_seq"`
		Time       int64           `json:"time"`
	}
	if err := adapter.DecodeJSON(data, &response); err != nil {
		return SendReceipt{}, adapter.Failure(adapter.ProtocolError)
	}
	seq, ok := adapter.ExactID(response.MessageSeq)
	if !ok {
		return SendReceipt{}, adapter.Failure(adapter.ProtocolError)
	}
	return SendReceipt{MessageSeq: seq, Time: response.Time}, nil
}
