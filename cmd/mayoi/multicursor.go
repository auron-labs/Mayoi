package main

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

// An editor has more than one caret once an Alt-click adds one, or a drag
// with Alt and the middle button selects a column: its own selection, sel,
// which its text area holds, and the others, carets, which the editor
// paints. The text area types at its own caret, and the editor finds what
// was typed and types it at the others; the keys that move and delete, it
// takes from the area and acts on at each.

// boxDrag is a column selection being dragged: the line it started on and
// how far along the text it started.
type boxDrag struct {
	line int
	x    float32
}

// multiCaret reports whether an editor can have more than one caret: not
// in Vim's normal and visual modes, which have a cursor of their own, nor
// in Live Preview, whose text is not laid out in cells.
func (a *app) multiCaret(d *editor) bool {
	return !(a.prefs().Vim && d.vim.mode != vimInsert) && !a.livePreview(d)
}

type caretRun uint8

const (
	// eachLine runs a command of whole lines once on each line: a
	// selection on a line it ran on already is passed over.
	eachLine caretRun = 1 << iota
	// eachTyping joins the step of undo to the typing before it, as the
	// keys that delete do.
	eachTyping
)

// eachCaret runs fn at each selection of d, from the last in the text to
// the first, as one step of undo: fn reads the selection from d.sel and
// edits and selects as it would at a caret alone, while the selections it
// has yet to run at move with its edits. i is the selection's place in the
// text, from 0.
func eachCaret(d *editor, how caretRun, fn func(d *editor, i int)) {
	d.observe()
	h := &d.hist
	all := append([][2]int{d.sel}, d.carets...)
	order := make([]int, len(all))
	for k := range order {
		order[k] = k
	}
	slices.SortFunc(order, func(x, y int) int { return cmp.Compare(selStart(all[y]), selStart(all[x])) })
	// The selections move with the edits as the offsets a command tracks
	// do: the anchor and the caret of each in turn.
	tracked := d.tracked
	d.tracked = make([]int, 0, 2*len(all))
	for _, s := range all {
		d.tracked = append(d.tracked, s[0], s[1])
	}
	sel0, carets0 := d.sel, slices.Clone(d.carets)
	opened := h.group == groupNone
	if opened {
		h.group = groupOpen
		if how&eachTyping != 0 && h.typing && time.Since(h.at) <= typingPause && len(h.undo) > 0 {
			h.group = groupJoin
		}
	}
	stepped := h.group == groupOpen
	version := d.buf.Snapshot().Version()
	above := -1 // the first line a command of lines ran on last
	for rank, k := range order {
		sel := [2]int{d.tracked[2*k], d.tracked[2*k+1]}
		if how&eachLine != 0 {
			first, last := lineSpan(d.buf.Snapshot(), sel)
			if above >= 0 && last >= above {
				continue
			}
			above = first
		}
		d.sel, d.pending = sel, nil
		fn(d, len(order)-1-rank)
		if d.pending != nil {
			sel = *d.pending
		} else {
			sel = d.sel
		}
		d.tracked[2*k], d.tracked[2*k+1] = sel[0], sel[1]
	}
	for k := range all {
		all[k] = [2]int{d.tracked[2*k], d.tracked[2*k+1]}
	}
	d.tracked = tracked
	if opened {
		if stepped && h.group == groupJoin {
			// The step the first edit made is of the selections before them all.
			s := &h.undo[len(h.undo)-1]
			s.sel, s.carets = sel0, carets0
		}
		h.group = groupNone
	}
	d.setCarets(all)
	if how&eachTyping != 0 && d.buf.Snapshot().Version() != version {
		h.typing, h.at = true, time.Now()
	}
}

// setCarets makes all the editor's selections, its own first, merging
// those that join, and selects its own as typing does. Typing after starts
// a step of undo, as it does after the caret moves.
func (d *editor) setCarets(all [][2]int) {
	snap := d.buf.Snapshot()
	n := snap.Len()
	for k, s := range all {
		all[k] = [2]int{min(max(s[0], 0), n), min(max(s[1], 0), n)}
	}
	sel, rest := mergeCarets(all)
	d.carets, d.caretsAt, d.goals = rest, snap.Version(), nil
	d.hist.typing = false
	d.selectTyped(sel[0], sel[1])
	d.keepFor = d.pending
}

func selStart(s [2]int) int { return min(s[0], s[1]) }
func selEnd(s [2]int) int   { return max(s[0], s[1]) }

// joins reports whether two selections are one: they overlap, start
// together, or one is a caret at an end of the other. Two selections that
// only touch stay apart.
func joins(x, y [2]int) bool {
	if selStart(y) < selStart(x) {
		x, y = y, x
	}
	x0, x1, y0, y1 := selStart(x), selEnd(x), selStart(y), selEnd(y)
	return y0 < x1 || y0 == x0 || y0 == x1 && (x0 == x1 || y0 == y1)
}

// mergeCarets merges the selections of all that join, the editor's own
// first: it returns that one, grown by those it took in, and the others,
// in their order in the text.
func mergeCarets(all [][2]int) ([2]int, [][2]int) {
	type run struct {
		sel [2]int
		own bool
	}
	runs := make([]run, len(all))
	for k, s := range all {
		runs[k] = run{s, k == 0}
	}
	slices.SortStableFunc(runs, func(x, y run) int {
		if c := cmp.Compare(selStart(x.sel), selStart(y.sel)); c != 0 {
			return c
		}
		return cmp.Compare(selEnd(x.sel), selEnd(y.sel))
	})
	out := runs[:1]
	for _, r := range runs[1:] {
		last := &out[len(out)-1]
		if !joins(last.sel, r.sel) {
			out = append(out, r)
			continue
		}
		// The editor's own, or else the first, keeps its direction.
		dir := last.sel
		if r.own {
			dir = r.sel
		}
		lo, hi := selStart(last.sel), max(selEnd(last.sel), selEnd(r.sel))
		last.sel = [2]int{lo, hi}
		if dir[1] < dir[0] {
			last.sel = [2]int{hi, lo}
		}
		last.own = last.own || r.own
	}
	var own [2]int
	rest := make([][2]int, 0, len(out)-1)
	for _, r := range out {
		if r.own {
			own = r.sel
		} else {
			rest = append(rest, r.sel)
		}
	}
	return own, rest
}

// typedOver returns the text typed over selection sel of text old that
// makes text now, and reports whether now is that: old with the runes of
// sel replaced.
func typedOver(old, now ui.TextSnapshot, sel [2]int) (string, bool) {
	a, b := selStart(sel), selEnd(sel)
	delta := now.Len() - old.Len()
	if b > old.Len() || b+delta < a {
		return "", false
	}
	start, end, ok := changedRunes(old, now, a)
	// The change shows further along when what was typed matches what
	// follows, as an l typed before an l: the runes between are the same.
	if !ok || start < a || end > b && old.Slice(b, end) != now.Slice(b+delta, end+delta) {
		return "", false
	}
	return now.Slice(a, b+delta), true
}

// typeEach types text over each selection, as typing at the editor's own
// caret, which the text area typed at, does.
func (d *editor) typeEach(text string) {
	eachCaret(d, eachTyping, func(d *editor, _ int) { typeSel(d, text) })
}

// typeSel types text over the editor's selection, the caret after it.
func typeSel(d *editor, text string) {
	a, b := selStart(d.sel), selEnd(d.sel)
	if a == b && text == "" {
		return
	}
	at := a + utf8.RuneCountInString(text)
	d.edit([2]int{at, at}, textEdit{a, b, text})
}

// caretsKey takes the keys that move, delete and break lines at each caret
// while the editor has more than one, queueing what they do for the next
// frame. A key that moves only the text area's caret first leaves it the
// only one.
func (a *app) caretsKey(d *editor, ev ui.InputEvent) bool {
	if len(d.carets) == 0 {
		return false
	}
	mods, shift := ev.Mods&^ui.Shift, ev.Mods&ui.Shift != 0
	mac := ui.Cmd == ui.Super // as the text area tells, by the system it runs on
	word := mods == ui.Ctrl && !mac || mods == ui.Alt && mac
	// ⌘ and an arrow go to the edge of the line, or of the text.
	edge := mods == ui.Super && mac
	var cmd func(d *editor)
	switch ev.Key {
	case ui.KeyEscape:
		if ev.Mods != 0 {
			return false
		}
		d.carets = nil
		a.invalidate()
		return true
	case ui.KeyLeft, ui.KeyRight:
		back := ev.Key == ui.KeyLeft
		switch {
		case edge && back:
			cmd = func(d *editor) { eachCaret(d, 0, func(d *editor, _ int) { home(d, shift) }) }
		case edge:
			cmd = func(d *editor) { moveEach(d, shift, lineEnd) }
		case mods == 0 || word:
			cmd = func(d *editor) {
				moveEach(d, shift, func(snap ui.TextSnapshot, s [2]int) int {
					switch {
					case word && back:
						return wordLeft(snap, s[1])
					case word:
						return wordRight(snap, s[1])
					case s[0] != s[1] && !shift && back:
						return selStart(s)
					case s[0] != s[1] && !shift:
						return selEnd(s)
					case back:
						return max(s[1]-1, 0)
					}
					return s[1] + 1
				})
			}
		}
	case ui.KeyUp, ui.KeyDown:
		up := ev.Key == ui.KeyUp
		switch {
		case edge:
			cmd = func(d *editor) { moveEach(d, shift, textEdge(up)) }
		case mods == 0:
			dir := 1
			if up {
				dir = -1
			}
			cmd = func(d *editor) { a.verticalEach(d, shift, dir) }
		}
	case ui.KeyHome, ui.KeyEnd:
		start := ev.Key == ui.KeyHome
		switch {
		case mods == ui.Ctrl:
			cmd = func(d *editor) { moveEach(d, shift, textEdge(start)) }
		case mods == 0 && start:
			cmd = func(d *editor) { eachCaret(d, 0, func(d *editor, _ int) { home(d, shift) }) }
		case mods == 0:
			cmd = func(d *editor) { moveEach(d, shift, lineEnd) }
		}
	case ui.KeyBackspace, ui.KeyDelete:
		back := ev.Key == ui.KeyBackspace
		if mods == 0 || word || edge && back {
			cmd = func(d *editor) { deleteEach(d, back, word, edge) }
		}
	case ui.KeyEnter:
		switch {
		case ev.Mods == 0:
			cmd = func(d *editor) { eachCaret(d, 0, func(d *editor, _ int) { newline(d) }) }
		case mods == ui.Cmd && !mdTaskKey(d):
			cmd = atEachLine(func(d *editor) { insertLine(d, shift) })
		case ev.Mods == ui.Shift:
			return false // the text area breaks the line, typed at each caret
		}
	case ui.KeyTab:
		if mods == 0 {
			cmd = func(d *editor) {
				// Indenting whole lines, a line indents once for all the
				// carets on it.
				var how caretRun
				snap := d.buf.Snapshot()
				for _, s := range append([][2]int{d.sel}, d.carets...) {
					if first, last := lineSpan(snap, s); shift || first != last {
						how = eachLine
					}
				}
				eachCaret(d, how, func(d *editor, _ int) { tab(d, shift) })
			}
		}
	case ui.KeyC:
		if ev.Mods != ui.Cmd {
			return false
		}
		cmd = a.copySel
	case ui.KeyPageUp, ui.KeyPageDown:
	default:
		if !(mac && mods == ui.Ctrl) {
			return false // the keys of the shortcuts, and of typing
		}
		// macOS's Control keys move and delete as Emacs does.
	}
	if cmd == nil {
		d.carets = nil
		return false
	}
	d.cmds = append(d.cmds, cmd)
	return true
}

// moveEach moves each caret to where to puts it, the selection going with
// it while extend is set, and else ending there.
func moveEach(d *editor, extend bool, to func(snap ui.TextSnapshot, s [2]int) int) {
	snap := d.buf.Snapshot()
	all := append([][2]int{d.sel}, d.carets...)
	for k, s := range all {
		c := min(max(to(snap, s), 0), snap.Len())
		if extend {
			all[k] = [2]int{s[0], c}
		} else {
			all[k] = [2]int{c, c}
		}
	}
	d.setCarets(all)
}

// lineEnd is the end of the line of a selection's caret.
func lineEnd(snap ui.TextSnapshot, s [2]int) int {
	_, e := snap.LineRange(snap.LineAt(min(s[1], snap.Len())))
	return e
}

// textEdge returns where a key that goes to the start of the text, or its
// end, takes a caret.
func textEdge(start bool) func(snap ui.TextSnapshot, s [2]int) int {
	return func(snap ui.TextSnapshot, _ [2]int) int {
		if start {
			return 0
		}
		return snap.Len()
	}
}

// wordLeft returns the start of the word before offset i, past what is not
// a word on the way.
func wordLeft(snap ui.TextSnapshot, i int) int {
	t := newVText(snap)
	i = min(i, snap.Len())
	for i > 0 && !isWordRune(t.at(i-1)) {
		i--
	}
	for i > 0 && isWordRune(t.at(i-1)) {
		i--
	}
	return i
}

// wordRight returns the end of the word after offset i, past what is not a
// word on the way.
func wordRight(snap ui.TextSnapshot, i int) int {
	t := newVText(snap)
	n := snap.Len()
	for i < n && !isWordRune(t.at(i)) {
		i++
	}
	for i < n && isWordRune(t.at(i)) {
		i++
	}
	return i
}

// verticalEach moves each caret a row up, dir -1, or down, each keeping to
// the column it went up or down from first.
func (a *app) verticalEach(d *editor, extend bool, dir int) {
	snap := d.buf.Snapshot()
	if d.goalsAt != snap.Version() {
		d.goals = nil
	}
	g := a.geometry(d, snap.LineCount())
	all := append([][2]int{d.sel}, d.carets...)
	goals := make(map[int]int, len(all))
	for k, s := range all {
		c := min(s[1], snap.Len())
		goal, ok := d.goals[c]
		if !ok {
			line := snap.LineAt(c)
			ls, _ := snap.LineRange(line)
			goal = columns(snap.Slice(ls+g.starts(line)[g.row(line, c-ls)], c))
		}
		to := vertical(g, snap, c, goal, dir)
		goals[to] = goal
		if extend {
			all[k] = [2]int{s[0], to}
		} else {
			all[k] = [2]int{to, to}
		}
	}
	d.setCarets(all)
	d.goals, d.goalsAt = goals, d.caretsAt
}

// starts returns where the rows of line i start, from the start of the
// line: one row, unless the editor wraps it.
func (g geometry) starts(i int) []int {
	if g.lines != nil {
		if rows := g.lines.Rows(i); len(rows) > 0 {
			return rows
		}
	}
	return []int{0}
}

// vertical returns where a caret at offset off goes a row up, dir -1, or
// down: to column goal of that row, the start of the text above the first
// row, and its end below the last.
func vertical(g geometry, snap ui.TextSnapshot, off, goal, dir int) int {
	line := snap.LineAt(off)
	s, _ := snap.LineRange(line)
	rows := g.starts(line)
	k := g.row(line, off-s) + dir
	switch {
	case k < 0 && line == 0:
		return 0
	case k < 0:
		line--
		rows = g.starts(line)
		k = len(rows) - 1
	case k >= len(rows) && line == snap.LineCount()-1:
		return snap.Len()
	case k >= len(rows):
		line++
		rows = g.starts(line)
		k = 0
	}
	s, e := snap.LineRange(line)
	if k+1 < len(rows) {
		e = s + rows[k+1] - 1 // a caret at the next row's start shows on it
	}
	return atColumn(snap, s+rows[k], e, goal)
}

// deleteEach deletes each selection, or from each caret what the key
// deletes: a rune back or forward, a word, or the line before it.
func deleteEach(d *editor, back, word, edge bool) {
	eachCaret(d, eachTyping, func(d *editor, _ int) {
		snap := d.buf.Snapshot()
		n := snap.Len()
		a, b := min(selStart(d.sel), n), min(selEnd(d.sel), n)
		if a == b {
			switch {
			case back && edge:
				a, _ = snap.LineRange(snap.LineAt(a))
			case back && word:
				a = wordLeft(snap, a)
			case back:
				a = max(a-1, 0)
			case word:
				b = wordRight(snap, b)
			default:
				b = min(b+1, n)
			}
		}
		if a < b {
			d.edit([2]int{a, a}, textEdit{a, b, ""})
		}
	})
}

// atEachLine runs a command of whole lines once on the lines of each
// selection.
func atEachLine(cmd func(d *editor)) func(d *editor) {
	return func(d *editor) {
		if len(d.carets) == 0 {
			cmd(d)
			return
		}
		eachCaret(d, eachLine, func(d *editor, _ int) { cmd(d) })
	}
}

// selTexts returns the text of each selection, in their order in the text.
func selTexts(d *editor) []string {
	snap := d.buf.Snapshot()
	n := snap.Len()
	all := append([][2]int{d.sel}, d.carets...)
	slices.SortFunc(all, func(x, y [2]int) int { return cmp.Compare(selStart(x), selStart(y)) })
	out := make([]string, len(all))
	for k, s := range all {
		out[k] = snap.Slice(min(selStart(s), n), min(selEnd(s), n))
	}
	return out
}

// copySel copies what is selected, a line for each selection, nothing
// while all are carets.
func (a *app) copySel(d *editor) {
	texts := selTexts(d)
	if slices.ContainsFunc(texts, func(s string) bool { return s != "" }) {
		a.services.WriteClipboard(strings.Join(texts, "\n"))
	}
}

// cutEach cuts what is selected, a line of the clipboard for each
// selection, or while all are carets, the lines they are on.
func (a *app) cutEach(d *editor) {
	texts := selTexts(d)
	if slices.ContainsFunc(texts, func(s string) bool { return s != "" }) {
		a.services.WriteClipboard(strings.Join(texts, "\n"))
		eachCaret(d, 0, func(d *editor, _ int) { typeSel(d, "") })
		return
	}
	snap := d.buf.Snapshot()
	n := snap.Len()
	lines := make([]int, 0, 1+len(d.carets))
	for _, s := range append([][2]int{d.sel}, d.carets...) {
		lines = append(lines, snap.LineAt(min(s[1], n)))
	}
	slices.Sort(lines)
	var b strings.Builder
	for _, l := range slices.Compact(lines) {
		s, e := snap.LineRange(l)
		b.WriteString(snap.Slice(s, e) + "\n")
	}
	a.services.WriteClipboard(b.String())
	eachCaret(d, eachLine, func(d *editor, _ int) { deleteLines(d) })
}

// pasteEach pastes text at each selection: a line of it at each when it
// has as many lines as there are selections, else all of it.
func pasteEach(d *editor, text string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	each := len(lines) == 1+len(d.carets)
	eachCaret(d, 0, func(d *editor, i int) {
		if each {
			typeSel(d, lines[i])
		} else {
			typeSel(d, text)
		}
	})
}

// caretsPointer adds a caret with an Alt-click, or takes away the one
// clicked, starts a column selection with a press of the middle button
// with Alt, and leaves the text area's caret the only one at any other
// click.
func (a *app) caretsPointer(d *editor, ev ui.InputEvent) bool {
	switch {
	case d.box != nil && ev.Kind == ui.InputPointerMove && ev.Button == 2:
		box, x, y := *d.box, ev.X, ev.Y
		a.queueEdit(d, func(d *editor) { a.boxSelect(d, box, x, y) })
		return true
	case d.box != nil && ev.Kind == ui.InputPointerUp:
		d.box = nil
		return true
	case ev.Kind != ui.InputPointerDown:
		return false
	case ev.Button == 2 && ev.Mods == ui.Alt && a.multiCaret(d):
		snap := d.buf.Snapshot()
		line := a.geometry(d, snap.LineCount()).at(ev.Y + d.vscroll.Y - editorPadY)
		box, x, y := boxDrag{line: line, x: ev.X - d.textX}, ev.X, ev.Y
		// Only the primary button moves the focus by itself.
		d.box, d.focusNext = &box, true
		a.queueEdit(d, func(d *editor) { a.boxSelect(d, box, x, y) })
		return true
	case ev.Button != 0:
		return false
	case ev.Mods == ui.Alt && ev.Clicks == 1 && a.multiCaret(d):
		at := a.offsetAt(d, ev.X, ev.Y)
		all := append([][2]int{d.sel}, d.carets...)
		if k := slices.Index(all, [2]int{at, at}); k >= 0 && len(all) > 1 {
			// An Alt-click on a caret takes it away; the last in the text
			// takes the place of the text area's own.
			all = slices.Delete(all, k, k+1)
			if k == 0 {
				all[0], all[len(all)-1] = all[len(all)-1], all[0]
			}
			d.setCarets(all)
			a.invalidate()
			return true
		}
		// The click puts the text area's caret; its selection stays, as
		// another.
		d.carets, d.caretsAt, d.goals = append(d.carets, d.sel), d.version, nil
	case ev.Mods&ui.Alt == 0:
		d.carets = nil
	}
	return false
}

// offsetAt returns the offset at x, y of an editor's text area, by the
// cells of its lines, as the last frame laid them out.
func (a *app) offsetAt(d *editor, x, y float32) int {
	snap := d.buf.Snapshot()
	g := a.geometry(d, snap.LineCount())
	cy := y + d.vscroll.Y - editorPadY
	line := g.at(cy)
	rows := g.starts(line)
	k := min(max(int((cy-g.top(line))/a.font.lineH), 0), len(rows)-1)
	s, e := snap.LineRange(line)
	if k+1 < len(rows) {
		e = s + rows[k+1] - 1
	}
	return atColumn(snap, s+rows[k], e, a.columnAt(x-d.textX))
}

// columnAt is the column nearest x, from the start of a line.
func (a *app) columnAt(x float32) int {
	return max(int(math.Round(float64(x/a.font.charW))), 0)
}

// boxSelect selects the columns from where box started to x, y on each
// line between, the editor's own selection where the pointer is: a line
// too short to reach them is left out, but for the first.
func (a *app) boxSelect(d *editor, box boxDrag, x, y float32) {
	snap := d.buf.Snapshot()
	l0 := min(box.line, snap.LineCount()-1)
	l1 := a.geometry(d, snap.LineCount()).at(y + d.vscroll.Y - editorPadY)
	c0, c1 := a.columnAt(box.x), a.columnAt(x-d.textX)
	step := 1
	if l1 < l0 {
		step = -1
	}
	var sels [][2]int
	for line := l0; ; line += step {
		s, e := snap.LineRange(line)
		if line == l0 || columns(snap.Slice(s, e)) >= min(c0, c1) {
			sels = append(sels, [2]int{atColumn(snap, s, e, c0), atColumn(snap, s, e, c1)})
		}
		if line == l1 {
			break
		}
	}
	last := len(sels) - 1
	sels[0], sels[last] = sels[last], sels[0]
	d.setCarets(sels)
}

// caretShapes returns the selections of an editor's carets on lines first
// up to last, and the carets, a cell's width for each.
func (a *app) caretShapes(d *editor, snap ui.TextSnapshot, carets [][2]int, first, last int) (*selectionShape, []runeRect) {
	sh := &d.caretShape
	sh.rects = sh.rects[:0]
	var bars []runeRect
	f, n := a.font, snap.Len()
	for _, s := range carets {
		s = [2]int{min(s[0], n), min(s[1], n)}
		sh.rects, _ = selRects(sh.rects, f, snap, s, first, last)
		l := snap.LineAt(s[1])
		if l < first || l > last {
			continue
		}
		ls, le := snap.LineRange(l)
		x := float32(s[1]-ls) * f.charW
		if le-ls <= highlightRunes {
			x = f.width(snap.Slice(ls, s[1]))
		}
		bars = append(bars, runeRect{line: l, x0: x, x1: x + f.charW})
	}
	return sh, bars
}
