//go:build windows

package localcredentials_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/localcredentials"
	"github.com/qilinbro/msg-manager/internal/localcredentials/credentialstest"
)

// These tests launch a fresh PowerShell host with an allowlisted environment,
// an explicit synthetic file and a module path containing only bundled modules.
// Neither native Known Folders nor a real credential file is used.
func syntheticPowerShell(t *testing.T, path, serviceURL, script string) ([]byte, error) {
	t.Helper()
	engine := os.Getenv("SOCIALD_TEST_POWERSHELL")
	if engine == "" {
		var err error
		engine, err = exec.LookPath("pwsh.exe")
		if err != nil {
			t.Skip("PowerShell 7 not installed; run scripts/test-inbox-launcher.ps1 separately")
		}
	}
	module, err := filepath.Abs(filepath.Join("..", "..", "scripts", "ServiceCredentials.psm1"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, engine, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; Import-Module $env:SYNTHETIC_SERVICE_MODULE -Force; "+script)
	cmd.WaitDelay = 2 * time.Second
	temporary := t.TempDir()
	cmd.Env = []string{
		"SOCIALD_CREDENTIAL_FILE=" + path,
		"SYNTHETIC_SERVICE_URL=" + serviceURL,
		"SYNTHETIC_SERVICE_MODULE=" + module,
		"TEMP=" + temporary, "TMP=" + temporary,
		"PSModulePath=" + filepath.Join(filepath.Dir(engine), "Modules"),
		"PATH=" + filepath.Dir(engine), "NO_COLOR=1",
	}
	for _, name := range []string{"SystemRoot", "WINDIR"} {
		if value, ok := os.LookupEnv(name); ok {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	output, err := cmd.CombinedOutput()
	if bytes.Contains(output, []byte(testToken)) {
		t.Fatal("PowerShell emitted a synthetic bearer token")
	}
	return output, err
}

const powerShellCredentialHash = `
$secure=Initialize-ServiceCredential -ServiceURL $env:SYNTHETIC_SERVICE_URL;
$pointer=[IntPtr]::Zero;
try {
    $pointer=[Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure);
    $token=[Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer);
    $digest=[Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes($token));
    Write-Output ('OK:'+[Convert]::ToHexString($digest));
} finally {
    if($pointer -ne [IntPtr]::Zero){[Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer)};
    $secure.Dispose(); $token=$null;
}`

func assertPowerShellHash(t *testing.T, output []byte, token string) {
	t.Helper()
	digest := sha256.Sum256([]byte(token))
	if strings.TrimSpace(string(output)) != "OK:"+strings.ToUpper(hex.EncodeToString(digest[:])) {
		t.Fatal("PowerShell did not return the expected synthetic token fingerprint")
	}
}

func TestPowerShellWriterGoReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-synthetic-credentials", "service.dpapi")
	t.Setenv(localcredentials.FileEnvironment, path)
	output, err := syntheticPowerShell(t, path, testURL, powerShellCredentialHash)
	if err != nil {
		t.Fatalf("PowerShell synthetic initialization failed: %v\n%s", err, output)
	}
	token, err := localcredentials.LoadToken(testURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 64 {
		t.Fatal("PowerShell did not generate a random 32-byte hexadecimal token")
	}
	if _, err := hex.DecodeString(token); err != nil {
		t.Fatal("PowerShell generated a non-hexadecimal token")
	}
	assertPowerShellHash(t, output, token)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	output, err = syntheticPowerShell(t, path, testURL, powerShellCredentialHash)
	if err != nil {
		t.Fatalf("PowerShell synthetic restart failed: %v\n%s", err, output)
	}
	assertPowerShellHash(t, output, token)
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("PowerShell restart replaced the saved ciphertext")
	}
	if bytes.Contains(output, []byte(token)) {
		t.Fatal("PowerShell printed its generated bearer token")
	}
}

func TestGoFixturePowerShellReader(t *testing.T) {
	path := credentialstest.Write(t, testURL, testToken)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	output, err := syntheticPowerShell(t, path, testURL, powerShellCredentialHash)
	if err != nil {
		t.Fatalf("PowerShell could not read a synthetic native Go DPAPI fixture: %v\n%s", err, output)
	}
	assertPowerShellHash(t, output, testToken)
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("PowerShell changed the Go fixture")
	}
}

func TestPowerShellSavedCredentialFailuresPreserveBytes(t *testing.T) {
	for _, tc := range []struct {
		name string
		path func(*testing.T) string
		url  string
	}{
		{"wrong-entropy", func(t *testing.T) string {
			return credentialstest.WritePlaintext(t, []byte(`{"version":1,"service_url":"`+testURL+`","api_token":"`+testToken+`"}`), "synthetic-wrong-purpose")
		}, testURL},
		{"url-mismatch", func(t *testing.T) string { return credentialstest.Write(t, testURL, testToken) }, "http://127.0.0.1:17891"},
		{"unknown-field", func(t *testing.T) string {
			return credentialstest.WritePlaintext(t, []byte(`{"version":1,"service_url":"`+testURL+`","api_token":"`+testToken+`","extra":true}`), credentialstest.Purpose)
		}, testURL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.path(t)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			output, err := syntheticPowerShell(t, path, tc.url, powerShellCredentialHash)
			if err == nil || !bytes.Contains(output, []byte("SOCIALD_CREDENTIAL_FILE")) || bytes.Contains(output, []byte(path)) {
				t.Fatal("PowerShell did not fail with a sanitized credential error")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("PowerShell repaired or replaced an unreadable synthetic credential")
			}
		})
	}
}
