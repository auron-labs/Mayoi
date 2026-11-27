package main

import (
	"context"
	"slices"
	"sync"
	"time"

	"mayoi/internal/bg"
	"mayoi/internal/lsp"
	"mayoi/locales"
)

// offeredAction is a code action, and the server that offered it.
type offeredAction struct {
	lsp.CodeAction
	server *lspServer
	client *lsp.Client
}

// actionRank orders what is offered: the action a server prefers first,
// then the fixes of problems, then the rest.
func actionRank(a lsp.CodeAction) int {
	switch {
	case a.IsPreferred:
		return 0
	case a.IsQuickFix():
		return 1
	}
	return 2
}

// codeActions asks every server of the document for the actions at the
// selection, with the problems in it, and lists what they all offer in one
// picker, which runs the one chosen.
func (a *app) codeActions() {
	d := a.textEditor()
	if d == nil {
		return
	}
	servers := a.lspFor(d.document, false)
	if len(servers) == 0 {
		a.tell(note{message: locales.LspNone(languageOf(d.document))})
		return
	}
	current := a.lspAsked(servers, d, &a.actionAsked)
	snap := d.buf.Snapshot()
	span := lsp.Range{
		Start: lsp.Position(positionOf(snap, min(d.sel[0], d.sel[1]))),
		End:   lsp.Position(positionOf(snap, max(d.sel[0], d.sel[1]))),
	}
	uri, lang, text := serverURI(d.path), languageOf(d.document), d.buf.String()
	bg.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		type answer struct {
			c       *lsp.Client
			actions []lsp.CodeAction
			err     error
		}
		answers := make([]answer, len(servers))
		var wg sync.WaitGroup
		for i, s := range servers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c := s.wait(5 * time.Second)
				if c == nil || !c.CanCodeAction() {
					return
				}
				c.Open(uri, lang, text)
				actions, err := c.CodeActionsAt(ctx, uri, span, c.DiagnosticsIn(uri, span))
				answers[i] = answer{c, actions, err}
			}()
		}
		wg.Wait()
		a.post(func() {
			if !current() {
				return
			}
			var offered []offeredAction
			asked := 0
			for i, an := range answers {
				if an.c == nil {
					continue
				}
				asked++
				if an.err != nil {
					a.lspAnswerFailed(servers[i], "codeAction", an.err, locales.CodeActionFailed)
				}
				for _, act := range an.actions {
					offered = append(offered, offeredAction{act, servers[i], an.c})
				}
			}
			switch failed := notRunning(servers); {
			case len(offered) > 0:
			case asked == 0 && failed != nil:
				a.lspFailed(failed)
				return
			case asked == 0:
				a.tell(note{message: locales.CodeActionUnsupported()})
				return
			default:
				a.tell(note{message: locales.CodeActionNone()})
				return
			}
			slices.SortStableFunc(offered, func(x, y offeredAction) int { return actionRank(x.CodeAction) - actionRank(y.CodeAction) })
			items := make([]pickItem, len(offered))
			for i, o := range offered {
				items[i] = pickItem{Label: o.Title}
				if len(servers) > 1 {
					items[i].Description = o.server.contrib.ID
				}
			}
			a.openPluginPicker(items, locales.CodeActionPick(), func(i int) {
				if i >= 0 {
					a.runCodeAction(offered[i])
				}
			})
			a.pluginPick.icon = iconZap
		})
	})
}

// runCodeAction makes the edit of an action, resolved first when the server
// left it out, and then runs its command, which may ask for edits of its
// own.
func (a *app) runCodeAction(o offeredAction) {
	root := a.root
	bg.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		act, err := o.client.ResolveCodeAction(ctx, o.CodeAction)
		a.post(func() {
			if a.root != root || !o.server.current() {
				return
			}
			if err != nil {
				a.lspAnswerFailed(o.server, "codeAction/resolve", err, locales.CodeActionFailed)
				return
			}
			if act.Edit == nil {
				a.executeCommand(o, act.Command)
				return
			}
			a.applyEdit(root, act.Edit, func(_ int, err error) {
				if err != nil {
					a.fail("%s", locales.LspEditFailed(firstLine(err.Error())))
					return
				}
				a.executeCommand(o, act.Command)
			})
		})
	})
}

// executeCommand has the server of o run a command, nil for none.
func (a *app) executeCommand(o offeredAction, command *lsp.Command) {
	if command == nil {
		return
	}
	bg.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := o.client.Execute(ctx, *command); err != nil {
			a.post(func() { a.lspAnswerFailed(o.server, "executeCommand", err, locales.CodeActionFailed) })
		}
	})
}
