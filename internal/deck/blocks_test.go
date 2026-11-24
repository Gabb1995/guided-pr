package deck

import (
	"strings"
	"testing"

	"github.com/Gabb1995/guided-pr/internal/forge"
	"github.com/Gabb1995/guided-pr/internal/session"
)

func TestNotesAndTablesAreValidated(t *testing.T) {
	snap := session.Snapshot{Files: []forge.ChangedFile{{
		Path:  "a.go",
		Hunks: []forge.Hunk{{ID: "hunk:1", OldStart: 10, NewStart: 10, Patch: " a\n-b\n+c\n d"}},
	}}}
	diagram := "graph LR; a-->b"
	cases := map[string]struct {
		block   Block
		wantErr string
	}{
		"note on an added line":   {Block{Type: "hunk", HunkID: "hunk:1", Notes: []Note{{Line: 11, Text: "new"}}}, ""},
		"note on a removed line":  {Block{Type: "hunk", HunkID: "hunk:1", Notes: []Note{{Line: 11, Side: "old", Text: "gone"}}}, ""},
		"note outside the hunk":   {Block{Type: "hunk", HunkID: "hunk:1", Notes: []Note{{Line: 40, Text: "x"}}}, "not in hunk"},
		"empty note":              {Block{Type: "hunk", HunkID: "hunk:1", Notes: []Note{{Line: 11}}}, "note text"},
		"note in code range":      {Block{Type: "code", Path: "a.go", Ref: "head", StartLine: 5, EndLine: 9, Notes: []Note{{Line: 7, Text: "x"}}}, ""},
		"note outside code range": {Block{Type: "code", Path: "a.go", Ref: "head", StartLine: 5, EndLine: 9, Notes: []Note{{Line: 12, Text: "x"}}}, "outside 5-9"},
		"note on a diagram":       {Block{Type: "diagram", Mermaid: diagram, Notes: []Note{{Line: 1, Text: "x"}}}, "only apply to hunk and code"},
		"table":                   {Block{Type: "table", Columns: []string{"in", "out"}, Cells: [][]string{{"1", "2"}}}, ""},
		"table with a short row":  {Block{Type: "table", Columns: []string{"in", "out"}, Cells: [][]string{{"1"}}}, "has 1 cells, want 2"},
		"table without rows":      {Block{Type: "table", Columns: []string{"in"}}, "rows of cells"},
		"cells on a non-table":    {Block{Type: "diagram", Mermaid: diagram, Cells: [][]string{{"x"}}}, "only apply to table"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := ""
			if err := validateBlock(c.block, snap, true); err != nil {
				got = err.Error()
			}
			if c.wantErr == "" && got != "" || c.wantErr != "" && !strings.Contains(got, c.wantErr) {
				t.Fatalf("error = %q, want %q", got, c.wantErr)
			}
		})
	}
}
