package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"time"
)

type LimitBuffer struct {
	bytes.Buffer
	Limit int
}

func (b *LimitBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.Limit {
		return 0, fmt.Errorf("command output exceeds %d bytes", b.Limit)
	}
	return b.Buffer.Write(p)
}

type TailBuffer struct {
	Data      []byte
	Limit     int
	Truncated bool
}

func (b *TailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(b.Data)+n > b.Limit {
		b.Truncated = true
		if n >= b.Limit {
			b.Data = append(b.Data[:0], p[n-b.Limit:]...)
			return n, nil
		}
		copy(b.Data, b.Data[len(b.Data)+n-b.Limit:])
		b.Data = b.Data[:b.Limit-n]
	}
	b.Data = append(b.Data, p...)
	return n, nil
}

func Run(ctx context.Context, directory, name string, args []string, output io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error {
		return killGroup(cmd.Process.Pid)
	}
	cmd.Dir = directory
	cmd.Env = slices.DeleteFunc(os.Environ(), func(value string) bool {
		return strings.HasPrefix(value, "GLAB_DEBUG_HTTP=") || strings.HasPrefix(value, "GLAB_DEBUG=") || strings.HasPrefix(value, "GH_DEBUG=")
	})
	cmd.Env = append(cmd.Env, "NO_COLOR=1", "GIT_TERMINAL_PROMPT=0", "GLAB_CHECK_UPDATE=false", "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "GH_NO_EXTENSION_UPDATE_NOTIFIER=1", "GH_PAGER=cat")
	stderr := &TailBuffer{Limit: 4096}
	cmd.Stdout = output
	cmd.Stderr = stderr
	runErr := cmd.Run()
	if cmd.Process != nil {
		runErr = errors.Join(runErr, killGroup(cmd.Process.Pid))
	}
	if err := runErr; err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%s: %w", name, ctx.Err())
		}
		message := strings.TrimSpace(string(stderr.Data))
		if message != "" {
			return fmt.Errorf("%s: %s: %w", name, message, err)
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func killGroup(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stop command process group: %w", err)
	}
	return nil
}

func Output(ctx context.Context, directory, name string, args ...string) (string, error) {
	output := &LimitBuffer{Limit: 8 << 20}
	if err := Run(ctx, directory, name, args, output); err != nil {
		return "", err
	}
	return strings.TrimSpace(output.String()), nil
}

func ExitCode(err error, code int) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == code
}
