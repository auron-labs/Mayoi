package lsp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"unicode/utf16"
)

// What asked for signature help, as the protocol numbers it.
const (
	SignatureInvoked       = 1
	SignatureTriggerChar   = 2
	SignatureContentChange = 3
)

// SignatureContext is why signature help is asked for: Kind, the
// character typed for SignatureTriggerChar, and the help that shows when
// this asks again.
type SignatureContext struct {
	Kind   int
	Char   string
	Active *SignatureHelp
}

// SignatureHelp is the signatures of the call at a place, of which Active
// is the one in use.
type SignatureHelp struct {
	Signatures []Signature
	Active     int
	// raw is what the server said, which it is told of as it is asked
	// again.
	raw json.RawMessage
}

// Signature is a way to call: its label, and in it the run of bytes of
// the parameter in use, [0 0] for none, with its documentation.
type Signature struct {
	Label         string
	Documentation string // Markdown
	Param         [2]int
	ParamDoc      string // Markdown
}

// CanSignatureHelp reports whether the server answers
// textDocument/signatureHelp.
func (c *Client) CanSignatureHelp() bool { return c.caps.SignatureHelp }

// SignatureTrigger reports whether typing char asks for signature help,
// and whether it asks again for help that shows.
func (c *Client) SignatureTrigger(char string) (trigger, retrigger bool) {
	return slices.Contains(c.caps.SignatureTriggers, char), slices.Contains(c.caps.SignatureRetriggers, char)
}

// SignatureHelpAt is the signature help at a place, nil for none.
func (c *Client) SignatureHelpAt(ctx context.Context, uri string, pos Position, why SignatureContext) (*SignatureHelp, error) {
	cx := map[string]any{"triggerKind": why.Kind, "isRetrigger": why.Active != nil}
	if why.Char != "" {
		cx["triggerCharacter"] = why.Char
	}
	if why.Active != nil {
		cx["activeSignatureHelp"] = why.Active.raw
	}
	params := docPos(uri, pos)
	params["context"] = cx
	res, err := c.Call(ctx, "textDocument/signatureHelp", params)
	if err != nil || len(res) == 0 || string(res) == "null" {
		return nil, err
	}
	var h struct {
		Signatures []struct {
			Label         string          `json:"label"`
			Documentation json.RawMessage `json:"documentation"`
			Parameters    []struct {
				Label         json.RawMessage `json:"label"`
				Documentation json.RawMessage `json:"documentation"`
			} `json:"parameters"`
			ActiveParameter *int `json:"activeParameter"`
		} `json:"signatures"`
		ActiveSignature *int `json:"activeSignature"`
		ActiveParameter *int `json:"activeParameter"`
	}
	if err := json.Unmarshal(res, &h); err != nil {
		return nil, err
	}
	if len(h.Signatures) == 0 {
		return nil, nil
	}
	out := &SignatureHelp{raw: res}
	if h.ActiveSignature != nil && *h.ActiveSignature >= 0 && *h.ActiveSignature < len(h.Signatures) {
		out.Active = *h.ActiveSignature
	}
	for _, s := range h.Signatures {
		sig := Signature{Label: s.Label, Documentation: markupText(s.Documentation)}
		active := h.ActiveParameter
		if s.ActiveParameter != nil {
			active = s.ActiveParameter
		}
		if active != nil && *active >= 0 && *active < len(s.Parameters) {
			p := s.Parameters[*active]
			sig.Param = parameterSpan(s.Label, p.Label)
			sig.ParamDoc = markupText(p.Documentation)
		}
		out.Signatures = append(out.Signatures, sig)
	}
	return out, nil
}

// parameterSpan is the bytes of label that a parameter's label names: a
// text to find in it, or offsets in UTF-16 code units. [0 0] when it
// names none.
func parameterSpan(label string, raw json.RawMessage) [2]int {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if i := strings.Index(label, text); text != "" && i >= 0 {
			return [2]int{i, i + len(text)}
		}
		return [2]int{}
	}
	var units [2]int
	if json.Unmarshal(raw, &units) != nil || units[0] < 0 || units[0] >= units[1] {
		return [2]int{}
	}
	var out [2]int
	found, at := 0, 0
	for i, r := range label {
		for ; found < 2 && at == units[found]; found++ {
			out[found] = i
		}
		at += len(utf16.Encode([]rune{r}))
	}
	for ; found < 2 && at == units[found]; found++ {
		out[found] = len(label)
	}
	if found < 2 {
		return [2]int{}
	}
	return out
}
