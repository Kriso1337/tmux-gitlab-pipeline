package repository_test

import (
	"path/filepath"
	"testing"

	"tmux-gitlab-pipeline/internal/repository"
)

func TestWorktreeDirectoryResolvesItsOwnBranchAndCommit(t *testing.T) {
	dir := fixture(t)
	git(t, dir, "remote", "add", "origin", "git@gitlab.example.com:team/project.git")
	worktree := filepath.Join(t.TempDir(), "worktree ' with spaces\nand newline")
	git(t, dir, "worktree", "add", "-b", "feature/database-cleanup", worktree)
	git(t, worktree, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "worktree")
	mainSHA := git(t, dir, "rev-parse", "HEAD")
	trees, err := repository.Worktrees(t.Context(), worktree)
	if err != nil || len(trees) != 2 {
		t.Fatalf("trees=%+v error=%v", trees, err)
	}
	repo, err := repository.Resolve(t.Context(), repository.Options{Directory: trees[1].Directory})
	if err != nil || repo.Directory != worktree || repo.Branch != "feature/database-cleanup" || repo.SHA == mainSHA || trees[1].SHA != repo.SHA {
		t.Fatalf("repo=%+v error=%v", repo, err)
	}
}

func TestWorktreesIncludesDetachedHEADAndRejectsNonRepositories(t *testing.T) {
	dir := fixture(t)
	git(t, dir, "worktree", "add", "--detach", filepath.Join(t.TempDir(), "detached"), "HEAD")
	trees, err := repository.Worktrees(t.Context(), dir)
	if err != nil || len(trees) != 2 || trees[1].Branch != "" || trees[1].SHA == "" {
		t.Fatalf("trees=%+v error=%v", trees, err)
	}
	if _, err := repository.Worktrees(t.Context(), t.TempDir()); err == nil {
		t.Fatal("non-Git directory accepted")
	}
}
