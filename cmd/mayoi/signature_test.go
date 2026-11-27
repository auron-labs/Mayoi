package main

import (
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

// signatureOf opens a fake file of text, the caret at its end, with its
// server up.
func signatureOf(hs *harness, text string) *editor {
	path := hs.path("a.fake")
	os.WriteFile(path, []byte(text), 0o644)
	hs.a.openFile(path, true)
	d := hs.a.activeEditor()
	serverUp(hs)
	hs.place(d, len(text), len(text))
	return d
}

// A trigger character typed shows the card of the signature, its parameter
// in use named as the caret moves on; Esc, leaving the call, and a server
// with nothing to say close it.
func TestSignatureHelpTyped(t *testing.T) {
	hs := withFakeServer(t)
	a := hs.a
	d := signatureOf(hs, "call")
	hs.tt.Type("(")
	hs.until("the card", func() bool { return a.sig.help != nil })
	hs.settle()
	if !hs.tt.HasText("call(first int, second string)") {
		t.Fatalf("no signature: %q", hs.tt.Texts())
	}
	if sig := a.sig.help.Signatures[0]; sig.Label[sig.Param[0]:sig.Param[1]] != "first int" {
		t.Errorf("first parameter: %+v", sig)
	}

	// Typing on, in the call, moves to the next parameter.
	hs.tt.Type("1,")
	hs.until("the second parameter", func() bool {
		sig := a.sig.help.Signatures[0]
		return sig.Label[sig.Param[0]:sig.Param[1]] == "second string"
	})

	// Esc closes it, and nothing else.
	hs.key(0, ui.KeyEscape)
	if a.sig.ed != nil || a.sig.help != nil {
		t.Fatal("Esc left the help")
	}
	expectText(t, d, "call(1,")

	// Leaving the call: the server says there is none.
	hs.tt.Type(")")
	hs.settle()
	hs.until("a closed call", func() bool { return a.sig.ed == nil })
	hs.tt.Type(" ")
	hs.settle()
	if a.sig.ed != nil || told(hs, "No signature help here") {
		t.Fatal("a space outside a call asked, or told")
	}
}

// The command asks at the caret and tells when there is no call there,
// which a character typed does not.
func TestSignatureHelpCommand(t *testing.T) {
	hs := withFakeServer(t)
	a := hs.a
	signatureOf(hs, "name(")
	a.run("go.signatureHelp")
	hs.until("the card", func() bool { return a.sig.help != nil })
	if a.sig.help.Active != 1 {
		t.Errorf("active signature %d", a.sig.help.Active)
	}
	hs.key(0, ui.KeyEscape)

	d := a.activeEditor()
	hs.place(d, 0, 0)
	a.run("go.signatureHelp")
	hs.until("the note", func() bool { return told(hs, "No signature help here") })
	if a.sig.ed != nil {
		t.Fatal("help shows outside a call")
	}
}

// The card goes as a list opens, the window loses the keyboard, or the caret
// goes back before the call.
func TestSignatureHelpCloses(t *testing.T) {
	hs := withFakeServer(t)
	a := hs.a
	d := signatureOf(hs, "call(")
	a.run("go.signatureHelp")
	a.openPluginPicker([]pickItem{{Label: "x"}}, "", func(int) {})
	hs.settle()
	hs.settle()
	if a.sig.help != nil {
		t.Fatal("a list opened and the card shows")
	}
	a.closePluginPicker(-1)
	hs.settle()
	a.run("go.signatureHelp")
	hs.until("the card", func() bool { return a.sig.help != nil })
	// The window loses the keyboard.
	hs.tt.SetFocused(false)
	hs.settle()
	if a.sig.ed != nil {
		t.Fatal("the card stays as the window loses the keyboard")
	}
	hs.tt.SetFocused(true)
	hs.settle()

	// The caret goes back before the call.
	hs.place(d, 5, 5)
	hs.tt.Type("(")
	hs.until("the card", func() bool { return a.sig.help != nil })
	hs.place(d, 2, 2)
	hs.settle()
	if a.sig.ed != nil {
		t.Fatal("the card stays as the caret leaves the call")
	}
}
