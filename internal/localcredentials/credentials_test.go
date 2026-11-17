package localcredentials

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const syntheticURL = "http://127.0.0.1:17890"
const syntheticToken = "synthetic-credential-token-12345678901234567890"

func TestResolvePathDoesNotConsultDefaultForExplicitPath(t *testing.T) {
	for _, path := range []string{"synthetic-explicit-path", ""} {
		called := false
		got, err := resolvePath(func(name string) (string, bool) {
			if name != FileEnvironment {
				t.Fatal("unexpected environment lookup")
			}
			return path, true
		}, func() (string, error) { called = true; return "", errors.New("must not resolve actual user location") })
		if called || got != path || (err != nil) != (path == "") {
			t.Fatal("explicit override consulted default storage or accepted an empty path")
		}
	}
	path, err := resolvePath(func(string) (string, bool) { return "", false }, func() (string, error) {
		return "synthetic-injected-default", nil
	})
	if err != nil || path != "synthetic-injected-default" {
		t.Fatal("injected default was not used")
	}
}

func TestCredentialRecordBoundaries(t *testing.T) {
	valid, err := json.Marshal(map[string]any{"version": 1, "service_url": syntheticURL, "api_token": syntheticToken})
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeRecord(valid, syntheticURL)
	if err != nil || got != syntheticToken {
		t.Fatal("valid synthetic credential rejected")
	}
	if _, err := decodeRecord(valid, "http://127.0.0.1:17891"); !errors.Is(err, ErrServiceURL) {
		t.Fatal("stored credential was not bound to its endpoint")
	}
	cases := []string{
		"", "null", "[]", "{}", string(valid) + "{}", string(valid) + " true", "\xef\xbb\xbf" + string(valid),
		strings.Replace(string(valid), `"version":1`, `"version":2`, 1),
		strings.Replace(string(valid), `"version":1`, `"version":null`, 1),
		strings.Replace(string(valid), `"version":1`, `"version":1.0`, 1),
		strings.Replace(string(valid), `"version":1`, `"version":"1"`, 1),
		strings.Replace(string(valid), `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(string(valid), `"version":1`, `"version":1,"extra":"never-echo-this"`, 1),
		strings.Replace(string(valid), `"version":1`, `"Version":1`, 1),
		strings.Replace(string(valid), syntheticToken, "short-synthetic-token", 1),
		strings.Replace(string(valid), syntheticToken, strings.Repeat("a", 4097), 1),
		strings.Replace(string(valid), syntheticToken, syntheticToken+`\n`, 1),
		strings.Replace(string(valid), syntheticToken, strings.Repeat("中", 32), 1),
		strings.Replace(string(valid), syntheticURL, "http://localhost:17890", 1),
		strings.Replace(string(valid), syntheticURL, "http://127.0.0.1:017890", 1),
		strings.Replace(string(valid), syntheticURL, "http://127.0.0.1:65536", 1),
		strings.Replace(string(valid), syntheticURL, "http://example.com:17890", 1),
		strings.Replace(string(valid), syntheticURL, "http://127.0.0.1:17890/", 1),
		string(append(append([]byte{}, valid[:len(valid)-1]...), 0xff, '}')),
		strings.Repeat(" ", MaxFileBytes+1),
	}
	for index, data := range cases {
		if token, err := decodeRecord([]byte(data), syntheticURL); err == nil || token != "" {
			t.Errorf("invalid synthetic record %d was accepted", index)
		} else if strings.Contains(err.Error(), syntheticToken) || strings.Contains(err.Error(), "never-echo-this") || strings.Contains(err.Error(), "short-synthetic-token") {
			t.Error("credential parser exposed a synthetic secret")
		}
	}
}
