package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCLIRejectsInvalidOptions(t *testing.T) {
	for _, args := range [][]string{{"--interval", "0s"}, {"--theme", "invalid"}, {"--icons", "invalid"}, {"--provider", "invalid"}, {"--pipeline", "-1"}, {"unexpected"}} {
		if err := run(t.Context(), args, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestDemoSnapshotWorksWithoutGlab(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var output bytes.Buffer
	if err := run(t.Context(), []string{"--demo", "--snapshot", "--no-motion"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "acme/payments") || !strings.Contains(output.String(), "integration") {
		t.Fatal(output.String())
	}
}

func TestMissingGlabHasActionableError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := run(t.Context(), []string{"--provider", "gitlab"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "glab auth login") {
		t.Fatalf("error=%v", err)
	}
}

func TestMissingGhHasActionableError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := run(t.Context(), []string{"--provider", "github"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "gh auth login") {
		t.Fatalf("error=%v", err)
	}
}
