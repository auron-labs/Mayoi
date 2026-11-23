package main

import (
	"mayoi/internal/highlight"
)

// rehighlight gives each document with a file the lexer it has now, as
// when the person's file associations changed.
func (a *app) rehighlight() {
	for _, d := range a.docs {
		if d.path == "" || d.hl == nil || d.language != "" {
			continue
		}
		d.hl.Close()
		d.hl = highlight.New(highlight.Lexer(d.path), a.invalidate)
	}
}
