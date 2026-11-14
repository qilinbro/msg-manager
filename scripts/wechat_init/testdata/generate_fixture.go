//go:build ignore

// Regenerate a public, entirely synthetic SQLCipher first-page test fixture.
package main

import (
	"crypto/pbkdf2"
	"crypto/sha512"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"

	_ "github.com/mutecomm/go-sqlcipher/v4"
)

func main() {
	const passwordHex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	password, _ := hex.DecodeString(passwordHex)
	salt := []byte("synthetic-salt01")
	if len(salt) != 16 {
		panic("synthetic salt must be exactly 16 bytes")
	}
	key, err := pbkdf2.Key(sha512.New, string(password), salt, 256000, 32)
	if err != nil {
		panic("synthetic derivation failed")
	}
	f, err := os.CreateTemp("", "wechat-init-synthetic-*.db")
	if err != nil {
		panic("synthetic fixture unavailable")
	}
	path := f.Name()
	_ = f.Close()
	defer os.Remove(path)
	uriPath := filepath.ToSlash(path)
	if runtime.GOOS == "windows" {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	u.RawQuery = url.Values{"_pragma_key": {"x'" + hex.EncodeToString(key) + hex.EncodeToString(salt) + "'"}}.Encode()
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		panic("synthetic fixture unavailable")
	}
	if _, err = db.Exec("CREATE TABLE SessionTable(username TEXT); INSERT INTO SessionTable VALUES ('wxid_public_fixture');"); err != nil {
		panic("synthetic fixture creation failed")
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) < 4096 {
		panic("synthetic fixture read failed")
	}
	if _, err = db.Exec("INSERT INTO SessionTable VALUES ('wxid_public_fixture_updated');"); err != nil {
		panic("synthetic checkpoint update failed")
	}
	_ = db.Close()
	updated, err := os.ReadFile(path)
	if err != nil || len(updated) < 4096 {
		panic("synthetic updated fixture read failed")
	}
	out, err := json.MarshalIndent(map[string]string{"description": "Public synthetic SQLCipher 4.4.2 first pages before/after a commit; no real account data", "password_hex": passwordHex, "page_base64": base64.StdEncoding.EncodeToString(raw[:4096]), "checkpoint_page_base64": base64.StdEncoding.EncodeToString(updated[:4096])}, "", "  ")
	if err != nil {
		panic("synthetic serialization failed")
	}
	if err := os.WriteFile("scripts/wechat_init/testdata/synthetic_page.json", append(out, '\n'), 0600); err != nil {
		panic("synthetic fixture output failed")
	}
}
