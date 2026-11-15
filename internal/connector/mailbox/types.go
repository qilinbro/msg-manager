// Package mailbox connects to an operator-configured IMAP mailbox over
// authenticated implicit TLS. It exposes no sending or mailbox mutation API.
package mailbox

import (
	"context"
	"errors"
	"time"
)

const (
	MaxRawMessageBytes = 512 << 10
	MaxBodyBytes       = 16000
)

type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	Folder   string
	Limit    int
}

func (Config) String() string { return "mailbox configuration (redacted)" }

type Info struct {
	AccountID   string `json:"account_id"`
	Folder      string `json:"folder"`
	UIDNext     uint32 `json:"uid_next"`
	UIDValidity uint32 `json:"uid_validity"`
	Messages    uint32 `json:"messages"`
	ReadOnly    bool   `json:"read_only"`
}

type Mail struct {
	ExternalID   string    `json:"external_id"`
	FromName     string    `json:"from_name"`
	FromAddress  string    `json:"from_address"`
	To           []string  `json:"to"`
	Subject      string    `json:"subject"`
	Body         string    `json:"body"`
	OccurredAt   string    `json:"occurred_at"`
	Seen         bool      `json:"seen"`
	InternalDate time.Time `json:"-"`
	UID          uint32    `json:"uid"`
}

type Batch struct {
	Info      Info   `json:"info"`
	Mails     []Mail `json:"mails"`
	Skipped   int    `json:"skipped"`
	Truncated bool   `json:"truncated"`
}

type Kind string

const (
	NotConfigured  Kind = "not_configured"
	InvalidConfig  Kind = "invalid_config"
	AddressDenied  Kind = "address_denied"
	Unreachable    Kind = "unreachable"
	TLSFailed      Kind = "tls_failed"
	AuthFailed     Kind = "auth_failed"
	MailboxChanged Kind = "mailbox_changed"
	MailboxFailed  Kind = "mailbox_failed"
	ProtocolError  Kind = "protocol_error"
)

type Error struct{ Kind Kind }

func (e *Error) Error() string { return "mailbox operation failed: " + string(e.Kind) }
func fail(kind Kind) error     { return &Error{Kind: kind} }

var (
	errUnsupported = errors.New("mail message is unsupported")
	errLimit       = errors.New("mail message exceeds a configured limit")
	errWireLimit   = errors.New("mail protocol response exceeds a configured limit")
)

type Reader interface {
	Probe(context.Context) (Info, error)
	Fetch(context.Context) (Batch, error)
}
