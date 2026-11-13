// Package web serves the review deck to the browser and the agent API used by the guided-pr CLI.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Gabb1995/guided-pr/internal/deck"
	"github.com/Gabb1995/guided-pr/internal/forge"
	"github.com/Gabb1995/guided-pr/internal/qa"
	"github.com/Gabb1995/guided-pr/internal/session"
)

//go:embed assets/*
var assets embed.FS

type Options struct {
	Store *session.Store
	Forge forge.Forge
	// LocalOnly rejects requests whose Host is not localhost (DNS-rebinding protection on a TCP port).
	LocalOnly bool
	// MaxWait caps one long-poll for the next question.
	MaxWait time.Duration
	// Version is reported by /healthz.
	Version string
	// OnShutdown, when set, is called by POST /api/shutdown (used by `guided-pr stop`).
	OnShutdown func()
}

type Server struct {
	store   *session.Store
	forge   forge.Forge
	hub     *qa.Hub
	local   bool
	maxWait time.Duration
	version string
	onStop  func()
}

func New(o Options) *Server {
	if o.MaxWait == 0 {
		o.MaxWait = 5 * time.Minute
	}
	return &Server{store: o.Store, forge: o.Forge, hub: qa.NewHub(o.Store), local: o.LocalOnly, maxWait: o.MaxWait, version: o.Version, onStop: o.OnShutdown}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		respond(w, 200, map[string]string{"status": "ok", "version": s.version})
	})
	mux.HandleFunc("POST /api/shutdown", func(w http.ResponseWriter, _ *http.Request) {
		if s.onStop == nil {
			fail(w, 404, "shutdown is not enabled")
			return
		}
		w.WriteHeader(204)
		go s.onStop()
	})

	// Agent API (the guided-pr CLI and the skill).
	mux.HandleFunc("POST /api/sessions", s.createSession)
	mux.HandleFunc("GET /api/sessions/{id}/bundle", s.bundle)
	mux.HandleFunc("PUT /api/sessions/{id}/deck", s.putDeck)
	mux.HandleFunc("POST /api/sessions/{id}/next", s.next)
	mux.HandleFunc("POST /api/sessions/{id}/questions/{qid}/status", s.status)
	mux.HandleFunc("POST /api/sessions/{id}/questions/{qid}/answer", s.answer)

	// Browser.
	mux.HandleFunc("GET /reviews/{id}", s.page)
	mux.HandleFunc("GET /api/sessions/{id}/view", s.view)
	mux.HandleFunc("GET /api/sessions/{id}/events", s.events)
	mux.HandleFunc("POST /api/sessions/{id}/questions", s.ask)
	mux.HandleFunc("POST /api/sessions/{id}/comments", s.comment)
	mux.HandleFunc("POST /api/sessions/{id}/end", s.end)
	mux.HandleFunc("GET /assets/{name}", s.asset)

	var h http.Handler = requireAPIHeader(mux)
	h = securityHeaders(h)
	if s.local {
		h = localHostsOnly(h)
	}
	return h
}

// ---- agent API ----

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PullRequestURL string `json:"pullRequestUrl"`
	}
	if !decode(w, r, &req) {
		return
	}
	ref, err := forge.ParsePullRequestURL(req.PullRequestURL)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	pr, files, err := s.forge.LoadPullRequest(r.Context(), ref)
	if err != nil {
		fail(w, 502, err.Error()+"\n\n(run `guided-pr doctor` to check the "+forge.ToolFor(ref.Provider).Name+" CLI)")
		return
	}
	id, err := session.NewID()
	if err != nil {
		fail(w, 500, "could not create session id")
		return
	}
	snap := session.Snapshot{SessionID: id, CreatedAt: time.Now().UTC(), PullRequest: pr, Files: files}
	if err := s.store.Create(snap); err != nil {
		fail(w, 500, err.Error())
		return
	}
	respond(w, 201, map[string]string{"id": id, "reviewUrl": "/reviews/" + id, "bundleUrl": "/api/sessions/" + id + "/bundle", "headSha": pr.HeadSHA})
}

func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) (session.Snapshot, bool) {
	snap, err := s.store.Snapshot(r.PathValue("id"))
	if errors.Is(err, session.ErrNotFound) {
		fail(w, 404, "review session not found")
		return snap, false
	}
	if err != nil {
		fail(w, 500, err.Error())
		return snap, false
	}
	return snap, true
}

func (s *Server) bundle(w http.ResponseWriter, r *http.Request) {
	if snap, ok := s.snapshot(w, r); ok {
		respond(w, 200, snap)
	}
}

func (s *Server) reader(snap session.Snapshot) deck.FileReader {
	return func(ctx context.Context, sha, path string) ([]byte, error) {
		return s.forge.ReadFile(ctx, snap.PullRequest, sha, path)
	}
}

func (s *Server) putDeck(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.snapshot(w, r)
	if !ok {
		return
	}
	var d deck.Deck
	if !decode(w, r, &d) {
		return
	}
	if err := deck.Validate(d, snap); err != nil {
		fail(w, 422, err.Error())
		return
	}
	blocks := d.AllBlocks()
	if err := deck.Resolve(r.Context(), blocks, snap, s.reader(snap)); err != nil {
		fail(w, 422, err.Error())
		return
	}
	highlightBlocks(blocks)
	if err := s.store.SaveDeck(snap.SessionID, d); err != nil {
		fail(w, 500, err.Error())
		return
	}
	respond(w, 201, map[string]string{"reviewUrl": "/reviews/" + snap.SessionID})
}

// nextQuestion is what the agent receives: the question plus the context it was asked in.
type nextQuestion struct {
	SessionID string        `json:"sessionId"`
	HeadSHA   string        `json:"headSha"`
	Question  qa.Question   `json:"question"`
	Slide     *deck.Slide   `json:"slide,omitempty"`
	Hunk      *contextHunk  `json:"hunk,omitempty"`
	Parent    *parentAnswer `json:"parent,omitempty"`
	Reply     string        `json:"reply"`
}

type contextHunk struct {
	ID    string `json:"id"`
	Path  string `json:"path"`
	Patch string `json:"patch"`
}

type parentAnswer struct {
	Question string          `json:"question"`
	Answer   json.RawMessage `json:"answer"`
}

func (s *Server) next(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.snapshot(w, r)
	if !ok {
		return
	}
	wait := s.maxWait
	if v, err := strconv.Atoi(r.URL.Query().Get("wait")); err == nil && v > 0 && time.Duration(v)*time.Second < wait {
		wait = time.Duration(v) * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), wait)
	defer cancel()
	q, err := s.hub.Next(ctx, snap.SessionID)
	switch {
	case errors.Is(err, qa.ErrEnded):
		fail(w, 410, err.Error())
		return
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		w.WriteHeader(204)
		return
	case err != nil:
		fail(w, 500, err.Error())
		return
	}
	out := nextQuestion{SessionID: snap.SessionID, HeadSHA: snap.PullRequest.HeadSHA, Question: q,
		Reply: fmt.Sprintf("/api/sessions/%s/questions/%s/answer", snap.SessionID, q.ID)}
	var d deck.Deck
	if s.store.Deck(snap.SessionID, &d) == nil {
		for _, c := range d.Chapters {
			for _, sl := range c.Slides {
				if sl.ID == q.SlideID {
					sl := sl
					stripRows(sl.Blocks)
					out.Slide = &sl
				}
			}
		}
	}
	hunkID := ""
	if q.Selection != nil {
		hunkID = q.Selection.HunkID
	}
	if hunkID == "" && out.Slide != nil {
		for _, b := range out.Slide.Blocks {
			if b.Type == "hunk" {
				hunkID = b.HunkID
				break
			}
		}
	}
	if h, f, ok := snap.Hunk(hunkID); ok {
		out.Hunk = &contextHunk{ID: h.ID, Path: f.Path, Patch: h.Patch}
	}
	if q.ParentID != "" {
		out.Parent = s.parent(snap.SessionID, q.ParentID)
	}
	respond(w, 200, out)
}

func stripRows(blocks []deck.Block) {
	for i := range blocks {
		blocks[i].Rows = nil
		if blocks[i].Left != nil {
			blocks[i].Left.Rows = nil
		}
		if blocks[i].Right != nil {
			blocks[i].Right.Rows = nil
		}
	}
}

func (s *Server) parent(id, qid string) *parentAnswer {
	events, err := s.store.Events(id)
	if err != nil {
		return nil
	}
	var p parentAnswer
	for _, ev := range events {
		switch ev.Type {
		case session.EventQuestion:
			var q qa.Question
			if json.Unmarshal(ev.Data, &q) == nil && q.ID == qid {
				p.Question = q.Text
			}
		case session.EventAnswer:
			var a qa.AnswerEvent
			if json.Unmarshal(ev.Data, &a) == nil && a.QuestionID == qid {
				var ans deck.Answer
				if json.Unmarshal(a.Answer, &ans) == nil {
					stripRows(ans.Blocks)
					p.Answer, _ = json.Marshal(ans)
				}
			}
		}
	}
	if p.Question == "" {
		return nil
	}
	return &p
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Text) == "" || len(req.Text) > 200 {
		fail(w, 400, "status text must be 1-200 characters")
		return
	}
	if err := s.hub.Status(r.PathValue("id"), r.PathValue("qid"), req.Text); err != nil {
		hubError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) answer(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.snapshot(w, r)
	if !ok {
		return
	}
	var a deck.Answer
	if !decode(w, r, &a) {
		return
	}
	if err := deck.ValidateAnswer(a, snap); err != nil {
		fail(w, 422, err.Error())
		return
	}
	blocks := deck.BlockPointers(a.Blocks)
	if err := deck.Resolve(r.Context(), blocks, snap, s.reader(snap)); err != nil {
		fail(w, 422, err.Error())
		return
	}
	highlightBlocks(blocks)
	if err := s.hub.Answer(snap.SessionID, r.PathValue("qid"), a); err != nil {
		hubError(w, err)
		return
	}
	w.WriteHeader(204)
}

// ---- browser ----

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.snapshot(w, r); !ok {
		return
	}
	data, _ := assets.ReadFile("assets/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) view(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.snapshot(w, r)
	if !ok {
		return
	}
	var d deck.Deck
	if err := s.store.Deck(snap.SessionID, &d); err != nil {
		fail(w, 409, "the walkthrough has not been generated yet")
		return
	}
	events, err := s.store.Events(snap.SessionID)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	respond(w, 200, map[string]any{
		"pullRequest": snap.PullRequest,
		"host":        snap.PullRequest.HostName(),
		"deck":        d,
		"hunks":       hunkViews(snap),
		"events":      events,
		"listening":   s.hub.Listening(snap.SessionID),
	})
}

// events streams new events as server-sent events, starting after ?after=SEQ, plus presence updates.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.snapshot(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "streaming is not supported")
		return
	}
	after, _ := strconv.Atoi(r.URL.Query().Get("after"))
	live, cancel := s.hub.Subscribe(snap.SessionID)
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	send := func(ev session.Event) {
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
	presence := func() {
		raw, _ := json.Marshal(map[string]bool{"listening": s.hub.Listening(snap.SessionID)})
		send(session.Event{Type: "presence", At: time.Now().UTC(), Data: raw})
	}
	backlog, _ := s.store.Events(snap.SessionID)
	last := after
	for _, ev := range backlog {
		if ev.Seq > last {
			send(ev)
			last = ev.Seq
		}
	}
	presence()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-live:
			if ev.Seq == 0 || ev.Seq > last {
				send(ev)
				if ev.Seq > last {
					last = ev.Seq
				}
			}
		case <-tick.C:
			presence()
		}
	}
}

func (s *Server) ask(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.snapshot(w, r)
	if !ok {
		return
	}
	var q qa.Question
	if !decode(w, r, &q) {
		return
	}
	q.Text = strings.TrimSpace(q.Text)
	if q.Text == "" || len(q.Text) > 4000 {
		fail(w, 400, "question must be 1-4000 characters")
		return
	}
	if q.Selection != nil && len(q.Selection.Code) > 20000 {
		fail(w, 400, "selection is too large")
		return
	}
	q, err := s.hub.Ask(snap.SessionID, q)
	if err != nil {
		hubError(w, err)
		return
	}
	respond(w, 201, q)
}

type commentRequest struct {
	HunkID    string `json:"hunkId"`
	Side      string `json:"side"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Body      string `json:"body"`
}

// comment posts a review comment to the forge. Only the browser calls this: the agent can draft, never post.
func (s *Server) comment(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.snapshot(w, r)
	if !ok {
		return
	}
	var req commentRequest
	if !decode(w, r, &req) {
		return
	}
	req.Body = strings.TrimSpace(req.Body)
	if req.Body == "" || len(req.Body) > 65536 {
		fail(w, 400, "comment body is required")
		return
	}
	start, end, file, err := deck.CommentLines(snap, req.HunkID, req.Side, req.StartLine, req.EndLine)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	pr := snap.PullRequest
	current, _, err := s.forge.LoadPullRequest(r.Context(), pr.Reference())
	if err != nil {
		fail(w, 502, "could not verify the current "+pr.HostName()+" commit: "+err.Error())
		return
	}
	if current.HeadSHA != pr.HeadSHA {
		fail(w, 409, "the "+pr.HostName()+" branch moved since this review was generated, so line numbers may be wrong; the comment was not posted")
		return
	}
	err = s.forge.CreateComment(r.Context(), pr, forge.Comment{Path: file.Path, OldPath: file.Previous, Body: req.Body, CommitSHA: pr.HeadSHA, Start: start, End: end})
	if err != nil {
		fail(w, 502, pr.HostName()+" rejected the comment: "+err.Error())
		return
	}
	ev, err := s.hub.Record(snap.SessionID, session.EventComment, req)
	if err != nil {
		fail(w, 500, "posted to "+pr.HostName()+", but could not record it locally")
		return
	}
	respond(w, 201, ev)
}

func (s *Server) end(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.snapshot(w, r)
	if !ok {
		return
	}
	if _, err := s.hub.Record(snap.SessionID, session.EventEnded, map[string]string{}); err != nil {
		fail(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

var contentTypes = map[string]string{".js": "application/javascript; charset=utf-8", ".css": "text/css; charset=utf-8"}

func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	data, err := assets.ReadFile("assets/" + name)
	dot := strings.LastIndex(name, ".")
	if err != nil || dot < 0 || contentTypes[name[dot:]] == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentTypes[name[dot:]])
	_, _ = w.Write(data)
}

// ---- helpers ----

func hubError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, qa.ErrUnknownQuestion):
		fail(w, 404, err.Error())
	case errors.Is(err, session.ErrNotFound):
		fail(w, 404, "review session not found")
	default:
		fail(w, 500, err.Error())
	}
}

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 8<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		fail(w, 400, "invalid JSON: "+err.Error())
		return false
	}
	if dec.More() {
		fail(w, 400, "invalid JSON: only one value is allowed")
		return false
	}
	return true
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, message string) {
	respond(w, status, map[string]string{"error": message})
}

// requireAPIHeader makes every state-changing request carry a custom header and a JSON body. Browsers cannot
// send either cross-origin without a CORS preflight, which this server never approves, so other websites
// cannot ask questions, post comments or answer on your behalf.
func requireAPIHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("X-Guided-PR") != "1" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				fail(w, 403, "requests must send X-Guided-PR: 1 and a JSON body")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func localHostsOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		switch strings.Trim(host, "[]") {
		case "localhost", "127.0.0.1", "::1":
			next.ServeHTTP(w, r)
		default:
			http.Error(w, "guided-pr only answers requests for localhost", http.StatusMisdirectedRequest)
		}
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mermaid writes <style> into its SVGs, hence 'unsafe-inline' for styles only.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
