package browser

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"runtime"
	"strings"
	"time"

	"tmux-gitlab-pipeline/internal/process"
)

type Opener struct{ Executable string }

func (o Opener) Open(ctx context.Context, address string) error {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || strings.ContainsAny(address, "\x00\r\n\x1b") {
		return fmt.Errorf("invalid merge request URL")
	}
	executable := o.Executable
	if executable == "" {
		switch runtime.GOOS {
		case "darwin":
			executable = "open"
		case "linux":
			executable = "xdg-open"
		default:
			return fmt.Errorf("default browser is unsupported on %s", runtime.GOOS)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := process.Run(ctx, "", executable, []string{address}, io.Discard); err != nil {
		return fmt.Errorf("open default browser: %w", err)
	}
	return nil
}
