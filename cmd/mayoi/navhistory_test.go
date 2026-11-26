package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// withFakeServer is a harness with a plugin whose language server, the
// fake of lsptest, serves .fake files.
func withFakeServer(t *testing.T) *harness {
	t.Setenv("MAYOI_FAKE_LSP", "1")
	manifest, _ := json.Marshal(map[string]any{
		"name": "fake",
		"contributes": map[string]any{
			"languages":       []any{map[string]any{"id": "fake", "extensions": []string{".fake"}}},
			"languageServers": []any{map[string]any{"id": "fake.server", "languages": []string{"fake"}, "command": os.Args[0], "args": []string{"-test.run=^$"}}},
		},
	})
	hs := newHarness(t, false)
	withPlugins(hs, map[string]map[string]string{"fake": {"plugin.json": string(manifest)}})
	t.Cleanup(stopServers)
	return hs
}

// serverUp waits for the fake server of the harness's folder to run,
// which a busy machine takes its time to start.
func serverUp(hs *harness) {
	hs.until("the language server", func() bool {
		lspServers.Lock()
		defer lspServers.Unlock()
		s := lspServers.m[hs.a.root+"\x00fake.server"]
		return s != nil && s.state() == lspRunning
	})
}

// caretAt is the line and column, from 0, of the caret of the editor
// that has the keyboard, and its file's name.
func caretAt(a *app) (string, int, int) {
	d := a.activeEditor()
	if d == nil || d.pending != nil {
		return "", -1, -1
	}
	snap := d.buf.Snapshot()
	at := min(d.sel[1], snap.Len())
	line := snap.LineAt(at)
	s, _ := snap.LineRange(line)
	return filepath.Base(d.path), line, at - s
}

// Go to Definition goes to the one place, offers several, says when there
// is none, and Go Back and Go Forward go between where it went and where
// it left.
func TestGoToDefinitionAndBack(t *testing.T) {
	hs := withFakeServer(t)
	a := hs.a
	path := hs.path("a.fake")
	os.WriteFile(path, []byte("call two\nuse one\ndef one\ndef two\ndef two\nnothing\n"), 0o644)
	a.openFile(path, true)
	d := a.activeEditor()
	serverUp(hs)

	want := func(what string, line, col int) {
		t.Helper()
		hs.until(what, func() bool {
			_, l, c := caretAt(a)
			return l == line && c == col
		})
	}
	hs.place(d, 13, 13) // in "one" of "use one"
	a.goToDefinition()
	want("the definition", 2, 4)

	a.navGo(-1)
	hs.settle()
	want("back where it left", 1, 4)
	a.navGo(-1)
	hs.settle()
	want("no further back", 1, 4)
	a.navGo(1)
	hs.settle()
	want("forward again", 2, 4)

	// Two definitions: a box offers both, the second chosen.
	hs.place(d, 6, 6) // in "two"
	a.goToDefinition()
	hs.until("the definitions offered", func() bool { return a.pluginPick.open })
	if items := a.pluginPick.items; len(items) != 2 || items[0].Label != "a.fake:4" || items[1].Label != "a.fake:5" {
		t.Fatalf("offered %+v", items)
	}
	a.closePluginPicker(1)
	want("the second definition", 4, 4)

	// None, said so: the caret stays and the history keeps what it had.
	places := len(a.nav.places)
	hs.place(d, 45, 45) // in "nothing"
	a.goToDefinition()
	hs.until("no definition told", func() bool { return hs.tt.HasText("No definition found") })
	if _, l, _ := caretAt(a); l != 5 || len(a.nav.places) != places {
		t.Fatalf("after no definition: line %d, %d places, was %d", l, len(a.nav.places), places)
	}
}

// fakeCall asks the fake server of the harness's folder a question of
// its own.
func fakeCall(hs *harness, method string) json.RawMessage {
	hs.t.Helper()
	c := serverOf(hs.a.root + "\x00fake.server").live()
	if c == nil {
		hs.t.Fatal("no fake server runs")
	}
	res, err := c.Call(context.Background(), method, nil)
	if err != nil {
		hs.t.Fatalf("%s: %v", method, err)
	}
	return res
}

// holdLate waits for the fake server to hold back the answer of "late",
// which settle then does not wait for.
func holdLate(hs *harness) {
	hs.t.Helper()
	hs.held++
	hs.until("the answer held", func() bool { return string(fakeCall(hs, "test/held")) == "true" })
}

// releaseLate has the fake server answer "late" and waits for the app to
// take the answer.
func releaseLate(hs *harness) {
	hs.t.Helper()
	fakeCall(hs, "test/release")
	hs.held--
	hs.settle()
}

// Only the definition last asked for, at the caret it was asked at, goes
// anywhere: an older answer that comes after a newer one, or after the
// caret moved, neither goes, offers, tells nor adds to the history.
func TestDefinitionAnswerTooLate(t *testing.T) {
	hs := withFakeServer(t)
	a := hs.a
	path := hs.path("a.fake")
	os.WriteFile(path, []byte("use late one\ndef one\ndef late\ndef late\n"), 0o644)
	a.openFile(path, true)
	d := a.activeEditor()
	serverUp(hs)
	still := func(what string, line, col, places int) {
		t.Helper()
		if _, l, c := caretAt(a); l != line || c != col || len(a.nav.places) != places || a.pluginPick.open || hs.tt.HasText("No definition found") {
			t.Fatalf("%s: at %d:%d, %d places (want %d), picker %v, %q", what, l, c, len(a.nav.places), places, a.pluginPick.open, hs.tt.Texts())
		}
	}

	// Asked of "late", then of "one", whose answer comes first.
	hs.place(d, 5, 5)
	a.goToDefinition()
	holdLate(hs)
	hs.place(d, 10, 10)
	a.goToDefinition()
	hs.until("the newer definition", func() bool { _, l, c := caretAt(a); return l == 1 && c == 4 })
	places := len(a.nav.places)
	releaseLate(hs)
	still("after the older answer", 1, 4, places)

	// Asked of "late", the caret moved on before the answer.
	hs.place(d, 5, 5)
	a.goToDefinition()
	holdLate(hs)
	hs.place(d, 1, 1)
	places = len(a.nav.places)
	releaseLate(hs)
	still("after the caret moved", 0, 1, places)

	// Asked of "late" in a file left: nothing goes on in the other one.
	other := hs.path("b.fake")
	os.WriteFile(other, []byte("plain\n"), 0o644)
	hs.place(d, 5, 5)
	a.goToDefinition()
	holdLate(hs)
	a.openFile(other, true)
	hs.settle()
	places = len(a.nav.places)
	releaseLate(hs)
	if name, _, _ := caretAt(a); name != "b.fake" || len(a.nav.places) != places || a.pluginPick.open {
		t.Fatalf("an answer for a file left went to %s, %d places, picker %v", name, len(a.nav.places), a.pluginPick.open)
	}

	// Asked again where it was, the answer goes: the two places offered.
	a.openFile(path, true)
	hs.settle()
	d = a.activeEditor()
	hs.place(d, 5, 5)
	a.goToDefinition()
	holdLate(hs)
	releaseLate(hs)
	hs.until("the definitions offered", func() bool { return a.pluginPick.open })
	if items := a.pluginPick.items; len(items) != 2 || items[0].Label != "a.fake:3" || items[1].Label != "a.fake:4" {
		t.Fatalf("offered %+v", items)
	}
	a.closePluginPicker(-1)
}

// A server restarted while it is asked for a definition leaves the
// caret where it is, and says nothing of there being none.
func TestDefinitionAcrossRestart(t *testing.T) {
	hs := withFakeServer(t)
	a := hs.a
	path := hs.path("a.fake")
	os.WriteFile(path, []byte("use late\ndef late\n"), 0o644)
	a.openFile(path, true)
	d := a.activeEditor()
	serverUp(hs)
	hs.place(d, 5, 5)
	a.goToDefinition()
	holdLate(hs)
	places := len(a.nav.places)
	a.restartServer(a.root + "\x00fake.server")
	hs.held--
	serverUp(hs)
	hs.settle()
	if _, l, c := caretAt(a); l != 0 || c != 5 || len(a.nav.places) != places || hs.tt.HasText("No definition found") {
		t.Fatalf("after a restart: at %d:%d, %d places, %q", l, c, len(a.nav.places), hs.tt.Texts())
	}
}

// Definitions on one line, apart, are each offered, with their columns,
// and the one chosen is gone to; the same place twice is offered once.
func TestDefinitionsOnOneLine(t *testing.T) {
	hs := withFakeServer(t)
	a := hs.a
	path := hs.path("a.fake")
	os.WriteFile(path, []byte("use two\ndef two x two\ndef two\n"), 0o644)
	a.openFile(path, true)
	d := a.activeEditor()
	serverUp(hs)
	hs.place(d, 5, 5)
	a.goToDefinition()
	hs.until("the definitions offered", func() bool { return a.pluginPick.open })
	if items := a.pluginPick.items; len(items) != 3 || items[0].Label != "a.fake:2:5" || items[1].Label != "a.fake:2:11" || items[2].Label != "a.fake:3" {
		t.Fatalf("offered %+v", items)
	}
	a.closePluginPicker(1)
	hs.until("the second on the line", func() bool { _, l, c := caretAt(a); return l == 1 && c == 10 })
	a.navGo(-1)
	hs.settle()
	if _, l, c := caretAt(a); l != 0 || c != 5 {
		t.Fatalf("back at %d:%d", l, c)
	}
}

// Go Back comes back from another file, to the place it left, which edits
// made since have moved; a new jump drops the places gone back past.
func TestNavigationHistory(t *testing.T) {
	hs := newHarness(t, false)
	a := hs.a
	one, two, three := hs.path("one.txt"), hs.path("two.txt"), hs.path("three.txt")
	os.WriteFile(one, []byte("alpha\nbeta\ngamma\n"), 0o644)
	os.WriteFile(two, []byte("first\nsecond\n"), 0o644)
	os.WriteFile(three, []byte("x\n"), 0o644)
	a.openFile(one, true)
	hs.settle()
	d := a.activeEditor()
	hs.place(d, 8, 8) // "be|ta"

	a.openFileAt(two, 1, true)
	hs.settle()
	if name, l, _ := caretAt(a); name != "two.txt" || l != 1 {
		t.Fatalf("opened at %s:%d", name, l)
	}
	// A line put in above the place it left moves it.
	d.edit(d.sel, textEdit{0, 0, "new\n"})
	hs.settle()
	a.navGo(-1)
	hs.settle()
	if name, l, c := caretAt(a); name != "one.txt" || l != 2 || c != 2 {
		t.Fatalf("back at %s:%d:%d", name, l, c)
	}
	a.navGo(1)
	hs.settle()
	if name, l, _ := caretAt(a); name != "two.txt" || l != 1 {
		t.Fatalf("forward at %s:%d", name, l)
	}
	// Back again, then a jump elsewhere: forward has nowhere to go.
	a.navGo(-1)
	hs.settle()
	a.openFileAt(three, 0, true)
	hs.settle()
	a.navGo(1)
	hs.settle()
	if name, _, _ := caretAt(a); name != "three.txt" {
		t.Fatalf("forward past a new jump went to %s", name)
	}

	// A file gone is passed over, and told of.
	a.navGo(-1)
	hs.settle()
	if name, _, _ := caretAt(a); name != "one.txt" {
		t.Fatalf("back from three at %s", name)
	}
	a.openFileAt(two, 0, true)
	hs.settle()
	a.openFileAt(three, 0, true)
	hs.settle()
	os.Remove(two)
	a.navGo(-1)
	hs.settle()
	if name, _, _ := caretAt(a); name != "one.txt" {
		t.Fatalf("back past a file gone at %s", name)
	}
	if !hs.tt.HasText("two.txt no longer exists") {
		t.Fatalf("toasts %q", hs.tt.Texts())
	}
}

// The history keeps so many places, and one on the line of the last
// takes its place.
func TestNavigationHistoryBounds(t *testing.T) {
	hs := newHarness(t, false)
	a := hs.a
	for i := range navMax + 10 {
		a.navPush(&navPlace{path: hs.path("f.txt"), at: i})
	}
	if len(a.nav.places) != navMax || a.nav.places[0].at != 10 || a.nav.at != navMax {
		t.Fatalf("%d places, the first at %d, at %d", len(a.nav.places), a.nav.places[0].at, a.nav.at)
	}
	path := hs.path("g.txt")
	os.WriteFile(path, []byte("one line\nanother\n"), 0o644)
	a.openFile(path, true)
	hs.settle()
	a.navPush(&navPlace{path: path, at: 1})
	a.navPush(&navPlace{path: path, at: 5})
	if n := len(a.nav.places); a.nav.places[n-1].at != 5 || a.nav.places[n-2].path == path {
		t.Fatalf("the places on one line: %+v %+v", a.nav.places[n-2], a.nav.places[n-1])
	}
}

// A Cmd-click on a name goes to its definition as it is let go; one that
// drags to select does not, nor does one in a language with no server.
func TestCmdClickDefinition(t *testing.T) {
	hs := withFakeServer(t)
	a := hs.a
	path := hs.path("a.fake")
	os.WriteFile(path, []byte("use one\ndef one\n"), 0o644)
	a.openFile(path, true)
	serverUp(hs)
	d := a.activeEditor()
	click := func(mods ui.Modifiers) {
		a.editorKey(d, ui.InputEvent{Kind: ui.InputPointerDown, Button: 0, Clicks: 1, Mods: mods})
		a.editorKey(d, ui.InputEvent{Kind: ui.InputPointerUp, Button: 0, Mods: mods})
		hs.settle()
	}
	hs.place(d, 0, 2) // a selection, as a drag makes
	click(ui.Cmd)
	hs.settle()
	if _, l, _ := caretAt(a); l != 0 {
		t.Fatalf("a drag went to line %d", l)
	}
	hs.place(d, 5, 5)
	click(0)
	if _, l, _ := caretAt(a); l != 0 {
		t.Fatalf("a plain click went to line %d", l)
	}
	click(ui.Cmd)
	hs.until("the definition", func() bool { _, l, c := caretAt(a); return l == 1 && c == 4 })

	other := hs.path("notes.txt")
	os.WriteFile(other, []byte("plain words\n"), 0o644)
	a.openFile(other, true)
	hs.settle()
	d = a.activeEditor()
	hs.place(d, 3, 3)
	click(ui.Cmd)
	hs.settle()
	if hs.tt.HasText("language server") {
		t.Fatalf("a Cmd-click with no server told %q", hs.tt.Texts())
	}
}

// Go Back to a place folded since opens the folds that hide it.
func TestNavigationBackIntoFold(t *testing.T) {
	hs, d := foldHarness(t, "data.json", foldJSON, false)
	a := hs.a
	other := hs.path("other.txt")
	os.WriteFile(other, []byte("x\n"), 0o644)
	caretOn(hs, d, 4)
	a.openFileAt(other, 0, true)
	hs.settle()
	a.foldAllCmd(d)
	hs.settle()
	if _, hidden := runAt(a.hiddenRuns(d, d.buf.Snapshot()), 4); !hidden {
		t.Fatal("line 5 does not fold")
	}
	a.navGo(-1)
	hs.settle()
	if name, l, _ := caretAt(a); name != "data.json" || l != 4 {
		t.Fatalf("back at %s:%d", name, l)
	}
	if _, hidden := runAt(a.hiddenRuns(d, d.buf.Snapshot()), 4); hidden {
		t.Fatal("the place gone back to stays folded")
	}
}

// Cmd held over a name a click goes to the definition of shows a hand,
// as it is pressed and let go with the pointer still; not over a space,
// past a line's end, at the gutter, nor where no server goes to one.
func TestCmdPointerOverName(t *testing.T) {
	hs := withFakeServer(t)
	a := hs.a
	path := hs.path("a.fake")
	os.WriteFile(path, []byte("use one\ndef one\n"), 0o644)
	a.openFile(path, true)
	serverUp(hs)
	d := a.activeEditor()
	hs.place(d, 1, 1)
	hs.settle()
	hand := func(want bool, what string) {
		t.Helper()
		if got := hs.tt.Cursor() == ui.CursorPointer; got != want {
			t.Errorf("%s: the pointer is %v", what, hs.tt.Cursor())
		}
	}
	// The tester's moves hold no modifiers: Cmd is held again after them,
	// as it stays held over a move in the app.
	held := ui.Modifiers(0)
	hold := func(m ui.Modifiers) {
		held = m
		hs.tt.HoldModifiers(m)
		hs.settle()
	}
	move := func(x, y float32) {
		hs.tt.Move(x, y)
		step(hs.tt)
		hs.tt.HoldModifiers(held)
		hs.settle()
	}
	x, y := restAt(t, hs, d, 4) // "one"
	move(x, y)
	hand(false, "over a name without Cmd")
	hold(ui.Cmd)
	hand(true, "over a name as Cmd is pressed")
	hold(0)
	hand(false, "over a name as Cmd is let go")
	hold(ui.Cmd)

	sx, sy := restAt(t, hs, d, 3) // the space
	move(sx, sy)
	hand(false, "over a space")
	move(pastEnd(t, hs, d, 7))
	hand(false, "past a line's end")
	gx, gy := restAt(t, hs, d, 0)
	move(gx-d.textX/2, gy)
	hand(false, "at the gutter")
	move(x, y)
	hand(true, "back over the name")

	// The click still goes to the definition.
	hs.tt.ClickAtWith(ui.Cmd, x, y)
	hs.until("the definition", func() bool { _, l, c := caretAt(a); return l == 1 && c == 4 })
	hold(0)

	for _, name := range []string{"notes.txt", "notes.md"} {
		other := hs.path(name)
		os.WriteFile(other, []byte("plain words\n"), 0o644)
		a.openFile(other, true)
		hs.settle()
		d := a.activeEditor()
		x, y := restAt(t, hs, d, 1)
		move(x, y)
		hold(ui.Cmd)
		hand(false, "over a word of "+name)
		hold(0)
	}
}

// The mouse back and forward buttons go one step through the history,
// with the keyboard in the editor or not.
func TestMouseButtonsGoThroughHistory(t *testing.T) {
	hs := newHarness(t, false)
	a := hs.a
	for _, n := range []string{"one.txt", "two.txt", "three.txt"} {
		os.WriteFile(hs.path(n), []byte("x\n"), 0o644)
		a.openFileAt(hs.path(n), 0, true)
		hs.settle()
	}
	at := func(want string) {
		t.Helper()
		if name, _, _ := caretAt(a); name != want {
			t.Fatalf("at %s, want %s", name, want)
		}
	}
	hs.key(0, ui.KeyBack)
	at("two.txt")
	hs.key(0, ui.KeyBack)
	at("one.txt")
	hs.key(0, ui.KeyForward)
	at("two.txt")
	// With the keyboard in the Explorer, they go as well.
	a.run("view.explorer")
	hs.settle()
	hs.key(0, ui.KeyForward)
	at("three.txt")
}

// longFiles writes files of forty lines, for places far apart in them.
func longFiles(hs *harness, names ...string) []string {
	var lines strings.Builder
	for i := range 40 {
		fmt.Fprintf(&lines, "line %d\n", i)
	}
	paths := make([]string, len(names))
	for i, n := range names {
		paths[i] = hs.path(n)
		os.WriteFile(paths[i], []byte(lines.String()), 0o644)
	}
	return paths
}

// wantPlace fails unless the caret is on line of the file name.
func wantPlace(t *testing.T, a *app, what, name string, line int) {
	t.Helper()
	if n, l, _ := caretAt(a); n != name || l != line {
		t.Fatalf("%s: at %s:%d, want %s:%d", what, n, l, name, line)
	}
}

// Go Back and Go Forward go through the files opened, as from Quick Open
// or the Explorer, to the places their carets were, and back and forth
// again without losing any.
func TestBackThroughFilesOpened(t *testing.T) {
	hs := newHarness(t, false)
	a := hs.a
	paths := longFiles(hs, "a.txt", "b.txt", "c.txt")
	a.openFile(paths[0], true)
	hs.settle()
	a.goToLine(21, 1)
	hs.settle()
	a.openFile(paths[1], true)
	hs.settle()
	a.goToLine(31, 1)
	hs.settle()
	a.previewFile(paths[2])
	hs.settle()

	a.navGo(-1)
	hs.settle()
	wantPlace(t, a, "back", "b.txt", 30)
	a.navGo(-1)
	hs.settle()
	wantPlace(t, a, "back again", "b.txt", 0)
	a.navGo(-1)
	hs.settle()
	wantPlace(t, a, "back to the first file", "a.txt", 20)
	a.navGo(1)
	hs.settle()
	a.navGo(1)
	hs.settle()
	a.navGo(1)
	hs.settle()
	wantPlace(t, a, "forward to the last file", "c.txt", 0)
	a.navGo(-1)
	hs.settle()
	wantPlace(t, a, "back once more", "b.txt", 30)
}

// Showing another tab, and closing one, keep the place left: Go Back
// goes to it, opening a file whose tab was closed again, and Go Forward
// returns.
func TestBackAcrossTabs(t *testing.T) {
	hs := newHarness(t, false)
	a := hs.a
	paths := longFiles(hs, "a.txt", "b.txt")
	a.openFile(paths[0], true)
	hs.settle()
	a.goToLine(21, 1)
	hs.settle()
	a.openFile(paths[1], true)
	hs.settle()
	a.goToLine(31, 1)
	hs.settle()

	a.cycleTab(-1)
	hs.settle()
	wantPlace(t, a, "the tab shown", "a.txt", 20)
	a.navGo(-1)
	hs.settle()
	wantPlace(t, a, "back to the tab left", "b.txt", 30)
	a.navGo(1)
	hs.settle()
	wantPlace(t, a, "forward", "a.txt", 20)

	a.closeTab(a.column().tabs[1], nil, nil)
	hs.settle()
	a.navGo(-1)
	hs.settle()
	wantPlace(t, a, "back to the tab closed", "b.txt", 30)
	a.navGo(1)
	hs.settle()
	wantPlace(t, a, "forward from it", "a.txt", 20)
}

// A jump far through one file, as Go to Line makes, keeps the place it
// left; arrow keys and an edit that moves the caret as far do not.
func TestBackFromAJumpInAFile(t *testing.T) {
	hs := newHarness(t, false)
	a := hs.a
	paths := longFiles(hs, "a.txt")
	a.openFile(paths[0], true)
	hs.settle()
	d := a.activeEditor()
	a.goToLine(21, 1)
	hs.settle()
	places := len(a.nav.places)
	for range 3 {
		hs.key(0, ui.KeyDown)
	}
	a.queueEdit(d, func(d *editor) {
		at := d.sel[1]
		text := strings.Repeat("pasted\n", 15)
		d.edit([2]int{at + len(text), at + len(text)}, textEdit{at, at, text})
	})
	hs.settle()
	wantPlace(t, a, "after the paste", "a.txt", 38)
	if len(a.nav.places) != places {
		t.Fatalf("arrow keys and an edit kept %d places", len(a.nav.places)-places)
	}

	a.goToLine(5, 1)
	hs.settle()
	a.navGo(-1)
	hs.settle()
	wantPlace(t, a, "back", "a.txt", 38)
	a.navGo(-1)
	hs.settle()
	wantPlace(t, a, "back before the arrow keys", "a.txt", 0)
	a.navGo(1)
	hs.settle()
	a.navGo(1)
	hs.settle()
	wantPlace(t, a, "forward", "a.txt", 4)
}

// From a tab of no file, Go Back goes first to the place of the file
// left for it.
func TestBackFromATabOfNoFile(t *testing.T) {
	hs := newHarness(t, false)
	a := hs.a
	paths := longFiles(hs, "a.txt", "b.txt")
	a.openFile(paths[0], true)
	hs.settle()
	a.openFile(paths[1], true)
	hs.settle()
	a.goToLine(31, 1)
	hs.settle()
	a.newFile()
	hs.settle()
	a.navGo(-1)
	hs.settle()
	wantPlace(t, a, "back from an untitled tab", "b.txt", 30)
	a.navGo(-1)
	hs.settle()
	wantPlace(t, a, "back again", "b.txt", 0)
	a.navGo(-1)
	hs.settle()
	wantPlace(t, a, "back to the first file", "a.txt", 0)
}
