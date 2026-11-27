package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"mayoi/internal/bg"
	"mayoi/internal/lsp"
	"mayoi/internal/vfs"
)

// editTarget is a file a workspace edit changes, and the document that
// holds it when it is open.
type editTarget struct {
	path  string
	doc   *document
	edits []lsp.TextEdit
}

// applyEdit makes a WorkspaceEdit of a server of the folder root: an open
// document changes as one step of its undo, a file not open is written in
// the background. An edit that cannot be read, or that names a file Mayoi
// cannot change, is made in no file. done is told, on the main loop, how
// many files changed, and why the edit failed.
func (a *app) applyEdit(root string, raw json.RawMessage, done func(files int, err error)) {
	files, err := lsp.ParseEdit(raw)
	if err != nil {
		done(0, err)
		return
	}
	var open, closed []editTarget
	for _, f := range files {
		if len(f.Edits) == 0 {
			continue
		}
		path := serverPath(root, f.URI)
		if path == "" {
			done(0, fmt.Errorf("%s is not a file Mayoi can change", f.URI))
			return
		}
		t := editTarget{path: path, edits: f.Edits}
		if i := a.docIndex(path); i >= 0 {
			if t.doc = a.docs[i]; t.doc.locked() || t.doc.main() == nil {
				done(0, fmt.Errorf("%s can't be changed now", filepath.Base(path)))
				return
			}
			open = append(open, t)
			continue
		}
		closed = append(closed, t)
	}
	if len(closed) == 0 {
		done(len(open), editDocuments(open))
		return
	}
	bg.Go(func() {
		err := writeEdits(closed)
		a.post(func() {
			if err != nil {
				done(0, err)
				return
			}
			done(len(open)+len(closed), editDocuments(open))
		})
	})
}

// editDocuments makes the edits in open documents, all or none.
func editDocuments(targets []editTarget) error {
	all := make([][]textEdit, len(targets))
	for i, t := range targets {
		snap := t.doc.buf.Snapshot()
		for _, e := range t.edits {
			s, z := posRune(snap, fromLSP(e.Range.Start)), posRune(snap, fromLSP(e.Range.End))
			all[i] = append(all[i], textEdit{min(s, z), max(s, z), e.NewText})
		}
		if editsOverlap(all[i]) {
			return fmt.Errorf("%s: the edits overlap", filepath.Base(t.path))
		}
	}
	for i, t := range targets {
		editKeepingSelection(t.doc.main(), all[i])
	}
	return nil
}

// writeEdits makes the edits in files not open, keeping their line
// endings. Every file is read and edited before any is written, so that
// one that cannot be changed leaves the others as they were. It may run
// on any goroutine.
func writeEdits(targets []editTarget) error {
	type change struct {
		path string
		data []byte
	}
	changes := make([]change, len(targets))
	for i, t := range targets {
		data, err := vfs.ReadFile(t.path)
		if err == nil && isBinary(data) {
			err = errors.New("not a text file")
		}
		if err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(t.path), err)
		}
		crlf := bytes.Contains(data, []byte("\r\n"))
		text := string(data)
		if crlf {
			text = strings.ReplaceAll(text, "\r\n", "\n")
		}
		if text, err = lsp.ApplyEdits(text, t.edits); err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(t.path), err)
		}
		if crlf {
			text = strings.ReplaceAll(text, "\n", "\r\n")
		}
		changes[i] = change{t.path, []byte(text)}
	}
	for _, c := range changes {
		if _, err := vfs.WriteFile(c.path, c.data, 0o644); err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(c.path), err)
		}
	}
	return nil
}
