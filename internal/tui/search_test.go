package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"tmux-gitlab-pipeline/internal/ci"
)

func searchLog(t *testing.T, text, query string) *Model {
	t.Helper()
	m := ready(t, &fixtureProvider{text: text})
	m.Update(press(m, tea.KeyEnter, "")())
	press(m, tea.KeyHome, "")
	press(m, '/', "/")
	for _, r := range query {
		press(m, r, string(r))
	}
	press(m, tea.KeyEnter, "")
	return m
}

func TestLogSearchNavigatesEveryOccurrenceAndWrapsBothWays(t *testing.T) {
	m := searchLog(t, "error ERROR error\nfine\nERROR", "error")
	for _, want := range []string{"1/4", "2/4", "3/4", "4/4", "1/4"} {
		if !strings.Contains(view(m), want) {
			t.Fatalf("missing %s: %s", want, view(m))
		}
		press(m, 'n', "n")
	}
	press(m, 'N', "N")
	if !strings.Contains(view(m), "1/4") {
		t.Fatal(view(m))
	}
	press(m, 'N', "N")
	if !strings.Contains(view(m), "4/4") || !strings.Contains(view(m), "строка 3") {
		t.Fatal(view(m))
	}
	m.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModShift})
	if !strings.Contains(view(m), "3/4") {
		t.Fatal(view(m))
	}
}

func TestLogSearchCountsLiteralUnicodeMatchesAndRevealsLongLine(t *testing.T) {
	text := "[ОШИБКА] " + strings.Repeat("ж", 180) + " [ошибка]"
	m := searchLog(t, text, "[ошибка]")
	if !strings.Contains(view(m), "1/2") {
		t.Fatal(view(m))
	}
	press(m, 'n', "n")
	if !strings.Contains(view(m), "2/2") || !strings.Contains(view(m), "[ошибка]") {
		t.Fatal(view(m))
	}
	press(m, 'N', "N")
	if !strings.Contains(view(m), "1/2") || !strings.Contains(view(m), "[ОШИБКА]") {
		t.Fatal(view(m))
	}
}

func TestLogSearchUpdatesCounterAndPreservesSelectionOnLiveRefresh(t *testing.T) {
	m := searchLog(t, "error one\nfine\nerror two", "error")
	press(m, 'n', "n")
	m.Update(traceMsg{generation: m.traceGeneration, trace: ci.Trace{Text: "error one\nfine\nerror two\nerror three"}})
	if !strings.Contains(view(m), "2/3") || !strings.Contains(view(m), "строка 3") {
		t.Fatal(view(m))
	}
	press(m, 'n', "n")
	if !strings.Contains(view(m), "3/3") || !strings.Contains(view(m), "строка 4") {
		t.Fatal(view(m))
	}
	m.Update(traceMsg{generation: m.traceGeneration, trace: ci.Trace{Text: "error one\nfine\nerror two\nerror three"}})
	press(m, 'n', "n")
	if !strings.Contains(view(m), "1/3") {
		t.Fatal(view(m))
	}
}

func TestLogSearchHandlesEmptyMissingAndReplacedResults(t *testing.T) {
	for _, text := range []string{"", "all fine"} {
		m := searchLog(t, text, "error")
		if !strings.Contains(view(m), "0/0") || !strings.Contains(view(m), "не найдено") {
			t.Fatal(view(m))
		}
		press(m, 'n', "n")
		press(m, 'N', "N")
		if !strings.Contains(view(m), "0/0") {
			t.Fatal(view(m))
		}
	}
	m := searchLog(t, "error", "error")
	m.Update(traceMsg{generation: m.traceGeneration, trace: ci.Trace{Text: "fine"}})
	if !strings.Contains(view(m), "0/0") {
		t.Fatal(view(m))
	}
	press(m, '/', "/")
	press(m, tea.KeyEnter, "")
	if strings.Contains(view(m), "0/0") || strings.Contains(view(m), "не найдено") {
		t.Fatal(view(m))
	}
}

func TestLogSearchHintsAndCounterStayVisibleInSmallWindows(t *testing.T) {
	for _, width := range []int{32, 40, 82, 140} {
		m := searchLog(t, "error error", "error")
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		content := view(m)
		for _, want := range []string{"1/2", "n", "Shift+N"} {
			if !strings.Contains(content, want) {
				t.Fatalf("width=%d missing %s: %s", width, want, content)
			}
		}
	}
}

func TestLogSearchCountsNonOverlappingMatchesAndCaseChangesByteLength(t *testing.T) {
	for _, tc := range []struct{ text, query, count string }{{"aaaaa", "aa", "2"}, {"KKİi", "k", "2"}, {"KKİi", "i", "2"}, {"[error]. [error].", "[error].", "2"}, {"error", "error", "1"}} {
		m := searchLog(t, tc.text, tc.query)
		if !strings.Contains(view(m), "1/"+tc.count) {
			t.Fatal(view(m))
		}
		press(m, 'N', "N")
		if !strings.Contains(view(m), tc.count+"/"+tc.count) {
			t.Fatal(view(m))
		}
		press(m, 'n', "n")
		if !strings.Contains(view(m), "1/"+tc.count) {
			t.Fatal(view(m))
		}
	}
}

func TestOpeningAnotherLogClearsSearchAndCounter(t *testing.T) {
	m := searchLog(t, "error", "error")
	press(m, tea.KeyEscape, "")
	m.Update(press(m, tea.KeyEnter, "")())
	if strings.Contains(view(m), "1/1") || strings.Contains(view(m), "/error") {
		t.Fatal(view(m))
	}
}
