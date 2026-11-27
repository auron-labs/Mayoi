package main

import (
	"context"
	"hash/maphash"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"mayoi/internal/bg"
	"mayoi/internal/highlight"
	"mayoi/internal/lsp"
)

// Semantic highlighting: what a language server says each name is, over
// the lexer's colours. The tokens of a document are kept by line, each
// line with a hash of the text it was answered for, so that an edit leaves
// the colours of the lines it did not touch where they are, and a line it
// did touch shows the lexer's until the next answer.

// semanticToken is a run of a line, in runes, coloured as kind; kind is
// highlight.Plain for one the lexer's colours stay under, as one only
// deprecated.
type semanticToken struct {
	start, end int32
	kind       highlight.Kind
	deprecated bool
}

// semanticLine is the tokens of a line, and the hash of the text they are
// of.
type semanticLine struct {
	hash   uint64
	tokens []semanticToken
}

// semanticState is the semantic tokens of a document.
type semanticState struct {
	lines []semanticLine
	// gen changes with the tokens, which what was built from them follows.
	gen uint64
	// asked counts the questions asked: only the answer to the last is kept.
	asked int
	// seen is whether the setting was on as the document was last looked at.
	seen bool
}

var semanticSeed = maphash.MakeSeed()

// at returns the tokens of line i of a text of n lines, if they are of text:
// those of the line as it is, or as it was before lines were added or
// removed above it.
func (s *semanticState) at(i, n int, text string) []semanticToken {
	if s == nil || len(s.lines) == 0 {
		return nil
	}
	var hash uint64
	hashed := false
	for _, j := range [2]int{i, i - (n - len(s.lines))} {
		if j < 0 || j >= len(s.lines) || s.lines[j].tokens == nil {
			continue
		}
		if !hashed {
			hash, hashed = maphash.String(semanticSeed, text), true
		}
		if s.lines[j].hash == hash {
			return s.lines[j].tokens
		}
	}
	return nil
}

// semanticKinds are the kinds the types of the protocol are coloured as,
// those of the colours the lexers' tokens have.
var semanticKinds = map[string]highlight.Kind{
	"namespace": highlight.Type, "type": highlight.Type, "class": highlight.Type, "enum": highlight.Type,
	"interface": highlight.Type, "struct": highlight.Type, "typeParameter": highlight.Type,
	"parameter": highlight.Variable, "variable": highlight.Variable, "property": highlight.Attribute,
	"enumMember": highlight.Constant, "event": highlight.Attribute, "function": highlight.Function,
	"method": highlight.Function, "macro": highlight.Function, "keyword": highlight.Keyword,
	"modifier": highlight.Keyword, "comment": highlight.Comment, "string": highlight.String,
	"number": highlight.Number, "regexp": highlight.String, "operator": highlight.Operator,
	"decorator": highlight.Attribute,
}

// semanticLegend is what the numbers of a server's tokens mean: the kind of
// each type, and the bits of the modifiers that change it.
type semanticLegend struct {
	types                         []string
	readonly, library, deprecated uint32
}

func newSemanticLegend(l lsp.SemanticLegend) semanticLegend {
	out := semanticLegend{types: l.Types}
	for i, m := range l.Modifiers {
		switch m {
		case "readonly":
			out.readonly = 1 << i
		case "defaultLibrary":
			out.library = 1 << i
		case "deprecated":
			out.deprecated = 1 << i
		}
	}
	return out
}

// kind is the kind a token is coloured as: its type's, a constant when
// read-only and the library's when from the language's own, Plain when
// the lexer's colour stays. static and the other modifiers change no
// colour.
func (l semanticLegend) kind(t lsp.SemanticToken) highlight.Kind {
	if t.Type < 0 || t.Type >= len(l.types) {
		return highlight.Plain
	}
	typ := l.types[t.Type]
	kind := semanticKinds[typ]
	switch {
	case t.Modifiers&l.readonly != 0 && (typ == "variable" || typ == "property"):
		return highlight.Constant
	case t.Modifiers&l.library != 0 && (kind == highlight.Function || kind == highlight.Variable):
		return highlight.Builtin
	}
	return kind
}

// semanticLines is the tokens of the text snap, by line, from a server's
// answer to it. The tokens it has no colour for and no mark are left out.
func semanticLines(snap ui.TextSnapshot, tokens []lsp.SemanticToken, legend semanticLegend) []semanticLine {
	lines := make([]semanticLine, snap.LineCount())
	var text string
	var ascii bool
	var runes int
	row := -1
	for _, t := range tokens {
		kind, deprecated := legend.kind(t), t.Modifiers&legend.deprecated != 0
		if t.Line < 0 || t.Line >= len(lines) || kind == highlight.Plain && !deprecated {
			continue
		}
		if t.Line != row {
			row, text = t.Line, snap.Line(t.Line)
			ascii = isASCII(text)
			runes = len(text)
			if !ascii {
				runes = utf8.RuneCountInString(text)
			}
			lines[row].hash = maphash.String(semanticSeed, text)
		}
		start, end := t.Start, t.Start+t.Length
		if !ascii {
			start, end = utf16ToRunes(text, start), utf16ToRunes(text, end)
		}
		end = min(end, runes)
		// A server may overlap its tokens, which painting does not take.
		if prev := lines[row].tokens; start >= end || len(prev) > 0 && start < int(prev[len(prev)-1].end) {
			continue
		}
		lines[row].tokens = append(lines[row].tokens, semanticToken{int32(start), int32(end), kind, deprecated})
	}
	return lines
}

// mergeSemantic puts the semantic tokens over the lexer's of a line: the
// lexer's tokens keep their colours where no semantic token with one is.
// Both are in order, apart.
func mergeSemantic(dst, lexed []highlight.Token, sem []semanticToken) []highlight.Token {
	dst = dst[:0]
	pos := 0 // the start of what is left of the lexed tokens
	next := 0
	// lexedUpTo adds the lexed tokens, or what is left of them, before x.
	lexedUpTo := func(x int) {
		for ; next < len(lexed) && lexed[next].Start < x; next++ {
			t := lexed[next]
			t.Start = max(t.Start, pos)
			if t.End > x {
				// A token cut at x goes on, from x, with the next.
				dst = append(dst, highlight.Token{Start: t.Start, End: x, Kind: t.Kind})
				return
			}
			if t.Start < t.End {
				dst = append(dst, t)
			}
		}
	}
	for _, s := range sem {
		if s.kind == highlight.Plain {
			continue
		}
		lexedUpTo(int(s.start))
		dst = append(dst, highlight.Token{Start: int(s.start), End: int(s.end), Kind: s.kind})
		pos = int(s.end)
	}
	lexedUpTo(1 << 30)
	return dst
}

// semanticWait is how long the answer of a server is waited for.
const semanticWait = 10 * time.Second

// askSemantic asks the first server of a document that runs and has
// semantic tokens for them, as the text now is, which lspDoc told it of.
func (a *app) askSemantic(d *document, servers []*lspServer) {
	st := &d.semantic
	if !a.prefs().SemanticHighlighting || len(servers) == 0 {
		return
	}
	st.asked++
	n, snap, uri := st.asked, d.buf.Snapshot(), serverURI(d.path)
	bg.Go(func() {
		s, c := firstReady(servers, 5*time.Second, func(_ *lspServer, c *lsp.Client) bool { return c.SemanticTokens() })
		// A server not told of the document yet is, as it starts, and asked
		// then.
		if c == nil || !c.IsOpen(uri) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), semanticWait)
		defer cancel()
		tokens, err := c.SemanticTokensOf(ctx, uri)
		if err != nil {
			s.log("warn", s.contrib.ID+": semantic tokens: "+err.Error())
			return
		}
		lines := semanticLines(snap, tokens, newSemanticLegend(c.SemanticLegend()))
		a.post(func() {
			if st.asked != n || !s.current() {
				return
			}
			st.lines, st.gen = lines, st.gen+1
			a.invalidate()
		})
	})
}

// semanticToggled is the setting turned on or off for d: on asks for the
// tokens, off forgets them.
func (a *app) semanticToggled(d *document) {
	st := &d.semantic
	st.seen = a.prefs().SemanticHighlighting
	if st.seen {
		a.lspDoc("change", d)
		return
	}
	st.asked++
	st.lines, st.gen = nil, st.gen+1
}

// semanticFor are the tokens of a document to colour it with, nil when the
// setting is off.
func (a *app) semanticFor(d *document) *semanticState {
	if !a.prefs().SemanticHighlighting {
		return nil
	}
	return &d.semantic
}
