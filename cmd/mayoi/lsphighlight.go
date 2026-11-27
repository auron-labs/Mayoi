package main

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"mayoi/internal/bg"
	"mayoi/internal/lsp"
)

// lspMarks are the runs of an editor's text that the language server says
// have the name at its caret, which the editor marks in place of the text
// that is the same name. They are of the text and the caret they were
// asked at, and go as either changes.
type lspMarks struct {
	// tracking is whether version and sel are the caret's, which since is
	// when it came to.
	tracking bool
	version  uint64
	sel      [2]int
	since    time.Time
	asked    bool
	// spans are the runes of each run, on a line each.
	spans [][2]int
	// serial changes as spans do, for the decorations that hold them.
	serial int
}

// lspHighlight follows the caret of d as its frame is built: once it
// rests hoverDwell, it asks the first server of d that highlights names
// for the runs, quietly. What it asked of a caret or a text gone is
// dropped, and the marks of the text show until the answer comes.
func (a *app) lspHighlight(d *editor, snap ui.TextSnapshot) {
	m := &d.wordMarks
	if d.sel[0] != d.sel[1] || len(d.cursors) > 0 {
		m.reset(0, [2]int{}, false)
		return
	}
	if !m.tracking || m.version != snap.Version() || m.sel != d.sel {
		m.reset(snap.Version(), d.sel, true)
		bg.AfterFunc(hoverDwell, a.invalidate)
		return
	}
	if m.asked || time.Since(m.since) < hoverDwell {
		return
	}
	m.asked = true
	servers := a.lspRunningFor(d.document)
	if len(servers) == 0 {
		return
	}
	version, sel := m.version, m.sel
	pos, uri, lang, text := positionOf(snap, sel[1]), serverURI(d.path), languageOf(d.document), d.buf.String()
	bg.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, c := firstReady(servers, 0, func(_ *lspServer, c *lsp.Client) bool { return c.CanHighlight() })
		if c == nil {
			return
		}
		// The server knows the text as it is now.
		c.Open(uri, lang, text)
		found, err := c.HighlightsAt(ctx, uri, lsp.Position(pos))
		if err != nil || len(found) < 2 {
			return
		}
		a.post(func() {
			if m.version != version || m.sel != sel || d.buf.Version() != version || !allCurrent(servers) {
				return
			}
			snap := d.buf.Snapshot()
			m.spans = m.spans[:0]
			for _, h := range found {
				start, end := posRune(snap, fromLSP(h.Range.Start)), posRune(snap, fromLSP(h.Range.End))
				if end > start && snap.LineAt(start) == snap.LineAt(end) {
					m.spans = append(m.spans, [2]int{start, end})
				}
			}
			m.serial++
			a.invalidate()
		})
	})
}

// reset forgets what was found and starts again for a text and caret.
func (m *lspMarks) reset(version uint64, sel [2]int, tracking bool) {
	serial := m.serial
	if len(m.spans) > 0 {
		serial++
	}
	*m = lspMarks{tracking: tracking, version: version, sel: sel, since: time.Now(), serial: serial}
}

// words are the runes the server found for the caret of d in the text of
// snap, none while it has not answered for them.
func (m *lspMarks) words(snap ui.TextSnapshot, sel [2]int) [][2]int {
	if m.version != snap.Version() || m.sel != sel {
		return nil
	}
	return m.spans
}
