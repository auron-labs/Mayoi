package main

import (
	"os"
	"testing"

	"github.com/egoist/mygo/ui"

	"mayoi/internal/highlight"
)

// pickedLabel is the label of the row chosen in the pick box.
func pickedLabel(a *app) string {
	pk := &a.pluginPick
	return pk.items[pk.results[pk.sel].Index].Label
}

// The language chosen in the box is the file's, for its highlighting and
// its comments, and Auto Detect gives it its own again.
func TestLanguagePickerSetsTheFilesLanguage(t *testing.T) {
	hs := newHarness(t, true)
	a := hs.a
	a.openFile(hs.path("main.go"), true)
	hs.settle()
	d := a.activeEditor().document
	a.run("view.languageMode")
	hs.settle()
	if !a.pluginPick.open || pickedLabel(a) != "Go" {
		t.Fatalf("the box is not open on the file's language: open %v", a.pluginPick.open)
	}
	hs.tt.Type("YAML")
	hs.settle()
	hs.key(0, ui.KeyEnter)
	hs.settle()
	if a.pluginPick.open || languageOf(d) != "yaml" || highlight.DisplayName(d.hl.Lexer()) != "YAML" || commentFor(d) != "#" {
		t.Fatalf("the file is %q, highlighted as %q, comments %q", languageOf(d), highlight.DisplayName(d.hl.Lexer()), commentFor(d))
	}

	a.run("view.languageMode")
	hs.settle()
	if pickedLabel(a) != "YAML" {
		t.Fatalf("the box opens on %q, not the language chosen", pickedLabel(a))
	}
	hs.tt.Type("Auto Detect")
	hs.settle()
	hs.key(0, ui.KeyEnter)
	hs.settle()
	if d.language != "" || languageOf(d) != "go" || commentFor(d) != "//" {
		t.Fatalf("Auto Detect leaves %q, %q", d.language, languageOf(d))
	}
}

// A file association chosen in the box goes to the settings, and every
// open file it matches takes its language at once.
func TestLanguagePickerAssociatesTheExtension(t *testing.T) {
	t.Cleanup(func() { highlight.SetAssociations(nil) })
	hs := newHarness(t, true)
	a := hs.a
	a.openFile(hs.path("internal/a/a.go"), true)
	hs.settle()
	other := a.activeEditor().document
	a.openFile(hs.path("main.go"), true)
	hs.settle()
	d := a.activeEditor().document
	a.setDocumentLanguage(d, nil, "json")

	a.run("view.languageMode")
	hs.settle()
	hs.tt.Type("Configure File Association")
	hs.settle()
	hs.key(0, ui.KeyEnter)
	hs.settle()
	if !a.pluginPick.open || pickedLabel(a) != "JSON" {
		t.Fatalf("no box of the languages for *.go: open %v", a.pluginPick.open)
	}
	hs.tt.Type("YAML")
	hs.settle()
	hs.key(0, ui.KeyEnter)
	hs.settle()
	if got := a.settings.FileAssociations["*.go"]; got != "yaml" {
		t.Fatalf("the settings associate *.go with %q", got)
	}
	for _, doc := range []*document{d, other} {
		if languageOf(doc) != "yaml" || highlight.DisplayName(doc.hl.Lexer()) != "YAML" {
			t.Errorf("%s is %q after the association", doc.name(), languageOf(doc))
		}
	}
}

// A file whose extension no language has offers its name too.
func TestAssociationGlobs(t *testing.T) {
	cases := map[string][]string{
		"/p/main.go":        {"*.go"},
		"/p/Jenkinsfile":    {"Jenkinsfile"},
		"/p/Dockerfile.dev": {"*.dev", "Dockerfile.dev"},
		"/p/.envrc":         {".envrc"},
	}
	for path, want := range cases {
		got := associationGlobs(path)
		if len(got) != len(want) || got[0] != want[0] || len(got) > 1 && got[1] != want[1] {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
}

// A language chosen for a file has its language server take it, and the
// server of the language before let it go.
func TestLanguagePickerMovesTheServer(t *testing.T) {
	t.Setenv("MAYOI_FAKE_LSP", "1")
	hs := newHarness(t, false)
	h := fakeServers(hs, map[string]string{"fake": os.Args[0]})
	a := hs.a
	path := hs.path("a.txt")
	os.WriteFile(path, []byte("so bad\n"), 0o644)
	a.openFile(path, true)
	hs.settle()
	a.run("view.languageMode")
	hs.settle()
	hs.tt.Type("fake")
	hs.settle()
	if pickedLabel(a) != "fake" {
		t.Fatalf("the plugin's language is not offered: %q", pickedLabel(a))
	}
	hs.key(0, ui.KeyEnter)
	hs.until("the server's diagnostics", func() bool { return len(h.diagnosticsOf(path)) == 1 })

	a.run("view.languageMode")
	hs.settle()
	hs.tt.Type("Auto Detect")
	hs.settle()
	hs.key(0, ui.KeyEnter)
	hs.settle()
	if c := serverOf(serverKey(a.root, "fake.fake")).live(); c == nil || c.IsOpen(serverURI(path)) {
		t.Fatal("the server still has the file as its language's")
	}
}
