package github_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tmux-gitlab-pipeline/internal/ci"
	"tmux-gitlab-pipeline/internal/github"
	"tmux-gitlab-pipeline/internal/repository"
)

func featureClient(t *testing.T, response string) (github.Client, repository.Repository, string) {
	t.Helper()
	directory := t.TempDir()
	args := filepath.Join(directory, "arguments")
	data := filepath.Join(directory, "response")
	executable := filepath.Join(directory, "gh")
	t.Setenv("TGP_ARGS", args)
	t.Setenv("TGP_RESPONSE", data)
	if err := os.WriteFile(data, []byte(response), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TGP_ARGS\"\ncat \"$TGP_RESPONSE\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return github.Client{Executable: executable}, repository.Repository{Directory: directory, Host: "github.com", Project: "acme/payments", Branch: "feature/test"}, args
}
func TestGitHubRetryAndDispatchSendOnlyExplicitPostRequests(t *testing.T) {
	c, repo, args := featureClient(t, `{}`)
	for _, tc := range []struct {
		mutation ci.Mutation
		path     string
	}{{ci.Mutation{Action: ci.RetryJob, Job: ci.Job{ID: 7, Status: ci.Failed}}, "jobs/7/rerun"}, {ci.Mutation{Action: ci.RetryFailed, Pipeline: ci.Pipeline{ID: 9, Status: ci.Failed}}, "runs/9/rerun-failed-jobs"}, {ci.Mutation{Action: ci.DispatchWorkflow, Workflow: ci.Workflow{ID: 3}, Inputs: map[string]string{"environment": "stage"}}, "workflows/3/dispatches"}} {
		if err := c.Mutate(t.Context(), repo, tc.mutation); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(args)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "POST\nrepos/acme/payments/actions/"+tc.path) {
			t.Fatal(string(data))
		}
		if tc.mutation.Action == ci.DispatchWorkflow && !strings.Contains(string(data), "inputs[environment]=stage") {
			t.Fatal(string(data))
		}
	}
	if err := c.Mutate(t.Context(), repo, ci.Mutation{Action: ci.DispatchWorkflow, Workflow: ci.Workflow{ID: 3}, Inputs: map[string]string{"evil]": "x"}}); err == nil {
		t.Fatal("invalid input key accepted")
	}
}
func TestGitHubArtifactsExposeExpiryAndWorkflowsExcludeDisabled(t *testing.T) {
	c, repo, _ := featureClient(t, `{"total_count":2,"artifacts":[{"id":1,"name":"junit","size_in_bytes":100},{"id":2,"name":"old","expired":true}],"workflows":[{"id":3,"name":"CI","state":"active"},{"id":4,"state":"disabled_manually"}]}`)
	items, err := c.Artifacts(t.Context(), ci.Snapshot{Repository: repo, Pipeline: &ci.Pipeline{ID: 9}})
	if err != nil || len(items) != 2 || !items[1].Expired {
		t.Fatalf("items=%+v error=%v", items, err)
	}
	workflows, err := c.Workflows(t.Context(), repo)
	if err != nil || len(workflows) != 1 || workflows[0].ID != 3 {
		t.Fatalf("workflows=%+v error=%v", workflows, err)
	}
}
