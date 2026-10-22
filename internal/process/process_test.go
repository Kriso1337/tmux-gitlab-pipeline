package process_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"tmux-gitlab-pipeline/internal/process"
)

func TestTailBufferKeepsLastBytesAcrossChunkSizes(t *testing.T) {
	for _, chunks := range [][]string{{}, {"abc"}, {"abc", "def"}, {"0123456789"}, {"abc", "def", "ghijklmnop"}} {
		buffer := process.TailBuffer{Limit: 5}
		all := ""
		for _, chunk := range chunks {
			n, err := buffer.Write([]byte(chunk))
			if err != nil || n != len(chunk) {
				t.Fatalf("write=%d,%v", n, err)
			}
			all += chunk
		}
		want := all[max(0, len(all)-5):]
		if string(buffer.Data) != want || buffer.Truncated != (len(all) > 5) {
			t.Fatalf("chunks=%v data=%q truncated=%t", chunks, buffer.Data, buffer.Truncated)
		}
	}
}

func TestOutputLimitIsReported(t *testing.T) {
	buffer := process.LimitBuffer{Limit: 2}
	if _, err := buffer.Write([]byte("abc")); err == nil {
		t.Fatal("expected output limit error")
	}
}

func TestCommandFailureIncludesStderrAndExitCode(t *testing.T) {
	_, err := process.Output(t.Context(), "", "sh", "-c", "printf 'access denied' >&2; exit 7")
	if err == nil || !strings.Contains(err.Error(), "access denied") || !process.ExitCode(err, 7) {
		t.Fatalf("error=%v", err)
	}
}

func TestCommandStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	_, err := process.Output(ctx, "", "sleep", "10")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
}

func TestCommandDoesNotInheritProviderHTTPDebugging(t *testing.T) {
	t.Setenv("GLAB_DEBUG_HTTP", "true")
	t.Setenv("GLAB_DEBUG", "true")
	t.Setenv("GH_DEBUG", "api")
	result, err := process.Output(t.Context(), "", "sh", "-c", `printf '%s%s%s' "${GLAB_DEBUG_HTTP-}" "${GLAB_DEBUG-}" "${GH_DEBUG-}"`)
	if err != nil || result != "" {
		t.Fatalf("result=%q error=%v", result, err)
	}
}

func TestOrphanedChildrenCannotKeepCommandPipesOpen(t *testing.T) {
	started := time.Now()
	_, err := process.Output(t.Context(), "", "sh", "-c", "sleep 60 & exit 0")
	if err == nil || time.Since(started) > 3*time.Second {
		t.Fatalf("elapsed=%s error=%v", time.Since(started), err)
	}
}
