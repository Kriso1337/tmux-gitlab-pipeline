package repository_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tmux-gitlab-pipeline/internal/process"
	"tmux-gitlab-pipeline/internal/repository"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := process.Output(t.Context(), dir, "git", args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func fixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo ' with spaces")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-b", "feature/test")
	git(t, dir, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "fixture")
	return dir
}

func TestResolveNestedDirectoryAndUpstreamRemote(t *testing.T) {
	dir := fixture(t)
	git(t, dir, "remote", "add", "origin", "git@gitlab.example.com:group/project.git")
	git(t, dir, "remote", "add", "upstream", "https://gitlab.other.com/nested/team/project.git")
	git(t, dir, "config", "branch.feature/test.remote", "upstream")
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	repo, err := repository.Resolve(t.Context(), repository.Options{Directory: nested})
	if err != nil {
		t.Fatal(err)
	}
	if repo.Directory != dir || repo.Host != "gitlab.other.com" || repo.Project != "nested/team/project" || repo.Branch != "feature/test" || len(repo.SHA) != 40 {
		t.Fatalf("repo=%+v", repo)
	}
}

func TestResolveDetachedWorktree(t *testing.T) {
	dir := fixture(t)
	git(t, dir, "remote", "add", "origin", "ssh://git@gitlab.example.com:2222/team/app.git")
	worktree := filepath.Join(t.TempDir(), "worktree")
	git(t, dir, "worktree", "add", "--detach", worktree, "HEAD")
	repo, err := repository.Resolve(t.Context(), repository.Options{Directory: worktree})
	if err != nil || repo.Branch != "" || repo.Host != "gitlab.example.com" {
		t.Fatalf("repo=%+v error=%v", repo, err)
	}
}

func TestResolveAmbiguousAndMissingRemotes(t *testing.T) {
	dir := fixture(t)
	if _, err := repository.Resolve(t.Context(), repository.Options{Directory: dir}); err == nil {
		t.Fatal("expected missing remote error")
	}
	git(t, dir, "remote", "add", "one", "git@gitlab.example.com:team/one.git")
	git(t, dir, "remote", "add", "two", "git@gitlab.example.com:team/two.git")
	if _, err := repository.Resolve(t.Context(), repository.Options{Directory: dir}); err == nil || !strings.Contains(err.Error(), "multiple remotes") {
		t.Fatalf("error=%v", err)
	}
	repo, err := repository.Resolve(t.Context(), repository.Options{Directory: dir, Remote: "two", Host: "gitlab.override.com"})
	if err != nil || repo.Host != "gitlab.override.com" || repo.Project != "team/two" {
		t.Fatalf("repo=%+v error=%v", repo, err)
	}
	if _, err := repository.Resolve(t.Context(), repository.Options{Directory: dir, Remote: "missing"}); err == nil {
		t.Fatal("expected unknown remote error")
	}
}

func TestResolveRejectsUnbornAndNonGitDirectories(t *testing.T) {
	dir := t.TempDir()
	if _, err := repository.Resolve(t.Context(), repository.Options{Directory: dir}); err == nil {
		t.Fatal("expected non-Git error")
	}
	git(t, dir, "init")
	if _, err := repository.Resolve(t.Context(), repository.Options{Directory: dir}); err == nil || !strings.Contains(err.Error(), "no commit") {
		t.Fatalf("error=%v", err)
	}
}

func TestParseRemoteProtocolsAndUntrustedPaths(t *testing.T) {
	cases := []struct {
		value, host, project string
		valid                bool
	}{
		{"git@gitlab.example.com:team/app.git", "gitlab.example.com", "team/app", true},
		{"https://gitlab.example.com:8443/nested/team/app.git", "gitlab.example.com:8443", "nested/team/app", true},
		{"ssh://git@alias:2222/team/app.git", "alias", "team/app", true},
		{"https://gitlab.example.com/team/app;echo-test.git", "gitlab.example.com", "team/app;echo-test", true},
		{"/tmp/local", "", "", false}, {"file:///tmp/local", "", "", false}, {"https://host/../app", "", "", false},
		{"https://host/team/app?token=example", "", "", false}, {"git@host:app.git", "", "", false}, {"git@host:team/\napp.git", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			host, project, err := repository.ParseRemote(tc.value)
			if (err == nil) != tc.valid || host != tc.host || project != tc.project {
				t.Fatalf("got=%q,%q,%v", host, project, err)
			}
		})
	}
}
