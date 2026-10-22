package source_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tmux-gitlab-pipeline/internal/ci"
	"tmux-gitlab-pipeline/internal/github"
	"tmux-gitlab-pipeline/internal/gitlab"
	"tmux-gitlab-pipeline/internal/process"
	"tmux-gitlab-pipeline/internal/repository"
	"tmux-gitlab-pipeline/internal/source"
)

func TestSourceRoutesRepositoryAndLogsToSelectedProvider(t *testing.T) {
	for _, tc := range []struct {
		host     string
		provider ci.Provider
		marker   string
	}{
		{"github.com", "", "GITHUB_SELECTED"},
		{"gitlab.example.com", "", "GITLAB_SELECTED"},
		{"github.enterprise.example", ci.GitHub, "GITHUB_SELECTED"},
		{"github.com", ci.GitLab, "GITLAB_SELECTED"},
	} {
		t.Run(tc.host+string(tc.provider), func(t *testing.T) {
			dir := t.TempDir()
			commands := [][]string{{"init", "-q", dir}, {"-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "fixture"}, {"-C", dir, "remote", "add", "origin", "https://" + tc.host + "/owner/repo.git"}}
			for _, args := range commands {
				if _, err := process.Output(t.Context(), "", "git", args...); err != nil {
					t.Fatal(err)
				}
			}
			glab, gh := filepath.Join(dir, "glab"), filepath.Join(dir, "gh")
			for path, marker := range map[string]string{glab: "GITLAB_SELECTED", gh: "GITHUB_SELECTED"} {
				if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '"+marker+"\\n' >&2\nexit 1\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			s := source.Source{Options: repository.Options{Directory: dir}, Provider: tc.provider, GitLab: gitlab.Client{Executable: glab}, GitHub: github.Client{Executable: gh}}
			if _, err := s.Load(t.Context(), 0); err == nil || !strings.Contains(err.Error(), tc.marker) {
				t.Fatalf("load error=%v", err)
			}
			repo, err := repository.Resolve(t.Context(), s.Options)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Trace(t.Context(), repo, ci.Job{ID: 1, Status: ci.Success}); err == nil || !strings.Contains(err.Error(), tc.marker) {
				t.Fatalf("trace error=%v", err)
			}
		})
	}
}
