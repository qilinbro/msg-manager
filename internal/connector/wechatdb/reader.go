// Package wechatdb reads an explicitly selected local WeChat 4.x account.
// It never modifies WeChat tables, extracts credentials, or drives the client.
package wechatdb

import (
	"context"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/sha512"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	sqlcipher "github.com/mutecomm/go-sqlcipher/v4"
)

var (
	ErrInvalidConfig     = errors.New("invalid local WeChat database configuration")
	ErrUnavailable       = errors.New("local WeChat database unavailable")
	ErrInvalidKey        = errors.New("local WeChat database key or encryption format is invalid")
	ErrUnsupportedSchema = errors.New("unsupported local WeChat database schema")
	ErrNotFound          = errors.New("local WeChat private conversation not found")
	ErrChanged           = errors.New("local WeChat database changed; read again after activity stops")
)

const (
	DefaultLimit = 100
	MaxLimit     = 500
	maxShards    = 64
	maxSessions  = 10000
	driverName   = "social-wechatdb-readonly"
)

var (
	usernamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{1,127}$`)
	shardPattern    = regexp.MustCompile(`^message_([0-9]{1,5})\.db$`)
	suffixPattern   = regexp.MustCompile(`^[A-Za-z0-9]{4}$`)
)

type Config struct {
	Directory string `json:"-"`
	AccountID string `json:"-"`
	Key       string `json:"-"`
	Limit     int    `json:"-"`
}

// Formatting configuration must not put a local account path or key in logs.
func (Config) String() string   { return "wechatdb.Config{[redacted]}" }
func (Config) GoString() string { return "wechatdb.Config{[redacted]}" }

type Info struct {
	AccountID    string `json:"account_id"`
	ReadOnly     bool   `json:"read_only"`
	SessionCount int    `json:"session_count"`
}

type Session struct {
	PeerID string `json:"peer_id"`
	Name   string `json:"name"`
}

type Message struct {
	ExternalID string `json:"external_id"`
	Direction  string `json:"direction"`
	Body       string `json:"body"`
	OccurredAt string `json:"occurred_at"`
}

type Batch struct {
	Info     Info      `json:"info"`
	PeerID   string    `json:"peer_id"`
	PeerName string    `json:"peer_name"`
	Messages []Message `json:"messages"`
	Skipped  int       `json:"skipped"`
	Limited  bool      `json:"limited"`
}

type Reader struct{ config Config }

func (*Reader) String() string   { return "wechatdb.Reader{[redacted]}" }
func (*Reader) GoString() string { return "wechatdb.Reader{[redacted]}" }

func init() {
	sql.Register(driverName, &sqlcipher.SQLiteDriver{ConnectHook: func(c *sqlcipher.SQLiteConn) error {
		for _, statement := range []string{
			"PRAGMA query_only=ON", "PRAGMA temp_store=MEMORY", "PRAGMA cipher_memory_security=ON",
			"PRAGMA busy_timeout=1000",
		} {
			if _, err := c.Exec(statement, nil); err != nil {
				return ErrUnavailable
			}
		}
		return nil
	}})
}

func privateID(id string) bool {
	if !usernamePattern.MatchString(id) || strings.HasPrefix(id, "gh_") {
		return false
	}
	switch strings.ToLower(id) {
	case "filehelper", "weixin", "fmessage", "medianote", "newsapp", "notification_messages", "floatbottle", "qqmail", "tmessage", "qmessage", "masssendapp", "weibo", "brandsessionholder", "helper_entry":
		return false
	}
	return true
}

func NewReader(c Config) (*Reader, error) {
	key, err := hex.DecodeString(c.Key)
	defer clear(key)
	if err != nil || len(c.Key) != 64 || len(key) != 32 || !privateID(c.AccountID) {
		return nil, ErrInvalidConfig
	}
	clear(key)
	if c.Limit == 0 {
		c.Limit = DefaultLimit
	}
	if c.Limit < 1 || c.Limit > MaxLimit || !filepath.IsAbs(c.Directory) || strings.ContainsAny(c.Directory, "\x00\r\n") || strings.HasPrefix(c.Directory, `\\`) || strings.HasPrefix(c.Directory, "//") || strings.HasPrefix(c.Directory, "file:") {
		return nil, ErrInvalidConfig
	}
	c.Directory = filepath.Clean(c.Directory)
	if !strings.EqualFold(filepath.Base(c.Directory), "db_storage") {
		return nil, ErrInvalidConfig
	}
	accountDir := filepath.Base(filepath.Dir(c.Directory))
	if accountDir != c.AccountID {
		suffix, ok := strings.CutPrefix(accountDir, c.AccountID+"_")
		if !ok || !suffixPattern.MatchString(suffix) {
			return nil, ErrInvalidConfig
		}
	}
	if !safePath(c.Directory) {
		return nil, ErrInvalidConfig
	}
	if info, err := os.Stat(c.Directory); err != nil || !info.IsDir() {
		return nil, ErrUnavailable
	}
	return &Reader{config: c}, nil
}

// operation records DB/WAL identity, length and modification time. A concurrent
// change fails the whole result instead of saving a mixed cross-file snapshot.
type sourceFile struct {
	path string
	info os.FileInfo
}

type shard struct {
	path   string
	number int
}

type operation struct {
	files  []sourceFile
	shards []shard
}

func fileState(path string) (os.FileInfo, error) {
	if !safePath(path) {
		return nil, ErrInvalidConfig
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrUnavailable
	}
	return info, nil
}

func (r *Reader) begin() (*operation, error) {
	if r == nil || !safePath(r.config.Directory) {
		return nil, ErrUnavailable
	}
	dir := filepath.Join(r.config.Directory, "message")
	if !safePath(dir) {
		return nil, ErrInvalidConfig
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, ErrUnavailable
	}
	op := &operation{}
	for _, entry := range entries {
		parts := shardPattern.FindStringSubmatch(entry.Name())
		if parts == nil {
			continue
		}
		n, _ := strconv.Atoi(parts[1])
		if len(op.shards) >= maxShards {
			return nil, ErrUnsupportedSchema
		}
		op.shards = append(op.shards, shard{path: filepath.Join(dir, entry.Name()), number: n})
	}
	if len(op.shards) == 0 {
		return nil, ErrUnsupportedSchema
	}
	sort.Slice(op.shards, func(i, j int) bool { return op.shards[i].number < op.shards[j].number })
	for i := 1; i < len(op.shards); i++ {
		if op.shards[i-1].number == op.shards[i].number {
			return nil, ErrUnsupportedSchema
		}
	}
	paths := []string{filepath.Join(r.config.Directory, "session", "session.db"), filepath.Join(r.config.Directory, "contact", "contact.db")}
	for _, s := range op.shards {
		paths = append(paths, s.path)
	}
	for _, path := range paths {
		for _, name := range []string{path, path + "-wal"} {
			info, err := fileState(name)
			if err != nil {
				return nil, err
			}
			if name == path && info == nil {
				return nil, ErrUnavailable
			}
			op.files = append(op.files, sourceFile{path: name, info: info})
		}
	}
	return op, nil
}

func (r *Reader) finish(op *operation) error {
	newOp, err := r.begin()
	if err != nil || len(op.files) != len(newOp.files) {
		return ErrChanged
	}
	for i, old := range op.files {
		current := newOp.files[i]
		if old.path != current.path || (old.info == nil) != (current.info == nil) {
			return ErrChanged
		}
		if old.info != nil && (!os.SameFile(old.info, current.info) || old.info.Size() != current.info.Size() || !old.info.ModTime().Equal(current.info.ModTime())) {
			return ErrChanged
		}
	}
	return nil
}

// open uses a per-file PBKDF2 key, not the user's hexadecimal password as a
// SQLCipher raw key. The explicit salt also avoids binary-string/NUL handling.
// SQLite itself reads committed WAL frames; no checkpoint or journal-mode
// mutation is requested. Its existing SHM coordination remains SQLite's own.
func (r *Reader) open(ctx context.Context, path string) (*sql.DB, *sql.Tx, error) {
	if !safePath(path) {
		return nil, nil, ErrInvalidConfig
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, ErrUnavailable
	}
	var salt [16]byte
	_, err = io.ReadFull(f, salt[:])
	_ = f.Close()
	if err != nil {
		return nil, nil, ErrInvalidKey
	}
	password, _ := hex.DecodeString(r.config.Key)
	key, err := pbkdf2.Key(sha512.New, string(password), salt[:], 256000, 32)
	clear(password)
	if err != nil {
		return nil, nil, ErrInvalidKey
	}
	keyWithSalt := hex.EncodeToString(key) + hex.EncodeToString(salt[:])
	clear(key)
	uriPath := filepath.ToSlash(path)
	if runtime.GOOS == "windows" {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := url.Values{"mode": {"ro"}, "cache": {"private"}, "_query_only": {"1"}, "_busy_timeout": {"1000"}, "_pragma_key": {"x'" + keyWithSalt + "'"}, "_pragma_cipher_page_size": {"4096"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open(driverName, u.String())
	if err != nil {
		return nil, nil, ErrUnavailable
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		_ = db.Close()
		return nil, nil, ErrInvalidKey
	}
	var count int
	if tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master").Scan(&count) != nil {
		_ = tx.Rollback()
		_ = db.Close()
		return nil, nil, ErrInvalidKey
	}
	return db, tx, nil
}

func closeDB(db *sql.DB, tx *sql.Tx) { _ = tx.Rollback(); _ = db.Close() }

func columns(ctx context.Context, tx *sql.Tx, table string, required ...string) (map[string]bool, error) {
	// All table names here are constants or a locally calculated MD5 identifier.
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info("`+table+`")`)
	if err != nil {
		return nil, ErrUnsupportedSchema
	}
	defer rows.Close()
	found := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var defaultValue any
		if rows.Scan(&cid, &name, &kind, &notnull, &defaultValue, &pk) != nil {
			return nil, ErrUnsupportedSchema
		}
		found[name] = true
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	for _, name := range required {
		if !found[name] {
			return nil, ErrUnsupportedSchema
		}
	}
	return found, nil
}

func (r *Reader) verifyOwner(ctx context.Context, tx *sql.Tx) error {
	if _, err := columns(ctx, tx, "Name2Id", "user_name"); err != nil {
		return err
	}
	var count int
	if tx.QueryRowContext(ctx, "SELECT count(*) FROM Name2Id WHERE user_name = ?", r.config.AccountID).Scan(&count) != nil {
		return ErrUnsupportedSchema
	}
	if count != 1 {
		return ErrUnsupportedSchema
	}
	return nil
}

func (r *Reader) sessions(ctx context.Context) ([]Session, error) {
	db, tx, err := r.open(ctx, filepath.Join(r.config.Directory, "session", "session.db"))
	if err != nil {
		return nil, err
	}
	defer closeDB(db, tx)
	if _, err := columns(ctx, tx, "SessionTable", "username"); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT CASE WHEN length(CAST(username AS BLOB)) <= 128 THEN username ELSE NULL END FROM SessionTable LIMIT ?", maxSessions+1)
	if err != nil {
		return nil, ErrUnsupportedSchema
	}
	defer rows.Close()
	seen := map[string]bool{}
	result := []Session{}
	for rows.Next() {
		if len(seen) >= maxSessions {
			return nil, ErrUnsupportedSchema
		}
		var id sql.NullString
		if rows.Scan(&id) != nil || !id.Valid {
			return nil, ErrUnsupportedSchema
		}
		if privateID(id.String) && id.String != r.config.AccountID && !seen[id.String] {
			result = append(result, Session{PeerID: id.String, Name: id.String})
		}
		seen[id.String] = true
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	sort.Slice(result, func(i, j int) bool { return result[i].PeerID < result[j].PeerID })
	return result, nil
}

func (r *Reader) names(ctx context.Context, sessions []Session) error {
	db, tx, err := r.open(ctx, filepath.Join(r.config.Directory, "contact", "contact.db"))
	if err != nil {
		return err
	}
	defer closeDB(db, tx)
	cols, err := columns(ctx, tx, "contact", "username")
	if err != nil {
		return err
	}
	parts := []string{"username"}
	for _, field := range []string{"remark", "nick_name", "alias"} {
		if cols[field] {
			parts = append(parts, "CASE WHEN length(\""+field+"\") <= 512 THEN \""+field+"\" ELSE '' END")
		} else {
			parts = append(parts, "''")
		}
	}
	query := "SELECT " + strings.Join(parts, ",") + " FROM contact WHERE username = ? LIMIT 2"
	for i := range sessions {
		rows, err := tx.QueryContext(ctx, query, sessions[i].PeerID)
		if err != nil {
			return ErrUnsupportedSchema
		}
		count := 0
		for rows.Next() {
			count++
			var id, remark, nick, alias sql.NullString
			if count > 1 || rows.Scan(&id, &remark, &nick, &alias) != nil {
				_ = rows.Close()
				return ErrUnsupportedSchema
			}
			for _, name := range []string{remark.String, nick.String, alias.String} {
				if name = cleanName(name); name != "" {
					sessions[i].Name = name
					break
				}
			}
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return ErrUnavailable
		}
	}
	return nil
}

func cleanName(s string) string {
	if !utf8.ValidString(s) {
		return ""
	}
	runes := make([]rune, 0, 128)
	for _, ch := range s {
		if unicode.IsControl(ch) {
			continue
		}
		runes = append(runes, ch)
		if len(runes) == 128 {
			break
		}
	}
	return strings.TrimSpace(string(runes))
}

func (r *Reader) metadata(ctx context.Context, op *operation) ([]Session, error) {
	sessions, err := r.sessions(ctx)
	if err != nil {
		return nil, err
	}
	if err := r.names(ctx, sessions); err != nil {
		return nil, err
	}
	for _, s := range op.shards {
		db, tx, err := r.open(ctx, s.path)
		if err != nil {
			return nil, err
		}
		err = r.verifyOwner(ctx, tx)
		closeDB(db, tx)
		if err != nil {
			return nil, err
		}
	}
	return sessions, nil
}

func (r *Reader) Probe(ctx context.Context) (Info, error) {
	sessions, err := r.Sessions(ctx)
	if err != nil {
		return Info{}, err
	}
	return Info{AccountID: r.config.AccountID, ReadOnly: true, SessionCount: len(sessions)}, nil
}

func (r *Reader) Sessions(ctx context.Context) ([]Session, error) {
	op, err := r.begin()
	if err != nil {
		return nil, err
	}
	sessions, err := r.metadata(ctx, op)
	if err != nil {
		return nil, err
	}
	if err := r.finish(op); err != nil {
		return nil, err
	}
	return sessions, nil
}

func (r *Reader) Read(ctx context.Context, peerID string) (Batch, error) {
	if r == nil || !privateID(peerID) || peerID == r.config.AccountID {
		return Batch{}, ErrInvalidConfig
	}
	op, err := r.begin()
	if err != nil {
		return Batch{}, err
	}
	sessions, err := r.metadata(ctx, op)
	if err != nil {
		return Batch{}, err
	}
	peerName := ""
	for _, session := range sessions {
		if session.PeerID == peerID {
			peerName = session.Name
			break
		}
	}
	if peerName == "" {
		return Batch{}, ErrNotFound
	}
	var raw []rawMessage
	identities := map[string][32]byte{}
	sourceLimited := false
	for _, s := range op.shards {
		messages, err := r.readShard(ctx, s, peerID)
		if err != nil {
			return Batch{}, err
		}
		if len(messages) > r.config.Limit {
			sourceLimited = true
		}
		for _, message := range messages {
			msg, err := r.normalize(message, peerID)
			if err != nil {
				return Batch{}, err
			}
			// Fingerprint before deduplicating: conflicting cross-shard server
			// identities fail the batch instead of choosing one silently.
			signature := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s\x00%s", message.kind, msg.Direction, msg.OccurredAt, msg.Body)))
			if previous, exists := identities[msg.ExternalID]; exists {
				if previous != signature {
					return Batch{}, ErrUnsupportedSchema
				}
				continue
			}
			identities[msg.ExternalID] = signature
			message.normalized = msg
			message.content, message.compressed = nil, nil
			raw = append(raw, message)
		}
		// Keep at most a window plus one sentinel across all shards. The extra
		// candidate preserves Limited without retaining unbounded message bodies.
		sort.Slice(raw, func(i, j int) bool { return newer(raw[i], raw[j]) })
		if len(raw) > r.config.Limit+1 {
			raw = raw[:r.config.Limit+1]
		}
	}
	sort.Slice(raw, func(i, j int) bool { return newer(raw[i], raw[j]) })
	batch := Batch{Info: Info{AccountID: r.config.AccountID, ReadOnly: true, SessionCount: len(sessions)}, PeerID: peerID, PeerName: peerName, Messages: []Message{}, Limited: sourceLimited}
	if len(raw) > r.config.Limit {
		batch.Limited = true
		raw = raw[:r.config.Limit]
	}
	for i := len(raw) - 1; i >= 0; i-- {
		msg := raw[i]
		if msg.kind != 1 {
			batch.Skipped++
			continue
		}
		batch.Messages = append(batch.Messages, msg.normalized)
	}
	if err := r.finish(op); err != nil {
		return Batch{}, err
	}
	return batch, nil
}

func (r *Reader) normalize(msg rawMessage, peerID string) (Message, error) {
	if msg.localID <= 0 || msg.timestamp <= 0 || msg.timestamp > 253402300799 {
		return Message{}, ErrUnsupportedSchema
	}
	id := fmt.Sprintf("wechatdb:local:%d:%x:%d", msg.shard, md5.Sum([]byte(peerID)), msg.localID)
	if msg.serverID != "" {
		id = fmt.Sprintf("wechatdb:server:%x:%s", md5.Sum([]byte(peerID)), msg.serverID)
	}
	m := Message{ExternalID: id, OccurredAt: time.Unix(msg.timestamp, 0).UTC().Format(time.RFC3339)}
	if msg.kind != 1 {
		return m, nil
	}
	if msg.sender != peerID && msg.sender != r.config.AccountID {
		return Message{}, ErrUnsupportedSchema
	}
	body, err := messageText(msg.content, msg.compressed)
	if err != nil {
		return Message{}, err
	}
	m.Direction, m.Body = "incoming", body
	if msg.sender == r.config.AccountID {
		m.Direction = "outgoing"
	}
	return m, nil
}
