package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/connector/wechatdb"
)

const syntheticAccount = "wxid_synthetic_probe"
const syntheticKey = "00112233445566778899AABBCCDDEEFF000112233445566778899AABBCCDDEEFF"
const syntheticDirectory = `D:\synthetic-only\wxid_synthetic_probe\db_storage`

type fakeProbe func(context.Context) (wechatdb.Info, error)

func (f fakeProbe) Probe(ctx context.Context) (wechatdb.Info, error) { return f(ctx) }

func validInput(t *testing.T) string {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"directory": syntheticDirectory, "account_id": syntheticAccount, "key": syntheticKey})
	if err != nil {
		t.Fatal("could not build synthetic input")
	}
	return string(raw)
}

func successfulFactory(cfg wechatdb.Config) (accountProbe, error) {
	return fakeProbe(func(context.Context) (wechatdb.Info, error) {
		return wechatdb.Info{AccountID: cfg.AccountID, ReadOnly: true, SessionCount: 789}, nil
	}), nil
}

func TestRunUsesOnlyStdinAndPassesKeyToExistingReaderUnchanged(t *testing.T) {
	var called atomic.Int32
	factory := func(cfg wechatdb.Config) (accountProbe, error) {
		called.Add(1)
		// The supplied hex string is the connector's password input. The CLI
		// must not pre-derive it or reinterpret it as a per-file SQLCipher key.
		if cfg.Directory != syntheticDirectory || cfg.AccountID != syntheticAccount || cfg.Key != syntheticKey || cfg.Limit != 0 {
			t.Error("probe configuration was changed or default discovery added")
		}
		return fakeProbe(func(ctx context.Context) (wechatdb.Info, error) {
			deadline, ok := ctx.Deadline()
			if remaining := time.Until(deadline); !ok || remaining < 25*time.Second || remaining > probeTimeout {
				t.Error("probe context is not limited to thirty seconds")
			}
			return wechatdb.Info{AccountID: syntheticAccount, ReadOnly: true, SessionCount: 789}, nil
		}), nil
	}
	var output bytes.Buffer
	if code := run(context.Background(), nil, strings.NewReader(validInput(t)), &output, factory); code != 0 || called.Load() != 1 {
		t.Fatal("valid stdin probe did not succeed")
	}
	want := `{"ok":true,"read_only":true,"account_id":"` + syntheticAccount + `"}` + "\n"
	if output.String() != want {
		t.Fatal("successful probe returned extra metadata or unexpected fields")
	}
	if strings.Contains(output.String(), syntheticKey) || strings.Contains(output.String(), "synthetic-only") || strings.Contains(output.String(), "789") {
		t.Fatal("successful probe disclosed private configuration or session metadata")
	}
}

func TestInvalidJSONDoesNotConstructReader(t *testing.T) {
	valid := validInput(t)
	cases := []string{
		"", " ", "null", `[]`, `"text"`, `{}`, `{"directory":"x"}`,
		valid[:len(valid)-1], valid + " {}", valid + " null", valid + " trailing",
		strings.TrimSuffix(valid, "}") + `,"extra":"private"}`,
		strings.TrimSuffix(valid, "}") + `,"key":"duplicate"}`,
		strings.TrimSuffix(valid, "}") + `,"\u006bey":"duplicate"}`,
		strings.Replace(valid, `"account_id"`, `"Account_ID"`, 1),
		strings.Replace(valid, `"key":"`+syntheticKey+`"`, `"key":null`, 1),
		strings.Replace(valid, `"key":"`+syntheticKey+`"`, `"key":123`, 1),
		strings.Replace(valid, `"key":"`+syntheticKey+`"`, `"key":{}`, 1),
		strings.Replace(valid, `"key":"`+syntheticKey+`"`, `"key":[]`, 1),
		strings.Replace(valid, `"key":"`+syntheticKey+`"`, `"key":""`, 1),
		"{\"directory\":\"\xff\",\"account_id\":\"owner\",\"key\":\"key\"}",
	}
	for i, input := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var output bytes.Buffer
			factory := func(wechatdb.Config) (accountProbe, error) {
				t.Error("invalid JSON touched a source factory")
				return nil, wechatdb.ErrUnavailable
			}
			if code := run(context.Background(), nil, strings.NewReader(input), &output, factory); code == 0 || output.String() != "{\"ok\":false,\"code\":\"invalid_config\"}\n" {
				t.Fatal("invalid input did not return a fixed safe failure")
			}
		})
	}
}

type trackedInput struct{ called bool }

func (r *trackedInput) Read([]byte) (int, error) {
	r.called = true
	return 0, errors.New("synthetic private stdin failure")
}

func TestArgumentsAreRejectedBeforeReadingStdin(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--key", syntheticKey}, {syntheticDirectory}, {syntheticKey}, {"-"}} {
		input := &trackedInput{}
		var output bytes.Buffer
		factory := func(wechatdb.Config) (accountProbe, error) {
			t.Error("argv selected a source")
			return nil, wechatdb.ErrUnavailable
		}
		if code := run(context.Background(), args, input, &output, factory); code == 0 || input.called || output.String() != "{\"ok\":false,\"code\":\"invalid_config\"}\n" {
			t.Fatal("arguments were accepted, echoed, or caused stdin to be read")
		}
	}
}

func TestEightKiBInputBoundary(t *testing.T) {
	valid := validInput(t)
	for _, size := range []int{maxInputBytes, maxInputBytes + 1} {
		var output bytes.Buffer
		var called atomic.Int32
		factory := func(cfg wechatdb.Config) (accountProbe, error) {
			called.Add(1)
			return successfulFactory(cfg)
		}
		input := valid + strings.Repeat(" ", size-len(valid))
		code := run(context.Background(), nil, strings.NewReader(input), &output, factory)
		if size == maxInputBytes && (code != 0 || called.Load() != 1) {
			t.Fatal("exactly eight KiB was rejected")
		}
		if size > maxInputBytes && (code == 0 || called.Load() != 0 || output.String() != "{\"ok\":false,\"code\":\"invalid_config\"}\n") {
			t.Fatal("oversize input was accepted or touched a source")
		}
	}
}

func TestStableErrorCodesNeverPrintNativeErrors(t *testing.T) {
	for _, source := range []string{"factory", "probe"} {
		for _, item := range []struct {
			err  error
			code string
		}{
			{wechatdb.ErrInvalidConfig, "invalid_config"}, {wechatdb.ErrInvalidKey, "invalid_key"},
			{wechatdb.ErrUnsupportedSchema, "unsupported_schema"}, {wechatdb.ErrChanged, "changed"},
			{wechatdb.ErrUnavailable, "unavailable"}, {context.Canceled, "unavailable"},
			{wechatdb.ErrNotFound, "unavailable"}, {errors.New("unknown native failure"), "unavailable"},
		} {
			t.Run(source+"/"+item.code+"/"+item.err.Error(), func(t *testing.T) {
				privateError := fmt.Errorf("private %s %s: %w", syntheticDirectory, syntheticKey, item.err)
				factory := func(wechatdb.Config) (accountProbe, error) {
					if source == "factory" {
						return nil, privateError
					}
					return fakeProbe(func(context.Context) (wechatdb.Info, error) { return wechatdb.Info{}, privateError }), nil
				}
				var output bytes.Buffer
				if code := run(context.Background(), nil, strings.NewReader(validInput(t)), &output, factory); code == 0 || output.String() != "{\"ok\":false,\"code\":\""+item.code+"\"}\n" {
					t.Fatal("native error did not map to fixed safe code")
				}
			})
		}
	}
}

func TestProbeIdentityAndReadOnlyMustMatch(t *testing.T) {
	for _, info := range []wechatdb.Info{{AccountID: "wxid_other", ReadOnly: true}, {AccountID: syntheticAccount, ReadOnly: false}} {
		var output bytes.Buffer
		factory := func(wechatdb.Config) (accountProbe, error) {
			return fakeProbe(func(context.Context) (wechatdb.Info, error) { return info, nil }), nil
		}
		if code := run(context.Background(), nil, strings.NewReader(validInput(t)), &output, factory); code == 0 || output.String() != "{\"ok\":false,\"code\":\"unsupported_schema\"}\n" {
			t.Fatal("identity mismatch or writable source was accepted")
		}
	}
}

func TestProbeCancellationAndPanicAreSafe(t *testing.T) {
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		entered := make(chan struct{})
		finished := make(chan struct{})
		factory := func(wechatdb.Config) (accountProbe, error) {
			return fakeProbe(func(probeCtx context.Context) (wechatdb.Info, error) {
				defer close(finished)
				close(entered)
				<-probeCtx.Done()
				return wechatdb.Info{}, probeCtx.Err()
			}), nil
		}
		var output bytes.Buffer
		done := make(chan int, 1)
		input := validInput(t)
		go func() { done <- run(ctx, nil, strings.NewReader(input), &output, factory) }()
		<-entered
		cancel()
		if code := <-done; code == 0 || output.String() != "{\"ok\":false,\"code\":\"unavailable\"}\n" {
			t.Fatal("canceled probe did not stop safely")
		}
		<-finished
	})
	t.Run("panic", func(t *testing.T) {
		var output bytes.Buffer
		factory := func(wechatdb.Config) (accountProbe, error) {
			panic("synthetic private panic " + syntheticKey + syntheticDirectory)
		}
		if code := run(context.Background(), nil, strings.NewReader(validInput(t)), &output, factory); code == 0 || output.String() != "{\"ok\":false,\"code\":\"unavailable\"}\n" {
			t.Fatal("dependency panic was not redacted")
		}
	})
}

type brokenOutput struct{}

func (brokenOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestInputAndOutputErrorsFailClosed(t *testing.T) {
	var output bytes.Buffer
	if code := run(context.Background(), nil, &trackedInput{}, &output, successfulFactory); code == 0 || output.String() != "{\"ok\":false,\"code\":\"invalid_config\"}\n" {
		t.Fatal("stdin error did not fail safely")
	}
	if code := run(context.Background(), nil, strings.NewReader(validInput(t)), brokenOutput{}, successfulFactory); code == 0 {
		t.Fatal("broken stdout still reported success")
	}
}
