package source

import (
	"context"
	"fmt"
	"strings"

	"tmux-gitlab-pipeline/internal/ci"
	"tmux-gitlab-pipeline/internal/github"
	"tmux-gitlab-pipeline/internal/gitlab"
	"tmux-gitlab-pipeline/internal/repository"
)

type Source struct {
	Options  repository.Options
	Provider ci.Provider
	GitLab   gitlab.Client
	GitHub   github.Client
}

func Detect(host string, requested ci.Provider) ci.Provider {
	if requested != "" {
		return requested
	}
	if strings.EqualFold(host, "github.com") {
		return ci.GitHub
	}
	return ci.GitLab
}

func (s Source) Load(ctx context.Context, id int64) (ci.Snapshot, error) {
	repo, err := repository.Resolve(ctx, s.Options)
	if err != nil {
		return ci.Snapshot{}, err
	}
	return s.LoadRepository(ctx, repo, id)
}

func (s Source) LoadRepository(ctx context.Context, repo repository.Repository, id int64) (ci.Snapshot, error) {
	switch Detect(repo.Host, s.Provider) {
	case ci.GitHub:
		return s.GitHub.Load(ctx, repo, id)
	case ci.GitLab:
		return s.GitLab.Load(ctx, repo, id)
	default:
		return ci.Snapshot{}, fmt.Errorf("unknown CI provider %q", s.Provider)
	}
}

func (s Source) Trace(ctx context.Context, repo repository.Repository, job ci.Job) (ci.Trace, error) {
	switch Detect(repo.Host, s.Provider) {
	case ci.GitHub:
		return s.GitHub.Trace(ctx, repo, job)
	case ci.GitLab:
		return s.GitLab.Trace(ctx, repo, job)
	default:
		return ci.Trace{}, fmt.Errorf("unknown CI provider %q", s.Provider)
	}
}

func (s Source) ChangeURL(ctx context.Context, repo repository.Repository) (string, error) {
	switch Detect(repo.Host, s.Provider) {
	case ci.GitHub:
		return s.GitHub.ChangeURL(ctx, repo)
	case ci.GitLab:
		return s.GitLab.ChangeURL(ctx, repo)
	default:
		return "", fmt.Errorf("unknown CI provider %q", s.Provider)
	}
}
