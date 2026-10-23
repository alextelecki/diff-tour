package web

import (
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/init-co-jp/diff-tour/internal/git"
	"github.com/init-co-jp/diff-tour/internal/i18n"
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
