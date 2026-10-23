package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/init-co-jp/diff-tour/internal/generate"
	"github.com/init-co-jp/diff-tour/internal/i18n"
	"github.com/init-co-jp/diff-tour/internal/tour"
)

// testServer serves the test tour from a copy of its file, answering questions with answer.
func testServer(t *testing.T, answer AnswerFunc) (*Server, *httptest.Server, string) {
	path := filepath.Join(t.TempDir(), "tour.json")
	b, err := os.ReadFile("testdata/tour.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := tour.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(testRepo(t), tr, i18n.EN, path, "Claude Code", answer)
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	s.Host = ts.Listener.Addr().String()
	return s, ts, path
}

func post(t *testing.T, ts *httptest.Server, token, body string, host string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/ask", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Tour-Token", token)
	}
	if host != "" {
		req.Host = host
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func answerWith(title string) AnswerFunc {
	return func(_ context.Context, q generate.Question) ([]tour.Step, generate.Stats, error) {
		text := "Because the index is the guard."
		return []tour.Step{{Title: title, Asked: q.Text, Blocks: []tour.Block{{P: &text, Basis: tour.Fact}}}}, generate.Stats{}, nil
	}
}

func TestServeRefusesOthers(t *testing.T) {
	called := false
	s, ts, _ := testServer(t, func(context.Context, generate.Question) ([]tour.Step, generate.Stats, error) {
		called = true
		return nil, generate.Stats{}, nil
	})
	body := `{"after": 0, "n": 5, "question": "why?"}`
	if code, _ := post(t, ts, "", body, ""); code != http.StatusForbidden {
		t.Errorf("no token: status %d, want 403", code)
	}
	if code, _ := post(t, ts, "wrong", body, ""); code != http.StatusForbidden {
		t.Errorf("wrong token: status %d, want 403", code)
	}
	// Only the exact 127.0.0.1:<port> passes, so DNS rebinding cannot reach the server.
	_, port, _ := strings.Cut(s.Host, ":")
	for _, h := range []string{"evil.example:80", "localhost:" + port, "LOCALHOST:" + port, "[::1]:" + port, "127.0.0.1",
		"127.0.0.1:" + port + ".evil", "127.0.0.2:" + port} {
		if code, _ := post(t, ts, s.token, body, h); code != http.StatusForbidden {
			t.Errorf("host %q: status %d, want 403", h, code)
		}
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
		req.Host = h
		if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != http.StatusForbidden {
			t.Errorf("page for host %q: %v %v, want 403", h, res.StatusCode, err)
		}
	}
	// A form or text/plain post from another site needs no preflight, but it cannot carry the token.
	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", ""} {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/ask", strings.NewReader(body))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != http.StatusForbidden {
			t.Errorf("%q without token: %v %v, want 403", ct, res.StatusCode, err)
		}
	}
	if code, _ := post(t, ts, s.token, `{"after": 99, "n": 5, "question": "why?"}`, ""); code != http.StatusBadRequest {
		t.Errorf("step out of range: status %d, want 400", code)
	}
	if code, _ := post(t, ts, s.token, `{"after": 0, "n": 5, "question": "`+strings.Repeat("a", 70<<10)+`"}`, ""); code != http.StatusBadRequest {
		t.Errorf("70 KiB body: status %d, want 400", code)
	}
	if called {
		t.Error("Answer was called for a refused request")
	}
}

// The page carries the token and the question form, with the step's suggested questions.
func TestServePage(t *testing.T) {
	s, ts, _ := testServer(t, answerWith("x"))
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	req.Header.Set("Origin", "https://evil.example")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	page := string(b)
	for _, want := range []string{`token: "` + s.token + `"`, `<form class="ask">`, "Why a unique index?", "Claude Code is reading the code"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// Another site can neither read the page (no CORS) nor frame it.
	for k := range res.Header {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			t.Errorf("CORS header %s", k)
		}
	}
	if res.Header.Get("X-Frame-Options") != "DENY" || res.Header.Get("Content-Security-Policy") != "frame-ancestors 'none'" {
		t.Errorf("framing headers: %v", res.Header)
	}
	for _, p := range []string{"/../../etc/passwd", "/assets/tour.js", "/page.html", "/testdata/tour.json", "/%2e%2e/go.mod"} {
		res, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", p, res.StatusCode)
		}
	}
}

func TestServeAnswer(t *testing.T) {
	s, ts, path := testServer(t, answerWith("The index is the guard"))
	other := filepath.Join(t.TempDir(), "other.json")
	// Fields other than after, n and question are ignored: the request cannot choose where the tour is written.
	code, body := post(t, ts, s.token, `{"after": 1, "n": 5, "question": "Why a unique index?", "path": "`+other+`"}`, "")
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	if !strings.Contains(body, `id="k3"`) || !strings.Contains(body, "The index is the guard") || !strings.Contains(body, "Why a unique index?") {
		t.Errorf("answer HTML:\n%s", body)
	}
	if strings.Contains(body, "<html") {
		t.Error("the answer should be only the new steps")
	}
	saved, err := tour.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if steps := saved.Steps(); len(steps) != 6 || steps[2].Title != "The index is the guard" || steps[2].Asked != "Why a unique index?" {
		t.Errorf("saved tour: %d steps, third %q", len(steps), steps[2].Title)
	}
	if left, _ := filepath.Glob(path + ".*.tmp"); len(left) > 0 {
		t.Errorf("temporary files left: %v", left)
	}
	if _, err := os.Stat(other); err == nil || s.path != path {
		t.Error("the tour was written to a path from the request")
	}
}

func TestServeOneQuestionAtATime(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	s, ts, _ := testServer(t, func(ctx context.Context, q generate.Question) ([]tour.Step, generate.Stats, error) {
		close(entered)
		<-release
		return answerWith("first")(ctx, q)
	})
	done := make(chan int)
	go func() {
		code, _ := post(t, ts, s.token, `{"after": 0, "n": 5, "question": "one"}`, "")
		done <- code
	}()
	<-entered
	if code, _ := post(t, ts, s.token, `{"after": 0, "n": 5, "question": "two"}`, ""); code != http.StatusConflict {
		t.Errorf("second question: status %d, want 409", code)
	}
	close(release)
	if code := <-done; code != http.StatusOK {
		t.Errorf("first question: status %d", code)
	}
}

// A page that does not have the server's steps (another tab asked, or a save failed) would ask about, and insert
// after, a different step than the reader sees. It is told to reload instead.
func TestServeStalePage(t *testing.T) {
	var asked []string
	s, ts, _ := testServer(t, func(ctx context.Context, q generate.Question) ([]tour.Step, generate.Stats, error) {
		asked = append(asked, q.Text)
		return answerWith("answer")(ctx, q)
	})
	if code, body := post(t, ts, s.token, `{"after": 0, "n": 5, "question": "tab A"}`, ""); code != http.StatusOK {
		t.Fatalf("tab A: status %d: %s", code, body)
	}
	code, body := post(t, ts, s.token, `{"after": 1, "n": 5, "question": "tab B"}`, "")
	if code != http.StatusConflict || strings.TrimSpace(body) != i18n.T("web.stale") {
		t.Errorf("tab B: status %d %q, want 409 %q", code, body, i18n.T("web.stale"))
	}
	if len(asked) != 1 || len(s.tour.Steps()) != 6 {
		t.Errorf("asked %v, %d steps", asked, len(s.tour.Steps()))
	}
}

// A failed answer changes neither the file nor the tour, and a panic in it still lets the next question in.
func TestServeAnswerFails(t *testing.T) {
	calls := 0
	s, ts, path := testServer(t, func(ctx context.Context, q generate.Question) ([]tour.Step, generate.Stats, error) {
		calls++
		switch calls {
		case 1:
			return nil, generate.Stats{}, errors.New("model broke\nsecond line")
		case 2:
			panic(http.ErrAbortHandler) // the panic net/http does not log
		}
		return answerWith("x")(ctx, q)
	})
	before, _ := os.ReadFile(path)
	body := `{"after": 0, "n": 5, "question": "q"}`
	if code, msg := post(t, ts, s.token, body, ""); code != http.StatusBadGateway || strings.Contains(msg, "second line") {
		t.Errorf("status %d %q, want 502 with the first line only", code, msg)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) || len(s.tour.Steps()) != 5 {
		t.Error("a failed answer changed the tour")
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/ask", strings.NewReader(body))
	req.Header.Set("X-Tour-Token", s.token)
	if res, err := http.DefaultClient.Do(req); err == nil {
		res.Body.Close()
	}
	if code, _ := post(t, ts, s.token, body, ""); code != http.StatusOK {
		t.Errorf("after a panic: status %d, want 200", code)
	}
}

// An answer that could not be saved is not kept in memory either, so the page and the server still agree.
func TestServeSaveFails(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs a read-only directory")
	}
	s, ts, path := testServer(t, answerWith("lost answer"))
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	before := s.tour.Steps()
	if code, _ := post(t, ts, s.token, `{"after": 0, "n": 5, "question": "q"}`, ""); code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", code)
	}
	if after := s.tour.Steps(); !reflect.DeepEqual(before, after) {
		t.Errorf("the tour in memory changed: %d steps, was %d", len(after), len(before))
	}
	os.Chmod(dir, 0o755)
	if code, _ := post(t, ts, s.token, `{"after": 0, "n": 5, "question": "q"}`, ""); code != http.StatusOK {
		t.Errorf("the same page after the save works again: status %d", code)
	}
}

// Saving keeps the file's permissions and writes through a link.
func TestSaveKeepsModeAndLink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.json")
	b, err := os.ReadFile("testdata/tour.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, b, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	tr, err := tour.Load(link)
	if err != nil {
		t.Fatal(err)
	}
	tr.Title = "saved"
	if err := save(link, tr); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("save replaced the link with a file")
	}
	if fi, err := os.Stat(real); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode after save: %v, want 0600", fi.Mode().Perm())
	}
	if got, err := tour.Load(real); err != nil || got.Title != "saved" {
		t.Errorf("the link's target was not written: %v", err)
	}
}
