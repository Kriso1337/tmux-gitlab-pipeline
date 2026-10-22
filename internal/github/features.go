package github

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"tmux-gitlab-pipeline/internal/artifact"
	"tmux-gitlab-pipeline/internal/ci"
	"tmux-gitlab-pipeline/internal/process"
	"tmux-gitlab-pipeline/internal/repository"
)

func (c Client) History(ctx context.Context, repo repository.Repository, current ci.Pipeline) ([]ci.Pipeline, error) {
	query := url.Values{"per_page": {"20"}}
	if repo.Branch != "" {
		query.Set("branch", repo.Branch)
	} else {
		query.Set("head_sha", repo.SHA)
	}
	base := endpoint(repo) + "/runs"
	if current.WorkflowID > 0 {
		base = fmt.Sprintf("%s/workflows/%d/runs", endpoint(repo), current.WorkflowID)
	}
	response, err := request[struct {
		Runs []run `json:"workflow_runs"`
	}](ctx, c, repo, base+"?"+query.Encode())
	if err != nil {
		return nil, err
	}
	var items []ci.Pipeline
	for _, r := range response.Runs[:min(20, len(response.Runs))] {
		p, err := pipeline(r)
		if err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, nil
}
func (c Client) Mutate(ctx context.Context, repo repository.Repository, mutation ci.Mutation) error {
	var path string
	var fields []string
	switch mutation.Action {
	case ci.RetryJob:
		if mutation.Job.ID <= 0 || !mutation.Job.Status.Finished() {
			return fmt.Errorf("выберите завершённую job")
		}
		path = fmt.Sprintf("%s/jobs/%d/rerun", endpoint(repo), mutation.Job.ID)
	case ci.RetryFailed:
		if mutation.Pipeline.ID <= 0 || mutation.Pipeline.Status != ci.Failed {
			return fmt.Errorf("выберите упавший workflow")
		}
		path = fmt.Sprintf("%s/runs/%d/rerun-failed-jobs", endpoint(repo), mutation.Pipeline.ID)
	case ci.DispatchWorkflow:
		if mutation.Workflow.ID <= 0 || repo.Branch == "" || len(mutation.Inputs) > 25 {
			return fmt.Errorf("выберите workflow и ветку")
		}
		path = fmt.Sprintf("%s/workflows/%d/dispatches", endpoint(repo), mutation.Workflow.ID)
		fields = append(fields, "--raw-field", "ref="+repo.Branch)
		keys := make([]string, 0, len(mutation.Inputs))
		for key := range mutation.Inputs {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			if key == "" || len(key) > 100 || strings.ContainsAny(key, "[]\x00\r\n") || len(mutation.Inputs[key]) > 4096 {
				return fmt.Errorf("invalid workflow input")
			}
			fields = append(fields, "--raw-field", "inputs["+key+"]="+mutation.Inputs[key])
		}
	case ci.PlayJob:
		return fmt.Errorf("GitHub: approvals и environment protection доступны в браузере; s запускает workflow_dispatch")
	default:
		return fmt.Errorf("unsupported GitHub action")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := []string{"api", "--hostname", repo.Host, "--method", "POST", path}
	args = append(args, fields...)
	_, err := process.Output(ctx, repo.Directory, c.Executable, args...)
	return err
}
func (c Client) Workflows(ctx context.Context, repo repository.Repository) ([]ci.Workflow, error) {
	response, err := request[struct {
		Workflows []ci.Workflow `json:"workflows"`
		Total     int           `json:"total_count"`
	}](ctx, c, repo, endpoint(repo)+"/workflows?per_page=100")
	if err != nil {
		return nil, err
	}
	if response.Total > 100 {
		return nil, fmt.Errorf("workflow list exceeds 100 entries")
	}
	return slices.DeleteFunc(response.Workflows, func(w ci.Workflow) bool { return w.State != "active" }), nil
}
func (c Client) Artifacts(ctx context.Context, snapshot ci.Snapshot) ([]ci.Artifact, error) {
	if snapshot.Pipeline == nil {
		return nil, fmt.Errorf("workflow unavailable")
	}
	response, err := request[struct {
		Total     int `json:"total_count"`
		Artifacts []struct {
			ID      int64  `json:"id"`
			Name    string `json:"name"`
			Size    int64  `json:"size_in_bytes"`
			Expired bool   `json:"expired"`
		} `json:"artifacts"`
	}](ctx, c, snapshot.Repository, fmt.Sprintf("%s/runs/%d/artifacts?per_page=100", endpoint(snapshot.Repository), snapshot.Pipeline.ID))
	if err != nil {
		return nil, err
	}
	if response.Total > 100 {
		return nil, fmt.Errorf("artifact list exceeds 100 entries")
	}
	items := make([]ci.Artifact, 0, len(response.Artifacts))
	for _, item := range response.Artifacts {
		items = append(items, ci.Artifact{ID: item.ID, Name: item.Name, Size: item.Size, Expired: item.Expired})
	}
	return items, nil
}
func (c Client) fetchArtifact(ctx context.Context, repo repository.Repository, item ci.Artifact, output io.Writer) error {
	if item.ID <= 0 || item.Expired {
		return fmt.Errorf("artifact unavailable")
	}
	help, err := process.Output(ctx, repo.Directory, c.Executable, "api", "--help")
	if err != nil {
		return err
	}
	args := []string{"api", "--hostname", repo.Host, "--method", "GET", fmt.Sprintf("%s/artifacts/%d/zip", endpoint(repo), item.ID)}
	if strings.Contains(help, "--allow-escape-sequences") {
		args = append(args, "--allow-escape-sequences")
	}
	return process.Run(ctx, repo.Directory, c.Executable, args, output)
}
func (c Client) Download(ctx context.Context, repo repository.Repository, item ci.Artifact, directory string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	return artifact.Download(ctx, directory, "artifact-"+strconv.FormatInt(item.ID, 10)+"-"+item.Name+".zip", 512<<20, func(ctx context.Context, output io.Writer) error { return c.fetchArtifact(ctx, repo, item, output) })
}
func (c Client) Report(ctx context.Context, snapshot ci.Snapshot) (ci.Report, error) {
	items, err := c.Artifacts(ctx, snapshot)
	if err != nil {
		return ci.Report{}, err
	}
	report := ci.Report{}
	count := 0
	for _, item := range items {
		name := strings.ToLower(item.Name)
		if item.Expired || (!strings.Contains(name, "test") && !strings.Contains(name, "junit") && !strings.Contains(name, "coverage")) {
			continue
		}
		count++
		if count > 8 {
			report.Notice = "Показаны первые 8 артефактов с отчётами"
			break
		}
		buffer := &process.LimitBuffer{Limit: 16 << 20}
		if err := c.fetchArtifact(ctx, snapshot.Repository, item, buffer); err != nil {
			return report, err
		}
		parsed, err := artifact.ReportZIP(buffer.Bytes())
		if err != nil {
			return report, fmt.Errorf("artifact %s: %w", item.Name, err)
		}
		report.Total += parsed.Total
		report.Passed += parsed.Passed
		report.Failed += parsed.Failed
		report.Skipped += parsed.Skipped
		report.Duration += parsed.Duration
		if parsed.Coverage != nil {
			report.Coverage = parsed.Coverage
		}
		report.Failures = append(report.Failures, parsed.Failures...)
		if len(report.Failures) > 20 {
			report.Failures = report.Failures[:20]
		}
	}
	if report.Total == 0 && report.Coverage == nil {
		report.Notice = "Нужны артефакты test/junit/coverage с JUnit XML, Cobertura, LCOV или coverage-summary.json"
	}
	return report, nil
}
