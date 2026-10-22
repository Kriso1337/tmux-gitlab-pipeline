package browser_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tmux-gitlab-pipeline/internal/browser"
)

func TestOpenPassesHTTPURLAsOneLiteralArgument(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "report")
	t.Setenv("TGP_URL_REPORT", report)
	executable := filepath.Join(dir, "browser")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '%s\\n' \"$#\" \"$1\" > \"$TGP_URL_REPORT\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	address := "https://gitlab.example.com/team/project/-/merge_requests/1?x=a;b=2&value=$(touch+injected)"
	if err := (browser.Opener{Executable: executable}).Open(t.Context(), address); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(report)
	if err != nil || string(data) != "1\n"+address+"\n" {
		t.Fatalf("arguments=%q error=%v", data, err)
	}
}

func TestOpenRejectsUnsafeURLsAndReportsBrowserFailures(t *testing.T) {
	for _, address := range []string{"", "file:///tmp/file", "javascript:alert(1)", "https://user:password@example.com/path", "https://example.com/\nvalue", "https://example.com/\x1bvalue"} {
		if err := (browser.Opener{Executable: "must-not-run"}).Open(t.Context(), address); err == nil || !strings.Contains(err.Error(), "invalid") {
			t.Fatalf("url=%q error=%v", address, err)
		}
	}
	dir := t.TempDir()
	executable := filepath.Join(dir, "browser")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf 'browser unavailable' >&2\nexit 3\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := (browser.Opener{Executable: executable}).Open(t.Context(), "https://example.com/mr/1"); err == nil || !strings.Contains(err.Error(), "browser unavailable") {
		t.Fatalf("error=%v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := (browser.Opener{Executable: executable}).Open(ctx, "https://example.com/mr/1"); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("error=%v", err)
	}
}
