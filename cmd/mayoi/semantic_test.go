package main

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"

	"mayoi/internal/highlight"
	"mayoi/internal/lsp"
)

func TestSemanticLegendKinds(t *testing.T) {
	legend := newSemanticLegend(lsp.SemanticLegend{
		Types:     []string{"function", "variable", "property", "parameter", "type", "unheard"},
		Modifiers: []string{"static", "readonly", "defaultLibrary", "deprecated"},
	})
	for _, c := range []struct {
		name string
		tok  lsp.SemanticToken
		want highlight.Kind
	}{
		{"a function", lsp.SemanticToken{Type: 0}, highlight.Function},
		{"a library function", lsp.SemanticToken{Type: 0, Modifiers: 1 << 2}, highlight.Builtin},
		{"a read-only variable", lsp.SemanticToken{Type: 1, Modifiers: 1 << 1}, highlight.Constant},
		{"a read-only property", lsp.SemanticToken{Type: 2, Modifiers: 1 << 1}, highlight.Constant},
		{"a read-only parameter", lsp.SemanticToken{Type: 3, Modifiers: 1 << 1}, highlight.Variable},
		{"a static type", lsp.SemanticToken{Type: 4, Modifiers: 1}, highlight.Type},
		{"a type of no colour", lsp.SemanticToken{Type: 5}, highlight.Plain},
		{"a type out of the legend", lsp.SemanticToken{Type: 9}, highlight.Plain},
	} {
		if got := legend.kind(c.tok); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// Tokens are put over the lexer's where they have a colour, cutting its
// tokens, and in UTF-16 units they are placed by runes.
func TestSemanticLinesAndMerge(t *testing.T) {
	snap := ui.NewTextBuffer("a\U0001F600 foo(bar) // x\n").Snapshot()
	legend := newSemanticLegend(lsp.SemanticLegend{Types: []string{"function", "parameter", "bad"}, Modifiers: []string{"deprecated"}})
	lines := semanticLines(snap, []lsp.SemanticToken{
		{Line: 0, Start: 3, Length: 3, Type: 0, Modifiers: 1}, // foo, after a two-unit emoji
		{Line: 0, Start: 7, Length: 3, Type: 1},               // bar
		{Line: 0, Start: 8, Length: 1, Type: 1},               // overlaps bar
		{Line: 0, Start: 30, Length: 3, Type: 1},              // past the line
		{Line: 5, Start: 0, Length: 1, Type: 1},               // past the text
	}, legend)
	want := []semanticToken{{2, 5, highlight.Function, true}, {6, 9, highlight.Variable, false}}
	if len(lines) != 2 || !slices.Equal(lines[0].tokens, want) {
		t.Fatalf("tokens %+v, want %+v", lines, want)
	}
	lexed := []highlight.Token{{Start: 0, End: 1, Kind: highlight.Keyword}, {Start: 2, End: 12, Kind: highlight.Type}, {Start: 13, End: 17, Kind: highlight.Comment}}
	got := mergeSemantic(nil, lexed, lines[0].tokens)
	wantTokens := []highlight.Token{
		{Start: 0, End: 1, Kind: highlight.Keyword},
		{Start: 2, End: 5, Kind: highlight.Function},
		{Start: 5, End: 6, Kind: highlight.Type},
		{Start: 6, End: 9, Kind: highlight.Variable},
		{Start: 9, End: 12, Kind: highlight.Type},
		{Start: 13, End: 17, Kind: highlight.Comment},
	}
	if !slices.Equal(got, wantTokens) {
		t.Fatalf("merged %+v, want %+v", got, wantTokens)
	}
}

// openSemantic opens a Go file that a fake server tells the tokens of:
// foo is a function, bar a parameter, and baz deprecated.
func openSemantic(t *testing.T, text string) (*harness, *editor) {
	t.Setenv("MAYOI_FAKE_LSP", "1")
	options, _ := json.Marshal(map[string]any{"semantic": map[string]any{
		"legend": map[string]any{"types": []string{"function", "parameter"}, "modifiers": []string{"deprecated"}},
		"words":  map[string]any{"foo": []int{0, 0}, "bar": []int{1, 0}, "baz": []int{0, 1}},
	}})
	manifest, _ := json.Marshal(map[string]any{
		"name": "fake",
		"contributes": map[string]any{
			"languageServers": []any{map[string]any{"id": "fake.server", "languages": []string{"go"}, "command": os.Args[0],
				"args": []string{"-test.run=^$"}, "initializationOptions": json.RawMessage(options)}},
		},
	})
	hs := newHarness(t, false)
	withPlugins(hs, map[string]map[string]string{"fake": {"plugin.json": string(manifest)}})
	t.Cleanup(stopServers)
	os.WriteFile(hs.path("a.go"), []byte(text), 0o644)
	hs.a.openFile(hs.path("a.go"), true)
	d := hs.a.activeEditor()
	serverUp(hs)
	return hs, d
}

// colorAt is the colour the editor gives rune at of d, the text's if none.
func colorAt(d *editor, sem *semanticState, at int) ui.Color {
	cc := colorCache{sem: sem}
	snap := d.buf.Snapshot()
	for _, r := range cc.ranges(d.hl, darkPal, snap, 0, snap.LineCount()-1, 700) {
		if at >= r.Start && at < r.End {
			return r.Color
		}
	}
	return darkPal.text
}

func TestSemanticTokensRecolour(t *testing.T) {
	hs, d := openSemantic(t, "package main\n\nfoo bar\nbaz\n")
	text := d.buf.String()
	lexed := func(at int) ui.Color { return colorAt(d, nil, at) }
	semantic := func(at int) ui.Color { return colorAt(d, &d.semantic, at) }
	const foo, bar = 14, 18
	hs.until("the semantic tokens", func() bool { return d.semantic.gen > 0 })
	if lexed(foo) != darkPal.text || semantic(foo) != darkPal.syntax[highlight.Function] || semantic(bar) != darkPal.syntax[highlight.Variable] {
		t.Fatalf("colours: lexed foo %v, foo %v, bar %v", lexed(foo), semantic(foo), semantic(bar))
	}
	if d.buf.String() != text {
		t.Fatal("the tokens changed the text")
	}
	// A deprecated name is struck through.
	cc := colorCache{sem: &d.semantic}
	cc.ranges(d.hl, darkPal, d.buf.Snapshot(), 0, 3, 700)
	if want := []lineRun{{3, 0, 3}}; !slices.Equal(cc.strikes, want) {
		t.Errorf("strikes %+v, want %+v", cc.strikes, want)
	}

	// An answer to a question since asked again is dropped.
	gen := d.semantic.gen
	hs.a.lspDoc("change", d.document)
	d.semantic.asked++
	hs.settle()
	if d.semantic.gen != gen {
		t.Error("a stale answer was kept")
	}

	// Lines added above keep their tokens where they move to, until the
	// server answers again; a line edited has none.
	d.edit([2]int{0, 0}, textEdit{0, 0, "// one\n// two\n"})
	if semantic(foo+14) != darkPal.syntax[highlight.Function] {
		t.Error("the tokens did not move with the lines")
	}
	d.edit([2]int{0, 0}, textEdit{foo + 14 + 1, foo + 14 + 1, "x"})
	if semantic(foo+14) != darkPal.text || semantic(bar+14+1) != darkPal.text {
		t.Error("an edited line kept its tokens")
	}
	if semantic(bar+14+1) != lexed(bar+14+1) {
		t.Error("an edited line differs from the lexer's")
	}

	// Off, the lexer's colours alone show.
	hs.a.settings.SemanticHighlighting = false
	if hs.a.semanticFor(d.document) != nil {
		t.Error("tokens used with the setting off")
	}
}
