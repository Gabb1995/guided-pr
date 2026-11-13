// Package deck defines review-deck.v1: the walkthrough the agent writes, and answers to the reviewer's questions.
// Slides and answers share one block vocabulary, so the browser renders both the same way.
package deck

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Gabb1995/guided-pr/internal/forge"
	"github.com/Gabb1995/guided-pr/internal/session"
)

const SchemaVersion = "review-deck.v1"

type Deck struct {
	SchemaVersion string    `json:"schemaVersion"`
	Source        Source    `json:"source"`
	Intro         Intro     `json:"intro"`
	Chapters      []Chapter `json:"chapters"`
}

type Source struct {
	SessionID string `json:"sessionId"`
	HeadSHA   string `json:"headSha"`
}

// Intro is the first screen: what the task is, told plainly by the author.
type Intro struct {
	Title  string  `json:"title"`
	Story  string  `json:"story"`
	Ticket *Ticket `json:"ticket,omitempty"`
}

type Ticket struct {
	Key   string `json:"key"`
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
}

type Chapter struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Slides []Slide  `json:"slides"`
	Minor  []string `json:"minor,omitempty"` // trivial hunks shown together at the end of the chapter
}

// Slide: code blocks on the left, the author's narration ("say") on the right.
type Slide struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Say    string  `json:"say"`
	Blocks []Block `json:"blocks"`
}

// Answer is the agent's reply to one question.
type Answer struct {
	Say    string  `json:"say"`
	Blocks []Block `json:"blocks,omitempty"`
}

// Block is one view. Which fields apply depends on Type:
//
//	hunk     hunkId, [side, startLine, endLine] to highlight lines, [label]
//	code     path, ref (head|base), startLine, endLine, [label]; the server fills rows from the forge
//	diagram  mermaid, [caption]
//	compare  left, right (each a hunk or code block), [caption]
//	comment  hunkId, side (old|new), startLine, endLine, body; answers only, the reviewer posts it
type Block struct {
	Type      string `json:"type"`
	Label     string `json:"label,omitempty"`
	HunkID    string `json:"hunkId,omitempty"`
	Path      string `json:"path,omitempty"`
	Ref       string `json:"ref,omitempty"`
	Side      string `json:"side,omitempty"`
	StartLine int    `json:"startLine,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
	Mermaid   string `json:"mermaid,omitempty"`
	Caption   string `json:"caption,omitempty"`
	Left      *Block `json:"left,omitempty"`
	Right     *Block `json:"right,omitempty"`
	Body      string `json:"body,omitempty"`

	Rows []Row `json:"rows,omitempty"` // server-filled for code blocks; rejected on input
}

// Row is one rendered line of a code block. HTML is the syntax-highlighted text, added by the web layer.
type Row struct {
	N    int    `json:"n"`
	Text string `json:"text"`
	HTML string `json:"html,omitempty"`
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

const maxCodeLines = 200

// Validate checks a deck against its snapshot: structure, references, and that every hunk is shown.
func Validate(d Deck, snap session.Snapshot) error {
	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }
	if d.SchemaVersion != SchemaVersion {
		add("schemaVersion must be %q", SchemaVersion)
	}
	if d.Source.SessionID != snap.SessionID || d.Source.HeadSHA != snap.PullRequest.HeadSHA {
		add("source must match the session id and head SHA of the analysis bundle")
	}
	if strings.TrimSpace(d.Intro.Title) == "" || strings.TrimSpace(d.Intro.Story) == "" {
		add("intro.title and intro.story are required")
	}
	if len(d.Chapters) == 0 {
		add("at least one chapter is required")
	}
	shown := map[string]bool{}
	ids := map[string]bool{}
	for ci, c := range d.Chapters {
		where := fmt.Sprintf("chapters[%d]", ci)
		if !idPattern.MatchString(c.ID) || ids[c.ID] {
			add("%s.id must be a unique kebab-case id", where)
		}
		ids[c.ID] = true
		if strings.TrimSpace(c.Title) == "" {
			add("%s.title is required", where)
		}
		if len(c.Slides) == 0 {
			add("%s needs at least one slide", where)
		}
		for si, s := range c.Slides {
			sw := fmt.Sprintf("%s.slides[%d]", where, si)
			if !idPattern.MatchString(s.ID) || ids[s.ID] {
				add("%s.id must be a unique kebab-case id", sw)
			}
			ids[s.ID] = true
			if strings.TrimSpace(s.Title) == "" || strings.TrimSpace(s.Say) == "" {
				add("%s needs title and say", sw)
			}
			for bi, b := range s.Blocks {
				if b.Type == "comment" {
					add("%s.blocks[%d]: comment blocks are only allowed in answers", sw, bi)
					continue
				}
				if err := validateBlock(b, snap, true); err != nil {
					add("%s.blocks[%d]: %v", sw, bi, err)
				}
				markShown(b, shown)
			}
		}
		for _, id := range c.Minor {
			if _, _, ok := snap.Hunk(id); !ok {
				add("%s.minor: unknown hunk %s", where, id)
			} else if shown[id] {
				add("%s.minor: hunk %s is already shown elsewhere", where, id)
			}
			shown[id] = true
		}
	}
	var missing []string
	for _, id := range snap.HunkIDs() {
		if !shown[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		add("every hunk must appear on a slide or in a chapter's minor list; missing: %s", strings.Join(missing, ", "))
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid deck:\n- %s", strings.Join(errs, "\n- "))
	}
	return nil
}

// ValidateAnswer checks an answer's blocks against the snapshot.
func ValidateAnswer(a Answer, snap session.Snapshot) error {
	if strings.TrimSpace(a.Say) == "" {
		return fmt.Errorf("answer.say is required")
	}
	for i, b := range a.Blocks {
		if err := validateBlock(b, snap, true); err != nil {
			return fmt.Errorf("answer.blocks[%d]: %w", i, err)
		}
	}
	return nil
}

func markShown(b Block, shown map[string]bool) {
	if b.Type == "hunk" {
		shown[b.HunkID] = true
	}
	if b.Left != nil {
		markShown(*b.Left, shown)
	}
	if b.Right != nil {
		markShown(*b.Right, shown)
	}
}

func validateBlock(b Block, snap session.Snapshot, top bool) error {
	if b.Rows != nil {
		return fmt.Errorf("rows are filled in by the server; give path, ref and line numbers instead")
	}
	switch b.Type {
	case "hunk":
		hunk, _, ok := snap.Hunk(b.HunkID)
		if !ok {
			return fmt.Errorf("unknown hunk %q", b.HunkID)
		}
		if b.Side != "" || b.StartLine != 0 || b.EndLine != 0 {
			if _, _, err := linesIn(hunk, b.Side, b.StartLine, b.EndLine); err != nil {
				return err
			}
		}
	case "code":
		if strings.TrimSpace(b.Path) == "" || strings.Contains(b.Path, "..") {
			return fmt.Errorf("code needs a repository path")
		}
		if b.Ref != "head" && b.Ref != "base" {
			return fmt.Errorf("code.ref must be head or base")
		}
		if b.StartLine < 1 || b.EndLine < b.StartLine || b.EndLine-b.StartLine >= maxCodeLines {
			return fmt.Errorf("code needs 1 <= startLine <= endLine and at most %d lines", maxCodeLines)
		}
	case "diagram":
		if strings.TrimSpace(b.Mermaid) == "" || len(b.Mermaid) > 8000 {
			return fmt.Errorf("diagram needs mermaid source under 8000 characters")
		}
	case "compare":
		if !top {
			return fmt.Errorf("compare cannot be nested")
		}
		if b.Left == nil || b.Right == nil {
			return fmt.Errorf("compare needs left and right")
		}
		for _, side := range []*Block{b.Left, b.Right} {
			if side.Type != "hunk" && side.Type != "code" {
				return fmt.Errorf("compare sides must be hunk or code blocks")
			}
			if err := validateBlock(*side, snap, false); err != nil {
				return err
			}
		}
	case "comment":
		hunk, file, ok := snap.Hunk(b.HunkID)
		if !ok {
			return fmt.Errorf("unknown hunk %q", b.HunkID)
		}
		if b.Path != "" && b.Path != file.Path {
			return fmt.Errorf("comment path does not match the hunk's file %s", file.Path)
		}
		if strings.TrimSpace(b.Body) == "" || len(b.Body) > 65536 {
			return fmt.Errorf("comment needs a body")
		}
		if _, _, err := linesIn(hunk, b.Side, b.StartLine, b.EndLine); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown block type %q", b.Type)
	}
	return nil
}

// CommentLines finds the first and last diff line of a selection on one side of a hunk.
func CommentLines(snap session.Snapshot, hunkID, side string, start, end int) (forge.DiffLine, forge.DiffLine, forge.ChangedFile, error) {
	hunk, file, ok := snap.Hunk(hunkID)
	if !ok {
		return forge.DiffLine{}, forge.DiffLine{}, file, fmt.Errorf("unknown hunk %q", hunkID)
	}
	first, last, err := linesIn(hunk, side, start, end)
	return first, last, file, err
}

func linesIn(hunk forge.Hunk, side string, start, end int) (forge.DiffLine, forge.DiffLine, error) {
	if side != "old" && side != "new" {
		return forge.DiffLine{}, forge.DiffLine{}, fmt.Errorf("side must be old or new")
	}
	if start < 1 || end < start {
		return forge.DiffLine{}, forge.DiffLine{}, fmt.Errorf("need 1 <= startLine <= endLine")
	}
	var first, last *forge.DiffLine
	for _, l := range forge.HunkLines(hunk) {
		l := l
		if l.Side() != side {
			continue
		}
		if l.Number() == start {
			first = &l
		}
		if l.Number() == end {
			last = &l
		}
	}
	if first == nil || last == nil {
		return forge.DiffLine{}, forge.DiffLine{}, fmt.Errorf("lines %d-%d (%s side) are not in hunk %s", start, end, side, hunk.ID)
	}
	return *first, *last, nil
}

// FileReader reads a file at a commit SHA.
type FileReader func(ctx context.Context, sha, path string) ([]byte, error)

// Resolve fills the rows of every code block from the repository at the snapshot's base or head SHA,
// so code shown outside the diff is never pasted by the agent.
func Resolve(ctx context.Context, blocks []*Block, snap session.Snapshot, read FileReader) error {
	cache := map[string][]string{}
	var fill func(b *Block) error
	fill = func(b *Block) error {
		if b.Left != nil {
			if err := fill(b.Left); err != nil {
				return err
			}
		}
		if b.Right != nil {
			if err := fill(b.Right); err != nil {
				return err
			}
		}
		if b.Type != "code" {
			return nil
		}
		sha := snap.PullRequest.HeadSHA
		if b.Ref == "base" {
			sha = snap.PullRequest.BaseSHA
		}
		key := sha + "\x00" + b.Path
		lines, ok := cache[key]
		if !ok {
			data, err := read(ctx, sha, b.Path)
			if err != nil {
				return fmt.Errorf("read %s at %s: %w", b.Path, b.Ref, err)
			}
			lines = strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
			cache[key] = lines
		}
		if b.StartLine > len(lines) {
			return fmt.Errorf("%s has %d lines at %s; startLine %d is past the end", b.Path, len(lines), b.Ref, b.StartLine)
		}
		if b.EndLine > len(lines) {
			b.EndLine = len(lines)
		}
		b.Rows = make([]Row, 0, b.EndLine-b.StartLine+1)
		for n := b.StartLine; n <= b.EndLine; n++ {
			b.Rows = append(b.Rows, Row{N: n, Text: lines[n-1]})
		}
		return nil
	}
	for _, b := range blocks {
		if err := fill(b); err != nil {
			return err
		}
	}
	return nil
}

// BlockPointers returns pointers into a block slice, for resolving.
func BlockPointers(blocks []Block) []*Block {
	out := make([]*Block, len(blocks))
	for i := range blocks {
		out[i] = &blocks[i]
	}
	return out
}

// AllBlocks returns pointers to every top-level block in the deck, for resolving.
func (d *Deck) AllBlocks() []*Block {
	var out []*Block
	for ci := range d.Chapters {
		for si := range d.Chapters[ci].Slides {
			for bi := range d.Chapters[ci].Slides[si].Blocks {
				out = append(out, &d.Chapters[ci].Slides[si].Blocks[bi])
			}
		}
	}
	return out
}
