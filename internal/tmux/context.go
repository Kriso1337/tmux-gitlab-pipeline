package tmux

import (
	"context"
	"fmt"
	"regexp"

	"tmux-gitlab-pipeline/internal/process"
	"tmux-gitlab-pipeline/internal/repository"
)

func paneDirectory(ctx context.Context, pane, directory, configured string) (string, string, bool, error) {
	identity, err := paneIdentity(ctx, pane)
	if err != nil {
		return "", "", false, err
	}
	saved, err := savedWorktree(ctx, pane, identity)
	if err != nil {
		return "", "", false, err
	}
	if configured != directory {
		return configured, identity, false, nil
	}
	if saved != "" {
		return saved, identity, false, nil
	}
	command, err := process.Output(ctx, "", "tmux", "display-message", "-p", "-t", pane, "#{pane_current_command}")
	if err != nil {
		return "", "", false, err
	}
	if agentCommand(command) {
		trees, err := repository.Worktrees(ctx, directory)
		if err != nil {
			return "", "", false, err
		}
		root, err := process.Output(ctx, directory, "git", "rev-parse", "--show-toplevel")
		if err != nil {
			return "", "", false, err
		}
		return configured, identity, len(trees) > 1 && trees[0].Directory == root, nil
	}
	return configured, identity, false, nil
}
func RepositoryContext(ctx context.Context, pane string) (repository.Options, string, error) {
	if !regexp.MustCompile(`^%[0-9]+$`).MatchString(pane) {
		return repository.Options{}, "", fmt.Errorf("invalid source pane")
	}
	directory, err := process.Output(ctx, "", "tmux", "display-message", "-p", "-t", pane, "#{pane_current_path}")
	if err != nil {
		return repository.Options{}, "", err
	}
	configured, err := option(ctx, "repo", directory)
	if err != nil {
		return repository.Options{}, "", err
	}
	selected, _, ambiguous, err := paneDirectory(ctx, pane, directory, configured)
	if err != nil {
		return repository.Options{}, "", err
	}
	if ambiguous {
		return repository.Options{}, "", fmt.Errorf("select the agent worktree in the popup")
	}
	options := repository.Options{Directory: selected}
	options.Host, err = option(ctx, "host", "")
	if err != nil {
		return options, "", err
	}
	options.Remote, err = option(ctx, "remote", "")
	if err != nil {
		return options, "", err
	}
	provider, err := option(ctx, "provider", "auto")
	if provider == "auto" {
		provider = ""
	}
	return options, provider, err
}
