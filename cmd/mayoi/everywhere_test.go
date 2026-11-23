package main

import (
	"fmt"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mayoi/internal/lsp"
)

// settleEverywhere draws frames until Search Everywhere's search of the
// text, under way or waiting for typing to pause, is done.
func (hs *harness) settleEverywhere() {
	e := &hs.a.everywhere
	deadline := time.Now().Add(5 * time.Second)
	for {
		hs.settle()
		if !e.running && !e.waiting && e.asking == 0 || time.Now().After(deadline) {
			break
		}
	}
	hs.settle()
}

// tapShift presses Shift on its own and lets it go, a frame each, as a
// person taps it.
func (hs *harness) tapShift() {
	hs.tt.HoldModifiers(ui.Shift)
	step(hs.tt)
	hs.tt.HoldModifiers(0)
	step(hs.tt)
}

func TestShiftTwiceOpensSearchEverywhere(t *testing.T) {
	hs := newHarness(t, true)
	a := hs.a
	a.openFile(hs.path("main.go"), true)
	hs.settle()

	hs.tapShift()
	if a.everywhere.open {
		t.Fatal("one tap of Shift opened Search Everywhere")
	}
	hs.tapShift()
	if !a.everywhere.open {
		t.Fatal("two taps of Shift did not open Search Everywhere")
	}
	hs.key(0, ui.KeyEscape)
	if a.everywhere.open {
		t.Fatal("Escape left Search Everywhere open")
	}

	// Over another dialog, the dialog keeps the keyboard.
	a.run("view.selectTheme")
	hs.settle()
	hs.tapShift()
	hs.tapShift()
	if a.everywhere.open {
		t.Fatal("two taps of Shift opened Search Everywhere over the theme picker")
	}
	hs.key(0, ui.KeyEscape)

	// Taps too far apart.
	hs.tapShift()
	hs.tt.Advance(shiftTapWithin)
	hs.tapShift()
	if a.everywhere.open {
		t.Fatal("taps of Shift far apart opened Search Everywhere")
	}
	hs.settle()

	// Shift held to type a capital, then tapped, as for "A(": the key
	// comes with Shift on macOS and Windows, and on Linux only as its
	// text and its release, which for a symbol has only its position.
	for _, typed := range []func(){
		func() { hs.tt.Key(ui.Shift, ui.KeyA) },
		func() { hs.tt.Type("A"); hs.tt.KeyUp(ui.KeyA) },
		func() { hs.tt.Type("("); hs.tt.KeyAt(ui.Shift, ui.Key9) },
	} {
		hs.tt.HoldModifiers(ui.Shift)
		step(hs.tt)
		typed()
		step(hs.tt)
		hs.tt.HoldModifiers(0)
		step(hs.tt)
		hs.tapShift()
		if a.everywhere.open {
			t.Fatal("typing a capital, then tapping Shift, opened Search Everywhere")
		}
		hs.settle()
	}
}

func TestSearchEverywhereFindsFilesAndText(t *testing.T) {
	hs := newHarness(t, true)
	a, tt := hs.a, hs.tt
	e := &a.everywhere
	a.run("go.searchEverywhere")
	hs.settle()
	tt.Type("main")
	hs.settleEverywhere()

	if len(e.names) == 0 || e.files[e.names[0].Index] != "main.go" {
		t.Fatalf("files %v; want main.go first", e.names)
	}
	var lines []string
	for _, l := range e.lines {
		lines = append(lines, fmt.Sprintf("%s:%d", l.file.rel, l.match.line+1))
	}
	slices.Sort(lines)
	if want := []string{"main.go:1", "main.go:5", "main.go:6"}; !slices.Equal(lines, want) {
		t.Fatalf("lines %v; want %v", lines, want)
	}
	if !tt.HasText("Files") || !tt.HasText("Text") || !tt.HasText("main.go:6") {
		t.Errorf("sections or lines missing: %q", tt.Texts())
	}
	hs.shot("search-everywhere")

	// Return on a line opens its file with the match selected.
	at := slices.IndexFunc(e.lines, func(l textLine) bool { return l.match.line == 5 })
	e.sel = len(e.names) + at
	hs.key(0, ui.KeyEnter)
	d := a.activeEditor()
	if e.open || d == nil || d.path != hs.path("main.go") {
		t.Fatalf("open %v, editor %v", e.open, d)
	}
	if got := d.buf.Slice(d.sel[0], d.sel[1]); got != "main" || d.buf.Snapshot().LineAt(d.sel[0]) != 5 {
		t.Errorf("selected %q on line %d", got, d.buf.Snapshot().LineAt(d.sel[0]))
	}

	// Opened again, it keeps the query, selected, to type over.
	a.run("go.searchEverywhere")
	hs.settle()
	tt.Type("zzzz-nothing")
	hs.settleEverywhere()
	if e.query != "zzzz-nothing" || len(e.names) != 0 || len(e.lines) != 0 || !tt.HasText("No results found") {
		t.Errorf("query %q, %d files, %d lines: %q", e.query, len(e.names), len(e.lines), tt.Texts())
	}
}

// The text lists a line once, at its first match, and stops at
// everywhereLines, offering the search view for the rest.
func TestSearchEverywhereLines(t *testing.T) {
	hs := newHarness(t, true)
	a := hs.a
	e := &a.everywhere
	a.openEverywhere()
	e.cancel = &atomic.Bool{}
	text := "a a\n"
	for range everywhereLines {
		text += "a\n"
	}
	locs, _ := foldQuery{needle: "a"}.matches(text, -1)
	f := &searchFile{rel: "a.txt", matches: matchesAt(text, locs)}
	a.takeText(e.gen, []*searchFile{f}, true, false)
	if len(e.lines) != everywhereLines || !e.stopped || !e.cancel.Load() || e.lines[0].match.col != 0 || e.lines[1].match.line != 1 {
		t.Fatalf("%d lines, stopped %v", len(e.lines), e.stopped)
	}
	e.query, e.shown = "a", "a"
	hs.settle()
	if !hs.tt.HasText("Show all results in Search") {
		t.Fatalf("no way to the rest: %q", hs.tt.Texts())
	}
}

// A language server's workspace symbols are listed in a Symbols group
// once typing pauses, and choosing one opens its file at the place and
// keeps where it was in the navigation history. Without a server there
// is no group.
func TestSearchEverywhereFindsSymbols(t *testing.T) {
	hs := withFakeServer(t)
	a, tt := hs.a, hs.tt
	e := &a.everywhere
	os.WriteFile(hs.path("sym.fake"), []byte("one\ntwo\nclass Widget\n"), 0o644)
	os.WriteFile(hs.path("a.fake"), []byte("one\n"), 0o644)
	a.openFile(hs.path("a.fake"), true)
	serverUp(hs)

	a.run("go.searchEverywhere")
	hs.settle()
	tt.Type("widget")
	hs.settleEverywhere()
	var names []string
	for _, s := range e.symbols {
		names = append(names, s.sym.Name+" "+lsp.SymbolKind(s.sym.Kind)+" "+s.sym.Container)
	}
	if want := []string{"Widget class shapes", "WidgetList struct "}; !slices.Equal(names, want) {
		t.Fatalf("symbols %q; want %q", names, want)
	}
	if !tt.HasText("Symbols") || !tt.HasText("WidgetList") {
		t.Errorf("group missing: %q", tt.Texts())
	}

	places := len(a.nav.places)
	e.sel = len(e.names)
	hs.key(0, ui.KeyEnter)
	hs.until("the symbol's place", func() bool {
		file, line, col := caretAt(a)
		return file == "sym.fake" && line == 2 && col == 6
	})
	if e.open || len(a.nav.places) != places+1 {
		t.Errorf("open %v, %d places, was %d", e.open, len(a.nav.places), places)
	}
}

// With no language server there is no Symbols group and no error.
func TestSearchEverywhereWithoutSymbolServers(t *testing.T) {
	hs := newHarness(t, true)
	a, tt := hs.a, hs.tt
	a.run("go.searchEverywhere")
	hs.settle()
	tt.Type("main")
	hs.settleEverywhere()
	if len(a.everywhere.symbols) != 0 || tt.HasText("Symbols") {
		t.Errorf("symbols %v: %q", a.everywhere.symbols, tt.Texts())
	}
}
