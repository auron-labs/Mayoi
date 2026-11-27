package main

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"mayoi/internal/bg"
	"mayoi/internal/lsp"
	"mayoi/locales"
)

// signatureHelp is the signature help of the call being typed in an
// editor: it starts as a trigger character is typed, or Show Signature
// Help runs, asks again as the text or the caret change, and ends as the
// caret leaves the call, the editor loses the keyboard, a list opens, the
// server has none, or Esc.
type signatureHelp struct {
	ed *editor
	// help is what the server last said, nil until it answers, and docs
	// the Markdown of its documentation.
	help *lsp.SignatureHelp
	docs []markdownBlock
	// start is the rune of the character typed that started it, which the
	// caret before ends the call; -1 when the command started it.
	start int
	// version and sel are the text and the caret it was last asked at.
	version uint64
	sel     [2]int
	picks   int
	// focused is set once the editor had the keyboard, as it need not
	// yet when a command started it.
	focused bool
	// card places it at the caret as a hover card is, and keeps where it
	// laid out.
	card *hoverCard
}

// signatureTyped asks for signature help as text was typed in d, if the
// first server of d that gives it takes text as a trigger, or as a
// retrigger while the help shows.
func (a *app) signatureTyped(d *editor, text string) {
	for _, s := range a.lspRunningFor(d.document) {
		if !s.client.CanSignatureHelp() {
			continue
		}
		trigger, retrigger := s.client.SignatureTrigger(text)
		if trigger || retrigger && a.sig.ed == d {
			if a.sig.ed != d {
				a.beginSignature(d, d.sel[1]-utf8.RuneCountInString(text))
			}
			a.askSignature(d, lsp.SignatureContext{Kind: lsp.SignatureTriggerChar, Char: text}, false)
		}
		return
	}
}

// showSignatureHelp is Show Signature Help: the help of the call at the
// caret, whichever character was typed last.
func (a *app) showSignatureHelp() {
	d := a.textEditor()
	if d == nil {
		return
	}
	if a.sig.ed != d {
		a.beginSignature(d, -1)
	}
	a.askSignature(d, lsp.SignatureContext{Kind: lsp.SignatureInvoked}, true)
}

func (a *app) beginSignature(d *editor, start int) {
	a.sig = signatureHelp{ed: d, start: start, picks: a.picksOpened, card: &hoverCard{ed: d}}
}

// closeSignature ends the help, and drops the answer still to come.
func (a *app) closeSignature() {
	a.sigAsked++
	a.sig = signatureHelp{}
}

// askSignature asks the first server of d that gives signature help, at
// the caret. A person who asked is told when there is none; a character
// typed asks of servers that run, and says nothing when there is none.
func (a *app) askSignature(d *editor, why lsp.SignatureContext, asked bool) {
	var servers []*lspServer
	if asked {
		servers = a.lspFor(d.document, false)
	} else {
		servers = a.lspRunningFor(d.document)
	}
	if len(servers) == 0 {
		a.closeSignature()
		if asked {
			a.tell(note{message: locales.LspNone(languageOf(d.document))})
		}
		return
	}
	s := &a.sig
	snap := d.buf.Snapshot()
	s.version, s.sel = snap.Version(), d.sel
	s.card.at, s.card.xKnown = min(d.sel[1], snap.Len()), false
	why.Active = s.help
	pos, uri, lang, text := caretPosition(d), serverURI(d.path), languageOf(d.document), d.buf.String()
	current := a.lspAsked(servers, d, &a.sigAsked)
	limit := time.Duration(0)
	if asked {
		limit = 3 * time.Second
	}
	bg.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var help *lsp.SignatureHelp
		_, c := firstReady(servers, limit, func(_ *lspServer, c *lsp.Client) bool { return c.CanSignatureHelp() })
		if c != nil {
			// The server knows the text as it is now.
			c.Open(uri, lang, text)
			help, _ = c.SignatureHelpAt(ctx, uri, lsp.Position(pos), why)
		}
		a.post(func() {
			if !current() {
				return
			}
			if help != nil {
				s.help, s.docs = help, signatureDocs(help.Signatures[help.Active])
				return
			}
			a.closeSignature()
			if !asked {
				return
			}
			if srv := notRunning(servers); srv != nil && c == nil {
				a.lspFailed(srv)
				return
			}
			a.tell(note{message: locales.SignatureNone()})
		})
	})
}

// signatureDocs is the documentation of a signature as Markdown blocks:
// the parameter in use first.
func signatureDocs(sig lsp.Signature) []markdownBlock {
	var parts []string
	for _, doc := range []string{sig.ParamDoc, sig.Documentation} {
		if strings.TrimSpace(doc) != "" {
			parts = append(parts, doc)
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return parseMarkdown([]byte(strings.Join(parts, "\n\n")), "", "", nil)
}

// signatureView follows the help as its frame is built: it asks for what
// a character typed asks for, ends it where it ends, asks again as the
// text or the caret changed, and draws the card at the caret.
func (a *app) signatureView(c *ui.Context) {
	s := &a.sig
	if d := a.activeEditor(); d != nil && d.typedRune != "" {
		text := d.typedRune
		d.typedRune = ""
		a.signatureTyped(d, text)
	}
	d := s.ed
	if d == nil {
		return
	}
	s.focused = s.focused || a.keyFocus == placeEditor
	if a.activeEditor() != d || s.focused && a.keyFocus != placeEditor || a.blurred || a.picksOpened != s.picks || a.pluginPick.open {
		a.closeSignature()
		return
	}
	if d.sel[1] < s.start {
		a.closeSignature()
		return
	}
	if d.buf.Version() != s.version || d.sel != s.sel {
		a.askSignature(d, lsp.SignatureContext{Kind: lsp.SignatureContentChange}, false)
		if a.sig.ed == nil {
			return
		}
	}
	if s.help == nil {
		return
	}
	at, shows := a.hoverAnchor(s.card)
	if !shows {
		return // back as the line scrolls into view
	}
	p, f := a.pal, a.font
	sig := s.help.Signatures[s.help.Active]
	spans := []ui.Span{}
	for i, part := range []string{sig.Label[:sig.Param[0]], sig.Label[sig.Param[0]:sig.Param[1]], sig.Label[sig.Param[1]:]} {
		if part == "" {
			continue
		}
		sp := ui.Span{Text: part, Font: a.settings.FontFamily, Weight: a.settings.FontWeight, Color: p.text}
		if i == 1 {
			sp.Weight, sp.Color = 700, p.accent
		}
		spans = append(spans, sp)
	}
	winW, winH := c.Size()
	x, w, edge, maxH, above := hoverPlace(at, d.lastView, winW, s.card.rect.W)
	ui.Overlay(c, func() {
		box := a.floating(c, ui.Column(c.Key("signature-card")).Absolute().Left(x).MaxWidth(w).MaxHeight(maxH).Radius(8).Clip(), false).
			Transition(popIn).Role(ui.RoleTooltip).Label(locales.CmdGoSignatureHelp())
		if above {
			box.Bottom(winH - edge)
		} else {
			box.Top(edge)
		}
		box.Children(func() {
			ui.Scroll(c.Key("signature-scroll")).TrackScroll(&s.card.scroll).Children(func() {
				col := ui.Column(c.Key("signature-label")).Padding(8, 12).Background(p.editor)
				if len(s.docs) > 0 {
					col.BorderWidth(0, 0, 1, 0).BorderColor(p.border)
				}
				col.Children(func() { ui.RichText(c, spans...).FontSize(f.size).Selectable() })
				if len(s.docs) > 0 {
					ui.Column(c.Key("signature-docs")).Padding(8, 12).Children(func() {
						a.markdownBlocksView(c, "signature-docs", s.docs, a.settings.FontSize)
					})
				}
			})
		})
		if s.card.rect.W == 0 && x < at.X {
			// Kept from the edge by a width it may not have: shown the
			// frame after, where its own puts it.
			box.Opacity(0)
			a.invalidate()
		}
		if b := box.Bounds(); b.W > 0 {
			s.card.rect = b
		}
	})
}
