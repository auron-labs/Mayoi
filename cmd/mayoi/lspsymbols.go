package main

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"mayoi/internal/bg"
	"mayoi/internal/lsp"
	"mayoi/internal/symbols"
)

// serverSymbols are the symbols of a document that its language server
// listed, which go to symbol and the box of a file's name show in place of
// the heuristics' while they hold.
type serverSymbols struct {
	list []symbols.Symbol
	// version and lines are the text they are of. They hold for the text
	// while the lines are as many, as an edit within a line moves none,
	// until the answer for the new text replaces them.
	version uint64
	lines   int
	// serial changes as list does, for the cache that holds them.
	serial int
	// asked is the version last asked for, and seen the last found, since
	// seenAt: a text changing is asked for once it rests.
	asked, seen uint64
	askedAny    bool
	seenAt      time.Time
	waiting     bool
}

// lspSymbols are the symbols the language server of d lists, nil when none
// holds for its text. It asks the server as they are wanted: at once the
// first time, then once the text rested hoverDwell.
func (a *app) lspSymbols(d *editor) ([]symbols.Symbol, int) {
	s := &d.lspSyms
	snap := d.buf.Snapshot()
	version := snap.Version()
	if !s.askedAny || s.asked != version {
		a.askSymbols(d, s, version)
	}
	if s.list == nil || s.version != version && s.lines != snap.LineCount() {
		return nil, 0
	}
	return s.list, s.serial
}

func (a *app) askSymbols(d *editor, s *serverSymbols, version uint64) {
	if s.seen != version {
		s.seen, s.seenAt = version, time.Now()
	}
	if wait := hoverDwell - time.Since(s.seenAt); s.askedAny && wait > 0 {
		if !s.waiting {
			s.waiting = true
			bg.AfterFunc(wait, func() { a.post(func() { s.waiting = false; a.invalidate() }) })
		}
		return
	}
	servers := a.lspRunningFor(d.document)
	if len(servers) == 0 {
		return
	}
	s.asked, s.askedAny = version, true
	uri, lang, text := serverURI(d.path), languageOf(d.document), d.buf.String()
	bg.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, c := firstReady(servers, 0, func(_ *lspServer, c *lsp.Client) bool { return c.CanListSymbols() })
		if c == nil {
			return
		}
		// The server knows the text as it is now.
		c.Open(uri, lang, text)
		found, err := c.DocumentSymbols(ctx, uri)
		if err != nil {
			return
		}
		a.post(func() {
			// A text that moved on is asked for again by what shows them.
			if d.buf.Version() != version || !allCurrent(servers) {
				return
			}
			snap := d.buf.Snapshot()
			s.list, s.version, s.lines = symbolsOfServer(snap, found), version, snap.LineCount()
			s.serial++
			a.invalidate()
		})
	})
}

// symbolsOfServer are the symbols a server listed for the text of snap, as
// go to symbol lists them: a nesting level is a tab's width.
func symbolsOfServer(snap ui.TextSnapshot, found []lsp.DocumentSymbol) []symbols.Symbol {
	var out []symbols.Symbol
	for _, f := range found {
		at := posRune(snap, fromLSP(f.Start))
		line := snap.LineAt(at)
		start, _ := snap.LineRange(line)
		detail := []rune(f.Detail)
		if len(detail) > 100 {
			detail = append(detail[:97], '…')
		}
		out = append(out, symbols.Symbol{Line: line, Name: f.Name, Detail: string(detail), Kind: lsp.SymbolKind(f.Kind), Depth: f.Depth * 4, Col: at - start})
	}
	return out
}
