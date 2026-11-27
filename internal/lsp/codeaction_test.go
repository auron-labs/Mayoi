package lsp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Code actions are asked with the diagnostics of the range, the server's
// quick fix names its edit by the client's paths, one without an edit is
// resolved, and a command that has the server ask for an edit applies it.
func TestCodeActions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "MAYOI_FAKE_LSP=1")
	published := make(chan struct{}, 4)
	edits := make(chan json.RawMessage, 1)
	c, err := Start(ctx, Options{Cmd: cmd, RootURI: "file:///server",
		PathMappings: []PathMapping{{ClientURI: "file:///work", ServerURI: "file:///server"}},
		Handlers: Handlers{
			Diagnostics: func(string, []Diagnostic) { published <- struct{}{} },
			ApplyEdit:   func(edit json.RawMessage) error { edits <- edit; return nil },
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !c.CanCodeAction() {
		t.Fatal("the code action capability was not read")
	}
	uri := "file:///work/a.fake"
	c.Open(uri, "fake", "good\nso bad\n")
	<-published

	bad := Range{Position{1, 3}, Position{1, 3}}
	diags := c.DiagnosticsIn(uri, bad)
	if len(diags) != 1 || diags[0].Message != "bad word" {
		t.Fatalf("diagnostics at the word = %+v", diags)
	}
	if got := c.DiagnosticsIn(uri, Range{Position{0, 0}, Position{0, 2}}); len(got) != 0 {
		t.Fatalf("diagnostics of another line = %+v", got)
	}

	actions, err := c.CodeActionsAt(ctx, uri, bad, diags)
	if err != nil || len(actions) != 4 {
		t.Fatalf("actions = %+v, %v", actions, err)
	}
	byTitle := map[string]CodeAction{}
	for _, a := range actions {
		byTitle[a.Title] = a
	}
	fix := byTitle["Fix bad word"]
	files, err := ParseEdit(fix.Edit)
	if !fix.IsPreferred || !fix.IsQuickFix() || err != nil || len(files) != 1 || files[0].URI != uri || files[0].Edits[0].NewText != "good" {
		t.Fatalf("quick fix = %+v, edit %+v, %v", fix, files, err)
	}
	if cmd := byTitle["Apply through the client"]; cmd.Edit != nil || cmd.Command == nil || cmd.Command.Command != "fake.applyEdit" {
		t.Fatalf("command = %+v", cmd)
	}
	if both := byTitle["Edit and command"]; both.Edit == nil || both.Command == nil || both.Command.Title != "Then" {
		t.Fatalf("action with edit and command = %+v", both)
	}

	extract, err := c.ResolveCodeAction(ctx, byTitle["Extract"])
	if err != nil || !strings.Contains(string(extract.Edit), "// resolved") || !strings.Contains(string(extract.Edit), uri) {
		t.Fatalf("resolved = %s, %v", extract.Edit, err)
	}

	if err := c.Execute(ctx, *byTitle["Apply through the client"].Command); err != nil {
		t.Fatal(err)
	}
	if edit := <-edits; !strings.Contains(string(edit), "// applied") || !strings.Contains(string(edit), uri) {
		t.Fatalf("edit the server asked for = %s", edit)
	}
}

func TestParseCodeAction(t *testing.T) {
	for _, c := range []struct {
		raw     string
		ok      bool
		command string
	}{
		{`{"title":"Run","command":"x.run","arguments":[1]}`, true, "x.run"},
		{`{"title":"Fix","kind":"quickfix","command":{"title":"Then","command":"x.then"}}`, true, "x.then"},
		{`{"title":"Fix","kind":"quickfix","edit":null}`, true, ""},
		{`{"title":"Later","disabled":{"reason":"not here"}}`, false, ""},
		{`{"kind":"quickfix"}`, false, ""},
	} {
		a, ok := parseCodeAction(json.RawMessage(c.raw))
		got := ""
		if a.Command != nil {
			got = a.Command.Command
		}
		if ok != c.ok || got != c.command || a.Edit != nil {
			t.Errorf("%s: %+v, %v", c.raw, a, ok)
		}
	}
}
