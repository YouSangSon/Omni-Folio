package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestKiwoomMockSyncKeychainFixedCommandAndClosedOutput(t *testing.T) {
	for _, mode := range []string{"valid", "failure", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var child *exec.Cmd
			raw, err := readKiwoomMockKeychain(ctx, "darwin", func(ctx context.Context, path string, args ...string) *exec.Cmd {
				if path != "/usr/bin/security" || !reflect.DeepEqual(args, []string{"find-generic-password", "-s", "com.omni-folio.kiwoom.mock.readonly.v1", "-a", "credentials", "-w"}) {
					t.Fatal("Keychain invocation changed or included secret arguments")
				}
				child = mockSyncSecretChild(ctx, mode)
				return child
			})
			if child == nil || child.ProcessState == nil {
				t.Fatal("Keychain child was not joined")
			}
			if mode == "valid" {
				if err != nil || !bytes.Equal(raw, mockSyncSecret("9876543210")) {
					t.Fatalf("synthetic Keychain record rejected: %v", err)
				}
			} else if err == nil || len(raw) != 0 || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "9876543210") {
				t.Fatalf("failed/oversized Keychain result leaked private output: %v", err)
			}
		})
	}
	called := false
	_, err := readKiwoomMockKeychain(context.Background(), "linux", func(context.Context, string, ...string) *exec.Cmd {
		called = true
		return nil
	})
	if err == nil || called {
		t.Fatal("unsupported OS did not fail before subprocess")
	}
}

func TestKiwoomMockSyncKeychainCancellationJoinsOwnedProcess(t *testing.T) {
	ready, signal, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer ready.Close()
	defer signal.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var child *exec.Cmd
	result := make(chan error, 1)
	go func() {
		_, err := readKiwoomMockKeychain(ctx, "darwin", func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			child = mockSyncSecretChild(ctx, "blocked")
			child.ExtraFiles = []*os.File{signal}
			return child
		})
		result <- err
	}()
	if err := ready.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(ready, make([]byte, 1)); err != nil {
		t.Fatal("synthetic Keychain process did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) || child == nil || child.ProcessState == nil {
			t.Fatalf("cancel did not join child and preserve context: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled Keychain subprocess remained active")
	}
}

func mockSyncSecretChild(ctx context.Context, mode string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestKiwoomMockSyncKeychainSubprocessHelper")
	cmd.Env = append(os.Environ(), "OMNI_MOCK_SYNC_SECRET_HELPER="+mode)
	return cmd
}

func TestKiwoomMockSyncKeychainSubprocessHelper(t *testing.T) {
	switch os.Getenv("OMNI_MOCK_SYNC_SECRET_HELPER") {
	case "valid":
		os.Stdout.Write(mockSyncSecret("9876543210"))
		os.Exit(0)
	case "failure":
		io.WriteString(os.Stderr, "synthetic-private-stderr 9876543210")
		os.Exit(1)
	case "overflow":
		os.Stdout.Write(bytes.Repeat([]byte{'x'}, kiwoomMockSecretLimit+1))
		time.Sleep(time.Hour)
	case "blocked":
		ready := os.NewFile(3, "owned-ready-pipe")
		ready.Write([]byte{1})
		ready.Close()
		time.Sleep(time.Hour)
	}
}
