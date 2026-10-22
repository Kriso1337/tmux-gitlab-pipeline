package gitlab_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"tmux-gitlab-pipeline/internal/gitlab"
	"tmux-gitlab-pipeline/internal/repository"
)

func fakeClient(t *testing.T, scenario string) (gitlab.Client, repository.Repository) {
	t.Helper()
	t.Setenv("TGP_HELPER", "1")
	t.Setenv("TGP_SCENARIO", scenario)
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "glab")
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(executable, "'", "'\"'\"'") + "' -test.run=TestGlabProcess -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return gitlab.Client{Executable: path}, repository.Repository{Directory: dir, Host: "gitlab.example.com", Project: "team/project", Branch: "feature/one", SHA: "local-sha"}
}

func TestGlabProcess(t *testing.T) {
	if os.Getenv("TGP_HELPER") != "1" {
		return
	}
	index := slices.Index(os.Args, "--")
	args := os.Args[index+1:]
	if len(args) < 6 || args[0] != "api" || args[1] != "--hostname" || args[2] != "gitlab.example.com" {
		fmt.Fprintln(os.Stderr, "invalid invocation")
		os.Exit(2)
	}
	scenario := os.Getenv("TGP_SCENARIO")
	if scenario == "timeout" {
		time.Sleep(10 * time.Second)
	}
	if slices.Contains([]string{"401", "403", "404", "429"}, scenario) {
		fmt.Fprintln(os.Stderr, "HTTP", scenario)
		os.Exit(1)
	}
	endpoint := args[5]
	fields := map[string]string{}
	for i := 6; i+1 < len(args); i += 2 {
		key, value, _ := strings.Cut(args[i+1], "=")
		fields[key] = value
	}
	var result string
	switch {
	case endpoint == "projects/team%2Fproject":
		result = `{"id":1}`
	case endpoint == "graphql":
		if fields["mr"] == "8" {
			result = `{"data":{"project":{"mergeRequest":{"diffHeadSha":"local-sha","headPipeline":{"id":"gid://gitlab/Ci::Pipeline/30","sha":"merge-sha"}}}}}`
			if scenario == "mr-stale" {
				result = `{"data":{"project":{"mergeRequest":{"diffHeadSha":"local-sha","headPipeline":null}}}}`
			}
			break
		}
		if args[4] != "POST" || fields["project"] != "team/project" || fields["iid"] != "2" {
			os.Exit(2)
		}
		if scenario == "graphql-error" {
			result = `{"errors":[{"message":"access denied"}]}`
			break
		}
		if scenario == "graphql-null" {
			result = `{"data":{"project":null}}`
			break
		}
		if scenario == "pagination" && fields["cursor"] == "" {
			result = `{"data":{"project":{"pipeline":{"stages":{"nodes":[{"name":"test"}],"pageInfo":{"hasNextPage":true,"endCursor":"next"}}}}}}`
			break
		}
		result = `{"data":{"project":{"pipeline":{"stages":{"nodes":[{"name":"build"},{"name":"test"},{"name":"deploy"}],"pageInfo":{"hasNextPage":false}}}}}}`
		if scenario == "pagination" {
			result = `{"data":{"project":{"pipeline":{"stages":{"nodes":[{"name":"build"},{"name":"deploy"}],"pageInfo":{"hasNextPage":false}}}}}}`
		}
	case strings.Contains(endpoint, "/merge_requests?"):
		result = `[]`
		if scenario == "mr" || scenario == "mr-stale" || scenario == "mr-fork" {
			result = `[{"iid":99,"source_branch":"feature/one","source_project_id":2},{"iid":8,"sha":"local-sha","source_branch":"feature/one","source_project_id":1}]`
		}
	case strings.HasSuffix(endpoint, "/merge_requests/8"):
		result = `{"iid":8,"sha":"local-sha","source_project_id":1,"target_project_id":1,"head_pipeline":{"id":30,"project_id":1,"sha":"merge-sha","ref":"refs/merge-requests/8/merge"}}`
		if scenario == "mr-fork" {
			result = `{"iid":8,"sha":"local-sha","source_project_id":2,"target_project_id":1,"head_pipeline":{"id":30,"project_id":2,"sha":"local-sha"}}`
		}
	case strings.Contains(endpoint, "/pipelines?"):
		result = `[{"id":20,"iid":2,"project_id":1,"sha":"local-sha","ref":"feature/one"}]`
		if scenario == "empty" {
			result = `[]`
		}
		if scenario == "fallback" || scenario == "mr-stale" {
			if strings.Contains(endpoint, "sha=") {
				result = `[]`
			} else {
				result = `[{"id":20,"iid":2,"project_id":1,"sha":"old-sha","ref":"feature/one"}]`
			}
		}
	case strings.Contains(endpoint, "/jobs?"):
		parsed, err := url.Parse(endpoint)
		if err != nil {
			os.Exit(2)
		}
		if parsed.Query().Get("include_retried") != "false" {
			os.Exit(2)
		}
		jobs := []gitlab.Job{{ID: 1, Name: "image", Stage: "build", Status: gitlab.Success}, {ID: 2, Name: "unit", Stage: "test", Status: gitlab.Running}}
		if scenario == "pagination" && parsed.Query().Get("page") == "1" {
			jobs = nil
			for i := range 100 {
				jobs = append(jobs, gitlab.Job{ID: int64(i + 10), Name: fmt.Sprintf("test-%03d", i), Stage: "test", Status: gitlab.Success})
			}
		}
		data, err := json.Marshal(jobs)
		if err != nil {
			os.Exit(2)
		}
		result = string(data)
	case strings.Contains(endpoint, "/bridges?"):
		result = `[{"id":99,"name":"downstream","stage":"deploy","status":"pending","downstream_pipeline":{"id":90}}]`
	case strings.HasSuffix(endpoint, "/trace"):
		if scenario == "large-log" {
			result = strings.Repeat("x", (1<<20)+50) + "END"
		} else {
			result = "line one\nline two\n"
		}
	case strings.Contains(endpoint, "/pipelines/"):
		sha := "local-sha"
		id := 20
		if scenario == "fallback" || scenario == "mr-stale" {
			sha = "old-sha"
		}
		if strings.HasSuffix(endpoint, "/30") {
			sha = "merge-sha"
			id = 30
		}
		result = fmt.Sprintf(`{"id":%d,"iid":2,"project_id":1,"sha":%q,"ref":"feature/one","status":"running","source":"push"}`, id, sha)
	default:
		fmt.Fprintln(os.Stderr, "unexpected endpoint", endpoint)
		os.Exit(2)
	}
	if _, err := fmt.Fprint(os.Stdout, result); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestLoadUsesGitLabStageOrderAndIncludesBridges(t *testing.T) {
	c, repo := fakeClient(t, "normal")
	snapshot, err := c.Load(t.Context(), repo, 0)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Pipeline.ID != 20 || len(snapshot.Stages) != 3 || snapshot.Stages[0].Name != "build" || !snapshot.Stages[2].Jobs[0].Bridge {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if snapshot.Notice != "" || snapshot.UpdatedAt.IsZero() {
		t.Fatalf("notice=%q updated=%s", snapshot.Notice, snapshot.UpdatedAt)
	}
}

func TestChangeURLUsesCurrentBranchAndDistinguishesMissingMRFromAPIFailure(t *testing.T) {
	c, repo := fakeClient(t, "mr")
	address, err := c.ChangeURL(t.Context(), repo)
	if err != nil || address != "https://gitlab.example.com/team/project/-/merge_requests/8" {
		t.Fatalf("address=%s error=%v", address, err)
	}
	for _, scenario := range []string{"normal", "401", "403", "404", "429"} {
		c, repo := fakeClient(t, scenario)
		_, err := c.ChangeURL(t.Context(), repo)
		if err == nil || (scenario == "normal" && !strings.Contains(err.Error(), "нет открытого MR")) || (scenario != "normal" && !strings.Contains(err.Error(), "HTTP "+scenario)) {
			t.Fatalf("scenario=%s error=%v", scenario, err)
		}
	}
	repo.Branch = ""
	if _, err := c.ChangeURL(t.Context(), repo); err == nil || !strings.Contains(err.Error(), "detached HEAD") {
		t.Fatalf("error=%v", err)
	}
}

func TestLoadReadsAllJobAndStagePages(t *testing.T) {
	c, repo := fakeClient(t, "pagination")
	snapshot, err := c.Load(t.Context(), repo, 0)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Stages[0].Name != "test" || len(snapshot.Stages[0].Jobs) != 101 {
		t.Fatalf("stages=%+v", snapshot.Stages)
	}
}

func TestLoadLabelsOldAndMergedResultCommits(t *testing.T) {
	for _, scenario := range []string{"fallback", "mr"} {
		t.Run(scenario, func(t *testing.T) {
			c, repo := fakeClient(t, scenario)
			snapshot, err := c.Load(t.Context(), repo, 0)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Notice == "" || snapshot.Pipeline.SHA == repo.SHA {
				t.Fatalf("snapshot=%+v", snapshot)
			}
			if scenario == "mr" && (snapshot.Pipeline.ID != 30 || snapshot.Pipeline.MergeRequest != 8 || snapshot.Pipeline.SourceSHA != repo.SHA) {
				t.Fatalf("pipeline=%+v", snapshot.Pipeline)
			}
		})
	}
}

func TestLoadDoesNotTreatStaleMRPipelineAsCurrentHEAD(t *testing.T) {
	c, repo := fakeClient(t, "mr-stale")
	snapshot, err := c.Load(t.Context(), repo, 0)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Pipeline.ID != 20 || snapshot.Pipeline.SourceSHA != "" || !strings.Contains(snapshot.Notice, "не проверяет локальный HEAD") {
		t.Fatalf("pipeline=%+v notice=%s", snapshot.Pipeline, snapshot.Notice)
	}
}

func TestLoadDoesNotFetchForeignForkPipelineFromTargetProject(t *testing.T) {
	c, repo := fakeClient(t, "mr-fork")
	snapshot, err := c.Load(t.Context(), repo, 0)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Pipeline.ID != 20 {
		t.Fatalf("pipeline=%+v", snapshot.Pipeline)
	}
}

func TestLoadDistinguishesNoPipelineFromAPIFailure(t *testing.T) {
	c, repo := fakeClient(t, "empty")
	snapshot, err := c.Load(t.Context(), repo, 0)
	if err != nil || snapshot.Pipeline != nil || snapshot.Notice == "" {
		t.Fatalf("snapshot=%+v error=%v", snapshot, err)
	}
	for _, scenario := range []string{"401", "403", "404", "429", "graphql-error", "graphql-null"} {
		t.Run(scenario, func(t *testing.T) {
			c, repo := fakeClient(t, scenario)
			if _, err := c.Load(t.Context(), repo, 0); err == nil {
				t.Fatal("expected API error")
			}
		})
	}
}

func TestLoadStopsWhenContextExpires(t *testing.T) {
	c, repo := fakeClient(t, "timeout")
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Load(ctx, repo, 0); err == nil {
		t.Fatal("expected timeout")
	}
}

func TestTracePreservesNewlinesAndBoundsMemory(t *testing.T) {
	for _, scenario := range []string{"normal", "large-log"} {
		t.Run(scenario, func(t *testing.T) {
			c, repo := fakeClient(t, scenario)
			trace, err := c.Trace(t.Context(), repo, gitlab.Job{ID: 2})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "normal" && trace.Text != "line one\nline two\n" {
				t.Fatalf("trace=%q", trace.Text)
			}
			if scenario == "large-log" && (!trace.Truncated || len(trace.Text) != 1<<20 || !strings.HasSuffix(trace.Text, "END")) {
				t.Fatalf("truncated=%t bytes=%d", trace.Truncated, len(trace.Text))
			}
		})
	}
}

func TestProgressExcludesManualJobsAndCountsTerminalStates(t *testing.T) {
	snapshot := gitlab.Snapshot{Stages: []gitlab.Stage{{Jobs: []gitlab.Job{{Status: gitlab.Success}, {Status: gitlab.Failed, AllowFailure: true}, {Status: gitlab.Canceled}, {Status: gitlab.Skipped}, {Status: gitlab.Manual}, {Status: gitlab.Running}}}}}
	done, total, manual := snapshot.Progress()
	if done != 4 || total != 5 || manual != 1 {
		t.Fatalf("progress=%d/%d manual=%d", done, total, manual)
	}
	if a, b, c := (gitlab.Snapshot{}).Progress(); a != 0 || b != 0 || c != 0 {
		t.Fatal("empty progress")
	}
}
