package main

import (
	"os"
	"slices"
	"testing"
)

// highlightsOf opens a fake file of text with its server up.
func highlightsOf(hs *harness, text string) *editor {
	path := hs.path("a.fake")
	os.WriteFile(path, []byte(text), 0o644)
	hs.a.openFile(path, true)
	d := hs.a.activeEditor()
	serverUp(hs)
	return d
}

// The runs the server finds for the name at the caret are marked in place
// of the text that is the same name, once the caret rested: the text's
// show until they come.
func TestServerHighlights(t *testing.T) {
	hs := withFakeServer(t)
	d := highlightsOf(hs, "x hold\nx\nhold\n")

	// The text's marks of a name until the server answers.
	d.selectRange(3, 3, false)
	holdLate(hs)
	if w := d.deco.words; len(w) != 2 || w[0].line != 0 || w[1].line != 2 || len(d.wordMarks.spans) != 0 {
		t.Fatalf("before the answer: %+v", w)
	}
	releaseLate(hs)
	if len(d.wordMarks.spans) != 2 {
		t.Fatalf("the server's runs: %v", d.wordMarks.spans)
	}

	// A name of one letter is not one the text finds.
	hs.place(d, 7, 7)
	hs.until("the server's runs", func() bool { return len(d.deco.words) == 2 })
	if w := d.deco.words; w[0].line != 0 || w[1].line != 1 {
		t.Errorf("marks on lines %d and %d, want 0 and 1", w[0].line, w[1].line)
	}
}

// What the server answers for a caret that moved, or a text that changed,
// since it was asked marks nothing.
func TestServerHighlightsTooLate(t *testing.T) {
	hs := withFakeServer(t)
	a := hs.a
	d := highlightsOf(hs, "x hold\nx\nhold\n")
	for _, move := range []func(){
		func() { hs.place(d, 0, 0) },
		func() { a.queueEdit(d, func(d *editor) { d.edit(d.sel, textEdit{0, 0, "y"}) }); hs.settle() },
	} {
		d.selectRange(3, 3, false)
		holdLate(hs)
		move()
		releaseLate(hs)
		if slices.Contains(d.wordMarks.spans, [2]int{2, 6}) {
			t.Fatalf("an answer of an earlier caret or text was kept: %v", d.wordMarks.spans)
		}
	}
}
