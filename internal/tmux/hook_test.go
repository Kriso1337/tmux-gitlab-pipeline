package tmux_test

import (
	"strings"
	"testing"
	plugin "tmux-gitlab-pipeline/internal/tmux"
)

func TestHookReadsOnlyDeclaredDirectoryAndPrefersCwdChanged(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{`{"cwd":"/repo","transcript_path":"/never/read"}`, "/repo"}, {`{"cwd":"/repo","new_cwd":"/repo/worktree","hook_event_name":"CwdChanged"}`, "/repo/worktree"}} {
		directory, err := plugin.HookDirectory(strings.NewReader(tc.input))
		if err != nil || directory != tc.want {
			t.Fatalf("directory=%q error=%v", directory, err)
		}
	}
	for _, input := range []string{"", `{"cwd":"relative"}`, `{"cwd":"/repo","hook_event_name":"CwdChanged"}`, strings.Repeat("x", (64<<10)+1)} {
		if _, err := plugin.HookDirectory(strings.NewReader(input)); err == nil {
			t.Fatal("invalid hook accepted")
		}
	}
}
