package tui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

func clean(value string) string {
	value = ansi.Strip(strings.ToValidUTF8(value, "�"))
	value = strings.ReplaceAll(value, "\r\n", "\n")
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		if index := strings.LastIndexByte(line, '\r'); index >= 0 {
			line = line[index+1:]
		}
		lines[i] = strings.Map(func(r rune) rune {
			if r == '\t' {
				return r
			}
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				return -1
			}
			return r
		}, line)
	}
	return strings.ReplaceAll(strings.Join(lines, "\n"), "\t", "    ")
}

func oneLine(value string) string { return strings.ReplaceAll(clean(value), "\n", " ") }

func clip(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(value, width, "…")
}
