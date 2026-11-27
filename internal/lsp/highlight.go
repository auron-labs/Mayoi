package lsp

import (
	"context"
	"encoding/json"
)

// Highlight is a run of a document that has the name at a place: Kind 1
// is a text, 2 a read and 3 a write of it.
type Highlight struct {
	Range Range `json:"range"`
	Kind  int   `json:"kind"`
}

// CanHighlight reports whether the server answers
// textDocument/documentHighlight.
func (c *Client) CanHighlight() bool { return c.caps.DocumentHighlight }

// HighlightsAt are the runs of the document that the name at a place is
// in, as the server finds them.
func (c *Client) HighlightsAt(ctx context.Context, uri string, pos Position) ([]Highlight, error) {
	res, err := c.Call(ctx, "textDocument/documentHighlight", docPos(uri, pos))
	if err != nil || len(res) == 0 || string(res) == "null" {
		return nil, err
	}
	var out []Highlight
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	return out, nil
}
