package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateFilePublicationFailureOutcomes(t *testing.T) {
	for _, scenario := range []string{"encoder", "cancelled", "link-collision", "after-link-directory-open"} {
		t.Run(scenario, func(t *testing.T) {
			if scenario == "after-link-directory-open" && os.Getuid() == 0 {
				t.Skip("root bypasses the directory read permission failure")
			}
			dir := t.TempDir()
			defer os.Chmod(dir, 0700)
			path := filepath.Join(dir, "capture.json")
			parent, target, err := privateFileTarget(path)
			if err != nil {
				t.Fatal(err)
			}
			retained := filepath.Join(dir, ".omni-private-test-preexisting")
			if err := os.WriteFile(retained, []byte("other-operation"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sentinel := errors.New("encoder failure")
			digest, err := publishPrivateFile(ctx, parent, target, ".omni-private-test-*", func(w io.Writer) error {
				if _, err := io.WriteString(w, "complete-bytes\n"); err != nil {
					return err
				}
				switch scenario {
				case "encoder":
					return sentinel
				case "cancelled":
					cancel()
				case "link-collision":
					return os.WriteFile(target, []byte("existing-bytes"), 0600)
				case "after-link-directory-open":
					return os.Chmod(parent, 0300)
				}
				return nil
			})
			if chmodErr := os.Chmod(dir, 0700); chmodErr != nil {
				t.Fatal(chmodErr)
			}
			if err == nil || digest != "" {
				t.Fatalf("failed publication reported success: %v %s", err, digest)
			}
			if scenario == "encoder" && !errors.Is(err, sentinel) {
				t.Fatal("encoder failure cause was lost")
			}
			if scenario == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal("pre-Link cancellation cause was lost")
			}
			raw, readErr := os.ReadFile(target)
			switch scenario {
			case "link-collision":
				if readErr != nil || string(raw) != "existing-bytes" {
					t.Fatal("Link collision overwrote existing bytes")
				}
			case "after-link-directory-open":
				if readErr != nil || string(raw) != "complete-bytes\n" {
					t.Fatal("post-Link failure deleted or truncated the completed file")
				}
			default:
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatal("pre-Link failure published final output")
				}
			}
			entries, readErr := os.ReadDir(dir)
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, entry := range entries {
				if entry.Name() != filepath.Base(target) && entry.Name() != filepath.Base(retained) {
					t.Fatalf("owned temporary file remained: %s", entry.Name())
				}
			}
			if raw, err := os.ReadFile(retained); err != nil || string(raw) != "other-operation" {
				t.Fatal("cleanup removed another operation's temporary file")
			}
		})
	}
}
