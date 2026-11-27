package lsp

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Highlights are the runs of the document the server says have the name at
// a place, each read, write or text, none for a place with none.
func TestDocumentHighlights(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "MAYOI_FAKE_LSP=1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Start(ctx, Options{Cmd: cmd, RootURI: URI(t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !c.CanHighlight() {
		t.Fatal("the server says it highlights")
	}
	uri := URI("/work/a.fake")
	c.Open(uri, "fake", "def x\nuse x, xy\nnothing\n")
	got, err := c.HighlightsAt(ctx, uri, Position{1, 4})
	want := []Highlight{
		{Range{Position{0, 4}, Position{0, 5}}, 3},
		{Range{Position{1, 4}, Position{1, 5}}, 2},
	}
	if err != nil || len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("highlights = %+v, %v", got, err)
	}
	if got, err := c.HighlightsAt(ctx, uri, Position{2, 2}); err != nil || got != nil {
		t.Errorf("none: %+v, %v", got, err)
	}
}
