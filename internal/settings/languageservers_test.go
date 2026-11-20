package settings

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestValidLanguageServers(t *testing.T) {
	var list []LanguageServer
	err := json.Unmarshal([]byte(`[
		{"id": "ok", "command": "gopls", "languages": ["go"]},
		{"command": "x", "languages": ["go"]},
		{"id": "nocmd", "languages": ["go"]},
		{"id": "nothing", "command": "x"},
		{"id": "ok", "command": "other", "files": ["*.go"]},
		{"id": "glob", "command": "tf", "args": ["serve"], "files": ["*.tf"], "initializationOptions": {"a": 1}}
	]`), &list)
	if err != nil {
		t.Fatal(err)
	}
	valid, problems := ValidLanguageServers(list)
	var ids []string
	for _, s := range valid {
		ids = append(ids, s.ID)
	}
	if !slices.Equal(ids, []string{"ok", "glob"}) || valid[0].Command != "gopls" {
		t.Errorf("valid %q", ids)
	}
	want := []string{
		"languageServers[1] has no id",
		"languageServers[2] has no command",
		"languageServers[3] has neither languages nor files",
		`languageServers[4] repeats the id "ok"`,
	}
	if !slices.Equal(problems, want) {
		t.Errorf("problems %q", problems)
	}
	if string(valid[1].Options) != `{"a": 1}` {
		t.Errorf("options %s", valid[1].Options)
	}
}

// The list reads from, and writes back to, the settings' file as the
// window's other preferences do, and a folder can't choose it.
func TestLanguageServersPreference(t *testing.T) {
	p := Default().Prefs
	if err := p.Set("languageServers", json.RawMessage(`[{"id":"a","command":"c","files":["*.x"]}]`)); err != nil {
		t.Fatal(err)
	}
	if len(p.LanguageServers) != 1 || p.IsDefaultKey("languageServers") {
		t.Errorf("set: %+v", p.LanguageServers)
	}
	options := json.RawMessage(`{"a":{"initializationOptions":{"enabled":true}}}`)
	if err := p.Set("languageServerOptions", options); err != nil {
		t.Fatal(err)
	}
	if len(p.LanguageServers) != 1 || len(p.LanguageServerOptions) != 1 || p.IsDefaultKey("languageServerOptions") {
		t.Fatalf("servers: %+v, options: %+v", p.LanguageServers, p.LanguageServerOptions)
	}
	for _, key := range []string{"languageServers", "languageServerOptions"} {
		if f, _ := FieldOf(key); f.Folder {
			t.Errorf("a folder may choose %s", key)
		}
	}
}
