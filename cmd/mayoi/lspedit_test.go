package main

import (
	"encoding/json"
	"os"
	"testing"

	"mayoi/internal/lsp"
)

// replaceEdit is a WorkspaceEdit that puts "new" in the columns of the
// first line of each file, through changes.
func replaceEdit(t *testing.T, columns map[string][2]int) json.RawMessage {
	t.Helper()
	changes := map[string]any{}
	for path, cols := range columns {
		changes[lsp.URI(path)] = []lsp.TextEdit{{
			Range:   lsp.Range{Start: lsp.Position{Line: 0, Character: cols[0]}, End: lsp.Position{Line: 0, Character: cols[1]}},
			NewText: "new",
		}}
	}
	raw, _ := json.Marshal(map[string]any{"changes": changes})
	return raw
}

// A workspace edit changes the open document as one step of undo, and the
// file not open on disk, in its line endings; one that renames a file
// changes neither.
func TestApplyWorkspaceEdit(t *testing.T) {
	hs := newHarness(t, false)
	a := hs.a
	open, closed := hs.path("open.txt"), hs.path("closed.txt")
	os.WriteFile(open, []byte("old one\nold two\n"), 0o644)
	os.WriteFile(closed, []byte("old a\r\nold b\r\n"), 0o644)
	a.openFile(open, true)
	hs.settle()
	d := a.activeEditor()

	apply := func(raw json.RawMessage) (int, error) {
		t.Helper()
		var files int
		var err error
		finished := false
		a.applyEdit(a.root, raw, func(n int, e error) { files, err, finished = n, e, true })
		hs.until("the edit made", func() bool { return finished })
		return files, err
	}

	rename := json.RawMessage(`{"documentChanges":[{"kind":"rename","oldUri":"` + lsp.URI(closed) + `","newUri":"` + lsp.URI(hs.path("moved.txt")) + `"}]}`)
	if _, err := apply(rename); err == nil {
		t.Fatal("an edit that renames a file was made")
	}

	n, err := apply(replaceEdit(t, map[string][2]int{open: {0, 3}, closed: {0, 3}}))
	if err != nil || n != 2 {
		t.Fatalf("applied to %d files: %v", n, err)
	}
	if got := d.buf.String(); got != "new one\nold two\n" {
		t.Fatalf("open document = %q", got)
	}
	if data, _ := os.ReadFile(closed); string(data) != "new a\r\nold b\r\n" {
		t.Fatalf("file on disk = %q", data)
	}
	d.undo()
	hs.settle()
	if got := d.buf.String(); got != "old one\nold two\n" {
		t.Fatalf("after undo = %q", got)
	}
	// A file that cannot be read leaves the others as they were.
	if n, err = apply(replaceEdit(t, map[string][2]int{open: {0, 3}, hs.path("missing.txt"): {0, 3}})); err == nil || n != 0 {
		t.Fatalf("missing file: %d, %v", n, err)
	}
	if got := d.buf.String(); got != "old one\nold two\n" {
		t.Fatalf("open document after a failed edit = %q", got)
	}
}
