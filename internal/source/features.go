package source

import (
	"context"
	"fmt"
	"tmux-gitlab-pipeline/internal/ci"
	"tmux-gitlab-pipeline/internal/repository"
)

type featureProvider interface {
	History(context.Context, repository.Repository, ci.Pipeline) ([]ci.Pipeline, error)
	Mutate(context.Context, repository.Repository, ci.Mutation) error
	Workflows(context.Context, repository.Repository) ([]ci.Workflow, error)
	Artifacts(context.Context, ci.Snapshot) ([]ci.Artifact, error)
	Download(context.Context, repository.Repository, ci.Artifact, string) (string, error)
	Report(context.Context, ci.Snapshot) (ci.Report, error)
}

func (s Source) features(repo repository.Repository) featureProvider {
	if Detect(repo.Host, s.Provider) == ci.GitHub {
		return s.GitHub
	}
	return s.GitLab
}
func (s Source) History(ctx context.Context, repo repository.Repository, pipeline ci.Pipeline) ([]ci.Pipeline, error) {
	return s.features(repo).History(ctx, repo, pipeline)
}
func (s Source) Mutate(ctx context.Context, repo repository.Repository, mutation ci.Mutation) error {
	current, err := repository.Resolve(ctx, repository.Options{Directory: repo.Directory, Remote: repo.Remote, Host: repo.Host})
	if err != nil {
		return err
	}
	if current != repo {
		return fmt.Errorf("ветка, HEAD или remote изменились; откройте действие заново")
	}
	return s.features(repo).Mutate(ctx, repo, mutation)
}
func (s Source) Workflows(ctx context.Context, repo repository.Repository) ([]ci.Workflow, error) {
	return s.features(repo).Workflows(ctx, repo)
}
func (s Source) Artifacts(ctx context.Context, snapshot ci.Snapshot) ([]ci.Artifact, error) {
	return s.features(snapshot.Repository).Artifacts(ctx, snapshot)
}
func (s Source) Download(ctx context.Context, repo repository.Repository, item ci.Artifact, directory string) (string, error) {
	return s.features(repo).Download(ctx, repo, item, directory)
}
func (s Source) Report(ctx context.Context, snapshot ci.Snapshot) (ci.Report, error) {
	return s.features(snapshot.Repository).Report(ctx, snapshot)
}
