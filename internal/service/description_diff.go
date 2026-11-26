package service

import (
	"fmt"
	"strings"
)

const (
	// diffContext is how many unchanged lines a hunk shows around a change.
	diffContext = 2
	// maxMyersLines bounds the changed region myers works on; a larger one is
	// shown as removed and added whole (myers keeps a trace that grows with
	// the size of the change).
	maxMyersLines = 4000
)

// lineDiff is a unified diff of a and b, by line, with diffContext lines of
// context and without file headers. It is "" when a and b are equal.
func lineDiff(a, b string) string {
	ops := diffOps(splitLines(a), splitLines(b))
	var out strings.Builder
	for start := 0; start < len(ops); {
		// Find the next change, then the hunk around it.
		first := start
		for first < len(ops) && ops[first].kind == ' ' {
			first++
		}
		if first == len(ops) {
			break
		}
		lo := max(first-diffContext, start)
		hi := first
		for hi < len(ops) {
			if ops[hi].kind != ' ' {
				hi++
				continue
			}
			run := hi
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run == len(ops) || run-hi > 2*diffContext {
				hi = min(hi+diffContext, len(ops))
				break
			}
			hi = run
		}
		writeHunk(&out, ops[lo:hi])
		start = hi
	}
	return out.String()
}

// diffOp is one line of a diff: ' ' kept, '-' removed, '+' added. ai and bi
// are the line's numbers, from 1, in a and in b.
type diffOp struct {
	kind   byte
	line   string
	ai, bi int
}

func writeHunk(out *strings.Builder, ops []diffOp) {
	aStart, bStart, aLen, bLen := 0, 0, 0, 0
	for _, op := range ops {
		if op.kind != '+' {
			if aStart == 0 {
				aStart = op.ai
			}
			aLen++
		}
		if op.kind != '-' {
			if bStart == 0 {
				bStart = op.bi
			}
			bLen++
		}
	}
	// An empty side starts at the line before the change.
	if aLen == 0 {
		aStart = ops[0].ai
	}
	if bLen == 0 {
		bStart = ops[0].bi
	}
	fmt.Fprintf(out, "@@ -%d,%d +%d,%d @@\n", aStart, aLen, bStart, bLen)
	for _, op := range ops {
		out.WriteByte(op.kind)
		out.WriteString(op.line)
		out.WriteByte('\n')
	}
}

// splitLines splits s into lines without their line breaks.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// diffOps diffs x and y by line: the lines they share at the start and end
// are kept, and myers diffs the rest.
func diffOps(x, y []string) []diffOp {
	p := 0
	for p < len(x) && p < len(y) && x[p] == y[p] {
		p++
	}
	q := 0
	for q < len(x)-p && q < len(y)-p && x[len(x)-1-q] == y[len(y)-1-q] {
		q++
	}
	mx, my := x[p:len(x)-q], y[p:len(y)-q]
	var mid []diffOp
	if len(mx)+len(my) > maxMyersLines {
		for i, l := range mx {
			mid = append(mid, diffOp{kind: '-', line: l, ai: i + 1})
		}
		for j, l := range my {
			mid = append(mid, diffOp{kind: '+', line: l, ai: len(mx), bi: j + 1})
		}
	} else {
		mid = myers(mx, my)
	}
	ops := make([]diffOp, 0, p+len(mid)+q)
	for i := range p {
		ops = append(ops, diffOp{kind: ' ', line: x[i], ai: i + 1, bi: i + 1})
	}
	for _, op := range mid {
		op.ai += p
		op.bi += p
		ops = append(ops, op)
	}
	for i := range q {
		ops = append(ops, diffOp{kind: ' ', line: x[len(x)-q+i], ai: len(x) - q + i + 1, bi: len(y) - q + i + 1})
	}
	return ops
}

// myers returns the shortest edit script from x to y (Myers' O(ND)
// algorithm), as one op per line.
func myers(x, y []string) []diffOp {
	n, m := len(x), len(y)
	limit := n + m
	v := make([]int, 2*limit+2)
	var trace [][]int
	for d := 0; d <= limit; d++ {
		trace = append(trace, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			var i int
			if k == -d || (k != d && v[limit+k-1] < v[limit+k+1]) {
				i = v[limit+k+1]
			} else {
				i = v[limit+k-1] + 1
			}
			j := i - k
			for i < n && j < m && x[i] == y[j] {
				i++
				j++
			}
			v[limit+k] = i
			if i >= n && j >= m {
				return backtrack(trace, x, y, limit, d)
			}
		}
	}
	return nil
}

// backtrack walks the trace of myers back from (len(x), len(y)) to (0, 0).
func backtrack(trace [][]int, x, y []string, limit, d int) []diffOp {
	i, j := len(x), len(y)
	var rev []diffOp
	for ; d > 0; d-- {
		v := trace[d]
		k := i - j
		var prevK int
		if k == -d || (k != d && v[limit+k-1] < v[limit+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevI := v[limit+prevK]
		prevJ := prevI - prevK
		for i > prevI && j > prevJ {
			rev = append(rev, diffOp{kind: ' ', line: x[i-1], ai: i, bi: j})
			i--
			j--
		}
		if i == prevI {
			rev = append(rev, diffOp{kind: '+', line: y[j-1], ai: i, bi: j})
		} else {
			rev = append(rev, diffOp{kind: '-', line: x[i-1], ai: i, bi: j})
		}
		i, j = prevI, prevJ
	}
	for i > 0 && j > 0 {
		rev = append(rev, diffOp{kind: ' ', line: x[i-1], ai: i, bi: j})
		i--
		j--
	}
	ops := make([]diffOp, len(rev))
	for n, op := range rev {
		ops[len(rev)-1-n] = op
	}
	return ops
}
