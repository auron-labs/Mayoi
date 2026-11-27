package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestParseEdit(t *testing.T) {
	edit := `{"changes":{"file:///b.go":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"x"}],
		"file:///a.go":[{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":1}},"newText":"y"}]}}`
	files, err := ParseEdit(json.RawMessage(edit))
	if err != nil || len(files) != 2 || files[0].URI != "file:///a.go" || files[1].URI != "file:///b.go" {
		t.Fatalf("changes = %+v, %v", files, err)
	}
	// documentChanges win over changes, and a document named twice has
	// both lists of edits.
	edit = `{"changes":{"file:///c.go":[]},"documentChanges":[
		{"textDocument":{"uri":"file:///a.go","version":1},"edits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":"1"}]},
		{"textDocument":{"uri":"file:///a.go","version":1},"edits":[{"range":{"start":{"line":2,"character":0},"end":{"line":2,"character":0}},"newText":"2"}]}]}`
	files, err = ParseEdit(json.RawMessage(edit))
	if err != nil || len(files) != 1 || files[0].URI != "file:///a.go" || len(files[0].Edits) != 2 {
		t.Fatalf("documentChanges = %+v, %v", files, err)
	}
	edit = `{"documentChanges":[{"textDocument":{"uri":"file:///a.go"},"edits":[]},{"kind":"rename","oldUri":"file:///a.go","newUri":"file:///b.go"}]}`
	if _, err = ParseEdit(json.RawMessage(edit)); !errors.Is(err, ErrFileOperation) {
		t.Fatalf("a rename of a file: %v", err)
	}
}

// Positions count UTF-16 code units, so the edits of a line with a rune
// outside the basic plane land on the text they name.
func TestApplyEdits(t *testing.T) {
	at := func(line, char int) Position { return Position{line, char} }
	text := "a😀b\nsecond\nthird"
	got, err := ApplyEdits(text, []TextEdit{
		{Range{at(0, 3), at(0, 4)}, "B"},
		{Range{at(1, 0), at(1, 6)}, "2nd"},
		{Range{at(2, 99), at(7, 0)}, "!"},
		{Range{at(0, 0), at(0, 0)}, ">"},
	})
	if want := ">a😀B\n2nd\nthird!"; err != nil || got != want {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
	if _, err = ApplyEdits(text, []TextEdit{{Range{at(1, 0), at(1, 4)}, "x"}, {Range{at(1, 2), at(1, 5)}, "y"}}); err == nil {
		t.Fatal("overlapping edits were applied")
	}
}

// A server's workspace/applyEdit goes to the handler, whose answer is the
// server's, as is the reason it gives for refusing.
func TestApplyEditRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var got json.RawMessage
	var refuse error
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "MAYOI_FAKE_LSP=1")
	c, err := Start(ctx, Options{Cmd: cmd, RootURI: URI(t.TempDir()), Handlers: Handlers{
		ApplyEdit: func(edit json.RawMessage) error { got = edit; return refuse },
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	run := func() string {
		res, err := c.Call(ctx, "workspace/executeCommand", map[string]any{
			"command": "fake.applyEdit", "arguments": []any{map[string]any{"changes": map[string]any{}}}})
		if err != nil {
			t.Fatal(err)
		}
		return string(res)
	}
	if res := run(); res != `{"applied":true}` || string(got) != `{"changes":{}}` {
		t.Fatalf("result %s, edit %s", res, got)
	}
	refuse = errors.New("no")
	if res := run(); res != `{"applied":false}` {
		t.Fatalf("refused: %s", res)
	}
}
