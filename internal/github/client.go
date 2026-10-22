package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"tmux-gitlab-pipeline/internal/ci"
	"tmux-gitlab-pipeline/internal/process"
	"tmux-gitlab-pipeline/internal/repository"
)

type Client struct{ Executable string }

func (c Client) ChangeURL(ctx context.Context, repo repository.Repository) (string, error) {
	if repo.Branch == "" {
		return "", fmt.Errorf("detached HEAD: выберите worktree с веткой")
	}
	owner, _, ok := strings.Cut(repo.Project, "/")
	if !ok || len(strings.Split(repo.Project, "/")) != 2 {
		return "", fmt.Errorf("GitHub repository must use owner/repository")
	}
	query := url.Values{"state": {"open"}, "head": {owner + ":" + repo.Branch}, "sort": {"updated"}, "direction": {"desc"}, "per_page": {"100"}}
	pulls, err := request[[]struct {
		Number int64 `json:"number"`
		Head   struct {
			Ref  string `json:"ref"`
			Repo struct {
				Name string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
	}](ctx, c, repo, strings.TrimSuffix(endpoint(repo), "/actions")+"/pulls?"+query.Encode())
	if err != nil {
		return "", err
	}
	for _, pr := range pulls {
		if pr.Head.Ref == repo.Branch && strings.EqualFold(pr.Head.Repo.Name, repo.Project) && pr.Number > 0 {
			address := url.URL{Scheme: "https", Host: repo.Host, Path: fmt.Sprintf("/%s/pull/%d", repo.Project, pr.Number)}
			return address.String(), nil
		}
	}
	return "", fmt.Errorf("для ветки %s нет открытого PR", repo.Branch)
}

type run struct {
	ID         int64      `json:"id"`
	Number     int64      `json:"run_number"`
	WorkflowID int64      `json:"workflow_id"`
	Name       string     `json:"name"`
	SHA        string     `json:"head_sha"`
	Branch     string     `json:"head_branch"`
	Status     string     `json:"status"`
	Conclusion string     `json:"conclusion"`
	Event      string     `json:"event"`
	URL        string     `json:"html_url"`
	StartedAt  *time.Time `json:"run_started_at"`
	UpdatedAt  *time.Time `json:"updated_at"`
}

type step struct {
	Name       string `json:"name"`
	Number     int    `json:"number"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

type job struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Conclusion  string     `json:"conclusion"`
	URL         string     `json:"html_url"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
	Steps       []step     `json:"steps"`
}

func request[T any](ctx context.Context, c Client, repo repository.Repository, endpoint string) (T, error) {
	return requestLimited[T](ctx, c, repo, endpoint, nil)
}
func requestLimited[T any](ctx context.Context, c Client, repo repository.Repository, endpoint string, budget *int) (T, error) {
	var result T
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	data, err := process.Output(ctx, repo.Directory, c.Executable, "api", "--hostname", repo.Host, "--method", "GET", endpoint)
	if err != nil {
		return result, fmt.Errorf("GitHub %s: %w", endpoint, err)
	}
	if budget != nil {
		*budget += len(data)
		if *budget > 8<<20 {
			return result, fmt.Errorf("GitHub paginated response exceeds 8 MiB")
		}
	}
	if err := json.Unmarshal([]byte(data), &result); err != nil {
		return result, fmt.Errorf("decode GitHub %s: %w", endpoint, err)
	}
	return result, nil
}

func endpoint(repo repository.Repository) string {
	parts := strings.Split(repo.Project, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return "repos/" + strings.Join(parts, "/") + "/actions"
}

func (c Client) runs(ctx context.Context, repo repository.Repository, exact bool) ([]run, error) {
	query := url.Values{"per_page": {"100"}}
	if exact {
		query.Set("head_sha", repo.SHA)
	}
	if repo.Branch != "" {
		query.Set("branch", repo.Branch)
	}
	var runs []run
	bytes := 0
	for page := 1; page <= 10; page++ {
		query.Set("page", strconv.Itoa(page))
		response, err := requestLimited[struct {
			Total int   `json:"total_count"`
			Runs  []run `json:"workflow_runs"`
		}](ctx, c, repo, endpoint(repo)+"/runs?"+query.Encode(), &bytes)
		if err != nil {
			return nil, err
		}
		runs = append(runs, response.Runs...)
		if !exact || len(response.Runs) < 100 || len(runs) >= response.Total {
			return runs, nil
		}
	}
	return nil, fmt.Errorf("GitHub workflow selection exceeds 1000 runs")
}

func state(status, conclusion string) (ci.Status, error) {
	switch status {
	case "in_progress":
		return ci.Running, nil
	case "queued", "pending":
		return ci.Pending, nil
	case "requested":
		return ci.Created, nil
	case "waiting":
		return ci.Waiting, nil
	case "completed":
		switch conclusion {
		case "success":
			return ci.Success, nil
		case "failure", "timed_out", "startup_failure":
			return ci.Failed, nil
		case "cancelled", "stale":
			return ci.Canceled, nil
		case "skipped":
			return ci.Skipped, nil
		case "neutral":
			return ci.Neutral, nil
		case "action_required":
			return ci.ActionRequired, nil
		}
	}
	return "", fmt.Errorf("unknown GitHub status %q / conclusion %q", status, conclusion)
}

func pipeline(r run) (ci.Pipeline, error) {
	status, err := state(r.Status, r.Conclusion)
	if err != nil {
		return ci.Pipeline{}, err
	}
	if r.ID <= 0 || r.WorkflowID <= 0 || r.SHA == "" {
		return ci.Pipeline{}, fmt.Errorf("GitHub returned an invalid workflow run")
	}
	p := ci.Pipeline{ID: r.ID, IID: r.Number, WorkflowID: r.WorkflowID, Name: r.Name, SHA: r.SHA, Ref: r.Branch, Source: r.Event, Status: status, WebURL: r.URL, StartedAt: r.StartedAt}
	if status.Finished() {
		p.FinishedAt = r.UpdatedAt
		if r.StartedAt != nil && r.UpdatedAt != nil {
			p.Duration = max(0, r.UpdatedAt.Sub(*r.StartedAt).Seconds())
		}
	}
	return p, nil
}

func (c Client) Load(ctx context.Context, repo repository.Repository, runID int64) (ci.Snapshot, error) {
	snapshot := ci.Snapshot{Provider: ci.GitHub, Repository: repo}
	if len(strings.Split(repo.Project, "/")) != 2 {
		return snapshot, fmt.Errorf("GitHub repository must use owner/repository")
	}
	runs, err := c.runs(ctx, repo, true)
	if err != nil {
		return snapshot, err
	}
	if len(runs) == 0 && repo.Branch != "" {
		runs, err = c.runs(ctx, repo, false)
		if err != nil {
			return snapshot, err
		}
	}
	slices.SortFunc(runs, func(a, b run) int {
		if a.ID > b.ID {
			return -1
		}
		if a.ID < b.ID {
			return 1
		}
		return 0
	})
	seen := make(map[int64]bool)
	for _, r := range runs {
		if seen[r.WorkflowID] {
			continue
		}
		p, err := pipeline(r)
		if err != nil {
			return snapshot, err
		}
		snapshot.Candidates = append(snapshot.Candidates, p)
		seen[r.WorkflowID] = true
	}
	if runID == 0 && len(snapshot.Candidates) == 0 {
		snapshot.Notice = "Для этой ветки и коммита нет запусков GitHub Actions"
		snapshot.UpdatedAt = time.Now()
		return snapshot, nil
	}
	if runID == 0 {
		runID = snapshot.Candidates[0].ID
	}
	r, err := request[run](ctx, c, repo, fmt.Sprintf("%s/runs/%d", endpoint(repo), runID))
	if err != nil {
		return snapshot, err
	}
	p, err := pipeline(r)
	if err != nil {
		return snapshot, err
	}
	snapshot.Pipeline = &p
	if p.SHA != repo.SHA {
		snapshot.Notice = "Другой коммит: этот workflow не проверяет локальный HEAD"
	}
	jobs, err := c.jobs(ctx, repo, runID)
	if err != nil {
		return snapshot, err
	}
	if len(jobs) > 0 {
		snapshot.Stages = []ci.Stage{{Name: "Jobs", Jobs: jobs}}
	}
	snapshot.UpdatedAt = time.Now()
	return snapshot, nil
}

func (c Client) jobs(ctx context.Context, repo repository.Repository, runID int64) ([]ci.Job, error) {
	var jobs []ci.Job
	bytes := 0
	for page := 1; page <= 100; page++ {
		response, err := requestLimited[struct {
			Jobs  []job `json:"jobs"`
			Total int   `json:"total_count"`
		}](ctx, c, repo, fmt.Sprintf("%s/runs/%d/jobs?filter=latest&per_page=100&page=%d", endpoint(repo), runID, page), &bytes)
		if err != nil {
			return nil, err
		}
		for _, item := range response.Jobs {
			if len(jobs) >= 2000 || len(item.Steps) > 1024 {
				return nil, fmt.Errorf("GitHub job/step limit exceeded")
			}
			status, err := state(item.Status, item.Conclusion)
			if err != nil {
				return nil, err
			}
			if item.ID <= 0 {
				return nil, fmt.Errorf("GitHub returned an invalid job")
			}
			j := ci.Job{ID: item.ID, Name: item.Name, Stage: "Jobs", Status: status, WebURL: item.URL, StartedAt: item.StartedAt, FinishedAt: item.CompletedAt}
			if item.StartedAt != nil {
				finish := time.Now()
				if item.CompletedAt != nil {
					finish = *item.CompletedAt
				}
				j.Duration = max(0, finish.Sub(*item.StartedAt).Seconds())
			}
			for _, s := range item.Steps {
				status, err := state(s.Status, s.Conclusion)
				if err != nil {
					return nil, err
				}
				j.Steps = append(j.Steps, ci.Step{Name: s.Name, Number: s.Number, Status: status})
			}
			jobs = append(jobs, j)
		}
		if len(response.Jobs) < 100 || len(jobs) >= response.Total {
			return jobs, nil
		}
	}
	return nil, fmt.Errorf("GitHub workflow exceeds 100 job pages")
}

func (c Client) Trace(ctx context.Context, repo repository.Repository, job ci.Job) (ci.Trace, error) {
	if err := ctx.Err(); err != nil {
		return ci.Trace{}, err
	}
	if !job.Status.Finished() {
		var text strings.Builder
		text.WriteString("GitHub Actions опубликует полный лог после завершения job.\nСостояние шагов обновляется автоматически.\n\n")
		for _, step := range job.Steps {
			fmt.Fprintf(&text, "%2d  %-14s %s\n", step.Number, step.Status, step.Name)
		}
		return ci.Trace{Text: text.String()}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	buffer := &process.TailBuffer{Limit: 1 << 20}
	args := []string{"api", "--hostname", repo.Host, "--method", "GET", fmt.Sprintf("%s/jobs/%d/logs", endpoint(repo), job.ID)}
	help, err := process.Output(ctx, repo.Directory, c.Executable, "api", "--help")
	if err != nil {
		return ci.Trace{}, fmt.Errorf("GitHub CLI log options: %w", err)
	}
	if strings.Contains(help, "--allow-escape-sequences") {
		args = append(args, "--allow-escape-sequences")
	}
	if err := process.Run(ctx, repo.Directory, c.Executable, args, buffer); err != nil {
		return ci.Trace{}, fmt.Errorf("GitHub job log: %w", err)
	}
	return ci.Trace{Text: string(buffer.Data), Truncated: buffer.Truncated}, nil
}
