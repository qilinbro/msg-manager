// Package localcredentials reads only the local service's API credential. It
// never provisions credentials or opens database/connector key material.
package localcredentials

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	FileEnvironment = "SOCIALD_CREDENTIAL_FILE"
	MaxFileBytes    = 32 << 10
	purpose         = "msg-manager/service-credential/v1"
)

// Errors deliberately contain neither paths, ciphertext, tokens nor native
// error messages, which may contain user-controlled input.
var (
	ErrPath        = errors.New("SOCIALD_CREDENTIAL_FILE requires an absolute, non-linked file on a local fixed drive")
	ErrMissing     = errors.New("SOCIALD_CREDENTIAL_FILE is missing; initialize service credentials with the Windows inbox launcher")
	ErrUnsafe      = errors.New("SOCIALD_CREDENTIAL_FILE requires current-user ownership and a private protected DACL; no permissions were changed")
	ErrRead        = errors.New("SOCIALD_CREDENTIAL_FILE could not be read securely; no credentials were changed")
	ErrDecrypt     = errors.New("SOCIALD_CREDENTIAL_FILE could not be decrypted by this Windows account; restore the original account or explicitly reauthorize")
	ErrRecord      = errors.New("SOCIALD_CREDENTIAL_FILE contains an invalid or unsupported service credential; no credentials were changed")
	ErrServiceURL  = errors.New("SOCIALD_CREDENTIAL_FILE is bound to a different service URL; explicitly reauthorize rather than forwarding its token")
	ErrUnsupported = errors.New("SOCIALD_CREDENTIAL_FILE protected storage requires Windows; explicitly set SOCIALD_API_TOKEN on this platform")
)

// LoadToken is a read-only operation. Callers must first validate and normalize
// serviceURL, and must not call this function when a token environment variable
// is explicitly present (even if that environment value is invalid).
func LoadToken(serviceURL string) (string, error) {
	path, err := resolvePath(os.LookupEnv, defaultPath)
	if err != nil {
		return "", err
	}
	return loadToken(path, serviceURL)
}

func resolvePath(lookup func(string) (string, bool), fallback func() (string, error)) (string, error) {
	if path, present := lookup(FileEnvironment); present {
		if path == "" {
			return "", ErrPath
		}
		return path, nil
	}
	return fallback()
}

// decodeRecord rejects unknown/duplicate/missing fields and trailing JSON, not
// just malformed JSON. The entire record is inside the DPAPI integrity envelope.
func decodeRecord(plain []byte, serviceURL string) (string, error) {
	if len(plain) == 0 || len(plain) > MaxFileBytes || !utf8.Valid(plain) {
		return "", ErrRecord
	}
	decoder := json.NewDecoder(bytes.NewReader(plain))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return "", ErrRecord
	}
	var version int
	var boundURL, token string
	seen := make(map[string]bool, 3)
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return "", ErrRecord
		}
		seen[name] = true
		switch name {
		case "version":
			err = decoder.Decode(&version)
		case "service_url":
			err = decoder.Decode(&boundURL)
		case "api_token":
			err = decoder.Decode(&token)
		default:
			return "", ErrRecord
		}
		if err != nil {
			return "", ErrRecord
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') || len(seen) != 3 || version != 1 || !validToken(token) {
		return "", ErrRecord
	}
	if _, err := decoder.Token(); err != io.EOF {
		return "", ErrRecord
	}
	// The producer writes the canonical literal-loopback URL, not arbitrary
	// URLs that could move a bearer token to a different local listener.
	if !canonicalURL(boundURL) {
		return "", ErrRecord
	}
	if boundURL != serviceURL {
		return "", ErrServiceURL
	}
	return token, nil
}

func validToken(token string) bool {
	if len(token) < 32 || len(token) > 4096 {
		return false
	}
	for _, value := range []byte(token) {
		if value < 33 || value > 126 {
			return false
		}
	}
	return true
}

func canonicalURL(value string) bool {
	const prefix = "http://127.0.0.1:"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	port := strings.TrimPrefix(value, prefix)
	number, err := strconv.Atoi(port)
	return err == nil && number >= 1 && number <= 65535 && strconv.Itoa(number) == port
}
