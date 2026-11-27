package main

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"mayoi/internal/bg"
	"mayoi/internal/lsp"
	"mayoi/locales"
)

// renameSymbol renames what is at the caret, in every file the first of the
// document's servers that renames says it is in. The server says what can
// be renamed, the person gives the new name, and the server's edit is made
// as any server's is.
func (a *app) renameSymbol() {
	d := a.textEditor()
	if d == nil {
		return
	}
	servers := a.lspFor(d.document, false)
	if len(servers) == 0 {
		a.tell(note{message: locales.LspNone(languageOf(d.document))})
		return
	}
	current := a.lspAsked(servers, d, &a.renameAsked)
	pos, uri, lang, text := caretPosition(d), serverURI(d.path), languageOf(d.document), d.buf.String()
	snap := d.buf.Snapshot()
	caret := min(d.sel[1], snap.Len())
	bg.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s, c := firstReady(servers, 5*time.Second, func(_ *lspServer, c *lsp.Client) bool { return c.CanRename() })
		if c == nil {
			a.post(func() {
				switch failed := notRunning(servers); {
				case !current():
				case failed != nil:
					a.lspFailed(failed)
				default:
					a.tell(note{message: locales.RenameUnsupported()})
				}
			})
			return
		}
		c.Open(uri, lang, text)
		target := &lsp.RenameTarget{}
		var err error
		if c.CanPrepareRename() {
			target, err = c.PrepareRename(ctx, uri, lsp.Position(pos))
		}
		a.post(func() {
			if !current() {
				return
			}
			if err != nil {
				a.lspAnswerFailed(s, "rename", err, locales.RenameFailed)
				return
			}
			name := renameName(snap, target, caret)
			if name == "" {
				a.tell(note{message: locales.RenameNothing()})
				return
			}
			a.askNewName(d, s, c, name)
		})
	})
}

// renameName is the name to start renaming from: the server's placeholder,
// or the text of the range it gave, or the word at the caret.
func renameName(snap ui.TextSnapshot, target *lsp.RenameTarget, caret int) string {
	switch {
	case target == nil:
		return ""
	case target.Placeholder != "":
		return target.Placeholder
	case target.Range != nil:
		return snap.Slice(posRune(snap, fromLSP(target.Range.Start)), posRune(snap, fromLSP(target.Range.End)))
	}
	s, e := wordAtCaret(snap, caret)
	return snap.Slice(s, e)
}

// askNewName asks for the new name, and has s rename.
func (a *app) askNewName(d *editor, s *lspServer, c *lsp.Client, name string) {
	pos, uri, root, version := caretPosition(d), serverURI(d.path), a.root, d.buf.Version()
	a.prompt = &prompt{title: locales.CmdEditRename(), value: name, whole: true, done: func(value string) string {
		value = strings.TrimSpace(value)
		if value == "" || value == name {
			return ""
		}
		bg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			edit, err := c.Rename(ctx, uri, lsp.Position(pos), value)
			a.post(func() {
				switch {
				case a.root != root || !s.current():
				case err != nil:
					a.lspAnswerFailed(s, "rename", err, locales.RenameFailed)
				case d.buf.Version() != version:
					a.fail("%s", locales.RenameStale())
				case edit == nil:
					a.tell(note{message: locales.RenameNothing()})
				default:
					a.applyEdit(root, edit, func(files int, err error) {
						switch {
						case err != nil:
							a.fail("%s", locales.LspEditFailed(firstLine(err.Error())))
						case files == 0:
							a.tell(note{message: locales.RenameNothing()})
						case files > 1:
							a.tell(note{message: locales.RenamedIn(files)})
						}
					})
				}
			})
		})
		return ""
	}}
}
