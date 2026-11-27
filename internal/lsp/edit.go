package lsp

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// ErrFileOperation is why an edit is refused: it creates, renames or
// deletes files, which Mayoi does not do for a server.
var ErrFileOperation = errors.New("the edit creates, renames or deletes files")

// FileEdit is the edits of one document, of its URI.
type FileEdit struct {
	URI   string
	Edits []TextEdit
}

// ParseEdit reads a WorkspaceEdit as the edits of each document it
// changes, in the order the server gave them. An edit with any file
// operation is refused whole: one applied in part leaves the files in a
// state the server did not intend.
func ParseEdit(raw json.RawMessage) ([]FileEdit, error) {
	var e struct {
		Changes         map[string][]TextEdit `json:"changes"`
		DocumentChanges []json.RawMessage     `json:"documentChanges"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, err
	}
	var out []FileEdit
	add := func(uri string, edits []TextEdit) {
		if i := slices.IndexFunc(out, func(f FileEdit) bool { return f.URI == uri }); i >= 0 {
			out[i].Edits = append(out[i].Edits, edits...)
			return
		}
		out = append(out, FileEdit{uri, edits})
	}
	// documentChanges is the edit when both are there, as the protocol has it.
	if len(e.DocumentChanges) == 0 {
		for _, uri := range slices.Sorted(maps.Keys(e.Changes)) {
			add(uri, e.Changes[uri])
		}
		return out, nil
	}
	for _, item := range e.DocumentChanges {
		var c struct {
			Kind         string `json:"kind"`
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Edits []TextEdit `json:"edits"`
		}
		if err := json.Unmarshal(item, &c); err != nil {
			return nil, err
		}
		if c.Kind != "" {
			return nil, ErrFileOperation
		}
		add(c.TextDocument.URI, c.Edits)
	}
	return out, nil
}

// ApplyEdits makes the edits of a document in its text, which has no
// carriage returns: its positions count UTF-16 code units of its lines.
// Edits that overlap are refused.
func ApplyEdits(text string, edits []TextEdit) (string, error) {
	starts := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	type span struct {
		start, end int
		text       string
	}
	spans := make([]span, len(edits))
	for i, e := range edits {
		s, z := byteOffset(text, starts, e.Range.Start), byteOffset(text, starts, e.Range.End)
		spans[i] = span{min(s, z), max(s, z), e.NewText}
	}
	slices.SortStableFunc(spans, func(a, b span) int { return a.start - b.start })
	var b strings.Builder
	at := 0
	for _, s := range spans {
		if s.start < at {
			return "", errors.New("the edits overlap")
		}
		b.WriteString(text[at:s.start])
		b.WriteString(s.text)
		at = s.end
	}
	b.WriteString(text[at:])
	return b.String(), nil
}

// byteOffset is the offset in text of a position, clamped to its line.
func byteOffset(text string, starts []int, p Position) int {
	if p.Line < 0 {
		return 0
	}
	if p.Line >= len(starts) {
		return len(text)
	}
	i, end := starts[p.Line], len(text)
	if p.Line+1 < len(starts) {
		end = starts[p.Line+1] - 1
	}
	for units := 0; i < end && units < p.Character; {
		r, n := utf8.DecodeRuneInString(text[i:end])
		if u := utf16.RuneLen(r); u > 0 {
			units += u
		} else {
			units += 2
		}
		i += n
	}
	return i
}
