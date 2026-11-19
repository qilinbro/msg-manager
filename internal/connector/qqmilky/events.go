package qqmilky

import (
	"context"
	"errors"
	"time"

	"github.com/gorilla/websocket"
	"github.com/qilinbro/msg-manager/internal/connector/adapter"
)

const reconnectDelay = 2 * time.Second
const eventReadTimeout = 90 * time.Second

// Run freshly verifies every connection to the forward /event stream. Milky
// events have no unique ID, cursor or replay: disconnects are reported so the
// API layer can reconcile. Identity/version failures are terminal, not gaps
// that may be reconciled or automatically retried under the old authorization.
func (c *Client) Run(ctx context.Context, onEvent func(NormalizedMessage) error, onGap func(Gap)) {
	if c == nil || ctx == nil {
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		_, err := c.Probe(ctx)
		if err == nil {
			var conn *websocket.Conn
			conn, err = c.base.DialEvent(ctx)
			if err == nil {
				err = c.readLoop(ctx, conn, onEvent)
				_ = conn.Close()
			}
		}
		if ctx.Err() != nil {
			return
		}
		reason := classify(err)
		reportGap(onGap, reason)
		if reason == string(adapter.IdentityMismatch) || reason == string(adapter.VersionMismatch) || !wait(ctx) {
			return
		}
	}
}

func (c *Client) readLoop(ctx context.Context, conn *websocket.Conn, onEvent func(NormalizedMessage) error) error {
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := conn.SetReadDeadline(time.Now().Add(eventReadTimeout)); err != nil {
			return err
		}
		kind, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if kind != websocket.TextMessage {
			return adapter.Failure(adapter.ProtocolError)
		}
		var event Event
		if err := adapter.DecodeJSON(raw, &event); err != nil {
			return err
		}
		switch event.EventType {
		case "message_receive":
			selfID, ok := adapter.ExactID(event.SelfID)
			if !ok || selfID != c.base.SelfID() {
				return adapter.Failure(adapter.IdentityMismatch)
			}
			var message IncomingMessage
			if err := adapter.DecodeJSON(event.Data, &message); err != nil {
				return err
			}
			normalized, err := c.Normalize(message, selfID)
			if err != nil {
				return err
			}
			if err := onEvent(normalized); err != nil {
				return err
			}
		case "bot_offline":
			return adapter.Failure(adapter.LoggedOut)
		default:
			// Milky compatibility policy: unknown event subtypes are ignored.
		}
	}
}

func classify(err error) string {
	var failure *adapter.Error
	if errors.As(err, &failure) {
		return string(failure.Kind)
	}
	return "disconnected"
}

func reportGap(onGap func(Gap), reason string) {
	if onGap != nil {
		onGap(Gap{At: time.Now().Unix(), Reason: reason})
	}
}

func wait(ctx context.Context) bool {
	timer := time.NewTimer(reconnectDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
