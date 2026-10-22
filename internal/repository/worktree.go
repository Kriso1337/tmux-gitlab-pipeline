package repository

import (
	"context"
	"strings"

	"tmux-gitlab-pipeline/internal/process"
)

type Worktree struct {
	Directory string
	Branch    string
	SHA       string
}

func Worktrees(ctx context.Context, directory string) ([]Worktree, error) {
	output, err := process.Output(ctx, directory, "git", "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	var result []Worktree
	for _, record := range strings.Split(output, "\x00\x00") {
		var tree Worktree
		prunable := false
		for _, field := range strings.Split(record, "\x00") {
			key, value, _ := strings.Cut(field, " ")
			switch key {
			case "worktree":
				tree.Directory = value
			case "branch":
				tree.Branch = strings.TrimPrefix(value, "refs/heads/")
			case "HEAD":
				tree.SHA = value
			case "prunable":
				prunable = true
			}
		}
		if tree.Directory != "" && tree.SHA != "" && !prunable {
			result = append(result, tree)
		}
	}
	return result, nil
}
