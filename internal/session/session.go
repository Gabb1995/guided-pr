// Package session stores one review per directory: an immutable snapshot, the deck, and an append-only event log.
package session

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Gabb1995/guided-pr/internal/forge"
)

var ErrNotFound = errors.New("review session not found")

// Snapshot is the pull request pinned to one head SHA. It never changes after creation.
type Snapshot struct {
	SessionID   string              `json:"sessionId"`
	CreatedAt   time.Time           `json:"createdAt"`
	PullRequest forge.PullRequest   `json:"pullRequest"`
	Files       []forge.ChangedFile `json:"files"`
}

// Hunk finds a hunk and the file it belongs to.
func (s Snapshot) Hunk(id string) (forge.Hunk, forge.ChangedFile, bool) {
	for _, f := range s.Files {
		for _, h := range f.Hunks {
			if h.ID == id {
				return h, f, true
			}
		}
	}
	return forge.Hunk{}, forge.ChangedFile{}, false
}

// HunkIDs lists every hunk in diff order.
func (s Snapshot) HunkIDs() []string {
	var ids []string
	for _, f := range s.Files {
		for _, h := range f.Hunks {
			ids = append(ids, h.ID)
		}
	}
	return ids
}

// Event types in events.jsonl.
const (
	EventQuestion = "question" // the reviewer asked something
	EventStatus   = "status"   // the agent reports progress on a question
	EventAnswer   = "answer"   // the agent answered a question
	EventComment  = "comment"  // the reviewer posted a comment to the forge
	EventEnded    = "ended"    // the reviewer finished; the agent should stop listening
)

type Event struct {
	Seq  int             `json:"seq"`
	Type string          `json:"type"`
	At   time.Time       `json:"at"`
	Data json.RawMessage `json:"data"`
}

type Store struct {
	root  string
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func NewStore(root string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(root, "sessions"), 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	return &Store{root: root, locks: map[string]*sync.Mutex{}}, nil
}

func (s *Store) lock(id string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks[id] == nil {
		s.locks[id] = &sync.Mutex{}
	}
	return s.locks[id]
}

func (s *Store) dir(id string) (string, error) {
	if !validID(id) {
		return "", ErrNotFound
	}
	return filepath.Join(s.root, "sessions", id), nil
}

func validID(id string) bool {
	if len(id) != 24 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func NewID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Store) Create(snap Snapshot) error {
	dir, err := s.dir(snap.SessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create session directory: %w", err)
	}
	return writeJSON(filepath.Join(dir, "snapshot.json"), snap)
}

func (s *Store) Snapshot(id string) (Snapshot, error) {
	var snap Snapshot
	return snap, s.readJSON(id, "snapshot.json", &snap)
}

// SaveDeck stores the deck as given; it is validated and resolved by the caller.
func (s *Store) SaveDeck(id string, deck any) error {
	dir, err := s.dir(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "snapshot.json")); err != nil {
		return ErrNotFound
	}
	return writeJSON(filepath.Join(dir, "deck.json"), deck)
}

// Deck decodes deck.json into target. It returns ErrNotFound when no deck was imported yet.
func (s *Store) Deck(id string, target any) error { return s.readJSON(id, "deck.json", target) }

func (s *Store) readJSON(id, name string, target any) error {
	dir, err := s.dir(id)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

// Append writes one event and returns it with its sequence number.
func (s *Store) Append(id, typ string, data any) (Event, error) {
	dir, err := s.dir(id)
	if err != nil {
		return Event{}, err
	}
	l := s.lock(id)
	l.Lock()
	defer l.Unlock()
	events, err := s.readEvents(dir)
	if err != nil {
		return Event{}, err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return Event{}, err
	}
	ev := Event{Seq: len(events) + 1, Type: typ, At: time.Now().UTC(), Data: raw}
	line, _ := json.Marshal(ev)
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return Event{}, err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return Event{}, err
	}
	return ev, f.Sync()
}

func (s *Store) Events(id string) ([]Event, error) {
	dir, err := s.dir(id)
	if err != nil {
		return nil, err
	}
	l := s.lock(id)
	l.Lock()
	defer l.Unlock()
	return s.readEvents(dir)
}

func (s *Store) readEvents(dir string) ([]Event, error) {
	f, err := os.Open(filepath.Join(dir, "events.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		if _, err := os.Stat(filepath.Join(dir, "snapshot.json")); err != nil {
			return nil, ErrNotFound
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var events []Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 16<<20)
	for scanner.Scan() {
		var ev Event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue // a torn last line from a crash is skipped
		}
		events = append(events, ev)
	}
	return events, scanner.Err()
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
