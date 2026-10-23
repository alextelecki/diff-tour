package render

import (
	"fmt"
	"slices"
	"strings"

	"github.com/init-co-jp/diff-tour/internal/git"
	"github.com/init-co-jp/diff-tour/internal/i18n"
	"github.com/init-co-jp/diff-tour/internal/text"
	"github.com/init-co-jp/diff-tour/internal/tour"
)

// kindStyle is how a flow node of each kind is drawn: border color and the tag after the label.
func kindStyle(kind string) (border, tag string) {
	switch kind {
	case "changed":
		return "93", i18n.T("flow.changed")
	case "new":
		return "92", i18n.T("flow.new")
	case "external":
		return "94", ""
	default:
		return "90", ""
	}
}

// renderFlow draws the nodes as boxes from top to bottom, joined by labeled arrows.
// The kind of a node (changed, new) is written into the top border of its box.
func renderFlow(f *tour.Flow, width int) []text.Line {
	// All boxes share one width so the arrows line up
	boxW := 0
	for _, n := range f.Nodes {
		_, tag := kindStyle(n.Kind)
		boxW = max(boxW, plainWidth(n.Label)+4, text.Width(tag)+8)
	}
	boxW = min(boxW, max(16, width*6/10))
	inner := boxW - 4
	center := boxW / 2

	var out []text.Line
	for i, n := range f.Nodes {
		if i > 0 {
			pad := strings.Repeat(" ", center)
			if n.Start {
				// A separate flow: a gap instead of an arrow
				out = append(out, nil, text.Line{{Text: strings.Repeat("┄", boxW), Style: "90"}}, nil)
			} else {
				arrow := text.Line{{Text: pad + "│", Style: "90"}}
				if next := f.Nodes[i-1].Next; next != "" {
					arrow = text.Concat(arrow, text.Line{{Text: " "}}, text.Fit(text.ParseInline(next, "36"), max(4, width-center-2), ""))
				}
				out = append(out, arrow, text.Line{{Text: pad + "▼", Style: "90"}})
			}
		}
		border, tag := kindStyle(n.Kind)
		labelStyle := "1"
		if n.Kind == "same" || n.Kind == "" {
			labelStyle = ""
		}
		label := text.Wrap(text.ParseInline(n.Label, labelStyle), inner)
		note := text.Wrap(text.ParseInline(n.Note, "90"), max(8, width-boxW-2))

		top := text.Line{{Text: "┌" + strings.Repeat("─", boxW-2) + "┐", Style: border}}
		if tag != "" {
			top = text.Line{{Text: "┌─ ", Style: border}, {Text: tag, Style: text.Join("1", border)}, {Text: " " + strings.Repeat("─", max(0, boxW-5-text.Width(tag))) + "┐", Style: border}}
		}
		rows := []text.Line{top}
		for _, l := range label {
			rows = append(rows, text.Concat(text.Line{{Text: "│ ", Style: border}}, text.Fit(l, inner, ""), text.Line{{Text: " │", Style: border}}))
		}
		rows = append(rows, text.Line{{Text: "└" + strings.Repeat("─", boxW-2) + "┘", Style: border}})
		// The note sits beside the box, starting on its first label line
		for k, l := range note {
			if k+1 < len(rows) {
				rows[k+1] = text.Concat(rows[k+1], text.Line{{Text: "  "}}, l)
			} else {
				rows = append(rows, text.Concat(text.Line{{Text: strings.Repeat(" ", boxW+2)}}, l))
			}
		}
		out = append(out, rows...)
	}
	for i := range out {
		out[i] = text.Fit(out[i], width, "")
	}
	return out
}

var markStyles = map[string]struct{ sym, style string }{
	"ok":   {"✔ ", "92"},
	"bad":  {"✘ ", "91"},
	"note": {"★ ", "93"},
}

// renderTimeline draws one column per lane with time going down. A message is an arrow from one lane to another.
func renderTimeline(tl *tour.Timeline, width int) []text.Line {
	n := len(tl.Lanes)
	const gutter = 3 // "12 " step numbers
	colW := max(8, (width-gutter)/n)
	mid := colW / 2
	lane := func(name string) int { return slices.Index(tl.Lanes, name) }

	// cells builds one line from per-lane contents; empty lanes show their lifeline
	row := func(num string, cells map[int]text.Line) text.Line {
		l := text.Line{{Text: fmt.Sprintf("%-*s", gutter, num), Style: "90"}}
		for c := range n {
			if cell, ok := cells[c]; ok {
				l = text.Concat(l, text.Fit(cell, colW, ""))
			} else {
				l = text.Concat(l, text.Line{{Text: strings.Repeat(" ", mid) + "│" + strings.Repeat(" ", colW-mid-1), Style: "90"}})
			}
		}
		return text.Fit(l, width, "")
	}

	var out []text.Line
	head := map[int]text.Line{}
	for c, name := range tl.Lanes {
		label := text.Fit(text.Line{{Text: name, Style: "1;97;100"}}, min(colW-1, text.Width(name)), "")
		left := max(0, min(colW-1-label.Width(), mid-label.Width()/2))
		head[c] = text.Concat(text.Line{{Text: strings.Repeat(" ", left)}}, label)
	}
	out = append(out, row("", head))

	for i, e := range tl.Events {
		num := fmt.Sprint(i + 1)
		from := lane(e.Lane)
		spans := text.ParseInline(e.Text, "")
		if m, ok := markStyles[e.Mark]; ok {
			spans = append([]text.Span{{Text: m.sym, Style: m.style}}, spans...)
		}
		out = append(out, row("", nil))
		if e.To == "" {
			// A note starts on its lane's lifeline and may run over the lanes to its right.
			// If there is too little room on the right (the last lane), it runs to the left and ends at the lifeline
			spanW := (n-from)*colW - mid - 3
			if leftW := from*colW + mid - 2; spanW < 20 && leftW > spanW {
				for k, l := range text.Wrap(spans, leftW) {
					label, tail := "", " │"
					if k == 0 {
						label, tail = num, " ●"
					}
					l = text.Concat(l, text.Line{{Text: tail, Style: "36"}})
					line := text.Concat(text.Line{{Text: strings.Repeat(" ", from*colW+mid+1-l.Width())}}, l)
					out = append(out, rowSpanning(row, label, 0, from, line, colW))
				}
				continue
			}
			for k, l := range text.Wrap(spans, max(8, spanW)) {
				label, lead := "", "│ "
				if k == 0 {
					label, lead = num, "● "
				}
				line := text.Concat(text.Line{{Text: strings.Repeat(" ", mid)}, {Text: lead, Style: "36"}}, l)
				out = append(out, rowSpanning(row, label, from, n-1, line, colW))
			}
			continue
		}
		// A message: an arrow from the sender's lifeline to the receiver's, with the text above it
		to := lane(e.To)
		lo, hi := min(from, to), max(from, to)
		spanW := (hi-lo)*colW + colW - 1
		wrapped := text.Wrap(spans, spanW-mid)
		for k, l := range wrapped {
			label := ""
			if k == 0 {
				label = num
			}
			// Text spans the lanes between the two ends, starting just right of the left lifeline
			line := text.Concat(text.Line{{Text: strings.Repeat(" ", mid) + "│ ", Style: "90"}}, l)
			full := text.Fit(line, (hi-lo+1)*colW, "")
			out = append(out, rowSpanning(row, label, lo, hi, full, colW))
		}
		arrowLen := (hi-lo)*colW - 1
		var arrow text.Line
		if to > from {
			arrow = text.Line{{Text: strings.Repeat(" ", mid) + "●" + strings.Repeat("─", max(0, arrowLen-1)) + "▶", Style: "36"}}
		} else {
			arrow = text.Line{{Text: strings.Repeat(" ", mid) + "◀" + strings.Repeat("─", max(0, arrowLen-1)) + "●", Style: "36"}}
		}
		out = append(out, rowSpanning(row, "", lo, hi, text.Fit(arrow, (hi-lo+1)*colW, ""), colW))
	}
	return out
}

// rowSpanning puts one line across lanes lo..hi and lifelines in the other lanes.
func rowSpanning(row func(string, map[int]text.Line) text.Line, num string, lo, hi int, content text.Line, colW int) text.Line {
	// Build the row with lifelines everywhere, then splice the content over lanes lo..hi
	base := row(num, nil)
	const gutter = 3
	start := gutter + lo*colW
	end := gutter + (hi+1)*colW
	return text.Concat(cutRight(base, start), text.Fit(content, end-start, ""), cutLeft(base, end))
}

// cutRight keeps the first n columns of a line, without the ellipsis Fit adds.
func cutRight(l text.Line, n int) text.Line {
	var out text.Line
	col := 0
	for _, s := range l {
		for _, r := range s.Text {
			w := text.RuneWidth(r)
			if col+w > n {
				return out
			}
			out = text.Append(out, string(r), s.Style)
			col += w
		}
	}
	return out
}

// cutLeft drops the first n columns of a line.
func cutLeft(l text.Line, n int) text.Line {
	var out text.Line
	col := 0
	for _, s := range l {
		for _, r := range s.Text {
			w := text.RuneWidth(r)
			if col >= n {
				out = text.Append(out, string(r), s.Style)
			}
			col += w
		}
	}
	return out
}

var roleOrder = []string{"core", "config", "test", "mechanical", "docs", ""}

// RoleLabel is the name shown for a file role.
func RoleLabel(role string) string {
	switch role {
	case "core":
		return i18n.T("role.core")
	case "mechanical":
		return i18n.T("role.mechanical")
	case "test":
		return i18n.T("role.test")
	case "config":
		return i18n.T("role.config")
	case "docs":
		return i18n.T("role.docs")
	}
	return ""
}

func roleStyle(role string) string {
	switch role {
	case "core":
		return "1;93"
	case "config":
		return "94"
	case "test":
		return "96"
	default:
		return "90"
	}
}

// renderFiles draws every changed file with a bar of its added and removed lines, grouped by role.
func renderFiles(m *tour.FileMap, width int, ctx Ctx) []text.Line {
	stats := ctx.Repo.FileStats(ctx.Tour.Base, ctx.Tour.Head)
	if len(stats) == 0 {
		return errorLines(i18n.T("err.noChanges"), width)
	}
	roles := map[string]tour.FileRole{}
	for _, r := range m.Roles {
		roles[r.File] = r
	}
	slices.SortStableFunc(stats, func(a, b git.FileStat) int {
		return slices.Index(roleOrder, roles[a.File].Role) - slices.Index(roleOrder, roles[b.File].Role)
	})

	most, nameW, roleW := 1, 0, 0
	for _, s := range stats {
		most = max(most, s.Insertions+s.Deletions)
		nameW = max(nameW, text.Width(s.File))
		if r := roles[s.File].Role; r != "" {
			roleW = max(roleW, text.Width(RoleLabel(r)))
		}
	}
	barW := min(20, max(6, width/5))
	nameW = min(nameW, max(12, width-barW-roleW-12))

	var out []text.Line
	for _, s := range stats {
		r := roles[s.File]
		style := roleStyle(r.Role)
		nameStyle := ""
		if r.Role == "mechanical" || r.Role == "docs" {
			nameStyle = "90"
		}
		if r.Role == "core" {
			nameStyle = "1"
		}
		add := (s.Insertions*barW + most - 1) / most
		del := (s.Deletions*barW + most - 1) / most
		mark := " "
		switch {
		case s.New:
			mark = "+"
		case s.Deleted:
			mark = "−"
		}
		l := text.Line{{Text: mark + " ", Style: "92"}}
		l = text.Concat(l, text.Fit(text.Line{{Text: s.File, Style: nameStyle}}, nameW, ""), text.Line{
			{Text: " "},
			{Text: strings.Repeat("█", add), Style: "92"},
			{Text: strings.Repeat("█", del), Style: "91"},
			{Text: strings.Repeat(" ", max(0, barW-add-del)+1)},
			{Text: fmt.Sprintf("+%-3d −%-3d ", s.Insertions, s.Deletions), Style: "90"},
		})
		if r.Role != "" {
			l = append(l, text.Span{Text: RoleLabel(r.Role), Style: style})
		}
		out = append(out, text.Fit(l, width, ""))
		if r.Note != "" {
			indent := text.Width(mark) + 1 + 2
			for _, nl := range text.Wrap(text.ParseInline(r.Note, "90"), max(8, width-indent)) {
				out = append(out, text.Concat(text.Line{{Text: strings.Repeat(" ", indent)}}, nl))
			}
		}
	}
	return out
}
