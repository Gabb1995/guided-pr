package web

import (
	"bytes"
	"html/template"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

var (
	formatter  = html.New(html.WithClasses(true), html.PreventSurroundingPre(true))
	style      = styles.Get("github")
	lexerCache sync.Map // path -> chroma.Lexer
	lineCache  sync.Map // path \x00 text -> string
)

func lexerFor(path string) chroma.Lexer {
	if cached, ok := lexerCache.Load(path); ok {
		return cached.(chroma.Lexer)
	}
	lexer := lexers.Match(path)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)
	lexerCache.Store(path, lexer)
	return lexer
}

// highlight returns one source line as escaped HTML with chroma token classes.
func highlight(path, text string) string {
	key := path + "\x00" + text
	if cached, ok := lineCache.Load(key); ok {
		return cached.(string)
	}
	out := template.HTMLEscapeString(text)
	if tokens, err := lexerFor(path).Tokenise(nil, text); err == nil {
		var buf bytes.Buffer
		if formatter.Format(&buf, style, tokens) == nil {
			out = buf.String()
		}
	}
	lineCache.Store(key, out)
	return out
}
