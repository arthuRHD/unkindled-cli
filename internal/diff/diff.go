// Package diff renders small unified diffs for SSH config previews.
package diff

import (
	"fmt"
	"strings"
)

type opKind byte

const (
	opEqual  opKind = ' '
	opDelete opKind = '-'
	opInsert opKind = '+'
)

type op struct {
	kind opKind
	text string
}

// Unified returns a unified diff between oldText and newText. It returns an
// empty string when the inputs are identical.
func Unified(oldText, newText, oldName, newName string) string {
	if oldText == newText {
		return ""
	}
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	ops := diffLines(oldLines, newLines)

	var changed []int
	for i, o := range ops {
		if o.kind != opEqual {
			changed = append(changed, i)
		}
	}
	if len(changed) == 0 {
		return ""
	}

	const context = 3
	type hunk struct{ start, end int }
	var hunks []hunk
	for _, idx := range changed {
		start := idx - context
		if start < 0 {
			start = 0
		}
		end := idx + context + 1
		if end > len(ops) {
			end = len(ops)
		}
		if len(hunks) > 0 && start <= hunks[len(hunks)-1].end {
			if end > hunks[len(hunks)-1].end {
				hunks[len(hunks)-1].end = end
			}
			continue
		}
		hunks = append(hunks, hunk{start: start, end: end})
	}

	// Precompute the old/new line number at each operation boundary.
	oldLine := make([]int, len(ops)+1)
	newLine := make([]int, len(ops)+1)
	oldLine[0], newLine[0] = 1, 1
	for i, o := range ops {
		oldLine[i+1] = oldLine[i]
		newLine[i+1] = newLine[i]
		if o.kind != opInsert {
			oldLine[i+1]++
		}
		if o.kind != opDelete {
			newLine[i+1]++
		}
	}

	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", oldName, newName)
	for _, h := range hunks {
		oldCount := 0
		newCount := 0
		for _, o := range ops[h.start:h.end] {
			if o.kind != opInsert {
				oldCount++
			}
			if o.kind != opDelete {
				newCount++
			}
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", oldLine[h.start], oldCount, newLine[h.start], newCount)
		for _, o := range ops[h.start:h.end] {
			out.WriteByte(byte(o.kind))
			out.WriteString(o.text)
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func diffLines(a, b []string) []op {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	ops := make([]op, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{kind: opEqual, text: a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			ops = append(ops, op{kind: opDelete, text: a[i]})
			i++
		default:
			ops = append(ops, op{kind: opInsert, text: b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		ops = append(ops, op{kind: opDelete, text: a[i]})
	}
	for ; j < len(b); j++ {
		ops = append(ops, op{kind: opInsert, text: b[j]})
	}
	return ops
}

func splitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
