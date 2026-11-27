package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// SemanticLegend names the token types and modifiers a server's semantic
// tokens number.
type SemanticLegend struct {
	Types     []string `json:"tokenTypes"`
	Modifiers []string `json:"tokenModifiers"`
}

// The token types and modifiers the protocol defines, which the client
// says it understands.
var (
	semanticTokenTypes = []string{"namespace", "type", "class", "enum", "interface", "struct", "typeParameter", "parameter",
		"variable", "property", "enumMember", "event", "function", "method", "macro", "keyword", "modifier", "comment",
		"string", "number", "regexp", "operator", "decorator"}
	semanticTokenModifiers = []string{"declaration", "definition", "readonly", "static", "deprecated", "abstract", "async",
		"modification", "documentation", "defaultLibrary"}
)

// semanticProvider is what a server says of its semantic tokens.
type semanticProvider struct {
	Legend SemanticLegend
	Full   bool
	Delta  bool
}

// SemanticToken is a run of one line, from a start in UTF-16 units, of a
// type the legend names by its index, and of the modifiers whose indices
// are the bits set in Modifiers.
type SemanticToken struct {
	Line, Start, Length int
	Type                int
	Modifiers           uint32
}

// semanticState is the tokens last answered of a document, as the integers
// the server sent, which its next answer may be edits of.
type semanticState struct {
	id   string
	data []uint32
}

// SemanticTokens reports whether the server answers textDocument/semanticTokens/full.
func (c *Client) SemanticTokens() bool { return c.semantic.Full }

// SemanticLegend is the legend of the server's semantic tokens.
func (c *Client) SemanticLegend() SemanticLegend { return c.semantic.Legend }

// parseSemanticProvider reads the semanticTokensProvider of a server's
// capabilities, whose full is true or an object that may offer deltas.
func parseSemanticProvider(raw json.RawMessage) semanticProvider {
	var p struct {
		Legend SemanticLegend  `json:"legend"`
		Full   json.RawMessage `json:"full"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return semanticProvider{}
	}
	var full struct {
		Delta bool `json:"delta"`
	}
	json.Unmarshal(p.Full, &full)
	return semanticProvider{Legend: p.Legend, Full: string(p.Full) != "" && string(p.Full) != "false" && string(p.Full) != "null", Delta: full.Delta}
}

// SemanticTokensOf asks for the semantic tokens of a document, as edits of
// the last answer where the server gives them.
func (c *Client) SemanticTokensOf(ctx context.Context, uri string) ([]SemanticToken, error) {
	c.mu.Lock()
	prev := c.tokens[uri]
	c.mu.Unlock()
	params := map[string]any{"textDocument": map[string]any{"uri": uri}}
	method := "textDocument/semanticTokens/full"
	var res json.RawMessage
	var err error
	if c.semantic.Delta && prev.id != "" {
		params["previousResultId"] = prev.id
		res, err = c.Call(ctx, method+"/delta", params)
		// A server that no longer has the result answers an error: ask for
		// all of them.
		var refused *ResponseError
		if errors.As(err, &refused) {
			delete(params, "previousResultId")
			res, err = nil, nil
		}
	}
	if res == nil && err == nil {
		res, err = c.Call(ctx, method, params)
	}
	if err != nil {
		return nil, err
	}
	var out struct {
		ID    string   `json:"resultId"`
		Data  []uint32 `json:"data"`
		Edits []struct {
			Start       int      `json:"start"`
			DeleteCount int      `json:"deleteCount"`
			Data        []uint32 `json:"data"`
		} `json:"edits"`
	}
	if len(res) == 0 || string(res) == "null" {
		return nil, nil
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	data := out.Data
	if out.Edits != nil {
		data = slices.Clone(prev.data)
		// Offsets are those of the data before the edits, so from the last.
		for i := len(out.Edits) - 1; i >= 0; i-- {
			e := out.Edits[i]
			if e.Start < 0 || e.DeleteCount < 0 || e.Start+e.DeleteCount > len(data) {
				return nil, fmt.Errorf("semantic tokens edit out of range")
			}
			data = slices.Replace(data, e.Start, e.Start+e.DeleteCount, e.Data...)
		}
	}
	c.mu.Lock()
	if _, open := c.versions[uri]; open {
		c.tokens[uri] = semanticState{out.ID, data}
	}
	c.mu.Unlock()
	return decodeSemanticTokens(data), nil
}

// decodeSemanticTokens turns the relative encoding, five integers a token:
// the lines and the units since the last token, its length, type and
// modifiers, into absolute tokens. A trailing partial token is ignored.
func decodeSemanticTokens(data []uint32) []SemanticToken {
	out := make([]SemanticToken, 0, len(data)/5)
	line, start := 0, 0
	for i := 0; i+5 <= len(data); i += 5 {
		if data[i] > 0 {
			line += int(data[i])
			start = 0
		}
		start += int(data[i+1])
		out = append(out, SemanticToken{line, start, int(data[i+2]), int(data[i+3]), data[i+4]})
	}
	return out
}
