package ci

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

func Compare(before, after Snapshot) Comparison {
	comparison := Comparison{UpdatedAt: time.Now()}
	if before.Pipeline != nil {
		comparison.Before = *before.Pipeline
	}
	if after.Pipeline != nil {
		comparison.After = *after.Pipeline
	}
	type key struct{ stage, name string }
	jobs := make(map[key]JobComparison)
	for _, stage := range before.Stages {
		for _, job := range stage.Jobs {
			k := key{job.Stage, job.Name}
			jobs[k] = JobComparison{Name: job.Name, Stage: job.Stage, Before: job.Status, BeforeDuration: job.Duration}
		}
	}
	for _, stage := range after.Stages {
		for _, job := range stage.Jobs {
			k := key{job.Stage, job.Name}
			row := jobs[k]
			row.Name, row.Stage, row.After, row.AfterDuration = job.Name, job.Stage, job.Status, job.Duration
			jobs[k] = row
		}
	}
	for _, row := range jobs {
		comparison.Jobs = append(comparison.Jobs, row)
	}
	slices.SortFunc(comparison.Jobs, func(a, b JobComparison) int {
		if n := strings.Compare(a.Stage, b.Stage); n != 0 {
			return n
		}
		return strings.Compare(a.Name, b.Name)
	})
	return comparison
}
func Diagnose(trace string) []string {
	lines := strings.Split(trace, "\n")
	selected := make(map[int]bool)
	matches := 0
	patterns := []string{"error", "failed", "failure", "fatal", "panic:", "exception", "assertion", "timed out", "exit code", "##[error]"}
	for i := len(lines) - 1; i >= 0 && matches < 12; i-- {
		lower := strings.ToLower(lines[i])
		matched := false
		for _, pattern := range patterns {
			if strings.Contains(lower, pattern) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		matches++
		for n := max(0, i-2); n <= min(len(lines)-1, i+2); n++ {
			selected[n] = true
		}
	}
	var result []string
	previous := -2
	for i, line := range lines {
		if !selected[i] {
			continue
		}
		if len(result) >= 60 {
			break
		}
		if i > previous+1 && len(result) > 0 {
			result = append(result, "…")
		}
		if len(line) > 1000 {
			line = line[:1000]
		}
		result = append(result, fmt.Sprintf("%d  %s", i+1, line))
		previous = i
	}
	if len(result) == 0 {
		return []string{"Явных сообщений об ошибке в последних строках лога не найдено.", "Enter — открыть полный доступный лог."}
	}
	return result
}
