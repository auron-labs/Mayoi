package lsp

import (
	"context"
	"encoding/json"
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
