package github_test

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

	"tmux-gitlab-pipeline/internal/ci"
	"tmux-gitlab-pipeline/internal/github"
	"tmux-gitlab-pipeline/internal/repository"
)

func fixture(t *testing.T, scenario string) (github.Client, repository.Repository) {
	t.Helper()
	t.Setenv("TGP_GH_HELPER", "1")
	t.Setenv("TGP_GH_SCENARIO", scenario)
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "gh")
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(executable, "'", "'\"'\"'") + "' -test.run=TestGitHubProcess -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return github.Client{Executable: path}, repository.Repository{Directory: dir, Host: "github.com", Project: "owner/repo", Branch: "feature/test", SHA: "local-sha"}
}

func TestGitHubProcess(t *testing.T) {
	if os.Getenv("TGP_GH_HELPER") != "1" {
		return
	}
	i := slices.Index(os.Args, "--")
	args := os.Args[i+1:]
	if len(args) == 2 && args[0] == "api" && args[1] == "--help" {
		if os.Getenv("TGP_GH_SCENARIO") == "escape-log" {
			if _, err := fmt.Fprintln(os.Stdout, "--allow-escape-sequences"); err != nil {
				os.Exit(2)
			}
		}
		os.Exit(0)
	}
	if (len(args) != 6 && len(args) != 7) || args[0] != "api" || args[1] != "--hostname" || args[2] != "github.com" || args[4] != "GET" {
		os.Exit(2)
	}
	scenario := os.Getenv("TGP_GH_SCENARIO")
	if scenario == "timeout" {
		time.Sleep(10 * time.Second)
	}
	if slices.Contains([]string{"401", "403", "404", "429"}, scenario) {
		fmt.Fprintln(os.Stderr, "HTTP", scenario)
		os.Exit(1)
	}
	endpoint := args[5]
	parsed, err := url.Parse(endpoint)
	if err != nil {
		os.Exit(2)
	}
	query := parsed.Query()
	result := ""
	switch {
	case strings.HasSuffix(parsed.Path, "/pulls"):
		if query.Get("head") != "owner:feature/test" || query.Get("state") != "open" {
			os.Exit(2)
		}
		result = `[{"number":8,"head":{"ref":"other","repo":{"full_name":"owner/repo"}}},{"number":7,"head":{"ref":"feature/test","repo":{"full_name":"owner/repo"}}}]`
		if scenario == "no-change" {
			result = `[]`
		}
	case strings.HasSuffix(parsed.Path, "/runs"):
		if query.Get("branch") != "feature/test" || query.Get("per_page") != "100" {
			os.Exit(2)
		}
		sha := "local-sha"
		if scenario == "fallback" {
			sha = "old-sha"
		}
		result = fmt.Sprintf(`{"total_count":3,"workflow_runs":[{"id":10,"workflow_id":1,"run_number":1,"name":"CI","head_sha":%q,"status":"completed","conclusion":"success"},{"id":20,"workflow_id":1,"run_number":2,"name":"CI","head_sha":%q,"status":"completed","conclusion":"success"},{"id":15,"workflow_id":2,"run_number":3,"name":"Security","head_sha":%q,"status":"completed","conclusion":"success"}]}`, sha, sha, sha)
		if scenario == "empty" || (scenario == "fallback" && query.Get("head_sha") != "") {
			result = `{"total_count":0,"workflow_runs":[]}`
		}
	case strings.HasSuffix(parsed.Path, "/jobs"):
		if query.Get("filter") != "latest" {
			os.Exit(2)
		}
		status, conclusion := "completed", "success"
		if scenario == "unknown" {
			conclusion = "new-unknown-conclusion"
		}
		if strings.HasPrefix(scenario, "conclusion-") {
			conclusion = strings.TrimPrefix(scenario, "conclusion-")
		}
		if scenario == "running" {
			status, conclusion = "in_progress", ""
		}
		one := fmt.Sprintf(`{"id":5,"name":"unit (linux)","status":%q,"conclusion":%q,"steps":[{"number":1,"name":"Checkout","status":"completed","conclusion":"success"},{"number":2,"name":"Test","status":%q,"conclusion":%q}]}`, status, conclusion, status, conclusion)
		if scenario == "pagination" && query.Get("page") == "1" {
			var items []json.RawMessage
			for range 100 {
				items = append(items, json.RawMessage(one))
			}
			data, err := json.Marshal(items)
			if err != nil {
				os.Exit(2)
			}
			result = `{"total_count":101,"jobs":` + string(data) + `}`
		} else {
			result = `{"total_count":1,"jobs":[` + one + `]}`
		}
	case strings.HasSuffix(parsed.Path, "/logs"):
		result = "line one\nline two\n"
		if scenario == "large-log" {
			result = strings.Repeat("x", (1<<20)+30) + "END"
		}
		if scenario == "escape-log" {
			if len(args) != 7 || args[6] != "--allow-escape-sequences" {
				os.Exit(2)
			}
			result = "\x1b[31mcolored output\x1b[0m\n"
		}
	case strings.Contains(parsed.Path, "/runs/"):
		sha := "local-sha"
		if scenario == "fallback" {
			sha = "old-sha"
		}
		result = fmt.Sprintf(`{"id":20,"workflow_id":1,"run_number":2,"name":"CI","head_sha":%q,"head_branch":"feature/test","status":"completed","conclusion":"success","event":"push","html_url":"https://github.com/owner/repo/actions/runs/20"}`, sha)
	default:
		os.Exit(2)
	}
	if scenario == "malformed" {
		result = `{bad json`
	}
	if _, err := fmt.Fprint(os.Stdout, result); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestLoadSelectsLatestRunPerWorkflowAndMapsJobSteps(t *testing.T) {
	c, repo := fixture(t, "normal")
	snapshot, err := c.Load(t.Context(), repo, 0)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Provider != ci.GitHub || snapshot.Pipeline.ID != 20 || snapshot.Pipeline.Name != "CI" || len(snapshot.Candidates) != 2 || snapshot.Candidates[1].Name != "Security" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	jobs := snapshot.Stages[0].Jobs
	if len(jobs) != 1 || jobs[0].Status != ci.Success || len(jobs[0].Steps) != 2 || jobs[0].Steps[1].Name != "Test" {
		t.Fatalf("jobs=%+v", jobs)
	}
}

func TestChangeURLUsesCurrentBranchAndReportsMissingPullRequest(t *testing.T) {
	c, repo := fixture(t, "normal")
	address, err := c.ChangeURL(t.Context(), repo)
	if err != nil || address != "https://github.com/owner/repo/pull/7" {
		t.Fatalf("address=%s error=%v", address, err)
	}
	for _, scenario := range []string{"no-change", "401", "403", "404", "429"} {
		c, repo := fixture(t, scenario)
		_, err := c.ChangeURL(t.Context(), repo)
		if err == nil || (scenario == "no-change" && !strings.Contains(err.Error(), "нет открытого PR")) || (scenario != "no-change" && !strings.Contains(err.Error(), "HTTP "+scenario)) {
			t.Fatalf("scenario=%s error=%v", scenario, err)
		}
	}
	repo.Branch = ""
	if _, err := c.ChangeURL(t.Context(), repo); err == nil || !strings.Contains(err.Error(), "detached HEAD") {
		t.Fatalf("error=%v", err)
	}
}

func TestLoadPaginatesJobsAndLabelsOldHEAD(t *testing.T) {
	for _, scenario := range []string{"pagination", "fallback", "empty"} {
		t.Run(scenario, func(t *testing.T) {
			c, repo := fixture(t, scenario)
			snapshot, err := c.Load(t.Context(), repo, 0)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "pagination" && len(snapshot.Stages[0].Jobs) != 101 {
				t.Fatal("jobs truncated")
			}
			if scenario == "fallback" && !strings.Contains(snapshot.Notice, "не проверяет локальный HEAD") {
				t.Fatal(snapshot.Notice)
			}
			if scenario == "empty" && (snapshot.Pipeline != nil || snapshot.Notice == "") {
				t.Fatal("missing empty state")
			}
		})
	}
}

func TestLoadReportsAPIAndDecodeErrors(t *testing.T) {
	for _, scenario := range []string{"401", "403", "404", "429", "malformed", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			c, repo := fixture(t, scenario)
			if _, err := c.Load(t.Context(), repo, 0); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	c, repo := fixture(t, "timeout")
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Load(ctx, repo, 0); err == nil {
		t.Fatal("expected cancellation")
	}
}

func TestTraceShowsLiveStepsAndBoundsCompletedLogs(t *testing.T) {
	c, repo := fixture(t, "normal")
	trace, err := c.Trace(t.Context(), repo, ci.Job{ID: 5, Status: ci.Running, Steps: []ci.Step{{Number: 1, Name: "Checkout", Status: ci.Success}}})
	if err != nil || !strings.Contains(trace.Text, "Checkout") || !strings.Contains(trace.Text, "после завершения") {
		t.Fatalf("trace=%+v error=%v", trace, err)
	}
	trace, err = c.Trace(t.Context(), repo, ci.Job{ID: 5, Status: ci.Success})
	if err != nil || trace.Text != "line one\nline two\n" {
		t.Fatalf("trace=%+v error=%v", trace, err)
	}
	c, repo = fixture(t, "large-log")
	trace, err = c.Trace(t.Context(), repo, ci.Job{ID: 5, Status: ci.Success})
	if err != nil || !trace.Truncated || len(trace.Text) != 1<<20 || !strings.HasSuffix(trace.Text, "END") {
		t.Fatalf("bytes=%d truncated=%t error=%v", len(trace.Text), trace.Truncated, err)
	}
	c, repo = fixture(t, "escape-log")
	trace, err = c.Trace(t.Context(), repo, ci.Job{ID: 5, Status: ci.Success})
	if err != nil || !strings.Contains(trace.Text, "colored output") {
		t.Fatalf("trace=%+v error=%v", trace, err)
	}
}

func TestGitHubConclusionsKeepTheirMeaning(t *testing.T) {
	for conclusion, want := range map[string]ci.Status{"failure": ci.Failed, "timed_out": ci.Failed, "cancelled": ci.Canceled, "skipped": ci.Skipped, "neutral": ci.Neutral, "action_required": ci.ActionRequired} {
		t.Run(conclusion, func(t *testing.T) {
			c, repo := fixture(t, "conclusion-"+conclusion)
			snapshot, err := c.Load(t.Context(), repo, 0)
			if err != nil {
				t.Fatal(err)
			}
			status := snapshot.Stages[0].Jobs[0].Status
			if status != want || !status.Finished() {
				t.Fatalf("status=%s want=%s", status, want)
			}
		})
	}
}
