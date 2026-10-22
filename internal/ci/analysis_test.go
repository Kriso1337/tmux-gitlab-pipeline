package ci

import (
	"strings"
	"testing"
)

func TestComparisonIncludesChangedAddedAndRemovedJobs(t *testing.T) {
	before := Snapshot{Stages: []Stage{{Jobs: []Job{{Name: "test", Stage: "verify", Status: Failed, Duration: 20}, {Name: "old", Stage: "build", Status: Success}}}}}
	after := Snapshot{Stages: []Stage{{Jobs: []Job{{Name: "test", Stage: "verify", Status: Success, Duration: 12}, {Name: "new", Stage: "build", Status: Success}}}}}
	result := Compare(before, after)
	if len(result.Jobs) != 3 {
		t.Fatalf("jobs=%+v", result.Jobs)
	}
	for _, job := range result.Jobs {
		if job.Name == "test" && (job.Before != Failed || job.After != Success || job.AfterDuration-job.BeforeDuration != -8) {
			t.Fatalf("job=%+v", job)
		}
	}
	if len(Compare(Snapshot{}, Snapshot{}).Jobs) != 0 {
		t.Fatal("empty comparison")
	}
}
func TestDiagnosisShowsErrorContextAndBoundsOutput(t *testing.T) {
	lines := Diagnose("setup\nbefore\nfatal: connection refused\nafter\ncleanup")
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, "before") || !strings.Contains(text, "fatal: connection refused") || !strings.Contains(text, "after") {
		t.Fatal(text)
	}
	if len(Diagnose(strings.Repeat("error\n", 10000))) > 60 {
		t.Fatal("unbounded diagnosis")
	}
	if !strings.Contains(strings.Join(Diagnose("ok"), " "), "не найдено") {
		t.Fatal("missing empty-state explanation")
	}
}
