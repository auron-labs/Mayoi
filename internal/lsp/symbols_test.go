package lsp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

func documentSymbolsOf(t *testing.T, init string, mappings []PathMapping, uri, text string) []DocumentSymbol {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "MAYOI_FAKE_LSP=1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Start(ctx, Options{Cmd: cmd, RootURI: URI(t.TempDir()), InitOptions: json.RawMessage(init), PathMappings: mappings})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !c.CanListSymbols() {
		t.Fatal("the server says it lists symbols")
	}
	c.Open(uri, "fake", text)
	got, err := c.DocumentSymbols(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// A server's nested symbols come flat, in the order of the document, each
// with how deep it is and where its name starts.
func TestDocumentSymbolsNested(t *testing.T) {
	got := documentSymbolsOf(t, `{}`, nil, URI("/work/a.fake"), "sym top\n  sym child\nplain\nsym next\n")
	want := []DocumentSymbol{
		{"top", "()", 12, 0, Position{0, 4}},
		{"child", "()", 12, 1, Position{1, 6}},
		{"next", "()", 12, 0, Position{3, 4}},
	}
	if len(got) != len(want) {
		t.Fatalf("symbols = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("symbol %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Symbol information comes as
// symbols with its container for their detail and its range's start, in a folder
// whose paths are mapped.
func TestDocumentSymbolsFlat(t *testing.T) {
	got := documentSymbolsOf(t, `{"flatSymbols": true}`,
		[]PathMapping{{ClientURI: "file:///client", ServerURI: "file:///server"}}, "file:///client/a.fake", "sym one\n  sym two\n")
	want := []DocumentSymbol{{"one", "pkg", 12, 0, Position{0, 0}}, {"two", "pkg", 12, 0, Position{1, 0}}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("symbols = %+v, want %+v", got, want)
	}
}
