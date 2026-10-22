package gitlab

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/url"
	"strconv"
	"time"

	"tmux-gitlab-pipeline/internal/artifact"
	"tmux-gitlab-pipeline/internal/ci"
	"tmux-gitlab-pipeline/internal/process"
	"tmux-gitlab-pipeline/internal/repository"
)

func (c Client) History(ctx context.Context, repo repository.Repository, pipeline ci.Pipeline) ([]ci.Pipeline, error) {
	query := url.Values{"per_page": {"20"}, "order_by": {"id"}, "sort": {"desc"}}
	base := projectEndpoint(repo) + "/pipelines"
	if pipeline.MergeRequest > 0 {
		base = fmt.Sprintf("%s/merge_requests/%d/pipelines", projectEndpoint(repo), pipeline.MergeRequest)
	} else if repo.Branch != "" {
		query.Set("ref", repo.Branch)
	} else {
		query.Set("sha", repo.SHA)
	}
	pipelines, err := get[[]Pipeline](ctx, c, repo, base+"?"+query.Encode())
	if err != nil {
		return nil, err
	}
	if len(pipelines) > 20 {
		pipelines = pipelines[:20]
	}
	for i := range pipelines {
		detail, err := get[Pipeline](ctx, c, repo, fmt.Sprintf("%s/pipelines/%d", projectEndpoint(repo), pipelines[i].ID))
		if err != nil {
			return nil, err
		}
		detail.MergeRequest = pipeline.MergeRequest
		pipelines[i] = detail
	}
	return pipelines, nil
}
func (c Client) Mutate(ctx context.Context, repo repository.Repository, mutation ci.Mutation) error {
	var endpoint string
	switch mutation.Action {
	case ci.RetryJob:
		if mutation.Job.ID <= 0 || !mutation.Job.Status.Finished() {
			return fmt.Errorf("выберите завершённую job")
		}
		endpoint = fmt.Sprintf("%s/jobs/%d/retry", projectEndpoint(repo), mutation.Job.ID)
	case ci.PlayJob:
		if mutation.Job.ID <= 0 || mutation.Job.Status != ci.Manual {
			return fmt.Errorf("выберите manual job")
		}
		endpoint = fmt.Sprintf("%s/jobs/%d/play", projectEndpoint(repo), mutation.Job.ID)
	case ci.RetryFailed:
		if mutation.Pipeline.ID <= 0 || mutation.Pipeline.Status != ci.Failed {
			return fmt.Errorf("выберите упавший пайплайн")
		}
		endpoint = fmt.Sprintf("%s/pipelines/%d/retry", projectEndpoint(repo), mutation.Pipeline.ID)
	default:
		return fmt.Errorf("unsupported GitLab action")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := process.Output(ctx, repo.Directory, c.Executable, "api", "--hostname", repo.Host, "--method", "POST", endpoint)
	return err
}
func (c Client) Workflows(context.Context, repository.Repository) ([]ci.Workflow, error) {
	return nil, fmt.Errorf("для GitLab выберите manual job и нажмите s")
}
func (c Client) Artifacts(_ context.Context, snapshot ci.Snapshot) ([]ci.Artifact, error) {
	var items []ci.Artifact
	for _, stage := range snapshot.Stages {
		for _, job := range stage.Jobs {
			if job.ArtifactsFile != nil && job.ArtifactsFile.Name != "" {
				items = append(items, ci.Artifact{ID: job.ID, JobID: job.ID, Name: job.ArtifactsFile.Name, Size: job.ArtifactsFile.Size})
			}
		}
	}
	return items, nil
}
func (c Client) Download(ctx context.Context, repo repository.Repository, item ci.Artifact, directory string) (string, error) {
	if item.JobID <= 0 || item.Expired {
		return "", fmt.Errorf("artifact unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	name := fmt.Sprintf("job-%d-%s", item.JobID, item.Name)
	return artifact.Download(ctx, directory, name, 512<<20, func(ctx context.Context, output io.Writer) error {
		return process.Run(ctx, repo.Directory, c.Executable, []string{"api", "--hostname", repo.Host, "--method", "GET", fmt.Sprintf("%s/jobs/%d/artifacts", projectEndpoint(repo), item.JobID)}, output)
	})
}
func (c Client) Report(ctx context.Context, snapshot ci.Snapshot) (ci.Report, error) {
	if snapshot.Pipeline == nil {
		return ci.Report{}, fmt.Errorf("pipeline unavailable")
	}
	response, err := get[struct {
		Total    int     `json:"total_count"`
		Passed   int     `json:"success_count"`
		Failed   int     `json:"failed_count"`
		Errors   int     `json:"error_count"`
		Skipped  int     `json:"skipped_count"`
		Duration float64 `json:"total_time"`
		Suites   []struct {
			Name  string `json:"name"`
			Cases []struct {
				Name, Status string
				SystemOutput string `json:"system_output"`
			} `json:"test_cases"`
		} `json:"test_suites"`
	}](ctx, c, snapshot.Repository, fmt.Sprintf("%s/pipelines/%d/test_report", projectEndpoint(snapshot.Repository), snapshot.Pipeline.ID))
	if err != nil {
		return ci.Report{}, err
	}
	report := ci.Report{Total: response.Total, Passed: response.Passed, Failed: response.Failed + response.Errors, Skipped: response.Skipped, Duration: response.Duration}
	if snapshot.Pipeline.Coverage != "" {
		value, err := strconv.ParseFloat(snapshot.Pipeline.Coverage, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
			return report, fmt.Errorf("invalid pipeline coverage")
		}
		report.Coverage = &value
	}
	for _, suite := range response.Suites {
		for _, test := range suite.Cases {
			if (test.Status == "failed" || test.Status == "error") && len(report.Failures) < 20 {
				text := test.SystemOutput
				if len(text) > 2048 {
					text = text[:2048]
				}
				report.Failures = append(report.Failures, ci.TestFailure{Name: test.Name, Suite: suite.Name, Message: text})
			}
		}
	}
	if report.Total == 0 {
		report.Notice = "JUnit-отчёт не опубликован этим пайплайном"
	}
	return report, nil
}
