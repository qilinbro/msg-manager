// Package onebot adapts a user-configured local OneBot v11 service. It neither
// starts a QQ provider nor implements a sending or background receiving loop.
package onebot

type Kind string

const (
	NotConfigured    Kind = "not_configured"
	InvalidConfig    Kind = "invalid_config"
	Unreachable      Kind = "unreachable"
	AuthFailed       Kind = "auth_failed"
	IdentityMismatch Kind = "identity_mismatch"
	LoggedOut        Kind = "logged_out"
	ProtocolError    Kind = "protocol_error"
)

// Error contains only a stable classification. It deliberately does not wrap
// transport errors, provider bodies, URLs, credentials or account identifiers.
type Error struct {
	Kind Kind
}

func (e *Error) Error() string {
	switch e.Kind {
	case NotConfigured:
		return "OneBot service is not configured"
	case InvalidConfig:
		return "OneBot configuration is invalid"
	case Unreachable:
		return "OneBot service could not be reached within the allowed time"
	case AuthFailed:
		return "OneBot authentication failed"
	case IdentityMismatch:
		return "OneBot account does not match the expected account"
	case LoggedOut:
		return "OneBot account is not online and healthy"
	default:
		return "OneBot response did not match the supported protocol"
	}
}

func failure(kind Kind) error { return &Error{Kind: kind} }
