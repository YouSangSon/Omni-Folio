package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const kiwoomMockSecretLimit = 8192

type kiwoomMockCredentials struct {
	SchemaVersion  string `json:"schema_version"`
	AppKey         string `json:"app_key"`
	SecretKey      string `json:"secret_key"`
	AliasKeyBase64 string `json:"alias_key_base64"`
	AccountNumber  string `json:"account_number"`
}

func decodeKiwoomMockCredentials(raw []byte) (*kiwoomMockCredentials, []byte, error) {
	invalid := errors.New("kiwoom mock credential record is invalid")
	if len(raw) == 0 || len(raw) > kiwoomMockSecretLimit || !utf8.Valid(raw) {
		return nil, nil, invalid
	}
	var value kiwoomMockCredentials
	fields := map[string]*string{
		"schema_version": &value.SchemaVersion, "app_key": &value.AppKey, "secret_key": &value.SecretKey,
		"alias_key_base64": &value.AliasKeyBase64, "account_number": &value.AccountNumber,
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if token, err := dec.Token(); err != nil || token != json.Delim('{') {
		return nil, nil, invalid
	}
	seen := map[string]bool{}
	for dec.More() {
		token, err := dec.Token()
		key, ok := token.(string)
		if err != nil || !ok || fields[key] == nil || seen[key] || dec.Decode(fields[key]) != nil {
			return nil, nil, invalid
		}
		seen[key] = true
	}
	if token, err := dec.Token(); err != nil || token != json.Delim('}') || len(seen) != len(fields) {
		return nil, nil, invalid
	}
	if dec.Decode(new(any)) != io.EOF || value.SchemaVersion != "kiwoom-mock-read.v1" ||
		!validKiwoomMockKey(value.AppKey) || !validKiwoomMockKey(value.SecretKey) || !kiwoomAccountPattern.MatchString(value.AccountNumber) {
		return nil, nil, invalid
	}
	aliasKey, err := base64.StdEncoding.Strict().DecodeString(value.AliasKeyBase64)
	if err != nil || len(aliasKey) != 32 || base64.StdEncoding.EncodeToString(aliasKey) != value.AliasKeyBase64 {
		return nil, nil, invalid
	}
	return &value, aliasKey, nil
}

func validKiwoomMockKey(value string) bool {
	if len(value) == 0 || len(value) > 1024 || strings.TrimSpace(value) == "" {
		return false
	}
	return !strings.ContainsFunc(value, unicode.IsControl)
}

func readKiwoomMockSecret(ctx context.Context) ([]byte, error) {
	return readKiwoomMockKeychain(ctx, runtime.GOOS, exec.CommandContext)
}

func readKiwoomMockKeychain(ctx context.Context, goos string, command func(context.Context, string, ...string) *exec.Cmd) ([]byte, error) {
	if goos != "darwin" {
		return nil, errors.New("kiwoom mock sync requires macOS Keychain")
	}
	callCtx, cancel := context.WithTimeout(ctx, kiwoomCallTimeout)
	defer cancel()
	output := &kiwoomMockSecretOutput{cancel: cancel}
	cmd := command(callCtx, "/usr/bin/security", "find-generic-password", "-s", "com.omni-folio.kiwoom.mock.readonly.v1", "-a", "credentials", "-w")
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = output, io.Discard, time.Second
	if err := cmd.Run(); err != nil {
		clear(output.raw)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("kiwoom mock Keychain record is unavailable")
	}
	return output.raw, nil
}

type kiwoomMockSecretOutput struct {
	raw    []byte
	cancel context.CancelFunc
}

func (w *kiwoomMockSecretOutput) Write(p []byte) (int, error) {
	if len(p) > kiwoomMockSecretLimit-len(w.raw) {
		w.cancel()
		return 0, errors.New("kiwoom mock Keychain record is too large")
	}
	w.raw = append(w.raw, p...)
	return len(p), nil
}
