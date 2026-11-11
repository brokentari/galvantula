package runner

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/brokentari/galvantula/internal/event"
)

// maxDiffPairs is the most line pairs the diff compares. Past it the diff
// calls the whole middle changed, so a huge contract cannot use much memory.
const maxDiffPairs = 4_000_000

// invalidError is a decision the person must fix. Its text goes to the person as it is.
type invalidError string

func (e invalidError) Error() string { return string(e) }
func (invalidError) Unwrap() error   { return ErrInvalid }

// gateBrief says what a person must judge at the gate. It reads only the state.
func gateBrief(st *event.State, gate string) *event.GateBrief {
	prev := st.Nodes[gate].Brief
	b := &event.GateBrief{Round: 1, Check: []string{}}
	if prev != nil {
		b.Round = prev.Round + 1
	}
	g, _ := st.Graph.Node(gate)
	for _, id := range g.Deps {
		if c := st.Nodes[id].Contract; c != nil {
			b.Contract = c
			break
		}
	}
	if b.Contract != nil {
		contractBrief(b, prev, st.Nodes[gate].Sent)
		return b
	}
	for _, id := range g.Deps {
		if len(st.Nodes[id].Emitted) > 0 {
			b.Tasks = taskRows(st, st.Nodes[id].Emitted)
			break
		}
	}
	if b.Tasks != nil {
		b.Check = []string{"Check that each task owns the right packages and waits only for the tasks it needs."}
		var parts []string
		if n := st.Rails.Budget.NodeUSD; n > 0 {
			parts = append(parts, fmt.Sprintf("Each attempt of a task may spend up to $%.2f.", n))
		}
		if r := st.Rails.Budget.RunUSD; r > 0 {
			parts = append(parts, fmt.Sprintf("The run starts no new node after it has spent $%.2f; it has spent $%.2f.", r, st.CostUSD))
		}
		b.Approve = strings.Join(parts, " ")
	}
	return b
}

func contractBrief(b, prev *event.GateBrief, sent []event.GateComment) {
	c := b.Contract
	if c.Unclear != (event.LineRange{}) {
		b.Check = append(b.Check, `Read "Unclear in the spec" first. Each item is a choice the architect made where the spec does not say. Comment on a choice that is wrong.`)
	} else {
		b.Check = append(b.Check, "The contract names nothing unclear in the spec. Check that the spec leaves nothing open.")
	}
	b.Check = append(b.Check, "Check that the tests ask for what you want. Builders cannot change a test, and a wrong test fails the run.")
	if c.Red < c.Tests {
		b.Check = append(b.Check, fmt.Sprintf("%d of %d tests pass on the stubs. A test that passes on a stub can test nothing.", c.Tests-c.Red, c.Tests))
	}
	if c.Cut {
		b.Check = append(b.Check, "The gate shows the first 64 KB of the contract. Read the rest in CONTRACT.md in the run's repo.")
	}
	b.Approve = "Approve locks these tests: no builder can change them."
	if prev == nil || prev.Contract == nil {
		return
	}
	old, now := prev.Contract.Lines(), c.Lines()
	ch := &event.ContractChanges{Earlier: earlierComments(sent, old, now)}
	ch.Added, ch.Removed = diffLines(old, now)
	b.Changes = ch
	if len(ch.Added) == 0 && len(ch.Removed) == 0 {
		b.Check = append(b.Check, "The architect changed nothing in the contract.")
	}
}

// taskRows makes one row for each emitted node still in the graph, in the order of ids.
func taskRows(st *event.State, ids []string) []event.TaskRow {
	rows := []event.TaskRow{}
	for _, id := range ids {
		n, ok := st.Graph.Node(id)
		if !ok {
			continue
		}
		after := []string{}
		for _, d := range n.Deps {
			if slices.Contains(ids, d) {
				after = append(after, d)
			}
		}
		rows = append(rows, event.TaskRow{
			ID: id, Title: n.Title, Owns: slices.Concat([]string{}, n.Scope), After: after,
			Retries: n.Retries, CapUSD: st.Rails.Budget.NodeUSD * float64(1+n.Retries),
		})
	}
	return rows
}

// diffLines compares whole lines. It returns the lines of now that old lacks
// and the lines of old that now lacks, both never nil.
func diffLines(old, now []string) ([]int, []event.RemovedLine) {
	added, removed := []int{}, []event.RemovedLine{}
	pre := 0
	for pre < min(len(old), len(now)) && old[pre] == now[pre] {
		pre++
	}
	suf := 0
	for suf < min(len(old), len(now))-pre && old[len(old)-1-suf] == now[len(now)-1-suf] {
		suf++
	}
	a, b := old[pre:len(old)-suf], now[pre:len(now)-suf]
	n, m := len(a), len(b)
	if n*m > maxDiffPairs {
		for _, t := range a {
			removed = append(removed, event.RemovedLine{After: pre, Text: t})
		}
		for j := range m {
			added = append(added, pre+j+1)
		}
		return added, removed
	}
	// lcs[i][j] is the length of the longest common subsequence of a[i:] and b[j:].
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	for i, j := 0, 0; i < n || j < m; {
		switch {
		case i < n && j < m && a[i] == b[j]:
			i++
			j++
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			removed = append(removed, event.RemovedLine{After: pre + j, Text: a[i]})
			i++
		default:
			added = append(added, pre+j+1)
			j++
		}
	}
	return added, removed
}

// earlierComments finds where each comment of the last send-back now sits.
func earlierComments(sent []event.GateComment, old, now []string) []event.EarlierComment {
	out := []event.EarlierComment{}
	for _, c := range sent {
		e := event.EarlierComment{WasLine: c.Line, Quote: c.Quote, Body: c.Body}
		if end := max(c.EndLine, c.Line); c.Line >= 1 && end <= len(old) {
			e.Line = nearestRun(now, old[c.Line-1:end], c.Line)
		}
		out = append(out, e)
	}
	return out
}

// nearestRun returns the line where run starts in lines, nearest to near
// (the smaller line on a tie), or 0 if run is not there.
func nearestRun(lines, run []string, near int) int {
	best := 0
	for k := 0; k+len(run) <= len(lines); k++ {
		if !slices.Equal(lines[k:k+len(run)], run) {
			continue
		}
		if d := abs(k + 1 - near); best == 0 || d < abs(best-near) {
			best = k + 1
		}
	}
	return best
}

func abs(x int) int { return max(x, -x) }

// checkComments validates comments against the gate's contract and returns
// them with the quote set, in line order. It does not change comments.
func checkComments(comments []event.GateComment, c *event.Contract) ([]event.GateComment, error) {
	if len(comments) == 0 {
		return nil, nil
	}
	if c == nil {
		return nil, invalidError("this gate has no contract, so it takes no comments on lines")
	}
	if len(comments) > event.MaxComments {
		return nil, invalidError(fmt.Sprintf("%d comments is too many: the most is %d", len(comments), event.MaxComments))
	}
	lines := c.Lines()
	out := slices.Clone(comments)
	for i := range out {
		c := &out[i]
		fail := func(format string, a ...any) error {
			return invalidError(fmt.Sprintf("comment %d: ", i+1) + fmt.Sprintf(format, a...))
		}
		end := max(c.EndLine, c.Line)
		switch {
		case c.Line < 1:
			return nil, fail("line %d is not a line of the contract (lines count from 1)", c.Line)
		case c.Line > len(lines):
			return nil, fail("line %d is past the end of the contract (%d lines)", c.Line, len(lines))
		case c.EndLine != 0 && c.EndLine < c.Line:
			return nil, fail("end line %d is before line %d", c.EndLine, c.Line)
		case end > len(lines):
			return nil, fail("end line %d is past the end of the contract (%d lines)", c.EndLine, len(lines))
		case strings.TrimSpace(c.Body) == "":
			return nil, fail("the comment is empty")
		case len(c.Body) > event.MaxCommentBytes:
			return nil, fail("the comment is %d bytes long; the most is %d", len(c.Body), event.MaxCommentBytes)
		}
		q := strings.Join(lines[c.Line-1:end], "\n")
		if len(q) > event.MaxQuoteBytes {
			cut := event.MaxQuoteBytes
			for !utf8.RuneStart(q[cut]) {
				cut--
			}
			q = q[:cut]
		}
		c.Quote = q
	}
	slices.SortStableFunc(out, func(a, b event.GateComment) int { return a.Line - b.Line })
	return out, nil
}
