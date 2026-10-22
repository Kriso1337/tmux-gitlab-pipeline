package tmux

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func HookDirectory(input io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(input, (64<<10)+1))
	if err != nil {
		return "", err
	}
	if len(data) > 64<<10 {
		return "", fmt.Errorf("hook payload exceeds 64 KiB")
	}
	var event struct {
		CWD    string `json:"cwd"`
		NewCWD string `json:"new_cwd"`
		Name   string `json:"hook_event_name"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return "", fmt.Errorf("decode context hook: %w", err)
	}
	directory := event.CWD
	if event.Name == "CwdChanged" {
		directory = event.NewCWD
	}
	if !filepath.IsAbs(directory) || len(directory) > 4096 || strings.ContainsAny(directory, "\x00\r\n") {
		return "", fmt.Errorf("invalid hook working directory")
	}
	return directory, nil
}
func RememberContext(ctx context.Context, pane, directory string) error {
	if os.Getenv("TMUX") == "" || pane == "" {
		return nil
	}
	identity, err := paneIdentity(ctx, pane)
	if err != nil {
		return err
	}
	return RememberWorktree(ctx, pane, identity, directory)
}
