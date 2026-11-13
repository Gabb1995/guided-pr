package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gabb1995/guided-pr/internal/deck"
	"github.com/Gabb1995/guided-pr/internal/forge"
	"github.com/Gabb1995/guided-pr/internal/session"
)

const patch = "@@ -10,3 +10,4 @@ class Guard\n context\n-    old();\n+    new();\n+    again();\n tail"

type fakeForge struct {
	mu       sync.Mutex
	head     string
	comments []forge.Comment
}

func (f *fakeForge) LoadPullRequest(_ context.Context, r forge.PullRequestURL) (forge.PullRequest, []forge.ChangedFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return forge.PullRequest{Provider: forge.ProviderGitLab, Owner: r.Owner, Repo: r.Repo, Number: r.Number, Title: "Guard", BaseSHA: "base", HeadSHA: f.head},
		[]forge.ChangedFile{{Path: "src/Guard.php", Status: "modified", Hunks: forge.SplitHunks("src/Guard.php", patch)}}, nil
}

func (f *fakeForge) ReadFile(_ context.Context, _ forge.PullRequest, sha, path string) ([]byte, error) {
	return []byte("<?php\nfunction caller() {\n    guard();\n}\n"), nil
}

func (f *fakeForge) CreateComment(_ context.Context, _ forge.PullRequest, c forge.Comment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.comments = append(f.comments, c)
	return nil
}

type harness struct {
	t     *testing.T
	srv   *httptest.Server
	forge *fakeForge
}

func newHarness(t *testing.T) *harness {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeForge{head: "head1"}
	s := New(Options{Store: store, Forge: f, MaxWait: 2 * time.Second})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &harness{t: t, srv: srv, forge: f}
}

func (h *harness) call(method, path string, body any, out any) int {
	h.t.Helper()
	var r io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		r = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, h.srv.URL+path, r)
	req.Header.Set("X-Guided-PR", "1")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			h.t.Fatalf("decode %s: %v (%s)", path, err, data)
		}
	}
	if res.StatusCode >= 400 {
		h.t.Logf("%s %s -> %d %s", method, path, res.StatusCode, data)
	}
	return res.StatusCode
}

func (h *harness) session() (string, session.Snapshot) {
	var created struct{ ID string }
	if code := h.call("POST", "/api/sessions", map[string]string{"pullRequestUrl": "https://gitlab.com/acme/shop/-/merge_requests/7"}, &created); code != 201 {
		h.t.Fatalf("create session: %d", code)
	}
	var snap session.Snapshot
	h.call("GET", "/api/sessions/"+created.ID+"/bundle", nil, &snap)
	return created.ID, snap
}

func validDeck(snap session.Snapshot) deck.Deck {
	hunk := snap.Files[0].Hunks[0].ID
	return deck.Deck{SchemaVersion: deck.SchemaVersion, Source: deck.Source{SessionID: snap.SessionID, HeadSHA: snap.PullRequest.HeadSHA},
		Intro: deck.Intro{Title: "Guard the checkout", Story: "I changed the guard."},
		Chapters: []deck.Chapter{{ID: "guard", Title: "The guard", Slides: []deck.Slide{{ID: "guard-call", Title: "Calls new()", Say: "Here I call new().",
			Blocks: []deck.Block{{Type: "hunk", HunkID: hunk}, {Type: "code", Path: "src/caller.php", Ref: "head", StartLine: 2, EndLine: 9}}}}}}}
}

func TestDeckIsValidatedAndCodeIsResolvedFromTheForge(t *testing.T) {
	h := newHarness(t)
	id, snap := h.session()

	incomplete := validDeck(snap)
	incomplete.Chapters[0].Slides[0].Blocks = incomplete.Chapters[0].Slides[0].Blocks[1:]
	var e struct{ Error string }
	if code := h.call("PUT", "/api/sessions/"+id+"/deck", incomplete, &e); code != 422 || !strings.Contains(e.Error, "missing") {
		t.Fatalf("expected missing-hunk rejection, got %d %q", code, e.Error)
	}

	pasted := validDeck(snap)
	pasted.Chapters[0].Slides[0].Blocks[1].Rows = []deck.Row{{N: 1, Text: "made up"}}
	if code := h.call("PUT", "/api/sessions/"+id+"/deck", pasted, nil); code != 400 && code != 422 {
		t.Fatalf("expected pasted rows to be rejected, got %d", code)
	}

	if code := h.call("PUT", "/api/sessions/"+id+"/deck", validDeck(snap), nil); code != 201 {
		t.Fatalf("import deck: %d", code)
	}
	var view struct {
		Deck  deck.Deck
		Hunks map[string]hunkView
	}
	h.call("GET", "/api/sessions/"+id+"/view", nil, &view)
	code := view.Deck.Chapters[0].Slides[0].Blocks[1]
	if len(code.Rows) != 3 || code.Rows[1].Text != "    guard();" || code.Rows[0].HTML == "" {
		t.Fatalf("code block not resolved and clamped to the file: %+v", code.Rows)
	}
	rows := view.Hunks[snap.Files[0].Hunks[0].ID].Rows
	if len(rows) != 5 || rows[1].Kind != "del" || rows[1].Old != 11 || rows[2].New != 11 {
		t.Fatalf("unexpected hunk rows: %+v", rows)
	}
}

func TestQuestionAnswerLoop(t *testing.T) {
	h := newHarness(t)
	id, snap := h.session()
	h.call("PUT", "/api/sessions/"+id+"/deck", validDeck(snap), nil)
	hunk := snap.Files[0].Hunks[0].ID

	// Nothing asked yet: the long-poll times out with 204.
	if code := h.call("POST", "/api/sessions/"+id+"/next?wait=1", map[string]string{}, nil); code != 204 {
		t.Fatalf("expected 204 when idle, got %d", code)
	}

	// The agent waits; the reviewer asks; the agent receives the question with its context.
	got := make(chan nextQuestion, 1)
	go func() {
		var n nextQuestion
		h.call("POST", "/api/sessions/"+id+"/next?wait=2", map[string]string{}, &n)
		got <- n
	}()
	time.Sleep(100 * time.Millisecond)
	var q struct{ ID string }
	h.call("POST", "/api/sessions/"+id+"/questions", map[string]any{"text": "Why again()?", "slideId": "guard-call",
		"selection": map[string]any{"hunkId": hunk, "path": "src/Guard.php", "side": "new", "startLine": 12, "endLine": 12, "code": "    again();"}}, &q)
	n := <-got
	if n.Question.ID != q.ID || n.Slide == nil || n.Slide.ID != "guard-call" || n.Hunk == nil || n.Hunk.ID != hunk || n.Question.Selection.StartLine != 12 {
		t.Fatalf("unexpected next payload: %+v", n)
	}
	if len(n.Slide.Blocks[1].Rows) != 0 {
		t.Fatal("next should not send resolved rows back to the agent")
	}

	// A claimed question is not handed out twice.
	if code := h.call("POST", "/api/sessions/"+id+"/next?wait=1", map[string]string{}, nil); code != 204 {
		t.Fatalf("claimed question was offered again: %d", code)
	}

	if code := h.call("POST", "/api/sessions/"+id+"/questions/"+q.ID+"/status", map[string]string{"text": "Reading Guard.php"}, nil); code != 204 {
		t.Fatalf("status: %d", code)
	}
	bad := deck.Answer{Say: "x", Blocks: []deck.Block{{Type: "comment", HunkID: hunk, Side: "new", StartLine: 99, EndLine: 99, Body: "?"}}}
	if code := h.call("POST", "/api/sessions/"+id+"/questions/"+q.ID+"/answer", bad, nil); code != 422 {
		t.Fatalf("comment on lines outside the hunk should be rejected, got %d", code)
	}
	good := deck.Answer{Say: "Because retries.", Blocks: []deck.Block{{Type: "comment", HunkID: hunk, Side: "new", StartLine: 11, EndLine: 12, Body: "Why twice?"}}}
	if code := h.call("POST", "/api/sessions/"+id+"/questions/"+q.ID+"/answer", good, nil); code != 204 {
		t.Fatalf("answer: %d", code)
	}
	if code := h.call("POST", "/api/sessions/"+id+"/questions/"+q.ID+"/answer", good, nil); code != 404 {
		t.Fatalf("answering twice should fail, got %d", code)
	}

	var view struct{ Events []session.Event }
	h.call("GET", "/api/sessions/"+id+"/view", nil, &view)
	var types []string
	for _, ev := range view.Events {
		types = append(types, ev.Type)
	}
	if strings.Join(types, ",") != "question,status,answer" {
		t.Fatalf("unexpected event log: %v", types)
	}

	// Finishing the review releases the agent.
	h.call("POST", "/api/sessions/"+id+"/end", map[string]string{}, nil)
	if code := h.call("POST", "/api/sessions/"+id+"/next?wait=1", map[string]string{}, nil); code != 410 {
		t.Fatalf("expected 410 after finish, got %d", code)
	}
}

func TestCommentsArePostedAsRangesAndRefusedWhenStale(t *testing.T) {
	h := newHarness(t)
	id, snap := h.session()
	hunk := snap.Files[0].Hunks[0].ID
	body := map[string]any{"hunkId": hunk, "side": "new", "startLine": 11, "endLine": 12, "body": "Why twice?"}
	if code := h.call("POST", "/api/sessions/"+id+"/comments", body, nil); code != 201 {
		t.Fatalf("comment: %d", code)
	}
	c := h.forge.comments[0]
	if c.Start.Kind != "add" || c.Start.NewPos != 11 || c.End.NewPos != 12 || c.End.OldPos != 12 || c.CommitSHA != "head1" {
		t.Fatalf("unexpected comment range: %+v", c)
	}
	h.forge.head = "head2"
	if code := h.call("POST", "/api/sessions/"+id+"/comments", body, nil); code != 409 {
		t.Fatalf("expected stale refusal, got %d", code)
	}
}

func TestStateChangingRequestsNeedTheAPIHeader(t *testing.T) {
	h := newHarness(t)
	id, _ := h.session()
	res, err := http.Post(h.srv.URL+"/api/sessions/"+id+"/questions", "text/plain", strings.NewReader(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("cross-site style request was accepted: %d", res.StatusCode)
	}
}
