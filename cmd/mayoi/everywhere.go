package main

import (
	"fmt"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"

	"mayoi/internal/bg"
	"mayoi/internal/workspace"
	"mayoi/locales"
)

const (
	// everywhereFiles is how many files Search Everywhere lists by name.
	everywhereFiles = 8
	// everywhereLines is the most lines of text Search Everywhere lists;
	// the search view finds them all.
	everywhereLines = 100
	// shiftTapWithin is how short a tap of Shift is, and how soon after
	// one a second must come to open Search Everywhere.
	shiftTapWithin = 400 * time.Millisecond
)

// everywhere is Search Everywhere: one box that finds the folder's files
// by name and the lines of their text, opened by tapping Shift twice.
type everywhere struct {
	open  bool
	query string
	input ui.Handle
	sel   int
	moved bool // the choice changed, to scroll into view once
	shook time.Time
	// stale says the files changed since names were found.
	stale bool
	shown string   // the query, trimmed, that names and lines are of
	files []string // the folder's files names are of
	names []workspace.Match

	// The search of the text, which starts once typing pauses: gen
	// counts the searches, so that what an old one finds is dropped.
	typedAt time.Time
	waiting bool
	running bool
	gen     int
	cancel  *atomic.Bool
	lines   []textLine // in the order found
	stopped bool       // there are more than lines
}

// textLine is a line of a file Search Everywhere found the query in, at
// its first match on the line.
type textLine struct {
	file  *searchFile
	match searchMatch
}

func (a *app) openEverywhere() {
	e := &a.everywhere
	a.quick.probe.epoch++
	a.quick.open = false
	e.stopText()
	// The last query, selected, so that typing replaces it; searched
	// again, as the files may have changed.
	e.open, e.stale, e.shown, e.sel = true, true, "", 0
	e.input.SelectAll()
	if a.index == nil {
		a.indexRead.Invalidate()
	}
	a.refreshIndex()
}

func (a *app) closeEverywhere() {
	a.everywhere.open = false
	a.everywhere.stopText()
}

// stopText stops the search of the text and forgets what it found.
func (e *everywhere) stopText() {
	if e.cancel != nil {
		e.cancel.Store(true)
	}
	e.gen++
	e.cancel, e.waiting, e.running, e.lines, e.stopped = nil, false, false, nil, false
}

// startText searches the text of the folder's files for the query, as
// the search view does with no options, once they are listed.
func (a *app) startText() {
	e := &a.everywhere
	if a.index == nil {
		a.refreshIndex()
		return
	}
	e.waiting = false
	texts := map[string]string{}
	for _, d := range a.docs {
		if d.path == "" || d.unread() || !d.dirty() {
			continue
		}
		if rel, ok := a.relOf(d.path); ok {
			texts[rel] = d.buf.String()
		}
	}
	re, err := compileQuery(e.shown, false, false, false)
	if err != nil {
		return // a literal query always compiles
	}
	cancel := &atomic.Bool{}
	e.cancel, e.running = cancel, true
	gen := e.gen
	a.runSearch(searchRun{
		re: re, fold: foldQueryOf(e.shown, false, false, false), rels: a.index, texts: texts, cancel: cancel,
		limit: maxSearchMatches, pre: &grepQuery{query: e.shown},
		take: func(files []*searchFile, done, stopped bool) { a.takeText(gen, files, done, stopped) },
	})
}

// takeText takes the files the search of the text found, a line for each
// line with matches, unless another search started since. It stops the
// search once it has everywhereLines.
func (a *app) takeText(gen int, files []*searchFile, done, stopped bool) {
	e := &a.everywhere
	if gen != e.gen {
		return
	}
	for _, f := range files {
		line := -1
		for _, m := range f.matches {
			if m.line == line {
				continue
			}
			if len(e.lines) == everywhereLines {
				stopped = true
				e.cancel.Store(true)
				break
			}
			line = m.line
			e.lines = append(e.lines, textLine{f, m})
		}
	}
	e.stopped = e.stopped || stopped
	if done {
		e.running = false
	}
}

// showAllText shows the search view searching for the query, which finds
// every match.
func (a *app) showAllText() {
	s := &a.inFiles
	a.showSearch(false)
	s.query, s.matchCase, s.word, s.regex = a.everywhere.shown, false, false, false
	a.startSearch()
}

func (a *app) everywhereView(c *ui.Context) {
	e := &a.everywhere
	if !e.open {
		return
	}
	p := a.pal
	if query := strings.TrimSpace(e.query); e.stale || query != e.shown {
		if query != e.shown {
			e.stopText()
			if query != "" && a.root != "" {
				e.waiting, e.typedAt = true, time.Now()
				bg.AfterFunc(searchPause, a.invalidate)
			}
		}
		e.files = a.index
		e.names = workspace.Fuzzy(e.files, query, everywhereFiles)
		if query == "" {
			recent := a.recentFirst(workspace.Fuzzy(e.files, "", 60))
			e.names = recent[:min(len(recent), everywhereFiles)]
		}
		e.shown, e.stale, e.sel, e.moved = query, false, 0, true
	}
	if e.waiting && time.Since(e.typedAt) >= searchPause {
		a.startText()
	}
	// The rows are the files, then the lines, then the row that shows
	// them all when there are more. They are taken from e, as closing the
	// box forgets them.
	files, names, lines, stopped := e.files, e.names, e.lines, e.stopped
	rows := len(names) + len(lines)
	if stopped {
		rows++
	}
	choose := func(i int, side bool) {
		if i < 0 || i >= rows {
			e.shook = c.Now()
			return
		}
		a.closeEverywhere()
		switch {
		case i < len(names):
			name := inDir(a.root, files[names[i].Index])
			if side {
				a.openFileToSide(name)
			} else {
				a.openFile(name, true)
			}
		case i < len(names)+len(lines):
			l := lines[i-len(names)]
			if side {
				a.openText(func() { a.openFileToSide(l.file.path) })
				a.selectMatch(l.file.path, l.match)
			} else {
				a.openMatch(l.file, l.match, true)
			}
		default:
			a.showAllText()
		}
	}
	ui.Overlay(c, func() {
		back := a.dialogBack(c)
		back.Children(func() {
			box := a.floating(c, ui.Column(c).Width(600).MaxWidthPercent(92).Margin(titleBarH+8, 0, 0, 0).Radius(10), true).Clip()
			shake(c, box, e.shook)
			box.Role(ui.RoleDialog).Label(locales.EverywhereLabel())
			box.Children(func() {
				ui.Row(c).Padding(10, 14).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
					ui.Icon(c, iconSearch).FontSize(15).TextColor(p.muted)
					in := a.textKeys(ui.TextInputBase(c.Key("everywhere-input"), &e.query)).Grow(1).FontSize(14).AutoFocus().
						Placeholder(locales.EverywherePlaceholder()).Label(locales.EverywhereLabel())
					in.Bind(&e.input)
					if in.Shortcut(0, ui.KeyDown) {
						e.sel, e.moved = max(min(e.sel+1, rows-1), 0), true
					}
					if in.Shortcut(0, ui.KeyUp) {
						e.sel, e.moved = max(e.sel-1, 0), true
					}
					if in.Shortcut(ui.Cmd, ui.KeyEnter) {
						choose(e.sel, true)
					} else if in.Submitted() {
						choose(e.sel, false)
					}
				})
				if e.waiting || e.running {
					ui.Progress(c.Key("everywhere-activity"), -1).Height(2).Radius(0).Label(locales.SearchSearching())
				} else {
					ui.Box(c.Key("everywhere-activity")).Height(2)
				}
				a.everywhereBody(c, files, names, lines, stopped, choose)
				a.quickFooter(c, false, locales.QuickOpenAction(), "", false, e.shown != "" && rows > 0)
			})
			if box.PressedOutside() || back.OverlayShortcut(0, ui.KeyEscape) {
				a.closeEverywhere()
				if a.focus != paneTerminal {
					a.focusColumn()
				}
			}
		})
	})
}

// everywhereBody is what Search Everywhere lists: the files found by
// name, then the lines found in them, or why there are none.
func (a *app) everywhereBody(c *ui.Context, files []string, names []workspace.Match, lines []textLine, stopped bool, choose func(int, bool)) {
	e := &a.everywhere
	p := a.pal
	if len(names) == 0 && len(lines) == 0 {
		var msg string
		switch {
		case a.root == "":
			msg = locales.EverywhereNoFolder()
		case a.indexRead.Running() && a.index == nil:
			msg = locales.QuickIndexing()
		case e.waiting || e.running:
			return // the bar shows it searches
		default:
			msg = locales.SearchNoResults()
		}
		ui.Text(c, msg).FontSize(13).TextColor(p.muted).Padding(12, 16)
		return
	}
	section := func(label string) {
		ui.Text(c, label).FontSize(11).TextColor(p.faint).SingleLine().Padding(6, 10, 3, 10)
	}
	row := func(i int, key, label string, children func()) {
		r := ui.ButtonBase(c.Key(key)).Padding(5, 10).Gap(10).Radius(6).Justify(ui.Start).AlignItems(ui.Center).Label(label)
		if i != e.sel && r.Hovered() {
			r.Background(p.hover)
		}
		if i == e.sel && e.moved {
			r.ScrollIntoView()
		}
		if r.Clicked() {
			choose(i, false)
		}
		r.Transition(hoverFade)
		r.Children(func() { a.pickMark(c, i == e.sel); children() })
	}
	ui.Scroll(c.Key("everywhere-results")).MaxHeight(380).Padding(6).Children(func() {
		if len(names) > 0 {
			section(locales.EverywhereFiles())
		}
		for i, m := range names {
			rel := files[m.Index]
			row(i, "file:"+rel, rel, func() { a.quickRow(c, rel, m.Positions) })
		}
		if len(lines) > 0 {
			section(locales.EverywhereText())
		}
		for j, l := range lines {
			at := fmt.Sprintf("%s:%d", path.Base(l.file.rel), l.match.line+1)
			row(len(names)+j, "line:"+l.file.rel+":"+fmt.Sprint(l.match.line), l.match.pre+l.match.hit+l.match.post, func() {
				icon, col := fileIcon(p, path.Base(l.file.rel))
				ui.Icon(c, icon).FontSize(15).TextColor(col)
				ui.RichText(c, a.matchPreview(l.match, false)...).FontSize(12.5).SingleLine().Grow(1).Shrink(1).MinWidth(0)
				ui.Text(c, at).FontSize(12).TextColor(p.faint).SingleLine().Shrink(0)
			})
		}
		if stopped {
			label := locales.EverywhereShowAll()
			row(len(names)+len(lines), "show-all", label, func() {
				ui.Icon(c, iconSearch).FontSize(15).TextColor(p.muted)
				ui.Text(c, label).FontSize(13).TextColor(p.muted).SingleLine()
			})
		}
	})
	e.moved = false
}

// shiftTaps tells Shift tapped twice on its own, quickly, from the
// modifiers held as each frame sees them, and the keys pressed between.
type shiftTaps struct {
	mods ui.Modifiers // as the frame before saw them
	held bool         // Shift alone is down, with no key pressed since
	down time.Time    // when it went down
	// tapped is when a tap ended, waiting for a second; zero for none.
	tapped time.Time
}

// see takes the modifiers held now, and reports whether Shift was just
// let go of after a second tap.
func (s *shiftTaps) see(mods ui.Modifiers, now time.Time) bool {
	was := s.mods
	s.mods = mods
	switch {
	case mods == was:
	case was == 0 && mods == ui.Shift:
		if now.Sub(s.tapped) > shiftTapWithin {
			s.tapped = time.Time{}
		}
		s.held, s.down = true, now
	case was == ui.Shift && mods == 0 && s.held && now.Sub(s.down) <= shiftTapWithin:
		s.held = false
		if !s.tapped.IsZero() {
			s.tapped = time.Time{}
			return true
		}
		s.tapped = now
	default:
		// Another modifier, or a hold too long for a tap.
		s.reset()
	}
	return false
}

// reset forgets the taps, as for a key that is not a modifier pressed
// between them, as Shift held to type a capital, which see does not see.
func (s *shiftTaps) reset() {
	s.held, s.tapped = false, time.Time{}
}

// seeKey has a key taken where the keyboard is end a double Shift: on
// Linux, a key that types text in a text field comes only as its text and
// its release, which for a symbol, as "(", has only its physical key.
// Shift's own release, on Windows, has neither.
func (a *app) seeKey(ev ui.InputEvent) {
	switch ev.Kind {
	case ui.InputKeyDown, ui.InputKeyUp:
		if ev.Key != ui.KeyUnknown || ev.PhysicalKey != ui.KeyUnknown {
			a.shift.reset()
		}
	case ui.InputText:
		a.shift.reset()
	}
}

// shiftTwice opens Search Everywhere when Shift was tapped twice, as in
// JetBrains' editors, except in a terminal, whose keys typed on Linux
// come only as text that its view takes, or over a dialog, which keeps
// the keyboard as it keeps the window's shortcuts.
func (a *app) shiftTwice(c *ui.Context) {
	dialog := a.dialogShown
	a.dialogShown = false
	if !a.shift.see(c.Modifiers(), c.Now()) || dialog || a.terminalFocused(c) {
		return
	}
	a.keyedAt = a.frameAt
	a.openEverywhere()
}
