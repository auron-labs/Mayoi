package lsp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Signature help names the parameter in use as a run of its label, from
// the offsets or the text the server gives, and tells the server why it is
// asked, and of the help that shows as it asks again.
func TestSignatureHelp(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "MAYOI_FAKE_LSP=1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Start(ctx, Options{Cmd: cmd, RootURI: URI(t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !c.CanSignatureHelp() {
		t.Fatal("the server says it helps with signatures")
	}
	if trig, re := c.SignatureTrigger("("); !trig || re {
		t.Errorf(`"(" = %v, %v`, trig, re)
	}
	if trig, re := c.SignatureTrigger(")"); trig || !re {
		t.Errorf(`")" = %v, %v`, trig, re)
	}
	uri := URI("/work/a.fake")
	c.Open(uri, "fake", "call(1, \nnot a call\nname(\n")
	h, err := c.SignatureHelpAt(ctx, uri, Position{0, 5}, SignatureContext{Kind: SignatureTriggerChar, Char: "("})
	if err != nil || h == nil || len(h.Signatures) != 1 {
		t.Fatalf("help = %+v, %v", h, err)
	}
	s := h.Signatures[0]
	if s.Label != "call(first int, second string)" || s.Documentation != "Calls **call**." || s.Param != [2]int{5, 14} || s.ParamDoc != "The first." {
		t.Errorf("signature = %+v", s)
	}
	h, err = c.SignatureHelpAt(ctx, uri, Position{0, 8}, SignatureContext{Kind: SignatureContentChange, Active: h})
	if err != nil || h == nil {
		t.Fatalf("second parameter: %+v, %v", h, err)
	}
	if s := h.Signatures[0]; s.Label[s.Param[0]:s.Param[1]] != "second string" {
		t.Errorf("parameter in use = %+v", s)
	}
	var snap struct {
		Navigation struct {
			Context struct {
				Kind       int             `json:"triggerKind"`
				Retrigger  bool            `json:"isRetrigger"`
				ActiveHelp json.RawMessage `json:"activeSignatureHelp"`
			} `json:"context"`
		} `json:"navigation"`
	}
	res, err := c.Call(ctx, "test/snapshot", nil)
	if err != nil || json.Unmarshal(res, &snap) != nil {
		t.Fatal(err)
	}
	if x := snap.Navigation.Context; x.Kind != SignatureContentChange || !x.Retrigger || len(x.ActiveHelp) == 0 {
		t.Errorf("context = %+v", x)
	}
	if h, err := c.SignatureHelpAt(ctx, uri, Position{1, 5}, SignatureContext{Kind: SignatureInvoked}); err != nil || h != nil {
		t.Errorf("outside a call: %+v, %v", h, err)
	}
	if h, _ := c.SignatureHelpAt(ctx, uri, Position{2, 5}, SignatureContext{Kind: SignatureInvoked}); h == nil || h.Active != 1 {
		t.Errorf("active signature = %+v", h)
	}
}

// A parameter's offsets are in UTF-16 code units, and name a run of bytes
// of a label whose letters take more than one.
func TestParameterSpan(t *testing.T) {
	label := "f(é: int, 😀: int)"
	// é is one unit, 😀 two.
	if got := parameterSpan(label, json.RawMessage(`[2,8]`)); label[got[0]:got[1]] != "é: int" {
		t.Errorf("é: %v", got)
	}
	if got := parameterSpan(label, json.RawMessage(`[10,17]`)); label[got[0]:got[1]] != "😀: int" {
		t.Errorf("emoji: %v", got)
	}
	if got := parameterSpan(label, json.RawMessage(`[10,99]`)); got != [2]int{} {
		t.Errorf("past the end: %v", got)
	}
}
