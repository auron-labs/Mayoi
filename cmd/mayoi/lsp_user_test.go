package main

import (
	"encoding/json"
	"os"
	"testing"

	"mayoi/internal/plugin"
	"mayoi/internal/settings"
)

func pluginWithServers(name string, servers ...plugin.ServerContrib) *plugin.Plugin {
	return &plugin.Plugin{Manifest: plugin.Manifest{Name: name, Contributes: plugin.Contributes{LanguageServers: servers}}}
}

func TestSelectServer(t *testing.T) {
	gopls := pluginWithServers("go", plugin.ServerContrib{ID: "gopls.server", Languages: []string{"go"}, Command: "gopls"})
	ts := pluginWithServers("ts", plugin.ServerContrib{ID: "ts.server", Languages: []string{"typescript", "go"}, Command: "tsserver"})
	enabled := []*plugin.Plugin{gopls, ts}
	own := func(id string, languages, files []string) settings.LanguageServer {
		return settings.LanguageServer{ID: id, Command: id + "-cmd", Languages: languages, Files: files}
	}
	cases := []struct {
		name       string
		own        []settings.LanguageServer
		lang, rel  string
		wantPlugin *plugin.Plugin
		want       string // the id, "" for none
	}{
		{"a plugin's server by language", nil, "go", "main.go", gopls, "gopls.server"},
		{"a plugin's server is by language only", nil, "", "main.go", nil, ""},
		{"no server", nil, "rust", "a.rs", nil, ""},
		{"your server wins over a plugin's for the same language", []settings.LanguageServer{own("mine", []string{"go"}, nil)}, "go", "main.go", nil, "mine"},
		{"the same id replaces a plugin's server for all its languages", []settings.LanguageServer{own("gopls.server", nil, []string{"*.txt"})}, "go", "main.go", ts, "ts.server"},
		{"the same id leaves the plugin's other server", []settings.LanguageServer{own("gopls.server", nil, []string{"*.txt"})}, "typescript", "a.ts", ts, "ts.server"},
		{"a name glob matches in any folder", []settings.LanguageServer{own("tf", nil, []string{"*.tf"})}, "", "infra/prod/main.tf", nil, "tf"},
		{"a glob with a slash matches from the folder", []settings.LanguageServer{own("tpl", nil, []string{"charts/**/templates/*.yaml"})}, "yaml", "charts/app/templates/a.yaml", nil, "tpl"},
		{"a glob with a slash does not match deeper than its start", []settings.LanguageServer{own("tpl", nil, []string{"charts/*.yaml"})}, "yaml", "other/charts/a.yaml", nil, ""},
		{"a glob wins over a plugin's server for the language", []settings.LanguageServer{own("gen", nil, []string{"gen/**"})}, "go", "gen/api.go", nil, "gen"},
		{"either languages or files select it", []settings.LanguageServer{own("both", []string{"toml"}, []string{"Makefile"})}, "make", "sub/Makefile", nil, "both"},
		{"a path outside the folder is by language only", []settings.LanguageServer{own("both", []string{"toml"}, []string{"**/*.tf"})}, "", "", nil, ""},
		{"the first of yours that matches wins", []settings.LanguageServer{own("first", nil, []string{"*.go"}), own("second", []string{"go"}, nil)}, "go", "main.go", nil, "first"},
		{"the later one serves what the first does not", []settings.LanguageServer{own("first", nil, []string{"*.rs"}), own("second", []string{"go"}, nil)}, "go", "main.go", nil, "second"},
	}
	for _, c := range cases {
		p, sc := selectServer(c.own, enabled, c.lang, c.rel)
		got := ""
		if sc != nil {
			got = sc.ID
		}
		if got != c.want || p != c.wantPlugin {
			t.Errorf("%s: got %q from %v, want %q from %v", c.name, got, p, c.want, c.wantPlugin)
		}
	}
}

// A document's path from its folder is what a glob matches, with slashes,
// for a folder on an SSH host too.
func TestServerForPathsFromTheFolder(t *testing.T) {
	hs := newHarness(t, false)
	a := hs.a
	a.settings.LanguageServers = []settings.LanguageServer{
		{ID: "tpl", Command: "tpl", Files: []string{"charts/**/templates/*.yaml"}},
	}
	for _, root := range []string{hs.root, "/ssh:host/work/proj", "/wsl:Ubuntu/home/me/proj"} {
		a.root = root
		in := &document{path: root + "/charts/app/templates/a.yaml"}
		out := &document{path: root + "/other/a.yaml"}
		beside := &document{path: root + "-other/charts/app/templates/a.yaml"}
		if _, sc := a.serverFor(in); sc == nil || sc.ID != "tpl" {
			t.Errorf("%s: no server for a file in the folder", root)
		}
		if _, sc := a.serverFor(out); sc != nil {
			t.Errorf("%s: a server for %s", root, out.path)
		}
		if _, sc := a.serverFor(beside); sc != nil {
			t.Errorf("%s: a server for a file of a folder beside it", root)
		}
	}
}

// Your server of a plugin's id, chosen by glob, runs in the plugin's place,
// and starts again when the list changes.
func TestUserLanguageServerReplacesAPlugin(t *testing.T) {
	t.Setenv("MAYOI_FAKE_LSP", "1")
	manifest, _ := json.Marshal(map[string]any{
		"name": "fake",
		"contributes": map[string]any{
			"languages":       []any{map[string]any{"id": "fake", "extensions": []string{".fake"}}},
			"languageServers": []any{map[string]any{"id": "fake.server", "languages": []string{"fake"}, "command": "mayoi-no-such-server"}},
		},
	})
	hs := newHarness(t, false)
	h := withPlugins(hs, map[string]map[string]string{"fake": {"plugin.json": string(manifest)}})
	t.Cleanup(stopServers)
	a := hs.a
	a.settings.LanguageServers = []settings.LanguageServer{
		{ID: "fake.server", Command: os.Args[0], Args: []string{"-test.run=^$"}, Files: []string{"*.fake"}},
	}
	path := hs.path("a.fake")
	os.WriteFile(path, []byte("fine\nso bad\n"), 0o644)
	a.openFile(path, true)
	hs.until("the server's diagnostics", func() bool { return len(h.diagnosticsOf(path)) == 1 })
	server := func() *lspServer {
		lspServers.Lock()
		defer lspServers.Unlock()
		return lspServers.m[a.root+"\x00fake.server"]
	}
	old := server()
	if old.plugin != nil {
		t.Fatal("the plugin's server runs")
	}

	h.restartServers()
	hs.until("the restarted server", func() bool {
		s := server()
		return s != nil && s != old && s.state() == lspRunning
	})
}
