package lsptest

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// applyEditID is the id of the workspace/applyEdit request that the command
// fake.applyEdit sends.
const applyEditID = 9001

// rootDir is the folder the server was started in, "" when it has none on
// disk.
func rootDir(initialize json.RawMessage) string {
	var in struct {
		RootURI string `json:"rootUri"`
	}
	json.Unmarshal(initialize, &in)
	u, err := url.Parse(in.RootURI)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	return filepath.FromSlash(u.Path)
}

func insertion(line, character int, text string) map[string]any {
	at := map[string]any{"line": line, "character": character}
	return map[string]any{"range": map[string]any{"start": at, "end": at}, "newText": text}
}

// wordRange is the range of the word at the place of a request's params,
// false where there is none.
func wordRange(texts map[string]string, params json.RawMessage) (map[string]any, bool) {
	line, s, e, word := wordSpan(texts, params)
	if word == "" {
		return nil, false
	}
	return map[string]any{"start": map[string]any{"line": line, "character": s}, "end": map[string]any{"line": line, "character": e}}, true
}

// rename is the edit that renames the word at a place, wherever it is in
// the documents open and in the files of the root folder, as
// documentChanges.
func rename(texts map[string]string, root string, params json.RawMessage) any {
	var p struct {
		NewName string `json:"newName"`
	}
	json.Unmarshal(params, &p)
	word := wordAt(texts, params)
	files := maps.Clone(texts)
	if entries, err := os.ReadDir(root); err == nil && root != "" {
		for _, e := range entries {
			path := filepath.Join(root, e.Name())
			uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
			if _, open := files[uri]; open {
				continue
			}
			if data, err := os.ReadFile(path); err == nil {
				files[uri] = string(data)
			}
		}
	}
	changes := []any{}
	for uri, text := range files {
		var edits []any
		for i, line := range strings.Split(text, "\n") {
			for at := 0; word != ""; {
				j := strings.Index(line[at:], word)
				if j < 0 {
					break
				}
				start := at + j
				edits = append(edits, map[string]any{"range": map[string]any{
					"start": map[string]any{"line": i, "character": start}, "end": map[string]any{"line": i, "character": start + len(word)},
				}, "newText": p.NewName})
				at = start + len(word)
			}
		}
		if len(edits) > 0 {
			changes = append(changes, map[string]any{"textDocument": map[string]any{"uri": uri, "version": nil}, "edits": edits})
		}
	}
	return map[string]any{"documentChanges": changes}
}

// codeActions are what the server offers at a place: a preferred quick fix
// for each diagnostic given, which turns its range into "good"; "Extract",
// which has its edit as it is resolved; a command that applies an edit
// through the client; and an action with an edit and a command, which
// applies both, the edit first.
func codeActions(params json.RawMessage) any {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Context struct {
			Diagnostics []map[string]any `json:"diagnostics"`
		} `json:"context"`
	}
	json.Unmarshal(params, &p)
	uri := p.TextDocument.URI
	insert := func(text string) map[string]any {
		return map[string]any{"changes": map[string]any{uri: []any{insertion(0, 0, text)}}}
	}
	out := []any{
		map[string]any{"title": "Extract", "kind": "refactor.extract", "data": map[string]any{"uri": uri}},
		map[string]any{"title": "Apply through the client", "command": "fake.applyEdit", "arguments": []any{insert("// applied\n")}},
		map[string]any{"title": "Edit and command", "kind": "refactor", "edit": insert("// first\n"),
			"command": map[string]any{"title": "Then", "command": "fake.applyEdit", "arguments": []any{insert("// second\n")}}},
	}
	for _, d := range p.Context.Diagnostics {
		out = append(out, map[string]any{"title": "Fix " + fmt.Sprint(d["message"]), "kind": "quickfix", "isPreferred": true,
			"diagnostics": []any{d},
			"edit":        map[string]any{"changes": map[string]any{uri: []any{map[string]any{"range": d["range"], "newText": "good"}}}}})
	}
	return out
}
