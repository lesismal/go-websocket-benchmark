package report

import (
	"strings"
	"unicode/utf8"
)

// markdownTable is github.com/lesismal/perf Table.Markdown, measuring a cell
// by its characters rather than its bytes. perf's padding counts bytes, which
// comes to the same thing for ASCII, but puts a column out of line the moment
// a cell carries anything wider: the ↓1 and ↓2 on the rank columns' titles
// are three bytes each for one character. An ASCII table comes out the same
// byte for byte, and benchcli-uwscpp's markdownTable is the same code.
func markdownTable(title []string, rows [][]string) string {
	width := utf8.RuneCountInString
	columnNum := len(title)
	maxLen := make([]int, 0, columnNum)
	for _, v := range title {
		maxLen = append(maxLen, width(v))
	}

	all := make([][]string, 0, len(rows)+1)
	separator := make([]string, columnNum)
	for i := range separator {
		separator[i] = "---"
	}
	all = append(all, separator)
	for _, row := range rows {
		all = append(all, append([]string(nil), row...))
	}

	for _, row := range all {
		for j, cell := range row {
			if len(maxLen) < j+1 {
				maxLen = append(maxLen, width(cell))
			} else if width(cell) > maxLen[j] {
				maxLen[j] = width(cell)
			}
		}
		columnNum = max(columnNum, len(row))
	}
	title = append([]string(nil), title...)
	for len(title) < columnNum {
		title = append(title, "")
	}
	for i := range all {
		for len(all[i]) < columnNum {
			all[i] = append(all[i], "")
		}
	}
	for i := range maxLen {
		maxLen[i] += 2
	}

	var b strings.Builder
	b.WriteString("|")
	titleLeftPaddingIdx := 0
	for i, v := range title {
		aligned := padCell(v, maxLen[i], false, 0)
		if i == 0 {
			// The last character that is not a space, as perf has it.
			for k, r := range []rune(aligned) {
				if r != ' ' {
					titleLeftPaddingIdx = k
				}
			}
		}
		b.WriteString(aligned)
		b.WriteString("|")
	}
	b.WriteString("\n")
	for _, row := range all {
		b.WriteString("|")
		for j, cell := range row {
			b.WriteString(padCell(cell, maxLen[j], j == 0, titleLeftPaddingIdx))
			b.WriteString("|")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// padCell is perf's padding: the first column is pushed right no further than
// its title and padded on the right, every other cell is centred.
func padCell(s string, maxLen int, isFirst bool, titleLeftPaddingIdx int) string {
	paddingLen := maxLen - utf8.RuneCountInString(s)
	if paddingLen <= 0 {
		return s
	}
	if isFirst {
		for i := 0; i < paddingLen/2 && i < titleLeftPaddingIdx; i++ {
			s = " " + s
			paddingLen--
		}
		return s + strings.Repeat(" ", paddingLen)
	}
	half := paddingLen / 2
	return strings.Repeat(" ", half) + s + strings.Repeat(" ", paddingLen-half)
}
