// Package adapter holds the shared local-transport boundary used by QQ
// protocol adapters. It deliberately contains no product decisions: callers
// still enforce workspace/contact scoping and keep the sender disabled.
package adapter

// Kind is a stable, non-sensitive failure classification. Provider bodies,
// URLs, credentials and account identifiers are never wrapped or returned.
type Kind string

const (
	NotConfigured    Kind = "not_configured"
	InvalidConfig    Kind = "invalid_config"
	Unreachable      Kind = "unreachable"
	AuthFailed       Kind = "auth_failed"
	IdentityMismatch Kind = "identity_mismatch"
	LoggedOut        Kind = "logged_out"
	VersionMismatch  Kind = "version_mismatch"
	ProtocolError    Kind = "protocol_error"
	Unsupported      Kind = "unsupported"
)

type Error struct{ Kind Kind }

func (e *Error) Error() string {
	switch e.Kind {
	case NotConfigured:
		return "protocol endpoint is not configured"
	case InvalidConfig:
		return "protocol endpoint configuration is invalid"
	case Unreachable:
		return "protocol endpoint could not be reached within the allowed time"
	case AuthFailed:
		return "protocol endpoint authentication failed"
	case IdentityMismatch:
		return "protocol endpoint account does not match the expected account"
	case LoggedOut:
		return "protocol endpoint account is not logged in"
	case VersionMismatch:
		return "protocol endpoint version is not supported"
	case Unsupported:
		return "protocol event is not supported by this product scope"
	default:
		return "protocol endpoint response did not match the supported protocol"
	}
}

func Failure(kind Kind) error { return &Error{Kind: kind} }
