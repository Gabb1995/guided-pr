// Package qa connects the reviewer's questions in the browser to the agent session that answers them.
//
// The browser asks; the question is stored as an event and wakes any agent blocked in Next. The agent
// claims it, may post status lines, and answers. Every event is fanned out to browser subscribers (SSE).
package qa

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/Gabb1995/guided-pr/internal/session"
)

// Selection is the code the reviewer pointed at when asking.
type Selection struct {
	HunkID    string `json:"hunkId,omitempty"`
	Path      string `json:"path"`
	Ref       string `json:"ref,omitempty"` // for code blocks outside the diff: head | base
	Side      string `json:"side"`          // old | new
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Code      string `json:"code,omitempty"`
}

type Question struct {
	ID        string     `json:"id"`
	Text      string     `json:"text"`
	SlideID   string     `json:"slideId,omitempty"`
	ParentID  string     `json:"parentId,omitempty"` // set when asked from an answer (a follow-up)
	Selection *Selection `json:"selection,omitempty"`
}

type Status struct {
	QuestionID string `json:"questionId"`
	Text       string `json:"text"`
}

// AnswerEvent wraps the answer payload with the question it belongs to.
type AnswerEvent struct {
	QuestionID string          `json:"questionId"`
	Answer     json.RawMessage `json:"answer"`
}

var (
	ErrUnknownQuestion = errors.New("unknown or already answered question")
	ErrEnded           = errors.New("the reviewer finished this review")
)

// ClaimTimeout is how long a claimed question stays hidden from other Next calls; after that an
// unanswered question is offered again (for example when the agent session died mid-answer).
var ClaimTimeout = 20 * time.Minute

// presenceGrace keeps the session "connected" briefly between Next calls while the agent answers.
const presenceGrace = 45 * time.Second

type Hub struct {
	store *session.Store

	mu       sync.Mutex
	subs     map[string]map[chan session.Event]struct{}
	wake     map[string]chan struct{}
	claims   map[string]map[string]time.Time
	waiting  map[string]int
	lastSeen map[string]time.Time
}

func NewHub(store *session.Store) *Hub {
	return &Hub{store: store, subs: map[string]map[chan session.Event]struct{}{}, wake: map[string]chan struct{}{},
		claims: map[string]map[string]time.Time{}, waiting: map[string]int{}, lastSeen: map[string]time.Time{}}
}

// Subscribe returns a channel of new events for a session. Call cancel when done.
func (h *Hub) Subscribe(id string) (<-chan session.Event, func()) {
	ch := make(chan session.Event, 64)
	h.mu.Lock()
	if h.subs[id] == nil {
		h.subs[id] = map[chan session.Event]struct{}{}
	}
	h.subs[id][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[id], ch)
		h.mu.Unlock()
	}
}

func (h *Hub) publish(id string, ev session.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[id] {
		select {
		case ch <- ev:
		default: // a slow browser misses a live event; it resyncs from the log on reconnect
		}
	}
}

func (h *Hub) append(id, typ string, data any) (session.Event, error) {
	ev, err := h.store.Append(id, typ, data)
	if err == nil {
		h.publish(id, ev)
	}
	return ev, err
}

func (h *Hub) wakeChan(id string) chan struct{} {
	if h.wake[id] == nil {
		h.wake[id] = make(chan struct{})
	}
	return h.wake[id]
}

func (h *Hub) broadcast(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ch := h.wake[id]; ch != nil {
		close(ch)
	}
	h.wake[id] = make(chan struct{})
}

// Ask records a question from the reviewer and wakes a waiting agent.
func (h *Hub) Ask(id string, q Question) (Question, error) {
	qid, err := session.NewID()
	if err != nil {
		return Question{}, err
	}
	q.ID = "q-" + qid[:12]
	if _, err := h.append(id, session.EventQuestion, q); err != nil {
		return Question{}, err
	}
	h.broadcast(id)
	return q, nil
}

// Next blocks until there is an unclaimed, unanswered question, then claims and returns it.
// It returns ErrEnded once the reviewer has finished the review.
func (h *Hub) Next(ctx context.Context, id string) (Question, error) {
	h.setWaiting(id, +1)
	defer h.setWaiting(id, -1)
	for {
		h.mu.Lock()
		wake := h.wakeChan(id)
		h.mu.Unlock()

		pending, ended, err := h.pending(id)
		if err != nil {
			return Question{}, err
		}
		if ended {
			return Question{}, ErrEnded
		}
		h.mu.Lock()
		if h.claims[id] == nil {
			h.claims[id] = map[string]time.Time{}
		}
		for _, q := range pending {
			if at, ok := h.claims[id][q.ID]; ok && time.Since(at) < ClaimTimeout {
				continue
			}
			h.claims[id][q.ID] = time.Now()
			h.mu.Unlock()
			return q, nil
		}
		h.mu.Unlock()

		select {
		case <-wake:
		case <-ctx.Done():
			return Question{}, ctx.Err()
		}
	}
}

func (h *Hub) pending(id string) ([]Question, bool, error) {
	events, err := h.store.Events(id)
	if err != nil {
		return nil, false, err
	}
	var order []string
	questions := map[string]Question{}
	for _, ev := range events {
		switch ev.Type {
		case session.EventEnded:
			return nil, true, nil
		case session.EventQuestion:
			var q Question
			if json.Unmarshal(ev.Data, &q) == nil {
				questions[q.ID] = q
				order = append(order, q.ID)
			}
		case session.EventAnswer:
			var a AnswerEvent
			if json.Unmarshal(ev.Data, &a) == nil {
				delete(questions, a.QuestionID)
			}
		}
	}
	var out []Question
	for _, qid := range order {
		if q, ok := questions[qid]; ok {
			out = append(out, q)
		}
	}
	return out, false, nil
}

// Question returns a pending question by id.
func (h *Hub) Question(id, qid string) (Question, error) {
	pending, _, err := h.pending(id)
	if err != nil {
		return Question{}, err
	}
	for _, q := range pending {
		if q.ID == qid {
			return q, nil
		}
	}
	return Question{}, ErrUnknownQuestion
}

// Status records a short progress line from the agent ("Reading StorePaymentVault.php").
func (h *Hub) Status(id, qid, text string) error {
	if _, err := h.Question(id, qid); err != nil {
		return err
	}
	h.seen(id)
	_, err := h.append(id, session.EventStatus, Status{QuestionID: qid, Text: text})
	return err
}

// Answer records the agent's (already validated and resolved) answer.
func (h *Hub) Answer(id, qid string, answer any) error {
	if _, err := h.Question(id, qid); err != nil {
		return err
	}
	raw, err := json.Marshal(answer)
	if err != nil {
		return err
	}
	h.seen(id)
	if _, err := h.append(id, session.EventAnswer, AnswerEvent{QuestionID: qid, Answer: raw}); err != nil {
		return err
	}
	h.mu.Lock()
	delete(h.claims[id], qid)
	h.mu.Unlock()
	return nil
}

// Record stores and publishes any other event (comments posted, review ended).
func (h *Hub) Record(id, typ string, data any) (session.Event, error) {
	ev, err := h.append(id, typ, data)
	if err == nil && typ == session.EventEnded {
		h.broadcast(id)
	}
	return ev, err
}

// Listening reports whether an agent is waiting for questions or answered one moments ago.
func (h *Hub) Listening(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.waiting[id] > 0 || time.Since(h.lastSeen[id]) < presenceGrace
}

func (h *Hub) seen(id string) {
	h.mu.Lock()
	h.lastSeen[id] = time.Now()
	h.mu.Unlock()
}

func (h *Hub) setWaiting(id string, delta int) {
	h.mu.Lock()
	before := h.waiting[id] > 0 || time.Since(h.lastSeen[id]) < presenceGrace
	h.waiting[id] += delta
	h.lastSeen[id] = time.Now()
	h.mu.Unlock()
	if after := h.Listening(id); after != before {
		h.publishPresence(id, after)
	}
}

func (h *Hub) publishPresence(id string, listening bool) {
	raw, _ := json.Marshal(map[string]bool{"listening": listening})
	h.publish(id, session.Event{Type: "presence", At: time.Now().UTC(), Data: raw})
}
