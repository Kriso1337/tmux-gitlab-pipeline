package ci_test

import (
	"encoding/json"
	"testing"

	"tmux-gitlab-pipeline/internal/ci"
)

func TestSessionSerializationPreservesMRContextAndLiveJobSteps(t *testing.T) {
	want := ci.Snapshot{Provider: ci.GitHub, Pipeline: &ci.Pipeline{ID: 10, MergeRequest: 8, SourceSHA: "head"}, Stages: []ci.Stage{{Name: "build", Jobs: []ci.Job{{ID: 2, Bridge: true, Steps: []ci.Step{{Number: 1, Name: "Test", Status: ci.Running}}}}}}}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var actual ci.Snapshot
	if err := json.Unmarshal(data, &actual); err != nil {
		t.Fatal(err)
	}
	if actual.Pipeline.MergeRequest != 8 || actual.Pipeline.SourceSHA != "head" || !actual.Stages[0].Jobs[0].Bridge || len(actual.Stages[0].Jobs[0].Steps) != 1 || actual.Stages[0].Jobs[0].Steps[0].Status != ci.Running {
		t.Fatalf("snapshot=%+v", actual)
	}
}
