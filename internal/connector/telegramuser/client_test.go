package telegramuser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// A synthetic child process exercises the real bounded stdin/stdout path.
	// It never imports Python, decrypts a configuration or contacts Telegram.
	if len(os.Args) == 3 && os.Args[1] == "-I" && strings.HasPrefix(filepath.Base(os.Args[2]), "test-helper-") {
		switch filepath.Base(os.Args[2]) {
		case "test-helper-timeout":
			time.Sleep(10 * time.Second)
		case "test-helper-large":
			fmt.Print(strings.Repeat("x", maxOutput+1))
		case "test-helper-probe":
			var req command
			_ = json.NewDecoder(os.Stdin).Decode(&req)
			if req.Action != "probe" || os.Getenv("AI_API_KEY") != "" || os.Getenv("SOCIALD_API_TOKEN") != "" || os.Getenv("TELEGRAM_API_HASH") != "" || os.Getenv("PATH") != "" {
				os.Exit(1)
			}
			fmt.Print(`{"ok":true,"data":{"account_id":"123","name":"合成账号","read_only":true,"sending_enabled":false}}`)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func testClient(t *testing.T, response string) *Client {
	t.Helper()
	c, err := NewClient(Config{ProjectRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	c.run = func(context.Context, []byte) ([]byte, error) { return []byte(response), nil }
	return c
}

func TestReadIsBoundedAndStrictlyAccountScoped(t *testing.T) {
	body := `{"ok":true,"data":{"account_id":"123","name":"合成账号","read_only":true,"sending_enabled":false,"peer_id":"456","peer_name":"测试联系人","messages":[{"external_id":"telegram:user:456:1","body":"hello","direction":"incoming","occurred_at":"2026-01-01T00:00:00Z"}],"skipped":0,"limited":false}}`
	c := testClient(t, body)
	calls := 0
	c.run = func(_ context.Context, input []byte) ([]byte, error) {
		calls++
		if string(input) != `{"action":"read","peer_id":"456","limit":1,"expected_account_id":"123"}` {
			t.Fatal(string(input))
		}
		return []byte(body), nil
	}
	batch, err := c.Read(context.Background(), "123", "456", 1)
	if err != nil || len(batch.Messages) != 1 {
		t.Fatal(err)
	}
	for _, peer := range []string{"", "0", "-1", "0456", "456.0", "9223372036854775808", "456\n"} {
		if _, err := c.Read(context.Background(), "123", peer, 1); err == nil {
			t.Fatal("invalid peer accepted")
		}
	}
	for _, limit := range []int{0, 101, -1} {
		if _, err := c.Read(context.Background(), "123", "456", limit); err == nil {
			t.Fatal("unbounded read")
		}
	}
	if calls != 1 {
		t.Fatal("invalid input invoked helper")
	}
	for _, bad := range []string{
		strings.Replace(body, "telegram:user:456:1", "telegram:user:999:1", 1),
		strings.Replace(body, `"read_only":true`, `"read_only":false`, 1),
		strings.Replace(body, `"sending_enabled":false`, `"sending_enabled":true`, 1),
		strings.Replace(body, `"peer_id":"456"`, `"peer_id":"789"`, 1),
		strings.Replace(body, `"direction":"incoming"`, `"direction":"send"`, 1),
		strings.Replace(body, `"skipped":0`, `"skipped":1`, 1),
		strings.Replace(body, `"messages":[`, `"unexpected":"private", "messages":[`, 1),
	} {
		if _, err := testClient(t, bad).Read(context.Background(), "123", "456", 1); err == nil {
			t.Fatal("invalid read accepted", bad)
		}
	}
}

func TestEnvelopeFailureRedactionCancellationAndBounds(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"ok":true,"data":null}`, `{"ok":true,"data":{"account_id":"123","name":"n","read_only":true,"sending_enabled":false},"debug":"secret"}`, strings.Repeat("x", maxOutput+1), `{} {}`} {
		if _, err := testClient(t, body).Probe(context.Background()); err == nil {
			t.Fatal("malformed envelope accepted")
		}
	}
	c := testClient(t, `{"ok":false,"error":{"code":"FLOOD_WAIT","retry_after":60}}`)
	_, err := c.Probe(context.Background())
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != "flood_wait" || failure.RetryAfter != 60 {
		t.Fatal(err)
	}
	c = testClient(t, `{"ok":false,"error":{"code":"synthetic-secret-should-not-escape"}}`)
	_, err = c.Probe(context.Background())
	if err == nil || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatal("raw helper failure escaped")
	}
	c = testClient(t, "")
	c.run = func(ctx context.Context, _ []byte) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() }
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := c.Probe(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	c.run = func(context.Context, []byte) ([]byte, error) {
		t.Fatal("cancelled call launched helper")
		return nil, nil
	}
	if _, err := c.Probe(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestIncrementalRequestsAndCursorValidation(t *testing.T) {
	base := `{"ok":true,"data":{"account_id":"123","name":"synthetic owner","read_only":true,"sending_enabled":false,"peer_id":"456","peer_name":"synthetic friend","messages":[],"skipped":0,"limited":false,"cursor":"10"}}`
	c := testClient(t, base)
	calls := 0
	c.run = func(_ context.Context, input []byte) ([]byte, error) {
		calls++
		if string(input) != `{"action":"baseline","peer_id":"456","expected_account_id":"123"}` {
			t.Fatal(string(input))
		}
		return []byte(base), nil
	}
	if batch, err := c.Baseline(context.Background(), "123", "456"); err != nil || batch.Cursor != "10" {
		t.Fatal(batch, err)
	}
	scan := strings.Replace(base, `"messages":[]`, `"messages":[{"external_id":"telegram:user:456:11","body":"new","direction":"incoming","occurred_at":"2026-01-01T00:00:00Z"}]`, 1)
	scan = strings.Replace(scan, `"cursor":"10"`, `"cursor":"12"`, 1)
	scan = strings.Replace(scan, `"skipped":0`, `"skipped":1`, 1)
	c.run = func(_ context.Context, input []byte) ([]byte, error) {
		calls++
		if string(input) != `{"action":"scan","peer_id":"456","limit":100,"expected_account_id":"123","after_id":"10"}` {
			t.Fatal(string(input))
		}
		return []byte(scan), nil
	}
	if batch, err := c.Scan(context.Background(), "123", "456", "10"); err != nil || batch.Cursor != "12" || batch.Skipped != 1 {
		t.Fatal(batch, err)
	}
	for _, after := range []string{"", "-1", "01", "１２", "9223372036854775808"} {
		if _, err := c.Scan(context.Background(), "123", "456", after); err == nil {
			t.Fatal("unsafe cursor accepted", after)
		}
	}
	if calls != 2 {
		t.Fatal("invalid cursor invoked helper")
	}
	for _, bad := range []string{
		strings.Replace(scan, `"cursor":"12"`, `"cursor":"9"`, 1),
		strings.Replace(scan, `"cursor":"12"`, `"cursor":"10"`, 1),
		strings.Replace(scan, `"cursor":"12"`, `"cursor":"01"`, 1),
		strings.Replace(scan, "telegram:user:456:11", "telegram:user:456:10", 1),
		strings.Replace(scan, "telegram:user:456:11", "telegram:user:456:13", 1),
		strings.Replace(scan, `"account_id":"123"`, `"account_id":"789"`, 1),
		strings.Replace(base, `"cursor":"10"`, `"cursor":"12"`, 1),
	} {
		if _, err := testClient(t, bad).Scan(context.Background(), "123", "456", "10"); err == nil {
			t.Fatal("malformed scan accepted", bad)
		}
	}
	for _, bad := range []string{scan, strings.Replace(base, `"limited":false`, `"limited":true`, 1), strings.Replace(base, `"skipped":0`, `"skipped":1`, 1)} {
		if _, err := testClient(t, bad).Baseline(context.Background(), "123", "456"); err == nil {
			t.Fatal("history-carrying baseline accepted")
		}
	}
	if _, err := testClient(t, base).Scan(context.Background(), "123", "456", "10"); err != nil {
		t.Fatal("empty unchanged scan rejected", err)
	}
	zero := strings.Replace(base, `"cursor":"10"`, `"cursor":"0"`, 1)
	if _, err := testClient(t, zero).Scan(context.Background(), "123", "456", "0"); err != nil {
		t.Fatal("empty new dialog rejected", err)
	}
}

func TestWorkerProcessIsIsolatedBoundedAndCancellable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only helper execution")
	}
	t.Setenv("AI_API_KEY", "synthetic-secret")
	t.Setenv("SOCIALD_API_TOKEN", "synthetic-secret")
	t.Setenv("TELEGRAM_API_HASH", "synthetic-secret")
	for _, name := range []string{"probe", "large", "timeout"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			script := filepath.Join(root, "test-helper-"+name)
			protected := filepath.Join(root, "placeholder.dpapi")
			if os.WriteFile(script, []byte("synthetic placeholder, never executed"), 0600) != nil || os.WriteFile(protected, []byte("synthetic placeholder, never decrypted"), 0600) != nil {
				t.Fatal("fixture write")
			}
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			c, err := NewClient(Config{ProjectRoot: root, PythonPath: binary, ScriptPath: script, CredentialsPath: protected})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if name == "timeout" {
				ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer cancel()
			}
			_, err = c.Probe(ctx)
			if name == "probe" && err != nil {
				t.Fatal(err)
			}
			if name != "probe" && err == nil {
				t.Fatal("unbounded helper accepted")
			}
		})
	}
}

func TestMetadataDoesNotReadCredentialsAndPathsAreFixed(t *testing.T) {
	root := t.TempDir()
	c, err := NewClient(Config{ProjectRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(c.cfg.ScriptPath, filepath.Join("scripts", "telegram_user", "worker.py")) || !strings.Contains(c.cfg.PythonPath, "telegram-user-venv") {
		t.Fatal("unexpected helper location")
	}
	if c.Metadata().Configured {
		t.Fatal("missing file configured")
	}
	if os.MkdirAll(filepath.Dir(c.cfg.CredentialsPath), 0700) != nil || os.WriteFile(c.cfg.CredentialsPath, []byte("not-readable-as-credentials"), 0600) != nil {
		t.Fatal("fixture")
	}
	if !c.Metadata().Configured {
		t.Fatal("metadata attempted decrypt or parse")
	}
	for _, cfg := range []Config{{ProjectRoot: "relative"}, {ProjectRoot: root, PythonPath: "relative"}, {ProjectRoot: root, CredentialsPath: "https://example.test"}} {
		if _, err := NewClient(cfg); err == nil {
			t.Fatal("unsafe path accepted")
		}
	}
}
