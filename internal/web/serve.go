package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/init-co-jp/diff-tour/internal/generate"
	"github.com/init-co-jp/diff-tour/internal/git"
	"github.com/init-co-jp/diff-tour/internal/i18n"
	"github.com/init-co-jp/diff-tour/internal/render"
	"github.com/init-co-jp/diff-tour/internal/tour"
)

// AnswerFunc answers a reader's question as steps to insert after the step they asked about.
type AnswerFunc func(context.Context, generate.Question) ([]tour.Step, generate.Stats, error)

// Server serves the page of a tour and answers the questions asked on it. Answers are added to the tour
// and written to its file, like answers in the TUI are added to a saved tour.
type Server struct {
	// Host is the address the page is served at (127.0.0.1:<port>). Requests naming any other host are refused,
	// so a page from another site cannot reach the server through DNS rebinding.
	Host string

	repo   *git.Repo
	tour   *tour.Tour
	lang   i18n.Lang
	path   string
	llm    string
	answer AnswerFunc
	// token is in the page and must come back in X-Tour-Token: another site can neither read it nor send the header.
	token string
	busy  sync.Mutex // one question at a time
	mu    sync.Mutex // guards tour while it changes
}

// NewServer serves t. Answers are saved to path; llm is the name shown while a question is answered.
func NewServer(repo *git.Repo, t *tour.Tour, lang i18n.Lang, path, llm string, answer AnswerFunc) *Server {
	return &Server{repo: repo, tour: t, lang: lang, path: path, llm: llm, answer: answer, token: rand.Text()}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Host != s.Host {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	switch {
	case r.URL.Path == "/" && r.Method == http.MethodGet:
		s.mu.Lock()
		b, err := renderPage(s.repo, s.tour, s.lang, s.token, i18n.T("web.answering", s.llm))
		s.mu.Unlock()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	case r.URL.Path == "/ask" && r.Method == http.MethodPost:
		s.ask(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) ask(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Tour-Token")), []byte(s.token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var req struct {
		After    int    `json:"after"`
		Question string `json:"question"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	question := strings.TrimSpace(req.Question)
	if !s.busy.TryLock() {
		http.Error(w, i18n.T("web.busy"), http.StatusConflict)
		return
	}
	defer s.busy.Unlock()
	// Only a question changes the tour, and it holds busy, so reading the tour from here on needs no lock
	if question == "" || req.After < 0 || req.After >= len(s.tour.Steps()) {
		http.Error(w, "bad question", http.StatusBadRequest)
		return
	}
	// Answering takes minutes. If the browser stops waiting, the answer is still saved for the next visit
	steps, _, err := s.answer(context.WithoutCancel(r.Context()), generate.Question{Tour: s.tour, Index: req.After, Text: question})
	if err != nil {
		msg, _, _ := strings.Cut(err.Error(), "\n")
		http.Error(w, i18n.T("ask.failed", msg), http.StatusBadGateway)
		return
	}
	s.mu.Lock()
	s.tour.InsertAfter(req.After, steps)
	s.mu.Unlock()
	if err := save(s.path, s.tour); err != nil {
		http.Error(w, i18n.T("ask.notSaved", err), http.StatusInternalServerError)
		return
	}
	added := s.tour.Steps()[req.After+1 : req.After+1+len(steps)]
	b, err := renderSteps(render.Ctx{Tour: s.tour, Repo: s.repo}, added, req.After+1, s.token)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b)
}

// save writes the tour to a temporary file next to path and renames it over path, so a crash never leaves half a tour.
func save(path string, t *tour.Tour) error {
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name()) // fails harmlessly once renamed
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(f.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
