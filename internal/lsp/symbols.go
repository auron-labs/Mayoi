package lsp

import (
	"context"
	"encoding/json"
	"sort"
)

// Symbol is a named declaration somewhere in the workspace. The range of
// its location is empty when the server left it out, as it may.
type Symbol struct {
	Name string `json:"name"`
	Kind int    `json:"kind"`
	// Container is what holds it, as its package or class.
	Container string   `json:"containerName"`
	Location  Location `json:"location"`
}

// CanSearchSymbols reports whether the server answers workspace/symbol.
func (c *Client) CanSearchSymbols() bool { return c.caps.WorkspaceSymbol }

// WorkspaceSymbols asks the server for the symbols of the workspace that
// match query, as it matches them.
func (c *Client) WorkspaceSymbols(ctx context.Context, query string) ([]Symbol, error) {
	res, err := c.Call(ctx, "workspace/symbol", map[string]string{"query": query})
	if err != nil || len(res) == 0 || string(res) == "null" {
		return nil, err
	}
	var out []Symbol
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	return out, nil
}

var symbolKinds = [...]string{
	1: "file", 2: "module", 3: "namespace", 4: "package", 5: "class", 6: "method", 7: "property", 8: "field",
	9: "constructor", 10: "enum", 11: "interface", 12: "function", 13: "variable", 14: "constant", 15: "string",
	16: "number", 17: "boolean", 18: "array", 19: "object", 20: "key", 21: "null", 22: "enum member",
	23: "struct", 24: "event", 25: "operator", 26: "type parameter",
}

// SymbolKind names a kind of symbol as the protocol numbers it, "" for a
// number it does not know.
func SymbolKind(kind int) string {
	if kind < 1 || kind >= len(symbolKinds) {
		return ""
	}
	return symbolKinds[kind]
}

// DocumentSymbol is a declaration of a document, in the order of the
// document: Depth is how many it is nested in, and Start where its name
// starts, or where it does, for a server that gave no name's place.
type DocumentSymbol struct {
	Name, Detail string
	Kind, Depth  int
	Start        Position
}

// CanListSymbols reports whether the server answers
// textDocument/documentSymbol.
func (c *Client) CanListSymbols() bool { return c.caps.DocumentSymbol }

// DocumentSymbols are the declarations of a document, flat or nested as
// the server has them, in the order of the document.
func (c *Client) DocumentSymbols(ctx context.Context, uri string) ([]DocumentSymbol, error) {
	res, err := c.Call(ctx, "textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": uri}})
	if err != nil || len(res) == 0 || string(res) == "null" {
		return nil, err
	}
	var list []documentSymbol
	if err := json.Unmarshal(res, &list); err != nil {
		return nil, err
	}
	out := flattenSymbols(nil, list, 0)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Start, out[j].Start
		return a.Line < b.Line || a.Line == b.Line && a.Character < b.Character
	})
	return out, nil
}

// documentSymbol is a DocumentSymbol or a SymbolInformation, as the
// server gave it: the second has a location, and the first may have
// children.
type documentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail"`
	Container      string           `json:"containerName"`
	Kind           int              `json:"kind"`
	SelectionRange *Range           `json:"selectionRange"`
	Range          *Range           `json:"range"`
	Location       *Location        `json:"location"`
	Children       []documentSymbol `json:"children"`
}

func flattenSymbols(out []DocumentSymbol, list []documentSymbol, depth int) []DocumentSymbol {
	for _, s := range list {
		sym := DocumentSymbol{Name: s.Name, Detail: s.Detail, Kind: s.Kind, Depth: depth}
		switch {
		case s.Location != nil:
			sym.Start, sym.Detail = s.Location.Range.Start, s.Container
		case s.SelectionRange != nil:
			sym.Start = s.SelectionRange.Start
		case s.Range != nil:
			sym.Start = s.Range.Start
		}
		out = flattenSymbols(append(out, sym), s.Children, depth+1)
	}
	return out
}
