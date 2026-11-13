package forge

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
)

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// SplitHunks splits a file patch into hunks with content-addressed IDs.
func SplitHunks(path, patch string) []Hunk {
	if patch == "" {
		return nil
	}
	lines := strings.Split(patch, "\n")
	starts := make([]int, 0)
	for index, line := range lines {
		if hunkHeader.MatchString(line) {
			starts = append(starts, index)
		}
	}
	hunks := make([]Hunk, 0, len(starts))
	for index, start := range starts {
		end := len(lines)
		if index+1 < len(starts) {
			end = starts[index+1]
		}
		body := strings.Join(lines[start:end], "\n")
		matches := hunkHeader.FindStringSubmatch(lines[start])
		oldStart, _ := strconv.Atoi(matches[1])
		newStart, _ := strconv.Atoi(matches[2])
		digest := sha256.Sum256([]byte(path + "\x00" + body))
		hunks = append(hunks, Hunk{ID: "hunk:" + hex.EncodeToString(digest[:])[:12], OldStart: oldStart, NewStart: newStart, Patch: body})
	}
	return hunks
}

// DiffLine is one line of a hunk. OldPos/NewPos are the running positions on each side; for an added line
// OldPos is the old-side position it sits at (GitLab needs it for line codes) but the line has no old number.
type DiffLine struct {
	Kind   string `json:"t"` // add | del | ctx
	OldPos int    `json:"o"`
	NewPos int    `json:"n"`
	Text   string `json:"text"`
}

// Side is "old" for removed lines and "new" for added and context lines.
func (l DiffLine) Side() string {
	if l.Kind == "del" {
		return "old"
	}
	return "new"
}

// Number is the line number on the line's own side.
func (l DiffLine) Number() int {
	if l.Kind == "del" {
		return l.OldPos
	}
	return l.NewPos
}

func HunkLines(h Hunk) []DiffLine {
	oldPos, newPos := h.OldStart, h.NewStart
	out := []DiffLine{}
	for _, raw := range strings.Split(h.Patch, "\n") {
		if raw == "" || hunkHeader.MatchString(raw) || raw[0] == '\\' {
			continue
		}
		switch raw[0] {
		case '+':
			out = append(out, DiffLine{Kind: "add", OldPos: oldPos, NewPos: newPos, Text: raw[1:]})
			newPos++
		case '-':
			out = append(out, DiffLine{Kind: "del", OldPos: oldPos, NewPos: newPos, Text: raw[1:]})
			oldPos++
		default:
			out = append(out, DiffLine{Kind: "ctx", OldPos: oldPos, NewPos: newPos, Text: raw[1:]})
			oldPos++
			newPos++
		}
	}
	return out
}

func countChanges(patch string) (additions, deletions int) {
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "+"):
			additions++
		case strings.HasPrefix(line, "-"):
			deletions++
		}
	}
	return
}
