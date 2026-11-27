package lsp

import (
	"context"
	"encoding/json"
)

// CanRename reports whether the server renames symbols, and CanPrepareRename
// whether it says what can be renamed at a place before it does.
func (c *Client) CanRename() bool        { return c.caps.Rename }
func (c *Client) CanPrepareRename() bool { return c.caps.PrepareRename }

// providerFlag is whether a server's provider, which is a boolean or its
// options, has the option key on.
func providerFlag(provider json.RawMessage, key string) bool {
	var options map[string]json.RawMessage
	return json.Unmarshal(provider, &options) == nil && string(options[key]) == "true"
}

// RenameTarget is what a server says can be renamed at a place: the range
// of its name, nil when the server leaves it to the client to take the
// word there, and the name to start from, "" for the text of the range.
type RenameTarget struct {
	Range       *Range
	Placeholder string
}

// PrepareRename asks what can be renamed at a place; nil when nothing can.
func (c *Client) PrepareRename(ctx context.Context, uri string, pos Position) (*RenameTarget, error) {
	res, err := c.Call(ctx, "textDocument/prepareRename", docPos(uri, pos))
	if err != nil || len(res) == 0 || string(res) == "null" {
		return nil, err
	}
	// A Range, a range and a placeholder, or the client's own default.
	var r struct {
		Start           *Position `json:"start"`
		End             Position  `json:"end"`
		Range           *Range    `json:"range"`
		Placeholder     string    `json:"placeholder"`
		DefaultBehavior bool      `json:"defaultBehavior"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, err
	}
	switch {
	case r.Start != nil:
		return &RenameTarget{Range: &Range{*r.Start, r.End}}, nil
	case r.Range != nil:
		return &RenameTarget{Range: r.Range, Placeholder: r.Placeholder}, nil
	case r.DefaultBehavior:
		return &RenameTarget{}, nil
	}
	return nil, nil
}

// Rename asks for the WorkspaceEdit that renames what is at a place, nil
// when there is none.
func (c *Client) Rename(ctx context.Context, uri string, pos Position, name string) (json.RawMessage, error) {
	params := docPos(uri, pos)
	params["newName"] = name
	res, err := c.Call(ctx, "textDocument/rename", params)
	if err != nil || string(res) == "null" {
		return nil, err
	}
	return res, nil
}
