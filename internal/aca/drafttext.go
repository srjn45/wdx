package aca

import "strings"

// draftText is the text of a box's lines, the first without its prompt mark:
// each line trimmed, the lines joined with \n, the blank ones at either end left
// out.
func draftText(lines []string, mark string) string {
	text := make([]string, len(lines))
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if i == 0 {
			line = strings.TrimSpace(strings.TrimPrefix(line, mark))
		}
		text[i] = line
	}
	return strings.Trim(strings.Join(text, "\n"), "\n")
}
