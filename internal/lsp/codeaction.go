package lsp

import (
	"context"
	"encoding/json"
	"strings"
)

// Command is a command of the server's, which it runs as it is asked to.
type Command struct {
	Title     string          `json:"title"`
	Command   string          `json:"command"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// CodeAction is a change a server offers at a place: an edit, a command, or
// both, as an edit is made first. A plain Command the server offered is one
// whose Edit is nil.
type CodeAction struct {
	Title string
	// Kind is the protocol's, as "quickfix" or "refactor.extract".
	Kind        string
	IsPreferred bool
	// Edit is a WorkspaceEdit, nil when the server leaves it to
	// ResolveCodeAction.
	Edit    json.RawMessage
	Command *Command
	// raw is the action as the server sent it, which it is resolved by.
	raw json.RawMessage
}

// IsQuickFix reports whether the action fixes a problem.
func (a CodeAction) IsQuickFix() bool {
	return a.Kind == "quickfix" || strings.HasPrefix(a.Kind, "quickfix.")
}

// CanCodeAction reports whether the server offers code actions.
func (c *Client) CanCodeAction() bool { return c.caps.CodeAction }

// DiagnosticsIn are the diagnostics of a document that overlap a range, as
// the server last published them.
func (c *Client) DiagnosticsIn(uri string, r Range) []Diagnostic {
	before := func(a, b Position) bool { return a.Line < b.Line || a.Line == b.Line && a.Character < b.Character }
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Diagnostic
	for _, d := range c.diagnostics[uri] {
		if !before(r.End, d.Range.Start) && !before(d.Range.End, r.Start) {
			out = append(out, d)
		}
	}
	return out
}

// CodeActionsAt asks for the actions that apply to a range, with the
// diagnostics in it.
func (c *Client) CodeActionsAt(ctx context.Context, uri string, r Range, diagnostics []Diagnostic) ([]CodeAction, error) {
	if diagnostics == nil {
		diagnostics = []Diagnostic{}
	}
	res, err := c.Call(ctx, "textDocument/codeAction", map[string]any{
		"textDocument": map[string]any{"uri": uri}, "range": r,
		"context": map[string]any{"diagnostics": diagnostics},
	})
	if err != nil || len(res) == 0 || string(res) == "null" {
		return nil, err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(res, &items); err != nil {
		return nil, err
	}
	var out []CodeAction
	for _, item := range items {
		if a, ok := parseCodeAction(item); ok {
			out = append(out, a)
		}
	}
	return out, nil
}

// parseCodeAction reads a Command or a CodeAction, which is told from a
// command by its command being an object. One the server says is disabled,
// or that does nothing, is left out.
func parseCodeAction(raw json.RawMessage) (CodeAction, bool) {
	var a struct {
		Title       string          `json:"title"`
		Kind        string          `json:"kind"`
		IsPreferred bool            `json:"isPreferred"`
		Disabled    json.RawMessage `json:"disabled"`
		Edit        json.RawMessage `json:"edit"`
		Command     json.RawMessage `json:"command"`
		Arguments   json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(raw, &a) != nil || a.Title == "" || len(a.Disabled) > 0 && string(a.Disabled) != "null" {
		return CodeAction{}, false
	}
	var name string
	if json.Unmarshal(a.Command, &name) == nil && name != "" {
		return CodeAction{Title: a.Title, Command: &Command{a.Title, name, a.Arguments}}, true
	}
	var command *Command
	json.Unmarshal(a.Command, &command)
	if string(a.Edit) == "null" {
		a.Edit = nil
	}
	return CodeAction{Title: a.Title, Kind: a.Kind, IsPreferred: a.IsPreferred, Edit: a.Edit, Command: command, raw: raw}, true
}

// ResolveCodeAction has the server fill in the edit of an action that has
// none, when it resolves actions; else the action is as it was.
func (c *Client) ResolveCodeAction(ctx context.Context, a CodeAction) (CodeAction, error) {
	if a.Edit != nil || a.raw == nil || !c.caps.ResolveCodeAction {
		return a, nil
	}
	res, err := c.Call(ctx, "codeAction/resolve", a.raw)
	if err != nil {
		return a, err
	}
	resolved, ok := parseCodeAction(res)
	if !ok {
		return a, nil
	}
	if resolved.Command == nil {
		resolved.Command = a.Command
	}
	return resolved, nil
}

// Execute asks the server to run one of its commands.
func (c *Client) Execute(ctx context.Context, command Command) error {
	params := map[string]any{"command": command.Command}
	if len(command.Arguments) > 0 {
		params["arguments"] = command.Arguments
	}
	_, err := c.Call(ctx, "workspace/executeCommand", params)
	return err
}
