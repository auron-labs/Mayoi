package main

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// Show Code Actions lists what the server offers at the caret, its fix of
// the problem there first. A fix, an action resolved for its edit, a plain
// command that has the server ask for an edit, and an action of an edit and
// a command each change the document as the server says.
func TestCodeActions(t *testing.T) {
	hs := withFakeServer(t)
	a := hs.a
	path := hs.path("a.fake")
	os.WriteFile(path, []byte("so bad\n"), 0o644)
	a.openFile(path, true)
	d := a.activeEditor()
	serverUp(hs)
	hs.until("the problem", func() bool { return len(plugins.diagnosticsOf(path)) == 1 })

	offer := func() []string {
		t.Helper()
		hs.place(d, 4, 4) // in "bad", or after it
		a.run("edit.codeAction")
		hs.until("the actions offered", func() bool { return a.pluginPick.open })
		var labels []string
		for _, it := range a.pluginPick.items {
			labels = append(labels, it.Label)
		}
		return labels
	}
	choose := func(title string) {
		t.Helper()
		labels := offer()
		i := slices.Index(labels, title)
		if i < 0 {
			t.Fatalf("%q is not offered: %q", title, labels)
		}
		a.closePluginPicker(i)
	}
	text := func(want string) {
		t.Helper()
		hs.until("the text "+want, func() bool { return d.buf.String() == want })
	}

	if labels := offer(); len(labels) != 4 || labels[0] != "Fix bad word" {
		t.Fatalf("offered %q", labels)
	}
	a.closePluginPicker(0)
	text("so good\n")

	choose("Extract")
	text("// resolved\nso good\n")
	choose("Apply through the client")
	text("// applied\n// resolved\nso good\n")
	choose("Edit and command")
	text("// second\n// first\n// applied\n// resolved\nso good\n")
	if !strings.HasPrefix(d.buf.String(), "// second") {
		t.Fatal("the command's edit came before the action's")
	}
	// The fix came from the problem at the caret: with it gone, none is offered.
	for _, label := range offer() {
		if strings.HasPrefix(label, "Fix ") {
			t.Fatalf("a fix is offered where there is no problem: %q", label)
		}
	}
	a.closePluginPicker(-1)
}
