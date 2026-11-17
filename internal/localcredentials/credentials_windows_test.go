//go:build windows

package localcredentials_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qilinbro/msg-manager/internal/localcredentials"
	"github.com/qilinbro/msg-manager/internal/localcredentials/credentialstest"
	"golang.org/x/sys/windows"
)

const testURL = "http://127.0.0.1:17890"
const testToken = "synthetic-windows-credential-12345678901234567890"

func syntheticRecord(t *testing.T) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"version": 1, "service_url": testURL, "api_token": testToken})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestWindowsCredentialReadIsBoundedReadOnlyAndBoundToURL(t *testing.T) {
	path := credentialstest.Write(t, testURL, testToken)
	t.Setenv(localcredentials.FileEnvironment, path)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(before, []byte(testToken)) {
		t.Fatal("synthetic token was persisted in plaintext")
	}
	for range 2 {
		got, err := localcredentials.LoadToken(testURL)
		if err != nil || got != testToken {
			t.Fatalf("could not reload the same synthetic credential: %v", err)
		}
	}
	if _, err := localcredentials.LoadToken("http://127.0.0.1:17891"); !errors.Is(err, localcredentials.ErrServiceURL) {
		t.Fatal("credential accepted for a different listener")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("read-only loader modified the credential")
	}
}

func TestWindowsCredentialFailurePreservesBytes(t *testing.T) {
	cases := []struct {
		name string
		make func(*testing.T) string
		want error
	}{
		{"missing", func(t *testing.T) string {
			return filepath.Join(filepath.Dir(credentialstest.Write(t, testURL, testToken)), "missing.dpapi")
		}, localcredentials.ErrMissing},
		{"wrong_entropy", func(t *testing.T) string {
			return credentialstest.WritePlaintext(t, syntheticRecord(t), credentialstest.Purpose+"-wrong")
		}, localcredentials.ErrDecrypt},
		{"bad_schema", func(t *testing.T) string {
			return credentialstest.WritePlaintext(t, []byte(`{"version":2,"service_url":"http://127.0.0.1:17890","api_token":"`+testToken+`"}`), credentialstest.Purpose)
		}, localcredentials.ErrRecord},
		{"tampered", func(t *testing.T) string {
			path := credentialstest.Write(t, testURL, testToken)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			data[len(data)-1] ^= 0xff
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			return path
		}, localcredentials.ErrDecrypt},
		{"oversize", func(t *testing.T) string {
			path := credentialstest.Write(t, testURL, testToken)
			if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, localcredentials.MaxFileBytes+1), 0600); err != nil {
				t.Fatal(err)
			}
			return path
		}, localcredentials.ErrRecord},
		{"other_file_reader", func(t *testing.T) string {
			path := credentialstest.Write(t, testURL, testToken)
			setSyntheticDACL(t, path, "D:P(A;;FA;;;%s)(A;;FR;;;WD)", true)
			return path
		}, localcredentials.ErrUnsafe},
		{"unsafe_parent", func(t *testing.T) string {
			path := credentialstest.Write(t, testURL, testToken)
			setSyntheticDACL(t, filepath.Dir(path), "D:P(A;OICI;FA;;;%s)(A;OICI;FR;;;WD)", true)
			return path
		}, localcredentials.ErrUnsafe},
		{"inherited_file_acl", func(t *testing.T) string {
			path := credentialstest.Write(t, testURL, testToken)
			setSyntheticDACL(t, path, "D:(A;;FA;;;%s)", false)
			return path
		}, localcredentials.ErrUnsafe},
		{"hard_link", func(t *testing.T) string {
			path := credentialstest.Write(t, testURL, testToken)
			if err := os.Link(path, filepath.Join(filepath.Dir(path), "synthetic-alias.dpapi")); err != nil {
				t.Fatal(err)
			}
			return path
		}, localcredentials.ErrRecord},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.make(t)
			t.Setenv(localcredentials.FileEnvironment, path)
			before, readErr := os.ReadFile(path)
			got, err := localcredentials.LoadToken(testURL)
			if got != "" || !errors.Is(err, tc.want) {
				t.Fatalf("unexpected safe-failure category: %v", err)
			}
			if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), path) {
				t.Fatal("loader exposed a synthetic secret or path")
			}
			after, afterErr := os.ReadFile(path)
			if (readErr == nil) != (afterErr == nil) || !bytes.Equal(before, after) {
				t.Fatal("failed load created or changed credentials")
			}
		})
	}
}

func setSyntheticDACL(t *testing.T, path, template string, protected bool) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString(strings.ReplaceAll(template, "%s", user.User.Sid.String()))
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	flags := windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION
	if protected {
		flags = windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.SECURITY_INFORMATION(flags), nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsCredentialRejectsAmbiguousAndNonLocalPaths(t *testing.T) {
	for _, path := range []string{"", "relative.dpapi", `C:relative.dpapi`, `\\server\share\secret.dpapi`, `\\?\C:\private\secret.dpapi`, `C:\private\secret.dpapi:stream`, `C:\private\..\secret.dpapi`, `C:\private\NUL`, `C:\private\secret.dpapi `, `C:\private\secret.dpapi.`, `C:\private\\secret.dpapi`} {
		t.Setenv(localcredentials.FileEnvironment, path)
		if token, err := localcredentials.LoadToken(testURL); token != "" || !errors.Is(err, localcredentials.ErrPath) {
			t.Fatal("ambiguous/non-local explicit credential path was accepted")
		}
	}
}

func TestWindowsCredentialRejectsLinkedFileWhenSupported(t *testing.T) {
	path := credentialstest.Write(t, testURL, testToken)
	link := filepath.Join(filepath.Dir(path), "synthetic-link.dpapi")
	if err := os.Symlink(path, link); err != nil {
		t.Skip("creating a synthetic Windows symlink requires developer mode or a link privilege")
	}
	t.Setenv(localcredentials.FileEnvironment, link)
	if token, err := localcredentials.LoadToken(testURL); token != "" || !errors.Is(err, localcredentials.ErrPath) {
		t.Fatal("credential loader followed a reparse-point file")
	}
}
