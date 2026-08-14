// diff.go: a minimal unified-diff renderer for RunInit's write preview,
// good enough for human-readable --dry-run/--force output. Not a byte-exact
// port of Python's difflib.unified_diff -- the format is informational
// preview text, not a machine-parsed contract.
package initproject

import (
	"fmt"
	"strings"
)

func unifiedDiff(existingText string, hasExisting bool, newText, destLabel string) string {
	oldText := ""
	if hasExisting {
		oldText = existingText
	}
	if oldText == newText {
		return ""
	}
	oldLines := splitLinesKeepEmpty(oldText)
	newLines := splitLinesKeepEmpty(newText)
	ops := lcsDiff(oldLines, newLines)

	var b strings.Builder
	fmt.Fprintf(&b, "--- %s (current)\n", destLabel)
	fmt.Fprintf(&b, "+++ %s (proposed)\n", destLabel)
	for _, op := range ops {
		switch op.kind {
		case ' ':
			// Context lines are omitted from this simplified renderer to
			// keep preview output focused on the actual change.
		case '-':
			b.WriteString("-" + op.line + "\n")
		case '+':
			b.WriteString("+" + op.line + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func splitLinesKeepEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

type diffOp struct {
	kind byte // ' ', '-', '+'
	line string
}

// lcsDiff computes a simple longest-common-subsequence line diff.
// Acceptable complexity for the config-file-sized inputs this package
// handles (dozens to low hundreds of lines).
func lcsDiff(a, b []string) []diffOp {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		if a[i] == b[j] {
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			ops = append(ops, diffOp{'-', a[i]})
			i++
		} else {
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}
