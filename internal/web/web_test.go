package web

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/init-co-jp/diff-tour/internal/git"
	"github.com/init-co-jp/diff-tour/internal/i18n"
	"github.com/init-co-jp/diff-tour/internal/render"
	"github.com/init-co-jp/diff-tour/internal/schema"
	"github.com/init-co-jp/diff-tour/internal/tour"
)

var update = flag.Bool("update", false, "rewrite testdata/page.golden.html")

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	// Fixed names and dates, so the commit hashes on the page are the same on every run
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@example.com", "GIT_AUTHOR_DATE=2026-01-02T03:04:05Z", "GIT_COMMITTER_DATE=2026-01-02T03:04:05Z")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func commitFiles(t *testing.T, dir, message string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", message)
}

// testRepo has a main branch and a feature branch that the test tour walks through.
func testRepo(t *testing.T) *git.Repo {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	commitFiles(t, dir, "Add coupons", map[string]string{
		"coupon.go":               "package coupon\n\nfunc Use(code string) error {\n\treturn nil\n}\n",
		"migrations/001_init.sql": "CREATE TABLE coupons (id INTEGER PRIMARY KEY, code TEXT NOT NULL);\n",
	})
	gitIn(t, dir, "checkout", "-q", "-b", "feature")
	commitFiles(t, dir, "Use a coupon only once", map[string]string{
		"coupon.go":                 "package coupon\n\nfunc Use(code string) error {\n\tif used(code) {\n\t\treturn errUsed\n\t}\n\treturn nil\n}\n",
		"migrations/002_unique.sql": "ALTER TABLE coupons ADD COLUMN used_at TEXT;\nCREATE UNIQUE INDEX ix_coupons_code ON coupons (code);\n",
		"notes.txt":                 "notes\n",
	})
	return git.Open(dir)
}

func testTour(t *testing.T) *tour.Tour {
	tr, err := tour.Load("testdata/tour.json")
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// withoutMermaid swaps the 3 MB of Mermaid for its hash, so the golden file stays readable.
func withoutMermaid(page []byte) []byte {
	return bytes.Replace(page, []byte(mermaidJS), fmt.Appendf(nil, "/* mermaid sha256 %x */", sha256.Sum256([]byte(mermaidJS))), 1)
}

// TestGolden pins the page of a tour with every kind of block, and checks that rendering twice gives the same bytes.
func TestGolden(t *testing.T) {
	i18n.Set(i18n.PL)
	defer i18n.Set(i18n.EN)
	repo := testRepo(t)
	first, err := Render(repo, testTour(t), i18n.PL)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Render(repo, testTour(t), i18n.PL)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("two renders of the same tour differ")
	}
	got := withoutMermaid(first)
	if bytes.Equal(got, first) {
		t.Fatal("the page does not hold Mermaid")
	}
	const golden = "testdata/page.golden.html"
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("page differs from %s (run go test ./internal/web -update to accept)", golden)
	}
	for _, s := range []string{"class=\"code", "class=\"code diff", "class=\"schema", "class=\"files", "class=\"target",
		"class=\"rows", "class=\"code snippet", "<blockquote>", "<ul>", "<pre class=\"text\">", "<table>",
		"flowchart TD", "sequenceDiagram", "Sprawdzone", "Z kodu", "Zdanie seniora", "Przykład", "Pokaż odpowiedź"} {
		if !strings.Contains(string(got), s) {
			t.Errorf("page lacks %q", s)
		}
	}
	if strings.Contains(string(got), `<form class="ask">`) {
		t.Error("a page written to a file must not have the question form")
	}
}

// Text from the tour never becomes markup: html/template escapes it, and Mermaid gets entity codes.
func TestNoScriptFromTour(t *testing.T) {
	page, err := Render(testRepo(t), testTour(t), i18n.EN)
	if err != nil {
		t.Fatal(err)
	}
	page = withoutMermaid(page)
	for _, bad := range []string{"<script>alert", "<b>now", "<i>\n", "<tag>"} {
		if bytes.Contains(page, []byte(bad)) {
			t.Errorf("page contains %q", bad)
		}
	}
}

// Hostile text in every field the model writes. The page has only the template's three scripts, and the
// Mermaid sources hold nothing Mermaid would read back as a tag or a directive once it decodes entities.
func TestNoMarkupFromAnyField(t *testing.T) {
	const x = `</script><img src=x onerror=alert(1)><a href="javascript:alert(1)">j</a>]]>%%{init: {'theme':'forest'}}%%`
	p := func(s string) *string { return &s }
	tr := &tour.Tour{Version: tour.Version, Title: x, Base: "main", Head: "feature", Chapters: []tour.Chapter{{
		Title: x,
		Steps: []tour.Step{
			{Title: x, Question: true, Asks: []string{x}, Asked: x, Blocks: []tour.Block{{P: p(x), Basis: tour.Fact}}},
			{Title: x, Asks: []string{x}, Asked: x, Blocks: []tour.Block{
				{Quote: p(x + "\n" + x)}, {List: []string{x, "**" + x + "**", "`" + x + "`"}}, {Pre: p(x)},
				{Table: &tour.Table{Head: []string{x}, Rows: [][]string{{x}}}},
				{Code: &tour.CodeRef{File: "coupon.go", Lines: [2]int{4, 5}, Note: x, Marks: []tour.Mark{{Line: 5, Text: x}}}},
				{Snippet: &tour.Snippet{Label: x, Lang: "html", Text: x}},
				{Rows: &tour.Rows{Tag: x, Label: x, Head: []string{x}, Data: [][]string{{x}}, Added: []string{x}}},
				{Files: &tour.FileMap{Roles: []tour.FileRole{{File: "coupon.go", Role: "core", Note: x}}}},
				{Flow: &tour.Flow{Nodes: []tour.FlowNode{{Label: x, Note: x, Next: x}, {Label: x}}}},
				{Timeline: &tour.Timeline{Lanes: []string{x, "b"}, Events: []tour.TimelineEvent{{Lane: x, To: "b", Text: x}, {Lane: "b", Text: x}}}},
			}},
		},
	}}}
	if err := tr.Validate(); err != nil {
		t.Fatal(err)
	}
	repo := testRepo(t)
	i18n.Set(i18n.EN)
	page, err := renderPage(repo, tr, i18n.EN, "tok</script><b>", "llm</script><img src=x>")
	if err != nil {
		t.Fatal(err)
	}
	sources := regexp.MustCompile(`(?s)<pre class="mermaid">(.*?)</pre>`).FindAllSubmatch(page, -1)
	if len(sources) != 2 {
		t.Errorf("%d Mermaid sources, want 2", len(sources))
	}
	for _, m := range sources {
		src := html.UnescapeString(string(m[1]))
		for _, bad := range []string{"<img", "<a ", "</script", `"javascript`, "%%"} {
			if strings.Contains(src, bad) {
				t.Errorf("Mermaid source contains %q:\n%s", bad, src)
			}
		}
	}
	page = withoutMermaid(page)
	if n := bytes.Count(page, []byte("<script")); n != 3 {
		t.Errorf("%d <script tags, want 3", n)
	}
	for _, bad := range []string{"<img", `href="javascript:`, "</script><b>", "llm</script>"} {
		if bytes.Contains(page, []byte(bad)) {
			t.Errorf("page contains %q", bad)
		}
	}
	// The steps sent as an answer go through the same templates.
	frag, err := renderSteps(render.Ctx{Tour: tr, Repo: repo}, tr.Steps(), 0, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(frag, []byte("<img")) || bytes.Contains(frag, []byte("</script><")) {
		t.Error("the answer carries markup from the tour")
	}
}

// A schema block without tables walks maps; the page must still come out the same every time.
func TestRenderIsDeterministic(t *testing.T) {
	repo := testRepo(t)
	commitFiles(t, repo.Dir, "more tables", map[string]string{
		"migrations/003_more.sql": "CREATE TABLE a (id INTEGER);\nCREATE TABLE b (id INTEGER);\nCREATE TABLE c (id INTEGER);\nCREATE TABLE d (id INTEGER);\nCREATE INDEX ix_a1 ON a (id);\nCREATE INDEX ix_a2 ON a (id);\n",
	})
	tr := testTour(t)
	first, err := Render(repo, tr, i18n.EN)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		if again, _ := Render(repo, tr, i18n.EN); !bytes.Equal(first, again) {
			t.Fatalf("render %d differs", i)
		}
	}
}

// A reference the repository does not have is an error on the page, not a failed render. As in the TUI,
// a show range past the file and a missing migrations directory draw nothing.
func TestBrokenRefsShowErrors(t *testing.T) {
	tr := &tour.Tour{Version: tour.Version, Title: "t", Base: "main", Head: "feature", Chapters: []tour.Chapter{{Title: "c", Steps: []tour.Step{{
		Title: "s", Blocks: []tour.Block{
			{Code: &tour.CodeRef{File: "nope.go", Lines: [2]int{1, 1}}},
			{Code: &tour.CodeRef{File: "coupon.go", Lines: [2]int{1, 99}}},
			{Code: &tour.CodeRef{File: "coupon.go", Lines: [2]int{4, 4}, At: "base"}},
			{Code: &tour.CodeRef{File: "coupon.go", Lines: [2]int{1, 1}, Show: &[2]int{50, 60}}},
			{Diff: &tour.DiffRef{File: "coupon.go", Hunks: []int{9}}},
			{Diff: &tour.DiffRef{File: "nope.go"}},
			{Schema: &tour.SchemaRef{Migrations: "nowhere"}},
			{Schema: &tour.SchemaRef{Migrations: "migrations", Tables: []string{"ghost"}}},
		},
	}}}}}
	page, err := Render(testRepo(t), tr, i18n.EN)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if n := bytes.Count(page, []byte(`class="err"`)); n < 5 {
		t.Errorf("%d error blocks, want 5 (nope.go, lines 1-99, hunk 9, diff of nope.go, table ghost)", n)
	}
}

// The schema error on the page is the TUI's message; the template adds the ⚠ mark.
func TestSchemaMessage(t *testing.T) {
	i18n.Set(i18n.EN)
	err := &schema.MigrationError{File: "001.sql", Err: errors.New("near x: syntax error")}
	if got, want := render.SchemaMessage(err), i18n.T("err.migration", err.File, err.Err); got != want {
		t.Errorf("SchemaMessage = %q, want %q", got, want)
	}
}

func TestMermaidSettings(t *testing.T) {
	page, err := Render(testRepo(t), testTour(t), i18n.EN)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"securityLevel: 'strict'", "startOnLoad: false", "mermaid.run("} {
		if !bytes.Contains(page, []byte(s)) {
			t.Errorf("page lacks %q", s)
		}
	}
}

func TestMermaidText(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`say "hi"`, "say #quot;hi#quot;"},
		{"a; b", "a#59; b"},
		{"#1", "#35;1"},
		{"#59;", "#35;59#59;"},
		{"[a] (b) {c}", "[a] (b) {c}"},
		{"a | b", "a #124; b"},
		{"<script>alert(1)</script>", "#lt;script#gt;alert(1)#lt;/script#gt;"},
		{"a & b", "a #amp; b"},
		{"one\ntwo", "one<br/>two"},
		{"**bold** and `code`", "bold and code"},
		{"100%", "100#37;"},
		{"%%", "#37;#37;"},
		{"%%{init: {'theme':'forest'}}%%", "#37;#37;{init: {'theme':'forest'}}#37;#37;"},
	} {
		if got := mermaidText(c.in); got != c.want {
			t.Errorf("mermaidText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFlowSource(t *testing.T) {
	i18n.Set(i18n.EN)
	for _, c := range []struct {
		name  string
		flow  tour.Flow
		want  []string
		never []string
	}{
		{"edges", tour.Flow{Nodes: []tour.FlowNode{{Label: "a", Next: "calls"}, {Label: "b"}, {Label: "c"}}},
			[]string{`n0["a"]`, `n0 -->|"calls"| n1`, "n1 --> n2"}, nil},
		{"start", tour.Flow{Nodes: []tour.FlowNode{{Label: "a"}, {Label: "job", Start: true}}},
			[]string{`n1["job"]`}, []string{"-->"}},
		{"kinds", tour.Flow{Nodes: []tour.FlowNode{{Label: "a", Kind: "changed"}, {Label: "b", Kind: "new"}, {Label: "c", Kind: "external"}, {Label: "d", Kind: "same"}}},
			[]string{`n0["changed · a"]`, "class n0 changed", `n1["new · b"]`, "class n2 external"}, []string{"class n3"}},
		{"note", tour.Flow{Nodes: []tour.FlowNode{{Label: "a", Note: "x\ny"}}},
			[]string{`n0["a<br/>x<br/>y"]`}, nil},
		{"escaped", tour.Flow{Nodes: []tour.FlowNode{{Label: `"q"; #1 | <script>`, Next: "a|b"}, {Label: "b"}}},
			[]string{`n0["#quot;q#quot;#59; #35;1 #124; #lt;script#gt;"]`, `-->|"a#124;b"|`}, []string{"<script>"}},
	} {
		got := flowSource(&c.flow)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: lacks %q:\n%s", c.name, w, got)
			}
		}
		for _, n := range c.never {
			if strings.Contains(got, n) {
				t.Errorf("%s: has %q:\n%s", c.name, n, got)
			}
		}
	}
}

func TestTimelineSource(t *testing.T) {
	tl := &tour.Timeline{Lanes: []string{"A; x", "DB"}, Events: []tour.TimelineEvent{
		{Lane: "A; x", To: "DB", Text: "insert", Mark: "ok"},
		{Lane: "DB", To: "A; x", Text: "refused", Mark: "bad"},
		{Lane: "DB", Text: "two\nlines", Mark: "note"},
		{Lane: "A; x", Text: "plain"},
	}}
	got := timelineSource(tl)
	for _, w := range []string{
		"participant p0 as A#59; x\n", "participant p1 as DB\n",
		"p0->>p1: ✔ insert\n", "p1->>p0: ✘ refused\n", "Note over p1: ★ two<br/>lines\n", "Note over p0: plain\n",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("lacks %q:\n%s", w, got)
		}
	}
}
