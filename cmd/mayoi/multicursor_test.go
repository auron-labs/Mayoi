package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mayoi/locales"
)

// cell returns where in the window line l's column col is, in the middle
// of the line's height, at the start of the column.
func (hs *harness) cell(d *editor, l, col int) (x, y float32) {
	hs.t.Helper()
	r, ok := hs.tt.Find(locales.EditorLabel(d.name()))
	if !ok {
		hs.t.Fatal("no editor")
	}
	f := hs.a.font
	return r.X + d.textX + float32(col)*f.charW + 1, r.Y + editorPadY + float32(l)*f.lineH + f.lineH/2
}

// altClick adds a caret at line l's column col.
func (hs *harness) altClick(d *editor, l, col int) {
	hs.t.Helper()
	x, y := hs.cell(d, l, col)
	hs.tt.ClickAtWith(ui.Alt, x, y)
	hs.settle()
}

func (hs *harness) typeText(s string) {
	hs.tt.Type(s)
	hs.settle()
}

func expectCarets(t *testing.T, d *editor, sel [2]int, carets ...[2]int) {
	t.Helper()
	if d.sel != sel || !slices.Equal(d.carets, carets) {
		t.Fatalf("selections %v %v, want %v %v", d.sel, d.carets, sel, carets)
	}
}

func TestAltClickTypesAtEachCaret(t *testing.T) {
	hs, d := editHarness(t, "a.txt", "one\ntwo\nthree\n")
	hs.place(d, 3, 3)
	hs.altClick(d, 1, 3)
	expectCarets(t, d, [2]int{7, 7}, [2]int{3, 3})

	hs.typeText("!")
	hs.typeText("?")
	expectText(t, d, "one!?\ntwo!?\nthree\n")
	expectCarets(t, d, [2]int{11, 11}, [2]int{5, 5})

	// What was typed undoes as one step, the carets with it, and redoes.
	hs.key(ui.Cmd, ui.KeyZ)
	expectText(t, d, "one\ntwo\nthree\n")
	expectCarets(t, d, [2]int{7, 7}, [2]int{3, 3})
	hs.key(ui.Cmd|ui.Shift, ui.KeyZ)
	expectText(t, d, "one!?\ntwo!?\nthree\n")

	// Escape leaves the text area's caret the only one.
	hs.key(0, ui.KeyEscape)
	expectCarets(t, d, [2]int{11, 11})
	hs.typeText("x")
	expectText(t, d, "one!?\ntwo!?x\nthree\n")
}

func TestAltClickOnCaretTakesItAway(t *testing.T) {
	hs, d := editHarness(t, "a.txt", "one\ntwo\nthree\n")
	hs.place(d, 0, 0)
	hs.altClick(d, 1, 0)
	hs.altClick(d, 2, 0)
	expectCarets(t, d, [2]int{8, 8}, [2]int{0, 0}, [2]int{4, 4})
	hs.altClick(d, 1, 0)
	expectCarets(t, d, [2]int{8, 8}, [2]int{0, 0})
	// The text area's own: the one added last takes its place.
	hs.altClick(d, 2, 0)
	expectCarets(t, d, [2]int{0, 0})
	// A click without Alt leaves one.
	hs.altClick(d, 1, 0)
	x, y := hs.cell(d, 2, 1)
	hs.tt.ClickAt(x, y)
	hs.settle()
	expectCarets(t, d, [2]int{9, 9})
}

func TestCaretKeysActAtEach(t *testing.T) {
	hs, d := editHarness(t, "a.txt", "alpha beta\nxy\ngamma delta\n")
	hs.place(d, 10, 10)
	hs.altClick(d, 2, 11)
	expectCarets(t, d, [2]int{25, 25}, [2]int{10, 10})

	// A word back, selecting, then deleted.
	hs.key(ui.Shift|wordMod(), ui.KeyLeft)
	expectCarets(t, d, [2]int{25, 20}, [2]int{10, 6})
	hs.key(0, ui.KeyBackspace)
	expectText(t, d, "alpha \nxy\ngamma \n")
	expectCarets(t, d, [2]int{16, 16}, [2]int{6, 6})

	// Down keeps the column it started from past a short line.
	hs.key(0, ui.KeyDown)
	expectCarets(t, d, [2]int{17, 17}, [2]int{9, 9})
	hs.key(0, ui.KeyDown)
	expectCarets(t, d, [2]int{17, 17}, [2]int{16, 16})
}

func TestCaretLineKeys(t *testing.T) {
	hs, d := editHarness(t, "a.go", "func a() {\n\tx := 1\n\ty := 2\n}\n")
	hs.place(d, 18, 18)
	hs.altClick(d, 2, 10)
	expectCarets(t, d, [2]int{26, 26}, [2]int{18, 18})
	// Enter keeps each line's indent.
	hs.key(0, ui.KeyEnter)
	hs.typeText("z")
	expectText(t, d, "func a() {\n\tx := 1\n\tz\n\ty := 2\n\tz\n}\n")
	// Home goes to the first character of each line, End to its end.
	hs.key(0, ui.KeyHome)
	hs.typeText("y")
	hs.key(0, ui.KeyEnd)
	hs.typeText(";")
	expectText(t, d, "func a() {\n\tx := 1\n\tyz;\n\ty := 2\n\tyz;\n}\n")
}

// wordMod is the modifier that moves the caret by words.
func wordMod() ui.Modifiers {
	if ui.Cmd == ui.Super {
		return ui.Alt
	}
	return ui.Ctrl
}

func TestCopyPastesALineAtEachCaret(t *testing.T) {
	hs, d := editHarness(t, "a.txt", "a = 1\nb = 2\n")
	hs.place(d, 0, 0)
	hs.altClick(d, 1, 0)
	hs.key(ui.Shift, ui.KeyRight)
	expectCarets(t, d, [2]int{6, 7}, [2]int{0, 1})
	hs.key(ui.Cmd, ui.KeyC)
	if got := hs.tt.Clipboard(); got != "a\nb" {
		t.Fatalf("copied %q", got)
	}
	hs.key(0, ui.KeyEnd)
	hs.typeText(" // ")
	hs.command("paste")
	expectText(t, d, "a = 1 // a\nb = 2 // b\n")

	// A text of other lines goes whole at each.
	hs.tt.SetClipboard("x\ny\nz")
	hs.command("paste")
	expectText(t, d, "a = 1 // ax\ny\nz\nb = 2 // bx\ny\nz\n")
}

func TestCutAtEachCaret(t *testing.T) {
	hs, d := editHarness(t, "a.txt", "one\ntwo\nthree\n")
	hs.place(d, 0, 0)
	hs.altClick(d, 2, 0)
	hs.key(ui.Cmd, ui.KeyX)
	if got := hs.tt.Clipboard(); got != "one\nthree\n" {
		t.Fatalf("cut %q", got)
	}
	expectText(t, d, "two\n")
}

func TestMiddleDragSelectsColumns(t *testing.T) {
	hs, d := editHarness(t, "a.txt", "abcdef\nab\nabcdef\nabcdef\n")
	a, f := hs.a, hs.a.font
	at := func(l, col int) (float32, float32) {
		return d.textX + float32(col)*f.charW, editorPadY + float32(l)*f.lineH + f.lineH/2
	}
	x, y := at(0, 3)
	a.editorKey(d, ui.InputEvent{Kind: ui.InputPointerDown, Button: 2, Mods: ui.Alt, Clicks: 1, X: x, Y: y})
	x, y = at(3, 5)
	a.editorKey(d, ui.InputEvent{Kind: ui.InputPointerMove, Button: 2, Mods: ui.Alt, X: x, Y: y})
	a.editorKey(d, ui.InputEvent{Kind: ui.InputPointerUp, Button: 2, Mods: ui.Alt, X: x, Y: y})
	hs.settle()
	// The short line is left out; the text area's caret is where the
	// pointer let go.
	expectCarets(t, d, [2]int{20, 22}, [2]int{3, 5}, [2]int{13, 15})
	hs.typeText("-")
	expectText(t, d, "abc-f\nab\nabc-f\nabc-f\n")
}

func TestCaretsDropWithAnotherSelection(t *testing.T) {
	hs, d := editHarness(t, "a.txt", "one\ntwo\n")
	hs.place(d, 0, 0)
	hs.altClick(d, 1, 0)
	if len(d.carets) != 1 {
		t.Fatalf("carets %v", d.carets)
	}
	// A selection asked for, as going to a line, leaves one caret.
	hs.place(d, 2, 2)
	expectCarets(t, d, [2]int{2, 2})
}

func TestLineCommandsRunOnceALine(t *testing.T) {
	hs, d := editHarness(t, "a.go", "x()\ny()\n")
	hs.place(d, 0, 0)
	hs.altClick(d, 0, 2)
	hs.altClick(d, 1, 0)
	hs.a.run("sel.comment")
	hs.settle()
	expectText(t, d, "// x()\n// y()\n")
	if len(d.carets) != 2 {
		t.Fatalf("carets %v", d.carets)
	}
}

func TestCopyFromMenu(t *testing.T) {
	hs, d := editHarness(t, "a.txt", "one\ntwo\n")
	copyFromMenu := func() {
		t.Helper()
		fromEditMenu(t, hs, mygo.RoleCopy, "copy", locales.CmdEditCopy())
		hs.settle()
	}
	hs.place(d, 1, 3)
	copyFromMenu()
	if got := hs.tt.Clipboard(); got != "ne" {
		t.Fatalf("copied %q", got)
	}
	hs.place(d, 1, 1)
	hs.altClick(d, 1, 0)
	hs.key(ui.Shift, ui.KeyRight)
	hs.key(ui.Shift, ui.KeyRight)
	copyFromMenu()
	if got := hs.tt.Clipboard(); got != "ne\ntw" {
		t.Fatalf("copied %q", got)
	}
	// A text field copies its own.
	hs.a.openQuick("")
	hs.settle()
	hs.tt.Type("needle")
	hs.settle()
	hs.key(ui.Cmd, ui.KeyA)
	copyFromMenu()
	if got := hs.tt.Clipboard(); got != "needle" {
		t.Fatalf("text field copied %q", got)
	}
}

func TestCaretsGoAlongWrappedRows(t *testing.T) {
	hs, d := editHarness(t, "a.txt", strings.Repeat("word ", 40)+"\nend\n")
	hs.a.settings.WordWrap = true
	hs.tt.SetSize(600, 400)
	hs.settle()
	rows := d.lines.Rows(0)
	if len(rows) < 3 {
		t.Fatalf("the line wraps in rows %v", rows)
	}
	hs.place(d, 0, 0)
	hs.altClick(d, 1, 2) // the second row
	at := rows[1] + 2
	expectCarets(t, d, [2]int{at, at}, [2]int{0, 0})
	hs.key(0, ui.KeyDown)
	expectCarets(t, d, [2]int{rows[2] + 2, rows[2] + 2}, [2]int{rows[1], rows[1]})
	hs.key(0, ui.KeyUp)
	expectCarets(t, d, [2]int{at, at}, [2]int{0, 0})
	// An Alt-click on a caret of a row takes it away.
	hs.altClick(d, 1, 2)
	expectCarets(t, d, [2]int{0, 0})
}
