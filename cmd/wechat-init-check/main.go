// wechat-init-check validates only a caller-selected local account directory.
// Credentials arrive through stdin; no configuration, paths or native errors
// are logged and no account discovery or key extraction is performed.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
	"unicode/utf8"

	"github.com/qilinbro/msg-manager/internal/connector/wechatdb"
)

const maxInputBytes = 8 << 10
const probeTimeout = 30 * time.Second

type accountProbe interface {
	Probe(context.Context) (wechatdb.Info, error)
}

type probeFactory func(wechatdb.Config) (accountProbe, error)

type probeResult struct {
	info wechatdb.Info
	err  error
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, newAccountProbe))
}

func newAccountProbe(cfg wechatdb.Config) (accountProbe, error) {
	return wechatdb.NewReader(cfg)
}

func run(parent context.Context, args []string, input io.Reader, output io.Writer, factory probeFactory) int {
	// Never interpret command-line arguments as credentials or input paths.
	if len(args) != 0 {
		return writeFailure(output, "invalid_config")
	}
	cfg, err := readConfig(input)
	if err != nil {
		return writeFailure(output, "invalid_config")
	}
	ctx, cancel := context.WithTimeout(parent, probeTimeout)
	defer cancel()
	if ctx.Err() != nil {
		return writeFailure(output, "unavailable")
	}
	// The process must still return on cancellation if a platform file access
	// or native driver operation has not yet observed the probe's context.
	results := make(chan probeResult, 1)
	go func() { results <- performProbe(ctx, cfg, factory) }()
	select {
	case <-ctx.Done():
		return writeFailure(output, "unavailable")
	case result := <-results:
		if ctx.Err() != nil {
			return writeFailure(output, "unavailable")
		}
		if result.err != nil {
			return writeFailure(output, failureCode(result.err))
		}
		if result.info.AccountID != cfg.AccountID || !result.info.ReadOnly {
			return writeFailure(output, "unsupported_schema")
		}
		if json.NewEncoder(output).Encode(struct {
			OK        bool   `json:"ok"`
			ReadOnly  bool   `json:"read_only"`
			AccountID string `json:"account_id"`
		}{true, true, cfg.AccountID}) != nil {
			return 1
		}
		return 0
	}
}

func performProbe(ctx context.Context, cfg wechatdb.Config, factory probeFactory) (result probeResult) {
	// A dependency panic must not turn supplied credentials or native details
	// into a stderr stack trace. Ordinary developer errors remain testable here.
	defer func() {
		if recover() != nil {
			result = probeResult{err: wechatdb.ErrUnavailable}
		}
	}()
	reader, err := factory(cfg)
	if err != nil {
		return probeResult{err: err}
	}
	info, err := reader.Probe(ctx)
	return probeResult{info: info, err: err}
}

func readConfig(input io.Reader) (wechatdb.Config, error) {
	raw, err := io.ReadAll(io.LimitReader(input, maxInputBytes+1))
	defer clear(raw)
	if err != nil || len(raw) > maxInputBytes || !utf8.Valid(raw) {
		return wechatdb.Config{}, wechatdb.ErrInvalidConfig
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return wechatdb.Config{}, wechatdb.ErrInvalidConfig
	}
	values := make(map[string]string, 3)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return wechatdb.Config{}, wechatdb.ErrInvalidConfig
		}
		name, ok := token.(string)
		if !ok || name != "directory" && name != "account_id" && name != "key" {
			return wechatdb.Config{}, wechatdb.ErrInvalidConfig
		}
		if _, duplicate := values[name]; duplicate {
			return wechatdb.Config{}, wechatdb.ErrInvalidConfig
		}
		token, err = decoder.Token()
		value, isString := token.(string)
		if err != nil || !isString || value == "" {
			return wechatdb.Config{}, wechatdb.ErrInvalidConfig
		}
		values[name] = value
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') || len(values) != 3 {
		return wechatdb.Config{}, wechatdb.ErrInvalidConfig
	}
	if _, err = decoder.Token(); err != io.EOF {
		return wechatdb.Config{}, wechatdb.ErrInvalidConfig
	}
	return wechatdb.Config{Directory: values["directory"], AccountID: values["account_id"], Key: values["key"]}, nil
}

func failureCode(err error) string {
	switch {
	case errors.Is(err, wechatdb.ErrInvalidConfig):
		return "invalid_config"
	case errors.Is(err, wechatdb.ErrInvalidKey):
		return "invalid_key"
	case errors.Is(err, wechatdb.ErrUnsupportedSchema):
		return "unsupported_schema"
	case errors.Is(err, wechatdb.ErrChanged):
		return "changed"
	default:
		return "unavailable"
	}
}

func writeFailure(output io.Writer, code string) int {
	_ = json.NewEncoder(output).Encode(struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
	}{false, code})
	return 1
}
