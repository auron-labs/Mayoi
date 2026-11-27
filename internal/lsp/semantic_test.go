package lsp

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"
)

func TestDecodeSemanticTokens(t *testing.T) {
	// A line change resets the start; a token on the same line is relative
	// to the one before; a trailing partial token is dropped.
	got := decodeSemanticTokens([]uint32{0, 2, 3, 1, 0, 0, 5, 2, 4, 3, 2, 1, 4, 0, 0, 9, 9})
	want := []SemanticToken{{0, 2, 3, 1, 0}, {0, 7, 2, 4, 3}, {2, 1, 4, 0, 0}}
	if !slices.Equal(got, want) {
		t.Fatalf("decoded %+v, want %+v", got, want)
	}
}

func TestSemanticTokensDelta(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "MAYOI_FAKE_LSP=1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	refreshed := make(chan struct{}, 1)
	c, err := Start(ctx, Options{Cmd: cmd, RootURI: URI(t.TempDir()),
		InitOptions: []byte(`{"semantic":{"legend":{"types":["variable","function"],"modifiers":["readonly"]},"words":{"foo":[0,1],"bar":[1,0]}}}`),
		Handlers:    Handlers{RefreshSemanticTokens: func() { refreshed <- struct{}{} }}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !c.SemanticTokens() || !slices.Equal(c.SemanticLegend().Types, []string{"variable", "function"}) {
		t.Fatalf("provider %+v", c.semantic)
	}
	uri := URI("/work/a.fake")
	c.Open(uri, "fake", "foo bar\né\U0001F600 foo\n")
	tokens, err := c.SemanticTokensOf(ctx, uri)
	want := []SemanticToken{{0, 0, 3, 0, 1}, {0, 4, 3, 1, 0}, {1, 4, 3, 0, 1}} // the emoji is two UTF-16 units
	if err != nil || !slices.Equal(tokens, want) {
		t.Fatalf("full = %+v, %v", tokens, err)
	}
	// The second answer is a delta, applied to the data of the first.
	c.Open(uri, "fake", "bar\nfoo\n")
	tokens, err = c.SemanticTokensOf(ctx, uri)
	want = []SemanticToken{{0, 0, 3, 1, 0}, {1, 0, 3, 0, 1}}
	if err != nil || !slices.Equal(tokens, want) {
		t.Fatalf("delta = %+v, %v", tokens, err)
	}
	// A document closed has no result to ask a delta of.
	c.Closed(uri)
	c.Open(uri, "fake", "foo\n")
	if tokens, err = c.SemanticTokensOf(ctx, uri); err != nil || len(tokens) != 1 {
		t.Fatalf("after reopening = %+v, %v", tokens, err)
	}
	// A server that lost the result a delta is of is asked for all.
	c.mu.Lock()
	c.tokens[uri] = semanticState{id: "lost", data: []uint32{1}}
	c.mu.Unlock()
	if tokens, err = c.SemanticTokensOf(ctx, uri); err != nil || len(tokens) != 1 {
		t.Fatalf("after a refused delta = %+v, %v", tokens, err)
	}
	c.Call(ctx, "test/refresh", nil)
	select {
	case <-refreshed:
	case <-ctx.Done():
		t.Fatal("refresh not told")
	}
}
