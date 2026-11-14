package wechatdb

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"
)

const maxTextBytes = 16000
const maxStoredBytes = 65536

type rawMessage struct {
	localID, timestamp, sequence, kind int64
	shard                              int
	sender                             string
	serverID                           string
	content, compressed                []byte
	normalized                         Message
}

func newer(a, b rawMessage) bool {
	if a.timestamp != b.timestamp {
		return a.timestamp > b.timestamp
	}
	if a.sequence != b.sequence {
		return a.sequence > b.sequence
	}
	if a.shard != b.shard {
		return a.shard > b.shard
	}
	return a.localID > b.localID
}

func (r *Reader) readShard(ctx context.Context, s shard, peerID string) ([]rawMessage, error) {
	db, tx, err := r.open(ctx, s.path)
	if err != nil {
		return nil, err
	}
	defer closeDB(db, tx)
	if err := r.verifyOwner(ctx, tx); err != nil {
		return nil, err
	}
	table := fmt.Sprintf("Msg_%x", md5.Sum([]byte(peerID)))
	var exists int
	if tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&exists) != nil {
		return nil, ErrUnsupportedSchema
	}
	if exists == 0 {
		return nil, nil
	}
	cols, err := columns(ctx, tx, table, "local_id", "create_time", "local_type", "real_sender_id", "message_content")
	if err != nil {
		return nil, err
	}
	// A bare integer in ORDER BY is a result-column ordinal in SQLite. Keep
	// the missing-column fallback an expression so zero is not ordinal zero.
	seq := "CAST(0 AS INTEGER)"
	if cols["sort_seq"] {
		seq = "COALESCE(m.sort_seq,0)"
	}
	compressed := "NULL"
	if cols["compress_content"] {
		compressed = "m.compress_content"
	}
	serverID := "NULL"
	if cols["server_id"] {
		serverID = "m.server_id"
	}
	// Bound body allocation inside SQLite before it crosses the driver boundary.
	query := `SELECT m.local_id,m.create_time,` + seq + `,m.local_type,n.user_name,
CASE WHEN length(CAST(m.message_content AS BLOB))<=? THEN m.message_content ELSE NULL END,
CASE WHEN length(CAST(` + compressed + ` AS BLOB))<=? THEN ` + compressed + ` ELSE NULL END,` + serverID + `,
COALESCE(length(CAST(m.message_content AS BLOB)),0),COALESCE(length(CAST(` + compressed + ` AS BLOB)),0)
FROM "` + table + `" m LEFT JOIN Name2Id n ON n.rowid=m.real_sender_id
ORDER BY m.create_time DESC,` + seq + ` DESC,m.local_id DESC LIMIT ?`
	rows, err := tx.QueryContext(ctx, query, maxStoredBytes, maxStoredBytes, r.config.Limit+1)
	if err != nil {
		return nil, ErrUnsupportedSchema
	}
	defer rows.Close()
	out := []rawMessage{}
	for rows.Next() {
		var m rawMessage
		var sender sql.NullString
		var sid sql.NullInt64
		var contentLen, compressedLen int64
		if rows.Scan(&m.localID, &m.timestamp, &m.sequence, &m.kind, &sender, &m.content, &m.compressed, &sid, &contentLen, &compressedLen) != nil {
			return nil, ErrUnsupportedSchema
		}
		m.shard, m.sender = s.number, sender.String
		if sid.Valid && sid.Int64 != 0 {
			m.serverID = strconv.FormatInt(sid.Int64, 10)
		}
		// Oversize bodies are an explicitly skipped item, never a truncated message.
		if contentLen > maxStoredBytes || compressedLen > maxStoredBytes {
			m.kind = -1
		}
		out = append(out, m)
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return out, nil
}

func messageText(content, compressed []byte) (string, error) {
	data := content
	if len(compressed) > 0 {
		data = compressed
	}
	if bytes.HasPrefix(data, []byte{0x28, 0xb5, 0x2f, 0xfd}) {
		decoder, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(1<<20), zstd.WithDecoderMaxWindow(1<<20), zstd.WithDecodeAllCapLimit(true))
		if err != nil {
			return "", ErrUnsupportedSchema
		}
		defer decoder.Close()
		data, err = decoder.DecodeAll(data, make([]byte, 0, maxTextBytes+256))
		if err != nil {
			return "", ErrUnsupportedSchema
		}
	}
	if !utf8.Valid(data) {
		return "", ErrUnsupportedSchema
	}
	body := string(data)
	// Private chat text remains literal, including any username/colon prefix.
	if len(body) > maxTextBytes || strings.TrimSpace(body) == "" || strings.ContainsRune(body, 0) {
		return "", ErrUnsupportedSchema
	}
	return body, nil
}
