package main

import (
	"os"
	"slices"
	"testing"
)

// symbolNames are the names symbolsOf gives d.
func symbolNames(a *app, d *editor) []string {
	_, names := a.symbolsOf(d)
	return names
}

// The symbols of a document are the language server's once it lists them,
// kept while an edit within a line goes unanswered, and the text's own
// before it answers, when a line comes or goes, and for a document with no
// server.
func TestServerSymbols(t *testing.T) {
	hs := withFakeServer(t)
	a := hs.a
	path := hs.path("a.fake")
	os.WriteFile(path, []byte("sym top\n  sym child\nfunc heur()\n"), 0o644)
	a.openFile(path, true)
	d := a.activeEditor()
	serverUp(hs)

	if names := symbolNames(a, d); !slices.Equal(names, []string{"heur"}) {
		t.Fatalf("before the server answered: %q", names)
	}
	hs.until("the server's symbols", func() bool { return slices.Equal(symbolNames(a, d), []string{"top", "child"}) })
	syms, _ := a.symbolsOf(d)
	if top, child := syms[0], syms[1]; top.Line != 0 || top.Col != 4 || top.Depth != 0 || top.Kind != "function" || top.Detail != "()" ||
		child.Line != 1 || child.Col != 6 || child.Depth != 4 {
		t.Errorf("symbols = %+v", syms)
	}

	// An edit within a line shows the list as it was until the new one comes.
	a.queueEdit(d, func(d *editor) { d.edit(d.sel, textEdit{25, 25, "x"}) })
	hs.settle()
	if names := symbolNames(a, d); !slices.Equal(names, []string{"top", "child"}) {
		t.Fatalf("after an edit within a line: %q", names)
	}
	a.queueEdit(d, func(d *editor) { d.edit(d.sel, textEdit{14, 19, "kid"}) })
	hs.until("the new symbols", func() bool { return slices.Equal(symbolNames(a, d), []string{"top", "kid"}) })

	// A line more: the text's until the server's, for the text as it is.
	a.queueEdit(d, func(d *editor) { d.edit(d.sel, textEdit{0, 0, "\n"}) })
	hs.settle()
	if names := symbolNames(a, d); !slices.Equal(names, []string{"xheur"}) {
		t.Fatalf("after a line came: %q", names)
	}
	hs.until("the server's symbols again", func() bool { return slices.Equal(symbolNames(a, d), []string{"top", "kid"}) })
	if syms, _ = a.symbolsOf(d); syms[0].Line != 1 {
		t.Errorf("top is on line %d, want 1", syms[0].Line)
	}

	// No server serves it.
	other := hs.path("b.txt")
	os.WriteFile(other, []byte("sym top\nfunc heur()\n"), 0o644)
	a.openFile(other, true)
	hs.settle()
	if names := symbolNames(a, a.activeEditor()); !slices.Equal(names, []string{"heur"}) {
		t.Fatalf("with no server: %q", names)
	}
}
