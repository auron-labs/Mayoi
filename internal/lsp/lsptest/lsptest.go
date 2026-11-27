// Package lsptest is a fake language server, for the tests of the LSP
// client and of the app: a test binary runs as one when asked.
package lsptest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Serve answers as a language server of a language whose every word
// "bad" is an error, on in and out, until it is told to exit. A word is
// defined by the lines of its document that are "def" and it, and else at
// line 3 of /tmp/def.fake. With initializationOptions failInitialize, it
// answers initialize with that error and quits, as a server that cannot
// start says why. The answer of a word "late" waits for
// test/release, and test/held says whether one waits; of hovers and
// completions, a word "latenothing" or "latebroken" waits as well. The
// hover and the completions of a word ending "nothing" are none, and of
// one ending "broken" an error. Its initializationOptions word is the word
// it flags in place of "bad", and capabilities what it says it does in
// place of all it does, as a server that only lints. test/snapshot
// answers what it was greeted with, its last document, navigation and
// settings, those of the last workspace/didChangeConfiguration. With
// initializationOptions semantic, it answers semantic tokens, a token of
// the type and modifiers it gives for each of its words in the document,
// and in place of the words of the last answer, as edits, to a delta; and
// test/refresh has it ask for them again.
func Serve(in io.Reader, out io.Writer) {
	r := bufio.NewReader(in)
	send := func(m any) {
		data, _ := json.Marshal(m)
		fmt.Fprintf(out, "Content-Length: %d\r\n\r\n%s", len(data), data)
	}
	var initialize, document, navigation, configuration json.RawMessage
	texts := map[string]string{} // by URI
	var late func()              // the answer held back
	var executing int            // the executeCommand that waits for the client's workspace/applyEdit answer
	var options struct {
		HoverContents  json.RawMessage `json:"hoverContents"`
		FailInitialize string          `json:"failInitialize"`
		Word           string          `json:"word"`
		Capabilities   json.RawMessage `json:"capabilities"`
		Semantic       *struct {
			Legend map[string][]string `json:"legend"`
			// Words are the words that are tokens, by their type's index and
			// modifiers' bits.
			Words map[string][2]uint32 `json:"words"`
		} `json:"semantic"`
		FlatSymbols bool `json:"flatSymbols"`
	}
	results := 0 // the semantic tokens answers made
	previous := map[string][]uint32{}
	for {
		n := -1
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
				n, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		body := make([]byte, n)
		io.ReadFull(r, body)
		var m struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
		}
		json.Unmarshal(body, &m)
		reply := func(result any) { send(map[string]any{"jsonrpc": "2.0", "id": *m.ID, "result": result}) }
		// holdable replies by the word at the place asked of, held back
		// when it starts prefix, none or an error by its end.
		holdable := func(prefix string, result any) {
			name := wordAt(texts, m.Params)
			id := *m.ID
			respond := func() {
				switch {
				case strings.HasSuffix(name, "broken"):
					send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32603, "message": "broken"}})
				case strings.HasSuffix(name, "nothing"):
					send(map[string]any{"jsonrpc": "2.0", "id": id, "result": nil})
				default:
					send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
				}
			}
			if strings.HasPrefix(name, prefix) && late == nil {
				late = respond
				return
			}
			respond()
		}
		answer := func(result any) { holdable("late", result) }
		switch m.Method {
		case "initialize":
			initialize = append(json.RawMessage(nil), m.Params...)
			var p struct {
				InitializationOptions json.RawMessage `json:"initializationOptions"`
			}
			json.Unmarshal(m.Params, &p)
			json.Unmarshal(p.InitializationOptions, &options)
			if options.FailInitialize != "" {
				send(map[string]any{"jsonrpc": "2.0", "id": *m.ID, "error": map[string]any{"code": -32603, "message": options.FailInitialize}})
				return
			}
			if options.Word == "" {
				options.Word = "bad"
			}
			if len(options.Capabilities) > 0 {
				reply(map[string]any{"capabilities": options.Capabilities})
				break
			}
			caps := map[string]any{
				"hoverProvider": true, "definitionProvider": true, "completionProvider": map[string]any{},
				"documentFormattingProvider": true, "textDocumentSync": 1,
				"typeDefinitionProvider": map[string]any{}, "implementationProvider": true, "referencesProvider": true,
				"workspaceSymbolProvider":   true,
				"renameProvider":            map[string]any{"prepareProvider": true},
				"codeActionProvider":        map[string]any{"resolveProvider": true, "codeActionKinds": []string{"quickfix", "refactor"}},
				"documentHighlightProvider": true, "documentSymbolProvider": true,
				"signatureHelpProvider":  map[string]any{"triggerCharacters": []string{"(", ","}, "retriggerCharacters": []string{")"}},
				"executeCommandProvider": map[string]any{"commands": []string{"fake.sourceDefinition", "fake.applyEdit"}},
			}
			if options.Semantic != nil {
				caps["semanticTokensProvider"] = map[string]any{"full": map[string]any{"delta": true}, "legend": map[string]any{
					"tokenTypes": options.Semantic.Legend["types"], "tokenModifiers": options.Semantic.Legend["modifiers"]}}
			}
			reply(map[string]any{"capabilities": caps})
		case "textDocument/semanticTokens/full", "textDocument/semanticTokens/full/delta":
			var p struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
				PreviousResultID string `json:"previousResultId"`
			}
			json.Unmarshal(m.Params, &p)
			data := semanticData(texts[p.TextDocument.URI], options.Semantic.Words)
			old := previous[p.TextDocument.URI]
			previous[p.TextDocument.URI] = data
			results++
			id := strconv.Itoa(results)
			if m.Method == "textDocument/semanticTokens/full/delta" {
				if p.PreviousResultID != strconv.Itoa(results-1) {
					send(map[string]any{"jsonrpc": "2.0", "id": *m.ID, "error": map[string]any{"code": -32603, "message": "unknown result " + p.PreviousResultID}})
					break
				}
				reply(map[string]any{"resultId": id, "edits": []any{map[string]any{"start": 0, "deleteCount": len(old), "data": data}}})
				break
			}
			reply(map[string]any{"resultId": id, "data": data})
		case "test/refresh":
			send(map[string]any{"jsonrpc": "2.0", "id": 1 << 20, "method": "workspace/semanticTokens/refresh"})
			reply(nil)
		case "textDocument/didOpen", "textDocument/didChange":
			document = append(json.RawMessage(nil), m.Params...)
			var p struct {
				TextDocument struct {
					URI  string `json:"uri"`
					Text string `json:"text"`
				} `json:"textDocument"`
				ContentChanges []struct {
					Text string `json:"text"`
				} `json:"contentChanges"`
			}
			json.Unmarshal(m.Params, &p)
			text := p.TextDocument.Text
			if len(p.ContentChanges) > 0 {
				text = p.ContentChanges[0].Text
			}
			texts[p.TextDocument.URI] = text
			var diags []any
			for i, line := range strings.Split(text, "\n") {
				if at := strings.Index(line, options.Word); at >= 0 {
					diags = append(diags, map[string]any{"range": map[string]any{
						"start": map[string]any{"line": i, "character": at}, "end": map[string]any{"line": i, "character": at + len(options.Word)},
					}, "severity": 1, "message": options.Word + " word", "source": "fake"})
				}
			}
			send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics",
				"params": map[string]any{"uri": p.TextDocument.URI, "diagnostics": diags}})
		case "textDocument/hover":
			// "plain", "marked" and "long" answer as their names say, with
			// the range of the word.
			line, s, e, word := wordSpan(texts, m.Params)
			at := map[string]any{"start": map[string]any{"line": line, "character": s}, "end": map[string]any{"line": line, "character": e}}
			switch {
			case len(options.HoverContents) > 0:
				answer(map[string]any{"contents": options.HoverContents})
			case word == "plain":
				answer(map[string]any{"contents": map[string]any{"kind": "plaintext", "value": "*not* bold\nnext line"}, "range": at})
			case word == "marked":
				answer(map[string]any{"contents": []any{map[string]any{"language": "go", "value": "func marked(x int) string"}, "Returns **x**."}, "range": at})
			case word == "long":
				answer(map[string]any{"contents": map[string]any{"kind": "markdown",
					"value": "```go\nfunc long()\n```\n\n" + strings.Repeat("A line of documentation that goes on.\n\n", 60)}, "range": at})
			default:
				answer(map[string]any{"contents": map[string]any{"kind": "markdown", "value": "**hello**"}})
			}
		case "textDocument/signatureHelp":
			navigation = m.Params
			reply(signatureHelp(texts, m.Params))
		case "textDocument/documentHighlight":
			holdable("hold", documentHighlights(texts, m.Params))
		case "textDocument/documentSymbol":
			reply(documentSymbols(texts, m.Params, options.FlatSymbols))
		case "workspace/symbol":
			reply(workspaceSymbols(initialize, m.Params))
		case "workspace/didChangeConfiguration":
			configuration = append(json.RawMessage(nil), m.Params...)
		case "test/snapshot":
			reply(map[string]any{"initialize": initialize, "document": document, "navigation": navigation,
				"configuration": configuration})
		case "test/noReply":
			// A response timeout must not disconnect an otherwise healthy client.
		case "textDocument/definition":
			defs, name := definitions(texts, m.Params)
			if defs == nil {
				defs = []any{map[string]any{"uri": "file:///tmp/def.fake", "range": map[string]any{
					"start": map[string]any{"line": 3, "character": 1}, "end": map[string]any{"line": 3, "character": 4}}}}
			}
			if name == "late" && late == nil {
				late = func() { reply(defs) }
				break
			}
			reply(defs)
		case "textDocument/typeDefinition":
			navigation = m.Params
			reply(json.RawMessage(`[{"targetUri":"file:///tmp/def.fake","targetRange":{"start":{"line":1,"character":0}},
				"targetSelectionRange":{"start":{"line":3,"character":1},"end":{"line":3,"character":4}}}]`))
		case "textDocument/implementation", "textDocument/references", "workspace/executeCommand":
			var c struct {
				Command   string            `json:"command"`
				Arguments []json.RawMessage `json:"arguments"`
			}
			json.Unmarshal(m.Params, &c)
			if m.Method == "workspace/executeCommand" && c.Command == "fake.applyEdit" && len(c.Arguments) == 1 {
				// Asks the client to apply the edit it is given, and answers
				// what the client says of it.
				executing = *m.ID
				send(map[string]any{"jsonrpc": "2.0", "id": applyEditID, "method": "workspace/applyEdit",
					"params": map[string]any{"edit": c.Arguments[0]}})
				break
			}
			navigation = m.Params
			// References of a word of a document are where it is defined.
			if defs, _ := definitions(texts, m.Params); defs != nil && m.Method == "textDocument/references" {
				reply(defs)
				break
			}
			reply(json.RawMessage(`[{"uri":"file:///tmp/def.fake","range":{"start":{"line":3,"character":1},"end":{"line":3,"character":4}}}]`))
		case "test/held":
			reply(late != nil)
		case "test/release":
			// The answer held goes before this one's.
			if late != nil {
				late()
				late = nil
			}
			reply(nil)
		case "textDocument/completion":
			answer(map[string]any{"isIncomplete": false, "items": []any{map[string]any{"label": "println", "insertText": "println()"}}})
		case "textDocument/formatting":
			reply([]any{map[string]any{"range": map[string]any{
				"start": map[string]any{"line": 0, "character": 0}, "end": map[string]any{"line": 0, "character": 0}}, "newText": "// formatted\n"}})
		case "textDocument/prepareRename":
			at, ok := wordRange(texts, m.Params)
			if !ok {
				reply(nil)
				break
			}
			answer(map[string]any{"range": at, "placeholder": wordAt(texts, m.Params)})
		case "textDocument/rename":
			reply(rename(texts, rootDir(initialize), m.Params))
		case "textDocument/codeAction":
			reply(codeActions(m.Params))
		case "codeAction/resolve":
			var a map[string]any
			json.Unmarshal(m.Params, &a)
			data, _ := a["data"].(map[string]any)
			uri, _ := data["uri"].(string)
			a["edit"] = map[string]any{"changes": map[string]any{uri: []any{insertion(0, 0, "// resolved\n")}}}
			reply(a)
		case "":
			// The client's answer to workspace/applyEdit is the answer to
			// the command that asked for it.
			if m.ID != nil && *m.ID == applyEditID {
				var r struct {
					Applied bool `json:"applied"`
				}
				json.Unmarshal(m.Result, &r)
				send(map[string]any{"jsonrpc": "2.0", "id": executing, "result": map[string]any{"applied": r.Applied}})
			}
		case "shutdown":
			reply(nil)
		case "exit":
			return
		default:
			if m.ID != nil {
				reply(nil)
			}
		}
	}
}

// signatureHelp is the help of the call open at a place: the signature
// "name(first int, second string)" of the word before its last open
// parenthesis, with the parameter after as many commas as there are
// after the parenthesis; none outside a call. "name" has two signatures,
// the second the active one.
func signatureHelp(texts map[string]string, params json.RawMessage) any {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Position struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"position"`
	}
	json.Unmarshal(params, &p)
	lines := strings.Split(texts[p.TextDocument.URI], "\n")
	if p.Position.Line >= len(lines) {
		return nil
	}
	before := lines[p.Position.Line][:min(p.Position.Character, len(lines[p.Position.Line]))]
	open := strings.LastIndex(before, "(")
	if open < 0 || strings.Contains(before[open:], ")") {
		return nil
	}
	start := open
	for start > 0 && before[start-1] != ' ' {
		start--
	}
	name := before[start:open]
	sig := map[string]any{
		"label":         name + "(first int, second string)",
		"documentation": map[string]any{"kind": "markdown", "value": "Calls **" + name + "**."},
		"parameters": []any{
			map[string]any{"label": []int{len(name) + 1, len(name) + 10}, "documentation": "The first."},
			map[string]any{"label": "second string"},
		},
	}
	help := map[string]any{"signatures": []any{sig}, "activeParameter": strings.Count(before[open:], ",")}
	if name == "name" {
		help["signatures"] = []any{map[string]any{"label": "name()"}, sig}
		help["activeSignature"] = 1
	}
	return help
}

// documentHighlights are the whole-word occurrences of the word at a place
// in the document's text, a write where "def " comes before it and else a
// read, one letter words as well. The answer of a word "hold" waits as one
// "late" does.
func documentHighlights(texts map[string]string, params json.RawMessage) any {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	json.Unmarshal(params, &p)
	word := wordAt(texts, params)
	out := []any{}
	for i, line := range strings.Split(texts[p.TextDocument.URI], "\n") {
		letter := func(i int) bool {
			return i >= 0 && i < len(line) && (line[i] >= 'a' && line[i] <= 'z' || line[i] >= '0' && line[i] <= '9')
		}
		for at := 0; word != "" && at+len(word) <= len(line); at++ {
			if line[at:at+len(word)] != word || letter(at-1) || letter(at+len(word)) {
				continue
			}
			kind := 2
			if strings.HasSuffix(line[:at], "def ") {
				kind = 3
			}
			out = append(out, map[string]any{"kind": kind, "range": map[string]any{
				"start": map[string]any{"line": i, "character": at}, "end": map[string]any{"line": i, "character": at + len(word)}}})
		}
	}
	return out
}

// documentSymbols are the lines of the document's text that start "sym ",
// each a function named by the rest of its line: one indented is a child
// of the one before it, unless flat, which gives them as symbol
// information.
func documentSymbols(texts map[string]string, params json.RawMessage, flat bool) any {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	json.Unmarshal(params, &p)
	at := func(line, col int) map[string]any {
		return map[string]any{"line": line, "character": col}
	}
	var out []any
	var last map[string]any
	for i, line := range strings.Split(texts[p.TextDocument.URI], "\n") {
		indent := len(line) - len(strings.TrimLeft(line, " "))
		name, ok := strings.CutPrefix(line[indent:], "sym ")
		if !ok {
			continue
		}
		start, end := at(i, indent+4), at(i, indent+4+len(name))
		if flat {
			out = append(out, map[string]any{"name": name, "kind": 12, "containerName": "pkg",
				"location": map[string]any{"uri": p.TextDocument.URI, "range": map[string]any{"start": at(i, 0), "end": end}}})
			continue
		}
		sym := map[string]any{"name": name, "kind": 12, "detail": "()", "children": []any{},
			"range": map[string]any{"start": at(i, 0), "end": end}, "selectionRange": map[string]any{"start": start, "end": end}}
		if indent == 0 || last == nil {
			out = append(out, sym)
			last = sym
			continue
		}
		last["children"] = append(last["children"].([]any), sym)
	}
	return out
}

// workspaceSymbols are the symbols of the fake workspace whose names hold
// the query: Widget, with its range, and WidgetList, as a workspace
// symbol with only its file, both in the folder it was started in.
func workspaceSymbols(initialize, params json.RawMessage) any {
	var in struct {
		RootURI string `json:"rootUri"`
	}
	json.Unmarshal(initialize, &in)
	var q struct {
		Query string `json:"query"`
	}
	json.Unmarshal(params, &q)
	has := func(name string) bool { return strings.Contains(strings.ToLower(name), strings.ToLower(q.Query)) }
	var out []any
	if has("Widget") {
		out = append(out, map[string]any{"name": "Widget", "kind": 5, "containerName": "shapes", "location": map[string]any{
			"uri": in.RootURI + "/sym.fake", "range": map[string]any{
				"start": map[string]any{"line": 2, "character": 6}, "end": map[string]any{"line": 2, "character": 12}}}})
	}
	if has("WidgetList") {
		out = append(out, map[string]any{"name": "WidgetList", "kind": 23, "location": map[string]any{"uri": in.RootURI + "/other.fake"}})
	}
	return out
}

// definitions are where the word at a place follows "def" on a line of
// the document's text, as many times as it does there, and the word: an
// empty list when the word is "nothing", nil when it is defined in none.
func definitions(texts map[string]string, params json.RawMessage) ([]any, string) {
	name := wordAt(texts, params)
	if name == "nothing" {
		return []any{}, name
	}
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	json.Unmarshal(params, &p)
	lines := strings.Split(texts[p.TextDocument.URI], "\n")
	var out []any
	for i, l := range lines {
		rest, ok := strings.CutPrefix(l, "def ")
		if !ok || name == "" {
			continue
		}
		at := 4
		for _, w := range strings.Split(rest, " ") {
			if w == name {
				out = append(out, map[string]any{"uri": p.TextDocument.URI, "range": map[string]any{
					"start": map[string]any{"line": i, "character": at}, "end": map[string]any{"line": i, "character": at + len(name)}}})
			}
			at += len(w) + 1
		}
	}
	return out, name
}

// wordAt is the word at the place of a request's params, of the
// document's text.
func wordAt(texts map[string]string, params json.RawMessage) string {
	_, _, _, word := wordSpan(texts, params)
	return word
}

// wordSpan is the word at the place of a request's params, its line and
// where on it it starts and ends.
func wordSpan(texts map[string]string, params json.RawMessage) (line, start, end int, word string) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Position struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"position"`
	}
	json.Unmarshal(params, &p)
	lines := strings.Split(texts[p.TextDocument.URI], "\n")
	if p.Position.Line >= len(lines) {
		return p.Position.Line, 0, 0, ""
	}
	text := lines[p.Position.Line]
	letter := func(r byte) bool { return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' }
	s, e := min(p.Position.Character, len(text)), min(p.Position.Character, len(text))
	for s > 0 && letter(text[s-1]) {
		s--
	}
	for e < len(text) && letter(text[e]) {
		e++
	}
	return p.Position.Line, s, e, text[s:e]
}

// semanticData is the relative encoding of the tokens of text: a token for
// each occurrence of each of the words, whose values are the type and the
// modifiers.
func semanticData(text string, words map[string][2]uint32) []uint32 {
	data := []uint32{}
	lastLine, lastStart := 0, 0
	for i, line := range strings.Split(text, "\n") {
		type hit struct {
			at   int
			word string
		}
		var hits []hit
		for w := range words {
			for from := 0; ; {
				at := strings.Index(line[from:], w)
				if at < 0 {
					break
				}
				hits = append(hits, hit{from + at, w})
				from += at + len(w)
			}
		}
		sort.Slice(hits, func(a, b int) bool { return hits[a].at < hits[b].at })
		for _, h := range hits {
			start := len(utf16.Encode([]rune(line[:h.at])))
			dLine, dStart := i-lastLine, start
			if dLine == 0 {
				dStart = start - lastStart
			}
			data = append(data, uint32(dLine), uint32(dStart), uint32(len(utf16.Encode([]rune(h.word)))), words[h.word][0], words[h.word][1])
			lastLine, lastStart = i, start
		}
	}
	return data
}
