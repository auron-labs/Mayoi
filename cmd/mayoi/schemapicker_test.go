package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

// A file's schema is the person's, by the first of their globs that
// matches it, else the catalog's first of its language, as the servers
// match globs: from the start of a name, at any depth.
func TestFileSchema(t *testing.T) {
	entries, err := parseSchemaCatalog([]byte(testCatalog))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path, lang string
		own        map[string]string
		on         bool
		url        string
		chosen     bool
	}{
		{"web/package.json", "json", nil, true, "https://json.schemastore.org/package.json", false},
		{"package.json", "json", nil, false, "", false},
		{"xpackage.json", "json", nil, true, "", false},
		{".github/workflows/ci.yml", "yaml", nil, true, "https://json.schemastore.org/github-workflow.json", false},
		{"settings.yaml", "yaml", nil, true, "https://example.com/settings.json", false},
		{"settings.json", "yaml", nil, true, "", false},
		{"package.json", "json", map[string]string{"package.json": ""}, true, "", true},
		{"a/config/app.json", "json", map[string]string{"config/*.json": "https://example.com/app.json"}, true, "https://example.com/app.json", true},
	} {
		url, chosen := fileSchema(c.path, c.lang, c.own, entries, c.on)
		if url != c.url || chosen != c.chosen {
			t.Errorf("schema of %s (%s, %v) = %q, %v; want %q, %v", c.path, c.lang, c.own, url, chosen, c.url, c.chosen)
		}
	}
}

// schemaSettingsOf are the settings a running server was last sent.
func schemaSettingsOf(t *testing.T, s *lspServer) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw, err := s.live().Call(ctx, "test/snapshot", nil)
	if err != nil {
		t.Fatal(err)
	}
	var snap struct {
		Configuration struct {
			Settings json.RawMessage `json:"settings"`
		} `json:"configuration"`
	}
	json.Unmarshal(raw, &snap)
	return string(snap.Configuration.Settings)
}

// A JSON file whose server takes schemas shows the catalog's in the status
// bar, and its server is sent it; Choose Schema… lists the catalog, and
// the schema chosen is the file's by its path, which the server is sent at
// once, as is No Schema.
func TestChooseSchema(t *testing.T) {
	t.Setenv("MAYOI_FAKE_LSP", "1")
	file := filepath.Join(t.TempDir(), "catalog.json")
	os.WriteFile(file, []byte(testCatalog), 0o644)
	withSchemaCatalog(t, file, offlineCatalog)
	manifest, _ := json.Marshal(map[string]any{"name": "fake-json", "contributes": map[string]any{
		"languageServers": []any{map[string]any{"id": "fake-json.server", "languages": []string{"json"}, "schemas": "json",
			"command": os.Args[0], "args": []string{"-test.run=^$"}}},
	}})
	hs := newHarness(t, false)
	withPlugins(hs, map[string]map[string]string{"fake-json": {"plugin.json": string(manifest)}})
	t.Cleanup(stopServers)
	a := hs.a
	os.MkdirAll(hs.path("web"), 0o755)
	os.WriteFile(hs.path("web/package.json"), []byte("{}\n"), 0o644)
	a.openFile(hs.path("web/package.json"), true)
	var s *lspServer
	hs.until("the server", func() bool {
		if servers := a.lspRunningFor(a.activeEditor().document); len(servers) == 1 {
			s = servers[0]
		}
		return s != nil
	})
	settings := func() map[string]any {
		var v map[string]any
		json.Unmarshal([]byte(schemaSettingsOf(t, s)), &v)
		return v
	}
	schemas := func() []any { return settings()["json"].(map[string]any)["schemas"].([]any) }
	if got := len(schemas()); got != 2 {
		t.Fatalf("schemas sent = %d, want the catalog's of JSON files", got)
	}
	hs.until("the catalog's schema in the status bar", func() bool { return slices.Contains(hs.tt.Texts(), "package.json") })

	a.openSchemaPicker()
	hs.settle()
	pk := &a.pluginPick
	if !pk.open || len(pk.items) != 5 || pk.items[0].Label != "package.json" || pk.items[0].Icon != "check" ||
		pk.items[1].Label != "No Schema" {
		t.Fatalf("picker: open %v, items %+v", pk.open, pk.items)
	}
	// The catalog's GitHub Workflow, chosen, is the file's, and the server's.
	if pk.items[2].Label != "GitHub Workflow" {
		t.Fatalf("items = %+v", pk.items)
	}
	a.closePluginPicker(2)
	hs.settle()
	if want := map[string]string{"web/package.json": "https://json.schemastore.org/github-workflow.json"}; !reflect.DeepEqual(a.settings.FileSchemas, want) {
		t.Fatalf("fileSchemas = %v", a.settings.FileSchemas)
	}
	hs.until("the chosen schema sent", func() bool {
		list := schemas()
		last := list[len(list)-1].(map[string]any)
		return last["url"] == "https://json.schemastore.org/github-workflow.json"
	})
	if first := schemas()[0].(map[string]any)["fileMatch"].([]any); !slices.Contains(first, any("!web/package.json")) {
		t.Fatalf("the catalog's still match the file: %v", first)
	}
	hs.until("the chosen schema in the status bar", func() bool { return slices.Contains(hs.tt.Texts(), "GitHub Workflow") })

	a.openSchemaPicker()
	hs.settle()
	if pk.items[0].Label != "GitHub Workflow" || pk.items[1].Label != "No Schema" {
		t.Fatalf("items = %+v", pk.items)
	}
	a.closePluginPicker(1)
	hs.settle()
	if want := map[string]string{"web/package.json": ""}; !reflect.DeepEqual(a.settings.FileSchemas, want) {
		t.Fatalf("fileSchemas after No Schema = %v", a.settings.FileSchemas)
	}
	hs.until("no schema in the status bar", func() bool { return slices.Contains(hs.tt.Texts(), "No Schema") })
	hs.until("no schema sent", func() bool { return len(schemas()) == 2 })
}

// Choose Schema… says the catalog is on its way, once it is a while, and
// lists it as it comes; one that could not download says why and offers
// to try again, which lists it.
func TestChooseSchemaStates(t *testing.T) {
	t.Setenv("MAYOI_FAKE_LSP", "1")
	release := make(chan struct{})
	var fail atomic.Bool
	withSchemaCatalog(t, "", func(context.Context) ([]byte, error) {
		<-release
		if fail.Load() {
			return nil, errors.New("no network")
		}
		return []byte(testCatalog), nil
	})
	manifest, _ := json.Marshal(map[string]any{"name": "fake-yaml", "contributes": map[string]any{
		"languageServers": []any{map[string]any{"id": "fake-yaml.server", "languages": []string{"yaml"}, "schemas": "yaml",
			"command": os.Args[0], "args": []string{"-test.run=^$"}}},
	}})
	hs := newHarness(t, false)
	withPlugins(hs, map[string]map[string]string{"fake-yaml": {"plugin.json": string(manifest)}})
	t.Cleanup(stopServers)
	a := hs.a
	os.WriteFile(hs.path("ci.yaml"), []byte("on: push\n"), 0o644)
	a.openFile(hs.path("ci.yaml"), true)
	fail.Store(true)
	// Its server, as it starts, has the catalog downloaded.
	hs.until("the server", func() bool { return len(a.lspRunningFor(a.activeEditor().document)) == 1 })
	a.openSchemaPicker()
	step(hs.tt)
	pk := &a.pluginPick
	// No Schema is not offered for YAML without a choice of the person's.
	if !pk.open || len(pk.items) != 0 || pk.note != "Reading SchemaStore's schemas…" {
		t.Fatalf("picker: open %v, items %+v, note %q", pk.open, pk.items, pk.note)
	}
	if slices.Contains(hs.tt.Texts(), pk.note) {
		t.Fatal("says it reads as it opens")
	}
	for range 30 {
		step(hs.tt)
	}
	if !slices.Contains(hs.tt.Texts(), "Reading SchemaStore's schemas…") {
		t.Fatalf("does not say it reads: %q", hs.tt.Texts())
	}
	close(release)
	hs.until("the failure", func() bool { return pk.note == "Could not download SchemaStore's schemas: no network" })
	if len(pk.items) != 1 || pk.items[0].Label != "Try Again" {
		t.Fatalf("items after the failure: %+v", pk.items)
	}
	fail.Store(false)
	a.closePluginPicker(0)
	hs.until("the catalog listed", func() bool { return pk.open && len(pk.items) == 4 && pk.note == "" })
	// Chosen for a YAML file, it is the person's, and No Schema takes it away.
	a.closePluginPicker(2)
	hs.settle()
	if want := map[string]string{"ci.yaml": "https://example.com/settings.json"}; !reflect.DeepEqual(a.settings.FileSchemas, want) {
		t.Fatalf("fileSchemas = %v", a.settings.FileSchemas)
	}
	a.openSchemaPicker()
	hs.settle()
	if pk.items[0].Label != "Settings" || pk.items[1].Label != "No Schema" {
		t.Fatalf("items = %+v", pk.items)
	}
	a.closePluginPicker(1)
	hs.settle()
	if len(a.settings.FileSchemas) != 0 {
		t.Fatalf("fileSchemas = %v", a.settings.FileSchemas)
	}
}
