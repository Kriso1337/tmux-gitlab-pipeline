package gitlab_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tmux-gitlab-pipeline/internal/ci"
	"tmux-gitlab-pipeline/internal/gitlab"
	"tmux-gitlab-pipeline/internal/repository"
)

func featureClient(t *testing.T, response string) (gitlab.Client, repository.Repository, string) {
	t.Helper()
	directory := t.TempDir()
	args := filepath.Join(directory, "arguments")
	data := filepath.Join(directory, "response")
	executable := filepath.Join(directory, "glab")
	t.Setenv("TGP_ARGS", args)
	t.Setenv("TGP_RESPONSE", data)
	if err := os.WriteFile(data, []byte(response), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TGP_ARGS\"\ncat \"$TGP_RESPONSE\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return gitlab.Client{Executable: executable}, repository.Repository{Directory: directory, Host: "gitlab.example.com", Project: "acme/payments", Branch: "feature/test"}, args
}
func TestGitLabMutationsUsePostAndValidateJobState(t *testing.T) {
	c, repo, args := featureClient(t, `{}`)
	for _, tc := range []struct {
		mutation ci.Mutation
		path     string
	}{{ci.Mutation{Action: ci.RetryJob, Job: ci.Job{ID: 7, Status: ci.Failed}}, "jobs/7/retry"}, {ci.Mutation{Action: ci.PlayJob, Job: ci.Job{ID: 8, Status: ci.Manual}}, "jobs/8/play"}, {ci.Mutation{Action: ci.RetryFailed, Pipeline: ci.Pipeline{ID: 9, Status: ci.Failed}}, "pipelines/9/retry"}} {
		if err := c.Mutate(t.Context(), repo, tc.mutation); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(args)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "POST\nprojects/acme%2Fpayments/"+tc.path) {
			t.Fatal(string(data))
		}
	}
	if err := c.Mutate(t.Context(), repo, ci.Mutation{Action: ci.PlayJob, Job: ci.Job{ID: 7, Status: ci.Running}}); err == nil {
		t.Fatal("non-manual job played")
	}
}
func TestGitLabReportMapsFailuresAndCoverage(t *testing.T) {
	c, repo, _ := featureClient(t, `{"total_count":3,"success_count":1,"failed_count":1,"error_count":0,"skipped_count":1,"test_suites":[{"name":"unit","test_cases":[{"name":"fails","status":"failed","system_output":"expected 2"}]}]}`)
	report, err := c.Report(t.Context(), ci.Snapshot{Repository: repo, Pipeline: &ci.Pipeline{ID: 9, Coverage: "84.2"}})
	if err != nil || report.Total != 3 || report.Failed != 1 || report.Coverage == nil || *report.Coverage != 84.2 || len(report.Failures) != 1 {
		t.Fatalf("report=%+v error=%v", report, err)
	}
}
