package tmux

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"tmux-gitlab-pipeline/internal/process"
)

func option(ctx context.Context, name, fallback string) (string, error) {
	value, err := process.Output(ctx, "", "tmux", "show-option", "-gqv", "@gitlab-pipeline-"+name)
	if err != nil {
		return "", err
	}
	if value == "" {
		value = fallback
	}
	return value, nil
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func Bind(ctx context.Context) error {
	key, err := option(ctx, "key", "G")
	if err != nil {
		return err
	}
	if !regexp.MustCompile(`^(?:[CM]-)*[A-Za-z0-9]+$`).MatchString(key) {
		return fmt.Errorf("invalid @gitlab-pipeline-key")
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	existing, err := process.Output(ctx, "", "tmux", "list-keys", "-T", "prefix", key)
	if err != nil && !process.ExitCode(err, 1) {
		return err
	}
	if existing != "" {
		note, err := process.Output(ctx, "", "tmux", "list-keys", "-N", "-T", "prefix", key)
		if err != nil && !process.ExitCode(err, 1) {
			return err
		}
		parts := strings.Fields(note)
		if len(parts) < 2 || parts[len(parts)-2] != key || parts[len(parts)-1] != "tmux-gitlab-pipeline" {
			return fmt.Errorf("prefix + %s is already bound; set @gitlab-pipeline-key to a free key", key)
		}
	}
	command := "exec " + shellQuote(strings.ReplaceAll(executable, "#", "##")) + " popup --pane #{q:pane_id} --client #{q:client_name}"
	_, err = process.Output(ctx, "", "tmux", "bind-key", "-N", "tmux-gitlab-pipeline", "-T", "prefix", key, "run-shell", "-b", command)
	if err != nil {
		return err
	}
	return bindStatus(ctx, executable)
}

func Popup(ctx context.Context, pane, client string) error {
	if !regexp.MustCompile(`^%[0-9]+$`).MatchString(pane) {
		return fmt.Errorf("invalid source pane")
	}
	directory, err := process.Output(ctx, "", "tmux", "display-message", "-p", "-t", pane, "#{pane_current_path}")
	if err != nil {
		return err
	}
	settings := map[string]string{"repo": directory, "width": "140", "height": "32", "theme": "auto", "icons": "unicode", "interval": "5s", "remote": "", "host": "", "provider": "auto", "motion": "on"}
	for name, fallback := range settings {
		value, err := option(ctx, name, fallback)
		if err != nil {
			return err
		}
		settings[name] = value
	}
	if settings["repo"] == "" {
		return fmt.Errorf("pane directory unavailable; set @gitlab-pipeline-repo")
	}
	selected, identity, chooseWorktree, err := paneDirectory(ctx, pane, directory, settings["repo"])
	if err != nil {
		return err
	}
	settings["repo"] = selected
	for _, name := range []string{"width", "height"} {
		value := settings[name]
		if !regexp.MustCompile(`^[0-9]+%?$`).MatchString(value) {
			return fmt.Errorf("invalid popup %s", name)
		}
		n, err := strconv.Atoi(strings.TrimSuffix(value, "%"))
		if err != nil || n < 1 || (strings.HasSuffix(value, "%") && n > 100) {
			return fmt.Errorf("invalid popup %s", name)
		}
	}
	if settings["motion"] != "on" && settings["motion"] != "off" {
		return fmt.Errorf("@gitlab-pipeline-motion must be on or off")
	}
	displayArgs := []string{"display-message", "-p", "-t", pane}
	if client != "" {
		displayArgs = append(displayArgs, "-c", client)
	}
	displayArgs = append(displayArgs, "#{client_width} #{client_height} #{client_termfeatures}")
	dimensions, err := process.Output(ctx, "", "tmux", displayArgs...)
	if err != nil {
		return err
	}
	sizes := strings.Fields(dimensions)
	if len(sizes) < 2 || len(sizes) > 3 {
		return fmt.Errorf("tmux client dimensions unavailable")
	}
	for i, name := range []string{"width", "height"} {
		available, err := strconv.Atoi(sizes[i])
		if err != nil || available < 3 {
			return fmt.Errorf("tmux client %s unavailable", name)
		}
		requested, err := strconv.Atoi(strings.TrimSuffix(settings[name], "%"))
		if err != nil {
			return fmt.Errorf("invalid popup %s: %w", name, err)
		}
		if strings.HasSuffix(settings[name], "%") {
			requested = available * requested / 100
		}
		settings[name] = strconv.Itoa(max(1, min(requested, available-2)))
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	command := []string{executable, "--repo", settings["repo"], "--theme", settings["theme"], "--icons", settings["icons"], "--interval", settings["interval"], "--remote", settings["remote"], "--host", settings["host"], "--provider", settings["provider"], "--source-pane", pane, "--pane-context", identity}
	if chooseWorktree {
		command = append(command, "--choose-worktree")
	}
	if settings["motion"] == "off" {
		command = append(command, "--no-motion")
	}
	if len(sizes) == 3 {
		for _, feature := range strings.Split(sizes[2], ",") {
			if feature == "RGB" {
				command = append(command, "--true-color")
				break
			}
		}
	}
	quoted := make([]string, len(command))
	for i, arg := range command {
		quoted[i] = shellQuote(arg)
	}
	args := []string{"display-popup", "-EE", "-t", pane, "-d", strings.ReplaceAll(settings["repo"], "#", "##"), "-w", settings["width"], "-h", settings["height"], "-b", "rounded", "-T", " CI pipelines "}
	if len(sizes) == 3 {
		for _, feature := range strings.Split(sizes[2], ",") {
			if feature == "RGB" {
				args = append(args, "-e", "COLORTERM=truecolor")
				break
			}
		}
	}
	if client != "" {
		args = append(args, "-c", client)
	}
	args = append(args, strings.Join(quoted, " "))
	return process.Run(ctx, "", "tmux", args, os.Stdout)
}
