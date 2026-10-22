package tui

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

type logSearch struct {
	query   string
	total   int
	current int
	line    int
	column  int
}

type searchDirection int8

const (
	searchBackward     searchDirection = -1
	searchFromViewport searchDirection = 0
	searchForward      searchDirection = 1
)

func (m *Model) updateSearch() {
	previous := m.search
	m.search = logSearch{query: strings.ToLower(m.query)}
	if m.search.query == "" {
		return
	}
	for line, text := range m.lines {
		lower := strings.ToLower(text)
		count := strings.Count(lower, m.search.query)
		if previous.current > 0 && previous.query == m.search.query && previous.line == line {
			offset := 0
			for match := 1; match <= count; match++ {
				position := strings.Index(lower[offset:], m.search.query) + offset
				if position == previous.column {
					m.search.current = m.search.total + match
					m.search.line, m.search.column = line, position
					break
				}
				if position > previous.column {
					break
				}
				offset = position + len(m.search.query)
			}
		}
		m.search.total += count
	}
}

func (m *Model) find(direction searchDirection) {
	if m.search.query != strings.ToLower(m.query) {
		m.updateSearch()
	}
	if m.search.total == 0 {
		return
	}
	target := m.search.current
	if target > 0 && direction != 0 {
		target = (target-1+int(direction)+m.search.total)%m.search.total + 1
	} else {
		before := 0
		for line, text := range m.lines {
			if line > m.offset || (line == m.offset && direction >= 0) {
				break
			}
			before += strings.Count(strings.ToLower(text), m.search.query)
		}
		target = before + 1
		if direction < 0 {
			target = before
			if target == 0 {
				target = m.search.total
			}
		}
		if target > m.search.total {
			target = 1
		}
	}
	remaining := target
	for line, text := range m.lines {
		lower := strings.ToLower(text)
		count := strings.Count(lower, m.search.query)
		if remaining > count {
			remaining -= count
			continue
		}
		offset := 0
		for match := 0; match < remaining; match++ {
			offset += strings.Index(lower[offset:], m.search.query)
			if match+1 < remaining {
				offset += len(m.search.query)
			}
		}
		m.search.current, m.search.line, m.search.column = target, line, offset
		m.follow = false
		m.offset = line
		start, _ := m.searchSpan()
		m.horizontal = max(0, ansi.StringWidth(text[:start])-max(0, (m.width-12)/3))
		return
	}
}

func (m *Model) searchSpan() (int, int) {
	text := m.lines[m.search.line]
	return originalOffset(text, m.search.column), originalOffset(text, m.search.column+len(m.search.query))
}

func originalOffset(text string, lowerOffset int) int {
	position := 0
	for index, r := range text {
		if position >= lowerOffset {
			return index
		}
		position += utf8.RuneLen(unicode.ToLower(r))
	}
	return len(text)
}

func (m *Model) searchStatus() string {
	status := fmt.Sprintf("%d/%d", m.search.current, m.search.total)
	if m.search.total == 0 && !m.searching {
		return status + " · не найдено"
	}
	return status
}
