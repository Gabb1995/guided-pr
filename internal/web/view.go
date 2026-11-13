package web

import (
	"github.com/Gabb1995/guided-pr/internal/deck"
	"github.com/Gabb1995/guided-pr/internal/forge"
	"github.com/Gabb1995/guided-pr/internal/session"
)

// hunkView is a hunk ready to render: rows carry both sides' line numbers and highlighted HTML.
type hunkView struct {
	Path      string    `json:"path"`
	Additions int       `json:"additions"`
	Deletions int       `json:"deletions"`
	Rows      []hunkRow `json:"rows"`
}

type hunkRow struct {
	Kind string `json:"t"`
	Old  int    `json:"o,omitempty"` // line number on the old side; 0 for added lines
	New  int    `json:"n,omitempty"` // line number on the new side; 0 for removed lines
	HTML string `json:"html"`
}

func hunkViews(snap session.Snapshot) map[string]hunkView {
	out := map[string]hunkView{}
	for _, f := range snap.Files {
		for _, h := range f.Hunks {
			v := hunkView{Path: f.Path}
			for _, l := range forge.HunkLines(h) {
				row := hunkRow{Kind: l.Kind, HTML: highlight(f.Path, l.Text)}
				switch l.Kind {
				case "add":
					row.New = l.NewPos
					v.Additions++
				case "del":
					row.Old = l.OldPos
					v.Deletions++
				default:
					row.Old, row.New = l.OldPos, l.NewPos
				}
				v.Rows = append(v.Rows, row)
			}
			out[h.ID] = v
		}
	}
	return out
}

// highlightBlocks adds highlighted HTML to resolved code rows.
func highlightBlocks(blocks []*deck.Block) {
	var walk func(b *deck.Block)
	walk = func(b *deck.Block) {
		if b == nil {
			return
		}
		for i := range b.Rows {
			b.Rows[i].HTML = highlight(b.Path, b.Rows[i].Text)
		}
		walk(b.Left)
		walk(b.Right)
	}
	for _, b := range blocks {
		walk(b)
	}
}
