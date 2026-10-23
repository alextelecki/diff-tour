package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	body := `{"after": 0, "question": "why?"}`
	if code, _ := post(t, ts, "", body, ""); code != http.StatusForbidden {
		t.Errorf("no token: status %d, want 403", code)
	}
	if code, _ := post(t, ts, "wrong", body, ""); code != http.StatusForbidden {
		t.Errorf("wrong token: status %d, want 403", code)
	}
	if code, _ := post(t, ts, s.token, body, "evil.example:80"); code != http.StatusForbidden {
		t.Errorf("wrong host: status %d, want 403", code)
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	req.Host = "localhost:1"
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != http.StatusForbidden {
		t.Errorf("page for a wrong host: %v %v, want 403", res.StatusCode, err)
	}
	if code, _ := post(t, ts, s.token, `{"after": 99, "question": "why?"}`, ""); code != http.StatusBadRequest {
		t.Errorf("step out of range: status %d, want 400", code)
	}
	if called {
		t.Error("Answer was called for a refused request")
	}
}

// The page carries the token and the question form, with the step's suggested questions.
func TestServePage(t *testing.T) {
	s, ts, _ := testServer(t, answerWith("x"))
	res, err := http.Get(ts.URL + "/")
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
}

func TestServeAnswer(t *testing.T) {
	s, ts, path := testServer(t, answerWith("The index is the guard"))
	code, body := post(t, ts, s.token, `{"after": 1, "question": "Why a unique index?"}`, "")
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
		code, _ := post(t, ts, s.token, `{"after": 0, "question": "one"}`, "")
		done <- code
	}()
	<-entered
	if code, _ := post(t, ts, s.token, `{"after": 0, "question": "two"}`, ""); code != http.StatusConflict {
		t.Errorf("second question: status %d, want 409", code)
	}
	close(release)
	if code := <-done; code != http.StatusOK {
		t.Errorf("first question: status %d", code)
	}
}
