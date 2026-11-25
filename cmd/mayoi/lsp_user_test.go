package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"mayoi/internal/plugin"
	"mayoi/internal/settings"
)

func pluginWithServers(name string, servers ...plugin.ServerContrib) *plugin.Plugin {
	return &plugin.Plugin{Manifest: plugin.Manifest{Name: name, Contributes: plugin.Contributes{LanguageServers: servers}}}
}

// ids are the ids of servers, in their order.
func ids(servers []docServer) []string {
	out := []string{}
	for _, s := range servers {
		out = append(out, s.contrib.ID)
	}
	return out
}

func TestSelectServers(t *testing.T) {
	gopls := pluginWithServers("go", plugin.ServerContrib{ID: "gopls.server", Languages: []string{"go"}, Command: "gopls"})
	ts := pluginWithServers("ts", plugin.ServerContrib{ID: "ts.server", Languages: []string{"typescript", "go"}, Command: "tsserver"})
	enabled := []*plugin.Plugin{gopls, ts}
	own := func(id string, languages, files []string) settings.LanguageServer {
		return settings.LanguageServer{ID: id, Command: id + "-cmd", Languages: languages, Files: files}
	}
	exclusive := func(s settings.LanguageServer) settings.LanguageServer {
		s.Exclusive = true
		return s
	}
	cases := []struct {
		name      string
		own       []settings.LanguageServer
		lang, rel string
		want      string // the ids, in their order
	}{
		{"the plugins' servers by language, in the plugins' order", nil, "go", "main.go", "gopls.server ts.server"},
		{"a plugin's server is by language only", nil, "", "main.go", ""},
		{"no server", nil, "rust", "a.rs", ""},
		{"yours comes before the plugins'", []settings.LanguageServer{own("mine", []string{"go"}, nil)}, "go", "main.go", "mine gopls.server ts.server"},
		{"the same id replaces a plugin's server for all its languages", []settings.LanguageServer{own("gopls.server", nil, []string{"*.txt"})}, "go", "main.go", "ts.server"},
		{"the same id leaves the plugin's other server", []settings.LanguageServer{own("gopls.server", nil, []string{"*.txt"})}, "typescript", "a.ts", "ts.server"},
		{"a name glob matches in any folder", []settings.LanguageServer{own("tf", nil, []string{"*.tf"})}, "", "infra/prod/main.tf", "tf"},
		{"a glob with a slash matches from the folder", []settings.LanguageServer{own("tpl", nil, []string{"charts/**/templates/*.yaml"})}, "yaml", "charts/app/templates/a.yaml", "tpl"},
		{"a glob with a slash does not match deeper than its start", []settings.LanguageServer{own("tpl", nil, []string{"charts/*.yaml"})}, "yaml", "other/charts/a.yaml", ""},
		{"a glob serves beside the plugins' servers of the language", []settings.LanguageServer{own("gen", nil, []string{"gen/**"})}, "go", "gen/api.go", "gen gopls.server ts.server"},
		{"either languages or files select it", []settings.LanguageServer{own("both", []string{"toml"}, []string{"Makefile"})}, "make", "sub/Makefile", "both"},
		{"a path outside the folder is by language only", []settings.LanguageServer{own("both", []string{"toml"}, []string{"**/*.tf"})}, "", "", ""},
		{"each of yours that serves it, in their order", []settings.LanguageServer{own("first", nil, []string{"*.go"}), own("second", []string{"go"}, nil)}, "go", "main.go", "first second gopls.server ts.server"},
		{"an exclusive server keeps the others from what it serves", []settings.LanguageServer{exclusive(own("gen", nil, []string{"gen/**"}))}, "go", "gen/api.go", "gen"},
		{"and leaves them the rest", []settings.LanguageServer{exclusive(own("gen", nil, []string{"gen/**"}))}, "go", "main.go", "gopls.server ts.server"},
		{"exclusive servers share", []settings.LanguageServer{exclusive(own("a", []string{"go"}, nil)), own("b", []string{"go"}, nil), exclusive(own("c", []string{"go"}, nil))}, "go", "main.go", "a c"},
	}
	for _, c := range cases {
		if got := strings.Join(ids(selectServers(c.own, enabled, c.lang, c.rel)), " "); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	// A plugin's server is of its plugin, and yours of none.
	got := selectServers([]settings.LanguageServer{own("mine", []string{"go"}, nil)}, enabled, "go", "main.go")
	if got[0].plugin != nil || got[1].plugin != gopls || got[2].plugin != ts {
		t.Errorf("plugins %v %v %v", got[0].plugin, got[1].plugin, got[2].plugin)
	}
}

// A plugin's server of files, as gonsible's of Ansible's, serves them
// before another plugin's of their language, with its files in a setting;
// an exclusive one serves them alone.
func TestSelectServersPluginFiles(t *testing.T) {
	yaml := pluginWithServers("yaml", plugin.ServerContrib{ID: "yaml.server", Languages: []string{"yaml"}, Command: "rlsp-yaml"})
	ansible := pluginWithServers("ansible", plugin.ServerContrib{ID: "ansible.server", Files: []string{"${settings.files}"}, Command: "gonsible"})
	ansible.Contributes.Settings = map[string]plugin.SettingContrib{
		"files": {Type: "array", Default: json.RawMessage(`["**/roles/*/tasks/**/*.{yml,yaml}"]`)},
	}
	enabled := []*plugin.Plugin{yaml, ansible}
	cases := []struct {
		name      string
		own       []settings.LanguageServer
		exclusive bool
		rel, want string
	}{
		{"its files, before the language's", nil, false, "roles/web/tasks/main.yml", "ansible.server yaml.server"},
		{"its files, alone when exclusive", nil, true, "roles/web/tasks/main.yml", "ansible.server"},
		{"its files in a folder below", nil, true, "infra/roles/web/tasks/sub/a.yaml", "ansible.server"},
		{"other files of the language", nil, true, ".github/workflows/ci.yml", "yaml.server"},
		{"a path outside the folder", nil, true, "", "yaml.server"},
		{"your server of its id", []settings.LanguageServer{{ID: "ansible.server", Command: "x", Files: []string{"*.txt"}}}, true, "roles/web/tasks/main.yml", "yaml.server"},
	}
	for _, c := range cases {
		ansible.Contributes.LanguageServers[0].Exclusive = c.exclusive
		if got := strings.Join(ids(selectServers(c.own, enabled, "yaml", c.rel)), " "); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
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
		if got := ids(a.serversFor(in)); len(got) != 1 || got[0] != "tpl" {
			t.Errorf("%s: no server for a file in the folder", root)
		}
		if len(a.serversFor(out)) != 0 {
			t.Errorf("%s: a server for %s", root, out.path)
		}
		if len(a.serversFor(beside)) != 0 {
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
