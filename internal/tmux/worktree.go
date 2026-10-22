package tmux

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"tmux-gitlab-pipeline/internal/process"
	"tmux-gitlab-pipeline/internal/repository"
)

type rememberedWorktree struct {
	Owner     string `json:"owner"`
	Directory string `json:"directory"`
}

func paneIdentity(ctx context.Context, pane string) (string, error) {
	metadata, err := process.Output(ctx, "", "tmux", "display-message", "-p", "-t", pane, "#{pane_pid}\n#{pane_current_path}")
	if err != nil {
		return "", err
	}
	pid, directory, ok := strings.Cut(metadata, "\n")
	if !ok {
		return "", fmt.Errorf("source pane process unavailable")
	}
	if n, err := strconv.Atoi(pid); err != nil || n <= 0 {
		return "", fmt.Errorf("invalid source pane process")
	}
	group, err := process.Output(ctx, "", "ps", "-o", "tpgid=", "-p", pid)
	if err != nil {
		return "", err
	}
	if _, err := strconv.Atoi(group); err != nil {
		return "", fmt.Errorf("invalid source pane foreground process")
	}
	return pid + ":" + group + ":" + directory, nil
}

func RememberWorktree(ctx context.Context, pane, identity, directory string) error {
	current, err := paneIdentity(ctx, pane)
	if err != nil {
		return err
	}
	if current != identity {
		return fmt.Errorf("процесс исходной панели изменился: откройте попап заново")
	}
	if _, err := repository.Worktrees(ctx, directory); err != nil {
		return err
	}
	data, err := json.Marshal(rememberedWorktree{Owner: identity, Directory: directory})
	if err != nil {
		return err
	}
	_, err = process.Output(ctx, "", "tmux", "set-option", "-p", "-t", pane, "@gitlab-pipeline-worktree", string(data))
	return err
}

func savedWorktree(ctx context.Context, pane, identity string) (string, error) {
	data, err := process.Output(ctx, "", "tmux", "show-option", "-pqv", "-t", pane, "@gitlab-pipeline-worktree")
	if err != nil || data == "" {
		return "", err
	}
	var selected rememberedWorktree
	if err := json.Unmarshal([]byte(data), &selected); err != nil {
		return "", fmt.Errorf("decode pane worktree: %w", err)
	}
	if selected.Owner != identity {
		return "", nil
	}
	return selected.Directory, nil
}

func agentCommand(command string) bool {
	return strings.Contains(command, "claude") || strings.Contains(command, "codex") || strings.Count(command, ".") == 2 && strings.Trim(command, "0123456789.") == ""
}
