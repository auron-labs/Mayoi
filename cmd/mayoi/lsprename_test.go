package main

import (
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

// Rename Symbol asks for the new name, starting from the name at the caret,
// and the rename it makes goes to the open document, as one step of undo,
// and to the file of the folder that is not open, and says how many files
// it changed. F2 in the editor asks. Where the server finds nothing to rename, it says so.
func TestRenameSymbol(t *testing.T) {
	hs := withFakeServer(t)
	a := hs.a
	path, other := hs.path("a.fake"), hs.path("other.fake")
	os.WriteFile(path, []byte("use foo\nnothing\n"), 0o644)
	os.WriteFile(other, []byte("also foo\n"), 0o644)
	a.openFile(path, true)
	d := a.activeEditor()
	serverUp(hs)

	hs.place(d, 9, 9) // in "nothing"
	a.run("edit.rename")
	hs.until("nothing to rename told", func() bool { return hs.tt.HasText("Nothing to rename here") })
	if a.prompt != nil {
		t.Fatal("asked for a name where nothing can be renamed")
	}

	hs.place(d, 5, 5) // in "foo"
	hs.tt.Key(0, ui.KeyF2)
	hs.until("the name asked for", func() bool { return a.prompt != nil })
	if a.prompt.value != "foo" {
		t.Fatalf("asked from %q", a.prompt.value)
	}
	a.prompt.done("bar")
	hs.until("the other file renamed", func() bool {
		data, _ := os.ReadFile(other)
		return string(data) == "also bar\n"
	})
	hs.until("the open document renamed", func() bool { return d.buf.String() == "use bar\nnothing\n" })
	hs.until("the files told", func() bool { return hs.tt.HasText("Renamed in 2 files") })
	d.undo()
	hs.settle()
	if got := d.buf.String(); got != "use foo\nnothing\n" {
		t.Fatalf("after undo = %q", got)
	}
}
