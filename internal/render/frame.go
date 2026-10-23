// Package render turns a tour step into one terminal screen's worth of lines.
package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/init-co-jp/diff-tour/internal/i18n"
	"github.com/init-co-jp/diff-tour/internal/text"
	"github.com/init-co-jp/diff-tour/internal/tour"
)

const (
	pad = 2
	// at this width or wider, code and explanation are placed side by side
	sideBySideMinCols = 110
)

func stack(blocks []tour.Block, width int, ctx Ctx) []text.Line {
	var out []text.Line
	for i, b := range blocks {
		if i > 0 {
			out = append(out, nil)
		}
		out = append(out, Block(b, width, ctx)...)
	}
	return out
}

// naturalWidth is how wide a block is when it has all the room it wants.
func naturalWidth(b tour.Block, ctx Ctx) int {
	w := 0
	for _, l := range Block(b, 400, ctx) {
		w = max(w, text.TrimRight(l).Width())
	}
	return w
}

func spread(left, right text.Line, width int) text.Line {
	room := width - right.Width()
	if room < 8 {
		return text.Fit(left, width, "")
	}
	return text.Concat(text.Fit(left, room, ""), right)
}

// Location is the place `o` opens. It is nil if there is nothing to open.
func Location(step tour.FlatStep, ctx Ctx) *tour.Location {
	if step.Open != nil {
		return step.Open
	}
	for _, b := range step.Blocks {
		switch {
		case b.Code != nil:
			return &tour.Location{File: b.Code.File, Line: b.Code.Lines[0], At: b.Code.At}
		case b.Diff != nil:
			picked, errMsg := pickHunks(ctx, b.Diff)
			if errMsg != "" {
				return &tour.Location{File: b.Diff.File, Line: 1}
			}
			// The first changed line. Deletion-only lines have no line number in head, so use the number of the preceding line
			line := 1
		find:
			for _, h := range picked {
				for _, l := range h.Lines {
					if l.NewNo > 0 {
						line = l.NewNo
					}
					if l.Kind != ' ' {
						break find
					}
				}
			}
			return &tour.Location{File: b.Diff.File, Line: line}
		case b.Schema != nil:
			old := map[string]bool{}
			for _, f := range ctx.Repo.ListFiles(ctx.Ref("base"), b.Schema.Migrations) {
				old[f] = true
			}
			var files, added []string
			for _, f := range ctx.Repo.ListFiles(ctx.Tour.Head, b.Schema.Migrations) {
				if strings.HasSuffix(f, ".sql") {
					files = append(files, f)
					if !old[f] {
						added = append(added, f)
					}
				}
			}
			sort.Strings(files)
			sort.Strings(added)
			// The last migration added in this change; if none, the last one overall
			if len(added) > 0 {
				return &tour.Location{File: added[len(added)-1], Line: 1}
			}
			if len(files) > 0 {
				return &tour.Location{File: files[len(files)-1], Line: 1}
			}
		}
	}
	return nil
}

// Frame returns one screen's worth of lines and the maximum scroll amount.
func Frame(ctx Ctx, steps []tour.FlatStep, index, scroll, cols, rows int, message string) ([]text.Line, int) {
	step := steps[index]
	inner := max(20, cols-pad*2)

	header := []text.Line{
		spread(
			text.Line{{Text: " tour ", Style: "30;46"}, {Text: " " + ctx.Tour.Title, Style: "1"}},
			text.Line{{Text: i18n.T("chapter", step.Chapter+1, step.ChapterTitle) + " ", Style: "36"}},
			cols,
		),
		{{Text: strings.Repeat("─", cols), Style: "90"}},
	}

	titleSpans := []text.Span{{Text: step.Title, Style: "1"}}
	if step.Question {
		titleSpans = append([]text.Span{{Text: " " + i18n.T("question") + " ", Style: "30;43"}, {Text: " "}}, titleSpans...)
	}
	title := text.Wrap(titleSpans, inner)
	if step.Asked != "" {
		// An answer to the reader's own question: show the question first
		asked := text.Wrap([]text.Span{{Text: " " + i18n.T("asked") + " ", Style: "30;45"}, {Text: " " + step.Asked, Style: "35"}}, inner)
		title = append(asked, title...)
	}
	var hint []text.Line
	if step.Question {
		hint = []text.Line{nil, {{Text: i18n.T("think"), Style: "2;33"}}}
	}

	// Only the visual block(s) at the very start (or, if none, the first visual block) go on the left.
	// Everything else goes on the right in its original order, so the flow of the explanation isn't broken.
	// A timeline always needs the full width, so it never goes on the left
	onLeft := func(b tour.Block) bool { return b.IsVisual() && b.Timeline == nil }
	var visual, other []tour.Block
	leading := 0
	for leading < len(step.Blocks) && onLeft(step.Blocks[leading]) {
		leading++
	}
	if leading > 0 {
		visual, other = step.Blocks[:leading], step.Blocks[leading:]
	} else {
		taken := false
		for _, b := range step.Blocks {
			if !taken && onLeft(b) {
				visual, taken = append(visual, b), true
			} else {
				other = append(other, b)
			}
		}
	}

	body := append(append([]text.Line{}, title...), nil)
	columns := false
	if cols >= sideBySideMinCols && len(visual) > 0 && len(other) > 0 {
		// Size the left side to the code's actual width so code isn't cut off (within 45-65% of the total)
		natural := 0
		for _, b := range visual {
			natural = max(natural, naturalWidth(b, ctx))
		}
		leftW := max(inner*45/100, min(natural, inner*65/100))
		rightW := inner - leftW - 3
		// The right column holds the blocks up to the first one too wide for it (a timeline, wide code or table).
		// That block and everything after it go under the columns at full width, still in order
		cut := len(other)
		for i, b := range other {
			if b.Timeline != nil || ((b.IsVisual() || b.Table != nil) && naturalWidth(b, ctx) > rightW) {
				cut = i
				break
			}
		}
		if cut > 0 {
			columns = true
			rest := other[cut:]
			left := stack(visual, leftW, ctx)
			right := stack(other[:cut], rightW, ctx)
			if len(rest) == 0 {
				right = append(right, hint...)
			}
			for i := range max(len(left), len(right)) {
				var l, r text.Line
				if i < len(left) {
					l = left[i]
				}
				if i < len(right) {
					r = right[i]
				}
				body = append(body, text.Concat(text.Fit(l, leftW, ""), text.Line{{Text: " │ ", Style: "90"}}, text.Fit(r, rightW, "")))
			}
			if len(rest) > 0 {
				body = append(append(append(body, nil), stack(rest, inner, ctx)...), hint...)
			}
		}
	}
	if !columns {
		body = append(append(body, stack(step.Blocks, inner, ctx)...), hint...)
	}

	bodyH := max(1, rows-len(header)-2)
	maxScroll := max(0, len(body)-bodyH)
	top := min(scroll, maxScroll)
	visible := body[top:min(len(body), top+bodyH)]

	out := header
	for i := range bodyH {
		var l text.Line
		if i < len(visible) {
			l = visible[i]
		}
		out = append(out, text.Concat(text.Line{{Text: strings.Repeat(" ", pad)}}, text.Fit(l, inner, ""), text.Line{{Text: strings.Repeat(" ", max(0, cols-inner-pad))}}))
	}

	barW := max(10, cols-10)
	filled := ((index+1)*barW*2 + len(steps)) / (len(steps) * 2)
	out = append(out, text.Fit(text.Line{
		{Text: " "},
		{Text: strings.Repeat("━", filled), Style: "36"},
		{Text: strings.Repeat("─", barW-filled), Style: "90"},
		{Text: fmt.Sprintf(" %d/%d", index+1, len(steps)), Style: "90"},
	}, cols, ""))

	backStyle := ""
	if index == 0 {
		backStyle = "90"
	}
	keys := text.Line{{Text: " " + i18n.T("key.back"), Style: backStyle}, {Text: "   "}}
	if index == len(steps)-1 {
		keys = append(keys, text.Span{Text: i18n.T("key.last"), Style: "90"})
	} else {
		keys = append(keys, text.Span{Text: i18n.T("key.next"), Style: "1"})
	}
	if Location(step, ctx) != nil {
		keys = append(keys, text.Span{Text: "   " + i18n.T("key.open"), Style: "90"})
	}
	if ctx.CanAsk {
		keys = append(keys, text.Span{Text: "   " + i18n.T("key.ask"), Style: "35"})
	}
	keys = append(keys, text.Span{Text: "   " + i18n.T("key.quit"), Style: "90"})

	var right text.Line
	switch {
	case message != "":
		right = text.Line{{Text: message + " ", Style: "33"}}
	case maxScroll > 0 && top < maxScroll:
		right = text.Line{{Text: i18n.T("scroll.more") + " ", Style: "33"}}
	case maxScroll > 0:
		right = text.Line{{Text: i18n.T("scroll.top") + " ", Style: "33"}}
	}
	out = append(out, spread(keys, right, cols))
	return out, maxScroll
}
