package tmux_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tmux-gitlab-pipeline/internal/process"
	plugin "tmux-gitlab-pipeline/internal/tmux"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--repo" {
		directory, err := os.Getwd()
		if err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(os.Getenv("TGP_POPUP_REPORT"), []byte(directory), 0600); err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(os.Getenv("TGP_POPUP_REPORT")+".args", []byte(strings.Join(os.Args, "\n")), 0600); err != nil {
			os.Exit(2)
		}
		if os.Getenv("TGP_POPUP_FAIL") == "1" {
			fmt.Fprintln(os.Stderr, "popup startup failed")
			os.Exit(3)
		}
		var input [1]byte
		if _, err := os.Stdin.Read(input[:]); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func tmuxServer(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "tmux.sock")
	if _, err := process.Output(t.Context(), "", "tmux", "-S", socket, "-f", "/dev/null", "new-session", "-d", "-s", "test", "-x", "100", "-y", "28", "sleep", "60"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if output, err := exec.Command("tmux", "-S", socket, "kill-server").CombinedOutput(); err != nil {
			t.Errorf("cleanup: %s: %v", output, err)
		}
	})
	pid, err := process.Output(t.Context(), "", "tmux", "-S", socket, "display-message", "-p", "#{pid}")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", socket+","+pid+",0")
	return socket
}

func TestBindIsIdempotentAndPreservesExistingKeys(t *testing.T) {
	tmuxServer(t)
	if _, err := process.Output(t.Context(), "", "tmux", "bind-key", "g", "display-message", "existing"); err != nil {
		t.Fatal(err)
	}
	if err := plugin.Bind(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := plugin.Bind(t.Context()); err != nil {
		t.Fatalf("second bind: %v", err)
	}
	binding, err := process.Output(t.Context(), "", "tmux", "list-keys", "-T", "prefix", "G")
	if err != nil || !strings.Contains(binding, "popup") || !strings.Contains(binding, "pane_id") {
		t.Fatalf("binding=%s error=%v", binding, err)
	}
	if _, err := process.Output(t.Context(), "", "tmux", "set-option", "-g", "@gitlab-pipeline-key", "g"); err != nil {
		t.Fatal(err)
	}
	if err := plugin.Bind(t.Context()); err == nil {
		t.Fatal("expected conflict")
	}
	binding, err = process.Output(t.Context(), "", "tmux", "list-keys", "-T", "prefix", "g")
	if err != nil || !strings.Contains(binding, "existing") {
		t.Fatalf("binding=%s error=%v", binding, err)
	}
}

func TestPopupRejectsInvalidPaneAndSize(t *testing.T) {
	tmuxServer(t)
	if err := plugin.Popup(t.Context(), "$(touch unexpected)", ""); err == nil {
		t.Fatal("invalid pane accepted")
	}
	if _, err := process.Output(t.Context(), "", "tmux", "set-option", "-g", "@gitlab-pipeline-width", "101%"); err != nil {
		t.Fatal(err)
	}
	if err := plugin.Popup(t.Context(), "%0", ""); err == nil || !strings.Contains(err.Error(), "width") {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat("unexpected"); !os.IsNotExist(err) {
		t.Fatal("unexpected command execution")
	}
}

func TestPopupKeepsLiteralDirectoryAndPreservesStartupErrors(t *testing.T) {
	for _, tc := range []struct{ fails, rgb, saved, stale, agent, nested bool }{{}, {rgb: true}, {fails: true}, {saved: true}, {saved: true, stale: true}, {agent: true}, {agent: true, nested: true}} {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			base := t.TempDir()
			directory := filepath.Join(base, "quote ' #{pane_id} #(touch injected)")
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			t.Chdir(base)
			if tc.saved || tc.agent {
				for _, args := range [][]string{{"init", "-b", "master"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "fixture"}, {"worktree", "add", "-b", "feature/database-cleanup", directory}} {
					if _, err := process.Output(t.Context(), base, "git", args...); err != nil {
						t.Fatal(err)
					}
				}
			}
			report := filepath.Join(base, "report")
			t.Setenv("TGP_POPUP_REPORT", report)
			if tc.fails {
				t.Setenv("TGP_POPUP_FAIL", "1")
			}
			socket := tmuxServer(t)
			if tc.rgb {
				if _, err := process.Output(t.Context(), "", "tmux", "set-option", "-s", "terminal-features", "xterm*:RGB"); err != nil {
					t.Fatal(err)
				}
			}
			expectedDirectory := directory
			if !tc.saved && !tc.agent {
				if _, err := process.Output(t.Context(), "", "tmux", "set-option", "-g", "@gitlab-pipeline-repo", directory); err != nil {
					t.Fatal(err)
				}
			}
			if tc.agent {
				binary, err := os.ReadFile("/usr/bin/sleep")
				if err != nil {
					t.Fatal(err)
				}
				agent := filepath.Join(base, "claude")
				if err := os.WriteFile(agent, binary, 0700); err != nil {
					t.Fatal(err)
				}
				expectedDirectory = base
				if tc.nested {
					expectedDirectory = filepath.Join(base, "nested")
					if err := os.Mkdir(expectedDirectory, 0700); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := process.Output(t.Context(), "", "tmux", "respawn-pane", "-k", "-t", "%0", "-c", expectedDirectory, "exec '"+agent+"' 60"); err != nil {
					t.Fatal(err)
				}
				waitCtx, stopWait := context.WithTimeout(t.Context(), 3*time.Second)
				defer stopWait()
				waitFor(t, waitCtx, func() bool {
					name, err := process.Output(waitCtx, "", "tmux", "display-message", "-p", "-t", "%0", "#{pane_current_command}")
					return err == nil && name == "claude"
				})
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			client := exec.CommandContext(ctx, "script", "-q", "-c", "env -u TMUX tmux -S '"+socket+"' attach-session", "/dev/null")
			client.Env = append(os.Environ(), "TERM=xterm-256color")
			client.Stdout, client.Stderr = io.Discard, io.Discard
			input, err := client.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := client.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := input.Close(); err != nil {
					t.Error(err)
				}
				cancel()
				if err := client.Wait(); err != nil && ctx.Err() == nil {
					t.Error(err)
				}
			}()
			waitFor(t, ctx, func() bool {
				clients, err := process.Output(ctx, "", "tmux", "list-clients", "-F", "#{client_name}")
				return err == nil && clients != ""
			})
			if tc.saved {
				metadata, err := process.Output(ctx, "", "tmux", "display-message", "-p", "-t", "%0", "#{pane_pid}\n#{pane_current_path}")
				if err != nil {
					t.Fatal(err)
				}
				pid, cwd, _ := strings.Cut(metadata, "\n")
				group, err := process.Output(ctx, "", "ps", "-o", "tpgid=", "-p", pid)
				if err != nil {
					t.Fatal(err)
				}
				identity := pid + ":" + group + ":" + cwd
				if err := plugin.RememberWorktree(ctx, "%0", "old-process", directory); err == nil {
					t.Fatal("remembered a worktree for a replaced process")
				}
				if err := plugin.RememberWorktree(ctx, "%0", identity, directory); err != nil {
					t.Fatal(err)
				}
				if tc.stale {
					if _, err := process.Output(ctx, "", "tmux", "set-option", "-p", "-t", "%0", "@gitlab-pipeline-worktree", `{"owner":"old-process","directory":"/must-not-open"}`); err != nil {
						t.Fatal(err)
					}
					expectedDirectory = base
				}
			}
			done := make(chan error, 1)
			go func() { done <- plugin.Popup(ctx, "%0", "") }()
			waitFor(t, ctx, func() bool {
				_, err := os.Stat(report + ".args")
				return err == nil
			})
			actual, err := os.ReadFile(report)
			if err != nil || string(actual) != expectedDirectory {
				t.Fatalf("directory=%q error=%v", actual, err)
			}
			arguments, err := os.ReadFile(report + ".args")
			if err != nil || strings.Contains(string(arguments), "--true-color") != tc.rgb {
				t.Fatalf("arguments=%s error=%v", arguments, err)
			}
			if strings.Contains(string(arguments), "--choose-worktree") != tc.agent {
				t.Fatalf("unexpected worktree chooser: %s", arguments)
			}
			if _, err := os.Stat(filepath.Join(base, "injected")); !os.IsNotExist(err) {
				t.Fatal("tmux executed a directory format")
			}
			if tc.fails {
				select {
				case err := <-done:
					t.Fatalf("failed popup closed before Escape: %v", err)
				case <-time.After(300 * time.Millisecond):
				}
			}
			key := "q\n"
			if tc.fails {
				key = "\x1b"
			}
			if _, err := io.WriteString(input, key); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil && !tc.fails {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("popup did not close")
			}
		})
	}
}

func waitFor(t *testing.T, ctx context.Context, condition func() bool) {
	t.Helper()
	for !condition() {
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for terminal")
		case <-time.After(20 * time.Millisecond):
		}
	}
}
