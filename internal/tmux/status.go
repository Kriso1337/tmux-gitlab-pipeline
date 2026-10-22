package tmux

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"tmux-gitlab-pipeline/internal/process"
)

func bindStatus(ctx context.Context, executable string) error {
	enabled, err := option(ctx, "status", "on")
	if err != nil {
		return err
	}
	if enabled != "on" && enabled != "off" {
		return fmt.Errorf("@gitlab-pipeline-status must be on or off")
	}
	marker := "#{E:@gitlab-pipeline-status-format}"
	current, err := process.Output(ctx, "", "tmux", "show-option", "-gqv", "status-right")
	if err != nil {
		return err
	}
	if enabled == "off" {
		_, err = process.Output(ctx, "", "tmux", "set-option", "-g", "status-right", strings.ReplaceAll(current, " "+marker, ""))
		return err
	}
	format := "#(" + shellQuote(strings.ReplaceAll(executable, "#", "##")) + " status --pane #{q:pane_id})"
	if _, err := process.Output(ctx, "", "tmux", "set-option", "-g", "@gitlab-pipeline-status-format", format); err != nil {
		return err
	}
	if !strings.Contains(current, marker) {
		if _, err := process.Output(ctx, "", "tmux", "set-option", "-g", "status-right", current+" "+marker); err != nil {
			return err
		}
	}
	interval, err := process.Output(ctx, "", "tmux", "show-option", "-gqv", "status-interval")
	if err != nil {
		return err
	}
	n, err := strconv.Atoi(interval)
	if err != nil {
		return err
	}
	if n > 5 {
		_, err = process.Output(ctx, "", "tmux", "set-option", "-g", "status-interval", "5")
	}
	return err
}
