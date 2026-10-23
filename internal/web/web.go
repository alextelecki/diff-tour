// Package web turns a tour into one self-contained HTML page. The page reads the same git data as the TUI,
// draws flows and timelines with Mermaid, and makes no network requests.
package web

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/init-co-jp/diff-tour/internal/git"
	"github.com/init-co-jp/diff-tour/internal/highlight"
	"github.com/init-co-jp/diff-tour/internal/i18n"
	"github.com/init-co-jp/diff-tour/internal/render"
	"github.com/init-co-jp/diff-tour/internal/schema"
	"github.com/init-co-jp/diff-tour/internal/text"
	"github.com/init-co-jp/diff-tour/internal/tour"
)

var (
	//go:embed page.html
	pageSrc string
	//go:embed assets/tour.css
	css string
	//go:embed assets/tour.js
	js string
	// The version is in the file name so that updating Mermaid is a visible change.
	//
	//go:embed assets/mermaid-11.17.2.min.js
	mermaidJS string
)

var page = template.Must(template.New("page").Funcs(template.FuncMap{"inline": inline}).Parse(pageSrc))

// labels are the words of the page itself.
type labels struct {
	Back, Next, Contents, Question, Think, Reveal, Asked, AskTitle, Placeholder, Ask string
}

func newLabels() labels {
	return labels{
		Back:        i18n.T("key.back"),
		Next:        i18n.T("key.next"),
		Contents:    i18n.T("web.contents"),
		Question:    i18n.T("question"),
		Think:       i18n.T("think"),
		Reveal:      i18n.T("web.reveal"),
		Asked:       i18n.T("asked"),
		AskTitle:    i18n.T("ask.title"),
		Placeholder: i18n.T("ask.placeholder"),
		Ask:         i18n.T("web.ask"),
	}
}

type pageData struct {
	Lang     string
	Title    string
	Chapters []chapterLink
	Steps    []stepView
	Labels   labels
	// Token is set only when the page is served, and turns on the question form.
	Token     string
	Answering string
	CSS       template.CSS
	JS        template.JS
	Mermaid   template.JS
}

type chapterLink struct {
	Index int
	Title string
	// Step is the number of the chapter's first step (1-based, as in #k<n>).
	Step int
}

type stepView struct {
	tour.FlatStep
	N            int
	ChapterLabel string
	// Left and Right are side by side on a wide screen; Below follows at full width.
	Left, Right, Below []template.HTML
}

// Render returns the tour as one HTML page. The same tour and repository give the same bytes.
func Render(repo *git.Repo, t *tour.Tour, lang i18n.Lang) ([]byte, error) {
	return renderPage(repo, t, lang, "", "")
}

func renderPage(repo *git.Repo, t *tour.Tour, lang i18n.Lang, token, answering string) ([]byte, error) {
	steps := t.Steps()
	views, err := stepViews(render.Ctx{Tour: t, Repo: repo}, steps, 0)
	if err != nil {
		return nil, err
	}
	d := pageData{
		Lang: string(lang), Title: t.Title, Steps: views, Labels: newLabels(), Token: token, Answering: answering,
		CSS: template.CSS(css), JS: template.JS(js), Mermaid: template.JS(mermaidJS),
	}
	for i, s := range steps {
		if i == 0 || s.Chapter != steps[i-1].Chapter {
			d.Chapters = append(d.Chapters, chapterLink{Index: s.Chapter, Title: s.ChapterTitle, Step: i + 1})
		}
	}
	var b bytes.Buffer
	if err := page.Execute(&b, d); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// renderSteps returns the HTML of some steps alone, numbered from first (0-based), for adding them to a page.
func renderSteps(ctx render.Ctx, steps []tour.FlatStep, first int, token string) ([]byte, error) {
	views, err := stepViews(ctx, steps, first)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := page.ExecuteTemplate(&b, "steps", pageData{Steps: views, Token: token, Labels: newLabels()}); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func stepViews(ctx render.Ctx, steps []tour.FlatStep, first int) ([]stepView, error) {
	var out []stepView
	for i, s := range steps {
		v := stepView{FlatStep: s, N: first + i + 1, ChapterLabel: i18n.T("chapter", s.Chapter+1, s.ChapterTitle)}
		left, right, below := split(s.Blocks)
		for _, part := range []struct {
			blocks []tour.Block
			into   *[]template.HTML
		}{{left, &v.Left}, {right, &v.Right}, {below, &v.Below}} {
			for _, b := range part.blocks {
				h, err := blockHTML(b, ctx)
				if err != nil {
					return nil, err
				}
				*part.into = append(*part.into, h)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// split places the blocks as the TUI does: the visual block(s) at the very start (or, if none, the first visual
// block) on the left, the rest on the right in order, and from the first timeline on under both, at full width.
func split(blocks []tour.Block) (left, right, below []tour.Block) {
	onLeft := func(b tour.Block) bool { return b.IsVisual() && b.Timeline == nil }
	leading := 0
	for leading < len(blocks) && onLeft(blocks[leading]) {
		leading++
	}
	var other []tour.Block
	if leading > 0 {
		left, other = blocks[:leading], blocks[leading:]
	} else {
		for _, b := range blocks {
			if left == nil && onLeft(b) {
				left = []tour.Block{b}
			} else {
				other = append(other, b)
			}
		}
	}
	cut := slices.IndexFunc(other, func(b tour.Block) bool { return b.Timeline != nil })
	if cut < 0 {
		cut = len(other)
	}
	if len(left) == 0 || cut == 0 {
		return nil, nil, blocks
	}
	return left, other[:cut], other[cut:]
}

// inline turns **bold** and `code` into HTML; everything else is escaped.
func inline(s string) template.HTML {
	var b strings.Builder
	for _, sp := range text.ParseInline(s, "") {
		t := template.HTMLEscapeString(sp.Text)
		if strings.HasSuffix(sp.Style, "36") {
			t = "<code>" + t + "</code>"
		}
		if sp.Style == "1" || strings.HasPrefix(sp.Style, "1;") {
			t = "<strong>" + t + "</strong>"
		}
		b.WriteString(t)
	}
	return template.HTML(b.String())
}

// colored turns a line from highlight.Lines into HTML: each style becomes a class such as s35.
func colored(l text.Line) template.HTML {
	var b strings.Builder
	for _, sp := range l {
		if sp.Style == "" {
			b.WriteString(template.HTMLEscapeString(sp.Text))
			continue
		}
		fmt.Fprintf(&b, `<span class="s%s">%s</span>`, sp.Style, template.HTMLEscapeString(sp.Text))
	}
	return template.HTML(b.String())
}

var preMarks = map[rune]string{'✔': "ok", '✘': "bad", '★': "star", '✎': "edit"}

func pre(s string) template.HTML {
	var b strings.Builder
	for _, r := range s {
		if c, ok := preMarks[r]; ok {
			fmt.Fprintf(&b, `<span class="m-%s">%c</span>`, c, r)
			continue
		}
		b.WriteString(template.HTMLEscapeString(string(r)))
	}
	return template.HTML(b.String())
}

type pView struct {
	Text, Basis, Label string
}

type codeLine struct {
	No    int
	On    bool
	HTML  template.HTML
	Marks []string
}

type codeView struct {
	File, Detail, Note string
	Lines              []codeLine
}

type diffLine struct {
	No   int
	Kind string // "add", "del" or "" (context)
	HTML template.HTML
}

type diffView struct {
	File, Summary string
	Hunks         [][]diffLine
}

type snippetView struct {
	Tag, Label string
	Lines      []template.HTML
}

type rowsView struct {
	Tag, Label string
	Head       []rowsHead
	Data       [][]string
	Added      string
}

type rowsHead struct {
	Text  string
	Added bool
}

type schemaColumn struct {
	Marker, Class string
	Focus         bool
	Cells         []string
}

type schemaIndex struct {
	Marker, Class, Kind, Text string
}

type schemaTable struct {
	Name, Label, Class, Missing string
	Columns                     []schemaColumn
	Indexes                     []schemaIndex
}

type schemaView struct {
	Title, Desc string
	Head        []string
	Tables      []schemaTable
}

type fileView struct {
	git.FileStat
	Role, RoleLabel, Note, Mark string
	Add, Del                    int // bar widths in percent
}

type filesView struct {
	Files, Mechanical []fileView
	MechanicalLabel   string
}

type targetView struct {
	Head, Branched, Summary string
	Commits                 []git.Commit
}

// blockHTML renders one block with the template of its kind.
func blockHTML(b tour.Block, ctx render.Ctx) (template.HTML, error) {
	name, v := blockView(b, ctx)
	var buf bytes.Buffer
	if err := page.ExecuteTemplate(&buf, name, v); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}

func blockView(b tour.Block, ctx render.Ctx) (string, any) {
	switch {
	case b.P != nil:
		v := pView{Text: *b.P, Basis: string(b.Basis)}
		switch b.Basis {
		case tour.Fact:
			v.Label = i18n.T("basis.fact")
		case tour.Inferred:
			v.Label = i18n.T("basis.inferred")
		case tour.Opinion:
			v.Label = i18n.T("basis.opinion")
		}
		return "p", v
	case b.Quote != nil:
		return "quote", strings.Split(*b.Quote, "\n")
	case b.List != nil:
		return "list", b.List
	case b.Pre != nil:
		return "pre", pre(*b.Pre)
	case b.Table != nil:
		return "table", b.Table
	case b.Code != nil:
		return codeBlock(b.Code, ctx)
	case b.Diff != nil:
		return diffBlock(b.Diff, ctx)
	case b.Snippet != nil:
		v := snippetView{Tag: i18n.T("tag.example"), Label: b.Snippet.Label}
		for _, l := range highlight.Lines("", b.Snippet.Lang, strings.Split(b.Snippet.Text, "\n")) {
			v.Lines = append(v.Lines, colored(l))
		}
		return "snippet", v
	case b.Schema != nil:
		return schemaBlock(b.Schema, ctx)
	case b.Rows != nil:
		return "rows", rowsBlock(b.Rows)
	case b.Flow != nil:
		return "mermaid", flowSource(b.Flow)
	case b.Timeline != nil:
		return "mermaid", timelineSource(b.Timeline)
	case b.Files != nil:
		return filesBlock(b.Files, ctx)
	case b.Target:
		stat := ctx.Repo.Shortstat(ctx.Tour.Base, ctx.Tour.Head)
		commits := ctx.Repo.Commits(ctx.Tour.Base, ctx.Tour.Head)
		return "target", targetView{
			Head:     render.RefName(ctx.Repo, ctx.Tour.Head),
			Branched: strings.TrimSpace(i18n.T("target.branched", ctx.Tour.Base)),
			Summary:  i18n.T("target.summary", len(commits), stat.Files, stat.Insertions, stat.Deletions),
			Commits:  commits,
		}
	}
	return "error", "?"
}

func codeBlock(ref *tour.CodeRef, ctx render.Ctx) (string, any) {
	at := ctx.Ref(ref.At)
	src := ctx.Repo.Show(at, ref.File)
	if src == nil {
		return "error", i18n.T("err.fileNotFound", ref.File, at)
	}
	hl0, hl1 := ref.Lines[0], ref.Lines[1]
	if hl0 < 1 || hl1 > len(src) || hl0 > hl1 {
		return "error", i18n.T("err.lineRange", ref.File, hl0, hl1, len(src))
	}
	s0, s1 := max(1, hl0-2), min(len(src), hl1+2)
	if ref.Show != nil {
		s0, s1 = max(1, ref.Show[0]), min(len(src), ref.Show[1])
	}
	lines := highlight.Lines(ref.File, "", src)
	v := &codeView{File: ref.File, Detail: fmt.Sprintf("L%d–%d", hl0, hl1), Note: ref.Note}
	if hl0 == hl1 {
		v.Detail = fmt.Sprintf("L%d", hl0)
	}
	for n := s0; n <= s1; n++ {
		l := codeLine{No: n, On: n >= hl0 && n <= hl1, HTML: colored(lines[n-1])}
		for _, m := range ref.Marks {
			if m.Line == n {
				l.Marks = append(l.Marks, m.Text)
			}
		}
		v.Lines = append(v.Lines, l)
	}
	return "code", v
}

func diffBlock(ref *tour.DiffRef, ctx render.Ctx) (string, any) {
	picked, errMsg := render.PickHunks(ctx, ref)
	if errMsg != "" {
		return "error", errMsg
	}
	v := &diffView{File: ref.File}
	added, removed := 0, 0
	for _, h := range picked {
		src := make([]string, len(h.Lines))
		for i, l := range h.Lines {
			src[i] = l.Text
		}
		lines := highlight.Lines(ref.File, "", src)
		var hunk []diffLine
		for i, l := range h.Lines {
			d := diffLine{No: l.NewNo, HTML: colored(lines[i])}
			switch l.Kind {
			case '+':
				d.Kind = "add"
				added++
			case '-':
				d.Kind, d.No = "del", l.OldNo
				removed++
			}
			hunk = append(hunk, d)
		}
		v.Hunks = append(v.Hunks, hunk)
	}
	v.Summary = i18n.T("diff.summary", added, removed)
	return "diff", v
}

func change(c schema.Change) (label, class, marker string) {
	switch c {
	case schema.Added:
		return i18n.T("change.added"), "add", "+"
	case schema.Removed:
		return i18n.T("change.removed"), "del", "−"
	case schema.Changed:
		return i18n.T("change.changed"), "chg", "~"
	default:
		return i18n.T("change.same"), "", ""
	}
}

func schemaBlock(ref *tour.SchemaRef, ctx render.Ctx) (string, any) {
	before, err := schema.Load(ctx.Repo, ctx.Ref("base"), ref.Migrations)
	if err != nil {
		return "error", render.SchemaMessage(err)
	}
	after, err := schema.Load(ctx.Repo, ctx.Tour.Head, ref.Migrations)
	if err != nil {
		return "error", render.SchemaMessage(err)
	}
	names := ref.Tables
	if names == nil {
		for n := range before {
			if after[n] == nil {
				names = append(names, n)
			}
		}
		for n := range after {
			if schema.DiffTable(before[n], after[n]).Change != schema.Same {
				names = append(names, n)
			}
		}
		sort.Strings(names)
	}
	focus := map[string]bool{}
	for _, f := range ref.Focus {
		focus[f] = true
	}
	v := &schemaView{
		Title: i18n.T("schema.title"),
		Desc:  i18n.T("schema.desc", ref.Migrations, ctx.Tour.Base, render.RefName(ctx.Repo, ctx.Tour.Head)),
		Head:  []string{i18n.T("schema.column"), i18n.T("schema.type"), i18n.T("schema.constraints")},
	}
	for _, name := range names {
		if before[name] == nil && after[name] == nil {
			v.Tables = append(v.Tables, schemaTable{Missing: i18n.T("err.noTable", name)})
			continue
		}
		d := schema.DiffTable(before[name], after[name])
		label, class, _ := change(d.Change)
		if d.Change == schema.Added {
			label = i18n.T("schema.newTable")
		}
		t := schemaTable{Name: name, Label: label, Class: class}
		for _, col := range d.Columns {
			_, class, marker := change(col.Change)
			constraints := col.Constraints
			if constraints == "" {
				constraints = "—"
			}
			t.Columns = append(t.Columns, schemaColumn{Marker: marker, Class: class, Focus: focus[name+"."+col.Name],
				Cells: []string{col.Name, col.Type, constraints}})
		}
		for _, idx := range d.Indexes {
			_, class, marker := change(idx.Change)
			kind := i18n.T("schema.index")
			if idx.Unique {
				kind = i18n.T("schema.unique")
			}
			t.Indexes = append(t.Indexes, schemaIndex{Marker: marker, Class: class, Kind: kind,
				Text: fmt.Sprintf("%s (%s)", idx.Name, strings.Join(idx.Columns, ", "))})
		}
		v.Tables = append(v.Tables, t)
	}
	return "schema", v
}

func rowsBlock(r *tour.Rows) *rowsView {
	v := &rowsView{Tag: r.Tag, Label: r.Label, Data: r.Data}
	if v.Tag == "" {
		v.Tag = i18n.T("tag.example")
	}
	for _, h := range r.Head {
		added := slices.Contains(r.Added, h)
		v.Head = append(v.Head, rowsHead{h, added})
		if added {
			v.Added = i18n.T("rows.added")
		}
	}
	return v
}

func filesBlock(m *tour.FileMap, ctx render.Ctx) (string, any) {
	stats := ctx.Repo.FileStats(ctx.Tour.Base, ctx.Tour.Head)
	if len(stats) == 0 {
		return "error", i18n.T("err.noChanges")
	}
	roles := map[string]tour.FileRole{}
	for _, r := range m.Roles {
		roles[r.File] = r
	}
	slices.SortStableFunc(stats, func(a, b git.FileStat) int {
		return slices.Index(render.RoleOrder, roles[a.File].Role) - slices.Index(render.RoleOrder, roles[b.File].Role)
	})
	most := 1
	for _, s := range stats {
		most = max(most, s.Insertions+s.Deletions)
	}
	v := &filesView{MechanicalLabel: i18n.T("role.mechanical")}
	for _, s := range stats {
		r := roles[s.File]
		f := fileView{FileStat: s, Role: r.Role, Note: r.Note, Add: s.Insertions * 100 / most, Del: s.Deletions * 100 / most}
		if r.Role != "" {
			f.RoleLabel = render.RoleLabel(r.Role)
		}
		switch {
		case s.New:
			f.Mark = "+"
		case s.Deleted:
			f.Mark = "−"
		}
		if r.Role == "mechanical" {
			v.Mechanical = append(v.Mechanical, f)
		} else {
			v.Files = append(v.Files, f)
		}
	}
	v.MechanicalLabel += " · " + strconv.Itoa(len(v.Mechanical))
	return "files", v
}

// mermaidEscaper makes text safe inside Mermaid syntax, where html/template cannot help: the page escapes
// the diagram source as HTML, but Mermaid decodes it back. So characters that end a label or a statement
// (" | ; #), start a tag (< > &), a markdown string (`) or a directive (%%) become Mermaid's entity codes.
var mermaidEscaper = strings.NewReplacer(
	"#", "#35;", `"`, "#quot;", ";", "#59;", "|", "#124;", "`", "#96;", "%", "#37;",
	"&", "#amp;", "<", "#lt;", ">", "#gt;", "\r", "", "\n", "<br/>",
)

// mermaidText is text from the tour as Mermaid label text: inline markup dropped, then escaped.
func mermaidText(s string) string {
	var b strings.Builder
	for _, sp := range text.ParseInline(s, "") {
		b.WriteString(sp.Text)
	}
	return mermaidEscaper.Replace(b.String())
}

// flowSource is a flow as a Mermaid flowchart: nodes n0…nN, each joined to the one before unless it starts a new flow.
func flowSource(f *tour.Flow) string {
	var b strings.Builder
	b.WriteString("flowchart TD\n")
	for i, n := range f.Nodes {
		label := mermaidText(n.Label)
		switch n.Kind {
		case "changed":
			label = mermaidText(i18n.T("flow.changed")) + " · " + label
		case "new":
			label = mermaidText(i18n.T("flow.new")) + " · " + label
		}
		if n.Note != "" {
			label += "<br/>" + mermaidText(n.Note)
		}
		fmt.Fprintf(&b, "  n%d[\"%s\"]\n", i, label)
		if n.Kind != "" && n.Kind != "same" {
			fmt.Fprintf(&b, "  class n%d %s\n", i, n.Kind)
		}
		if i > 0 && !n.Start {
			if next := f.Nodes[i-1].Next; next != "" {
				fmt.Fprintf(&b, "  n%d -->|\"%s\"| n%d\n", i-1, mermaidText(next), i)
			} else {
				fmt.Fprintf(&b, "  n%d --> n%d\n", i-1, i)
			}
		}
	}
	b.WriteString("  classDef changed stroke:#d29922,stroke-width:2px\n")
	b.WriteString("  classDef new stroke:#2da44e,stroke-width:2px\n")
	b.WriteString("  classDef external stroke:#4c8dd6,stroke-dasharray:4 3\n")
	return b.String()
}

var timelineMarks = map[string]string{"ok": "✔ ", "bad": "✘ ", "note": "★ "}

// timelineSource is a timeline as a Mermaid sequence diagram: a lane is a participant, an event with to is
// a message, and one without is a note over its lane.
func timelineSource(tl *tour.Timeline) string {
	var b strings.Builder
	b.WriteString("sequenceDiagram\n")
	for i, l := range tl.Lanes {
		fmt.Fprintf(&b, "  participant p%d as %s\n", i, mermaidText(l))
	}
	for _, e := range tl.Events {
		msg := timelineMarks[e.Mark] + mermaidText(e.Text)
		from := slices.Index(tl.Lanes, e.Lane)
		if e.To == "" {
			fmt.Fprintf(&b, "  Note over p%d: %s\n", from, msg)
		} else {
			fmt.Fprintf(&b, "  p%d->>p%d: %s\n", from, slices.Index(tl.Lanes, e.To), msg)
		}
	}
	return b.String()
}
