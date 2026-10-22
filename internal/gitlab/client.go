package gitlab

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

func (c Client) request(ctx context.Context, repo repository.Repository, endpoint string, fields map[string]string) ([]byte, error) {
	args := []string{"api", "--hostname", repo.Host, "--method", "GET", endpoint}
	if endpoint == "graphql" {
		args[4] = "POST"
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		args = append(args, "--raw-field", key+"="+fields[key])
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	output, err := process.Output(ctx, repo.Directory, c.Executable, args...)
	if err != nil {
		return nil, fmt.Errorf("GitLab %s: %w", endpoint, err)
	}
	return []byte(output), nil
}

func get[T any](ctx context.Context, c Client, repo repository.Repository, endpoint string) (T, error) {
	var value T
	data, err := c.request(ctx, repo, endpoint, nil)
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, fmt.Errorf("decode %s: %w", endpoint, err)
	}
	return value, nil
}

func list[T any](ctx context.Context, c Client, repo repository.Repository, endpoint string, query url.Values) ([]T, error) {
	if query == nil {
		query = url.Values{}
	}
	query.Set("per_page", "100")
	var items []T
	bytes := 0
	for page := 1; page <= 100; page++ {
		query.Set("page", strconv.Itoa(page))
		data, err := c.request(ctx, repo, endpoint+"?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		bytes += len(data)
		if bytes > 8<<20 {
			return nil, fmt.Errorf("%s exceeds 8 MiB", endpoint)
		}
		var batch []T
		if err := json.Unmarshal(data, &batch); err != nil {
			return nil, fmt.Errorf("decode %s: %w", endpoint, err)
		}
		items = append(items, batch...)
		if len(batch) < 100 {
			return items, nil
		}
	}
	return nil, fmt.Errorf("%s exceeds 100 pages", endpoint)
}

func projectEndpoint(repo repository.Repository) string {
	return "projects/" + url.PathEscape(repo.Project)
}

type mergeRequest struct {
	IID             int64     `json:"iid"`
	SHA             string    `json:"sha"`
	SourceBranch    string    `json:"source_branch"`
	SourceProjectID int64     `json:"source_project_id"`
	TargetProjectID int64     `json:"target_project_id"`
	HeadPipeline    *Pipeline `json:"head_pipeline"`
}

func (c Client) ChangeURL(ctx context.Context, repo repository.Repository) (string, error) {
	if repo.Branch == "" {
		return "", fmt.Errorf("detached HEAD: выберите worktree с веткой")
	}
	project, err := get[struct {
		ID int64 `json:"id"`
	}](ctx, c, repo, projectEndpoint(repo))
	if err != nil {
		return "", err
	}
	if project.ID <= 0 {
		return "", fmt.Errorf("GitLab returned an invalid project")
	}
	mrs, err := list[mergeRequest](ctx, c, repo, projectEndpoint(repo)+"/merge_requests", url.Values{"state": {"opened"}, "source_branch": {repo.Branch}, "order_by": {"updated_at"}, "sort": {"desc"}})
	if err != nil {
		return "", err
	}
	for _, mr := range mrs {
		if mr.SourceBranch == repo.Branch && mr.SourceProjectID == project.ID && mr.IID > 0 {
			address := url.URL{Scheme: "https", Host: repo.Host, Path: fmt.Sprintf("/%s/-/merge_requests/%d", repo.Project, mr.IID)}
			return address.String(), nil
		}
	}
	return "", fmt.Errorf("для ветки %s нет открытого MR", repo.Branch)
}

func (c Client) candidates(ctx context.Context, repo repository.Repository) ([]Pipeline, error) {
	base := projectEndpoint(repo)
	query := url.Values{"sha": {repo.SHA}, "per_page": {"20"}, "order_by": {"id"}, "sort": {"desc"}}
	if repo.Branch != "" {
		query.Set("ref", repo.Branch)
	}
	pipelines, err := get[[]Pipeline](ctx, c, repo, base+"/pipelines?"+query.Encode())
	if err != nil {
		return nil, err
	}
	if repo.Branch != "" {
		mrs, err := list[mergeRequest](ctx, c, repo, base+"/merge_requests", url.Values{"state": {"opened"}, "source_branch": {repo.Branch}})
		if err != nil {
			return nil, err
		}
		for _, mr := range mrs {
			if mr.SourceBranch != repo.Branch || mr.SHA != repo.SHA {
				continue
			}
			detail, err := get[mergeRequest](ctx, c, repo, fmt.Sprintf("%s/merge_requests/%d", base, mr.IID))
			if err != nil {
				return nil, err
			}
			if detail.SHA == repo.SHA && detail.HeadPipeline != nil {
				pipeline := *detail.HeadPipeline
				if pipeline.ProjectID != detail.TargetProjectID {
					continue
				}
				if pipeline.SHA != repo.SHA {
					current, err := c.currentMRPipeline(ctx, repo, mr.IID, pipeline)
					if err != nil {
						return nil, err
					}
					if !current {
						continue
					}
				}
				pipeline.MergeRequest, pipeline.SourceSHA = mr.IID, repo.SHA
				pipelines = append(pipelines, pipeline)
			}
		}
	}
	if len(pipelines) == 0 && repo.Branch != "" {
		query.Del("sha")
		pipelines, err = get[[]Pipeline](ctx, c, repo, base+"/pipelines?"+query.Encode())
		if err != nil {
			return nil, err
		}
	}
	slices.SortFunc(pipelines, func(a, b Pipeline) int {
		if a.ID > b.ID {
			return -1
		}
		if a.ID < b.ID {
			return 1
		}
		if a.MergeRequest > b.MergeRequest {
			return -1
		}
		if a.MergeRequest < b.MergeRequest {
			return 1
		}
		return 0
	})
	return slices.CompactFunc(pipelines, func(a, b Pipeline) bool { return a.ID == b.ID }), nil
}

func (c Client) currentMRPipeline(ctx context.Context, repo repository.Repository, iid int64, pipeline Pipeline) (bool, error) {
	query := `query($project: ID!, $mr: String!) { project(fullPath: $project) { mergeRequest(iid: $mr) { diffHeadSha headPipeline { id sha } } } }`
	data, err := c.request(ctx, repo, "graphql", map[string]string{"query": query, "project": repo.Project, "mr": strconv.FormatInt(iid, 10)})
	if err != nil {
		return false, err
	}
	var response struct {
		Data struct {
			Project *struct {
				MergeRequest *struct {
					DiffHeadSHA  string `json:"diffHeadSha"`
					HeadPipeline *struct {
						ID  string `json:"id"`
						SHA string `json:"sha"`
					} `json:"headPipeline"`
				} `json:"mergeRequest"`
			} `json:"project"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return false, fmt.Errorf("decode MR pipeline: %w", err)
	}
	if len(response.Errors) > 0 {
		return false, fmt.Errorf("GitLab MR pipeline: %s", response.Errors[0].Message)
	}
	if response.Data.Project == nil || response.Data.Project.MergeRequest == nil {
		return false, fmt.Errorf("GitLab MR pipeline is unavailable")
	}
	mr := response.Data.Project.MergeRequest
	head := mr.HeadPipeline
	return mr.DiffHeadSHA == repo.SHA && head != nil && head.SHA == pipeline.SHA && head.ID == fmt.Sprintf("gid://gitlab/Ci::Pipeline/%d", pipeline.ID), nil
}

func (c Client) Load(ctx context.Context, repo repository.Repository, pipelineID int64) (Snapshot, error) {
	snapshot := Snapshot{Provider: ci.GitLab, Repository: repo}
	candidates, err := c.candidates(ctx, repo)
	if err != nil {
		return snapshot, err
	}
	snapshot.Candidates = candidates
	if pipelineID == 0 && len(candidates) == 0 {
		snapshot.Notice = "Для этой ветки и коммита нет пайплайнов"
		snapshot.UpdatedAt = time.Now()
		return snapshot, nil
	}
	if pipelineID == 0 {
		pipelineID = candidates[0].ID
	}
	base := fmt.Sprintf("%s/pipelines/%d", projectEndpoint(repo), pipelineID)
	pipeline, err := get[Pipeline](ctx, c, repo, base)
	if err != nil {
		return snapshot, err
	}
	if pipeline.ID <= 0 || pipeline.IID <= 0 {
		return snapshot, fmt.Errorf("GitLab returned an invalid pipeline")
	}
	for _, candidate := range candidates {
		if candidate.ID == pipeline.ID {
			pipeline.MergeRequest, pipeline.SourceSHA = candidate.MergeRequest, candidate.SourceSHA
			break
		}
	}
	snapshot.Pipeline = &pipeline
	if pipeline.SHA != repo.SHA {
		if pipeline.SourceSHA == repo.SHA {
			snapshot.Notice = "MR pipeline: SHA результата слияния отличается от локального HEAD"
		} else {
			snapshot.Notice = "Другой коммит: этот пайплайн не проверяет локальный HEAD"
		}
	}
	jobs, err := list[Job](ctx, c, repo, base+"/jobs", url.Values{"include_retried": {"false"}})
	if err != nil {
		return snapshot, err
	}
	bridges, err := list[Job](ctx, c, repo, base+"/bridges", nil)
	if err != nil {
		return snapshot, err
	}
	for _, bridge := range bridges {
		bridge.Bridge = true
		jobs = append(jobs, bridge)
	}
	names, err := c.stages(ctx, repo, pipeline.IID)
	if err != nil {
		return snapshot, err
	}
	snapshot.Stages = GroupJobs(names, jobs)
	snapshot.UpdatedAt = time.Now()
	return snapshot, nil
}

func GroupJobs(names []string, jobs []Job) []Stage {
	byName := make(map[string][]Job)
	for _, job := range jobs {
		byName[job.Stage] = append(byName[job.Stage], job)
	}
	var extra []string
	for name := range byName {
		if !slices.Contains(names, name) {
			extra = append(extra, name)
		}
	}
	slices.Sort(extra)
	order := append(slices.Clone(names), extra...)
	stages := make([]Stage, 0, len(order))
	for _, name := range order {
		stageJobs := byName[name]
		if len(stageJobs) == 0 {
			continue
		}
		slices.SortFunc(stageJobs, func(a, b Job) int { return strings.Compare(a.Name, b.Name) })
		stages = append(stages, Stage{Name: name, Jobs: stageJobs})
	}
	return stages
}

type stagePage struct {
	Nodes []struct {
		Name string `json:"name"`
	} `json:"nodes"`
	PageInfo struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	} `json:"pageInfo"`
}

type stageResponse struct {
	Data struct {
		Project *struct {
			Pipeline *struct {
				Stages stagePage `json:"stages"`
			} `json:"pipeline"`
		} `json:"project"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (c Client) stages(ctx context.Context, repo repository.Repository, iid int64) ([]string, error) {
	query := `query($project: ID!, $iid: ID!, $cursor: String) { project(fullPath: $project) { pipeline(iid: $iid) { stages(first: 100, after: $cursor) { nodes { name } pageInfo { hasNextPage endCursor } } } } }`
	fields := map[string]string{"query": query, "project": repo.Project, "iid": strconv.FormatInt(iid, 10)}
	var names []string
	for range 100 {
		data, err := c.request(ctx, repo, "graphql", fields)
		if err != nil {
			return nil, err
		}
		var response stageResponse
		if err := json.Unmarshal(data, &response); err != nil {
			return nil, fmt.Errorf("decode stages: %w", err)
		}
		if len(response.Errors) > 0 {
			return nil, fmt.Errorf("GitLab stages: %s", response.Errors[0].Message)
		}
		if response.Data.Project == nil || response.Data.Project.Pipeline == nil {
			return nil, fmt.Errorf("pipeline stages are not accessible")
		}
		page := response.Data.Project.Pipeline.Stages
		for _, node := range page.Nodes {
			names = append(names, node.Name)
		}
		if !page.PageInfo.HasNextPage {
			return names, nil
		}
		if page.PageInfo.EndCursor == "" || page.PageInfo.EndCursor == fields["cursor"] {
			return nil, fmt.Errorf("GitLab returned a non-advancing stages cursor")
		}
		fields["cursor"] = page.PageInfo.EndCursor
	}
	return nil, fmt.Errorf("pipeline stages exceed 100 pages")
}

func (c Client) Trace(ctx context.Context, repo repository.Repository, job Job) (Trace, error) {
	if job.Bridge {
		return Trace{Text: "Trigger job: лог находится в downstream pipeline."}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	buffer := &process.TailBuffer{Limit: 1 << 20}
	endpoint := fmt.Sprintf("%s/jobs/%d/trace", projectEndpoint(repo), job.ID)
	if err := process.Run(ctx, repo.Directory, c.Executable, []string{"api", "--hostname", repo.Host, "--method", "GET", endpoint}, buffer); err != nil {
		return Trace{}, fmt.Errorf("job %d log: %w", job.ID, err)
	}
	return Trace{Text: string(buffer.Data), Truncated: buffer.Truncated}, nil
}
