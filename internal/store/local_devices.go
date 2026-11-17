package store

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"strconv"
	"strings"
	"time"
)

// LocalDevice is a public-key grant, not a persisted session or credential.
// IssuerHash is a caller-generated, domain-separated SHA-256 fingerprint; the
// original issuer token and the device's private key never enter this store.
type LocalDevice struct {
	ID             string
	PublicKey      []byte
	ServiceOrigin  string
	FrontendOrigin string
	IssuerHash     string
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

const MaxLocalDevices = 32

const (
	localDeviceLifetimeSeconds = int64(90 * 24 * 60 * 60)
	localDeviceMaxUnixSeconds  = int64(253402300799) // 9999-12-31T23:59:59Z
)

func validLocalDeviceID(id string) bool {
	if len(id) != 22 {
		return false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(id)
	return err == nil && len(raw) == 16 && base64.RawURLEncoding.EncodeToString(raw) == id
}

func validLocalDeviceIssuer(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	for _, ch := range hash {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}

func validLocalDeviceServiceOrigin(origin string) bool {
	const prefix = "http://127.0.0.1:"
	if !strings.HasPrefix(origin, prefix) || len(origin) < len(prefix)+1 || len(origin) > len(prefix)+5 {
		return false
	}
	rawPort := origin[len(prefix):]
	port, err := strconv.Atoi(rawPort)
	return err == nil && port >= 1 && port <= 65535 && strconv.Itoa(port) == rawPort
}

func validLocalDeviceFrontendOrigin(origin string) bool {
	switch origin {
	case "http://127.0.0.1:1420", "http://localhost:1420", "tauri://localhost", "http://tauri.localhost", "https://tauri.localhost":
		return true
	default:
		return false
	}
}

func validLocalDeviceKey(der []byte) bool {
	// The standard P-256 SPKI encoding is exactly 91 bytes. Reject private-key
	// encodings, arbitrary payloads and oversized values before ASN.1 parsing.
	if len(der) != 91 {
		return false
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return false
	}
	key, ok := parsed.(*ecdsa.PublicKey)
	if !ok || key.Curve != elliptic.P256() || key.X == nil || key.Y == nil || !key.Curve.IsOnCurve(key.X, key.Y) {
		return false
	}
	canonical, err := x509.MarshalPKIXPublicKey(key)
	return err == nil && bytes.Equal(canonical, der)
}

func validLocalDeviceTime(value time.Time) bool {
	return !value.IsZero() && value.Year() >= 1970 && value.Year() <= 9999 && value.Unix() > 0 && value.Unix() <= localDeviceMaxUnixSeconds
}

func validLocalDevice(device LocalDevice) bool {
	if !validLocalDeviceID(device.ID) || !validLocalDeviceKey(device.PublicKey) || !validLocalDeviceServiceOrigin(device.ServiceOrigin) || !validLocalDeviceFrontendOrigin(device.FrontendOrigin) || !validLocalDeviceIssuer(device.IssuerHash) {
		return false
	}
	if !validLocalDeviceTime(device.CreatedAt) || !validLocalDeviceTime(device.ExpiresAt) || device.CreatedAt.Nanosecond() != 0 || device.ExpiresAt.Nanosecond() != 0 {
		return false
	}
	lifetime := device.ExpiresAt.Unix() - device.CreatedAt.Unix()
	return lifetime > 0 && lifetime <= localDeviceLifetimeSeconds
}

// CreateLocalDevice atomically removes expired grants and enforces the total
// per-workspace device cap. Open configures BEGIN IMMEDIATE, so separate Store
// handles cannot both reserve the same final slot. An existing ID is never
// overwritten, even when it has the same public key and origins.
func (s *Store) CreateLocalDevice(ctx context.Context, device LocalDevice) error {
	if len(device.PublicKey) != 91 {
		return ErrInvalidInput
	}
	device.PublicKey = bytes.Clone(device.PublicKey)
	if !validLocalDevice(device) {
		return ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wechatMonitorDBError(ctx)
	}
	defer tx.Rollback()
	// Read the clock after acquiring both locks. A grant that expired while
	// waiting must not be inserted, and input timestamps cannot advance purge.
	now := time.Now().UTC().Unix()
	if device.CreatedAt.Unix() > now || device.ExpiresAt.Unix() <= now {
		return ErrInvalidInput
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM local_devices WHERE workspace_id=? AND id=?)`, workspaceID, device.ID).Scan(&exists); err != nil {
		return wechatMonitorDBError(ctx)
	}
	if exists {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM local_devices WHERE workspace_id=? AND expires_at<=?`, workspaceID, now); err != nil {
		return wechatMonitorDBError(ctx)
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM local_devices WHERE workspace_id=?`, workspaceID).Scan(&count); err != nil {
		return wechatMonitorDBError(ctx)
	}
	if count >= MaxLocalDevices {
		return ErrConflict
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO local_devices(workspace_id,id,public_key,service_origin,frontend_origin,issuer_hash,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(workspace_id,id) DO NOTHING`, workspaceID, device.ID, device.PublicKey, device.ServiceOrigin, device.FrontendOrigin, device.IssuerHash, device.CreatedAt.Unix(), device.ExpiresAt.Unix())
	if err != nil {
		return wechatMonitorDBError(ctx)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return wechatMonitorDBError(ctx)
	}
	if inserted != 1 {
		return ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return wechatMonitorDBError(ctx)
	}
	return nil
}

// LoadLocalDevice validates persisted bytes as strictly as a new grant. A bad
// row fails closed with no partial result; a missing or expired grant is absent.
func (s *Store) LoadLocalDevice(ctx context.Context, id string) (LocalDevice, error) {
	if !validLocalDeviceID(id) {
		return LocalDevice{}, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return LocalDevice{}, err
	}
	defer s.mu.Unlock()
	var publicKey, serviceOrigin, frontendOrigin, issuerHash, createdAt, expiresAt any
	err := s.db.QueryRowContext(ctx, `SELECT public_key,service_origin,frontend_origin,issuer_hash,created_at,expires_at FROM local_devices WHERE workspace_id=? AND id=?`, workspaceID, id).Scan(&publicKey, &serviceOrigin, &frontendOrigin, &issuerHash, &createdAt, &expiresAt)
	if err == sql.ErrNoRows {
		return LocalDevice{}, ErrNotFound
	}
	if err != nil {
		return LocalDevice{}, wechatMonitorDBError(ctx)
	}
	keyBytes, keyOK := publicKey.([]byte)
	service, serviceOK := serviceOrigin.(string)
	frontend, frontendOK := frontendOrigin.(string)
	issuer, issuerOK := issuerHash.(string)
	createdSeconds, createdOK := createdAt.(int64)
	expiresSeconds, expiresOK := expiresAt.(int64)
	if !keyOK || !serviceOK || !frontendOK || !issuerOK || !createdOK || !expiresOK || createdSeconds <= 0 || createdSeconds > localDeviceMaxUnixSeconds || expiresSeconds <= 0 || expiresSeconds > localDeviceMaxUnixSeconds {
		return LocalDevice{}, ErrUnavailable
	}
	device := LocalDevice{ID: id, PublicKey: keyBytes, ServiceOrigin: service, FrontendOrigin: frontend, IssuerHash: issuer, CreatedAt: time.Unix(createdSeconds, 0).UTC(), ExpiresAt: time.Unix(expiresSeconds, 0).UTC()}
	now := time.Now().UTC().Unix()
	if !validLocalDevice(device) || createdSeconds > now {
		return LocalDevice{}, ErrUnavailable
	}
	if expiresSeconds <= now {
		return LocalDevice{}, ErrNotFound
	}
	return device, nil
}

// RevokeLocalDevice permanently deletes this workspace's grant. Repeating a
// revocation, including for an unknown canonical ID, is a successful no-op.
func (s *Store) RevokeLocalDevice(ctx context.Context, id string) error {
	if !validLocalDeviceID(id) {
		return ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM local_devices WHERE workspace_id=? AND id=?`, workspaceID, id); err != nil {
		return wechatMonitorDBError(ctx)
	}
	return nil
}

// PruneLocalDevices deletes, rather than merely ignores, grants issued by an
// old token or service address. Changing A to B and back to A cannot revive an
// old confirmation. The caller supplies the current issuer's domain-separated
// hash, never its token; fractional now values are safely compared as Unix seconds.
func (s *Store) PruneLocalDevices(ctx context.Context, issuerHash, serviceOrigin string, now time.Time) error {
	if !validLocalDeviceIssuer(issuerHash) || !validLocalDeviceServiceOrigin(serviceOrigin) || !validLocalDeviceTime(now) {
		return ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM local_devices WHERE workspace_id=? AND (expires_at<=? OR issuer_hash IS NOT ? OR service_origin IS NOT ?)`, workspaceID, now.Unix(), issuerHash, serviceOrigin); err != nil {
		return wechatMonitorDBError(ctx)
	}
	return nil
}
