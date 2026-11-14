package wechatdb

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/sha512"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

const fixtureKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
const fixtureOwner = "wxid_synthetic_owner"
const fixturePeer = "wxid_synthetic_peer"

type fixture struct {
	c      Config
	t      *testing.T
	writes []*sql.DB
}

func fixtureDB(t *testing.T, path string, saltByte byte) *sql.DB {
	t.Helper()
	var salt [16]byte
	for i := range salt {
		salt[i] = saltByte
	}
	password, _ := hex.DecodeString(fixtureKey)
	key, err := pbkdf2.Key(sha512.New, string(password), salt[:], 256000, 32)
	if err != nil {
		t.Fatal("synthetic fixture key derivation failed")
	}
	uriPath := filepath.ToSlash(path)
	if runtime.GOOS == "windows" {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	u.RawQuery = url.Values{"_pragma_key": {"x'" + hex.EncodeToString(key) + hex.EncodeToString(salt[:]) + "'"}, "_pragma_cipher_page_size": {"4096"}}.Encode()
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		t.Fatal("synthetic SQLCipher fixture open failed")
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA temp_store=MEMORY; PRAGMA cipher_memory_security=ON;"); err != nil {
		t.Fatal("synthetic fixture setup failed")
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func mustExec(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.Exec(statement, args...); err != nil {
		t.Fatal("synthetic fixture statement failed:", err)
	}
}

func newFixture(t *testing.T, shards int) *fixture {
	t.Helper()
	dir := filepath.Join(t.TempDir(), fixtureOwner+"_a1B2", "db_storage")
	for _, sub := range []string{"session", "contact", "message"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0700); err != nil {
			t.Fatal(err)
		}
	}
	f := &fixture{c: Config{Directory: dir, AccountID: fixtureOwner, Key: fixtureKey}, t: t}
	sessions := fixtureDB(t, filepath.Join(dir, "session", "session.db"), 1)
	mustExec(t, sessions, "CREATE TABLE SessionTable(username TEXT,sort_timestamp INTEGER)")
	for _, id := range []string{fixturePeer, fixtureOwner, "room@chatroom", "filehelper", "gh_synthetic"} {
		mustExec(t, sessions, "INSERT INTO SessionTable VALUES (?,?)", id, 1700000000)
	}
	_ = sessions.Close()
	contacts := fixtureDB(t, filepath.Join(dir, "contact", "contact.db"), 2)
	mustExec(t, contacts, "CREATE TABLE contact(username TEXT,remark TEXT,nick_name TEXT,alias TEXT)")
	mustExec(t, contacts, "INSERT INTO contact VALUES (?,?,?,?)", fixturePeer, "合成联系人", "昵称", "alias")
	_ = contacts.Close()
	for i := 0; i < shards; i++ {
		db := fixtureDB(t, filepath.Join(dir, "message", fmt.Sprintf("message_%d.db", i)), byte(10+i))
		mustExec(t, db, "CREATE TABLE Name2Id(user_name TEXT)")
		for _, id := range []string{fixtureOwner, fixturePeer, "wxid_other_peer"} {
			mustExec(t, db, "INSERT INTO Name2Id(user_name) VALUES (?)", id)
		}
		table := fmt.Sprintf("Msg_%x", md5.Sum([]byte(fixturePeer)))
		mustExec(t, db, `CREATE TABLE "`+table+`"(local_id INTEGER PRIMARY KEY,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,real_sender_id INTEGER,create_time INTEGER,message_content,compress_content)`)
		f.writes = append(f.writes, db)
	}
	return f
}

func (f *fixture) add(shard, localID, kind, sender int, timestamp int64, content, compressed any) {
	f.t.Helper()
	table := fmt.Sprintf("Msg_%x", md5.Sum([]byte(fixturePeer)))
	mustExec(f.t, f.writes[shard], `INSERT INTO "`+table+`" VALUES (?,?,?,?,?,?,?,?)`, localID, 9000+localID+shard*1000, kind, timestamp*1000, sender, timestamp, content, compressed)
}

func (f *fixture) close() {
	for _, db := range f.writes {
		_ = db.Close()
	}
}

func (f *fixture) reader() *Reader {
	f.t.Helper()
	r, err := NewReader(f.c)
	if err != nil {
		f.t.Fatal("reader construction failed:", err)
	}
	return r
}

func TestEncryptedReadPrivateTextMultipleShardsAndIsolation(t *testing.T) {
	f := newFixture(t, 2)
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	packed := encoder.EncodeAll([]byte("合成压缩回复"), nil)
	encoder.Close()
	f.add(0, 1, 1, 2, 1700000000, "合成来信", nil)
	f.add(1, 1, 1, 1, 1700000000, "ignored fallback", packed)
	f.add(1, 2, 3, 2, 1700000001, "media sentinel", nil)
	f.close()
	r := f.reader()
	info, err := r.Probe(context.Background())
	if err != nil || info.AccountID != fixtureOwner || !info.ReadOnly || info.SessionCount != 1 {
		t.Fatalf("probe mismatch: %+v, %v", info, err)
	}
	sessions, err := r.Sessions(context.Background())
	if err != nil || !reflect.DeepEqual(sessions, []Session{{PeerID: fixturePeer, Name: "合成联系人"}}) {
		t.Fatalf("private sessions mismatch: %+v, %v", sessions, err)
	}
	batch, err := r.Read(context.Background(), fixturePeer)
	if err != nil {
		t.Fatal("read failed:", err)
	}
	if len(batch.Messages) != 2 || batch.Skipped != 1 || batch.Limited || batch.PeerName != "合成联系人" {
		t.Fatalf("batch scope/count mismatch: %+v", batch)
	}
	if batch.Messages[0].Body != "合成来信" || batch.Messages[0].Direction != "incoming" || batch.Messages[1].Body != "合成压缩回复" || batch.Messages[1].Direction != "outgoing" {
		t.Fatalf("direction/text mismatch: %+v", batch.Messages)
	}
	if batch.Messages[0].ExternalID == batch.Messages[1].ExternalID || batch.Messages[0].OccurredAt != "2023-11-14T22:13:20Z" {
		t.Fatal("same-time cross-shard messages lost identity")
	}
	again, err := r.Read(context.Background(), fixturePeer)
	if err != nil || !reflect.DeepEqual(batch, again) {
		t.Fatal("repeat read was not stable")
	}
	if _, err := r.Read(context.Background(), "wxid_unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatal("non-session target accepted")
	}
	for _, peer := range []string{"room@chatroom", "filehelper", "gh_synthetic", fixtureOwner, `x';SELECT`} {
		if _, err := r.Read(context.Background(), peer); !errors.Is(err, ErrInvalidConfig) {
			t.Fatal("unsupported target accepted")
		}
	}
}

func TestReadUsesCommittedWALWithoutChangingDatabaseOrWAL(t *testing.T) {
	f := newFixture(t, 1)
	db := f.writes[0]
	mustExec(t, db, "PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0;")
	f.add(0, 10, 1, 2, 1700000001, "合成WAL独有来信", nil)
	path := filepath.Join(f.c.Directory, "message", "message_0.db")
	before := fileHashes(t, path, path+"-wal")
	for _, name := range []string{path, path + "-wal"} {
		b, err := os.ReadFile(name)
		if err != nil || bytes.Contains(b, []byte("合成WAL独有来信")) || bytes.HasPrefix(b, []byte("SQLite format 3")) {
			t.Fatal("fixture is not genuinely encrypted")
		}
	}
	r := f.reader()
	batch, err := r.Read(context.Background(), fixturePeer)
	if err != nil || len(batch.Messages) != 1 || batch.Messages[0].Body != "合成WAL独有来信" {
		t.Fatalf("committed WAL message was not read: %+v, %v", batch, err)
	}
	if !reflect.DeepEqual(before, fileHashes(t, path, path+"-wal")) {
		t.Fatal("readonly reader modified source database or WAL")
	}
	readDB, tx, err := r.open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB(readDB, tx)
	if _, err := tx.Exec("CREATE TABLE must_not_exist(value TEXT)"); err == nil {
		t.Fatal("readonly source accepted a schema write")
	}
	if _, err := tx.Exec("DELETE FROM Name2Id"); err == nil {
		t.Fatal("readonly source accepted a data write")
	}
}

func fileHashes(t *testing.T, paths ...string) [][32]byte {
	t.Helper()
	result := make([][32]byte, 0, len(paths))
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("could not hash synthetic database")
		}
		result = append(result, sha256.Sum256(raw))
	}
	return result
}

func TestLimitLatestWindowAndStableTies(t *testing.T) {
	f := newFixture(t, 2)
	for i := 1; i <= 4; i++ {
		f.add(0, i, 1, 2, 1700000000, fmt.Sprint("older-", i), nil)
		f.add(1, i, 1, 1, 1700000000, fmt.Sprint("newer-", i), nil)
	}
	f.close()
	f.c.Limit = 3
	batch, err := f.reader().Read(context.Background(), fixturePeer)
	if err != nil || !batch.Limited || len(batch.Messages) != 3 {
		t.Fatalf("window mismatch: %+v %v", batch, err)
	}
	for i, msg := range batch.Messages {
		if msg.Body != fmt.Sprint("newer-", i+2) {
			t.Fatal("tie order did not retain latest three")
		}
	}
}

func TestWrongKeyUnknownOwnerAndSchemasFailClosed(t *testing.T) {
	f := newFixture(t, 1)
	f.close()
	f.c.Key = strings.Repeat("ff", 32)
	_, err := f.reader().Probe(context.Background())
	if !errors.Is(err, ErrInvalidKey) || strings.Contains(err.Error(), f.c.Key) || strings.Contains(err.Error(), f.c.Directory) {
		t.Fatal("incorrect key not rejected with sanitized error")
	}
	f.c.Key = fixtureKey
	db := fixtureDB(t, filepath.Join(f.c.Directory, "message", "message_0.db"), 10)
	mustExec(t, db, "UPDATE Name2Id SET user_name='wxid_someone_else' WHERE user_name=?", fixtureOwner)
	_ = db.Close()
	if _, err := f.reader().Probe(context.Background()); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatal("account identity was inferred without Name2Id evidence")
	}
}

func TestMissingAndPlaintextSourceAreNotCreatedOrAccepted(t *testing.T) {
	f := newFixture(t, 1)
	f.close()
	path := filepath.Join(f.c.Directory, "contact", "contact.db")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reader().Probe(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing source accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("missing source was created")
	}
	plain, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, plain, "CREATE TABLE contact(username TEXT)")
	_ = plain.Close()
	if _, err := f.reader().Probe(context.Background()); !errors.Is(err, ErrInvalidKey) {
		t.Fatal("plaintext source accepted as encrypted")
	}
}

func TestConfigValidationAndFormattingRedactPathAndKey(t *testing.T) {
	f := newFixture(t, 1)
	f.close()
	for _, format := range []string{"%v", "%+v", "%#v"} {
		for _, object := range []any{f.c, f.reader()} {
			text := fmt.Sprintf(format, object)
			if strings.Contains(text, fixtureKey) || strings.Contains(text, f.c.Directory) || strings.Contains(text, fixtureOwner) {
				t.Fatal("configuration formatting disclosed private settings")
			}
		}
	}
	bad := []Config{f.c, f.c, f.c, f.c, f.c, f.c}
	bad[0].Directory = "relative/db_storage"
	bad[1].Directory = filepath.Dir(f.c.Directory)
	bad[2].AccountID = "wxid_wrong_account"
	bad[3].Key += `';secret`
	bad[4].Limit = 501
	bad[5].Directory = `\\host\share\wxid_synthetic_owner\db_storage`
	for _, c := range bad {
		if _, err := NewReader(c); !errors.Is(err, ErrInvalidConfig) {
			t.Fatal("invalid configuration accepted")
		}
	}
}

func TestChangedSourceAndAddedShardAreRejected(t *testing.T) {
	f := newFixture(t, 1)
	f.add(0, 1, 1, 2, 1700000000, "first", nil)
	f.close()
	r := f.reader()
	op, err := r.begin()
	if err != nil {
		t.Fatal(err)
	}
	db := fixtureDB(t, filepath.Join(f.c.Directory, "message", "message_0.db"), 10)
	mustExec(t, db, "CREATE TABLE changed(value TEXT)")
	_ = db.Close()
	if err := r.finish(op); !errors.Is(err, ErrChanged) {
		t.Fatal("cross-file change was not detected")
	}
	op, err = r.begin()
	if err != nil {
		t.Fatal(err)
	}
	added := filepath.Join(f.c.Directory, "message", "message_1.db")
	if err := os.WriteFile(added, []byte("synthetic placeholder"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.finish(op); !errors.Is(err, ErrChanged) {
		t.Fatal("new shard was not detected")
	}
}

func TestMalformedTextAndSenderAreRejected(t *testing.T) {
	if _, err := messageText([]byte{0x28, 0xb5, 0x2f, 0xfd, 0}, nil); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatal("invalid zstd accepted")
	}
	for _, raw := range [][]byte{{0xff}, []byte("a\x00b"), []byte(" "), bytes.Repeat([]byte("x"), maxTextBytes+1)} {
		if _, err := messageText(raw, nil); !errors.Is(err, ErrUnsupportedSchema) {
			t.Fatal("invalid text accepted")
		}
	}
	f := newFixture(t, 1)
	f.add(0, 1, 1, 3, 1700000000, "wrong peer", nil)
	f.close()
	if _, err := f.reader().Read(context.Background(), fixturePeer); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatal("message from unknown sender accepted")
	}
}

func TestLiteralPrivateTextAndBoundedName(t *testing.T) {
	literal := fixturePeer + ":\nkeep this literal prefix\n尾部  "
	body, err := messageText([]byte(literal), nil)
	if err != nil || body != literal {
		t.Fatal("private message text was rewritten")
	}
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	packed := encoder.EncodeAll([]byte(literal), nil)
	if body, err := messageText(nil, packed); err != nil || body != literal {
		t.Fatal("compressed private message text was rewritten")
	}
	bomb := encoder.EncodeAll(bytes.Repeat([]byte("x"), 1<<20), nil)
	encoder.Close()
	if _, err := messageText(bomb, nil); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatal("oversized compressed text was accepted")
	}
	if got := cleanName(" \n" + strings.Repeat("名", 200) + "\x00"); len([]rune(got)) > 128 || strings.ContainsAny(got, "\x00\n") {
		t.Fatal("contact name exceeded import bounds")
	}
	if raw, err := json.Marshal(Config{Key: fixtureKey, Directory: "private-path"}); err != nil || strings.Contains(string(raw), fixtureKey) || strings.Contains(string(raw), "private-path") {
		t.Fatal("JSON disclosed private configuration")
	}
}

func TestServerIdentityDedupConflictLargeIntegerAndLocalFallback(t *testing.T) {
	f := newFixture(t, 2)
	f.add(0, 1, 1, 2, 1700000000, "same server content", nil)
	f.add(1, 8, 1, 2, 1700000000, "same server content", nil)
	table := fmt.Sprintf("Msg_%x", md5.Sum([]byte(fixturePeer)))
	for _, db := range f.writes {
		mustExec(t, db, `UPDATE "`+table+`" SET server_id=9223372036854775806`)
	}
	f.add(0, 2, 1, 2, 1700000001, "local A", nil)
	f.add(1, 2, 1, 2, 1700000001, "local B", nil)
	for _, db := range f.writes {
		mustExec(t, db, `UPDATE "`+table+`" SET server_id=0 WHERE local_id=2`)
	}
	f.close()
	batch, err := f.reader().Read(context.Background(), fixturePeer)
	if err != nil || len(batch.Messages) != 3 {
		t.Fatalf("server dedup/local fallback mismatch: %d %v", len(batch.Messages), err)
	}
	if !strings.HasSuffix(batch.Messages[0].ExternalID, ":9223372036854775806") || !strings.HasPrefix(batch.Messages[1].ExternalID, "wechatdb:local:") || batch.Messages[1].ExternalID == batch.Messages[2].ExternalID {
		t.Fatal("server integer precision or fallback scope lost")
	}
	db := fixtureDB(t, filepath.Join(f.c.Directory, "message", "message_1.db"), 11)
	mustExec(t, db, `UPDATE "`+table+`" SET message_content='conflicting content' WHERE local_id=8`)
	_ = db.Close()
	if _, err := f.reader().Read(context.Background(), fixturePeer); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatal("conflicting duplicate server identity was silently accepted")
	}
}

func TestMissingRequiredColumnAndOversizeBody(t *testing.T) {
	f := newFixture(t, 1)
	f.add(0, 1, 1, 2, 1700000000, bytes.Repeat([]byte("x"), maxStoredBytes+1), nil)
	f.close()
	batch, err := f.reader().Read(context.Background(), fixturePeer)
	if err != nil || batch.Skipped != 1 || len(batch.Messages) != 0 {
		t.Fatal("oversize body was truncated or retained")
	}
	db := fixtureDB(t, filepath.Join(f.c.Directory, "message", "message_0.db"), 10)
	mustExec(t, db, "ALTER TABLE Name2Id RENAME COLUMN user_name TO unsupported_name")
	_ = db.Close()
	if _, err := f.reader().Probe(context.Background()); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatal("unrecognized identity schema was accepted")
	}
}

func TestSymlinkedAccountDirectoryIsRejected(t *testing.T) {
	f := newFixture(t, 1)
	f.close()
	parent := filepath.Join(t.TempDir(), fixtureOwner)
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "db_storage")
	if err := os.Symlink(f.c.Directory, alias); err != nil {
		t.Skip("symlink creation not available to this test user")
	}
	f.c.Directory = alias
	if _, err := NewReader(f.c); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("symlinked account directory was accepted")
	}
}

func TestOptionalMessageColumnsCanBeAbsent(t *testing.T) {
	f := newFixture(t, 1)
	table := fmt.Sprintf("Msg_%x", md5.Sum([]byte(fixturePeer)))
	db := f.writes[0]
	mustExec(t, db, `DROP TABLE "`+table+`"`)
	mustExec(t, db, `CREATE TABLE "`+table+`"(local_id INTEGER PRIMARY KEY,local_type INTEGER,real_sender_id INTEGER,create_time INTEGER,message_content)`)
	mustExec(t, db, `INSERT INTO "`+table+`" VALUES (?,?,?,?,?)`, 1, 1, 2, 1700000000, "minimal incoming")
	mustExec(t, db, `INSERT INTO "`+table+`" VALUES (?,?,?,?,?)`, 2, 1, 1, 1700000000, "minimal outgoing")
	f.close()
	batch, err := f.reader().Read(context.Background(), fixturePeer)
	if err != nil || len(batch.Messages) != 2 || batch.Limited || batch.Skipped != 0 {
		t.Fatalf("minimal valid message schema was rejected: %+v %v", batch, err)
	}
	if batch.Messages[0].Body != "minimal incoming" || batch.Messages[0].Direction != "incoming" || batch.Messages[1].Body != "minimal outgoing" || batch.Messages[1].Direction != "outgoing" {
		t.Fatal("optional-column fallback changed stable local order or direction")
	}
	if !strings.HasPrefix(batch.Messages[0].ExternalID, "wechatdb:local:") || batch.Messages[0].ExternalID == batch.Messages[1].ExternalID {
		t.Fatal("missing server ID did not fall back to separate local identities")
	}
}
